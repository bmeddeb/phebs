"""T47.2o: export run g's Caller Map predictions and bind a leaf-lane ledger.

The candidate population is what the product's exact Caller Map serves: the
complete caller-leaf publication, read through the live ``/api/contract_callers``
route for every declared operation and reconciled one-to-one with the
digest-verified publication records. The decision ledger comes from a
byte-identical replay of the leaf scan (``leafreplay``). Nothing here creates
labels, randomness, scores, a protocol seal or a publication.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import urllib.parse
import urllib.request
from pathlib import Path

import binding_bundle
import candidate_export
import capture_replay
import frames
import source_outcomes as outcomes

MAX_RESPONSE = 16 << 20
MAX_PAGES = 1000
API_PROTOCOL = 'protobuf'
LEDGER_BASIS = 'byte_identical_caller_leaf_replay_observed_ast_call'
PROOF_SLOT = 'sealed proof-bundle-v1 envelope identity and sha256 (precision, unresolved frames)'
LEDGER_SLOT = 'phebs complete candidate ledger identity and sha256 (abstention frame)'


def sha(raw: bytes) -> str:
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def repository_key(repository: str) -> str:
    return hashlib.sha256(repository.encode()).hexdigest()


def canonical(value) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()


class Api:
    """Bearer-authenticated reads of the operator's own live run; the key is never written."""

    def __init__(self, base: str, key: str):
        self.base, self.key = base, key

    def get(self, path: str, params: dict) -> dict:
        request = urllib.request.Request(self.base + path + '?' + urllib.parse.urlencode(params),
                                         headers={'Authorization': 'Bearer ' + self.key,
                                                  'Accept': 'application/json'})
        with urllib.request.urlopen(request, timeout=60) as response:
            raw = response.read(MAX_RESPONSE + 1)
        outcomes.require(len(raw) <= MAX_RESPONSE, 'API response exceeds bound')
        return json.loads(raw)


def run_config(run_root: Path) -> tuple[str, str, Path]:
    text = (run_root / 'phebs.yaml').read_text()
    addr = re.search(r'^\s*addr:\s*"?(127\.0\.0\.1:\d+)"?\s*$', text, re.M)
    key = re.search(r'^\s*api_key:\s*"?([^"\s]+)"?\s*$', text, re.M)
    data = re.search(r'^\s*data_dir:\s*"?([^"\n]+?)"?\s*$', text, re.M)
    outcomes.require(bool(addr and key and data), 'run config lacks loopback addr, api_key or data_dir')
    return 'http://' + addr.group(1), key.group(1), Path(data.group(1))


def generation_controls(api: Api, repositories: list[str]) -> list[dict]:
    selected = []
    for repository in repositories:
        progress = api.get('/api/caller-generation-progress', {'repository': repository})
        g = progress['generation']
        p = g.get('partition_progress') or {}
        outcomes.require(g['state'] == 'current' and g['plane'] == 'repository-overlay'
                         and g['repository'] == repository and p.get('state') == 'complete'
                         and p.get('refused_pair_count') == 0, 'caller generation is not complete and current')
        selected.append({k: g[k] for k in ('repository', 'commit', 'generation_digest', 'manifest_digest',
                                           'resolver_manifest_digest', 'result_count', 'abstention_count')})
    return selected


def declared_endpoints(data: Path, control: dict, generation_digest: str) -> list[tuple[str, str]]:
    """Every resolved grpc declaration_operation in the generation's exact resolver catalog."""
    root = data / 'resolver-catalogs'
    base = 'phebs-resolver-catalog-' + repository_key(control['repository'])
    manifest = json.loads((root / (base + '.manifest.json')).read_text())
    outcomes.require(manifest.get('authority_digest') == control['resolver_manifest_digest']
                     and manifest['identity']['generation_digest'] == generation_digest
                     and manifest['identity']['repository'] == control['repository'],
                     'resolver catalog differs from the caller generation')
    members = [m for m in manifest['members'] if m['name'] == 'grpc-generated-attribution-v2.ndjson']
    outcomes.require(len(members) == 1, 'resolver catalog lacks one grpc attribution member')
    path = root / f"{base}-v1-{generation_digest.removeprefix('sha256:')}-{members[0]['name']}"
    raw = path.read_bytes()
    outcomes.require(sha(raw) == members[0]['content_digest'], 'resolver member digest mismatch')
    endpoints = set()
    for line in raw.splitlines():
        record = json.loads(line)
        if record.get('kind') == 'declaration_operation' and record.get('protocol') == 'grpc' \
                and record.get('state') == 'resolved':
            endpoints.add((record['lineage'], '/' + record['operation']))
    return sorted(endpoints)


def publication_facts(data: Path, control: dict) -> tuple[dict, list[dict]]:
    """Fact-bearing records of the exact complete publication, digest-verified per pair."""
    overlay = data / 'caller-leaves' / ('phebs-caller-overlay-' + repository_key(control['repository']))
    name = (f"complete-v1-{control['generation_digest'].removeprefix('sha256:')}-"
            f"{control['manifest_digest'].removeprefix('sha256:')}.manifest.json")
    manifest = json.loads((overlay / name).read_text())
    outcomes.require(manifest['digest'] == control['manifest_digest']
                     and manifest['generation']['digest'] == control['generation_digest']
                     and manifest['generation']['repository'] == control['repository'],
                     'complete publication differs from the live generation')
    facts = []
    for pair in manifest['pairs']:
        receipt = pair['receipt']
        raw = (overlay / receipt['name']).read_bytes()
        outcomes.require(sha(raw) == receipt['content_digest'], 'leaf artifact digest mismatch')
        for line in raw.splitlines():
            record = json.loads(line)
            outcomes.require(record.get('coverage') is None, 'compact coverage hides call-site records')
            fact = record.get('fact')
            if not fact:
                continue
            a = fact['Assertion']
            path, span = a['Subject'].rsplit(':', 1)
            start, end = (int(v) for v in span.split('-'))
            outcomes.require(path == record['path'] == fact['Path'] and start == fact['Atom']['StartByte']
                             and end == fact['Atom']['EndByte'], 'leaf fact subject is inconsistent')
            facts.append({'path': path, 'start_byte': start, 'end_byte': end,
                          'start_line': fact['StartLine'], 'end_line': fact['EndLine'],
                          'predicate': a['Predicate'], 'object': a['Object'], 'lineage': a['Lineage'],
                          'tier': a['Tier'], 'code_role': a['CodeRole'], 'detail': a['Detail'],
                          'kind': record['kind'], 'reason': record.get('reason', '')})
    results = sum(f['kind'] == 'result' for f in facts)
    outcomes.require(results == control['result_count'] == manifest['aggregate']['result_count'],
                     'publication result count differs from the live generation')
    return manifest, facts


def export_rows(api: Api, control: dict, endpoints: list[tuple[str, str]]) -> list[dict]:
    rows = []
    for lineage, operation in endpoints:
        cursor, pages = '', 0
        while True:
            params = {'protocol': API_PROTOCOL, 'repository': control['repository'], 'lineage': lineage,
                      'operation': operation, 'resolution': 'any', 'page_size': 100}
            if cursor:
                params['cursor'] = cursor
            page = api.get('/api/contract_callers', params)
            g = page.get('generation') or {}
            outcomes.require(g.get('generation_digest') == control['generation_digest']
                             and g.get('manifest_digest') == control['manifest_digest'],
                             'Caller Map page is bound to a different generation')
            rows.extend({'endpoint': {'lineage': lineage, 'operation': operation}, **row} for row in page['rows'])
            pages += 1
            outcomes.require(pages <= MAX_PAGES, 'Caller Map pagination exceeds bound')
            if page['pagination']['complete']:
                break
            cursor = page['pagination']['next_cursor']
            outcomes.require(bool(cursor), 'incomplete Caller Map page has no cursor')
    return rows


def reconcile(rows: list[dict], facts: list[dict]) -> tuple[list[dict], list[dict]]:
    """Join served rows one-to-one to publication facts; unserved facts are returned, never dropped."""
    def key(path, start, end, predicate, operation):
        return (path, start, end, predicate, operation)
    by_key = {}
    for fact in facts:
        k = key(fact['path'], fact['start_byte'], fact['end_byte'], fact['predicate'], fact['object'])
        outcomes.require(k not in by_key, 'duplicate publication fact coordinate')
        by_key[k] = fact
    served = {}
    for row in rows:
        source = row['source']
        identity = source['assertion_id']
        # A citation is a request-bound, five-minute token, not record identity.
        body = {**{k: v for k, v in row.items() if k != 'endpoint'},
                'source': {k: v for k, v in source.items() if k != 'citation'}}
        outcomes.require(served.get(identity, (body,))[0] == body, 'one Caller Map record served inconsistently')
        if identity in served:
            continue
        predicate = {'resolved_caller': 'CALLS_OPERATION', 'extractor_abstention': 'UNRESOLVED_CALLER'}.get(row['classification'])
        fact = by_key.get(key(source['path'], source['start_byte'], source['end_byte'], predicate, row['operation']))
        outcomes.require(fact is not None, 'served Caller Map row is absent from the publication')
        outcomes.require((row.get('declaration_lineage', '') or '') == (fact['lineage'] if predicate == 'CALLS_OPERATION' else '')
                         and row['tier'] == fact['tier'] and row.get('code_role', '') == fact['code_role'],
                         'served Caller Map row disagrees with its publication fact')
        if predicate == 'CALLS_OPERATION':
            outcomes.require(row['declaration_lineage'] == row['endpoint']['lineage'], 'resolved row served under another lineage')
        served[identity] = (body, fact)
    matched = [{**fact, 'assertion_id': identity} for identity, (_, fact) in served.items()]
    seen = {key(m['path'], m['start_byte'], m['end_byte'], m['predicate'], m['object']) for m in matched}
    outcomes.require(len(seen) == len(matched), 'two served records share one publication fact')
    unserved = [f for k, f in by_key.items() if k not in seen]
    return matched, unserved


def projector_envelope(controls: list[dict], matched: dict, aliases: dict) -> dict:
    """Adapt served facts to the frozen projector's envelope shape, on census repository names."""
    assertions, evidence = [], []
    for control in controls:
        repo, run = aliases[control['repository']], control['generation_digest']
        for fact in matched[control['repository']]:
            atom = fact['assertion_id']
            assertions.append({'id': atom, 'predicate': fact['predicate'], 'subject': f"{fact['path']}:{fact['start_byte']}-{fact['end_byte']}",
                               'object': fact['object'], 'lineage': fact['lineage'], 'tier': fact['tier'],
                               'code_role': fact['code_role'], 'detail': fact['detail'], 'repo': repo,
                               'run_id': run, 'supporting': [atom]})
            evidence.append({'repository': repo, 'run_id': run,
                             'atom': {'id': atom, 'start_byte': fact['start_byte'], 'end_byte': fact['end_byte']},
                             'occurrences': [{'repo': repo, 'run_id': run, 'commit': control['commit'], 'path': fact['path'],
                                              'start_line': fact['start_line'], 'end_line': fact['end_line']}]})
    return {'bundle': {'assertions': assertions, 'evidence': evidence}}


def unmatched_trace(universe: dict, trace: list[dict], aliases: dict) -> int:
    spans = {(s['repository'], s['path'], a, b) for s in universe['sites'] if frames.recall_eligibility(s)[0]
             for a, b in s['citation_spans']}
    return sum((aliases[e['repository']], e['path'], e['start_byte'], e['end_byte']) not in spans for e in trace)


def write_private(root: Path, name: str, value, jsonl: bool = False) -> str:
    path = root / name
    with open(path, 'xb') as f:
        if jsonl:
            f.write(b''.join(canonical(row) + b'\n' for row in value))
        else:
            f.write(json.dumps(value, indent=1, sort_keys=True).encode() + b'\n')
    os.chmod(path, 0o600)
    return sha(path.read_bytes())


def run(repo_root: Path, run_root: Path, private: Path, aliases_path: Path, receipt_path: Path, bundle_path: Path) -> dict:
    spike = repo_root / 'spike' / 't472'
    base, key, data = run_config(run_root)
    aliases = json.loads(aliases_path.read_text())
    api = Api(base, key)
    private.mkdir(mode=0o700)
    before = generation_controls(api, sorted(aliases))
    digests = {'generation-controls.json': write_private(private, 'generation-controls.json', before)}
    matched, unserved, endpoint_count, rows_total = {}, [], 0, 0
    for control in before:
        manifest, facts = publication_facts(data, control)
        endpoints = declared_endpoints(data, control, manifest['generation']['resolver_generation_digest'])
        rows = export_rows(api, control, endpoints)
        served, missing = reconcile(rows, facts)
        matched[control['repository']] = served
        unserved += [{'repository': control['repository'], **f} for f in missing]
        endpoint_count += len(endpoints)
        rows_total += len(rows)
    export = {'schema': 't472-caller-map-export-v1', 'controls': before, 'served': matched, 'unserved': unserved}
    digests['caller-map-export.json'] = write_private(private, 'caller-map-export.json', export)

    overlay = private / 'overlay'
    overlay.mkdir(mode=0o700)
    overlay_hashes = capture_replay.prepare_leaf_overlay(repo_root, overlay)
    replay = private / 'leaf-replay'
    subprocess.run(['go', 'run', '-tags', 't472replay', '-overlay', str(overlay / 'overlay.json'), './spike/t472/leafreplay',
                    '-data', str(data), '-controls', str(private / 'generation-controls.json'), '-out', str(replay)],
                   cwd=repo_root, check=True, env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'})
    for name in ('leaf-trace.jsonl', 'leaf-replay-summary.json'):
        digests['leaf-replay/' + name] = sha((replay / name).read_bytes())
    after = generation_controls(api, sorted(aliases))
    outcomes.require(after == before, 'Caller Map generation changed during export')

    universe = frames.load_universe(spike / 'universe')
    projected = frames.project_candidate_frames(projector_envelope(before, matched, aliases), universe)
    trace = [json.loads(line) for line in (replay / 'leaf-trace.jsonl').read_text().splitlines()]
    outcomes.require(unmatched_trace(universe, trace, aliases) == 0, 'leaf visit outside the source universe')
    ledger = candidate_export.decision_ledger(universe, projected, trace, aliases, basis=LEDGER_BASIS)
    digests['candidate-frames.json'] = write_private(private, 'candidate-frames.json', projected)
    digests['call-site-decisions.jsonl'] = write_private(private, 'call-site-decisions.jsonl', ledger, jsonl=True)

    states = {}
    for row in ledger:
        states[row['state']] = states.get(row['state'], 0) + 1
    template = json.loads((spike / 'bundle' / 'preregistration.run-g.bundle.json').read_text())
    bundle = json.loads(json.dumps(template))
    bundle['candidate_identities'][PROOF_SLOT] = {
        'identity': 't472-caller-map-v2-export:' + digests['caller-map-export.json'].removeprefix('sha256:'),
        'sha256': digests['caller-map-export.json']}
    bundle['candidate_identities'][LEDGER_SLOT] = {
        'identity': 't472-callsite-decision-ledger-v2:caller-leaf-replay:20261011g',
        'sha256': digests['call-site-decisions.jsonl']}
    bundle.pop('missing_bindings')
    bundle['missing_bindings'] = binding_bundle.validate_bundle(bundle)
    bundle_path.write_text(json.dumps(bundle, indent=1, sort_keys=True) + '\n')
    receipt = {
        'schema': 't472-caller-map-bindings-v1', 'round': 't472-candidate-run-20261011g',
        'supersedes': 'spike/t472/candidate_bindings_20261011g.json (T47.2n extraction base lane)',
        'private_root': str(private), 'generation_controls': before, 'controls_unchanged_after_export': True,
        'declared_endpoints_queried': endpoint_count, 'served_rows_including_repeats': rows_total,
        'served_records': sum(map(len, matched.values())), 'unserved_publication_facts': len(unserved),
        'frame_populations': {name: frame['population'] for name, frame in projected.items()},
        'decision_ledger_population': len(ledger), 'decision_ledger_states': dict(sorted(states.items())),
        'leaf_replay': 'every scanned file reproduces its retained facts byte-for-byte; per-pair reads and bytes equal the receipts',
        'leaf_visits': len(trace), 'leaf_visits_outside_universe': 0,
        'overlay_sha256': overlay_hashes, 'artifacts_sha256': digests,
        'recipe_sha256': {p: sha((repo_root / p).read_bytes()) for p in (
            'spike/t472/caller_map_export.py', 'spike/t472/capture_replay.py',
            'spike/t472/candidate_export.py', 'spike/t472/leafreplay/main.go')},
        'proof_slot_note': ('The bundle slot names a sealed proof-bundle-v1 envelope; Caller Map serves no proof '
                            'bundle for partitioned caller publications, so the slot binds the product-served '
                            'caller-map-v2 export reconciled to its complete publication.'),
        'sample_drawn': False, 'labels_produced': False, 'predictions_disclosed': False,
        'quality_scored': False, 'protocol_sealed': False, 'external_publication': False, 'acceptance_met': False,
    }
    receipt_path.write_text(json.dumps(receipt, indent=1, sort_keys=True) + '\n')
    return receipt


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--repo-root', type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument('--run-root', type=Path, required=True)
    parser.add_argument('--private-root', type=Path, required=True)
    parser.add_argument('--aliases', type=Path, required=True)
    parser.add_argument('--receipt', type=Path, required=True)
    parser.add_argument('--bundle', type=Path, required=True)
    args = parser.parse_args()
    receipt = run(args.repo_root.resolve(), args.run_root, args.private_root, args.aliases, args.receipt, args.bundle)
    print(json.dumps({k: receipt[k] for k in ('served_records', 'unserved_publication_facts', 'frame_populations',
                                              'decision_ledger_states')}, sort_keys=True))


if __name__ == '__main__':
    main()
