package storage

import "database/sql"

// Migration 118 preserves legacy WB identities and all referencing tables.
// Migrate disables FK enforcement for the table rebuild, then validates every FK
// inside the same transaction before commit. It runs before application services.
func migrateOzon(tx *sql.Tx) error {
	var n int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=118`).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return nil
	}
	queries := []string{
		`CREATE TABLE marketplace_connections_next (
 id INTEGER PRIMARY KEY, workshop_id INTEGER NOT NULL REFERENCES workshops(id),
 provider TEXT NOT NULL DEFAULT 'wildberries', secret_ref TEXT NOT NULL DEFAULT 'WB_API_TOKEN',
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), revision INTEGER NOT NULL DEFAULT 1,
 seller_id TEXT NOT NULL DEFAULT '',seller_name TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,disabled_at TEXT NOT NULL DEFAULT '',
 checked_at TEXT NOT NULL DEFAULT '',check_error TEXT NOT NULL DEFAULT '',identity_fingerprint TEXT NOT NULL DEFAULT '',
 CHECK((id=1 AND provider='wildberries' AND secret_ref='WB_API_TOKEN') OR (id=2 AND provider='OZON' AND secret_ref='OZON_ENV')),
 UNIQUE(id,workshop_id),UNIQUE(id,workshop_id,provider))`,
		`INSERT INTO marketplace_connections_next SELECT id,workshop_id,provider,secret_ref,enabled,revision,seller_id,seller_name,created_at,disabled_at,checked_at,check_error,identity_fingerprint FROM marketplace_connections`,
		`DROP TABLE marketplace_connections`,
		`ALTER TABLE marketplace_connections_next RENAME TO marketplace_connections`,
		`CREATE TABLE marketplace_catalog (
 connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL CHECK(provider='OZON'),
 product_id INTEGER NOT NULL CHECK(product_id>0),offer_id TEXT NOT NULL,name TEXT NOT NULL,
 updated_at TEXT NOT NULL,last_seen_at TEXT NOT NULL,
 PRIMARY KEY(connection_id,product_id),UNIQUE(connection_id,workshop_id,provider,product_id),
 FOREIGN KEY(connection_id,workshop_id,provider) REFERENCES marketplace_connections(id,workshop_id,provider))`,
		`CREATE TABLE marketplace_catalog_skus (
 connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL,product_id INTEGER NOT NULL,sku INTEGER NOT NULL CHECK(sku>0),
 PRIMARY KEY(connection_id,sku),
 FOREIGN KEY(connection_id,workshop_id,provider,product_id) REFERENCES marketplace_catalog(connection_id,workshop_id,provider,product_id))`,
		`CREATE TABLE marketplace_source_runs (
 id INTEGER PRIMARY KEY,connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL CHECK(provider='OZON'),
 source TEXT NOT NULL CHECK(source IN ('CATALOG','OZON_SELLER_STOCK','OZON_FBO_AVAILABLE')),captured_at TEXT NOT NULL,
 status TEXT NOT NULL,info_json TEXT NOT NULL,
 UNIQUE(id,connection_id,workshop_id,provider,source),
 FOREIGN KEY(connection_id,workshop_id,provider) REFERENCES marketplace_connections(id,workshop_id,provider))`,
		`CREATE TABLE marketplace_stock_current (
 connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL,source TEXT NOT NULL,
 sku INTEGER NOT NULL CHECK(sku>0),warehouse_id INTEGER NOT NULL CHECK(warehouse_id>0),
 product_id INTEGER NOT NULL,offer_id TEXT NOT NULL,warehouse_name TEXT NOT NULL,quantity INTEGER NOT NULL CHECK(quantity>=0),
 captured_at TEXT NOT NULL,run_id INTEGER NOT NULL,
 PRIMARY KEY(connection_id,source,sku,warehouse_id),
 FOREIGN KEY(run_id,connection_id,workshop_id,provider,source) REFERENCES marketplace_source_runs(id,connection_id,workshop_id,provider,source))`,
		`CREATE TABLE marketplace_stock_history (
 run_id INTEGER NOT NULL,connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL,source TEXT NOT NULL,
 sku INTEGER NOT NULL CHECK(sku>0),warehouse_id INTEGER NOT NULL CHECK(warehouse_id>0),
 product_id INTEGER NOT NULL,offer_id TEXT NOT NULL,warehouse_name TEXT NOT NULL,quantity INTEGER NOT NULL CHECK(quantity>=0),captured_at TEXT NOT NULL,
 PRIMARY KEY(run_id,sku,warehouse_id),
 FOREIGN KEY(run_id,connection_id,workshop_id,provider,source) REFERENCES marketplace_source_runs(id,connection_id,workshop_id,provider,source))`,
		`CREATE TABLE integration_cooldowns (
 connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL, retry_at TEXT NOT NULL,
 PRIMARY KEY(connection_id,provider),
 FOREIGN KEY(connection_id,workshop_id,provider) REFERENCES marketplace_connections(id,workshop_id,provider))`,
		`CREATE TABLE integration_request_trace (
 id INTEGER PRIMARY KEY,connection_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,provider TEXT NOT NULL,timestamp TEXT NOT NULL,trace_json TEXT NOT NULL,
 FOREIGN KEY(connection_id,workshop_id,provider) REFERENCES marketplace_connections(id,workshop_id,provider))`,
		`INSERT INTO schema_migrations(number,name) VALUES(118,'ozon_stage_a')`,
	}
	for _, q := range queries {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
