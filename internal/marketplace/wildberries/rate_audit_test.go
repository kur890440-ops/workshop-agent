package wildberries

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRate120SharedCallersAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			r := response(429, "")
			r.Header.Set("x-ratelimit-retry", "120")
			r.Header.Set("X-Ratelimit-Reset", "900")
			return r, nil
		}
		return response(200, `{"sid":"a","name":"A"}`), nil
	})
	c.clock = func() time.Time { return now }
	_, err := c.Seller(WithTrace(context.Background(), CallMetadata{Caller: "telegram_wb_sync"}))
	var rate *RateLimitError
	if !errors.As(err, &rate) || rate.BlockedLocally || !rate.RetryAt.Equal(now.Add(120*time.Second)) {
		t.Fatal(err, rate)
	}
	now = now.Add(10 * time.Second)
	_, err = c.Seller(WithTrace(context.Background(), CallMetadata{Caller: "wb_daily_sync"}))
	if !errors.As(err, &rate) || !rate.BlockedLocally || calls != 1 {
		t.Fatal(err, calls)
	}
	now = now.Add(111 * time.Second)
	if _, err = c.Seller(context.Background()); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	h := c.History()
	if h[0].Result != "WB_429" || h[1].Result != "BLOCKED_LOCALLY" || h[1].HTTPStatus != 0 {
		t.Fatal(h)
	}
	raw, _ := json.Marshal(h)
	if strings.Contains(string(raw), fakeSecret) {
		t.Fatal("trace leaked token")
	}
}
func TestRemainingZeroOnSuccessfulResponse(t *testing.T) {
	now := time.Now()
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		r := response(200, `[]`)
		r.Header.Set("X-Ratelimit-Remaining", "0")
		r.Header.Set("X-Ratelimit-Reset", "120")
		r.Header.Set("X-Ratelimit-Limit", "10")
		return r, nil
	})
	c.clock = func() time.Time { return now }
	var out any
	if e := c.request(context.Background(), "GET", "marketplace-api.wildberries.ru", "/api/v3/orders/new", nil, &out); e != nil {
		t.Fatal(e)
	}
	e := c.request(context.Background(), "GET", "marketplace-api.wildberries.ru", "/api/v3/orders/new", nil, &out)
	if !errors.Is(e, RateLimited) || calls != 1 {
		t.Fatal(e, calls)
	}
	if *c.History()[0].Rate.Limit != 10 {
		t.Fatal("headers lost")
	}
}
func TestConcurrentSellerSingleFlightAndGroup429(t *testing.T) {
	for _, status := range []int{200, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			c := client(t, func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				time.Sleep(15 * time.Millisecond)
				r := response(status, `{"sid":"a","name":"A"}`)
				r.Header.Set("X-Ratelimit-Retry", "120")
				return r, nil
			})
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, e := c.Seller(context.Background())
					if status == 200 && e != nil {
						t.Error(e)
					}
					if status == 429 && !errors.Is(e, RateLimited) {
						t.Error(e)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 1 {
				t.Fatal("duplicate identity HTTP", calls.Load())
			}
		})
	}
}
func TestGroupGateWaitsFor429BeforeNextHTTP(t *testing.T) {
	var calls atomic.Int32
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		time.Sleep(15 * time.Millisecond)
		r := response(429, "")
		r.Header.Set("X-Ratelimit-Retry", "120")
		return r, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out any
			e := c.request(context.Background(), "GET", "marketplace-api.wildberries.ru", "/api/v3/orders/new", nil, &out)
			if !errors.Is(e, RateLimited) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("group race", calls.Load())
	}
}
func TestTokenMetadataIsLocalOnly(t *testing.T) {
	for _, tc := range []struct{ payload, want string }{{`{"acc":1,"t":false}`, "BASE"}, {`{"acc":2,"t":true}`, "TEST"}, {`{"acc":3,"for":"self","t":false}`, "PERSONAL"}, {`{"acc":4,"for":"asid:fixture","t":false}`, "SERVICE"}, {`{"acc":1}`, "UNKNOWN"}} {
		c := New("e30." + base64.RawURLEncoding.EncodeToString([]byte(tc.payload)) + ".fixture")
		if c.TokenType() != tc.want {
			t.Fatal(tc.want, c.TokenType())
		}
	}
}

func TestDiagnosticBudgetBoundsPagesAndRetries(t *testing.T) {
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"data":{"listGoods":[{"nmID":1,"currencyIsoCode4217":"RUB","sizes":[{"sizeID":2,"price":10}]}]}}`), nil
	})
	c.LimitToOneRequest()
	if _, e := c.Prices(context.Background()); e != PageLimit || calls != 1 {
		t.Fatal(e, calls)
	}
	if c.History()[1].Result != "BLOCKED_LOCALLY" || c.History()[1].Detail != "DIAGNOSTIC_BUDGET" {
		t.Fatal(c.History())
	}
}

func TestRemainingZeroWithoutResetUsesDocumentedPeriod(t *testing.T) {
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	c := client(t, func(*http.Request) (*http.Response, error) {
		r := response(200, `{"data":{"listGoods":[]}}`)
		r.Header.Set("X-Ratelimit-Remaining", "0")
		r.Header.Set("X-Ratelimit-Limit", "1")
		return r, nil
	})
	c.clock = func() time.Time { return now }
	if _, e := c.Prices(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := c.cooldown("prices")
	if e != nil || state.Source != "local_interval" || !state.RetryAt.Equal(now.Add(6*time.Second)) {
		t.Fatal(e, state)
	}
}
func TestBoundedNetworkRetryAndLong5xx(t *testing.T) {
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New(fakeSecret) })
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, e := c.NewOrders(context.Background()); e != Unavailable || calls != 3 {
		t.Fatal(e, calls)
	}
	calls = 0
	c = client(t, func(*http.Request) (*http.Response, error) {
		calls++
		r := response(503, "")
		r.Header.Set("Retry-After", "3600")
		return r, nil
	})
	c.sleep = func(context.Context, time.Duration) error { t.Fatal("long sleep"); return nil }
	if _, e := c.NewOrders(context.Background()); !errors.Is(e, RateLimited) || calls != 1 {
		t.Fatal(e, calls)
	}
}
