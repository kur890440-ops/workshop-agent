package telegram

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
)

type completionContext struct {
	Task              string
	Workshop, Session int64
	Expires           time.Time
	Input             bool
}

func (b *Bot) rememberCompletion(key sessionKey, w int64, id string) error {
	sc, e := b.Agent.Memory.ForUser(key.UserID).EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: w}, key.ChatID)
	if e != nil {
		return e
	}
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	if b.completions == nil {
		b.completions = map[sessionKey]completionContext{}
	}
	b.completions[key] = completionContext{Task: id, Workshop: w, Session: sc.SessionID, Expires: time.Now().Add(15 * time.Minute)}
	return nil
}

var completionPhrase = regexp.MustCompile(`(?i)^(?:заверши задачу|закончи задачу|завершить задачу|закончить задачу|задача выполнена|сборка завершена|заверши|закончи|завершить|закончить|закончил|готово)(?:\s+([^\s]+)(?:\s+(?:штуки|штук|шт\.?))?)?$`)

func completionCommand(text string) (bool, string, string) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "/task complete") {
		p := strings.Fields(text)
		if len(p) < 2 || p[1] != "complete" {
			return false, "", ""
		}
		if len(p) == 2 {
			return true, "", ""
		}
		if len(p) == 3 {
			return true, p[2], ""
		}
		return false, "", ""
	}
	if m := completionPhrase.FindStringSubmatch(text); m != nil {
		if len(m[1]) == 32 {
			if _, e := strconv.ParseUint(m[1][:8], 16, 32); e == nil {
				return true, m[1], ""
			}
		}
		return true, "", m[1]
	}
	return false, "", ""
}
func (b *Bot) completionMessage(key sessionKey, text string) (bool, error) {
	if b.Agent == nil || (b.getSetup(key.ChatID, key.UserID) != nil && text != "/setup_stop") {
		return false, nil
	}
	command, id, number := completionCommand(text)
	b.uiMu.Lock()
	ctx, ok := b.completions[key]
	b.uiMu.Unlock()
	if !command && (!ok || !ctx.Input || strings.HasPrefix(text, "/")) {
		if text != "/setup_stop" {
			return false, nil
		}
	}
	if !command && !ok {
		return false, nil
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return command, e
	}
	m := b.Agent.Memory.ForUser(key.UserID)
	sc, e := m.EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: w}, key.ChatID)
	if e != nil {
		return true, e
	}
	valid := ok && ctx.Workshop == w && ctx.Session == sc.SessionID && time.Now().Before(ctx.Expires)
	if text == "/setup_stop" {
		b.uiMu.Lock()
		delete(b.completions, key)
		b.uiMu.Unlock()
		if e = m.CancelCompletion(sc, key.ChatID); e != nil {
			return true, e
		}
		return false, nil
	}
	if !command {
		if !valid {
			return false, nil
		}
		id = ctx.Task
		number = strings.TrimSpace(text)
	} else if id == "" && valid {
		id = ctx.Task
	}
	if id == "" {
		t, e := m.ActiveWorking(sc)
		if e != nil {
			return true, e
		}
		if t != nil {
			id = t.ID
		}
	}
	if id == "" {
		if e = b.sendMessage(key.ChatID, "Выберите задачу для завершения из списка."); e != nil {
			return true, e
		}
		return true, b.ordersMenu(key, w, 0)
	}
	return true, b.completionStart(key, w, id, number)
}
func (b *Bot) completionStart(key sessionKey, w int64, id, number string) error {
	m := b.Agent.Memory.ForUser(key.UserID)
	sc := memory.Scope{UserID: key.UserID, WorkshopID: w, TaskID: id}
	t, e := m.Task(sc)
	if e != nil {
		return e
	}
	if e = memory.TaskPermission(b.WS.DB(), key.UserID, w, t); e != nil {
		return e
	}
	if e = b.rememberCompletion(key, w, id); e != nil {
		return e
	}
	if e = m.CancelCompletion(sc, key.ChatID); e != nil {
		return e
	}
	if t.Status == "completed" {
		p, e := m.CompletionReceipt(sc)
		if errors.Is(e, sql.ErrNoRows) {
			return b.sendMessage(key.ChatID, "Задача завершена ранее без складского проведения. Автоматического списания не будет.")
		}
		if e != nil {
			return e
		}
		return b.productionReceipt(key, p)
	}
	if t.Phase == "execution" {
		return b.denyExecutionCompletion(key, w, t.ID, t.Phase)
	}
	if t.Status != "active" || t.Phase != "validation" {
		if e = b.sendMessage(key.ChatID, "Сначала выполните текущий шаг задачи. Начало и возобновление требуют отдельного подтверждения."); e != nil {
			return e
		}
		return b.orderCard(key, w, id)
	}
	if number == "" {
		b.uiMu.Lock()
		c := b.completions[key]
		c.Input = true
		b.completions[key] = c
		b.uiMu.Unlock()
		previous := ""
		if t.State.ProducedQuantity != nil {
			previous = fmt.Sprintf(" Ранее указано: %g шт.", *t.State.ProducedQuantity)
		}
		return b.screen(key, fmt.Sprintf("Задача: %s. Этап: %s.\nПо плану: %g шт.%s\nСколько фактически изготовлено? Введите положительное целое число. Отмена: /setup_stop.", t.State.ProductName, t.Phase, t.State.Quantity, previous)+b.assignmentText(key, w, t.CreatedByUserID, t.AssignedToUserID), choice{Text: "Отмена", Action: "completion_cancel", Workshop: w})
	}
	n, e := pieceCount(number)
	if e != nil {
		b.uiMu.Lock()
		c := b.completions[key]
		c.Input = true
		b.completions[key] = c
		b.uiMu.Unlock()
		return b.sendMessage(key.ChatID, e.Error()+" Нулевой выпуск не завершает производство. Можно продолжить работу или отдельно отменить задачу.")
	}
	sc, e = m.EnsureSession(sc, key.ChatID)
	if e != nil {
		return e
	}
	preview, e := m.PrepareCompletion(sc, key.ChatID, float64(n))
	if e != nil {
		return e
	}
	var workshopName string
	_ = b.WS.DB().QueryRow("SELECT name FROM workshops WHERE id=?", w).Scan(&workshopName)
	text := fmt.Sprintf("Завершить задачу?\nМастерская: %s\nТовар: %s\nПлан: %g шт. Факт: %d шт.", workshopName, t.State.ProductName, t.State.Quantity, n) + b.assignmentText(key, w, t.CreatedByUserID, t.AssignedToUserID)
	text += "\nБудет списано:"
	for _, r := range preview.Plan.Materials {
		text += fmt.Sprintf("\n%s: %s. Остаток: %s → %s", r.Name, inventory.FormatQuantity(r.Quantity, r.DisplayUnit), inventory.FormatQuantity(r.Before, r.DisplayUnit), inventory.FormatQuantity(r.After, r.DisplayUnit))
	}
	text += fmt.Sprintf("\nБудет оприходовано: %d шт. готового товара. Остаток: %g → %g шт.", n, preview.Plan.ProductBefore, preview.Plan.ProductAfter)
	choices := []choice{{Text: "Изменить результат", Action: "completion_edit", Workshop: w, Value: id}, {Text: "Отмена", Action: "completion_cancel", Workshop: w}}
	if shortage := preview.Plan.Shortage(); shortage != "" {
		text = "Нельзя завершить со списанием:" + shortage
		choices = append(choices, choice{Text: "Материалы", Action: "completion_materials", Workshop: w})
	} else {
		choices = append([]choice{{Text: "Завершить и списать", Action: "completion_post", Workshop: w, Value: preview.Token}}, choices...)
	}
	return b.completionScreen(key, text, choices...)
}
func (b *Bot) completionScreen(key sessionKey, text string, choices ...choice) error {
	parts := materialMessageParts(text)
	for _, p := range parts[:len(parts)-1] {
		if e := b.sendMessage(key.ChatID, p); e != nil {
			return e
		}
	}
	return b.screen(key, parts[len(parts)-1], choices...)
}
func (b *Bot) productionReceipt(key sessionKey, p products.ProductionPlan) error {
	text := fmt.Sprintf("Задача завершена. Выпуск проведён: %s — %g шт.\nЗапись производства №%d.\nСписано:", p.ProductName, p.Quantity, p.RecordID)
	for _, r := range p.Materials {
		text += fmt.Sprintf("\n%s: %s. Остаток после проведения: %s.", r.Name, inventory.FormatQuantity(r.Quantity, r.DisplayUnit), inventory.FormatQuantity(r.After, r.DisplayUnit))
	}
	text += fmt.Sprintf("\nГотовый товар: +%g шт. Остаток после проведения: %g шт.", p.Quantity, p.ProductAfter)
	return b.sendMessage(key.ChatID, text)
}
func (b *Bot) completionButton(a buttonAction) error {
	m := b.Agent.Memory.ForUser(a.Key.UserID)
	sc, e := m.EnsureSession(memory.Scope{UserID: a.Key.UserID, WorkshopID: a.Workshop}, a.Key.ChatID)
	if e != nil {
		return e
	}
	switch a.Action {
	case "completion_open":
		return b.completionStart(a.Key, a.Workshop, a.Value, "")
	case "completion_edit":
		if e = m.CancelCompletion(sc, a.Key.ChatID); e != nil {
			return e
		}
		return b.completionStart(a.Key, a.Workshop, a.Value, "")
	case "completion_cancel":
		if e = m.CancelCompletion(sc, a.Key.ChatID); e != nil {
			return e
		}
		b.uiMu.Lock()
		delete(b.completions, a.Key)
		b.uiMu.Unlock()
		return b.sendMessage(a.Key.ChatID, "Завершение отменено. Задача и склад не изменены.")
	case "completion_materials":
		return b.materialsMenu(a.Key, a.Workshop)
	case "completion_post":
		p, e := m.PostCompletion(sc, a.Key.ChatID, a.Value)
		if e != nil {
			var shortage *products.PostingError
			if errors.Is(e, products.ErrProductionChanged) || errors.As(e, &shortage) {
				var id string
				if lookup := b.WS.DB().QueryRow("SELECT task_id FROM task_completion_intents WHERE id=? AND user_id=? AND workshop_id=? AND chat_id=?", a.Value, a.Key.UserID, a.Workshop, a.Key.ChatID).Scan(&id); lookup != nil {
					return lookup
				}
				if send := b.sendMessage(a.Key.ChatID, publicError(e)); send != nil {
					return send
				}
				return b.completionStart(a.Key, a.Workshop, id, strconv.FormatFloat(p.Quantity, 'f', 0, 64))
			}
			return e
		}
		return b.productionReceipt(a.Key, p)
	}
	return nil
}
