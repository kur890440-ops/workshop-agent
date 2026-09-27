# Credentials: полный путь

| Тип | Источник | В памяти / передача | Постоянное хранение |
|---|---|---|---|
| Seller Client-Id, Api-Key | OZON_CLIENT_ID, OZON_API_KEY либо web shop form | settings dict -> pool -> OzonSellerClient attrs -> HTTPS headers api-seller.ozon.ru | Web save шифрует оба поля в shops.json |
| Performance client_id/client_secret | OZON_PERF_CLIENT_ID, OZON_PERF_CLIENT_SECRET либо web form | OzonPerformanceClient attrs; JSON POST /api/client/token на api-performance.ozon.ru | Fernet в shops.json |
| Performance access token | Ответ Ozon token endpoint | RAM: _token и Authorization: Bearer клиента; обновление по 1740 секундам | Не сохраняется штатно |
| MCP_AUTH_TOKEN | ENV | Bearer request или query token; compare_digest | Upstream не пишет сам; ENV/compose задаёт оператор |
| Fernet key | Сгенерирован или прочитан .encryption_key | Fernet encrypt/decrypt | Открытый key рядом с ciphertext в DATA_DIR |

Код: settings.py:23–122; client.py:13–24,1161–1210; app.py:27–29,101–110,247–266; diagnostics.py:150–183,224–244.

ENV default shop объединяется с shops.json; файл имеет приоритет. Ошибки чтения/decrypt молча подавляются. Это может скрыть повреждение файла или оставить ENV credentials активными. Удаление ENV-defined shop через UI не убирает ENV: он появляется снова при следующем load. Rename/rebind не подтверждается identity кабинета.

Миграция settings.json сохраняет новый shops.json, но не удаляет старый ciphertext. Запись файла не атомарная и без блокировки. Permissions/ACL не ограничиваются кодом; наследуются от ОС/директории. Полная копия /data содержит и ciphertext, и ключ, поэтому доступ к backup раскрывает все ключи. Частичная копия только shops.json защищена лучше, чем plaintext, но это не vault.

## UI, SQLite, logs, errors

- GET /shops использует get_masked_shop: первые/последние 3 символа, короткие значения полностью маскируются. Полный сохранённый API key штатно не возвращается UI.
- Пользователь вводит новый ключ в DOM; web POST передаёт его без TLS в default HTTP deployment. CDN JavaScript способен читать эти поля. Masking не защищает вводимый секрет.
- SQLite не является штатным хранилищем API keys. tool_calls хранит имя, shop_id, latency, success, error_text; health_checks хранит detailed diagnostics JSON. Diagnostics включает полный Client-Id (идентификатор, не Api-Key), roles и сроки.
- Error paths НЕ используют общий redactor: server.call_tool возвращает type + str(exception), diagnostics иногда первые 300 символов response.text; app test возвращает str(exception). Mock с DUMMY_SENSITIVE_ERROR_MARKER дошёл до MCP ответа. Это доказательство отсутствия redaction, а не доказательство уже случившейся утечки настоящего ключа.
- Не найден штатный dump Authorization/header/body в logger. Uvicorn access logs могут сохранить `?token=` из URL. SQLite error_text может сохранить данные, попавшие в exception.
- scripts/collect_corpus имеет отдельную heuristic PII-маску строк, но она не используется в штатном error path, не даёт общей гарантии и не маскирует все числовые идентификаторы.

## Куда секреты уходят

Прямых серверных отправок Ozon credentials на третьи домены в audited runtime коде не обнаружено. Seller headers — фиксированный Seller host, Performance credentials/token — фиксированный Performance host. Redirect-following не включён; mock redirect остался на единственном исходном запросе. Однако httpx trust_env по умолчанию допускает operator-configured proxy/CA; окружение необходимо контролировать. Web UI — отдельная граница риска: CDN JS и default plaintext HTTP, см. network-map и findings.

Стенд использовал только dummy значения. Настоящий .env Workshop Agent не читался.
