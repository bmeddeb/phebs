"""T49.1b: independent protobuf declaration universe (no phebs code or output).

A standalone lexer and recursive-descent reader over the frozen corpus's
`.proto` blobs, written independently of phebs's protocompile-based reader. It
enumerates every declaration of the five kinds in the frozen surface of
PROTOBUF_DECLARATION_IDENTITY.md §5: service, rpc operation, message (groups
included), field (oneof, map and group fields included) and extension. Each
gets the §5 object identity, its whole-statement byte span and the declared
role stratum. Enums, enum values, reserved and extension ranges, and options are
enumerated only as out-of-scope counts, never as universe members. A file the
reader cannot parse is recorded, never silently skipped.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
import re
import subprocess
from pathlib import Path

SCHEMA = 't491-declaration-universe-v1'
_TOKEN = re.compile(rb'''
    (?P<ws>\s+)
  | (?P<line>//[^\n]*)
  | (?P<block>/\*.*?\*/)
  | (?P<str>"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')
  | (?P<num>[-+]?(?:0[xX][0-9a-fA-F]+|[0-9]+(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?))
  | (?P<ident>\.?[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)
  | (?P<punct>[{}()\[\];=<>,:.\-+/])
''', re.S | re.X)
LABELS = {'optional', 'required', 'repeated'}
SCALARS = {'double', 'float', 'int32', 'int64', 'uint32', 'uint64', 'sint32', 'sint64', 'fixed32', 'fixed64',
           'sfixed32', 'sfixed64', 'bool', 'string', 'bytes'}


class ProtoError(ValueError):
    """The reader could not account for a construct; the file is recorded, not skipped."""


def tokens(content: bytes) -> list[tuple[str, bytes, int, int]]:
    out, position = [], 0
    while position < len(content):
        match = _TOKEN.match(content, position)
        if not match:
            raise ProtoError(f'unlexable byte at {position}')
        kind = match.lastgroup
        if kind not in ('ws', 'line', 'block'):
            out.append((kind, match.group(), match.start(), match.end()))
        position = match.end()
    return out


def role(path: str) -> str:
    """Declared stratum rule: a `vendor` segment wins, then `tests`/`testdata`, else production."""
    segments = path.split('/')
    if 'vendor' in segments:
        return 'vendor'
    if 'tests' in segments or 'testdata' in segments:
        return 'test'
    return 'production'


class Reader:
    def __init__(self, content: bytes):
        self.toks = tokens(content)
        self.i = 0
        self.package = ''
        self.facts: list[dict] = []
        self.out_of_scope: dict[str, int] = {}
        self.imports = 0
        self.extendees: list[str] = []
        self.types: dict[str, list[str]] = {}  # fully-qualified name -> kinds declared in this file
        self.references: list[dict] = []

    def peek(self, offset: int = 0) -> bytes | None:
        j = self.i + offset
        return self.toks[j][1] if j < len(self.toks) else None

    def take(self, expected: bytes | None = None) -> tuple[str, bytes, int, int]:
        if self.i >= len(self.toks):
            raise ProtoError('unexpected end of file')
        tok = self.toks[self.i]
        if expected is not None and tok[1] != expected:
            raise ProtoError(f'expected {expected!r} at byte {tok[2]}, found {tok[1]!r}')
        self.i += 1
        return tok

    def ident(self) -> str:
        kind, text, _, _ = self.take()
        if kind != 'ident':
            raise ProtoError(f'expected identifier, found {text!r}')
        return text.decode()

    def skip_balanced(self, open_: bytes, close: bytes) -> int:
        depth, end = 0, None
        while True:
            _, text, _, stop = self.take()
            if text == open_:
                depth += 1
            elif text == close:
                depth -= 1
                if depth == 0:
                    return stop
            end = stop

    def skip_statement(self) -> int:
        """Skip to the terminating ';' at this nesting level, stepping over [] and {} groups."""
        while True:
            text = self.peek()
            if text in (b'[', b'{'):
                close = {b'[': b']', b'{': b'}'}[text]
                stop = self.skip_balanced(text, close)
                if text == b'{' and self.peek() != b';':
                    return stop
            else:
                _, text, _, stop = self.take()
                if text == b';':
                    return stop

    def emit(self, predicate: str, obj: str, start: int, end: int) -> None:
        self.facts.append({'predicate': predicate, 'object': obj, 'start_byte': start, 'end_byte': end})

    def scoped(self, scope: str, name: str) -> str:
        return f'{scope}.{name}' if scope else name

    def parse(self) -> None:
        while self.peek() is not None:
            word = self.peek()
            start = self.toks[self.i][2]
            if word in (b'syntax', b'edition', b'import', b'option'):
                self.imports += word == b'import'
                self.skip_statement()
            elif word == b'package':
                self.take()
                self.package = self.ident()
                self.take(b';')
            elif word == b'message':
                self.message(self.package, start)
            elif word == b'enum':
                self.enum(self.package)
            elif word == b'service':
                self.service(start)
            elif word == b'extend':
                self.extend(self.package)
            elif word == b';':
                self.take()
            else:
                raise ProtoError(f'unexpected top-level token {word!r}')

    def enum(self, scope: str) -> None:
        self.take(b'enum')
        self.types.setdefault(self.scoped(scope, self.ident()), []).append('enum')
        self.out_of_scope['enum'] = self.out_of_scope.get('enum', 0) + 1
        self.skip_balanced(b'{', b'}')

    def message(self, scope: str, start: int) -> None:
        self.take(b'message')
        name = self.ident()
        fqn = self.scoped(scope, name)
        self.types.setdefault(fqn, []).append('message')
        self.take(b'{')
        self.body(fqn)
        end = self.take(b'}')[3]
        self.emit('DECLARES_MESSAGE', fqn, start, end)

    def body(self, fqn: str) -> None:
        while self.peek() != b'}':
            word = self.peek()
            start = self.toks[self.i][2]
            if word == b'message':
                self.message(fqn, start)
            elif word == b'enum':
                self.enum(fqn)
            elif word == b'extend':
                self.extend(fqn)
            elif word == b'oneof':
                self.take()
                self.ident()
                self.take(b'{')
                while self.peek() != b'}':
                    if self.peek() == b'option':
                        self.skip_statement()
                    elif self.peek() == b';':
                        self.take()
                    else:
                        self.field(fqn, self.toks[self.i][2])
                self.take(b'}')
            elif word in (b'reserved', b'extensions'):
                kind = word.decode()
                self.out_of_scope[kind] = self.out_of_scope.get(kind, 0) + 1
                self.skip_statement()
            elif word == b'option':
                self.skip_statement()
            elif word == b';':
                self.take()
            else:
                self.field(fqn, start)

    def field(self, fqn: str, start: int, extension_scope: str | None = None) -> None:
        if self.peek() in {l.encode() for l in LABELS}:
            self.take()
        refs = []
        if self.peek() == b'map':
            self.take()
            self.take(b'<')
            refs.append(('map.key', self.ident()))
            self.take(b',')
            refs.append(('map.value', self.ident()))
            self.take(b'>')
            name = self.ident()
        elif self.peek() == b'group':
            self.take()
            name = self.ident()
            self.take(b'=')
            tag = int(self.take()[1], 0)
            if self.peek() == b'[':
                self.skip_balanced(b'[', b']')
            group_fqn = self.scoped(fqn if extension_scope is None else extension_scope, name)
            if extension_scope is not None:
                raise ProtoError('extension groups need descriptor linking')
            self.types.setdefault(group_fqn, []).append('message')
            self.take(b'{')
            self.body(group_fqn)
            end = self.take(b'}')[3]
            self.emit('DECLARES_FIELD', f'{fqn}#{tag}', start, end)
            self.emit('DECLARES_MESSAGE', group_fqn, start, end)
            self.reference(f'{fqn}#{tag}', 'type', '.' + group_fqn, fqn, message_only=False)
            return
        else:
            refs.append(('type', self.ident()))
            name = self.ident()
        self.take(b'=')
        tag = int(self.take()[1], 0)
        end = self.skip_statement()
        if extension_scope is None:
            obj = f'{fqn}#{tag}'
            self.emit('DECLARES_FIELD', obj, start, end)
        else:
            obj = f'{self.scoped(extension_scope, name)}#{tag}'
            self.emit('DECLARES_EXTENSION', obj, start, end)
            for extendee in self.extendees[-1:]:
                self.reference(obj, 'extendee', extendee, fqn, message_only=True)
        for slot, raw in refs:
            self.reference(obj, slot, raw, fqn, message_only=False)

    def extend(self, scope: str) -> None:
        self.take(b'extend')
        self.extendees.append(self.ident())
        self.take(b'{')
        while self.peek() != b'}':
            if self.peek() == b';':
                self.take()
                continue
            if self.peek() == b'map':
                raise ProtoError('map fields are invalid in an extension body')
            self.field(scope, self.toks[self.i][2], extension_scope=scope)
        self.take(b'}')
        self.extendees.pop()

    def service(self, start: int) -> None:
        self.take(b'service')
        name = self.ident()
        fqn = self.scoped(self.package, name)
        self.take(b'{')
        while self.peek() != b'}':
            word = self.peek()
            rpc_start = self.toks[self.i][2]
            if word == b'rpc':
                self.take()
                rpc = self.ident()
                types = []
                for _ in range(2):
                    self.take(b'(')
                    if self.peek() == b'stream' and self.peek(1) != b')':
                        self.take()
                    types.append(self.ident())
                    self.take(b')')
                    if self.peek() == b'returns':
                        self.take()
                end = self.skip_balanced(b'{', b'}') if self.peek() == b'{' else self.take(b';')[3]
                if self.peek() == b';':
                    self.take()
                self.emit('DECLARES_OPERATION', f'{fqn}/{rpc}', rpc_start, end)
                for slot, raw in zip(('request', 'response'), types):
                    self.reference(f'{fqn}/{rpc}', slot, raw, self.package, message_only=True)
            elif word == b'option':
                self.skip_statement()
            elif word == b';':
                self.take()
            else:
                raise ProtoError(f'unexpected service token {word!r}')
        end = self.take(b'}')[3]
        self.emit('DECLARES_SERVICE', fqn, start, end)


    def reference(self, obj: str, slot: str, raw: str, scope: str, message_only: bool) -> None:
        self.references.append({'object': obj, 'slot': slot, 'raw': raw, 'scope': scope, 'message_only': message_only})

    def resolve(self, ref: dict) -> dict:
        """Oracle rule: protobuf innermost-scope-outward lookup over this file's own declarations only.

        A leading '.' is fully qualified. Otherwise each enclosing scope is tried from the innermost
        outward (then the root), and the first scope level with any match decides. One match is
        same_file; several declarations of that exact name are ambiguous; none falls back to
        IMPORT_LINKING_UNAVAILABLE when the file imports anything, else DECLARATION_NOT_FOUND. A
        request, response or extendee that resolves to a non-message is INVALID_DECLARATION_KIND.
        """
        raw, out = ref['raw'], {k: ref[k] for k in ('object', 'slot', 'raw')}
        if raw in SCALARS:
            return {**out, 'resolution': 'intrinsic', 'reason': ''}
        if raw.startswith('.'):
            candidates = [raw[1:]]
        else:
            parts = ref['scope'].split('.') if ref['scope'] else []
            candidates = ['.'.join(parts[:i] + [raw]) for i in range(len(parts), -1, -1)]
        for name in candidates:
            kinds = self.types.get(name)
            if not kinds:
                continue
            if len(kinds) > 1:
                return {**out, 'resolution': 'unresolved', 'reason': 'AMBIGUOUS_SAME_FILE_DECLARATION'}
            if ref['message_only'] and kinds[0] != 'message':
                return {**out, 'resolution': 'unresolved', 'reason': 'INVALID_DECLARATION_KIND'}
            return {**out, 'resolution': 'same_file', 'reason': '', 'target': name}
        reason = 'IMPORT_LINKING_UNAVAILABLE' if self.imports else 'DECLARATION_NOT_FOUND'
        return {**out, 'resolution': 'unresolved', 'reason': reason}


def enumerate_file(content: bytes) -> tuple[list[dict], dict]:
    facts, skipped, _ = enumerate_file_with_references(content)
    return facts, skipped


def enumerate_file_with_references(content: bytes) -> tuple[list[dict], dict, list[dict]]:
    reader = Reader(content)
    reader.parse()
    return reader.facts, reader.out_of_scope, [reader.resolve(r) for r in reader.references]


def git(repo: Path, *args: str) -> bytes:
    return subprocess.run(['git', '-C', str(repo), *args], check=True, capture_output=True,
                          env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'}).stdout


def build(corpus: Path, repositories: dict[str, dict]) -> dict:
    """repositories: census name -> {'dir': clone directory name, 'commit': exact commit}."""
    rows, files, failures, out_of_scope, references = [], [], [], {}, []
    for name, spec in sorted(repositories.items()):
        repo = corpus / spec['dir']
        listing = git(repo, 'ls-tree', '-r', '-z', '--full-tree', spec['commit']).split(b'\0')
        for entry in listing:
            if not entry:
                continue
            meta, path = entry.split(b'\t', 1)
            mode, kind, oid = meta.split()
            path = path.decode()
            if not path.endswith('.proto') or kind != b'blob':
                continue
            if mode == b'120000':
                failures.append({'repository': name, 'path': path, 'reason': 'symlink_not_a_regular_proto'})
                continue
            content = git(repo, 'cat-file', 'blob', oid.decode())
            digest = 'sha256:' + hashlib.sha256(content).hexdigest()
            try:
                facts, skipped, refs = enumerate_file_with_references(content)
            except (ProtoError, ValueError) as error:
                failures.append({'repository': name, 'path': path, 'reason': str(error), 'blob_sha256': digest})
                continue
            files.append({'repository': name, 'path': path, 'blob_sha256': digest, 'declarations': len(facts)})
            for kind_name, count in skipped.items():
                out_of_scope[kind_name] = out_of_scope.get(kind_name, 0) + count
            references += [{'repository': name, 'commit': spec['commit'], 'path': path, **r} for r in refs]
            for fact in facts:
                rows.append({'site_id': f"{name}@{spec['commit']}:{path}:{fact['start_byte']}-{fact['end_byte']}",
                             'repository': name, 'commit': spec['commit'], 'path': path, **fact,
                             'stratum': f"{name}:{role(path)}:{fact['predicate']}"})
    rows.sort(key=lambda r: (r['repository'], r['path'], r['start_byte'], r['predicate'], r['object']))
    keys = [(r['repository'], r['path'], r['predicate'], r['object']) for r in rows]
    references.sort(key=lambda r: (r['repository'], r['path'], r['object'], r['slot']))
    return {'rows': rows, 'references': references, 'files': files, 'failures': failures, 'out_of_scope': out_of_scope,
            'duplicate_object_keys': len(keys) - len(set(keys))}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--corpus', type=Path, required=True)
    parser.add_argument('--lock', type=Path, required=True, help='spike/t472/corpus.lock.json')
    parser.add_argument('--out-dir', type=Path, required=True)
    args = parser.parse_args()
    lock = json.loads(args.lock.read_text())
    repositories = {}
    for repo in lock['repos']:
        derived = repo['derived']
        if derived.get('corpus_admitted'):
            repositories[repo['name'] if 'name' in repo else repo['repository']] = {
                'dir': (repo.get('name') or repo['repository']).rsplit('/', 1)[1], 'commit': derived['derived_commit']}
    universe = build(args.corpus, repositories)
    args.out_dir.mkdir(parents=True, exist_ok=True)
    body = b''.join(json.dumps(r, sort_keys=True, separators=(',', ':')).encode() + b'\n' for r in universe['rows'])
    (args.out_dir / 'declarations.jsonl.gz').write_bytes(gzip.compress(body, mtime=0))
    ref_body = b''.join(json.dumps(r, sort_keys=True, separators=(',', ':')).encode() + b'\n' for r in universe['references'])
    (args.out_dir / 'references.jsonl.gz').write_bytes(gzip.compress(ref_body, mtime=0))
    states: dict[str, int] = {}
    for ref in universe['references']:
        key = ref['resolution'] + (':' + ref['reason'] if ref['reason'] else '')
        states[key] = states.get(key, 0) + 1
    counts: dict[str, int] = {}
    for row in universe['rows']:
        counts[row['stratum']] = counts.get(row['stratum'], 0) + 1
    summary = {'schema': SCHEMA, 'repositories': repositories, 'files': universe['files'],
               'failures': universe['failures'], 'out_of_scope': universe['out_of_scope'],
               'duplicate_object_keys': universe['duplicate_object_keys'], 'total': len(universe['rows']),
               'by_stratum': dict(sorted(counts.items())),
               'members_sha256': 'sha256:' + hashlib.sha256((args.out_dir / 'declarations.jsonl.gz').read_bytes()).hexdigest(),
               'references_total': len(universe['references']), 'references_by_state': dict(sorted(states.items())),
               'references_sha256': 'sha256:' + hashlib.sha256((args.out_dir / 'references.jsonl.gz').read_bytes()).hexdigest(),
               'enumerator_sha256': 'sha256:' + hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (args.out_dir / 'universe.json').write_text(json.dumps(summary, indent=1, sort_keys=True) + '\n')
    print(json.dumps({k: summary[k] for k in ('total', 'failures', 'out_of_scope', 'duplicate_object_keys')}, indent=1))


if __name__ == '__main__':
    main()
