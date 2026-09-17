package telegram

import (
	"strings"
	"testing"
	"workshop-agent/internal/memory"
)

func TestTaskAliases(t *testing.T) {
	h := botFixture(t)
	h.message(t, 900001, "/start")
	w, err := h.bot.WS.CreateOwnedWorkshop(1, "A")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"задачи", "ЗАДАЧА", "  Задачи  ", "/task"} {
		h.message(t, 900001, input)
		text := h.sent[len(h.sent)-1]["text"].(string)
		want := "Нет активной задачи"
		if strings.EqualFold(strings.TrimSpace(input), "задачи") {
			want = "Текущих задач сборки нет"
		}
		if !strings.Contains(text, want) || strings.Contains(text, "Токены LLM") {
			t.Fatal(text)
		}
	}
	m := h.bot.Agent.Memory.ForUser(1)
	sc, err := m.EnsureSession(memory.Scope{UserID: 1, WorkshopID: w}, 900001)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateWorkingMemory(sc, "assembly", memory.TaskState{Quantity: 25, ProductName: "Набор"})
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "задачи")
	text := h.sent[len(h.sent)-1]["text"].(string)
	if !strings.Contains(text, "Набор — 25 шт.") {
		t.Fatal(text)
	}
	h.message(t, 900001, "/setup_add_material")
	h.message(t, 900001, "задача")
	s := h.bot.getSetup(900001, 1)
	if s == nil || s.name != "задача" || s.stage != 1 {
		t.Fatal("alias interrupted form")
	}
}
