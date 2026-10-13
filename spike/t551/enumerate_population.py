"""T55.1: the real-history Kafka producer population (no phebs code or output).

One unit is one distinct version (git blob) of a non-test, non-vendored `.go`
file that imports a round-one library (sarama under either import path, or
segmentio/kafka-go). It is collected from the first-parent history of each
locked production repository up to its pinned commit. Identical content seen
again later in the history is the same unit (first occurrence kept), so
unchanged files are not double-counted. The oracle and the extractor each read
the unit's exact blob.
"""
from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import subprocess
from pathlib import Path

SCHEMA = 't551-producer-population-v1'
IMPORTS = (b'"github.com/Shopify/sarama"', b'"github.com/IBM/sarama"', b'"github.com/segmentio/kafka-go"')


def eligible_path(path: str) -> bool:
    return (path.endswith('.go') and not path.endswith('_test.go') and not path.startswith(('vendor/', 'third_party/'))
            and '/vendor/' not in path)


def blobs(repo: Path, commit: str) -> list[tuple[str, str, str]]:
    """(commit, path, blob) for every added or modified eligible file, oldest first."""
    log = subprocess.run(['git', '-C', str(repo), 'log', '--first-parent', '--reverse', '--raw', '--no-renames',
                          '--no-abbrev', '--format=commit %H', commit, '--', '*.go'],
                         check=True, capture_output=True, text=True).stdout
    out, current = [], None
    for line in log.splitlines():
        if line.startswith('commit '):
            current = line.split()[1]
        elif line.startswith(':'):
            meta, path = line.split('\t', 1)
            fields = meta.split()
            blob, status = fields[3], fields[4]
            if status in ('A', 'M') and eligible_path(path):
                out.append((current, path, blob))
    return out


def contents(repo: Path, ids: list[str]) -> dict[str, bytes]:
    batch = subprocess.run(['git', '-C', str(repo), 'cat-file', '--batch'], input=''.join(i + '\n' for i in ids).encode(),
                           check=True, capture_output=True).stdout
    found, at = {}, 0
    for blob in ids:
        header_end = batch.index(b'\n', at)
        oid, _, size = batch[at:header_end].split()
        start = header_end + 1
        found[blob] = batch[start:start + int(size)]
        at = start + int(size) + 1
    return found


def build(history: Path, lock: dict) -> list[dict]:
    units = []
    for spec in lock['repos']:
        repo = history / spec['dir']
        seen: set[str] = set()
        candidates = [(c, p, b) for c, p, b in blobs(repo, spec['commit']) if not (b in seen or seen.add(b))]
        data = contents(repo, [b for _, _, b in candidates])
        for commit, path, blob in candidates:
            content = data[blob]
            if any(i in content for i in IMPORTS):
                units.append({'unit_id': f"{spec['name']}@{blob}", 'repository': spec['name'], 'dir': spec['dir'],
                              'commit': commit, 'path': path, 'blob': blob,
                              'sha256': 'sha256:' + hashlib.sha256(content).hexdigest()})
    units.sort(key=lambda u: u['unit_id'])
    return units


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--history', type=Path, required=True)
    parser.add_argument('--lock', type=Path, default=Path(__file__).with_name('corpus.lock.json'))
    parser.add_argument('--out-dir', type=Path, required=True)
    args = parser.parse_args()
    units = build(args.history, json.loads(args.lock.read_text()))
    args.out_dir.mkdir(parents=True, exist_ok=True)
    body = ''.join(json.dumps(u, sort_keys=True, separators=(',', ':')) + '\n' for u in units).encode()
    (args.out_dir / 'units.jsonl.gz').write_bytes(gzip.compress(body, mtime=0))
    by_repo: dict[str, int] = {}
    for u in units:
        by_repo[u['repository']] = by_repo.get(u['repository'], 0) + 1
    summary = {'schema': SCHEMA, 'total': len(units), 'by_repository': dict(sorted(by_repo.items())),
               'units_sha256': 'sha256:' + hashlib.sha256(body).hexdigest(),
               'enumerator_sha256': 'sha256:' + hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (args.out_dir / 'population.json').write_text(json.dumps(summary, indent=1, sort_keys=True) + '\n')
    print(json.dumps(summary, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
