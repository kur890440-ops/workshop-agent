package telegram

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"workshop-agent/internal/speech"
)

type statusSpeech struct {
	*speechFake
	state speech.SpeechProviderStatus
}

func (s *statusSpeech) Status(context.Context) speech.SpeechProviderStatus { return s.state }

func TestVoiceRuntimeWiring(t *testing.T) {
	ready := speech.SpeechProviderStatus{Enabled: true, Provider: "gigaam", ModelLoaded: true, RuntimeLoaded: true}
	for _, tc := range []struct {
		name         string
		enabled      bool
		state        speech.SpeechProviderStatus
		present, run bool
	}{
		{"A_ready", true, ready, true, true},
		{"B_config_disabled", false, ready, true, false},
		{"B_provider_disabled", true, speech.SpeechProviderStatus{LastError: speech.Disabled}, true, false},
		{"C_initialization_failed", true, speech.SpeechProviderStatus{Enabled: true, Provider: "gigaam", LastError: speech.ModelUnavailable}, true, false},
		{"C_nil_provider", true, ready, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, _ := assemblyFixture(t)
			f := &speechFake{text: "/invariants"}
			downloads := attachSpeech(t, h, f)
			cfg := speech.DefaultConfig()
			cfg.Enabled = tc.enabled
			var provider speech.SpeechRecognitionProvider
			if tc.present {
				provider = &statusSpeech{f, tc.state}
			}
			h.bot.ConfigureSpeech(provider, cfg)
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)
			before := len(h.sent)
			if tc.run {
				deliverVoice(t, h)
				if f.calls != 1 || *downloads != 2 {
					t.Fatal(f.calls, *downloads)
				}
				if strings.Contains(lastAnswer(h), speechError(speech.Disabled)) {
					t.Fatal("READY routed to unavailable fallback")
				}
				if !strings.Contains(logs.String(), `text_handler_called\":true`) || !strings.Contains(logs.String(), `response_sent\":true`) {
					t.Fatal(logs.String())
				}
			} else {
				if err := h.bot.startAudio(context.Background(), voiceMessage(900001)); err != nil {
					t.Fatal(err)
				}
				requireAnswer(t, h, speechError(speech.Disabled))
				if f.calls != 0 || *downloads != 0 {
					t.Fatal("unavailable reached download/inference")
				}
			}
			if len(h.sent) != before+1 {
				t.Fatal("response count", len(h.sent)-before)
			}
		})
	}
}

func TestVoiceTypedAndTranscriptSameHandlerExactlyOnce(t *testing.T) {
	typed, _, _, _ := assemblyFixture(t)
	voice, _, _, _ := assemblyFixture(t)
	command := "/invariants"
	typed.message(t, 900001, command)
	f := &speechFake{text: command}
	attachSpeech(t, voice, f)
	before := len(voice.sent)
	deliverVoice(t, voice)
	if lastAnswer(typed) != lastAnswer(voice) || f.calls != 1 || len(voice.sent) != before+1 {
		t.Fatal("typed/voice downstream mismatch")
	}
	var tracked int
	if err := voice.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id=26001`).Scan(&tracked); err != nil || tracked != 1 {
		t.Fatal(tracked, err)
	}
}
