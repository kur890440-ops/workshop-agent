package storage

import "database/sql"

// Existing approvals are recovered from immutable transition history, never
// inferred from dialogue or an execution phase alone.
func migrateControlledTransitions(tx *sql.Tx) error {
	var n int
	if e := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE number=109").Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return nil
	}
	for _, q := range []string{
		`UPDATE working_memory SET state_json=json_set(state_json,'$.parameters_approved',json('true')) WHERE fsm_version=1 AND phase IN ('execution','validation','done') AND EXISTS(SELECT 1 FROM task_transitions h WHERE h.task_id=working_memory.task_id AND h.action IN ('confirm_task','confirm_plan') AND h.to_phase='execution')`,
		`UPDATE working_memory SET state_json=json_set(state_json,'$.validation_result','passed') WHERE fsm_version=1 AND current_step='confirm_completion' AND EXISTS(SELECT 1 FROM task_transitions h WHERE h.task_id=working_memory.task_id AND h.action='verify_result' AND h.to_step='confirm_completion')`,
		`UPDATE task_completion_intents SET expires_at=0 WHERE consumed=0`,
		`INSERT INTO schema_migrations(number,name) VALUES(109,'controlled_task_transitions')`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}
