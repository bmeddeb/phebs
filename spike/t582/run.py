#!/usr/bin/env python3
"""One offline build and exactly two fresh globally bounded reader processes.

Source review, immutable checkpoint and the lead's serialized slot are required;
this file and its CLI do not themselves grant execution authority.
"""
from pathlib import Path, PurePosixPath
import argparse
import base64
import ctypes
import hashlib
import json
import os
import platform
import resource
import selectors
import shutil
import signal
import subprocess
import sys
import tempfile
import time

import score

ROOT = Path(__file__).resolve().parent
WORKSPACE = ROOT.parent.parent
PREREG = '6e9f39ad56cbba47eb4a5ea74630b3a33f8b747ac6c4db7d1256ccaf84fd51e5'
SOURCE_CHECKPOINT = '842fb139edc1fa104c06d66d446b1bfc09a85314'
OWN_FILES = ['go.mod', 'export/main.go', 'export/main_test.go', 'run.py', 'score.py', 'test_score.py', 'call-span-witnesses.json']


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def load(path):
    return score.load_json(path.read_bytes())


def write_new(path, value):
    with path.open('x') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')


def safe_read(relative, limit=8 << 20):
    path = PurePosixPath(relative)
    if not relative or path.is_absolute() or str(path) != relative or '..' in path.parts or '\\' in relative:
        raise ValueError('noncanonical frozen path')
    current = ROOT
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError('symlink source admission')
    if not current.is_file() or current.stat().st_size > limit:
        raise ValueError('source file admission')
    with current.open('rb') as stream:
        raw = stream.read(limit + 1)
    if len(raw) > limit:
        raise ValueError('source read bound')
    return raw


def verify_frozen():
    raw = safe_read('preregistration.json')
    if sha(raw) != PREREG:
        raise ValueError('preregistration drift')
    inventory = score.load_json(raw)['files']
    for row in inventory:
        raw = safe_read(row['path'])
        if len(raw) != row['bytes'] or sha(raw) != row['sha256']:
            raise ValueError('frozen input drift')
    return {'source_checkpoint': SOURCE_CHECKPOINT, 'preregistration_sha256': PREREG,
            'files': len(inventory), 'bytes': sum(row['bytes'] for row in inventory)}


def source_inventory():
    paths = OWN_FILES + ['source-review.json', '../../go.mod', '../../go.sum', '../../internal/extract/sdk/sdk.go', '../../internal/extract/sdk/attribution.go',
                         '../../internal/extract/extractors/thriftgo/thriftgo.go']
    rows = []
    for relative in sorted(paths):
        path = ROOT / relative
        raw = path.read_bytes()
        rows.append({'path': relative, 'bytes': len(raw), 'sha256': sha(raw)})
    actual_go = sorted(str(p.relative_to(ROOT)) for p in (ROOT / 'export').glob('*.go'))
    if actual_go != ['export/main.go', 'export/main_test.go']:
        raise ValueError('export package source census')
    if (ROOT / 'go.sum').exists():
        raise ValueError('unexpected nested dependency metadata')
    return rows


def prepare_plan():
    """Candidate receives source/identity only, no oracle labels/expectations."""
    controls = load(ROOT / 'controls.json')
    prereg = load(ROOT / 'preregistration.json')
    units, source = [], {}

    def add_unit(identifier, entries):
        repo = 'jaegertracing/jaeger-client-go' if identifier == 'public' else 'authored-t582/' + identifier
        commit = '8d8e8fcfd04de42b8482476abac6a902fca47c18' if identifier == 'public' else 'authored-' + PREREG
        files = []
        for logical, origin, context, raw in sorted(entries):
            if (identifier, logical) in source:
                raise ValueError('duplicate logical source')
            source[identifier, logical] = raw
            files.append({'path': logical, 'origin': origin, 'context': context,
                          'sha256': sha(raw), 'content': base64.b64encode(raw).decode('ascii')})
        if len(files) > 64 or sum(len(source[identifier, f['path']]) for f in files) > 8 << 20:
            raise ValueError('case source bound')
        units.append({'id': identifier, 'repository': repo, 'commit': commit, 'files': files})

    context = controls['contexts']['public']
    entries = [(f['logical_path'], f['input'], True, safe_read(f['input'])) for f in context]
    entries += [(path, 'inputs/public/' + path, False, safe_read('inputs/public/' + path))
                for path in prereg['admission']['handwritten_source']]
    add_unit('public', entries)
    for case in controls['cases']:
        entries = [(f['logical_path'], f['input'], True, safe_read(f['input']))
                   for f in controls['contexts'][case['context']]]
        if case.get('source'):
            entries.append(('consumer.go', case['source'], False, safe_read(case['source'])))
        if case.get('source_recipe'):
            recipe = load(ROOT / case['source_recipe'])
            raw = recipe['prefix_utf8'].encode() + recipe['repeat_ascii'].encode() * recipe['repeat_count'] + recipe['suffix_utf8'].encode()
            if len(raw) != 4194305 or len(raw) != recipe['bytes'] or sha(raw) != recipe['sha256']:
                raise ValueError('oversize recipe custody')
            entries.append(('consumer.go', case['source_recipe'], False, raw))
        add_unit(case['id'], entries)
    if len(units) != 27 or sum(map(len, source.values())) != 9436787:
        raise ValueError('whole-pass offered-source census')
    plan = {'schema': 't582-plan-v1', 'preregistration_sha256': PREREG, 'units': units}
    raw = json.dumps(plan, separators=(',', ':'), sort_keys=True).encode()
    if len(raw) > 32 << 20:
        raise ValueError('encoded plan bound')
    return plan, raw, source


class Usage(ctypes.Structure):
    _fields_ = [('uuid', ctypes.c_uint8 * 16)] + [(name, ctypes.c_uint64) for name in (
        'user', 'system', 'idle', 'interrupt', 'pageins', 'wired', 'resident', 'footprint',
        'start', 'exit', 'child_user', 'child_system', 'child_idle', 'child_interrupt',
        'child_pageins', 'child_elapsed', 'read', 'written')]


def group_absent(pid):
    try:
        os.killpg(pid, 0)
        return False
    except ProcessLookupError:
        return True


def stop_group(pid):
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def bounded_diagnostic(error):
    return ''.join(c if 32 <= ord(c) <= 126 else '?' for c in str(error))[:240]


def custody_complete(children):
    return all(c['joined'] and c['owned_process_group_absent'] for c in children)


def run_child(argv, cwd, stdin, output, stderr, wall, stdout_cap, ledger):
    """Bound retained pipe bytes, stop the owned group, reap the exact child.

    Darwin direct-PID sampling is observed accounting, not a hard heap limit.
    Build descendants share custody, but their RSS is not added to driver RSS.
    """
    if sys.platform != 'darwin':
        raise ValueError('unadmitted runner host; this adapter is Darwin only')
    library = ctypes.CDLL('/usr/lib/libproc.dylib', use_errno=True)
    library.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.POINTER(Usage)]
    env = os.environ.copy()
    env.pop('GOROOT', None)
    env.update(GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOWORK='off', GOENV='off',
               GOFLAGS='', GOEXPERIMENT='', GOMAXPROCS='2', CGO_ENABLED='0', PYTHONDONTWRITEBYTECODE='1')
    started = time.monotonic()
    entry = {'argv': argv, 'launch_attempted': False, 'pid': None, 'joined': False,
             'owned_process_group_absent': False, 'phase': 'launching', 'stop': 'launch-custody-incomplete'}
    ledger.append(entry)  # Before Popen; any later exception keeps custody unknown.
    stopped = None
    peak, samples, sample_errors = 0, 0, 0
    counts = {'stdout': 0, 'stderr': 0}
    caps = {'stdout': stdout_cap, 'stderr': 65536}
    usage = None
    with selectors.DefaultSelector() as selector, output.open('xb') as out, stderr.open('xb') as err, stdin.open('rb') as incoming:
        entry['launch_attempted'] = True
        child = subprocess.Popen(argv, cwd=cwd, stdin=incoming, stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, env=env, start_new_session=True)
        entry.update(pid=child.pid, phase='running')
        try:
            for stream, kind, target in [(child.stdout, 'stdout', out), (child.stderr, 'stderr', err)]:
                os.set_blocking(stream.fileno(), False)
                selector.register(stream, selectors.EVENT_READ, (kind, target))
            while True:
                for key, _ in selector.select(.02):
                    block = os.read(key.fd, 65536)
                    if not block:
                        selector.unregister(key.fileobj)
                        key.fileobj.close()
                        continue
                    kind, target = key.data
                    available = max(0, caps[kind] - counts[kind])
                    target.write(block[:available])
                    counts[kind] += len(block)
                    if counts[kind] > caps[kind] and stopped is None:
                        stopped = kind + '-retained-output-bound'
                if child.returncode is None:
                    waited, status, current_usage = os.wait4(child.pid, os.WNOHANG)
                    if waited:
                        child.returncode = os.waitstatus_to_exitcode(status)
                        usage = current_usage
                    else:
                        sample = Usage()
                        if library.proc_pid_rusage(child.pid, 2, ctypes.byref(sample)) == 0:
                            peak = max(peak, sample.resident)
                            samples += 1
                        else:
                            sample_errors += 1
                if time.monotonic() - started >= wall - 2 and stopped is None:
                    stopped = 'wall-limit-including-cleanup-reserve'
                if stopped:
                    stop_group(child.pid)
                    if child.returncode is None:
                        _, status, usage = os.wait4(child.pid, 0)
                        child.returncode = os.waitstatus_to_exitcode(status)
                    break
                if child.returncode is not None and not selector.get_map():
                    break
        except BaseException as error:
            stopped = stopped or 'runner-interruption-' + type(error).__name__
            entry.update(stop=stopped, stop_diagnostic=bounded_diagnostic(error))
            stop_group(child.pid)
        finally:
            entry['phase'] = 'cleanup'
            if child.returncode is None:
                stop_group(child.pid)
                _, status, usage = os.wait4(child.pid, 0)
                child.returncode = os.waitstatus_to_exitcode(status)
            joined = child.wait()
            entry['joined'] = True
            if not group_absent(child.pid):
                stopped = stopped or 'surviving-owned-descendant'
                stop_group(child.pid)
            deadline = started + wall
            while not group_absent(child.pid) and time.monotonic() < deadline:
                time.sleep(.01)
            absent = group_absent(child.pid)
            entry['owned_process_group_absent'] = absent
            for key in list(selector.get_map().values()):
                # Preserve already available bounded trailing bytes after join.
                try:
                    block = os.read(key.fd, 65536)
                    kind, target = key.data
                    target.write(block[:max(0, caps[kind]-counts[kind])])
                    counts[kind] += len(block)
                    if counts[kind] > caps[kind]:
                        stopped = stopped or kind + '-retained-output-bound'
                except BlockingIOError:
                    pass
                except OSError as error:
                    stopped = stopped or 'cleanup-output-' + type(error).__name__
                finally:
                    key.fileobj.close()
            selector.close()
        elapsed = time.monotonic() - started
    if elapsed > wall:
        stopped = stopped or 'outer-wall-bound'
    entry.update({'argv': argv, 'exit': joined, 'stop': stopped, 'wall_seconds': elapsed,
            'wall_limit_seconds_including_cleanup': wall, 'execution_reserve_seconds': 2,
            'joined': True, 'owned_process_group_absent': absent,
            'peak_observed_direct_pid_rss_bytes': peak if samples else None,
            'rss_samples': samples, 'rss_sample_errors': sample_errors,
            'exact_wait4_direct_child_peak_rss_bytes': usage.ru_maxrss if usage else None,
            'rss_qualification': 'Direct driver/parser only; build compiler descendants excluded. Observation, not hard heap or family RSS guarantee.',
            'cpu_measurement': 'unmeasured',
            'stdout_observed_bytes': counts['stdout'], 'stderr_observed_bytes': counts['stderr'],
            'stdout_bytes': output.stat().st_size, 'stderr_bytes': stderr.stat().st_size,
            'stdout_sha256': sha(output.read_bytes()), 'stderr_sha256': sha(stderr.read_bytes()), 'phase': 'complete'})
    return entry


def git(*args):
    return subprocess.check_output(['git', *args], cwd=WORKSPACE, timeout=5, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', required=True, help='Fresh absolute result directory outside checkout')
    parser.add_argument('--execute-reviewed-contract', required=True)
    args = parser.parse_args()
    if sys.platform != 'darwin':
        raise ValueError('unadmitted runner host')
    contract_raw = safe_read('candidate-contract.json')
    if sha(contract_raw) != args.execute_reviewed_contract:
        raise ValueError('unreviewed implementation contract')
    contract = score.load_json(contract_raw)
    frozen = verify_frozen()
    before = source_inventory()
    if before != contract['implementation_files']:
        raise ValueError('implementation drift')
    go_tool = Path(shutil.which('go') or '').resolve()
    for row in contract['tool_files']:
        p = Path(row['path'])
        if p.is_symlink() or not p.is_file() or p.stat().st_size != row['bytes'] or sha(p.read_bytes()) != row['sha256']:
            raise ValueError('tool image drift')
    if str(go_tool) != contract['go_executable']:
        raise ValueError('Go executable selection drift')
    tracked = ['spike/t582/' + row['path'] if not row['path'].startswith('../../') else row['path'][6:] for row in before]
    tracked += ['spike/t582/candidate-contract.json', 'spike/t582/preregistration.json', 'spike/t582/source-review.json']
    git('ls-files', '--error-unmatch', *tracked)
    if git('status', '--porcelain', '--', *tracked):
        raise ValueError('tracked evaluator/source dirty')
    if git('merge-base', '--is-ancestor', SOURCE_CHECKPOINT, 'HEAD'):
        raise ValueError('source checkpoint ancestry')
    head, tree = git('rev-parse', 'HEAD'), git('rev-parse', 'HEAD^{tree}')
    result = Path(args.out)
    if not result.is_absolute() or result.exists() or result.is_relative_to(WORKSPACE):
        raise ValueError('result path must be fresh/outside checkout')
    result.mkdir(mode=0o700)
    scratch = Path(tempfile.mkdtemp(prefix='phebs-t582-'))
    children = []
    receipt = {'children': children, 'schema': 't582-first-execution-v1', 'outcome': 'preparing', 'frozen': frozen,
               'candidate_contract_sha256': args.execute_reviewed_contract, 'evaluator_git_commit': head,
               'evaluator_git_tree': tree, 'source_inventory': before, 'tools': contract['tool_files'],
               'host': {'platform': platform.platform(), 'machine': platform.machine()}, 'passes': []}
    try:
        plan, encoded, source = prepare_plan()
        request = scratch / 'request.json'; request.write_bytes(encoded)
        empty = scratch / 'empty'; empty.write_bytes(b'')
        receipt['candidate_input'] = {'sha256': sha(encoded), 'encoded_bytes': len(encoded), 'units': 27,
                                      'offered_source_bytes': sum(map(len, source.values())), 'oracle_fields_in_candidate_input': False}
        binary = scratch / 'reader'
        build = run_child([str(go_tool), 'build', '-mod=readonly', '-p=1', '-trimpath', '-buildvcs=true', '-o', str(binary), './export'], ROOT,
                          empty, result/'build.stdout', result/'build.stderr', 120, 65536, children)
        receipt['build'] = build
        if build['exit'] or build['stop'] or not build['owned_process_group_absent']:
            raise ValueError('first offline build stopped')
        receipt['binary'] = {'bytes': binary.stat().st_size, 'sha256': sha(binary.read_bytes())}
        metadata = run_child([str(go_tool), 'version', '-m', str(binary)], ROOT, empty,
                             result/'binary-metadata.stdout', result/'binary-metadata.stderr', 5, 65536, children)
        receipt['binary_metadata_command'] = metadata
        if metadata['exit'] or metadata['stop']:
            raise ValueError('binary metadata stopped')
        build_metadata = (result/'binary-metadata.stdout').read_text()
        if 'go1.26.5' not in build_metadata or 'vcs.revision='+head not in build_metadata or 'vcs.modified=false' not in build_metadata:
            raise ValueError('actual compiled tool/VCS metadata mismatch')
        for number in (1, 2):
            metrics = run_child([str(binary)], ROOT, request, result/f'pass{number}.jsonl',
                                result/f'pass{number}.stderr', 30, 16 << 20, children)
            receipt['passes'].append(metrics)
            if metrics['exit'] or metrics['stop'] or not metrics['owned_process_group_absent']:
                raise ValueError('first reader stop; no retry')
        if receipt['passes'][0]['stdout_sha256'] != receipt['passes'][1]['stdout_sha256']:
            raise ValueError('fresh-state determinism disagreement')
        observed = score.score((result/'pass1.jsonl').read_bytes(), plan, source, ROOT)
        write_new(result/'score.json', observed)
        receipt['outcome'] = observed['decision']
    except BaseException as error:
        receipt['outcome'] = 'stopped-no-retry'
        receipt['stop_class'] = type(error).__name__
        receipt['stop_diagnostic'] = bounded_diagnostic(error)
    finally:
        all_joined = custody_complete(children)
        receipt['all_supervised_workload_children_joined_and_groups_absent'] = all_joined
        receipt['git_metadata_qualification'] = 'Six read-only Git metadata calls use check_output with 5s timeout and exact direct-child wait. Their process groups/family RSS are not part of the supervised workload ledger.'
        if all_joined:
            shutil.rmtree(scratch)
            receipt['scratch_removed_after_join'] = True
        else:
            receipt['scratch_removed_after_join'] = False
            receipt['retained_scratch'] = str(scratch)
        try:
            receipt['frozen_after'] = verify_frozen()
            receipt['source_unchanged'] = source_inventory() == before
            receipt['evaluator_git_after'] = git('rev-parse', 'HEAD')
            if not receipt['source_unchanged'] or receipt['evaluator_git_after'] != head:
                receipt['outcome'] = 'stopped-evaluator-source-drift'
        except BaseException as error:
            receipt['outcome'] = 'stopped-source-verification-' + type(error).__name__
        receipt['python_direct_process_peak_rss_bytes'] = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
        receipt['python_rss_qualification'] = 'Darwin direct runner lifetime high-water value, excludes all child RSS; no hard heap guarantee.'
        write_new(result/'receipt.json', receipt)
    return 0 if receipt['outcome'] == 'finite-engineering-pass-only' else 2 if receipt['outcome'] == 'NO-GO' else 1


if __name__ == '__main__':
    try:
        sys.exit(main())
    except BaseException as error:
        if isinstance(error, SystemExit):
            raise
        print('t582 preflight stopped: ' + type(error).__name__ + ': ' + bounded_diagnostic(error), file=sys.stderr)
        sys.exit(1)
