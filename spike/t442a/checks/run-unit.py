from pathlib import Path
import json,hashlib,os,subprocess,sys,time,traceback
Q=Path(__file__).parent;P=json.loads((Q/'plan.json').read_text());ROOT=Path(P['root']);UI=ROOT/'ui';NODE=P['node']
sys.path.insert(0,str(ROOT/'spike/t503'));from requalification_browser import OwnedChild
os.chdir(UI);os.environ['PATH']=str(Path(NODE).parent)+':'+os.environ['PATH'];runs=[]
def run(name,args,limit,expected=0):
 start=time.monotonic();c=OwnedChild(args,Q/'checks',name);r={'name':name,'argv':args,'limit_seconds':limit,'expected_exit':expected,'pid':c.child.pid};failure=None
 try:c.child.wait(timeout=limit);assert c.child.returncode==expected,'unexpected command result'
 except BaseException as e:failure=e;r['error']=str(e)
 finally:
  try:r.update(c.stop(),joined=True)
  except BaseException as e:failure=failure or e;r.update(cleanup_error=str(e),joined=False)
  r['seconds_including_join']=time.monotonic()-start;r['log_sha256']=hashlib.sha256((Q/'checks'/(name+'.log')).read_bytes()).hexdigest();runs.append(r);(Q/'checks/result.json').write_text(json.dumps({'runs':runs,'failed':bool(failure)},indent=2)+'\n');print(json.dumps(r),flush=True)
 if failure:raise failure
 return (Q/'checks'/(name+'.log')).read_text()
source=UI/'src/pages/SettingsPage.tsx';changed=source.read_bytes();(Q/'SettingsPage.edited.tsx').write_bytes(changed)
try:
 source.write_bytes(subprocess.check_output(['git','show',P['base_commit']+':ui/src/pages/SettingsPage.tsx'],cwd=ROOT))
 raw=run('baseline-regression',[NODE,'node_modules/vitest/vitest.mjs','run','--maxWorkers=1','--no-file-parallelism','src/pages/SettingsPage.test.tsx','--testNamePattern','explains language failure','--bail=1','--testTimeout=10000'],30,1)
 assert 'Unable to find role="status"' in raw or 'Unable to find role' in raw,'baseline did not reproduce missing status'
finally:source.write_bytes(changed)
run('focused',[NODE,'node_modules/vitest/vitest.mjs','run','--maxWorkers=1','--no-file-parallelism','src/palette.test.ts','src/theme.test.ts','src/pages/SettingsPage.test.tsx','src/pages/FilePage.test.tsx','src/pages/SearchPage.test.tsx','src/pages/ExactCallerCitation.test.tsx','src/components/kit.test.tsx','--testNamePattern','T44.2|palette|syntax palettes','--testTimeout=10000','--hookTimeout=10000'],60)
