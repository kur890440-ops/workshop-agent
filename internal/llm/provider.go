package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"workshop-agent/internal/stability"
)

// GenerationProvider never executes tools. JSON commands remain data for the
// existing authorized semantic/Agent pipeline.
type GenerationProvider interface {
	Generate(context.Context, string, bool, int) (string, *Usage, error)
	ModelName() string
}

func (c *OpenRouterClient) Generate(ctx context.Context, prompt string, structured bool, budget int) (string, *Usage, error) {
	return c.request(ctx, prompt, structured, budget)
}
func (c *OpenRouterClient) ModelName() string { return c.Model }

var ErrInvalidStructuredOutput = errors.New("INVALID_STRUCTURED_OUTPUT")
var ErrValidationFailed = errors.New("VALIDATION_FAILED")
var ErrLocalUnavailable = errors.New("LOCAL_UNAVAILABLE")
var ErrCloudUnavailable = errors.New("CLOUD_UNAVAILABLE")

type OllamaClient struct {
	capsMu                    sync.Mutex
	capacity, templateReserve int
	tokenizer                 *qwenTokenizer
	capDigest                 string
	RequireCapabilities       bool
	modelMu                   sync.Mutex
	BaseURL, Model            string
	HTTPClient                *http.Client
}

func NewOllamaClient(baseURL, model string, timeout time.Duration) (*OllamaClient, error) {
	u, err := url.Parse(baseURL)
	// A local provider must not become a remote data transfer through configuration.
	if err != nil || u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || timeout <= 0 {
		return nil, errors.New("LLM_LOCAL_BASE_URL must be a loopback HTTP origin; timeout must be positive")
	}
	if strings.Contains(strings.ToLower(model), "cloud") {
		return nil, errors.New("Ollama cloud models cannot be local providers")
	}
	return &OllamaClient{BaseURL: strings.TrimRight(baseURL, "/"), Model: model, HTTPClient: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *OllamaClient) ModelName() string { c.modelMu.Lock(); defer c.modelMu.Unlock(); return c.Model }

// DiscoverModel uses exact tags from Ollama; never pulls or creates a model.
func (c *OllamaClient) DiscoverModel(ctx context.Context) error {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return ErrLocalUnavailable
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return ErrLocalUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrLocalUnavailable
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tags) != nil {
		return ErrLocalUnavailable
	}
	if c.Model != "" {
		for _, m := range tags.Models {
			if m.Name == c.Model {
				return nil
			}
		}
		return ErrLocalUnavailable
	}
	for _, preferred := range []string{"qwen3:4b-q4_K_M", "qwen3:4b", "qwen3:4b-q8_0"} {
		for _, m := range tags.Models {
			if m.Name == preferred {
				c.Model = m.Name
				return nil
			}
		}
	}
	return ErrLocalUnavailable
}

func (c *OllamaClient) Generate(ctx context.Context, prompt string, structured bool, budget int) (string, *Usage, error) {
	if c.ModelName() == "" {
		if err := c.DiscoverModel(ctx); err != nil {
			return "", nil, err
		}
	}
	var preflightErr error
	budget, preflightErr = c.CheckPrompt(ctx, prompt, budget)
	if preflightErr != nil {
		return "", nil, preflightErr
	}
	payload := map[string]any{"model": c.ModelName(), "messages": []map[string]string{{"role": "user", "content": prompt}}, "stream": false, "think": false, "options": map[string]any{"temperature": 0, "num_predict": budget, "num_ctx": stability.Config(ctx).MaxContext}}
	if structured {
		payload["format"] = "json"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, ErrValidationFailed
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", nil, ErrLocalUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, ErrLocalUnavailable
	}
	var r struct {
		Model   string `json:"model"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Done       bool   `json:"done"`
		DoneReason string `json:"done_reason"`
		Prompt     int    `json:"prompt_eval_count"`
		Completion int    `json:"eval_count"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&r) != nil {
		return "", nil, ErrInvalidStructuredOutput
	}
	usage := &Usage{Model: r.Model, PromptTokens: r.Prompt, CompletionTokens: r.Completion, TotalTokens: r.Prompt + r.Completion}
	if r.DoneReason == "length" || !r.Done {
		return "", usage, ErrResponseTruncated
	}
	if strings.TrimSpace(r.Message.Content) == "" {
		return "", usage, ErrValidationFailed
	}
	return strings.TrimSpace(r.Message.Content), usage, nil
}

func (c *OllamaClient) CheckPrompt(ctx context.Context, prompt string, budget int) (int, error) {
	if !c.RequireCapabilities {
		return Preflight(ctx, prompt, budget, 512)
	}
	capacity, reserve, err := c.Capabilities(ctx)
	if err != nil {
		return 0, err
	}
	if stability.Config(ctx).MaxContext > capacity {
		return 0, &stability.Error{Code: "MODEL_CONTEXT_UNSUPPORTED"}
	}
	c.capsMu.Lock()
	tokenizer := c.tokenizer
	c.capsMu.Unlock()
	if tokenizer == nil {
		return Preflight(ctx, prompt, budget, reserve)
	}
	n, err := tokenizer.Count(ctx, prompt)
	if err != nil {
		return 0, err
	}
	return checkBudget(ctx, n+512, budget, "ollama_gguf_qwen2_bpe_with_template_reserve")
}

// Revalidate the installed digest, so retagging a model cannot reuse a stale
// tokenizer/capacity snapshot. This is a local metadata read, not inference.
func (c *OllamaClient) modelDigest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/tags", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", ErrLocalUnavailable
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tags); err != nil {
		return "", err
	}
	for _, m := range tags.Models {
		if m.Name == c.ModelName() && m.Digest != "" {
			return m.Digest, nil
		}
	}
	return "", ErrLocalUnavailable
}

// Capabilities are obtained from the installed model, never inferred from a tag.
func (c *OllamaClient) Capabilities(ctx context.Context) (int, int, error) {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	digest, err := c.modelDigest(ctx)
	if err != nil {
		return 0, 0, err
	}
	if c.capacity > 0 && c.capDigest == digest {
		return c.capacity, c.templateReserve, nil
	}
	body, _ := json.Marshal(map[string]any{"model": c.ModelName(), "verbose": true})
	req, e := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/show", bytes.NewReader(body))
	if e != nil {
		return 0, 0, e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.HTTPClient.Do(req)
	if e != nil {
		return 0, 0, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, 0, ErrLocalUnavailable
	}
	var v ollamaShow
	if e = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&v); e != nil {
		return 0, 0, e
	}
	arch, _ := v.ModelInfo["general.architecture"].(string)
	cap, _ := v.ModelInfo[arch+".context_length"].(float64)
	if cap <= 0 {
		return 0, 0, &stability.Error{Code: "MODEL_CAPACITY_UNVERIFIED"}
	}
	tokenizer, e := buildQwenTokenizer(v)
	if e != nil {
		return 0, 0, e
	}
	// Do not cache metadata obtained while the tag was being reassigned.
	after, e := c.modelDigest(ctx)
	if e != nil {
		return 0, 0, e
	}
	if after != digest {
		return 0, 0, ErrLocalUnavailable
	}
	c.tokenizer = tokenizer
	c.capDigest = digest
	c.capacity = int(cap)
	c.templateReserve = len([]byte(v.Template)) + 512
	return c.capacity, c.templateReserve, nil
}
