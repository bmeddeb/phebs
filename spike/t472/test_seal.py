"""Synthetic seal/seed/draw fences with fake Gist and Beacon fetchers; nothing real is fetched."""
import datetime as dt
import gzip
import json
import tempfile
import unittest
from pathlib import Path

import seal

ROOT = Path(__file__).parent
REPO = ROOT.parents[1]


class SeedTests(unittest.TestCase):
    def setUp(self):
        self.document = seal.canonical_document({'schema': seal.SEAL_SCHEMA, 'activation_at': '2026-07-14T12:00:30Z',
                                                 'frames_plan': {'sha256': 'x'}, 'planned_samples': {}})
        self.api = 'https://api.github.com/gists/abc123'
        self.revision = 'd' * 40
        self.created = '2026-07-14T11:59:00Z'
        self.uri = 'https://beacon.nist.gov/beacon/2.0/chain/2/pulse/123'
        self.pulse = {'uri': self.uri, 'timeStamp': '2026-07-14T12:01:00.000Z', 'outputValue': 'ABCDEF0123456789' * 8}
        self.time_url = 'https://beacon.nist.gov/beacon/2.0/pulse/time/1784030460000'

    def gist(self, url):
        return {self.api: {'url': self.api, 'created_at': self.created,
                           'history': [{'version': self.revision, 'committed_at': self.created}]},
                self.api + '/' + self.revision: {'files': {'seal.json': {'content': self.document.decode(),
                                                                          'truncated': False}}}}[url]

    def nist(self, url):
        if url not in (self.time_url, self.uri):
            raise AssertionError(url)
        return {'pulse': dict(self.pulse)}

    def test_seed_uses_only_the_first_pulse_after_activation_and_is_deterministic(self):
        record = seal.seed(self.document, self.api, self.gist, self.nist)
        self.assertEqual(record, seal.seed(self.document, self.api, self.gist, self.nist))
        self.assertRegex(record['seed_hex'], r'\A[0-9a-f]{64}\Z')
        self.assertEqual(record['output_value'], self.pulse['outputValue'].lower())
        self.assertEqual(record['gist_receipt']['revision'], self.revision)

    def test_refuses_late_gist_wrong_pulse_or_disagreeing_lookups(self):
        cases = {
            'published before activation': lambda: setattr(self, 'created', '2026-07-14T12:00:40Z'),
            'first eligible pulse': lambda: self.pulse.update(timeStamp='2026-07-14T12:02:00.000Z'),
            'canonical URI': lambda: self.pulse.update(uri='https://example.test/pulse/1'),
            'outputValue': lambda: self.pulse.update(outputValue='zz'),
        }
        for message, mutate in cases.items():
            self.setUp()
            mutate()
            with self.subTest(message), self.assertRaisesRegex(ValueError, message):
                seal.seed(self.document, self.api, self.gist, self.nist)
        self.setUp()
        disagree = lambda url: {'pulse': {**self.pulse, 'outputValue': '0' * 128}} if url == self.uri else self.nist(url)
        with self.assertRaisesRegex(ValueError, 'disagree'):
            seal.seed(self.document, self.api, self.gist, disagree)

    def test_refuses_a_noncanonical_document(self):
        with self.assertRaisesRegex(ValueError, 'canonical'):
            seal.seed(self.document.rstrip(b'\n'), self.api, self.gist, self.nist)


class SealTests(unittest.TestCase):
    def test_seal_fills_only_the_protocol_digest_and_refuses_a_near_activation(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            source = ROOT / 'bundle/preregistration.frames.run-g.bundle.json'
            bundle_out, document_out = REPO / 'spike/t472/bundle/.test-sealed.json', out / 'seal.json'
            try:
                now = dt.datetime(2026, 10, 11, tzinfo=dt.timezone.utc)
                with self.assertRaisesRegex(ValueError, 'five minutes'):
                    seal.seal(REPO, source, bundle_out, document_out, '2026-10-11T00:01:00Z', now)
                document = seal.seal(REPO, source, bundle_out, document_out, '2026-10-11T01:00:00Z', now)
                before, after = json.loads(source.read_text()), json.loads(bundle_out.read_text())
                self.assertEqual(len(after['missing_bindings']), len(before['missing_bindings']) - 1)
                self.assertEqual(after['candidate_identities'][seal.PROTOCOL_SLOT]['sha256'], before['protocol_doc']['sha256'])
                self.assertEqual(document['planned_samples'], {'recall': 1045, 'attribution': 74, 'precision': 206, 'unresolved': 261})
                self.assertEqual(document['dropped_frames'], ['abstention'])
                self.assertEqual(document_out.read_bytes(), seal.canonical_document(document))
                with self.assertRaisesRegex(ValueError, 'already sealed'):
                    seal.seal(REPO, bundle_out, out / 'again.json', out / 'again-doc.json', '2026-10-11T01:00:00Z', now)
            finally:
                bundle_out.unlink(missing_ok=True)


class DrawTests(unittest.TestCase):
    def test_draw_matches_the_sealed_plan_and_skips_dropped_frames(self):
        with tempfile.TemporaryDirectory() as directory:
            public, private = Path(directory) / 'public', Path(directory) / 'private'
            public.mkdir(), private.mkdir()
            def population(name, path, n, stratum):
                rows = [{'site_id': f'{name}{i:03d}', 'stratum': stratum} for i in range(n)]
                body = b''.join(json.dumps(r, sort_keys=True).encode() + b'\n' for r in rows)
                raw = gzip.compress(body, mtime=0) if name.endswith('.gz') else body
                (path / name).write_bytes(raw)
                return {'file': name, 'sha256': seal.sha(raw), 'rows': n, 'population': n,
                        'strata': {stratum: {'population': n, 'sample_size': min(97, n), 'census': n <= 97}}}
            plan = {'quality_frames': seal.frames.QUALITY_FRAMES, 'frames': [
                {'frame_id': 'recall', **population('frames.recall.jsonl.gz', public, 150, 'r:production')},
                {'frame_id': 'precision', **population('frames.precision.jsonl', private, 20, 'r:production')},
                {'frame_id': 'unresolved', **population('frames.unresolved.jsonl', private, 5, 'r:production')},
                {'frame_id': 'abstention', 'dropped': {}, 'population': 9}]}
            plan_raw = json.dumps(plan).encode()
            document = seal.canonical_document({'frames_plan': {'sha256': seal.sha(plan_raw)},
                                                'planned_samples': {'recall': 97, 'precision': 20, 'unresolved': 5}})
            record = {'seal_document_sha256': seal.sha(document), 'seed_hex': 'ab' * 32}
            sample = seal.draw(plan_raw, record, document, public, private)
            self.assertEqual(sample['counts'], {'recall': 97, 'precision': 20, 'unresolved': 5})
            self.assertNotIn('abstention', sample['samples'])
            self.assertEqual(sample, seal.draw(plan_raw, record, document, public, private))
            with self.assertRaisesRegex(ValueError, 'another seal document'):
                seal.draw(plan_raw, {**record, 'seal_document_sha256': 'x'}, document, public, private)
            (private / 'frames.precision.jsonl').write_bytes(b'')
            with self.assertRaisesRegex(ValueError, 'differs from the sealed plan'):
                seal.draw(plan_raw, record, document, public, private)


if __name__ == '__main__':
    unittest.main()
