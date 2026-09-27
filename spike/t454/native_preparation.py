#!/usr/bin/env python3
"""Closed neutral preparation transport. Nothing runs implicitly.

check/build are offline. stage/run require a separately reviewed configuration
hash and the already running dedicated VM. This script never starts a VM, pulls
an image, downloads dependencies, changes a profile, or supplies Docker flags.

It drives exactly ONE finite preparation invocation (no case loop, no retry, no
downloader, no installer, no general job framework). The offline bounded archive
helpers are reused from native_acceptance by import; the old spike stage/run/
collect entrypoints are never invoked. A lost SSH reply is re-queried against
only this exact recorded owner/result and never starts another run.
"""
import argparse
import inspect
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile

# Reuse the reviewed offline helpers without invoking the acceptance transport.
from native_acceptance import (
    read_ustar, digest, canonical, decode, hash_valid, regular, read,
    file_hash, relative, exclusive, PROFILE, MAX_FILE, MAX_RECEIPT,
)

BASE = Path('/var/lib/phebs-typed-preparation')
REPO = 'example.invalid/phebs-native-neutral'
REMOTE = 'https://example.invalid/phebs-native-neutral'
ROOT = '//lib:lib'
SCHEMA = 'phebs-typed-native-preparation-v1'
# Closed key order; must equal the Go nativePreparationConfig field order so the
# canonical round-trip check is meaningful across both codecs. It carries only
# identities knowable BEFORE the run: the run-produced Selection-v1 digest and the
# real-store profile epoch are recorded in the post-run final identity chain, never
# staged here, so a truthful config can exist at stage time without fabricating a
# run output. (The leaf-4.79 acceptance codec keeps both: its cases run only after
# preparation has produced the final selection/epoch.)
CONFIG_KEYS = ('schema', 'id', 'source_commit', 'source', 'module', 'remote',
               'root', 'test_sha256', 'helper_sha256', 'engine_sha256',
               'inventory_sha256', 'profile_sha256', 'image_sha256',
               'mkfs_sha256', 'deployment_sha256')
# Native preparation stages no engine and no seed: the pristine real-store seed
# is a separate, non-native host step. Only these fixed names plus bundle/ join.
STAGED_NAMES = frozenset({'config.json', 'inventory.json', 'profile.json',
                          'deployment.json', 'native-preparation.test'})
MAX_BUNDLE = 2 << 30
MAX_ARCHIVE = MAX_BUNDLE + MAX_FILE + (32 << 20)


def fail(message):
    raise ValueError(message)


def preparation_id(value):
    return isinstance(value, str) and re.fullmatch(r'[a-z][a-z0-9-]{0,31}', value)


def config(raw, expected=None):
    value = decode(raw, 16384)
    if tuple(value) != CONFIG_KEYS or value['schema'] != SCHEMA:
        fail('closed preparation configuration')
    if not preparation_id(value['id']):
        fail('preparation identity')
    source = value['source']
    if tuple(source) != ('repository', 'incarnation', 'generation', 'commit') or source['repository'] != REPO:
        fail('neutral source')
    if not re.fullmatch(r'[0-9a-f]{40}', source['commit']) or not source['incarnation'] or not hash_valid(source['generation']):
        fail('source identity')
    if value['module'] != REPO or value['remote'] != REMOTE or value['root'] != ROOT:
        fail('neutral module/remote/root')
    if not re.fullmatch(r'[0-9a-f]{40}', value['source_commit']):
        fail('implementation identity')
    for key in CONFIG_KEYS:
        if key.endswith('_sha256') and not hash_valid(value[key]):
            fail('missing preparation identity')
    if expected is not None and (not hash_valid(expected) or digest(raw) != expected):
        fail('reviewed preparation config changed')
    return value


def inputs(directory, expected=None):
    directory = Path(directory).absolute()
    raw = read(directory / 'config.json', 16384)
    cfg = config(raw, expected)
    inv_raw = read(directory / 'inventory.json', 16 << 20)
    inv = decode(inv_raw, 16 << 20)
    if tuple(inv) != ('schema', 'files') or inv['schema'] != 'phebs-typed-prehydration-v1' or digest(inv_raw) != cfg['inventory_sha256']:
        fail('inventory identity')
    if not 1 <= len(inv['files']) <= 50000:
        fail('inventory count')
    rows = [('config.json', digest(raw), len(raw), False),
            ('inventory.json', cfg['inventory_sha256'], len(inv_raw), False)]
    for name, key, maximum in (('deployment.json', 'deployment_sha256', 16384),
                               ('profile.json', 'profile_sha256', 16384),
                               ('native-preparation.test', 'test_sha256', MAX_FILE)):
        h, size = file_hash(directory / name, maximum)
        if h != cfg[key]:
            fail('fixed preparation input identity')
        rows.append((name, h, size, name == 'native-preparation.test'))
    total, previous, directories = 0, '', set()
    for row in inv['files']:
        if tuple(row) != ('path', 'bytes', 'digest', 'executable') or not relative(row['path']) or row['path'] <= previous or type(row['bytes']) is not int or not 0 <= row['bytes'] <= MAX_FILE or type(row['executable']) is not bool:
            fail('inventory member')
        previous = row['path']
        total += row['bytes']
        directories.update(str(p) for p in PurePosixPath(row['path']).parents if str(p) != '.')
        if total > MAX_BUNDLE or len(directories) > 20000:
            fail('inventory aggregate')
        h, size = file_hash(directory / 'bundle' / row['path'], row['bytes'])
        if h != row['digest'] or size != row['bytes']:
            fail('bundle identity')
        rows.append(('bundle/' + row['path'], h, size, row['executable']))
    for name, _, _, _ in rows:
        entry = tarfile.TarInfo(name)
        try:
            entry.tobuf(format=tarfile.USTAR_FORMAT)
        except ValueError:
            fail('path exceeds fixed USTAR contract')
    expected_paths = {row[0] for row in rows}
    seen, count = set(), 0
    for parent, dirs, files in os.walk(directory, followlinks=False):
        count += len(dirs) + len(files)
        if count > 71000:
            fail('input census overflow')
        for name in dirs + files:
            if (Path(parent) / name).is_symlink():
                fail('input alias')
        for name in files:
            seen.add(str((Path(parent) / name).relative_to(directory)))
    if seen != expected_paths:
        fail('extra/missing staged preparation input')
    return cfg, rows


def check_deployment_host(path, expected=None):
    raw = read(path, 16384)
    value = decode(raw, 16384)
    if expected is not None and digest(raw) != expected:
        fail('deployment identity')
    if value.get('schema') != 'phebs-typed-native-deployment-v1' or value.get('profile') != PROFILE:
        fail('deployment profile')
    vm = Path.home() / '.colima' / PROFILE / 'colima.yaml'
    h, _ = file_hash(vm, 1 << 20)
    if value.get('vm_config_sha256') != h:
        fail('saved VM configuration changed')
    return digest(raw)


def transport(arguments, **kwargs):
    return subprocess.run(['colima', 'ssh', '--profile', PROFILE, '--', 'sudo', 'python3', '-c', REMOTE_PROGRAM, *arguments], check=True, **kwargs)


def stage(directory, expected):
    cfg, rows = inputs(directory, expected)
    check_deployment_host(Path(directory) / 'deployment.json', cfg['deployment_sha256'])
    archive = Path(directory).parent / (cfg['id'] + '.preparation.tar')
    with archive.open('xb') as out:
        with tarfile.open(fileobj=out, mode='w|', format=tarfile.USTAR_FORMAT) as tar:
            for name, h, size, executable in rows:
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = size, (0o500 if executable else 0o400)
                with regular(Path(directory) / name, size) as source:
                    data_hash, data_size = file_hash(Path(directory) / name, size)
                    if data_hash != h or data_size != size:
                        fail('preparation input changed before staging')
                    tar.addfile(entry, source)
        out.flush()
        os.fsync(out.fileno())
    if archive.stat().st_size > MAX_ARCHIVE:
        fail('preparation archive bound')
    with archive.open('rb') as stream:
        transport(['stage', cfg['id'], expected], stdin=stream, timeout=600)


def run(prepare_id, expected, deployment):
    if not preparation_id(prepare_id) or not hash_valid(expected):
        fail('preparation dispatch selector')
    deployment_hash = check_deployment_host(deployment)
    # One exclusive remote dispatch marker; no retry, no second invocation.
    transport(['run', prepare_id, expected, deployment_hash], timeout=600)


def collect(prepare_id, expected, output):
    if not preparation_id(prepare_id) or not hash_valid(expected):
        fail('preparation collection selector')
    # A lost reply re-queries only this exact recorded owner/result.
    result = transport(['collect', prepare_id, expected], stdout=subprocess.PIPE, timeout=30)
    value = decode(result.stdout, MAX_RECEIPT)
    if value.get('config') != expected or value.get('schema') != 'phebs-typed-native-preparation-receipt-v1':
        fail('returned preparation receipt binding')
    exclusive(output, result.stdout)


def build(output):
    output = Path(output).absolute()
    output.mkdir(mode=0o700)  # deliberately refuses an existing output directory
    repo = Path(__file__).resolve().parents[2]
    env = dict(os.environ, GOENV='off', GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOWORK='off', GOOS='linux', GOARCH='arm64', CGO_ENABLED='0')
    subprocess.run(['go', 'test', '-c', '-o', str(output / 'native-preparation.test'), './internal/typedbazel/provider'], cwd=repo, env=env, check=True, timeout=300)
    print('native-preparation.test', *file_hash(output / 'native-preparation.test', MAX_FILE))
    print('Unfilled: finalized neutral lock/cache, pre-selection inventory, profile, VM engine/formatter/image and source identities. No config was invented.')


# Fixed remote program: no arbitrary command or extraction option. It refuses an
# existing stage root, never follows archive links, enforces one dispatch, and
# never owns VM lifecycle or native cleanup (the Go owners do that).
REMOTE_BODY = r'''
import hashlib,json,os,pathlib,re,signal,stat,subprocess,sys,tarfile
signal.alarm(600)
B=pathlib.Path('/var/lib/phebs-typed-preparation')
NAMES={'config.json','inventory.json','profile.json','deployment.json','native-preparation.test'}
def reject(): raise RuntimeError('neutral preparation transport refused')
def digest(b): return 'sha256:'+hashlib.sha256(b).hexdigest()
def checkdir(p):
 s=p.lstat()
 if not stat.S_ISDIR(s.st_mode) or s.st_uid!=0 or s.st_mode&0o077: reject()
def read(p,n):
 for a in [p,*p.parents]:
  if a.is_symlink(): reject()
 fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
 with os.fdopen(fd,'rb') as f:
  s=os.fstat(f.fileno())
  if not stat.S_ISREG(s.st_mode) or s.st_uid!=0 or s.st_nlink!=1 or s.st_mode&0o022 or s.st_size>n: reject()
  b=f.read(n+1)
  if len(b)>n: reject()
  return b
def write(p,b,mode=0o600):
 fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode)
 with os.fdopen(fd,'wb') as f: f.write(b);f.flush();os.fsync(f.fileno())
 fd=os.open(p.parent,os.O_RDONLY|os.O_DIRECTORY)
 try: os.fsync(fd)
 finally: os.close(fd)
if os.geteuid()!=0 or len(sys.argv)<3: reject()
a,ident=sys.argv[1:3]
if not re.fullmatch('[a-z][a-z0-9-]{0,31}',ident): reject()
checkdir(B);root=B/ident
if a=='stage':
 expected=sys.argv[3]
 if len(sys.argv)!=4 or not re.fullmatch('sha256:[0-9a-f]{64}',expected): reject()
 total=0;seen=set();dirs=set()
 for row,contents in read_ustar(sys.stdin.buffer):
  name=row.name;p=pathlib.PurePosixPath(name)
  if not row.isfile() or p.is_absolute() or str(p)!=name or '..' in p.parts or '\\' in name or any(ord(c)<32 or ord(c)==127 for c in name) or name in seen or row.size<0 or row.size>256<<20 or row.mode not in (0o400,0o500): reject()
  if name not in NAMES and not name.startswith('bundle/'): reject()
  if not seen:
   if name!='config.json' or row.size>16384: reject()
   data=b''.join(contents)
   if digest(data)!=expected: reject()
   root.mkdir(mode=0o700)
   contents=iter([data])
  seen.add(name);total+=row.size
  if len(seen)>50006 or total>(2<<30)+(256<<20)+(32<<20): reject()
  dest=root/name
  for d in reversed(dest.parent.relative_to(root).parents):
   if str(d)!='.': (root/d).mkdir(mode=0o700,exist_ok=True)
  dest.parent.mkdir(mode=0o700,exist_ok=True)
  dirs.update(str(x) for x in p.parents)
  if len(dirs)>20010: reject()
  v=os.statvfs(root)
  need=((row.size+v.f_frsize-1)//v.f_frsize+4)*v.f_frsize
  if v.f_bavail*v.f_frsize-need <= v.f_blocks*v.f_frsize//5 or v.f_favail<4: reject()
  fd=os.open(dest,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,row.mode)
  with os.fdopen(fd,'wb') as out:
   for chunk in contents: out.write(chunk)
   out.flush();os.fsync(out.fileno())
 raw=read(root/'config.json',16384)
 if digest(raw)!=expected: reject()
 cfg=json.loads(raw)
 invraw=read(root/'inventory.json',16<<20)
 if digest(invraw)!=cfg['inventory_sha256']: reject()
 inv=json.loads(invraw)
 expected_names=set(NAMES)
 for f in inv['files']:
  name='bundle/'+f['path'];expected_names.add(name)
  if digest(read(root/name,f['bytes']))!=f['digest']: reject()
 for name,key,n in [('deployment.json','deployment_sha256',16384),('profile.json','profile_sha256',16384),('native-preparation.test','test_sha256',256<<20)]:
  if digest(read(root/name,n))!=cfg[key]: reject()
 if seen!=expected_names or cfg['id']!=ident: reject()
 write(root/'staged.json',json.dumps({'config':expected},separators=(',',':')).encode())
elif a in ('run','collect'):
 if len(sys.argv)!=(5 if a=='run' else 4): reject()
 expected=sys.argv[3]
 if not re.fullmatch('sha256:[0-9a-f]{64}',expected): reject()
 checkdir(root)
 raw=read(root/'config.json',16384)
 if digest(raw)!=expected: reject()
 if json.loads(read(root/'staged.json',1024))!={'config':expected}: reject()
 if a=='collect':
  data=read(root/'receipt.json',128<<10)
  receipt=json.loads(data)
  if receipt.get('config')!=expected: reject()
  sys.stdout.buffer.write(data)
 else:
  if digest(read(root/'deployment.json',16384))!=sys.argv[4]: reject()
  cfg=json.loads(raw)
  if digest(read(root/'native-preparation.test',256<<20))!=cfg['test_sha256']: reject()
  write(root/'dispatch.json',json.dumps({'config':expected},separators=(',',':')).encode())
  result=subprocess.run([str(root/'native-preparation.test'),'-test.run=^TestNativePreparationHost$','-test.count=1','-test.timeout=600s','-typed-preparation-config='+str(root/'config.json'),'-typed-preparation-role=host'],env={'PATH':'/usr/bin:/bin','HOME':str(root)},stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True,timeout=600)
  if result.returncode: reject()
else: reject()
'''

REMOTE_PROGRAM = inspect.getsource(read_ustar) + '\n' + REMOTE_BODY


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('build'); p.add_argument('--output', required=True)
    for action in ('check', 'stage'):
        p = sub.add_parser(action); p.add_argument('--input', required=True); p.add_argument('--config-sha', required=True)
    p = sub.add_parser('run'); p.add_argument('--id', required=True); p.add_argument('--config-sha', required=True); p.add_argument('--deployment', required=True)
    p = sub.add_parser('collect'); p.add_argument('--id', required=True); p.add_argument('--config-sha', required=True); p.add_argument('--output', required=True)
    args = parser.parse_args()
    if args.command == 'build':
        build(args.output)
    elif args.command == 'check':
        cfg, rows = inputs(args.input, args.config_sha)
        print(cfg['id'], len(rows), 'verified preparation inputs; no execution')
    elif args.command == 'stage':
        stage(args.input, args.config_sha)
    elif args.command == 'run':
        run(args.id, args.config_sha, args.deployment)
    else:
        collect(args.id, args.config_sha, args.output)


if __name__ == '__main__':
    main()
