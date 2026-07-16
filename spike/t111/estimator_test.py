#!/usr/bin/env python3
"""Synthetic-only regressions for the one-score estimator state machine."""

from __future__ import annotations

import json
import os
import stat
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parent))

import estimator  # noqa: E402


class InstrumentedOutcomePath:
    """A fixture path that records every metadata or content access."""

    def __init__(self, name: str) -> None:
        self.name = name
        self.accesses: list[str] = []

    def _access(self, operation: str) -> None:
        self.accesses.append(operation)
        raise AssertionError(f"unexpected {operation} access to {self.name}")

    def lstat(self) -> None:
        self._access("lstat")

    def stat(self) -> None:
        self._access("stat")

    def exists(self) -> bool:
        self._access("exists")
        return False

    def is_file(self) -> bool:
        self._access("is_file")
        return False

    def is_symlink(self) -> bool:
        self._access("is_symlink")
        return False

    def open(self, *_args: object, **_kwargs: object) -> None:
        self._access("open")

    def read_bytes(self) -> bytes:
        self._access("read_bytes")
        return b""

    def read_text(self, *_args: object, **_kwargs: object) -> str:
        self._access("read_text")
        return ""

    def __fspath__(self) -> str:
        self._access("fspath")
        return self.name


def synthetic_authorization(root: Path) -> dict[str, object]:
    return {
        "authorization_id": estimator.AUTHORIZATION_ID,
        "state": {
            "admission_receipt": str(root / "admission.json"),
            "consumption_marker": str(root / "consumed.json"),
            "result_receipt": str(root / "result.json"),
        },
    }


class FakeAdmissionServices:
    def __init__(
        self,
        *,
        key_path: InstrumentedOutcomePath,
        labels_path: InstrumentedOutcomePath,
        fail_at: str,
    ) -> None:
        self.key_path = key_path
        self.labels_path = labels_path
        self.fail_at = fail_at
        self.events: list[str] = []
        self.closed = False

    def _step(self, name: str) -> None:
        self.events.append(name)
        if self.fail_at == name:
            raise estimator.EstimatorError(f"synthetic {name} failure")

    def guard_toolchain(self) -> dict[str, str]:
        self._step("toolchain")
        return {"go_version": "synthetic", "git_version": "synthetic"}

    def verify_implementation(self, _toolchain: object) -> None:
        self._step("implementation")

    def verify_public(self) -> None:
        self._step("public")

    def hash_outcomes(self) -> None:
        self._step("opaque_hash")
        # Tests that reach this step deliberately exercise only synthetic bytes.
        self.key_path.read_bytes()
        self.labels_path.read_bytes()

    def reconstruct(self) -> dict[str, object]:
        self._step("reconstruct")
        return {"synthetic": True}

    def prove_unused(self) -> None:
        self._step("state")

    def context(self, _toolchain: object, _bundle: object) -> object:
        self._step("context")
        raise AssertionError("failure fixture unexpectedly reached context creation")

    def close(self) -> None:
        self.closed = True


class EstimatorStateMachineTests(unittest.TestCase):
    def test_toolchain_failure_has_zero_key_or_label_path_access(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            auth = synthetic_authorization(root)
            key = InstrumentedOutcomePath("synthetic-key.jsonl")
            labels = InstrumentedOutcomePath("synthetic-labels.jsonl")
            services = FakeAdmissionServices(
                key_path=key, labels_path=labels, fail_at="toolchain"
            )

            outcome = estimator.run_phase_a(
                auth, "sha256:" + "1" * 64, ["synthetic"], services=services
            )

            self.assertFalse(outcome.admitted)
            self.assertEqual(services.events, ["toolchain"])
            self.assertEqual(key.accesses, [])
            self.assertEqual(labels.accesses, [])
            self.assertTrue(services.closed)
            self.assertEqual(outcome.receipt["decision"], "NO RESULT")
            self.assertFalse(outcome.receipt["consumed"])
            self.assertTrue((root / "admission.json").is_file())
            self.assertFalse((root / "consumed.json").exists())
            self.assertFalse((root / "result.json").exists())

    def test_consumption_marker_is_durable_and_exclusive(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            auth = synthetic_authorization(root)
            admission = {
                "schema": estimator.ADMISSION_RECEIPT_SCHEMA,
                "authorization_id": estimator.AUTHORIZATION_ID,
                "status": "ADMITTED",
                "consumed": False,
            }
            estimator.atomic_replace_receipt(root / "admission.json", admission)
            context = estimator.AdmissionContext(
                authorization=auth,
                authorization_digest="sha256:" + "2" * 64,
                toolchain={},
            )
            decoded = 0

            def execute(_context: estimator.AdmissionContext) -> int:
                nonlocal decoded
                decoded += 1
                print("synthetic score transcript")
                return 0

            guard = lambda: {
                "go_version": "synthetic-go",
                "git_version": "synthetic-git",
            }
            with patch.object(estimator.os, "fsync", wraps=os.fsync) as fsync:
                self.assertEqual(
                    estimator.run_phase_b(context, guard=guard, execute=execute), 0
                )
            self.assertGreaterEqual(fsync.call_count, 4)
            marker_path = root / "consumed.json"
            result_path = root / "result.json"
            marker_before = marker_path.read_bytes()
            result_before = result_path.read_bytes()
            self.assertEqual(stat.S_IMODE(marker_path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(result_path.stat().st_mode), 0o600)
            self.assertEqual(json.loads(marker_before)["transition"], "consumed")
            self.assertEqual(json.loads(result_before)["status"], "COMPLETED")
            self.assertEqual(decoded, 1)

            with self.assertRaises(estimator.AuthorizationConsumed):
                estimator.run_phase_b(context, guard=guard, execute=execute)
            self.assertEqual(decoded, 1)
            self.assertEqual(marker_path.read_bytes(), marker_before)
            self.assertEqual(result_path.read_bytes(), result_before)

    def test_phase_a_public_failure_is_repeatable_and_non_consuming(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            auth = synthetic_authorization(root)
            key = InstrumentedOutcomePath("synthetic-key.jsonl")
            labels = InstrumentedOutcomePath("synthetic-labels.jsonl")
            failures: list[dict[str, object]] = []

            for _ in range(2):
                services = FakeAdmissionServices(
                    key_path=key, labels_path=labels, fail_at="public"
                )
                outcome = estimator.run_phase_a(
                    auth,
                    "sha256:" + "3" * 64,
                    ["synthetic"],
                    services=services,
                )
                self.assertFalse(outcome.admitted)
                self.assertEqual(
                    services.events, ["toolchain", "implementation", "public"]
                )
                self.assertTrue(services.closed)
                failures.append(outcome.receipt["failure"])

            self.assertEqual(failures[0], failures[1])
            self.assertEqual(key.accesses, [])
            self.assertEqual(labels.accesses, [])
            self.assertFalse((root / "consumed.json").exists())
            self.assertFalse((root / "result.json").exists())
            persisted = json.loads((root / "admission.json").read_bytes())
            self.assertEqual(persisted["status"], "REJECTED")
            self.assertFalse(persisted["consumed"])

    def test_post_marker_exception_writes_one_terminal_result(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            auth = synthetic_authorization(root)
            estimator.atomic_replace_receipt(
                root / "admission.json",
                {
                    "schema": estimator.ADMISSION_RECEIPT_SCHEMA,
                    "authorization_id": estimator.AUTHORIZATION_ID,
                    "status": "ADMITTED",
                    "consumed": False,
                },
            )
            context = estimator.AdmissionContext(
                authorization=auth,
                authorization_digest="sha256:" + "4" * 64,
                toolchain={},
            )
            executions = 0

            def abort(_context: estimator.AdmissionContext) -> int:
                nonlocal executions
                executions += 1
                raise RuntimeError("synthetic post-marker abort")

            guard = lambda: {
                "go_version": "synthetic-go",
                "git_version": "synthetic-git",
            }
            self.assertEqual(
                estimator.run_phase_b(context, guard=guard, execute=abort), 2
            )
            marker = root / "consumed.json"
            result = root / "result.json"
            self.assertTrue(marker.is_file())
            receipt = json.loads(result.read_bytes())
            self.assertEqual(receipt["status"], "ABORTED")
            self.assertEqual(receipt["gate_2"], "NOT ESTABLISHED")
            self.assertIsNone(receipt["performance_claim"])
            self.assertIsNone(receipt["score_stdout"])
            terminal_bytes = result.read_bytes()

            with self.assertRaises(estimator.AuthorizationConsumed):
                estimator.run_phase_b(context, guard=guard, execute=abort)
            self.assertEqual(executions, 1)
            self.assertEqual(result.read_bytes(), terminal_bytes)


if __name__ == "__main__":
    unittest.main()
