"""T47.2r: bind a completed seal ceremony (pulse, seed, draw) into the round bundle.

Fills only the seed slot, randomness steps 1-3 and the beacon-reference fields
from the verified seed record and the private sample; labels, ledgers and the
label commitment stay explicit missing inputs.
"""
from __future__ import annotations

import argparse
import copy
import json
from pathlib import Path

import binding_bundle
import seal

SEED_SLOT = 'NIST beacon pulse and derived 64-hex seed for the sample draw'


def record(sealed: dict, document_raw: bytes, seed_raw: bytes, sample_raw: bytes) -> dict:
    seed_record, sample = json.loads(seed_raw), json.loads(sample_raw)
    document = json.loads(document_raw)
    seal.require(seed_record['seal_document_sha256'] == seal.sha(document_raw)
                 and sample['seal_document_sha256'] == seal.sha(document_raw)
                 and sample['seed_hex'] == seed_record['seed_hex'], 'ceremony artifacts belong to different seals')
    seal.require(document['sealed_bundle']['sha256'] == seal.sha(
        (json.dumps(sealed, indent=1, sort_keys=True) + '\n').encode()), 'bundle is not the sealed bundle')
    seal.require(sample['counts'] == document['planned_samples'], 'sample does not complete the sealed plan')
    bundle = copy.deepcopy(sealed)
    seal.require(bundle['candidate_identities'][SEED_SLOT] is None, 'seed is already bound')
    bundle['candidate_identities'][SEED_SLOT] = {'identity': seed_record['pulse_uri'], 'sha256': seal.sha(seed_raw)}
    steps = bundle['randomness_commitment_sequence']
    for index, digest in enumerate((seed_record['pulse_payload_sha256'], seal.sha(seed_raw), seal.sha(sample_raw))):
        seal.require(steps[index]['binding'] is None, 'randomness step is already bound')
        steps[index]['binding'] = digest
    bundle['external_publication']['prepared_payloads'][1]['fields'] = {
        'pulse': seed_record['pulse_uri'], 'output_value': seed_record['output_value'],
        'pulse_timestamp': seed_record['pulse_timestamp']}
    bundle.pop('missing_bindings')
    bundle['missing_bindings'] = binding_bundle.validate_bundle(bundle)
    return bundle


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for name in ('sealed-bundle', 'document', 'seed-record', 'sample', 'out'):
        parser.add_argument('--' + name, type=Path, required=True)
    args = parser.parse_args()
    bundle = record(json.loads(args.sealed_bundle.read_text()), args.document.read_bytes(),
                    args.seed_record.read_bytes(), args.sample.read_bytes())
    with open(args.out, 'x') as f:
        f.write(json.dumps(bundle, indent=1, sort_keys=True) + '\n')
    print(f'missing={len(bundle["missing_bindings"])}')


if __name__ == '__main__':
    main()
