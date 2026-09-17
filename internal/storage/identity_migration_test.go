package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func legacyDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range migrations() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func TestLegacyIdentityMigrationPreservesDataAndBacksUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db := legacyDB(t, path)
	for _, stmt := range []string{
		`INSERT INTO users(id,telegram_user_id,display_name) VALUES(1,900001,'Owner')`,
		`INSERT INTO workshops(id,name) VALUES(1,'Existing'),(2,'Old demo')`,
		`INSERT INTO workshop_members(workshop_id,user_id,role) VALUES(1,900001,'owner')`,
		`INSERT INTO telegram_chats(telegram_chat_id,workshop_id) VALUES(900001,1)`,
		`INSERT INTO materials(id,workshop_id,name,category,base_unit,current_stock) VALUES(7,1,'Gypsum','raw','g',230)`,
		`INSERT INTO products(id,workshop_id,name,product_type,current_stock) VALUES(8,1,'Product','product',12)`,
		`INSERT INTO bom_items(workshop_id,product_id,component_type,material_id,quantity,unit) VALUES(1,8,'material',7,2,'g')`,
		`INSERT INTO production_records(workshop_id,product_id,attempted_quantity,good_quantity,scrap_quantity,user_id) VALUES(1,8,10,9,1,900001)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checks := map[string]int{`SELECT COUNT(*) FROM users`: 1, `SELECT COUNT(*) FROM workshops`: 2, `SELECT COUNT(*) FROM workshop_members WHERE user_id=1 AND role='OWNER'`: 2, `SELECT COUNT(*) FROM materials WHERE id=7 AND workshop_id=1 AND current_stock=230`: 1, `SELECT COUNT(*) FROM products WHERE id=8 AND workshop_id=1 AND current_stock=12`: 1, `SELECT COUNT(*) FROM bom_items WHERE material_id=7 AND product_id=8`: 1, `SELECT COUNT(*) FROM production_records WHERE user_id=1 AND good_quantity=9`: 1, `SELECT COUNT(*) FROM user_workshop_context WHERE user_id=1 AND active_workshop_id=1`: 1}
	for q, want := range checks {
		var n int
		if err := store.DB.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s: %d want %d", q, n, want)
		}
	}
	backups, err := filepath.Glob(path + ".backup-*.db")
	if err != nil || len(backups) != 1 {
		t.Fatalf("missing backup: %v %v", backups, err)
	}
	backup, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var old int64
	if err := backup.QueryRow(`SELECT user_id FROM workshop_members`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if old != 900001 {
		t.Fatal("backup is not pre-migration")
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=100`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("migration repeated")
	}
	if _, err := store.DB.Exec(`INSERT INTO workshop_members(workshop_id,user_id,role) VALUES(1,777,'VIEWER')`); err == nil {
		t.Fatal("missing membership FK")
	}
}

func TestLegacySingleUserWithoutWorkshop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.db")
	db := legacyDB(t, path)
	if _, err := db.Exec(`INSERT INTO users(telegram_user_id,display_name) VALUES(900001,'Owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE materials DROP COLUMN workshop_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO materials(name,category,base_unit,current_stock) VALUES('Legacy','raw','g',42)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var w int64
	var stock float64
	if err := s.DB.QueryRow(`SELECT workshop_id,current_stock FROM materials WHERE name='Legacy'`).Scan(&w, &stock); err != nil {
		t.Fatal(err)
	}
	if w == 0 || stock != 42 {
		t.Fatal("data lost")
	}
	var owner int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM workshop_members WHERE user_id=1 AND workshop_id=? AND role='OWNER'`, w).Scan(&owner); err != nil || owner != 1 {
		t.Fatal("owner not migrated", err)
	}
}

func TestAmbiguousMigrationRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambiguous.db")
	db := legacyDB(t, path)
	for _, stmt := range []string{`INSERT INTO users(id,telegram_user_id) VALUES(1,200),(200,300)`, `INSERT INTO workshops(name) VALUES('A')`, `INSERT INTO workshop_members(workshop_id,user_id,role) VALUES(1,200,'owner')`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if s, err := New(path); err == nil {
		s.Close()
		t.Fatal("ambiguous identity got access")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err := db.QueryRow(`SELECT role FROM workshop_members`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "owner" {
		t.Fatal("failed migration changed original")
	}
}
