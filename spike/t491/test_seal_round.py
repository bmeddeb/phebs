"""Synthetic fences for the T49.1b seal/draw tooling; nothing real is fetched."""
import datetime as dt
import json
import unittest
from pathlib import Path

import seal_round

ROOT = Path(__file__).parent


class SealRoundTests(unittest.TestCase):
    def test_plan_samples_97_per_stratum_with_census_below(self):
        _, declarations, references = seal_round.load_universe(ROOT / 'universe')
        plan = seal_round.plan(declarations, references)
        self.assertEqual(plan['declaration:DECLARES_FIELD']['sample_size'], 97)
        self.assertTrue(plan['declaration:DECLARES_EXTENSION']['census'])
        self.assertEqual(sum(p['population'] for n, p in plan.items() if n.startswith('declaration:')), len(declarations))
        self.assertEqual(sum(p['population'] for n, p in plan.items() if n.startswith('reference:')), len(references))

    def test_seal_binds_thresholds_tools_and_refuses_a_near_activation(self):
        export = ROOT / 'universe' / 'universe.json'  # any bytes stand in for the private export here
        now = dt.datetime(2026, 10, 12, tzinfo=dt.timezone.utc)
        with self.assertRaisesRegex(ValueError, 'five minutes'):
            seal_round.seal_document(ROOT.parents[1], ROOT / 'universe', export, '2026-10-12T00:01:00Z', now)
        document = seal_round.seal_document(ROOT.parents[1], ROOT / 'universe', export, '2026-10-12T01:00:00Z', now)
        self.assertEqual(document['thresholds'], {'declaration_agreement': 0.995, 'reference_agreement': 0.995,
                                                  'oracle_sample_correct': 0.99})
        self.assertEqual(set(document['tools_sha256']), set(seal_round.TOOLS))

    def test_draw_is_deterministic_and_bound_to_its_seal(self):
        export = ROOT / 'universe' / 'universe.json'
        now = dt.datetime(2026, 10, 12, tzinfo=dt.timezone.utc)
        document = seal_round.seal.canonical_document(
            seal_round.seal_document(ROOT.parents[1], ROOT / 'universe', export, '2026-10-12T01:00:00Z', now))
        record = {'seal_document_sha256': seal_round.seal.sha(document), 'seed_hex': 'ab' * 32}
        first = seal_round.draw(document, record, ROOT / 'universe')
        self.assertEqual(first, seal_round.draw(document, record, ROOT / 'universe'))
        plan = json.loads(document)['sample_plan']
        self.assertEqual({k: len(v) for k, v in first['samples'].items()}, {k: v['sample_size'] for k, v in plan.items()})
        with self.assertRaisesRegex(ValueError, 'another seal'):
            seal_round.draw(document, {**record, 'seal_document_sha256': 'x'}, ROOT / 'universe')


if __name__ == '__main__':
    unittest.main()
