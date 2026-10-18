from pathlib import Path
import hashlib,json,os,shutil,socket,sys,time,traceback,urllib.request
ROOT=Path('/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com');UI=ROOT/"ui";QA=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442-appearance-835thdfq')
sys.path.insert(0,str(ROOT/'spike/t503'))
from requalification_browser import OwnedChild
from requalification_checks import Deadline
NODE=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t574-tools-gbrqb_vk/node-v24.18.0-darwin-arm64/bin/node')
CHROME=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
AXE=Path('/Users/ben/phebs.com/ui/node_modules/axe-core/axe.min.js')
DIST=Path('/Users/ben/.codex/worktrees/t25-embedded-docs/phebs.com/ui/dist')

def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def verify():
    assert json.loads((QA/'checks/receipt.json').read_text())['passed']
    plan=json.loads((QA/'plan.json').read_text())
    for name,digest in plan['source_sha256'].items(): assert sha(ROOT/name)==digest,name
    for name,digest in plan['historical_baseline_sha256'].items(): assert sha(UI/'receipts/baselines'/name)==digest,name
    census={}
    for name,base in [('production_inputs',ROOT),('production_dist',Path(plan['dist_root']))]:
        document=plan[name]
        for row in document['rows']:
            path=base/row['path']; assert path.is_file() and path.stat().st_size==row['bytes'] and sha(path)==row['sha256'],str(path)
        census[name]={'rows':len(document['rows']),'digest':document['digest']}
    assert sha(AXE)=='66a8aaa95a8b044a7fd74a5435873bf04ff65a1ca75567c921b7509742085a14'
    assert sha(NODE)=='ee6fb0e015284d83a91e8ec5213f43a157f8a392b58555301682892ba928c04a'
    return census

def port():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]

name=sys.argv[1];assert name in ('author','fresh');role=name
run=QA/name;assert not run.exists();run.mkdir(mode=0o700)
result={'schema':'phebs-t442-native-qa-v1','pass':name,'outcome':'preparing','source_checkpoint':'c3003613d87cbf2f123486d8fe587e614ba2d404','scroll_helper_sha256':sha(UI/'receipts/fixture.ts'),'native_renderer_claim':'Darwin installed Chrome only; no Noble baseline equivalence','commands':[],'terminals':{},'owners':{}}
owned=[];profile=run/'private-chrome-profile';failure=None
try:
    result['verified_census']=verify()
    result['tools']={'node':{'path':str(NODE),'sha256':sha(NODE)},'chrome_launcher':{'path':str(CHROME),'sha256':sha(CHROME)},'axe':{'path':str(AXE),'sha256':sha(AXE),'version':'4.12.1'},'playwright':json.loads((UI/'node_modules/playwright/package.json').read_text())['version']}
    clock=Deadline(180);result['started_monotonic']=clock.started
    def left():
        remaining=clock.remaining()-30
        assert remaining>0,'work deadline reached;30s owned-group cleanup reserve'
        return remaining
    webport=port();base='http://127.0.0.1:'+str(webport)
    argv=[str(NODE),str(UI/'node_modules/vite/bin/vite.js'),'preview',str(UI),'--outDir',str(DIST),'--host','127.0.0.1','--port',str(webport),'--strictPort']
    result['commands'].append({'name':'vite','argv':argv});web=OwnedChild(argv,run,'vite');owned.append(('vite',web));result['owners']['vite']={'pid':web.child.pid,'expected_pgid':web.child.pid,'observed_pgid':os.getpgid(web.child.pid)}
    ready=time.monotonic()+min(10,left())
    while True:
        assert web.child.poll() is None,'Vite exited before readiness'
        try:
            with urllib.request.urlopen(base,timeout=1) as response:
                assert response.status==200
                assert b'phebs' in response.read(2048)
            break
        except OSError:
            assert time.monotonic()<ready,'Vite readiness deadline'
            time.sleep(.05)
    argv=[str(CHROME),'--headless=new','--no-first-run','--no-default-browser-check','--disable-background-networking','--disable-component-update','--disable-sync','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+str(profile),'about:blank']
    result['commands'].append({'name':'chrome','argv':argv});chrome=OwnedChild(argv,run,'chrome');owned.append(('chrome',chrome));result['owners']['chrome']={'pid':chrome.child.pid,'expected_pgid':chrome.child.pid,'observed_pgid':os.getpgid(chrome.child.pid)}
    ready=time.monotonic()+min(5,left());active=profile/'DevToolsActivePort'
    while not active.exists():
        assert chrome.child.poll() is None,'Chrome exited before readiness'
        assert time.monotonic()<ready,'Chrome readiness deadline'
        left();time.sleep(.02)
    cdp='http://127.0.0.1:'+active.read_text().splitlines()[0]
    with urllib.request.urlopen(cdp+'/json/version',timeout=1) as response:result['renderer']=json.load(response)
    os.environ.update(T442_CDP=cdp,T442_AXE=str(AXE),T442_OUTPUT=str(run/'output'),T442_REPORT=str(run/'playwright.json'),PHEBS_RECEIPTS_URL=base)
    argv=[str(NODE),str(UI/'node_modules/@playwright/test/cli.js'),'test','--config',str(QA/'runner/playwright.config.ts'),'--project','appearance']
    argv+=['--max-failures=1']
    argv+=[] if role=='author' else ['--grep','native Appearance frame']
    result['commands'].append({'name':'client','argv':argv});client=OwnedChild(argv,run,'client');owned.append(('client',client));result['owners']['client']={'pid':client.child.pid,'expected_pgid':client.child.pid,'observed_pgid':os.getpgid(client.child.pid)}
    client.child.wait(timeout=left());assert client.child.returncode==0,'Playwright pass failed; first failure retained'
    document=json.loads((run/'playwright.json').read_text());result['playwright_stats']=document['stats']
    expected=10 if role=='author' else 8
    frame_count=8
    assert document['stats']['expected']==expected and document['stats']['unexpected']==0 and document['stats']['flaky']==0 and document['stats']['skipped']==0,'wrong native test census'
    frames=sorted((run/'output').rglob('palette-appearance--*.png'))
    assert len(frames)==frame_count and len({p.name for p in frames})==frame_count,'native frame census differs'
    result['frames']=[{'path':str(p),'name':p.name,'bytes':p.stat().st_size,'sha256':sha(p)} for p in frames]
    audits=[]
    for p in sorted((run/'output').rglob('native-qa.json')):
        value=json.loads(p.read_text())
        if value['title'].startswith('native Appearance frame'):
            assert value['audit']['version']=='4.12.1' and not value['audit']['violations'] and not value['errors'],'native audit/console failure'
            audits.append(value)
    assert len(audits)==frame_count,'native audit census differs'
    result['audits']=audits;result['outcome']='passed'
except BaseException as error:
    failure=error;result.update(outcome='stopped',error_type=type(error).__name__,error=str(error))
finally:
    for kind,child in reversed(owned):
        try:
            result['terminals'][kind]=dict(child.stop(),**result['owners'][kind],joined=True)
        except BaseException as error:
            failure=failure or error;result.update(outcome='stopped')
            terminal=dict(result['owners'][kind],cleanup_error=str(error),cleanup_traceback=traceback.format_exc(),returncode_at_error=child.child.returncode,joined=False)
            try:
                terminal['exit_code']=child.child.wait(timeout=3)
                terminal['joined']=True
                terminal['teardown']=__import__('qualify').serve.receipts._drain_group(child.child.pid,False)
            except BaseException:
                terminal['followup_wait_traceback']=traceback.format_exc()
            child.log.close()
            result['terminals'][kind]=terminal
    drained=all(v.get('teardown')=='clean' and v.get('termination_mode')!='sigkill' for v in result['terminals'].values())
    if drained and profile.exists():shutil.rmtree(profile)
    result['all_owned_groups_joined_cleanly']=drained and len(result['terminals'])==len(owned)
    if 'clock' in locals():
        result['total_seconds']=round(clock.elapsed(),6)
        if result['total_seconds']>180:failure=failure or TimeoutError('180s including teardown crossed');result['outcome']='stopped'
    try:result['final_census']=verify()
    except BaseException as error:failure=failure or error;result.update(outcome='stopped',source_final_error=str(error))
    (run/'result.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({'pass':name,'outcome':result['outcome'],'seconds':result.get('total_seconds'),'joined':result['all_owned_groups_joined_cleanly'],'result':str(run/'result.json'),'error':result.get('error')}),flush=True)
if failure:raise SystemExit(1)
