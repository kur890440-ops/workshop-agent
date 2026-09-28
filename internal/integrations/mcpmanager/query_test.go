package mcpmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"workshop-agent/internal/integrations/wbmcpfixture"
	"workshop-agent/internal/marketplace"
	"workshop-agent/internal/marketplace/ozon"
	wb "workshop-agent/internal/marketplace/wildberries"
	q "workshop-agent/internal/marketplacequery"
	"workshop-agent/internal/workshops"
)

type queryAPI struct {
	wbmcpfixture.API
	seller, warehouse, catalog atomic.Int32
}

func (a *queryAPI) Catalog(context.Context) ([]wb.Card, error) {
	a.catalog.Add(1)
	return []wb.Card{{ID: 101, Title: "Товар A", Sizes: []wb.Size{{ID: 201}}}, {ID: 102, Title: "Товар B", Sizes: []wb.Size{{ID: 202}}}}, nil
}
func (a *queryAPI) SellerStockBatch(context.Context, []wb.Card) wb.StockBatch {
	a.seller.Add(1)
	return wb.NewStockBatch(wb.StockSeller, []wb.Stock{{NmID: 101, ChrtID: 201, WarehouseID: 301, Quantity: 2}, {NmID: 102, ChrtID: 202, WarehouseID: 301, Quantity: 8}, {NmID: 103, ChrtID: 203, WarehouseID: 301, Quantity: 0}}, nil)
}
func (a *queryAPI) WBStocks(ctx context.Context) ([]wb.Stock, error) {
	a.warehouse.Add(1)
	return a.API.WBStocks(ctx)
}

func TestDay20ActualMCPQueries(t *testing.T) {
	ctx := context.Background()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "q.db"))
	defer ws.Close()
	u, e := ws.UpsertUser(901701, "", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := ws.CreateOwnedWorkshop(u, "Day20")
	if e != nil {
		t.Fatal(e)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := ws.DB().Exec(sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,?,1,'day17-fixture')`, w)
	oz := ozon.NewService(ws.DB(), "fake-client", "fake-key")
	defer oz.Close()
	if e = oz.Attach(ozon.Access{UserID: u, WorkshopID: w}); e != nil {
		t.Fatal(e)
	}
	api := &queryAPI{API: wbmcpfixture.API{Mode: "success"}}
	svc, e := marketplace.New(ws.DB(), api)
	if e != nil {
		t.Fatal(e)
	}
	defer svc.Close()
	manager, e := New(ctx, api, nil, oz)
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	repo := &q.Repository{DB: ws.DB(), Sensitive: func(s string) bool { return strings.Contains(s, "fake-key") }}
	orchestrator := &q.MarketplaceOrchestrator{Store: repo, MCP: &QueryAdapter{Manager: manager, WB: svc, Ozon: oz}}
	sc := q.Scope{UserID: u, WorkshopID: w}
	for i := int64(1); i <= 3; i++ {
		exec(`INSERT INTO marketplace_catalog(connection_id,workshop_id,provider,product_id,offer_id,name,updated_at,last_seen_at) VALUES(2,?,'OZON',?,?,?,'',?)`, w, 700+i, fmt.Sprint(i), fmt.Sprintf("Товар %c", 'A'+i-1), time.Now().UTC().Format(time.RFC3339))
		exec(`INSERT INTO marketplace_catalog_skus(connection_id,workshop_id,provider,product_id,sku) VALUES(2,?,'OZON',?,?)`, w, 700+i, 900+i)
		exec(`INSERT INTO marketplace_cards(connection_id,workshop_id,nm_id,title,vendor_code,source_updated_at,fetched_at) VALUES(1,?,?,?,'','','2026-09-28T01:00:00Z')`, w, 100+i, fmt.Sprintf("Товар %c", 'A'+i-1))
		exec(`INSERT INTO marketplace_variants(connection_id,workshop_id,nm_id,chrt_id,size) VALUES(1,?,?,?,'M')`, w, 100+i, 200+i)
	}
	putRun := func(status string) int64 {
		v := ozon.Result{Provider: "OZON", Source: ozon.SellerSource, Status: status, UntrustedData: true, CapturedAt: "2026-09-28T01:00:00Z", RetryNotBefore: "2026-09-28T02:00:00Z"}
		b, _ := json.Marshal(v)
		res, e := ws.DB().Exec(`INSERT INTO marketplace_source_runs(connection_id,workshop_id,provider,source,captured_at,status,info_json) VALUES(2,?,'OZON',?,?,?,?)`, w, ozon.SellerSource, v.CapturedAt, status, string(b))
		if e != nil {
			t.Fatal(e)
		}
		id, _ := res.LastInsertId()
		return id
	}
	run := putRun("SUCCESS")
	for i, qty := range []int{1, 3, 0} {
		exec(`INSERT INTO marketplace_stock_current(connection_id,workshop_id,provider,source,sku,warehouse_id,product_id,offer_id,warehouse_name,quantity,captured_at,run_id) VALUES(2,?,'OZON',?,?,88,?,'','Ozon seller',?,'2026-09-28T01:00:00Z',?)`, w, ozon.SellerSource, 901+i, 701+i, qty, run)
	}
	// Catalog query also needs a recorded successful source state.
	b, _ := json.Marshal(ozon.Result{Provider: "OZON", Source: ozon.CatalogSource, Status: "SUCCESS", UntrustedData: true})
	exec(`INSERT INTO marketplace_source_runs(connection_id,workshop_id,provider,source,captured_at,status,info_json) VALUES(2,?,'OZON',?,'2026-09-28T01:00:00Z','SUCCESS',?)`, w, ozon.CatalogSource, string(b))
	five := int64(5)
	base := q.MarketplaceQueryIntent{Marketplaces: []q.Provider{q.WB, q.Ozon}, QueryType: q.Stocks, StockSource: q.Seller, Filters: q.Filters{Operator: "LT", Quantity: &five}, Grouping: "MARKETPLACE", Sorting: "NAME", ComparisonMode: "NONE", IncludeZeroStock: true, RequestedFields: []string{"NAME", "QUANTITY"}}
	evidence := map[string]any{"discovery": manager.Client.State()}
	runQuery := func(name, text string, in q.MarketplaceQueryIntent) q.MarketplaceQueryResult {
		t.Helper()
		v, e := orchestrator.Execute(ctx, sc, text, in)
		if e != nil {
			t.Fatal(e)
		}
		evidence[name] = map[string]any{"trace": v.Trace, "telegram": q.Render(v)}
		return v
	}
	both := runQuery("both", "Покажи товары с остатком меньше 5 на WB и Ozon", base)
	if both.Status != "SUCCESS" || len(both.Trace.Steps) != 2 || both.Trace.Steps[0].Tool.Server != "WB" || both.Trace.Steps[1].Tool.Server != "OZON" || len(both.Sections[0].Items) != 2 || len(both.Sections[1].Items) != 3 {
		t.Fatalf("both %+v", both)
	}
	if api.warehouse.Load() != 0 {
		t.Fatal("wrong source")
	}
	wbOnly := base
	wbOnly.Marketplaces = []q.Provider{q.WB}
	before := traceCount(t, ws, "OZON")
	v := runQuery("wb_only", "Покажи только Wildberries", wbOnly)
	if len(v.Trace.Steps) != 1 || traceCount(t, ws, "OZON") != before {
		t.Fatal("Ozon called")
	}
	ozOnly := base
	ozOnly.Marketplaces = []q.Provider{q.Ozon}
	ozOnly.QueryType = q.Products
	ozOnly.StockSource = q.Auto
	ozOnly.Filters = q.Filters{Operator: "NONE"}
	calls := api.seller.Load() + api.catalog.Load() + api.warehouse.Load()
	v = runQuery("ozon_only", "Покажи товары Ozon", ozOnly)
	if len(v.Sections[0].Items) != 3 || len(v.Trace.Steps) != 1 || calls != api.seller.Load()+api.catalog.Load()+api.warehouse.Load() {
		t.Fatal("WB called or catalog lost")
	}
	three := int64(3)
	long := base
	long.Filters = q.Filters{Operator: "LE", Quantity: &three, SplitZero: true}
	v = runQuery("long", "Посмотри остатки WB и Ozon, найди позиции <=3, отдельно нулевые и 1–3", long)
	if !strings.Contains(q.Render(v), "Нулевой остаток") || !strings.Contains(q.Render(v), "больше нуля") {
		t.Fatal("split missing")
	}
	// Explicit mapping, never matching names. WB A=2, Ozon product703=0.
	res, e := ws.DB().Exec(`INSERT INTO products(workshop_id,name,product_type) VALUES(?,'Internal A','finished')`, w)
	if e != nil {
		t.Fatal(e)
	}
	pid, _ := res.LastInsertId()
	exec(`INSERT INTO marketplace_mappings(connection_id,workshop_id,product_id,nm_id,chrt_id) VALUES(1,?,?,101,201)`, w, pid)
	if e = repo.MapOzon(sc, 903, pid); e != nil {
		t.Fatal(e)
	}
	cmp := base
	cmp.QueryType = q.Compare
	cmp.ComparisonMode = "WB_AVAILABLE_OZON_ZERO"
	cmp.Filters = q.Filters{Operator: "NONE"}
	v = runQuery("comparison", "Какие товары есть на WB, но закончились на Ozon?", cmp)
	if len(v.Comparison) != 1 || v.Comparison[0].ProductID != pid || v.Comparison[0].Ozon != 0 || v.Unmapped == 0 {
		t.Fatal("comparison/mapping", v)
	}
	putRun("RATE_LIMITED")
	v = runQuery("partial", "Покажи остатки WB и Ozon", base)
	if v.Status != "PARTIAL_SUCCESS" || len(v.Sections[0].Items) == 0 || len(v.Sections[1].Items) != 0 || v.Sections[1].RetryNotBefore == "" {
		t.Fatal("partial zeros", v)
	}
	if _, e = repo.LastTrace(sc); e != nil {
		t.Fatal(e)
	}
	// Cross-workshop mappings/queries must fail even for the same application user.
	other, e := ws.CreateOwnedWorkshop(u, "Other workshop")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = orchestrator.Execute(ctx, q.Scope{UserID: u, WorkshopID: w}, "old workshop", base); e == nil {
		t.Fatal("stale active workshop")
	}
	if e = repo.MapOzon(q.Scope{UserID: u, WorkshopID: other}, 903, pid); e == nil {
		t.Fatal("cross workshop mapping")
	}
	exec(`UPDATE user_workshop_context SET active_workshop_id=? WHERE user_id=?`, w, u)
	exec(`UPDATE marketplace_connections SET enabled=0,revision=revision+1 WHERE id=2`)
	disabled := runQuery("disabled", "Остатки Ozon", base)
	if disabled.Sections[1].Status == "SUCCESS" || len(disabled.Sections[1].Items) != 0 {
		t.Fatal("disabled data")
	}
	exec(`UPDATE marketplace_connections SET enabled=1,revision=revision+1 WHERE id=2`)
	if _, e = orchestrator.Execute(ctx, sc, "fake-key", base); e == nil {
		t.Fatal("secret persisted")
	}
	raw, _ := json.Marshal(evidence)
	if strings.Contains(string(raw), "fake-key") {
		t.Fatal("trace leak")
	}
	if path := os.Getenv("DAY20_REPORT_DIR"); path != "" {
		if e = os.MkdirAll(path, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(path, "evidence.json"), raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE workshop_members SET is_active=0 WHERE user_id=? AND workshop_id=?`, u, w)
	if _, e = orchestrator.Execute(ctx, sc, "остатки", base); e == nil {
		t.Fatal("revoked access")
	}
}
func traceCount(t *testing.T, ws *workshops.Service, provider string) int {
	t.Helper()
	var n int
	if e := ws.DB().QueryRow(`SELECT COUNT(*) FROM integration_request_trace WHERE provider=?`, provider).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
