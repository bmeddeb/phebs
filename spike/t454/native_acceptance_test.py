import copy
import importlib.util
import io
import json
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


class AcceptanceTests(unittest.TestCase):
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
        with mock.patch.object(subprocess, 'run') as run:
            n.transport(['collect', 'neutral-1', 'success', h], timeout=30)
            argv = run.call_args.args[0]
            self.assertEqual(argv[:7], ['colima', 'ssh', '--profile', 'phebs-t451a', '--', 'sudo', 'python3'])
            self.assertNotIn('start', argv)
            self.assertNotIn('shell', run.call_args.kwargs)

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


if __name__ == '__main__':
    unittest.main()
