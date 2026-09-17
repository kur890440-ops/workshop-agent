package telegram

import (
	"fmt"
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

func TestSharedTaskCreationAssignmentAndEmployeeUI(t *testing.T) {
	h, _, w, _ := assemblyFixture(t)
	h.message(t, 900002, "/start")
	var employee int64
	if e := h.bot.WS.DB().QueryRow("SELECT id FROM users WHERE telegram_user_id=900002").Scan(&employee); e != nil {
		t.Fatal(e)
	}
	_, token, e := h.bot.WS.CreateInvite(1, w, auth.Employee, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.bot.WS.AcceptInvite(employee, token); e != nil {
		t.Fatal(e)
	}
	h.message(t, 900001, "собрать 5 фигурок Б")
	h.click(t, 900001, "Выбрать")
	requireAnswer(t, h, "Кому назначить задачу?")
	if taskCount(t, h) != 0 {
		t.Fatal("created before confirmation")
	}
	h.click(t, 900001, fmt.Sprintf("Person (#%d) — EMPLOYEE", employee))
	requireAnswer(t, h, fmt.Sprintf("Исполнитель: Person (#%d)", employee))
	h.click(t, 900001, "Создать задачу")
	m := h.bot.Agent.Memory.ForUser(employee)
	sc := memory.Scope{UserID: employee, WorkshopID: w}
	task, e := m.ActiveWorking(sc)
	if e != nil || task == nil || task.CreatedByUserID != 1 || task.AssignedToUserID != employee {
		t.Fatal(task, e)
	}
	h.message(t, 900001, "задачи")
	requireAnswer(t, h, "Фигурка Б")
	h.click(t, 900001, "Мои задачи")
	requireAnswer(t, h, "Текущих задач сборки нет")
	h.message(t, 900002, "заказы")
	h.click(t, 900002, "1. Фигурка Б")
	requireAnswer(t, h, "Исполнитель")
	h.message(t, 900002, "/task")
	requireAnswer(t, h, "5 шт. Фигурка Б")
	h.message(t, 900002, "сделай паузу")
	requireAnswer(t, h, "paused")
	h.message(t, 900001, "заказы")
	h.click(t, 900001, "1. Фигурка Б")
	h.click(t, 900001, "Сменить исполнителя")
	h.click(t, 900001, "Person (#1) — OWNER")
	h.click(t, 900001, "Подтвердить назначение")
	sc.TaskID = task.ID
	task, e = m.Task(sc)
	if e != nil || task.AssignedToUserID != 1 || task.Status != "paused" {
		t.Fatal(task, e)
	}
	h.message(t, 900002, "/task resume "+task.ID)
	requireAnswer(t, h, "нет прав")
	h.message(t, 900002, "заказы")
	h.click(t, 900002, "1. Фигурка Б")
	if strings.Contains(lastAnswer(h), "PRIVATE") {
		t.Fatal("leak")
	}
}
