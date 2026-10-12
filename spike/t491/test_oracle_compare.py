"""Synthetic fences for the machine-oracle comparison and pass rule."""
import json
import unittest

import oracle_compare as oc

ALIASES = {'runtime/r': 'o/r'}


def candidate(facts):
    return {'facts': [{'repo': 'runtime/r', 'subject': 'a.proto', 'predicate': p, 'object': o,
                       'detail': json.dumps(d)} for p, o, d in facts]}


class CompareTests(unittest.TestCase):
    def setUp(self):
        self.oracle_decls = [{'repository': 'o/r', 'path': 'a.proto', 'predicate': p, 'object': o}
                             for p, o in (('DECLARES_MESSAGE', 'p.M'), ('DECLARES_FIELD', 'p.M#1'), ('DECLARES_FIELD', 'p.M#2'))]
        self.oracle_refs = [{'repository': 'o/r', 'path': 'a.proto', 'object': 'p.M#1', 'slot': 'type',
                             'resolution': 'same_file', 'reason': ''}]
        self.cand = candidate([('DECLARES_MESSAGE', 'p.M', {}),
                               ('DECLARES_FIELD', 'p.M#1', {'type': {'resolution': 'unresolved', 'reason': 'DECLARATION_NOT_FOUND'}}),
                               ('DECLARES_SERVICE', 'p.S', {})])

    def comparison(self):
        okeys, orefs = oc.oracle_index(self.oracle_decls, self.oracle_refs)
        cfacts, crefs = oc.candidate_index(self.cand, ALIASES)
        return oc.compare(okeys, orefs, cfacts, crefs)

    def test_counts_agreement_and_every_kind_of_disagreement(self):
        result = self.comparison()
        self.assertEqual(result['declarations'], {'oracle': 3, 'candidate': 3, 'agree': 2, 'union': 4, 'agreement': 0.5})
        self.assertEqual(result['references'], {'compared': 1, 'same': 0, 'agreement': 0.0})
        self.assertEqual(sorted(d['kind'] for d in result['disagreements']),
                         ['candidate_only', 'oracle_only', 'reference_state'])

    def test_scoring_requires_complete_adjudication_and_applies_verdicts(self):
        result = self.comparison()
        verdicts = {d['id']: {'oracle_only': 'oracle', 'candidate_only': 'neither', 'reference_state': 'oracle'}[d['kind']]
                    for d in result['disagreements']}
        labels = [{'correct': 'yes'}] * 99 + [{'correct': 'no'}] + [{'correct': 'unsure'}]
        scored = oc.score(result, verdicts, labels)
        self.assertEqual(scored['outcome'], 'fail')  # 50% agreement is far below 99.5%
        self.assertAlmostEqual(scored['emission_precision'], 2 / 3)
        self.assertAlmostEqual(scored['declaration_recall'], 2 / 3)
        self.assertEqual(scored['oracle_sample_unsure'], 1)
        self.assertAlmostEqual(scored['oracle_sample_correct'], 0.99)
        with self.assertRaisesRegex(ValueError, 'every disagreement'):
            oc.score(result, dict(list(verdicts.items())[:1]), labels)
        unsure = {k: 'unsure' for k in verdicts}
        self.assertEqual(oc.score(result, unsure, labels)['outcome'], 'not_measured')

    def test_a_perfect_round_passes_and_an_unlabelled_sample_is_not_measured(self):
        self.cand = candidate([('DECLARES_MESSAGE', 'p.M', {}),
                               ('DECLARES_FIELD', 'p.M#1', {'type': {'resolution': 'same_file'}}),
                               ('DECLARES_FIELD', 'p.M#2', {})])
        result = self.comparison()
        self.assertEqual(result['disagreements'], [])
        self.assertEqual(oc.score(result, {}, [{'correct': 'yes'}] * 100)['outcome'], 'pass')
        self.assertEqual(oc.score(result, {}, [{'correct': 'unsure'}])['outcome'], 'not_measured')


if __name__ == '__main__':
    unittest.main()
