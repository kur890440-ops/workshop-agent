package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryState struct {
	mu     sync.Mutex
	until  time.Time
	traces []Trace
	broken bool
}

func (s *memoryState) LoadCooldown(context.Context, Scope) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return time.Time{}, errors.New("storage")
	}
	return s.until, nil
}
func (s *memoryState) SaveCooldown(_ context.Context, _ Scope, d time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.After(s.until) {
		s.until = d
	}
	return nil
}
func (s *memoryState) AppendTrace(_ context.Context, t Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, t)
	return nil
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}
}

const fakeKey = "synthetic-ozon-key-never-real"

func fixture(t *testing.T, f transport) (*Client, *memoryState) {
	t.Helper()
	s := &memoryState{}
	c := New("dummy-client", fakeKey, Scope{1, 2, 1}, s, func(context.Context, Scope) error { return nil })
	c.http.Transport = f
	t.Cleanup(c.Close)
	return c, s
}
func request() ProductsRequest {
	var r ProductsRequest
	r.Limit = 100
	r.Filter.Visibility = "ALL"
	return r
}
func TestConfirmedRequestBoundariesAndCredentials(t *testing.T) {
	var calls int
	paths := []string{"/v3/product/list", "/v3/product/info/list", "/v2/product/info/stocks-by-warehouse/fbs", "/v1/analytics/stocks"}
	c, state := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != paths[calls-1] {
			t.Fatal("wrong endpoint", r.URL.Path)
		}
		if r.Method != "POST" || r.URL.Host != "api-seller.ozon.ru" || r.URL.Scheme != "https" || r.Header.Get("Api-Key") != fakeKey || r.Header.Get("Client-Id") != "dummy-client" || r.Header.Get("Authorization") != "" {
			t.Fatal("boundary")
		}
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), fakeKey) || strings.Contains(string(b), "dummy-client") {
			t.Fatal("credential payload")
		}
		return response(r, 200, `{"result":{}}`), nil
	})
	if _, e := c.ProductsPage(context.Background(), request()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ProductDetailsBatch(context.Background(), DetailsRequest{[]int64{1, 2, 3}}); e != nil {
		t.Fatal(e)
	}
	ctx := WithMetadata(context.Background(), Metadata{Caller: "telegram:ozon_seller_stocks", Tool: "ozon_get_seller_stocks", Cache: "BYPASS", Page: 1})
	if _, e := c.SellerStocksPage(ctx, SellerStocksRequest{Limit: 100, SKUs: []string{"1", "2"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.AnalyticsStocksBatch(context.Background(), AnalyticsRequest{[]string{"1", "2"}}); e != nil {
		t.Fatal(e)
	}
	if calls != 4 {
		t.Fatal(calls)
	}
	trace, _ := json.Marshal(state.traces[2])
	t.Log("OZON MOCK REQUEST", string(trace))
}

func TestDetailsHundredProductsAreOneBatch(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var body DetailsRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.ProductIDs) != 100 {
			t.Fatal("batch body")
		}
		return response(r, 200, `{"items":[]}`), nil
	})
	ids := make([]int64, 100)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, e := c.ProductDetailsBatch(context.Background(), DetailsRequest{ids}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("N+1", calls)
	}
}
func TestAuthorizationBeforeRequestAndFailClosedStorage(t *testing.T) {
	c, s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network"); return nil, nil })
	c.guard = func(context.Context, Scope) error { return errors.New(fakeKey) }
	_, e := c.ProductsPage(context.Background(), request())
	if e.Error() != "PERMISSION_DENIED" {
		t.Fatal(e)
	}
	c.guard = func(context.Context, Scope) error { return nil }
	s.broken = true
	_, e = c.ProductsPage(context.Background(), request())
	if e.Error() != "STORAGE_FAILED" {
		t.Fatal(e)
	}
}
func TestRetryAuthPermissionRateAndServerFailures(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c, s := fixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				out := response(r, status, fakeKey)
				out.Header.Set("Retry-After", "3600")
				return out, nil
			})
			_, e := c.ProductsPage(context.Background(), request())
			if e == nil {
				t.Fatal("missing error")
			}
			want := 1
			if status == 503 {
				want = 2
			}
			if calls != want {
				t.Fatal(calls, want)
			}
			if status == 429 {
				if time.Until(s.until) < 59*time.Minute {
					t.Fatal("deadline clamped")
				}
				_, e = c.ProductsPage(context.Background(), request())
				if e.Error() != "RATE_LIMITED" || calls != 1 || s.traces[len(s.traces)-1].Result != "BLOCKED_LOCALLY" {
					t.Fatal("cooldown")
				}
			}
			b, _ := json.Marshal(s.traces)
			if strings.Contains(string(b), fakeKey) || strings.Contains(string(b), "Api-Key") || strings.Contains(string(b), "Authorization") {
				t.Fatal("trace leaked")
			}
		})
	}
}
func TestDedupIdenticalRequests(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	c, s := fixture(t, func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return response(r, 200, `{"result":{}}`), nil
	})
	done := make(chan error, 2)
	go func() { _, e := c.ProductsPage(context.Background(), request()); done <- e }()
	<-entered
	go func() { _, e := c.ProductsPage(context.Background(), request()); done <- e }()
	// Wait for the second call to enter singleflight before releasing the leader.
	time.Sleep(30 * time.Millisecond)
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate HTTP", calls.Load())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	joined := 0
	for _, tr := range s.traces {
		if tr.Dedup == "JOINED_EXISTING" {
			joined++
		}
	}
	if joined != 1 {
		t.Fatal("dedup trace", s.traces)
	}
}
func TestResponseBoundsRedirectsAndRedaction(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `bad`, strings.Repeat("x", maxResponse+1), `{"error":"` + fakeKey + `"}`, `{"error":"\u0073ynthetic-ozon-key-never-real"}`} {
		c, _ := fixture(t, func(r *http.Request) (*http.Response, error) { return response(r, 200, body), nil })
		_, e := c.ProductsPage(context.Background(), request())
		if e == nil || e.Error() != "INVALID_RESPONSE" {
			t.Fatal("unsafe response")
		}
	}
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		out := response(r, 302, "")
		out.Header.Set("Location", "https://untrusted.invalid/")
		return out, nil
	})
	if _, e := c.ProductsPage(context.Background(), request()); e == nil || calls != 1 {
		t.Fatal("redirect followed")
	}
}
func TestInputAndRetryDate(t *testing.T) {
	c, _ := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid input reached HTTP"); return nil, nil })
	for _, r := range []AnalyticsRequest{{nil}, {[]string{"0"}}, {[]string{"1", "1"}}, {[]string{"../bad"}}} {
		if _, e := c.AnalyticsStocksBatch(context.Background(), r); e == nil {
			t.Fatal("input")
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	h := http.Header{}
	h.Set("Retry-After", now.Add(time.Hour).Format(http.TimeFormat))
	if !retryTime(h, now).Equal(now.Add(time.Hour)) {
		t.Fatal("HTTP-date")
	}
}
