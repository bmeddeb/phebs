"""T53.3: an independent WIRE-compatibility oracle (no Buf, no phebs code or output).

A standalone reader builds a wire model of each side's `.proto` set: messages,
fields (number, label, resolved kind, oneof, default, packing), enums, reserved
numbers and services. The model is reduced with T49.1b's independent lexer
only. The model is compared with rules taken from protobuf's documented
update rules (protobuf.dev, "Updating A Message Type") and the purposes the
T53.1 goldens recorded, never from Buf's implementation. Each finding is
`break`, or `undetermined` where neither source fixes the answer; an
undetermined unit is adjudicated by hand and never auto-scored. The verdict is
`breaking` if any finding breaks, else `undetermined` if any is undetermined,
else `compatible`.
"""
from __future__ import annotations

import sys
from dataclasses import dataclass, field
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 't491'))
from enumerate_declarations import tokens  # noqa: E402  (T49.1b's independent lexer)

SCALARS = {'double', 'float', 'int32', 'int64', 'uint32', 'uint64', 'sint32', 'sint64', 'fixed32', 'fixed64',
           'sfixed32', 'sfixed64', 'bool', 'string', 'bytes'}
NUMERIC = SCALARS - {'string', 'bytes'}
# protobuf.dev compatible groups; an enum is varint-compatible with the int group.
COMPATIBLE_GROUPS = [{'int32', 'uint32', 'int64', 'uint64', 'bool', 'enum'}, {'sint32', 'sint64'},
                     {'string', 'bytes'}, {'fixed32', 'sfixed32'}, {'fixed64', 'sfixed64'}]
# sint* shares the varint wire type but zigzag-encodes; protobuf.dev calls it incompatible with the other ints.
WIRE_TYPE = {**{t: 'varint' for t in ('int32', 'uint32', 'int64', 'uint64', 'bool', 'enum')},
             'sint32': 'zigzag', 'sint64': 'zigzag',
             'fixed32': 'i32', 'sfixed32': 'i32', 'float': 'i32', 'fixed64': 'i64', 'sfixed64': 'i64',
             'double': 'i64', 'string': 'len', 'bytes': 'len', 'message': 'len', 'group': 'group'}
WKT_ENUMS = {'google.protobuf.NullValue'}
MAX_FIELD = 536870911


class OracleError(ValueError):
    pass


@dataclass
class Field:
    name: str
    number: int
    label: str  # 'optional' (explicit), 'required', 'repeated', '' (proto3 implicit)
    type_name: str
    oneof: str | None = None
    options: dict = field(default_factory=dict)
    is_map: bool = False
    kind: str = ''  # scalar name, 'enum', 'message' or 'group' once resolved
    resolved: str = ''


@dataclass
class Message:
    fields: dict = field(default_factory=dict)  # number -> Field
    reserved: set = field(default_factory=set)  # numbers; ranges expanded only up to a bound
    reserved_ranges: list = field(default_factory=list)
    reserved_names: set = field(default_factory=set)
    oneofs: dict = field(default_factory=dict)  # name -> [numbers]
    has_extensions: bool = False


@dataclass
class Enum:
    values: dict = field(default_factory=dict)  # number -> name
    reserved_ranges: list = field(default_factory=list)
    reserved_names: set = field(default_factory=set)


@dataclass
class Model:
    packages: dict = field(default_factory=dict)  # request path -> package
    syntax: dict = field(default_factory=dict)  # request path -> 'proto2'|'proto3'|'editions'
    messages: dict = field(default_factory=dict)  # fqn -> Message
    message_syntax: dict = field(default_factory=dict)
    enums: dict = field(default_factory=dict)
    rpcs: dict = field(default_factory=dict)  # 'svc.fqn/Method' -> (in, out, client_stream, server_stream, idempotency)
    extensions: dict = field(default_factory=dict)  # (extendee, number) -> Field


class Reader:
    def __init__(self, content: bytes, model: Model, path: str):
        self.toks = [t for t in tokens(content)]
        self.i, self.model, self.path = 0, model, path
        self.package, self.syntax, self.scoped_fields = '', 'proto2', []

    def peek(self, k: int = 0) -> bytes | None:
        return self.toks[self.i + k][1] if self.i + k < len(self.toks) else None

    def take(self, expected: bytes | None = None) -> bytes:
        if self.i >= len(self.toks):
            raise OracleError('unexpected end of file')
        text = self.toks[self.i][1]
        if expected is not None and text != expected:
            raise OracleError(f'expected {expected!r}, found {text!r}')
        self.i += 1
        return text

    def word(self) -> str:
        return self.take().decode()

    def skip_balanced(self, open_: bytes, close: bytes) -> None:
        depth = 0
        while True:
            text = self.take()
            depth += (text == open_) - (text == close)
            if depth == 0:
                return

    def skip_statement(self) -> None:
        while True:
            text = self.peek()
            if text in (b'{', b'['):
                self.skip_balanced(text, {b'{': b'}', b'[': b']'}[text])
                continue
            self.take()
            if text == b';':
                return

    def string_value(self) -> str:
        parts = []
        while self.peek() is not None and self.peek()[:1] in (b'"', b"'"):
            parts.append(self.take()[1:-1].decode('unicode_escape', errors='replace'))
        return ''.join(parts)

    def option_value(self) -> str:
        if self.peek() == b'{':
            start = self.i
            self.skip_balanced(b'{', b'}')
            return ' '.join(t[1].decode(errors='replace') for t in self.toks[start:self.i])
        if self.peek() is not None and self.peek()[:1] in (b'"', b"'"):
            return '"' + self.string_value() + '"'
        value = self.word()
        if value in ('-', '+'):
            value += self.word()
        return value

    def option_name(self) -> str:
        parts = []
        while self.peek() not in (b'=', None):
            parts.append(self.word())
        return ''.join(parts)

    def field_options(self) -> dict:
        options = {}
        if self.peek() != b'[':
            return options
        self.take(b'[')
        while True:
            name = self.option_name()
            self.take(b'=')
            options[name] = self.option_value()
            if self.take() == b']':
                return options

    def parse(self) -> None:
        while self.peek() is not None:
            word = self.peek()
            if word == b'syntax':
                self.take(), self.take(b'=')
                self.syntax = self.string_value()
                self.take(b';')
            elif word == b'edition':
                self.syntax = 'editions'
                self.skip_statement()
            elif word == b'package':
                self.take()
                self.package = self.word()
                self.take(b';')
            elif word in (b'import', b'option'):
                self.skip_statement()
            elif word == b'message':
                self.take()
                self.message(self.scoped(self.package, self.word()))
            elif word == b'enum':
                self.take()
                self.enum(self.scoped(self.package, self.word()))
            elif word == b'service':
                self.take()
                self.service(self.scoped(self.package, self.word()))
            elif word == b'extend':
                self.take()
                self.extend(self.word(), self.package)
            elif word == b';':
                self.take()
            else:
                raise OracleError(f'unexpected top-level {word!r}')
        self.model.packages[self.path] = self.package
        self.model.syntax[self.path] = self.syntax

    @staticmethod
    def scoped(scope: str, name: str) -> str:
        return f'{scope}.{name}' if scope else name

    def reserved(self) -> tuple[list, set]:
        self.take(b'reserved')
        ranges, names = [], set()
        while True:
            if self.peek()[:1] in (b'"', b"'"):
                names.add(self.string_value())
            elif self.peek() not in (b',', b';'):
                low = int(self.word(), 0)
                high = low
                if self.peek() == b'to':
                    self.take()
                    end = self.word()
                    high = MAX_FIELD if end == 'max' else int(end, 0)
                ranges.append((low, high))
            text = self.take()
            if text == b';':
                return ranges, names

    def message(self, fqn: str) -> None:
        message = self.model.messages.setdefault(fqn, Message())
        self.model.message_syntax[fqn] = self.syntax
        self.take(b'{')
        self.body(fqn, message, None)

    def body(self, fqn: str, message: Message, oneof: str | None) -> None:
        while (word := self.peek()) != b'}':
            if word is None:
                raise OracleError('unterminated message')
            if word == b'message':
                self.take()
                self.message(self.scoped(fqn, self.word()))
            elif word == b'enum':
                self.take()
                self.enum(self.scoped(fqn, self.word()))
            elif word == b'extend':
                self.take()
                self.extend(self.word(), fqn)
            elif word == b'oneof':
                self.take()
                name = self.word()
                message.oneofs.setdefault(name, [])
                self.take(b'{')
                self.body(fqn, message, name)
            elif word == b'reserved':
                ranges, names = self.reserved()
                message.reserved_ranges += ranges
                message.reserved_names |= names
            elif word == b'extensions':
                message.has_extensions = True
                self.skip_statement()
            elif word in (b'option', b';'):
                self.skip_statement() if word == b'option' else self.take()
            else:
                f = self.field(fqn)
                f.oneof = oneof
                if oneof is not None:
                    message.oneofs[oneof].append(f.number)
                if f.number in message.fields:
                    raise OracleError(f'duplicate field number {f.number} in {fqn}')
                message.fields[f.number] = f
        self.take(b'}')

    def field(self, scope: str) -> Field:
        label = ''
        if self.peek() in (b'optional', b'required', b'repeated'):
            label = self.word()
        elif self.syntax == 'proto2':
            label = 'optional'  # oneof members carry no label
        if self.peek() == b'map':
            self.take(), self.take(b'<')
            key = self.word()
            self.take(b',')
            value = self.word()
            self.take(b'>')
            name = self.word()
            self.take(b'=')
            number = int(self.word(), 0)
            options = self.field_options()
            self.take(b';')
            f = Field(name, number, 'repeated', f'map<{key},{value}>', options=options, is_map=True)
            f.map_value = value  # type: ignore[attr-defined]
            self.scoped_fields.append((scope, f))
            return f
        if self.peek() == b'group':
            self.take()
            name = self.word()
            self.take(b'=')
            number = int(self.word(), 0)
            options = self.field_options()
            group = self.scoped(scope, name)
            self.message(group)
            f = Field(name.lower(), number, label, group, options=options)
            f.kind, f.resolved = 'group', group
            return f
        type_name = self.word()
        name = self.word()
        self.take(b'=')
        number = int(self.word(), 0)
        options = self.field_options()
        self.take(b';')
        f = Field(name, number, label, type_name, options=options)
        if type_name in SCALARS:
            f.kind, f.resolved = type_name, type_name
        else:
            self.scoped_fields.append((scope, f))
        return f

    def enum(self, fqn: str) -> None:
        enum = self.model.enums.setdefault(fqn, Enum())
        self.take(b'{')
        while (word := self.peek()) != b'}':
            if word == b'reserved':
                ranges, names = self.reserved()
                enum.reserved_ranges += ranges
                enum.reserved_names |= names
            elif word in (b'option', b';'):
                self.skip_statement() if word == b'option' else self.take()
            else:
                name = self.word()
                self.take(b'=')
                number = self.word()
                if number == '-':
                    number += self.word()
                self.field_options()
                self.take(b';')
                enum.values.setdefault(int(number, 0), name)
        self.take(b'}')

    def service(self, fqn: str) -> None:
        self.take(b'{')
        while (word := self.peek()) != b'}':
            if word != b'rpc':
                self.skip_statement() if word == b'option' else self.take()
                continue
            self.take()
            method = self.word()
            self.take(b'(')
            client = self.peek() == b'stream' and bool(self.take())
            request = self.word()
            self.take(b')'), self.take(b'returns'), self.take(b'(')
            server = self.peek() == b'stream' and bool(self.take())
            response = self.word()
            self.take(b')')
            idempotency = 'IDEMPOTENCY_UNKNOWN'
            if self.peek() == b'{':
                self.take()
                while self.peek() != b'}':
                    if self.peek() == b'option':
                        self.take()
                        name = self.option_name()
                        self.take(b'=')
                        value = self.option_value()
                        self.take(b';')
                        if name == 'idempotency_level':
                            idempotency = value
                    else:
                        self.take()
                self.take(b'}')
            else:
                self.take(b';')
            self.scoped_fields.append((fqn, ('rpc', f'{fqn}/{method}', request, response, client, server, idempotency)))
        self.take(b'}')

    def extend(self, extendee: str, scope: str) -> None:
        self.take(b'{')
        while self.peek() != b'}':
            f = self.field(scope)
            self.model.extensions[(scope, extendee, f.number)] = f
        self.take(b'}')


def resolve(model: Model, scope: str, name: str) -> str | None:
    if name.startswith('.'):
        return name[1:] if name[1:] in model.messages or name[1:] in model.enums or name[1:] in WKT_ENUMS else None
    parts = scope.split('.') if scope else []
    for k in range(len(parts), -1, -1):
        candidate = '.'.join(parts[:k] + [name])
        if candidate in model.messages or candidate in model.enums:
            return candidate
    if name.startswith('google.protobuf.'):
        return name
    return None


def kind_of(model: Model, fqn: str) -> str:
    if fqn in model.enums or fqn in WKT_ENUMS:
        return 'enum'
    return 'message'


def load(files: list[tuple[str, bytes]]) -> Model:
    model, pending = Model(), []
    for path, content in files:
        reader = Reader(content, model, path)
        reader.parse()
        pending += reader.scoped_fields
    for scope, item in pending:
        if isinstance(item, tuple):
            _, key, request, response, client, server, idempotency = item
            package_scope = scope.rsplit('.', 1)[0] if '.' in scope else ''
            model.rpcs[key] = (resolve(model, package_scope, request) or request,
                               resolve(model, package_scope, response) or response, client, server, idempotency)
            continue
        type_name = getattr(item, 'map_value', None) or item.type_name
        if type_name in SCALARS:
            item.kind = item.kind or 'map'
            continue
        target = resolve(model, scope, type_name)
        if target is None:
            raise OracleError(f'unresolved type {type_name} in {scope}')
        item.resolved = target
        item.kind = 'map' if item.is_map else kind_of(model, target)
        item.map_kind = kind_of(model, target)  # type: ignore[attr-defined]
    return model


def in_ranges(number: int, ranges: list) -> bool:
    return any(low <= number <= high for low, high in ranges)


def compatible_types(before: Field, after: Field) -> str:
    """'same', 'compatible', 'break' or 'undetermined' for one field number."""
    if before.is_map or after.is_map:
        if before.is_map and after.is_map and before.type_name.split(',')[0] == after.type_name.split(',')[0]:
            return compatible_types(map_value(before), map_value(after))
        return 'undetermined'
    a, b = before.kind, after.kind
    if a == b and before.resolved == after.resolved:
        return 'same'
    if 'group' in (a, b):
        return 'undetermined'
    if a == b == 'message' or a == b == 'enum':
        return 'undetermined'  # a different named type: wire type alike, contents not fixed by the rules
    if any(a in g and b in g for g in COMPATIBLE_GROUPS):
        return 'compatible'
    if {a, b} == {'bytes', 'message'}:
        return 'compatible'  # protobuf.dev: embedded messages are compatible with bytes holding an encoding
    if WIRE_TYPE[a] != WIRE_TYPE[b]:
        return 'break'
    return 'undetermined'  # same wire type, value interpretation not fixed (e.g. float<->fixed32)


def map_value(f: Field) -> Field:
    value = f.map_value  # type: ignore[attr-defined]
    out = Field(f.name, f.number, '', value)
    out.kind = value if value in SCALARS else f.map_kind  # type: ignore[attr-defined]
    out.resolved = f.resolved or value
    return out


def packed(f: Field, syntax: str) -> bool:
    explicit = f.options.get('packed')
    if explicit is not None:
        return explicit == 'true'
    return syntax == 'proto3'


def explicit_presence(f: Field) -> bool:
    return f.label == 'optional' or f.kind in ('message', 'group')


def compare(before: Model, after: Model) -> list[dict]:
    findings = []

    def add(outcome: str, rule: str, where: str, why: str) -> None:
        findings.append({'outcome': outcome, 'rule': rule, 'where': where, 'why': why})

    for path in sorted(set(before.packages) & set(after.packages)):
        if before.packages[path] != after.packages[path]:
            add('break', 'package', path, 'the file package changed')
        if 'editions' in (before.syntax[path], after.syntax[path]) and before.syntax[path] != after.syntax[path]:
            add('undetermined', 'syntax', path, 'an editions syntax change is outside this oracle')
        elif before.syntax[path] != after.syntax[path]:
            add('undetermined', 'syntax', path, 'a syntax change alters implicit presence and packing defaults')
    for fqn in sorted(set(before.messages) & set(after.messages)):
        old, new = before.messages[fqn], after.messages[fqn]
        syntax_old, syntax_new = before.message_syntax[fqn], after.message_syntax[fqn]
        if 'editions' in (syntax_old, syntax_new) and old != new:
            add('undetermined', 'editions', fqn, 'editions feature resolution is outside this oracle')
            continue
        if not set(old.reserved_names) <= set(new.reserved_names) or any(
                not any(lo <= l and h <= hi for lo, hi in new.reserved_ranges) for l, h in old.reserved_ranges):
            add('break', 'reserved_message_deleted', fqn, 'a reserved field number or name was dropped')
        required_old = {n for n, f in old.fields.items() if f.label == 'required'}
        required_new = {n for n, f in new.fields.items() if f.label == 'required'}
        if required_old != required_new:
            add('break', 'required_fields', fqn, 'the set of required fields changed')
        for number, f in sorted(old.fields.items()):
            g = new.fields.get(number)
            where = f'{fqn}#{number}'
            if g is None:
                if not in_ranges(number, new.reserved_ranges):
                    add('break', 'field_deleted_unreserved', where, 'a field number was deleted without reserving it')
                continue
            verdict = compatible_types(f, g)
            if verdict in ('break', 'undetermined'):
                add(verdict, 'field_type', where, f'{f.kind}:{f.resolved} -> {g.kind}:{g.resolved}')
            if (f.label == 'repeated') != (g.label == 'repeated') and not (f.is_map or g.is_map):
                repeated, kinds = (g if g.label == 'repeated' else f), {f.kind, g.kind}
                syntax = syntax_new if g.label == 'repeated' else syntax_old
                if kinds <= {'string', 'bytes', 'message'}:
                    pass  # protobuf.dev: singular<->repeated is compatible for string, bytes and message
                elif kinds & ({'enum'} | NUMERIC) and packed(repeated, syntax):
                    add('break', 'cardinality', where, 'a packed repeated numeric meets a singular reader')
                elif kinds & ({'enum'} | NUMERIC):
                    pass  # unpacked repeated numerics share the singular encoding
                else:
                    add('undetermined', 'cardinality', where, f'{f.kind} cardinality change')
            if f.oneof != g.oneof:
                if f.oneof is None:
                    fresh = g.oneof not in old.oneofs and len(new.oneofs[g.oneof]) == 1
                    ok = fresh and explicit_presence(f)
                else:
                    ok = g.oneof is None and len(old.oneofs[f.oneof]) == 1 and explicit_presence(g)
                if not ok:
                    add('break', 'oneof', where, 'the field moved into or out of a oneof unsafely')
            if f.options.get('default') != g.options.get('default') and 'proto2' in (syntax_old, syntax_new):
                add('break', 'default', where, 'the proto2 default changed')
            if (f.label != g.label and f.oneof == g.oneof and 'required' not in (f.label, g.label)
                    and 'repeated' not in (f.label, g.label)):
                add('undetermined', 'presence', where, f'presence {f.label or "implicit"} -> {g.label or "implicit"}')
    for fqn in sorted(set(before.enums) & set(after.enums)):
        old, new = before.enums[fqn], after.enums[fqn]
        for number in sorted(set(old.values) - set(new.values)):
            if not in_ranges(number, new.reserved_ranges):
                add('break', 'enum_value_deleted_unreserved', f'{fqn}={number}', 'an enum number was deleted without reserving it')
        if not set(old.reserved_names) <= set(new.reserved_names) or any(
                not any(lo <= l and h <= hi for lo, hi in new.reserved_ranges) for l, h in old.reserved_ranges):
            add('break', 'reserved_enum_deleted', fqn, 'a reserved enum number or name was dropped')
    for key in sorted(set(before.rpcs) & set(after.rpcs)):
        (req, res, cs, ss, idem), (req2, res2, cs2, ss2, idem2) = before.rpcs[key], after.rpcs[key]
        for name, x, y in (('rpc_request', req, req2), ('rpc_response', res, res2), ('rpc_client_streaming', cs, cs2),
                           ('rpc_server_streaming', ss, ss2), ('rpc_idempotency', idem, idem2)):
            if x != y:
                add('break', name, key, f'{x} -> {y}')
    # An added extension is additive; custom options on descriptor *Options never reach a message's wire.
    changed_extensions = {k for k in before.extensions
                          if not k[1].lstrip('.').startswith('google.protobuf.') and (
                              k not in after.extensions
                              or compatible_types(before.extensions[k], after.extensions[k]) != 'same')}
    for key in sorted(changed_extensions):
        add('undetermined', 'extension', f'{key[1]}#{key[2]}', 'an extension changed; extension rules are outside this oracle')
    return findings


def verdict(findings: list[dict]) -> str:
    outcomes = {f['outcome'] for f in findings}
    return 'breaking' if 'break' in outcomes else 'undetermined' if outcomes else 'compatible'


def judge(before: list[tuple[str, bytes]], after: list[tuple[str, bytes]]) -> dict:
    try:
        findings = compare(load(before), load(after))
    except (OracleError, ValueError, IndexError, TypeError) as error:
        return {'verdict': 'unreadable', 'findings': [], 'error': str(error)}
    return {'verdict': verdict(findings), 'findings': findings}
