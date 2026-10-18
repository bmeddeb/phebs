"""Independent finite source-oracle scorer. Never classifies Go/Redis source.

Candidate inputs contain no labels. This evaluator joins every emitted sample
site and safe ACL token to prospectively chosen immutable source witnesses.
"""
from pathlib import Path
from collections import Counter
import base64
import hashlib
import json
import re

ROOT = Path(__file__).resolve().parent


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def read_json(name):
    return json.loads((ROOT / name).read_text())


def decoded(text):
    return base64.b64decode(text or '', validate=True)


def source_key(span):
    return span['start'], span['end']


def schema_valid(value, rule, root):
    """Small evaluator for this closed retained schema, no package dependency."""
    if '$ref' in rule:
        return schema_valid(value, root['$defs'][rule['$ref'].split('/')[-1]], root)
    if 'oneOf' in rule:
        return sum(schema_valid(value, item, root) for item in rule['oneOf']) == 1
    if 'const' in rule and value != rule['const']:
        return False
    if 'enum' in rule and value not in rule['enum']:
        return False
    kinds = rule.get('type', [])
    kinds = [kinds] if isinstance(kinds, str) else kinds
    if kinds:
        actual = 'null' if value is None else 'boolean' if type(value) is bool else 'integer' if type(value) is int else 'string' if type(value) is str else 'array' if type(value) is list else 'object' if type(value) is dict else 'unknown'
        if actual not in kinds:
            return False
    if isinstance(value, dict):
        props = rule.get('properties', {})
        if any(name not in value for name in rule.get('required', [])):
            return False
        for name, child in value.items():
            if name in props:
                if not schema_valid(child, props[name], root):
                    return False
            elif rule.get('additionalProperties') is False:
                return False
            elif isinstance(rule.get('additionalProperties'), dict):
                if not schema_valid(child, rule['additionalProperties'], root):
                    return False
    if isinstance(value, list):
        if len(value) > rule.get('maxItems', 1000000):
            return False
        if any(not schema_valid(child, rule.get('items', {}), root) for child in value):
            return False
    if isinstance(value, str):
        if len(value) > rule.get('maxLength', 100000000):
            return False
        if 'pattern' in rule and not re.fullmatch(rule['pattern'], value):
            return False
        if rule.get('contentEncoding') == 'base64':
            try:
                decoded(value)
            except ValueError:
                return False
    if type(value) is int and value < rule.get('minimum', value):
        return False
    return True


def verify_spans(node, raw):
    count = 0
    if isinstance(node, list):
        return sum(verify_spans(x, raw) for x in node)
    if not isinstance(node, dict):
        return count
    if set(node) == {'start', 'end', 'sha256'}:
        a, b = node['start'], node['end']
        if type(a) is not int or type(b) is not int or not 0 <= a < b <= len(raw) or sha(raw[a:b]) != node['sha256']:
            raise ValueError('invalid output source range/hash; no context retained')
        return 1
    for value in node.values():
        count += verify_spans(value, raw)
    return count


def safe_acl_projection(blob, raw):
    if blob['sites'] or blob['native']:
        raise ValueError('ACL emitted a foreign fact shape')
    for token in blob['principals'] + [p['principal'] for p in blob['acl_pairs']] + [p['pattern'] for p in blob['acl_pairs']]:
        if set(token) != {'token', 'span'} or decoded(token['token']) != raw[token['span']['start']:token['span']['end']]:
            raise ValueError('ACL safe token projection/citation mismatch')
    return True


def credential_scan(all_output, source_files):
    """Safety scan only. No credential bytes/spans/digests enter the report.

    Principal/pattern provenance is checked separately against safe labels;
    public payload/username collisions cannot be treated as a substring ban.
    """
    checked = 0
    for (population, name), raw in source_files.items():
        if not name.endswith('.acl'):
            continue
        distinct = population.startswith('control/') and ('adjacent-credentials' in name or 'pattern-looking-secret' in name)
        for word in re.findall(rb'[^\s()]+', raw):
            if len(word) < 2 or word[:1] not in (b'>', b'<', b'#', b'!'):
                continue
            checked += 1
            forbidden = [word, base64.b64encode(word)]
            if distinct or word[:1] in (b'#', b'!'):
                forbidden += [word[1:], base64.b64encode(word[1:])]
            if any(secret in all_output for secret in forbidden):
                raise ValueError('credential non-retention failure; token/context withheld')
    return checked


def score(output, stderr, manifest, source_files):
    if len(output) > 32 << 20 or len(stderr) > 65536:
        raise ValueError('outer output bound')
    records = [json.loads(line) for line in output.splitlines()]
    schema = read_json('output-schema.json')
    if any(not schema_valid(record, schema, schema) for record in records):
        raise ValueError('closed output schema refusal; payload withheld')
    blobs, bindings, endings = {}, [], []
    spans = 0
    allowed_blob = {'population', 'repository', 'commit', 'module_path', 'client_version', 'source_scope', 'path', 'sha256', 'role', 'status', 'boundary', 'sites', 'native', 'principals', 'acl_pairs'}
    inputs = {(f['population'], f['path']): f for f in manifest['files']}
    semantic = set(read_json('bounds.json')['semantic_unresolved'])
    boundary = set(read_json('bounds.json')['structural_boundary_refusals'])
    for record in records:
        kind = record.get('type')
        if kind == 'end':
            if set(record) != {'type', 'files'}:
                raise ValueError('unknown terminal shape')
            endings.append(record)
            continue
        if kind == 'binding':
            if set(record) != {'type', 'binding'}:
                raise ValueError('unknown binding record')
            item = record['binding']; key = (item['population'], item['path'])
            spans += verify_spans(item, source_files[key]); bindings.append(item)
            continue
        if kind != 'blob' or set(record) != {'type', 'blob'}:
            raise ValueError('unknown output record')
        blob = record['blob']; key = (blob['population'], blob['path'])
        if set(blob) - allowed_blob or key not in inputs or key in blobs:
            raise ValueError('unknown/duplicate blob shape/identity')
        expected = inputs[key]
        for field in ('population', 'repository', 'commit', 'path', 'sha256', 'role'):
            if blob[field] != expected[field]:
                raise ValueError('source provenance drift')
        if blob['status'] == 'refused':
            if blob.get('boundary') not in boundary or any(blob[n] for n in ('sites', 'native', 'principals', 'acl_pairs')):
                raise ValueError('non-atomic boundary refusal')
        elif blob['status'] != 'processed' or blob.get('boundary'):
            raise ValueError('unknown blob state')
        raw = source_files.get(key, b'')
        spans += verify_spans(blob, raw)
        for site in blob['sites']:
            for keyfact in site['keys']:
                is_script=keyfact['predicate']=='PASSES_REDIS_KEY_TO_SCRIPT'
                if is_script != bool(keyfact.get('script_pass')) or (is_script and (keyfact['access'] or keyfact.get('expected_kind'))):
                    raise ValueError('script evidence inherited access/kind semantics')
            if set(site['gaps']) - semantic:
                raise ValueError('unknown semantic refusal')
        if blob['role'] == 'acl':
            safe_acl_projection(blob, raw)
        blobs[key] = blob
    if set(blobs) != set(inputs) or len(endings) != 1 or records[-1] != endings[0] or endings[0]['files'] != len(inputs):
        raise ValueError('incomplete output census')
    scanned = credential_scan(output + stderr, source_files)
    errors = []

    def expect(condition, code, identity):
        if not condition:
            errors.append({'class': code, 'identity': identity})

    usage = read_json('labels/public-usage.json')
    counters = Counter()
    for f in usage['files']:
        blob = blobs[f['population'], f['source_path']]
        expected_scope = sha((f['repository'] + '\0' + f['module_path'] + '\0github.com/redis/go-redis/v9').encode())
        expect(blob.get('module_path') == f['module_path'] and blob.get('client_version') == f['actual_version'] and blob.get('source_scope') == expected_scope, 'module/version/source-scope', f['source_path'])
        counters['observed_command_sites'] += len(blob['sites'])
        counters['observed_recognized_commands'] += sum(s['recognized'] for s in blob['sites'])
        for observed in blob['sites']:
            for item in observed['keys']:
                counters['observed_position_modes'] += bool(item['access'])
                counters['observed_known_kinds'] += bool(item.get('expected_kind'))
                counters['observed_concrete_keys'] += item['value']['state'] == 'literal'
        actual = {source_key(s['span']): s for s in blob['sites']}
        expect(len(actual) == len(blob['sites']), 'duplicate-site', f['source_path'])
        expected_sites = {source_key(s['method_token']): s for s in f['sites']}
        expect(set(actual) == set(expected_sites), 'complete-sample-site-census', f['source_path'])
        for key, truth in expected_sites.items():
            identity = f['source_path'] + ':' + truth['id']
            if key not in actual:
                continue
            site = actual[key]
            expect(site['method'] == truth['method'], 'method-token', identity)
            expect(site['recognized'] == truth['expected_command_recognized'], 'command-admission', identity)
            if truth.get('expected_command'):
                expect(site.get('command') == truth['expected_command'], 'command-identity', identity)
            if truth.get('expected_unresolved'):
                expect(truth['expected_unresolved'] in site['gaps'], 'unresolved-class', identity)
            expect(len(site['keys']) == len(truth['keys']), 'key-position-census', identity)
            for actual_key, gold in zip(site['keys'], truth['keys']):
                expect(source_key(actual_key['value']['span']) == source_key(gold['expression_span']), 'key-expression-citation', identity)
                expect(actual_key['value']['state'] == 'unresolved' and actual_key['value'].get('reason') == gold['expected_reason'], 'key-abstention', identity)
                expect(actual_key['access'] == gold['expected_access_flags'], 'position-access-vocabulary', identity)
                expect(actual_key.get('expected_kind') == gold.get('expected_kind'), 'separate-kind-table', identity)

    acl = read_json('labels/public-acl.json')
    for f in acl['files']:
        blob = blobs['redis', f['source_path']]
        principal = lambda p: (decoded(p['token']), source_key(p['span']))
        actual_principals = [principal(p) for p in blob['principals']]
        expected_principals = [(p['token'].encode(), (p['start'], p['end'])) for p in f['principals']]
        actual_pairs = [(principal(p['principal']), principal(p['pattern']), p['intent']) for p in blob['acl_pairs']]
        expected_pairs = [((p['principal']['token'].encode(), (p['principal']['start'], p['principal']['end'])), (p['pattern']['token'].encode(), (p['pattern']['start'], p['pattern']['end'])), p['authorization_intent']) for p in f['pairs']]
        expect(actual_principals == expected_principals, 'safe-principal-census', f['source_path'])
        expect(actual_pairs == expected_pairs, 'safe-pattern-pair-census', f['source_path'])
    for site in read_json('labels/native-declarations.json')['index_sites']:
        expect(not blobs['eino', site['source_path']]['native'], 'public-native-atomicity', site['source_path'])

    for witness in read_json('projection-witnesses.json')['cases']:
        raw=source_files['control/'+witness['id'],witness['source_path']]
        if sha(raw)!=witness['source_sha256']:
            raise ValueError('supplemental source projection witness drift')
        actual=blobs['control/'+witness['id'],witness['source_path']]['native']
        projected=[]
        for fact in actual:
            item={k:v for k,v in fact.items() if k!='source'}
            if 'fields' in item:
                item['fields']=[{k:v for k,v in f.items() if k!='span'} for f in item['fields']]
            projected.append(item)
        expect(projected==witness['expected_native_projection'],'control-complete-native-projection',witness['id'])

    control_results = []
    for case in read_json('controls.json')['cases'] + read_json('controls.json')['admission_controls']:
        pop = 'control/' + case['id']; blob = blobs[pop, case['source_path']]
        exp = case['expected']; sites = blob['sites']; keys = [k for s in sites for k in s['keys']]
        native = blob['native']; matching_bindings = [b for b in bindings if b['population'] == pop and b['path'] == case['source_path']]
        before = len(errors)
        counts = {'recognized': sum(s['recognized'] for s in sites), 'key_expressions': len(keys), 'resolved_keys': sum(k['value']['state'] == 'literal' and not k.get('script_pass') for k in keys), 'resolved_structures': sum(k['value']['state'] == 'structure' for k in keys), 'script_passthrough_rows': sum(k.get('script_pass', False) for k in keys), 'key_access_rows': sum(bool(k['access']) for k in keys), 'expected_kind_rows': sum(bool(k.get('expected_kind')) for k in keys), 'native_declarations': len(native), 'index_fields': sum(len(n.get('fields', [])) for n in native), 'complete_value_models': 0, 'entry_fields': 0, 'resource_references': sum(len(n.get('references', [])) for n in native), 'unique_family_bindings': sum(b['state'] == 'bound-indexed-projection' for b in matching_bindings), 'principal_pattern_pairs': len(blob['acl_pairs']), 'cluster_claims': 0, 'all_fact_rows': len(sites)+len(keys)+len(native)+len(blob['principals'])+len(blob['acl_pairs'])}
        for name in counts:
            if name in exp:
                expect(counts[name] == exp[name], 'control-' + name, case['id'])
        if exp.get('decision') == 'refuse':
            expect(blob['status'] == 'refused' and blob.get('boundary') == exp['boundary_reason'], 'control-boundary', case['id'])
        observed_gaps = set(g for s in sites for g in s['gaps']) | {b['state'] for b in matching_bindings if b['state'] == 'ambiguous-key-family'}
        if 'gaps' in exp:
            expect(observed_gaps == set(exp['gaps']), 'control-gap-vocabulary', case['id'])
        if 'access' in exp:
            expect([k['access'] for k in keys] == exp['access'], 'control-access', case['id'])
        if 'expected_kinds' in exp:
            expect([k.get('expected_kind') for k in keys] == exp['expected_kinds'], 'control-kind', case['id'])
        actual_values = [decoded(k['value'].get('bytes')) for k in keys if k['value']['state'] == 'literal']
        if case.get('key_labels'):
            expect(actual_values == [decoded(k['bytes_base64']) for k in case['key_labels']], 'control-binary-key-values', case['id'])
        if 'hash_tag_bytes_base64' in exp:
            expect([k.get('hash_tag', '') for k in keys] == exp['hash_tag_bytes_base64'], 'control-hash-tag-byte-semantics', case['id'])
        if 'hash_tag_state' in exp:
            expect(all(k.get('hash_tag_state') == exp['hash_tag_state'] for k in keys), 'control-format-hash-tag', case['id'])
        if 'format_segments' in exp:
            projected = []
            for k in keys:
                for part in k['value'].get('segments', []):
                    projected.append({'kind': 'literal', 'bytes_base64': part['literal']} if part['kind'] == 'literal' else part)
            expect(projected == exp['format_segments'], 'control-format-structure', case['id'])
        if 'key_cardinality' in exp:
            expect(any(s.get('key_cardinality') == exp['key_cardinality'] for s in sites), 'control-script-cardinality', case['id'])
        if 'native_kind' in exp:
            expect([n['kind'] for n in native] == [exp['native_kind']], 'control-native-kind', case['id'])
        if case['source_class'] == 'acl' and exp.get('decision') != 'refuse':
            actual_safe = [decoded(t['token']) for t in blob['principals']] + [decoded(p['pattern']['token']) for p in blob['acl_pairs']]
            expected_safe = [t['token'].encode() for t in case['principal_tokens'] + case['pattern_tokens']]
            expect(actual_safe == expected_safe, 'control-safe-acl-token-census', case['id'])
        control_results.append({'id': case['id'], 'mismatches': len(errors)-before})
    return {'schema': 't281-finite-source-score-v1', 'outcome': 'pass' if not errors else 'quality-gaps-no-production-adoption', 'public_denominators': usage['denominators'], 'observed_public_sample': dict(counters), 'native_public': {'known_source_sites': 2, 'static_resource_names': 1, 'atomic_complete_source_shapes': 0, 'admitted_exact_version_sites': 0, 'candidate_emitted_native_facts': sum(len(blobs['eino', s['source_path']]['native']) for s in read_json('labels/native-declarations.json')['index_sites'])}, 'acl_public': {'principals': 7, 'pairs': 7, 'safe_source_census_matches': not any(e['class'].startswith('safe-') for e in errors)}, 'source_span_round_trips': spans, 'credential_token_scans': scanned, 'credential_failures': 0, 'control_results': control_results, 'mismatches': errors, 'qualification': 'Finite frozen source expectations only; no runtime Redis/client/generator, production registration, independent population accuracy or scale result. Generated/opaque/unmeasured quantities remain separate.'}
