package marketplace

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	wb "workshop-agent/internal/marketplace/wildberries"
	"workshop-agent/internal/storage"
)

func TestPartialStockPersistenceProvenanceAndRestart(t *testing.T) {
	f := setup(t)
	f.attach(t)
	save := func(b wb.StockBatch) {
		t.Helper()
		tx, e := f.ws.DB().Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = storage.SaveStockBatch(tx, f.sc.WorkshopID, f.sc.ConnectionID, 0, b); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	rows := []wb.Stock{}
	for i := int64(1); i <= 34; i++ {
		rows = append(rows, wb.Stock{NmID: i, ChrtID: i, WarehouseID: 1, Quantity: 10})
	}
	save(wb.NewStockBatch(wb.StockSeller, rows, nil))
	save(wb.NewStockBatch(wb.StockWB, []wb.Stock{{NmID: 34, ChrtID: 34, WarehouseID: 1, Quantity: 77}}, nil))
	b := wb.NewStockBatch(wb.StockSeller, rows[:33], nil)
	b.Info.Status = "PARTIAL"
	b.Info.Received = 34
	b.Info.Invalid = 1
	b.Info.Error = "invalid_response"
	b.Rows[0].Quantity = 0
	b.Rejections = []wb.StockRejection{{Index: 33, ExternalID: 34, Field: "amount", Expected: "integer", Actual: "schema_mismatch"}}
	save(b)
	save(wb.NewStockBatch(wb.StockWB, nil, wb.Forbidden))
	var seller, warehouse int
	if e := f.ws.DB().QueryRow(`SELECT quantity FROM marketplace_stocks WHERE kind='seller_stocks' AND chrt_id=34`).Scan(&seller); e != nil {
		t.Fatal(e)
	}
	if e := f.ws.DB().QueryRow(`SELECT quantity FROM marketplace_stocks WHERE kind='wb_stocks' AND chrt_id=34`).Scan(&warehouse); e != nil {
		t.Fatal(e)
	}
	if seller != 10 || warehouse != 77 {
		t.Fatal("missing or cross-source overwritten", seller, warehouse)
	}
	var raw string
	var info wb.StockInfo
	_ = f.ws.DB().QueryRow(`SELECT info_json FROM marketplace_stock_runs WHERE source='SELLER' ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if json.Unmarshal([]byte(raw), &info) != nil || info.Saved != 33 || info.Invalid != 1 {
		t.Fatal(raw)
	}
	// A new service facade reads persisted source status/history; no network call.
	var seq int
	var name, path string
	if e := f.ws.DB().QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	reopened, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	restarted, e := New(reopened, f.api)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	text, e := restarted.StockSourcesText(f.sc, "stocks", 0)
	if e != nil || !strings.Contains(text, "Получены частично") || !strings.Contains(text, "Нет доступа") || !strings.Contains(text, "сохранено: 33") {
		t.Fatal(text, e)
	}
}
