from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys,time,traceback
QA=Path(__file__).parent
plan=json.loads((QA/'plan.json').read_text());ROOT=Path(plan['root'])
sys.path.insert(0,str(ROOT/'spike/t503'))
from requalification_browser import OwnedChild
GO=Path('/opt/homebrew/opt/phebs-go-1.26.5/bin/go')
os.chdir(ROOT)
os.environ.update(GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off',GOMAXPROCS='2',GOFLAGS='-mod=readonly',TMPDIR=str(QA/'native-tmp'))
os.environ['PATH']=str(GO.parent)+':'+os.environ['PATH']
assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()==plan['source_commit']
assert not subprocess.check_output(['git','diff','--name-only'],text=True)
assert not subprocess.check_output(['git','diff','--cached','--name-only'],text=True)
assert subprocess.check_output([str(GO),'version'],text=True).strip()=='go version go1.26.5 darwin/arm64'
assert subprocess.check_output(['surreal','version'],text=True).strip()=='3.2.0 for macos on aarch64'
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
state={'schema':'phebs-t57r1-native-run-v1','source_commit':plan['source_commit'],'outcome':'running','clock_scope':'Compilation separate; per-case original context retained, outer Go-test and process cleanup bounds explicit','runs':[],'tools':{'go':{'path':str(GO.resolve()),'sha256':sha(GO)},'surreal':{'path':str(Path(shutil.which('surreal')).resolve()),'sha256':sha(shutil.which('surreal'))}}}
def write():(QA/'result.json').write_text(json.dumps(state,indent=2)+'\n')
def run(name,args,seconds):
 row={'name':name,'argv':args,'work_limit_seconds':seconds,'cleanup_reserve_seconds':15};start=time.monotonic();child=None;err=None
 try:
  child=OwnedChild(args,QA/'logs',name);row['pid']=child.child.pid;row['pgid']=os.getpgid(child.child.pid)
  child.child.wait(timeout=seconds);assert child.child.returncode==0,name+' returned nonzero'
 except BaseException as e:err=e;row.update(error=str(e),traceback=traceback.format_exc())
 finally:
  if child:
   try:row.update(child.stop(),joined=True)
   except BaseException as e:err=err or e;row.update(cleanup_error=str(e),joined=False)
  row['seconds_including_cleanup']=round(time.monotonic()-start,6)
  row['log_sha256']=sha(QA/'logs'/(name+'.log'));state['runs'].append(row);write();print(json.dumps(row),flush=True)
 if err:raise err
 return (QA/'logs'/(name+'.log')).read_text()
def probe(name):
 raw=run(name,[str(QA/'capacity-probe'),str(ROOT),str(QA/'native-tmp')],10)
 rows=[json.loads(line) for line in raw.splitlines()];assert len(rows)==2 and all(r['ready'] and r['goos']=='darwin' and r['goarch']=='arm64' and r['capacity']['Pressure']=='normal' and not r['error'] for r in rows)
 state.setdefault('capacity_checks',{})[name]=rows;write()
try:
 probe_source=Path(plan['probe_source']);shutil.copyfile(probe_source,QA/'capacity-probe.go')
 run('build-capacity-probe',[str(GO),'build','-o',str(QA/'capacity-probe'),str(probe_source)],60)
 shutil.rmtree(probe_source.parent)
 probe('capacity-before-compile')
 run('compile-native-tests',[str(GO),'test','-c','-o',str(QA/'cmd-phebs.test'),'./cmd/phebs'],180)
 state['test_binary_sha256']=sha(QA/'cmd-phebs.test');write()
 for i,name in enumerate(plan['test_names']):
  probe('capacity-before-'+str(i+1))
  timeout='200s' if i==0 else '630s';outer=215 if i==0 else 645
  raw=run(name,[str(QA/'cmd-phebs.test'),'-test.run=^'+name+'$','-test.count=1','-test.timeout='+timeout,'-test.v'],outer)
  assert '--- PASS: '+name+' ' in raw and '--- SKIP:' not in raw and '--- FAIL:' not in raw,'required native case did not pass without skips'
  probe('capacity-after-'+str(i+1))
 state['outcome']='passed'
except BaseException as e:
 state.update(outcome='stopped',error=str(e),traceback=traceback.format_exc());write();print(json.dumps({'outcome':'stopped','error':str(e)}),flush=True);raise SystemExit(1)
finally:
 state['tracked_source_unchanged']=not subprocess.check_output(['git','diff','--name-only'],text=True) and not subprocess.check_output(['git','diff','--cached','--name-only'],text=True)
 write()
print(json.dumps({'outcome':state['outcome'],'result':str(QA/'result.json')}),flush=True)
