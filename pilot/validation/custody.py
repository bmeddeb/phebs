"""Blind label custody and round-state discipline for validation rounds.

One ordered, append-only state record carries a round from preregistration to
scoring; every transition names the digest of the artifact that justifies it,
and disclosure of candidate-derived predictions is impossible before the
blind-label commitment is published. This module encodes no protocol
semantics and never validates, freezes, commits or scores labels itself —
those stay with the frozen t111 label machinery and each protocol's own
round. Machinery only: a well-formed state record is not a seal and makes no
claim.
"""

from __future__ import annotations

import os
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

_T111_DIR = Path(__file__).resolve().parents[2] / "spike" / "t111"
if str(_T111_DIR) not in sys.path:
    sys.path.insert(0, str(_T111_DIR))

import label_protocol as protocol  # noqa: E402  (path bound above; frozen label keeper)

ROUND_SCHEMA = "harness-round-state-v1"
DIGEST_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")
_UTC_RE = re.compile(r"\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")

# Ordered stages; each non-drafted transition binds the digest of the artifact
# that justifies it, so a short round stays exactly as short as it is.
STAGES = (
    "drafted",
    "parameters_approved",
    "corpus_sealed",
    "frames_frozen",
    "seed_bound",
    "sample_drawn",
    "labels_frozen",
    "commitment_published",
    "predictions_disclosed",
    "scored",
)


class CustodyError(ValueError):
    """A round-state record, stage transition or custody separation is invalid."""


def _text(value: Any, context: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise CustodyError(f"{context} must be a non-empty string")
    return value


def _utc(value: Any, context: str) -> str:
    if not isinstance(value, str) or not _UTC_RE.match(value):
        raise CustodyError(f"{context} must be an RFC3339 UTC second")
    return value


def _digest(value: Any, context: str) -> str:
    if not isinstance(value, str) or not DIGEST_RE.match(value):
        raise CustodyError(f"{context} must be a tagged sha256 digest")
    return value


def validate_round_state(value: Any) -> dict:
    """Structurally validate one round-state record; no stage is inferred."""
    if not isinstance(value, Mapping) or set(value) != {"schema", "round_id", "transitions"}:
        raise CustodyError("round state: missing or unknown required fields")
    if value["schema"] != ROUND_SCHEMA:
        raise CustodyError(f"round state is not {ROUND_SCHEMA}")
    _text(value["round_id"], "round_id")
    transitions = value["transitions"]
    if not isinstance(transitions, list) or not transitions:
        raise CustodyError("round state must carry at least the drafted transition")
    previous_utc = ""
    for index, step in enumerate(transitions):
        if not isinstance(step, Mapping) or set(step) != {"stage", "utc", "binding"}:
            raise CustodyError(f"transition {index}: missing or unknown required fields")
        if step["stage"] != STAGES[index]:
            raise CustodyError("transitions must follow the ordered stages exactly once")
        utc = _utc(step["utc"], f"transition[{step['stage']}].utc")
        if utc < previous_utc:
            raise CustodyError("transition timestamps must not decrease")
        previous_utc = utc
        if step["stage"] == "drafted":
            if step["binding"] is not None:
                raise CustodyError("the drafted transition binds no artifact")
        else:
            _digest(step["binding"], f"transition[{step['stage']}].binding")
    return dict(value)


def new_round(round_id: str, utc: str) -> dict:
    _text(round_id, "round_id")
    _utc(utc, "utc")
    return validate_round_state(
        {"schema": ROUND_SCHEMA, "round_id": round_id, "transitions": [{"stage": "drafted", "utc": utc, "binding": None}]}
    )


def current_stage(record: Mapping[str, Any]) -> str:
    return validate_round_state(record)["transitions"][-1]["stage"]


def advance(record: Mapping[str, Any], stage: str, utc: str, binding: str) -> dict:
    """Return the record advanced by exactly one stage; nothing is skipped."""
    record = validate_round_state(record)
    if stage not in STAGES or stage == "drafted":
        raise CustodyError(f"{stage!r} is not an advanceable stage")
    expected = STAGES[STAGES.index(current_stage(record)) + 1] if current_stage(record) != STAGES[-1] else None
    if stage != expected:
        raise CustodyError(f"next stage is {expected!r}, not {stage!r}")
    _utc(utc, "utc")
    _digest(binding, f"transition[{stage}].binding")
    transitions = [dict(step) for step in record["transitions"]]
    if utc < transitions[-1]["utc"]:
        raise CustodyError("transition timestamps must not decrease")
    transitions.append({"stage": stage, "utc": utc, "binding": binding})
    return validate_round_state({"schema": ROUND_SCHEMA, "round_id": record["round_id"], "transitions": transitions})


def require_stage(record: Mapping[str, Any], minimum: str) -> None:
    """Fail closed unless the round already reached ``minimum``."""
    record = validate_round_state(record)
    if minimum not in STAGES:
        raise CustodyError(f"unknown stage {minimum!r}")
    if STAGES.index(current_stage(record)) < STAGES.index(minimum):
        raise CustodyError(f"round {record['round_id']!r} is at {current_stage(record)!r}, before {minimum!r}")


def require_scorable(record: Mapping[str, Any]) -> None:
    require_stage(record, "predictions_disclosed")


def require_separation(labels_path: str | os.PathLike[str], predictions_paths: Sequence[str | os.PathLike[str]]) -> None:
    """Refuse any label location that meets a prediction location.

    The frozen blind-label document may not be, contain, or sit inside any
    candidate-derived prediction path, so custody and prediction custody can
    never silently become the same store.
    """
    labels = Path(labels_path).resolve()
    if isinstance(predictions_paths, (str, bytes)) or not isinstance(predictions_paths, Sequence):
        raise CustodyError("predictions_paths must be a sequence of paths")
    for raw in predictions_paths:
        predictions = Path(raw).resolve()
        if labels == predictions or labels in predictions.parents or predictions in labels.parents:
            raise CustodyError(f"labels path {labels} meets prediction path {predictions}")


def label_commitment_binding(commitment: Mapping[str, Any]) -> str:
    """The digest a commitment_published transition binds, via the frozen keeper."""
    document = protocol.canonical_label_commitment_document(commitment)
    return protocol.sha256_bytes(document)


def labels_binding(rows: Any, known_ids: Any) -> str:
    """The digest a labels_frozen transition binds, via the frozen keeper."""
    return protocol.canonical_labels_sha256(rows, known_ids)
