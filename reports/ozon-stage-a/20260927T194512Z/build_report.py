"""Offline report generation only; not a Workshop Agent production dependency."""
import html,json
from pathlib import Path
root=Path(__file__).resolve().parent
events=[json.loads(line) for line in (root/'tests.jsonl').read_text(encoding='utf-8-sig').splitlines() if line.strip()]
failed=[e for e in events if e['Action']=='fail']
packages=[e['Package'] for e in events if e['Action']=='pass' and 'Test' not in e]
tests=[e for e in events if e['Action']=='pass' and 'Test' in e]
traces=[]
for e in events:
    if 'OZON MOCK REQUEST ' in e.get('Output',''):
        traces.append(json.loads(e['Output'].split('OZON MOCK REQUEST ',1)[1]))
(root/'trace.json').write_text(json.dumps(traces,ensure_ascii=False,indent=2),encoding='utf8')
sections=[
('Architecture','Существующая one-process/in-memory MCP архитектура сохранена. Добавлен standalone Go package HTTP boundary, НЕ отдельный process. Production wiring ещё не добавлен.'),
('Ozon connection','Не внедрена. Legacy marketplace_connections имеет CHECK(id=1/provider=wildberries/secret_ref=WB_API_TOKEN); provider-aware migration требует проверки FKs. Новая migration не применялась.'),
('MCP tools','Ozon tools НЕ зарегистрированы: fake tools запрещены. Existing WB/MCP tests проходят. Нет нового Ozon ListTools/CallTool acceptance.'),
('Product identity','product_id / offer_id / sku должны сохраняться раздельно. Persistent model/cache пока не внедрены. Typed requests используют отдельные ProductIDs/SKUs, это не готовая DB identity.'),
('Product catalog sync','ProductsPage формирует POST /v3/product/list с filter.visibility, limit, last_id. Полный нормализованный catalog sync/pagination/cache не реализован: response contract требует подтверждения. ProductDetailsBatch отправляет 100 IDs одним HTTP POST в mock.'),
('Seller/FBS stock','Подтверждён request wrapper POST /v2/product/info/stocks-by-warehouse/fbs. Response остаётся RawMessage, stock normalization и Telegram output НЕ реализованы.'),
('Ozon-side stock','Подтверждён request wrapper POST /v1/analytics/stocks. Quantity/warehouse/identity semantics не угаданы; source name/decoder/normalized output остаются OPEN.'),
('Stock source separation','Нормативно зафиксированы независимые Workshop Inventory / Seller / Ozon-side источники. Production stock storage не менялось; тестов новых source snapshots ещё нет.'),
('HTTP request count','На 4 разных wrappers mock получил 4 HTTP calls. На batch из100 products получен1 HTTP call. Concurrent identical calls:1 HTTP call. Это transport tests, не доказательство полной catalog pagination.'),
('Cache usage','BYPASS metadata есть в request trace; catalog SQLite cache не реализован. HIT/MISS допускаются metadata, но cache acceptance не выполнен.'),
('N+1 prevention','TestDetailsHundredProductsAreOneBatch прошёл. Stock rendering ещё нет; нельзя утверждать, что уже проверены0 additional Product API calls в Telegram.'),
('Rate-limit/retry behavior','401/403 без retries,503 максимум2attempts,429 без автоматического retry,Retry-After seconds/date без10s cap. Singleflight+serial gate. Есть State interface для persisted cooldown/trace; production SQLite adapter пока отсутствует, restart persistence не принят.'),
('Request trace','Ниже реальная запись из mock Go test. Caller/tool/endpoint/attempt/cache/dedup/status/count фиксируются. records_received/valid/saved=null: normalized rows не разобраны. SUCCESS означает успех HTTP/JSON boundary, не полный успешный stock sync.'),
('Security / credentials','Fixed HTTPS host, redirects disabled, response<=4MiB, timeout30s, bounded IDs/batches, private immutable credentials, mandatory guard and state. Proxy ENV не наследуется. Mock tests проверяют отсутствие secrets в payload/trace/errors, включая escaped response echo. Настоящий .env не читался; реальные Ozon/WB/Telegram запросы не выполнялись.'),
('WB regression','Полный go test ./... exit0; пакеты marketplace/wildberries, integrations/wbmcp, mcpclient, mcpmanager, background и telegram прошли. Production WB code/semantics не менялись.'),
('Tests',f'{len(tests)} passed test events, {len(packages)} passed packages, {len(failed)} failures. go build -o NUL ./cmd/workshop-agent exit0. EXE не создан/не заменён. Подробный machine-readable output: tests.jsonl.'),
('Contract blocker','Официальные docs.ozon.ru Seller docs и swagger.json возвращают redirect loop. Python reference подтверждает пути/requests, но не typed response. Запрошена локальная официальная OpenAPI либо обезличенные fixtures. По требованию пользователя запрещено угадывать. Stage A NOT ACCEPTED; продолжить только подтверждённую часть после получения контрактов.'),
]
body=''.join('<section><h2>'+str(i)+'. '+html.escape(name)+'</h2><p>'+html.escape(text)+'</p></section>' for i,(name,text) in enumerate(sections,1))
body+='<h2>Фактический mock trace</h2><pre>'+html.escape(json.dumps(traces,ensure_ascii=False,indent=2))+'</pre>'
body+='<h2>Passed packages</h2><pre>'+html.escape('\n'.join(packages))+'</pre>'
page='<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Ozon Stage A — incomplete</title><style>body{max-width:1100px;margin:36px auto;padding:0 20px;font:16px/1.6 system-ui;color:#203247}section{border-top:1px solid #ddd;padding:12px 0}pre{background:#f1f4f8;padding:16px;overflow:auto}.status{background:#fff0cc;padding:20px;font-weight:bold}</style><h1>Ozon Stage A · промежуточный отчёт</h1><p class="status">NOT ACCEPTED — HTTP foundation verified; response contracts unresolved</p>'+body+'</html>'
(root/'report.html').write_text(page,encoding='utf8')
assert not failed and traces
print('report:',root/'report.html','packages:',len(packages),'test events:',len(tests),'failures:',len(failed))
