import os,sys,subprocess,socket,time,json
from pathlib import Path
import httpx
R=Path(__file__).resolve().parent
env={k:v for k,v in os.environ.items() if not any(s in k.upper() for s in ('TOKEN','SECRET','API_KEY','OZON','WB_','TELEGRAM','PROXY'))}
env.update(DATA_DIR=str(R/'web-runtime'),HEALTH_CHECK_INTERVAL_MIN='0',OZON_CLIENT_ID='dummy-client',OZON_API_KEY='dummy-only',PYTHONPATH=os.pathsep.join([str(R/'guard'),str(R/'source'),str(R/'.venv/Lib/site-packages')]),PYTHONUTF8='1')
with socket.socket() as s:s.bind(('127.0.0.1',0)); port=s.getsockname()[1]
with (R/'raw/web-process.txt').open('wb') as log:
    bootstrap=f"import site,runpy; site.addsitedir({str(R/'.venv/Lib/site-packages')!r}); runpy.run_module('uvicorn',run_name='__main__')"
    proc=subprocess.Popen([sys._base_executable,'-c',bootstrap,'ozon_mcp.app:fastapi_app','--host','127.0.0.1','--port',str(port),'--log-level','warning'],env=env,cwd=R,stdout=log,stderr=log)
    try:
        for _ in range(100):
            if proc.poll() is not None:raise RuntimeError('web process exited')
            try:
                response=httpx.get(f'http://127.0.0.1:{port}/api/health',timeout=.5,trust_env=False)
                if response.status_code==200:break
            except httpx.HTTPError:pass
            time.sleep(.1)
        else:raise RuntimeError('web startup timeout')
        rows=subprocess.check_output(['netstat','-ano','-p','tcp']).decode(errors='replace').splitlines()
        owned=[row.strip() for row in rows if row.split() and (row.split()[-1]==str(proc.pid) or f'127.0.0.1:{port}' in row)]
        out={'pid':proc.pid,'bind_override':'127.0.0.1','port':port,'health_status':response.status_code,'auth_enabled':response.json().get('auth_enabled'),'tcp_connections':owned,'external_network':'child socket guard rejects non-loopback TCP; no browser loaded, so CDN not requested','files':[str(p.relative_to(R)) for p in (R/'web-runtime').rglob('*') if p.is_file()]}
    finally:
        proc.terminate()
        proc.wait(timeout=10)
out['process_exited']=proc.poll() is not None
with socket.socket() as closed:
    out['port_closed_after_exit']=closed.connect_ex(('127.0.0.1',port))!=0
(R/'raw/web-dynamic.json').write_text(json.dumps(out,indent=2),encoding='utf8');print(json.dumps(out,indent=2))
