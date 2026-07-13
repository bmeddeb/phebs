#!/usr/bin/env python3
"""Validate and score the sealed v3 Gate 3/4 benchmark fail closed."""

from __future__ import annotations

import argparse
import json
import math
import sys
from collections import defaultdict
from fractions import Fraction
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping, Sequence

from gate34_common import (
    DIRECT_ACCESS,
    GATE3_MERGE_PREDICATES,
    GATE_THRESHOLDS,
    MINIMUM_EVIDENCE,
    ValidationError,
    assertion_identity,
    canonical_fact_identity,
    canonical_json,
    coordinate_for,
    expected_label_identity,
    fact_occurrence_matches,
    load_burn_ledger,
    load_corpus_lock,
    load_facts,
    parse_detail,
    producer_summary,
    read_jsonl,
    require_fresh_blind_population,
    split_sites,
    summarize_sites,
    validate_artifacts,
    validate_labels,
    validate_manifest,
    validate_single_artifact,
    verify_corpus,
)
from label_prep_g34 import gate4_candidate_sites, generated_field_vocabulary


ID_SYSTEMS = ("online-boutique", "dapr", "temporal", "loki", "temporal-helm")
FIELD_SYSTEMS = ("online-boutique", "dapr", "temporal", "loki")
GATE_CONFIDENCE = Fraction(95, 100)


def parse_args(argv: Iterable[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("g34", "h2"))
    parser.add_argument("--base", type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument("--corpus-dir", type=Path)
    parser.add_argument("--labels", required=True, type=Path)
    parser.add_argument("--split", choices=("dev", "holdout", "all"), default="holdout")
    parser.add_argument("--json", action="store_true", dest="as_json")
    parser.add_argument("--enforce-gates", action="store_true")
    parser.add_argument(
        "--skip-corpus-check", action="store_true",
        help="diagnostic only; skipped verification can never produce a pass",
    )
    return parser.parse_args(argv)


def ratio(numerator: float, denominator: float) -> float | None:
    return numerator / denominator if denominator else None


def pct(value: float | None) -> str:
    return "n/a" if value is None else f"{value:.1%}"


def hypergeom_probability(
    population: int, successes: int, sample: int, low: int, high: int
) -> Fraction:
    """Return an exact finite-population interval probability."""

    if not 0 <= successes <= population or not 0 <= sample <= population:
        raise ValidationError(
            "invalid hypergeometric inputs: "
            f"population={population} successes={successes} sample={sample}"
        )
    low = max(low, 0, sample - (population - successes))
    high = min(high, sample, successes)
    if low > high:
        return Fraction(0, 1)
    denominator = math.comb(population, sample)
    numerator = sum(
        math.comb(successes, value)
        * math.comb(population - successes, sample - value)
        for value in range(low, high + 1)
    )
    return Fraction(numerator, denominator)


def lower_population_total(
    population: int, sample: int, observed: int, alpha: Fraction
) -> int:
    """Exact one-sided lower confidence bound for a population total."""

    if sample == 0 or observed == 0:
        return 0
    if sample == population:
        return observed
    low, high = observed, population - (sample - observed)
    while low < high:
        midpoint = (low + high) // 2
        tail = hypergeom_probability(population, midpoint, sample, observed, sample)
        if tail >= alpha:
            high = midpoint
        else:
            low = midpoint + 1
    return low


def upper_population_total(
    population: int, sample: int, observed: int, alpha: Fraction
) -> int:
    """Exact one-sided upper confidence bound for a population total."""

    if sample == 0:
        return population
    if sample == population:
        return observed
    if observed == sample:
        return population
    low, high = observed, population - (sample - observed)
    while low < high:
        midpoint = (low + high + 1) // 2
        cdf = hypergeom_probability(population, midpoint, sample, 0, observed)
        if cdf >= alpha:
            low = midpoint
        else:
            high = midpoint - 1
    return low


def emitted_population_design(
    fact_sets: Mapping[str, Sequence[Mapping[str, Any]]],
    burned: Sequence[Mapping[str, Any]],
) -> tuple[dict[str, int], dict[str, int]]:
    """Reconstruct emitted-assertion population strata from manifest-bound facts."""

    gate3: defaultdict[str, set[str]] = defaultdict(set)
    gate4: defaultdict[str, set[str]] = defaultdict(set)
    burn_index: defaultdict[tuple[str, str], list[tuple[int, int]]] = defaultdict(list)
    for coordinate in burned:
        burn_index[(str(coordinate["system"]), str(coordinate["path"]))].append(
            (int(coordinate["start_line"]), int(coordinate["end_line"]))
        )

    def is_burned(fact: Mapping[str, Any]) -> bool:
        start, end = int(fact["start_line"]), int(fact["end_line"])
        return any(
            start <= prior_end and end >= prior_start
            for prior_start, prior_end in burn_index.get(
                (str(fact["system"]), str(fact["path"])), ()
            )
        )

    for system in ID_SYSTEMS:
        for fact in fact_sets.get(f"{system}.identity", ()):
            if fact.get("predicate") == "EXTRACTION_FAILURE":
                continue
            if fact.get("tier") not in {"exact", "derived"}:
                continue
            if fact.get("predicate") not in GATE3_MERGE_PREDICATES:
                continue
            if is_burned(fact):
                continue
            stratum = f"g3:{system}:{fact['predicate']}"
            gate3[stratum].add(canonical_json(assertion_identity(fact, 3)))
    for system in FIELD_SYSTEMS:
        for fact in fact_sets.get(f"{system}.fields", ()):
            if fact.get("predicate") != "REFERENCES_PROTO_FIELD":
                continue
            if is_burned(fact):
                continue
            access = parse_detail(fact.get("detail", "")).get("access", "unknown")
            stratum = f"g4p:{system}:{access}"
            gate4[stratum].add(canonical_json(assertion_identity(fact, 4)))
    return (
        {stratum: len(values) for stratum, values in sorted(gate3.items())},
        {stratum: len(values) for stratum, values in sorted(gate4.items())},
    )


def rebuilt_candidate_population_design(
    corpus_dir: Path,
    locks: Mapping[str, Mapping[str, Any]],
    burned: Sequence[Mapping[str, Any]],
    manifest: Mapping[str, Any],
    dev: Sequence[Mapping[str, Any]],
    holdout: Sequence[Mapping[str, Any]],
) -> dict[str, int]:
    """Re-enumerate and reselect the Git candidate frame; trust no embedded N."""

    config = manifest["generator"]["config"]
    vocabulary = generated_field_vocabulary(corpus_dir, locks)
    candidates, lineage, populations, _ = gate4_candidate_sites(
        corpus_dir,
        locks,
        vocabulary,
        burned,
        seed=int(config["seed"]),
        hit_quota=int(config["candidate_hit_quota"]),
        open_quota=int(config["candidate_open_quota"]),
    )
    expected_dev, expected_holdout = split_sites(
        [*candidates, *lineage],
        seed=int(config["seed"]),
        holdout_fraction=float(config["holdout_fraction"]),
    )

    def relevant(records: Sequence[Mapping[str, Any]]) -> dict[str, str]:
        return {
            str(record["site_id"]): canonical_json(dict(record))
            for record in records
            if record.get("cohort") in {"candidate_recall", "lineage"}
        }

    if relevant(dev) != relevant(expected_dev) or relevant(holdout) != relevant(expected_holdout):
        raise ValidationError(
            "Gate-4 candidate/lineage artifacts do not equal deterministic Git re-enumeration"
        )
    return {f"g4c:{stratum}": population for stratum, population in sorted(populations.items())}


def _site_stratum(site: Mapping[str, Any]) -> str:
    if site.get("gate") == 3:
        expected = f"g3:{site['system']}:{site['predicate']}"
    elif site.get("gate") == 4 and site.get("cohort") == "assertion_precision":
        expected = f"g4p:{site['system']}:{site['reported_access']}"
    elif site.get("cohort") == "candidate_recall":
        expected = f"g4c:{site['population_stratum']}"
    else:
        raise ValidationError(f"site {site.get('site_id')!r} has no probability stratum")
    recorded = site.get("split_stratum")
    if recorded is not None and recorded != expected:
        raise ValidationError(f"site {site.get('site_id')!r} has inconsistent split stratum")
    return expected


def stratified_ratio_bound(
    sites: Sequence[Mapping[str, Any]],
    design: Mapping[str, int],
    *,
    domain: Callable[[Mapping[str, Any]], bool],
    success: Callable[[Mapping[str, Any]], bool],
    alpha_each: Fraction,
    population_is_domain: bool = False,
) -> dict[str, Any]:
    """Design-weighted ratio and simultaneous exact finite-population lower bound."""

    grouped: defaultdict[str, list[Mapping[str, Any]]] = defaultdict(list)
    for site in sites:
        stratum = _site_stratum(site)
        if stratum not in design:
            raise ValidationError(
                f"sample site {site.get('site_id')!r} is outside the emitted population"
            )
        grouped[stratum].append(site)

    estimated_successes = 0.0
    estimated_domain = 0.0
    lower_successes = 0
    upper_domain = 0
    raw_successes = 0
    raw_domain = 0
    uncovered: list[str] = []
    strata: dict[str, Any] = {}
    for stratum, population in sorted(design.items()):
        rows = grouped.get(stratum, [])
        sample = len(rows)
        if sample == 0:
            uncovered.append(stratum)
            strata[stratum] = {
                "population": population,
                "sample": 0,
                "domain": 0,
                "successes": 0,
            }
            continue
        if sample > population:
            raise ValidationError(
                f"stratum {stratum!r} sample {sample} exceeds population {population}"
            )
        domain_count = sum(bool(domain(row)) for row in rows)
        success_count = sum(bool(success(row)) for row in rows)
        if success_count > domain_count:
            raise ValidationError(f"stratum {stratum!r} has success outside metric domain")
        raw_successes += success_count
        raw_domain += domain_count
        estimated_successes += population * success_count / sample
        estimated_domain += population if population_is_domain else population * domain_count / sample
        lower = lower_population_total(population, sample, success_count, alpha_each)
        upper = (
            population
            if population_is_domain
            else upper_population_total(population, sample, domain_count, alpha_each)
        )
        lower_successes += lower
        upper_domain += upper
        strata[stratum] = {
            "population": population,
            "sample": sample,
            "domain": domain_count,
            "successes": success_count,
            "estimated_domain": population
            if population_is_domain
            else population * domain_count / sample,
            "estimated_successes": population * success_count / sample,
            "lower_successes": lower,
            "upper_domain": upper,
        }
    covered = not uncovered and bool(design)
    point = ratio(estimated_successes, estimated_domain) if covered else None
    lower = ratio(lower_successes, upper_domain) if covered else None
    return {
        "point": point,
        "lower": lower,
        "raw_successes": raw_successes,
        "raw_domain": raw_domain,
        "estimated_successes": estimated_successes,
        "estimated_domain": estimated_domain,
        "lower_successes": lower_successes,
        "upper_domain": upper_domain,
        "uncovered_strata": uncovered,
        "strata": strata,
    }


def confidence_family_size(
    gate3_design: Mapping[str, int],
    gate4_precision_design: Mapping[str, int],
    gate4_recall_design: Mapping[str, int],
) -> int:
    pair_strata = sum(
        stratum.endswith(":DEPLOYABLE_REACHES_OPERATION") for stratum in gate3_design
    )
    # G3 overall + pairwise; G4 overall precision, direct ratio and three
    # classification rates; G4 recall plus three classification domain ratios.
    return (
        len(gate3_design)
        + pair_strata
        + 6 * len(gate4_precision_design)
        + 8 * len(gate4_recall_design)
    )


def _artifact_path(base: Path, manifest: Mapping[str, Any], name: str) -> Path:
    return (base / str(manifest["artifacts"][name]["path"])).resolve()


def load_current(
    base: Path,
    corpus_dir: Path,
    locks: Mapping[str, Mapping[str, Any]],
    manifest: Mapping[str, Any],
    mode: str,
) -> tuple[
    dict[str, set[str]],
    dict[tuple[str, str], list[dict[str, Any]]],
    dict[str, Sequence[Mapping[str, Any]]],
]:
    fact_sets: dict[str, Sequence[Mapping[str, Any]]] = {}
    identity: dict[str, set[str]] = {}
    for system in ID_SYSTEMS:
        key = f"{system}.identity"
        descriptor = manifest["fact_files"].get(key)
        if not isinstance(descriptor, dict):
            raise ValidationError(f"manifest lacks {key} producer facts")
        facts = load_facts(
            (base / descriptor["path"]).resolve(), system=system,
            expected_commit=str(locks[system]["commit"]), corpus_dir=corpus_dir,
        )
        fact_sets[key] = facts
        identity[system] = {
            canonical_json(assertion_identity(fact, 3))
            for fact in facts if fact["predicate"] != "EXTRACTION_FAILURE"
        }
    fields: defaultdict[tuple[str, str], list[dict[str, Any]]] = defaultdict(list)
    if mode == "g34":
        for system in FIELD_SYSTEMS:
            key = f"{system}.fields"
            descriptor = manifest["fact_files"].get(key)
            if not isinstance(descriptor, dict):
                raise ValidationError(f"manifest lacks {key} producer facts")
            facts = load_facts(
                (base / descriptor["path"]).resolve(), system=system,
                expected_commit=str(locks[system]["commit"]), corpus_dir=corpus_dir,
            )
            fact_sets[key] = facts
            for fact in facts:
                if fact["predicate"] == "REFERENCES_PROTO_FIELD":
                    fields[(system, str(fact["path"]))].append(fact)
    expected_keys = {
        *(f"{system}.identity" for system in ID_SYSTEMS),
        *((f"{system}.fields" for system in FIELD_SYSTEMS) if mode == "g34" else ()),
    }
    if set(manifest["fact_files"]) != expected_keys:
        raise ValidationError("manifest has an incomplete or unexpected producer fact-file set")
    if producer_summary(fact_sets) != manifest["producer"]:
        raise ValidationError("manifest producer summary does not match validated facts")
    return identity, dict(fields), fact_sets


def matching_occurrence_facts(
    site: Mapping[str, Any], fields: Mapping[tuple[str, str], Sequence[dict[str, Any]]]
) -> list[dict[str, Any]]:
    return [
        fact
        for fact in fields.get((str(site["system"]), str(site["path"])), ())
        if fact_occurrence_matches(site, fact)
    ]


def score_gate3(
    sites: Sequence[Mapping[str, Any]], labels: Mapping[str, Mapping[str, Any]],
    current: Mapping[str, set[str]], producer: Mapping[str, Any],
    design: Mapping[str, int], alpha_each: Fraction,
) -> dict[str, Any]:
    correct = incorrect = withdrawn = 0
    pair_correct = pair_incorrect = 0
    bad: list[str] = []
    evaluated: list[dict[str, Any]] = []
    for site in sites:
        present = canonical_json(site["identity"]) in current[str(site["system"])]
        verdict = labels[site["site_id"]]["verdict"]
        is_correct = present and verdict == "correct"
        evaluated.append({**site, "_correct": is_correct})
        if not present:
            withdrawn += 1
            continue
        is_pair = site["predicate"] == "DEPLOYABLE_REACHES_OPERATION"
        if verdict == "correct":
            correct += 1
            pair_correct += is_pair
        else:
            incorrect += 1
            pair_incorrect += is_pair
            bad.append(str(site["site_id"]))
    overall = stratified_ratio_bound(
        evaluated, design, domain=lambda _: True, success=lambda row: row["_correct"],
        alpha_each=alpha_each, population_is_domain=True,
    )
    pair_design = {
        stratum: population for stratum, population in design.items()
        if stratum.endswith(":DEPLOYABLE_REACHES_OPERATION")
    }
    pair_sites = [
        site for site in evaluated if site["predicate"] == "DEPLOYABLE_REACHES_OPERATION"
    ]
    pair = stratified_ratio_bound(
        pair_sites, pair_design, domain=lambda _: True, success=lambda row: row["_correct"],
        alpha_each=alpha_each, population_is_domain=True,
    )
    pair_n = pair_correct + pair_incorrect
    reasons: list[str] = []
    capabilities = producer["capabilities"]
    if not capabilities["gate3_pairwise_measurement"] or pair_n < MINIMUM_EVIDENCE["gate3_pairwise_pairs"]:
        reasons.append("deployable-to-operation pairwise cohort is absent or undersized")
    if correct + incorrect < MINIMUM_EVIDENCE["gate3_assertions"]:
        reasons.append("Gate-3 assertion sample is below the predeclared minimum")
    if overall["uncovered_strata"]:
        reasons.append(
            "Gate-3 holdout misses emitted-population strata: "
            + repr(overall["uncovered_strata"])
        )
    if pair_design and pair["uncovered_strata"]:
        reasons.append(
            "Gate-3 pairwise holdout misses emitted-population strata: "
            + repr(pair["uncovered_strata"])
        )
    passed = (
        not reasons and withdrawn == 0 and overall["lower"] is not None
        and overall["lower"] >= GATE_THRESHOLDS["gate3_precision"]
        and pair["lower"] is not None
        and pair["lower"] >= GATE_THRESHOLDS["gate3_pairwise_precision"]
    )
    return {
        "status": "PASS" if passed else ("NOT ESTABLISHED" if reasons else "FAIL"),
        "not_established_reasons": reasons,
        "precision": overall["point"],
        "precision_95pct_lower": overall["lower"],
        "correct": correct,
        "incorrect": incorrect,
        "withdrawn": withdrawn,
        "pairwise_precision": pair["point"],
        "pairwise_precision_95pct_lower": pair["lower"],
        "pairwise_correct": pair_correct,
        "pairwise_incorrect": pair_incorrect,
        "population_design": overall["strata"],
        "bad_site_ids": bad,
        "passed": passed,
    }


def _exact_assertion_fact(
    site: Mapping[str, Any], facts: Sequence[Mapping[str, Any]]
) -> Mapping[str, Any] | None:
    wanted = canonical_json(site["identity"])
    for fact in facts:
        if canonical_json(assertion_identity(fact, 4)) == wanted:
            return fact
    return None


def score_gate4_precision(
    sites: Sequence[Mapping[str, Any]], labels: Mapping[str, Mapping[str, Any]],
    fields: Mapping[tuple[str, str], Sequence[dict[str, Any]]], producer: Mapping[str, Any],
    design: Mapping[str, int], alpha_each: Fraction,
) -> dict[str, Any]:
    correct = incorrect = withdrawn = 0
    direct_correct = direct_incorrect = 0
    evaluated: list[dict[str, Any]] = []
    for site in sites:
        facts = fields.get((str(site["system"]), str(site["path"])), ())
        fact = _exact_assertion_fact(site, facts)
        label = labels[site["site_id"]]
        verdict = label["verdict"]
        evaluated.append(
            {
                **site,
                "_direct": label["expected_access"] in DIRECT_ACCESS,
                "_verdict_correct": fact is not None and verdict == "correct",
                "_access_correct": fact is not None
                and parse_detail(fact.get("detail", "")).get("access")
                == label["expected_access"],
                "_role_correct": fact is not None
                and fact.get("code_role") == label["expected_code_role"],
                "_canonical_correct": fact is not None
                and canonical_fact_identity(fact) == expected_label_identity(label),
            }
        )
        if fact is None:
            withdrawn += 1
            continue
        correct += verdict == "correct"
        incorrect += verdict == "incorrect"
        # The direct denominator is human expected_access, never producer output.
        if label["expected_access"] in DIRECT_ACCESS:
            direct_correct += verdict == "correct"
            direct_incorrect += verdict == "incorrect"
    overall = stratified_ratio_bound(
        evaluated, design, domain=lambda _: True,
        success=lambda row: row["_verdict_correct"], alpha_each=alpha_each,
        population_is_domain=True,
    )
    direct = stratified_ratio_bound(
        evaluated, design, domain=lambda row: row["_direct"],
        success=lambda row: row["_direct"] and row["_verdict_correct"],
        alpha_each=alpha_each,
    )
    access = stratified_ratio_bound(
        evaluated, design, domain=lambda _: True,
        success=lambda row: row["_access_correct"], alpha_each=alpha_each,
        population_is_domain=True,
    )
    role = stratified_ratio_bound(
        evaluated, design, domain=lambda _: True,
        success=lambda row: row["_role_correct"], alpha_each=alpha_each,
        population_is_domain=True,
    )
    canonical = stratified_ratio_bound(
        evaluated, design, domain=lambda _: True,
        success=lambda row: row["_canonical_correct"], alpha_each=alpha_each,
        population_is_domain=True,
    )
    direct_n = direct_correct + direct_incorrect
    reasons: list[str] = []
    if not producer["capabilities"]["canonical_field_identity"]:
        reasons.append("producer lacks explicit canonical field identity")
    if direct_n < MINIMUM_EVIDENCE["gate4_direct_precision"]:
        reasons.append("direct precision sample is below the predeclared minimum")
    if overall["uncovered_strata"]:
        reasons.append(
            "Gate-4 precision holdout misses emitted-population strata: "
            + repr(overall["uncovered_strata"])
        )
    metrics = {
        "precision": overall["point"],
        "precision_95pct_lower": overall["lower"],
        "direct_precision": direct["point"],
        "direct_precision_95pct_lower": direct["lower"],
        "access_accuracy": access["point"],
        "access_accuracy_95pct_lower": access["lower"],
        "role_accuracy": role["point"],
        "role_accuracy_95pct_lower": role["lower"],
        "canonical_accuracy": canonical["point"],
        "canonical_accuracy_95pct_lower": canonical["lower"],
    }
    passed = (
        not reasons and withdrawn == 0
        and metrics["direct_precision_95pct_lower"] is not None
        and metrics["direct_precision_95pct_lower"] >= GATE_THRESHOLDS["gate4_direct_precision"]
        and metrics["access_accuracy_95pct_lower"] is not None
        and metrics["access_accuracy_95pct_lower"] >= GATE_THRESHOLDS["gate4_precision_access_accuracy"]
        and metrics["role_accuracy_95pct_lower"] is not None
        and metrics["role_accuracy_95pct_lower"] >= GATE_THRESHOLDS["gate4_precision_role_accuracy"]
        and metrics["canonical_accuracy_95pct_lower"] is not None
        and metrics["canonical_accuracy_95pct_lower"] >= GATE_THRESHOLDS["gate4_precision_canonical_accuracy"]
    )
    return {
        "status": "PASS" if passed else ("NOT ESTABLISHED" if reasons else "FAIL"),
        "not_established_reasons": reasons,
        **metrics,
        "correct": correct, "incorrect": incorrect, "withdrawn": withdrawn,
        "direct_correct": direct_correct, "direct_incorrect": direct_incorrect,
        "population_design": overall["strata"],
        "passed": passed,
    }


def score_gate4_recall(
    sites: Sequence[Mapping[str, Any]], labels: Mapping[str, Mapping[str, Any]],
    fields: Mapping[tuple[str, str], Sequence[dict[str, Any]]], producer: Mapping[str, Any],
    design: Mapping[str, int], alpha_each: Fraction,
) -> dict[str, Any]:
    trials = successes = 0
    per_system_trials: defaultdict[str, int] = defaultdict(int)
    misses: list[str] = []
    evaluated: list[dict[str, Any]] = []
    for site in sites:
        label = labels[site["site_id"]]
        eligible_direct = (
            label["eligible"] == "yes" and label["expected_access"] in DIRECT_ACCESS
        )
        canonical_matches: list[Mapping[str, Any]] = []
        if eligible_direct:
            expected = expected_label_identity(label)
            canonical_matches = [
                fact for fact in matching_occurrence_facts(site, fields)
                if canonical_fact_identity(fact) == expected
            ]
        access_match = eligible_direct and any(
            parse_detail(fact.get("detail", "")).get("access") == label["expected_access"]
            for fact in canonical_matches
        )
        role_match = eligible_direct and any(
            fact.get("code_role") == label["expected_code_role"]
            for fact in canonical_matches
        )
        # A recall success is one canonical emitted fact carrying both correct
        # classifications; two contradictory facts cannot be combined.
        joint_match = eligible_direct and any(
            parse_detail(fact.get("detail", "")).get("access") == label["expected_access"]
            and fact.get("code_role") == label["expected_code_role"]
            for fact in canonical_matches
        )
        evaluated.append(
            {
                **site,
                "_eligible_direct": eligible_direct,
                "_canonical_correct": eligible_direct and bool(canonical_matches),
                "_access_correct": access_match,
                "_role_correct": role_match,
                "_joint_correct": joint_match,
            }
        )
        if eligible_direct:
            trials += 1
            successes += bool(joint_match)
            per_system_trials[str(site["system"])] += 1
            if not joint_match:
                misses.append(str(site["site_id"]))
    direct = stratified_ratio_bound(
        evaluated, design, domain=lambda row: row["_eligible_direct"],
        success=lambda row: row["_joint_correct"], alpha_each=alpha_each,
    )
    access_metric = stratified_ratio_bound(
        evaluated, design, domain=lambda row: row["_eligible_direct"],
        success=lambda row: row["_access_correct"], alpha_each=alpha_each,
    )
    role_metric = stratified_ratio_bound(
        evaluated, design, domain=lambda row: row["_eligible_direct"],
        success=lambda row: row["_role_correct"], alpha_each=alpha_each,
    )
    canonical_metric = stratified_ratio_bound(
        evaluated, design, domain=lambda row: row["_eligible_direct"],
        success=lambda row: row["_canonical_correct"], alpha_each=alpha_each,
    )
    reasons: list[str] = []
    if not producer["capabilities"]["canonical_field_identity"]:
        reasons.append("producer lacks explicit canonical field identity")
    if trials < MINIMUM_EVIDENCE["gate4_direct_recall_eligible"]:
        reasons.append("direct eligible recall sample is below the predeclared minimum")
    deficient = {
        system: per_system_trials.get(system, 0) for system in FIELD_SYSTEMS
        if per_system_trials.get(system, 0) < MINIMUM_EVIDENCE["gate4_direct_recall_per_system"]
    }
    if deficient:
        reasons.append(f"per-system direct eligible evidence is below minimum: {deficient}")
    if direct["uncovered_strata"]:
        reasons.append(
            "Gate-4 recall holdout misses candidate-population strata: "
            + repr(direct["uncovered_strata"])
        )
    passed = (
        not reasons and direct["lower"] is not None
        and direct["lower"] >= GATE_THRESHOLDS["gate4_direct_recall"]
        and access_metric["lower"] is not None
        and access_metric["lower"] >= GATE_THRESHOLDS["gate4_recall_access_accuracy"]
        and role_metric["lower"] is not None
        and role_metric["lower"] >= GATE_THRESHOLDS["gate4_recall_role_accuracy"]
    )
    return {
        "status": "PASS" if passed else ("NOT ESTABLISHED" if reasons else "FAIL"),
        "not_established_reasons": reasons,
        "direct_recall": direct["point"],
        "direct_recall_95pct_lower": direct["lower"],
        "direct_trials": trials,
        "direct_successes": successes,
        "weighted_direct_trials": direct["estimated_domain"],
        "weighted_direct_successes": direct["estimated_successes"],
        "access_accuracy": access_metric["point"],
        "access_accuracy_95pct_lower": access_metric["lower"],
        "role_accuracy": role_metric["point"],
        "role_accuracy_95pct_lower": role_metric["lower"],
        "canonical_accuracy": canonical_metric["point"],
        "canonical_accuracy_95pct_lower": canonical_metric["lower"],
        "per_system_trials": dict(sorted(per_system_trials.items())),
        "population_design": direct["strata"],
        "miss_site_ids": misses,
        "passed": passed,
    }


def score_lineage(
    sites: Sequence[Mapping[str, Any]], labels: Mapping[str, Mapping[str, Any]],
    fields: Mapping[tuple[str, str], Sequence[dict[str, Any]]], producer: Mapping[str, Any],
) -> dict[str, Any]:
    cases: defaultdict[str, list[Mapping[str, Any]]] = defaultdict(list)
    for site in sites:
        cases[str(site["lineage_case_id"])].append(site)
    case_results: dict[str, Any] = {}
    overall = bool(cases) and producer["capabilities"]["canonical_field_identity"]
    for case_id, members in sorted(cases.items()):
        implementations = {
            (str(site["implementation_module"]), str(site["implementation_version"]))
            for site in members
        }
        expected = {expected_label_identity(labels[site["site_id"]]) for site in members}
        canonical_sites = []
        for site in members:
            wanted = expected_label_identity(labels[site["site_id"]])
            if any(
                canonical_fact_identity(fact) == wanted
                for fact in matching_occurrence_facts(site, fields)
            ):
                canonical_sites.append(str(site["site_id"]))
        passed = (
            len(implementations) >= MINIMUM_EVIDENCE["gate4_lineage_implementations"]
            and len(expected) == 1 and len(canonical_sites) == len(members)
        )
        overall = overall and passed
        case_results[case_id] = {
            "implementations": [list(value) for value in sorted(implementations)],
            "expected_identities": [list(value) for value in sorted(expected)],
            "canonical_site_ids": canonical_sites,
            "passed": passed,
        }
    reasons = [] if producer["capabilities"]["canonical_field_identity"] else [
        "producer lacks explicit canonical field identity"
    ]
    if not cases:
        reasons.append("lineage cohort is absent")
    return {
        "status": "PASS" if overall else ("NOT ESTABLISHED" if reasons else "FAIL"),
        "not_established_reasons": reasons,
        "cases": case_results,
        "passed": overall,
    }


def validate_g34_counts(
    manifest: Mapping[str, Any], dev: Sequence[Mapping[str, Any]],
    holdout: Sequence[Mapping[str, Any]], burn_count: int, burn_digest: str,
) -> None:
    counts = manifest["counts"]
    summary = summarize_sites(dev, holdout)
    for split in ("dev", "holdout"):
        if counts.get(split) != summary[split]:
            raise ValidationError(f"manifest {split} cohort/sample counts do not match artifacts")
    candidates = [site for site in [*dev, *holdout] if site.get("cohort") == "candidate_recall"]
    populations: dict[str, int] = {}
    sampled: defaultdict[str, int] = defaultdict(int)
    for site in candidates:
        stratum = str(site["population_stratum"])
        population = site.get("population_size")
        total_sample = site.get("sample_size_total")
        if not isinstance(population, int) or not isinstance(total_sample, int):
            raise ValidationError("candidate population/sample counts must be integers")
        if stratum in populations and populations[stratum] != population:
            raise ValidationError(f"inconsistent candidate population size for {stratum}")
        populations[stratum] = population
        sampled[stratum] += 1
        split_n = site.get("split_sample_size")
        weight = site.get("sample_weight")
        if not isinstance(split_n, int) or split_n <= 0 or not isinstance(weight, (int, float)):
            raise ValidationError(f"invalid split count/weight for {site['site_id']}")
        if not math.isclose(float(weight), population / split_n, rel_tol=1e-12):
            raise ValidationError(f"sample weight is not population/split-sample for {site['site_id']}")
    for site in candidates:
        if site["sample_size_total"] != sampled[str(site["population_stratum"])]:
            raise ValidationError("candidate total sample count is inconsistent")
    if counts.get("candidate_population_by_stratum") != dict(sorted(populations.items())):
        raise ValidationError("manifest candidate population counts do not match artifacts")
    if counts.get("candidate_population") != sum(populations.values()):
        raise ValidationError("manifest candidate population total does not match artifacts")
    if counts.get("candidate_sample") != len(candidates):
        raise ValidationError("manifest candidate sample count does not match artifacts")
    if counts.get("lineage_sites") != sum(site.get("cohort") == "lineage" for site in [*dev, *holdout]):
        raise ValidationError("manifest lineage count does not match artifacts")
    ledger = manifest["burn_ledger"]
    if ledger["coordinate_count"] != burn_count or ledger["coordinates_sha256"] != burn_digest:
        raise ValidationError("manifest burn ledger projection does not match committed ledger")


def score_g34(
    base: Path, manifest: Mapping[str, Any], labels_path: Path, split: str,
    identity: Mapping[str, set[str]], fields: Mapping[tuple[str, str], Sequence[dict[str, Any]]],
    fact_sets: Mapping[str, Sequence[Mapping[str, Any]]],
    burn: Sequence[Mapping[str, Any]], burn_digest: str,
    corpus_dir: Path, locks: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    dev_records = read_jsonl(_artifact_path(base, manifest, "dev_sites"))
    holdout_records = read_jsonl(_artifact_path(base, manifest, "holdout_sites"))
    key_records = read_jsonl(_artifact_path(base, manifest, "key"))
    dev, holdout, _ = validate_artifacts(dev_records, holdout_records, key_records)
    validate_g34_counts(manifest, list(dev.values()), list(holdout.values()), len(burn), burn_digest)
    all_sites = {**dev, **holdout}
    selected_map = all_sites if split == "all" else (dev if split == "dev" else holdout)
    selected = list(selected_map.values())
    labels = validate_labels(read_jsonl(labels_path), all_sites, selected_map)
    gate3_design, gate4_precision_design = emitted_population_design(fact_sets, burn)
    gate4_recall_design = rebuilt_candidate_population_design(
        corpus_dir, locks, burn, manifest, list(dev.values()), list(holdout.values())
    )
    family_size = confidence_family_size(
        gate3_design, gate4_precision_design, gate4_recall_design
    )
    if family_size <= 0:
        raise ValidationError("Gate-3/4 benchmark has no confidence-bound family")
    alpha_each = (Fraction(1, 1) - GATE_CONFIDENCE) / family_size
    g3 = score_gate3(
        [site for site in selected if site["gate"] == 3], labels, identity,
        manifest["producer"], gate3_design, alpha_each,
    )
    precision = score_gate4_precision(
        [site for site in selected if site["gate"] == 4 and site["cohort"] == "assertion_precision"],
        labels, fields, manifest["producer"], gate4_precision_design, alpha_each,
    )
    recall = score_gate4_recall(
        [site for site in selected if site.get("cohort") == "candidate_recall"],
        labels, fields, manifest["producer"], gate4_recall_design, alpha_each,
    )
    lineage = score_lineage(
        [site for site in selected if site.get("cohort") == "lineage"],
        labels, fields, manifest["producer"],
    )
    gate4_passed = precision["passed"] and recall["passed"] and lineage["passed"]
    decision_split = split == "holdout"
    passed = decision_split and g3["passed"] and gate4_passed
    return {
        "split": split,
        "status": "PASS" if passed else ("FAIL" if decision_split else "DIAGNOSTIC"),
        "confidence": {
            "joint": float(GATE_CONFIDENCE),
            "method": "Bonferroni simultaneous exact finite-population bounds",
            "family_size": family_size,
            "per_bound_alpha": float(alpha_each),
        },
        "gate3": g3,
        "gate4": {"precision": precision, "recall": recall, "lineage": lineage, "passed": gate4_passed},
        "passed": passed,
    }


def score_h2(
    base: Path, manifest: Mapping[str, Any], labels_path: Path,
    identity: Mapping[str, set[str]], fact_sets: Mapping[str, Sequence[Mapping[str, Any]]],
    burn: Sequence[Mapping[str, Any]], burn_digest: str,
    lock_path: Path, locks: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    sites_records = read_jsonl(_artifact_path(base, manifest, "sites"))
    key_records = read_jsonl(_artifact_path(base, manifest, "key"))
    sites, _ = validate_single_artifact(sites_records, key_records)
    counts = manifest["counts"]
    if counts.get("sites") != len(sites):
        raise ValidationError("manifest holdout-2 count does not match artifact")
    if counts.get("by_system") != {
        system: sum(site["system"] == system for site in sites.values()) for system in ID_SYSTEMS
    }:
        raise ValidationError("manifest holdout-2 system counts do not match artifact")
    ledger = manifest["burn_ledger"]
    if ledger["coordinate_count"] != len(burn) or ledger["coordinates_sha256"] != burn_digest:
        raise ValidationError("manifest burn ledger projection mismatch")
    labels = validate_labels(read_jsonl(labels_path), sites, sites)
    upstream = validate_manifest(
        base / "labeling" / "g34.v3.manifest.json", base=base, mode="g34",
        lock_path=lock_path, locks=locks,
        artifact_names={"dev_sites", "holdout_sites", "key", "label_template"},
    )
    expected_upstream = manifest["generator"]["config"].get("upstream_g34_manifest_digest")
    if upstream["manifest_digest"] != expected_upstream:
        raise ValidationError("holdout-2 is bound to a different Gate-3/4 generation")
    prior_dev = read_jsonl(_artifact_path(base, upstream, "dev_sites"))
    prior_holdout = read_jsonl(_artifact_path(base, upstream, "holdout_sites"))
    prior_keys = read_jsonl(_artifact_path(base, upstream, "key"))
    validate_artifacts(prior_dev, prior_holdout, prior_keys)
    require_fresh_blind_population(len(prior_dev) + len(prior_holdout))
    prior_burn = [*burn, *(coordinate_for(site) for site in [*prior_dev, *prior_holdout])]
    gate3_design, _ = emitted_population_design(fact_sets, prior_burn)
    family_size = confidence_family_size(gate3_design, {}, {})
    if family_size <= 0:
        raise ValidationError("Gate-3 holdout-2 has no confidence-bound family")
    alpha_each = (Fraction(1, 1) - GATE_CONFIDENCE) / family_size
    gate3 = score_gate3(
        list(sites.values()), labels, identity, manifest["producer"],
        gate3_design, alpha_each,
    )
    return {
        "evaluation": "holdout-2",
        "status": gate3["status"],
        "confidence": {
            "joint": float(GATE_CONFIDENCE),
            "method": "Bonferroni simultaneous exact finite-population bounds",
            "family_size": family_size,
            "per_bound_alpha": float(alpha_each),
        },
        "gate3": gate3,
        "passed": gate3["passed"],
    }


def print_human(result: Mapping[str, Any]) -> None:
    print(f"status={result['status']}")
    gate3 = result["gate3"]
    print(
        f"g3: status={gate3['status']} precision={pct(gate3['precision'])} "
        f"lower={pct(gate3['precision_95pct_lower'])} "
        f"pairwise={pct(gate3['pairwise_precision'])} "
        f"pairwise-lower={pct(gate3['pairwise_precision_95pct_lower'])} "
        f"withdrawn={gate3['withdrawn']}"
    )
    for reason in gate3["not_established_reasons"]:
        print(f"g3 NOT ESTABLISHED: {reason}")
    if "gate4" not in result:
        return
    gate4 = result["gate4"]
    precision, recall = gate4["precision"], gate4["recall"]
    print(
        f"g4 precision: status={precision['status']} "
        f"direct={pct(precision['direct_precision'])}/{pct(precision['direct_precision_95pct_lower'])} "
        f"access={pct(precision['access_accuracy'])}/{pct(precision['access_accuracy_95pct_lower'])} "
        f"role={pct(precision['role_accuracy'])}/{pct(precision['role_accuracy_95pct_lower'])} "
        f"canonical={pct(precision['canonical_accuracy'])}/{pct(precision['canonical_accuracy_95pct_lower'])}"
    )
    print(
        f"g4 recall: status={recall['status']} "
        f"direct={pct(recall['direct_recall'])}/{pct(recall['direct_recall_95pct_lower'])} "
        f"access={pct(recall['access_accuracy'])}/{pct(recall['access_accuracy_95pct_lower'])} "
        f"role={pct(recall['role_accuracy'])}/{pct(recall['role_accuracy_95pct_lower'])} "
        f"sample={recall['direct_successes']}/{recall['direct_trials']}"
    )
    print(f"g4 lineage: status={gate4['lineage']['status']} pass={gate4['lineage']['passed']}")


def main(argv: Iterable[str] | None = None) -> int:
    args = parse_args(argv)
    if args.enforce_gates and args.mode == "g34" and args.split != "holdout":
        raise ValidationError("--enforce-gates is allowed only for the holdout split")
    if args.mode == "h2" and args.split != "holdout":
        raise ValidationError("holdout-2 supports only --split holdout")
    if args.enforce_gates and args.skip_corpus_check:
        raise ValidationError("cannot enforce gates while skipping corpus verification")

    base = args.base.resolve()
    corpus_dir = (args.corpus_dir or base / "corpus").resolve()
    lock_path = base / "corpus.lock.json"
    locks = load_corpus_lock(lock_path)
    manifest_path = base / "labeling" / (
        "g34.v3.manifest.json" if args.mode == "g34" else "g3h2.v3.manifest.json"
    )
    artifact_names = (
        {"dev_sites", "holdout_sites", "key", "label_template"}
        if args.mode == "g34" else {"sites", "key", "label_template"}
    )
    manifest = validate_manifest(
        manifest_path, base=base, mode=args.mode, lock_path=lock_path,
        locks=locks, artifact_names=artifact_names,
    )
    if not manifest["corpus"]["verified_clean_before_and_after"]:
        raise ValidationError("manifest was generated without complete corpus verification")
    systems = ID_SYSTEMS if args.mode == "h2" else set(ID_SYSTEMS) | set(FIELD_SYSTEMS)
    if not args.skip_corpus_check:
        verify_corpus(corpus_dir, locks, systems)
    identity, fields, fact_sets = load_current(base, corpus_dir, locks, manifest, args.mode)
    burn, burn_digest = load_burn_ledger(base / "labeling" / "burn-ledger.json")
    if args.mode == "g34":
        result = score_g34(
            base, manifest, args.labels.resolve(), args.split, identity, fields,
            fact_sets, burn, burn_digest, corpus_dir, locks,
        )
    else:
        result = score_h2(
            base, manifest, args.labels.resolve(), identity, fact_sets, burn,
            burn_digest, lock_path, locks,
        )
    if not args.skip_corpus_check:
        verify_corpus(corpus_dir, locks, systems)  # post-score immutability check
    else:
        result["passed"] = False
        result["status"] = "NOT ESTABLISHED"
        result["verification"] = "current corpus check skipped"
    if args.as_json:
        print(json.dumps(result, sort_keys=True, indent=2))
    else:
        print_human(result)
    # Failed or merely diagnostic gates are nonzero even without --enforce-gates.
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValidationError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)
