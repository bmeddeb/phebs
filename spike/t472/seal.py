"""T47.2q: seal, public-randomness seed and draw for one T47.2b round.

Follows the retained t111 discipline with the frozen label_protocol helpers:
Ben publishes the canonical seal document as a single-file GitHub Gist whose
initial revision predates the declared activation time. The seed then comes
from the first 60-second NIST Beacon 2.0 pulse strictly after that minute,
with no latest-pulse or retry fallback. Nothing here fabricates a seal, pulse,
gist or label: every external value is fetched and verified.

  seal.py seal     fill the protocol digest; write the sealed bundle and seal document
  seal.py seed     verify the gist, fetch the pulse, write the seed record
  seal.py draw     draw the planned samples from digest-bound populations
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import sys
from pathlib import Path

_SPIKE = Path(__file__).resolve().parent
for _extra in ('../t111', '../../pilot/validation'):
    p = str((_SPIKE / _extra).resolve())
    if p not in sys.path:
        sys.path.insert(0, p)

import label_protocol as protocol  # noqa: E402

import binding_bundle  # noqa: E402
import frames  # noqa: E402

SEAL_SCHEMA = 't472-seal-commitment-v1'
SEED_SCHEMA = 't472-seed-record-v1'
SAMPLE_SCHEMA = 't472-planned-sample-v1'
PROTOCOL_SLOT = 'protocol digest after the section 10 sealing checklist completes'
NIST_ROOT = 'https://beacon.nist.gov/beacon/2.0'
_CANONICAL_PULSE = re.compile(r'https://beacon\.nist\.gov/beacon/2\.0/chain/[0-9]+/pulse/[0-9]+')


class SealError(ValueError):
    """A seal, seed or draw input is inconsistent; the round stops."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SealError(message)


def sha(raw: bytes) -> str:
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def canonical_document(value: dict) -> bytes:
    return (json.dumps(value, indent=2, sort_keys=True) + '\n').encode()


def utc(value: str) -> dt.datetime:
    require(isinstance(value, str) and value.endswith('Z'), 'times must be RFC3339 UTC ending in Z')
    parsed = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    require(parsed.tzinfo is not None, 'time has no zone')
    return parsed


def seal(repo_root: Path, bundle_in: Path, bundle_out: Path, document_out: Path,
         activation_at: str, now: dt.datetime) -> dict:
    """Bind the protocol digest into the round bundle; write the document Ben publishes."""
    activation = utc(activation_at)
    require(activation > now + dt.timedelta(minutes=5), 'activation must be at least five minutes ahead')
    bundle = json.loads(bundle_in.read_text())
    binding_bundle.validate_bundle(bundle)
    require(bundle['candidate_identities'][PROTOCOL_SLOT] is None, 'protocol digest is already sealed')
    protocol_sha = sha((repo_root / bundle['protocol_doc']['path']).read_bytes())
    require(protocol_sha == bundle['protocol_doc']['sha256'], 'protocol changed since the bundle was built')
    plan_path = repo_root / bundle['frames_plan']['path']
    require(sha(plan_path.read_bytes()) == bundle['frames_plan']['sha256'], 'frames plan changed since the bundle was built')
    bundle['candidate_identities'][PROTOCOL_SLOT] = {'identity': 'docs/CALLER_QUALITY_PROTOCOL.md@sealed',
                                                     'sha256': protocol_sha}
    bundle.pop('missing_bindings')
    bundle['missing_bindings'] = binding_bundle.validate_bundle(bundle)
    sealed_raw = (json.dumps(bundle, indent=1, sort_keys=True) + '\n').encode()
    bundle_out.write_bytes(sealed_raw)
    plan = json.loads(plan_path.read_text())
    document = {
        'schema': SEAL_SCHEMA, 'sealed_by': 'Ben Meddeb', 'activation_at': activation_at,
        'protocol': {'path': bundle['protocol_doc']['path'], 'sha256': protocol_sha},
        'sealed_bundle': {'path': str(bundle_out.relative_to(repo_root)), 'sha256': sha(sealed_raw)},
        'frames_plan': {'path': bundle['frames_plan']['path'], 'sha256': bundle['frames_plan']['sha256']},
        'planned_samples': {f['frame_id']: sum(s['sample_size'] for s in f['strata'].values())
                            for f in plan['frames'] if 'strata' in f},
        'dropped_frames': sorted(f['frame_id'] for f in plan['frames'] if 'dropped' in f),
        'randomness_rule': 'first NIST Beacon 2.0 pulse strictly after the activation minute; no fallback',
    }
    document_out.write_bytes(canonical_document(document))
    return document


def first_pulse_time(activation_at: str) -> dt.datetime:
    return utc(activation_at).replace(second=0, microsecond=0) + dt.timedelta(minutes=1)


def seed(document_raw: bytes, gist_api_url: str, gist_fetcher=protocol.github_json_fetcher,
         nist_fetcher=protocol.nist_beacon_json_fetcher) -> dict:
    document = json.loads(document_raw)
    require(document_raw == canonical_document(document) and document.get('schema') == SEAL_SCHEMA,
            'seal document is not the canonical t472 seal document')
    receipt = protocol.build_github_initial_gist_receipt(gist_api_url, document_raw, gist_fetcher)
    require(utc(receipt['created_at']) < utc(document['activation_at']),
            'gist initial revision was not published before activation')
    pulse_ms = int(first_pulse_time(document['activation_at']).timestamp() * 1000)
    by_time = nist_fetcher(f'{NIST_ROOT}/pulse/time/{pulse_ms}')
    require(isinstance(by_time, dict) and set(by_time) == {'pulse'} and isinstance(by_time['pulse'], dict),
            'NIST time lookup must return exactly one pulse')
    pulse = by_time['pulse']
    uri = pulse.get('uri')
    require(isinstance(uri, str) and bool(_CANONICAL_PULSE.fullmatch(uri)), 'NIST pulse has no canonical URI')
    require(nist_fetcher(uri) == by_time, 'NIST time lookup and canonical pulse disagree')
    require(pulse.get('timeStamp') is not None and utc(pulse['timeStamp']) == first_pulse_time(document['activation_at']),
            'NIST pulse is not the first eligible pulse')
    output = pulse.get('outputValue')
    require(isinstance(output, str) and bool(re.fullmatch(r'[0-9A-Fa-f]{128}', output)), 'NIST pulse outputValue is invalid')
    payload_sha = sha(json.dumps(by_time, sort_keys=True, separators=(',', ':')).encode())
    parts = ['t472-caller-quality-seed-v1', sha(document_raw), document['activation_at'], receipt['api_url'],
             receipt['revision'], receipt['created_at'], uri, output.lower(), payload_sha, pulse['timeStamp']]
    return {'schema': SEED_SCHEMA, 'seal_document_sha256': sha(document_raw), 'gist_receipt': receipt,
            'pulse_uri': uri, 'pulse_timestamp': pulse['timeStamp'], 'output_value': output.lower(),
            'pulse_payload_sha256': payload_sha, 'derivation': parts[0],
            'seed_hex': hashlib.sha256(json.dumps(parts, separators=(',', ':')).encode()).hexdigest()}


def load_populations(plan: dict, public_dir: Path, private_dir: Path) -> dict:
    populations = {}
    for frame in plan['frames']:
        if 'strata' not in frame:
            continue
        directory = private_dir if frame['file'] in binding_bundle.ROUND_CANDIDATE_MEMBERS else public_dir
        path = directory / frame['file']
        require(frames.sha256_file(path) == frame['sha256'], f"{frame['file']} differs from the sealed plan")
        populations[frame['frame_id']] = frames.load_jsonl(path)
    return populations


def draw(plan_raw: bytes, seed_record: dict, document_raw: bytes, public_dir: Path, private_dir: Path) -> dict:
    document = json.loads(document_raw)
    require(seed_record['seal_document_sha256'] == sha(document_raw), 'seed belongs to another seal document')
    require(sha(plan_raw) == document['frames_plan']['sha256'], 'frames plan differs from the sealed plan')
    plan = json.loads(plan_raw)
    populations = load_populations(plan, public_dir, private_dir)
    drawn = frames.draw_samples(plan, seed_record['seed_hex'], populations)
    frames.quality_samples(plan, drawn, populations)
    counts = {name: sum(map(len, strata.values())) for name, strata in drawn.items()}
    require(counts == document['planned_samples'], 'draw does not equal the sealed planned samples')
    return {'schema': SAMPLE_SCHEMA, 'seal_document_sha256': sha(document_raw),
            'seed_hex': seed_record['seed_hex'], 'counts': counts, 'samples': drawn}


def write_once(path: Path, raw: bytes, private: bool) -> None:
    with open(path, 'xb') as f:
        f.write(raw)
    if private:
        os.chmod(path, 0o600)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest='command', required=True)
    s = sub.add_parser('seal')
    s.add_argument('--bundle-in', type=Path, required=True)
    s.add_argument('--bundle-out', type=Path, required=True)
    s.add_argument('--document-out', type=Path, required=True)
    s.add_argument('--activation-at', required=True)
    d = sub.add_parser('seed')
    d.add_argument('--document', type=Path, required=True)
    d.add_argument('--gist-api-url', required=True)
    d.add_argument('--out', type=Path, required=True)
    r = sub.add_parser('draw')
    r.add_argument('--document', type=Path, required=True)
    r.add_argument('--seed-record', type=Path, required=True)
    r.add_argument('--population-dir', type=Path, required=True)
    r.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    repo_root = _SPIKE.parents[1]
    if args.command == 'seal':
        document = seal(repo_root, args.bundle_in.resolve(), args.bundle_out.resolve(), args.document_out,
                        args.activation_at, dt.datetime.now(dt.timezone.utc))
        print(json.dumps(document, indent=1, sort_keys=True))
    elif args.command == 'seed':
        record = seed(args.document.read_bytes(), args.gist_api_url)
        write_once(args.out, (json.dumps(record, indent=1, sort_keys=True) + '\n').encode(), private=False)
        print('seed_hex', record['seed_hex'])
    else:
        document_raw = args.document.read_bytes()
        plan_raw = (repo_root / json.loads(document_raw)['frames_plan']['path']).read_bytes()
        sample = draw(plan_raw, json.loads(args.seed_record.read_text()), document_raw,
                      _SPIKE / 'frames', args.population_dir)
        write_once(args.out, (json.dumps(sample, indent=1, sort_keys=True) + '\n').encode(), private=True)
        print(json.dumps(sample['counts'], sort_keys=True))


if __name__ == '__main__':
    main()
