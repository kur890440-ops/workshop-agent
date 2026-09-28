package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOzonMigrationPreservesWBForeignKeys(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE schema_migrations(number INTEGER PRIMARY KEY,name TEXT)`,
		`CREATE TABLE workshops(id INTEGER PRIMARY KEY)`,
		`INSERT INTO workshops VALUES(6)`,
		`CREATE TABLE marketplace_connections(id INTEGER PRIMARY KEY CHECK(id=1),workshop_id INTEGER REFERENCES workshops(id),provider TEXT DEFAULT 'wildberries',secret_ref TEXT DEFAULT 'WB_API_TOKEN',enabled INTEGER,revision INTEGER DEFAULT 1,seller_id TEXT DEFAULT '',seller_name TEXT DEFAULT '',created_at TEXT DEFAULT '',disabled_at TEXT DEFAULT '',checked_at TEXT DEFAULT '',check_error TEXT DEFAULT '',identity_fingerprint TEXT DEFAULT '',UNIQUE(id,workshop_id))`,
		`INSERT INTO marketplace_connections(id,workshop_id,enabled,seller_id) VALUES(1,6,1,'original-seller')`,
		`CREATE TABLE wb_fixture(connection_id INTEGER,workshop_id INTEGER,value INTEGER,FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`INSERT INTO wb_fixture VALUES(1,6,37)`,
		`PRAGMA foreign_keys=OFF`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = migrateOzon(tx); e != nil {
		t.Fatal(e)
	}
	if e = migrateOzon(tx); e != nil {
		t.Fatal("not idempotent", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`PRAGMA foreign_keys=ON`); e != nil {
		t.Fatal(e)
	}
	rows, e := db.Query(`PRAGMA foreign_key_check`)
	if e != nil {
		t.Fatal(e)
	}
	bad := rows.Next()
	rows.Close()
	if bad {
		t.Fatal("FK broken")
	}
	var value int
	var seller string
	if db.QueryRow(`SELECT f.value,c.seller_id FROM wb_fixture f JOIN marketplace_connections c ON c.id=f.connection_id`).Scan(&value, &seller) != nil || value != 37 || seller != "original-seller" {
		t.Fatal("WB data changed")
	}
	if _, e = db.Exec(`INSERT INTO marketplace_connections(id,workshop_id,provider,secret_ref,enabled) VALUES(2,6,'OZON','OZON_ENV',1)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO marketplace_catalog(connection_id,workshop_id,provider,product_id,offer_id,name,updated_at,last_seen_at) VALUES(1,6,'OZON',99,'a','name','','')`); e == nil {
		t.Fatal("Ozon data under WB connection accepted")
	}
}
