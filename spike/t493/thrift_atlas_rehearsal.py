"""T49.3 release gates for phebs.thrift.contract on the real binary.

The real `phebs serve` runs with no experimental switch; only the signed
selection admits thrift-contract. The repositories are the T49.2 round-3
population at their locked commits, served as local git connections. The
expected Atlas is spike/t493/expected.json: every service and operation the
sealed reference-compiler oracle declares in a file thriftdecl 1.1.2 does not
gap.

Every phase (cold, restart, hard death, backup->restore) pages the whole Atlas
and requires it to equal the expected set exactly. The cold phase also checks
capabilities, anonymous denial, an OIDC member limited to one repository, and
one operation detail per repository: request/response shapes present,
coverage naming thrift-contract alone, and every relationship section named
unavailable. `--final` runs the signed records with each named release revoked
in turn.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[1] / 'pilot' / 'validation'))
import serve  # noqa: E402
import caller_operating  # noqa: E402
import serve_rehearsal  # noqa: E402

RELATIONSHIPS = ['implementations', 'callers', 'unresolved_candidates']
MCP = False  # --mcp: the release declares the Atlas MCP tools (T49.4), so MCP must equal HTTP


def connection(name: str) -> str:
    return re.sub(r'[^a-z0-9-]+', '-', name.lower())


def prepare(history: Path, lock: dict, work: Path) -> dict[str, Path]:
    paths = {}
    for repo in lock['repos']:
        target = work / repo['dir']
        subprocess.run(['git', 'clone', '-q', '--no-checkout', str(history / repo['dir']), str(target)], check=True)
        subprocess.run(['git', '-C', str(target), 'checkout', '-q', '-B', 'main', repo['commit']], check=True)
        paths[repo['dir']] = target
    return paths


def atlas(base: str, token: str) -> list[dict]:
    items, cursor = [], ''
    while True:
        params = {'page_size': '100', **({'cursor': cursor} if cursor else {})}
        status, body = serve.http_get(base, '/api/contract_atlas', params, token=token)
        if status != 200:
            raise serve.ServeError(f'contract_atlas answered {status}: {body[:300]!r}')
        page = json.loads(body)
        if page['coverage'].get('domains') != ['thrift-contract']:
            raise serve.ServeError(f"Atlas coverage describes {page['coverage'].get('domains')}, not thrift-contract alone")
        items.extend(page['items'])
        cursor = page['pagination'].get('next_cursor', '')
        if not cursor:
            return items


def served(items: list[dict], names: dict[str, str]) -> dict:
    by_dir = {name: d for d, name in names.items()}
    out: dict = {}
    for item in items:
        if item['protocol'] != 'thrift':
            raise serve.ServeError(f"Atlas served a {item['protocol']} item under a Thrift-only release")
        entry = out.setdefault(by_dir[item['repository']], {'services': [], 'operations': []})
        if item['kind'] == 'service':
            entry['services'].append(item['service_fqn'])
        else:
            entry['operations'].append(item['operation'])
    return {d: {k: sorted(v) for k, v in e.items()} for d, e in sorted(out.items())}


def check_atlas(base: str, token: str, want: dict, names: dict) -> list[dict]:
    items = atlas(base, token)
    got = served(items, names)
    if got != want:
        missing = {d: e for d, e in want.items() if got.get(d) != e}
        raise serve.ServeError(f'Atlas differs from the oracle in {sorted(missing)[:5]}: got {[got.get(d) for d in sorted(missing)[:2]]}')
    return items


def wait_atlas(base: str, token: str, want: dict, names: dict, deadline_s: float) -> float:
    started, last = time.monotonic(), None
    while True:
        try:
            check_atlas(base, token, want, names)
            return time.monotonic() - started
        except (serve.ServeError, OSError, KeyError) as error:
            last = error
        if time.monotonic() - started > deadline_s:
            raise serve.ServeError(f'Atlas never equalled the oracle before the deadline: {last}')
        time.sleep(3)


def env_for(binary: Path) -> dict:
    helpers = binary.parent
    return {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
            'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}


def config_text(port: int, data: Path, key: str, selection: Path, artifacts: Path, key_id: str, public_key: str,
                paths: dict, extra: str = '', revoked: list[str] | None = None) -> str:
    connections = ''.join(f'  - name: {connection(d)}\n    type: git\n    url: "{p}"\n' for d, p in paths.items())
    revoke = ('  revoked:\n' + ''.join(f'    - "{i}"\n' for i in revoked)) if revoked else ''
    return (f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{data}"\n'
            f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n{extra}'
            f'release_selection:\n  path: "{selection}"\n  artifacts_path: "{artifacts}"\n'
            f'  keys:\n    - id: "{key_id}"\n      public_key: "{public_key}"\n{revoke}'
            f'sync:\n  poll_interval: 15s\n  resync_interval: 1h\nconnections:\n{connections}')


def rehearse(binary: Path, history: Path, lock_path: Path, expected: Path, selection: Path, artifacts: Path,
             key_id: str, public_key: str, member_dir: str, run_root: Path, deadline_s: float = 900) -> dict:
    run_root.mkdir(parents=True)
    paths = prepare(history, json.loads(lock_path.read_text()), run_root / 'corpus')
    want = json.loads(expected.read_text())['repositories']
    names = {d: 'local' + str(p) for d, p in paths.items()}
    port, issuer_port, key = serve_rehearsal.free_port(), serve_rehearsal.free_port(), secrets.token_hex(24)
    secret_file = run_root / 'oidc-client-secret'
    secret_file.write_text(secrets.token_hex(24))
    os.chmod(secret_file, 0o600)
    member = 'member@t493.test'
    oidc = (f'  oidc:\n    issuer_url: "http://127.0.0.1:{issuer_port}"\n    client_id: "phebs-t493"\n'
            f'    client_secret: "{secret_file.read_text()}"\n'
            f'    redirect_url: "http://127.0.0.1:{port}/api/auth/oidc/callback"\n'
            f'permissions:\n  users:\n    {member}: []\n  always_visible: ["{names[member_dir]}"]\n')
    data = run_root / 'data'
    cfg = run_root / 'phebs.yaml'
    cfg.write_text(config_text(port, data, key, selection, artifacts, key_id, public_key, paths, extra=oidc))
    os.chmod(cfg, 0o600)
    env, base, harness = env_for(binary), f'http://127.0.0.1:{port}', run_root / 'harness'
    identities = json.dumps([{'sub': 'owner', 'email': 'owner@t493.test', 'name': 'Owner'},
                             {'sub': 'member', 'email': member, 'name': 'Member'}])
    issuer = serve.ServeSession(harness, 'oidc-issuer', [sys.executable, str(HERE.parents[1] / 'pilot' / 'validation' / 'oidc_issuer.py'),
                                '--port', str(issuer_port), '--client-id', 'phebs-t493',
                                '--client-secret-file', str(secret_file), '--identities', identities],
                                f'http://127.0.0.1:{issuer_port}', ready_timeout_s=30)
    issuer.start()
    result = {'schema': 't493-thrift-atlas-rehearsal-v1', 'binary_sha256': serve.receipts.sha256_file(binary),
              'repositories': names, 'expected': {d: {k: len(v) for k, v in e.items()} for d, e in want.items()}, 'phases': {}}

    def phase(step: str, stop: str, cold: bool = False) -> dict:
        session = serve.ServeSession(harness, step, [str(binary), 'serve', '-config', str(cfg)], base,
                                     env=env, ready_timeout_s=300, stop_timeout_s=60)
        record: dict = {'ready_seconds': session.start()}
        try:
            with caller_operating.SessionRSS(os.getsid(session.pid)) as rss:
                record['seconds_to_oracle_equal'] = round(wait_atlas(base, key, want, names, deadline_s), 3)
                if cold:
                    status, body = serve.http_get(base, '/api/version', token=key)
                    record['capabilities'] = sorted(json.loads(body).get('capabilities', []))
                    if record['capabilities'] != ['contract-atlas', 'service-catalog-v2']:
                        raise serve.ServeError(f"Thrift release exposed {record['capabilities']}")
                    record['anonymous'] = serve.http_get(base, '/api/contract_atlas')[0]
                    if record['anonymous'] != 401:
                        raise serve.ServeError('anonymous Atlas was not refused')
                    owner, _ = serve.oidc_login(base, 'owner')  # the first OIDC login becomes the administrator
                    user, token = serve.oidc_login(base, 'member')
                    if not owner.get('is_admin') or user.get('is_admin'):
                        raise serve.ServeError('OIDC did not yield one administrator and one ordinary member')
                    check_atlas(base, token, {member_dir: want[member_dir]}, names)
                    record['member_atlas_limited_to'] = member_dir
                    items = atlas(base, key)
                    details = 0
                    for d in sorted(want):
                        op = next(i for i in items if i['kind'] == 'operation' and i['repository'] == names[d])
                        params = {'repository': op['repository'], 'lineage': op['declaration_lineage'], 'operation': op['operation']}
                        status, body = serve.http_get(base, '/api/contract_atlas/operation', params, token=key)
                        detail = json.loads(body) if status == 200 else {}
                        mcp = serve.mcp_call(base, 'get_contract_operation', {'protocol': 'thrift', 'repository': op['repository'],
                                             'declaration_lineage': op['declaration_lineage'], 'operation': op['operation']}, token=key)
                        if MCP:
                            serve.parity(body, {'structuredContent': (mcp.get('structuredContent') or {}).get('detail')}, serve_rehearsal.VOLATILE)
                        if (status != 200 or detail.get('unavailable_relationships') != RELATIONSHIPS
                                or any(detail.get(k) for k in RELATIONSHIPS)
                                or detail['coverage'].get('domains') != ['thrift-contract']
                                or 'request' not in detail or 'response' not in detail):
                            raise serve.ServeError(f'{d}: operation detail {status} {body[:300]!r}')
                        details += 1
                        if d == member_dir:
                            continue
                        hidden = serve.http_get(base, '/api/contract_atlas/operation', params, token=token)[0]
                        if hidden != 404:
                            raise serve.ServeError(f'a member read a hidden repository operation ({hidden})')
                    record['operation_details_checked'] = details
                    if MCP:
                        status, body = serve.http_get(base, '/api/contract_atlas', {'page_size': '100'}, token=key)
                        serve.parity(body, serve.mcp_call(base, 'search_contract_operations', {'page_size': 100}, token=key),
                                     serve_rehearsal.VOLATILE)
                        record['mcp_matches_http'] = True
                    latencies = []
                    for _ in range(20):
                        started = time.monotonic()
                        atlas(base, key)
                        latencies.append(time.monotonic() - started)
                    record['atlas_latency'] = caller_operating.percentiles(latencies)
                if step == 'restart':
                    backup = run_root / 'backup'
                    done = subprocess.run([str(binary), 'backup', '-config', str(cfg), '-output', str(backup)],
                                          env=env, capture_output=True, text=True, timeout=1800)
                    if done.returncode != 0:
                        raise serve.ServeError(f'online backup failed: {done.stderr[-400:]}')
                    record['backup_bytes'] = caller_operating.directory_bytes(backup)
            record['rss'] = rss.summary()
        except BaseException:
            session.stop('hard_kill', reason=f'{step} probe failed')
            raise
        receipt = session.stop(stop)
        record['stop'] = {k: receipt[k] for k in ('outcome', 'stop_mode', 'wall_seconds')}
        record['atlas_equals_oracle'] = True
        return record

    try:
        result['phases']['cold'] = phase('cold', 'graceful', cold=True)
        result['phases']['restart'] = phase('restart', 'hard_kill')
        result['phases']['hard-death'] = phase('hard-death', 'graceful')
        os.rename(data, run_root / 'data-before-restore')
        started = time.monotonic()
        done = subprocess.run([str(binary), 'restore', '-config', str(cfg), '-backup', str(run_root / 'backup')],
                              env=env, capture_output=True, text=True, timeout=1800)
        if done.returncode != 0:
            raise serve.ServeError(f'restore failed: {done.stderr[-400:]}')
        result['restore_seconds'] = round(time.monotonic() - started, 3)
        result['phases']['backup-restore'] = phase('backup-restore', 'graceful')
        result['data_bytes'] = caller_operating.directory_bytes(data)
    finally:
        issuer.stop('graceful')
    return result


def final(binary: Path, history: Path, lock_path: Path, expected: Path, selection: Path, artifacts: Path,
          key_id: str, public_key: str, revoke: list[str], run_root: Path, deadline_s: float = 900) -> dict:
    """All signed records first, then each named release revoked alone."""
    run_root.mkdir(parents=True)
    paths = prepare(history, json.loads(lock_path.read_text()), run_root / 'corpus')
    want = json.loads(expected.read_text())['repositories']
    names = {d: 'local' + str(p) for d, p in paths.items()}
    port, key, env = serve_rehearsal.free_port(), secrets.token_hex(24), env_for(binary)
    base, result = f'http://127.0.0.1:{port}', {}
    for step, ids in [('signed', [])] + [(f'revoked-{i}', [i]) for i in revoke]:
        cfg = run_root / f'{step}.yaml'
        cfg.write_text(config_text(port, run_root / 'data', key, selection, artifacts, key_id, public_key, paths, revoked=ids))
        os.chmod(cfg, 0o600)
        session = serve.ServeSession(run_root / 'harness', step, [str(binary), 'serve', '-config', str(cfg)], base,
                                     env=env, ready_timeout_s=300, stop_timeout_s=60)
        session.start()
        try:
            capabilities = sorted(json.loads(serve.http_get(base, '/api/version', token=key)[1]).get('capabilities', []))
            thrift_released = not any('thrift.contract' in i for i in ids)
            record = {'capabilities': capabilities}
            if thrift_released:
                if 'contract-atlas' not in capabilities:
                    raise serve.ServeError(f'{step}: Atlas missing {capabilities}')
                record['seconds_to_oracle_equal'] = round(wait_atlas(base, key, want, names, deadline_s), 3)
                record['atlas_equals_oracle'] = True
            else:
                record['atlas_status'] = serve.http_get(base, '/api/contract_atlas', token=key)[0]
                if 'contract-atlas' in capabilities or record['atlas_status'] == 200:
                    raise serve.ServeError(f'{step}: a revoked Thrift release still serves Atlas')
            result[step] = record
        except BaseException:
            session.stop('hard_kill', reason=f'{step} probe failed')
            raise
        session.stop('graceful')
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for flag in ('--binary', '--history', '--lock', '--expected', '--selection', '--artifacts', '--run-root'):
        parser.add_argument(flag, type=Path, required=True)
    parser.add_argument('--key-id', required=True)
    parser.add_argument('--public-key', required=True)
    parser.add_argument('--member', default='HartLuk__CCS')
    parser.add_argument('--final-revoke', action='append', default=[], metavar='RELEASE_ID')
    parser.add_argument('--mcp', action='store_true', help='require Atlas MCP parity (T49.4)')
    args = parser.parse_args()
    global MCP
    MCP = args.mcp
    common = (args.binary.resolve(), args.history, args.lock, args.expected, args.selection.resolve(),
              args.artifacts.resolve(), args.key_id, args.public_key)
    if args.final_revoke:
        out = final(*common, args.final_revoke, args.run_root.resolve())
    else:
        out = rehearse(*common, args.member, args.run_root.resolve())
    print(json.dumps(out, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
