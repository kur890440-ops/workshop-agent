package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type generationStub struct {
	raw     string
	err     error
	calls   int
	prompts []string
}

func (s *generationStub) ModelName() string { return "synthetic-model" }
func (s *generationStub) Generate(_ context.Context, p string, _ bool, _ int) (string, *Usage, error) {
	s.calls++
	s.prompts = append(s.prompts, p)
	return s.raw, &Usage{TotalTokens: 1}, s.err
}
func TestHybridFallbackPrivacyAndModes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        Mode
		local       string
		failure     error
		complex     bool
		wantConsent bool
	}{
		{"local success", Auto, "Ответ", nil, false, false},
		{"timeout", Auto, "", context.DeadlineExceeded, false, true},
		{"unavailable", Auto, "", ErrLocalUnavailable, false, true},
		{"truncated", Auto, "", ErrResponseTruncated, false, true},
		{"invalid answer", Auto, "{}", nil, false, true},
		{"complexity", Auto, "Ответ", nil, true, true},
		{"local only", LocalOnly, "", context.DeadlineExceeded, false, false},
		{"local only complex", LocalOnly, "Ответ", nil, true, false},
		{"cloud only", CloudOnly, "Ответ", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &generationStub{raw: tc.local, err: tc.failure}
			cloud := &generationStub{raw: "Облачный ответ"}
			h := &HybridClient{Local: local, Cloud: cloud, Policy: LLMFallbackPolicy{Mode: tc.mode}}
			r := &Run{Complex: tc.complex}
			ctx := WithRun(context.Background(), r)
			_, _, err := h.Complete(ctx, "Внутренний документ: synthetic")
			var consent *ConsentRequired
			if errors.As(err, &consent) != tc.wantConsent {
				t.Fatalf("consent=%v error=%v", tc.wantConsent, err)
			}
			if cloud.calls != 0 {
				t.Fatal("cloud disclosure without consent")
			}
			if tc.mode == CloudOnly && local.calls != 0 {
				t.Fatal("hidden local generation")
			}
			if tc.wantConsent {
				r.ApprovedHash = PayloadHash(consent)
				answer, _, err := h.Resume(ctx, consent)
				if err != nil || answer != "Облачный ответ" || cloud.calls != 1 {
					t.Fatal("resume", err, cloud.calls)
				}
				changed := *consent
				changed.Prompt += " extra"
				if _, _, err = h.Resume(ctx, &changed); err == nil || cloud.calls != 1 {
					t.Fatal("consent reused for different payload")
				}
			}
		})
	}
}
func TestHybridSemanticFallbackValidation(t *testing.T) {
	for _, raw := range []string{"{", `{"action":"delete_everything"}`, `{"action":"get_all_material_stock","invented":true}`} {
		local := &generationStub{raw: raw}
		cloud := &generationStub{raw: `{"action":"get_all_material_stock"}`}
		h := &HybridClient{Local: local, Cloud: cloud, Policy: LLMFallbackPolicy{Mode: Auto}}
		r := &Run{}
		ctx := WithRun(context.Background(), r)
		_, _, err := h.Interpret(ctx, `{"contacts":"synthetic private"}`, "покажи материалы")
		var p *ConsentRequired
		if !errors.As(err, &p) || p.Kind != "semantic" {
			t.Fatal(err)
		}
		if cloud.calls != 0 || !r.Fallback {
			t.Fatal("privacy/fallback")
		}
		r.ApprovedHash = PayloadHash(p)
		if _, _, err = h.Resume(ctx, p); err != nil || cloud.calls != 1 {
			t.Fatal(err)
		}
	}
}
func TestHybridCloudFailureNoRetry(t *testing.T) {
	cloud := &generationStub{err: errors.New("private raw server error")}
	h := &HybridClient{Cloud: cloud, Policy: LLMFallbackPolicy{Mode: CloudOnly}}
	p := &ConsentRequired{Prompt: "synthetic", Budget: 2048, Kind: "answer"}
	r := &Run{ApprovedHash: PayloadHash(p)}
	_, _, err := h.Resume(WithRun(context.Background(), r), p)
	if !errors.Is(err, ErrCloudUnavailable) || cloud.calls != 1 || strings.Contains(r.Error, "private") {
		t.Fatal(err, r)
	}
	h.Policy.Mode = LocalOnly
	if _, _, err = h.Resume(WithRun(context.Background(), r), p); err == nil || cloud.calls != 1 {
		t.Fatal("LOCAL_ONLY bypass")
	}
}
func TestRouterSchemaAndSafeFallback(t *testing.T) {
	for _, raw := range []string{`{"route":"GENERAL","reason":"AMBIGUOUS","complexity":"SIMPLE","general_answer":"hello"}`, `{"route":"GENERAL","reason":"GENERAL_KNOWLEDGE","complexity":"SIMPLE","general_answer":"{}"}`, `{"route":"WORKSHOP","reason":"WORKSHOP_ACTION","complexity":"SIMPLE","general_answer":"","command":"cancel"}`, "{}", "broken"} {
		router := MessageRouter{Local: &generationStub{raw: raw}}
		decision, err := router.Route(context.Background(), RouteInput{Original: "непонятно"})
		if err == nil || decision.Route != "WORKSHOP" {
			t.Fatal(decision, err)
		}
	}
}
func TestOllamaHTTPContract(t *testing.T) {
	for _, reason := range []string{"stop", "length"} {
		t.Run(reason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:4b-q4_K_M"}]}`))
					return
				}
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("request")
				}
				if payload["stream"] != false || payload["think"] != false || payload["format"] != "json" || payload["model"] != "qwen3:4b-q4_K_M" {
					t.Error(payload)
				}
				json.NewEncoder(w).Encode(map[string]any{"done": true, "done_reason": reason, "message": map[string]string{"content": `{"action":"clarification"}`}})
			}))
			defer server.Close()
			c, e := NewOllamaClient(server.URL, "", time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if e = c.DiscoverModel(context.Background()); e != nil {
				t.Fatal(e)
			}
			_, _, e = c.Generate(context.Background(), "synthetic", true, 1024)
			if errors.Is(e, ErrResponseTruncated) != (reason == "length") {
				t.Fatal(e)
			}
		})
	}
	for _, url := range []string{"https://example.com", "http://192.168.1.1:11434", "http://localhost:11434/evil"} {
		if _, e := NewOllamaClient(url, "qwen3:4b", time.Second); e == nil {
			t.Fatal(url)
		}
	}
}
func TestHybridLiveLocalRouting(t *testing.T) {
	if os.Getenv("HYBRID_LIVE_LOCAL") != "1" {
		t.Skip("requires real local Ollama")
	}
	c, e := NewOllamaClient("http://127.0.0.1:11434", "", 120*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.DiscoverModel(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Log("actual model", c.Model)
	for _, tc := range []struct {
		question string
		history  []string
		route    string
	}{
		{"Что такое PETG?", nil, "GENERAL"},
		{"Какие сейчас заказы?", nil, "WORKSHOP"},
		{"Отмени заказ 123", nil, "WORKSHOP"},
		{"А за прошлую неделю?", []string{"Сколько продали дефлекторов?"}, "WORKSHOP"},
		{"А чем отличается от ABS?", []string{"Что такое PETG?"}, "GENERAL"},
		{"Ну и как там это?", nil, "WORKSHOP"},
	} {
		d, e := (MessageRouter{Local: c}).Route(context.Background(), RouteInput{Original: tc.question, Recent: tc.history})
		if d.Route != tc.route || (e != nil && tc.question != "Ну и как там это?") {
			t.Errorf("%q => %+v error=%v", tc.question, d, e)
		} else {
			t.Logf("%q => %s %s", tc.question, d.Route, d.Reason)
		}
	}
	cmd, _, e := (&HybridClient{Local: c, Policy: LLMFallbackPolicy{Mode: LocalOnly}}).Interpret(context.Background(), `{"materials":[{"id":1,"name":"Кисточки","unit":"pcs"}]}`, "Покажи все материалы")
	if e != nil || cmd.Action != "get_all_material_stock" {
		t.Fatal("real semantic", cmd, e)
	}
}

func TestHybridLegacyContract(t *testing.T) {
	local := &generationStub{raw: `{"action":"record_production","product":"synthetic product","attempted":3,"scrap":1}`}
	c := &HybridClient{Local: local, Policy: LLMFallbackPolicy{Mode: LocalOnly}}
	cmd, _, err := c.ParseCommand(context.Background(), "synthetic production")
	if err != nil || cmd.Product != "synthetic product" || cmd.Attempted != 3 || cmd.Scrap != 1 {
		t.Fatal(cmd, err)
	}
}
func TestConsentConsumedEvenOnCloudFailure(t *testing.T) {
	cloud := &generationStub{err: ErrCloudUnavailable}
	h := &HybridClient{Cloud: cloud, Policy: LLMFallbackPolicy{Mode: Auto}}
	p := &ConsentRequired{Prompt: "synthetic", Budget: 2048, Kind: "answer"}
	r := &Run{ApprovedHash: PayloadHash(p)}
	ctx := WithRun(context.Background(), r)
	h.Resume(ctx, p)
	h.Resume(ctx, p)
	if cloud.calls != 1 {
		t.Fatal("same approval was reused")
	}
}
