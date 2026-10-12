"""Synthetic fences for the T47.2p round frames plan; no candidate populations read."""
import copy
import json
import unittest
from pathlib import Path

import binding_bundle
import frames
import round_frames

ROOT = Path(__file__).parent


def site(name, stratum='r:production'):
    return {'site_id': name, 'stratum': stratum, 'repository': 'r', 'path': 'a.go', 'start_byte': 1, 'end_byte': 2}


class PopulationTests(unittest.TestCase):
    def setUp(self):
        self.recall = {n: site(n) for n in ('p', 'u', 'w', 'n')}
        self.candidate = {'precision': {'rows': [{'site_id': 'p', 'stratum': 'r:production', 'object': 'secret'}]},
                          'unresolved': {'rows': [{'site_id': 'u', 'stratum': 'r:production'}]}}
        self.ledger = [{'site_id': 'p', 'stratum': 'r:production', 'state': 'reported'},
                       {'site_id': 'u', 'stratum': 'r:production', 'state': 'reported'},
                       {'site_id': 'w', 'stratum': 'r:production', 'state': 'withheld'},
                       {'site_id': 'n', 'stratum': 'r:production', 'state': 'not_considered'}]

    def test_builds_coordinate_only_populations(self):
        populations = round_frames.candidate_populations(self.candidate, self.ledger, self.recall)
        self.assertEqual({k: [r['site_id'] for r in v] for k, v in populations.items()},
                         {'precision': ['p'], 'unresolved': ['u'], 'abstention': ['w']})
        self.assertNotIn('object', populations['precision'][0])

    def test_refuses_inconsistent_custody(self):
        cases = {
            'outside the recall universe': lambda c, l, r: r.pop('p'),
            'cover the recall population': lambda c, l, r: l.pop(),
            'duplicate ledger site': lambda c, l, r: l.append(dict(l[0])),
            'reported ledger rows differ': lambda c, l, r: l[2].update(state='reported'),
        }
        for message, mutate in cases.items():
            candidate, ledger, recall = copy.deepcopy(self.candidate), copy.deepcopy(self.ledger), copy.deepcopy(self.recall)
            mutate(candidate, ledger, recall)
            with self.subTest(message), self.assertRaisesRegex(ValueError, message):
                round_frames.candidate_populations(candidate, ledger, recall)


class CommittedRoundTests(unittest.TestCase):
    def test_round_plan_fills_only_candidate_frames_from_the_template(self):
        template = json.loads((ROOT / 'frames/frames.plan.json').read_text())
        plan = json.loads((ROOT / 'frames/frames.plan.run-g.json').read_text())
        bundle = json.loads((ROOT / 'bundle/preregistration.frames.run-g.bundle.json').read_text())
        source = json.loads((ROOT / 'bundle/preregistration.caller-map.run-g.bundle.json').read_text())
        receipt = json.loads((ROOT / 'caller_map_bindings_20261011g.json').read_text())
        self.assertEqual(plan['round']['template_plan_sha256'], frames.sha256_file(ROOT / 'frames/frames.plan.json'))
        self.assertEqual(plan['round']['candidate_bundle_sha256'],
                         frames.sha256_file(ROOT / 'bundle/preregistration.caller-map.run-g.bundle.json'))
        for before, after in zip(template['frames'], plan['frames']):
            if before['frame_id'] not in round_frames.POPULATION_FILES:
                self.assertEqual(before, after)
                continue
            self.assertNotIn('pending', after)
            if before['frame_id'] in round_frames.DROPPED_FRAMES:
                self.assertNotIn('strata', after)
                self.assertEqual(after['dropped'], round_frames.DROPPED_FRAMES[before['frame_id']])
                continue
            self.assertEqual(sum(s['population'] for s in after['strata'].values()), after['population'])
            for sizes in after['strata'].values():
                self.assertEqual(sizes['sample_size'], min(97, sizes['population']))
                self.assertEqual(sizes['census'], sizes['population'] <= 97)
            self.assertEqual(bundle['frames_plan']['members'][after['file']],
                             {'sha256': after['sha256'], 'rows': after['rows']})
        populations = {f['frame_id']: f['population'] for f in plan['frames']}
        self.assertEqual(populations['precision'], receipt['frame_populations']['precision'])
        self.assertEqual(populations['unresolved'], receipt['frame_populations']['unresolved'])
        self.assertEqual(populations['abstention'], receipt['decision_ledger_states']['withheld'])
        self.assertEqual(bundle['frames_plan']['sha256'], frames.sha256_file(ROOT / 'frames/frames.plan.run-g.json'))
        expected = copy.deepcopy(source)
        expected['frames_plan'] = bundle['frames_plan']
        expected['machinery']['binding_bundle'] = bundle['machinery']['binding_bundle']
        expected['protocol_doc'] = bundle['protocol_doc']
        self.assertEqual(bundle['protocol_doc']['sha256'], frames.sha256_file(ROOT.parents[1] / 'docs/CALLER_QUALITY_PROTOCOL.md'))
        expected.pop('missing_bindings')
        expected['missing_bindings'] = binding_bundle.validate_bundle(expected)
        self.assertEqual(bundle, expected)
        self.assertEqual(len(bundle['missing_bindings']), 22)
        self.assertEqual(plan['missing_bindings'],
                         [m for m in template['missing_bindings'] if bundle['candidate_identities'][m] is None])
        self.assertFalse(plan['seed_committed'] or plan['labels_committed'] or plan['quality_scored'])

    def test_validator_rejects_an_unregistered_plan_path_or_member_set(self):
        bundle = json.loads((ROOT / 'bundle/preregistration.frames.run-g.bundle.json').read_text())
        for mutate in (lambda b: b['frames_plan'].update(path='spike/t472/frames/elsewhere.json'),
                       lambda b: b['frames_plan']['members'].pop('frames.abstention.jsonl.gz')):
            changed = copy.deepcopy(bundle)
            mutate(changed)
            with self.subTest(mutate=mutate), self.assertRaises(binding_bundle.BundleError):
                binding_bundle.validate_bundle(changed)


if __name__ == '__main__':
    unittest.main()
