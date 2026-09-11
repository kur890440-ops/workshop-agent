package llm

import "context"

type StructuredCommand struct {
	Action     string  `json:"action"`
	Product    string  `json:"product,omitempty"`
	Material   string  `json:"material,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
	Attempted  float64 `json:"attempted,omitempty"`
	Scrap      float64 `json:"scrap,omitempty"`
	Date       string  `json:"date,omitempty"`
	Channel    string  `json:"channel,omitempty"`
	Notes      string  `json:"notes,omitempty"`
	ProductID  int64   `json:"product_id,omitempty"`
	MaterialID int64   `json:"material_id,omitempty"`
}

type Client interface {
	ParseCommand(ctx context.Context, text string) (*StructuredCommand, error)
}
