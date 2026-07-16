#!/usr/bin/env python3
"""GATE2-V2 §5 Stage-0 ADVISORY power analysis (proxy cardinalities).

ADVISORY ONLY per GATE2-V2 §3/§5: computed against the sealed Attempt-3
frame cardinalities as a population proxy; the Stage-2 run against actual
post-snapshot cardinalities net of burns is the sole feasibility authority.

The bound functions are imported from the sealed scorer (score.py) — that
import IS the mechanical transcription the protocol requires. The family
partition is: four estimand families {client_call, registration} x
{precision, recall} (score.py assertion membership, `families` field),
alpha_each = 1/80 at 95% joint one-sided confidence, matching the sealed
attempt-3 commitment's family_size = 4.

Design points (GATE2-V2 §5): true overall precision 0.995, true
eligible-population recall 0.95, true per-fixture precision 0.97; required
assurance 0.90; thresholds per §10.
"""

from __future__ import annotations

import json
import sys
from fractions import Fraction
from pathlib import Path

SPIKE = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(SPIKE))
from score import hypergeom_probability, lower_population_total  # noqa: E402

ALPHA = Fraction(1, 80)
ASSURANCE = Fraction(9, 10)

# Sealed Attempt-3 frame cardinalities (input.commitment.json .frames).
FRAMES = {
    "client_call_precision": {"population": 5383, "census": 2690,
                              "threshold": Fraction(98, 100), "p": Fraction(995, 1000)},
    "client_call_recall": {"population": 5463, "census": 2770,
                           "threshold": Fraction(90, 100), "p": Fraction(95, 100)},
    "registration_precision": {"population": 127, "census": 127,
                               "threshold": Fraction(98, 100), "p": Fraction(995, 1000)},
    "registration_recall": {"population": 281, "census": 281,
                            "threshold": Fraction(90, 100), "p": Fraction(95, 100)},
}

# Per-fixture typed-call counts: advisory proxy from the sealed prefix-2
# capacity preflight (independent_typed_call_counts); treated with zero
# census help (worst case).
FIXTURE_TYPED_CALLS = {"temporal": 3825, "dapr": 1447, "loki": 177,
                       "online-boutique": 19}
FIXTURE_THRESHOLD = Fraction(90, 100)
FIXTURE_P = Fraction(97, 100)


def assurance(population: int, census: int, n: int, p: Fraction,
              threshold: Fraction) -> Fraction:
    """P[census_k + exact LB over the sampled remainder >= threshold*pop]."""
    sampled_pop = population - census
    census_k = (p * census).__floor__()
    need = threshold * population
    if sampled_pop == 0:
        return Fraction(1) if Fraction(census_k) >= need else Fraction(0)
    n = min(n, sampled_pop)
    true_k = (p * sampled_pop).__floor__()
    total = Fraction(0)
    for observed in range(min(n, true_k), -1, -1):
        bound = lower_population_total(sampled_pop, n, observed, ALPHA)
        if Fraction(census_k + bound) < need:
            break
        total += hypergeom_probability(sampled_pop, true_k, n, observed, observed)
    return total


def minimal_n(population: int, census: int, p: Fraction,
              threshold: Fraction) -> int | None:
    sampled_pop = population - census
    lo, hi = 0, sampled_pop
    if assurance(population, census, hi, p, threshold) < ASSURANCE:
        return None
    while lo < hi:
        mid = (lo + hi) // 2
        if assurance(population, census, mid, p, threshold) >= ASSURANCE:
            hi = mid
        else:
            lo = mid + 1
    return lo


def main() -> int:
    result = {
        "schema": "t111-gate2-v2-stage0-power-advisory-v1",
        "status": "ADVISORY — Stage-2 exact power against actual post-snapshot cardinalities is the sole authority (GATE2-V2 §5)",
        "alpha_each": "1/80",
        "assurance": "9/10",
        "family_partition": [
            "client_call precision", "client_call recall",
            "registration precision", "registration recall",
        ],
        "partition_source": "score.py assertion family membership ('client_call'/'registration' x precision/recall); commitment family_size=4",
        "frames": {},
        "per_fixture_precision": {},
    }
    for name, frame in FRAMES.items():
        n = minimal_n(frame["population"], frame["census"], frame["p"],
                      frame["threshold"])
        result["frames"][name] = {
            "population": frame["population"], "census": frame["census"],
            "sampled_population": frame["population"] - frame["census"],
            "threshold": str(frame["threshold"]), "design_point": str(frame["p"]),
            "minimal_sample_size": n,
            "feasible": n is not None,
        }
    for fixture, population in FIXTURE_TYPED_CALLS.items():
        n = minimal_n(population, 0, FIXTURE_P, FIXTURE_THRESHOLD)
        entry = {
            "population_proxy": population, "threshold": str(FIXTURE_THRESHOLD),
            "design_point": str(FIXTURE_P), "minimal_sample_size": n,
            "feasible": n is not None,
        }
        if n is not None and n >= population:
            entry["note"] = "full census of the fixture: bound is exact count"
        result["per_fixture_precision"][fixture] = entry
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
