package audit

import (
	"database/sql"
	"fmt"
	"time"

	"workshop-agent/internal/storage"
)

type Service struct {
	store *storage.Store
}

func NewService(path string) *Service {
	store, err := storage.New(path)
	if err != nil {
		panic(err)
	}
	return &Service{store: store}
}

func (s *Service) Close() error {
	return s.store.Close()
}

func (s *Service) DB() *sql.DB {
	return s.store.DB
}

func (s *Service) Log(workshopID int64, entityType string, entityID int64, actorUserID int64, actorName string, action string, fieldName string, oldValue string, newValue string, details string) error {
	_, err := s.store.DB.Exec(`
		INSERT INTO audit_logs (
			workshop_id, entity_type, entity_id, actor_user_id, actor_name,
			action, field_name, old_value, new_value, details, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, workshopID, entityType, entityID, actorUserID, actorName, action, fieldName, oldValue, newValue, details, time.Now().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("audit log insert: %w", err)
	}
	return nil
}

func (s *Service) ListByWorkshop(workshopID int64) ([]map[string]any, error) {
	rows, err := s.store.DB.Query(`
		SELECT id, entity_type, entity_id, actor_user_id, actor_name, action, field_name, old_value, new_value, details, created_at
		FROM audit_logs WHERE workshop_id = ? ORDER BY created_at DESC`, workshopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var entityType, actorName, action, fieldName, oldValue, newValue, details, createdAt string
		var actorUserID sql.NullInt64
		var entityID int64
		if err := rows.Scan(&id, &entityType, &entityID, &actorUserID, &actorName, &action, &fieldName, &oldValue, &newValue, &details, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id,
			"entity_type": entityType,
			"entity_id": entityID,
			"actor_user_id": actorUserID.Int64,
			"actor_name": actorName,
			"action": action,
			"field_name": fieldName,
			"old_value": oldValue,
			"new_value": newValue,
			"details": details,
			"created_at": createdAt,
		})
	}
	return out, rows.Err()
}
