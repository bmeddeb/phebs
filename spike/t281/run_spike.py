#!/usr/bin/env python3
"""Frozen-input isolated build and two fresh joined reads; no Redis execution.

Execution requires an independently checkpointed candidate-contract and the
lead's serialized native slot. This source file grants no execution authority.
"""
from pathlib import Path, PurePosixPath
import argparse
import ctypes
import hashlib
import json
import os
import platform
import signal
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time

import score_outputs

ROOT = Path(__file__).resolve().parent
PREREG = 'ad9f09d50f77a789dd5e9287ae8194ace2ca0d0937d2ac1e764656710893c518'
CHECKPOINT = 'c525a50f'


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def load(path):
    return json.loads(path.read_text())


def write_new(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2, sort_keys=True)
        out.write('\n')


def verify_frozen():
    raw = (ROOT / 'preregistration.json').read_bytes()
    if sha(raw) != PREREG:
        raise ValueError('preregistration drift')
    inventory = json.loads(raw)['files']
    for f in inventory:
        raw = (ROOT / f['path']).read_bytes()
        if len(raw) != f['bytes'] or sha(raw) != f['sha256']:
            raise ValueError('frozen input drift; path/context omitted')
    return {'files': len(inventory), 'bytes': sum(f['bytes'] for f in inventory), 'preregistration_sha256': PREREG, 'source_checkpoint': CHECKPOINT}


def source_inventory():
    paths = sorted((ROOT / 'reader').glob('*.go')) + [ROOT / 'go.mod', ROOT / 'run_spike.py', ROOT / 'score_outputs.py', ROOT / 'output-schema.json', ROOT / 'projection-witnesses.json']
    return [{'path': str(p.relative_to(ROOT)), 'bytes': p.stat().st_size, 'sha256': sha(p.read_bytes())} for p in sorted(paths)]


def materialize(root):
    """One exact bounded archive member at a time, hashed flat blob names only.

    The candidate manifest contains source/module/role provenance only. No
    truth labels, expectations, source decision tables or evaluator outcomes.
    """
    root.mkdir(mode=0o700)
    files, source = [], {}

    def add(pop, repo, commit, path, raw, role, missing=False):
        if not path or PurePosixPath(path).is_absolute() or '..' in PurePosixPath(path).parts or '\\' in path:
            raise ValueError('input member path')
        digest = sha(raw)
        row = {'population': pop, 'repository': repo, 'commit': commit, 'path': path, 'role': role, 'sha256': digest, 'bytes': len(raw)}
        if missing:
            row['missing'] = True
        else:
            target = root / digest
            if not target.exists():
                with target.open('xb') as out:
                    out.write(raw)
            source[pop, path] = raw
        files.append(row)

    for pop in load(ROOT / 'source-receipt.json')['records']:
        expected = {f['path']: f for f in pop['files']}
        with tarfile.open(ROOT / pop['archive']) as archive:
            members = archive.getmembers()
            if len(members) != len(expected) or len(members) > 1024 or {m.name for m in members} != set(expected):
                raise ValueError('archive member census')
            for member in members:
                truth = expected[member.name]
                if not member.isfile() or member.size != truth['bytes'] or member.size > 262144:
                    raise ValueError('archive member type/size')
                raw = archive.extractfile(member).read(262145)
                if sha(raw) != truth['sha256'] or hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest() != truth['git_blob']:
                    raise ValueError('archive member Git/digest')
                role = 'core-json' if pop['name'] == 'redis' and member.name.endswith('.json') else 'acl' if member.name.endswith('.acl') else 'go-source' if member.name.endswith('.go') else 'module' if member.name.endswith('go.mod') else 'retained-metadata'
                add(pop['name'], pop['repository'], pop['commit'], member.name, raw, role)
    controls = load(ROOT / 'controls.json')
    for case in controls['cases'] + controls['admission_controls']:
        pop = 'control/' + case['id']; path = case['source_path']; target = ROOT / path
        raw = target.read_bytes() if target.exists() else b''
        if 'source_sha256' in case and (sha(raw) != case['source_sha256'] or len(raw) != case['source_bytes']):
            raise ValueError('control input custody')
        add(pop, 'authored-neutral', '', path, raw, case['source_class'], missing=not target.exists())
        if case['source_class'] == 'go-source':
            version = case.get('version', 'v9.14.1'); major = 'v8' if version.startswith('v8.') else 'v9'
            # A virtual evaluator module renders the already frozen version
            # metadata. It is not an application-source edit or dependency.
            module = f'module evaluation.local/t281/{case["id"]}\n\ngo 1.26.0\n\nrequire github.com/redis/go-redis/{major} {version}\n'.encode()
            add(pop, 'authored-neutral', '', 'go.mod', module, 'module')
    if len(files) > 1024 or sum(f['bytes'] for f in files) > 8 << 20:
        raise ValueError('materialized input aggregate bound')
    return {'format': 't281-reader-input-v1', 'root': str(root), 'files': sorted(files, key=lambda f: (f['population'], f['path']))}, source


class Usage(ctypes.Structure):
    _fields_ = [('uuid', ctypes.c_uint8 * 16)] + [(name, ctypes.c_uint64) for name in ('user', 'system', 'idle', 'interrupt', 'pageins', 'wired', 'resident', 'footprint', 'start', 'exit', 'child_user', 'child_system', 'child_idle', 'child_interrupt', 'child_pageins', 'child_elapsed', 'read', 'written')]


def run_child(argv, cwd, stdin, outpath, errpath, wall, rss_limit=None,go_root=None):
    if sys.platform != 'darwin':
        raise ValueError('this prospective runner adapter is Darwin-only')
    library = ctypes.CDLL('/usr/lib/libproc.dylib', use_errno=True)
    library.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.POINTER(Usage)]
    env = os.environ.copy()
    if go_root:
        env['GOROOT']=str(go_root)
    else:
        env.pop('GOROOT',None)
    env.update(GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOWORK='off', GOMAXPROCS='2', CGO_ENABLED='0', GOENV='off', GOFLAGS='', GOEXPERIMENT='')
    started = time.monotonic(); peak = samples = errors = 0; stop = None
    with outpath.open('xb') as stdout, errpath.open('xb') as stderr, stdin.open('rb') as input_file:
        child = subprocess.Popen(argv, cwd=cwd, stdin=input_file, stdout=stdout, stderr=stderr, env=env, start_new_session=True)
        try:
            while True:
                waited, status, usage = os.wait4(child.pid, os.WNOHANG)
                if waited:
                    child.returncode = os.waitstatus_to_exitcode(status)
                    break
                current = Usage()
                if library.proc_pid_rusage(child.pid, 2, ctypes.byref(current)) == 0:
                    peak = max(peak, current.resident); samples += 1
                else:
                    errors += 1
                if time.monotonic() - started >= wall - 2:
                    stop = 'wall-limit-with-joined-cleanup-reserve'
                if rss_limit and peak > rss_limit:
                    stop = 'observed-rss-limit'
                if outpath.stat().st_size > 32 << 20 or errpath.stat().st_size > 65536:
                    stop = 'output-limit'
                if stop:
                    os.killpg(child.pid, signal.SIGKILL)
                    _, status, usage = os.wait4(child.pid, 0)
                    child.returncode = os.waitstatus_to_exitcode(status)
                    break
                time.sleep(.02)
        except BaseException as failure:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            if child.returncode is None:
                _, status, usage = os.wait4(child.pid, 0)
                child.returncode = os.waitstatus_to_exitcode(status)
            stop='runner-interrupted-'+type(failure).__name__
        joined = child.wait()
        try:
            os.killpg(child.pid, 0)
            group_absent = False
        except ProcessLookupError:
            group_absent = True
        if not group_absent:
            os.killpg(child.pid, signal.SIGKILL)
            stop = 'descendant-custody-unavailable'
            deadline=time.monotonic()+1
            while time.monotonic()<deadline:
                try:
                    os.killpg(child.pid,0)
                except ProcessLookupError:
                    group_absent=True
                    break
                time.sleep(.02)
        elapsed = time.monotonic() - started
    if samples == 0 and not stop:
        stop = 'rss-measurement-unavailable'
    if elapsed > wall:
        stop = 'outer-wall-limit'
    return {'argv': argv, 'exit': joined, 'stop': stop, 'joined': True, 'owned_process_group_absent': group_absent, 'wall_seconds': elapsed, 'wall_limit_seconds_including_cleanup': wall, 'execution_reserve_seconds': 2, 'observed_rss_limit_bytes': rss_limit, 'peak_observed_direct_pid_rss_bytes': peak, 'rss_samples': samples, 'rss_sample_errors': errors, 'sample_interval_ms': 20, 'exact_child_wait4_maxrss_bytes': usage.ru_maxrss, 'rss_qualification': 'Observed direct-PID samples plus exact wait4 direct-child peak; not a hard heap cap. Build excludes compiler descendants; parser has no child-producing imports. No aggregate family RSS claim.', 'stdout_bytes': outpath.stat().st_size, 'stderr_bytes': errpath.stat().st_size, 'stdout_sha256': sha(outpath.read_bytes()), 'stderr_sha256': sha(errpath.read_bytes())}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--lineage', required=True)
    parser.add_argument('--execute-reviewed-contract', required=True, help='Exact SHA256 of independently reviewed candidate-contract.json')
    args = parser.parse_args()
    if not args.lineage or not re_lineage(args.lineage):
        raise ValueError('invalid result lineage')
    contract = ROOT / 'candidate-contract.json'
    if sha(contract.read_bytes()) != args.execute_reviewed_contract:
        raise ValueError('unreviewed candidate contract')
    expected = load(contract)
    if expected['implementation_files'] != source_inventory():
        raise ValueError('candidate source inventory drift')
    frozen = verify_frozen(); results = ROOT / 'results' / args.lineage
    results.mkdir(parents=True, exist_ok=False)
    go_tool = Path(shutil.which('go') or '').resolve()
    if not go_tool.is_file():
        raise ValueError('Go tool unavailable')
    if expected['go_tool'] != {'path': str(go_tool), 'bytes': go_tool.stat().st_size, 'sha256': sha(go_tool.read_bytes())}:
        raise ValueError('Go executable binding drift')
    go_root=go_tool.parent.parent
    for tool in expected['compiler_tools']:
        path=Path(tool['path'])
        if not path.is_file() or path.stat().st_size!=tool['bytes'] or sha(path.read_bytes())!=tool['sha256']:
            raise ValueError('Go compiler tool binding drift')
    git_head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    receipt = {'git_head_at_build': git_head, 'go_tool': {'path': str(go_tool), 'bytes': go_tool.stat().st_size, 'sha256': sha(go_tool.read_bytes())}, 'git_qualification': 'Actual evaluator HEAD recorded separately from frozen input checkpoint and file inventory; no upstream evaluator provenance substitution.', 'schema': 't281-first-attempt-v1', 'outcome': 'preparing', 'frozen': frozen, 'candidate_contract_sha256': args.execute_reviewed_contract, 'source_inventory': source_inventory(), 'host': {'platform': platform.platform(), 'machine': platform.machine()}, 'passes': []}
    private = Path(tempfile.mkdtemp(prefix='phebs-t281-'))
    try:
        manifest, source = materialize(private / 'blobs')
        request = private / 'input.json'; request.write_text(json.dumps(manifest, separators=(',', ':')))
        empty = private / 'empty'; empty.write_bytes(b'')
        binary = private / 'reader'
        receipt['build'] = run_child([str(go_tool), 'build', '-trimpath', '-buildvcs=true', '-o', str(binary), './reader'], ROOT, empty, results / 'build.stdout', results / 'build.stderr', 60,go_root=go_root)
        if receipt['build']['exit'] or receipt['build']['stop']:
            raise ValueError('first build stopped')
        receipt['binary_sha256'] = sha(binary.read_bytes())
        receipt['go_binary_metadata']=subprocess.check_output([str(go_tool),'version','-m',str(binary)],text=True,timeout=5)
        for number in (1, 2):
            out = results / f'pass{number}.jsonl'; err = results / f'pass{number}.stderr'
            private_out=private/f'pass{number}.jsonl';private_err=private/f'pass{number}.stderr'
            metrics = run_child([str(binary)], ROOT, request, private_out, private_err, 30, 1 << 30)
            receipt['passes'].append(metrics)
            # Scan even failed/stopped children before publishing artifacts.
            # A safety failure retains original digests/counts only, never a
            # credential-bearing fact/log. It is still a terminal FAIL.
            score_outputs.credential_scan(private_out.read_bytes()+private_err.read_bytes(),source)
            with out.open('xb') as stream:stream.write(private_out.read_bytes())
            with err.open('xb') as stream:stream.write(private_err.read_bytes())
            if metrics['exit'] or metrics['stop']:
                raise ValueError('first candidate pass stopped')
        if receipt['passes'][0]['stdout_sha256'] != receipt['passes'][1]['stdout_sha256']:
            raise ValueError('fresh-state deterministic output mismatch')
        # Quality is evaluated only after preserving both candidate bytes.
        score = score_outputs.score((results / 'pass1.jsonl').read_bytes(), (results / 'pass1.stderr').read_bytes() + (results / 'pass2.stderr').read_bytes(), manifest, source)
        write_new(results / 'score.json', score)
        receipt['outcome'] = score['outcome']
    except BaseException as error:
        receipt['outcome'] = 'stopped-no-retry'
        receipt['reason_class'] = type(error).__name__
        raise
    finally:
        all_children=[receipt['build']] if 'build' in receipt else []
        all_children+=receipt['passes']
        joined=bool(all_children) and all(c['joined'] and c['owned_process_group_absent'] for c in all_children)
        if joined:
            shutil.rmtree(private);receipt['scratch_removed_after_child_join']=True
        else:
            receipt['scratch_removed_after_child_join']=False
            receipt['private_runtime_retained']=str(private)
        receipt['frozen_after'] = verify_frozen()
        receipt['source_after'] = source_inventory()
        receipt['source_inventory_unchanged']=receipt['source_after']==receipt['source_inventory']
        receipt['git_head_after']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
        if receipt['git_head_after']!=receipt['git_head_at_build'] or not receipt['source_inventory_unchanged']:
            receipt['outcome']='stopped-source-binding-drift'
        write_new(results / 'receipt.json', receipt)


def re_lineage(text):
    return all(c.isascii() and (c.isalnum() or c in '-_') for c in text)


if __name__ == '__main__':
    try:
        main()
    except BaseException:
        print('t281 runner stopped; source-free first-attempt receipt retained when preparation completed', file=sys.stderr)
        sys.exit(1)
