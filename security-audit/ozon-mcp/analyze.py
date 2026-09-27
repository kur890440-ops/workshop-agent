import ast, json, os, re, subprocess, sys, hashlib
from pathlib import Path
ROOT=Path(__file__).resolve().parent
SRC=ROOT/'source'
RAW=ROOT/'raw'; RAW.mkdir(exist_ok=True)
for k in list(os.environ):
    if any(x in k.upper() for x in ('TOKEN','SECRET','API_KEY','OZON','WB_','TELEGRAM')): os.environ.pop(k,None)
os.environ.update(DATA_DIR=str(ROOT/'runtime'), HEALTH_CHECK_INTERVAL_MIN='0', OZON_CLIENT_ID='dummy-client', OZON_API_KEY='dummy-key')
sys.path.insert(0,str(SRC))
from ozon_mcp.server import TOOLS
client=ast.parse((SRC/'ozon_mcp/client.py').read_text(encoding='utf8'))
server=ast.parse((SRC/'ozon_mcp/server.py').read_text(encoding='utf8'))
classes={c.name:{m.name:m for m in c.body if isinstance(m,(ast.FunctionDef,ast.AsyncFunctionDef))} for c in client.body if isinstance(c,ast.ClassDef)}
def endpoints(cls,m,seen=None):
    seen=set() if seen is None else seen
    if m in seen or m not in classes[cls]: return []
    seen.add(m); out=[]
    for c in ast.walk(classes[cls][m]):
        if not isinstance(c,ast.Call) or not isinstance(c.func,ast.Attribute): continue
        attr=c.func.attr; receiver=ast.unparse(c.func.value)
        if receiver=='self' and attr in ('_get','_post','_put') and c.args:
            out.append({'method':attr[1:].upper(),'path':ast.unparse(c.args[0]).strip("'\""),'line':c.lineno})
        elif receiver=='self._http' and attr in ('get','post','put','delete','patch') and c.args:
            out.append({'method':attr.upper(),'path':ast.unparse(c.args[0]).strip("'\""),'line':c.lineno})
        elif receiver=='self' and attr not in ('_send','_get','_post','_put'):
            out.extend(endpoints(cls,attr,seen))
    return list({(e['method'],e['path']):e for e in out}.values())
dispatch={}
for n in ast.walk(server):
    if not isinstance(n,ast.If) or not isinstance(n.test,ast.Compare): continue
    if ast.unparse(n.test.left)!='name' or len(n.test.comparators)!=1 or not isinstance(n.test.comparators[0],ast.Constant): continue
    name=n.test.comparators[0].value; methods=[]
    for stmt in n.body:
        for c in ast.walk(stmt):
            if isinstance(c,ast.Call) and isinstance(c.func,ast.Attribute) and ast.unparse(c.func.value) in ('s','p'):
                cls='OzonSellerClient' if ast.unparse(c.func.value)=='s' else 'OzonPerformanceClient'
                methods.append({'class':cls,'name':c.func.attr,'line':classes[cls].get(c.func.attr,n).lineno,'endpoints':endpoints(cls,c.func.attr)})
    dispatch[name]={'dispatch_line':n.lineno,'methods':methods}
out=[]
for t in TOOLS:
    d=t.model_dump(); d.update(dispatch.get(t.name,{})); out.append(d)
(RAW/'tools-inventory.json').write_text(json.dumps(out,ensure_ascii=False,indent=2),encoding='utf8')
(RAW/'tool-review.txt').write_text('\n'.join(f"{t['name']} | {t['description'][:130]} | "+'; '.join(m['name']+':'+','.join(e['method']+' '+e['path'] for e in m['endpoints']) for m in t.get('methods',[])) for t in out),encoding='utf8')
patterns={'network':r'https?://|httpx|requests\.|urllib|socket|websocket|grpc|aiohttp','secrets':r'Client-Id|Api-Key|Authorization|client_secret|api_key|encryption|token|credential','danger':r'subprocess|os\.system|eval\(|exec\(|pickle|yaml\.load|verify=False|follow_redirects|open\(|write_text|write_bytes','telemetry':r'Sentry|sentry|telemetry|opentelemetry|posthog|mixpanel|exporter'}
lines=[]
for file in SRC.rglob('*'):
    if not file.is_file() or '.git' in file.parts or '__pycache__' in file.parts: continue
    if file.suffix.lower() not in ('.py','.toml','.yml','.yaml','.html','.sh'): continue
    for num,line in enumerate(file.read_text(encoding='utf8',errors='replace').splitlines(),1):
        for group,pattern in patterns.items():
            if re.search(pattern,line,re.I): lines.append(f'{group} {file.relative_to(SRC)}:{num}: {line}')
(RAW/'static-search.txt').write_text('\n'.join(lines),encoding='utf8')
print('TOOLS',len(out),'unmapped',[t['name'] for t in out if not t.get('methods')])
