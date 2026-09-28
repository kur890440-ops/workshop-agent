package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSemanticTruncationRecovery(t *testing.T) {
	for _, first := range []string{"", `{"action":`, `{"action":"get_all_material_stock"}`} {
		for _, failAgain := range []bool{false, true} {
			c, _ := NewOpenRouterClient("fixture", "https://example.invalid", "fixture")
			calls := 0
			c.HTTPClient = &http.Client{Transport: semanticTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				expected := float64(1024)
				if calls == 2 {
					expected = 4096
				}
				if calls > 2 || body["max_tokens"] != expected {
					t.Fatal("unbounded or wrong recovery", calls, body["max_tokens"])
				}
				reason, content := "length", first
				if calls == 2 && !failAgain {
					reason = "stop"
					content = `{"action":"clarification"}`
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": reason, "message": map[string]string{"content": content}}}, "usage": Usage{PromptTokens: 10, CompletionTokens: 1024, TotalTokens: 1034}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
			})}
			cmd, usage, e := c.Interpret(context.Background(), "{}", "question")
			if calls != 2 || usage == nil || usage.TotalTokens != 2068 || usage.CompletionTokens != 2048 {
				t.Fatal(calls, usage, e)
			}
			if failAgain {
				if !errors.Is(e, ErrResponseTruncated) || cmd != nil {
					t.Fatal(cmd, e)
				}
			} else if e != nil || cmd.Action != "clarification" {
				t.Fatal(cmd, e)
			}
		}
	}
}

func TestSemanticProviderErrorDoesNotRetry(t *testing.T) {
	c, _ := NewOpenRouterClient("fixture", "https://example.invalid", "fixture")
	calls := 0
	c.HTTPClient = &http.Client{Transport: semanticTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}
	cmd, _, e := c.Interpret(context.Background(), "{}", "question")
	if calls != 1 || cmd != nil || e == nil {
		t.Fatal(calls, cmd, e)
	}
}
