"""Real-binary rehearsal of the T48.3 serve drivers.

``lifecycle`` boots a fresh one-repository instance of the exact binary and
runs four receipted phases: cold start, warm restart, hard death (group
SIGKILL), and recovery after it. Every phase must answer the same
authenticated repository listing. ``callers`` is read-only against a live
server. It pages every given Caller Map endpoint over HTTP and MCP and
requires exact parity once request-bound tokens are removed. It also requires
an unauthenticated read of a real repository to be indistinguishable from one
of an absent repository. Test credentials are generated locally and never
printed.
"""
from __future__ import annotations

import argparse
import json
import os
import secrets
import socket
import subprocess
import time
from pathlib import Path

import serve

VOLATILE = frozenset({'$schema', 'citation', 'next_cursor'})


def free_port() -> int:
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def fixture_repository(root: Path) -> Path:
    repo = root / 'fixture'
    repo.mkdir(parents=True)
    (repo / 'main.go').write_text('package main\n\nfunc main() { println("t48.3") }\n')
    env = {**os.environ, 'GIT_AUTHOR_NAME': 't48.3', 'GIT_AUTHOR_EMAIL': 't48.3@localhost',
           'GIT_COMMITTER_NAME': 't48.3', 'GIT_COMMITTER_EMAIL': 't48.3@localhost',
           'GIT_AUTHOR_DATE': '2026-10-12T00:00:00Z', 'GIT_COMMITTER_DATE': '2026-10-12T00:00:00Z'}
    for argv in (['git', 'init', '-q', '-b', 'main'], ['git', 'add', 'main.go'], ['git', 'commit', '-q', '-m', 'fixture']):
        subprocess.run(argv, cwd=repo, env=env, check=True)
    return repo


def listing(base: str, key: str) -> list:
    status, body = serve.http_get(base, '/api/repos', token=key)
    if status != 200:
        raise serve.ServeError(f'repository listing returned {status}')
    value = json.loads(body)
    rows = value if isinstance(value, list) else value.get('repos', value.get('items', value))
    return serve.strip_volatile(rows, VOLATILE)


def lifecycle(binary: Path, zoekt: Path, run_root: Path, index_deadline_s: float = 180) -> dict:
    run_root.mkdir(parents=True)
    repo = fixture_repository(run_root)
    port, key = free_port(), secrets.token_hex(24)
    config = run_root / 'phebs.yaml'
    config.write_text(f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{run_root / "data"}"\n'
                      f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n'
                      f'sync:\n  poll_interval: 15s\n  resync_interval: 1h\n'
                      f'connections:\n  - name: fixture\n    type: git\n    url: "{repo}"\n')
    os.chmod(config, 0o600)
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(zoekt)}
    base = f'http://127.0.0.1:{port}'
    harness = run_root / 'harness'

    def phase(step: str, stop: str, wait_indexed: bool = False):
        session = serve.ServeSession(harness, step, [str(binary), 'serve', '-config', str(config)], base,
                                     env=env, ready_timeout_s=120, stop_timeout_s=30)
        session.start()
        try:
            deadline = time.monotonic() + index_deadline_s
            while True:
                rows = listing(base, key)
                indexed = bool(rows) and all(r.get('indexed_commit_hash') for r in rows)
                if indexed or not wait_indexed:
                    break
                if time.monotonic() > deadline:
                    raise serve.ServeError('fixture repository was not indexed before its deadline')
                time.sleep(1)
            unauthenticated = serve.http_get(base, '/api/repos')[0]
        except BaseException:
            session.stop('hard_kill', reason='phase probe failed')
            raise
        return rows, unauthenticated, session.stop(stop)

    results = {}
    for step, stop, wait in (('cold-start', 'graceful', True), ('warm-restart', 'hard_kill', False),
                             ('restart-after-hard-death', 'graceful', False)):
        rows, unauthenticated, receipt = phase(step, stop, wait)
        results[step] = {'listing': rows, 'unauthenticated_status': unauthenticated, 'receipt': receipt}
    first = results['cold-start']['listing']
    for step, value in results.items():
        if value['listing'] != first:
            raise serve.ServeError(f'{step} answered a different repository listing')
        if value['unauthenticated_status'] != 401 or value['receipt']['outcome'] != 'success':
            raise serve.ServeError(f'{step} did not refuse anonymous reads or did not end cleanly')
    return {'schema': 't48.3-lifecycle-rehearsal-v1', 'binary_sha256': serve.receipts.sha256_file(binary),
            'phases': {s: {k: v['receipt'][k] for k in ('outcome', 'stop_mode', 'ready_seconds', 'exit_code', 'signal',
                                                        'wall_seconds', 'max_rss_kib', 'teardown')}
                       for s, v in results.items()},
            'listing_identical_across_phases': True, 'anonymous_refused_every_phase': True}


def callers(base: str, key: str, endpoints: list[dict], absent_repository: str) -> dict:
    pages = 0
    for endpoint in endpoints:
        params = {'protocol': 'protobuf', 'repository': endpoint['repository'], 'lineage': endpoint['lineage'],
                  'operation': endpoint['operation'], 'resolution': 'any', 'page_size': 100}
        status, body = serve.http_get(base, '/api/contract_callers', params, token=key)
        if status != 200:
            raise serve.ServeError(f'HTTP Caller Map returned {status}')
        result = serve.mcp_call(base, 'list_operation_callers',
                                {**{k: v for k, v in params.items() if k != 'lineage'},
                                 'declaration_lineage': endpoint['lineage']}, token=key)
        serve.parity(body, result, VOLATILE)
        pages += 1
    first = endpoints[0]
    hidden = serve.http_get(base, '/api/contract_callers', {'protocol': 'protobuf', 'repository': first['repository'],
                                                           'lineage': first['lineage'], 'operation': first['operation']})
    absent = serve.http_get(base, '/api/contract_callers', {'protocol': 'protobuf', 'repository': absent_repository,
                                                           'lineage': first['lineage'], 'operation': first['operation']})
    serve.undisclosed(hidden, absent, first['repository'], absent_repository)
    return {'schema': 't48.3-caller-parity-rehearsal-v1', 'endpoints': len(endpoints), 'first_pages_compared': pages,
            'volatile_keys_ignored': sorted(VOLATILE), 'anonymous_hidden_equals_absent_status': hidden[0]}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest='command', required=True)
    life = sub.add_parser('lifecycle')
    life.add_argument('--binary', type=Path, required=True)
    life.add_argument('--zoekt', type=Path, required=True)
    life.add_argument('--run-root', type=Path, required=True)
    call = sub.add_parser('callers')
    call.add_argument('--base', required=True)
    call.add_argument('--key-file', type=Path, required=True, help='operator config holding auth.api_key')
    call.add_argument('--endpoints', type=Path, required=True)
    call.add_argument('--absent-repository', required=True)
    args = parser.parse_args()
    if args.command == 'lifecycle':
        result = lifecycle(args.binary.resolve(), args.zoekt.resolve(), args.run_root.resolve())
    else:
        import re
        key = re.search(r'^\s*api_key:\s*"?([^"\s]+)"?\s*$', args.key_file.read_text(), re.M).group(1)
        result = callers(args.base, key, json.loads(args.endpoints.read_text()), args.absent_repository)
    print(json.dumps(result, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
