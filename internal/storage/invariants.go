package storage

import "database/sql"

func migrateInvariants(tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS invariant_settings(workshop_id INTEGER NOT NULL REFERENCES workshops(id), key TEXT NOT NULL CHECK(key='requires_material_check_before_production'), is_active INTEGER NOT NULL CHECK(is_active IN(0,1)), version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(workshop_id,key))`,
		`CREATE TABLE IF NOT EXISTS invariant_traces(id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), workshop_id INTEGER NOT NULL REFERENCES workshops(id), result_json TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT OR IGNORE INTO schema_migrations(number,name) VALUES(107,'invariants')`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
