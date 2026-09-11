package storage

import "testing"

func TestInitDatabaseCreatesAllTables(t *testing.T) {
	path := t.TempDir() + "/workshop.db"
	if err := InitDatabase(path); err != nil {
		t.Fatalf("init database: %v", err)
	}

	store, err := New(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer store.Close()

	for _, table := range RequiredTables() {
		var count int
		if err := store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("check table %q: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %q is missing", table)
		}
	}
}