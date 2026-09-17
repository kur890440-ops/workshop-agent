package agent

import "time"

type ConversationContext struct {
	WorkshopID     int64
	TelegramChatID int64
	UserID         int64
	// Scope identifiers for the separate working-memory task and conversation session.
	TaskID        string
	SessionID     string
	LastContext   string
	PendingAction string
	UpdatedAt     time.Time
}
