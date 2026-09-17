package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"workshop-agent/internal/agent"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
)

func (b *Bot) currentList(key sessionKey, workshop int64) materialListContext {
	var sessionID int64
	if b.Agent != nil {
		_ = b.WS.DB().QueryRow(`SELECT id FROM conversation_sessions WHERE user_id=? AND workshop_id=? AND telegram_chat_id=? AND status='active'`, key.UserID, workshop, key.ChatID).Scan(&sessionID)
	}
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	s := b.materialLists[key]
	if s.Workshop != workshop || time.Now().After(s.Expires) || (b.Agent != nil && (sessionID == 0 || s.SessionID != sessionID)) {
		delete(b.materialLists, key)
		return materialListContext{}
	}
	return s
}
func (b *Bot) selectMaterial(key sessionKey, workshop, id int64) {
	s := b.currentList(key, workshop)
	if b.Agent != nil {
		sc, err := b.Agent.Memory.ForUser(key.UserID).EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: workshop}, key.ChatID)
		if err == nil {
			s.SessionID = sc.SessionID
		}
	}
	s.Workshop = workshop
	s.LastID = id
	s.Expires = time.Now().Add(15 * time.Minute)
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	if b.materialLists == nil {
		b.materialLists = map[sessionKey]materialListContext{}
	}
	b.materialLists[key] = s
}

func (b *Bot) semanticMessage(key sessionKey, workshop int64, text string) (bool, error) {
	client, ok := b.Agent.LLM.(llm.SemanticClient)
	if !ok || strings.HasPrefix(text, "/") {
		return false, nil
	}
	if err := auth.Require(b.WS.DB(), key.UserID, workshop, auth.WorkshopRead); err != nil {
		return true, err
	}
	m := b.Agent.Memory.ForUser(key.UserID)
	sc, err := m.EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: workshop}, key.ChatID)
	if err != nil {
		return true, b.sendMessage(key.ChatID, "Контекст диалога недоступен. Используйте /materials или /stock <название>.")
	}
	task, err := m.ActiveWorking(sc)
	if err != nil {
		return true, err
	}
	candidate := (memory.MemoryRouter{}).Route(text, sc, task)
	// Retain explicit memory/task workflows; domain stock requests use the new path.
	if candidate.Target == memory.Personal || candidate.Target == memory.Workshop || candidate.Target == memory.Working || candidate.Target == memory.Discard || candidate.Category == "bom_update" {
		return false, nil
	}
	if cmd := agent.LocalReadCommand(text); cmd != nil {
		return true, b.executeSemantic(key, workshop, cmd, nil, "local", text)
	}
	items, err := b.Inv.ForUser(key.UserID).ListMaterials(workshop)
	if err != nil {
		return true, err
	}
	snapshot := b.currentList(key, workshop)
	catalog := []map[string]any{}
	for _, item := range items {
		catalog = append(catalog, map[string]any{"id": item["id"], "name": materialLabel(fmt.Sprint(item["name"])), "unit": inventory.DisplayUnit(item)})
	}
	if len(catalog) > 100 {
		catalog = catalog[:100]
	}
	history, err := m.ShortTerm(sc)
	if err != nil {
		return true, err
	}
	// Prompt uses a bounded recent dialogue; quantities here are not domain truth.
	recent := []string{}
	for i := len(history) - 1; i >= 0 && len(recent) < 4; i-- {
		r := []rune(history[i].Content)
		if len(r) > 400 {
			r = r[:400]
		}
		recent = append([]string{history[i].Role + ": " + string(r)}, recent...)
	}
	shownIDs := snapshot.IDs
	if len(shownIDs) > 100 {
		shownIDs = shownIDs[:100]
	}
	contextData := map[string]any{"user_id": key.UserID, "workshop_id": workshop, "session_id": sc.SessionID, "form": nil, "last_list_type": snapshot.Kind, "list_ids": shownIDs, "list_total": len(snapshot.IDs), "last_material_id": snapshot.LastID, "materials": catalog, "recent_dialogue": recent}
	if task != nil {
		contextData["task"] = map[string]any{"type": task.Type, "quantity": task.State.Quantity, "product": task.State.ProductName}
	}
	raw, _ := json.Marshal(contextData)
	cmd, usage, err := client.Interpret(context.Background(), string(raw), text)
	if err != nil {
		log.Printf("semantic route=llm validation=failed")
		return true, b.sendMessage(key.ChatID, "Не удалось надёжно разобрать запрос. Уточните материал или используйте /materials.\n"+formatTokenUsage(usage))
	}
	if err = llm.ValidateSemantic(cmd); err != nil {
		return true, b.sendMessage(key.ChatID, "Некорректная команда интерпретатора. Данные не изменены. Используйте /materials.")
	}
	if cmd.Action == "legacy" {
		b.forgetMaterialList(key)
		log.Printf("semantic route=llm action=legacy outcome=unsupported tokens=%v", usage)
		return true, b.sendMessage(key.ChatID, "Этот запрос пока не поддерживается семантическим обработчиком. Используйте /materials, /products, /task или /memory. Данные не изменены.\n"+formatTokenUsage(usage))
	}
	return true, b.executeSemantic(key, workshop, cmd, usage, "llm", text)
}

func (b *Bot) resolveSemantic(key sessionKey, workshop int64, r *llm.EntityReference) (map[string]any, string, error) {
	snapshot := b.currentList(key, workshop)
	id := int64(0)
	switch r.Kind {
	case "list_position":
		if snapshot.Kind != "material" || len(snapshot.IDs) == 0 {
			return nil, "Уточните название материала или откройте /materials: актуального списка материалов нет.", nil
		}
		if r.Position > len(snapshot.IDs) {
			return nil, "Такого номера нет в показанном списке. Откройте /materials.", nil
		}
		id = snapshot.IDs[r.Position-1]
	case "last":
		id = snapshot.LastID
		if id == 0 {
			return nil, "Уточните, о каком материале идёт речь, или откройте /materials.", nil
		}
	case "name":
		items, err := b.Inv.ForUser(key.UserID).ListMaterials(workshop)
		if err != nil {
			return nil, "", err
		}
		matches := []map[string]any{}
		for _, item := range items {
			if materialNameMatch(r.Name, fmt.Sprint(item["name"])) {
				matches = append(matches, item)
			}
		}
		if len(matches) == 0 {
			return nil, "Не удалось однозначно найти материал. Укажите полное название или номер из /materials.", nil
		}
		if len(matches) > 1 {
			names := []string{}
			for i, item := range matches {
				if i >= 8 {
					break
				}
				names = append(names, materialLabel(fmt.Sprint(item["name"])))
			}
			return nil, "Найдено несколько материалов: " + strings.Join(names, "; ") + ". Выберите номер через /materials.", nil
		}
		return matches[0], "", nil
	}
	item, err := b.Inv.ForUser(key.UserID).Material(workshop, id)
	return item, "", err
}

// Conservative token matching: inflection endings and small edit distance only.
// Every matching catalog entry is retained, never silently choose the first.
func materialNameMatch(query, name string) bool {
	tokenize := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(s, "ё", "е")), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	q, n := tokenize(query), tokenize(name)
	if len(q) == 0 {
		return false
	}
	for _, a := range q {
		found := false
		for _, b := range n {
			if a == b {
				found = true
				break
			}
			ar, br := []rune(a), []rune(b)
			if len(ar) >= 4 && len(br) >= 4 && editDistance(ar, br) <= 2 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func editDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, x := range a {
		cur := make([]int, len(b)+1)
		cur[0] = i + 1
		for j, y := range b {
			cost := 0
			if x != y {
				cost = 1
			}
			v := prev[j] + cost
			if prev[j+1]+1 < v {
				v = prev[j+1] + 1
			}
			if cur[j]+1 < v {
				v = cur[j] + 1
			}
			cur[j+1] = v
		}
		prev = cur
	}
	return prev[len(b)]
}

type semanticChange struct {
	Amount                  float64
	Mode, Unit, DisplayUnit string
	SessionID               int64
}

func (b *Bot) invalidateSemantic(key sessionKey) {
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	for id, a := range b.buttons {
		if a.Key == key && (a.Action == "semantic_stock_confirm" || a.Action == "semantic_cancel") {
			delete(b.buttons, id)
		}
	}
}

func (b *Bot) executeSemantic(key sessionKey, workshop int64, cmd *llm.StructuredCommand, usage *llm.Usage, route, text string) (err error) {
	outcome := "validated"
	reference := "none"
	if cmd != nil && cmd.Reference != nil {
		reference = cmd.Reference.Kind
	}
	defer func() {
		if err != nil {
			outcome = "denied_or_failed"
		}
		tokens := 0
		if usage != nil {
			tokens = usage.TotalTokens
		}
		action := "invalid"
		if cmd != nil {
			action = cmd.Action
		}
		log.Printf("semantic route=%s action=%s reference=%s source=ui_or_catalog outcome=%s tokens=%d", route, action, reference, outcome, tokens)
	}()
	if err = llm.ValidateSemantic(cmd); err != nil {
		return err
	}
	active, e := b.WS.ActiveWorkshop(key.UserID)
	if e != nil {
		return e
	}
	if active != workshop {
		return auth.ErrDenied
	}
	if err = auth.Require(b.WS.DB(), key.UserID, workshop, auth.WorkshopRead); err != nil {
		return err
	}
	answer := ""
	var item map[string]any
	if cmd.Reference != nil {
		if cmd.Reference.Kind == "last" && !regexp.MustCompile(`(?i)(?:^|\s)(?:его|е[её]|этого|этой|него|не[её])(?:\s|[?!.]|$)`).MatchString(text) {
			return b.sendMessage(key.ChatID, "Уточните название или номер материала: ссылка на предыдущий объект неоднозначна.")
		}
		if cmd.Reference.Kind == "name" && !strings.Contains(strings.ToLower(text), strings.ToLower(cmd.Reference.Name)) {
			return b.sendMessage(key.ChatID, "Уточните название материала: интерпретатор не смог надёжно связать его с вашим сообщением.")
		}
		item, answer, err = b.resolveSemantic(key, workshop, cmd.Reference)
		if err != nil {
			return err
		}
		if answer != "" {
			outcome = "clarification"
			return b.sendMessage(key.ChatID, answer+"\n"+formatTokenUsage(usage))
		}
	}
	switch cmd.Action {
	case "get_material_stock", "get_material_minimum":
		field := "current_stock"
		label := ""
		if cmd.Action == "get_material_minimum" {
			field = "minimum_stock"
			label = "минимальный остаток: "
		}
		answer = fmt.Sprintf("%s — %s%s.", item["name"], label, inventory.Quantity(item, field))
		b.selectMaterial(key, workshop, item["id"].(int64))
	case "get_all_material_stock":
		return b.materialsMenu(key, workshop)
	case "get_purchase_needs":
		needs, e := b.Inv.ForUser(key.UserID).ListPurchaseNeeds(workshop)
		if e != nil {
			return e
		}
		answer = formatPurchaseNeeds(needs)
	case "get_task":
		answer, _, err = b.Agent.HandleMessageForWorkshop(context.Background(), workshop, key.UserID, key.ChatID, "/task")
		if err != nil {
			return err
		}
	case "change_material_stock":
		b.invalidateSemantic(key)
		if err = auth.Require(b.WS.DB(), key.UserID, workshop, auth.InventoryWrite); err != nil {
			return err
		}
		unit := cmd.Unit
		foundQuantity := false
		for _, n := range regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)?`).FindAllString(text, -1) {
			v, e := strconv.ParseFloat(strings.ReplaceAll(n, ",", "."), 64)
			if e == nil && v == *cmd.Amount {
				foundQuantity = true
			}
		}
		if !foundQuantity {
			return b.sendMessage(key.ChatID, "Укажите количество цифрами; изменение не выполнено.")
		}
		if unit != "" {
			explicit, e := inventory.InputUnit(text, item["base_unit"].(string), "")
			if e != nil || explicit != unit {
				return b.sendMessage(key.ChatID, "Уточните единицу количества; изменение не выполнено.")
			}
		}
		if unit == "" {
			unit = inventory.DisplayUnit(item)
		}
		compatible := false
		for _, u := range inventory.CompatibleUnits(item["base_unit"].(string)) {
			if u == unit {
				compatible = true
			}
		}
		if !compatible {
			return b.sendMessage(key.ChatID, "Единица несовместима с материалом. Уточните количество и единицу.")
		}
		if base := inventory.ConvertToBase(unit, *cmd.Amount); math.IsInf(base, 0) || math.IsNaN(base) {
			return b.sendMessage(key.ChatID, "Количество слишком велико. Изменение не выполнено.")
		}
		b.selectMaterial(key, workshop, item["id"].(int64))
		proposal := semanticChange{Amount: *cmd.Amount, Mode: cmd.QuantityMode, Unit: unit, DisplayUnit: inventory.DisplayUnit(item), SessionID: b.currentList(key, workshop).SessionID}
		raw, _ := json.Marshal(proposal)
		verb := map[string]string{"absolute": "установить остаток", "increase": "добавить", "decrease": "списать"}[cmd.QuantityMode]
		answer = fmt.Sprintf("Материал: %s.\nДействие: %s %s.\nПодтвердить?", item["name"], verb, inventory.FormatQuantity(inventory.ConvertToBase(unit, *cmd.Amount), unit))
		b.selectMaterial(key, workshop, item["id"].(int64))
		return b.screen(key, answer+"\n"+formatTokenUsage(usage), choice{Text: "Подтвердить", Action: "semantic_stock_confirm", Workshop: workshop, Target: item["id"].(int64), Value: string(raw)}, choice{Text: "Отмена", Action: "semantic_cancel", Workshop: workshop})
	default:
		outcome = "clarification"
		answer = "Уточните материал и действие: узнать остаток, установить новое значение, добавить или списать. Можно открыть /materials."
	}
	if b.Agent != nil {
		m := b.Agent.Memory.ForUser(key.UserID)
		sc, e := m.EnsureSession(memory.Scope{UserID: key.UserID, WorkshopID: workshop}, key.ChatID)
		if e == nil {
			_ = m.AppendShortTerm(sc, "user", text)
			_ = m.AppendShortTerm(sc, "assistant", answer)
		}
	}
	if route == "llm" {
		answer += "\n" + formatTokenUsage(usage)
	}
	return b.sendMessage(key.ChatID, answer)
}
