> Исторический документ предыдущего этапа. С 2026-09-24 STDIO, отдельные
> server/smoke EXE и прежняя схема jobs заменены [рефакторингом WA-D154–156](day18-refactor.md).
> Старые команды сборки ниже не являются текущей инструкцией запуска.

# Day 16 — MCP поверх существующего WB Client

@PROJECT:WORKSHOP_AGENT @L3:DAY16_MCP @PRESERVE

## Диагностическая команда Day16/17 — проверено 2026-09-24

Из корня проекта: `go run ./cmd/mcp-wb-smoke`.
Нужен ранее собранный `bin/wb-mcp-server-day16.exe`; при отсутствии:
`go build -o bin/wb-mcp-server-day16.exe ./cmd/wb-mcp-server`.
Другой путь задаётся через `-server`. Используются прежний клиент, сервер и STDIO;
names/descriptions/count приходят из SDK ListTools, не из констант CLI.
Smoke передаёт `-no-token` и минимальное окружение, не читает `.env` и не вызывает WB.

Результат: Connection: OK; Tools discovered: 5; Write tools exposed: 0;
Session closed: true; Child process exited: true; exit 0.
Артефакт: [report.html](../reports/day16-mcp/20260924T123620.894123100Z/report.html).
Отсутствующий `-server` проверен: понятная ошибка с командой сборки, exit 1.
`go test ./...` — exit 0. Изменения ограничены текстом вывода/ошибки smoke;
архитектура MCP не менялась. Ранее собранный smoke EXE этим запуском не обновлялся.

Статус 2026-09-23: Day 16 завершён. Полный go test ./... и реальный STDIO smoke
прошли. WB API не вызывался; LLM tool calling не подключён.

## L0 — границы

Только локальный STDIO, initialize + tools/list, пять read-only tools.
Нет LLM tool selection, write-tools, HTTP/SSE/WebSocket/TCP listener, Python,
Docker, стороннего wb-mcp-server. Настоящий .env/рабочая БД не читаются.
Реальные WB-запросы не нужны и не выполняются.

## L1 — зависимости

Workshop Agent MCP client (отдельный smoke entrypoint) → STDIO → наш MCP server
→ существующий wildberries.Client → WB API (последний переход в demo не вызывается).

Server: cmd/wb-mcp-server и internal/integrations/wbmcp.
Client: cmd/mcp-wb-smoke и internal/integrations/mcpclient.
WB: internal/marketplace/wildberries/client.go, *Client, New(token).
HTTP, DTO parsing, token, TLS, redirects, retry/rate limits остаются в WB Client.
Существующий Telegram Marketplace путь не переключается на MCP в Day 16.

## L2 — решения

### WA-D141 — локальное разрешение MCP для Day 16

Уточняет прежний запрет запуска MCP из Marketplace L0: новый запрос пользователя
разрешает собственный STDIO server и реальный протокольный smoke без WB token.
Все запреты реальных WB/Telegram вызовов и изменений рабочей БД сохраняются.

### WA-D142 — SDK, allowlist и граница полномочий

Официальный github.com/modelcontextprotocol/go-sdk v1.7.0; требует Go >=1.25.0.
Версия закрепляется в go.mod/go.sum. Проверена официальная документация:
[SDK](https://github.com/modelcontextprotocol/go-sdk/tree/v1.7.0),
[API](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.7.0/mcp),
[go.mod](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/go.mod).

Регистрация явная. Аннотации READ_ONLY — описание, не проверка авторизации.
Этот server доверяет локальной OS-границе запуска; он ещё не предоставляет
Workshop user/workshop authorization. Не подключать его к Telegram/LLM, пока
не реализован будущий ProposedCall → Authorization → Invariants → Confirmation
pipeline. Наличие STDIO не заменяет такую проверку.

Наш discovery client использует lifecycle `initialize` с protocol `2025-11-25`.
SDK v1.7.0 сначала пробует более новый `server/discover`; sending middleware
пропускает этот probe локально через предусмотренный SDK fallback. Настоящие
`initialize` и `tools/list` выполняются SDK через STDIO. Сервер не ограничивает
поддерживаемые SDK версии искусственным transport wrapper. JSON-RPC вручную
не реализован; список инструментов берётся исключительно из ответа сервера.

### WA-D143 — секреты и lifecycle

Токен читает только server config layer из окружения. MCP client не загружает
.env, не получает токен из server и не передаёт его в requests. Smoke запускает
server с -no-token и минимальным окружением без WB/Telegram/LLM secrets.
stdout server зарезервирован под протокол; diagnostics — фиксированный stderr.
Handlers ограничены context/deadline. Ошибки из закрытого справочника, без dumps.
Client закрывает session и дожидается завершения child process, включая ошибки.

## Аудит и таблица методов

| WB операция | Реальный Go method (*wildberries.Client) | Класс | MCP |
|---|---|---|---|
| Кабинет | Seller(ctx) | READ | wb_get_seller |
| Карточки | Catalog(ctx) | READ | wb_get_products |
| Остатки WB | WBStocks(ctx) | READ | wb_get_wb_stocks |
| Новые FBS | NewOrders(ctx) | READ | wb_get_new_orders |
| Статусы FBS | OrderStatuses(ctx, ids) | READ | wb_get_order_statuses |
| Остатки продавца | SellerStocks(ctx, cards) | READ | Не экспортируется: требует каталога вариантов, отдельный будущий use case |

AVAILABLE_IN_GO_BUT_NOT_EXPOSED_TO_MCP: SellerStocks; служебные Configured,
ContainsSecret, String/GoString и WithGuard. WB WRITE methods отсутствуют, поэтому
проверка «известный существующий WB write method скрыт» неприменима; отрицательный
тест всё равно проверяет отклонение имени write-tool и точное совпадение allowlist.
Marketplace.Attach/Disable/SetMapping — локальные изменения, не WB API методы;
также не экспортируются. Sales API метода нет, fake sales tool не создаётся.

Существующие проверки: wildberries/client_test.go — HTTP safety/ошибки/пагинация;
marketplace/service_test.go — права/изоляция/снимки; telegram/marketplace_test.go —
UI и secrets. WB клиент не логирует запросы/ответы, использует закрытый Error code.

## L3 — приёмка и продолжение

- [x] Аудит существующего клиента, DTO, startup, тестов и ограничений.
- [x] Зависимость SDK, server, typed tools и безопасные ошибки.
- [x] STDIO client, initialize/tools/list без токена, shutdown.
- [x] Unit/protocol/subprocess tests и go test ./....
- [x] Реальный smoke + HTML report с фактическими tools.

Текущий этап закрыт. Рабочее дерево содержит незакоммиченные изменения предыдущей
WB реализации — они сохранены; новых миграций и изменений WB HTTP-клиента нет.
После сжатия перечитать этот файл и docs/marketplace/WORKSTATE.md, проверить diff.
После Day 16 остановиться; не переходить к LLM tool calling.

## Запуск в Windows PowerShell

Из `C:\TEMP\WorkshopAgent`, с Go >=1.25.0 (или установленным Go с разрешённым
автоматическим получением toolchain):

```powershell
go build -o .\bin\wb-mcp-server-day16.exe ./cmd/wb-mcp-server
go build -o .\bin\mcp-wb-smoke-day16.exe ./cmd/mcp-wb-smoke
.\bin\mcp-wb-smoke-day16.exe
```

Бинарники уже собраны в `bin/`. Для повторного demo достаточно последней команды.
Путь к server можно задать через `-server`; default — `bin/wb-mcp-server-day16.exe`.
Не запускайте server в терминале ради печатного списка: он ждёт MCP на stdin,
а список показывает smoke client. Нет TCP listener и отдельного фонового сервиса.

Server entrypoint: `cmd/wb-mcp-server/main.go`; реализация:
`internal/integrations/wbmcp/server.go`. Smoke entrypoint:
`cmd/mcp-wb-smoke/main.go`; discovery/lifecycle: `internal/integrations/mcpclient/`.

## Токен и схемы

Day 16 не требует настройки токена. Smoke всегда запускает server с `-no-token`
и окружением только для работы ОС. Этот flag имеет приоритет даже при наличии
WB_API_TOKEN. Конфигурация server находится в `internal/config/wb_mcp.go`.

При отдельном будущем запуске для реального чтения server получает
`WB_API_TOKEN` из своего окружения; `.env` автоматически не загружается.
У существующего бота сохраняется прежний локальный `.env`-путь настройки.
Секрет не является аргументом MCP и не возвращается client. `.env` — открытый
текст, доступ к нему должен быть ограничен владельцем ОС. Настоящий `.env`
в этой задаче не читался и не менялся.

Четыре tools принимают `{}`. `wb_get_order_statuses` принимает
`{"order_ids":[123,456]}`: 1–1000 уникальных положительных целых ID.
Неизвестные поля отвергаются; схемы и metadata передаются через `tools/list`.
Ответы используют существующие WB DTO внутри envelope с `source`, `fetched_at`,
`complete`, `untrusted_data`. Частичная ошибка не выдаётся за успешные данные.

Timeout tool handler — 90 секунд, discovery — 30 секунд; после закрытия STDIO
SDK ожидает child process и завершает зависший процесс. Размер сериализованного
envelope ограничен 1 MiB; больший результат возвращает `wb_output_limit`.
Ошибки конфигурации, аутентификации, прав, rate limit, timeout, лимита и WB API
разделены безопасными фиксированными кодами. Сырые HTTP dumps не возвращаются.

## Доказательства приёмки

- `go test ./...` — exit 0, включая существующие Agent/Storage/Telegram tests.
- Оба `go build` из инструкции — exit 0; рабочий exe бота не перезаписывался.
- Реальный запуск `bin/mcp-wb-smoke-day16.exe` — exit 0.
- [HTML report](../reports/day16-mcp/20260923T111346.093190300Z/report.html).
- [Фактический discovery со схемами](../reports/day16-mcp/20260923T111346.093190300Z/discovery.json).

Выдержка из фактического вывода (описания находятся в HTML/JSON):

```text
MCP connection: OK
Server: workshop-agent-wb
Transport: stdio
Protocol: 2025-11-25
Tools discovered: 5
1. wb_get_new_orders
2. wb_get_order_statuses
3. wb_get_products
4. wb_get_seller
5. wb_get_wb_stocks
Write tools exposed: 0
MCP tool discovery: OK
Session closed: true
Child process exited: true
```

| Требование | Проверка |
|---|---|
| Создание server, уникальные имена, точный read-only allowlist, валидные schemas | TestRegistrySchemasAndNoTokenProtocol |
| Dispatch в существующий API interface, типизированные ответы | TestTypedDispatchReusesWBMethods; compile assertion для *wildberries.Client |
| Нет write tools, неизвестных полей, credentials в arguments | TestRejectWriteUnknownAndInvalidArguments |
| Понятные errors без секретов, timeout | TestSafeErrorMappingAndTimeout |
| Ограничение ответа, частичный сбой | TestOutputLimitAndNoPartialSuccess |
| initialize/tools/list через реальный процесс, без наследования секретов, shutdown | TestSTDIOInitializeListAndProcessCleanup |
| Завершение процесса при ошибке handshake/таймауте | TestFailedInitializeAndTimeoutReapChild |
| Server-side config, принудительный no-token | internal/config/wb_mcp_test.go |
| Недоверенные описания не исполняются в HTML | TestReportEscapesDiscoveredContent |

Проверка кода MCP layer: отсутствуют WB URLs, net/http client, построение
Authorization headers и parsing WB HTTP responses; направление зависимости
только MCP → существующий WB API client. Новая DB migration не требуется.

## Оставшиеся границы

Подтверждена работоспособность MCP-протокола, не live-доступ к Wildberries.
Существующие [D1/V1](marketplace/WORKSTATE.md) не закрыты: полная актуальная
сверка WB-контрактов и реальные API/Telegram проверки остаются отдельной работой.
Большой каталог может упереться в timeout/лимит MCP ответа; streaming и новые
WB endpoints в Day 16 не добавлялись. Операции используют доступ настроенного
кабинета в доверенном локальном процессе. Подключение к LLM/Telegram требует
отдельной реализации pipeline из WA-D142. Write-tools не добавлены.
