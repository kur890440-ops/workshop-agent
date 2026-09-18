package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/products"
)

var errFormExpired = errors.New("Форма или шаг устарели. Откройте актуальное меню: /materials, /products или /workshop.")

type formFrame struct {
	State   setupSession
	Text    string
	Choices []choice
	Step    string
}
type formNavigation struct {
	Version   int
	MessageID int64
	Current   formFrame
	History   []formFrame
}

func cloneForm(s *setupSession) setupSession {
	c := *s
	c.navigation = nil
	c.materialIDs = append([]int64(nil), s.materialIDs...)
	c.productIDs = append([]int64(nil), s.productIDs...)
	if s.composition != nil {
		v := *s.composition
		v.MaterialIDs = append([]int64(nil), v.MaterialIDs...)
		v.Rows = append([]products.BOMRow(nil), v.Rows...)
		c.composition = &v
	}
	return c
}
func formStep(s *setupSession) string {
	extra := ""
	if s.composition != nil {
		extra = s.composition.Stage + ":" + s.composition.Change.Action
	}
	return fmt.Sprintf("%s:%d:%d:%s:%s", s.kind, s.stage, s.productID, s.edit.Field, extra)
}
func (b *Bot) invalidateForm(key sessionKey) {
	s := b.getSetup(key.ChatID, key.UserID)
	b.uiMu.Lock()
	for id, a := range b.buttons {
		if a.Key == key && a.Form != nil {
			delete(b.buttons, id)
		}
	}
	b.uiMu.Unlock()
	if s != nil && s.navigation != nil && s.navigation.MessageID > 0 {
		id := s.navigation.MessageID
		s.navigation.MessageID = 0
		_ = b.api("editMessageReplyMarkup", map[string]any{"chat_id": key.ChatID, "message_id": id, "reply_markup": map[string]any{"inline_keyboard": [][]inlineButton{}}}, nil)
	}
}
func (b *Bot) formChoices(key sessionKey, s *setupSession, text string, choices []choice) []choice {
	b.invalidateForm(key)
	if s.navigation == nil {
		s.navigation = &formNavigation{}
	}
	n := s.navigation
	step := formStep(s)
	if s.kind == "product_edit" && s.stage == 1 && (s.composition == nil || s.composition.Stage == "menu") {
		n.History = nil
		n.Current = formFrame{}
	}
	if n.Current.Step != "" && n.Current.Step != step {
		n.History = append(n.History, n.Current)
	}
	n.Current = formFrame{cloneForm(s), text, append([]choice(nil), choices...), step}
	n.Version++
	// Existing cancel labels use the same exit semantics as the common controls.
	out := []choice{}
	for _, c := range choices {
		if c.Text != "Отмена" && c.Text != "Назад" && c.Text != "Главное меню" {
			out = append(out, c)
		}
	}
	if len(n.History) > 0 {
		out = append(out, choice{Text: "Назад", Action: "form_back", Workshop: s.workshopID})
	}
	return append(out, choice{Text: "Отмена", Action: "form_cancel", Workshop: s.workshopID}, choice{Text: "Главное меню", Action: "form_home", Workshop: s.workshopID})
}
func (b *Bot) formExit(key sessionKey, s *setupSession, target string) error {
	b.clearSetup(key.ChatID, key.UserID)
	if active, e := b.WS.ActiveWorkshop(key.UserID); e != nil || active != s.workshopID {
		return b.home(key)
	}
	if e := b.sendMessage(key.ChatID, "Ввод остановлен. Несохранённые данные формы отброшены. Сохранённые операции и рабочая задача не изменены."); e != nil {
		return e
	}
	switch target {
	case "home":
		return b.home(key)
	case "switch":
		return b.switcher(key)
	case "task":
		return b.taskScreen(key, s.workshopID, "Текущая задача:")
	}
	if strings.HasPrefix(s.kind, "material") {
		return b.materialsMenu(key, s.workshopID)
	}
	return b.productsMenu(key, s.workshopID)
}
func (b *Bot) formButton(a buttonAction) error {
	key := a.Key
	s := b.getSetup(key.ChatID, key.UserID)
	if s == nil {
		return errFormExpired
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return e
	}
	if w != s.workshopID {
		b.clearSetup(key.ChatID, key.UserID)
		return errFormExpired
	}
	if e = auth.Require(b.WS.DB(), key.UserID, w, auth.WorkshopRead); e != nil {
		b.clearSetup(key.ChatID, key.UserID)
		return e
	}
	switch a.Action {
	case "form_cancel":
		return b.formExit(key, s, "")
	case "form_home":
		return b.formExit(key, s, "home")
	case "form_task":
		return b.formExit(key, s, "task")
	case "form_continue":
		return b.screen(key, s.navigation.Current.Text, s.navigation.Current.Choices...)
	case "form_back":
		n := s.navigation
		if n == nil || len(n.History) == 0 {
			return errFormExpired
		}
		prev := n.History[len(n.History)-1]
		n.History = n.History[:len(n.History)-1]
		b.invalidateForm(key)
		*s = cloneForm(&prev.State)
		s.navigation = n
		n.Current = prev
		if (s.kind == "material_stock" || s.kind == "material_unit") && s.stage == 0 {
			if e = b.materialsMenu(key, w); e != nil {
				return e
			}
			items, e := b.Inv.ForUser(key.UserID).ListMaterials(w)
			if e != nil {
				return e
			}
			ids := []int64{}
			for _, item := range items {
				ids = append(ids, item["id"].(int64))
			}
			raw, _ := json.Marshal(ids)
			action := "materials_edit"
			if s.kind == "material_unit" {
				action = "materials_unit"
			}
			return b.executeButton(buttonAction{Key: key, Action: action, Workshop: w, Value: string(raw)})
		}
		return b.screen(key, prev.Text, prev.Choices...)
	}
	return errFormExpired
}
func (b *Bot) formMessage(key sessionKey, text string) (bool, error) {
	s := b.getSetup(key.ChatID, key.UserID)
	if s == nil {
		return b.dialogMessage(key, text)
	}
	low := strings.ToLower(strings.TrimSpace(text))
	switch low {
	case "/setup_stop", "/cancel", "отмена", "отмени", "остановись", "прекрати ввод", "выйти из ввода":
		return true, b.formExit(key, s, "")
	case "/start", "/menu", "главное меню", "/workshop", "⚙️ мастерская":
		return true, b.formExit(key, s, "home")
	case "/workshops":
		return true, b.formExit(key, s, "switch")
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil || w != s.workshopID {
		b.clearSetup(key.ChatID, key.UserID)
		return true, errFormExpired
	}
	if e = auth.Require(b.WS.DB(), key.UserID, w, auth.WorkshopRead); e != nil {
		b.clearSetup(key.ChatID, key.UserID)
		return true, e
	}
	command, _, _ := completionCommand(low)
	if !strings.HasPrefix(low, "/") && !command && (low == "задача" || low == "задачи") && s.stage == 0 && (s.kind == "material" || s.kind == "product") {
		return false, nil
	}
	if command || strings.HasPrefix(agent.TaskCommand(low), "/task") {
		// Keep the actual field prompt for “continue”, without creating a new step.
		if s.navigation == nil {
			return true, errFormExpired
		}
		saved := s.navigation.Current
		e = b.screen(key, "Сейчас открыта форма ввода. Чтобы перейти к задаче, сначала выйдите из формы. Несохранённые данные будут отброшены.", choice{Text: "Продолжить ввод", Action: "form_continue", Workshop: w}, choice{Text: "Отменить ввод и открыть задачу", Action: "form_task", Workshop: w})
		s.navigation.Current = saved
		return true, e
	}
	return false, nil
}
