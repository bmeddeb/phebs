"""Tests for the shared scoring discipline."""

from __future__ import annotations

import unittest

import harness as pilot_harness
import scoring

DIGEST = "sha256:" + "a" * 64


def threshold(kind: str = "min", value: float = 0.75, conditional: float | None = None) -> dict:
    spec = {"kind": kind, "value": value}
    if conditional is not None:
        spec["conditional_value"] = conditional
    return spec


def parameters(**claims: dict) -> dict:
    return {
        "schema": scoring.PARAMETERS_SCHEMA,
        "status": "sealed",
        "claims": claims or {"caller_invocation": threshold()},
    }


def score(field: str = "caller_invocation", yes: int = 8, no: int = 2, unsure: int = 0) -> dict:
    decided = yes + no
    value = {
        "schema": pilot_harness.HARNESS_SCHEMA,
        "field": field,
        "sampled": decided + unsure,
        "yes": yes,
        "no": no,
        "unsure": unsure,
        "decided": decided,
    }
    if decided > 0:
        proportion = yes / decided
        value["proportion"] = proportion
        value["wilson_low"] = max(0.0, proportion - 0.1)
        value["wilson_high"] = min(1.0, proportion + 0.1)
    return value


class SealedParametersTest(unittest.TestCase):
    def test_valid_parameters_survive_validation(self) -> None:
        value = parameters(caller_invocation=threshold())
        sealed = scoring.require_sealed_parameters(value)
        self.assertEqual(sealed, {"schema": scoring.PARAMETERS_SCHEMA, "status": "sealed",
                                  "claims": {"caller_invocation": {"kind": "min", "value": 0.75}}})

    def test_refusal_cases(self) -> None:
        cases = {
            "not a mapping": [],
            "missing claims": {"schema": scoring.PARAMETERS_SCHEMA, "status": "sealed"},
            "extra field": {**parameters(), "extra": 1},
            "wrong schema": {**parameters(), "schema": "other"},
            "not sealed": {**parameters(), "status": "approved_not_sealed"},
            "no claims": {**parameters(), "claims": {}},
            "claims not a mapping": {**parameters(), "claims": []},
            "empty claim name": {**parameters(), "claims": {"": threshold()}},
            "unknown threshold field": {**parameters(), "claims": {"c": {"kind": "min", "value": 0.5, "extra": 1}}},
            "unknown kind": {**parameters(), "claims": {"c": threshold(kind="around")}},
            "nan value": {**parameters(), "claims": {"c": {"kind": "min", "value": float("nan")}}},
            "bool value": {**parameters(), "claims": {"c": {"kind": "min", "value": True}}},
            "conditional stronger than pass": {**parameters(), "claims": {"c": threshold(conditional=0.9)}},
            "max conditional stronger than pass": {
                **parameters(),
                "claims": {"c": threshold(kind="max", value=5.0, conditional=4.0)},
            },
        }
        for name, value in cases.items():
            with self.subTest(name):
                with self.assertRaises(scoring.ScoringError):
                    scoring.require_sealed_parameters(value)

    def test_sealed_threshold_refuses_unsealed_claims(self) -> None:
        value = parameters(caller_invocation=threshold())
        resolved = scoring.sealed_threshold(value, "caller_invocation")
        resolved["value"] = 0.0
        self.assertEqual(scoring.sealed_threshold(value, "caller_invocation")["value"], 0.75)
        with self.assertRaises(scoring.ScoringError):
            scoring.sealed_threshold(value, "caller_registration")

    def test_claim_coverage_is_exact(self) -> None:
        value = parameters(a=threshold(), b=threshold(kind="max", value=2.0))
        scoring.require_claim_coverage(value, ["b", "a"])
        with self.assertRaises(scoring.ScoringError):
            scoring.require_claim_coverage(value, ["a"])
        with self.assertRaises(scoring.ScoringError):
            scoring.require_claim_coverage(value, ["a", "b", "c"])


class VerdictTest(unittest.TestCase):
    def test_minimum_kind(self) -> None:
        self.assertEqual(scoring.verdict_for(0.8, threshold()), "pass")
        self.assertEqual(scoring.verdict_for(0.75, threshold()), "pass")
        self.assertEqual(scoring.verdict_for(0.7, threshold(conditional=0.65)), "conditional")
        self.assertEqual(scoring.verdict_for(0.6, threshold(conditional=0.65)), "fail")
        self.assertEqual(scoring.verdict_for(0.7, threshold()), "fail")

    def test_maximum_kind(self) -> None:
        spec = threshold(kind="max", value=5.0)
        self.assertEqual(scoring.verdict_for(4.0, spec), "pass")
        self.assertEqual(scoring.verdict_for(5.0, spec), "pass")
        self.assertEqual(scoring.verdict_for(6.0, threshold(kind="max", value=5.0, conditional=8.0)), "conditional")
        self.assertEqual(scoring.verdict_for(9.0, threshold(kind="max", value=5.0, conditional=8.0)), "fail")

    def test_absent_estimate_is_not_measured(self) -> None:
        self.assertEqual(scoring.verdict_for(None, threshold()), "not_measured")

    def test_malformed_inputs_refuse(self) -> None:
        for estimate in (float("nan"), True, "0.8"):
            with self.subTest(estimate=estimate):
                with self.assertRaises(scoring.ScoringError):
                    scoring.verdict_for(estimate, threshold())
        with self.assertRaises(scoring.ScoringError):
            scoring.verdict_for(0.8, {"kind": "around", "value": 0.5})


class QualityRowTest(unittest.TestCase):
    def test_passing_success_row(self) -> None:
        row = scoring.quality_row(score(), parameters(), outcome="success")
        self.assertEqual(row["claim"], "caller_invocation")
        self.assertEqual(row["sampled"], 10)
        self.assertEqual(row["decided"], 10)
        self.assertEqual(row["estimate"], 0.8)
        self.assertEqual(row["result"], "pass")
        self.assertEqual(row["threshold"], {"kind": "min", "value": 0.75})

    def test_failing_and_conditional_rows(self) -> None:
        below = scoring.quality_row(score(yes=6, no=4), parameters(), outcome="success")
        self.assertEqual(below["result"], "fail")
        band = parameters(caller_invocation=threshold(conditional=0.65))
        conditional = scoring.quality_row(score(yes=7, no=3), band, outcome="success")
        self.assertEqual(conditional["result"], "conditional")

    def test_zero_denominator_is_not_measured_with_visible_counts(self) -> None:
        row = scoring.quality_row(score(yes=0, no=0, unsure=3), parameters(), outcome="success")
        self.assertEqual(row["result"], "not_measured")
        self.assertIsNone(row["estimate"])
        self.assertEqual((row["decided"], row["unsure"]), (0, 3))

    def test_non_successful_outcomes_cannot_pass(self) -> None:
        for outcome in ("stop", "failed", "not_run"):
            with self.subTest(outcome):
                row = scoring.quality_row(score(), parameters(), outcome=outcome)
                self.assertEqual(row["result"], "not_measured")
                self.assertEqual(row["estimate"], 0.8)

    def test_refusal_cases(self) -> None:
        cases = {
            "unknown outcome": (score(), parameters(), "perhaps"),
            "claim without sealed threshold": (score(field="other"), parameters(), "success"),
            "wrong schema": ({**score(), "schema": "other"}, parameters(), "success"),
            "decided disagrees": ({**score(), "decided": 9}, parameters(), "success"),
            "sampled disagrees": ({**score(), "sampled": 99}, parameters(), "success"),
            "interval inverted": ({**score(), "wilson_low": 0.95}, parameters(), "success"),
            "bool count": ({**score(), "no": True}, parameters(), "success"),
        }
        for name, (value, sealed, outcome) in cases.items():
            with self.subTest(name):
                with self.assertRaises(scoring.ScoringError):
                    scoring.quality_row(value, sealed, outcome=outcome)
        zero = score(yes=0, no=0, unsure=3)
        zero["proportion"] = 1.0
        zero["wilson_low"] = 0.0
        zero["wilson_high"] = 1.0
        with self.assertRaises(scoring.ScoringError):
            scoring.quality_row(zero, parameters(), outcome="success")


class DirectMeasurementTest(unittest.TestCase):
    def test_direct_record_passes(self) -> None:
        record = {"provenance": "direct", "frame_sha256": DIGEST, "sample_size": 10}
        self.assertEqual(scoring.require_direct_measurement(record), record)

    def test_derived_or_composed_records_refuse(self) -> None:
        cases = {
            "derived provenance": {"provenance": "derived", "frame_sha256": DIGEST, "sample_size": 10},
            "component multiplication": {"provenance": "direct", "frame_sha256": DIGEST, "sample_size": 10,
                                         "component_families": ["a", "b"]},
            "component listing": {"provenance": "direct", "derived_from": ["a"]},
            "missing digest": {"provenance": "direct", "frame_sha256": "a" * 64, "sample_size": 10},
            "empty sample": {"provenance": "direct", "frame_sha256": DIGEST, "sample_size": 0},
            "bool sample": {"provenance": "direct", "frame_sha256": DIGEST, "sample_size": True},
        }
        for name, record in cases.items():
            with self.subTest(name):
                with self.assertRaises(scoring.ScoringError):
                    scoring.require_direct_measurement(record)


class StratumDigestTest(unittest.TestCase):
    ROWS = [{"stratum": "recall", "size": 40, "drawn": 24}, {"stratum": "precision", "size": 30, "drawn": 18}]

    def test_digest_reuses_the_frozen_commitment(self) -> None:
        digest = scoring.stratum_table_digest(self.ROWS)
        self.assertEqual(digest, pilot_harness.rows_commitment(self.ROWS))
        self.assertTrue(digest.startswith("sha256:"))
        reordered = [{"drawn": 24, "size": 40, "stratum": "recall"}, dict(self.ROWS[1])]
        self.assertEqual(scoring.stratum_table_digest(reordered), digest)
        changed = [dict(self.ROWS[0], drawn=23), dict(self.ROWS[1])]
        self.assertNotEqual(scoring.stratum_table_digest(changed), digest)

    def test_refusal_cases(self) -> None:
        cases = {
            "empty": [],
            "not a sequence": "strata",
            "non-mapping row": [1],
            "nan value": [{"x": float("nan")}],
        }
        for name, rows in cases.items():
            with self.subTest(name):
                with self.assertRaises(scoring.ScoringError):
                    scoring.stratum_table_digest(rows)


if __name__ == "__main__":
    unittest.main()
