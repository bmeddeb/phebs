"""Synthetic fences for the T47.2o Caller Map export; no retained predictions read."""
import copy
import hashlib
import json
import tempfile
import unittest
from pathlib import Path

import binding_bundle
import caller_map_export as export
import capture_replay
import frames

ROOT = Path(__file__).parent


def fact(path='a.go', start=10, end=20, predicate='CALLS_OPERATION', operation='/pkg.Svc/M', lineage='L'):
    return {'path': path, 'start_byte': start, 'end_byte': end, 'start_line': 1, 'end_line': 1,
            'predicate': predicate, 'object': operation,
            'lineage': lineage if predicate == 'CALLS_OPERATION' else '', 'tier': 'derived',
            'code_role': 'production', 'detail': '{}', 'kind': 'result', 'reason': ''}


def row(identity='r1', path='a.go', start=10, end=20, classification='resolved_caller',
        operation='/pkg.Svc/M', lineage='L', citation='c1'):
    served = {'endpoint': {'lineage': 'L', 'operation': operation}, 'classification': classification,
              'operation': operation, 'tier': 'derived', 'code_role': 'production',
              'source': {'assertion_id': identity, 'path': path, 'start_byte': start, 'end_byte': end,
                         'citation': citation}}
    if classification == 'resolved_caller':
        served['declaration_lineage'] = lineage
    return served


class ReconcileTests(unittest.TestCase):
    def test_joins_served_rows_one_to_one_and_ignores_request_citations(self):
        facts = [fact(), fact(path='b.go', predicate='UNRESOLVED_CALLER')]
        rows = [row(), row(citation='c2'), row('r2', path='b.go', classification='extractor_abstention')]
        matched, unserved = export.reconcile(rows, facts)
        self.assertEqual(sorted(m['assertion_id'] for m in matched), ['r1', 'r2'])
        self.assertEqual(unserved, [])

    def test_reports_unserved_publication_facts_instead_of_dropping_them(self):
        matched, unserved = export.reconcile([row()], [fact(), fact(path='z.go')])
        self.assertEqual([u['path'] for u in unserved], ['z.go'])

    def test_refuses_rows_absent_from_or_disagreeing_with_the_publication(self):
        cases = {'absent from the publication': [row(path='other.go')],
                 'disagrees with its publication fact': [row(lineage='other')],
                 'served inconsistently': [row(), {**row(), 'tier': 'heuristic'}]}
        for message, rows in cases.items():
            with self.subTest(message), self.assertRaisesRegex(ValueError, message):
                export.reconcile(rows, [fact()])

    def test_refuses_duplicate_publication_coordinates(self):
        with self.assertRaisesRegex(ValueError, 'duplicate publication fact'):
            export.reconcile([], [fact(), fact()])


class ControlTests(unittest.TestCase):
    class FakeApi:
        def __init__(self, generation):
            self.generation = generation

        def get(self, path, params):
            return {'generation': {**self.generation, 'repository': params['repository']}}

    def test_requires_a_complete_current_overlay_generation(self):
        good = {'state': 'current', 'plane': 'repository-overlay', 'commit': 'c', 'generation_digest': 'g',
                'manifest_digest': 'm', 'resolver_manifest_digest': 'r', 'result_count': 1,
                'abstention_count': 2, 'partition_progress': {'state': 'complete', 'refused_pair_count': 0}}
        self.assertEqual(export.generation_controls(self.FakeApi(good), ['repo'])[0]['generation_digest'], 'g')
        for change in ({'state': 'missing'}, {'plane': 'legacy'},
                       {'partition_progress': {'state': 'partial', 'refused_pair_count': 0}},
                       {'partition_progress': {'state': 'complete', 'refused_pair_count': 1}}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                export.generation_controls(self.FakeApi({**good, **change}), ['repo'])


class EndpointTests(unittest.TestCase):
    def test_reads_resolved_grpc_operations_from_the_digest_bound_member(self):
        with tempfile.TemporaryDirectory() as directory:
            data = Path(directory)
            root = data / 'resolver-catalogs'
            root.mkdir()
            repo, generation = 'repo', 'sha256:' + 'ab' * 32
            base = 'phebs-resolver-catalog-' + export.repository_key(repo)
            records = [{'kind': 'declaration_operation', 'protocol': 'grpc', 'state': 'resolved',
                        'lineage': 'L', 'operation': 'pkg.Svc/M'},
                       {'kind': 'declaration_operation', 'protocol': 'grpc', 'state': 'ambiguous',
                        'lineage': 'L', 'operation': 'pkg.Svc/X'}]
            member = b''.join(json.dumps(r).encode() + b'\n' for r in records)
            name = 'grpc-generated-attribution-v2.ndjson'
            (root / f'{base}-v1-{"ab" * 32}-{name}').write_bytes(member)
            manifest = {'authority_digest': 'auth', 'identity': {'generation_digest': generation, 'repository': repo},
                        'members': [{'name': name, 'content_digest': export.sha(member)}]}
            (root / (base + '.manifest.json')).write_text(json.dumps(manifest))
            control = {'repository': repo, 'resolver_manifest_digest': 'auth'}
            self.assertEqual(export.declared_endpoints(data, control, generation), [('L', '/pkg.Svc/M')])
            (root / f'{base}-v1-{"ab" * 32}-{name}').write_bytes(member + b'\n')
            with self.assertRaisesRegex(ValueError, 'member digest'):
                export.declared_endpoints(data, control, generation)
            with self.assertRaisesRegex(ValueError, 'differs from the caller generation'):
                export.declared_endpoints(data, {**control, 'resolver_manifest_digest': 'other'}, generation)


class EnvelopeTests(unittest.TestCase):
    def test_served_facts_project_through_the_frozen_harness_on_census_names(self):
        control = {'repository': 'runtime', 'generation_digest': 'gen', 'commit': 'c' * 40}
        matched = {'runtime': [{**fact(), 'assertion_id': 'r1'}]}
        envelope = export.projector_envelope([control], matched, {'runtime': 'owner/repo'})
        rows = frames.pilot_harness.candidate_rows(envelope)
        self.assertEqual([r['site_id'] for r in rows], [f"owner/repo@{'c' * 40}:a.go:10-20"])


class OverlayTests(unittest.TestCase):
    def test_leaf_overlay_refuses_a_changed_anchor_and_leaves_source_exact(self):
        with tempfile.TemporaryDirectory() as directory:
            repo, out = Path(directory) / 'repo', Path(directory) / 'out'
            out.mkdir()
            source = repo / 'internal/extract/extractors/gocaller'
            source.mkdir(parents=True)
            (source / 'fallback.go').write_text('case *ast.CallExpr:\n')
            with self.assertRaisesRegex(ValueError, 'injection point changed'):
                capture_replay.prepare_leaf_overlay(repo, out)
            (source / 'fallback.go').write_text('x\n' + capture_replay.LEAF_FALLBACK_ANCHOR + '\n')
            before = (source / 'fallback.go').read_bytes()
            hashes = capture_replay.prepare_leaf_overlay(repo, out)
            self.assertEqual((source / 'fallback.go').read_bytes(), before)
            self.assertNotIn('err =', (out / 'fallback.go').read_text().split('t472Observe')[1].split('\n')[0])
            self.assertEqual(hashes['observer.go'], 'sha256:' + hashlib.sha256((out / 'observer.go').read_bytes()).hexdigest())


class BundleTests(unittest.TestCase):
    def test_caller_map_bundle_fills_only_the_two_candidate_identities(self):
        before = json.loads((ROOT / 'bundle/preregistration.run-g.bundle.json').read_text())
        after = json.loads((ROOT / 'bundle/preregistration.caller-map.run-g.bundle.json').read_text())
        receipt = json.loads((ROOT / 'caller_map_bindings_20261011g.json').read_text())
        expected = copy.deepcopy(before)
        for slot in (export.PROOF_SLOT, export.LEDGER_SLOT):
            self.assertIsNone(before['candidate_identities'][slot])
            expected['candidate_identities'][slot] = after['candidate_identities'][slot]
        expected.pop('missing_bindings')
        expected['missing_bindings'] = binding_bundle.validate_bundle(expected)
        self.assertEqual(after, expected)
        self.assertEqual(len(after['missing_bindings']), 22)
        self.assertEqual(after['candidate_identities'][export.PROOF_SLOT]['sha256'],
                         receipt['artifacts_sha256']['caller-map-export.json'])
        self.assertEqual(after['candidate_identities'][export.LEDGER_SLOT]['sha256'],
                         receipt['artifacts_sha256']['call-site-decisions.jsonl'])
        self.assertEqual(receipt['unserved_publication_facts'], 0)
        self.assertEqual(sum(receipt['decision_ledger_states'].values()), receipt['decision_ledger_population'])
        self.assertEqual(receipt['decision_ledger_population'], 498793)
        for key in ('sample_drawn', 'labels_produced', 'predictions_disclosed', 'quality_scored',
                    'protocol_sealed', 'external_publication', 'acceptance_met'):
            self.assertIs(receipt[key], False)


if __name__ == '__main__':
    unittest.main()
