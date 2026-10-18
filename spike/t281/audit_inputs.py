"""Source-only custody/literal checks; no Go, Redis or ACL recognizer.

Verifies already chosen records/ranges. Does not discover statements, command
semantics, keys, declarations or credentials. Prints only aggregate counters.
"""
from pathlib import Path, PurePosixPath
import base64
import hashlib
import json
import tarfile

ROOT = Path(__file__).resolve().parent


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def load(path):
    return json.loads((ROOT / path).read_text())


def check(condition):
    if not condition:
        raise ValueError('source-only custody/literal audit failed; no source context retained')


def main():
    receipt = load('source-receipt.json')
    sources = {}
    size = 0
    for population in receipt['records']:
        expected = {f['path']: f for f in population['files']}
        check(len(expected) == len(population['files']))
        with tarfile.open(ROOT / population['archive']) as archive:
            members = archive.getmembers()
            check(len(members) == len(expected) <= 1024)
            check({m.name for m in members} == set(expected))
            for member in members:
                path = PurePosixPath(member.name)
                check(member.isfile() and not path.is_absolute())
                check(all(p not in ('', '.', '..') for p in path.parts))
                record = expected[member.name]
                check(member.size == record['bytes'] <= 262144)
                raw = archive.extractfile(member).read(262145)
                check(len(raw) == record['bytes'] and sha(raw) == record['sha256'])
                check(hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\x00' + raw).hexdigest() == record['git_blob'])
                sources[population['name'], member.name] = raw
                size += len(raw)
    check(len(sources) == 793 and size == 3600148)

    ranges = 0

    def witness(value, raw):
        nonlocal ranges
        if isinstance(value, list):
            for item in value:
                witness(item, raw)
        elif isinstance(value, dict):
            if {'start', 'end', 'sha256'} <= value.keys():
                start, end = value['start'], value['end']
                check(isinstance(start, int) and isinstance(end, int))
                check(0 <= start < end <= len(raw))
                check(sha(raw[start:end]) == value['sha256'])
                if 'token' in value:
                    check(raw[start:end] == value['token'].encode())
                ranges += 1
            for child in value.values():
                if isinstance(child, (dict, list)):
                    witness(child, raw)

    acl = load('labels/public-acl.json')
    for file in acl['files']:
        raw = sources['redis', file['source_path']]
        check(len(raw) == file['source_bytes'] and sha(raw) == file['source_sha256'])
        witness(file, raw)
    check(sum(len(f['principals']) for f in acl['files']) == 7)
    check(sum(len(f['pairs']) for f in acl['files']) == 7)

    usage = load('labels/public-usage.json')
    for file in usage['files']:
        raw = sources[file['population'], file['source_path']]
        check(len(raw) == file['source_bytes'] and sha(raw) == file['source_sha256'])
        module = sources[file['population'], file['module_path']]
        check(sha(module) == file['module_sha256'])
        check(('github.com/redis/go-redis/v9 ' + file['actual_version']).encode() in module)
        parents = PurePosixPath(file['source_path']).parent
        possible = ['go.mod'] + [str(p / 'go.mod') for p in (parents, *parents.parents) if str(p) != '.']
        present = [p for p in possible if (file['population'], p) in sources]
        nearest = max(present, key=lambda p: len(PurePosixPath(p).parts))
        check(nearest == file['module_path'])
        witness(file, raw)
    sites = [s for f in usage['files'] for s in f['sites']]
    keys = [k for s in sites for k in s['keys']]
    check(len(sites) == 99 and len(keys) == 30)

    native = load('labels/native-declarations.json')
    for site in native['index_sites']:
        raw = sources['eino', site['source_path']]
        check(sha(raw) == site['source_sha256'])
        witness(site, raw)
    census = load('source-literal-census.json')
    for match in census['matches']:
        raw = sources[match['population'], match['source_path']]
        check(sha(raw) == match['source_sha256'])
        witness(match, raw)

    sampling = load('sampling.json')
    selected = []
    for population in sampling['records']:
        for group in ('selected', 'unsampled'):
            for file in population[group]:
                raw = sources[population['population'], file['path']]
                check(sha(raw) == file['source_sha256'] and len(raw) == file['bytes'])
                if group == 'selected':
                    selected.append((population['population'], file['path']))
    check(len(selected) == 7 and sum(len(sources[k]) for k in selected) == 136115)
    check(set(selected) == {(f['population'], f['source_path']) for f in usage['files']})

    controls = load('controls.json')
    for case in controls['cases']:
        raw = (ROOT / case['source_path']).read_bytes()
        check(sha(raw) == case['source_sha256'] and len(raw) == case['source_bytes'])
        witness(case, raw)
        for key in case['key_labels']:
            check(isinstance(base64.b64decode(key['bytes_base64'], validate=True), bytes))
    check(len(controls['cases']) == 60 and len(controls['admission_controls']) == 2)
    check(not (ROOT / controls['admission_controls'][1]['source_path']).exists())

    adapter = load('adapter-contract.json')
    refs = {(r['version'], r['path']): r for r in load('adapter-source-receipt.json')['references']}
    for rule in adapter['method_rules']:
        reference = refs[rule['version'], rule['source_path']]
        check(rule['commit'] == reference['commit'] and rule['source_file_sha256'] == reference['sha256'])
        check(0 <= rule['source_function_start'] < rule['source_function_end'] <= reference['bytes'])
    for rule in adapter['supporting_helper_witnesses']:
        reference = refs[rule['version'], rule['source_path']]
        check(rule['commit'] == reference['commit'] and 0 <= rule['start'] < rule['end'] <= reference['bytes'])
    for rule in adapter['receiver_type_witnesses']:
        reference = refs[rule['version'], rule['source_path']]
        check(rule['commit'] == reference['commit'] and rule['source_file_sha256'] == reference['sha256'])
        check(0 <= rule['start'] < rule['end'] <= reference['bytes'])
    check(len(adapter['method_rules']) == 56 and len(adapter['supporting_helper_witnesses']) == 8)
    check(len(adapter['receiver_type_witnesses']) == 10 and len(refs) == 30)
    receiver_review = load('receiver-source-review.json')
    for site in receiver_review['sites']:
        witness(site, sources[site['population'], site['source_path']])
    for proof in receiver_review['source_witnesses']:
        witness(proof['span'], sources['asynq' if proof['repository'].endswith('/asynq') else 'eino', proof['source_path']])
    check(len(receiver_review['sites']) == 37)

    print(json.dumps(dict(format='t281-source-only-input-audit-v1', recognizer_runs=0,
        immutable_git_sha256_files=len(sources), immutable_source_bytes=size,
        chosen_literal_span_round_trips=ranges, selected_complete_files=len(selected),
        selected_source_bytes=136115, usage_sites=len(sites), known_key_expressions=len(keys),
        public_acl_principals=7, public_acl_pairs=7, native_sites=2,
        inert_source_controls=60, admission_controls=2, adapter_method_witnesses=56,
        adapter_helper_witnesses=8, receiver_type_witnesses=10, scope='Custody, chosen literal ranges, module/version and inventory checks only; no parser/recognizer, output safety or candidate quality measured.'), indent=2))


if __name__ == '__main__':
    main()
