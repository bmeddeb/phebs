"""Gate-neutral accuracy-gold harness for the pilot internal validation.

Mechanical machinery only: proof-bundle candidate extraction, deterministic
seeded sampling, preregistered sample-size and confidence computations, and
fail-closed scoring against adjudicated labels. It reuses the sealed GATE2-V2
label machinery (`spike/t111/label_protocol.py`) for label validation,
freezing, commitments, and receipts rather than re-implementing them.

Running this harness establishes nothing. Thresholds, frames, and every
sealing step are owned by docs/ACCURACY_GOLD_PROTOCOL.md and the charter's
Gate 0 authority; scores produced outside a sealed, preregistered round carry
no claim.
"""

from __future__ import annotations

import hashlib
import json
import math
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

_SPIKE_DIR = Path(__file__).resolve().parents[2] / "spike" / "t111"
if str(_SPIKE_DIR) not in sys.path:
    sys.path.insert(0, str(_SPIKE_DIR))

import label_protocol as protocol  # noqa: E402  (path bound above)

HARNESS_SCHEMA = "pilot-accuracy-gold-harness-v1"

_CANDIDATE_PREDICATES = frozenset(
    {
        "CALLS_OPERATION",
        "REGISTERS_GRPC_SERVICE",
        "REFERENCES_PROTO_FIELD",
        "UNRESOLVED_GRPC_CALL",
    }
)
_SEED_RE = re.compile(r"\A[0-9a-f]{64}\Z")


class HarnessError(ValueError):
    """Raised for any inconsistent, unbounded, or incomplete input."""


def candidate_rows(envelope: Mapping[str, Any]) -> list[dict[str, Any]]:
    """Project one proof-bundle envelope into sorted candidate rows.

    Every assertion occurrence becomes one row whose ``site_id`` is the
    immutable citation coordinate ``repository@commit:path:start-end``. The
    projection is fail-closed: unknown predicates, missing evidence, or
    inconsistent scopes raise instead of dropping rows silently.
    """
    if not isinstance(envelope, Mapping):
        raise HarnessError("envelope must be a mapping")
    bundle = envelope.get("bundle")
    if not isinstance(bundle, Mapping):
        raise HarnessError("envelope has no bundle")
    evidence: dict[tuple[str, str, str], Mapping[str, Any]] = {}
    for item in _sequence(bundle.get("evidence"), "bundle.evidence"):
        atom = item.get("atom")
        if not isinstance(atom, Mapping):
            raise HarnessError("evidence entry has no atom")
        key = (
            _text(item.get("repository"), "evidence.repository"),
            _text(item.get("run_id"), "evidence.run_id"),
            _text(atom.get("id"), "evidence.atom.id"),
        )
        if key in evidence:
            raise HarnessError(f"duplicate evidence identity {key}")
        evidence[key] = item

    rows: list[dict[str, Any]] = []
    seen: set[str] = set()
    for assertion in _sequence(bundle.get("assertions"), "bundle.assertions"):
        predicate = _text(assertion.get("predicate"), "assertion.predicate")
        if predicate not in _CANDIDATE_PREDICATES:
            raise HarnessError(f"unsupported candidate predicate {predicate!r}")
        repo = _text(assertion.get("repo"), "assertion.repo")
        run_id = _text(assertion.get("run_id"), "assertion.run_id")
        for atom_id in _sequence(assertion.get("supporting"), "assertion.supporting"):
            atom_id = _text(atom_id, "assertion.supporting[]")
            item = evidence.get((repo, run_id, atom_id))
            if item is None:
                raise HarnessError(f"assertion {assertion.get('id')!r} cites absent evidence {atom_id}")
            atom = item["atom"]
            start, end = atom.get("start_byte"), atom.get("end_byte")
            if not isinstance(start, int) or not isinstance(end, int) or start < 0 or end <= start:
                raise HarnessError(f"atom {atom_id} span is inconsistent")
            for occurrence in _sequence(item.get("occurrences"), "evidence.occurrences"):
                if occurrence.get("repo") != repo or occurrence.get("run_id") != run_id:
                    raise HarnessError(f"occurrence scope mismatch for atom {atom_id}")
                commit = _text(occurrence.get("commit"), "occurrence.commit")
                path = _text(occurrence.get("path"), "occurrence.path")
                site_id = f"{repo}@{commit}:{path}:{start}-{end}"
                dedupe = site_id + "\x00" + predicate + "\x00" + _text(assertion.get("object"), "assertion.object")
                if dedupe in seen:
                    continue
                seen.add(dedupe)
                rows.append(
                    {
                        "site_id": site_id,
                        "predicate": predicate,
                        "object": assertion.get("object"),
                        "lineage": assertion.get("lineage", ""),
                        "tier": assertion.get("tier", ""),
                        "code_role": assertion.get("code_role", ""),
                        "repository": repo,
                        "path": path,
                        "start_line": occurrence.get("start_line"),
                        "end_line": occurrence.get("end_line"),
                    }
                )
    rows.sort(key=lambda row: (row["site_id"], row["predicate"], str(row["object"])))
    return rows


def rows_commitment(rows: Sequence[Mapping[str, Any]]) -> str:
    """Return the sha256 commitment of the canonical JSONL row encoding."""
    body = b"".join(
        json.dumps(dict(row), ensure_ascii=False, allow_nan=False, sort_keys=True, separators=(",", ":")).encode(
            "utf-8"
        )
        + b"\n"
        for row in rows
    )
    return protocol.sha256_bytes(body)


def stratified_sample(
    strata: Mapping[str, Sequence[str]],
    sizes: Mapping[str, int],
    seed_hex: str,
) -> dict[str, list[str]]:
    """Deterministic per-stratum sample: lowest sha256(seed || site_id) ranks.

    The seed is a 64-hex-character public randomness value (the protocol binds
    it to a NIST beacon pulse recorded before labeling). Identical inputs
    always select identical samples, so a third party can re-derive the draw.
    """
    if not _SEED_RE.match(seed_hex or ""):
        raise HarnessError("seed must be 64 lowercase hex characters")
    if set(sizes) - set(strata):
        raise HarnessError("sample size names a stratum that does not exist")
    sample: dict[str, list[str]] = {}
    for name in sorted(strata):
        members = list(strata[name])
        if len(set(members)) != len(members):
            raise HarnessError(f"stratum {name!r} contains duplicate site IDs")
        want = sizes.get(name, 0)
        if want < 0 or want > len(members):
            raise HarnessError(f"stratum {name!r} cannot supply {want} of {len(members)} members")
        ranked = sorted(
            members,
            key=lambda site_id: hashlib.sha256((seed_hex + "\x00" + site_id).encode("utf-8")).hexdigest(),
        )
        sample[name] = sorted(ranked[:want])
    return sample


def minimum_sample_size(margin: float, z: float = 1.96, proportion: float = 0.5) -> int:
    """Conservative preregistration sample size for one proportion."""
    if not 0 < margin < 1 or z <= 0 or not 0 < proportion < 1:
        raise HarnessError("margin and proportion must be in (0,1) and z positive")
    return math.ceil((z * z * proportion * (1 - proportion)) / (margin * margin))


def wilson_interval(successes: int, trials: int, z: float = 1.96) -> tuple[float, float]:
    """Two-sided Wilson score interval, the protocol's preregistered method."""
    if trials <= 0 or successes < 0 or successes > trials or z <= 0:
        raise HarnessError("interval requires 0 <= successes <= trials and positive z")
    p = successes / trials
    denominator = 1 + z * z / trials
    center = (p + z * z / (2 * trials)) / denominator
    half = z * math.sqrt(p * (1 - p) / trials + z * z / (4 * trials * trials)) / denominator
    return (max(0.0, center - half), min(1.0, center + half))


def score_claim(
    sampled_site_ids: Sequence[str],
    labels: Sequence[Mapping[str, Any]],
    field: str,
    z: float = 1.96,
) -> dict[str, Any]:
    """Score one claim field over one sealed sample, fail-closed.

    Every sampled site must carry exactly one adjudicated label; ``unsure``
    labels are excluded from both numerator and denominator but always
    reported, per the protocol's missing/unlabelable rule. Any missing or
    surplus label invalidates the round instead of degrading it.

    Only the two decision fields of the sealed t111 label schema are
    scorable today; the field-reference round requires the versioned schema
    extension named in docs/ACCURACY_GOLD_PROTOCOL.md before it can seal.
    """
    if field not in ("invocation", "registration"):
        raise HarnessError("field must be a t111 label decision field: invocation or registration")
    validated = protocol.validate_labels(labels, sampled_site_ids)
    by_site = {row["site_id"]: row for row in validated}
    if len(by_site) != len(validated):
        raise HarnessError("labels contain duplicate site IDs")
    if set(by_site) != set(sampled_site_ids):
        raise HarnessError("labels do not exactly cover the sealed sample")
    yes = no = unsure = 0
    for site_id in sampled_site_ids:
        decision = by_site[site_id].get(field)
        if decision not in ("yes", "no", "unsure"):
            raise HarnessError(f"{site_id}: {field} must be yes/no/unsure")
        if decision == "yes":
            yes += 1
        elif decision == "no":
            no += 1
        else:
            unsure += 1
    decided = yes + no
    result: dict[str, Any] = {
        "schema": HARNESS_SCHEMA,
        "field": field,
        "sampled": len(sampled_site_ids),
        "yes": yes,
        "no": no,
        "unsure": unsure,
        "decided": decided,
    }
    if decided > 0:
        low, high = wilson_interval(yes, decided, z)
        result["proportion"] = yes / decided
        result["wilson_low"] = low
        result["wilson_high"] = high
    return result


def _sequence(value: Any, context: str) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, (str, bytes)) or not isinstance(value, Sequence):
        raise HarnessError(f"{context} must be a sequence")
    return list(value)


def _text(value: Any, context: str) -> str:
    if not isinstance(value, str) or not value:
        raise HarnessError(f"{context} must be a non-empty string")
    return value
