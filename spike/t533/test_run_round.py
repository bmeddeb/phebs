"""Fences for the round-2 scoring classes."""
import unittest

import run_round as r

BREAK = [{'outcome': 'break', 'rule': 'field_type'}]
NOTE = [{'outcome': 'note', 'rule': 'disclosed_type_equivalent'}]


class ClassifyTest(unittest.TestCase):
    def test_classes(self):
        cases = [
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_TYPE']}, 'breaking', BREAK, 'agree'),
            ({'product': 'compatible', 'rules': []}, 'compatible', [], 'agree'),
            ({'product': 'refused'}, 'unreadable', [], 'agree'),
            ({'product': 'compatible', 'rules': []}, 'unreadable', [], 'judged_unreadable'),
            ({'product': 'breaking', 'rules': ['COMPILE']}, 'unreadable', [], 'judged_unreadable'),
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_TYPE']}, 'compatible', NOTE, 'disclosed_conservative'),
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_TYPE']}, 'compatible', [], 'conservative'),
            ({'product': 'breaking', 'rules': ['FIELD_SAME_ONEOF']}, 'compatible', NOTE, 'conservative'),
            ({'product': 'compatible', 'rules': []}, 'breaking', BREAK, 'permissive'),
            ({'product': 'refused'}, 'compatible', [], 'refused_readable'),
            ({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_TYPE']}, 'undetermined', [], 'pending'),
        ]
        for product, truth, findings, want in cases:
            with self.subTest(product=product, truth=truth):
                self.assertEqual(r.classify(product, truth, findings), want)
        self.assertEqual(r.classify({'product': 'breaking', 'rules': ['FIELD_WIRE_COMPATIBLE_CARDINALITY']},
                                    'compatible', [], disclosed=True), 'disclosed_conservative')


if __name__ == '__main__':
    unittest.main()
