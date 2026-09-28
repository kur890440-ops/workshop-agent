package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/sqlite"
)

type Store struct {
	DB   *sql.DB
	Path string
}

func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		path = "./data/workshop.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{DB: db, Path: path}
	if err := store.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Migrate() error {
	var legacy int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&legacy); err != nil {
		return err
	}
	var done int
	var taskOnlyDone int
	var controlledDone int
	var marketplaceDone int
	var cooldownDone int
	var pacingDone int
	var backgroundDone int
	var genericDone int
	var rateDone int
	var incidentDone int
	var stockSourcesDone int
	var ozonDone int
	var queryDone int
	if legacy > 0 {
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=119`).Scan(&queryDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=118`).Scan(&ozonDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=117`).Scan(&stockSourcesDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=116`).Scan(&incidentDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=115`).Scan(&rateDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=114`).Scan(&genericDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=113`).Scan(&backgroundDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=108`).Scan(&taskOnlyDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=109`).Scan(&controlledDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=110`).Scan(&marketplaceDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=111`).Scan(&cooldownDone)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=112`).Scan(&pacingDone)
		// A missing version table is also a legacy database.
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number IN (100,101,102,103)`).Scan(&done)
		if (queryDone == 0 || done != 4 || taskOnlyDone == 0 || controlledDone == 0 || marketplaceDone == 0 || cooldownDone == 0 || pacingDone == 0 || backgroundDone == 0 || genericDone == 0 || rateDone == 0 || incidentDone == 0 || stockSourcesDone == 0 || ozonDone == 0) && s.Path != "" && s.Path != ":memory:" {
			backup := s.Path + ".backup-" + time.Now().UTC().Format("20060102T150405.000000000") + ".db"
			if _, err := s.DB.Exec(`VACUUM INTO ?`, backup); err != nil {
				return fmt.Errorf("backup before migration: %w", err)
			}
		}
	}
	if ozonDone == 0 {
		if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			return err
		}
		defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, stmt := range migrations() {
		if taskOnlyDone != 0 && strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS production_plans") {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migration %d failed: %w", i+1, err)
		}
	}
	if err := migrateIdentity(tx); err != nil {
		return fmt.Errorf("identity migration: %w", err)
	}
	if err := migrateMemory(tx); err != nil {
		return fmt.Errorf("memory migration: %w", err)
	}
	if err := migrateDisplayUnits(tx); err != nil {
		return fmt.Errorf("display units migration: %w", err)
	}
	if err := migrateChatClear(tx); err != nil {
		return fmt.Errorf("chat clear migration: %w", err)
	}
	if err := migrateTaskState(tx); err != nil {
		return fmt.Errorf("task state migration: %w", err)
	}
	if err := migrateSharedTasks(tx); err != nil {
		return fmt.Errorf("shared tasks migration: %w", err)
	}
	if err := migrateTaskCompletion(tx); err != nil {
		return fmt.Errorf("task completion migration: %w", err)
	}
	if err := migrateInvariants(tx); err != nil {
		return err
	}
	if err := migrateTaskOnly(tx); err != nil {
		return fmt.Errorf("task-only migration: %w", err)
	}
	if err := migrateControlledTransitions(tx); err != nil {
		return err
	}
	if err := migrateMarketplace(tx); err != nil {
		return fmt.Errorf("marketplace migration: %w", err)
	}
	if err := migrateMarketplaceCooldown(tx); err != nil {
		return fmt.Errorf("marketplace cooldown migration: %w", err)
	}
	if err := migrateMarketplacePacing(tx); err != nil {
		return fmt.Errorf("marketplace pacing migration: %w", err)
	}
	if err := migrateBackground(tx); err != nil {
		return fmt.Errorf("background migration: %w", err)
	}
	if err := migrateGenericBackground(tx); err != nil {
		return fmt.Errorf("generic background migration: %w", err)
	}
	if err := migrateWBRateAudit(tx); err != nil {
		return fmt.Errorf("WB rate audit migration: %w", err)
	}
	if err := migrateWBIncidents(tx); err != nil {
		return fmt.Errorf("WB incidents migration: %w", err)
	}
	if err := migrateStockSources(tx); err != nil {
		return err
	}
	if err := migrateOzon(tx); err != nil {
		return fmt.Errorf("Ozon migration: %w", err)
	}
	if err := migrateMarketplaceQuery(tx); err != nil {
		return err
	}
	if ozonDone == 0 {
		rows, err := tx.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		bad := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if bad {
			return fmt.Errorf("Ozon migration: foreign key check failed")
		}
	}

	return tx.Commit()
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}
