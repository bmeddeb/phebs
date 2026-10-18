"""Pure/static adversarial controls; no product, subprocess, socket or browser."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import field_checks as fields
import qualify_fields as harness
import score


def fixture():
    repo, commit = "evaluation.local/public", "a" * 40
    source = b"package p\nvar x = message.Enabled\n"
    start, end = source.index(b"Enabled"), source.index(b"Enabled") + 7
    path = "profiling/service/service.go"
    lineage, object_id = "contract_scip_package_v1_" + "f" * 64, "p.Message#1"
    root = {"repository": repo, "domain": fields.DOMAIN, "run_id": "run-field", "commit": commit, "plan_digest": "sha256:" + "1" * 64, "root_digest": "sha256:" + "2" * 64}
    params = {"lineage": lineage, "message": "p.Message", "field_number": 1}
    occurrence = {"occurrence_id": "occurrence", "atom_id": "atom", "repo": repo, "commit": commit, "path": path, "start_line": 2, "end_line": 2, "visibility_scope": "repo:" + repo, "run_id": root["run_id"]}
    atom = {"atom_id": "atom", "schema_version": "t13-v1", "blob_digest": "sha256:" + hashlib.sha256(source).hexdigest(), "start_byte": start, "end_byte": end, "rule_id": "scip-proto-field-reference-v1", "extractor_version": fields.VERSION}
    assertion = {"id": "assertion", "predicate": "REFERENCES_PROTO_FIELD", "subject": path + ":" + str(start) + "-" + str(end), "object": object_id, "lineage": lineage, "tier": "exact", "code_role": "production", "repo": repo, "run_id": root["run_id"], "supporting": ["atom"], "detail": json.dumps({"schema": "proto-field-reference-detail-v1", "name": "enabled", "classification": "read", "dependency_version": "v1.80.0"})}
    bundle = {"schema_version": "proof-bundle-v1", "query": {"kind": fields.ENDPOINTS[0], **params, "domains": [fields.DOMAIN]}, "assertions": [assertion], "evidence": [{"repository": repo, "run_id": root["run_id"], "atom": atom, "occurrences": [occurrence]}], "exact_authorities": [root], "coverage": {"domains": [fields.DOMAIN], "repositories": [{"repository": repo, "indexed_commit": commit}], "digest": "coverage"}, "extractor_versions": [{"repository": repo, "domain": fields.DOMAIN, "run_id": root["run_id"], "extractor": fields.VERSION}]}
    value = {"bundle": bundle}; resign(value)
    labels = [{"path": path, "start_byte": start, "end_byte": end, "label": "positive", "object": object_id}]
    return value, params, repo, commit, {path: source}, labels, {object_id: "enabled"}


def resign(value):
    value["id"] = "pb_" + hashlib.sha256(fields.canonical(value["bundle"])).hexdigest()


def projection(value):
    bundle = value["bundle"]; a = bundle["assertions"][0]; o = bundle["evidence"][0]["occurrences"][0]
    fact = {"fact_id": a["id"], "logical_relationship_id": a["id"], "predicate": a["predicate"], "subject": {"kind": "source_occurrence", "id": o["occurrence_id"]}, "object": {"kind": "proto_field", "id": a["lineage"] + ":" + a["object"]}, "semantic_resolution": {"value": "resolved"}, "qualifiers": {"repository": a["repo"], "tier": a["tier"], "lineage": a["lineage"], "path": o["path"], "commit": o["commit"], "start_line": str(o["start_line"]), "end_line": str(o["end_line"]), "code_role": a["code_role"]}, "proof_references": [{"occurrence_id": o["occurrence_id"], "proof_id": value["id"] + "#atom", "snapshot_id": o["commit"], "verification_action": "GET /api/proof_bundles/" + value["id"]}]}
    fact.update(evidence_basis="evidenced", verification_tool="proof_bundle_api", attribution_states={hop: {"state": "unresolved"} for hop in ("build_target", "deployable", "owner", "service")})
    return {"structuredContent": {"request_id": value["id"], "tool": {"name": fields.ENDPOINTS[0], "semantic_version": "1.0"}, "outcome": "partial", "payload": {"kind": "contract_edges", "data": {"facts": [fact]}}, "coverage": {"visibility": "withheld", "processing": None, "processing_reconciled": False}, "result_window": {"result_set_complete": False, "page_exhausted": True, "truncated": False, "returned": 1}, "absence_eligibility": {"eligible": False}, "provenance": {"input_manifest_id": "coverage", "extractor_version": fields.VERSION}}}


class FieldHarnessTests(unittest.TestCase):
    def test_accepted_post_discovery_source_and_image_binding(self):
        plan = json.loads((fields.HERE / "operating-plan.json").read_text())
        amendment = json.loads((fields.HERE / "post-discovery-execution-amendment.json").read_text())
        selected = amendment["implementation_binding"]
        binding = {"source_commit": selected["source_commit"], "binary": {"path": selected["binary"], "sha256": "sha256:" + selected["binary_sha256"]}}
        harness.validate_implementation_binding(binding, plan)
        changes = [
            lambda b: b.update(source_commit=plan["implementation_binding"]["source_commit"]),
            lambda b: b["binary"].update(path=plan["implementation_binding"]["binary"]),
            lambda b: b["binary"].update(sha256="sha256:" + plan["implementation_binding"]["binary_sha256"]),
        ]
        for change in changes:
            bad = copy.deepcopy(binding); change(bad)
            with self.assertRaises(score.Mismatch): harness.validate_implementation_binding(bad, plan)

    def test_post_discovery_amendment_refuses_original_contract_or_acceptance_drift(self):
        plan = json.loads((fields.HERE / "operating-plan.json").read_text())
        amendment = json.loads((fields.HERE / "post-discovery-execution-amendment.json").read_text())
        selected = amendment["implementation_binding"]
        binding = {"source_commit": selected["source_commit"], "binary": {"path": selected["binary"], "sha256": "sha256:" + selected["binary_sha256"]}}
        changes = [
            lambda a: a.update(schema="other"),
            lambda a: a.update(accepted=False),
            lambda a: a.update(accepted=1),
            lambda a: a.update(original_plan_digest="sha256:" + "0" * 64),
            lambda a: a["original_implementation_binding"].update(source_commit="0" * 40),
            lambda a: a["original_implementation_binding"].update(binary="/unreviewed/image"),
            lambda a: a["original_implementation_binding"].update(binary_sha256="0" * 64),
        ]
        for change in changes:
            bad = copy.deepcopy(amendment); change(bad)
            with patch.object(harness.checks, "bounded_json", return_value=bad):
                with self.assertRaises(score.Mismatch): harness.validate_implementation_binding(binding, plan)

    def test_live_source_default_branch_must_be_input(self):
        for branch in ("input\n", "other\n"):
            with patch.object(harness.qualify, "command", return_value=branch) as command:
                if branch.startswith("input"):
                    harness.verify_source_branch("git", "bound-source")
                else:
                    with self.assertRaises(score.Mismatch):
                        harness.verify_source_branch("git", "bound-source")
                command.assert_called_once_with(["git", "symbolic-ref", "--short", "HEAD"], cwd="bound-source")

    def test_exact_frozen_source_labels_and_transitions(self):
        plan, population, labels, files, contents, transition, names = fields.frozen()
        self.assertEqual(len(labels), 70)
        self.assertEqual(len(files), 8)
        self.assertEqual(sum(row["label"] == "positive" for row in labels), 14)
        self.assertEqual(population["field_lineage"], plan["population"]["field_lineage"])
        self.assertEqual(len(names), 11)
        self.assertEqual(fields.git_blob(contents[transition["path"]]), transition["base_git_blob"])

    def check(self, value, expected=None, labels=None):
        _, params, repo, commit, contents, truth, names = fixture()
        return fields.check_bundle(value, fields.ENDPOINTS[0], params, repo, commit, contents, truth if labels is None else labels, names, expected)

    def test_valid_complete_proof(self):
        value, *_ = fixture()
        receipt = self.check(value)
        self.assertEqual(len(receipt["consumer_spans"]), 1)
        self.assertEqual(receipt["proof_id"], value["id"])

    def test_same_commit_wrong_root_run_version_and_domain_refused(self):
        value, *_ = fixture(); expected = copy.deepcopy(value["bundle"]["exact_authorities"][0])
        changes = [
            lambda b: b["exact_authorities"][0].update(root_digest="sha256:" + "9" * 64),
            lambda b: b["exact_authorities"][0].update(run_id="other"),
            lambda b: b["extractor_versions"][0].update(extractor="1.4.0"),
            lambda b: b["exact_authorities"][0].update(domain="proto-contract"),
            lambda b: b["coverage"]["repositories"][0].update(indexed_commit="b" * 40),
            lambda b: b.update(exact_authorities=[]),
        ]
        for change in changes:
            with self.subTest(change=change):
                bad = copy.deepcopy(value); change(bad["bundle"]); resign(bad)
                with self.assertRaises(score.Mismatch): self.check(bad, expected)

    def test_extra_negative_duplicate_missing_and_crossed_source_refused(self):
        value, *_ = fixture()
        changes = [
            lambda b: b["evidence"][0]["atom"].update(blob_digest="sha256:" + "f" * 64),
            lambda b: b["evidence"][0]["atom"].update(end_byte=b["evidence"][0]["atom"]["end_byte"] + 1),
            lambda b: b["evidence"][0]["occurrences"][0].update(commit="b" * 40),
            lambda b: b["evidence"][0]["occurrences"][0].update(start_line=1),
            lambda b: b["assertions"][0].update(lineage="other"),
            lambda b: b["assertions"][0].update(tier="heuristic"),
            lambda b: b["assertions"].append(copy.deepcopy(b["assertions"][0])),
            lambda b: b.update(assertions=[]),
        ]
        for change in changes:
            bad = copy.deepcopy(value); change(bad["bundle"]); resign(bad)
            with self.assertRaises(score.Mismatch): self.check(bad)
        truth = fixture()[5]; truth[0]["label"] = "negative"
        with self.assertRaises(score.Mismatch): self.check(value, labels=truth)

    def test_content_hash_refuses_even_same_rows(self):
        value, *_ = fixture(); value["bundle"]["query"]["message"] = "other"
        with self.assertRaises(score.Mismatch): self.check(value)

    def test_mcp_is_projection_with_partial_withheld_semantics(self):
        value, *_ = fixture()
        fields.check_mcp(projection(value), fields.ENDPOINTS[0], value)
        changes = [
            lambda e: e.update(request_id="other"),
            lambda e: e.update(outcome="complete"),
            lambda e: e["coverage"].update(visibility="complete"),
            lambda e: e["result_window"].update(result_set_complete=True),
            lambda e: e["result_window"].update(returned=9),
            lambda e: e["absence_eligibility"].update(eligible=True),
            lambda e: e["payload"]["data"]["facts"][0]["attribution_states"]["service"].update(state="resolved"),
            lambda e: e["payload"]["data"]["facts"][0]["proof_references"][0].update(snapshot_id="other"),
            lambda e: e["payload"]["data"]["facts"][0]["qualifiers"].update(commit="other"),
            lambda e: e["payload"]["data"].update(facts=[]),
            lambda e: e["provenance"].update(extractor_version="1.4.0"),
        ]
        for change in changes:
            bad = projection(value); change(bad["structuredContent"])
            with self.assertRaises(score.Mismatch): fields.check_mcp(bad, fields.ENDPOINTS[0], value)

    def test_prospective_matrix_reuses_joined_http_and_server_controls(self):
        self.assertIs(harness.FieldMatrix.start, harness.shared.Matrix.start)
        self.assertIs(harness.FieldMatrix.stop, harness.shared.Matrix.stop)
        self.assertIs(harness.FieldMatrix.request, harness.shared.Matrix.request)
        self.assertIs(harness.FieldMatrix.write_rewrite, harness.shared.Matrix.write_rewrite)
        self.assertIs(harness.FieldMatrix.mcp, harness.shared.Matrix.mcp)

    def test_publication_success_after_deadline_stops(self):
        value, *_ = fixture()
        matrix = object.__new__(harness.FieldMatrix)
        matrix.deadline = Mock(); matrix.deadline.timeout.return_value = 90
        matrix.bounds = {"publication_seconds": 90, "cold_request_seconds": 2}
        matrix.population = {"field_lineage": fixture()[1]["lineage"]}
        matrix.current_commit = fixture()[3]
        value["bundle"]["coverage"]["repositories"][0]["runs"] = []
        matrix.get = Mock(return_value=(200, json.dumps(value).encode()))
        with patch.object(harness.time, "monotonic", side_effect=[0, 91]):
            with self.assertRaises(score.Mismatch): matrix.exact("p.Message#1")

    def test_cleanup_never_deletes_when_server_or_sampler_not_joined(self):
        for server, sampler in ((False, None), (True, Mock(joined=False))):
            matrix = Mock(server_groups_drained=server, monitor=sampler)
            with patch.object(harness.shutil, "rmtree") as remove:
                with self.assertRaises(score.Mismatch): harness.cleanup(matrix, Path("/private/unused"), True)
                remove.assert_not_called()

    def test_failed_cleanup_retains_raw_and_source_free_receipt_removes_secrets_and_data(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "attempt"; root.mkdir()
            raw = root / "private-raw"; raw.mkdir()
            body = b'{"actual":"first response"}'
            (raw / "response-0001.json").write_bytes(body)
            for name in ("config.json", "gitconfig", "disposable-only.pem"):
                (root / name).write_text("private mock auth marker")
            for name in ("data", "harness", "selection", "artifacts"):
                (root / name).mkdir(); (root / name / "marker").write_text("private scratch marker")
            matrix = Mock(server_groups_drained=True, monitor=None, bounds={"http_response_bytes": 1048576}, report={"phases": [{"phase": "stopped", "terminal": {"teardown": "clean"}}], "reads": [{"raw_digest": "sha256:" + hashlib.sha256(body).hexdigest()}]})
            harness.cleanup(matrix, root, True)
            self.assertEqual(sorted(p.name for p in root.iterdir()), ["private-raw", "source-free-diagnostics.json"])
            self.assertEqual((raw / "response-0001.json").read_bytes(), body)
            self.assertEqual(matrix.report["retained_stop_evidence"]["raw_files"], 1)
            self.assertEqual(matrix.report["retained_stop_evidence"]["raw_bytes"], len(body))
            self.assertNotIn("private mock auth marker", (root / "source-free-diagnostics.json").read_text())
            self.assertTrue(matrix.report["retained_stop_evidence"]["auth_config_key_data_removed_after_join"])

    def test_success_cleanup_removes_whole_scratch(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "attempt"; root.mkdir()
            (root / "config.json").write_text("private mock auth marker")
            matrix = Mock(server_groups_drained=True, monitor=None, report={})
            harness.cleanup(matrix, root, False)
            self.assertFalse(root.exists())
            self.assertTrue(matrix.report["private_scratch_removed_after_join"])


    def test_generated_paths_are_separate_from_consumer_denominator(self):
        value, *_ = fixture()
        value["bundle"]["evidence"][0]["occurrences"][0]["path"] = "unexpected.go"
        resign(value)
        with self.assertRaises(score.Mismatch): self.check(value)

    def test_disposable_selection_is_field_only_and_key_marker_always_removed(self):
        for fail in (False, True):
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                calls = []
                binding = {"tools": {"openssl": {"path": "reviewed-openssl"}}, "binary": {"path": "reviewed-phebs"}}
                def command(argv):
                    calls.append(argv)
                    if argv[0] == "reviewed-openssl":
                        Path(argv[-1]).write_text("inert mock marker; not a key")
                        return ""
                    if fail:
                        raise ValueError("mock signer refusal")
                    output = Path(argv[argv.index("-out") + 1])
                    output.write_text(json.dumps({"pack_id": fields.PACK, "pack_claim_version": "1.0.0"}))
                    return "public key inert-mock-public-marker\n"
                with patch.object(harness.qualify, "command", side_effect=command):
                    if fail:
                        with self.assertRaises(ValueError): harness.sign_selection(root, binding)
                    else:
                        artifacts, selection, public, receipt = harness.sign_selection(root, binding)
                        self.assertEqual(len(list(artifacts.iterdir())), 3)
                        self.assertEqual([p.name for p in selection.iterdir()], ["protobuf-fields.json"])
                        self.assertEqual(receipt["record"]["pack_id"], fields.PACK)
                        template = json.loads((root / "field-template.json").read_text())
                        self.assertEqual(template["pack_id"], fields.PACK)
                self.assertEqual([args[0] for args in calls], ["reviewed-openssl", "reviewed-phebs"])
                self.assertFalse((root / "disposable-only.pem").exists())


if __name__ == "__main__":
    unittest.main()
