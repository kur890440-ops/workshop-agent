package storage

import "database/sql"

const chatClearVersion = 103

func migrateChatClear(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE number=?`, chatClearVersion).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, q := range []string{
		`CREATE TABLE telegram_messages(chat_id INTEGER NOT NULL,user_id INTEGER NOT NULL REFERENCES users(id),message_id INTEGER NOT NULL,sent_at INTEGER NOT NULL,kind TEXT NOT NULL DEFAULT 'message',PRIMARY KEY(chat_id,message_id))`,
		`CREATE INDEX telegram_messages_owner ON telegram_messages(user_id,chat_id)`,
		`CREATE TABLE telegram_clear_limits(chat_id INTEGER PRIMARY KEY,retry_at INTEGER NOT NULL)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO schema_migrations(number,name) VALUES(?,'telegram chat clear')`, chatClearVersion)
	return err
}
