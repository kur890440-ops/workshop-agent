# Day30 Diagnostics — WORKSHOP_AGENT

Дата: 11 октября 2026 года. Объект: рабочее дерево `C:\TEMP\WorkshopAgent`, включая незакоммиченные изменения. Это диагностический снимок, а не заключение о любом другом коммите или развёртывании.

## 1. Executive Summary

**Итог: Day30 реализован частично.** Встроенный Web Chat, вход через Telegram, общий обработчик сообщений, проверки прав, локальный провайдер и управляемый cloud fallback присутствуют. Основные недоработки относятся к сквозному контролю контекста, отмене запросов, справедливости нагрузки, контракту ошибок API и отзыву доступа к сохранённой Web-истории.

Подтверждённые результаты:

- **IMPLEMENTED:** Web HTTP-маршруты подключены к тому же `Bot`, который обслуживает Telegram; отдельного Web Agent нет. См. `main`, [cmd/workshop-agent/main.go:298–311](cmd/workshop-agent/main.go#L298), `HandleInbound`, [internal/telegram/web.go:9–26](internal/telegram/web.go#L9).
- **IMPLEMENTED:** cookie authentication, браузерная привязка одноразового входа, CSRF-проверки и повторная проверка членства. См. `ConfirmLogin/Complete/Authenticate`, [internal/webchat/service.go:107–231](internal/webchat/service.go#L107), `Handler`, [internal/webchat/http.go:78–114](internal/webchat/http.go#L78).
- **PARTIAL:** rate limit существует, но сообщения ограничиваются по сессии, а не по пользователю; общей квоты Web/Telegram нет. Повторный вход даёт новый бюджет. `action`, [internal/webchat/http.go:186–205](internal/webchat/http.go#L186); `processMessage`, [internal/telegram/hybrid.go:86–148](internal/telegram/hybrid.go#L86). Воспроизведено: 20 ответов 200, затем 429; новая сессия того же пользователя — 200.
- **PARTIAL:** размеры Web-ввода и части памяти ограничены, но реального токенного admission control для полного prompt нет. `Generate`, [internal/llm/provider.go:95–143](internal/llm/provider.go#L95); `Build`, [internal/memory/context.go:97–139](internal/memory/context.go#L97). MOCK-провайдер получил полный prompt размером 500 000 байт при `num_ctx=16384`.
- **PARTIAL:** сериализация защищает общий Bot, но один медленный запрос блокирует другие диалоги; отмена HTTP не доходит до LLM. `handleMessage`, [internal/telegram/bot.go:774–789](internal/telegram/bot.go#L774); `processMessage`, [internal/telegram/hybrid.go:141–149](internal/telegram/hybrid.go#L141). В изолированном тесте после отмены родителя provider context оставался активным без deadline.
- **PARTIAL:** ошибка прикладного обработчика возвращается как HTTP 200 / `status=complete`. `action`, [internal/webchat/http.go:232–244](internal/webchat/http.go#L232). Восстановление следующим запросом работает, но клиент не получает машинно различимого статуса ошибки.
- **PARTIAL:** права на операции повторно проверяются, однако история не имеет workshop provenance. После отзыва доступа к мастерской A при сохранении доступа к B старая история доступна. `Authenticate/History`, [internal/webchat/service.go:219–230](internal/webchat/service.go#L219), [265–283](internal/webchat/service.go#L265); схема [internal/storage/web_chat.go:7–10](internal/storage/web_chat.go#L7). Изолированная проверка воспроизвела это поведение.

**P0 не подтверждены.** P1: перечисленные пробелы контекста, cancellation/глобальная блокировка, квоты пользователя, история после частичного отзыва доступа и контракт ошибок API. Это не доказательство отсутствия других уязвимостей: полный pentest, race detector и отказоустойчивость при аварийном завершении не выполнялись.

Существующий `go test ./... -count=1 -json`: **518 PASS, 8 SKIP, 0 FAIL** на уровне тестовых событий, включая subtests. Дополнительно выполнено **8 изолированных диагностических тестов**, все подтвердили ожидаемые наблюдения. Прохождение диагностического теста, воспроизводящего дефект, не означает соответствия механизма требованиям. См. раздел 9.

Обозначения: **IMPLEMENTED** — механизм подтверждён кодом и указанными проверками; **PARTIAL** — существует, но имеет существенные границы; **MISSING** — соответствующий механизм не найден в исследованном пути; **UNVERIFIED** — нет достаточного фактического подтверждения. **REAL** обозначает настоящий транспорт/код/сервис, **MOCK** — подставной обработчик или LLM. Они могут сочетаться в одном тесте.

## 2. Architecture / Request Flow

Фактическая схема:

```text
Браузер -> Caddy HTTPS -> встроенный net/http Server
  -> cookie + Origin + session authorization
  -> Web busy(1) + session rate limit
  -> message.Inbound -> Bot.HandleInbound -> Bot.handleMessage
                                             |
Telegram update -----------------------------+
  -> applicationMu -> processMessage -> локальный MessageRouter
  -> GENERAL: ответ / WORKSHOP: существующий прикладной pipeline
  -> HybridClient -> Local Ollama
                  -> разрешённый fallback -> экран согласия
                  -> точное однократное согласие -> OpenRouter
  -> доменные обработчики, permissions, invariants, confirmations
  -> Telegram sendMessage либо WebOutput -> web_messages -> polling
```

Composition root: `main`, [cmd/workshop-agent/main.go:244–311](cmd/workshop-agent/main.go#L244). Создание Hybrid: `newHybridClient`, [cmd/workshop-agent/hybrid.go:11–32](cmd/workshop-agent/hybrid.go#L11). Входной DTO и выходные кнопки: [internal/message/message.go:4–19](internal/message/message.go#L4). Web-адаптация не дублирует бизнес-логику: [internal/telegram/web.go:10–49](internal/telegram/web.go#L10).

В Web используются отрицательные conversation IDs, производные от Web session ID; Telegram ID пользователя берётся из аутентифицированной записи. Клиент не передаёт произвольный user/workshop ID для выполнения сообщения. `action`, [internal/webchat/http.go:216–235](internal/webchat/http.go#L216); `inboundUser`, [internal/telegram/web.go:28–42](internal/telegram/web.go#L28).

Рабочий контекст привязан к user/workshop/conversation session. Перед роутером запрашиваются права на активную мастерскую. Роутер получает текущее сообщение, ограниченную историю пользователя и краткое состояние задачи; не весь каталог. `processMessage`, [internal/telegram/hybrid.go:94–148](internal/telegram/hybrid.go#L94). Ошибка роутера приводит к безопасному маршруту WORKSHOP, а не к исполнению произвольного ответа: `Route`, [internal/llm/router.go:68–88](internal/llm/router.go#L68).

Ответ API не содержит сам ответ LLM: POST выполняет обработку синхронно, затем возвращает request ID; UI читает историю polling-запросами. `action`, [internal/webchat/http.go:221–244](internal/webchat/http.go#L221); JS [261–270](internal/webchat/http.go#L261). SSE/WebSocket, отдельная публичная очередь задач и endpoint результата конкретного request ID в этом Handler отсутствуют.

## 3. Web API / Authentication

### 3.1. Endpoints

Источник регистрации: `Service.Handler`, [internal/webchat/http.go:78–94](internal/webchat/http.go#L78).

| Метод и путь | Назначение | Authentication / ограничения | Типичные статусы |
|---|---|---|---|
| GET `/chat` | HTML чата; без сессии создаёт pending login | Публичная страница; создание входа 5/IP/мин | 200; 429 при ошибке CreateLogin |
| GET `/chat/app.js` | Код браузера | Публичный ресурс | 200 |
| GET `/chat/style.css` | Стили | Публичный ресурс | 200 |
| GET `/api/login/status` | Получение подтверждённой сессии | Browser nonce cookie; 60/IP/мин | 200 pending/authorized; 410; 429 |
| GET `/api/chat/messages/status` | Последние 200 сообщений | Session cookie; 60/session/мин | 200; 401; 429; 500 |
| POST `/api/chat/messages` | Отправка текста | Session + Origin + специальный header; shared busy; 20/session/мин | 200; 400; 401; 403; 429; 500 |
| POST `/api/confirmations/{id}` | Нажатие существующей кнопки | Те же проверки и та же квота, что у сообщений | Те же транспортные статусы; прикладной отказ может быть сообщением при 200 |
| POST `/logout` | Отзыв текущей сессии | Session + Origin + header | 200; 401; 403; 500 |

Детали статусов: `chat/poll/session/history/action/logout`, [internal/webchat/http.go:117–257](internal/webchat/http.go#L117). При включённом HTTPS общий transport guard может вернуть 403 ещё до endpoint.

**HTTP API реализован как API браузерного чата.** Внешний клиент технически может использовать тот же протокол после Telegram-подтверждения, с cookie и нужными заголовками. Независимого machine-to-machine API key, OAuth/service account или Bearer-схемы в исследованном Handler нет — **MISSING** как отдельная возможность, не дефект существующего cookie-контракта. Источник: полный список маршрутов и `session`, [internal/webchat/http.go:78–94](internal/webchat/http.go#L78), [162–169](internal/webchat/http.go#L162).

### 3.2. Вход и сессия

1. `CreateLogin`: криптографически случайные 5 байт кода (8 Base32-символов) и 32 байта nonce; в БД хеши. Код показывается для команды Telegram `/login`. [internal/webchat/service.go:48–55](internal/webchat/service.go#L48), [85–106](internal/webchat/service.go#L85).
2. `ConfirmLogin`: лимит Telegram user ID, существующий авторизованный пользователь, срок действия, число попыток, сравнение hash; подтверждение создаёт запись сессии в транзакции. [107–166](internal/webchat/service.go#L107).
3. `Complete`: предъявление браузерного nonce, одноразовое потребление, случайный session token и хранение его hash. [178–218](internal/webchat/service.go#L178).
4. `Authenticate`: token hash + expiry + revoked_at, повторная проверка активного пользователя/доступа хотя бы к одной мастерской. [219–230](internal/webchat/service.go#L219), `ExistingTelegramUser`, [internal/auth/telegram_access.go:7–35](internal/auth/telegram_access.go#L7).
5. `logout/Revoke/Cleanup`: отзыв и последующая очистка истёкших записей. [internal/webchat/http.go:246–257](internal/webchat/http.go#L246), [internal/webchat/service.go:232–246](internal/webchat/service.go#L232).

Cookie: HttpOnly, SameSite=Strict, Path=/, Secure при HTTPS URL, MaxAge из TTL. `cookie`, [internal/webchat/http.go:18–24](internal/webchat/http.go#L18). Заголовки no-store, CSP self, nosniff, no-referrer и HSTS для HTTPS: `Handler`, [95–105](internal/webchat/http.go#L95). POST требует точного Origin и `X-Workshop-Request: 1`, [106–113](internal/webchat/http.go#L106). Это защита браузерного запроса, а не самостоятельная аутентификация.

### 3.3. Конфигурация и доступность

| Параметр | Значение по умолчанию | Фактическая проверка |
|---|---|---|
| `WEB_ENABLED` | false | В текущем `.env` true |
| `WEB_LISTEN_ADDRESS` | `127.0.0.1:8080` | В `.env` тот же loopback |
| `WEB_PUBLIC_BASE_URL` | `http://127.0.0.1:8080` | HTTPS LAN URL, порт 8443 |
| `WEB_TRUSTED_PROXIES` | пусто | В `.env` loopback `/32` |
| `WEB_SESSION_TTL` | 12h | В `.env` override отсутствует |
| `WEB_LOGIN_CODE_TTL` | 2m | В `.env` override отсутствует |

Источник defaults/env: `config.Load`, [internal/config/config.go:57–84](internal/config/config.go#L57). Это чтение конфигурационного файла, не дамп окружения уже запущенного процесса. Секретные параметры не включены в отчёт.

`Config.Validate` допускает HTTP только для localhost/loopback и loopback listener; HTTPS требует trusted proxies. Session TTL 1 мин–30 суток, login TTL 10 сек–10 мин. [internal/webchat/config.go:17–47](internal/webchat/config.go#L17). Forwarded IP учитывается только от доверенного peer, XFF разбирается справа налево: [internal/webchat/http.go:32–72](internal/webchat/http.go#L32).

Текущий Caddy bind — `192.168.11.41:8443`, `tls internal`, reverse proxy на loopback; фильтра подсети в конфиге нет. [scripts/Caddyfile.lan:1–17](scripts/Caddyfile.lan#L1). **REAL:** GET статического JS через HTTPS LAN-адрес с проверкой проектного CA дал 200 за 15 мс; неавторизованная история — 401 за 3 мс. Это подтверждает локальное обращение к сетевому интерфейсу, но **не** доступ с другого компьютера, актуальные firewall rules для всех сетевых профилей или доступ из Интернета — эти части **UNVERIFIED**. Нагрузки на этот endpoint не было.

## 4. Rate Limiting

`allow` — in-memory fixed-window limiter, mutex, окно 1 минута от первого обращения, очистка истёкших buckets, максимум 10 000 buckets. При заполненной карте новый ключ отклоняется. Перезапуск обнуляет счётчики; между процессами лимиты не разделяются. `Service/New/allow`, [internal/webchat/service.go:32–78](internal/webchat/service.go#L32).

| Операция | Ключ | Лимит | Где применяется |
|---|---|---:|---|
| Создание login | `create:` + client IP | 5/мин | `CreateLogin`, service.go:85–88 |
| Telegram-подтверждение login | `telegram:` + Telegram ID | 5/мин | `ConfirmLogin`, service.go:107–119 |
| Попытки одного login-кода | pending login locator | 5, затем блокировка | service.go:132–145 |
| Polling login | `poll:` + client IP | 60/мин | http.go:140–145 |
| Чтение истории | `history:` + hash(session cookie) | 60/мин | http.go:170–179 |
| Message и confirmation | общий `message:` + hash(session cookie) | 20/мин | http.go:186–205 |
| Занятость Web | один slot на Service | 1 одновременно | http.go:191–197 |
| Logout, статические ресурсы | отсутствует | — | http.go:81–88, 246–257 |
| Обычные сообщения Telegram | общего Day30 limiter нет | — | telegram/bot.go:774–789; telegram/hybrid.go:86–148 |

Ссылки: [service.go:85–166](internal/webchat/service.go#L85), [http.go:140–205](internal/webchat/http.go#L140), [bot.go:774–789](internal/telegram/bot.go#L774).

**HTTP 429 — IMPLEMENTED. `Retry-After` — MISSING.** `fail` вызывает `http.Error` без этого header: [internal/webchat/http.go:73–77](internal/webchat/http.go#L73). Отсутствие заголовка подтверждено на 21-м сообщении. Ошибки `CreateLogin` любого типа отображаются как 429, включая возможную ошибку storage; `chat`, [127–130](internal/webchat/http.go#L127).

**Параллельность limiter — IMPLEMENTED в исследованном процессе.** 30 одновременных прямых вызовов одного bucket при пороге 20 дали ровно 20 разрешений и 10 отказов. Это проверяет mutex limiter отдельно от busy gate. Пять перекрывающихся HTTP actions дали 1×200 и 4×429 по занятости; этот результат сам по себе не доказывал бы корректность квоты.

**Покрытие пользователя — PARTIAL.** Новая сессия того же пользователя получает свежую квоту. Confirmation endpoint использует тот же bucket, поэтому прямого обхода через смену `/api/chat/messages` на `/api/confirmations/...` не обнаружено. Telegram не использует message bucket. Следовательно, ограничение одной Web-сессии нельзя считать общим лимитом пользователя или суммарной стоимости LLM.

`session/Authenticate` выполняется до history/action limiter: даже запросы сверх квоты проходят lookup и обновление `last_seen`; невалидные cookies не имеют отдельной квоты неудачной authentication. [internal/webchat/http.go:162–201](internal/webchat/http.go#L162), [service.go:219–230](internal/webchat/service.go#L219). Login IP-квота также объединяет клиентов за NAT. Значения limiter hardcoded, отдельных env-параметров этих квот нет в `config.Load`, [internal/config/config.go:57–84](internal/config/config.go#L57).

## 5. Max Context

### 5.1. Что реально ограничено

| Слой | Ограничение | Единицы / способ | Поведение при превышении |
|---|---|---|---|
| Web action body | 16 384 | байты читаемого тела | Decode error → HTTP 400; проверяется только первый JSON |
| Web `text` | 12 000 | `len` UTF-8 строки после TrimSpace | HTTP 400 до Handle |
| HTML textarea | 12 000 | браузерный maxlength | UI-ограничение; не эквивалентно UTF-8 bytes |
| ShortTerm | 20 сообщений / 1600 | оценка `ceil(UTF8 bytes/3)` | clipping отдельного сообщения и удаление старых |
| AgentContextBuilder | 6000 | та же оценка | убирает SHORT_TERM, затем LONG_TERM; если не хватило — ошибка |
| Router history | 6 пользовательских сообщений × 600 runes | количество записей/символов | обрезка без отдельного маркера |
| Semantic recent dialog | 4 сообщения × 400 runes | количество записей/символов | обрезка |
| Semantic catalog/list | до 100 элементов | количество | срез массива |
| Ollama `num_ctx` | 16 384 | параметр server request | локальной проверки total tokens нет |
| Output generation | router 2048, answer/legacy 2048, semantic 4096 | requested output tokens | output truncation распознаётся по ответу provider |

Источники: `action`, [internal/webchat/http.go:203–215](internal/webchat/http.go#L203); HTML [259](internal/webchat/http.go#L259); `limits/AppendShortTerm`, [internal/memory/service.go:106–166](internal/memory/service.go#L106); `EstimateTokens`, [internal/memory/model.go:109](internal/memory/model.go#L109); `Build`, [internal/memory/context.go:97–139](internal/memory/context.go#L97); `processMessage`, [internal/telegram/hybrid.go:120–132](internal/telegram/hybrid.go#L120); `semanticMessage`, [internal/telegram/semantic.go:109–145](internal/telegram/semantic.go#L109); `Generate`, [internal/llm/provider.go:95–143](internal/llm/provider.go#L95); [internal/llm/router.go:68–75](internal/llm/router.go#L68); [internal/llm/hybrid.go:220–248](internal/llm/hybrid.go#L220).

`SHORT_TERM_MAX_MESSAGES` и `SHORT_TERM_MAX_TOKENS` читаются из env, диапазон 1–10000, defaults 20/1600; действительно присваиваются Memory в main. [internal/config/config.go:98–108](internal/config/config.go#L98), [cmd/workshop-agent/main.go:268–269](cmd/workshop-agent/main.go#L268). Для provider `num_ctx` env override в исследованном HybridConfig отсутствует: [internal/config/hybrid.go:11–48](internal/config/hybrid.go#L11).

### 5.2. Сквозной token budget отсутствует

`AgentContextBuilder` проверяет только построенный им фрагмент, не финальный запрос всех маршрутов. Semantic после Build добавляет этот prompt в `contextData`, сериализует весь JSON, а `HybridClient.Interpret` добавляет `SemanticInstructions` и текущее сообщение ещё раз. Поэтому даже успешный Build не доказывает, что итоговый prompt вместе с output budget помещается в модель. [internal/telegram/semantic.go:134–145](internal/telegram/semantic.go#L134), [internal/llm/hybrid.go:227–229](internal/llm/hybrid.go#L227).

Router и GENERAL не проходят через этот builder; вызов `Local.Generate` принимает собранную строку без preflight tokenizer. [internal/llm/router.go:68–88](internal/llm/router.go#L68), [internal/llm/provider.go:95–114](internal/llm/provider.go#L95). Аналогичного admission control по model context для OpenRouter не найдено: `request`, [internal/llm/openrouter.go:79–105](internal/llm/openrouter.go#L79).

**Реальный token count до отправки — MISSING.** Ollama `prompt_eval_count/eval_count` читаются после генерации как usage, [internal/llm/provider.go:122–138](internal/llm/provider.go#L122). Это не защита от oversized input. `num_predict`/`max_tokens` ограничивают желаемый output, а не доказывают соблюдение общего контекстного окна.

MOCK HTTP-проба: provider отправил 500 000 байт prompt без изменения, с `num_ctx=16384`; сервер вернул синтетический короткий ответ. **FAIL для требования предварительного ограничения входа.** Это не измерение истинного числа токенов конкретного tokenizer. Поведение установленной Ollama при реальном превышении окна — ошибка, clipping или иной вариант — **UNVERIFIED**, oversized generation на ней не запускалась.

### 5.3. Silent truncation и обходы

- ShortTerm clipping подтверждён кодом; текущий input при этом отдельно доступен, но сохранённая история уже короче. `AppendShortTerm`, [internal/memory/service.go:121–135](internal/memory/service.go#L121). Это нужно отличать от обрезки текущего запроса.
- Router/semantic срезы истории и catalog не добавляют явный marker в урезанную строку; semantic `list_total` сохраняет общее число IDs, но не восстанавливает отброшенный каталог. [internal/telegram/hybrid.go:120–132](internal/telegram/hybrid.go#L120), [internal/telegram/semantic.go:109–134](internal/telegram/semantic.go#L109).
- Builder фиксирует `Trace.Warnings` при удалении элементов; защищённые current/domain не обрезает, а возвращает ошибку, если они не помещаются. [internal/memory/context.go:102–139](internal/memory/context.go#L102). Наличие trace warning не означает явное уведомление пользователя во всех UI-маршрутах.
- Web/API text cap работает: 12000 ASCII bytes → 200; 12001 → 400. Кириллица занимает больше байт на символ, поэтому HTML maxlength и серверный лимит могут расходиться. [internal/webchat/http.go:211–215](internal/webchat/http.go#L211), [259](internal/webchat/http.go#L259).
- Body cap неполный: маленький валидный JSON плюс 20 000 trailing bytes → 200. Decoder читает один объект, EOF не проверяется. Это **не обход text cap**, а пробел проверки целого HTTP body. [internal/webchat/http.go:203–215](internal/webchat/http.go#L203).
- Общего прикладного лимита исходного Telegram-текста до роутера не найдено в `handleMessage/processMessage`: [internal/telegram/bot.go:774–789](internal/telegram/bot.go#L774), [internal/telegram/hybrid.go:86–148](internal/telegram/hybrid.go#L86). Нельзя подменять его внешними ограничениями Telegram; их актуальные значения этим аудитом не проверялись.

## 6. Concurrency / Timeouts

### 6.1. Параллельность

**Web admission:** `busy` — buffered channel ёмкости 1; получение nonblocking, при занятости HTTP 429, освобождение defer. Это отказ при перегрузке, не очередь. `New`, [internal/webchat/service.go:42–47](internal/webchat/service.go#L42); `action`, [internal/webchat/http.go:191–197](internal/webchat/http.go#L191).

**Bot serialization:** `applicationMu` охватывает весь `handleMessage`, Telegram callback и Web confirmation. [internal/telegram/bot.go:38](internal/telegram/bot.go#L38), [774–789](internal/telegram/bot.go#L774); [internal/telegram/identity.go:457–483](internal/telegram/identity.go#L457); [internal/telegram/web.go:18–24](internal/telegram/web.go#L18). Telegram update channel unbuffered: `StartContext`, [internal/telegram/bot.go:117–190](internal/telegram/bot.go#L117).

Для foreground-запросов одного Bot одновременно исполняется один обработчик и его последовательные LLM-вызовы. Это не универсальный process-wide provider semaphore: отдельные клиенты, фоновые/CLI-вызовы и прямые вызовы GenerationProvider этим mutex не охватываются. В `OllamaClient.Generate` и `HybridClient.generate` независимого локального/cloud pool нет: [internal/llm/provider.go:95–143](internal/llm/provider.go#L95), [internal/llm/hybrid.go:146–211](internal/llm/hybrid.go#L146).

MOCK-проверка пяти перекрывающихся HTTP actions: **1×200 + 4×429**, выполнявшийся handler удерживался тестовым barrier. Очередь HTTP actions отсутствует. Если Web первым занял busy и ждёт занятого Telegram mutex, остальные Web actions уже будут получать 429. Это вывод из порядка locks, не отдельный latency benchmark.

**Потеря/смешивание:** вывод сохраняется по session ID, что препятствует межсессионному смешиванию; общий mutex защищает разделяемый hybridContext. Но `message.Output` не содержит request ID, а `Deliver` не заполняет request_id assistant-записи. Корреляция по отдельному запросу неполная, durable очередь/replay не реализованы. [internal/message/message.go:14–19](internal/message/message.go#L14), [internal/webchat/service.go:247–283](internal/webchat/service.go#L247), [internal/storage/web_chat.go:9](internal/storage/web_chat.go#L9). Race detector, 10 параллельных реальных генераций и многочасовой soak — **NOT RUN**. По этим проверкам нельзя заявить отсутствие всех гонок или гарантированную доставку.

### 6.2. Таймауты и отмена

| Уровень | Значение | Источник / фактическая граница |
|---|---|---|
| Local provider | 90s default, env override до 5m | `loadHybrid`, config/hybrid.go:17–46; `NewOllamaClient`, provider.go:39–49 |
| Cloud provider в Hybrid | 60s default, env override до 5m | config/hybrid.go:17–46; newHybridClient, cmd/workshop-agent/hybrid.go:24–30 |
| Startup local discovery | 5s | cmd/workshop-agent/hybrid.go:16–18 |
| ReadHeaderTimeout | 5s | main.go:311 |
| ReadTimeout | 20s | main.go:311 |
| WriteTimeout | 5m | main.go:311 |
| IdleTimeout | 60s | main.go:311 |
| MaxHeaderBytes | 16384 bytes | main.go:311 |
| Shutdown grace | 30s, затем Close | main.go:321–329 |
| Общий deadline всего сообщения/ожидания mutex | отсутствует | web.go:10–26; hybrid.go:148, 284 |

Ссылки: [internal/config/hybrid.go:17–46](internal/config/hybrid.go#L17), [internal/llm/provider.go:39–49](internal/llm/provider.go#L39), [cmd/workshop-agent/hybrid.go:16–30](cmd/workshop-agent/hybrid.go#L16), [cmd/workshop-agent/main.go:311–329](cmd/workshop-agent/main.go#L311).

Provider HTTP calls используют `NewRequestWithContext`, но входной Web context проверяется только на входе `HandleInbound`, далее не передаётся; основной pipeline создаёт `context.Background()`. [internal/telegram/web.go:10–26](internal/telegram/web.go#L10), [internal/telegram/hybrid.go:148](internal/telegram/hybrid.go#L148), [284](internal/telegram/hybrid.go#L284). **FAIL:** отмена родителя во время Generate не отменила provider context. HTTP WriteTimeout нельзя считать таймаутом исполнения бизнес-операции. Router и последующая генерация имеют раздельные transport timeouts; суммарная задержка может превосходить один LocalTimeout.

## 7. Stability / Recovery

| Механизм | Статус | Фактическое поведение и доказательство |
|---|---|---|
| Local endpoint restriction | IMPLEMENTED | HTTP loopback origin, redirect disabled, cloud-tag model отвергается; `NewOllamaClient`, [provider.go:39–49](internal/llm/provider.go#L39) |
| Модель отсутствует / Ollama недоступна | IMPLEMENTED в коде и MOCK | Startup discovery логирует unavailable, автоматической загрузки нет; runtime может повторить discovery при пустом model. [cmd/workshop-agent/hybrid.go:16–23](cmd/workshop-agent/hybrid.go#L16), [provider.go:53–100](internal/llm/provider.go#L53) |
| JSON/пустой ответ/output truncation | IMPLEMENTED | Типизированные local ошибки; `done_reason=length`/`done=false` обрабатываются; [provider.go:122–143](internal/llm/provider.go#L122), validators [hybrid.go:213–248](internal/llm/hybrid.go#L213) |
| Local timeout | IMPLEMENTED на HTTP client | MOCK timeout при 40ms с результатом LOCAL_TIMEOUT, следующий вызов успешен; [provider.go:109–121](internal/llm/provider.go#L109), [hybrid.go:34–49](internal/llm/hybrid.go#L34) |
| Fallback policy | IMPLEMENTED, live cloud UNVERIFIED | AUTO допускает ограниченные причины; LOCAL_ONLY cloud запрещает; CLOUD_ONLY выбирает cloud generation. [hybrid.go:16–49](internal/llm/hybrid.go#L16), [146–211](internal/llm/hybrid.go#L146) |
| Cloud consent | IMPLEMENTED | Согласие на hash точного payload, потребляется до вызова. Нет автоматической отправки только по решению модели. [hybrid.go:102–141](internal/llm/hybrid.go#L102) |
| Повтор побочных эффектов при fallback | Защитный механизм IMPLEMENTED в проверенных путях | Resume продолжает сохранённую генерацию, затем прежний dispatcher, а не повторяет исходный Agent/MCP pipeline. [hybrid.go:258–277](internal/llm/hybrid.go#L258), [telegram/hybrid.go:291–327](internal/telegram/hybrid.go#L291) |
| Retry/backoff/circuit breaker provider | MISSING как отдельная политика | Hybrid делает одну local попытку, затем одну разрешённую cloud генерацию; автоматического бесконечного retry нет. [hybrid.go:146–211](internal/llm/hybrid.go#L146) |
| Cloud failure details | PARTIAL | Ошибки cloud сворачиваются в CLOUD_UNAVAILABLE, теряется различие timeout/rate/truncation. [hybrid.go:132–140](internal/llm/hybrid.go#L132) |
| Освобождение ресурсов | IMPLEMENTED для проверенных normal/error путей | resp.Body.Close; освобождение busy и mutex через defer. [provider.go:114–121](internal/llm/provider.go#L114), [openrouter.go:115–126](internal/llm/openrouter.go#L115), [http.go:191–197](internal/webchat/http.go#L191), [bot.go:774–789](internal/telegram/bot.go#L774) |
| Размер provider response | PARTIAL | Ollama tags ограничены 1 MiB, generation 2 MiB; OpenRouter использует неограниченный io.ReadAll. [provider.go:53–143](internal/llm/provider.go#L53), [openrouter.go:119–126](internal/llm/openrouter.go#L119) |
| Перезапуск / durable pending | PARTIAL | Web session в SQLite; hybrid pending в памяти, bounded и TTL; после перезапуска согласие/кнопка не гарантируют продолжение. [storage/web_chat.go:7–9](internal/storage/web_chat.go#L7), [telegram/hybrid.go:51–72](internal/telegram/hybrid.go#L51) |

Ограничение модели/discovery не равно proof фактического inference: **REAL** `/api/tags` вернул 200 за 80 мс и наличие `qwen3:4b-q4_K_M`; новая live-генерация в этом аудите не запускалась. Cloud вызовов в этом аудите не было. Существующие live-тесты явно отключены, результаты прежних запусков не засчитываются как новые.

`CLOUD_ONLY` относится к выбору генерации в HybridClient; `MessageRouter` остаётся отдельным local consumer. Нельзя трактовать этот режим как доказательство полного отсутствия обращения к Ollama во всём процессе: [internal/llm/router.go:68–75](internal/llm/router.go#L68), [internal/llm/hybrid.go:146–162](internal/llm/hybrid.go#L146), [cmd/workshop-agent/hybrid.go:12–23](cmd/workshop-agent/hybrid.go#L12).

## 8. Security

**До Agent:** cookie/session проверяется в HTTP adapter; Web identity повторно сверяется с действительной записью session/user в Bot; затем права на активную мастерскую проверяются до router и domain pipeline. [internal/webchat/http.go:162–190](internal/webchat/http.go#L162), [internal/telegram/web.go:28–42](internal/telegram/web.go#L28), [internal/telegram/hybrid.go:94–104](internal/telegram/hybrid.go#L94). Неавторизованный реальный GET history вернул 401.

**Permissions и confirmations:** Web button ID передаётся существующему `handleConfirmation`, который проверяет владельца conversation/user и TTL; dispatcher проверяет workshop/active context и конкретные permissions. [internal/telegram/identity.go:487–547](internal/telegram/identity.go#L487). После cloud response права и роль перепроверяются: [internal/telegram/hybrid.go:295–305](internal/telegram/hybrid.go#L295). `executeSemantic` повторяет validation и read/write authorization: [internal/telegram/semantic.go:293–323](internal/telegram/semantic.go#L293), [419–430](internal/telegram/semantic.go#L419). Legacy continuation тоже проверяет права: [internal/agent/agent.go:86–104](internal/agent/agent.go#L86).

**InvariantEngine:** движок остаётся в существующих domain paths, а Web не получает альтернативного прямого SQL executor. Проверка invariant permission/confirmation: `Evaluate`, [internal/invariants/engine.go:116–179](internal/invariants/engine.go#L116); пример transactional stock check: [internal/inventory/adjustment.go:59](internal/inventory/adjustment.go#L59). Существующие Web-тесты проверяют общий pipeline, FSM, владельца подтверждения, повторную проверку роли и invariant denial: [internal/telegram/web_test.go:100–226](internal/telegram/web_test.go#L100). Они прошли. **Обход в этих сценариях не обнаружен**, но это не полный аудит каждой доменной команды.

**Отзыв доступа к истории — PARTIAL, P1.** Session authorization допускает доступ при наличии любой читаемой мастерской, тогда как `web_messages` содержит только session_id, без workshop_id. History возвращает все сохранённые сообщения сессии. [internal/auth/telegram_access.go:7–35](internal/auth/telegram_access.go#L7), [internal/storage/web_chat.go:7–10](internal/storage/web_chat.go#L7), [internal/webchat/service.go:219–230](internal/webchat/service.go#L219), [265–283](internal/webchat/service.go#L265). В тестовой БД: создать вторую мастерскую, сохранить синтетический ранее разрешённый вывод, отозвать membership первой — HTTP history остаётся 200 с этим выводом. Это доступ к ранее сохранённым данным, **не подтверждение возможности выполнить новую операцию в отозванной мастерской**. При потере всех memberships existing tests проверяют отказ. Ожидаемая политика хранения личного transcript после отзыва должна быть явно определена; текущая реализация не может фильтровать сообщения по мастерской.

**Rate-limit обход:** новые Web-сессии и Telegram расширяют доступный суммарный бюджет, но не заменяют authentication/permissions. Confirmation использует тот же session limiter. См. раздел 4; security impact — расход ресурсов и задержка других пользователей, а не установленное повышение роли.

**Данные/логирование:** `Run` исключает prompt, pending и approved hash из JSON metadata, [internal/llm/hybrid.go:52–75](internal/llm/hybrid.go#L52). При этом содержимое чата намеренно хранится в `web_messages` и памяти диалога; нельзя считать всю БД «только метаданными». [internal/webchat/http.go:221–231](internal/webchat/http.go#L221), [internal/webchat/service.go:258–266](internal/webchat/service.go#L258), [internal/memory/service.go:130–135](internal/memory/service.go#L130). Отчёт не воспроизводит реальные тексты сообщений, cookies или secrets. UI использует textContent, CSP ограничивает script sources: [internal/webchat/http.go:95–98](internal/webchat/http.go#L95), [261–270](internal/webchat/http.go#L261).

Сетевое ограничение подсети отсутствует в текущем Caddyfile по ранее выбранной конфигурации; это не отменяет application authentication. TLS trust на других клиентских машинах, firewall effective state и публичная экспозиция этим аудитом не изменялись и не сертифицировались.

## 9. Test Results

### 9.1. Методика и воспроизводимость

Сначала изучены существующие тесты `internal/webchat/*_test.go`, `internal/telegram/web_test.go`, `internal/telegram/hybrid_test.go`, `internal/llm/hybrid_test.go` и memory tests. Выполнен полный существующий suite с отключёнными live Hybrid-тестами. Новые проверки размещены **только в `.tmp/day30-audit`**, подключались через Go overlay виртуальных файлов; существующие тесты и исходники не редактировались.

Артефакты:

- [existing-tests.jsonl](.tmp/day30-audit/existing-tests.jsonl) — полный текущий запуск suite, stdout JSON; файл UTF-16 из PowerShell redirect.
- [probes.jsonl](.tmp/day30-audit/probes.jsonl), [probes-extra.jsonl](.tmp/day30-audit/probes-extra.jsonl) — дополнительные диагностические тесты, UTF-16.
- [overlay.json](.tmp/day30-audit/overlay.json), [webchat_probe_test.go](.tmp/day30-audit/webchat_probe_test.go), [llm_probe_test.go](.tmp/day30-audit/llm_probe_test.go), [telegram_probe_test.go](.tmp/day30-audit/telegram_probe_test.go) — воспроизводимые изолированные проверки.
- [live-readonly.json](.tmp/day30-audit/live-readonly.json) — только статусы/latency безопасных GET; ответы чата и токены не записывались.

Команды запуска (Go доступен через локальную установку; cache внутри `.tmp`):

```powershell
$env:PATH = 'C:\TEMP\go\go\bin;' + $env:PATH
$env:GOCACHE = 'C:\TEMP\WorkshopAgent\.tmp\go-cache'
$env:HYBRID_LIVE_LOCAL = '0'
$env:HYBRID_LIVE_CLOUD = '0'
go test ./... -count=1 -json
go test -overlay .tmp/day30-audit/overlay.json ./internal/webchat ./internal/llm ./internal/telegram -run '^TestAudit' -count=1 -json
```

Тесты использовали `t.TempDir()` SQLite и синтетические identities/messages. Существующий suite выполняет доменные сценарии на своих fixtures; реальные команды изменения заказов/товаров не отправлялись. На работающем Web endpoint сделаны только два read-only GET, без login создания и без нагрузки. На Ollama — один GET tags; paid Cloud API не вызывался.

### 9.2. Таблица результатов

Latency диагностического Go-теста ниже включает fixture/setup, если отдельно не указано время операции; это не production percentile и не скорость настоящей LLM.

| ID / сценарий | Условия, REAL/MOCK | Ожидание | Фактически / HTTP | Итог | Latency |
|---|---|---|---|---|---|
| T01 Существующий suite | REAL код/temp DB; LLM/Telegram external calls подставные по fixtures | Все обязательные тесты проходят | 518 pass, 8 skip, 0 fail | PASS, кроме явно skipped | Пакет telegram 90.188s; это не общий wall time команды |
| T02 20 последовательных действий | REAL HTTP Handler/temp DB, MOCK Handle | 20 действий проходят при свободном slot | 20×200 | PASS | 128ms весь измеренный цикл с quota/new-session проверками; тест 0.29s |
| T03 21-е действие и новая сессия | Тот же пользователь, MOCK Handle | Session quota → 429; для user-wide quota новая сессия не должна помогать | 21-е 429 без Retry-After; новая сессия 200 | PASS session limit; FAIL user-wide limit | Входит в T02 |
| T04 5 перекрывающихся действий | REAL Handler, MOCK blocking Handle, одна session | Не больше одного execution slot | 1×200, 4×429 | PASS bounded concurrency; очереди нет | Тест 0.09s |
| T05 30 параллельных calls limiter | REAL `allow`, один ключ, порог 20 | Ровно 20 разрешений | 20 true, 10 false | PASS | Тест 0.07s |
| T06 Web text/input size | REAL validation, MOCK Handle | 12000 pass, 12001 reject | 12000→200; 12001→400; 17000→400 | PASS text bound | Группа size/recovery 0.11s |
| T07 Полный body limit | REAL Decoder, MOCK Handle | Превышение 16KiB тела отклоняется целиком | Маленький JSON + 20000 trailing bytes → 200 | FAIL whole-body validation | Входит в T06 |
| T08 Max context до provider transport | REAL OllamaClient, MOCK HTTP server | Сверхбольшой prompt не уходит без budget admission | 500000 bytes отправлены полностью, num_ctx=16384, ответ server 200 | FAIL preflight; фактические model tokens UNVERIFIED | Тест 0.01s |
| T09 Provider timeout/recovery | REAL HTTP client, MOCK server first response delay120ms, timeout40ms | Timeout; следующий запрос проходит; без скрытого retry | LOCAL_TIMEOUT, затем success; всего 2 HTTP requests | PASS | Timeout40ms; тест0.13s |
| T10 Handler error/recovery | REAL Handler/DB, MOCK error then success | Ошибка различима для API, slot освобождён | Ошибка →200 complete; следующий →200 | FAIL status contract; PASS recovery | Входит в T06 |
| T11 Отмена Web context | REAL Bot pipeline/temp DB, MOCK blocking local provider | После cancel provider ctx canceled | ctx остаётся active без deadline, обработка завершается нормально | FAIL end-to-end cancellation | Тест0.23s |
| T12 Partial membership revocation/history | REAL auth/DB/Handler, синтетические две мастерские | При политике отзыва истории старый вывод A недоступен после отзыва A | При сохранении B history200 содержит ранее сохранённый вывод | FAIL для scoped revocation; политика требует явного решения | Тест0.17s |
| T13 Login limits/nonce race | Существующий REAL service test/temp DB | 6-й create/confirm denied; nonce consumption once | Тест прошёл | PASS | См. JSON suite, отдельный endpoint percentile не измерен |
| T14 Auth/CSRF/logout/HTTPS | Существующие REAL Handler/network httptest | Unauthorized denied, wrong Origin denied, logout invalidates | Проверяемые 401/403/200, Secure/HttpOnly/Strict прошли | PASS | См. JSON suite |
| T15 Poll limit/cleanup | Существующий REAL service test | 61-й poll429, cleanup удаляет expired/revoked | Тест прошёл | PASS | См. JSON suite |
| T16 Shared pipeline/FSM/permissions/invariants | REAL application/temp DB, MOCK LLM/Telegram | Не обходить confirmation, ownership, role и invariant | Существующие Day30 Web tests прошли | PASS для проверенных сценариев | См. JSON suite |
| T17 Fallback/consent/no replay | REAL Hybrid policy, MOCK Local/Cloud | Нет cloud без точного consent, нет повторного вызова по consumed consent | Hybrid unit/application tests прошли | PASS MOCK; live cloud NOT RUN | См. JSON suite |
| T18 Сетевой static HTTPS | REAL текущий Caddy, GET через LAN IP с сервера, trusted project CA | 200 | 200, 2483 bytes JS | PASS локальная сетевой-interface проверка | 15ms |
| T19 Неавторизованная история HTTPS | REAL текущий Caddy/application, без cookie | 401 | 401 | PASS | 3ms |
| T20 Ollama discovery availability | REAL текущая Ollama `/api/tags` | Доступен список модели | 200; qwen3:4b-q4_K_M присутствует | PASS discovery only | 80ms |
| T21 Реальная oversized generation | Настоящая Ollama | Проверить реакцию модели на окно | Не запускалась | NOT RUN | — |
| T22 Новая live Local/Cloud генерация | Настоящие провайдеры | Оценить настоящий inference/fallback | Live tests skipped; cloud отдельно не разрешён | NOT RUN | — |
| T23 Другая LAN-машина/Internet/soak/race/restart | Реальная инфраструктура/нагрузка | Проверить доставку, гонки, восстановление процесса | Не запускались | NOT RUN | — |

Источники T13–T17: `TestRateLimitsAndConcurrentConsumption`, [internal/webchat/service_test.go:147–184](internal/webchat/service_test.go#L147); `TestHTTPAuthCSRFLogoutAndCookieFlags`, [199–253](internal/webchat/service_test.go#L199); `TestPollingLimitAndCleanup`, [254](internal/webchat/service_test.go#L254); network tests [internal/webchat/network_test.go:13–101](internal/webchat/network_test.go#L13); Web application tests [internal/telegram/web_test.go:100–226](internal/telegram/web_test.go#L100); Hybrid unit tests [internal/llm/hybrid_test.go:28–158](internal/llm/hybrid_test.go#L28), [197–216](internal/llm/hybrid_test.go#L197); Hybrid application tests [internal/telegram/hybrid_test.go:41–135](internal/telegram/hybrid_test.go#L41), [179–231](internal/telegram/hybrid_test.go#L179). Дополнительные T02–T12 именованы `TestAudit*` в overlay-файлах выше.

8 skipped test events: HybridLiveCloud, HybridLiveLocalRouting, HybridLiveLocalApplication, NativeONNXOptional, RealGigaAMOptional, NativeCancellationOptional, VoiceRealGigaAMOptional и TaskFSMRestartHelper. Последний — helper, его skip не равнозначен дефекту. Packages без test files не включены в число 8.

### 9.3. Неизменность рабочего дерева

Перед проверками снят SHA-256 baseline **337 файлов**: `internal`, `cmd`, `scripts` (без `__pycache__`), `.env`, `.env.example`, `go.mod`, `go.sum`, `README.md`. После проверок сопоставление дало **0 изменённых/отсутствующих файлов**. Ранее существовавшие незакоммиченные изменения сохранены. Аудит добавляет этот отчёт и временные диагностические артефакты/cache; конфигурация, архитектура, dependencies, существующий код и тесты не исправлялись.

## 10. Day30 Compliance Matrix

| Requirement | Status | Evidence | Problem |
|---|---|---|---|
| Web Chat доступен по сети | PARTIAL | Caddyfile:13–17; T18/T19; main.go:298–311 | Доступ через LAN bind с сервера подтверждён; другой клиент/firewall path UNVERIFIED |
| HTTP API работает | IMPLEMENTED для browser API | http.go:78–94, 162–257; T02/T14 | Независимый machine API отсутствует; error contract требует доработки |
| Authentication | IMPLEMENTED | service.go:85–231; http.go:162–169; T14/T19 | Live Telegram-подтверждение новой сессии в этом аудите не выполнялось |
| Authorization | PARTIAL | telegram/web.go:28–42; hybrid.go:94–104; identity.go:487–547; T16 | Операции защищены; workshop-specific history revocation отсутствует, T12 |
| Rate limit | PARTIAL | service.go:56–78; http.go:140–205; T03/T05 | Нет общей user/channel квоты и Retry-After |
| Max context | PARTIAL | memory/context.go:97–139; llm/provider.go:95–143; T06/T08 | Нет реального token preflight полного prompt; input clipping в истории |
| Concurrency control | PARTIAL | webchat/service.go:42–47; http.go:191–197; telegram/bot.go:774–789; T04 | Один global Bot lock, нет справедливой очереди/per-provider pools |
| Timeouts | PARTIAL | config/hybrid.go:17–46; main.go:311–329; T09/T11 | Transport timeouts есть; нет общего cancel/deadline |
| Error handling | PARTIAL | provider.go:119–143; http.go:232–244; T10 | Ошибка handler →200 complete; cloud причины сворачиваются |
| Recovery | PARTIAL | T09/T10, hybrid.go:125–140 | Следующий запрос восстанавливается; crash/soak/durable request recovery UNVERIFIED |
| Telegram/Web pipeline reuse | IMPLEMENTED | main.go:303–305; telegram/web.go:10–26; T16 | Контекст отмены потерян в adapter |
| Local LLM | PARTIAL по текущей проверке | llm/provider.go:39–143; T09/T20 | Адаптер и discovery подтверждены; live inference в этом запуске UNVERIFIED |
| Cloud fallback | PARTIAL по текущей проверке | llm/hybrid.go:102–211, 258–277; telegram/hybrid.go:250–327; T17 | Policy/consent подтверждены MOCK; настоящий paid cloud NOT RUN |
| Invariants/confirmations reuse | IMPLEMENTED в проверенных сценариях | identity.go:487–547; invariants/engine.go:116–179; T16 | Не является исчерпывающим аудитом всех доменных операций |

Имена сокращённых путей в этой таблице раскрыты ссылками в разделах 2–9; статусы намеренно не повышены до IMPLEMENTED по одному наличию настроек.

## 11. Problems and Priorities

### P0 — критические

**Подтверждённых P0 нет в выполненном объёме.** Не выявлен воспроизводимый неавторизованный доступ к чужой новой операции или обход подтверждения в проверенных сценариях. Это ограниченный диагностический вывод, не security certification.

### P1 — важные

| ID | Проблема / причина | Последствия | Рекомендуемое решение, без реализации |
|---|---|---|---|
| P1-01 | Нет admission control по настоящим токенам полного prompt; hardcoded num_ctx, fragment estimate. provider.go:95–114; semantic.go:134–145; hybrid.go:227–229; T08 | Provider-dependent ошибка/обрезка, неконтролируемая задержка, разный результат маршрутов | Считать окончательный сериализованный prompt подходящим tokenizer, резервировать output и template overhead; отвергать oversized input до transport; определить отдельные Local/Cloud budgets |
| P1-02 | Web ctx теряется, создаётся Background; global applicationMu удерживается на network calls. telegram/web.go:10–26; hybrid.go:148,284; bot.go:774–789; T11 | Ушедший клиент не прекращает работу; один медленный вызов задерживает всех; transport timeout не ограничивает полную операцию | Провести ctx через общий pipeline; deadline для ожидания и исполнения; ограниченное планирование с сохранением session ordering; не удалять mutex без анализа shared state |
| P1-03 | Message quota по session, нет объединения с Telegram. http.go:198–201; hybrid.go:86–148; T03 | Новые сессии обходят суммарную квоту пользователя; возможное вытеснение других пользователей | Общий user/workshop admission budget для обоих каналов плюс отдельные IP/session login protections; проверить fairness, не меняя auth semantics |
| P1-04 | История без workshop provenance, Authenticate допускает любую активную membership. storage/web_chat.go:9; service.go:219–230,265–283; T12 | Старые данные мастерской остаются доступны после частичного отзыва membership | Согласовать политику transcript retention; хранить workshop provenance и фильтровать read, либо определённым образом отзывать/очищать соответствующие сессии; отдельно решить миграцию старых сообщений |
| P1-05 | Handler error скрывается за 200/complete. http.go:232–244; T10 | Внешний клиент не отличает success от failure, ненадёжный retry/monitoring | Ввести явный outcome/error_code и корректный HTTP contract; различать завершение обработки и успешную бизнес-операцию; не допускать автоматического повтора побочных эффектов |

### P2 — улучшения

| ID | Проблема / причина | Последствия | Рекомендуемое решение, без реализации |
|---|---|---|---|
| P2-01 | 429 без Retry-After; create storage error тоже 429. http.go:73–77,127–130 | Клиент не знает время retry, причины перегрузки смешаны | Отдавать retry metadata и отдельные busy/quota/storage коды |
| P2-02 | Decoder не проверяет EOF; browser maxlength отличается от bytes. http.go:203–215,259; T07 | Лишние данные тела принимаются; неожиданные отказы для многобайтного текста | Строгий body-size/EOF/schema contract и согласованный UI input counter |
| P2-03 | History/router/catalog clipping без единой явной политики. memory/service.go:121–135; hybrid.go:120–132; semantic.go:109–134 | Потеря уточняющих деталей, затруднённая диагностика | Документировать retention, сохранять признаки omitted/clipped в trace/UI там, где они влияют на результат |
| P2-04 | Output не коррелируется с request ID; нет durable status запроса. message.go:14–19; service.go:262–266 | Сложнее сопоставлять запрос/ответ и восстанавливать клиент после обрыва | Определить request lifecycle, output correlation и idempotency с учётом existing confirmations |
| P2-05 | Cloud ошибки сворачиваются, response body читается без cap. hybrid.go:132–140; openrouter.go:119–126 | Слабая диагностика и неограниченное чтение ответа внешнего сервиса | Ограничить response size; структурировать очищенные error reasons без вывода secrets/provider payload |
| P2-06 | In-memory limiter с обходом карты для очистки; DB Authenticate перед limiter. service.go:56–78,219–230; http.go:162–201 | Нагрузка на DB/limiter даже при отклоняемых запросах; reset после рестарта | Оценить early coarse limiter, bounded cleanup и нужность persistent/distributed квот по deployment scope |
| P2-07 | Нет нового live inference/fallback, remote-client smoke, race/soak/restart evidence | Нельзя уверенно оценить реальную latency, утечки ресурсов и recovery процесса | Отдельный безопасный acceptance план; paid cloud только с отдельным разрешением; результаты по сервисам не заменять MOCK |

## 12. Recommended Next Steps

1. Рассмотреть P1-01–P1-05 как отдельные решения: общий token budget, жизненный цикл request/cancellation, квоты пользователя, семантика доступа к transcript и контракт ошибок. Этот аудит их не реализует.
2. Сохранить один общий Telegram/Web pipeline, существующие permissions, invariant engine и confirmations. При проектировании concurrency не переносить общие mutable fields в параллельное выполнение без доказанной session isolation.
3. Уточнить целевой статус HTTP API: браузерный frontend protocol или поддерживаемый внешний клиентский контракт; после этого определить outcome schema, Retry-After, correlation и idempotency.
4. Добавить согласованные регрессионные проверки воспроизведённых дефектов в отдельной задаче разработки. В этом аудите probes существуют только как временные диагностические артефакты.
5. Провести отдельно: доступ с другой LAN-машины, новую безопасную local generation, модельный oversized admission test после определения политики, отмену/перезапуск при незавершённом запросе и ограниченный race/soak. Реальный Cloud — только после отдельного разрешения на платный вызов и отправляемый payload.

Исправления не выполнялись. Отчёт предназначен для последующего анализа и выбора архитектурных решений.
