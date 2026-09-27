# WB rate-limit audit — 2026-09-25

@PROJECT:WORKSHOP_AGENT @NO_COMPRESS @PRESERVE

## Аудит до изменений

| Caller | MCP tool | WB method | HTTP endpoint | Причина / частота |
|---|---|---|---|---|
| Startup, MCP initialize/ListTools, /wb status/menu | нет | нет | нет | HTTP не вызывается |
| /wb check, «Проверить WB» | legacy direct flow | Seller | GET common-api /api/v1/seller-info | Каждое явное действие, если preflight cooldown разрешает |
| /wb sync, кнопки обновления/остатков | legacy direct flow | Seller | тот же | При пустом RAM identity cache, после restart/24 часов/revision/error |
| /wb_stocks [trace] | wb_get_seller → wb_get_wb_stocks | Seller → WBStocks | seller-info → POST seller-analytics /api/analytics/v1/stocks-report/wb-warehouses | Отдельный MCP RAM identity cache, после restart/24 часов/ошибки |
| Scheduler / Run Now, WB_DAILY_SYNC | wb_get_seller → wb_get_prices → wb_get_wb_stocks | Seller, Prices, WBStocks | seller-info; GET discounts-prices /api/v2/list/goods/filter; POST stocks-report/wb-warehouses | Один run на локальную дату; страницы запросов отдельно |
| /wb sync catalog; MCP wb_get_products | wb_get_products либо legacy direct | Catalog | POST content-api /content/v2/get/cards/list | Cursor pagination, interval 600 ms |
| /wb sync seller_stocks | legacy direct | SellerStocks | GET marketplace-api /api/v3/warehouses; POST /api/v3/stocks/{warehouse} | Склады и пачки SKU |
| /wb sync orders; MCP tools | wb_get_new_orders / wb_get_order_statuses либо legacy direct | NewOrders, OrderStatuses | GET marketplace-api /api/v3/orders/new; POST /api/v3/orders/status | Новые заказы и пачки статусов |
| /wb_auto summary | wb_get_daily_summary | нет | нет | Только SQLite |
| Offline reports/tests | те же MCP handlers | fixture API | только mocks | Реальные WB/Telegram не вызываются |

Выявлено: два RAM identity cache с TTL 24h; persistent seller_id/name есть, но
не привязаны к версии токена. Один WB client уже переиспользуется production
legacy/MCP путями. HTTP retry только в клиенте (5xx до 3 попыток), 429 без retry.
Marketplace.Start имеет отдельный preflight cooldown по common/analytics.
Другого heartbeat/reconnect HTTP нет. Каждое нажатие check принудительно запрашивает
seller-info. Прежние logs не фиксировали все HTTP attempts, точное число неизвестно.

Проблемы: нет заголовков успешных ответов/remaining, нет bounded trace; MCP теряет
retry timestamp; rate slot освобождён до HTTP response, долгий ответ может позволить
другому caller отправить запрос до получения нового 429. Source server-header может
ошибочно подписывать срок, увеличенный локальным pacing. 409 penalty применён ко всем
группам, хотя относится к конкретным marketplace methods.

## Сверка документации

Официальные индексированные страницы WB: api-information (включая JWT acc/for/t,
Retry/Reset в секундах, seller-info 1/min burst10), work-with-products (prices
10/6s, interval600ms, burst5), analytics (wb-warehouses 3/min,20s,burst1,
PERSONAL/SERVICE), orders-fbs (shared marketplace limits).
Прямое открытие страниц 2026-09-25 возвращает HTTP498; индекс помечен 5 месяцев.
Утверждать, что индекс гарантированно отражает сегодняшние правила, нельзя.
Ответные заголовки WB приоритетны и не сокращаются до индексированных лимитов.
Суточный срок seller-info не следует из доступной таблицы — требуется trace
реального ответа, а не вывод о типе токена по длине cooldown.

Официальный GET /ping предназначен для ручной проверки подключения/токена,
не для heartbeat доступности; максимум3/30s, автоматизацию WB запрещает.
Нового автоматического ping не добавлять; текущего heartbeat в приложении нет.

## WA-D158 — общий rate limiter и persistent identity

Дата решения: 2026-09-25. Заменяет RAM-only/24h identity из WA-D145 и соответствующую
часть WA-D150. Остальные ограничения изоляции и единый MCP WA-D154–156 сохраняются.

- Один WB Client остаётся владельцем rate policy. Удалён отдельный preflight в
  Marketplace.Start. Telegram, Scheduler и MCP не делают собственных HTTP retries.
- Seller profile хранится в существующей marketplace_connections: workshop_id,
  seller_id/name, checked_at и identity_fingerprint (SHA-256 неизменяемого токена).
  Это техническая привязка к версии секрета, не сам токен; fingerprint не выводится
  в Telegram, trace, отчёт или LLM. Успешный профиль не имеет произвольного TTL.
- При смене токена нужен новый ответ seller-info; sid обязан совпасть с привязкой.
  Для старой БД после migration115 fingerprint пуст: один повтор идентификации
  после cooldown необходим, автоматически приписывать старый sid новому токену нельзя.
- /wb check остаётся явным refresh identity и подчиняется тому же limiter.
  Обычные sync и MCP tools используют общий кэш. Startup, меню, ListTools,
  /wb debug и сохранённая сводка не отправляют WB HTTP.
- Identity gate объединяет параллельные проверки. Каждый rate group имеет отдельный
  gate до получения HTTP response; поздний 429 известен до следующего запроса группы.
- Все response сохраняют безопасные numeric Remaining/Retry/Reset/Limit. При 429
  Retry (секунды от получения ответа, НЕ Unix timestamp) / Retry-After имеют приоритет;
  Reset используется при отсутствии валидного Retry. Без headers — локальный backoff.
  Нулевая квота на HTTP200 также ставит cooldown. Без валидного срока headers используется консервативный полный документированный период группы: prices6s, остальные60s; source=local_interval, не выдуманный срок WB.
- marketplace_cooldowns переиспользуется, более поздний deadline не сокращается.
  Если локальный интервал длиннее server Retry, его source остаётся local_interval;
  фактические server seconds отдельно видны в observations/trace.
- 429 не повторяется внутри операции; будущие callers немедленно получают
  BLOCKED_LOCALLY. Короткий pacing <=30s допустим в фоновой работе, долгие ожидания
  возвращаются как controlled error. Сетевые ошибки/5xx: максимум3 HTTP попытки
  (с pacing их может быть меньше). 401/403/прочие4xx/redirects не повторяются.
- 409 penalty ограничен marketplace group: выбран консервативный cost10 ввиду
  разных официальных RU/EN указаний5/10. Не переносится на prices/content/analytics.
- MCP возвращает IsError + structured code=WB_RATE_LIMITED, rate_key, endpoint,
  retry_not_before, source, blocked_locally. SDK клиент восстанавливает тип ошибки.
  WB_DAILY_SYNC сохраняет failed/partial_success, WB_RATE_LIMITED и максимальный
  retry_not_before среди частей; расписание остаётся. Повторной петли jobs нет.
- Trace: последние100 событий глобально в wb_request_trace, безопасные поля request,
  caller/tool/workshop, HTTP status, duration, попытка, headers. CACHED и
  BLOCKED_LOCALLY имеют HTTP=0. /wb debug требует MarketplaceManage, история
  фильтруется по мастерской. Полных URL/query/body/Authorization/credentials нет.

Группы применяются ко всему единственному настроенному кабинету, а не отдельно
к Telegram user/tool. Это намеренно консервативно и защищает общие квоты категорий.
Другие приложения с тем же кабинетом находятся вне этого процесса; их частоту
этот limiter не координирует. Она может объяснять новые429, но пока не подтверждена.

## Политика методов и официальные источники

| Группа/метод | Документированный лимит / интервал / burst | Реализовано |
|---|---|---|
| seller-info | 1/min; 60s; burst10 на продавца, отдельная BASE-строка не найдена | 60s без использования burst; persistent identity |
| prices | 10/6s; 600ms; burst5, общая Prices/Discounts группа | 600ms; response headers приоритетны |
| wb-warehouses | 3/min; 20s; burst1; данные раз в30min | 20s; headers; тип токена локально только диагностируется |
| content | 100/min; 600ms; burst5, выбранный cards/list | 600ms, cursor pagination |
| marketplace | PERSONAL/SERVICE300/min; BASE/TEST150/min; burst20/10 | 400ms для всех, общая группа, без burst |

Ссылки, сверка 2026-09-25 (ограничение свежести индекса описано выше):
- https://dev.wildberries.ru/docs/openapi/api-information
- https://dev.wildberries.ru/en/openapi/work-with-products
- https://dev.wildberries.ru/openapi//analytics
- https://dev.wildberries.ru/release-notes?id=272
- https://dev.wildberries.ru/knowledge-base/articles/019d49a1-28ca-7735-bf2f-98210695abc7/limity-zaprosov-wb-api
- https://dev.wildberries.ru/knowledge-base/articles/019d49a1-1540-76f6-befe-726633dc11be/dekodirovanie-i-proverka-tokenov-wb-api

BASE определён локально по acc/for/t, JWT signature не проверялась. Это не доказательство
прав на конкретный метод. Индекс WB указывает PERSONAL/SERVICE для wb-warehouses;
текущую применимость к BASE нужно уточнять у WB. Не вводим новый жёсткий запрет
на основе устаревшего индекса. В live-диагностике проверяется только prices.

## Фактический before / after

BEFORE: explicit /wb check → Seller HTTP (если allowed); первый legacy sync после
restart/24h → Seller HTTP; первый MCP Stocks/Prices после restart/24h → ещё один
Seller HTTP. Daily job мог инициировать этот MCP preflight. Меню/startup уже были
без HTTP. Частота реальных отправок не восстанавливается из статуса последнего sync.

AFTER: первый verified seller → SQLite; следующий caller/restart → CACHED;
MCP Prices/Stocks проверяют привязку через общий cache и выполняют только нужный
endpoint. При cache miss — единственный seller-info flight с общим cooldown.
Explicit refresh идёт через тот же gate. Никакого heartbeat не добавлено:
health — конфигурация, кэш и последние реальные ответы API. /ping не используется.

До изменений полноценного request trace не было. Пример BEFORE можно показать
только как известный статус: check failed/rate_limited; orders/wb_stocks blocked
by seller-info. Это НЕ доказательство HTTP-вызова orders/stocks и не выдуманный лог.
Пример AFTER из offline-теста: telegram_wb_sync Seller → WB_429 HTTP429 Retry120;
через10s wb_daily_sync Seller → BLOCKED_LOCALLY HTTP0; после121s → HTTP200.
Живые события записаны отдельно в audit.json отчёта, без смешения с mock.

## Диагностика и данные

Migration115: marketplace_connections.identity_fingerprint; wb_rate_observations;
wb_request_trace; background_job_runs.retry_not_before. Существующие cooldown и
бизнес-данные сохраняются. Миграцию выполняет обычный startup, не диагностическая команда.

- Telegram: /wb debug — owner/manage-only локальный экран.
- bin/workshop-agent.exe wb-rate-audit — read-only локальный отчёт.
- bin/workshop-agent.exe wb-rate-audit --probe-prices — только при остановленном боте:
  тот же in-memory MCP, существующий wb_get_prices, максимум ОДНА HTTP попытка,
  без seller-info, фоновых заданий, Telegram, миграций или полного импорта.
  Если нужна следующая страница, controlled result_limit означает ограничение
  диагностического бюджета, не успешную полную синхронизацию.
  Новые технические cooldown сохраняются в существующей marketplace_cooldowns,
  чтобы последующий запуск не потерял реальный429. Бизнес-таблицы не меняются.
  Read-only команда не меняет БД. .env читается только в памяти, не выводится/не записывается.

## Реальная диагностика 2026-09-25

Read-only отчёт 08:53:44 UTC подтвердил BASE и сохранённый common cooldown
до 2026-09-25T12:17:48.284Z (15:17:48.284 Москва). Рабочий процесс к live-пробе
уже завершился; агент его не останавливал и рабочий бот не запускал.

В 08:57:03 UTC существующий wb_get_prices через MCP получил HTTP200 за138ms.
Ровно1 HTTP; следующий page locally stopped: DIAGNOSTIC_BUDGET / HTTP0.
Headers: Remaining=0, Limit=1, Retry/Reset не распознаны как числовые значения.
Это основание для добавленного fallback полного периода при Remaining0.
Фактический Limit1 отличается от индексированной таблицы10/6s: точный текущий
период BASE по этому единственному ответу установить нельзя. Полный импорт
и работоспособность stocks этим probe НЕ подтверждены. Общего запрета всех WB API
нет: prices доступен. Seller-info не вызывался, его deadline не сбрасывался.

Raw-safe evidence: reports/wb-rate-limit-audit/20260925T085703.592905300Z/audit.json.
Итоговый report.html и команды приёмки указаны в WORKSTATE.

## Проверяемые сценарии

- TestRate120SharedCallersAndExpiry: 429 +120s, локальный запрет через10s, запрос после121s.
- TestServerCooldownSurvivesNewClientAndNoEarlyRequest: длинный cooldown после нового клиента.
- TestPersistentIdentityRestartAndTokenRotation: SQLite cache, menu/debug/restart безHTTP;
  изменённый токен не использует чужую привязку.
- TestConcurrentSellerSingleFlightAndGroup429 / TestGroupGateWaitsFor429BeforeNextHTTP:
  параллельные проверки и разные методы одной группы не обходят пришедший429.
- TestRemainingZeroOnSuccessfulResponse: HTTP200/Remaining0/Reset120.
- TestMCPUsesPersistentIdentityAndStructuredCooldown: настоящий MCP in-memory,
  shared identity, deadline/source/caller/workshop без потерь.
- TestRateLimitedRunPersistsDeadlineAndSchedule: partial_success, deadline, расписание сохранено.
- TestDiagnosticBudgetBoundsPagesAndRetries: один HTTP, никакой массовой live-выгрузки.
- Существующие тесты проверяют 401/403/4xx/5xx, secrets, redirects/hosts,
  пагинацию, SQLite/migrations, permissions, изоляцию и partial snapshots.

Состояние итоговых команд и артефактов — docs/marketplace/WORKSTATE.md и L3.

## WA-D159 — точный формат request trace (v0.18.5)

Уточняет формат WA-D158 по требованию пользователя. Каждая HTTP попытка получает
отдельную запись после закрытия ответа; duration включает чтение/проверку JSON,
но не паузу перед retry. Сетевые сбои также фиксируются. Набор result закрытый:
SUCCESS / WB_429 / BLOCKED_LOCALLY / ERROR. HTTP200 с некорректным JSON — ERROR.
SUCCESS обозначает обработанный HTTP-ответ, не завершение всей многозапросной синхронизации.

JSON-поля: timestamp, workshop_id, caller, operation, mcp_tool, wb_method,
http_method, endpoint, request_started_at, request_finished_at, duration_ms,
http_status, X-Ratelimit-Limit, X-Ratelimit-Remaining, X-Ratelimit-Retry,
X-Ratelimit-Reset, retry_not_before, attempt_number, result.

operation — фиксированное имя операции адаптера (seller_info, prices, wb_stocks,
catalog, seller_stocks, new_orders, order_statuses); wb_method — имя Go-метода.
MCP tool пуст для legacy direct flow. Все timestamps новых записей — UTC.
Числовые headers дублируются на верхнем уровне с точными именами из задания;
отсутствующие/невалидные значения — null. retry_not_before — известный срок
ожидания (учитывая более поздний локальный pacing), иначе null.

attempt_number начинается с1 для первой HTTP попытки каждой страницы/запроса.
При отсутствии попытки —0; request_sent=false. Кэш: SUCCESS/detail=CACHED;
диагностический бюджет: BLOCKED_LOCALLY/detail=DIAGNOSTIC_BUDGET. Сетевая ошибка:
ERROR/detail=NETWORK_ERROR или TIMEOUT, request_sent=true означает попытку
транспорта, а не доказательство получения запроса сервером WB.

Хранение прежнее: последние100 событий в SQLite wb_request_trace.record_json и
runtime ring. /wb debug показывает краткие последние события своей мастерской.
Secrets, raw payload и Authorization исключены. Исторические записи/отчёты
предыдущего формата не переписываются. Новая миграция не нужна.
Проверка контракта: TestTraceContractAndCompletedResponse (200, malformed200,
403,429, все JSON-поля, время завершения, локальная блокировка, отсутствие секрета).

## WA-D160 — caller, фактический endpoint и контекст HTTP429 (v0.18.6)

Уточнение пользователя по четырём требованиям к диагностике. Caller сохраняет
инициатора при прохождении MCP; в trace: background:WB_DAILY_SYNC,
background:WB_DAILY_SYNC:run_now, telegram_wb_sync, seller_info_refresh,
telegram_wb_stocks, diagnostic. Если исходный caller неизвестен и вызов пришёл
на MCP handler, записывается mcp_tool:<реальное имя tool>. Отдельное поле mcp_tool
не затирает исходного инициатора. Startup/меню по-прежнему не вызывают WB HTTP;
искусственных событий для них не создаём.

Добавлены host и верхнеуровневый rate_key. endpoint теперь содержит фактический
разрешённый path, включая числовой warehouse ID; query/Authorization/body не пишутся.
Нормализованный path в rate observation описывает группу; request endpoint точный.
HTTP429 → WB_429, локальный запрет → BLOCKED_LOCALLY/HTTP0/request_sent=false.
HTTP0 при сетевом сбое означает отсутствие ответа, не доказательство отсутствия
сетевой попытки. RequestSent=true — транспортная попытка, не подтверждение WB.
Новый429 после разрешённого срока сам по себе не доказывает обход limiter:
нужно сравнивать время, группу и ранее сохранённый retry_not_before.

Migration116: wb_rate_incidents(id, workshop_id, created_at, history_json).
В той же короткой транзакции, что и запись настоящего429, сохраняются это событие
и до50 предыдущих событий своей мастерской. Если накоплено меньше — только имеющиеся.
Локальные блокировки и ошибки сети не создают ложных HTTP429 incidents.
Снимок не исчезает при очистке текущего100-event ring. Сохраняются последние10
инцидентов на мастерскую, затем самые старые удаляются. Бесконечных журналов нет.
Обычная история и snapshot содержат HTTP-попытки, local blocks и cache events;
их нельзя все считать реальными сетевыми запросами.

/wb history [offset] — последние сохранённые события;
/wb incident [offset] — последний429 и предыстория.
Оба читают SQLite, требуют MarketplaceManage и текущую мастерскую/подключение;
пагинация по4 события, от новых к старым. Показывают caller, MCP/Go method,
host/path, group, status/result/sent/attempt, timestamps, headers и deadline.
Новых WB-запросов или автоматических сообщений не создают. /wb debug остаётся
коротким представлением, теперь также показывает endpoint и request_sent.

Проверки: TestIncidentHistoryPersistsBeyondRingAndRestart — 60 событий,429,
120 local blocks, новая SQLite-сессия: cooldown и snapshot51 сохранены,
ring100, один incident; чужой user/workshop и отрицательный offset отвергаются;
retention10 подтверждён. TestTraceActualEndpointAndCaller проверяет host, path
со складом, rate_key и caller. Прежние MCP-тесты подтверждают caller после протокола.
Исторические данные до внедрения trace восстановить невозможно.
