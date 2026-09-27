package storage

import "database/sql"

func migrateWBRateAudit(tx *sql.Tx) error {
	var done int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=115`).Scan(&done); e != nil {
		return e
	}
	if done != 0 {
		return nil
	}
	for _, q := range []string{
		`ALTER TABLE marketplace_connections ADD COLUMN identity_fingerprint TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE wb_rate_observations(rate_group TEXT PRIMARY KEY,received_at_ms INTEGER NOT NULL,endpoint TEXT NOT NULL,http_status INTEGER NOT NULL,remaining INTEGER,retry_seconds INTEGER,reset_seconds INTEGER,limit_value INTEGER)`,
		`CREATE TABLE wb_request_trace(id INTEGER PRIMARY KEY,workshop_id INTEGER NOT NULL,record_json TEXT NOT NULL)`,
		`ALTER TABLE background_job_runs ADD COLUMN retry_not_before TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO schema_migrations(number,name) VALUES(115,'wb_rate_identity_trace')`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
