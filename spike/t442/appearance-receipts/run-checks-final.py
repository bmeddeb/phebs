from pathlib import Path
import hashlib,json,os,sys,time,traceback
ROOT=Path('/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com');UI=ROOT/'ui';QA=Path('/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442-appearance-835thdfq')
sys.path.insert(0,str(ROOT/'spike/t503'))
from requalification_browser import OwnedChild
NODE='/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t574-tools-gbrqb_vk/node-v24.18.0-darwin-arm64/bin/node'
runs=[]
commands=[
 ('receipt-types',[NODE,'node_modules/typescript/bin/tsc','--ignoreConfig','--noEmit','--strict','--skipLibCheck','--target','es2023','--lib','es2023,dom,dom.iterable','--module','esnext','--moduleResolution','bundler','--types','node','--jsx','react-jsx','--allowImportingTsExtensions','receipts/palette-appearance.spec.ts','playwright.config.ts']),
 ('receipt-lint',[NODE,'node_modules/oxlint/bin/oxlint','receipts/palette-appearance.spec.ts','playwright.config.ts'])]
commands.append(('receipt-discovery',[NODE,'node_modules/@playwright/test/cli.js','test','--project','appearance','--list']))
os.chdir(UI)
os.environ['PATH']=str(Path(NODE).parent)+':'+os.environ['PATH']
failed=None
for name,argv in commands:
 row={'name':name,'argv':argv,'cwd':str(UI),'outer_limit_seconds':90};child=None;started=time.monotonic()
 try:
  child=OwnedChild(argv,QA/'checks-final',name)
  row['pid']=child.child.pid
  child.child.wait(timeout=75)
  row['exit_code']=child.child.returncode
  if child.child.returncode!=0:raise RuntimeError(name+' failed')
 except BaseException as error:
  failed=error;row.update(error=str(error),traceback=traceback.format_exc())
 finally:
  if child:
   try:row.update(child.stop(),joined=True)
   except BaseException as error:failed=failed or error;row.update(cleanup_error=str(error),joined=False)
  row['wall_seconds']=time.monotonic()-started
  if row['wall_seconds']>90:failed=failed or TimeoutError(name+' outer bound exceeded')
  row['log_sha256']=hashlib.sha256((QA/'checks-final'/(name+'.log')).read_bytes()).hexdigest()
  runs.append(row)
  (QA/'checks-final/receipt.json').write_text(json.dumps({'runs':runs,'passed':failed is None and len(runs)==len(commands)},indent=2)+'\n')
  print(json.dumps({k:row[k] for k in ['name','exit_code','wall_seconds','joined'] if k in row}),flush=True)
 if failed:break
if failed:raise SystemExit(1)
