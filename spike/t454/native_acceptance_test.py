import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('native_acceptance', Path(__file__).with_name('native_acceptance.py'))
n = importlib.util.module_from_spec(spec)
spec.loader.exec_module(n)


def fixture_config():
    h = n.digest(b'fixed')
    return dict(zip(n.CONFIG_KEYS, [
        'phebs-typed-native-acceptance-v1', 'neutral-1', 'a' * 40,
        dict(repository=n.REPO, incarnation='fixture', generation=h, commit='b' * 40),
        h, h, h, h, h, h, h, h, h, h, 1, h, dict(n.POLICY)]))


def fixture_corpus_config(cohort='ordinary'):
    c = fixture_config()
    c['schema'] = n.CORPUS_SCHEMA
    c['source']['repository'] = n.CORPUS_REPO
    c['source']['commit'] = n.CORPUS_COMMIT
    c['cohort'] = cohort
    c['source_git_sha256'] = n.digest(b'git')
    return c


def fixture_corpus_receipt(cohort):
    count = {'ordinary': (3, 535, 148), 'proto': (1, 1309, 241), 'fanout': (13, 13278, 2350)}[cohort]
    names = {'ordinary': ('NewOutWriter', 'NewErrWriter'), 'proto': ('NewRemoteErrorResult',),
             'fanout': ('NewOutWriter', 'NewErrWriter', 'NewRemoteErrorResult')}[cohort]
    rows = []
    for name in names:
        points = 16 if cohort == 'fanout' and name == 'NewRemoteErrorResult' else 1
        rows.append(dict(name=name, symbol_sha256=n.digest(name.encode()), query_points=points,
                         definition_locations=0 if cohort == 'fanout' else 1,
                         hover_payloads=0 if cohort == 'fanout' else 1,
                         reference_points=points if cohort == 'fanout' else 0))
    proof = dict(cohort=cohort, commit=n.CORPUS_COMMIT, archive_sha256=n.ARCHIVE_SHA256,
                 oracle_sha256=n.ORACLE_SHA256, root_digest=n.digest(b'root'), plan_digest=n.digest(b'plan'),
                 member_digests=[n.digest(cohort.encode())], documents=count[0], occurrences=count[1], definitions=count[2],
                 generated_documents=0, navigation_verified=True, source_git_drained=True, symbols=rows,
                 cross_cohort='pending', cost_gate='unavailable',
                 cost_missing=['sampled_child_lifetimes', 'sampled_fd_counts', 'private_cache_inventory'])
    return dict(schema=n.CORPUS_SCHEMA, config=n.digest(cohort.encode()), case='success',
                **{'pass': True}, native_absent=True, workspace_drained=True, engine_joined=True,
                result=dict(settled=True, no_replay=True, publication_verified=True, growth_released=True,
                            execution_error=False, corpus=proof))


def fixture_measured_corpus_receipt(cohort):
    receipt = fixture_corpus_receipt(cohort)
    costs, reports, controls = [], [], []
    planning, attempt = n.digest(b'planning'), n.digest(b'attempt')
    for phase in ('plan', 'execute'):
        metrics = dict(schema='phebs-typed-native-corpus-cost-v1',
                       observations=dict(version='phebs-t451b-sampled-observations-v1', interval_nanoseconds=50000000,
                                         duration_nanoseconds=1, samples=2, sampled_child_lifetimes=1,
                                         child_lifetimes_lower_bound=True, sampled_process_fd_peak=2,
                                         sampled_aggregate_fd_peak=2, fd_counts_non_atomic=True,
                                         vanished=0, raced=0, unexpected_errors=0, unavailable=False),
                       cache=dict(version='phebs-t451b-private-cache-v1',
                                  roots=['/scratch/'+k for k in ('bazel-user', 'bazel-output', 'repository-cache', 'gocache', 'gomodcache', 'cache')],
                                  missing_roots=[], entries=6, regular_files=0, directories=6, symlinks=0,
                                  unique_inodes=6, logical_bytes=0, allocated_bytes=0, complete=True))
        costs.append(dict(phase=phase, planning_digest=planning, attempt_digest=attempt,
                          request_digest=planning if phase == 'plan' else n.digest(b'execute'),
                          seal_digest=n.digest(phase.encode()), stdout_sha256=n.digest(b'worker-result'),
                          stderr_sha256=n.digest(n.canonical(metrics)+b'\n'), worker_output_bytes=4096, metrics=metrics))
        controls.append(dict(phase=phase, seal=n.digest(phase.encode()),
                             allowance=dict(planning_digest=planning, attempt_digest=attempt)))
        resources = dict(memory_oom_events=0, memory_oom_kills=0, memory_limit_events=0, task_limit_events=0,
                         sampling_unavailable=False, limits_verified=True, memory_peak_bytes=1,
                         sampled_peak_rss_bytes=1, sampled_peak_processes=2, sampled_peak_scratch_bytes=1,
                         sampled_peak_scratch_inodes=6, samples=2, per_process_descriptors=128,
                         aggregate_descriptor_ceiling=128*294)
        reports.append(dict(Phase=phase, ExitCode=0, Removed=True, StopReason='', Failure=None, Resources=resources))
    receipt['observations'] = controls
    receipt['result']['outcome'] = dict(Reports=reports)
    receipt['result']['corpus'].update(cost_gate='pass', cost_missing=[], costs=costs)
    return receipt


def fixture_root_measured_corpus_receipt(cohort):
    receipt = fixture_measured_corpus_receipt(cohort)
    costs = receipt['result']['corpus']['costs']
    for i, cost in enumerate(costs):
        metrics = cost.pop('metrics')
        worker_cache = dict(schema='phebs-typed-native-corpus-worker-cache-v2', cache=metrics['cache'])
        cost['stderr_sha256'] = n.digest(n.canonical(worker_cache)+b'\n')
        control = dict(planning_digest=cost['planning_digest'], attempt_digest=cost['attempt_digest'],
                       request_digest=cost['request_digest'], phase=cost['phase'],
                       seal_digest=cost['seal_digest'], device=1, inode=2)
        allowance = dict(schema='phebs-typed-allowance-v1', planning_digest=cost['planning_digest'],
                         attempt_digest=cost['attempt_digest'], boot_id='12345678-1234-1234-1234-123456789abc',
                         time_device=1, time_inode=2, start_boottime_ns=1, deadline_boottime_ns=300000000001,
                         worker_bytes_used=4096 if i else 0, wire_bytes_used=8192 if i else 0)
        witness = dict(schema='phebs-typed-native-host-cost-witness-v2', control=control, allowance=allowance,
                       container_id='a'*64, worker_start='2', supervisor_start='1', private_worker_pid=2,
                       namespace_device=1, namespace_inode=2, proc_device=3, proc_inode=4,
                       stdout_sha256=cost['stdout_sha256'], stderr_sha256=cost['stderr_sha256'],
                       observations=metrics['observations'])
        cost.update(host_witness=witness, host_witness_sha256=n.digest(n.canonical(witness)), worker_cache=worker_cache)
        receipt['observations'][i].update(id=witness['container_id'], worker_start=witness['worker_start'],
                                          allowance=copy.deepcopy(allowance))
    return receipt


def rebind_root_cost(receipt, index=1):
    """Keep hashes coherent so semantic negatives reach their actual fence."""
    cost = receipt['result']['corpus']['costs'][index]
    witness = cost['host_witness']
    cost['stderr_sha256'] = n.digest(n.canonical(cost['worker_cache'])+b'\n')
    witness['stderr_sha256'] = cost['stderr_sha256']
    cost['host_witness_sha256'] = n.digest(n.canonical(witness))
    receipt['observations'][index]['allowance'] = copy.deepcopy(witness['allowance'])


class AcceptanceTests(unittest.TestCase):
    def test_legacy_measurement_and_neutral_golden_bytes(self):
        self.assertEqual(n.digest(n.canonical(fixture_config())),
                         'sha256:3ff6e5582144614b9685e9090855cb41b3a6d356ba3ab482708946ec2eebeead')
        receipt = fixture_measured_corpus_receipt('ordinary')
        self.assertEqual(n.digest(n.canonical(receipt)),
                         'sha256:5d3452d6a29206eb0f74f7007b799935ebf569066e6dc12dc4843383a7c2740e')
        self.assertTrue(n.corpus_cost(receipt, receipt['result']['corpus']))
        for cost in receipt['result']['corpus']['costs']:
            self.assertEqual(set(cost), {'phase', 'planning_digest', 'attempt_digest', 'request_digest',
                                        'seal_digest', 'stdout_sha256', 'stderr_sha256', 'worker_output_bytes', 'metrics'})
            self.assertEqual(cost['metrics']['schema'], 'phebs-typed-native-corpus-cost-v1')

    def test_closed_corpus_config_preserves_neutral_wire(self):
        neutral = fixture_config()
        self.assertEqual(tuple(n.config(n.canonical(neutral))), n.CONFIG_KEYS)
        self.assertNotIn(b'cohort', n.canonical(neutral))
        for cohort in n.COHORTS:
            c = fixture_corpus_config(cohort)
            self.assertEqual(n.config(n.canonical(c)), c)
        c = fixture_corpus_config()
        for change in [lambda v: v.update(cohort='all'), lambda v: v['source'].update(commit='a'*40),
                       lambda v: v['source'].update(repository=n.REPO), lambda v: v.update(source_git_sha256=''),
                       lambda v: v.update(schema='phebs-typed-native-acceptance-v1')]:
            v = copy.deepcopy(c); change(v)
            with self.assertRaises(ValueError): n.config(n.canonical(v))

    def test_corpus_cross_cohort_product_proof_keeps_cost_open(self):
        with tempfile.TemporaryDirectory() as d:
            paths = {c: Path(d).resolve()/c for c in n.COHORTS}
            for cohort, path in paths.items(): path.write_bytes(n.canonical(fixture_corpus_receipt(cohort)))
            proof = n.compare_corpus(paths)
            self.assertEqual(proof['cross_cohort_identity'], 'pass')
            self.assertEqual(proof['product_navigation'], 'pass')
            self.assertEqual(proof['cost_gate'], 'unavailable')
            self.assertIs(proof['registration_ready'], False)
            for change in [lambda r: r['result']['corpus']['symbols'][0].update(symbol_sha256=n.digest(b'mismatch')),
                           lambda r: r['result']['corpus'].update(source_git_drained=False),
                           lambda r: r['result']['corpus'].update(occurrences=13277),
                           lambda r: r['result']['corpus'].update(cost_gate='pass'),
                           lambda r: r['result']['corpus']['symbols'][0].update(reference_points=0),
                           lambda r: r.update(engine_joined=False)]:
                r = fixture_corpus_receipt('fanout'); change(r)
                paths['fanout'].write_bytes(n.canonical(r))
                with self.assertRaises(ValueError): n.compare_corpus(paths)
                paths['fanout'].write_bytes(n.canonical(fixture_corpus_receipt('fanout')))

    def test_measured_corpus_requires_qualified_bound_both_phase_costs(self):
        with tempfile.TemporaryDirectory() as d:
            paths = {c: Path(d).resolve()/c for c in n.COHORTS}
            for cohort, path in paths.items(): path.write_bytes(n.canonical(fixture_measured_corpus_receipt(cohort)))
            proof = n.compare_corpus(paths)
            self.assertEqual(proof['cost_gate'], 'pass')
            self.assertIs(proof['registration_ready'], True)
            original = fixture_measured_corpus_receipt('fanout')
            mutations = [lambda r: r['result']['corpus']['costs'].pop(),
                         lambda r: r['result'].update(cost_stop={'diagnostic_only': True, 'completion_verified': False}),
                         lambda r: r['result']['corpus']['costs'][1].update(phase='plan'),
                         lambda r: r['result']['corpus']['costs'][1].update(attempt_digest=n.digest(b'foreign')),
                         lambda r: r['result']['corpus']['costs'][1].update(stderr_sha256=n.digest(b'foreign')),
                         lambda r: r['observations'][1].update(seal=n.digest(b'foreign')),
                         lambda r: r['result']['corpus']['costs'][1].update(worker_output_bytes=n.POLICY['output_bytes']),
                         lambda r: r['result']['outcome']['Reports'][1].update(Removed=False),
                         lambda r: r['result']['outcome']['Reports'][1]['Resources'].update(sampling_unavailable=True),
                         lambda r: r['result']['outcome']['Reports'][1]['Resources'].update(memory_peak_bytes=n.POLICY['memory_bytes']+1)]
            for mutate in mutations:
                value = copy.deepcopy(original); mutate(value)
                paths['fanout'].write_bytes(n.canonical(value))
                with self.assertRaises(ValueError): n.compare_corpus(paths)
            for mutate in [lambda m: m['observations'].update(unavailable=True),
                           lambda m: m['observations'].update(child_lifetimes_lower_bound=False),
                           lambda m: m['observations'].update(fd_counts_non_atomic=False),
                           lambda m: m['observations'].update(sampled_process_fd_peak=129),
                           lambda m: m['observations'].update(duration_nanoseconds=300000000001),
                           lambda m: m['cache'].update(complete=False),
                           lambda m: m['cache'].update(allocated_bytes=n.POLICY['scratch_bytes']+1)]:
                value = copy.deepcopy(original)
                cost = value['result']['corpus']['costs'][1]
                mutate(cost['metrics']); cost['stderr_sha256'] = n.digest(n.canonical(cost['metrics'])+b'\n')
                paths['fanout'].write_bytes(n.canonical(value))
                with self.assertRaises(ValueError): n.compare_corpus(paths)
            paths['fanout'].write_bytes(n.canonical(fixture_corpus_receipt('fanout')))
            proof = n.compare_corpus(paths)
            self.assertEqual(proof['cost_gate'], 'unavailable')
            self.assertIs(proof['registration_ready'], False)

    def test_root_measured_corpus_requires_both_bound_phases(self):
        with tempfile.TemporaryDirectory() as d:
            paths = {c: Path(d).resolve()/c for c in n.COHORTS}
            for cohort, path in paths.items():
                path.write_bytes(n.canonical(fixture_root_measured_corpus_receipt(cohort)))
            proof = n.compare_corpus(paths)
            self.assertEqual(proof['cost_gate'], 'pass')
            self.assertIs(proof['registration_ready'], True)
            original = fixture_root_measured_corpus_receipt('fanout')
            changes = {
                'missing-phase': lambda r: r['result']['corpus']['costs'].pop(),
                'old-stop': lambda r: r['result'].update(cost_stop={'diagnostic_only': True}),
                'host-stop': lambda r: r['result'].update(host_cost_stop={'diagnostic_only': True}),
                'foreign-phase': lambda r: r['result']['corpus']['costs'][1].update(phase='plan'),
                'foreign-owner': lambda r: r['result']['corpus']['costs'][1].update(attempt_digest=n.digest(b'foreign')),
                'changed-witness': lambda r: r['result']['corpus']['costs'][1]['host_witness'].update(proc_inode=5),
                'changed-cache': lambda r: r['result']['corpus']['costs'][1]['worker_cache']['cache'].update(allocated_bytes=512),
                'foreign-stdout': lambda r: r['result']['corpus']['costs'][1].update(stdout_sha256=n.digest(b'foreign')),
                'foreign-stderr': lambda r: r['result']['corpus']['costs'][1].update(stderr_sha256=n.digest(b'foreign')),
                'foreign-container': lambda r: r['observations'][1].update(id='b'*64),
                'foreign-worker': lambda r: r['observations'][1].update(worker_start='3'),
                'foreign-seal': lambda r: r['observations'][1].update(seal=n.digest(b'foreign')),
                'foreign-allowance': lambda r: r['observations'][1]['allowance'].update(time_inode=3),
                'allowance-type-alias': lambda r: r['observations'][1]['allowance'].update(time_device=True),
                'physical-output': lambda r: r['result']['corpus']['costs'][1].update(worker_output_bytes=n.POLICY['output_bytes']),
                'missing-completion': lambda r: r['result']['outcome']['Reports'][1].update(Removed=False),
                'mixed-methods': lambda r: r['result']['corpus']['costs'].__setitem__(1, fixture_measured_corpus_receipt('fanout')['result']['corpus']['costs'][1]),
                'missing-witness': lambda r: r['result']['corpus']['costs'][1].pop('host_witness'),
                'missing-cache': lambda r: r['result']['corpus']['costs'][1].pop('worker_cache'),
                'v1-metrics-added': lambda r: r['result']['corpus']['costs'][1].update(metrics={}),
                'scalar-completion': lambda r: r['result']['corpus']['costs'][1]['host_witness'].update(verified=True),
            }
            for name, mutate in changes.items():
                with self.subTest(name=name):
                    value = copy.deepcopy(original); mutate(value)
                    paths['fanout'].write_bytes(n.canonical(value))
                    with self.assertRaises(ValueError): n.compare_corpus(paths)

    def test_root_measurement_semantic_tampering_cannot_rehash_to_pass(self):
        original = fixture_root_measured_corpus_receipt('ordinary')
        changes = {
            'control-request': lambda w, c: w['control'].update(request_digest=w['control']['planning_digest']),
            'control-extra': lambda w, c: w['control'].update(verified=True),
            'control-device': lambda w, c: w['control'].update(device=0),
            'control-bool': lambda w, c: w['control'].update(inode=True),
            'host-schema': lambda w, c: w.update(schema='phebs-typed-native-corpus-cost-v1'),
            'cache-schema': lambda w, c: c.update(schema='phebs-typed-native-corpus-cost-v1'),
            'clock-domain': lambda w, c: w['allowance'].update(time_inode=3),
            'deadline': lambda w, c: w['allowance'].update(deadline_boottime_ns=300000000002),
            'boot-uuid': lambda w, c: w['allowance'].update(boot_id='not-a-boot'),
            'start-overflow': lambda w, c: w['allowance'].update(start_boottime_ns=1 << 63, deadline_boottime_ns=(1 << 63)+300000000000),
            'cumulative-output': lambda w, c: w['allowance'].update(worker_bytes_used=4097),
            'no-wire': lambda w, c: w['allowance'].update(wire_bytes_used=0),
            'wire-cap': lambda w, c: w['allowance'].update(wire_bytes_used=(24 << 20)+1),
            'private-pid1': lambda w, c: w.update(private_worker_pid=1),
            'private-pid-overflow': lambda w, c: w.update(private_worker_pid=1 << 32),
            'namespace-zero': lambda w, c: w.update(namespace_inode=0),
            'proc-zero': lambda w, c: w.update(proc_device=0),
            'supervisor-leading-zero': lambda w, c: w.update(supervisor_start='01'),
            'supervisor-overflow': lambda w, c: w.update(supervisor_start=str(1 << 64)),
            'sticky': lambda w, c: w['observations'].update(unavailable=True),
            'error': lambda w, c: w['observations'].update(unexpected_errors=1),
            'failure': lambda w, c: w['observations'].update(failure='process'),
            'interval': lambda w, c: w['observations'].update(interval_nanoseconds=100000000),
            'samples': lambda w, c: w['observations'].update(samples=1),
            'lifetimes': lambda w, c: w['observations'].update(sampled_child_lifetimes=65537),
            'lower-bound': lambda w, c: w['observations'].update(child_lifetimes_lower_bound=False),
            'fd-atomic': lambda w, c: w['observations'].update(fd_counts_non_atomic=False),
            'fd-bound': lambda w, c: w['observations'].update(sampled_process_fd_peak=129),
            'counter-overflow': lambda w, c: w['observations'].update(vanished=1 << 64),
            'shared-wall': lambda w, c: w['observations'].update(duration_nanoseconds=300000000000),
            'incomplete-cache': lambda w, c: c['cache'].update(complete=False),
            'cache-roots': lambda w, c: c['cache'].update(roots=['/inputs']),
            'cache-cap': lambda w, c: c['cache'].update(allocated_bytes=n.POLICY['scratch_bytes']+1),
            'cache-count': lambda w, c: c['cache'].update(entries=7),
            'cache-duplicate-root': lambda w, c: c['cache'].update(missing_roots=['/scratch/cache']*2),
            'cache-extra': lambda w, c: c['cache'].update(path='/private'),
            'witness-order': lambda w, c: w.__setitem__('schema', w.pop('schema')),
            'cache-order': lambda w, c: c.__setitem__('schema', c.pop('schema')),
        }
        for name, mutate in changes.items():
            with self.subTest(name=name):
                value = copy.deepcopy(original)
                cost = value['result']['corpus']['costs'][1]
                mutate(cost['host_witness'], cost['worker_cache'])
                rebind_root_cost(value)
                with self.assertRaises(ValueError): n.corpus_cost(value, value['result']['corpus'])
        for mutate in [lambda r: r['result']['outcome']['Reports'][1]['Resources'].update(sampling_unavailable=True),
                       lambda r: r['result']['outcome']['Reports'][1]['Resources'].update(memory_oom_events=False),
                       lambda r: r['result']['outcome']['Reports'][1]['Resources'].update(samples=1 << 64),
                       lambda r: r['result']['outcome']['Reports'][1]['Resources'].update(memory_peak_bytes=n.POLICY['memory_bytes']+1)]:
            value = copy.deepcopy(original); mutate(value)
            with self.assertRaises(ValueError): n.corpus_cost(value, value['result']['corpus'])
        # Zero plan spending is mandatory even when both recorded allowances agree.
        value = copy.deepcopy(original)
        value['result']['corpus']['costs'][0]['host_witness']['allowance']['worker_bytes_used'] = 1
        rebind_root_cost(value, 0)
        with self.assertRaises(ValueError): n.corpus_cost(value, value['result']['corpus'])

    def test_root_measurement_exact_shared_output_and_wall_boundaries(self):
        value = fixture_root_measured_corpus_receipt('ordinary')
        costs = value['result']['corpus']['costs']
        costs[1]['worker_output_bytes'] = n.POLICY['output_bytes']-costs[0]['worker_output_bytes']
        for i, cost in enumerate(costs):
            cost['host_witness']['observations']['duration_nanoseconds'] = 150000000000
            rebind_root_cost(value, i)
        self.assertTrue(n.corpus_cost(value, value['result']['corpus']))
        overflow = copy.deepcopy(value)
        overflow['result']['corpus']['costs'][1]['worker_output_bytes'] += 1
        with self.assertRaises(ValueError): n.corpus_cost(overflow, overflow['result']['corpus'])
        overflow = copy.deepcopy(value)
        overflow['result']['corpus']['costs'][1]['host_witness']['observations']['duration_nanoseconds'] += 1
        rebind_root_cost(overflow)
        with self.assertRaises(ValueError): n.corpus_cost(overflow, overflow['result']['corpus'])
        # A cache-only frame cannot claim a successful provider result's stdout.
        cache_only = fixture_root_measured_corpus_receipt('ordinary')
        cache_only['result']['corpus']['costs'][1]['worker_output_bytes'] = len(n.canonical(costs[1]['worker_cache']))+1
        with self.assertRaises(ValueError): n.corpus_cost(cache_only, cache_only['result']['corpus'])

    def test_remote_stage_admits_only_bounded_corpus_git_input(self):
        import ast
        tree = ast.parse(n.REMOTE)
        function = next(x for x in tree.body if isinstance(x, ast.FunctionDef) and x.name == 'fixed_input')
        scope = {}
        exec(compile(ast.Module(body=[function], type_ignores=[]), '<fixed-input>', 'exec'), scope)
        admit = scope['fixed_input']
        self.assertTrue(admit('source-git.tar', n.CORPUS_GIT_MAX, 0o400, True))
        for size, mode, corpus in [(n.CORPUS_GIT_MAX, 0o400, False), (n.CORPUS_GIT_MAX+1, 0o400, True), (1, 0o500, True)]:
            self.assertFalse(admit('source-git.tar', size, mode, corpus))
        self.assertFalse(admit('other-git.tar', 1, 0o400, True))
        for name in ('config.json', 'inventory.json', 'profile.json', 'seed.surql', 'native-acceptance.test', 'surreal', 'deployment.json', 'bundle/source/lib/lib.go'):
            self.assertTrue(admit(name, 1, 0o400, False))
        calls = [x for x in ast.walk(tree) if isinstance(x, ast.Call) and isinstance(x.func, ast.Name) and x.func.id == 'fixed_input']
        self.assertEqual(len(calls), 1)

    def test_config_positive_and_refusals(self):
        c = fixture_config()
        raw = n.canonical(c)
        self.assertEqual(n.config(raw, n.digest(raw)), c)
        for key, bad in [('id', '../escape'), ('helper_sha256', ''), ('profile_epoch', True), ('source_commit', 'main')]:
            v = copy.deepcopy(c); v[key] = bad
            with self.subTest(key=key), self.assertRaises(ValueError): n.config(n.canonical(v))
        for mutate in [lambda v: v['source'].update(repository='github.com/target/repo'),
                       lambda v: v['policy'].update(wall_seconds=900),
                       lambda v: v.update(command='sh')]:
            v = copy.deepcopy(c); mutate(v)
            with self.assertRaises(ValueError): n.config(n.canonical(v))
        for changed in [raw + b'\n', raw.replace(b'"id":"neutral-1"', b'"id":"neutral-1","id":"neutral-1"'), raw.replace(b'"id":"neutral-1"', b'"id":"neutral-2"')]:
            with self.assertRaises(ValueError): n.config(changed, n.digest(raw))

    def test_closed_transport(self):
        h = n.digest(b'config')
        with mock.patch.object(n, 'transport') as transport, mock.patch.object(n, 'check_deployment_host', return_value=h):
            for case in n.CASES:
                transport.reset_mock()
                n.run('neutral-1', case, h, 'deployment.json')
                transport.assert_called_once_with(['run', 'neutral-1', case, h, h], timeout=600)
            for ident, case in [('../escape', 'success'), ('neutral-1', 'target'), ('neutral-1', 'success;sh')]:
                with self.assertRaises(ValueError): n.run(ident, case, h, "deployment.json")
        with mock.patch.dict(os.environ, {}, clear=True), self.assertRaisesRegex(ValueError, 'transport unconfigured'):
            n.transport(['collect', 'neutral-1', 'success', h], timeout=30)
        with mock.patch.dict(os.environ, {'PHEBS_TYPED_NATIVE_TRANSPORT': 'ssh', 'PHEBS_TYPED_NATIVE_SSH_TARGET': 'phebs@typed-native'}), mock.patch.object(subprocess, 'run') as run:
            n.transport(['collect', 'neutral-1', 'success', h], timeout=30)
            argv = run.call_args.args[0]
            self.assertEqual(argv[:10], ['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'RequestTTY=no', '-o', 'ConnectTimeout=10', 'phebs@typed-native'])
            self.assertEqual(argv[10], '--')
            self.assertIn(n.sh_quote(n.REMOTE), argv[11])
            self.assertIn(n.sh_quote('sudo'), argv[11])
            self.assertNotIn('colima', argv[11])
            self.assertNotIn('shell', run.call_args.kwargs)
        with mock.patch.dict(os.environ, {'PHEBS_TYPED_NATIVE_TRANSPORT': 'ssh', 'PHEBS_TYPED_NATIVE_SSH_TARGET': '-oProxyCommand=evil'}), self.assertRaises(ValueError):
            n.transport(['collect', 'neutral-1', 'success', h], timeout=30)
        with mock.patch.dict(os.environ, {'PHEBS_TYPED_NATIVE_TRANSPORT': 'direct'}), mock.patch.object(n.os, 'uname', return_value=type('Host', (), {'machine': 'ppc64le'})()), self.assertRaisesRegex(ValueError, 'admitted linux host'):
            n.transport(['collect', 'neutral-1', 'success', h], timeout=30)

    def test_receipt_exclusive_and_bound(self):
        h = n.digest(b'config')
        raw = n.canonical(dict(config=h, case='success', passed=True))
        with tempfile.TemporaryDirectory() as d, mock.patch.object(n, 'transport', return_value=subprocess.CompletedProcess([], 0, raw)):
            path = Path(d) / 'receipt'
            n.collect('neutral-1', 'success', h, path)
            self.assertEqual(path.read_bytes(), raw)
            with self.assertRaises(FileExistsError): n.collect('neutral-1', 'success', h, path)
        with tempfile.TemporaryDirectory() as d, mock.patch.object(n, 'transport', return_value=subprocess.CompletedProcess([], 0, b'x' * (n.MAX_RECEIPT + 1))):
            with self.assertRaises(ValueError): n.collect('neutral-1', 'success', h, Path(d) / 'receipt')

    def test_real_local_stage_inventory(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d).resolve() / 'input'; root.mkdir()
            (root / 'bundle/source/lib').mkdir(parents=True)
            c = fixture_config()
            for name, key in [('deployment.json', 'deployment_sha256'), ('profile.json', 'profile_sha256'), ('seed.surql', 'seed_sha256'), ('native-acceptance.test', 'test_sha256'), ('surreal', 'engine_sha256')]:
                raw = name.encode(); (root / name).write_bytes(raw); c[key] = n.digest(raw)
            data = b'package lib\n'
            (root / 'bundle/source/lib/lib.go').write_bytes(data)
            selection = b'final-selection'
            (root / 'bundle/typed-bazel-selection.json').write_bytes(selection)
            c['selection_sha256'] = n.digest(selection)
            inv = dict(schema='phebs-typed-prehydration-v1', files=[dict(path='source/lib/lib.go', bytes=len(data), digest=n.digest(data), executable=False),
                       dict(path='typed-bazel-selection.json', bytes=len(selection), digest=n.digest(selection), executable=False)])
            raw = n.canonical(inv); (root / 'inventory.json').write_bytes(raw); c['inventory_sha256'] = n.digest(raw)
            (root / 'config.json').write_bytes(n.canonical(c))
            cfg, rows = n.inputs(root)
            self.assertEqual(cfg, c); self.assertEqual(len(rows), 9)
            wrong = copy.deepcopy(c); wrong['selection_sha256'] = n.digest(b'preparation-control')
            (root / 'config.json').write_bytes(n.canonical(wrong))
            with mock.patch.object(n, 'transport') as transport, self.assertRaisesRegex(ValueError, 'selection identity'):
                n.inputs(root)
            transport.assert_not_called()
            (root / 'config.json').write_bytes(n.canonical(c))
            (root / 'extra').write_bytes(b'no')
            with self.assertRaises(ValueError): n.inputs(root)
            (root / 'extra').unlink()
            (root / 'bundle/source/lib/lib.go').write_bytes(b'changed')
            with self.assertRaises(ValueError): n.inputs(root)
            (root / 'bundle/source/lib/lib.go').unlink()
            (root / 'bundle/source/lib/lib.go').symlink_to(root / 'profile.json')
            with self.assertRaises(ValueError): n.inputs(root)

    def test_special_files_and_cap(self):
        with tempfile.TemporaryDirectory() as d:
            base = Path(d).resolve(); path = base / 'file'; path.write_bytes(b'abc')
            self.assertEqual(n.read(path, 3), b'abc')
            with self.assertRaises(ValueError): n.read(path, 2)
            link = base / 'link'; link.symlink_to(path)
            with self.assertRaises(ValueError): n.read(link, 3)
            import os
            hard = base / 'hard'; os.link(path, hard)
            with self.assertRaises(ValueError): n.read(path, 3)
            fifo = base / 'fifo'; os.mkfifo(fifo)
            with self.assertRaises(ValueError): n.read(fifo, 3)

    def test_ustar_headers_reject_extensions_before_payload(self):
        import tarfile
        entry = tarfile.TarInfo('config.json'); entry.size = 3; entry.mode = 0o400
        good = entry.tobuf(format=tarfile.USTAR_FORMAT) + b'abc' + bytes(509) + bytes(1024)
        rows = []
        for member, chunks in n.read_ustar(io.BytesIO(good)):
            rows.append((member.name, b''.join(chunks)))
        self.assertEqual(rows, [('config.json', b'abc')])
        for kind in (tarfile.XHDTYPE, tarfile.XGLTYPE, tarfile.GNUTYPE_LONGNAME, tarfile.SYMTYPE):
            entry.type = kind; entry.size = 200 << 20
            header = entry.tobuf(format=tarfile.USTAR_FORMAT)
            class HeaderOnly(io.BytesIO):
                def read(self, count=-1):
                    if self.tell() >= 512: raise AssertionError('extension payload was read')
                    return super().read(count)
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                list(n.read_ustar(HeaderOnly(header)))
        for bad in (good[:100], good[:-513], good + b'nonzero'):
            with self.assertRaises(ValueError):
                for _, chunks in n.read_ustar(io.BytesIO(bad)): list(chunks)

    def test_remote_dispatch_is_detached_and_bounded(self):
        import ast
        import subprocess
        from pathlib import Path
        tree = ast.parse(n.REMOTE)
        calls = [x for x in ast.walk(tree) if isinstance(x, ast.Call)
                 and isinstance(x.func, ast.Attribute) and isinstance(x.func.value, ast.Name)
                 and x.func.value.id == 'subprocess' and x.func.attr == 'run']
        self.assertEqual(len(calls), 1)
        call = calls[0]
        scope = {'subprocess': subprocess, 'root': Path('/var/lib/phebs-typed-acceptance/neutral'), 'case': 'cancel', 'str': str}
        keywords = {x.arg: eval(compile(ast.Expression(x.value), '<keyword>', 'eval'), scope) for x in call.keywords}
        self.assertIs(keywords['start_new_session'], True)
        self.assertEqual(keywords['timeout'], 600)
        for stream in ('stdin', 'stdout', 'stderr'):
            self.assertEqual(keywords[stream], subprocess.DEVNULL)
        argv = eval(compile(ast.Expression(call.args[0]), '<argv>', 'eval'), scope)
        self.assertEqual(argv[1:4], ['-test.run=^TestTypedNativeAcceptance$', '-test.count=1', '-test.timeout=600s'])
        self.assertEqual(argv[-1], '-typed-native-case=cancel')
        self.assertLess(n.REMOTE.index("write(root/('dispatch-'+case+'.json')"), n.REMOTE.index('result=subprocess.run'))

    def test_remote_program_compiles_and_has_no_privilege_escape_recipe(self):
        compile(n.REMOTE, '<fixed remote program>', 'exec')
        for text in ['docker run', 'colima start', 'rm -rf', 'shell=True', 'tar.extractall', '--privileged']:
            self.assertNotIn(text, n.REMOTE)
        self.assertIn("root/('dispatch-'+case+'.json')", n.REMOTE)
        self.assertIn('os.O_EXCL', n.REMOTE)
        self.assertIn("for prior in cases[:cases.index(case)]", n.REMOTE)

    def test_remote_case_prerequisites_preserve_original_sequence(self):
        import ast
        tree = ast.parse(n.REMOTE)
        assignments = [x for x in ast.walk(tree) if isinstance(x, ast.Assign)
                       and any(isinstance(t, ast.Name) and t.id == 'cases' for t in x.targets)]
        self.assertEqual(len(assignments), 1)
        loop = next(x for x in ast.walk(tree) if isinstance(x, ast.For)
                    and isinstance(x.target, ast.Name) and x.target.id == 'prior')
        for case, expected in [('success', ()), ('cancel', ('success',)),
                               ('wall', ('success', 'cancel')),
                               ('hard-death', ('success', 'cancel', 'wall')),
                               ('canary', ()), ('dry-run', ('canary',))]:
            scope = {'C': n.CASES, 'case': case}
            scope['cases'] = eval(compile(ast.Expression(assignments[0].value), '<cases>', 'eval'), scope)
            self.assertEqual(eval(compile(ast.Expression(loop.iter), '<prior>', 'eval'), scope), expected)

    def test_host_observation_replaces_colima_attestation(self):
        self.assertNotIn('colima', Path(n.__file__).read_text())
        compile(n.OBSERVER, '<observer>', 'exec')
        self.assertIn('def docker_body(', n.OBSERVER)
        payload = b'{"ID":"daemon"}'
        self.assertEqual(n.info_object(payload + b'\n'), payload)
        self.assertEqual(n.json_pairs(n.info_object(payload + b'\n'))[0][0], 'ID')
        framed = f'{len(payload):x}\r\n'.encode() + payload + b'\r\n0\r\n\r\n'
        self.assertEqual(n.docker_body(b'HTTP/1.1 200 OK\r\nContent-Length: ' + str(len(payload)).encode() + b'\r\n\r\n', payload + b'extra'), payload)
        self.assertEqual(n.docker_body(b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n', b'4\r\n{"ID\r\n' + f'{len(payload) - 4:x}'.encode() + b'\r\n' + payload[4:] + b'\r\n0\r\n\r\n'), payload)
        self.assertEqual(n.docker_body(b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n', framed), payload)
        for head, body in (
            (b'HTTP/1.1 200 OK\r\n\r\n', payload),
            (b'HTTP/1.1 200 OK\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n', framed),
            (b'HTTP/1.1 200 OK\r\nContent-Length: 9\r\n\r\n', b'short'),
            (b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n', b'100001\r\n' + b'x' * ((1 << 20) + 1) + b'\r\n0\r\n\r\n'),
            (b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n', b'zz\r\n'),
            (b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n', b'2\r\nab'),
        ):
            with self.assertRaises(SystemExit) as refused:
                n.docker_body(head, body)
            self.assertEqual(refused.exception.code, 'docker length')
        for text in ('colima', 'shell=True', 'docker run', '--privileged'):
            self.assertNotIn(text, n.OBSERVER)
        raw = b'{"b":1,"a":{"z":2,"y":1}}'
        self.assertEqual(n.canonical_object(raw), b'{"a":{"z":2,"y":1},"b":1}')
        h = n.digest(b'os')
        runtime = n.digest(b'{"runc":{"path":"runc"}}')
        obs = dict(zip(n.OBSERVATION_KEYS, [
            'phebs-typed-native-host-observation-v1', '6.8.0', h, 'arm64', 2,
            n.POLICY['memory_bytes'] // 1024, 'daemon', '29.5.2', 'cgroupfs', runtime]))
        sealed = n.deployment_from_observation(obs)
        self.assertEqual(tuple(sealed), n.DEPLOYMENT_KEYS)
        self.assertEqual(sealed['vm_config_sha256'], n.digest(n.canonical(obs)))
        self.assertEqual(sealed['profile'], 'phebs-t451a')
        small = dict(obs)
        small['memory_total_kb'] = n.POLICY['memory_bytes'] // 1024 - 1
        with self.assertRaisesRegex(ValueError, 'memory'):
            n.deployment_from_observation(small)
        wide = dict(obs)
        wide['architecture'] = 'amd64'
        wide['cpus'] = 4
        self.assertEqual(n.deployment_from_observation(wide)['architecture'], 'amd64')
        self.assertEqual(n.deployment_from_observation(wide)['cpus'], 4)
        for bad in ({'architecture': '386', 'cpus': 4}, {'architecture': 'amd64', 'cpus': 1}):
            refused = dict(obs)
            refused.update(bad)
            with self.assertRaisesRegex(ValueError, 'geometry'):
                n.deployment_from_observation(refused)
        if os.uname().machine == 'x86_64':
            self.assertEqual(n.host_goarch(), 'amd64')
        elif os.uname().machine == 'aarch64':
            self.assertEqual(n.host_goarch(), 'arm64')
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / 'deployment.json'
            with mock.patch.object(n, 'observe_host', return_value=obs):
                n.write_deployment(path)
                self.assertEqual(n.check_deployment_host(path), n.digest(path.read_bytes()))
            changed = dict(obs)
            changed['kernel_release'] = 'other'
            with mock.patch.object(n, 'observe_host', return_value=changed), self.assertRaisesRegex(ValueError, 'observation changed'):
                n.check_deployment_host(path)

    def test_direct_transport_executes_on_an_admitted_host(self):
        for machine in ('aarch64', 'x86_64'):
            host = type('Host', (), {'machine': machine})()
            with mock.patch.object(n.os, 'uname', return_value=host), mock.patch.object(n.os, 'geteuid', return_value=0), mock.patch.dict(os.environ, {'PHEBS_TYPED_NATIVE_TRANSPORT': 'direct'}), mock.patch.object(subprocess, 'run') as run:
                n.transport(['collect', 'neutral-1', 'success', 'sha256:' + 'a' * 64], timeout=30)
                self.assertEqual(run.call_args.args[0][:2], ['python3', '-c'])
            with mock.patch.object(n.os, 'uname', return_value=host), mock.patch.object(n.os, 'geteuid', return_value=1000), mock.patch.dict(os.environ, {'PHEBS_TYPED_NATIVE_TRANSPORT': 'direct'}), mock.patch.object(subprocess, 'run') as run:
                n.transport(['collect', 'neutral-1', 'success', 'sha256:' + 'a' * 64], timeout=30)
                self.assertEqual(run.call_args.args[0][:4], ['sudo', '-n', 'python3', '-c'])


if __name__ == '__main__':
    unittest.main()
