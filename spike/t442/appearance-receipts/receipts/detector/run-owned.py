from pathlib import Path
import hashlib,json,os,shutil,socket,sys,time,traceback,urllib.request
ROOT=Path('/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com'); UI=ROOT/'ui'
QA=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442-appearance-835thdfq'); run=QA/'detector'
sys.path.insert(0,str(ROOT/'spike/t503'))
from requalification_browser import OwnedChild
from requalification_checks import Deadline
NODE=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t574-tools-gbrqb_vk/node-v24.18.0-darwin-arm64/bin/node')
CHROME=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
LAUNCHER='/Users/ben/.agents/skills/impeccable/scripts/impeccable'
DIST=Path('/Users/ben/.codex/worktrees/t25-embedded-docs/phebs.com/ui/dist')
def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def verify():
    plan=json.loads((QA/'plan.json').read_text()); census={}
    for name,base in [('production_inputs',ROOT),('production_dist',Path(plan['dist_root']))]:
        document=plan[name]
        for row in document['rows']:
            path=base/row['path'];assert path.is_file() and path.stat().st_size==row['bytes'] and sha(path)==row['sha256'],str(path)
        census[name]={'rows':len(document['rows']),'digest':document['digest']}
    assert sha(NODE)=='ee6fb0e015284d83a91e8ec5213f43a157f8a392b58555301682892ba928c04a'
    return census

def port():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
assert not (run/'result.json').exists() and (run/'cli.json').exists() and (run/'cli-terminal.json').exists(),'browser not repeated; CLI already retained'
result={'schema':'phebs-t442-assessment-b-v1','outcome':'preparing','commands':[],'terminals':{},'owners':{},'browser_plugin':'unavailable; cached regular Playwright fallback','user_visible_overlay':False}
owned=[];profile=run/'private-chrome-profile';failure=None
try:
    clock=Deadline(180);result['initial_census']=verify()
    def left():
        value=clock.remaining()-30;assert value>0,'work deadline reached;30s cleanup reserve';return value
    def start(name,argv,cwd=ROOT):
        result['commands'].append({'name':name,'argv':argv,'cwd':str(cwd)})
        os.chdir(cwd)
        try: child=OwnedChild(argv,run,name)
        finally: os.chdir(ROOT)
        owned.append((name,child));result['owners'][name]={'pid':child.child.pid,'expected_pgid':child.child.pid,'observed_pgid':os.getpgid(child.child.pid)}
        assert result['owners'][name]['observed_pgid']==child.child.pid,'child group ownership differs'
        return child
    result['cli']=json.loads((run/'cli-terminal.json').read_text())
    assert result['cli']['invocations']==1 and result['cli']['exit_code'] in (0,2) and result['cli']['terminal']['teardown']=='clean'
    result['cli_findings']=json.loads((run/'cli.json').read_text())
    webport=port();base='http://127.0.0.1:'+str(webport)
    web=start('vite',[str(NODE),str(UI/'node_modules/vite/bin/vite.js'),'preview',str(UI),'--outDir',str(DIST),'--host','127.0.0.1','--port',str(webport),'--strictPort'])
    ready=time.monotonic()+min(10,left())
    while True:
        assert web.child.poll() is None,'Vite exited before readiness'
        try:
            with urllib.request.urlopen(base,timeout=1) as response:assert response.status==200 and b'phebs' in response.read(2048)
            break
        except OSError:
            assert time.monotonic()<ready,'Vite readiness deadline';time.sleep(.05)
    chrome=start('chrome',[str(CHROME),'--headless=new','--no-first-run','--no-default-browser-check','--disable-background-networking','--disable-component-update','--disable-sync','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+str(profile),'about:blank'])
    ready=time.monotonic()+min(5,left());active=profile/'DevToolsActivePort'
    while not active.exists():
        assert chrome.child.poll() is None,'Chrome exited before readiness';assert time.monotonic()<ready,'Chrome readiness deadline';left();time.sleep(.02)
    cdp='http://127.0.0.1:'+active.read_text().splitlines()[0]
    with urllib.request.urlopen(cdp+'/json/version',timeout=1) as response:result['renderer']=json.load(response)
    os.environ.update(T442_CDP=cdp,T442_B_OUT=str(run),PHEBS_RECEIPTS_URL=base)
    client=start('client',[str(NODE),str(run/'browser.cjs')])
    ready=time.monotonic()+min(15,left())
    while not (run/'preflight.json').exists():
        assert client.child.poll() is None,'browser client exited before mutable preflight';assert time.monotonic()<ready,'mutable preflight deadline';left();time.sleep(.05)
    result['mutable_preflight']=json.loads((run/'preflight.json').read_text());assert result['mutable_preflight']['passed']
    livedir=run/'live-workset';livedir.mkdir(mode=0o700);liveport=port()
    live=start('live-server',[LAUNCHER,'live-server','--port='+str(liveport)],livedir)
    livebase='http://127.0.0.1:'+str(liveport);ready=time.monotonic()+min(5,left())
    while True:
        assert live.child.poll() is None,'live server exited before readiness'
        try:
            with urllib.request.urlopen(livebase+'/health',timeout=1) as response:assert response.status==200
            break
        except OSError:
            assert time.monotonic()<ready,'live server readiness deadline';time.sleep(.05)
    result['live_server_started_after_mutation']=True
    (run/'live.json').write_text(json.dumps({'detectURL':livebase+'/detect.js'}))
    client.child.wait(timeout=min(30,left()));assert client.child.returncode==0,'browser injection client failed'
    result['browser_result']=str(run/'browser-result.json');browser=json.loads((run/'browser-result.json').read_text());assert browser['outcome']=='passed' and browser['injection']
    result['outcome']='passed'
except BaseException as error:
    failure=error;result.update(outcome='stopped',error_type=type(error).__name__,error=str(error),traceback=traceback.format_exc())
finally:
    for kind,child in reversed(owned):
        try:result['terminals'][kind]=dict(child.stop(),**result['owners'][kind],joined=True)
        except BaseException as error:
            failure=failure or error;result.update(outcome='stopped');terminal=dict(result['owners'][kind],cleanup_error=str(error),cleanup_traceback=traceback.format_exc(),joined=False)
            try:
                terminal['exit_code']=child.child.wait(timeout=3);terminal['joined']=True;terminal['teardown']=__import__('qualify').serve.receipts._drain_group(child.child.pid,False)
            except BaseException:terminal['followup_wait_traceback']=traceback.format_exc()
            child.log.close();result['terminals'][kind]=terminal
    drained=all(v.get('teardown')=='clean' and v.get('termination_mode')!='sigkill' for v in result['terminals'].values())
    if drained and profile.exists():shutil.rmtree(profile)
    result['all_owned_groups_joined_cleanly']=drained and len(result['terminals'])==len(owned)
    result['private_browser_profile_removed']=not profile.exists()
    if 'clock' in locals():
        result['total_seconds']=round(clock.elapsed(),6)
        if result['total_seconds']>180:failure=failure or TimeoutError('180s including teardown crossed');result['outcome']='stopped'
    try:result['final_census']=verify()
    except BaseException as error:failure=failure or error;result.update(outcome='stopped',source_final_error=str(error))
    (run/'result.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({'outcome':result['outcome'],'seconds':result.get('total_seconds'),'joined':result['all_owned_groups_joined_cleanly'],'result':str(run/'result.json'),'error':result.get('error')}),flush=True)
if failure:raise SystemExit(1)
