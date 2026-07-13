from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

from gate34_common import (
    GATE3_MERGE_PREDICATES,
    ValidationError,
    _tree_entries,
    ensure_writable_targets,
    precommitted_generator_config,
    require_fresh_blind_population,
    validate_precommitted_generator_config,
)


class Gate34IntegrityTests(unittest.TestCase):
    def test_exposed_population_cannot_be_silently_shrunk(self) -> None:
        require_fresh_blind_population(0)
        with self.assertRaisesRegex(ValidationError, "cannot drop 700"):
            require_fresh_blind_population(700)

    def test_gate3_population_contains_merges_not_declarations(self) -> None:
        self.assertIn("WORKLOAD_RUNS_IMAGE", GATE3_MERGE_PREDICATES)
        self.assertIn("DEPLOYABLE_REACHES_OPERATION", GATE3_MERGE_PREDICATES)
        self.assertNotIn("K8S_WORKLOAD", GATE3_MERGE_PREDICATES)
        self.assertNotIn("SERVICE_NAME_LITERAL", GATE3_MERGE_PREDICATES)

    def test_precommitted_configuration_cannot_be_reseeded(self) -> None:
        config = precommitted_generator_config("g34")
        validate_precommitted_generator_config("g34", config)
        changed = {**config, "seed": int(config["seed"]) + 1}
        with self.assertRaisesRegex(ValidationError, "precommitted"):
            validate_precommitted_generator_config("g34", changed)

    def test_sealed_target_cannot_be_overwritten(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            target = Path(raw) / "holdout.jsonl"
            target.write_text("sealed\n", encoding="utf-8")
            with self.assertRaisesRegex(ValidationError, "overwrite or reuse"):
                ensure_writable_targets([target], force=False)
            with self.assertRaisesRegex(ValidationError, "--force is forbidden"):
                ensure_writable_targets([Path(raw) / "new.jsonl"], force=True)

    def test_gitlink_is_rejected_before_source_filtering(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            self._git(root, "init", "-q")
            self._git(root, "config", "user.email", "test@example.test")
            self._git(root, "config", "user.name", "Test")
            (root / "main.go").write_text("package main\n", encoding="utf-8")
            self._git(root, "add", "main.go")
            self._git(root, "commit", "-q", "-m", "initial")
            commit = self._git(root, "rev-parse", "HEAD")
            self._git(
                root, "update-index", "--add", "--cacheinfo",
                f"160000,{commit},nested-without-go-suffix",
            )
            tree = self._git(root, "write-tree")
            with self.assertRaisesRegex(ValidationError, "gitlink"):
                _tree_entries(root, tree)

    @staticmethod
    def _git(root: Path, *args: str) -> str:
        return subprocess.run(
            ["git", "-C", str(root), *args], check=True,
            capture_output=True, text=True,
        ).stdout.strip()


if __name__ == "__main__":
    unittest.main()
