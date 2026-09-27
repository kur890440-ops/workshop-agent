# Ozon MCP: технический и security-аудит

Дата: 2026-09-27. Repository: https://github.com/DeviceIngineering/ozon-mcp-server

Branch: `main`. Tag: `v2.5.2`.

**AUDITED COMMIT: b95da59689cdacb544c659c5c0c1fcbecc50a995**

Рекомендация: **C — использовать как reference, переносить выбранные read-методы в Go**. В текущем состоянии не подключать upstream с production credentials. Изучать исходники допустимо.

Workshop Agent, его конфигурация, секреты, SQLite и executable этим аудитом не изменялись. Все новые материалы находятся в этой папке. До аудита рабочее дерево уже содержало изменения. Ozon API не вызывался; использовались dummy credentials, mock HTTP, локальные STDIO/SSE и loopback web. Python установлен только в изолированную `.venv` для аудита.

## Результат

| Проверка | Результат |
|---|---|
| Реальный MCP initialize + ListTools | 156 tools, STDIO; локальный tool вызван, сессия закрыта |
| SAFE_READ / READ_SENSITIVE | 51 / 48; всего READ 99, включая 5 явно отмеченных неподдерживаемых stub-tools |
| WRITE / DESTRUCTIVE_OR_HIGH_RISK | 36 / 21; всего WRITE 57; создание remote report jobs учтено консервативно как WRITE |
| Upstream pytest | 49 passed, 1 skipped, 9 warnings; exit 0 |
| Dependency scan | 10 записей scanner, 5 уникальных advisory IDs, только pip 25.3 из audit-venv; известных advisory runtime-пакетов в разрешённом наборе не найдено |
| Secret scan | 49 commits, 211 уникальных blobs; 1 тестовый placeholder, подтверждённых production secrets не найдено; heuristic scan имеет ограничения |
| PyPI wheel | 14 файлов ozon_mcp совпадают после CRLF/LF-нормализации; hashes и sdist сохранены |
| Web | При MCP_AUTH_TOKEN unauthenticated POST/DELETE магазинов возвращают 200; /sse без токена — 401 |
| Retry | Mock 429 + Retry-After=3600: запись цен отправлена 4 раза, ожидания 10/10/10 секунд |
| Сеть стенда | Loopback listener подтверждён netstat; процесс завершён, порт закрыт; outbound TCP блокировался audit guard |

## Карта материалов

- [Автономный HTML-отчёт](report.html) — все разделы и полный каталог.
- [Findings](findings.md) — severity, код, сценарий, исправление и доказательства.
- [Архитектура](architecture.md), [сеть](network-map.md), [credentials](credentials.md).
- [Полная карта tools](tools-map.md), [машиночитаемый JSON](tools-map.json), [READ/WRITE](read-write-tools.md).
- [Dependencies, license и supply chain](dependencies.md).
- [Минимальный Go scope, identity, stocks, prices, postings](minimal-go-scope.md).
- [WB vs Ozon](wb-vs-ozon.md), [условия безопасного запуска и вердикты](safe-deployment.md).
- `raw/`: статический поиск, dependency scan, secret scan, результаты mock/dynamic/pytest, package hashes.

## Границы доказательств

Это аудит зафиксированного кода и локального поведения, не pentest инфраструктуры Ozon и не проверка реального кабинета. Полная текущая Seller/Performance OpenAPI не получена: docs.ozon.ru и seller/swagger.json возвращали redirect loop. Официальные новости API подтвердили отдельные версии методов; точные текущие response schemas, категории прав и rate limits необходимо сверить перед реализацией. Не переносить Python dict-пасстру и комментарии как нормативный контракт.

Источник исходного запроса: attachment `a6a292b4-4e0e-41bb-a2a0-18611b45745f/pasted-text.txt`. После аудита интеграция не выполнялась.
