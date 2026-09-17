package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/memory"
)

type taskButtonData struct {
	ID      string
	Version int
	Action  string
}

func (b *Bot) taskMessage(key sessionKey, text string) (bool, error) {
	if b.Agent == nil {
		return false, nil
	}
	if normalized := agent.TaskCommand(text); strings.HasPrefix(normalized, "/task complete") {
		return b.completionMessage(key, normalized)
	}
	if b.getSetup(key.ChatID, key.UserID) != nil && !strings.HasPrefix(text, "/task") {
		return false, nil
	}
	// Avoid requiring a workshop for every ordinary message.
	normalized := agent.TaskCommand(text)
	if normalized == text && !strings.HasPrefix(text, "/task") {
		low := strings.ToLower(text)
		if !strings.HasPrefix(low, "сделаем ") && !strings.HasPrefix(low, "нужно произвести ") && !strings.HasPrefix(low, "произвести ") && !strings.HasPrefix(low, "нет") && !strings.HasPrefix(low, "сделай ") && !strings.HasPrefix(low, "произведено ") && !strings.HasPrefix(low, "готово ") {
			numeric := strings.Trim(text, "0123456789.! ") == ""
			if !numeric {
				return false, nil
			}
		}
	}
	w, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		return false, nil
	}
	if !strings.HasPrefix(text, "/task") {
		if d, e := b.loadAssembly(key, w); e != nil {
			return true, e
		} else if d != nil {
			return false, nil
		}
	}
	handled, answer, err := b.Agent.TaskMessage(key.UserID, w, text)
	if !handled {
		return false, nil
	}
	if err != nil {
		return true, b.sendMessage(key.ChatID, publicError(err))
	}
	if answer == memory.ErrNoTask.Error() {
		if err = b.sendMessage(key.ChatID, "Вам не назначена текущая задача. Общий список мастерской может содержать задачи других исполнителей. Выберите задачу ниже и используйте действие в её карточке; права будут проверены."); err != nil {
			return true, err
		}
		return true, b.ordersMenu(key, w, 0)
	}
	return true, b.taskScreen(key, w, answer)
}
func (b *Bot) taskScreen(key sessionKey, w int64, answer string) error {
	b.uiMu.Lock()
	for id, a := range b.buttons {
		if a.Key == key && strings.HasPrefix(a.Action, "fsm_") {
			delete(b.buttons, id)
		}
	}
	b.uiMu.Unlock()
	tasks, err := b.Agent.Memory.ForUser(key.UserID).Tasks(memory.Scope{UserID: key.UserID, WorkshopID: w})
	if err != nil {
		return err
	}
	choices := []choice{}
	for _, t := range tasks {
		if memory.TaskPermission(b.WS.DB(), key.UserID, w, t) != nil {
			continue
		}
		if t.FSMVersion != 1 && t.Type != "assembly" && t.Type != "production_plan" {
			continue
		}
		add := func(label, action string) {
			raw, _ := json.Marshal(taskButtonData{t.ID, t.Version, action})
			choices = append(choices, choice{Text: label, Action: "fsm_apply", Workshop: w, Value: string(raw)})
		}
		if t.Status == "paused" {
			add(fmt.Sprintf("▶️ Продолжить %s", shortTaskID(t.ID)), "resume")
		} else {
			add("⏸ Пауза", "pause")
			if t.ExpectedActionType == "USER_CONFIRMATION" {
				action := t.ExpectedAction
				if action == "confirm_completion" {
					action = "complete"
				}
				add("✅ Подтвердить текущий шаг", action)
			}
			if t.CurrentStep == "set_quantity" && t.State.Quantity > 0 {
				add("✅ Подтвердить количество", "accept_quantity")
			}
		}
		add("❌ Отменить "+shortTaskID(t.ID), "cancel")
	}
	parts := materialMessageParts(answer)
	if len(parts) == 0 {
		return nil
	}
	for _, part := range parts[:len(parts)-1] {
		if err = b.sendMessage(key.ChatID, part); err != nil {
			return err
		}
	}
	return b.screen(key, parts[len(parts)-1], choices...)
}
func (b *Bot) taskButton(a buttonAction) error {
	var data taskButtonData
	if err := json.Unmarshal([]byte(a.Value), &data); err != nil {
		return err
	}
	sc := memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop, TaskID: data.ID}
	m := b.Agent.Memory.ForUser(a.Key.UserID)
	intent := memory.TaskIntent{Action: data.Action, Version: data.Version}
	if data.Action == "complete" {
		return b.completionStart(a.Key, a.Workshop, data.ID, "")
	}
	if data.Action == "accept_quantity" {
		t, err := m.Task(sc)
		if err != nil {
			return err
		}
		intent.Action = "set_quantity"
		intent.Quantity = &t.State.Quantity
	}
	t, err := (memory.TaskStateMachine{Memory: m}).Apply(sc, intent)
	if err != nil {
		return err
	}
	return b.taskScreen(a.Key, a.Workshop, agent.TaskStatus(t, false))
}

func shortTaskID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
