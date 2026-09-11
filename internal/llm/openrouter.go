package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenRouterClient struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

func NewOpenRouterClient(apiKey, baseURL, model string) (*OpenRouterClient, error) {
	apiKey = strings.TrimSpace(apiKey)
	baseURL = strings.TrimSpace(baseURL)
	model = strings.TrimSpace(model)

	if apiKey == "" {
		return nil, errors.New("LLM_API_KEY is required")
	}
	if model == "" {
		return nil, errors.New("LLM_MODEL is required")
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	return &OpenRouterClient{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      model,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *OpenRouterClient) ParseCommand(ctx context.Context, text string) (*StructuredCommand, error) {
	prompt := fmt.Sprintf(`Classify the user's request. Return ONE compact JSON object on ONE line.
Allowed actions: get_all_material_stock, get_material_stock, get_purchase_needs, record_production, assemble_product, record_shipment, calculate_requirements, get_product_stock, clarification.
Use get_all_material_stock for questions about all inventory or "какие у нас остатки". Use get_material_stock only for one named material. Use get_purchase_needs for what to buy, ordering, low stock, or replenishment.
Use only these keys when needed: action, material, product, quantity, attempted, scrap, channel. Do not include null values, explanations, markdown, or notes.
User request: %s`, text)

	payload := map[string]any{
		"model": c.Model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": prompt,
		}},
		"temperature": 0,
		"max_tokens":  128,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal llm payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create llm request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost")
	req.Header.Set("X-Title", "WorkshopAgent")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call llm provider: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read llm response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("llm provider returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var llmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &llmResp); err != nil {
		return nil, fmt.Errorf("decode llm response: %w", err)
	}
	if len(llmResp.Choices) == 0 || strings.TrimSpace(llmResp.Choices[0].Message.Content) == "" {
		return nil, errors.New("empty llm response")
	}

	content := strings.TrimSpace(llmResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var cmd StructuredCommand
	if err := json.Unmarshal([]byte(content), &cmd); err != nil {
		return nil, fmt.Errorf("parse llm json: %w; raw=%s", err, content)
	}
	if cmd.Action == "" {
		return nil, errors.New("llm response missing action field")
	}

	return &cmd, nil
}
