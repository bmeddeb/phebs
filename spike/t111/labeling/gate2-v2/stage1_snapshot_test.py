"""Synthetic-transport tests for the Stage-1 snapshot runner. No network."""

import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from email.utils import format_datetime
from pathlib import Path
from unittest import mock

import stage1_snapshot as s1

CUTOFF = datetime(2026, 7, 20, 16, 0, 0, tzinfo=timezone.utc)
CONSTANTS = {"proposed_cutoff": "2026-07-20T16:00:00Z", "max_clock_skew_seconds": 120}
QUERY = b"query { x }"


def good_body():
    def repo(owner, oid="a" * 40):
        return {"nameWithOwner": owner, "url": "https://x",
                "defaultBranchRef": {"name": "main",
                                     "target": {"oid": oid,
                                                "committedDate": "2026-07-20T15:00:00Z"}},
                "licenseInfo": {"spdxId": "MIT"}}
    return json.dumps({"data": {
        "temporal": repo("temporalio/temporal"), "dapr": repo("dapr/dapr"),
        "loki": repo("grafana/loki"),
        "onlineBoutique": repo("GoogleCloudPlatform/microservices-demo"),
    }}).encode()


def transport_for(status=200, body=None, date=None):
    headers = {"Date": format_datetime(date or CUTOFF)}
    def transport(query_bytes, token):
        return status, headers, body if body is not None else good_body()
    return transport


class Stage1Test(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.stage1 = Path(self.tmp.name) / "stage1"
        self.stage1.mkdir()
        self.patches = [
            mock.patch.object(s1, "STAGE1", self.stage1),
            mock.patch.dict(os.environ, {"GITHUB_TOKEN": "t"}),
        ]
        for p in self.patches:
            p.start()

    def tearDown(self):
        for p in self.patches:
            p.stop()
        self.tmp.cleanup()

    def _run(self, transport, now=CUTOFF):
        return s1.run(transport, lambda: now, CONSTANTS, QUERY)

    def test_admits_and_seals_good_response(self):
        receipt = self._run(transport_for())
        self.assertEqual(receipt["status"], "ADMITTED")
        self.assertEqual(len(receipt["heads"]), 4)
        self.assertTrue((self.stage1 / "response.json").exists())
        sealed = json.loads((self.stage1 / "receipt.json").read_text())
        self.assertEqual(sealed["response_sha256"],
                         s1.sha256_bytes(good_body()))

    def test_rejects_outside_fire_window(self):
        receipt = self._run(transport_for(), now=CUTOFF + timedelta(seconds=200))
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("cutoff", receipt["failure"])
        self.assertFalse((self.stage1 / "response.json").exists())

    def test_rejects_clock_skew(self):
        receipt = self._run(transport_for(date=CUTOFF - timedelta(seconds=500)))
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("skew", receipt["failure"])

    def test_rejects_graphql_errors_and_missing_fixture(self):
        errors = json.dumps({"errors": [{"message": "boom"}]}).encode()
        self.assertEqual(self._run(transport_for(body=errors))["status"], "REJECTED")
        partial = json.loads(good_body())
        del partial["data"]["loki"]
        (self.stage1 / "receipt.json").unlink()
        receipt = self._run(transport_for(body=json.dumps(partial).encode()))
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("loki", receipt["failure"])

    def test_rejects_malformed_oid(self):
        body = json.loads(good_body())
        body["data"]["dapr"]["defaultBranchRef"]["target"]["oid"] = "HEAD"
        receipt = self._run(transport_for(body=json.dumps(body).encode()))
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("dapr", receipt["failure"])

    def test_rejection_still_seals_receipt(self):
        self._run(transport_for(status=500))
        sealed = json.loads((self.stage1 / "receipt.json").read_text())
        self.assertEqual(sealed["status"], "REJECTED")

    def test_rejects_malformed_date_header(self):
        def transport(query_bytes, token):
            return 200, {"Date": "not a date"}, good_body()
        receipt = self._run(transport)
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("Date header", receipt["failure"])

    def test_transport_exception_seals_rejection(self):
        def transport(query_bytes, token):
            raise OSError("connection reset")
        receipt = self._run(transport)
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("unexpected failure", receipt["failure"])
        sealed = json.loads((self.stage1 / "receipt.json").read_text())
        self.assertEqual(sealed["status"], "REJECTED")
        self.assertIsNone(sealed["request_completed"])
        self.assertIsNone(sealed["response_sha256"])

    def test_non_json_body_rejected_with_response_digest_bound(self):
        receipt = self._run(transport_for(body=b"<html>gateway</html>"))
        self.assertEqual(receipt["status"], "REJECTED")
        self.assertIn("not JSON", receipt["failure"])
        self.assertEqual(receipt["response_sha256"],
                         s1.sha256_bytes(b"<html>gateway</html>"))
        self.assertIsNotNone(receipt["request_completed"])

    def test_token_never_appears_in_sealed_receipt(self):
        secret = "SECRET-TOKEN-XYZ-9c41"
        with mock.patch.dict(os.environ, {"GITHUB_TOKEN": secret}):
            def transport(query_bytes, token):
                raise OSError(f"refused for bearer someone")
            self._run(transport)
        sealed = (self.stage1 / "receipt.json").read_bytes()
        self.assertNotIn(secret.encode(), sealed)


if __name__ == "__main__":
    unittest.main()
