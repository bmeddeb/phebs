#!/usr/bin/env python3
"""Deterministic regression tests for Gate 3/4 design-based scoring."""

from __future__ import annotations

import json
import sys
import unittest
from copy import deepcopy
from fractions import Fraction
from pathlib import Path
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parent))

import score_g34 as scorer  # noqa: E402
from gate34_common import ValidationError, digest_value, split_sites  # noqa: E402
from score_g34 import score_gate3, score_gate4_recall, stratified_ratio_bound  # noqa: E402


class Gate34ScoringTests(unittest.TestCase):
    def test_candidate_population_is_rebuilt_not_trusted_from_artifact(self) -> None:
        candidate = {
            "schema_version": "t111-benchmark-v3",
            "site_id": "candidate",
            "gate": 4,
            "cohort": "candidate_recall",
            "identity": {"gate": 4},
            "system": "fixture",
            "path": "consumer.go",
            "start_byte": 1,
            "end_byte": 2,
            "start_line": 1,
            "end_line": 1,
            "shape": "getter_call",
            "token": "GetValue",
            "split_stratum": "g4c:fixture:getter_call:open",
            "population_stratum": "fixture:getter_call:open",
            "population_size": 100,
            "sample_size_total": 1,
        }
        dev, holdout = split_sites([deepcopy(candidate)], seed=111, holdout_fraction=0.30)
        manifest = {
            "generator": {
                "config": {
                    "seed": 111,
                    "holdout_fraction": 0.30,
                    "candidate_hit_quota": 35,
                    "candidate_open_quota": 8,
                }
            }
        }
        generated = ([deepcopy(candidate)], [], {"fixture:getter_call:open": 100}, 0)
        with (
            patch.object(scorer, "generated_field_vocabulary", return_value={"Value"}),
            patch.object(scorer, "gate4_candidate_sites", return_value=generated),
        ):
            design = scorer.rebuilt_candidate_population_design(
                Path("/corpus"), {}, [], manifest, dev, holdout
            )
            self.assertEqual(design, {"g4c:fixture:getter_call:open": 100})

            tampered_dev, tampered_holdout = deepcopy(dev), deepcopy(holdout)
            target = (tampered_dev or tampered_holdout)[0]
            target["population_size"] = 1
            unsigned = dict(target)
            unsigned.pop("record_digest")
            target["record_digest"] = digest_value(unsigned)
            with self.assertRaisesRegex(ValidationError, "re-enumeration"):
                scorer.rebuilt_candidate_population_design(
                    Path("/corpus"), {}, [], manifest, tampered_dev, tampered_holdout
                )

    def test_precision_point_uses_population_weights(self) -> None:
        sites = [
            {
                "site_id": "large",
                "gate": 3,
                "system": "large",
                "predicate": "MERGE",
                "_correct": True,
            },
            {
                "site_id": "small",
                "gate": 3,
                "system": "small",
                "predicate": "MERGE",
                "_correct": False,
            },
        ]
        result = stratified_ratio_bound(
            sites,
            {"g3:large:MERGE": 1000, "g3:small:MERGE": 10},
            domain=lambda _: True,
            success=lambda row: row["_correct"],
            alpha_each=Fraction(1, 40),
            population_is_domain=True,
        )
        self.assertAlmostEqual(result["point"], 1000 / 1010)
        self.assertNotEqual(result["point"], 0.5)

    def test_confidence_lower_bound_not_point_estimate_governs(self) -> None:
        ordinary = [
            {
                "site_id": f"site-{index}",
                "gate": 3,
                "system": "fixture",
                "predicate": "MERGE",
                "identity": {"index": index},
                "_correct": True,
            }
            for index in range(50)
        ]
        pairs = [
            {
                "site_id": f"pair-{index}",
                "gate": 3,
                "system": "fixture",
                "predicate": "DEPLOYABLE_REACHES_OPERATION",
                "identity": {"pair": index},
                "_correct": True,
            }
            for index in range(30)
        ]
        result = stratified_ratio_bound(
            ordinary,
            {"g3:fixture:MERGE": 1000},
            domain=lambda _: True,
            success=lambda row: row["_correct"],
            alpha_each=Fraction(1, 20),
            population_is_domain=True,
        )
        self.assertEqual(result["point"], 1.0)
        self.assertLess(result["lower"], 0.99)

        sites = [{key: value for key, value in site.items() if key != "_correct"} for site in ordinary + pairs]
        labels = {site["site_id"]: {"verdict": "correct"} for site in sites}
        current = {"fixture": {
            json.dumps(site["identity"], sort_keys=True, separators=(",", ":"))
            for site in sites
        }}
        scored = score_gate3(
            sites,
            labels,
            current,
            {"capabilities": {"gate3_pairwise_measurement": True}},
            {
                "g3:fixture:MERGE": 1000,
                "g3:fixture:DEPLOYABLE_REACHES_OPERATION": 30,
            },
            Fraction(1, 40),
        )
        self.assertEqual(scored["precision"], 1.0)
        self.assertLess(scored["precision_95pct_lower"], 0.99)
        self.assertFalse(scored["passed"])
        self.assertEqual(scored["status"], "FAIL")

    def test_recall_cannot_combine_access_and_role_from_different_facts(self) -> None:
        site = {
            "site_id": "candidate",
            "gate": 4,
            "cohort": "candidate_recall",
            "system": "online-boutique",
            "path": "consumer.go",
            "start_byte": 10,
            "end_byte": 15,
            "population_stratum": "online-boutique:getter_call:hit",
        }
        label = {
            "eligible": "yes",
            "expected_access": "getter_read",
            "expected_code_role": "production",
            "expected_contract_lineage_id": "lineage",
            "expected_message_full_name": "example.Message",
            "expected_field_number": "1",
        }
        canonical = {
            "contract_lineage_id": "lineage",
            "message_full_name": "example.Message",
            "field_number": 1,
            "field_name": "value",
        }
        facts = [
            {
                "system": "online-boutique",
                "path": "consumer.go",
                "start_byte": 9,
                "end_byte": 16,
                "detail": "access=getter_read",
                "code_role": "test",
                "canonical_field": canonical,
            },
            {
                "system": "online-boutique",
                "path": "consumer.go",
                "start_byte": 9,
                "end_byte": 16,
                "detail": "access=field_write",
                "code_role": "production",
                "canonical_field": canonical,
            },
        ]
        result = score_gate4_recall(
            [site],
            {"candidate": label},
            {("online-boutique", "consumer.go"): facts},
            {"capabilities": {"canonical_field_identity": True}},
            {"g4c:online-boutique:getter_call:hit": 1},
            Fraction(1, 20),
        )
        self.assertEqual(result["access_accuracy"], 1.0)
        self.assertEqual(result["role_accuracy"], 1.0)
        self.assertEqual(result["direct_successes"], 0)
        self.assertEqual(result["direct_recall"], 0.0)


if __name__ == "__main__":
    unittest.main()
