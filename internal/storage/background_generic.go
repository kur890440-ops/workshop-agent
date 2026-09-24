package storage

import (
	"database/sql"
	"strings"
)

// Rebuild the parent and its referencing tables together, preserving IDs and
// history with foreign_keys enabled throughout the migration transaction.
func migrateGenericBackground(tx *sql.Tx) error {
	var done int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=114`).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	_, err := tx.Exec(`CREATE TABLE background_jobs_v114(
 id INTEGER PRIMARY KEY, workshop_id INTEGER NOT NULL REFERENCES workshops(id),
 created_by_user_id INTEGER NOT NULL REFERENCES users(id), job_type TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('active','paused','completed','cancelled','failed')),
 schedule_type TEXT NOT NULL CHECK(schedule_type='DAILY_AT_TIME'), local_time TEXT NOT NULL,
 timezone TEXT NOT NULL, parameters_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(parameters_json)),
 notification_enabled INTEGER NOT NULL DEFAULT 1 CHECK(notification_enabled IN (0,1)),
 next_run_at INTEGER NOT NULL,last_run_at INTEGER NOT NULL DEFAULT 0,last_success_at INTEGER NOT NULL DEFAULT 0,
 last_result TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
 UNIQUE(workshop_id,job_type))`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO background_jobs_v114 SELECT id,workshop_id,created_by_user_id,job_type,status,'DAILY_AT_TIME',local_time,timezone,json_object('connection_id',connection_id),1,next_run_at,last_run_at,last_success_at,last_result,created_at,updated_at FROM background_jobs`)
	if err != nil {
		return err
	}
	children := []string{"background_job_runs", "wb_daily_snapshots", "wb_daily_current", "wb_daily_diffs"}
	for _, name := range children {
		var ddl string
		if err = tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&ddl); err != nil {
			return err
		}
		ddl = strings.Replace(ddl, "CREATE TABLE "+name, "CREATE TABLE "+name+"_v114", 1)
		ddl = strings.ReplaceAll(ddl, "REFERENCES background_jobs(", "REFERENCES background_jobs_v114(")
		ddl = strings.ReplaceAll(ddl, "REFERENCES background_job_runs(", "REFERENCES background_job_runs_v114(")
		ddl = strings.ReplaceAll(ddl, " DEFAULT 'WB_DAILY_SYNC'", "")
		if _, err = tx.Exec(ddl); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO ` + name + `_v114 SELECT * FROM ` + name); err != nil {
			return err
		}
	}
	for _, name := range []string{"wb_daily_diffs", "wb_daily_current", "wb_daily_snapshots", "background_job_runs", "background_jobs"} {
		if _, err = tx.Exec(`DROP TABLE ` + name); err != nil {
			return err
		}
	}
	for _, name := range append([]string{"background_jobs"}, children...) {
		if _, err = tx.Exec(`ALTER TABLE ` + name + `_v114 RENAME TO ` + name); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE UNIQUE INDEX background_one_running ON background_job_runs(job_id) WHERE status='running'`,
		`CREATE INDEX background_due ON background_jobs(status,next_run_at)`,
		`INSERT INTO schema_migrations(number,name) VALUES(114,'generic_background_jobs')`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
