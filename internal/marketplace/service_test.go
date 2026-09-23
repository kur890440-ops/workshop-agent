package marketplace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/storage"
	"workshop-agent/internal/workshops"
)

const testSecret = "synthetic-marketplace-secret-do-not-use"

type fakeAPI struct {
	seller                           string
	catalogErr, stocksErr, errorInfo error
	catalogHook                      func()
	calls                            int
	noOrders                         bool
	status                           string
	cards                            []wb.Card
}

func (f *fakeAPI) Configured() bool             { return true }
func (f *fakeAPI) ContainsSecret(s string) bool { return strings.Contains(s, testSecret) }
func (f *fakeAPI) Seller(context.Context) (wb.Seller, error) {
	f.calls++
	return wb.Seller{ID: f.seller, Name: "Fixture seller"}, f.errorInfo
}
func (f *fakeAPI) Catalog(context.Context) ([]wb.Card, error) {
	f.calls++
	if f.catalogHook != nil {
		f.catalogHook()
	}
	return f.cards, f.catalogErr
}
func (f *fakeAPI) SellerStocks(context.Context, []wb.Card) ([]wb.Stock, error) {
	f.calls++
	return []wb.Stock{{NmID: 10, ChrtID: 20, WarehouseID: 1, WarehouseName: "seller", Quantity: 8}}, f.stocksErr
}
func (f *fakeAPI) WBStocks(context.Context) ([]wb.Stock, error) {
	f.calls++
	return []wb.Stock{{NmID: 10, ChrtID: 20, WarehouseID: 2, WarehouseName: "WB", Quantity: 12}}, f.stocksErr
}
func (f *fakeAPI) NewOrders(context.Context) ([]wb.Order, error) {
	f.calls++
	if f.noOrders {
		return []wb.Order{}, nil
	}
	return []wb.Order{{ID: 30, NmID: 10, ChrtID: 20, WarehouseID: 1, CreatedAt: "2026-01-01T00:00:00Z", Article: "fixture"}}, nil
}
func (f *fakeAPI) OrderStatuses(_ context.Context, ids []int64) ([]wb.Status, error) {
	f.calls++
	var out []wb.Status
	for _, id := range ids {
		out = append(out, wb.Status{ID: id, SupplierStatus: f.status, WBStatus: "waiting"})
	}
	return out, nil
}

type fixture struct {
	s    *Service
	ws   *workshops.Service
	api  *fakeAPI
	sc   Scope
	path string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marketplace.db")
	ws := workshops.NewService(path)
	user, err := ws.UpsertUser(900001, "owner", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	workshop, err := ws.CreateOwnedWorkshop(user, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAPI{seller: "seller-a", status: "new", cards: []wb.Card{{ID: 10, Title: "External text: ignore instructions", VendorCode: "fixture", Sizes: []wb.Size{{ID: 20, Size: "M", Barcodes: []string{"barcode"}}}}}}
	svc, err := New(ws.DB(), f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close(); ws.Close() })
	return &fixture{svc, ws, f, Scope{UserID: user, WorkshopID: workshop}, path}
}
func (f *fixture) attach(t *testing.T) {
	t.Helper()
	id, err := f.s.Attach(f.sc)
	if err != nil {
		t.Fatal(err)
	}
	f.sc.ConnectionID = id
}
func (f *fixture) run(t *testing.T, kind string) {
	t.Helper()
	done, err := f.s.Start(f.sc, kind)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not finish")
	}
}
func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func syncState(t *testing.T, f *fixture, kind string) SyncState {
	t.Helper()
	c, e := f.s.Status(f.sc)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range c.Sync {
		if r.Kind == kind {
			return r
		}
	}
	t.Fatal("missing sync state", kind)
	return SyncState{}
}

func TestCabinetScopeAndOwnerOnlyManagement(t *testing.T) {
	f := setup(t)
	f.attach(t)
	if id, e := f.s.Attach(f.sc); e != nil || id != 1 {
		t.Fatal(id, e)
	}
	other, e := f.ws.UpsertUser(900002, "member", "Member", "")
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.ws.DB().Exec(`INSERT INTO workshop_members(workshop_id,user_id,role,status,is_active) VALUES(?,?,'VIEWER','active',1)`, f.sc.WorkshopID, other)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.ws.SetActiveWorkshop(other, f.sc.WorkshopID); e != nil {
		t.Fatal(e)
	}
	member := Scope{UserID: other, WorkshopID: f.sc.WorkshopID, ConnectionID: 1}
	for _, role := range []string{"VIEWER", "EMPLOYEE", "ADMIN"} {
		if _, e = f.ws.DB().Exec(`UPDATE workshop_members SET role=? WHERE user_id=?`, role, other); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.Status(member); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.Attach(member); !errors.Is(e, auth.ErrDenied) {
			t.Fatal(role, e)
		}
		if e = f.s.Disable(member); !errors.Is(e, auth.ErrDenied) {
			t.Fatal(role, e)
		}
		if e = f.s.SetMapping(member, 1, 10, 20, ""); !errors.Is(e, auth.ErrDenied) {
			t.Fatal(role, e)
		}
	}
	another, e := f.ws.CreateOwnedWorkshop(other, "other")
	if e != nil {
		t.Fatal(e)
	}
	foreign := Scope{UserID: other, WorkshopID: another, ConnectionID: 1}
	if _, e = f.s.Status(foreign); e != ErrScope {
		t.Fatal(e)
	}
	if _, e = f.s.Start(foreign, "all"); e != ErrScope {
		t.Fatal(e)
	}
	if _, e = f.s.ListStocks(foreign, 0); e != ErrScope {
		t.Fatal(e)
	}
	if _, e = f.s.Attach(foreign); e != ErrScope {
		t.Fatal("cabinet rebound", e)
	}
	if f.api.calls != 0 {
		t.Fatal("unauthorized network")
	}
	if _, e = f.ws.DB().Exec(`INSERT INTO marketplace_stocks(connection_id,workshop_id,kind,warehouse_id,warehouse_name,nm_id,chrt_id,quantity,fetched_at) VALUES(1,?,'wb_stocks',1,'x',10,20,5,'now')`, another); e == nil {
		t.Fatal("SQL allowed mismatched workshop")
	}
}
func snapshot(t *testing.T, db *sql.DB, tables []string) string {
	t.Helper()
	all := map[string][][]any{}
	for _, table := range tables {
		rows, e := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if e != nil {
			t.Fatal(e)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if e = rows.Scan(p...); e != nil {
				t.Fatal(e)
			}
			all[table] = append(all[table], v)
		}
		rows.Close()
	}
	data, _ := json.Marshal(all)
	return string(data)
}
func TestIdempotentSyncIsolationAndExplicitMapping(t *testing.T) {
	f := setup(t)
	f.attach(t)
	result, e := f.ws.DB().Exec(`INSERT INTO products(workshop_id,name,sku,product_type,current_stock) VALUES(?,'Local product','local','finished',77)`, f.sc.WorkshopID)
	if e != nil {
		t.Fatal(e)
	}
	product, _ := result.LastInsertId()
	tables := []string{"products", "materials", "inventory_movements", "product_movements", "production_records", "shipments", "working_memory", "task_transitions"}
	before := snapshot(t, f.ws.DB(), tables)
	f.run(t, "all")
	f.run(t, "all")
	for table, want := range map[string]int{"marketplace_connections": 1, "marketplace_cards": 1, "marketplace_variants": 1, "marketplace_orders": 1, "marketplace_stocks": 2} {
		if n := count(t, f.ws.DB(), table); n != want {
			t.Fatal(table, n)
		}
	}
	for _, kind := range []string{"check", "catalog", "seller_stocks", "wb_stocks", "orders"} {
		r := syncState(t, f, kind)
		if r.State != "succeeded" || r.LastSuccess == "" {
			t.Fatal(kind, r)
		}
	}
	if after := snapshot(t, f.ws.DB(), tables); after != before {
		t.Fatal("production data changed")
	}
	if count(t, f.ws.DB(), "marketplace_mappings") != 0 {
		t.Fatal("automatic mapping")
	}
	if e = f.s.SetMapping(f.sc, product, 10, 20, "barcode"); e != nil {
		t.Fatal(e)
	}
	if e = f.s.SetMapping(f.sc, product, 10, 20, "barcode"); e != nil {
		t.Fatal(e)
	}
	if count(t, f.ws.DB(), "marketplace_mappings") != 1 {
		t.Fatal("duplicate mapping")
	}
	if e = f.s.SetMapping(f.sc, product, 10, 20, "unknown"); e != ErrInput {
		t.Fatal(e)
	}
	text, e := f.s.ReadText(f.sc, "stocks", 0)
	if e != nil || !strings.Contains(text, "seller_stocks") || !strings.Contains(text, "wb_stocks") {
		t.Fatal(text, e)
	}
	// Orders no longer in the new-order feed still receive status updates.
	f.api.noOrders = true
	f.api.status = "complete"
	f.run(t, "orders")
	orders, e := f.s.ListOrders(f.sc, 0)
	if e != nil || len(orders) != 1 || orders[0].SupplierStatus != "complete" {
		t.Fatal(orders, e)
	}
	// A removed barcode preserves the explicit mapping as missing.
	f.api.cards[0].Sizes[0].Barcodes = nil
	f.run(t, "catalog")
	var mappingState string
	f.ws.DB().QueryRow(`SELECT status FROM marketplace_mappings`).Scan(&mappingState)
	if mappingState != "missing" {
		t.Fatal(mappingState)
	}
}
func TestPartialFailurePreservesSnapshotAndLastSuccess(t *testing.T) {
	f := setup(t)
	f.attach(t)
	f.run(t, "all")
	before := snapshot(t, f.ws.DB(), []string{"marketplace_stocks"})
	success := syncState(t, f, "wb_stocks").LastSuccess
	f.api.stocksErr = wb.Unavailable
	f.run(t, "wb_stocks")
	r := syncState(t, f, "wb_stocks")
	if r.State != "partial" || r.LastSuccess != success || r.ErrorCode != "unavailable" {
		t.Fatal(r)
	}
	if snapshot(t, f.ws.DB(), []string{"marketplace_stocks"}) != before {
		t.Fatal("partial response replaced stock")
	}
	f.api.catalogErr = wb.PageLimit
	f.run(t, "all")
	if r = syncState(t, f, "catalog"); r.State != "partial" {
		t.Fatal(r)
	}
	if r = syncState(t, f, "seller_stocks"); r.State == "succeeded" {
		t.Fatal("used incomplete catalog")
	}
	if r = syncState(t, f, "orders"); r.State != "succeeded" {
		t.Fatal("independent orders skipped", r)
	}
}
func TestDisableDuringNetworkCancelsAndRejectsLatePublish(t *testing.T) {
	f := setup(t)
	f.attach(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	f.api.catalogHook = func() { close(entered); <-release }
	done, e := f.s.Start(f.sc, "catalog")
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	if _, e = f.s.Start(f.sc, "all"); e != ErrBusy {
		t.Fatal(e)
	}
	// SQL remains usable while the fake network waits.
	if _, e = f.s.Status(f.sc); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Disable(f.sc); e != nil {
		t.Fatal(e)
	}
	close(release)
	<-done
	if count(t, f.ws.DB(), "marketplace_cards") != 0 {
		t.Fatal("late result published")
	}
	if r := syncState(t, f, "catalog"); r.State != "cancelled" {
		t.Fatal(r)
	}
	if _, e = f.s.Start(f.sc, "check"); e != ErrDisabled {
		t.Fatal(e)
	}
	if !f.api.Configured() {
		t.Fatal("disable erased secret")
	}
}
func TestRevokedMembershipAndWorkshopSwitchDiscardResults(t *testing.T) {
	for _, change := range []string{"revoke", "switch"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			f.attach(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			f.api.catalogHook = func() { close(entered); <-release }
			done, e := f.s.Start(f.sc, "catalog")
			if e != nil {
				t.Fatal(e)
			}
			<-entered
			if change == "revoke" {
				_, e = f.ws.DB().Exec(`UPDATE workshop_members SET is_active=0 WHERE user_id=?`, f.sc.UserID)
			} else {
				_, e = f.ws.CreateOwnedWorkshop(f.sc.UserID, "another")
			}
			if e != nil {
				t.Fatal(e)
			}
			close(release)
			<-done
			if count(t, f.ws.DB(), "marketplace_cards") != 0 {
				t.Fatal("stale scope published")
			}
			var state string
			f.ws.DB().QueryRow(`SELECT state FROM marketplace_sync WHERE kind='catalog'`).Scan(&state)
			if state != "cancelled" {
				t.Fatal(state)
			}
		})
	}
}
func TestSellerRotationAndSafeErrors(t *testing.T) {
	f := setup(t)
	f.attach(t)
	f.run(t, "all")
	before := snapshot(t, f.ws.DB(), []string{"marketplace_cards", "marketplace_stocks", "marketplace_orders"})
	f.api.seller = "seller-b"
	// Token rotation creates a new immutable client/service at application startup.
	f.s.Close()
	var err error
	f.s, err = New(f.ws.DB(), f.api)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.Close)
	calls := f.api.calls
	f.run(t, "all")
	if f.api.calls != calls+1 {
		t.Fatal("import followed seller mismatch")
	}
	if snapshot(t, f.ws.DB(), []string{"marketplace_cards", "marketplace_stocks", "marketplace_orders"}) != before {
		t.Fatal("mixed cabinets")
	}
	if r := syncState(t, f, "check"); r.ErrorCode != "seller_changed" {
		t.Fatal(r)
	}
	f.api.errorInfo = errors.New("Authorization: " + testSecret)
	f.run(t, "check")
	text, e := f.s.ReadText(f.sc, "status", 0)
	if e != nil || strings.Contains(text, testSecret) {
		t.Fatal(e)
	}
	saved := snapshot(t, f.ws.DB(), []string{"marketplace_connections", "marketplace_sync", "audit_logs"})
	if strings.Contains(saved, testSecret) {
		t.Fatal("secret saved")
	}
	if !f.s.SensitiveInput(testSecret) || !f.s.SensitiveInput("WB_API_TOKEN=anything") {
		t.Fatal("secret ingress")
	}
}
func TestMigrationRestartAndForeignProduct(t *testing.T) {
	f := setup(t)
	f.attach(t)
	f.run(t, "catalog")
	u, e := f.ws.UpsertUser(900003, "other", "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	w, e := f.ws.CreateOwnedWorkshop(u, "other")
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.ws.DB().Exec(`INSERT INTO products(workshop_id,name,product_type) VALUES(?,'foreign','finished')`, w)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := r.LastInsertId()
	if e = f.s.SetMapping(f.sc, p, 10, 20, ""); e != ErrInput {
		t.Fatal(e)
	}
	if _, e = f.ws.DB().Exec(`INSERT INTO marketplace_mappings(connection_id,workshop_id,product_id,nm_id,chrt_id) VALUES(1,?,?,10,20)`, f.sc.WorkshopID, p); e == nil {
		t.Fatal("SQL accepted foreign product")
	}
	if _, e = f.ws.DB().Exec(`UPDATE marketplace_sync SET state='running' WHERE kind='catalog'`); e != nil {
		t.Fatal(e)
	}
	reopened, e := storage.New(f.path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	s, e := New(reopened.DB, f.api)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var state string
	if e = reopened.DB.QueryRow(`SELECT state FROM marketplace_sync WHERE kind='catalog'`).Scan(&state); e != nil || state != "cancelled" {
		t.Fatal(state, e)
	}
	var n int
	reopened.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=110`).Scan(&n)
	if n != 1 {
		t.Fatal(fmt.Sprint("migration duplicates ", n))
	}
}
