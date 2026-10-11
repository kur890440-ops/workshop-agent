# @PROJECT:WORKSHOP_AGENT @L3:DAY30_HYBRID_LLM_IMPLEMENTATION

@PRESERVE

Реализовано 2026-10-10. Существующие незакоммиченные изменения Web Chat сохранены.

## Контракты и поток

Telegram text, voice → text и Web `Bot.HandleInbound` используют общий
`Bot.processMessage`. Сначала проверяются identity, active workshop и
`auth.WorkshopRead`. Команды, открытые формы и подтверждения сохраняют прежний
приоритет. Произвольный текст проходит локальный `MessageRouter`.

Router получает оригинальный вопрос, до шести последних пользовательских
сообщений, предыдущий маршрут и метаданные текущей задачи (status/phase/action type).
Каталог, остатки, скрытые ответы мастерской ему не передаются. Схема ограничивает
route, reason, complexity и optional general_answer. Неоднозначный запрос или
ошибка классификации направляются в WORKSHOP. Confidence не выдумывается.

GENERAL в LOCAL_ONLY/AUTO SIMPLE отвечает результатом одного локального вызова
route + answer. Служебный JSON не показывается. MCP, каталог и Task FSM не вызываются.
Ответ и вопрос сохраняются в существующей short-term memory. WORKSHOP входит в
существующие обработчики semantic/Agent, с прежними authorization, permissions,
InvariantEngine, FSM, preconditions и confirmation. Нового исполнителя действий нет.

`HybridClient` реализует существующие `llm.Client`, `SemanticClient` и
`Complete`. Сохраняются отдельные legacy/semantic JSON-схемы. Существующий
`OpenRouterClient` используется как cloud generation provider. Новый
`OllamaClient` — единственный Ollama-клиент этого репозитория, HTTP /api/chat,
stream=false, think=false. Native tool calling не нужен: приложение уже исполняет
проверенные StructuredCommand через свои обработчики, а не команды из текста модели.

## Конфигурация

| Переменная | По умолчанию |
| --- | --- |
| LLM_MODE | AUTO |
| LLM_LOCAL_BASE_URL | http://127.0.0.1:11434 |
| LLM_LOCAL_MODEL | пусто: discovery установленной Qwen3 4B через /api/tags |
| LLM_LOCAL_TIMEOUT | 90s |
| LLM_CLOUD_PROVIDER | openrouter |
| LLM_CLOUD_TIMEOUT | 60s |

Cloud сохраняет LLM_API_KEY, LLM_BASE_URL, LLM_MODEL. В .env.example указан
проверенный tag qwen3:4b-q4_K_M. Реальный .env не изменялся.

LOCAL_ONLY не создаёт cloud-клиент и запрещает cloud, включая ручной recheck
и старые cloud CLI experiments. AUTO использует local first; MULTISTEP_ANALYSIS
может выбрать cloud после локальной оценки сложности. CLOUD_ONLY генерирует
ответ/план в cloud, но Router остаётся локальным. Ошибка cloud не запускает local retry.

Fallback: LOCAL_UNAVAILABLE, LOCAL_TIMEOUT, INVALID_STRUCTURED_OUTPUT,
OUTPUT_TRUNCATED, VALIDATION_FAILED, TASK_COMPLEXITY. Последний определяется
семантически локальным Router, а не длиной текста или самооценкой уверенности.
На одну генерацию — максимум одна local-попытка и одна cloud-попытка. Cloud
продолжение после consent не повторяет local. Ollama не скачивается и модели
не загружаются автоматически. Разрешены только loopback HTTP endpoints без redirects;
Ollama cloud tags запрещены. После восстановления Ollama пустой model tag
может быть обнаружен при следующем запросе.

## Privacy и продолжение после consent

Политика консервативна: **любой application payload считается потенциально
чувствительным**, в том числе простой пользовательский вопрос. Перед каждой внешней
передачей требуется согласие на конкретный payload. Router не может объявить
данные публичными и тем самым разрешить раскрытие.

Без согласия возвращается typed `*llm.ConsentRequired`
(`CLOUD_CONSENT_REQUIRED`). В обоих интерфейсах показывается исходный вопрос
и кнопка согласия. Согласие связано с hash точного prompt/format/token budget,
пользователем, conversation session, мастерской и ролью. Оно одноразовое,
погашается до сетевого вызова, действует 15 минут. Новый ввод, обычный callback,
смена сессии/роли/мастерской, истечение TTL или потеря доступа делают его недействительным.
Payload хранится только в ограниченной in-memory pending map, не в diagnostics.

После согласия продолжается только приостановленная генерация. Для semantic
результат проходит DecodeSemantic и прежний executeSemantic; для legacy — прежний
код выполнения, выделенный в HandlePlannedLegacyCommand и используемый также
обычным Agent. Исходное сообщение не проигрывается заново. Согласие на внешнюю
обработку не является согласием на списание, создание задачи или другую операцию.
После сетевого вызова доступ и активная мастерская проверяются повторно.

## Recheck и UI

Telegram inline button и Web button используют существующий callback contract:
«Перепроверить через облачную LLM». LOCAL_ONLY эту кнопку не предлагает.
Кнопка также требует отдельного согласия. Cloud получает оригинальный вопрос
и зафиксированный ответ, без исполнителя MCP, повторного чтения или действий.
Первоначальный ответ остаётся, cloud-ответ выводится отдельным сообщением.
Если прежний обработчик не сохранил синхронный результат (например, background job),
snapshot явно сообщает об отсутствии результата; recheck не выдумывает его.

Индикаторы: Qwen · Local, Cloud, Cloud · Auto fallback,
Cloud · Перепроверка. Причины и подробности — в diagnostics.

## Diagnostics

Миграция 121 создаёт hybrid_llm_runs. JSON metadata содержит request ID,
user/channel/conversation/session, route/reason, mode, selected provider,
actual model (из ответа провайдера, если доступен), fallback/reason,
local/cloud latency, validation, consent и безопасный error code.
Ошибка Router имеет отдельное поле router_failure_reason.
Нет prompt, истории, контактов, результатов мастерской, credentials или raw HTTP ошибок.

## Проверка и границы доказательств

| Проверка | Результат |
| --- | --- |
| Реальная Ollama /api/tags | qwen3:4b-q4_K_M, qwen3:4b-q8_0; использована первая |
| Реальная Qwen: PETG / orders / cancel / sales follow-up / ABS follow-up | ожидаемые GENERAL/WORKSHOP |
| Неоднозначный запрос | schema validation отвергла ответ Qwen; безопасный WORKSHOP |
| Реальная Qwen через application pipeline | general, чтение остатков, изменение только после confirmation; изолированная БД |
| Реальный cloud при local unavailable | HTTP 200, minimax/minimax-m3 |
| Реальный read-only cloud recheck | HTTP 200, minimax/minimax-m3 |
| Согласие, LOCAL_ONLY, CLOUD_ONLY, timeout, schema/truncation/validation, complexity, cloud failure | автоматические тесты |
| Telegram/Web parity, повторный callback, stale scope/role/TTL, unauthorized user | автоматические тесты |
| Повторное списание / recheck без повторного planning | автоматические тесты |
| Общий go test ./... | PASS |
| go build -o bin/workshop-agent.exe ./cmd/workshop-agent | PASS |

Реальные cloud-запросы содержали только синтетические общие вопросы, не production
данные. Первую попытку блокировала sandbox-сеть (WinSock 10013); после разрешения
на сетевой доступ оба запроса прошли. Endpoint сохранился из существующего .env.

Live Telegram transport, браузер с реальной пользовательской сессией,
production WB/Ozon/MCP actions и свежее реальное голосовое сообщение не проверялись.
Для них есть существующие regression tests; их нельзя считать live E2E.
Production бот не запускался, рабочая БД не использовалась в тестовых операциях.
Ollama запущена из существующей portable-установки соседнего проекта;
автозапуска Ollama в приложение не добавлено. Cloud approval/pending кнопки
теряются при рестарте и после нового ввода; требуется новый запрос.

Артефакты: reports/day30-hybrid-tests.txt, reports/day30-hybrid-local-e2e.txt,
reports/day30-hybrid-cloud-e2e.txt, reports/day30-hybrid-build.txt.

## Изменения этой реализации

Новые: cmd/workshop-agent/hybrid.go и hybrid_test.go;
internal/config/hybrid.go и hybrid_test.go;
internal/llm/provider.go, router.go, hybrid.go, hybrid_test.go;
internal/storage/hybrid.go;
internal/telegram/hybrid.go и hybrid_test.go;
этот документ и четыре отчёта.

Изменены: .env.example, README.md, cmd/workshop-agent/main.go,
internal/config/config.go, internal/llm/openrouter.go и types.go,
internal/agent/agent.go, internal/storage/sqlite.go,
internal/telegram/bot.go, identity.go, semantic.go, profile.go.
Прочие ранее изменённые/новые Web Chat файлы принадлежат исходному рабочему дереву.

Контракт Ollama сверялся с официальной документацией:
https://github.com/ollama/ollama/blob/main/docs/api.md
