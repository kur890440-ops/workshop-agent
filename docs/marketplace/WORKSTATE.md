# Marketplace — оперативное состояние

Обновлено: 2026-09-23. Правила продолжения — [карта](README.md).

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
