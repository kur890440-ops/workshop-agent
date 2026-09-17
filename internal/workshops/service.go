package workshops

import (
	"database/sql"
	"time"
	"workshop-agent/internal/storage"
)

type Service struct {
	store         *storage.Store
	InviteTTL     time.Duration
	InviteMaxUses int
}

func NewService(path string) *Service {
	store, err := storage.New(path)
	if err != nil {
		panic(err)
	}
	return &Service{store: store, InviteTTL: 24 * time.Hour, InviteMaxUses: 1}
}
func (s *Service) Close() error { return s.store.Close() }
func (s *Service) DB() *sql.DB  { return s.store.DB }
func (s *Service) RegisterUser(telegramID int64, username, displayName string) (int64, error) {
	return s.UpsertUser(telegramID, username, displayName, "")
}
