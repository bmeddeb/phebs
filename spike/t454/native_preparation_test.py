import ast
import copy
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


# native_preparation imports the reviewed offline helpers from native_acceptance,
# so load that module first under its real name.
load('native_acceptance')
n = load('native_preparation')


def fixture_config():
    h = n.digest(b'fixed')
    return dict(zip(n.CONFIG_KEYS, [
        n.SCHEMA, 'neutral-prep-1', 'a' * 40,
        dict(repository=n.REPO, incarnation='fixture', generation=h, commit='b' * 40),
        n.REPO, n.REMOTE, n.ROOT,
        h, h, h, h, h, h, h, h]))


class PreparationTests(unittest.TestCase):
    def test_frozen_corpus_config_preserves_neutral(self):
        neutral = fixture_config()
        self.assertNotIn('cohort', neutral)
        for cohort in n.CORPUS_ROOTS:
            c = copy.deepcopy(neutral)
            c.update(schema=n.CORPUS_SCHEMA, module=n.CORPUS_REPO,
                     remote='https://' + n.CORPUS_REPO, root='', cohort=cohort)
            c['source'].update(repository=n.CORPUS_REPO, commit=n.CORPUS_COMMIT)
            raw = n.canonical(c)
            self.assertEqual(n.config(raw, n.digest(raw)), c)
            for mutate in [lambda v: v['source'].update(commit='c' * 40),
                           lambda v: v['source'].update(repository=n.REPO),
                           lambda v: v.update(cohort='all'),
                           lambda v: v.update(root='//...'),
                           lambda v: v.update(module=n.REPO),
                           lambda v: v.update(remote=n.REMOTE),
                           lambda v: v.update(schema=n.SCHEMA),
                           lambda v: v.update(roots=list(n.CORPUS_ROOTS[cohort]))]:
                v = copy.deepcopy(c); mutate(v)
                with self.assertRaises(ValueError):
                    n.config(n.canonical(v))

    def test_frozen_corpus_archive_refuses_unpinned_bytes(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d).resolve() / 'corpus.tar.gz'
            path.write_bytes(b'unreviewed archive')
            with self.assertRaisesRegex(ValueError, 'archive identity'):
                n.corpus_files(path)

    def test_config_positive_and_refusals(self):
        c = fixture_config()
        raw = n.canonical(c)
        self.assertEqual(n.config(raw, n.digest(raw)), c)
        for key, bad in [('id', '../escape'), ('helper_sha256', ''), ('source_commit', 'main')]:
            v = copy.deepcopy(c); v[key] = bad
            with self.subTest(key=key), self.assertRaises(ValueError):
                n.config(n.canonical(v))
        for mutate in [lambda v: v['source'].update(repository='github.com/target/repo'),
                       lambda v: v.update(root='//other:other'),
                       lambda v: v.update(module='example.invalid/other'),
                       lambda v: v.update(remote='https://example.invalid/other'),
                       lambda v: v.update(command='sh'),
                       lambda v: v.update(selection_sha256=n.digest(b'run-produced')),
                       lambda v: v.update(profile_epoch=1)]:
            v = copy.deepcopy(c); mutate(v)
            with self.assertRaises(ValueError):
                n.config(n.canonical(v))
        for changed in [raw + b'\n',
                        raw.replace(b'"id":"neutral-prep-1"', b'"id":"neutral-prep-1","id":"neutral-prep-1"'),
                        raw.replace(b'"id":"neutral-prep-1"', b'"id":"neutral-prep-2"')]:
            with self.assertRaises(ValueError):
                n.config(changed, n.digest(raw))

    def test_closed_transport_one_invocation(self):
        h = n.digest(b'config')
        with mock.patch.object(n, 'transport') as transport, mock.patch.object(n, 'check_deployment_host', return_value=h):
            n.run('neutral-prep-1', h, 'deployment.json')
            transport.assert_called_once_with(['run', 'neutral-prep-1', h, h], timeout=600)
            for ident in ('../escape', 'neutral-prep-1;sh', ''):
                with self.assertRaises(ValueError):
                    n.run(ident, h, 'deployment.json')
        with mock.patch.object(subprocess, 'run') as run:
            n.transport(['collect', 'neutral-prep-1', h], timeout=30)
            argv = run.call_args.args[0]
            self.assertEqual(argv[:7], ['colima', 'ssh', '--profile', 'phebs-t451a', '--', 'sudo', 'python3'])
            self.assertNotIn('start', argv)
            self.assertNotIn('shell', run.call_args.kwargs)

    def test_receipt_exclusive_and_bound(self):
        h = n.digest(b'config')
        raw = n.canonical(dict(schema='phebs-typed-native-preparation-receipt-v1', config=h, complete=True))
        with tempfile.TemporaryDirectory() as d, mock.patch.object(n, 'transport', return_value=subprocess.CompletedProcess([], 0, raw)):
            path = Path(d) / 'receipt'
            n.collect('neutral-prep-1', h, path)
            self.assertEqual(path.read_bytes(), raw)
            with self.assertRaises(FileExistsError):
                n.collect('neutral-prep-1', h, path)
        # A receipt that does not bind this exact config is refused.
        other = n.canonical(dict(schema='phebs-typed-native-preparation-receipt-v1', config=n.digest(b'other'), complete=True))
        with tempfile.TemporaryDirectory() as d, mock.patch.object(n, 'transport', return_value=subprocess.CompletedProcess([], 0, other)):
            with self.assertRaises(ValueError):
                n.collect('neutral-prep-1', h, Path(d) / 'receipt')
        with tempfile.TemporaryDirectory() as d, mock.patch.object(n, 'transport', return_value=subprocess.CompletedProcess([], 0, b'x' * (n.MAX_RECEIPT + 1))):
            with self.assertRaises(ValueError):
                n.collect('neutral-prep-1', h, Path(d) / 'receipt')

    def test_real_local_stage_inventory(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d).resolve() / 'input'; root.mkdir()
            (root / 'bundle/source/lib').mkdir(parents=True)
            (root / 'bundle/tools/bin').mkdir(parents=True)
            c = fixture_config()
            for name, key in [('deployment.json', 'deployment_sha256'), ('profile.json', 'profile_sha256'), ('native-preparation.test', 'test_sha256')]:
                raw = name.encode(); (root / name).write_bytes(raw); c[key] = n.digest(raw)
            test_bytes = (root / 'native-preparation.test').read_bytes()
            (root / 'native-preparation.test').chmod(0o500)
            helpers = ('phebs-typed-worker', 'tools/bin/phebs-t451b-native-driver')
            for path in helpers:
                helper = root / 'bundle' / path
                helper.write_bytes(test_bytes); helper.chmod(0o500)
            data = b'package lib\n'
            (root / 'bundle/source/lib/lib.go').write_bytes(data)
            inv = dict(schema='phebs-typed-prehydration-v1', files=[
                dict(path=helpers[0], bytes=len(test_bytes), digest=n.digest(test_bytes), executable=True),
                dict(path='source/lib/lib.go', bytes=len(data), digest=n.digest(data), executable=False),
                dict(path=helpers[1], bytes=len(test_bytes), digest=n.digest(test_bytes), executable=True),
            ])
            raw = n.canonical(inv); (root / 'inventory.json').write_bytes(raw); c['inventory_sha256'] = n.digest(raw)
            (root / 'config.json').write_bytes(n.canonical(c))
            cfg, rows = n.inputs(root)
            self.assertEqual(cfg, c)
            self.assertEqual(len(rows), 8)
            (root / 'extra').write_bytes(b'no')
            with self.assertRaises(ValueError):
                n.inputs(root)
            (root / 'extra').unlink()
            (root / 'bundle/source/lib/lib.go').write_bytes(b'changed')
            with self.assertRaises(ValueError):
                n.inputs(root)
            (root / 'bundle/source/lib/lib.go').unlink()
            (root / 'bundle/source/lib/lib.go').symlink_to(root / 'profile.json')
            with self.assertRaises(ValueError):
                n.inputs(root)
            (root / 'bundle/source/lib/lib.go').unlink()
            (root / 'bundle/source/lib/lib.go').write_bytes(data)
            stale = b'previous test binary'
            for path in helpers:
                helper = root / 'bundle' / path
                helper.unlink(); helper.write_bytes(stale); helper.chmod(0o500)
            for row in inv['files']:
                if row['path'] in helpers:
                    row.update(bytes=len(stale), digest=n.digest(stale))
            raw = n.canonical(inv); (root / 'inventory.json').write_bytes(raw); c['inventory_sha256'] = n.digest(raw)
            (root / 'config.json').write_bytes(n.canonical(c))
            with self.assertRaisesRegex(ValueError, 'preparation test helper mismatch'):
                n.inputs(root)

    def test_remote_dispatch_is_detached_and_bounded(self):
        tree = ast.parse(n.REMOTE_PROGRAM)
        calls = [x for x in ast.walk(tree) if isinstance(x, ast.Call)
                 and isinstance(x.func, ast.Attribute) and isinstance(x.func.value, ast.Name)
                 and x.func.value.id == 'subprocess' and x.func.attr == 'run']
        self.assertEqual(len(calls), 1)
        call = calls[0]
        scope = {'subprocess': subprocess, 'root': Path('/var/lib/phebs-typed-preparation/neutral'), 'str': str}
        keywords = {x.arg: x.value for x in call.keywords}
        ev = lambda node: eval(compile(ast.Expression(node), '<keyword>', 'eval'), scope)
        # detached session + wall-clock bound are unchanged
        self.assertIs(ev(keywords['start_new_session']), True)
        self.assertEqual(ev(keywords['timeout']), 600)
        # stdin stays closed; stdout/stderr are captured to the SAME bounded
        # diagnostic fd (never DEVNULL-discarded, never inherited from the transport)
        self.assertEqual(ev(keywords['stdin']), subprocess.DEVNULL)
        for stream in ('stdout', 'stderr'):
            self.assertIsInstance(keywords[stream], ast.Name)
            self.assertEqual(keywords[stream].id, 'diagfd')
        argv = ev(call.args[0])
        self.assertEqual(argv[1:4], ['-test.run=^TestNativePreparationHost$', '-test.count=1', '-test.timeout=600s'])
        self.assertEqual(argv[-1], '-typed-preparation-role=host')
        # one-dispatch fence: the exclusive marker is written before the test runs
        self.assertLess(n.REMOTE_PROGRAM.index("write(root/'dispatch.json'"), n.REMOTE_PROGRAM.index('subprocess.run('))
        # The staged helper bytes are checked before spending the one dispatch.
        self.assertIn("for name in ('phebs-typed-worker','tools/bin/phebs-t451b-native-driver'):", n.REMOTE_PROGRAM)
        self.assertLess(n.REMOTE_PROGRAM.index("if stat.S_IMODE(os.lstat(p).st_mode)!=0o555 or digest(read(p,256<<20))!=cfg['test_sha256']: reject()"),
                        n.REMOTE_PROGRAM.index("write(root/'dispatch.json'"))

    def test_staged_bundle_is_visible_to_unprivileged_worker(self):
        node = next(x for x in ast.parse(n.REMOTE_PROGRAM).body
                    if isinstance(x, ast.FunctionDef) and x.name == 'staged_mode')
        scope = {}
        exec(compile(ast.Module(body=[node], type_ignores=[]), '<staged_mode>', 'exec'), scope)
        mode = scope['staged_mode']
        self.assertEqual(mode('bundle/phebs-typed-worker', 0o500), 0o555)
        self.assertEqual(mode('bundle/source/lib/lib.go', 0o400), 0o444)
        self.assertEqual(mode('native-preparation.test', 0o500), 0o500)
        rp = n.REMOTE_PROGRAM
        self.assertLess(rp.index('os.fchmod(out.fileno(),mode);os.fsync(out.fileno())'),
                        rp.index("write(root/'staged.json'"))
        self.assertLess(rp.index('os.fchmod(fd,0o555);os.fsync(fd)'),
                        rp.index("write(root/'staged.json'"))

    def test_remote_diagnostic_output_is_bounded_and_retained(self):
        rp = n.REMOTE_PROGRAM
        # created exclusively, no symlink follow, owner-only, in the verified stage root
        self.assertIn("os.open(root/'test-output.log',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW|os.O_APPEND,0o600)", rp)
        # bounded to MAX_DIAG after the run, then durably persisted and closed
        self.assertIn('MAX_DIAG=1<<20', rp)
        self.assertIn('if os.fstat(diagfd).st_size>MAX_DIAG: os.ftruncate(diagfd,MAX_DIAG)', rp)
        self.assertIn('os.fsync(diagfd)', rp)
        self.assertIn('finally: os.close(diagfd)', rp)
        # retained on a wall-clock timeout and on a non-zero exit: the bounded
        # capture is finalized BEFORE the refusal, so a STOP keeps its diagnostic
        self.assertIn('except subprocess.TimeoutExpired: rc=1', rp)
        self.assertLess(rp.index('os.ftruncate(diagfd,MAX_DIAG)'), rp.index('if rc: reject()'))
        self.assertLess(rp.index('os.fsync(diagfd)'), rp.index('if rc: reject()'))

    def test_remote_program_compiles_and_has_no_privilege_escape_recipe(self):
        compile(n.REMOTE_PROGRAM, '<fixed preparation remote program>', 'exec')
        for text in ['docker run', 'colima start', 'rm -rf', 'shell=True', 'tar.extractall', '--privileged']:
            self.assertNotIn(text, n.REMOTE_PROGRAM)
        self.assertIn("write(root/'dispatch.json'", n.REMOTE_PROGRAM)
        self.assertIn('os.O_EXCL', n.REMOTE_PROGRAM)
        # Exactly one dispatch marker; no case-ordering loop and no retry.
        self.assertNotIn('for prior in', n.REMOTE_PROGRAM)
        self.assertNotIn('for attempt in', n.REMOTE_PROGRAM)


if __name__ == '__main__':
    unittest.main()
