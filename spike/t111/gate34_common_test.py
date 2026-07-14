from __future__ import annotations

import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from gate34_common import (
    BURN_LEDGER_SCHEMA_VERSION,
    GATE3_MERGE_PREDICATES,
    LEGACY_BURN_LEDGER_SCHEMA_VERSION,
    ValidationError,
    _coordinate_digest,
    _legacy_coordinate,
    _tree_entries,
    build_burn_ledger,
    digest_value,
    ensure_writable_targets,
    load_burn_ledger,
    load_burn_ledger_cohorts,
    precommitted_generator_config,
    read_jsonl,
    require_fresh_blind_population,
    sha256_file,
    validate_precommitted_generator_config,
)


class Gate34IntegrityTests(unittest.TestCase):
    def test_burn_ledger_v2_canonical_build_load_round_trip(self) -> None:
        cohorts = self._burn_cohorts()
        ledger = build_burn_ledger(list(reversed(cohorts)))
        self.assertEqual(ledger, build_burn_ledger(cohorts))
        self.assertEqual(ledger["schema_version"], BURN_LEDGER_SCHEMA_VERSION)
        self.assertEqual(
            [cohort["cohort_id"] for cohort in ledger["cohorts"]],
            ["a-first", "z-last"],
        )

        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "burn-ledger.json"
            self._write_json(path, ledger)
            loaded_cohorts, cohort_digest = load_burn_ledger_cohorts(path)
            projected, projection_digest = load_burn_ledger(path)

        self.assertEqual(loaded_cohorts, ledger["cohorts"])
        self.assertEqual(build_burn_ledger(loaded_cohorts), ledger)
        self.assertEqual(cohort_digest, ledger["coordinates_sha256"])
        self.assertEqual(projection_digest, cohort_digest)
        self.assertEqual(
            projected,
            [
                {
                    "system": "alpha",
                    "path": "a.go",
                    "start_line": 3,
                    "end_line": 4,
                },
                {
                    "system": "zeta",
                    "path": "z.go",
                    "start_line": 8,
                    "end_line": 8,
                },
            ],
        )

    def test_burn_ledger_v2_rejects_tampered_coordinate_and_digest(self) -> None:
        ledger = build_burn_ledger(self._burn_cohorts())
        coordinate_tamper = self._clone(ledger)
        coordinate_tamper["cohorts"][0]["coordinates"][0]["end_line"] += 1
        self._resign(coordinate_tamper)

        digest_tamper = self._clone(ledger)
        digest_tamper["coordinates_sha256"] = "sha256:" + "f" * 64
        self._resign(digest_tamper)

        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            coordinate_path = root / "coordinate.json"
            digest_path = root / "digest.json"
            self._write_json(coordinate_path, coordinate_tamper)
            self._write_json(digest_path, digest_tamper)
            with self.assertRaisesRegex(ValidationError, "coordinate projection mismatch"):
                load_burn_ledger_cohorts(coordinate_path)
            with self.assertRaisesRegex(
                ValidationError, "burn coordinate projection does not match"
            ):
                load_burn_ledger_cohorts(digest_path)

    def test_burn_ledger_v2_rejects_duplicate_cohort_ids(self) -> None:
        cohorts = self._burn_cohorts()
        cohorts[1]["cohort_id"] = cohorts[0]["cohort_id"]
        with self.assertRaisesRegex(ValidationError, "cohort IDs must be unique"):
            build_burn_ledger(cohorts)

        ledger = build_burn_ledger(self._burn_cohorts())
        ledger["cohorts"][1]["cohort_id"] = ledger["cohorts"][0]["cohort_id"]
        self._resign(ledger)
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "duplicate.json"
            self._write_json(path, ledger)
            with self.assertRaisesRegex(ValidationError, "cohort IDs must be unique"):
                load_burn_ledger_cohorts(path)

    def test_burn_ledger_v2_requires_source_commit_for_each_system(self) -> None:
        cohorts = self._burn_cohorts()
        cohorts[0]["source_commits"] = {"other": "c" * 40}
        with self.assertRaisesRegex(ValidationError, "system has no source commit"):
            build_burn_ledger(cohorts)

        ledger = build_burn_ledger(self._burn_cohorts())
        ledger["cohorts"][0]["source_commits"] = {"other": "c" * 40}
        self._resign(ledger)
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "missing-source.json"
            self._write_json(path, ledger)
            with self.assertRaisesRegex(ValidationError, "system has no source commit"):
                load_burn_ledger_cohorts(path)

    def test_burn_ledger_v2_copies_and_validates_source_artifacts(self) -> None:
        cohorts = self._burn_cohorts()
        ledger = build_burn_ledger(cohorts)
        expected = self._clone(ledger)

        source_artifacts = cohorts[0]["source_artifacts"]
        source_artifacts[0]["path"] = "mutated.labels.jsonl"
        source_artifacts.append(
            {
                "path": "extra.labels.jsonl",
                "records": 2,
                "sha256": "sha256:" + "3" * 64,
            }
        )
        self.assertEqual(ledger, expected)

        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "isolated.json"
            self._write_json(path, ledger)
            loaded, _ = load_burn_ledger_cohorts(path)
        self.assertEqual(loaded, ledger["cohorts"])

        invalid = self._burn_cohorts()
        invalid[0]["source_artifacts"][0]["records"] = True
        with self.assertRaisesRegex(ValidationError, "source artifact provenance"):
            build_burn_ledger(invalid)

    def test_legacy_burn_projection_remains_loadable_after_v2_append(self) -> None:
        labeling = Path(__file__).resolve().parent / "labeling"
        current_path = labeling / "burn-ledger.json"
        current_document = json.loads(current_path.read_text(encoding="utf-8"))
        self.assertEqual(
            current_document["schema_version"], BURN_LEDGER_SCHEMA_VERSION
        )
        current_coordinates, _ = load_burn_ledger(current_path)
        legacy_names = (
            "sites.dev.jsonl",
            "sites.holdout.jsonl",
            "g34.sites.dev.jsonl",
            "g34.sites.holdout.jsonl",
            "g3h2.sites.jsonl",
        )

        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            sources = []
            projected = []
            for name in legacy_names:
                source = labeling / name
                copied = root / name
                copied.write_bytes(source.read_bytes())
                projected.extend(
                    coordinate
                    for row in read_jsonl(source)
                    if (coordinate := _legacy_coordinate(row)) is not None
                )
                sources.append(
                    {
                        "path": name,
                        "records": len(read_jsonl(source)),
                        "sha256": sha256_file(source),
                    }
                )
            unique = {
                json.dumps(coordinate, sort_keys=True, separators=(",", ":")): coordinate
                for coordinate in projected
            }
            expected_coordinates = [unique[key] for key in sorted(unique)]
            expected_digest = _coordinate_digest(expected_coordinates)
            unsigned = {
                "schema_version": LEGACY_BURN_LEDGER_SCHEMA_VERSION,
                "projection": (
                    "system,path,start_line,end_line; all legacy labeled/template "
                    "site coordinates; predicates and values deliberately ignored"
                ),
                "sources": sources,
                "coordinate_count": len(expected_coordinates),
                "coordinates_sha256": expected_digest,
            }
            legacy_path = root / "burn-ledger-v1.json"
            self._write_json(
                legacy_path,
                {**unsigned, "ledger_digest": digest_value(unsigned)},
            )
            legacy_coordinates, legacy_digest = load_burn_ledger(legacy_path)

        self.assertEqual(legacy_coordinates, expected_coordinates)
        self.assertEqual(legacy_digest, expected_digest)
        current_keys = {
            json.dumps(coordinate, sort_keys=True, separators=(",", ":"))
            for coordinate in current_coordinates
        }
        self.assertTrue(set(unique).issubset(current_keys))

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

    def test_only_exact_reviewed_gitlink_is_excluded(self) -> None:
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
                f"160000,{commit},docs/theme",
            )
            tree = self._git(root, "write-tree")
            policy = [{
                "path": "docs/theme",
                "object_id": commit,
                "reason": "reviewed non-code fixture",
            }]
            entries = _tree_entries(root, tree, policy)
            self.assertNotIn("docs/theme", [entry[3] for entry in entries])
            changed = [{**policy[0], "object_id": "0" * 40}]
            with self.assertRaisesRegex(ValidationError, "changed"):
                _tree_entries(root, tree, changed)
            missing = [{**policy[0], "path": "docs/elsewhere"}]
            with self.assertRaisesRegex(ValidationError, "undeclared gitlink"):
                _tree_entries(root, tree, missing)

    @staticmethod
    def _git(root: Path, *args: str) -> str:
        return subprocess.run(
            ["git", "-C", str(root), *args], check=True,
            capture_output=True, text=True,
        ).stdout.strip()

    @staticmethod
    def _burn_cohorts() -> list[dict[str, object]]:
        return [
            {
                "cohort_id": "a-first",
                "input_binding": "sha256:" + "1" * 64,
                "source_commits": {"alpha": "a" * 40},
                "source_artifacts": [
                    {
                        "path": "alpha.labels.jsonl",
                        "records": 1,
                        "sha256": "sha256:" + "2" * 64,
                    }
                ],
                "coordinates": [
                    {
                        "system": "alpha",
                        "path": "a.go",
                        "start_line": 3,
                        "end_line": 4,
                    }
                ],
            },
            {
                "cohort_id": "z-last",
                "input_binding": None,
                "source_commits": {"zeta": "b" * 40},
                "source_artifacts": [],
                "coordinates": [
                    {
                        "system": "zeta",
                        "path": "z.go",
                        "start_line": 8,
                        "end_line": 8,
                    }
                ],
            },
        ]

    @staticmethod
    def _clone(value: dict[str, object]) -> dict[str, object]:
        return json.loads(json.dumps(value))

    @staticmethod
    def _resign(ledger: dict[str, object]) -> None:
        unsigned = dict(ledger)
        unsigned.pop("ledger_digest", None)
        ledger["ledger_digest"] = digest_value(unsigned)

    @staticmethod
    def _write_json(path: Path, value: object) -> None:
        path.write_text(json.dumps(value), encoding="utf-8")


if __name__ == "__main__":
    unittest.main()
