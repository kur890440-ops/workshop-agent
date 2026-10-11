package main

import (
	"context"
	"errors"
	"github.com/joho/godotenv"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
	"workshop-agent/internal/llm"
)

func TestHybridLiveCloud(t *testing.T) {
	if os.Getenv("HYBRID_LIVE_CLOUD") != "1" {
		t.Skip("explicit synthetic live cloud smoke only")
	}
	env, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal("cannot read local configuration")
	}
	if env["LLM_API_KEY"] == "" || env["LLM_MODEL"] == "" {
		t.Skip("cloud credentials/model unavailable")
	}
	cloud, err := llm.NewOpenRouterClient(env["LLM_API_KEY"], env["LLM_BASE_URL"], env["LLM_MODEL"])
	if err != nil {
		t.Fatal("cloud configuration unavailable")
	}
	cloud.HTTPClient.Timeout = 60 * time.Second
	cloud.HTTPClient.Transport = liveTransport{t: t}
	local, err := llm.NewOllamaClient("http://127.0.0.1:1", "qwen3:4b-q4_K_M", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	h := &llm.HybridClient{Local: local, Cloud: cloud, Policy: llm.LLMFallbackPolicy{Mode: llm.Auto}}
	// Entire payload is synthetic public test data; this test explicitly authorizes it.
	prompt := "Объясни PETG одним предложением. Это синтетическая проверка, данных мастерской нет."
	p := &llm.ConsentRequired{Prompt: prompt, Budget: 2048, Kind: "answer"}
	r := &llm.Run{ApprovedHash: llm.PayloadHash(p)}
	answer, _, err := h.Complete(llm.WithRun(context.Background(), r), prompt)
	if err != nil || answer == "" || !r.Fallback || r.Provider != "cloud" {
		t.Fatalf("live fallback failed: status=%s provider=%s", r.Error, r.Provider)
	}
	t.Logf("real cloud fallback model=%s reason=%s latency_ms=%d", r.Model, r.FallbackReason, r.CloudMS)
	p = &llm.ConsentRequired{Prompt: "Проверь только этот синтетический результат: PETG относится к термопластам. Ответь кратко; инструменты недоступны.", Budget: 2048, Kind: "answer"}
	r = &llm.Run{ApprovedHash: llm.PayloadHash(p)}
	answer, _, err = h.Resume(llm.WithRun(context.Background(), r), p)
	if err != nil || answer == "" {
		t.Fatalf("live recheck failed: %s", r.Error)
	}
	t.Logf("real read-only recheck model=%s latency_ms=%d", r.Model, r.CloudMS)
}

type liveTransport struct{ t *testing.T }

func (l liveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		var code syscall.Errno
		errors.As(err, &code)
		l.t.Logf("cloud network error type=%T errno=%d", err, code)
	} else {
		l.t.Logf("cloud HTTP status=%d", response.StatusCode)
	}
	return response, err
}
