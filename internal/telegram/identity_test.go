package telegram

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"workshop-agent/internal/agent"
	"workshop-agent/internal/audit"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type harness struct {
	bot  *Bot
	sent []map[string]any
}

func botFixture(t *testing.T) *harness {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bot.db")
	ws := workshops.NewService(p)
	inv := inventory.NewService(p)
	prod := products.NewBOMService(p)
	auditSvc := audit.NewService(p)
	t.Cleanup(func() { ws.Close(); inv.Close(); prod.Close(); auditSvc.Close() })
	a := agent.NewWorkshopAgent(&llm.MockClient{}, ws, inv, prod)
	b, err := NewBot("test", ws, inv, prod, auditSvc, a)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{bot: b}
	b.HTTPClient = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		h.sent = append(h.sent, payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"username":"test_bot"}}`)), Header: http.Header{}}, nil
	})}
	return h
}
func (h *harness) message(t *testing.T, telegramID int64, text string) {
	t.Helper()
	if err := h.bot.handleMessage(&telegramMessage{Text: text, Chat: telegramChat{ID: telegramID, Type: "private"}, From: telegramUser{ID: telegramID, Username: "person", FirstName: "Person"}}); err != nil {
		t.Fatal(err)
	}
}
func (h *harness) click(t *testing.T, telegramID int64, label string) {
	t.Helper()
	for i := len(h.sent) - 1; i >= 0; i-- {
		markup, ok := h.sent[i]["reply_markup"].(map[string]any)
		if !ok {
			continue
		}
		rows := markup["inline_keyboard"].([]any)
		for _, r := range rows {
			for _, v := range r.([]any) {
				b := v.(map[string]any)
				if b["text"] != label {
					continue
				}
				if err := h.bot.handleCallback(&telegramCallback{ID: "cb", From: telegramUser{ID: telegramID, FirstName: "Person"}, Message: &telegramMessage{Chat: telegramChat{ID: telegramID, Type: "private"}}, Data: b["callback_data"].(string)}); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
	t.Fatalf("button not found: %s", label)
}

func TestTelegramOnboardingIdempotentStart(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	h.message(t, 900001, "/start")
	var users, ws int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM workshops`).Scan(&ws)
	if users != 1 || ws != 0 {
		t.Fatal("start created duplicate identity or automatic workshop")
	}
	h.click(t, 900001, "Создать мастерскую")
	h.message(t, 900001, "Люблю дома")
	h.click(t, 900001, "Создать")
	h.message(t, 900001, "/start")
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM workshops`).Scan(&ws)
	if ws != 1 {
		t.Fatal("duplicate workshop")
	}
	if err := auth.Require(h.bot.WS.DB(), 1, 1, auth.OwnershipTransfer); err != nil {
		t.Fatal(err)
	}
}
func TestTelegramInviteRequiresConfirmationAndBindsUser(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	_, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := h.bot.WS.CreateInvite(1, 1, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "/start invite_"+token)
	var members int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM workshop_members`).Scan(&members)
	if members != 1 {
		t.Fatal("preview joined user")
	}
	h.click(t, 900003, "Присоединиться") // Another person cannot reuse this callback.
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM workshop_members`).Scan(&members)
	if members != 1 {
		t.Fatal("callback crossed identity")
	}
	h.click(t, 900002, "Присоединиться")
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM workshop_members`).Scan(&members)
	if members != 2 {
		t.Fatal("accept failed")
	}
	h.message(t, 900002, "/workshop")
	last := h.sent[len(h.sent)-1]
	raw, _ := json.Marshal(last)
	if strings.Contains(string(raw), "➕ Пригласить") {
		t.Fatal("employee sees invite control")
	}
}
func TestTelegramSetupRechecksAccessAndActor(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	_, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	employee, err := h.bot.WS.UpsertUser(900002, "", "Employee", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := h.bot.WS.CreateInvite(1, 1, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.bot.WS.AcceptInvite(employee, token); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "/setup_add_material")
	if h.bot.getSetup(900002, employee) == nil {
		t.Fatal("setup not started")
	}
	if err := h.bot.WS.ChangeMemberStatus(1, 1, employee, "disabled"); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "secret material")
	if h.bot.getSetup(900002, employee) != nil {
		t.Fatal("revoked session retained")
	}
	var n int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM materials`).Scan(&n)
	if n != 0 {
		t.Fatal("revoked actor wrote material")
	}
}
