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
import sys
import time
from pathlib import Path

import serve

VOLATILE = frozenset({'$schema', 'citation', 'next_cursor'})


def free_port() -> int:
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def fixture_repository(root: Path, name: str = 'fixture', token: str = 't48.3') -> Path:
    repo = root / name
    repo.mkdir(parents=True)
    (repo / 'main.go').write_text(f'package main\n\nfunc main() {{ println("{token}") }}\n')
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


REPO_READS = (('/api/source', {'path': 'main.go'}), ('/api/folder_contents', {}), ('/api/tree', {}),
              ('/api/commits', {}), ('/api/blame', {'path': 'main.go'}))


def authorization(binary: Path, zoekt: Path, run_root: Path, index_deadline_s: float = 180) -> dict:
    """A non-admin OIDC principal sees the visible repository and cannot tell the hidden one from an absent one."""
    run_root.mkdir(parents=True)
    visible = fixture_repository(run_root, 'fixture-visible', 't48visibleNeedle')
    hidden = fixture_repository(run_root, 'fixture-hidden', 't48hiddenNeedle')
    visible_name, hidden_name = 'local' + str(visible), 'local' + str(hidden)
    absent_name = 'local' + str(run_root / 'fixture-absent')
    port, issuer_port, key = free_port(), free_port(), secrets.token_hex(24)
    secret_file = run_root / 'oidc-client-secret'
    secret_file.write_text(secrets.token_hex(24))
    os.chmod(secret_file, 0o600)
    member = 'member@t48.test'
    identities = json.dumps([{'sub': 'owner', 'email': 'owner@t48.test', 'name': 'Owner'},
                             {'sub': 'member', 'email': member, 'name': 'Member'}])
    config = run_root / 'phebs.yaml'
    config.write_text(
        f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{run_root / "data"}"\n'
        f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n  oidc:\n'
        f'    issuer_url: "http://127.0.0.1:{issuer_port}"\n    client_id: "phebs-t48"\n'
        f'    client_secret: "{secret_file.read_text()}"\n'
        f'    redirect_url: "http://127.0.0.1:{port}/api/auth/oidc/callback"\n'
        f'permissions:\n  users:\n    {member}: []\n  always_visible: ["{visible_name}"]\n'
        f'sync:\n  poll_interval: 15s\n  resync_interval: 1h\n'
        f'connections:\n  - name: visible\n    type: git\n    url: "{visible}"\n'
        f'  - name: hidden\n    type: git\n    url: "{hidden}"\n')
    os.chmod(config, 0o600)
    harness = run_root / 'harness'
    base = f'http://127.0.0.1:{port}'
    issuer = serve.ServeSession(harness, 'oidc-issuer', [sys.executable, str(Path(__file__).with_name('oidc_issuer.py')),
                                '--port', str(issuer_port), '--client-id', 'phebs-t48',
                                '--client-secret-file', str(secret_file), '--identities', identities],
                                f'http://127.0.0.1:{issuer_port}', ready_timeout_s=30)
    issuer.start()
    server = serve.ServeSession(harness, 'authorization', [str(binary), 'serve', '-config', str(config)], base,
                                env={**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(zoekt)}, ready_timeout_s=120)
    try:
        server.start()
        deadline = time.monotonic() + index_deadline_s
        while not (len(rows := listing(base, key)) == 2 and all(r.get('indexed_commit_hash') for r in rows)):
            if time.monotonic() > deadline:
                raise serve.ServeError('fixtures were not indexed before the deadline')
            time.sleep(1)
        owner, _ = serve.oidc_login(base, 'owner')
        user, token = serve.oidc_login(base, 'member')
        if not owner.get('is_admin') or user.get('is_admin') or user.get('email') != member:
            raise serve.ServeError('OIDC did not yield one administrator and one ordinary member')
        names = lambda rows: sorted(r['name'] for r in rows)  # noqa: E731
        if names(listing(base, token)) != [visible_name] or names(listing(base, key)) != sorted([visible_name, hidden_name]):
            raise serve.ServeError('repository listings do not reflect the permission block')
        checked = []
        for path, extra in REPO_READS:
            admin = serve.http_get(base, path, {'repo': hidden_name, **extra}, token=key)
            if admin[0] != 200:
                raise serve.ServeError(f'administrator control read of {path} returned {admin[0]}')
            serve.undisclosed(serve.http_get(base, path, {'repo': hidden_name, **extra}, token=token),
                              serve.http_get(base, path, {'repo': absent_name, **extra}, token=token),
                              hidden_name, absent_name)
            checked.append(path)
        def hits(principal, needle):
            status, body = serve.http_get(base, '/api/search', {'q': needle}, token=principal)
            if status != 200:
                raise serve.ServeError(f'search returned {status}')
            return body.count(needle.encode())
        if hits(key, 't48hiddenNeedle') == 0 or hits(token, 't48hiddenNeedle') != 0 or hits(token, 't48visibleNeedle') == 0:
            raise serve.ServeError('search visibility does not follow the permission block')
        mcp = json.dumps(serve.mcp_call(base, 'list_repos', {}, token=token))
        if hidden_name in mcp or visible_name not in mcp:
            raise serve.ServeError('MCP list_repos does not follow the permission block')
    except BaseException:
        if server.pid:
            server.stop('hard_kill', reason='authorization probe failed')
        issuer.stop('hard_kill', reason='authorization probe failed')
        raise
    receipts_out = {'server': server.stop('graceful'), 'issuer': issuer.stop('graceful')}
    return {'schema': 't48.3-authorization-rehearsal-v1', 'binary_sha256': serve.receipts.sha256_file(binary),
            'ordinary_principal': 'OIDC member, not administrator', 'undisclosed_reads': checked,
            'search_hidden': True, 'mcp_list_hidden': True,
            'receipts': {k: {f: v[f] for f in ('outcome', 'stop_mode', 'ready_seconds', 'exit_code', 'teardown')}
                         for k, v in receipts_out.items()}}


ATLAS_PROTO = """syntax = "proto3";
package demo.v1;

service Catalog {
  rpc Get (GetRequest) returns (GetResponse);
}

message GetRequest { string id = 1; }
message GetResponse { string name = 1; }
"""


def atlas(binary: Path, helpers: Path, run_root: Path, deadline_s: float = 300) -> dict:
    """Contract Atlas serves a partitioned declaration root exactly, and HTTP equals MCP."""
    run_root.mkdir(parents=True)
    repo = run_root / 'contracts'
    (repo / 'api' / 'v1').mkdir(parents=True)
    (repo / 'api' / 'v1' / 'catalog.proto').write_text(ATLAS_PROTO)
    env_git = {**os.environ, 'GIT_AUTHOR_NAME': 't49.3', 'GIT_AUTHOR_EMAIL': 't49.3@localhost',
               'GIT_COMMITTER_NAME': 't49.3', 'GIT_COMMITTER_EMAIL': 't49.3@localhost'}
    for argv in (['git', 'init', '-q', '-b', 'main'], ['git', 'add', '.'], ['git', 'commit', '-q', '-m', 'fixture']):
        subprocess.run(argv, cwd=repo, env=env_git, check=True)
    name = 'local' + str(repo)
    port, key = free_port(), secrets.token_hex(24)
    config = run_root / 'phebs.yaml'
    config.write_text(f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{run_root / "data"}"\n'
                      f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n'
                      f'experimental:\n  provisional_proto_extraction: true\n  typed_scope_reservations: true\n'
                      f'sync:\n  poll_interval: 15s\n  resync_interval: 1h\n'
                      f'connections:\n  - name: contracts\n    type: git\n    url: "{repo}"\n')
    os.chmod(config, 0o600)
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    base = f'http://127.0.0.1:{port}'
    session = serve.ServeSession(run_root / 'harness', 'atlas', [str(binary), 'serve', '-config', str(config)], base,
                                 env=env, ready_timeout_s=120)
    session.start()
    try:
        deadline = time.monotonic() + deadline_s
        while True:
            status, body = serve.http_get(base, '/api/contract_atlas', {'page_size': 100}, token=key)
            page = json.loads(body) if status == 200 else {}
            if page.get('items'):
                break
            if time.monotonic() > deadline:
                raise serve.ServeError(f'Atlas listed no declaration before the deadline (last status {status})')
            time.sleep(2)
        authorities = page.get('exact_authorities') or []
        if len(authorities) != 1 or authorities[0]['repository'] != name or authorities[0]['domain'] != 'proto-contract':
            raise serve.ServeError(f'Atlas did not name the exact partitioned root: {authorities}')
        legacy = [r for repo_cov in page['coverage']['repositories'] for r in repo_cov['runs']
                  if r['domain'] == 'proto-contract' and r['status'] == 'published']
        if legacy:
            raise serve.ServeError('the fixture used a legacy published run, so the partitioned path was not exercised')
        serve.parity(body, serve.mcp_call(base, 'search_contract_operations', {'page_size': 100}, token=key), VOLATILE)
        item = next(i for i in page['items'] if i.get('operation'))
        params = {'repository': name, 'lineage': item['declaration_lineage'], 'operation': item['operation']}
        status, detail = serve.http_get(base, '/api/contract_atlas/operation', params, token=key)
        if status != 200 or json.loads(detail).get('exact_authority', {}).get('root_digest') != authorities[0]['root_digest']:
            raise serve.ServeError(f'Atlas operation detail did not come from the exact root (status {status})')
        mcp_args = {'protocol': item.get('protocol', 'protobuf'), 'repository': name,
                    'declaration_lineage': item['declaration_lineage'], 'operation': item['operation']}
        mcp_detail = serve.mcp_call(base, 'get_contract_operation', mcp_args, token=key)
        if mcp_detail.get('isError'):
            raise serve.ServeError(f"MCP detail failed: {mcp_detail.get('content')}")
        # MCP wraps the same detail with an echo of the requested endpoint.
        serve.parity(detail, {'structuredContent': (mcp_detail.get('structuredContent') or {}).get('detail')}, VOLATILE)
        anonymous = serve.http_get(base, '/api/contract_atlas')[0]
    except BaseException:
        session.stop('hard_kill', reason='atlas probe failed')
        raise
    receipt = session.stop('graceful')
    return {'schema': 't49.3-atlas-rehearsal-v1', 'binary_sha256': serve.receipts.sha256_file(binary),
            'items': len(page['items']), 'exact_authority': {k: authorities[0][k] for k in ('domain', 'run_id', 'root_digest')},
            'legacy_certificate_unpublished': True, 'http_mcp_list_parity': True, 'http_mcp_detail_parity': True,
            'anonymous_status': anonymous,
            'receipt': {k: receipt[k] for k in ('outcome', 'stop_mode', 'ready_seconds', 'exit_code', 'teardown')}}


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
    auth = sub.add_parser('authorization')
    auth.add_argument('--binary', type=Path, required=True)
    auth.add_argument('--zoekt', type=Path, required=True)
    auth.add_argument('--run-root', type=Path, required=True)
    at = sub.add_parser('atlas')
    at.add_argument('--binary', type=Path, required=True)
    at.add_argument('--helpers', type=Path, required=True)
    at.add_argument('--run-root', type=Path, required=True)
    call = sub.add_parser('callers')
    call.add_argument('--base', required=True)
    call.add_argument('--key-file', type=Path, required=True, help='operator config holding auth.api_key')
    call.add_argument('--endpoints', type=Path, required=True)
    call.add_argument('--absent-repository', required=True)
    args = parser.parse_args()
    if args.command == 'lifecycle':
        result = lifecycle(args.binary.resolve(), args.zoekt.resolve(), args.run_root.resolve())
    elif args.command == 'atlas':
        result = atlas(args.binary.resolve(), args.helpers.resolve(), args.run_root.resolve())
    elif args.command == 'authorization':
        result = authorization(args.binary.resolve(), args.zoekt.resolve(), args.run_root.resolve())
    else:
        import re
        key = re.search(r'^\s*api_key:\s*"?([^"\s]+)"?\s*$', args.key_file.read_text(), re.M).group(1)
        result = callers(args.base, key, json.loads(args.endpoints.read_text()), args.absent_repository)
    print(json.dumps(result, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
