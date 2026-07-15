#!/usr/bin/env python3
"""Fail-closed, dependency-free support for the T11.1 Gate 3/4 benchmark.

Benchmark artifacts contain coordinates and commitments, never copied source.
Every source byte is read from a pinned Git object.  Site records are sealed,
their keys commit to the complete record, and a self-digested manifest commits
to the generator configuration, corpus, producer outputs, artifacts, and
cohort counts.
"""

from __future__ import annotations

import hashlib
import heapq
import json
import math
import os
import posixpath
import re
import subprocess
import tempfile
from collections import defaultdict
from pathlib import Path, PurePosixPath
from typing import Any, Iterable, Iterator, Mapping, Sequence


SCHEMA_VERSION = "t111-g34-v3"
MANIFEST_SCHEMA_VERSION = "t111-g34-manifest-v1"
BURN_LEDGER_SCHEMA_VERSION = "t111-burn-ledger-v2"
LEGACY_BURN_LEDGER_SCHEMA_VERSION = "t111-burn-ledger-v1"
PRODUCER_SCHEMA_VERSION = "t111-v4"
PRODUCER_EXTRACTOR_VERSION = "spike-0.5.1"

VALID_TIERS = frozenset({"exact", "derived", "heuristic", "unresolved"})
VALID_ACCESS = frozenset(
    {"getter_read", "field_read", "field_write", "construct", "unknown"}
)
VALID_ROLES = frozenset(
    {"production", "test", "mock", "generated", "vendor", "unknown"}
)
DIRECT_ACCESS = frozenset({"getter_read", "field_read", "field_write"})
GATE3_MERGE_PREDICATES = frozenset(
    {
        "K8S_SERVICE_DNS",
        "WORKLOAD_RUNS_IMAGE",
        "IMAGE_BUILD_CONTEXT",
        "DEPLOYABLE_REACHES_OPERATION",
    }
)

# These are part of the manifest, and the scorer rejects a manifest that
# weakens them.  Gate decisions use the holdout split only.
GATE_THRESHOLDS: dict[str, float] = {
    "gate3_precision": 0.99,
    "gate3_pairwise_precision": 0.95,
    "gate4_direct_precision": 0.98,
    "gate4_precision_access_accuracy": 0.95,
    "gate4_precision_role_accuracy": 0.95,
    "gate4_precision_canonical_accuracy": 0.98,
    "gate4_direct_recall": 0.90,
    "gate4_recall_access_accuracy": 0.95,
    "gate4_recall_role_accuracy": 0.95,
    "gate4_lineage": 1.0,
}
MINIMUM_EVIDENCE: dict[str, int] = {
    "gate3_assertions": 50,
    "gate3_pairwise_pairs": 30,
    "gate4_direct_precision": 50,
    "gate4_direct_recall_eligible": 75,
    "gate4_direct_recall_per_system": 8,
    "gate4_lineage_implementations": 2,
}


def precommitted_generator_config(
    mode: str, *, upstream_g34_manifest_digest: str | None = None,
) -> dict[str, Any]:
    """Return the one code-reviewed sampling configuration for a gate mode."""
    if mode == "g34":
        return {
            "schema_version": SCHEMA_VERSION,
            "seed": 111,
            "holdout_fraction": 0.30,
            "id_systems": ["online-boutique", "dapr", "temporal", "loki", "temporal-helm"],
            "field_systems": ["online-boutique", "dapr", "temporal", "loki"],
            "id_quota": {
                "online-boutique": 40, "dapr": 35, "temporal": 12,
                "loki": 45, "temporal-helm": 4,
            },
            "field_precision_quota": {
                "online-boutique": 25, "dapr": 30, "temporal": 30, "loki": 25,
            },
            "candidate_hit_quota": 35,
            "candidate_open_quota": 8,
            "lineage_anchors": [
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
            ],
            "target_population": (
                "full pinned regular-Go-blob population; sealed generation requires "
                "zero previously exposed coordinates"
            ),
        }
    if mode == "h2":
        if not _sha256_string(upstream_g34_manifest_digest):
            raise ValidationError(
                "holdout-2 configuration requires the validated Gate-3/4 manifest digest"
            )
        return {
            "schema_version": SCHEMA_VERSION,
            "seed": 222,
            "systems": ["online-boutique", "dapr", "temporal", "loki", "temporal-helm"],
            "quota": {
                "online-boutique": 12, "dapr": 12, "temporal": 6,
                "loki": 22, "temporal-helm": 3,
            },
            "upstream_g34_manifest_digest": upstream_g34_manifest_digest,
            "burn_rule": (
                "line overlap on system,path,start_line,end_line; predicate/value ignored"
            ),
        }
    raise ValidationError(f"unknown benchmark generation mode: {mode!r}")


def validate_precommitted_generator_config(
    mode: str, config: Mapping[str, Any], *,
    upstream_g34_manifest_digest: str | None = None,
) -> dict[str, Any]:
    """Reject any configuration other than the single precommitted one."""
    if mode == "h2" and upstream_g34_manifest_digest is None:
        candidate = config.get("upstream_g34_manifest_digest")
        upstream_g34_manifest_digest = candidate if isinstance(candidate, str) else None
    expected = precommitted_generator_config(
        mode, upstream_g34_manifest_digest=upstream_g34_manifest_digest,
    )
    supplied = dict(config)
    if supplied != expected:
        raise ValidationError(
            f"{mode} generator configuration differs from the precommitted configuration"
        )
    return expected


class ValidationError(ValueError):
    """The benchmark input is incomplete, ambiguous, or not reproducible."""


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def sha256_bytes(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    try:
        return sha256_bytes(path.read_bytes())
    except OSError as exc:
        raise ValidationError(f"cannot hash {path}: {exc}") from exc


def digest_value(value: Any) -> str:
    return sha256_bytes(canonical_json(value).encode("utf-8"))


def stable_id(prefix: str, identity: Mapping[str, Any]) -> str:
    return f"{prefix}:{hashlib.sha256(canonical_json(dict(identity)).encode('utf-8')).hexdigest()}"


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _sha256_string(value: Any) -> bool:
    if not isinstance(value, str) or not value.startswith("sha256:"):
        return False
    digest = value.removeprefix("sha256:")
    return len(digest) == 64 and all(ch in "0123456789abcdef" for ch in digest)


def _safe_relative(value: str) -> PurePosixPath:
    path = PurePosixPath(value)
    if not value or path.is_absolute() or ".." in path.parts or value.startswith("./"):
        raise ValidationError(f"unsafe repository-relative path: {value!r}")
    return path


def assertion_identity(fact: Mapping[str, Any], gate: int) -> dict[str, Any]:
    required = (
        "system", "commit", "path", "start_byte", "end_byte", "start_line",
        "end_line", "predicate", "subject", "object", "tier", "rule_id",
        "assertion_id",
    )
    missing = [name for name in required if name not in fact]
    if missing:
        raise ValidationError(f"fact is missing {', '.join(missing)}")
    if fact["tier"] not in VALID_TIERS:
        raise ValidationError(f"invalid tier {fact['tier']!r}")
    return {
        "schema_version": SCHEMA_VERSION,
        "kind": "assertion",
        "gate": gate,
        **{name: fact[name] for name in required},
        "detail": fact.get("detail", ""),
        "code_role": fact.get("code_role", "unknown"),
    }


def candidate_identity(candidate: Mapping[str, Any]) -> dict[str, Any]:
    required = (
        "system", "commit", "path", "start_byte", "end_byte", "start_line",
        "end_line", "shape", "token", "source_sha256",
    )
    missing = [name for name in required if name not in candidate]
    if missing:
        raise ValidationError(f"candidate is missing {', '.join(missing)}")
    return {
        "schema_version": SCHEMA_VERSION,
        "kind": "candidate",
        "gate": 4,
        **{name: candidate[name] for name in required},
    }


def read_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValidationError(f"cannot read JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValidationError(f"{path}: top-level JSON value must be an object")
    return value


def read_jsonl(path: Path, *, required: bool = True) -> list[dict[str, Any]]:
    if not path.exists():
        if required:
            raise ValidationError(f"required file does not exist: {path}")
        return []
    records: list[dict[str, Any]] = []
    with path.open(encoding="utf-8") as handle:
        for line_number, raw in enumerate(handle, 1):
            if not raw.strip():
                continue
            try:
                value = json.loads(raw)
            except json.JSONDecodeError as exc:
                raise ValidationError(f"{path}:{line_number}: {exc}") from exc
            if not isinstance(value, dict):
                raise ValidationError(f"{path}:{line_number}: JSON value must be an object")
            records.append(value)
    return records


def _atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        # link(2) publishes the complete temporary file only if the destination
        # does not already exist. Unlike os.replace, this makes a retry or a
        # concurrent generator structurally unable to overwrite a holdout.
        try:
            os.link(temporary, path)
        except FileExistsError as exc:
            raise ValidationError(f"refusing to overwrite benchmark artifact: {path}") from exc
        os.unlink(temporary)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def atomic_write_json(path: Path, value: Mapping[str, Any]) -> None:
    _atomic_write(path, (canonical_json(dict(value)) + "\n").encode("utf-8"))


def atomic_write_jsonl(path: Path, records: Iterable[Mapping[str, Any]]) -> None:
    data = "".join(canonical_json(dict(record)) + "\n" for record in records)
    _atomic_write(path, data.encode("utf-8"))


def ensure_writable_targets(paths: Iterable[Path], *, force: bool) -> None:
    if force:
        raise ValidationError(
            "--force is forbidden for sealed benchmark generation; refresh the locked "
            "corpus or combine the prior labeled outcomes in a reviewed estimator"
        )
    existing = [str(path) for path in paths if path.exists()]
    if existing:
        raise ValidationError(
            "refusing to overwrite or reuse benchmark artifacts: "
            + ", ".join(existing)
        )


def unique_index(
    records: Iterable[Mapping[str, Any]], field: str, description: str
) -> dict[Any, dict[str, Any]]:
    result: dict[Any, dict[str, Any]] = {}
    for number, raw in enumerate(records, 1):
        record = dict(raw)
        if field not in record:
            raise ValidationError(f"{description} record {number} has no {field}")
        key = record[field]
        if key in result:
            raise ValidationError(f"duplicate/colliding {description} {field}: {key!r}")
        result[key] = record
    return result


def seal_site(raw: Mapping[str, Any]) -> dict[str, Any]:
    record = dict(raw)
    record.pop("record_digest", None)
    record["record_digest"] = digest_value(record)
    return record


def site_key(site: Mapping[str, Any]) -> dict[str, Any]:
    return {
        "schema_version": SCHEMA_VERSION,
        "site_id": site["site_id"],
        "identity": site["identity"],
        "record_digest": site["record_digest"],
    }


def validate_site_id(record: Mapping[str, Any]) -> None:
    if record.get("schema_version") != SCHEMA_VERSION:
        raise ValidationError(f"site {record.get('site_id')!r} has wrong schema")
    identity = record.get("identity")
    if not isinstance(identity, dict):
        raise ValidationError(f"site {record.get('site_id')!r} has no complete identity")
    cohort = record.get("cohort")
    if cohort == "candidate_recall":
        prefix = "g4c"
    elif cohort == "lineage":
        prefix = "g4l"
    elif record.get("gate") == 3 and record.get("evaluation") == "holdout-2":
        prefix = "g3h2"
    elif record.get("gate") == 3:
        prefix = "g3"
    elif record.get("gate") == 4:
        prefix = "g4"
    else:
        raise ValidationError(f"site has invalid gate/cohort: {record.get('site_id')!r}")
    expected = stable_id(prefix, identity)
    if record.get("site_id") != expected:
        raise ValidationError(f"site ID does not cover complete identity: {record.get('site_id')!r}")
    supplied_digest = record.get("record_digest")
    unsigned = dict(record)
    unsigned.pop("record_digest", None)
    if supplied_digest != digest_value(unsigned):
        raise ValidationError(f"site record digest mismatch: {record.get('site_id')!r}")
    if identity.get("gate") != record.get("gate"):
        raise ValidationError(f"site/identity gate mismatch: {record.get('site_id')!r}")
    for field in (
        "system", "commit", "path", "start_byte", "end_byte", "start_line", "end_line"
    ):
        if record.get(field) != identity.get(field):
            raise ValidationError(f"site/identity {field} mismatch: {record.get('site_id')!r}")
    if record.get("gate") == 3 or cohort == "assertion_precision":
        for field in ("predicate", "subject", "object", "tier", "code_role"):
            if record.get(field) != identity.get(field):
                raise ValidationError(f"site/identity {field} mismatch: {record.get('site_id')!r}")
    if cohort in {"candidate_recall", "lineage"}:
        for field in ("shape", "token"):
            if record.get(field) != identity.get(field):
                raise ValidationError(f"site/identity {field} mismatch: {record.get('site_id')!r}")


def validate_artifacts(
    dev_sites: Sequence[Mapping[str, Any]],
    holdout_sites: Sequence[Mapping[str, Any]],
    key_records: Sequence[Mapping[str, Any]],
) -> tuple[dict[str, dict[str, Any]], dict[str, dict[str, Any]], dict[str, dict[str, Any]]]:
    dev = unique_index(dev_sites, "site_id", "dev site")
    holdout = unique_index(holdout_sites, "site_id", "holdout site")
    overlap = sorted(set(dev) & set(holdout))
    if overlap:
        raise ValidationError(f"dev and holdout share site IDs: {overlap[:5]!r}")
    keys = unique_index(key_records, "site_id", "key")
    sites = {**dev, **holdout}
    if set(keys) != set(sites):
        raise ValidationError(
            "site/key coverage mismatch; "
            f"no key={sorted(set(sites)-set(keys))[:5]!r}, "
            f"no site={sorted(set(keys)-set(sites))[:5]!r}"
        )
    for site_id, site in sites.items():
        validate_site_id(site)
        expected_key = site_key(site)
        if keys[site_id] != expected_key:
            raise ValidationError(f"key does not bind the complete site record: {site_id}")
    return dev, holdout, keys


def validate_single_artifact(
    sites: Sequence[Mapping[str, Any]], key_records: Sequence[Mapping[str, Any]]
) -> tuple[dict[str, dict[str, Any]], dict[str, dict[str, Any]]]:
    indexed = unique_index(sites, "site_id", "site")
    keys = unique_index(key_records, "site_id", "key")
    if set(indexed) != set(keys):
        raise ValidationError("site/key coverage mismatch")
    for site_id, site in indexed.items():
        validate_site_id(site)
        if keys[site_id] != site_key(site):
            raise ValidationError(f"key does not bind the complete site record: {site_id}")
    return indexed, keys


def _label_evidence(label: Mapping[str, Any], site_id: str) -> None:
    rationale = label.get("rationale")
    evidence = label.get("evidence")
    if not isinstance(rationale, str) or not rationale.strip():
        raise ValidationError(f"label {site_id} needs a non-empty rationale")
    if (
        not isinstance(evidence, list) or not evidence
        or any(not isinstance(item, str) or not item.strip() for item in evidence)
    ):
        raise ValidationError(f"label {site_id} needs a non-empty evidence string list")


def _canonical_label_fields(label: Mapping[str, Any], site_id: str) -> None:
    for field in (
        "expected_contract_lineage_id", "expected_message_full_name", "expected_field_number"
    ):
        value = label.get(field)
        if value in (None, "", "TODO"):
            raise ValidationError(f"label {site_id} needs {field}")
    if label.get("expected_access") not in VALID_ACCESS:
        raise ValidationError(f"label {site_id} has invalid expected_access")
    if label.get("expected_code_role") not in VALID_ROLES:
        raise ValidationError(f"label {site_id} has invalid expected_code_role")


def validate_labels(
    labels: Sequence[Mapping[str, Any]],
    all_sites: Mapping[str, Mapping[str, Any]],
    required_site_ids: Iterable[str],
) -> dict[str, dict[str, Any]]:
    indexed = unique_index(labels, "site_id", "label")
    unknown = sorted(set(indexed) - set(all_sites))
    if unknown:
        raise ValidationError(f"labels reference unknown site IDs: {unknown[:5]!r}")
    required = set(required_site_ids)
    missing = sorted(required - set(indexed))
    if missing:
        raise ValidationError(f"missing labels for selected sites: {missing[:10]!r}")
    for site_id in required:
        site = all_sites[site_id]
        label = indexed[site_id]
        _label_evidence(label, site_id)
        if site["gate"] == 3:
            allowed = {"site_id", "verdict", "rationale", "evidence"}
            if label.get("verdict") not in {"correct", "incorrect"}:
                raise ValidationError(f"label {site_id} needs correct|incorrect verdict")
        elif site["cohort"] == "assertion_precision":
            allowed = {
                "site_id", "verdict", "rationale", "evidence", "expected_access",
                "expected_code_role", "expected_contract_lineage_id",
                "expected_message_full_name", "expected_field_number",
            }
            if label.get("verdict") not in {"correct", "incorrect"}:
                raise ValidationError(f"label {site_id} needs correct|incorrect verdict")
            _canonical_label_fields(label, site_id)
        elif site["cohort"] in {"candidate_recall", "lineage"}:
            allowed = {
                "site_id", "eligible", "rationale", "evidence", "expected_access",
                "expected_code_role", "expected_contract_lineage_id",
                "expected_message_full_name", "expected_field_number",
            }
            if label.get("eligible") not in {"yes", "no"}:
                raise ValidationError(f"label {site_id} needs yes|no eligible")
            if site["cohort"] == "lineage" and label["eligible"] != "yes":
                raise ValidationError(f"mandatory lineage anchor {site_id} must be eligible")
            if label["eligible"] == "yes":
                _canonical_label_fields(label, site_id)
            else:
                allowed -= {
                    "expected_access", "expected_code_role", "expected_contract_lineage_id",
                    "expected_message_full_name", "expected_field_number",
                }
        else:
            raise ValidationError(f"unknown cohort for {site_id}")
        extras = set(label) - allowed
        missing_fields = allowed - set(label)
        if extras or missing_fields:
            raise ValidationError(
                f"label {site_id} fields do not match schema; missing={sorted(missing_fields)!r}, "
                f"extra={sorted(extras)!r}"
            )
    return indexed


def label_template(site: Mapping[str, Any]) -> dict[str, Any]:
    base: dict[str, Any] = {
        "site_id": site["site_id"],
        "rationale": "TODO",
        "evidence": ["TODO immutable path:line or descriptor citation"],
    }
    if site["gate"] == 3:
        base["verdict"] = "TODO"
    elif site["cohort"] == "assertion_precision":
        base["verdict"] = "TODO"
        base.update(_canonical_template())
    else:
        base["eligible"] = "TODO"
        base.update(_canonical_template())
    return base


def _canonical_template() -> dict[str, str]:
    return {
        "expected_access": "TODO",
        "expected_code_role": "TODO",
        "expected_contract_lineage_id": "TODO",
        "expected_message_full_name": "TODO",
        "expected_field_number": "TODO",
    }


def rank_for(seed: int, namespace: str, site_id: str) -> int:
    raw = f"{SCHEMA_VERSION}\x00{seed}\x00{namespace}\x00{site_id}".encode("utf-8")
    return int.from_bytes(hashlib.sha256(raw).digest(), "big")


def deterministic_sample(
    records: Sequence[dict[str, Any]], quota: int, *, seed: int, namespace: str
) -> list[dict[str, Any]]:
    if quota <= 0:
        return []
    return sorted(
        records, key=lambda record: (rank_for(seed, namespace, record["site_id"]), record["site_id"])
    )[:quota]


class HashReservoir:
    """Order-independent deterministic hash samples with exact population counts."""

    def __init__(self, quotas: Mapping[str, int], *, seed: int):
        self.quotas = dict(quotas)
        self.seed = seed
        self.counts: defaultdict[str, int] = defaultdict(int)
        self.heaps: defaultdict[str, list[tuple[int, str, dict[str, Any]]]] = defaultdict(list)

    def add(self, stratum: str, record: dict[str, Any]) -> None:
        self.counts[stratum] += 1
        quota = self.quotas.get(stratum, 0)
        if quota <= 0:
            return
        rank = rank_for(self.seed, stratum, record["site_id"])
        item = (-rank, record["site_id"], record)
        heap = self.heaps[stratum]
        if len(heap) < quota:
            heapq.heappush(heap, item)
        elif item > heap[0]:
            heapq.heapreplace(heap, item)

    def sampled(self) -> list[dict[str, Any]]:
        result: list[dict[str, Any]] = []
        for stratum in sorted(self.counts):
            records = [item[2] for item in self.heaps[stratum]]
            records.sort(key=lambda r: (rank_for(self.seed, stratum, r["site_id"]), r["site_id"]))
            for record in records:
                record["population_stratum"] = stratum
                record["population_size"] = self.counts[stratum]
                record["sample_size_total"] = len(records)
                result.append(record)
        return result


def allocate_quota(counts: Mapping[str, int], quota: int) -> dict[str, int]:
    positive = {key: value for key, value in counts.items() if value > 0}
    if not positive or quota <= 0:
        return {key: 0 for key in counts}
    target = min(quota, sum(positive.values()))
    total = sum(positive.values())
    raw = {key: target * value / total for key, value in positive.items()}
    allocation = {key: min(positive[key], int(raw[key])) for key in positive}
    remaining = target - sum(allocation.values())
    for key in sorted(positive, key=lambda item: (-(raw[item] - int(raw[item])), item)):
        if remaining == 0:
            break
        if allocation[key] < positive[key]:
            allocation[key] += 1
            remaining -= 1
    return {key: allocation.get(key, 0) for key in counts}


def split_sites(
    records: Sequence[dict[str, Any]], *, seed: int, holdout_fraction: float
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    if not 0.0 < holdout_fraction < 1.0:
        raise ValidationError("holdout fraction must be between zero and one")
    strata: defaultdict[str, list[dict[str, Any]]] = defaultdict(list)
    dev: list[dict[str, Any]] = []
    holdout: list[dict[str, Any]] = []
    for record in records:
        forced = record.get("forced_split")
        if forced in {"dev", "holdout"}:
            (dev if forced == "dev" else holdout).append(record)
        elif forced is None:
            strata[str(record["split_stratum"])].append(record)
        else:
            raise ValidationError(f"invalid forced_split {forced!r}")
    for stratum, members in sorted(strata.items()):
        ordered = sorted(
            members,
            key=lambda r: (rank_for(seed, f"split:{stratum}", r["site_id"]), r["site_id"]),
        )
        if len(ordered) == 1:
            hold_count = 1 if rank_for(seed, "singleton", ordered[0]["site_id"]) % 10 < 3 else 0
        else:
            hold_count = max(1, min(len(ordered) - 1, round(len(ordered) * holdout_fraction)))
        held = {record["site_id"] for record in ordered[:hold_count]}
        for record in members:
            (holdout if record["site_id"] in held else dev).append(record)
    for split_name, members in (("dev", dev), ("holdout", holdout)):
        split_counts: defaultdict[str, int] = defaultdict(int)
        for record in members:
            if record["cohort"] == "candidate_recall":
                split_counts[record["population_stratum"]] += 1
        for record in members:
            if record["cohort"] != "candidate_recall":
                continue
            n = split_counts[record["population_stratum"]]
            if n <= 0:
                raise ValidationError(f"missing {split_name} candidate stratum")
            record["split_sample_size"] = n
            record["sample_weight"] = record["population_size"] / n
    dev = [seal_site(record) for record in sorted(dev, key=lambda row: row["site_id"])]
    holdout = [seal_site(record) for record in sorted(holdout, key=lambda row: row["site_id"])]
    return dev, holdout


def load_corpus_lock(path: Path) -> dict[str, dict[str, Any]]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValidationError(f"cannot read corpus lock {path}: {exc}") from exc
    if not isinstance(value, list):
        raise ValidationError(f"corpus lock must be a JSON array: {path}")
    result: dict[str, dict[str, Any]] = {}
    for record in value:
        if not isinstance(record, dict) or not record.get("name") or not record.get("commit"):
            raise ValidationError(f"bad corpus lock record: {record!r}")
        if record["name"] in result:
            raise ValidationError(f"duplicate corpus lock system: {record['name']}")
        commit = str(record["commit"])
        if len(commit) != 40 or any(ch not in "0123456789abcdef" for ch in commit):
            raise ValidationError(f"corpus lock has non-full commit: {commit!r}")
        result[str(record["name"])] = dict(record)
    return result


def _git(root: Path, args: Sequence[str], *, text: bool = False) -> bytes | str:
    try:
        result = subprocess.run(
            ["git", "-C", str(root), *args], check=True, capture_output=True,
            text=text,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        stderr = getattr(exc, "stderr", b"")
        if isinstance(stderr, bytes):
            stderr = stderr.decode("utf-8", errors="replace")
        raise ValidationError(f"git {' '.join(args)} failed in {root}: {str(stderr).strip()}") from exc
    return result.stdout


def _gitlink_exclusions(
    records: Sequence[Mapping[str, Any]] | None,
) -> dict[str, Mapping[str, Any]]:
    expected: dict[str, Mapping[str, Any]] = {}
    for record in records or ():
        if not isinstance(record, Mapping):
            raise ValidationError(f"bad excluded_gitlinks record: {record!r}")
        relative = str(record.get("path", ""))
        object_id = str(record.get("object_id", ""))
        reason = str(record.get("reason", "")).strip()
        _safe_relative(relative)
        if not re.fullmatch(r"[0-9a-f]{40}", object_id):
            raise ValidationError(
                f"excluded gitlink {relative!r} has invalid object ID {object_id!r}"
            )
        if not reason:
            raise ValidationError(f"excluded gitlink {relative!r} has no review rationale")
        if relative in expected:
            raise ValidationError(f"duplicate excluded gitlink path: {relative!r}")
        expected[relative] = dict(record)
    return expected


def _tree_entries(
    root: Path,
    commit: str,
    excluded_gitlinks: Sequence[Mapping[str, Any]] | None = None,
) -> list[tuple[str, str, str, str]]:
    """Read a pinned tree, permitting only exact reviewed gitlink boundaries."""
    expected = _gitlink_exclusions(excluded_gitlinks)
    raw = _git(root, ["ls-tree", "-r", "-z", commit])
    assert isinstance(raw, bytes)
    result: list[tuple[str, str, str, str]] = []
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        metadata, separator, path_bytes = entry.partition(b"\t")
        if not separator:
            raise ValidationError(f"malformed git ls-tree entry in {root}")
        fields = metadata.decode("ascii").split()
        if len(fields) != 3:
            raise ValidationError(f"malformed git ls-tree metadata in {root}")
        mode, kind, object_id = fields
        relative = path_bytes.decode("utf-8", errors="surrogateescape")
        _safe_relative(relative)
        if mode == "160000" or kind == "commit":
            exclusion = expected.get(relative)
            if exclusion is None:
                raise ValidationError(
                    f"refusing undeclared gitlink {relative!r} in pinned corpus {root}"
                )
            wanted = str(exclusion["object_id"])
            if mode != "160000" or kind != "commit" or object_id != wanted:
                raise ValidationError(
                    f"excluded gitlink {relative!r} changed: got {mode} {kind} "
                    f"{object_id}, want 160000 commit {wanted}"
                )
            del expected[relative]
            continue
        if relative in expected:
            raise ValidationError(
                f"excluded gitlink {relative!r} is now {mode} {kind}, not a gitlink"
            )
        result.append((mode, kind, object_id, relative))
    if expected:
        raise ValidationError(
            "declared gitlink exclusions are absent from pinned tree: "
            + ", ".join(sorted(expected))
        )
    return result


def verify_corpus(
    corpus_dir: Path, locks: Mapping[str, Mapping[str, Any]], systems: Iterable[str]
) -> None:
    for system in sorted(set(systems)):
        if system not in locks:
            raise ValidationError(f"system {system!r} is absent from corpus.lock.json")
        root = corpus_dir / system
        if not root.is_dir():
            raise ValidationError(f"missing corpus checkout: {root}")
        head = str(_git(root, ["rev-parse", "HEAD"], text=True)).strip()
        dirty = str(
            _git(root, ["status", "--porcelain=v1", "--untracked-files=all"], text=True)
        ).strip()
        expected = str(locks[system]["commit"])
        if head != expected:
            raise ValidationError(f"corpus {system} HEAD {head} != lock {expected}")
        if dirty:
            raise ValidationError(f"corpus {system} is dirty; benchmark input is not immutable")
        _tree_entries(root, expected, locks[system].get("excluded_gitlinks"))


def git_blob(root: Path, commit: str, relative: str) -> bytes:
    """Read a regular blob or a safe, commit-backed in-tree file alias."""

    _safe_relative(relative)
    current = relative
    seen: set[str] = set()
    while True:
        if current in seen:
            raise ValidationError(f"pinned Git symlink cycle at {relative!r}")
        seen.add(current)
        raw = _git(root, ["ls-tree", "-z", commit, "--", current])
        assert isinstance(raw, bytes)
        entries = [entry for entry in raw.split(b"\0") if entry]
        exact: list[tuple[str, str, str]] = []
        for entry in entries:
            metadata, separator, encoded_path = entry.partition(b"\t")
            if not separator:
                continue
            decoded = encoded_path.decode("utf-8", errors="surrogateescape")
            fields = metadata.decode("ascii").split()
            if decoded == current and len(fields) == 3:
                exact.append((fields[0], fields[1], fields[2]))
        if len(exact) != 1:
            raise ValidationError(
                f"expected one pinned Git blob for {current!r} in {root}"
            )
        mode, kind, object_id = exact[0]
        if kind != "blob":
            raise ValidationError(
                f"refusing non-blob Git object {mode} {kind} at {current!r}"
            )
        data = _git(root, ["cat-file", "blob", object_id])
        assert isinstance(data, bytes)
        if mode in {"100644", "100755"}:
            return data
        if mode != "120000":
            raise ValidationError(f"refusing non-regular Git object {mode} at {current!r}")
        try:
            target = data.decode("utf-8", errors="strict")
        except UnicodeDecodeError as exc:
            raise ValidationError(
                f"pinned Git symlink target is not UTF-8 at {current!r}"
            ) from exc
        if not target or "\x00" in target or posixpath.isabs(target):
            raise ValidationError(
                f"pinned Git symlink has empty or absolute target at {current!r}"
            )
        current = posixpath.normpath(
            posixpath.join(posixpath.dirname(current), target)
        )
        try:
            _safe_relative(current)
        except ValidationError as exc:
            raise ValidationError(
                f"pinned Git symlink escapes the tree at {relative!r}"
            ) from exc


def source_blobs(
    root: Path,
    commit: str,
    suffix: str = ".go",
    excluded_gitlinks: Sequence[Mapping[str, Any]] | None = None,
) -> Iterator[tuple[str, bytes]]:
    entries: list[tuple[str, str]] = []
    for mode, kind, object_id, relative in _tree_entries(
        root, commit, excluded_gitlinks
    ):
        if not relative.endswith(suffix):
            continue
        if kind != "blob" or mode not in {"100644", "100755"}:
            raise ValidationError(f"refusing non-regular source {mode} {kind} {relative}")
        entries.append((relative, object_id))
    for relative, object_id in sorted(entries):
        data = _git(root, ["cat-file", "blob", object_id])
        assert isinstance(data, bytes)
        yield relative, data


def _length_delimited_digest(values: Iterable[str]) -> str:
    digest = hashlib.sha256()
    for value in values:
        encoded = value.encode("utf-8")
        digest.update(str(len(encoded)).encode("ascii"))
        digest.update(b":")
        digest.update(encoded)
        digest.update(b";")
    return digest.hexdigest()


def parse_detail(detail: Any) -> dict[str, str]:
    result: dict[str, str] = {}
    if not isinstance(detail, str):
        return result
    for part in detail.split(";"):
        key, separator, value = part.partition("=")
        if separator:
            result[key] = value
    return result


def _fact_fingerprint(record: Mapping[str, Any]) -> str:
    predicate = str(record["predicate"])
    values = [predicate, str(record["object"])]
    if predicate in {"CALLS_OPERATION", "REFERENCES_PROTO_FIELD"}:
        values.append(str(record.get("detail", "")))
    elif predicate in {"SERVICE_REACHABLE_FROM_BINARY", "CALLER_REACHABLE_FROM_BINARY"}:
        values.append(parse_detail(record.get("detail", "")).get("support", ""))
    elif predicate not in {"DECLARES_OPERATION", "DECLARES_FIELD", "IMPLEMENTS_SERVICE"}:
        values.extend([str(record["subject"]), str(record.get("detail", ""))])
    fingerprint = "sha256:" + _length_delimited_digest(values)
    semantic_inputs = record.get("semantic_inputs_digest")
    if semantic_inputs is not None:
        fingerprint = sha256_bytes(
            f"{fingerprint};semantic_inputs={semantic_inputs}".encode("utf-8")
        )
    return fingerprint


def _atom_id(record: Mapping[str, Any]) -> str:
    values = [
        str(record["schema_version"]), str(record["blob_digest"]),
        f"{record['start_byte']}-{record['end_byte']}", str(record["rule_id"]),
        str(record["extractor_version"]), str(record["adapter_config_digest"]),
        str(record["fact_fingerprint"]),
    ]
    return "ea_" + _length_delimited_digest(values)


def _assertion_id(record: Mapping[str, Any]) -> str:
    detail = str(record.get("detail", ""))
    if record["predicate"] in {"SERVICE_REACHABLE_FROM_BINARY", "CALLER_REACHABLE_FROM_BINARY"}:
        detail = ""
    return "as_" + _length_delimited_digest(
        [
            str(record["system"]), str(record["predicate"]), str(record["subject"]), str(record["object"]),
            detail,
        ]
    )


def canonical_fact_identity(fact: Mapping[str, Any]) -> tuple[str, str, str] | None:
    canonical = fact.get("canonical_field")
    if not isinstance(canonical, dict):
        return None
    if set(canonical) != {
        "contract_lineage_id", "message_full_name", "field_number", "field_name"
    }:
        return None
    lineage = canonical.get("contract_lineage_id")
    message = canonical.get("message_full_name")
    number = canonical.get("field_number")
    if not isinstance(lineage, str) or not lineage or not isinstance(message, str) or not message:
        return None
    if not _is_int(number) or number <= 0:
        return None
    return lineage, message, str(number)


def expected_label_identity(label: Mapping[str, Any]) -> tuple[str, str, str]:
    return (
        str(label["expected_contract_lineage_id"]),
        str(label["expected_message_full_name"]),
        str(label["expected_field_number"]),
    )


def load_facts(
    path: Path, *, system: str, expected_commit: str, corpus_dir: Path
) -> list[dict[str, Any]]:
    records = read_jsonl(path)
    if not records:
        raise ValidationError(f"producer fact file is empty: {path}")
    seen_occurrences: set[tuple[Any, ...]] = set()
    blob_cache: dict[str, bytes] = {}
    required_strings = (
        "predicate", "subject", "object", "code_role", "tier", "system", "commit",
        "path", "rule_id", "atom_id", "schema_version", "blob_digest",
        "extractor_version", "adapter_config_digest", "fact_fingerprint", "assertion_id",
        "toolchain_identity",
    )
    for number, record in enumerate(records, 1):
        location = f"{path}:{number}"
        missing = [field for field in required_strings if not isinstance(record.get(field), str)]
        if missing:
            raise ValidationError(f"{location}: missing/non-string v3 fields {missing!r}")
        for field in ("start_byte", "end_byte", "start_line", "end_line"):
            if not _is_int(record.get(field)):
                raise ValidationError(f"{location}: {field} must be an integer")
        if record["system"] != system or record["commit"] != expected_commit:
            raise ValidationError(f"{location}: fact does not match locked system/commit")
        if record["schema_version"] != PRODUCER_SCHEMA_VERSION:
            raise ValidationError(f"{location}: producer schema is not current v3")
        if record["extractor_version"] != PRODUCER_EXTRACTOR_VERSION:
            raise ValidationError(f"{location}: unexpected extractor version")
        if not _sha256_string(record["blob_digest"]):
            raise ValidationError(f"{location}: invalid blob digest")
        if not _sha256_string(record["adapter_config_digest"]):
            raise ValidationError(f"{location}: invalid adapter config digest")
        type_derived = record["predicate"] in {
            "CALLS_OPERATION", "IMPLEMENTS_SERVICE", "REFERENCES_PROTO_FIELD",
            "SERVICE_REACHABLE_FROM_BINARY", "CALLER_REACHABLE_FROM_BINARY",
        }
        semantic_inputs = record.get("semantic_inputs_digest")
        if type_derived and not _sha256_string(semantic_inputs):
            raise ValidationError(
                f"{location}: type-derived fact lacks a valid semantic input digest"
            )
        if semantic_inputs is not None and not _sha256_string(semantic_inputs):
            raise ValidationError(f"{location}: invalid semantic input digest")
        if record["tier"] not in VALID_TIERS or record["code_role"] not in VALID_ROLES - {"unknown"}:
            raise ValidationError(f"{location}: invalid tier or code role")
        relative = str(record["path"])
        _safe_relative(relative)
        if relative not in blob_cache:
            blob_cache[relative] = git_blob(corpus_dir / system, expected_commit, relative)
        data = blob_cache[relative]
        if record["blob_digest"] != sha256_bytes(data):
            raise ValidationError(f"{location}: blob digest does not match pinned Git object")
        start, end = int(record["start_byte"]), int(record["end_byte"])
        if start < 0 or end <= start or end > len(data):
            raise ValidationError(f"{location}: byte span is outside pinned Git blob")
        expected_start_line = 1 + data[:start].count(b"\n")
        expected_end_line = 1 + data[:end].count(b"\n")
        if record["start_line"] != expected_start_line or record["end_line"] != expected_end_line:
            raise ValidationError(f"{location}: line span does not match byte coordinates")
        fingerprint = _fact_fingerprint(record)
        if record["fact_fingerprint"] != fingerprint:
            raise ValidationError(f"{location}: fact fingerprint cannot be reconstructed")
        if record["atom_id"] != _atom_id(record):
            raise ValidationError(f"{location}: atom ID cannot be reconstructed")
        if record["assertion_id"] != _assertion_id(record):
            raise ValidationError(f"{location}: assertion ID cannot be reconstructed")
        occurrence = (
            record["assertion_id"], record["atom_id"], record["system"], record["commit"],
            record["path"], start, end,
        )
        if occurrence in seen_occurrences:
            raise ValidationError(f"{location}: duplicate assertion/evidence occurrence")
        seen_occurrences.add(occurrence)
        if "canonical_field" in record and canonical_fact_identity(record) is None:
            raise ValidationError(f"{location}: malformed canonical_field")
    return records


def materialize_context(
    sites: Sequence[Mapping[str, Any]], corpus_dir: Path, destination: Path,
    locks: Mapping[str, Mapping[str, Any]], *, pad: int = 10,
) -> None:
    rows: list[dict[str, Any]] = []
    cache: dict[tuple[str, str], list[str]] = {}
    for site in sites:
        system, relative = str(site["system"]), str(site["path"])
        key = (system, relative)
        if key not in cache:
            data = git_blob(corpus_dir / system, str(locks[system]["commit"]), relative)
            cache[key] = data.decode("utf-8", errors="replace").splitlines()
        lines = cache[key]
        start = int(site.get("start_line") or 1)
        end = int(site.get("end_line") or start)
        lo, hi = max(0, start - 1 - pad), min(len(lines), end + pad)
        rows.append(
            {
                "site_id": site["site_id"],
                "context": "\n".join(
                    f"{index + 1:5d}| {lines[index]}" for index in range(lo, hi)
                ),
            }
        )
    atomic_write_jsonl(destination, rows)


def fact_occurrence_matches(candidate: Mapping[str, Any], fact: Mapping[str, Any]) -> bool:
    if candidate.get("system") != fact.get("system") or candidate.get("path") != fact.get("path"):
        return False
    try:
        return int(fact["start_byte"]) <= int(candidate["start_byte"]) and int(
            fact["end_byte"]
        ) >= int(candidate["end_byte"])
    except (KeyError, TypeError, ValueError):
        return False


def assertion_matches(identity: Mapping[str, Any], fact: Mapping[str, Any]) -> bool:
    return assertion_identity(fact, int(identity["gate"])) == dict(identity)


def _file_descriptor(path: Path, base: Path, *, records: int | None = None) -> dict[str, Any]:
    try:
        relative = path.resolve().relative_to(base.resolve()).as_posix()
        data = path.read_bytes()
    except (OSError, ValueError) as exc:
        raise ValidationError(f"cannot describe file {path}: {exc}") from exc
    result: dict[str, Any] = {"path": relative, "bytes": len(data), "sha256": sha256_bytes(data)}
    if records is not None:
        result["records"] = records
    return result


def _resolve_manifest_path(base: Path, relative: Any) -> Path:
    if not isinstance(relative, str):
        raise ValidationError("manifest path must be a string")
    _safe_relative(relative)
    path = (base / relative).resolve()
    try:
        path.relative_to(base.resolve())
    except ValueError as exc:
        raise ValidationError(f"manifest path escapes base: {relative!r}") from exc
    return path


def _verify_descriptor(base: Path, descriptor: Mapping[str, Any], *, jsonl: bool) -> Path:
    if set(descriptor) != ({"path", "bytes", "sha256", "records"} if jsonl else {"path", "bytes", "sha256"}):
        raise ValidationError(f"malformed file descriptor: {descriptor!r}")
    path = _resolve_manifest_path(base, descriptor["path"])
    try:
        data = path.read_bytes()
    except OSError as exc:
        raise ValidationError(f"cannot verify manifest file {path}: {exc}") from exc
    if len(data) != descriptor["bytes"] or sha256_bytes(data) != descriptor["sha256"]:
        raise ValidationError(f"manifest digest/length mismatch for {path}")
    if jsonl and len(read_jsonl(path)) != descriptor["records"]:
        raise ValidationError(f"manifest record count mismatch for {path}")
    return path


def summarize_sites(dev: Sequence[Mapping[str, Any]], holdout: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for split, records in (("dev", dev), ("holdout", holdout)):
        cohorts: defaultdict[str, int] = defaultdict(int)
        strata: defaultdict[str, int] = defaultdict(int)
        for site in records:
            cohorts[f"g{site['gate']}:{site['cohort']}"] += 1
            if site.get("population_stratum"):
                strata[str(site["population_stratum"])] += 1
        result[split] = {
            "records": len(records),
            "cohorts": dict(sorted(cohorts.items())),
            "sample_strata": dict(sorted(strata.items())),
        }
    return result


def producer_summary(fact_sets: Mapping[str, Sequence[Mapping[str, Any]]]) -> dict[str, Any]:
    schemas: set[str] = set()
    versions: set[str] = set()
    configs: set[str] = set()
    toolchains: set[str] = set()
    semantic_inputs: set[str] = set()
    field_refs = 0
    canonical_refs = 0
    pairwise = 0
    for facts in fact_sets.values():
        for fact in facts:
            schemas.add(str(fact["schema_version"]))
            versions.add(str(fact["extractor_version"]))
            configs.add(str(fact["adapter_config_digest"]))
            toolchains.add(str(fact["toolchain_identity"]))
            if fact.get("semantic_inputs_digest") is not None:
                semantic_inputs.add(str(fact["semantic_inputs_digest"]))
            if fact["predicate"] == "REFERENCES_PROTO_FIELD":
                field_refs += 1
                canonical_refs += canonical_fact_identity(fact) is not None
            if fact["predicate"] == "DEPLOYABLE_REACHES_OPERATION":
                pairwise += 1
    if schemas != {PRODUCER_SCHEMA_VERSION} or versions != {PRODUCER_EXTRACTOR_VERSION}:
        raise ValidationError("fact files do not share the required current producer schema/version")
    if len(configs) != 1:
        raise ValidationError("fact files were not produced by one adapter configuration")
    if len(toolchains) != 1:
        raise ValidationError("fact files were not produced by one exact toolchain")
    return {
        "schema_version": next(iter(schemas)),
        "extractor_version": next(iter(versions)),
        "adapter_config_digest": next(iter(configs)),
        "toolchain_identity": next(iter(toolchains)),
        "semantic_input_digests": sorted(semantic_inputs),
        "capabilities": {
            "canonical_field_identity": field_refs > 0 and canonical_refs == field_refs,
            "canonical_field_refs": canonical_refs,
            "field_refs": field_refs,
            "gate3_deployable_operation_pairs": pairwise,
            "gate3_pairwise_measurement": pairwise > 0,
        },
    }


def require_producer_capabilities(summary: Mapping[str, Any], mode: str) -> None:
    """Fail before any benchmark artifact is exposed for an incapable producer."""
    capabilities = summary.get("capabilities")
    if not isinstance(capabilities, dict):
        raise ValidationError("producer summary has no capability declaration")
    missing: list[str] = []
    if mode == "g34" and capabilities.get("canonical_field_identity") is not True:
        missing.append("canonical field identity on every field reference")
    pair_count = capabilities.get("gate3_deployable_operation_pairs")
    if (
        capabilities.get("gate3_pairwise_measurement") is not True
        or not _is_int(pair_count)
        or pair_count < MINIMUM_EVIDENCE["gate3_pairwise_pairs"]
    ):
        missing.append(
            f"at least {MINIMUM_EVIDENCE['gate3_pairwise_pairs']} "
            "deployable-to-operation pairs"
        )
    if mode not in {"g34", "h2"}:
        missing.append(f"known gate mode (got {mode!r})")
    if missing:
        raise ValidationError(
            "producer capabilities are insufficient for sealed generation: "
            + "; ".join(missing)
        )


def require_fresh_blind_population(burn_coordinate_count: int) -> None:
    if burn_coordinate_count:
        raise ValidationError(
            f"sealed gate generation cannot drop {burn_coordinate_count} previously exposed "
            "coordinates from the same pinned AC population; refresh the locked corpus or "
            "use a reviewed estimator that includes their labeled outcomes"
        )


def create_manifest(
    *, mode: str, base: Path, manifest_path: Path, lock_path: Path,
    locks: Mapping[str, Mapping[str, Any]], generator_config: Mapping[str, Any],
    generator_files: Sequence[Path], fact_files: Mapping[str, Path],
    fact_sets: Mapping[str, Sequence[Mapping[str, Any]]], artifact_files: Mapping[str, Path],
    artifact_records: Mapping[str, int], counts: Mapping[str, Any],
    burn_ledger_path: Path, burn_coordinate_count: int, burn_coordinates_sha256: str,
    corpus_verified: bool,
) -> dict[str, Any]:
    validated_config = validate_precommitted_generator_config(mode, generator_config)
    producer = producer_summary(fact_sets)
    require_producer_capabilities(producer, mode)
    require_fresh_blind_population(burn_coordinate_count)
    generators = [_file_descriptor(path, base) for path in generator_files]
    facts = {
        name: _file_descriptor(path, base, records=len(fact_sets[name]))
        for name, path in sorted(fact_files.items())
    }
    artifacts = {
        name: _file_descriptor(path, base, records=int(artifact_records[name]))
        for name, path in sorted(artifact_files.items())
    }
    ledger = _file_descriptor(burn_ledger_path, base)
    ledger.update(
        {"coordinate_count": burn_coordinate_count, "coordinates_sha256": burn_coordinates_sha256}
    )
    unsigned = {
        "schema_version": MANIFEST_SCHEMA_VERSION,
        "mode": mode,
        "benchmark_schema_version": SCHEMA_VERSION,
        "generator": {"config": validated_config, "files": generators},
        "corpus": {
            "lock": _file_descriptor(lock_path, base),
            "commits": {name: str(record["commit"]) for name, record in sorted(locks.items())},
            "verified_clean_before_and_after": corpus_verified,
        },
        "producer": producer,
        "fact_files": facts,
        "artifacts": artifacts,
        "counts": dict(counts),
        "thresholds": GATE_THRESHOLDS,
        "minimum_evidence": MINIMUM_EVIDENCE,
        "burn_ledger": ledger,
    }
    manifest = {**unsigned, "manifest_digest": digest_value(unsigned)}
    atomic_write_json(manifest_path, manifest)
    return manifest


def validate_manifest(
    path: Path, *, base: Path, mode: str, lock_path: Path,
    locks: Mapping[str, Mapping[str, Any]], artifact_names: set[str],
) -> dict[str, Any]:
    manifest = read_json(path)
    required = {
        "schema_version", "mode", "benchmark_schema_version", "generator", "corpus",
        "producer", "fact_files", "artifacts", "counts", "thresholds",
        "minimum_evidence", "burn_ledger", "manifest_digest",
    }
    if set(manifest) != required:
        raise ValidationError(
            f"manifest top-level fields differ; missing={sorted(required-set(manifest))!r}, "
            f"extra={sorted(set(manifest)-required)!r}"
        )
    unsigned = dict(manifest)
    supplied = unsigned.pop("manifest_digest")
    if supplied != digest_value(unsigned):
        raise ValidationError("manifest self-digest mismatch")
    if manifest["schema_version"] != MANIFEST_SCHEMA_VERSION or manifest["mode"] != mode:
        raise ValidationError("manifest schema/mode mismatch")
    if manifest["benchmark_schema_version"] != SCHEMA_VERSION:
        raise ValidationError("manifest benchmark schema mismatch")
    if manifest["thresholds"] != GATE_THRESHOLDS or manifest["minimum_evidence"] != MINIMUM_EVIDENCE:
        raise ValidationError("manifest changed predeclared thresholds/minimum evidence")
    corpus = manifest["corpus"]
    if not isinstance(corpus, dict) or set(corpus) != {
        "lock", "commits", "verified_clean_before_and_after"
    }:
        raise ValidationError("malformed manifest corpus section")
    _verify_descriptor(base, corpus["lock"], jsonl=False)
    if _resolve_manifest_path(base, corpus["lock"]["path"]) != lock_path.resolve():
        raise ValidationError("manifest is bound to a different corpus lock")
    expected_commits = {name: str(record["commit"]) for name, record in sorted(locks.items())}
    if corpus["commits"] != expected_commits:
        raise ValidationError("manifest corpus commits differ from corpus.lock.json")
    generator = manifest["generator"]
    if not isinstance(generator, dict) or set(generator) != {"config", "files"}:
        raise ValidationError("malformed manifest generator section")
    if not isinstance(generator["config"], dict) or not isinstance(generator["files"], list):
        raise ValidationError("malformed manifest generator configuration")
    upstream_digest: str | None = None
    if mode == "h2":
        upstream = validate_manifest(
            path.parent / "g34.v3.manifest.json",
            base=base,
            mode="g34",
            lock_path=lock_path,
            locks=locks,
            artifact_names={"dev_sites", "holdout_sites", "key", "label_template"},
        )
        upstream_digest = str(upstream["manifest_digest"])
    validate_precommitted_generator_config(
        mode,
        generator["config"],
        upstream_g34_manifest_digest=upstream_digest,
    )
    for descriptor in generator["files"]:
        _verify_descriptor(base, descriptor, jsonl=False)
    require_producer_capabilities(manifest["producer"], mode)
    facts = manifest["fact_files"]
    artifacts = manifest["artifacts"]
    if not isinstance(facts, dict) or not facts:
        raise ValidationError("manifest has no producer fact files")
    if not isinstance(artifacts, dict) or set(artifacts) != artifact_names:
        raise ValidationError("manifest artifact set is incomplete or unexpected")
    for descriptor in facts.values():
        _verify_descriptor(base, descriptor, jsonl=True)
    for descriptor in artifacts.values():
        _verify_descriptor(base, descriptor, jsonl=True)
    ledger = manifest["burn_ledger"]
    if not isinstance(ledger, dict) or set(ledger) != {
        "path", "bytes", "sha256", "coordinate_count", "coordinates_sha256"
    }:
        raise ValidationError("malformed manifest burn ledger section")
    _verify_descriptor(
        base, {name: ledger[name] for name in ("path", "bytes", "sha256")}, jsonl=False
    )
    require_fresh_blind_population(int(ledger["coordinate_count"]))
    return manifest


def _legacy_coordinate(record: Mapping[str, Any]) -> dict[str, Any] | None:
    system, path = record.get("system"), record.get("path")
    line = record.get("start_line", record.get("line"))
    if not isinstance(system, str) or not isinstance(path, str) or not _is_int(line):
        return None
    end_line = record.get("end_line", line)
    if not _is_int(end_line) or line <= 0 or end_line < line:
        raise ValidationError(f"bad legacy burn coordinate: {record!r}")
    return {"system": system, "path": path, "start_line": line, "end_line": end_line}


def _coordinate_digest(coordinates: Iterable[Mapping[str, Any]]) -> str:
    encoded = sorted({canonical_json(dict(coordinate)) for coordinate in coordinates})
    return sha256_bytes("".join(value + "\n" for value in encoded).encode("utf-8"))


def _load_legacy_burn_ledger(path: Path, ledger: Mapping[str, Any]) -> tuple[list[dict[str, Any]], str]:
    """Load the historical path-referencing v1 ledger."""

    ledger = read_json(path)
    required = {
        "schema_version", "projection", "sources", "coordinate_count",
        "coordinates_sha256", "ledger_digest",
    }
    if set(ledger) != required:
        raise ValidationError("burn ledger fields are incomplete or unexpected")
    unsigned = dict(ledger)
    supplied = unsigned.pop("ledger_digest")
    if supplied != digest_value(unsigned):
        raise ValidationError("burn ledger self-digest mismatch")
    if ledger["schema_version"] != LEGACY_BURN_LEDGER_SCHEMA_VERSION:
        raise ValidationError("unsupported burn ledger schema")
    sources = ledger["sources"]
    if not isinstance(sources, list) or not sources:
        raise ValidationError("burn ledger has no sources")
    coordinates: list[dict[str, Any]] = []
    for descriptor in sources:
        if not isinstance(descriptor, dict) or set(descriptor) != {"path", "records", "sha256"}:
            raise ValidationError("malformed burn source descriptor")
        source = path.parent / str(descriptor["path"])
        rows = read_jsonl(source)
        if len(rows) != descriptor["records"] or sha256_file(source) != descriptor["sha256"]:
            raise ValidationError(f"legacy burn source was truncated or changed: {source}")
        coordinates.extend(filter(None, (_legacy_coordinate(row) for row in rows)))
    unique = {canonical_json(row): row for row in coordinates}
    result = [unique[key] for key in sorted(unique)]
    coordinate_digest = _coordinate_digest(result)
    if len(result) != ledger["coordinate_count"] or coordinate_digest != ledger["coordinates_sha256"]:
        raise ValidationError("burn coordinate projection does not match committed ledger")
    return result, coordinate_digest


def _burn_cohort_coordinate(record: Mapping[str, Any]) -> dict[str, Any]:
    if set(record) != {"system", "path", "start_line", "end_line"}:
        raise ValidationError(f"bad burn cohort coordinate fields: {record!r}")
    coordinate = _legacy_coordinate(record)
    if coordinate is None:
        raise ValidationError(f"bad burn cohort coordinate: {record!r}")
    return coordinate


def _burn_source_artifacts(
    cohort_id: str, raw_artifacts: Any,
) -> list[dict[str, Any]]:
    """Validate and defensively normalize burn-cohort artifact provenance."""

    if not isinstance(raw_artifacts, list):
        raise ValidationError(f"{cohort_id}: invalid source artifact provenance")
    normalized: list[dict[str, Any]] = []
    for item in raw_artifacts:
        if not (
            isinstance(item, dict)
            and set(item) == {"path", "records", "sha256"}
            and isinstance(item["path"], str)
            and _is_int(item["records"])
            and item["records"] >= 0
            and isinstance(item["sha256"], str)
            and re.fullmatch(r"sha256:[0-9a-f]{64}", item["sha256"])
        ):
            raise ValidationError(f"{cohort_id}: invalid source artifact provenance")
        normalized.append(
            {
                "path": item["path"],
                "records": item["records"],
                "sha256": item["sha256"],
            }
        )
    return normalized


def build_burn_ledger(cohorts: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    """Build a canonical, self-digested v2 burn-ledger document."""

    normalized: list[dict[str, Any]] = []
    projection: dict[str, dict[str, Any]] = {}
    cohort_ids: set[str] = set()
    for raw in cohorts:
        cohort_id = raw.get("cohort_id")
        if not isinstance(cohort_id, str) or not cohort_id or cohort_id in cohort_ids:
            raise ValidationError("burn cohort IDs must be unique nonempty strings")
        cohort_ids.add(cohort_id)
        input_binding = raw.get("input_binding")
        if input_binding is not None and not (
            isinstance(input_binding, str)
            and re.fullmatch(r"sha256:[0-9a-f]{64}", input_binding)
        ):
            raise ValidationError(f"{cohort_id}: invalid input binding")
        source_commits = raw.get("source_commits")
        if (
            not isinstance(source_commits, dict)
            or not source_commits
            or not all(
                isinstance(system, str)
                and system
                and isinstance(commit, str)
                and re.fullmatch(r"[0-9a-f]{40}", commit)
                for system, commit in source_commits.items()
            )
        ):
            raise ValidationError(f"{cohort_id}: invalid source commits")
        source_artifacts = _burn_source_artifacts(
            cohort_id, raw.get("source_artifacts", [])
        )
        coordinates_by_key: dict[str, dict[str, Any]] = {}
        raw_coordinates = raw.get("coordinates")
        if not isinstance(raw_coordinates, Sequence) or isinstance(
            raw_coordinates, (str, bytes)
        ):
            raise ValidationError(f"{cohort_id}: burn cohort has no coordinates")
        for item in raw_coordinates:
            if not isinstance(item, Mapping):
                raise ValidationError(f"{cohort_id}: malformed coordinate")
            coordinate = _burn_cohort_coordinate(item)
            if coordinate["system"] not in source_commits:
                raise ValidationError(
                    f"{cohort_id}: coordinate system has no source commit: "
                    f"{coordinate['system']}"
                )
            coordinates_by_key[canonical_json(coordinate)] = coordinate
        coordinates = [coordinates_by_key[key] for key in sorted(coordinates_by_key)]
        if not coordinates:
            raise ValidationError(f"{cohort_id}: burn cohort has no coordinates")
        digest = _coordinate_digest(coordinates)
        for coordinate in coordinates:
            projection[canonical_json(coordinate)] = coordinate
        normalized.append(
            {
                "cohort_id": cohort_id,
                "input_binding": input_binding,
                "source_commits": dict(sorted(source_commits.items())),
                "source_artifacts": source_artifacts,
                "coordinates": coordinates,
                "coordinate_count": len(coordinates),
                "coordinates_sha256": digest,
            }
        )
    if not normalized:
        raise ValidationError("burn ledger has no cohorts")
    normalized.sort(key=lambda row: row["cohort_id"])
    projected = [projection[key] for key in sorted(projection)]
    unsigned = {
        "schema_version": BURN_LEDGER_SCHEMA_VERSION,
        "projection": (
            "system,path,start_line,end_line; disclosed labeler coordinates; "
            "predicates and values deliberately ignored"
        ),
        "cohorts": normalized,
        "coordinate_count": len(projected),
        "coordinates_sha256": _coordinate_digest(projected),
    }
    return {**unsigned, "ledger_digest": digest_value(unsigned)}


def load_burn_ledger_cohorts(path: Path) -> tuple[list[dict[str, Any]], str]:
    """Load self-contained v2 burn cohorts with source-commit provenance."""

    ledger = read_json(path)
    required = {
        "schema_version", "projection", "cohorts", "coordinate_count",
        "coordinates_sha256", "ledger_digest",
    }
    if set(ledger) != required:
        raise ValidationError("burn ledger fields are incomplete or unexpected")
    unsigned = dict(ledger)
    supplied = unsigned.pop("ledger_digest")
    if supplied != digest_value(unsigned):
        raise ValidationError("burn ledger self-digest mismatch")
    if ledger["schema_version"] != BURN_LEDGER_SCHEMA_VERSION:
        raise ValidationError("unsupported burn ledger schema")
    raw_cohorts = ledger["cohorts"]
    if not isinstance(raw_cohorts, list) or not raw_cohorts:
        raise ValidationError("burn ledger has no cohorts")
    cohorts: list[dict[str, Any]] = []
    cohort_ids: set[str] = set()
    projection: dict[str, dict[str, Any]] = {}
    for raw in raw_cohorts:
        if not isinstance(raw, dict) or set(raw) != {
            "cohort_id", "input_binding", "source_commits", "source_artifacts",
            "coordinates", "coordinate_count", "coordinates_sha256",
        }:
            raise ValidationError("malformed burn cohort")
        cohort_id = raw["cohort_id"]
        if not isinstance(cohort_id, str) or not cohort_id or cohort_id in cohort_ids:
            raise ValidationError("burn cohort IDs must be unique nonempty strings")
        cohort_ids.add(cohort_id)
        input_binding = raw["input_binding"]
        if input_binding is not None and not (
            isinstance(input_binding, str)
            and re.fullmatch(r"sha256:[0-9a-f]{64}", input_binding)
        ):
            raise ValidationError(f"{cohort_id}: invalid input binding")
        source_commits = raw["source_commits"]
        if (
            not isinstance(source_commits, dict)
            or not source_commits
            or not all(
                isinstance(system, str)
                and system
                and isinstance(commit, str)
                and re.fullmatch(r"[0-9a-f]{40}", commit)
                for system, commit in source_commits.items()
            )
        ):
            raise ValidationError(f"{cohort_id}: invalid source commits")
        source_artifacts = _burn_source_artifacts(cohort_id, raw["source_artifacts"])
        raw_coordinates = raw["coordinates"]
        if not isinstance(raw_coordinates, list) or not raw_coordinates:
            raise ValidationError(f"{cohort_id}: burn cohort has no coordinates")
        unique: dict[str, dict[str, Any]] = {}
        for item in raw_coordinates:
            if not isinstance(item, dict):
                raise ValidationError(f"{cohort_id}: malformed coordinate")
            coordinate = _burn_cohort_coordinate(item)
            if coordinate["system"] not in source_commits:
                raise ValidationError(
                    f"{cohort_id}: coordinate system has no source commit: "
                    f"{coordinate['system']}"
                )
            unique[canonical_json(coordinate)] = coordinate
        coordinates = [unique[key] for key in sorted(unique)]
        digest = _coordinate_digest(coordinates)
        if (
            len(coordinates) != raw["coordinate_count"]
            or digest != raw["coordinates_sha256"]
        ):
            raise ValidationError(f"{cohort_id}: coordinate projection mismatch")
        for coordinate in coordinates:
            projection[canonical_json(coordinate)] = coordinate
        cohorts.append(
            {
                "cohort_id": cohort_id,
                "input_binding": input_binding,
                "source_commits": dict(sorted(source_commits.items())),
                "source_artifacts": source_artifacts,
                "coordinates": coordinates,
                "coordinate_count": len(coordinates),
                "coordinates_sha256": digest,
            }
        )
    projected = [projection[key] for key in sorted(projection)]
    projected_digest = _coordinate_digest(projected)
    if (
        len(projected) != ledger["coordinate_count"]
        or projected_digest != ledger["coordinates_sha256"]
    ):
        raise ValidationError("burn coordinate projection does not match committed ledger")
    return cohorts, projected_digest


def load_burn_ledger(path: Path) -> tuple[list[dict[str, Any]], str]:
    """Load either historical v1 or append-only self-contained v2 burn history."""

    ledger = read_json(path)
    if ledger.get("schema_version") == LEGACY_BURN_LEDGER_SCHEMA_VERSION:
        return _load_legacy_burn_ledger(path, ledger)
    cohorts, digest = load_burn_ledger_cohorts(path)
    projection: dict[str, dict[str, Any]] = {}
    for cohort in cohorts:
        for coordinate in cohort["coordinates"]:
            projection[canonical_json(coordinate)] = coordinate
    return [projection[key] for key in sorted(projection)], digest


def coordinate_for(record: Mapping[str, Any]) -> dict[str, Any]:
    return {
        "system": str(record["system"]), "path": str(record["path"]),
        "start_line": int(record["start_line"]), "end_line": int(record["end_line"]),
    }


def coordinate_burned(record: Mapping[str, Any], burned: Sequence[Mapping[str, Any]]) -> bool:
    candidate = coordinate_for(record)
    return any(
        candidate["system"] == prior["system"]
        and candidate["path"] == prior["path"]
        and candidate["start_line"] <= int(prior["end_line"])
        and candidate["end_line"] >= int(prior["start_line"])
        for prior in burned
    )


def canonical_counts(records: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    by_system: defaultdict[str, int] = defaultdict(int)
    by_stratum: defaultdict[str, int] = defaultdict(int)
    for record in records:
        by_system[str(record["system"])] += 1
        if record.get("population_stratum"):
            by_stratum[str(record["population_stratum"])] += 1
    return {
        "records": len(records), "by_system": dict(sorted(by_system.items())),
        "by_stratum": dict(sorted(by_stratum.items())),
    }


def clopper_pearson_lower(successes: int, trials: int, alpha: float = 0.05) -> float | None:
    """Exact one-sided binomial lower confidence bound (stdlib bisection)."""

    if trials <= 0:
        return None
    if successes <= 0:
        return 0.0
    if successes > trials or not 0.0 < alpha < 1.0:
        raise ValidationError("invalid binomial confidence-bound inputs")

    def upper_tail(probability: float) -> float:
        return sum(
            math.comb(trials, value)
            * probability**value
            * (1.0 - probability) ** (trials - value)
            for value in range(successes, trials + 1)
        )

    low, high = 0.0, 1.0
    for _ in range(80):
        mid = (low + high) / 2.0
        if upper_tail(mid) < alpha:
            low = mid
        else:
            high = mid
    return high
