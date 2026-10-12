"""Synthetic export/replay fences; no retained predictions or labels read."""
import copy
import tempfile
import unittest
from pathlib import Path

import candidate_export as export
import capture_replay
import json
import binding_bundle


class CandidateExportTests(unittest.TestCase):
    def test_ledger_requires_observed_consideration_for_every_reported_site(self):
        sites = [{'site_id':name,'stratum':'repo:production','repository':'repo','path':'a.go',
                  'kind':'indirect_or_other_call','citation_spans':[[offset,offset+1]]}
                 for offset,name in enumerate(('reported','withheld','unvisited'))]
        universe={'sites':sites}
        projected={'precision':{'rows':[{'site_id':'reported'}]},'unresolved':{'rows':[]}}
        trace=[{'repository':'runtime','path':'a.go','start_byte':i,'end_byte':i+1} for i in range(2)]
        ledger=export.decision_ledger(universe,projected,trace,{'runtime':'repo'})
        self.assertEqual({r['site_id']:r['state'] for r in ledger},
                         {'reported':'reported','withheld':'withheld','unvisited':'not_considered'})
        with self.assertRaisesRegex(ValueError,'missing an observed consideration'):
            export.decision_ledger(universe,projected,trace[1:],{'runtime':'repo'})

    def test_replay_refuses_changed_chunks_results_or_attributes(self):
        original={'assertion_id':'a','predicate':'CALLS_OPERATION','subject':'s','object':'o',
                  'repo':'repo','run_id':'run','tier':'derived','code_role':'production','detail':'d'}
        retained={'authorities':[{'repository':'repo','run_id':'run'}], 'assertions':[original],
                  'chunks':[{'run_id':'run','chunk_id':'chunk','fact_count':1}]}
        replay=[{'kind':'chunk','repository':'repo','ordinal':0,'chunk_id':'chunk','fact_count':1,
                 'assertions':[{**original,'id':'a'}]},
                {'kind':'result','repository':'repo','ordinal':0,'spec':{'disposition':'success'}}]
        result={('repo',0):{'disposition':'success'}}
        export.reconcile_replay(retained,replay,result)
        mutations=[lambda rows:rows[0].update(chunk_id='other'),
                   lambda rows:rows[1]['spec'].update(disposition='empty'),
                   lambda rows:rows[0]['assertions'][0].update(detail='changed')]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                altered=copy.deepcopy(replay);mutate(altered)
                with self.assertRaises(ValueError): export.reconcile_replay(retained,altered,result)

    def test_round_bundle_fills_only_the_two_real_candidate_identities(self):
        root=Path(__file__).parent
        before=json.loads((root/'bundle/preregistration.run-g.bundle.json').read_text())
        after=json.loads((root/'bundle/preregistration.candidate-bound.run-g.bundle.json').read_text())
        receipt=json.loads((root/'candidate_bindings_20261011g.json').read_text())
        proof='sealed proof-bundle-v1 envelope identity and sha256 (precision, unresolved frames)'
        ledger='phebs complete candidate ledger identity and sha256 (abstention frame)'
        expected=copy.deepcopy(before)
        for key in (proof,ledger):
            self.assertIsNone(before['candidate_identities'][key])
            expected['candidate_identities'][key]=after['candidate_identities'][key]
        expected.pop('missing_bindings')
        expected['missing_bindings']=binding_bundle.validate_bundle(expected)
        self.assertEqual(after,expected)
        self.assertEqual(len(after['missing_bindings']),22)
        self.assertEqual(after['candidate_identities'][proof],{'identity':receipt['proof_identity'],
                         'sha256':receipt['artifacts_sha256']['proof-bundle.json']})
        self.assertEqual(after['candidate_identities'][ledger]['sha256'],receipt['artifacts_sha256']['call-site-decisions.jsonl'])
        self.assertTrue(receipt['replay_matches_all_chunks_results_and_assertion_attributes'])
        self.assertEqual(receipt['decision_ledger_population'],498793)
        for key in ('sample_drawn','labels_produced','predictions_disclosed','quality_scored',
                    'protocol_sealed','external_publication','acceptance_met'):
            self.assertIs(receipt[key],False)

    def test_overlay_refuses_ambiguous_injection_and_leaves_source_exact(self):
        with tempfile.TemporaryDirectory() as directory:
            repo=Path(directory)/'repo';out=Path(directory)/'out';out.mkdir()
            sources=repo/'internal/extract/extractors/gocaller';sources.mkdir(parents=True)
            (sources/'fallback.go').write_text('case *ast.CallExpr:\n')
            (sources/'gocaller.go').write_text('')
            before=(sources/'fallback.go').read_bytes()
            with self.assertRaisesRegex(ValueError,'injection point changed'):
                capture_replay.prepare_overlay(repo,out)
            self.assertEqual((sources/'fallback.go').read_bytes(),before)


if __name__ == '__main__': unittest.main()
