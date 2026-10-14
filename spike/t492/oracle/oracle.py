"""T49.2 oracle (protocol section 3): compile every tracked .thrift file of one
checkout with the pinned Apache Thrift compiler and map its JSON to the
section 2 identities. One JSON line per file. Uses no phebs code or output."""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path

IMAGE = 'phebs-t492-thrift-oracle:0.19.0'
BASE = {'bool', 'byte', 'i8', 'i16', 'i32', 'i64', 'double', 'string', 'binary', 'uuid'}
MESSAGE = {'struct', 'union', 'exception'}
CARDINALITY = {'required': 'required', 'optional': 'optional', 'req_out': 'default'}


def scope_of(program: dict) -> str:
    namespaces = program.get('namespaces') or {}
    raw = namespaces.get('go') or namespaces.get('*')
    return raw.split('.')[-1] if raw else program['name']


def state(scope: str, type_id: str, node: dict | None, under_container: bool) -> str:
    """Reference state of one type, mirroring section 3 (and phebs D8 shape rules)."""
    if type_id in BASE:
        return 'intrinsic'
    node = node or {}
    if type_id in MESSAGE or type_id == 'enum':
        cls = node.get('class', '')
        if '.' in cls:
            return 'unresolved:include'
        return f"same_file:{'enum' if type_id == 'enum' else 'message'}:{scope}.{cls}"
    if type_id in ('list', 'set'):
        if under_container:
            return 'unresolved:nested'
        return state(scope, node.get('elemTypeId', ''), node.get('elemType'), True)
    if type_id == 'map':
        return 'unresolved:nested'  # a map below the field's top level abstains (D8)
    return f'unknown:{type_id}'


def field_shape(scope: str, field: dict) -> dict:
    type_id, node = field.get('typeId', ''), field.get('type')
    if type_id == 'map':
        node = node or {}
        refs = [state(scope, node.get('keyTypeId', ''), node.get('keyType'), False),
                state(scope, node.get('valueTypeId', ''), node.get('valueType'), False)]
    else:
        refs = [state(scope, type_id, node, False)]
    return {'name': field['name'], 'cardinality': CARDINALITY.get(field.get('required', ''), field.get('required')),
            'refs': refs}


def declarations(program: dict) -> tuple[list, dict]:
    scope = scope_of(program)
    decls, fields = [], {}

    def add_field(message: str, field: dict) -> None:
        obj = f"{message}#{field['key']}"
        decls.append(['DECLARES_FIELD', obj])
        fields[obj] = field_shape(scope, field)

    for struct in program.get('structs', []):
        message = f"{scope}.{struct['name']}"
        decls.append(['DECLARES_MESSAGE', message])
        for field in struct.get('fields', []):
            add_field(message, field)
    for service in program.get('services', []):
        decls.append(['DECLARES_SERVICE', f"{scope}.{service['name']}"])
        for fn in service.get('functions', []):
            decls.append(['DECLARES_OPERATION', f"{scope}.{service['name']}/{fn['name']}"])
            args = f"{scope}.{service['name']}.{fn['name']}_args"
            decls.append(['DECLARES_MESSAGE', args])
            for field in fn.get('arguments', []):
                add_field(args, field)
            if not fn.get('oneway'):
                result = f"{scope}.{service['name']}.{fn['name']}_result"
                decls.append(['DECLARES_MESSAGE', result])
                if fn.get('returnTypeId') != 'void':
                    obj = f'{result}#0'
                    decls.append(['DECLARES_FIELD', obj])
                    fields[obj] = field_shape(scope, {'name': 'success', 'typeId': fn['returnTypeId'],
                                                      'type': fn.get('returnType'), 'required': 'req_out'})
                for field in fn.get('exceptions', []):
                    add_field(result, field)
    return decls, fields


def run(repo: Path, name: str, image: str = IMAGE) -> list[dict]:
    listed = subprocess.run(['git', '-C', str(repo), 'ls-files', '-z', '--', '*.thrift'],
                            check=True, capture_output=True).stdout.decode().split('\0')
    files = sorted(f for f in listed if f)
    if not files:
        return []
    dirs = sorted({str(Path(f).parent) for f in files})[:64]
    with tempfile.TemporaryDirectory() as out:
        script = ['set -u']
        for index, path in enumerate(files):
            includes = ' '.join(f"-I '/r/{d}'" for d in [str(Path(path).parent), '.'] + dirs)
            script.append(f"mkdir -p /o/{index} && thrift {includes} --gen json -out /o/{index} '/r/{path}' "
                          f"2> /o/{index}/err; echo $? > /o/{index}/rc")
        Path(out, 'run.sh').write_text('\n'.join(script) + '\n')
        subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--user', f'{_uid()}',
                        '-v', f'{repo.resolve()}:/r:ro', '-v', f'{out}:/o', image, 'bash', '/o/run.sh'],
                       check=True, capture_output=True)
        rows = []
        for index, path in enumerate(files):
            base = Path(out, str(index))
            rc = int((base / 'rc').read_text().strip() or 1)
            produced = [p for p in base.glob('*.json')]
            row = {'repository': name, 'path': path}
            if rc != 0 or len(produced) != 1:
                err = (base / 'err').read_text(errors='replace').strip().splitlines()
                row.update(status='rejected', error=(err[0] if err else f'exit {rc}')[:300])
            else:
                decls, fields = declarations(json.loads(produced[0].read_text()))
                row.update(status='ok', declarations=decls, fields=fields)
            rows.append(row)
        return rows


def _uid() -> str:
    import os
    return f'{os.getuid()}:{os.getgid()}'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--name', required=True)
    args = parser.parse_args()
    for row in run(args.repo, args.name):
        print(json.dumps(row, sort_keys=True))
