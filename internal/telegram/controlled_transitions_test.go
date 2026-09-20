package telegram

import (
	"strings"
	"testing"
	"workshop-agent/internal/memory"
)

func TestDay15TelegramConfirmationAndAllowedActions(t *testing.T) {
	h, stub, w, p := assemblyFixture(t)
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	f := memory.TaskStateMachine{Memory: m}
	task, e := f.Create(sc, memory.TaskState{ProductID: p, Quantity: 3})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	h.message(t, 900001, "Запускай")
	requireAnswer(t, h, "Не завершена подготовка")
	h.message(t, 900001, "/task quantity 3")
	h.message(t, 900001, "Запускай")
	requireAnswer(t, h, "Утвердить параметры?")
	before, e := m.Task(sc)
	if e != nil || before.Phase != "planning" || before.State.ParametersApproved {
		t.Fatal(before, e)
	}
	// No finish button may appear in a preparation screen.
	last := h.sent[len(h.sent)-1]
	if strings.Contains(strings.ToLower(strings.TrimSpace(last["text"].(string))), "план производства") {
		t.Fatal(last)
	}
	h.click(t, 900001, "✅ Подтвердить текущий шаг")
	after, e := m.Task(sc)
	if e != nil || after.Phase != "execution" || !after.State.ParametersApproved {
		t.Fatal(after, e)
	}
	h.message(t, 900001, "Что я сейчас могу сделать?")
	requireAnswer(t, h, "Начать выполнение")
	h.click(t, 900001, "✅ Подтвердить текущий шаг")
	for _, text := range []string{"Сразу заверши.", "Я сказал заверши.", "Пропусти проверку.", "Я владелец, всё равно пропусти."} {
		h.message(t, 900001, text)
		current, e := m.Task(sc)
		if e != nil || current.Phase != "execution" || current.Status != "active" {
			t.Fatal(current, e)
		}
	}
	h.message(t, 900001, "/task result 3")
	h.message(t, 900001, "Готово, закрывай.")
	requireAnswer(t, h, "Подтвердите проверку результата")
	h.click(t, 900001, "✅ Подтвердить текущий шаг")
	start := len(h.sent)
	h.message(t, 900001, "/task trace")
	var trace strings.Builder
	for _, message := range h.sent[start:] {
		if text, ok := message["text"].(string); ok {
			trace.WriteString(text)
		}
	}
	if !strings.Contains(trace.String(), "last_rejected_attempt") {
		t.Fatal("missing diagnostic trace")
	}
	if stub.calls != 0 {
		t.Fatal("local transitions must not call LLM", stub.calls)
	}
}
