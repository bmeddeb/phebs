"""Synthetic fences for blind label sheets; no real sample read."""
import json
import unittest

import label_sheets

COMMIT = 'c' * 40
SOURCE = b'package a\n\nfunc f() {\n\tclient.Call(ctx)\n\tother()\n}\n'


def site(path, start, end, repo='o/r'):
    return f'{repo}@{COMMIT}:{path}:{start}-{end}'


class SheetTests(unittest.TestCase):
    def setUp(self):
        self.call = site('a.go', SOURCE.index(b'client.Call'), SOURCE.index(b'client.Call') + 11)
        self.other = site('a.go', SOURCE.index(b'other'), SOURCE.index(b'other') + 5)
        self.iface = site('a.go', 0, 9)
        self.sample = {'seed_hex': 'ab' * 32, 'samples': {
            'recall': {'o/r:production': [self.call, self.other]},
            'precision': {'o/r:production': [self.call]},
            'attribution': {'o/r:generated': [self.iface]}}}
        self.reads = []

    def read(self, repository, commit, path):
        self.reads.append((repository, commit, path))
        return SOURCE

    def test_sheet_is_deduplicated_seed_ordered_and_carries_no_frame(self):
        rows, membership = label_sheets.build(self.sample, self.read, {self.iface})
        self.assertEqual(sorted(r['site_id'] for r in rows), sorted({self.call, self.other, self.iface}))
        self.assertEqual([r['site_id'] for r in rows],
                         label_sheets.blind_order(self.sample['seed_hex'], [self.call, self.other, self.iface]))
        text = json.dumps(rows) + label_sheets.review_markdown(rows)
        for leak in ('precision', 'recall', 'unresolved', 'attribution', 'stratum'):
            self.assertNotIn(leak, text)
        self.assertEqual({m['frame'] for m in membership['membership'][self.call]}, {'recall', 'precision'})
        by_id = {r['site_id']: r for r in rows}
        self.assertEqual(by_id[self.call]['span_text'], 'client.Call')
        self.assertEqual(by_id[self.call]['start_line'], 4)
        self.assertEqual(by_id[self.iface]['unit'], 'service_client_interface')
        self.assertEqual(by_id[self.other]['unit'], 'call_site')

    def test_refuses_spans_outside_the_blob_and_unknown_interfaces(self):
        self.sample['samples']['recall']['o/r:production'].append(site('a.go', 0, len(SOURCE) + 1))
        with self.assertRaisesRegex(ValueError, 'exceeds its source blob'):
            label_sheets.build(self.sample, self.read, {self.iface})
        self.setUp()
        with self.assertRaisesRegex(ValueError, 'outside the census'):
            label_sheets.build(self.sample, self.read, set())

    def test_blank_templates_match_the_frozen_label_schema(self):
        import label_protocol
        self.assertEqual(set(label_sheets.LABEL_ROW), set(label_protocol.LABEL_FIELDS))
        with self.assertRaises(label_protocol.LabelProtocolError):
            label_protocol.validate_labels([{**label_sheets.LABEL_ROW, 'site_id': self.call}], [self.call])


if __name__ == '__main__':
    unittest.main()
