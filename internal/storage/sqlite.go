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
	if legacy > 0 {
		// A missing version table is also a legacy database.
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number IN (100,101,102,103)`).Scan(&done)
		if done != 4 && s.Path != "" && s.Path != ":memory:" {
			backup := s.Path + ".backup-" + time.Now().UTC().Format("20060102T150405.000000000") + ".db"
			if _, err := s.DB.Exec(`VACUUM INTO ?`, backup); err != nil {
				return fmt.Errorf("backup before migration: %w", err)
			}
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, stmt := range migrations() {
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
	return tx.Commit()
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}
