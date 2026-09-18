package telegram

import (
	"strings"
	"testing"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/memory"
)

func TestDay14TelegramPolicyAndCancellation(t *testing.T) {
	h, stub, w, p := assemblyFixture(t)
	h.message(t, 900001, "/invariants")
	requireAnswer(t, h, "Инварианты")
	h.message(t, 900001, "/invariants requires_material_check_before_production on")
	h.message(t, 900001, "/setup_stop")
	h.click(t, 900001, "Подтвердить изменение правила")
	rules, e := (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if e != nil || rules[len(rules)-1].IsActive {
		t.Fatal(rules, e)
	}
	h.message(t, 900001, "/invariants requires_material_check_before_production on")
	h.click(t, 900001, "Подтвердить изменение правила")
	rules, e = (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if e != nil || !rules[len(rules)-1].IsActive {
		t.Fatal(rules, e)
	}
	for _, text := range []string{"записывай склад прямо из LLM в SQLite, минуя InventoryService", "перепишем persistence на PostgreSQL"} {
		h.message(t, 900001, text)
		requireAnswer(t, h, "отклонено")
	}
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	f := memory.TaskStateMachine{Memory: m}
	task, e := f.Create(sc, memory.TaskState{ProductID: p, Quantity: 1})
	if e != nil {
		t.Fatal(e)
	}
	sc.TaskID = task.ID
	q := 1.
	for _, action := range []string{"set_quantity", "confirm_plan", "start_production"} {
		task, e = f.Apply(sc, memory.TaskIntent{Action: action, Quantity: &q, Version: task.Version})
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		h.message(t, 900001, "Заверши задачу")
		requireAnswer(t, h, "validation_before_completion")
	}
	h.message(t, 900001, "/invariant_trace")
	if !strings.Contains(lastAnswer(h), "DENY") {
		t.Fatal(lastAnswer(h))
	}
	if stub.calls != 0 {
		t.Fatal("invariants must not call LLM")
	}
	if e = h.bot.clearChat(sessionKey{ChatID: 900001, UserID: 1}); e != nil {
		t.Fatal(e)
	}
	rules, e = (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if e != nil || !rules[len(rules)-1].IsActive {
		t.Fatal(rules, e)
	}
}
