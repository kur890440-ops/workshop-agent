import json,html,re,hashlib,subprocess
from pathlib import Path
from markdown_it import MarkdownIt
R=Path(__file__).resolve().parent
md=MarkdownIt('commonmark',{'html':False}).enable('table')
def read(name):return (R/name).read_text(encoding='utf8')
def part(name,start,end=None):
    text=read(name); text=text[text.index(start):]
    if end and end in text:text=text[:text.index(end)]
    return text
sha='b95da59689cdacb544c659c5c0c1fcbecc50a995'
sections=[
 ('Executive summary',read('README.md')),
 ('Audited commit',f'**AUDITED COMMIT: {sha}**\n\nRepository: https://github.com/DeviceIngineering/ozon-mcp-server\n\nBranch main · tag v2.5.2 · audit 2026-09-27. Source checkout unchanged.'),
 ('Architecture',part('architecture.md','# Архитектура','## Transports')),
 ('Credential handling',read('credentials.md')),
 ('Network destinations / telemetry',read('network-map.md')),
 ('MCP transports',part('architecture.md','## Transports','## Web routes')),
 ('Web exposure',part('architecture.md','## Web routes')),
 ('Tool inventory',read('tools-map.md')),
 ('READ vs WRITE',read('read-write-tools.md')),
 ('Product identity',part('minimal-go-scope.md','## Product identity','## Stocks model')),
 ('Stocks model',part('minimal-go-scope.md','## Stocks model','## Prices model')),
 ('Prices model',part('minimal-go-scope.md','## Prices model','## Orders / postings')),
 ('Orders / Postings',part('minimal-go-scope.md','## Orders / postings','## Будущий')),
 ('Rate limits',part('safe-deployment.md','## Rate limits')),
 ('Dependencies / license',read('dependencies.md')),
 ('Docker',part('safe-deployment.md','## Docker','## Rate limits')),
 ('Findings',read('findings.md')),
 ('WB vs Ozon',read('wb-vs-ozon.md')),
 ('Recommended Ozon V1 Go scope',read('minimal-go-scope.md')),
 ('Final verdict',part('safe-deployment.md','# Verdict','## Docker')),
 ('Verification evidence', '## Dynamic results\n\n```json\n'+json.dumps(json.loads(read('raw/dynamic.json')),ensure_ascii=False,indent=2)+'\n```\n\n## Local process / port\n\n```json\n'+read('raw/web-dynamic.json')+'\n```\n\n## Pytest\n\n```\n'+read('raw/pytest.txt')+'\n```'),
]
nav=''.join(f'<a href="#s{i}">{i}. {html.escape(title)}</a>' for i,(title,_) in enumerate(sections,1))
body=''.join(f'<section id="s{i}"><h1>{i}. {html.escape(title)}</h1>{md.render(text)}</section>' for i,(title,text) in enumerate(sections,1))
doc='''<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Ozon MCP — audit 2026-09-27</title><style>
body{margin:0;background:#f2f5f9;color:#152334;font:16px/1.55 system-ui,sans-serif}header{padding:28px;background:#112f4c;color:white}header p{margin:8px 0}nav{padding:20px;display:flex;gap:10px;flex-wrap:wrap;background:#e2ebf4}nav a{padding:4px 8px}main{max-width:1320px;margin:auto;padding:20px}section{background:white;padding:24px;margin:18px 0;border-radius:10px;overflow-wrap:anywhere}h1,h2,h3{line-height:1.25}h1{color:#163b62}a{color:#17639d}table{border-collapse:collapse;width:100%;font-size:14px;display:block;overflow:auto}th,td{padding:10px;border:1px solid #d8e1eb;text-align:left;vertical-align:top}th{background:#edf3f9}pre{background:#f3f5f7;padding:14px;overflow:auto;max-height:650px}code{font-size:13px}input{padding:12px;font-size:16px;max-width:650px;width:85%;border:1px solid #8ca3bb;border-radius:6px}#s8 h2{border-top:2px solid #d8e1eb;padding-top:20px}footer{padding:24px;text-align:center}@media print{nav,input{display:none}section{break-inside:auto}pre{max-height:none;white-space:pre-wrap}body{background:white}}
</style></head><body><header><h1 style="color:white">Ozon MCP · технический и security-аудит</h1><p>Reference only · 156 tools · 99 READ / 57 WRITE · CRITICAL 0 / HIGH 3</p><p>AUDITED COMMIT: '''+sha+'''</p><input id="search" placeholder="Найти раздел по тексту / endpoint / tool" aria-label="Поиск по отчёту"></header><nav>'''+nav+'</nav><main>'+body+'''</main><footer>Standalone HTML · no CDN · no external scripts · no production credentials</footer><script>document.getElementById('search').addEventListener('input',function(){const q=this.value.toLowerCase().trim();document.querySelectorAll('main section').forEach(s=>{s.hidden=!!q&&!s.textContent.toLowerCase().includes(q)});});</script></body></html>'''
(R/'report.html').write_text(doc,encoding='utf8')
manifest={str(p.relative_to(R)).replace('\\','/'):hashlib.sha256(p.read_bytes()).hexdigest() for p in R.iterdir() if p.is_file() and p.suffix in ('.md','.html','.json','.py')}
(R/'raw/artifact-sha256.json').write_text(json.dumps(manifest,indent=2),encoding='utf8')
print('HTML sections',len(sections),'bytes',len(doc.encode('utf8')))
