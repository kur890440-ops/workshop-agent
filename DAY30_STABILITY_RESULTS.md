# Day30 — стабильность, лимиты и администрирование

> **Дополнение от 11.10.2026: исправление ложного CONTEXT_LIMIT_EXCEEDED.** Ниже исходный отчёт сохранён как история первоначальной реализации. Его утверждения об отсутствии BPE-токенизатора и неподтверждённом повторе вопроса о форме Земли заменены результатами раздела 11.

Дата: 11 октября 2026 года. Проект: WORKSHOP_AGENT. Основание: `DAY30_DIAGNOSTICS.md` и задание `@L3:DAY30_STABILITY_LIMITS_ADMIN @PRESERVE`.

## 1. Результат и границы проверки

Реализованы общие квоты Telegram/Web, ограничение Local/Cloud, ограниченная очередь, асинхронные Web-запросы с отменой, preflight контекста, административные команды и сохранение настроек в существующем SQLite. Исправлена привязка Web-истории к мастерской и проверка HTTP body. Архитектура сохранена:

```text
ONE workshop-agent.exe
ONE application process
ONE Agent pipeline

Telegram update ────────────────┐
Web POST -> session/CSRF/body ──┤
                              v
                   shared stability.Runtime
                  domain user quota / queue
                              v
             existing Bot / processMessage / Agent
             permissions / FSM / invariants / confirmations
                              v
                 Hybrid router / Local / Cloud
                   preflight + provider gates
```

Нового сервиса или отдельной БД нет. Существующие Caddy и Ollama остаются инфраструктурными компонентами. Composition root: `cmd/workshop-agent/main.go`; общий вход: `internal/telegram/web.go:HandleInbound`, `internal/telegram/bot.go:handleMessageContext`, `internal/telegram/stability.go:submitTelegram, EnableLimits`. Доказательства общего pipeline: `TestLimitsAdminDeterministicAndSharedBudget`, существующие `internal/telegram/web_test.go`, `TestHybridConsentContinuesPlanningOnceAndPreservesConfirmation`.

Финальный `go test ./... -count=1 -timeout 240s -json`: **532 PASS, 10 SKIP, 0 FAIL**, включая subtests. Сборка успешна. Отдельные реальные проверки Ollama пройдены; платная Cloud LLM не вызывалась. Реальная проверка всей цепочки с вопросом о форме Земли выявила оставшееся ограничение маршрутизации/консервативного бюджета; она не скрыта и описана ниже.

## 2. До и после

| Область | До, по диагностике | Теперь | Код и доказательство |
|---|---|---|---|
| Квота | Сессионная Web-квота, нет общей пользовательской | Domain user, 20/min суммарно Telegram/Web; новые сессии не сбрасывают бюджет | `internal/stability/queue.go:Charge, Submit`; `TestSharedUserQuotaAcrossSessionsAndAsyncError`, `TestLimitsAdminDeterministicAndSharedBudget` |
| Local | Сериализация глобальным mutex | Gate вокруг router и генераций, default 1; Cloud имеет отдельный gate | `internal/llm/admission.go:ControlledProvider.Generate`; `TestGateQueueFairnessCancellationAndShrink`, `TestStabilityLiveOllama` |
| Очередь | Один Web busy slot | Ограниченный приём, FIFO одного пользователя, чередование пользователей, deadline и отмена | `internal/stability/queue.go`; `TestRuntimeDialogOrderQueueAndCancel`, `TestFiveConcurrentWebRequestsQueue` |
| Глобальная блокировка | Удерживалась при inference | Освобождается на сетевое ожидание, затем восстанавливается и проверяются права | `internal/telegram/stability.go:enterApplication`; `internal/llm/admission.go:Call`; `TestInferenceYieldsGlobalStateLockAndCancellation` |
| Web-ошибка | HTTP 200 выглядел как успех | Принятие 202; terminal FAILED с error_code | `internal/webchat/requests.go:action`; `TestAsyncErrorCancellationBodyAndOwnership` |
| Отмена | Context терялся внутри pipeline | Контекст запроса до providers, явный cancel API | `internal/telegram/hybrid.go`, `internal/llm/hybrid.go`; `TestProviderCancellation`, реальный Ollama cancel |
| Контекст | Финальный prompt не проверялся | Preflight до transport, явный estimate, отказ CONTEXT_LIMIT_EXCEEDED | `internal/llm/admission.go:Preflight`, `internal/llm/provider.go`; `TestFinalPromptPreflightBeforeTransport` |
| История | Недостаточная workshop attribution | workshop_id, текущие права при чтении; legacy без attribution скрыта | `internal/storage/stability.go:migrateStability`, `internal/webchat/service.go:History`; `TestHistoryWorkshopRevocationAndLegacyRetention` |
| Настройки | Нет Telegram admin runtime API | Детерминированные /limits, SQLite overrides, аудит | `internal/telegram/stability.go:limitsCommand`, `internal/stability/settings.go:Store.Change`; `TestUserBudgetPersistenceAndAuthorization` |
| HTTP body | Неполная проверка, trailing JSON, maxlength != bytes | Полный body, EOF, UTF-8, byte limit в UI, различимые storage errors | `internal/webchat/requests.go:action`, `internal/webchat/http.go`; `TestAsyncErrorCancellationBodyAndOwnership` |

## 3. Лимиты и управление

| Ключ /limits set | Default | Единицы / диапазон | Env |
|---|---:|---|---|
| rate | 20 | запросов пользователя/min, 1–600 | LIMIT_RATE |
| local_concurrency | 1 | генераций, 1–4 и не выше аппаратного ceiling | LIMIT_LOCAL_CONCURRENCY |
| cloud_concurrency | 2 | генераций, 1–8 | LIMIT_CLOUD_CONCURRENCY |
| queue | 5 | ожидающих, 1–100 | LIMIT_QUEUE |
| max_context | 16384 | бюджет контекста, 1024–16384 и не выше модели | LIMIT_MAX_CONTEXT |
| max_output | 2048 | резерв ответа, 16–4096 | LIMIT_MAX_OUTPUT |
| timeout | 120 | секунд от принятия, 5–600 | LIMIT_REQUEST_TIMEOUT |

Дополнительная зависимость: `max_output + 512 < max_context`; фактический model template учитывается отдельно перед transport. Аппаратный ceiling `LIMIT_LOCAL_CAPACITY=1` по умолчанию. Поднять local concurrency выше него через Telegram нельзя: `HARDWARE_CAPACITY_UNVERIFIED`. Повышение ceiling требует проверенной оператором конфигурации оборудования; автоматического VRAM benchmark нет. Источник: `cmd/workshop-agent/main.go`, `Settings.Validate`, `.env.example`.

Приоритет: validated SQLite runtime override → env → defaults. На момент развёртывания таблица overrides пуста. `/limits` показывает effective settings, а не только env. Изменения process-wide: администратор своей активной мастерской меняет общий лимит процесса. Это следует учитывать при нескольких мастерских.

Команды, только в личном Telegram-чате существующего OWNER/ADMIN:

```text
/limits
/limits set rate 20
/limits set local_concurrency 1
/limits set cloud_concurrency 2
/limits set queue 5
/limits set max_context 16384
/limits set max_output 2048
/limits set timeout 120
```

Авторизация использует `auth.ExistingTelegramUser` и `auth.Require(...WorkshopManage)`. LLM не интерпретирует команды. Web и обычный пользователь не могут менять настройки этим механизмом. `Store.Change` атомарно сохраняет override и `runtime_limit_audit`: actor, workshop, parameter, old/new value, created_at, без секретов. Настройки переживают создание нового Store из SQLite; тест `TestUserBudgetPersistenceAndAuthorization` проверяет значение и audit row. Проверка через настоящий Telegram после развёртывания пока не подтверждена; обработчик команд проверен с MOCK Telegram API, в том числе изменение 20 → 21 → 20.

Новые запросы получают snapshot настроек. Обычная смена лимита не отменяет принятые запросы. Уменьшение concurrency не отбирает занятые слоты; следующие выдаются после освобождения. Повышение будит ожидающих. Уменьшение queue ограничивает новый приём, уже принятые сохраняются. Доказательства: `TestGateQueueFairnessCancellationAndShrink`, `TestRuntimeConcurrencyChangeWakesWaiters`.

## 4. Admission, очередь и shared state

Один принятый пользовательский запрос расходует квоту один раз. Router и повторные внутренние генерации не увеличивают её. Окно — фиксированная минута от первого запроса пользователя; Retry-After вычисляется по остатку окна. Подтверждения имеют отдельный бюджет 60/min, не зависящий от обычной LLM-квоты. Login/auth ограничения сохранены. `/limits`, `/login`, `/start` идут по управляющему пути. Источник: `Runtime.Charge`, `Runtime.Submit`, `submitCallback`, `submitTelegram`; тесты бюджета и авторизации.

Один пользователь может иметь не более одного активного и одного ожидающего запроса. Это сильнее сериализации одного диалога и не позволяет занять всю очередь. Планировщик выбирает следующего пользователя с учётом предыдущего; внутри пользователя порядок сохраняется. Provider gates дополнительно ограничивают реальные генерации. Суммарный outstanding bound равен `queue + 1` (default 6), включая ожидание provider gate: для Local это одна генерация и до пяти ожидающих. Для смешанного Local/Cloud это намеренно консервативная общая граница, а не отдельные независимые очереди по пять.

Deadline начинается при приёме и включает ожидание очереди. Cancel удаляет ожидающее задание либо отменяет контекст активного. Диалоговая lease сохраняется до завершения. Глобальный `applicationMu` продолжает защищать hybrid context/pending, navigation, material lists и другие mutable поля Bot; его не удаляли. `Scope.Yield` освобождает его перед network inference, после чего resume заново проверяет context, активную мастерскую, роль, membership, а для Web — сессию и её срок/revocation. При изменении прав продолжение отклоняется. У коротких мутаций сохраняются существующие FSM, InvariantEngine и подтверждения.

Foreground MCP-ожидания для request-owned обработки проходят через тот же context и временное освобождение lock (`internal/telegram/stability.go:network`, `marketplace_query.go`, `mcp_stocks.go`, `ozon.go`). Результат не отделяется от lifecycle Web-запроса. Ранее существовавшие явно фоновые задачи имеют собственный lifecycle. Отмена не откатывает уже совершённый внешний эффект; автоматического повтора таких действий нет.

## 5. Web API и повторные запросы

| Операция / ситуация | HTTP / результат |
|---|---|
| POST /api/chat/messages; POST /api/confirmations/{id}, принято | 202 + request_id/status |
| GET /api/requests | Авторизованный список последних 30 запросов с текущими правами |
| GET /api/requests/{id} | Текущий status/error_code и сообщения запроса |
| POST /api/requests/{id}/cancel | Отмена своего запроса; QUEUED/RUNNING → CANCELLED |
| Нет сессии | 401 |
| Недостаточные права / CSRF | 403 либо скрытие недоступного объекта |
| Неверный JSON, неизвестное поле, trailing bytes, invalid UTF-8 | 400 |
| Полный body > 16384 bytes | 413 |
| User rate limit | 429 + Retry-After |
| Queue/user queue full | 429; без выдуманного Retry-After, когда ожидание неизвестно |
| Idempotency-Key с другим содержимым | 409 |
| Storage failure при admission/auth storage | 500, а не ложная rate-limit ошибка |
| Прикладная ошибка после принятия | status FAILED + error_code, исходное принятие остаётся 202 |

Состояния: QUEUED → RUNNING → COMPLETED / FAILED / CANCELLED. При ожидании следующего provider slot возможен возврат RUNNING → QUEUED. UI показывает русские подписи и кнопку отмены; polling раз в 3 секунды. Ограничение текста — 12000 UTF-8 bytes через TextEncoder в браузере и ту же byte-проверку сервера; общий JSON body дополнительно ограничен 16384 bytes. HTML maxlength удалён, поскольку считает другую величину.

Принятое задание связано с application root context, а не временем жизни POST. Закрытие вкладки не отменяет его. После перезагрузки UI получает сохранённый список. Polling не запускает Agent. Session-scoped Idempotency-Key сохраняется вместе с hash payload: повтор того же POST возвращает существующий request_id без повторного выполнения и квоты; другой payload с тем же ключом отклоняется. UI повторно использует ключ при сетевом retry. Источник: `internal/webchat/requests.go`, JS в Web Chat; доказательство: `TestIdempotentPostDoesNotReplay`.

При перезапуске процесса незавершённые сохранённые задания получают CANCELLED / PROCESS_RESTARTED. Автоматического replay нет. Пользователь должен оценить уже полученный результат перед новым намеренным действием. Polling ограничен отдельно (120/session/min), cancel — 60/user/min.

## 6. Preflight и безопасность истории

`llm.Call` / `ControlledProvider.Generate` проверяют финальный собранный prompt; Ollama учитывает дополнительно свой chat template и num_ctx, OpenRouter — сведения о context capacity. Это охватывает router, system/history/task/user/tool content, вошедший в итоговый prompt, и резерв ответа. Несовместимый запрос отклоняется до генерации с CONTEXT_LIMIT_EXCEEDED, пользовательский текст не обрезается ради допуска.

Точный tokenizer не интегрирован. Метод явно называется `conservative_utf8_byte_bound`: UTF-8 bytes используются как консервативная верхняя оценка, а НЕ точное число токенов; добавляется template/special-token reserve и output reserve. Это может давать ложные отказы для русского текста. Диагностика содержит token_count_method, InputEstimate, OutputReserve, ContextLimit, HistoryReduced. Источники: `internal/llm/admission.go:Preflight`, `internal/llm/provider.go:Capabilities, Generate`, `internal/llm/openrouter.go`, `internal/telegram/semantic.go`; тесты preflight и `TestHistoryReductionDiagnostic`.

У установленной Qwen3 `/api/show` сообщил context capacity 40960; установленный application cap 16384 ниже него. Шаблон модели занимал 1723 UTF-8 bytes; дополнительный резерв включает ещё 512. Отсутствие необходимых model capabilities не выдаётся за разрешение неограниченного контекста. Получение OpenRouter `/models` — чтение метаданных, не платный inference. Платная Cloud генерация не проверялась.

Миграция 122 добавляет workshop_id в web_messages, web_requests, runtime_limits, runtime_limit_audit. Используется существующий механизм backup перед недостающей миграцией и транзакционная миграция. Legacy-сообщения остаются в БД с NULL attribution и не показываются. Новые ответы/сообщения получают workshop provenance; History и request API проверяют текущие права именно на эту мастерскую. General Chat сейчас также имеет workshop scope — отдельное user-scoped хранилище не создавалось. При отзыве A и сохранении membership B история A недоступна. Тест `TestHistoryWorkshopRevocationAndLegacyRetention` проверяет отказ в выдаче и сохранение строк, включая legacy.

## 7. Матрица regression A–P

Все перечисленные автоматические тесты вошли в успешный финальный прогон. Это интеграционные/модульные проверки; слово LIVE к mock fixtures не применяется.

| Требование | Доказательство |
|---|---|
| A: 20 разрешены, 21 ограничен | `internal/stability/stability_test.go:TestUserBudgetPersistenceAndAuthorization`; `internal/webchat/stability_test.go:TestSharedUserQuotaAcrossSessionsAndAsyncError` |
| B: новая сессия не обходит квоту | `TestSharedUserQuotaAcrossSessionsAndAsyncError`: 20 HTTP 202, следующий через другую сессию 429 с Retry-After |
| C: общий Telegram/Web budget | `internal/telegram/stability_test.go:TestLimitsAdminDeterministicAndSharedBudget` |
| D: пять одновременно | `internal/webchat/stability_test.go:TestFiveConcurrentWebRequestsQueue`: 5 принятых HTTP 202, 5 COMPLETED; application MOCK |
| E: лимиты, fairness, порядок | `TestGateQueueFairnessCancellationAndShrink`, `TestRuntimeDialogOrderQueueAndCancel` |
| F: нет глобального lock во время LLM | `TestInferenceYieldsGlobalStateLockAndCancellation` |
| G: cancellation до provider | `internal/llm/admission_test.go:TestProviderCancellation`, Web cancellation regression |
| H: oversized не отправлен | `TestFinalPromptPreflightBeforeTransport`, live preflight |
| I/J: admin разрешён, viewer/Web запрещены | `TestLimitsAdminDeterministicAndSharedBudget`, `TestUserBudgetPersistenceAndAuthorization` |
| K: без рестарта | `TestUserBudgetPersistenceAndAuthorization`, `TestRuntimeConcurrencyChangeWakesWaiters` |
| L: сохранение настроек | Повторное создание Store из SQLite в `TestUserBudgetPersistenceAndAuthorization` |
| M: Agent failure != COMPLETED | `TestAsyncErrorCancellationBodyAndOwnership` и реальный отказ oversized после router failure |
| N: история после revocation | `TestHistoryWorkshopRevocationAndLegacyRetention` |
| O: общий Agent | `internal/telegram/web_test.go`, `TestLimitsAdminDeterministicAndSharedBudget`, live приложение с реальным Bot |
| P: confirmations/FSM/invariants | Полный existing suite, включая `TestDay15TelegramConfirmationAndAllowedActions`, `TestHybridConsentContinuesPlanningOnceAndPreservesConfirmation`, `TestTaskFSMOwnershipPermissionsAndFailure`, `TestAssemblyMenuDraftEditsAndFSM` |

Дополнительно: ownership чужого request_id, cancel/recovery, trailing body/oversized, идемпотентность POST, динамическое увеличение и уменьшение capacity, diagnostics сокращения истории. Источники: `internal/webchat/stability_test.go`, `internal/stability/stability_test.go`, `internal/telegram/stability_test.go`.

## 8. Реальные проверки и latency

Нагрузочные данные синтетические, SQLite тестов изолирован. Заказы и товары рабочей мастерской не менялись. Время — wall-clock конкретного запуска на текущем хосте, не performance SLA.

| Проверка | Реальный компонент / подмена | Принято / отклонено, результат | Время |
|---|---|---|---|
| 10 последовательных коротких генераций | REAL Ollama, Runtime, ControlledProvider; без полного Agent | 10 успешных | 5524 ms суммарно |
| 5 одновременных | REAL Ollama и общий Local gate | 5 принято, 0 отклонено; max local active = 1 | 207 ms суммарно, прогретая модель/короткие ответы |
| Rate | Реальный Runtime; первые 10 квотных запросов вызывали Ollama, следующие 10 только admission | 20 разрешены; 21-й RATE_LIMIT_EXCEEDED | Не является 20 реальными LLM-генерациями |
| Oversized | Реальный provider preflight | CONTEXT_LIMIT_EXCEEDED, transport не вызван | До генерации |
| Отмена активного HTTP к Ollama | REAL HTTP context cancellation | CANCELLED | 0 ms по округлённому client elapsed |
| Восстановление | REAL следующая генерация | PASS | В отдельном live test |
| «Что такое PETG?» | REAL Ollama + Bot + async Web + isolated SQLite; Telegram API MOCK | HTTP 202 → COMPLETED; GENERAL, Cloud calls = 0 | 2512 ms; local inference 2314 ms |
| Изменение /limits | Реальный command handler, Telegram HTTP API MOCK | 20 → 21 → 20, PASS | Не LIVE Telegram transport |

Артефакты: `.tmp/day30-stability/live-ollama.jsonl`, `live-application-petg.jsonl`, `web-concurrency.jsonl`. Тесты: `internal/llm/stability_live_test.go:TestStabilityLiveOllama`, `internal/telegram/stability_live_test.go:TestStabilityLiveApplicationReadOnly`.

**Отдельный неуспешный live сценарий сохранён:** вопрос «почему земной шар круглый?» дал невалидный ответ локального router (`VALIDATION_FAILED`), затем существующий WORKSHOP fallback. Консервативная оценка 15214 плюс output reserve 2048 превысила 16384, поэтому Web завершился FAILED / CONTEXT_LIMIT_EXCEEDED. Это корректный отказ admission, но неудовлетворительный ответ на простой вопрос. Доказательство: `.tmp/day30-stability/live-application.jsonl`. В том запуске диагностический hybrid log ещё содержал обобщённый APPLICATION_FAILED; после исправления терминальный код диагностик берётся через `stability.Code`. Повторный успешный PETG-тест не доказывает исправления качества router для всех вопросов. Сам вопрос о форме Земли на финальной сборке повторно не подтверждён.

Cancellation доказывает разрыв ожидания HTTP-клиента и передачу context; точное время прекращения GPU-вычислений внутри Ollama не измерялось. Отдельная статистика p95/p99 и длительный soak test не выполнялись.

## 9. Сборка, развёртывание и воспроизведение

```powershell
$env:PATH = 'C:\TEMP\go\go\bin;' + $env:PATH
$env:GOCACHE = 'C:\TEMP\WorkshopAgent\.tmp\go-cache'
go test ./... -count=1 -timeout 240s
go build -o .tmp/day30-stability/workshop-agent.exe ./cmd/workshop-agent

# Только при работающей локальной Ollama; платный Cloud эти тесты не вызывают.
$env:STABILITY_LIVE_LOCAL = '1'
go test ./internal/llm -run '^TestStabilityLiveOllama$' -count=1 -v
go test ./internal/telegram -run '^TestStabilityLiveApplicationReadOnly$' -count=1 -v
Remove-Item Env:STABILITY_LIVE_LOCAL
```

Финальный full-suite журнал: `.tmp/day30-stability/release-tests.jsonl`, 532 PASS / 10 SKIP / 0 FAIL. Опциональные live-тесты при обычном прогоне пропущены и запускались отдельно. `go test -race` не выполнен: CGO отключён по умолчанию, а после включения обнаружено отсутствие gcc. Это ограничение toolchain; отсутствие data races не заявляется. Доказательство: `.tmp/day30-stability/race-attempt.txt`.

По явному разрешению пользователя проверенная сборка установлена в `bin/workshop-agent.exe` и запущена скрыто. Первоначальная попытка копирования встретила sharing violation; повторная замена с остановкой точного checkout-owned процесса и проверкой хеша завершилась успешно.

- SHA256 установленного файла: `36CDBCF341A17960FC7D376B2A3AA0262DD0F2014625B6C46A599A4D8B99D81B`.
- На проверке после запуска: один процесс WorkshopAgent, PID 88856.
- Миграция 122 присутствует ровно один раз, overrides при запуске пусты.
- `https://192.168.11.41:8443/chat` доступна; `/chat/app.js` HTTP 200 и содержит новый `/api/requests`.
- HTTPS проверен с доверенным локальным CA `reports/workshop-lan-root.crt`.
- Неавторизованные `/api/chat/messages/status` и `/api/requests` возвращают 401.
- Существующий внешний интерфейс и отсутствие фильтра подсети сохранены; session/CSRF/workshop authorization остаются обязательными.
- Startup log подтверждает запуск Telegram polling, но это само по себе не доказательство успешного живого `/limits`.

Реальные идентификаторы пользователей, токены, cookies и содержимое рабочей истории в отчёт не включены. `.env` не изменялся; defaults и варианты env добавлены в `.env.example`.

## 10. Оставшиеся ограничения

1. Качество локального router не гарантировано: воспроизведён невалидный route на обычном вопросе. Консервативный byte estimate может отклонять фактически помещающийся prompt; exact tokenizer не интегрирован. Исправление стабильности не равно исправлению качества ответов.
2. Живой Telegram `/limits` и изменение через настоящий транспорт после развёртывания требуют подтверждения пользователя. Проверки command handler с MOCK Telegram API пройдены и не называются LIVE.
3. Платный Cloud inference, Cloud latency и отмена на стороне настоящего Cloud не проверялись. Использованы mock tests и чтение публичных capabilities.
4. Race detector не запущен из-за отсутствия C compiler; длительные нагрузочные и crash/fault-injection проверки не проводились.
5. Квотные счётчики находятся в памяти и сбрасываются при рестарте; настройки, аудит и Web lifecycle сохраняются в SQLite. Незавершённые задания после рестарта отменяются, не возобновляются автоматически.
6. Ошибки сохранения промежуточных/терминальных async status при отказе SQLite не имеют надёжного retry-журнала; отдельного recovery после panic job также нет. Эти аварийные сценарии не следует считать проверенными.
7. Local hardware ceiling — операторская конфигурация, не автоматическая оценка VRAM. General Chat хранится в workshop scope; legacy без достоверной attribution скрыта, но не удалена.

Работа ограничена Day30. Переход к следующему дню не выполнялся.


## 11. Исправление после скриншота: технический вопрос ошибочно превышал контекст

Подтверждено по metadata рабочей БД без извлечения содержимого истории: первый запрос был GENERAL, следующий получил WORKSHOP / WORKSHOP_DATA; итоговая byte-оценка InputEstimate=21226, OutputReserve=2048, ContextLimit=16384. Таким образом, ошибка возникла на расширенном служебном prompt, а не из-за длины короткого вопроса пользователя.

Изменения:

- `internal/llm/router.go:routeInstructions`: отделены общие технические объяснения о 3D-печати и материалах от чтения/изменения частных данных мастерской. Добавлены положительные GENERAL и отрицательные WORKSHOP примеры. Разрешения, FSM и подтверждения не менялись.
- `internal/llm/qwen_tokens.go`: in-process Go BPE из vocabulary/merges/special tokens установленной модели через verbose `/api/show`. Проверяются Qwen3, GPT2 tokenizer, Qwen2 pre-tokenizer, отсутствие BOS и известный SHA256 chat template. Используется pure-Go regexp2 v1.11.5 для совместимого предварительного разбиения. Подпроцесс Python при работе приложения не запускается.
- `OllamaClient.CheckPrompt` и `Capabilities`: content BPE + 512 safety reserve для проверенного шаблона + output reserve. Метод diagnostics `ollama_gguf_qwen2_bpe_with_template_reserve`. Полный итоговый бюджет НЕ выдаётся за точное число токенов Ollama: wrapper имеет запас. Cache привязан к model digest; `/api/tags` проверяется перед использованием, смена digest инвалидирует metadata. Неизвестный tokenizer/template сохраняет явно обозначенный byte fallback; повреждённые данные поддерживаемого tokenizer дают отказ.
- `ControlledProvider.Generate`: перед gate использует provider-specific preflight, поэтому старый byte preflight больше не отклоняет подходящий Qwen prompt до подсчёта BPE. Непосредственно перед Ollama transport проверка повторяется. Local gate, cancellation и текущий max_context=16384 сохранены. Повышения лимитов и обрезания вопроса нет.
- `internal/telegram/hybrid.go:hybridError`, `internal/telegram/stability.go:limitMessage`: сообщение об ошибке объясняет общий контекст (инструкции, история, вопрос, резерв ответа), вместо требования сократить короткое сообщение. `/limits` описывает подсчёт tokenizer + reserve с fallback для неподдерживаемых моделей.

Проверки:

1. `TestQwenLiveBudgetAndReference`: восемь независимых golden counts получены Hugging Face tokenizers по metadata того же GGUF. Русский/английский текст, contractions, пробелы/CRLF, combining mark, emoji, CJK, арабские цифры и special tokens совпали. Python использовался только для независимой тестовой сверки.
2. Тот же тест через ControlledProvider и REAL Ollama: русский prompt **58054 UTF-8 bytes**, BPE + safety reserve **11523**, реальный `prompt_eval_count` **11027**, output reserve **32**. Запрос принят в контексте 16384; старый byte guard отклонил бы его. Последующий prompt из 20000 цифр отклонён до `/api/chat`. В отличие от повторяющейся буквы `x`, цифры в Qwen pre-tokenizer дают достоверный oversized случай.
3. `TestQwenBPEMergePriorityAndCancellation`, `TestQwenUnsupportedTokenizerFallback`, `TestOllamaBudgetCacheFollowsDigest`: порядок merge, отмена, invalid UTF-8, fallback и инвалидирование cache при смене digest.
4. `TestStabilityLiveApplicationReadOnly`: REAL Ollama + общий Bot + async Web + изолированный SQLite, MOCK Telegram API, Cloud calls=0. Последовательно в одной сессии проверены точные вопросы:
   - «Объясни разницу между PETG и ABS для 3D-печати.» → COMPLETED / GENERAL; local 5448 ms.
   - «Почему повышение температуры сопла может ухудшить качество печати?» → COMPLETED / GENERAL; local 2807 ms.
   - «почему земной шар круглый?» → COMPLETED / GENERAL; local 2255 ms.
   Весь сценарий — 11009 ms. Это подтверждение маршрута и успешного ответа для этих примеров, не универсальная гарантия качества LLM.
5. REAL Ollama повторно: 10 последовательных генераций — 2768 ms; пять одновременно — 5 принятых/0 отклонённых, local max active=1, 233 ms. Rate, oversized, cancellation и следующая успешная генерация прошли.

Уточнение старого live evidence: при повторной проверке найден повреждённый кириллический литерал (`??? ????? PETG?`) в прежнем `TestStabilityLiveApplicationReadOnly`. Поэтому первоначальную строку отчёта про точный русский PETG-вопрос нельзя считать доказательством именно этой формулировки. Тест исправлен; в новых логах сохранены корректные русские строки трёх вопросов выше.

Артефакты новой проверки: `.tmp/day30-context/tokenizer-live.txt`, `application-live.txt`, `queue-live.txt`, `all-tests.jsonl`. Старые FAILED записи и история пользователя не удалялись: прошлый запрос остаётся FAILED, его автоматического повторного выполнения нет.

Первичные источники для tokenizer format и regex implementation: [Qwen3-4B tokenizer](https://huggingface.co/Qwen/Qwen3-4B/blob/main/tokenizer.json), [regexp2](https://github.com/dlclark/regexp2). Фактические vocabulary/merges берутся из локальной установленной модели, а не скачиваются с этих страниц при выполнении приложения.

Финальная проверка исправления: `go test ./... -count=1 -timeout 240s -json` — **535 PASS, 11 SKIP, 0 FAIL**; дополнительно тест `/limits` после изменения диагностической подписи — PASS. Сборка успешна. Исправление установлено и процесс перезапущен по ранее данному разрешению; SHA256 `5B55B9FE84CDABEC319651958C57EA7C03DBBE052B3006033C456B5FA0B36A5C`. HTTPS `/chat` после запуска — 200.
