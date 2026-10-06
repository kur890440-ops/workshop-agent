package telegram

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"workshop-agent/internal/invariants"
)

func TestVoiceTranscriptFeedback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		show, fail bool
	}{{"A_enabled", true, false}, {"B_disabled", false, false}, {"C_send_failure", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h, semantic, _ := semanticFixture(t)
			if !h.bot.VoiceShowTranscript {
				t.Fatal("default must be true")
			}
			semantic.raw = `{"action":"get_all_material_stock"}`
			const transcript = "материлы"
			fake := &speechFake{text: transcript}
			attachSpeech(t, h, fake)
			h.bot.VoiceShowTranscript = tc.show
			original := h.bot.HTTPClient.Transport
			attempted := 0
			h.bot.HTTPClient.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/sendMessage") {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var payload map[string]any
					if err := json.Unmarshal(raw, &payload); err != nil {
						t.Fatal(err)
					}
					if strings.HasPrefix(payload["text"].(string), "🎙 Распознано:") {
						attempted++
						if semantic.calls != 0 {
							t.Fatal("feedback sent after business handler")
						}
						if payload["text"] != "🎙 Распознано:\n«"+transcript+"»" {
							t.Fatal(payload["text"])
						}
						reply := payload["reply_parameters"].(map[string]any)
						if reply["message_id"] != float64(26001) {
							t.Fatal(reply)
						}
						if tc.fail {
							return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":500}`)), Header: http.Header{}}, nil
						}
					}
				}
				return original.RoundTrip(r)
			})
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)
			before := len(h.sent)
			deliverVoice(t, h)
			wantFeedback := 0
			if tc.show {
				wantFeedback = 1
			}
			if attempted != wantFeedback || fake.calls != 1 || semantic.calls != 1 {
				t.Fatal(attempted, fake.calls, semantic.calls)
			}
			wantReplies := 1
			if tc.show && !tc.fail {
				wantReplies++
			}
			if len(h.sent)-before != wantReplies {
				t.Fatal("duplicate/missing outbound", len(h.sent)-before)
			}
			requireAnswer(t, h, "Материалы:")
			var tracked int
			if err := h.bot.WS.DB().QueryRow(`SELECT COUNT(*) FROM telegram_messages WHERE message_id=26001`).Scan(&tracked); err != nil || tracked != 1 {
				t.Fatal(tracked, err)
			}
			if tc.fail && !strings.Contains(logs.String(), "transcript feedback failed") {
				t.Fatal("missing warning")
			}
		})
	}
}

func TestVoiceTranscriptFeedbackConfirmation(t *testing.T) {
	h, _, w, _ := assemblyFixture(t)
	fake := &speechFake{text: "/invariants requires_material_check_before_production on"}
	attachSpeech(t, h, fake)
	h.bot.VoiceShowTranscript = true
	before := len(h.sent)
	deliverVoice(t, h)
	if len(h.sent) != before+2 || !strings.HasPrefix(h.sent[before]["text"].(string), "🎙 Распознано:") || fake.calls != 1 {
		t.Fatal("feedback/confirmation ordering")
	}
	rules, err := (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if err != nil || rules[len(rules)-1].IsActive {
		t.Fatal("applied without confirmation", err)
	}
	h.click(t, 900001, "Подтвердить изменение правила")
	rules, err = (invariants.InvariantRegistry{}).Workshop(h.bot.WS.DB(), 1, w)
	if err != nil || !rules[len(rules)-1].IsActive {
		t.Fatal("confirmation failed", err)
	}
}
