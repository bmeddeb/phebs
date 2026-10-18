#!/usr/bin/env python3
"""Offline diagnosis of retained first-attempt output; no product reads."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path

import score

HERE = Path(__file__).resolve().parent


def sha(path):
    return 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--capture-root', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    labels = json.loads((HERE / 'labels.json').read_text())
    binding = json.loads((HERE / 'committed-inputs.json').read_text())
    capture = args.capture_root
    paths = [capture / 'raw' / f'http-{n:03}.json' for n in range(16, 21)]
    pages = [json.loads(path.read_bytes()) for path in paths]
    truth = {site['site_id']: site for site in labels['sites'] if site['pair_side'] != 'outside_pair'}
    files = {f['path']: f for f in binding['files']}
    rows = [row for page in pages for row in page['rows']]
    observed, issues = set(), []
    for page in pages:
        for side in ('old', 'replacement'):
            generation = page[side]['generation']
            score.require(generation['state'] == 'current' and generation['repository'] == binding['roles'][0]['repository'] and generation['commit'] == binding['derived_input_commit'], 'capture authority differs')
    for row in rows:
        for side in ('old', 'replacement'):
            for item in row[side]['rows']:
                source = item['source']; key = score.site_id(source); expected = truth[key]
                score.require(key not in observed and expected['pair_side'] == side and item['operation'] == expected['wire_operation'], 'wrong source membership/side')
                score.require(source['repository'] == binding['roles'][0]['repository'] and source['commit'] == binding['derived_input_commit'], 'wrong source revision')
                score.require(source['object_id'] == files[source['path']]['git_blob'] and source['blob_digest'] == expected['upstream_blob_sha256'], 'wrong source bytes')
                score.require((source['start_line'], source['end_line']) == (expected['start_line'], expected['end_line']), 'wrong source lines')
                score.require(item['classification'] == 'resolved_caller' and item['resolution'] == 'syntax' and item['tier'] == 'heuristic' and item['fresh'] is True, 'unexpected source resolution/tier')
                observed.add(key)
                issues.append({'site_id': key, 'expected_unit': expected['unit'], 'actual_unit': item['unit']})
    score.require(observed == set(truth) and Counter(score.row_signature(r) for r in rows) == score.expected_signatures(labels, 'occurrence'), 'wrong full occurrence classes/membership')
    source_outcomes = []
    for path in sorted((capture / 'source-outcomes').glob('*.jsonl')):
        lines = [json.loads(line) for line in path.read_text().splitlines()]
        header = lines[0]
        if header['repository'] == binding['roles'][0]['repository'] and any('path' in row for row in lines[1:]):
            score.require(header['commit'] == binding['derived_input_commit'], 'crossed outcome revision')
            source_outcomes.extend(row for row in lines[1:] if 'path' in row)
    go_files = {path: value for path, value in files.items() if path.endswith('.go')}
    score.require(len(source_outcomes) == len(go_files) == 9, 'source outcome census differs')
    score.require({r['path'] for r in source_outcomes} == set(go_files), 'source outcome paths differ')
    for row in source_outcomes:
        score.require(row['state'] == 'analyzed' and row['reason'] == 'source_scan_complete' and row['blob_digest'] == 'sha256:' + go_files[row['path']]['sha256'], 'partial/failed/mismatched source outcome')
    report = {
        'schema': 'phebs-t503-first-capture-diagnosis-v1', 'date': '2026-10-18',
        'outcome': 'real_unit_attribution_mismatch_no_release_or_overall_pass',
        'new_product_reads': 0, 'first_result_digest': sha(HERE / 'baseline-result.json'),
        'first_attempt_status': 'stopped_no_retry; unchanged',
        'qualification': 'Offline diagnosis of retained actual first output. It does not complete the interrupted native matrix or establish unit-composition/citation-open/transport/UI/concurrency/restart quality.',
        'observed_metrics': {
            'selected_occurrence_membership_precision': score.exact_metric(5),
            'selected_occurrence_recall': score.exact_metric(5),
            'occurrence_classification': {'metric': score.exact_metric(5), 'actual_classes': dict(Counter(r['classification'] for r in rows))},
            'outside_pair_exclusion': score.exact_metric(4),
            'source_tuple_integrity': score.exact_metric(5),
            'source_processing_accounted': {'metric': score.exact_metric(9), 'rows': source_outcomes},
            'unit_attribution': {'numerator': 0, 'denominator': 5, 'observed_rate': 0, 'wilson95': score.wilson(0,5), 'issues': issues},
            'unit_composition': {'status': 'not measured; run stopped before unit query'},
            'citation_opening': {'status': 'not measured; run stopped before immutable source reads'},
            'resolution_tiers': {'syntax_heuristic': 5, 'scip_derived': 0},
        },
        'cause': {'file': 'internal/callerexecute/execute.go', 'boundary': 'ExecutePair -> gocaller.ScanDirectSyntaxFile', 'finding': 'DirectScanRequest.ConsumerUnits is omitted, so directAttribution returns consumer_unit_projection_unavailable for every source. Exact public reads preserve this unavailable metadata; committed legacy extraction facts do not populate this separate exact overlay.'},
        'next_gate': 'Repair exact committed unit projection with an explicit version/admission/authority/cost review and focused regression. Freeze repaired implementation and independently review prospective continuation before any new product execution. Labels, sources, thresholds and first failure remain exact.',
    }
    args.out.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'outcome': report['outcome'], 'unit_attribution': '0/5', 'source_accounting': '9/9', 'new_product_reads': 0}))


if __name__ == '__main__':
    main()
