package storage

import (
	"path/filepath"
	"testing"
)

func TestTaskMigrationPreservesLegacy(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	statements := []string{
		`DROP TABLE task_transitions`,
		`DROP TABLE working_memory`,
		`CREATE TABLE working_memory(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,workshop_id INTEGER NOT NULL,task_id TEXT NOT NULL UNIQUE,task_type TEXT NOT NULL,state_json TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('active','waiting_input','completed','cancelled')),created_at TEXT,updated_at TEXT,completed_at TEXT)`,
		`INSERT INTO users(id,telegram_user_id,display_name) VALUES(1,100,'Test')`,
		`INSERT INTO workshops(id,name) VALUES(1,'Test')`,
		`INSERT INTO working_memory VALUES(8,1,1,'legacy-task','assembly','{"quantity":25}','active','2026-01-01','2026-01-02',NULL)`,
		`DELETE FROM schema_migrations WHERE number IN (104,105)`,
	}
	for _, q := range statements {
		if _, err = s.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	var id, version int
	var data, status, created string
	err = s.DB.QueryRow("SELECT id,fsm_version,state_json,status,created_at FROM working_memory WHERE task_id='legacy-task'").Scan(&id, &version, &data, &status, &created)
	if err != nil || id != 8 || version != 0 || data != `{"quantity":25}` || status != "active" || created != "2026-01-01" {
		t.Fatal(id, version, data, status, created, err)
	}
	var author, executor int64
	if err = s.DB.QueryRow("SELECT created_by_user_id,assigned_to_user_id FROM working_memory WHERE task_id='legacy-task'").Scan(&author, &executor); err != nil || author != 1 || executor != 1 {
		t.Fatal(author, executor, err)
	}
}
