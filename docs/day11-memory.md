# День 11 — модель памяти агента

Реализованы три слоя памяти с отдельным хранением, lifecycle и scopes. Текущие User, Workshop, WorkshopMembership, активная мастерская и AuthorizationService переиспользованы. Позднее отдельным заданием добавлен [Day 12 — Personalization](day12-personalization.md): USER_PREFERENCE использует прежний user_preferences, а ContextBuilder включает разрешённый профиль отдельным блоком без дублирования в LONG_TERM. Профиль применяется и при отключённом retrieval Long-Term; исторические результаты эксперимента Day 11 сохраняются без изменений.

## До и после

До Day 11 существовали conversation_sessions с last_context/pending_action, временные Telegram-формы и user_preferences. История сообщений не подавалась LLM; устойчивой рабочей задачи и ContextBuilder не было. Domain DB уже хранила реальные остатки, продукты и BOM.

| Слой | Хранение | Scope | Lifecycle |
| --- | --- | --- | --- |
| Short-Term | существующая conversation_sessions + новая conversation_messages | user_id + workshop_id + session_id | Текущий диалог, максимум N сообщений и оценочный token budget. Новая сессия не читает прежний диалог. |
| Working | working_memory | user_id + workshop_id + task_id | Одна active/waiting_input задача. completed/cancelled сохраняется для диагностики и исключается из active context. |
| Personal Long-Term | существующая user_preferences; USER_PROFILE в long_term_memory | user_id | Переживает сессии, задачи и перезапуск. |
| Shared Long-Term | long_term_memory | workshop_id; дополнительно entity_id для продукта | Явные подтверждённые правила/решения, версия, is_active и Audit Log. |
| Domain DB | materials, products, bom_items и существующие доменные таблицы | workshop_id + entity_id | Единственный источник реальных бизнес-фактов. |

Новые таблицы: conversation_messages, working_memory, long_term_memory, memory_traces. Последняя — ограниченная диагностика фактически выбранного контекста; её исторические snapshots не участвуют в retrieval как источник актуальных остатков. Существующая conversation_sessions получила status и уникальность активной сессии user/workshop/chat. Legacy sessions сохранены закрытыми, без автоматического превращения старого текста в достоверную историю. user_preferences получила version/updated_at. pending_actions используется для подтверждения memory/BOM-кандидатов.

## Явные операции и router

`internal/memory` предоставляет AppendShortTerm, CreateWorkingMemory, UpdateWorkingMemory, CompleteWorkingMemory, SaveLongTermMemory, UpdateLongTermMemory, DeactivateLongTermMemory и ForgetPreference. Каждый метод проверяет actor, текущую мастерскую и Membership.

MemoryRouter детерминированно возвращает Candidate: content, target, category, scope, confidence, reason, requires_confirmation и разобранные параметры. Confidence — эвристическая метка, не статистически откалиброванная вероятность.

| Пример | Решение |
| --- | --- |
| «А чёрного?» | SHORT_TERM: ссылка текущего диалога; без предмета в новой сессии — уточнение. |
| «Соберем 20 наборов», «Нет, сделай 25» | WORKING: создание/обновление количества задачи. |
| «Для этой партии используй Box B» | WORKING: временный параметр; постоянные правила не меняются. |
| «Запомни, что мне удобнее, когда итог идет первым» | LONG_TERM_PERSONAL после подтверждения; user_preferences.summary_first. |
| «Запомни для этой мастерской: проверять вес каждой катушки» | LONG_TERM_WORKSHOP / PROCESS_RULE после подтверждения и проверки workshop.settings.manage. |
| «Теперь всегда клади в этот комплект 3 кисти» | DOMAIN_OPERATION: Products.SetBOMQuantity после подтверждения и проверки bom.write. |
| «На складе 12 кг PETG» | DOMAIN_OPERATION; не запись в Long-Term. |
| Явный секрет/invite token | DO_NOT_STORE. |

Вопрос о параметре партии не считается указанием изменить этот параметр. В долговременную память не попадает каждое сообщение. Даже прямой persistent write API отклоняет распознанные складские/BOM/production-факты. Текст сообщения может присутствовать в краткой истории диалога; это не делает его доменной истиной.

Long-Term поддерживает USER_PREFERENCE, USER_PROFILE, WORKSHOP_DECISION, WORKSHOP_KNOWLEDGE, PROCESS_RULE, PRODUCT_KNOWLEDGE. Personal preferences остаются в прежней user_preferences, без второй копии в long_term_memory. В текущем NL-интерфейсе реализованы основные шаблоны личных предпочтений и общих правил; остальные категории доступны через typed service API.

Persistent updates увеличивают version; предыдущие и новые значения, actor, scope, source и время доступны в существующем audit_logs. События: MEMORY_PREFERENCE_SAVED, MEMORY_LONG_TERM_SAVED, MEMORY_DEACTIVATED, MEMORY_PREFERENCE_REMOVED. BOM пишет собственное BOM_QUANTITY_CHANGED. Данные и audit коммитятся в одной транзакции.

## ContextBuilder и источник истины

AgentContextBuilder собирает IDENTITY → relevant LONG_TERM → active WORKING → relevant DOMAIN → bounded SHORT_TERM → CURRENT. Каждый item хранит layer/source/key и оценку токенов. Полный prompt и trace доступны в эксперименте; рабочий бот сохраняет последние 20 traces пользователя/мастерской.

Retrieval сначала фильтрует SQL по user/workshop scope, category/task_type, product/entity и ключу запроса, затем применяет LIMIT 8. Дополнительно выбираются только поддерживаемые ключи личных preferences. Все долговременные записи в prompt не передаются. При превышении общего бюджета 6000 оценочных токенов сначала исключаются старые сообщения, затем менее приоритетные знания; обязательные факты и текущий запрос молча не обрезаются.

Параметры Short-Term: SHORT_TERM_MAX_MESSAGES=20, SHORT_TERM_MAX_TOKENS=1600. Оценка токенов — ceil(UTF-8 bytes/3), с явной маркировкой. Это не tokenizer MiniMax и не provider usage.

LLM остаётся интерпретатором команд. Фактические остатки и расчёт BOM возвращаются детерминированными сервисами. Working quantity участвует в расчёте потребности по текущей BOM. Постоянный состав не копируется в память. Memory outage приводит к явному отказу/уточнению либо доступному доменному пути, без выдумывания данных; сами domain services продолжают работать независимо.

## Изоляция

- User в Scope должен совпадать с привязанным actor; нельзя подставить другого владельца personal memory.
- Workshop должна совпадать с текущей активной мастерской и иметь действующую Membership.
- Short-Term проверяет session_id вместе с user/workshop; Working — task_id вместе с user/workshop.
- Shared knowledge доступна только текущей мастерской; product memory дополнительно проверяет принадлежность продукта.
- Запись общего знания требует workshop.settings.manage; employee/viewer не могут обойти это через память.
- Предложение подтверждения связано с user/workshop/chat/session, действует 15 минут. Новая сессия не подтверждает старое предложение.
- Memory/dialogue передаются модели как недоверенные данные; разрешения и domain writes никогда не определяются текстом памяти.

## Telegram и CLI

```text
/memory                 краткая сводка слоёв
/memory short           сообщения текущего диалога
/memory working         текущая задача и расчёт
/memory long            релевантные постоянные записи
/memory trace           provenance последнего обычного запроса
/memory confirm         подтвердить последнее предложение текущей сессии
/memory cancel          отменить предложение
/memory forget <key>    предложить удаление личного preference
/memory forget <id>     предложить деактивацию long_term_memory record
/session new            закрыть диалог и открыть новый
/task                   показать активную задачу
/task complete          завершить задачу
/task cancel            отменить задачу
```

Новая сессия не завершает рабочую задачу: lifecycle разделены. Чтобы новая задача не наследовала предыдущие параметры, сначала завершите/отмените прежнюю. Команды памяти доступны и во время Telegram-форм; они не воспринимаются как название нового материала.

Существующий CLI расширен без нового framework:

```powershell
go run ./cmd/workshop-agent memory show 1 6
go run ./cmd/workshop-agent memory trace 1 6
go run ./cmd/workshop-agent memory working 1 6
go run ./cmd/workshop-agent memory list 1 6
go run ./cmd/workshop-agent memory long-term 1 6 <session_id> входной контроль
go run ./cmd/workshop-agent day11-memory-report
go run ./cmd/workshop-agent day11-memory-render reports/day11-memory/<run-id>
```

Диагностика требует уже существующей активной session, либо её явного ID. CLI — локальный операторский инструмент с доступом к SQLite. `day11-memory-render` только обновляет HTML по сохранённым results.json, без LLM-вызовов. Неизвестные CLI-команды отвергаются и не запускают Telegram polling.

## Эксперимент

[HTML-отчёт](../reports/day11-memory/20260916T174744.744562000Z/report.html) · [Полные результаты](../reports/day11-memory/20260916T174744.744562000Z/results.json) · [Сверка данных](../reports/day11-memory/20260916T174744.744562000Z/validation.json).

Модель MiniMax/MiniMax-M3, temperature=0, max_tokens=512. 6 сценариев × A/B/C/D = 24 реальных запроса на синтетических данных. Внутри сценария identity, Domain snapshot и текущий вопрос совпадают; меняются только включённые memory layers. Провайдер получает каждый prompt без скрытой прошлой истории.

| Case | Слои | Строгая проверка | Input tokens | Output tokens |
| --- | --- | --- | --- | --- |
| A | без memory | 2/6 | 2736 | 1505 |
| B | Short-Term | 2/6 | 2917 | 1409 |
| C | Short-Term + Working | 4/6 | 3146 | 1289 |
| D | все слои | 5/6 | 3338 | 1255 |

Всего provider usage: 12137 input + 5458 output = 17595 tokens. Оценки токенов каждого слоя раскрыты в HTML/JSON trace; точное разделение provider tokens по слоям API не возвращает.

Цвет модель определила во всех вариантах — в одинаковом Domain snapshot было только два цвета PETG. Поэтому именно этот live-сценарий не доказывает преимущество Short-Term; сброс ссылки при новой session проверен отдельно на рабочем агенте. Количество и временная упаковка корректны в C/D; общая контрольная процедура использована в D. Актуальные 8.4 кг не были заменены старым упоминанием 10 кг.

В сценарии preference D вынес сводку «Кратко: … 12,6 кг» перед таблицей. Строгая эвристика ожидала слово «Итог» в самом начале и не засчитала результат. Это ложное отрицание отмечено вручную; исходные ответы и scores не исправлялись задним числом. Один прогон не является статистическим доказательством качества; эвристики ошибок/уточнений ограничены.

## Проверки и миграция

Добавлены 21 тест Day 11 к 20 существующим: отдельные таблицы; Short-Term retention/clipping; user/workshop/session/task/product isolation; working updates/completion; новая session; реальное переоткрытие БД; отсутствие автоматического promotion; актуальный Inventory; domain BOM update; права; audit/version/deactivation; relevant retrieval; provenance; запрет stale confirmation; fail-safe при недоступности памяти. Многошаговые agent tests проверяют диалоги из задания. Проект на Go: `go test ./...`; pytest не используется.

Миграция 101 применена к локальной базе с предварительной согласованной SQLite backup. Сохранены все прежние Users, Workshops, Memberships и domain records. Сверены 19 материалов, 34 продукта, 34 BOM-строки и остальные domain-таблицы; integrity_check=ok, foreign_key_check пустой. Учебные данные находятся только в отдельных scenario-*.db каталога отчёта.

Ограничения: deterministic NL-router распознаёт заданные семейства фраз, а не произвольный язык; vector/semantic retrieval не добавлялся; старые завершённые задачи и закрытые session metadata сохраняются, глобальная TTL-очистка архива не реализована; процессные/производственные ERP-заготовки не расширялись за рамки нужного изменения BOM. Day 12 — отдельное задание.
