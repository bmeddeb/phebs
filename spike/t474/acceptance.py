"""T47.4 acceptance on the operating class Ben froze on 2026-10-14.

Runs the groundwork lifecycle (cold, warm restart, hard death, interrupted
cold, recovery) and the extended lifecycle (incremental update under
concurrent paging, collection, backup/restore) on the real ordinary binary
against the T47.2 workload, then judges every frozen limit. A limit that fails
is reported as failed; nothing here relaxes it. The released path (T47.5) is
outside this run and named as such.
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'pilot' / 'validation'))
import caller_operating  # noqa: E402

FROZEN = {  # Ben, 2026-10-14: one node on this host, the three-repository T47.2 workload
    'cold_seconds_to_current_max': 300,
    'recovery_seconds_to_current_max': 300,
    'warm_ready_seconds_max': 30,
    'peak_session_rss_bytes_max': int(2.5 * 2**30),
    'data_bytes_max': 3 * 2**30,
    'read_p95_ms_max': 250,
    'read_p99_ms_max': 500,
}


def judge(life: dict, ext: dict) -> dict:
    p = life['phases']
    checks = {
        'cold_seconds_to_current': (p['cold']['seconds_to_current'], FROZEN['cold_seconds_to_current_max']),
        'recovery_seconds_to_current': (p['recovered-after-interruption']['seconds_to_current'], FROZEN['recovery_seconds_to_current_max']),
        'warm_ready_seconds': (p['warm-restart']['ready_seconds'], FROZEN['warm_ready_seconds_max']),
        'peak_session_rss_bytes': (max(v['rss']['peak_bytes'] for v in p.values()), FROZEN['peak_session_rss_bytes_max']),
        'data_bytes': (max(v['data_bytes'] for v in p.values()), FROZEN['data_bytes_max']),
        'read_p95_ms': (p['cold']['caller_map_latency']['p95_ms'], FROZEN['read_p95_ms_max']),
        'read_p99_ms': (p['cold']['caller_map_latency']['p99_ms'], FROZEN['read_p99_ms_max']),
    }
    out = {k: {'measured': v, 'limit': lim, 'pass': v <= lim, 'headroom_x': round(lim / v, 2) if v else None}
           for k, (v, lim) in checks.items()}
    out['generation_unchanged_across_restarts'] = {'pass': life.get('store_local_identity_stable_across_restarts') is True}
    out['content_identical_across_fresh_stores'] = {'pass': life.get('content_identical_across_stores') is True}
    out['http_mcp_parity'] = {'pass': p['cold'].get('mcp_parity_endpoints', 0) > 0, 'endpoints': p['cold'].get('mcp_parity_endpoints')}
    out['paging_during_update'] = {'pass': ext['phases']['update-under-paging']['paging']['errors'] == 0}
    out['restore_identical'] = {'pass': ext['phases']['backup-restore']['identical_caller_content'] is True}
    return out


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for flag in ('--binary', '--helpers', '--corpus', '--run-root'):
        parser.add_argument(flag, type=Path, required=True)
    args = parser.parse_args()
    repos = ['etcd', 'grpc-go', 'containerd']
    life = caller_operating.rehearse(args.binary, args.helpers, args.corpus, args.run_root / 'lifecycle', repos)
    ext = caller_operating.lifecycle_extended(args.binary, args.helpers, args.corpus, args.run_root / 'extended', repos)
    verdict = judge(life, ext)
    result = {'schema': 't47.4-acceptance-v1', 'frozen_class': FROZEN, 'frozen_by': 'Ben, 2026-10-14',
              'released_path': 'not exercised: waits on T47.5', 'binary_sha256': life['binary_sha256'],
              'lifecycle': life, 'extended': ext, 'verdict': verdict,
              'pass': all(v['pass'] for v in verdict.values()),
              'collection_seconds_after_current': ext['phases']['update-under-paging']['collection_seconds_after_current']}
    print(json.dumps(result, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
