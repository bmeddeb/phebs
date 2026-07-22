"""Committed synthetic tests for the Stage-2 preparation glue. No real
ceremony inputs; a synthetic git fixture and invented cardinalities only."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import stage2_prepare as s2  # noqa: E402


def _git(repo, *args):
    env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
               GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t",
               GIT_AUTHOR_DATE="2026-01-01T00:00:00Z",
               GIT_COMMITTER_DATE="2026-01-01T00:00:00Z")
    subprocess.run(["git", "-C", repo, *args], check=True, env=env,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


class Stage2Test(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        repo = self.dir / "repo"
        repo.mkdir()
        self.repo = repo
        _git(repo, "init", "-q")
        (repo / "kept.go").write_text("func Call() { rpc() }\n")
        (repo / "gone.go").write_text("func Gone() {}\n")
        _git(repo, "add", "-A"); _git(repo, "commit", "-qm", "old")
        self.old = subprocess.run(["git", "-C", repo, "rev-parse", "HEAD"],
                                  check=True, stdout=subprocess.PIPE).stdout.decode().strip()
        (repo / "gone.go").unlink()
        _git(repo, "add", "-A"); _git(repo, "commit", "-qm", "new")
        self.new = subprocess.run(["git", "-C", repo, "rev-parse", "HEAD"],
                                  check=True, stdout=subprocess.PIPE).stdout.decode().strip()

        self._w("ledger.json", {"cohorts": [{"cohort_id": "syn", "coordinates": [
            {"system": "temporal", "path": "kept.go", "start_line": 1, "end_line": 1},
            {"system": "temporal", "path": "gone.go", "start_line": 1, "end_line": 1},
            {"system": "dapr", "path": "missing.go", "start_line": 1, "end_line": 1}]}]})
        self._w("heads.json", {
            fixture: {"old_commit": self.old, "new_commit": self.new,
                      "repo_dir": str(repo)}
            for fixture in s2.SEALED_FIXTURES
        })
        membership = {
            frame: self._frame_membership(frame)
            for frame in s2.AGGREGATE_FRAMES
        }
        self._w("cards.json", {
            frame: {"population": len(membership[frame]), "census": 0}
            for frame in s2.AGGREGATE_FRAMES
        })
        self._w("design.json", {
            "client_call_precision": {"p": "995/1000", "threshold": "98/100"},
            "client_call_recall": {"p": "95/100", "threshold": "90/100"},
            "registration_precision": {"p": "995/1000", "threshold": "98/100"},
            "registration_recall": {"p": "95/100", "threshold": "90/100"},
            "per_fixture_precision": {"p": "97/100", "threshold": "90/100"},
        })
        # kept.go is a member of ONLY client-call precision; missing.go is in
        # no frame and must count against every aggregate frame and against
        # both Dapr precision subframes (burn on doubt).
        self._w("membership.json", membership)

    def tearDown(self):
        self.tmp.cleanup()

    def _w(self, name, obj):
        (self.dir / name).write_text(json.dumps(obj))

    def _frame_membership(self, frame, per_fixture=25):
        keys = []
        for fixture in s2.SEALED_FIXTURES:
            for number in range(1, per_fixture + 1):
                path = f"{frame}/site-{number}.go"
                if frame == "client_call_precision" and fixture == "temporal" and number == 1:
                    path = "kept.go"
                keys.append(f"{fixture}:{path}:{number}:{number}")
        return sorted(keys)

    def _run(self, *extra, out="out"):
        cmd = ["/usr/bin/python3", str(HERE / "stage2_prepare.py"),
               "--ledger", str(self.dir / "ledger.json"),
               "--heads", str(self.dir / "heads.json"),
               "--cardinalities", str(self.dir / "cards.json"),
               "--frame-membership", str(self.dir / "membership.json"),
               "--design", str(self.dir / "design.json"),
               "--out", str(self.dir / out), *extra]
        return subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def _sealed_receipt(self):
        return {
            "schema": "t111-gate2-v2-stage1-receipt-v1",
            "status": "ADMITTED",
            "cutoff": "2026-07-20T16:00:00Z",
            "query_sha256": "sha256:8e9f76872c955e0bad76dfde432e846fbc7c340dfd23bba7a67fda14a55d897b",
            "response_sha256": "sha256:synthetic",
            "heads": {
                "temporal": {"head_oid": self.new},
                "dapr": {"head_oid": self.new},
                "loki": {"head_oid": self.new},
                "online-boutique": {"head_oid": self.new},
            },
        }

    def _sealed_heads(self):
        return {fixture: {"old_commit": s2.SEALED_OLD_COMMITS[fixture],
                          "new_commit": self.new,
                          "repo_dir": str(self.dir / "repo")}
                for fixture in self._sealed_receipt()["heads"]}

    def _history_heads(self, repo=None, old=None, new=None):
        repo = self.repo if repo is None else Path(repo)
        old = self.old if old is None else old
        new = self.new if new is None else new
        return {
            fixture: {"old_commit": old, "new_commit": new, "repo_dir": str(repo)}
            for fixture in s2.SEALED_FIXTURES
        }

    def test_synthetic_end_to_end_aggregate_and_fixture_power(self):
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 0, r.stderr)
        result = json.loads((self.dir / "out" / "stage2-preparation.json").read_text())
        self.assertEqual(result["schema"], "t111-gate2-v2-stage2-preparation-v3")
        # 2 burns total: kept.go (identical) + missing.go (Git refusal).
        self.assertEqual(result["burned"], 2)
        self.assertEqual(result["freed"], 1)
        self.assertEqual(result["burns_unmapped_to_any_frame"], 1)
        self.assertEqual(result["burns_by_frame"], {
            "client_call_precision": 2,
            "client_call_recall": 1,
            "registration_precision": 1,
            "registration_recall": 1,
        })
        self.assertEqual(result["power"]["client_call_precision"]["population_net"], 98)
        self.assertEqual(result["power"]["client_call_recall"]["population_net"], 99)
        fixture_power = result["per_fixture_power"]["client_call_precision"]
        self.assertEqual(fixture_power["temporal"]["population_net"], 24)
        self.assertEqual(fixture_power["dapr"]["population_net"], 24)
        self.assertEqual(fixture_power["loki"]["population_net"], 25)
        self.assertEqual(fixture_power["online-boutique"]["population_net"], 25)
        self.assertTrue(result["all_frames_feasible"])
        self.assertEqual(list((self.dir / "out").glob(".*.tmp")), [])

    def test_refuses_fixture_population_sum_mismatch(self):
        cards = json.loads((self.dir / "cards.json").read_text())
        cards["client_call_precision"]["population"] += 1
        self._w("cards.json", cards)
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"derived fixture population sum", r.stderr)
        self.assertFalse((self.dir / "out").exists())

    def test_infeasible_fixture_fails_combined_power_gate(self):
        membership = json.loads((self.dir / "membership.json").read_text())
        loki = [
            key for key in membership["client_call_precision"]
            if key.startswith("loki:")
        ][:9]
        membership["client_call_precision"] = sorted([
            key for key in membership["client_call_precision"]
            if not key.startswith("loki:")
        ] + loki)
        cards = json.loads((self.dir / "cards.json").read_text())
        cards["client_call_precision"]["population"] = len(
            membership["client_call_precision"]
        )
        self._w("membership.json", membership)
        self._w("cards.json", cards)
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 1, r.stderr)
        result = json.loads((self.dir / "out" / "stage2-preparation.json").read_text())
        self.assertTrue(all(value["feasible"] for value in result["power"].values()))
        self.assertFalse(
            result["per_fixture_power"]["client_call_precision"]["loki"]["feasible"]
        )
        self.assertFalse(result["all_frames_feasible"])

    def test_infeasible_registration_fixture_fails_combined_power_gate(self):
        membership = json.loads((self.dir / "membership.json").read_text())
        loki = [
            key for key in membership["registration_precision"]
            if key.startswith("loki:")
        ][:9]
        membership["registration_precision"] = sorted([
            key for key in membership["registration_precision"]
            if not key.startswith("loki:")
        ] + loki)
        cards = json.loads((self.dir / "cards.json").read_text())
        cards["registration_precision"]["population"] = len(
            membership["registration_precision"]
        )
        self._w("membership.json", membership)
        self._w("cards.json", cards)
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 1, r.stderr)
        result = json.loads((self.dir / "out" / "stage2-preparation.json").read_text())
        self.assertTrue(all(value["feasible"] for value in result["power"].values()))
        self.assertFalse(
            result["per_fixture_power"]["registration_precision"]["loki"]["feasible"]
        )
        self.assertFalse(result["all_frames_feasible"])

    def test_refuses_unconsumed_design_metadata(self):
        design = json.loads((self.dir / "design.json").read_text())
        design["not_consumed"] = {"p": "1/1", "threshold": "0/1"}
        self._w("design.json", design)
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"exact consumed power fields", r.stderr)
        self.assertFalse((self.dir / "out").exists())

    def test_refuses_nested_or_missing_design_metadata(self):
        base = json.loads((self.dir / "design.json").read_text())
        nested = json.loads(json.dumps(base))
        nested["per_fixture_precision"]["ignored"] = True
        missing = json.loads(json.dumps(base))
        del missing["per_fixture_precision"]
        for number, design in enumerate((nested, missing), 1):
            with self.subTest(number=number):
                self._w("design.json", design)
                r = self._run("--synthetic", out=f"design-out-{number}")
                self.assertEqual(r.returncode, 2)
                self.assertFalse((self.dir / f"design-out-{number}").exists())
        self._w("design.json", base)

    def test_refuses_without_receipt_outside_synthetic(self):
        r = self._run()
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"no Stage-1 receipt", r.stderr)

    def test_refuses_receipt_not_bound_to_sealed_inputs(self):
        bad = {"schema": "t111-gate2-v2-stage1-receipt-v1", "status": "ADMITTED",
               "cutoff": "1999-01-01T00:00:00Z", "query_sha256": "sha256:wrong",
               "response_sha256": "sha256:x", "heads": {"a": {}}}
        self._w("receipt.json", bad)
        r = self._run("--receipt", str(self.dir / "receipt.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"cutoff", r.stderr)

    def test_refuses_existing_output_directory(self):
        (self.dir / "out").mkdir()
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 3)

    def test_refuses_new_commit_not_matching_admitted_head(self):
        receipt = self._sealed_receipt()
        self._w("receipt.json", receipt)
        heads = self._sealed_heads()
        heads["temporal"]["new_commit"] = self.old
        self._w("heads.json", heads)
        r = self._run("--receipt", str(self.dir / "receipt.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"new_commit does not match", r.stderr)
        self.assertFalse((self.dir / "out").exists())

    def test_refuses_old_commit_not_matching_sealed_carry_forward_rule(self):
        self._w("receipt.json", self._sealed_receipt())
        heads = self._sealed_heads()
        heads["temporal"]["old_commit"] = "0" * 40
        self._w("heads.json", heads)
        r = self._run("--receipt", str(self.dir / "receipt.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"old_commit does not match the sealed carry-forward rule", r.stderr)
        self.assertFalse((self.dir / "out").exists())

    def test_source_history_accepts_nonshallow_ancestor_and_refuses_reverse(self):
        expected = {fixture: str(self.repo) for fixture in s2.SEALED_FIXTURES}
        with mock.patch.dict(s2.SEALED_REPO_DIRS, expected, clear=True):
            s2.verify_source_history(self._history_heads())
            with self.assertRaisesRegex(s2.Stage2Error, "not an ancestor"):
                s2.verify_source_history(self._history_heads(old=self.new, new=self.old))

    def test_source_history_refuses_shallow_repository(self):
        shallow = self.dir / "shallow"
        subprocess.run(
            ["/usr/bin/git", "clone", "-q", "--depth", "1",
             f"file://{self.repo}", str(shallow)],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        expected = {fixture: str(shallow) for fixture in s2.SEALED_FIXTURES}
        with mock.patch.dict(s2.SEALED_REPO_DIRS, expected, clear=True):
            with self.assertRaisesRegex(s2.Stage2Error, "shallow or unreadable"):
                s2.verify_source_history(self._history_heads(repo=shallow))

    def test_git_execution_context_refuses_path_or_environment_override(self):
        s2.verify_git_execution_context()
        with mock.patch.object(s2.shutil, "which", return_value="/tmp/fake/git"):
            with self.assertRaisesRegex(s2.Stage2Error, "Git on PATH"):
                s2.verify_git_execution_context()
        with mock.patch.dict(os.environ, {"GIT_DIR": str(self.repo)}):
            with self.assertRaisesRegex(s2.Stage2Error, "unsealed override"):
                s2.verify_git_execution_context()

    def test_prior_lineage_and_ledger_digest_refusals(self):
        tampered = self.dir / "expansion-lineage.json"
        tampered.write_bytes(s2.PRIOR_LINEAGE_PATH.read_bytes() + b" ")
        with mock.patch.object(s2, "PRIOR_LINEAGE_PATH", tampered):
            with self.assertRaisesRegex(s2.Stage2Error, "receipt digest"):
                s2.verify_prior_lineage()
        lineage = s2.verify_prior_lineage()
        with self.assertRaisesRegex(s2.Stage2Error, "burn ledger digest"):
            s2.verify_sealed_ledger(str(self.dir / "ledger.json"), lineage)

    def test_refuses_heads_fixture_set_not_matching_admitted_receipt(self):
        self._w("receipt.json", self._sealed_receipt())
        heads = self._sealed_heads()
        heads["extra"] = {"old_commit": self.old, "new_commit": self.new,
                          "repo_dir": str(self.dir / "repo")}
        self._w("heads.json", heads)
        r = self._run("--receipt", str(self.dir / "receipt.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"fixtures do not exactly match", r.stderr)
        self.assertFalse((self.dir / "out").exists())

    def test_refuses_missing_fixture_from_admitted_receipt(self):
        self._w("receipt.json", self._sealed_receipt())
        heads = self._sealed_heads()
        del heads["loki"]
        self._w("heads.json", heads)
        r = self._run("--receipt", str(self.dir / "receipt.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn(b"fixtures do not exactly match", r.stderr)
        self.assertFalse((self.dir / "out").exists())

    def test_unexpected_error_has_distinct_exit_and_no_output(self):
        (self.dir / "cards.json").write_text("{")
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 4)
        self.assertEqual(r.stderr, b"stage2: unexpected failure: JSONDecodeError\n")
        self.assertFalse((self.dir / "out").exists())

    def test_output_pair_is_not_published_if_directory_rename_fails(self):
        out = self.dir / "out"
        staging = out.with_name(f".{out.name}.tmp")
        real_rename = os.rename

        def fail_final_rename(src, dst):
            if Path(src) == staging and Path(dst) == out:
                raise OSError("injected final rename failure")
            return real_rename(src, dst)

        result = {"schema": "synthetic", "all_frames_feasible": True}
        with mock.patch.object(s2.os, "rename", side_effect=fail_final_rename):
            with self.assertRaises(OSError):
                s2.publish_outputs(out, [], result)
        self.assertFalse(out.exists())
        self.assertTrue((staging / "census-v2-seed.jsonl").is_file())
        self.assertTrue((staging / "stage2-preparation.json").is_file())


if __name__ == "__main__":
    unittest.main()
