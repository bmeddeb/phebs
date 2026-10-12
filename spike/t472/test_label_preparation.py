"""Retained preparation binds real outcomes without manufacturing missing inputs."""
import copy
import unittest
from pathlib import Path

import binding_bundle
import source_outcomes

ROOT = Path(__file__).parent


class LabelPreparationTests(unittest.TestCase):
    def test_round_binding_preserves_blindness_and_remaining_gaps(self):
        template = source_outcomes.load(ROOT / "bundle/preregistration.bundle.json")
        round_bundle = source_outcomes.load(ROOT / "bundle/preregistration.run-g.bundle.json")
        run = source_outcomes.load(ROOT / "candidate_run_processing_20261011g.json")
        self.assertEqual(len(binding_bundle.validate_bundle(template)), 25)
        self.assertEqual(len(binding_bundle.validate_bundle(round_bundle)), 24)
        key = "phebs eligible-unit outcome receipt for the processing-state ledger"
        self.assertEqual(round_bundle["candidate_identities"][key], {
            "identity": "t472-source-outcome-reconciliation-v1:" + run["run_root"],
            "sha256": run["artifact_sha256"]["source-outcomes.json"],
        })
        expected = copy.deepcopy(template)
        expected["candidate_identities"][key] = round_bundle["candidate_identities"][key]
        # The retained round bundle binds the builder bytes current when it was made (pre-T47.2p).
        expected["machinery"]["binding_bundle"] = round_bundle["machinery"]["binding_bundle"]
        expected.pop("missing_bindings")
        expected["missing_bindings"] = binding_bundle.validate_bundle(expected)
        self.assertEqual(round_bundle, expected)

    def test_confirmations_and_packet_manifest_bind_unchanged_source_frames(self):
        judgments = source_outcomes.load(ROOT / "source_frame_judgments_20261011.json")
        manifest = source_outcomes.load(ROOT / "artifact_bindings_20261011g.json")
        self.assertEqual(judgments["frames_plan_sha256"], source_outcomes.sha(ROOT / "frames/frames.plan.json"))
        self.assertEqual(judgments["universe_sha256"], source_outcomes.sha(ROOT / "universe/universe.json"))
        self.assertEqual(manifest["source_frame_judgments_sha256"], source_outcomes.sha(ROOT / "source_frame_judgments_20261011.json"))
        self.assertEqual(manifest["prepared_files_sha256"]["candidate/preregistration.run-g.bundle.json"],
                         source_outcomes.sha(ROOT / "bundle/preregistration.run-g.bundle.json"))
        self.assertEqual({row["id"]: row["status"] for row in judgments["judgments"]},
                         {"recall_scope": "confirmed", "declaration_census_coordinates": "confirmed"})
        for key in ("sample_drawn", "labels_produced", "predictions_disclosed", "quality_scored",
                    "protocol_sealed", "external_publication"):
            self.assertIs(manifest[key], False)
        self.assertEqual(manifest["source_census_reference_units"], 74)
        self.assertEqual(manifest["source_census_reference_files"], 60)
        self.assertEqual(manifest["proof_bundles_retained"], 0)


if __name__ == "__main__":
    unittest.main()
