package storage

import (
	"fmt"
	"strings"
)

var requiredTables = []string{
	"schema_migrations",
	"workshops",
	"users",
	"workshop_members",
	"telegram_chats",
	"materials",
	"products",
	"bom_items",
	"inventory_movements",
	"product_movements",
	"production_records",
	"shipments",
	"production_plans",
	"conversation_sessions",
	"pending_actions",
	"audit_logs",
	"workshop_invites",
	"user_workshop_context",
	"user_preferences",
	"workshop_settings",
	"conversation_messages",
	"working_memory",
	"long_term_memory",
	"memory_traces",
}

// InitDatabase creates the local database schema and verifies every required table.
func InitDatabase(path string) error {
	store, err := New(path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	for _, table := range requiredTables {
		var name string
		err := store.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			return fmt.Errorf("verify table %q: %w", table, err)
		}
		if !strings.EqualFold(name, table) {
			return fmt.Errorf("verify table %q: got %q", table, name)
		}
	}
	return nil
}

func RequiredTables() []string {
	return append([]string(nil), requiredTables...)
}
