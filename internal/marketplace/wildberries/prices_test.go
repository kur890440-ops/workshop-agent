package wildberries

import (
	"context"
	"net/http"
	"testing"
)

func TestPricesPaginationAndMoney(t *testing.T) {
	calls := 0
	c := client(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Host != "discounts-prices-api.wildberries.ru" || r.URL.Query().Get("limit") != "1000" {
			t.Fatal(r.URL)
		}
		if calls == 1 {
			return response(200, `{"data":{"listGoods":[{"nmID":1,"currencyIsoCode4217":"RUB","sizes":[{"sizeID":2,"price":123.45,"discountedPrice":100.01}]}]}}`), nil
		}
		if r.URL.Query().Get("offset") != "1000" {
			t.Fatal("bad pagination")
		}
		return response(200, `{"data":{"listGoods":[]}}`), nil
	})
	p, e := c.Prices(context.Background())
	if e != nil || calls != 2 || len(p) != 1 || p[0].PriceCents != 12345 || *p[0].DiscountedCents != 10001 {
		t.Fatal(p, e, calls)
	}
}
func TestPricesRejectMalformedAndRepeatedPages(t *testing.T) {
	for _, body := range []string{`{}`, `{"error":true,"data":{"listGoods":[]}}`, `{"data":{"listGoods":[{"nmID":1,"currencyIsoCode4217":"RUB","sizes":[{"sizeID":2,"price":1.001}]}]}}`, `{"data":{"listGoods":[{"nmID":1,"currencyIsoCode4217":"RUB","sizes":[{"sizeID":2,"price":10}]}]}}`} {
		calls := 0
		c := client(t, func(*http.Request) (*http.Response, error) { calls++; return response(200, body), nil })
		if _, e := c.Prices(context.Background()); e != InvalidResponse || calls > 2 {
			t.Fatal(e, calls)
		}
	}
}
