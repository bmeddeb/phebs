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


class AcceptanceTests(unittest.TestCase):
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
            n.run('neutral-1', 'success', h, 'deployment.json')
            transport.assert_called_once_with(['run', 'neutral-1', 'success', h, h], timeout=600)
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
            inv = dict(schema='phebs-typed-prehydration-v1', files=[dict(path='source/lib/lib.go', bytes=len(data), digest=n.digest(data), executable=False)])
            raw = n.canonical(inv); (root / 'inventory.json').write_bytes(raw); c['inventory_sha256'] = n.digest(raw)
            (root / 'config.json').write_bytes(n.canonical(c))
            cfg, rows = n.inputs(root)
            self.assertEqual(cfg, c); self.assertEqual(len(rows), 8)
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
        self.assertIn("for prior in C[:C.index(case)]", n.REMOTE)


if __name__ == '__main__':
    unittest.main()
