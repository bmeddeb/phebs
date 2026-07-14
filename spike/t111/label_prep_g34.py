#!/usr/bin/env python3
"""Generate the sealed v3 Gate 3/4 benchmark without touching legacy labels.

The candidate population and all context are read from pinned Git objects.
Legacy dev/holdout coordinates are excluded through the committed burn ledger.
The resulting manifest binds generator configuration, corpus commits, producer
provenance, fact files, artifacts, and all population/sample counts.

The generator fails before writing any artifact unless the producer already has
the canonical-field and Gate-3 pairwise capabilities required by the sealed
benchmark.
"""

from __future__ import annotations

import argparse
import bisect
import hashlib
import re
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any, Iterable, Mapping, Sequence

from gate34_common import (
    GATE3_MERGE_PREDICATES,
    SCHEMA_VERSION,
    HashReservoir,
    ValidationError,
    allocate_quota,
    assertion_identity,
    atomic_write_jsonl,
    candidate_identity,
    canonical_json,
    coordinate_burned,
    create_manifest,
    deterministic_sample,
    ensure_writable_targets,
    git_blob,
    label_template,
    load_burn_ledger,
    load_corpus_lock,
    load_facts,
    materialize_context,
    parse_detail,
    producer_summary,
    require_producer_capabilities,
    require_fresh_blind_population,
    seal_site,
    site_key,
    source_blobs,
    split_sites,
    stable_id,
    summarize_sites,
    validate_artifacts,
    validate_precommitted_generator_config,
    verify_corpus,
)


SEED = 111
HOLDOUT_FRACTION = 0.30
ID_SYSTEMS = ("online-boutique", "dapr", "temporal", "loki", "temporal-helm")
FIELD_SYSTEMS = ("online-boutique", "dapr", "temporal", "loki")
ID_QUOTA = {
    "online-boutique": 40,
    "dapr": 35,
    "temporal": 12,
    "loki": 45,
    "temporal-helm": 4,
}
FIELD_PRECISION_QUOTA = {
    "online-boutique": 25,
    "dapr": 30,
    "temporal": 30,
    "loki": 25,
}

# These are real consumer references to the same canonical descriptor field,
# across two protobuf implementations.  The Loki anchor is intentionally in
# Prometheus consumer code, not generated Timestamp self-code.
LINEAGE_ANCHORS = (
    {
        "case_id": "google-protobuf-timestamp-seconds",
        "system": "temporal",
        "path": "chasm/lib/scheduler/util.go",
        "start_line": 78,
        "shape": "getter_call",
        "token": "GetSeconds",
        "implementation_module": "google.golang.org/protobuf",
        "version_file": "go.mod",
    },
    {
        "case_id": "google-protobuf-timestamp-seconds",
        "system": "loki",
        "path": "vendor/github.com/prometheus/prometheus/model/textparse/protobufparse.go",
        "start_line": 390,
        "shape": "getter_call",
        "token": "GetSeconds",
        "implementation_module": "github.com/gogo/protobuf",
        "version_file": "vendor/modules.txt",
    },
)

PROTO_FIELD_RE = re.compile(
    rb"(?m)^\s*([A-Z][A-Za-z0-9_]*)\s+[^\n`]+`protobuf:\"[^\"]+\""
)
GETTER_RE = re.compile(rb"\.\s*(Get[A-Z][A-Za-z0-9_]*)\s*\(\s*\)")
SELECTOR_RE = re.compile(rb"\.\s*([A-Z][A-Za-z0-9_]*)")
COMPOSITE_KEY_RE = re.compile(rb"\b([A-Z][A-Za-z0-9_]*)\s*:")


def parse_args(argv: Iterable[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument("--out-dir", type=Path)
    parser.add_argument("--corpus-dir", type=Path)
    parser.add_argument("--label-dir", type=Path)
    parser.add_argument("--seed", type=int, default=SEED)
    parser.add_argument("--holdout-fraction", type=float, default=HOLDOUT_FRACTION)
    parser.add_argument("--candidate-hit-quota", type=int, default=35)
    parser.add_argument("--candidate-open-quota", type=int, default=8)
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


def assertion_site(fact: Mapping[str, Any], gate: int) -> dict[str, Any]:
    identity = assertion_identity(fact, gate)
    site_id = stable_id("g3" if gate == 3 else "g4", identity)
    record: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "site_id": site_id,
        "gate": gate,
        "cohort": "assertion_precision",
        "identity": identity,
        **{field: identity[field] for field in (
            "system", "commit", "path", "start_byte", "end_byte", "start_line",
            "end_line", "predicate", "subject", "object", "tier", "code_role",
        )},
    }
    if gate == 4:
        record["reported_access"] = parse_detail(fact.get("detail", "")).get(
            "access", "unknown"
        )
    return record


def unique_sites(records: Iterable[dict[str, Any]]) -> list[dict[str, Any]]:
    seen: dict[str, str] = {}
    result: list[dict[str, Any]] = []
    for record in records:
        encoded = canonical_json(record["identity"])
        previous = seen.get(record["site_id"])
        if previous is not None:
            if previous != encoded:
                raise ValidationError(f"stable-ID collision for {record['site_id']}")
            continue
        seen[record["site_id"]] = encoded
        result.append(record)
    return result


def sample_assertions(
    grouped: Mapping[str, list[dict[str, Any]]], quota: int, *, seed: int, namespace: str
) -> list[dict[str, Any]]:
    allocations = allocate_quota(
        {stratum: len(records) for stratum, records in grouped.items()}, quota
    )
    selected: list[dict[str, Any]] = []
    for stratum, records in sorted(grouped.items()):
        selected.extend(
            deterministic_sample(
                records, allocations[stratum], seed=seed, namespace=f"{namespace}:{stratum}"
            )
        )
    return selected


def gate3_sites(
    facts_by_system: Mapping[str, Sequence[Mapping[str, Any]]],
    burned: Sequence[Mapping[str, Any]], *, seed: int,
) -> tuple[list[dict[str, Any]], dict[str, int]]:
    selected: list[dict[str, Any]] = []
    excluded: defaultdict[str, int] = defaultdict(int)
    for system in ID_SYSTEMS:
        grouped: defaultdict[str, list[dict[str, Any]]] = defaultdict(list)
        for fact in facts_by_system[system]:
            if fact["predicate"] == "EXTRACTION_FAILURE":
                excluded["extraction_failure"] += 1
                continue
            if fact["tier"] == "unresolved":
                excluded["abstained"] += 1
                continue
            if fact["tier"] == "heuristic":
                excluded["heuristic"] += 1
                continue
            if fact["predicate"] not in GATE3_MERGE_PREDICATES:
                excluded["non_merge_declaration"] += 1
                continue
            site = assertion_site(fact, 3)
            if coordinate_burned(site, burned):
                excluded["legacy_burn"] += 1
                continue
            site["split_stratum"] = f"g3:{system}:{fact['predicate']}"
            grouped[str(fact["predicate"])].append(site)
        normalized = {
            predicate: unique_sites(records) for predicate, records in grouped.items()
        }
        selected.extend(
            sample_assertions(
                normalized, ID_QUOTA[system], seed=seed, namespace=f"g3:{system}"
            )
        )
    return selected, dict(excluded)


def gate4_precision_sites(
    facts_by_system: Mapping[str, Sequence[Mapping[str, Any]]],
    burned: Sequence[Mapping[str, Any]], *, seed: int,
) -> tuple[list[dict[str, Any]], int]:
    selected: list[dict[str, Any]] = []
    burned_count = 0
    for system in FIELD_SYSTEMS:
        grouped: defaultdict[str, list[dict[str, Any]]] = defaultdict(list)
        for fact in facts_by_system[system]:
            if fact["predicate"] != "REFERENCES_PROTO_FIELD":
                continue
            site = assertion_site(fact, 4)
            if coordinate_burned(site, burned):
                burned_count += 1
                continue
            access = str(site["reported_access"])
            site["split_stratum"] = f"g4p:{system}:{access}"
            grouped[access].append(site)
        selected.extend(
            sample_assertions(
                {access: unique_sites(records) for access, records in grouped.items()},
                FIELD_PRECISION_QUOTA[system], seed=seed, namespace=f"g4p:{system}",
            )
        )
    return selected, burned_count


def generated_field_vocabulary(
    corpus_dir: Path, locks: Mapping[str, Mapping[str, Any]]
) -> set[str]:
    names: set[str] = set()
    for system in FIELD_SYSTEMS:
        root, commit = corpus_dir / system, str(locks[system]["commit"])
        for relative, data in source_blobs(
            root, commit, excluded_gitlinks=locks[system].get("excluded_gitlinks")
        ):
            if not relative.endswith(".pb.go"):
                continue
            for match in PROTO_FIELD_RE.finditer(data):
                names.add(match.group(1).decode("ascii"))
    if not names:
        raise ValidationError("generated protobuf field vocabulary is empty")
    return names


def role_hint(relative: str, data: bytes) -> str:
    lowered = relative.lower()
    if lowered.startswith("vendor/") or "/vendor/" in lowered:
        return "vendor"
    if relative.endswith(".pb.go") or b"Code generated" in data[:2048]:
        return "generated"
    if relative.endswith("_test.go") or "/test/" in lowered or "/tests/" in lowered:
        return "test"
    if "mock" in Path(relative).name.lower() or "/mock/" in lowered:
        return "mock"
    return "production"


def line_for(offsets: list[int], byte_offset: int) -> int:
    return bisect.bisect_right(offsets, byte_offset)


def candidates_in_file(
    system: str, commit: str, relative: str, data: bytes
) -> Iterable[dict[str, Any]]:
    digest = hashlib.sha256(data).hexdigest()
    newlines = [-1, *(index for index, value in enumerate(data) if value == 10)]
    emitted: set[tuple[int, int, str]] = set()

    def candidate(match: re.Match[bytes], shape: str) -> dict[str, Any]:
        start, end = match.span(1)
        return {
            "system": system,
            "commit": commit,
            "path": relative,
            "start_byte": start,
            "end_byte": end,
            "start_line": line_for(newlines, start),
            "end_line": line_for(newlines, max(start, end - 1)),
            "shape": shape,
            "token": match.group(1).decode("ascii"),
            "source_sha256": "sha256:" + digest,
        }

    for match in GETTER_RE.finditer(data):
        raw = candidate(match, "getter_call")
        emitted.add((raw["start_byte"], raw["end_byte"], raw["shape"]))
        yield raw
    for match in SELECTOR_RE.finditer(data):
        start, end = match.span(1)
        if data[end : end + 32].lstrip().startswith(b"("):
            continue
        raw = candidate(match, "selector")
        marker = (start, end, raw["shape"])
        if marker not in emitted:
            emitted.add(marker)
            yield raw
    for match in COMPOSITE_KEY_RE.finditer(data):
        raw = candidate(match, "composite_key")
        marker = (raw["start_byte"], raw["end_byte"], raw["shape"])
        if marker not in emitted:
            emitted.add(marker)
            yield raw


def dependency_version(
    root: Path, commit: str, module: str, version_file: str
) -> str:
    data = git_blob(root, commit, version_file).decode("utf-8", errors="strict")
    if version_file == "go.mod":
        pattern = re.compile(rf"^\s*{re.escape(module)}\s+(\S+)")
    else:
        pattern = re.compile(rf"^#\s+{re.escape(module)}\s+(\S+)")
    versions = {match.group(1) for line in data.splitlines() if (match := pattern.search(line))}
    if len(versions) != 1:
        raise ValidationError(
            f"expected one {module} version in pinned {version_file}, found {sorted(versions)!r}"
        )
    return versions.pop()


def lineage_site(
    candidate: Mapping[str, Any], anchor: Mapping[str, Any], version: str
) -> dict[str, Any]:
    identity = candidate_identity(candidate)
    identity.update(
        {
            "kind": "lineage_candidate",
            "lineage_case_id": anchor["case_id"],
            "implementation_module": anchor["implementation_module"],
            "implementation_version": version,
        }
    )
    return {
        "schema_version": SCHEMA_VERSION,
        "site_id": stable_id("g4l", identity),
        "gate": 4,
        "cohort": "lineage",
        "identity": identity,
        **{field: candidate[field] for field in (
            "system", "commit", "path", "start_byte", "end_byte", "start_line",
            "end_line", "shape", "token",
        )},
        "lineage_case_id": anchor["case_id"],
        "implementation_module": anchor["implementation_module"],
        "implementation_version": version,
        "split_stratum": f"g4l:{anchor['case_id']}",
        "forced_split": "holdout",
    }


def gate4_candidate_sites(
    corpus_dir: Path,
    locks: Mapping[str, Mapping[str, Any]],
    vocabulary: set[str],
    burned: Sequence[Mapping[str, Any]],
    *, seed: int, hit_quota: int, open_quota: int,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], Mapping[str, int], int]:
    quotas = {
        f"{system}:{shape}:{bucket}": hit_quota if bucket == "vocabulary" else open_quota
        for system in FIELD_SYSTEMS
        for shape in ("getter_call", "selector", "composite_key")
        for bucket in ("vocabulary", "open")
    }
    reservoir = HashReservoir(quotas, seed=seed)
    anchor_lookup: defaultdict[tuple[str, str, int, str, str], list[Mapping[str, Any]]] = defaultdict(list)
    for anchor in LINEAGE_ANCHORS:
        anchor_lookup[
            (
                str(anchor["system"]), str(anchor["path"]), int(anchor["start_line"]),
                str(anchor["shape"]), str(anchor["token"]),
            )
        ].append(anchor)
    found_anchors: dict[tuple[str, str], dict[str, Any]] = {}
    burned_count = 0

    for system in FIELD_SYSTEMS:
        commit, root = str(locks[system]["commit"]), corpus_dir / system
        for relative, data in source_blobs(
            root, commit, excluded_gitlinks=locks[system].get("excluded_gitlinks")
        ):
            hint = role_hint(relative, data)
            for raw in candidates_in_file(system, commit, relative, data):
                lookup_key = (
                    system, relative, int(raw["start_line"]), str(raw["shape"]), str(raw["token"])
                )
                for anchor in anchor_lookup.get(lookup_key, ()):
                    anchor_key = (str(anchor["case_id"]), system)
                    if anchor_key in found_anchors:
                        raise ValidationError(f"lineage anchor is ambiguous: {anchor_key!r}")
                    version = dependency_version(
                        root, commit, str(anchor["implementation_module"]),
                        str(anchor["version_file"]),
                    )
                    anchor_site = lineage_site(raw, anchor, version)
                    if coordinate_burned(anchor_site, burned):
                        raise ValidationError(f"mandatory lineage anchor is already burned: {anchor_key!r}")
                    found_anchors[anchor_key] = anchor_site

                identity = candidate_identity(raw)
                field_name = raw["token"][3:] if raw["shape"] == "getter_call" else raw["token"]
                vocabulary_hit = field_name in vocabulary
                population_stratum = (
                    f"{system}:{raw['shape']}:{'vocabulary' if vocabulary_hit else 'open'}"
                )
                site = {
                    "schema_version": SCHEMA_VERSION,
                    "site_id": stable_id("g4c", identity),
                    "gate": 4,
                    "cohort": "candidate_recall",
                    "identity": identity,
                    **{field: raw[field] for field in (
                        "system", "commit", "path", "start_byte", "end_byte", "start_line",
                        "end_line", "shape", "token",
                    )},
                    "vocabulary_hit": vocabulary_hit,
                    "role_hint": hint,
                    "split_stratum": f"g4c:{population_stratum}",
                }
                if coordinate_burned(site, burned):
                    burned_count += 1
                    continue
                reservoir.add(population_stratum, site)

    expected = {(str(anchor["case_id"]), str(anchor["system"])) for anchor in LINEAGE_ANCHORS}
    if set(found_anchors) != expected:
        raise ValidationError(f"missing lineage anchors: {sorted(expected-set(found_anchors))!r}")
    lineage = [found_anchors[key] for key in sorted(found_anchors)]
    implementations: defaultdict[str, set[tuple[str, str]]] = defaultdict(set)
    for site in lineage:
        implementations[str(site["lineage_case_id"])].add(
            (str(site["implementation_module"]), str(site["implementation_version"]))
        )
    insufficient = {
        case_id: sorted(values) for case_id, values in implementations.items() if len(values) < 2
    }
    if insufficient:
        raise ValidationError(f"lineage cases lack two implementations/versions: {insufficient!r}")
    return reservoir.sampled(), lineage, dict(reservoir.counts), burned_count


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
            "sealed Gate-3/4 generation requires the precommitted base-relative "
            "out, corpus, and labeling directories"
        )
    generator_config = {
        "schema_version": SCHEMA_VERSION,
        "seed": args.seed,
        "holdout_fraction": args.holdout_fraction,
        "id_systems": list(ID_SYSTEMS),
        "field_systems": list(FIELD_SYSTEMS),
        "id_quota": ID_QUOTA,
        "field_precision_quota": FIELD_PRECISION_QUOTA,
        "candidate_hit_quota": args.candidate_hit_quota,
        "candidate_open_quota": args.candidate_open_quota,
        "lineage_anchors": list(LINEAGE_ANCHORS),
        "target_population": (
            "full pinned regular-Go-blob population; sealed generation requires "
            "zero previously exposed coordinates"
        ),
    }
    validate_precommitted_generator_config("g34", generator_config)
    if args.skip_corpus_check:
        raise ValidationError(
            "sealed Gate-3/4 generation cannot skip corpus verification"
        )
    if args.force:
        raise ValidationError(
            "--force is forbidden for sealed Gate-3/4 generation"
        )
    lock_path = base / "corpus.lock.json"
    ledger_path = base / "labeling" / "burn-ledger.json"
    locks = load_corpus_lock(lock_path)
    systems = set(ID_SYSTEMS) | set(FIELD_SYSTEMS)
    corpus_verified = True
    verify_corpus(corpus_dir, locks, systems)

    burned, burn_digest = load_burn_ledger(ledger_path)
    require_fresh_blind_population(len(burned))
    identity_facts: dict[str, list[dict[str, Any]]] = {}
    field_facts: dict[str, list[dict[str, Any]]] = {}
    fact_files: dict[str, Path] = {}
    fact_sets: dict[str, Sequence[Mapping[str, Any]]] = {}
    for system in ID_SYSTEMS:
        path = out_dir / f"{system}.identity.jsonl"
        facts = load_facts(
            path, system=system, expected_commit=str(locks[system]["commit"]),
            corpus_dir=corpus_dir,
        )
        identity_facts[system] = facts
        fact_files[f"{system}.identity"] = path
        fact_sets[f"{system}.identity"] = facts
    for system in FIELD_SYSTEMS:
        path = out_dir / f"{system}.fields.jsonl"
        facts = load_facts(
            path, system=system, expected_commit=str(locks[system]["commit"]),
            corpus_dir=corpus_dir,
        )
        field_facts[system] = facts
        fact_files[f"{system}.fields"] = path
        fact_sets[f"{system}.fields"] = facts

    producer = producer_summary(fact_sets)
    require_producer_capabilities(producer, "g34")

    paths = {
        "dev_sites": label_dir / "g34.v3.sites.dev.jsonl",
        "holdout_sites": label_dir / "g34.v3.sites.holdout.jsonl",
        "key": label_dir / "g34.v3.key.jsonl",
        "label_template": label_dir / "g34.v3.labels.template.jsonl",
    }
    manifest_path = label_dir / "g34.v3.manifest.json"
    context_paths: list[Path] = []
    if args.materialize_context:
        context_dir = (args.context_dir or out_dir / "label-context").resolve()
        context_paths = [
            context_dir / "g34.v3.context.dev.jsonl",
            context_dir / "g34.v3.context.holdout.jsonl",
        ]
    ensure_writable_targets(
        [*paths.values(), manifest_path, *context_paths], force=False,
    )

    g3, g3_excluded = gate3_sites(identity_facts, burned, seed=args.seed)
    g4_precision, g4p_burned = gate4_precision_sites(
        field_facts, burned, seed=args.seed
    )
    vocabulary = generated_field_vocabulary(corpus_dir, locks)
    g4_candidates, lineage, population_counts, g4c_burned = gate4_candidate_sites(
        corpus_dir, locks, vocabulary, burned,
        seed=args.seed, hit_quota=args.candidate_hit_quota,
        open_quota=args.candidate_open_quota,
    )
    dev, holdout = split_sites(
        g3 + g4_precision + g4_candidates + lineage,
        seed=args.seed, holdout_fraction=args.holdout_fraction,
    )
    keys = [site_key(site) for site in sorted(dev + holdout, key=lambda row: row["site_id"])]
    validate_artifacts(dev, holdout, keys)

    verify_corpus(corpus_dir, locks, systems)  # post-scan check, before publication
    atomic_write_jsonl(paths["dev_sites"], dev)
    atomic_write_jsonl(paths["holdout_sites"], holdout)
    atomic_write_jsonl(paths["key"], keys)
    atomic_write_jsonl(
        paths["label_template"], (label_template(site) for site in sorted(dev + holdout, key=lambda row: row["site_id"]))
    )
    verify_corpus(corpus_dir, locks, systems)  # post-publication source check
    counts = summarize_sites(dev, holdout)
    counts.update(
        {
            "candidate_population_by_stratum": dict(sorted(population_counts.items())),
            "candidate_population": sum(population_counts.values()),
            "candidate_sample": len(g4_candidates),
            "gate3_exclusions": g3_excluded,
            "gate4_precision_burned": g4p_burned,
            "gate4_candidate_burned": g4c_burned,
            "legacy_burn_coordinates": len(burned),
            "lineage_sites": len(lineage),
        }
    )
    manifest = create_manifest(
        mode="g34", base=base, manifest_path=manifest_path, lock_path=lock_path,
        locks=locks, generator_config=generator_config,
        generator_files=(Path(__file__).resolve(), base / "gate34_common.py"),
        fact_files=fact_files, fact_sets=fact_sets, artifact_files=paths,
        artifact_records={
            "dev_sites": len(dev), "holdout_sites": len(holdout), "key": len(keys),
            "label_template": len(keys),
        },
        counts=counts, burn_ledger_path=ledger_path,
        burn_coordinate_count=len(burned), burn_coordinates_sha256=burn_digest,
        corpus_verified=corpus_verified,
    )
    if args.materialize_context:
        materialize_context(
            dev, corpus_dir, context_paths[0], locks
        )
        materialize_context(
            holdout, corpus_dir, context_paths[1], locks
        )

    print(
        f"schema={SCHEMA_VERSION} dev={len(dev)} holdout={len(holdout)} "
        f"population={sum(population_counts.values())} sample={len(g4_candidates)}"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValidationError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)
