package workshops

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

func (s *Service) Close() error { return s.store.Close() }
func (s *Service) DB() *sql.DB { return s.store.DB }

func (s *Service) CreateWorkshop(name string) (int64, error) {
	res, err := s.store.DB.Exec(`INSERT INTO workshops (name, created_at, is_active) VALUES (?, ?, ?)`, name, time.Now().Format(time.RFC3339), 1)
	if err != nil { return 0, err }
	return res.LastInsertId()
}

func (s *Service) GetWorkshopByChat(chatID int64) (int64, error) {
	var id int64
	err := s.store.DB.QueryRow(`SELECT workshop_id FROM telegram_chats WHERE telegram_chat_id = ? AND is_active = 1 LIMIT 1`, chatID).Scan(&id)
	return id, err
}

func (s *Service) EnsureTelegramChat(chatID int64, workshopID int64, title string) error {
	_, err := s.store.DB.Exec(`INSERT OR REPLACE INTO telegram_chats (telegram_chat_id, workshop_id, title, is_active) VALUES (?, ?, ?, 1)`, chatID, workshopID, title)
	return err
}

func (s *Service) RegisterUser(telegramUserID int64, username, displayName string) (int64, error) {
	res, err := s.store.DB.Exec(`INSERT OR IGNORE INTO users (telegram_user_id, telegram_username, display_name, is_active, created_at) VALUES (?, ?, ?, 1, ?)`, telegramUserID, username, displayName, time.Now().Format(time.RFC3339))
	if err != nil { return 0, err }
	if res != nil {
		if rows, err := res.RowsAffected(); err == nil && rows == 0 {
			var existing int64
			if err := s.store.DB.QueryRow(`SELECT id FROM users WHERE telegram_user_id = ?`, telegramUserID).Scan(&existing); err != nil {
				return 0, err
			}
			return existing, nil
		}
	}
	var id int64
	if err := s.store.DB.QueryRow(`SELECT id FROM users WHERE telegram_user_id = ?`, telegramUserID).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) AddMember(workshopID, userID int64, role string) error {
	_, err := s.store.DB.Exec(`INSERT OR REPLACE INTO workshop_members (workshop_id, user_id, role, is_active) VALUES (?, ?, ?, 1)`, workshopID, userID, role)
	return err
}

func (s *Service) UserWorkshops(userID int64) ([]int64, error) {
	rows, err := s.store.DB.Query(`SELECT workshop_id FROM workshop_members WHERE user_id = ? AND is_active = 1`, userID)
	if err != nil { return nil, err }
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var workshopID int64
		if err := rows.Scan(&workshopID); err != nil { return nil, err }
		ids = append(ids, workshopID)
	}
	return ids, rows.Err()
}

func (s *Service) IsAuthorized(userID, workshopID int64, requiredRole string) (bool, error) {
	var role string
	err := s.store.DB.QueryRow(`SELECT role FROM workshop_members WHERE user_id = ? AND workshop_id = ? AND is_active = 1 LIMIT 1`, userID, workshopID).Scan(&role)
	if err != nil { return false, err }
	if requiredRole == "viewer" { return true, nil }
	if requiredRole == "worker" { return role == "owner" || role == "manager" || role == "worker", nil }
	if requiredRole == "manager" { return role == "owner" || role == "manager", nil }
	if requiredRole == "owner" { return role == "owner", nil }
	return false, fmt.Errorf("unsupported role %s", requiredRole)
}
