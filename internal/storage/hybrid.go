package storage

import "database/sql"

func migrateHybrid(tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS hybrid_llm_runs(request_id TEXT PRIMARY KEY, user_id INTEGER NOT NULL, channel TEXT NOT NULL, metadata TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (unixepoch()))`,
		`INSERT OR IGNORE INTO schema_migrations(number,name) VALUES(121,'day30_hybrid_llm')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
