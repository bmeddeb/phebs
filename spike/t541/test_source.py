"""Source-only oracle controls; no product imports, children or sockets."""
import copy
import json
from pathlib import Path
import unittest

import check_source


HERE = Path(__file__).resolve().parent


class SourceOracleTests(unittest.TestCase):
    def setUp(self):
        self.binding = json.loads((HERE / "input-binding.json").read_text())
        self.oracle = json.loads((HERE / "oracle.json").read_text())
        self.root = Path(self.binding["evaluation"]["local_source_root"])

    def test_all_exact_public_input_coordinates_and_denominators(self):
        result = check_source.verify(self.binding, self.oracle, self.root)
        self.assertEqual(result["query_memberships"], {"positive": 9, "negative": 36, "filtered_empty_queries": 2})
        self.assertEqual(result["report_predictions_read"], 0)

    def test_malformed_source_span_and_negative_membership_refuse(self):
        for mutate in (
            lambda o: o["sites"][0].update(end_byte=o["sites"][0]["end_byte"] + 1),
            lambda o: o["queries"][0]["excluded_site_ids"].pop(),
            lambda o: o["declarations"][0]["endpoint"].update(declaration_lineage="foreign"),
        ):
            changed = copy.deepcopy(self.oracle)
            mutate(changed)
            with self.assertRaises(ValueError):
                check_source.verify(self.binding, changed, self.root)

    def test_bundle_and_license_are_exact_reusable_inputs(self):
        self.assertEqual(check_source.digest(HERE / "base.bundle"), self.binding["evaluation"]["bundle"]["sha256"])
        self.assertEqual(self.binding["license"]["spdx"], "Apache-2.0")
        self.assertFalse(self.binding["reuse"]["quality_transfer"])

    def test_checkpoint_binds_all_preparation_files(self):
        checkpoint = json.loads((HERE / "checkpoint.json").read_text())
        for row in checkpoint["files"]:
            self.assertEqual(check_source.digest(HERE / row["path"]), row["sha256"])


if __name__ == "__main__":
    unittest.main()
