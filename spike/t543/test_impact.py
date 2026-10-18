"""Inert transport/checker/cleanup controls; never a public candidate scorer."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import impact_checks as impact
import impact_browser
import qualify_impact as harness


def fixture(query_id="operation-3"):
    oracle = json.loads((impact.REPO / "spike/t541/oracle.json").read_text())
    binding = json.loads((impact.REPO / "spike/t541/input-binding.json").read_text())
    gold = next(q for q in oracle["queries"] if q["query_id"] == query_id)
    commit = binding["evaluation"]["derived_input_commit"]
    generation = {"state": "current", "plane": "repository-overlay", "repository": binding["evaluation"]["repository"], "commit": commit, "publication_revision": 1, "pair_count": 1, "partition_progress": {"state": "complete", "settled_pair_count": 1, "succeeded_pair_count": 1, "total_pair_count": 1}, "record_counts": {"candidate_records": 9, "base_records": 9, "excluded_go_test_records": 0}}
    for index, key in enumerate(("generation_digest", "declaration_set_digest", "candidate_manifest_digest", "resolver_manifest_digest", "pair_set_digest", "manifest_digest")):
        generation[key] = "sha256:" + str(index)*64
    declaration = next(d for d in oracle["declarations"] if d["operation"] == gold["query"]["endpoint"]["operation"])
    claim = {"predicate": "DECLARES_OPERATION", "object": declaration["declaration"]["object"], "lineage": declaration["endpoint"]["declaration_lineage"], "tier": "exact", "assertion_id": "declaration-control", "run_id": "declaration-run", "sources": [{**{key: declaration["declaration"][key] for key in ("path", "start_byte", "end_byte", "start_line", "end_line")}, "repository": binding["evaluation"]["repository"], "commit": commit, "assertion_id": "declaration-control", "run_id": "declaration-run", "atom_id": "declaration-atom"}]}
    files = {r["path"]: r for r in binding["evaluation"]["files"]}
    rows = []
    for key in gold["expected_site_ids"]:
        site = next(s for s in oracle["sites"] if s["site_id"] == key)
        unit = {"state": site["unit"]["state"]}
        if unit["state"] == "resolved":
            unit.update(candidates=[{k: site["unit"][k] for k in ("id", "build_targets")}], candidate_total=1)
        else:
            unit.update(reason=site["unit"]["reason"], candidates=[{"id": "au_" + "a"*64}], candidate_total=1)
        source = {k: site[k] for k in ("path", "start_byte", "end_byte", "start_line", "end_line")}
        source.update(repository=binding["evaluation"]["repository"], commit=commit, run_id=generation["generation_digest"], object_id=files[site["path"]]["git_blob"], blob_digest="sha256:"+files[site["path"]]["sha256"], atom_id="sha256:"+files[site["path"]]["sha256"], plane="repository-overlay", assertion_id="sha256:"+hashlib.sha256(key.encode()).hexdigest(), citation="inert-control")
        rows.append({"classification": "resolved_caller", "protocol": "protobuf", "operation": site["wire_operation"], "declaration_lineage": declaration["endpoint"]["declaration_lineage"], "resolution": "syntax", "tier": "heuristic", "code_role": "production", "fresh": True, "unit": unit, "unit_group": unit["state"], "source": source})
    report = {"schema_version": impact.SCHEMA, "shape": impact.SHAPE, "caller_page": {"schema_version": "caller-map-v2", "query": impact.query(gold), "rows": rows, "generation": generation, "scope": {"repository": binding["evaluation"]["repository"], "commit": commit, "scope_posture": "whole-repository"}, "matching_rows_state": "exact", "total_matching_rows": gold["expected_total_matching_rows"], "pagination": {"complete": True}, "declaration": claim, "caveat": impact.CURRENT_CAVEAT}, "conclusion": {"state": gold["expected_conclusion_state"], "query": impact.query(gold), "generation_digest": generation["generation_digest"], "qualification": impact.QUALIFICATIONS[gold["expected_conclusion_state"]]}, "unavailable_sections": {k: "not_in_supported_shape" for k in impact.SECTIONS}}
    return report, gold, binding, oracle, files


class ContractControls(unittest.TestCase):
    def test_http_schema_metadata_is_bounded_and_extra_top_level_sections_refuse(self):
        original, gold, binding, oracle, files = fixture()
        value = copy.deepcopy(original)
        value["$schema"] = "http://127.0.0.1:3073/schemas/OperationImpactReport.json"
        impact.check_page(value, gold, 100, binding, oracle, binding["evaluation"]["derived_input_commit"], files)
        for metadata in (None, 1, {}, [], "", "x" * 4097, "\u00e9" * 2049):
            with self.subTest(metadata_type=type(metadata).__name__):
                value = copy.deepcopy(original)
                value["$schema"] = metadata
                with self.assertRaises(ValueError):
                    impact.authority(value, gold, binding["evaluation"]["derived_input_commit"])
        for section in ("field_references", "saved_report", "compatibility", "unknown"):
            with self.subTest(section=section):
                value = copy.deepcopy(original)
                value[section] = {}
                with self.assertRaises(ValueError):
                    impact.authority(value, gold, binding["evaluation"]["derived_input_commit"])
        value = copy.deepcopy(original)
        del value["unavailable_sections"]
        with self.assertRaises(ValueError):
            impact.authority(value, gold, binding["evaluation"]["derived_input_commit"])

    def test_resolved_syntax_is_valid_and_unavailable_id_candidate_is_not_promoted(self):
        value, gold, binding, oracle, files = fixture()
        receipt = impact.check_page(value, gold, 100, binding, oracle, binding["evaluation"]["derived_input_commit"], files)
        self.assertEqual(receipt["resolutions"], {"syntax/heuristic": 1})
        self.assertEqual(value["caller_page"]["rows"][0]["unit"]["state"], "unavailable")

    def test_crossed_metadata_tier_source_query_and_unavailable_sections_refuse(self):
        original, gold, binding, oracle, files = fixture()
        for case in ("run", "tier", "oid", "section", "query", "unit", "declaration", "progress", "gap_caveat"):
            with self.subTest(case=case):
                value = copy.deepcopy(original)
                page = value["caller_page"]
                if case == "run": page["rows"][0]["source"]["run_id"] = "sha256:"+"f"*64
                elif case == "tier": page["rows"][0]["tier"] = "exact"
                elif case == "oid": page["rows"][0]["source"]["object_id"] = "f"*40
                elif case == "section": value["unavailable_sections"]["saved_report"] = "available"
                elif case == "query": value["conclusion"]["query"]["owner"] = "foreign"
                elif case == "unit": page["rows"][0]["unit"]["candidates"][0]["owners"] = ["invented"]
                elif case == "declaration": page["declaration"]["sources"][0]["start_byte"] += 1
                elif case == "progress": page["generation"]["partition_progress"]["state"] = "partial"
                else: page["caveat"] = impact.GAP_CAVEAT
                with self.assertRaises(ValueError):
                    impact.check_page(value, gold, 100, binding, oracle, binding["evaluation"]["derived_input_commit"], files)

    def test_full_generation_equality_prevents_same_commit_epoch_mixing(self):
        value, gold, binding, oracle, files = fixture()
        expected = copy.deepcopy(value["caller_page"]["generation"])
        expected["publication_revision"] += 1
        with self.assertRaises(ValueError):
            impact.check_page(value, gold, 100, binding, oracle, binding["evaluation"]["derived_input_commit"], files, expected)

    def test_declaration_run_is_the_actual_consumed_native_publication(self):
        value, _, _, _, _ = fixture()
        selected = {"domain": "proto-contract", "authority_schema": "phebs-partitioned-extraction-domain-v1", "generation_digest": "sha256:"+"a"*64, "root_digest": "sha256:"+"a"*64, "run_id": value["caller_page"]["declaration"]["run_id"]}
        impact.declaration_run(value, selected)
        selected["run_id"] = "other-same-commit-run"
        with self.assertRaises(ValueError):
            impact.declaration_run(value, selected)

    def test_empty_is_scoped_and_unavailable_never_claims_zero(self):
        value, gold, binding, oracle, files = fixture("empty-unary-greeter")
        impact.check_page(value, gold, 100, binding, oracle, binding["evaluation"]["derived_input_commit"], files)
        page = value["caller_page"]
        page.update(matching_rows_state="unavailable")
        page["generation"]["state"] = "stale"
        page["caveat"] = impact.GAP_CAVEAT
        value["conclusion"].update(state="unavailable", qualification=impact.QUALIFICATIONS["unavailable"])
        with self.assertRaises(ValueError):
            impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)
        page.pop("total_matching_rows")
        with self.assertRaises(ValueError):
            impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)
        page.pop("declaration")
        impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)
        page["caveat"] = impact.CURRENT_CAVEAT
        with self.assertRaises(ValueError):
            impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)

    def test_paging_duplicate_is_refused_and_repeats_do_not_increase_metrics(self):
        _, gold, _, oracle, _ = fixture()
        with self.assertRaises(ValueError):
            impact.complete([{"site_ids": gold["expected_site_ids"]}, {"site_ids": gold["expected_site_ids"]}], gold)
        actual = {q["query_id"]: q["expected_site_ids"] for q in oracle["queries"]}
        metric = impact.metrics(oracle, actual)
        self.assertEqual(metric["counts"], {"tp": 9, "fn": 0, "fp": 0, "tn": 36})
        self.assertEqual(metric["scoped_empty_queries"], 2)

    def test_unavailable_source_scopes_preserve_missing_and_retained_generations(self):
        original, gold, binding, _, _ = fixture("empty-unary-greeter")
        for case in ("unindexed", "indexed_before_resolver", "replacement_indexed"):
            with self.subTest(case=case):
                value = copy.deepcopy(original)
                page = value["caller_page"]
                page.update(matching_rows_state="unavailable", caveat=impact.GAP_CAVEAT)
                page.pop("declaration"); page.pop("total_matching_rows")
                value["conclusion"].update(state="unavailable", qualification=impact.QUALIFICATIONS["unavailable"])
                if case == "replacement_indexed":
                    page["generation"]["state"] = "stale"
                    page["scope"]["commit"] = "f" * 40
                else:
                    page["generation"] = {"state": "missing", "plane": "repository-overlay", "repository": page["scope"]["repository"]}
                    value["conclusion"].pop("generation_digest")
                    if case == "unindexed": page["scope"].pop("commit")
                before = copy.deepcopy(value)
                impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)
                self.assertEqual(value, before)
                page["scope"]["repository"] = "foreign.example/repo"
                with self.assertRaises(ValueError):
                    impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)

    def test_current_scope_mismatch_and_malformed_gap_scope_refuse(self):
        value, gold, binding, _, _ = fixture()
        value["caller_page"]["scope"]["commit"] = "f" * 40
        with self.assertRaises(ValueError):
            impact.authority(value, gold, binding["evaluation"]["derived_input_commit"])
        for bad in (None, {}, "unknown", ""):
            with self.subTest(bad=bad):
                value, gold, binding, _, _ = fixture("empty-unary-greeter")
                page = value["caller_page"]
                page.update(matching_rows_state="unavailable", caveat=impact.GAP_CAVEAT)
                page.pop("declaration"); page.pop("total_matching_rows")
                page["generation"]["state"] = "stale"
                page["scope"]["commit"] = bad
                value["conclusion"].update(state="unavailable", qualification=impact.QUALIFICATIONS["unavailable"])
                with self.assertRaises(ValueError):
                    impact.authority(value, gold, binding["evaluation"]["derived_input_commit"], unavailable=True)

    def test_signer_finalizes_both_artifact_sets_first_and_removes_key_on_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root, calls = Path(directory), []
            def command(argv):
                if "genpkey" in argv:
                    Path(argv[-1]).write_text("inert-key-control"); return ""
                self.assertEqual(len(list((root/"artifacts").iterdir())), 6)
                current = json.loads(Path(argv[argv.index("-template")+1]).read_text())
                calls.append(current["pack_id"])
                if len(calls) == 2: raise ValueError("injected second signature refusal")
                Path(argv[-1]).write_text(json.dumps({"pack_id": current["pack_id"], "pack_claim_version": "1.0.0"}))
                return "public key inert-control\n"
            binding = {"tools": {"openssl": {"path": "inert-openssl"}}, "binary": {"path": "inert-binary"}}
            with patch.object(harness.qualify, "command", command), self.assertRaises(ValueError):
                harness.sign_selection(root, binding)
            self.assertEqual(calls, ["phebs.grpc.caller.go", impact.PACK])
            self.assertFalse((root/"disposable-only.pem").exists())

    def test_failed_browser_client_joins_before_private_profile_input_logs_are_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)/"browser"
            class InertOwned:
                def __init__(self, argv, folder, name):
                    self.name, self.child = name, self
                    self.returncode = 0 if name == "chrome" else 1
                    (folder/(name+".log")).write_text("inert")
                    if name == "chrome":
                        profile = folder/"private-chrome-profile"; profile.mkdir()
                        (profile/"DevToolsActivePort").write_text("1234\n")
                def poll(self): return None
                def wait(self, **_): return self.returncode
                def stop(self):
                    self_outer.assertTrue((root/"private-input.json").exists())
                    return {"exit_code": self.returncode, "teardown": "clean", "termination_mode": "already_exited"}
            self_outer = self
            binding = {"tools": {"chrome": {"path": "inert-chrome"}, "node": {"path": "inert-node"}}, "node_modules": "inert-modules", "axe_source": {"path": "inert-axe"}}
            with patch.object(impact_browser.owned, "OwnedChild", InertOwned), self.assertRaises(ValueError):
                impact_browser.run(binding, root, {"token": "inert-private-token"}, lambda: 20)
            record = json.loads((root/"joined-teardown.json").read_text())
            self.assertTrue(record["joined"])
            self.assertTrue(record["private_input_profile_logs_removed"])
            self.assertEqual({p.name for p in root.iterdir()}, {"joined-teardown.json"})


if __name__ == "__main__":
    unittest.main()
