from pathlib import Path
import hashlib,json,os,shutil,socket,sys,time,urllib.request,traceback
ROOT=Path('/Users/ben/.codex/worktrees/t442b-key-copy/phebs.com');QA=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442b-qa-rg7rva8k');RUN=QA/'assessment-b';UI=ROOT/'ui'
sys.path.insert(0,str(ROOT/'spike/t503'))
from requalification_browser import OwnedChild
from requalification_checks import Deadline
NODE=Path(json.loads((QA/'plan.json').read_text())['node']);CHROME=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome');IMPECCABLE=Path('/Users/ben/.agents/skills/impeccable/scripts/impeccable')
def port():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
clock=Deadline(90);owned=[];profile=RUN/'private-chrome-profile';failure=None
r={'schema':'phebs-t442b-assessment-b-custody-v1','outcome':'preparing','owners':{},'terminals':{},'source_sha256':sha(ROOT/'ui/src/pages/SettingsPage.tsx'),'detector':{'invocations':1,'exit_code':0,'json':[]},'native_renderer_claim':'Darwin installed Chrome; headless fallback only; no visible Human tab','live_server_mode':'foreground command owned in background process group; no detached daemon'}
def left():
 n=clock.remaining()-30;assert n>0,'60s work ceiling with30s joined cleanup reserve';return n
def spawn(name,argv):
 c=OwnedChild(argv,RUN,name);owned.append((name,c));r['owners'][name]={'pid':c.child.pid,'expected_pgid':c.child.pid,'observed_pgid':os.getpgid(c.child.pid),'argv':argv};return c
def ready(url,child):
 until=time.monotonic()+min(8,left())
 while True:
  assert child.child.poll() is None,'server exited before readiness'
  try:
   with urllib.request.urlopen(url,timeout=1) as response:assert response.status==200;return
  except OSError:assert time.monotonic()<until,'server readiness deadline';left();time.sleep(.05)
try:
 wp,lp=port(),port();base='http://127.0.0.1:'+str(wp);live='http://127.0.0.1:'+str(lp)
 web=spawn('vite',[str(NODE),str(UI/'node_modules/vite/bin/vite.js'),'preview',str(UI),'--outDir',str(UI/'dist'),'--host','127.0.0.1','--port',str(wp),'--strictPort']);ready(base,web)
 os.chdir(RUN)
 server=spawn('impeccable',[str(IMPECCABLE),'live-server','--port='+str(lp)]);ready(live+'/health',server)
 chrome=spawn('chrome',[str(CHROME),'--headless=new','--no-first-run','--no-default-browser-check','--disable-background-networking','--disable-component-update','--disable-sync','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+str(profile),'about:blank'])
 active=profile/'DevToolsActivePort';until=time.monotonic()+min(5,left())
 while not active.exists():assert chrome.child.poll() is None,'Chrome exited';assert time.monotonic()<until,'Chrome readiness deadline';left();time.sleep(.02)
 cdp='http://127.0.0.1:'+active.read_text().splitlines()[0]
 with urllib.request.urlopen(cdp+'/json/version',timeout=1) as response:r['renderer']=json.load(response)
 os.environ.update(B_OUTPUT=str(RUN),B_BASE=base,B_LIVE=live,B_CDP=cdp)
 client=spawn('client',[str(NODE),str(RUN/'client.cjs')]);client.child.wait(timeout=left());assert client.child.returncode==0,'browser client stopped'
 r['browser_result']=json.loads((RUN/'browser-result.json').read_text());assert r['browser_result']['outcome']=='passed';r['outcome']='passed'
except BaseException as error:
 failure=error;r.update(outcome='stopped',error_type=type(error).__name__,error=str(error),traceback=traceback.format_exc())
finally:
 for name,child in reversed(owned):
  try:r['terminals'][name]=dict(child.stop(),**r['owners'][name],joined=True)
  except BaseException as error:
   failure=failure or error;r['outcome']='stopped';terminal=dict(r['owners'][name],cleanup_error=str(error),cleanup_traceback=traceback.format_exc(),joined=False,returncode_at_error=child.child.returncode)
   try:terminal['exit_code']=child.child.wait(timeout=3);terminal['joined']=True;terminal['readonly_known_group_followup']=__import__('qualify').serve.receipts._drain_group(child.child.pid,False)
   except BaseException:terminal['followup_traceback']=traceback.format_exc()
   child.log.close();r['terminals'][name]=terminal
 clean=len(r['terminals'])==len(owned) and all(x.get('teardown')=='clean' and x.get('termination_mode')!='sigkill' for x in r['terminals'].values())
 r['all_owned_groups_joined_cleanly']=clean
 if clean and profile.exists():shutil.rmtree(profile)
 r['profile_removed']=not profile.exists();r['source_final_sha256']=sha(ROOT/'ui/src/pages/SettingsPage.tsx');assert r['source_final_sha256']==r['source_sha256']
 r['total_seconds']=round(clock.elapsed(),6)
 if r['total_seconds']>90:failure=failure or TimeoutError('90s joined ceiling exceeded');r['outcome']='stopped'
 (RUN/'result.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps({'outcome':r['outcome'],'seconds':r['total_seconds'],'joined_cleanly':clean,'result':str(RUN/'result.json'),'error':r.get('error')}),flush=True)
if failure:raise SystemExit(1)
