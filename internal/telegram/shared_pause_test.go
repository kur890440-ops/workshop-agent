package telegram

import (
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

func TestPauseWithoutAssignedTaskOffersSharedList(t *testing.T) {
	h, stub, w, p := assemblyFixture(t)
	m := h.bot.Agent.Memory.ForUser(1)
	task, e := m.CreateWorkingMemory(memory.Scope{UserID: 1, WorkshopID: w}, "assembly", memory.TaskState{ProductID: p, ProductName: "Фигурка Б", Quantity: 3})
	if e != nil {
		t.Fatal(e)
	}
	h.message(t, 900002, "/start")
	var other int64
	if e = h.bot.WS.DB().QueryRow("SELECT id FROM users WHERE telegram_user_id=900002").Scan(&other); e != nil {
		t.Fatal(e)
	}
	_, token, e := h.bot.WS.CreateInvite(1, w, auth.Admin, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.bot.WS.AcceptInvite(other, token); e != nil {
		t.Fatal(e)
	}
	h.message(t, 900002, "задачи")
	start := len(h.sent)
	h.message(t, 900002, "пауза")
	found := false
	for _, s := range h.sent[start:] {
		text, _ := s["text"].(string)
		if strings.Contains(text, "Вам не назначена текущая задача") {
			found = true
		}
		if strings.Contains(text, "Сначала начните новую") {
			t.Fatal(text)
		}
	}
	if !found {
		t.Fatal("missing explanation")
	}
	requireAnswer(t, h, "Фигурка Б — 3 шт.")
	sc := memory.Scope{UserID: 1, WorkshopID: w, TaskID: task.ID}
	current, e := m.Task(sc)
	if e != nil || current.Status != "active" {
		t.Fatal(current, e)
	}
	h.click(t, 900002, "1. Фигурка Б")
	h.click(t, 900002, "Поставить на паузу")
	h.click(t, 900002, "Подтвердить")
	current, e = m.Task(sc)
	if e != nil || current.Status != "paused" {
		t.Fatal(current, e)
	}
	if stub.calls != 0 {
		t.Fatal("local command used LLM")
	}
}
