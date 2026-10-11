package webchat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// A real loopback HTTP listener verifies browser cookies across separate requests.
// No production bot, model, marketplace, or database is contacted.
func TestNetworkLoginToLogout(t *testing.T) {
	s := fixture(t)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	r, e := server.Client().Get(server.URL + "/chat")
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	code := regexp.MustCompile(`/login ([A-Z2-7]{8})`).FindSubmatch(body)
	if r.StatusCode != 200 || len(code) != 2 {
		t.Fatal(r.StatusCode)
	}
	if e = s.ConfirmLogin(900001, string(code[1])); e != nil {
		t.Fatal(e)
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/login/status", nil)
	req.AddCookie(r.Cookies()[0])
	r, e = server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	var cookie *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid session cookie")
	}
	for _, path := range []string{"/chat", "/api/chat/messages/status"} {
		req, _ = http.NewRequest("GET", server.URL+path, nil)
		req.AddCookie(cookie)
		r, e = server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatal(path, r.StatusCode)
		}
	}
	req, _ = http.NewRequest("POST", server.URL+"/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", s.Config.PublicBaseURL)
	req.Header.Set("X-Workshop-Request", "1")
	r, e = server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	req, _ = http.NewRequest("GET", server.URL+"/api/chat/messages/status", nil)
	req.AddCookie(cookie)
	r, e = server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("revoked cookie accepted")
	}
}
func TestProductionConfigRejectsUnsafeTransport(t *testing.T) {
	s := fixture(t)
	c := s.Config
	c.PublicBaseURL = "http://192.0.2.1:8080"
	c.ListenAddress = "0.0.0.0:8080"
	if c.Validate() == nil {
		t.Fatal("public HTTP allowed")
	}
	c.PublicBaseURL = "https://chat.example.test"
	if c.Validate() == nil {
		t.Fatal("missing proxy trust allowed")
	}
	c.TrustedProxies = []string{"127.0.0.1/32"}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.TrustedProxies = []string{"not a CIDR"}
	if c.Validate() == nil {
		t.Fatal("invalid CIDR")
	}
}
