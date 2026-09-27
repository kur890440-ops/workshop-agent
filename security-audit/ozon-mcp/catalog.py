import json,collections,ast
from pathlib import Path
R=Path(__file__).resolve().parent
tools=json.loads((R/'raw/tools-inventory.json').read_text(encoding='utf8'))
# Explicit semantic classification reviewed against dispatch + underlying endpoint bodies.
high='''ozon_set_prices ozon_pricing_strategy_delete ozon_order_fbs_ship ozon_order_fbs_cancel ozon_order_fbs_act_create ozon_carriage_create ozon_carriage_approve ozon_returns_fbs_approve ozon_returns_fbs_reject ozon_returns_rfbs_action ozon_cancellation_approve ozon_cancellation_reject ozon_product_delete ozon_product_archive ozon_product_update_stocks ozon_ad_campaign_create ozon_ad_campaign_activate ozon_ad_campaign_bids ozon_ad_campaign_budget_update ozon_search_promo_enable ozon_discount_approve'''.split()
write='''ozon_actions_activate ozon_actions_deactivate ozon_seller_action_create ozon_seller_action_toggle ozon_seller_action_products_add ozon_seller_action_products_delete ozon_pricing_strategy_create ozon_pricing_strategy_update ozon_pricing_strategy_status ozon_pricing_strategy_products ozon_min_price_timer_renew ozon_review_reply ozon_review_reply_delete ozon_ad_campaign_stop ozon_ad_products_add ozon_ad_products_delete ozon_search_promo_disable ozon_product_import ozon_product_update_offer_id ozon_product_update_images ozon_product_unarchive ozon_product_attributes_update ozon_product_import_by_sku ozon_action_auto_add_delete ozon_order_fbs_country_set ozon_question_reply ozon_chat_send ozon_chat_send_file ozon_chat_start ozon_chat_read ozon_discount_decline ozon_returns_report ozon_report_products_create ozon_report_stocks_create ozon_report_discounted_create ozon_ad_statistics'''.split()
sensitive_prefixes=('ozon_finance','ozon_order','ozon_returns','ozon_chat','ozon_cancellation','ozon_supply','ozon_review','ozon_question','ozon_company','ozon_diagnostic','ozon_degradation','ozon_report','ozon_ad_stat','ozon_ad_balance','ozon_ad_campaign_budget','ozon_list_shops','ozon_discount_task')
for t in tools:
    n=t['name']; t['classification']='DESTRUCTIVE_OR_HIGH_RISK' if n in high else 'WRITE' if n in write else 'READ_SENSITIVE' if n.startswith(sensitive_prefixes) else 'SAFE_READ'
    families=set(); endpoints=[]
    for m in t.get('methods',[]):
        family='Performance API' if m['class']=='OzonPerformanceClient' else 'Seller API'; families.add(family)
        for e in m['endpoints']:
            if e['path'].startswith("f'"): e['path']=e['path'][2:]
            if e['path'].startswith('f"'): e['path']=e['path'][2:]
            e['host']='api-performance.ozon.ru' if family=='Performance API' else 'api-seller.ozon.ru'
            endpoints.append(e)
    t['api_family']=sorted(families) or ['Local']
    t['credentials']='Seller Client-Id + Api-Key' if families=={'Seller API'} else 'Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials' if 'Performance API' in families else 'None for local tools'
    t['permission_category']='Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use'
    t['notes']=[]
    if 'Performance API' in families:t['auth_endpoint']='POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)'
    if t['classification'] in ('WRITE','DESTRUCTIVE_OR_HIGH_RISK'):t['notes'].append('Excluded from V1; no upstream per-operation approval gate')
    if n=='ozon_pricing_strategy_products':t['notes'].append('Mixed tool: action=list reads, add/delete writes; classify entire capability as WRITE')
    if n in ('ozon_ad_statistics','ozon_returns_report','ozon_report_products_create','ozon_report_stocks_create','ozon_report_discounted_create'):t['notes'].append('Report generation creates a remote job/artifact; counted as WRITE conservatively, not business inventory mutation')
    if t.get('methods') and not endpoints:t['notes'].append('Local unsupported-endpoint stub; no data request, not a working read capability')
    if n=='ozon_diagnostics':
        t['api_family']=['Seller API','Performance API']; t['credentials']='Seller Client-Id + Api-Key; optional Performance client_id/client_secret'
        t['diagnostics']='diagnostics.py:full_diagnostics -> 2 host GETs + 12 parallel Seller probes (financial probe expands to multiple requests) + POST /v1/roles + optional Performance token POST. Typical cold-cache two-day one-page financial probe: 18 initial calls with Performance; actual count varies with pagination/cache/errors/retries.'
        t['composite_endpoints']=[{'method':method,'host':host,'path':path} for method,host,path in [
            ('GET','api-seller.ozon.ru','/'),('GET','api-performance.ozon.ru','/'),
            *[('POST','api-seller.ozon.ru',p) for p in ['/v3/product/list','/v5/product/info/prices','/v1/rating/summary','/v1/analytics/turnover/stocks','/v4/posting/fbs/list','/v1/finance/accrual/types','/v1/finance/accrual/by-day','/v1/review/list','/v1/question/list','/v3/chat/list','/v2/warehouse/list','/v1/report/list','/v1/roles']],
            ('GET','api-seller.ozon.ru','/v1/actions'),('POST','api-performance.ozon.ru','/api/client/token')]]
        t['notes'].append('Active diagnostics, not passive health; detailed network map in network-map.md')
counts=dict(collections.Counter(t['classification'] for t in tools))
(R/'tools-map.json').write_text(json.dumps({'commit':'b95da59689cdacb544c659c5c0c1fcbecc50a995','counts':counts,'tools':tools},ensure_ascii=False,indent=2),encoding='utf8')
lines=['# Complete MCP tool inventory','', 'AUDITED COMMIT: b95da59689cdacb544c659c5c0c1fcbecc50a995','', '156 tools from real stdio ListTools, matched against TOOLS and dispatch. JSON schemas below are upstream declarations, not proof of semantic validation. Full machine-readable map: [tools-map.json](tools-map.json).','',str(counts),'']
for t in tools:
    lines.extend(['## '+t['name'],'',t['description'],'',f"Classification: **{t['classification']}**. Family: {', '.join(t['api_family'])}. Credentials: {t['credentials']}.",'',f"Dispatch: `ozon_mcp/server.py:{t.get('dispatch_line','?')}`.",''])
    for m in t.get('methods',[]):
        lines.append(f"- `ozon_mcp/client.py:{m['line']}` `{m['class']}.{m['name']}`")
        for e in m['endpoints']:lines.append(f"  - `{e['method']} https://{e['host']}{e['path']}` (client.py:{e['line']})")
    if t.get('auth_endpoint'):lines.append('- Auth prerequisite: `'+t['auth_endpoint']+'`')
    if t.get('diagnostics'):lines.append(t['diagnostics'])
    lines.extend(['',t['permission_category'],'','; '.join(t['notes']),'','Input schema:','```json',json.dumps(t['inputSchema'],ensure_ascii=False,indent=2),'```',''])
(R/'tools-map.md').write_text('\n'.join(lines),encoding='utf8')
lines=['# READ / WRITE policy','', 'Classification follows actual dispatched endpoints. SAFE_READ does not mean public information. DESTRUCTIVE_OR_HIGH_RISK is included in total WRITE. Report-creation tools are conservatively WRITE even when intended for reading; ozon_report_finance_create and ozon_order_fbs_digital_act actually only read. ozon_pricing_strategy_products is mixed and is WRITE. Five unsupported stubs remain in read counts; do not treat them as working API methods.','']
for group,count in counts.items():
    lines.extend(['## '+group+f' ({count})','']+[f"- `{t['name']}`" for t in tools if t['classification']==group]+[''])
lines.extend(['## OZON_WRITE_TOOLS_NOT_FOR_V1','','All WRITE and DESTRUCTIVE_OR_HIGH_RISK entries above. Future V1 WRITE TOOLS = 0. No advertisement, report creation, price/stock edits, shipment/cancellation, customer replies, refunds, media or card mutation.'])
(R/'read-write-tools.md').write_text('\n'.join(lines),encoding='utf8')
print(counts,'total',len(tools))
