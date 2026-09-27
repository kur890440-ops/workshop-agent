import os,sys,subprocess,json,re,hashlib
from pathlib import Path
ROOT=Path(__file__).resolve().parent; SRC=ROOT/'source'; RAW=ROOT/'raw'
env={k:v for k,v in os.environ.items() if not any(x in k.upper() for x in ('TOKEN','SECRET','API_KEY','OZON','WB_','TELEGRAM','PROXY'))}
env.update(DATA_DIR=str(ROOT/'test-runtime'),HEALTH_CHECK_INTERVAL_MIN='0',PYTHONPATH=str(ROOT/'guard')+os.pathsep+str(SRC),PYTHONUTF8='1')
# Tests' child fixtures override PYTHONPATH; they only initialize/list tools or use dummy mocked HTTP.
r=subprocess.run([sys.executable,'-m','pytest',str(SRC/'tests'),'-q','--basetemp',str(ROOT/'pytest-temp')],env=env,cwd=ROOT,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
(RAW/'pytest.txt').write_bytes(r.stdout); print('pytest exit',r.returncode, r.stdout.decode('utf8',errors='replace')[-1600:])
def git(*args):return subprocess.check_output(['git','-C',str(SRC),*args])
commits=git('rev-list','--all').decode().splitlines(); blobs={}
for commit in commits:
    for entry in git('ls-tree','-r',commit).decode('utf8',errors='replace').splitlines():
        meta,path=entry.split('\t',1); mode,kind,oid=meta.split()
        if kind=='blob': blobs.setdefault(oid,(commit,path))
patterns={
 'private-key':r'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',
 'cloud-key':r'AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{30,}',
 'literal-secret':r'''(?i)(?:api[_-]?key|client_secret|password|bearer|token)\s*[=:]\s*["']([A-Za-z0-9_./+\-=]{16,})["']''',
 'jwt':r'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{15,}'
}
hits=[]
for oid,(commit,path) in blobs.items():
    data=git('cat-file','blob',oid)
    if b'\0' in data or len(data)>2_000_000:continue
    for n,line in enumerate(data.decode('utf8',errors='replace').splitlines(),1):
        for name,pat in patterns.items():
            if re.search(pat,line): hits.append({'type':name,'commit':commit,'path':path,'line':n,'value':'[REDACTED]','status':'candidate; manual review required','blob':oid})
(RAW/'secret-scan.txt').write_text(json.dumps({'scanner':'audit heuristic regex, no secret validation/network','commits':len(commits),'unique_blobs':len(blobs),'hits':hits,'limitations':'Not entropy scanner; binary and >2MB blobs excluded; absence is not proof'},indent=2),encoding='utf8')
print('secret scan:',len(commits),'commits',len(blobs),'blobs',len(hits),'candidates')
freeze=subprocess.check_output([sys.executable,'-m','pip','freeze'],env=env)
(RAW/'freeze.txt').write_bytes(freeze)
