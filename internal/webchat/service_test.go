package webchat

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"workshop-agent/internal/workshops"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "web.db"))
	t.Cleanup(func() { ws.Close() })
	u, err := ws.UpsertUser(900001, "owner", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.CreateOwnedWorkshop(u, "Test"); err != nil {
		t.Fatal(err)
	}
	s, err := New(ws.DB(), Config{Enabled: true, ListenAddress: "127.0.0.1:8080", PublicBaseURL: "http://127.0.0.1:8080", LoginCodeTTL: 2 * time.Minute, SessionTTL: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func login(t *testing.T, s *Service) (string, Session) {
	t.Helper()
	code, nonce, e := s.CreateLogin("test")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmLogin(900001, code); e != nil {
		t.Fatal(e)
	}
	token, e := s.Complete(nonce)
	if e != nil {
		t.Fatal(e)
	}
	ss, e := s.Authenticate(token)
	if e != nil {
		t.Fatal(e)
	}
	return token, ss
}
func TestLoginLifecycleAndBrowserBinding(t *testing.T) {
	s := fixture(t)
	code, a, e := s.CreateLogin("a")
	if e != nil {
		t.Fatal(e)
	}
	_, b, e := s.CreateLogin("b")
	if e != nil {
		t.Fatal(e)
	}
	var stored, bound string
	if e = s.DB.QueryRow(`SELECT code_hash,browser_hash FROM pending_web_logins WHERE id=?`, code[:4]).Scan(&stored, &bound); e != nil || stored == code || bound == a {
		t.Fatal("plaintext stored", e)
	}
	if e = s.ConfirmLogin(999999, code); e == nil {
		t.Fatal("unknown identity authorized")
	}
	if e = s.ConfirmLogin(900001, code); e != nil {
		t.Fatal(e)
	}
	if token, e := s.Complete(b); e != nil || token != "" {
		t.Fatal("browser B authenticated", e)
	}
	token, e := s.Complete(a)
	if e != nil || token == "" {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(token); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmLogin(900001, code); e == nil {
		t.Fatal("reused code")
	}
	if _, e = s.Complete(a); e == nil {
		t.Fatal("reused browser nonce")
	}
}
func TestExpiryWrongAttemptsAndLockout(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		s := fixture(t)
		code, nonce, _ := s.CreateLogin("a")
		s.Now = func() time.Time { return time.Now().Add(3 * time.Minute) }
		if s.ConfirmLogin(900001, code) == nil {
			t.Fatal("expired code")
		}
		if _, e := s.Complete(nonce); e == nil {
			t.Fatal("expired nonce")
		}
	})
	t.Run("attempts", func(t *testing.T) {
		s := fixture(t)
		code, _, _ := s.CreateLogin("a")
		wrong := code[:4] + "!!!!"
		for i := 1; i <= MaxAttempts; i++ {
			if s.ConfirmLogin(900001, wrong) == nil {
				t.Fatal("wrong accepted")
			}
			var n int
			var status string
			s.DB.QueryRow(`SELECT attempt_count,status FROM pending_web_logins WHERE id=?`, code[:4]).Scan(&n, &status)
			if n != i {
				t.Fatal(n, i)
			}
			if i == MaxAttempts && status != "blocked" {
				t.Fatal(status)
			}
		}
		s.Now = func() time.Time { return time.Now().Add(61 * time.Second) }
		if s.ConfirmLogin(900001, code) == nil {
			t.Fatal("blocked login accepted")
		}
	})
}
func TestSessionsRevocationExpiryAndAccessLoss(t *testing.T) {
	for _, reason := range []string{"expiry", "logout", "disabled", "membership"} {
		t.Run(reason, func(t *testing.T) {
			s := fixture(t)
			token, ss := login(t, s)
			switch reason {
			case "expiry":
				s.Now = func() time.Time { return time.Now().Add(13 * time.Hour) }
			case "logout":
				if e := s.Revoke(ss.ID); e != nil {
					t.Fatal(e)
				}
			case "disabled":
				s.DB.Exec(`UPDATE users SET is_active=0 WHERE id=?`, ss.UserID)
			case "membership":
				s.DB.Exec(`UPDATE workshop_members SET status='disabled' WHERE user_id=?`, ss.UserID)
			}
			if _, e := s.Authenticate(token); e == nil {
				t.Fatal("session accepted after", reason)
			}
		})
	}
}
func TestRateLimitsAndConcurrentConsumption(t *testing.T) {
	s := fixture(t)
	for i := 0; i < 5; i++ {
		if _, _, e := s.CreateLogin("a"); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e := s.CreateLogin("a"); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		_ = s.ConfirmLogin(555, "AAAAAAAA")
	}
	if e := s.ConfirmLogin(555, "AAAAAAAA"); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	code, nonce, _ := s.CreateLogin("b")
	if e := s.ConfirmLogin(900001, code); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); token, e := s.Complete(nonce); results <- e == nil && token != "" }()
	}
	wg.Wait()
	close(results)
	n := 0
	for ok := range results {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatal("not single-use", n)
	}
}
func req(s *Service, method, path, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"text":"X"}`))
	r.RemoteAddr = "127.0.0.1:1234"
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Workshop-Request", "1")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestHTTPAuthCSRFLogoutAndCookieFlags(t *testing.T) {
	s := fixture(t)
	w := req(s, "GET", "/chat", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/login ") {
		t.Fatal(w.Code)
	}
	if len(w.Result().Cookies()) != 1 || !w.Result().Cookies()[0].HttpOnly {
		t.Fatal("pending cookie")
	}
	for _, path := range []string{"/api/chat/messages", "/api/confirmations/test", "/logout"} {
		if w = req(s, "POST", path, "", s.Config.PublicBaseURL); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	token, _ := login(t, s)
	if w = req(s, "GET", "/chat", token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `id="history"`) {
		t.Fatal(w.Code)
	}
	for _, origin := range []string{"", "https://evil.invalid"} {
		for _, path := range []string{"/api/chat/messages", "/api/confirmations/test", "/logout"} {
			if w = req(s, "POST", path, token, origin); w.Code != 403 {
				t.Fatal(path, w.Code)
			}
		}
	}
	if w = req(s, "POST", "/logout", token, s.Config.PublicBaseURL); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("cookie not cleared")
	}
	if w = req(s, "GET", "/api/chat/messages/status", token, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	s.Config.PublicBaseURL = "https://chat.example.test"
	s.Config.TrustedProxies = []string{"127.0.0.1/32"}
	r := httptest.NewRequest("GET", "/chat", nil)
	r.RemoteAddr = "127.0.0.1:4567"
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatal(c)
	}
	r.RemoteAddr = "192.0.2.1:333"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted proxy")
	}
}
func TestPollingLimitAndCleanup(t *testing.T) {
	s := fixture(t)
	for i := 0; i < 60; i++ {
		req(s, "GET", "/api/login/status", "", "")
	}
	if w := req(s, "GET", "/api/login/status", "", ""); w.Code != 429 {
		t.Fatal(w.Code)
	}
	_, ss := login(t, s)
	s.Revoke(ss.ID)
	if e := s.Cleanup(t.Context()); e != nil {
		t.Fatal(e)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM web_sessions`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
