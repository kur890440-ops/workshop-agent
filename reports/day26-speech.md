# Day26 — итог, 2026-10-05

Реализован optional local speech input. Full Go suite и Windows native smoke
прошли. Реальное GigaAM распознавание пока не проверено: model assets и три
речевых samples отсутствуют. Это ограничение приёмки, не успешный ASR benchmark.

## Existing text flow и архитектура

`main.go` создаёт services/Bot, signal context и startup/shutdown lifecycle.
Config: internal/config; logging: log; diagnostics: CLI mcp-status/report commands.
Отдельного HTTP health service нет. LLM/MCP interfaces и Telegram HTTP fakes
используются как существующий pattern; tests работают с temporary SQLite.

Найден flow: `getUpdatesContext → telegramUpdate.Message → Bot.handleMessage
→ processMessage → private-chat/sender check → UpsertUser/trackMessage
→ command/form/task handlers → workshop resolution
→ WorkshopAgent.HandleMessageForWorkshop`.
Authorization: auth.Require; identity/workshop: workshops.Service.
agent/memory.go использует InvariantEngine; agent/task_fsm.go передаёт
TransitionRequest существующему memory.TaskStateMachine. Подтверждения остаются
в текущих Telegram callback/task/completion handlers. Не каждый text является
Task transition: voice проходит ровно тот же branch routing.

Voice adapter проверяет authorization до getFile/download, ограничивает размеры,
длительность и количество jobs, получает файл текущим HTTPClient, вызывает ASR.
Completion возвращается в serial event loop, повторно проверяет auth/workshop,
создаёт копию typed telegramMessage с transcript и metadata и вызывает точно
**Bot.handleMessage(&m)**. Прямых Task mutation/MCP calls и копии business logic
в speech нет. Polling вынесен в goroutine; business handlers остаются serial.

## Files/packages и устройство

- internal/speech/types.go: SpeechRecognitionProvider, AudioInput, typed result,
  statuses/errors, Config и Normalizer interface.
- internal/speech/provider.go: GigaAMProvider, одна startup session, bounded
  admission, serial inference, reusable lifecycle и safe Close.
- internal/speech/assets.go: version/model/frontend/vocab/blank validation и SHA-256.
- internal/speech/audio.go, frontend.go: WAV/OGG Opus/MP3, mono/resampling,
  точный log-mel frontend и CTC greedy decode.
- internal/speech/onnx*.go: узкий native C API 23 wrapper через purego v0.10.0,
  UTF-16 Windows paths/LoadLibraryExW, CUDA V2 и RunOptionsSetTerminate.
- internal/speech/cli.go: speech status/test/day26 внутри workshop-agent.exe.
- internal/speech/*test.go и testdata: unit/native/optional model tests, golden.
- internal/telegram/speech.go, speech_test.go, bot.go: adapter, inbound metadata,
  error UX, async processing с serial delivery, lifecycle и regression tests.
- internal/config/speech.go, speech_test.go, config.go: existing env loader.
- cmd/workshop-agent/main.go: wiring и lifecycle.
- scripts/speech_prepare.py: dev-only export packaging/reference/native fixtures.
- .env.example, .gitignore, go.mod/go.sum; docs/DAY26_SPEECH.md.
- Собран bin/workshop-agent.exe. .env не менялся. Прежние пользовательские
  изменения .env.example сохранены; security-audit не затрагивался.

Нужны model.onnx, config.json, vocab.json, manifest.json; reference-config.yaml
сохраняется exporter для аудита. Python требуется лишь для development export
и golden generation. **Python не нужен в production; ffmpeg subprocess отсутствует.**
Модель/runtime не скачиваются на startup; отсутствие assets не ломает text chat.

OGG/Opus декодируется pure-Go pion/opus v0.1.0; WAV decoder локальный;
MP3 — go-mp3 v0.3.4. Resampling windowed-sinc low-pass, отдельно от features.
Frontend: 16 kHz, FFT/window=320 periodic Hann, hop=160, 64 HTK mels,
power=2, center=false, norm=None, ln(clamp(1e-9,1e9)), [1,64,time].
CTC использует asset vocabulary/blank ID и output lengths, collapse repeats
перед удалением blanks; только trim краёв без LLM correction.

Auto пробует CUDA session, при разрешённом fallback создаёт CPU session.
CUDA с fallback=false возвращает error. Выбранный device логируется; GPU memory
не измеряется. Status показывает enabled/provider/model/device/model_loaded/
runtime_loaded/last_error. Structured trace без полного transcript и secrets.

## Проверки

| Проверка | Результат |
|---|---|
| go test ./... | PASS, все пакеты |
| go vet speech/telegram/config | PASS |
| Windows build workshop-agent.exe | PASS |
| speech status | Работает, default DISABLED |
| Official FeatureExtractor → Go | PASS, 6336 features, max abs delta 0.0000212192535; tolerance 0.0002 |
| WAV mono/stereo/resampling/limits/invalid audio | PASS |
| OGG Opus decode/pre-skip/CRC rejection | PASS |
| Missing model/altered hash/CPU fallback/success/empty | PASS |
| Context cancellation/timeout | PASS |
| Native ONNX repeated session + Unicode path | PASS, искусственная модель, output «аа» |
| Native ONNX Loop cancellation, два вызова | PASS, около 21 ms на timeout 20 ms; ORT подтвердил terminate |
| Unauthorized voice | PASS: TestVoiceUnauthorizedNoDownloadNoInference; 0 downloads, 0 inference |
| Authorized voice → common handler once | PASS: TestVoiceAuthorizedSameTextHandlerExactlyOnce |
| Same confirmation + InvariantEngine | PASS: TestVoiceUsesSameConfirmationAndInvariantEngine; правило не меняется до callback, запрещённое действие → DENY |
| ASR failure leaves text working | PASS: TestVoiceFailureAndEmptyLeaveTextWorking |
| Bounded jobs, responsive text, revoked access | PASS: TestVoiceBoundedAdmissionCancellationAndRevocation |
| No subprocess/common delivery architecture | PASS: TestProductionSpeechHasNoSubprocess |
| Real GigaAM integration/reference transcript | SKIP: нет assets/sample |

Golden получен прямым вызовом official gigaam/preprocess.py, SHA-256:
`d4cd47b7c07664c0aab682148155a1fb2eab4829a91730cb14b1c75dddfcc22f`,
torch 2.14.1+cpu, torchaudio 2.11.0+cpu. 26 ABI slots wrapper проверены по
Microsoft ONNX Runtime v1.23.2 header. Native DLL 1.30.0 прошла C API 23 tests
без запуска Python. Native fake graph не проверяет качество распознавания.

## Три Day26 tests, performance и blockers

| Sample | Audio duration | Processing time | Recognized text | RTF | Статус |
|---|---|---|---|---|---|
| A: короткая русская фраза | — | — | — | — | Не предоставлен |
| B: длинная русская фраза | — | — | — | — | Не предоставлен |
| C: терминология мастерской | — | — | — | — | Не предоставлен |

Runner `speech day26 A.wav B.wav C.wav` готов. После установки assets он выдаёт
для каждого sample transcript/status/duration/processing/RTF и timings decode,
preprocess, inference, tokens. Реальные показатели GigaAM в этой сессии не получены.
Инструкции export, paths, config и запуска находятся в docs/DAY26_SPEECH.md.

Ограничения: проверена Windows amd64, Unix loader не запускался. Физический CUDA
execution не проверен, GPU memory telemetry не добавлена. Ogg mapping family 0,
без chained/multiplexed streams; WAV PCM/float32. Отдельный MP3 fixture не прогонялся.
Resampler не заявлен bit-exact с FFmpeg. Native wrapper следует повторно проверять
при обновлении DLL. Startup session creation синхронный; timeout/cancellation
действуют на request inference. Voice может завершиться после следующего typed
сообщения; delivery повторно проверяет identity/workshop.

Нельзя объявить все 17 acceptance criteria подтверждёнными до реального GigaAM
и Telegram voice прогона с assets. Optional real-model test явно SKIP, не PASS.
Следующий день и другие подсистемы не реализовывались.
