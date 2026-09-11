package llm

import (
	"context"
	"strings"
)

type MockClient struct{}

func (m *MockClient) ParseCommand(ctx context.Context, text string) (*StructuredCommand, *Usage, error) {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "гипс") && strings.Contains(lower, "сколько") {
		return &StructuredCommand{Action: "get_material_stock", Material: "гипс"}, &Usage{}, nil
	}
	if strings.Contains(lower, "сделали") || strings.Contains(lower, "произвели") {
		return &StructuredCommand{Action: "record_production", Product: "Stitch", Attempted: 50, Scrap: 3, Date: "today"}, &Usage{}, nil
	}
	if strings.Contains(lower, "собрали") || strings.Contains(lower, "набор") {
		return &StructuredCommand{Action: "assemble_product", Product: "Набор Stitch", Quantity: 20}, &Usage{}, nil
	}
	if strings.Contains(lower, "отправили") || strings.Contains(lower, "отгруз") {
		return &StructuredCommand{Action: "record_shipment", Product: "Набор Stitch", Quantity: 20, Channel: "Ozon"}, &Usage{}, nil
	}
	if strings.Contains(lower, "что купить") || strings.Contains(lower, "заказать") {
		return &StructuredCommand{Action: "calculate_requirements", Product: "Набор Stitch", Quantity: 300}, &Usage{}, nil
	}
	if strings.Contains(lower, "остатки") || strings.Contains(lower, "сколько осталось") {
		return &StructuredCommand{Action: "get_product_stock", Product: "Stitch"}, &Usage{}, nil
	}
	return &StructuredCommand{Action: "clarification"}, &Usage{}, nil
}
