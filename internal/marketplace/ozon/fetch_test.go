package ozon

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"workshop-agent/internal/workshops"
)

func TestCatalogLoopAndPerRecordValidation(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == details.path {
			return response(r, 200, `{"items":[{"id":1,"offer_id":"A","name":"First","sku":11}]}`), nil
		}
		return response(r, 200, `{"result":{"items":[{"product_id":1,"offer_id":"A"},{"product_id":0,"offer_id":"invalid"}],"total":20,"last_id":"loop"}}`), nil
	})
	out := c.Fetch(context.Background(), CatalogSource, nil)
	if out.Status != Partial || len(out.Products) != 1 || out.Metrics.HTTPRequests != 3 || out.Metrics.Pages != 2 || calls != 3 || len(out.Diagnostics) == 0 {
		t.Fatal(out, calls)
	}
}

func TestStockMissingQuantityAndEnvelopeNeverBecomeZero(t *testing.T) {
	for _, body := range []string{`{}`, `{"products":[]}`, `{"has_next":false,"products":[{"sku":1,"warehouse_id":8}]}`, `{"has_next":false,"products":[{"sku":1,"warehouse_id":8,"free_stock":-1}]}`} {
		c, _ := fixture(t, func(r *http.Request) (*http.Response, error) { return response(r, 200, body), nil })
		out := c.Fetch(context.Background(), SellerSource, []int64{1, 2, 3})
		if out.Status != string(InvalidResponse) || len(out.Stocks) != 0 || out.Metrics.Invalid == 0 {
			t.Fatal(body, out)
		}
	}
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 200, `{"has_next":false,"products":[{"sku":"1","warehouse_id":"8","free_stock":0}]}`), nil
	})
	out := c.Fetch(context.Background(), SellerSource, []int64{1, 2, 3})
	if out.Status != Success || len(out.Stocks) != 1 || out.Stocks[0].Quantity != 0 {
		t.Fatal(out)
	}
}

func TestFBOUnrequestedSKUAndBatchCounts(t *testing.T) {
	calls := 0
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 200, `{"items":[{"sku":9999,"warehouse_id":9,"available_stock_count":8}]}`), nil
	})
	ids := make([]int64, 201)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	out := c.Fetch(context.Background(), FBOSource, ids)
	if calls != 3 || out.Metrics.HTTPRequests != 3 || out.Status != string(InvalidResponse) || len(out.Stocks) != 0 {
		t.Fatal(out)
	}
}

func TestWholeRefreshDedupAndRevocationBeforeCommit(t *testing.T) {
	ws := workshops.NewService(filepath.Join(t.TempDir(), "scope.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(920001, "", "owner", "")
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
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_catalog(connection_id,workshop_id,provider,product_id,offer_id,name,updated_at,last_seen_at) VALUES(2,?,'OZON',7,'A','A','','')`, w); e != nil {
		t.Fatal(e)
	}
	if _, e = ws.DB().Exec(`INSERT INTO marketplace_catalog_skus(connection_id,workshop_id,provider,product_id,sku) VALUES(2,?,'OZON',7,11)`, w); e != nil {
		t.Fatal(e)
	}
	cn, _ := s.Status(a)
	client, e := s.bound(a, cn)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		entered <- struct{}{}
		<-release
		return response(r, 200, `{"has_next":false,"products":[{"sku":11,"warehouse_id":8,"free_stock":5}]}`), nil
	})
	results := make(chan Result, 2)
	errs := make(chan error, 2)
	run := func() {
		r, e := s.Execute(context.Background(), a, SellerSource, Input{Refresh: true})
		results <- r
		errs <- e
	}
	go run()
	<-entered
	go run()
	time.Sleep(40 * time.Millisecond)
	close(release)
	r1, r2 := <-results, <-results
	if e1, e2 := <-errs, <-errs; e1 != nil || e2 != nil {
		t.Fatal(e1, e2)
	}
	if calls != 1 || r1.Metrics.HTTPRequests+r2.Metrics.HTTPRequests != 1 || r1.Dedup == r2.Dedup {
		t.Fatal("not one sequence", calls, r1.Dedup, r2.Dedup)
	}
	var count int
	ws.DB().QueryRow(`SELECT COUNT(*) FROM marketplace_source_runs`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate runs", count)
	}
	client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if e := s.Disable(a); e != nil {
			t.Error(e)
		}
		return response(r, 200, `{"has_next":false,"products":[{"sku":11,"warehouse_id":8,"free_stock":99}]}`), nil
	})
	if _, e = s.Execute(context.Background(), a, SellerSource, Input{Refresh: true}); e == nil {
		t.Fatal("revoked scope saved")
	}
	ws.DB().QueryRow(`SELECT quantity FROM marketplace_stock_current WHERE sku=11`).Scan(&count)
	if count != 5 {
		t.Fatal("late save after disable", count)
	}
	if _, e = ws.DB().Exec(`UPDATE user_workshop_context SET active_workshop_id=NULL WHERE user_id=?`, u); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(context.Background(), a, SellerSource, Input{}); e == nil {
		t.Fatal("old active-workshop view accepted")
	}
}

func TestStrictPartialCounts(t *testing.T) {
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 200, fmt.Sprintf(`{"has_next":false,"products":[{"sku":1,"warehouse_id":1,"free_stock":8},{"sku":2,"warehouse_id":1,"free_stock":"bad"},{"sku":3,"warehouse_id":1,"free_stock":0}]}`)), nil
	})
	r := c.Fetch(context.Background(), SellerSource, []int64{1, 2, 3})
	if r.Metrics.Received != 3 || r.Metrics.Valid != 2 || r.Metrics.Invalid != 1 || r.Status != Partial {
		t.Fatal(r)
	}
}

func TestValidStockSurvivesUnknownPagination(t *testing.T) {
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 200, `{"products":[{"sku":1,"warehouse_id":8,"free_stock":9}]}`), nil
	})
	r := c.Fetch(context.Background(), SellerSource, []int64{1, 2, 3})
	if r.Status != Partial || len(r.Stocks) != 1 || r.Stocks[0].Quantity != 9 || r.Metrics.HTTPRequests != 1 {
		t.Fatal(r)
	}
}
