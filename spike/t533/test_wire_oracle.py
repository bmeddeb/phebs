"""Fences for the independent WIRE oracle."""
import json
import unittest
from pathlib import Path

import wire_oracle as w

GOLDENS = Path(__file__).resolve().parents[2] / 'internal/compat/testdata/golden_changes.json'
H = 'syntax = "proto3";\npackage p;\n'


def judge(before: str, after: str, path: str = 'p/x.proto') -> dict:
    return w.judge([(path, before.encode())], [(path, after.encode())])


class GoldenTest(unittest.TestCase):
    def test_oracle_reproduces_every_golden_truth(self):
        for case in json.loads(GOLDENS.read_text())['cases']:
            with self.subTest(case['id']):
                got = judge(case['before'], case['after'], case['path'])
                want = 'compatible' if case['expect']['compatible'] else 'breaking'
                self.assertEqual(got['verdict'], want, got)


class RuleTest(unittest.TestCase):
    def test_single_explicit_field_into_a_new_oneof_is_safe(self):
        self.assertEqual(judge(H + 'message M { optional string a = 1; }',
                               H + 'message M { oneof c { string a = 1; } }')['verdict'], 'compatible')

    def test_unpacked_proto2_numeric_cardinality_is_compatible_and_packed_breaks(self):
        p2 = 'syntax = "proto2";\npackage p;\n'
        self.assertEqual(judge(p2 + 'message M { optional int32 n = 1; }',
                               p2 + 'message M { repeated int32 n = 1; }')['verdict'], 'compatible')
        self.assertEqual(judge(p2 + 'message M { optional int32 n = 1; }',
                               p2 + 'message M { repeated int32 n = 1 [packed = true]; }')['verdict'], 'breaking')

    def test_a_different_message_type_is_undetermined_and_enum_to_int_is_compatible(self):
        self.assertEqual(judge(H + 'message A {} message B {} message M { A a = 1; }',
                               H + 'message A {} message B {} message M { B a = 1; }')['verdict'], 'undetermined')
        self.assertEqual(judge(H + 'enum E { Z = 0; } message M { E e = 1; }',
                               H + 'enum E { Z = 0; } message M { int32 e = 1; }')['verdict'], 'compatible')

    def test_nested_scopes_maps_groups_and_rpc_options_read(self):
        before = ('syntax = "proto2";\npackage p;\nimport "google/protobuf/empty.proto";\n'
                  'message M { message In { optional int32 x = 1; } map<string, In> m = 1;'
                  ' optional group G = 2 { optional string s = 1; } reserved 5 to 7, "old"; extensions 100 to max; }\n'
                  'service S { rpc Get (M) returns (google.protobuf.Empty) { option idempotency_level = NO_SIDE_EFFECTS; } }\n')
        self.assertEqual(judge(before, before)['verdict'], 'compatible')
        after = before.replace('NO_SIDE_EFFECTS', 'IDEMPOTENT').replace('reserved 5 to 7, "old";', 'reserved 5 to 6;')
        rules = {f['rule'] for f in judge(before, after)['findings']}
        self.assertEqual(rules, {'rpc_idempotency', 'reserved_message_deleted'})

    def test_unreadable_input_is_reported_not_judged(self):
        self.assertEqual(judge(H + 'message M { string a = ; }', H)['verdict'], 'unreadable')


if __name__ == '__main__':
    unittest.main()
