package wildberries

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type timedBody struct {
	io.Reader
	finish func()
}

func TestTraceActualEndpointAndCaller(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { return response(200, `{}`), nil })
	ctx := WithTrace(context.Background(), CallMetadata{WorkshopID: 6, Caller: "wb_daily_sync", Tool: "wb_get_wb_stocks"})
	var out any
	if e := c.request(ctx, "POST", "marketplace-api.wildberries.ru", "/api/v3/stocks/12345", nil, &out); e != nil {
		t.Fatal(e)
	}
	r := c.History()[0]
	if r.Host != "marketplace-api.wildberries.ru" || r.Endpoint != "/api/v3/stocks/12345" || r.RateKey != "marketplace" || r.Caller != "background:WB_DAILY_SYNC" || r.Tool != "wb_get_wb_stocks" {
		t.Fatal(r)
	}
}

func (b timedBody) Close() error { b.finish(); return nil }

func TestTraceContractAndCompletedResponse(t *testing.T) {
	for _, tc := range []struct {
		status       int
		body, result string
	}{{200, `{"sid":"seller","name":"name"}`, "SUCCESS"}, {200, `invalid json`, "ERROR"}, {403, `{}`, "ERROR"}, {429, `{}`, "WB_429"}} {
		t.Run(tc.result+http.StatusText(tc.status), func(t *testing.T) {
			now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
			start := now
			c := client(t, func(*http.Request) (*http.Response, error) {
				r := response(tc.status, tc.body)
				r.Body = timedBody{strings.NewReader(tc.body), func() { now = now.Add(250 * time.Millisecond) }}
				r.Header.Set("X-Ratelimit-Limit", "10")
				r.Header.Set("X-Ratelimit-Remaining", "0")
				r.Header.Set("X-Ratelimit-Retry", "120")
				r.Header.Set("X-Ratelimit-Reset", "300")
				return r, nil
			})
			c.clock = func() time.Time { return now }
			_, _ = c.Seller(WithTrace(context.Background(), CallMetadata{WorkshopID: 6, Caller: "run_now", Tool: "wb_get_seller"}))
			history := c.History()
			if len(history) != 1 {
				t.Fatal(history)
			}
			r := history[0]
			if r.Result != tc.result || r.Operation != "seller_info" || r.Attempt != 1 || !r.RequestSent || r.WorkshopID != 6 || r.Caller != "background:WB_DAILY_SYNC:run_now" {
				t.Fatal(r)
			}
			if !r.RequestStartedAt.Equal(start) || !r.RequestFinishedAt.Equal(now) || r.DurationMS != 250 {
				t.Fatal("trace finished before response body", r)
			}
			raw, _ := json.Marshal(r)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			for _, key := range []string{"timestamp", "workshop_id", "caller", "operation", "mcp_tool", "wb_method", "http_method", "endpoint", "request_started_at", "request_finished_at", "duration_ms", "http_status", "X-Ratelimit-Limit", "X-Ratelimit-Remaining", "X-Ratelimit-Retry", "X-Ratelimit-Reset", "retry_not_before", "attempt_number", "result"} {
				if _, ok := fields[key]; !ok {
					t.Fatal("missing", key)
				}
			}
			if strings.Contains(string(raw), fakeSecret) {
				t.Fatal("secret leaked")
			}
			_, _ = c.Seller(RefreshIdentity(context.Background()))
			blocked := c.History()[1]
			if blocked.Result != "BLOCKED_LOCALLY" || blocked.RequestSent || blocked.Attempt != 0 || blocked.RetryAt == nil {
				t.Fatal(blocked)
			}
		})
	}
}
