package storage

import (
	"path/filepath"
	"testing"
)

func TestDisplayUnitsMigrationPreservesValuesAndBacksUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "migration.db")
	s, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO users(id,telegram_user_id,display_name) VALUES(1,123,'User')`,
		`INSERT INTO workshops(id,name) VALUES(1,'A')`,
		`INSERT INTO materials(id,workshop_id,name,category,base_unit,current_stock,minimum_stock) VALUES(1,1,'Гипс','raw','g',10000,2000)`,
		`INSERT INTO products(id,workshop_id,name,product_type) VALUES(1,1,'Product','product')`,
		`INSERT INTO bom_items(workshop_id,product_id,component_type,material_id,quantity,unit) VALUES(1,1,'material',1,300,'g')`,
		`INSERT INTO inventory_movements(workshop_id,material_id,quantity,movement_type,user_id) VALUES(1,1,10000,'manual_adjustment',1)`,
		`DELETE FROM schema_migrations WHERE number=102`,
		`ALTER TABLE materials DROP COLUMN display_unit`,
	}
	for _, q := range statements {
		if _, err = s.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	s, err = New(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var base, display string
	var stock, minimum, bom, movement float64
	if err = s.DB.QueryRow(`SELECT base_unit,display_unit,current_stock,minimum_stock FROM materials WHERE id=1`).Scan(&base, &display, &stock, &minimum); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT quantity FROM bom_items WHERE material_id=1`).Scan(&bom); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT quantity FROM inventory_movements WHERE material_id=1`).Scan(&movement); err != nil {
		t.Fatal(err)
	}
	if base != "g" || display != "g" || stock != 10000 || minimum != 2000 || bom != 300 || movement != 10000 {
		t.Fatal("migration changed base facts")
	}
	backups, err := filepath.Glob(p + ".backup-*.db")
	if err != nil || len(backups) != 1 {
		t.Fatal("backup missing", backups, err)
	}
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	backups, _ = filepath.Glob(p + ".backup-*.db")
	if len(backups) != 1 {
		t.Fatal("migration not idempotent")
	}
}
