import json,re,socket
from pathlib import Path
from html.parser import HTMLParser
R=Path(__file__).resolve().parent
needed=['README.md','findings.md','architecture.md','network-map.md','credentials.md','tools-map.md','read-write-tools.md','dependencies.md','minimal-go-scope.md','wb-vs-ozon.md','safe-deployment.md','report.html','raw/static-search.txt','raw/dependency-scan.txt','raw/secret-scan.txt']
assert all((R/p).is_file() and (R/p).stat().st_size for p in needed)
data=json.loads((R/'tools-map.json').read_text(encoding='utf8'))
dyn=json.loads((R/'raw/dynamic.json').read_text(encoding='utf8'))
assert len(data['tools'])==156==dyn['stdio']['tools']
assert {t['name'] for t in data['tools']}==set(dyn['stdio']['names'])
assert sum(data['counts'].values())==156
assert all(t.get('inputSchema') and t.get('classification') and t.get('dispatch_line') for t in data['tools'])
assert dyn['shop_write_unauthenticated']==dyn['shop_delete_unauthenticated']==200
assert len(dyn['write_retry']['requests'])==4 and dyn['write_retry']['wait_seconds']==[10,10,10]
assert dyn['negative_price_passes_declared_schema'] and dyn['stored_js_context_injection']
class Parser(HTMLParser):
    def __init__(self):super().__init__();self.sections=0;self.extern=[];self.broken=[]
    def handle_starttag(self,tag,attrs):
        d=dict(attrs)
        if tag=='section':self.sections+=1
        if tag in ('script','link','img','iframe') and (d.get('src','').startswith(('http:','https:','//')) or d.get('href','').startswith(('http:','https:','//'))):self.extern.append(d)
        if tag=='a':
            target=d.get('href','')
            if target and not target.startswith(('http:','https:','#','mailto:')) and not (R/target.split('#')[0]).exists():self.broken.append(target)
p=Parser();p.feed((R/'report.html').read_text(encoding='utf8'))
assert p.sections>=20 and not p.extern and not p.broken,(p.sections,p.extern,p.broken)
web=json.loads((R/'raw/web-dynamic.json').read_text(encoding='utf8'))
assert web['process_exited'] and web['port_closed_after_exit']
with socket.socket() as s:assert s.connect_ex(('127.0.0.1',web['port']))!=0
parity=json.loads((R/'raw/pypi-comparison.json').read_text(encoding='utf8'))
assert all(a['sha256']==a['index_sha256'] and not a['normalized_mismatches'] for a in parity['artifacts'])
print('PASS: required artifacts, 156 live tools/schema map, mock evidence, package hashes/parity, 21 HTML sections, no external HTML assets, no broken local links, local process port closed.')
