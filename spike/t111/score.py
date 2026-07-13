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
    DEFAULT_BURN_LEDGER,
    DEFAULT_CORPUS,
    DEFAULT_LOCK,
    GATE_PRECISION_DEV_PER_SYSTEM,
    GATE_PRECISION_HOLDOUT_PER_SYSTEM,
    GATE_PRECISION_MIN_PER_STRATUM,
    GATE_RECALL_DEV_PER_SYSTEM,
    GATE_RECALL_HOLDOUT_PER_SYSTEM,
    GATE_RECALL_MIN_PER_STRATUM,
    MIN_BLIND_HOLDOUT_FRACTION,
    PrepError,
    SEED,
    SYSTEMS,
    build_artifacts,
    canonical_json,
    load_corpus_lock,
    pinned_tracked_files,
)
from gate34_common import (
    ValidationError as EvidenceValidationError,
    load_burn_ledger,
)


BASE = Path(__file__).resolve().parent
DEFAULT_ARTIFACTS = BASE / "labeling" / "g2-v2"
DEFAULT_FACTS = BASE / "out"
SCHEMA = "t111-gate2-probability-sample-v2"
GATE_CONFIDENCE = Fraction(95, 100)
GATE_PRECISION_THRESHOLD = Fraction(98, 100)
GATE_RECALL_THRESHOLD = Fraction(90, 100)
GATE_FIXTURE_PRECISION_THRESHOLD = Fraction(90, 100)
MIN_POSITIVES = 200
MIN_HARD_NEGATIVES = 100
ALLOWED_ROLES = {"production", "test", "mock", "generated", "vendor"}
LABEL_FIELDS = {
    "site_id",
    "invocation",
    "operation",
    "expected_code_role",
    "rationale",
    "evidence",
}
OPERATION_RE = re.compile(
    r"^/?[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+"
    r"/[A-Za-z_][A-Za-z0-9_]*$"
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


def validate_burn_binding(manifest: dict[str, Any], path: Path) -> dict[str, Any]:
    """Require the manifest to name the exact verified legacy burn ledger."""

    try:
        burned, coordinates_sha256 = load_burn_ledger(path)
    except EvidenceValidationError as exc:
        raise ScoreError(f"burn-ledger verification failed: {exc}") from exc
    expected = {
        "sha256": sha256_file(path),
        "coordinate_count": len(burned),
        "coordinates_sha256": coordinates_sha256,
    }
    if manifest.get("burn_ledger") != expected:
        raise ScoreError("manifest is not bound to the exact current burn ledger")
    return expected


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


def load_bundle(
    artifact_dir: Path, corpus_dir: Path, corpus_lock: Path, facts_dir: Path
) -> dict[str, Any]:
    manifest_path = artifact_dir / "manifest.json"
    if not manifest_path.is_file():
        raise ScoreError(
            f"{manifest_path} is missing. Legacy Gate 2 artifacts are a case-control sample "
            "without inclusion probabilities and cannot establish population recall; rerun "
            "label_prep.py prepare into a fresh artifact directory."
        )
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ScoreError(f"cannot load {manifest_path}: {exc}") from exc
    if manifest.get("schema") != SCHEMA:
        raise ScoreError(f"unsupported artifact schema {manifest.get('schema')!r}; expected {SCHEMA}")
    if manifest.get("coordinate_only_sites") is not True:
        raise ScoreError("artifact does not assert coordinate-only labeler sites")
    validate_burn_binding(manifest, DEFAULT_BURN_LEDGER)
    systems = manifest.get("systems")
    if (
        not isinstance(systems, list)
        or not systems
        or len(systems) != len(set(systems))
        or not all(isinstance(x, str) and x in SYSTEMS for x in systems)
    ):
        raise ScoreError("manifest has no valid system list")
    try:
        locked = load_corpus_lock(corpus_lock, systems)
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
            pinned_tracked_files(corpus_dir / system, locked[system])
    except PrepError as exc:
        raise ScoreError(f"corpus preflight failed: {exc}") from exc

    paths = {
        "sites.dev.jsonl": artifact_dir / "sites.dev.jsonl",
        "sites.holdout.jsonl": artifact_dir / "sites.holdout.jsonl",
        "key.jsonl": artifact_dir / "key.jsonl",
    }
    expected_hashes = manifest.get("artifacts_sha256")
    if not isinstance(expected_hashes, dict):
        raise ScoreError("manifest is missing artifact digests")
    for name, path in paths.items():
        if expected_hashes.get(name) != sha256_file(path):
            raise ScoreError(f"artifact digest mismatch: {path}")

    dev = unique_by_id(load_jsonl(paths["sites.dev.jsonl"]), "sites.dev")
    holdout = unique_by_id(load_jsonl(paths["sites.holdout.jsonl"]), "sites.holdout")
    key = unique_by_id(load_jsonl(paths["key.jsonl"]), "key")
    overlap = set(dev) & set(holdout)
    if overlap:
        raise ScoreError(f"dev/holdout ID leakage: {sorted(overlap)[:3]}")
    if set(key) != set(dev) | set(holdout):
        raise ScoreError("key IDs do not equal the union of site IDs")

    allowed = {"site_id", "system", "path", "line", "column", "method"}
    coordinates: dict[tuple[Any, ...], tuple[str, str]] = {}
    for split, sites in (("dev", dev), ("holdout", holdout)):
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
    for frame in ("recall", "precision"):
        frame_manifest = manifest.get("frames", {}).get(frame)
        if not isinstance(frame_manifest, dict):
            raise ScoreError(f"manifest missing {frame} frame")
        for row in frame_manifest.get("strata", []):
            stratum = row.get("id")
            if not isinstance(stratum, str) or (frame, stratum) in strata:
                raise ScoreError(f"duplicate/invalid {frame} stratum {stratum!r}")
            n_population = row.get("population_size")
            n_holdout = row.get("holdout_sample_size")
            probability = row.get("holdout_inclusion_probability")
            if not isinstance(n_population, int) or n_population <= 0:
                raise ScoreError(f"{frame}/{stratum}: invalid population size")
            if not isinstance(n_holdout, int) or not 0 <= n_holdout <= n_population:
                raise ScoreError(f"{frame}/{stratum}: invalid holdout sample size")
            if not isinstance(probability, (int, float)) or not math.isclose(
                probability, n_holdout / n_population, rel_tol=0, abs_tol=1e-15
            ):
                raise ScoreError(f"{frame}/{stratum}: invalid inclusion probability")
            strata[(frame, stratum)] = row

    observed: dict[tuple[str, str, str], int] = defaultdict(int)
    for sid, row in key.items():
        frames = row.get("frames")
        if not isinstance(frames, dict) or not frames:
            raise ScoreError(f"{sid}: missing frame membership")
        for frame, membership in frames.items():
            if frame not in ("recall", "precision") or not isinstance(membership, dict):
                raise ScoreError(f"{sid}: invalid frame membership {frame}")
            stratum = membership.get("stratum")
            if (frame, stratum) not in strata:
                raise ScoreError(f"{sid}: unknown {frame} stratum {stratum}")
            observed[(frame, stratum, row["split"])] += 1
    for (frame, stratum), row in strata.items():
        for split, field in (("holdout", "holdout_sample_size"), ("dev", "dev_sample_size")):
            if observed[(frame, stratum, split)] != row.get(field):
                raise ScoreError(f"{frame}/{stratum}: {split} membership count mismatch")

    holdout_meta = manifest.get("holdout")
    if not isinstance(holdout_meta, dict):
        raise ScoreError("manifest has no holdout design")
    selected_total = len(dev) + len(holdout)
    blind_fraction = len(holdout) / selected_total if selected_total else 0.0
    if (
        holdout_meta.get("unique_dev_sites") != len(dev)
        or holdout_meta.get("unique_holdout_sites") != len(holdout)
        or not isinstance(holdout_meta.get("blind_fraction"), (int, float))
        or not math.isclose(
            float(holdout_meta["blind_fraction"]), blind_fraction, rel_tol=0, abs_tol=1e-15
        )
    ):
        raise ScoreError("manifest holdout cardinalities do not match the artifacts")

    config = manifest.get("sampling_config")
    required_config = {
        "recall_holdout_per_system",
        "recall_dev_per_system",
        "precision_holdout_per_system",
        "precision_dev_per_system",
        "recall_min_per_stratum",
        "precision_min_per_stratum",
    }
    if not isinstance(config, dict) or set(config) != required_config:
        raise ScoreError("manifest has no complete sampling configuration")
    if not all(isinstance(config[name], int) and config[name] >= 0 for name in config):
        raise ScoreError("manifest sampling configuration is invalid")

    # Re-enumerate both populations and rerun deterministic selection from the
    # current pinned Git objects and extractor facts.  Self-consistent manifest
    # edits are therefore insufficient to alter N, n, pi, or verdicts.
    rebuild_args = SimpleNamespace(
        systems=",".join(systems),
        seed=manifest.get("seed"),
        corpus_dir=corpus_dir,
        corpus_lock=corpus_lock,
        facts_dir=facts_dir,
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
    if canonical_json(recorded_without_hashes) != canonical_json(rebuilt_manifest):
        raise ScoreError("manifest does not match recomputed frames and selection")
    if rebuilt_dev != list(dev.values()) or rebuilt_holdout != list(holdout.values()):
        raise ScoreError("coordinate artifacts do not match recomputed selection")
    if rebuilt_key != list(key.values()):
        raise ScoreError("hidden key does not match recomputed extractor outcomes")

    return {
        "manifest": manifest,
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
        if label.get("expected_code_role") not in ALLOWED_ROLES:
            raise ScoreError(
                f"{sid}: expected_code_role must be one of {sorted(ALLOWED_ROLES)}"
            )
        for field in ("rationale", "evidence"):
            if not isinstance(label.get(field), str) or not label[field].strip():
                raise ScoreError(f"{sid}: {field} must be a nonempty string")
    return labels


def measurement_rows(
    bundle: dict[str, Any], labels: dict[str, dict[str, Any]], split: str, frame: str
) -> dict[str, list[tuple[dict[str, Any], dict[str, Any]]]]:
    sites = bundle[split]
    missing = set(sites) - set(labels)
    if missing:
        raise ScoreError(f"{split}: {len(missing)} labels missing (for example {sorted(missing)[0]})")
    unsure = [sid for sid in sites if labels[sid].get("invocation") == "unsure"]
    if unsure:
        raise ScoreError(f"{split}: {len(unsure)} unsure labels remain; confidence gate fails closed")
    grouped: dict[str, list[tuple[dict[str, Any], dict[str, Any]]]] = defaultdict(list)
    for sid in sites:
        key = bundle["key"][sid]
        membership = key["frames"].get(frame)
        if membership is not None:
            grouped[str(membership["stratum"])].append((membership, labels[sid]))
    return dict(grouped)


def precision_results(
    bundle: dict[str, Any], grouped: dict[str, list[Any]], alpha_each: Fraction
) -> tuple[dict[str, tuple[float | None, Fraction]], int, int]:
    strata = bundle["manifest"]["frames"]["precision"]["strata"]
    lower: dict[str, int] = {}
    estimated: dict[str, float] = {}
    raw_correct = raw_total = 0
    for stratum in strata:
        rows = grouped.get(stratum["id"], [])
        successes = sum(
            label["invocation"] == "yes" and op_match(membership.get("object"), label.get("operation"))
            for membership, label in rows
        )
        raw_correct += successes
        raw_total += len(rows)
        N = int(stratum["population_size"])
        n = len(rows)
        lower[stratum["id"]] = lower_population_total(N, n, successes, alpha_each)
        estimated[stratum["id"]] = N * successes / n if n else 0.0

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
    bundle: dict[str, Any], grouped: dict[str, list[Any]], alpha_each: Fraction
) -> tuple[float | None, Fraction, int, int]:
    strata = bundle["manifest"]["frames"]["recall"]["strata"]
    estimated_a = estimated_b = 0.0
    lower_a = upper_b = 0
    raw_a = raw_b = 0
    for stratum in strata:
        rows = grouped.get(stratum["id"], [])
        a = sum(
            label["invocation"] == "yes"
            and membership.get("extracted") is True
            and op_match(membership.get("object"), label.get("operation"))
            for membership, label in rows
        )
        eligible = sum(label["invocation"] == "yes" for _, label in rows)
        b = eligible - a
        raw_a += a
        raw_b += b
        N = int(stratum["population_size"])
        n = len(rows)
        if n:
            estimated_a += N * a / n
            estimated_b += N * b / n
        lower_a += lower_population_total(N, n, a, alpha_each)
        upper_b += upper_population_total(N, n, b, alpha_each)
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
            results[expected][1] += 1
            results[expected][0] += membership.get("role") == expected
    frozen = {role: (counts[0], counts[1]) for role, counts in results.items()}
    complete_and_exact = all(total > 0 and correct == total for correct, total in frozen.values())
    return frozen, complete_and_exact


def gate_configuration_reasons(
    bundle: dict[str, Any], args: argparse.Namespace
) -> list[str]:
    manifest = bundle["manifest"]
    reasons: list[str] = []
    expected_sampling = {
        "recall_holdout_per_system": GATE_RECALL_HOLDOUT_PER_SYSTEM,
        "recall_dev_per_system": GATE_RECALL_DEV_PER_SYSTEM,
        "precision_holdout_per_system": GATE_PRECISION_HOLDOUT_PER_SYSTEM,
        "precision_dev_per_system": GATE_PRECISION_DEV_PER_SYSTEM,
        "recall_min_per_stratum": GATE_RECALL_MIN_PER_STRATUM,
        "precision_min_per_stratum": GATE_PRECISION_MIN_PER_STRATUM,
    }
    if manifest.get("systems") != list(SYSTEMS):
        reasons.append("gate mode requires all four fixtures in the precommitted order")
    if manifest.get("seed") != SEED or manifest.get("gate_design_fixed") is not True:
        reasons.append(f"gate mode requires the precommitted seed {SEED}")
    if manifest.get("sampling_config") != expected_sampling:
        reasons.append("gate mode requires the fixed precommitted sampling configuration")
    burn_binding = manifest.get("burn_ledger")
    if not isinstance(burn_binding, dict) or burn_binding.get("coordinate_count") != 0:
        reasons.append(
            "gate mode cannot remove exposed coordinates from the same pinned population; "
            "refresh the corpus or include prior outcomes in a reviewed estimator"
        )
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
    unsure = [sid for sid, label in labels.items() if label["invocation"] == "unsure"]
    if unsure:
        raise ScoreError(f"benchmark has {len(unsure)} unresolved labels")
    positives = sum(label["invocation"] == "yes" for label in labels.values())
    hard_negatives = sum(label["invocation"] == "no" for label in labels.values())
    benchmark_ok = positives >= MIN_POSITIVES and hard_negatives >= MIN_HARD_NEGATIVES
    holdout_ok = bundle["blind_fraction"] >= MIN_BLIND_HOLDOUT_FRACTION

    protocol = bundle["manifest"].get("protocol_coverage")
    if not isinstance(protocol, dict) or protocol.get("registration") != "protocol_missing":
        raise ScoreError(
            "this scorer has no independent registration frame and requires an explicit "
            "registration=protocol_missing declaration"
        )
    registration_ok = False

    precision_rows = measurement_rows(bundle, labels, "holdout", "precision")
    recall_rows = measurement_rows(bundle, labels, "holdout", "recall")
    precision_strata = bundle["manifest"]["frames"]["precision"]["strata"]
    recall_strata = bundle["manifest"]["frames"]["recall"]["strata"]
    family_size = len(precision_strata) + 2 * len(recall_strata)
    if family_size <= 0:
        raise ScoreError("gate has no confidence-bound family")
    alpha_each = (Fraction(1, 1) - GATE_CONFIDENCE) / family_size
    precision, raw_correct, raw_precision_n = precision_results(
        bundle, precision_rows, alpha_each
    )
    recall, recall_lower, raw_a, raw_b = recall_result(
        bundle, recall_rows, alpha_each
    )
    roles, role_ok = role_results(precision_rows)

    print(
        "Gate 2 blind holdout — 95.0% joint simultaneous one-sided exact design bounds"
    )
    print(
        f"Bonferroni family: {family_size} exact integer hypergeometric bounds; "
        f"per-bound alpha={float(alpha_each):.8g}"
    )
    print("metric                 point       lower      threshold   decision")
    point, lower = precision["TOTAL"]
    overall_ok = lower >= GATE_PRECISION_THRESHOLD
    print(f"precision overall      {pct(point):>8}   {pct(lower):>8}      98.0%   {'PASS' if overall_ok else 'NOT ESTABLISHED'}")
    recall_ok = recall_lower >= GATE_RECALL_THRESHOLD
    print(f"recall population      {pct(recall):>8}   {pct(recall_lower):>8}      90.0%   {'PASS' if recall_ok else 'NOT ESTABLISHED'}")
    fixture_ok = True
    for system in sorted(scope for scope in precision if scope != "TOTAL"):
        point, lower = precision[system]
        ok = lower >= GATE_FIXTURE_PRECISION_THRESHOLD
        fixture_ok = fixture_ok and ok
        print(f"precision {system:12s} {pct(point):>8}   {pct(lower):>8}      90.0%   {'PASS' if ok else 'NOT ESTABLISHED'}")
    print(f"\nraw labeled holdout counts: precision={raw_correct}/{raw_precision_n}; recall correct/missed eligible={raw_a}/{raw_b}")
    print("recall point estimate uses stratum population weights; the bound is not sample recall")
    print("\ncode-role classification on emitted-fact holdout:")
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
    print(
        "registration / IMPLEMENTS_SERVICE: PROTOCOL MISSING — independently labeled "
        "registration precision/recall and classification have not been measured"
    )
    passed = (
        overall_ok
        and recall_ok
        and fixture_ok
        and role_ok
        and benchmark_ok
        and holdout_ok
        and registration_ok
    )
    print(f"\nGATE 2: {'PASS' if passed else 'NOT ESTABLISHED'}")
    return 0 if passed else 1


def score_diagnostic(bundle: dict[str, Any], labels: dict[str, dict[str, Any]], split: str) -> int:
    sites = bundle[split] if split in ("dev", "holdout") else {**bundle["dev"], **bundle["holdout"]}
    missing = set(sites) - set(labels)
    if missing:
        raise ScoreError(f"{split}: {len(missing)} labels missing")
    rows = [bundle["key"][sid] for sid in sites]
    p_good = p_n = a = b = unsure = 0
    for row in rows:
        label = labels[row["site_id"]]
        if label["invocation"] == "unsure":
            unsure += 1
            continue
        precision = row["frames"].get("precision")
        if precision:
            p_n += 1
            p_good += label["invocation"] == "yes" and op_match(precision.get("object"), label.get("operation"))
        recall = row["frames"].get("recall")
        if recall and label["invocation"] == "yes":
            correct = recall.get("extracted") is True and op_match(recall.get("object"), label.get("operation"))
            a += correct
            b += not correct
    print(f"{split} diagnostic only: precision sample={p_good}/{p_n}; recall eligible correct/missed={a}/{b}; unsure={unsure}")
    print("GATE 2: NOT EVALUATED (only the frozen probability-sampled holdout can establish it)")
    return 0


def run_self_test() -> None:
    assert op_match("/pkg.Service/Method", "pkg.Service/Method")
    assert not op_match("/?.Service/Method", "pkg.Service/Method")
    assert not op_match("/Service/Method", "Service/Method")
    assert not op_match("/pkg.Service/Other", "pkg.Service/Method")

    burned, coordinates_sha256 = load_burn_ledger(DEFAULT_BURN_LEDGER)
    valid_burn_manifest = {
        "burn_ledger": {
            "sha256": sha256_file(DEFAULT_BURN_LEDGER),
            "coordinate_count": len(burned),
            "coordinates_sha256": coordinates_sha256,
        }
    }
    validate_burn_binding(valid_burn_manifest, DEFAULT_BURN_LEDGER)
    for field, value in (
        ("sha256", "0" * 64),
        ("coordinate_count", len(burned) + 1),
        ("coordinates_sha256", "sha256:" + "0" * 64),
    ):
        tampered = {"burn_ledger": dict(valid_burn_manifest["burn_ledger"])}
        tampered["burn_ledger"][field] = value
        try:
            validate_burn_binding(tampered, DEFAULT_BURN_LEDGER)
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
            validate_burn_binding(valid_burn_manifest, corrupt_path)
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
        bundle = load_bundle(
            args.artifact_dir,
            args.corpus_dir,
            args.corpus_lock,
            args.facts_dir,
        )
        labels = load_labels(args.labels, set(bundle["key"]))
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
