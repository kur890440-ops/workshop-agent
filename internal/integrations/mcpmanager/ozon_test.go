package mcpmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace/ozon"
	"workshop-agent/internal/workshops"
)

// All requests retain api-seller.ozon.ru in their URL; the test-only transport
// routes them to a local TLS server trusted by its explicit test certificate.
func mockOzonTransport(t *testing.T, h http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(h)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	old := http.DefaultTransport
	http.DefaultTransport = tr
	t.Cleanup(func() { http.DefaultTransport = old; tr.CloseIdleConnections(); server.Close() })
}

func TestOzonStageARealMCPPersistenceAndIsolation(t *testing.T) {
	ctx := context.Background()
	var httpCalls, productCalls, sellerMode atomic.Int64
	mockOzonTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" || r.Host != "api-seller.ozon.ru" || r.Header.Get("Client-Id") != "synthetic-client" || r.Header.Get("Api-Key") != "synthetic-ozon-key" {
			t.Error("HTTP credential/host boundary")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/v3/product/list":
			productCalls.Add(1)
			var in ozon.ProductsRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Limit != 100 || in.Filter.Visibility != "ALL" {
				t.Error("catalog request")
			}
			start, end, cursor := 1, 60, "next"
			if in.LastID == "next" {
				start, end, cursor = 61, 100, ""
			}
			rows := []map[string]any{}
			for i := start; i <= end; i++ {
				rows = append(rows, map[string]any{"product_id": i, "offer_id": fmt.Sprintf("OFFER-%d", i)})
			}
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"items": rows, "total": 100, "last_id": cursor}})
		case "/v3/product/info/list":
			productCalls.Add(1)
			var in ozon.DetailsRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.ProductIDs) != 100 {
				t.Error("details must be one batch100")
			}
			rows := []map[string]any{}
			for _, id := range in.ProductIDs {
				rows = append(rows, map[string]any{"id": id, "offer_id": fmt.Sprintf("OFFER-%d", id), "name": fmt.Sprintf("Товар %d", id), "sku": id + 2000, "updated_at": "2026-09-27T10:00:00Z"})
			}
			json.NewEncoder(w).Encode(map[string]any{"items": rows})
		case "/v2/product/info/stocks-by-warehouse/fbs":
			if sellerMode.Load() == 2 {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(429)
				return
			}
			if sellerMode.Load() == 1 {
				fmt.Fprint(w, `{"has_next":false,"products":[{"product_id":1,"offer_id":"OFFER-1","sku":2001,"warehouse_id":8,"free_stock":7},{"sku":2004,"warehouse_id":8}]}`)
				return
			}
			var in ozon.SellerStocksRequest
			json.NewDecoder(r.Body).Decode(&in)
			if in.Cursor == "" {
				fmt.Fprint(w, `{"has_next":true,"cursor":"s2","products":[{"product_id":1,"offer_id":"OFFER-1","sku":2001,"warehouse_id":8,"warehouse_name":"Склад продавца","free_stock":5},{"product_id":2,"sku":2002,"warehouse_id":8,"free_stock":0}]}`)
			} else {
				fmt.Fprint(w, `{"has_next":false,"products":[{"product_id":3,"sku":2003,"warehouse_id":8,"free_stock":2}]}`)
			}
		case "/v1/analytics/stocks":
			fmt.Fprint(w, `{"items":[{"sku":2001,"warehouse_id":9,"warehouse_name":"Ozon FBO","available_stock_count":9}]}`)
		default:
			t.Error("unapproved endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	path := filepath.Join(t.TempDir(), "stage-a.db")
	ws := workshops.NewService(path)
	defer ws.Close()
	u, e := ws.UpsertUser(901001, "", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "Ozon fixture")
	if e != nil {
		t.Fatal(e)
	}
	svc := ozon.NewService(ws.DB(), "synthetic-client", "synthetic-ozon-key")
	defer svc.Close()
	a := ozon.Access{UserID: u, WorkshopID: w, ConnectionID: 2}
	if e = svc.Attach(a); e != nil {
		t.Fatal(e)
	}
	manager, e := New(ctx, &wbmcpfixture.API{Mode: "success"}, nil, svc)
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	discovery := manager.Client.State()
	names := []string{}
	seen := map[string]bool{}
	for _, tool := range discovery.Tools {
		if seen[tool.Name] {
			t.Fatal("duplicate tool")
		}
		seen[tool.Name] = true
		names = append(names, tool.Name)
		if strings.HasPrefix(tool.Name, "ozon_") {
			if !tool.ReadOnly || strings.Contains(string(tool.InputSchema), "workshop_id") || strings.Contains(strings.ToLower(string(tool.InputSchema)), "api_key") {
				t.Fatal("tool authority schema")
			}
		}
	}
	for _, name := range []string{"ozon_list_products", "ozon_get_seller_stocks", "ozon_get_ozon_stocks", "wb_get_seller", "wb_get_wb_stocks"} {
		if !seen[name] {
			t.Fatal("missing tool", name)
		}
	}
	t.Log("OZON LISTTOOLS", strings.Join(names, ", "))
	if httpCalls.Load() != 0 {
		t.Fatal("startup/discovery made API calls")
	}
	call := func(tool string, refresh bool) ozon.Result {
		t.Helper()
		r, e := manager.Ozon(ctx, a, tool, ozon.Input{Refresh: refresh})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	catalog := call("ozon_list_products", true)
	if catalog.Status != ozon.Success || catalog.Total != 100 || catalog.Metrics.HTTPRequests != 3 || catalog.Metrics.Pages != 2 || catalog.Metrics.Saved != 100 || len(catalog.Products[0].SKUs) != 1 || catalog.Products[0].ProductID == catalog.Products[0].SKUs[0] {
		t.Fatalf("catalog: %+v", catalog)
	}
	seller := call("ozon_get_seller_stocks", true)
	if seller.Status != ozon.Success || seller.Total != 3 || seller.Metrics.HTTPRequests != 2 || seller.Stocks[1].Quantity != 0 || seller.Source != ozon.SellerSource {
		t.Fatalf("seller: %+v", seller)
	}
	fbo := call("ozon_get_ozon_stocks", true)
	if fbo.Status != ozon.Success || fbo.Source != ozon.FBOSource || fbo.Stocks[0].Quantity != 9 || fbo.Metrics.HTTPRequests != 1 {
		t.Fatalf("fbo: %+v", fbo)
	}
	before := httpCalls.Load()
	cached := call("ozon_get_seller_stocks", false)
	if cached.Cache != "HIT" || cached.Metrics.HTTPRequests != 0 || httpCalls.Load() != before || productCalls.Load() != 3 || cached.Stocks[0].Name != "Товар 1" {
		t.Fatal("cache/N+1", cached)
	}
	page, e := manager.Ozon(ctx, a, "ozon_list_products", ozon.Input{Offset: 90})
	if e != nil || len(page.Products) != 10 || page.Products[0].ProductID != 91 || httpCalls.Load() != before {
		t.Fatal("UI pagination", e)
	}
	sellerMode.Store(1)
	partial := call("ozon_get_seller_stocks", true)
	if partial.Status != ozon.Partial || partial.Total != 3 || partial.Metrics.Received != 2 || partial.Metrics.Valid != 1 || partial.Metrics.Invalid != 1 || partial.Stocks[0].Quantity != 7 || partial.Stocks[1].Quantity != 0 {
		t.Fatal("partial save", partial)
	}
	if partial.Stocks[1].Observation != "MISSING" || seller.Stocks[1].Observation != "ZERO" {
		t.Fatal("missing and zero confused")
	}
	var snapshots int
	if ws.DB().QueryRow(`SELECT COUNT(*) FROM marketplace_stock_history WHERE source=?`, ozon.SellerSource).Scan(&snapshots) != nil || snapshots != 4 {
		t.Fatal("history", snapshots)
	}
	if call("ozon_get_ozon_stocks", false).Stocks[0].Quantity != 9 {
		t.Fatal("source overwritten")
	}
	sellerMode.Store(2)
	rate := call("ozon_get_seller_stocks", true)
	if rate.Status != "RATE_LIMITED" || rate.RetryNotBefore == "" || rate.Metrics.HTTPRequests != 1 || len(rate.Stocks) != 3 {
		t.Fatal("rate", rate)
	}
	until, e := time.Parse(time.RFC3339Nano, rate.RetryNotBefore)
	if e != nil || time.Until(until) < 59*time.Minute {
		t.Fatal("deadline shortened")
	}
	// Reopen SQLite and construct a fresh client/manager: cooldown + identity +
	// catalog + current state must survive, not just remain in an old process map.
	manager.Close()
	svc.Close()
	ws.Close()
	ws = workshops.NewService(path)
	defer ws.Close()
	svc = ozon.NewService(ws.DB(), "synthetic-client", "synthetic-ozon-key")
	defer svc.Close()
	manager, e = New(ctx, &wbmcpfixture.API{Mode: "success"}, nil, svc)
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	before = httpCalls.Load()
	blocked := call("ozon_get_seller_stocks", true)
	if blocked.Status != "RATE_LIMITED" || blocked.Metrics.HTTPRequests != 0 || blocked.Metrics.Pages != 0 || httpCalls.Load() != before {
		t.Fatal("persistent cooldown", blocked)
	}
	if call("ozon_list_products", false).Total != 100 {
		t.Fatal("catalog lost on restart")
	}
	outsider, e := ws.UpsertUser(901002, "", "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	other, e := ws.CreateOwnedWorkshop(outsider, "Other")
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []ozon.Access{{UserID: outsider, WorkshopID: w, ConnectionID: 2}, {UserID: outsider, WorkshopID: other, ConnectionID: 2}, {UserID: u, WorkshopID: w, ConnectionID: 1}} {
		if _, e = manager.Ozon(ctx, bad, "ozon_list_products", ozon.Input{}); e == nil {
			t.Fatal("cross-scope accepted")
		}
	}
	if e = svc.Attach(ozon.Access{UserID: outsider, WorkshopID: other, ConnectionID: 2}); e == nil {
		t.Fatal("silent rebind")
	}
	if _, e = manager.Client.Ozon(ctx, "ozon_list_products", "forged", ozon.Input{}); e == nil {
		t.Fatal("forged grant")
	}
	traces, e := svc.History(a)
	if e != nil {
		t.Fatal(e)
	}
	traceBytes, _ := json.Marshal(traces)
	for _, secret := range []string{"synthetic-client", "synthetic-ozon-key", "Api-Key", "Authorization"} {
		if strings.Contains(string(traceBytes), secret) {
			t.Fatal("trace secret")
		}
	}
	foundBlocked := false
	foundCounts := false
	for _, tr := range traces {
		if tr.Result == "BLOCKED_LOCALLY" && tr.HTTPRequestCount == 0 {
			foundBlocked = true
		}
		if tr.Stage == "HTTP" && tr.HTTPStatus == 200 && tr.RecordsReceived != nil && tr.RecordsValid != nil {
			foundCounts = true
		}
	}
	if !foundBlocked || !foundCounts {
		t.Fatal("missing HTTP trace counts or local block")
	}
	t.Log("OZON TRACE", string(traceBytes))
	if e = svc.Disable(a); e != nil {
		t.Fatal(e)
	}
	if _, e = manager.Ozon(ctx, a, "ozon_get_seller_stocks", ozon.Input{Refresh: true}); e == nil {
		t.Fatal("disabled call")
	}
	if httpCalls.Load() != before {
		t.Fatal("denied call reached HTTP")
	}
	if dir := os.Getenv("OZON_STAGE_A_REPORT_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		evidence := struct {
			ListTools                                    []string
			Catalog, Seller, FBO, Partial, Rate, Blocked ozon.Result
			Traces                                       []ozon.Trace
			Telegram                                     []string
			AdditionalProductCalls                       int
		}{names, catalog, seller, fbo, partial, rate, blocked, traces, []string{ozon.Format(catalog), ozon.Format(seller), ozon.Format(fbo)}, 0}
		raw, _ := json.MarshalIndent(evidence, "", "  ")
		if e = os.WriteFile(filepath.Join(dir, "evidence.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
