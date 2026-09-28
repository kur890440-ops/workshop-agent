# WA-D166 — Ozon Stage A

## WA-D167 — продолжение Stage A, 2026-09-27

Явное задание e0d91aa3-109a-4d49-bf3e-9a655e04fed3 заменяет остановку всей
реализации из WA-D166 из-за недоступности official schemas. Реализовать MCP,
provider-aware connections, SQLite cache/current/history/cooldown и Telegram.
Минимальные декодеры подтверждать public reference models; provenance и разницу
между reference-confirmed и official/live verification сохранять в отчёте.
Неизвестные/отсутствующие quantity не превращать в нули.

Миграция118 расширяет marketplace_connections с сохранением WB id1/FKs;
Ozon id2 привязывается owner явно. Один настроенный Ozon кабинет, без скрытой
перепривязки. Отдельные provider-aware normalized catalog/run/current/snapshot
таблицы нужны из-за WB-specific nmID/chrtID в старых таблицах.
Миграция выполняется при startup с backup и foreign_key_check до commit;
рабочую DB в разработке не открывать. Secrets только server-side ENV startup.
MCP модуль регистрируется в том же server до initialize, authority через one-use
grant в meta, scope не в tool arguments. Cache reads также проходят CallTool.
Refresh явный; полная последовательность singleflight по connection/source,
права и revision проверяются до HTTP и перед atomic save. Telegram refresh async.

Текущий шаг: migration/storage/service + минимальные decoders; затем MCP/UI/tests.

@PROJECT:WORKSHOP_AGENT @L3:OZON_STAGE_A @NO_COMPRESS @PRESERVE

Статус: IN PROGRESS, не production acceptance.
Задание: attachment a5377a21-00ed-447c-8ce4-64ca18d10148/pasted-text.txt.
Reference: DeviceIngineering/ozon-mcp-server, MIT,
b95da59689cdacb544c659c5c0c1fcbecc50a995; [аудит](../security-audit/ozon-mcp/README.md).

## L0 — границы

Один workshop-agent.exe, один процесс, существующий in-memory MCP и scheduler.
Go Seller client + внутренний Ozon модуль, только каталог и два независимых
источника остатков. WRITE TOOLS=0. Нет Python/STDIO/SSE/HTTP MCP, Ozon jobs,
pipelines, Performance, orders, price writes, LLM automatic selection.
Реальные .env/секреты/production DB/API/Telegram при разработке не использовать.
Тестировать через временные SQLite, fake HTTP и настоящий in-memory MCP.

## L1 — фактическая архитектура и размещение

MCP Manager создаёт wbmcp server, регистрирует другие модули, затем единственную
mcpclient.Service через SDK NewInMemoryTransports. Новый Ozon module должен
регистрироваться до initialize/ListTools на том же server/session.

marketplace_connections сейчас CHECK(id=1), CHECK(provider='wildberries'),
CHECK(secret_ref='WB_API_TOKEN'); прочие marketplace tables используют WB IDs.
Это не готовая универсальная модель. Нельзя вставить Ozon под WB connection или
хранить SKU в nm_id/chrt_id. Миграция provider-aware connection требует отдельной
проверки сохранения существующих foreign keys/данных; копировать таблицы WB
под другой prefix без нормальной identity/source модели нельзя.

Секреты проекта — локальный .env/ENV при startup, не vault. Для Ozon planned
OZON_CLIENT_ID/OZON_API_KEY, только server-side client, ссылка в connection.
Одно настроенное подключение привязывает owner к конкретной мастерской явно;
не выбирать первое из произвольного списка, не разрешать скрытую перепривязку.

## L2 — обязательные инварианты

- Application -> MCP Client -> existing in-memory transport -> Ozon handler -> Go client.
- Credentials не MCP arguments/meta/results, не job params, не LLM, не logs/SQLite.
- Проверять user/workshop/connection/provider/enabled/active workshop до вызова
  и перед сохранением. Повторные callbacks после смены мастерской не авторизуют.
- Product product_id/offer_id/sku раздельно, scoped. Persistent cache, explicit
  refresh; нет catalog refresh при startup/open/stock render.
- Details только batch при подтверждённой необходимости, не N+1.
- WORKSHOP INVENTORY != OZON_SELLER_STOCK != источник Ozon analytics;
  точное имя/quantity последнего определить только по подтверждённой schema.
- Current state отдельно от append-only snapshots; source/status/timestamp/run
  независимы. ZERO только явно подтверждённый 0; missing/error не обнуляют.
- Partial сохраняет валидные records, не удаляет отсутствующие. Failed/auth/
  permission/rate не уничтожают last-known-good.
- Единственный Ozon request controller; fixed HTTPS api-seller.ozon.ru, no redirects,
  TLS verification, bounded response/timeouts, endpoint allowlist и read semantics.
- Retry ограничен для network/timeout/5xx. 401/403 без retry. 429 -> structured
  state и persistent retry_not_before, без длинного sleep в Telegram.
- Request dedup только одинаковых read в одном connection/scope; отличающиеся
  параметры не объединять. Cache HIT/MISS/BYPASS и dedup NEW/JOINED_EXISTING
  различать от actual HTTP attempts.
- Trace: caller, scope, provider, tool, Go method, HTTP endpoint/status, attempt,
  duration, pagination, received/valid/saved, counters, safe numeric/date headers,
  retry deadline; без payload, Api-Key, Authorization и полного Client-Id.
- Не регистрировать fake tool ради ListTools. Если schema недоступна, не угадывать.

## API evidence / unresolved contracts

2026-09-27 официальный docs.ozon.ru/api/seller/swagger.json, /api/seller/en/,
/api/seller/?__rr=1 и /global/en/api/intro/ возвращают redirect loop.
Официальные новости подтверждают версии endpoints, но не полные response schemas:
https://t.me/s/OzonSellerAPI?after=387 (products v3),
https://t.me/s/ozonsellerapi?before=648 (seller stocks v2),
https://t.me/s/OzonSellerAPI?before=685 (analytics).

Upstream подтверждает request methods/paths/pagination inputs:
/v3/product/list, /v3/product/info/list,
/v2/product/info/stocks-by-warehouse/fbs, /v1/analytics/stocks.
Ответы в Python — dict, не typed contract. Нельзя подменить отсутствующие поля
quantity/warehouse/sku значениями по умолчанию. Запрошен путь к локальной official
OpenAPI или обезличенным response fixtures. До подтверждения response contract
не включать parsing неизвестных stock fields и не утверждать production readiness.

## L3 — последовательность и доказательства

1. Изучить текущие docs/code/audit, зафиксировать constraints — выполнено.
2. Подтвердить response contracts, pagination termination, source semantics — OPEN.
3. Независимо реализовать безопасный request controller + offline tests — выполнено
   в internal/marketplace/ozon; State interface пока без SQLite implementation.
4. Provider-aware migration, scope/service/cache/snapshots.
5. Typed confirmed endpoint decoders, per-record validation/partial.
6. Ozon module на existing MCP, scoped grants, ListTools/CallTool tests.
7. Минимальный Telegram UX без auto-fetch/N+1, secret input guard.
8. Приёмка: 38 проверок задания, WB/Day18/19 regression, go test ./..., build,
   standalone reports/ozon-stage-a/<run-id>/report.html. EXE не менять без необходимости.

План не является выполненной реализацией. Source of operational progress:
[marketplace WORKSTATE](marketplace/WORKSTATE.md).

Промежуточный проверяемый результат: [HTML report](../reports/ozon-stage-a/20260927T194512Z/report.html),
tests.jsonl и trace.json рядом. Полный go test ./... и build -o NUL прошли.
Это проверка HTTP foundation и существующих regression tests, не приёмка Stage A.
