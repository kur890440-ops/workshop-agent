# Verdict и условия безопасного исследования

| Вопрос | Ответ |
|---|---|
| A. SAFE ENOUGH FOR LOCAL SOURCE REFERENCE? | **YES**. Читать зафиксированные исходники и извлекать идеи; MIT notices сохранять при заимствовании. Это не разрешение выполнять любые scripts с реальными ключами. |
| B. SAFE FOR PRODUCTION READ-ONLY DIRECT USE? | **NO as-is**. Даже read-only Ozon key не защищает сам ключ, shop management, коммерческие данные и квоты. Возможен отдельный ограниченный pilot только после hardening ниже; для Workshop Agent он не предлагается. |
| C. SAFE FOR PRODUCTION WRITE DIRECT USE? | **NO**. Auth/UI, retry semantics, payload validation, per-operation authorization и подтверждение действий не соответствуют требованиям production write. |
| Recommendation | **C: source reference + selected Go port.** Fork+hardening возможен как самостоятельный проект, но добавляет ненужную границу процесса/web для текущей архитектуры. |

## Что требуется для отдельного upstream pilot

Применить точный read-only allowlist и fail-closed profile config; минимальные права API key; закрыть management и все diagnostics под owner auth; private/loopback bind, TLS reverse proxy, строгие Host/Origin; убрать query token и third-party JS; исправить stored JS injection; хранить ключ в OS secret store с owner ACL; закрепить зависимости и non-root image; отключить auto-health; централизовать limiter и retries; ограничить responses/inputs; redact errors; ограничить access к shop IDs. Write требует дополнительных подтверждений, idempotency и бизнес-инвариантов. Это условия, а не выполненные этим аудитом исправления.

## Безопасность по умолчанию

`ozon-mcp` — STDIO без слушающего порта, но все profiles включены и write-инструменты доступны процессу клиента. `ozon-mcp-web`/compose — 0.0.0.0:8000 с web management без auth; пустой MCP_AUTH_TOKEN открывает и MCP. При ключах health loop начинает реальные API-пробы после 15 секунд. Поэтому простого запуска с production ENV делать нельзя.

Audit local run использовал отдельный DATA_DIR, dummy keys, HEALTH_CHECK_INTERVAL_MIN=0, loopback и guard outbound TCP. Mock tests выполняли только фиктивные writes. Реальные read/write Ozon requests не выполнялись. Production Telegram bot не запускался, executable не собирался и не заменялся.

## Docker

Dockerfile: `python:3.12-slim` без digest; pip install . с плавающим dependency resolution; нет USER (root), EXPOSE8000, volume /data. Compose publish `8000:8000`, restart unless-stopped, environment credentials, healthcheck localhost; privileged, host network и docker.sock mount не найдены. start.sh запускает фиксированный compose up --build. Контейнер не собирался и не запускался в этом аудите; анализ Docker статический.

## Rate limits и ошибки: что перенять

Timeouts Seller30s/Performance60s и конечное число retries/polls — полезная основа. Не копировать Retry-After cap10s, retries на любой write, no-jitter и независимые concurrent clients. 401/403 в Seller не повторяются, 429/500/502/503/504 повторяются; transport timeout/invalid JSON не входят в retry loop и уходят как exception. Performance вызывает raise_for_status без аналогичного retry, token refresh по фиксированным1740s, а не фактическому expires_in; 401 не запускает отдельную refresh/replay policy.

Будущий Go: классифицировать auth/permission/rate/network/invalid-response отдельно, соблюдать HTTP-date/seconds Retry-After и account/group quotas после официальной сверки, persist next_allowed_at, не считать local cooldown реальным HTTP429. GET сам по себе не доказывает read; remote job creation исключить из V1. Не обещать пользователю полный snapshot при batch/page failure.
