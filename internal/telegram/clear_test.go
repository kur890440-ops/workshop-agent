package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/memory"
)

type clearHarness struct {
	*harness
	deleted  []int64
	response map[int64]string
	network  bool
	next     int64
}

func clearFixture(t *testing.T) *clearHarness {
	c := &clearHarness{harness: botFixture(t), response: map[int64]string{}, next: 1000}
	c.bot.HTTPClient = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		c.sent = append(c.sent, p)
		body := `{"ok":true,"result":true}`
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			c.next++
			body = fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":%d,"chat":{"id":%.0f,"type":"private"}}}`, c.next, time.Now().Unix(), p["chat_id"])
		}
		if strings.HasSuffix(r.URL.Path, "/deleteMessage") {
			id := int64(p["message_id"].(float64))
			c.deleted = append(c.deleted, id)
			if c.network {
				return nil, errors.New("simulated network error containing secret")
			}
			if result, ok := c.response[id]; ok {
				body = result
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	return c
}
func (c *clearHarness) incoming(t *testing.T, id int64, text string) {
	t.Helper()
	if err := c.bot.handleMessage(&telegramMessage{MessageID: id, Date: time.Now().Unix(), Text: text, Chat: telegramChat{ID: 900001, Type: "private"}, From: telegramUser{ID: 900001}}); err != nil {
		t.Fatal(err)
	}
}
func (c *clearHarness) lastText() string {
	for i := len(c.sent) - 1; i >= 0; i-- {
		if text, ok := c.sent[i]["text"].(string); ok {
			return text
		}
	}
	return ""
}

func TestClearAliasesAndCancellation(t *testing.T) {
	for _, s := range []string{"/clear", "очисти", "очистить", "очисти чат", "очистить чат", "очисти диалог", "очистить диалог", "удали переписку", "удалить переписку", "  ОЧИСТИ  "} {
		if !isClearCommand(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"удали материал", "очисти склад", "пожалуйста очисти чат", "/clear_all"} {
		if isClearCommand(s) {
			t.Fatal(s)
		}
	}
	c := clearFixture(t)
	c.bot.Agent = nil
	c.incoming(t, 1, "очисти")
	var count int
	if err := c.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages`).Scan(&count); err != nil || count != 2 {
		t.Fatal("incoming/outgoing tracking", count, err)
	}
	c.click(t, 900001, "Отмена")
	c.click(t, 900001, "Очистить чат")
	if len(c.deleted) != 0 {
		t.Fatal("cancelled action executed")
	}
	c.incoming(t, 2, "/clear")
	// Another user cannot consume the owner's confirmation.
	c.click(t, 900002, "Очистить чат")
	if len(c.deleted) != 0 {
		t.Fatal("foreign callback deleted messages")
	}
	c.click(t, 900001, "Очистить чат")
	if len(c.deleted) == 0 {
		t.Fatal("no delete requests")
	}
	before := len(c.deleted)
	c.click(t, 900001, "Очистить чат")
	if len(c.deleted) != before {
		t.Fatal("replayed confirmation")
	}
}

func TestClearPartialResultsAndRetention(t *testing.T) {
	c := clearFixture(t)
	c.incoming(t, 1, "/clear")
	now := time.Now().Unix()
	for _, e := range []struct {
		id, date int64
		kind     string
	}{{2, now, "message"}, {3, now, "message"}, {4, now - 49*3600, "message"}, {5, now, "dice"}, {6, 0, "message"}} {
		if _, err := c.bot.WS.DB().Exec(`INSERT INTO telegram_messages VALUES(900001,1,?,?,?)`, e.id, e.date, e.kind); err != nil {
			t.Fatal(err)
		}
	}
	c.response[2] = `{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`
	c.response[3] = `{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted"}`
	c.click(t, 900001, "Очистить чат")
	text := c.lastText()
	for _, part := range []string{"Удалено: 2.", "Уже отсутствовали: 1.", "неизвестной дате: 3.", "Ошибок удаления: 1.", "неизвестными ID"} {
		if !strings.Contains(text, part) {
			t.Fatal(text)
		}
	}
	var count int
	if err := c.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id IN (3,4,5,6)`).Scan(&count); err != nil || count != 4 {
		t.Fatal("failed/skipped entries lost")
	}
}

func TestClearNetworkAndRateLimit(t *testing.T) {
	for _, network := range []bool{true, false} {
		t.Run(fmt.Sprint(network), func(t *testing.T) {
			c := clearFixture(t)
			c.incoming(t, 1, "/clear")
			c.network = network
			if !network {
				c.response[1] = `{"ok":false,"error_code":429,"parameters":{"retry_after":60},"description":"Too Many Requests"}`
			}
			c.click(t, 900001, "Очистить чат")
			if len(c.deleted) != 1 || !strings.Contains(c.lastText(), "Удалено: 0.") {
				t.Fatal(c.lastText())
			}
			var count int
			if err := c.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id=1`).Scan(&count); err != nil || count != 1 {
				t.Fatal("failed entry lost")
			}
			if !network {
				c.incoming(t, 2, "/clear")
				c.click(t, 900001, "Очистить чат")
				if len(c.deleted) != 1 {
					t.Fatal("ignored retry_after")
				}
			}
		})
	}
}

func TestClearClosesSessionsPreservesTaskAndDomain(t *testing.T) {
	c := clearFixture(t)
	c.incoming(t, 1, "/start")
	w, err := c.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.bot.Inv.ForUser(1).CreateMaterial(w, "Гипс", "raw", "kg", 10, 1, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	m := c.bot.Agent.Memory.ForUser(1)
	sc, err := m.EnsureSession(memory.Scope{UserID: 1, WorkshopID: w}, 900001)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.AppendShortTerm(sc, "user", "old conversation"); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{Quantity: 25})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SaveLongTermMemory(sc, memory.LongTerm{Type: "USER_PREFERENCE", ScopeType: "user", Key: "response_style", Value: "concise", Source: "explicit"}); err != nil {
		t.Fatal(err)
	}
	c.bot.startSetup(900001, &setupSession{userID: 1, workshopID: w, kind: "material"})
	if err = c.bot.screen(sessionKey{900001, 1}, "old menu", choice{Text: "old", Action: "home"}); err != nil {
		t.Fatal(err)
	}
	c.incoming(t, 2, "очистить диалог")
	c.click(t, 900001, "Очистить чат")
	if c.bot.getSetup(900001, 1) != nil {
		t.Fatal("form survived")
	}
	c.bot.uiMu.Lock()
	for _, a := range c.bot.buttons {
		if a.Key == (sessionKey{900001, 1}) {
			t.Error("old button survived")
		}
	}
	c.bot.uiMu.Unlock()
	fresh, err := m.EnsureSession(memory.Scope{UserID: 1, WorkshopID: w}, 900001)
	if err != nil || fresh.SessionID == sc.SessionID {
		t.Fatal("session not rotated", err)
	}
	messages, err := m.ShortTerm(fresh)
	if err != nil || len(messages) != 0 {
		t.Fatal("old context leaked", err)
	}
	active, err := m.ActiveWorking(fresh)
	if err != nil || active == nil || active.ID != task.ID {
		t.Fatal("task lost", err)
	}
	stock, err := c.bot.Inv.ForUser(1).GetMaterialStock(w, "Гипс")
	if err != nil || stock != 10000 {
		t.Fatal("domain data changed")
	}
	var count int
	if err = c.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM user_preferences WHERE user_id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("preference lost", err)
	}
}

func TestClearExpiryMediaAndOtherChatIsolation(t *testing.T) {
	c := clearFixture(t)
	c.incoming(t, 1, "/clear")
	c.bot.uiMu.Lock()
	for id, a := range c.bot.buttons {
		if a.Action == "clear_execute" {
			a.Expires = time.Now().Add(-time.Minute)
			c.bot.buttons[id] = a
		}
	}
	c.bot.uiMu.Unlock()
	c.click(t, 900001, "Очистить чат")
	if len(c.deleted) != 0 {
		t.Fatal("expired action ran")
	}
	// Empty text represents a photo/document update and must still be tracked.
	c.incoming(t, 2, "")
	if err := c.bot.handleMessage(&telegramMessage{MessageID: 3, Date: time.Now().Add(-25 * time.Hour).Unix(), Dice: json.RawMessage(`{"value":3}`), Chat: telegramChat{ID: 900001, Type: "private"}, From: telegramUser{ID: 900001}}); err != nil {
		t.Fatal(err)
	}
	c.message(t, 900002, "/start")
	var other int64
	if err := c.bot.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=900002`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := c.bot.trackMessage(&telegramMessage{MessageID: 50, Date: time.Now().Unix(), Chat: telegramChat{ID: 900002, Type: "private"}}, other); err != nil {
		t.Fatal(err)
	}
	if err := c.bot.clearChat(sessionKey{900002, 1}); err == nil {
		t.Fatal("foreign chat allowed")
	}
	c.incoming(t, 4, "/clear")
	c.click(t, 900001, "Очистить чат")
	seen := map[int64]bool{}
	for _, id := range c.deleted {
		seen[id] = true
	}
	if !seen[2] || !seen[3] || seen[50] {
		t.Fatal("media or chat isolation", seen)
	}
	var count int
	if err := c.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE chat_id=900002 AND message_id=50`).Scan(&count); err != nil || count != 1 {
		t.Fatal("other chat record lost")
	}
}
