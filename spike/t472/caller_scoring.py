"""Caller-specific, fail-closed scoring mechanics. No real round is executed here.

Labels, expected-resolution records and declaration citations are independently
committed inputs. Source interface inventories are not precision/recall scores.
The frozen t111 validation and pilot Wilson machinery remain unedited.
"""
from __future__ import annotations

import hashlib
import json
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path

_SPIKE_DIR = Path(__file__).resolve().parent
for extra in ("../t111", "../../pilot/validation"):
    sys.path.insert(0, str((_SPIKE_DIR / extra).resolve()))
import label_protocol as protocol
import harness as pilot_harness

SCORING_SCHEMA = "t472-caller-scoring-v3"
LEDGER_SCHEMA = "t472-eligible-unit-ledger-v2"
OUTCOME_STATES = frozenset({"analyzed", "excluded", "partial", "failed"})
QUALITY_FAMILIES = frozenset({"caller_precision", "caller_recall", "unresolved_accuracy",
                              "attributed_edge_precision", "attributed_edge_recall"})
_OPERATION = re.compile(r"[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+/[A-Za-z_][A-Za-z0-9_]*")
_SITE = re.compile(r"(?P<repo>[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@(?P<commit>[0-9a-f]{40}):(?P<path>[^\s:]+):(?P<start>[0-9]+)-(?P<end>[0-9]+)")


class ScoringError(ValueError):
    """A planned sample, ledger or comparison is inconsistent."""


def load_parameters(path: Path) -> dict:
    params = json.loads(path.read_text())
    if params.get("schema") != "t472-quality-parameters-v1":
        raise ScoringError("parameters file is not t472-quality-parameters-v1")
    return params


def _labels(ids: Sequence[str], labels: Sequence[Mapping], strata: Mapping[str, str]) -> dict:
    validated = protocol.validate_labels(labels, ids)
    for row in validated:
        if row["site_id"] not in strata:
            raise ScoringError("sample has no source stratum")
        role = strata[row["site_id"]].rsplit(":", 1)[-1]
        if row["expected_code_role"] != role:
            raise ScoringError("label code role disagrees with source stratum")
    return {r["site_id"]: r for r in validated}


def _truth(ids: Sequence[str], records: Sequence[Mapping] | None, labels: Mapping,
           declaration: bool) -> dict:
    if records is None:
        raise ScoringError("independent committed ledger is required")
    expected_fields = {"site_id", "state", "rationale"} | (
        {"operation", "lineage", "declaration_site_id"} if declaration else {"alternatives"})
    found = {}
    for rec in records:
        if set(rec) != expected_fields or rec["site_id"] in found:
            raise ScoringError("ledger fields or duplicate coordinate are invalid")
        state = rec["state"]
        if state not in {"resolved", "unresolved", "not_call", "unsure"}:
            raise ScoringError("unknown independent resolution state")
        if not isinstance(rec["rationale"], str) or not rec["rationale"].strip():
            raise ScoringError("ledger requires independent source rationale")
        found[rec["site_id"]] = dict(rec)
    if set(found) != set(ids):
        raise ScoringError("ledger does not exactly cover the planned sample")
    for site_id, rec in found.items():
        invocation = labels[site_id]["invocation"]
        if invocation == "no" and rec["state"] in {"resolved", "unresolved"}:
            raise ScoringError("resolution ledger contradicts invocation=no")
        if invocation == "yes" and rec["state"] == "not_call":
            raise ScoringError("resolution ledger contradicts invocation=yes")
        if declaration:
            if rec["state"] == "resolved":
                call, cite = _SITE.fullmatch(site_id), _SITE.fullmatch(rec["declaration_site_id"] or "")
                if call is None or cite is None or not cite["path"].endswith(".proto") \
                        or int(cite["start"]) >= int(cite["end"]) \
                        or (call["repo"], call["commit"]) != (cite["repo"], cite["commit"]):
                    raise ScoringError("declaration citation must be in the same exact repository tree")
                lineage = "provisional_repo_path_v1_" + hashlib.sha256(
                    (cite["repo"] + "\0" + cite["path"]).encode()).hexdigest()
                if rec["lineage"] != lineage or not _OPERATION.fullmatch(rec["operation"] or ""):
                    raise ScoringError("independent declaration identity is invalid")
                if invocation == "yes" and rec["operation"] != labels[site_id]["operation"]:
                    raise ScoringError("declaration ledger contradicts blind operation label")
            elif any(rec[k] is not None for k in ("lineage", "operation", "declaration_site_id")):
                raise ScoringError("nonresolved declaration ledger record must have null attribution")
        else:
            alternatives = _unresolved_alternatives(rec["alternatives"])
            if bool(alternatives) != (rec["state"] in {"unresolved", "not_call"}):
                raise ScoringError("independent resolution state and alternatives disagree")
            if rec["state"] == "unresolved" and invocation == "yes" \
                    and labels[site_id]["operation"] not in {op for op, _ in alternatives}:
                raise ScoringError("independent alternatives contradict the blind operation label")
    return found


def _result(family: str, stratum: str, ids: Sequence[str], yes: int, no: int,
            unsure: int, z: float) -> dict:
    decided = yes + no
    result = {"threshold_family": family, "stratum": stratum,
              "sample_site_ids": sorted(ids), "sampled": len(ids),
              "yes": yes, "no": no, "unsure": unsure, "decided": decided,
              "nonpositive": len(ids) - decided - unsure}
    if unsure == len(ids):
        result["invalid"] = "all-unsure stratum"
    if decided == 0:
        result["unavailable"] = True
    else:
        low, high = pilot_harness.wilson_interval(yes, decided, z)
        result.update(proportion=yes / decided, wilson_low=low, wilson_high=high)
    return result


def _finish(family: str, tallies: Mapping, z: float) -> list[dict]:
    return [_result(family, name, t["ids"], t["yes"], t["no"], t["unsure"], z)
            for name, t in sorted(tallies.items())]


def _tally(tallies: dict, stratum: str, site_id: str) -> dict:
    t = tallies.setdefault(stratum, {"ids": [], "yes": 0, "no": 0, "unsure": 0})
    t["ids"].append(site_id)
    return t


def score_recall(family: str, sampled_site_ids: Sequence[str], labels: Sequence[Mapping],
                 predicted_site_ids: set[str], stratum_of: Mapping[str, str], z: float,
                 predicted_operations: Mapping[str, str] | None = None,
                 predicted_lineages: Mapping[str, str] | None = None,
                 declaration_ledger: Sequence[Mapping] | None = None) -> list[dict]:
    if family not in {"caller_recall", "attributed_edge_recall"}:
        raise ScoringError("not a recall family")
    by_site = _labels(sampled_site_ids, labels, stratum_of)
    truth = None
    if family == "attributed_edge_recall":
        if predicted_operations is None or predicted_lineages is None:
            raise ScoringError("attributed recall requires predicted operations and lineages")
        truth = _truth(sampled_site_ids, declaration_ledger, by_site, True)
    tallies = {}
    for site_id in sampled_site_ids:
        row = by_site[site_id]
        t = _tally(tallies, stratum_of[site_id], site_id)
        if row["invocation"] == "unsure" or truth is not None and truth[site_id]["state"] == "unsure":
            t["unsure"] += 1
            continue
        if row["invocation"] == "no" or truth is not None and truth[site_id]["state"] != "resolved":
            continue
        hit = site_id in predicted_site_ids
        if truth is not None:
            hit = hit and predicted_operations.get(site_id) == truth[site_id]["operation"] \
                and predicted_lineages.get(site_id) == truth[site_id]["lineage"]
        t["yes" if hit else "no"] += 1
    return _finish(family, tallies, z)


def score_precision_family(family: str, sampled_site_ids: Sequence[str], labels: Sequence[Mapping],
                           stratum_of: Mapping[str, str], z: float,
                           predicted_operations: Mapping[str, str] | None = None,
                           predicted_lineages: Mapping[str, str] | None = None,
                           declaration_ledger: Sequence[Mapping] | None = None) -> list[dict]:
    if family not in {"caller_precision", "attributed_edge_precision"}:
        raise ScoringError("not a precision family; unresolved accuracy requires its resolution ledger")
    by_site = _labels(sampled_site_ids, labels, stratum_of)
    truth = None
    if family == "attributed_edge_precision":
        if predicted_operations is None or predicted_lineages is None:
            raise ScoringError("attributed precision requires predicted operations and lineages")
        truth = _truth(sampled_site_ids, declaration_ledger, by_site, True)
    tallies = {}
    for site_id in sampled_site_ids:
        row = by_site[site_id]
        t = _tally(tallies, stratum_of[site_id], site_id)
        if row["invocation"] == "unsure" or truth is not None and truth[site_id]["state"] == "unsure":
            t["unsure"] += 1
            continue
        correct = row["invocation"] == "yes"
        if truth is not None:
            correct = correct and truth[site_id]["state"] == "resolved" \
                and predicted_operations.get(site_id) == truth[site_id]["operation"] \
                and predicted_lineages.get(site_id) == truth[site_id]["lineage"]
        t["yes" if correct else "no"] += 1
    return _finish(family, tallies, z)


def _unresolved_alternatives(records: Sequence[Mapping], predicted: bool = False) -> frozenset:
    if not isinstance(records, (list, tuple)):
        raise ScoringError("unresolved comparison requires the complete alternative list")
    alternatives, operations = set(), set()
    for rec in records:
        if not isinstance(rec, Mapping):
            raise ScoringError("unresolved alternative must be a record")
        allowed = {"operation", "reason"}
        if predicted and set(rec) == allowed | {"lineage", "source_citation"}:
            allowed |= {"lineage", "source_citation"}
        if set(rec) != allowed or not isinstance(rec["operation"], str) \
                or not _OPERATION.fullmatch(rec["operation"]) \
                or not isinstance(rec["reason"], str) or not rec["reason"].strip() \
                or rec["operation"] in operations:
            raise ScoringError("unresolved alternative identity, reason or duplicate is invalid")
        operations.add(rec["operation"])
        alternatives.add((rec["operation"], rec["reason"]))
    return frozenset(alternatives)


def score_unresolved(sampled_site_ids: Sequence[str], labels: Sequence[Mapping],
                     predicted_alternatives: Mapping[str, Sequence[Mapping]], stratum_of: Mapping[str, str],
                     z: float, resolution_ledger: Sequence[Mapping]) -> list[dict]:
    by_site = _labels(sampled_site_ids, labels, stratum_of)
    truth = _truth(sampled_site_ids, resolution_ledger, by_site, False)
    if not set(sampled_site_ids) <= set(predicted_alternatives):
        raise ScoringError("sampled unresolved candidate has no recorded alternatives")
    predicted_sets = {s: _unresolved_alternatives(predicted_alternatives[s], predicted=True)
                      for s in sampled_site_ids}
    if any(not alternatives for alternatives in predicted_sets.values()):
        raise ScoringError("unresolved candidate has no alternatives")
    tallies = {}
    for site_id in sampled_site_ids:
        t = _tally(tallies, stratum_of[site_id], site_id)
        rec = truth[site_id]
        if by_site[site_id]["invocation"] == "unsure" or rec["state"] == "unsure":
            t["unsure"] += 1
            continue
        correct = rec["state"] in {"unresolved", "not_call"} \
            and _unresolved_alternatives(rec["alternatives"]) == predicted_sets[site_id]
        t["yes" if correct else "no"] += 1
    return _finish("unresolved_accuracy", tallies, z)


def gate_quality(results: Sequence[Mapping], parameters: Mapping,
                 required_samples: Mapping[str, Mapping[str, Sequence[str]]]) -> dict:
    """Require every approved family and its complete preregistered sample."""
    if set(required_samples) != QUALITY_FAMILIES:
        raise ScoringError("planned samples must name all five approved quality families")
    expected = {(family, stratum): sorted(ids) for family, strata in required_samples.items()
                for stratum, ids in strata.items()}
    actual = {}
    for r in results:
        key = (r["threshold_family"], r["stratum"])
        if key in actual or key not in expected or r["sample_site_ids"] != expected[key]:
            raise ScoringError("results do not match the complete planned sample")
        if r["sampled"] != len(expected[key]) or r["sampled"] == 0:
            raise ScoringError("planned nonempty sample is incomplete")
        for field in ("yes", "no", "unsure", "decided", "sampled", "nonpositive"):
            if type(r.get(field)) is not int or r[field] < 0:
                raise ScoringError("invalid score counts")
        if r["decided"] != r["yes"] + r["no"] or r["sampled"] != r["decided"] + r["unsure"] + r["nonpositive"]:
            raise ScoringError("score counts do not reconcile")
        if bool(r.get("unavailable")) != (r["decided"] == 0) \
                or bool(r.get("invalid")) != (r["unsure"] == r["sampled"]):
            raise ScoringError("score availability flags disagree with its counts")
        if r["decided"] and r.get("proportion") != r["yes"] / r["decided"]:
            raise ScoringError("score rate disagrees with its numerator/denominator")
        actual[key] = r
    if set(actual) != set(expected):
        raise ScoringError("missing planned family/stratum result")
    invalid = [r for r in results if r.get("invalid")]
    if invalid:
        return {"round": "invalid", "reason": "all-unsure stratum", "results": list(results)}
    floors = parameters["pass_thresholds_percent"]
    failures = []
    for r in results:
        if r["unsure"] / r["sampled"] * 100 > floors["unsure_max"]:
            failures.append({**r, "gate": "unsure_max"})
        if not r.get("unavailable") and r["proportion"] * 100 < floors[r["threshold_family"] + "_min"]:
            failures.append({**r, "gate": r["threshold_family"] + "_min"})
    unavailable = any(not strata for strata in required_samples.values()) or any(r.get("unavailable") for r in results)
    return {"round": "fail" if failures else "unavailable" if unavailable else "pass",
            "failures": failures, "results": list(results)}


def _units(universe: Mapping) -> dict[str, str]:
    units = {}
    for repo in universe["repositories"]:
        seen = set()
        for role, paths in repo["eligible_units"]["by_role"].items():
            if role not in protocol.CODE_ROLES:
                raise ScoringError("unknown census code role")
            for path in paths:
                key = f"{repo['repo']}@{repo['derived_commit']}:{path}"
                if path in seen or key in units:
                    raise ScoringError("duplicate census source")
                seen.add(path)
                units[key] = f"{repo['repo']}:{role}"
        if seen != set(repo["eligible_units"]["regular_go_source_paths"]) \
                or len(seen) != repo["units"]["regular_go_sources"]:
            raise ScoringError("source roles do not reconcile to the full census")
    return units


def eligible_unit_ledger(outcome_records: Sequence[Mapping], universe: Mapping) -> dict:
    units = _units(universe)
    counts, seen = {}, set()
    for rec in outcome_records:
        if set(rec) != {"repository", "commit", "path", "state"} or rec["state"] not in OUTCOME_STATES:
            raise ScoringError("outcome must explicitly identify its repository, commit, path and terminal state")
        key = f"{rec['repository']}@{rec['commit']}:{rec['path']}"
        if key in seen or key not in units:
            raise ScoringError("unknown or duplicate outcome source")
        seen.add(key)
        per = counts.setdefault(units[key], {state: 0 for state in sorted(OUTCOME_STATES)})
        per[rec["state"]] += 1
    if seen != set(units):
        raise ScoringError("census source lacks an explicit outcome; reading a file never implies analyzed")
    return {"schema": LEDGER_SCHEMA, "by_stratum": counts, "total": len(units)}


def gate_processing_state(ledger: Mapping, universe: Mapping, parameters: Mapping) -> dict:
    units = _units(universe)
    expected = {}
    for stratum in units.values():
        expected[stratum] = expected.get(stratum, 0) + 1
    if ledger.get("schema") != LEDGER_SCHEMA or ledger["total"] != len(units) \
            or set(ledger["by_stratum"]) != set(expected):
        raise ScoringError("outcome ledger disagrees with the census")
    results, failures = [], []
    floors = parameters["pass_thresholds_percent"]
    for stratum, total in sorted(expected.items()):
        counts = ledger["by_stratum"][stratum]
        if set(counts) != OUTCOME_STATES or any(type(v) is not int or v < 0 for v in counts.values()) \
                or sum(counts.values()) != total:
            raise ScoringError("outcome totals do not exactly reconcile within a source stratum")
        observed = {"stratum": stratum, "units": total,
                    "analyzed_percent": counts["analyzed"] / total * 100,
                    "partial_failed_percent": (counts["partial"] + counts["failed"]) / total * 100,
                    "excluded_percent": counts["excluded"] / total * 100, "counts": counts}
        for key, threshold, minimum in (("analyzed", "analyzed_min", True),
                                         ("partial_failed", "partial_plus_failed_max", False),
                                         ("excluded", "excluded_max", False)):
            value = observed[key + "_percent"]
            if (minimum and value < floors[threshold]) or (not minimum and value > floors[threshold]):
                failures.append({"stratum": stratum, "gate": threshold, "observed_percent": value})
        results.append(observed)
    return {"round": "fail" if failures else "unavailable" if not units else "pass",
            "failures": failures, "results": results, "units": len(units)}
