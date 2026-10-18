#!/usr/bin/env python3
"""Prospective finite T513 ordinary field-only matrix; requires lead review."""
from concurrent.futures import ThreadPoolExecutor
import argparse
import hashlib
import hmac
import json
import os
from pathlib import Path
import shutil
import time

import field_checks as fields
import requalify as shared
import requalification_checks as checks
import qualify
import score

HERE, REPO = fields.HERE, fields.REPO
OWN_FILES = ("qualify_fields.py", "field_checks.py", "test_fields.py", "README.md")
REUSED_FILES = (*sorted(shared.HARNESS_FILES), "qualify.py", "score.py")
DARK_CAPABILITIES = (shared.DARK_CAPABILITIES - {"protobuf-field-references"}) | {"contract-caller-map", "contract-caller-comparison", "contract-atlas"}
DARK_TOOLS = (shared.DARK_TOOLS - set(fields.ENDPOINTS)) | {"list_operation_callers", "compare_operation_callers", "find_operation_consumers", "get_extraction_coverage", "list_contracts", "read_contract"}


def validate_binding(binding, plan):
    score.require(binding["schema"] == "phebs-t513-execution-binding-v1" and binding["reviewed"] is True and binding["plan_digest"] == checks.sha(HERE / "operating-plan.json"), "field execution binding not reviewed")
    score.require(binding["source_commit"] == plan["implementation_binding"]["source_commit"] and binding["binary"]["sha256"] == "sha256:" + plan["implementation_binding"]["binary_sha256"], "field implementation binding drift")
    score.require(qualify.command(["git", "rev-parse", "HEAD"], cwd=REPO).strip() == binding["harness_commit"] and not qualify.command(["git", "status", "--porcelain", "--untracked-files=no"], cwd=REPO).strip(), "field harness is not the reviewed clean commit")
    score.require(set(binding["harness_files"]) == set(OWN_FILES) and set(binding["reused_t503_files"]) == set(REUSED_FILES), "field binding omits owned/reused code")
    for folder, items in ((HERE, binding["harness_files"]), (REPO / "spike/t503", binding["reused_t503_files"])):
        for name, expected in items.items():
            score.require(checks.sha(folder / name) == expected, "field/reused harness code drift")
    for item in [binding["binary"], binding["observer"], *binding["tools"].values()]:
        path = Path(item["path"])
        score.require(path.is_absolute() and path.is_file() and checks.sha(path) == item["sha256"] and path.stat().st_size == item["bytes"], "field bound image/tool drift")
    score.require(binding["tools"]["openssl"]["path"] == "/opt/homebrew/opt/openssl@3/bin/openssl", "field selection needs admitted Ed25519-capable image")
    metadata = qualify.command([binding["tools"]["go"]["path"], "version", "-m", binding["binary"]["path"]])
    score.require("vcs.revision=" + binding["source_commit"] in metadata and "vcs.modified=false" in metadata and "sha256:" + hashlib.sha256(metadata.encode()).hexdigest() == binding["binary"]["metadata_sha256"], "field binary lacks exact native clean metadata")
    build = checks.bounded_json(Path(binding["build_receipt"]["path"]), 65536)
    helpers = checks.bounded_json(Path(binding["helper_build_receipt"]["path"]), 65536)
    for key in ("build_receipt", "helper_build_receipt"):
        score.require(checks.sha(Path(binding[key]["path"])) == binding[key]["sha256"], "field build receipt drift")
    score.require(build["source_commit"] == binding["source_commit"] and build["binary_sha256"] == binding["binary"]["sha256"].removeprefix("sha256:") and build["source_clean_before"] and build["source_clean_after"], "field image preparation differs")
    expected = {filename: binding["tools"][key]["sha256"].removeprefix("sha256:") for key, filename in shared.HELPERS.items()}
    expected["native-observer"] = binding["observer"]["sha256"].removeprefix("sha256:")
    score.require({row["name"]: row["sha256"] for row in helpers["runs"]} == expected and all(row["exit"] == 0 and row["joined"] for row in helpers["runs"]), "field helper preparation differs")
    return metadata


def sign_selection(root, binding):
    """One isolated disposable field selection; no other recipe artifacts."""
    artifacts, selection = root / "artifacts", root / "selection"
    artifacts.mkdir(); selection.mkdir()
    stem = "protobuf-fields"
    descriptions = {
        "card": (".md", "# T513 disposable field integration selection\nProspective finite measurement only; no production signature or result.\n"),
        "manifest": (".json", json.dumps({"schema": "t513-disposable-manifest-v1", "pack_id": fields.PACK, "extractor": fields.VERSION, "plan_digest": checks.sha(HERE / "operating-plan.json"), "production_release": False})),
        "validation": (".json", json.dumps({"schema": "t513-disposable-validation-v1", "measurement": "prospective_in_progress", "plan_digest": checks.sha(HERE / "operating-plan.json"), "production_release": False})),
    }
    template = checks.bounded_json(REPO / "spike/t475/release/receipts/template.json", 65536)
    template.update(pack_id=fields.PACK, release_id="rel-" + fields.PACK + "-1.0.0", approved_at="2026-10-18T00:00:00Z", approval_records=["t513-disposable-key-measurement-only"])
    template["validation"]["expires_at"] = "2027-01-16T23:59:59Z"
    for kind, (extension, text) in descriptions.items():
        name = stem + "-" + kind + "-1.0.0" + extension
        (artifacts / name).write_text(text)
        template[kind]["artifact_id"] = name
    template_path = root / "field-template.json"
    template_path.write_text(json.dumps(template))
    private = root / "disposable-only.pem"
    output = selection / "protobuf-fields.json"
    try:
        qualify.command([binding["tools"]["openssl"]["path"], "genpkey", "-algorithm", "ED25519", "-out", private])
        private.chmod(0o600)
        result = qualify.command([binding["binary"]["path"], "release-sign", "-template", template_path, "-key", private, "-key-id", "t513-integration-only", "-artifacts", artifacts, "-out", output])
        public = next(line.removeprefix("public key ") for line in result.splitlines() if line.startswith("public key "))
        record = checks.bounded_json(output, 65536)
        score.require(record["pack_id"] == fields.PACK and record["pack_claim_version"] == "1.0.0" and list(selection.iterdir()) == [output], "disposable field selection scope differs")
        return artifacts, selection, public, {"record": record, "record_digest": checks.sha(output)}
    finally:
        private.unlink(missing_ok=True)


class FieldMatrix(shared.Matrix):
    """Reuse reviewed HTTP, owned lifecycle, atomic rewrite, MCP and sampler."""
    def __init__(self, root, binding, plan, population, labels, contents, transition, names):
        role = {"role": "public", "repository": plan["population"]["repository"], "local_path": plan["population"]["base_local_path"]}
        super().__init__(root, binding, labels, {"roles": [role]}, transition, plan["bounds"])
        self.plan, self.population, self.contents, self.transition, self.names = plan, population, contents, transition, names
        self.env.pop("PHEBS_T472_SOURCE_OUTCOME_ROOT", None)
        self.env.pop("PHEBS_BUF", None)
        self.report.update(schema="phebs-t513-finite-operating-v1", qualification="Finite selected public source conformance and measured Darwin local envelope only; production signing pending. No dedicated field UI/browser claim.")
        self.current_commit = plan["population"]["base_commit"]
        self.repository = role["repository"]
        self.first = None

    def proof(self, endpoint, object_id, maximum, expected=None):
        params = fields.query(self.population, object_id)
        status, raw = self.get("/api/" + endpoint, params, maximum)
        score.require(status == 200, "field whole-proof request refused")
        value = json.loads(raw)
        receipt = fields.check_bundle(value, endpoint, params, self.repository, self.current_commit, self.contents, self.labels, self.names, expected)
        return value, receipt

    def exact(self, object_id):
        limit = time.monotonic() + self.deadline.timeout(self.bounds["publication_seconds"])
        params = fields.query(self.population, object_id)
        while True:
            status, raw = self.get("/api/" + fields.ENDPOINTS[0], params, self.bounds["cold_request_seconds"])
            score.require(status in (200, 404, 409), "unexpected field publication status")
            if status == 200:
                value = json.loads(raw)
                coverage = value["bundle"]["coverage"]
                for repository in coverage["repositories"]:
                    for run in repository["runs"]:
                        score.require(not run.get("failures") and (run.get("outcome") or {}).get("disposition") not in ("terminal_refusal", "failed"), "terminal field extraction refusal")
                roots = value["bundle"].get("exact_authorities", [])
                if roots and roots[0].get("commit") == self.current_commit:
                    score.require(time.monotonic() <= limit, "successful field publication crossed local deadline")
                    self.deadline.remaining()
                    receipt = fields.check_bundle(value, fields.ENDPOINTS[0], params, self.repository, self.current_commit, self.contents, self.labels, self.names)
                    return value, receipt
            score.require(time.monotonic() < limit, "field publication deadline exceeded")
            time.sleep(min(0.25, self.deadline.remaining()))

    def discovery(self, enabled):
        status, raw = self.get("/api/version")
        score.require(status == 200, "field capability discovery failed")
        capabilities = json.loads(raw).get("capabilities", [])
        tools = {t["name"] for t in self.mcp("tools/list", {})["tools"]}
        score.require(("protobuf-field-references" in capabilities) == enabled and all((name in tools) == enabled for name in fields.ENDPOINTS), "field signature/discovery admission differs")
        score.require(not DARK_CAPABILITIES.intersection(capabilities) and not DARK_TOOLS.intersection(tools), "unrelated product admitted by field selection")
        self.report.setdefault("discovery", []).append({"field_enabled": enabled, "capabilities": capabilities, "tools": sorted(tools)})

    def immutable(self, value):
        status, raw = self.get("/api/proof_bundles/" + value["id"], maximum=self.bounds["cold_request_seconds"])
        score.require(status == 200, "immutable field proof unavailable")
        saved = json.loads(raw)
        score.require(saved["id"] == value["id"] and fields.canonical(saved["bundle"]) == fields.canonical(value["bundle"]), "retained immutable field content changed")
        return value["id"]

    def mcp_projection(self, endpoint, object_id, value):
        result = self.tool(endpoint, fields.query(self.population, object_id))
        projected = fields.check_mcp(result, endpoint, value)
        # Projection provenance may name a separately built same-content proof.
        # Its ID is verified here by the ordinary authenticated HTTP-only read.
        score.require(projected["request_id"] == self.immutable(value), "MCP provenance proof identity differs")

    def source(self, value, contents):
        by_path = {}
        for item in value["bundle"]["evidence"]:
            for occurrence in item["occurrences"]:
                if occurrence["path"] == self.population["consumer"]:
                    by_path.setdefault(occurrence["path"], []).append(item["atom"])
        receipts = []
        for path, atoms in by_path.items():
            params = {"repo": self.repository, "ref": self.current_commit, "path": path}
            status, raw = self.get("/api/source", params, self.bounds["cold_request_seconds"])
            score.require(status == 200, "immutable field source HTTP refused")
            source = json.loads(raw)
            expected = contents[path]
            score.require(source["encoding"] == "utf8" and source["size"] == len(expected) and source["content"].encode() == expected, "field HTTP source bytes differ")
            result = self.tool("read_file", params)
            score.require(not result.get("isError"), "MCP immutable field source refused")
            mcp = result["structuredContent"]
            # read_file deliberately removes exactly one final newline.
            score.require(not mcp.get("truncated") and mcp["content"] == expected.decode().removesuffix("\n") and mcp["start_line"] == 1 and mcp["end_line"] == mcp["total_lines"], "MCP full source projection differs")
            for atom in atoms:
                start, end = atom["start_byte"], atom["end_byte"]
                score.require(source["content"].encode()[start:end] == expected[start:end] and mcp["content"].encode()[start:end] == expected[start:end], "field source byte-slice differs")
            receipts.append({"path": path, "commit": self.current_commit, "blob_digest": "sha256:" + hashlib.sha256(expected).hexdigest(), "unique_consumer_atoms_sliced": len({a["atom_id"] for a in atoms}), "transports": ["GET /api/source full blob", "MCP read_file full blob"], "blob_read_count": "one full immutable blob per transport request by source-code contract; no instrumented counter"})
        return receipts

    def quality(self):
        objects = sorted(self.plan["population"]["objects"])
        initial, initial_receipt = self.exact(objects[0])
        expected = initial_receipt["authority"]
        self.report["publication_metadata"] = [fields.publication_metadata(self.root / "data", expected)]
        self.discovery(True)
        score.require(self.get("/api/" + fields.ENDPOINTS[0], fields.query(self.population, objects[0]), authenticated=False)[0] == 401, "anonymous field query admitted")
        rows, source_evidence, bundles = [], {}, set()
        consumer_spans, generated_spans = set(), set()
        for object_id in objects:
            for endpoint in fields.ENDPOINTS:
                # Probes/shared caches are disclosed: cold means first success
                # for this object/endpoint, never an untouched process cache.
                value, receipt = self.proof(endpoint, object_id, self.bounds["cold_request_seconds"], expected)
                self.mcp_projection(endpoint, object_id, value)
                bundles.add(value["id"])
                for item in value["bundle"]["evidence"]:
                    if item["occurrences"][0]["path"] == self.population["consumer"]:
                        source_evidence[item["atom"]["atom_id"]] = item
                consumer_spans.update(tuple(s) for s in receipt["consumer_spans"])
                generated_spans.update(tuple(s) for s in receipt["generated_spans_outside_quality_denominator"])
                for _ in range(5):
                    self.proof(endpoint, object_id, self.bounds["warm_request_seconds"], expected)
                rows.append({"endpoint": endpoint, "object": object_id, **receipt, "warm_reads": 5})
                self.first = self.first or value
        score.require(len(consumer_spans) == 14, "public stable field consumer denominator differs")
        sources = self.source({"bundle": {"evidence": list(source_evidence.values())}}, self.contents)
        self.report["quality"] = {"public_positives": 14, "consumer_unique_spans": len(consumer_spans), "frozen_negative_sites": 56, "false_positives": 0, "generated_unique_spans_outside_quality_denominator": len(generated_spans), "objects_both_endpoints": rows, "immutable_proofs_verified": sorted(bundles), "source_reads": sources, "cold_caveat": "first success per object/endpoint after explicit publication probes; process/shared caches may be warm"}
        batches = []
        for count in (1, 4, 8):
            clock = time.monotonic()
            with ThreadPoolExecutor(max_workers=count) as workers:
                pending = [workers.submit(self.proof, fields.ENDPOINTS[i % 2], objects[i % len(objects)], self.bounds["cold_request_seconds"], expected) for i in range(count)]
                for task in pending:
                    task.result(timeout=self.deadline.remaining())
            elapsed = time.monotonic() - clock
            self.check()
            batches.append({"clients": count, "whole_proof_requests": count, "wall_seconds": round(elapsed, 6), "requests_per_second": count / elapsed})
        self.report["whole_proof_concurrency"] = batches

    def publication(self):
        old = self.first
        self.active_roles["public"] = {**self.active_roles["public"], "local_path": self.transition["local_path"]}
        self.write_rewrite()
        payload = json.dumps({"repository": {"clone_url": "https://" + self.repository + ".git"}}, separators=(",", ":")).encode()
        signature = "sha256=" + hmac.new(self.webhook_secret.encode(), payload, hashlib.sha256).hexdigest()
        status, _, _ = self.request("/api/webhook", payload=payload, extra={"Content-Type": "application/json", "X-GitHub-Event": "push", "X-Hub-Signature-256": signature})
        score.require(status == 202, "field transition webhook refused")
        self.current_commit = self.transition["transition_commit"]
        self.contents = {**self.contents, self.transition["path"]: self.contents[self.transition["path"]] + self.transition["appended_utf8"].encode()}
        object_id = old["bundle"]["query"]["message"] + "#" + str(old["bundle"]["query"]["field_number"])
        fresh, receipt = self.exact(object_id)
        score.require(receipt["authority"]["run_id"] != old["bundle"]["exact_authorities"][0]["run_id"] and fresh["id"] != old["id"], "field publication reused prior source authority")
        self.report["publication_metadata"].append(fields.publication_metadata(self.root / "data", receipt["authority"]))
        self.immutable(old)
        self.mcp_projection(fields.ENDPOINTS[0], object_id, fresh)
        source = self.source(fresh, self.contents)
        # The full unchanged fourteen-site truth is checked again without
        # adding a second quality denominator or a new source population.
        for object_id in sorted(self.plan["population"]["objects"]):
            self.proof(fields.ENDPOINTS[0], object_id, self.bounds["cold_request_seconds"], receipt["authority"])
        self.report["publication_transition"] = {"old_proof": old["id"], "new_proof": fresh["id"], "old_immutable_byte_identical": True, "new_authority": receipt["authority"], "new_source": source, "unchanged_consumer_truth": 14, "quality_denominator_added": 0}

    def withdrawal(self):
        self.stop()
        self.start("field-withdrawn", ["rel-" + fields.PACK + "-1.0.0"])
        self.discovery(False)
        params = fields.query(self.population, sorted(self.plan["population"]["objects"])[0])
        for endpoint in fields.ENDPOINTS:
            score.require(self.get("/api/" + endpoint, params)[0] == 404, "revoked field HTTP remains admitted")


def cleanup(matrix, root, failed):
    # No data/source/log/auth removal before exact server and sampler join.
    shared.require_derived_cleanup(matrix)
    if not failed:
        score.require(not (root / "disposable-only.pem").exists(), "disposable field private key survived")
        shutil.rmtree(root)
        matrix.report["private_scratch_removed_after_join"] = True
        return
    # Preserve only bounded actual response bodies. Server logs, mirrors,
    # auth/config, URL rewrites, signatures/artifacts and every other scratch
    # child are removed after the joined boundary before writing diagnostics.
    for path in root.iterdir():
        if path.name == "private-raw" and path.is_dir() and not path.is_symlink():
            continue
        if path.is_dir() and not path.is_symlink():
            shutil.rmtree(path)
        else:
            path.unlink()
    raw = root / "private-raw"
    score.require(raw.is_dir() and not raw.is_symlink(), "private response evidence directory unavailable")
    retained, total = [], 0
    for path in sorted(raw.iterdir()):
        score.require(path.name.startswith("response-") and path.name.endswith(".json") and path.is_file() and not path.is_symlink(), "private response custody shape differs")
        size = path.stat().st_size
        score.require(size <= matrix.bounds["http_response_bytes"], "private response custody exceeds per-response bound")
        total += size
        retained.append({"path": path.name, "bytes": size, "sha256": checks.sha(path)})
    diagnostics = {"schema": "phebs-t513-source-free-stop-diagnostics-v1", "phases": matrix.report["phases"], "reads": matrix.report["reads"], "raw_responses": retained, "raw_bytes": total, "auth_config_key_data_removed": True}
    encoded = json.dumps(diagnostics, indent=2).encode() + b"\n"
    score.require(len(encoded) <= 1 << 20, "source-free stop diagnostics exceed bound")
    path = root / "source-free-diagnostics.json"
    path.write_bytes(encoded)
    matrix.report["retained_stop_evidence"] = {"root": str(root), "raw_files": len(retained), "raw_bytes": total, "bound": "finite reviewed request matrix, fixed publication deadlines/cadence, global180s and1MiB per response", "diagnostics_digest": checks.sha(path), "auth_config_key_data_removed_after_join": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binding", type=Path, required=True)
    parser.add_argument("--helpers", type=Path)
    parser.add_argument("--run-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--execute-reviewed-binding", action="store_true")
    args = parser.parse_args()
    score.require(args.execute_reviewed_binding, "T513 execution requires reviewed exact binding and serialized lead slot")
    score.require(not args.out.exists() and not args.run_root.exists() and not args.out.resolve().is_relative_to(args.run_root.resolve()), "field attempt output/root exists or output is private scratch")
    try:
        execute(args)
    except BaseException as error:
        if not args.out.exists():
            # Binding/admission refusal before an owned Matrix exists is still
            # a preserved first preparation stop, never a hidden retry.
            args.out.write_text(json.dumps({"schema": "phebs-t513-preparation-stop-v1", "outcome": "stopped_no_retry", "reason": str(error), "reads": [], "phases": [], "seconds": 0, "production_signing": "pending; no production signature", "qualification": "preparation only; no native/product phase result"}, indent=2) + "\n")
        raise


def execute(args):
    plan, population, labels, files, contents, transition, names = fields.frozen()
    binding = checks.bounded_json(args.binding, 262144)
    if args.helpers is not None:
        shared.select_helpers(binding, args.helpers)
    metadata = validate_binding(binding, plan)
    public = next(r for r in checks.bounded_json(REPO / "spike/t511/committed-inputs.json", 65536)["records"] if r["population"] == "public")
    score.require(checks.sha(REPO / plan["population"]["bundle"]) == "sha256:" + public["bundle_sha256"], "public committed field bundle drift")
    censuses = [shared.verify_input_repository(binding["tools"]["git"]["path"], plan["population"]["base_local_path"], plan["population"]["base_commit"], plan["population"]["base_tree"], files)]
    changed_files = [dict(row) for row in files]
    changed = next(f for f in changed_files if f["path"] == transition["path"])
    changed.update(git_blob=transition["new_git_blob"], bytes=changed["bytes"] + len(transition["appended_utf8"].encode()))
    censuses.append(shared.verify_input_repository(binding["tools"]["git"]["path"], transition["local_path"], transition["transition_commit"], transition["transition_tree"], changed_files))
    os.umask(0o077)
    args.run_root.mkdir(parents=True)
    host = shared.serve.receipts.host_facts()
    score.require(host["sysname"] == "Darwin" and host["machine"] == "arm64", "T513 operating binding is Darwin arm64 only")
    headroom = qualify.physical_headroom()
    free_disk = shutil.disk_usage(args.run_root).free
    score.require(headroom["available_physical_bytes"] >= plan["bounds"]["minimum_available_physical_headroom_bytes"] and free_disk >= plan["bounds"]["minimum_free_disk_bytes"], "field physical/disk headroom unavailable")
    matrix = FieldMatrix(args.run_root, binding, plan, population, labels, contents, transition, names)
    matrix.report.update(host=host, headroom_preflight=headroom, free_disk_preflight_bytes=free_disk, bounds=plan["bounds"], input_censuses=censuses, plan_digest=checks.sha(HERE / "operating-plan.json"), binding_digest=checks.sha(args.binding), source_commit=binding["source_commit"], binary_metadata=metadata, binary_digest=binding["binary"]["sha256"], executed_tool_bindings=binding["tools"], observer_binding=binding["observer"])
    failure = None
    try:
        artifacts, selection, public_key, record = sign_selection(args.run_root, binding)
        matrix.report["signed_disposable_field_selection"] = record
        matrix.config["release_selection"] = {"path": str(selection), "artifacts_path": str(artifacts), "keys": [{"id": "t513-integration-only", "public_key": public_key}]}
        matrix.deadline = checks.Deadline(plan["bounds"]["complete_native_seconds"])
        matrix.start("field-baseline")
        matrix.quality()
        matrix.publication()
        matrix.withdrawal()
        matrix.report["outcome"] = "matrix_complete_pending_joined_teardown"
    except BaseException as error:
        failure = error
        matrix.report.update(outcome="stopped_no_retry", reason=str(error))
    finally:
        if matrix.deadline is not None:
            try:
                matrix.stop()
            except BaseException as error:
                failure = failure or error
                matrix.report.update(outcome="stopped_no_retry", cleanup_failure=str(error))
        try:
            cleanup(matrix, args.run_root, failure is not None)
        except BaseException as error:
            failure = failure or error
            matrix.report.update(outcome="stopped_no_retry", custody_cleanup_failure=str(error))
        matrix.report["seconds"] = matrix.deadline.elapsed() if matrix.deadline is not None else 0
        if matrix.report["seconds"] > plan["bounds"]["complete_native_seconds"]:
            failure = failure or ValueError("field global wall bound exceeded through teardown")
            matrix.report.update(outcome="stopped_no_retry", wall_bound_exceeded=True)
        if failure is None:
            matrix.report["outcome"] = "passed_finite_field_composition_darwin_only_production_signing_pending"
        args.out.write_text(json.dumps(matrix.report, indent=2) + "\n")
        print(json.dumps({"outcome": matrix.report["outcome"], "receipt": str(args.out)}))
    if failure:
        raise failure


if __name__ == "__main__":
    main()
