"""Closed finite source-oracle checker, never a Go parser or recognizer.

It joins every raw SDK emission to independently frozen literal source labels.
Unknown/lookalike positive promotions are failures, never relabelled matches.
"""
from collections import Counter
import hashlib
import json


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate JSON key')
        result[key] = value
    return result


def load_json(raw):
    return json.loads(raw, object_pairs_hook=unique_object)


def operation(candidate):
    return '/' + candidate['scope_hint'] + '.' + candidate['service_hint'] + '/' + candidate['wire_hint']


def source_labels(root):
    public = load_json((root / 'public-sites.json').read_bytes())['sites']
    controls = load_json((root / 'controls.json').read_bytes())['cases']
    labels = {'public': [{'id': row['id'], 'span': row['callee'], 'role': row['role'],
                         'expected': row['expected'], 'path': row['callee']['path'].removeprefix('inputs/public/')}
                        for row in public]}
    rules = {'public': None}
    for case in controls:
        labels[case['id']] = [{'id': case['id'] + '/' + str(index), 'span': row['site'],
                             'role': row['role'], 'expected': row['expected'], 'path': 'consumer.go'}
                            for index, row in enumerate(case['sites'])]
        rules[case['id']] = case['required_file_outcome']
    witnesses = load_json((root / 'call-span-witnesses.json').read_bytes())['witnesses']
    endpoints = {(x['unit'], x['site']): x for x in witnesses}
    wanted = {(unit, row['id']) for unit, rows in labels.items() for row in rows
              if row['role'].startswith(('supported-', 'ambiguous-'))}
    if len(witnesses) != 13 or len(endpoints) != 13 or set(endpoints) != wanted:
        raise ValueError('closed endpoint witness census')
    for unit, rows in labels.items():
        for row in rows:
            if (unit, row['id']) not in endpoints:
                continue
            witness = endpoints[unit, row['id']]
            span = witness['span']; raw = (root / witness['source_path']).read_bytes()
            a, b = span['start_byte'], span['end_byte']
            if witness['source_path'] != row['span']['path'] or a != row['span']['start_byte'] or not 0 <= a < b <= len(raw) or raw[a:b] != span['text'].encode() or sha(raw[a:b]) != span['sha256']:
                raise ValueError('literal endpoint source fence')
            row['whole_call'] = span
    return labels, rules


def require_keys(value, keys):
    if not isinstance(value, dict) or set(value) != set(keys.split()):
        raise ValueError('closed raw output shape')


def check_raw(unit, request, source):
    """Independent immutable blob/range/line check, before semantic scoring."""
    require_keys(unit, 'schema id repository commit files facts coverage source_outcomes source_outcome_qualification read_bytes error')
    require_keys(unit['coverage'], 'Protocols Failures UnresolvedCount ExcludedSCIPDocuments ExcludedSCIPDefinitions ExcludedSCIPOccurrences Diagnostics')
    if unit['schema'] != 't582-unit-output-v1' or unit['id'] != request['id'] or unit['error']:
        raise ValueError('incomplete or foreign unit output')
    if unit['repository'] != request['repository'] or unit['commit'] != request['commit']:
        raise ValueError('unit provenance drift')
    expected_files = {f['path']: f for f in request['files']}
    if len(unit['files']) != len(expected_files) or len({f['path'] for f in unit['files']}) != len(expected_files):
        raise ValueError('file census drift')
    read_bytes = 0
    for f in unit['files']:
        require_keys(f, 'path origin sha256 bytes context reads')
        expected = expected_files[f['path']]
        for key in ['path', 'origin', 'sha256', 'context']:
            if f[key] != expected[key]:
                raise ValueError('file metadata drift')
        raw = source[unit['id'], f['path']]
        if f['bytes'] != len(raw) or not 1 <= f['reads'] <= 2:
            raise ValueError('mandatory read/byte census')
        read_bytes += f['reads'] * len(raw)
    if read_bytes != unit['read_bytes']:
        raise ValueError('read-byte accounting drift')
    unresolved = set()
    for value in unit['facts']:
        require_keys(value, 'raw_fact source_slice_sha256 validation_error')
        f = value['raw_fact']; atom = f['Atom']; assertion = f['Assertion']
        require_keys(f, 'Atom Assertion Path StartLine EndLine')
        require_keys(atom, 'SchemaVersion BlobDigest StartByte EndByte RuleID AdapterConfigDigest FactFingerprint')
        require_keys(assertion, 'Predicate Subject Object Lineage Tier CodeRole Detail')
        if value['validation_error']:
            raise ValueError('exporter source fence failed; raw fact retained')
        raw = source[unit['id'], f['Path']]
        a, b = atom['StartByte'], atom['EndByte']
        if type(a) is not int or type(b) is not int or not 0 <= a < b <= len(raw):
            raise ValueError('raw source range fence')
        if atom['BlobDigest'] != 'sha256:' + sha(raw) or value['source_slice_sha256'] != sha(raw[a:b]):
            raise ValueError('raw source digest fence')
        line_end = b - 1 if assertion['Predicate'] == 'THRIFT_EXTRACTION_GAP' else b
        if f['StartLine'] != 1 + raw[:a].count(b'\n') or f['EndLine'] != 1 + raw[:line_end].count(b'\n'):
            raise ValueError('raw source line fence')
        if atom['SchemaVersion'] != 't19-v1' or assertion['Subject'] != f['Path']:
            raise ValueError('raw source/schema placement')
        if atom['AdapterConfigDigest'] != 'sha256:' + sha(b'') or not atom['RuleID']:
            raise ValueError('raw adapter/rule placement')
        if assertion['Tier'] == 'unresolved':
            unresolved.add((f['Path'], assertion['Predicate'], assertion['Object']))
    for outcome in unit['source_outcomes']:
        require_keys(outcome, 'path state reason blob_digest')
        if outcome['path'] not in expected_files or outcome['blob_digest'] != 'sha256:' + expected_files[outcome['path']]['sha256']:
            raise ValueError('SDK source outcome provenance')
    diagnostics = unit['coverage']['Diagnostics'] or []
    for counter in diagnostics:
        require_keys(counter, 'name value')
        if type(counter['value']) is not int or counter['value'] < 0:
            raise ValueError('diagnostic counter type/range')
    if len({x['name'] for x in diagnostics}) != len(diagnostics):
        raise ValueError('duplicate diagnostic counter')
    if unit['coverage']['UnresolvedCount'] != len(unresolved) or unit['coverage']['Failures']:
        raise ValueError('unresolved/failure accounting drift')
    return expected_files


def exact_span(fact, label):
    span = label['whole_call']; atom = fact['Atom']
    return (fact['Path'] == label['path'] and atom['StartByte'] == span['start_byte'] and
            atom['EndByte'] == span['end_byte'] and fact['StartLine'] == span['start_line'] and
            fact['EndLine'] == span['end_line'])


def match_hint(fact, expected, request):
    assertion = fact['Assertion']; detail = load_json(assertion['Detail'])
    if assertion['Predicate'] != expected['predicate'] or assertion['Tier'] != expected['tier']:
        return False
    fqn = expected['scope_hint'] + '.' + expected['service_hint']
    stub = 'thrift-gen/agent/agent.go'  # Exact frozen positive source witness, not inferred from output.
    lineage = 'provisional_repo_path_v1_' + sha((request['repository'] + '\0' + stub).encode())
    if assertion['Lineage'] != lineage:
        return False
    if expected['predicate'] == 'CALLS_OPERATION':
        return (assertion['Object'] == operation(expected) and detail == {
            'schema': 'thriftgo-call-detail-v1', 'method': expected['wire_hint'], 'go_method': expected['method_hint']})
    return (assertion['Object'] == fqn and detail == {
        'schema': 'thriftgo-register-detail-v1', 'constructor': expected['constructor_hint']})


def check_ambiguity(rows, expected, label):
    predicate = expected['predicate']; count = expected['candidate_count']
    if any(not exact_span(f, label) for f in rows):
        return False, 'ambiguity-exact-source-span'
    if any(f['Assertion']['Predicate'] != predicate or f['Assertion']['Tier'] != 'unresolved' or
           f['Assertion']['Lineage'] for f in rows):
        return False, 'ambiguity-state'
    if predicate == 'UNRESOLVED_THRIFT_CALL':
        targets = [operation(x) for x in expected['candidates']]
        if Counter(f['Assertion']['Object'] for f in rows) != Counter(targets):
            return False, 'ambiguity-candidate-set'
        go_name = label['span']['text'].split('.')[-1]
        for f in rows:
            if load_json(f['Assertion']['Detail']) != {'schema': 'thriftgo-call-ambiguity-v1',
                 'method': go_name, 'candidate_count': count, 'candidate_operation': f['Assertion']['Object']}:
                return False, 'ambiguity-detail'
        return True, None
    if len(rows) != 1:
        return False, 'registration-ambiguity-row-count'
    assertion = rows[0]['Assertion']; detail = load_json(assertion['Detail'])
    constructor = label['span']['text'].split('.')[-1]
    if assertion['Object'] != constructor or detail.get('constructor') != constructor or detail.get('candidate_count') != count:
        return False, 'registration-ambiguity-detail'
    # Frozen README/c15 require both source-distinct candidates. Do not invent
    # predictions from oracle/context when the unchanged reader exposes count only.
    if detail.get('candidates') != expected['candidates']:
        return False, 'registration-ambiguity-candidates-not-exposed'
    if detail != {'schema': 'thriftgo-register-ambiguity-v1', 'constructor': constructor,
                  'candidate_count': count, 'candidates': expected['candidates']}:
        return False, 'registration-ambiguity-detail-shape'
    return True, None


def score(raw, plan, source, root):
    if len(raw) > 16 << 20:
        raise ValueError('output input bound')
    values = [load_json(line) for line in raw.splitlines()]
    requests = plan['units']
    if len(values) != len(requests) + 1:
        raise ValueError('complete unit/footer census')
    footer = values.pop()
    require_keys(footer, 'schema preregistration_sha256 units facts domain version go_version goos goarch')
    if footer['schema'] != 't582-pass-complete-v1' or footer['units'] != 27 or footer['domain'] != 'thrift-consumer' or footer['version'] != '1.2.0' or footer['go_version'] != 'go1.26.5' or footer['preregistration_sha256'] != plan['preregistration_sha256']:
        raise ValueError('reader/footer identity')
    labels, file_rules = source_labels(root)
    mismatches, sites, emissions, unit_summaries = [], [], [], []
    for unit, request in zip(values, requests):
        files = check_raw(unit, request, source)
        unit_labels = labels[unit['id']]
        groups = {label['id']: [] for label in unit_labels}
        gaps = []
        seen = set()
        for index, value in enumerate(unit['facts']):
            f = value['raw_fact']; a = f['Assertion']; atom = f['Atom']; issues = []
            identity = (f['Path'], atom['StartByte'], atom['EndByte'], a['Predicate'], a['Object'])
            if identity in seen:
                issues.append('duplicate-emission')
            seen.add(identity)
            if files[f['Path']]['context']:
                issues.append('generated-context-body-emission')
            if a['Tier'] not in ('heuristic', 'derived', 'unresolved'):
                issues.append('unadmitted-or-exact-tier')
            if a['Predicate'] == 'THRIFT_EXTRACTION_GAP':
                gaps.append(f)
            else:
                matching = [label for label in unit_labels if label['path'] == f['Path'] and
                            label['span']['start_byte'] == atom['StartByte'] and label['span']['end_byte'] <= atom['EndByte']]
                if matching:
                    # Nested calls can share a starting byte. The longest frozen
                    # callee prefix within the raw expression identifies its site.
                    label = max(matching, key=lambda x: x['span']['end_byte'])
                    groups[label['id']].append(f)
                else:
                    issues.append('unmatched-emission')
            row = {'unit': unit['id'], 'raw_row': index, 'predicate': a['Predicate'],
                   'object': a['Object'], 'tier': a['Tier'], 'issues': issues,
                   'semantic_disposition': 'See owning site state or explicit unmatched/generated/duplicate issue.'}
            emissions.append(row)
            mismatches.extend({'unit': unit['id'], 'raw_row': index, 'reason': reason} for reason in issues)
        rule = file_rules[unit['id']]
        required_gap = bool(rule and rule.get('predicate') == 'THRIFT_EXTRACTION_GAP')
        if required_gap:
            valid = len(gaps) == 1
            if valid:
                f = gaps[0]; a = f['Assertion']; atom = f['Atom']; content = source[unit['id'], 'consumer.go']
                valid = (f['Path'] == 'consumer.go' and atom['StartByte'] == 0 and atom['EndByte'] == len(content) and
                         a['Tier'] == 'unresolved' and a['Object'] == rule['reason'] and
                         load_json(a['Detail']) == {'schema': 'thriftgo-gap-detail-v1', 'reason': rule['reason']})
            if not valid:
                mismatches.append({'unit': unit['id'], 'reason': 'required-whole-file-gap'})
        elif gaps:
            mismatches.append({'unit': unit['id'], 'reason': 'unexpected-whole-file-gap', 'count': len(gaps)})
        if rule and rule.get('positive_claims') == 0 and any(f['Assertion']['Predicate'] in ('CALLS_OPERATION','REGISTERS_THRIFT_SERVICE') for f in (x['raw_fact'] for x in unit['facts'])):
            mismatches.append({'unit': unit['id'], 'reason': 'forbidden-file-positive'})
        for label in unit_labels:
            rows = groups[label['id']]; role = label['role']; expected = label['expected']; reason = None
            if role in ('supported-method-call', 'supported-processor-construction'):
                valid = len(rows) == 1 and exact_span(rows[0], label) and match_hint(rows[0], expected, request)
                state = 'matched-supported-candidate' if valid else 'missing-or-wrong-supported-candidate'
                if not valid: reason = state
            elif role.startswith('ambiguous-'):
                valid, reason = check_ambiguity(rows, expected, label)
                state = 'matched-explicit-ambiguity' if valid else reason
            elif role in ('excluded-other-call', 'client-construction-no-positive'):
                state = 'excluded-silent' if not rows else 'unexpected-excluded-emission'
                if rows: reason = state
            elif not rows:
                state = 'silent-unproved-or-unsupported-not-analyzed'
            elif all(f['Assertion']['Predicate'] in ('UNRESOLVED_THRIFT_CALL', 'UNRESOLVED_THRIFT_REGISTRATION') and
                     f['Assertion']['Tier'] == 'unresolved' and not f['Assertion']['Lineage'] for f in rows):
                state = 'explicit-unresolved-no-positive'
            else:
                state = 'false-positive-promotion'
                reason = state
            sites.append({'unit': unit['id'], 'site': label['id'], 'role': role, 'raw_rows': len(rows), 'state': state})
            if reason: mismatches.append({'unit': unit['id'], 'site': label['id'], 'reason': reason})
        diagnostics = {x['name']: x['value'] for x in (unit['coverage']['Diagnostics'] or [])}
        if unit['id'] == 'public' and diagnostics.get('call_expressions_inspected') != 102:
            mismatches.append({'unit': 'public', 'reason': 'public-complete-call-census-diagnostic'})
        unit_summaries.append({'unit': unit['id'], 'raw_assertions': len(unit['facts']), 'gap_rows': len(gaps),
            'coverage_unresolved': unit['coverage']['UnresolvedCount'], 'source_outcomes_reported': len(unit['source_outcomes']),
            'coverage_qualification': unit['source_outcome_qualification'], 'diagnostics': diagnostics,
            'read_bytes': unit['read_bytes']})
    if footer['facts'] != len(emissions):
        raise ValueError('whole-pass assertion accounting')
    return {'schema': 't582-finite-source-score-v1', 'decision': 'NO-GO' if mismatches else 'finite-engineering-pass-only',
        'production_adoption': False, 'registration_release_activation': False,
        'qualification': 'Source-derived independently reviewed finite oracle, historical purposive corpus; no blind/general accuracy, exact identity or runtime claim. T13.1/T58.3 remain unsatisfied.',
        'all_raw_assertions': len(emissions), 'all_labelled_sites': len(sites), 'mismatch_count': len(mismatches),
        'mismatches': mismatches, 'site_states': sites, 'emission_census': emissions, 'unit_summaries': unit_summaries,
        'denominators': {'public': dict(Counter(x['role'] for x in labels['public'])),
                         'authored': dict(Counter(x['role'] for key, rows in labels.items() if key != 'public' for x in rows)),
                         'file_controls': {'malformed': 1, 'oversized': 1, 'empty': 1, 'generated_only': 1, 'comments_strings': 1},
                         'unselected_public_population': 'unmeasured', 'runtime_registration': 'unmeasured', 'exact_identity': 'unmeasured'}}
