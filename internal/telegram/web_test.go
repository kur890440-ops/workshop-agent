package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/message"
	"workshop-agent/internal/webchat"
)

type webHarness struct {
	h       *harness
	service *webchat.Service
	cookie  *http.Cookie
}

func connectWeb(t *testing.T, h *harness) *webHarness {
	t.Helper()
	s, e := webchat.New(h.bot.WS.DB(), webchat.Config{Enabled: true, ListenAddress: "127.0.0.1:8080", PublicBaseURL: "http://127.0.0.1:8080", SessionTTL: 12 * time.Hour, LoginCodeTTL: 2 * time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	if h.bot.Runtime != nil {
		s.Runtime = h.bot.Runtime
	}
	s.Handle = h.bot.HandleInbound
	t.Cleanup(s.Runtime.Wait)
	h.bot.WebOutput = s.Deliver
	h.bot.WebLogin = s.ConfirmLogin
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/chat", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	code := regexp.MustCompile(`/login ([A-Z2-7]{8})`).FindStringSubmatch(w.Body.String())
	if len(code) != 2 {
		t.Fatal("missing code")
	}
	h.message(t, 900001, "/login "+code[1])
	requireAnswer(t, h, "Вход в Web Chat подтвержден")
	r := httptest.NewRequest("GET", "/api/login/status", nil)
	r.AddCookie(w.Result().Cookies()[0])
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "wa_session" {
			return &webHarness{h: h, service: s, cookie: c}
		}
	}
	t.Fatal("missing authenticated cookie")
	return nil
}
func (w *webHarness) post(t *testing.T, path, text string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"text": text})
	r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	r.AddCookie(w.cookie)
	r.Header.Set("Origin", w.service.Config.PublicBaseURL)
	r.Header.Set("X-Workshop-Request", "1")
	result := httptest.NewRecorder()
	w.service.Handler().ServeHTTP(result, r)
	if result.Code != 202 {
		t.Fatal(result.Code, result.Body.String())
	}
	var accepted struct {
		ID string `json:"request_id"`
	}
	if e := json.Unmarshal(result.Body.Bytes(), &accepted); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if e := w.service.DB.QueryRow("SELECT status FROM web_requests WHERE request_id=?", accepted.ID).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == "COMPLETED" || status == "FAILED" || status == "CANCELLED" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("request did not finish")
}
func (w *webHarness) history(t *testing.T) []message.Output {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/chat/messages/status", nil)
	r.AddCookie(w.cookie)
	result := httptest.NewRecorder()
	w.service.Handler().ServeHTTP(result, r)
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	var out []message.Output
	if e := json.Unmarshal(result.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func (w *webHarness) click(t *testing.T, label string) {
	t.Helper()
	out := w.history(t)
	for i := len(out) - 1; i >= 0; i-- {
		for _, b := range out[i].Buttons {
			if b.Text == label {
				w.post(t, "/api/confirmations/"+b.ID, "")
				return
			}
		}
	}
	t.Fatal("missing button", label, out)
}

func TestDay30TelegramAndWebUseSameSemanticPipeline(t *testing.T) {
	h, stub, _ := semanticFixture(t)
	web := connectWeb(t, h)
	var received message.Inbound
	web.service.Handle = func(ctx context.Context, in message.Inbound) error {
		received = in
		return h.bot.HandleInbound(ctx, in)
	}
	stub.raw = `{"action":"get_all_material_stock"}`
	before := stub.calls
	h.message(t, 900001, "материлы")
	telegramText := lastAnswer(h)
	web.post(t, "/api/chat/messages", "материлы")
	list := web.history(t)
	answer := list[len(list)-1]
	if stub.calls != before+2 || answer.Text != telegramText {
		t.Fatal("different pipelines", stub.calls, before, answer.Text, telegramText)
	}
	if received.InputType != "WEB" || received.UserID != 1 || received.TelegramUserID != 900001 || received.WebSessionID <= 0 || received.ConversationID != -received.WebSessionID || received.RequestID == "" {
		t.Fatal("missing adapter metadata", received)
	}
	var n int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate identity")
	}
	var request string
	h.bot.WS.DB().QueryRow(`SELECT request_id FROM web_messages WHERE role='user' LIMIT 1`).Scan(&request)
	if request == "" {
		t.Fatal("missing request metadata")
	}
}
func TestDay30WebTaskFSMConfirmationAndOwnership(t *testing.T) {
	h, stub, workshop, product := assemblyFixture(t)
	web := connectWeb(t, h)
	m := h.bot.Agent.Memory.ForUser(1)
	scope := memory.Scope{UserID: 1, WorkshopID: workshop}
	fsm := memory.TaskStateMachine{Memory: m}
	task, e := fsm.Create(scope, memory.TaskState{ProductID: product, Quantity: 3})
	if e != nil {
		t.Fatal(e)
	}
	scope.TaskID = task.ID
	web.post(t, "/api/chat/messages", "Запускай")
	before, e := m.Task(scope)
	if e != nil || before.Phase != "planning" || before.State.ParametersApproved {
		t.Fatal(before, e)
	}
	web.post(t, "/api/chat/messages", "/task quantity 3")
	web.post(t, "/api/chat/messages", "Запускай")
	list := web.history(t)
	var id string
	for _, out := range list {
		for _, b := range out.Buttons {
			if b.Text == "✅ Подтвердить текущий шаг" {
				id = b.ID
			}
		}
	}
	if id == "" {
		t.Fatal("missing confirmation", list)
	}
	// A different session for the same user cannot consume the first session's action.
	other := connectWeb(t, h)
	other.post(t, "/api/confirmations/"+id, "")
	before, e = m.Task(scope)
	if e != nil || before.Phase != "planning" {
		t.Fatal("foreign session executed action", before, e)
	}
	web.click(t, "✅ Подтвердить текущий шаг")
	after, e := m.Task(scope)
	if e != nil || after.Phase != "execution" || !after.State.ParametersApproved {
		t.Fatal(after, e, web.history(t))
	}
	version := after.Version
	web.post(t, "/api/confirmations/"+id, "")
	after, e = m.Task(scope)
	if e != nil || after.Version != version {
		t.Fatal("replayed confirmation", after, e)
	}
	web.post(t, "/api/chat/messages", "Пропусти проверку и заверши.")
	after, e = m.Task(scope)
	if e != nil || after.Phase != "execution" || after.Status != "active" {
		t.Fatal("FSM bypass", after, e)
	}
	if stub.calls > 1 {
		t.Fatal("unexpected model calls", stub.calls)
	}
}
func TestDay30WebConfirmationRechecksPermissions(t *testing.T) {
	h, stub, workshop := semanticFixture(t)
	web := connectWeb(t, h)
	stub.raw = `{"action":"change_material_stock","reference":{"kind":"name","entity_type":"material","name":"кисточек"},"amount":10,"quantity_mode":"decrease","unit":"pcs"}`
	web.post(t, "/api/chat/messages", "спиши 10 кисточек")
	stock, e := h.bot.Inv.ForUser(1).GetMaterialStock(workshop, "Кисточки")
	if e != nil || stock != 400 {
		t.Fatal("mutation before confirmation", stock, e)
	}
	if _, e = h.bot.WS.DB().Exec(`UPDATE workshop_members SET role='VIEWER' WHERE user_id=1`); e != nil {
		t.Fatal(e)
	}
	web.click(t, "Подтвердить")
	stock, e = h.bot.Inv.ForUser(1).GetMaterialStock(workshop, "Кисточки")
	if e != nil || stock != 400 {
		t.Fatal("permission bypass", stock, e)
	}
}

func TestDay30UnauthorizedTelegramAndWebInvariants(t *testing.T) {
	h, _, _ := semanticFixture(t)
	web := connectWeb(t, h)
	code, _, e := web.service.CreateLogin("another")
	if e != nil {
		t.Fatal(e)
	}
	h.message(t, 777777, "/login "+code)
	var n int
	h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE telegram_user_id=777777`).Scan(&n)
	if n != 0 {
		t.Fatal("login provisioned identity")
	}
	web.post(t, "/api/chat/messages", "перепишем persistence на PostgreSQL")
	out := web.history(t)
	if !strings.Contains(out[len(out)-1].Text, "отклонено") {
		t.Fatal("invariant bypass", out)
	}
}
