#!/usr/bin/env python3
"""Source-only oracle. No SCIP, compiler or production extractor imports/reads."""
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent
KEYWORDS = set('break default func interface select case defer go map struct chan else goto package switch const fallthrough if range type continue for import return var'.split())
# Complete positive field uses in two selected unchanged handwritten sources.
PUBLIC_TAG = {'Key': ('jaeger.Tag', 1), 'VType': ('jaeger.Tag', 2), 'VStr': ('jaeger.Tag', 3), 'VDouble': ('jaeger.Tag', 4), 'VBool': ('jaeger.Tag', 5), 'VLong': ('jaeger.Tag', 6)}
PUBLIC_SAMPLING = {'StrategyType': ('sampling.SamplingStrategyResponse', 1), 'ProbabilisticSampling': ('sampling.SamplingStrategyResponse', 2), 'SamplingRate': ('sampling.ProbabilisticSamplingStrategy', 1)}
NEUTRAL = {'Code': ('neutral.Parcel', 1), 'Count': ('neutral.Parcel', 7), 'Tags': ('neutral.Parcel', 9), 'Attrs': ('neutral.Parcel', 13), 'Success': ('neutral.MessengerSendResult', 0)}


def digest(raw):
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def tokens(raw):
    # ASCII Go identifiers in these frozen ASCII identifier sources. Comments,
    # interpreted/raw strings and rune literals are excluded, including escapes.
    pattern = rb'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|`[^`]*`|\'(?:\\.|[^\'\\])*\'|[A-Za-z_][A-Za-z_0-9]*'
    for found in re.finditer(pattern, raw):
        value = found.group().decode('utf-8')
        if re.fullmatch('[A-Za-z_][A-Za-z_0-9]*', value) and value not in KEYWORDS:
            yield found.start(), found.end(), value


def main():
    rows = []
    cases = [('public', 'jaeger_tag.go'), ('public', 'testutils/sampling_manager.go'), ('neutral', 'use/use.go')]
    for population, path in cases:
        raw = (ROOT / 'testdata' / population / path).read_bytes()
        for start, end, text in tokens(raw):
            expected, reason, identity = 'negative', 'ordinary Go identifier/type/method/local, not a generated tagged field use', None
            if population == 'public':
                declared = PUBLIC_TAG if path == 'jaeger_tag.go' else PUBLIC_SAMPLING
                if text in declared and re.match(rb'\s*:', raw[end:]):
                    identity = declared[text]
            else:
                prefix = raw[max(0, start-16):start]
                if text in NEUTRAL and (re.match(rb'\s*:', raw[end:]) or re.search(rb'(?:p|result)\.$', prefix)):
                    identity = NEUTRAL[text]
                if text in {'GetCode', 'Left', 'Right'} and re.search(rb'(?:p|ambiguous)\.$', prefix):
                    expected, reason = 'unsupported', 'Apache accessor method or duplicate wire-ID generated identity; no unique tagged-field reference'
            if identity:
                expected, reason = 'positive', 'source-authored field identity from exact composite key/direct selector and pinned generated tag/IDL witness'
            row = {'population': population, 'path': path, 'source_digest': digest(raw), 'start_byte': start, 'end_byte': end, 'line': raw.count(b'\n', 0, start)+1, 'text': text, 'expected': expected, 'reason': reason}
            if identity:
                row.update({'message': identity[0], 'field_number': identity[1], 'object': identity[0]+'#'+str(identity[1]), 'tier': 'derived', 'generator': 'apache', 'source_binding': 'none'})
            rows.append(row)
        if population == 'neutral':
            marker = b'FieldByName("Code")'
            assert raw.count(marker) == 1
            start = raw.index(marker) + len(b'FieldByName("')
            rows.append({'population': population, 'path': path, 'source_digest': digest(raw), 'start_byte': start, 'end_byte': start+4, 'line': raw.count(b'\n', 0, start)+1, 'text': 'Code', 'expected': 'unsupported', 'reason': 'dynamic reflection string is not an exact indexed field-use identifier'})
    rows.sort(key=lambda r: (r['population'], r['path'], r['start_byte']))
    counts = {}
    for row in rows:
        key = row['population']+'/'+row['expected']
        counts[key] = counts.get(key, 0)+1
    assert counts['public/positive'] == 33, counts
    assert counts['neutral/positive'] == 11, counts
    assert counts['neutral/unsupported'] == 4, counts
    data = ''.join(json.dumps(row, sort_keys=True, ensure_ascii=False)+'\n' for row in rows).encode()
    target = ROOT/'source-labels.jsonl'
    if target.exists() and target.read_bytes() != data:
        raise ValueError('frozen label bytes changed; obtain independent review before replacement')
    target.write_bytes(data)
    files = []
    for path in sorted((ROOT/'testdata').rglob('*')):
        if path.is_file():
            raw = path.read_bytes()
            files.append({'path': str(path.relative_to(ROOT)), 'bytes': len(raw), 'digest': digest(raw)})
    record = {'schema': 't521-source-label-freeze-v1', 'record_date': '2026-10-18', 'counts': counts, 'labels_digest': digest(data), 'labels_bytes': len(data), 'candidate_executed': False, 'indexer_executed': False, 'files': files}
    (ROOT/'source-freeze.json').write_text(json.dumps(record, indent=2)+'\n')
    print(json.dumps({'counts': counts, 'labels_digest': digest(data), 'input_bytes': sum(f['bytes'] for f in files)}))

if __name__ == '__main__':
    main()
