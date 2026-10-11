package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/stability"
	"workshop-agent/internal/workshops"

	"github.com/dlclark/regexp2"
)

func TestQwenBPEMergePriorityAndCancellation(t *testing.T) {
	q := &qwenTokenizer{ranks: map[tokenPair]int{{"b", "c"}: 0, {"a", "b"}: 1, {"a", "bc"}: 2}, pattern: regexp2.MustCompile(qwenPattern, 0)}
	for text, want := range map[string]int{"abc": 1, "ab": 1, "bc": 1, "a b": 3, "123": 3, "аб": 4} {
		got, e := q.Count(context.Background(), text)
		if e != nil || got != want {
			t.Fatalf("%q got=%d want=%d err=%v", text, got, want, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := q.Count(ctx, "abc"); e == nil {
		t.Fatal("ignored cancellation")
	}
	if _, e := q.Count(context.Background(), string([]byte{255})); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestQwenUnsupportedTokenizerFallback(t *testing.T) {
	q, e := buildQwenTokenizer(ollamaShow{ModelInfo: map[string]any{}})
	if e != nil || q != nil {
		t.Fatal("unsupported metadata should retain byte estimate", e)
	}
	// Actual metadata completeness is exercised in the local-model test below.
}

func TestOllamaBudgetCacheFollowsDigest(t *testing.T) {
	digest, capacity, shows := "one", 40960, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "synthetic", "digest": digest}}})
		case "/api/show":
			shows++
			json.NewEncoder(w).Encode(ollamaShow{ModelInfo: map[string]any{"general.architecture": "synthetic", "synthetic.context_length": capacity}, Template: "synthetic"})
		default:
			t.Error("preflight sent a generation")
			http.Error(w, "unexpected generation", 500)
		}
	}))
	defer server.Close()
	c, _ := NewOllamaClient(server.URL, "synthetic", time.Second)
	c.RequireCapabilities = true
	for i := 0; i < 2; i++ {
		if _, e := c.CheckPrompt(context.Background(), "short", 32); e != nil {
			t.Fatal(e)
		}
	}
	if shows != 1 {
		t.Fatal("metadata not cached", shows)
	}
	digest, capacity = "two", 1024
	if _, e := c.CheckPrompt(context.Background(), "short", 32); stability.Code(e) != "MODEL_CONTEXT_UNSUPPORTED" || shows != 2 {
		t.Fatal("stale digest cache", e, shows)
	}
}

func TestQwenLiveBudgetAndReference(t *testing.T) {
	if os.Getenv("STABILITY_LIVE_LOCAL") != "1" {
		t.Skip("requires real local Qwen GGUF metadata")
	}
	c, e := NewOllamaClient("http://127.0.0.1:11434", "qwen3:4b-q4_K_M", 60*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	c.RequireCapabilities = true
	if _, _, e = c.Capabilities(context.Background()); e != nil {
		t.Fatal(e)
	}
	if c.tokenizer == nil {
		t.Fatal("verified tokenizer unavailable")
	}
	// Golden content counts independently generated with Hugging Face tokenizers
	// from this GGUF's vocabulary/merges (not from the Go implementation).
	for _, tc := range []struct {
		text  string
		count int
	}{
		{"Объясни разницу между PETG и ABS для 3D-печати.", 20},
		{"Почему повышение температуры сопла может ухудшить качество печати?", 26},
		{"I'm printing PETG. DON'T raise 240°C to 280°C!", 21},
		{"  a\n\n\t  б  \r\n ", 7},
		{"🙂 🚀 中文 café e\u0301 ١٢٣", 16},
		{"<|im_start|>user\nПривет<|im_end|>", 7},
		{"0123456789", 10},
		{strings.Repeat("Служебный контекст мастерской. ", 1000), 11001},
	} {
		n, err := c.tokenizer.Count(context.Background(), tc.text)
		if err != nil || n != tc.count {
			t.Fatalf("reference mismatch bytes=%d got=%d want=%d err=%v", len(tc.text), n, tc.count, err)
		}
	}
	// Verify fail-closed behavior for corrupted metadata of a supported model.
	resp, e := c.HTTPClient.Post(c.BaseURL+"/api/show", "application/json", strings.NewReader(`{"model":"qwen3:4b-q4_K_M","verbose":true}`))
	if e != nil {
		t.Fatal(e)
	}
	var show ollamaShow
	e = json.NewDecoder(resp.Body).Decode(&show)
	resp.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	delete(show.ModelInfo, "tokenizer.ggml.merges")
	if _, e = buildQwenTokenizer(show); e == nil {
		t.Fatal("missing merges accepted")
	}
	measure := &measureTransport{base: c.HTTPClient.Transport}
	if measure.base == nil {
		measure.base = http.DefaultTransport
	}
	c.HTTPClient.Transport = measure
	ws := workshops.NewService(filepath.Join(t.TempDir(), "budget.db"))
	defer ws.Close()
	store, e := stability.NewStore(ws.DB(), stability.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	provider := &ControlledProvider{Provider: c, Runtime: stability.New(store), Local: true}
	ctx := WithRun(context.Background(), &Run{})
	prompt := strings.Repeat("Служебный контекст мастерской. ", 1000) + "\nОтветь одним словом: принято."
	_, usage, e := provider.Generate(ctx, prompt, false, 32)
	if e != nil {
		t.Fatal("valid Russian context rejected", e)
	}
	r := RunFrom(ctx)
	if r.TokenCountMethod != "ollama_gguf_qwen2_bpe_with_template_reserve" || usage.PromptTokens > r.InputEstimate {
		t.Fatal("underestimated context", r, usage)
	}
	t.Logf("Russian prompt bytes=%d BPE+reserve=%d actual_prompt_tokens=%d output_reserve=%d", len(prompt), r.InputEstimate, usage.PromptTokens, r.OutputReserve)
	before := measure.calls.Load()
	_, _, e = provider.Generate(ctx, strings.Repeat("1234567890", 2000), false, 32)
	if stability.Code(e) != "CONTEXT_LIMIT_EXCEEDED" || measure.calls.Load() != before {
		t.Fatal("oversize reached generation", e)
	}
}
