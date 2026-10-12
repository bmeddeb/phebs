"""Fences for the change-population closure over a synthetic repository."""
import subprocess
import tempfile
import unittest
from pathlib import Path

import enumerate_changes as e


def commit(repo: Path, files: dict[str, str], message: str) -> str:
    for path, content in files.items():
        (repo / path).parent.mkdir(parents=True, exist_ok=True)
        (repo / path).write_text(content)
    subprocess.run(['git', '-C', str(repo), 'add', '-A'], check=True)
    subprocess.run(['git', '-C', str(repo), '-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', message], check=True)
    return subprocess.run(['git', '-C', str(repo), 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True).stdout.strip()


class ClosureTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.repo = Path(self.dir.name) / 'r'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True)
        api = ('syntax = "proto3";\npackage x.api;\n'
               '/* import "missing/in_comment.proto"; */\n'
               'import "github.com/x/r/api/types/t.proto";\nimport "gogoproto/gogo.proto";\n'
               'import "google/protobuf/empty.proto";\nmessage M { x.types.T t = 1; }\n')
        self.first = commit(self.repo, {
            'api/svc/v1/svc.proto': api,
            'api/types/t.proto': 'syntax = "proto3";\npackage x.types;\nmessage T {}\n',
            'vendor/github.com/gogo/protobuf/gogoproto/gogo.proto': 'syntax = "proto2";\npackage gogoproto;\n',
            'lone/l.proto': 'syntax = "proto3";\nmessage L {}\n',
        }, 'one')
        self.second = commit(self.repo, {
            'api/svc/v1/svc.proto': api.replace('t = 1;', 't = 1; string s = 2;'),
            'lone/l.proto': 'syntax = "proto3";\nimport "nowhere/x.proto";\nmessage L {}\n',
        }, 'two')

    def tearDown(self):
        self.dir.cleanup()

    def test_each_file_is_supplied_under_its_import_name(self):
        state, names = e.closure(e.Tree(self.repo, self.second), 'api/svc/v1/svc.proto')
        self.assertEqual(state, 'closed')
        self.assertEqual(names, {
            'api/svc/v1/svc.proto': 'github.com/x/r/api/svc/v1/svc.proto',
            'api/types/t.proto': 'github.com/x/r/api/types/t.proto',
            'vendor/github.com/gogo/protobuf/gogoproto/gogo.proto': 'gogoproto/gogo.proto',
        })

    def test_population_keeps_unclosable_units_with_their_state(self):
        population = e.build(Path(self.dir.name), {'x/r': {'dir': 'r', 'commit': self.second}})
        states = {u['path']: u['state'] for u in population['units']}
        self.assertEqual(states, {'api/svc/v1/svc.proto': 'closed', 'lone/l.proto': 'unresolved_import'})
        closed = next(u for u in population['units'] if u['state'] == 'closed')
        self.assertEqual((closed['commit'], closed['parent']), (self.second, self.first))
        self.assertNotEqual(closed['before'], closed['after'])
        self.assertNotIn('request_path', next(u for u in population['units'] if u['state'] != 'closed'))


if __name__ == '__main__':
    unittest.main()
