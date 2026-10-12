"""T49.1b: export phebs's protobuf declaration facts from a run's exact partitioned roots.

Read-only. For each admitted repository the current `proto-contract`
extraction-domain root is read, then every assertion of that root's run with
its Detail (which carries the §6 type-reference states). The export goes to
private candidate custody and only its digest is committed. It is never
compared with the oracle before the protocol is sealed and the oracle sample
is labeled.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 't472'))
import candidate_export  # noqa: E402  (bounded, authenticated store query)
import source_outcomes as outcomes  # noqa: E402

DOMAIN = 'proto-contract'


def export(run_root: Path) -> dict:
    roots = candidate_export.query(run_root, f"SELECT * FROM extraction_domain_root WHERE domain = '{DOMAIN}' LIMIT 16;")
    outcomes.require(0 < len(roots) < 16, 'unexpected proto-contract root census')
    facts = []
    for root in sorted(roots, key=lambda r: r['repository']):
        limit = max(root['facts'], root['rows']) + 1
        rows = candidate_export.query(run_root, 'SELECT predicate, subject, object, lineage, tier, code_role, detail, '
                                      'repo, run_id, assertion_id FROM assertion WHERE run_id = '
                                      + json.dumps(root['run_id']) + f' LIMIT {limit};')
        outcomes.require(len(rows) < limit, 'root assertion census overflow')
        outcomes.require(all(r['repo'] == root['repository'] for r in rows), 'assertion escaped its root')
        facts += rows
    controls = [{k: r[k] for k in ('repository', 'run_id', 'plan_digest', 'root_digest', 'facts', 'rows')} for r in roots]
    return {'schema': 't491-candidate-export-v1', 'domain': DOMAIN, 'roots': controls,
            'facts': sorted(facts, key=lambda f: (f['repo'], f['subject'], f['predicate'], f['object']))}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--run-root', type=Path, required=True)
    parser.add_argument('--private-out', type=Path, required=True)
    args = parser.parse_args()
    first = export(args.run_root)
    again = export(args.run_root)
    outcomes.require(first == again, 'candidate roots changed during export')
    raw = (json.dumps(first, sort_keys=True, separators=(',', ':')) + '\n').encode()
    args.private_out.mkdir(mode=0o700)
    path = args.private_out / 'candidate-export.json'
    with open(path, 'xb') as f:
        f.write(raw)
    os.chmod(path, 0o600)
    print(json.dumps({'roots': [{k: r[k] for k in ('repository', 'root_digest', 'facts')} for r in first['roots']],
                      'export_sha256': 'sha256:' + hashlib.sha256(raw).hexdigest()}, indent=1))


if __name__ == '__main__':
    main()
