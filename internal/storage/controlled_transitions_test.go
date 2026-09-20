package storage

import (
	"path/filepath"
	"testing"
)

func TestDay15MigrationUsesHistoryNotPhase(t *testing.T) {
	s, e := New(filepath.Join(t.TempDir(), "day15.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, q := range []string{
		`DELETE FROM schema_migrations WHERE number=109`,
		`INSERT INTO users(id,telegram_user_id,display_name) VALUES(1,1515,'Test')`,
		`INSERT INTO workshops(id,name) VALUES(1,'Test')`,
		`INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status,phase,current_step,fsm_version) VALUES(1,1,'approved','production','{"quantity":5}','paused','execution','record_result',1),(1,1,'unapproved','production','{"quantity":5}','paused','execution','record_result',1)`,
		`INSERT INTO task_transitions(task_id,actor_user_id,from_phase,from_step,to_phase,to_step,action,from_status,to_status,metadata_json) VALUES('approved',1,'planning','confirm_task','execution','start_production','confirm_task','active','active','{}')`,
	} {
		if _, e = s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		if e = s.Migrate(); e != nil {
			t.Fatal(e)
		}
	}
	var approved, unapproved int
	e = s.DB.QueryRow(`SELECT (SELECT COALESCE(json_extract(state_json,'$.parameters_approved'),0) FROM working_memory WHERE task_id='approved'),(SELECT COALESCE(json_extract(state_json,'$.parameters_approved'),0) FROM working_memory WHERE task_id='unapproved')`).Scan(&approved, &unapproved)
	if e != nil || approved != 1 || unapproved != 0 {
		t.Fatal(approved, unapproved, e)
	}
	var n int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM working_memory`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	backups, e := filepath.Glob(s.Path + ".backup-*.db")
	if e != nil || len(backups) != 1 {
		t.Fatal(backups, e)
	}
}
