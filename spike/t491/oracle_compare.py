"""T49.1b machine-oracle comparison and scoring (run only after the seal).

The independent enumerator is the oracle for the whole population. phebs's
exported declaration facts and their type-reference states are compared with
it key by key. Every disagreement is then adjudicated by Ben from source, and
Ben separately labels a seeded sample of oracle claims, so the oracle itself is
validated. The pass rule is the one Ben approved on 2026-10-12:

- declaration agreement of at least 99.5%;
- reference-state agreement of at least 99.5%;
- oracle-sample correctness of at least 99%;
- every disagreement adjudicated, with none left `unsure`.

Post-adjudication emission precision, declaration recall and
gap-classification correctness are reported alongside. Wilson intervals come
from the frozen pilot harness.
"""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'pilot' / 'validation'))
import harness  # noqa: E402

THRESHOLDS = {'declaration_agreement': 0.995, 'reference_agreement': 0.995, 'oracle_sample_correct': 0.99}
VERDICTS = {'oracle', 'candidate', 'neither', 'unsure'}
CANDIDATE_SLOTS = {'DECLARES_FIELD': ('type', 'map.key', 'map.value'), 'DECLARES_OPERATION': ('request', 'response'),
                   'DECLARES_EXTENSION': ('extendee', 'type')}


class CompareError(ValueError):
    """Inputs are inconsistent, or the round cannot be scored."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise CompareError(message)


def candidate_index(export: dict, aliases: dict) -> tuple[dict, dict]:
    """(declaration key -> fact, reference key -> (resolution, reason)) on census repository names."""
    facts, refs = {}, {}
    for fact in export['facts']:
        repo = aliases[fact['repo']]
        key = (repo, fact['subject'], fact['predicate'], fact['object'])
        require(key not in facts, 'duplicate candidate declaration key')
        facts[key] = fact
        detail = json.loads(fact['detail'] or '{}')
        slots = {'type': detail.get('type'), 'request': detail.get('request'), 'response': detail.get('response'),
                 'extendee': detail.get('extendee')}
        if isinstance(detail.get('map'), dict):
            slots['map.key'], slots['map.value'] = detail['map'].get('key'), detail['map'].get('value')
        for slot in CANDIDATE_SLOTS.get(fact['predicate'], ()):
            ref = slots.get(slot)
            if isinstance(ref, dict):
                refs[(repo, fact['subject'], fact['object'], slot)] = (ref.get('resolution', ''), ref.get('reason', '') or '')
    return facts, refs


def oracle_index(declarations: list[dict], references: list[dict]) -> tuple[set, dict]:
    keys = {(d['repository'], d['path'], d['predicate'], d['object']) for d in declarations}
    require(len(keys) == len(declarations), 'duplicate oracle declaration key')
    refs = {(r['repository'], r['path'], r['object'], r['slot']): (r['resolution'], r['reason'] or '') for r in references}
    return keys, refs


def disagreement_id(kind: str, key: tuple) -> str:
    return 'sha256:' + hashlib.sha256(json.dumps([kind, *key]).encode()).hexdigest()


def compare(oracle_keys: set, oracle_refs: dict, candidate_facts: dict, candidate_refs: dict) -> dict:
    candidate_keys = set(candidate_facts)
    agree = oracle_keys & candidate_keys
    union = oracle_keys | candidate_keys
    disagreements = [{'id': disagreement_id('oracle_only', k), 'kind': 'oracle_only', 'key': list(k)}
                     for k in sorted(oracle_keys - candidate_keys)]
    disagreements += [{'id': disagreement_id('candidate_only', k), 'kind': 'candidate_only', 'key': list(k)}
                      for k in sorted(candidate_keys - oracle_keys)]
    agreed_objects = {(k[0], k[1], k[3]) for k in agree}
    ref_keys = {k for k in set(oracle_refs) | set(candidate_refs) if (k[0], k[1], k[2]) in agreed_objects}
    same = 0
    for key in sorted(ref_keys):
        left, right = oracle_refs.get(key), candidate_refs.get(key)
        if left == right:
            same += 1
        else:
            disagreements.append({'id': disagreement_id('reference_state', key), 'kind': 'reference_state', 'key': list(key),
                                  'oracle': list(left) if left else None, 'candidate': list(right) if right else None})
    return {'declarations': {'oracle': len(oracle_keys), 'candidate': len(candidate_keys), 'agree': len(agree),
                             'union': len(union), 'agreement': len(agree) / len(union) if union else None},
            'references': {'compared': len(ref_keys), 'same': same,
                           'agreement': same / len(ref_keys) if ref_keys else None},
            'disagreements': disagreements}


def score(comparison: dict, adjudications: dict[str, str], sample_labels: list[dict]) -> dict:
    """Apply Ben's adjudications and oracle-sample labels; refuse anything incomplete."""
    ids = {d['id'] for d in comparison['disagreements']}
    require(set(adjudications) == ids, 'every disagreement needs exactly one adjudication')
    require(set(adjudications.values()) <= VERDICTS, 'unknown adjudication verdict')
    unresolved = sorted(i for i, v in adjudications.items() if v == 'unsure')
    correct = sum(1 for row in sample_labels if row['correct'] == 'yes')
    wrong = sum(1 for row in sample_labels if row['correct'] == 'no')
    require(all(row['correct'] in ('yes', 'no', 'unsure') for row in sample_labels), 'unknown sample label')
    sample_n = correct + wrong
    sample_rate = correct / sample_n if sample_n else None
    decl, refs = comparison['declarations'], comparison['references']
    kinds = {d['id']: d['kind'] for d in comparison['disagreements']}
    true_oracle_only = sum(1 for i, v in adjudications.items() if kinds[i] == 'oracle_only' and v == 'oracle')
    true_candidate_only = sum(1 for i, v in adjudications.items() if kinds[i] == 'candidate_only' and v == 'candidate')
    candidate_correct_refs = sum(1 for i, v in adjudications.items() if kinds[i] == 'reference_state' and v == 'candidate')
    truth = decl['agree'] + true_oracle_only + true_candidate_only
    emitted_true = decl['agree'] + true_candidate_only
    results = {
        'declaration_agreement': decl['agreement'], 'reference_agreement': refs['agreement'],
        'oracle_sample_correct': sample_rate,
        'oracle_sample_wilson': list(harness.wilson_interval(correct, sample_n)) if sample_n else None,
        'oracle_sample_unsure': sum(1 for row in sample_labels if row['correct'] == 'unsure'),
        'emission_precision': emitted_true / decl['candidate'] if decl['candidate'] else None,
        'declaration_recall': emitted_true / truth if truth else None,
        'gap_classification_correct': (refs['same'] + candidate_correct_refs) / refs['compared'] if refs['compared'] else None,
        'unresolved_adjudications': unresolved,
    }
    measured = all(results[k] is not None for k in THRESHOLDS) and not unresolved
    results['outcome'] = 'not_measured' if not measured else (
        'pass' if all(results[k] >= v for k, v in THRESHOLDS.items()) else 'fail')
    return results
