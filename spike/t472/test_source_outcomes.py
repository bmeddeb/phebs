"""Synthetic receipts exercise reconciliation; no candidate predictions or scores."""
import copy
import json
import tempfile
import unittest
from pathlib import Path

import caller_scoring
import source_outcomes

ROOT = Path(__file__).parent


class SourceOutcomeTests(unittest.TestCase):
    def setUp(self):
        self.universe = source_outcomes.load(ROOT / "universe/universe.json")
        self.receipt = {"schema": "t472-convergence-v1", "result": "converged", "controls": []}
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for index, repo in enumerate(self.universe["repositories"]):
            digest = "sha256:" + f"{index+1:064x}"
            result = {"digest": digest, "partition_ordinal": 1, "disposition": "success"}
            control = {"repository": "local/fixture/" + repo["repo"], "census_repository": repo["repo"],
                       "commit": repo["derived_commit"], "domain": "grpc-caller", "run_id": "run:" + str(index),
                       "plan": {"digest": digest, "extractor_version": "1.6.0"},
                       "root": {"disposition": "success", "plan_digest": digest, "results": [result]}}
            self.receipt["controls"].append(control)
            rows = [{"schema": "t472-source-outcomes-v1", "repository": control["repository"],
                     "commit": control["commit"], "run_id": control["run_id"], "plan_digest": digest, "partition_ordinal": 1}]
            rows += [{"path": path, "state": "analyzed", "reason": "source_scan_complete",
                      "blob_digest": "sha256:" + "a"*64} for path in repo["eligible_units"]["regular_go_source_paths"]]
            rows.append({"result": result})
            self.write(index, rows)

    def file(self, index):
        return self.root / (f"{index+1:064x}" + ".jsonl")

    def rows(self, index):
        return [json.loads(line) for line in self.file(index).read_text().splitlines()]

    def write(self, index, rows):
        self.file(index).write_text("\n".join(json.dumps(row) for row in rows) + "\n")

    def test_complete_fixed_census_and_explicit_nonregular_treatment(self):
        joined = source_outcomes.reconcile(self.receipt, self.root, self.universe)
        self.assertEqual(joined["ledger"]["total"], 7685)
        self.assertEqual(sum(v["analyzed"] for v in joined["ledger"]["by_stratum"].values()), 7685)
        self.assertEqual(joined["nonregular_go_paths"]["count"], 11)
        self.assertFalse(joined["acceptance_met"])

    def test_missing_duplicate_unknown_state_reason_and_stale_controls(self):
        mutations = {
            "missing": lambda rows: rows.pop(1),
            "duplicate": lambda rows: rows.insert(1, copy.deepcopy(rows[1])),
            "unknown": lambda rows: rows[1].update(path="not-in-census.go"),
            "invented": lambda rows: rows[1].update(state="done"),
            "reason": lambda rows: rows[1].update(reason="read_complete"),
            "digest": lambda rows: rows[1].update(blob_digest="not-a-digest"),
            "commit": lambda rows: rows[0].update(commit="f"*40),
            "run": lambda rows: rows[0].update(run_id="another-run"),
            "result": lambda rows: rows[-1]["result"].update(disposition="terminal_refusal"),
            "footer": lambda rows: rows.pop(),
        }
        original = self.rows(0)
        for name, mutation in mutations.items():
            with self.subTest(name=name):
                rows = copy.deepcopy(original)
                mutation(rows)
                self.write(0, rows)
                with self.assertRaises((ValueError, caller_scoring.ScoringError)):
                    source_outcomes.reconcile(self.receipt, self.root, self.universe)
        self.write(0, original)
        self.file(0).unlink()
        with self.assertRaisesRegex(ValueError, "lacks source outcome"):
            source_outcomes.reconcile(self.receipt, self.root, self.universe)

    def test_reconciler_is_bound_before_any_real_processing_round(self):
        import binding_bundle
        bundle = source_outcomes.load(ROOT / "bundle/preregistration.bundle.json")
        self.assertEqual(bundle["machinery"]["source_outcomes"]["sha256"], source_outcomes.sha(ROOT / "source_outcomes.py"))
        self.assertEqual(len(binding_bundle.validate_bundle(bundle)), 25)
        self.assertFalse(bundle["quality_scored"])

    def test_retained_run_e_has_convergence_but_no_processing_receipt(self):
        receipt = source_outcomes.load(ROOT / "candidate_run_convergence_20261011e.json")
        self.assertEqual(receipt["queues"]["caller_leaf_job"], {"done": 18})
        self.assertEqual(receipt["current_domain_roots"], 12)
        self.assertEqual(receipt["processing_outcomes"]["missing_regular_source_rows"], 7685)
        self.assertFalse(receipt["acceptance_met"])
        with tempfile.TemporaryDirectory() as empty:
            with self.assertRaisesRegex(ValueError, "lacks source outcome"):
                source_outcomes.reconcile(receipt, Path(empty), self.universe)


if __name__ == "__main__":
    unittest.main()
