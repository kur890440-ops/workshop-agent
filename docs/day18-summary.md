# Day18: сохранённая сводка через MCP

@PROJECT:WORKSHOP_AGENT @L3:DAY18_BACKGROUND_JOBS @PRESERVE

## WA-D157 — read-only stored summary @NO_COMPRESS

В существующем MCPManager зарегистрирован `wb_get_daily_summary`. Это read-only
инструмент без записи, destructive actions и подтверждений. Input — типизированный
пустой объект `{}`. User/workshop передаются существующим механизмом одноразового
grant в metadata; grant дополнительно привязан к имени инструмента. Разрешение
чтения не может использоваться для scheduling mutation.

Источник истины — `background_job_runs.aggregate_json`. Существующий
BackgroundJobService читает запись в одной транзакции после проверки
marketplace.read, membership и активной мастерской. Выбирается последний
success/partial_success по finished_at, id, только для WB_DAILY_SYNC этой мастерской.
Более новый failed/running не скрывает предыдущую пригодную сводку; timestamp
показывает её фактический возраст. Проверка WB identity не нужна для локального чтения.

Ни нового scheduler/service/клиента WB/БД, ни миграции не добавлено.
Сессия, процесс и EXE прежние. Сейчас ListTools возвращает 8 инструментов:
6 WB API read, 1 stored-summary read, 1 local scheduling mutation.

## L1: реализация и данные

- `internal/background/summary.go`: DailySummary DTO и чтение существующего storage.
- `internal/integrations/mcpmanager/summary.go`: регистрация, scope, вызов сервиса.
- `internal/integrations/mcpclient/summary.go`: настоящий SDK CallTool, fixed read-only name.
- `internal/telegram/background_summary.go`: представление результата MCP без SQL.
- `internal/experiment/day18.go`: дополнение прежнего отчёта.

Output: code, job_id, run_id, status, captured_at (время завершения сохранённого run),
next_run_at, job_status, timezone, aggregate, error_categories, summary.
Aggregate содержит существующие products_count, price_changes_count,
stock_changes_count, zero_stock_count, low_stock_count, errors_count, prices_ok,
stocks_ok. Он декодируется из JSON, не пересчитывается по snapshots/сообщениям/LLM.

## L2: безопасность и состояния

Без usable run: `code=NO_SUMMARY_AVAILABLE`, `aggregate=null`,
«Утренняя синхронизация еще не выполнялась.» Нули вместо отсутствующего aggregate
не создаются. Отсутствующие обязательные поля/некорректный JSON дают безопасную ошибку.

Partial: сохранённые status=partial_success, errors_count>0, признаки доступных
источников и категории ошибок; текст явно сообщает о неполноте. Произвольные
сохранённые сообщения ошибок заменяются безопасной категорией source_failed.
Неизвестные поля JSON не попадают в output. Токен/headers/config не читаются.

Область доступа проверяется на сервере, даже при обходе Telegram UI. После смены
активной мастерской старый scope отвергается. VIEWER с marketplace.read может читать,
отключённый membership не может. Read grant нельзя применить к CreateJob.

## Telegram и демонстрация

Wildberries → **Последняя сводка**; команды:

```text
/wb_auto summary
/wb_auto summary trace
```

Путь: Telegram → MCPManager → MCP Client.CallTool(wb_get_daily_summary)
→ in-memory MCP → тот же server → BackgroundJobService → SQLite → structured result
→ Telegram. Handler не читает aggregate SQL. Trace показывает имя инструмента,
transport, BackgroundJobRun.aggregate_json, run ID и WB API CALLS: 0.

Демонстрация: `/wb_auto enable` → `/wb_auto run` → дождаться summary →
«Последняя сводка». Для offline-проверки без .env/рабочей БД/реальных API:
`workshop-agent.exe day18-background-report`.

Report содержит RAW/SNAPSHOT DATA → AGGREGATE → SUMMARY, ссылки на фактический
summary-mcp.json, run/snapshot counters и измеренное изменение счётчика WB API = 0.
Закрыты четыре требования: сохранять данные (SQLite), работать по расписанию
(DAILY_AT_TIME 08:00), возвращать aggregate (MCP tool), регулярный summary (Sender).

## L3: проверки

- `TestStoredSummaryThroughMCP`: ListTools/read-only/typed output, no job/no run,
  success, newer failed, partial, реальные сохранённые значения, ноль WB calls,
  foreign/stale workshop, membership, VIEWER, read grant не даёт права mutation,
  отсутствие синтетического секрета в результате.
- `TestTelegramLastSummaryUsesMCP`: команда и кнопка через настоящую MCP session;
  после session.Close данные в БД остаются, но Telegram уже не получает сводку.
- Остальные scheduler/Day16/17/18 тесты сохраняются. Финальные результаты и
  путь нового offline report фиксируются в WORKSTATE.

Live WB/Telegram не запускались. Тексты отчёта получены на фиктивных данных
с управляемыми датами, а не из реального кабинета.

## Фактическая приёмка

`go test ./...` — exit 0; scheduler regressions сохранены. Build v0.18.3 — exit 0,
в bin/ только workshop-agent.exe. Готовый бинарник подтвердил 8 tools/in-memory,
обе MCP sessions закрыты. `git diff --check` — exit 0.

Отчёт: `reports/day18-background-jobs/20260924T145621.939783000Z/report.html`.
Результат: соседний `summary-mcp.json`. Passed=true, run_id=2,
products=3, price_changes=1, stock_changes=1, zero=1, low=2, errors=0;
SummaryWBCalls=0. Фиктивный captured_at=2026-09-27T06:00:00Z,
next_run_at=2026-09-28T05:00:00Z — управляемое время сценария.
