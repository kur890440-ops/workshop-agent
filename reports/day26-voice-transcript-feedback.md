@PROJECT:WORKSHOP_AGENT
@L3:DAY26_VOICE_TRANSCRIPT_FEEDBACK
@PRESERVE

Implemented TELEGRAM_VOICE_SHOW_TRANSCRIPT (default true; invalid bool rejected).
Config: internal/config/telegram_voice.go, config.go; main passes value to Bot.
Bot default is also true. ASR architecture unchanged.

finishAudio revalidates auth/workshop, then sends plain-text transcript UI through
existing api(sendMessage) with reply_parameters to original voice. On failure:
warning without transcript/URL/secrets, then same raw transcript reaches existing
Bot.handleMessage exactly once. No user wait, second Agent pipeline or MCP call.
Existing message splitting handles long outbound text; confirmation flow unchanged.

Changed files in this iteration:
- .env.example
- cmd/workshop-agent/main.go
- internal/config/config.go
- internal/config/telegram_voice.go
- internal/config/telegram_voice_test.go
- internal/telegram/bot.go
- internal/telegram/speech.go
- internal/telegram/speech_feedback.go
- internal/telegram/speech_feedback_test.go
- internal/telegram/speech_test.go
- internal/telegram/speech_real_test.go
- docs/DAY26_SPEECH.md
- this report

Tests A/B/C/D PASS: UI enabled/disabled, send failure continuation, confirmation.
A/B/C assert one Transcribe and one semantic interpretation, expected outbound
count/order, original voice reply ID, and one tracked incoming message.
Existing pipeline tests explicitly disable optional UI to retain their original
business-response count assertions.
go test ./... PASS. go build ./... PASS. Executable build PASS.

Single live process updated: PID 73140, bin/workshop-agent.exe.
Executable SHA256: 25A1B5AD84DC806A5F82FF0184DD7A2E1FE98C96D1A2A417CF03273AC8A0595C.
UI live delivery not asserted; tests use Telegram HTTP fixtures.
