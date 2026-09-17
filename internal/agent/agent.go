package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type WorkshopAgent struct {
	LLM    llm.Client
	WS     *workshops.Service
	Inv    *inventory.Service
	Prod   *products.Service
	Memory *memory.Service
}

func NewWorkshopAgent(llmClient llm.Client, ws *workshops.Service, inv *inventory.Service, prod *products.Service) *WorkshopAgent {
	return &WorkshopAgent{LLM: llmClient, WS: ws, Inv: inv, Prod: prod, Memory: memory.New(ws.DB())}
}

func (a *WorkshopAgent) HandleMessage(ctx context.Context, userID int64, chatID int64, text string) (string, *llm.Usage, error) {
	workshopID, err := a.WS.ActiveWorkshop(userID)
	if err != nil {
		return "", nil, err
	}
	return a.HandleMessageForWorkshop(ctx, workshopID, userID, chatID, text)
}

func (a *WorkshopAgent) handleLegacyMessage(ctx context.Context, workshopID, userID, chatID int64, text string) (string, *llm.Usage, error) {
	if err := auth.Require(a.WS.DB(), userID, workshopID, auth.WorkshopRead); err != nil {
		return "", nil, err
	}
	bound := *a
	bound.Inv = a.Inv.ForUser(userID)
	bound.Prod = a.Prod.ForUser(userID)
	a = &bound
	noLLM := &llm.Usage{}
	if text == "/start" {
		return "Вы пока не подключены ни к одной мастерской. Используйте bootstrap через CLI или свяжите Telegram chat с workshop.", noLLM, nil
	}
	if isAllStockQuestion(text) {
		answer, err := a.formatAllMaterialStock(workshopID)
		return answer, noLLM, err
	}
	if isAllProductQuestion(text) {
		answer, err := a.formatAllProductStock(workshopID)
		return answer, noLLM, err
	}
	if isPurchaseQuestion(text) {
		answer, err := a.formatPurchaseNeeds(workshopID)
		return answer, noLLM, err
	}
	input := text
	if _, wrapped := a.LLM.(contextualClient); !wrapped {
		profile, err := personalization.New(a.WS.DB()).ForUser(userID).ResolveProfile(text, nil)
		if err != nil {
			return "", nil, err
		}
		if err = personalization.New(a.WS.DB()).ForUser(userID).SaveTrace(workshopID, profile); err != nil {
			return "", nil, err
		}
		input = profile.Context + "\nCURRENT MESSAGE:\n" + text
	}
	cmd, usage, err := a.LLM.ParseCommand(ctx, input)
	if err != nil {
		return "", usage, err
	}
	if cmd == nil {
		return "Не удалось понять запрос. Попробуйте сформулировать проще.", usage, nil
	}
	permission := map[string]auth.Permission{"record_production": auth.ProductionCreate, "assemble_product": auth.ProductionCreate, "record_shipment": auth.ShipmentsWrite, "calculate_requirements": auth.BOMRead, "get_product_stock": auth.ProductsRead}[cmd.Action]
	if permission != "" {
		if err := auth.Require(a.WS.DB(), userID, workshopID, permission); err != nil {
			return "", usage, err
		}
	}
	if cmd.Action == "clarification" {
		return "Нужно уточнить данные: укажите конкретный материал, товар или количество.", usage, nil
	}
	if cmd.Action == "get_daily_summary" {
		answer, err := a.DailySummary(userID, workshopID, time.Now())
		return answer, usage, err
	}
	if cmd.Action == "get_material_stock" {
		materialName := cmd.Material
		if materialName == "" {
			return "Уточните, какой материал вы хотите проверить.", usage, nil
		}
		item, err := a.Inv.MaterialByName(workshopID, materialName)
		if err != nil {
			return "", usage, err
		}
		return fmt.Sprintf("Остаток %s: %s", materialName, inventory.Quantity(item, "current_stock")), usage, nil
	}
	if cmd.Action == "get_all_material_stock" {
		answer, err := a.formatAllMaterialStock(workshopID)
		return answer, usage, err
	}
	if cmd.Action == "get_purchase_needs" {
		answer, err := a.formatPurchaseNeeds(workshopID)
		return answer, usage, err
	}
	if cmd.Action == "record_production" {
		if cmd.Product == "" {
			return "Уточните товар для производства.", usage, nil
		}
		if _, err := a.Prod.GetProductByName(workshopID, cmd.Product); err != nil {
			return "", usage, err
		}
		return fmt.Sprintf("Записать производство %s: произведено %.0f, брак %.0f. Подтверждение требуется.", cmd.Product, cmd.Attempted, cmd.Scrap), usage, nil
	}
	if cmd.Action == "assemble_product" {
		if cmd.Product == "" {
			return "Уточните набор для сборки.", usage, nil
		}
		return fmt.Sprintf("Сборка %s в количестве %.0f готова к подтверждению.", cmd.Product, cmd.Quantity), usage, nil
	}
	if cmd.Action == "record_shipment" {
		if cmd.Product == "" {
			return "Уточните товар для отгрузки.", usage, nil
		}
		return fmt.Sprintf("Отгрузка %s в количестве %.0f на %s ожидает подтверждения.", cmd.Product, cmd.Quantity, cmd.Channel), usage, nil
	}
	if cmd.Action == "calculate_requirements" {
		if cmd.Product == "" {
			return "Уточните продукт для расчета потребности.", usage, nil
		}
		return fmt.Sprintf("Для %s на %v единиц требуется уточнить материалы.", cmd.Product, cmd.Quantity), usage, nil
	}
	if cmd.Action == "get_product_stock" {
		if cmd.Product == "" {
			return "Уточните товар для остатка.", usage, nil
		}
		return fmt.Sprintf("Остаток товара %s: проверяется по данным склада.", cmd.Product), usage, nil
	}
	return fmt.Sprintf("Команда %s пока не поддержана в MVP.", cmd.Action), usage, nil
}

func isAllStockQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return (strings.Contains(lower, "остат") || strings.Contains(lower, "сколько осталось")) &&
		(strings.Contains(lower, "какие") || strings.Contains(lower, "все") || strings.Contains(lower, "в целом") || strings.Contains(lower, "у нас"))
}

// LocalReadCommand preserves existing deterministic read routes for adapters.
func LocalReadCommand(text string) *llm.StructuredCommand {
	if IsDailySummary(text) {
		return &llm.StructuredCommand{Action: "get_daily_summary"}
	}
	if isAllStockQuestion(text) {
		return &llm.StructuredCommand{Action: "get_all_material_stock"}
	}
	if isPurchaseQuestion(text) {
		return &llm.StructuredCommand{Action: "get_purchase_needs"}
	}
	return nil
}

func isPurchaseQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	orderingQuestion := strings.Contains(lower, "заказ") && (strings.Contains(lower, "что ") || strings.Contains(lower, "нужно") || strings.Contains(lower, "надо"))
	return orderingQuestion || strings.Contains(lower, "закуп") || strings.Contains(lower, "купить") || strings.Contains(lower, "покуп")
}

func isAllProductQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return (strings.Contains(lower, "товар") || strings.Contains(lower, "продукт") || strings.Contains(lower, "издел")) &&
		(strings.Contains(lower, "какие") || strings.Contains(lower, "есть") || strings.Contains(lower, "все") || strings.Contains(lower, "у нас"))
}

func (a *WorkshopAgent) formatAllMaterialStock(workshopID int64) (string, error) {
	materials, err := a.Inv.ListMaterials(workshopID)
	if err != nil {
		return "", err
	}
	if len(materials) == 0 {
		return "Материалы не найдены.", nil
	}
	result := "Текущие остатки материалов:\n"
	for _, item := range materials {
		result += fmt.Sprintf("- %v: %s\n", item["name"], inventory.Quantity(item, "current_stock"))
	}
	return result, nil
}

func (a *WorkshopAgent) formatAllProductStock(workshopID int64) (string, error) {
	products, err := a.Prod.ListProducts(workshopID)
	if err != nil {
		return "", err
	}
	if len(products) == 0 {
		return "Товары не найдены.", nil
	}
	result := "Товары на складе:\n"
	for _, item := range products {
		result += fmt.Sprintf("- %v: %.2f шт.\n", item["name"], item["current_stock"])
	}
	return result, nil
}

func (a *WorkshopAgent) formatPurchaseNeeds(workshopID int64) (string, error) {
	needs, err := a.Inv.ListPurchaseNeeds(workshopID)
	if err != nil {
		return "", err
	}
	if len(needs) == 0 {
		return "Закупка не требуется: все материалы выше минимального остатка.", nil
	}
	result := "Нужно заказать:\n"
	for _, item := range needs {
		result += fmt.Sprintf("- %v: %s (сейчас %s, минимум %s)\n", item["name"], inventory.Quantity(item, "order_quantity"), inventory.Quantity(item, "current_stock"), inventory.Quantity(item, "minimum_stock"))
	}
	return result, nil
}

func (a *WorkshopAgent) SaveSession(workshopID, chatID, userID int64, contextText, pendingAction string) error {
	if err := auth.Require(a.WS.DB(), userID, workshopID, auth.WorkshopRead); err != nil {
		return err
	}
	if pendingAction != "" {
		return fmt.Errorf("use explicit working memory or pending action operations")
	}
	m := a.Memory.ForUser(userID)
	sc, err := m.EnsureSession(memory.Scope{UserID: userID, WorkshopID: workshopID}, chatID)
	if err != nil {
		return err
	}
	return m.AppendShortTerm(sc, "assistant", contextText)
}
