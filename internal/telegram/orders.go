package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

func ordersAlias(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(lower, "собрать заказ ") || strings.HasPrefix(lower, "нужно собрать заказ ") {
		return true
	}
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!?")) {
	case "задачи", "список задач", "/orders", "заказ", "заказы", "текущие заказы", "покажи заказы", "список заказов", "собрать заказ", "нужно собрать заказ":
		return true
	}
	return false
}
func newAssemblyAlias(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "новый заказ", "создать заказ", "новая задача сборки":
		return true
	}
	return false
}
func (b *Bot) ordersEntry(key sessionKey, text string) (bool, error) {
	text = strings.TrimSpace(text)
	if b.Agent == nil || b.getSetup(key.ChatID, key.UserID) != nil {
		return false, nil
	}
	if !ordersAlias(text) && !newAssemblyAlias(text) && !assemblyStart.MatchString(text) {
		return false, nil
	}
	w, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		return true, err
	}
	if ordersAlias(text) {
		return true, b.ordersMenu(key, w, 0)
	}
	if newAssemblyAlias(text) {
		return true, b.beginAssembly(key, w, 0, 0, "")
	}
	return b.assemblyMessage(key, text)
}
func (b *Bot) ordersMenu(key sessionKey, w int64, page int, filter ...bool) error {
	mine := len(filter) > 0 && filter[0]
	tasks, err := b.Agent.Memory.ForUser(key.UserID).WorkshopTasks(memory.Scope{UserID: key.UserID, WorkshopID: w}, mine)
	if err != nil {
		return err
	}
	list := []*memory.Task{}
	for _, t := range tasks {
		if t.Type == "assembly" || t.Type == "production_plan" {
			list = append(list, t)
		}
	}
	if page < 0 {
		page = 0
	}
	start := page * 8
	if start >= len(list) {
		page = 0
		start = 0
	}
	end := start + 8
	if end > len(list) {
		end = len(list)
	}
	text := "Учёт заказов пока не настроен. Показаны задачи мастерской.\nЗадачи мастерской:\n"
	choices := []choice{}
	for i := start; i < end; i++ {
		t := list[i]
		status := map[string]string{"planning": "Планирование", "execution": "Выполнение", "validation": "Проверка"}[t.Phase]
		if status == "" {
			status = "Планирование"
		}
		if t.Status == "paused" {
			status = "На паузе"
		}
		text += fmt.Sprintf("%d. %s — %g шт. %s\n", i+1, materialLabel(t.State.ProductName), t.State.Quantity, status)
		text += b.assignmentText(key, w, t.CreatedByUserID, t.AssignedToUserID) + "\n"
		choices = append(choices, choice{Text: fmt.Sprintf("%d. %s", i+1, materialLabel(t.State.ProductName)), Action: "orders_open", Workshop: w, Value: t.ID})
	}
	if len(list) == 0 {
		text = "Учёт заказов пока не настроен.\nТекущих задач сборки нет."
	} else {
		text += "Выберите задачу, чтобы открыть её и перейти к сборке.\nКоманда «пауза» без ID относится к вашей текущей задаче. Для задачи из общего списка используйте кнопки её карточки."
	}
	if page > 0 {
		choices = append(choices, choice{Text: "Назад", Action: "orders_page", Workshop: w, Value: strconv.Itoa(page - 1)})
	}
	if end < len(list) {
		choices = append(choices, choice{Text: "Далее", Action: "orders_page", Workshop: w, Value: strconv.Itoa(page + 1)})
	}
	if auth.Require(b.WS.DB(), key.UserID, w, auth.TasksCreate) == nil {
		choices = append(choices, choice{Text: "Новая задача сборки", Action: "orders_new", Workshop: w})
	}
	choices = append(choices, choice{Text: "Все задачи", Action: "orders_all", Workshop: w}, choice{Text: "Мои задачи", Action: "orders_mine", Workshop: w})
	if mine {
		for i := range choices {
			if choices[i].Action == "orders_page" {
				choices[i].Action = "orders_mine_page"
			}
		}
	}
	return b.screen(key, text, choices...)
}
func (b *Bot) orderCard(key sessionKey, w int64, id string) error {
	t, err := b.Agent.Memory.ForUser(key.UserID).Task(memory.Scope{UserID: key.UserID, WorkshopID: w, TaskID: id})
	if err != nil {
		return err
	}
	text := agent.TaskStatus(t, false)
	if err = b.rememberCompletion(key, w, id); err != nil {
		return err
	}
	if t.FSMVersion == 0 {
		text = fmt.Sprintf("Задача сборки #%s\n%s — %g шт.\nЭтап: планирование\nСтатус: %s\nСуществующий план; склад не изменён.", t.ID, t.State.ProductName, t.State.Quantity, t.Status)
	}
	text += b.assignmentText(key, w, t.CreatedByUserID, t.AssignedToUserID)
	if t.ExpectedActionType == "USER_INPUT" {
		switch t.CurrentStep {
		case "set_quantity":
			text += "\nДля этой задачи: /task quantity 25 " + t.ID
		case "record_result":
			text += "\nДля этой задачи: /task result 25 " + t.ID
		case "select_product":
			text += "\nДля этой задачи: /task product <ID> " + t.ID
		}
	}
	choices := []choice{}
	if auth.Require(b.WS.DB(), key.UserID, w, auth.TasksAssign) == nil {
		raw, _ := json.Marshal(assignmentAction{ID: t.ID, Version: t.Version})
		choices = append(choices, choice{Text: "Сменить исполнителя", Action: "orders_assign", Workshop: w, Value: string(raw)})
	}
	add := func(label, action string) {
		raw, _ := json.Marshal(taskButtonData{ID: id, Version: t.Version, Action: action})
		choices = append(choices, choice{Text: label, Action: "orders_confirm", Workshop: w, Value: string(raw)})
	}
	if memory.TaskPermission(b.WS.DB(), key.UserID, w, t) == nil {
		choices = append(choices, choice{Text: "Завершить задачу", Action: "completion_open", Workshop: w, Value: id})
		if t.Status == "paused" {
			add("Продолжить сборку", "resume")
		} else if t.Status == "active" || t.Status == "waiting_input" {
			if t.FSMVersion == 0 {
				add("Подготовить план к сборке", "adopt_plan")
			} else if t.ExpectedActionType == "USER_CONFIRMATION" {
				action := t.ExpectedAction
				if action == "confirm_completion" {
					action = "complete"
				}
				add("Подтвердить текущий шаг", action)
			}
			add("Поставить на паузу", "pause")
		}
	}
	choices = append(choices, choice{Text: "Назад к заказам", Action: "orders_page", Workshop: w, Value: "0"})
	return b.screen(key, text, choices...)
}
func (b *Bot) ordersButton(a buttonAction) error {
	if strings.HasPrefix(a.Action, "orders_assign") {
		return b.assignmentButton(a)
	}
	switch a.Action {
	case "orders_all":
		return b.ordersMenu(a.Key, a.Workshop, 0)
	case "orders_mine", "orders_mine_page":
		page, _ := strconv.Atoi(a.Value)
		return b.ordersMenu(a.Key, a.Workshop, page, true)
	case "orders_page":
		page, _ := strconv.Atoi(a.Value)
		return b.ordersMenu(a.Key, a.Workshop, page)
	case "orders_new":
		return b.beginAssembly(a.Key, a.Workshop, 0, 0, "")
	case "orders_open":
		return b.orderCard(a.Key, a.Workshop, a.Value)
	case "orders_confirm":
		var d taskButtonData
		if err := json.Unmarshal([]byte(a.Value), &d); err != nil {
			return err
		}
		t, err := b.Agent.Memory.ForUser(a.Key.UserID).Task(memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop, TaskID: d.ID})
		if err != nil {
			return err
		}
		if t.Version != d.Version {
			return memory.ErrTaskChanged
		}
		if d.Action == "resume" && t.AssignedToUserID == a.Key.UserID {
			active, err := b.Agent.Memory.ForUser(a.Key.UserID).ActiveWorking(memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop})
			if err != nil {
				return err
			}
			if active != nil {
				return b.pauseConflict(a.Key, a.Workshop, active, "orders_open", d.ID)
			}
		}
		return b.screen(a.Key, "Подтвердить действие для выбранной задачи?\n"+t.State.ProductName+fmt.Sprintf(" — %g шт.\nДействие: %s", t.State.Quantity, d.Action), choice{Text: "Подтвердить", Action: "orders_apply", Workshop: a.Workshop, Value: a.Value}, choice{Text: "Отмена", Action: "orders_open", Workshop: a.Workshop, Value: d.ID})
	case "orders_apply":
		var d taskButtonData
		if err := json.Unmarshal([]byte(a.Value), &d); err != nil {
			return err
		}
		if d.Action == "complete" {
			return b.completionStart(a.Key, a.Workshop, d.ID, "")
		}
		_, err := (memory.TaskStateMachine{Memory: b.Agent.Memory.ForUser(a.Key.UserID)}).Apply(memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop, TaskID: d.ID}, memory.TaskIntent{Action: d.Action, Version: d.Version})
		if err != nil {
			return err
		}
		return b.orderCard(a.Key, a.Workshop, d.ID)
	}
	return auth.ErrDenied
}
