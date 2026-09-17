package storage

import (
	"database/sql"
	"fmt"
	"time"
)

const identityVersion = 100

// addColumn supports the existing SQLite schema as well as old single-user tables.
func addColumn(tx *sql.Tx, table, name, definition string) error {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, nn, pk int
		var n, typ string
		var d any
		if err := rows.Scan(&cid, &n, &typ, &nn, &d, &pk); err != nil {
			rows.Close()
			return err
		}
		if n == name {
			found = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || found {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + definition)
	return err
}

func migrateIdentity(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=?`, identityVersion).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, table := range []string{"users", "workshops", "workshop_members"} {
		for _, c := range [][2]string{{"updated_at", "TEXT NOT NULL DEFAULT ''"}, {"status", "TEXT NOT NULL DEFAULT 'active'"}} {
			if err := addColumn(tx, table, c[0], c[1]); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE ` + table + ` SET status=CASE WHEN is_active=1 THEN 'active' ELSE 'disabled' END, updated_at=CURRENT_TIMESTAMP`); err != nil {
			return err
		}
	}
	for _, c := range [][2]string{{"first_name", "TEXT"}, {"last_name", "TEXT"}} {
		if err := addColumn(tx, "users", c[0], c[1]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE users SET first_name=trim(display_name) WHERE first_name IS NULL`); err != nil {
		return err
	}
	for _, c := range [][2]string{{"created_at", "TEXT NOT NULL DEFAULT ''"}, {"invited_by_user_id", "INTEGER REFERENCES users(id)"}, {"joined_at", "TEXT"}} {
		if err := addColumn(tx, "workshop_members", c[0], c[1]); err != nil {
			return err
		}
	}

	// Repair the old handler's Telegram IDs only when the identity is unambiguous.
	var ambiguous int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workshop_members m JOIN users external ON external.telegram_user_id=m.user_id JOIN users internal ON internal.id=m.user_id WHERE external.id<>internal.id`).Scan(&ambiguous); err != nil {
		return err
	}
	if ambiguous > 0 {
		return fmt.Errorf("ambiguous legacy membership IDs; migration rolled back; resolve identity mapping from backup")
	}
	if _, err := tx.Exec(`UPDATE workshop_members SET user_id=COALESCE((SELECT id FROM users WHERE telegram_user_id=workshop_members.user_id),user_id) WHERE NOT EXISTS(SELECT 1 FROM users WHERE id=workshop_members.user_id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE workshop_members SET role=CASE upper(role) WHEN 'MANAGER' THEN 'ADMIN' WHEN 'WORKER' THEN 'EMPLOYEE' ELSE upper(role) END, created_at=CURRENT_TIMESTAMP, joined_at=CURRENT_TIMESTAMP`); err != nil {
		return err
	}

	// Preserve historical authors too; leave already-valid internal IDs untouched.
	for _, item := range [][2]string{{"audit_logs", "actor_user_id"}, {"inventory_movements", "user_id"}, {"product_movements", "user_id"}, {"production_records", "user_id"}, {"shipments", "user_id"}, {"conversation_sessions", "user_id"}, {"pending_actions", "user_id"}} {
		table, col := item[0], item[1]
		if _, err := tx.Exec(`UPDATE ` + table + ` SET ` + col + `=(SELECT id FROM users WHERE telegram_user_id=` + table + `.` + col + `) WHERE NOT EXISTS(SELECT 1 FROM users WHERE id=` + table + `.` + col + `) AND EXISTS(SELECT 1 FROM users WHERE telegram_user_id=` + table + `.` + col + `)`); err != nil {
			return err
		}
	}
	var users int
	var soleID sql.NullInt64
	if err := tx.QueryRow(`SELECT COUNT(*),MIN(id) FROM users WHERE is_active=1`).Scan(&users, &soleID); err != nil {
		return err
	}
	var workshops int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workshops`).Scan(&workshops); err != nil {
		return err
	}
	if users == 1 && workshops == 0 {
		if _, err := tx.Exec(`INSERT INTO workshops(name,status,updated_at) VALUES('Моя мастерская','active',CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
	}
	for _, table := range []string{"materials", "products", "bom_items", "inventory_movements", "product_movements", "production_records", "shipments", "production_plans", "conversation_sessions", "pending_actions", "audit_logs"} {
		if err := addColumn(tx, table, "workshop_id", "INTEGER REFERENCES workshops(id)"); err != nil {
			return err
		}
		var missing int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE workshop_id IS NULL OR workshop_id=0`).Scan(&missing); err != nil {
			return err
		}
		if missing > 0 {
			var count int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM workshops`).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("cannot infer workshop for legacy %s rows", table)
			}
			if _, err := tx.Exec(`UPDATE ` + table + ` SET workshop_id=(SELECT MIN(id) FROM workshops) WHERE workshop_id IS NULL OR workshop_id=0`); err != nil {
				return err
			}
		}
	}
	if users == 1 {
		// Old bootstrap created ownerless demo workshops. Only the unambiguous single user may own them.
		if _, err := tx.Exec(`INSERT INTO workshop_members(workshop_id,user_id,role,is_active,status,created_at,updated_at,joined_at) SELECT w.id,?,'OWNER',1,'active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM workshops w WHERE NOT EXISTS(SELECT 1 FROM workshop_members m WHERE m.workshop_id=w.id)`, soleID.Int64); err != nil {
			return err
		}
	}
	var ownerless int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workshops w WHERE w.is_active=1 AND NOT EXISTS(SELECT 1 FROM workshop_members m JOIN users u ON u.id=m.user_id WHERE m.workshop_id=w.id AND m.role='OWNER' AND m.status='active' AND u.is_active=1)`).Scan(&ownerless); err != nil {
		return err
	}
	if ownerless > 0 {
		return fmt.Errorf("%d workshops have no identifiable owner; refusing automatic access assignment", ownerless)
	}

	statements := []string{
		`CREATE TABLE workshop_members_v100 (id INTEGER PRIMARY KEY AUTOINCREMENT, workshop_id INTEGER NOT NULL REFERENCES workshops(id), user_id INTEGER NOT NULL REFERENCES users(id), role TEXT NOT NULL CHECK(role IN ('OWNER','ADMIN','EMPLOYEE','VIEWER')), is_active INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','disabled','left','removed')), created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, invited_by_user_id INTEGER REFERENCES users(id), joined_at TEXT, UNIQUE(user_id,workshop_id))`,
		`INSERT INTO workshop_members_v100 SELECT id,workshop_id,user_id,role,is_active,status,created_at,updated_at,invited_by_user_id,joined_at FROM workshop_members`,
		`DROP TABLE workshop_members`,
		`ALTER TABLE workshop_members_v100 RENAME TO workshop_members`,
		`CREATE INDEX membership_workshop_status ON workshop_members(workshop_id,status)`,
		`CREATE TABLE workshop_invites (id INTEGER PRIMARY KEY AUTOINCREMENT, workshop_id INTEGER NOT NULL REFERENCES workshops(id), created_by_user_id INTEGER NOT NULL REFERENCES users(id), role TEXT NOT NULL CHECK(role IN ('ADMIN','EMPLOYEE','VIEWER')), token_hash TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','used','expired','revoked')), expires_at INTEGER NOT NULL, max_uses INTEGER NOT NULL DEFAULT 1 CHECK(max_uses>0), used_count INTEGER NOT NULL DEFAULT 0 CHECK(used_count>=0 AND used_count<=max_uses), created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, revoked_at TEXT)`,
		`CREATE INDEX invites_workshop ON workshop_invites(workshop_id)`,
		`CREATE TABLE user_workshop_context (user_id INTEGER PRIMARY KEY REFERENCES users(id), active_workshop_id INTEGER REFERENCES workshops(id), updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE user_preferences (user_id INTEGER PRIMARY KEY REFERENCES users(id), settings_json TEXT NOT NULL DEFAULT '{}')`,
		`CREATE TABLE workshop_settings (workshop_id INTEGER PRIMARY KEY REFERENCES workshops(id), settings_json TEXT NOT NULL DEFAULT '{}')`,
		`CREATE INDEX audit_workshop_created ON audit_logs(workshop_id,created_at)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	for _, c := range [][2]string{{"target_user_id", "INTEGER REFERENCES users(id)"}, {"event_type", "TEXT NOT NULL DEFAULT ''"}, {"metadata_json", "TEXT NOT NULL DEFAULT '{}'"}} {
		if err := addColumn(tx, "audit_logs", c[0], c[1]); err != nil {
			return err
		}
	}
	// Prefer a legacy private-chat context, without granting any membership through the chat.
	if _, err := tx.Exec(`INSERT INTO user_workshop_context(user_id,active_workshop_id) SELECT u.id,c.workshop_id FROM users u JOIN telegram_chats c ON c.telegram_chat_id=u.telegram_user_id JOIN workshop_members m ON m.user_id=u.id AND m.workshop_id=c.workshop_id WHERE m.status='active' AND c.is_active=1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO user_workshop_context(user_id,active_workshop_id) SELECT user_id,MIN(workshop_id) FROM workshop_members WHERE status='active' GROUP BY user_id HAVING COUNT(*)=1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,event_type,metadata_json) SELECT workshop_id,'membership',id,user_id,'migration','IDENTITY_MIGRATED','','','','Legacy identity normalized','IDENTITY_MIGRATED','{}' FROM workshop_members`); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO schema_migrations(number,name,applied_at) VALUES(?, 'identity_access',?)`, identityVersion, time.Now().UTC().Format(time.RFC3339))
	return err
}
