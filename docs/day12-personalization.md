# Day 12 — Personalization

@PROJECT:WORKSHOP_AGENT @UPDATE @NO_COMPRESS

## L0 — Architecture

Identity → Workshop context → Membership / Authorization → Telegram UI →
AgentContextBuilder (Day 11 memory + resolved UserProfile + current request + domain facts)
→ LLM interpreter or read-only report → deterministic services → SQLite.

Personalization определяет форму ответа. Данные склада, BOM, производства и permissions
остаются в существующих сервисах. Профиль не заменяет Identity и не принадлежит Workshop.
Day 13 не реализуется.

## L1 — Entities and ownership

`UserProfile` — типизированное представление существующей строки
`user_preferences(user_id, settings_json, version, updated_at)`. Новая таблица не нужна.
`user_id` — внутренний идентификатор User, не Telegram ID. Новому пользователю создаётся
строка defaults; старый получает её при первом обращении. Неизвестные старые JSON-ключи
сохраняются при изменениях и сбросе поддержанных настроек.

| Поле профиля | Ключ settings_json | Default / значения |
|---|---|---|
| style | style | neutral; concise, friendly, technical, formal |
| detail_level | response_style | normal; concise → brief, detailed |
| language | language | ru; en можно хранить, генерация пока ru |
| format | response_format | text; list, table |
| summary_first | summary_first | false / true |
| confirmation_level | confirmation_level | confirm_destructive; always_confirm, minimal_confirmation |
| hide_llm_details | hide_llm_details | false / true |
| constraints | фиксированные правила сервиса | подтверждение изменения BOM и производства |

`PersonalizationService` предоставляет GetProfile, UpdatePreference, Reset, ResolveProfile,
SaveTrace и LastTrace. Обновление валидируется по whitelist и атомарно сохраняется
с версией, временем и Audit Log (старое/новое значение, автор, source).
Последний trace доступен только по собственному internal user_id.

Day 11 USER_PREFERENCE пишет в то же хранилище через UpdateTx. Неструктурированные
личные факты остаются в long_term_memory. Уже разрешённые preferences не дублируются
в блоке LONG_TERM. Профиль подключается независимо от ablation-переключателя Long.

## L2 — Decisions

- **WA-D060**: UserProfile — typed view user_preferences; отдельного конкурирующего
  хранилища предпочтений нет. Один ключ имеет одно активное значение.
- **WA-D061**: security / authorization / business rules > current explicit request >
  working-task presentation requirements > profile > personal memory > defaults.
  Требования задачи представлены разрешёнными ключами `presentation.*`.
- **WA-D062**: profile context включается до генерации. Обычный ParseCommand,
  Telegram Interpret и Complete отчётов получают разрешённый профиль. Локальные
  команды и формы не требуют LLM и сохраняют свой детерминированный интерфейс.
- **WA-D063**: временное указание действует на один запрос; явное постоянное
  указание обновляет профиль. Свободный текст не становится настройкой автоматически.
- **WA-D064**: пользовательские preferences не выдают permissions, не меняют scope
  и не отменяют обязательные подтверждения. Уровень подтверждений — рекомендация
  для поведения; always_confirm пока не добавляет кнопки ко всем операциям чтения.
- **WA-D065**: отчёт генерируется одним Complete из проверенных доменных данных,
  без последующего LLM-переписывания. Лимит ответа 2048, finish_reason=length
  считается ошибкой; неполный ответ не признаётся успешным.

Компактный профиль, реально отправленный в эксперименте:

```text
[USER PROFILE]
Style: neutral
Detail level: brief
Format: list; summary_first=true; show_units=true
Language: ru
Interaction preference: confirm_destructive
Constraints: confirm BOM and production mutations; hide_llm_details=false
```

Далее в том же блоке идут приоритеты и запрет менять факты, permissions и schema.
Полные реальные prompts сохранены в results.json и раскрываемых секциях HTML.

## L3 — Implementation and verification

Telegram: `/profile` или «профиль» вне незавершённой формы. Кнопки стиля, подробности,
формата, итога сначала, подтверждений и показа LLM-деталей; сброс с подтверждением.
`/profile trace` показывает сохранённые настройки, current override и applied profile.
Профиль доступен зарегистрированному пользователю без выбранной Workshop.
Кнопки привязаны к user/chat и используют существующую защиту callback.

Примеры постоянных фраз: «Всегда отвечай коротко», «Теперь всегда отвечай подробно»,
«Всегда сначала показывай итог», «Таблицы мне удобнее списков».
Временный пример: «Покажи материалы и в этот раз объясни подробно».
Следующий запрос снова использует сохранённую подробность. Смена сессии, рестарт
и переключение Workshop не меняют personal profile. Правила Workshop не становятся
личными настройками. Язык UI и генерации пока русский.

Проверки: defaults, сохранение/повторное открытие БД, изоляция двух пользователей,
смена Workshop, приоритеты, временные/постоянные изменения, Day 11 bridge,
автоматическое включение в оба интерпретатора, один вызов генерации отчёта,
Telegram UI/trace/reset, скрытие token footer, сохранение авторизации,
обязательные подтверждения, обнаружение обрезанного ответа.

Команды:

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
& 'C:\Program Files\Go\bin\go.exe' build -o bin\workshop-agent.exe ./cmd/workshop-agent
.\bin\workshop-agent.exe day12-personalization-report
```

Проект написан на Go: Python-тестов и pytest suite нет; вместо pytest выполняется
нативный `go test ./...`. Автотесты используют mock transport, не рабочий Telegram.
Команда отчёта выполняет реальные платные LLM-вызовы на отдельной fixture.db,
не изменяет рабочую БД и не отправляет сообщения Telegram.

### Реальный эксперимент 20260917T135225Z

[HTML](../reports/day12-personalization/20260917T135225Z/report.html),
[исходные результаты](../reports/day12-personalization/20260917T135225Z/results.json).
MiniMax/MiniMax-M3, temperature=0, семь вызовов, 8485 provider tokens.
Profile overhead ≈184–187 tokens на запрос (UTF-8 bytes / 3, не точный tokenizer).

Для контролируемого A/B использован один user/session и одинаковый domain snapshot;
менялся только профиль. A: neutral / brief / list / summary_first=true.
B: technical / detailed / table / summary_first=false. Изоляция второго User
проверена отдельно. A вернул короткий итог и три строки; B — таблицу с минимумами
и пояснениями. В обоих: гипс 10 кг, кисточки 400 шт, коробки 100 шт.
Новая сессия сохранила brief; временное detailed не сохранилось; постоянное detailed
сохранилось в следующей сессии. Производственный отчёт начал с итога.
Все пять структурных checks true; доменных изменений нет.

Первый сетевой запуск заблокирован sandbox; следующий запуск с бюджетом 512
дал обрезанные ответы (7847 tokens). Эти артефакты сохранены, но не считаются
успешным сравнением. После повышения бюджета выполнен указанный полный прогон.

Ограничения: точные поддержанные фразы preferences, не универсальное извлечение;
en пока только подготовлен; нет workshop overrides; оформление LLM вероятностно.
StocksPresent проверяет только наличие чисел/названий, не доказывает точность всего
текста. Ручная проверка выявила в подробном ответе фразу «расход не критичен», хотя
данные о расходе не передавались: это неподтверждённая оценка модели, не факт БД.
Запасы и минимумы в ответах совпали с fixture. Такие ответы предназначены для
чтения и не инициируют закупку, производство или списание.

Для применения обновления нужен перезапуск бота: **Ctrl+C → `.\bin\workshop-agent.exe`**.
