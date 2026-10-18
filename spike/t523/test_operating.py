"""Small synthetic checker/signer controls; no native, source or quality run."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import operating_checks as fields
import qualify_thrift_fields as harness


def envelope():
    records = [{"repository": name, "commit": str(i) * 40} for i, name in enumerate(("eval/apache", "eval/rw"), 1)]
    records[0].update(generator="apache", tier="derived", source_binding="none", module_version="v0.0.0-control", application=["app.go"], generated_prefix="thrift-gen/", field_lineage="control-lineage")
    roots = [{"repository": r["repository"], "domain": fields.DOMAIN, "commit": r["commit"], "run_id": "run" + str(i), "plan_digest": "sha256:" + "a" * 64, "root_digest": "sha256:" + "b" * 64} for i, r in enumerate(records)]
    version, params = "control-selected-set", fields.query(records[0], "scope.Message#1")
    atom = {"atom_id": "atom", "schema_version": "t22-v1", "rule_id": "scip-thrift-field-reference-v1", "extractor_version": version, "blob_digest": "sha256:" + hashlib.sha256(b"Field").hexdigest(), "start_byte": 0, "end_byte": 5}
    occurrence = {"atom_id": "atom", "occurrence_id": "occurrence", "repo": records[0]["repository"], "commit": records[0]["commit"], "run_id": "run0", "path": "app.go", "start_line": 1, "end_line": 1, "visibility_scope": "repo:" + records[0]["repository"]}
    assertion = {"id": "assertion", "repo": records[0]["repository"], "run_id": "run0", "predicate": "REFERENCES_THRIFT_FIELD", "subject": "app.go:0-5", "object": "scope.Message#1", "lineage": params["lineage"], "tier": "derived", "code_role": "production", "supporting": ["atom"], "detail": json.dumps({"schema": "thrift-field-reference-detail-v1", "name": "field", "dependency_version": "v0.0.0-control", "generator": "apache", "source_binding": "none", "classification": "read"})}
    bundle = {"schema_version": "proof-bundle-v1", "query": {"kind": fields.ENDPOINT, **params, "domains": [fields.DOMAIN]}, "coverage": {"domains": [fields.DOMAIN], "repositories": [{"repository": r["repository"], "indexed_commit": r["commit"]} for r in records]}, "exact_authorities": roots, "extractor_versions": [{"repository": r["repository"], "domain": fields.DOMAIN, "run_id": r["run_id"], "extractor": version} for r in roots], "evidence": [{"repository": records[0]["repository"], "run_id": "run0", "atom": atom, "occurrences": [occurrence]}], "assertions": [assertion]}
    value = {"bundle": bundle}
    reidentify(value)
    labels = {"apache": [{"expected": "positive", "object": "scope.Message#1", "path": "app.go", "start_byte": 0, "end_byte": 5}]}
    return value, params, records, version, labels


def reidentify(value):
    value["id"] = "pb_" + hashlib.sha256(fields.common.canonical(value["bundle"])).hexdigest()


class ProofControls(unittest.TestCase):
    def check(self, value, params, records, version, labels, selected=("apache",), expected=None):
        return fields.check_bundle(value, params, records[0], records, selected, version, {"apache": {"app.go": b"Field"}}, labels, {"apache": {"scope.Message#1": "field"}}, expected)

    def test_current_positive_and_coherent_unselected_empty(self):
        value, params, records, version, labels = envelope()
        self.assertEqual(self.check(value, params, records, version, labels)["consumer_spans"], [("app.go", 0, 5)])
        value["bundle"].update(evidence=[], assertions=[])
        reidentify(value)
        self.assertEqual(self.check(value, params, records, version, labels, selected=())["consumer_spans"], [])
        value["bundle"]["exact_authorities"].pop()
        reidentify(value)
        with self.assertRaises(ValueError):
            self.check(value, params, records, version, labels, selected=())

    def test_wrong_source_generation_tier_binding_and_duplicates_refuse(self):
        original, params, records, version, labels = envelope()
        cases = ("root", "version", "commit", "tier", "binding", "duplicate", "missing", "unselected", "mixed_epoch")
        for case in cases:
            with self.subTest(case=case):
                value = copy.deepcopy(original)
                bundle = value["bundle"]
                selected, expected = ("apache",), None
                if case == "root":
                    bundle["exact_authorities"][0]["domain"] = "scip-proto-field"
                elif case == "version":
                    bundle["extractor_versions"][0]["extractor"] = "old-selected-set"
                elif case == "commit":
                    bundle["evidence"][0]["occurrences"][0]["commit"] = "f" * 40
                elif case == "tier":
                    bundle["assertions"][0]["tier"] = "exact"
                elif case == "binding":
                    detail = json.loads(bundle["assertions"][0]["detail"]); detail["source_binding"] = "module_digest"
                    bundle["assertions"][0]["detail"] = json.dumps(detail)
                elif case == "duplicate":
                    bundle["assertions"].append(copy.deepcopy(bundle["assertions"][0]))
                elif case == "missing":
                    bundle.update(evidence=[], assertions=[])
                elif case == "unselected":
                    selected = ()
                else:
                    expected = copy.deepcopy(bundle["exact_authorities"])
                    expected[0]["root_digest"] = "sha256:" + "c" * 64
                reidentify(value)
                with self.assertRaises(ValueError):
                    self.check(value, params, records, version, labels, selected, expected)

    def test_field_zero_preserved(self):
        self.assertEqual(fields.query({"field_lineage": "scope"}, "health.Meta_Health_Result#0")["field_number"], 0)

    def test_zero_fact_field_zero_receipt_claims_only_transport_admission(self):
        value, _, records, version, _ = envelope()
        records[0]["generator"] = "thriftrw"
        params = fields.query(records[0], "health.Meta_Health_Result#0")
        value["bundle"].update(query={"kind": fields.ENDPOINT, **params, "domains": [fields.DOMAIN]}, assertions=[], evidence=[])
        reidentify(value)
        receipt = fields.check_bundle(value, params, records[0], records, ("thriftrw",), version, {"thriftrw": {}}, {"thriftrw": []}, {"thriftrw": {}})
        self.assertEqual(receipt["consumer_spans"], [])
        self.assertEqual(receipt["field0_mechanics"]["returned_generated_references"], 0)
        self.assertFalse(receipt["field0_mechanics"]["live_reference_claim"])

    def test_both_pack_artifact_census_fixed_before_either_signature_and_key_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            root, signed = Path(directory), []
            def command(argv):
                if "genpkey" in argv:
                    Path(argv[-1]).write_text("inert-key-marker")
                    return ""
                artifacts = Path(argv[argv.index("-artifacts") + 1])
                self.assertEqual(len(list(artifacts.iterdir())), 6)
                current = json.loads(Path(argv[argv.index("-template") + 1]).read_text())
                signed.append(current["pack_id"])
                Path(argv[-1]).write_text(json.dumps({"pack_id": current["pack_id"], "pack_claim_version": "1.0.0"}))
                return "public key control-only\n"
            binding = {"tools": {"openssl": {"path": "control-openssl"}}, "binary": {"path": "control-binary"}}
            with patch.object(harness.qualify, "command", command):
                harness.sign_selection(root, binding)
            self.assertEqual(signed, list(fields.PACKS.values()))
            self.assertFalse((root / "disposable-only.pem").exists())


if __name__ == "__main__":
    unittest.main()
