package mcpmanager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/background"
	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/integrations/wbmcpfixture"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/workshops"
)

func pipelineFixture(t *testing.T) (*background.Service, *Manager, *countedAPI, int64, int64) {
	t.Helper()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "jobs.db"))
	t.Cleanup(func() { ws.Close() })
	u, e := ws.UpsertUser(991919, "", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "Pipeline")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'day17-fixture')`, w); e != nil {
		t.Fatal(e)
	}
	api := &countedAPI{API: wbmcpfixture.API{Mode: "success"}}
	jobs := background.New(ws.DB(), nil, nil)
	jobs.Now = func() time.Time { return time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC) }
	m, e := New(context.Background(), api, jobs)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	jobs.WB.MCP = m.Client
	if _, e = jobs.Create(u, w); e != nil {
		t.Fatal(e)
	}
	return jobs, m, api, u, w
}
func pipelineLast(t *testing.T, j *background.Service, u, w int64) background.PipelineResult {
	t.Helper()
	job, e := j.Get(u, w)
	if e != nil {
		t.Fatal(e)
	}
	run, e := j.LastRun(w, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	var p background.PipelineResult
	if e = json.Unmarshal([]byte(run.ResultJSON), &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func pipelineCount(t *testing.T, j *background.Service, table string) int {
	t.Helper()
	var n int
	if e := j.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestPipelineRealMCPDataTransferScheduledAndManual(t *testing.T) {
	j, m, api, u, w := pipelineFixture(t)
	ctx := context.Background()
	names := []string{mcpclient.PricesTool, mcpclient.SellerStocksTool, mcpclient.StocksTool, background.BuildTool, background.SaveTool}
	discovered := map[string]bool{}
	for _, tool := range m.Client.State().Tools {
		discovered[tool.Name] = true
	}
	for _, name := range names {
		if !discovered[name] {
			t.Fatal("missing real ListTools entry", name)
		}
	}
	if e := j.RunNow(ctx, u, w); e != nil {
		t.Fatal(e)
	}
	first := pipelineLast(t, j, u, w)
	if first.Status != "SUCCESS" {
		t.Fatalf("%+v", first)
	}
	if first.Aggregate.ProductsCount != 3 || first.SaveResult.PriceRecords != 1 || first.SaveResult.StockRecords != 3 {
		t.Fatal(first)
	}
	for i, st := range first.Steps {
		if st.Tool != names[i] || st.Status != "SUCCESS" || st.WBWrite || st.Required != (i >= 3) {
			t.Fatal(st)
		}
		if i > 0 && st.StartedAt < first.Steps[i-1].FinishedAt {
			t.Fatal("step ordering")
		}
	}
	if first.Steps[3].Input.Prices != 1 || first.Steps[3].Input.Stocks != 3 || !reflect.DeepEqual(*first.Steps[3].Output.Aggregate, *first.Aggregate) || !reflect.DeepEqual(first.Steps[4].Input.Aggregate, first.Aggregate) {
		t.Fatal("typed data lost", first)
	}
	// Changed fixture tests values, not just invocations/counts.
	api.Mode = "changed"
	j.Now = func() time.Time { return time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC) }
	if e := j.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	second := pipelineLast(t, j, u, w)
	if second.Status != "SUCCESS" || second.Aggregate.PriceChanges != 1 || second.Aggregate.StockChanges != 1 || second.Aggregate.LowStock != 2 || second.Aggregate.ZeroStock != 1 {
		t.Fatalf("%+v", second)
	}
	var raw string
	var price wb.Price
	var stock wb.Stock
	if e := j.DB.QueryRow(`SELECT data_json FROM wb_daily_current WHERE source_tool=? AND nm_id=101`, mcpclient.PricesTool).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if json.Unmarshal([]byte(raw), &price) != nil || price.PriceCents != 12000 {
		t.Fatal(raw)
	}
	if e := j.DB.QueryRow(`SELECT data_json FROM wb_daily_current WHERE source_tool=? AND nm_id=101`, mcpclient.StocksTool).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if json.Unmarshal([]byte(raw), &stock) != nil || stock.Quantity != 3 {
		t.Fatal(raw)
	}
	before := api.prices.Load() + api.stocks.Load() + api.sellers.Load()
	summary, e := m.DailySummary(ctx, u, w)
	if e != nil || summary.Aggregate == nil || !reflect.DeepEqual(*summary.Aggregate, *second.Aggregate) || before != api.prices.Load()+api.stocks.Load()+api.sellers.Load() {
		t.Fatal("stored summary", summary, e)
	}
	job, _ := j.Get(u, w)
	if job.LocalTime != "08:00" {
		t.Fatal("schedule changed")
	}
	trace, e := j.WBTrace(u, w)
	if e != nil || !strings.Contains(trace, background.MarketPipeline) {
		t.Fatal(trace, e)
	}
	if _, e = j.WBTrace(u+999, w); e == nil {
		t.Fatal("foreign trace")
	}
}
func TestPipelineRealMCPStrictFailuresAndAtomicStorage(t *testing.T) {
	for _, fail := range []int{0, 1, 2, 3} {
		t.Run([]string{"prices", "stocks", "summary", "save"}[fail], func(t *testing.T) {
			j, _, api, u, w := pipelineFixture(t)
			switch fail {
			case 0:
				api.Mode = "price_error"
			case 1:
				api.Mode = "api"
			case 2:
				_, e := j.DB.Exec(`DROP TABLE wb_daily_current`)
				if e != nil {
					t.Fatal(e)
				}
			case 3:
				_, e := j.DB.Exec(`CREATE TRIGGER reject_stock BEFORE INSERT ON wb_daily_snapshots WHEN NEW.source_tool='wb_get_wb_stocks' BEGIN SELECT RAISE(ABORT,'synthetic-secret'); END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e := j.RunNow(context.Background(), u, w); e != nil {
				t.Fatal(e)
			}
			p := pipelineLast(t, j, u, w)
			if fail < 2 {
				if p.Status != "PARTIAL_SUCCESS" || p.SaveResult == nil || p.Steps[3].Status != "SUCCESS" || p.Steps[4].Status != "SUCCESS" {
					t.Fatal(p.Status, p.Error)
				}
				want := 4
				if fail == 1 {
					want = 2
				}
				if pipelineCount(t, j, "wb_daily_snapshots") != want {
					t.Fatal("available source lost")
				}
			} else {
				if p.Status != "FAILED" || p.SaveResult != nil {
					t.Fatal(p.Status, p.Error)
				}
				step := fail + 1
				for i, st := range p.Steps {
					want := "SUCCESS"
					if i == step {
						want = "FAILED"
					}
					if i > step {
						want = "SKIPPED"
					}
					if st.Status != want {
						t.Fatalf("step %d %s want %s", i, st.Status, want)
					}
				}
				if pipelineCount(t, j, "wb_daily_snapshots") != 0 || pipelineCount(t, j, "wb_daily_diffs") != 0 {
					t.Fatal("partial transaction")
				}
				if fail == 3 && pipelineCount(t, j, "wb_daily_current") != 0 {
					t.Fatal("rollback failed")
				}
			}

			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "synthetic-secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestPipelineLocalToolsRejectForgedAndExpiredAuthority(t *testing.T) {
	j, m, _, u, w := pipelineFixture(t)
	ctx := context.Background()
	job, _ := j.Get(u, w)
	input := background.MarketInput{Prices: mcpclient.Envelope[[]wb.Price]{Source: "wildberries", FetchedAt: "2026-09-25T00:00:00Z", Complete: true, UntrustedData: true, Data: []wb.Price{}}, Stocks: mcpclient.Envelope[[]wb.Stock]{Source: "wildberries", FetchedAt: "2026-09-25T00:00:00Z", Complete: true, UntrustedData: true, Data: []wb.Stock{}}}
	var a background.Aggregate
	if e := m.Client.LocalPipeline(ctx, background.BuildTool, "forged", input, &a); e == nil {
		t.Fatal("forged grant accepted")
	}
	p := background.PipelineContext{Job: job, ConnectionID: 1, Revision: 1, RunID: 999, Owner: "forged"}
	if e := m.CallPipelineTool(ctx, p, background.BuildTool, input, &a); e == nil {
		t.Fatal("missing lease accepted")
	}
	if e := m.CallPipelineTool(ctx, p, "wb_update_prices", input, &a); e == nil {
		t.Fatal("arbitrary tool")
	}
	m.pipelineGrants["expired"] = pipelineGrant{p, background.BuildTool, time.Now().Add(-time.Minute)}
	if e := m.Client.LocalPipeline(ctx, background.BuildTool, "expired", input, &a); e == nil {
		t.Fatal("expired grant")
	}
	if pipelineCount(t, j, "wb_daily_snapshots") != 0 {
		t.Fatal("unauthorized write")
	}
}

func TestSellerPartialAndWB403ThroughRealMCP(t *testing.T) {
	jobs, m, api, u, w := pipelineFixture(t)
	api.Mode = "stock_sources"
	if e := jobs.RunNow(context.Background(), u, w); e != nil {
		t.Fatal(e)
	}
	p := pipelineLast(t, jobs, u, w)
	if p.Status != "PARTIAL_SUCCESS" || p.SaveResult == nil || p.SaveResult.SellerRecords != 33 || p.Aggregate.SellerSource.Status != "PARTIAL" || p.Aggregate.WBSource.Status != "PERMISSION_DENIED" || p.Aggregate.ZeroStock != 0 || p.Aggregate.SellerZero != 0 {
		t.Fatalf("status %s aggregate %+v error %+v", p.Status, p.Aggregate, p.Error)
	}
	var n int
	if e := jobs.DB.QueryRow(`SELECT COUNT(*) FROM marketplace_stocks WHERE kind='seller_stocks'`).Scan(&n); e != nil || n != 33 {
		t.Fatal(n, e)
	}
	before := api.prices.Load() + api.stocks.Load()
	summary, e := m.DailySummary(context.Background(), u, w)
	if e != nil || summary.Aggregate == nil || summary.Aggregate.WBSource.Status != "PERMISSION_DENIED" || before != api.prices.Load()+api.stocks.Load() {
		t.Fatal(summary, e)
	}
}

func TestDailySyncWithSellerInfoCooldownDoesNotCallSeller(t *testing.T) {
	j, _, api, u, w := pipelineFixture(t)
	if _, e := j.DB.Exec(`INSERT INTO marketplace_cooldowns(rate_group,retry_at_ms,source) VALUES('common',?,'wb_retry')`, time.Now().Add(24*time.Hour).UnixMilli()); e != nil {
		t.Fatal(e)
	}
	if e := j.RunNow(context.Background(), u, w); e != nil {
		t.Fatal(e)
	}
	p := pipelineLast(t, j, u, w)
	if p.Status != "SUCCESS" || api.sellers.Load() != 0 || api.prices.Load() == 0 || api.stocks.Load() == 0 {
		t.Fatal(p.Status, api.sellers.Load())
	}
	j.Now = func() time.Time { return time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC) }
	if e := j.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if pipelineLast(t, j, u, w).Status != "SUCCESS" || api.sellers.Load() != 0 {
		t.Fatal("scheduled identity gateway")
	}
}
