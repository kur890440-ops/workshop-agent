# День 14 — инварианты и ограничения состояния

## Обследование перед изменениями

- CURRENT HARD CONSTRAINTS: tenant isolation, неотрицательные остатки, атомарное проведение выпуска, уникальная производственная запись на задачу, защита последнего OWNER.
- CURRENT BUSINESS RULES: BOM определяет расход, фактический выпуск подтверждается, повторное завершение возвращает квитанцию без повторного списания.
- CURRENT AUTHORIZATION RULES: активные User/Workshop/Membership и Permissions проверяются сервисами; UI дополнительно проверяет текущую мастерскую и владельца кнопки.
- CURRENT TASK STATE RULES: Day13 FSM хранит phase/step/status/version. Ранее WA-D094 разрешал проведение из execution/record_result; Day14 отменяет этот shortcut.
- CURRENT STACK CONSTRAINTS: Go, SQLite, Telegram. LLM только интерпретирует команды из разрешённой схемы.
- CURRENT ARCHITECTURE DECISIONS: сервисы владеют бизнес-логикой; личная память и профиль не являются источником разрешений; общие задачи мастерской имеют автора и исполнителя.
- Повторно используемые механизмы: auth.Require, TaskStateMachine, CalculateProductionTx/PostProductionTx, транзакционное изменение остатков, действующие nonce-кнопки, user_preferences, long_term_memory, Audit Log.
- MIGRATIONS REQUIRED: 107 — invariant_settings и invariant_traces. Данные мастерской не переносятся и не удаляются.

## L0 · Архитектура

Identity → Workshop context → Authorization → ProposedAction → InvariantEngine → DomainService → deterministic logic → SQLite.

LLM, профиль и память не могут изменить обязательные правила. Они могут влиять на интерпретацию и оформление ответа. Protected rules заданы кодом и не редактируются через Telegram. Доменная авторизация и FSM сохраняют окончательную проверку; engine не заменяет их.

## L1 · Модель

`internal/invariants` предоставляет InvariantRegistry, InvariantEngine, Rule, ProposedAction, Facts, Result и Denied.

Rule: id, key, title, description, category, scope_type, scope_id, severity, enforcement, is_active, version, created_at, updated_at, modifiable, actions.

Категории: ARCHITECTURE, TECHNICAL_DECISION, STACK_CONSTRAINT, BUSINESS_RULE, SECURITY_RULE, DATA_INTEGRITY_RULE.

ScopeContext/Applies поддерживает SYSTEM, WORKSHOP, MODULE, PROCESS, PRODUCT, ROLE. Сейчас реальные правила зарегистрированы в SYSTEM и WORKSHOP; произвольное создание правил других scope из Telegram не предоставляется. Область мастерской читается по аутентифицированному workshop_id.

Registry предоставляет System, Workshop, ForAction, ForTask. Настройки хранят только изменяемые значения, версию и timestamps; описания и исполнительная логика восстанавливаются из доверенного кода. Ни текст диалога, ни JSON модели не становятся исполняемыми предикатами.

Facts поступают от сервисов: текущая phase, рассчитанный внутри транзакции остаток, состояние формы подтверждения, результат расчёта материалов. ProposedAction не даёт права на SQL или обход сервисов.

Result: allowed, decision ALLOW/DENY/WARN, action, applicable, passed, violations, warnings, suggested_alternative. Trace содержит факты проверок, без скрытых рассуждений.

## L2 · Решения

- WA-D102: инварианты существуют независимо от сессий, очистки чата и профиля. Системные правила — код, настройка мастерской — SQLite.
- WA-D103: completion требует фактическую phase=validation. Это заменяет разрешение execution/record_result в WA-D094. Отказ не переводит задачу в validation автоматически.
- WA-D104: mandatory Membership/Permissions, tenant isolation, последний OWNER, FSM и неотрицательный склад нельзя отключить OWNER-командой или памятью.
- WA-D105: requires_material_check_before_production — отдельная дополнительная проверка перед start_production; по умолчанию выключена для совместимости. OWNER/ADMIN меняет её только отдельным подтверждением с optimistic version и аудитом. Отключение не отменяет проверку склада при выпуске.
- WA-D106: HARD блокирует, SOFT предупреждает независимо от severity. stock_depleted — SOFT/WARNING; установка нулевого остатка разрешена.
- WA-D107: архитектурные запросы об обходе сервиса/замене SQLite локально преобразуются в ProposedAction и отклоняются с разрешённой альтернативой. Это ограниченный маршрутизатор, не универсальный анализатор произвольных советов.
- WA-D108: в контекст текущей задачи добавляются релевантные ACTIVE_CONSTRAINTS. Исполнение не зависит от того, прочитала ли их LLM.

## L3 · Использование и проверка

- `/invariants` — правила текущей мастерской, включая состояние настройки.
- `/invariants requires_material_check_before_production on` или `off` — запрос изменения, затем кнопка подтверждения. `/setup_stop` отменяет кнопку. Смена Workshop, истечение nonce и повторное нажатие защищены общим UI.
- `/invariant_trace` — последняя сохранённая проверка архитектурного запроса или отказа завершения этого пользователя в этой мастерской. Это не полный журнал всех доменных вызовов; прочие результаты engine доступны вызывающему сервису и отчёту.
- В execution команда «завершить задачу» отказывает. Сначала `/task result 5`, затем проверка расчёта и явное «Завершить и списать». FSM, доступ, версия задачи и остатки проверяются повторно при проведении.
- `bin\workshop-agent.exe day14-invariants-report` создаёт отдельную fixture.db, results.json и автономный report.html в reports/day14-invariants/<run-id>/. Рабочая БД и Telegram не используются.

Проверки Go: реальное списание и отказ при дефиците, отказ execution без изменения task/version, повторный запрос, профиль и память, разрешённый выпуск после validation, preflight, запрещённое изменение BOM для EMPLOYEE, конфигурация с подтверждением/версией/аудитом, защищённые правила, изоляция мастерских, очистка чата, новая сессия, запуск отдельного процесса на сохранённой тестовой БД, UI-отмена, WARN и scopes.

Python-кода и pytest suite в этом задании нет: применяются `go test ./...` и `go build -o bin\workshop-agent.exe ./cmd/workshop-agent`. Проверка report runner запускается отдельно после сборки.

Ограничения: не предоставляется универсальный язык политик и редактор произвольных scope; прямой доступ администратора к SQLite вне приложения не контролируется engine; существующие legacy-сервисы сохраняют собственные проверки, а не переводятся полностью на новый engine. SOFT предупреждение возвращается в Result и демонстрируется в тестах, текущий складской UI не показывает его отдельным сообщением.

После обновления требуется перезапуск: `Ctrl+C` → `.\bin\workshop-agent.exe`.
Day15 не входит в эту задачу.

## Результаты проверки 2026-09-17

- `go test ./...` — PASS. После финального уточнения императива «Заверши задачу» и scope-фильтра повторные тесты Day14 в invariants/memory/telegram и тесты фраз завершения — PASS.
- `go build -o bin\workshop-agent.exe ./cmd/workshop-agent` — PASS, итоговый exe пересобран.
- `day14-invariants-report` — PASS, 11 исполненных сценариев. Итоговый отчёт: `reports/day14-invariants/20260917T200159.589354700Z/report.html`, данные — соседний `results.json`, 13 разделов HTML.
- pytest — неприменим: Python-тестов и конфигурации pytest в проекте нет; Python-пакеты не устанавливались.
- Рабочий Telegram-чат не использовался, рабочая БД не изменялась экспериментом. Миграция 107 применится при запуске новой сборки.
