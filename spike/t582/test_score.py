"""Supplemental inert checker controls; no frozen source or reader execution."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import score


def registration_sample(detail):
    end = len('a.NewSProcessor(h)')
    fact = {'Path': 'consumer.go', 'StartLine': 1, 'EndLine': 1,
            'Atom': {'StartByte': 0, 'EndByte': end},
            'Assertion': {'Predicate': 'UNRESOLVED_THRIFT_REGISTRATION', 'Tier': 'unresolved',
                          'Lineage': '', 'Object': 'NewSProcessor', 'Detail': json.dumps(detail)}}
    label = {'path': 'consumer.go', 'span': {'text': 'a.NewSProcessor'},
             'whole_call': {'start_byte': 0, 'end_byte': end, 'start_line': 1, 'end_line': 1}}
    return fact, label


class CheckerBoundaries(unittest.TestCase):
    def test_duplicate_json_keys_refused(self):
        with self.assertRaises(ValueError):
            score.load_json('{"tier":"unresolved","tier":"exact"}')

    def test_registration_candidates_not_inferred_from_count(self):
        expected = {'predicate': 'UNRESOLVED_THRIFT_REGISTRATION', 'candidate_count': 2,
                    'candidates': [{'scope_hint': 'a', 'service_hint': 'S'}, {'scope_hint': 'b', 'service_hint': 'S'}]}
        fact, label = registration_sample({'constructor': 'NewSProcessor', 'candidate_count': 2})
        valid, reason = score.check_ambiguity([fact], expected, label)
        self.assertFalse(valid)
        self.assertEqual(reason, 'registration-ambiguity-candidates-not-exposed')

    def test_exact_tier_and_wrong_lineage_do_not_match(self):
        expected = {'predicate': 'CALLS_OPERATION', 'tier': 'heuristic', 'scope_hint': 'agent',
                    'service_hint': 'Agent', 'wire_hint': 'emitBatch', 'method_hint': 'EmitBatch'}
        request = {'repository': 'authored'}
        lineage = 'provisional_repo_path_v1_' + score.sha(b'authored\0thrift-gen/agent/agent.go')
        fact = {'Assertion': {'Predicate': 'CALLS_OPERATION', 'Tier': 'heuristic', 'Lineage': lineage,
                'Object': '/agent.Agent/emitBatch',
                'Detail': '{"schema":"thriftgo-call-detail-v1","method":"emitBatch","go_method":"EmitBatch"}'}}
        self.assertTrue(score.match_hint(fact, expected, request))
        fact['Assertion']['Tier'] = 'exact'
        self.assertFalse(score.match_hint(fact, expected, request))
        fact['Assertion']['Tier'] = 'heuristic'
        fact['Assertion']['Lineage'] = 'foreign'
        self.assertFalse(score.match_hint(fact, expected, request))

    def test_registration_schema_and_extra_keys_refused(self):
        candidates = [{'scope_hint': 'a', 'service_hint': 'S'}, {'scope_hint': 'b', 'service_hint': 'S'}]
        expected = {'predicate': 'UNRESOLVED_THRIFT_REGISTRATION', 'candidate_count': 2, 'candidates': candidates}
        detail = {'schema': 'thriftgo-register-ambiguity-v1', 'constructor': 'NewSProcessor',
                  'candidate_count': 2, 'candidates': candidates}
        fact, label = registration_sample(detail)
        self.assertTrue(score.check_ambiguity([fact], expected, label)[0])
        for invalid in [dict(detail, schema='wrong'), dict(detail, extra=True)]:
            fact, label = registration_sample(invalid)
            self.assertFalse(score.check_ambiguity([fact], expected, label)[0])

    def test_overlong_span_refused(self):
        fact, label = registration_sample({})
        self.assertTrue(score.exact_span(fact, label))
        fact['Atom']['EndByte'] += 1
        self.assertFalse(score.exact_span(fact, label))

    def test_postlaunch_cleanup_exception_keeps_unknown_custody(self):
        import run as runner
        ledger = []
        child = mock.MagicMock(pid=55555, returncode=0)
        child.wait.return_value = 0
        selector = mock.MagicMock()
        selector.select.side_effect = RuntimeError('inert postlaunch exception')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); incoming = root/'input'; incoming.write_bytes(b'')
            with mock.patch.object(runner.sys, 'platform', 'darwin'), \
                 mock.patch.object(runner.ctypes, 'CDLL'), \
                 mock.patch.object(runner.selectors, 'DefaultSelector') as selector_type, \
                 mock.patch.object(runner.subprocess, 'Popen', return_value=child), \
                 mock.patch.object(runner.os, 'set_blocking'), \
                 mock.patch.object(runner, 'stop_group'), \
                 mock.patch.object(runner, 'group_absent', side_effect=RuntimeError('inert cleanup exception')):
                selector_type.return_value.__enter__.return_value = selector
                with self.assertRaisesRegex(RuntimeError, 'inert cleanup exception'):
                    runner.run_child(['inert'], root, incoming, root/'out', root/'err', 5, 1024, ledger)
            self.assertEqual(len(ledger), 1)
            self.assertTrue(ledger[0]['launch_attempted'])
            self.assertEqual(ledger[0]['pid'], 55555)
            self.assertFalse(runner.custody_complete(ledger))
            self.assertFalse(ledger[0]['owned_process_group_absent'])
            self.assertEqual(ledger[0]['stop_diagnostic'], 'inert postlaunch exception')


if __name__ == '__main__':
    unittest.main()
