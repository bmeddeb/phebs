#!/usr/bin/env python3
"""Score the Gate 2 stratified holdout with fail-closed design bounds.

Only ``holdout`` can establish Gate 2.  Development labels are diagnostics.
The one-sided confidence bounds invert the exact hypergeometric distribution
inside every finite-population stratum and use Bonferroni simultaneous bounds:

* precision lower-bounds correct emitted facts in every stratum;
* recall lower-bounds correctly extracted eligible sites and upper-bounds
  missed eligible sites, then takes A_lower/(A_lower + B_upper).

Legacy case-control artifacts have no frame sizes or inclusion probabilities
and are deliberately rejected instead of reporting unweighted sample recall.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import math
import re
import sys
import tempfile
from collections import defaultdict
from fractions import Fraction
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Sequence

from label_prep import (
    FRAMES,
    FRAME_CALL_PRECISION,
    FRAME_CALL_RECALL,
    FRAME_REGISTRATION_PRECISION,
    FRAME_REGISTRATION_RECALL,
    FRAME_ROLE,
    DEFAULT_BURN_LEDGER,
    DEFAULT_CORPUS,
    DEFAULT_LOCK,
    GATE_CONFIDENCE,
    GATE_FIXTURE_PRECISION_THRESHOLD,
    GATE_PRECISION_THRESHOLD,
    GATE_RECALL_THRESHOLD,
    LEGACY_DIAGNOSTIC_SEED,
    MIN_HARD_NEGATIVES,
    MIN_POSITIVES,
    MIN_BLIND_HOLDOUT_FRACTION,
    PrepError,
    SYSTEMS,
    build_artifacts,
    canonical_json,
    confidence_bound_family_size,
    gate_sampling_config,
    gate_decision_rule,
    load_corpus_entries,
    pinned_tracked_files,
    preflight_gate_design,
    scoped_burn_binding,
    validate_attempt_claim,
    validate_seed_record,
)
from gate34_common import (
    ValidationError as EvidenceValidationError,
    load_burn_ledger_cohorts,
)
from label_protocol import (
    LabelProtocolError,
    canonical_label_commitment_document,
    github_json_fetcher,
    sha256_bytes as protocol_sha256_bytes,
    validate_github_gist_receipt,
    validate_label_commitment,
    verify_github_initial_gist_receipt,
    verify_label_commitment,
)
from label_adjudication import AdjudicationError, validate_assignment_manifest


BASE = Path(__file__).resolve().parent
DEFAULT_ARTIFACTS = BASE / "labeling" / "g2-v3"
DEFAULT_FACTS = BASE / "out"
SCHEMA = "t111-gate2-probability-sample-v3"
ALLOWED_ROLES = {"production", "test", "mock", "generated", "vendor"}
LABEL_FIELDS = {
    "site_id",
    "invocation",
    "operation",
    "registration",
    "service",
    "expected_code_role",
    "rationale",
    "evidence",
}
OPERATION_RE = re.compile(
    r"^/?[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+"
    r"/[A-Za-z_][A-Za-z0-9_]*$"
)
SERVICE_RE = re.compile(
    r"^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$"
)


class ScoreError(RuntimeError):
    pass


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    try:
        handle = path.open(encoding="utf-8")
    except OSError as exc:
        raise ScoreError(f"cannot read {path}: {exc}") from exc
    with handle:
        for line_number, line in enumerate(handle, 1):
            if not line.strip():
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ScoreError(f"{path}:{line_number}: invalid JSON: {exc}") from exc
            if not isinstance(row, dict):
                raise ScoreError(f"{path}:{line_number}: expected a JSON object")
            rows.append(row)
    return rows


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    try:
        with path.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                h.update(chunk)
    except OSError as exc:
        raise ScoreError(f"cannot hash {path}: {exc}") from exc
    return h.hexdigest()


def validate_burn_binding(
    manifest: dict[str, Any], path: Path, commits: dict[str, str],
    corpus_dir: Path = DEFAULT_CORPUS,
) -> dict[str, Any]:
    """Require exact ledger bytes and exact commit/source-scoped activation."""

    try:
        _, expected, _ = scoped_burn_binding(path, commits, corpus_dir)
    except (PrepError, EvidenceValidationError) as exc:
        raise ScoreError(f"burn-ledger verification failed: {exc}") from exc
    if manifest.get("burn_ledger") != expected:
        raise ScoreError("manifest is not bound to the exact current burn ledger")
    return expected


def validate_current_burn_superset(snapshot: Path, current: Path) -> None:
    """Require the live append-only ledger to retain every input cohort."""

    try:
        snapshot_cohorts, _ = load_burn_ledger_cohorts(snapshot)
        current_cohorts, _ = load_burn_ledger_cohorts(current)
    except EvidenceValidationError as exc:
        raise ScoreError(f"burn-ledger cohort verification failed: {exc}") from exc
    current_by_id = {
        str(cohort["cohort_id"]): cohort for cohort in current_cohorts
    }
    for cohort in snapshot_cohorts:
        cohort_id = str(cohort["cohort_id"])
        if current_by_id.get(cohort_id) != cohort:
            raise ScoreError(
                f"current burn ledger does not retain input cohort {cohort_id}"
            )


def unique_by_id(rows: Sequence[dict[str, Any]], name: str) -> dict[str, dict[str, Any]]:
    result: dict[str, dict[str, Any]] = {}
    for row in rows:
        sid = row.get("site_id")
        if not isinstance(sid, str) or not sid:
            raise ScoreError(f"{name}: missing site_id")
        if sid in result:
            raise ScoreError(f"{name}: duplicate site_id {sid}")
        result[sid] = row
    return result


def validate_sealed_artifact_set(
    artifact_dir: Path, payload_paths: dict[str, Path]
) -> None:
    """Reject missing, extra, symlinked, and non-regular sealed entries."""

    try:
        children = list(artifact_dir.iterdir())
    except OSError as exc:
        raise ScoreError(f"cannot enumerate sealed artifact directory: {exc}") from exc
    expected_names = set(payload_paths) | {"manifest.json"}
    if {path.name for path in children} != expected_names:
        raise ScoreError("sealed artifact directory has a missing or extra entry")
    if any(path.is_symlink() or not path.is_file() for path in children):
        raise ScoreError("sealed artifact directory contains a non-regular entry")


def load_bundle(
    artifact_dir: Path, corpus_dir: Path, corpus_lock: Path, facts_dir: Path
) -> dict[str, Any]:
    manifest_path = artifact_dir / "manifest.json"
    if artifact_dir.is_symlink():
        raise ScoreError(f"sealed artifact directory may not be a symlink: {artifact_dir}")
    if manifest_path.is_symlink() or not manifest_path.is_file():
        raise ScoreError(
            f"{manifest_path} is missing. Legacy Gate 2 artifacts are a case-control sample "
            "without inclusion probabilities and cannot establish population recall; rerun "
            "label_prep.py prepare into a fresh artifact directory."
        )
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ScoreError(f"cannot load {manifest_path}: {exc}") from exc
    if not isinstance(manifest, dict):
        raise ScoreError(f"{manifest_path}: expected a JSON object")
    if manifest.get("schema") != SCHEMA:
        raise ScoreError(f"unsupported artifact schema {manifest.get('schema')!r}; expected {SCHEMA}")
    if manifest.get("coordinate_only_sites") is not True:
        raise ScoreError("artifact does not assert coordinate-only labeler sites")
    systems = manifest.get("systems")
    if (
        not isinstance(systems, list)
        or not systems
        or len(systems) != len(set(systems))
        or not all(isinstance(x, str) and x in SYSTEMS for x in systems)
    ):
        raise ScoreError("manifest has no valid system list")
    try:
        entries = load_corpus_entries(corpus_lock, systems)
        locked = {system: str(entries[system]["commit"]) for system in systems}
        burn_ledger_path = (
            artifact_dir / "burn-ledger.input.json"
            if manifest.get("gate_design_fixed") is True
            else DEFAULT_BURN_LEDGER
        )
        validate_burn_binding(manifest, burn_ledger_path, locked, corpus_dir)
        if manifest.get("gate_design_fixed") is True:
            validate_current_burn_superset(burn_ledger_path, DEFAULT_BURN_LEDGER)
        provenance = manifest.get("provenance")
        if not isinstance(provenance, dict):
            raise ScoreError("manifest has no provenance")
        recorded = provenance.get("locked_commits")
        expected = {system: locked[system] for system in systems}
        if recorded != expected:
            raise ScoreError("artifact locked commits do not match the current corpus.lock")
        if provenance.get("corpus_lock_sha256") != sha256_file(corpus_lock):
            raise ScoreError("artifact corpus.lock digest does not match the current lock")
        prep_path = BASE / "label_prep.py"
        if provenance.get("prep_script_sha256") != sha256_file(prep_path):
            raise ScoreError("artifact prep-script digest does not match the current label_prep.py")
        verifier_path = BASE / "gate34_common.py"
        if provenance.get("fact_verifier_sha256") != sha256_file(verifier_path):
            raise ScoreError("artifact evidence-verifier digest does not match the current verifier")
        recorded_facts = provenance.get("facts_sha256")
        expected_fact_names = {f"{system}.facts.jsonl" for system in systems}
        if not isinstance(recorded_facts, dict) or set(recorded_facts) != expected_fact_names:
            raise ScoreError("artifact has an incomplete facts provenance set")
        for name in sorted(expected_fact_names):
            fact_path = facts_dir / name
            if not fact_path.is_file() or recorded_facts[name] != sha256_file(fact_path):
                raise ScoreError(f"extractor fact digest mismatch: {fact_path}")
        for system in systems:
            pinned_tracked_files(
                corpus_dir / system,
                locked[system],
                entries[system]["excluded_gitlinks"],
            )
        expected_inputs = {
            system: {
                "commit": locked[system],
                "excluded_gitlinks": entries[system]["excluded_gitlinks"],
                "go_build_tags": entries[system]["go_build_tags"],
            }
            for system in systems
        }
        if provenance.get("corpus_inputs") != expected_inputs:
            raise ScoreError("artifact corpus input policy does not match corpus.lock")
    except PrepError as exc:
        raise ScoreError(f"corpus preflight failed: {exc}") from exc

    paths = {
        "sites.census.jsonl": artifact_dir / "sites.census.jsonl",
        "sites.dev.jsonl": artifact_dir / "sites.dev.jsonl",
        "sites.holdout.jsonl": artifact_dir / "sites.holdout.jsonl",
        "key.jsonl": artifact_dir / "key.jsonl",
    }
    sealed = manifest.get("gate_design_fixed") is True
    seed_record: dict[str, Any] | None = None
    if sealed:
        paths["seed.json"] = artifact_dir / "seed.json"
        paths["attempt.json"] = artifact_dir / "attempt.json"
        paths["burn-ledger.input.json"] = artifact_dir / "burn-ledger.input.json"
    expected_hashes = manifest.get("artifacts_sha256")
    if not isinstance(expected_hashes, dict):
        raise ScoreError("manifest is missing artifact digests")
    if sealed and set(expected_hashes) != set(paths):
        raise ScoreError("sealed artifact digest set is incomplete or contains extras")
    for name, path in paths.items():
        if path.is_symlink() or not path.is_file():
            raise ScoreError(f"artifact must be a regular non-symlink file: {path}")
        if expected_hashes.get(name) != sha256_file(path):
            raise ScoreError(f"artifact digest mismatch: {path}")
    if sealed and manifest.get("artifact_set") != sorted(paths):
        raise ScoreError("sealed artifact set is incomplete or contains extras")
    if sealed:
        validate_sealed_artifact_set(artifact_dir, paths)
        randomization = manifest.get("randomization")
        if not isinstance(randomization, dict):
            raise ScoreError("sealed manifest has no randomization record")
        input_binding = randomization.get("input_binding")
        if not isinstance(input_binding, str) or not re.fullmatch(
            r"sha256:[0-9a-f]{64}", input_binding
        ):
            raise ScoreError("sealed manifest has no valid input binding")
        try:
            seed_record = validate_seed_record(
                json.loads(paths["seed.json"].read_text(encoding="utf-8")),
                expected_input_binding=input_binding,
                expected_input_committed_at=randomization.get("input_committed_at"),
            )
        except (OSError, json.JSONDecodeError, PrepError) as exc:
            raise ScoreError(f"invalid sealed audit seed: {exc}") from exc
        if randomization.get("seed_record") != seed_record:
            raise ScoreError("manifest audit seed does not match sealed seed.json")
        try:
            attempt_claim = validate_attempt_claim(
                json.loads(paths["attempt.json"].read_text(encoding="utf-8")),
                commits={str(k): str(v) for k, v in provenance["locked_commits"].items()},
                base_input_binding=str(randomization.get("base_input_binding")),
                input_binding=input_binding,
            )
        except (OSError, json.JSONDecodeError, KeyError, TypeError, PrepError) as exc:
            raise ScoreError(f"invalid sealed attempt claim: {exc}") from exc
        if manifest.get("attempt_claim") != attempt_claim:
            raise ScoreError("manifest attempt claim does not match sealed attempt.json")
        if attempt_claim["committed_at"] != seed_record["input_committed_at"]:
            raise ScoreError("attempt claim and audit seed timestamps differ")

    census = unique_by_id(load_jsonl(paths["sites.census.jsonl"]), "sites.census")
    dev = unique_by_id(load_jsonl(paths["sites.dev.jsonl"]), "sites.dev")
    holdout = unique_by_id(load_jsonl(paths["sites.holdout.jsonl"]), "sites.holdout")
    key = unique_by_id(load_jsonl(paths["key.jsonl"]), "key")
    overlap = (set(dev) & set(holdout)) | (set(dev) & set(census)) | (set(holdout) & set(census))
    if overlap:
        raise ScoreError(f"census/dev/holdout ID leakage: {sorted(overlap)[:3]}")
    if set(key) != set(census) | set(dev) | set(holdout):
        raise ScoreError("key IDs do not equal the union of site IDs")

    allowed = {"site_id", "system", "path", "line", "column", "method"}
    coordinates: dict[tuple[Any, ...], tuple[str, str]] = {}
    for split, sites in (("census", census), ("dev", dev), ("holdout", holdout)):
        for sid, site in sites.items():
            if set(site) != allowed:
                raise ScoreError(f"{split}/{sid}: labeler site must be coordinate-only")
            coordinate = tuple(site[field] for field in ("system", "path", "line", "column", "method"))
            prior = coordinates.setdefault(coordinate, (split, sid))
            if prior != (split, sid):
                raise ScoreError(f"duplicate coordinate or split leakage: {coordinate}")
            hidden = key[sid]
            if hidden.get("split") != split:
                raise ScoreError(f"{sid}: key split mismatch")
            for field in ("system", "path", "line", "column", "method"):
                if hidden.get(field) != site[field]:
                    raise ScoreError(f"{sid}: key/site {field} mismatch")

    strata: dict[tuple[str, str], dict[str, Any]] = {}
    for frame in FRAMES:
        frame_manifest = manifest.get("frames", {}).get(frame)
        if not isinstance(frame_manifest, dict):
            raise ScoreError(f"manifest missing {frame} frame")
        for row in frame_manifest.get("strata", []):
            stratum = row.get("id")
            if not isinstance(stratum, str) or (frame, stratum) in strata:
                raise ScoreError(f"duplicate/invalid {frame} stratum {stratum!r}")
            n_population = row.get("population_size")
            n_sample_population = row.get("sample_population_size")
            n_census = row.get("census_size")
            n_holdout = row.get("holdout_sample_size")
            probability = row.get("holdout_inclusion_probability")
            if not isinstance(n_population, int) or n_population <= 0:
                raise ScoreError(f"{frame}/{stratum}: invalid population size")
            if (
                not isinstance(n_sample_population, int)
                or not isinstance(n_census, int)
                or n_sample_population < 0
                or n_census < 0
                or n_sample_population + n_census != n_population
                or row.get("census_inclusion_probability") != 1
            ):
                raise ScoreError(f"{frame}/{stratum}: invalid census partition")
            if not isinstance(n_holdout, int) or not 0 <= n_holdout <= n_sample_population:
                raise ScoreError(f"{frame}/{stratum}: invalid holdout sample size")
            expected_probability = n_holdout / n_sample_population if n_sample_population else 0
            if not isinstance(probability, (int, float)) or not math.isclose(
                probability, expected_probability, rel_tol=0, abs_tol=1e-15
            ):
                raise ScoreError(f"{frame}/{stratum}: invalid inclusion probability")
            strata[(frame, stratum)] = row

    observed: dict[tuple[str, str, str], int] = defaultdict(int)
    for sid, row in key.items():
        frames = row.get("frames")
        if not isinstance(frames, dict) or not frames:
            raise ScoreError(f"{sid}: missing frame membership")
        for frame, membership in frames.items():
            if frame not in FRAMES or not isinstance(membership, dict):
                raise ScoreError(f"{sid}: invalid frame membership {frame}")
            stratum = membership.get("stratum")
            if (frame, stratum) not in strata:
                raise ScoreError(f"{sid}: unknown {frame} stratum {stratum}")
            observed[(frame, stratum, row["split"])] += 1
    for (frame, stratum), row in strata.items():
        for split, field in (
            ("census", "census_size"),
            ("holdout", "holdout_sample_size"),
            ("dev", "dev_sample_size"),
        ):
            if observed[(frame, stratum, split)] != row.get(field):
                raise ScoreError(f"{frame}/{stratum}: {split} membership count mismatch")

    holdout_meta = manifest.get("holdout")
    if not isinstance(holdout_meta, dict):
        raise ScoreError("manifest has no holdout design")
    selected_total = len(census) + len(dev) + len(holdout)
    blind_fraction = len(holdout) / selected_total if selected_total else 0.0
    if (
        holdout_meta.get("unique_dev_sites") != len(dev)
        or holdout_meta.get("unique_holdout_sites") != len(holdout)
        or holdout_meta.get("unique_census_sites") != len(census)
        or not isinstance(holdout_meta.get("blind_fraction"), (int, float))
        or not math.isclose(
            float(holdout_meta["blind_fraction"]), blind_fraction, rel_tol=0, abs_tol=1e-15
        )
    ):
        raise ScoreError("manifest holdout cardinalities do not match the artifacts")

    config = manifest.get("sampling_config")
    required_config = set(gate_sampling_config())
    if not isinstance(config, dict) or set(config) != required_config:
        raise ScoreError("manifest has no complete sampling configuration")
    if not all(isinstance(config[name], int) and config[name] >= 0 for name in config):
        raise ScoreError("manifest sampling configuration is invalid")

    # Re-enumerate both populations and rerun deterministic selection from the
    # current pinned Git objects and extractor facts.  Self-consistent manifest
    # edits are therefore insufficient to alter N, n, pi, or verdicts.
    rebuild_args = SimpleNamespace(
        systems=",".join(systems),
        seed=LEGACY_DIAGNOSTIC_SEED,
        audit_seed_file=paths.get("seed.json"),
        force=False,
        corpus_dir=corpus_dir,
        corpus_lock=corpus_lock,
        facts_dir=facts_dir,
        burn_ledger=paths.get("burn-ledger.input.json", DEFAULT_BURN_LEDGER),
        rebuild_input_committed_at=(
            seed_record["input_committed_at"] if seed_record is not None else None
        ),
        rebuild_attempt_claim=manifest.get("attempt_claim"),
        **config,
    )
    try:
        rebuilt_manifest, rebuilt_dev, rebuilt_holdout, rebuilt_key = build_artifacts(
            rebuild_args
        )
    except PrepError as exc:
        raise ScoreError(f"frame recomputation failed: {exc}") from exc
    recorded_without_hashes = dict(manifest)
    recorded_without_hashes.pop("artifacts_sha256", None)
    recorded_without_hashes.pop("artifact_set", None)
    if canonical_json(recorded_without_hashes) != canonical_json(rebuilt_manifest):
        raise ScoreError("manifest does not match recomputed frames and selection")
    if rebuilt_dev != list(dev.values()) or rebuilt_holdout != list(holdout.values()):
        raise ScoreError("coordinate artifacts do not match recomputed selection")
    rebuilt_census = [
        {field: row[field] for field in ("site_id", "system", "path", "line", "column", "method")}
        for row in rebuilt_key
        if row.get("split") == "census"
    ]
    if rebuilt_census != list(census.values()):
        raise ScoreError("census coordinates do not match recomputed burn carry-forward")
    if rebuilt_key != list(key.values()):
        raise ScoreError("hidden key does not match recomputed extractor outcomes")

    return {
        "manifest": manifest,
        "census": census,
        "dev": dev,
        "holdout": holdout,
        "key": key,
        "strata": strata,
        "blind_fraction": blind_fraction,
    }


def norm_op(operation: Any) -> str | None:
    if not isinstance(operation, str):
        return None
    value = operation.strip()
    if not OPERATION_RE.fullmatch(value) or "?" in value:
        return None
    return value.lstrip("/")


def op_match(fact_object: Any, label_operation: Any) -> bool:
    fact = norm_op(fact_object)
    label = norm_op(label_operation)
    return fact is not None and fact == label


def norm_service(service: Any) -> str | None:
    if not isinstance(service, str):
        return None
    value = service.strip().lstrip("/")
    return value if SERVICE_RE.fullmatch(value) else None


def service_match(fact_object: Any, label_service: Any) -> bool:
    fact = norm_service(fact_object)
    label = norm_service(label_service)
    return fact is not None and fact == label


def hypergeom_probability(
    N: int, K: int, n: int, low: int, high: int
) -> Fraction:
    """Exact hypergeometric interval probability using integer arithmetic."""

    if not 0 <= K <= N or not 0 <= n <= N:
        raise ValueError(f"invalid hypergeometric parameters N={N}, K={K}, n={n}")
    low = max(low, 0, n - (N - K))
    high = min(high, n, K)
    if low > high:
        return Fraction(0, 1)
    denominator = math.comb(N, n)
    numerator = sum(
        math.comb(K, x) * math.comb(N - K, n - x)
        for x in range(low, high + 1)
    )
    return Fraction(numerator, denominator)


def lower_population_total(N: int, n: int, k: int, alpha: Fraction) -> int:
    """Exact one-sided lower confidence bound for K successes among N."""
    if n == 0 or k == 0:
        return 0
    if n == N:
        return k
    lo, hi = k, N - (n - k)
    while lo < hi:
        mid = (lo + hi) // 2
        tail = hypergeom_probability(N, mid, n, k, n)
        if tail >= alpha:
            hi = mid
        else:
            lo = mid + 1
    return lo


def upper_population_total(N: int, n: int, k: int, alpha: Fraction) -> int:
    """Exact one-sided upper confidence bound for K successes among N."""
    if n == 0:
        return N
    if n == N:
        return k
    if k == n:
        return N
    lo, hi = k, N - (n - k)
    while lo < hi:
        mid = (lo + hi + 1) // 2
        cdf = hypergeom_probability(N, mid, n, 0, k)
        if cdf >= alpha:
            lo = mid
        else:
            hi = mid - 1
    return lo


def load_labels(path: Path, known_ids: set[str]) -> dict[str, dict[str, Any]]:
    labels = unique_by_id(load_jsonl(path), "labels")
    unknown = set(labels) - known_ids
    if unknown:
        raise ScoreError(f"labels contain unknown site IDs: {sorted(unknown)[:3]}")
    for sid, label in labels.items():
        if set(label) != LABEL_FIELDS:
            raise ScoreError(
                f"{sid}: label fields must be exactly {sorted(LABEL_FIELDS)}"
            )
        invocation = label.get("invocation")
        if invocation not in ("yes", "no", "unsure"):
            raise ScoreError(f"{sid}: invocation must be yes/no/unsure")
        operation = label.get("operation")
        if invocation == "yes":
            if not norm_op(operation):
                raise ScoreError(
                    f"{sid}: eligible invocation requires fully-qualified package.Service/Method"
                )
        elif operation is not None:
            raise ScoreError(f"{sid}: non-invocation labels require operation=null")
        registration = label.get("registration")
        if registration not in ("yes", "no", "unsure"):
            raise ScoreError(f"{sid}: registration must be yes/no/unsure")
        service = label.get("service")
        if registration == "yes":
            if not norm_service(service):
                raise ScoreError(
                    f"{sid}: eligible registration requires fully-qualified package.Service"
                )
        elif service is not None:
            raise ScoreError(f"{sid}: non-registration labels require service=null")
        if label.get("expected_code_role") not in ALLOWED_ROLES:
            raise ScoreError(
                f"{sid}: expected_code_role must be one of {sorted(ALLOWED_ROLES)}"
            )
        for field in ("rationale", "evidence"):
            if not isinstance(label.get(field), str) or not label[field].strip():
                raise ScoreError(f"{sid}: {field} must be a nonempty string")
    return labels


def verify_label_publication_before_key(
    *,
    labels_path: Path,
    commitment_path: Path,
    receipt_path: Path,
    assignment_manifest_path: Path,
    artifact_dir: Path,
    fetcher: Any = github_json_fetcher,
) -> dict[str, Any]:
    """Verify the external label freeze before loading hidden outcomes."""

    paths = (
        labels_path,
        commitment_path,
        receipt_path,
        assignment_manifest_path,
        artifact_dir / "manifest.json",
    )
    if any(path.is_symlink() or not path.is_file() for path in paths):
        raise ScoreError("label-freeze inputs must be regular non-symlink files")
    try:
        labels_document = labels_path.read_bytes()
        commitment_raw = commitment_path.read_bytes()
        receipt_raw = receipt_path.read_text(encoding="utf-8")
        assignment_document = assignment_manifest_path.read_bytes()
        artifact_manifest_document = (artifact_dir / "manifest.json").read_bytes()
        commitment_value = json.loads(commitment_raw)
        receipt_value = json.loads(receipt_raw)
        artifact_manifest = json.loads(artifact_manifest_document)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ScoreError(f"cannot load label-freeze proof: {exc}") from exc
    try:
        commitment = validate_label_commitment(commitment_value)
        receipt = validate_github_gist_receipt(receipt_value)
        canonical_commitment = canonical_label_commitment_document(commitment)
        if commitment_raw != canonical_commitment:
            raise LabelProtocolError("label commitment file is not canonical")
        verify_github_initial_gist_receipt(
            receipt, canonical_commitment, fetcher
        )
    except LabelProtocolError as exc:
        raise ScoreError(f"label commitment publication is invalid: {exc}") from exc
    if protocol_sha256_bytes(labels_document) != commitment["labels_sha256"]:
        raise ScoreError("frozen labels do not match their public commitment")
    if (
        protocol_sha256_bytes(artifact_manifest_document)
        != commitment["artifact_manifest_sha256"]
    ):
        raise ScoreError("label commitment is bound to a different artifact manifest")
    if (
        protocol_sha256_bytes(assignment_document)
        != commitment["reviewer_assignment_sha256"]
    ):
        raise ScoreError("label commitment is bound to a different reviewer assignment")
    try:
        validate_assignment_manifest(
            assignment_document, commitment["reviewer_assignment_sha256"]
        )
    except AdjudicationError as exc:
        raise ScoreError(f"reviewer assignment is invalid: {exc}") from exc
    input_binding = artifact_manifest.get("randomization", {}).get("input_binding")
    if commitment["input_binding"] != input_binding:
        raise ScoreError("label commitment belongs to different Gate-2 inputs")
    committed_at = dt.datetime.strptime(
        commitment["committed_at"], "%Y-%m-%dT%H:%M:%SZ"
    ).replace(tzinfo=dt.timezone.utc)
    published_at = dt.datetime.strptime(
        receipt["created_at"], "%Y-%m-%dT%H:%M:%SZ"
    ).replace(tzinfo=dt.timezone.utc)
    if published_at < committed_at:
        raise ScoreError("GitHub label publication predates the frozen commitment")
    return commitment


def measurement_rows(
    bundle: dict[str, Any], labels: dict[str, dict[str, Any]], split: str, frame: str
) -> dict[str, list[tuple[dict[str, Any], dict[str, Any]]]]:
    sites = bundle[split]
    missing = set(sites) - set(labels)
    if missing:
        raise ScoreError(f"{split}: {len(missing)} labels missing (for example {sorted(missing)[0]})")
    if frame in {FRAME_CALL_PRECISION, FRAME_CALL_RECALL}:
        decision_fields = ("invocation",)
    elif frame in {FRAME_REGISTRATION_PRECISION, FRAME_REGISTRATION_RECALL}:
        decision_fields = ("registration",)
    else:
        decision_fields = ("invocation", "registration")
    unsure = [
        sid
        for sid in sites
        if any(labels[sid].get(field) == "unsure" for field in decision_fields)
    ]
    if unsure:
        raise ScoreError(f"{split}: {len(unsure)} unsure labels remain; confidence gate fails closed")
    grouped: dict[str, list[tuple[dict[str, Any], dict[str, Any]]]] = defaultdict(list)
    for sid in sites:
        key = bundle["key"][sid]
        membership = key["frames"].get(frame)
        if membership is not None:
            grouped[str(membership["stratum"])].append((membership, labels[sid]))
    return dict(grouped)


def label_positive(label: dict[str, Any], family: str) -> bool:
    return label["invocation" if family == "client_call" else "registration"] == "yes"


def identity_match(membership: dict[str, Any], label: dict[str, Any], family: str) -> bool:
    if family == "client_call":
        return op_match(membership.get("object"), label.get("operation"))
    return service_match(membership.get("object"), label.get("service"))


def benchmark_label_counts(
    bundle: dict[str, Any], labels: dict[str, dict[str, Any]]
) -> tuple[int, int]:
    """Count unique labeled recall units by ``(site_id, family)``."""

    units: list[str] = []
    for sid, row in bundle["key"].items():
        if FRAME_CALL_RECALL in row["frames"]:
            units.append(labels[sid]["invocation"])
        if FRAME_REGISTRATION_RECALL in row["frames"]:
            units.append(labels[sid]["registration"])
    return sum(value == "yes" for value in units), sum(value == "no" for value in units)


def precision_results(
    bundle: dict[str, Any], grouped: dict[str, list[Any]],
    census_grouped: dict[str, list[Any]], alpha_each: Fraction,
    frame: str, family: str,
) -> tuple[dict[str, tuple[float | None, Fraction]], int, int]:
    strata = bundle["manifest"]["frames"][frame]["strata"]
    lower: dict[str, int] = {}
    estimated: dict[str, float] = {}
    raw_correct = raw_total = 0
    for stratum in strata:
        rows = grouped.get(stratum["id"], [])
        census_rows = census_grouped.get(stratum["id"], [])
        successes = sum(
            label_positive(label, family) and identity_match(membership, label, family)
            for membership, label in rows
        )
        census_successes = sum(
            label_positive(label, family) and identity_match(membership, label, family)
            for membership, label in census_rows
        )
        if len(census_rows) != int(stratum["census_size"]):
            raise ScoreError(f"{frame}/{stratum['id']}: incomplete census labels")
        raw_correct += successes + census_successes
        raw_total += len(rows) + len(census_rows)
        N = int(stratum["sample_population_size"])
        n = len(rows)
        lower[stratum["id"]] = census_successes + lower_population_total(
            N, n, successes, alpha_each
        )
        estimated[stratum["id"]] = census_successes + (
            N * successes / n if n else 0.0
        )

    results: dict[str, tuple[float | None, Fraction]] = {}
    systems = sorted({stratum["system"] for stratum in strata})
    for scope in ["TOTAL"] + systems:
        selected = [s for s in strata if scope == "TOTAL" or s["system"] == scope]
        N = sum(int(s["population_size"]) for s in selected)
        point = sum(estimated[s["id"]] for s in selected) / N if N else None
        bound = (
            Fraction(sum(lower[s["id"]] for s in selected), N)
            if N
            else Fraction(0, 1)
        )
        results[scope] = (point, bound)
    return results, raw_correct, raw_total


def recall_result(
    bundle: dict[str, Any], grouped: dict[str, list[Any]],
    census_grouped: dict[str, list[Any]], alpha_each: Fraction,
    frame: str, family: str,
) -> tuple[float | None, Fraction, int, int]:
    strata = bundle["manifest"]["frames"][frame]["strata"]
    estimated_a = estimated_b = 0.0
    lower_a = upper_b = 0
    raw_a = raw_b = 0
    for stratum in strata:
        rows = grouped.get(stratum["id"], [])
        census_rows = census_grouped.get(stratum["id"], [])
        a = sum(
            label_positive(label, family)
            and membership.get("extracted") is True
            and identity_match(membership, label, family)
            for membership, label in rows
        )
        eligible = sum(label_positive(label, family) for _, label in rows)
        b = eligible - a
        census_a = sum(
            label_positive(label, family)
            and membership.get("extracted") is True
            and identity_match(membership, label, family)
            for membership, label in census_rows
        )
        census_eligible = sum(
            label_positive(label, family) for _, label in census_rows
        )
        census_b = census_eligible - census_a
        if len(census_rows) != int(stratum["census_size"]):
            raise ScoreError(f"{frame}/{stratum['id']}: incomplete census labels")
        raw_a += a + census_a
        raw_b += b + census_b
        N = int(stratum["sample_population_size"])
        n = len(rows)
        if n:
            estimated_a += N * a / n
            estimated_b += N * b / n
        estimated_a += census_a
        estimated_b += census_b
        lower_a += census_a + lower_population_total(N, n, a, alpha_each)
        upper_b += census_b + upper_population_total(N, n, b, alpha_each)
    point = estimated_a / (estimated_a + estimated_b) if estimated_a + estimated_b else None
    bound = (
        Fraction(lower_a, lower_a + upper_b)
        if lower_a + upper_b
        else Fraction(0, 1)
    )
    return point, bound, raw_a, raw_b


def pct(value: float | Fraction | None) -> str:
    return "n/a" if value is None else f"{float(value):.2%}"


def role_results(
    grouped: dict[str, list[tuple[dict[str, Any], dict[str, Any]]]]
) -> tuple[dict[str, tuple[int, int]], bool]:
    results = {role: [0, 0] for role in sorted(ALLOWED_ROLES)}
    for rows in grouped.values():
        for membership, label in rows:
            expected = str(label["expected_code_role"])
            families = set(membership.get("families", []))
            eligible = (
                ("client_call" in families and label["invocation"] == "yes")
                or ("registration" in families and label["registration"] == "yes")
            )
            if not eligible:
                continue
            results[expected][1] += 1
            emitted = membership.get("emitted_roles")
            results[expected][0] += (
                isinstance(emitted, list) and bool(emitted) and set(emitted) == {expected}
            )
    frozen = {role: (counts[0], counts[1]) for role, counts in results.items()}
    complete_and_exact = all(total > 0 and correct == total for correct, total in frozen.values())
    return frozen, complete_and_exact


def gate_configuration_reasons(
    bundle: dict[str, Any], args: argparse.Namespace
) -> list[str]:
    manifest = bundle["manifest"]
    reasons: list[str] = []
    if manifest.get("systems") != list(SYSTEMS):
        reasons.append("gate mode requires all four fixtures in the precommitted order")
    if manifest.get("gate_design_fixed") is not True:
        reasons.append("gate mode requires a one-shot sealed design")
    randomization = manifest.get("randomization")
    if (
        not isinstance(randomization, dict)
        or randomization.get("mode") != "audited-unpredictable-sha256-rank-v2"
        or not isinstance(randomization.get("input_binding"), str)
        or not randomization["input_binding"].startswith("sha256:")
        or not isinstance(randomization.get("seed_record"), dict)
        or randomization["seed_record"].get("input_binding")
        != randomization.get("input_binding")
        or "seed" in randomization
    ):
        reasons.append("gate mode requires an input-bound audited unpredictable seed receipt")
    attempt_claim = manifest.get("attempt_claim")
    randomization_fields = randomization if isinstance(randomization, dict) else {}
    if (
        not isinstance(attempt_claim, dict)
        or attempt_claim.get("input_binding") != randomization_fields.get("input_binding")
        or attempt_claim.get("base_input_binding") != randomization_fields.get(
            "base_input_binding"
        )
        or attempt_claim.get("committed_at")
        != randomization_fields.get("input_committed_at")
    ):
        reasons.append("gate mode requires the bundled four-commit attempt claim")
    if manifest.get("sampling_config") != gate_sampling_config():
        reasons.append("gate mode requires the fixed precommitted sampling configuration")
    if manifest.get("decision_rule") != gate_decision_rule():
        reasons.append("gate mode requires the fixed precommitted decision rule")
    burn_binding = manifest.get("burn_ledger")
    if (
        not isinstance(burn_binding, dict)
        or not isinstance(burn_binding.get("coordinate_count"), int)
        or burn_binding.get("coordinate_count", 0) <= 0
        or burn_binding.get("carry_forward_schema")
        != "t111-burn-carry-forward-census-v2"
        or not isinstance(burn_binding.get("active_coordinate_count"), int)
        or burn_binding.get("active_coordinate_count", 0) <= 0
        or manifest.get("holdout", {}).get("unique_census_sites", 0) <= 0
    ):
        reasons.append(
            "gate mode must carry disclosed source sites into the exact census stratum"
        )
    protocol = manifest.get("protocol_coverage")
    if protocol != {
        "client_calls": "measured",
        "registration": "measured",
        "registration_predicate": "IMPLEMENTS_SERVICE",
        "role_classification": "emitted_reference_sample_with_source_roles",
        "heuristic_call_facts": "quarantined_as_abstention_debt",
        "disclosed_source_sites": "exact_census_combined_with_blind_probability_bounds",
    }:
        reasons.append("gate mode requires call, registration, and emitted-role census coverage")
    try:
        expected_power = preflight_gate_design(
            manifest,
        )
    except (KeyError, TypeError, PrepError) as exc:
        reasons.append(f"gate power preflight failed: {exc}")
    else:
        if manifest.get("power_analysis") != expected_power or not expected_power.get(
            "attainable"
        ):
            reasons.append("gate power analysis is stale or statistically incapable")
    requested = (
        Fraction(str(args.confidence)),
        Fraction(str(args.precision_threshold)),
        Fraction(str(args.recall_threshold)),
        Fraction(str(args.fixture_precision_threshold)),
    )
    required = (
        GATE_CONFIDENCE,
        GATE_PRECISION_THRESHOLD,
        GATE_RECALL_THRESHOLD,
        GATE_FIXTURE_PRECISION_THRESHOLD,
    )
    if requested != required:
        reasons.append(
            "custom confidence or thresholds are diagnostic only; gate mode is fixed at "
            "95% joint confidence and 98%/90%/90% AC floors"
        )
    return reasons


def score_holdout(
    bundle: dict[str, Any], labels: dict[str, dict[str, Any]], args: argparse.Namespace
) -> int:
    all_ids = set(bundle["key"])
    if set(labels) != all_ids:
        missing = all_ids - set(labels)
        raise ScoreError(
            f"gate benchmark requires labels for all dev and holdout sites; missing {len(missing)}"
        )
    unsure = [
        sid
        for sid, label in labels.items()
        if label["invocation"] == "unsure" or label["registration"] == "unsure"
    ]
    if unsure:
        raise ScoreError(f"benchmark has {len(unsure)} unresolved labels")
    positives, hard_negatives = benchmark_label_counts(bundle, labels)
    benchmark_ok = positives >= MIN_POSITIVES and hard_negatives >= MIN_HARD_NEGATIVES
    holdout_ok = bundle["blind_fraction"] >= MIN_BLIND_HOLDOUT_FRACTION

    call_precision_rows = measurement_rows(
        bundle, labels, "holdout", FRAME_CALL_PRECISION
    )
    call_precision_census = measurement_rows(
        bundle, labels, "census", FRAME_CALL_PRECISION
    )
    call_recall_rows = measurement_rows(bundle, labels, "holdout", FRAME_CALL_RECALL)
    call_recall_census = measurement_rows(bundle, labels, "census", FRAME_CALL_RECALL)
    registration_precision_rows = measurement_rows(
        bundle, labels, "holdout", FRAME_REGISTRATION_PRECISION
    )
    registration_precision_census = measurement_rows(
        bundle, labels, "census", FRAME_REGISTRATION_PRECISION
    )
    registration_recall_rows = measurement_rows(
        bundle, labels, "holdout", FRAME_REGISTRATION_RECALL
    )
    registration_recall_census = measurement_rows(
        bundle, labels, "census", FRAME_REGISTRATION_RECALL
    )
    role_rows = measurement_rows(bundle, labels, "holdout", FRAME_ROLE)
    role_census = measurement_rows(bundle, labels, "census", FRAME_ROLE)
    for stratum, rows in role_census.items():
        role_rows.setdefault(stratum, []).extend(rows)
    family_size = confidence_bound_family_size(bundle["manifest"]["frames"])
    alpha_each = (
        (Fraction(1, 1) - GATE_CONFIDENCE) / family_size
        if family_size
        else Fraction(0, 1)
    )
    measurements: dict[str, dict[str, Any]] = {}
    for (
        family,
        precision_frame,
        recall_frame,
        precision_rows,
        precision_census,
        recall_rows,
        recall_census,
    ) in (
        (
            "client calls",
            FRAME_CALL_PRECISION,
            FRAME_CALL_RECALL,
            call_precision_rows,
            call_precision_census,
            call_recall_rows,
            call_recall_census,
        ),
        (
            "registrations",
            FRAME_REGISTRATION_PRECISION,
            FRAME_REGISTRATION_RECALL,
            registration_precision_rows,
            registration_precision_census,
            registration_recall_rows,
            registration_recall_census,
        ),
    ):
        family_key = "client_call" if family == "client calls" else "registration"
        precision, raw_correct, raw_precision_n = precision_results(
            bundle, precision_rows, precision_census, alpha_each, precision_frame, family_key
        )
        recall, recall_lower, raw_a, raw_b = recall_result(
            bundle, recall_rows, recall_census, alpha_each, recall_frame, family_key
        )
        measurements[family] = {
            "precision": precision,
            "recall": recall,
            "recall_lower": recall_lower,
            "raw": (raw_correct, raw_precision_n, raw_a, raw_b),
        }
    roles, role_ok = role_results(role_rows)

    print(
        "Gate 2 blind holdout — 95.0% joint simultaneous one-sided exact design bounds"
    )
    print(
        f"Bonferroni family: {family_size} exact integer hypergeometric bounds; "
        f"per-bound alpha={float(alpha_each):.8g}"
    )
    print("metric                              point       lower      threshold   decision")
    metric_ok = True
    for family, measurement in measurements.items():
        precision = measurement["precision"]
        point, lower = precision["TOTAL"]
        overall_ok = lower >= GATE_PRECISION_THRESHOLD
        metric_ok = metric_ok and overall_ok
        print(
            f"{family + ' precision overall':35s} {pct(point):>8}   {pct(lower):>8}"
            f"      98.0%   {'PASS' if overall_ok else 'NOT ESTABLISHED'}"
        )
        recall_ok = measurement["recall_lower"] >= GATE_RECALL_THRESHOLD
        metric_ok = metric_ok and recall_ok
        print(
            f"{family + ' recall population':35s} {pct(measurement['recall']):>8}   "
            f"{pct(measurement['recall_lower']):>8}      90.0%   "
            f"{'PASS' if recall_ok else 'NOT ESTABLISHED'}"
        )
        for system in sorted(scope for scope in precision if scope != "TOTAL"):
            point, lower = precision[system]
            ok = lower >= GATE_FIXTURE_PRECISION_THRESHOLD
            metric_ok = metric_ok and ok
            print(
                f"{family + ' precision ' + system:35s} {pct(point):>8}   {pct(lower):>8}"
                f"      90.0%   {'PASS' if ok else 'NOT ESTABLISHED'}"
            )
        raw_correct, raw_precision_n, raw_a, raw_b = measurement["raw"]
        print(
            f"  raw {family}: precision={raw_correct}/{raw_precision_n}; "
            f"recall correct/missed eligible={raw_a}/{raw_b}"
        )
    print("recall point estimate uses stratum population weights; the bound is not sample recall")
    print("\ncode-role classification on the independent candidate holdout:")
    for role, (correct, total) in roles.items():
        decision = "PASS" if total > 0 and correct == total else "NOT ESTABLISHED"
        print(f"  {role:10s} {correct}/{total} {decision}")
    print(
        f"benchmark labels: positives={positives} (minimum {MIN_POSITIVES}), "
        f"hard negatives={hard_negatives} (minimum {MIN_HARD_NEGATIVES}) — "
        f"{'PASS' if benchmark_ok else 'NOT ESTABLISHED'}"
    )
    print(
        f"blind holdout: {bundle['blind_fraction']:.2%} (minimum "
        f"{MIN_BLIND_HOLDOUT_FRACTION:.0%}) — "
        f"{'PASS' if holdout_ok else 'NOT ESTABLISHED'}"
    )
    passed = (
        metric_ok
        and role_ok
        and benchmark_ok
        and holdout_ok
    )
    print(f"\nGATE 2: {'PASS' if passed else 'NOT ESTABLISHED'}")
    return 0 if passed else 1


def score_diagnostic(bundle: dict[str, Any], labels: dict[str, dict[str, Any]], split: str) -> int:
    sites = bundle[split] if split in ("dev", "holdout") else {**bundle["dev"], **bundle["holdout"]}
    missing = set(sites) - set(labels)
    if missing:
        raise ScoreError(f"{split}: {len(missing)} labels missing")
    rows = [bundle["key"][sid] for sid in sites]
    reports: list[str] = []
    for family, verdict_field, object_field, precision_frame, recall_frame, matcher in (
        (
            "client calls", "invocation", "operation", FRAME_CALL_PRECISION,
            FRAME_CALL_RECALL, op_match,
        ),
        (
            "registrations", "registration", "service", FRAME_REGISTRATION_PRECISION,
            FRAME_REGISTRATION_RECALL, service_match,
        ),
    ):
        p_good = p_n = a = b = unsure = 0
        for row in rows:
            label = labels[row["site_id"]]
            relevant = precision_frame in row["frames"] or recall_frame in row["frames"]
            if relevant and label[verdict_field] == "unsure":
                unsure += 1
                continue
            precision = row["frames"].get(precision_frame)
            if precision:
                p_n += 1
                p_good += label[verdict_field] == "yes" and matcher(
                    precision.get("object"), label.get(object_field)
                )
            recall = row["frames"].get(recall_frame)
            if recall and label[verdict_field] == "yes":
                correct = recall.get("extracted") is True and matcher(
                    recall.get("object"), label.get(object_field)
                )
                a += correct
                b += not correct
        reports.append(
            f"{family}: precision sample={p_good}/{p_n}; recall eligible "
            f"correct/missed={a}/{b}; unsure={unsure}"
        )
    print(f"{split} diagnostic only: " + "; ".join(reports))
    print("GATE 2: NOT EVALUATED (only the frozen probability-sampled holdout can establish it)")
    return 0


def run_self_test() -> None:
    assert op_match("/pkg.Service/Method", "pkg.Service/Method")
    assert not op_match("/?.Service/Method", "pkg.Service/Method")
    assert not op_match("/Service/Method", "Service/Method")
    assert not op_match("/pkg.Service/Other", "pkg.Service/Method")
    assert service_match("pkg.Service", "/pkg.Service")
    assert not service_match("Service", "Service")
    assert not service_match("pkg.Other", "pkg.Service")

    entries = load_corpus_entries(DEFAULT_LOCK, SYSTEMS)
    commits = {system: str(entries[system]["commit"]) for system in SYSTEMS}
    _, burn_binding, _ = scoped_burn_binding(DEFAULT_BURN_LEDGER, commits)
    valid_burn_manifest = {"burn_ledger": burn_binding}
    validate_burn_binding(valid_burn_manifest, DEFAULT_BURN_LEDGER, commits)
    for field, value in (
        ("ledger_sha256", "0" * 64),
        ("coordinate_count", int(burn_binding["coordinate_count"]) + 1),
        ("coordinates_sha256", "sha256:" + "0" * 64),
        ("active_coordinate_count", int(burn_binding["active_coordinate_count"]) + 1),
    ):
        tampered = {"burn_ledger": dict(valid_burn_manifest["burn_ledger"])}
        tampered["burn_ledger"][field] = value
        try:
            validate_burn_binding(tampered, DEFAULT_BURN_LEDGER, commits)
        except ScoreError:
            pass
        else:
            raise AssertionError(f"tampered burn-ledger {field} binding was accepted")
    with tempfile.TemporaryDirectory() as raw_tmp:
        corrupt_path = Path(raw_tmp) / "burn-ledger.json"
        corrupt = json.loads(DEFAULT_BURN_LEDGER.read_text(encoding="utf-8"))
        corrupt["coordinate_count"] += 1
        corrupt_path.write_text(json.dumps(corrupt), encoding="utf-8")
        try:
            validate_burn_binding(valid_burn_manifest, corrupt_path, commits)
        except ScoreError:
            pass
        else:
            raise AssertionError("corrupt burn ledger was accepted")
    alpha = Fraction(1, 20)
    assert lower_population_total(10, 10, 9, alpha) == 9
    assert upper_population_total(10, 10, 2, alpha) == 2
    assert lower_population_total(100, 10, 10, alpha) < 90
    assert lower_population_total(100, 100, 98, alpha) == 98
    # These exact-alpha boundaries were anti-conservative with lgamma floats.
    assert upper_population_total(6, 3, 0, alpha) == 3
    assert lower_population_total(6, 3, 3, alpha) == 3
    for K in (0, 3, 10, 17, 20):
        probability = hypergeom_probability(20, K, 5, 0, 5)
        assert probability == 1, (K, probability)
    for N in range(1, 26):
        for n in range(N + 1):
            for k in range(n + 1):
                candidates = range(k, N - (n - k) + 1)
                expected_lower = (
                    0
                    if n == 0 or k == 0
                    else k
                    if n == N
                    else min(
                        K
                        for K in candidates
                        if hypergeom_probability(N, K, n, k, n) >= alpha
                    )
                )
                expected_upper = (
                    N
                    if n == 0 or k == n
                    else k
                    if n == N
                    else max(
                        K
                        for K in candidates
                        if hypergeom_probability(N, K, n, 0, k) >= alpha
                    )
                )
                assert lower_population_total(N, n, k, alpha) == expected_lower
                assert upper_population_total(N, n, k, alpha) == expected_upper
    print("score self-test: PASS")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("labels", nargs="?", type=Path)
    result.add_argument(
        "split", nargs="?", choices=("dev", "holdout", "all"), default="holdout"
    )
    result.add_argument("--artifact-dir", type=Path, default=DEFAULT_ARTIFACTS)
    result.add_argument("--corpus-dir", type=Path, default=DEFAULT_CORPUS)
    result.add_argument("--corpus-lock", type=Path, default=DEFAULT_LOCK)
    result.add_argument("--facts-dir", type=Path, default=DEFAULT_FACTS)
    result.add_argument("--label-commitment", type=Path)
    result.add_argument("--label-receipt", type=Path)
    result.add_argument("--assignment-manifest", type=Path)
    result.add_argument("--confidence", type=float, default=0.95)
    result.add_argument("--precision-threshold", type=float, default=0.98)
    result.add_argument("--recall-threshold", type=float, default=0.90)
    result.add_argument("--fixture-precision-threshold", type=float, default=0.90)
    result.add_argument("--self-test", action="store_true")
    return result


def main(argv: Sequence[str] | None = None) -> int:
    args = parser().parse_args(argv)
    if args.self_test:
        run_self_test()
        return 0
    if args.labels is None:
        parser().error("labels is required unless --self-test is used")
    for name in (
        "confidence",
        "precision_threshold",
        "recall_threshold",
        "fixture_precision_threshold",
    ):
        if not 0.0 < getattr(args, name) < 1.0:
            parser().error(f"--{name.replace('_', '-')} must be between zero and one")
    try:
        label_commitment: dict[str, Any] | None = None
        if args.split == "holdout":
            if (
                args.label_commitment is None
                or args.label_receipt is None
                or args.assignment_manifest is None
            ):
                raise ScoreError(
                    "gate scoring requires --label-commitment, --label-receipt, "
                    "and --assignment-manifest"
                )
            label_commitment = verify_label_publication_before_key(
                labels_path=args.labels,
                commitment_path=args.label_commitment,
                receipt_path=args.label_receipt,
                assignment_manifest_path=args.assignment_manifest,
                artifact_dir=args.artifact_dir,
            )
        bundle = load_bundle(
            args.artifact_dir,
            args.corpus_dir,
            args.corpus_lock,
            args.facts_dir,
        )
        labels = load_labels(args.labels, set(bundle["key"]))
        if label_commitment is not None:
            try:
                verify_label_commitment(
                    label_commitment, labels.values(), set(bundle["key"])
                )
            except LabelProtocolError as exc:
                raise ScoreError(f"frozen labels do not match commitment: {exc}") from exc
        if args.split == "holdout":
            reasons = gate_configuration_reasons(bundle, args)
            if reasons:
                for reason in reasons:
                    print(f"diagnostic-only: {reason}")
                score_diagnostic(bundle, labels, "holdout")
                return 1
            return score_holdout(bundle, labels, args)
        return score_diagnostic(bundle, labels, args.split)
    except ScoreError as exc:
        print(f"score: NOT ESTABLISHED: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
