from __future__ import annotations

import unittest
from typing import Any

import harness


def envelope(assertions: list[dict[str, Any]], evidence: list[dict[str, Any]]) -> dict[str, Any]:
    return {"id": "pb_" + "0" * 64, "bundle": {"assertions": assertions, "evidence": evidence}}


def assertion(**changes: Any) -> dict[str, Any]:
    value: dict[str, Any] = {
        "id": "as-1",
        "predicate": "CALLS_OPERATION",
        "object": "/shop.Cart/Get",
        "lineage": "lineage-1",
        "tier": "heuristic",
        "code_role": "production",
        "repo": "example.com/consumer",
        "run_id": "run-1",
        "supporting": ["ea-1"],
    }
    value.update(changes)
    return value


def evidence_item(**changes: Any) -> dict[str, Any]:
    value: dict[str, Any] = {
        "repository": "example.com/consumer",
        "run_id": "run-1",
        "atom": {"id": "ea-1", "start_byte": 120, "end_byte": 140},
        "occurrences": [
            {
                "repo": "example.com/consumer",
                "run_id": "run-1",
                "commit": "a" * 40,
                "path": "client/cart.go",
                "start_line": 7,
                "end_line": 7,
            }
        ],
    }
    value.update(changes)
    return value


def label(site_id: str, **changes: Any) -> dict[str, Any]:
    value: dict[str, Any] = {
        "site_id": site_id,
        "invocation": "yes",
        "operation": "shop.Cart/Get",
        "registration": "no",
        "service": None,
        "expected_code_role": "production",
        "rationale": "call through generated client",
        "evidence": "reviewed source span",
    }
    value.update(changes)
    return value


class CandidateRowsTest(unittest.TestCase):
    def test_projects_pinned_site_identity(self) -> None:
        rows = harness.candidate_rows(envelope([assertion()], [evidence_item()]))
        self.assertEqual(len(rows), 1)
        self.assertEqual(
            rows[0]["site_id"],
            "example.com/consumer@" + "a" * 40 + ":client/cart.go:120-140",
        )
        self.assertEqual(rows[0]["predicate"], "CALLS_OPERATION")
        self.assertEqual(rows[0]["start_line"], 7)

    def test_missing_evidence_fails_closed(self) -> None:
        with self.assertRaises(harness.HarnessError):
            harness.candidate_rows(envelope([assertion(supporting=["ea-absent"])], [evidence_item()]))

    def test_unsupported_predicate_fails_closed(self) -> None:
        with self.assertRaises(harness.HarnessError):
            harness.candidate_rows(envelope([assertion(predicate="SOMETHING_ELSE")], [evidence_item()]))

    def test_occurrence_scope_mismatch_fails_closed(self) -> None:
        bad = evidence_item()
        bad["occurrences"][0]["repo"] = "example.com/other"
        with self.assertRaises(harness.HarnessError):
            harness.candidate_rows(envelope([assertion()], [bad]))

    def test_commitment_is_stable_and_content_sensitive(self) -> None:
        rows = harness.candidate_rows(envelope([assertion()], [evidence_item()]))
        first = harness.rows_commitment(rows)
        self.assertEqual(first, harness.rows_commitment(list(rows)))
        rows[0]["object"] = "/shop.Cart/Delete"
        self.assertNotEqual(first, harness.rows_commitment(rows))


class SamplingTest(unittest.TestCase):
    seed = "ab" * 32

    def test_deterministic_and_stratum_bounded(self) -> None:
        strata = {
            "production": [f"site-{i}" for i in range(20)],
            "generated": [f"gen-{i}" for i in range(5)],
        }
        sizes = {"production": 3, "generated": 2}
        first = harness.stratified_sample(strata, sizes, self.seed)
        second = harness.stratified_sample(strata, sizes, self.seed)
        self.assertEqual(first, second)
        self.assertEqual(len(first["production"]), 3)
        self.assertEqual(len(first["generated"]), 2)
        self.assertTrue(set(first["production"]) <= set(strata["production"]))
        other = harness.stratified_sample(strata, sizes, "cd" * 32)
        self.assertNotEqual(first, other)

    def test_rejects_invalid_seed_and_oversized_request(self) -> None:
        with self.assertRaises(harness.HarnessError):
            harness.stratified_sample({"s": ["a"]}, {"s": 1}, "not-a-seed")
        with self.assertRaises(harness.HarnessError):
            harness.stratified_sample({"s": ["a"]}, {"s": 2}, self.seed)
        with self.assertRaises(harness.HarnessError):
            harness.stratified_sample({"s": ["a", "a"]}, {"s": 1}, self.seed)


class StatisticsTest(unittest.TestCase):
    def test_minimum_sample_size_conservative_default(self) -> None:
        self.assertEqual(harness.minimum_sample_size(0.05), 385)
        self.assertEqual(harness.minimum_sample_size(0.03, 1.96, 0.9), 385)

    def test_wilson_interval_known_value(self) -> None:
        low, high = harness.wilson_interval(90, 100)
        self.assertEqual(round(low, 3), 0.826)
        self.assertEqual(round(high, 3), 0.945)
        with self.assertRaises(harness.HarnessError):
            harness.wilson_interval(5, 0)


class ScoreClaimTest(unittest.TestCase):
    def test_scores_and_reports_unsure_separately(self) -> None:
        sampled = ["site-a", "site-b", "site-c"]
        labels = [
            label("site-a"),
            label("site-b", invocation="no", operation=None),
            label("site-c", invocation="unsure", operation=None),
        ]
        result = harness.score_claim(sampled, labels, "invocation")
        self.assertEqual((result["yes"], result["no"], result["unsure"]), (1, 1, 1))
        self.assertEqual(result["decided"], 2)
        self.assertEqual(result["proportion"], 0.5)
        self.assertIn("wilson_low", result)

    def test_incomplete_label_coverage_fails_closed(self) -> None:
        with self.assertRaises(Exception):
            harness.score_claim(["site-a", "site-b"], [label("site-a")], "invocation")

    def test_unknown_field_fails_closed(self) -> None:
        with self.assertRaises(harness.HarnessError):
            harness.score_claim(["site-a"], [label("site-a")], "field_reference")


if __name__ == "__main__":
    unittest.main()
