# Identity & Access — L3

Этот документ фиксирует границу завершённого этапа Identity & Access. Следующее за ним задание Day 11 уже реализовано отдельно: [Модель памяти агента](day11-memory.md).

Реализован этап перед Day 11. Существующие решения WA-D009–WA-D026 сохранены в README. Memory Layers не реализованы.

## Модель и миграция

До изменений User уже существовал отдельно: `users.id`, `telegram_user_id`, username, display_name, created_at и is_active. Однако Telegram handler записывал внешний Telegram ID в Membership и не вызывал проверку ролей. Bootstrap создавал demo-мастерскую и тестовые данные при каждом запуске.

Теперь Telegram ID используется только для регистрации/обновления User; все проверки, Membership, активный контекст и авторство операций используют внутренний `users.id`. Username может меняться и не участвует в установлении доступа. User имеет nullable first_name/last_name, updated_at и status; Workshop — updated_at и status. User не принадлежит другому User.

`workshop_members` переиспользована как WorkshopMembership: уникальная пара user_id/workshop_id, FK к User и Workshop, роль, status, is_active, created_at, updated_at, invited_by_user_id, joined_at. Статусы: active, disabled, removed, left. Удаление означает изменение Membership, User и история сохраняются. Возврат отключённого/удалённого сотрудника выполняется администратором через восстановление доступа; invite не обходит ранее отключённую Membership.

Добавлены таблицы:

- `workshop_invites` — приглашения, hash, срок, лимит и количество использований, статус и автор.
- `user_workshop_context` — активная мастерская пользователя.
- `user_preferences` — личные настройки пользователя в JSON.
- `workshop_settings` — общие настройки мастерской в JSON.

Расширен существующий `audit_logs`: target_user_id, event_type, metadata_json. Существующие поля аудита сохранены. Добавлены индексы Membership(workshop_id,status), Invite(workshop_id), уникальный token_hash, Audit(workshop_id,created_at); индекс Telegram ID и уникальность Membership уже существовали.

Текущие domain-таблицы уже имели workshop_id — повторные столбцы не добавлялись. Мигратор поддерживает старые domain-таблицы без него при однозначно определяемой мастерской. Глобальные Users и личные настройки остаются user-scoped.

Миграция 100 выполняется однократно в транзакции. До изменений старой БД SQLite `VACUUM INTO` создаёт согласованную резервную копию рядом с исходной. При ошибке транзакция откатывается. Неоднозначное соответствие внешнего/внутреннего ID или неизвестный владелец останавливает миграцию, чтобы не выдать доступ ошибочному пользователю. Для старой single-user БД без Workshop создаётся «Моя мастерская» и OWNER Membership; неприкреплённые данные связываются с единственной мастерской.

В локальной БД на момент реализации уже было 18 demo-мастерских и один User. Они сохранены, новые не создавались. Исправлена прежняя Membership мастерской #6: внешний Telegram ID заменён внутренним `user_id=1`, роль нормализована в OWNER. У оставшихся 17 мастерских не было участников; их OWNER назначен единственному существующему пользователю. Активной осталась мастерская #6. При нескольких возможных пользователях такой автоматический выбор владельца запрещён.

Результат сверки: 19 материалов, 34 продукта, 34 строки BOM и все остальные domain-таблицы совпадают с резервной копией. Сохранены ID мастерских, их названия и исходные записи. Подробности: [migration.json](../reports/identity-access/migration.json).

## Централизованный доступ

`internal/auth` определяет отдельные типы Role и Permission, единую матрицу и AuthorizationService.RequirePermission. Проверяются status/is_active пользователя, мастерской и Membership, а затем permission. Неизвестная роль/permission не предоставляет доступ.

| Permission | OWNER | ADMIN | EMPLOYEE | VIEWER |
| --- | --- | --- | --- | --- |
| workshop.read | да | да | да | да |
| workshop.settings.manage | да | да | — | — |
| workshop.delete | да | — | — | — |
| ownership.transfer | да | — | — | — |
| members.read | да | да | да | да |
| members.invite, members.manage | да | да | — | — |
| inventory.read | да | да | да | да |
| inventory.write | да | да | да | — |
| products.read, bom.read | да | да | да | да |
| products.write, bom.write | да | да | — | — |
| production.read | да | да | да | да |
| production.create, production.update | да | да | да | — |
| shipments.read | да | да | да | да |
| shipments.write | да | да | да | — |
| audit.read | да | да | — | — |

Всего 21 permission. Матрица находится в одном пакете и расширяется без добавления проверок ролей в Telegram handlers. Разрешение workshop.delete зарезервировано: отдельный сценарий удаления мастерской не добавлялся.

Inventory, Products/BOM и Audit требуют привязки сервиса `ForUser(internalUserID)`. Каждый публичный domain-вызов заново проверяет permission; непривязанный сервис отказывает. Queries принимают workshop_id, ссылки BOM/движений дополнительно проверяются на принадлежность этой мастерской. Автор операций берётся из привязанного пользователя. Telegram проверяет права для UX, но не является единственным барьером. LLM interpreter проверяет Membership до вызова LLM и permissions перед обработкой production/shipment-команд. User management не вызывает LLM.

Существующие DB()/storage-интерфейсы остаются низкоуровневым доступом для миграций и локальных операторских инструментов, а не пользовательским API.

## Invitations

Владелец/администратор выбирает ADMIN, EMPLOYEE или VIEWER и подтверждает создание. OWNER в invite отсутствует. Генерируется 32 случайных байта через crypto/rand; raw URL-safe token содержит 43 символа. В SQLite хранится только SHA-256 hash. Ссылка: `https://t.me/<bot>?start=invite_<token>`.

По умолчанию срок — 24 часа, max_uses=1. Настройки: `INVITE_TTL` и `INVITE_MAX_USES`; сервис также принимает явные ttl/maxUses. Имя бота определяется через Telegram getMe при создании ссылки.

Получатель `/start invite_...` сначала видит название мастерской и роль. Membership появляется только после кнопки «Присоединиться». Принятие получает write lock транзакции перед проверками, повторно проверяет срок/статус/лимит, создаёт Membership, увеличивает used_count, выставляет used при исчерпании лимита, пишет audit и меняет активный контекст атомарно. Повторная Membership не создаётся и не расходует новую ссылку. При двух одновременных accept одноразового приглашения успешно завершается только один.

Отозванные, использованные и истекшие ссылки отвергаются. Эффективный статус expired вычисляется по expires_at при просмотре, без фонового таймера. Revoked и used сохраняются в БД. Raw token не попадает в audit/CLI; в UI он показывается только создателю ссылки и хранится временно для подтверждения принимающим пользователем.

## Активная мастерская и управление участниками

Если сохранённого контекста нет и доступна одна мастерская, она выбирается автоматически. При нескольких нужен явный выбор. Каждый раз active_workshop_id проверяется на актуальный доступ. При утрате доступа контекст сбрасывается и предлагается новый выбор.

Передача владения доступна только OWNER, требует отдельного подтверждения и активной Membership получателя. В одной транзакции получатель становится OWNER, прежний — ADMIN; создаётся OWNERSHIP_TRANSFERRED. Обычная смена роли не назначает OWNER. Отключение, удаление, выход и смена роли владельца запрещены до явной передачи владения — это защищает и последнего OWNER.

Audit events:

- WORKSHOP_CREATED
- MEMBER_INVITED
- INVITE_REVOKED
- INVITE_ACCEPTED
- MEMBER_ROLE_CHANGED
- MEMBER_DISABLED
- MEMBER_ENABLED
- MEMBER_REMOVED
- MEMBER_LEFT
- OWNERSHIP_TRANSFERRED
- ACTIVE_WORKSHOP_CHANGED
- IDENTITY_MIGRATED — дополнительное событие миграции.

Изменения управления и их audit event коммитятся вместе. В metadata приглашения записываются invite_id и роль, без raw token/hash.

## Telegram

Все пользовательские операции доступны в личном чате; групповые сообщения направляют пользователя в личный чат с ботом, чтобы данные мастерской и приглашения не раскрывались остальным участникам группы.

- `/start`: идемпотентная регистрация, текущая мастерская или onboarding.
- «Создать мастерскую»: название → подтверждение → Workshop + OWNER + active context.
- `/workshop`: текущая мастерская, сотрудники, смена мастерской, создание новой, выход; приглашения только с permission.
- `/workshops`: список с отметкой текущей мастерской.
- `/members`: список участников и карточки с именем, username при наличии, ролью, статусом, датой присоединения.
- Карточка сотрудника: смена роли, отключение/восстановление, удаление; для OWNER — явная передача владения активному участнику.
- Приглашения: выбор роли, создание ссылки, просмотр списка, отзыв.
- Изменения ролей/доступа, передача владения, выход и отзыв требуют подтверждения. Старые `/edit_material` и `/edit_product` также показывают подтверждение.
- Формы настройки разделены по chat_id + внутреннему user_id, права проверяются повторно на каждом шаге. После смены мастерской или утраты доступа старая форма не выполняется.

Inline-кнопки содержат случайный непрозрачный ID. Действие привязано к пользователю/чату и истекает через 15 минут. Чужая кнопка не выполняется; старая кнопка управления не изменяет другую мастерскую после переключения. Состояние UI хранится в памяти процесса: после рестарта меню нужно открыть заново, invite можно снова открыть по ссылке до его истечения. Сами приглашения и активный контекст сохраняются в SQLite.

## CLI и проверки

Локальный CLI — операторская диагностика с доступом к файлу SQLite, без второго framework и без LLM/Telegram-запросов:

```powershell
go run ./cmd/workshop-agent users list
go run ./cmd/workshop-agent workshops list
go run ./cmd/workshop-agent workshop members 6
go run ./cmd/workshop-agent workshop invites 6
go run ./cmd/workshop-agent workshop permissions 1 6
go run ./cmd/workshop-agent workshop audit 6
go run ./cmd/workshop-agent authorization-trace 1 6
```

Trace показывает Membership status, Role, фактически доступные permissions, сохранённый active context и результат проверки доступа. CLI не выводит invite token/hash.

Тесты Go покрывают повторную регистрацию/смену username, создание Workshop/OWNER/active context, invite preview/confirm, expiry/revoke/max uses, конкурентный accept через два соединения, duplicate Membership, multi-workshop switching, tenant isolation Inventory/Products/BOM/Members/Invites, матрицу permissions, защиту OWNER, transfer, disable/enable/remove/leave, сброс недоступного active context, audit без секретов и rollback при ошибке audit. Telegram тестируется через fake HTTP transport; настоящие сообщения не отправляются. Добавлены проверки CLI и запрета вызова LLM до авторизации.

Миграционные тесты проверяют backup, восстановление внутренних ID, сохранение продукции/материалов/BOM/исторического автора, single-user schema без workshop_id, идемпотентность, FK и отказ при неоднозначной identity.

Проверка: `go test ./...`. Проект написан на Go; pytest не используется. Сборка: `go build -o bin/workshop-agent.exe ./cmd/workshop-agent`.

## Граница Day 11

Подготовлены стабильные внутренние user_id/workshop_id, UserWorkshopContext, раздельные настройки, авторство и поля TaskID/SessionID в ConversationContext. Будущие ключи:

- short-term: user_id + workshop_id + session_id;
- working: user_id + workshop_id + task_id;
- personal long-term: user_id;
- workshop knowledge: workshop_id;
- product knowledge: workshop_id + product_id.

Сами Memory Layers не добавлялись. Старые MVP-заготовки production/shipment не превращались в новые ERP-сервисы: их дальнейшее развитие остаётся отдельной задачей. Реальные Telegram-запросы и работа с пользователями через сеть в ходе проверки не выполнялись.
