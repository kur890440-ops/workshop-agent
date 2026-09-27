package storage

import "database/sql"

func migrateWBIncidents(tx *sql.Tx) error {
	var done int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=116`).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	if _, err := tx.Exec(`CREATE TABLE wb_rate_incidents(id INTEGER PRIMARY KEY,workshop_id INTEGER NOT NULL,created_at TEXT NOT NULL,history_json TEXT NOT NULL)`); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO schema_migrations(number,name) VALUES(116,'wb_rate_incident_history')`)
	return err
}
