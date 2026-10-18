"""Fail-closed checker controls, assembled from reviewed source truth only."""
import copy
import hashlib
import json
from pathlib import Path
import unittest

import score

HERE = Path(__file__).resolve().parent


class ScoreTest(unittest.TestCase):
    def setUp(self):
        self.labels = json.loads((HERE / 'labels.json').read_text())
        self.binding = json.loads((HERE / 'committed-inputs.json').read_text())
        self.repository = self.binding['roles'][0]['repository']

    def page(self, level):
        endpoints = {}
        for side, truth in self.labels['endpoints'].items():
            endpoints[side] = {'protocol': 'protobuf', 'repository': self.repository,
                               'declaration_lineage': 'provisional_repo_path_v1_' + hashlib.sha256((self.repository + '\0' + truth['declaration_path']).encode()).hexdigest(),
                               'operation': truth['wire_operation']}
        page = {'schema_version': 'caller-comparison-v2', 'matching_rows_state': 'exact',
                'query': {'level': level, **endpoints}, 'total_rows': len(self.labels['expected_' + level + '_rows']), 'rows': []}
        for side in endpoints:
            page[side] = {'endpoint': endpoints[side], 'matching_rows_state': 'exact',
                          'generation': {'state': 'current', 'repository': self.repository, 'commit': self.binding['derived_input_commit']}}
        sites = {s['site_id']: s for s in self.labels['sites']}
        files = {f['path']: f for f in self.binding['files']}
        for n, truth in enumerate(self.labels['expected_' + level + '_rows']):
            row = {'key': str(n), 'level': level, 'classification': truth['classification'],
                   'old': {'rows': [], 'occurrence_count': truth['old_count'], 'rows_truncated': False},
                   'replacement': {'rows': [], 'occurrence_count': truth['replacement_count'], 'rows_truncated': False}}
            if truth.get('unit_id'):
                row['unit'] = {'id': truth['unit_id']}
            for key in truth['site_ids']:
                s = sites[key]; side = s['pair_side']; unit = s['unit']
                attribution = {'state': unit['state']}
                if unit['state'] == 'resolved':
                    attribution.update(candidates=[{'id': unit['id'], 'build_targets': unit['build_targets']}], candidate_total=1)
                else:
                    attribution['reason'] = unit['reason']
                source = {k: s[k] for k in ('path', 'start_byte', 'end_byte', 'start_line', 'end_line')}
                source.update(repository=self.repository, commit=self.binding['derived_input_commit'], object_id=files[s['path']]['git_blob'], blob_digest=s['upstream_blob_sha256'], citation='opaque', run_id='run', atom_id='atom')
                row[side]['rows'].append({'source': source, 'protocol': 'protobuf', 'operation': s['wire_operation'], 'declaration_lineage': endpoints[side]['declaration_lineage'], 'classification': 'resolved_caller', 'resolution': 'scip', 'tier': 'derived', 'fresh': True, 'unit': attribution})
            page['rows'].append(row)
        return page

    def test_current_source_truth_accepts_both_levels_and_paging(self):
        for level in ('occurrence', 'unit'):
            with self.subTest(level=level):
                page = self.page(level)
                pages = []
                for row in page['rows']:
                    one = copy.deepcopy(page); one['rows'] = [row]; pages.append(one)
                rows, citations = score.check_rows(pages, self.labels, self.binding, level, self.repository)
                self.assertEqual(len(rows), len(self.labels['expected_' + level + '_rows']))
                self.assertEqual(len(citations), 5)

    def test_resolved_syntax_keeps_heuristic_tier(self):
        page = self.page('unit')
        for row in page['rows']:
            for side in ('old', 'replacement'):
                for item in row[side]['rows']:
                    item.update(resolution='syntax', tier='heuristic')
        score.check_rows([page], self.labels, self.binding, 'unit', self.repository)

    def test_rejects_quality_and_authority_errors(self):
        mutations = {
            'missing': lambda p: p['rows'].pop(),
            'extra_duplicate': lambda p: p['rows'].append(copy.deepcopy(p['rows'][0])),
            'wrong_class': lambda p: p['rows'][0].update(classification='both_evidence'),
            'wrong_unit': lambda p: p['rows'][0].update(unit={'id': 'foreign'}),
            'wrong_protocol': lambda p: p['old']['endpoint'].update(protocol='grpc'),
            'wrong_commit': lambda p: p['old']['generation'].update(commit='f' * 40),
            'unavailable': lambda p: p.update(matching_rows_state='unavailable'),
            'wrong_side_count': lambda p: p['rows'][0]['old'].update(occurrence_count=2),
            'truncated': lambda p: p['rows'][0]['old'].update(rows_truncated=True),
            'crossed_lineage': lambda p: p['rows'][0]['old']['rows'][0].update(declaration_lineage='foreign'),
            'wrong_blob': lambda p: p['rows'][0]['old']['rows'][0]['source'].update(blob_digest='sha256:' + '0' * 64),
            'abstention': lambda p: p['rows'][0]['old']['rows'][0].update(classification='extractor_abstention'),
            'unresolved_resolution': lambda p: p['rows'][0]['old']['rows'][0].update(resolution='unresolved'),
            'unknown_resolution': lambda p: p['rows'][0]['old']['rows'][0].update(resolution='unknown'),
            'nonfresh': lambda p: p['rows'][0]['old']['rows'][0].update(fresh=False),
            'promoted_syntax': lambda p: p['rows'][0]['old']['rows'][0].update(resolution='syntax'),
            'invented_owner': lambda p: p['rows'][0]['old']['rows'][0]['unit']['candidates'][0].update(owners=['invented']),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                page = self.page('unit'); mutate(page)
                with self.assertRaises(score.Mismatch):
                    score.check_rows([page], self.labels, self.binding, 'unit', self.repository)


if __name__ == '__main__':
    unittest.main()
