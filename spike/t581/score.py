#!/usr/bin/env python3
"""T58.1: score the oracle's sites against the extractor's candidates.

Joins on `(unit, path, start_byte, end_byte)` — one row per **site**, never per
assertion, because two same-file sites sharing a claim identity collapse into one
stored assertion carrying two atoms.

The claim is site coverage only (protocol §1): emitted operation and service
identities are recorded but never scored. The scored denominator excludes
`out_of_domain`, whose silence GD10 makes the claim's largest blind spot; those
counts are still published, because §6 requires it.

Roles come from the exporter, not the oracle: GD16 freezes role names and
precedence but deliberately not role *detection*, so per-role figures inherit the
extractor's own classification. That limit is restated in the output rather than
hidden.

Usage:
    python3 spike/t581/score.py --sites sites.jsonl --candidate candidate.jsonl \
        [--adjudications adjudications.json] [--out round/score.json]
"""

from __future__ import annotations

import argparse
import json
import sys
from collections import Counter, defaultdict
from pathlib import Path

SITE_SCHEMA = "t581-site-v1"
CANDIDATE_SCHEMA = "t581-candidate-v1"
OUT_OF_DOMAIN_SCHEMA = "t581-out-of-domain-v1"
SCORE_SCHEMA = "t581-score-v1"

# The frozen §6 thresholds (Ben, 2026-10-14). Never edit these after a count is
# seen; a round that misses is a failed round and a replacement needs a newly
# pre-declared population.
THRESHOLDS = {
    "coverage_min": 0.99,
    "state_agreement_min": 0.99,
    "inversions_max": 0,
    "call_registration_swaps_max": 0,
    "spurious_per_scored_site_max": 0.01,
    "pending_after_adjudication_max": 0,
    "scored_sites_min": 300,
    "production_role_agreement_min": 0.99,
    # Disclosed, not scored.
    "other_role_agreement_note_below": 0.95,
}

CALL_STATES = {"resolved_call", "ambiguous_call"}
REGISTRATION_STATES = {"resolved_registration", "ambiguous_registration"}
GAP_STATES = {"gap_source_too_large", "gap_parse_error"}
RESOLVED_STATES = {"resolved_call", "resolved_registration"}
AMBIGUOUS_STATES = {"ambiguous_call", "ambiguous_registration"}

# The extractor's predicates map onto the protocol's scored state vocabulary.
# GRPC_EXTRACTION_GAP is split by its object, which carries the reason.
PREDICATE_TO_STATE = {
    "CALLS_OPERATION": "resolved_call",
    "UNRESOLVED_GRPC_CALL": "ambiguous_call",
    "REGISTERS_GRPC_SERVICE": "resolved_registration",
    "UNRESOLVED_GRPC_REGISTRATION": "ambiguous_registration",
}


def candidate_state(row: dict) -> str:
    predicate = row["predicate"]
    if predicate == "GRPC_EXTRACTION_GAP":
        reason = row.get("object", "")
        if reason == "source_too_large":
            return "gap_source_too_large"
        if reason == "parse_error":
            return "gap_parse_error"
        return "gap_unknown_reason:" + reason
    state = PREDICATE_TO_STATE.get(predicate)
    if state is None:
        return "unknown_predicate:" + predicate
    return state


def key(unit: str, path: str, start: int, end: int) -> tuple:
    return (unit, path, start, end)


def read_jsonl(path: Path) -> list[dict]:
    rows = []
    with path.open(encoding="utf-8") as handle:
        for number, line in enumerate(handle, 1):
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError as error:
                raise SystemExit(f"{path}:{number}: invalid JSON: {error}") from error
    return rows


def sites_from_rows(rows: list[dict]) -> dict[tuple, dict]:
    sites: dict[tuple, dict] = {}
    for row in rows:
        schema = row.get("schema")
        if schema == OUT_OF_DOMAIN_SCHEMA:
            # Per-unit aggregates share the stream but are not sites.
            continue
        if schema != SITE_SCHEMA:
            raise SystemExit(f"site row has schema {schema!r}, want {SITE_SCHEMA!r}")
        k = key(row["unit"], row["path"], row["start_byte"], row["end_byte"])
        if k in sites:
            # Two oracle sites at one span would make the join ambiguous, so it
            # is a hard failure rather than a silent last-writer-wins.
            raise SystemExit(f"oracle emitted two sites at {_adj_key(k)}")
        sites[k] = row
    return sites


def aggregates_from_rows(rows: list[dict]) -> dict[str, dict]:
    """Collect the per-unit out-of-domain aggregates §6 requires published.

    A unit with zero candidates still emits an aggregate, so a missing record is
    distinguishable from an empty one and is reported as such.
    """
    out: dict[str, dict] = {}
    for row in rows:
        if row.get("schema") != OUT_OF_DOMAIN_SCHEMA:
            continue
        unit = row["unit"]
        if unit in out:
            raise SystemExit(f"oracle emitted two out-of-domain aggregates for {unit}")
        out[unit] = {
            "total": row.get("total", 0),
            "by_reason": dict(row.get("by_reason", {})),
            "files_with_candidates": row.get("files_with_candidates", 0),
        }
    return out


def candidates_from_rows(rows: list[dict]) -> dict[tuple, list[dict]]:
    grouped: dict[tuple, list[dict]] = defaultdict(list)
    for row in rows:
        if row.get("schema") != CANDIDATE_SCHEMA:
            raise SystemExit(
                f"candidate row has schema {row.get('schema')!r}, want {CANDIDATE_SCHEMA!r}"
            )
        grouped[key(row["unit_id"], row["path"], row["start_byte"], row["end_byte"])].append(row)
    return grouped


def load_sites(path: Path) -> dict[tuple, dict]:
    return sites_from_rows(read_jsonl(path))


def load_candidates(path: Path) -> dict[tuple, list[dict]]:
    return candidates_from_rows(read_jsonl(path))


def ratio(numerator: int, denominator: int) -> float | None:
    return None if denominator == 0 else numerator / denominator


def score(sites: dict, candidates: dict, adjudications: dict | None, aggregates: dict | None = None) -> dict:
    adjudications = adjudications or {}
    aggregates = aggregates or {}
    pending = [k for k, v in adjudications.items() if v is None or v[0] is None]

    scored = 0
    covered = 0
    agreeing = 0
    inversions = 0
    swaps = 0
    spurious = 0
    missed: list[dict] = []
    duplicated: list[dict] = []
    disagreed: list[dict] = []
    spurious_rows: list[dict] = []

    per_repo_scored: Counter = Counter()
    per_role_total: Counter = Counter()
    per_role_agree: Counter = Counter()
    out_of_domain = 0
    oracle_states: Counter = Counter()

    for k, site in sites.items():
        state = site["state"]
        oracle_states[state] += 1
        if state == "out_of_domain":
            out_of_domain += 1
            # Out-of-domain sites are outside the denominator, but a candidate
            # row there is still spurious: GD10 says the product emits nothing.
            if k in candidates:
                spurious += len(candidates[k])
                spurious_rows.extend(_brief(k, candidates[k], state))
            continue

        # An adjudicated truth replaces the oracle's for this key.
        override = adjudications.get(_adj_key(k))
        if override is not None and override[0] is not None:
            if override[0] == "not_a_site":
                # A confirmed spurious extractor row: the site leaves the
                # denominator and every candidate at that span counts.
                spurious += len(candidates.get(k, []))
                spurious_rows.extend(_brief(k, candidates.get(k, []), "not_a_site"))
                continue
            state = override[0]

        scored += 1
        per_repo_scored[site["unit"]] += 1
        rows = candidates.get(k, [])
        if len(rows) == 0:
            missed.append(_site_brief(k, site))
            continue
        if len(rows) > 1:
            duplicated.append({"key": _adj_key(k), "rows": len(rows), "oracle_state": state})
        covered += 1
        got = candidate_state(rows[0])
        role = rows[0].get("code_role", "unknown")
        per_role_total[role] += 1
        if got == state:
            agreeing += 1
            per_role_agree[role] += 1
        else:
            disagreed.append({
                "key": _adj_key(k), "oracle_state": state, "candidate_state": got,
                "role": role, "object": rows[0].get("object", ""),
            })
            if (state in RESOLVED_STATES and got in AMBIGUOUS_STATES) or (
                state in AMBIGUOUS_STATES and got in RESOLVED_STATES
            ):
                inversions += 1
            if (state in CALL_STATES and got in REGISTRATION_STATES) or (
                state in REGISTRATION_STATES and got in CALL_STATES
            ):
                swaps += 1

    # Candidate rows at spans the oracle never reported, excluding spans already
    # counted above (out-of-domain and adjudicated not_a_site).
    accounted = set(sites.keys())
    for k, rows in candidates.items():
        if k in accounted:
            continue
        spurious += len(rows)
        spurious_rows.extend(_brief(k, rows, "no_oracle_site"))

    production_total = per_role_total.get("production", 0)
    production_agree = per_role_agree.get("production", 0)
    total = sum(per_repo_scored.values())
    largest = per_repo_scored.most_common(1)[0] if per_repo_scored else None

    metrics = {
        "scored_sites": scored,
        "covered_sites": covered,
        "coverage": ratio(covered, scored),
        "state_agreement": ratio(agreeing, covered),
        "inversions": inversions,
        "call_registration_swaps": swaps,
        "spurious_rows": spurious,
        "spurious_per_scored_site": ratio(spurious, scored),
        "missed_sites": len(missed),
        "duplicated_sites": len(duplicated),
        "pending_after_adjudication": len(pending),
        "production_role_agreement": ratio(production_agree, production_total),
        "out_of_domain_candidates": out_of_domain,
        "out_of_domain": _summarize_out_of_domain(aggregates, sites),
        "oracle_state_counts": dict(sorted(oracle_states.items())),
        "per_role_total": dict(sorted(per_role_total.items())),
        "per_role_agreement": {
            role: ratio(per_role_agree.get(role, 0), count)
            for role, count in sorted(per_role_total.items())
        },
        "per_repository_scored": dict(sorted(per_repo_scored.items())),
        "concentration": {
            "largest_member": largest[0] if largest else None,
            "largest_share": ratio(largest[1], total) if largest else None,
        },
    }
    return {
        "schema": SCORE_SCHEMA,
        "thresholds": dict(THRESHOLDS),
        "metrics": metrics,
        "pass": verdict(metrics, pending),
        "detail": {
            "missed": missed[:200],
            "duplicated": duplicated[:200],
            "disagreed": disagreed[:200],
            "spurious": spurious_rows[:200],
            "pending_keys": sorted(pending)[:200],
        },
        "disclosures": [
            "Identities (operation and service full names) were recorded but not "
            "scored: the claim is site coverage only.",
            "Roles come from the extractor, not the oracle, so every per-role "
            "figure inherits the extractor's role classification.",
            "out_of_domain candidates are excluded from the denominator by GD10 "
            "and reported only as a count.",
        ],
    }


def _summarize_out_of_domain(aggregates: dict, sites: dict) -> dict:
    """Publish the out-of-domain blind spot §6 requires, from the per-unit
    aggregates. A unit with no aggregate is named, because the oracle emits one
    even at total 0 — so absence means the stream was incomplete, not that the
    unit had no candidates."""
    by_reason: Counter = Counter()
    total = 0
    files = 0
    for value in aggregates.values():
        total += value.get("total", 0)
        files += value.get("files_with_candidates", 0)
        for reason, count in value.get("by_reason", {}).items():
            by_reason[reason] += count
    units_in_sites = {site["unit"] for site in sites.values()}
    return {
        "total": total,
        "files_with_candidates": files,
        "by_reason": dict(sorted(by_reason.items())),
        "units_with_aggregate": len(aggregates),
        "units_missing_aggregate": sorted(units_in_sites - set(aggregates)),
        "per_unit": {unit: aggregates[unit]["total"] for unit in sorted(aggregates)},
    }


def _adj_key(k: tuple) -> str:
    return "\x00".join(str(part) for part in k)


def _brief(k: tuple, rows: list[dict], note: str) -> list[dict]:
    return [{
        "key": _adj_key(k), "note": note,
        "candidate_states": [candidate_state(r) for r in rows],
    }]


def _site_brief(k: tuple, site: dict) -> dict:
    return {
        "key": _adj_key(k), "state": site["state"], "object": site.get("object", ""),
    }


def verdict(metrics: dict, pending: list) -> dict:
    checks = {
        "coverage": _at_least(metrics["coverage"], THRESHOLDS["coverage_min"]),
        "state_agreement": _at_least(
            metrics["state_agreement"], THRESHOLDS["state_agreement_min"]
        ),
        "inversions": _at_most(metrics["inversions"], THRESHOLDS["inversions_max"]),
        "call_registration_swaps": _at_most(
            metrics["call_registration_swaps"], THRESHOLDS["call_registration_swaps_max"]
        ),
        "spurious_per_scored_site": _at_least_below(
            metrics["spurious_per_scored_site"], THRESHOLDS["spurious_per_scored_site_max"]
        ),
        "pending_after_adjudication": _at_most(
            metrics["pending_after_adjudication"], THRESHOLDS["pending_after_adjudication_max"]
        ),
        "scored_sites": _count_at_least(metrics["scored_sites"], THRESHOLDS["scored_sites_min"]),
        "production_role_agreement": _at_least(
            metrics["production_role_agreement"], THRESHOLDS["production_role_agreement_min"]
        ),
    }
    return {"passed": all(checks.values()), "checks": checks}


def _at_least(value, floor) -> bool:
    return value is not None and value >= floor


def _count_at_least(value, floor) -> bool:
    return value >= floor


def _at_most(value, ceiling) -> bool:
    return value <= ceiling


def _at_least_below(value, ceiling) -> bool:
    return value is not None and value <= ceiling


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sites", type=Path, required=True, help="oracle output")
    parser.add_argument("--candidate", type=Path, required=True, help="exporter output")
    parser.add_argument("--adjudications", type=Path)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()

    sites = load_sites(args.sites)
    aggregates = aggregates_from_rows(read_jsonl(args.sites))
    candidates = load_candidates(args.candidate)
    adjudications = {}
    if args.adjudications:
        adjudications = json.loads(args.adjudications.read_text(encoding="utf-8"))
    result = score(sites, candidates, adjudications, aggregates)

    text = json.dumps(result, indent=2) + "\n"
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(text, encoding="utf-8")
    print(text, end="")
    return 0 if result["pass"]["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
