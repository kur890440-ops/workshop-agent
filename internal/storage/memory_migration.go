package storage

import "database/sql"

const memoryVersion = 101

func migrateMemory(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=?`, memoryVersion).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := addColumn(tx, "conversation_sessions", "status", "TEXT NOT NULL DEFAULT 'closed'"); err != nil {
		return err
	}
	if err := addColumn(tx, "user_preferences", "version", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := addColumn(tx, "user_preferences", "updated_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	statements := []string{
		`CREATE UNIQUE INDEX conversation_scope ON conversation_sessions(id,user_id,workshop_id)`,
		`CREATE UNIQUE INDEX conversation_active ON conversation_sessions(user_id,workshop_id,telegram_chat_id) WHERE status='active'`,
		`CREATE TABLE conversation_messages(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL REFERENCES users(id),workshop_id INTEGER NOT NULL REFERENCES workshops(id),session_id INTEGER NOT NULL,role TEXT NOT NULL CHECK(role IN ('user','assistant')),content TEXT NOT NULL,token_count INTEGER NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(session_id,user_id,workshop_id) REFERENCES conversation_sessions(id,user_id,workshop_id))`,
		`CREATE INDEX message_scope ON conversation_messages(user_id,workshop_id,session_id,id)`,
		`CREATE TABLE working_memory(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL REFERENCES users(id),workshop_id INTEGER NOT NULL REFERENCES workshops(id),task_id TEXT NOT NULL UNIQUE,task_type TEXT NOT NULL,state_json TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('active','waiting_input','completed','cancelled')),created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,completed_at TEXT)`,
		`CREATE UNIQUE INDEX working_active ON working_memory(user_id,workshop_id) WHERE status IN ('active','waiting_input')`,
		`CREATE TABLE long_term_memory(id INTEGER PRIMARY KEY AUTOINCREMENT,memory_type TEXT NOT NULL,category TEXT NOT NULL,scope_type TEXT NOT NULL CHECK(scope_type IN ('user','workshop','product','process')),user_id INTEGER REFERENCES users(id),workshop_id INTEGER REFERENCES workshops(id),entity_type TEXT,entity_id INTEGER REFERENCES products(id),key TEXT NOT NULL,value_json TEXT NOT NULL,source_type TEXT NOT NULL,source_message_id INTEGER,created_by_user_id INTEGER NOT NULL REFERENCES users(id),created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,is_active INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,CHECK((scope_type='user' AND user_id IS NOT NULL AND workshop_id IS NULL AND entity_id IS NULL) OR (scope_type IN ('workshop','process') AND user_id IS NULL AND workshop_id IS NOT NULL AND entity_id IS NULL) OR (scope_type='product' AND user_id IS NULL AND workshop_id IS NOT NULL AND entity_id IS NOT NULL)))`,
		`CREATE UNIQUE INDEX memory_key ON long_term_memory(scope_type,COALESCE(user_id,0),COALESCE(workshop_id,0),COALESCE(entity_id,0),key)`,
		`CREATE INDEX memory_retrieval ON long_term_memory(scope_type,workshop_id,user_id,category,is_active)`,
		`CREATE TABLE memory_traces(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL REFERENCES users(id),workshop_id INTEGER NOT NULL REFERENCES workshops(id),session_id INTEGER NOT NULL,trace_json TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(session_id,user_id,workshop_id) REFERENCES conversation_sessions(id,user_id,workshop_id))`,
		`CREATE INDEX memory_trace_scope ON memory_traces(user_id,workshop_id,session_id,id)`,
		`INSERT INTO schema_migrations(number,name) VALUES(101,'day11_memory_layers')`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
