package telegram

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
)

func (b *Bot) denyExecutionCompletion(key sessionKey, w int64, id, phase string) error {
	r := (invariants.InvariantEngine{}).Evaluate(b.WS.DB(), invariants.ProposedAction{ActionType: "complete_task", UserID: key.UserID, WorkshopID: w, TaskID: id}, invariants.Facts{Phase: phase})
	if e := invariants.SaveTrace(b.WS.DB(), r); e != nil {
		return e
	}
	return b.sendMessage(key.ChatID, (&invariants.Denied{Result: r}).Error())
}
func (b *Bot) invariantMessage(key sessionKey, text string) (bool, error) {
	if strings.TrimSpace(text) == "/setup_stop" {
		b.uiMu.Lock()
		for id, a := range b.buttons {
			if a.Key == key && a.Action == "invariant_update" {
				delete(b.buttons, id)
			}
		}
		b.uiMu.Unlock()
		return false, nil
	}
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) == 0 {
		return false, nil
	}
	action := invariants.AdviceAction(text)
	if action == "" && fields[0] != "/invariants" && fields[0] != "/invariant_trace" {
		return false, nil
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return true, e
	}
	if e = auth.Require(b.WS.DB(), key.UserID, w, auth.WorkshopRead); e != nil {
		return true, e
	}
	if action != "" {
		r := (invariants.InvariantEngine{}).Evaluate(b.WS.DB(), invariants.ProposedAction{ActionType: action, UserID: key.UserID, WorkshopID: w}, invariants.Facts{})
		if e = invariants.SaveTrace(b.WS.DB(), r); e != nil {
			return true, e
		}
		return true, b.sendMessage(key.ChatID, (&invariants.Denied{Result: r}).Error())
	}
	if fields[0] == "/invariant_trace" {
		raw, e := invariants.LastTrace(b.WS.DB(), key.UserID, w)
		if errors.Is(e, sql.ErrNoRows) {
			return true, b.sendMessage(key.ChatID, "Проверок пока нет.")
		}
		if e != nil {
			return true, e
		}
		return true, b.sendMessage(key.ChatID, raw)
	}
	rules, e := (invariants.InvariantRegistry{}).Workshop(b.WS.DB(), key.UserID, w)
	if e != nil {
		return true, e
	}
	configurable := rules[len(rules)-1]
	if len(fields) > 1 {
		if e = auth.Require(b.WS.DB(), key.UserID, w, auth.WorkshopManage); e != nil {
			return true, e
		}
		if len(fields) != 3 || fields[1] != invariants.MaterialCheck {
			return true, invariants.ErrProtected
		}
		if fields[2] != "on" && fields[2] != "off" {
			return true, b.sendMessage(key.ChatID, "Значение: on или off.")
		}
		raw, _ := json.Marshal(struct {
			Active  bool
			Version int
		}{fields[2] == "on", configurable.Version})
		return true, b.screen(key, fmt.Sprintf("Изменить правило %s: %t → %t? Версия %d. Запрет отрицательных остатков остаётся. Отмена: /setup_stop", configurable.Key, configurable.IsActive, fields[2] == "on", configurable.Version), choice{Text: "Подтвердить изменение правила", Action: "invariant_update", Workshop: w, Value: string(raw)})
	}
	var out strings.Builder
	out.WriteString("Инварианты мастерской:\n")
	for _, r := range rules {
		fmt.Fprintf(&out, "• %s [%s, %s] — активно: %t, версия: %d\n", r.Title, r.Category, r.Enforcement, r.IsActive, r.Version)
	}
	if auth.Require(b.WS.DB(), key.UserID, w, auth.WorkshopManage) == nil {
		out.WriteString("\nНастройка с подтверждением:\n/invariants requires_material_check_before_production on\n/invariants requires_material_check_before_production off")
	}
	return true, b.sendMessage(key.ChatID, out.String())
}
func (b *Bot) invariantButton(a buttonAction) error {
	var v struct {
		Active  bool
		Version int
	}
	if e := json.Unmarshal([]byte(a.Value), &v); e != nil {
		return e
	}
	if e := invariants.UpdateConfirmed(b.WS.DB(), a.Key.UserID, a.Workshop, invariants.MaterialCheck, v.Active, v.Version, true); e != nil {
		return e
	}
	return b.sendMessage(a.Key.ChatID, "Правило обновлено. Версия увеличена, изменение записано в Audit Log.")
}
