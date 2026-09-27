# Минимальный Ozon V1: design input, не реализация

**OZON_V1_READ_TOOLS: 6 возможностей. WRITE TOOLS = 0.** Все ниже — POST read по семантике, host `https://api-seller.ozon.ru`. Credentials: отдельные Seller Client-Id + Api-Key, минимальные категории прав. Performance исключён.

Полная актуальная OpenAPI не получена из-за redirect loop официального сайта. Таблица фиксирует actual Python request и ожидаемую модель данных для проектирования, а не сертифицированный response contract. До Go implementation обязательны official schema/permissions/limits и sanitized fixtures; неизвестные пределы не заменять догадкой.

| Польза / будущий MCP tool | Endpoint | Python implementation | Input и pagination | Expected output / сложность / риск |
|---|---|---|---|---|
| Каталог / ozon_get_products | /v3/product/list | client.py:593 product_list; source tool ozon_product_list | visibility, limit(default100), last_id; ограниченный обход страниц | Список product_id/offer_id, continuation; MEDIUM / LOW |
| Детали для явного mapping / ozon_get_product_info | /v3/product/info/list | client.py:603 product_info_list; ozon_product_info | product_id[]; bounded batches | ID, seller offer, название, доступные SKU/атрибуты; dict response необходимо типизировать; MEDIUM / LOW |
| Цены только читать / ozon_get_prices | /v5/product/info/prices | client.py:202 product_info_prices; ozon_get_prices | filter.visibility=ALL, offer_id[]/product_id[] (строки), limit(default100), cursor | product_id/offer_id, price object, индексы, currency при наличии; MEDIUM / MEDIUM (коммерческие данные) |
| Остатки продавца / ozon_get_seller_stocks | /v2/product/info/stocks-by-warehouse/fbs | client.py:582 product_stocks_by_warehouse; ozon_product_stocks_by_warehouse | sku[] строками, limit(default100), cursor | Отдельные строки SKU+warehouse, доступные поля free/present/reserved после сверки; MEDIUM / MEDIUM |
| Остатки у Ozon / ozon_get_ozon_stocks | /v1/analytics/stocks | client.py:518 analytics_stocks; ozon_analytics_stocks | skus[] строками; source comment 1–100 (official limit ещё сверить), bounded batches | Ozon warehouse/cluster stock analytics, availability/liquidity; не смешивать с FBS; HIGH / MEDIUM из-за модели и полноты |
| Новые/текущие отправления FBS / ozon_get_postings | /v4/posting/fbs/list | client.py:708 posting_fbs_list; ozon_orders_fbs | since/to, limit(default50), status, cursor; body with.analytics_data/financial_data=true в upstream | postings на верхнем уровне, has_next+cursor, posting_number/status/products; MEDIUM / MEDIUM (возможны персональные/финансовые поля) |

Источник версий: [официальные новости Ozon — FBS warehouse v2](https://t.me/s/ozonsellerapi?before=648), [официальные новости — FBS v4/FBO v3 и analytics stocks](https://t.me/s/OzonSellerAPI?before=685), проверено 2026-09-27. Они подтверждают направления миграции, но не заменяют полную schema. [Seller docs](https://docs.ozon.ru/api/seller/) недоступны из audit tool из-за redirects. Для postings Go запрашивает/сохраняет минимально необходимые поля; не копировать financial_data=true автоматически.

## Product identity

| ID | Смысл для будущей модели | Нельзя предполагать |
|---|---|---|
| internal product_id | ID нашего продукта | Не равен Ozon product_id |
| Ozon product_id | Ozon product reference: product_info_list, prices, stock filters | Не равен SKU или WB nmID |
| offer_id | Артикул/идентификатор продавца; есть отдельный update_offer_id endpoint | Не глобально уникален и не неизменяем; всегда connection scope |
| sku | Ozon SKU, используется warehouse stocks/analytics и posting products | Не наш product_id; строка/число зависит от контракта |
| fbo_sku/fbs_sku | Исторические/схемные SKU-поля, если действительно присутствуют в выбранной response version | В audited коде нет typed definitions этих полей; наличие и связь НЕ подтверждены текущей schema |
| warehouse_id | Склад маркетплейса/продавца внутри source namespace | Не ID склада мастерской |
| posting_number / order_id | Отправление / родительский заказ | Один заказ не обязательно одно отправление |

Предложение mapping: (workshop_id, connection_id, provider=OZON, internal_product_id, ozon_product_id, offer_id, sku, fulfillment/source, mapping_state). Создавать явно по ID, не по названию. Несколько SKU/offer/источников допускаются только с установленным контрактом. WB mapping остаётся отдельно; общий internal_product_id связывает системы.

## Stocks model — не повторять смешение WB

- **FBO/Ozon warehouse**: товар у Ozon. Выбран analytics/stocks как отдельный источник; turnover/stocks — отдельная аналитика оборачиваемости, не синоним физического available.
- **FBS seller warehouse**: товар на складе продавца, marketplace-учёт. Warehouse stock API v2 — отдельные SKU/warehouse rows. Это не current_stock мастерской.
- **realFBS/rFBS**: схема исполнения доставки продавцом/его перевозчиком. Upstream содержит rFBS return methods, но warehouse wrapper не типизирует fulfillment. Не создавать ложное поле scheme из названия tool; определить по официальной warehouse/stock schema.
- **/v4/product/info/stocks**: upstream называет FBO/FBS и передаёт last_id, но typed response нет. Не брать его «универсальным агрегатором» в V1 без проверки: Ozon менял описание метода; cursor/stock coverage нужно подтвердить. Даже при наличии stocks.fbo/fbs нельзя суммировать агрегат с warehouse rows.
- `present`, `reserved`, `available/free`, in-transit, defect и аналитическая liquidity — разные величины. Не выводить available=present-reserved без подтверждённой семантики конкретного endpoint. Не складывать FBO, FBS и мастерскую в единственную доступность.
- Unique snapshot key: connection + source + warehouse/cluster + SKU + stock kind. Указывать source_updated_at (если дан) и fetched_at, completed_at, requested/received/missing counts.
- Empty/missing/invalid/forbidden/partial — разные состояния; отсутствующая в неполной выборке позиция не становится нулём. Сохранять прежний snapshot с возрастом, не объявлять полное обновление.

Это design model; точные quantity response fields и rFBS coverage остаются обязательной проверкой до внедрения. Upstream generic dict не предоставляет достаточного доказательства.

## Prices model

В client.py:182–194 явно перечислены `price`, `old_price`, `min_price`, `auto_action_enabled`, `min_price_for_auto_actions_enabled` для записи; V1 эту запись не переносит. Read v5 возвращает generic dict; shaping.py сохраняет price object, product_id, offer_id, price_indexes, acquiring, volume_weight.

Разделять текущую marketplace price, зачёркнутую old price, минимальную seller price, акционные/marketing values и Premium-цену **если поля присутствуют в текущем контракте**. Не вычислять маркетинговую скидку из случайной пары полей. `marketing_price`, `marketing_seller_price`, `premium_price` не определены typed моделями этого репозитория; нельзя обещать их наличие в v5. Старое описание ozon_get_prices_v4 обещает purchase_price, но implementation делегирует v5 — не основание считать его себестоимостью цеха.

В Go деньги — decimal/minor units с currency и видом цены, никогда float для денежных расчётов. Цены Ozon не изменяют internal cost. Фиксировать состояние отсутствующего поля отдельно от 0.

## Orders / postings

FBS list v4 использует cursor/has_next и top-level postings, FBO list v3 (`client.py:693`) — свой request contract с offset. FBS get v3 и FBO get v2 — отдельные детали. Нельзя парсить v4 как старый result.postings и объявлять отсутствие заказов. Сохранять внешний posting_number, status, products/quantity, связи с order_id если доступны. Unknown status сохранять безопасно, не превращать в local FSM transition. Импорт не создаёт production tasks, не списывает материалы и не оформляет отгрузку.

## Будущий Go layout — только рекомендация

Сверено с `internal/integrations/mcpclient/call.go`, `mcpmanager/manager.go`, `wbmcp/server.go`, `internal/marketplace/service.go`, `internal/background/registry.go` и marketplace L1. Сейчас marketplace.API/Connection связаны с WB; готового универсального provider abstraction не предполагаем.

```text
workshop-agent.exe (ONE process)
  existing MCP Client -> existing In-Memory MCP -> internal/integrations/ozonmcp
                                                 -> internal/marketplace/ozon
                                                 -> fixed Seller API host
  existing authorization -> marketplace application boundary
  existing BackgroundScheduler -> OZON_DAILY_SYNC executor -> same MCP path
  existing SQLite/audit/migrations -> scoped snapshots and sync state
```

Предлагаемые будущие файлы `internal/marketplace/ozon/{client,products,prices,stocks,postings,models,errors,ratelimit}.go`, `internal/integrations/ozonmcp/{server,tools,schemas}.go`. Их сейчас нет и аудит их не создаёт. Не добавлять internal/mcp framework параллельно существующим integrations.

Scope user_id/workshop_id/connection_id проверяется до каждой операции; отдельные read/manage permissions; connection provider OZON и secret reference не пересекаются с WB. Secret вводится локально владельцем; LLM/Telegram получают только configured/status. Выбор ENV/OS keystore и provider-aware миграция требуют отдельного решения, не маскировать plaintext соседним Fernet key. Primary binding подтверждается owner и полученной identity, если надёжный read identity endpoint доступен; неподдерживаемый upstream company_info не подходит.

OZON_DAILY_SYNC — будущий executor существующего scheduler, с bounded concurrency, per-connection lock, отменой, persisted checkpoints. Никакого OzonScheduler, Python process, web/stdio child в production или второго exe. HTTP только вне длительной SQLite transaction. Общая трассировка caller/tool/method/endpoint/attempt/status/retry deadline без headers/payload/secrets. Limiter account+API group должен переживать restart; semantics retry только для проверенных read endpoints.
