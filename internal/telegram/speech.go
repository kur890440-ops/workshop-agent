package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/speech"
)

type telegramAudio struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
}
type speechMetadata struct {
	InputType                         string
	Provider, Model                   string
	ChatID, SenderID, MessageID       int64
	AudioDuration, ProcessingDuration time.Duration
}
type speechCompletion struct {
	message  telegramMessage
	workshop int64
	result   speech.SpeechRecognitionResult
	err      error
	trace    *voiceTrace
}

// ConfigureSpeech is called before StartContext. Workers never enter domain handlers.
func (b *Bot) ConfigureSpeech(p speech.SpeechRecognitionProvider, c speech.Config) {
	if c.MaxConcurrentJobs < 1 {
		c.MaxConcurrentJobs = 1
	}
	b.Speech = p
	b.speechConfig = c
	b.speechSlots = make(chan struct{}, c.MaxConcurrentJobs)
	b.speechResults = make(chan speechCompletion, c.MaxConcurrentJobs)
}
func (b *Bot) audioAuthorization(m *telegramMessage) (int64, error) {
	if m == nil || m.Chat.Type != "private" || m.From.ID <= 0 || m.From.ID != m.Chat.ID || b.WS == nil {
		return 0, auth.ErrDenied
	}
	var user int64
	if e := b.WS.DB().QueryRow(`SELECT id FROM users WHERE telegram_user_id=?`, m.From.ID).Scan(&user); e != nil {
		return 0, auth.ErrDenied
	}
	w, e := b.WS.ActiveWorkshop(user)
	if e != nil {
		return 0, e
	}
	return w, auth.Require(b.WS.DB(), user, w, auth.WorkshopRead)
}
func (b *Bot) startAudio(ctx context.Context, m *telegramMessage) error {
	trace := &voiceTrace{ID: voiceTraceID.Add(1), VoiceReceived: m.Voice != nil, SpeechEnabled: b.speechConfig.Enabled, ProviderPresent: b.Speech != nil, DecodeStatus: "NOT_RUN", TranscriptionStatus: "NOT_RUN"}
	slog.Info("telegram voice received", "voice_request_id", trace.ID, "voice_received", trace.VoiceReceived)
	var status speech.SpeechProviderStatus
	if b.Speech != nil {
		status = b.Speech.Status(ctx)
		trace.Provider = status.Provider
		trace.ModelLoaded = status.ModelLoaded
		trace.RuntimeLoaded = status.RuntimeLoaded
		trace.LastError = status.LastError
	}
	async := false
	finishReply := b.observeSpeechReply(m.Chat.ID, trace)
	defer func() {
		if !async {
			finishReply()
			trace.log()
		}
	}()
	w, e := b.audioAuthorization(m)
	trace.Authorized = e == nil
	if e != nil {
		return b.sendMessage(m.Chat.ID, publicError(e))
	}
	if b.Speech == nil || !b.speechConfig.Enabled {
		trace.LastError = speech.Disabled
		return b.sendMessage(m.Chat.ID, speechError(speech.Disabled))
	}
	if !status.Enabled || !status.ModelLoaded || !status.RuntimeLoaded {
		code := status.LastError
		if !status.Enabled {
			code = speech.Disabled
		} else if code == "" || code == speech.Success {
			code = speech.ModelUnavailable
		}
		trace.LastError = code
		return b.sendMessage(m.Chat.ID, speechError(code))
	}
	a := m.Voice
	if a == nil {
		a = m.Audio
	}
	if a == nil {
		return nil
	}
	if a.FileSize < 0 || a.FileSize > b.speechConfig.MaxBytes {
		return b.sendMessage(m.Chat.ID, speechError(speech.TooLarge))
	}
	if a.Duration < 0 || time.Duration(a.Duration)*time.Second > b.speechConfig.MaxDuration {
		return b.sendMessage(m.Chat.ID, speechError(speech.TooLong))
	}
	if !supportedAudio(a) {
		return b.sendMessage(m.Chat.ID, speechError(speech.Unsupported))
	}
	select {
	case b.speechSlots <- struct{}{}:
	default:
		return b.sendMessage(m.Chat.ID, speechError(speech.Busy))
	}
	original := *m
	file := *a
	finishReply()
	async = true
	b.speechWait.Add(1)
	go func() {
		defer b.speechWait.Done()
		job, cancel := context.WithTimeout(ctx, b.speechConfig.Timeout)
		defer cancel()
		input, e := b.downloadAudio(job, original, file, trace)
		var r speech.SpeechRecognitionResult
		if e == nil {
			r, e = b.Speech.Transcribe(job, input)
			trace.DecodeStatus = r.DecodeStatus
			trace.TranscriptionStatus = speech.ErrorCode(e)
			if e == nil && r.Status != speech.Success {
				trace.TranscriptionStatus = r.Status
			}
		}
		if job.Err() != nil {
			e = job.Err()
		}
		select {
		case b.speechResults <- speechCompletion{original, w, r, e, trace}:
		case <-ctx.Done():
			trace.LastError = speech.ErrorCode(ctx.Err())
			trace.log()
			<-b.speechSlots
		}
	}()
	return nil
}
func supportedAudio(a *telegramAudio) bool {
	switch strings.ToLower(strings.Split(a.MimeType, ";")[0]) {
	case "", "audio/ogg", "application/ogg", "audio/opus", "audio/wav", "audio/x-wav", "audio/wave", "audio/mpeg":
		return true
	}
	return false
}
func (b *Bot) downloadAudio(ctx context.Context, m telegramMessage, a telegramAudio, trace *voiceTrace) (speech.AudioInput, error) {
	input := speech.AudioInput{MimeType: a.MimeType, FileName: a.FileName, Source: "telegram", SourceMessageID: m.MessageID, Duration: time.Duration(a.Duration) * time.Second}
	endpoint := "https://api.telegram.org/bot" + b.Token + "/getFile?file_id=" + url.QueryEscape(a.FileID)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return input, speech.Failed
	}
	client := *b.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	trace.FileRequested = true
	resp, e := client.Do(req)
	if e != nil {
		return input, speech.Failed
	}
	var file struct {
		OK     bool `json:"ok"`
		Result struct {
			Path string `json:"file_path"`
			Size int64  `json:"file_size"`
		} `json:"result"`
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&file)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || !file.OK || file.Result.Path == "" {
		return input, speech.Failed
	}
	if file.Result.Size > b.speechConfig.MaxBytes {
		return input, speech.TooLarge
	}
	// Accept only Telegram relative paths; never follow arbitrary URLs or redirects carrying credentials.
	if strings.Contains(file.Result.Path, "..") || strings.ContainsAny(file.Result.Path, "?:\\#") || strings.HasPrefix(file.Result.Path, "/") {
		return input, speech.Failed
	}
	req, e = http.NewRequestWithContext(ctx, http.MethodGet, "https://api.telegram.org/file/bot"+b.Token+"/"+file.Result.Path, nil)
	if e != nil {
		return input, speech.Failed
	}
	resp, e = client.Do(req)
	if e != nil {
		return input, speech.Failed
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return input, speech.Failed
	}
	if resp.ContentLength > b.speechConfig.MaxBytes {
		return input, speech.TooLarge
	}
	input.Data, e = io.ReadAll(io.LimitReader(resp.Body, b.speechConfig.MaxBytes+1))
	if e != nil {
		return input, speech.Failed
	}
	if int64(len(input.Data)) > b.speechConfig.MaxBytes {
		return input, speech.TooLarge
	}
	trace.FileDownloaded = true
	return input, nil
}
func (b *Bot) finishAudio(c speechCompletion) error {
	defer func() { <-b.speechSlots }()
	if c.trace == nil {
		c.trace = &voiceTrace{}
	}
	finishReply := b.observeSpeechReply(c.message.Chat.ID, c.trace)
	defer func() { finishReply(); c.trace.log() }()
	c.trace.LastError = speech.ErrorCode(c.err)
	if c.err != nil {
		return b.sendMessage(c.message.Chat.ID, speechError(speech.ErrorCode(c.err)))
	}
	if c.result.Status != speech.Success {
		return b.sendMessage(c.message.Chat.ID, speechError(c.result.Status))
	}
	if strings.TrimSpace(c.result.Text) == "" {
		return b.sendMessage(c.message.Chat.ID, speechError(speech.Empty))
	}
	w, e := b.audioAuthorization(&c.message)
	c.trace.Authorized = e == nil
	if e != nil {
		return b.sendMessage(c.message.Chat.ID, publicError(e))
	}
	if w != c.workshop {
		return b.sendMessage(c.message.Chat.ID, "Мастерская изменилась. Отправьте голосовое сообщение ещё раз.")
	}
	if b.VoiceShowTranscript && c.message.Voice != nil {
		if err := b.sendVoiceTranscript(c.message, c.result.Text); err != nil {
			// Do not log the transcript, credentials, or Telegram request error/URL.
			slog.Warn("telegram voice transcript feedback failed", "voice_request_id", c.trace.ID)
		}
	}
	m := c.message
	m.Text = c.result.Text
	m.Voice = nil
	m.Audio = nil
	m.Speech = &speechMetadata{InputType: "VOICE", Provider: c.result.Provider, Model: c.result.Model, ChatID: m.Chat.ID, SenderID: m.From.ID, MessageID: m.MessageID, AudioDuration: c.result.AudioDuration, ProcessingDuration: c.result.ProcessingDuration}
	// This is the same existing entrypoint as typed text, including all confirmations.
	c.trace.TextHandlerCalled = true
	return b.handleMessage(&m)
}
func speechError(c speech.Code) string {
	switch c {
	case speech.Disabled, speech.ModelUnavailable, speech.RuntimeUnavailable, speech.InvalidAssets:
		return "Распознавание речи недоступно. Отправьте сообщение текстом."
	case speech.TooLarge:
		return "Аудиофайл слишком большой."
	case speech.TooLong:
		return "Аудиозапись слишком длинная."
	case speech.Unsupported:
		return "Формат аудио не поддерживается. Отправьте голосовое сообщение или WAV."
	case speech.Busy:
		return "Распознавание занято. Повторите отправку позже."
	case speech.Timeout:
		return "Время распознавания истекло. Отправьте более короткую запись."
	default:
		return "Не удалось распознать голосовое сообщение. Отправьте сообщение текстом."
	}
}
