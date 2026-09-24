# Day 17 — первый вызов MCP из приложения

@PROJECT:WORKSHOP_AGENT @L3:DAY17_FIRST_MCP_TOOL @PRESERVE

Статус: реализация и mock-приёмка завершены 2026-09-24. Live WB не проверялся.

## WA-D149 — явный вызов остатков через существующий MCP

Day 17 расширяет Day 16: `/wb_stocks [trace]` → WorkshopAgent.MCPStocks →
Marketplace.MCPRead (доступ и single-flight) → существующий mcpclient → STDIO →
`wb_get_wb_stocks` → `wildberries.Client.WBStocks`. Регистрация и DTO сервера
переиспользуются. Вход — `{}`; неизвестные поля отклоняет SDK.
Клиент разрешает только stocks и seller (проверка принадлежности кабинета).
Автоматического выбора инструмента LLM нет; write/destructive/confirmation=false,
read_only=true. Пять read-only tools Day 16 остаются зарегистрированы.

## WA-D150 — область доступа и конфигурация

Один кабинет по существующей привязке connection/workshop/user. Требуются
активная мастерская, marketplace.read, включённая связь и ранее сохранённый seller ID.
Сервер самостоятельно читает локальный `.env` при запуске по доверенному пути;
MCP-клиент не читает и не передаёт секрет. Старые прямые `/wb` потоки сохраняются:
это не перенос всей прежней конфигурации приложения за процессную границу.
Сервер использует существующую БД только для общих cooldown, не запускает миграции.
Сессия живёт до завершения приложения, сохраняет pacing и identity cache на 24 часа.
Перед stocks серверный seller через MCP сверяется с привязкой; несовпадение закрывает доступ.
Изменение токена требует перезапуска. Отзыв доступа отменяет запрос, запоздалый
результат не выдаётся. Изменений производственных данных и сохранения снимка нет.

## Проверки / состояние

До изменений выполнен реальный tokenless Day16 STDIO smoke: initialize/list_tools,
5 tools, write=0, session closed=true, child exited=true.
Артефакт: `reports/day16-mcp/20260923T184129.865629100Z/report.html`.

`go test ./...` — exit 0; Telegram 52.256s. Сборки `bin/workshop-agent-check.exe`
и прежнего сервера `bin/wb-mcp-server-day16.exe` — exit 0.
После проверки отсутствия процесса `workshop-agent` проверочная сборка скопирована
в прежнее имя `bin/workshop-agent.exe`; приложение не запускалось.
SHA256 обоих app binaries: `4E1711F11AD2B92FC84D12950E837211E1A0046A7FDB78A0BCA95C3E91DC197B`.
`workshop-agent-check.exe day17-mcp-report` — exit 0:
[HTML](../reports/day17-first-mcp-tool/20260924T064145.092958100Z/report.html),
[фактический JSON](../reports/day17-first-mcp-tool/20260924T064145.092958100Z/result.json).
Путь приложения → SDK → STDIO → существующий сервер → mock API → приложение:
3 строки, 12/4/0 шт., 96 ms. Транспорт реальный, downstream WB фиктивный.
Ответ Telegram отдельно проверен HTTP-заглушкой, не настоящим ботом.
Повтор Day16 после сборки сервера: [HTML](../reports/day16-mcp/20260924T064209.169276700Z/report.html),
5 tools, write=0, закрытие сессии/процесса подтверждено.
Реальный `.env`, рабочая БД и внешние WB/Telegram при разработке не используются.

Содержательные tests:
- mcpclient/call_test.go: STDIO call/result, повторное использование сессии,
  отсутствие parent credentials, config/auth/403/429/timeout/API ошибки,
  закрытый allowlist, чужой seller, отмена и cleanup.
- agent/mcp_stocks_test.go: прикладной entrypoint, отсутствие прямого WB bypass,
  разрешённый scope, чужой пользователь/мастерская, отключение, audit и human errors.
- telegram/mcp_stocks_test.go: команда, асинхронный ответ, trace, config/429/API ошибки.
- marketplace/mcp_read_test.go: single-flight совместно с прежней синхронизацией,
  отключение во время вызова и запрет позднего результата.
- config/wb_mcp_file_test.go: синтетический env-файл; no-token и отсутствие
  fallback к credential родителя при недоступном файле.
- wbmcp/server_test.go: прежняя регистрация/description/schema/typed result и
  отсутствие write; дополнен отказ неизвестных stocks-параметров до WB method.

## Использование и границы

Из корня проекта запустить обычный `bin/workshop-agent.exe` после обновления сборки.
Сервер `bin/wb-mcp-server-day16.exe` запускается приложением автоматически.
Нужны прежняя явная привязка `/wb attach`, сохранённый seller ID после `/wb check`
и права marketplace.read в активной мастерской. Токен задаётся владельцем локально
в корневом `.env`; после замены перезапустить приложение. Файл хранит секрет
открытым текстом: ограничить права доступа. Через Telegram секрет не передавать.

`/wb_stocks` — свежая выборка через MCP; `/wb_stocks trace` — та же операция с
безопасным trace. Старые `/wb stocks` и кнопка «Остатки WB» сохраняют прежний
sync/cache flow. Новая команда не заменяет локальный снимок, показывает до 12 строк
с указанием общего числа и времени; отсутствие строк не означает нулей всех товаров.

Вход инструмента: `{}`; schema `{type:object,additionalProperties:false}`.
Выход: source/fetched_at/complete/untrusted_data/data, строки используют существующий
`wildberries.Stock` (nmId/chrtId/warehouseId/warehouseName/quantity).
Регистрация и handler — `internal/integrations/wbmcp/server.go`, `mcp.AddTool`.
Прямого HTTP в MCP handler, client и Telegram flow нет.

Ограничения: один кабинет на приложение; нет автоматического LLM tool selection,
write tools, смены токена на лету, новой миграции и импорта результата в производство.
MCP server читает `.env` сам; старый прямой `/wb` adapter остаётся в основном процессе
с прежней конфигурацией. Это изоляция секрета для нового MCP flow, а не удаление
всех прежних credential-bearing компонентов из приложения.
Начальная проверка seller может вернуть действующий WB cooldown: она не обходится.
Сервер читает/обновляет общую marketplace_cooldowns через существующую БД.
Публичные контракты WB D1 и live-проверка V1 остаются открытыми, см. marketplace/L3.

## Продолжение после сжатия

Читать marketplace L0–L3/WORKSTATE и этот документ. Изменения предыдущих задач
в рабочем дереве сохранять. Новые файлы: mcpclient/call.go, marketplace/mcp_read.go,
agent/mcp_stocks.go, telegram/mcp_stocks.go. Новых миграций нет.
Не запускать main bot для демонстрации; mock-report ветка выполняется ДО config.Load.
Проверочные exe: bin/workshop-agent-check.exe; сервер сохраняет имя
bin/wb-mcp-server-day16.exe. Не заменять работающий binary.
