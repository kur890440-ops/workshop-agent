package telegram

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"workshop-agent/internal/memory"
)

func TestFormNavigationKeyboardFailureAndForeignCallback(t *testing.T) {
	h, _, _ := semanticFixture(t)
	base := h.bot.HTTPClient.Transport
	var id int64 = 500
	edits := 0
	h.bot.HTTPClient.Transport = transport(func(r *http.Request) (*http.Response, error) {
		resp, e := base.RoundTrip(r)
		if e != nil {
			return resp, e
		}
		if strings.HasSuffix(r.URL.Path, "editMessageReplyMarkup") {
			edits++
			resp.Body.Close()
			return nil, fmt.Errorf("test network failure")
		}
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			id++
			resp.Body.Close()
			resp.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":1,"chat":{"id":900001,"type":"private"}}}`, id)))
		}
		return resp, nil
	})
	h.click(t, 900001, "Редактировать")
	var token string
	var old buttonAction
	for k, a := range h.bot.buttons {
		if a.Action == "form_cancel" {
			token = k
			old = a
		}
	}
	if e := h.bot.handleCallback(&telegramCallback{ID: "foreign", From: telegramUser{ID: 900002}, Message: &telegramMessage{Chat: telegramChat{ID: 900002, Type: "private"}}, Data: "wa:" + token}); e != nil {
		t.Fatal(e)
	}
	if h.bot.getSetup(900001, 1) == nil {
		t.Fatal("foreign callback cancelled form")
	}
	h.message(t, 900001, "1")
	if edits == 0 {
		t.Fatal("old markup not removed")
	}
	if e := h.bot.executeButton(old); e == nil {
		t.Fatal("network failure revived old button")
	}
	h.click(t, 900001, "Отмена")
}

func requireFormButtons(t *testing.T, h *harness, back bool) {
	t.Helper()
	markup, ok := h.sent[len(h.sent)-1]["reply_markup"].(map[string]any)
	if !ok {
		t.Fatal("missing keyboard", lastAnswer(h))
	}
	labels := map[string]bool{}
	for _, row := range markup["inline_keyboard"].([]any) {
		for _, v := range row.([]any) {
			labels[v.(map[string]any)["text"].(string)] = true
		}
	}
	for _, label := range []string{"Отмена", "Главное меню"} {
		if !labels[label] {
			t.Fatal("missing", label)
		}
	}
	if back && !labels["Назад"] {
		t.Fatal("missing back")
	}
}
func TestFormNavigationStockErrorBackCancel(t *testing.T) {
	h, stub, w := semanticFixture(t)
	h.click(t, 900001, "Редактировать")
	requireFormButtons(t, h, false)
	h.message(t, 900001, "1")
	requireFormButtons(t, h, true)
	var old buttonAction
	for _, a := range h.bot.buttons {
		if a.Action == "form_back" {
			old = a
		}
	}
	h.message(t, 900001, "-50")
	requireAnswer(t, h, "Нельзя списать 50 кг: доступно 10 кг")
	requireFormButtons(t, h, true)
	if e := h.bot.executeButton(old); e == nil {
		t.Fatal("stale step accepted")
	}
	h.click(t, 900001, "Назад")
	requireAnswer(t, h, "Введите номер материала")
	requireFormButtons(t, h, false)
	h.message(t, 900001, "1")
	h.click(t, 900001, "Отмена")
	requireAnswer(t, h, "Материалы:")
	if h.bot.getSetup(900001, 1) != nil {
		t.Fatal("form remains")
	}
	mat, e := h.bot.Inv.ForUser(1).MaterialByName(w, "Гипс")
	if e != nil || mat["current_stock"] != float64(10000) {
		t.Fatal(mat, e)
	}
	var n int
	if e = h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM inventory_movements").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if stub.calls != 0 {
		t.Fatal("navigation called model")
	}
}
func TestFormNavigationCommandsAndTaskPreserved(t *testing.T) {
	h, _, w, p := assemblyFixture(t)
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	task, e := (memory.TaskStateMachine{Memory: m}).Create(sc, memory.TaskState{ProductID: p, Quantity: 1})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	for _, command := range []string{"/workshop", "/workshops", "/start", "/menu", "/setup_stop", " ОСТАНОВИСЬ ", "отмена", "отмени", "прекрати ввод", "выйти из ввода"} {
		h.message(t, 900001, "/materials")
		h.click(t, 900001, "Редактировать")
		h.message(t, 900001, "1")
		h.message(t, 900001, command)
		if h.bot.getSetup(900001, 1) != nil {
			t.Fatal("command trapped", command)
		}
		if strings.Contains(lastAnswer(h), "Введите остаток") {
			t.Fatal(command)
		}
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "1")
	h.message(t, 900001, "заверши задачу")
	requireAnswer(t, h, "сначала выйдите из формы")
	h.click(t, 900001, "Продолжить ввод")
	requireAnswer(t, h, "Введите новый остаток")
	h.message(t, 900001, "завершить задачу")
	h.click(t, 900001, "Отменить ввод и открыть задачу")
	after, e := m.Task(sc)
	if e != nil || after.Version != task.Version || after.Status != task.Status {
		t.Fatal(after, e)
	}
}
func TestFormNavigationCreationBackAndIsolation(t *testing.T) {
	h, _, w := semanticFixture(t)
	h.click(t, 900001, "Добавить")
	requireFormButtons(t, h, false)
	h.message(t, 900001, "Прежнее имя")
	requireFormButtons(t, h, true)
	h.click(t, 900001, "Назад")
	h.message(t, 900001, "Новое имя")
	for _, v := range []string{"4", "5", "oops", "10", "2"} {
		h.message(t, 900001, v)
		requireFormButtons(t, h, true)
	}
	h.click(t, 900001, "Главное меню")
	if _, e := h.bot.Inv.ForUser(1).MaterialByName(w, "Новое имя"); e == nil {
		t.Fatal("saved without confirm")
	}
	h.message(t, 900001, "/materials")
	h.click(t, 900001, "Редактировать")
	h.message(t, 900001, "1")
	var old buttonAction
	for _, a := range h.bot.buttons {
		if a.Action == "form_cancel" {
			old = a
		}
	}
	other, e := h.bot.WS.CreateOwnedWorkshop(1, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if e = h.bot.executeButton(buttonAction{Key: sessionKey{900001, 1}, Action: "switch_to", Workshop: other}); e != nil {
		t.Fatal(e)
	}
	if e = h.bot.executeButton(old); e == nil {
		t.Fatal("old workshop form accepted")
	}
}
