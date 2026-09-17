package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

func (b *Bot) assignmentText(key sessionKey, w, creator, executor int64) string {
	name := func(id int64) string {
		var n string
		_ = b.WS.DB().QueryRow("SELECT COALESCE(NULLIF(display_name,''),'User') FROM users WHERE id=?", id).Scan(&n)
		r := []rune(n)
		if len(r) > 60 {
			n = string(r[:60]) + "…"
		}
		return fmt.Sprintf("%s (#%d)", n, id)
	}
	text := "\nАвтор: " + name(creator) + "\nИсполнитель: " + name(executor)
	if auth.Require(b.WS.DB(), executor, w, auth.TasksExecute) != nil {
		text += " — требуется переназначение"
	}
	return text
}
func (b *Bot) chooseDraftExecutor(key sessionKey, w int64, d *assemblyDraft) error {
	members, err := b.Agent.Memory.ForUser(key.UserID).Executors(memory.Scope{UserID: key.UserID, WorkshopID: w})
	if err != nil {
		return err
	}
	if len(members) == 1 && members[0].ID == key.UserID || auth.Require(b.WS.DB(), key.UserID, w, auth.TasksAssign) != nil {
		d.Assignee = key.UserID
		return b.previewAssembly(key, w, d)
	}
	d.Stage = "executor"
	if err = b.storeAssembly(key, w, d); err != nil {
		return err
	}
	choices := []choice{}
	for _, u := range members {
		label := fmt.Sprintf("%s (#%d) — %s", materialLabel(u.Name), u.ID, u.Role)
		if u.ID == key.UserID {
			label = "Назначить себе"
		}
		choices = append(choices, choice{Text: label, Action: "assembly_executor", Workshop: w, Target: d.ID, Value: fmt.Sprintf("%d:%d", d.Revision, u.ID)})
	}
	choices = append(choices, choice{Text: "Отмена", Action: "assembly_cancel", Workshop: w, Target: d.ID, Value: fmt.Sprint(d.Revision)})
	return b.screen(key, "Кому назначить задачу?", choices...)
}

type assignmentAction struct {
	ID      string
	Version int
	User    int64
}

func (b *Bot) assignmentButton(a buttonAction) error {
	m := b.Agent.Memory.ForUser(a.Key.UserID)
	sc := memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop}
	if err := auth.Require(b.WS.DB(), sc.UserID, sc.WorkshopID, auth.TasksAssign); err != nil {
		return err
	}
	var d assignmentAction
	if err := json.Unmarshal([]byte(a.Value), &d); err != nil {
		return err
	}
	sc.TaskID = d.ID
	t, err := m.Task(sc)
	if err != nil {
		return err
	}
	if t.Version != d.Version {
		return memory.ErrTaskChanged
	}
	switch a.Action {
	case "orders_assign":
		members, err := m.Executors(sc)
		if err != nil {
			return err
		}
		choices := []choice{}
		for _, u := range members {
			raw, _ := json.Marshal(assignmentAction{t.ID, t.Version, u.ID})
			choices = append(choices, choice{Text: fmt.Sprintf("%s (#%d) — %s", materialLabel(u.Name), u.ID, u.Role), Action: "orders_assign_confirm", Workshop: sc.WorkshopID, Value: string(raw)})
		}
		choices = append(choices, choice{Text: "Отмена", Action: "orders_open", Workshop: sc.WorkshopID, Value: t.ID})
		return b.screen(a.Key, "Выберите нового исполнителя. Выполняемую задачу сначала поставьте на паузу.", choices...)
	case "orders_assign_confirm":
		return b.screen(a.Key, "Подтвердить смену исполнителя?\nБыл: #"+strconv.FormatInt(t.AssignedToUserID, 10)+b.assignmentText(a.Key, sc.WorkshopID, t.CreatedByUserID, d.User), choice{Text: "Подтвердить назначение", Action: "orders_assign_apply", Workshop: sc.WorkshopID, Value: a.Value}, choice{Text: "Отмена", Action: "orders_open", Workshop: sc.WorkshopID, Value: t.ID})
	case "orders_assign_apply":
		if _, err = m.AssignTask(sc, d.User, d.Version); err != nil {
			if errors.Is(err, memory.ErrForeground) {
				return b.screen(a.Key, "У выбранного исполнителя уже есть текущая задача. Выберите другого исполнителя либо откройте список и явно решите конфликт.", choice{Text: "Другой исполнитель", Action: "orders_assign", Workshop: sc.WorkshopID, Value: a.Value}, choice{Text: "Все задачи", Action: "orders_all", Workshop: sc.WorkshopID}, choice{Text: "Отмена", Action: "orders_open", Workshop: sc.WorkshopID, Value: t.ID})
			}
			return err
		}
		return b.orderCard(a.Key, sc.WorkshopID, t.ID)
	}
	return auth.ErrDenied
}
