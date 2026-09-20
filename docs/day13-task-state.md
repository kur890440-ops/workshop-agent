# День 13 — состояние задачи

Текущее завершение со складским проведением описано в
[TASK_COMPLETION_AND_MATERIAL_WRITEOFF](task-completion-posting.md), WA-D093–WA-D101.
Правило «completed без выпуска и движений» ниже сохраняется только как история Day 13.

Обновление модели доступа: [WORKSHOP_SHARED_TASKS](workshop-shared-tasks.md),
WA-D086–WA-D092, заменяет персональную видимость рабочих задач общим списком Workshop
с отдельным исполнителем. Правило текущей задачи применяется к исполнителю.
Остальной текст сохраняет историю решений Day 13.

@PROJECT:WORKSHOP_AGENT @L3:DAY13_TASK_STATE_MACHINE @UPDATE @NO_COMPRESS

## L0 — Architecture

Identity → active Workshop → Membership / Authorization → Telegram / intent
→ TaskStateMachine → existing Working Memory + transition history → SQLite.
AgentContextBuilder получает текущий снимок задачи; Day 12 меняет оформление,
но не разрешённые переходы. Day 14 не выполняется.

До Day 13 working_memory содержала task_id, task_type, state_json, status,
created_at, updated_at, completed_at. Состояния active/waiting_input/completed/
cancelled не описывали этапы и шаги. /task показывал задачу, complete/cancel закрывали
его напрямую. Рабочая память уже сохранялась между сессиями и рестартами.

## L1 — Entities, persistence, ownership

Переиспользована **та же working_memory**, без второй таблицы задач:

| Группа | Поля |
|---|---|
| Identity/scope | task_id, user_id, workshop_id, task_type |
| Данные задачи | state_json: product_id, product_name, quantity, parameters, open_questions, produced_quantity |
| FSM | phase, current_step, expected_action, expected_action_type |
| Lifecycle | status; created_at, updated_at, started_at, paused_at, completed_at |
| Concurrency/compatibility | version, fsm_version |

Переходы хранятся в task_transitions: task_id, actor_user_id, from/to phase/step/
status, action, metadata_json, created_at. Текущее состояние, событие и Audit Log
изменяются одной транзакцией. Ошибка записи истории откатывает переход.

Working Memory отвечает «какие параметры собраны». FSM отвечает «где процесс и
что ожидается». Short-Term — последние реплики. Данные склада и фактического
производства остаются в Domain DB. produced_quantity в задаче — введённый
пользователем результат для проверки, а не автоматически проведённый выпуск.

## L2 — Decisions

- **WA-D071**: migration 104 расширяет working_memory, перестраивая CHECK статуса
  в SQLite; сохраняет существующие ID, JSON, даты, статусы. Старые задачи имеют
  fsm_version=0, новые production — 1. Миграция идемпотентна, проверена тестом.
- **WA-D072**: основные phases planning → execution → validation → done.
  Статусы новых задач active, paused, completed, cancelled, failed. waiting_input
  оставлен только для обратной совместимости legacy. DONE терминален.
- **WA-D073**: шаги и expected actions — стабильные identifiers, задаваемые кодом.
  Один полноценный учебный workflow production; другой workflow требует
  отдельного явно проверяемого определения в TaskStateMachine, не свободного JSON.
- **WA-D074**: пауза меняет lifecycle и timestamps/version, сохраняя phase, step,
  expected_action и task_data. Resume не реконструирует задачу из разговора.
- **WA-D075**: одна foreground-задача на user + workshop, обеспеченная уникальным
  partial index; paused-задач может быть несколько. При неоднозначности нужен ID.
  Resume при наличии другой active-задачи отклоняется.
- **WA-D076**: все изменения проходят проверку владельца задачи, активной Workshop,
  Membership и TasksCreate / TasksExecute; товар проверяется в текущей Workshop. LLM выдаёт
  intent, а не поля phase/status. Legacy update/complete не могут изменить FSM v1.
- **WA-D077**: кнопка содержит task_id/version и привязана существующим nonce к
  пользователю, чату и Workshop. Устаревшая версия отклоняется; повторный callback
  не продвигает задачу на ещё один шаг. Изменения фиксируются под write lock.
- **WA-D078 (заменено WA-D093)**: исходное учебное завершение не проводило выпуск.
  Сейчас задача завершается только через подтверждённую атомарную проводку выпуска
  и списания компонентов. Простой Apply(complete) возвращает требование проводки.

### Transition table

| До | Intent / обязательные данные | После |
|---|---|---|
| planning/select_product | select_product, товар текущей Workshop | planning/set_quantity |
| planning/set_quantity или confirm_task | set_quantity, целое 1…1e9 | planning/confirm_task |
| planning/confirm_task | confirm_task, товар и количество заданы | execution/start_production |
| execution/start_production | start_production | execution/record_result |
| execution/record_result | record_result, целое 0…1e9 | validation/verify_result |
| validation/verify_result | verify_result, результат задан | validation/confirm_completion |
| validation/verify_result или confirm_completion | production_posted, подтверждены результат и расход | done/completed |
| validation/* | correct_result | execution/record_result, прежний результат очищен |
| незавершённая active | pause | тот же phase/step, paused |
| paused, нет другой active | resume | тот же phase/step, active |
| active/paused | cancel или fail | cancelled / failed |

Пропуск обязательных шагов, завершение из planning, возобновление completed/
cancelled/failed и устаревшая версия возвращают ошибку. Интерфейс не предлагает
произвольную установку phase. Fail предусмотрен сервисом, отдельной кнопки нет.

## L3 — Telegram and context

Начать: **/task new** или «Нужно произвести 20 <точное название товара>» / «Сделаем
20 <название>». Неоднозначное/неполное имя не угадывается: предлагаются товары с ID.
Выбор: `/task product <ID>`. Количество: `/task quantity 25`, «Нет, 25» или кнопка
подтверждения предложенного количества.

Далее: `/task confirm_task` → `/task start_production` →
`/task result 25` → `/task verify_result` («Всё правильно») → `/task complete`.
Кнопка «✅ Подтвердить текущий шаг» выполняет только ожидаемый переход.
Для исправления результата: `/task correct_result`.

После Day 15 фраза «Запускай» предлагает кнопку подтверждения, а сама не меняет этап.
Возврат на доработку требует причины: `/task correct_result <причина> [task_id]`.
Завершение требует отдельно подтверждённой успешной проверки результата.
[Актуальные правила переходов](day15-controlled-transitions.md).

`/task`, «на чём мы остановились?», «что сейчас нужно от меня?» показывают этап,
шаг, expected action, параметры, статус. Подробный профиль добавляет диагностику;
brief не скрывает обязательные данные и ограничения. Кнопка «📋 Текущая задача»
добавлена в /workshop. Существующие материалы/товары не создают задач.

Пауза: `/task pause`, «поставь задачу на паузу», «поставь пока на паузу» или кнопка.
Продолжение: `/task resume [task_id]`, «продолжим», «продолжим задачу» или кнопка
конкретной paused-задачи. Отмена: `/task cancel [task_id]` или кнопка выбранной задачи.
При нескольких paused выводятся список и кнопки, ID не подбирается моделью.
`/task trace [task_id]` показывает снимок и историю, включая терминальные задачи.

Точные фразы обрабатываются локально. Semantic interpreter дополнен task_pause,
task_resume, task_complete без полей state. Свободные формулировки проходят тот же
сервис. Формы добавления материалов сохраняют приоритет над обычными фразами.

В ContextBuilder добавлен компактный **[ACTIVE TASK]** с ID, type, phase, step,
expected action/type, status и данными. Полная история переходов туда не включается.
Paused-задача не становится foreground до resume. /session new и очистка диалога
не удаляют working_memory; после рестарта старые кнопки надо заменить новым /task.

### Проверки и отчёт

- Основной flow, паузы на трёх этапах, сохранение параметров, ожидаемое действие.
- Новая session и восстановление в **отдельном дочернем OS-процессе**.
- Запрет пропуска шагов, терминальные completed/cancelled/failed, устаревшая версия.
- Изоляция пользователей, активной Workshop, проверка permissions.
- Одна foreground-задача, несколько paused, явный выбор.
- Откат при ошибке transition history; миграция legacy без потери данных.
- Profile brief/detailed не меняет переходы; старые Telegram-сценарии сохранены.
- Автотесты используют временные БД и fake Telegram transport.

[HTML-отчёт](../reports/day13-task-state/20260918T125116.721198500Z/report.html) ·
[Снимки и проверки](../reports/day13-task-state/20260918T125116.721198500Z/results.json).
Отчёт показывает реальные вызовы обработчика, pause/resume, закрытие/повторное
открытие сервисов и новую сессию. Все три проверки отчёта true. Рабочая БД не
менялась. Отдельный process restart проверяется integration test, не HTML-командой.

Повторить отчёт: `.\bin\workshop-agent.exe day13-task-state-report`.
Команда локальная, без LLM и Telegram API. HTML автономный, responsive, print-friendly.

Верификация: `go test ./...`; сборка `bin/workshop-agent.exe`. Pytest неприменим:
проект на Go, Python suite отсутствует. Миграция рабочей базы выполнится при запуске
обновлённого бота; старые задачи не переводятся автоматически в новый workflow.

Для применения изменений: **Ctrl+C → `.\bin\workshop-agent.exe`**.

### Исправление совместимости старых задач

Старые assembly/production тоже являются текущими рабочими задачами.
Просмотр явно показывает их статус и предлагает паузу. При явной pause/resume/cancel
сервис переводит такую задачу в FSM v1 в той же транзакции, сохраняя task_id, тип,
товар, количество и остальные параметры. Этап — planning: confirm_task при наличии
товара и целого положительного количества, иначе соответствующий шаг ввода.
Выполнение не предполагается автоматически. Это уточняет прежнюю границу legacy:
при чтении и миграции БД формат не меняется, при явном управлении меняется.
«Что в работе?» и «Сделай паузу» обрабатываются локально. Проверена старая задача
на 5 сливов, пауза и продолжение после новой сессии, без дубликата задачи и выпуска.
