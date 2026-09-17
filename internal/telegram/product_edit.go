package telegram

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
)

var errProductEditExpired = errors.New("Форма редактирования устарела. Откройте /products заново.")

func (b *Bot) invalidateProductButtons(key sessionKey) {
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	for id, a := range b.buttons {
		if a.Key == key && (strings.HasPrefix(a.Action, "product_edit_") || a.Action == "product_add") {
			delete(b.buttons, id)
		}
	}
}
func (b *Bot) checkProductEdit(key sessionKey, s *setupSession) error {
	if s == nil || s.kind != "product_edit" || time.Now().After(s.editExpires) {
		b.clearSetup(key.ChatID, key.UserID)
		return errProductEditExpired
	}
	w, err := b.WS.ActiveWorkshop(key.UserID)
	if err != nil {
		return err
	}
	if w != s.workshopID {
		return auth.ErrDenied
	}
	for _, p := range []auth.Permission{auth.ProductsRead} {
		if err = auth.Require(b.WS.DB(), key.UserID, w, p); err != nil {
			return err
		}
	}
	if s.editSession != 0 {
		var n int
		err = b.WS.DB().QueryRow("SELECT COUNT(*) FROM conversation_sessions WHERE id=? AND user_id=? AND workshop_id=? AND telegram_chat_id=? AND status='active'", s.editSession, key.UserID, w, key.ChatID).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return memory.ErrScope
		}
	}
	return nil
}
func (b *Bot) productEditButton(a buttonAction) error {
	key := a.Key
	if a.Action == "product_edit_open" {
		if err := auth.Require(b.WS.DB(), key.UserID, a.Workshop, auth.ProductsRead); err != nil {
			return err
		}
		b.clearSetup(key.ChatID, key.UserID)
		s := &setupSession{kind: "product_edit", userID: key.UserID, workshopID: a.Workshop, editExpires: time.Now().Add(15 * time.Minute)}
		if b.Agent != nil && b.Agent.Memory != nil {
			sc, err := b.Agent.Memory.ForUser(key.UserID).EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: a.Workshop}, key.ChatID)
			if err != nil {
				return err
			}
			s.editSession = sc.SessionID
		}
		b.startSetup(key.ChatID, s)
		if err := b.productsMenu(key, a.Workshop); err != nil {
			b.clearSetup(key.ChatID, key.UserID)
			return err
		}
		snap := b.currentList(key, a.Workshop)
		s.productIDs = append([]int64(nil), snap.IDs...)
		b.invalidateProductButtons(key)
		return b.sendMessage(key.ChatID, "Введите номер продукта для редактирования. Отмена: /setup_stop.")
	}
	s := b.getSetup(key.ChatID, key.UserID)
	if err := b.checkProductEdit(key, s); err != nil {
		return err
	}
	if s.productID != a.Target {
		return auth.ErrDenied
	}
	if strings.HasPrefix(a.Action, "product_edit_bom_") {
		return b.compositionButton(a, s)
	}
	if a.Action == "product_edit_field" || a.Action == "product_edit_type" || a.Action == "product_edit_save" {
		if err := auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.ProductsWrite); err != nil {
			return err
		}
	}
	switch a.Action {
	case "product_edit_back":
		b.clearSetup(key.ChatID, key.UserID)
		return b.productsMenu(key, a.Workshop)
	case "product_edit_cancel":
		return b.productCard(key, s)
	case "product_edit_field":
		if s.stage != 1 {
			return auth.ErrDenied
		}
		if products.FieldLabel(a.Value) == "" {
			return products.ErrInvalidEdit
		}
		b.invalidateProductButtons(key)
		s.edit = products.Edit{Field: a.Value}
		s.stage = 2
		prompt := "Введите новое значение: " + products.FieldLabel(a.Value) + ". Отмена: /setup_stop."
		switch a.Value {
		case "sku":
			prompt = "Введите артикул (SKU) или - для очистки. Отмена: /setup_stop."
		case "current_stock":
			prompt = "Введите новый остаток или изменение:\n10 — установить остаток 10;\n+3 — добавить 3;\n-2 — списать 2.\nЕдиница: шт.\nОтмена: /setup_stop"
		case "minimum_stock":
			prompt = "Введите новый минимальный остаток: неотрицательное число в шт. Отмена: /setup_stop."
		case "product_type":
			s.stage = 4
			choices := []choice{}
			for _, v := range []string{"product", "kit", "semi_finished"} {
				choices = append(choices, choice{Text: products.TypeLabel(v), Action: "product_edit_type", Workshop: s.workshopID, Target: s.productID, Value: v})
			}
			return b.screen(key, "Выберите тип продукта. Отмена: /setup_stop.", choices...)
		}
		return b.sendMessage(key.ChatID, prompt)
	case "product_edit_type":
		if s.stage != 4 {
			return auth.ErrDenied
		}
		s.edit.Value = a.Value
		return b.productEditPreview(key, s, "")
	case "product_edit_save":
		if s.stage != 3 {
			return auth.ErrDenied
		}
		b.invalidateProductButtons(key)
		r, err := b.Prod.ForUser(key.UserID).ApplyEdit(s.workshopID, s.productID, s.edit)
		if errors.Is(err, products.ErrChanged) {
			return b.productEditPreview(key, s, "Данные изменились после показа подтверждения. Проверьте новое предложение.\n")
		}
		if err != nil {
			return err
		}
		answer := "Значение уже установлено"
		if r.Changed {
			answer = fmt.Sprintf("%s: %s → %s. Сохранено.", products.FieldLabel(s.edit.Field), products.DisplayField(s.edit.Field, r.Before), products.DisplayField(s.edit.Field, r.After))
		}
		if err = b.sendMessage(key.ChatID, answer); err != nil {
			return err
		}
		return b.productCard(key, s)
	}
	return auth.ErrDenied
}

func (b *Bot) productCard(key sessionKey, s *setupSession) error {
	if err := b.checkProductEdit(key, s); err != nil {
		return err
	}
	p, err := b.Prod.ForUser(key.UserID).Product(s.workshopID, s.productID)
	if err != nil {
		return err
	}
	b.invalidateProductButtons(key)
	s.stage = 1
	s.composition = nil
	s.edit = products.Edit{}
	choices := []choice{}
	for _, field := range []string{"name", "sku", "product_type", "current_stock", "minimum_stock"} {
		if auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.ProductsWrite) != nil {
			continue
		}
		choices = append(choices, choice{Text: products.FieldLabel(field), Action: "product_edit_field", Workshop: s.workshopID, Target: p.ID, Value: field})
	}
	if auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.BOMRead) == nil {
		choices = append(choices, choice{Text: "Состав товара", Action: "product_edit_bom_open", Workshop: s.workshopID, Target: p.ID})
	}
	choices = append(choices, choice{Text: "Назад к товарам", Action: "product_edit_back", Workshop: s.workshopID, Target: p.ID})
	text := fmt.Sprintf("Название: %s\nSKU: %s\nТип: %s\nОстаток: %g шт\nМинимальный остаток: %g шт", p.Name, products.DisplayField("sku", p.SKU), products.TypeLabel(p.Type), p.Stock, p.Minimum)
	return b.productEditScreen(key, text, choices...)
}
func (b *Bot) productEditScreen(key sessionKey, text string, choices ...choice) error {
	r := []rune(text)
	for len(r) > 1500 {
		if err := b.sendMessage(key.ChatID, string(r[:1500])); err != nil {
			return err
		}
		r = r[1500:]
	}
	return b.screen(key, string(r), choices...)
}
func (b *Bot) productEditPreview(key sessionKey, s *setupSession, prefix string) error {
	if err := b.checkProductEdit(key, s); err != nil {
		return err
	}
	p, err := b.Prod.ForUser(key.UserID).Product(s.workshopID, s.productID)
	if err != nil {
		return err
	}
	after, err := products.EditValue(p, s.edit)
	if err != nil {
		s.stage = 2
		b.invalidateProductButtons(key)
		return b.sendMessage(key.ChatID, prefix+publicError(err)+"\nВведите другое значение или /setup_stop.")
	}
	before := p.Field(s.edit.Field)
	s.edit.Expected = &before
	s.stage = 3
	b.invalidateProductButtons(key)
	text := fmt.Sprintf("%sПродукт: %s\nПоле: %s\nБыло: %s\nБудет: %s", prefix, p.Name, products.FieldLabel(s.edit.Field), products.DisplayField(s.edit.Field, before), products.DisplayField(s.edit.Field, after))
	if s.edit.Relative {
		text += "\nИзменение: " + s.edit.Value + " шт"
	}
	return b.productEditScreen(key, text, choice{Text: "Сохранить", Action: "product_edit_save", Workshop: s.workshopID, Target: p.ID}, choice{Text: "Отмена", Action: "product_edit_cancel", Workshop: s.workshopID, Target: p.ID})
}
func (b *Bot) productEditStep(key sessionKey, text string, s *setupSession) error {
	if err := b.checkProductEdit(key, s); err != nil {
		return err
	}
	if s.composition != nil {
		return b.compositionStep(key, text, s)
	}
	switch s.stage {
	case 0:
		n, err := parseChoice(strings.TrimSpace(text), len(s.productIDs))
		if err != nil {
			return b.sendMessage(key.ChatID, "Введите номер продукта из показанного списка или /setup_stop.")
		}
		s.productID = s.productIDs[n-1]
		return b.productCard(key, s)
	case 2:
		if err := auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.ProductsWrite); err != nil {
			return err
		}
		s.edit.Value = strings.TrimSpace(text)
		s.edit.Relative = s.edit.Field == "current_stock" && (strings.HasPrefix(s.edit.Value, "+") || strings.HasPrefix(s.edit.Value, "-"))
		return b.productEditPreview(key, s, "")
	default:
		return b.sendMessage(key.ChatID, "Выберите действие кнопкой. Отмена: /setup_stop.")
	}
}
