@PROJECT:WORKSHOP_AGENT
@L3:DAY26_TELEGRAM_VOICE_RUNTIME_WIRING_FIX
@PRESERVE

# Day26 Telegram speech runtime wiring — 2026-10-06

Root cause подтверждён: `SPEECH_ENABLED` отсутствовал в `.env` и в environment
работавшего процесса PID 114608. DefaultConfig.Enabled=false. CLI smoke ранее
выполнялся с временными environment overrides; speech test также включает provider
для диагностики. Эти настройки не включали speech в обычном Telegram startup.
Без overrides `speech status` воспроизвёл Enabled=false, LastError=DISABLED.

Процесс до исправления: `c:\TEMP\WorkshopAgent\bin\workshop-agent.exe`,
cwd=`c:\TEMP\WorkshopAgent\`, запуск 2026-10-06 16:13:25 Europe/Moscow.
Проверено чтением только CWD/executable/SPEECH_* его process parameters/environment;
остальные environment values не выводились и не сохранялись. Бинарник содержал
предыдущую рабочую speech-реализацию; замена старого executable не была причиной
первоначальной ошибки. К моменту перезапуска PID 114608 уже завершился.

Fallback находится в `internal/telegram/speech.go`, `speechError`:
Disabled / ModelUnavailable / RuntimeUnavailable / InvalidAssets →
«Распознавание речи недоступно. Отправьте сообщение текстом.»
В данном случае `Bot.startAudio`: `b.Speech == nil || !b.speechConfig.Enabled`;
истинной была **!b.speechConfig.Enabled**, provider не nil, но disabled.

Startup wiring уже был корректным: main → config.Load → speech.New(cfg.Speech)
→ bot.ConfigureSpeech(speechProvider,cfg.Speech) → bot.StartContext.
Один provider в composition root, initialization синхронная, Telegram polling
начинается после ConfigureSpeech. Новый provider на каждое voice не создаётся.
Неправильного startup order, stub/wrong instance и фактического неверного пути
в исследованном процессе не было.

Изменения:

- В локальном ignored `.env` включён SPEECH_ENABLED=true; portable model/runtime paths.
  Безопасный default для других установок остаётся disabled.
- Для layout app/executable и app/bin/executable `.env` и относительные speech paths
  определяются от app root. go run сохраняет repository-cwd fallback. Абсолютные
  paths сохраняются; drive-rooted Windows paths не маскируются как относительные.
- Startup trace: executable/PID/start time/version/build commit, cwd/app root,
  raw/resolved paths/exists, enabled/provider/model/runtime/device/errors.
- startAudio проверяет действительный provider status до download. READY идёт
  в Transcribe; disabled/failed init сохраняют fallback. Auth остаётся до download/inference.
- Voice trace: received, auth, enabled/present/loaded, getFile/download, decode и
  transcription status, вызов text handler и успешный Telegram send/edit acknowledgement.
  Никаких transcript/token/keys/URL/file IDs в trace. DecodeStatus — только диагностика;
  audio/model/frontend/CTC поведение не менялось.
- Text и voice по-прежнему сходятся в Bot.handleMessage → Bot.processMessage;
  далее существующий WorkshopAgent.HandleMessageForWorkshop при соответствующем маршруте.
- Ответ считается отправленным по успешному Telegram API send/edit acknowledgement
  для этого chat во время синхронной доставки. Background sends в тот же chat в этом
  интервале могут также подтвердить acknowledgement; это не отдельная гарантия live E2E.

Новый процесс оставлен запущенным:

- executable: `C:\TEMP\WorkshopAgent\bin\workshop-agent.exe`;
- PID: 41936;
- start: 2026-10-06 16:29:26.4019817 Europe/Moscow;
- application version: 0.21.4;
- build commit: d55816e393cc054153a0c25e1ec1b33a9b21a531, modified=true;
- SHA256 executable: 7BD08435E48BC1123DBA9F57F22F5434EF8363347336910FFEDBB53A4B46862C.

Реальный обычный startup после fix:
`enabled=true provider=gigaam runtime_loaded=true model_loaded=true device=cpu last_error=""`.
Оба resolved paths существуют под app root. CUDA fallback объяснён CPU-only DLL;
это ожидаемый CPU режим, не speech unavailable. Startup evidence сохранён отдельно.

Проверки:

- Regression A: READY → вызов provider, fallback отсутствует — PASS.
- B: disabled config/provider → fallback, 0 downloads/inferences — PASS.
- C: failed init/nil provider → fallback, 0 downloads/inferences — PASS.
- D/E: transcript и typed text → одинаковый existing handler result,
  одна invocation/response/tracked incoming message — PASS.
- Auth, confirmations/invariants и real GigaAM+OGG Telegram fixture — PASS.
- Paths: tests обоих layouts + фактический speech status из `.tmp` cwd — PASS.
- go test ./... — PASS (real WAV/OGG env включены).
- go build ./... и сборка fresh executable — PASS.
- targeted go vet и git diff --check — PASS.

**LIVE Telegram Voice E2E: NOT RUN** — после запуска новой сборки ещё не получено
новое реальное voice-сообщение пользователя. Бот работает; требуется одно voice.
Ожидаемый trace: received → authorized → downloaded → decode SUCCESS →
transcription SUCCESS → text_handler_called=true → response_sent=true.
Рабочие логи: `.tmp/day26-live.stdout.log`, `.tmp/day26-live.stderr.log`.

Модель, preprocessing, CTC, decoder architecture, FSM, invariants, MCP/LLM,
SQLite и scheduler не переписывались. Новых speech-возможностей не добавлено.

## Live follow-up 2026-10-06 16:32–16:36 Europe/Moscow

Первый реальный voice после wiring fix получен 16:32:41. Trace подтвердил:
authorized=true, speech_enabled=true, provider_present=true, runtime_loaded=true,
model_loaded=true, telegram_file_requested=true, telegram_file_downloaded=true.
Файл audio/ogg, 33008 bytes. Decode вернул UNSUPPORTED_AUDIO за 5.5918 ms;
GigaAM inference и text handler не запускались. Поэтому live E2E — FAIL at decode,
а не прежняя ошибка disabled provider. Точная причина внутри OGG/Opus пока неизвестна.

Добавлены safe diagnostic reasons для OGG header/lacing/CRC/sequence/Opus packet/
EOS-granule branches и in-process Opus decoder error. Алгоритм и ограничения
decoder, model/frontend/CTC не изменялись; аудио и file IDs не сохраняются.
Speech/Telegram tests и executable build — PASS.

Бот перезапущен 16:36:47 МСК, PID 132288, executable SHA256
C0E3095442EE29F20A01F313B4F334683DDD7442ABE9EB9D64CBCD1FDDC96BB3.
Новый бинарник проверен hash comparison перед запуском.
Текущий log: `.tmp/day26-decode-live.stderr.log`.
Для точного диагноза требуется повторить то же voice; исправление decoder
и успешный live E2E пока не заявляются.

## OGG EOF fix — 2026-10-06 16:42 Europe/Moscow

Скриншот live trace в 16:38:29 установил причину: eos=false,
pending_packet_bytes=0, packet_count=77, decoded_samples=72000, preskip=312.
Opus-пакеты успешно декодированы; отказ вызван обязательным `!eos` в конце
Normalizer.ogg. Поле granule=0 в прежнем trace было неинициализированным EOS
значением, а не доказательством нулевого granule последней страницы.

Fix: EOF после полностью проверенных страниц и законченных пакетов допускается
без EOS; берутся decoded samples минус preskip. При наличии EOS сохраняется
прежний end trimming. CRC, порядок страниц, ограничения размера/длительности,
обрезанные страницы и незаконченные packets по-прежнему проверяются.
Основание: https://www.rfc-editor.org/rfc/rfc7845.html#section-3
Отсутствие EOS само по себе не позволяет доказать полноту исходной записи;
поддерживается фактически полученный полный набор страниц/пакетов.

TestOggOpusEOFWithoutEOS: clean EOF, EOS trimming, bad CRC, truncated page,
unfinished packet, no audio, invalid granules, page after EOS, sequence gap — PASS.
Дополнительно создана копия официального речевого OGG с удалённым EOS и
пересчитанным CRC, без изменения аудиопакетов. go test ./... с этим sample:
real GigaAM повторно, reference transcript comparison и Telegram fixture — PASS.
go build ./... и свежий executable — PASS.
Исходный пользовательский voice локально не сохранён: его повторное live
распознавание после fix ещё не подтверждено.

409 Conflict: одновременно работали пользовательский PID 12416 и запущенный
ассистентом PID 132288. PID 132288 остановлен; пользовательский процесс затем
также завершился. Запущен один новый PID 9508 в 16:42:02 МСК.
Executable SHA256: B3D99C5BC2C98DB262DF6BAA344F39ECE807E28DE16010B3CD4B268915D75B83.
Текущий лог `.tmp/day26-eof-live.stderr.log`. Не запускать второй экземпляр
параллельно; для live acceptance требуется повторное voice-сообщение.
