package wildberries

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type traceCapture struct {
	memoryCooldown
	persisted [][]byte
}

func (s *traceCapture) AppendTrace(r RequestTrace) error {
	raw, e := json.Marshal(r)
	s.persisted = append(s.persisted, raw)
	return e
}
func (s *traceCapture) SaveObservation(r RateObservation) error {
	raw, e := json.Marshal(r)
	s.persisted = append(s.persisted, raw)
	return e
}

func TestTraceSecretsAndSharedGroupBlock(t *testing.T) {
	const credentials = "fixture-password-DO-NOT-LOG"
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	calls := 0
	store := &traceCapture{memoryCooldown: memoryCooldown{}}
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		r := response(429, `{"Authorization":"Bearer `+fakeSecret+`","password":"`+credentials+`"}`)
		r.Header.Set("Authorization", "Bearer "+fakeSecret)
		r.Header.Set("X-Ratelimit-Limit", credentials)
		r.Header.Set("X-Ratelimit-Retry", "120")
		return r, nil
	})
	c.clock = func() time.Time { return now }
	c.SetCooldownStore(store)
	first := WithTrace(context.Background(), CallMetadata{WorkshopID: 6, Caller: "wb_daily_sync", Tool: "wb_get_new_orders", ExpectedSeller: credentials})
	if _, e := c.NewOrders(first); !errors.Is(e, RateLimited) {
		t.Fatal(e)
	}
	now = now.Add(10 * time.Second)
	second := WithTrace(context.Background(), CallMetadata{WorkshopID: 6, Caller: "telegram_wb_sync", Tool: "wb_get_order_statuses"})
	if _, e := c.OrderStatuses(second, []int64{1}); !errors.Is(e, RateLimited) {
		t.Fatal(e)
	}
	h := c.History()
	if calls != 1 || len(h) != 2 {
		t.Fatal("local block sent HTTP", calls, len(h))
	}
	if h[0].Result != "WB_429" || h[1].Result != "BLOCKED_LOCALLY" || h[1].RequestSent || h[1].HTTPStatus != 0 || h[1].Attempt != 0 {
		t.Fatal(h)
	}
	if h[0].RateKey != h[1].RateKey || h[0].Endpoint == h[1].Endpoint || h[1].Caller != "telegram_wb_sync" || h[1].RetryAt == nil || !h[1].RetryAt.Equal(*h[0].RetryAt) {
		t.Fatal("shared group or initiator lost", h)
	}
	raw, _ := json.Marshal(h)
	all := append(store.persisted, raw)
	for _, data := range all {
		for _, secret := range []string{fakeSecret, credentials, "Authorization", "Bearer ", "password", "expected_seller"} {
			if strings.Contains(string(data), secret) {
				t.Fatal("sensitive value in trace/observation")
			}
		}
	}
}

func TestTransportErrorSecretNeverReachesPersistedTrace(t *testing.T) {
	store := &traceCapture{memoryCooldown: memoryCooldown{}}
	c := client(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Authorization: Bearer " + fakeSecret)
	})
	c.SetCooldownStore(store)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, e := c.NewOrders(context.Background())
	if !errors.Is(e, Unavailable) {
		t.Fatal(e)
	}
	if len(c.History()) != 3 {
		t.Fatal("unexpected retry count")
	}
	for _, data := range store.persisted {
		if strings.Contains(string(data), fakeSecret) || strings.Contains(string(data), "Authorization") {
			t.Fatal("transport error leaked")
		}
	}
	for _, r := range c.History() {
		if r.Result != "ERROR" || !r.RequestSent || r.HTTPStatus != 0 {
			t.Fatal(r)
		}
	}
}
