package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"workshop-agent/internal/speech"
)

// Real model and real encoded voice; Telegram HTTP and business services stay local.
func TestVoiceRealGigaAMOptional(t *testing.T) {
	model, audio := os.Getenv("SPEECH_TEST_MODEL"), os.Getenv("SPEECH_TEST_OGG")
	if model == "" || audio == "" {
		t.Skip("real GigaAM/Opus fixture not configured")
	}
	data, err := os.ReadFile(audio)
	if err != nil {
		t.Fatal(err)
	}
	cfg := speech.DefaultConfig()
	cfg.Enabled, cfg.ModelPath, cfg.RuntimePath = true, model, os.Getenv("SPEECH_TEST_RUNTIME")
	provider := speech.New(cfg)
	defer provider.Close()
	if !provider.Status(context.Background()).ModelLoaded {
		t.Fatal(provider.Status(context.Background()))
	}
	h, _, _, _ := assemblyFixture(t)
	h.bot.ConfigureSpeech(provider, cfg)
	h.bot.VoiceShowTranscript = false
	original := h.bot.HTTPClient.Transport
	downloads := 0
	h.bot.HTTPClient.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/getFile") {
			downloads++
			body := fmt.Sprintf(`{"ok":true,"result":{"file_path":"voice/real.oga","file_size":%d}}`, len(data))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		}
		if strings.Contains(r.URL.Path, "/file/") {
			downloads++
			return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
		}
		return original.RoundTrip(r)
	})
	fixture := fmt.Sprintf(`{"update_id":26006,"message":{"message_id":26006,"chat":{"id":900001,"type":"private"},"from":{"id":900001},"voice":{"file_id":"local-fixture","duration":12,"mime_type":"audio/ogg","file_size":%d}}}`, len(data))
	var update telegramUpdate
	if err := json.Unmarshal([]byte(fixture), &update); err != nil {
		t.Fatal(err)
	}
	before := len(h.sent)
	if err := h.bot.startAudio(context.Background(), update.Message); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-h.bot.speechResults:
		h.bot.speechWait.Wait()
		if c.err != nil || c.result.Status != speech.Success {
			t.Fatal(c.err, c.result)
		}
		if expected := os.Getenv("SPEECH_TEST_EXPECTED"); expected != "" && c.result.Text != expected {
			t.Fatal(c.result.Text, expected)
		}
		if err := h.bot.finishAudio(c); err != nil {
			t.Fatal(err)
		}
		if downloads != 2 || len(h.sent) != before+1 {
			t.Fatal(downloads, len(h.sent)-before)
		}
		var tracked int
		if err := h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id=26006`).Scan(&tracked); err != nil || tracked != 1 {
			t.Fatal(tracked, err)
		}
		t.Logf("real OGG -> real GigaAM -> same text handler -> one fixture response; transcript=%q", c.result.Text)
	case <-time.After(30 * time.Second):
		t.Fatal("voice pipeline timed out")
	}
}
