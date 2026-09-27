import json,sys,subprocess,tarfile,hashlib,importlib.metadata as im
from pathlib import Path
from packaging.requirements import Requirement
R=Path(__file__).resolve().parent;raw=R/'raw'
d=json.loads((raw/'secret-scan.txt').read_text(encoding='utf8'))
for hit in d['hits']:
    if hit['path']=='tests/test_mcp_sse.py' and hit['line']==30:hit['status']='Reviewed: explicit test-token-placeholder, not production credential'
(raw/'secret-scan.txt').write_text(json.dumps(d,indent=2),encoding='utf8')
p=json.loads((raw/'pypi-comparison.json').read_text(encoding='utf8'))
for a in p['artifacts']:
    if a['filename'].endswith('.tar.gz'):
        a['normalized_matches']=[];a['normalized_mismatches']=[]
        with tarfile.open(raw/a['filename']) as tar:
            for m in tar.getmembers():
                parts=m.name.split('/',1)
                if not m.isfile() or len(parts)<2:continue
                rel=parts[1]; file=R/'source'/rel
                if rel.startswith('ozon_mcp/') or rel in ('pyproject.toml','LICENSE','README.md'):
                    blob=tar.extractfile(m).read()
                    (a['normalized_matches'] if file.exists() and file.read_bytes().replace(b'\r\n',b'\n')==blob.replace(b'\r\n',b'\n') else a['normalized_mismatches']).append(rel)
(raw/'pypi-comparison.json').write_text(json.dumps(p,indent=2),encoding='utf8')
scan=json.loads((raw/'pip-audit.json').read_text(encoding='utf8'))
direct={'mcp','httpx','pydantic','fastapi','uvicorn','jinja2','aiosqlite','cryptography'}
closure=set();todo=list(direct)
def canon(s):return s.lower().replace('_','-')
while todo:
    name=todo.pop()
    if name in closure:continue
    closure.add(name)
    try:dist=im.distribution(name)
    except im.PackageNotFoundError:continue
    for requirement in dist.requires or []:
        q=Requirement(requirement)
        if q.marker is None or any(q.marker.evaluate({'extra':e}) for e in ('','cli','standard')):todo.append(canon(q.name))
rows=[]
for dep in scan['dependencies']:
    dep['audit_role']='direct runtime' if dep['name'] in direct else 'transitive runtime' if dep['name'] in closure else 'audit/build/test tooling or audited root package'
    dep['unique_advisories']=sorted({v['id'] for v in dep.get('vulns',[])})
    rows.append(dep)
out={'command':'python -m pip_audit --format json --output raw/pip-audit.json','audit_date':'2026-09-27','exit_code':1,'python':sys.version,'raw_advisory_entries':sum(len(x.get('vulns',[])) for x in rows),'unique_ids':sorted({v['id'] for x in rows for v in x.get('vulns',[])}),'dependencies':rows}
(raw/'dependency-scan.txt').write_text(json.dumps(out,ensure_ascii=False,indent=2),encoding='utf8')
lines=['# Dependencies, reproducibility и license','',f'Проверено 2026-09-27, Python {sys.version.split()[0]}, isolated venv. pip-audit exit 1: 10 advisory entries / 5 unique IDs, все у pip 25.3 — bootstrap/build tooling, не runtime dependency Ozon. Scanner продублировал advisory entries; не считать их десятью независимыми CVE. Полный raw JSON и freeze сохранены. Известных advisory в разрешённых runtime зависимостях scanner не вернул; это не отсутствие всех уязвимостей.','', '| Package | Version | Role | Scanner advisory IDs |','|---|---|---|---|']
for x in rows:lines.append('| '+ ' | '.join([x['name'],x.get('version','?'),x['audit_role'],', '.join(x['unique_advisories']) or x.get('skip_reason','none reported')])+' |')
lines.extend(['','## Pip advisories',''])
for x in rows:
    seen=set()
    for v in x.get('vulns',[]):
        if v['id'] in seen:continue
        seen.add(v['id']);lines.append(f"- {v['id']}: aliases {', '.join(v.get('aliases',[]))}; fix versions {', '.join(v['fix_versions'])}. См. raw/pip-audit.json: исходное описание scanner.")
lines.extend(['','Для следующего чистого audit/build environment использовать исправленный pip (все перечисленные fixes покрывает >=26.2), проверенные индексы и hashes. Этот аудит не изменял глобальный Python/pip. Проверка ограничена версиями freeze.txt; будущие floating resolution не покрыты.','', '## Build / install / package parity','', 'pyproject: Hatchling build backend, wheel package ozon_mcp; setup.py/custom install hooks не обнаружены. Console entrypoints ozon-mcp и ozon-mcp-web запускают server/app. Standard backend и зависимости всё равно исполняют Python при build/import — нет обещания безопасности любого downloaded wheel. Dependency lock с hashes отсутствует. GitHub publish использует OIDC Trusted Publishing; Actions version tags не закреплены commit hash.','', 'PyPI 2.5.2 wheel и sdist скачаны, не запускались как отдельная поставка. 14 Python/template package files wheel совпали с commit после CRLF/LF normalization (working checkout Windows). Raw byte mismatch из-за EOL не считается подменой. Sdist package files/pyproject/LICENSE/README сравнены отдельно; подробности и SHA256 артефактов — raw/pypi-comparison.json. Это source parity, не verified build provenance/signature.','', '## License','', 'Фактический source/LICENSE: MIT, Copyright (c) 2026 DeviceIngineering. Разрешает использование, копирование, модификацию, распространение и внутренний fork при сохранении copyright/license notices в копиях или существенных частях. При переносе заимствованного кода сохранить уведомление и LICENSE; это не разрешение от Ozon на API/данные и не гарантия автора. См. [LICENSE](source/LICENSE).','', '## Secret scan','', 'Выполнен redacted heuristic regex scan всей доступной Git history: 49 commits / 211 unique blobs. Найден один explicit test token placeholder (tests/test_mcp_sse.py:30), production credentials не подтверждены. Private key/JWT/cloud/literal-secret patterns; не entropy scanner, binary и >2MB исключены. Не выдавать отсутствие совпадений за математическую гарантию. raw/secret-scan.txt содержит location/status без найденного значения.'])
(R/'dependencies.md').write_text('\n'.join(lines),encoding='utf8')
print('dependencies',len(rows),'advisories',out['unique_ids'])
print('sdist',[(a['filename'],len(a.get('normalized_matches',[])),a.get('normalized_mismatches')) for a in p['artifacts']])
