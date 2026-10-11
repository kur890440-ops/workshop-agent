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
	"sync"
	"time"
	"workshop-agent/internal/stability"
)

type OpenRouterClient struct {
	capsMu              sync.Mutex
	capacity            int
	RequireCapabilities bool
	APIKey              string
	BaseURL             string
	Model               string
	HTTPClient          *http.Client
}

var ErrResponseTruncated = errors.New("LLM response truncated by output budget")

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

func legacyCommandPrompt(text string) string {
	return fmt.Sprintf(`Classify the user's request. Return ONE compact JSON object on ONE line.
Allowed actions: get_all_material_stock, get_material_stock, get_purchase_needs, get_daily_summary, record_production, assemble_product, record_shipment, calculate_requirements, get_product_stock, clarification.
Use get_daily_summary for today's actual production quantities, completed orders or daily summary, with no other fields. Other report dates require clarification. Never classify a production instruction as a summary.
Use get_all_material_stock for questions about all inventory or "какие у нас остатки". Use get_material_stock only for one named material. Use get_purchase_needs for what to buy, ordering, low stock, or replenishment.
Use only these keys when needed: action, material, product, quantity, attempted, scrap, channel. Do not include null values, explanations, markdown, or notes.
User request: %s`, text)
}

func (c *OpenRouterClient) ParseCommand(ctx context.Context, text string) (*StructuredCommand, *Usage, error) {
	prompt := legacyCommandPrompt(text)

	content, usage, err := c.request(ctx, prompt, true)
	if err != nil {
		return nil, usage, err
	}
	var cmd StructuredCommand
	if err := json.Unmarshal([]byte(content), &cmd); err != nil {
		return nil, usage, fmt.Errorf("parse llm json: %w; raw=%s", err, content)
	}
	if cmd.Action == "" {
		return nil, usage, errors.New("llm response missing action field")
	}

	return &cmd, usage, nil
}

// Complete uses the same model and temperature=0 for the controlled Day 11 experiment.
func (c *OpenRouterClient) Complete(ctx context.Context, prompt string) (string, *Usage, error) {
	return c.request(ctx, prompt, false, 2048)
}
func (c *OpenRouterClient) request(ctx context.Context, prompt string, jsonObject bool, tokenBudget ...int) (string, *Usage, error) {
	payload := map[string]any{
		"model": c.Model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": prompt,
		}},
		"temperature": 0,
		"max_tokens":  256,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	if !jsonObject {
		delete(payload, "response_format")
		payload["max_tokens"] = 512
	}
	if len(tokenBudget) > 0 {
		payload["max_tokens"] = tokenBudget[0]
	}
	if c.RequireCapabilities {
		cap, e := c.Capabilities(ctx)
		if e != nil {
			return "", nil, e
		}
		if stability.Config(ctx).MaxContext > cap {
			return "", nil, &stability.Error{Code: "MODEL_CONTEXT_UNSUPPORTED"}
		}
	}
	b, e := Preflight(ctx, prompt, payload["max_tokens"].(int), 512)
	if e != nil {
		return "", nil, e
	}
	payload["max_tokens"] = b
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal llm payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("create llm request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "http://localhost")
	req.Header.Set("X-Title", "WorkshopAgent")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("call llm provider: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", nil, fmt.Errorf("read llm response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", nil, fmt.Errorf("llm provider returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var llmResp struct {
		Model   string `json:"model"`
		Usage   Usage  `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &llmResp); err != nil {
		return "", nil, fmt.Errorf("decode llm response: %w", err)
	}
	llmResp.Usage.Model = llmResp.Model
	// A reasoning model may spend the entire budget before producing content.
	// Check the finish reason first, including empty and otherwise valid JSON.
	if len(llmResp.Choices) > 0 && llmResp.Choices[0].FinishReason == "length" {
		return strings.TrimSpace(llmResp.Choices[0].Message.Content), &llmResp.Usage, ErrResponseTruncated
	}
	if len(llmResp.Choices) == 0 || strings.TrimSpace(llmResp.Choices[0].Message.Content) == "" {
		return "", &llmResp.Usage, errors.New("empty llm response")
	}

	content := strings.TrimSpace(llmResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	return content, &llmResp.Usage, nil
}

func (c *OpenRouterClient) Capabilities(ctx context.Context) (int, error) {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	if c.capacity > 0 {
		return c.capacity, nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/models", nil)
	if e != nil {
		return 0, e
	}
	resp, e := c.HTTPClient.Do(req)
	if e != nil {
		return 0, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, &stability.Error{Code: "MODEL_CAPACITY_UNVERIFIED"}
	}
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Context int    `json:"context_length"`
		}
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); e != nil {
		return 0, e
	}
	for _, m := range result.Data {
		if strings.EqualFold(m.ID, c.Model) && m.Context > 0 {
			c.capacity = m.Context
			return c.capacity, nil
		}
	}
	return 0, &stability.Error{Code: "MODEL_CAPACITY_UNVERIFIED"}
}
