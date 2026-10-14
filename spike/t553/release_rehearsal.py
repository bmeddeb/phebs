"""T55.3/T56.3 release gates for the released Kafka planes on the real binary.

`--planes` names the released planes (default producer, as in T55.3); every
census check runs per released plane, and every other plane must be named
unavailable.

The real `phebs serve` runs with no experimental switch. Only the signed
selection admits the producer plane. The repositories are seven T55.1 corpus
repositories at their locked commits, served as local git connections. The
expected census is the independent T55.1 oracle's unresolved-site counts at
those commits.

The phases:

- **cold.** Wait for every producer run to publish. Check the capability
  set, admin census parity, a member's census restricted to its one visible
  repository, anonymous denial, bundle reauthorization and MCP. Measure
  query latency and session RSS.
- **restart.** A graceful stop, then the same census.
- **hard-death.** A SIGKILL of the whole session, then the same census.
- **backup-restore.** An online backup, restore into a fresh data dir, then
  the same census.
"""
from __future__ import annotations

import argparse
import collections
import json
import os
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

REPOS = ('goka', 'kafka-pixy', 'watermill-kafka', 'Burrow', 'go-dnscollector', 'plugins', 'tiflow')
MEMBER_REPO = 'goka'
PLANES = ['producer']  # set from --planes
TOPICS = False  # --topics: the T57.3 combined Topics claim is selected beside both planes
CLASSES = ('selector-expr', 'call-expr', 'unresolved-ident', 'non-literal-expr', 'invalid-topic-literal',
           'ambiguous-library-import')


def prepare(history: Path, lock: dict, work: Path) -> dict[str, Path]:
    commits = {r['dir']: r['commit'] for r in lock['repos']}
    paths = {}
    for name in REPOS:
        target = work / name
        subprocess.run(['git', 'clone', '-q', '--no-checkout', str(history / name), str(target)], check=True)
        subprocess.run(['git', '-C', str(target), 'checkout', '-q', '-B', 'main', commits[name]], check=True)
        paths[name] = target
    return paths


def oracle_rows(oracle: Path, paths: dict[str, Path]) -> dict[str, dict[str, list[dict]]]:
    """Per released plane and repository: the independent oracle's site rows."""
    return {plane: {name: [json.loads(line) for line in subprocess.run(
        [str(oracle), '-plane', plane, '-repo', str(path), '-name', name],
        check=True, capture_output=True, text=True).stdout.splitlines()] for name, path in paths.items()}
        for plane in PLANES}


def expected(rows: dict) -> dict[str, dict[str, dict[str, int]]]:
    """Per released plane and repository: unresolved-site counts by class, from the independent oracle."""
    out: dict = {}
    for plane, by_repo in rows.items():
        out[plane] = {}
        for name, sites in by_repo.items():
            counts = collections.Counter(r['class'] for r in sites if r['state'] == 'unresolved')
            out[plane][name] = {c: counts.get(c, 0) for c in CLASSES}
    return out


def expected_topics(rows: dict, names) -> dict[str, dict[str, dict[str, int]]]:
    """TOPICS_QUALITY_PROTOCOL §3: topic -> plane -> resolved sites and distinct files, over `names`."""
    sites: dict = collections.defaultdict(lambda: collections.defaultdict(list))
    for plane, by_repo in rows.items():
        for name in names:
            for r in by_repo[name]:
                if r['state'] == 'resolved' and r.get('object', '').startswith('topic:'):
                    sites[r['object'][len('topic:'):]][plane].append((name, r['path']))
    return {topic: {plane: {'sites': len(found), 'files': len(set(found))} for plane, found in by_plane.items()}
            for topic, by_plane in sites.items()}


def inventory(base: str, token: str) -> tuple[dict, dict, list]:
    """The served composed inventory, every page: topic -> plane -> sites/files for served rows, the
    first page's report, and that page's rows."""
    status, body = serve.http_get(base, '/api/list_kafka_topics', token=token)
    if status != 200:
        raise serve.ServeError(f'list_kafka_topics answered {status}: {body[:300]!r}')
    bundle, cursor, first, out = json.loads(body)['id'], '', None, {}
    while True:
        query = {'page_size': '100', **({'cursor': cursor} if cursor else {})}
        status, body = serve.http_get(base, f'/api/topic_inventory/{bundle}', query, token=token)
        if status != 200:
            raise serve.ServeError(f'topic_inventory answered {status}: {body[:300]!r}')
        report = json.loads(body)
        first = first or report
        if report.get('truncated') or any(not p['released'] or p['state'] != 'served' for p in report['planes']):
            raise serve.ServeError(f"inventory is truncated or a plane is not served: {report['planes']} {report.get('truncated')}")
        for row in report['topics']:
            if row['topic'] in out:
                raise serve.ServeError(f"topic {row['topic']!r} repeats across pages")
            out[row['topic']] = {p: {'sites': row[p]['sites'], 'files': row[p]['files']}
                                 for p in PLANES if row[p]['state'] == 'served' and row[p]['sites'] > 0}
        cursor = report['pagination'].get('next_cursor', '')
        if not cursor:
            if not report['pagination']['complete'] or len(out) != report['pagination']['total']:
                raise serve.ServeError(f"inventory pagination ended incomplete: {report['pagination']}")
            return out, first, first['topics']


def check_topics(base: str, token: str, want: dict, census_want: dict, published: int) -> dict:
    served, first, _ = inventory(base, token)
    if served != want:
        missing, extra = sorted(set(want) - set(served)), sorted(set(served) - set(want))
        wrong = sorted(t for t in set(want) & set(served) if want[t] != served[t])
        raise serve.ServeError(f'inventory differs from the oracle union: missing {missing[:10]} extra {extra[:10]} '
                               f'wrong {[(t, served[t], want[t]) for t in wrong[:5]]}')
    if not matches(first['unresolved_census'], census_want, published=published):
        raise serve.ServeError(f"inventory census {first['unresolved_census']} differs from the oracle {census_want}")
    return {'topics': len(served), 'inventory_matches_oracle': True}


def total(expected_counts: dict, names) -> dict[str, dict[str, int]]:
    return {plane: {c: sum(by_repo[n][c] for n in names) for c in CLASSES} for plane, by_repo in expected_counts.items()}


def capabilities_want() -> list[str]:
    return (['kafka-topic-inventory'] if TOPICS else []) + ['kafka-topic-usage', 'service-catalog-v2']


def unavailable() -> list[str]:
    return [p for p in ('consumer', 'producer') if p not in PLANES]


def matches(served: dict, want: dict, published: int | None = None) -> bool:
    return (all(served[p] == want[p] for p in PLANES) and (served.get('unavailable_planes') or []) == unavailable()
            and (published is None or all(served[f'{p}_published_runs'] == published for p in PLANES)))


def census(base: str, token: str, topic: str = 'orders') -> tuple[dict, str]:
    status, body = serve.http_get(base, '/api/find_kafka_topic_usage', {'topic': topic}, token=token)
    if status != 200:
        raise serve.ServeError(f'topic usage answered {status}: {body[:300]!r}')
    envelope = json.loads(body)
    return envelope['bundle']['unresolved_census'], envelope['id']


def comparable(value: dict) -> dict:
    return {k: value.get(k) for k in ('producer', 'producer_published_runs', 'consumer', 'consumer_published_runs',
                                      'unavailable_planes', 'truncated')}


def wait_published(base: str, key: str, repos: int, deadline_s: float) -> tuple[float, dict]:
    started, last = time.monotonic(), None
    while True:
        try:
            now, _ = census(base, key)
            if all(now[f'{p}_published_runs'] == repos for p in PLANES) and last == comparable(now):
                return time.monotonic() - started, now
            last = comparable(now)
        except (serve.ServeError, OSError, KeyError):
            last = None
        if time.monotonic() - started > deadline_s:
            raise serve.ServeError(f'released plane runs did not all publish before the deadline (last {last})')
        time.sleep(3)


def rehearse(binary: Path, history: Path, lock_path: Path, oracle: Path, selection: Path, artifacts: Path,
             key_id: str, public_key: str, run_root: Path, deadline_s: float = 900) -> dict:
    run_root.mkdir(parents=True)
    paths = prepare(history, json.loads(lock_path.read_text()), run_root / 'corpus')
    rows = oracle_rows(oracle, paths)
    want = expected(rows)
    names = {name: 'local' + str(path) for name, path in paths.items()}
    port, issuer_port, key = serve_rehearsal.free_port(), serve_rehearsal.free_port(), secrets.token_hex(24)
    secret_file = run_root / 'oidc-client-secret'
    secret_file.write_text(secrets.token_hex(24))
    os.chmod(secret_file, 0o600)
    member = 'member@t553.test'
    connections = ''.join(f'  - name: {n.lower()}\n    type: git\n    url: "{p}"\n' for n, p in paths.items())

    def config(data: Path) -> Path:
        path = run_root / f'phebs-{data.name}.yaml'
        path.write_text(
            f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{data}"\n'
            f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n  oidc:\n'
            f'    issuer_url: "http://127.0.0.1:{issuer_port}"\n    client_id: "phebs-t553"\n'
            f'    client_secret: "{secret_file.read_text()}"\n'
            f'    redirect_url: "http://127.0.0.1:{port}/api/auth/oidc/callback"\n'
            f'permissions:\n  users:\n    {member}: []\n  always_visible: ["{names[MEMBER_REPO]}"]\n'
            f'release_selection:\n  path: "{selection}"\n  artifacts_path: "{artifacts}"\n'
            f'  keys:\n    - id: "{key_id}"\n      public_key: "{public_key}"\n'
            f'sync:\n  poll_interval: 15s\n  resync_interval: 1h\n'
            f'connections:\n{connections}')
        os.chmod(path, 0o600)
        return path

    helpers = binary.parent
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    base = f'http://127.0.0.1:{port}'
    harness = run_root / 'harness'
    identities = json.dumps([{'sub': 'owner', 'email': 'owner@t553.test', 'name': 'Owner'},
                             {'sub': 'member', 'email': member, 'name': 'Member'}])
    issuer = serve.ServeSession(harness, 'oidc-issuer', [sys.executable, str(HERE.parents[1] / 'pilot' / 'validation' / 'oidc_issuer.py'),
                                '--port', str(issuer_port), '--client-id', 'phebs-t553',
                                '--client-secret-file', str(secret_file), '--identities', identities],
                                f'http://127.0.0.1:{issuer_port}', ready_timeout_s=30)
    issuer.start()
    admin_want = total(want, REPOS)
    result = {'schema': 't553-kafka-release-rehearsal-v2', 'planes': PLANES, 'binary_sha256': serve.receipts.sha256_file(binary),
              'repositories': {n: names[n] for n in REPOS}, 'expected_by_repository': want,
              'expected_total': admin_want, 'phases': {}}
    if TOPICS:
        topics_want = expected_topics(rows, REPOS)
        result['schema'], result['expected_topics'] = 't573-topics-release-rehearsal-v1', len(topics_want)
    data = run_root / 'data'
    reference = None

    def phase(step: str, cfg: Path, stop: str, cold: bool = False) -> dict:
        nonlocal reference
        session = serve.ServeSession(harness, step, [str(binary), 'serve', '-config', str(cfg)], base,
                                     env=env, ready_timeout_s=300, stop_timeout_s=60)
        ready = session.start()
        record: dict = {'ready_seconds': ready}
        try:
            with caller_operating.SessionRSS(os.getsid(session.pid)) as rss:
                seconds, admin = wait_published(base, key, len(REPOS), deadline_s)
                record['seconds_to_published'] = round(seconds, 3)
                if not matches(admin, admin_want):
                    raise serve.ServeError(f'{step}: served census {admin} differs from the oracle {admin_want}')
                if reference is not None and comparable(admin) != reference:
                    raise serve.ServeError(f'{step}: census changed across the lifecycle')
                reference = comparable(admin)
                if TOPICS:
                    record['topics'] = check_topics(base, key, topics_want, admin_want, len(REPOS))
                if cold:
                    status, body = serve.http_get(base, '/api/version', token=key)
                    record['capabilities'] = sorted(json.loads(body).get('capabilities', []))
                    if record['capabilities'] != capabilities_want():
                        raise serve.ServeError(f"released Kafka planes exposed {record['capabilities']}")
                    record['anonymous'] = serve.http_get(base, '/api/find_kafka_topic_usage', {'topic': 'orders'})[0]
                    if record['anonymous'] != 401:
                        raise serve.ServeError('anonymous topic usage was not refused')
                    owner, _ = serve.oidc_login(base, 'owner')  # the first OIDC login becomes the administrator
                    user, token = serve.oidc_login(base, 'member')
                    if not owner.get('is_admin') or user.get('is_admin'):
                        raise serve.ServeError('OIDC did not yield one administrator and one ordinary member')
                    listed = sorted(r['name'] for r in serve_rehearsal.listing(base, token))
                    if listed != [names[MEMBER_REPO]]:
                        raise serve.ServeError(f'member repository listing is {listed}')
                    member_census, _ = census(base, token)
                    member_want = total(want, [MEMBER_REPO])
                    if not matches(member_census, member_want, published=1):
                        raise serve.ServeError(f'member census {member_census} is not limited to {MEMBER_REPO} ({member_want})')
                    _, admin_bundle = census(base, key)
                    record['member_reads_admin_bundle'] = serve.http_get(base, f'/api/proof_bundles/{admin_bundle}', token=token)[0]
                    if record['member_reads_admin_bundle'] != 404:
                        raise serve.ServeError('a member read a bundle covering repositories it cannot see')
                    if TOPICS:
                        record['member_topics'] = check_topics(base, token, expected_topics(rows, [MEMBER_REPO]),
                                                               member_want, 1)
                        record['anonymous_inventory'] = serve.http_get(base, '/api/list_kafka_topics')[0]
                        if record['anonymous_inventory'] != 401:
                            raise serve.ServeError('anonymous inventory was not refused')
                        _, _, page = inventory(base, key)
                        listed = serve.mcp_call(base, 'list_kafka_topics', {'page_size': 100}, token=key)
                        mcp_rows = (listed.get('structuredContent') or {}).get('topics')
                        if listed.get('isError') or mcp_rows != page:
                            raise serve.ServeError('MCP list_kafka_topics differs from the HTTP first page')
                        record['mcp_inventory_matches_http'] = True
                        latencies = []
                        for _ in range(20):
                            started = time.monotonic()
                            inventory(base, key)
                            latencies.append(time.monotonic() - started)
                        record['inventory_latency'] = caller_operating.percentiles(latencies)
                    mcp = serve.mcp_call(base, 'find_kafka_topic_usage', {'topic': 'orders'}, token=key)
                    if mcp.get('isError'):
                        raise serve.ServeError(f'MCP topic usage failed: {mcp}')
                    latencies = []
                    for i in range(60):
                        started = time.monotonic()
                        census(base, key, topic=f'orders-{i % 6}')
                        latencies.append(time.monotonic() - started)
                    record['query_latency'] = caller_operating.percentiles(latencies)
                    record['member_census_matches'] = True
                if step == 'restart':
                    backup = run_root / 'backup'
                    completed = subprocess.run([str(binary), 'backup', '-config', str(cfg), '-output', str(backup)],
                                               env=env, capture_output=True, text=True, timeout=1800)
                    if completed.returncode != 0:
                        raise serve.ServeError(f'online backup failed: {completed.stderr[-400:]}')
                    record['backup_bytes'] = caller_operating.directory_bytes(backup)
            record['rss'] = rss.summary()
        except BaseException:
            session.stop('hard_kill', reason=f'{step} probe failed')
            raise
        receipt = session.stop(stop)
        record['stop'] = {k: receipt[k] for k in ('outcome', 'stop_mode', 'wall_seconds')}
        record['census_matches_oracle'] = True
        return record

    try:
        cfg = config(data)
        result['phases']['cold'] = phase('cold', cfg, 'graceful', cold=True)
        result['phases']['restart'] = phase('restart', cfg, 'hard_kill')
        result['phases']['hard-death'] = phase('hard-death', cfg, 'graceful')
        os.rename(data, run_root / 'data-before-restore')
        started = time.monotonic()
        completed = subprocess.run([str(binary), 'restore', '-config', str(cfg), '-backup', str(run_root / 'backup')],
                                   env=env, capture_output=True, text=True, timeout=1800)
        if completed.returncode != 0:
            raise serve.ServeError(f'restore failed: {completed.stderr[-400:]}')
        result['restore_seconds'] = round(time.monotonic() - started, 3)
        result['phases']['backup-restore'] = phase('backup-restore', cfg, 'graceful')
        result['data_bytes'] = caller_operating.directory_bytes(data)
    finally:
        issuer.stop('graceful')
    return result


def final(binary: Path, history: Path, lock_path: Path, oracle: Path, selection: Path, artifacts: Path,
          key_id: str, public_key: str, revoke: list[str], run_root: Path, deadline_s: float = 900) -> dict:
    """The signed records: release-only admission with census parity, then revoking `revoke`
    (`release_id=plane` pairs) withdraws exactly those planes and leaves the rest served."""
    global PLANES
    run_root.mkdir(parents=True)
    paths = prepare(history, json.loads(lock_path.read_text()), run_root / 'corpus')
    rows = oracle_rows(oracle, paths)
    want = total(expected(rows), REPOS)
    port, key = serve_rehearsal.free_port(), secrets.token_hex(24)
    connections = ''.join(f'  - name: {n.lower()}\n    type: git\n    url: "{p}"\n' for n, p in paths.items())
    helpers = binary.parent
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    base, result = f'http://127.0.0.1:{port}', {'planes': list(PLANES), 'expected_total': want}
    released = list(PLANES)
    global TOPICS
    topics_selected = TOPICS
    steps = [('signed', [])] + [(f'revoked-{pair.split("=")[1]}', [pair.split('=')[0]]) for pair in revoke]
    if not topics_selected:  # T55.3/T56.3 shape: one step revoking every named release
        steps = [('signed', []), ('revoked', [pair.split('=')[0] for pair in revoke])]
    for step, ids in steps:
        revoked_planes = [pair.split('=')[1] for pair in revoke if pair.split('=')[0] in ids]
        PLANES = [p for p in released if p not in revoked_planes]
        # Topics serves only while its own record and both planes stand (releasedKafkaTopics).
        TOPICS = topics_selected and 'topics' not in revoked_planes and len(PLANES) == 2
        revoked = ('  revoked:\n' + ''.join(f'    - "{i}"\n' for i in ids)) if ids else ''
        cfg = run_root / f'{step}.yaml'
        cfg.write_text(f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{run_root / "data"}"\n'
                       f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n'
                       f'release_selection:\n  path: "{selection}"\n  artifacts_path: "{artifacts}"\n'
                       f'  keys:\n    - id: "{key_id}"\n      public_key: "{public_key}"\n{revoked}'
                       f'sync:\n  poll_interval: 15s\n  resync_interval: 1h\nconnections:\n{connections}')
        os.chmod(cfg, 0o600)
        session = serve.ServeSession(run_root / 'harness', step, [str(binary), 'serve', '-config', str(cfg)], base,
                                     env=env, ready_timeout_s=300, stop_timeout_s=60)
        session.start()
        try:
            status, body = serve.http_get(base, '/api/version', token=key)
            capabilities = sorted(json.loads(body).get('capabilities', []))
            if PLANES:
                seconds, served = wait_published(base, key, len(REPOS), deadline_s)
                if capabilities != capabilities_want() or not matches(served, want):
                    raise serve.ServeError(f'{step}: {capabilities} {served}')
                result[step] = {'capabilities': capabilities, 'served_planes': list(PLANES),
                                'seconds_to_published': round(seconds, 3), 'census_matches_oracle': True,
                                'unavailable_planes': served.get('unavailable_planes')}
                if TOPICS:
                    result[step]['topics'] = check_topics(base, key, expected_topics(rows, REPOS), want, len(REPOS))
                elif topics_selected:
                    result[step]['inventory_status'] = serve.http_get(base, '/api/list_kafka_topics', token=key)[0]
                    if result[step]['inventory_status'] != 404:
                        raise serve.ServeError(f'{step}: withdrawn Topics still serves the inventory')
            else:
                status = serve.http_get(base, '/api/find_kafka_topic_usage', {'topic': 'orders'}, token=key)[0]
                if 'kafka-topic-usage' in capabilities or status == 200:
                    raise serve.ServeError('revoked Kafka releases still serve topic usage')
                result[step] = {'capabilities': capabilities, 'topic_usage_status': status}
        except BaseException:
            session.stop('hard_kill', reason=f'{step} probe failed')
            raise
        session.stop('graceful')
    PLANES, TOPICS = released, topics_selected
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for flag in ('--binary', '--history', '--lock', '--oracle', '--selection', '--artifacts', '--run-root'):
        parser.add_argument(flag, type=Path, required=True)
    parser.add_argument('--key-id', required=True)
    parser.add_argument('--public-key', required=True)
    parser.add_argument('--planes', default='producer', help='comma-separated released planes')
    parser.add_argument('--final-revoke', action='append', default=[], metavar='RELEASE_ID=PLANE',
                        help='run the final signed-record proof, then revoke these releases '
                             '(with --topics, one at a time; PLANE "topics" names the Topics release)')
    parser.add_argument('--topics', action='store_true', help='the T57.3 Topics claim is selected beside both planes')
    args = parser.parse_args()
    global PLANES, TOPICS
    PLANES, TOPICS = sorted(args.planes.split(',')), args.topics
    if args.final_revoke:
        print(json.dumps(final(args.binary.resolve(), args.history, args.lock, args.oracle.resolve(), args.selection.resolve(),
                               args.artifacts.resolve(), args.key_id, args.public_key, args.final_revoke,
                               args.run_root.resolve()), indent=1, sort_keys=True))
        return
    print(json.dumps(rehearse(args.binary.resolve(), args.history, args.lock, args.oracle.resolve(),
                              args.selection.resolve(), args.artifacts.resolve(), args.key_id, args.public_key,
                              args.run_root.resolve()), indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
