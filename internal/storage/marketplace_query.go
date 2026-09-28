package storage

import "database/sql"

func migrateMarketplaceQuery(tx *sql.Tx) error {
	var n int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=119`).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	for _, q := range []string{
		`CREATE TABLE ozon_product_mappings(connection_id INTEGER NOT NULL CHECK(connection_id=2),workshop_id INTEGER NOT NULL,sku INTEGER NOT NULL CHECK(sku>0),product_id INTEGER NOT NULL REFERENCES products(id),PRIMARY KEY(connection_id,sku),FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`CREATE TRIGGER ozon_mapping_insert BEFORE INSERT ON ozon_product_mappings BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM products WHERE id=NEW.product_id AND workshop_id=NEW.workshop_id) OR NOT EXISTS(SELECT 1 FROM marketplace_catalog_skus WHERE connection_id=NEW.connection_id AND workshop_id=NEW.workshop_id AND sku=NEW.sku) THEN RAISE(ABORT,'mapping scope') END; END`,
		`CREATE TRIGGER ozon_mapping_update BEFORE UPDATE ON ozon_product_mappings BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM products WHERE id=NEW.product_id AND workshop_id=NEW.workshop_id) OR NOT EXISTS(SELECT 1 FROM marketplace_catalog_skus WHERE connection_id=NEW.connection_id AND workshop_id=NEW.workshop_id AND sku=NEW.sku) THEN RAISE(ABORT,'mapping scope') END; END`,
		`CREATE TABLE marketplace_query_traces(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL REFERENCES users(id),workshop_id INTEGER NOT NULL REFERENCES workshops(id),trace_json TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX marketplace_query_trace_scope ON marketplace_query_traces(user_id,workshop_id,id)`,
		`INSERT INTO schema_migrations(number,name) VALUES(119,'day20_marketplace_query')`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
