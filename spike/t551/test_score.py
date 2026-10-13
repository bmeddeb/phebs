"""Fences for the T55.1 site scorer."""
import unittest

import score as s


def site(state, start, obj=None, cls=None):
    return {'unit_id': 'r@b', 'start_byte': start, 'end_byte': start + 5, 'state': state, 'object': obj, 'class': cls}


def row(pred, start, obj):
    return {'unit_id': 'r@b', 'start_byte': start, 'end_byte': start + 5, 'predicate': pred, 'object': obj, 'detail': ''}


class ScoreTest(unittest.TestCase):
    def test_classes(self):
        sites = [site('resolved', 0, obj='topic:a'), site('unresolved', 10, cls='selector-expr'),
                 site('unresolved', 20, cls='call-expr'), site('unresolved', 30, cls='unresolved-ident'),
                 site('no_topic_field', 40)]
        rows = [row('PRODUCES_TO_TOPIC', 0, 'topic:a'), row('UNRESOLVED_KAFKA_PRODUCER', 10, 'unresolved:selector-expr'),
                row('UNRESOLVED_KAFKA_PRODUCER', 20, 'unresolved:selector-expr'), row('PRODUCES_TO_TOPIC', 50, 'topic:x')]
        got = s.score(sites, rows, {})
        self.assertEqual(got['scored_sites'], 4)
        self.assertEqual(got['classes'], {'covered_agree': 2, 'covered_disagree': 1, 'missed': 1, 'duplicate': 0})
        self.assertEqual(got['extra'], 1)
        self.assertEqual(len(got['pending']), 3)  # the disagreement, the miss and the extra row
        adjudicated = {'r@b:20-25': {'truth': ['unresolved', 'selector-expr']}, 'r@b:30-35': {'truth': ['unresolved', 'unresolved-ident']},
                       'r@b:50-55': {'truth': ['not_a_site', None]}}
        again = s.score(sites, rows, adjudicated)
        self.assertEqual(again['classes']['covered_agree'], 3)
        self.assertEqual((again['pending'], again['extra_confirmed']), ([], 1))

    def test_a_wrong_resolved_topic_is_listed(self):
        got = s.score([site('resolved', 0, obj='topic:a')], [row('PRODUCES_TO_TOPIC', 0, 'topic:b')], {})
        self.assertEqual(got['wrong_topic'], ['r@b:0-5'])


if __name__ == '__main__':
    unittest.main()
