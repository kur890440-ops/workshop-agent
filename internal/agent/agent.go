package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

type WorkshopAgent struct {
	LLM  llm.Client
	WS   *workshops.Service
	Inv  *inventory.Service
	Prod *products.Service
}

func NewWorkshopAgent(llmClient llm.Client, ws *workshops.Service, inv *inventory.Service, prod *products.Service) *WorkshopAgent {
	return &WorkshopAgent{LLM: llmClient, WS: ws, Inv: inv, Prod: prod}
}

func (a *WorkshopAgent) HandleMessage(ctx context.Context, userID int64, chatID int64, text string) (string, *llm.Usage, error) {
	return a.HandleMessageForWorkshop(ctx, 1, userID, chatID, text)
}

func (a *WorkshopAgent) HandleMessageForWorkshop(ctx context.Context, workshopID, userID, chatID int64, text string) (string, *llm.Usage, error) {
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
	cmd, usage, err := a.LLM.ParseCommand(ctx, text)
	if err != nil {
		return "", usage, err
	}
	if cmd == nil {
		return "Не удалось понять запрос. Попробуйте сформулировать проще.", usage, nil
	}
	if cmd.Action == "clarification" {
		return "Нужно уточнить данные: укажите конкретный материал, товар или количество.", usage, nil
	}
	if cmd.Action == "get_material_stock" {
		materialName := cmd.Material
		if materialName == "" {
			return "Уточните, какой материал вы хотите проверить.", usage, nil
		}
		stock, err := a.Inv.GetMaterialStock(workshopID, materialName)
		if err != nil {
			return "", usage, err
		}
		return fmt.Sprintf("Остаток %s: %.2f", materialName, stock), usage, nil
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

func isPurchaseQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "заказ") || strings.Contains(lower, "закуп") || strings.Contains(lower, "купить") || strings.Contains(lower, "покуп")
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
		result += fmt.Sprintf("- %v: %.2f %v\n", item["name"], item["current_stock"], item["base_unit"])
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
		result += fmt.Sprintf("- %v: %.2f %v (сейчас %.2f, минимум %.2f)\n", item["name"], item["order_quantity"], item["base_unit"], item["current_stock"], item["minimum_stock"])
	}
	return result, nil
}

func (a *WorkshopAgent) SaveSession(workshopID, chatID, userID int64, contextText, pendingAction string) error {
	_, err := a.WS.DB().Exec(`INSERT INTO conversation_sessions (workshop_id, telegram_chat_id, user_id, last_context, pending_action, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO UPDATE SET last_context = excluded.last_context, pending_action = excluded.pending_action, updated_at = excluded.updated_at`, workshopID, chatID, userID, contextText, pendingAction, time.Now().Format(time.RFC3339))
	return err
}
