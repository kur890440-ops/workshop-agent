package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration114Preserves113JobsRunsAndSnapshots(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`PRAGMA foreign_keys=ON`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE schema_migrations(number INTEGER PRIMARY KEY,name TEXT)`,
		`CREATE TABLE users(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE workshops(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE workshop_settings(workshop_id INTEGER PRIMARY KEY)`,
		`CREATE TABLE marketplace_connections(id INTEGER,workshop_id INTEGER,UNIQUE(id,workshop_id))`,
		`CREATE TABLE marketplace_cooldowns(rate_group TEXT PRIMARY KEY,retry_at_ms INTEGER,source TEXT)`,
		`INSERT INTO users VALUES(10)`, `INSERT INTO workshops VALUES(20)`,
		`INSERT INTO workshop_settings VALUES(20)`, `INSERT INTO marketplace_connections VALUES(30,20)`,
	} {
		if _, e = tx.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if e = migrateBackground(tx); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{
		`INSERT INTO background_jobs(id,workshop_id,connection_id,created_by_user_id,job_type,status,schedule_type,timezone,next_run_at,created_at,updated_at) VALUES(40,20,30,10,'WB_DAILY_SYNC','active','DAILY','Europe/Moscow',100,90,90)`,
		`INSERT INTO background_job_runs(id,job_id,workshop_id,local_date,status,started_at,lease_owner,lease_until) VALUES(50,40,20,'2026-09-23','success',90,'owner',100)`,
		`INSERT INTO wb_daily_snapshots VALUES(50,20,30,'wb_get_prices','1:2',1,'2026-09-23T08:00:00Z','{"price_cents":100}')`,
		`INSERT INTO wb_daily_current VALUES(20,30,'wb_get_prices','1:2',1,50,'2026-09-23T08:00:00Z','{"price_cents":100}')`,
		`INSERT INTO wb_daily_diffs VALUES(50,'wb_get_prices','1:2',1,'{}','{"price_cents":100}','{"price_cents":5}')`,
	} {
		if _, e = tx.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if e = migrateGenericBackground(tx); e != nil {
		t.Fatal(e)
	}
	if e = migrateGenericBackground(tx); e != nil {
		t.Fatal("non-idempotent migration", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var params, schedule string
	if e = db.QueryRow(`SELECT parameters_json,schedule_type FROM background_jobs WHERE id=40`).Scan(&params, &schedule); e != nil || params != `{"connection_id":30}` || schedule != "DAILY_AT_TIME" {
		t.Fatal(params, schedule, e)
	}
	for _, name := range []string{"background_job_runs", "wb_daily_snapshots", "wb_daily_current", "wb_daily_diffs"} {
		var n int
		if e = db.QueryRow(`SELECT COUNT(*) FROM ` + name + ` WHERE ` + map[string]string{"background_job_runs": "id=50", "wb_daily_snapshots": "run_id=50", "wb_daily_current": "run_id=50", "wb_daily_diffs": "run_id=50"}[name]).Scan(&n); e != nil || n != 1 {
			t.Fatal(name, n, e)
		}
	}
	var raw string
	if e = db.QueryRow(`SELECT data_json FROM wb_daily_current`).Scan(&raw); e != nil || raw != `{"price_cents":100}` {
		t.Fatal(raw, e)
	}
	rows, e := db.Query(`PRAGMA foreign_key_check`)
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	rows.Close()
	if _, e = db.Exec(`INSERT INTO background_jobs(workshop_id,created_by_user_id,job_type,status,schedule_type,local_time,timezone,next_run_at,created_at,updated_at) VALUES(20,10,'OTHER_JOB','active','DAILY_AT_TIME','08:00','UTC',100,90,90)`); e != nil {
		t.Fatal("still WB-specific", e)
	}
}
