package telegram

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"workshop-agent/internal/memory"
)

func TestCompletionPhrasesAndNegation(t *testing.T) {
	for _, s := range []string{"завершить", "закончить", "завершить задачу", "закончить задачу", "задача выполнена", "сборка завершена", "закончил", "  ГОТОВО  ", "готово 3", "закончил 3 штуки", "/task complete"} {
		if ok, _, _ := completionCommand(s); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"не заканчивай", "когда закончим?", "я ещё не закончил", "товары"} {
		if ok, _, _ := completionCommand(s); ok {
			t.Fatal(s)
		}
	}
}
func TestCompletionTelegramCancelPostingAndReplay(t *testing.T) {
	h, stub, w, p := assemblyFixture(t)
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	f := memory.TaskStateMachine{Memory: m}
	task, e := f.Create(sc, memory.TaskState{ProductID: p, Quantity: 5})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	q := 5.
	h.message(t, 900001, "готово 3")
	requireAnswer(t, h, "Подготовка задачи") // planning never auto advances
	current, e := m.Task(sc)
	if e != nil || current.Phase != "planning" {
		t.Fatal(current, e)
	}
	for _, a := range []string{"set_quantity", "confirm_task", "start_production", "record_result", "verify_result"} {
		task, e = f.Apply(sc, memory.TaskIntent{Confirmed: true, Action: a, Quantity: &q, Version: task.Version})
		if e != nil {
			t.Fatal(e)
		}
	}
	h.message(t, 900001, "готово 3")
	requireAnswer(t, h, "Факт: 3")
	before, e := m.Task(sc)
	if e != nil {
		t.Fatal(e)
	}
	h.message(t, 900001, "/setup_stop")
	h.click(t, 900001, "Завершить и списать")
	var count int
	_ = m.DB.QueryRow("SELECT COUNT(*) FROM production_records").Scan(&count)
	if count != 0 {
		t.Fatal("cancel bypass")
	}
	h.message(t, 900001, "закончить задачу")
	requireAnswer(t, h, "Сколько фактически")
	for _, bad := range []string{"0", "-1", "1,5", "NaN", "Inf"} {
		h.message(t, 900001, bad)
		requireAnswer(t, h, "целое число")
	}
	h.message(t, 900001, "3")
	// Simulate losing the success reply after the database transaction commits.
	oldTransport := h.bot.HTTPClient.Transport
	failed := false
	h.bot.HTTPClient.Transport = transport(func(r *http.Request) (*http.Response, error) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !failed && strings.Contains(string(body), "Выпуск проведён") {
			failed = true
			return nil, fmt.Errorf("test network failure")
		}
		return oldTransport.RoundTrip(r)
	})
	h.click(t, 900001, "Завершить и списать")
	if !failed {
		t.Fatal("network failure not exercised")
	}
	h.click(t, 900001, "Завершить и списать")
	requireAnswer(t, h, "Выпуск проведён")
	h.message(t, 900001, "завершить")
	requireAnswer(t, h, "Запись производства №")
	_ = m.DB.QueryRow("SELECT COUNT(*) FROM production_records").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	after, e := m.Task(sc)
	if e != nil || after.Status != "completed" || after.Version != before.Version+1 {
		t.Fatal(after, e)
	}
	if stub.calls != 0 {
		t.Fatal("local completion called model")
	}
}
func TestCompletionSemanticEntry(t *testing.T) {
	h, stub, w, p := assemblyFixture(t)
	stub.fail = false
	stub.raw = `{"action":"task_complete"}`
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	f := memory.TaskStateMachine{Memory: m}
	task, e := f.Create(sc, memory.TaskState{ProductID: p, Quantity: 1})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	q := 1.
	for _, a := range []string{"set_quantity", "confirm_task", "start_production", "record_result", "verify_result"} {
		task, e = f.Apply(sc, memory.TaskIntent{Confirmed: true, Action: a, Quantity: &q, Version: task.Version})
		if e != nil {
			t.Fatal(e)
		}
	}
	h.message(t, 900001, "все изделия изготовлены, пора завершать")
	if !strings.Contains(lastAnswer(h), "Сколько фактически") {
		t.Fatal(lastAnswer(h))
	}
}
