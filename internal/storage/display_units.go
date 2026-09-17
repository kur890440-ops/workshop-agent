package storage

import "database/sql"

const displayUnitsVersion = 102

func migrateDisplayUnits(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=?`, displayUnitsVersion).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := addColumn(tx, "materials", "display_unit", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE materials SET display_unit=base_unit`); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO schema_migrations(number,name) VALUES(?, 'material display units')`, displayUnitsVersion)
	return err
}
