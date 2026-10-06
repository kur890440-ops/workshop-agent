# Day26: GigaAM v3 CTC local speech input

Speech добавлен как optional вход в existing text pipeline. Production: один
`bin/workshop-agent.exe`, один OS process. Python, ffmpeg subprocess, ASR server
и worker executable не используются.

## Подготовка assets один раз

Установите официальный [GigaAM](https://github.com/salute-developers/GigaAM) в
отдельное dev-окружение по его инструкции. Зафиксируйте commit и dependencies.
Нужен **v3_ctc FP32 charwise**, не E2E/RNNT.

```python
import gigaam, torch
model = gigaam.load_model("v3_ctc")
model.to_onnx(dir_path="export", dtype=torch.float32)
```

В dev-окружении с `onnx` и `omegaconf`:

```powershell
python scripts/speech_prepare.py package assets/gigaam-v3-ctc --export export
```

Скрипт проверяет config/FP32/charwise vocabulary, собирает external weights
внутрь ONNX и создаёт:

| Файл | Назначение |
|---|---|
| model.onnx | Модель со встроенными weights |
| config.json | Версия 1, v3_ctc, frontend, tensor names, blank ID |
| vocab.json | Vocabulary из официального cfg.decoding.vocabulary |
| manifest.json | SHA-256 трёх файлов выше |
| reference-config.yaml | Оригинальный config для аудита |

Приложение проверяет hashes, известную версию и поддерживаемый frontend.
Manifest проверяет целостность; доверенный источник выбирается при подготовке.
Модель не скачивается на startup. При отсутствии assets: MODEL_NOT_AVAILABLE,
текстовый чат продолжает работать.

Native Windows x64 runtime **1.23.2 или совместимый с C API 23** берите из
[Microsoft releases](https://github.com/microsoft/onnxruntime/releases/tag/v1.23.2).
Положите DLL вместе с accompanying libraries и лицензией в `assets/onnxruntime`.
Для GPU нужны CUDA build и совместимые CUDA/cuDNN. Windows loader использует
абсолютный путь, LoadLibraryExW с ограниченным search path и UTF-16 model paths.
Относительные пути считаются от рабочего каталога.

В этой сессии runtime 1.30.0 скопирован из dev-wheel в
`assets/onnxruntime/onnxruntime.dll` и проверен с C API 23. Для deployment берите
контролируемый native distribution с лицензией. Реальная модель не установлена.
Assets/DLL/dev-venv исключены из Git. После export Python не нужен.

## Включение и CLI

Существующий `.env` не изменялся. Добавьте настройки из `.env.example`:

```dotenv
SPEECH_ENABLED=true
SPEECH_PROVIDER=gigaam
SPEECH_MODEL_PATH=./assets/gigaam-v3-ctc
SPEECH_RUNTIME_PATH=./assets/onnxruntime/onnxruntime.dll
SPEECH_DEVICE=auto
SPEECH_CPU_FALLBACK=true
SPEECH_MAX_FILE_SIZE_MB=20
SPEECH_MAX_DURATION=120s
SPEECH_TIMEOUT=90s
SPEECH_MAX_CONCURRENT_JOBS=1
```

По умолчанию speech выключен. Используется существующий `config.Load`.
Невалидный config отклоняется; model/runtime startup failure не убивает text bot.

```powershell
cd C:\TEMP\WorkshopAgent
.\bin\workshop-agent.exe speech status
.\bin\workshop-agent.exe speech test .\samples\A.wav
.\bin\workshop-agent.exe speech day26 .\samples\A.wav .\samples\B.wav .\samples\C.wav
```

Test/day26 включают provider для offline запуска независимо от SPEECH_ENABLED,
не открывают application DB и не подключаются к Telegram. JSON содержит
provider/model/device/text/status, durations, decode/preprocess/inference/tokens
и real-time factor. Duration values — наносекунды. Ошибка даёт exit code 1;
day26 всё равно выводит результат каждого sample.

A — короткая чистая русская фраза; B — длинная; C — терминология мастерской.
Речевые samples с реализацией не поставляются.

## Lifecycle и pipeline

Interface `internal/speech.SpeechRecognitionProvider` отделяет typed encoded
AudioInput от Normalizer/PCM и inference. GigaAMProvider создаёт одну session
на startup и переиспользует её. Admission bounded, default 1, без растущей очереди.
MaxConcurrentJobs ограничивает download/normalization; native inference одной
session сериализован. Close ждёт active lease, освобождает session/env/DLL.
Timeout/shutdown отменяют native Run через RunOptionsSetTerminate; watcher
завершается до освобождения RunOptions. Startup session creation синхронный.

Auto пробует CUDA V2, затем CPU при разрешённом fallback. CUDA с fallback=false
возвращает ошибку, не переключается на CPU. Device означает выбранный provider,
а не обещание исполнения всех operators на GPU. GPU memory telemetry не добавлена.
Binding: узкий локальный C API 23 wrapper через `ebitengine/purego v0.10.0`;
26 ABI slots сверены с официальным header v1.23.2. CGO/C compiler не требуется.

Telegram: private chat/sender → existing identity/membership/read authorization
→ metadata limits/admission → getFile/download через текущий HTTPClient
→ normalize → transcribe → completion channel → повторная auth и проверка
той же workshop → копия telegramMessage с Text и Speech metadata
→ **Bot.handleMessage → Bot.processMessage** → существующие Agent/Task/MCP.

Polling выполняется отдельной goroutine. Callback/text/transcript handlers
остаются последовательными в основном loop. Text может обогнать распознаваемый
gолос; при delivery проверяются текущий доступ и workshop. Voice/Audio поддержаны,
caption не подменяет transcript. Metadata: input_type=VOICE, provider/model,
исходные chat/sender/message IDs, audio/processing duration. Metadata находится
на inbound message; отдельная SQL-схема или хранение audio не добавлены.
InvariantEngine, Task FSM, preconditions и confirmations не изменялись.
Structured trace не содержит transcript, credentials, URLs и headers.
Speech status доступен через existing CLI pattern и startup log.

## Codecs, frontend, CTC

OGG/Opus: `pion/opus v0.1.0`, in-process pure Go; CRC, sequence, serial,
lacing/continuation, pre-skip, EOS trim и output gain. Mapping family 0 only;
chained/multiplexed streams отклоняются. WAV: PCM 8/16/24/32-bit и float32,
до 8 channels; mono averaging. MP3: `go-mp3 v0.3.4`.
Resampling — windowed-sinc low-pass до 16 kHz; не заявляется bit-exact с FFmpeg.

Frontend соответствует [official FeatureExtractor/SpecScaler](https://github.com/salute-developers/GigaAM/blob/main/gigaam/preprocess.py):
FFT/window 320, periodic Hann, hop 160, 64 HTK mel bands, power=2, center=false,
mel norm=None, ln(clamp(1e-9,1e9)), layout [1,64,time]. Config export проверяется
строго; другие варианты отклоняются. CTC: argmax → collapse adjacent repeats
→ remove blank → charwise vocab → trim краёв; учитывается actual output length.
Blank ID/vocab читаются из assets. Автокоррекция transcript не добавлена.

## Проверки

```powershell
go test ./...
go vet ./internal/speech ./internal/telegram ./internal/config
python scripts/speech_prepare.py golden internal/speech/testdata --reference path/to/GigaAM/gigaam/preprocess.py
python scripts/speech_prepare.py native-fixture .tmp/native-model
python scripts/speech_prepare.py native-cancellation .tmp/native-cancellation
$env:SPEECH_TEST_RUNTIME="$PWD/assets/onnxruntime/onnxruntime.dll"
$env:SPEECH_TEST_NATIVE_MODEL="$PWD/.tmp/native-model"
$env:SPEECH_TEST_CANCEL_MODEL="$PWD/.tmp/native-cancellation"
go test ./internal/speech -run 'TestNative|TestFrontend' -v
```

Golden получен прямым импортом official Python FeatureExtractor; metadata,
source/PCM SHA-256 и versions находятся в testdata/frontend.json. Golden WAV и
features включены в Git. Unit tests не требуют Python/runtime/большой модели.
Native fixtures искусственные и не доказывают распознавание речи.

```powershell
$env:SPEECH_TEST_MODEL="$PWD/assets/gigaam-v3-ctc"
$env:SPEECH_TEST_AUDIO="$PWD/samples/A.wav"
$env:SPEECH_TEST_EXPECTED="результат официальной модели"
go test ./internal/speech -run TestRealGigaAMOptional -v
```

Для reference comparison получите expected text через official
`gigaam.onnx_utils.infer_onnx` из исходного export на том же mono PCM16 16 kHz WAV.
Сравнивается text после trim краёв. В этой сессии реальных assets/sample нет;
reference transcript comparison не выполнялось. Проверялась Windows amd64;
Unix loader предусмотрен, но не запускался.
## Обновление 2026-10-06: воспроизводимая подготовка и реальные smoke tests

Теперь assets готовятся одной dev-командой из корня проекта:

```powershell
python scripts/prepare_gigaam_v3_ctc.py
```

Она получает официальный GigaAM commit
`7447938d791c4f3e643386ee22c33777004293a5`, устанавливает зависимости в `.tmp/day26-venv`,
скачивает v3_ctc и использует обычный `model.to_onnx(dtype=torch.float32)`.
До packaging проверяются реальные names/dtypes/shapes. Выход encoded_lengths
официального export — int32; Go adapter теперь принимает int32 и int64.
Manifest остаётся map filename → SHA256. Помимо runtime-файлов, bundle содержит
reference-config.yaml, onnx-contract.json и metadata.json для воспроизводимости.
Полный список build dependencies закреплён в `scripts/gigaam-build-requirements.txt`.

Ранее описанное отсутствие реальных assets/samples относится к первоначальной
итерации. На 2026-10-06 реальные WAV и OGG/Opus пройдены, reference transcript
совпал, повторное использование native session подтверждено. Frontend не менялся;
real-speech numerical tolerance 0.001 log units обоснован сравнением official
float32/float64; первоначальный golden tolerance 0.0002 сохраняется.

Текущая DLL выбирает CPU: `CUDA execution provider is not enabled in this build.`
Status теперь показывает FallbackReason; ModelLoaded и RuntimeLoaded — true.
LIVE TELEGRAM E2E NOT RUN; integration fixture использует реальный GigaAM и OGG.
Полный отчёт, timings, hash и ограничения:
[day26-gigaam-real-smoke.md](../reports/day26-gigaam-real-smoke.md).

Reference sample и Opus без ffmpeg готовятся так:

```powershell
.tmp/day26-venv/Scripts/python.exe scripts/gigaam_smoke_reference.py
$env:SPEECH_ENABLED='true'
$env:SPEECH_DEVICE='auto'
$env:SPEECH_MODEL_PATH='assets/gigaam-v3-ctc'
$env:SPEECH_RUNTIME_PATH='assets/onnxruntime/onnxruntime.dll'
./bin/workshop-agent.exe speech status
./bin/workshop-agent.exe speech test .tmp/gigaam-example.wav
./bin/workshop-agent.exe speech test .tmp/gigaam-example.ogg
```

Для real integration tests:

```powershell
$env:SPEECH_TEST_MODEL="$PWD/assets/gigaam-v3-ctc"
$env:SPEECH_TEST_RUNTIME="$PWD/assets/onnxruntime/onnxruntime.dll"
$env:SPEECH_TEST_AUDIO="$PWD/.tmp/gigaam-example.wav"
$env:SPEECH_TEST_OGG="$PWD/.tmp/gigaam-example.ogg"
$env:SPEECH_TEST_FEATURES="$PWD/.tmp/gigaam-example.features.f32"
$env:SPEECH_TEST_EXPECTED=(Get-Content .tmp/gigaam-reference.json -Raw -Encoding UTF8 | ConvertFrom-Json).transcript
go test ./internal/speech ./internal/telegram ./internal/config -run 'TestRealGigaAMOptional|TestVoiceRealGigaAMOptional|TestSpeechWindowsProjectRelativePaths' -v
```

Пути разрешаются относительно рабочего каталога процесса. В Windows `/assets/...`
означает корень диска; используйте `assets/...` и запускайте из app/repository directory.

## Обновление 2026-10-06: обычный Telegram startup

Успешный `speech test` не означает, что speech включён у Telegram-процесса:
эта CLI-команда включает provider для диагностики. Для обычного startup нужны
`SPEECH_ENABLED=true` в `.env` и перезапуск процесса. В текущем локальном `.env`
настройка включена; безопасный default для остальных установок остаётся false.

Теперь `.env` и относительные speech paths разрешаются от application root
(layout app/executable или app/bin/executable); запуск из произвольного cwd
не меняет speech assets. Это заменяет прежнее правило cwd для speech paths.
Существующие остальные relative paths приложения здесь не переопределяются.
Startup log содержит только safe speech/process diagnostics. Voice trace содержит
этапы auth/download/decode/transcription/text handler/response, без transcript и credentials.

Root cause, regression tests и текущий live статус:
[day26-runtime-wiring.md](../reports/day26-runtime-wiring.md).

## Day26: показ распознанного текста Telegram Voice

`TELEGRAM_VOICE_SHOW_TRANSCRIPT=true` (default true) загружается через
`internal/config/telegram_voice.go` и передаётся Bot в composition root.
При false распознавание и обычная обработка продолжаются без UI-сообщения.

После успешного Transcribe и повторной проверки доступа/workshop finishAudio
отправляет plain-text reply на исходный voice:

```text
🎙 Распознано:
«<transcript>»
```

Отправка использует существующий Telegram API client напрямую, без Agent.
Затем тот же c.result.Text ровно один раз передаётся Bot.handleMessage.
Ожидания подтверждения распознавания нет; обычные confirmations команд сохраняются.
Если отправка не удалась, выводится warning без текста/credentials и обработка
продолжается. Длинный UI-текст делится существующим materialMessageParts;
исходный transcript для handler не изменяется. Обычные Audio attachments этим
voice-only UI не затрагиваются.

Regression tests: включено/выключено, ошибка отправки, порядок UI до business
handler, один semantic call и сохранение confirmation flow.
