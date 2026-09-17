package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCompletionRejectsTruncatedAnswer(t *testing.T) {
	for _, reason := range []string{"stop", "length"} {
		t.Run(reason, func(t *testing.T) {
			c, _ := NewOpenRouterClient("test", "https://example.invalid", "test")
			c.HTTPClient = &http.Client{Transport: semanticTransport(func(r *http.Request) (*http.Response, error) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request["max_tokens"] != float64(2048) {
					t.Fatalf("budget: %v", request["max_tokens"])
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": reason, "message": map[string]string{"content": "report"}}}, "usage": Usage{TotalTokens: 12}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
			})}
			answer, usage, err := c.Complete(context.Background(), "report")
			if answer != "report" || usage == nil || usage.TotalTokens != 12 {
				t.Fatal(answer, usage, err)
			}
			if (err != nil) != (reason == "length") {
				t.Fatalf("finish_reason=%s error=%v", reason, err)
			}
		})
	}
}
