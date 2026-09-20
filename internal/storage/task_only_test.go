package storage

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Reconstruct schema 107; historical names here are migration fixtures only.
func taskOnlyFixture(t *testing.T) *Store {
	t.Helper()
	s, e := New(filepath.Join(t.TempDir(), "migration.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	for _, q := range []string{
		`DELETE FROM schema_migrations WHERE number=108`,
		`ALTER TABLE task_completion_intents RENAME COLUMN calculation_json TO plan_json`,
		`CREATE TABLE production_plans(id INTEGER PRIMARY KEY,workshop_id INTEGER NOT NULL,product_id INTEGER NOT NULL,planned_quantity REAL NOT NULL,start_date TEXT,due_date TEXT,status TEXT NOT NULL,notes TEXT,created_at TEXT,updated_at TEXT)`,
		`INSERT INTO users(id,telegram_user_id,display_name) VALUES(1,101,'Owner')`,
		`INSERT INTO workshops(id,name) VALUES(1,'Workshop')`,
		`INSERT INTO workshop_members(workshop_id,user_id,role,is_active) VALUES(1,1,'OWNER',1)`,
		`INSERT INTO products(id,workshop_id,name,product_type,current_stock) VALUES(1,1,'Deflector','finished',12)`,
		`INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status,phase,current_step,expected_action,fsm_version) VALUES(1,1,'existing','production_plan','{"quantity":5}','active','planning','confirm_plan','confirm_plan',1)`,
		`INSERT INTO production_plans VALUES(42,1,1,25,'2026-01-02','2026-01-05','paused','preserve notes','2026-01-01','2026-01-03')`,
	} {
		if _, e = s.DB.Exec(q); e != nil {
			t.Fatal(q, e)
		}
	}
	return s
}

func TestTaskOnlyMigrationPreservesDataAndIsIdempotent(t *testing.T) {
	s := taskOnlyFixture(t)
	if e := s.Migrate(); e != nil {
		t.Fatal(e)
	}
	var id, kind, step, expected, data, created, status string
	var user, workshop, author, assignee int64
	e := s.DB.QueryRow(`SELECT task_id,task_type,current_step,expected_action,state_json,created_at,status,user_id,workshop_id,created_by_user_id,assigned_to_user_id FROM working_memory WHERE task_id!='existing'`).Scan(&id, &kind, &step, &expected, &data, &created, &status, &user, &workshop, &author, &assignee)
	if e != nil || kind != "production" || step != "confirm_task" || expected != "confirm_task" || created != "2026-01-01" || status != "paused" || user != 1 || workshop != 1 || author != 1 || assignee != 1 {
		t.Fatal(e, id, kind, step, created, status, user, workshop, author, assignee)
	}
	var state struct {
		Quantity   float64           `json:"quantity"`
		Parameters map[string]string `json:"parameters"`
	}
	if e = json.Unmarshal([]byte(data), &state); e != nil {
		t.Fatal(e)
	}
	var source map[string]any
	if e = json.Unmarshal([]byte(state.Parameters["legacy_source_record"]), &source); e != nil || state.Quantity != 25 || source["notes"] != "preserve notes" || source["due_date"] != "2026-01-05" || source["id"] != float64(42) {
		t.Fatal(e, state, source)
	}
	for i := 0; i < 2; i++ {
		if e = s.Migrate(); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM working_memory`: 2,
		`SELECT COUNT(*) FROM working_memory WHERE task_id='existing' AND task_type='production' AND current_step='confirm_task' AND expected_action='confirm_task' AND status='active' AND state_json='{"quantity":5}'`: 1,
		`SELECT COUNT(*) FROM sqlite_master WHERE name='production_plans'`: 0,
		`SELECT COUNT(*) FROM production_records`:                          0,
		`SELECT COUNT(*) FROM inventory_movements`:                         0,
		`SELECT COUNT(*) FROM task_transitions WHERE action='migrated'`:    1,
		`SELECT COUNT(*) FROM audit_logs WHERE event_type='TASK_CREATED'`:  1,
	} {
		if e = s.DB.QueryRow(q).Scan(&n); e != nil || n != want {
			t.Fatal(q, n, want, e)
		}
	}
	var stock float64
	if e = s.DB.QueryRow(`SELECT current_stock FROM products WHERE id=1`).Scan(&stock); e != nil || stock != 12 {
		t.Fatal(stock, e)
	}
	var stable string
	if e = s.DB.QueryRow(`SELECT task_id FROM working_memory WHERE task_id!='existing'`).Scan(&stable); e != nil || stable != id {
		t.Fatal(stable, id, e)
	}
	backups, e := filepath.Glob(s.Path + ".backup-*.db")
	if e != nil || len(backups) != 1 {
		t.Fatal(backups, e)
	}
	db, e := sql.Open("sqlite", backups[0])
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.QueryRow(`SELECT COUNT(*) FROM production_plans`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestTaskOnlyMigrationRollsBack(t *testing.T) {
	for _, scenario := range []string{"ambiguous_owner", "cross_workshop_product", "external_cascade", "audit_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s := taskOnlyFixture(t)
			statements := map[string][]string{
				"ambiguous_owner":        {`INSERT INTO users(id,telegram_user_id,display_name) VALUES(2,102,'Second')`, `INSERT INTO workshop_members(workshop_id,user_id,role,is_active) VALUES(1,2,'OWNER',1)`},
				"cross_workshop_product": {`INSERT INTO workshops(id,name) VALUES(2,'Other')`, `UPDATE products SET workshop_id=2 WHERE id=1`},
				"external_cascade":       {`CREATE TABLE extension(id INTEGER PRIMARY KEY,source_id INTEGER REFERENCES production_plans(id) ON DELETE CASCADE)`, `INSERT INTO extension VALUES(1,42)`},
				"audit_failure":          {`CREATE TRIGGER fail_migration BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'test rollback'); END`},
			}
			for _, q := range statements[scenario] {
				if _, e := s.DB.Exec(q); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.Migrate(); e == nil {
				t.Fatal("migration unexpectedly succeeded")
			}
			for q, want := range map[string]int{
				`SELECT COUNT(*) FROM production_plans`: 1,
				`SELECT COUNT(*) FROM working_memory`:   1,
				`SELECT COUNT(*) FROM working_memory WHERE task_type='production_plan' AND current_step='confirm_plan'`: 1,
				`SELECT COUNT(*) FROM schema_migrations WHERE number=108`:                                               0,
			} {
				var n int
				if e := s.DB.QueryRow(q).Scan(&n); e != nil || n != want {
					t.Fatal(q, n, e)
				}
			}
			if scenario == "external_cascade" {
				var n int
				if e := s.DB.QueryRow(`SELECT COUNT(*) FROM extension`).Scan(&n); e != nil || n != 1 {
					t.Fatal(n, e)
				}
			}
		})
	}
}
