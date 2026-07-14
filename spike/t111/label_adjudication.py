"""Canonical two-reviewer label adjudication for the T11.1 Gate 2 protocol.

The assignment manifest is treated as a committed input: callers provide its
exact canonical bytes and trusted SHA-256.  Reviewer and adjudicator rows are
validated through :mod:`label_protocol`; this module adds only assignment,
overlap, disagreement, and resolution invariants.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from typing import Any, Final

import label_protocol
import reviewer_kits


DISAGREEMENT_FIELDS: Final = (
    "invocation",
    "operation",
    "registration",
    "service",
    "expected_code_role",
)
COUNTER_FIELDS: Final = (
    "overlap_sites",
    "disagreements",
    "adjudicated",
    "unresolved",
)
_MANIFEST_FIELDS: Final = frozenset(
    {
        "schema",
        "input_binding",
        "reviewers",
        "primary_assignment",
        "cross_review",
        "source_population_size",
        "source_population_sha256",
        "assignments",
        "kits",
    }
)
_ASSIGNMENT_FIELDS: Final = frozenset(
    {"site_id", "primary_reviewer", "cross_review"}
)
_KIT_FIELDS: Final = frozenset(
    {"reviewer", "filename", "site_count", "document_sha256"}
)
_STRATUM_FIELDS: Final = frozenset(
    {"system", "split", "population_size", "cross_review_count"}
)
_SHA256_RE = re.compile(r"sha256:[0-9a-f]{64}")


class AdjudicationError(ValueError):
    """Assignment or labels cannot produce a complete resolved label set."""


@dataclass(frozen=True)
class AdjudicationResult:
    rows: tuple[dict[str, Any], ...]
    document: bytes
    document_sha256: str
    counters: Mapping[str, int]


@dataclass(frozen=True)
class _Assignment:
    reviewers: tuple[str, str]
    by_site: Mapping[str, Mapping[str, Any]]
    kit_site_ids: Mapping[str, frozenset[str]]
    overlap_sites: frozenset[str]


def adjudicate_labels(
    manifest_document: bytes,
    manifest_sha256: str,
    reviewer_labels: Mapping[str, Iterable[Mapping[str, Any]]],
    adjudicator_rows: Iterable[Mapping[str, Any]],
) -> AdjudicationResult:
    """Resolve two assigned reviewer populations into one canonical label set."""

    assignment = validate_assignment_manifest(
        manifest_document, manifest_sha256
    )
    if not isinstance(reviewer_labels, Mapping):
        raise AdjudicationError("reviewer_labels must be a reviewer-to-rows mapping")
    if set(reviewer_labels) != set(assignment.reviewers):
        missing = sorted(set(assignment.reviewers) - set(reviewer_labels))
        extra = sorted(set(reviewer_labels) - set(assignment.reviewers))
        raise AdjudicationError(
            f"reviewer label partitions do not match assignment: "
            f"missing={missing} extra={extra}"
        )

    labels: dict[str, dict[str, dict[str, Any]]] = {}
    for reviewer in assignment.reviewers:
        expected = assignment.kit_site_ids[reviewer]
        try:
            rows = label_protocol.validate_labels(
                reviewer_labels[reviewer], expected
            )
        except (label_protocol.LabelProtocolError, TypeError) as exc:
            raise AdjudicationError(
                f"invalid labels for reviewer {reviewer}: {exc}"
            ) from exc
        labels[reviewer] = {row["site_id"]: row for row in rows}

    disagreements: set[str] = set()
    requires_adjudication: set[str] = set()
    for site_id, assigned in assignment.by_site.items():
        primary = str(assigned["primary_reviewer"])
        primary_label = labels[primary][site_id]
        site_labels = [primary_label]
        if bool(assigned["cross_review"]):
            other = (
                assignment.reviewers[1]
                if primary == assignment.reviewers[0]
                else assignment.reviewers[0]
            )
            secondary_label = labels[other][site_id]
            site_labels.append(secondary_label)
            if decision_signature(primary_label) != decision_signature(
                secondary_label
            ):
                disagreements.add(site_id)
                requires_adjudication.add(site_id)
        if any(_is_unsure(row) for row in site_labels):
            requires_adjudication.add(site_id)

    try:
        adjudicated = label_protocol.validate_labels(
            adjudicator_rows, requires_adjudication
        )
    except (label_protocol.LabelProtocolError, TypeError) as exc:
        raise AdjudicationError(f"invalid adjudicator labels: {exc}") from exc
    adjudicated_by_id = {row["site_id"]: row for row in adjudicated}
    unresolved = sorted(
        site_id
        for site_id, row in adjudicated_by_id.items()
        if _is_unsure(row)
    )
    if unresolved:
        raise AdjudicationError(
            f"adjudicator left unresolved labels: {unresolved[:3]}"
        )

    final: list[dict[str, Any]] = []
    for site_id in sorted(assignment.by_site):
        if site_id in adjudicated_by_id:
            final.append(adjudicated_by_id[site_id])
            continue
        primary = str(assignment.by_site[site_id]["primary_reviewer"])
        row = labels[primary][site_id]
        if _is_unsure(row):
            # This should be unreachable because the exact adjudicator
            # population was validated above. Keep the invariant local.
            raise AdjudicationError(f"unresolved primary label: {site_id}")
        final.append(row)

    all_ids = set(assignment.by_site)
    try:
        document = label_protocol.canonical_labels_jsonl(final, all_ids)
        canonical_rows = label_protocol.validate_labels(final, all_ids)
    except label_protocol.LabelProtocolError as exc:
        raise AdjudicationError(f"invalid final label population: {exc}") from exc
    if any(_is_unsure(row) for row in canonical_rows):
        raise AdjudicationError("final label population contains unresolved decisions")

    counters = {
        "overlap_sites": len(assignment.overlap_sites),
        "disagreements": len(disagreements),
        "adjudicated": len(adjudicated_by_id),
        "unresolved": 0,
    }
    if tuple(counters) != COUNTER_FIELDS:
        raise AdjudicationError("internal adjudication counter schema drift")
    return AdjudicationResult(
        rows=canonical_rows,
        document=document,
        document_sha256=label_protocol.sha256_bytes(document),
        counters=counters,
    )


def validate_assignment_manifest(
    document: bytes, expected_sha256: str
) -> _Assignment:
    """Validate canonical manifest bytes and recompute primary assignments."""

    if not isinstance(document, bytes):
        raise AdjudicationError("assignment manifest document must be bytes")
    if not isinstance(expected_sha256, str) or not _SHA256_RE.fullmatch(
        expected_sha256
    ):
        raise AdjudicationError(
            "assignment manifest digest must be sha256:<64 lowercase hex>"
        )
    actual_sha256 = label_protocol.sha256_bytes(document)
    if actual_sha256 != expected_sha256:
        raise AdjudicationError("assignment manifest digest mismatch")
    try:
        text = document.decode("utf-8", errors="strict")
        manifest = json.loads(text)
    except (UnicodeError, json.JSONDecodeError) as exc:
        raise AdjudicationError(f"invalid assignment manifest JSON: {exc}") from exc
    if not isinstance(manifest, dict):
        raise AdjudicationError("assignment manifest must be an object")
    try:
        canonical = reviewer_kits.canonical_json_document(manifest)
    except reviewer_kits.ReviewerKitError as exc:
        raise AdjudicationError(f"invalid assignment manifest data: {exc}") from exc
    if canonical != document:
        raise AdjudicationError("assignment manifest is not canonical JSON")
    if set(manifest) != _MANIFEST_FIELDS:
        raise AdjudicationError(
            f"assignment manifest fields must be exactly {sorted(_MANIFEST_FIELDS)}"
        )
    if manifest["schema"] != reviewer_kits.ASSIGNMENT_SCHEMA:
        raise AdjudicationError("unsupported assignment manifest schema")

    binding = manifest["input_binding"]
    if not isinstance(binding, str) or not re.fullmatch(
        r"sha256:[0-9a-f]{64}", binding
    ):
        raise AdjudicationError("assignment manifest has invalid input_binding")
    reviewers_value = manifest["reviewers"]
    if (
        not isinstance(reviewers_value, list)
        or len(reviewers_value) != 2
        or not all(
            isinstance(reviewer, str)
            and reviewer
            and reviewer == reviewer.strip()
            for reviewer in reviewers_value
        )
        or reviewers_value != sorted(reviewers_value)
        or len(set(reviewers_value)) != 2
    ):
        raise AdjudicationError(
            "assignment manifest must contain two sorted distinct reviewers"
        )
    reviewers = (reviewers_value[0], reviewers_value[1])

    primary_design = manifest["primary_assignment"]
    if primary_design != {
        "algorithm": "sha256-domain-modulo-two-v1",
        "domain": "t111-reviewer-primary-assignment-v1",
    }:
        raise AdjudicationError("assignment manifest primary design drift")
    cross_design = manifest["cross_review"]
    if not isinstance(cross_design, dict) or set(cross_design) != {
        "fraction",
        "algorithm",
        "domain",
        "strata",
    }:
        raise AdjudicationError("assignment manifest cross-review design is invalid")
    if (
        cross_design["fraction"] != "1/10"
        or cross_design["algorithm"]
        != "sha256-domain-stratified-rank-ceiling-v1"
        or cross_design["domain"]
        != "t111-reviewer-cross-review-rank-v1"
    ):
        raise AdjudicationError("assignment manifest cross-review design drift")

    strata = cross_design["strata"]
    if not isinstance(strata, list):
        raise AdjudicationError("assignment manifest strata must be an array")
    seen_strata: set[tuple[str, str]] = set()
    stratum_population = 0
    expected_overlap = 0
    previous_stratum: tuple[str, str] | None = None
    for index, stratum in enumerate(strata):
        if not isinstance(stratum, dict) or set(stratum) != _STRATUM_FIELDS:
            raise AdjudicationError(f"invalid cross-review stratum {index}")
        system, split = stratum["system"], stratum["split"]
        key = (system, split)
        population = stratum["population_size"]
        overlap = stratum["cross_review_count"]
        if (
            not isinstance(system, str)
            or not system
            or not isinstance(split, str)
            or not split
            or isinstance(population, bool)
            or not isinstance(population, int)
            or population <= 0
            or isinstance(overlap, bool)
            or not isinstance(overlap, int)
            or overlap != (population + 9) // 10
            or key in seen_strata
            or (previous_stratum is not None and key <= previous_stratum)
        ):
            raise AdjudicationError(f"invalid cross-review stratum {index}")
        previous_stratum = key
        seen_strata.add(key)
        stratum_population += population
        expected_overlap += overlap

    assignments_value = manifest["assignments"]
    if not isinstance(assignments_value, list):
        raise AdjudicationError("assignment manifest assignments must be an array")
    by_site: dict[str, dict[str, Any]] = {}
    overlap_sites: set[str] = set()
    previous_site_id: str | None = None
    for index, raw in enumerate(assignments_value):
        if not isinstance(raw, dict) or set(raw) != _ASSIGNMENT_FIELDS:
            raise AdjudicationError(f"invalid assignment row {index}")
        site_id = raw["site_id"]
        primary = raw["primary_reviewer"]
        overlap = raw["cross_review"]
        if (
            not isinstance(site_id, str)
            or not site_id
            or site_id != site_id.strip()
            or site_id in by_site
            or (previous_site_id is not None and site_id <= previous_site_id)
            or primary not in reviewers
            or not isinstance(overlap, bool)
        ):
            raise AdjudicationError(f"invalid or duplicate assignment row {index}")
        expected_primary = reviewers[
            int.from_bytes(_primary_rank(binding, site_id), "big") % 2
        ]
        if primary != expected_primary:
            raise AdjudicationError(
                f"primary assignment tampering detected for {site_id}"
            )
        previous_site_id = site_id
        by_site[site_id] = raw
        if overlap:
            overlap_sites.add(site_id)

    population_size = manifest["source_population_size"]
    if (
        isinstance(population_size, bool)
        or not isinstance(population_size, int)
        or population_size != len(by_site)
        or population_size != stratum_population
    ):
        raise AdjudicationError("assignment source population is incomplete")
    source_digest = manifest["source_population_sha256"]
    if not isinstance(source_digest, str) or not _SHA256_RE.fullmatch(source_digest):
        raise AdjudicationError("assignment source population digest is invalid")
    if len(overlap_sites) != expected_overlap:
        raise AdjudicationError("assignment cross-review population is incomplete")

    kits_value = manifest["kits"]
    if not isinstance(kits_value, list) or len(kits_value) != 2:
        raise AdjudicationError("assignment manifest must bind two reviewer kits")
    kit_site_ids: dict[str, frozenset[str]] = {}
    for index, (reviewer, raw) in enumerate(zip(reviewers, kits_value), start=1):
        if not isinstance(raw, dict) or set(raw) != _KIT_FIELDS:
            raise AdjudicationError(f"invalid reviewer kit binding {index}")
        expected_ids = frozenset(
            site_id
            for site_id, assignment in by_site.items()
            if assignment["primary_reviewer"] == reviewer
            or assignment["cross_review"]
        )
        if (
            raw["reviewer"] != reviewer
            or raw["filename"] != f"reviewer-{index}.jsonl"
            or isinstance(raw["site_count"], bool)
            or raw["site_count"] != len(expected_ids)
            or not isinstance(raw["document_sha256"], str)
            or not _SHA256_RE.fullmatch(raw["document_sha256"])
        ):
            raise AdjudicationError(f"reviewer kit assignment mismatch {index}")
        kit_site_ids[reviewer] = expected_ids

    return _Assignment(
        reviewers=reviewers,
        by_site=by_site,
        kit_site_ids=kit_site_ids,
        overlap_sites=frozenset(overlap_sites),
    )


def decision_signature(row: Mapping[str, Any]) -> tuple[Any, ...]:
    """Return exactly the five fields that define reviewer disagreement."""

    return tuple(row[field] for field in DISAGREEMENT_FIELDS)


def _is_unsure(row: Mapping[str, Any]) -> bool:
    return row["invocation"] == "unsure" or row["registration"] == "unsure"


def _primary_rank(input_binding: str, site_id: str) -> bytes:
    domain = b"t111-reviewer-primary-assignment-v1"
    digest = hashlib.sha256()
    digest.update(len(domain).to_bytes(4, "big"))
    digest.update(domain)
    for part in (input_binding, site_id):
        encoded = part.encode("utf-8", errors="strict")
        digest.update(len(encoded).to_bytes(8, "big"))
        digest.update(encoded)
    return digest.digest()
