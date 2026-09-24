package storage

import (
	"path/filepath"
	"testing"
)

func TestPacingMigrationPreservesServerCooldownAndRunsOnce(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	// Reconstruct a version-111 fixture with old local and real server waits.
	if _, err = s.DB.Exec(`DELETE FROM schema_migrations WHERE number=112`); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ group, source string }{{"common", "local_interval"}, {"analytics", "wb_retry"}, {"content", "wb_reset"}, {"marketplace", "local_backoff"}} {
		if _, err = s.DB.Exec(`INSERT INTO marketplace_cooldowns VALUES(?,9999999999999,?)`, v.group, v.source); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM marketplace_cooldowns WHERE rate_group='common'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM marketplace_cooldowns WHERE retry_at_ms=9999999999999`).Scan(&n); err != nil || n != 3 {
		t.Fatal("server deadlines changed", n, err)
	}
	if _, err = s.DB.Exec(`INSERT INTO marketplace_cooldowns VALUES('common',9999999999999,'local_interval')`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM marketplace_cooldowns`).Scan(&n); err != nil || n != 4 {
		t.Fatal("migration repeated", n, err)
	}
}
