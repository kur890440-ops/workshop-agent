package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStockSourcesMigrationPreservesLegacyAndAddsSeller(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE schema_migrations(number INTEGER PRIMARY KEY,name TEXT)`,
		`CREATE TABLE marketplace_connections(id INTEGER,workshop_id INTEGER,PRIMARY KEY(id,workshop_id))`,
		`INSERT INTO marketplace_connections VALUES(1,6)`,
		`CREATE TABLE marketplace_stocks(connection_id INTEGER,workshop_id INTEGER,kind TEXT,warehouse_id INTEGER,nm_id INTEGER,chrt_id INTEGER,quantity INTEGER,fetched_at TEXT)`,
		`INSERT INTO marketplace_stocks VALUES(1,6,'seller_stocks',1,10,20,9,'2026-09-25T00:00:00Z'),(1,6,'wb_stocks',1,10,20,17,'2026-09-25T00:00:00Z')`,
		`CREATE TABLE wb_daily_snapshots(run_id INTEGER,workshop_id INTEGER,connection_id INTEGER,source_tool TEXT CHECK(source_tool IN ('wb_get_prices','wb_get_wb_stocks')),item_key TEXT,nm_id INTEGER,captured_at TEXT,data_json TEXT,PRIMARY KEY(run_id,source_tool,item_key))`,
		`CREATE TABLE wb_daily_current(workshop_id INTEGER,connection_id INTEGER,source_tool TEXT CHECK(source_tool IN ('wb_get_prices','wb_get_wb_stocks')),item_key TEXT,nm_id INTEGER,run_id INTEGER,captured_at TEXT,data_json TEXT,PRIMARY KEY(workshop_id,connection_id,source_tool,item_key))`,
		`INSERT INTO wb_daily_snapshots VALUES(1,6,1,'wb_get_wb_stocks','10:20:1',10,'2026-09-25T00:00:00Z','{"quantity":17}')`,
		`INSERT INTO wb_daily_current VALUES(6,1,'wb_get_wb_stocks','10:20:1',10,1,'2026-09-25T00:00:00Z','{"quantity":17}')`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		if e = migrateStockSources(tx); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM marketplace_stock_snapshots`).Scan(&n)
	if n != 2 {
		t.Fatal("legacy stocks lost", n)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM wb_daily_snapshots WHERE source_tool='wb_get_wb_stocks'`).Scan(&n)
	if n != 1 {
		t.Fatal("daily history lost")
	}
	if _, e = db.Exec(`INSERT INTO wb_daily_current VALUES(6,1,'wb_get_seller_stocks','10:20:1',10,2,'2026-09-25T01:00:00Z','{"quantity":9}')`); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM wb_daily_current`).Scan(&n)
	if n != 2 {
		t.Fatal("source collision")
	}
}
