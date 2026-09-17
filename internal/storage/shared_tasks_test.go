package storage

import (
	"path/filepath"
	"testing"
)

func TestSharedMigrationPreservesAllStatesAndPrivateOwnership(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "migration.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, q := range []string{
		`DROP TABLE task_transitions`, `DROP TABLE working_memory`,
		`CREATE TABLE working_memory(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,task_id TEXT UNIQUE,task_type TEXT,state_json TEXT,status TEXT,version INTEGER DEFAULT 1,fsm_version INTEGER DEFAULT 0)`,
		`CREATE UNIQUE INDEX working_active ON working_memory(user_id,workshop_id) WHERE status IN ('active','waiting_input')`,
		`INSERT INTO users(id,telegram_user_id,display_name) VALUES(1,99,'Former member')`,
		`INSERT INTO workshops(id,name) VALUES(1,'A')`,
		`INSERT INTO working_memory(id,user_id,workshop_id,task_id,task_type,state_json,status) VALUES(1,1,1,'plan','assembly','{"quantity":5,"parameters":{"secret":"private"}}','active'),(2,1,1,'paused','production_plan','{}','paused'),(3,1,1,'done','assembly','{}','completed'),(4,1,1,'cancelled','assembly','{}','cancelled'),(5,1,1,'failed','assembly','{}','failed'),(6,1,1,'personal','note','{}','paused')`,
		`DELETE FROM schema_migrations WHERE number=105`,
	} {
		if _, e = s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = migrateSharedTasks(tx); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	tx, e = s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = migrateSharedTasks(tx); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM working_memory WHERE task_type IN ('assembly','production_plan') AND user_id=1 AND created_by_user_id=1 AND assigned_to_user_id=1").Scan(&n); e != nil || n != 5 {
		t.Fatal(n, e)
	}
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM working_memory WHERE task_id='personal' AND user_id=1 AND created_by_user_id IS NULL AND assigned_to_user_id IS NULL").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	var raw string
	if e = s.DB.QueryRow("SELECT state_json FROM working_memory WHERE task_id='plan'").Scan(&raw); e != nil || raw != `{"quantity":5,"parameters":{"secret":"private"}}` {
		t.Fatal(raw, e)
	}
}
