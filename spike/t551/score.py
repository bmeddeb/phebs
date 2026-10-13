"""T55.1: score the kafka-producer extractor against the independent site oracle.

Join key: (unit_id, start_byte, end_byte) of the Topic expression. Each oracle
site with a Topic field is a scored site:

- covered: the extractor emits exactly one row at that span;
- class agreement: a resolved site must be PRODUCES_TO_TOPIC with the same
  object, and an unresolved site must be UNRESOLVED_KAFKA_PRODUCER carrying the
  same shape class.

Extractor rows at no oracle span are `extra`. Oracle `no_topic_field` sites are
reported, not scored, because the card makes no claim about them.
`parse_error` units are compared against KAFKA_EXTRACTION_GAP rows. Every
disagreement must be adjudicated (`adjudications.json`: key → {truth, note})
before the round scores.
"""
from __future__ import annotations

import argparse
import gzip
import json
from pathlib import Path

SCHEMA = 't551-producer-score-v1'
# Frozen by Ben on 2026-10-13 (docs/KAFKA_PRODUCER_QUALITY_PROTOCOL.md §6).
THRESHOLDS = {
    'coverage_min': 0.99,          # scored sites that receive exactly one row
    'class_agreement_min': 0.99,   # covered sites whose state, object or shape class is right
    'wrong_topic_max': 0,          # a resolved topic that differs from the truth fails the round
    'extra_rate_max': 0.01,        # extractor rows at no real producer site, after adjudication
    'pending_max': 0,
    'scored_sites_min': 300,
}


def load(path: Path) -> list[dict]:
    opener = gzip.open if path.suffix == '.gz' else open
    with opener(path, 'rt') as f:
        return [json.loads(line) for line in f if line.strip()]


def candidate_state(row: dict) -> tuple[str, str | None]:
    """The T23.2 contract: evidence objects are `topic:<literal>`, abstention
    objects `unresolved:<shape-class>` from the frozen six-class vocabulary."""
    if row['predicate'] == 'PRODUCES_TO_TOPIC':
        return 'resolved', row['object']
    if row['predicate'] == 'UNRESOLVED_KAFKA_PRODUCER':
        return 'unresolved', row['object'].removeprefix('unresolved:')
    return 'gap', None


def site_key(row: dict) -> str:
    return f"{row['unit_id']}:{row['start_byte']}-{row['end_byte']}"


def score(sites: list[dict], rows: list[dict], adjudications: dict) -> dict:
    by_span: dict[str, list[dict]] = {}
    for row in rows:
        if row['predicate'] != 'KAFKA_EXTRACTION_GAP':
            by_span.setdefault(site_key(row), []).append(row)
    classes = {'covered_agree': 0, 'covered_disagree': 0, 'missed': 0, 'duplicate': 0}
    disagreements, wrong_topic, scored, claimed = [], [], 0, set()
    for site in sites:
        if site['state'] in ('no_topic_field', 'parse_error'):
            continue
        scored += 1
        key = site_key(site)
        claimed.add(key)
        found = by_span.get(key, [])
        truth = adjudications.get(key, {}).get('truth')
        want = (site['state'], site['object'] if site['state'] == 'resolved' else site['class'])
        if truth:
            want = tuple(truth)
        if not found:
            kind = 'missed'
        elif len(found) > 1:
            kind = 'duplicate'
        elif candidate_state(found[0]) == want:
            kind = 'covered_agree'
        else:
            kind = 'covered_disagree'
            if want[0] == 'resolved' and candidate_state(found[0])[0] == 'resolved':
                wrong_topic.append(key)
        classes[kind] += 1
        if kind != 'covered_agree' and key not in adjudications:
            disagreements.append(key)
    extra = [k for k in by_span if k not in claimed]
    extra_unadjudicated = [k for k in extra if adjudications.get(k, {}).get('truth') is None]
    extra_confirmed = [k for k in extra if adjudications.get(k, {}).get('truth') == ['not_a_site', None]]
    covered = classes['covered_agree'] + classes['covered_disagree']
    coverage = covered / scored if scored else 0.0
    agreement = classes['covered_agree'] / covered if covered else 0.0
    extra_rate = len(extra_confirmed) / max(1, scored)
    pending = disagreements + extra_unadjudicated
    checks = {
        'coverage': coverage >= THRESHOLDS['coverage_min'],
        'class_agreement': agreement >= THRESHOLDS['class_agreement_min'],
        'wrong_topic': len(wrong_topic) <= THRESHOLDS['wrong_topic_max'],
        'extra_rate': extra_rate <= THRESHOLDS['extra_rate_max'],
        'pending': len(pending) <= THRESHOLDS['pending_max'],
        'scored_sites': scored >= THRESHOLDS['scored_sites_min'],
    }
    return {'schema': SCHEMA, 'scored_sites': scored, 'classes': classes, 'coverage': round(coverage, 5),
            'class_agreement': round(agreement, 5), 'extra': len(extra), 'extra_confirmed': len(extra_confirmed),
            'wrong_topic': wrong_topic, 'pending': pending, 'thresholds': THRESHOLDS, 'checks': checks,
            'passed': all(checks.values())}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--sites', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--adjudications', type=Path)
    args = parser.parse_args()
    adjudications = json.loads(args.adjudications.read_text())['units'] if args.adjudications else {}
    print(json.dumps(score(load(args.sites), load(args.candidate), adjudications), indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
