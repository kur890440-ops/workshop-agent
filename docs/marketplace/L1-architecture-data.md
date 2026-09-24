# L1 — модули, сущности и размещение данных

@PROJECT:WORKSHOP_AGENT @NO_COMPRESS @PRESERVE

Статус: модель реализована миграцией 110; результаты проверок — в L3.

Таблицы: marketplace_connections, marketplace_cards, marketplace_variants,
marketplace_barcodes, marketplace_mappings, marketplace_stocks, marketplace_orders,
marketplace_sync. Технические события используют существующую audit_logs.
Сервис использует существующую storage/SQLite базу через WS.DB(), без второго файла БД.
Обновление карточек помечает отсутствующие карточки/варианты present=0, сохраняя
ссылки сопоставлений; исчезнувшее сопоставление получает status=missing.
UI и deterministic `/wb` в Agent читают сервис; внешние данные в LLM не передаются.

Day17 добавляет явный `/wb_stocks`: прикладной handler → marketplace access gate →
существующий MCP client → STDIO server → WB adapter. Размещение секрета и общей
таблицы cooldown описано в [WA-D150](../day17-first-mcp-tool.md#wa-d150--область-доступа-и-конфигурация).
Полученный MCP результат используется в ответе, не становится производственным
остатком или памятью LLM. Прежний `/wb` sync/cache flow сохраняется.

## Модули и зависимости

| Часть | Ответственность | Разрешённые зависимости |
|---|---|---|
| Telegram / Agent | Разобрать намерение, показать данные и состояние | Marketplace Service, существующая identity/context инфраструктура |
| `internal/marketplace` | Use cases, принадлежность кабинета, доступ, синхронизация, сопоставление | auth, интерфейс WB adapter, storage, безопасный audit |
| `internal/marketplace/wildberries` | Фиксированные API endpoints, DTO WB, HTTP, пагинация, ошибки | net/http, context, provider секрета/конфигурация; без Telegram/LLM |
| Существующий `internal/storage` | Миграции и хранение интеграционных сущностей | SQLite, без сетевых вызовов |
| Конфигурация/секрет | Чтение WB_API_TOKEN при запуске и передача адаптеру | Процессное окружение; без сериализации секрета в DTO |
| Представление | Ограниченная выборка, источник, давность и полнота | Прикладные DTO; не raw WB payload |

Синхронизация — техническая загрузка данных, не второй производственный Task/FSM.
WB DTO преобразуются в локальные записи на границе адаптера/сервиса; изменения
внешнего JSON не должны распространяться в Telegram или производственную логику.

## Владение

User → WorkshopMembership → Workshop → MarketplaceConnection.
Для бизнес-операций используются внутренние user_id, а не Telegram ID.
Connection → внешние товары, заказы, остатки и загрузки.
ProductMapping связывает продукт той же мастерской с вариантом внешнего товара.
Существующие правила identity: [Identity & Access](../identity-access.md).

## Распределение данных и источники истины

| Хранилище / сущность | Что хранить | Что исключить / семантика |
|---|---|---|
| `.env` / окружение процесса | WB_API_TOKEN; в памяти — только для выполнения разрешённого запроса | Настоящий секрет не копируется в SQLite, документы, memory, fixtures, отчёты и LLM. Тестовый маркер не является настоящим credential |
| SQLite: Connection | connection_id, workshop_id, provider, доступные seller ID/name, состояние, имя источника секрета, даты создания/проверки/отключения | Только ссылка `WB_API_TOKEN`, не значение. Статус настройки не равен успешной проверке WB |
| SQLite: ProductMapping | connection_id, workshop_id, product_id, nmID, variant ID/баркод, состояние сопоставления | Явная связь, не автоматическое равенство по названию |
| SQLite: ExternalProduct | Нужные идентификаторы и поля карточки/варианта, время получения и доступное время изменения WB | Не копировать весь raw response; не заменять наши products |
| SQLite: ExternalOrder | Внешний ID, необходимые строки/статусы/времена и scope | Это внешний заказ, не производственная задача и не shipment |
| SQLite: ExternalStock | Scope, тип/ID склада, идентификатор варианта, количество, времена источника/получения, принадлежность загрузке | Остатки WB и продавца различимы; не products.current_stock/materials.current_stock |
| SQLite: SyncRun/checkpoint | Тип загрузки, scope, состояние, безопасный cursor при необходимости, прогресс, timestamps, last full success, безопасный error code | Cursor не должен содержать credentials. Неполная загрузка не выдаётся за актуальный полный снимок |
| Существующий audit | Время, actor user, мастерская, connection, действие и результат | Без секретов, полных payload и ненужных персональных данных |
| Контекст LLM | Только нужный для текущего ответа фрагмент, источник, время актуальности, неполнота | Нет credentials; external text — untrusted. Не является источником прав/остатков/заказов |
| WORKSTATE / документы | Решения, этап, ссылки, команды проверок, результаты и нерешённое | Не runtime business data, не секреты и не копия рабочей БД |

WB — внешний источник состояния кабинета. SQLite хранит локальную выборку с её
происхождением и давностью, а не обещание текущего состояния удалённого API.
Membership/permissions текущей БД — источник прав. Сервисы цеха — источник
производственных данных. История диалога/long-term memory их не заменяют.

Конкретные unique keys, права и поведение обновлений — [L2](L2-contracts-decisions.md).

Дополнение migration 111: `marketplace_cooldowns` хранит группу запросов одного
источника WB_API_TOKEN, UTC deadline в целых миллисекундах (округление вверх)
и закрытый источник срока. В `marketplace_sync` добавлены blocked_by, retry_at,
retry_source, rate_operation. Токена/его отпечатка в этих полях нет.
Проверенная identity живёт только в памяти Service с ключом workshop/connection/
revision/seller; новый Service с новым неизменяемым клиентом начинает без доверия.
Client отвечает за parsing заголовков и интервалы, Service подключает SQLite
CooldownStore и проверяет права, Telegram показывает сроки и результат.
WB_API_PROFILE — несекретная настройка профиля интервалов, не доказательство
прав токена и не источник identity. Подробная политика — WA-D145–147.
