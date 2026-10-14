"""Tests for the T58.1 scorer. Run: cd spike/t581 && python3 -m unittest test_score"""

from __future__ import annotations

import unittest

import score


def site(unit, path, start, end, state, object_="", role=None):
    row = {
        "schema": score.SITE_SCHEMA, "unit": unit, "path": path,
        "start_byte": start, "end_byte": end, "state": state, "object": object_,
    }
    return row


def cand(unit, path, start, end, predicate, object_="", role="production"):
    return {
        "schema": score.CANDIDATE_SCHEMA, "unit_id": unit, "repository": unit,
        "commit": "c" * 40, "path": path, "predicate": predicate, "object": object_,
        "tier": "heuristic", "code_role": role, "detail": "{}",
        "start_byte": start, "end_byte": end, "start_line": 1, "end_line": 1,
    }


def index(rows, key_fn):
    out = {}
    for row in rows:
        out.setdefault(key_fn(row), []).append(row)
    return out


def run(sites, candidates, adjudications=None):
    return score.score(
        {score.key(s["unit"], s["path"], s["start_byte"], s["end_byte"]): s for s in sites},
        index(candidates, lambda c: score.key(c["unit_id"], c["path"], c["start_byte"], c["end_byte"])),
        adjudications,
    )


class TestStateMapping(unittest.TestCase):
    def test_predicates_map_onto_the_frozen_vocabulary(self):
        cases = {
            "CALLS_OPERATION": "resolved_call",
            "UNRESOLVED_GRPC_CALL": "ambiguous_call",
            "REGISTERS_GRPC_SERVICE": "resolved_registration",
            "UNRESOLVED_GRPC_REGISTRATION": "ambiguous_registration",
        }
        for predicate, want in cases.items():
            self.assertEqual(score.candidate_state({"predicate": predicate}), want)

    def test_gap_splits_by_reason(self):
        self.assertEqual(
            score.candidate_state({"predicate": "GRPC_EXTRACTION_GAP", "object": "source_too_large"}),
            "gap_source_too_large",
        )
        self.assertEqual(
            score.candidate_state({"predicate": "GRPC_EXTRACTION_GAP", "object": "parse_error"}),
            "gap_parse_error",
        )

    def test_unrecognised_shapes_are_loud_not_silent(self):
        self.assertTrue(score.candidate_state({"predicate": "GRPC_EXTRACTION_GAP", "object": "weird"}).startswith("gap_unknown_reason:"))
        self.assertTrue(score.candidate_state({"predicate": "SOMETHING_ELSE"}).startswith("unknown_predicate:"))


class TestScoring(unittest.TestCase):
    def test_perfect_agreement_passes_when_the_floor_is_met(self):
        sites, cands = [], []
        for i in range(score.THRESHOLDS["scored_sites_min"]):
            sites.append(site("r", f"f{i}.go", i, i + 5, "resolved_call", "/p.S/M"))
            cands.append(cand("r", f"f{i}.go", i, i + 5, "CALLS_OPERATION", "/p.S/M"))
        result = run(sites, cands)
        self.assertTrue(result["pass"]["passed"], result["pass"]["checks"])
        self.assertEqual(result["metrics"]["scored_sites"], 300)
        self.assertEqual(result["metrics"]["coverage"], 1.0)
        self.assertEqual(result["metrics"]["state_agreement"], 1.0)

    def test_scored_floor_is_enforced(self):
        result = run(
            [site("r", "f.go", 1, 5, "resolved_call")],
            [cand("r", "f.go", 1, 5, "CALLS_OPERATION")],
        )
        self.assertFalse(result["pass"]["passed"])
        self.assertFalse(result["pass"]["checks"]["scored_sites"])

    def test_a_missed_site_lowers_coverage(self):
        sites = [site("r", f"f{i}.go", i, i + 5, "resolved_call") for i in range(3)]
        cands = [cand("r", "f0.go", 0, 5, "CALLS_OPERATION")]
        result = run(sites, cands)
        self.assertEqual(result["metrics"]["missed_sites"], 2)
        self.assertAlmostEqual(result["metrics"]["coverage"], 1 / 3)

    def test_resolved_ambiguous_disagreement_is_an_inversion(self):
        result = run(
            [site("r", "f.go", 1, 5, "resolved_call")],
            [cand("r", "f.go", 1, 5, "UNRESOLVED_GRPC_CALL")],
        )
        self.assertEqual(result["metrics"]["inversions"], 1)
        self.assertFalse(result["pass"]["checks"]["inversions"])

    def test_call_registration_swap_is_counted_separately(self):
        result = run(
            [site("r", "f.go", 1, 5, "resolved_call")],
            [cand("r", "f.go", 1, 5, "REGISTERS_GRPC_SERVICE")],
        )
        self.assertEqual(result["metrics"]["call_registration_swaps"], 1)
        self.assertFalse(result["pass"]["checks"]["call_registration_swaps"])

    def test_spurious_row_with_no_oracle_site(self):
        result = run([site("r", "f.go", 1, 5, "resolved_call")],
                     [cand("r", "f.go", 1, 5, "CALLS_OPERATION"),
                      cand("r", "g.go", 9, 14, "CALLS_OPERATION")])
        self.assertEqual(result["metrics"]["spurious_rows"], 1)

    def test_duplicate_rows_at_one_span_are_reported(self):
        result = run([site("r", "f.go", 1, 5, "resolved_call")],
                     [cand("r", "f.go", 1, 5, "CALLS_OPERATION"),
                      cand("r", "f.go", 1, 5, "CALLS_OPERATION")])
        self.assertEqual(result["metrics"]["duplicated_sites"], 1)

    def test_out_of_domain_leaves_the_denominator_but_a_row_there_is_spurious(self):
        sites = [site("r", "f.go", 1, 5, "out_of_domain"), site("r", "g.go", 1, 5, "resolved_call")]
        cands = [cand("r", "f.go", 1, 5, "CALLS_OPERATION"), cand("r", "g.go", 1, 5, "CALLS_OPERATION")]
        result = run(sites, cands)
        self.assertEqual(result["metrics"]["scored_sites"], 1)
        self.assertEqual(result["metrics"]["out_of_domain_candidates"], 1)
        self.assertEqual(result["metrics"]["spurious_rows"], 1)
        self.assertEqual(result["metrics"]["coverage"], 1.0)


class TestAdjudication(unittest.TestCase):
    def test_override_replaces_oracle_truth(self):
        k = score._adj_key(score.key("r", "f.go", 1, 5))
        result = run([site("r", "f.go", 1, 5, "resolved_call")],
                     [cand("r", "f.go", 1, 5, "UNRESOLVED_GRPC_CALL")],
                     {k: ["ambiguous_call", "two services declare this method"]})
        self.assertEqual(result["metrics"]["state_agreement"], 1.0)
        self.assertEqual(result["metrics"]["inversions"], 0)

    def test_not_a_site_leaves_the_denominator_and_counts_its_rows(self):
        k = score._adj_key(score.key("r", "f.go", 1, 5))
        result = run([site("r", "f.go", 1, 5, "resolved_call")],
                     [cand("r", "f.go", 1, 5, "CALLS_OPERATION")],
                     {k: ["not_a_site", "a local method, not a gRPC call"]})
        self.assertEqual(result["metrics"]["scored_sites"], 0)
        self.assertEqual(result["metrics"]["spurious_rows"], 1)

    def test_a_pending_adjudication_fails_the_round(self):
        k = score._adj_key(score.key("r", "f.go", 1, 5))
        result = run([site("r", "f.go", 1, 5, "resolved_call")],
                     [cand("r", "f.go", 1, 5, "CALLS_OPERATION")],
                     {k: [None, None]})
        self.assertEqual(result["metrics"]["pending_after_adjudication"], 1)
        self.assertFalse(result["pass"]["checks"]["pending_after_adjudication"])


class TestDisclosures(unittest.TestCase):
    def test_per_role_agreement_uses_the_exporter_roles(self):
        sites = [site("r", "a.go", 1, 5, "resolved_call"), site("r", "b_test.go", 1, 5, "resolved_call")]
        cands = [cand("r", "a.go", 1, 5, "CALLS_OPERATION", role="production"),
                 cand("r", "b_test.go", 1, 5, "UNRESOLVED_GRPC_CALL", role="test")]
        result = run(sites, cands)
        self.assertEqual(result["metrics"]["production_role_agreement"], 1.0)
        self.assertEqual(result["metrics"]["per_role_agreement"]["test"], 0.0)
        self.assertTrue(result["pass"]["checks"]["production_role_agreement"])

    def test_a_production_role_defect_fails_even_when_the_aggregate_would_pass(self):
        sites = [site("r", f"p{i}.go", i, i + 5, "resolved_call") for i in range(2)]
        sites += [site("r", f"t{i}.go", 100 + i, 105 + i, "resolved_call") for i in range(200)]
        cands = [cand("r", "p0.go", 0, 5, "UNRESOLVED_GRPC_CALL", role="production"),
                 cand("r", "p1.go", 1, 6, "CALLS_OPERATION", role="production")]
        cands += [cand("r", f"t{i}.go", 100 + i, 105 + i, "CALLS_OPERATION", role="test") for i in range(200)]
        result = run(sites, cands)
        self.assertEqual(result["metrics"]["production_role_agreement"], 0.5)
        self.assertFalse(result["pass"]["checks"]["production_role_agreement"])

    def test_concentration_names_the_largest_member(self):
        sites = [site("big", f"a{i}.go", i, i + 5, "resolved_call") for i in range(9)]
        sites += [site("small", "b.go", 1, 5, "resolved_call")]
        cands = [cand("big", f"a{i}.go", i, i + 5, "CALLS_OPERATION") for i in range(9)]
        cands += [cand("small", "b.go", 1, 5, "CALLS_OPERATION")]
        result = run(sites, cands)
        self.assertEqual(result["metrics"]["concentration"]["largest_member"], "big")
        self.assertEqual(result["metrics"]["concentration"]["largest_share"], 0.9)
        self.assertEqual(result["metrics"]["per_repository_scored"], {"big": 9, "small": 1})

    def test_the_limits_are_restated_in_the_output(self):
        result = run([site("r", "f.go", 1, 5, "resolved_call")], [cand("r", "f.go", 1, 5, "CALLS_OPERATION")])
        joined = " ".join(result["disclosures"])
        self.assertIn("not scored", joined)
        self.assertIn("role classification", joined)
        self.assertIn("out_of_domain", joined)


class TestOutOfDomainAggregates(unittest.TestCase):
    def aggregate(self, unit, total, reasons, files):
        return {"schema": score.OUT_OF_DOMAIN_SCHEMA, "unit": unit, "total": total,
                "by_reason": reasons, "files_with_candidates": files}

    def test_aggregates_are_not_treated_as_sites(self):
        rows = [site("r", "f.go", 1, 5, "resolved_call"),
                self.aggregate("r", 42, {"unindexed_selector": 40, "zero_arguments": 2}, 7)]
        sites = score.sites_from_rows(rows)
        self.assertEqual(len(sites), 1)
        got = score.aggregates_from_rows(rows)
        self.assertEqual(got["r"]["total"], 42)
        self.assertEqual(got["r"]["by_reason"]["zero_arguments"], 2)

    def test_two_aggregates_for_one_unit_are_refused(self):
        rows = [self.aggregate("r", 1, {}, 1), self.aggregate("r", 2, {}, 1)]
        with self.assertRaises(SystemExit):
            score.aggregates_from_rows(rows)

    def test_a_zero_aggregate_is_distinguishable_from_a_missing_one(self):
        sites = score.sites_from_rows([site("have", "f.go", 1, 5, "resolved_call"),
                                       site("missing", "g.go", 1, 5, "resolved_call")])
        result = score.score(sites, {}, None, {"have": {"total": 0, "by_reason": {}, "files_with_candidates": 0}})
        summary = result["metrics"]["out_of_domain"]
        self.assertEqual(summary["units_with_aggregate"], 1)
        self.assertEqual(summary["units_missing_aggregate"], ["missing"])

    def test_reasons_and_files_sum_across_units(self):
        aggregates = {
            "a": {"total": 10, "by_reason": {"unindexed_selector": 9, "zero_arguments": 1}, "files_with_candidates": 3},
            "b": {"total": 5, "by_reason": {"unindexed_selector": 5}, "files_with_candidates": 2},
        }
        result = score.score({}, {}, None, aggregates)
        summary = result["metrics"]["out_of_domain"]
        self.assertEqual(summary["total"], 15)
        self.assertEqual(summary["files_with_candidates"], 5)
        self.assertEqual(summary["by_reason"], {"unindexed_selector": 14, "zero_arguments": 1})
        self.assertEqual(summary["per_unit"], {"a": 10, "b": 5})


class TestInputGuards(unittest.TestCase):
    def test_two_oracle_sites_at_one_span_are_refused(self):
        rows = [site("r", "f.go", 1, 5, "resolved_call"), site("r", "f.go", 1, 5, "ambiguous_call")]
        with self.assertRaises(SystemExit):
            score.sites_from_rows(rows)

    def test_a_foreign_site_schema_is_refused(self):
        row = site("r", "f.go", 1, 5, "resolved_call")
        row["schema"] = "something-else-v1"
        with self.assertRaises(SystemExit):
            score.sites_from_rows([row])

    def test_a_foreign_candidate_schema_is_refused(self):
        row = cand("r", "f.go", 1, 5, "CALLS_OPERATION")
        row["schema"] = "something-else-v1"
        with self.assertRaises(SystemExit):
            score.candidates_from_rows([row])

    def test_a_missing_schema_is_refused_rather_than_assumed(self):
        row = site("r", "f.go", 1, 5, "resolved_call")
        del row["schema"]
        with self.assertRaises(SystemExit):
            score.sites_from_rows([row])


if __name__ == "__main__":
    unittest.main()
