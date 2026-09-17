package telegram

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"workshop-agent/internal/auth"
)

type telegramAPIError struct {
	Code, RetryAfter int
	Missing          bool
}

func (e *telegramAPIError) Error() string { return fmt.Sprintf("Telegram API error (%d)", e.Code) }

func isClearCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/clear", "очисти", "очистить", "очисти чат", "очистить чат", "очисти диалог", "очистить диалог", "удали переписку", "удалить переписку":
		return true
	}
	return false
}
func (b *Bot) ownChat(key sessionKey) error {
	var n int
	err := b.WS.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=? AND telegram_user_id=? AND status='active' AND is_active=1`, key.UserID, key.ChatID).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 || key.ChatID <= 0 {
		return auth.ErrDenied
	}
	return nil
}
func (b *Bot) trackMessage(msg *telegramMessage, user int64) error {
	if msg == nil || msg.MessageID <= 0 || msg.Chat.Type != "private" {
		return nil
	}
	if err := b.ownChat(sessionKey{msg.Chat.ID, user}); err != nil {
		return err
	}
	kind := "message"
	if len(msg.Dice) > 0 && string(msg.Dice) != "null" {
		kind = "dice"
	}
	_, err := b.WS.DB().Exec(`INSERT INTO telegram_messages(chat_id,user_id,message_id,sent_at,kind) VALUES(?,?,?,?,?) ON CONFLICT(chat_id,message_id) DO NOTHING`, msg.Chat.ID, user, msg.MessageID, msg.Date, kind)
	return err
}
func (b *Bot) invalidateClear(key sessionKey, all bool) {
	b.uiMu.Lock()
	defer b.uiMu.Unlock()
	for id, a := range b.buttons {
		if a.Key == key && (all || a.Action == "clear_execute" || a.Action == "clear_cancel") {
			delete(b.buttons, id)
		}
	}
	if all {
		delete(b.ui, key)
		delete(b.materialLists, key)
	}
}
func (b *Bot) clearPrompt(key sessionKey) error {
	if err := b.ownChat(key); err != nil {
		return err
	}
	b.invalidateClear(key, false)
	return b.screen(key, "Удалить доступные сообщения из этого чата?\nДанные мастерской, задачи и сохранённые настройки останутся.\nTelegram может ограничивать удаление старых сообщений.", choice{Text: "Очистить чат", Action: "clear_execute"}, choice{Text: "Отмена", Action: "clear_cancel"})
}
func (b *Bot) clearChat(key sessionKey) error {
	if err := b.ownChat(key); err != nil {
		return err
	}
	b.invalidateClear(key, true)
	b.clearSetup(key.ChatID, key.UserID)
	// Close every workshop's short-term session in this personal chat. Keep tasks,
	// preferences, domain rows and historical session records intact.
	tx, err := b.WS.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE conversation_sessions SET status='closed',updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND telegram_chat_id=? AND status='active'`, key.UserID, key.ChatID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM pending_actions WHERE user_id=? AND chat_id=?`, key.UserID, key.ChatID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	var retryAt int64
	_ = b.WS.DB().QueryRow(`SELECT retry_at FROM telegram_clear_limits WHERE chat_id=?`, key.ChatID).Scan(&retryAt)
	if retryAt > time.Now().Unix() {
		return b.sendMessage(key.ChatID, fmt.Sprintf("Telegram ограничил частоту запросов. Повторите /clear через %d сек. Удаление не запускалось. Краткосрочный контекст закрыт.", retryAt-time.Now().Unix()))
	}
	rows, err := b.WS.DB().Query(`SELECT message_id,sent_at,kind FROM telegram_messages WHERE chat_id=? AND user_id=? ORDER BY message_id`, key.ChatID, key.UserID)
	if err != nil {
		return err
	}
	type entry struct {
		id, date int64
		kind     string
	}
	var list []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.id, &e.date, &e.kind); err != nil {
			rows.Close()
			return err
		}
		list = append(list, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	deleted, missing, skipped, failed, untried := 0, 0, 0, 0, 0
	reason := ""
	for i, e := range list {
		age := time.Now().Unix() - e.date
		if e.date <= 0 || age < 0 || age >= 48*3600 || (e.kind == "dice" && age <= 24*3600) {
			skipped++
			continue
		}
		var ok bool
		err = b.api("deleteMessage", map[string]any{"chat_id": key.ChatID, "message_id": e.id}, &ok)
		var apiErr *telegramAPIError
		absent := errors.As(err, &apiErr) && apiErr.Missing
		if (err == nil && ok) || absent {
			if absent {
				missing++
			} else {
				deleted++
			}
			if _, dbErr := b.WS.DB().Exec(`DELETE FROM telegram_messages WHERE chat_id=? AND user_id=? AND message_id=?`, key.ChatID, key.UserID, e.id); dbErr != nil {
				reason = "Ошибка обновления локального учёта; записи сохранены для повторной проверки."
				untried = len(list) - i - 1
				break
			}
			continue
		}
		failed++
		if apiErr != nil && apiErr.Code == 429 {
			delay := apiErr.RetryAfter
			if delay < 1 {
				delay = 30
			}
			_, dbErr := b.WS.DB().Exec(`INSERT INTO telegram_clear_limits(chat_id,retry_at) VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET retry_at=excluded.retry_at`, key.ChatID, time.Now().Unix()+int64(delay))
			reason = fmt.Sprintf("Лимит Telegram: повторите /clear не ранее чем через %d сек.", delay)
			if dbErr != nil {
				reason += " Не удалось сохранить время следующей попытки."
			}
			untried = len(list) - i - 1
			break
		}
		// Permanent per-message refusals allow the rest of the snapshot to proceed.
		// Network/authorization/server failures stop this run; no success is inferred.
		if apiErr == nil || apiErr.Code != 400 {
			reason = "Удаление остановлено из-за ошибки связи или отказа Telegram. Неудачные записи сохранены для повторной попытки."
			untried = len(list) - i - 1
			break
		}
	}
	result := fmt.Sprintf("Удалено: %d. Уже отсутствовали: %d. Пропущено по возрасту, типу или неизвестной дате: %d. Ошибок удаления: %d. Не проверено: %d.\n%s\nСтарая история с неизвестными ID не проверялась. Вся переписка могла не удалиться.\nКраткосрочный контекст закрыт. Данные мастерской, задачи и настройки сохранены.", deleted, missing, skipped, failed, untried, reason)
	return b.sendMessage(key.ChatID, result)
}
