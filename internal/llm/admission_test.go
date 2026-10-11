package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/stability"
)

func TestFinalPromptPreflightBeforeTransport(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"done":true,"message":{"content":"ok"}}`))
	}))
	defer server.Close()
	c, _ := NewOllamaClient(server.URL, "synthetic", time.Second)
	run := &Run{}
	ctx := WithRun(context.Background(), run)
	_, _, e := c.Generate(ctx, strings.Repeat("x", 16384), false, 2048)
	if stability.Code(e) != "CONTEXT_LIMIT_EXCEEDED" || calls != 0 {
		t.Fatal(e, calls)
	}
	if run.TokenCountMethod != "conservative_utf8_byte_bound" {
		t.Fatal(run.TokenCountMethod)
	}
	_, _, e = c.Generate(ctx, "small", false, 2048)
	if e != nil || calls != 1 {
		t.Fatal(e, calls)
	}
}
func TestProviderCancellation(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		close(entered)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()
	c, _ := NewOllamaClient(server.URL, "synthetic", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, e := c.Generate(ctx, "small", false, 16); done <- e }()
	<-entered
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("not cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("client stuck")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("server context not cancelled")
	}
}
