"""Tests for the per-pack quality and operating result envelope."""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

import corpus
import harness as pilot_harness
import receipts
import results
import scoring

DIGEST = "sha256:" + "c" * 64


def threshold(kind: str = "min", value: float = 0.75) -> dict:
    return {"kind": kind, "value": value}


def parameters() -> dict:
    return {
        "schema": scoring.PARAMETERS_SCHEMA,
        "status": "sealed",
        "claims": {"caller_invocation": threshold(), "caller_registration": threshold(value=0.6)},
    }


def score(field: str, yes: int, no: int, unsure: int = 0) -> dict:
    decided = yes + no
    value = {
        "schema": pilot_harness.HARNESS_SCHEMA,
        "field": field,
        "sampled": decided + unsure,
        "yes": yes,
        "no": no,
        "unsure": unsure,
        "decided": decided,
    }
    if decided > 0:
        proportion = yes / decided
        value["proportion"] = proportion
        value["wilson_low"] = max(0.0, proportion - 0.1)
        value["wilson_high"] = min(1.0, proportion + 0.1)
    return value


def binding() -> dict:
    return {
        "schema": corpus.BINDING_SCHEMA,
        "source_commit": "a" * 40,
        "corpus_lock_sha256": DIGEST,
        "repositories": [{"name": "acme", "commit": "b" * 40}],
    }


def rows(outcome: str = "success") -> list[dict]:
    sealed = parameters()
    return [
        scoring.quality_row(score("caller_invocation", 8, 2), sealed, outcome=outcome),
        scoring.quality_row(score("caller_registration", 0, 0, 3), sealed, outcome=outcome),
    ]


class Workspace:
    def __init__(self, test: unittest.TestCase) -> None:
        self._tmp = tempfile.TemporaryDirectory(prefix="harness-results-")
        test.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)

    def receipt(self, step: str = "build", run_binding: dict | None = None) -> dict:
        bound = corpus.digest_of(run_binding or binding())
        return receipts.run_bounded_step(
            self.root / "run", step, [sys.executable, "-c", "pass"], 30, run_binding_sha256=bound
        )


class AssembleTest(unittest.TestCase):
    def test_success_result_binds_run_rows_and_receipts(self) -> None:
        receipt = Workspace(self).receipt()
        result = results.assemble_pack_result(
            "phebs.caller-quality",
            outcome="success",
            parameters=parameters(),
            run_binding=binding(),
            quality_rows=rows(),
            receipts=[receipt],
        )
        self.assertEqual(result["schema"], results.RESULT_SCHEMA)
        self.assertEqual(result["run_binding"], binding())
        self.assertEqual(result["quality"]["parameters_sha256"], corpus.digest_of(scoring.require_sealed_parameters(parameters())))
        self.assertEqual([row["result"] for row in result["quality"]["rows"]], ["pass", "not_measured"])
        self.assertTrue(result["operating"]["measured"])
        self.assertIsNone(result["operating"]["reason"])
        self.assertEqual(result["operating"]["receipts"], [receipt])
        self.assertEqual(results.validate_pack_result(result), result)

    def test_never_ran_or_stopped_runs_cannot_pass(self) -> None:
        passing_rows = rows()  # assembled under a successful run: one pass exists
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="not_run",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=passing_rows,
                operating_reason="run never started",
            )
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="stop",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=passing_rows,
                operating_reason="capacity stop",
            )
        for outcome in ("not_run", "stop", "failed"):
            with self.subTest(outcome):
                result = results.assemble_pack_result(
                    "phebs.caller-quality",
                    outcome=outcome,
                    parameters=parameters(),
                    run_binding=binding(),
                    quality_rows=rows(outcome),
                    operating_reason=f"{outcome} before measurement",
                )
                self.assertTrue(all(row["result"] == "not_measured" for row in result["quality"]["rows"]))
                self.assertFalse(result["operating"]["measured"])

    def test_claim_coverage_must_be_exact(self) -> None:
        base = rows()
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="success",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=base[:1],
                operating_reason="none",
            )
        forged = [dict(base[0], claim="caller_other"), base[1]]
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="success",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=forged,
                operating_reason="none",
            )

    def test_forged_threshold_or_counts_refuse(self) -> None:
        base = rows()
        cases = {
            "forged threshold": [dict(base[0], threshold=threshold(value=0.1)), base[1]],
            "inconsistent counts": [dict(base[0], decided=9), base[1]],
            "estimate on zero denominator": [dict(base[0]), dict(base[1], estimate=0.5)],
            "pass under failed run": [dict(base[0], result="pass"), dict(base[1])],
        }
        for name, quality_rows in cases.items():
            with self.subTest(name):
                with self.assertRaises(results.ResultError):
                    results.assemble_pack_result(
                        "phebs.caller-quality",
                        outcome="failed",
                        parameters=parameters(),
                        run_binding=binding(),
                        quality_rows=quality_rows,
                        operating_reason="failed before scoring",
                    )

    def test_receipts_must_bind_this_run(self) -> None:
        workspace = Workspace(self)
        unbound = workspace.receipt(step="unbound", run_binding=binding())
        unbound["run_binding_sha256"] = None
        other = workspace.receipt(step="other", run_binding={**binding(), "source_commit": "d" * 40})
        cases = {
            "no binding": [unbound],
            "foreign run": [other],
        }
        for name, receipts_in in cases.items():
            with self.subTest(name):
                with self.assertRaises(results.ResultError):
                    results.assemble_pack_result(
                        "phebs.caller-quality",
                        outcome="success",
                        parameters=parameters(),
                        run_binding=binding(),
                        quality_rows=rows(),
                        receipts=receipts_in,
                    )

    def test_operating_envelope_rules(self) -> None:
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="success",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=rows(),
            )
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="success",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=rows(),
                receipts=[Workspace(self).receipt()],
                operating_reason="but also a reason",
            )

    def test_pack_identity_and_unsealed_parameters_refuse(self) -> None:
        for pack_id in ("Phebs.caller", "phebs", "phebs.", "caller-quality"):
            with self.subTest(pack_id=pack_id):
                with self.assertRaises(results.ResultError):
                    results.assemble_pack_result(
                        pack_id,
                        outcome="success",
                        parameters=parameters(),
                        run_binding=binding(),
                        quality_rows=rows(),
                        operating_reason="none",
                    )
        draft = {**parameters(), "status": "approved_not_sealed"}
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="success",
                parameters=draft,
                run_binding=binding(),
                quality_rows=rows(),
                operating_reason="none",
            )
        with self.assertRaises(results.ResultError):
            results.assemble_pack_result(
                "phebs.caller-quality",
                outcome="perhaps",
                parameters=parameters(),
                run_binding=binding(),
                quality_rows=rows(),
                operating_reason="none",
            )


class ValidatePackResultTest(unittest.TestCase):
    def assembled(self) -> dict:
        return results.assemble_pack_result(
            "phebs.caller-quality",
            outcome="success",
            parameters=parameters(),
            run_binding=binding(),
            quality_rows=rows(),
            receipts=[Workspace(self).receipt()],
        )

    def test_tampering_refuses(self) -> None:
        good = self.assembled()
        row = good["quality"]["rows"][0]
        cases = {
            "unknown field": {**good, "extra": 1},
            "unknown outcome": {**good, "outcome": "perhaps"},
            "outcome without rows changing": {**good, "outcome": "not_run"},
            "empty rows": {**good, "quality": {**good["quality"], "rows": []}},
            "duplicate claims": {**good, "quality": {**good["quality"], "rows": [row, dict(row)]}},
            "interval not numeric": {
                **good,
                "quality": {**good["quality"], "rows": [{**row, "wilson_low": "x"}, good["quality"]["rows"][1]]},
            },
            "interval inverted": {
                **good,
                "quality": {**good["quality"], "rows": [{**row, "wilson_low": 0.99}, good["quality"]["rows"][1]]},
            },
            "interval missing": {
                **good,
                "quality": {**good["quality"], "rows": [{**row, "wilson_high": None}, good["quality"]["rows"][1]]},
            },
            "unbound receipt": {
                **good,
                "operating": {**good["operating"], "receipts": [{**good["operating"]["receipts"][0], "run_binding_sha256": None}]},
            },
            "measured without receipts": {**good, "operating": {**good["operating"], "measured": False, "receipts": [], "reason": None}},
        }
        for name, value in cases.items():
            with self.subTest(name):
                with self.assertRaises(results.ResultError):
                    results.validate_pack_result(value)
        results.validate_pack_result(good)

    def test_write_is_exclusive_and_returns_the_digest(self) -> None:
        good = self.assembled()
        target = Workspace(self).root / "result.json"
        digest = results.write_pack_result(target, good)
        self.assertEqual(digest, corpus.digest_of(good))
        self.assertEqual(json.loads(target.read_text()), good)
        with self.assertRaises(results.ResultError):
            results.write_pack_result(target, good)
        broken = {**good, "extra": 1}
        with self.assertRaises(results.ResultError):
            results.write_pack_result(target.parent / "broken.json", broken)
        self.assertFalse((target.parent / "broken.json").exists())


if __name__ == "__main__":
    unittest.main()
