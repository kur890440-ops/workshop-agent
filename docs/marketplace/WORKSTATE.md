# WA-D173 - semantic output truncation recovery

Detect finish_reason=length before empty-content check, JSON included.
One bounded recovery1024->4096, summed usage, explicit typed terminal error.
Actual screenshot cause unconfirmed without provider finish reason.
go test ./... final exit0; canonical bin/workshop-agent.exe v0.21.4 built
and --version verified. No live requests, no .env reads.
Evidence: reports/ozon-seller-v2-fix/llm-0.21.4-tests.txt.
See docs/day20-mcp-orchestration.md WA-D173.

# WA-D172 - named product totals and follow-up context

Implemented optional product_name/total semantic fields, local matching, separate
source totals with partial-data labels, existing scoped short-term intent memory.
go test ./... exit0; TestDay20SemanticTelegramAndTrace separately exit0.
No live LLM/vendor requests. See docs/day20-mcp-orchestration.md WA-D172.

# WA-D171 - free-form marketplace LLM interpretation

Existing semantic prompt extended for colloquial names, source ambiguity and
negation. Generic plural stock questions use AUTO (existing SELLER default).
Explicit FBS/FBO and BOTH preserved; no architecture/API changes.
go test ./... exit0; final LLM prompt tests exit0. Canonical EXE rebuilt,
--version WorkshopAgent v0.21.2. No live model/vendor calls, .env unread.
Mock tests verify schema/routing, not live language accuracy.
See docs/day20-mcp-orchestration.md WA-D171.

# WA-D170 - Seller v2 filter fix, offline verified

See docs/ozon-seller-v2-fix.md. Targeted client/SQLite/actual MCP tests pass.
25 cached products/27 valid SKU verified read-only. Full suite exit0:20 packages,406 passed tests,0 failures.
Canonical bin/workshop-agent.exe built; --version WorkshopAgent v0.21.1.
Evidence: reports/ozon-seller-v2-fix/tests.jsonl, mock-refresh.txt, build.txt.
Live API compatibility remains unverified; awaiting user refresh after restart. No real API call, .env unread.

# Day20 — WA-D169 implemented / offline verified

Task: attachment65696e54-e143-423c-9118-65c8ab56c31b. Read docs/day20-mcp-orchestration.md
for L0-L3 decisions. Implemented typed intent/router/query read-model, existing
MCP dispatch, normalization/mapping/trace, semantic Telegram, migration119.
Targeted tests passed: actual SDK calls WB+Ozon/WB-only/Ozon-only/long/partial/
mapping; LLM schema/mock variants; Telegram semantic route. Evidence generated:
reports/day20-mcp-orchestration/20260928-day20/evidence.json.
Full go test -json ./... exit0 (session19174):20 passed packages,397 pass events,
0 failures. git diff --check exit0. Standalone report.html generated (15sections).
Canonical bin/workshop-agent.exe build exit0; --version WorkshopAgent v0.21.0.
No running process; no new executable name, no bot startup or production migration.
Changed Day20: marketplacequery/*, llm types/semantic/tests, mcpclient call/query,
mcpmanager query/tests, storage migration119/sqlite, Telegram semantic/query/tests/
bot, main/version, docs/layer links and report artifacts. Prior unrelated changes retained.
No live requests or .env reads. Stop at Day20; live-model/vendor validation and
previously proven Ozon seller missing sku/offer filter remain explicit limitations.

# Release 0.20.1 — 2026-09-28

User instruction: rebuild canonical bin/workshop-agent.exe by default after code
changes; keep the same filename and do not create extra executables.
No running workshop-agent process found. Build exit0; --version output:
WorkshopAgent v0.20.1. Includes WA-D168 HTTP400 diagnostics.
Application/bot not started; .env not read; no live API calls.
This release supersedes the no-rebuild statements in the historical audit below.

# WA-D168 — targeted Ozon Seller 400 audit (2026-09-28)

Source of truth: docs/ozon-seller-400.md. Pinned Python and Go default body both
{"limit":100}; no proven schema mismatch. No endpoint switch or N+1 added.
Added bounded sanitized400 diagnostics, BAD_REQUEST, owner debug pointer and tests.
Changed: ozon request.go/request_test.go/format.go, new diagnostics.go/test,
docs/ozon-seller-400.md, marketplace README/L2/L3/WORKSTATE.
Targeted Go tests passed; full go test ./... exit0 (session43398, Telegram69.684s),
git diff --check exit0. Read-only catalog inspection:
25 products/27 unique positive SKU, no identity substitution; seller sends none.
No inherited credentials, .env unread, no live requests, no EXE build/bot startup.
Next: stop with unknown historical400 cause and v4 proposal
awaiting architectural approval. Existing EXE remains0.20.0 without this patch.

# Release 0.20.0 - canonical EXE updated

User requested EXE build. No running workshop-agent process found.
go build -o bin/workshop-agent.exe ./cmd/workshop-agent: exit0.
bin/workshop-agent.exe --version: WorkshopAgent v0.20.0.
No application startup, .env read, API calls or production DB migration performed.
WA-D167 remains PARTIALLY ACCEPTED pending official/live contract verification.

# WA-D167 ? PARTIALLY ACCEPTED / offline end-to-end verified

2026-09-27. ?????????? ????????????: docs/ozon-stage-a-implementation.md.
????????118 + Go client/decoders/service/SQLite + ozonmcp module + existing MCP
client/manager + Telegram /ozon + startup wiring ??????????? ? ?????????.
????????? go test -json ./... session74333: exit0, 19 passed packages,
377 successful test events, zero failures. WB/Day18/19/Tasks/Memory/Users passed.
????????? go build -o NUL ./cmd/workshop-agent: exit0 (????? ?????????? decoder/metrics fix).
git diff --check: exit0 (?????? CRLF notices); git check-ignore .env ???????????.
reports/ozon-stage-a/20260927T204000Z/report.html, evidence.json, tests.jsonl:
????????? MCP ListTools14 tools (3 Ozon READ); catalog100 ->2 pages+1 batch=3 HTTP;
seller2 HTTP/FBO1; cache rendering0 Product calls; partial valid saved/missing
retained; source history separated; reopened DB cooldown ->HTTP0/pages0.
????? Telegram tests: commands/buttons/cache, credentials ?? history/memory,
async return/revoked late reply. Migration test: WB rows/FKs retained/provider guard.
????????? invalid schema ?????? INVALID_RESPONSE; missing pagination+valid rows
??????????? ??? PARTIAL. Trace ?????? HTTP/SYNC/CACHE/DEDUP ?? ????????? HTTP200
? ???????? ???????? normalized records.
????????: ??????????? ?????????? official OpenAPI/?????????/??????/????? ?????????
endpoints; live ??????? ?? ????????. Reference models ???????????? ???? ? provenance.
??????? real API/Telegram/production DB/.env reads/EXE changes.
????????? ??? ?????? ??? ????????? ??????????? ???????????: official/live validation;
?? ????????? Ozon scheduler/pipelines/writes. ??????? ????? schema blocker WA-D166
?? ?????????? ?????????? MCP/storage/UI ?? ????????? ???????????? WA-D167.

# WA-D167 ? ???????? ?????????? / ????????? ????????

????????? migration118, ???????? ???????/SKU relation/current/history/run/cooldown/
trace, auth/scope/revision service, ?????????? ??????????? decoders, ozonmcp module,
?????? MCP client dispatcher/manager, main wiring, /ozon commands/menu/async refresh.
???????? ListTools ????????14 tools (3 Ozon read + ???????????? WB/local).
CallTool ? local TLS HTTP mock ? temp SQLite ??????: 100 products ->2 pages+1 batch
(details100); seller2 pages; FBO1 batch; rendering0 Product API calls; restart
cooldown blocked HTTP0; partial98/invalid semantics covered by small equivalent
fixtures, FK migration preserves WB; auth/forged grant/stale callback/disable tested.
?????? ozon/storage/mcpmanager ? ????? Telegram tests ??????.
?????? go test -json ./... ?????? session62528; ?????????:
reports/ozon-stage-a/20260927T204000Z/tests.jsonl + evidence.json.
NEXT: ??????????? ?????? exit, build -o NUL, HTML report, final docs/state.
??????? real API/production DB/.env reads/EXE changes.

# WA-D167 ? Ozon Stage A continuation / IMPLEMENTING

2026-09-27. Source: attachment e0d91aa3-109a-4d49-bf3e-9a655e04fed3.
WA-D167 in docs/ozon-stage-a.md supersedes whole-task contract blocker.
Implemented pending acceptance: migration118 provider-aware connection rebuild,
normalized catalog/SKU relation, source runs/current/history, cooldown/trace;
Ozon service authorized attach/disable, persistent State, source singleflight,
reference-backed minimal decoders with bounded pagination and batch details;
ozonmcp module and dedicated mcpclient dispatcher on existing manager/session;
/ozon commands/menu, cached views, explicit async refresh, main startup wiring,
empty local env example keys and pre-history credential input rejection.
Focused pre-integration tests ozon/mcpmanager/storage passed before final UI edits.
No .env or production DB read; no real API calls; EXE unchanged.
NEXT: integration tests through real in-memory MCP with mock HTTP + temp SQLite;
security/restart/scope/partial tests, report, full regression/build.
Models evidence: audited Python requests + public OzonFromGAS types.ts seller v2,
public ru-marketplaces-api-docs response examples; official current response
schema/live permissions still not verified. This is reference evidence, not live API.

# WA-D166 — Ozon Stage A / IN PROGRESS, CONTRACT BLOCKER

2026-09-27. Основной документ: ../ozon-stage-a.md. Это не завершённый production этап.
Изучены текущая архитектура, WB model, auth, migrations117 и audited upstream.
Официальные Seller docs/OpenAPI снова недоступны (redirect loop); response schemas
stock endpoints в Python reference не типизированы. У пользователя запрошен путь
к official OpenAPI или обезличенным response fixtures. Не угадывать quantity/SKU/
warehouse/termination; не регистрировать fake tools.

Добавлены internal/marketplace/ozon/request.go и request_test.go: подтверждённые
request shapes четырёх read endpoints, fixed HTTPS/redirect denial/response bound,
server-only secrets, guard hook, singleflight, serial request gate, bounded retries,
429 deadline без обрезания, State interface и safe traces. Ответы остаются RawMessage
внутри клиента, поскольку нормализованный контракт не подтверждён. State пока не
имеет production SQLite implementation: нельзя заявлять persistent cooldown готовым.

НЕТ production wiring, Ozon MCP registration, SQLite migration, catalog cache,
source snapshots, Telegram UI или Ozon background jobs. EXE/.env/рабочая DB не менялись.
Основная MCP architecture, WB и scheduler сохранены. HTTP calls только mock.

Добавлен security-audit/ozon-mcp/go.mod как граница Python audit fixture subtree:
исходный go test ./... упирался в Access denied на pytest-temp. Upstream source
аудита не изменён. Старые и текущие аудиторские файлы не удалялись.

Проверки: новый пакет go test прошёл; полный go test ./... exit0; go build -o NUL
./cmd/workshop-agent exit0. Финальный JSON test run с доступом к Go cache: exit0;
reports/ozon-stage-a/20260927T194512Z/tests.jsonl. Предыдущая sandbox-попытка имела
Access denied к Go cache; это устранено повтором с необходимым доступом.
Автономный промежуточный отчёт: reports/ozon-stage-a/20260927T194512Z/report.html;
trace.json содержит фактическую запись mock HTTP test, не реального Ozon API.
Следующий шаг: получить подтверждённые response contracts, продолжить п.4–8 L3
WA-D166; не выдавать mock HTTP tests за приёмку всего Stage A.

# WA-D165 — IMPLEMENTED / VERIFIED

Source0.19.4. Seller summary items in existing aggregate/MCP compute-save, one formatter morning/retrieval, old run snapshot compatibility, UTF16 chunks. Read-only real preview67 rows ->23 positions, one message. go test ./... exit0 (Telegram52.403s). go build -o NUL ./cmd/workshop-agent exit0. git diff --check exit0. Day19 report exit0: reports/day19-mcp-composition/20260927T131952.129608200Z/report.html. Real saved preview: reports/wb-morning-summary/report.html; raw normalized aggregate and tests.txt alongside. Canonical0.19.3 currently running PID72336; do not overwrite live EXE. No live WB/Telegram/production changes.

# WA-D164 — COMPLETE / VERIFIED

Source v0.19.3; presentation only. See ../wb-stock-presentation.md. go test ./... exit0; canonical build exit0; --version WorkshopAgent v0.19.3; git diff --check exit0. Test output reports/wb-stock-presentation/tests.txt. No running application found; canonical EXE updated, not started. No live WB/Telegram or production data changes.

# WA-D163 — COMPLETE / VERIFIED

Implementation source0.19.2; see ../wb-independent-operations.md. All source paths retain local credential binding, no network preflight. Final go test ./... exit0 (marketplace15.962s, Telegram61.189s); git diff --check exit0. Canonical bin/workshop-agent.exe rebuilt successfully as v0.19.2; no running process at build time. --version verified. Bot not started; no live API or production DB changes. Full test output: reports/wb-independent-operations/tests.txt.

# Stock sources — IMPLEMENTED / VERIFIED (WA-D162)

Implementation and offline acceptance complete, source version 0.19.1.
SELLER and WB independent: source models, MCP tools, partial validation/save,
source current/history, migration117, separate Telegram views, PARTIAL_SUCCESS pipeline.
Details: ../wb-stock-sources.md. Historical Day19 strict behavior below is superseded.

Final verification 2026-09-25:
- go test ./...: exit0 (storage8.949s, Telegram59.981s); full output in report tests.txt.
- go build -o NUL ./cmd/workshop-agent: exit0, no new executable.
- git diff --check: exit0 (line-ending warnings only).
- wb-stock-sources-report: exit0, 34 received /33 valid /1 invalid /33 saved;
  WB403, previous3 WB rows retained, PARTIAL_SUCCESS.
  reports/wb-stock-sources/20260925T131810.930154200Z/report.html
- day18-background-report: exit0, reports/day18-background-jobs/20260925T131827.238539100Z/report.html
- day19-mcp-report: exit0, reports/day19-mcp-composition/20260925T132211.913015800Z/report.html

Deployment NOT performed: canonical bin/workshop-agent.exe v0.19.0 is running
(PID64180 at verification), not overwritten/stopped. Migration117 tested only on
fixtures, not applied to production. Stop app and rebuild canonical binary before use.
No .env reads, live WB calls, production DB writes or real Telegram messages.
Official documentation direct fetch returned498; indexed official evidence and
previous cached HTTP200/403 used, freshness limitation recorded in report.
No further feature work required; remaining action is controlled deployment.
# Day19 MCP composition — COMPLETE

WA-D161: [контракты и проверки](../day19-mcp-composition.md).
Реализованы MCPPipelineRunner, фиксированные prices/stocks/build/save, два local
MCP tools с one-use grants, атомарный existing snapshot storage, strict errors,
персистентный result_json и /wb_auto trace. Scheduled08:00 и Run Now используют
одну цепочку. Новых миграций/таблиц/EXE/процессов нет. Canonical EXE v0.19.0 собран.

Day19 offline report: reports/day19-mcp-composition/20260925T104145.168872300Z/report.html.
Фактический SUCCESS28ms (1price+3stocks, changes1+1, zero1, low2); FAILED1ms
на stocks, summary/save SKIPPED. Mock WB, temp SQLite удалена, .env не читался.
Day18 прежний report тоже exit0: reports/day18-background-jobs/20260925T103927.912052500Z/report.html.
Финальный go test ./... — exit0 (background4.617s, mcpmanager2.956s,
Telegram55.669s); canonical build и git diff --check — exit0.
Задача завершена. Day20 не начинать; live WB/Telegram не проверялись.


Основные новые файлы: internal/background/pipeline.go, pipeline_test.go;
internal/integrations/mcpclient/pipeline.go; internal/integrations/mcpmanager/pipeline.go,
pipeline_test.go; internal/experiment/day19.go; docs/day19-mcp-composition.md.
Существующие Day18 tests частичного успеха адаптированы под strict WA-D161,
остальные regression cases сохранены. Полные критерии и интерфейсы в основном документе.

Previous state follows.

# Marketplace — оперативное состояние

## Дополнительная проверка отсутствия секретов в trace

TestTraceSecretsAndSharedGroupBlock и TestTransportErrorSecretNeverReachesPersistedTrace
прошли вместе с TestTraceContractAndCompletedResponse (exit0, 0.586s).
Mock429 на orders/new, затем через10s orders/status той же группы от Telegram:
HTTP-вызов всего1; второе событие BLOCKED_LOCALLY/request_sent=false/HTTP0/attempt0,
с тем же deadline. Фиктивные token, Authorization/Bearer и password внедрялись
в body, headers, metadata и network error; в сериализованных trace/observations
их нет. Runtime-код не менялся, EXE остаётся0.18.6, пересборка не требовалась.
Настоящие .env/БД/секреты и WB/Telegram не использовались.


## Контекст HTTP429, WA-D160, v0.18.6

Добавлены migration116 и persisted incident snapshots (429+до50 предыдущих
событий, последние10 инцидентов); реальные host/path/rate_key; caller с фоновым
job type; /wb history и /wb incident с проверкой управления и пагинацией.
Целевые marketplace/WB/storage/MCP/background тесты прошли, включая reopen SQLite,
сохранение cooldown, retention и запрет доступа к чужим данным.
Завершено: полный go test ./... — exit0 (marketplace20.960s, Telegram69.469s).
Сборка прежнего bin/workshop-agent.exe v0.18.6 — exit0. Миграция116 применяется
при следующем обычном запуске; рабочий бот не запускался, новых EXE нет.
Реальные WB/Telegram, .env и рабочая БД не использовались.


## Формат логов WB, WA-D159, v0.18.5

Реализован точный JSON-контракт пользователя: operation, start/finish, headers,
1-based attempt, result из4 значений. Запись после body/JSON validation, безопасные
нулевые поля для cache/local block. Целевые тесты marketplace/integrations/background
прошли. Финальный go test ./... — exit0 (WB10.757s, marketplace8.744s, Telegram57.394s).
Сборка того же bin/workshop-agent.exe v0.18.5 — exit0. Новых EXE нет.
Задача завершена; рабочий бот не запускался.
Реальные WB/Telegram не запускались; .env/рабочая БД не читались.


## Текущий аудит rate limits, 2026-09-25

[WA-D158](../wb-rate-limit-audit.md): аудит завершён, migration115/persistent
identity/central group gates/header observation/structured MCP cooldown/run deadline
и /wb debug реализованы. Новый EXE не создаётся: прежний bin/workshop-agent.exe v0.18.4.

Полный go test ./... прошёл дважды. Последняя до live-наблюдения полная проверка:
Telegram65.533s, marketplace13.087s, wildberries10.957s; exit0. Сборка canonical,
--version и mcp-status прошли: 8tools, in-memory, обе session закрыты.
Offline Day18: reports/day18-background-jobs/20260925T085721.059554200Z/report.html.

Рабочий бот PID53544 был запущен в начале аудита, затем завершился самостоятельно
относительно действий агента; агент не посылал Stop-Process/Telegram messages.
Только после проверки отсутствия процесса обновлён прежний EXE и выполнена1 live
попытка prices: HTTP200, Remaining0/Limit1, без seller-info. Технический pacing
записан в существующую marketplace_cooldowns; миграции/бизнес-таблицы не менялись.
Report: reports/wb-rate-limit-audit/20260925T085703.592905300Z/report.html.
Сохранённый seller-info cooldown: 2026-09-25T12:17:48.284Z. Токен BASE локально,
без проверки JWT-подписи. Секреты не выводились и .env не записывался.

Завершено: финальный go test ./... — exit0 (wildberries10.830s,
marketplace5.986s, Telegram55.071s), включая fallback Remaining0 без Retry/Reset.
Каноническая сборка повторно прошла после этой правки, v0.18.4. Новых EXE нет.
HTML/JSON/terminal.txt отчёта дополнены фактическими доказательствами; повторных
live-вызовов не было. git diff --check — exit0. Бот остановлен, миграция115
применится при следующем обычном запуске. Задача завершена, следующий этап не начат.

## Дополнение Day18: summary, 2026-09-24

[WA-D157](../day18-summary.md): wb_get_daily_summary читает persisted aggregate
через тот же MCP/server/service. Команда /wb_auto summary [trace], кнопка
«Последняя сводка». Новых таблиц/миграций/планировщиков/EXE нет.
Summary tests и финальный go test ./... — exit 0 (Telegram 54.238s,
background 3.463s, mcpmanager 0.846s; остальные passed/cached).
Offline report: reports/day18-background-jobs/20260924T145621.939783000Z/report.html;
summary-mcp.json — фактический MCP result, WB calls delta=0.
Сборка прежнего bin/workshop-agent.exe v0.18.3 — exit 0, других EXE нет.
Готовый EXE: --version, mcp-status (8 tools/in-memory), day18-background-report — exit 0.
git diff --check — exit 0. Рабочие .env/БД и реальные WB/Telegram не использовались.
Дополнение завершено; дальше остановиться.
Ниже — историческое состояние предыдущей версии.

## Текущий рефакторинг Day16–18, 2026-09-24

Источник требований/новых решений: [WA-D154–156](../day18-refactor.md).
MCP in-memory, общий service/repository/scheduler/registry, migration114,
scheduling tool, bootstrap и regressions реализованы. Старые cmd server/smoke удалены.
Полный `go test ./...` прошёл, включая дополнительные architecture/safety tests.
Offline Day17: `reports/day17-first-mcp-tool/20260924T143348.151387200Z/report.html`.
Offline Day18: `reports/day18-background-jobs/20260924T143348.455734200Z/report.html`,
Passed=true, 2 runs / 8 snapshots / 2 diffs, реальные MCP ListTools/CallTool в памяти,
WB mock, Telegram summary захвачен без отправки.
Завершено: старые server/smoke EXE и workshop-agent.exe~ удалены.
Clean build `go build -o bin/workshop-agent.exe ./cmd/workshop-agent` — exit 0.
В bin/ только workshop-agent.exe, 23 113 728 bytes, версия 0.18.2.
Готовый EXE: --version, mcp-status, day17-mcp-report, day18-background-report — exit 0.
mcp-status: 7 tools, in-memory, 0 WB write / 1 local mutation; обе сессии закрыты.
Финальный `go test ./...` — exit 0 (Telegram 55.647s, background 4.496s,
mcpclient 0.482s, mcpmanager 0.670s, wbmcp 0.761s, остальные passed/cached).
`git diff --check` — exit 0. .env/рабочая БД не читались и не изменялись.
Обновлены cmd bootstrap/version; mcpclient/manager/fixture; background layers;
storage migration114; agent/Telegram trace/tests; offline reports; L0–L3 и usage.
Рефакторинг завершён. Следующий день не начинать без нового задания.
Отдельная live-проверка WB/Telegram владельцем остаётся за пределами offline-приёмки.

Нижеследующее — историческое состояние до рефакторинга; прежние указания
про две сборки и запущенные EXE заменены текущей записью.

## Day18 — завершён 2026-09-24

Основной документ: [Day18](../day18-background-jobs.md), WA-D151–153.
Добавлены migration113, persistent WB_DAILY_SYNC, Prices read MCP tool,
snapshots/current/diff, schedule timezone, Telegram menu и mock report.
Финальный go test ./... — exit 0 (background 6.328s, Telegram 66.940s),
обе сборки и git diff --check — exit 0. Отчёт:
`reports/day18-background-jobs/20260924T132053.913479400Z/report.html` — Passed=true,
2 runs, 8 snapshots, 2 diffs; actual STDIO, mock WB/Telegram, один catch-up.
Рабочие app/server запущены: не перезаписывать
их EXE. Сборки в bin/workshop-agent-check.exe и bin/check/wb-mcp-server-day16.exe.
Реальный env/DB/WB/Telegram не использовались. Доказательства и установка — Day18.
Остановиться; Day19 не начинать без нового задания.

## Day 17 — завершён, 2026-09-24

Продолжение: [Day17](../day17-first-mcp-tool.md), решения WA-D149–150.
Реализован явный `/wb_stocks [trace]` через прежний MCP SDK/STDIO.
Полный `go test ./...` — exit 0 (Telegram 52.256s). Проверены revoke/scope,
STDIO call/result, tokenless error vs connection error, cleanup, Telegram mock.
Сборки check app и прежнего MCP server — exit 0. App mock report:
`reports/day17-first-mcp-tool/20260924T064145.092958100Z/report.html` — passed=true,
12/4/0 шт., без реального WB. Повтор Day16:
`reports/day16-mcp/20260924T064209.169276700Z/report.html` — passed, 5 tools, write=0.
Остановиться на Day17. Day18/LLM auto selection/write не реализовывать.
Основной `bin/workshop-agent.exe` обновлён из check после проверки, что не работает;
запуск бота не выполнялся. SHA256: `4E1711F11AD2B92FC84D12950E837211E1A0046A7FDB78A0BCA95C3E91DC197B`.
Реальный `.env` не читать; рабочие данные/бот не запускать для demo.

Обновлено: 2026-09-23. Правила продолжения — [карта](README.md).

## Снятие искусственного ожидания — завершено (WA-D148)

По явному запросу пользователя неизвестный профиль больше не добавляет 24ч/30мин.
Migration 112 однократно снимает local_interval, сохраняя явно помеченные WB сроки.
Исправлена потеря server source при объединении ожиданий. В старой записи исходный
заголовок восстановить нельзя — это ограничение зафиксировано в WA-D148.
Тесты storage/marketplace/client/MCP прошли; дополнительный тест provenance прошёл.
Собран bin/workshop-agent-check.exe и скопирован в bin/workshop-agent.exe после
проверки, что основной exe не запущен (работал отдельный wb-views, он не тронут).
SHA256 обоих файлов совпал. Код бота не запускался, .env/рабочая БД не открывались.
Следующий шаг владельца: остановить старый бот, запустить основной exe и выполнить
один /wb sync orders. Если WB вернёт 429, код остановится и сохранит новый срок.

## Уточнение экрана заказов — завершено

По скриншоту пользователя исправлен presentation.go: заголовки разделов,
фильтрация sync-состояний по выбранному разделу (check остаётся общим),
отдельные сообщения для пустых заказов/карточек/остатков. Заказы показывают
отсутствие успешной загрузки и `/wb sync orders`; автоматическая загрузка заказов
не добавлялась. Квоты и сохранённый cooldown не изменялись.
`go test ./internal/marketplace ./internal/telegram` — exit 0.
`go build -o bin/workshop-agent-wb-views.exe ./cmd/workshop-agent` — exit 0.
Работающий exe не заменён; реальных WB/Telegram вызовов не было.

## Текущий результат — исправление identity/cooldown завершено

Новое задание из WB_STOCKS_FIX_PROMPT реализовано; приняты WA-D145–147.
Изменены Service/Client/presentation, Telegram publicError, server config/main,
.env.example; добавлены cooldown.go/test в marketplace и wildberries; migration 111
в storage с учётом backup. Токен/отпечаток не сохраняется. Профиль квоты
WB_API_PROFILE задаёт владелец; неизвестный профиль консервативный.
Полные tests и отдельная сборка прошли; доказательства в L3, использование в USAGE.
Артефакт: bin/workshop-agent-wb-cooldown.exe. Рабочие бот/БД/.env не запускались
и не читались. D1/V1 остаются открыты; источники и границы — API-CONTRACTS.
Предыдущие записи ниже — история, а не указание повторять завершённые этапы.
После сжатия перечитать карту/L0–L3/USAGE/этот файл и сверить рабочее дерево.

## Уточнение после Day 16 — автоматическое обновление остатков

Новый запрос пользователя реализован по WA-D144: Telegram-кнопка «Остатки WB»
и `/wb stocks` запускают wb_stocks и отправляют результат после завершения.
Переход по страницам не перезапускает загрузку; повторный запрос блокирует ErrBusy.
Перед отправкой результата повторно проверяются scope, права и включение кабинета.
Изменены internal/telegram/marketplace.go, marketplace_test.go и bot.go (wait group).
Тесты TestWB прошли на временной БД и fake API, включая кнопку/команду, сбой,
смену мастерской, отключение, пагинацию и повторный запрос. `go test ./...` — exit 0.
Сборка `go build -o bin/workshop-agent-wb-autostocks.exe ./cmd/workshop-agent` —
exit 0; новый exe не запускался, работающий не заменялся. Рабочая БД/.env не открывались,
реальные WB/Telegram запросы не выполнялись. Day 16 MCP не расширялся.

## Day 16 — завершён 2026-09-23

По новому явному запросу добавлен собственный read-only MCP поверх существующего
WB клиента. Полное задание, WA-D141–143, mapping tools, запуск и доказательства:
[docs/day16-mcp.md](../day16-mcp.md). Прежний запрет запуска MCP уточнён только
для локального tokenless STDIO smoke; WB/Telegram/рабочая БД не запускались.

Добавлены cmd/wb-mcp-server, cmd/mcp-wb-smoke, internal/integrations/wbmcp,
internal/integrations/mcpclient и server-only config wb_mcp.go с тестами.
Официальный MCP Go SDK v1.7.0 закреплён в go.mod/go.sum; Go минимум 1.25.0.
go test ./... и обе отдельные сборки прошли. Реальный STDIO smoke получил пять
tools, ноль write tools; session закрыта, child process завершён, exit 0.
Отчёт: reports/day16-mcp/20260923T111346.093190300Z/report.html.
Новых миграций нет; предыдущие незакоммиченные изменения WB сохранены.

Текущая задача завершена: остановиться. LLM tool calling не реализовывать
автоматически. Ниже сохранены результаты предыдущего этапа; D1/V1 остаются
открытыми, но не являются продолжением Day 16 без нового задания.

## Текущий результат

Пользователь явно поручил полную реализацию. Добавлены сервис Marketplace,
read-only WB adapter, migration 110, права, /wb, deterministic Agent reads,
mock-тесты и руководство. Локальные тесты и отдельная сборка завершены успешно.
Это не live-приёмка и не закрытие полной актуальной сверки спецификации.

Нормативные документы: [L0](L0-scope.md), [L1](L1-architecture-data.md),
[L2](L2-contracts-decisions.md); доказательства — [L3](L3-implementation-acceptance.md).
Существующие WA-D127–140 сохранены, уточнения записаны в L2; ID не перенумерованы.

## Изменённые файлы

- internal/marketplace: service.go, sync.go, presentation.go, service_test.go.
- internal/marketplace/wildberries: client.go, client_test.go.
- internal/storage/marketplace.go и sqlite.go: migration 110 и backup.
- internal/auth/service.go: marketplace.read/manage (manage только OWNER).
- internal/telegram/marketplace.go, marketplace_test.go, bot.go, identity.go.
- internal/agent/marketplace.go, agent.go, memory.go.
- cmd/workshop-agent/main.go, .env.example, README.md, docs/marketplace/*.

До реализации уже были изменены README.md и неотслеживаемые docs/marketplace/.
Они дополнены без удаления прежних решений. Git commit не создавался.

## Проверки

- Прочитаны слои, auth/storage/Telegram/Agent/startup; upstream client и аудит.
- git rev-parse подтвердил audited b0e208318d5099a7d429d7e4bcf745352ca20771.
- go test ./...: exit 0; все пакеты, включая новые 20 функций тестов.
- go build -o bin/workshop-agent-wb-20260922-check.exe ./cmd/workshop-agent:
  exit 0. Отдельный бинарник не запускался; SHA256 и точные команды — в L3.
- .env исключён из Git; только пустой WB_API_TOKEN добавлен в .env.example.
- Настоящие env/DB/бот не запускались и не изменялись. Только временные БД,
  синтетические маркеры, fake HTTP/Telegram. Docker/MCP/HTTP server не запускались.
- Первые ошибки тестов были в fixtures (обязательный product_type), исправлены.
- Go требует доступ за пределами sandbox к стандартной библиотеке/кешу;
  выполнены разрешённые эскалированные test/build, не запуск приложения.

## Открыто и следующий шаг

**D1 не закрыт:** публичные страницы и YAML WB возвращают HTTP 498, в том числе
при прямой загрузке вне sandbox. Схемы выбранных методов/квоты проверены по
доступным разделам официального поискового индекса (пометка около пяти месяцев).
Это не доказательство актуальности полного контракта сегодня. Не скрывать эту
оговорку и не объявлять live-готовность. См. [API-CONTRACTS](API-CONTRACTS.md).

Когда официальная документация доступна: завершить D1, сравнить response schemas,
категории/тип токена/квоты и семантику отсутствующих нулевых остатков. При расхождении
исправить adapter и относящиеся тесты; повторить tests/build только после изменений.

**V1 не выполнялся:** реальные WB/Telegram проверки требуют отдельного разрешения.
Токен локально вводит владелец; инструкция [USAGE](USAGE.md). Не запускать бинарник
автоматически при продолжении. Не открывать .env или рабочую БД.

Известные границы: одно приложение/кабинет, без распределённого rate limiter;
ручные ограниченные снимки (20 min/50000 rows), без resume cursor и исторической
массовой выгрузки. Check проверяет identity, доступ отдельных категорий виден
по соответствующей загрузке. Неполные данные не заменяют старый снимок.

После сжатия перечитать README/L0–L3/этот файл и ../day16-mcp.md, проверить git
status. Day 16 завершён; дальнейшая работа — только по новому заданию.

Deployment update 2026-09-25 16:35 MSK: user requested EXE update. No running workshop-agent process found. Built canonical bin/workshop-agent.exe successfully; --version returned WorkshopAgent v0.19.1. Bot not started; production migration remains pending next normal startup. This supersedes the earlier binary-not-updated note.

Deployment update 2026-09-27: user requested EXE update. No running workshop-agent process found. Canonical bin/workshop-agent.exe built successfully; --version returned WorkshopAgent v0.19.4. Bot not started. This supersedes the earlier deployment-pending note.
