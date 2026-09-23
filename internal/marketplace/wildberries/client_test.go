package wildberries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const fakeSecret = "synthetic-WB-secret-NOT-A-CREDENTIAL"

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func client(t *testing.T, f roundTrip) *Client {
	t.Helper()
	c := New(fakeSecret)
	c.intervals = map[string]time.Duration{}
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.Header.Get("Authorization") != fakeSecret {
			t.Fatal("unsafe auth destination")
		}
		if _, ok := endpoint(r.Method, r.URL.Host, r.URL.Path); !ok {
			t.Fatal("unexpected endpoint")
		}
		return f(r)
	})
	return c
}
func TestHTTPFailuresAndBoundedRetries(t *testing.T) {
	for _, tc := range []struct {
		status, attempts int
		want             Error
	}{{401, 1, Unauthorized}, {403, 1, Forbidden}, {429, 1, RateLimited}, {500, 3, Unavailable}, {503, 3, Unavailable}, {400, 1, InvalidResponse}, {409, 1, InvalidResponse}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			n := 0
			c := client(t, func(*http.Request) (*http.Response, error) {
				n++
				r := response(tc.status, `{"error":"`+fakeSecret+`"}`)
				r.Header.Set("Retry-After", "0")
				return r, nil
			})
			_, err := c.Seller(context.Background())
			if !errors.Is(err, tc.want) || n != tc.attempts || strings.Contains(fmt.Sprint(err), fakeSecret) {
				t.Fatalf("err=%v attempts=%d", err, n)
			}
		})
	}
}
func TestTransportFailuresNeverExposeRequestOrToken(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("Authorization=" + fakeSecret) })
	_, err := c.Seller(context.Background())
	if err != Unavailable {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", c, c, c), fakeSecret) {
		t.Fatal("formatted client leaked token")
	}
	for _, body := range []string{`{"sid":"x","name":"` + fakeSecret + `"}`, `{"sid":"x","name":"\u0073ynthetic-WB-secret-NOT-A-CREDENTIAL"}`, `null`, `{invalid`} {
		c = client(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		_, err = c.Seller(context.Background())
		if err != InvalidResponse {
			t.Fatalf("accepted invalid body: %v", err)
		}
	}
}
func TestHostsRedirectsTLSAndGuards(t *testing.T) {
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		r := response(302, "")
		r.Header.Set("Location", "https://evil.test/steal")
		return r, nil
	})
	for _, v := range []struct{ method, host, path string }{{"GET", "evil.test", "/api/v1/seller-info"}, {"GET", "common-api.wildberries.ru:443", "/api/v1/seller-info"}, {"GET", "common-api.wildberries.ru@evil.test", "/api/v1/seller-info"}, {"PUT", "marketplace-api.wildberries.ru", "/api/v3/stocks/1"}, {"POST", "marketplace-api.wildberries.ru", "/api/v3/stocks/../orders"}} {
		if err := c.request(context.Background(), v.method, v.host, v.path, nil, &Seller{}); err != InvalidInput {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("rejected host was contacted")
	}
	if _, err := c.Seller(context.Background()); err != RedirectDenied || calls != 1 {
		t.Fatal(err, calls)
	}
	if New(fakeSecret).http.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification disabled")
	}
	ctx := WithGuard(context.Background(), func() error { return Forbidden })
	if _, err := c.Seller(ctx); err != Forbidden || calls != 1 {
		t.Fatal(err, calls)
	}
}
func TestRetryAfterAndRateWaitRespectCancellation(t *testing.T) {
	calls := 0
	c := client(t, func(*http.Request) (*http.Response, error) {
		calls++
		r := response(429, "")
		r.Header.Set("Retry-After", "120")
		return r, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Seller(ctx); !errors.Is(err, RateLimited) || calls != 1 {
		t.Fatal(err, calls)
	}
	if time.Until(c.next["common"]) < 119*time.Second {
		t.Fatal("Retry-After shortened")
	}
	if retryDelay(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat), 0) < 59*time.Second {
		t.Fatal("HTTP date not honored")
	}
}
func TestResponseLimitAndNoTokenConfiguration(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { return response(200, strings.Repeat("x", maxBody+1)), nil })
	if _, err := c.Seller(context.Background()); err != ResponseTooLarge {
		t.Fatal(err)
	}
	for _, token := range []string{"", "bad\r\nheader"} {
		c = New(token)
		if _, err := c.Seller(context.Background()); err != NotConfigured {
			t.Fatal(err)
		}
	}
}
func pageBody(count int, nm int64, at string) string {
	cards := make([]Card, count)
	for i := range cards {
		cards[i] = Card{ID: int64(i + 1), Sizes: []Size{{ID: int64(i + 1000), Barcodes: []string{"barcode"}}}}
	}
	raw, _ := json.Marshal(map[string]any{"cards": cards, "cursor": cursor{Total: count, NmID: nm, UpdatedAt: at}})
	return string(raw)
}
func TestCatalogPaginationAndLoopGuard(t *testing.T) {
	calls := 0
	c := client(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var req struct {
			Settings struct {
				Cursor cursor `json:"cursor"`
			} `json:"settings"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if calls == 1 {
			if req.Settings.Cursor.Limit != 100 {
				t.Fatal("limit")
			}
			return response(200, pageBody(100, 100, "2026-01-01T00:00:00Z")), nil
		}
		if req.Settings.Cursor.NmID != 100 {
			t.Fatal("cursor lost")
		}
		return response(200, pageBody(0, 100, "2026-01-01T00:00:00Z")), nil
	})
	cards, err := c.Catalog(context.Background())
	if err != nil || len(cards) != 100 || calls != 2 {
		t.Fatal(len(cards), err, calls)
	}
	calls = 0
	c = client(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, pageBody(100, 100, "same")), nil
	})
	_, err = c.Catalog(context.Background())
	if err != InvalidResponse || calls != 2 {
		t.Fatal(err, calls)
	}
}
func TestStockContractsAndIncompleteResponse(t *testing.T) {
	c := client(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v3/warehouses":
			return response(200, `[{"id":1,"name":"Seller"}]`), nil
		case "/api/v3/stocks/1":
			var body map[string][]int64
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body["chrtIds"]) != 1 || body["chrtIds"][0] != 3 {
				t.Fatal("expected chrtIds")
			}
			return response(200, `{"stocks":[{"chrtId":3,"amount":7}]}`), nil
		default:
			return response(200, `{"data":{"items":[{"nmId":2,"chrtId":3,"warehouseId":4,"warehouseName":"WB","quantity":8}]}}`), nil
		}
	})
	stocks, err := c.SellerStocks(context.Background(), []Card{{ID: 2, Sizes: []Size{{ID: 3}}}})
	if err != nil || len(stocks) != 1 || stocks[0].Quantity != 7 {
		t.Fatal(stocks, err)
	}
	stocks, err = c.WBStocks(context.Background())
	if err != nil || len(stocks) != 1 || stocks[0].WarehouseID != 4 {
		t.Fatal(stocks, err)
	}
	c = client(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return response(200, `[{"id":1}]`), nil
		}
		return response(200, `{"stocks":[]}`), nil
	})
	if _, err = c.SellerStocks(context.Background(), []Card{{ID: 2, Sizes: []Size{{ID: 3}}}}); err != InvalidResponse {
		t.Fatal("missing stock accepted", err)
	}
}
func TestOrdersValidationAndStatuses(t *testing.T) {
	calls := 0
	c := client(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == "GET" {
			return response(200, `{"orders":[{"id":1,"nmId":2,"chrtId":3,"warehouseId":4,"createdAt":"2026-01-01T00:00:00Z"}]}`), nil
		}
		return response(200, `{"orders":[{"id":1,"supplierStatus":"new","wbStatus":"waiting"}]}`), nil
	})
	if _, err := c.OrderStatuses(context.Background(), []int64{0}); err != InvalidInput || calls != 0 {
		t.Fatal(err)
	}
	if _, err := c.OrderStatuses(context.Background(), make([]int64, 1001)); err != InvalidInput {
		t.Fatal(err)
	}
	if o, err := c.NewOrders(context.Background()); err != nil || len(o) != 1 {
		t.Fatal(o, err)
	}
	if o, err := c.OrderStatuses(context.Background(), []int64{1}); err != nil || len(o) != 1 {
		t.Fatal(o, err)
	}
	if _, err := c.OrderStatuses(context.Background(), []int64{2}); err != InvalidResponse {
		t.Fatal("unexpected order accepted")
	}
}

func TestWBStockOffsetAndLoopGuard(t *testing.T) {
	calls := 0
	c := client(t, func(r *http.Request) (*http.Response, error) {
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		if body["offset"] != calls*1000 || body["limit"] != 1000 {
			t.Fatal("offset contract", body)
		}
		calls++
		items := []Stock{}
		if calls == 1 {
			for i := 0; i < 1000; i++ {
				items = append(items, Stock{NmID: 1, ChrtID: int64(i + 1), WarehouseID: 2, Quantity: 3})
			}
		}
		raw, _ := json.Marshal(map[string]any{"data": map[string]any{"items": items}})
		return response(200, string(raw)), nil
	})
	out, err := c.WBStocks(context.Background())
	if err != nil || len(out) != 1000 || calls != 2 {
		t.Fatal(err, len(out), calls)
	}
	calls = 0
	c = client(t, func(*http.Request) (*http.Response, error) {
		calls++
		items := make([]Stock, 1000)
		for i := range items {
			items[i] = Stock{NmID: 1, ChrtID: int64(i + 1), WarehouseID: 2, Quantity: 3}
		}
		raw, _ := json.Marshal(map[string]any{"data": map[string]any{"items": items}})
		return response(200, string(raw)), nil
	})
	if _, err = c.WBStocks(context.Background()); err != InvalidResponse || calls != 2 {
		t.Fatal(err, calls)
	}
}

func TestRateLimiterRechecksGuardAfterWait(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { t.Fatal("revoked request sent"); return nil, nil })
	c.next["common"] = time.Now().Add(10 * time.Millisecond)
	checks := 0
	ctx := WithGuard(context.Background(), func() error { checks++; return Forbidden })
	if _, err := c.Seller(ctx); err != Forbidden || checks == 0 {
		t.Fatal(err, checks)
	}
}

func TestRequestTimeoutIsSafe(t *testing.T) {
	c := client(t, func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	c.http.Timeout = 10 * time.Millisecond
	if _, err := c.Seller(context.Background()); err != Timeout {
		t.Fatal(err)
	}
}
