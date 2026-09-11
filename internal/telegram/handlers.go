package telegram

import "fmt"

func (b *Bot) HandleMessage(userID, chatID int64, text string) string {
	return fmt.Sprintf("сообщение принято от пользователя %d в чате %d: %s", userID, chatID, text)
}
