package telegram

import (
	"fmt"
	"strings"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
)

// Adapters keep draft/session state in its existing owner. No task is cancelled
// by leaving a data-entry dialog.
func (b *Bot) dialogState(key sessionKey) (kind, version string, w int64) {
	b.uiMu.Lock()
	ui := b.ui[key]
	completion, ok := b.completions[key]
	b.uiMu.Unlock()
	if ui != nil && (ui.Kind == "create" || ui.Kind == "create_confirm") {
		return "create", fmt.Sprintf("%p", ui), 0
	}
	w, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return "", "", 0
	}
	if ok && completion.Input && completion.Workshop == w {
		return "completion", fmt.Sprintf("%s:%d:%d", completion.Task, completion.Session, completion.Expires.UnixNano()), w
	}
	if d, e := b.loadAssembly(key, w); e == nil && d != nil {
		return "assembly", fmt.Sprintf("%d:%d", d.ID, d.Revision), w
	}
	return "", "", 0
}
func (b *Bot) dialogChoices(key sessionKey, choices []choice) []choice {
	kind, version, w := b.dialogState(key)
	if kind == "" {
		return choices
	}
	hasCancel, hasBack := false, false
	for i, c := range choices {
		if c.Text == "Отмена" {
			hasCancel = true
			if kind == "create" {
				choices[i] = choice{Text: "Отмена", Action: "dialog_cancel", Workshop: w, Value: kind + ":" + version}
			}
		}
		if c.Text == "Назад" {
			hasBack = true
		}
	}
	value := kind + ":" + version
	if !hasBack && kind != "create" {
		choices = append(choices, choice{Text: "Назад", Action: "dialog_back", Workshop: w, Value: value})
	}
	if !hasCancel {
		choices = append(choices, choice{Text: "Отмена", Action: "dialog_cancel", Workshop: w, Value: value})
	}
	return append(choices, choice{Text: "Главное меню", Action: "dialog_home", Workshop: w, Value: value})
}
func (b *Bot) dialogButton(a buttonAction) error {
	kind, version, w := b.dialogState(a.Key)
	if kind == "" || a.Workshop != w || a.Value != kind+":"+version {
		return errFormExpired
	}
	if w != 0 {
		if e := auth.Require(b.WS.DB(), a.Key.UserID, w, auth.WorkshopRead); e != nil {
			return e
		}
	}
	if a.Action == "dialog_back" {
		if kind == "assembly" {
			d, e := b.loadAssembly(a.Key, w)
			if e != nil {
				return e
			}
			return b.assemblyButton(buttonAction{Key: a.Key, Workshop: w, Action: "assembly_edit_product", Target: d.ID, Value: fmt.Sprint(d.Revision)})
		}
		if kind == "completion" {
			b.uiMu.Lock()
			c := b.completions[a.Key]
			b.uiMu.Unlock()
			return b.completionStart(a.Key, w, c.Task, "")
		}
		return errFormExpired
	}
	switch kind {
	case "create":
		b.uiMu.Lock()
		delete(b.ui, a.Key)
		for id, button := range b.buttons {
			if button.Key == a.Key && button.Action == "create_confirm" {
				delete(b.buttons, id)
			}
		}
		b.uiMu.Unlock()
	case "assembly":
		if e := b.dropAssembly(a.Key); e != nil {
			return e
		}
	case "completion":
		if e := b.Agent.Memory.ForUser(a.Key.UserID).CancelCompletion(memory.Scope{UserID: a.Key.UserID, WorkshopID: w}, a.Key.ChatID); e != nil {
			return e
		}
		b.uiMu.Lock()
		delete(b.completions, a.Key)
		for id, button := range b.buttons {
			if button.Key == a.Key && strings.HasPrefix(button.Action, "completion_") {
				delete(b.buttons, id)
			}
		}
		b.uiMu.Unlock()
	}
	if e := b.sendMessage(a.Key.ChatID, "Ввод остановлен. Несохранённые данные отброшены; рабочая задача не изменена."); e != nil {
		return e
	}
	if a.Action == "dialog_switch" {
		return b.switcher(a.Key)
	}
	return b.home(a.Key)
}
func (b *Bot) dialogMessage(key sessionKey, text string) (bool, error) {
	low := strings.ToLower(strings.TrimSpace(text))
	action := ""
	switch low {
	case "/setup_stop", "/cancel", "отмена", "отмени", "остановись", "прекрати ввод", "выйти из ввода":
		action = "dialog_cancel"
	case "/start", "/menu", "главное меню", "/workshop", "⚙️ мастерская":
		action = "dialog_home"
	case "/workshops":
		action = "dialog_switch"
	}
	if action == "" {
		return false, nil
	}
	kind, version, w := b.dialogState(key)
	if kind == "" {
		return false, nil
	}
	return true, b.dialogButton(buttonAction{Key: key, Action: action, Workshop: w, Value: kind + ":" + version})
}
