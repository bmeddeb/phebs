"""One quality-and-operating result envelope per pack per run.

A pack result binds exactly one pack to exactly one run: the run binding
(source commit and sealed corpus lock), the sealed parameter digest, the
quality rows with their visible denominators and sealed thresholds, the
cost receipts of that run, and the run's honest outcome. Rules that hold no
matter which protocol assembled the rows: a pack whose run failed, stopped
or never ran carries only ``not_measured`` rows; a pass needs a positive
denominator, a successful run and a sealed threshold; the operating
envelope is either measured with receipts or explains why it is not; and
there are no joint results spanning packs or runs. Writing is exclusive,
so a recorded result cannot be restated.
"""

from __future__ import annotations

import os
import re
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

import receipts as receipt_mod
import scoring
from corpus import canonical_bytes, digest_of, validate_binding

RESULT_SCHEMA = "harness-pack-result-v1"
_PACK_RE = re.compile(r"\Aphebs(?:\.[a-z0-9]+(?:-[a-z0-9]+)*)+\Z")
_DIGEST_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")
_UTC_RE = re.compile(r"\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")
_ROW_FIELDS = {"claim", "sampled", "decided", "yes", "no", "unsure", "estimate",
               "wilson_low", "wilson_high", "threshold", "result"}


class ResultError(ValueError):
    """A pack result breaks run binding, coverage, outcome or envelope rules."""


def _integer(value: Any, context: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ResultError(f"{context} must be a nonnegative integer")
    return value


def _expected_result(row: Mapping[str, Any], outcome: str) -> str:
    """The only verdict this row may carry under this run outcome."""
    if outcome != "success" or row["decided"] == 0:
        return "not_measured"
    try:
        return scoring.verdict_for(row["estimate"], row["threshold"])
    except scoring.ScoringError as exc:
        raise ResultError(f"{row['claim']}: threshold or estimate is invalid: {exc}") from exc


def _validated_row(value: Any, outcome: str) -> dict:
    if not isinstance(value, Mapping) or set(value) != _ROW_FIELDS:
        raise ResultError("quality row: missing or unknown required fields")
    if not isinstance(value["claim"], str) or not value["claim"].strip():
        raise ResultError("quality row claim must be a non-empty string")
    yes = _integer(value["yes"], "row.yes")
    no = _integer(value["no"], "row.no")
    unsure = _integer(value["unsure"], "row.unsure")
    sampled = _integer(value["sampled"], "row.sampled")
    decided = _integer(value["decided"], "row.decided")
    if decided != yes + no or sampled != decided + unsure:
        raise ResultError(f"{value['claim']}: counts disagree with sampled/decided")
    if decided == 0:
        if value["estimate"] is not None or value["wilson_low"] is not None or value["wilson_high"] is not None:
            raise ResultError(f"{value['claim']}: a zero denominator carries no estimate or interval")
    elif not isinstance(value["estimate"], (int, float)) or isinstance(value["estimate"], bool):
        raise ResultError(f"{value['claim']}: a measured row carries its estimate")
    if outcome not in scoring.OUTCOMES:
        raise ResultError(f"outcome must be one of {scoring.OUTCOMES}")
    if value["result"] not in scoring.RESULTS:
        raise ResultError(f"{value['claim']}: result must be one of {scoring.RESULTS}")
    expected = _expected_result(value, outcome)
    if value["result"] != expected:
        raise ResultError(
            f"{value['claim']}: result {value['result']!r} contradicts a {outcome!r} run ({expected!r})"
        )
    return dict(value)


def assemble_pack_result(
    pack_id: str,
    *,
    outcome: str,
    parameters: Mapping[str, Any],
    run_binding: Mapping[str, Any],
    quality_rows: Sequence[Mapping[str, Any]],
    receipts: Sequence[Mapping[str, Any]] = (),
    operating_reason: str | None = None,
    created_utc: str | None = None,
) -> dict:
    """Assemble one pack's result; every rule here refuses rather than repairs."""
    if not isinstance(pack_id, str) or not _PACK_RE.match(pack_id):
        raise ResultError("pack_id must be a namespaced phebs pack identifier")
    if outcome not in scoring.OUTCOMES:
        raise ResultError(f"outcome must be one of {scoring.OUTCOMES}")
    try:
        sealed = scoring.require_sealed_parameters(parameters)
    except scoring.ScoringError as exc:
        raise ResultError(f"parameters: {exc}") from exc
    binding = validate_binding(run_binding)
    binding_digest = digest_of(binding)
    rows = [_validated_row(row, outcome) for row in quality_rows]
    try:
        scoring.require_claim_coverage(sealed, [row["claim"] for row in rows])
    except scoring.ScoringError as exc:
        raise ResultError(str(exc)) from exc
    for row in rows:
        if row["threshold"] != scoring.sealed_threshold(sealed, row["claim"]):
            raise ResultError(f"{row['claim']}: row threshold is not the sealed threshold")
    checked_receipts = []
    for receipt in receipts:
        receipt = receipt_mod.validate_receipt(receipt)
        if receipt["run_binding_sha256"] != binding_digest:
            raise ResultError(f"step {receipt['step']!r} receipt is not bound to this run")
        checked_receipts.append(receipt)
    if checked_receipts:
        if operating_reason is not None:
            raise ResultError("a measured operating envelope carries no unmeasured reason")
    elif not isinstance(operating_reason, str) or not operating_reason.strip():
        raise ResultError("an unmeasured operating envelope must name why it is unmeasured")
    created = created_utc or receipt_mod.utc_now()
    if not isinstance(created, str) or not _UTC_RE.match(created):
        raise ResultError("created_utc must be an RFC3339 UTC second")
    result = {
        "schema": RESULT_SCHEMA,
        "pack_id": pack_id,
        "created_utc": created,
        "run_binding": binding,
        "outcome": outcome,
        "quality": {"parameters_sha256": digest_of(sealed), "rows": rows},
        "operating": {
            "measured": bool(checked_receipts),
            "receipts": checked_receipts,
            "reason": None if checked_receipts else operating_reason,
        },
    }
    return validate_pack_result(result)


def validate_pack_result(value: Any) -> dict:
    if not isinstance(value, Mapping) or set(value) != {
        "schema", "pack_id", "created_utc", "run_binding", "outcome", "quality", "operating",
    }:
        raise ResultError("pack result: missing or unknown required fields")
    if value["schema"] != RESULT_SCHEMA:
        raise ResultError(f"pack result is not {RESULT_SCHEMA}")
    if not isinstance(value["pack_id"], str) or not _PACK_RE.match(value["pack_id"]):
        raise ResultError("pack_id must be a namespaced phebs pack identifier")
    if not isinstance(value["created_utc"], str) or not _UTC_RE.match(value["created_utc"]):
        raise ResultError("created_utc must be an RFC3339 UTC second")
    binding = validate_binding(value["run_binding"])
    binding_digest = digest_of(binding)
    outcome = value["outcome"]
    quality = value["quality"]
    if not isinstance(quality, Mapping) or set(quality) != {"parameters_sha256", "rows"}:
        raise ResultError("quality block: missing or unknown required fields")
    if not isinstance(quality["parameters_sha256"], str) or not _DIGEST_RE.match(quality["parameters_sha256"]):
        raise ResultError("quality parameters digest must be a tagged sha256 digest")
    if not isinstance(quality["rows"], list) or not quality["rows"]:
        raise ResultError("quality rows must be a non-empty list")
    rows = [_validated_row(row, outcome) for row in quality["rows"]]
    if len({row["claim"] for row in rows}) != len(rows):
        raise ResultError("quality rows must name each claim exactly once")
    operating = value["operating"]
    if not isinstance(operating, Mapping) or set(operating) != {"measured", "receipts", "reason"}:
        raise ResultError("operating block: missing or unknown required fields")
    if not isinstance(operating["measured"], bool):
        raise ResultError("operating.measured must be a boolean")
    if not isinstance(operating["receipts"], list):
        raise ResultError("operating.receipts must be a list")
    checked = []
    for receipt in operating["receipts"]:
        receipt = receipt_mod.validate_receipt(receipt)
        if receipt["run_binding_sha256"] != binding_digest:
            raise ResultError(f"step {receipt['step']!r} receipt is not bound to this run")
        checked.append(receipt)
    reason = operating["reason"]
    if operating["measured"]:
        if not checked or reason is not None:
            raise ResultError("a measured operating envelope carries receipts and no reason")
    elif checked or not isinstance(reason, str) or not reason.strip():
        raise ResultError("an unmeasured operating envelope is empty and names why")
    return dict(value)


def write_pack_result(path: str | os.PathLike[str], result: Mapping[str, Any]) -> str:
    """Write one result exclusively; return its tagged digest. History is append-only."""
    result = validate_pack_result(result)
    data = canonical_bytes(result)
    target = Path(path)
    try:
        fd = os.open(target, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError:
        raise ResultError(f"a pack result already exists at {target}; results are written once") from None
    try:
        os.write(fd, data)
        os.fsync(fd)
    finally:
        os.close(fd)
    receipt_mod.fsync_dir(target.parent)
    return digest_of(result)
