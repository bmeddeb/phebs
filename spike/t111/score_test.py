#!/usr/bin/env python3
"""Regression tests for Gate 2 v3 scoring and bundle integrity."""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from copy import deepcopy
from fractions import Fraction
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parent))

import label_prep as prep  # noqa: E402
import label_protocol as protocol  # noqa: E402
import reviewer_kits  # noqa: E402
import score  # noqa: E402


class Gate2ScoringTests(unittest.TestCase):
    def test_toolchain_attestation_precedes_hidden_key_hash(self) -> None:
        provenance = {"typed_call_oracle_toolchain_identity": "producer"}
        paths = {"key.jsonl": Path("must-not-be-hashed.jsonl")}
        with (
            patch.object(
                score,
                "attest_pre_key_toolchain",
                side_effect=score.ScoreError("wrong Go binary"),
            ),
            patch.object(score, "sha256_file") as sha256_file,
            self.assertRaisesRegex(score.ScoreError, "wrong Go binary"),
        ):
            score.validate_payloads_after_toolchain_attestation(
                paths, {"key.jsonl": "irrelevant"}, provenance
            )
        sha256_file.assert_not_called()

    def test_toolchain_attestation_precedes_hidden_key_read(self) -> None:
        identity = (
            'go_version="go version go1.26.5 darwin/arm64";'
            'go_digest=sha256:' + '1' * 64 + ';'
            'git_version="git version 2.50.1";'
            'git_digest=sha256:' + '2' * 64
        )
        provenance = {"typed_call_oracle_toolchain_identity": identity}
        paths = {"key.jsonl": Path("must-not-be-opened.jsonl")}
        with (
            patch.object(
                score,
                "resolve_oracle_toolchain",
                side_effect=prep.PrepError("wrong Go binary"),
            ),
            patch.object(score, "load_jsonl") as load_jsonl,
            self.assertRaisesRegex(score.ScoreError, "pre-key toolchain attestation"),
        ):
            score.load_hidden_key_after_toolchain_attestation(paths, provenance)
        load_jsonl.assert_not_called()

        with (
            patch.object(score, "resolve_oracle_toolchain", return_value={}),
            patch.object(
                score, "load_jsonl", return_value=[{"site_id": "site"}]
            ) as load_jsonl,
        ):
            key = score.load_hidden_key_after_toolchain_attestation(paths, provenance)
        load_jsonl.assert_called_once_with(paths["key.jsonl"])
        self.assertEqual(set(key), {"site"})

    def test_toolchain_preflight_requires_sealed_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            artifact = Path(raw)
            manifest = {
                "schema": score.SCHEMA,
                "provenance": {"typed_call_oracle_toolchain_identity": "producer"},
            }
            (artifact / "manifest.json").write_text(
                json.dumps(manifest), encoding="utf-8"
            )
            with self.assertRaisesRegex(score.ScoreError, "sealed Gate-2"):
                score.preflight_toolchain(artifact)

            manifest["gate_design_fixed"] = True
            (artifact / "manifest.json").write_text(
                json.dumps(manifest), encoding="utf-8"
            )
            with patch.object(
                score, "attest_pre_key_toolchain", return_value={"go_version": "ok"}
            ):
                self.assertEqual(
                    score.preflight_toolchain(artifact), {"go_version": "ok"}
                )

    def test_label_publication_is_verified_before_hidden_bundle_loading(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            artifact = root / "artifact"
            artifact.mkdir()
            binding = "sha256:" + "1" * 64
            manifest_document = (
                json.dumps(
                    {"randomization": {"input_binding": binding}},
                    sort_keys=True,
                    separators=(",", ":"),
                )
                + "\n"
            ).encode()
            (artifact / "manifest.json").write_bytes(manifest_document)
            assignment_bundle = reviewer_kits.build_reviewer_kits(
                {
                    "holdout": [
                        {
                            "site_id": "site",
                            "system": "fixture",
                            "path": "p.go",
                            "line": 1,
                            "column": 1,
                            "method": "Call",
                            "context": "     1| Call()",
                        }
                    ]
                },
                {"holdout": ["site"]},
                input_binding=binding,
                reviewers=("reviewer-a", "reviewer-b"),
            )
            assignment_document = assignment_bundle.manifest_document
            assignment = root / "assignment.json"
            assignment.write_bytes(assignment_document)
            labels = [self._label("site")]
            labels_document = protocol.canonical_labels_jsonl(labels, ["site"])
            labels_path = root / "labels.jsonl"
            labels_path.write_bytes(labels_document)
            commitment = protocol.build_label_commitment(
                labels,
                ["site"],
                "2026-07-14T12:00:00Z",
                input_binding=binding,
                artifact_manifest_sha256=protocol.sha256_bytes(manifest_document),
                reviewer_assignment_sha256=protocol.sha256_bytes(assignment_document),
                adjudication={
                    "overlap_sites": 0,
                    "disagreements": 0,
                    "adjudicated": 0,
                    "unresolved": 0,
                },
            )
            commitment_document = protocol.canonical_label_commitment_document(commitment)
            commitment_path = root / "commitment.json"
            commitment_path.write_bytes(commitment_document)
            api_url = "https://api.github.com/gists/abc123"
            revision = "a" * 40
            receipt = {
                "schema": protocol.GITHUB_GIST_RECEIPT_SCHEMA,
                "api_url": api_url,
                "revision": revision,
                "created_at": "2026-07-14T12:01:00Z",
                "document_sha256": protocol.sha256_bytes(commitment_document),
            }
            receipt_path = root / "receipt.json"
            receipt_path.write_text(json.dumps(receipt), encoding="utf-8")
            responses = {
                api_url: {
                    "url": api_url,
                    "created_at": receipt["created_at"],
                    "history": [
                        {"version": revision, "committed_at": receipt["created_at"]}
                    ],
                },
                api_url + "/" + revision: {
                    "files": {
                        "labels.commitment.json": {
                            "content": commitment_document.decode(),
                            "truncated": False,
                        }
                    }
                },
            }
            verified = score.verify_label_publication_before_key(
                labels_path=labels_path,
                commitment_path=commitment_path,
                receipt_path=receipt_path,
                assignment_manifest_path=assignment,
                artifact_dir=artifact,
                fetcher=responses.__getitem__,
            )
            self.assertEqual(verified, commitment)
            labels_path.write_bytes(labels_document + b"\n")
            with self.assertRaisesRegex(score.ScoreError, "frozen labels"):
                score.verify_label_publication_before_key(
                    labels_path=labels_path,
                    commitment_path=commitment_path,
                    receipt_path=receipt_path,
                    assignment_manifest_path=assignment,
                    artifact_dir=artifact,
                    fetcher=responses.__getitem__,
                )

    def test_registration_labels_require_canonical_service_identity(self) -> None:
        valid = self._label("site", registration="yes", service="pkg.Service")
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "labels.jsonl"
            path.write_text(json.dumps(valid) + "\n", encoding="utf-8")
            self.assertEqual(score.load_labels(path, {"site"})["site"], valid)
            path.write_text(
                json.dumps({**valid, "service": "ShortService"}) + "\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(score.ScoreError, "fully-qualified"):
                score.load_labels(path, {"site"})

    def test_role_cohort_requires_eligible_evidence_for_every_role(self) -> None:
        grouped: dict[str, list[tuple[dict[str, object], dict[str, object]]]] = {}
        for role in sorted(score.ALLOWED_ROLES):
            grouped[role] = [
                (
                    {
                        "families": ["client_call"],
                        "emitted_roles": [role],
                    },
                    self._label(role, invocation="yes", operation="pkg.Svc/Call", role=role),
                )
            ]
        results, passed = score.role_results(grouped)
        self.assertTrue(passed)
        self.assertTrue(all(result == (1, 1) for result in results.values()))

        missing_vendor = deepcopy(grouped)
        missing_vendor["vendor"] = [
            (
                {"families": ["client_call"], "emitted_roles": ["vendor"]},
                self._label("vendor", invocation="no", role="vendor"),
            )
        ]
        results, passed = score.role_results(missing_vendor)
        self.assertFalse(passed)
        self.assertEqual(results["vendor"], (0, 0))

        wrong_vendor = deepcopy(grouped)
        wrong_vendor["vendor"][0][0]["emitted_roles"] = ["production"]
        results, passed = score.role_results(wrong_vendor)
        self.assertFalse(passed)
        self.assertEqual(results["vendor"], (0, 1))

    def test_benchmark_counts_unique_site_family_recall_units(self) -> None:
        bundle = {
            "key": {
                "both": {
                    "frames": {
                        prep.FRAME_CALL_RECALL: {},
                        prep.FRAME_REGISTRATION_RECALL: {},
                    }
                },
                "precision-only": {"frames": {prep.FRAME_CALL_PRECISION: {}}},
            }
        }
        labels = {
            "both": self._label(
                "both", invocation="yes", operation="pkg.Svc/Call", registration="no"
            ),
            "precision-only": self._label(
                "precision-only", invocation="no", registration="no"
            ),
        }
        self.assertEqual(score.benchmark_label_counts(bundle, labels), (1, 1))

    def test_registration_precision_and_recall_are_scored_separately(self) -> None:
        bundle = {
            "manifest": {
                "frames": {
                    prep.FRAME_REGISTRATION_PRECISION: {
                        "strata": [
                            {
                                "id": "fixture|production",
                                "system": "fixture",
                                "population_size": 1,
                                "sample_population_size": 1,
                                "census_size": 0,
                            }
                        ]
                    },
                    prep.FRAME_REGISTRATION_RECALL: {
                        "strata": [
                            {
                                "id": "fixture|production",
                                "system": "fixture",
                                "population_size": 1,
                                "sample_population_size": 1,
                                "census_size": 0,
                            }
                        ]
                    },
                }
            }
        }
        label = self._label("site", registration="yes", service="pkg.Service")
        precision_rows = {
            "fixture|production": [({"object": "pkg.Service"}, label)]
        }
        precision, correct, total = score.precision_results(
            bundle,
            precision_rows,
            {},
            Fraction(1, 20),
            prep.FRAME_REGISTRATION_PRECISION,
            "registration",
        )
        self.assertEqual((correct, total), (1, 1))
        self.assertEqual(precision["TOTAL"], (1.0, Fraction(1, 1)))

        recall_rows = {
            "fixture|production": [
                ({"extracted": True, "object": "pkg.Service"}, label)
            ]
        }
        point, lower, correct, missed = score.recall_result(
            bundle,
            recall_rows,
            {},
            Fraction(1, 20),
            prep.FRAME_REGISTRATION_RECALL,
            "registration",
        )
        self.assertEqual((point, lower, correct, missed), (1.0, Fraction(1, 1), 1, 0))

        recall_rows["fixture|production"][0][0]["extracted"] = False
        point, lower, correct, missed = score.recall_result(
            bundle,
            recall_rows,
            {},
            Fraction(1, 20),
            prep.FRAME_REGISTRATION_RECALL,
            "registration",
        )
        self.assertEqual((point, lower, correct, missed), (0.0, Fraction(0, 1), 0, 1))

    def test_generated_forwarder_is_negative_but_direct_application_wiring_is_eligible(self) -> None:
        bundle = {
            "manifest": {
                "frames": {
                    prep.FRAME_REGISTRATION_RECALL: {
                        "strata": [
                            {
                                "id": "fixture|direct_register_service:production",
                                "system": "fixture",
                                "population_size": 2,
                                "sample_population_size": 2,
                                "census_size": 0,
                            }
                        ]
                    }
                }
            }
        }
        grouped = {
            "fixture|direct_register_service:production": [
                (
                    {"extracted": False, "object": None},
                    self._label("generated-forwarder", registration="no", role="generated"),
                ),
                (
                    {"extracted": False, "object": None},
                    self._label(
                        "application-wiring",
                        registration="yes",
                        service="pkg.Service",
                    ),
                ),
            ]
        }
        point, lower, correct, missed = score.recall_result(
            bundle,
            grouped,
            {},
            Fraction(1, 20),
            prep.FRAME_REGISTRATION_RECALL,
            "registration",
        )
        self.assertEqual((point, lower, correct, missed), (0.0, Fraction(0, 1), 0, 1))

    def test_exact_census_totals_combine_with_sample_bounds(self) -> None:
        stratum = {
            "id": "fixture|production",
            "system": "fixture",
            "population_size": 3,
            "sample_population_size": 2,
            "census_size": 1,
        }
        bundle = {
            "manifest": {
                "frames": {
                    prep.FRAME_CALL_PRECISION: {"strata": [stratum]},
                    prep.FRAME_CALL_RECALL: {"strata": [stratum]},
                }
            }
        }
        good = self._label("good", invocation="yes", operation="pkg.Svc/Call")
        sample_precision = {
            stratum["id"]: [({"object": "pkg.Svc/Call"}, good)] * 2
        }
        census_precision = {stratum["id"]: [({"object": "pkg.Svc/Call"}, good)]}
        precision, _, _ = score.precision_results(
            bundle,
            sample_precision,
            census_precision,
            Fraction(1, 20),
            prep.FRAME_CALL_PRECISION,
            "client_call",
        )
        self.assertEqual(precision["TOTAL"], (1.0, Fraction(1, 1)))

        sample_recall = {
            stratum["id"]: [
                ({"extracted": True, "object": "pkg.Svc/Call"}, good),
                ({"extracted": True, "object": "pkg.Svc/Call"}, good),
            ]
        }
        census_recall = {
            stratum["id"]: [
                ({"extracted": False, "object": None}, good),
            ]
        }
        point, lower, correct, missed = score.recall_result(
            bundle,
            sample_recall,
            census_recall,
            Fraction(1, 20),
            prep.FRAME_CALL_RECALL,
            "client_call",
        )
        self.assertEqual((point, lower, correct, missed), (2 / 3, Fraction(2, 3), 2, 1))

    def test_measurement_rows_rejects_unsure_only_for_relevant_family(self) -> None:
        bundle = {
            "holdout": {"site": {}},
            "key": {
                "site": {
                    "frames": {
                        prep.FRAME_CALL_RECALL: {"stratum": "fixture|risk"},
                    }
                }
            },
        }
        label = self._label(
            "site",
            invocation="yes",
            operation="pkg.Svc/Call",
            registration="unsure",
        )
        grouped = score.measurement_rows(
            bundle, {"site": label}, "holdout", prep.FRAME_CALL_RECALL
        )
        self.assertIn("fixture|risk", grouped)
        with self.assertRaisesRegex(score.ScoreError, "unsure"):
            score.measurement_rows(
                bundle,
                {"site": {**label, "invocation": "unsure", "operation": None}},
                "holdout",
                prep.FRAME_CALL_RECALL,
            )

    def test_holdout_rejects_unresolved_registration_label(self) -> None:
        bundle = {
            "key": {"site": {"frames": {}}},
            "blind_fraction": 1.0,
        }
        labels = {"site": self._label("site", registration="unsure")}
        with self.assertRaisesRegex(score.ScoreError, "unresolved"):
            score.score_holdout(bundle, labels, SimpleNamespace())

    def test_gate_configuration_rejects_active_burns_and_legacy_randomness(self) -> None:
        binding = "sha256:" + "a" * 64
        power = {"attainable": True}
        manifest = {
            "systems": list(prep.SYSTEMS),
            "gate_design_fixed": True,
            "randomization": {
                "mode": "audited-unpredictable-sha256-rank-v2",
                "input_binding": binding,
                "base_input_binding": "sha256:" + "b" * 64,
                "input_committed_at": "2026-07-13T12:00:30Z",
                "seed_record": {"input_binding": binding},
            },
            "attempt_claim": {
                "input_binding": binding,
                "base_input_binding": "sha256:" + "b" * 64,
                "committed_at": "2026-07-13T12:00:30Z",
            },
            "sampling_config": prep.gate_sampling_config(),
            "decision_rule": prep.gate_decision_rule(),
            "burn_ledger": {
                "coordinate_count": 700,
                "active_coordinate_count": 700,
                "carry_forward_schema": "t111-burn-carry-forward-census-v2",
            },
            "holdout": {"unique_census_sites": 1},
            "protocol_coverage": {
                "client_calls": "measured",
                "registration": "measured",
                "registration_predicate": "IMPLEMENTS_SERVICE",
                "role_classification": "emitted_reference_sample_with_source_roles",
                "heuristic_call_facts": "quarantined_as_abstention_debt",
                "disclosed_source_sites": "exact_census_combined_with_blind_probability_bounds",
            },
            "power_analysis": power,
        }
        bundle = {"manifest": manifest, "census": {}, "dev": {}, "holdout": {}, "key": {}}
        args = SimpleNamespace(
            confidence=0.95,
            precision_threshold=0.98,
            recall_threshold=0.90,
            fixture_precision_threshold=0.90,
        )
        with patch.object(score, "preflight_gate_design", return_value=power):
            self.assertEqual(score.gate_configuration_reasons(bundle, args), [])
            missing_census = deepcopy(bundle)
            missing_census["manifest"]["holdout"]["unique_census_sites"] = 0
            reasons = score.gate_configuration_reasons(missing_census, args)
            self.assertTrue(any("census stratum" in reason for reason in reasons))

            legacy = deepcopy(bundle)
            legacy["manifest"]["randomization"] = {"seed": 111}
            reasons = score.gate_configuration_reasons(legacy, args)
            self.assertTrue(any("unpredictable seed" in reason for reason in reasons))

    def test_sealed_artifact_set_rejects_extra_and_symlink_entries(self) -> None:
        names = {
            "sites.census.jsonl",
            "sites.dev.jsonl",
            "sites.holdout.jsonl",
            "key.jsonl",
            "seed.json",
            "attempt.json",
        }
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw) / "bundle"
            root.mkdir()
            (root / "manifest.json").write_text("{}\n", encoding="utf-8")
            paths = {}
            for name in names:
                path = root / name
                path.write_text("\n", encoding="utf-8")
                paths[name] = path
            score.validate_sealed_artifact_set(root, paths)

            (root / "extra").write_text("extra\n", encoding="utf-8")
            with self.assertRaisesRegex(score.ScoreError, "missing or extra"):
                score.validate_sealed_artifact_set(root, paths)
            (root / "extra").unlink()

            target = Path(raw) / "outside"
            target.write_text("outside\n", encoding="utf-8")
            paths["seed.json"].unlink()
            paths["seed.json"].symlink_to(target)
            with self.assertRaisesRegex(score.ScoreError, "non-regular"):
                score.validate_sealed_artifact_set(root, paths)

    @staticmethod
    def _label(
        site_id: str,
        *,
        invocation: str = "no",
        operation: str | None = None,
        registration: str = "no",
        service: str | None = None,
        role: str = "production",
    ) -> dict[str, object]:
        return {
            "site_id": site_id,
            "invocation": invocation,
            "operation": operation,
            "registration": registration,
            "service": service,
            "expected_code_role": role,
            "rationale": "reviewed",
            "evidence": "source context",
        }


if __name__ == "__main__":
    unittest.main()
