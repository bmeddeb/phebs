"""T47.4 groundwork: exploratory current-host Caller Map operating rehearsal.

Uses the T48.3 drivers on the real ordinary binary against the T47.2 workload
(the three frozen derived repositories). The phases are:

1. cold: a fresh store converges to current caller generations;
2. warm restart, ended by a planned hard death;
3. restart after that hard death;
4. a second fresh store hard-killed mid-generation, then recovered.

The rehearsal records time to current, sampled whole-session RSS, data
growth, Caller Map read latency over every declared endpoint, and generation
digests, which must be identical across every phase and both stores. It is
exploratory: T47.4 must freeze its operating class and thresholds before an
acceptance run, and nothing here is one.
"""
from __future__ import annotations

import argparse
import json
import os
import secrets
import statistics
import subprocess
import sys
import threading
import time
from pathlib import Path

import serve
import serve_rehearsal

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'spike' / 't472'))
import caller_map_export  # noqa: E402

PAGE_SIZE = os.sysconf('SC_PAGE_SIZE')


class SessionRSS:
    """Peak and mean summed RSS of every process in one session, sampled from /proc."""

    def __init__(self, session_id: int, interval: float = 0.5):
        self.session_id, self.interval = session_id, interval
        self.samples: list[int] = []
        self.peak_processes = 0
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._run, daemon=True)

    def _sample(self) -> tuple[int, int]:
        total = count = 0
        for entry in os.scandir('/proc'):
            if not entry.name.isdigit():
                continue
            try:
                stat = Path(entry.path, 'stat').read_text()
                fields = stat[stat.rindex(')') + 2:].split()
                if int(fields[3]) != self.session_id:  # field 6: session id
                    continue
                total += int(Path(entry.path, 'statm').read_text().split()[1]) * PAGE_SIZE
                count += 1
            except (OSError, ValueError, IndexError):
                continue
        return total, count

    def _run(self):
        while not self._stop.is_set():
            total, count = self._sample()
            if count:
                self.samples.append(total)
                self.peak_processes = max(self.peak_processes, count)
            self._stop.wait(self.interval)

    def __enter__(self):
        self._thread.start()
        return self

    def __exit__(self, *exc):
        self._stop.set()
        self._thread.join()

    def summary(self) -> dict:
        if not self.samples:
            return {'samples': 0}
        return {'samples': len(self.samples), 'peak_bytes': max(self.samples),
                'mean_bytes': int(statistics.fmean(self.samples)), 'peak_processes': self.peak_processes}


def directory_bytes(path: Path) -> int:
    return sum(f.stat().st_size for f in path.rglob('*') if f.is_file() and not f.is_symlink())


def percentiles(values: list[float]) -> dict:
    ordered = sorted(values)
    pick = lambda q: ordered[min(len(ordered) - 1, int(q * len(ordered)))]  # noqa: E731
    return {'n': len(ordered), 'p50_ms': round(pick(0.5) * 1000, 2), 'p95_ms': round(pick(0.95) * 1000, 2),
            'p99_ms': round(pick(0.99) * 1000, 2), 'max_ms': round(ordered[-1] * 1000, 2)}


def write_config(run_root: Path, data: Path, corpus: Path, port: int, key: str, repositories: list[str],
                 watch: bool = False) -> Path:
    config = run_root / f'phebs-{data.name}.yaml'
    # Local connections only re-sync on HEAD moves when watched (the cadence covers remotes).
    connections = ''.join(f'  - name: {name}\n    type: git\n    url: "{corpus / name}"\n'
                          + ('    watch: true\n' if watch else '') for name in repositories)
    config.write_text(f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{data}"\n'
                      f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n  session_lifetime: 12h\n'
                      f'sync:\n  cleanup_orphans: false\n  poll_interval: 15s\n  resync_interval: 1h\n'
                      f'proof_bundles:\n  retention: 168h\n'
                      f'experimental:\n  provisional_proto_extraction: true\n  typed_scope_reservations: true\n'
                      f'connections:\n{connections}')
    os.chmod(config, 0o600)
    return config


def wait_current(api: caller_map_export.Api, repositories: list[str], deadline_s: float) -> tuple[float, list[dict]]:
    started = time.monotonic()
    while True:
        try:
            return time.monotonic() - started, caller_map_export.generation_controls(api, repositories)
        except (ValueError, OSError, KeyError):
            pass
        if time.monotonic() - started > deadline_s:
            raise serve.ServeError('caller generations were not current before the deadline')
        time.sleep(2)


def read_latency(api: caller_map_export.Api, data: Path, controls: list[dict], passes: int) -> tuple[dict, int]:
    endpoints = []
    for control in controls:
        manifest, _ = caller_map_export.publication_facts(data, control)
        endpoints += [(control, e) for e in caller_map_export.declared_endpoints(
            data, control, manifest['generation']['resolver_generation_digest'])]
    timings = []
    for _ in range(passes):
        for control, (lineage, operation) in endpoints:
            started = time.monotonic()
            caller_map_export.export_rows(api, control, [(lineage, operation)])
            timings.append(time.monotonic() - started)
    return percentiles(timings), len(endpoints)


def identities(controls: list[dict]) -> list[tuple]:
    """Store-local identity: a generation embeds its store's random run ids."""
    return sorted((c['repository'], c['generation_digest'], c['manifest_digest']) for c in controls)


def content_identity(data: Path, controls: list[dict]) -> list:
    """Run-id-free identity comparable across fresh stores: upstream plans/roots plus exact leaf bytes."""
    out = []
    for control in controls:
        manifest, _ = caller_map_export.publication_facts(data, control)
        upstream = [{k: v for k, v in d.items() if k != 'run_id'} for d in manifest['generation']['upstream']['domains']]
        out.append([control['repository'], upstream,
                    [[p['pair']['leaf']['content_digest'], p['receipt']['content_digest']] for p in manifest['pairs']],
                    manifest['aggregate']['result_count'], manifest['aggregate']['abstention_count']])
    return sorted(out, key=lambda row: row[0])


def rehearse(binary: Path, helpers: Path, corpus: Path, run_root: Path, repositories: list[str],
             deadline_s: float = 1800, passes: int = 3) -> dict:
    run_root.mkdir(parents=True)
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    names = ['local' + str(corpus / name) for name in repositories]
    key, harness = secrets.token_hex(24), run_root / 'harness'
    result: dict = {'schema': 't47.4-operating-groundwork-v1', 'exploratory': True,
                    'binary_sha256': serve.receipts.sha256_file(binary), 'host': serve.receipts.host_facts(),
                    'repositories': names, 'phases': {}}

    def phase(step: str, data: Path, stop: str, *, measure_cold=False, kill_after: float | None = None,
              latency=False) -> list[dict] | None:
        port = serve_rehearsal.free_port()
        config = write_config(run_root, data, corpus, port, key, repositories)
        base = f'http://127.0.0.1:{port}'
        api = caller_map_export.Api(base, key)
        session = serve.ServeSession(harness, step, [str(binary), 'serve', '-config', str(config)], base,
                                     env=env, ready_timeout_s=300, stop_timeout_s=60)
        ready = session.start()
        record: dict = {'ready_seconds': ready}
        controls = None
        with SessionRSS(session.pid) as rss:
            try:
                if kill_after is not None:
                    time.sleep(kill_after)
                else:
                    record['seconds_to_current'], controls = wait_current(api, names, deadline_s)
                    if latency:
                        record['caller_map_latency'], record['endpoints'] = read_latency(api, data, controls, passes)
            except BaseException:
                session.stop('hard_kill', reason=f'{step} probe failed')
                raise
            receipt = session.stop(stop)
        record.update(rss=rss.summary(), data_bytes=directory_bytes(data),
                      receipt={k: receipt[k] for k in ('outcome', 'stop_mode', 'exit_code', 'signal', 'teardown',
                                                       'wall_seconds', 'max_rss_kib')})
        if measure_cold:
            record['cold'] = True
        result['phases'][step] = record
        return controls

    first = run_root / 'data-a'
    cold = phase('cold', first, 'graceful', measure_cold=True, latency=True)
    expected, expected_content = identities(cold), content_identity(first, cold)
    for step, stop in (('warm-restart', 'hard_kill'), ('restart-after-hard-death', 'graceful')):
        if identities(phase(step, first, stop)) != expected:
            raise serve.ServeError(f'{step} changed the caller generation')
    second = run_root / 'data-b'
    phase('interrupted-cold', second, 'hard_kill',
          kill_after=max(5.0, result['phases']['cold']['seconds_to_current'] / 2))
    recovered = phase('recovered-after-interruption', second, 'graceful')
    if content_identity(second, recovered) != expected_content:
        raise serve.ServeError('an interrupted fresh store converged to different caller content')
    result['generation_identities'] = [list(i) for i in expected]
    result['store_local_identity_stable_across_restarts'] = True
    result['content_identical_across_stores'] = True
    result['cross_store_difference'] = 'generation identities differ only by per-store upstream run ids'
    return result


class PagingReader:
    """Continuously pages every endpoint with small pages; counts complete, conflicted and broken traversals."""

    def __init__(self, base: str, key: str, endpoints: list[tuple[str, str, str]], page_size: int = 5):
        self.base, self.key, self.endpoints, self.page_size = base, key, endpoints, page_size
        self.complete = self.conflicts = 0
        self.errors: list[str] = []
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._run, daemon=True)

    def traverse(self, repository: str, lineage: str, operation: str) -> str:
        params = {'protocol': 'protobuf', 'repository': repository, 'lineage': lineage, 'operation': operation,
                  'resolution': 'any', 'page_size': self.page_size}
        generation = None
        while True:
            status, body = serve.http_get(self.base, '/api/contract_callers', params, token=self.key)
            if status == 409:
                return 'conflict'
            if status != 200:
                return f'status {status}'
            page = json.loads(body)
            digest = (page.get('generation') or {}).get('generation_digest')
            if generation is None:
                generation = digest
            elif digest != generation:
                return 'mixed generations within one traversal'
            if page['pagination']['complete']:
                return 'complete'
            params['cursor'] = page['pagination']['next_cursor']

    def _run(self):
        while not self._stop.is_set():
            for repository, lineage, operation in self.endpoints:
                if self._stop.is_set():
                    return
                try:
                    outcome = self.traverse(repository, lineage, operation)
                except (OSError, ValueError) as error:
                    outcome = f'exception {type(error).__name__}'
                if outcome == 'complete':
                    self.complete += 1
                elif outcome == 'conflict':
                    self.conflicts += 1
                elif outcome != 'status 404':  # an endpoint absent from a moved generation is honest
                    self.errors.append(outcome)

    def __enter__(self):
        self._thread.start()
        return self

    def __exit__(self, *exc):
        self._stop.set()
        self._thread.join()


def clone_corpus(source: Path, target: Path, repositories: list[str]) -> None:
    target.mkdir(parents=True)
    for name in repositories:
        subprocess.run(['git', 'clone', '-q', '--no-hardlinks', '-b', 'derived', str(source / name), str(target / name)],
                       check=True, env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'})


def commit_update(repo: Path) -> str:
    env = {**os.environ, 'GIT_AUTHOR_NAME': 't47.4', 'GIT_AUTHOR_EMAIL': 't47.4@localhost',
           'GIT_COMMITTER_NAME': 't47.4', 'GIT_COMMITTER_EMAIL': 't47.4@localhost'}
    (repo / 'T47_4_UPDATE.md').write_text('incremental source update (no Go or .proto change)\n')
    subprocess.run(['git', 'add', 'T47_4_UPDATE.md'], cwd=repo, check=True, env=env)
    subprocess.run(['git', 'commit', '-q', '-m', 't47.4 incremental update'], cwd=repo, check=True, env=env)
    return subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=repo, check=True, capture_output=True, text=True).stdout.strip()


def lifecycle_extended(binary: Path, helpers: Path, source_corpus: Path, run_root: Path, repositories: list[str],
                       updated: str = 'grpc-go', deadline_s: float = 900) -> dict:
    """Incremental update under concurrent paging, then online backup and restore."""
    run_root.mkdir(parents=True)
    corpus = run_root / 'corpus'
    clone_corpus(source_corpus, corpus, repositories)
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    names = ['local' + str(corpus / name) for name in repositories]
    key, harness, data = secrets.token_hex(24), run_root / 'harness', run_root / 'data'
    port = serve_rehearsal.free_port()
    config = write_config(run_root, data, corpus, port, key, repositories, watch=True)
    base = f'http://127.0.0.1:{port}'
    api = caller_map_export.Api(base, key)
    result: dict = {'schema': 't47.4-operating-lifecycle-v1', 'exploratory': True,
                    'binary_sha256': serve.receipts.sha256_file(binary), 'phases': {}}
    session = serve.ServeSession(harness, 'update-under-paging', [str(binary), 'serve', '-config', str(config)], base,
                                 env=env, ready_timeout_s=300, stop_timeout_s=60)
    session.start()
    try:
        cold_seconds, before = wait_current(api, names, deadline_s)
        endpoints = []
        for control in before:
            manifest, _ = caller_map_export.publication_facts(data, control)
            endpoints += [(control['repository'], lineage, op) for lineage, op in caller_map_export.declared_endpoints(
                data, control, manifest['generation']['resolver_generation_digest'])]
        by_repo = {c['repository']: c for c in before}
        target = 'local' + str(corpus / updated)
        with PagingReader(base, key, endpoints) as reader:
            time.sleep(10)
            new_head = commit_update(corpus / updated)
            started = time.monotonic()
            while True:
                try:
                    after = caller_map_export.generation_controls(api, names)
                    moved = {c['repository']: c for c in after}
                    if moved[target]['commit'] == new_head:
                        break
                except (ValueError, OSError, KeyError):
                    pass
                if time.monotonic() - started > deadline_s:
                    raise serve.ServeError('the incremental update did not converge before the deadline')
                time.sleep(2)
            update_seconds = time.monotonic() - started
            time.sleep(10)
        if reader.errors or reader.complete == 0:
            raise serve.ServeError(f'paging during publication broke: {reader.errors[:5]} complete={reader.complete}')
        for repository, control in by_repo.items():
            now = moved[repository]
            if repository == target:
                if (now['result_count'], now['abstention_count']) != (control['result_count'], control['abstention_count']):
                    raise serve.ServeError('a non-source update changed caller counts')
            elif now['generation_digest'] != control['generation_digest']:
                raise serve.ServeError('an update to one repository republished another')
        result['phases']['update-under-paging'] = {
            'cold_seconds_to_current': round(cold_seconds, 3), 'update_seconds_to_current': round(update_seconds, 3),
            'paging': {'complete_traversals': reader.complete, 'conflicts_409': reader.conflicts, 'errors': 0},
            'updated_repository_counts_unchanged': True, 'other_repositories_unchanged': True}
        backup = run_root / 'backup'
        completed = subprocess.run([str(binary), 'backup', '-config', str(config), '-output', str(backup)],
                                   env=env, capture_output=True, text=True, timeout=1800)
        if completed.returncode != 0:
            raise serve.ServeError(f'online backup failed: {completed.stderr[-400:]}')
    except BaseException:
        session.stop('hard_kill', reason='update or backup probe failed')
        raise
    receipt = session.stop('graceful')
    result['phases']['update-under-paging']['receipt'] = {k: receipt[k] for k in ('outcome', 'teardown', 'wall_seconds')}
    os.rename(data, run_root / 'data-before-restore')
    started = time.monotonic()
    completed = subprocess.run([str(binary), 'restore', '-config', str(config), '-backup', str(backup)],
                               env=env, capture_output=True, text=True, timeout=1800)
    if completed.returncode != 0:
        raise serve.ServeError(f'restore failed: {completed.stderr[-400:]}')
    restore_seconds = time.monotonic() - started
    restored = serve.ServeSession(harness, 'after-restore', [str(binary), 'serve', '-config', str(config)], base,
                                  env=env, ready_timeout_s=300, stop_timeout_s=60)
    ready = restored.start()
    try:
        current_seconds, after_restore = wait_current(api, names, deadline_s)
        # Restore re-fences derived publications and rebuilds them (new upstream run ids); content must match.
        if content_identity(data, after_restore) != content_identity(run_root / 'data-before-restore', after):
            raise serve.ServeError('restore did not reproduce the exact caller content')
    except BaseException:
        restored.stop('hard_kill', reason='restore probe failed')
        raise
    receipt = restored.stop('graceful')
    result['phases']['backup-restore'] = {
        'backup_bytes': directory_bytes(backup), 'restore_seconds': round(restore_seconds, 3),
        'ready_seconds': ready, 'seconds_to_current': round(current_seconds, 3), 'identical_caller_content': True,
        'derived_rebuilt_after_restore': identities(after_restore) != identities(after),
        'receipt': {k: receipt[k] for k in ('outcome', 'teardown', 'wall_seconds')}}
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--helpers', type=Path, required=True)
    parser.add_argument('--corpus', type=Path, required=True)
    parser.add_argument('--run-root', type=Path, required=True)
    parser.add_argument('--repositories', nargs='+', default=['etcd', 'grpc-go', 'containerd'])
    parser.add_argument('--extended', action='store_true', help='incremental update, paging, backup/restore')
    args = parser.parse_args()
    if args.extended:
        result = lifecycle_extended(args.binary.resolve(), args.helpers.resolve(), args.corpus.resolve(),
                                    args.run_root.resolve(), args.repositories)
        (args.run_root / 'operating-lifecycle.json').write_text(json.dumps(result, indent=1, sort_keys=True) + '\n')
        print(json.dumps(result, indent=1, sort_keys=True))
        return
    result = rehearse(args.binary.resolve(), args.helpers.resolve(), args.corpus.resolve(), args.run_root.resolve(),
                      args.repositories)
    (args.run_root / 'operating-groundwork.json').write_text(json.dumps(result, indent=1, sort_keys=True) + '\n')
    print(json.dumps(result, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
