"""T58.3 identity scorer: grpcgo rows against the go/types oracle.

Precision: grpcgo's resolved rows (CALLS_OPERATION, REGISTERS_GRPC_SERVICE) in
files the oracle type-checked, excluding rows at oracle-"external" sites; a
row is correct when the oracle resolves the same call-expression span to the
same identity. Recall: oracle "resolved" sites; a site is found when grpcgo
emits a resolved row at its span with the same identity. Oracle "ambiguous"
and "external" sites are reported, not scored."""
from __future__ import annotations

import argparse
import collections
import json
from pathlib import Path

RESOLVED = {'CALLS_OPERATION': 'call', 'REGISTERS_GRPC_SERVICE': 'registration'}


def score(oracle_rows: list[dict], candidate_rows: list[dict]) -> tuple[dict, list]:
    loaded = {(r['repository'], f) for r in oracle_rows if r.get('summary') for f in r.get('loaded') or []}
    sites = {(r['repository'], r['path'], r['start_byte'], r['end_byte']): r for r in oracle_rows if not r.get('summary')}
    rows = {(r['repository'], r['path'], r['start_byte'], r['end_byte']): r for r in candidate_rows}
    per = collections.defaultdict(collections.Counter)
    out = []

    def bump(repo, key):
        per[repo][key] += 1
        per['*'][key] += 1

    for key, row in rows.items():
        repo = key[0]
        if row['predicate'] not in RESOLVED:
            continue
        if (repo, key[1]) not in loaded:
            bump(repo, 'rows_unloaded_file')
            continue
        site = sites.get(key)
        if site and site['state'] == 'external':
            bump(repo, 'rows_at_external_sites')
            continue
        bump(repo, 'precision_den')
        identity = row['object']
        if site and site['state'] == 'resolved' and site['kind'] == RESOLVED[row['predicate']] and site['identity'] == identity:
            bump(repo, 'precision_num')
        else:
            out.append({'kind': 'wrong_row', 'repository': repo, 'path': key[1], 'start_byte': key[2], 'end_byte': key[3],
                        'phebs': [row['predicate'], identity], 'oracle': site and [site['kind'], site['state'], site.get('identity'), site.get('candidates')]})
    for key, site in sites.items():
        repo = key[0]
        bump(repo, f"oracle_{site['state']}")
        if site['state'] != 'resolved':
            continue
        bump(repo, 'recall_den')
        row = rows.get(key)
        if row and row['predicate'] in RESOLVED and RESOLVED[row['predicate']] == site['kind'] and row['object'] == site['identity']:
            bump(repo, 'recall_num')
        else:
            out.append({'kind': 'missed_site', 'repository': repo, 'path': key[1], 'start_byte': key[2], 'end_byte': key[3],
                        'oracle': [site['kind'], site['identity']], 'phebs': row and [row['predicate'], row['object']]})
    result = {}
    for repo, c in per.items():
        result[repo] = {**c, 'precision': round(c['precision_num'] / c['precision_den'], 6) if c['precision_den'] else None,
                        'recall': round(c['recall_num'] / c['recall_den'], 6) if c['recall_den'] else None}
    return result, out


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for flag in ('--oracle', '--candidate', '--disagreements'):
        parser.add_argument(flag, type=Path, required=True)
    args = parser.parse_args()
    load = lambda p: [json.loads(l) for l in p.read_text().splitlines() if l.strip()]
    result, out = score(load(args.oracle), load(args.candidate))
    args.disagreements.write_text(''.join(json.dumps(d, sort_keys=True) + '\n' for d in out))
    print(json.dumps(result, indent=1, sort_keys=True))
