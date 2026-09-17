package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPollingTimeoutExceedsServerWait(t *testing.T) {
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 40*time.Second || time.Until(deadline) > 45*time.Second {
			t.Fatal("polling deadline must allow the 30-second server wait")
		}
		if r.URL.Query().Get("timeout") != "30" || r.URL.Query().Get("offset") != "123" {
			t.Fatal("wrong polling parameters")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`)), Header: http.Header{}}, nil
	})}
	b := &Bot{Token: "secret-test-token", HTTPClient: client}
	updates, err := b.getUpdates(123)
	if err != nil || len(updates) != 0 {
		t.Fatalf("polling: %v %v", updates, err)
	}
	if client.Timeout != 15*time.Second {
		t.Fatal("ordinary API timeout changed")
	}
}

func TestPollingTimeoutDoesNotExposeToken(t *testing.T) {
	b := &Bot{Token: "secret-test-token", HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}}
	_, err := b.getUpdates(0)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("missing timeout diagnostic: %v", err)
	}
	if strings.Contains(err.Error(), b.Token) || strings.Contains(err.Error(), "https://") {
		t.Fatal("request URL exposed")
	}
}
