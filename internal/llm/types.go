package llm

import "context"

type StructuredCommand struct {
	Reference    *EntityReference `json:"reference,omitempty"`
	QuantityMode string           `json:"quantity_mode,omitempty"`
	Amount       *float64         `json:"amount,omitempty"`
	Unit         string           `json:"unit,omitempty"`
	Action       string           `json:"action"`
	Product      string           `json:"product,omitempty"`
	Material     string           `json:"material,omitempty"`
	Quantity     float64          `json:"quantity,omitempty"`
	Attempted    float64          `json:"attempted,omitempty"`
	Scrap        float64          `json:"scrap,omitempty"`
	Date         string           `json:"date,omitempty"`
	Channel      string           `json:"channel,omitempty"`
	Notes        string           `json:"notes,omitempty"`
	ProductID    int64            `json:"product_id,omitempty"`
	MaterialID   int64            `json:"material_id,omitempty"`
}

type EntityReference struct {
	Kind       string `json:"kind"`
	EntityType string `json:"entity_type"`
	Position   int    `json:"position,omitempty"`
	Name       string `json:"name,omitempty"`
}

type SemanticClient interface {
	Interpret(context.Context, string, string) (*StructuredCommand, *Usage, error)
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Client interface {
	ParseCommand(ctx context.Context, text string) (*StructuredCommand, *Usage, error)
}
