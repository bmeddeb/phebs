"""Synthetic fences for binding a ceremony into the sealed bundle."""
import json
import unittest
from pathlib import Path

import ceremony_record
import seal

ROOT = Path(__file__).parent


class CeremonyRecordTests(unittest.TestCase):
    def setUp(self):
        self.sealed = json.loads((ROOT / 'bundle/preregistration.sealed.run-g.bundle.json').read_text())
        self.document = (ROOT / 'seal.run-g.json').read_bytes()
        planned = json.loads(self.document)['planned_samples']
        self.seed = json.dumps({'seal_document_sha256': seal.sha(self.document), 'seed_hex': 'ab' * 32,
                                'pulse_uri': 'https://beacon.nist.gov/beacon/2.0/chain/2/pulse/1',
                                'pulse_payload_sha256': 'sha256:' + 'c' * 64, 'output_value': 'd' * 128,
                                'pulse_timestamp': '2026-10-12T03:11:00.000Z'}).encode()
        self.sample = json.dumps({'seal_document_sha256': seal.sha(self.document), 'seed_hex': 'ab' * 32,
                                  'counts': planned}).encode()

    def test_binds_only_seed_and_first_three_randomness_steps(self):
        bundle = ceremony_record.record(self.sealed, self.document, self.seed, self.sample)
        self.assertEqual(len(bundle['missing_bindings']), len(self.sealed['missing_bindings']) - 7)
        self.assertEqual([s['binding'] is None for s in bundle['randomness_commitment_sequence']],
                         [False, False, False, True, True])
        self.assertIsNone(bundle['candidate_identities']['frozen blind label document per frame (Ben, sole owner/reviewer)'])

    def test_refuses_mixed_ceremonies_and_incomplete_samples(self):
        with self.assertRaisesRegex(ValueError, 'different seals'):
            ceremony_record.record(self.sealed, self.document, self.seed,
                                   self.sample.replace(b'abab', b'cdcd', 1))
        short = json.loads(self.sample)
        short['counts']['recall'] -= 1
        with self.assertRaisesRegex(ValueError, 'does not complete'):
            ceremony_record.record(self.sealed, self.document, self.seed, json.dumps(short).encode())


if __name__ == '__main__':
    unittest.main()
