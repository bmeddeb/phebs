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
            {"system": "alpha", "path": "kept.go", "start_line": 1, "end_line": 1},
            {"system": "alpha", "path": "gone.go", "start_line": 1, "end_line": 1},
            {"system": "beta", "path": "x.go", "start_line": 1, "end_line": 1}]}]})
        self._w("heads.json", {"alpha": {"old_commit": self.old,
                "new_commit": self.new, "repo_dir": str(repo)}})
        self._w("cards.json", {
            "precision": {"population": 1000, "census": 400},
            "recall": {"population": 1100, "census": 450}})
        self._w("design.json", {
            "precision": {"p": "995/1000", "threshold": "98/100"},
            "recall": {"p": "95/100", "threshold": "90/100"}})
        # kept.go burn is a member of ONLY the precision frame; the beta burn
        # is in no frame and must count against all frames (burn on doubt).
        self._w("membership.json", {
            "precision": [f"alpha:kept.go:1:1"], "recall": []})

    def tearDown(self):
        self.tmp.cleanup()

    def _w(self, name, obj):
        (self.dir / name).write_text(json.dumps(obj))

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
        return {fixture: {"old_commit": self.old, "new_commit": self.new,
                          "repo_dir": str(self.dir / "repo")}
                for fixture in self._sealed_receipt()["heads"]}

    def test_synthetic_end_to_end_frame_specific_burns(self):
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 0, r.stderr)
        result = json.loads((self.dir / "out" / "stage2-preparation.json").read_text())
        # 2 burns total: kept.go (identical) + beta (system-not-in-snapshot).
        self.assertEqual(result["burned"], 2)
        self.assertEqual(result["freed"], 1)
        self.assertEqual(result["burns_unmapped_to_any_frame"], 1)
        # precision gets both burns (member + unmapped); recall only unmapped.
        self.assertEqual(result["burns_by_frame"], {"precision": 2, "recall": 1})
        self.assertEqual(result["power"]["precision"]["population_net"], 998)
        self.assertEqual(result["power"]["recall"]["population_net"], 1099)
        self.assertEqual(list((self.dir / "out").glob(".*.tmp")), [])

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
        self._w("cards.json", {
            "precision": {"population": "not-an-int", "census": 400},
            "recall": {"population": 1100, "census": 450}})
        r = self._run("--synthetic")
        self.assertEqual(r.returncode, 4)
        self.assertEqual(r.stderr, b"stage2: unexpected failure: TypeError\n")
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
