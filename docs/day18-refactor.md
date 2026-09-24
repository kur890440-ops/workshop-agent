# Day 16–18: один процесс, общий планировщик

Дополнение: [WA-D157 — сохранённая сводка через MCP](day18-summary.md). Теперь 8 tools; результаты 7-tool приёмки ниже относятся к предыдущей версии.

## Принятые решения @NO_COMPRESS @PRESERVE

- WA-D154: по новому заданию пользователя production MCP использует официальный
  SDK `NewInMemoryTransports`, один сервер и одну долгоживущую клиентскую сессию.
  Это заменяет прежние решения Day16/17 о STDIO и отдельном исполняемом файле.
  Исторические отчёты STDIO остаются свидетельствами прежней реализации.
- WA-D155: общий BackgroundJobService, repository, Scheduler и registry. WB-логика
  находится в executor, snapshots отдельно от jobs/runs. Миграция 114 сохраняет
  существующие задания и историю; проверяется только на временных БД.
- WA-D156: `schedule_wb_daily_sync` — локальная mutation, адаптер CreateJob.
  Scope передаётся доверенным приложением одноразовым непрозрачным grant через
  MCP metadata; ID пользователя/мастерской не принимаются как tool arguments.
  WB read allowlist не включает этот инструмент. Секрет WB не проходит через MCP.

## Аудит до изменений

EXE: workshop-agent.exe, wb-mcp-server-day16.exe, mcp-wb-smoke-day16.exe,
резервный workshop-agent.exe~. Cmd: workshop-agent, wb-mcp-server, mcp-wb-smoke,
init-db (прежняя утилита, не production dependency).
MCP SDK v1.7.0, STDIO child process; шесть WB read tools. HTTP и секреты —
wildberries.Client. В background.Service были смешаны управление, ticker,
claim/lease/history, WB вызовы и отправка summary. Инициализация дочерних
процессов повторялась в bootstrap и диагностике. Jobs были ограничены WB на уровне SQL.

## Реализация и границы модулей

| Модуль | Ответственность |
|---|---|
| cmd/workshop-agent/main.go | Композиция зависимостей, startup/shutdown, offline subcommands |
| internal/integrations/mcpmanager/manager.go | Один сервер, регистрация WB + scheduling module, доверенный scope |
| internal/integrations/mcpclient/call.go | SDK server/client sessions, NewInMemoryTransports, настоящий initialize/ListTools/CallTool, close |
| internal/integrations/wbmcp/server.go | Прежние typed WB schemas/handlers, safe envelopes/errors |
| internal/background/service.go | CreateJob/GetJob/ListJobs/UpdateJob/PauseJob/ResumeJob/CancelJob; авторизация и validation |
| internal/background/repository.go | Модель Job, чтение persistent jobs из SQLite |
| internal/background/schedule.go | Next: календарное ежедневное расписание в IANA timezone |
| internal/background/scheduler.go | Единственный ticker 15 с, claim/lease/run history/dispatch/finalization |
| internal/background/registry.go | job_type → Executor; второй тип используется только в тестах |
| internal/background/wb_executor.go | WBDailySyncExecutor; только MCP Prices/Stocks; scope/revision |
| internal/background/snapshots.go | WB snapshots/current/diff/aggregate, без производственных записей |
| internal/background/trace.go | Run metadata и безопасный `/wb_auto trace` |
| internal/storage/background_generic.go | Миграция 114 с сохранением старых ID и связанных строк |

Один сервер `workshop-agent-wb` содержит оба tool module: отдельный сервер
для локального scheduling не нужен. `wbmcp.Tools()` описывает только 6 WB read tools.
Седьмой `schedule_wb_daily_sync` регистрируется через SDK `mcp.AddTool` в manager.
Клиент выполняет ListTools, проверяет классификацию и разрешает executor только
фиксированные wb_get_seller/wb_get_prices/wb_get_wb_stocks. Local mutation отдельно
от WB write count: диагностика показывает 0 WB write / 1 local mutation.

Цены: `wb_get_prices` → `wildberries.Client.Prices`.
Остатки: `wb_get_wb_stocks` → `wildberries.Client.WBStocks`.
HTTP не продублирован. Foreground `/wb_stocks` сохраняет существующую проверку
прав, identity и trace, теперь с transport из фактического discovery.
Старый `/wb stocks` остаётся прежним cache/sync интерфейсом, вне MCP-flow.

## Job / Run / Schedule / данные

`background_jobs`: id, workshop_id, created_by_user_id, job_type, status,
schedule_type=DAILY_AT_TIME, local_time, timezone, parameters_json,
notification_enabled, next_run_at, last_run_at, last_success_at, last_result,
created_at, updated_at. Статусы: active/paused/completed/cancelled/failed.
WB connection_id перенесён в typed parameters; общей таблице больше не нужен
обязательный кабинет. Ни credentials, ни выбираемые пользователем tool names
WB executor в parameters не принимает. Единственная WB-настройка там — connection_id.

`background_job_runs`: id, job_id, workshop_id, job_type, local_date, status,
started_at, finished_at, duration_ms, result_json, aggregate_json, error_code,
error_message, lease_owner, lease_until, notification_state.
Ошибка run не выключает recurring job. UNIQUE(workshop_id,job_type,local_date)
и partial UNIQUE(job_id WHERE running) защищают от дубля и overlap. Lease 2 минуты,
продление каждые 20 секунд; полный execution timeout 4 минуты. Истёкший run
помечается failed/interrupted; уже сохранённые источники остаются в snapshots.

По умолчанию local_time=08:00, timezone из workshop_settings (Europe/Moscow при
первичной настройке); next_run_at — абсолютный UTC Unix timestamp. time/tzdata
встроена для Windows. Next считает календарный следующий день, не +24 часа.
После restart scheduler читает SQLite; максимум один catch-up за сегодняшнюю
локальную дату, без воспроизведения пропущенных дней. Уже неудачная попытка
за эту дату автоматически не повторяется — это прежняя консервативная гарантия.

`wb_daily_snapshots` сохраняет исторические строки run/source_tool/item_key,
`wb_daily_current` — последние известные строки workshop/connection/tool/key,
`wb_daily_diffs` — old/new/delta по совпадающим ключам. Первый снимок — baseline.
Цены сравниваются по варианту, остатки — по варианту/складу; счётчики изменений
агрегируются по nmID. Aggregate хранится в run. Неполная/ошибочная загрузка
не обнуляет отсутствующие позиции. Цены WB не меняют себестоимость, остатки WB —
инвентарь мастерской. Порог low stock находится в workshop_settings; по умолчанию 5,
его можно задать MCP input или Telegram. Нулевые остатки считаются отдельно.

Миграция 114 пересоздаёт jobs/runs и referencing tables в одной транзакции,
с включёнными FK, сохранением ID и данных. Перед миграцией существующего файла
storage создаёт backup через VACUUM INTO. Проверена заполненная 113 fixture,
повторный вызов миграции и foreign_key_check. Рабочая БД не открывалась.

## Scheduling adapter, управление и lifecycle

Typed input: local_time, timezone, notification_enabled, low_stock_threshold?.
Typed output: job_id, job_type, status, schedule, next_run_at.
Scope поступает из `Manager.ScheduleWB(ctx,user,workshop,input)` через случайный
одноразовый grant с TTL 1 мин в MCP metadata. Handler потребляет grant, затем
вызывает тот же `Service.CreateJob`, что Telegram. Сервис повторно проверяет
marketplace.manage и текущую активную мастерскую внутри транзакции. Unknown fields
и произвольные user/workshop IDs в arguments не принимаются. Handler не содержит
goroutine, Sleep или ticker и не вызывает WB. Повторное создание возвращает
существующее задание без скрытой смены расписания/включения отменённого задания.

Telegram: `/wb_auto enable`, `run`, `pause`, `resume`, `cancel`,
`/wb_auto time 08:00 Europe/Moscow [порог]`, `/wb_auto trace`.
Run Now делегирует тому же scheduler.execute/executor; не обходит daily key.
Уведомление содержит количество товаров, изменённых цен/остатков, нулевых/низких
остатков, ошибок и время следующего запуска. Sender проверяет внутренний user ID
и membership; notification_enabled=false исключает отправку.

Startup: config → SQLite/migrations → domain + job service/registry → один WB client
→ один MCP server/tool registration → SDK in-memory sessions/ListTools → bind
executor/agent → Telegram adapter → scheduler.Start → polling.
Shutdown: application cancellation прекращает polling/новые ticks, scheduler.Close
ждёт текущий run, Telegram WaitBackground ждёт ручные операции, manager.Close закрывает
client/server sessions, затем закрываются domain/SQLite handles. Отмена отдельного
MCP запроса не уничтожает общую сессию. Новая сессия на каждый run не создаётся.

Ограничения: приложение должно работать для выполнения расписания; после простоя
действует catch-up. Ошибки отправки summary записываются, outbox/retry доставки в
этом этапе нет. Реальный WB/Telegram требует отдельной проверки владельцем;
offline результаты не доказывают доступность API или права реального токена.

## Запуск и диагностика

Остановить работающий бот Ctrl+C перед заменой бинарника. Из корня проекта:

```powershell
go build -o bin/workshop-agent.exe ./cmd/workshop-agent
.\bin\workshop-agent.exe --version
.\bin\workshop-agent.exe
```

Без конфигурации, рабочей БД, WB token и внешних запросов:

```powershell
.\bin\workshop-agent.exe mcp-status
.\bin\workshop-agent.exe day17-mcp-report
.\bin\workshop-agent.exe day18-background-report
```

cmd/wb-mcp-server и cmd/mcp-wb-smoke удалены после переноса диагностики и тестов.
Прежний cmd/init-db оставлен как разработческая утилита вне production build;
обычная инициализация БД также доступна через основной `setup` subcommand.

## Доказательства и матрица проверки

| Требования тестов задания | Доказательство |
|---|---|
| 1–6: in-process, ListTools, CallTool, без child/listener | mcpclient discovery/call tests; manager architecture AST test; mcp-status |
| 7: несколько job types | background.TestOneSchedulerHandlesTwoTypesAndGenericManagement |
| 8–13: persistence, timezone, pause/resume/cancel | background service/generic tests; storage migration114 test |
| 14–24: MCP prices/stocks, snapshots/current/diff/aggregate/изоляция/summary | manager counted API; background tests; Day18 offline report |
| 25–28: registration, typed schemas, общий CreateJob, без ticker | manager.TestSchedulingToolScopeSchemasAndExecution; architecture AST test |
| 29: WRITE запрет | mcpclient.TestWriteClassificationBlocksWBExecution; fixed dispatch allowlist |
| 30–31: secrets/scope | service typed validation; manager tests: unknown fields, чужой user/workshop, VIEWER, stale active workshop, grant replay |
| 32–34: duplicate/overlap/error | background service tests; MCP safe failure tests; lease/revocation tests |
| 35 | go test ./...; финальный результат фиксируется в WORKSTATE |

Offline Day17: `reports/day17-first-mcp-tool/20260924T143348.151387200Z/report.html`.
Offline Day18: `reports/day18-background-jobs/20260924T143348.455734200Z/report.html`:
Passed=true; 2 runs, 8 snapshot rows, 2 diffs; 3 products; price changes=1,
stock changes=1, zero=1, low=2, errors=0. Scheduling tool создал job через MCP;
повторные ticks после сдвига clock создали один catch-up. Одна MCP session
переиспользована между runs. Summary захвачен в отчёт, реальные сообщения не отправлялись.

## Финальная приёмка

2026-09-24: `go test ./...` — exit 0; `git diff --check` — exit 0.
Clean build после удаления прежнего основного EXE — exit 0.
В bin/ один файл: workshop-agent.exe, 23 113 728 bytes, версия 0.18.2.
Готовый бинарник выполнил --version, mcp-status и оба offline report subcommands
с exit 0. ListTools: 7, transport=in-memory, WB write=0, local mutation=1,
SessionClosed=true, ServerClosed=true. Дополнительные EXE удалены.
Day19 не начат; реальная WB/Telegram интеграция здесь не запускалась.
