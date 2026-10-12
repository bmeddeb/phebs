"""T49.1b: seal, seed and oracle-sample draw for the machine-oracle round.

The seal document binds the protocol, the oracle universe and references, the
private candidate export digest, every tool digest, the approved thresholds,
the sample plan and an activation time. Ben publishes it as a single-file gist
before activation. The seed then comes from the first NIST pulse after the
activation minute through T47.2's verified `seal.seed`. The draw samples
oracle claims for Ben to validate from source (97 per declaration predicate
and 97 per reference state, with a census below that) using the frozen
`harness.stratified_sample`. No phebs output is compared here.
"""
from __future__ import annotations

import argparse
import datetime as dt
import gzip
import json
import os
import sys
from pathlib import Path

_HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(_HERE.parent / 't472'))
sys.path.insert(0, str(_HERE.parents[1] / 'pilot' / 'validation'))
import harness  # noqa: E402
import oracle_compare  # noqa: E402
import seal  # noqa: E402  (T47.2 canonical document, gist and NIST verification)

SCHEMA = 't491-seal-commitment-v1'
SAMPLE_SCHEMA = 't491-oracle-sample-v1'
PER_STRATUM = 97
TOOLS = ('enumerate_declarations.py', 'oracle_compare.py', 'export_candidate.py', 'seal_round.py')


def load_universe(directory: Path) -> tuple[dict, list[dict], list[dict]]:
    summary = json.loads((directory / 'universe.json').read_text())
    members = gzip.decompress((directory / 'declarations.jsonl.gz').read_bytes())
    references = gzip.decompress((directory / 'references.jsonl.gz').read_bytes())
    seal.require(seal.sha(gzip.compress(members, mtime=0)) == summary['members_sha256']
                 and seal.sha(gzip.compress(references, mtime=0)) == summary['references_sha256'], 'universe members changed')
    return summary, [json.loads(l) for l in members.splitlines()], [json.loads(l) for l in references.splitlines()]


def strata(declarations: list[dict], references: list[dict]) -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for row in declarations:
        out.setdefault('declaration:' + row['predicate'], []).append(row['site_id'])
    for ref in references:
        state = ref['resolution'] + (':' + ref['reason'] if ref['reason'] else '')
        out.setdefault('reference:' + state, []).append(
            f"{ref['repository']}@{ref['commit']}:{ref['path']}#{ref['object']}#{ref['slot']}")
    return {name: sorted(ids) for name, ids in sorted(out.items())}


def plan(declarations: list[dict], references: list[dict]) -> dict:
    return {name: {'population': len(ids), 'sample_size': min(PER_STRATUM, len(ids)), 'census': len(ids) <= PER_STRATUM}
            for name, ids in strata(declarations, references).items()}


def seal_document(repo_root: Path, universe_dir: Path, candidate_export: Path, activation_at: str,
                  now: dt.datetime) -> dict:
    seal.require(seal.utc(activation_at) > now + dt.timedelta(minutes=5), 'activation must be at least five minutes ahead')
    summary, declarations, references = load_universe(universe_dir)
    protocol = repo_root / 'docs' / 'DECLARATION_QUALITY_PROTOCOL.md'
    return {
        'schema': SCHEMA, 'sealed_by': 'Ben Meddeb', 'activation_at': activation_at,
        'protocol': {'path': 'docs/DECLARATION_QUALITY_PROTOCOL.md', 'sha256': seal.sha(protocol.read_bytes())},
        'universe': {'summary_sha256': seal.sha((universe_dir / 'universe.json').read_bytes()),
                     'members_sha256': summary['members_sha256'], 'references_sha256': summary['references_sha256'],
                     'declarations': len(declarations), 'references': len(references)},
        'candidate_export_sha256': seal.sha(candidate_export.read_bytes()),
        'tools_sha256': {name: seal.sha((_HERE / name).read_bytes()) for name in TOOLS},
        'thresholds': oracle_compare.THRESHOLDS,
        'sample_plan': plan(declarations, references),
        'randomness_rule': 'first NIST Beacon 2.0 pulse strictly after the activation minute; no fallback',
    }


def draw(document_raw: bytes, seed_record: dict, universe_dir: Path) -> dict:
    document = json.loads(document_raw)
    seal.require(seed_record['seal_document_sha256'] == seal.sha(document_raw), 'seed belongs to another seal document')
    summary, declarations, references = load_universe(universe_dir)
    seal.require(summary['members_sha256'] == document['universe']['members_sha256']
                 and summary['references_sha256'] == document['universe']['references_sha256'], 'universe differs from the seal')
    populations = strata(declarations, references)
    seal.require(plan(declarations, references) == document['sample_plan'], 'sample plan differs from the seal')
    sizes = {name: spec['sample_size'] for name, spec in document['sample_plan'].items()}
    return {'schema': SAMPLE_SCHEMA, 'seal_document_sha256': seal.sha(document_raw), 'seed_hex': seed_record['seed_hex'],
            'samples': harness.stratified_sample(populations, sizes, seed_record['seed_hex'])}


def sheet(sample: dict, universe_dir: Path, corpus: Path) -> tuple[str, list[dict]]:
    """Review text plus blank label rows: Ben judges each oracle claim from source (yes/no/unsure)."""
    import subprocess
    _, declarations, references = load_universe(universe_dir)
    decl_by_id = {d['site_id']: d for d in declarations}
    ref_by_id = {f"{r['repository']}@{r['commit']}:{r['path']}#{r['object']}#{r['slot']}": r for r in references}
    spans = {(d['repository'], d['path'], d['object']): (d['start_byte'], d['end_byte']) for d in declarations}
    blobs: dict[tuple, bytes] = {}

    def source(repository: str, commit: str, path: str) -> bytes:
        key = (repository, commit, path)
        if key not in blobs:
            blobs[key] = subprocess.run(['git', '-C', str(corpus / repository.rsplit('/', 1)[1]), 'show', f'{commit}:{path}'],
                                        check=True, capture_output=True, env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'}).stdout
        return blobs[key]
    units = sorted(((stratum, unit) for stratum, ids in sample['samples'].items() for unit in ids),
                   key=lambda pair: pair[1])
    lines, labels = ['# T49.1b oracle validation sample', '',
                     'Judge each oracle claim from the source shown: `yes`, `no` or `unsure`, with a rationale.', ''], []
    for number, (stratum, unit) in enumerate(units, 1):
        if unit in decl_by_id:
            row = decl_by_id[unit]
            start, end = row['start_byte'], row['end_byte']
            claim = f"{row['predicate']} {row['object']}"
        else:
            row = ref_by_id[unit]
            start, end = spans[(row['repository'], row['path'], row['object'])]
            claim = f"{row['slot']} `{row['raw']}` -> {row['resolution']} {row['reason']}".strip()
        text = source(row['repository'], row['commit'], row['path'])[start:end].decode('utf-8', errors='replace')
        lines += [f'## {number}. {stratum}', '', f'`{unit}`', '', f'Oracle claims: {claim}', '', '```proto', text, '```', '']
        labels.append({'unit_id': unit, 'stratum': stratum, 'correct': None, 'rationale': ''})
    return '\n'.join(lines) + '\n', labels


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest='command', required=True)
    s = sub.add_parser('seal')
    s.add_argument('--candidate-export', type=Path, required=True)
    s.add_argument('--activation-at', required=True)
    s.add_argument('--out', type=Path, required=True)
    d = sub.add_parser('seed')
    d.add_argument('--document', type=Path, required=True)
    d.add_argument('--gist-api-url', required=True)
    d.add_argument('--out', type=Path, required=True)
    r = sub.add_parser('draw')
    r.add_argument('--document', type=Path, required=True)
    r.add_argument('--seed-record', type=Path, required=True)
    r.add_argument('--out', type=Path, required=True)
    h = sub.add_parser('sheet')
    h.add_argument('--sample', type=Path, required=True)
    h.add_argument('--corpus', type=Path, required=True)
    h.add_argument('--out-dir', type=Path, required=True)
    args = parser.parse_args()
    repo_root, universe_dir = _HERE.parents[1], _HERE / 'universe'
    if args.command == 'seal':
        document = seal_document(repo_root, universe_dir, args.candidate_export, args.activation_at,
                                 dt.datetime.now(dt.timezone.utc))
        seal.write_once(args.out, seal.canonical_document(document), private=False)
        print(json.dumps({k: document[k] for k in ('activation_at', 'thresholds')}, indent=1))
    elif args.command == 'seed':
        record = seal.seed(args.document.read_bytes(), args.gist_api_url, schema=SCHEMA)
        seal.write_once(args.out, (json.dumps(record, indent=1, sort_keys=True) + '\n').encode(), private=False)
        print('seed_hex', record['seed_hex'])
    elif args.command == 'sheet':
        text, labels = sheet(json.loads(args.sample.read_text()), universe_dir, args.corpus)
        args.out_dir.mkdir(mode=0o700)
        seal.write_once(args.out_dir / 'review.md', text.encode(), private=True)
        seal.write_once(args.out_dir / 'labels.jsonl',
                        b''.join(json.dumps(l, sort_keys=True).encode() + b'\n' for l in labels), private=True)
        print(f'{len(labels)} units')
    else:
        sample = draw(args.document.read_bytes(), json.loads(args.seed_record.read_text()), universe_dir)
        seal.write_once(args.out, (json.dumps(sample, indent=1, sort_keys=True) + '\n').encode(), private=False)
        print(json.dumps({k: len(v) for k, v in sample['samples'].items()}, sort_keys=True))


if __name__ == '__main__':
    main()
