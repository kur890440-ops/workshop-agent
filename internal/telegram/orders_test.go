package telegram

import (
	"fmt"
	"strings"
	"testing"
	"workshop-agent/internal/memory"
)

func TestOrdersReadSelectionAndLegacyAdoption(t *testing.T) {
	h, stub, w, p := assemblyFixture(t)
	m := h.bot.Agent.Memory.ForUser(1)
	sc := memory.Scope{UserID: 1, WorkshopID: w}
	original, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{ProductID: p, ProductName: "Фигурка Б", Quantity: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"/tasks", "Что сейчас в работе?", "заказ", "заказы", "  ТЕКУЩИЕ ЗАКАЗЫ  ", "покажи заказы", "список заказов", "собрать заказ"} {
		h.message(t, 900001, text)
		requireAnswer(t, h, "Учёт заказов пока не настроен")
		requireAnswer(t, h, "5 шт.")
		if taskCount(t, h) != 1 {
			t.Fatal("list mutated tasks")
		}
	}
	h.click(t, 900001, "1. Фигурка Б")
	requireAnswer(t, h, "5 шт.")
	sc.TaskID = original.ID
	before, err := m.Task(sc)
	if err != nil || before.FSMVersion != 0 || before.Version != 1 {
		t.Fatal(before, err)
	}
	h.click(t, 900001, "Подготовить задачу к сборке")
	h.click(t, 900001, "Подтвердить")
	after, err := m.Task(sc)
	if err != nil || after.ID != original.ID || after.FSMVersion != 1 || after.CurrentStep != "confirm_task" || after.Status != "active" {
		t.Fatal(after, err)
	}
	h.click(t, 900001, "Подтвердить текущий шаг")
	h.click(t, 900001, "Подтвердить")
	after, err = m.Task(sc)
	if err != nil || after.Phase != "execution" || taskCount(t, h) != 1 {
		t.Fatal(after, err)
	}
	h.click(t, 900001, "Поставить на паузу")
	h.click(t, 900001, "Подтвердить")
	h.message(t, 900001, "заказы")
	h.click(t, 900001, "1. Фигурка Б")
	h.click(t, 900001, "Продолжить сборку")
	h.click(t, 900001, "Подтвердить")
	after, err = m.Task(sc)
	if err != nil || after.Status != "active" || after.Phase != "execution" {
		t.Fatal(after, err)
	}
	if stub.calls != 0 {
		t.Fatal("local list called model")
	}
}
func TestAssemblyMenuDraftEditsAndFSM(t *testing.T) {
	h, _, w, _ := assemblyFixture(t)
	p, err := h.bot.Prod.ForUser(1).CreateProduct(w, "Дефлектор Ballu", "", "product", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "заказы")
	requireAnswer(t, h, "Текущих задач сборки нет")
	h.message(t, 900001, "собрать дефлектор")
	requireAnswer(t, h, "Подойдёт")
	h.click(t, 900001, "Выбрать")
	requireAnswer(t, h, "Сколько единиц")
	h.message(t, 900001, "5")
	requireAnswer(t, h, "Дефлектор Ballu — 5 шт.")
	requireAnswer(t, h, "BOM отсутствует")
	if taskCount(t, h) != 0 {
		t.Fatal("created before confirmation")
	}
	h.message(t, 900001, "Нет, 10")
	requireAnswer(t, h, "10 шт.")
	h.click(t, 900001, "Изменить количество")
	h.message(t, 900001, "3")
	h.click(t, 900001, "Создать задачу")
	task, err := h.bot.Agent.Memory.ForUser(1).ActiveWorking(memory.Scope{UserID: 1, WorkshopID: w})
	if err != nil || task == nil || task.State.Quantity != 3 || task.State.ProductID != p || task.FSMVersion != 1 || task.CurrentStep != "confirm_task" || task.Phase != "planning" {
		t.Fatal(task, err)
	}
	h.click(t, 900001, "Создать задачу")
	if taskCount(t, h) != 1 {
		t.Fatal("duplicate")
	}
	h.message(t, 900001, "новый заказ")
	requireAnswer(t, h, "У вас уже есть активная задача")
	h.click(t, 900001, "Поставить текущую на паузу и продолжить")
	selected, err := h.bot.Agent.Memory.ForUser(1).Task(memory.Scope{UserID: 1, WorkshopID: w, TaskID: task.ID})
	if err != nil || selected.Status != "paused" {
		t.Fatal(selected, err)
	}
	h.message(t, 900001, "/setup_stop")
	if taskCount(t, h) != 1 {
		t.Fatal("cancel created task")
	}
}
func TestOrdersPaginationOwnershipAndTerminalFilter(t *testing.T) {
	h, _, w, p := assemblyFixture(t)
	for i := 0; i < 10; i++ {
		_, err := h.bot.WS.DB().Exec(`INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status) VALUES(1,?,?,'assembly',?,'paused')`, w, fmt.Sprintf("paused-%02d", i), fmt.Sprintf(`{"product_id":%d,"product_name":"Товар %d","quantity":5}`, p, i))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := h.bot.WS.DB().Exec(`INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status) VALUES(1,?,'closed','assembly','{"product_name":"НЕ ПОКАЗЫВАТЬ"}','completed')`, w)
	if err != nil {
		t.Fatal(err)
	}
	h.message(t, 900001, "заказы")
	if strings.Contains(lastAnswer(h), "НЕ ПОКАЗЫВАТЬ") {
		t.Fatal(lastAnswer(h))
	}
	h.click(t, 900001, "Далее")
	requireAnswer(t, h, "9.")
	h.message(t, 900002, "/start")
	_, token, err := h.bot.WS.CreateInvite(1, w, "ADMIN", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var other int64
	if err = h.bot.WS.DB().QueryRow("SELECT id FROM users WHERE telegram_user_id=?", 900002).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = h.bot.WS.AcceptInvite(other, token); err != nil {
		t.Fatal(err)
	}
	h.message(t, 900002, "заказы")
	requireAnswer(t, h, "Товар 9")
	h.click(t, 900002, "Мои задачи")
	requireAnswer(t, h, "Текущих задач сборки нет")
	h.click(t, 900002, "9. Товар 1")
	if strings.Contains(lastAnswer(h), "Задача сборки #") {
		t.Fatal("foreign callback")
	}
}

func TestAssemblySemanticEntryAndStaleConfirmation(t *testing.T) {
	h, stub, w, _ := assemblyFixture(t)
	stub.fail = false
	stub.raw = `{"action":"list_assembly_tasks"}`
	h.message(t, 900001, "покажи незаконченные сборки")
	requireAnswer(t, h, "Текущих задач сборки нет")
	stub.raw = `{"action":"start_assembly"}`
	h.message(t, 900001, "не создавай заказ")
	if d, err := h.bot.loadAssembly(sessionKey{900001, 1}, w); err != nil || d != nil {
		t.Fatal(d, err)
	}
	h.message(t, 900001, "хочу изготовить 7 изделий")
	h.click(t, 900001, "2. Фигурка Б")
	requireAnswer(t, h, "Фигурка Б — 7 шт.")
	h.click(t, 900001, "Изменить количество")
	// The confirmation from the previous revision must not consume the draft.
	h.click(t, 900001, "Создать задачу")
	if taskCount(t, h) != 0 {
		t.Fatal("stale confirmation created task")
	}
	for _, bad := range []string{"0", "-1", "2.5", "NaN", "Inf"} {
		h.message(t, 900001, bad)
		requireAnswer(t, h, "целое число")
	}
	h.message(t, 900001, "9")
	h.click(t, 900001, "Создать задачу")
	active, err := h.bot.Agent.Memory.ForUser(1).ActiveWorking(memory.Scope{UserID: 1, WorkshopID: w})
	if err != nil || active == nil || active.State.Quantity != 9 || active.FSMVersion != 1 {
		t.Fatal(active, err)
	}
}
