#!/usr/bin/env python3
"""Generate a sealed Gate-3 holdout-2 after burning all exposed coordinates.

This script requires a validated v3 Gate-3/4 manifest.  It combines every
coordinate in that benchmark with the committed legacy burn ledger, and burns
line-overlapping occurrences regardless of predicate or assertion value.
"""

from __future__ import annotations

import argparse
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any, Iterable, Mapping, Sequence

from gate34_common import (
    GATE3_MERGE_PREDICATES,
    SCHEMA_VERSION,
    ValidationError,
    allocate_quota,
    assertion_identity,
    atomic_write_jsonl,
    coordinate_burned,
    coordinate_for,
    create_manifest,
    deterministic_sample,
    ensure_writable_targets,
    label_template,
    load_burn_ledger,
    load_corpus_lock,
    load_facts,
    materialize_context,
    producer_summary,
    read_jsonl,
    require_producer_capabilities,
    require_fresh_blind_population,
    seal_site,
    site_key,
    stable_id,
    validate_artifacts,
    validate_manifest,
    validate_precommitted_generator_config,
    validate_single_artifact,
    verify_corpus,
)


SEED = 222
SYSTEMS = ("online-boutique", "dapr", "temporal", "loki", "temporal-helm")
QUOTA = {
    "online-boutique": 12,
    "dapr": 12,
    "temporal": 6,
    "loki": 22,
    "temporal-helm": 3,
}


def parse_args(argv: Iterable[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument("--out-dir", type=Path)
    parser.add_argument("--corpus-dir", type=Path)
    parser.add_argument("--label-dir", type=Path)
    parser.add_argument("--seed", type=int, default=SEED)
    parser.add_argument(
        "--force", action="store_true",
        help="reserved for compatibility; sealed generation always rejects overwrites",
    )
    parser.add_argument(
        "--skip-corpus-check", action="store_true",
        help="reserved for compatibility; sealed generation always verifies the corpus",
    )
    parser.add_argument("--materialize-context", action="store_true")
    parser.add_argument("--context-dir", type=Path)
    return parser.parse_args(argv)


def h2_site(fact: Mapping[str, Any]) -> dict[str, Any]:
    identity = assertion_identity(fact, 3)
    record = {
        "schema_version": SCHEMA_VERSION,
        "site_id": stable_id("g3h2", identity),
        "gate": 3,
        "cohort": "assertion_precision",
        "evaluation": "holdout-2",
        "identity": identity,
        **{field: identity[field] for field in (
            "system", "commit", "path", "start_byte", "end_byte", "start_line",
            "end_line", "predicate", "subject", "object", "tier", "code_role",
        )},
    }
    return record


def prior_coordinates(
    label_dir: Path, base: Path, lock_path: Path,
    locks: Mapping[str, Mapping[str, Any]],
) -> tuple[list[dict[str, Any]], int, str, str]:
    legacy, legacy_digest = load_burn_ledger(base / "labeling" / "burn-ledger.json")
    manifest_path = label_dir / "g34.v3.manifest.json"
    manifest = validate_manifest(
        manifest_path, base=base, mode="g34", lock_path=lock_path, locks=locks,
        artifact_names={"dev_sites", "holdout_sites", "key", "label_template"},
    )
    dev = read_jsonl(label_dir / "g34.v3.sites.dev.jsonl")
    holdout = read_jsonl(label_dir / "g34.v3.sites.holdout.jsonl")
    keys = read_jsonl(label_dir / "g34.v3.key.jsonl")
    validate_artifacts(dev, holdout, keys)
    projected = [*legacy, *(coordinate_for(site) for site in dev + holdout)]
    unique = {
        (row["system"], row["path"], row["start_line"], row["end_line"]): row
        for row in projected
    }
    return (
        [unique[key] for key in sorted(unique)],
        len(legacy),
        legacy_digest,
        str(manifest["manifest_digest"]),
    )


def sample_h2(
    facts_by_system: Mapping[str, Sequence[Mapping[str, Any]]],
    burned: Sequence[Mapping[str, Any]], *, seed: int,
) -> tuple[list[dict[str, Any]], int]:
    result: list[dict[str, Any]] = []
    excluded = 0
    for system in SYSTEMS:
        groups: defaultdict[str, list[dict[str, Any]]] = defaultdict(list)
        seen: set[str] = set()
        for fact in facts_by_system[system]:
            if fact["predicate"] == "EXTRACTION_FAILURE" or fact["tier"] not in {"exact", "derived"}:
                continue
            if fact["predicate"] not in GATE3_MERGE_PREDICATES:
                continue
            site = h2_site(fact)
            if coordinate_burned(site, burned):
                excluded += 1
                continue
            if site["site_id"] in seen:
                continue
            seen.add(site["site_id"])
            groups[str(fact["predicate"])].append(site)
        allocations = allocate_quota(
            {predicate: len(records) for predicate, records in groups.items()}, QUOTA[system]
        )
        for predicate, records in sorted(groups.items()):
            result.extend(
                deterministic_sample(
                    records, allocations[predicate], seed=seed,
                    namespace=f"g3h2:{system}:{predicate}",
                )
            )
    sealed = [seal_site(site) for site in sorted(result, key=lambda row: row["site_id"])]
    return sealed, excluded


def main(argv: Iterable[str] | None = None) -> int:
    args = parse_args(argv)
    base = args.base.resolve()
    out_dir = (args.out_dir or base / "out").resolve()
    corpus_dir = (args.corpus_dir or base / "corpus").resolve()
    label_dir = (args.label_dir or base / "labeling").resolve()
    fixed_paths = {
        "out": (base / "out").resolve(),
        "corpus": (base / "corpus").resolve(),
        "label": (base / "labeling").resolve(),
    }
    supplied_paths = {"out": out_dir, "corpus": corpus_dir, "label": label_dir}
    if supplied_paths != fixed_paths:
        raise ValidationError(
            "sealed holdout-2 generation requires the precommitted base-relative "
            "out, corpus, and labeling directories"
        )
    if args.seed != SEED:
        raise ValidationError(
            f"holdout-2 seed {args.seed} differs from precommitted seed {SEED}"
        )
    if args.skip_corpus_check:
        raise ValidationError("sealed holdout-2 generation cannot skip corpus verification")
    if args.force:
        raise ValidationError("--force is forbidden for sealed holdout-2 generation")
    lock_path = base / "corpus.lock.json"
    ledger_path = base / "labeling" / "burn-ledger.json"
    locks = load_corpus_lock(lock_path)
    corpus_verified = True
    verify_corpus(corpus_dir, locks, SYSTEMS)
    legacy_burn, _ = load_burn_ledger(ledger_path)
    require_fresh_blind_population(len(legacy_burn))

    fact_files: dict[str, Path] = {}
    fact_sets: dict[str, Sequence[Mapping[str, Any]]] = {}
    facts_by_system: dict[str, list[dict[str, Any]]] = {}
    for system in SYSTEMS:
        path = out_dir / f"{system}.identity.jsonl"
        facts = load_facts(
            path, system=system, expected_commit=str(locks[system]["commit"]),
            corpus_dir=corpus_dir,
        )
        fact_files[f"{system}.identity"] = path
        fact_sets[f"{system}.identity"] = facts
        facts_by_system[system] = facts

    # Check the current producer before reading a prior holdout or exposing a
    # new one. An incapable retry gets no opportunity to reuse burned sites.
    producer = producer_summary(fact_sets)
    require_producer_capabilities(producer, "h2")

    burned, legacy_count, legacy_digest, upstream_manifest = prior_coordinates(
        label_dir, base, lock_path, locks
    )
    require_fresh_blind_population(len(burned))
    generator_config = {
        "schema_version": SCHEMA_VERSION,
        "seed": args.seed,
        "systems": list(SYSTEMS),
        "quota": QUOTA,
        "upstream_g34_manifest_digest": upstream_manifest,
        "burn_rule": "line overlap on system,path,start_line,end_line; predicate/value ignored",
    }
    validate_precommitted_generator_config(
        "h2", generator_config, upstream_g34_manifest_digest=upstream_manifest,
    )

    paths = {
        "sites": label_dir / "g3h2.v3.sites.jsonl",
        "key": label_dir / "g3h2.v3.key.jsonl",
        "label_template": label_dir / "g3h2.v3.labels.template.jsonl",
    }
    manifest_path = label_dir / "g3h2.v3.manifest.json"
    context_paths: list[Path] = []
    if args.materialize_context:
        context_dir = (args.context_dir or out_dir / "label-context").resolve()
        context_paths = [context_dir / "g3h2.v3.context.jsonl"]
    ensure_writable_targets(
        [*paths.values(), manifest_path, *context_paths], force=False,
    )

    sites, excluded = sample_h2(facts_by_system, burned, seed=args.seed)
    keys = [site_key(site) for site in sites]
    validate_single_artifact(sites, keys)
    verify_corpus(corpus_dir, locks, SYSTEMS)
    atomic_write_jsonl(paths["sites"], sites)
    atomic_write_jsonl(paths["key"], keys)
    atomic_write_jsonl(paths["label_template"], (label_template(site) for site in sites))
    verify_corpus(corpus_dir, locks, SYSTEMS)

    counts = {
        "sites": len(sites),
        "by_system": {
            system: sum(site["system"] == system for site in sites) for system in SYSTEMS
        },
        "legacy_burn_coordinates": legacy_count,
        "total_burn_coordinates": len(burned),
        "excluded_by_burn": excluded,
    }
    manifest = create_manifest(
        mode="h2", base=base, manifest_path=manifest_path, lock_path=lock_path,
        locks=locks,
        generator_config=generator_config,
        generator_files=(Path(__file__).resolve(), base / "gate34_common.py"),
        fact_files=fact_files, fact_sets=fact_sets, artifact_files=paths,
        artifact_records={"sites": len(sites), "key": len(keys), "label_template": len(sites)},
        counts=counts, burn_ledger_path=ledger_path,
        burn_coordinate_count=legacy_count, burn_coordinates_sha256=legacy_digest,
        corpus_verified=corpus_verified,
    )
    if args.materialize_context:
        materialize_context(
            sites, corpus_dir, context_paths[0], locks
        )

    print(
        f"schema={SCHEMA_VERSION} holdout-2={len(sites)} burn={len(burned)} excluded={excluded}"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValidationError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)
