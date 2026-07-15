from __future__ import annotations

import json
import stat
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import label_prep as prep
import label_protocol as protocol


class NistSeedCeremonyTest(unittest.TestCase):
    def setUp(self) -> None:
        self.commits = {
            system: f"{index + 1:040x}"
            for index, system in enumerate(prep.SYSTEMS)
        }
        self.base_binding = "sha256:" + "a" * 64
        self.input_binding = "sha256:" + "b" * 64
        self.committed_at = "2026-07-14T12:00:30Z"
        claim = {
            "schema": "t111-gate2-commit-set-attempt-v2",
            "source_lineage_binding": prep.commit_set_lineage_binding(self.commits),
            "commits": dict(sorted(self.commits.items())),
            "base_input_binding": self.base_binding,
            "input_binding": self.input_binding,
            "committed_at": self.committed_at,
        }
        commitment = {
            "schema": prep.INPUT_COMMITMENT_SCHEMA,
            "committed_at": self.committed_at,
            "input_binding": self.input_binding,
            "attempt_claim": claim,
            "systems": list(prep.SYSTEMS),
            "sampling_config": {"fixture": 1},
            "decision_rule": {"threshold": "fixed"},
            "burn_ledger": {"sha256": "sha256:" + "c" * 64},
            "protocol_coverage": {"grpc": True},
            "frames": {
                frame: {
                    "definition": frame,
                    "extractor_independent": frame.endswith("recall"),
                    "population_size": 1,
                    "strata": [],
                }
                for frame in prep.FRAMES
            },
            "power_analysis": {"attainable": True},
            "provenance": {"locked_commits": self.commits},
        }
        self.commitment_document = (
            json.dumps(commitment, indent=2, sort_keys=True) + "\n"
        ).encode()
        self.api_url = "https://api.github.com/gists/abc123"
        self.revision = "d" * 40
        self.created_at = "2026-07-14T11:59:00Z"
        self.receipt = {
            "schema": protocol.GITHUB_GIST_RECEIPT_SCHEMA,
            "api_url": self.api_url,
            "revision": self.revision,
            "created_at": self.created_at,
            "document_sha256": protocol.sha256_bytes(self.commitment_document),
        }
        self.github_responses = {
            self.api_url: {
                "url": self.api_url,
                "created_at": self.created_at,
                "history": [
                    {"version": self.revision, "committed_at": self.created_at}
                ],
            },
            self.api_url + "/" + self.revision: {
                "files": {
                    "t111-gate2-input-commitment.json": {
                        "content": self.commitment_document.decode(),
                        "truncated": False,
                    }
                }
            },
        }
        self.canonical_uri = (
            "https://beacon.nist.gov/beacon/2.0/chain/2/pulse/123"
        )
        self.pulse = {
            "uri": self.canonical_uri,
            "version": "2.0",
            "cipherSuite": 0,
            "period": 60_000,
            "certificateId": "1" * 128,
            "chainIndex": 2,
            "pulseIndex": 123,
            "timeStamp": "2026-07-14T12:01:00.000Z",
            "localRandomValue": "2" * 128,
            "external": {
                "sourceId": "0" * 128,
                "statusCode": 0,
                "value": "0" * 128,
            },
            "listValues": [
                {
                    "uri": "https://beacon.nist.gov/beacon/2.0/chain/2/pulse/122",
                    "type": "previous",
                    "value": "3" * 128,
                }
            ],
            "precommitmentValue": "4" * 128,
            "statusCode": 0,
            "signatureValue": "5" * 1024,
            "outputValue": "ABCDEF0123456789" * 8,
        }
        self.time_url = (
            "https://beacon.nist.gov/beacon/2.0/pulse/time/1784030460000"
        )

    def github_fetcher(self, url: str) -> dict[str, Any]:
        return self.github_responses[url]

    def nist_fetcher(self, url: str) -> dict[str, Any]:
        if url not in {self.time_url, self.canonical_uri}:
            raise AssertionError(f"unexpected NIST URL: {url}")
        return {"pulse": self.pulse}

    def test_builds_valid_seed_from_only_the_exact_precommitted_pulse(self) -> None:
        calls: list[str] = []

        def fetch_nist(url: str) -> dict[str, Any]:
            calls.append(url)
            return self.nist_fetcher(url)

        seed = prep.build_nist_seed_record(
            self.commitment_document,
            self.receipt,
            github_fetcher=self.github_fetcher,
            nist_fetcher=fetch_nist,
        )
        self.assertEqual(calls, [self.time_url, self.canonical_uri])
        self.assertEqual(seed["schema"], prep.SEED_RECORD_SCHEMA)
        self.assertEqual(seed["source_reference"], self.canonical_uri)
        self.assertEqual(seed["source_output_hex"], self.pulse["outputValue"].lower())
        self.assertEqual(
            prep.validate_seed_record(
                seed,
                expected_input_binding=self.input_binding,
                expected_input_committed_at=self.committed_at,
            ),
            seed,
        )
        self.assertNotIn("site_id", json.dumps(seed))

    def test_wrong_or_changed_pulse_fails_closed_without_later_fallback(self) -> None:
        wrong = dict(self.pulse)
        wrong["timeStamp"] = "2026-07-14T12:02:00.000Z"

        def wrong_time(_url: str) -> dict[str, Any]:
            return {"pulse": wrong}

        with self.assertRaisesRegex(prep.PrepError, "first one-minute pulse"):
            prep.build_nist_seed_record(
                self.commitment_document,
                self.receipt,
                github_fetcher=self.github_fetcher,
                nist_fetcher=wrong_time,
            )

        changed = dict(self.pulse)
        changed["outputValue"] = "9" * 128

        def endpoints_disagree(url: str) -> dict[str, Any]:
            return {
                "pulse": self.pulse if url == self.time_url else changed
            }

        with self.assertRaisesRegex(prep.PrepError, "endpoint.*disagree"):
            prep.build_nist_seed_record(
                self.commitment_document,
                self.receipt,
                github_fetcher=self.github_fetcher,
                nist_fetcher=endpoints_disagree,
            )

    def test_publication_must_be_exact_and_strictly_before_activation(self) -> None:
        late_receipt = dict(self.receipt)
        late_receipt["created_at"] = self.committed_at
        late_github = dict(self.github_responses)
        late_github[self.api_url] = {
            "url": self.api_url,
            "created_at": self.committed_at,
            "history": [
                {"version": self.revision, "committed_at": self.committed_at}
            ],
        }
        with self.assertRaisesRegex(prep.PrepError, "before activation"):
            prep.build_nist_seed_record(
                self.commitment_document,
                late_receipt,
                github_fetcher=late_github.__getitem__,
                nist_fetcher=self.nist_fetcher,
            )

        changed_document = self.commitment_document.replace(b'"fixture": 1', b'"fixture": 2')
        with self.assertRaisesRegex(prep.PrepError, "GitHub receipt cannot be verified"):
            prep.build_nist_seed_record(
                changed_document,
                self.receipt,
                github_fetcher=self.github_fetcher,
                nist_fetcher=self.nist_fetcher,
            )

    def test_command_freezes_mode_0600_and_never_replaces(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            commitment = root / "commitment.json"
            receipt = root / "receipt.json"
            output = root / "seed.json"
            commitment.write_bytes(self.commitment_document)
            receipt.write_text(json.dumps(self.receipt) + "\n", encoding="utf-8")
            args = SimpleNamespace(
                commitment=commitment,
                github_receipt=receipt,
                output=output,
            )
            prep.create_nist_seed_command(
                args,
                github_fetcher=self.github_fetcher,
                nist_fetcher=self.nist_fetcher,
            )
            seed = json.loads(output.read_bytes())
            self.assertEqual(seed["source_reference"], self.canonical_uri)
            self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o600)
            original = output.read_bytes()
            with self.assertRaisesRegex(prep.PrepError, "cannot create NIST audit seed"):
                prep.create_nist_seed_command(
                    args,
                    github_fetcher=self.github_fetcher,
                    nist_fetcher=self.nist_fetcher,
                )
            self.assertEqual(output.read_bytes(), original)

    def test_transport_rejects_last_or_non_nist_urls_before_network(self) -> None:
        for url in (
            "https://beacon.nist.gov/beacon/2.0/pulse/last",
            "https://example.test/beacon/2.0/pulse/time/1784030460000",
            "http://beacon.nist.gov/beacon/2.0/pulse/time/1784030460000",
        ):
            with self.subTest(url=url), self.assertRaisesRegex(
                protocol.LabelProtocolError, "exact time or canonical"
            ):
                protocol.nist_beacon_json_fetcher(url)


if __name__ == "__main__":
    unittest.main()
