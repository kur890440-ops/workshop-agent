package marketplace

import (
	"strings"
	"testing"
	"time"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/storage"
)

func TestIdentityCacheExpiryRestartRevisionAndExplicitCheck(t *testing.T) {
	f := setup(t)
	f.attach(t)
	clock := time.Now()
	f.s.clock = func() time.Time { return clock }
	f.run(t, "wb_stocks")
	if f.api.calls != 2 {
		t.Fatal(f.api.calls)
	}
	f.run(t, "wb_stocks")
	if f.api.calls != 3 {
		t.Fatal("identity not cached", f.api.calls)
	}
	clock = clock.Add(24 * time.Hour)
	f.run(t, "wb_stocks")
	if f.api.calls != 4 {
		t.Fatal("expired identity reused", f.api.calls)
	}
	f.run(t, "check")
	if f.api.calls != 5 {
		t.Fatal("explicit check skipped")
	}
	if err := f.s.Disable(f.sc); err != nil {
		t.Fatal(err)
	}
	f.attach(t)
	f.run(t, "wb_stocks")
	if f.api.calls != 6 {
		t.Fatal("revision not checked")
	}
	f.s.Close()
	s, err := New(f.ws.DB(), f.api)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f.s = s
	f.run(t, "wb_stocks")
	if f.api.calls != 7 {
		t.Fatal("restart trusted sqlite identity")
	}
}
func TestIdentityBlockedAndStocksRateLimitAreDistinct(t *testing.T) {
	f := setup(t)
	f.attach(t)
	retry := time.Now().Add(time.Hour).UTC()
	f.api.errorInfo = &wb.RateLimitError{Operation: "common", RetryAt: retry, Source: "wb_retry"}
	f.run(t, "wb_stocks")
	r := syncState(t, f, "wb_stocks")
	if r.BlockedBy != "check" || r.RateOperation != "common" || r.RetryAt == "" || f.api.calls != 1 {
		t.Fatal(r, f.api.calls)
	}
	text, err := f.s.ReadText(f.sc, "stocks", 0)
	if err != nil || !strings.Contains(text, "загрузка источника не выполнялась") || !strings.Contains(text, "Текущая попытка") {
		t.Fatal(text, err)
	}
	f.api.errorInfo = nil
	f.run(t, "wb_stocks")
	success := syncState(t, f, "wb_stocks").LastSuccess
	before := count(t, f.ws.DB(), "marketplace_stocks")
	f.api.stocksErr = &wb.RateLimitError{Operation: "analytics", RetryAt: retry, Source: "wb_retry"}
	f.run(t, "wb_stocks")
	r = syncState(t, f, "wb_stocks")
	if r.BlockedBy != "" || r.RateOperation != "analytics" || r.LastSuccess != success || count(t, f.ws.DB(), "marketplace_stocks") != before {
		t.Fatal(r)
	}
}
func TestCooldownPersistenceMigrationAndNoSecrets(t *testing.T) {
	f := setup(t)
	f.attach(t)
	d := cooldownDB{f.ws.DB()}
	deadline := time.Now().Add(24 * time.Hour).Truncate(time.Millisecond)
	if err := d.SaveCooldown(wb.RateLimitError{Operation: "common", RetryAt: deadline, Source: "wb_retry"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveCooldown(wb.RateLimitError{Operation: "common", RetryAt: deadline.Add(-time.Hour), Source: "local_interval"}); err != nil {
		t.Fatal(err)
	}
	f.s.Close()
	s, err := New(f.ws.DB(), f.api)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := (cooldownDB{f.ws.DB()}).LoadCooldown("common")
	if err != nil || !r.RetryAt.Equal(deadline) || f.api.calls != 0 {
		t.Fatal(err, r, f.api.calls)
	}
	store, err := storage.New(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(); err != nil {
		t.Fatal(err)
	}
	if count(t, f.ws.DB(), "marketplace_cooldowns") != 1 {
		t.Fatal("migration lost cooldown")
	}
	if strings.Contains(snapshot(t, f.ws.DB(), []string{"marketplace_cooldowns"}), testSecret) {
		t.Fatal("secret persisted")
	}
}

func TestCachedIdentityNeverCachesAccessOrExplicitFailure(t *testing.T) {
	f := setup(t)
	f.attach(t)
	f.run(t, "wb_stocks")
	calls := f.api.calls
	other, err := f.ws.CreateOwnedWorkshop(f.sc.UserID, "other")
	if err != nil || other == f.sc.WorkshopID {
		t.Fatal(err)
	}
	if _, err = f.s.Start(f.sc, "wb_stocks"); err == nil || f.api.calls != calls {
		t.Fatal("cached identity bypassed active workshop")
	}
	if err = f.ws.SetActiveWorkshop(f.sc.UserID, f.sc.WorkshopID); err != nil {
		t.Fatal(err)
	}
	f.api.errorInfo = wb.Unauthorized
	f.run(t, "check")
	f.api.errorInfo = nil
	calls = f.api.calls
	f.run(t, "wb_stocks")
	if f.api.calls != calls+1 {
		t.Fatal("failed explicit check retained trust")
	}
	calls = f.api.calls
	if _, err = f.ws.DB().Exec(`UPDATE workshop_members SET status='removed' WHERE user_id=? AND workshop_id=?`, f.sc.UserID, f.sc.WorkshopID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Start(f.sc, "wb_stocks"); err == nil || f.api.calls != calls {
		t.Fatal("cached identity bypassed membership")
	}
}

func TestStocksJobIgnoresSellerInfoFailureWithPersistentProfile(t *testing.T) {
	f := setup(t)
	f.attach(t)
	f.run(t, "all")
	f.api.errorInfo = &wb.RateLimitError{Operation: "common", RetryAt: time.Now().Add(time.Hour), Source: "wb_retry"}
	f.run(t, "check")
	before := f.api.calls
	f.run(t, "stocks")
	if f.api.calls != before+2 {
		t.Fatal("unexpected seller-info call", f.api.calls-before)
	}
	for _, kind := range []string{"seller_stocks", "wb_stocks"} {
		r := syncState(t, f, kind)
		if r.State != "succeeded" || r.BlockedBy != "" {
			t.Fatal(r)
		}
	}
}
func TestHistoricalStockCountersAreUnknown(t *testing.T) {
	f := setup(t)
	f.attach(t)
	_, e := f.ws.DB().Exec(`INSERT INTO marketplace_sync(connection_id,workshop_id,kind,state,started_at,error_code,row_count) VALUES(1,?,'seller_stocks','partial','2026-09-25T12:24:46Z','invalid_response',34)`, f.sc.WorkshopID)
	if e != nil {
		t.Fatal(e)
	}
	text, e := f.s.ReadText(f.sc, "seller_stocks", 0)
	if e != nil || !strings.Contains(text, "Исторический результат") || strings.Contains(text, "Получено: 0") {
		t.Fatal(text, e)
	}
}
