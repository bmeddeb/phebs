from __future__ import annotations

import json
import os
import stat
import tempfile
import unittest
from pathlib import Path
from typing import Any

import reviewer_kits as kits


class ReviewerKitsTest(unittest.TestCase):
    binding = "sha256:" + "a" * 64
    reviewers = ("reviewer-z", "reviewer-a")

    def row(
        self, site_id: str, *, system: str = "alpha", line: int = 10
    ) -> dict[str, Any]:
        method = "Call"
        return {
            "site_id": site_id,
            "system": system,
            "path": f"src/{site_id}.go",
            "line": line,
            "column": 8,
            "method": method,
            "context": f"{line - 1:6d}| before\n{line:6d}| client.{method}(ctx)\n{line + 1:6d}| after",
        }

    def populations(self) -> tuple[dict[str, list[dict[str, Any]]], dict[str, list[str]]]:
        partitions: dict[str, list[dict[str, Any]]] = {
            "dev": [],
            "holdout": [],
        }
        for split in partitions:
            for system in ("alpha", "beta"):
                for index in range(20):
                    site_id = f"{split}-{system}-{index:02d}"
                    partitions[split].append(self.row(site_id, system=system))
        expected = {
            split: [row["site_id"] for row in rows]
            for split, rows in partitions.items()
        }
        return partitions, expected

    def build(self) -> kits.ReviewerKitBundle:
        partitions, expected = self.populations()
        return kits.build_reviewer_kits(
            partitions,
            expected,
            input_binding=self.binding,
            reviewers=self.reviewers,
        )

    def test_assignment_is_deterministic_order_independent_and_complete(self) -> None:
        partitions, expected = self.populations()
        first = kits.build_reviewer_kits(
            partitions,
            expected,
            input_binding=self.binding,
            reviewers=self.reviewers,
        )
        reordered = {
            split: list(reversed(rows))
            for split, rows in reversed(list(partitions.items()))
        }
        second = kits.build_reviewer_kits(
            reordered,
            {split: list(reversed(ids)) for split, ids in expected.items()},
            input_binding=self.binding,
            reviewers=tuple(reversed(self.reviewers)),
        )
        self.assertEqual(first.manifest_document, second.manifest_document)
        self.assertEqual(first.manifest_sha256, second.manifest_sha256)
        self.assertEqual(
            [kit.document for kit in first.kits],
            [kit.document for kit in second.kits],
        )

        assignments = first.manifest["assignments"]
        self.assertEqual(len(assignments), 80)
        self.assertEqual(len({row["site_id"] for row in assignments}), 80)
        kit_ids = {kit.reviewer: set(kit.site_ids) for kit in first.kits}
        all_reviewers = set(kit_ids)
        for assignment in assignments:
            present = {
                reviewer
                for reviewer, site_ids in kit_ids.items()
                if assignment["site_id"] in site_ids
            }
            self.assertEqual(
                present,
                all_reviewers
                if assignment["cross_review"]
                else {assignment["primary_reviewer"]},
            )

    def test_cross_review_is_independent_and_ten_percent_per_system_split(self) -> None:
        bundle = self.build()
        strata = bundle.manifest["cross_review"]["strata"]
        self.assertEqual(len(strata), 4)
        self.assertTrue(
            all(
                row["population_size"] == 20 and row["cross_review_count"] == 2
                for row in strata
            )
        )
        assignments = {
            row["site_id"]: row for row in bundle.manifest["assignments"]
        }
        partitions, _ = self.populations()
        for split, rows in partitions.items():
            for system in ("alpha", "beta"):
                population = [
                    row["site_id"] for row in rows if row["system"] == system
                ]
                selected = sorted(
                    population,
                    key=lambda site_id: (
                        kits._rank(
                            kits._CROSS_DOMAIN,
                            self.binding,
                            system,
                            split,
                            site_id,
                        ),
                        site_id,
                    ),
                )[:2]
                self.assertEqual(
                    {site_id for site_id in population if assignments[site_id]["cross_review"]},
                    set(selected),
                )
        # Cross-review selection has its own declared domain and is not derived
        # from either reviewer's primary assignment.
        self.assertNotEqual(
            bundle.manifest["primary_assignment"]["domain"],
            bundle.manifest["cross_review"]["domain"],
        )

    def test_kits_emit_source_only_rows_and_canonical_manifest_digest(self) -> None:
        bundle = self.build()
        self.assertEqual(
            bundle.manifest_sha256,
            kits.sha256_bytes(bundle.manifest_document),
        )
        self.assertEqual(
            bundle.manifest_document,
            kits.canonical_json_document(bundle.manifest),
        )
        for kit in bundle.kits:
            rows = [json.loads(line) for line in kit.document.splitlines()]
            self.assertEqual(
                [row["site_id"] for row in rows],
                sorted(row["site_id"] for row in rows),
            )
            for row in rows:
                self.assertEqual(set(row), kits.SOURCE_FIELDS)
                self.assertFalse(set(row) & kits.PROHIBITED_FIELDS)

    def test_source_validation_rejects_hidden_fields_and_bad_coordinates(self) -> None:
        valid = self.row("site")
        self.assertEqual(kits.validate_source_row(valid), valid)
        cases = [
            ({**valid, "split": "holdout"}, "prohibited"),
            ({**valid, "frames": {}}, "prohibited"),
            ({**valid, "object": "/pkg.Service/Call"}, "prohibited"),
            ({key: value for key, value in valid.items() if key != "context"}, "exactly"),
            ({**valid, "path": "../outside.go"}, "safe relative"),
            ({**valid, "line": True}, "positive integer"),
            ({**valid, "method": "Not-Method"}, "Go identifier"),
            ({**valid, "context": "no numbered source line"}, "method line"),
        ]
        for row, message in cases:
            with self.subTest(message=message), self.assertRaisesRegex(
                kits.ReviewerKitError, message
            ):
                kits.validate_source_row(row)

    def test_duplicate_ids_and_incomplete_partitions_are_rejected(self) -> None:
        row = self.row("site")
        with self.assertRaisesRegex(kits.ReviewerKitError, "duplicate site_id inside"):
            kits.validate_source_partitions(
                {"dev": [row, row]}, {"dev": ["site"]}
            )
        with self.assertRaisesRegex(kits.ReviewerKitError, "across partitions"):
            kits.validate_source_partitions(
                {"dev": [row], "holdout": [row]},
                {"dev": ["site"], "holdout": ["site"]},
            )
        with self.assertRaisesRegex(kits.ReviewerKitError, "incomplete partition set"):
            kits.validate_source_partitions(
                {"dev": [row]},
                {"dev": ["site"], "holdout": []},
            )
        with self.assertRaisesRegex(kits.ReviewerKitError, "is incomplete"):
            kits.validate_source_partitions(
                {"dev": [row]}, {"dev": ["site", "missing"]}
            )

    def test_partition_loader_rejects_symlinks_and_non_objects(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            source = root / "source.jsonl"
            source.write_text(json.dumps(self.row("site")) + "\n", encoding="utf-8")
            self.assertEqual(kits.load_source_partition(source), [self.row("site")])
            link = root / "link.jsonl"
            link.symlink_to(source)
            with self.assertRaisesRegex(kits.ReviewerKitError, "non-symlink"):
                kits.load_source_partition(link)
            invalid = root / "invalid.jsonl"
            invalid.write_text("[]\n", encoding="utf-8")
            with self.assertRaisesRegex(kits.ReviewerKitError, "must be an object"):
                kits.load_source_partition(invalid)

    def test_freeze_rejects_overwrite_and_symlink_and_uses_private_files(self) -> None:
        bundle = self.build()
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            output = root / "kits"
            digest = kits.freeze_reviewer_kits(output, bundle)
            self.assertEqual(digest, bundle.manifest_sha256)
            expected_names = {
                "reviewer-1.jsonl",
                "reviewer-2.jsonl",
                "assignment-manifest.json",
                "assignment-manifest.sha256",
            }
            self.assertEqual({path.name for path in output.iterdir()}, expected_names)
            before = {path.name: path.read_bytes() for path in output.iterdir()}
            for path in output.iterdir():
                self.assertFalse(path.is_symlink())
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                kits.freeze_reviewer_kits(output, bundle)
            self.assertEqual(
                {path.name: path.read_bytes() for path in output.iterdir()}, before
            )

            target = root / "target"
            target.mkdir()
            symlink = root / "linked-output"
            symlink.symlink_to(target, target_is_directory=True)
            with self.assertRaises(FileExistsError):
                kits.freeze_reviewer_kits(symlink, bundle)

    def test_exactly_two_distinct_reviewers_and_valid_binding_are_required(self) -> None:
        partitions = {"dev": [self.row("site")]}
        expected = {"dev": ["site"]}
        for reviewers in ((), ("one",), ("same", "same"), ("a", "b", "c")):
            with self.subTest(reviewers=reviewers), self.assertRaises(
                kits.ReviewerKitError
            ):
                kits.build_reviewer_kits(
                    partitions,
                    expected,
                    input_binding=self.binding,
                    reviewers=reviewers,
                )
        with self.assertRaisesRegex(kits.ReviewerKitError, "input_binding"):
            kits.build_reviewer_kits(
                partitions,
                expected,
                input_binding="a" * 64,
                reviewers=("one", "two"),
            )


if __name__ == "__main__":
    unittest.main()
