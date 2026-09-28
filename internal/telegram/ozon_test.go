package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"workshop-agent/internal/integrations/mcpmanager"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace/ozon"
)

func TestOzonCommandsMenuCacheAndSecretGuard(t *testing.T) {
	h, u, w := wbFixture(t)
	svc := ozon.NewService(h.bot.WS.DB(), "synthetic-ozon-client", "synthetic-ozon-secret")
	defer svc.Close()
	h.bot.Ozon = svc
	manager, e := mcpmanager.New(context.Background(), &wbmcpfixture.API{Mode: "success"}, nil, svc)
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	h.bot.OzonTools = manager
	// Discovery/cache operations do not use the real HTTP client.
	h.message(t, 900001, "/ozon attach")
	c, e := svc.Status(ozon.Access{UserID: u, WorkshopID: w, ConnectionID: 2})
	if e != nil || !c.Enabled {
		t.Fatal(c, e)
	}
	h.click(t, 900001, "Товары")
	text := h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "Ozon · Товары") || !strings.Contains(text, "Данные ещё не загружены") {
		t.Fatal(text)
	}
	h.message(t, 900001, "/ozon seller_stocks")
	text = h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "Остатки продавца") || !strings.Contains(text, "не подтверждение нулевых") {
		t.Fatal(text)
	}
	h.message(t, 900001, "/ozon fbo_stocks")
	text = h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "FBO Ozon") {
		t.Fatal(text)
	}
	h.message(t, 900001, "/ozon debug")
	all := ""
	for _, msg := range h.sent {
		if text, ok := msg["text"].(string); ok {
			all += text
		}
	}
	if !strings.Contains(all, "ozon_get_seller_stocks") || !strings.Contains(all, "in-memory") {
		t.Fatal("no real discovery in debug")
	}
	h.message(t, 900001, "synthetic-ozon-secret")
	if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "не принимаются") {
		t.Fatal("secret not rejected")
	}
	var n int
	for _, q := range []string{`SELECT COUNT(*) FROM conversation_messages WHERE content LIKE '%synthetic-ozon-secret%'`, `SELECT COUNT(*) FROM long_term_memory WHERE value_json LIKE '%synthetic-ozon-secret%'`, `SELECT COUNT(*) FROM integration_request_trace WHERE trace_json LIKE '%synthetic-ozon-secret%'`} {
		if e = h.bot.WS.DB().QueryRow(q).Scan(&n); e != nil || n != 0 {
			t.Fatal("secret persistence", e, n)
		}
	}
	old := buttonAction{Key: sessionKey{900001, u}, Action: "ozon_disable", Workshop: w, Target: 2}
	if _, e = h.bot.WS.CreateOwnedWorkshop(u, "Other active"); e != nil {
		t.Fatal(e)
	}
	if e = h.bot.executeButton(old); e == nil {
		t.Fatal("stale callback accepted")
	}
}

type slowOzonTools struct{ entered, release chan struct{} }

func (f *slowOzonTools) Ozon(ctx context.Context, a ozon.Access, tool string, in ozon.Input) (ozon.Result, error) {
	close(f.entered)
	select {
	case <-ctx.Done():
		return ozon.Result{}, ctx.Err()
	case <-f.release:
	}
	return ozon.Result{Provider: "OZON", Source: ozon.SellerSource, Status: "SUCCESS", Stocks: []ozon.Stock{{SKU: 1, WarehouseID: 1, Quantity: 7, Name: "Fixture"}}, Total: 1, UntrustedData: true}, nil
}
func (*slowOzonTools) OzonDiscovery() string { return "test fixture" }
func TestOzonRefreshReturnsBeforeNetworkAndDropsRevokedReply(t *testing.T) {
	h, u, w := wbFixture(t)
	svc := ozon.NewService(h.bot.WS.DB(), "dummy-client", "dummy-secret")
	defer svc.Close()
	h.bot.Ozon = svc
	a := ozon.Access{UserID: u, WorkshopID: w, ConnectionID: 2}
	if e := svc.Attach(a); e != nil {
		t.Fatal(e)
	}
	fake := &slowOzonTools{make(chan struct{}), make(chan struct{})}
	h.bot.OzonTools = fake
	started := time.Now()
	h.message(t, 900001, "/ozon refresh seller")
	if time.Since(started) > time.Second {
		t.Fatal("handler blocked")
	}
	<-fake.entered
	count := len(h.sent)
	if e := svc.Disable(a); e != nil {
		t.Fatal(e)
	}
	close(fake.release)
	h.bot.WaitBackground()
	if len(h.sent) != count {
		t.Fatal("late response after disable")
	}
}
