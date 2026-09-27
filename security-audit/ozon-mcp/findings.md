# Findings

AUDITED COMMIT: b95da59689cdacb544c659c5c0c1fcbecc50a995

**CRITICAL 0 · HIGH 3 · MEDIUM 9 · LOW 2 · INFO 1.** Это 15 выводов аудита, не 15 CVE. Advisory scanner перечислены отдельно.

Ссылки на код: [зафиксированное дерево](https://github.com/DeviceIngineering/ozon-mcp-server/tree/b95da59689cdacb544c659c5c0c1fcbecc50a995). Все paths/line numbers ниже относительно source/. Доказательства dynamic — raw/dynamic.json и воспроизводимый runtime_probe.py.

| ID / severity | Код | Сценарий и влияние | Исправление / доказательство |
|---|---|---|---|
| OZ-A01 HIGH | app.py:101–110,169–339; Dockerfile; docker-compose.yml | Сетевой клиент без auth изменяет/удаляет магазины, читает диагностику, запускает probes. MCP_AUTH_TOKEN защищает только MCP. Default web bind 0.0.0.0 и пустой token дополнительно раскрывают все MCP write-tools. | Общая deny-by-default auth, owner ACL для management, loopback/private bind, TLS. Dynamic: защищённый /sse=401, POST/DELETE shops=200 без токена. |
| OZ-A02 HIGH | client.py:32–48,182–194,748–750,795–813 | Общий retry повторяет в том числе изменения цен, отгрузку, refunds на 429/5xx. После выполненной операции и потерянного успешного ответа возможна повторная бизнес-операция. Retry-After=3600 обрезается до 10s. | Endpoint semantic retry policy; idempotency где поддерживается; Retry-After целиком, durable shared cooldown. Mock price write: 4 attempts, waits 10/10/10. Не утверждается, что любой price update неидемпотентен; опасен общий механизм для всех writes. |
| OZ-A03 HIGH | app.py:247–266; templates/shops.html:19–21 | Неограниченный shop_id сохраняется и подставляется в JS строку onclick. HTML escaping не экранирует JS после декодирования HTML entities браузером. Нападающий сохраняет ID с кавычкой; при нажатии owner выполняется JavaScript в origin с формой credentials. | Убрать inline event handlers; dataset + addEventListener, строгий формат shop_id, CSP. Dummy response подтвердил разрыв JS string; browser exploitation не выполнялась. |
| OZ-A04 MEDIUM | toolsets.py:48–76; server.py:944–948 | Нет точного read-only allowlist. Профили смешивают чтение и запись; опечатка без допустимого профиля превращается в all-tools. Пользователь рассчитывает ограничить права, но LLM получает write. | Явный список capability, unknown profile => startup error; immutable read-only policy. Dynamic invalid profile разрешает ozon_set_prices. |
| OZ-A05 MEDIUM | templates/base.html:7–8; templates/shops.html:29–35 | Third-party JS без SRI работает рядом с вводимыми ключами; компрометация поставщика/CDN читает поля и делает API-запросы от origin. CSS major-version плавает. Фактическая эксфильтрация не обнаружена. | Локальные pinned assets, hashes/SRI, CSP без inline JS. |
| OZ-A06 MEDIUM | settings.py:23–31,87–98 | Ключ Fernet рядом с ciphertext, inherited ACL, совместный /data backup раскрывает credentials. | Отдельный OS secret store/vault, права owner-only, раздельные backups/rotation; атомарная запись. |
| OZ-A07 MEDIUM | server.py:141–157,258–261,690–695; client.py:32–48,936–947,1348–1375 | Nested write payload почти не валидируется; отрицательная price/ID проходят schema. action произвольно входит в path, без enum/проверки в client. Неограниченный response читается целиком, report trim после чтения. Ошибка/вредоносный ввод вызывает ненужные запросы/DoS/непредусмотренный same-host path. | Range/date/enum/maxItems, path ID encoding, max response bytes, bounded concurrency. Mock schema test, static path/response analysis; arbitrary-host SSRF не подтверждён. |
| OZ-A08 MEDIUM | server.py:920–941; diagnostics.py:116–140,171–183; app.py:298–318; stats.py:67–99 | Raw exception и фрагменты response становятся tool result/DB/web. При sensitive data в upstream error они раскрываются; штатная UI mask сюда не применяется. | Typed safe errors, centrally scrub secrets/PII, no raw dumps. Synthetic marker дошёл до MCP. Настоящая утечка не утверждается. |
| OZ-A09 MEDIUM | server.py:42–79,977–998; settings.py:101–113 | Любой клиент выбирает любой shop_id. Shop pool — routing, не tenant authorization. Общая MCP credential даёт доступ ко всем настроенным магазинам. | Principal→workshop→connection policy на каждой операции, отдельные secrets/capabilities. |
| OZ-A10 MEDIUM | app.py:44–80,213–243; diagnostics.py:67–145; client.py:1178–1208 | Автопробы через 15s и каждые 30min, manual probes параллельно; нет общего limiter/cooldown. Финансовая probe составная. Performance token refresh без singleflight; отчёт без enforced one-at-a-time. | Общий limiter account+API group, durable retry deadline, singleflight, diagnostics opt-in; не копировать hidden health fanout. |
| OZ-A11 MEDIUM | pyproject.toml:dependencies/build-system; Dockerfile:1–14; .github/workflows/publish.yml | Плавающие зависимости и base image без lock/digests, root container; следующая установка отличается от проверенной. | Lock + hashes, image digest, non-root, readonly rootfs, dependency updates. PyPI source match не доказывает безопасность будущих resolution. |
| OZ-A12 MEDIUM | client.py:349–371; server.py:920–941 | Финансовый composite молча ограничивает диапазон 31д и страницы 50; повторяющийся last_id не распознаётся, при cap возвращается обычный результат. Structured {error:...} из stub или report ERROR может учитываться как success, потому что не exception. | Явные completeness/cursor/error states, loop detection, не обозначать partial как full success; отдельные tests границ. |
| OZ-A13 LOW | app.py:101–110 | MCP token разрешён в query; URL может попасть в access logs/browser history/referrer. | Authorization header only; query scrub, короткоживущие session credentials. |
| OZ-A14 LOW | settings.py:34–98 | Silent decrypt failure, env fallback, неатомарная read-modify-write и повторное появление удалённого ENV shop. При сбое/параллельном save возможны потеря конфигурации и неожиданное подключение. | Fail closed, structured diagnostics, atomic replace+lock, явная граница ENV-managed config. |
| OZ-A15 INFO | client.py:196–200,786–813,1139–1150,1403–1404; diagnostics.py:67–91 | Есть compatibility aliases и 5 неподдерживаемых stubs. Название ozon_get_prices_v4 фактически вызывает v5; digital_act читает статус, finance report читает cash flow. Подписи probes старее actual methods. | Пользоваться endpoint map, удалить misleading aliases/stubs, версии сверять с official docs; V1 не переносит эти tools. |

## Подтверждённые положительные свойства

- Фиксированные HTTPS hosts для runtime Ozon; нет публичного generic HTTP tool, штатного shell/file-reader MCP tool.
- TLS verification не отключена; redirects default off и это проверено mock.
- Seller и Performance credentials разделены; Bearer token сохраняется только в RAM.
- Fernet encryption и UI masking существуют; это лучше plaintext-файла ключей, хотя соседство master key ограничивает защиту.
- MCP Bearer сравнивается compare_digest; профили проверяются не только в ListTools, но и при вызове.
- TOOLS/dispatch объявлены вручную, новый Ozon endpoint не публикуется автоматически.
- Финансовая pagination имеет конечный cap; carriage_create требует delivery_method_id; shaping маркирует сокращение ряда ответов.
- Есть upstream тесты, включая реальные локальные SSE initialize/ListTools; 49 passed, 1 skipped.

## Severity interpretation

HIGH A01 требует сетевой доступности web; A03 — последующего взаимодействия owner с UI. Не выдаём локальные исходники сами по себе за удалённую RCE. CRITICAL не обнаружены. Нет доказательств вредоносного поведения автора или намеренной отправки ключей посторонним сервисам.
