"""Static adversarial checks; no product, child, browser, build or socket."""
import copy
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch

import requalify
import requalification_browser as browser
import requalification_checks as checks
import requalification_monitor as monitor
import score

HERE = Path(__file__).resolve().parent


def page(old='old0', new='new0'):
    result = {'schema_version': 'caller-comparison-v2', 'matching_rows_state': 'exact', 'rows': []}
    for side, repo, commit in [('old', 'repo/old', old), ('replacement', 'repo/new', new)]:
        result[side] = {'matching_rows_state': 'exact', 'generation': {'state': 'current', 'repository': repo, 'commit': commit, **{key: commit + key for key in ('generation_digest','candidate_manifest_digest','resolver_manifest_digest','pair_set_digest','manifest_digest')}, 'publication_revision': 1}}
    return result


class RequalificationTests(unittest.TestCase):
    def test_unit_projection_uses_only_actual_outer_candidate_without_mutation(self):
        resolved = {"level": "unit", "classification": "both_evidence", "unit": {"id": "actual", "build_targets": ["pkg"]},
                    "old": {"rows": [{"unit": {"state": "resolved"}}]},
                    "replacement": {"rows": [{"unit": {"state": "resolved"}}]}}
        unavailable = {"level": "unit", "classification": "unresolved", "old": {"rows": [{"unit": {"state": "unavailable", "reason": "mapping_has_no_attribution", "candidates": [{"id": "placeholder"}]}}]}, "replacement": {"rows": []}}
        pages = [{"query": {"level": "unit"}, "rows": [resolved, unavailable]}]
        original = copy.deepcopy(pages)
        projected = checks.unit_sample_projection(pages)
        self.assertEqual(pages, original)
        self.assertEqual(projected[0]["rows"][0]["old"]["rows"][0]["unit"], {"state": "resolved", "candidates": [resolved["unit"]], "candidate_total": 1})
        self.assertEqual(projected[0]["rows"][1], unavailable)
        for mutate in (
            lambda p: p[0]["query"].update(level="occurrence"),
            lambda p: p[0]["rows"][0].update(classification="unresolved"),
            lambda p: p[0]["rows"][0]["unit"].pop("id"),
            lambda p: p[0]["rows"][0]["old"]["rows"][0]["unit"].update(candidates=[{"id": "foreign"}]),
            lambda p: p[0]["rows"][0]["old"]["rows"][0]["unit"].update(state="unavailable"),
            lambda p: p[0]["rows"][0]["old"]["rows"][0]["unit"].update(candidate_total=2),
        ):
            changed = copy.deepcopy(pages); mutate(changed)
            with self.assertRaises(score.Mismatch): checks.unit_sample_projection(changed)

    def test_deadline_never_resets_for_phases_or_cleanup(self):
        clock = [10]
        deadline = checks.Deadline(180, lambda: clock[0])
        self.assertEqual(deadline.timeout(30), 30)
        clock[0] = 185
        self.assertEqual(deadline.timeout(30), 5)
        clock[0] = 190
        with self.assertRaises(score.Mismatch): deadline.remaining()

    def test_protected_attempt_and_all_bounds_remain_exact(self):
        plan = checks.check_frozen(HERE)
        original = json.loads((HERE / 'preregistration.json').read_text())
        self.assertEqual(plan['measurement_controls']['bounds'], original['operating']['bounds'])
        self.assertEqual(checks.sha(HERE / 'baseline-result.json'), 'sha256:7e63806f9269a357fa056d46ff625a1a764da8930eb74df00d84341607163693')

    def test_filters_select_only_frozen_complete_rows(self):
        labels = json.loads((HERE / 'labels.json').read_text())
        for level, expected in [('occurrence',2),('unit',1)]:
            truth = checks.filtered_truth(labels,level,{'path_prefix':'features/multiplex/client/'})
            self.assertEqual(len(truth['expected_'+level+'_rows']),expected)
            self.assertEqual(len(truth['sites']),2)
        empty = checks.filtered_truth(labels,'occurrence',{'classification':'both_evidence'})
        self.assertEqual(empty['sites'],[])
        self.assertEqual(len(labels['sites']),9)

    def test_parallel_pages_must_match_one_complete_epoch(self):
        before, after = page(), page('old1','new0')
        endpoints = {'old':'repo/old','replacement':'repo/new'}
        for value in (before,after):
            self.assertEqual(checks.assert_transition_response(200,value,endpoints,before,after),'exact_epoch')
        for mutate in [lambda p:p['old']['generation'].update(manifest_digest='foreign'),lambda p:p['replacement']['generation'].update(commit='new1'),lambda p:p.update(matching_rows_state='unavailable',total_rows=0)]:
            crossed=copy.deepcopy(after);mutate(crossed)
            with self.assertRaises(score.Mismatch):checks.assert_transition_response(200,crossed,endpoints,before,after)
        with self.assertRaises(score.Mismatch):checks.assert_transition_response(500,{},endpoints,before,after)
        self.assertEqual(checks.assert_transition_response(409,{},endpoints,before,after),'explicit_refusal')
        with self.assertRaises(score.Mismatch):checks.assert_transition_response(409,{'rows':[{}]},endpoints,before,after)
        missing={'schema_version':'caller-comparison-v2','matching_rows_state':'unavailable','rows':[]}
        self.assertEqual(checks.assert_transition_response(200,missing,endpoints,before,after),'explicit_unavailable')

    def test_source_tuple_cannot_cross_a_current_epoch(self):
        value=page()
        value['rows']=[{'old':{'rows':[{'source':{'repository':'repo/old','commit':'old1','run_id':'foreign'}}]},'replacement':{'rows':[]}}]
        with self.assertRaises(score.Mismatch): checks.assert_authorities(value,{'old':'repo/old','replacement':'repo/new'},{'repo/old':'old0','repo/new':'new0'})

    def test_citation_checks_blob_range_and_actual_bytes(self):
        truth={'site_id':'src/a.go:1-4','selector':'c.M','start_line':1,'end_line':1}
        generation={'state':'current','repository':'repo','commit':'commit','generation_digest':'gen','candidate_manifest_digest':'candidate','resolver_manifest_digest':'resolver','pair_set_digest':'pairs','manifest_digest':'manifest','publication_revision':1}
        value={'generation':generation,'source':{'repository':'repo','commit':'commit','path':'src/a.go','start_byte':1,'end_byte':4,'start_line':1,'end_line':1,'object_id':'blob','blob_digest':'digest','run_id':'gen'},'content':'c.M'}
        checks.check_citation(value,truth,'repo','commit','blob','digest',generation)
        for key,wrong in [('commit','foreign'),('object_id','foreign'),('end_byte',5)]:
            bad=copy.deepcopy(value);bad['source'][key]=wrong
            with self.assertRaises(score.Mismatch):checks.check_citation(bad,truth,'repo','commit','blob','digest',generation)
        bad=copy.deepcopy(value);bad['content']='other'
        with self.assertRaises(score.Mismatch):checks.check_citation(bad,truth,'repo','commit','blob','digest',generation)
        for mutate in [lambda p:p['generation'].update(generation_digest='same-commit-other-generation'),lambda p:p['source'].update(run_id='wrong-run'),lambda p:p['generation'].update(publication_revision=2)]:
            bad=copy.deepcopy(value);mutate(bad)
            with self.assertRaises(score.Mismatch):checks.check_citation(bad,truth,'repo','commit','blob','digest',generation)

    def test_every_quality_page_pins_initial_authority_at_same_commit(self):
        first=page();commits={'repo/old':'old0','repo/new':'new0'}
        checks.assert_quality_authorities(first,first,commits)
        for mutate in [lambda p:p['old']['generation'].update(generation_digest='other'),lambda p:p['replacement']['generation'].update(publication_revision=2)]:
            bad=copy.deepcopy(first);mutate(bad)
            with self.assertRaises(score.Mismatch):checks.assert_quality_authorities(bad,first,commits)

    def test_http_total_deadline_aborts_headers_and_body_and_joins_timer(self):
        for phase in ['headers','body']:
            timer=Mock();connection=Mock();response=Mock()
            response.__enter__=Mock(return_value=response);response.__exit__=Mock(return_value=False)
            response.status=200;response.getheader.return_value='application/json'
            def timer_factory(seconds,callback):
                timer.callback=callback
                return timer
            def read(maximum):
                if phase=='body':timer.callback()
                return b'{}'
            def headers():
                if phase=='headers':timer.callback()
                return response
            response.read.side_effect=read;connection.getresponse.side_effect=headers
            with patch.object(requalify,'HTTPConnection',return_value=connection),patch.object(requalify.threading,'Timer',side_effect=timer_factory):
                with self.assertRaises(score.Mismatch):requalify.http('http://127.0.0.1:9999','/api/version',None,None,2,262144)
            connection.sock.shutdown.assert_called_once();timer.cancel.assert_called_once();timer.join.assert_called_once();connection.close.assert_called_once()

    def test_exact_success_cannot_cross_local_publication_deadline(self):
        matrix=object.__new__(requalify.Matrix)
        matrix.bounds={'publication_seconds':90,'cold_request_seconds':2};matrix.deadline=Mock();matrix.deadline.timeout.return_value=90
        matrix.get=Mock(return_value=(200,json.dumps(page()).encode()))
        params={'old_repository':'repo/old','replacement_repository':'repo/new'}
        with patch.object(requalify.time,'monotonic',side_effect=[0,91]):
            with self.assertRaises(score.Mismatch):matrix.exact(params,{'repo/old':'old0','repo/new':'new0'})

    def test_mcp_requires_single_result_event(self):
        self.assertEqual(requalify.rpc_result(b'data: {"id":1,"result":{"ok":true}}\n\n','text/event-stream'),{'ok':True})
        for raw in [b'data: {}\ndata: {}',b'data: {"id":1,"error":{}}']:
            with self.assertRaises(score.Mismatch):requalify.rpc_result(raw,'text/event-stream')

    def test_helper_override_accepts_only_reviewed_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            binding={'tools':{}}
            for key,name in requalify.HELPERS.items():
                (root/name).write_bytes(name.encode())
                binding['tools'][key]={'path':'old/'+name,'sha256':checks.sha(root/name)}
            requalify.select_helpers(binding,root)
            self.assertEqual(binding['tools']['zoekt_git_index']['path'],str(root.resolve()/'zoekt-git-index'))
            (root/'zoekt-git-index').write_bytes(b'foreign')
            with self.assertRaises(score.Mismatch):requalify.select_helpers(binding,root)

    def test_current_admission_uses_actual_closed_capability_names(self):
        self.assertTrue({'protobuf-field-references','kafka-topic-inventory','kafka-topic-usage','contract-compatibility'} <= requalify.DARK_CAPABILITIES)
        self.assertTrue({'find_field_references','find_proto_field_references','find_kafka_topic_usage','list_kafka_topics','check_wire_compatibility'} <= requalify.DARK_TOOLS)

    def test_pristine_input_census_binds_every_committed_blob(self):
        files=[{'path':'src/main.go','git_blob':'abc','bytes':3}]
        replies=['commit\n','tree\n','','100644 blob abc 3\tsrc/main.go\0']
        with patch.object(requalify.qualify,'command',side_effect=replies):
            value=requalify.verify_input_repository('git','never-read','commit','tree',files)
            self.assertEqual((value['files'],value['bytes']),(1,3))
        for bad in ['100644 blob other 3\tsrc/main.go\0','120000 blob abc 3\tsrc/main.go\0','100644 blob abc 4\tsrc/main.go\0','100644 blob abc 3\tsrc/main.go\0'+'100644 blob abc 3\textra.go\0']:
            with patch.object(requalify.qualify,'command',side_effect=replies[:3]+[bad]):
                with self.assertRaises(score.Mismatch):requalify.verify_input_repository('git','never-read','commit','tree',files)

    def test_owned_child_joins_without_a_real_process(self):
        child=Mock(pid=999,returncode=0)
        child.poll.return_value=None
        with tempfile.TemporaryDirectory() as directory, patch.object(browser.subprocess,'Popen',return_value=child),patch.object(browser.qualify.serve.receipts,'_drain_group',return_value='clean') as drain:
            owned=browser.OwnedChild(['never-executed'],Path(directory),'mock')
            result=owned.stop()
            child.terminate.assert_called_once();child.wait.assert_called_once_with(timeout=3)
            drain.assert_called_once_with(999,False)
            self.assertEqual(result['teardown'],'clean')

    def test_owned_child_timeout_is_joined_and_truthfully_named(self):
        child=Mock(pid=999,returncode=-9);child.poll.return_value=None
        child.wait.side_effect=[subprocess.TimeoutExpired('mock',3),None]
        with tempfile.TemporaryDirectory() as directory, patch.object(browser.subprocess,'Popen',return_value=child),patch.object(browser.os,'killpg') as kill,patch.object(browser.qualify.serve.receipts,'_drain_group',return_value='clean'):
            with self.assertRaises(score.Mismatch):browser.OwnedChild(['never-executed'],Path(directory),'mock').stop()
            kill.assert_called_once()
            self.assertEqual(child.wait.call_count,2)

    def test_stray_group_is_drained_but_never_upgraded_to_clean_pass(self):
        child=Mock(pid=999,returncode=0);child.poll.return_value=0
        with tempfile.TemporaryDirectory() as directory,patch.object(browser.subprocess,'Popen',return_value=child),patch.object(browser.os,'killpg') as kill,patch.object(browser.qualify.serve.receipts,'_drain_group',side_effect=['unclean','killed_stray_members']):
            with self.assertRaises(score.Mismatch):browser.OwnedChild(['never-executed'],Path(directory),'mock').stop()
            kill.assert_called_once()

    def test_host_sample_timeout_terminates_and_joins_owned_vm_stat(self):
        value=object.__new__(monitor.JoinedMonitor)
        value.host_lock=threading.Lock();value.host_child=None;value.host_terminals=[]
        child=Mock(pid=999,returncode=-15);child.poll.return_value=None
        child.communicate.side_effect=subprocess.TimeoutExpired('mock',2)
        with patch.object(monitor.subprocess,'Popen',return_value=child):
            with self.assertRaises(subprocess.TimeoutExpired):value.host_sample()
        child.terminate.assert_called_once();child.wait.assert_called_once_with(timeout=0.5)
        self.assertIsNone(value.host_child);self.assertTrue(value.host_terminals[0]['joined'])
        child.stdout.close.assert_called_once();child.stderr.close.assert_called_once()

    def test_live_sampler_prevents_data_custody_deletion(self):
        matrix=object.__new__(requalify.Matrix)
        matrix.monitor=Mock(joined=False,ownership={'sampling_thread_joined':False})
        matrix.monitor.stop.side_effect=ValueError('sampler not joined')
        matrix.session=None;matrix.report={'phases':[{}]};matrix.deadline=Mock()
        with self.assertRaises(ValueError):matrix.stop()
        self.assertIsNotNone(matrix.monitor)
        self.assertFalse(matrix.report['phases'][0]['operating_ownership']['sampling_thread_joined'])

    def test_startup_and_stop_terminal_keep_actual_owned_pgid_after_pid_clear(self):
        terminal={'outcome':'failed','exit_code':1,'teardown':'unclean','wall_seconds':1}
        for mode in [None,'graceful']:
            session=requalify.OwnedServeSession(Path('never-used'),'mock',['never-executed'],'http://127.0.0.1:9999')
            session.pid=999
            def finish(original,*args,**kwargs):
                original.pid=None
                return terminal
            with patch.object(requalify.serve.ServeSession,'_finish',autospec=True,side_effect=finish):
                result=session._finish('failed',None,None,mode=mode,reason='mock terminal')
            self.assertIsNone(session.pid);self.assertEqual(session.owned_pgid,999)
            matrix=object.__new__(requalify.Matrix);matrix.monitor=None;matrix.server_groups_drained=False
            phase={}
            with patch.object(requalify.os,'killpg') as kill,patch.object(requalify.serve.receipts,'_drain_group',return_value='killed_stray_members') as drain:
                matrix.record_terminal(phase,session,result)
            kill.assert_called_once_with(999,requalify.signal.SIGKILL)
            drain.assert_called_once_with(999,True)
            self.assertTrue(matrix.server_groups_drained);requalify.require_derived_cleanup(matrix)
            self.assertEqual(phase['terminal']['outcome'],'failed')

    def test_surviving_or_unknown_server_group_blocks_derived_deletion(self):
        matrix=object.__new__(requalify.Matrix);matrix.monitor=None;matrix.server_groups_drained=False
        terminal={'outcome':'failed','exit_code':1,'teardown':'unclean','wall_seconds':1}
        with patch.object(requalify.os,'killpg'),patch.object(requalify.serve.receipts,'_drain_group',return_value='unclean'):
            matrix.record_terminal({},Mock(pid=None,owned_pgid=999),terminal)
        with self.assertRaises(score.Mismatch):requalify.require_derived_cleanup(matrix)
        with self.assertRaises(score.Mismatch):matrix.record_terminal({},Mock(pid=None,owned_pgid=None),terminal)
        with self.assertRaises(score.Mismatch):requalify.require_derived_cleanup(matrix)

    def test_git_selection_replace_never_exposes_partial_live_config(self):
        with tempfile.TemporaryDirectory() as directory:
            matrix=object.__new__(requalify.Matrix);matrix.rewrite=Path(directory)/'gitconfig'
            matrix.rewrite.write_text('old complete')
            matrix.active_roles={'primary':{'local_path':'/bound/primary','repository':'evaluation.local/primary'},'copy':{'local_path':'/bound/copy','repository':'evaluation.local/copy'}}
            actual_replace=os.replace
            def replace(source,target):
                self.assertEqual(Path(target).read_text(),'old complete')
                text=Path(source).read_text()
                self.assertIn('https://evaluation.local/primary.git',text);self.assertIn('https://evaluation.local/copy.git',text)
                self.assertEqual(Path(source).stat().st_mode&0o777,0o600)
                actual_replace(source,target)
            with patch.object(requalify.os,'replace',side_effect=replace) as swap:
                matrix.write_rewrite()
            swap.assert_called_once();self.assertEqual(list(Path(directory).glob('.gitconfig-*')),[])

    def test_sampler_forced_cancellation_joins_host_observer_and_thread(self):
        value=object.__new__(monitor.JoinedMonitor)
        value.done=Mock();value.thread=Mock();value.thread.is_alive.side_effect=[True,False,False]
        value.host_lock=threading.Lock();value.host_child=Mock(pid=998,returncode=0);value.host_child.poll.return_value=None
        value.host_terminals=[];value.child=Mock(pid=999,returncode=0);value.child.poll.return_value=None
        value.stderr=Mock();value.failure=None;value.joined=False;value.ownership={}
        with self.assertRaises(ValueError):value.stop()
        value.host_child.terminate.assert_called_once();value.host_child.wait.assert_called_once_with(timeout=0.5)
        value.child.kill.assert_called_once();self.assertTrue(value.ownership['sampling_thread_joined'])
        self.assertEqual(value.thread.join.call_count,3)

    def test_normal_monitor_stop_joins_before_frozen_metrics_projection(self):
        value=object.__new__(monitor.JoinedMonitor)
        value.done=Mock();value.thread=Mock();value.thread.is_alive.return_value=False
        value.host_child=None;value.host_terminals=[{'exit_code':0,'joined':True,'forced':False}]
        value.child=Mock(returncode=0);value.child.poll.return_value=0
        value.stderr=Mock();value.failure=None;value.joined=False;value.ownership={}
        value.samples=[{'rss_bytes':100,'descendants':1}]
        value.headroom=[{'available_physical_bytes':4294967296,'formula':'same frozen formula'}];value.disk=[100]
        result=value.stop()
        self.assertTrue(value.joined);self.assertTrue(result['ownership']['sampling_thread_joined'])
        self.assertEqual(result['sampled_max_tree_rss_bytes'],100)
        value.child.kill.assert_not_called()

    def test_observer_launch_failure_closes_log_without_a_real_child(self):
        log=io.StringIO()
        with patch.object(Path,'open',return_value=log),patch.object(monitor.subprocess,'Popen',side_effect=OSError('mock launch failure')):
            with self.assertRaises(OSError):monitor.JoinedMonitor(Path('mock'),999,Path('mock'),{},Path('mock'))
        self.assertTrue(log.closed)


if __name__ == '__main__': unittest.main()
