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
    facts_root = derived / "spike" / "t111" / "stage2-facts"
    run_paths = {"run1": facts_root / "run1", "run2": facts_root / "run2"}
    goroot = root / "toolchain" / "goroot"
    git = Path("/tmp/git")
    go = goroot / "bin" / "go"
    go_digest = digest("e")
    git_digest = digest("d")
    variables = {
        "PATH": prebuild.sealed_path(go, git),
        "GOROOT": str(goroot),
        **prebuild.STATIC_ENVIRONMENT,
    }
    t111 = str(root / "bootstrap" / "t111")
    extraction = {
        run: {
            "argv": {
                fixture: [t111, "extract", "-system", fixture]
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
    return {
        "schema": prebuild.AUTH_SCHEMA,
        "status": "AUTHORIZED",
        "authorization_id": "stage2-prebuild-auth-0001",
        "implementation": {key: digest("a") for key in prebuild.IMPLEMENTATION_FIELDS},
        "bootstrap": {"t111_path": t111, "t111_sha256": digest("a")},
        "inputs": {
            **{key: digest("b") for key in prebuild.INPUT_FIELDS - {"heads"}},
            "heads": heads,
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
                "variables": variables,
            },
            "offline": {"proxy": "off", "sumdb": "off", "variables": variables},
        },
        "derived_root": {
            "root": str(derived),
            "lock_path": str(derived / "spike" / "t111" / "corpus.lock.json"),
            "cache_path": str(derived / "spike" / "t111" / ".module-cache"),
            "corpus_path": str(derived / "spike" / "t111" / "corpus"),
            "facts_root": str(facts_root),
        },
        "fact_runs": {
            "run1": {"run_id": "prebuild-run-0001", "path": str(run_paths["run1"]), "commands": commands},
            "run2": {"run_id": "prebuild-run-0002", "path": str(run_paths["run2"]), "commands": commands},
        },
        "operations": {
            "hydrate": {
                "order": list(prebuild.RECEIPT_FIXTURES),
                "commands": {
                    fixture: {
                        "argv": [
                            t111, "hydrate", "-system", fixture,
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
                "cow": {
                    "mode": "copy-on-write",
                    "source_path": str(root / "source"),
                    "destination_path": str(derived),
                },
            },
            "extract": extraction,
        },
        "state": {
            "ceremony_directory": str(root / "ceremony"),
            "consumption_marker": str(root / "ceremony" / "consumed.json"),
            "terminal_receipt": str(root / "ceremony" / "terminal.json"),
            "evidence_receipt": str(root / "ceremony" / "evidence.json"),
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
            value["operations"]["hydrate"]["cow"]["source_path"] = value["derived_root"]["root"]
            with self.assertRaises(prebuild.PrebuildError):
                prebuild.load_authorization(self.write(root, value))
            value = p0(root)
            value["state"]["consumption_marker"] = (
                value["operations"]["hydrate"]["commands"]["temporal"]["capture"]["stdout_path"]
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
