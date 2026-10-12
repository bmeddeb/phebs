"""T47.2s: blind, source-only label sheets for a sealed T47.2b draw.

Every sampled site appears once, ordered by sha256(seed || site_id) so that
position reveals no frame. The sheet holds the immutable coordinate and exact
source context read from the pinned commit; frame membership, predicates and
every other candidate field go only to a separate candidate-side map. Blank
rows follow the unchanged label, expected-resolution and declaration-citation
templates. Nothing is labeled here.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
from pathlib import Path

import frames
import seal

CONTEXT_LINES = 6
MAX_SPAN_LINES = 80
LABEL_ROW = {'site_id': None, 'invocation': None, 'operation': None, 'registration': None,
             'service': None, 'expected_code_role': None, 'rationale': '', 'evidence': ''}
RESOLUTION_ROW = {'site_id': None, 'state': None, 'alternatives': [], 'rationale': ''}
CITATION_ROW = {'site_id': None, 'state': None, 'operation': None, 'lineage': None,
                'declaration_site_id': None, 'rationale': ''}
CALLER_FRAMES = ('recall', 'precision', 'unresolved')


class SheetError(ValueError):
    """The sample or source cannot produce an exact blind sheet."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SheetError(message)


def parse_site(site_id: str) -> tuple[str, str, str, int, int]:
    repository, rest = site_id.split('@', 1)
    commit, rest = rest.split(':', 1)
    path, span = rest.rsplit(':', 1)
    start, end = (int(v) for v in span.split('-'))
    require(len(commit) == 40 and 0 <= start < end, f'invalid site coordinate {site_id}')
    return repository, commit, path, start, end


def excerpt(content: bytes, start: int, end: int) -> dict:
    require(end <= len(content), 'site span exceeds its source blob')
    starts = [0] + [i + 1 for i, b in enumerate(content) if b == 0x0A]
    def line_of(offset):  # 1-based line containing byte offset
        lo, hi = 0, len(starts) - 1
        while lo < hi:
            mid = (lo + hi + 1) // 2
            if starts[mid] <= offset:
                lo = mid
            else:
                hi = mid - 1
        return lo + 1
    first, last = line_of(start), line_of(end - 1)
    shown_last = min(last, first + MAX_SPAN_LINES - 1)
    lines = content.decode('utf-8', errors='replace').split('\n')
    lo, hi = max(1, first - CONTEXT_LINES), min(len(lines), shown_last + CONTEXT_LINES)
    return {'start_line': first, 'end_line': last, 'span_text': content[start:end].decode('utf-8', errors='replace'),
            'context': [{'line': n, 'in_span': first <= n <= last, 'text': lines[n - 1]} for n in range(lo, hi + 1)],
            'span_truncated': shown_last < last}


def blind_order(seed_hex: str, site_ids) -> list[str]:
    return sorted(site_ids, key=lambda s: hashlib.sha256((seed_hex + '\x00' + s).encode()).hexdigest())


def build(sample: dict, read_source, attribution_ids: set[str]) -> tuple[list[dict], dict]:
    """Return (blind sheet rows, candidate-side membership map)."""
    membership: dict[str, list[dict]] = {}
    for frame, strata in sample['samples'].items():
        for stratum, ids in strata.items():
            for site_id in ids:
                membership.setdefault(site_id, []).append({'frame': frame, 'stratum': stratum})
    rows = []
    for position, site_id in enumerate(blind_order(sample['seed_hex'], membership), 1):
        repository, commit, path, start, end = parse_site(site_id)
        frames_here = {m['frame'] for m in membership[site_id]}
        # The task kind follows from the source unit, not the candidate: interfaces vs call sites.
        require(not (frames_here & {'attribution'}) or site_id in attribution_ids, 'attribution site outside the census')
        kind = 'service_client_interface' if site_id in attribution_ids else 'call_site'
        content = read_source(repository, commit, path)
        rows.append({'position': position, 'site_id': site_id, 'unit': kind, 'repository': repository,
                     'commit': commit, 'path': path, 'start_byte': start, 'end_byte': end,
                     'source_blob_sha256': 'sha256:' + hashlib.sha256(content).hexdigest(),
                     **excerpt(content, start, end)})
    return rows, {'schema': 't472-candidate-membership-v1', 'seed_hex': sample['seed_hex'],
                  'membership': dict(sorted(membership.items()))}


def review_markdown(rows: list[dict]) -> str:
    out = ['# T47.2b blind label review (source only)', '',
           'Order is seed-keyed and carries no frame information. Lines marked `>` are the site span.',
           'Fill `labels.jsonl` (all units), `expected-resolution.jsonl` and `declaration-citation.jsonl`',
           '(call sites only) per `labels/README.md` of the T47.2m preparation packet.', '']
    for row in rows:
        out += [f"## {row['position']}. {row['unit']}", '',
                f"`{row['site_id']}`", '', f"{row['repository']} `{row['path']}` lines {row['start_line']}-{row['end_line']}"
                + (' (span truncated)' if row['span_truncated'] else ''), '', '```go']
        out += [f"{'>' if c['in_span'] else ' '} {c['line']:>6} {c['text']}" for c in row['context']]
        out += ['```', '']
    return '\n'.join(out) + '\n'


def corpus_reader(corpus: Path):
    def read(repository: str, commit: str, path: str) -> bytes:
        directory = corpus / repository.rsplit('/', 1)[1]
        return subprocess.run(['git', '-C', str(directory), 'show', f'{commit}:{path}'], check=True,
                              capture_output=True, env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'}).stdout
    return read


def write_jsonl(path: Path, rows) -> str:
    raw = b''.join(json.dumps(r, sort_keys=True, ensure_ascii=False).encode() + b'\n' for r in rows)
    with open(path, 'xb') as f:
        f.write(raw)
    os.chmod(path, 0o600)
    return seal.sha(raw)


def run(sample_path: Path, corpus: Path, sheet_dir: Path, candidate_dir: Path) -> dict:
    sample_raw = sample_path.read_bytes()
    sample = json.loads(sample_raw)
    require(sample.get('schema') == seal.SAMPLE_SCHEMA, 'not a sealed planned sample')
    attribution = {r['site_id'] for r in frames.load_jsonl(seal._SPIKE / 'frames' / 'frames.attribution.jsonl')}
    rows, membership = build(sample, corpus_reader(corpus), attribution)
    sheet_dir.mkdir(mode=0o700)
    digests = {'review.md': None}
    (sheet_dir / 'review.md').write_text(review_markdown(rows))
    os.chmod(sheet_dir / 'review.md', 0o600)
    digests['review.md'] = seal.sha((sheet_dir / 'review.md').read_bytes())
    digests['sites.jsonl'] = write_jsonl(sheet_dir / 'sites.jsonl', rows)
    digests['labels.jsonl'] = write_jsonl(sheet_dir / 'labels.jsonl', [{**LABEL_ROW, 'site_id': r['site_id']} for r in rows])
    callers = [r for r in rows if r['unit'] == 'call_site']
    digests['expected-resolution.jsonl'] = write_jsonl(sheet_dir / 'expected-resolution.jsonl',
                                                       [{**RESOLUTION_ROW, 'site_id': r['site_id']} for r in callers])
    digests['declaration-citation.jsonl'] = write_jsonl(sheet_dir / 'declaration-citation.jsonl',
                                                        [{**CITATION_ROW, 'site_id': r['site_id']} for r in callers])
    candidate_dir.mkdir(mode=0o700)
    membership_raw = (json.dumps(membership, indent=1, sort_keys=True) + '\n').encode()
    with open(candidate_dir / 'membership.json', 'xb') as f:
        f.write(membership_raw)
    os.chmod(candidate_dir / 'membership.json', 0o600)
    manifest = {'schema': 't472-blind-sheet-manifest-v1', 'sample_sha256': seal.sha(sample_raw),
                'seal_document_sha256': sample['seal_document_sha256'], 'units': len(rows),
                'call_sites': len(callers), 'interfaces': len(rows) - len(callers),
                'sheet_sha256': digests, 'membership_sha256': seal.sha(membership_raw),
                'blind': 'no frame, predicate, resolution, lineage, operation or score appears in the sheet'}
    (sheet_dir / 'manifest.json').write_text(json.dumps(manifest, indent=1, sort_keys=True) + '\n')
    return manifest


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--sample', type=Path, required=True)
    parser.add_argument('--corpus', type=Path, required=True)
    parser.add_argument('--sheet-dir', type=Path, required=True)
    parser.add_argument('--candidate-dir', type=Path, required=True)
    args = parser.parse_args()
    manifest = run(args.sample, args.corpus, args.sheet_dir, args.candidate_dir)
    print(json.dumps({k: manifest[k] for k in ('units', 'call_sites', 'interfaces')}))


if __name__ == '__main__':
    main()
