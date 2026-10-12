"""T47.2p: the round frames plan (protocol section 10.2) for one candidate round.

Fills the template plan's pending precision, abstention and unresolved frames
with exact digest-bound populations, stratum counts, sample sizes and census
flags taken from the bound candidate custody (T47.2o). Populations stay
private; only counts and digests are committed. No seed, draw, label or score
is produced, and frames.py and the template plan stay byte-exact.
"""
from __future__ import annotations

import argparse
import copy
import gzip
import hashlib
import json
import os
from pathlib import Path

import binding_bundle
import caller_map_export
import frames

POPULATION_FILES = {'precision': 'frames.precision.jsonl', 'abstention': 'frames.abstention.jsonl.gz',
                    'unresolved': 'frames.unresolved.jsonl'}
BOUND_SLOTS = (caller_map_export.PROOF_SLOT, caller_map_export.LEDGER_SLOT)


class RoundFramesError(ValueError):
    """A candidate population disagrees with its bound custody or the universe."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RoundFramesError(message)


def sha(raw: bytes) -> str:
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def encode_population(name: str, rows: list[dict]) -> bytes:
    body = b''.join(json.dumps(r, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode() + b'\n'
                    for r in rows)
    return gzip.compress(body, mtime=0) if name.endswith('.gz') else body


def candidate_populations(candidate_frames: dict, ledger: list[dict], recall: dict) -> dict[str, list[dict]]:
    """Site coordinates only: membership is the candidate signal, so no prediction field is copied."""
    def coordinate(site_id: str, stratum: str) -> dict:
        site = recall.get(site_id)
        require(site is not None and site['stratum'] == stratum, 'candidate site is outside the recall universe')
        return {k: site[k] for k in ('site_id', 'stratum', 'repository', 'path', 'start_byte', 'end_byte')}
    populations = {name: [coordinate(r['site_id'], r['stratum']) for r in candidate_frames[name]['rows']]
                   for name in ('precision', 'unresolved')}
    states = {}
    for row in ledger:
        require(row['site_id'] not in states, 'duplicate ledger site')
        states[row['site_id']] = row['state']
    require(set(states) == set(recall), 'ledger does not cover the recall population exactly')
    reported = {r['site_id'] for name in ('precision', 'unresolved') for r in populations[name]}
    require(reported == {s for s, state in states.items() if state == 'reported'}, 'reported ledger rows differ from candidate frames')
    populations['abstention'] = [coordinate(row['site_id'], row['stratum']) for row in ledger if row['state'] == 'withheld']
    for rows in populations.values():
        rows.sort(key=lambda r: r['site_id'])
        require(len({r['site_id'] for r in rows}) == len(rows), 'candidate frame has duplicate sites')
    return populations


def round_plan(template: dict, populations: dict, sites_per_stratum: int, round_block: dict,
               digests: dict, filled: dict) -> dict:
    plan = copy.deepcopy(template)
    for frame in plan['frames']:
        name = frame['frame_id']
        if name not in POPULATION_FILES:
            continue
        require(frame.get('pending') is True, 'template candidate frame is not pending')
        frame.pop('pending')
        strata = frames.stratify_population(populations[name])
        frame.update(strata={s: frames.frame_strata_sizes({s: ids}, sites_per_stratum)[s] for s, ids in strata.items()},
                     population=len(populations[name]), file=POPULATION_FILES[name],
                     rows=len(populations[name]), sha256=digests[POPULATION_FILES[name]])
    plan['round'] = round_block
    plan['missing_bindings'] = [m for m in template['missing_bindings'] if filled.get(m) is None]
    return plan


def run(repo_root: Path, candidate_root: Path, private_out: Path, plan_out: Path,
        bundle_in: Path, bundle_out: Path) -> dict:
    spike = repo_root / 'spike' / 't472'
    template_path = spike / 'frames' / 'frames.plan.json'
    template = json.loads(template_path.read_text())
    receipt_path = spike / 'caller_map_bindings_20261011g.json'
    receipt = json.loads(receipt_path.read_text())
    bundle = json.loads(bundle_in.read_text())
    parameters = json.loads((spike / 'validation.parameters.json').read_text())
    sites_per_stratum = int(parameters['sampling']['sites_per_frame_stratum'])
    for name in ('candidate-frames.json', 'call-site-decisions.jsonl'):
        require(sha((candidate_root / name).read_bytes()) == receipt['artifacts_sha256'][name],
                f'{name} differs from its bound digest')
    for slot, name in zip(BOUND_SLOTS, ('caller-map-export.json', 'call-site-decisions.jsonl')):
        require(bundle['candidate_identities'][slot]['sha256'] == receipt['artifacts_sha256'][name],
                'input bundle is not the bound candidate bundle')
    recall_frame = next(f for f in template['frames'] if f['frame_id'] == 'recall')
    recall_path = spike / 'frames' / recall_frame['file']
    require(frames.sha256_file(recall_path) == recall_frame['sha256'], 'recall frame digest mismatch')
    recall = {r['site_id']: r for r in frames.load_jsonl(recall_path)}
    candidate = json.loads((candidate_root / 'candidate-frames.json').read_text())
    ledger = [json.loads(line) for line in (candidate_root / 'call-site-decisions.jsonl').read_text().splitlines()]
    populations = candidate_populations(candidate, ledger, recall)

    private_out.mkdir(mode=0o700)
    digests = {}
    for name, file in POPULATION_FILES.items():
        raw = encode_population(file, populations[name])
        with open(private_out / file, 'xb') as f:
            f.write(raw)
        os.chmod(private_out / file, 0o600)
        digests[file] = sha(raw)
    round_block = {'name': receipt['round'], 'population_root': str(private_out),
                   'candidate_receipt_sha256': sha(receipt_path.read_bytes()),
                   'candidate_bundle_sha256': sha(bundle_in.read_bytes()),
                   'template_plan_sha256': sha(template_path.read_bytes()),
                   'round_frames_py_sha256': sha((spike / 'round_frames.py').read_bytes()),
                   'population_rule': 'site coordinates only; membership is the candidate signal and stays private'}
    plan = round_plan(template, populations, sites_per_stratum, round_block, digests, bundle['candidate_identities'])
    plan_raw = (json.dumps(plan, indent=1, sort_keys=True) + '\n').encode()
    plan_out.write_bytes(plan_raw)
    (private_out / plan_out.name).write_bytes(plan_raw)
    os.chmod(private_out / plan_out.name, 0o600)
    loaded = frames.load_frame_populations({'frames': [f for f in plan['frames'] if f['frame_id'] in POPULATION_FILES]}, private_out)
    for frame in plan['frames']:
        if frame['frame_id'] in POPULATION_FILES:
            frames._planned_strata(frame, loaded)

    members = dict(bundle['frames_plan']['members'])
    members.update({file: {'sha256': digests[file], 'rows': len(populations[name])} for name, file in POPULATION_FILES.items()})
    bundle['frames_plan'] = {'path': str(plan_out.relative_to(repo_root)), 'sha256': sha(plan_raw),
                             'frames': bundle['frames_plan']['frames'], 'members': members}
    bundle['machinery']['binding_bundle']['sha256'] = sha((spike / 'binding_bundle.py').read_bytes())
    bundle.pop('missing_bindings')
    bundle['missing_bindings'] = binding_bundle.validate_bundle(bundle)
    bundle_out.write_text(json.dumps(bundle, indent=1, sort_keys=True) + '\n')
    return plan


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--repo-root', type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument('--candidate-root', type=Path, required=True)
    parser.add_argument('--private-out', type=Path, required=True)
    parser.add_argument('--plan-out', type=Path, required=True)
    parser.add_argument('--bundle-in', type=Path, required=True)
    parser.add_argument('--bundle-out', type=Path, required=True)
    args = parser.parse_args()
    root = args.repo_root.resolve()
    plan = run(root, args.candidate_root, args.private_out, args.plan_out.resolve(), args.bundle_in, args.bundle_out)
    for frame in plan['frames']:
        sizes = frame['strata']
        print(f"{frame['frame_id']}: population={frame['population']} strata={len(sizes)} "
              f"planned_sample={sum(s['sample_size'] for s in sizes.values())}")


if __name__ == '__main__':
    main()
