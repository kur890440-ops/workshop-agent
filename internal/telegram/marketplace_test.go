package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/marketplace/wildberries"
)

func wbFixture(t *testing.T) (*harness, int64, int64) {
	t.Helper()
	h := botFixture(t)
	user, e := h.bot.WS.UpsertUser(900001, "owner", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	workshop, e := h.bot.WS.CreateOwnedWorkshop(user, "WB fixture")
	if e != nil {
		t.Fatal(e)
	}
	s, e := marketplace.New(h.bot.WS.DB(), wildberries.New("synthetic-wb-test-token"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	h.bot.Marketplace = s
	h.bot.Agent.Marketplace = s
	return h, user, workshop
}

// Unexpected API methods panic through the nil embedded interface: this flow
// must load only identity and WB stocks, never catalog or seller stocks.
type stockRefreshAPI struct {
	marketplace.API
	release chan struct{}
	calls   atomic.Int32
	err     error
}

func (*stockRefreshAPI) Configured() bool           { return true }
func (*stockRefreshAPI) ContainsSecret(string) bool { return false }
func (*stockRefreshAPI) Seller(context.Context) (wildberries.Seller, error) {
	return wildberries.Seller{ID: "fixture", Name: "Fixture"}, nil
}
func (f *stockRefreshAPI) WBStocks(ctx context.Context) ([]wildberries.Stock, error) {
	f.calls.Add(1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
	}
	return []wildberries.Stock{{NmID: 101, ChrtID: 102, WarehouseID: 103, Quantity: 7}}, f.err
}

func TestWBStocksViewsDoNotFetchAndSeparateSources(t *testing.T) {
	h, user, workshop := wbFixture(t)
	h.bot.Marketplace.Close()
	api := &stockRefreshAPI{release: make(chan struct{})}
	svc, e := marketplace.New(h.bot.WS.DB(), api)
	if e != nil {
		t.Fatal(e)
	}
	h.bot.Marketplace = svc
	h.bot.Agent.Marketplace = svc
	t.Cleanup(func() { close(api.release); svc.Close() })
	if _, e = svc.Attach(marketplace.Scope{UserID: user, WorkshopID: workshop}); e != nil {
		t.Fatal(e)
	}
	for _, cmd := range []string{"/wb seller_stocks", "/wb wb_stocks", "/wb stocks 10"} {
		h.message(t, 900001, cmd)
		text := h.sent[len(h.sent)-1]["text"].(string)
		if !strings.Contains(text, "ОСТАТКИ") {
			t.Fatal(text)
		}
	}
	h.message(t, 900001, "/wb")
	h.click(t, 900001, "Остатки продавца")
	if api.calls.Load() != 0 {
		t.Fatal("view unexpectedly fetched WB")
	}
	h.message(t, 900001, "/wb stocks 10")
	text := h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "ОСТАТКИ ПРОДАВЦА") || !strings.Contains(text, "ОСТАТКИ НА СКЛАДАХ WB") || !strings.Contains(text, "Обновлено:") || !strings.Contains(text, "Позиций:") {
		t.Fatal(text)
	}
}
func TestWBAttachConfirmationAndStaleWorkshopButton(t *testing.T) {
	h, user, workshop := wbFixture(t)
	h.message(t, 900001, "/wb attach")
	var n int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM marketplace_connections`).Scan(&n)
	if n != 0 {
		t.Fatal("attached before explicit confirmation")
	}
	h.click(t, 900001, "Подтвердить")
	sc := marketplace.Scope{UserID: user, WorkshopID: workshop, ConnectionID: 1}
	c, e := h.bot.Marketplace.Status(sc)
	if e != nil || !c.Enabled {
		t.Fatal(c, e)
	}
	old := buttonAction{Key: sessionKey{900001, user}, Action: "wb_disable", Workshop: workshop, Target: 1}
	if _, e = h.bot.WS.CreateOwnedWorkshop(user, "other"); e != nil {
		t.Fatal(e)
	}
	if e = h.bot.executeButton(old); e == nil {
		t.Fatal("old button acted in changed workshop")
	}
	var enabled int
	h.bot.WS.DB().QueryRow(`SELECT enabled FROM marketplace_connections`).Scan(&enabled)
	if enabled != 1 {
		t.Fatal("old button disabled connection")
	}
	if e = h.bot.WS.SetActiveWorkshop(user, workshop); e != nil {
		t.Fatal(e)
	}
	h.message(t, 900001, "/wb disable")
	h.click(t, 900001, "Подтвердить")
	c, e = h.bot.Marketplace.Status(sc)
	if e != nil || c.Enabled {
		t.Fatal(c, e)
	}
}
func TestWBSecretIngressAndAgentBypass(t *testing.T) {
	h, user, workshop := wbFixture(t)
	for _, msg := range []string{"synthetic-wb-test-token", "WB_API_TOKEN=synthetic-other", "/wb token synthetic-wb-test-token"} {
		h.message(t, 900001, msg)
	}
	raw, _ := json.Marshal(h.sent)
	if strings.Contains(string(raw), "synthetic-wb-test-token") || strings.Contains(string(raw), "synthetic-other") {
		t.Fatal("token echoed")
	}
	for _, table := range []string{"conversation_messages", "long_term_memory", "working_memory"} {
		var n int
		if e := h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n != 0 {
			t.Fatal("secret entered memory", table)
		}
	}
	// The agent's deterministic read uses no LLM call and saves no dialogue content.
	answer, _, e := h.bot.Agent.HandleMessageForWorkshop(context.Background(), workshop, user, 900001, "/wb status")
	if e != nil || !strings.Contains(answer, "Кабинет не привязан") {
		t.Fatal(answer, e)
	}
	answer, _, e = h.bot.Agent.HandleMessageForWorkshop(context.Background(), workshop, user, 900001, "WB_API_TOKEN=synthetic-other")
	if e != nil || strings.Contains(answer, "synthetic-other") {
		t.Fatal(answer, e)
	}
	var n int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM conversation_messages`).Scan(&n)
	if n != 0 {
		t.Fatal("WB command entered chat memory")
	}
}

type independentStockReader struct{ sources []wildberries.StockSource }

func (r *independentStockReader) StockSource(ctx context.Context, seller string, source wildberries.StockSource) (wildberries.StockBatch, error) {
	r.sources = append(r.sources, source)
	return wildberries.NewStockBatch(source, []wildberries.Stock{{NmID: 1, ChrtID: 2, WarehouseID: 3, Quantity: 7}}, nil), nil
}
func TestWBStocksStartsBothSourcesDuringSellerCooldown(t *testing.T) {
	h, u, w := wbFixture(t)
	h.bot.Marketplace.Close()
	api := &stockRefreshAPI{release: make(chan struct{})}
	svc, e := marketplace.New(h.bot.WS.DB(), api)
	if e != nil {
		t.Fatal(e)
	}
	h.bot.Marketplace = svc
	t.Cleanup(svc.Close)
	if _, e = svc.Attach(marketplace.Scope{UserID: u, WorkshopID: w}); e != nil {
		t.Fatal(e)
	}
	if _, e = h.bot.WS.DB().Exec(`UPDATE marketplace_connections SET seller_id='fixture',checked_at='2026-09-25T00:00:00Z' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = h.bot.WS.DB().Exec(`INSERT INTO marketplace_cooldowns(rate_group,retry_at_ms,source) VALUES('common',9999999999999,'wb_retry')`); e != nil {
		t.Fatal(e)
	}
	reader := &independentStockReader{}
	svc.SetStockReader(reader)
	h.message(t, 900001, "/wb stocks")
	h.bot.wbWait.Wait()
	if len(reader.sources) != 2 || reader.sources[0] != wildberries.StockSeller || reader.sources[1] != wildberries.StockWB {
		t.Fatal(reader.sources)
	}
	text := h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "Результат загрузки") || !strings.Contains(text, "Остаток: 7 шт.") {
		t.Fatal(text)
	}
}
