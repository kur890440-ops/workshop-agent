package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"workshop-agent/internal/workshops"
)

func TestSellerCachedCatalogRefreshPreservesLastGood(t *testing.T) {
	ws := workshops.NewService(filepath.Join(t.TempDir(), "seller.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(920002, "", "owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "scope")
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(ws.DB(), "dummy-client", fakeKey)
	defer s.Close()
	a := Access{u, w, 2}
	if e = s.Attach(a); e != nil {
		t.Fatal(e)
	}
	for n := 1; n <= 25; n++ {
		if _, e = ws.DB().Exec(`INSERT INTO marketplace_catalog(connection_id,workshop_id,provider,product_id,offer_id,name,updated_at,last_seen_at) VALUES(2,?,'OZON',?,?,'Product A','','')`, w, n, fmt.Sprint(n)); e != nil {
			t.Fatal(e)
		}
	}
	for n := 1; n <= 27; n++ {
		if _, e = ws.DB().Exec(`INSERT INTO marketplace_catalog_skus(connection_id,workshop_id,provider,product_id,sku) VALUES(2,?,'OZON',?,?)`, w, (n-1)%25+1, 9000+n); e != nil {
			t.Fatal(e)
		}
	}
	cn, _ := s.Status(a)
	c, e := s.bound(a, cn)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	c.http.Transport = transport(func(req *http.Request) (*http.Response, error) {
		calls++
		var body SellerStocksRequest
		if req.URL.Path != seller.path || json.NewDecoder(req.Body).Decode(&body) != nil || len(body.SKUs) != 27 {
			t.Fatal("cached filter missing")
		}
		if calls == 2 {
			return response(req, 400, `{"code":3,"message":"fixture bad request","request_id":"fixture-id"}`), nil
		}
		return response(req, 200, `{"has_next":false,"products":[{"sku":9001,"warehouse_id":8,"warehouse_name":"Warehouse A","free_stock":8},{"sku":9001,"warehouse_id":9,"warehouse_name":"Warehouse B","free_stock":4}]}`), nil
	})
	r, e := s.Execute(context.Background(), a, SellerSource, Input{Refresh: true})
	if e != nil || r.Status != Success || calls != 1 || len(r.Stocks) != 2 || r.Metrics.ProductsConsidered != 25 || r.Metrics.IdentifiersValid != 27 || r.Metrics.Batches != 1 || r.Metrics.Saved != 2 {
		t.Fatal(r, e, calls)
	}
	t.Logf("mock refresh: HTTP200 catalog=CACHE HIT products=25 identifiers=27 batches=1 requests=%d rows=%d warehouses=2 saved=%d", r.Metrics.HTTPRequests, len(r.Stocks), r.Metrics.Saved)
	t.Log(Format(r))
	r, e = s.Execute(context.Background(), a, SellerSource, Input{Refresh: true})
	if e != nil || r.Status != "BAD_REQUEST" || calls != 2 || len(r.Stocks) != 2 {
		t.Fatal(r, e, calls)
	}
	var sum int
	if e = ws.DB().QueryRow(`SELECT SUM(quantity) FROM marketplace_stock_current WHERE connection_id=2`).Scan(&sum); e != nil || sum != 12 {
		t.Fatal("last good changed", sum, e)
	}
}

func TestSellerFilterBatchPaginationAndNoNPlusOne(t *testing.T) {
	calls := 0
	c, state := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != seller.path {
			t.Fatal("unexpected catalog or v4 call")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("body")
		}
		ids := body["sku"].([]any)
		batch := 1
		if calls == 3 {
			batch = 2
		}
		want := []any{}
		from, to := 1, 100
		if batch == 2 {
			from, to = 101, 101
		}
		for n := from; n <= to; n++ {
			want = append(want, fmt.Sprint(9000+n))
		}
		expected := map[string]any{"sku": want, "limit": float64(100)}
		if calls == 2 {
			expected["cursor"] = "next"
		}
		if !reflect.DeepEqual(body, expected) {
			t.Fatal("filter changed across pages", body)
		}
		if calls == 1 {
			return response(r, 200, `{"has_next":true,"cursor":"next","products":[{"sku":"9001","warehouse_id":"8","warehouse_name":"Warehouse A","free_stock":8}]}`), nil
		}
		if calls == 2 {
			return response(r, 200, `{"has_next":false,"products":[{"sku":"9001","warehouse_id":"9","warehouse_name":"Warehouse B","free_stock":4}]}`), nil
		}
		return response(r, 200, fmt.Sprintf(`{"has_next":false,"products":[{"sku":"%s","warehouse_id":8,"free_stock":0}]}`, ids[0])), nil
	})
	ids := []int64{0, -1}
	for n := int64(9001); n <= 9101; n++ {
		ids = append(ids, n)
	}
	ids = append(ids, 9001)
	r := c.Fetch(context.Background(), SellerSource, ids)
	if r.Status != Success || calls != 3 || r.Metrics.Batches != 2 || r.Metrics.IdentifiersValid != 101 || r.Metrics.IdentifiersInvalid != 2 || r.Metrics.IdentifiersDuplicate != 1 || len(r.Stocks) != 3 {
		t.Fatal(r, calls)
	}
	if state.traces[0].IdentifierCount != 100 || state.traces[1].Batch != 1 || state.traces[2].Batch != 2 {
		t.Fatal("batch trace")
	}
	r.Total = 3
	for i := range r.Stocks {
		r.Stocks[i].Name = "Product"
	}
	if !strings.Contains(Format(r), "12") || !strings.Contains(Format(r), "Warehouse B") {
		t.Fatal("warehouse display")
	}
}

func TestSellerEmptyAndRepeatedCursor(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 200, `{"has_next":true,"cursor":"repeat","products":[{"sku":1,"warehouse_id":8,"free_stock":2}]}`), nil
	})
	r := c.Fetch(context.Background(), SellerSource, []int64{0, -3})
	if calls != 0 || r.Status != "UNAVAILABLE" {
		t.Fatal("empty request")
	}
	if _, e := c.SellerStocksPage(context.Background(), SellerStocksRequest{Limit: 100}); e == nil || calls != 0 {
		t.Fatal("unfiltered low-level request")
	}
	r = c.Fetch(context.Background(), SellerSource, []int64{1})
	if calls != 2 || r.Status != Partial {
		t.Fatal("pagination loop", calls, r)
	}
}

func TestSeller25ProductsOneRequest(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var in SellerStocksRequest
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.SKUs) != 25 {
			t.Fatal(in)
		}
		return response(r, 200, `{"products":[],"has_next":false}`), nil
	})
	ids := []int64{}
	for n := int64(1); n <= 25; n++ {
		ids = append(ids, 9000+n)
	}
	r := c.Fetch(context.Background(), SellerSource, ids)
	if calls != 1 || r.Metrics.HTTPRequests != 1 || r.Metrics.Batches != 1 || r.Metrics.IdentifiersValid != 25 {
		t.Fatal(r)
	}
}

func TestAllNon2xxDiagnosticsPreserved(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			c, s := fixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				v := response(r, status, `{"code":3,"message":"invalid filter","details":[{"field":"sku"}],"request_id":"safe-id","trace_id":"safe-trace"}`)
				v.Header.Set("Retry-After", "60")
				return v, nil
			})
			_, e := c.SellerStocksPage(context.Background(), SellerStocksRequest{Limit: 100, SKUs: []string{"9001"}})
			err, ok := e.(*Error)
			if !ok || err.Diagnostic == nil {
				t.Fatal(e)
			}
			d := err.Diagnostic
			if d.HTTPStatus != status || d.Operation != "seller_stocks" || d.Endpoint != seller.path || string(d.RequestID) != `"safe-id"` || string(d.TraceID) != `"safe-trace"` {
				t.Fatal(d)
			}
			want := 1
			if status == 500 {
				want = 2
			}
			if calls != want || len(s.traces) != want {
				t.Fatal("retry policy")
			}
		})
	}
}
