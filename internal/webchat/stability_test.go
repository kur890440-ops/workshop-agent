package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"workshop-agent/internal/message"
	"workshop-agent/internal/workshops"
)

func postBody(s *Service, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/chat/messages", strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	r.Header.Set("Origin", s.Config.PublicBaseURL)
	r.Header.Set("X-Workshop-Request", "1")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func waitRequest(t *testing.T, s *Service, w *httptest.ResponseRecorder) Request {
	t.Helper()
	var out Request
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e := s.DB.QueryRow("SELECT status,error_code FROM web_requests WHERE request_id=?", out.ID).Scan(&out.Status, &out.Error); e != nil {
			t.Fatal(e)
		}
		if out.Status == "COMPLETED" || out.Status == "FAILED" || out.Status == "CANCELLED" {
			s.Runtime.Wait()
			return out
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("request timeout")
	return out
}
func TestSharedUserQuotaAcrossSessionsAndAsyncError(t *testing.T) {
	s := fixture(t)
	defer s.Runtime.Wait()
	token, ss := login(t, s)
	s.Handle = func(context.Context, message.Inbound) error { return nil }
	start := time.Now()
	for i := 0; i < 20; i++ {
		w := postBody(s, token, `{"text":"synthetic"}`)
		if w.Code != 202 {
			t.Fatal(i, w.Code, w.Body.String())
		}
		if out := waitRequest(t, s, w); out.Status != "COMPLETED" {
			t.Fatal(out)
		}
	}
	other, _ := login(t, s)
	w := postBody(s, other, `{"text":"synthetic"}`)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Header())
	}
	if e := s.Runtime.Charge(ss.UserID, true); e != nil {
		t.Fatal("confirmation denied", e)
	}
	t.Logf("20 accepted COMPLETED; new session 21st=429; latency=%s", time.Since(start))
}
func TestAsyncErrorCancellationBodyAndOwnership(t *testing.T) {
	s := fixture(t)
	token, _ := login(t, s)
	defer s.Runtime.Wait()
	s.Handle = func(context.Context, message.Inbound) error { return errors.New("synthetic failure") }
	w := postBody(s, token, `{"text":"synthetic"}`)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	if out := waitRequest(t, s, w); out.Status != "FAILED" || out.Error != "APPLICATION_FAILED" {
		t.Fatal(out)
	}
	for body, want := range map[string]int{`{"text":"x"} trailing`: 400, `{"text":"x","unknown":1}`: 400, `{"text":"` + strings.Repeat("x", 12001) + `"}`: 400, `{"text":"x"}` + strings.Repeat(" ", 20000): 413} {
		if w = postBody(s, token, body); w.Code != want {
			t.Fatal(w.Code, want)
		}
	}
	entered := make(chan struct{})
	s.Handle = func(ctx context.Context, _ message.Inbound) error { close(entered); <-ctx.Done(); return ctx.Err() }
	w = postBody(s, token, `{"text":"cancel"}`)
	<-entered
	var out Request
	json.Unmarshal(w.Body.Bytes(), &out)
	other, _ := login(t, s)
	if denied := req(s, "POST", "/api/requests/"+out.ID+"/cancel", other, s.Config.PublicBaseURL); denied.Code != 404 {
		t.Fatal("cross-session cancellation", denied.Code)
	}
	if ok := req(s, "POST", "/api/requests/"+out.ID+"/cancel", token, s.Config.PublicBaseURL); ok.Code != 200 {
		t.Fatal(ok.Code)
	}
	if out = waitRequest(t, s, w); out.Status != "CANCELLED" {
		t.Fatal(out)
	}
	s.Handle = func(context.Context, message.Inbound) error { return nil }
	if out = waitRequest(t, s, postBody(s, token, `{"text":"recover"}`)); out.Status != "COMPLETED" {
		t.Fatal(out)
	}
}
func TestHistoryWorkshopRevocationAndLegacyRetention(t *testing.T) {
	s := fixture(t)
	token, ss := login(t, s)
	var n int
	var name, path string
	s.DB.QueryRow("PRAGMA database_list").Scan(&n, &name, &path)
	ws := workshops.NewService(path)
	defer ws.Close()
	a, e := ws.ActiveWorkshop(ss.UserID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ws.CreateOwnedWorkshop(ss.UserID, "B"); e != nil {
		t.Fatal(e)
	}
	if e = s.Deliver(-ss.ID, message.Output{Role: "assistant", Text: "synthetic A", WorkshopID: a}); e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("INSERT INTO web_messages(session_id,role,text,created_at) VALUES(?,'assistant','legacy',unixepoch())", ss.ID)
	before := req(s, "GET", "/api/chat/messages/status", token, "")
	if !strings.Contains(before.Body.String(), "synthetic A") || strings.Contains(before.Body.String(), "legacy") {
		t.Fatal(before.Body.String())
	}
	s.DB.Exec("UPDATE workshop_members SET status='removed',is_active=0 WHERE user_id=? AND workshop_id=?", ss.UserID, a)
	after := req(s, "GET", "/api/chat/messages/status", token, "")
	if after.Code != 200 || strings.Contains(after.Body.String(), "synthetic A") {
		t.Fatal(after.Code, after.Body.String())
	}
	s.DB.QueryRow("SELECT count(*) FROM web_messages WHERE session_id=?", ss.ID).Scan(&n)
	if n != 2 {
		t.Fatal("history deleted", n)
	}
}

func TestIdempotentPostDoesNotReplay(t *testing.T) {
	s := fixture(t)
	token, _ := login(t, s)
	count := 0
	s.Handle = func(context.Context, message.Inbound) error { count++; return nil }
	send := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/chat/messages", strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Origin", s.Config.PublicBaseURL)
		r.Header.Set("X-Workshop-Request", "1")
		r.Header.Set("Idempotency-Key", "synthetic-retry")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	first := send(`{"text":"synthetic"}`)
	if first.Code != 202 {
		t.Fatal(first.Code, first.Body.String())
	}
	out := waitRequest(t, s, first)
	again := send(`{"text":"synthetic"}`)
	var replay Request
	json.Unmarshal(again.Body.Bytes(), &replay)
	if replay.ID != out.ID || count != 1 {
		t.Fatal("replayed", count)
	}
	if w := send(`{"text":"changed"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
}

func TestFiveConcurrentWebRequestsQueue(t *testing.T) {
	s := fixture(t)
	var seq int
	var name, path string
	s.DB.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path)
	ws := workshops.NewService(path)
	defer ws.Close()
	tokens := []string{}
	for i := int64(0); i < 5; i++ {
		tg := 990001 + i
		u, e := ws.UpsertUser(tg, "synthetic", "Synthetic", "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ws.CreateOwnedWorkshop(u, "Synthetic"); e != nil {
			t.Fatal(e)
		}
		code, nonce, e := s.CreateLogin(fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.ConfirmLogin(tg, code); e != nil {
			t.Fatal(e)
		}
		token, e := s.Complete(nonce)
		if e != nil {
			t.Fatal(e)
		}
		tokens = append(tokens, token)
	}
	release := make(chan struct{})
	s.Handle = func(ctx context.Context, _ message.Inbound) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var wg sync.WaitGroup
	results := make(chan int, 5)
	for _, token := range tokens {
		wg.Add(1)
		go func(token string) { defer wg.Done(); results <- postBody(s, token, `{"text":"synthetic"}`).Code }(token)
	}
	wg.Wait()
	close(results)
	close(release)
	s.Runtime.Wait()
	for code := range results {
		if code != 202 {
			t.Fatal(code)
		}
	}
	var completed int
	s.DB.QueryRow("SELECT count(*) FROM web_requests WHERE status='COMPLETED'").Scan(&completed)
	if completed != 5 {
		t.Fatal(completed)
	}
	t.Log("5 HTTP requests: 5 accepted202, 5 completed; separate authenticated users; mock application")
}
