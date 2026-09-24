package background

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workshop-agent/internal/integrations/mcpclient"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/workshops"
)

type fakeMCP struct {
	price              int64
	stock              int64
	priceErr, stockErr error
	calls              []string
	block              chan struct{}
	entered            chan struct{}
}

func (f *fakeMCP) Prices(ctx context.Context, _ string) (mcpclient.Envelope[[]wb.Price], error) {
	f.calls = append(f.calls, "prices")
	if f.entered != nil {
		close(f.entered)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return mcpclient.Envelope[[]wb.Price]{}, ctx.Err()
		}
	}
	return mcpclient.Envelope[[]wb.Price]{Source: "wildberries", FetchedAt: time.Now().UTC().Format(time.RFC3339), Complete: true, UntrustedData: true, Data: []wb.Price{{NmID: 1, SizeID: 2, Currency: "RUB", PriceCents: f.price}}}, f.priceErr
}
func (f *fakeMCP) Stocks(context.Context, string) (mcpclient.StocksResult, error) {
	f.calls = append(f.calls, "stocks")
	return mcpclient.StocksResult{Value: mcpclient.Envelope[[]wb.Stock]{Source: "wildberries", FetchedAt: time.Now().UTC().Format(time.RFC3339), Complete: true, UntrustedData: true, Data: []wb.Stock{{NmID: 1, ChrtID: 2, WarehouseID: 3, Quantity: f.stock}, {NmID: 2, ChrtID: 4, WarehouseID: 3, Quantity: 0}}}}, f.stockErr
}

type fakeSender struct{ messages []string }

func (f *fakeSender) Send(_ context.Context, _, _ int64, text string) error {
	f.messages = append(f.messages, text)
	return nil
}
func fixture(t *testing.T) (*Service, *workshops.Service, *fakeMCP, *fakeSender, int64, int64) {
	t.Helper()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "fixture.db"))
	t.Cleanup(func() { ws.Close() })
	u, e := ws.UpsertUser(991801, "", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "Day18")
	if e != nil {
		t.Fatal(e)
	}
	_, e = ws.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'fixture')`, w)
	if e != nil {
		t.Fatal(e)
	}
	m := &fakeMCP{price: 10000, stock: 12}
	sender := &fakeSender{}
	s := New(ws.DB(), m, sender)
	s.Now = func() time.Time { return time.Date(2026, 9, 24, 6, 0, 0, 0, time.UTC) }
	return s, ws, m, sender, u, w
}
func scalar(t *testing.T, s *Service, q string) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow(q).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestDailyScheduleTimezoneDST(t *testing.T) {
	for _, tc := range []struct{ now, zone, want string }{
		{"2026-09-24T04:00:00Z", "Europe/Moscow", "2026-09-24T05:00:00Z"},
		{"2026-09-24T06:00:00Z", "Europe/Moscow", "2026-09-25T05:00:00Z"},
		{"2026-03-07T14:00:00Z", "America/New_York", "2026-03-08T12:00:00Z"},
		{"2026-10-31T14:00:00Z", "America/New_York", "2026-11-01T13:00:00Z"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		next, e := Next(now, "08:00", tc.zone, false)
		if e != nil || next.Format(time.RFC3339) != tc.want {
			t.Fatal(tc, next, e)
		}
	}
	if _, e := Next(time.Now(), "25:00", "Europe/Moscow", false); e == nil {
		t.Fatal("invalid time")
	}
	if _, e := Next(time.Now(), "08:00", "Local", false); e == nil {
		t.Fatal("server local timezone")
	}
}

func TestRestartBeforeMorningDoesNotRunEarly(t *testing.T) {
	s, _, m, _, u, w := fixture(t)
	_, _ = s.Create(u, w)
	s.Now = func() time.Time { return time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC) }
	if e := s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	j, _ := s.Get(u, w)
	if len(m.calls) != 0 || time.Unix(j.NextRun, 0).UTC().Hour() != 5 {
		t.Fatal("early catch-up", j)
	}
}

func TestRevocationDuringNetworkSuppressesSnapshotsAndNotification(t *testing.T) {
	s, _, m, sender, u, w := fixture(t)
	_, _ = s.Create(u, w)
	m.entered = make(chan struct{})
	m.block = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.RunNow(context.Background(), u, w) }()
	<-m.entered
	if _, e := s.DB.Exec(`UPDATE workshop_members SET is_active=0 WHERE user_id=? AND workshop_id=?`, u, w); e != nil {
		t.Fatal(e)
	}
	close(m.block)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT COUNT(*) FROM wb_daily_snapshots`) != 0 || len(sender.messages) != 0 || len(m.calls) != 1 {
		t.Fatal("revoked execution leaked data")
	}
}

func TestExpiredLeaseRecoveryAndMissingRowsAreNotZero(t *testing.T) {
	s, _, _, _, u, w := fixture(t)
	j, _ := s.Create(u, w)
	_, e := s.DB.Exec(`INSERT INTO background_job_runs(job_id,workshop_id,job_type,local_date,status,started_at,lease_owner,lease_until) VALUES(?,?,'WB_DAILY_SYNC','2026-09-23','running',1,'dead-worker',2)`, j.ID, w)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT COUNT(*) FROM background_job_runs WHERE error_code='interrupted'`) != 1 || scalar(t, s, `SELECT COUNT(*) FROM background_job_runs WHERE status='success'`) != 1 {
		t.Fatal("lease recovery")
	}
	var value string
	if e = s.DB.QueryRow(`SELECT data_json FROM wb_daily_current WHERE source_tool='wb_get_wb_stocks' AND nm_id=1`).Scan(&value); e != nil {
		t.Fatal(e)
	}
	// A failed next source must not wipe previously successful current rows.
	s.WB.MCP = &fakeMCP{price: 10000, stockErr: mcpclient.Error("wb_rate_limit")}
	s.Now = func() time.Time { return time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC) }
	_ = s.Tick(context.Background())
	var after string
	_ = s.DB.QueryRow(`SELECT data_json FROM wb_daily_current WHERE source_tool='wb_get_wb_stocks' AND nm_id=1`).Scan(&after)
	if after != value {
		t.Fatal("old stock wiped")
	}
}
func TestSnapshotsDiffAggregateAndInventoryIsolation(t *testing.T) {
	s, _, m, sender, u, w := fixture(t)
	j, e := s.Create(u, w)
	if e != nil || j.LocalTime != "08:00" || j.Timezone != "Europe/Moscow" || j.Schedule != "DAILY_AT_TIME" {
		t.Fatal(j, e)
	}
	before := scalar(t, s, `SELECT COUNT(*) FROM products`)
	if _, e = s.DB.Exec(`INSERT INTO materials(workshop_id,name,category,base_unit,current_stock) VALUES(?,'Workshop material','raw','pcs',400)`, w); e != nil {
		t.Fatal(e)
	}
	// Internal cost does not currently have a production column. A sentinel table
	// verifies the executor touches only its integration tables even when one exists.
	if _, e = s.DB.Exec(`CREATE TABLE fixture_internal_cost(product_id INTEGER,cost_cents INTEGER)`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`INSERT INTO fixture_internal_cost VALUES(1,99999)`); e != nil {
		t.Fatal(e)
	}
	if e = s.RunNow(context.Background(), u, w); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT COUNT(*) FROM wb_daily_snapshots`) != 3 || scalar(t, s, `SELECT COUNT(*) FROM wb_daily_current`) != 3 {
		t.Fatal("snapshots/current missing")
	}
	if scalar(t, s, `SELECT COUNT(*) FROM wb_daily_diffs`) != 0 {
		t.Fatal("baseline counted as change")
	}
	if e = s.RunNow(context.Background(), u, w); !errors.Is(e, ErrBusy) {
		t.Fatal("duplicate", e)
	}
	s.Now = func() time.Time { return time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC) }
	m.price = 12000
	m.stock = 3
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	var raw string
	if e = s.DB.QueryRow(`SELECT aggregate_json FROM background_job_runs ORDER BY id DESC LIMIT 1`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var a Aggregate
	_ = json.Unmarshal([]byte(raw), &a)
	if a.PriceChanges != 1 || a.StockChanges != 1 || a.ZeroStock != 1 || a.LowStock != 1 || a.ProductsCount != 2 || a.Errors != 0 {
		t.Fatal(a)
	}
	if scalar(t, s, `SELECT COUNT(*) FROM wb_daily_snapshots`) != 6 || scalar(t, s, `SELECT COUNT(*) FROM wb_daily_diffs`) != 2 || scalar(t, s, `SELECT COUNT(*) FROM products`) != before {
		t.Fatal("history or inventory changed")
	}
	if len(sender.messages) != 2 || !strings.Contains(sender.messages[1], "изменилось — 1") || strings.Join(m.calls, ",") != "prices,stocks,prices,stocks" {
		t.Fatal(sender.messages, m.calls)
	}
	if scalar(t, s, `SELECT current_stock FROM materials WHERE name='Workshop material'`) != 400 || scalar(t, s, `SELECT cost_cents FROM fixture_internal_cost`) != 99999 {
		t.Fatal("production data overwritten")
	}
}
func TestPartialFailureAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		price, stock bool
		want         string
		n            int
	}{{false, true, "partial_success", 1}, {true, false, "partial_success", 2}, {true, true, "failed", 0}} {
		t.Run(tc.want+string(rune('0'+tc.n)), func(t *testing.T) {
			s, _, m, sender, u, w := fixture(t)
			if tc.price {
				m.priceErr = errors.New("SECRET")
			}
			if tc.stock {
				m.stockErr = mcpclient.Error("wb_authentication_error")
			}
			_, _ = s.Create(u, w)
			if e := s.RunNow(context.Background(), u, w); e != nil {
				t.Fatal(e)
			}
			j, _ := s.Get(u, w)
			if j.LastResult != tc.want || j.Status != "active" || j.LastSuccess != 0 || scalar(t, s, `SELECT COUNT(*) FROM wb_daily_snapshots`) != tc.n {
				t.Fatal(j)
			}
			var raw string
			_ = s.DB.QueryRow(`SELECT result_json FROM background_job_runs`).Scan(&raw)
			if strings.Contains(raw, "SECRET") || strings.Contains(sender.messages[0], "SECRET") {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestPauseResumeCancelRestartAndCatchup(t *testing.T) {
	s, _, m, _, u, w := fixture(t)
	_, _ = s.Create(u, w)
	if e := s.Change(u, w, "pause", "", "", 0); e != nil {
		t.Fatal(e)
	}
	_ = s.Tick(context.Background())
	if len(m.calls) != 0 {
		t.Fatal("paused execution")
	}
	if e := s.Change(u, w, "resume", "", "", 0); e != nil {
		t.Fatal(e)
	}
	restarted := New(s.DB, m, nil)
	restarted.Now = func() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) }
	_ = restarted.Tick(context.Background())
	_ = restarted.Tick(context.Background())
	if scalar(t, s, `SELECT COUNT(*) FROM background_job_runs`) != 1 {
		t.Fatal("catch-up repeated missed days")
	}
	if e := restarted.Change(u, w, "cancel", "", "", 0); e != nil {
		t.Fatal(e)
	}
	restarted.Now = func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) }
	_ = restarted.Tick(context.Background())
	if scalar(t, s, `SELECT COUNT(*) FROM background_job_runs`) != 1 {
		t.Fatal("cancel ignored")
	}
}
func TestOverlapManualScheduledAndScope(t *testing.T) {
	s, ws, m, _, u, w := fixture(t)
	_, _ = s.Create(u, w)
	m.block = make(chan struct{})
	m.entered = make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if e := s.RunNow(context.Background(), u, w); e != nil {
			t.Error(e)
		}
	}()
	<-m.entered
	if e := s.RunNow(context.Background(), u, w); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	_ = s.Tick(context.Background())
	close(m.block)
	wg.Wait()
	if len(m.calls) != 2 {
		t.Fatal("overlap", m.calls)
	}
	other, e := ws.CreateOwnedWorkshop(u, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(u, other); e == nil {
		t.Fatal("foreign cabinet")
	}
	if _, e = s.Create(u+999, w); e == nil {
		t.Fatal("foreign user")
	}
	// Generic JSON storage supports different executors; each executor validates
	// its typed parameters before persistence, rejecting tools and credentials.
	if e = ws.SetActiveWorkshop(u, w); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateJob(u, w, CreateInput{Type: JobType, Parameters: `{"tool":"write_stock","token":"SECRET"}`}); e == nil {
		t.Fatal("unsafe job parameters accepted")
	}
	var params string
	if e = s.DB.QueryRow(`SELECT parameters_json FROM background_jobs WHERE workshop_id=?`, w).Scan(&params); e != nil || strings.Contains(params, "SECRET") {
		t.Fatal("secret persisted", e)
	}
}
