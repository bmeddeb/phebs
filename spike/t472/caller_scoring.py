"""T47.2b caller-specific scoring projection and ledgers (protocol sections 3, 10.7).

Exact numerator/denominator rules, per stratum, fail-closed:

  caller_recall             |sampled true call sites phebs claimed| /
                            |sampled true call sites| (decided labels only)
  caller_precision          score_claim(invocation) over phebs-claimed sampled sites
  attributed_edge_recall    |sampled true sites phebs claimed with operation equal
                            to the blind label| / |sampled true call sites|
  attributed_edge_precision |claimed sampled sites whose attributed operation equals
                            the blind label| / |claimed sampled decided|
  unresolved_accuracy       score_claim(invocation) over phebs-unresolved sampled sites
  declaration_attribution   gates on the attributed_edge thresholds: mapped clients
                            must have registration=yes with the predicted service,
                            abstained clients must be registration=no

Invalidation (approved parameters): missing or surplus labels invalidate the
round; an all-unsure stratum invalidates the round; a zero decided denominator
makes that stratum unavailable, never a 100 percent pass; gates use unrounded
point estimates with no pooling rescue.

The eligible-unit ledger consumes only explicit per-source outcome records
from the sealed run receipt. A file read is never inferred as "analyzed"; a
census source absent from the receipt is unaccounted and refuses the ledger.
The processing-state denominator is the complete census of regular sources,
not a 97-unit sample.

Nothing here executes on real round data before the sealed protocol exists.
"""

from __future__ import annotations

import json
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path

_SPIKE_DIR = Path(__file__).resolve().parent
for _extra in ("../t111", "../../pilot/validation"):
    p = (_SPIKE_DIR / _extra).resolve()
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

import label_protocol as protocol  # noqa: E402
import harness as pilot_harness  # noqa: E402

SCORING_SCHEMA = "t472-caller-scoring-v1"
LEDGER_SCHEMA = "t472-eligible-unit-ledger-v1"
OUTCOME_STATES = frozenset({"analyzed", "excluded", "partial", "failed"})
LEDGER_GATES = (("analyzed", "min", "analyzed_min"),
                ("partial_failed", "max", "partial_plus_failed_max"),
                ("excluded", "max", "excluded_max"))
_SERVICE_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+")


class ScoringError(ValueError):
    """A label set, prediction join or outcome receipt violates the round rules."""


def load_parameters(path: Path) -> dict:
    params = json.loads(path.read_text())
    if params.get("schema") != "t472-quality-parameters-v1":
        raise ScoringError("parameters file is not t472-quality-parameters-v1")
    return params


def _validated_by_site(sampled_site_ids: Sequence[str],
                       labels: Sequence[Mapping]) -> dict[str, Mapping]:
    validated = protocol.validate_labels(labels, sampled_site_ids)
    return {row["site_id"]: row for row in validated}


def _stratum_result(family: str, stratum: str, yes: int, no: int, unsure: int,
                    z: float) -> dict:
    decided = yes + no
    result: dict = {"threshold_family": family, "stratum": stratum,
                    "sampled": decided + unsure, "yes": yes, "no": no,
                    "unsure": unsure, "decided": decided}
    if decided == 0:
        result["unavailable"] = True  # zero denominator: never a 100 percent pass
        return result
    low, high = pilot_harness.wilson_interval(yes, decided, z)
    result.update(proportion=yes / decided, wilson_low=low, wilson_high=high)
    return result


def _gate(results: Sequence[Mapping], parameters: Mapping) -> dict:
    if any(r.get("unavailable") for r in results):
        return {"round": "unavailable", "reason": "zero-decided stratum",
                "results": list(results)}
    floors = parameters["pass_thresholds_percent"]
    failures = []
    for r in results:
        floor = floors[f"{r['threshold_family']}_min"]
        if r["proportion"] * 100 < floor:
            failures.append({**r, "floor_percent": floor})
    return {"round": "fail" if failures else "pass", "failures": failures,
            "results": list(results)}


def score_recall(family: str, sampled_site_ids: Sequence[str],
                 labels: Sequence[Mapping], predicted_site_ids: set[str],
                 stratum_of: Mapping[str, str], z: float,
                 predicted_operations: Mapping[str, str] | None = None) -> list[dict]:
    """Recall-family scoring: denominator is blind truth, numerator joins phebs.

    family is "caller_recall" or "attributed_edge_recall"; the latter passes
    predicted_operations and requires the claimed operation to equal the
    label's for a hit.
    """
    if family not in ("caller_recall", "attributed_edge_recall"):
        raise ScoringError(f"not a recall family: {family}")
    by_site = _validated_by_site(sampled_site_ids, labels)
    tallies: dict[str, dict[str, int]] = {}
    for site_id in sampled_site_ids:
        row = by_site[site_id]
        t = tallies.setdefault(stratum_of[site_id], {"true": 0, "hit": 0, "unsure": 0})
        if row["invocation"] == "unsure":
            t["unsure"] += 1
            continue
        if row["invocation"] == "no":
            continue
        t["true"] += 1
        if site_id not in predicted_site_ids:
            continue
        if predicted_operations is not None \
                and predicted_operations.get(site_id) != row["operation"]:
            continue
        t["hit"] += 1
    return [_stratum_result(family, name, t["hit"], t["true"] - t["hit"], t["unsure"], z)
            for name, t in sorted(tallies.items())]


def score_precision_family(family: str, sampled_site_ids: Sequence[str],
                           labels: Sequence[Mapping], stratum_of: Mapping[str, str],
                           z: float,
                           predicted_operations: Mapping[str, str] | None = None) -> list[dict]:
    """Precision-family scoring over phebs-claimed sampled sites.

    family is "caller_precision" or "unresolved_accuracy" (invocation only) or
    "attributed_edge_precision" (also requires operation equality; a truly
    invoked site with a wrong attributed operation counts as no).
    """
    if family not in ("caller_precision", "unresolved_accuracy", "attributed_edge_precision"):
        raise ScoringError(f"not a precision family: {family}")
    by_site = _validated_by_site(sampled_site_ids, labels)
    tallies: dict[str, dict[str, int]] = {}
    for site_id in sampled_site_ids:
        row = by_site[site_id]
        t = tallies.setdefault(stratum_of[site_id], {"yes": 0, "no": 0, "unsure": 0})
        if row["invocation"] == "unsure":
            t["unsure"] += 1
            continue
        if row["invocation"] == "yes" and predicted_operations is not None \
                and predicted_operations.get(site_id) != row["operation"]:
            t["no"] += 1
            continue
        t[row["invocation"]] += 1
    return [_stratum_result(family, name, t["yes"], t["no"], t["unsure"], z)
            for name, t in sorted(tallies.items())]


def score_declaration_attribution(sampled_site_ids: Sequence[str],
                                  labels: Sequence[Mapping],
                                  predicted_services: Mapping[str, str],
                                  stratum_of: Mapping[str, str], z: float) -> list[dict]:
    """Attribution scoring; gates on the attributed_edge thresholds."""
    by_site = _validated_by_site(sampled_site_ids, labels)
    for site_id, service in predicted_services.items():
        if not _SERVICE_RE.fullmatch(service):
            raise ScoringError(f"{site_id}: attributed service is not canonical: {service!r}")
    tallies: dict[str, dict[str, int]] = {}
    for site_id in sampled_site_ids:
        row = by_site[site_id]
        t = tallies.setdefault(stratum_of[site_id], {"yes": 0, "no": 0, "unsure": 0})
        if row["registration"] == "unsure":
            t["unsure"] += 1
            continue
        if site_id in predicted_services:
            correct = (row["registration"] == "yes"
                       and row["service"] == predicted_services[site_id])
        else:
            correct = row["registration"] == "no"
        t["yes" if correct else "no"] += 1
    return [_stratum_result("attributed_edge", name, t["yes"], t["no"], t["unsure"], z)
            for name, t in sorted(tallies.items())]


def gate_quality(results: Sequence[Mapping], parameters: Mapping) -> dict:
    """Apply the approved per-family floors to scored strata."""
    return _gate(results, parameters)


def eligible_unit_ledger(outcome_records: Sequence[Mapping],
                         universe: Mapping) -> dict:
    """Reconcile explicit per-source outcome records to the eligible-unit census.

    The census is the universe enumeration's regular-source path list, which
    enumerate_universe.py already reconciles fail-closed to source.census.json
    counts. Unknown paths, unknown states, duplicate records, and any census
    source missing from the receipt all refuse the ledger; a file read is
    never inferred as "analyzed".
    """
    expected = {r["repo"]: set(r["eligible_units"]["regular_go_source_paths"])
                for r in universe["repositories"]}
    counts: dict[str, dict[str, int]] = {}
    seen: set[str] = set()
    for rec in outcome_records:
        path, state = rec.get("path"), rec.get("state")
        if not isinstance(path, str) or not path:
            raise ScoringError("outcome record has no path")
        if path in seen:
            raise ScoringError(f"duplicate outcome record: {path}")
        seen.add(path)
        if state not in OUTCOME_STATES:
            raise ScoringError(f"{path}: state must be one of {sorted(OUTCOME_STATES)}")
        repo = next((n for n, paths in expected.items() if path in paths), None)
        if repo is None:
            raise ScoringError(f"{path}: not a census regular Go source")
        per = counts.setdefault(repo, {})
        per[state] = per.get(state, 0) + 1
    for name, paths in expected.items():
        accounted = set(p for p in seen if p in paths)
        if accounted != paths:
            missing = sorted(paths - accounted)[:3]
            raise ScoringError(f"{name}: {len(paths - accounted)} census sources lack "
                               f"an explicit outcome record (e.g. {missing}); a file read "
                               f"is never inferred as analyzed")
        if sum(counts[name].values()) != len(paths):
            raise ScoringError(f"{name}: outcome totals do not reconcile to the census")
    return {"schema": LEDGER_SCHEMA, "by_repository": counts,
            "total": sum(sum(p.values()) for p in counts.values())}


def gate_processing_state(ledger: Mapping, universe: Mapping,
                          parameters: Mapping) -> dict:
    """Apply analyzed/partial+failed/excluded gates over the whole census."""
    total_units = sum(r["units"]["regular_go_sources"]
                      for r in universe["repositories"])
    sums = {state: sum(per.get(state, 0) for per in ledger["by_repository"].values())
            for state in OUTCOME_STATES}
    partial_failed = sums["partial"] + sums["failed"]
    observed = {
        "analyzed_percent": sums["analyzed"] / total_units * 100,
        "partial_failed_percent": partial_failed / total_units * 100,
        "excluded_percent": sums["excluded"] / total_units * 100,
        "units": total_units, **{f"{k}_units": v for k, v in sums.items()},
    }
    floors = parameters["pass_thresholds_percent"]
    failures = []
    for key, direction, floor_key in LEDGER_GATES:
        value = observed[f"{key}_percent"]
        floor = floors[floor_key]
        bad = value < floor if direction == "min" else value > floor
        if bad:
            failures.append({"gate": floor_key, "observed_percent": value,
                             "floor_percent": floor})
    return {"round": "fail" if failures else "pass", "failures": failures,
            "observed": observed}
