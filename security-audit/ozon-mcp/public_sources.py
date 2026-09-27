import urllib.request,json,hashlib,zipfile,io
from pathlib import Path
ROOT=Path(__file__).resolve().parent
def get(url):
    with urllib.request.urlopen(url,timeout=40) as r: return r.read()
d=json.loads(get('https://pypi.org/pypi/ozon-mcp-server/2.5.2/json'))
out={'url':'https://pypi.org/pypi/ozon-mcp-server/2.5.2/json','version':d['info']['version'],'artifacts':[]}
for artifact in d['urls']:
    blob=get(artifact['url']); (ROOT/'raw'/artifact['filename']).write_bytes(blob)
    item={'filename':artifact['filename'],'sha256':hashlib.sha256(blob).hexdigest(),'index_sha256':artifact['digests']['sha256'],'matches':[],'mismatches':[]}
    if artifact['filename'].endswith('.whl'):
        with zipfile.ZipFile(io.BytesIO(blob)) as z:
            for n in z.namelist():
                if n.startswith('ozon_mcp/') and not n.endswith('/'):
                    p=ROOT/'source'/n
                    (item['matches'] if p.exists() and p.read_bytes()==z.read(n) else item['mismatches']).append(n)
    out['artifacts'].append(item)
(ROOT/'raw/pypi-comparison.json').write_text(json.dumps(out,indent=2),encoding='utf8')
print([(a['filename'],len(a['matches']),a['mismatches']) for a in out['artifacts']])
