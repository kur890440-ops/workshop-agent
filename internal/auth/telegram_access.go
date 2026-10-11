package auth

import "database/sql"

// ExistingTelegramUser resolves an existing enabled identity with at least one
// readable workshop, without provisioning or changing its active workshop.
func ExistingTelegramUser(db *sql.DB, telegramID int64) (int64, error) {
	var user int64
	if err := db.QueryRow(`SELECT id FROM users WHERE telegram_user_id=? AND status='active' AND is_active=1`, telegramID).Scan(&user); err != nil {
		return 0, ErrDenied
	}
	rows, err := db.Query(`SELECT workshop_id FROM workshop_members WHERE user_id=?`, user)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if Require(db, user, id, WorkshopRead) == nil {
			return user, nil
		}
	}
	return 0, ErrDenied
}
