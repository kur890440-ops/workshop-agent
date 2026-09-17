package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type semanticTransport func(*http.Request) (*http.Response, error)

func (f semanticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSemanticRepairBoundAndUsage(t *testing.T) {
	c, _ := NewOpenRouterClient("test", "https://example.invalid", "test")
	calls := 0
	c.HTTPClient = &http.Client{Transport: semanticTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		content := `{"action":"get_task"}`
		if calls == 1 {
			content = `{"intent":"unknown"}`
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}, "usage": Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
	})}
	cmd, usage, err := c.Interpret(context.Background(), "{}", "задача")
	if err != nil || cmd.Action != "get_task" || calls != 2 || usage.TotalTokens != 30 {
		t.Fatal(cmd, usage, calls, err)
	}
}

func TestSemanticSchema(t *testing.T) {
	for _, raw := range []string{
		`{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":2}}`,
		`{"action":"change_material_stock","reference":{"kind":"name","entity_type":"material","name":"гипса"},"amount":0,"quantity_mode":"absolute"}`,
	} {
		if _, err := DecodeSemantic(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{
		`not json`, `{"action":"delete_workshop"}`, `{"action":"get_task","material_id":777}`, `{"action":"get_task","material_id":0}`,
		`{"action":"get_material_stock","reference":{"kind":"id","entity_type":"material","id":7}}`,
		`{"action":"get_material_stock","reference":{"kind":"list_position","entity_type":"material","position":0}}`,
		`{"action":"get_material_stock","reference":{"kind":"name","entity_type":"material","name":"Гипс"},"amount":10}`,
		`{"action":"change_material_stock","reference":{"kind":"last","entity_type":"material"},"quantity_mode":"absolute"}`,
		`{"action":"get_task"} {"action":"get_task"}`,
	} {
		if _, err := DecodeSemantic(raw); err == nil {
			t.Fatal("accepted invalid command", raw)
		}
	}
}
