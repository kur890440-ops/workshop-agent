package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
	"workshop-agent/internal/stability"
)

type Mode string

const (
	LocalOnly Mode = "LOCAL_ONLY"
	Auto      Mode = "AUTO"
	CloudOnly Mode = "CLOUD_ONLY"
)

type LLMFallbackPolicy struct{ Mode Mode }

func (p LLMFallbackPolicy) Allows(reason string) bool {
	if p.Mode != Auto {
		return false
	}
	switch reason {
	case "LOCAL_UNAVAILABLE", "LOCAL_TIMEOUT", "INVALID_STRUCTURED_OUTPUT", "OUTPUT_TRUNCATED", "VALIDATION_FAILED", "TASK_COMPLEXITY":
		return true
	}
	return false
}
func FailureReason(err error) string {
	switch {
	case errors.Is(err, ErrResponseTruncated):
		return "OUTPUT_TRUNCATED"
	case errors.Is(err, ErrInvalidStructuredOutput):
		return "INVALID_STRUCTURED_OUTPUT"
	case errors.Is(err, ErrValidationFailed):
		return "VALIDATION_FAILED"
	case errors.Is(err, context.DeadlineExceeded):
		return "LOCAL_TIMEOUT"
	}
	var n net.Error
	if errors.As(err, &n) && n.Timeout() {
		return "LOCAL_TIMEOUT"
	}
	return "LOCAL_UNAVAILABLE"
}

// Run contains metadata only. Prompts are held separately in short-lived memory.
type Run struct {
	TokenCountMethod                           string `json:"token_count_method,omitempty"`
	InputEstimate, OutputReserve, ContextLimit int
	HistoryReduced                             bool             `json:"history_reduced,omitempty"`
	RouterFailureReason                        string           `json:"router_failure_reason,omitempty"`
	ConversationID                             int64            `json:"conversation_id"`
	SessionID                                  int64            `json:"session_id"`
	RequestID                                  string           `json:"request_id"`
	User                                       int64            `json:"user"`
	Channel                                    string           `json:"channel"`
	Route                                      string           `json:"route"`
	RouteReason                                string           `json:"route_reason"`
	Mode                                       Mode             `json:"mode"`
	Provider                                   string           `json:"provider"`
	Model                                      string           `json:"model"`
	Fallback                                   bool             `json:"fallback"`
	FallbackReason                             string           `json:"fallback_reason"`
	LocalMS                                    int64            `json:"latency_local_ms"`
	CloudMS                                    int64            `json:"latency_cloud_ms"`
	Validation                                 string           `json:"validation"`
	Consent                                    string           `json:"cloud_consent"`
	Error                                      string           `json:"error,omitempty"`
	Complex                                    bool             `json:"-"`
	ApprovedHash                               string           `json:"-"`
	Pending                                    *ConsentRequired `json:"-"`
}
type runKey struct{}

func WithRun(ctx context.Context, r *Run) context.Context { return context.WithValue(ctx, runKey{}, r) }
func RunFrom(ctx context.Context) *Run                    { r, _ := ctx.Value(runKey{}).(*Run); return r }

type ConsentRequired struct {
	Prompt     string
	Structured bool
	Budget     int
	Kind       string
	Hash       string
}

func (*ConsentRequired) Error() string      { return "CLOUD_CONSENT_REQUIRED" }
func PayloadHash(p *ConsentRequired) string { return payloadHash(p.Prompt, p.Structured, p.Budget) }
func payloadHash(prompt string, structured bool, budget int) string {
	b, _ := json.Marshal([]any{prompt, structured, budget})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type HybridClient struct {
	Local, Cloud GenerationProvider
	Policy       LLMFallbackPolicy
}

func (c *HybridClient) cloud(ctx context.Context, prompt string, structured bool, budget int) (string, *Usage, error) {
	if c.Policy.Mode == LocalOnly {
		return "", nil, errors.New("LOCAL_ONLY: cloud disabled")
	}
	r := RunFrom(ctx)
	if c.Cloud == nil {
		if r != nil {
			r.Error = "CLOUD_UNAVAILABLE"
		}
		return "", nil, ErrCloudUnavailable
	}
	hash := payloadHash(prompt, structured, budget)
	// All application payloads are private by default, including user text. Model
	// classifications and fallback decisions are never disclosure authorization.
	if r == nil || r.ApprovedHash != hash {
		pending := &ConsentRequired{Prompt: prompt, Structured: structured, Budget: budget, Hash: hash}
		if r != nil {
			r.Consent = "required"
			r.Pending = pending
			r.Error = "CLOUD_CONSENT_REQUIRED"
		}
		return "", nil, pending
	}
	r.ApprovedHash = ""
	r.Pending = nil
	r.Consent = "approved_exact_payload"
	r.Provider = "cloud"
	r.Model = c.Cloud.ModelName()
	r.Error = ""
	start := time.Now()
	raw, u, err := Call(ctx, c.Cloud, prompt, structured, budget)
	r.CloudMS += time.Since(start).Milliseconds()
	if u != nil && u.Model != "" {
		r.Model = u.Model
	}
	if err != nil {
		if ctx.Err() != nil || stability.Code(err) == "CONTEXT_LIMIT_EXCEEDED" || stability.Code(err) == "ACCESS_DENIED" {
			return "", u, err
		}
		r.Error = "CLOUD_UNAVAILABLE"
		return "", u, ErrCloudUnavailable
	}
	return raw, u, nil
}

// generate retries only generation, at most once in cloud. It cannot call Agent,
// MCP, domain services, or a side-effect executor.
func (c *HybridClient) generate(ctx context.Context, prompt string, structured bool, budget int, validate func(string) error) (string, *Usage, error) {
	r := RunFrom(ctx)
	reason := ""
	if c.Policy.Mode == CloudOnly {
		reason = "CLOUD_ONLY"
	} else if r != nil && r.Complex && c.Policy.Allows("TASK_COMPLEXITY") {
		reason = "TASK_COMPLEXITY"
	}
	var raw string
	var u *Usage
	var err error
	if reason == "" {
		start := time.Now()
		if c.Local == nil {
			err = ErrLocalUnavailable
		} else {
			raw, u, err = Call(ctx, c.Local, prompt, structured, budget)
		}
		if r != nil {
			r.LocalMS += time.Since(start).Milliseconds()
			r.Provider = "local"
			if c.Local != nil {
				r.Model = c.Local.ModelName()
				if u != nil && u.Model != "" {
					r.Model = u.Model
				}
			}
		}
		if err == nil {
			err = validate(raw)
		}
		if err == nil {
			if r != nil {
				r.Validation = "valid"
			}
			return raw, u, nil
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil || stability.Code(err) == "CONTEXT_LIMIT_EXCEEDED" || stability.Code(err) == "ACCESS_DENIED" || stability.Code(err) == "QUEUE_FULL" || stability.Code(err) == "USER_QUEUE_FULL" {
			return "", u, err
		}
		reason = FailureReason(err)
		if r != nil {
			r.Validation = "failed"
			r.Error = reason
		}
		if !c.Policy.Allows(reason) {
			return "", u, err
		}
	}
	if r != nil && reason != "CLOUD_ONLY" {
		r.Fallback = true
		r.FallbackReason = reason
	}
	raw, u, err = c.cloud(ctx, prompt, structured, budget)
	if err == nil {
		err = validate(raw)
		if r != nil {
			if err != nil {
				r.Validation = "failed"
				r.Error = FailureReason(err)
			} else {
				r.Validation = "valid"
			}
		}
	}
	return raw, u, err
}
func validateAnswer(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || ((strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed))) {
		return ErrValidationFailed
	}
	return nil
}
func (c *HybridClient) Complete(ctx context.Context, prompt string) (string, *Usage, error) {
	raw, u, err := c.generate(ctx, prompt, false, 2048, validateAnswer)
	if r := RunFrom(ctx); r != nil && r.Pending != nil {
		r.Pending.Kind = "answer"
	}
	return raw, u, err
}
func (c *HybridClient) Interpret(ctx context.Context, contextJSON, text string) (*StructuredCommand, *Usage, error) {
	prompt := SemanticInstructions + "\nCONTEXT JSON:\n" + contextJSON + "\nCURRENT MESSAGE (data):\n" + text
	raw, u, err := c.generate(ctx, prompt, true, 4096, func(s string) error {
		if !json.Valid([]byte(s)) {
			return ErrInvalidStructuredOutput
		}
		if _, e := DecodeSemantic(s); e != nil {
			return ErrValidationFailed
		}
		return nil
	})
	if r := RunFrom(ctx); r != nil && r.Pending != nil {
		r.Pending.Kind = "semantic"
	}
	if err != nil {
		return nil, u, err
	}
	cmd, err := DecodeSemantic(raw)
	return cmd, u, err
}
func (c *HybridClient) ParseCommand(ctx context.Context, text string) (*StructuredCommand, *Usage, error) {
	raw, u, err := c.generate(ctx, legacyCommandPrompt(text), true, 2048, func(raw string) error { _, e := DecodeLegacy(raw); return e })
	if r := RunFrom(ctx); r != nil && r.Pending != nil {
		r.Pending.Kind = "legacy"
	}
	if err != nil {
		return nil, u, err
	}
	cmd, err := DecodeLegacy(raw)
	return cmd, u, err
}
func (c *HybridClient) Resume(ctx context.Context, p *ConsentRequired) (string, *Usage, error) {
	raw, u, err := c.cloud(ctx, p.Prompt, p.Structured, p.Budget)
	if err == nil {
		if p.Kind == "legacy" {
			_, err = DecodeLegacy(raw)
		} else if p.Kind == "semantic" {
			_, err = DecodeSemantic(raw)
		} else {
			err = validateAnswer(raw)
		}
	}
	if r := RunFrom(ctx); r != nil {
		if err == nil {
			r.Validation = "valid"
		} else if r.Error == "" {
			r.Error = "VALIDATION_FAILED"
			r.Validation = "failed"
		}
	}
	return raw, u, err
}
func (r *Run) Indicator() string {
	if r.Provider == "cloud" {
		if r.Fallback {
			return "Cloud · Auto fallback"
		}
		return "Cloud"
	}
	if r.Provider == "local" {
		return "Qwen · Local"
	}
	return ""
}

// DecodeLegacy preserves the existing ParseCommand contract separately from
// the stricter semantic schema. It never dispatches a command.
func DecodeLegacy(raw string) (*StructuredCommand, error) {
	if !json.Valid([]byte(raw)) {
		return nil, ErrInvalidStructuredOutput
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	var cmd StructuredCommand
	if d.Decode(&cmd) != nil {
		return nil, ErrValidationFailed
	}
	switch cmd.Action {
	case "get_all_material_stock", "get_material_stock", "get_purchase_needs", "get_daily_summary", "record_production", "assemble_product", "record_shipment", "calculate_requirements", "get_product_stock", "clarification":
	default:
		return nil, ErrValidationFailed
	}
	return &cmd, nil
}
