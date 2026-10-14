"""T49.2 scorer (protocol sections 5-6): oracle JSON lines vs candidate export
JSON lines for the locked population. Prints the measured result and writes
every disagreement for adjudication."""
from __future__ import annotations

import argparse
import collections
import json
from pathlib import Path


def role_of(path: str) -> str:
    parts = path.split('/')[:-1]
    if 'vendor' in parts:
        return 'vendor'
    if {'test', 'tests', 'testdata'} & set(parts):
        return 'test'
    return 'production'


def phebs_state(ref: dict) -> str:
    resolution = ref.get('resolution')
    if resolution == 'intrinsic':
        return 'intrinsic'
    if resolution == 'same_file':
        return f"same_file:{ref.get('kind')}:{ref.get('declaration')}"
    reason = ref.get('reason', '')
    return {'IMPORT_LINKING_UNAVAILABLE': 'unresolved:include',
            'NESTED_CONTAINER_SHAPE': 'unresolved:nested'}.get(reason, f'unresolved:{reason}')


def phebs_shape(detail: dict) -> dict:
    if detail.get('map'):
        refs = [phebs_state(detail['map']['key']), phebs_state(detail['map']['value'])]
    else:
        refs = [phebs_state(detail.get('type') or {})]
    return {'name': detail.get('name'), 'cardinality': detail.get('cardinality'), 'refs': refs}


def load(path: Path) -> dict:
    return {(r['repository'], r['path']): r for r in map(json.loads, path.read_text().splitlines())}


def score(oracle: dict, candidate: dict) -> tuple[dict, list]:
    strata = collections.Counter()
    by_role = collections.defaultdict(lambda: collections.Counter())
    disagreements = []
    for key in sorted(set(oracle) | set(candidate)):
        repo, path = key
        o, c = oracle.get(key), candidate.get(key)
        role = role_of(path)
        if o is None or c is None:
            strata['unpaired'] += 1
            disagreements.append({'kind': 'unpaired_file', 'repository': repo, 'path': path})
            continue
        facts = c['facts']
        gaps = [f['object'] for f in facts if f['predicate'] == 'THRIFT_EXTRACTION_GAP']
        if o['status'] != 'ok':
            behaviour = 'error' if c.get('error') else (f'gap:{gaps[0]}' if gaps else f'{len(facts)} facts')
            strata['oracle_rejected'] += 1
            disagreements.append({'kind': 'oracle_rejected', 'repository': repo, 'path': path,
                                  'oracle_error': o['error'], 'phebs': behaviour})
            continue
        oracle_decls = {tuple(d) for d in o['declarations']}
        if 'implicit_field_id' in gaps:
            strata['phebs_d2_gap'] += 1
            continue
        if c.get('error') or gaps:
            stratum = 'phebs_error' if c.get('error') else 'phebs_other_gap'
            strata[stratum] += 1
            for counter in (strata, by_role[role]):
                counter['union'] += len(oracle_decls)
            disagreements.append({'kind': stratum, 'repository': repo, 'path': path,
                                  'phebs': c.get('error') or gaps[0], 'missed': len(oracle_decls)})
            continue
        strata['scored'] += 1
        phebs_decls = {(f['predicate'], f['object']) for f in facts}
        both = oracle_decls & phebs_decls
        for counter in (strata, by_role[role]):
            counter['agree'] += len(both)
            counter['union'] += len(oracle_decls | phebs_decls)
        for pred, obj in sorted(oracle_decls - phebs_decls):
            disagreements.append({'kind': 'oracle_only', 'repository': repo, 'path': path, 'predicate': pred, 'object': obj})
        for pred, obj in sorted(phebs_decls - oracle_decls):
            disagreements.append({'kind': 'phebs_only', 'repository': repo, 'path': path, 'predicate': pred, 'object': obj})
        phebs_fields = {f['object']: phebs_shape(json.loads(f['detail'])) for f in facts if f['predicate'] == 'DECLARES_FIELD'}
        for obj, shape in sorted(o['fields'].items()):
            for counter in (strata, by_role[role]):
                counter['oracle_fields'] += 1
            mine = phebs_fields.get(obj)
            if mine is not None and mine['name'] == shape['name']:
                for counter in (strata, by_role[role]):
                    counter['field_id_agree'] += 1
            elif mine is not None:
                disagreements.append({'kind': 'field_id', 'repository': repo, 'path': path, 'object': obj,
                                      'oracle_name': shape['name'], 'phebs_name': mine['name']})
            if mine is None:
                continue
            for counter in (strata, by_role[role]):
                counter['shared_fields'] += 1
            if mine['cardinality'] == shape['cardinality'] and mine['refs'] == shape['refs']:
                for counter in (strata, by_role[role]):
                    counter['ref_agree'] += 1
            else:
                disagreements.append({'kind': 'reference_state', 'repository': repo, 'path': path, 'object': obj,
                                      'oracle': shape, 'phebs': mine})
    return {'strata': dict(strata), 'by_role': {k: dict(v) for k, v in by_role.items()}}, disagreements


def ratio(n: int, d: int) -> float | None:
    return round(n / d, 6) if d else None


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--oracle', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--disagreements', type=Path, required=True)
    args = parser.parse_args()
    result, disagreements = score(load(args.oracle), load(args.candidate))
    s = result['strata']
    result['measures'] = {
        'declaration_agreement': ratio(s.get('agree', 0), s.get('union', 0)),
        'field_id_agreement': ratio(s.get('field_id_agree', 0), s.get('oracle_fields', 0)),
        'reference_state_agreement': ratio(s.get('ref_agree', 0), s.get('shared_fields', 0)),
    }
    result['disagreement_kinds'] = dict(collections.Counter(d['kind'] for d in disagreements))
    args.disagreements.write_text(''.join(json.dumps(d, sort_keys=True) + '\n' for d in disagreements))
    print(json.dumps(result, indent=1, sort_keys=True))
