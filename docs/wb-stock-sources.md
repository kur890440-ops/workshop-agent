# Остатки Wildberries: независимые источники

@PROJECT:WORKSHOP_AGENT @NO_COMPRESS @PRESERVE

## WA-D162 — источники и частичная загрузка

2026-09-25. Явное новое задание заменяет strict stop-on-read-error из WA-D161,
общий stocks view/автообновление WA-D144 и discard partial stock policy WA-D140.
Остальные требования авторизации, одного процесса, in-memory MCP, limiter,
отсутствия WB write и сохранности производственных данных остаются в силе.

### L0: границы

INTERNAL != SELLER != WB. Никакого автоматического суммирования или перезаписи
одного источника другим. Новых endpoints/write-функций, EXE, процессов, MCP client,
server или scheduler нет. Проверки только mock и temporary SQLite. Токен не менялся,
настоящий .env не читался, реальный API не вызывался.

### Аудит до изменений

- SELLER: Client.SellerStocks, GET marketplace-api.wildberries.ru/api/v3/warehouses,
  затем POST /api/v3/stocks/{warehouseId}, chrtIds по вариантам каталога.
- WB: Client.WBStocks, POST seller-analytics-api.wildberries.ru/api/analytics/v1/stocks-report/wb-warehouses.
- MCP имел wb_get_wb_stocks; отдельного seller tool не было.
- marketplace_stocks уже содержал обязательный kind и ключ с kind; смешение было
  в отображении/поведении команды. Background имел source_tool, но CHECK только
  prices/WB, а strict pipeline блокировал сохранение любого независимого источника.
- SellerStocks останавливался при неверной записи/отсутствующем requested chrtId.
  finish записывал count34/statepartial, но вызывал save только при runErr=nil.
  Это подтверждённый механизм потери валидной части. Конкретная ветка и запись
  реального ответа 15:24:47 не восстановимы: raw response тогда не сохранялся.

### L1: модели и хранение

StockSource: INTERNAL/SELLER/WB. StockBatch: Info + Rows + bounded Rejections.
StockInfo: source/status/received/valid/invalid/missing/saved, timestamps/duration,
endpoint/method/tool/caller/rate_key, HTTP status, safe error, retry deadline,
token type/required capability. Rejection: index/external_id/field/expected/actual
JSON type или безопасная причина. Значение произвольного поля/секрета не пишется.

Migration117:
- marketplace_stock_runs — отдельный fetch/run на source, workshop, connection;
  optional logical job_run_id, status, capture timestamp, info_json, rejections_json.
- marketplace_stock_snapshots — валидные normalized rows по run_id; источник
  однозначен через обязательный FK к source-specific run.
- marketplace_stocks остаётся current state с прежним ключом connection/kind/
  warehouse/chrtID и workshop ownership. Только upsert полученных валидных строк.
- wb_daily_snapshots/current: CHECK source_tool расширен на wb_get_seller_stocks,
  остальные строки и ключи переносятся без изменения. История Day18 и diffs остаются.
  Эти таблицы сохраняют аудит состава фонового run; общий stock current и fetch
  history используются и ручным путём, и background через SaveStockBatch.
- Старый current переносится в LEGACY source-specific snapshots по известному kind;
  старые данные не объявляются полным successful snapshot. Неопределённый источник
  не угадывается (старый CHECK уже гарантировал seller_stocks/wb_stocks).

Production migration НЕ запускалась агентом. Применится при обычном запуске новой
версии с существующим механизмом backup. Протестирована на временных БД, включая
повторный запуск миграции, сохранение старых строк и реальное повторное открытие БД.

### L2: контракты

| Source | MCP | Go method | API / category | Current BASE |
|---|---|---|---|---|
| SELLER | wb_get_seller_stocks | SellerStockBatch (SellerStocks compatibility wrapper) | GET /api/v3/warehouses + POST /api/v3/stocks/{warehouseId}; Marketplace | YES: ранее фактически HTTP200 |
| WB | wb_get_wb_stocks | WBStocks | POST /api/analytics/v1/stocks-report/wb-warehouses; Analytics, PERSONAL/SERVICE | NO: BASE не указан разрешённым, ранее HTTP403 |

Сверка официальных материалов25.09.2026:
- https://dev.wildberries.ru/openapi/work-with-products : seller stocks/warehouses300/min,
 200ms,burst20; отдельного исключения BASE для этого read-метода в доступном разделе нет.
- https://dev.wildberries.ru/release-notes?id=272 : WB warehouses только PERSONAL/SERVICE + Analytics.
- https://dev.wildberries.ru/openapi/analytics :3/min,20s,burst1; offset pagination.
- https://dev.wildberries.ru/docs/openapi/api-information : маска s, категории и read-only bit30.
Прямое открытие страниц вернуло498. Использованы индексированные официальные страницы
(индекс показывает давность около5месяцев); это ограничение актуальности явно сохраняется.
BASE READ ONLY и категории текущей конфигурации — данные пользователя; live-доступ
подтверждён/отклонён ранее сохранёнными результатами25.09.2026, не новым probe.

Общий limiter сохранён: SELLER — marketplace, WB — analytics; существующая
консервативная marketplace пауза400ms не сокращалась. 403 не повторяется автоматически,
429 сохраняет deadline; новые retries поверх WB client не добавлены.

Seller parsing:
- Каждая JSON row валидируется отдельно; неверные ID/type/amount/duplicates отклоняются.
- Продолжаются следующие корректные строки и склады; Missing — отдельный счётчик.
- Валидная часть предыдущих ответов сохраняется и при последующей ошибке.
- Diagnostics ограничены50 деталями, counts полные. Нет raw dumps.
- SUCCESS и PARTIAL обновляют только явные валидные строки. Отсутствующие не удаляются
  и не превращаются в quantity0. Ошибка без данных не меняет current.
- PARTIAL не обновляет last_success; timestamps каждого source независимы.
- ZERO — явное quantity0 в валидной строке. Product-level zero/low SELLER вычисляется
  только при полном source: частичная сумма неизвестных складов не считается нулём.

Pipeline: prices → SELLER → WB → build → save, sequential через ту же MCP session.
Первые3 источника независимы; source error не пропускает другой источник. Нарушение
scope/lease, cancellation и ошибки обязательных compute/save останавливают цепочку.
Полезные источники + ошибка другого = PARTIAL_SUCCESS. Все источники недоступны =FAILED.
Aggregate хранит seller_stock и wb_stock availability/status отдельно; seller
zero/low/changes отдельно от WB. Fetch metadata в aggregate описывает получение;
фактически сохранённые counts — SaveResult и marketplace_stock_runs.info_json.
Save атомарен для текущего набора валидных данных/диагностики, без HTTP внутри tx.
Day18 summary читает последний success/partial_success с явным source status, без fetch.

MCP server tool не вызывает другие MCP tools. Для SELLER напрямую используется
существующий API Catalog как источник ID (Content), затем SellerStockBatch (Marketplace).
Content dependency тоже соблюдает прежний limiter. Ручная production stock sync
подключена к этому же MCP client через SetStockReader, отдельного клиента нет.

### Telegram и диагностика

- /wb stocks — сохранённые SELLER и WB отдельными секциями, без HTTP.
- /wb seller_stocks [offset] — только SELLER; /wb wb_stocks [offset] — только WB.
- /wb sync seller_stocks или /wb sync wb_stocks — явное обновление нужного источника.
- /wb sync all — прежнее обновление всех типов, source outcomes независимы.
- /wb debug — локально decoded token type/read-only/categories, cached capability;
  это не проверка подписи и не предоставление прав. Никаких capability probes.
- /wb_auto run — prices/SELLER/WB pipeline; default08:00 не изменён.
- /wb_auto trace — source-specific step results и safe counts.

Статусы: SUCCESS, PARTIAL, FAILED, PERMISSION_DENIED, RATE_LIMITED. 403 не называется
баном. UI показывает last attempt/full success/snapshot по source и timestamp
каждой строки; старые данные при недоступном источнике отмечены явно.

### L3: проверка

Новые тесты:
- TestSellerRecordValidation34Received33Valid (HTTP mock, bad amount string);
- TestSellerMissingIsNotZeroAndContinuesNextWarehouse;
- TestPartialStockPersistenceProvenanceAndRestart (33 saved, missing unchanged,
  same product/warehouse SELLER10 != WB77, reopen SQLite);
- TestSellerPartialAndWB403ThroughRealMCP (33 saved +403, PARTIAL_SUCCESS);
- TestStockSourcesMigrationPreservesLegacyAndAddsSeller (history + idempotency);
- TestWBStocksViewsDoNotFetchAndSeparateSources.
Старые rate-limit, authorization/revocation, cancellation, transaction rollback,
production isolation и no-child-process tests сохранены. Старые strict/auto-fetch
ожидания обновлены под явно новое задание, а не объявлены прежним поведением.

Offline command: go run ./cmd/workshop-agent wb-stock-sources-report.
Report: reports/wb-stock-sources/20260925T130700.137810100Z/report.html (первый успешный прогон).
33 Seller records сохранены, WB403,3 старые WB rows остались, pipelinePARTIAL_SUCCESS.
Финальные test/build результаты и актуальный report фиксируются в WORKSTATE.
