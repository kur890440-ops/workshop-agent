package telegram

// UI only: never routes the outgoing text through handleMessage or Agent.
func (b *Bot) sendVoiceTranscript(message telegramMessage, transcript string) error {
	for _, part := range materialMessageParts("🎙 Распознано:\n«" + transcript + "»") {
		payload := map[string]any{"chat_id": message.Chat.ID, "text": part, "disable_web_page_preview": true}
		if message.MessageID > 0 {
			payload["reply_parameters"] = map[string]any{"message_id": message.MessageID, "allow_sending_without_reply": true}
		}
		if err := b.api("sendMessage", payload, nil); err != nil {
			return err
		}
	}
	return nil
}
