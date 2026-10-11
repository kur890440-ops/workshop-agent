package llm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"workshop-agent/internal/stability"
	"workshop-agent/internal/workshops"
)

type measureTransport struct {
	base               http.RoundTripper
	active, max, calls atomic.Int32
}

func (m *measureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/api/chat" {
		m.calls.Add(1)
		n := m.active.Add(1)
		for old := m.max.Load(); n > old && !m.max.CompareAndSwap(old, n); old = m.max.Load() {
		}
		defer m.active.Add(-1)
	}
	return m.base.RoundTrip(r)
}
func TestStabilityLiveOllama(t *testing.T) {
	if os.Getenv("STABILITY_LIVE_LOCAL") != "1" {
		t.Skip("requires explicit local synthetic live run")
	}
	ws := workshops.NewService(filepath.Join(t.TempDir(), "live.db"))
	defer ws.Close()
	store, e := stability.NewStore(ws.DB(), stability.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	rt := stability.New(store)
	local, e := NewOllamaClient("http://127.0.0.1:11434", "qwen3:4b-q4_K_M", 60*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	local.RequireCapabilities = true
	m := &measureTransport{base: http.DefaultTransport}
	local.HTTPClient.Transport = m
	provider := &ControlledProvider{Provider: local, Runtime: rt, Local: true}
	generate := func(ctx context.Context) error {
		_, _, e := provider.Generate(ctx, "Reply with exactly one word: OK", false, 64)
		return e
	}
	start := time.Now()
	for i := 0; i < 10; i++ {
		var result error
		j, e := rt.Submit(context.Background(), 1, 1, fmt.Sprint(i), false, func(ctx context.Context) error { result = generate(ctx); return result }, nil)
		if e != nil {
			t.Fatal(e)
		}
		j.Wait()
		if result != nil {
			t.Fatal(i, result)
		}
	}
	t.Logf("REAL Ollama 10 sequential generations PASS elapsed_ms=%d", time.Since(start).Milliseconds())
	start = time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := int64(2); i < 7; i++ {
		wg.Add(1)
		go func(u int64) {
			defer wg.Done()
			var result error
			j, e := rt.Submit(context.Background(), u, u, fmt.Sprint(u), false, func(ctx context.Context) error { result = generate(ctx); return result }, nil)
			if e == nil {
				j.Wait()
				e = result
			}
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if m.max.Load() != 1 {
		t.Fatal("local concurrency", m.max.Load())
	}
	t.Logf("REAL Ollama 5 simultaneous admitted=5 rejected=0 local_max_active=%d elapsed_ms=%d", m.max.Load(), time.Since(start).Milliseconds())
	for i := 0; i < 10; i++ {
		if e = rt.Charge(1, false); e != nil {
			t.Fatal(e)
		}
	}
	if e = rt.Charge(1, false); stability.Code(e) != "RATE_LIMIT_EXCEEDED" {
		t.Fatal(e)
	}
	t.Log("REAL admission quota 20 allowed,21st rejected; only first10 called Ollama")
	before := m.calls.Load()
	ctx := stability.WithScope(context.Background(), &stability.Scope{User: 9, Settings: store.Get()})
	_, _, e = provider.Generate(ctx, strings.Repeat("1234567890", 2000), false, 64)
	if stability.Code(e) != "CONTEXT_LIMIT_EXCEEDED" || m.calls.Load() != before {
		t.Fatal("oversize contacted model", e)
	}
	t.Log("REAL provider preflight oversized rejected before transport")
	ctx, cancel := context.WithCancel(stability.WithScope(context.Background(), &stability.Scope{User: 10, Settings: store.Get()}))
	done := make(chan error, 1)
	go func() {
		_, _, e := provider.Generate(ctx, "Write a detailed numbered list of 1000 different animals, with descriptions.", false, 2048)
		done <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	for m.calls.Load() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	start = time.Now()
	cancel()
	if e = <-done; e == nil {
		t.Fatal("generation ended before cancellation evidence")
	}
	t.Logf("REAL HTTP cancellation error=%s elapsed_ms=%d", stability.Code(e), time.Since(start).Milliseconds())
	ctx = stability.WithScope(context.Background(), &stability.Scope{User: 11, Settings: store.Get()})
	if e = generate(ctx); e != nil {
		t.Fatal("recovery", e)
	}
	t.Log("REAL generation after cancellation PASS; no Cloud invoked")
}
