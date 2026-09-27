# День 19 · Композиция MCP-инструментов

@PROJECT:WORKSHOP_AGENT @L3:DAY19_MCP_COMPOSITION @NO_COMPRESS @PRESERVE

## WA-D161 — trusted strict market pipeline

Дата: 2026-09-25. Уточняет WA-D151–153 (частичная запись Day18) и WA-D157
(выбор partial summary): новый pipeline строго последовательный, сохраняет оба
источника атомарно; retrieval выбирает только последний successful aggregate.
Прежние документы и historical partial rows сохранены, но новые runs не создают
partial_success. WA-D154–160 остаются в силе: один executable/process, существующий
in-memory MCP, общий WB client/limiter/identity и safe HTTP trace.

### L0 — границы

WB_MARKET_SYNC_PIPELINE выполняет prices → stocks → summary → save.
WB_WRITE=false на всех шагах. Нет новых endpoints, scheduler, process, server/client,
LLM-controlled chaining, snapshot tables, production DB или миграций.
Разработка и демонстрация: mock WB, временная SQLite, без .env и реального Telegram.

### L1 — модули и данные

- `internal/background/pipeline.go`: MCPPipelineRunner, фиксированные четыре шага,
  typed data flow, validation, cancellation, stop-on-error, PipelineResult.
- `internal/background/wb_executor.go`: тонкий adapter Execute → pipelineExecution;
  прикладной wrapper передаёт trusted run context, отображает status/summary/retry
  в существующий ExecutionResult. Scheduler по-прежнему завершает JobRun и уведомляет.
- `internal/integrations/mcpmanager/pipeline.go`: регистрация двух local tools на
  том же сервере; одноразовые tool-bound grants. Handler не вызывает следующий tool.
- `internal/integrations/mcpclient/pipeline.go`: закрытый dispatcher LOCAL tools
  через ту же SDK session.CallTool. Нет произвольных имён tools от пользователя.
- `internal/background/snapshots.go`: единственный prepareMarket для normalization,
  product union, stock totals, previous-state diff и aggregate; BuildMarketSummary
  читает consistent SQLite state; SaveMarketSnapshot перепроверяет и атомарно пишет
  существующие wb_daily_snapshots/current/diffs + background_job_runs.aggregate_json.
  Повторный расчёт внутри save проверяет актуальность aggregate, это тот же алгоритм.
- `background_job_runs.result_json`: PipelineResult со всеми шагами, counts,
  timestamps, durations, aggregate, save result, безопасной ошибкой и retry deadline.
  Секреты, raw datasets, grant, lease_owner и protocol dumps туда не записываются.
- Runtime JSON datasets существуют только для передачи между tools. Источники
  времени получения — envelopes.fetched_at; snapshot ownership — trusted run context.

| Шаг / реальный MCP tool | Go method | Класс | WB_WRITE |
|---|---|---|---|
| wb_get_prices | wildberries.Client.Prices | READ_ONLY | false |
| wb_get_wb_stocks | wildberries.Client.WBStocks | READ_ONLY | false |
| wb_build_market_summary | background.WBDailySyncExecutor.BuildMarketSummary | LOCAL_COMPUTE | false |
| wb_save_market_snapshot | background.WBDailySyncExecutor.SaveMarketSnapshot | LOCAL_WRITE | false |

Всего реальный ListTools менеджера: 10 tools. Два LOCAL_WRITE (schedule и save),
0 WB write tools. Старый wb_get_daily_summary остаётся только retrieval.

### L2 — контракты и инварианты

Price output: `mcpclient.Envelope[[]wildberries.Price]`.
Stock output: envelope `[]wildberries.Stock`, клиент оборачивает его в StocksResult.Value.
MarketInput: typed Prices + Stocks envelopes (source/fetched_at/complete/untrusted_data/data).
Build получает previous state из SQLite по trusted workshop/connection и возвращает
существующий Aggregate: products_count, price_changes_count, stock_changes_count,
zero_stock_count, low_stock_count, errors_count, prices_ok, stocks_ok.
SaveInput: MarketInput + тот же Aggregate. SaveResult: saved, price_records_saved,
stock_records_saved, aggregate_saved, current_state_updated.

PipelineResult: pipeline_name/status/started_at/finished_at/duration_ms/steps/
aggregate/save_result/error. Каждый step: key/tool/required/classification/wb_write,
status/timing/input summary/output summary/error. Ошибка: code + optional retry_not_before.
Пропущенные steps имеют SKIPPED и пустые start/finish: они не выполнялись.

1. Tools фиксированы кодом. Runner вызывает существующие typed Prices/Stocks,
   которые выполняют MCP CallTool; local steps используют тот же SDK client.
2. Scope не принимается из tool arguments. Manager выдаёт expiring one-use grant,
   связанный с tool + Job/Connection/Revision/RunID/lease owner.
3. Перед каждым шагом проверяются membership, connection/revision, job и активный
   lease; перед local compute/save проверки повторяются внутри SQLite transaction.
   После завершения run уведомление проверяет права/connection, не running lease.
4. Required step error останавливает цепочку. Уже успешные steps остаются в trace.
   Aggregate при save failure может остаться transient; успешный save не заявляется.
5. Save: обе выборки, diff/current и aggregate — одна transaction, без HTTP.
   Invalid/duplicate IDs, incomplete envelope, отрицательные значения, overflow,
   stale scope/lease и несовпадение пересчитанного aggregate отклоняются.
   Отсутствующие позиции не обнуляются и не удаляются.
6. Централизованный limiter остаётся единственным. Дополнительных retries нет;
   typed RateLimitError переносит retry_not_before в pipeline и JobRun.
7. DailySummary выбирает последний status=success. Не запускает pipeline/WB.
8. Scheduled default08:00 в workshop timezone и Run Now вызывают тот же executor.
   Ранее выбранное пользователем время не перезаписывается.
9. `/wb_auto trace` использует существующий debug route, проверяет управление
   мастерской и показывает сохранённый pipeline trace, без нового вызова WB.

### L3 — проверки и демонстрация

- Real MCP ListTools содержит все 4 tools; один in-memory manager/session.
- Manual baseline и scheduled update: price10000→12000, stock12→3 реально сохранены;
  summary input1price+3stocks, products3, changes1+1, zero1, low2; aggregate совпадает
  между summary step, save input, PipelineResult и Day18 summary retrieval.
- Отказ prices, stocks, summary, save: корректные FAILED/SKIPPED; бизнес-снимков0.
  Ошибка INSERT stocks после INSERT prices откатывает snapshots/current/diffs.
- Отмена перед save и malformed aggregate исключают запись.
- Forged/expired grant, nonexistent lease и arbitrary WB write tool отклоняются.
- Существующие rate deadline, revocation, inventory isolation, schedule/DST,
  duplicate-run и architecture regression tests сохранены.
- Day18 тесты partial policy обновлены явно под strict WA-D161; tests не удалены.

Демонстрация в прежнем EXE:

```powershell
.\bin\workshop-agent.exe day19-mcp-report
```

Offline HTML: `reports/day19-mcp-composition/20260925T104145.168872300Z/report.html`.
JSON с фактическим ListTools, successful/failed PipelineResult рядом в result.json.
Временная fixture DB удаляется после демонстрации; отдельной Day19 DB не остаётся.
Successful run28ms: SUCCESS/SUCCESS/SUCCESS/SUCCESS, saved1+3.
Failure run1ms: SUCCESS/FAILED/SKIPPED/SKIPPED, wb_api_error; snapshots остаются8.
Сборка canonical bin/workshop-agent.exe v0.19.0 выполнена; новых EXE нет.
Полный финальный `go test ./...` — exit0: background4.617s, mcpmanager2.956s, Telegram55.669s; все остальные пакеты PASS/cached/no test files. `git diff --check` — exit0.
Live WB/Telegram проверка не выполнялась и этой offline-приёмкой не утверждается.


Исходный аудит перед рефакторингом: wb_get_prices, wb_get_wb_stocks,
wb_get_seller, wb_get_products, wb_get_new_orders, wb_get_order_statuses,
schedule_wb_daily_sync, wb_get_daily_summary. Execute Day18 загружал источники
и сразу сохранял каждый отдельно, а savePrices/saveStocks/save считали diff и
aggregate. Эта последовательность удалена из executor; её единственный вариант
теперь находится в runner, а normalization/diff — в prepareMarket. Новые local
handlers только вызывают прикладные методы, не содержат SQL или CallTool.
