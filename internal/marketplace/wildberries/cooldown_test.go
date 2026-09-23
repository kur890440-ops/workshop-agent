package wildberries

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRateHeaderPolicy(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, retry, wb, reset string
		want                   time.Duration
		source                 string
	}{
		{"conflict", "5", "120", "900", 120 * time.Second, "wb_retry"},
		{"date", now.Add(time.Hour).Format(http.TimeFormat), "60", "", time.Hour, "wb_retry"},
		{"reset", "", "", "90", 90 * time.Second, "wb_reset"},
		{"zero", "0", "0", "", time.Second, "wb_retry"},
		{"bad", "-1", "bad", "-3", time.Second, "local_backoff"},
		{"overflow", "99999999999999999999999", "9223372036854775807", "", time.Second, "local_backoff"},
		{"long", "86400", "", "", 24 * time.Hour, "wb_retry"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", tt.retry)
			h.Set("X-Ratelimit-Retry", tt.wb)
			h.Set("X-Ratelimit-Reset", tt.reset)
			d, source := rateDelay(h, now, 0)
			if d != tt.want || source != tt.source {
				t.Fatal(d, source)
			}
		})
	}
}

type memoryCooldown map[string]RateLimitError

func (s memoryCooldown) LoadCooldown(g string) (RateLimitError, error) { return s[g], nil }
func (s memoryCooldown) SaveCooldown(v RateLimitError) error {
	if !s[v.Operation].RetryAt.After(v.RetryAt) {
		s[v.Operation] = v
	}
	return nil
}
func TestServerCooldownSurvivesNewClientAndNoEarlyRequest(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := memoryCooldown{}
	calls := 0
	makeClient := func() *Client {
		c := client(t, func(*http.Request) (*http.Response, error) {
			calls++
			r := response(429, "")
			r.Header.Set("X-Ratelimit-Retry", "86400")
			return r, nil
		})
		c.clock = func() time.Time { return now }
		c.SetCooldownStore(store)
		return c
	}
	c := makeClient()
	_, err := c.Seller(context.Background())
	var rate *RateLimitError
	if !errors.As(err, &rate) || !errors.Is(err, RateLimited) || rate.RetryAt != now.Add(24*time.Hour) {
		t.Fatal(err)
	}
	c = makeClient()
	for i := 0; i < 5; i++ {
		_, err = c.Seller(context.Background())
		if !errors.Is(err, RateLimited) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("early HTTP", calls)
	}
	now = now.Add(24 * time.Hour)
	_, _ = c.Seller(context.Background())
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestPacingFakeClockAndCancellation(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"sid":"seller","name":"name"}`), nil
	})
	c.clock = func() time.Time { return now }
	c.intervals["common"] = 20 * time.Second
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if d != 20*time.Second {
			t.Fatal(d)
		}
		now = now.Add(d)
		return nil
	}
	_, _ = c.Seller(context.Background())
	_, err := c.Seller(context.Background())
	if err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	c.sleep = func(ctx context.Context, d time.Duration) error { return Cancelled }
	_, err = c.Seller(context.Background())
	if err != Cancelled || calls != 2 {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Seller(ctx)
	if err != Cancelled {
		t.Fatal(err)
	}
}
func TestUnknownProfileConservativeAndLongLocalWait(t *testing.T) {
	if New("fixture").intervals["common"] != 24*time.Hour || NewWithProfile("fixture", "personal").intervals["analytics"] != 20*time.Second {
		t.Fatal("profile policy")
	}
	c := client(t, func(*http.Request) (*http.Response, error) { t.Fatal("request during wait"); return nil, nil })
	c.next["common"] = time.Now().Add(time.Hour)
	_, err := c.Seller(context.Background())
	var r *RateLimitError
	if !errors.As(err, &r) || r.Source != "local_interval" {
		t.Fatal(err)
	}
}
