package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/invariants"
	"workshop-agent/internal/speech"
)

type speechFake struct {
	text  string
	err   error
	calls int
	block <-chan struct{}
}

func (f *speechFake) Name() string { return "gigaam" }
func (f *speechFake) Status(context.Context) speech.SpeechProviderStatus {
	return speech.SpeechProviderStatus{Enabled: true, Provider: "gigaam", ModelLoaded: true, RuntimeLoaded: true}
}
func (f *speechFake) Transcribe(ctx context.Context, a speech.AudioInput) (speech.SpeechRecognitionResult, error) {
	f.calls++
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return speech.SpeechRecognitionResult{}, ctx.Err()
		}
	}
	return speech.SpeechRecognitionResult{Text: f.text, Provider: "gigaam", Model: "v3_ctc", Status: speech.ErrorCode(f.err), DecodeStatus: speech.Success}, f.err
}
func attachSpeech(t *testing.T, h *harness, f *speechFake) *int {
	t.Helper()
	cfg := speech.DefaultConfig()
	cfg.Enabled = true
	h.bot.ConfigureSpeech(f, cfg)
	// These pre-existing tests isolate the business reply from optional UI feedback.
	h.bot.VoiceShowTranscript = false
	downloads := new(int)
	original := h.bot.HTTPClient.Transport
	h.bot.HTTPClient.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/getFile") {
			*downloads++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"file_path":"voice/test.oga","file_size":3}}`)), Header: http.Header{}}, nil
		}
		if strings.Contains(r.URL.Path, "/file/") {
			*downloads++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ogg")), Header: http.Header{}}, nil
		}
		return original.RoundTrip(r)
	})
	return downloads
}
func voiceMessage(id int64) *telegramMessage {
	return &telegramMessage{MessageID: 26001, Chat: telegramChat{ID: id, Type: "private"}, From: telegramUser{ID: id}, Voice: &telegramAudio{FileID: "fake", FileSize: 3, Duration: 1, MimeType: "audio/ogg"}}
}
func deliverVoice(t *testing.T, h *harness) {
	t.Helper()
	if e := h.bot.startAudio(context.Background(), voiceMessage(900001)); e != nil {
		t.Fatal(e)
	}
	select {
	case c := <-h.bot.speechResults:
		h.bot.speechWait.Wait()
		if e := h.bot.finishAudio(c); e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not complete")
	}
}
func TestVoiceAuthorizedSameTextHandlerExactlyOnce(t *testing.T) {
	h, _, _, _ := assemblyFixture(t)
	fake := &speechFake{text: "/invariants"}
	downloads := attachSpeech(t, h, fake)
	before := len(h.sent)
	deliverVoice(t, h)
	requireAnswer(t, h, "Инварианты мастерской")
	if *downloads != 2 || fake.calls != 1 || len(h.sent) != before+1 {
		t.Fatal(*downloads, fake.calls, len(h.sent)-before)
	}
	var tracked int
	if e := h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id=26001`).Scan(&tracked); e != nil || tracked != 1 {
		t.Fatal(tracked, e)
	}
}
func TestVoiceUnauthorizedNoDownloadNoInference(t *testing.T) {
	h := botFixture(t)
	f := &speechFake{text: "/invariants"}
	downloads := attachSpeech(t, h, f)
	if e := h.bot.startAudio(context.Background(), voiceMessage(999999)); e != nil {
		t.Fatal(e)
	}
	h.bot.speechWait.Wait()
	if *downloads != 0 || f.calls != 0 || len(h.bot.speechResults) != 0 {
		t.Fatal("unauthorized audio reached worker")
	}
}
func TestVoiceFailureAndEmptyLeaveTextWorking(t *testing.T) {
	for _, code := range []speech.Code{speech.Failed, speech.Empty} {
		t.Run(string(code), func(t *testing.T) {
			h, _, _, _ := assemblyFixture(t)
			f := &speechFake{err: code}
			attachSpeech(t, h, f)
			deliverVoice(t, h)
			requireAnswer(t, h, "Не удалось распознать")
			var tracked int
			h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id=26001`).Scan(&tracked)
			if tracked != 0 {
				t.Fatal("failure entered text handler")
			}
			h.message(t, 900001, "/invariants")
			requireAnswer(t, h, "Инварианты мастерской")
		})
	}
}
func TestVoiceUsesSameConfirmationAndInvariantEngine(t *testing.T) {
	h, _, w, _ := assemblyFixture(t)
	command := "/invariants requires_material_check_before_production on"
	h.message(t, 900001, command)
	typed := lastAnswer(h)
	h.message(t, 900001, "/setup_stop")
	f := &speechFake{text: command}
	attachSpeech(t, h, f)
	deliverVoice(t, h)
	if lastAnswer(h) != typed {
		t.Fatal("voice confirmation differs", lastAnswer(h), typed)
	}
	rules, e := (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if e != nil || rules[len(rules)-1].IsActive {
		t.Fatal("voice changed rule without confirmation", e)
	}
	h.click(t, 900001, "Подтвердить изменение правила")
	rules, e = (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if e != nil || !rules[len(rules)-1].IsActive {
		t.Fatal(e)
	}
	f.text = "записывай склад прямо из LLM в SQLite, минуя InventoryService"
	deliverVoice(t, h)
	requireAnswer(t, h, "отклонено")
	h.message(t, 900001, "/invariant_trace")
	requireAnswer(t, h, "DENY")
}
func TestVoiceBoundedAdmissionCancellationAndRevocation(t *testing.T) {
	h, _, _, _ := assemblyFixture(t)
	block := make(chan struct{})
	f := &speechFake{text: "/invariants", block: block}
	attachSpeech(t, h, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := h.bot.startAudio(ctx, voiceMessage(900001)); e != nil {
		t.Fatal(e)
	}
	if e := h.bot.startAudio(ctx, voiceMessage(900001)); e != nil {
		t.Fatal(e)
	}
	requireAnswer(t, h, "занято")
	// Text remains responsive while inference is in flight.
	h.message(t, 900001, "/invariants")
	requireAnswer(t, h, "Инварианты мастерской")
	close(block)
	var c speechCompletion
	select {
	case c = <-h.bot.speechResults:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	h.bot.speechWait.Wait()
	if _, e := h.bot.WS.DB().Exec(`UPDATE users SET is_active=0 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	before := len(h.sent)
	if e := h.bot.finishAudio(c); e != nil {
		t.Fatal(e)
	}
	if len(h.sent) != before+1 || strings.Contains(lastAnswer(h), "Инварианты мастерской") {
		t.Fatal("revoked identity reached text flow")
	}
}
