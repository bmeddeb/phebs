"""Synthetic tests for the Stage-2 enumeration verifier.

These tests never build a derived root, hydrate modules, invoke t111, or
enumerate a real fixture.  They exercise only the glue's fail-closed data
contracts and a fully in-memory frame-builder double.
"""

import copy
import importlib._bootstrap_external
import importlib.util
import json
import os
import signal
import tempfile
import unittest
from unittest import mock
from pathlib import Path

HERE = Path(__file__).resolve().parent
import sys

sys.path.insert(0, str(HERE))
import stage2_enumerate as enum  # noqa: E402
import stage2_prebuild_test as prebuild_tests  # noqa: E402


def _receipt():
    return {
        "heads": {
            fixture: {"head_oid": f"{number:040x}"}
            for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
        }
    }


def _auth(base: Path, derived: Path, receipt: dict):
    return {
        "base_lock_sha256": enum.sha256_file(base),
        "derived_lock_sha256": enum.sha256_file(derived),
        "heads": {fixture: receipt["heads"][fixture]["head_oid"] for fixture in enum.RECEIPT_FIXTURES},
    }


def _digest(character: str) -> str:
    return "sha256:" + character * 64


def _write_fact_runs(root: Path, *, failure_fixture=None, same_run_id=False):
    run1, run2 = root / "run1", root / "run2"
    run1.mkdir(); run2.mkdir()
    facts = {}
    for fixture in enum.RECEIPT_FIXTURES:
        body = json.dumps({"predicate": "CALLS_OPERATION"}) + "\n"
        if fixture == failure_fixture:
            body = json.dumps({"predicate": "LOAD_ERRORS"}) + "\n"
        for directory in (run1, run2):
            path = directory / f"{fixture}.facts.jsonl"
            path.write_text(body)
            path.chmod(0o400)
        facts[fixture] = enum.sha256_file(run1 / f"{fixture}.facts.jsonl")
    auth = {
        "heads": {fixture: f"{number:040x}" for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)},
        "derived_lock_sha256": _digest("a"),
        "cache_tree_sha256": _digest("b"),
        "t111_binary_sha256": _digest("c"),
        "typedcalloracle_binary_sha256": _digest("d"),
        "git_sha256": _digest("e"),
        "go_sha256": _digest("f"),
        "producer_toolchain_identity": "exact-tools",
        "fact_runs": {},
    }
    for number, directory in enumerate((run1, run2), 1):
        run_id = "separate-run-0001" if same_run_id else f"separate-run-{number:04d}"
        receipt = {
            "schema": enum.FACT_RUN_RECEIPT_SCHEMA,
            "status": "COMPLETE",
            "run_id": run_id,
            "execution_mode": "offline",
            "commands": {
                fixture: {"subcommand": "extract", "system": fixture, "exit_code": 0}
                for fixture in enum.RECEIPT_FIXTURES
            },
            "derived_lock_sha256": auth["derived_lock_sha256"],
            "cache_tree_sha256": auth["cache_tree_sha256"],
            "t111_binary_sha256": auth["t111_binary_sha256"],
            "typedcalloracle_binary_sha256": auth["typedcalloracle_binary_sha256"],
            "git_sha256": auth["git_sha256"],
            "go_sha256": auth["go_sha256"],
            "producer_toolchain_identity": auth["producer_toolchain_identity"],
            "heads": auth["heads"],
            "facts_sha256": facts,
        }
        receipt_path = directory / "fact-run-receipt.json"
        receipt_path.write_text(enum.canonical_json(receipt) + "\n")
        receipt_path.chmod(0o400)
        auth["fact_runs"][f"run{number}"] = {
            "run_id": run_id,
            "receipt_sha256": enum.sha256_file(receipt_path),
            "facts_sha256": facts,
        }
    run1.chmod(0o500); run2.chmod(0o500)
    return run1, run2, auth


def _write_canonical(path: Path, value: dict) -> bytes:
    path.parent.mkdir(parents=True, exist_ok=True)
    raw = (enum.canonical_json(value) + "\n").encode("utf-8")
    path.write_bytes(raw)
    return raw


def _p0_result_chain_fixture(root: Path):
    """Build only synthetic P0 state receipts; no derived tree is created."""
    root = root.resolve()
    execution_root = root / "derived"
    ceremony = root / "p0-ceremony"
    state = {
        "ceremony_directory": str(ceremony),
        "consumption_marker": str(ceremony / "consumed.json"),
        "terminal_receipt": str(ceremony / "terminal.json"),
        "evidence_receipt": str(ceremony / "evidence.json"),
    }
    heads = {
        fixture: f"{number:040x}"
        for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
    }
    digests = {
        "receipt": _digest("a"),
        "response": _digest("b"),
        "inventory": _digest("c"),
        "lock": _digest("d"),
        "manifest": _digest("e"),
        "t111": _digest("f"),
        "oracle": _digest("0"),
        "python": _digest("1"),
        "git": _digest("2"),
        "go": _digest("3"),
        "derived_lock": _digest("4"),
        "derived_manifest": _digest("5"),
        "cache": _digest("6"),
        "marker_auth": _digest("7"),
    }
    inputs = {
        "stage1_receipt_sha256": digests["receipt"],
        "stage1_response_sha256": digests["response"],
        "stage0_inventory_sha256": digests["inventory"],
        "base_lock_sha256": digests["lock"],
        "derived_lock_sha256": digests["derived_lock"],
        "stage0_harness_manifest_sha256": digests["manifest"],
        "t111_binary_sha256": digests["t111"],
        "typedcalloracle_binary_sha256": digests["oracle"],
        "heads": heads,
    }
    toolchain = {
        "python_executable": "/tools/python3",
        "python_version": "3.9.6",
        "python_sha256": digests["python"],
        "git_executable": "/tools/git",
        "git_sha256": digests["git"],
        "go_executable": "/tools/go",
        "go_sha256": digests["go"],
        "producer_toolchain_identity": "exact-tools",
    }
    implementation = {
        "prebuild_runner_sha256": _digest("8"),
        "enumerator_sha256": _digest("9"),
        "enumerator_review_sha256": _digest("a"),
    }
    fact_runs = {
        run: {
            "run_id": f"p0-{run}-0001",
            "path": str(execution_root / "spike" / "t111" / "stage2-facts" / run),
            "receipt_path": str(
                execution_root / "spike" / "t111" / "stage2-facts" / run / "fact-run-receipt.json"
            ),
        }
        for run in ("run1", "run2")
    }
    authorization_id = "stage2-prebuild-auth-0001"
    expected_runs = {
        run: {
            "run_id": fact_runs[run]["run_id"],
            "root": fact_runs[run]["path"],
            "receipt_path": fact_runs[run]["receipt_path"],
        }
        for run in ("run1", "run2")
    }
    p0 = {
        "schema": enum.P0_AUTHORIZATION_SCHEMA,
        "status": "AUTHORIZED",
        "authorization_id": authorization_id,
        "implementation": implementation,
        "bootstrap": {"t111_sha256": digests["t111"]},
        "inputs": inputs,
        "toolchain": toolchain,
        "environment": {"hydration": {"phase": "hydration"}, "offline": {"phase": "offline"}},
        "derived_root": {"root": str(execution_root)},
        "fact_runs": fact_runs,
        "operations": {},
        "state": state,
        "record_projections": {
            "evidence": {
                "schema": enum.P0_EVIDENCE_RECEIPT_SCHEMA,
                "status": "COMPLETE",
                "prebuild_id": authorization_id,
                "authorization_schema": enum.P0_AUTHORIZATION_SCHEMA,
                "authorization_id": authorization_id,
                "record_path": state["evidence_receipt"],
                "execution_root": str(execution_root),
                "fact_runs": expected_runs,
            },
            "terminal": {
                "schema": enum.P0_TERMINAL_RECEIPT_SCHEMA,
                "prebuild_id": authorization_id,
                "authorization_schema": enum.P0_AUTHORIZATION_SCHEMA,
                "authorization_id": authorization_id,
                "record_path": state["terminal_receipt"],
                "execution_root": str(execution_root),
                "fact_runs": expected_runs,
            },
        },
        "scope": dict(enum.P0_EXPECTED_SCOPE),
        "implementation_review": {},
        "implementation_binding": {},
    }
    facts = {fixture: _digest("b") for fixture in enum.RECEIPT_FIXTURES}
    marker = {
        "schema": enum.P0_CONSUMPTION_MARKER_SCHEMA,
        "status": "CONSUMED",
        "authorization_id": authorization_id,
        "authorization_sha256": digests["marker_auth"],
    }
    marker_bytes = _write_canonical(Path(state["consumption_marker"]), marker)
    marker_sha256 = enum.sha256_bytes(marker_bytes)
    result = {
        "schema": enum.P0_EVIDENCE_RECEIPT_SCHEMA,
        "status": "COMPLETE",
        "authorization_id": authorization_id,
        "authorization_sha256": digests["marker_auth"],
        "consumption_marker_sha256": marker_sha256,
        "implementation": implementation,
        "inputs": inputs,
        "toolchain": toolchain,
        "environment": p0["environment"],
        "derived": {
            "execution_root": str(execution_root),
            "derived_lock_sha256": digests["derived_lock"],
            "derived_harness_manifest_sha256": digests["derived_manifest"],
            "cache_tree_sha256": digests["cache"],
            "corpus_heads": heads,
        },
        "hydration": {
            fixture: {
                "exit_code": 0,
                "stdout_sha256": _digest("c"),
                "stderr_sha256": _digest("d"),
            }
            for fixture in enum.RECEIPT_FIXTURES
        },
        "fact_runs": {
            run: {
                "run_id": fact_runs[run]["run_id"],
                "root": fact_runs[run]["path"],
                "receipt_sha256": _digest("e" if run == "run1" else "f"),
                "facts_sha256": facts,
                "capture": {"stdout_sha256": _digest("0"), "stderr_sha256": _digest("1")},
            }
            for run in ("run1", "run2")
        },
    }
    result_bytes = _write_canonical(Path(state["evidence_receipt"]), result)
    result_sha256 = enum.sha256_bytes(result_bytes)
    terminal = {
        "schema": enum.P0_TERMINAL_RECEIPT_SCHEMA,
        "status": "COMPLETED",
        "authorization_sha256": digests["marker_auth"],
        "consumption_marker_sha256": marker_sha256,
        "exit_code": 0,
        "failure": None,
        "evidence_receipt_sha256": result_sha256,
    }
    terminal_bytes = _write_canonical(Path(state["terminal_receipt"]), terminal)
    evidence = {
        "schema": enum.PREBUILD_EVIDENCE_SCHEMA,
        "status": "COMPLETE",
        "prebuild_id": authorization_id,
        "p0_authorization": {
            "path": enum.P0_AUTHORIZATION_REL,
            "sha256": digests["marker_auth"],
            "authorization_commit": "a" * 40,
        },
        "receipt_sha256": inputs["stage1_receipt_sha256"],
        "response_sha256": inputs["stage1_response_sha256"],
        "stage0_inventory_sha256": inputs["stage0_inventory_sha256"],
        "base_lock_sha256": inputs["base_lock_sha256"],
        "derived_lock_sha256": result["derived"]["derived_lock_sha256"],
        "stage0_harness_manifest_sha256": inputs["stage0_harness_manifest_sha256"],
        "derived_harness_manifest_sha256": result["derived"]["derived_harness_manifest_sha256"],
        "cache_tree_sha256": result["derived"]["cache_tree_sha256"],
        "t111_binary_sha256": inputs["t111_binary_sha256"],
        "typedcalloracle_binary_sha256": inputs["typedcalloracle_binary_sha256"],
        "python_executable": toolchain["python_executable"],
        "python_version": toolchain["python_version"],
        "python_mode": enum.PYTHON_MODE,
        "python_sha256": toolchain["python_sha256"],
        "git_executable": toolchain["git_executable"],
        "git_sha256": toolchain["git_sha256"],
        "go_executable": toolchain["go_executable"],
        "go_sha256": toolchain["go_sha256"],
        "producer_toolchain_identity": toolchain["producer_toolchain_identity"],
        "environment": {"network": "disabled", "variables": {"PATH": "/tools"}},
        "heads": heads,
        "execution_root": str(execution_root),
        "fact_runs": {
            run: {
                "run_id": result["fact_runs"][run]["run_id"],
                "root": result["fact_runs"][run]["root"],
                "receipt_sha256": result["fact_runs"][run]["receipt_sha256"],
                "facts_sha256": result["fact_runs"][run]["facts_sha256"],
            }
            for run in ("run1", "run2")
        },
        "p0_result": {
            "consumption_marker_sha256": marker_sha256,
            "terminal_receipt_sha256": enum.sha256_bytes(terminal_bytes),
            "evidence_receipt_sha256": result_sha256,
        },
    }
    authorization = {
        "p0_authorization": dict(evidence["p0_authorization"]),
        "fact_runs": {
            run: {
                "run_id": evidence["fact_runs"][run]["run_id"],
                "receipt_sha256": evidence["fact_runs"][run]["receipt_sha256"],
                "facts_sha256": evidence["fact_runs"][run]["facts_sha256"],
            }
            for run in ("run1", "run2")
        },
    }
    return {
        "p0": p0,
        "authorization": authorization,
        "evidence": evidence,
        "marker": marker,
        "result": result,
        "terminal": terminal,
        "paths": {name: Path(path) for name, path in state.items()},
    }


class FakeLabelPrep:
    """Exact minimal surface used by ``enumerate_raw_frames``; no selectors."""

    def __init__(self, *, unstable=False):
        self.unstable = unstable
        self.typed_calls = 0

    def pinned_tracked_files(self, root, commit, excluded):
        return ["client.go"], []

    def load_verified_facts(self, path, **kwargs):
        return [{"toolchain_identity": "exact-tools"}]

    def operation_facts(self, fixture, facts_dir, commit, corpus):
        return [], []

    def resolve_oracle_toolchain(self, identity):
        self.assert_equal(identity, "exact-tools")
        digest = enum.sha256_file(Path(sys.executable))
        return {
            "identity": identity,
            "go_digest": digest,
            "git_digest": digest,
            "go_path": sys.executable,
            "git_path": sys.executable,
        }

    @staticmethod
    def assert_equal(actual, expected):
        if actual != expected:
            raise AssertionError(f"{actual!r} != {expected!r}")

    def scan_typed_call_recall_frame(self, fixture, root, commit, *args):
        self.typed_calls += 1
        line = 7 if not self.unstable or self.typed_calls % 2 else 8
        row = {
            "site_id": f"{fixture}-typed-{line}",
            "system": fixture,
            "path": "client.go",
            "line": line,
            "start_line": line,
            "end_line": line,
        }
        return [row], {"schema": "test", "matched_sites": 1}

    @staticmethod
    def scan_registration_recall_frame(*args):
        return []

    @staticmethod
    def gate_call_facts(fixture, facts):
        return facts, {"count": 0}

    @staticmethod
    def build_precision_frame(*args):
        return []

    @staticmethod
    def build_registration_precision_frame(*args):
        return []

    @staticmethod
    def align_recall(*args):
        return None

    @staticmethod
    def align_registration(*args):
        return None

    @staticmethod
    def select_by_system(*args):
        raise AssertionError("Stage-3 selection must not be called")

    @staticmethod
    def select_role_cohort(*args):
        raise AssertionError("Stage-3 role selection must not be called")

    @staticmethod
    def build_burn_ledger(*args):
        raise AssertionError("Stage-2 carry-forward must not be called")


class Stage2EnumerationTest(unittest.TestCase):
    @staticmethod
    def _synthetic_tools():
        executable = Path(sys.executable).resolve()
        return {"go": executable, "git": executable}

    def test_membership_uses_sampling_line_not_precision_evidence_span(self):
        frames = {frame: [] for frame in enum.EXTERNAL_FRAMES}
        frames["client_call_precision"] = [{
            "system": "temporal", "path": "worker.go", "line": 44,
            "start_line": 39, "end_line": 71,
        }]
        cards, membership = enum.stage2_inputs(frames)
        self.assertEqual(cards["client_call_precision"], {"population": 1, "census": 0})
        self.assertEqual(membership["client_call_precision"], ["temporal:worker.go:44:44"])

    def test_duplicate_sampling_line_refuses_even_when_fact_spans_differ(self):
        frames = {frame: [] for frame in enum.EXTERNAL_FRAMES}
        frames["client_call_precision"] = [
            {"system": "dapr", "path": "x.go", "line": 8, "start_line": 1, "end_line": 8},
            {"system": "dapr", "path": "x.go", "line": 8, "start_line": 8, "end_line": 20},
        ]
        with self.assertRaises(enum.EnumerationError):
            enum.stage2_inputs(frames)

    def test_ambiguous_membership_components_refuse(self):
        for path in ("worker:shadow.go", "worker\nshadow.go", "../worker.go"):
            frames = {frame: [] for frame in enum.EXTERNAL_FRAMES}
            frames["client_call_precision"] = [{
                "system": "temporal", "path": path, "line": 44,
            }]
            with self.assertRaises(enum.EnumerationError):
                enum.stage2_inputs(frames)

    def test_derived_lock_allows_only_stage1_commit_changes(self):
        receipt = _receipt()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = root / "base.json"
            derived = root / "derived.json"
            base_rows = [
                {"name": fixture, "commit": f"old-{number}", "goos": "darwin"}
                for number, fixture in enumerate(enum.RECEIPT_FIXTURES)
            ] + [{"name": "grpc-go", "commit": "fixed", "goos": "darwin"}]
            derived_rows = [dict(row) for row in base_rows]
            for row in derived_rows:
                if row["name"] in receipt["heads"]:
                    row["commit"] = receipt["heads"][row["name"]]["head_oid"]
            base.write_text(json.dumps(base_rows))
            derived.write_text(json.dumps(derived_rows))
            auth = _auth(base, derived, receipt)
            inventory = {"spike/t111/corpus.lock.json": enum.sha256_file(base)}
            entries = enum.verify_derived_lock(base, derived, receipt, auth, inventory)
            self.assertEqual(set(entries), set(enum.RECEIPT_FIXTURES))

            derived_rows[-1]["goos"] = "linux"
            derived.write_text(json.dumps(derived_rows))
            auth["derived_lock_sha256"] = enum.sha256_file(derived)
            with self.assertRaises(enum.EnumerationError):
                enum.verify_derived_lock(base, derived, receipt, auth, inventory)

    def test_fact_pair_refuses_a_recorded_load_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            run1, run2, auth = _write_fact_runs(root, failure_fixture="loki")
            with self.assertRaises(enum.EnumerationError):
                enum.verify_fact_pairs(run1, run2, auth)

    def test_fact_proof_requires_two_distinct_directories(self):
        with tempfile.TemporaryDirectory() as tmp:
            run, _other, auth = _write_fact_runs(Path(tmp))
            with self.assertRaises(enum.EnumerationError):
                enum.verify_fact_pairs(run, run, auth)

    def test_fact_proof_requires_separate_run_receipts(self):
        with tempfile.TemporaryDirectory() as tmp:
            run1, run2, auth = _write_fact_runs(Path(tmp), same_run_id=True)
            with self.assertRaises(enum.EnumerationError):
                enum.verify_fact_pairs(run1, run2, auth)

    def test_cache_root_and_intermediate_symlinks_are_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cache = root / "cache"
            cache.mkdir()
            # A sealed cache must include the root directory, not merely its
            # descendants: otherwise a checked cache can be swapped later.
            with self.assertRaises(enum.EnumerationError):
                enum._tree_digest(cache)

            outside = root / "outside"
            outside.mkdir()
            (root / "spike").symlink_to(outside, target_is_directory=True)
            with self.assertRaises(enum.EnumerationError):
                enum.within(root / "spike" / "t111", root, "derived t111 root")

    def test_sealed_git_metadata_refuses_external_linkage(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            fixture = root / "fixture"
            git_dir = fixture / ".git"
            (git_dir / "objects" / "info").mkdir(parents=True)
            (git_dir / "config").write_text("[core]\nrepositoryformatversion = 0\n")
            (git_dir / "commondir").write_text("/outside\n")
            for path in sorted(git_dir.rglob("*"), reverse=True):
                if path.is_dir():
                    path.chmod(0o500)
                else:
                    path.chmod(0o400)
            git_dir.chmod(0o500)
            fixture.chmod(0o500)
            with self.assertRaises(enum.EnumerationError):
                enum.verify_sealed_git_repository(fixture, root)

    def test_sealed_git_metadata_refuses_active_local_config(self):
        hostile = (
            "[core]\nrepositoryformatversion = 0\nworktree = /outside\n",
            "[core]\nrepositoryformatversion = 0\nattributesfile = /outside\n",
            "[core]\nrepositoryformatversion = 0\nbare = false\n",
            "[filter \"owned\"]\nclean = /bin/false\n",
            "[extensions]\nworktreeConfig = true\n",
        )
        for config in hostile:
            with self.subTest(config=config), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                fixture = root / "fixture"
                git_dir = fixture / ".git"
                (git_dir / "objects" / "info").mkdir(parents=True)
                (git_dir / "config").write_text(config)
                for path in sorted(git_dir.rglob("*"), reverse=True):
                    if path.is_dir():
                        path.chmod(0o500)
                    else:
                        path.chmod(0o400)
                git_dir.chmod(0o500)
                fixture.chmod(0o500)
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_sealed_git_repository(fixture, root)

    def test_sealed_git_metadata_refuses_a_fixture_source_symlink(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            fixture = root / "fixture"
            git_dir = fixture / ".git"
            (git_dir / "objects" / "info").mkdir(parents=True)
            (git_dir / "config").write_text("[core]\nrepositoryformatversion = 0\n")
            outside = root / "outside.go"
            outside.write_text("package outside\n")
            (fixture / "linked.go").symlink_to(outside)
            for path in sorted(git_dir.rglob("*"), reverse=True):
                if path.is_dir():
                    path.chmod(0o500)
                else:
                    path.chmod(0o400)
            git_dir.chmod(0o500)
            fixture.chmod(0o500)
            with self.assertRaises(enum.EnumerationError):
                enum.verify_sealed_git_repository(fixture, root)

    def test_authorized_state_consumes_once_and_publishes_one_terminal_receipt(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            output = root / "output"
            marker = root / "consumed.json"
            terminal = root / "terminal.json"
            authorization = {"state": {
                "output_dir": str(output),
                "consumption_marker": str(marker),
                "terminal_receipt": str(terminal),
            }}
            self.assertTrue(enum.verify_authorized_output(output, authorization))
            with self.assertRaises(enum.EnumerationError):
                enum.verify_authorized_output(root / "different-output", authorization)
            auth_file = root / "authorization.json"
            auth_file.write_text("{}\n")
            with mock.patch.object(enum, "AUTHORIZATION_PATH", auth_file):
                authorization_digest = enum.sha256_file(auth_file)
                transition = {"started": False, "marker_sha256": ""}
                digest = enum._consume_authorization(
                    authorization, authorization_digest, transition
                )
                self.assertEqual(digest, enum.sha256_file(marker))
                self.assertTrue(transition["started"])
                with self.assertRaises(enum.EnumerationError):
                    enum._consume_authorization(
                        authorization, authorization_digest, {"started": False, "marker_sha256": ""}
                    )
                terminal_state = {"written": False, "status": None}
                terminal_digest = enum._write_terminal_receipt(
                    authorization,
                    authorization_digest,
                    digest,
                    terminal_state,
                    status="ABORTED",
                    exit_code=2,
                    failure=enum._terminal_failure(enum.EnumerationError("sensitive /path:7")),
                    outputs=None,
                )
                self.assertEqual(terminal_digest, enum.sha256_file(terminal))
                self.assertTrue(terminal_state["written"])
                self.assertNotIn("sensitive /path:7", terminal.read_text())
                with self.assertRaises(enum.TerminalReceiptError):
                    enum._write_terminal_receipt(
                        authorization,
                        authorization_digest,
                        digest,
                        {"written": False, "status": None},
                        status="ABORTED",
                        exit_code=2,
                        failure=enum._terminal_failure(enum.EnumerationError("retry")),
                        outputs=None,
                    )
            self.assertTrue(marker.exists())
            self.assertTrue(terminal.exists())
            self.assertFalse(output.exists())

    def test_marker_persistence_ambiguity_is_consumed_and_terminalized(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            marker = root / "consumed.json"
            terminal = root / "terminal.json"
            authorization = {"state": {
                "output_dir": str(root / "output"),
                "consumption_marker": str(marker),
                "terminal_receipt": str(terminal),
            }}
            auth_file = root / "authorization.json"
            auth_file.write_text("{}\n")
            with mock.patch.object(enum, "AUTHORIZATION_PATH", auth_file):
                authorization_digest = enum.sha256_file(auth_file)
                transition = {"started": False, "marker_sha256": ""}
                with mock.patch.object(enum.os, "fsync", side_effect=OSError("fsync blocked")):
                    with self.assertRaises(enum.MarkerTransitionError) as raised:
                        enum._consume_authorization(authorization, authorization_digest, transition)
                self.assertTrue(marker.exists())
                enum._write_terminal_receipt(
                    authorization,
                    authorization_digest,
                    raised.exception.marker_sha256,
                    {"written": False, "status": None},
                    status="ABORTED",
                    exit_code=2,
                    failure=enum._terminal_failure(raised.exception),
                    outputs=None,
                )
            terminal_value = json.loads(terminal.read_text())
            self.assertEqual(terminal_value["status"], "ABORTED")
            self.assertEqual(
                terminal_value["failure"]["code"], "marker-persistence-indeterminate"
            )

    def test_terminal_write_failure_never_publishes_a_partial_final_receipt(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            terminal = root / "terminal.json"
            authorization = {"state": {
                "output_dir": str(root / "output"),
                "consumption_marker": str(root / "consumed.json"),
                "terminal_receipt": str(terminal),
            }}
            terminal_state = {"written": False, "status": None}
            with mock.patch.object(enum.os, "fsync", side_effect=OSError("fsync blocked")):
                with self.assertRaises(enum.TerminalReceiptError):
                    enum._write_terminal_receipt(
                        authorization,
                        _digest("a"),
                        _digest("b"),
                        terminal_state,
                        status="ABORTED",
                        exit_code=2,
                        failure=enum._terminal_failure(enum.EnumerationError("refused")),
                        outputs=None,
            )
            self.assertFalse(terminal.exists())
            self.assertEqual(list(root.glob(".terminal.json.*.tmp")), [])
            self.assertFalse(terminal_state["written"])

    def test_post_consume_refusal_publishes_an_abort_terminal_receipt(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            marker = root / "consumed.json"
            terminal = root / "terminal.json"
            digest = _digest("a")
            authorization = {
                "enumerator_sha256": digest,
                "stage0_inventory_sha256": digest,
                "state": {
                    "output_dir": str(root / "output"),
                    "consumption_marker": str(marker),
                    "terminal_receipt": str(terminal),
                },
            }
            args = type("Args", (), {
                "out": str(root / "output"),
                "receipt": str(root / "receipt.json"),
                "execution_root": str(root / "missing-derived-root"),
                "facts_run1": str(root / "run1"),
                "facts_run2": str(root / "run2"),
            })()
            with (
                mock.patch.object(enum, "load_authorization", return_value=(authorization, b"{}\n")),
                mock.patch.object(enum, "load_prebuild_evidence", return_value=({}, b"{}\n")),
                mock.patch.object(enum, "verify_authorized_toolchain_identity"),
                mock.patch.object(enum, "verify_committed_authorization", return_value=digest),
                mock.patch.object(enum, "verify_prebuild_evidence"),
                mock.patch.object(enum, "verify_interpreter"),
                mock.patch.object(enum, "configure_sealed_execution_environment", return_value={}),
                mock.patch.object(enum, "verify_authorized_state_namespaces"),
                mock.patch.object(enum, "read_inventory_sealed", return_value=({
                    enum.PROTOCOL_REL: digest,
                    "spike/t111/labeling/gate2-v2/stage0/snapshot-query.graphql": digest,
                    "spike/t111/labeling/gate2-v2/stage0/snapshot-constants.json": digest,
                }, digest)),
                mock.patch.object(enum, "verify_receipt", return_value=({}, digest)),
                mock.patch.object(enum, "sha256_file", return_value=digest),
            ):
                with self.assertRaises(enum.EnumerationError):
                    enum.run(args)
            terminal_value = json.loads(terminal.read_text())
            self.assertEqual(terminal_value["status"], "ABORTED")
            self.assertEqual(terminal_value["exit_code"], 2)
            self.assertEqual(terminal_value["failure"]["code"], "refused")
            self.assertTrue(marker.exists())
            self.assertFalse((root / "output").exists())

    def test_signal_immediately_after_marker_transition_gets_a_terminal_receipt(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            marker = root / "consumed.json"
            terminal = root / "terminal.json"
            digest = _digest("a")
            authorization = {
                "enumerator_sha256": digest,
                "stage0_inventory_sha256": digest,
                "state": {
                    "output_dir": str(root / "output"),
                    "consumption_marker": str(marker),
                    "terminal_receipt": str(terminal),
                },
            }
            args = type("Args", (), {
                "out": str(root / "output"),
                "receipt": str(root / "receipt.json"),
                "execution_root": str(root / "missing-derived-root"),
                "facts_run1": str(root / "run1"),
                "facts_run2": str(root / "run2"),
            })()
            actual_consume = enum._consume_authorization

            def consume_then_interrupt(*call_args):
                result = actual_consume(*call_args)
                handler = signal.getsignal(signal.SIGTERM)
                handler(signal.SIGTERM, None)
                return result

            with (
                mock.patch.object(enum, "load_authorization", return_value=(authorization, b"{}\n")),
                mock.patch.object(enum, "load_prebuild_evidence", return_value=({}, b"{}\n")),
                mock.patch.object(enum, "verify_authorized_toolchain_identity"),
                mock.patch.object(enum, "verify_committed_authorization", return_value=digest),
                mock.patch.object(enum, "verify_prebuild_evidence"),
                mock.patch.object(enum, "verify_interpreter"),
                mock.patch.object(enum, "configure_sealed_execution_environment", return_value={}),
                mock.patch.object(enum, "verify_authorized_state_namespaces"),
                mock.patch.object(enum, "read_inventory_sealed", return_value=({
                    enum.PROTOCOL_REL: digest,
                    "spike/t111/labeling/gate2-v2/stage0/snapshot-query.graphql": digest,
                    "spike/t111/labeling/gate2-v2/stage0/snapshot-constants.json": digest,
                }, digest)),
                mock.patch.object(enum, "verify_receipt", return_value=({}, digest)),
                mock.patch.object(enum, "sha256_file", return_value=digest),
                mock.patch.object(enum, "_consume_authorization", side_effect=consume_then_interrupt),
            ):
                with self.assertRaises(enum.EnumerationInterrupted):
                    enum.run(args)
            terminal_value = json.loads(terminal.read_text())
            self.assertEqual(terminal_value["status"], "ABORTED")
            self.assertEqual(terminal_value["failure"]["code"], "interrupted")
            self.assertTrue(marker.exists())

    def test_signal_guard_converts_a_catchable_stop_to_a_terminalizable_error(self):
        previous = enum._install_signal_guards()
        try:
            handler = signal.getsignal(signal.SIGTERM)
            self.assertTrue(callable(handler))
            with self.assertRaises(enum.EnumerationInterrupted):
                handler(signal.SIGTERM, None)
        finally:
            enum._restore_signal_guards(previous)

    def test_consumption_refuses_without_atomic_signal_transition_support(self):
        with mock.patch.object(enum.signal, "pthread_sigmask", None):
            with self.assertRaises(enum.EnumerationError):
                enum._require_signal_transition_support()

    def test_authorized_toolchain_identity_must_match_its_digest_fields(self):
        go_digest = _digest("a")
        git_digest = _digest("b")
        authorization = {
            "producer_toolchain_identity": (
                f'go_version="go version go1.26.5 darwin/arm64";go_digest={go_digest};'
                f'git_version="git version 2.50.1";git_digest={git_digest}'
            ),
            "go_sha256": go_digest,
            "git_sha256": git_digest,
        }
        enum.verify_authorized_toolchain_identity(authorization)
        authorization["git_sha256"] = _digest("c")
        with self.assertRaises(enum.EnumerationError):
            enum.verify_authorized_toolchain_identity(authorization)

    def test_authorization_schema_requires_review_binding_environment_and_ceremony(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            digest = _digest("a")
            go_digest = _digest("b")
            git_digest = _digest("c")
            variables = {"PATH": "/tmp:/usr/bin:/bin", **enum.SEALED_ENVIRONMENT_STATIC}
            authorization = {
                "schema": enum.AUTH_SCHEMA,
                "status": "AUTHORIZED",
                "authorization_id": "stage2-enumeration-auth-1",
                "p0_authorization": {
                    "path": enum.P0_AUTHORIZATION_REL,
                    "sha256": digest,
                    "authorization_commit": "a" * 40,
                },
                "enumerator_sha256": digest,
                "stage1_snapshot_sha256": digest,
                "receipt_sha256": digest,
                "response_sha256": digest,
                "stage0_inventory_sha256": digest,
                "base_lock_sha256": digest,
                "derived_lock_sha256": digest,
                "stage0_harness_manifest_sha256": digest,
                "derived_harness_manifest_sha256": digest,
                "cache_tree_sha256": digest,
                "t111_binary_sha256": digest,
                "typedcalloracle_binary_sha256": digest,
                "python_executable": "/tmp/python3",
                "python_version": "3.9.6",
                "python_mode": enum.PYTHON_MODE,
                "python_sha256": digest,
                "git_executable": "/tmp/git",
                "git_sha256": git_digest,
                "go_executable": "/tmp/go",
                "go_sha256": go_digest,
                "producer_toolchain_identity": (
                    f'go_version="go";go_digest={go_digest};git_version="git";git_digest={git_digest}'
                ),
                "review": {
                    "status": "accepted",
                    "accepted_commit": "a" * 40,
                    "record_sha256": digest,
                },
                "binding": {"status": "executable", "commit": "a" * 40},
                "environment": {"network": "disabled", "variables": variables},
                "prior_authorizations": {"estimator": {
                    "path": enum.ESTIMATOR_AUTHORIZATION_REL,
                    "sha256": digest,
                }},
                "prebuild_evidence": {
                    "path": enum.PREBUILD_EVIDENCE_REL,
                    "sha256": digest,
                    "accepted_commit": "a" * 40,
                    "review": {
                        "path": enum.PREBUILD_EVIDENCE_REVIEW_REL,
                        "sha256": digest,
                    },
                },
                "heads": {fixture: f"{number:040x}" for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)},
                "fact_runs": {run: {
                    "run_id": f"sealed-{run}-0001",
                    "receipt_sha256": digest,
                    "facts_sha256": {fixture: digest for fixture in enum.RECEIPT_FIXTURES},
                } for run in ("run1", "run2")},
                "state": {
                    "ceremony_directory": str(root / "ceremony"),
                    "output_dir": str(root / "ceremony" / "output"),
                    "consumption_marker": str(root / "ceremony" / "consumed.json"),
                    "terminal_receipt": str(root / "ceremony" / "terminal.json"),
                },
            }
            auth_file = root / "authorization.json"
            auth_file.write_text(enum.canonical_json(authorization) + "\n")
            with mock.patch.object(enum, "AUTHORIZATION_PATH", auth_file):
                loaded, raw = enum.load_authorization()
                self.assertEqual(loaded, authorization)
                self.assertEqual(raw, auth_file.read_bytes())
                authorization["review"]["status"] = "pending"
                auth_file.write_text(enum.canonical_json(authorization) + "\n")
                with self.assertRaises(enum.EnumerationError):
                    enum.load_authorization()

    def test_prebuild_evidence_is_canonical_committed_reviewed_and_p0_bound(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            evidence_path = root / "stage2-prebuild-evidence.json"
            review_path = root / "stage2-prebuild-evidence-review-r1.md"
            commit = "a" * 40
            digests = {name: _digest(character) for name, character in (
                ("receipt_sha256", "b"),
                ("response_sha256", "c"),
                ("stage0_inventory_sha256", "d"),
                ("base_lock_sha256", "e"),
                ("derived_lock_sha256", "f"),
                ("stage0_harness_manifest_sha256", "0"),
                ("derived_harness_manifest_sha256", "1"),
                ("cache_tree_sha256", "2"),
                ("t111_binary_sha256", "3"),
                ("typedcalloracle_binary_sha256", "4"),
                ("python_sha256", "5"),
                ("git_sha256", "6"),
                ("go_sha256", "7"),
            )}
            heads = {
                fixture: f"{number:040x}"
                for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
            }
            variables = {"PATH": "/tools:/usr/bin:/bin", **enum.SEALED_ENVIRONMENT_STATIC}
            fact_runs = {
                run: {
                    "run_id": f"p0-{run}-0001",
                    "root": f"/sealed/derived/facts/{run}",
                    "receipt_sha256": _digest("7" if run == "run1" else "8"),
                    "facts_sha256": {
                        fixture: _digest("9")
                        for fixture in enum.RECEIPT_FIXTURES
                    },
                }
                for run in ("run1", "run2")
            }
            review_bytes = b"# Prebuild review\n\n**Verdict: ACCEPT.**\n"
            review_digest = enum.sha256_bytes(review_bytes)
            evidence = {
                "schema": enum.PREBUILD_EVIDENCE_SCHEMA,
                "status": "COMPLETE",
                "prebuild_id": "stage2-prebuild-1",
                **digests,
                "p0_authorization": {
                    "path": enum.P0_AUTHORIZATION_REL,
                    "sha256": _digest("a"),
                    "authorization_commit": commit,
                },
                "python_executable": "/tools/python3",
                "python_version": "3.9.6",
                "python_mode": enum.PYTHON_MODE,
                "git_executable": "/tools/git",
                "go_executable": "/tools/go",
                "producer_toolchain_identity": "exact-tools",
                "environment": {"network": "disabled", "variables": variables},
                "heads": heads,
                "execution_root": "/sealed/derived",
                "fact_runs": fact_runs,
                "p0_result": {
                    "consumption_marker_sha256": _digest("b"),
                    "terminal_receipt_sha256": _digest("c"),
                    "evidence_receipt_sha256": _digest("d"),
                },
            }
            evidence_raw = (enum.canonical_json(evidence) + "\n").encode("utf-8")
            evidence_path.write_bytes(evidence_raw)
            review_path.write_bytes(review_bytes)
            authorization = {
                **{field: evidence[field] for field in enum.PREBUILD_EVIDENCE_AUTHORIZATION_FIELDS},
                "p0_authorization": dict(evidence["p0_authorization"]),
                "heads": heads,
                "fact_runs": {
                    run: {
                        "run_id": fact_runs[run]["run_id"],
                        "receipt_sha256": fact_runs[run]["receipt_sha256"],
                        "facts_sha256": fact_runs[run]["facts_sha256"],
                    }
                    for run in ("run1", "run2")
                },
                "prebuild_evidence": {
                    "path": "stage2-prebuild-evidence.json",
                    "sha256": enum.sha256_bytes(evidence_raw),
                    "accepted_commit": commit,
                    "review": {
                        "path": "stage2-prebuild-evidence-review-r1.md",
                        "sha256": review_digest,
                    },
                },
                "state": {"output_dir": "/sealed/ceremony/output"},
            }
            args = type("Args", (), {
                "execution_root": "/sealed/derived",
                "facts_run1": "/sealed/derived/facts/run1",
                "facts_run2": "/sealed/derived/facts/run2",
                # E1 deliberately does not bind output: final enum auth owns it.
                "out": "/another-ceremony/output",
            })()
            blobs = {
                "stage2-prebuild-evidence.json": evidence_raw,
                "stage2-prebuild-evidence-review-r1.md": review_bytes,
                f"{commit}:stage2-prebuild-evidence.json": evidence_raw,
                f"{commit}:stage2-prebuild-evidence-review-r1.md": review_bytes,
            }
            with (
                mock.patch.object(enum, "PREBUILD_EVIDENCE_PATH", evidence_path),
                mock.patch.object(enum, "PREBUILD_EVIDENCE_REL", "stage2-prebuild-evidence.json"),
                mock.patch.object(enum, "PREBUILD_EVIDENCE_REVIEW_PATH", review_path),
                mock.patch.object(enum, "PREBUILD_EVIDENCE_REVIEW_REL", "stage2-prebuild-evidence-review-r1.md"),
                mock.patch.object(enum, "_head_blob", side_effect=lambda relative: blobs[relative]),
                mock.patch.object(
                    enum, "_commit_blob", side_effect=lambda revision, relative: blobs[f"{revision}:{relative}"]
                ),
                mock.patch.object(enum, "_require_ancestor") as require_ancestor,
                mock.patch.object(enum, "verify_p0_authorization_binding", return_value={}),
                mock.patch.object(enum, "verify_p0_result_chain"),
            ):
                loaded, raw = enum.load_prebuild_evidence()
                self.assertEqual(loaded, evidence)
                with_arguments = copy.deepcopy(evidence)
                with_arguments["arguments"] = {"out": "/sealed/ceremony/output"}
                evidence_path.write_bytes((enum.canonical_json(with_arguments) + "\n").encode("utf-8"))
                with self.assertRaises(enum.EnumerationError):
                    enum.load_prebuild_evidence()
                evidence_path.write_bytes(evidence_raw)
                enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                authorization["p0_authorization"]["sha256"] = _digest("f")
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                authorization["p0_authorization"] = dict(evidence["p0_authorization"])
                authorization["response_sha256"] = _digest("e")
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                authorization["response_sha256"] = evidence["response_sha256"]
                authorization["fact_runs"]["run1"]["run_id"] = "wrong-run-id"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                authorization["fact_runs"]["run1"]["run_id"] = fact_runs["run1"]["run_id"]
                args.facts_run1 = "/sealed/derived/facts/other"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                args.facts_run1 = "/sealed/derived/facts/run1"
                args.execution_root = "/sealed/other-derived"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                args.execution_root = "/sealed/derived"
                require_ancestor.side_effect = enum.EnumerationError("not an ancestor")
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                require_ancestor.side_effect = None
                blobs["stage2-prebuild-evidence.json"] = b"different committed evidence\n"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)
                blobs["stage2-prebuild-evidence.json"] = evidence_raw
                authorization["heads"] = {**heads, "temporal": "f" * 40}
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_prebuild_evidence(authorization, loaded, raw, args)

    def test_prebuild_evidence_refuses_noncanonical_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve() / "stage2-prebuild-evidence.json"
            path.write_text('{ "schema": "not canonical" }\n')
            with mock.patch.object(enum, "PREBUILD_EVIDENCE_PATH", path):
                with self.assertRaises(enum.EnumerationError):
                    enum.load_prebuild_evidence()

    def test_p0_authorization_requires_real_committed_plan_approved_record(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            p0_path = root / "stage2-prebuild-authorization.json"
            p0_relative = "stage2-prebuild-authorization.json"
            p0 = copy.deepcopy(prebuild_tests.p0(root))
            p0["implementation"]["prebuild_runner_sha256"] = enum.sha256_file(
                enum.PREBUILD_RUNNER_PATH
            )
            p0_bytes = (enum.canonical_json(p0) + "\n").encode("utf-8")
            p0_path.write_bytes(p0_bytes)
            p0_digest = enum.sha256_bytes(p0_bytes)
            p0_commit = "b" * 40
            evidence_commit = "a" * 40
            binding = {
                "path": p0_relative,
                "sha256": p0_digest,
                "authorization_commit": p0_commit,
            }
            inputs = p0["inputs"]
            toolchain = p0["toolchain"]
            evidence = {
                "prebuild_id": p0["authorization_id"],
                "p0_authorization": dict(binding),
                "receipt_sha256": inputs["stage1_receipt_sha256"],
                "response_sha256": inputs["stage1_response_sha256"],
                "stage0_inventory_sha256": inputs["stage0_inventory_sha256"],
                "base_lock_sha256": inputs["base_lock_sha256"],
                "derived_lock_sha256": p0["operations"]["lock_rewrite"]["derived_lock_sha256"],
                "stage0_harness_manifest_sha256": inputs["stage0_harness_manifest_sha256"],
                "t111_binary_sha256": inputs["t111_binary_sha256"],
                "typedcalloracle_binary_sha256": inputs["typedcalloracle_binary_sha256"],
                "python_executable": toolchain["python_executable"],
                "python_version": toolchain["python_version"],
                "python_sha256": toolchain["python_sha256"],
                "git_executable": toolchain["git_executable"],
                "git_sha256": toolchain["git_sha256"],
                "go_executable": toolchain["go_executable"],
                "go_sha256": toolchain["go_sha256"],
                "producer_toolchain_identity": toolchain["producer_toolchain_identity"],
                "heads": dict(inputs["heads"]),
                "execution_root": p0["derived_root"]["root"],
                "fact_runs": {
                    run: {
                        "run_id": p0["fact_runs"][run]["run_id"],
                        "root": p0["fact_runs"][run]["path"],
                    }
                    for run in ("run1", "run2")
                },
            }
            plan = (
                f"| 2026-07-21 | {enum.PLAN_P0_AUTHORIZATION_MARKER} | "
                f"**{enum.PLAN_P0_AUTHORIZATION_APPROVAL}.** "
                f"{p0['authorization_id']} {p0_digest} |\n"
            ).encode("utf-8")
            blobs = {
                f"HEAD:{p0_relative}": p0_bytes,
                f"{p0_commit}:{p0_relative}": p0_bytes,
                f"{evidence_commit}:{p0_relative}": p0_bytes,
                f"{p0_commit}:PLAN.md": plan,
            }

            def control(*arguments):
                if arguments[:2] == ("merge-base", "--is-ancestor"):
                    return b""
                return blobs[arguments[1]]

            with (
                mock.patch.object(enum, "P0_AUTHORIZATION_PATH", p0_path),
                mock.patch.object(enum, "P0_AUTHORIZATION_REL", p0_relative),
                mock.patch.object(enum, "_control_git", side_effect=control),
            ):
                enum.verify_p0_authorization_binding(
                    {"p0_authorization": binding},
                    evidence,
                    evidence_commit,
                )
                self.assertFalse((root / "derived").exists())
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        p0_commit,
                    )
                fabricated = dict(binding)
                fabricated["sha256"] = _digest("f")
                fabricated_evidence = dict(evidence)
                fabricated_evidence["p0_authorization"] = dict(fabricated)
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": fabricated},
                        fabricated_evidence,
                        evidence_commit,
                    )
                evidence["derived_lock_sha256"] = _digest("0")
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )
                evidence["derived_lock_sha256"] = p0["operations"]["lock_rewrite"]["derived_lock_sha256"]
                p0["operations"]["lock_rewrite"]["derived_lock_path"] = "/sealed/wrong-lock.json"
                with self.assertRaises(enum.EnumerationError):
                    enum._verify_p0_evidence_projection(p0, evidence)
                p0["operations"]["lock_rewrite"]["derived_lock_path"] = p0["derived_root"]["lock_path"]
                evidence["execution_root"] = "/sealed/other-derived-root"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )

                evidence["execution_root"] = p0["derived_root"]["root"]
                evidence["heads"] = {**inputs["heads"], "temporal": "f" * 40}
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )
                evidence["heads"] = dict(inputs["heads"])
                evidence["fact_runs"] = {
                    **evidence["fact_runs"],
                    "run2": {"run_id": "other-run-0001", "root": "/sealed/other-derived-root/run2"},
                }
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )
                evidence["fact_runs"] = {
                    run: {
                        "run_id": p0["fact_runs"][run]["run_id"],
                        "root": p0["fact_runs"][run]["path"],
                    }
                    for run in ("run1", "run2")
                }
                p0["bootstrap"]["source_t111_sha256"] = _digest("0")
                with self.assertRaises(enum.EnumerationError):
                    enum._verify_p0_evidence_projection(p0, evidence)
                p0["bootstrap"]["source_t111_sha256"] = inputs["t111_binary_sha256"]
                p0["bootstrap"]["derived_t111_sha256"] = _digest("0")
                with self.assertRaises(enum.EnumerationError):
                    enum._verify_p0_evidence_projection(p0, evidence)
                p0["bootstrap"]["derived_t111_sha256"] = inputs["t111_binary_sha256"]
                p0["implementation"]["prebuild_runner_sha256"] = _digest("0")
                with self.assertRaises(enum.EnumerationError):
                    enum._verify_p0_admission_parser(p0, p0_bytes)
                p0["implementation"]["prebuild_runner_sha256"] = enum.sha256_file(
                    enum.PREBUILD_RUNNER_PATH
                )
                p0["scope"]["enumerate_frames"] = True
                p0_path.write_bytes((enum.canonical_json(p0) + "\n").encode("utf-8"))
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )
                p0["scope"] = dict(enum.P0_EXPECTED_SCOPE)
                p0["scope"]["construct_derived_root"] = 1
                p0_path.write_bytes((enum.canonical_json(p0) + "\n").encode("utf-8"))
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )
                p0["scope"] = dict(enum.P0_EXPECTED_SCOPE)
                p0["unexpected"] = "field"
                p0_path.write_bytes((enum.canonical_json(p0) + "\n").encode("utf-8"))
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )
                p0.pop("unexpected")
                p0_path.write_bytes(p0_bytes)
                blobs[f"{p0_commit}:PLAN.md"] = b"| no authorization anchor |\n"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_p0_authorization_binding(
                        {"p0_authorization": binding},
                        evidence,
                        evidence_commit,
                    )

    def test_p0_result_chain_requires_canonical_completed_projected_receipts(self):
        with tempfile.TemporaryDirectory() as tmp:
            fixture = _p0_result_chain_fixture(Path(tmp))
            enum.verify_p0_result_chain(
                fixture["authorization"], fixture["evidence"], fixture["p0"]
            )

            fixture["result"]["hydration"]["loki"]["exit_code"] = 1
            _write_canonical(fixture["paths"]["evidence_receipt"], fixture["result"])
            with self.assertRaises(enum.EnumerationError):
                enum.verify_p0_result_chain(
                    fixture["authorization"], fixture["evidence"], fixture["p0"]
                )

            fixture["result"]["hydration"]["loki"]["exit_code"] = 0
            result_bytes = _write_canonical(fixture["paths"]["evidence_receipt"], fixture["result"])
            fixture["terminal"]["evidence_receipt_sha256"] = enum.sha256_bytes(result_bytes)
            terminal_bytes = _write_canonical(fixture["paths"]["terminal_receipt"], fixture["terminal"])
            fixture["evidence"]["p0_result"] = {
                "consumption_marker_sha256": enum.sha256_file(fixture["paths"]["consumption_marker"]),
                "terminal_receipt_sha256": enum.sha256_bytes(terminal_bytes),
                "evidence_receipt_sha256": enum.sha256_bytes(result_bytes),
            }
            fixture["evidence"]["fact_runs"]["run2"]["run_id"] = "different-run-0001"
            with self.assertRaises(enum.EnumerationError):
                enum.verify_p0_result_chain(
                    fixture["authorization"], fixture["evidence"], fixture["p0"]
                )

            fixture["evidence"]["fact_runs"]["run2"]["run_id"] = fixture["p0"]["fact_runs"]["run2"]["run_id"]
            fixture["terminal"]["status"] = "ABORTED"
            _write_canonical(fixture["paths"]["terminal_receipt"], fixture["terminal"])
            with self.assertRaises(enum.EnumerationError):
                enum.verify_p0_result_chain(
                    fixture["authorization"], fixture["evidence"], fixture["p0"]
                )

    def test_receipt_response_digest_must_match_the_prebuild_bound_authorization(self):
        class FakeSnapshot:
            OID_RE = type("OID", (), {
                "fullmatch": staticmethod(lambda value: bool(enum.re.fullmatch(r"[0-9a-f]{40}", value))),
            })

            @staticmethod
            def load_sealed_inputs():
                return b"query", {"proposed_cutoff": "2026-07-20T16:00:00Z", "fixtures": enum.RECEIPT_FIXTURES}

            sha256_bytes = staticmethod(enum.sha256_bytes)

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            response_path = root / "response.json"
            response_path.write_bytes(b"sealed response\n")
            heads = {
                fixture: {"head_oid": f"{number:040x}"}
                for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
            }
            receipt = {
                "schema": enum.RECEIPT_SCHEMA,
                "status": "ADMITTED",
                "cutoff": "2026-07-20T16:00:00Z",
                "query_sha256": enum.sha256_bytes(b"query"),
                "response_sha256": enum.sha256_file(response_path),
                "heads": heads,
            }
            receipt_path = root / "receipt.json"
            receipt_path.write_text(json.dumps(receipt))
            authorization = {
                "receipt_sha256": enum.sha256_file(receipt_path),
                "response_sha256": receipt["response_sha256"],
                "stage1_snapshot_sha256": _digest("a"),
                "heads": {fixture: heads[fixture]["head_oid"] for fixture in enum.RECEIPT_FIXTURES},
            }
            with (
                mock.patch.object(enum, "STAGE1_RESPONSE_PATH", response_path),
                mock.patch.object(enum, "_read_verified_source", return_value=b"synthetic snapshot"),
                mock.patch.object(enum, "_execute_verified_source", return_value=FakeSnapshot),
            ):
                enum.verify_receipt(receipt_path, authorization)
                authorization["response_sha256"] = _digest("b")
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_receipt(receipt_path, authorization)

    def test_reviewed_binding_requires_exact_accepted_ancestor_bytes(self):
        commit = "a" * 40
        enum_bytes = b"enumerator bytes\n"
        snapshot_bytes = b"snapshot bytes\n"
        review_bytes = b"# Review\n\n**Verdict: ACCEPT.**\n"
        review_digest = enum.sha256_bytes(review_bytes)
        plan_bytes = (
            f"| 2026-07-21 | {enum.PLAN_IMPLEMENTATION_MARKER} | "
            f"**{enum.PLAN_IMPLEMENTATION_APPROVAL}.** {review_digest} |\n"
        ).encode("utf-8")
        authorization = {
            "enumerator_sha256": enum.sha256_bytes(enum_bytes),
            "stage1_snapshot_sha256": enum.sha256_bytes(snapshot_bytes),
            "review": {
                "status": "accepted", "accepted_commit": commit,
                "record_sha256": review_digest,
            },
            "binding": {"status": "executable", "commit": commit},
        }
        blobs = {
            f"{commit}:{enum.ENUMERATOR_REL}": enum_bytes,
            f"{commit}:{enum.STAGE1_SNAPSHOT_REL}": snapshot_bytes,
            f"{commit}:{enum.ENUMERATION_REVIEW_REL}": review_bytes,
            f"{commit}:PLAN.md": plan_bytes,
        }

        def control(*args):
            if args[:2] == ("merge-base", "--is-ancestor"):
                return b""
            return blobs[args[1]]

        with mock.patch.object(enum, "_control_git", side_effect=control):
            enum.verify_reviewed_binding(authorization)
            blobs[f"{commit}:{enum.ENUMERATOR_REL}"] = b"wrong bytes\n"
            with self.assertRaises(enum.EnumerationError):
                enum.verify_reviewed_binding(authorization)

    def test_environment_is_exactly_bound_before_use(self):
        git = Path("/tools/git")
        go = Path("/tools/go")
        variables = enum._sealed_environment(git, go)
        authorization = {"environment": {"network": "disabled", "variables": variables}}
        with (
            mock.patch.object(enum, "_authorized_executable", side_effect=(git, go)),
            mock.patch.dict(os.environ, {"LD_PRELOAD": "/untrusted/lib.so"}, clear=True),
        ):
            tools = enum.configure_sealed_execution_environment(authorization)
            self.assertEqual(os.environ, variables)
            self.assertEqual(tools, {"git": git.resolve(), "go": go.resolve()})
        authorization["environment"]["variables"] = {**variables, "PATH": "/wrong"}
        with mock.patch.object(enum, "_authorized_executable", side_effect=(git, go)):
            with self.assertRaises(enum.EnumerationError):
                enum.configure_sealed_execution_environment(authorization)

    def test_prior_ceremony_registry_blocks_id_and_namespace_reuse(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            ceremony_root = root / "ceremony"
            ceremony_root.mkdir(); ceremony_root.chmod(0o700)
            prior = ceremony_root / "estimator"
            attempt = ceremony_root / "attempt-3"
            current = root / "new-ceremony"
            prior.mkdir(); attempt.mkdir(); current.mkdir()
            prior.chmod(0o700); attempt.chmod(0o700); current.chmod(0o700)
            prior_value = {
                "schema": "t111-estimator-authorization-v1",
                "authorization_id": "t111-estimator-auth-1",
                "state": {
                    "admission_receipt": str(prior / "admission.json"),
                    "consumption_marker": str(prior / "consumed.json"),
                    "result_receipt": str(prior / "result.json"),
                },
                "inputs": {
                    "label_publication_receipt": str(attempt / "label-github-receipt.json"),
                },
            }
            prior_file = root / "prior-authorization.json"
            prior_raw = enum.canonical_json(prior_value).encode("utf-8")
            prior_file.write_bytes(prior_raw)
            authorization = {
                "authorization_id": "stage2-enumeration-auth-1",
                "prior_authorizations": {"estimator": {
                    "path": "prior-authorization.json", "sha256": enum.sha256_bytes(prior_raw),
                }},
                "state": {
                    "ceremony_directory": str(current),
                    "output_dir": str(current / "output"),
                    "consumption_marker": str(current / "consumed.json"),
                    "terminal_receipt": str(current / "terminal.json"),
                },
            }
            with (
                mock.patch.object(enum, "ESTIMATOR_AUTHORIZATION_PATH", prior_file),
                mock.patch.object(enum, "ESTIMATOR_AUTHORIZATION_REL", "prior-authorization.json"),
                mock.patch.object(enum, "_head_blob", return_value=prior_raw),
            ):
                enum.verify_authorized_state_namespaces(authorization)
                # A deleted prior ceremony remains a reserved canonical
                # namespace; it must not make a genuinely disjoint new
                # ceremony fail admission.
                attempt.rmdir()
                enum.verify_authorized_state_namespaces(authorization)
                attempt.mkdir(); attempt.chmod(0o700)
                authorization["authorization_id"] = "t111-estimator-auth-1"
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_authorized_state_namespaces(authorization)
                authorization["authorization_id"] = "stage2-enumeration-auth-1"
                for namespace in (prior, attempt):
                    overlap = namespace / "nested"
                    overlap.mkdir(); overlap.chmod(0o700)
                    authorization["state"] = {
                        "ceremony_directory": str(overlap),
                        "output_dir": str(overlap / "output"),
                        "consumption_marker": str(overlap / "consumed.json"),
                        "terminal_receipt": str(overlap / "terminal.json"),
                    }
                    with self.assertRaises(enum.EnumerationError):
                        enum.verify_authorized_state_namespaces(authorization)

    def test_state_writes_refuse_when_no_follow_is_unavailable(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp).resolve() / "output"
            authorization = {"state": {
                "output_dir": str(output),
                "consumption_marker": str(Path(tmp).resolve() / "consumed.json"),
                "terminal_receipt": str(Path(tmp).resolve() / "terminal.json"),
            }}
            with mock.patch.object(enum.os, "O_NOFOLLOW", None):
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_authorized_output(output, authorization)

    def test_committed_authorization_binds_the_initially_parsed_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            auth_file = root / "authorization.json"
            auth_file.write_text('{"authorization_id":"committed"}\n')
            swapped_bytes = b'{"authorization_id":"swapped"}\n'
            with (
                mock.patch.object(enum, "AUTHORIZATION_PATH", auth_file),
                mock.patch.object(enum, "AUTHORIZATION_REL", "authorization.json"),
                mock.patch.object(enum, "_control_git", return_value=b""),
                mock.patch.object(enum, "_require_clean_tracked_worktree"),
                mock.patch.object(enum, "_head_blob", return_value=auth_file.read_bytes()),
            ):
                with self.assertRaises(enum.EnumerationError):
                    enum.verify_committed_authorization(
                        {"authorization_id": "swapped"}, swapped_bytes
                    )

    def test_oracle_environment_guard_keeps_hardened_git_controls(self):
        class FakeModule:
            @staticmethod
            def oracle_subprocess_env(toolchain, tool_dir, cache_dir):
                return {
                    "PATH": str(tool_dir),
                    "GIT_OPTIONAL_LOCKS": "0",
                    "LD_PRELOAD": "/untrusted/lib.so",
                    "DYLD_INSERT_LIBRARIES": "/untrusted/lib.dylib",
                }

        module = FakeModule()
        enum.install_oracle_environment_guard(module)
        environment = module.oracle_subprocess_env({}, Path("/tools"), Path("/cache"))
        self.assertEqual(environment["PATH"], "/tools")
        self.assertEqual(environment["GIT_NO_REPLACE_OBJECTS"], "1")
        self.assertEqual(environment["GIT_NO_LAZY_FETCH"], "1")
        self.assertEqual(environment["GIT_CONFIG_GLOBAL"], os.devnull)
        self.assertEqual(environment["GIT_CONFIG_VALUE_1"], "false")
        self.assertNotIn("LD_PRELOAD", environment)
        self.assertNotIn("DYLD_INSERT_LIBRARIES", environment)

    def test_verified_loader_uses_source_bytes_not_cache_or_path_shadow(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            module_dir = root / "spike" / "t111"
            module_dir.mkdir(parents=True)
            sources = {
                "gate34_common.py": "COMMON = 'common'\n",
                "label_protocol.py": "PROTOCOL = 'protocol'\n",
                "reviewer_kits.py": "KITS = 'kits'\n",
                "label_adjudication.py": "import label_protocol\nimport reviewer_kits\nADJ = 'adjudication'\n",
                "label_prep.py": (
                    "import subprocess\nfrom gate34_common import COMMON\n"
                    "from label_adjudication import ADJ\nMARKER = 'source'\n"
                    "SHADOW_PATH = getattr(subprocess, '__file__', '')\n"
                ),
            }
            for name, source in sources.items():
                (module_dir / name).write_text(source)
            (module_dir / "subprocess.py").write_text("raise RuntimeError('path shadow used')\n")
            prep = module_dir / "label_prep.py"
            pycache = module_dir / "__pycache__"
            pycache.mkdir()
            forged = importlib._bootstrap_external._code_to_hash_pyc(
                compile("MARKER = 'forged-pyc'\n", str(prep), "exec"),
                importlib.util.source_hash(prep.read_bytes()),
                checked=False,
            )
            (pycache / f"label_prep.{sys.implementation.cache_tag}.pyc").write_bytes(forged)
            inventory = {
                f"spike/t111/{name}": enum.sha256_file(module_dir / name)
                for name in sources
            }
            try:
                loaded = enum.load_execution_label_prep(root, inventory)
                self.assertEqual(loaded.MARKER, "source")
                self.assertFalse(str(loaded.SHADOW_PATH).startswith(str(module_dir)))
            finally:
                for name in ("gate34_common", "label_protocol", "reviewer_kits", "label_adjudication", "label_prep"):
                    sys.modules.pop(name, None)

    def test_failed_publication_removes_its_private_staging_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp).resolve() / "enumeration"
            outputs = {
                "cardinalities.json": {},
                "frame-membership.json": {},
                "enumeration-receipt.json": {},
            }
            with mock.patch.object(
                enum,
                "_rename_directory_no_replace",
                side_effect=enum.EnumerationError("rename blocked"),
            ):
                with self.assertRaises(enum.EnumerationError):
                    enum.publish_outputs(out, outputs)
            self.assertFalse(out.exists())
            self.assertFalse((Path(tmp).resolve() / ".enumeration.tmp").exists())

    def test_atomic_no_replace_publish_preserves_an_intervening_output_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            out = root / "enumeration"
            out.mkdir()
            sentinel = out / "operator-state"
            sentinel.write_text("do not replace\n")
            outputs = {
                "cardinalities.json": {},
                "frame-membership.json": {},
                "enumeration-receipt.json": {},
            }
            # Bypass only the advisory pre-check so the macOS no-replace
            # primitive itself is exercised as the final race boundary.
            with mock.patch.object(enum, "_state_target_exists", return_value=False):
                with self.assertRaises(enum.EnumerationError):
                    enum.publish_outputs(out, outputs)
            self.assertEqual(sentinel.read_text(), "do not replace\n")
            self.assertFalse((root / ".enumeration.tmp").exists())

    def test_atomic_no_replace_publish_emits_the_complete_synthetic_set(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            out = root / "enumeration"
            outputs = {
                "cardinalities.json": {"client_call_precision": {"population": 0, "census": 0}},
                "frame-membership.json": {"client_call_precision": []},
                "enumeration-receipt.json": {"schema": "synthetic"},
            }
            digests = enum.publish_outputs(out, outputs)
            self.assertEqual(set(digests), set(outputs))
            self.assertTrue(all((out / name).is_file() for name in outputs))
            self.assertFalse((root / ".enumeration.tmp").exists())

    def test_raw_enumeration_runs_typed_oracle_twice_and_never_selects(self):
        lp = FakeLabelPrep()
        entries = {
            fixture: {"commit": f"{number:040x}", "goos": "darwin", "goarch": "arm64", "go_tests": "include"}
            for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
        }
        facts = {fixture: Path("/synthetic") / f"{fixture}.facts.jsonl" for fixture in enum.RECEIPT_FIXTURES}
        frames, proofs = enum.enumerate_raw_frames(
            lp,
            entries=entries,
            corpus_dir=Path("/synthetic/corpus"),
            facts=facts,
            expected_toolchain_identity="exact-tools",
            authorized_tools=self._synthetic_tools(),
        )
        self.assertEqual(lp.typed_calls, len(enum.RECEIPT_FIXTURES) * 2)
        self.assertEqual(set(frames), set(enum.EXTERNAL_FRAMES))
        self.assertEqual(len(frames["client_call_recall"]), len(enum.RECEIPT_FIXTURES))
        self.assertEqual(set(proofs), set(enum.RECEIPT_FIXTURES))

    def test_raw_enumeration_refuses_nonreproducible_typed_oracle(self):
        lp = FakeLabelPrep(unstable=True)
        entries = {
            fixture: {"commit": f"{number:040x}", "goos": "darwin", "goarch": "arm64", "go_tests": "include"}
            for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
        }
        facts = {fixture: Path("/synthetic") / f"{fixture}.facts.jsonl" for fixture in enum.RECEIPT_FIXTURES}
        with self.assertRaises(enum.EnumerationError):
            enum.enumerate_raw_frames(
                lp,
                entries=entries,
                corpus_dir=Path("/synthetic/corpus"),
                facts=facts,
                expected_toolchain_identity="exact-tools",
                authorized_tools=self._synthetic_tools(),
            )

    def test_raw_enumeration_refuses_an_unauthorized_fact_toolchain(self):
        lp = FakeLabelPrep()
        entries = {
            fixture: {"commit": f"{number:040x}", "goos": "darwin", "goarch": "arm64", "go_tests": "include"}
            for number, fixture in enumerate(enum.RECEIPT_FIXTURES, 1)
        }
        facts = {fixture: Path("/synthetic") / f"{fixture}.facts.jsonl" for fixture in enum.RECEIPT_FIXTURES}
        with self.assertRaises(enum.EnumerationError):
            enum.enumerate_raw_frames(
                lp,
                entries=entries,
                corpus_dir=Path("/synthetic/corpus"),
                facts=facts,
                expected_toolchain_identity="different-tools",
                authorized_tools=self._synthetic_tools(),
            )

    def test_prebuild_evidence_refusal_precedes_consumption_and_derived_root_io(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            digest = _digest("a")
            authorization = {"enumerator_sha256": digest}
            args = type("Args", (), {
                "out": str(root / "out"),
                "receipt": str(root / "receipt.json"),
                "execution_root": str(root / "derived-root"),
                "facts_run1": str(root / "run1"),
                "facts_run2": str(root / "run2"),
            })()
            with (
                mock.patch.object(enum, "load_authorization", return_value=(authorization, b"{}\n")),
                mock.patch.object(enum, "load_prebuild_evidence", return_value=({}, b"{}\n")),
                mock.patch.object(enum, "verify_authorized_toolchain_identity"),
                mock.patch.object(enum, "sha256_file", return_value=digest),
                mock.patch.object(enum, "verify_committed_authorization", return_value=digest),
                mock.patch.object(
                    enum, "verify_prebuild_evidence",
                    side_effect=enum.EnumerationError("prebuild evidence missing"),
                ),
                mock.patch.object(
                    enum, "_consume_authorization",
                    side_effect=AssertionError("marker transition reached"),
                ),
                mock.patch.object(
                    enum, "load_execution_label_prep",
                    side_effect=AssertionError("derived root reached"),
                ),
                mock.patch.object(Path, "lstat", side_effect=AssertionError("derived metadata reached")),
                mock.patch.object(Path, "resolve", side_effect=AssertionError("derived resolution reached")),
                mock.patch.object(enum, "within", side_effect=AssertionError("derived containment reached")),
            ):
                with self.assertRaises(enum.EnumerationError):
                    enum.run(args)
            self.assertFalse((root / "out").exists())

    def test_missing_authorization_stops_before_the_derived_root_is_loaded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = type("Args", (), {
                "out": str(root / "out"),
                "receipt": str(root / "receipt.json"),
                "execution_root": str(root / "derived-root"),
                "facts_run1": str(root / "run1"),
                "facts_run2": str(root / "run2"),
            })()
            with mock.patch.object(enum, "load_execution_label_prep", side_effect=AssertionError("source read")):
                with self.assertRaises(enum.EnumerationError):
                    enum.run(args)
            self.assertFalse((root / "out").exists())


if __name__ == "__main__":
    unittest.main()
