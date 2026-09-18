package telegram

import (
	"errors"
	"fmt"
	"strings"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/products"
)

type compositionInput struct {
	Rows        []products.BOMRow
	MaterialIDs []int64
	Change      products.BOMChange
	Stage       string
}

func (b *Bot) checkComposition(key sessionKey, s *setupSession, write bool) error {
	if err := b.checkProductEdit(key, s); err != nil {
		return err
	}
	if err := auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.BOMRead); err != nil {
		return err
	}
	if write {
		return auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.BOMWrite)
	}
	return nil
}
func (b *Bot) compositionMenu(key sessionKey, s *setupSession) error {
	if err := b.checkComposition(key, s, false); err != nil {
		return err
	}
	snapshot, err := b.Prod.ForUser(key.UserID).Composition(s.workshopID, s.productID)
	if err != nil {
		return err
	}
	p, err := b.Prod.ForUser(key.UserID).Product(s.workshopID, s.productID)
	if err != nil {
		return err
	}
	b.invalidateProductButtons(key)
	s.composition = &compositionInput{Rows: snapshot.Rows, Stage: "menu", Change: products.BOMChange{Expected: snapshot.Version}}
	text := "Товар: " + p.Name + "\nСостав на 1 товар:\n"
	for i, r := range snapshot.Rows {
		kind := "материал"
		if r.Kind == "product" {
			kind = "вложенный продукт"
		}
		text += fmt.Sprintf("%d. %s (%s) — %s\n", i+1, r.Name, kind, r.DisplayQuantity())
	}
	if len(snapshot.Rows) == 0 {
		text += "Состав товара пока не задан. Добавьте материалы, компоненты и упаковку на одну единицу товара."
	}
	choices := []choice{}
	if auth.Require(b.WS.DB(), key.UserID, s.workshopID, auth.BOMWrite) == nil {
		choices = append(choices, b.compositionChoice(s, "Добавить компонент", "add"))
		if len(snapshot.Rows) > 0 {
			choices = append(choices, b.compositionChoice(s, "Изменить количество", "edit"), b.compositionChoice(s, "Убрать компонент", "remove"))
		}
	}
	choices = append(choices, b.compositionChoice(s, "Назад к товару", "back"))
	return b.productEditScreen(key, text, choices...)
}
func (b *Bot) compositionChoice(s *setupSession, label, action string) choice {
	return choice{Text: label, Action: "product_edit_bom_" + action, Workshop: s.workshopID, Target: s.productID}
}
func (b *Bot) compositionButton(a buttonAction, s *setupSession) error {
	key := a.Key
	op := strings.TrimPrefix(a.Action, "product_edit_bom_")
	write := op != "open" && op != "cancel" && op != "back"
	if err := b.checkComposition(key, s, write); err != nil {
		return err
	}
	if op == "open" || op == "cancel" {
		return b.compositionMenu(key, s)
	}
	if op == "back" {
		return b.productCard(key, s)
	}
	c := s.composition
	if c == nil {
		return errProductEditExpired
	}
	switch op {
	case "add":
		items, err := b.Inv.ForUser(key.UserID).ListMaterials(s.workshopID)
		if err != nil {
			return err
		}
		b.invalidateProductButtons(key)
		c.Stage = "material"
		c.MaterialIDs = nil
		c.Change = products.BOMChange{Action: "add", Expected: c.Change.Expected}
		text := "Выберите компонент из списка:\n"
		for i, m := range items {
			c.MaterialIDs = append(c.MaterialIDs, m["id"].(int64))
			text += fmt.Sprintf("%d. %s — %s\n", i+1, m["name"], inventory.UnitLabel(inventory.DisplayUnit(m)))
		}
		text += "Введите номер. Если нужной позиции нет, создайте её через /materials. Отмена: /setup_stop."
		return b.productEditScreen(key, text, b.compositionChoice(s, "Отмена", "cancel"))
	case "edit", "remove":
		b.invalidateProductButtons(key)
		c.Stage = "row"
		c.Change.Action = "update"
		if op == "remove" {
			c.Change.Action = "remove"
		}
		return b.sendMessage(key.ChatID, "Введите номер компонента из показанного состава. Отмена: /setup_stop.")
	case "save":
		if err := invariants.Check(b.WS.DB(), invariants.ProposedAction{ActionType: "change_bom", UserID: key.UserID, WorkshopID: s.workshopID}, invariants.Facts{Confirmed: c.Stage == "confirm"}); err != nil {
			return err
		}
		if c.Stage != "confirm" {
			return errProductEditExpired
		}
		b.invalidateProductButtons(key)
		changed, err := b.Prod.ForUser(key.UserID).ApplyBOMChange(s.workshopID, s.productID, c.Change)
		if errors.Is(err, inventory.ErrUnitChanged) {
			return b.compositionQuantityPrompt(key, s, "Рабочая единица изменилась. Введите количество заново.\n")
		}
		if errors.Is(err, products.ErrChanged) {
			return b.compositionPreview(key, s, "Состав изменился. Проверьте актуальные данные и подтвердите заново.\n")
		}
		if errors.Is(err, products.ErrBOMDuplicate) {
			return b.compositionNotice(key, s, err.Error())
		}
		if err != nil {
			return err
		}
		text := "Состав сохранён. Остатки не изменены."
		if !changed {
			text = "Значение уже установлено"
		}
		return b.compositionNotice(key, s, text)
	}
	return errProductEditExpired
}
func (b *Bot) compositionNotice(key sessionKey, s *setupSession, text string) error {
	if err := b.sendMessage(key.ChatID, text); err != nil {
		return err
	}
	return b.compositionMenu(key, s)
}

// Selection and previews always refresh metadata; IDs remain those selected
// from the displayed snapshots. Duplicate additions never turn into updates.
func (b *Bot) compositionSelection(key sessionKey, s *setupSession) (products.BOMRow, string, error) {
	c := s.composition
	snapshot, err := b.Prod.ForUser(key.UserID).Composition(s.workshopID, s.productID)
	if err != nil {
		return products.BOMRow{}, "", err
	}
	if c.Change.Action == "add" {
		for _, r := range snapshot.Rows {
			if r.Kind == "material" && r.MaterialID == c.Change.MaterialID {
				return r, snapshot.Version, products.ErrBOMDuplicate
			}
		}
		m, err := b.Inv.ForUser(key.UserID).Material(s.workshopID, c.Change.MaterialID)
		if err != nil {
			return products.BOMRow{}, "", err
		}
		return products.BOMRow{Kind: "material", MaterialID: c.Change.MaterialID, Name: m["name"].(string), Unit: m["base_unit"].(string), DisplayUnit: inventory.DisplayUnit(m)}, snapshot.Version, nil
	}
	for _, r := range snapshot.Rows {
		if r.ID == c.Change.RowID {
			return r, snapshot.Version, nil
		}
	}
	return products.BOMRow{}, snapshot.Version, products.ErrChanged
}
func (b *Bot) compositionQuantityPrompt(key sessionKey, s *setupSession, prefix string) error {
	r, version, err := b.compositionSelection(key, s)
	if errors.Is(err, products.ErrBOMDuplicate) {
		return b.compositionNotice(key, s, fmt.Sprintf("%s Уже в составе: %s — %s. Нажмите «Изменить количество» и выберите строку.", err.Error(), r.Name, r.DisplayQuantity()))
	}
	if errors.Is(err, products.ErrChanged) {
		return b.compositionNotice(key, s, "Строка состава больше недоступна. Выберите компонент заново.")
	}
	if err != nil {
		return err
	}
	b.invalidateProductButtons(key)
	s.composition.Stage = "quantity"
	s.composition.Change.DisplayUnit = r.DisplayUnit
	s.composition.Change.Expected = version
	s.composition.Change.Value = ""
	current := ""
	if r.ID != 0 {
		current = "\nСейчас на один товар: " + r.DisplayQuantity()
	}
	return b.productEditScreen(key, prefix+r.Name+current+"\nСколько требуется на один товар? Введите новое абсолютное количество. Единица: "+inventory.UnitLabel(r.DisplayUnit)+".\nОтмена: /setup_stop.", b.compositionChoice(s, "Отмена", "cancel"))
}
func (b *Bot) compositionPreview(key sessionKey, s *setupSession, prefix string) error {
	if err := b.checkComposition(key, s, true); err != nil {
		return err
	}
	c := s.composition
	r, version, err := b.compositionSelection(key, s)
	if errors.Is(err, products.ErrBOMDuplicate) {
		return b.compositionQuantityPrompt(key, s, "")
	}
	if errors.Is(err, products.ErrChanged) {
		return b.compositionNotice(key, s, "Строка состава больше недоступна. Выберите компонент заново.")
	}
	if err != nil {
		return err
	}
	if c.Change.Action != "remove" && c.Change.DisplayUnit != r.DisplayUnit {
		return b.compositionQuantityPrompt(key, s, "Рабочая единица изменилась. Введите количество заново.\n")
	}
	if c.Change.Expected != version && prefix == "" {
		prefix = "Состав изменился. Проверьте обновлённое предложение.\n"
	}
	p, err := b.Prod.ForUser(key.UserID).Product(s.workshopID, s.productID)
	if err != nil {
		return err
	}
	text := prefix + "Товар: " + p.Name + "\nКомпонент: " + r.Name
	if c.Change.Action == "remove" {
		text += "\nУбрать из состава: " + r.DisplayQuantity() + " на один товар?"
	} else {
		qty, err := products.BOMQuantity(c.Change.Value, c.Change.DisplayUnit, r.Unit)
		if err != nil {
			c.Stage = "quantity"
			b.invalidateProductButtons(key)
			return b.productEditScreen(key, publicError(err)+"\nПовторите ввод или /setup_stop.", b.compositionChoice(s, "Отмена", "cancel"))
		}
		if c.Change.Action == "update" {
			text += "\nБыло: " + r.DisplayQuantity()
		}
		r.Quantity = qty
		text += "\nБудет: " + r.DisplayQuantity() + " на один товар."
	}
	c.Change.Expected = version
	c.Stage = "confirm"
	b.invalidateProductButtons(key)
	return b.productEditScreen(key, text, b.compositionChoice(s, "Сохранить", "save"), b.compositionChoice(s, "Отмена", "cancel"))
}
func (b *Bot) compositionStep(key sessionKey, text string, s *setupSession) error {
	if err := b.checkComposition(key, s, s.composition.Stage != "menu"); err != nil {
		return err
	}
	c := s.composition
	switch c.Stage {
	case "material":
		n, err := parseChoice(strings.TrimSpace(text), len(c.MaterialIDs))
		if err != nil {
			return b.sendMessage(key.ChatID, "Введите номер из списка материалов. Если позиции нет, создайте её через /materials.")
		}
		c.Change.MaterialID = c.MaterialIDs[n-1]
		return b.compositionQuantityPrompt(key, s, "")
	case "row":
		n, err := parseChoice(strings.TrimSpace(text), len(c.Rows))
		if err != nil {
			return b.sendMessage(key.ChatID, "Введите номер из показанного состава или /setup_stop.")
		}
		c.Change.RowID = c.Rows[n-1].ID
		if c.Change.Action == "remove" {
			return b.compositionPreview(key, s, "")
		}
		return b.compositionQuantityPrompt(key, s, "")
	case "quantity":
		c.Change.Value = strings.TrimSpace(text)
		return b.compositionPreview(key, s, "")
	default:
		return b.sendMessage(key.ChatID, "Выберите действие кнопкой. Отмена: /setup_stop.")
	}
}
