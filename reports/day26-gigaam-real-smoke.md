@PROJECT:WORKSHOP_AGENT
@L3:DAY26_GIGAAM_ASSETS_AND_REAL_SMOKE_TEST
@PRESERVE

# Day26: реальные GigaAM assets и local smoke — 2026-10-06

Результат: реальный официальный bundle подготовлен; WAV и OGG/Opus распознаются
в Go-процессе. Local acceptance PASS. LIVE TELEGRAM E2E NOT RUN.
Предыдущий отчёт `day26-speech.md` сохраняет историю первоначальной реализации.

## Contract и официальный export

До загрузки проверены `internal/speech/provider.go`, `assets.go`, `frontend.go`,
`onnx.go` и tests. Исходный provider принимает float32 features [1,64,T] и
int64 feature_lengths [1], ожидает float32 logits [1,Tout,34] и длину [1].
Имена читаются из config.json. Ранее длина выхода принималась только как int64.
CTC выполняется в Go: argmax, collapse соседних повторов, исключение blank,
charwise vocabulary, trim краёв; учитывается encoded_lengths.

Использован **вариант A**, без изменения official exporter:

```python
model = gigaam.load_model("v3_ctc", device="cpu", fp16_encoder=False,
                          use_flash=False, download_root=...)
model.to_onnx(export_directory, dtype=torch.float32)
```

Source: https://github.com/salute-developers/GigaAM
Commit: `7447938d791c4f3e643386ee22c33777004293a5`.
Официальный `gigaam/model.py` экспортирует `log_probs`, `encoded_lengths`.
Triton token-ID export не использовался: он потребовал бы другого decode contract.
Checkpoint получен с официального CDN:
`https://cdn.chatwm.opensmodel.sberdevices.ru/GigaAM/v3_ctc.ckpt`.
Официальный loader проверил собственный checksum checkpoint.
Затем получены `v3_ctc.onnx` и `v3_ctc.yaml`. Отдельного SentencePiece файла
для charwise v3_ctc не требуется: vocabulary взят из официального config.

Реальная ONNX-диагностика выполнена **до inference**:

| Tensor | dtype | Shape в ONNX |
|---|---|---|
| features | float32 | [batch_size,64,seq_len] |
| feature_lengths | int64 | [batch_size] |
| log_probs | float32 | [batch_size,seq_len,34] |
| encoded_lengths | int32 | [batch_size] |

Символьные имена времени совпадают в export; это не означает одинаковые длины
input/output. Go использует batch=1. Реальный frontend example: [1,64,1128].

**Исправлена фактическая несовместимость:** Go теперь читает int32 либо int64
encoded_lengths. Logits и CTC contract сохранены; повторного argmax нет.
Добавлены LoadErrorDetail/FallbackReason с сообщением ONNX Runtime;
семантические error codes сохранены через wrapping.

## Bundle

```text
assets/gigaam-v3-ctc/
  model.onnx
  manifest.json
  config.json
  vocab.json
  reference-config.yaml
  onnx-contract.json
  metadata.json
```

model.onnx: **885274810 bytes** (844.264 MiB).
SHA256: `814a3bf0082465fdfae75d5cd0527e0ce892e0950cb62912911f740374bada99`.
Два независимых запуска official export в этом окружении дали одинаковый SHA256.
Это не обещание одинаковых bytes на всех платформах/версиях tools.

`manifest.json` сохраняет existing schema: map filename → SHA256. В нём шесть
hashes для всех остальных файлов bundle; runtime по существующему контракту
проверяет три обязательных файла model.onnx/config.json/vocab.json.
Копия manifest и ONNX diagnostics сохранены в `reports/day26-real-smoke/`.
`config.json`: version=1, model=v3_ctc, charwise, blank_id=33, frontend и имена
двух inputs/двух outputs. `vocab.json`: 33 символа, ID 0 — пробел; blank не является
символом словаря. `reference-config.yaml` — фактический official export config.
`metadata.json` — family/source/commit, checkpoint SHA256, export path, dtype,
sample rate, Python/dependency versions. `onnx-contract.json` — проверенные
names/dtypes/shapes. Эти metadata нужны для воспроизводимости, не для inference.
Большие assets и `.tmp` уже исключены из Git; model в history не добавлялся.

## Воспроизведение

Из repository directory, Python 3.13 + Git:

```powershell
python scripts/prepare_gigaam_v3_ctc.py
```

Helper создаёт/использует `.tmp/day26-venv`, получает официальный pinned checkout,
устанавливает зафиксированные build dependencies, скачивает weights, экспортирует
FP32, проверяет ONNX names/ranks/dtypes/classes и checker, упаковывает модель
со встроенными weights, считает и проверяет SHA256. В конце: bundle path,
model size, SHA256 и validation=PASS. Полная команда проверена.
`--reuse-export` проверяет и упаковывает уже существующий export без нового export.
Последний проверенный запуск этой команды завершился exit code 0.
Python >=3.11 нужен для helper; dependency lock проверен на Python 3.13 Windows.

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

Reference helper скачивает официальный example.wav (16kHz mono PCM16),
кодирует OGG/Opus через in-process libsndfile, вычисляет reference transcript
через official infer_onnx с numpy waveform input (без official ffmpeg loader),
сохраняет frontend features. Sample source и SHA256 находятся в reference.json.
В production этот helper не вызывается.

## Frontend и Windows paths

Official config: 16000 Hz; FFT/window=320; hop=160; 64 HTK mel bins;
periodic Hann; center=false; power=2; mel norm=None;
ln(clamp(power,1e-9,1e9)); [1,64,T]; T=1+floor((N-320)/160).
Go preprocessing **не изменялся**, несовместимости параметров не обнаружено.
На настоящем WAV max absolute log-mel error=0.0006256103515625.
У official torch frontend float32 против float64 ошибка=0.0006425305324384567
на том же тихом bin (flat index 63258, log power около -16.18).
Поэтому real-speech test использует tolerance=0.001 log units (~0.1% power).
Существующий white-noise golden сохраняет более строгий tolerance=0.0002.
Transcript совпадает с official reference полностью.

В этом checkout функции resolvePath нет; применяется filepath.Abs и рабочий
каталог процесса. Defaults уже были project-relative, пример теперь использует
`assets/...` без `./`. Leading slash в Windows означает корень диска, не repo.
Добавлен и пройден TestSpeechWindowsProjectRelativePaths: model/runtime paths
разрешаются от cwd, а `/assets/...` не считается application-relative.
Запускать из repository/application directory; drive letter не hardcoded.

## Status, device, timings

`speech status`: Enabled=true, ModelLoaded=true, RuntimeLoaded=true,
LastError="" (existing эквивалент отсутствия ошибки), Device=cpu.
Попытка auto → CUDA действительно выполнена. Exact fallback reason:
`cuda: CUDA options: RUNTIME_NOT_AVAILABLE: CUDA execution provider is not enabled in this build.`
Текущая production DLL — ранее установленный CPU ONNX Runtime 1.30.0;
build-time reference ONNX Runtime — 1.23.2. GPU execution не подтверждено.

| Input | Duration s | decode_ms | preprocess_ms | inference_ms | ctc_decode_ms | total_ms | Device | Status |
|---|---:|---:|---:|---:|---:|---:|---|---|
| WAV PCM16 mono 16kHz | 11.290 | 0.5076 | 15.2949 | 443.3402 | 0* | 459.1427 | CPU | SUCCESS |
| OGG/Opus mono | 11.290 | 759.4882 | 14.5152 | 510.5080 | 0* | 1284.5114 | CPU | SUCCESS |

*Таймер вернул 0 для очень короткого CTC decode; это ниже доступного разрешения,
а не заявление об отсутствии вычислений. total_ms — Transcribe; initial session
loading/hashing не входит в эту величину. RTF: WAV 0.040668; OGG 0.113774.
OGG декодируется существующим Go decoder при 48kHz и нормализуется до 16kHz.
Это сгенерированный Telegram-like Opus, а не запись, полученная live от Telegram.

Оба transcript и official reference одинаковы:

> ничьих не требуя похвал счастлив уж я надеждой сладкой что дева с трепетом любви посмотрит может быть украдкой на песни грешные мои у лукоморья дуб зеленый

Два последовательных реальных вызова TestRealGigaAMOptional сравнивают engine
и native session handle до/после: same_native_session=true для обоих.
В финальном тесте total=550.2162 ms и 464.2651 ms; evidence — integration-tests.txt.

## Telegram, tests, ограничения

**LIVE TELEGRAM E2E NOT RUN.** Token настроен (значение не выводилось).
Live-процесс бота не был запущен; новый согласованный voice/test chat не получен.
Для live проверки запрошены test chat_id и новый voice. Это остаётся blocker
только для live E2E, не для local ASR acceptance.

TestVoiceRealGigaAMOptional: Telegram JSON update fixture → local getFile/file
HTTP fixtures с настоящим OGG → production decoder/GigaAM → finishAudio →
существующий Bot.handleMessage → одна fixture response и одна tracked message.
HTTP не обращался к Telegram. Business/LLM использует существующий offline test
harness (его fallback response), поэтому успешный live LLM response не заявляется.
Существующие authorization/confirmation/invariant voice tests также прошли.

- `go test ./...`: PASS; реальные model/audio env включены; native int64/cancellation fixtures также включены.
- `go build ./...`: PASS.
- `go build -o bin/workshop-agent.exe ./cmd/workshop-agent`: PASS.
- Windows paths, real reference frontend, repeated native session, real Telegram fixture: PASS.
- ffmpeg subprocess = **none** (также не использовался для подготовки samples).
- production Python dependency = **none**.
- CUDA blocker: CPU-only DLL; CUDA/GPU acceptance не выполнена, предусмотренный CPU fallback исправен.
- Одна официальная запись в двух форматах не является оценкой качества на всех голосах/шуме.

Telegram architecture, Task FSM, InvariantEngine, MCP/LLM layers, SQLite и scheduler
не переписывались. Работа ограничена этой Day26 итерацией.
