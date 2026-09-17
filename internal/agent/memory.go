package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
)

func (a *WorkshopAgent) HandleMessageForWorkshop(ctx context.Context, workshop, user, chat int64, text string) (answer string, usage *llm.Usage, err error) {
	if err = auth.Require(a.WS.DB(), user, workshop, auth.WorkshopRead); err != nil {
		return
	}
	if IsDailySummary(text) {
		answer, err = a.DailySummary(user, workshop, time.Now())
		return answer, &llm.Usage{}, err
	}
	if key, value, ok := personalization.PersistentIntent(text); ok {
		err = personalization.New(a.WS.DB()).ForUser(user).UpdatePreference(key, value, "explicit_user_message")
		return "Профиль обновлён. Настройка будет применяться автоматически.", &llm.Usage{}, err
	}
	if IsPersonalizedReport(text) {
		return a.PersonalizedReport(ctx, user, workshop, chat, text)
	}
	m := a.Memory.ForUser(user)
	sc := memory.Scope{UserID: user, WorkshopID: workshop}
	sc, err = m.EnsureSession(sc, chat)
	if err != nil {
		if errors.Is(err, memory.ErrScope) || errors.Is(err, auth.ErrDenied) || errors.Is(err, auth.ErrDisabled) {
			return
		}
		// A memory outage never authorizes or fabricates a domain write/reference.
		if isColorReference(strings.ToLower(text)) {
			return "Память диалога недоступна. Укажите материал полностью.", &llm.Usage{}, nil
		}
		return a.handleLegacyMessage(ctx, workshop, user, chat, text)
	}
	if strings.HasPrefix(text, "/memory") || strings.HasPrefix(text, "/session") || strings.HasPrefix(text, "/task") {
		return a.memoryCommand(ctx, m, sc, chat, text)
	}
	task, err := m.ActiveWorking(sc)
	if err != nil {
		return "", nil, err
	}
	candidate := (memory.MemoryRouter{}).Route(text, sc, task)
	if candidate.Target == memory.Discard {
		return "Это сообщение не сохраняется в память. Не передавайте секреты в диалог.", &llm.Usage{}, nil
	}
	history, err := m.ShortTerm(sc)
	if err != nil {
		return "", nil, err
	}
	domain, material, product, err := a.resolveDomain(user, workshop, text, history, task)
	if err != nil {
		return "", nil, err
	}
	built, err := (memory.AgentContextBuilder{Memory: m}).Build(sc, text, domain, memory.All)
	if err != nil {
		return "", nil, err
	}
	built.Trace.Router = &candidate
	defer func() {
		if err == nil {
			if e := m.AppendShortTerm(sc, "user", text); e != nil {
				built.Trace.Warnings = append(built.Trace.Warnings, "short-term write failed")
			}
			if answer != "" {
				if e := m.AppendShortTerm(sc, "assistant", answer); e != nil {
					built.Trace.Warnings = append(built.Trace.Warnings, "assistant short-term write failed")
				}
			}
			if e := m.SaveTrace(sc, built.Trace); e != nil {
				answer += "\nДиагностика памяти временно недоступна."
			}
		}
	}()
	usage = &llm.Usage{}
	switch candidate.Target {
	case memory.Personal, memory.Workshop:
		if candidate.Target == memory.Workshop {
			if err = auth.Require(a.WS.DB(), user, workshop, auth.WorkshopManage); err != nil {
				return
			}
		}
		err = m.Propose(sc, chat, candidate)
		if err != nil {
			return
		}
		area := "ваши личные предпочтения"
		if candidate.Target == memory.Workshop {
			area = "общие правила текущей мастерской"
		}
		return "Сохранить постоянно: " + candidate.Value + "\nОбласть: " + area + ".\nПодтвердите: /memory confirm\nОтмена: /memory cancel", usage, nil
	case memory.Working:
		if candidate.Key == "create" {
			if task != nil {
				return "Уже есть активная задача. Завершите её /task complete или отмените /task cancel.", usage, nil
			}
			state := memory.TaskState{Quantity: candidate.Quantity, Parameters: map[string]string{}}
			if product != nil {
				state.ProductID = product["id"].(int64)
				state.ProductName = fmt.Sprint(product["name"])
			} else {
				return fmt.Sprintf("Что будем собирать и сколько единиц каждого продукта входит в заказ? Укажите название из /products. Например: «Соберём %g наборов <название продукта>». Задача пока не создана.", candidate.Quantity), usage, nil
			}
			task, err = m.CreateWorkingMemory(sc, candidate.Category, state)
			if err != nil {
				return "", usage, err
			}
		} else {
			if task == nil {
				return memory.ErrNoTask.Error(), usage, nil
			}
			if candidate.Key == "quantity" {
				task.State.Quantity = candidate.Quantity
			} else {
				if task.State.Parameters == nil {
					task.State.Parameters = map[string]string{}
				}
				task.State.Parameters[candidate.Key] = candidate.Value
			}
			updateScope := sc
			updateScope.TaskID = task.ID
			err = m.UpdateWorkingMemory(updateScope, task.State, "active")
			if err != nil {
				return "", usage, err
			}
		}
		refreshed, e := (memory.AgentContextBuilder{Memory: m}).Build(sc, text, domain, memory.All)
		if e != nil {
			return "", usage, e
		}
		built = refreshed
		built.Trace.Router = &candidate
		return a.taskAnswer(user, workshop, task, built.Long), usage, nil
	case memory.Domain:
		if candidate.Category == "bom_update" {
			if err = auth.Require(a.WS.DB(), user, workshop, auth.BOMWrite); err != nil {
				return
			}
			if product == nil || material == nil || candidate.Quantity <= 0 {
				return "Для изменения BOM укажите продукт, компонент и количество. Например: «Теперь всегда клади в Набор 3 кисти».", usage, nil
			}
			candidate.ProductID = product["id"].(int64)
			candidate.MaterialID = material["id"].(int64)
			// Proposal freezes the conversion; confirmation writes base units only.
			inputUnit, unitErr := inventory.InputUnit(text, material["base_unit"].(string), inventory.DisplayUnit(material))
			if unitErr != nil {
				return "Единица количества несовместима с материалом. Уточните количество и единицу.", usage, nil
			}
			candidate.Quantity = inventory.ConvertToBase(inputUnit, candidate.Quantity)
			err = m.Propose(sc, chat, candidate)
			if err != nil {
				return
			}
			return fmt.Sprintf("Изменить BOM: %s → %s, количество %s?\nПодтвердите: /memory confirm\nОтмена: /memory cancel", product["name"], material["name"], inventory.FormatQuantity(candidate.Quantity, inventory.DisplayUnit(material))), usage, nil
		}
	}
	lower := strings.ToLower(text)
	if material != nil && (strings.Contains(lower, "остат") || strings.Contains(lower, "есть") || strings.Contains(lower, "сколько") || isColorReference(lower)) {
		return formatMaterial(material), usage, nil
	}
	if isColorReference(lower) && material == nil {
		return "Уточните материал: к чему относится цвет? Например, PETG.", usage, nil
	}
	if task != nil && hasAny(lower, "план", "парти", "расчет", "расчёт", "задач", "сколько нужно") {
		return a.taskAnswer(user, workshop, task, built.Long), usage, nil
	}
	if hasAny(lower, "отчет", "отчёт", "сводк") {
		items, e := a.Inv.ForUser(user).ListMaterials(workshop)
		if e != nil {
			return "", usage, e
		}
		total := fmt.Sprintf("Итог: %d материалов на складе.", len(items))
		details := []string{}
		for _, item := range items {
			details = append(details, formatMaterial(item))
		}
		result := strings.Join(details, "\n")
		for _, p := range built.Long {
			if p.Key == "summary_first" && p.Value == "true" {
				return total + "\n" + result, usage, nil
			}
			if p.Key == "response_style" && p.Value == "concise" {
				return total, usage, nil
			}
		}
		return result + "\n" + total, usage, nil
	}
	// Keep the LLM as interpreter; authorized services still produce all factual stock/BOM answers.
	bound := *a
	bound.LLM = contextualClient{Client: a.LLM, Prompt: built.Prompt}
	return bound.handleLegacyMessage(ctx, workshop, user, chat, text)
}

type contextualClient struct {
	llm.Client
	Prompt string
}

func (c contextualClient) ParseCommand(ctx context.Context, text string) (*llm.StructuredCommand, *llm.Usage, error) {
	return c.Client.ParseCommand(ctx, c.Prompt)
}
func asJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
func hasAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
func color(text string) string {
	s := strings.ToLower(text)
	if hasAny(s, "черн", "чёрн", "black") {
		return "black"
	}
	if hasAny(s, "бел", "white") {
		return "white"
	}
	return ""
}
func isColorReference(s string) bool {
	return color(s) != "" && (strings.HasPrefix(strings.TrimSpace(s), "а ") || strings.HasPrefix(strings.TrimSpace(s), "а?"))
}

func (a *WorkshopAgent) resolveDomain(user, workshop int64, text string, history []memory.Message, task *memory.Task) ([]memory.Item, map[string]any, map[string]any, error) {
	materials, err := a.Inv.ForUser(user).ListMaterials(workshop)
	if err != nil {
		return nil, nil, nil, err
	}
	products, err := a.Prod.ForUser(user).ListProducts(workshop)
	if err != nil {
		return nil, nil, nil, err
	}
	lower := strings.ToLower(text)
	family := ""
	if strings.Contains(lower, "petg") {
		family = "petg"
	}
	if family == "" && isColorReference(lower) {
		for i := len(history) - 1; i >= 0; i-- {
			if strings.Contains(strings.ToLower(history[i].Content), "petg") {
				family = "petg"
				break
			}
		}
	}
	wantedColor := color(text)
	var material, product map[string]any
	matches := 0
	for _, m := range materials {
		name := strings.ToLower(fmt.Sprint(m["name"]))
		match := strings.Contains(lower, name) || (family != "" && strings.Contains(name, family) && (wantedColor == "" || color(name) == wantedColor))
		if hasAny(lower, "кисти", "кистей", "кисть") && strings.Contains(name, "кист") {
			match = true
		}
		if match {
			matches++
			material = m
		}
	}
	if matches != 1 {
		material = nil
	}
	for _, p := range products {
		if strings.Contains(lower, strings.ToLower(fmt.Sprint(p["name"]))) {
			if product != nil {
				product = nil
				break
			}
			product = p
		}
	}
	if product == nil && task != nil && task.State.ProductID > 0 {
		for _, p := range products {
			if p["id"] == task.State.ProductID {
				product = p
				break
			}
		}
	}
	if product == nil && hasAny(lower, "этот", "этого", "этом") {
		for i := len(history) - 1; i >= 0 && product == nil; i-- {
			for _, p := range products {
				if strings.Contains(strings.ToLower(history[i].Content), strings.ToLower(fmt.Sprint(p["name"]))) {
					product = p
					break
				}
			}
		}
	}
	items := []memory.Item{}
	if material != nil {
		material["display_stock"] = inventory.Quantity(material, "current_stock")
		material["display_minimum"] = inventory.Quantity(material, "minimum_stock")
		items = append(items, memory.Item{Layer: "DOMAIN", Source: "materials", Key: fmt.Sprint(material["id"]), Content: asJSON(material)})
	}
	if product != nil {
		items = append(items, memory.Item{Layer: "DOMAIN", Source: "products", Key: fmt.Sprint(product["id"]), Content: asJSON(product)})
		bom, err := a.Prod.ForUser(user).GetBOM(workshop, product["id"].(int64))
		if err != nil {
			return nil, nil, nil, err
		}
		items = append(items, memory.Item{Layer: "DOMAIN", Source: "bom_items", Key: fmt.Sprint(product["id"]), Content: asJSON(bom)})
	}
	return items, material, product, nil
}
func formatMaterial(item map[string]any) string {
	return fmt.Sprintf("%s: %s.", item["name"], inventory.Quantity(item, "current_stock"))
}
func (a *WorkshopAgent) taskAnswer(user, workshop int64, task *memory.Task, prefs []memory.LongTerm) string {
	title := fmt.Sprintf("План: %g шт. %s.", task.State.Quantity, task.State.ProductName)
	for _, key := range []string{"packaging", "material", "override"} {
		if value := task.State.Parameters[key]; value != "" {
			title += "\nТолько для этой задачи: " + value
		}
	}
	if task.State.ProductID > 0 {
		needs, err := a.Prod.ForUser(user).Requirements(workshop, task.State.ProductID, task.State.Quantity)
		if err != nil {
			return title + "\nРасчёт BOM недоступен; проверьте спецификацию."
		}
		ids := []int64{}
		for id := range needs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			item, err := a.Inv.ForUser(user).Material(workshop, id)
			if err != nil {
				return title + "\nДанные материала недоступны."
			}
			title += fmt.Sprintf("\nКомпонент #%d (%s): %s по BOM.", id, item["name"], inventory.FormatQuantity(needs[id], inventory.DisplayUnit(item)))
		}
	}
	for _, m := range prefs {
		if m.Type == "PROCESS_RULE" {
			title += "\nПравило процесса: " + m.Value
		}
	}
	return title
}
func (a *WorkshopAgent) memoryCommand(ctx context.Context, m *memory.Service, sc memory.Scope, chat int64, text string) (string, *llm.Usage, error) {
	usage := &llm.Usage{}
	parts := strings.Fields(text)
	if parts[0] == "/session" && len(parts) == 2 && parts[1] == "new" {
		if err := m.EndSession(sc); err != nil {
			return "", usage, err
		}
		_, err := m.EnsureSession(memory.Scope{UserID: sc.UserID, WorkshopID: sc.WorkshopID}, chat)
		return "Начат новый диалог. Рабочая задача и постоянные предпочтения сохранены.", usage, err
	}
	if parts[0] == "/task" {
		task, err := m.ActiveWorking(sc)
		if err != nil {
			return "", usage, err
		}
		if task == nil {
			return memory.ErrNoTask.Error(), usage, nil
		}
		sc.TaskID = task.ID
		if len(parts) == 2 && (parts[1] == "complete" || parts[1] == "cancel") {
			err := m.CompleteWorkingMemory(sc, parts[1] == "cancel")
			return "Задача закрыта и исключена из активного контекста.", usage, err
		}
		return a.taskAnswer(sc.UserID, sc.WorkshopID, task, nil), usage, nil
	}
	if len(parts) > 1 && (parts[1] == "confirm" || parts[1] == "cancel") {
		c, err := m.TakeProposal(sc, chat)
		if errors.Is(err, sql.ErrNoRows) {
			return "Нет ожидающего подтверждения для этой сессии.", usage, nil
		}
		if err != nil {
			return "", usage, err
		}
		if parts[1] == "cancel" {
			return "Сохранение отменено.", usage, nil
		}
		if c.Target == memory.Domain {
			err = a.Prod.ForUser(sc.UserID).SetBOMQuantity(sc.WorkshopID, c.ProductID, c.MaterialID, c.Quantity)
			if err != nil {
				return "", usage, err
			}
			return "BOM обновлён через доменный сервис. Копия состава в долговременной памяти не создавалась.", usage, nil
		}
		if c.Category == "deactivate" {
			err = m.DeactivateLongTermMemory(sc, c.ProductID)
			return "Запись памяти отключена.", usage, err
		}
		if c.Category == "forget_preference" {
			err = m.ForgetPreference(sc, c.Key)
			return "Личное предпочтение удалено.", usage, err
		}
		item := memory.LongTerm{Type: c.Category, Key: c.Key, Value: c.Value, Source: "explicit_user_confirmation", ScopeType: "user", Category: "profile"}
		if c.Target == memory.Workshop {
			item.ScopeType = "process"
			item.Category = "quality_control"
		}
		err = m.SaveLongTermMemory(sc, item)
		if err != nil {
			return "", usage, err
		}
		return "Сохранено в долговременной памяти: " + c.Key + " = " + c.Value + ".", usage, nil
	}
	if len(parts) == 3 && parts[1] == "forget" {
		candidate := memory.Candidate{Scope: sc, Target: memory.Personal, Category: "forget_preference", Key: parts[2], RequiresConfirmation: true}
		if id, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			candidate.Category = "deactivate"
			candidate.ProductID = id
		}
		err := m.Propose(sc, chat, candidate)
		return "Отключить запись? /memory confirm или /memory cancel", usage, err
	}
	if len(parts) == 2 && parts[1] == "trace" {
		trace, err := m.LastTrace(sc)
		if errors.Is(err, sql.ErrNoRows) {
			return "В этой сессии ещё нет trace запроса.", usage, nil
		}
		if err != nil {
			return "", usage, err
		}
		lines := []string{"Memory Trace (токены оценочные):"}
		for _, item := range trace.Items {
			lines = append(lines, fmt.Sprintf("%s · %s · ~%d tokens", item.Layer, item.Source, item.Tokens))
		}
		return strings.Join(lines, "\n"), usage, nil
	}
	built, err := (memory.AgentContextBuilder{Memory: m}).Build(sc, "память", nil, memory.All)
	if err != nil {
		return "", usage, err
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "short":
			lines := []string{"Текущий диалог:"}
			for _, msg := range built.Short {
				lines = append(lines, msg.Role+": "+msg.Content)
			}
			return strings.Join(lines, "\n"), usage, nil
		case "working":
			if built.Working == nil {
				return memory.ErrNoTask.Error(), usage, nil
			}
			return a.taskAnswer(sc.UserID, sc.WorkshopID, built.Working, nil), usage, nil
		case "long":
			lines := []string{"Релевантная долговременная память:"}
			for _, record := range built.Long {
				lines = append(lines, fmt.Sprintf("%s %s=%s (v%d)", record.ScopeType, record.Key, record.Value, record.Version))
			}
			return strings.Join(lines, "\n"), usage, nil
		}
	}
	working := "нет"
	if built.Working != nil {
		working = fmt.Sprintf("%s · %g шт.", built.Working.Type, built.Working.State.Quantity)
	}
	return fmt.Sprintf("🧠 Память\nКраткосрочная: %d сообщений\nРабочая: %s\nДолговременная: %d релевантных записей\n\n/memory short · /memory working · /memory long\n/memory trace\n/session new — новый диалог\n/task complete — завершить задачу", len(built.Short), working, len(built.Long)), usage, nil
}
