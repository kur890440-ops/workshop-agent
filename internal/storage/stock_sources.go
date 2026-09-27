package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	wb "workshop-agent/internal/marketplace/wildberries"
)

func migrateStockSources(tx *sql.Tx) error {
	var n int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=117`).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	for _, table := range []string{"wb_daily_snapshots", "wb_daily_current"} {
		var schema string
		if e := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, table).Scan(&schema); e != nil {
			return e
		}
		schema = strings.Replace(schema, table, table+"_sources117", 1)
		schema = strings.ReplaceAll(schema, "'wb_get_prices','wb_get_wb_stocks'", "'wb_get_prices','wb_get_wb_stocks','wb_get_seller_stocks'")
		for _, q := range []string{schema, "INSERT INTO " + table + "_sources117 SELECT * FROM " + table, "DROP TABLE " + table, "ALTER TABLE " + table + "_sources117 RENAME TO " + table} {
			if _, e := tx.Exec(q); e != nil {
				return e
			}
		}
	}
	for _, q := range []string{
		`CREATE TABLE marketplace_stock_runs(id INTEGER PRIMARY KEY,workshop_id INTEGER NOT NULL,connection_id INTEGER NOT NULL,job_run_id INTEGER,source TEXT NOT NULL CHECK(source IN ('SELLER','WB')),captured_at TEXT NOT NULL,status TEXT NOT NULL,info_json TEXT NOT NULL,rejections_json TEXT NOT NULL,FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`CREATE TABLE marketplace_stock_snapshots(run_id INTEGER NOT NULL REFERENCES marketplace_stock_runs(id),warehouse_id INTEGER NOT NULL,nm_id INTEGER NOT NULL,chrt_id INTEGER NOT NULL,quantity INTEGER NOT NULL CHECK(quantity>=0),PRIMARY KEY(run_id,warehouse_id,chrt_id))`,
		`CREATE INDEX stock_runs_scope ON marketplace_stock_runs(workshop_id,connection_id,source,id)`,
		`INSERT INTO marketplace_stock_runs(workshop_id,connection_id,source,captured_at,status,info_json,rejections_json) SELECT workshop_id,connection_id,CASE kind WHEN 'seller_stocks' THEN 'SELLER' ELSE 'WB' END,MAX(fetched_at),'LEGACY','{}','[]' FROM marketplace_stocks GROUP BY workshop_id,connection_id,kind`,
		`INSERT INTO marketplace_stock_snapshots SELECT r.id,s.warehouse_id,s.nm_id,s.chrt_id,s.quantity FROM marketplace_stocks s JOIN marketplace_stock_runs r ON r.workshop_id=s.workshop_id AND r.connection_id=s.connection_id AND r.source=CASE s.kind WHEN 'seller_stocks' THEN 'SELLER' ELSE 'WB' END`,
		`INSERT INTO schema_migrations(number,name) VALUES(117,'independent_stock_sources')`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}

// SaveStockBatch is shared by manual sync and the MCP pipeline save service.
// Current state is an upsert of validated rows, never a destructive replacement.
func SaveStockBatch(tx *sql.Tx, workshop, connection, jobRun int64, b wb.StockBatch) (int, error) {
	if b.Info.Source != wb.StockSeller && b.Info.Source != wb.StockWB {
		return 0, errors.New("invalid_stock_source")
	}
	kind := "seller_stocks"
	if b.Info.Source == wb.StockWB {
		kind = "wb_stocks"
	}
	if !b.Usable() {
		b.Rows = nil
	}
	seen := map[string]bool{}
	for _, r := range b.Rows {
		key := fmt.Sprintf("%d:%d", r.WarehouseID, r.ChrtID)
		if r.NmID <= 0 || r.ChrtID <= 0 || r.WarehouseID <= 0 || r.Quantity < 0 || seen[key] {
			return 0, errors.New("invalid_stock_row")
		}
		seen[key] = true
	}
	b.Info.Saved = len(b.Rows)
	info, e := json.Marshal(b.Info)
	if e != nil {
		return 0, e
	}
	rejections, e := json.Marshal(b.Rejections)
	if e != nil {
		return 0, e
	}
	result, e := tx.Exec(`INSERT INTO marketplace_stock_runs(workshop_id,connection_id,job_run_id,source,captured_at,status,info_json,rejections_json) VALUES(?,?,?,?,?,?,?,?)`, workshop, connection, jobRun, b.Info.Source, b.Info.CapturedAt, b.Info.Status, string(info), string(rejections))
	if e != nil {
		return 0, e
	}
	id, e := result.LastInsertId()
	if e != nil {
		return 0, e
	}
	for _, r := range b.Rows {
		if _, e = tx.Exec(`INSERT INTO marketplace_stock_snapshots(run_id,warehouse_id,nm_id,chrt_id,quantity) VALUES(?,?,?,?,?)`, id, r.WarehouseID, r.NmID, r.ChrtID, r.Quantity); e != nil {
			return 0, e
		}
		if _, e = tx.Exec(`INSERT INTO marketplace_stocks(connection_id,workshop_id,kind,warehouse_id,warehouse_name,nm_id,chrt_id,quantity,fetched_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(connection_id,kind,warehouse_id,chrt_id) DO UPDATE SET nm_id=excluded.nm_id,quantity=excluded.quantity,warehouse_name=excluded.warehouse_name,fetched_at=excluded.fetched_at`, connection, workshop, kind, r.WarehouseID, r.WarehouseName, r.NmID, r.ChrtID, r.Quantity, b.Info.CapturedAt); e != nil {
			return 0, e
		}
	}
	return len(b.Rows), nil
}
