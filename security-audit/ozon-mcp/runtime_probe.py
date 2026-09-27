import os, sys, asyncio, json, socket, subprocess, time, html
from pathlib import Path
from unittest.mock import patch
ROOT=Path(__file__).resolve().parent
for k in list(os.environ):
    if any(x in k.upper() for x in ('TOKEN','SECRET','API_KEY','OZON','WB_','TELEGRAM','PROXY')): os.environ.pop(k,None)
os.environ.update(DATA_DIR=str(ROOT/'runtime'),HEALTH_CHECK_INTERVAL_MIN='0',OZON_CLIENT_ID='dummy-client',OZON_API_KEY='dummy-key',MCP_AUTH_TOKEN='dummy-mcp-auth',PYTHONPATH=str(ROOT/'guard')+os.pathsep+str(ROOT/'source'),PYTHONUTF8='1')
sys.path.insert(0,str(ROOT/'source'))
import httpx
from ozon_mcp import client, server, toolsets
from ozon_mcp.app import fastapi_app
from fastapi.testclient import TestClient
results={}
original_connect=socket.socket.connect
def guard(sock,address):
    if isinstance(address,tuple) and address[0] not in ('127.0.0.1','::1','localhost'):
        raise RuntimeError('Audit forbids external network')
    return original_connect(sock,address)
socket.socket.connect=guard
with TestClient(fastapi_app) as web:
    results['http_unauthenticated']={p:web.get(p).status_code for p in ['/sse','/shops','/api/stats','/api/health','/openapi.json']}
    results['shop_write_unauthenticated']=web.post('/api/shops',json={'shop_id':'audit-only','name':'Dummy audit','ozon_client_id':'dummy','ozon_api_key':'dummy-only-not-real'}).status_code
    results['shop_delete_unauthenticated']=web.delete('/api/shops/audit-only').status_code
    payload="audit');audit_probe();//"
    web.post('/api/shops',json={'shop_id':payload,'name':'Dummy XSS test'})
    page=html.unescape(web.get('/shops').text)
    results['stored_js_context_injection']=f"saveShop('{payload}')" in page
    web.delete('/api/shops/'+payload)
import jsonschema
schema=next(t.inputSchema for t in server.TOOLS if t.name=='ozon_set_prices')
jsonschema.validate({'prices':[{'product_id':-1,'price':'-100'}]},schema)
results['negative_price_passes_declared_schema']=True
os.environ['OZON_TOOLSETS']='invalid-profile'
results['invalid_profile_exposes_write']=toolsets.is_enabled('ozon_set_prices')
os.environ.pop('OZON_TOOLSETS')
async def run():
    calls=[]; waits=[]
    def handler(req):
        calls.append({'method':req.method,'host':req.url.host,'path':req.url.path})
        return httpx.Response(429,headers={'Retry-After':'3600'},json={'error':'rate limit'},request=req)
    async def sleep(n): waits.append(n)
    c=client.OzonSellerClient('dummy','dummy-only')
    await c._http.aclose()
    c._http=httpx.AsyncClient(base_url=client.SELLER_BASE,transport=httpx.MockTransport(handler))
    with patch('ozon_mcp.client.asyncio.sleep',sleep):
        try: await c.product_import_prices([{'product_id':1,'price':'100'}])
        except httpx.HTTPStatusError: pass
    results['write_retry']={'requests':calls,'wait_seconds':waits}
    await c.close()
    seen=[]
    def redirect(req):
        seen.append(str(req.url)); return httpx.Response(302,headers={'Location':'https://audit.invalid/'},request=req)
    c=client.OzonSellerClient('dummy','dummy-only'); await c._http.aclose()
    c._http=httpx.AsyncClient(base_url=client.SELLER_BASE,transport=httpx.MockTransport(redirect))
    try: await c.product_list()
    except httpx.HTTPStatusError: pass
    results['redirect_requests']=seen; await c.close()
    async def synthetic_error(*args):raise ValueError('DUMMY_SENSITIVE_ERROR_MARKER')
    with patch.object(server,'_call_tool_impl',synthetic_error):
        response=await server.call_tool('ozon_product_list',{})
        results['raw_error_marker_returned']=any('DUMMY_SENSITIVE_ERROR_MARKER' in x.text for x in response)
    from mcp import ClientSession,StdioServerParameters
    from mcp.client.stdio import stdio_client
    params=StdioServerParameters(command=sys.executable,args=['-m','ozon_mcp.server'],env=dict(os.environ))
    async with stdio_client(params) as (read,write):
        async with ClientSession(read,write) as session:
            init=await session.initialize(); tools=await session.list_tools()
            results['stdio']={'server':init.serverInfo.model_dump(),'tools':len(tools.tools),'names':[t.name for t in tools.tools]}
            # Only local read tool. No Ozon API request.
            result=await session.call_tool('ozon_list_shops',{})
            results['stdio']['local_tool_ok']=not result.isError
    results['stdio']['session_closed']=True
asyncio.run(run())
results['files_created']=[str(p.relative_to(ROOT)) for p in (ROOT/'runtime').rglob('*') if p.is_file()]
(ROOT/'raw/dynamic.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps({k:v for k,v in results.items() if k!='stdio'},indent=2))
print('MCP stdio initialize / ListTools:',results['stdio']['tools'],'tools; local tool OK; session closed')
