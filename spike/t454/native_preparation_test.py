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
            c = fixture_config()
            for name, key in [('deployment.json', 'deployment_sha256'), ('profile.json', 'profile_sha256'), ('native-preparation.test', 'test_sha256')]:
                raw = name.encode(); (root / name).write_bytes(raw); c[key] = n.digest(raw)
            data = b'package lib\n'
            (root / 'bundle/source/lib/lib.go').write_bytes(data)
            inv = dict(schema='phebs-typed-prehydration-v1', files=[dict(path='source/lib/lib.go', bytes=len(data), digest=n.digest(data), executable=False)])
            raw = n.canonical(inv); (root / 'inventory.json').write_bytes(raw); c['inventory_sha256'] = n.digest(raw)
            (root / 'config.json').write_bytes(n.canonical(c))
            cfg, rows = n.inputs(root)
            self.assertEqual(cfg, c)
            self.assertEqual(len(rows), 6)
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

    def test_remote_dispatch_is_detached_and_bounded(self):
        tree = ast.parse(n.REMOTE_PROGRAM)
        calls = [x for x in ast.walk(tree) if isinstance(x, ast.Call)
                 and isinstance(x.func, ast.Attribute) and isinstance(x.func.value, ast.Name)
                 and x.func.value.id == 'subprocess' and x.func.attr == 'run']
        self.assertEqual(len(calls), 1)
        call = calls[0]
        scope = {'subprocess': subprocess, 'root': Path('/var/lib/phebs-typed-preparation/neutral'), 'str': str}
        keywords = {x.arg: eval(compile(ast.Expression(x.value), '<keyword>', 'eval'), scope) for x in call.keywords}
        self.assertIs(keywords['start_new_session'], True)
        self.assertEqual(keywords['timeout'], 600)
        for stream in ('stdin', 'stdout', 'stderr'):
            self.assertEqual(keywords[stream], subprocess.DEVNULL)
        argv = eval(compile(ast.Expression(call.args[0]), '<argv>', 'eval'), scope)
        self.assertEqual(argv[1:4], ['-test.run=^TestNativePreparationHost$', '-test.count=1', '-test.timeout=600s'])
        self.assertEqual(argv[-1], '-typed-preparation-role=host')
        self.assertLess(n.REMOTE_PROGRAM.index("write(root/'dispatch.json'"), n.REMOTE_PROGRAM.index('result=subprocess.run'))

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
