"""T53.3: run the sealed change population through the real binary, then score it.

`run` starts the real `phebs serve` with the provisional switch that admits the
standalone verdict. It sends every closed unit to `POST
/api/check_wire_compatibility` and to the MCP `check_wire_compatibility` tool,
and records the verdict and HTTP/MCP parity. It must not run before the round
is sealed: the oracle, the population and these thresholds are frozen first.

`score` compares each unit's product outcome with its truth: the oracle's
verdict, replaced by a hand adjudication where one exists. A unit whose truth
is undetermined, or whose outcome disagrees, scores only once adjudicated.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
import secrets
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[1] / 'pilot' / 'validation'))
import serve  # noqa: E402
import wire_oracle  # noqa: E402
from enumerate_changes import LINEAGE  # noqa: E402

SCHEMA = 't533-wire-round-v2'
# Round 2, the narrower claim (docs/WIRE_COMPATIBILITY_PROTOCOL.md §9). Round 1's frozen v1
# thresholds live at the seal commit d88a6c97.
THRESHOLDS = {
    'permissive_max': 0,             # the claim: never call a breaking change compatible
    'judged_unreadable_max': 0,      # a source that does not compile is refused, never judged
    'pending_max': 0,                # every undetermined truth and every disagreement is adjudicated
    'undisclosed_error_rate_max': 0.01,  # conservative or refused-readable outside the disclosed classes
    'breaking_truth_min': 50,        # enough real breaks for the zero-permissive result to mean something
}
DISCLOSED_RULES = {'FIELD_WIRE_COMPATIBLE_CARDINALITY', 'FIELD_WIRE_COMPATIBLE_TYPE'}
DISCLOSED_NOTES = {'disclosed_cardinality', 'disclosed_type_equivalent'}


def units(population: Path):
    for line in gzip.open(population / 'units.jsonl.gz'):
        unit = json.loads(line)
        if unit['state'] == 'closed':
            yield unit


def contents(history: Path, unit: dict) -> tuple[list, list]:
    repo = history / unit['repository'].split('/')[1]

    def read(files):
        out = []
        for f in files:
            content = subprocess.run(['git', '-C', str(repo), 'cat-file', 'blob', f['blob']],
                                     check=True, capture_output=True).stdout
            if 'sha256:' + hashlib.sha256(content).hexdigest() != f['sha256']:
                raise ValueError(f"{unit['unit_id']}: {f['path']} does not match its frozen digest")
            out.append((f['path'], content))
        return out
    return read(unit['before']), read(unit['after'])


def post(base: str, path: str, body: dict, token: str) -> tuple[int, bytes]:
    request = urllib.request.Request(base + path, data=json.dumps(body).encode(), method='POST', headers={
        'Content-Type': 'application/json', 'Accept': 'application/json', 'Authorization': 'Bearer ' + token})
    try:
        with urllib.request.urlopen(request, timeout=120) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def outcome(status: int, body: bytes) -> dict:
    if status != 200:
        return {'product': 'refused', 'status': status}
    result = json.loads(body)
    return {'product': 'compatible' if result['compatible'] else 'breaking', 'status': status,
            'rules': sorted({v['rule'] for v in result['violations']}),
            'before_digest': result['before']['digest'], 'after_digest': result['after']['digest']}


def run(binary: Path, helpers: Path, history: Path, population: Path, run_root: Path) -> dict:
    run_root.mkdir(parents=True)
    port, key = free_port(), secrets.token_hex(24)
    config = run_root / 'phebs.yaml'
    config.write_text(f'server:\n  addr: "127.0.0.1:{port}"\n  data_dir: "{run_root / "data"}"\n'
                      f'auth:\n  api_key: "{key}"\n  cookie_secure: false\n'
                      f'experimental:\n  provisional_proto_extraction: true\n'
                      f'connections: []\n')
    os.chmod(config, 0o600)
    env = {**os.environ, 'PHEBS_ZOEKT_GIT_INDEX': str(helpers / 'zoekt-git-index'),
           'PHEBS_FOCUSED_INDEX': str(helpers / 'phebs-focused-index'), 'PHEBS_BUF': str(helpers / 'buf')}
    base = f'http://127.0.0.1:{port}'
    session = serve.ServeSession(run_root / 'harness', 'wire-round', [str(binary), 'serve', '-config', str(config)],
                                 base, env=env, ready_timeout_s=120)
    session.start()
    rows = []
    try:
        status, body = serve.http_get(base, '/api/version', token=key)
        if status != 200 or 'wire-compatibility' not in json.loads(body).get('capabilities', []):
            raise serve.ServeError('the real binary does not advertise wire-compatibility')
        for unit in units(population):
            before, after = contents(history, unit)
            request = {'lineage': LINEAGE,
                       'before': [{'path': p, 'content': c.decode()} for p, c in before],
                       'after': [{'path': p, 'content': c.decode()} for p, c in after]}
            status, body = post(base, '/api/check_wire_compatibility', request, key)
            row = {'unit_id': unit['unit_id'], **outcome(status, body)}
            mcp = serve.mcp_call(base, 'check_wire_compatibility', request, token=key)
            if status == 200:
                http_body = {k: v for k, v in json.loads(body).items() if k != '$schema'}  # Huma's HTTP-only link
                row['parity'] = not mcp.get('isError') and mcp.get('structuredContent') == http_body
            else:
                row['parity'] = bool(mcp.get('isError'))
            rows.append(row)
    finally:
        receipt = session.stop('graceful')
    out = b''.join(json.dumps(r, sort_keys=True, separators=(',', ':')).encode() + b'\n' for r in rows)
    (run_root / 'results.jsonl.gz').write_bytes(gzip.compress(out, mtime=0))
    return {'schema': SCHEMA, 'units': len(rows), 'stop': receipt.get('outcome'),
            'results_sha256': 'sha256:' + hashlib.sha256(out).hexdigest()}


def free_port() -> int:
    import socket
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def classify(product: dict, truth: str, findings: list[dict], disclosed: bool = False) -> str:
    """One unit's scoring class. truth is compatible, breaking, unreadable or undetermined."""
    p = product['product']
    if truth == 'undetermined':
        return 'pending'
    if truth == 'unreadable':
        return 'agree' if p == 'refused' else 'judged_unreadable'
    if p == truth:
        return 'agree'
    if p == 'refused':
        return 'refused_readable'
    if p == 'breaking':
        noted = disclosed or any(f['outcome'] == 'note' and f['rule'] in DISCLOSED_NOTES for f in findings)
        return 'disclosed_conservative' if noted and set(product['rules']) <= DISCLOSED_RULES else 'conservative'
    return 'permissive'


def score(history: Path, population: Path, results: Path, adjudications: Path | None) -> dict:
    rows = {r['unit_id']: r for r in map(json.loads, gzip.open(results))}
    adjudicated = json.loads(adjudications.read_text())['units'] if adjudications else {}
    classes: dict[str, int] = {}
    listed: dict[str, list] = {}
    parity_failures, breaking_truth = [], 0
    for unit in units(population):
        product = rows[unit['unit_id']]
        if not product['parity']:
            parity_failures.append(unit['unit_id'])
        judged = wire_oracle.judge(*contents(history, unit))
        truth, findings, disclosed = judged['verdict'], judged['findings'], False
        if unit['unit_id'] in adjudicated:
            entry = adjudicated[unit['unit_id']]
            truth, disclosed = entry['truth'], entry.get('disclosed', False)
        kind = classify(product, truth, findings, disclosed)
        # A disagreement the oracle decided alone is adjudicated before it counts.
        if kind in ('conservative', 'permissive', 'refused_readable', 'judged_unreadable') and unit['unit_id'] not in adjudicated:
            kind = 'pending'
        breaking_truth += truth == 'breaking' and kind != 'pending'
        classes[kind] = classes.get(kind, 0) + 1
        if kind not in ('agree', 'disclosed_conservative'):
            listed.setdefault(kind, []).append(unit['unit_id'])
    judged_units = sum(classes.values()) - classes.get('pending', 0)
    undisclosed = classes.get('conservative', 0) + classes.get('refused_readable', 0)
    rate = undisclosed / judged_units if judged_units else 1.0
    checks = {
        'permissive': classes.get('permissive', 0) <= THRESHOLDS['permissive_max'],
        'judged_unreadable': classes.get('judged_unreadable', 0) <= THRESHOLDS['judged_unreadable_max'],
        'pending': classes.get('pending', 0) <= THRESHOLDS['pending_max'],
        'undisclosed_error_rate': rate <= THRESHOLDS['undisclosed_error_rate_max'],
        'breaking_truth': breaking_truth >= THRESHOLDS['breaking_truth_min'],
        'parity': not parity_failures,
    }
    return {'schema': SCHEMA, 'classes': dict(sorted(classes.items())), 'judged_units': judged_units,
            'breaking_truth': breaking_truth, 'undisclosed_error_rate': round(rate, 5),
            # Rule of three: with zero permissive among n real breaks, the 95% upper bound is about 3/n.
            'permissive_rate_upper_95': round(3 / breaking_truth, 5) if breaking_truth else None,
            'thresholds': THRESHOLDS, 'checks': checks, 'listed': listed,
            'parity_failures': parity_failures, 'passed': all(checks.values())}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest='command', required=True)
    r = sub.add_parser('run')
    r.add_argument('--binary', type=Path, required=True)
    r.add_argument('--helpers', type=Path, required=True)
    r.add_argument('--history', type=Path, required=True)
    r.add_argument('--population', type=Path, default=HERE / 'population')
    r.add_argument('--run-root', type=Path, required=True)
    s = sub.add_parser('score')
    s.add_argument('--history', type=Path, required=True)
    s.add_argument('--population', type=Path, default=HERE / 'population')
    s.add_argument('--results', type=Path, required=True)
    s.add_argument('--adjudications', type=Path)
    args = parser.parse_args()
    if args.command == 'run':
        out = run(args.binary.resolve(), args.helpers.resolve(), args.history, args.population, args.run_root.resolve())
    else:
        out = score(args.history, args.population, args.results, args.adjudications)
    print(json.dumps(out, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
