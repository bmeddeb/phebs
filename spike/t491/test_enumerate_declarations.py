"""Synthetic grammar fences for the independent declaration reader."""
import unittest

import enumerate_declarations as e

PROTO = b'''
syntax = "proto2"; // a comment with { braces }
package acme.v1; /* block { comment } */
import "other.proto";
option go_package = "x/{y}";
message Outer {
  option (custom) = { a: 1 nested { b: "}" } };
  required string name = 1 [default = "}{"];
  map<string, Inner> things = 2;
  oneof choice { int32 small = 3; Inner big = 4; }
  message Inner { optional int64 info = 1; }
  enum Kind { A = 0; }
  reserved 9 to 11;
  extensions 100 to 199;
  optional group Result = 5 { optional string url = 1; }
  extend Base { optional int32 nested_ext = 120; }
}
extend Outer { optional string top_ext = 150; }
service Store {
  option deprecated = true;
  rpc Get (Outer) returns (Outer);
  rpc Watch (stream Outer) returns (stream Outer) { option idempotency_level = NO_SIDE_EFFECTS; }
}
'''


class ReaderTests(unittest.TestCase):
    def test_enumerates_the_frozen_kinds_with_section_5_objects(self):
        facts, skipped = e.enumerate_file(PROTO)
        objects = sorted((f['predicate'], f['object']) for f in facts)
        self.assertEqual(objects, sorted([
            ('DECLARES_MESSAGE', 'acme.v1.Outer'), ('DECLARES_MESSAGE', 'acme.v1.Outer.Inner'),
            ('DECLARES_MESSAGE', 'acme.v1.Outer.Result'),
            ('DECLARES_FIELD', 'acme.v1.Outer#1'), ('DECLARES_FIELD', 'acme.v1.Outer#2'),
            ('DECLARES_FIELD', 'acme.v1.Outer#3'), ('DECLARES_FIELD', 'acme.v1.Outer#4'),
            ('DECLARES_FIELD', 'acme.v1.Outer#5'), ('DECLARES_FIELD', 'acme.v1.Outer.Inner#1'),
            ('DECLARES_FIELD', 'acme.v1.Outer.Result#1'),
            ('DECLARES_EXTENSION', 'acme.v1.Outer.nested_ext#120'), ('DECLARES_EXTENSION', 'acme.v1.top_ext#150'),
            ('DECLARES_SERVICE', 'acme.v1.Store'),
            ('DECLARES_OPERATION', 'acme.v1.Store/Get'), ('DECLARES_OPERATION', 'acme.v1.Store/Watch')]))
        self.assertEqual(skipped, {'enum': 1, 'reserved': 1, 'extensions': 1})

    def test_spans_cover_the_whole_statement(self):
        facts, _ = e.enumerate_file(PROTO)
        spans = {f['object']: PROTO[f['start_byte']:f['end_byte']] for f in facts}
        self.assertEqual(spans['acme.v1.Outer#1'], b'required string name = 1 [default = "}{"];')
        self.assertTrue(spans['acme.v1.Store/Watch'].startswith(b'rpc Watch') and spans['acme.v1.Store/Watch'].endswith(b'}'))
        self.assertTrue(spans['acme.v1.Outer'].startswith(b'message Outer {') and spans['acme.v1.Outer'].endswith(b'}'))

    def test_refuses_constructs_that_need_descriptor_linking(self):
        for bad in (b'package p; extend M { optional group G = 1 { optional int32 a = 1; } }',
                    b'package p; extend M { map<string,string> m = 1; }',
                    b'package p; message M { string = 1; }'):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                e.enumerate_file(bad)

    def test_role_strata_follow_the_declared_precedence(self):
        self.assertEqual(e.role('vendor/x/tests/a.proto'), 'vendor')
        self.assertEqual(e.role('a/testdata/b.proto'), 'test')
        self.assertEqual(e.role('api/v1/a.proto'), 'production')


if __name__ == '__main__':
    unittest.main()
