from __future__ import annotations

import json
import unittest
from typing import Any

import label_adjudication as adjudication
import label_protocol
import reviewer_kits


class LabelAdjudicationTest(unittest.TestCase):
    binding = "sha256:" + "c" * 64
    reviewers = ("reviewer-a", "reviewer-b")

    def source_row(self, site_id: str, *, line: int = 10) -> dict[str, Any]:
        return {
            "site_id": site_id,
            "system": "fixture",
            "path": f"src/{site_id}.go",
            "line": line,
            "column": 8,
            "method": "Call",
            "context": f"{line:6d}| client.Call(ctx)",
        }

    def label(self, site_id: str, **changes: Any) -> dict[str, Any]:
        value: dict[str, Any] = {
            "site_id": site_id,
            "invocation": "yes",
            "operation": "package.v1.Service/Call",
            "registration": "no",
            "service": None,
            "expected_code_role": "production",
            "rationale": "Resolved from the source and generated declaration.",
            "evidence": "The typed receiver and method signature agree.",
        }
        value.update(changes)
        return value

    def assignment_bundle(self) -> reviewer_kits.ReviewerKitBundle:
        rows = [self.source_row(f"site-{index:02d}") for index in range(20)]
        return reviewer_kits.build_reviewer_kits(
            {"holdout": rows},
            {"holdout": [row["site_id"] for row in rows]},
            input_binding=self.binding,
            reviewers=self.reviewers,
        )

    def reviewer_label_populations(
        self, bundle: reviewer_kits.ReviewerKitBundle
    ) -> dict[str, list[dict[str, Any]]]:
        return {
            kit.reviewer: [self.label(site_id) for site_id in kit.site_ids]
            for kit in bundle.kits
        }

    def assignment_by_site(
        self, bundle: reviewer_kits.ReviewerKitBundle
    ) -> dict[str, dict[str, Any]]:
        return {
            row["site_id"]: row for row in bundle.manifest["assignments"]
        }

    def replace_label(
        self,
        labels: dict[str, list[dict[str, Any]]],
        reviewer: str,
        site_id: str,
        **changes: Any,
    ) -> None:
        labels[reviewer] = [
            self.label(site_id, **changes) if row["site_id"] == site_id else row
            for row in labels[reviewer]
        ]

    def test_complete_adjudication_and_exact_counters(self) -> None:
        bundle = self.assignment_bundle()
        labels = self.reviewer_label_populations(bundle)
        assignments = self.assignment_by_site(bundle)
        overlap = sorted(
            site_id
            for site_id, row in assignments.items()
            if row["cross_review"]
        )
        self.assertEqual(len(overlap), 2)

        disagreement = overlap[0]
        primary = assignments[disagreement]["primary_reviewer"]
        secondary = (
            self.reviewers[1] if primary == self.reviewers[0] else self.reviewers[0]
        )
        self.replace_label(
            labels,
            secondary,
            disagreement,
            invocation="no",
            operation=None,
            rationale="Different conclusion.",
        )

        # Different prose on an otherwise identical overlap is not a formal
        # disagreement and needs no adjudicator disclosure.
        prose_only = overlap[1]
        prose_primary = assignments[prose_only]["primary_reviewer"]
        prose_secondary = (
            self.reviewers[1]
            if prose_primary == self.reviewers[0]
            else self.reviewers[0]
        )
        self.replace_label(
            labels,
            prose_secondary,
            prose_only,
            rationale="Independent reviewer used different words.",
            evidence="Independent source citation.",
        )

        unsure_site = next(
            site_id
            for site_id, row in assignments.items()
            if not row["cross_review"]
        )
        unsure_primary = assignments[unsure_site]["primary_reviewer"]
        self.replace_label(
            labels,
            unsure_primary,
            unsure_site,
            invocation="unsure",
            operation=None,
        )
        adjudicator_rows = [
            self.label(disagreement, rationale="Adjudicated disagreement."),
            self.label(
                unsure_site,
                invocation="no",
                operation=None,
                rationale="Adjudicated uncertainty.",
            ),
        ]

        result = adjudication.adjudicate_labels(
            bundle.manifest_document,
            bundle.manifest_sha256,
            labels,
            adjudicator_rows,
        )
        self.assertEqual(
            result.counters,
            {
                "overlap_sites": 2,
                "disagreements": 1,
                "adjudicated": 2,
                "unresolved": 0,
            },
        )
        self.assertEqual(len(result.rows), 20)
        self.assertEqual(
            result.document_sha256, label_protocol.sha256_bytes(result.document)
        )
        self.assertEqual(
            [row["site_id"] for row in result.rows],
            sorted(assignments),
        )
        final = {row["site_id"]: row for row in result.rows}
        self.assertEqual(final[unsure_site]["invocation"], "no")
        self.assertEqual(
            final[prose_only]["rationale"],
            next(
                row["rationale"]
                for row in labels[prose_primary]
                if row["site_id"] == prose_only
            ),
        )

    def test_every_cross_review_site_requires_both_reviewer_labels(self) -> None:
        bundle = self.assignment_bundle()
        labels = self.reviewer_label_populations(bundle)
        overlap = next(
            row for row in bundle.manifest["assignments"] if row["cross_review"]
        )
        primary = overlap["primary_reviewer"]
        secondary = (
            self.reviewers[1] if primary == self.reviewers[0] else self.reviewers[0]
        )
        labels[secondary] = [
            row for row in labels[secondary] if row["site_id"] != overlap["site_id"]
        ]
        with self.assertRaisesRegex(adjudication.AdjudicationError, "missing"):
            adjudication.adjudicate_labels(
                bundle.manifest_document,
                bundle.manifest_sha256,
                labels,
                [],
            )

    def test_primary_coverage_and_no_labels_outside_assignment(self) -> None:
        bundle = self.assignment_bundle()
        assignments = self.assignment_by_site(bundle)
        non_overlap = next(
            site_id for site_id, row in assignments.items() if not row["cross_review"]
        )
        primary = assignments[non_overlap]["primary_reviewer"]
        other = self.reviewers[1] if primary == self.reviewers[0] else self.reviewers[0]

        missing = self.reviewer_label_populations(bundle)
        missing[primary] = [
            row for row in missing[primary] if row["site_id"] != non_overlap
        ]
        with self.assertRaisesRegex(adjudication.AdjudicationError, "missing"):
            adjudication.adjudicate_labels(
                bundle.manifest_document, bundle.manifest_sha256, missing, []
            )

        outside = self.reviewer_label_populations(bundle)
        outside[other].append(self.label(non_overlap))
        with self.assertRaisesRegex(adjudication.AdjudicationError, "unknown"):
            adjudication.adjudicate_labels(
                bundle.manifest_document, bundle.manifest_sha256, outside, []
            )

        duplicate = self.reviewer_label_populations(bundle)
        duplicate[primary].append(self.label(non_overlap))
        with self.assertRaisesRegex(adjudication.AdjudicationError, "duplicate"):
            adjudication.adjudicate_labels(
                bundle.manifest_document, bundle.manifest_sha256, duplicate, []
            )

    def test_disagreement_uses_only_five_fixed_fields(self) -> None:
        base = self.label("site")
        prose = {**base, "rationale": "different", "evidence": "different"}
        self.assertEqual(
            adjudication.decision_signature(base),
            adjudication.decision_signature(prose),
        )
        variants = [
            {**base, "invocation": "no", "operation": None},
            {**base, "operation": "package.v1.Service/Other"},
            {**base, "registration": "yes", "service": "package.v1.Service"},
            {
                **base,
                "registration": "yes",
                "service": "package.v1.OtherService",
            },
            {**base, "expected_code_role": "test"},
        ]
        for variant in variants:
            with self.subTest(variant=variant):
                self.assertNotEqual(
                    adjudication.decision_signature(base),
                    adjudication.decision_signature(variant),
                )

    def test_adjudicator_rows_must_exactly_cover_required_sites(self) -> None:
        bundle = self.assignment_bundle()
        labels = self.reviewer_label_populations(bundle)
        assignments = self.assignment_by_site(bundle)
        site_id = next(
            site_id for site_id, row in assignments.items() if not row["cross_review"]
        )
        primary = assignments[site_id]["primary_reviewer"]
        self.replace_label(
            labels,
            primary,
            site_id,
            registration="unsure",
            service=None,
        )

        cases = [
            ([], "missing"),
            ([self.label("outside")], "missing=.*site"),
            ([self.label(site_id), self.label("outside")], "unknown"),
            ([self.label(site_id), self.label(site_id)], "duplicate"),
        ]
        for rows, message in cases:
            with self.subTest(message=message), self.assertRaisesRegex(
                adjudication.AdjudicationError, message
            ):
                adjudication.adjudicate_labels(
                    bundle.manifest_document,
                    bundle.manifest_sha256,
                    labels,
                    rows,
                )

    def test_adjudicator_may_not_leave_unsure_or_invalid_schema(self) -> None:
        bundle = self.assignment_bundle()
        labels = self.reviewer_label_populations(bundle)
        assignments = self.assignment_by_site(bundle)
        site_id = next(iter(assignments))
        primary = assignments[site_id]["primary_reviewer"]
        self.replace_label(
            labels,
            primary,
            site_id,
            invocation="unsure",
            operation=None,
        )
        if assignments[site_id]["cross_review"]:
            other = (
                self.reviewers[1]
                if primary == self.reviewers[0]
                else self.reviewers[0]
            )
            self.replace_label(
                labels,
                other,
                site_id,
                invocation="unsure",
                operation=None,
            )
        with self.assertRaisesRegex(adjudication.AdjudicationError, "unresolved"):
            adjudication.adjudicate_labels(
                bundle.manifest_document,
                bundle.manifest_sha256,
                labels,
                [self.label(site_id, invocation="unsure", operation=None)],
            )

        invalid = self.reviewer_label_populations(bundle)
        invalid_row = dict(invalid[primary][0])
        invalid_row["extractor_outcome"] = True
        invalid[primary][0] = invalid_row
        with self.assertRaisesRegex(adjudication.AdjudicationError, "fields must be exactly"):
            adjudication.adjudicate_labels(
                bundle.manifest_document,
                bundle.manifest_sha256,
                invalid,
                [],
            )

    def test_assignment_digest_canonicality_and_primary_tampering_fail(self) -> None:
        bundle = self.assignment_bundle()
        labels = self.reviewer_label_populations(bundle)
        manifest = json.loads(bundle.manifest_document)
        manifest["assignments"][0]["primary_reviewer"] = (
            self.reviewers[1]
            if manifest["assignments"][0]["primary_reviewer"] == self.reviewers[0]
            else self.reviewers[0]
        )
        tampered = reviewer_kits.canonical_json_document(manifest)
        with self.assertRaisesRegex(adjudication.AdjudicationError, "digest mismatch"):
            adjudication.adjudicate_labels(
                tampered, bundle.manifest_sha256, labels, []
            )
        with self.assertRaisesRegex(adjudication.AdjudicationError, "tampering"):
            adjudication.validate_assignment_manifest(
                tampered, label_protocol.sha256_bytes(tampered)
            )

        pretty = json.dumps(bundle.manifest, indent=2, sort_keys=True).encode() + b"\n"
        with self.assertRaisesRegex(adjudication.AdjudicationError, "not canonical"):
            adjudication.validate_assignment_manifest(
                pretty, label_protocol.sha256_bytes(pretty)
            )

    def test_assignment_missing_duplicate_and_kit_count_tampering_fail(self) -> None:
        bundle = self.assignment_bundle()
        mutations: list[tuple[str, dict[str, Any]]] = []

        missing = json.loads(bundle.manifest_document)
        missing["assignments"].pop()
        mutations.append(("incomplete", missing))

        duplicate = json.loads(bundle.manifest_document)
        duplicate["assignments"][1] = dict(duplicate["assignments"][0])
        mutations.append(("duplicate", duplicate))

        bad_count = json.loads(bundle.manifest_document)
        bad_count["kits"][0]["site_count"] += 1
        mutations.append(("kit assignment mismatch", bad_count))

        for message, manifest in mutations:
            document = reviewer_kits.canonical_json_document(manifest)
            with self.subTest(message=message), self.assertRaisesRegex(
                adjudication.AdjudicationError, message
            ):
                adjudication.validate_assignment_manifest(
                    document, label_protocol.sha256_bytes(document)
                )


if __name__ == "__main__":
    unittest.main()
