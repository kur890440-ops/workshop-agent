package storage

import "database/sql"

func migrateStability(tx *sql.Tx) error {
	var n int
	if e := tx.QueryRow("SELECT count(*) FROM schema_migrations WHERE number=122").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	for _, q := range []string{
		`CREATE TABLE runtime_limits(parameter TEXT PRIMARY KEY,value INTEGER NOT NULL)`,
		`CREATE TABLE runtime_limit_audit(id INTEGER PRIMARY KEY,actor INTEGER NOT NULL,workshop_id INTEGER NOT NULL,parameter TEXT NOT NULL,old_value INTEGER NOT NULL,new_value INTEGER NOT NULL,created_at INTEGER NOT NULL)`,
		`ALTER TABLE web_messages ADD COLUMN workshop_id INTEGER REFERENCES workshops(id)`,
		`CREATE TABLE web_requests(request_id TEXT PRIMARY KEY,idempotency_key TEXT NOT NULL DEFAULT '',payload_hash TEXT NOT NULL DEFAULT '',session_id INTEGER NOT NULL REFERENCES web_sessions(id) ON DELETE CASCADE,user_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL REFERENCES workshops(id),status TEXT NOT NULL,error_code TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
		`CREATE UNIQUE INDEX web_request_idempotency ON web_requests(session_id,idempotency_key) WHERE idempotency_key<>''`,
		`CREATE INDEX web_request_session ON web_requests(session_id,created_at)`,
		`INSERT INTO schema_migrations(number,name) VALUES(122,'day30_stability')`} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
