"""Tests for the shared round-state and blind-custody machinery."""

from __future__ import annotations

import inspect
import unittest

import custody
import label_protocol as protocol

BINDING = "sha256:" + "a" * 64


def advance_through(record: dict, upto: str) -> dict:
    """Advance a round one stage at a time up to ``upto`` from wherever it is."""
    start = custody.STAGES.index(custody.current_stage(record)) + 1
    for stage in custody.STAGES[start : custody.STAGES.index(upto) + 1]:
        minute = len(record["transitions"])
        record = custody.advance(record, stage, f"2026-10-10T00:{minute:02d}:00Z", BINDING)
    return record


def label_row(site_id: str, **changes: object) -> dict:
    row = {
        "site_id": site_id,
        "invocation": "yes",
        "operation": "acme.v1.Caller/Call",
        "registration": "yes",
        "service": "acme.v1.Caller",
        "expected_code_role": "production",
        "rationale": f"fixture rationale for {site_id}",
        "evidence": f"fixture evidence for {site_id}",
    }
    row.update(changes)
    return row


class RoundStateTest(unittest.TestCase):
    def test_new_round_is_drafted_only(self) -> None:
        record = custody.new_round("round-1", "2026-10-10T00:00:00Z")
        self.assertEqual(custody.current_stage(record), "drafted")
        self.assertIsNone(record["transitions"][0]["binding"])

    def test_transitions_follow_stages_exactly_once(self) -> None:
        base = custody.new_round("round-1", "2026-10-10T00:00:00Z")
        for transitions in (
            [],
            [{"stage": "drafted", "utc": "2026-10-10T00:00:00Z", "binding": None}] * 2,
            [
                {"stage": "drafted", "utc": "2026-10-10T00:00:00Z", "binding": None},
                {"stage": "corpus_sealed", "utc": "2026-10-10T00:01:00Z", "binding": BINDING},
            ],
            [
                {"stage": "drafted", "utc": "2026-10-10T00:00:00Z", "binding": None},
                {"stage": "parameters_approved", "utc": "2026-10-10T00:01:00Z", "binding": BINDING},
                {"stage": "parameters_approved", "utc": "2026-10-10T00:02:00Z", "binding": BINDING},
            ],
        ):
            with self.subTest(transitions=len(transitions)):
                with self.assertRaises(custody.CustodyError):
                    custody.validate_round_state({**base, "transitions": transitions})

    def test_refusal_cases(self) -> None:
        base = custody.new_round("round-1", "2026-10-10T00:00:00Z")
        drafted = base["transitions"][0]
        cases = {
            "not a mapping": [],
            "missing field": {"schema": custody.ROUND_SCHEMA, "round_id": "round-1"},
            "unknown field": {**base, "extra": 1},
            "wrong schema": {**base, "schema": "other"},
            "empty round id": {**base, "round_id": ""},
            "drafted binds an artifact": {
                **base,
                "transitions": [{**drafted, "binding": BINDING}],
            },
            "non-digest binding": {
                **base,
                "transitions": [
                    drafted,
                    {"stage": "parameters_approved", "utc": "2026-10-10T00:01:00Z", "binding": "sha256:" + "A" * 64},
                ],
            },
            "bad utc": {
                **base,
                "transitions": [{**drafted, "utc": "2026-10-10 00:00:00"}],
            },
            "decreasing utc": {
                **base,
                "transitions": [
                    drafted,
                    {"stage": "parameters_approved", "utc": "2026-10-09T23:59:59Z", "binding": BINDING},
                ],
            },
        }
        for name, value in cases.items():
            with self.subTest(name):
                with self.assertRaises(custody.CustodyError):
                    custody.validate_round_state(value)

    def test_advance_is_one_stage_and_immutable(self) -> None:
        record = custody.new_round("round-1", "2026-10-10T00:00:00Z")
        advanced = custody.advance(record, "parameters_approved", "2026-10-10T00:01:00Z", BINDING)
        self.assertEqual(custody.current_stage(advanced), "parameters_approved")
        self.assertEqual(custody.current_stage(record), "drafted")
        self.assertEqual(len(record["transitions"]), 1)
        with self.assertRaises(custody.CustodyError):
            custody.advance(advanced, "frames_frozen", "2026-10-10T00:02:00Z", BINDING)
        with self.assertRaises(custody.CustodyError):
            custody.advance(advanced, "parameters_approved", "2026-10-10T00:02:00Z", BINDING)
        with self.assertRaises(custody.CustodyError):
            custody.advance(advanced, "drafted", "2026-10-10T00:02:00Z", BINDING)
        with self.assertRaises(custody.CustodyError):
            custody.advance(advanced, "no_such_stage", "2026-10-10T00:02:00Z", BINDING)
        with self.assertRaises(custody.CustodyError):
            custody.advance(advanced, "corpus_sealed", "2026-10-10T00:00:30Z", BINDING)

    def test_require_stage_and_require_scorable(self) -> None:
        record = custody.new_round("round-1", "2026-10-10T00:00:00Z")
        custody.require_stage(record, "drafted")
        with self.assertRaises(custody.CustodyError):
            custody.require_stage(record, "no_such_stage")
        with self.assertRaises(custody.CustodyError):
            custody.require_scorable(record)
        record = advance_through(record, "commitment_published")
        with self.assertRaises(custody.CustodyError):
            custody.require_scorable(record)
        record = advance_through(record, "scored")
        custody.require_scorable(record)
        custody.require_stage(record, "drafted")


class SeparationTest(unittest.TestCase):
    def test_separated_paths_pass(self) -> None:
        custody.require_separation(
            "/tmp/round/labels/frozen.jsonl",
            ["/tmp/round/predictions/candidate.jsonl", "/tmp/round/predictions/other.jsonl"],
        )
        custody.require_separation("/tmp/round/labels/frozen.jsonl", [])

    def test_meeting_paths_refuse(self) -> None:
        cases = (
            ("/tmp/round/labels.jsonl", ["/tmp/round/labels.jsonl"]),
            ("/tmp/round/labels.jsonl", ["/tmp/round/./labels.jsonl"]),
            ("/tmp/round/predictions/labels.jsonl", ["/tmp/round/predictions"]),
            ("/tmp/round", ["/tmp/round/predictions/candidate.jsonl"]),
        )
        for labels, predictions in cases:
            with self.subTest(labels=labels, predictions=predictions):
                with self.assertRaises(custody.CustodyError):
                    custody.require_separation(labels, predictions)

    def test_predictions_must_be_a_sequence(self) -> None:
        with self.assertRaises(custody.CustodyError):
            custody.require_separation("/tmp/round/labels.jsonl", "/tmp/round/predictions.jsonl")


class LabelBindingTest(unittest.TestCase):
    def fixture(self) -> tuple[list[dict], list[str]]:
        known = ["repo@c0ffee:internal/a.go:10-20", "repo@c0ffee:internal/b.go:30-40"]
        return [label_row(site_id) for site_id in known], known

    def test_labels_binding_matches_the_frozen_keeper(self) -> None:
        rows, known = self.fixture()
        expected = protocol.sha256_bytes(protocol.canonical_labels_jsonl(rows, known))
        self.assertEqual(custody.labels_binding(rows, known), expected)
        changed = [dict(row) for row in rows]
        changed[0]["invocation"] = "no"
        changed[0]["operation"] = None
        self.assertNotEqual(custody.labels_binding(changed, known), expected)

    def test_labels_binding_refuses_incomplete_populations(self) -> None:
        rows, known = self.fixture()
        with self.assertRaises(ValueError):
            custody.labels_binding(rows[:1], known)

    def test_label_commitment_binding_matches_the_frozen_keeper(self) -> None:
        rows, known = self.fixture()
        commitment = protocol.build_label_commitment(
            rows,
            known,
            "2026-10-10T00:00:00Z",
            input_binding="sha256:" + "1" * 64,
            artifact_manifest_sha256="sha256:" + "2" * 64,
            reviewer_assignment_sha256="sha256:" + "3" * 64,
            adjudication={"overlap_sites": 0, "disagreements": 0, "adjudicated": 0, "unresolved": 0},
        )
        expected = protocol.sha256_bytes(protocol.canonical_label_commitment_document(commitment))
        self.assertEqual(custody.label_commitment_binding(commitment), expected)
        broken = dict(commitment)
        broken["label_count"] = -1
        with self.assertRaises(ValueError):
            custody.label_commitment_binding(broken)


class DependencyDirectionTest(unittest.TestCase):
    def test_custody_does_not_import_later_layers(self) -> None:
        source = inspect.getsource(custody)
        for forbidden in ("import scoring", "import receipts", "import results"):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
