package storage

import "database/sql"

func migrateTaskCompletion(tx *sql.Tx) error {
	var n int
	if e := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE number=106").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	for _, q := range []string{
		`ALTER TABLE production_records ADD COLUMN task_id TEXT REFERENCES working_memory(task_id)`,
		`ALTER TABLE production_records ADD COLUMN composition_json TEXT`,
		`ALTER TABLE production_records ADD COLUMN created_by_user_id INTEGER REFERENCES users(id)`,
		`ALTER TABLE production_records ADD COLUMN assigned_to_user_id INTEGER REFERENCES users(id)`,
		`CREATE UNIQUE INDEX production_task_once ON production_records(task_id) WHERE task_id IS NOT NULL`,
		`CREATE TABLE task_completion_intents(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,chat_id INTEGER NOT NULL,session_id INTEGER NOT NULL,task_id TEXT NOT NULL,version INTEGER NOT NULL,plan_json TEXT NOT NULL,expires_at INTEGER NOT NULL,consumed INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO schema_migrations(number,name) VALUES(106,'task_completion_posting')`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
