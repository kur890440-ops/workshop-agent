# Ozon Stage A — WA-D167

@PROJECT:WORKSHOP_AGENT @L3:OZON_STAGE_A @NO_COMPRESS @PRESERVE

Продолжение WA-D166 по заданию e0d91aa3-109a-4d49-bf3e-9a655e04fed3.
Первоначальные ограничения и история: [WA-D166/167](ozon-stage-a.md).
Оперативное состояние: [WORKSTATE](marketplace/WORKSTATE.md).

## Реализация

- `internal/marketplace/ozon/request.go`: единственный HTTP boundary для четырёх
  read endpoints, secret-only headers, fixed HTTPS/TLS, no redirects/proxy,
  timeout30s, body4MiB, max2 attempts network/5xx; 401/403 без повтора;
  429 без автоматического повтора, Retry-After seconds/date. Fallback60s —
  локальная консервативная политика, не заявленная квота Ozon. Пока cooldown
  общий для настроенного Ozon connection, независимый от WB.
- `models.go`, `fetch.go`: typed minimum response, построчная валидация,
  diagnostics без сырого payload. `product_id`, `offer_id`, `sku` не подменяют друг друга.
- `service.go`, `repository.go`: существующие auth permissions, active workshop,
  provider+connection+revision, SQLite, audit, singleflight всей refresh sequence.
- `internal/integrations/ozonmcp/server.go`: READ module на существующем server;
  `mcpclient/ozon.go`: настоящий SDK CallTool; `mcpmanager` выдаёт одноразовые
  случайные grants с TTL, user/workshop не являются tool arguments.
- `internal/telegram/ozon.go`: cached views, explicit async refresh, scoped buttons,
  owner-only диагностика50 events. Поздний ответ перепроверяет права/revision.
- `cmd/workshop-agent/main.go`: server-side ENV initialization и регистрация модуля
  до MCP initialize. Startup/ListTools не вызывают Ozon API.

Tools: `ozon_list_products`, `ozon_get_seller_stocks`, `ozon_get_ozon_stocks`.
Input: `refresh` (default false), `offset` (страница локального результата).
Это локальные параметры чтения кэша/явной загрузки; credentials, URL, user/workshop
и произвольные HTTP параметры не принимаются. Ozon write tools =0.
Отдельный details tool не нужен: batch enrichment выполняется внутри refresh каталога.
LLM executor не получает новый dispatcher/allowlist и не выбирает Ozon tools.

## Данные / миграция118

`marketplace_connections` расширена в существующем storage. WB остаётся id1,
provider=wildberries. Ozon=id2, provider=OZON, secret_ref=OZON_ENV. В Stage A
поддерживается одна локально настроенная пара Ozon credentials, привязанная
владельцем к одной мастерской. Отключение не освобождает привязку для другой мастерской.
Хеш Client-Id в существующем identity_fingerprint фиксирует кабинет; API key
не сохраняется/не хешируется в БД. Смена Client-Id требует отдельной процедуры,
скрытое переиспользование старого каталога другого кабинета запрещено.

Новые normalized tables (старые WB nmID/chrtID таблицы не подходят):

- `marketplace_catalog`: product_id, offer_id, name, timestamps.
- `marketplace_catalog_skus`: отдельные SKU → product_id, несколько SKU на товар.
- `marketplace_source_runs`: provider/source/status/metrics/diagnostics/last success.
- `marketplace_stock_current`: connection/source/SKU/warehouse, явное quantity,
  product_id/offer_id отдельно, captured_at/run_id.
- `marketplace_stock_history`: append-only строки каждого run, отдельные от current.
- `integration_cooldowns`: scoped retry_not_before; сохраняется после restart.
- `integration_request_trace`: scoped безопасная история HTTP/CACHE/SYNC/DEDUP.

Миграция сохраняет WB parent IDs и child FKs; штатный backup, transactional rebuild,
foreign_key_check до commit, восстановление FK enforcement. Проверена только на
временных БД. Рабочая БД при разработке не открывалась; применится при запуске
собранной новой версии. WB service/background selection теперь явно фильтруют provider.

Секреты только ENV/.env процесса. `.env` — открытый текст: ограничить права доступа.
В MCP payload, SQLite, audit, request trace, историю Telegram/LLM секреты не передаются.
Введённый известный секрет или имя переменной отвергается до сохранения сообщения.

## Каталог и остатки

Каталог: limit100, last_id, total, максимум100 pages, защита повторного cursor и ID.
Данные нескольких страниц объединяются; не достигнут total — PARTIAL.
Details: только explicit catalog refresh, batches<=100, локальное обогащение UI.
100 товаров в acceptance fixture: 2 pages +1 details batch =3 HTTP requests.
100 details IDs в request-layer test:1 HTTP request. UI page15, SQL bulk join.

Seller: POST `/v2/product/info/stocks-by-warehouse/fbs`, limit100, cursor/has_next,
source `OZON_SELLER_STOCK`, quantity=`free_stock` (доступно к продаже).
Не подставлять present/reserved или вычисляемую разницу при отсутствии free_stock.

FBO: POST `/v1/analytics/stocks`, batches<=100 SKU из локального каталога,
source `OZON_FBO_AVAILABLE`, quantity=`available_stock_count`, warehouse/SKU key.
Это аналитика доступного остатка по выбранным SKU, не сумма всех категорий запасов
и не гарантированно мгновенный баланс. Без catalog SKU — UNAVAILABLE,
CATALOG_SKUS_REQUIRED, без скрытого Product API refresh.

ZERO только из явного числа0. MISSING/STALE — сохранённое прежнее значение,
которое не подтверждено последним run; UI так и подписывает. Partial обновляет
валидные строки и не удаляет остальные. Failed/403/429 сохраняют прежние данные.
Даже полная выборка не обнуляет отсутствующие позиции. Источники не суммируются;
production inventory/заказы/производство не изменяются. Snapshot history не очищается.

## Контракты: происхождение и границы доказательства

2026-09-27: официальный `https://docs.ozon.ru/api/seller/` остаётся недоступен
через использованный web-доступ. Это не блокирует MCP/storage/UI по новому заданию.
Request paths/shapes сверены с audited Python b95da59689cdacb544c659c5c0c1fcbecc50a995.
Дополнительные response reference artifacts:

- [OzonFromGAS types](https://github.com/googlesheets-ru/OzonFromGAS/blob/master/Ozon/types.ts):
  seller v2 products/free_stock/sku/warehouse_id/cursor/has_next.
- [Опубликованные response examples](https://github.com/neteraf0/ru-marketplaces-api-docs/tree/main/ozon/_shared/examples):
  `POST__v3_product_info_list_200.json`, `POST__v1_analytics_stocks_200.json`,
  `POST__v2_product_info_stocks_by_warehouse_fbs_200.json`.
- [Catalog reference implementation](https://pkg.go.dev/github.com/diphantxm/ozon-api-client/ozon):
  v3 result/items/product_id/offer_id/total/last_id.
- [Официальные уведомления Ozon](https://t.me/s/OzonSellerAPI/592): версия seller v2.

Новый Go-код написан самостоятельно, Python runtime/code не переносился.
Примеры тестов синтетические, поля основаны на перечисленных reference models.
Эти материалы **не заменяют актуальную официальную OpenAPI и реальную проверку
прав кабинета**. Reference-backed catalog/FBS/FBO parsing реализован, неизвестные
поля игнорируются, обязательные используемые поля проверяются. Неизвестные
типы/отсутствующие коллекции/quantity дают diagnostics, а не ложный полный успех.
Остаётся отдельная проверка официальной семантики, текущих лимитов/прав и live API.

## Настройка и использование

Локально заполнить `OZON_CLIENT_ID` и `OZON_API_KEY` в корневом `.env` и перезапустить
приложение. Значения через Telegram не вводить. Текущий EXE этой задачей не заменяется.

1. Выбрать мастерскую, владельцу отправить `/ozon attach`.
2. `/ozon refresh catalog` — обновить каталог и имена/SKU.
3. `/ozon products` либо `/ozon products 15` — читать страницы кэша.
4. `/ozon refresh seller` и `/ozon refresh fbo` — обновлять источники независимо.
5. `/ozon seller_stocks`, `/ozon fbo_stocks` — читать кэш, Product API calls=0.
6. `/ozon debug` — owner-only ListTools, последние50 safe events/cooldown.
7. `/ozon disable` — запретить дальнейшие запросы, сохранив секрет/данные.

Кнопки доступны из Ozon меню основной мастерской. `/ozon` ничего не обновляет.
Refresh async до3мин, shutdown context отменяет операции и дожидается завершения.

## Trace и проверки

HTTP event: caller/tool/method/endpoint/attempt/status/duration/page, received/valid
на границе ответа; saved=0, поскольку транзакция ещё не выполнялась. SYNC event:
фактически committed received/valid/saved и status. CACHE/DEDUP events: HTTP count0.
По stage различаются HTTP_SUCCESS и полный/частичный результат сохранения.
`last_sync_metrics` не выдаются за дополнительные HTTP текущего cached view.
Cache HIT/MISS/BYPASS и dedup NEW/JOINED_EXISTING независимы.

Проверки: реальные SDK ListTools/CallTool, local TLS mock, temporary SQLite,
restart/cooldown, batch100, pagination loop, source separation, zero/missing,
partial validity, revoked-before-save, stale callbacks, secret guard, forged grants,
права мастерской, migration FK preservation, Telegram nonblocking refresh.
Итог: [автономный отчёт](../reports/ozon-stage-a/20260927T204000Z/report.html).
Статус до официальной/live сверки response contracts: **PARTIALLY ACCEPTED**.
Не добавлялись Ozon scheduler/pipeline/orders/Performance/write tools.

????????? offline ???????? 2026-09-27: `go test ./...` exit0 (19 packages,
377 successful test events), `go build -o NUL ./cmd/workshop-agent` exit0,
`git diff --check` exit0. ??????????? ??????????/trace/UI ? ? report/evidence.json.
