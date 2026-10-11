package telegram

import (
	"context"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/message"
	"workshop-agent/internal/stability"
)

// HandleInbound adapts Web to handleMessage/processMessage, also used by Telegram.
func (b *Bot) HandleInbound(ctx context.Context, in message.Inbound) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if in.InputType != "WEB" || in.WebSessionID <= 0 || in.ConversationID != -in.WebSessionID {
		return auth.ErrDenied
	}
	if scope := stability.ScopeFrom(ctx); scope != nil {
		scope.WebSession = in.WebSessionID
	}
	m := &telegramMessage{Input: &in, Text: in.Text, Chat: telegramChat{ID: in.ConversationID, Type: "private"}, From: telegramUser{ID: in.TelegramUserID}}
	if in.ConfirmationID != "" {
		done := b.enterApplication(ctx, in.UserID)
		defer done()
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, err := b.inboundUser(m); err != nil {
			return err
		}
		return b.handleConfirmation(sessionKey{in.ConversationID, in.UserID}, in.ConfirmationID)
	}
	return b.handleMessageContext(ctx, m)
}
func (b *Bot) inboundUser(m *telegramMessage) (int64, error) {
	if m.Input == nil {
		return b.WS.UpsertUser(m.From.ID, m.From.Username, m.From.FirstName, m.From.LastName)
	}
	i := m.Input
	var user int64
	err := b.WS.DB().QueryRow(`SELECT s.user_id FROM web_sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND s.user_id=? AND s.telegram_user_id=? AND u.telegram_user_id=s.telegram_user_id AND u.status='active' AND u.is_active=1 AND s.revoked_at IS NULL AND s.expires_at>unixepoch()`, i.WebSessionID, i.UserID, i.TelegramUserID).Scan(&user)
	if err != nil {
		return 0, auth.ErrDenied
	}
	resolved, err := auth.ExistingTelegramUser(b.WS.DB(), i.TelegramUserID)
	if err != nil || resolved != user {
		return 0, auth.ErrDenied
	}
	if i.WorkshopID > 0 {
		w, e := b.WS.ActiveWorkshop(user)
		if e != nil || w != i.WorkshopID {
			return 0, auth.ErrDenied
		}
		if e = auth.Require(b.WS.DB(), user, w, auth.WorkshopRead); e != nil {
			return 0, e
		}
	}
	return user, nil
}
func (b *Bot) deliverWeb(chat int64, out message.Output) error {
	if b.WebOutput == nil {
		return auth.ErrDenied
	}
	if scope := stability.ScopeFrom(b.llmContext()); scope != nil {
		out.RequestID = scope.ID
		out.WorkshopID, _ = b.WS.ActiveWorkshop(scope.User)
	}
	return b.WebOutput(chat, out)
}
