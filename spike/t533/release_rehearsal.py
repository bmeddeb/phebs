"""T53.3 release gates on the real binary: operating envelope, authorization, signed admission.

`envelope` runs before signing, under the provisional switch. It checks that
an anonymous or wrong-token request is denied. It then drives sampled round-2
units at 1, 4 and 8 concurrent clients, recording latency, busy refusals
(the checker admits 4 Buf children at once) and the server process tree's peak
RSS.

`release` runs after signing, with no experimental switch. Only the signed
selection may admit the verdict. It checks the capability set, denial, a real
verdict over HTTP and MCP, and that revoking the release_id withdraws the
surface on restart.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import secrets
import sys
import threading
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[1] / 'pilot' / 'validation'))
import serve  # noqa: E402
import run_round  # noqa: E402

LEVELS = (1, 4, 8)
BREAKING = {'lineage': 'release_rehearsal',
            'before': [{'path': 'p/v1/m.proto', 'content': 'syntax = "proto3";\npackage p.v1;\nmessage M { int32 a = 1; }\n'}],
            'after': [{'path': 'p/v1/m.proto', 'content': 'syntax = "proto3";\npackage p.v1;\nmessage M { string a = 1; }\n'}]}


def start(binary: Path, run_root: Path, step: str, extra_config: str) -> tuple[serve.ServeSession, str, str]:
    port, key = run_round.free_port(), secrets.token_hex(24)
    config = run_root / f'{step}.yaml'
    config.write_text(f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{run_root / ("data-" + step)}"\n'
                      f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n{extra_config}connections: []\n')
    os.chmod(config, 0o600)
    helpers = binary.parent
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    session = serve.ServeSession(run_root / 'harness', step, [str(binary), 'serve', '-config', str(config)],
                                 f'http://127.0.0.1:{port}', env=env, ready_timeout_s=120)
    session.start()
    return session, f'http://127.0.0.1:{port}', key


def capabilities(base: str, key: str) -> list[str]:
    status, body = serve.http_get(base, '/api/version', token=key)
    if status != 200:
        raise serve.ServeError(f'/api/version answered {status}')
    return sorted(json.loads(body).get('capabilities', []))


def denial(base: str) -> dict:
    anonymous = run_round.post(base, '/api/check_wire_compatibility', BREAKING, '')[0]
    wrong = run_round.post(base, '/api/check_wire_compatibility', BREAKING, 'not-a-key')[0]
    if anonymous not in (401, 403) or wrong not in (401, 403):
        raise serve.ServeError(f'unauthenticated requests answered {anonymous}/{wrong}')
    return {'anonymous': anonymous, 'wrong_token': wrong}


def tree_rss_kib(root_pid: int) -> int:
    children: dict[int, list[int]] = {}
    rss: dict[int, int] = {}
    for entry in os.listdir('/proc'):
        if not entry.isdigit():
            continue
        try:
            fields = Path(f'/proc/{entry}/stat').read_text().rsplit(')', 1)[1].split()
            status = Path(f'/proc/{entry}/status').read_text()
        except OSError:
            continue
        children.setdefault(int(fields[1]), []).append(int(entry))
        rss[int(entry)] = next((int(line.split()[1]) for line in status.splitlines() if line.startswith('VmRSS:')), 0)
    total, pending = 0, [root_pid]
    while pending:
        pid = pending.pop()
        total += rss.get(pid, 0)
        pending += children.get(pid, [])
    return total


def sample(history: Path, population: Path) -> list[dict]:
    units = sorted(run_round.units(population), key=lambda u: u['unit_id'])
    chosen = units[::7]
    largest = sorted(units, key=lambda u: -sum(len(f['path']) for f in u['after']) - len(u['after']) * 1000)[:20]
    requests = []
    for unit in {u['unit_id']: u for u in chosen + largest}.values():
        before, after = run_round.contents(history, unit)
        requests.append({'lineage': run_round.LINEAGE,
                         'before': [{'path': p, 'content': c.decode()} for p, c in before],
                         'after': [{'path': p, 'content': c.decode()} for p, c in after]})
    return requests


def percentile(values: list[float], q: float) -> float:
    ordered = sorted(values)
    return round(ordered[min(len(ordered) - 1, int(q * len(ordered)))], 4)


def envelope(binary: Path, history: Path, population: Path, run_root: Path) -> dict:
    run_root.mkdir(parents=True)
    requests = sample(history, population)
    session, base, key = start(binary, run_root, 'envelope', 'experimental:\n  provisional_proto_extraction: true\n')
    result = {'units': len(requests), 'levels': {}}
    try:
        result['denial'] = denial(base)
        for level in LEVELS:
            peak, stop = [0], threading.Event()

            def watch() -> None:
                while not stop.is_set():
                    peak[0] = max(peak[0], tree_rss_kib(session.pid))
                    time.sleep(0.05)
            watcher = threading.Thread(target=watch)
            watcher.start()
            latencies, statuses = [], {}

            def one(request: dict) -> None:
                started = time.monotonic()
                status, _ = run_round.post(base, '/api/check_wire_compatibility', request, key)
                latencies.append(time.monotonic() - started)
                statuses[status] = statuses.get(status, 0) + 1
            started = time.monotonic()
            with concurrent.futures.ThreadPoolExecutor(level) as pool:
                list(pool.map(one, requests))
            elapsed = time.monotonic() - started
            stop.set()
            watcher.join()
            result['levels'][str(level)] = {
                'statuses': {str(k): v for k, v in sorted(statuses.items())}, 'seconds': round(elapsed, 2),
                'checks_per_second': round(len(requests) / elapsed, 2), 'p50_s': percentile(latencies, 0.50),
                'p95_s': percentile(latencies, 0.95), 'p99_s': percentile(latencies, 0.99),
                'max_s': round(max(latencies), 4), 'peak_tree_rss_mib': round(peak[0] / 1024, 1)}
    finally:
        result['stop'] = session.stop('graceful').get('outcome')
    return result


def release(binary: Path, selection: Path, artifacts: Path, key_id: str, public_key: str, release_id: str,
            run_root: Path) -> dict:
    run_root.mkdir(parents=True)
    trust = (f'release_selection:\n  path: "{selection}"\n  artifacts_path: "{artifacts}"\n'
             f'  keys:\n    - id: "{key_id}"\n      public_key: "{public_key}"\n')
    result = {}
    session, base, key = start(binary, run_root, 'released', trust)
    try:
        result['capabilities'] = capabilities(base, key)
        if 'wire-compatibility' not in result['capabilities'] or any(
                c.startswith('contract-') or c == 'kafka-topic-usage' for c in result['capabilities']):
            raise serve.ServeError(f"released claim admitted the wrong surface set: {result['capabilities']}")
        result['denial'] = denial(base)
        status, body = run_round.post(base, '/api/check_wire_compatibility', BREAKING, key)
        verdict = json.loads(body)
        mcp = serve.mcp_call(base, 'check_wire_compatibility', BREAKING, token=key)
        result['verdict'] = {'status': status, 'compatible': verdict.get('compatible'),
                             'rules': sorted({v['rule'] for v in verdict.get('violations', [])}),
                             'mcp_matches': mcp.get('structuredContent') == {k: v for k, v in verdict.items() if k != '$schema'}}
        if status != 200 or verdict.get('compatible') is not False or not result['verdict']['mcp_matches']:
            raise serve.ServeError(f"released verdict is wrong: {result['verdict']}")
    finally:
        result['released_stop'] = session.stop('graceful').get('outcome')
    session, base, key = start(binary, run_root, 'revoked', trust + f'  revoked:\n    - "{release_id}"\n')
    try:
        result['revoked_capabilities'] = capabilities(base, key)
        status, _ = run_round.post(base, '/api/check_wire_compatibility', BREAKING, key)
        result['revoked_status'] = status
        if 'wire-compatibility' in result['revoked_capabilities'] or status == 200:
            raise serve.ServeError('a revoked release still serves the verdict')
    finally:
        result['revoked_stop'] = session.stop('graceful').get('outcome')
    log = (run_root / 'harness' / 'scratch' / 'revoked' / serve.LOG_NAME)
    result['revocation_logged'] = log.exists() and 'not admitted' in log.read_text(errors='replace')
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest='command', required=True)
    e = sub.add_parser('envelope')
    e.add_argument('--binary', type=Path, required=True)
    e.add_argument('--history', type=Path, required=True)
    e.add_argument('--population', type=Path, default=HERE / 'population-r2')
    e.add_argument('--run-root', type=Path, required=True)
    r = sub.add_parser('release')
    for flag in ('--binary', '--selection', '--artifacts', '--run-root'):
        r.add_argument(flag, type=Path, required=True)
    for flag in ('--key-id', '--public-key', '--release-id'):
        r.add_argument(flag, required=True)
    args = parser.parse_args()
    if args.command == 'envelope':
        out = envelope(args.binary.resolve(), args.history, args.population, args.run_root.resolve())
    else:
        out = release(args.binary.resolve(), args.selection.resolve(), args.artifacts.resolve(), args.key_id,
                      args.public_key, args.release_id, args.run_root.resolve())
    print(json.dumps(out, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
