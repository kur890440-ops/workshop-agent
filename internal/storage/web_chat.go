package storage

import "database/sql"

func migrateWebChat(tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS web_sessions(id INTEGER PRIMARY KEY AUTOINCREMENT,token_hash TEXT UNIQUE,user_id INTEGER NOT NULL REFERENCES users(id),telegram_user_id INTEGER NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,last_seen INTEGER NOT NULL,revoked_at INTEGER)`,
		`CREATE TABLE IF NOT EXISTS pending_web_logins(id TEXT PRIMARY KEY,code_hash TEXT NOT NULL,browser_hash TEXT NOT NULL UNIQUE,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,attempt_count INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'pending',session_id INTEGER REFERENCES web_sessions(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS web_messages(id INTEGER PRIMARY KEY AUTOINCREMENT,session_id INTEGER NOT NULL REFERENCES web_sessions(id) ON DELETE CASCADE,request_id TEXT NOT NULL DEFAULT '',role TEXT NOT NULL,text TEXT NOT NULL,buttons TEXT NOT NULL DEFAULT '[]',created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS web_messages_session ON web_messages(session_id,id)`,
		`CREATE INDEX IF NOT EXISTS web_sessions_expiry ON web_sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS pending_web_expiry ON pending_web_logins(expires_at)`,
		`INSERT OR IGNORE INTO schema_migrations(number,name) VALUES(120,'day30_web_chat')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
