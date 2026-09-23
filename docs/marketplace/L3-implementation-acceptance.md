# L3 — этапы реализации и приёмка

@PROJECT:WORKSHOP_AGENT @PRESERVE

## Приёмка WA-D145–147, 2026-09-23

Исправление identity/cooldown реализовано. Migration 111 идемпотентна,
сохраняет существующий срок и требует backup перед применением к старой базе.
Проверки на fake HTTP/clock и временной SQLite:
- TestRateHeaderPolicy: Retry-After/date, Retry/Reset, конфликт, ноль, отрицательные,
  переполненные и длительные значения; Reset применяется только как fallback.
- TestServerCooldownSurvivesNewClientAndNoEarlyRequest: 429, сохранение, новый
  клиент, повторные действия без HTTP, разрешение после deadline.
- TestPacingFakeClockAndCancellation, TestUnknownProfileConservativeAndLongLocalWait.
- TestIdentityCacheExpiryRestartRevisionAndExplicitCheck: кеш, 24ч, рестарт,
  отключение/новая revision, принудительный check.
- TestIdentityBlockedAndStocksRateLimitAreDistinct: зависимость, свой 429,
  время попытки, сохранение прошлого снимка и last_success.
- TestCooldownPersistenceMigrationAndNoSecrets: SQLite, миграция и отсутствие секрета.
- TestCachedIdentityNeverCachesAccessOrExplicitFailure: active workshop,
  удалённое membership, сброс доверия после ошибки явной проверки.
- Обновлён TestSellerRotationAndSafeErrors: замена неизменяемого клиента через
  новый Service, запрет смешивания кабинетов; существующие partial/pagination/UI tests.

`go test ./...` — exit 0; Telegram 54.354s, Storage 5.573s, Marketplace 6.327s,
WB client 1.027s, MCP client/server проходят. После уточнения округления deadline
вверх и дополнительной проверки прав — целевой повтор marketplace/integrations/config,
exit 0. `go build -o bin/workshop-agent-wb-cooldown.exe ./cmd/workshop-agent` — exit 0.
Бинарник не запускался. Настоящие env/DB/WB/Telegram не использовались.
Остались D1 и V1; успешные тесты не гарантируют исчезновение внешнего 429.

Статус на 2026-09-22: локальная реализация, mock-тесты и сборка выполнены.
Полная актуальная сверка D1 и реальная проверка V1 остаются незавершёнными.
План и критерии ниже не являются разрешением выполнить реальные внешние операции.
Границы первого этапа определены в [L0](L0-scope.md).

| Этап | Зависимость | Результат и критерий | Статус |
|---|---|---|---|
| D0: контекст | Существующие L0–L3 | Карта, распределение данных, ID решений, состояние продолжения | Выполнено; эти документы |
| D1: контракты WB | WA-D139 | Полные схемы/права/лимиты выбранных endpoints с URL и датой | Частичная сверка по официальному индексу; HTTP 498 препятствует полной актуальной сверке; [реестр](API-CONTRACTS.md) |
| I1: доступ/конфигурация | L1, WA-D127–130 | Connection, permissions, однозначная привязка, отсутствие доступа без прав, секрет из env | Реализовано, mock-тесты пройдены |
| I2: HTTP adapter | D1, WA-D131–132 | Типизированные DTO, fixed hosts, ошибки, retries, pagination; mock-tests | Код и mock-тесты готовы; зависимость от D1 открыта |
| I3: storage/mapping | I1, L1, WA-D133–136 | Миграции временной БД, keys/scope, явное сопоставление, отсутствие влияния на цех | Migration 110, tests passed |
| I4: синхронизация | I2 + I3 | Ручное обновление, координация, partial/success, idempotency, cancellation | Реализовано, mock-тесты пройдены |
| I5: Telegram/Agent | I1 + I4 | Действия L0, права на каждом callback, свежесть/неполнота; без передачи секретов | Реализовано, fake Telegram tests passed |
| I6: приёмка Go | I1–I5 | Необходимые тесты, отдельный бинарник, инструкция настройки и ограничения | go test ./... и отдельная сборка прошли; не означает закрытие D1/V1 |
| V1: реальный WB | I6 и отдельная авторизация пользователя | Отдельный live protocol с разрешённым токеном/данными | Отложено, не выполнено |

## Обязательные проверки

| Решение | Доказательство, необходимое при реализации |
|---|---|
| WA-D127–128 | Чужие connection/workshop, отсутствие membership, старая кнопка, повтор attach, отключение во время sync отклоняются корректно |
| WA-D129 | Синтетический secret marker отсутствует в SQLite, логе, ошибке, Telegram и LLM payload; нет токена — цех работает |
| WA-D130–132 | Только выбранные операции; IDs/ranges; 401/403/429/5xx, timeout, malformed/oversized response, redirect и чужой host |
| WA-D132 | Несколько страниц, повтор cursor, отмена и ограничение retry; частичный результат не выдаётся за полный |
| WA-D133 | Повтор sync без дубликатов; одинаковые WB IDs разных кабинетов не смешиваются; чужой product нельзя сопоставить |
| WA-D134–135 | Сбой в середине загрузки/перезапуск; checkpoint, last success и поколения данных согласованы; partial не обнуляет остатки |
| WA-D136 | Производственные таблицы до/после импорта неизменны; нет автоматической задачи/проводки |
| WA-D137–138 | External text не разрешает действия; audit безопасен и не расширяет права пользователя |
| WA-D139–140 | Реестр соответствует выбранным контрактам; статусы результата подкреплены конкретными проверками |

Тестировать HTTP-заглушками и временной БД, не рабочими secrets/data. Каждая запись
«пройдено» получает команду, дату и результат/путь артефакта. Повторять проверки
после относящихся к ним изменений или при новом риске, не ради количества запусков.

## Документационная проверка D0

- Проверена совместимость обозначений с README, Identity & Access, Task-only и Day15.
- До добавления максимальный ID WA-D126; новые определения WA-D127–WA-D140 уникальны.
- Проверены локальные ссылки, diff formatting и состав изменённых файлов.
- Go-тесты/сборка не запускались: этот этап изменяет только Markdown.
- Реальные WB/Telegram запросы не выполнялись; env/рабочая БД не читались.

## Что потребуется перед I6

Документировать локальное задание WB_API_TOKEN, привязку кабинета, permissions,
действия Telegram, применение замены токена, meaning freshness/partial,
команды тестов и отдельной сборки. Настоящую .env не включать в примеры.
Называть оставшиеся ограничения и V1 явно; не объявлять всю интеграцию готовой
по результату одного happy-path mock-теста.

## Доказательства локальной приёмки 2026-09-22

Команды из C:\TEMP\WorkshopAgent:

```powershell
& 'C:/Program Files/Go/bin/go.exe' test ./...
& 'C:/Program Files/Go/bin/go.exe' build -o bin/workshop-agent-wb-20260922-check.exe ./cmd/workshop-agent
git diff --check
git check-ignore --no-index .env
```

Полный go test ./...: exit 0; все пакеты пройдены. Последний прогон после изменений
миграции/таймаутов: marketplace 9.303s, wildberries 0.527s, storage 10.352s,
telegram 59.374s. Сборка: exit 0, бинарник не запускался.
SHA256: 063F44F77BD2FF37440B47180EA685A1D9A7D39DC676FFDAB25B6CF0D892F025.

Новые 20 тестовых функций (плюс подслучаи):

- [service_test.go](../../internal/marketplace/service_test.go): OWNER/чужой кабинет/
  смена мастерской/отозванное membership; идемпотентность; mapping; foreign keys;
  неизменность снимка восьми производственных таблиц; partial сохраняет предыдущий
  снимок/last_success; отключение во время сети и запоздалый ответ; смена sid;
  безопасные ошибки/SQLite/audit; повтор миграции и recovery прерванной работы.
- [client_test.go](../../internal/marketplace/wildberries/client_test.go): 401/403/
  429/5xx, три попытки, Retry-After и deadline; URL allowlist/redirect/TLS;
  тело больше 8 MiB; токен в ошибке/response/escaped JSON/formatting;
  отсутствующая конфигурация; курсор карточек и offset WB stocks, повтор страницы;
  chrtIds/неполные остатки, ID/status validation; guard после ожидания; timeout.
- [marketplace_test.go](../../internal/telegram/marketplace_test.go): явное
  подтверждение, stale кнопка после переключения, отключение, secret ingress,
  отсутствие команд WB/секрета в памяти агента и fake Telegram payload.

Дополнено [руководство](USAGE.md), сохранён MIT notice upstream.
Настоящий .env/рабочая БД не читались и не изменялись; реальные WB/Telegram
запросы не выполнялись. HTTP тестируется через fake RoundTripper, без сервера.
Запрашивалась только публичная документация WB. Go запускался вне sandbox из-за
отсутствия доступа к стандартной библиотеке/кешу; это не запуск приложения.

Общий интеграционный допуск остаётся открытым: завершить D1, затем отдельно
авторизованный V1. В V1 проверить реальные категории/тип/срок токена, нулевые
остатки, большие/пустые страницы, sid при замене токена и реальные Telegram-кнопки.
