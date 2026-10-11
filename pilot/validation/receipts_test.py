"""Tests for serial run custody and current-host cost receipts."""

from __future__ import annotations

import hashlib
import json
import os
import stat
import sys
import tempfile
import unittest
from pathlib import Path

import receipts


def digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


class RunRoot:
    def __init__(self, test: unittest.TestCase) -> None:
        self._tmp = tempfile.TemporaryDirectory(prefix="harness-receipts-")
        test.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)


class SerialRunLockTest(unittest.TestCase):
    def test_acquire_hold_release(self) -> None:
        root = RunRoot(self).root
        lock = receipts.SerialRunLock(root, "round-a")
        self.assertIsNone(lock.held_by())
        lock.acquire()
        self.assertEqual(lock.held_by(), "round-a")
        mode = stat.S_IMODE((root / receipts.RUN_LOCK_NAME).stat().st_mode)
        self.assertEqual(mode, 0o600)
        with self.assertRaises(receipts.ReceiptError):
            receipts.SerialRunLock(root, "round-a").acquire()
        with self.assertRaises(receipts.ReceiptError):
            receipts.SerialRunLock(root, "round-b").acquire()
        self.assertEqual(lock.held_by(), "round-a")
        with self.assertRaises(receipts.ReceiptError):
            receipts.SerialRunLock(root, "round-b").release()
        self.assertEqual(lock.held_by(), "round-a")
        lock.release()
        self.assertIsNone(lock.held_by())
        with self.assertRaises(receipts.ReceiptError):
            lock.release()

    def test_malformed_lock_is_never_touched(self) -> None:
        root = RunRoot(self).root
        root.mkdir(parents=True, exist_ok=True)
        (root / receipts.RUN_LOCK_NAME).write_text("not json")
        with self.assertRaises(receipts.ReceiptError):
            receipts.SerialRunLock(root, "round-a").held_by()
        with self.assertRaises(receipts.ReceiptError):
            receipts.SerialRunLock(root, "round-a").acquire()
        self.assertEqual((root / receipts.RUN_LOCK_NAME).read_text(), "not json")


class BoundedStepTest(unittest.TestCase):
    def test_successful_step(self) -> None:
        root = RunRoot(self).root
        receipt = receipts.run_bounded_step(
            root,
            "ok",
            [sys.executable, "-c", "open('out.txt','w').write('data')"],
            30,
            outputs=("out.txt",),
            run_binding_sha256="sha256:" + "b" * 64,
        )
        self.assertEqual(receipt["outcome"], "success")
        self.assertEqual(receipt["exit_code"], 0)
        self.assertIsNone(receipt["signal"])
        self.assertFalse(receipt["timed_out"])
        self.assertEqual(receipt["teardown"], "clean")
        self.assertEqual(receipt["retained"], [])
        self.assertEqual(
            receipt["outputs"],
            [{"path": "out.txt", "bytes": 4, "sha256": digest(b"data")}],
        )
        self.assertGreaterEqual(receipt["wall_seconds"], 0)
        self.assertIsInstance(receipt["max_rss_kib"], int)
        self.assertEqual(receipt["host"], receipts.host_facts())
        self.assertFalse((root / receipts.SCRATCH_DIR / "ok").exists())
        written = json.loads((root / receipts.RECEIPTS_DIR / "ok.json").read_text())
        self.assertEqual(written, receipt)
        self.assertEqual(receipts.validate_receipt(written), written)

    def test_failed_step_retains_its_workspace(self) -> None:
        root = RunRoot(self).root
        script = "open('note.txt','w').write('why it failed')\nimport sys\nsys.exit(3)"
        receipt = receipts.run_bounded_step(
            root, "fail", [sys.executable, "-c", script], 30, outputs=("note.txt", "absent.txt")
        )
        self.assertEqual(receipt["outcome"], "failed")
        self.assertEqual(receipt["exit_code"], 3)
        self.assertIsNone(receipt["signal"])
        self.assertEqual(receipt["retained"], ["retained/fail/note.txt"])
        self.assertEqual((root / "retained" / "fail" / "note.txt").read_text(), "why it failed")
        self.assertFalse((root / "retained" / "fail" / "TRUNCATED.json").exists())
        self.assertEqual(
            receipt["outputs"],
            [
                {"path": "note.txt", "bytes": 13, "sha256": digest(b"why it failed")},
                {"path": "absent.txt", "missing": True},
            ],
        )
        self.assertFalse((root / receipts.SCRATCH_DIR / "fail").exists())

    def test_oversized_retention_is_marked(self) -> None:
        root = RunRoot(self).root
        size = receipts.RETAINED_FILE_CAP + 1
        script = f"open('big.bin','wb').write(b'x' * {size})\nimport sys\nsys.exit(1)"
        receipt = receipts.run_bounded_step(root, "big", [sys.executable, "-c", script], 30)
        self.assertEqual(receipt["outcome"], "failed")
        marker = root / "retained" / "big" / "TRUNCATED.json"
        self.assertIn("retained/big/TRUNCATED.json", receipt["retained"])
        omitted = json.loads(marker.read_text())
        self.assertEqual(omitted["schema"], receipts.TRUNCATION_SCHEMA)
        self.assertEqual(omitted["omitted"], [{"path": "big.bin", "reason": "size_cap", "bytes": size}])
        self.assertFalse((root / "retained" / "big" / "big.bin").exists())

    def test_retention_entry_cap_is_marked(self) -> None:
        root = RunRoot(self).root
        count = receipts.RETAINED_ENTRY_CAP + 8
        script = (
            f"for i in range({count}):\n"
            "    open(f'f{i:04d}.txt','w').write('x')\n"
            "import sys\nsys.exit(1)"
        )
        receipt = receipts.run_bounded_step(root, "many", [sys.executable, "-c", script], 60)
        self.assertEqual(receipt["outcome"], "failed")
        kept = [entry for entry in receipt["retained"] if not entry.endswith("TRUNCATED.json")]
        self.assertEqual(len(kept), receipts.RETAINED_ENTRY_CAP)
        omitted = json.loads((root / "retained" / "many" / "TRUNCATED.json").read_text())["omitted"]
        self.assertTrue(all(entry["reason"] == "entry_cap" for entry in omitted))
        self.assertEqual(len(omitted), count - receipts.RETAINED_ENTRY_CAP)

    def test_timeout_kills_the_session_and_joins_descendants(self) -> None:
        root = RunRoot(self).root
        script = (
            "import subprocess, sys, time\n"
            "child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'])\n"
            "open('grandchild.pid','w').write(str(child.pid))\n"
            "time.sleep(30)\n"
        )
        receipt = receipts.run_bounded_step(root, "hang", [sys.executable, "-c", script], 0.7)
        self.assertEqual(receipt["outcome"], "stop")
        self.assertTrue(receipt["timed_out"])
        self.assertEqual(receipt["signal"], 9)
        self.assertEqual(receipt["teardown"], "killed_stray_members")
        grandchild = int((root / "retained" / "hang" / "grandchild.pid").read_text())
        with self.assertRaises(ProcessLookupError):
            os.kill(grandchild, 0)

    def test_step_names_are_single_use(self) -> None:
        root = RunRoot(self).root
        receipts.run_bounded_step(root, "once", [sys.executable, "-c", "pass"], 30)
        before = json.loads((root / receipts.RECEIPTS_DIR / "once.json").read_text())
        with self.assertRaises(receipts.ReceiptError):
            receipts.run_bounded_step(root, "once", [sys.executable, "-c", "pass"], 30)
        self.assertEqual(json.loads((root / receipts.RECEIPTS_DIR / "once.json").read_text()), before)
        self.assertFalse((root / receipts.SCRATCH_DIR / "once").exists())

    def test_malformed_requests_refuse(self) -> None:
        root = RunRoot(self).root
        cases = {
            "bad step name": dict(step="../evil", argv=[sys.executable], timeout_s=1),
            "empty argv": dict(step="s", argv=[], timeout_s=1),
            "non-string argv entry": dict(step="s", argv=[sys.executable, 1], timeout_s=1),
            "zero timeout": dict(step="s", argv=[sys.executable], timeout_s=0),
            "absolute output": dict(step="s", argv=[sys.executable], timeout_s=1, outputs=("/tmp/x",)),
            "escaping output": dict(step="s", argv=[sys.executable], timeout_s=1, outputs=("../x",)),
            "bad binding": dict(step="s", argv=[sys.executable], timeout_s=1, run_binding_sha256="b" * 64),
        }
        for name, request in cases.items():
            with self.subTest(name):
                with self.assertRaises(receipts.ReceiptError):
                    receipts.run_bounded_step(root, **request)
        self.assertFalse((root / receipts.RECEIPTS_DIR).exists())


class ValidateReceiptTest(unittest.TestCase):
    def receipt(self) -> dict:
        root = RunRoot(self).root
        return receipts.run_bounded_step(root, "ok", [sys.executable, "-c", "pass"], 30)

    def test_tampering_refuses(self) -> None:
        good = self.receipt()
        cases = {
            "unknown field": {**good, "extra": 1},
            "success with exit 1": {**good, "exit_code": 1},
            "success with signal": {**good, "signal": 9},
            "success with timeout": {**good, "timed_out": True},
            "clean while timed out": {**good, "outcome": "stop", "timed_out": True},
            "stop without cause": {**good, "outcome": "stop", "timed_out": False},
            "bad workspace": {**good, "workspace": "scratch/other"},
            "bad host": {**good, "host": {**good["host"], "physical_memory_bytes": 0}},
            "unknown teardown": {**good, "teardown": "mostly"},
            "open output entry": {**good, "outputs": [{"path": "x", "bytes": 1}]},
            "bad retained": {**good, "retained": [1]},
        }
        for name, value in cases.items():
            with self.subTest(name):
                with self.assertRaises(receipts.ReceiptError):
                    receipts.validate_receipt(value)
        receipts.validate_receipt(good)

    def test_host_facts_are_closed_and_positive(self) -> None:
        facts = receipts.host_facts()
        self.assertEqual(set(facts), {"sysname", "machine", "cpu_count", "physical_memory_bytes"})
        self.assertGreaterEqual(facts["cpu_count"], 1)
        self.assertGreater(facts["physical_memory_bytes"], 0)


if __name__ == "__main__":
    unittest.main()
