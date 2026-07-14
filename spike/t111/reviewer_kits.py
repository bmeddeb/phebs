"""Deterministic, source-only assignment kits for two independent reviewers.

The caller supplies already verified coordinate/context rows partitioned by
split.  Split membership is used only to stratify the cross-review draw; it is
never included in a reviewer-facing row or a per-site manifest assignment.
This module has no CLI and never reads extractor facts or hidden key records.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
from collections import defaultdict
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any, Final


SOURCE_FIELDS: Final = frozenset(
    {"site_id", "system", "path", "line", "column", "method", "context"}
)
PROHIBITED_FIELDS: Final = frozenset(
    {
        "key",
        "frames",
        "frame",
        "split",
        "extracted",
        "object",
        "tier",
        "atom_id",
        "predicate",
        "code_role",
        "expected_code_role",
        "label",
        "verdict",
    }
)
ASSIGNMENT_SCHEMA: Final = "t111-two-reviewer-assignment-v1"
CROSS_REVIEW_NUMERATOR: Final = 1
CROSS_REVIEW_DENOMINATOR: Final = 10

_INPUT_BINDING_RE = re.compile(r"sha256:[0-9a-f]{64}")
_SYSTEM_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*")
_SPLIT_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*")
_METHOD_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
_PRIMARY_DOMAIN = b"t111-reviewer-primary-assignment-v1"
_CROSS_DOMAIN = b"t111-reviewer-cross-review-rank-v1"


class ReviewerKitError(ValueError):
    """Reviewer-kit inputs or artifacts violate the fixed protocol."""


@dataclass(frozen=True)
class ReviewerKit:
    reviewer: str
    filename: str
    document: bytes
    document_sha256: str
    site_ids: tuple[str, ...]


@dataclass(frozen=True)
class ReviewerKitBundle:
    manifest: Mapping[str, Any]
    manifest_document: bytes
    manifest_sha256: str
    kits: tuple[ReviewerKit, ReviewerKit]


def load_source_partition(path: str | os.PathLike[str]) -> list[dict[str, Any]]:
    """Load one regular, non-symlink source-only JSONL partition."""

    source = Path(path)
    if source.is_symlink() or not source.is_file():
        raise ReviewerKitError(
            f"source partition must be a regular non-symlink file: {source}"
        )
    rows: list[dict[str, Any]] = []
    try:
        with source.open("r", encoding="utf-8", errors="strict") as handle:
            for line_number, line in enumerate(handle, start=1):
                if not line.strip():
                    raise ReviewerKitError(
                        f"{source}:{line_number}: blank JSONL records are forbidden"
                    )
                try:
                    value = json.loads(line)
                except json.JSONDecodeError as exc:
                    raise ReviewerKitError(
                        f"{source}:{line_number}: invalid JSON: {exc}"
                    ) from exc
                if not isinstance(value, dict):
                    raise ReviewerKitError(
                        f"{source}:{line_number}: source row must be an object"
                    )
                rows.append(value)
    except UnicodeError as exc:
        raise ReviewerKitError(f"{source}: source partition is not UTF-8") from exc
    return rows


def load_source_partitions(
    paths: Mapping[str, str | os.PathLike[str]],
) -> dict[str, list[dict[str, Any]]]:
    """Load every named partition, rejecting an absent or empty path map."""

    if not isinstance(paths, Mapping) or not paths:
        raise ReviewerKitError("source partition path map must be nonempty")
    loaded: dict[str, list[dict[str, Any]]] = {}
    for split, path in sorted(paths.items()):
        _canonical_split(split)
        loaded[split] = load_source_partition(path)
    return loaded


def validate_source_partitions(
    partitions: Mapping[str, Iterable[Mapping[str, Any]]],
    expected_partition_ids: Mapping[str, Iterable[str]],
) -> dict[str, tuple[dict[str, Any], ...]]:
    """Validate exact fields, unique IDs, and complete split populations."""

    if not isinstance(partitions, Mapping) or not partitions:
        raise ReviewerKitError("source partitions must be a nonempty mapping")
    if not isinstance(expected_partition_ids, Mapping) or not expected_partition_ids:
        raise ReviewerKitError("expected partition IDs must be a nonempty mapping")
    partition_names = set(partitions)
    expected_names = set(expected_partition_ids)
    if partition_names != expected_names:
        missing = sorted(expected_names - partition_names)
        extra = sorted(partition_names - expected_names)
        raise ReviewerKitError(
            f"incomplete partition set: missing={missing[:3]} extra={extra[:3]}"
        )

    result: dict[str, tuple[dict[str, Any], ...]] = {}
    globally_seen: dict[str, str] = {}
    for split in sorted(partition_names):
        _canonical_split(split)
        expected = _canonical_id_population(
            expected_partition_ids[split], f"expected partition {split}"
        )
        by_id: dict[str, dict[str, Any]] = {}
        rows = partitions[split]
        if isinstance(rows, (str, bytes, Mapping)):
            raise ReviewerKitError(f"partition {split} rows must be an iterable")
        for index, raw in enumerate(rows):
            row = validate_source_row(raw, f"partition {split} row {index}")
            site_id = row["site_id"]
            if site_id in by_id:
                raise ReviewerKitError(
                    f"duplicate site_id inside partition {split}: {site_id}"
                )
            if site_id in globally_seen:
                raise ReviewerKitError(
                    f"duplicate site_id across partitions {globally_seen[site_id]} "
                    f"and {split}: {site_id}"
                )
            globally_seen[site_id] = split
            by_id[site_id] = row
        actual = set(by_id)
        if actual != expected:
            missing = sorted(expected - actual)
            unknown = sorted(actual - expected)
            raise ReviewerKitError(
                f"partition {split} is incomplete: missing={missing[:3]} "
                f"unknown={unknown[:3]}"
            )
        result[split] = tuple(by_id[site_id] for site_id in sorted(by_id))
    return result


def validate_source_row(
    value: Mapping[str, Any], context: str = "source row"
) -> dict[str, Any]:
    """Validate the exact reviewer-visible source-only row schema."""

    if not isinstance(value, Mapping):
        raise ReviewerKitError(f"{context} must be an object")
    fields = set(value)
    if fields != SOURCE_FIELDS:
        prohibited = sorted(fields & PROHIBITED_FIELDS)
        if prohibited:
            raise ReviewerKitError(
                f"{context} contains prohibited hidden/outcome fields: {prohibited}"
            )
        raise ReviewerKitError(
            f"{context} fields must be exactly {sorted(SOURCE_FIELDS)}"
        )
    row = dict(value)
    site_id = _canonical_text(row["site_id"], f"{context}.site_id")
    system = _canonical_text(row["system"], f"{context}.system")
    if not _SYSTEM_RE.fullmatch(system):
        raise ReviewerKitError(f"{context}.system is not canonical")
    path = _canonical_text(row["path"], f"{context}.path")
    pure = PurePosixPath(path)
    if (
        "\\" in path
        or path.startswith("/")
        or str(pure) != path
        or any(part in ("", ".", "..") for part in pure.parts)
    ):
        raise ReviewerKitError(f"{context}.path must be a safe relative POSIX path")
    for field in ("line", "column"):
        number = row[field]
        if isinstance(number, bool) or not isinstance(number, int) or number <= 0:
            raise ReviewerKitError(f"{context}.{field} must be a positive integer")
    method = row["method"]
    if not isinstance(method, str) or not _METHOD_RE.fullmatch(method):
        raise ReviewerKitError(f"{context}.method must be a Go identifier")
    source_context = row["context"]
    if (
        not isinstance(source_context, str)
        or not source_context
        or "\x00" in source_context
        or "\r" in source_context
    ):
        raise ReviewerKitError(
            f"{context}.context must be nonempty canonical source text"
        )
    target_prefix = f"{row['line']:6d}| "
    target_lines = [
        line for line in source_context.split("\n") if line.startswith(target_prefix)
    ]
    if len(target_lines) != 1 or method not in target_lines[0]:
        raise ReviewerKitError(
            f"{context}.context does not contain the coordinate's method line"
        )
    row["site_id"] = site_id
    row["system"] = system
    row["path"] = path
    return row


def build_reviewer_kits(
    partitions: Mapping[str, Iterable[Mapping[str, Any]]],
    expected_partition_ids: Mapping[str, Iterable[str]],
    *,
    input_binding: str,
    reviewers: Sequence[str],
) -> ReviewerKitBundle:
    """Assign every source site to one primary and a stratified 10% overlap."""

    binding = _canonical_input_binding(input_binding)
    reviewer_names = _canonical_reviewers(reviewers)
    validated = validate_source_partitions(partitions, expected_partition_ids)

    sites: dict[str, dict[str, Any]] = {}
    site_split: dict[str, str] = {}
    strata: dict[tuple[str, str], list[str]] = defaultdict(list)
    for split, rows in validated.items():
        for row in rows:
            site_id = row["site_id"]
            sites[site_id] = row
            site_split[site_id] = split
            strata[(row["system"], split)].append(site_id)

    primary: dict[str, str] = {}
    for site_id in sorted(sites):
        choice = int.from_bytes(
            _rank(_PRIMARY_DOMAIN, binding, site_id), "big"
        ) % len(reviewer_names)
        primary[site_id] = reviewer_names[choice]

    cross_review: set[str] = set()
    stratum_manifest: list[dict[str, Any]] = []
    for (system, split), site_ids in sorted(strata.items()):
        count = (
            len(site_ids) * CROSS_REVIEW_NUMERATOR
            + CROSS_REVIEW_DENOMINATOR
            - 1
        ) // CROSS_REVIEW_DENOMINATOR
        ranked = sorted(
            site_ids,
            key=lambda site_id: (
                _rank(_CROSS_DOMAIN, binding, system, split, site_id),
                site_id,
            ),
        )
        selected = ranked[:count]
        cross_review.update(selected)
        stratum_manifest.append(
            {
                "system": system,
                "split": split,
                "population_size": len(site_ids),
                "cross_review_count": count,
            }
        )

    reviewer_rows: dict[str, list[dict[str, Any]]] = {
        reviewer: [] for reviewer in reviewer_names
    }
    assignments: list[dict[str, Any]] = []
    for site_id in sorted(sites):
        assigned = primary[site_id]
        reviewer_rows[assigned].append(sites[site_id])
        overlap = site_id in cross_review
        if overlap:
            other = reviewer_names[1] if assigned == reviewer_names[0] else reviewer_names[0]
            reviewer_rows[other].append(sites[site_id])
        assignments.append(
            {
                "site_id": site_id,
                "primary_reviewer": assigned,
                "cross_review": overlap,
            }
        )

    kit_values: list[ReviewerKit] = []
    kit_manifest: list[dict[str, Any]] = []
    for index, reviewer in enumerate(reviewer_names, start=1):
        rows = sorted(reviewer_rows[reviewer], key=lambda row: row["site_id"])
        document = canonical_jsonl(rows)
        filename = f"reviewer-{index}.jsonl"
        site_ids = tuple(row["site_id"] for row in rows)
        digest = sha256_bytes(document)
        kit_values.append(
            ReviewerKit(
                reviewer=reviewer,
                filename=filename,
                document=document,
                document_sha256=digest,
                site_ids=site_ids,
            )
        )
        kit_manifest.append(
            {
                "reviewer": reviewer,
                "filename": filename,
                "site_count": len(rows),
                "document_sha256": digest,
            }
        )

    source_binding_rows = [
        {"split": site_split[site_id], **sites[site_id]} for site_id in sorted(sites)
    ]
    manifest: dict[str, Any] = {
        "schema": ASSIGNMENT_SCHEMA,
        "input_binding": binding,
        "reviewers": list(reviewer_names),
        "primary_assignment": {
            "algorithm": "sha256-domain-modulo-two-v1",
            "domain": _PRIMARY_DOMAIN.decode(),
        },
        "cross_review": {
            "fraction": "1/10",
            "algorithm": "sha256-domain-stratified-rank-ceiling-v1",
            "domain": _CROSS_DOMAIN.decode(),
            "strata": stratum_manifest,
        },
        "source_population_size": len(sites),
        "source_population_sha256": sha256_bytes(
            canonical_jsonl(source_binding_rows)
        ),
        "assignments": assignments,
        "kits": kit_manifest,
    }
    manifest_document = canonical_json_document(manifest)
    bundle = ReviewerKitBundle(
        manifest=manifest,
        manifest_document=manifest_document,
        manifest_sha256=sha256_bytes(manifest_document),
        kits=(kit_values[0], kit_values[1]),
    )
    _validate_bundle(bundle, sites)
    return bundle


def freeze_reviewer_kits(
    output_directory: str | os.PathLike[str], bundle: ReviewerKitBundle
) -> str:
    """Publish one kit directory exactly once, rejecting symlinks/overwrite."""

    if not isinstance(bundle, ReviewerKitBundle):
        raise ReviewerKitError("bundle must be a ReviewerKitBundle")
    output = Path(output_directory)
    if output.is_symlink() or output.exists():
        raise FileExistsError(f"reviewer-kit output already exists: {output}")
    parent = output.parent
    if parent.is_symlink() or not parent.is_dir():
        raise ReviewerKitError(
            f"reviewer-kit parent must be a regular non-symlink directory: {parent}"
        )
    _validate_bundle(bundle)
    created: list[Path] = []
    try:
        os.mkdir(output, 0o700)
        for kit in bundle.kits:
            target = output / kit.filename
            _freeze_file(target, kit.document)
            created.append(target)
        manifest_path = output / "assignment-manifest.json"
        _freeze_file(manifest_path, bundle.manifest_document)
        created.append(manifest_path)
        digest_path = output / "assignment-manifest.sha256"
        _freeze_file(digest_path, (bundle.manifest_sha256 + "\n").encode("ascii"))
        created.append(digest_path)
        _fsync_directory(output)
        _fsync_directory(parent)
    except Exception:
        for path in reversed(created):
            try:
                path.unlink()
            except FileNotFoundError:
                pass
        try:
            output.rmdir()
        except FileNotFoundError:
            pass
        raise
    return bundle.manifest_sha256


def canonical_jsonl(rows: Iterable[Mapping[str, Any]]) -> bytes:
    """Serialize already ordered objects as compact canonical UTF-8 JSONL."""

    try:
        return b"".join(_canonical_json_line(dict(row)) for row in rows)
    except (TypeError, UnicodeEncodeError, ValueError) as exc:
        raise ReviewerKitError(f"rows are not canonical JSON data: {exc}") from exc


def canonical_json_document(value: Mapping[str, Any]) -> bytes:
    """Serialize one compact canonical UTF-8 JSON document with final LF."""

    try:
        return _canonical_json_line(dict(value))
    except (TypeError, UnicodeEncodeError, ValueError) as exc:
        raise ReviewerKitError(f"manifest is not canonical JSON data: {exc}") from exc


def sha256_bytes(value: bytes) -> str:
    if not isinstance(value, bytes):
        raise TypeError("sha256_bytes requires bytes")
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _validate_bundle(
    bundle: ReviewerKitBundle, source_sites: Mapping[str, Mapping[str, Any]] | None = None
) -> None:
    if len(bundle.kits) != 2:
        raise ReviewerKitError("bundle must contain exactly two reviewer kits")
    if sha256_bytes(bundle.manifest_document) != bundle.manifest_sha256:
        raise ReviewerKitError("assignment manifest digest mismatch")
    if canonical_json_document(bundle.manifest) != bundle.manifest_document:
        raise ReviewerKitError("assignment manifest is not canonical")
    manifest_kits = bundle.manifest.get("kits")
    if not isinstance(manifest_kits, list) or len(manifest_kits) != 2:
        raise ReviewerKitError("assignment manifest has an incomplete kit partition")
    reviewer_set: set[str] = set()
    kit_membership: dict[str, set[str]] = {}
    for kit, recorded in zip(bundle.kits, manifest_kits):
        if kit.reviewer in reviewer_set:
            raise ReviewerKitError("duplicate reviewer in bundle")
        reviewer_set.add(kit.reviewer)
        if sha256_bytes(kit.document) != kit.document_sha256:
            raise ReviewerKitError(f"reviewer kit digest mismatch: {kit.reviewer}")
        rows = _decode_kit_document(kit.document, kit.reviewer)
        ids = tuple(row["site_id"] for row in rows)
        if ids != kit.site_ids or len(ids) != len(set(ids)):
            raise ReviewerKitError(f"reviewer kit IDs mismatch: {kit.reviewer}")
        if recorded != {
            "reviewer": kit.reviewer,
            "filename": kit.filename,
            "site_count": len(rows),
            "document_sha256": kit.document_sha256,
        }:
            raise ReviewerKitError(f"reviewer kit manifest mismatch: {kit.reviewer}")
        kit_membership[kit.reviewer] = set(ids)
        if source_sites is not None:
            for row in rows:
                if source_sites.get(row["site_id"]) != row:
                    raise ReviewerKitError(
                        f"reviewer kit row differs from source: {row['site_id']}"
                    )

    assignments = bundle.manifest.get("assignments")
    if not isinstance(assignments, list):
        raise ReviewerKitError("assignment manifest has no assignments")
    seen: set[str] = set()
    for assignment in assignments:
        if not isinstance(assignment, dict) or set(assignment) != {
            "site_id",
            "primary_reviewer",
            "cross_review",
        }:
            raise ReviewerKitError("invalid assignment manifest row")
        site_id = assignment["site_id"]
        primary = assignment["primary_reviewer"]
        overlap = assignment["cross_review"]
        if (
            not isinstance(site_id, str)
            or site_id in seen
            or primary not in reviewer_set
            or not isinstance(overlap, bool)
        ):
            raise ReviewerKitError("invalid or duplicate site assignment")
        seen.add(site_id)
        present = {
            reviewer for reviewer, ids in kit_membership.items() if site_id in ids
        }
        expected = reviewer_set if overlap else {primary}
        if present != expected:
            raise ReviewerKitError(f"incomplete reviewer partition for {site_id}")
    if source_sites is not None and seen != set(source_sites):
        raise ReviewerKitError("assignment manifest omits source sites")


def _decode_kit_document(document: bytes, reviewer: str) -> list[dict[str, Any]]:
    try:
        text = document.decode("utf-8", errors="strict")
    except UnicodeError as exc:
        raise ReviewerKitError(f"reviewer kit is not UTF-8: {reviewer}") from exc
    rows: list[dict[str, Any]] = []
    for index, line in enumerate(text.splitlines()):
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ReviewerKitError(
                f"reviewer kit has invalid JSON: {reviewer}:{index + 1}"
            ) from exc
        rows.append(validate_source_row(value, f"reviewer kit {reviewer} row {index}"))
    if canonical_jsonl(rows) != document:
        raise ReviewerKitError(f"reviewer kit is not canonical JSONL: {reviewer}")
    if [row["site_id"] for row in rows] != sorted(row["site_id"] for row in rows):
        raise ReviewerKitError(f"reviewer kit is not site-ID sorted: {reviewer}")
    return rows


def _canonical_id_population(values: Iterable[str], context: str) -> set[str]:
    if isinstance(values, (str, bytes)):
        raise ReviewerKitError(f"{context} must be an iterable of site IDs")
    result: set[str] = set()
    for index, value in enumerate(values):
        site_id = _canonical_text(value, f"{context}[{index}]")
        if site_id in result:
            raise ReviewerKitError(f"{context} contains duplicate site_id {site_id}")
        result.add(site_id)
    return result


def _canonical_reviewers(values: Sequence[str]) -> tuple[str, str]:
    if isinstance(values, (str, bytes)) or len(values) != 2:
        raise ReviewerKitError("exactly two reviewer IDs are required")
    reviewers = tuple(
        sorted(_canonical_text(value, f"reviewers[{index}]") for index, value in enumerate(values))
    )
    if reviewers[0] == reviewers[1]:
        raise ReviewerKitError("reviewer IDs must be distinct")
    return reviewers


def _canonical_input_binding(value: Any) -> str:
    if not isinstance(value, str) or not _INPUT_BINDING_RE.fullmatch(value):
        raise ReviewerKitError("input_binding must be sha256:<64 lowercase hex>")
    return value


def _canonical_split(value: Any) -> str:
    if not isinstance(value, str) or not _SPLIT_RE.fullmatch(value):
        raise ReviewerKitError(f"invalid split name: {value!r}")
    return value


def _canonical_text(value: Any, context: str) -> str:
    if (
        not isinstance(value, str)
        or not value
        or value != value.strip()
        or any(ord(char) < 0x20 or ord(char) == 0x7F for char in value)
    ):
        raise ReviewerKitError(f"{context} must be a canonical nonempty string")
    return value


def _rank(domain: bytes, *parts: str) -> bytes:
    digest = hashlib.sha256()
    digest.update(len(domain).to_bytes(4, "big"))
    digest.update(domain)
    for part in parts:
        encoded = part.encode("utf-8", errors="strict")
        digest.update(len(encoded).to_bytes(8, "big"))
        digest.update(encoded)
    return digest.digest()


def _canonical_json_line(value: Mapping[str, Any]) -> bytes:
    return (
        json.dumps(
            value,
            ensure_ascii=False,
            allow_nan=False,
            sort_keys=True,
            separators=(",", ":"),
        )
        + "\n"
    ).encode("utf-8", errors="strict")


def _freeze_file(path: Path, document: bytes) -> None:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd: int | None = None
    created = False
    try:
        fd = os.open(path, flags, 0o600)
        created = True
        os.fchmod(fd, 0o600)
        view = memoryview(document)
        written = 0
        while written < len(view):
            count = os.write(fd, view[written:])
            if count <= 0:
                raise OSError("short write while freezing reviewer kit")
            written += count
        os.fsync(fd)
        os.close(fd)
        fd = None
    except Exception:
        if fd is not None:
            os.close(fd)
        if created:
            try:
                path.unlink()
            except FileNotFoundError:
                pass
        raise


def _fsync_directory(path: Path) -> None:
    flags = os.O_RDONLY
    if hasattr(os, "O_DIRECTORY"):
        flags |= os.O_DIRECTORY
    fd = os.open(path, flags)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
