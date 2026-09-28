"""Offline evidence rendering, not a production Python dependency."""
import html
import json
from pathlib import Path

root = Path(__file__).resolve().parent
events = [json.loads(line) for line in (root / 'tests.jsonl').read_text(encoding='utf-8-sig').splitlines() if line.strip()]
failed = [e for e in events if e['Action'] in ('fail', 'build-fail')]
passed = [e for e in events if e['Action'] == 'pass' and 'Test' in e]
packages = [e['Package'] for e in events if e['Action'] == 'pass' and 'Test' not in e]
data = json.loads((root / 'evidence.json').read_text(encoding='utf8'))
assert not failed and data['ListTools'] and len(packages) >= 19
assert data['Catalog']['metrics']['http_request_count'] == 3
assert data['Blocked']['metrics']['http_request_count'] == 0
assert data['AdditionalProductCalls'] == 0
assert data['Partial']['stocks'][1]['observation'] == 'MISSING'
assert data['Seller']['stocks'][1]['observation'] == 'ZERO'
raw = json.dumps(data, ensure_ascii=False)
assert 'synthetic-ozon-key' not in raw and 'synthetic-client' not in raw

def pre(value):
    return '<pre>' + html.escape(value if isinstance(value, str) else json.dumps(value, ensure_ascii=False, indent=2)) + '</pre>'

sections = []
def add(title, text, value=None):
    sections.append('<section><h2>' + html.escape(title) + '</h2><p>' + html.escape(text) + '</p>' + (pre(value) if value is not None else '') + '</section>')

add('1. Реализованная цепочка', 'Application → существующий MCP client → in-memory SDK → ozonmcp module → Go request controller → local TLS HTTP mock. Один production процесс сохранён. EXE не заменялся; проверочная сборка направлена в NUL. Production API/Telegram/DB не использовались.')
add('2. Endpoints и contract evidence', 'POST /v3/product/list; POST /v3/product/info/list; POST /v2/product/info/stocks-by-warehouse/fbs; POST /v1/analytics/stocks. Requests: audited reference b95da59689cdacb544c659c5c0c1fcbecc50a995. Response fields: опубликованные reference models/examples. Текущая официальная OpenAPI и live permissions ещё не проверены; детали provenance в docs/ozon-stage-a-implementation.md. Поэтому общий статус PARTIALLY ACCEPTED, хотя offline цепочка реализована.')
add('3. Настоящий ListTools', f'Получено через SDK initialize/ListTools: {len(data["ListTools"])} tools, в том числе3 Ozon READ tools. Ozon WRITE tools=0. Два существующих WB local mutation tools не являются Ozon API writes.', data['ListTools'])
add('4. CallTool и пагинация', 'Настоящий MCP CallTool: каталог SUCCESS. 100 товаров,2 страницы каталога,1 details batch100. Всего3 HTTP requests. Offset90 возвращает последние10 товаров без API. Повторный cursor ограничивает загрузку и даёт PARTIAL.', {k: data['Catalog'][k] for k in ['status', 'total', 'metrics', 'products']})
add('5. Cache и identity', 'SQLite marketplace_catalog + marketplace_catalog_skus: product_id/offer_id/SKU хранятся отдельно, возможны несколько SKU на product. Явный catalog refresh; нет startup/catalog-refresh-on-stock-view. Перезапуск SQLite/client/manager сохраняет100 товаров. Stock names читаются локальным SQL join.')
add('6. Seller source', 'OZON_SELLER_STOCK, quantity=free_stock, ключ SKU/warehouse.2 страницы,2 HTTP requests,3 валидные записи. Явное0 отображается как ZERO.', data['Seller'])
add('7. Ozon-side source', 'OZON_FBO_AVAILABLE, quantity=available_stock_count. FBO analytics по SKU локального каталога; не online-total всех видов запасов.1 batch/1 HTTP request. Reference-backed parsing; актуальная официальная семантика/права требуют отдельной сверки.', data['FBO'])
add('8. Current/history и source separation', 'marketplace_stock_current и append-only marketplace_stock_history отдельны. Seller partial обновляет только полученную запись: бывший zero, отсутствующий в новом ответе, имеет MISSING, не становится новым подтверждённым ZERO.4 seller history rows после initial+partial; FBO9 не меняется. Workshop Inventory не изменяется.', data['Partial'])
add('9. No N+1 / cache', 'Batch100 details=1 request; catalog total=3requests. Cached products/stocks проходят MCP без Product API calls. source refresh не обновляет каталог. Дополнительных Product API requests при stock rendering:0.', {'additional_product_api_requests': data['AdditionalProductCalls']})
add('10. Dedup / access', 'Request-layer singleflight и whole-refresh singleflight проверены. Два одинаковых concurrent refresh дают1 HTTP sequence/1 committed run. Scope проверяется до HTTP и commit. Forged grant, чужая мастерская, wrong provider/connection, stale active workshop и disabled/revoked connection отвергнуты. HTTP после denied=0.')
add('11. 429 и restart', '401/403 без retry, temporary5xx max2attempts, Retry-After3600 не обрезается. Persistent integration_cooldowns пережил закрытие SQLite и создание нового service/client/manager. После restart BLOCKED_LOCALLY, HTTP requests0, last-known-good сохранён.', {'rate': data['Rate'], 'after_restart': data['Blocked']})
add('12. Реальный safe trace из mock-теста', 'HTTP: request start/finish/status/count/page/received/valid, saved0 до транзакции. SYNC: фактически сохранённые counts/status. CACHE и DEDUP не считаются HTTP. Caller=mcp_tool для этого Application теста; Telegram передаёт собственный caller. Секретных значений и headers в этих данных нет.', data['Traces'])
add('13. Telegram output', 'Ниже реальный форматтер над результатами CallTool из acceptance fixture. Отдельные Telegram mock tests проверили команды, кнопки, secret input rejection до memory, немедленный ответ при refresh и подавление ответа после отключения.', '\n\n---\n\n'.join(data['Telegram']))
add('14. Migration118', 'Provider-aware marketplace_connections: WB id1 сохранён, Ozon id2 owner-bound. Новые catalog/SKU/run/current/history/cooldown/trace tables. FK rebuild тест проверил WB данные и ссылки, idempotency и запрет Ozon rows под WB connection. Рабочая DB не открывалась, migration применится при будущем запуске новой сборки.')
add('15. Secret safety', 'OZON_CLIENT_ID/OZON_API_KEY читаются только server-side при startup; пустые placeholders в .env.example. В SQLite только secret_ref и cabinet identity fingerprint. MCP input содержит refresh/offset, без credentials/user/workshop/URL. Input guard, escaped response-secret redaction, bounded HTTPS/TLS, no redirects, no proxy, safe errors проверены.')
add('16. Regression / build', f'go test ./...: exit0; {len(passed)} successful test events; {len(packages)} packages passed; failures0. go build -o NUL ./cmd/workshop-agent: exit0. WB, Day18/19/background, Tasks/Memory/Users/Telegram tests прошли. tests.jsonl — полный вывод.', packages)
add('17. Ограничения приёмки', 'PARTIALLY ACCEPTED. MCP/storage/UI и offline acceptance реализованы. Остаётся официальная/live сверка актуальных response fields, FBO semantics, прав кабинета и лимитов. Reference fixtures — не реальные ответы вашего кабинета. Ozon jobs/pipelines/Performance/postings/write operations не добавлялись. EXE не обновлён согласно заданию.')
page = '<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Ozon Stage A · WA-D167</title><style>body{max-width:1180px;margin:32px auto;padding:0 20px;font:16px/1.6 system-ui;color:#183047}section{border-top:1px solid #d9e0e7;padding:12px 0}pre{padding:16px;background:#eef3f8;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere}.status{background:#fff0cc;padding:20px;font-weight:700}</style><h1>Ozon Stage A · WA-D167</h1><p class="status">PARTIALLY ACCEPTED · MCP/storage/UI verified offline · official/live verification pending</p>' + ''.join(sections) + '</html>'
(root / 'report.html').write_text(page, encoding='utf8')
print(f'report.html: {len(packages)} packages, {len(passed)} successful test events, {len(failed)} failures; {len(data["ListTools"])} discovered tools')
