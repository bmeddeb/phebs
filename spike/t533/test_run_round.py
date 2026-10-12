"""Fences for the round's scoring classes."""
import unittest

import run_round as r

BREAK = [{'outcome': 'break', 'rule': 'field_type'}]


class ClassifyTest(unittest.TestCase):
    def test_classes(self):
        cases = [
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_TYPE']}, 'breaking', BREAK, 'agree'),
            ({'product': 'compatible', 'rules': []}, 'compatible', [], 'agree'),
            ({'product': 'refused'}, 'unreadable', [], 'agree'),
            ({'product': 'compatible', 'rules': []}, 'unreadable', [], 'pending'),
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_CARDINALITY']}, 'compatible', [], 'pinned_conservative'),
            ({'product': 'breaking', 'rules': ['FIELD_SAME_ONEOF']}, 'compatible', [], 'pending'),
            # A permissive verdict is never scored on the oracle alone: it is adjudicated first.
            ({'product': 'compatible', 'rules': []}, 'breaking', BREAK, 'pending'),
            ({'product': 'refused'}, 'compatible', [], 'pending'),
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_TYPE']}, 'undetermined', [], 'pending'),
        ]
        for product, truth, findings, want in cases:
            with self.subTest(product=product, truth=truth):
                self.assertEqual(r.classify(product, truth, findings), want)


if __name__ == '__main__':
    unittest.main()
