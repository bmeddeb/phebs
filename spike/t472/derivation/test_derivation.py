import copy
import json
from pathlib import Path
import unittest

from fill_lock import update_lock
from modules import enclosing_module


class DerivationTests(unittest.TestCase):
    def test_innermost_module_owns_sources(self):
        self.assertEqual(enclosing_module('api/v1/client.go', ['', 'api', 'api/v1']), 'api/v1')
        self.assertEqual(enclosing_module('api2/client.go', ['', 'api']), '')

    def test_update_preserves_disposition_refusals_and_prior_measurements(self):
        lock = json.loads((Path(__file__).resolve().parent.parent / 'corpus.lock.json').read_text())
        old = copy.deepcopy(lock)
        facts = {}
        for repo in lock['repos']:
            d = repo['derived']
            if not d['corpus_admitted']:
                continue
            i, s = d['index'], d['snapshots']
            facts[repo['name'].split('/')[1]] = {
                'pin': repo['commit'], 'derived_parents': [repo['commit']],
                'derived_commit': d['derived_commit'], 'derived_tree': d['derived_tree'],
                'files': d['files'],
                'merge': {'docs': i['documents'], 'occurrences': i['occurrences'], 'symbols': i['symbols'],
                          'out_of_tree_dropped': i['out_of_tree_build_cache_documents_dropped'],
                          'out_of_tree_testmain': 0, 'out_of_tree_other': [], 'version_skew_references': 7,
                          'version_skew_sample': [], 'external_symbols_dropped': i['external_symbols_dropped'],
                          'round_trip_unstable_docs': i['round_trip_unstable_documents'],
                          'bytes': i['bytes'], 'runs': i['module_runs'], 'module_rels': i['module_rels']},
                'snapshots': dict(s, roots=s['layout_roots'], roots_deduped=s['layout_roots_deduped'],
                                  gates=s['gates_passed'], mapping_table_sha256='test', over_blob_bound=[]),
            }
        out = update_lock(lock, facts, {'executed_utc': '2026-10-10'})
        self.assertEqual(lock, old)
        self.assertEqual(out['derivation']['disposition'], old['derivation']['disposition'])
        self.assertEqual(out['derivation']['refusals'], old['derivation']['refusals'])
        for before, after in zip(old['repos'], out['repos']):
            if before['derived']['corpus_admitted']:
                self.assertEqual(after['derived']['previous_derived'], before['derived'].get('previous_derived', before['derived']))
            else:
                self.assertEqual(after, before)
        for mutation in ('pin', 'parents', 'files', 'index_bound', 'source_bound', 'missing_repo'):
            broken = copy.deepcopy(facts)
            f = broken['etcd']
            if mutation == 'pin': f['pin'] = 'wrong'
            if mutation == 'parents': f['derived_parents'] = []
            if mutation == 'files': f['files']['extra'] = {}
            if mutation == 'index_bound': f['files']['index.scip']['bytes'] = (64 << 20) + 1
            if mutation == 'source_bound': f['snapshots']['regular_files'] = 200_000
            if mutation == 'missing_repo': del broken['etcd']
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                update_lock(lock, broken, {'executed_utc': '2026-10-10'})


if __name__ == '__main__':
    unittest.main()
