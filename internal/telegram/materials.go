package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
)

func (b *Bot) materialsMenu(key sessionKey, workshop int64) error {
	items, err := b.Inv.ForUser(key.UserID).ListMaterials(workshop)
	if err != nil {
		return err
	}
	b.clearSetup(key.ChatID, key.UserID)
	lines := []string{"Материалы:"}
	ids := []int64{}
	for i, item := range items {
		lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, item["name"], inventory.Quantity(item, "current_stock")))
		ids = append(ids, item["id"].(int64))
	}
	if len(items) == 0 {
		lines = append(lines, "Пока нет материалов.")
	}
	// Count UTF-16 units, including when a single legacy name exceeds the limit.
	parts := materialMessageParts(strings.Join(lines, "\n"))
	for _, part := range parts[:len(parts)-1] {
		if err := b.sendMessage(key.ChatID, part); err != nil {
			return err
		}
	}
	choices := []choice{}
	if auth.Require(b.WS.DB(), key.UserID, workshop, auth.InventoryWrite) == nil {
		choices = append(choices, choice{Text: "Добавить", Action: "materials_add", Workshop: workshop})
		if len(ids) > 0 {
			payload, _ := json.Marshal(ids)
			choices = append(choices, choice{Text: "Редактировать", Action: "materials_edit", Workshop: workshop, Value: string(payload)})
			choices = append(choices, choice{Text: "Единица измерения", Action: "materials_unit", Workshop: workshop, Value: string(payload)})
		}
	}
	if err := b.screen(key, parts[len(parts)-1], choices...); err != nil {
		return err
	}
	b.rememberMaterialList(key, workshop, ids)
	return nil
}

func materialMessageParts(text string) []string {
	var parts []string
	var part strings.Builder
	size := 0
	for _, r := range text {
		n := 1
		if r > 0xFFFF {
			n = 2
		}
		if size+n > 3500 {
			parts = append(parts, part.String())
			part.Reset()
			size = 0
		}
		part.WriteRune(r)
		size += n
	}
	if part.Len() > 0 {
		parts = append(parts, part.String())
	}
	return parts
}

func materialUnit(unit string) string {
	return inventory.UnitLabel(unit)
}

func materialLabel(name string) string {
	r := []rune(name)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return name
}

func (b *Bot) materialStockStep(chatID int64, text string, s *setupSession) error {
	if err := auth.Require(b.WS.DB(), s.userID, s.workshopID, auth.InventoryWrite); err != nil {
		b.clearSetup(chatID, s.userID)
		return err
	}
	if s.stage == 0 {
		n, err := parseChoice(strings.TrimSpace(text), len(s.materialIDs))
		if err != nil {
			return b.sendMessage(chatID, "Введите номер материала из списка.")
		}
		s.materialID = s.materialIDs[n-1]
		item, err := b.Inv.ForUser(s.userID).Material(s.workshopID, s.materialID)
		if err != nil {
			return err
		}
		s.name = fmt.Sprint(item["name"])
		s.unit = inventory.DisplayUnit(item)
		if s.kind == "material_unit" {
			b.clearSetup(chatID, s.userID)
			choices := []choice{}
			for _, u := range inventory.CompatibleUnits(item["base_unit"].(string)) {
				choices = append(choices, choice{Text: inventory.UnitLabel(u), Action: "material_unit_confirm", Workshop: s.workshopID, Target: s.materialID, Value: u})
			}
			return b.screen(sessionKey{chatID, s.userID}, "Выберите рабочую единицу: "+materialLabel(s.name), choices...)
		}
		s.stage = 1
		s.name = materialLabel(s.name)
		return b.sendMessage(chatID, fmt.Sprintf("%s: %s.\nВведите новый остаток или изменение:\n10 — установить остаток 10;\n+2 — добавить 2;\n-0,5 — списать 0,5.\nЕдиница: %s.\nОтмена: /setup_stop", s.name, inventory.Quantity(item, "current_stock"), inventory.UnitLabel(s.unit)))
	}
	input := strings.TrimSpace(text)
	relative := strings.HasPrefix(input, "+") || strings.HasPrefix(input, "-")
	value, err := strconv.ParseFloat(strings.ReplaceAll(input, ",", "."), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || (relative && value == 0) {
		return b.sendMessage(chatID, "Введите остаток без знака (например, 10000 или 0) либо ненулевое изменение со знаком (+5 или -2,5).")
	}
	var stock, previous float64
	inv := b.Inv.ForUser(s.userID)
	stock, previous, err = inv.ChangeDisplayedStock(s.workshopID, s.materialID, value, !relative, s.unit)
	if errors.Is(err, inventory.ErrUnitChanged) {
		s.stage = 0
		return b.sendMessage(chatID, "Рабочая единица изменилась. Повторно введите номер материала, затем количество в новой единице. /setup_stop — отмена.")
	}
	if err != nil {
		return b.sendMessage(chatID, "Не удалось изменить остаток. Проверьте доступ и количество: остаток не может стать отрицательным. Повторите ввод или /setup_stop.")
	}
	b.clearSetup(chatID, s.userID)
	message := fmt.Sprintf("%s: изменение %+g %s. Новый остаток: %s.", s.name, value, inventory.UnitLabel(s.unit), inventory.FormatQuantity(stock, s.unit))
	if !relative {
		message = fmt.Sprintf("%s: остаток установлен — %s. Было: %s.", s.name, inventory.FormatQuantity(stock, s.unit), inventory.FormatQuantity(previous, s.unit))
		if stock == previous {
			message = "Остаток уже равен указанному значению"
		}
	}
	if err := b.sendMessage(chatID, message); err != nil {
		return err
	}
	return b.materialsMenu(sessionKey{chatID, s.userID}, s.workshopID)
}
