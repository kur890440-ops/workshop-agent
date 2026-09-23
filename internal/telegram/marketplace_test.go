package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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

func TestWBStocksRefreshOnRequest(t *testing.T) {
	for _, mode := range []string{"command", "button", "failure", "workshop_changed", "disabled", "page", "invalid_page"} {
		t.Run(mode, func(t *testing.T) {
			h, user, workshop := wbFixture(t)
			h.bot.Marketplace.Close()
			api := &stockRefreshAPI{release: make(chan struct{})}
			if mode == "failure" {
				api.err = wildberries.Forbidden
			}
			svc, err := marketplace.New(h.bot.WS.DB(), api)
			if err != nil {
				t.Fatal(err)
			}
			h.bot.Marketplace = svc
			h.bot.Agent.Marketplace = svc
			var once sync.Once
			release := func() { once.Do(func() { close(api.release) }) }
			t.Cleanup(func() { release(); svc.Close(); h.bot.wbWait.Wait() })
			c, err := svc.Attach(marketplace.Scope{UserID: user, WorkshopID: workshop})
			if err != nil {
				t.Fatal(err)
			}
			sc := marketplace.Scope{UserID: user, WorkshopID: workshop, ConnectionID: c}
			if mode == "page" || mode == "invalid_page" {
				command := "/wb stocks 10"
				if mode == "invalid_page" {
					command = "/wb stocks -1"
				}
				h.message(t, 900001, command)
				if api.calls.Load() != 0 {
					t.Fatal("pagination started WB request")
				}
				return
			}
			if mode == "button" {
				h.message(t, 900001, "/wb")
				h.click(t, 900001, "Остатки WB")
			} else {
				h.message(t, 900001, "/wb stocks")
			}
			// The API is blocked; command handling must already have returned.
			if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "Обновляю остатки") {
				t.Fatal(h.sent)
			}
			h.message(t, 900001, "/wb stocks")
			if !strings.Contains(h.sent[len(h.sent)-1]["text"].(string), "уже выполняется") {
				t.Fatal("duplicate refresh not rejected")
			}
			if mode == "workshop_changed" {
				if _, err = h.bot.WS.CreateOwnedWorkshop(user, "Other"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "disabled" {
				if err = svc.Disable(sc); err != nil {
					t.Fatal(err)
				}
			}
			before := len(h.sent)
			release()
			h.bot.wbWait.Wait()
			if mode == "workshop_changed" || mode == "disabled" {
				if len(h.sent) != before {
					t.Fatal("late data published after scope changed")
				}
				return
			}
			if api.calls.Load() != 1 {
				t.Fatal("wrong request count", api.calls.Load())
			}
			result := h.sent[len(h.sent)-1]["text"].(string)
			if mode == "failure" {
				if !strings.Contains(result, "не завершилось успешно") || strings.Contains(result, "nm=101") {
					t.Fatal(result)
				}
			} else if !strings.Contains(result, "Остатки на складах WB обновлены") || !strings.Contains(result, "nm=101 chrt=102: 7") {
				t.Fatal(result)
			}
		})
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
