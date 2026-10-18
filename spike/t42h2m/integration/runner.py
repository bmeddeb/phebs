from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import traceback

ROOT = Path('/Users/ben/.codex/worktrees/t42h2m-linux-pressure/phebs.com')
sys.path.insert(0, str(ROOT / 'spike/t503'))
from requalification_browser import OwnedChild
import qualify

OUT = Path(tempfile.mkdtemp(prefix='phebs-h2m-integration-', dir='/tmp'))
os.chdir(ROOT)
os.environ['GOTOOLCHAIN'] = 'local'
os.environ['GOMAXPROCS'] = '2'
os.environ['GOOS'] = 'darwin'
os.environ['GOARCH'] = 'arm64'
GO = shutil.which('go')
LINT = '/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t574-fv0m6vmf/lint-bin/golangci-lint'
SELECTOR = r'^TestLinuxPressure'
result = {'checkpoint': 'fa05d0dcf3aa8d355ea74300d335e8a7e7428ab7', 'outcome': 'running',
          'root': str(OUT), 'cwd': str(ROOT), 'environment': {'GOTOOLCHAIN': 'local', 'GOMAXPROCS': '2', 'GOOS': 'darwin', 'GOARCH': 'arm64'},
          'steps': [], 'linux_native_execution': 'not_run', 'pressure_ceremony_execution': 'not_run'}

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def census():
    paths = sorted((ROOT / 'spike/t421').glob('*.go')) + [ROOT / 'go.mod', ROOT / 'go.sum']
    return [{'path': str(p.relative_to(ROOT)), 'bytes': p.stat().st_size, 'sha256': sha(p)} for p in paths]

def save():
    pending = OUT / 'summary.pending'
    pending.write_text(json.dumps(result, indent=2) + '\n')
    pending.replace(OUT / 'summary.json')

started = time.monotonic()
result['source_before'] = census()
result['driver'] = {'path': str(Path(__file__)), 'sha256': sha(__file__)}
result['ownership_helper'] = {'path': str(ROOT / 'spike/t503/requalification_browser.py'), 'sha256': sha(ROOT / 'spike/t503/requalification_browser.py')}
result['scope'] = 'focused portable Darwin models; Linux tagged static analysis and compile only; no Linux binary execution'
result['tools'] = {name: {'path': str(Path(path).resolve()), 'sha256': sha(path)} for name, path in [('go', GO), ('golangci-lint', LINT)]}
save()
print(json.dumps({'root': str(OUT), 'checkpoint': result['checkpoint']}), flush=True)

def step(name, argv, cap=120, environment=None, artifact=None, check=None):
    original = {key: os.environ.get(key) for key in (environment or {})}
    os.environ.update(environment or {})
    record = {'name': name, 'argv': [str(v) for v in argv], 'outer_seconds_cap': cap,
              'environment': environment or {}, 'started_unix': time.time()}
    result['steps'].append(record)
    save()
    print(json.dumps({'starting': name}), flush=True)
    owned, failure = None, None
    clock = time.monotonic()
    try:
        owned = OwnedChild(record['argv'], OUT, name)
        record['pid'] = owned.child.pid
        record['expected_pgid'] = owned.child.pid
        record['observed_pgid'] = os.getpgid(owned.child.pid)
        owned.child.wait(timeout=cap)
        if owned.child.returncode != 0:
            raise RuntimeError(f'{name} exited {owned.child.returncode}')
    except BaseException as error:
        failure = error
        record['error'] = str(error)
        record['traceback'] = traceback.format_exc()
    finally:
        if owned:
            try:
                record['terminal'] = dict(owned.stop(), joined=True)
            except BaseException as error:
                failure = failure or error
                record['cleanup_error'] = str(error)
                record['cleanup_traceback'] = traceback.format_exc()
                try:
                    record['terminal'] = {'exit_code': owned.child.wait(timeout=3), 'joined': True,
                                          'teardown': qualify.serve.receipts._drain_group(owned.child.pid, False)}
                except BaseException:
                    record['followup_wait_traceback'] = traceback.format_exc()
                owned.log.close()
        for key, value in original.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
        record['seconds_including_join'] = round(time.monotonic() - clock, 6)
        record['log_sha256'] = sha(OUT / (name + '.log'))
    if not failure and check:
        try:
            check((OUT / (name + '.log')).read_text())
        except BaseException as error:
            failure = error
            record['admission_error'] = str(error)
    if not failure and artifact:
        artifact = Path(artifact)
        record['artifact'] = {'path': str(artifact), 'bytes': artifact.stat().st_size, 'sha256': sha(artifact),
                              'executed': False, 'removed_after_hash': True}
        artifact.unlink()
    record['outcome'] = 'stopped' if failure else 'passed'
    save()
    print(json.dumps({'step': name, 'outcome': record['outcome'], 'seconds': record['seconds_including_join'],
                      'terminal': record.get('terminal'), 'error': str(failure) if failure else None}), flush=True)
    if failure:
        raise failure

def go_version(raw):
    expected = 'go version go' + (ROOT / '.go-version').read_text().strip() + ' darwin/arm64'
    if raw.strip() != expected:
        raise RuntimeError('Go version does not match repository pin: ' + raw.strip())

def lint_version(raw):
    if 'version 2.12.2 ' not in raw:
        raise RuntimeError('golangci-lint version does not match 2.12.2: ' + raw.strip())

failure = None
try:
    first_path = Path('/tmp/phebs-h2m-checks-_xr8oog3/summary.json')
    first = json.loads(first_path.read_text())
    for name in ['go', 'golangci-lint']:
        if result['tools'][name] != first['tools'][name]:
            raise RuntimeError('previously admitted tool image changed: ' + name)
    result['previous_first_failure'] = {'path': str(first_path), 'sha256': sha(first_path), 'outcome': 'stopped'}
    result['integration_scope'] = 'reviewed namespace and pressure owners combined; eight pressure-owner sources unchanged; no passed tests/vet/lint/arm64 compile repeated'
    completed_path = Path('/tmp/phebs-h2m-completion-_zxh5_89/summary.json')
    result['previous_completion'] = {'path': str(completed_path), 'sha256': sha(completed_path), 'outcome': 'passed'}
    target = OUT / 'compile-linux-amd64-tagged-spike-t421.test'
    step('compile-linux-amd64-tagged-spike-t421', [GO, 'test', '-c', '-tags=phebs_t42_native', '-o', target, './spike/t421'], environment={'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '0'}, artifact=target)
    step('docs-check', ['make', 'docs-check'])
    step('verify-glossary', ['make', 'verify-glossary'])
except BaseException as error:
    failure = error
    result['error'] = str(error)
    result['traceback'] = traceback.format_exc()
finally:
    result['source_after'] = census()
    result['source_unchanged'] = result['source_before'] == result['source_after']
    if not result['source_unchanged']:
        failure = failure or RuntimeError('source changed during checks')
    result['all_owned_groups_joined_cleanly'] = all(s.get('terminal', {}).get('joined') and
        s.get('terminal', {}).get('teardown') == 'clean' and not s.get('cleanup_error') and
        s.get('terminal', {}).get('termination_mode') != 'sigkill' for s in result['steps'])
    if not result['all_owned_groups_joined_cleanly']:
        failure = failure or RuntimeError('an owned group lacked clean joined teardown')
    result['outcome'] = 'stopped' if failure else 'passed'
    result['seconds_including_all_joins'] = round(time.monotonic() - started, 6)
    save()
    print(json.dumps({'outcome': result['outcome'], 'summary': str(OUT / 'summary.json'),
                      'seconds': result['seconds_including_all_joins'], 'joined': result['all_owned_groups_joined_cleanly'],
                      'error': str(failure) if failure else None}), flush=True)
sys.exit(1 if failure else 0)
