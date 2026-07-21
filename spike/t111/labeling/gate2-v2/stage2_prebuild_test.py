"""Synthetic-only tests for the Stage-2 P0 admission parser."""

import tempfile
import unittest
from pathlib import Path

import sys

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import stage2_prebuild as prebuild  # noqa: E402


def digest(character: str) -> str:
    return "sha256:" + character * 64


def p0(root: Path) -> dict:
    heads = {fixture: f"{index:040x}" for index, fixture in enumerate(prebuild.RECEIPT_FIXTURES, 1)}
    commands = {fixture: {"subcommand": "extract", "system": fixture} for fixture in prebuild.RECEIPT_FIXTURES}
    derived = root / "derived"
    source_repo = prebuild.REPO_ROOT
    cache_path = derived / "spike" / "t111" / ".module-cache"
    out_path = derived / "spike" / "t111" / "out"
    facts_root = derived / "spike" / "t111" / "stage2-facts"
    run_paths = {"run1": facts_root / "run1", "run2": facts_root / "run2"}
    run_receipts = {run: run_paths[run] / "fact-run-receipt.json" for run in run_paths}
    ceremony = root / "ceremony"
    goroot = root / "toolchain" / "goroot"
    git = Path("/tmp/git")
    go = goroot / "bin" / "go"
    go_digest = digest("e")
    git_digest = digest("d")
    variables = {
        phase: prebuild.sealed_phase_environment(
            go_executable=go,
            git_executable=git,
            scratch_root=ceremony / "scratch" / phase,
        )
        for phase in ("hydration", "offline")
    }
    source_t111 = str(source_repo / "spike" / "t111" / ".module-cache" / "bin" / "t111")
    derived_t111 = str(cache_path / "bin" / "t111")
    normalization = {
        "core_hooks_path": "/dev/null",
        "core_fsmonitor": "false",
        "core_use_builtin_fsmonitor": "false",
        "core_symlinks": "false",
        "remove_origin": True,
        "forbid_alternates": True,
        "before_checkout": True,
    }
    # The derived source must be the exact accepted implementation revision,
    # while the control repository may later advance to commit the P0 record.
    source_commit = "a" * 40

    def publication(run: str) -> dict:
        return {
            "source_root": str(out_path),
            "destination_root": str(run_paths[run]),
            "reset_before_run": {
                "out_path": str(out_path),
                "destination_path": str(run_paths[run]),
            },
            "copy_mode": "copy-regular-files",
            "hardlink_policy": "forbid",
            "reuse_policy": "forbid",
            "files": {
                fixture: {
                    "source_path": str(out_path / f"{fixture}.facts.jsonl"),
                    "destination_path": str(run_paths[run] / f"{fixture}.facts.jsonl"),
                }
                for fixture in prebuild.RECEIPT_FIXTURES
            },
        }

    extraction = {
        run: {
            "argv": {
                fixture: [derived_t111, "extract", "-system", fixture]
                for fixture in prebuild.RECEIPT_FIXTURES
            },
            "cwd": str(derived),
            "capture": {
                "stdout_path": str(run_paths[run] / "extract.stdout"),
                "stderr_path": str(run_paths[run] / "extract.stderr"),
            },
        }
        for run in ("run1", "run2")
    }
    fact_runs = {
        run: {
            "run_id": f"prebuild-{run}-0001",
            "path": str(run_paths[run]),
            "receipt_path": str(run_receipts[run]),
            "commands": commands,
            "publication": publication(run),
        }
        for run in ("run1", "run2")
    }
    state = {
        "ceremony_directory": str(ceremony),
        "consumption_marker": str(ceremony / "consumed.json"),
        "terminal_receipt": str(ceremony / "terminal.json"),
        "evidence_receipt": str(ceremony / "evidence.json"),
    }
    projection_runs = {
        run: {
            "run_id": fact_runs[run]["run_id"],
            "root": str(run_paths[run]),
            "receipt_path": str(run_receipts[run]),
        }
        for run in ("run1", "run2")
    }
    authorization_id = "stage2-prebuild-auth-0001"
    inputs = {
        **{key: digest("b") for key in prebuild.INPUT_FIELDS - {"heads"}},
        "heads": heads,
        "derived_lock_sha256": digest("d"),
    }
    lock_rewrite = {
        "base_lock_path": str(source_repo / "spike" / "t111" / "corpus.lock.json"),
        "base_lock_access": "read-only",
        "base_lock_sha256": inputs["base_lock_sha256"],
        "derived_lock_path": str(derived / "spike" / "t111" / "corpus.lock.json"),
        "derived_lock_sha256": digest("d"),
        "serialization": {
            "encoding": "utf-8",
            "indent": 2,
            "ensure_ascii": False,
            "sort_keys": False,
            "newline": "LF",
            "trailing_newline": True,
        },
        "transform": {
            "kind": "replace-fixture-commit-only",
            "fixtures": {
                fixture: {
                    "field": "commit",
                    "source_commit": prebuild.BASE_LOCK_FIXTURE_COMMITS[fixture],
                    "target_commit": heads[fixture],
                }
                for fixture in prebuild.RECEIPT_FIXTURES
            },
            "preserve_array_order": True,
            "preserve_object_key_order": True,
            "preserve_non_commit_fields": True,
            "reject_missing_fixtures": True,
            "reject_duplicate_fixtures": True,
            "reject_extra_fixtures": True,
        },
    }
    fixture_finalization = {
        "order": list(prebuild.RECEIPT_FIXTURES),
        "fixtures": {
            fixture: {
                "repo_path": str(derived / "spike" / "t111" / "corpus" / fixture),
                "config_path": str(derived / "spike" / "t111" / "corpus" / fixture / ".git" / "config"),
                "mode": "minimal-core-only",
                "encoding": "ascii",
                "newline": "LF",
                "trailing_newline": True,
                "only_core": True,
                "core_values": {
                    "repositoryformatversion": "0",
                    "symlinks": "false",
                },
                "remove_origin": True,
                "forbid_alternates": True,
                "forbid_hooks": True,
                "forbid_fsmonitor": True,
                "forbid_bare": True,
                "forbid_logallrefupdates": True,
                "after_hydration": True,
                "before_extract": True,
            }
            for fixture in prebuild.RECEIPT_FIXTURES
        },
    }
    return {
        "schema": prebuild.AUTH_SCHEMA,
        "status": "AUTHORIZED",
        "authorization_id": authorization_id,
        "implementation": {key: digest("a") for key in prebuild.IMPLEMENTATION_FIELDS},
        "bootstrap": {
            "source_t111_path": source_t111,
            "source_t111_sha256": inputs["t111_binary_sha256"],
            "source_t111_access": "read-only",
            "derived_t111_path": derived_t111,
            "derived_t111_sha256": inputs["t111_binary_sha256"],
            "binary_copy_policy": "forbid",
        },
        "inputs": inputs,
        "prior_authorizations": {
            "estimator": {
                "path": prebuild.ESTIMATOR_AUTHORIZATION_REL,
                "sha256": inputs["estimator_authorization_sha256"],
            }
        },
        "toolchain": {
            "python_executable": "/tmp/python3",
            "python_version": "3.9.6",
            "python_sha256": digest("c"),
            "git_executable": str(git),
            "git_sha256": git_digest,
            "go_executable": str(go),
            "go_sha256": go_digest,
            "producer_toolchain_identity": (
                f'go_version="go";go_digest={go_digest};git_version="git";git_digest={git_digest}'
            ),
        },
        "environment": {
            "hydration": {
                "proxy": "https://proxy.golang.org",
                "sumdb": "sum.golang.org",
                "scratch_root": str(ceremony / "scratch" / "hydration"),
                "variables": variables["hydration"],
            },
            "offline": {
                "proxy": "off",
                "sumdb": "off",
                "scratch_root": str(ceremony / "scratch" / "offline"),
                "variables": variables["offline"],
            },
        },
        "derived_root": {
            "root": str(derived),
            "lock_path": str(derived / "spike" / "t111" / "corpus.lock.json"),
            "cache_path": str(cache_path),
            "corpus_path": str(derived / "spike" / "t111" / "corpus"),
            "out_path": str(out_path),
            "facts_root": str(facts_root),
        },
        "fact_runs": fact_runs,
        "operations": {
            "order": list(prebuild.OPERATION_ORDER),
            "source_clone": {
                "source_repo_path": str(source_repo),
                "source_commit": source_commit,
                "destination_path": str(derived),
                "sequence": list(prebuild.SOURCE_CLONE_SEQUENCE),
                "clone_argv": [
                    str(git),
                    "clone",
                    "--no-checkout",
                    "--no-local",
                    "--no-hardlinks",
                    "--no-recurse-submodules",
                    "--no-tags",
                    "--no-single-branch",
                    str(source_repo),
                    str(derived),
                ],
                "normalization": normalization,
                "checkout_argv": [str(git), "-C", str(derived), "checkout", "--detach", source_commit],
            },
            "lock_rewrite": lock_rewrite,
            "corpus": {
                "order": list(prebuild.RECEIPT_FIXTURES),
                "fixtures": {
                    fixture: {
                        "source_repo_path": str(source_repo / "spike" / "t111" / "corpus" / fixture),
                        "source_commit": heads[fixture],
                        "destination_path": str(derived / "spike" / "t111" / "corpus" / fixture),
                        "bundle_path": str(root / "ceremony" / "bundles" / f"{fixture}.bundle"),
                        "sequence": list(prebuild.CORPUS_FIXTURE_SEQUENCE),
                        "bundle_argv": [
                            str(git),
                            "-C", str(source_repo / "spike" / "t111" / "corpus" / fixture),
                            "bundle", "create", str(root / "ceremony" / "bundles" / f"{fixture}.bundle"),
                            heads[fixture],
                        ],
                        "init_argv": [
                            str(git), "init", "--quiet", str(derived / "spike" / "t111" / "corpus" / fixture),
                        ],
                        "normalization": normalization,
                        "fetch_argv": [
                            str(git), "-C", str(derived / "spike" / "t111" / "corpus" / fixture),
                            "fetch", "--no-tags", str(root / "ceremony" / "bundles" / f"{fixture}.bundle"),
                            f"{heads[fixture]}:refs/gate2-v2/{fixture}",
                        ],
                        "checkout_argv": [
                            str(git), "-C", str(derived / "spike" / "t111" / "corpus" / fixture),
                            "checkout", "--detach", heads[fixture],
                        ],
                    }
                    for fixture in prebuild.RECEIPT_FIXTURES
                },
            },
            "cache": {
                "path": str(cache_path),
                "mode": "fresh-empty",
                "copy_from": None,
                "reset_before_hydration": True,
                "hardlink_policy": "forbid",
                "reuse_policy": "forbid",
            },
            "hydrate": {
                "order": list(prebuild.RECEIPT_FIXTURES),
                "commands": {
                    fixture: {
                        "argv": [
                            source_t111, "hydrate", "-system", fixture,
                            "-proxy", "https://proxy.golang.org",
                            "-sumdb", "sum.golang.org",
                        ],
                        "cwd": str(derived),
                        "capture": {
                            "stdout_path": str(root / "ceremony" / "hydrate" / fixture / "stdout"),
                            "stderr_path": str(root / "ceremony" / "hydrate" / fixture / "stderr"),
                        },
                    }
                    for fixture in prebuild.RECEIPT_FIXTURES
                },
            },
            "fixture_finalize": fixture_finalization,
            "extract": extraction,
        },
        "state": state,
        "record_projections": {
            "evidence": {
                "schema": prebuild.PREBUILD_EVIDENCE_RECEIPT_SCHEMA,
                "status": "COMPLETE",
                "prebuild_id": authorization_id,
                "authorization_schema": prebuild.AUTH_SCHEMA,
                "authorization_id": authorization_id,
                "record_path": state["evidence_receipt"],
                "execution_root": str(derived),
                "fact_runs": projection_runs,
            },
            "terminal": {
                "schema": prebuild.PREBUILD_TERMINAL_SCHEMA,
                "prebuild_id": authorization_id,
                "authorization_schema": prebuild.AUTH_SCHEMA,
                "authorization_id": authorization_id,
                "record_path": state["terminal_receipt"],
                "execution_root": str(derived),
                "fact_runs": projection_runs,
            },
        },
        "scope": dict(prebuild.EXPECTED_SCOPE),
        "implementation_review": {"status": "accepted", "accepted_commit": "a" * 40, "record_sha256": digest("f")},
        "implementation_binding": {"status": "executable", "commit": "a" * 40},
    }


class PrebuildAuthorizationTests(unittest.TestCase):
    def write(self, root: Path, value: dict) -> Path:
        path = root / "p0.json"
        path.write_text(prebuild.canonical_json(value) + "\n")
        return path

    def test_missing_p0_refuses_without_derived_or_source_input_access(self):
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(Path(temporary) / "missing.json")

    def test_canonical_p0_parses_without_creating_or_reading_derived_root(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            path = self.write(root, value)
            loaded, raw = prebuild.load_authorization(path)
            self.assertEqual(loaded, value)
            self.assertEqual(raw, path.read_bytes())
            self.assertFalse((root / "derived").exists())
            self.assertFalse((root / "source").exists())

    def test_rejects_noncanonical_json(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            path = root / "p0.json"
            path.write_text("{}\n")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(path)

    def test_rejects_scope_expansion(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["scope"]["enumerate_frames"] = True
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["scope"]["construct_derived_root"] = 1
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_head_drift_and_unsafe_fact_run_path(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["inputs"]["heads"]["loki"] = "bad"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["fact_runs"]["run2"]["path"] = str(root / "elsewhere" / "run2")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_network_policy_widening_and_duplicate_run_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["environment"]["hydration"]["proxy"] = "https://bad.example"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["environment"]["offline"]["proxy"] = "https://proxy.golang.org"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["hydrate"]["commands"]["loki"]["argv"][3] = "wrong-system"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["fact_runs"]["run2"]["run_id"] = value["fact_runs"]["run1"]["run_id"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_overlapping_fixed_topology_and_terminal_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["derived_root"]["cache_path"] = value["derived_root"]["facts_root"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["state"]["terminal_receipt"] = value["state"]["consumption_marker"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["state"]["ceremony_directory"] = value["derived_root"]["root"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["source_clone"]["source_repo_path"] = value["derived_root"]["root"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["source_clone"]["source_repo_path"] = str(root / "external-source")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["state"]["consumption_marker"] = (
                value["operations"]["hydrate"]["commands"]["temporal"]["capture"]["stdout_path"]
            )
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_hardcoded_output_bootstrap_and_publication_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["derived_root"]["out_path"] = str(root / "derived" / "out")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["bootstrap"]["source_t111_path"] = str(root / "bootstrap" / "t111")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["bootstrap"]["derived_t111_path"] = str(root / "bootstrap" / "t111")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["bootstrap"]["source_t111_sha256"] = digest("0")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["bootstrap"]["derived_t111_sha256"] = digest("0")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["fact_runs"]["run2"]["publication"]["reset_before_run"]["out_path"] = str(
                root / "other-out"
            )
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["fact_runs"]["run2"]["publication"]["hardlink_policy"] = "allow"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["fact_runs"]["run2"]["publication"]["files"]["loki"]["source_path"] = (
                value["derived_root"]["out_path"] + "/wrong.facts.jsonl"
            )
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["fact_runs"]["run2"]["receipt_path"] = value["fact_runs"]["run1"]["receipt_path"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_source_bootstrap_phase_misuse_and_copy_policy_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            derived_t111 = value["bootstrap"]["derived_t111_path"]
            value["operations"]["hydrate"]["commands"]["temporal"]["argv"][0] = derived_t111
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            source_t111 = value["bootstrap"]["source_t111_path"]
            value["operations"]["extract"]["run1"]["argv"]["temporal"][0] = source_t111
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["bootstrap"]["source_t111_access"] = "read-write"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["bootstrap"]["binary_copy_policy"] = "allow"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_nul_and_control_characters_in_bound_text(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["derived_root"]["root"] += "\x00"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["hydrate"]["commands"]["temporal"]["argv"][0] += "\x00"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["source_clone"]["clone_argv"][0] += "\t"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["toolchain"]["python_version"] += "\x7f"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_clone_bundle_and_fresh_cache_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["prior_authorizations"]["estimator"]["sha256"] = digest("0")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["source_clone"]["source_commit"] = "b" * 40
            value["operations"]["source_clone"]["checkout_argv"][-1] = "b" * 40
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["source_clone"]["clone_argv"][3] = "--local"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["source_clone"]["normalization"]["core_symlinks"] = "true"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["corpus"]["fixtures"]["loki"]["source_commit"] = "a" * 40
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["corpus"]["fixtures"]["loki"]["sequence"] = [
                "bundle", "init", "fetch", "checkout", "normalize",
            ]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["corpus"]["fixtures"]["loki"]["fetch_argv"][-1] = "HEAD"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["cache"]["copy_from"] = str(root / "base-cache")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["cache"]["reuse_policy"] = "allow"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_lock_rewrite_digest_path_serialization_and_transform_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["operations"]["lock_rewrite"]["base_lock_sha256"] = digest("0")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["derived_lock_sha256"] = value["inputs"]["base_lock_sha256"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["derived_lock_sha256"] = digest("e")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            del value["operations"]["lock_rewrite"]["derived_lock_sha256"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["derived_lock_path"] = str(
                root / "derived" / "spike" / "t111" / "other.lock.json"
            )
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["serialization"]["indent"] = 4
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["serialization"]["ensure_ascii"] = True
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["serialization"]["newline"] = "CRLF"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["serialization"]["trailing_newline"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            del value["operations"]["lock_rewrite"]["transform"]["fixtures"]["loki"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["transform"]["fixtures"]["extra"] = {
                "field": "commit",
                "source_commit": "a" * 40,
                "target_commit": "b" * 40,
            }
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["transform"]["fixtures"]["loki"]["field"] = "git_url"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["transform"]["fixtures"]["loki"]["source_commit"] = "a" * 40
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["transform"]["fixtures"]["loki"]["target_commit"] = "a" * 40
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["operations"]["lock_rewrite"]["transform"]["preserve_non_commit_fields"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_fixture_finalization_config_closure_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            fixture = "loki"
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["config_path"] = str(root / "derived" / "other.config")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["encoding"] = "utf-8"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["core_values"]["symlinks"] = "true"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["core_values"]["hooksPath"] = "/dev/null"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["remove_origin"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["forbid_alternates"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["forbid_hooks"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["forbid_fsmonitor"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["forbid_bare"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["forbid_logallrefupdates"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["after_hydration"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            plan = value["operations"]["fixture_finalize"]["fixtures"][fixture]
            plan["before_extract"] = False
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_record_projection_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["record_projections"]["evidence"]["authorization_id"] = "other"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["record_projections"]["terminal"]["record_path"] = str(root / "ceremony" / "other.json")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["record_projections"]["evidence"]["fact_runs"]["run1"]["receipt_path"] = str(
                root / "other.json"
            )
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_default_hydration_and_nonmatching_goroot(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["operations"]["hydrate"]["commands"] = {}
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_missing_executor_binding_or_ambient_scratch_environment(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            del value["implementation"]["prebuild_executor_sha256"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["environment"]["hydration"]["variables"]["TMPDIR"] = "/tmp"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["environment"]["offline"]["scratch_root"] = value["environment"]["hydration"]["scratch_root"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["state"]["terminal_receipt"] = str(root / "ceremony" / "nested" / "terminal.json")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["environment"]["offline"]["variables"]["GOROOT"] = str(root / "wrong")
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))

    def test_rejects_path_shadowing_and_toolchain_identity_digest_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            value = p0(root)
            value["environment"]["offline"]["variables"]["PATH"] = "/shadow:/usr/bin:/bin"
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["toolchain"]["producer_toolchain_identity"] = (
                f'go_version="go";go_digest={digest("0")};git_version="git";git_digest={digest("d")}'
            )
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))


if __name__ == "__main__":
    unittest.main()
