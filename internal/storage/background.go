package storage

import "database/sql"

func migrateBackground(tx *sql.Tx) error {
	var done int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=113`).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	for _, q := range []string{
		`ALTER TABLE workshop_settings ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Europe/Moscow'`,
		`ALTER TABLE workshop_settings ADD COLUMN wb_low_stock_threshold INTEGER NOT NULL DEFAULT 5 CHECK(wb_low_stock_threshold>=0)`,
		`CREATE TABLE marketplace_cooldowns_new(rate_group TEXT PRIMARY KEY CHECK(rate_group IN ('common','analytics','content','marketplace','prices')),retry_at_ms INTEGER NOT NULL,source TEXT NOT NULL CHECK(source IN ('wb_retry','wb_reset','local_backoff','local_interval')))`,
		`INSERT INTO marketplace_cooldowns_new SELECT * FROM marketplace_cooldowns`,
		`DROP TABLE marketplace_cooldowns`, `ALTER TABLE marketplace_cooldowns_new RENAME TO marketplace_cooldowns`,
		`CREATE TABLE background_jobs(id INTEGER PRIMARY KEY,workshop_id INTEGER NOT NULL REFERENCES workshops(id),connection_id INTEGER NOT NULL,created_by_user_id INTEGER NOT NULL REFERENCES users(id),job_type TEXT NOT NULL CHECK(job_type='WB_DAILY_SYNC'),status TEXT NOT NULL CHECK(status IN ('active','paused','completed','cancelled','failed')),schedule_type TEXT NOT NULL CHECK(schedule_type='DAILY'),local_time TEXT NOT NULL DEFAULT '08:00',timezone TEXT NOT NULL,parameters_json TEXT NOT NULL DEFAULT '{}' CHECK(parameters_json='{}'),next_run_at INTEGER NOT NULL,last_run_at INTEGER NOT NULL DEFAULT 0,last_success_at INTEGER NOT NULL DEFAULT 0,last_result TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,UNIQUE(workshop_id,job_type),FOREIGN KEY(connection_id,workshop_id) REFERENCES marketplace_connections(id,workshop_id))`,
		`CREATE TABLE background_job_runs(id INTEGER PRIMARY KEY,job_id INTEGER NOT NULL REFERENCES background_jobs(id),workshop_id INTEGER NOT NULL,job_type TEXT NOT NULL DEFAULT 'WB_DAILY_SYNC',local_date TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('running','success','partial_success','failed')),started_at INTEGER NOT NULL,finished_at INTEGER NOT NULL DEFAULT 0,duration_ms INTEGER NOT NULL DEFAULT 0,result_json TEXT NOT NULL DEFAULT '{}',aggregate_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',error_message TEXT NOT NULL DEFAULT '',lease_owner TEXT NOT NULL,lease_until INTEGER NOT NULL,notification_state TEXT NOT NULL DEFAULT 'pending',UNIQUE(workshop_id,job_type,local_date))`,
		`CREATE UNIQUE INDEX background_one_running ON background_job_runs(job_id) WHERE status='running'`,
		`CREATE TABLE wb_daily_snapshots(run_id INTEGER NOT NULL REFERENCES background_job_runs(id),workshop_id INTEGER NOT NULL,connection_id INTEGER NOT NULL,source_tool TEXT NOT NULL CHECK(source_tool IN ('wb_get_prices','wb_get_wb_stocks')),item_key TEXT NOT NULL,nm_id INTEGER NOT NULL,captured_at TEXT NOT NULL,data_json TEXT NOT NULL,PRIMARY KEY(run_id,source_tool,item_key))`,
		`CREATE TABLE wb_daily_current(workshop_id INTEGER NOT NULL,connection_id INTEGER NOT NULL,source_tool TEXT NOT NULL CHECK(source_tool IN ('wb_get_prices','wb_get_wb_stocks')),item_key TEXT NOT NULL,nm_id INTEGER NOT NULL,run_id INTEGER NOT NULL REFERENCES background_job_runs(id),captured_at TEXT NOT NULL,data_json TEXT NOT NULL,PRIMARY KEY(workshop_id,connection_id,source_tool,item_key))`,
		`CREATE TABLE wb_daily_diffs(run_id INTEGER NOT NULL REFERENCES background_job_runs(id),source_tool TEXT NOT NULL,item_key TEXT NOT NULL,nm_id INTEGER NOT NULL,old_json TEXT NOT NULL,new_json TEXT NOT NULL,delta_json TEXT NOT NULL,PRIMARY KEY(run_id,source_tool,item_key))`,
		`INSERT INTO schema_migrations(number,name) VALUES(113,'wb_daily_background_jobs')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
