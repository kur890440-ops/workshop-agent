package telegram

import (
	"testing"
	"workshop-agent/internal/memory"
)

func TestTaskUIFlowResumeAndStale(t *testing.T) {
	h, _, w := semanticFixture(t)
	p, e := h.bot.Prod.ForUser(1).CreateProduct(w, "Дефлектор", "", "product", 0, 0, "")
	if e != nil {
		t.Fatal(e)
	}
	material, e := h.bot.Inv.ForUser(1).GetMaterialID(w, "Кисточки")
	if e != nil {
		t.Fatal(e)
	}
	if e = h.bot.Prod.ForUser(1).SetBOMItem(w, p, "material", material, 0, 1, "pcs", 0, ""); e != nil {
		t.Fatal(e)
	}
	h.message(t, 900001, "Сделаем 20 Дефлектор")
	h.click(t, 900001, "Выбрать")
	h.message(t, 900001, "Нет, 25.")
	h.click(t, 900001, "Создать задачу")
	requireAnswer(t, h, "Подтверждение плана")
	h.message(t, 900001, "Запускай")
	h.click(t, 900001, "⏸ Пауза")
	h.message(t, 900001, "/session new")
	h.message(t, 900001, "Продолжим")
	requireAnswer(t, h, "25 шт. Дефлектор")
	requireAnswer(t, h, "Начало выполнения")
	h.click(t, 900001, "✅ Подтвердить текущий шаг")
	h.message(t, 900001, "что сейчас нужно от меня?")
	requireAnswer(t, h, "фактически произведённых")
	h.message(t, 900001, "/task result 25")
	h.message(t, 900001, "/task complete")
	requireAnswer(t, h, "Сколько фактически изготовлено")
	h.message(t, 900001, "25")
	h.click(t, 900001, "Завершить и списать")
	requireAnswer(t, h, "Выпуск проведён")
	tasks, e := h.bot.Agent.Memory.ForUser(1).Tasks(memory.Scope{UserID: 1, WorkshopID: w})
	if e != nil || len(tasks) != 0 {
		t.Fatal(tasks, e)
	}
}

func TestLegacyAssemblyPauseResumeInPlace(t *testing.T) {
	h, stub, w := semanticFixture(t)
	p, err := h.bot.Prod.ForUser(1).CreateProduct(w, "Слив боковой Advantix", "", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	original, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{ProductID: p, ProductName: "Слив боковой Advantix", Quantity: 5, Parameters: map[string]string{"packaging": "box"}})
	if err != nil {
		t.Fatal(err)
	}
	sc.TaskID = original.ID
	stub.context = ""
	h.message(t, 900001, "что в работе?")
	requireAnswer(t, h, "5 шт. Слив боковой Advantix")
	requireAnswer(t, h, "Статус: active")
	h.message(t, 900001, "сделай паузу")
	requireAnswer(t, h, "paused")
	task, err := m.Task(sc)
	if err != nil || task.ID != original.ID || task.FSMVersion != 1 || task.Type != "assembly" || task.State.Quantity != 5 || task.State.Parameters["packaging"] != "box" || task.CurrentStep != "confirm_plan" {
		t.Fatal(task, err)
	}
	h.message(t, 900001, "/session new")
	h.message(t, 900001, "продолжим")
	requireAnswer(t, h, "5 шт. Слив боковой Advantix")
	requireAnswer(t, h, "Статус: active")
	if stub.context != "" {
		t.Fatal("exact lifecycle commands used LLM")
	}
	var count int
	if err = h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM working_memory WHERE user_id=1 AND workshop_id=?", w).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = h.bot.WS.DB().QueryRow("SELECT COUNT(*) FROM production_records").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
