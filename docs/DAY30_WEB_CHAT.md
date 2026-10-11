# @PROJECT:WORKSHOP_AGENT @L3:DAY30_WEB_CHAT_TELEGRAM_AUTH

@PRESERVE

Day30: Web Chat внутри существующего `workshop-agent.exe`. Один процесс,
один Bot/application handler, один Agent, существующие in-process MCP и scheduler.

## Запуск

В `.env` включить `WEB_ENABLED=true`. Параметры приведены в `.env.example`.
Локальный адрес: **http://127.0.0.1:8080/chat**. Либо включить Web для одного запуска:

```powershell
Set-Location C:\TEMP\WorkshopAgent
$env:WEB_ENABLED='true'
.\bin\workshop-agent.exe
```

Миграция 120 выполняется обычным `storage.Migrate`; перед обновлением существующей
БД создаётся backup. `.env` с действующими секретами не изменялся. Web по умолчанию
выключен; production bot во время автоматической проверки не запускался.

Для другого устройства нужны HTTPS reverse proxy и собственное имя сервера:

```dotenv
WEB_ENABLED=true
WEB_LISTEN_ADDRESS=127.0.0.1:8080
WEB_PUBLIC_BASE_URL=https://chat.example.org
WEB_TRUSTED_PROXIES=127.0.0.1/32
WEB_SESSION_TTL=12h
WEB_LOGIN_CODE_TTL=2m
```

`chat.example.org` — пример, не развёрнутый адрес. Прокси завершает TLS,
перезаписывает `X-Forwarded-Proto: https`, добавляет адрес клиента в
`X-Forwarded-For`, сохраняет Host и направляет запросы к listener.
Доверять только фактическим IP/CIDR прокси. Для прокси на другом хосте задать
соответствующий bind и ограничить доступ сетью прокси. HTTP без TLS разрешён
только на loopback. Production-запрос без доверенного HTTPS-прокси отклоняется.
Адреса Ollama не меняются, новых MCP/Ollama listener нет.

## Интеграция

| Компонент | Использование |
| --- | --- |
| `cmd/workshop-agent/main.go` | Composition root, один HTTP server, graceful shutdown |
| `Bot.handleMessage → processMessage` | Общая обработка Telegram text, voice transcript и Web text |
| `Bot.HandleInbound` | Web adapter к существующему typed message с metadata |
| `message.Inbound` | WEB, internal user ID, Telegram ID, Web session ID, conversation ID, request ID |
| `Bot.handleConfirmation → executeButton` | Общие кнопки, scope, TTL, версия формы, права и активная мастерская |
| `auth.ExistingTelegramUser → auth.Require` | Existing identity/membership; без регистрации Web user |
| Existing Agent/semantic/TaskStateMachine/InvariantEngine | Существующие preconditions, confirmations и бизнес-операции |
| `webchat.Service.Deliver` | Текст/кнопки в Web history; повторная проверка доступа |
| `background.Scheduler.Maintenance` | Очистка на существующем 15-секундном clock |

При обследовании HTTP server и browser session helpers отсутствовали.
Application orchestration находится в `internal/telegram`; Web использует
существующий Bot, не копию бизнес-логики. Text/callback сериализованы общим mutex:
исходный Telegram event loop был последовательным. Web принимает один запрос
к Agent одновременно; занятость возвращает HTTP 429. Async MCP ответы идут в
тот же Web output sink. Прямых импортов/вызовов MCP, LLM или domain services
в Web нет.

Web conversation ID = `-session_id` в существующих chat_id/telegram_chat_id
contracts. Domain user, активная мастерская, права и Task state общие с Telegram.
Telegram group updates не направляются в Web. History возвращает последние
200 сообщений текущей Web session.

## Вход и безопасность

1. GET /chat создаёт pending_web_logins с code hash, browser nonce hash,
   created/expires, attempts и status. Код: crypto/rand, 5 bytes → 8 символов
   Base32. Первые четыре — случайный locator записи, остальные четыре —
   проверяемая часть. Хранится SHA-256 полного кода, не полный код.
2. Browser получает отдельный случайный 256-bit nonce в HttpOnly cookie.
3. /login CODE в личном Telegram проверяет существующую identity/membership,
   TTL, attempts/status. Команда перехватывается до истории, форм и LLM.
4. Успех атомарно помечает код confirmed (повторно уже не принимается) и создаёт
   server-side session для существующего пользователя.
5. Polling каждые 2 секунды предъявляет nonce. Только исходный browser атомарно
   меняет его на новый случайный 256-bit opaque session token. Status становится
   consumed; browser переходит на /chat.

Vanilla-JS polling выбран потому, что существующего HTMX/SSE стека нет.
SPA, WebSocket, внешние JS/CDN зависимости не добавлены.

| Ограничение | Значение |
| --- | --- |
| Код | 2m; WEB_LOGIN_CODE_TTL, допустимо 10s–10m |
| Ошибки проверяемой части | 5 → blocked; неизвестный locator учитывается по Telegram user |
| Создание login | 5/мин/IP |
| /login | 5/мин/Telegram user, включая неверные/неизвестные коды |
| Login polling | 60/мин/IP |
| History polling | 60/мин/session |
| Messages/actions | 20/мин/session |
| Сессия | 12h; WEB_SESSION_TTL, допустимо 1m–30d |
| Cookie | HttpOnly, SameSite=Strict, Path=/, MaxAge; Secure при HTTPS |
| CSRF | Exact configured Origin + X-Workshop-Request: 1; missing Origin отклоняется |

Лимиты — ограниченная in-memory таблица с очисткой минутных окон. После рестарта
лимиты сбрасываются, а attempts/status/expiry остаются в SQLite. CSRF проверяется
для messages, confirmations и logout. CORS allowance отсутствует. HTML строится
html/template, сообщения — DOM text nodes. Поддержаны переносы, **bold** и inline
code без исполнения HTML. CSP запрещает inline scripts, чужие ресурсы и frames.

Таблицы `pending_web_logins`, `web_sessions`, `web_messages` находятся в общей
SQLite. Cookie содержит только opaque token; в БД — SHA-256 hash, identity,
created/expires/last_seen/revoked. Доступ проверяется на каждом запросе,
перед обработкой и при доставке ответа. POST /logout отзывает сессию и удаляет
cookie. Scheduler удаляет expired/revoked sessions вместе с Web history.
Кнопки живут в существующем Bot registry 15 минут; после рестарта требуется
открыть меню заново, старые кнопки возвращают понятную ошибку.

Audit: login created/confirmed, identity references, session created/revoked,
фиксированная причина ошибки, request ID и факт confirmation. Login code,
nonce, session token, Telegram token и API secrets не логируются.

## API

- GET /chat, /chat/app.js, /chat/style.css — UI и встроенные ресурсы.
- GET /api/login/status — browser-bound завершение входа.
- GET /api/chat/messages/status — история своей сессии.
- POST /api/chat/messages — JSON {"text":"..."}, общий обработчик.
- POST /api/confirmations/{id} — JSON {}, существующее действие кнопки.
- POST /logout — server-side revoke.

Все chat/action endpoints требуют действующую сессию. Message POST возвращает
request ID; output доступен через history, включая поздние MCP ответы.

## Демо Day30

1. Запустить один bin/workshop-agent.exe с Web config. Для другого устройства
   настроить HTTPS-прокси и открыть <WEB_PUBLIC_BASE_URL>/chat.
2. Получить код и отправить /login CODE существующему боту от пользователя,
   у которого уже есть доступ к мастерской.
3. Проверить автоматический переход в чат. Второй браузер этим входом не
   авторизуется.
4. Спросить «Какие материалы есть?» и получить существующий Agent ответ.
5. Для тестовой задачи задать количество, затем «Запускай». До нажатия
   «Подтвердить текущий шаг» переход не выполняется. Нажать кнопку и проверить
   состояние через /task. На confirmation screen также доступна отмена.
6. Если WB/Ozon настроены, выполнить существующий read-запрос, например
   /wb_stocks, и увидеть результат того же Agent/MCP pipeline.
7. Выйти. Прежний cookie больше не открывает API.

## Проверки и границы

internal/webchat/*_test.go: TTL/reuse, attempts/lockout, browser binding,
unauthorized identity, concurrent completion, revoked/expired session,
access loss, CSRF, cookie flags, proxy, rate limits, cleanup, реальный loopback
HTTP login→history→logout на временной БД.

internal/telegram/web_test.go: одинаковый текст Telegram/Web через один semantic
handler, identity/request ID, Task FSM confirmation, чужая сессия/replay,
preconditions, понижение роли, InvariantEngine.

Старый architecture test запрещал любой HTTP listener в composition root.
Day30 разрешает Web listener только в main.go, сохраняя запреты subprocess,
HTTP bypass в MCP/background и второго scheduler.

Live Telegram, реальный браузер другого устройства и production HTTPS требуют
ручного демо с реальным кодом. Автоматические тесты не заменяют эту проверку.
Day31 не реализуется.
## Результат проверки — 2026-10-10

- Полный `go test ./...`: PASS, включая существующие Telegram/Task/Invariant/MCP tests.
- После окончательной правки shutdown и усиления metadata assertions повторены
  Day30, loopback HTTP и architecture tests: PASS.
- `go build -o bin/workshop-agent.exe ./cmd/workshop-agent`: PASS.
- `bin/workshop-agent.exe --version`: `WorkshopAgent v0.21.4`.
- Артефакт: `bin/workshop-agent.exe`, 26 450 944 bytes.
- Полный лог: `reports/day30-tests.txt` (только тестовые package results).
- Реальный Telegram bot, production database, live LLM/MCP и внешний HTTPS
  в рамках проверки не запускались. Browser UI проверен HTTP/шаблонными тестами,
  интерактивный браузер вручную не проверялся.

Изменённые и добавленные файлы:

- `cmd/workshop-agent/main.go`
- `internal/config/config.go`, `.env.example`
- `internal/auth/telegram_access.go`
- `internal/message/message.go`
- `internal/webchat/config.go`, `service.go`, `http.go`, `service_test.go`, `network_test.go`
- `internal/storage/sqlite.go`, `internal/storage/web_chat.go`
- `internal/telegram/bot.go`, `identity.go`, `clear.go`, `speech.go`, `web.go`, `web_test.go`
- `internal/background/scheduler.go`
- `internal/integrations/mcpmanager/architecture_test.go`
- `README.md`, `docs/DAY30_WEB_CHAT.md`, `reports/day30-tests.txt`

В бинарник включены UI/CSS/JS; внешние runtime-файлы интерфейса не нужны.