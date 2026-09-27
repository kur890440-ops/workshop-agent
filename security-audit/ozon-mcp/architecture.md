# Архитектура и поверхность выполнения

Все upstream ссылки относятся к AUDITED COMMIT из README.

```text
MCP client --stdio--> ozon_mcp.server:main
MCP client --HTTP SSE /sse + POST /messages/--> FastAPI app
                         ↓
             manually declared TOOLS + call_tool dispatch
                         ↓ shop_id
            settings.load_shops -> per-shop client pool
                         ↓
       OzonSellerClient             OzonPerformanceClient
       Client-Id / Api-Key          client_credentials -> Bearer
                         ↓
            api-seller.ozon.ru / api-performance.ozon.ru

Browser -> FastAPI dashboard / shops / diagnostics
        -> third-party JS unpkg.com and CSS cdn.jsdelivr.net
FastAPI health loop -> diagnostics -> same Ozon hosts
settings -> shops.json + adjacent .encryption_key
stats -> stats.db (calls and detailed health results)
```

## Модули

| Файл | Ответственность |
|---|---|
| pyproject.toml | Python >=3.10, console scripts, Hatchling build, зависимости |
| ozon_mcp/server.py | MCP Server, 156 вручную объявленных tools, JSON schemas, dispatch, pool клиентов и shaping |
| ozon_mcp/client.py | Seller и Performance HTTP wrappers, retry, некоторые composite methods |
| ozon_mcp/settings.py | ENV, миграция settings.json, encrypted shops.json, masking |
| ozon_mcp/app.py | FastAPI, SSE, shop administration, dashboard, health loop |
| ozon_mcp/diagnostics.py | 12 Seller probes, roles/expiry, проверка hosts и Performance token |
| ozon_mcp/stats.py | aiosqlite: tool_calls, health_checks, деградации |
| ozon_mcp/shaping.py | Сокращение полей ответов и пометки усечения для LLM; не HTTP size limit |
| ozon_mcp/toolsets.py | Профили инструментов; проверка и в discovery, и в dispatch |
| templates/*.html | Jinja UI, inline JS, CDN dependencies |
| scripts/collect_corpus.py | Только вручную: читает выбранные tools, маскирует часть PII и пишет corpus в --out |
| scripts/measure_corpus.py | Локальные измерения corpus; не runtime MCP |
| tests/ | 50 тестов, в этом окружении 49 pass + 1 skip |
| Dockerfile / docker-compose.yml / start.sh | Web deployment, volume /data, запуск compose; не часть install hook |

Нового endpoint в Ozon недостаточно для его появления в MCP: список TOOLS и dispatch задаются вручную; генерации из OpenAPI нет. В client.py есть и незарегистрированные функции, например notification_set: наличие wrapper не означает доступный tool.

## Transports и defaults

| Запуск | Транспорт / bind | Auth | TLS / CORS / Origin |
|---|---|---|---|
| ozon-mcp / python -m ozon_mcp.server | STDIO; listening socket не нужен | Доверие к локальному клиенту/OS | Неприменимо; любой подключённый клиент получает выбранные профили |
| ozon-mcp-web / app.main | SSE и POST messages; 0.0.0.0:8000 | MCP_AUTH_TOKEN опционален, только MCP routes | HTTP без встроенного TLS; CORS middleware нет; явной Origin/Host policy нет |
| Docker по умолчанию | Uvicorn SSE + web, 0.0.0.0:8000, publish 8000:8000 | Пустой token = без MCP auth | Нужен внешний TLS/auth, UI всё равно без собственного auth |

Streamable HTTP и WebSocket routes upstream не реализованы. Наличие websockets в зависимостях uvicorn не делает их transport проекта. В разрешённом MCP SDK 1.30.0 `TransportSecurityMiddleware` без settings отключает DNS rebinding protection; upstream вызывает SseServerTransport без настроек. `/messages/` принимает живой session_id вместо повторной Bearer-проверки: этот UUID является session capability, его нельзя публиковать.

## Web routes

| Route | Действие | Проверка auth в коде |
|---|---|---|
| GET /sse | MCP stream | Только если задан MCP_AUTH_TOKEN; также допускается query token |
| /messages/ | MCP сообщения | Bearer/query token или UUID живой сессии |
| GET /, /shops, /diagnostics | Данные/UI | Нет |
| POST /api/shops | Создать/изменить shop/credentials | Нет |
| DELETE /api/shops/{shop_id} | Удалить сохранённый shop | Нет |
| POST /api/shops/{shop_id}/test | Активные Seller/Performance вызовы | Нет |
| POST /api/diagnostics/run | Все магазины, активные probes | Нет |
| GET /api/diagnostics/{shop_id} | Активные probes, запись health | Нет; GET здесь имеет побочный эффект |
| GET /api/stats, /api/health | История, ошибки и health | Нет |
| GET /docs, /redoc, /openapi.json | FastAPI documentation | Нет, defaults |

## Tenant, filesystem, execution

Pool индексируется shop_id; случайного переключения между ключами в обычном dispatch не обнаружено. Однако users/workshops/ACL отсутствуют: любой допущенный MCP client выбирает любой shop. Single shop подставляется автоматически. Смена settings сбрасывает pool, но активный запрос может пересекаться с закрытием клиента; per-shop transaction/lock нет.

MCP не предоставляет произвольный filesystem path и не выполняет shell/eval/subprocess. `chat_send_file` передаёт URL в Ozon, не читает локальный файл; image URLs тоже уходят в тело Ozon API. DATA_DIR задаёт оператор, не tool. Стандартные записи — stats.db, shops.json, .encryption_key, legacy settings.json. SQL использует параметры, подтверждённой SQL injection не найдено. Shell в start.sh запускает фиксированный docker compose; subprocess в тестах запускает тестовый сервер, не получает MCP args. CLI collect_corpus пишет в заданный оператором --out, это не MCP capability.

JSONschemas проверяют базовые типы/required через SDK, но почти не содержат numerical ranges, maxItems, maxLength или nested required для write objects. Отдельные проверки есть (carriage delivery method, максимум дней финансов, cap pricing strategy), они не составляют общей валидации. Shaping сокращает контекст, но внешние тексты всё ещё попадут в LLM как данные: отдельного enforcement против prompt injection в upstream нет.
