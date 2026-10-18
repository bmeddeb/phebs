#!/usr/bin/env python3
"""Prospective T523 finite signed Thrift matrix; exact review and slot required."""
from concurrent.futures import ThreadPoolExecutor
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import time

import operating_checks as fields
import qualify_fields as inherited
import requalify as shared
import requalification_checks as checks
import qualify
import score

HERE, REPO = fields.HERE, fields.REPO
OWN_FILES = ("operating_checks.py", "qualify_thrift_fields.py", "test_operating.py", "OPERATING_README.md")
REUSED_T503 = (*sorted(shared.HARNESS_FILES), "qualify.py", "score.py")
REUSED_T513 = ("qualify_fields.py", "field_checks.py")
DARK_CAPABILITIES = (shared.DARK_CAPABILITIES - {"thrift-field-references"}) | {"contract-caller-map", "contract-caller-comparison", "contract-atlas"}
DARK_TOOLS = (shared.DARK_TOOLS - {fields.ENDPOINT}) | {"list_operation_callers", "compare_operation_callers", "find_operation_consumers", "get_extraction_coverage", "list_contracts", "read_contract"}


def validate_binding(binding):
    score.require(binding["schema"] == "phebs-t523-execution-binding-v1" and binding["reviewed"] is True and binding["plan_digest"] == checks.sha(HERE / "operating-plan.json") and binding["input_digest"] == checks.sha(HERE / "operating-inputs.json"), "Thrift execution binding not independently reviewed")
    score.require(qualify.command(["git", "rev-parse", "HEAD"], cwd=REPO).strip() == binding["harness_commit"] and not qualify.command(["git", "status", "--porcelain", "--untracked-files=no"], cwd=REPO).strip(), "Thrift harness is not the reviewed clean commit")
    for key, folder, names in (("harness_files", HERE, OWN_FILES), ("reused_t503_files", REPO / "spike/t503", REUSED_T503), ("reused_t513_files", REPO / "spike/t513", REUSED_T513)):
        score.require(set(binding[key]) == set(names), "Thrift execution binding omits owned/reused code")
        for name, digest in binding[key].items():
            score.require(checks.sha(folder / name) == digest, "Thrift owned/reused harness source drift")
    for item in [binding["binary"], binding["observer"], *binding["tools"].values()]:
        path = Path(item["path"])
        score.require(path.is_absolute() and path.is_file() and checks.sha(path) == item["sha256"] and path.stat().st_size == item["bytes"], "Thrift bound tool/image drift")
    score.require(binding["tools"]["openssl"]["path"] == "/opt/homebrew/opt/openssl@3/bin/openssl", "Thrift selection requires the admitted Ed25519-capable tool")
    metadata = qualify.command([binding["tools"]["go"]["path"], "version", "-m", binding["binary"]["path"]])
    score.require("vcs.revision=" + binding["source_commit"] in metadata and "vcs.modified=false" in metadata and "sha256:" + hashlib.sha256(metadata.encode()).hexdigest() == binding["binary"]["metadata_sha256"], "Thrift native binary source metadata differs")
    for key in ("build_receipt", "helper_build_receipt"):
        score.require(checks.sha(Path(binding[key]["path"])) == binding[key]["sha256"], "Thrift build receipt drift")
    build = checks.bounded_json(Path(binding["build_receipt"]["path"]), 65536)
    helpers = checks.bounded_json(Path(binding["helper_build_receipt"]["path"]), 65536)
    score.require(build["source_commit"] == binding["source_commit"] and build["binary_sha256"] == binding["binary"]["sha256"].removeprefix("sha256:") and build["source_clean_before"] and build["source_clean_after"], "Thrift image preparation differs")
    expected = {name: binding["tools"][key]["sha256"].removeprefix("sha256:") for key, name in shared.HELPERS.items()}
    expected["native-observer"] = binding["observer"]["sha256"].removeprefix("sha256:")
    score.require({r["name"]: r["sha256"] for r in helpers["runs"]} == expected and all(r["exit"] == 0 and r["joined"] for r in helpers["runs"]), "Thrift helper preparation differs")
    return metadata


def sign_selection(root, binding):
    artifacts, selection = root / "artifacts", root / "selection"
    artifacts.mkdir(); selection.mkdir()
    template = checks.bounded_json(REPO / "spike/t475/release/receipts/template.json", 65536)
    private, public, records, templates = root / "disposable-only.pem", None, [], []
    # Freeze the complete artifact directory before either record binds its
    # census. Adding the second pack after the first signature would stale it.
    for generator, pack in fields.PACKS.items():
        current = json.loads(json.dumps(template))
        current.update(pack_id=pack, release_id="rel-" + pack + "-1.0.0", approved_at="2026-10-18T00:00:00Z", approval_records=["t523-disposable-key-finite-measurement-only"])
        current["validation"]["expires_at"] = "2027-01-16T23:59:59Z"
        for kind in ("card", "manifest", "validation"):
            extension = ".md" if kind == "card" else ".json"
            name = generator + "-" + kind + "-1.0.0" + extension
            value = {"schema": "t523-disposable-" + kind + "-v1", "pack_id": pack, "claim_version": "1.0.0", "plan_digest": checks.sha(HERE / "operating-plan.json"), "measurement": "prospective_in_progress", "production_release": False}
            text = "# T523 disposable " + generator + " integration selection\nNo production signature or quality/operating pass is granted.\n" if kind == "card" else json.dumps(value)
            (artifacts / name).write_text(text)
            current[kind]["artifact_id"] = name
        path = root / (generator + "-template.json"); path.write_text(json.dumps(current))
        templates.append((generator, pack, path))
    try:
        qualify.command([binding["tools"]["openssl"]["path"], "genpkey", "-algorithm", "ED25519", "-out", private])
        private.chmod(0o600)
        for generator, pack, path in templates:
            output = selection / (generator + ".json")
            text = qualify.command([binding["binary"]["path"], "release-sign", "-template", path, "-key", private, "-key-id", "t523-integration-only", "-artifacts", artifacts, "-out", output])
            key = next(line.removeprefix("public key ") for line in text.splitlines() if line.startswith("public key "))
            score.require(public in (None, key), "Thrift disposable public key changed")
            public = key
            record = checks.bounded_json(output, 65536)
            score.require(record["pack_id"] == pack and record["pack_claim_version"] == "1.0.0", "Thrift signed record scope differs")
            records.append({"generator": generator, "record_digest": checks.sha(output), "record": record})
        score.require({p.name for p in selection.iterdir()} == {"apache.json", "thriftrw.json"}, "unrelated signed selection artifact present")
        return artifacts, selection, public, records
    finally:
        private.unlink(missing_ok=True)


class ThriftMatrix(shared.Matrix):
    """Reuse accepted joined lifecycle/HTTP/sampler; no new runtime engine."""
    def __init__(self, root, binding, plan, records, labels, contents, names):
        roles = [{"role": r["generator"], "repository": r["repository"], "local_path": r["local_path"]} for r in records]
        super().__init__(root, binding, labels, {"roles": roles}, {}, plan["bounds"])
        self.plan, self.records, self.contents, self.names = plan, records, contents, names
        self.env.pop("PHEBS_BUF", None)
        self.env.pop("PHEBS_T472_SOURCE_OUTCOME_ROOT", None)
        self.phase = None
        self.report.update(schema="phebs-t523-finite-operating-v1", qualification="Frozen public source conformance and bounded Darwin-local measurements only; no production signature, dedicated UI, Linux capacity, scale or SLO.")

    def proof(self, record, object_id, maximum, expected=None):
        params = fields.query(record, object_id)
        status, raw = self.get("/api/" + fields.ENDPOINT, params, maximum)
        score.require(status == 200, "Thrift whole-proof request refused")
        value = json.loads(raw)
        receipt = fields.check_bundle(value, params, record, self.records, self.phase["selected"], self.phase["version"], self.contents, self.labels, self.names, expected)
        return value, receipt

    def exact(self):
        record = self.records[0]
        object_id = sorted(record["objects"])[0]
        limit = time.monotonic() + self.deadline.timeout(self.bounds["publication_seconds"])
        while True:
            status, raw = self.get("/api/" + fields.ENDPOINT, fields.query(record, object_id), self.bounds["cold_request_seconds"])
            score.require(status in (200, 404, 409), "unexpected Thrift publication status")
            if status == 200:
                value = json.loads(raw)
                for repository in value["bundle"]["coverage"]["repositories"]:
                    for run in repository["runs"]:
                        score.require(not run.get("failures") and (run.get("outcome") or {}).get("disposition") not in ("terminal_refusal", "failed"), "terminal Thrift extraction refusal")
                roots = value["bundle"].get("exact_authorities", [])
                versions = value["bundle"].get("extractor_versions", [])
                if len(roots) == len(self.records) and versions and all(r["extractor"] == self.phase["version"] for r in versions):
                    score.require(time.monotonic() <= limit, "successful Thrift publication crossed local deadline")
                    self.deadline.remaining()
                    receipt = fields.check_bundle(value, fields.query(record, object_id), record, self.records, self.phase["selected"], self.phase["version"], self.contents, self.labels, self.names)
                    return value, receipt
            score.require(time.monotonic() < limit, "Thrift publication deadline exceeded")
            time.sleep(min(0.25, self.deadline.remaining()))

    def discovery(self):
        status, raw = self.get("/api/version")
        score.require(status == 200, "Thrift capability discovery failed")
        capabilities = json.loads(raw).get("capabilities", [])
        tools = {t["name"] for t in self.mcp("tools/list", {})["tools"]}
        score.require("thrift-field-references" in capabilities and fields.ENDPOINT in tools and not DARK_CAPABILITIES.intersection(capabilities) and not DARK_TOOLS.intersection(tools), "Thrift independent signature/discovery admission differs")
        self.report.setdefault("discovery", []).append({"phase": self.phase["name"], "capabilities": capabilities, "tools": sorted(tools)})
        params = fields.query(self.records[0], sorted(self.records[0]["objects"])[0])
        score.require(self.get("/api/" + fields.ENDPOINT, params, authenticated=False)[0] == 401, "anonymous field proof admitted")
        score.require(self.get("/api/find_proto_field_references", params)[0] == 404, "protobuf field HTTP remains admitted")

    def immutable(self, value):
        status, raw = self.get("/api/proof_bundles/" + value["id"], maximum=self.bounds["cold_request_seconds"])
        score.require(status == 200, "immutable Thrift proof unavailable")
        saved = json.loads(raw)
        score.require(saved["id"] == value["id"] and fields.common.canonical(saved["bundle"]) == fields.common.canonical(value["bundle"]), "immutable Thrift proof changed")

    def source(self, by_source):
        receipts = []
        for (generator, path), atoms in sorted(by_source.items()):
            record = next(r for r in self.records if r["generator"] == generator)
            params = {"repo": record["repository"], "ref": record["commit"], "path": path}
            status, raw = self.get("/api/source", params, self.bounds["cold_request_seconds"])
            score.require(status == 200, "Thrift immutable source HTTP refused")
            source, expected = json.loads(raw), self.contents[generator][path]
            score.require(source["encoding"] == "utf8" and source["size"] == len(expected) and source["content"].encode() == expected, "Thrift source HTTP bytes differ")
            result = self.tool("read_file", params)
            score.require(not result.get("isError"), "Thrift source MCP refused")
            mcp = result["structuredContent"]
            score.require(not mcp.get("truncated") and mcp["content"] == expected.decode().removesuffix("\n") and mcp["start_line"] == 1 and mcp["end_line"] == mcp["total_lines"], "Thrift source MCP bytes/window differ")
            for atom in atoms.values():
                start, end = atom["start_byte"], atom["end_byte"]
                score.require(source["content"].encode()[start:end] == expected[start:end] and mcp["content"].encode()[start:end] == expected[start:end], "Thrift source exact byte slice differs")
            receipts.append({"repository": record["repository"], "commit": record["commit"], "path": path, "blob_digest": "sha256:" + hashlib.sha256(expected).hexdigest(), "unique_consumer_atoms_sliced": len(atoms), "transports": ["GET /api/source full blob", "MCP read_file full blob"], "blob_read_count": "one immutable full blob per transport by source contract; no instrumented counter"})
        return receipts

    def quality(self):
        initial, receipt = self.exact()
        expected = receipt["authorities"]
        phase_report = {"name": self.phase["name"], "selected": self.phase["selected"], "version": self.phase["version"], "metadata": [fields.publication_metadata(self.root / "data", r, self.phase["version"]) for r in expected]}
        self.report.setdefault("selected_set_phases", []).append(phase_report)
        self.discovery()
        sources, rows, total, first = {}, [], 0, None
        for record in self.records:
            generator, spans = record["generator"], set()
            objects = sorted(record["objects"])
            if generator == "thriftrw":
                objects.append("health.Meta_Health_Result#0")
            for object_id in objects:
                value, checked = self.proof(record, object_id, self.bounds["cold_request_seconds"], expected)
                first = first or value
                fields.check_mcp(self.tool(fields.ENDPOINT, fields.query(record, object_id)), value, self.phase["version"])
                self.immutable(value)
                for item in value["bundle"]["evidence"]:
                    occurrence = item["occurrences"][0]
                    if occurrence["path"] in record["application"]:
                        sources.setdefault((generator, occurrence["path"]), {})[item["atom"]["atom_id"]] = item["atom"]
                spans.update(tuple(s) for s in checked["consumer_spans"])
                for _ in range(5):
                    self.proof(record, object_id, self.bounds["warm_request_seconds"], expected)
                rows.append({"generator": generator, "object": object_id, **checked, "warm_reads": 5})
            count = record["positive"] if generator in self.phase["selected"] else 0
            score.require(len(spans) == count, "Thrift selected public denominator differs")
            total += len(spans)
        phase_report.update(
            native_query_scope=self.plan["quality"]["native_query_scope"],
            native_positive_opportunities=sum(r["positive"] for r in self.records if r["generator"] in self.phase["selected"]),
            native_verified_consumer_unique_spans=total,
            native_fixed_query_extra_consumer_facts=0,
            prior_frozen_negative_sites={
                "count": sum(r["negative"] for r in self.records if r["generator"] in self.phase["selected"]),
                "scope": "prior T521/T522 direct full-stream labels/results only; not new native full-stream opportunities or a new negative-site score",
            },
            objects=rows, source_reads=self.source(sources),
            cold_caveat="first object success after explicit publication probes; process/shared caches may be warm",
        )
        if self.phase["name"] == "both":
            batches = []
            pairs = [(r, o) for r in self.records for o in sorted(r["objects"])]
            for count in (1, 4, 8):
                clock = time.monotonic()
                with ThreadPoolExecutor(max_workers=count) as workers:
                    pending = [workers.submit(self.proof, *pairs[i % len(pairs)], self.bounds["cold_request_seconds"], expected) for i in range(count)]
                    for task in pending:
                        task.result(timeout=self.deadline.remaining())
                elapsed = time.monotonic() - clock
                self.check()
                batches.append({"clients": count, "whole_proof_requests": count, "wall_seconds": round(elapsed, 6), "requests_per_second": count / elapsed})
            phase_report["whole_proof_concurrency"] = batches
        return first, expected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binding", type=Path, required=True)
    parser.add_argument("--helpers", type=Path)
    parser.add_argument("--run-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--execute-reviewed-binding", action="store_true")
    args = parser.parse_args()
    score.require(args.execute_reviewed_binding, "T523 requires reviewed binding and the lead's serialized native slot")
    score.require(not args.out.exists() and not args.run_root.exists() and not args.out.resolve().is_relative_to(args.run_root.resolve()), "Thrift attempt output/root exists or output is private scratch")
    try:
        execute(args)
    except BaseException as error:
        if not args.out.exists():
            args.out.write_text(json.dumps({"schema": "phebs-t523-preparation-stop-v1", "outcome": "stopped_no_retry", "reason": str(error), "reads": [], "phases": [], "seconds": 0, "production_signing": "pending; no production signature", "qualification": "preparation only; no native/product phase result"}, indent=2) + "\n")
        raise


def execute(args):
    plan, records, labels, contents, names = fields.frozen()
    binding = checks.bounded_json(args.binding, 262144)
    if args.helpers is not None:
        shared.select_helpers(binding, args.helpers)
    metadata = validate_binding(binding)
    censuses = [fields.verify_repository(binding["tools"]["git"]["path"], r) for r in records]
    score.require(sum(r["files"] for r in censuses) == plan["bounds"]["evaluation_tree_files"] and sum(r["bytes"] for r in censuses) <= plan["bounds"]["evaluation_tree_bytes"], "Thrift aggregate input admission differs")
    os.umask(0o077)
    args.run_root.mkdir(parents=True)
    host = shared.serve.receipts.host_facts()
    score.require(host["sysname"] == "Darwin" and host["machine"] == "arm64", "T523 envelope is Darwin arm64 only")
    headroom, free_disk = qualify.physical_headroom(), shutil.disk_usage(args.run_root).free
    score.require(headroom["available_physical_bytes"] >= plan["bounds"]["minimum_available_physical_headroom_bytes"] and free_disk >= plan["bounds"]["minimum_free_disk_bytes"], "Thrift physical/disk headroom unavailable")
    matrix = ThriftMatrix(args.run_root, binding, plan, records, labels, contents, names)
    matrix.report.update(host=host, headroom_preflight=headroom, free_disk_preflight_bytes=free_disk, bounds=plan["bounds"], input_censuses=censuses, plan_digest=checks.sha(HERE / "operating-plan.json"), binding_digest=checks.sha(args.binding), source_commit=binding["source_commit"], binary_metadata=metadata, binary_digest=binding["binary"]["sha256"], executed_tool_bindings=binding["tools"], observer_binding=binding["observer"])
    failure, previous_roots, both_proof = None, None, None
    matrix.deadline = checks.Deadline(plan["bounds"]["complete_native_seconds"])
    try:
        artifacts, selection, public_key, signed = sign_selection(args.run_root, binding)
        matrix.deadline.remaining()
        matrix.report["signed_disposable_field_selection"] = signed
        matrix.config["release_selection"] = {"path": str(selection), "artifacts_path": str(artifacts), "keys": [{"id": "t523-integration-only", "public_key": public_key}]}
        for phase in plan["phases"]:
            matrix.phase = phase
            matrix.start(phase["name"], phase["revoked"])
            value, roots = matrix.quality()
            score.require(previous_roots is None or all(next(r for r in previous_roots if r["repository"] == root["repository"])["run_id"] != root["run_id"] for root in roots), "Thrift selected-set transition reused old run")
            if phase["name"] == "both":
                both_proof = value
            if phase["name"] == "rw-survivor":
                score.require(both_proof is not None and matrix.get("/api/proof_bundles/" + both_proof["id"])[0] == 404, "prior both-version proof remains currently served after withdrawal")
                matrix.report["withdrawal"] = {"withdrawn": fields.PACKS["apache"], "survivor": fields.PACKS["thriftrw"], "old_both_proof": both_proof["id"], "old_proof_current_serving": "404", "historical_bytes": "custody remains a separate retained registration regression; not mutated or newly claimed here"}
            previous_roots = roots
            matrix.stop()
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
            inherited.cleanup(matrix, args.run_root, failure is not None)
        except BaseException as error:
            failure = failure or error
            matrix.report.update(outcome="stopped_no_retry", custody_cleanup_failure=str(error))
        matrix.report["seconds"] = matrix.deadline.elapsed() if matrix.deadline is not None else 0
        if matrix.report["seconds"] > plan["bounds"]["complete_native_seconds"]:
            failure = failure or ValueError("Thrift global wall bound exceeded through teardown")
            matrix.report.update(outcome="stopped_no_retry", wall_bound_exceeded=True)
        if failure is None:
            matrix.report["outcome"] = "passed_finite_thrift_composition_darwin_only_production_signing_pending"
        args.out.write_text(json.dumps(matrix.report, indent=2) + "\n")
        print(json.dumps({"outcome": matrix.report["outcome"], "receipt": str(args.out)}))
    if failure:
        raise failure


if __name__ == "__main__":
    main()
