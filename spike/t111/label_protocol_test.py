from __future__ import annotations

import json
import os
import stat
import tempfile
import unittest
from pathlib import Path
from typing import Any

import label_protocol as protocol


class LabelProtocolTest(unittest.TestCase):
    def label(self, site_id: str, **changes: Any) -> dict[str, Any]:
        value: dict[str, Any] = {
            "site_id": site_id,
            "invocation": "yes",
            "operation": "package.v1.Service/Call",
            "registration": "no",
            "service": None,
            "expected_code_role": "production",
            "rationale": "Static receiver resolves to the generated client.",
            "evidence": "The call expression and generated declaration agree.",
        }
        value.update(changes)
        return value

    def test_labels_are_exact_complete_sorted_canonical_jsonl(self) -> None:
        rows = [self.label("site-b"), self.label("site-a")]
        document = protocol.canonical_labels_jsonl(rows, ["site-a", "site-b"])
        decoded = [json.loads(line) for line in document.splitlines()]
        self.assertEqual([row["site_id"] for row in decoded], ["site-a", "site-b"])
        self.assertTrue(document.endswith(b"\n"))
        self.assertNotIn(b'": ', document)
        self.assertNotIn(b'", "', document)
        expected = b"".join(
            (
                json.dumps(
                    row,
                    ensure_ascii=False,
                    allow_nan=False,
                    sort_keys=True,
                    separators=(",", ":"),
                )
                + "\n"
            ).encode()
            for row in sorted(rows, key=lambda row: row["site_id"])
        )
        self.assertEqual(document, expected)
        self.assertEqual(
            protocol.canonical_labels_sha256(rows, ["site-a", "site-b"]),
            protocol.sha256_bytes(expected),
        )

    def test_exact_fields_decisions_and_identity_null_invariants(self) -> None:
        valid = self.label(
            "site",
            invocation="no",
            operation=None,
            registration="yes",
            service="package.v1.Service",
        )
        self.assertEqual(protocol.validate_labels([valid], ["site"])[0], valid)

        cases = [
            ({**valid, "extra": True}, "fields must be exactly"),
            (self.label("site", invocation="maybe"), "yes/no/unsure"),
            (self.label("site", invocation=[]), "yes/no/unsure"),
            (self.label("site", invocation="no"), "requires operation=null"),
            (
                self.label("site", operation="/package.v1.Service/Call"),
                "requires canonical",
            ),
            (self.label("site", operation="Service/Call"), "requires canonical"),
            (
                self.label("site", registration="unsure", service="package.Service"),
                "requires service=null",
            ),
            (
                self.label("site", registration="yes", service="Service"),
                "requires canonical",
            ),
            (
                self.label("site", expected_code_role="fixture"),
                "expected_code_role",
            ),
            (
                self.label("site", expected_code_role=[]),
                "expected_code_role",
            ),
            (self.label("site", rationale="  "), "rationale"),
            (self.label("site", evidence=""), "evidence"),
        ]
        for row, message in cases:
            with self.subTest(message=message), self.assertRaisesRegex(
                protocol.LabelProtocolError, message
            ):
                protocol.validate_labels([row], ["site"])

    def test_known_id_equality_and_duplicate_rejection(self) -> None:
        with self.assertRaisesRegex(protocol.LabelProtocolError, "duplicate label"):
            protocol.validate_labels([self.label("a"), self.label("a")], ["a"])
        with self.assertRaisesRegex(protocol.LabelProtocolError, "missing"):
            protocol.validate_labels([self.label("a")], ["a", "b"])
        with self.assertRaisesRegex(protocol.LabelProtocolError, "unknown"):
            protocol.validate_labels([self.label("a"), self.label("b")], ["a"])
        with self.assertRaisesRegex(protocol.LabelProtocolError, "duplicate known"):
            protocol.validate_labels([self.label("a")], ["a", "a"])
        with self.assertRaisesRegex(protocol.LabelProtocolError, "canonical string"):
            protocol.validate_labels([self.label(" a")], [" a"])

    def test_freeze_is_mode_0600_and_never_replaces(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "labels.jsonl"
            rows = [self.label("site")]
            digest = protocol.freeze_labels(path, rows, ["site"])
            document = protocol.canonical_labels_jsonl(rows, ["site"])
            self.assertEqual(path.read_bytes(), document)
            self.assertEqual(digest, protocol.sha256_bytes(document))
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)

            with self.assertRaises(FileExistsError):
                protocol.freeze_labels(
                    path,
                    [self.label("site", rationale="different")],
                    ["site"],
                )
            self.assertEqual(path.read_bytes(), document)

    def test_label_commitment_schema_and_document(self) -> None:
        rows = [self.label("b"), self.label("a")]
        commitment = protocol.build_label_commitment(
            rows,
            ["a", "b"],
            "2026-07-14T12:34:56Z",
            input_binding="sha256:" + "1" * 64,
            artifact_manifest_sha256="sha256:" + "2" * 64,
            reviewer_assignment_sha256="sha256:" + "3" * 64,
            adjudication={
                "overlap_sites": 1,
                "disagreements": 1,
                "adjudicated": 1,
                "unresolved": 0,
            },
        )
        self.assertEqual(commitment["schema"], protocol.LABEL_COMMITMENT_SCHEMA)
        self.assertEqual(commitment["label_count"], 2)
        self.assertEqual(
            commitment["labels_sha256"],
            protocol.canonical_labels_sha256(rows, ["a", "b"]),
        )
        document = protocol.canonical_label_commitment_document(commitment)
        self.assertEqual(json.loads(document), commitment)
        self.assertTrue(document.endswith(b"\n"))
        self.assertEqual(
            protocol.verify_label_commitment(commitment, rows, ["a", "b"]),
            commitment,
        )
        changed_rows = [self.label("a"), self.label("b", evidence="changed")]
        with self.assertRaisesRegex(protocol.LabelProtocolError, "labels_sha256"):
            protocol.verify_label_commitment(commitment, changed_rows, ["a", "b"])

        for field, replacement in (
            ("schema", "wrong"),
            ("labels_sha256", "abc"),
            ("site_ids_sha256", "sha256:" + "A" * 64),
            ("input_binding", "abc"),
            ("artifact_manifest_sha256", "abc"),
            ("reviewer_assignment_sha256", "abc"),
            ("label_count", True),
            ("committed_at", "2026-07-14T12:34:56+00:00"),
        ):
            invalid = dict(commitment)
            invalid[field] = replacement
            with self.subTest(field=field), self.assertRaises(
                protocol.LabelProtocolError
            ):
                protocol.validate_label_commitment(invalid)
        invalid = dict(commitment)
        invalid["extra"] = None
        with self.assertRaisesRegex(protocol.LabelProtocolError, "exactly"):
            protocol.validate_label_commitment(invalid)

    def test_github_initial_revision_receipt_verification(self) -> None:
        document = b'{"schema":"fixture"}\n'
        revision = "a" * 40
        later = "b" * 40
        api_url = "https://api.github.com/gists/abc123"
        receipt = {
            "schema": protocol.GITHUB_GIST_RECEIPT_SCHEMA,
            "api_url": api_url,
            "revision": revision,
            "created_at": "2026-07-14T01:02:03Z",
            "document_sha256": protocol.sha256_bytes(document),
        }
        responses = {
            api_url: {
                "url": api_url,
                "created_at": receipt["created_at"],
                "history": [
                    {"version": later, "committed_at": "2026-07-14T02:00:00Z"},
                    {
                        "version": revision,
                        "committed_at": receipt["created_at"],
                    },
                ],
            },
            api_url + "/" + revision: {
                "files": {
                    "t111-label-commitment.json": {
                        "content": document.decode(),
                        "truncated": False,
                    }
                }
            },
        }
        calls: list[str] = []

        def fetcher(url: str) -> dict[str, Any]:
            calls.append(url)
            return responses[url]

        self.assertEqual(
            protocol.verify_github_initial_gist_receipt(
                receipt, document, fetcher
            ),
            receipt,
        )
        self.assertEqual(calls, [api_url, api_url + "/" + revision])
        calls.clear()
        self.assertEqual(
            protocol.build_github_initial_gist_receipt(api_url, document, fetcher),
            receipt,
        )
        self.assertEqual(
            calls,
            [api_url, api_url, api_url + "/" + revision],
        )

    def test_github_receipt_and_remote_mismatches_fail_closed(self) -> None:
        document = b"commitment\n"
        revision = "1" * 40
        api_url = "https://api.github.com/gists/cafe"
        receipt = {
            "schema": protocol.GITHUB_GIST_RECEIPT_SCHEMA,
            "api_url": api_url,
            "revision": revision,
            "created_at": "2026-07-14T00:00:00Z",
            "document_sha256": protocol.sha256_bytes(document),
        }

        invalid_receipts = [
            ({**receipt, "api_url": "http://api.github.com/gists/cafe"}, "https"),
            ({**receipt, "api_url": "https://github.com/gists/cafe"}, "https"),
            ({**receipt, "revision": "A" * 40}, "revision"),
            ({**receipt, "created_at": "2026-07-14T00:00:00+00:00"}, "RFC3339"),
            ({**receipt, "document_sha256": "0" * 64}, "sha256"),
        ]
        for invalid, message in invalid_receipts:
            with self.subTest(message=message), self.assertRaisesRegex(
                protocol.LabelProtocolError, message
            ):
                protocol.validate_github_gist_receipt(invalid)

        def responses(
            *,
            initial: str = revision,
            created_at: str = receipt["created_at"],
            content: bytes = document,
            files: int = 1,
            truncated: bool = False,
        ) -> dict[str, dict[str, Any]]:
            file_map = {
                f"file-{index}.json": {
                    "content": content.decode(),
                    "truncated": truncated,
                }
                for index in range(files)
            }
            return {
                api_url: {
                    "url": api_url,
                    "created_at": created_at,
                    "history": [
                        {"version": initial, "committed_at": receipt["created_at"]}
                    ],
                },
                api_url + "/" + revision: {"files": file_map},
            }

        mismatch_cases = [
            (responses(initial="2" * 40), "not the initial"),
            (
                responses(created_at="2026-07-14T00:00:01Z"),
                "created_at",
            ),
            (responses(content=b"different\n"), "bytes do not equal"),
            (responses(files=2), "exactly one"),
            (responses(truncated=True), "truncated"),
        ]
        for remote, message in mismatch_cases:
            with self.subTest(message=message), self.assertRaisesRegex(
                protocol.LabelProtocolError, message
            ):
                protocol.verify_github_initial_gist_receipt(
                    receipt, document, remote.__getitem__
                )

        with self.assertRaisesRegex(protocol.LabelProtocolError, "document_sha256"):
            protocol.verify_github_initial_gist_receipt(
                receipt, b"wrong\n", responses().__getitem__
            )

    def test_freeze_bytes_requires_existing_parent(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            missing = Path(raw) / "missing" / "document"
            with self.assertRaises(FileNotFoundError):
                protocol.freeze_bytes_exclusive(missing, b"document")
            self.assertFalse(missing.exists())


if __name__ == "__main__":
    unittest.main()
