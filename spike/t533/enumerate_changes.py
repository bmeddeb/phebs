"""T53.3: the real-history protobuf change population (no phebs code or output).

One unit is one modified, non-vendored `.proto` file in a first-parent commit
of a frozen T47.2 corpus repository, up to its admitted derived commit. Each
side of a unit is that file plus its repository-local transitive import
closure at that revision, each file
under the exact names it is imported by, which is what a caller supplies to
`check_wire_compatibility`. Vendored dependencies count as repository-local.
Well-known `google/protobuf/*` imports are built into Buf and need no file.

A unit whose closure cannot be formed (an import found nowhere or in more than
one place in the tree, one file imported under two names, or over the
checker's input limits) is kept with that state. It is never silently dropped,
but it carries no request: the input contract makes the caller supply a
compilable closure. Added and deleted files are census counts, not units.
Only blob ids are recorded; content is read back from git when the round runs.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 't491'))
from enumerate_declarations import tokens  # noqa: E402  (T49.1b's independent lexer)

SCHEMA = 't533-change-population-v1'
LINEAGE = 'contract_t533_population'
# The checker's own input ceilings (internal/compat/check.go, idlpreflight).
MAX_FILES, MAX_FILE_BYTES, MAX_INPUT_BYTES = 256, 1 << 20, 32 << 20
EXCLUDED_PREFIXES = ('vendor/', 'third_party/', 'Godeps/')


def vendored(path: str) -> bool:
    """Third-party copies are dependency inputs, never units of their own."""
    return path.startswith(EXCLUDED_PREFIXES) or '/vendor/' in path or '/_workspace/' in path


def git(repo: Path, *args: str) -> bytes:
    return subprocess.run(['git', '-C', str(repo), *args], check=True, capture_output=True).stdout


def imports(content: bytes) -> list[str]:
    toks = tokens(content)
    found = []
    for i, (kind, text, _, _) in enumerate(toks):
        if kind == 'ident' and text == b'import' and (i == 0 or toks[i - 1][1] in (b';', b'}')):
            j = i + 1
            if j < len(toks) and toks[j][1] in (b'public', b'weak'):
                j += 1
            if j < len(toks) and toks[j][0] == 'str':
                found.append(toks[j][1][1:-1].decode())
    return found


class Tree:
    """The .proto blobs of one revision, by path."""

    def __init__(self, repo: Path, rev: str):
        self.repo, self.blobs, self._content = repo, {}, {}
        for entry in git(repo, 'ls-tree', '-r', '-z', '--full-tree', rev).split(b'\0'):
            if entry:
                meta, path = entry.split(b'\t', 1)
                mode, kind, oid = meta.split()
                if kind == b'blob' and mode != b'120000' and path.endswith(b'.proto'):
                    self.blobs[path.decode()] = oid.decode()

    def content(self, path: str) -> bytes:
        if path not in self._content:
            self._content[path] = git(self.repo, 'cat-file', 'blob', self.blobs[path])
        return self._content[path]

    def resolve(self, name: str) -> list[str]:
        """Tree paths an import name can denote, best class only: the exact path, a
        path under an extra include directory (vendor/..., api/...), or a path the
        import reaches through a GOPATH-style module prefix (github.com/org/repo/...)."""
        exact = [p for p in self.blobs if p == name]
        if exact:
            return exact
        included = sorted(p for p in self.blobs if p.endswith('/' + name))
        if included:
            return included
        return sorted(p for p in self.blobs if '/' in p and name.endswith('/' + p))


def closure(tree: Tree, path: str) -> tuple[str, dict]:
    """Returns (state, {tree path: request path}) for one side. Every imported file
    is supplied under the exact name it is imported by, which is what makes the
    set compile; the changed file takes the include mapping of its own first
    same-repository import, or its tree path when it has none."""
    names, pending, own = {}, [path], None
    while pending:
        current = pending.pop()
        try:
            imported = imports(tree.content(current))
        except ValueError:
            return 'unreadable', {}
        for name in imported:
            if name.startswith('google/protobuf/'):
                continue
            matches = tree.resolve(name)
            if not matches:
                return 'unresolved_import', {}
            if len(matches) > 1:
                return 'ambiguous_import', {}
            match = matches[0]
            if names.get(match, name) != name:
                return 'conflicting_import_names', {}
            if current == path and own is None and not vendored(match):
                own = (match, name)
            if match not in names:
                names[match] = name
                pending.append(match)
    if path in names:
        return 'import_cycle', {}
    request = path
    if own is not None:
        match, name = own
        if match.endswith(name):
            prefix = match[: len(match) - len(name)]
            request = path[len(prefix):] if path.startswith(prefix) else None
        else:
            request = name[: len(name) - len(match)] + path
    if request is None or request in names.values():
        return 'unplaceable_changed_file', {}
    names[path] = request
    sizes = [len(tree.content(p)) for p in names]
    if len(names) > MAX_FILES or max(sizes) > MAX_FILE_BYTES or sum(sizes) > MAX_INPUT_BYTES:
        return 'over_input_limit', {}
    return 'closed', names


def side(tree: Tree, names: dict) -> list[dict]:
    return sorted(({'path': request, 'tree_path': p, 'blob': tree.blobs[p],
                    'sha256': 'sha256:' + hashlib.sha256(tree.content(p)).hexdigest()} for p, request in names.items()),
                  key=lambda f: f['path'])


def build(corpus: Path, repositories: dict[str, dict]) -> dict:
    units, census = [], {}
    for name, spec in sorted(repositories.items()):
        repo = corpus / spec['dir']
        commits = git(repo, 'rev-list', '--first-parent', spec['commit'], '--', '*.proto').decode().split()
        for commit in commits:
            parents = git(repo, 'rev-list', '--parents', '-n', '1', commit).decode().split()[1:]
            if not parents:
                continue
            parent = parents[0]
            changed = git(repo, 'diff-tree', '--no-renames', '-r', '-z', '--name-status', parent, commit, '--', '*.proto')
            fields = [f.decode() for f in changed.split(b'\0') if f]
            pairs = list(zip(fields[0::2], fields[1::2]))
            modified = [p for s, p in pairs if s == 'M' and not vendored(p)]
            for status, path in pairs:
                key = f"{name}:{'excluded_path' if vendored(path) else status}"
                census[key] = census.get(key, 0) + 1
            if not modified:
                continue
            before_tree, after_tree = Tree(repo, parent), Tree(repo, commit)
            for path in modified:
                state_after, after = closure(after_tree, path)
                state_before, before = closure(before_tree, path)
                state = state_after if state_after != 'closed' else state_before
                if state == 'closed' and after[path] != before[path]:
                    state = 'changed_file_moved'
                unit = {'unit_id': f'{name}@{commit}:{path}', 'repository': name, 'commit': commit,
                        'parent': parent, 'path': path, 'state': state, 'stratum': f'{name}:{state}'}
                if state == 'closed':
                    unit['request_path'] = after[path]
                    unit['before'], unit['after'] = side(before_tree, before), side(after_tree, after)
                    unit['changed_closure'] = [f['sha256'] for f in unit['before']] != [f['sha256'] for f in unit['after']]
                units.append(unit)
    units.sort(key=lambda u: u['unit_id'])
    return {'units': units, 'census': dict(sorted(census.items()))}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--corpus', type=Path, required=True)
    parser.add_argument('--lock', type=Path, required=True, help='spike/t472/corpus.lock.json')
    parser.add_argument('--out-dir', type=Path, required=True)
    args = parser.parse_args()
    lock = json.loads(args.lock.read_text())
    repositories = {r['name']: {'dir': r['name'].rsplit('/', 1)[1], 'commit': r['derived']['derived_commit']}
                    for r in lock['repos'] if r['derived'].get('corpus_admitted')}
    population = build(args.corpus, repositories)
    args.out_dir.mkdir(parents=True, exist_ok=True)
    body = ''.join(json.dumps(u, sort_keys=True, separators=(',', ':')) + '\n' for u in population['units']).encode()
    (args.out_dir / 'units.jsonl.gz').write_bytes(gzip.compress(body, mtime=0))
    by_stratum: dict[str, int] = {}
    for unit in population['units']:
        by_stratum[unit['stratum']] = by_stratum.get(unit['stratum'], 0) + 1
    summary = {'schema': SCHEMA, 'lineage': LINEAGE, 'repositories': repositories, 'census': population['census'],
               'total': len(population['units']), 'by_stratum': dict(sorted(by_stratum.items())),
               'units_sha256': 'sha256:' + hashlib.sha256(body).hexdigest(),
               'enumerator_sha256': 'sha256:' + hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (args.out_dir / 'population.json').write_text(json.dumps(summary, indent=1, sort_keys=True) + '\n')
    print(json.dumps({k: summary[k] for k in ('total', 'by_stratum')}, indent=1))


if __name__ == '__main__':
    main()
