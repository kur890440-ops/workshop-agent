package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

// Migration 108 is the sole reader of the retired production_plans table.
// It preserves the complete source record in task metadata and never posts stock.
func migrateTaskOnly(tx *sql.Tx) error {
	var done int
	if e := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE number=108").Scan(&done); e != nil {
		return e
	}
	if done != 0 {
		return nil
	}
	// Unknown extensions may use cascading foreign keys. Refuse to drop their
	// parent until an explicit relationship migration has been supplied.
	var references int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master m, pragma_foreign_key_list(m.name) fk WHERE m.type='table' AND fk."table"='production_plans'`).Scan(&references); e != nil {
		return e
	}
	if references != 0 {
		return fmt.Errorf("legacy production_plans has external references; explicit relationship migration required")
	}
	var before int
	if e := tx.QueryRow("SELECT COUNT(*) FROM working_memory").Scan(&before); e != nil {
		return e
	}
	rows, e := tx.Query("SELECT * FROM production_plans ORDER BY id")
	if e != nil {
		return e
	}
	columns, e := rows.Columns()
	if e != nil {
		rows.Close()
		return e
	}
	var records []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptr := make([]any, len(columns))
		for i := range values {
			ptr[i] = &values[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			rows.Close()
			return e
		}
		record := map[string]any{}
		for i, k := range columns {
			if b, ok := values[i].([]byte); ok {
				values[i] = string(b)
			}
			record[k] = values[i]
		}
		records = append(records, record)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Replace assignment triggers before changing task_type or inserting migrated tasks.
	for _, q := range []string{
		`DROP TRIGGER IF EXISTS working_assignment_defaults`,
		`DROP TRIGGER IF EXISTS working_assignment_required`,
		`UPDATE working_memory SET task_type='production' WHERE task_type='production_plan'`,
		`UPDATE working_memory SET current_step='confirm_task' WHERE current_step='confirm_plan'`,
		`UPDATE working_memory SET expected_action='confirm_task' WHERE expected_action='confirm_plan'`,
		`UPDATE long_term_memory SET category='production' WHERE category='production_plan'`,
		`ALTER TABLE task_completion_intents RENAME COLUMN plan_json TO calculation_json`,
		`UPDATE task_completion_intents SET expires_at=0 WHERE consumed=0`,
		`CREATE TRIGGER working_assignment_defaults AFTER INSERT ON working_memory WHEN NEW.task_type IN ('assembly','production') BEGIN UPDATE working_memory SET created_by_user_id=COALESCE(NEW.created_by_user_id,NEW.user_id),assigned_to_user_id=COALESCE(NEW.assigned_to_user_id,NEW.user_id) WHERE id=NEW.id; END`,
		`CREATE TRIGGER working_assignment_required BEFORE UPDATE OF assigned_to_user_id,created_by_user_id ON working_memory WHEN NEW.task_type IN ('assembly','production') AND (NEW.assigned_to_user_id IS NULL OR NEW.created_by_user_id IS NULL) BEGIN SELECT RAISE(ABORT,'task assignment required'); END`,
	} {
		if _, e = tx.Exec(q); e != nil {
			return e
		}
	}
	for _, r := range records {
		id, ok := r["id"].(int64)
		if !ok {
			return fmt.Errorf("legacy record without integer ID")
		}
		w, ok := r["workshop_id"].(int64)
		if !ok {
			return fmt.Errorf("legacy record %d has no workshop", id)
		}
		p, ok := r["product_id"].(int64)
		if !ok {
			return fmt.Errorf("legacy record %d has no product", id)
		}
		qty, ok := r["planned_quantity"].(float64)
		if !ok {
			if n, valid := r["planned_quantity"].(int64); valid {
				qty = float64(n)
				ok = true
			}
		}
		if !ok || qty < 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
			return fmt.Errorf("legacy record %d invalid quantity", id)
		}
		var name string
		if e = tx.QueryRow("SELECT name FROM products WHERE id=? AND workshop_id=?", p, w).Scan(&name); e != nil {
			return fmt.Errorf("legacy record %d product scope: %w", id, e)
		}
		user, _ := r["user_id"].(int64)
		if user == 0 {
			user, _ = r["owner_user_id"].(int64)
		}
		if user == 0 {
			var count int
			if e = tx.QueryRow("SELECT COUNT(*),COALESCE(MIN(user_id),0) FROM workshop_members WHERE workshop_id=? AND role='OWNER' AND is_active=1", w).Scan(&count, &user); e != nil {
				return e
			}
			if count != 1 {
				return fmt.Errorf("cannot infer owner of legacy record %d; expected one workshop OWNER", id)
			}
		}
		raw, e := json.Marshal(r)
		if e != nil {
			return e
		}
		status, _ := r["status"].(string)
		phase, step, expected, kind := "planning", "confirm_task", "confirm_task", "USER_CONFIRMATION"
		switch status {
		case "active", "waiting_input", "paused":
		case "completed", "done":
			status = "completed"
			phase = "done"
			step = "completed"
			expected = "none"
			kind = "NONE"
		case "cancelled", "canceled", "failed":
			if status == "canceled" {
				status = "cancelled"
			}
			expected = "none"
			kind = "NONE"
		default:
			status = "paused"
		}
		state, e := json.Marshal(map[string]any{"product_id": p, "product_name": name, "quantity": qty, "parameters": map[string]string{"legacy_source_record": string(raw), "migration_note": "Imported without production posting; original fields retained."}})
		if e != nil {
			return e
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("task-migration-108:%d:%d", w, id)))
		taskID := hex.EncodeToString(hash[:16])
		created, _ := r["created_at"].(string)
		updated, _ := r["updated_at"].(string)
		if _, e = tx.Exec(`INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status,phase,current_step,expected_action,expected_action_type,fsm_version,created_by_user_id,assigned_to_user_id,created_at,updated_at,started_at,completed_at) VALUES(?,?,?,'production',?,?,?,?,?,?,1,?,?,COALESCE(NULLIF(?,''),CURRENT_TIMESTAMP),COALESCE(NULLIF(?,''),CURRENT_TIMESTAMP),?,?)`, user, w, taskID, string(state), status, phase, step, expected, kind, user, user, created, updated, r["start_date"], r["completed_at"]); e != nil {
			return fmt.Errorf("import legacy record %d: %w", id, e)
		}
		if _, e = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,action,details,event_type,metadata_json) VALUES(?,'task',0,?,'migrate','Task migration 108','TASK_CREATED',?)`, w, user, fmt.Sprintf(`{"task_id":%q,"source":%s}`, taskID, raw)); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO task_transitions(task_id,actor_user_id,from_phase,from_step,to_phase,to_step,action,from_status,to_status,metadata_json) VALUES(?,?,'','',?,?,'migrated','',?,?)`, taskID, user, phase, step, status, string(raw)); e != nil {
			return e
		}
	}
	var after int
	if e = tx.QueryRow("SELECT COUNT(*) FROM working_memory").Scan(&after); e != nil {
		return e
	}
	if after != before+len(records) {
		return fmt.Errorf("task migration count mismatch")
	}
	// Existing references cannot be silently orphaned: DROP/FK check aborts the transaction.
	if _, e = tx.Exec("DROP TABLE production_plans"); e != nil {
		return e
	}
	check, e := tx.Query("PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	invalid := check.Next()
	e = check.Err()
	check.Close()
	if e != nil {
		return e
	}
	if invalid {
		return fmt.Errorf("task migration foreign key check failed")
	}
	_, e = tx.Exec("INSERT INTO schema_migrations(number,name) VALUES(108,'task_only')")
	return e
}
