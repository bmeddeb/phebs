from pathlib import Path
import json,os,shutil,socket,sys,time,urllib.request
ROOT=Path('/Users/ben/.codex/worktrees/t442a-preview-recovery/phebs.com');QA=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442a-qa-uz1tx612');OUT=QA/'assessment-b';UI=ROOT/'ui'
sys.path.insert(0,str(ROOT/'spike/t503'))
from requalification_browser import OwnedChild
NODE=json.loads((QA/'plan.json').read_text())['node'];CHROME='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';CLI='/Users/ben/.agents/skills/impeccable/scripts/impeccable'
owned=[];result={'schema':'phebs-t442a-assessment-b-v1','terminals':{},'owners':{},'visible_overlay':False,'live_background_mode':'OwnedChild process, foreground engine; no detached daemon'};start=time.monotonic();profile=OUT/'private-chrome-profile';failure=None

def port():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
def left():
 remain=60-(time.monotonic()-start)
 assert remain>0,'60s work allowance reached;30s cleanup reserve'
 return remain
def own(name,argv):
 c=OwnedChild(argv,OUT,name);owned.append((name,c));result['owners'][name]={'pid':c.child.pid,'pgid':os.getpgid(c.child.pid),'argv':argv};return c
def ready(url,child):
 deadline=time.monotonic()+min(7,left())
 while True:
  assert child.child.poll() is None,'server exited before readiness'
  try:
   with urllib.request.urlopen(url,timeout=1) as r:assert r.status==200
   return
  except OSError:
   assert time.monotonic()<deadline,'server readiness timeout';time.sleep(.05)
try:
 base='http://127.0.0.1:'+str(port());web=own('vite',[NODE,str(UI/'node_modules/vite/bin/vite.js'),'preview',str(UI),'--outDir',str(UI/'dist'),'--host','127.0.0.1','--port',base.rsplit(':',1)[1],'--strictPort']);ready(base,web)
 chrome=own('chrome',[CHROME,'--headless=new','--no-first-run','--no-default-browser-check','--disable-background-networking','--disable-component-update','--disable-sync','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+str(profile),'about:blank']);active=profile/'DevToolsActivePort';deadline=time.monotonic()+min(7,left())
 while not active.exists():assert chrome.child.poll() is None and time.monotonic()<deadline;time.sleep(.03)
 cdp='http://127.0.0.1:'+active.read_text().splitlines()[0];(OUT/'input.json').write_text(json.dumps({'cdp':cdp,'base':base,'out':str(OUT)}))
 client=own('client',[NODE,str(OUT/'client.cjs'),str(OUT/'input.json')]);deadline=time.monotonic()+min(15,left())
 while not (OUT/'mutable-preflight.json').exists():assert client.child.poll() is None and time.monotonic()<deadline,'mutable preflight did not complete';time.sleep(.05)
 liveurl='http://127.0.0.1:'+str(port());prior=os.getcwd();os.chdir(OUT)
 try:live=own('impeccable-live',[CLI,'live-server','--port='+liveurl.rsplit(':',1)[1]])
 finally:os.chdir(prior)
 ready(liveurl+'/health',live);(OUT/'live-url.json').write_text(json.dumps({'url':liveurl}));result['mutable_preflight_before_live_server']=True
 client.child.wait(timeout=left());assert client.child.returncode==0,'browser assessment stopped';result['outcome']='passed'
except BaseException as error:failure=error;result.update(outcome='stopped',error=str(error),error_type=type(error).__name__)
finally:
 for name,c in reversed(owned):
  try:result['terminals'][name]=dict(c.stop(),joined=True)
  except BaseException as error:
   failure=failure or error;result['terminals'][name]={'joined':False,'error':str(error),'exit_code':c.child.returncode}
 clean=len(result['terminals'])==len(owned) and all(v.get('joined') and v.get('teardown')=='clean' for v in result['terminals'].values());result['all_owned_groups_joined_cleanly']=clean
 if clean and profile.exists():shutil.rmtree(profile)
 result['seconds']=round(time.monotonic()-start,3);result['within90s']=result['seconds']<=90
 if not clean or not result['within90s']:failure=failure or RuntimeError('custody/deadline failed');result['outcome']='stopped'
 (OUT/'owned-result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)
if failure:sys.exit(1)
