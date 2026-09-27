package wildberries

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSellerCooldownDoesNotGateIndependentReads(t *testing.T) {
	calls := map[string]int{}
	c := client(t, func(r *http.Request) (*http.Response, error) {
		calls[r.URL.Path]++
		switch {
		case strings.Contains(r.URL.Path, "seller-info"):
			t.Fatal("unexpected identity HTTP")
		case strings.Contains(r.URL.Path, "warehouses") && r.Method == "GET":
			return response(200, `[{"id":1}]`), nil
		case strings.Contains(r.URL.Path, "/api/v3/stocks/"):
			return response(200, `{"stocks":[{"chrtId":3,"amount":7}]}`), nil
		case strings.Contains(r.URL.Path, "stocks-report"):
			return response(200, `{"data":{"items":[{"nmId":2,"chrtId":3,"warehouseId":4,"quantity":8}]}}`), nil
		default:
			return response(200, `{"data":{"listGoods":[]}}`), nil
		}
		return nil, nil
	})
	c.sellerCache = SellerCache{Seller: Seller{ID: "known"}, FetchedAt: time.Now().Add(-72 * time.Hour)}
	c.SetCooldownStore(memoryCooldown{"common": {Operation: "common", RetryAt: time.Now().Add(time.Hour), Source: "wb_retry"}})
	ctx := context.Background()
	if e := c.EnsureSeller(ctx, "known"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Prices(ctx); e != nil {
		t.Fatal(e)
	}
	b := c.SellerStockBatch(ctx, []Card{{ID: 2, Sizes: []Size{{ID: 3}}}})
	if b.Info.Status != "SUCCESS" || len(b.Rows) != 1 {
		t.Fatal(b)
	}
	if _, e := c.WBStocks(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Seller(RefreshIdentity(ctx)); !errors.Is(e, RateLimited) {
		t.Fatal(e)
	}
	if calls["/api/v1/seller-info"] != 0 || len(calls) != 4 {
		t.Fatal(calls)
	}
	if e := c.EnsureSeller(ctx, "known"); e != nil {
		t.Fatal("refresh failure discarded cached identity", e)
	}
}
