# Network destinations и telemetry

| Code location | Destination | Purpose / данные |
|---|---|---|
| client.py:9,19,32 | https://api-seller.ozon.ru | Seller API: Client-Id/Api-Key headers, выбранные product/order/stock/price/finance payloads |
| client.py:10,1173–1208 | https://api-performance.ozon.ru | client_credentials token JSON, Bearer; кампании, ставки, отчёты |
| diagnostics.py:25–57 | Оба фиксированных Ozon host, GET / | Проверка доступности без auth; не third party |
| diagnostics.py:67–91,191–206 | Seller host | 12 параллельных probes + /v1/roles; Seller credentials |
| diagnostics.py:151–183 | Performance host /api/client/token | Отдельный token probe, client_id/client_secret |
| templates/base.html:7 | cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css | Browser CSS, IP/UA/referrer; плавающая major-version |
| templates/base.html:8 | unpkg.com/htmx.org@2.0.4 | Browser executable JS, без SRI; может читать DOM при компрометации CDN |
| PyPI/build, GitHub Actions | pypi.org, files.pythonhosted.org, GitHub; Docker registry при build | Download/publish пакетов/образа; не runtime отправка кабинета |

Telemetry SDK/init Sentry, OTel, PostHog, Mixpanel, external metrics exporters, runtime update checks и GitHub API calls в runtime-коде не обнаружены. `analytics_*` — бизнес-методы Ozon, не tracking. stats.py пишет локальную SQLite. Это source-level вывод плюс ограниченный local run; не доказательство поведения всех будущих floating dependencies. Browser CDN не является заявленной телеметрией, но это third-party traffic.

## Полная диагностика — реальные endpoints

Все Seller методы ниже POST, кроме actions GET. Подписи endpoint внутри build_probes местами устарели: таблица построена по реально вызываемым client methods, не по строке подписи.

| Probe | Реальный путь |
|---|---|
| product_list | /v3/product/list |
| product_info_prices | /v5/product/info/prices |
| actions_list | GET /v1/actions |
| rating_summary | /v1/rating/summary |
| analytics_turnover_stocks | /v1/analytics/turnover/stocks |
| posting_fbs_list | /v4/posting/fbs/list |
| finance_transaction_totals | /v1/finance/accrual/types (если cache miss), /v1/finance/accrual/by-day (каждый день/страница) |
| review_list / question_list | /v1/review/list, /v1/question/list |
| chat_list | /v3/chat/list |
| warehouse_list | /v2/warehouse/list |
| report_list | /v1/report/list |
| key_expiry | /v1/roles |

Дополнительно 2 unauth host GET и optional Performance token POST. Число HTTP calls не равно 12: financial composite разворачивается в несколько запросов и retries умножают их. При двух датах, одной странице начислений на дату и холодном cache ожидается 18 исходных запросов при настроенном Performance, 17 без него. При отсутствии данных/ошибке/пагинации число меняется. Автоматически запускается через 15 секунд web startup, затем каждые 30 минут после окончания предыдущей проверки; 0 отключает. Ручные route/tool могут работать параллельно. Health probe 429 отмечает API доступным, не успешность business sync.

## SSRF / TLS / redirects

URL base фиксирован в коде и не берётся из tool/shop config. В некоторых путях интерполируются IDs/action; args должны иметь ограничения, но это не возможность выбора другого host через обычный schema-valid ID. returns_rfbs_action не проверяет action: перечень есть только в description, без enum, поэтому возможен непредусмотренный path на том же host. Image/file URLs передаются Ozon как данные; сервер сам их не скачивает. report_info возвращает download URL, автоматического перехода на произвольный URL нет. Performance report скачивается с фиксированного host.

verify=False не найден; HTTPS проверяется default httpx. Redirects default disabled, подтверждено mock. Response size не ограничен до чтения; CSV truncation 50000 применяется после загрузки. HTTPX proxy/CA ENV доверены оператору, поэтому deployment env — security boundary.

## Локальные наблюдения

raw/dynamic.json: stdio initialize, 156 tools, local read, no Ozon. raw/web-dynamic.json: отдельный локальный Python web process, listener 127.0.0.1 на временном порту, только loopback TCP, создан stats.db. Процесс закрыт, connect_ex подтвердил закрытие порта. Audit guard запрещал non-loopback TCP в контролируемых child processes. Это не full packet capture/OS sandbox: краткие UDP/DNS события отдельно не трассировались. Web health loop был отключён; браузер не запускался, CDN не загружались.
