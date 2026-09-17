package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
)

var taskStart = regexp.MustCompile(`(?i)^(?:нужно произвести|сделаем|произвести)\s+(\d+)\s+(.+?)[.!]?$`)
var taskNumber = regexp.MustCompile(`(?i)^(?:нет,?\s*(?:сделай\s*)?|сделай\s+|произведено\s+|готово\s+)?(\d+)[.!]?$`)

func TaskCommand(text string) string {
	t := strings.TrimRight(strings.ToLower(strings.TrimSpace(text)), ".!?")
	switch t {
	case "задачи", "задача", "📋 текущая задача", "на чем мы остановились", "на чём мы остановились", "что сейчас нужно от меня", "что от меня нужно", "что в работе":
		return "/task"
	case "поставь задачу на паузу", "поставь пока на паузу", "пауза", "сделай паузу":
		return "/task pause"
	case "продолжим", "продолжай", "продолжим задачу":
		return "/task resume"
	case "задача выполнена":
		return "/task complete"
	case "запускай":
		return "/task confirm_plan"
	case "всё правильно", "все правильно":
		return "/task verify_result"
	}
	return text
}
func TaskStatus(t *memory.Task, detailed bool) string {
	phases := map[string]string{"planning": "Планирование", "execution": "Выполнение", "validation": "Проверка", "done": "Завершено"}
	steps := map[string]string{"select_product": "Выбор товара", "set_quantity": "Подтверждение количества", "confirm_plan": "Подтверждение плана", "start_production": "Начало выполнения", "record_result": "Ввод результата", "verify_result": "Проверка результата", "confirm_completion": "Подтверждение завершения", "completed": "Завершено"}
	expected := map[string]string{"select_product": "Выберите товар: /task product <ID> (ID показаны ниже).", "confirm_quantity": "Укажите плановое количество: /task quantity 25 или «Нет, 25». Даже предложенное количество нужно подтвердить.", "confirm_plan": "Подтвердите план кнопкой или /task confirm_plan.", "start_production": "Подтвердите начало шага: /task start_production.", "user_input_produced_quantity": "Укажите количество фактически произведённых изделий: /task result 25.", "verify_result": "Проверьте введённый результат: /task verify_result или /task correct_result.", "confirm_completion": "Завершите проверку: /task complete.", "none": "Ничего: задача завершена."}
	result := fmt.Sprintf("Задача #%s\nПроизводственный план: %g шт. %s\nЭтап: %s\nШаг: %s\nОжидается: %s\nСтатус: %s", t.ID, t.State.Quantity, t.State.ProductName, phases[t.Phase], steps[t.CurrentStep], expected[t.ExpectedAction], t.Status)
	result += fmt.Sprintf("\nАвтор: #%d\nИсполнитель: #%d", t.CreatedByUserID, t.AssignedToUserID)
	if t.State.ProducedQuantity != nil {
		result += fmt.Sprintf("\nЗаявленный результат: %g шт.", *t.State.ProducedQuantity)
	}
	if t.Status == "paused" {
		result += "\nНа паузе. Для продолжения: /task resume " + t.ID
	}
	if detailed {
		result += fmt.Sprintf("\nType: %s · phase=%s · step=%s · version=%d", t.Type, t.Phase, t.CurrentStep, t.Version)
	}
	for _, cmd := range []string{"/task product <ID>", "/task quantity 25", "/task result 25", "/task confirm_plan", "/task start_production", "/task verify_result", "/task correct_result", "/task complete"} {
		result = strings.ReplaceAll(result, cmd, cmd+" "+t.ID)
	}
	return result + "\nДля выпуска и списания компонентов используйте «Завершить задачу» и проверьте финальный расчёт."
}

// TaskMessage returns handled=false for legacy workflows and unrelated messages.
func (a *WorkshopAgent) TaskMessage(user, workshop int64, text string) (bool, string, error) {
	m := a.Memory.ForUser(user)
	sc := memory.Scope{UserID: user, WorkshopID: workshop}
	original := text
	text = TaskCommand(text)
	start := taskStart.FindStringSubmatch(text)
	explicit := text == "/task" || strings.HasPrefix(text, "/task ")
	if start == nil && !explicit {
		if taskNumber.FindStringSubmatch(text) == nil {
			return false, "", nil
		}
		active, err := m.ActiveWorking(sc)
		if err != nil {
			return true, "", err
		}
		if active == nil || active.FSMVersion != 1 {
			return false, "", nil
		}
	}
	f := memory.TaskStateMachine{Memory: m}
	parts := strings.Fields(text)
	action := "show"
	arg := ""
	if explicit && len(parts) > 1 {
		action = parts[1]
		if len(parts) > 2 {
			arg = parts[2]
		}
	}
	var t *memory.Task
	var err error
	if start != nil || action == "new" {
		data := memory.TaskState{}
		if start != nil {
			data.Quantity, _ = strconv.ParseFloat(start[1], 64)
			items, e := a.Prod.ForUser(user).ListProducts(workshop)
			if e != nil {
				return true, "", e
			}
			name := strings.ToLower(strings.TrimSpace(start[2]))
			matches := 0
			for _, p := range items {
				if strings.EqualFold(fmt.Sprint(p["name"]), name) {
					data.ProductID = p["id"].(int64)
					data.ProductName = fmt.Sprint(p["name"])
					matches++
				}
			}
			if matches != 1 {
				data.ProductID = 0
				data.ProductName = ""
				data.Parameters = map[string]string{"requested_product": start[2]}
			}
		}
		t, err = f.Create(sc, data)
	} else {
		list, e := m.Tasks(sc)
		if e != nil {
			return true, "", e
		}
		// Explicit IDs select only an owned task. Never guess among paused tasks.
		selectID := arg
		if action == "quantity" || action == "product" || action == "result" {
			selectID = ""
			if len(parts) > 3 {
				selectID = parts[3]
			}
		}
		if selectID != "" {
			sc.TaskID = selectID
			t, err = m.Task(sc)
		} else {
			for _, item := range list {
				if item.Status == "active" || item.Status == "waiting_input" {
					t = item
					break
				}
			}
			if t == nil && len(list) == 1 {
				t = list[0]
			}
			if t == nil && len(list) > 1 {
				lines := []string{"Выберите задачу; параметры заново вводить не нужно:"}
				for _, item := range list {
					lines = append(lines, fmt.Sprintf("%s — %g шт. %s · %s/%s\n/task resume %s", item.ID, item.State.Quantity, item.State.ProductName, item.Phase, item.CurrentStep, item.ID))
				}
				return true, strings.Join(lines, "\n"), nil
			}
		}
		if err != nil {
			return true, "", err
		}
		if t == nil {
			if action == "show" {
				return false, "", nil
			}
			return true, memory.ErrNoTask.Error(), nil
		}
		if t.FSMVersion == 0 {
			if t.Type != "assembly" && t.Type != "production_plan" {
				return false, "", nil
			}
			if action == "show" {
				return true, a.taskAnswer(user, workshop, t, nil) + "\nСтатус: " + t.Status + ". Это текущая рабочая задача.\nПауза: /task pause · продолжение: /task resume", nil
			}
			if action != "pause" && action != "resume" && action != "cancel" && action != "trace" {
				return false, "", nil
			}
		}
		sc.TaskID = t.ID
		if !explicit {
			if match := taskNumber.FindStringSubmatch(original); match != nil {
				arg = match[1]
				action = "quantity"
				if t.CurrentStep == "record_result" {
					action = "result"
				}
			}
		}
		if action == "trace" {
			history, e := m.TaskHistory(sc)
			if e != nil {
				return true, "", e
			}
			raw, _ := json.MarshalIndent(struct {
				Task    *memory.Task
				History any
			}{t, history}, "", "  ")
			return true, string(raw), nil
		}
		if action != "show" {
			intent := memory.TaskIntent{Action: action, Version: t.Version}
			switch action {
			case "quantity", "result":
				value, e := strconv.ParseFloat(strings.ReplaceAll(arg, ",", "."), 64)
				if e != nil {
					return true, "Введите число: /task " + action + " 25", nil
				}
				intent.Quantity = &value
				intent.Action = "set_quantity"
				if action == "result" {
					intent.Action = "record_result"
				}
			case "product":
				intent.ProductID, _ = strconv.ParseInt(arg, 10, 64)
				intent.Action = "select_product"
			}
			t, err = f.Apply(sc, intent)
		}
	}
	if err != nil {
		return true, "", err
	}
	p, e := personalization.New(a.WS.DB()).ForUser(user).GetProfile()
	if e != nil {
		return true, "", e
	}
	answer := TaskStatus(t, p.Detail == "detailed")
	if action == "resume" {
		answer = "Продолжаем с сохранённого шага.\n" + answer
	}
	if t.CurrentStep == "select_product" {
		items, e := a.Prod.ForUser(user).ListProducts(workshop)
		if e != nil {
			return true, "", e
		}
		for _, p := range items {
			answer += fmt.Sprintf("\nID %v — %v", p["id"], p["name"])
		}
	}
	return true, answer, nil
}
