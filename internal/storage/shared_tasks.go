package storage

import "database/sql"

func migrateSharedTasks(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE number=105").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, q := range []string{
		`ALTER TABLE working_memory ADD COLUMN created_by_user_id INTEGER REFERENCES users(id)`,
		`ALTER TABLE working_memory ADD COLUMN assigned_to_user_id INTEGER REFERENCES users(id)`,
		`UPDATE working_memory SET created_by_user_id=user_id,assigned_to_user_id=user_id WHERE task_type IN ('assembly','production_plan')`,
		`DROP INDEX working_active`,
		`CREATE UNIQUE INDEX working_active ON working_memory(COALESCE(assigned_to_user_id,user_id),workshop_id) WHERE status IN ('active','waiting_input')`,
		`CREATE INDEX workshop_tasks ON working_memory(workshop_id,task_type,status)`,
		`CREATE TRIGGER working_assignment_defaults AFTER INSERT ON working_memory WHEN NEW.task_type IN ('assembly','production_plan') BEGIN UPDATE working_memory SET created_by_user_id=COALESCE(NEW.created_by_user_id,NEW.user_id),assigned_to_user_id=COALESCE(NEW.assigned_to_user_id,NEW.user_id) WHERE id=NEW.id; END`,
		`CREATE TRIGGER working_assignment_required BEFORE UPDATE OF assigned_to_user_id,created_by_user_id ON working_memory WHEN NEW.task_type IN ('assembly','production_plan') AND (NEW.assigned_to_user_id IS NULL OR NEW.created_by_user_id IS NULL) BEGIN SELECT RAISE(ABORT,'task assignment required'); END`,
		`INSERT INTO schema_migrations(number,name) VALUES(105,'workshop_shared_tasks')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
