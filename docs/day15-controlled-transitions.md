# Day 15 — контролируемые переходы состояний

## L0 — архитектура и аудит

Используется существующий TaskStateMachine. Новый каталог TransitionDefinition
описывает его входы и допустимые результаты; второй движок состояния не создан.
Терминология Task сохранена. Техническая phase `planning` отображается как
«Подготовка задачи», отдельной сущности производственного плана нет.

До Day 15 фазы и шаги проверялись отдельными ветками Apply; pause/resume уже
сохраняли параметры. Кнопки имели task_id/version, но «Запускай» непосредственно
утверждало параметры. Day 14 блокировал завершение вне validation, однако сам факт
нахождения в validation ещё не означал успешной проверки результата.
Финальная запись phase/status находилась в completion service. Эти пробелы устранены.

Путь: TransitionRequest → загрузка Task → Membership/Permissions/исполнитель →
версия, phase/status/step → InvariantEngine → условия перехода → подтверждение →
изменение FSM → проверка назначения по таблице → атомарная запись и история.
Складская проводка и финальный переход остаются в одной транзакции.

## L1 — модель

- Task хранится в прежней working_memory. Phase и status независимы.
- TransitionDefinition: key, label, from_phase/step/status, to_phase/step/status,
  required_permissions, requires_confirmation, preconditions, invariants.
  `*` в назначении означает сохранение значения. При сохранении FSM проверяет
  соответствие результата таблице, включая статус.
- TransitionRequest: task_id, actor_user_id, transition, source, payload.
  Проверяется соответствие авторизованному Scope. UI/LLM не передают новый phase.
- TaskState: parameters_approved, validation_result, validation_reason дополняют
  прежние товар, количество и produced_quantity. Разговор не является доказательством
  утверждения или успешной проверки.
- TransitionDenied: allowed=false, reason_code, task_id, current_phase/step/status,
  requested_transition, allowed_transitions, reason, state_changed=false.
- task_transitions сохраняет ключ перехода в action, actor, фазы, шаги, статусы,
  timestamp. metadata_json содержит source, result=APPLIED, reason, transition_key.
  Структурированные отказы пишутся в TASK_TRANSITION_REJECTED после отката.
  Диагностика не изменяет Task или склад. Отказы инвариантов также доступны в
  прежнем /invariant_trace.

## L2 — решения

- **WA-D120:** каталог переходов общий для FSM и доступных кнопок. Нельзя назначить
  произвольный phase. Версия и состояние повторно проверяются внутри транзакции.
- **WA-D121:** confirm_task — утверждение параметров. «Запускай» запрашивает явное
  подтверждение; данные и состав показываются перед кнопкой. Само сообщение
  не переводит в execution. `/task confirm_task <task_id>` остаётся явной командой
  подтверждения; при отсутствии ID используется однозначно выбранная текущая задача.
- **WA-D122:** record_result требует числового результата и открывает validation.
  verify_result требует подтверждения и записывает validation_result=passed.
  Текст «проверка пройдена» предлагает подтверждение. production_posted завершает
  задачу только после проверки и подтверждения расхода. Нельзя завершить из execution
  либо из validation без passed. Это уточняет прежние сценарии завершения Day 14.
- **WA-D123:** correct_result возвращает в execution/record_result, очищает прежний
  результат и сохраняет причину возврата. Пустая причина отклоняется.
- **WA-D124:** pause/resume меняют lifecycle status, сохраняя phase, current_step,
  expected_action и task_data. DONE, cancelled и failed терминальны. Reopen не добавлен.
- **WA-D125:** migration 109 восстанавливает parameters_approved только при наличии
  сохранённого confirm_task/исторического эквивалента в task_transitions.
  Успешная проверка восстанавливается при соответствующем verify_result и шаге
  confirm_completion. Фаза execution сама по себе не подтверждает параметры.
  Незавершённые разрешения выпуска истекают. Перед миграцией файловой БД создаётся
  резервная копия. Миграция идемпотентна, не создаёт задач или складских движений.
- **WA-D126:** запись phase/status/task_data централизована в FSM, включая финальный
  production_posted. Day 11 API делегирует ограниченным адаптерам FSM для версии 0;
  их legacy_update/complete/cancel также описаны таблицей. legacy_complete разрешён
  только для личных задач старого формата, не для production/assembly.
  Назначение исполнителя остаётся в сервисе назначений: оно не меняет phase/status.

Основные переходы существующего workflow:

| Переход | До | После | Условия |
|---|---|---|---|
| select_product | preparation/select_product | preparation/set_quantity | товар текущей Workshop |
| set_quantity | preparation/set_quantity или confirm_task | preparation/confirm_task | положительное целое |
| edit_parameters | preparation/confirm_task | preparation/select_product | сброс утверждения |
| confirm_task | preparation/confirm_task | execution/start_production | товар, количество, явное подтверждение |
| start_production | execution/start_production | execution/record_result | параметры утверждены, подтверждение, настроенные инварианты |
| record_result | execution/record_result | validation/verify_result | фактический результат задан |
| verify_result | validation/verify_result | validation/confirm_completion | явное подтверждение проверки |
| correct_result | validation | execution/record_result | причина доработки |
| production_posted | validation/confirm_completion | done/completed | passed, разрешённый расчёт и подтверждённая проводка |
| pause / resume | незавершённая фаза | та же фаза и шаг | active ↔ paused, нет конфликта текущей задачи |
| cancel / fail | незавершённая фаза | терминальный status | права управления |

`preparation` в таблице — пользовательское название хранимой phase `planning`.
Смысл примера approve_plan из задания реализован confirm_task; execution_completed —
record_result. Успешная проверка и финальный выпуск разделены, чтобы сохранить явное
подтверждение списания материалов, реализованное ранее.

## L3 — интерфейс, диагностика, проверки

«Что я сейчас могу сделать?» / `/task actions` строят ответ из допустимых переходов
и прав текущего пользователя. `/task trace [task_id]` показывает текущее состояние,
доступные переходы, историю с источниками и последний структурированный отказ.
Большие ответы разбиваются по ограничениям Telegram.

Пример: `/task quantity 5` → «Запускай» → просмотр товара, количества и состава →
«✅ Подтвердить текущий шаг» → execution/start_production → ещё одно явное начало →
`/task result 5` → «✅ Подтвердить текущий шаг» → `/task complete` → фактическое
количество → расчёт расхода → «Завершить и списать».
Возврат: `/task correct_result <причина> [task_id]`.

В подготовке нет кнопки завершения. Устаревшие и повторные подтверждения не выполняют
переход повторно. Произвольное «да» не заменяет подтверждение конкретной задачи.
LLM может распознать намерение, но не меняет правила и не устанавливает поля состояния.

Проверки A–P: TestDay15ControlledLifecycle, TestDay15NoForgedTransitionIdentity,
TestDay15TelegramConfirmationAndAllowedActions, TestDay15MigrationUsesHistoryNotPhase.
Существующие тесты дополнительно проверяют перезапуск отдельного OS-процесса,
конкурентное завершение, откаты склада, изоляцию пользователей/мастерских и историю.
Telegram в тестах использует поддельный транспорт; реальные API-запросы не нужны.

Локальный воспроизводимый отчёт: `bin\workshop-agent.exe day15-transitions-report`.
Создаёт отдельную fixture.db, HTML и JSON со сценариями A–P, таблицей, историей и trace
в `reports/day15-controlled-transitions/<UTC>/`. Рабочая БД не используется.

Проверка проекта: `go test ./...`; сборка:
`go build -o bin/workshop-agent.exe ./cmd/workshop-agent`.

Локальные отчёты Day 13 и Day 14 после расширения также проходят. Для Day 15
отчёт проверяет 34 условия: сценарии A–P и промежуточные состояния/повторные действия.

Проверено 2026-09-18: полный `go test ./...` — PASS, сборка exe — PASS.
[HTML-отчёт: 34/34](../reports/day15-controlled-transitions/20260918T143934.825413300Z/report.html) ·
[Данные, история и trace](../reports/day15-controlled-transitions/20260918T143934.825413300Z/results.json).

Для применения: **Ctrl+C → `.\bin\workshop-agent.exe`**.
Миграция рабочей БД выполняется при запуске новой версии; старые кнопки нужно открыть заново.
