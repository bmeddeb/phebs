"""Shared scoring discipline for validation rounds.

Consumes the frozen pilot harness's canonical score shape and the sealed
parameter record, and encodes the rules that must never be softened between
protocols: a zero denominator is ``not_measured`` and never a pass; a round
that stopped, failed or never ran cannot carry a measured verdict; a composed
claim is directly measured over its own frame rather than multiplied from
component metrics; and every result names a threshold that was sealed before
the round started. This module performs no sampling, labels nothing and never
scores a real round by itself.
"""

from __future__ import annotations

import math
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

_DIR = Path(__file__).resolve().parent
if str(_DIR) not in sys.path:
    sys.path.insert(0, str(_DIR))

import harness as pilot_harness  # noqa: E402  (frozen pilot scoring primitive)

PARAMETERS_SCHEMA = "harness-sealed-parameters-v1"
OUTCOMES = ("success", "failed", "stop", "not_run")
RESULTS = ("pass", "conditional", "fail", "not_measured")
_DIGEST_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")
_SCORE_FIELDS = {"schema", "field", "sampled", "yes", "no", "unsure", "decided"}
_DECIDED_FIELDS = {"proportion", "wilson_low", "wilson_high"}


class ScoringError(ValueError):
    """A score, threshold, sealed parameter or composition breaks a fixed rule."""


def _number(value: Any, context: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ScoringError(f"{context} must be a finite number")
    return float(value)


def _integer(value: Any, context: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ScoringError(f"{context} must be a nonnegative integer")
    return value


def _threshold(value: Any, context: str) -> dict:
    if not isinstance(value, Mapping) or set(value) not in ({"kind", "value"}, {"kind", "value", "conditional_value"}):
        raise ScoringError(f"{context}: threshold needs kind, value and an optional conditional_value")
    kind = value["kind"]
    if kind not in ("min", "max"):
        raise ScoringError(f"{context}: threshold kind must be 'min' or 'max'")
    result = {"kind": kind, "value": _number(value["value"], f"{context}.value")}
    if "conditional_value" in value:
        conditional = _number(value["conditional_value"], f"{context}.conditional_value")
        weaker = conditional < result["value"] if kind == "min" else conditional > result["value"]
        if not weaker:
            raise ScoringError(f"{context}: the conditional band must be strictly weaker than the pass threshold")
        result["conditional_value"] = conditional
    return result


def require_sealed_parameters(value: Any) -> dict:
    """Validate one sealed parameter record; nothing unsealed may score."""
    if not isinstance(value, Mapping) or set(value) != {"schema", "status", "claims"}:
        raise ScoringError("parameters: missing or unknown required fields")
    if value["schema"] != PARAMETERS_SCHEMA:
        raise ScoringError(f"parameters are not {PARAMETERS_SCHEMA}")
    if value["status"] != "sealed":
        raise ScoringError(f"parameters are {value['status']!r}, not sealed")
    claims = value["claims"]
    if not isinstance(claims, Mapping) or not claims:
        raise ScoringError("sealed parameters must declare at least one claim threshold")
    sealed: dict[str, dict] = {}
    for claim, threshold in claims.items():
        if not isinstance(claim, str) or not claim.strip():
            raise ScoringError("claim names must be non-empty strings")
        if claim in sealed:
            raise ScoringError(f"duplicate claim {claim!r}")
        sealed[claim] = _threshold(threshold, f"claims[{claim}]")
    return {"schema": PARAMETERS_SCHEMA, "status": "sealed", "claims": sealed}


def sealed_threshold(parameters: Any, claim: str) -> dict:
    parameters = require_sealed_parameters(parameters)
    if claim not in parameters["claims"]:
        raise ScoringError(f"claim {claim!r} has no sealed threshold")
    return dict(parameters["claims"][claim])


def require_claim_coverage(parameters: Any, claims: Sequence[str]) -> None:
    """Refuse any measured-claim set that is not exactly the sealed one."""
    parameters = require_sealed_parameters(parameters)
    declared = sorted(parameters["claims"])
    measured = sorted(claims)
    if measured != declared:
        missing = sorted(set(declared) - set(measured))
        extra = sorted(set(measured) - set(declared))
        raise ScoringError(f"measured claims must exactly cover the sealed claims: missing={missing} extra={extra}")


def verdict_for(estimate: Any, threshold: Any) -> str:
    """The sealed threshold's verdict for one estimate; absent means ``not_measured``."""
    threshold = _threshold(threshold, "threshold")
    if estimate is None:
        return "not_measured"
    estimate = _number(estimate, "estimate")
    if threshold["kind"] == "min":
        if estimate >= threshold["value"]:
            return "pass"
        if "conditional_value" in threshold and estimate >= threshold["conditional_value"]:
            return "conditional"
        return "fail"
    if estimate <= threshold["value"]:
        return "pass"
    if "conditional_value" in threshold and estimate <= threshold["conditional_value"]:
        return "conditional"
    return "fail"


def _validated_score(score: Any) -> dict:
    if not isinstance(score, Mapping):
        raise ScoringError("score must be a mapping")
    fields = set(score)
    if score.get("schema") != pilot_harness.HARNESS_SCHEMA:
        raise ScoringError(f"score is not {pilot_harness.HARNESS_SCHEMA}")
    want = _SCORE_FIELDS | (_DECIDED_FIELDS if "proportion" in score else set())
    if fields != want:
        raise ScoringError(f"score fields must be exactly {sorted(want)}")
    field = score["field"]
    if not isinstance(field, str) or not field.strip():
        raise ScoringError("score field must be a non-empty string")
    yes = _integer(score["yes"], "score.yes")
    no = _integer(score["no"], "score.no")
    unsure = _integer(score["unsure"], "score.unsure")
    sampled = _integer(score["sampled"], "score.sampled")
    decided_count = _integer(score["decided"], "score.decided")
    if decided_count != yes + no:
        raise ScoringError("score.decided must equal yes + no")
    if sampled != decided_count + unsure:
        raise ScoringError("score.sampled must equal decided + unsure")
    if decided_count > 0:
        proportion = _number(score["proportion"], "score.proportion")
        low = _number(score["wilson_low"], "score.wilson_low")
        high = _number(score["wilson_high"], "score.wilson_high")
        if not 0.0 <= low <= proportion <= high <= 1.0:
            raise ScoringError("score interval must satisfy 0 <= low <= proportion <= high <= 1")
    elif "proportion" in score:
        raise ScoringError("a zero denominator carries no proportion or interval")
    return dict(score)


def quality_row(score: Any, parameters: Any, *, outcome: str) -> dict:
    """One §9-shaped validation row: counts, estimate, interval, threshold, result.

    Underpowered, absent or non-successful measurement is reported as
    ``not_measured`` with its real denominator visible; it is never converted
    into a pass.
    """
    if outcome not in OUTCOMES:
        raise ScoringError(f"outcome must be one of {OUTCOMES}")
    score = _validated_score(score)
    threshold = sealed_threshold(parameters, score["field"])
    row = {
        "claim": score["field"],
        "sampled": score["sampled"],
        "decided": score["decided"],
        "yes": score["yes"],
        "no": score["no"],
        "unsure": score["unsure"],
        "estimate": None,
        "wilson_low": None,
        "wilson_high": None,
        "threshold": threshold,
        "result": "not_measured",
    }
    if score["decided"] > 0:
        row["estimate"] = score["proportion"]
        row["wilson_low"] = score["wilson_low"]
        row["wilson_high"] = score["wilson_high"]
    if outcome == "success" and score["decided"] > 0:
        row["result"] = verdict_for(row["estimate"], threshold)
    return row


def require_direct_measurement(record: Any) -> dict:
    """Refuse a composed claim that was multiplied from component metrics.

    A composed accuracy is only admissible when it was measured directly over
    its own sealed frame; a record naming component metrics is not a
    measurement and cannot become one by arithmetic.
    """
    if not isinstance(record, Mapping) or set(record) != {"provenance", "frame_sha256", "sample_size"}:
        raise ScoringError(
            "a direct measurement record carries exactly provenance, frame_sha256 and sample_size"
        )
    if record["provenance"] != "direct":
        raise ScoringError(
            f"composed claims must be directly measured; {record['provenance']!r} is not a measurement"
        )
    if not isinstance(record["frame_sha256"], str) or not _DIGEST_RE.match(record["frame_sha256"]):
        raise ScoringError("frame_sha256 must be a tagged sha256 digest")
    sample_size = _integer(record["sample_size"], "sample_size")
    if sample_size < 1:
        raise ScoringError("a direct measurement needs at least one sampled site")
    return dict(record)


def stratum_table_digest(rows: Sequence[Mapping[str, Any]]) -> str:
    """The digest a scoring record binds for its per-stratum table."""
    if isinstance(rows, (str, bytes)) or not isinstance(rows, Sequence) or not rows:
        raise ScoringError("stratum table must be a non-empty sequence of rows")
    for row in rows:
        if not isinstance(row, Mapping):
            raise ScoringError("stratum table rows must be mappings")
    try:
        return pilot_harness.rows_commitment([dict(row) for row in rows])
    except ValueError as exc:
        raise ScoringError(f"stratum table is not canonically encodable: {exc}") from exc
