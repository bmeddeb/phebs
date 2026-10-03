#!/usr/bin/env python3
"""Closed neutral acceptance transport. Nothing runs implicitly.

check/build are offline. stage/run require a separately reviewed configuration
hash and the already running dedicated VM. This script never starts a VM, pulls
an image, downloads dependencies, changes a profile, or supplies Docker flags.
"""
import argparse
import hashlib
import io
import inspect
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys
import tarfile

BASE = Path('/var/lib/phebs-typed-acceptance')
PROFILE = 'phebs-t451a'
CASES = ('success', 'cancel', 'wall', 'hard-death', 'canary', 'dry-run')
CONFIG_KEYS = ('schema', 'id', 'source_commit', 'source', 'test_sha256',
               'helper_sha256', 'engine_sha256', 'seed_sha256',
               'inventory_sha256', 'profile_sha256', 'selection_sha256',
               'image_sha256', 'mkfs_sha256', 'universe_sha256', 'profile_epoch', 'deployment_sha256', 'policy')
POLICY = dict(memory_bytes=4533092352, scratch_bytes=4573403136,
              scratch_inodes=262144, tasks=294, descriptors_per_process=128,
              cpu_quota_micros=200000, cpu_period_micros=100000,
              wall_seconds=300, output_bytes=16777216, scip_bytes=2285819)
REPO = 'example.invalid/phebs-native-neutral'
CORPUS_SCHEMA = 'phebs-typed-native-corpus-acceptance-v1'
CORPUS_REPO = 'github.com/bazelbuild/remote-apis-sdks'
CORPUS_COMMIT = 'd5824b1a2286806b07efd030aa3a139c4f540157'
CORPUS_GIT_MAX = 4 << 20
COHORTS = ('ordinary', 'proto', 'fanout')
ORACLE_SHA256 = 'sha256:0620be2b4c5631e01626f6a27b18621908f045edbf73a00ceffd0b638bb616d1'
ARCHIVE_SHA256 = 'sha256:c9ecf680cd7bd0d88d8a6d1a0084a09c0a9dc45145fc28fbdcda888586d54bcc'
MAX_BUNDLE = 2 << 30
MAX_FILE = 256 << 20
MAX_ARCHIVE = MAX_BUNDLE + 2 * MAX_FILE + (32 << 20)
MAX_RECEIPT = 128 << 10


def read_ustar(stream):
    """Parse fixed regular-file headers before any extension can allocate."""
    consumed = 0
    def take(count):
        nonlocal consumed
        if count < 0 or consumed + count > (2 << 30) + 2 * (256 << 20) + (32 << 20):
            raise ValueError('archive byte bound')
        raw = stream.read(count)
        consumed += len(raw)
        if len(raw) != count:
            raise ValueError('truncated archive')
        return raw
    while True:
        header = take(512)
        if header == bytes(512):
            if take(512) != bytes(512):
                raise ValueError('archive terminator')
            tail = stream.read(10241)
            if len(tail) > 10240 or any(tail):
                raise ValueError('archive trailing metadata')
            return
        if header[156:157] not in (b'0', b'\0') or header[257:263] != b'ustar\0' or header[263:265] != b'00':
            raise ValueError('only ordinary USTAR regular files accepted')
        row = tarfile.TarInfo.frombuf(header, 'utf-8', 'strict')
        if row.size < 0 or row.size > 256 << 20:
            raise ValueError('member byte bound')
        remaining = row.size
        def contents():
            nonlocal remaining
            while remaining:
                block = take(min(1 << 20, remaining))
                remaining -= len(block)
                yield block
        yield row, contents()
        if remaining:
            raise ValueError('member was not consumed')
        padding = take((-row.size) % 512)
        if any(padding):
            raise ValueError('nonzero archive padding')


def fail(message):
    raise ValueError(message)


def digest(data):
    return 'sha256:' + hashlib.sha256(data).hexdigest()


def canonical(value):
    return json.dumps(value, separators=(',', ':'), ensure_ascii=False).encode()


def decode(raw, maximum):
    if not raw or len(raw) > maximum:
        fail('control bound')
    def pairs(items):
        out = {}
        for key, value in items:
            if key in out:
                fail('duplicate key')
            out[key] = value
        return out
    value = json.loads(raw, object_pairs_hook=pairs)
    if canonical(value) != raw:
        fail('noncanonical control')
    return value


def hash_valid(value):
    return isinstance(value, str) and re.fullmatch(r'sha256:[0-9a-f]{64}', value)


def config(raw, expected=None):
    value = decode(raw, 16384)
    corpus = value.get('schema') == CORPUS_SCHEMA
    keys = CONFIG_KEYS + ('cohort', 'source_git_sha256') if corpus else CONFIG_KEYS
    if tuple(value) != keys or value['schema'] not in ('phebs-typed-native-acceptance-v1', CORPUS_SCHEMA):
        fail('closed configuration')
    if not re.fullmatch(r'[a-z][a-z0-9-]{0,31}', value['id']):
        fail('run identity')
    source = value['source']
    if tuple(source) != ('repository', 'incarnation', 'generation', 'commit') or source['repository'] != (CORPUS_REPO if corpus else REPO):
        fail('closed source')
    if corpus and (source['commit'] != CORPUS_COMMIT or value['cohort'] not in COHORTS or not hash_valid(value['source_git_sha256'])):
        fail('frozen corpus source')
    if not re.fullmatch(r'[0-9a-f]{40}', source['commit']) or not source['incarnation'] or not hash_valid(source['generation']):
        fail('source identity')
    if not re.fullmatch(r'[0-9a-f]{40}', value['source_commit']):
        fail('implementation identity')
    for key in keys:
        if key.endswith('_sha256') and not hash_valid(value[key]):
            fail('missing identity')
    if type(value['profile_epoch']) is not int or value['profile_epoch'] < 1 or value['policy'] != POLICY:
        fail('policy/epoch override')
    if expected is not None and (not hash_valid(expected) or digest(raw) != expected):
        fail('reviewed config changed')
    return value


def regular(path, limit):
    path = Path(path)
    # Refuse aliases in every existing path component, including input parents.
    for part in (path, *path.parents):
        if part.is_symlink():
            fail('symlink input')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_size > limit:
            fail('input type/length')
        return os.fdopen(fd, 'rb')
    except BaseException:
        os.close(fd)
        raise


def read(path, limit):
    with regular(path, limit) as stream:
        raw = stream.read(limit + 1)
    if len(raw) > limit:
        fail('input overflow')
    return raw


def file_hash(path, limit):
    h = hashlib.sha256()
    count = 0
    with regular(path, limit) as stream:
        while block := stream.read(1 << 20):
            count += len(block)
            if count > limit:
                fail('input grew')
            h.update(block)
    return 'sha256:' + h.hexdigest(), count


def relative(name):
    return isinstance(name, str) and name and not name.startswith('/') and '\\' not in name and all(ord(c) >= 32 and ord(c) != 127 for c in name) and str(PurePosixPath(name)) == name and '..' not in PurePosixPath(name).parts


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
    # The final trusted provisioned selection differs from preparation's input
    # control. Bind the actual bundle file before any transport or native work.
    selection_hash, selection_size = file_hash(directory / 'bundle/typed-bazel-selection.json', 16 << 20)
    if selection_hash != cfg['selection_sha256']:
        fail('selection identity')
    selection_rows = [r for r in inv['files'] if r.get('path') == 'typed-bazel-selection.json']
    if len(selection_rows) != 1 or selection_rows[0].get('digest') != selection_hash or selection_rows[0].get('bytes') != selection_size or selection_rows[0].get('executable') is not False:
        fail('selection inventory identity')
    rows = [('config.json', digest(raw), len(raw), False),
            ('inventory.json', cfg['inventory_sha256'], len(inv_raw), False)]
    if cfg['schema'] == CORPUS_SCHEMA:
        h, size = file_hash(directory / 'source-git.tar', CORPUS_GIT_MAX)
        if h != cfg['source_git_sha256']:
            fail('source Git identity')
        rows.append(('source-git.tar', h, size, False))
    for name, key, maximum in (('deployment.json', 'deployment_sha256', 16384), ('profile.json', 'profile_sha256', 16384),
                               ('seed.surql', 'seed_sha256', 4 << 20),
                               ('native-acceptance.test', 'test_sha256', MAX_FILE),
                               ('surreal', 'engine_sha256', MAX_FILE)):
        h, size = file_hash(directory / name, maximum)
        if h != cfg[key]:
            fail('fixed input identity')
        rows.append((name, h, size, name in ('surreal', 'native-acceptance.test')))
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
        try: entry.tobuf(format=tarfile.USTAR_FORMAT)
        except ValueError: fail('path exceeds fixed USTAR contract')
    expected_paths = {row[0] for row in rows}
    # Bounded census prevents unnoticed files from joining the staged authority.
    seen = set()
    count = 0
    for parent, dirs, files in os.walk(directory, followlinks=False):
        count += len(dirs) + len(files)
        if count > 71000:
            fail('input census overflow')
        for name in dirs + files:
            path = Path(parent) / name
            if path.is_symlink():
                fail('input alias')
        for name in files:
            seen.add(str((Path(parent) / name).relative_to(directory)))
    if seen != expected_paths:
        fail('extra/missing staged input')
    return cfg, rows


def exclusive(path, raw):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
    finally:
        directory = os.open(Path(path).parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)


def transport(arguments, **kwargs):
    return subprocess.run(['colima', 'ssh', '--profile', PROFILE, '--', 'sudo', 'python3', '-c', REMOTE, *arguments], check=True, **kwargs)


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


def stage(directory, expected):
    cfg, rows = inputs(directory, expected)
    check_deployment_host(Path(directory) / 'deployment.json', cfg['deployment_sha256'])
    # One bounded private local archive, exclusively created and never reused.
    archive = Path(directory).parent / (cfg['id'] + '.stage.tar')
    with archive.open('xb') as out:
        with tarfile.open(fileobj=out, mode='w|', format=tarfile.USTAR_FORMAT) as tar:
            for name, h, size, executable in rows:
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = size, (0o500 if executable else 0o400)
                with regular(Path(directory) / name, size) as source:
                    # Recheck after offline verification, before transmitting.
                    data_hash, data_size = file_hash(Path(directory) / name, size)
                    if data_hash != h or data_size != size:
                        fail('input changed before staging')
                    tar.addfile(entry, source)
        out.flush()
        os.fsync(out.fileno())
    if archive.stat().st_size > MAX_ARCHIVE:
        fail('archive bound')
    with archive.open('rb') as stream:
        transport(['stage', cfg['id'], expected], stdin=stream, timeout=600)


def run(run_id, case, expected, deployment):
    if not acceptance_id(run_id) or case not in CASES or not hash_valid(expected):
        fail('dispatch selector')
    # Config-bound remote deployment receipt is compared with this same digest.
    deployment_hash = check_deployment_host(deployment)
    # The remote exclusive marker survives transport death. No retry is offered.
    transport(['run', run_id, case, expected, deployment_hash], timeout=600)


def collect(run_id, case, expected, output):
    if not acceptance_id(run_id) or case not in CASES or not hash_valid(expected):
        fail('collection selector')
    result = transport(['collect', run_id, case, expected], stdout=subprocess.PIPE, timeout=30)
    value = decode(result.stdout, MAX_RECEIPT)
    if value.get('config') != expected or value.get('case') != case:
        fail('returned receipt binding')
    exclusive(output, result.stdout)


def corpus_cost(receipt, proof):
    """Require two authenticated, qualified cold-phase measurement records."""
    missing = ['sampled_child_lifetimes', 'sampled_fd_counts', 'private_cache_inventory']
    if proof.get('cost_gate') == 'unavailable':
        if proof.get('cost_missing') != missing or proof.get('costs'):
            fail('contradictory unavailable cost evidence')
        return False
    costs = proof.get('costs', [])
    if proof.get('cost_gate') != 'pass' or proof.get('cost_missing') != [] or len(costs) != 2:
        fail('both cold phases require measured cost')
    reports = receipt.get('result', {}).get('outcome', {}).get('Reports', [])
    observed = receipt.get('observations', [])
    if len(reports) != 2 or len(observed) != 2:
        fail('measured native phase controls missing')
    total_output, duration = 0, 0
    roots = ['/scratch/'+n for n in ('bazel-user', 'bazel-output', 'repository-cache', 'gocache', 'gomodcache', 'cache')]
    for i, phase in enumerate(('plan', 'execute')):
        cost, report, control = costs[i], reports[i], observed[i]
        allowance = control.get('allowance', {})
        metrics = cost.get('metrics', {})
        o, cache = metrics.get('observations', {}), metrics.get('cache', {})
        if (set(cost) != {'phase', 'planning_digest', 'attempt_digest', 'request_digest', 'seal_digest', 'stdout_sha256', 'stderr_sha256', 'worker_output_bytes', 'metrics'}
                or cost.get('phase') != phase or report.get('Phase') != phase or control.get('phase') != phase
                or report.get('ExitCode') != 0 or report.get('Removed') is not True or report.get('StopReason') != '' or report.get('Failure') is not None
                or any(not hash_valid(cost.get(k)) for k in ('planning_digest', 'attempt_digest', 'request_digest', 'seal_digest', 'stdout_sha256', 'stderr_sha256'))
                or cost['planning_digest'] != allowance.get('planning_digest') or cost['attempt_digest'] != allowance.get('attempt_digest') or cost['seal_digest'] != control.get('seal')
                or cost['planning_digest'] != costs[0]['planning_digest'] or cost['attempt_digest'] != costs[0]['attempt_digest']
                or (cost['request_digest'] == cost['planning_digest']) != (phase == 'plan')
                or set(metrics) != {'schema', 'observations', 'cache'} or metrics.get('schema') != 'phebs-typed-native-corpus-cost-v1'
                or digest(canonical(metrics)+b'\n') != cost['stderr_sha256']):
            fail('cost control or native completion binding')
        numeric = ('interval_nanoseconds', 'duration_nanoseconds', 'samples', 'sampled_child_lifetimes', 'sampled_process_fd_peak', 'sampled_aggregate_fd_peak', 'vanished', 'raced', 'unexpected_errors')
        if (set(o) != {'version', *numeric, 'child_lifetimes_lower_bound', 'fd_counts_non_atomic', 'unavailable'}
                or any(type(o.get(k)) is not int or o[k] < 0 for k in numeric)
                or o.get('version') != 'phebs-t451b-sampled-observations-v1' or o['interval_nanoseconds'] != 50000000
                or not 0 < o['duration_nanoseconds'] <= 300000000000 or o['samples'] < 2
                or not 0 < o['sampled_child_lifetimes'] <= 65536 or o['child_lifetimes_lower_bound'] is not True
                or o['fd_counts_non_atomic'] is not True or not 0 < o['sampled_process_fd_peak'] <= 128
                or not o['sampled_process_fd_peak'] <= o['sampled_aggregate_fd_peak'] <= 128*294
                or o['unavailable'] is not False or o['unexpected_errors'] != 0):
            fail('sampled lifetime or descriptor evidence unavailable')
        counters = ('entries', 'regular_files', 'directories', 'symlinks', 'unique_inodes', 'logical_bytes', 'allocated_bytes')
        if (set(cache) != {'version', 'roots', 'missing_roots', *counters, 'complete'}
                or any(type(cache.get(k)) is not int or cache[k] < 0 for k in counters)
                or cache.get('version') != 'phebs-t451b-private-cache-v1' or cache.get('complete') is not True or cache.get('roots') != roots
                or not 0 < cache['entries'] <= POLICY['scratch_inodes'] or cache['unique_inodes'] > cache['entries']
                or cache['regular_files']+cache['directories']+cache['symlinks'] != cache['entries']
                or max(cache['logical_bytes'], cache['allocated_bytes']) > POLICY['scratch_bytes']
                or not isinstance(cache.get('missing_roots'), list) or len(set(cache['missing_roots'])) != len(cache['missing_roots'])
                or any(n not in roots for n in cache['missing_roots'])):
            fail('private cache evidence unavailable')
        resources = report.get('Resources', {})
        bounds = {'memory_peak_bytes': POLICY['memory_bytes'], 'sampled_peak_rss_bytes': POLICY['memory_bytes'],
                  'sampled_peak_processes': POLICY['tasks'], 'sampled_peak_scratch_bytes': POLICY['scratch_bytes'],
                  'sampled_peak_scratch_inodes': POLICY['scratch_inodes']}
        if (resources.get('limits_verified') is not True or resources.get('sampling_unavailable') is not False
                or any(type(resources.get(k)) is not int or not 0 <= resources[k] <= bound for k, bound in bounds.items())
                or type(resources.get('samples')) is not int or resources['samples'] <= 0
                or any(resources.get(k) != 0 for k in ('memory_oom_events', 'memory_oom_kills', 'memory_limit_events', 'task_limit_events'))
                or resources.get('per_process_descriptors') != 128 or resources.get('aggregate_descriptor_ceiling') != 128*294):
            fail('native resource cost not established')
        if type(cost.get('worker_output_bytes')) is not int or not len(canonical(metrics))+1 <= cost['worker_output_bytes'] <= POLICY['output_bytes']-total_output:
            fail('shared physical worker output cost')
        total_output += cost['worker_output_bytes']
        duration += o['duration_nanoseconds']
    if duration > 300000000000:
        fail('sampled durations exceed shared wall')
    return True


def compare_corpus(paths):
    """Offline source-free equality at the three frozen function identities.

    Registration readiness additionally requires complete native cost records
    from both phases of every cohort; unavailable measurements remain open.
    """
    symbols, inputs, measured = {}, [], []
    names = {'ordinary': ('NewOutWriter', 'NewErrWriter'),
             'proto': ('NewRemoteErrorResult',),
             'fanout': ('NewOutWriter', 'NewErrWriter', 'NewRemoteErrorResult')}
    counts = {'ordinary': (3, 535, 148), 'proto': (1, 1309, 241),
              'fanout': (13, 13278, 2350)}
    for cohort in COHORTS:
        raw = read(paths[cohort], MAX_RECEIPT)
        receipt = decode(raw, MAX_RECEIPT)
        result = receipt.get('result', {})
        proof = result.get('corpus', {})
        if (receipt.get('schema') != CORPUS_SCHEMA or receipt.get('case') != 'success'
                or receipt.get('pass') is not True or not hash_valid(receipt.get('config'))
                or any(receipt.get(k) is not True for k in ('native_absent', 'workspace_drained', 'engine_joined'))
                or any(result.get(k) is not True for k in ('settled', 'no_replay', 'publication_verified', 'growth_released'))
                or result.get('execution_error') is not False
                or result.get('cost_stop') is not None
                or proof.get('cohort') != cohort or proof.get('commit') != CORPUS_COMMIT
                or proof.get('oracle_sha256') != ORACLE_SHA256 or proof.get('archive_sha256') != ARCHIVE_SHA256
                or proof.get('navigation_verified') is not True or proof.get('source_git_drained') is not True
                or proof.get('generated_documents') != 0
                or tuple(proof.get(k) for k in ('documents', 'occurrences', 'definitions')) != counts[cohort]
                or proof.get('cross_cohort') != 'pending'):
            fail('incomplete frozen corpus receipt')
        rows = proof.get('symbols', [])
        if tuple(row.get('name') for row in rows) != names[cohort]:
            fail('closed oracle identities')
        for row in rows:
            expected = 16 if row['name'] == 'NewRemoteErrorResult' and cohort == 'fanout' else 1
            if (not hash_valid(row.get('symbol_sha256')) or row.get('query_points') != expected
                    or row.get('definition_locations') != (0 if cohort == 'fanout' else 1)
                    or row.get('hover_payloads') != (0 if cohort == 'fanout' else 1)
                    or row.get('reference_points') != (expected if cohort == 'fanout' else 0)):
                fail('frozen oracle point proof')
            prior = symbols.setdefault(row['name'], row['symbol_sha256'])
            if prior != row['symbol_sha256']:
                fail('cross-cohort emitted symbol mismatch')
        measured.append(corpus_cost(receipt, proof))
        inputs.append(dict(cohort=cohort, receipt_sha256=digest(raw), config=receipt['config']))
    return dict(schema='phebs-typed-native-corpus-comparison-v1', commit=CORPUS_COMMIT,
                oracle_sha256=ORACLE_SHA256, inputs=inputs, frozen_oracle_points=21,
                cross_cohort_identity='pass', product_navigation='pass',
                cost_gate='pass' if all(measured) else 'unavailable', registration_ready=all(measured))


def acceptance_id(value):
    return isinstance(value, str) and re.fullmatch(r'[a-z][a-z0-9-]{0,31}', value)


def build(output):
    output = Path(output).absolute()
    output.mkdir(mode=0o700)  # deliberately refuses an existing output directory
    repo = Path(__file__).resolve().parents[2]
    env = dict(os.environ, GOENV='off', GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOWORK='off', GOOS='linux', GOARCH='arm64', CGO_ENABLED='0')
    subprocess.run(['go', 'build', '-trimpath', '-o', str(output / 'phebs'), './cmd/phebs'], cwd=repo, env=env, check=True, timeout=300)
    subprocess.run(['go', 'test', '-c', '-o', str(output / 'native-acceptance.test'), './internal/typedexecutor'], cwd=repo, env=env, check=True, timeout=300)
    for name in ('phebs', 'native-acceptance.test'):
        print(name, *file_hash(output / name, MAX_FILE))
    print('Unfilled: finalized neutral lock/cache/selection, seed from real APIs, profile/inventory, VM engine/formatter/image and source identities. No config was invented.')


# This fixed remote program has no arbitrary command or extraction option. It
# refuses all existing stage roots, never follows archive links, and never owns
# VM lifecycle or cleanup of native artifacts (the Go owners do that).
REMOTE_BODY = r'''
import hashlib,json,os,pathlib,re,signal,stat,subprocess,sys,tarfile
signal.alarm(600)
B=pathlib.Path('/var/lib/phebs-typed-acceptance')
C=('success','cancel','wall','hard-death','canary','dry-run')
def reject(): raise RuntimeError('neutral acceptance transport refused')
def digest(b): return 'sha256:'+hashlib.sha256(b).hexdigest()
def fixed_input(name,size,mode,corpus):
 if name=='source-git.tar': return corpus and size<=4<<20 and mode==0o400
 return name in {'config.json','inventory.json','profile.json','seed.surql','native-acceptance.test','surreal','deployment.json'} or name.startswith('bundle/')
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
if os.geteuid()!=0 or len(sys.argv)<4: reject()
a,ident=sys.argv[1:3]
if not re.fullmatch('[a-z][a-z0-9-]{0,31}',ident): reject()
checkdir(B);root=B/ident
if a=='stage':
 expected=sys.argv[3]
 if len(sys.argv)!=4 or not re.fullmatch('sha256:[0-9a-f]{64}',expected): reject()
 total=0;seen=set();dirs=set();corpus=False
 for row,contents in read_ustar(sys.stdin.buffer):
  name=row.name;p=pathlib.PurePosixPath(name)
  if not row.isfile() or p.is_absolute() or str(p)!=name or '..' in p.parts or '\\' in name or any(ord(c)<32 or ord(c)==127 for c in name) or name in seen or row.size<0 or row.size>256<<20 or row.mode not in (0o400,0o500): reject()
  if not fixed_input(name,row.size,row.mode,corpus): reject()
  if not seen:
   if name!='config.json' or row.size>16384: reject()
   data=b''.join(contents)
   if digest(data)!=expected: reject()
   corpus=json.loads(data).get('schema')=='phebs-typed-native-corpus-acceptance-v1'
   root.mkdir(mode=0o700)
   contents=iter([data])
  seen.add(name);total+=row.size
  if len(seen)>(50008 if corpus else 50007) or total>(2<<30)+2*(256<<20)+(32<<20): reject()
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
   for chunk in contents:
    out.write(chunk)
   out.flush();os.fsync(out.fileno())
 raw=read(root/'config.json',16384)
 if digest(raw)!=expected: reject()
 cfg=json.loads(raw)
 invraw=read(root/'inventory.json',16<<20)
 if digest(invraw)!=cfg['inventory_sha256']: reject()
 inv=json.loads(invraw)
 expected_names={'config.json','inventory.json','profile.json','seed.surql','native-acceptance.test','surreal','deployment.json'}
 if cfg['schema']=='phebs-typed-native-corpus-acceptance-v1':
  expected_names.add('source-git.tar')
  if digest(read(root/'source-git.tar',4<<20))!=cfg['source_git_sha256']: reject()
 for f in inv['files']:
  name='bundle/'+f['path'];expected_names.add(name)
  if digest(read(root/name,f['bytes']))!=f['digest']: reject()
 for name,key,n in [('deployment.json','deployment_sha256',16384),('profile.json','profile_sha256',16384),('seed.surql','seed_sha256',4<<20),('native-acceptance.test','test_sha256',256<<20),('surreal','engine_sha256',256<<20)]:
  if digest(read(root/name,n))!=cfg[key]: reject()
 if seen!=expected_names or cfg['id']!=ident: reject()
 write(root/'staged.json',json.dumps({'config':expected},separators=(',',':')).encode())
elif a in ('run','collect'):
 if len(sys.argv)!=(6 if a=='run' else 5): reject()
 case,expected=sys.argv[3:5]
 if case not in C or not re.fullmatch('sha256:[0-9a-f]{64}',expected): reject()
 checkdir(root)
 raw=read(root/'config.json',16384)
 if digest(raw)!=expected: reject()
 cfg=json.loads(raw)
 if cfg['schema']=='phebs-typed-native-corpus-acceptance-v1' and case!='success': reject()
 if json.loads(read(root/'staged.json',1024))!={'config':expected}: reject()
 if a=='collect':
  data=read(root/case/'receipt.json',128<<10)
  receipt=json.loads(data)
  if receipt.get('config')!=expected or receipt.get('case')!=case: reject()
  sys.stdout.buffer.write(data)
 else:
  if cfg['deployment_sha256']!=sys.argv[5] or digest(read(root/'deployment.json',16384))!=sys.argv[5]: reject()
  cases=C[:4] if case in C[:4] else C[4:]
  for prior in cases[:cases.index(case)]:
   receipt=json.loads(read(root/prior/'receipt.json',128<<10))
   if not receipt.get('pass') or receipt.get('config')!=expected or receipt.get('case')!=prior: reject()
  if digest(read(root/'native-acceptance.test',256<<20))!=cfg['test_sha256']: reject()
  write(root/('dispatch-'+case+'.json'),json.dumps({'config':expected,'case':case},separators=(',',':')).encode())
  result=subprocess.run([str(root/'native-acceptance.test'),'-test.run=^TestTypedNativeAcceptance$','-test.count=1','-test.timeout=600s','-typed-native-config='+str(root/'config.json'),'-typed-native-case='+case],env={'PATH':'/usr/bin:/bin','HOME':str(root)},stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True,timeout=600)
  if result.returncode: reject()
else: reject()
'''


REMOTE = inspect.getsource(read_ustar) + '\n' + REMOTE_BODY

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('build'); p.add_argument('--output', required=True)
    p = sub.add_parser('compare-corpus')
    for cohort in COHORTS: p.add_argument('--' + cohort, required=True)
    p.add_argument('--output', required=True)
    for action in ('check', 'stage'):
        p = sub.add_parser(action); p.add_argument('--input', required=True); p.add_argument('--config-sha', required=True)
    for action in ('run', 'collect'):
        p = sub.add_parser(action); p.add_argument('--id', required=True); p.add_argument('--case', choices=CASES, required=True); p.add_argument('--config-sha', required=True)
        if action == 'collect': p.add_argument('--output', required=True)
        else: p.add_argument('--deployment', required=True)
    args = parser.parse_args()
    if args.command == 'build': build(args.output)
    elif args.command == 'compare-corpus':
        exclusive(args.output, canonical(compare_corpus({c: getattr(args, c) for c in COHORTS})))
    elif args.command == 'check':
        cfg, rows = inputs(args.input, args.config_sha)
        print(cfg['id'], len(rows), 'verified inputs; no execution')
    elif args.command == 'stage': stage(args.input, args.config_sha)
    elif args.command == 'run': run(args.id, args.case, args.config_sha, args.deployment)
    else: collect(args.id, args.case, args.config_sha, args.output)


if __name__ == '__main__':
    main()
