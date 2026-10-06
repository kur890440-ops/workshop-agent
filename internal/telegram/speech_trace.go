package telegram

import (
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"workshop-agent/internal/speech"
)

var voiceTraceID atomic.Uint64

type voiceTrace struct {
	ID                  uint64      `json:"voice_request_id"`
	VoiceReceived       bool        `json:"voice_received"`
	Authorized          bool        `json:"authorized"`
	SpeechEnabled       bool        `json:"speech_enabled"`
	ProviderPresent     bool        `json:"provider_present"`
	Provider            string      `json:"provider"`
	RuntimeLoaded       bool        `json:"runtime_loaded"`
	ModelLoaded         bool        `json:"model_loaded"`
	FileRequested       bool        `json:"telegram_file_requested"`
	FileDownloaded      bool        `json:"telegram_file_downloaded"`
	DecodeStatus        speech.Code `json:"decode_status"`
	TranscriptionStatus speech.Code `json:"transcription_status"`
	TextHandlerCalled   bool        `json:"text_handler_called"`
	ResponseSent        bool        `json:"response_sent"`
	LastError           speech.Code `json:"last_error"`
}

func (t *voiceTrace) log() {
	if t == nil {
		return
	}
	data, _ := json.Marshal(t)
	slog.Info("telegram voice", "trace", string(data))
}

// Scoped to synchronous reply delivery; workers never enter the text handler.
// Count only successful Telegram send/edit acknowledgements for this chat.
func (b *Bot) observeSpeechReply(chat int64, t *voiceTrace) func() {
	ack := &atomic.Bool{}
	b.speechReplies.Store(chat, ack)
	return func() { b.speechReplies.Delete(chat); t.ResponseSent = ack.Load() }
}
func (b *Bot) speechReplyAcknowledged(method string, raw []byte) {
	if method != "sendMessage" && method != "editMessageText" {
		return
	}
	var payload struct {
		ChatID int64 `json:"chat_id"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	if value, ok := b.speechReplies.Load(payload.ChatID); ok {
		value.(*atomic.Bool).Store(true)
	}
}
