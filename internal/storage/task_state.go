package storage

import "database/sql"

func migrateTaskState(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE number=104").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	statements := []string{
		`CREATE TABLE working_memory_v104(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL REFERENCES users(id),workshop_id INTEGER NOT NULL REFERENCES workshops(id),task_id TEXT NOT NULL UNIQUE,task_type TEXT NOT NULL,state_json TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('active','waiting_input','paused','completed','cancelled','failed')),created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,completed_at TEXT,phase TEXT NOT NULL DEFAULT 'planning' CHECK(phase IN ('planning','execution','validation','done')),current_step TEXT NOT NULL DEFAULT 'legacy',expected_action TEXT NOT NULL DEFAULT 'legacy',expected_action_type TEXT NOT NULL DEFAULT 'USER_INPUT',started_at TEXT,paused_at TEXT,version INTEGER NOT NULL DEFAULT 1,fsm_version INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO working_memory_v104(id,user_id,workshop_id,task_id,task_type,state_json,status,created_at,updated_at,completed_at,phase) SELECT id,user_id,workshop_id,task_id,task_type,state_json,status,created_at,updated_at,completed_at,CASE WHEN status='completed' THEN 'done' ELSE 'planning' END FROM working_memory`,
		`DROP TABLE working_memory`, `ALTER TABLE working_memory_v104 RENAME TO working_memory`,
		`CREATE UNIQUE INDEX working_active ON working_memory(user_id,workshop_id) WHERE status IN ('active','waiting_input')`,
		`CREATE TABLE task_transitions(id INTEGER PRIMARY KEY AUTOINCREMENT,task_id TEXT NOT NULL REFERENCES working_memory(task_id),actor_user_id INTEGER NOT NULL REFERENCES users(id),from_phase TEXT NOT NULL,from_step TEXT NOT NULL,to_phase TEXT NOT NULL,to_step TEXT NOT NULL,action TEXT NOT NULL,from_status TEXT NOT NULL,to_status TEXT NOT NULL,metadata_json TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX task_transition_history ON task_transitions(task_id,id)`,
		`INSERT INTO schema_migrations(number,name) VALUES(104,'day13_task_state_machine')`,
	}
	for _, q := range statements {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
