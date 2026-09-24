package storage

import "database/sql"

// One-time removal of the obsolete application interval, never server-labelled waits.
func migrateMarketplacePacing(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=112`).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	for _, q := range []string{
		`DELETE FROM marketplace_cooldowns WHERE source='local_interval'`,
		`UPDATE marketplace_sync SET retry_at='',retry_source='',rate_operation='' WHERE retry_source='local_interval'`,
		`INSERT INTO schema_migrations(number,name) VALUES(112,'wb_remove_legacy_local_interval')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func migrateMarketplaceCooldown(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=111`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, q := range []string{
		`CREATE TABLE marketplace_cooldowns (rate_group TEXT PRIMARY KEY CHECK(rate_group IN ('common','analytics','content','marketplace')), retry_at_ms INTEGER NOT NULL, source TEXT NOT NULL CHECK(source IN ('wb_retry','wb_reset','local_backoff','local_interval')))`,
		`ALTER TABLE marketplace_sync ADD COLUMN blocked_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE marketplace_sync ADD COLUMN retry_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE marketplace_sync ADD COLUMN retry_source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE marketplace_sync ADD COLUMN rate_operation TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO schema_migrations(number,name) VALUES(111,'wb_cooldown')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Migration 110 only creates marketplace-owned tables; no production data is changed.
func migrateMarketplace(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=110`).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	for _, q := range []string{
		`CREATE TABLE marketplace_connections (
		 id INTEGER PRIMARY KEY CHECK(id=1), workshop_id INTEGER NOT NULL REFERENCES workshops(id),
		 provider TEXT NOT NULL DEFAULT 'wildberries' CHECK(provider='wildberries'),
		 secret_ref TEXT NOT NULL DEFAULT 'WB_API_TOKEN' CHECK(secret_ref='WB_API_TOKEN'),
		 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), revision INTEGER NOT NULL DEFAULT 1,
		 seller_id TEXT NOT NULL DEFAULT '', seller_name TEXT NOT NULL DEFAULT '',
		 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, disabled_at TEXT NOT NULL DEFAULT '',
		 checked_at TEXT NOT NULL DEFAULT '', check_error TEXT NOT NULL DEFAULT '', UNIQUE(id,workshop_id))`,
		`CREATE TABLE marketplace_cards (
		 connection_id INTEGER NOT NULL, workshop_id INTEGER NOT NULL, nm_id INTEGER NOT NULL CHECK(nm_id>0),
		 title TEXT NOT NULL, vendor_code TEXT NOT NULL, source_updated_at TEXT NOT NULL, fetched_at TEXT NOT NULL, present INTEGER NOT NULL DEFAULT 1,
		 PRIMARY KEY(connection_id,nm_id), UNIQUE(connection_id,workshop_id,nm_id),
		 FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`CREATE TABLE marketplace_variants (
		 connection_id INTEGER NOT NULL, workshop_id INTEGER NOT NULL, nm_id INTEGER NOT NULL, chrt_id INTEGER NOT NULL CHECK(chrt_id>0), size TEXT NOT NULL, present INTEGER NOT NULL DEFAULT 1,
		 PRIMARY KEY(connection_id,chrt_id), UNIQUE(connection_id,workshop_id,nm_id,chrt_id),
		 FOREIGN KEY(connection_id,workshop_id,nm_id) REFERENCES marketplace_cards(connection_id,workshop_id,nm_id))`,
		`CREATE TABLE marketplace_barcodes (
		 connection_id INTEGER NOT NULL, chrt_id INTEGER NOT NULL, barcode TEXT NOT NULL,
		 PRIMARY KEY(connection_id,chrt_id,barcode), FOREIGN KEY(connection_id,chrt_id) REFERENCES marketplace_variants(connection_id,chrt_id))`,
		`CREATE TABLE marketplace_mappings (
		 connection_id INTEGER NOT NULL, workshop_id INTEGER NOT NULL, product_id INTEGER NOT NULL REFERENCES products(id),
		 nm_id INTEGER NOT NULL, chrt_id INTEGER NOT NULL, barcode TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
		 PRIMARY KEY(connection_id,chrt_id,barcode),
		 FOREIGN KEY(connection_id,workshop_id,nm_id,chrt_id) REFERENCES marketplace_variants(connection_id,workshop_id,nm_id,chrt_id))`,
		`CREATE TRIGGER marketplace_mapping_scope_insert BEFORE INSERT ON marketplace_mappings BEGIN
		 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM products WHERE id=NEW.product_id AND workshop_id=NEW.workshop_id) THEN RAISE(ABORT,'marketplace product scope') END;
		 SELECT CASE WHEN NEW.barcode<>'' AND NOT EXISTS(SELECT 1 FROM marketplace_barcodes WHERE connection_id=NEW.connection_id AND chrt_id=NEW.chrt_id AND barcode=NEW.barcode) THEN RAISE(ABORT,'marketplace barcode scope') END; END`,
		`CREATE TRIGGER marketplace_mapping_scope_update BEFORE UPDATE OF product_id,workshop_id,connection_id,nm_id,chrt_id,barcode ON marketplace_mappings BEGIN
		 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM products WHERE id=NEW.product_id AND workshop_id=NEW.workshop_id) THEN RAISE(ABORT,'marketplace product scope') END;
		 SELECT CASE WHEN NEW.barcode<>'' AND NOT EXISTS(SELECT 1 FROM marketplace_barcodes WHERE connection_id=NEW.connection_id AND chrt_id=NEW.chrt_id AND barcode=NEW.barcode) THEN RAISE(ABORT,'marketplace barcode scope') END; END`,
		`CREATE TABLE marketplace_stocks (
		 connection_id INTEGER NOT NULL, workshop_id INTEGER NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('seller_stocks','wb_stocks')),
		 warehouse_id INTEGER NOT NULL CHECK(warehouse_id>0), warehouse_name TEXT NOT NULL, nm_id INTEGER NOT NULL CHECK(nm_id>0), chrt_id INTEGER NOT NULL CHECK(chrt_id>0),
		 quantity INTEGER NOT NULL CHECK(quantity>=0), fetched_at TEXT NOT NULL,
		 PRIMARY KEY(connection_id,kind,warehouse_id,chrt_id),
		 FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`CREATE TABLE marketplace_orders (
		 connection_id INTEGER NOT NULL, workshop_id INTEGER NOT NULL, order_id INTEGER NOT NULL CHECK(order_id>0),
		 nm_id INTEGER NOT NULL, chrt_id INTEGER NOT NULL, warehouse_id INTEGER NOT NULL,
		 article TEXT NOT NULL, created_at TEXT NOT NULL, supplier_status TEXT NOT NULL, wb_status TEXT NOT NULL, fetched_at TEXT NOT NULL,
		 PRIMARY KEY(connection_id,order_id), FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`CREATE TABLE marketplace_sync (
		 connection_id INTEGER NOT NULL, workshop_id INTEGER NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('check','catalog','seller_stocks','wb_stocks','orders')),
		 state TEXT NOT NULL CHECK(state IN ('running','succeeded','partial','failed','cancelled')), started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '',
		 last_success TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', row_count INTEGER NOT NULL DEFAULT 0,
		 PRIMARY KEY(connection_id,kind), FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`INSERT INTO schema_migrations(number,name) VALUES(110,'wildberries_readonly')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
