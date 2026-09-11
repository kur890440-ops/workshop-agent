package agent

import "time"

type ConversationContext struct {
	WorkshopID    int64
	TelegramChatID int64
	UserID        int64
	LastContext   string
	PendingAction string
	UpdatedAt     time.Time
}
