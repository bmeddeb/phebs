#!/usr/bin/env python3
"""One prospective frozen Impact matrix; requires exact review and native slot."""
from concurrent.futures import ThreadPoolExecutor
import argparse
import copy
import hashlib
import hmac
import json
import os
from pathlib import Path
import shutil
import time

import impact_checks as impact
import impact_browser
import qualify_fields as inherited
import requalify as shared
import requalification_checks as checks
import qualify
import score

HERE, REPO = impact.HERE, impact.REPO
OWN_FILES = ("impact_checks.py", "qualify_impact.py", "impact_browser.py", "impact_browser.cjs", "test_impact.py", "README.md")
REUSED_T503 = (*sorted(shared.HARNESS_FILES), "qualify.py", "score.py")
REUSED_T513 = ("qualify_fields.py", "field_checks.py")
DARK_CAPABILITIES = shared.DARK_CAPABILITIES | {"contract-caller-comparison"}
DARK_TOOLS = shared.DARK_TOOLS | {"compare_operation_callers", "find_operation_consumers", "get_extraction_coverage"}
VOLATILE = frozenset({"$schema", "citation", "next_cursor"})


def validate_binding(binding):
    score.require(binding["schema"] == "phebs-t543-execution-binding-v1" and binding["reviewed"] is True and binding["plan_digest"] == checks.sha(HERE / "qualification-plan.json"), "Impact exact execution binding not independently reviewed")
    score.require(qualify.command([binding["tools"]["git"]["path"], "rev-parse", "HEAD"], cwd=REPO).strip() == binding["harness_commit"] and not qualify.command([binding["tools"]["git"]["path"], "status", "--porcelain", "--untracked-files=no"], cwd=REPO).strip(), "Impact harness is not its reviewed clean commit")
    for key, folder, names in (("harness_files", HERE, OWN_FILES), ("reused_t503_files", REPO / "spike/t503", REUSED_T503), ("reused_t513_files", REPO / "spike/t513", REUSED_T513)):
        score.require(set(binding[key]) == set(names), "Impact binding omits owned/reused source")
        for name, digest in binding[key].items():
            score.require(checks.sha(folder / name) == digest, "Impact owned/reused source drift")
    for item in [binding["binary"], binding["observer"], binding["axe_source"], *binding["tools"].values()]:
        path = Path(item["path"])
        score.require(path.is_absolute() and path.is_file() and path.stat().st_size == item["bytes"] and checks.sha(path) == item["sha256"], "Impact exact tool/image drift")
    score.require(binding["tools"]["openssl"]["path"] == "/opt/homebrew/opt/openssl@3/bin/openssl", "Impact signer needs the admitted Ed25519-capable image")
    metadata = qualify.command([binding["tools"]["go"]["path"], "version", "-m", binding["binary"]["path"]])
    score.require("vcs.revision=" + binding["source_commit"] in metadata and "vcs.modified=false" in metadata and "sha256:" + hashlib.sha256(metadata.encode()).hexdigest() == binding["binary"]["metadata_sha256"], "Impact binary lacks native exact clean source binding")
    for key in ("build_receipt", "helper_build_receipt"):
        score.require(checks.sha(Path(binding[key]["path"])) == binding[key]["sha256"], "Impact build receipt drift")
    build = checks.bounded_json(Path(binding["build_receipt"]["path"]), 65536)
    helper = checks.bounded_json(Path(binding["helper_build_receipt"]["path"]), 65536)
    score.require(build["source_commit"] == binding["source_commit"] and build["binary_sha256"] == binding["binary"]["sha256"].removeprefix("sha256:") and build["source_clean_before"] and build["source_clean_after"], "Impact UI/binary preparation differs")
    score.require(shared.inventory_digest(REPO, ["ui/src", "ui/public", "ui/package.json", "ui/package-lock.json"]) == binding["ui"]["source_inventory_digest"], "Impact embedded UI source changed")
    score.require(shared.inventory_digest(Path(binding["ui"]["dist_workspace"]), ["ui/dist"]) == binding["ui"]["dist_inventory_digest"], "Impact built UI changed")
    expected = {name: binding["tools"][key]["sha256"].removeprefix("sha256:") for key, name in shared.HELPERS.items()}
    expected["native-observer"] = binding["observer"]["sha256"].removeprefix("sha256:")
    score.require({r["name"]: r["sha256"] for r in helper["runs"]} == expected and all(r["exit"] == 0 and r["joined"] for r in helper["runs"]), "Impact helper preparation differs")
    for name in ("playwright", "@playwright/test"):
        score.require(checks.bounded_json(Path(binding["node_modules"]) / name / "package.json", 65536)["version"] == binding["playwright_version"], "Impact installed client version differs")
    preflight = checks.bounded_json(Path(binding["browser_preflight"]["path"]), 65536)
    score.require(checks.sha(Path(binding["browser_preflight"]["path"])) == binding["browser_preflight"]["sha256"] and preflight["outcome"] == "passed" and preflight["chrome_path"] == str(Path(binding["tools"]["chrome"]["path"]).resolve()) and preflight["playwright_version"] == binding["playwright_version"], "Impact installed browser preflight differs")
    return metadata


def sign_selection(root, binding):
    artifacts, selection = root / "artifacts", root / "selection"
    artifacts.mkdir(); selection.mkdir()
    template = checks.bounded_json(REPO / "spike/t475/release/receipts/template.json", 65536)
    templates, records, public = [], [], None
    for pack, stem in (("phebs.grpc.caller.go", "caller-map"), (impact.PACK, "operation-impact")):
        current = copy.deepcopy(template)
        current.update(pack_id=pack, release_id="rel-" + pack + "-1.0.0", approved_at="2026-10-18T00:00:00Z", approval_records=["t543-disposable-key-measurement-only"])
        current["validation"]["expires_at"] = "2027-01-16T23:59:59Z"
        for kind in ("card", "manifest", "validation"):
            name = stem + "-" + kind + "-1.0.0" + (".md" if kind == "card" else ".json")
            document = {"schema": "t543-disposable-" + kind + "-v1", "pack_id": pack, "plan_digest": checks.sha(HERE / "qualification-plan.json"), "measurement": "prospective_in_progress", "production_release": False}
            text = "# Disposable T543 " + stem + " selection\nNo production signature or quality/operating pass is granted.\n" if kind == "card" else json.dumps(document)
            (artifacts / name).write_text(text)
            current[kind]["artifact_id"] = name
        path = root / (stem + "-template.json"); path.write_text(json.dumps(current))
        templates.append((pack, stem, path))
    private = root / "disposable-only.pem"
    try:
        qualify.command([binding["tools"]["openssl"]["path"], "genpkey", "-algorithm", "ED25519", "-out", private]); private.chmod(0o600)
        for pack, stem, path in templates:
            output = selection / (stem + ".json")
            text = qualify.command([binding["binary"]["path"], "release-sign", "-template", path, "-key", private, "-key-id", "t543-integration-only", "-artifacts", artifacts, "-out", output])
            key = next(line.removeprefix("public key ") for line in text.splitlines() if line.startswith("public key "))
            score.require(public in (None, key), "Impact disposable public key changed")
            public = key
            record = checks.bounded_json(output, 65536)
            score.require(record["pack_id"] == pack and record["pack_claim_version"] == "1.0.0", "Impact/input signed scope differs")
            records.append({"record": record, "sha256": checks.sha(output)})
        score.require({p.name for p in selection.iterdir()} == {"caller-map.json", "operation-impact.json"}, "unrelated signed Impact selection artifact")
        return artifacts, selection, public, records
    finally:
        private.unlink(missing_ok=True)


class ImpactMatrix(shared.Matrix):
    def __init__(self, root, binding, plan, inputs, oracle, contents, transition, browser_root):
        source = inputs["evaluation"]
        roles = [{"role": "primary", "repository": source["repository"], "local_path": source["local_source_root"]}]
        super().__init__(root, binding, oracle, {"roles": roles}, {"records": [transition]}, plan["bounds"])
        self.plan, self.inputs, self.oracle, self.contents, self.transition = plan, inputs, oracle, contents, transition
        self.commit, self.files = source["derived_input_commit"], {r["path"]: r for r in source["files"]}
        self.browser_root, self.browser_joined = browser_root, True
        self.declaration_bindings = {}
        self.env.pop("PHEBS_BUF", None)
        self.report.update(schema="phebs-t543-operation-impact-native-v1", qualification="Actual composed operation_callers output over the frozen selected public source and finite Darwin-local HTTP/MCP/UI operating envelope only; no component accuracy transfer, Linux/scale/SLO/runtime/absence or production signature.")

    def read(self, gold, size=100, cursor="", maximum=None, expected=None):
        status, raw = self.get(impact.ROUTE, impact.parameters(gold, size, cursor), maximum or self.bounds["cold_request_seconds"])
        score.require(status == 200, "composed operation report refused")
        value = json.loads(raw)
        checked = impact.check_page(value, gold, size, self.inputs, self.oracle, self.commit, self.files, expected)
        impact.declaration_run(value, self.declaration_bindings[checked["generation"]["resolver_manifest_digest"]])
        return value, checked, raw

    def bind_native(self, report):
        generation = report["caller_page"]["generation"]
        repository = self.inputs["evaluation"]["repository"]
        resolver = checks.resolver_unit_binding(self.root / "data", repository, self.commit, generation, self.files["unit-snapshot.json"])
        selected = resolver["catalog_identity"]["declarations"]
        score.require(len(selected) == 1, "unexpected declaration input set in operation recipe")
        impact.declaration_run(report, selected[0])
        self.declaration_bindings[generation["resolver_manifest_digest"]] = selected[0]
        self.report.setdefault("resolver_units", []).append(resolver)
        self.report.setdefault("caller_authorities", []).append(checks.caller_binding(self.root / "data", repository, self.commit, generation))

    def exact(self, gold, size=100):
        limit = time.monotonic() + self.deadline.timeout(self.bounds["publication_seconds"])
        while True:
            status, raw = self.get(impact.ROUTE, impact.parameters(gold, size), self.bounds["cold_request_seconds"])
            score.require(status in (200, 404, 409), "unexpected Impact publication status")
            if status == 200:
                value = json.loads(raw)
                page = value["caller_page"]
                score.require(page["generation"]["state"] != "failed" and not page["generation"].get("partition_progress", {}).get("refused_pair_count"), "terminal caller source publication failure")
                if page["matching_rows_state"] == "exact" and page["generation"].get("commit") == self.commit:
                    score.require(time.monotonic() <= limit, "successful Impact publication crossed local deadline")
                    self.deadline.remaining()
                    return value, impact.check_page(value, gold, size, self.inputs, self.oracle, self.commit, self.files)
                if page["matching_rows_state"] == "unavailable":
                    impact.authority(value, gold, self.commit, unavailable=True)
            score.require(time.monotonic() < limit, "Impact publication deadline exceeded")
            time.sleep(min(0.25, self.deadline.remaining()))

    def discovery(self, caller, admitted):
        status, raw = self.get("/api/version")
        score.require(status == 200, "Impact capability discovery failed")
        caps = json.loads(raw)["capabilities"]
        tools = {r["name"] for r in self.mcp("tools/list", {})["tools"]}
        score.require(("contract-impact-operation" in caps) == admitted and (impact.TOOL in tools) == admitted and ("contract-caller-map" in caps) == caller and ("list_operation_callers" in tools) == caller, "Impact consumed/own signed dependency registration differs")
        score.require(not DARK_CAPABILITIES.intersection(caps) and not DARK_TOOLS.intersection(tools), "operation-only selection enabled unrelated/legacy products")
        self.report.setdefault("dependency_discovery", []).append({"caller_map": caller, "operation_impact": admitted, "capabilities": caps, "tools": sorted(tools)})

    def cite(self, row, generation):
        source = row["source"]
        truth = next(s for s in self.oracle["sites"] if s["site_id"] == score.site_id(source))
        file = self.files[source["path"]]
        status, raw = self.get("/api/contract_callers/citation", {"citation": source["citation"]}, self.bounds["cold_request_seconds"])
        score.require(status == 200, "composed exact source citation refused")
        value = json.loads(raw)
        checks.check_citation(value, truth, self.inputs["evaluation"]["repository"], self.commit, file["git_blob"], "sha256:" + file["sha256"], generation)
        score.require(value["generation"] == generation, "Impact citation crossed full reported generation metadata")
        shared.serve.parity(raw, self.tool("read_operation_caller_citation", {"citation": source["citation"]}), VOLATILE)
        return {"site_id": truth["site_id"], "commit": self.commit, "generation_digest": generation["generation_digest"], "source_oid": file["git_blob"], "blob_digest": "sha256:" + file["sha256"], "citation_bytes": len(truth["selector"].encode()), "citation_bytes_sha256": "sha256:" + hashlib.sha256(truth["selector"].encode()).hexdigest(), "transports": ["HTTP exact caller citation", "MCP read_operation_caller_citation"]}

    def declaration_sources(self):
        rows = []
        for path in sorted({d["declaration"]["path"] for d in self.oracle["declarations"]}):
            params = {"repo": self.inputs["evaluation"]["repository"], "ref": self.commit, "path": path}
            status, raw = self.get("/api/source", params, self.bounds["cold_request_seconds"])
            expected = self.contents[path]
            score.require(status == 200 and json.loads(raw)["content"].encode() == expected and json.loads(raw)["size"] == len(expected), "Impact declaration immutable blob differs")
            result = self.tool("read_file", params)
            score.require(not result.get("isError") and result["structuredContent"]["content"] == expected.decode().removesuffix("\n") and not result["structuredContent"].get("truncated"), "Impact declaration source MCP differs")
            for item in self.oracle["declarations"]:
                source = item["declaration"]
                if source["path"] == path:
                    score.require(expected[source["start_byte"]:source["end_byte"]].decode() == source["statement"], "RPC source slice differs")
                    rows.append({"operation": item["operation"], "path": path, "commit": self.commit, "blob_digest": source["blob_digest"], "start_byte": source["start_byte"], "end_byte": source["end_byte"], "transports": ["HTTP full immutable source", "MCP read_file full blob"], "blob_read_scope": "two unique proto blobs once per transport; slices checked client-side; no instrumented read-count claim"})
        return rows

    def quality(self):
        initial, checked = self.exact(self.oracle["queries"][0])
        generation = checked["generation"]
        self.bind_native(initial)
        self.discovery(True, True)
        seen, citations, resolutions = {}, {}, {}
        self.report["quality_progress"] = []
        for gold in self.oracle["queries"]:
            traversals = []
            cold = None
            for size in (1, 2, 100):
                pages, cursor, visited = [], "", set()
                for _ in range(4):
                    value, checked, raw = self.read(gold, size, cursor, expected=generation)
                    if cold is None:
                        cold = dict(self.report["reads"][-1])
                    pages.append(checked)
                    if size == 100:
                        shared.serve.parity(raw, self.tool(impact.TOOL, impact.mcp_arguments(impact.parameters(gold, size))), VOLATILE)
                        for row in value["caller_page"]["rows"]:
                            key = score.site_id(row["source"])
                            if key not in citations:
                                citations[key] = self.cite(row, generation)
                            resolutions[key] = row["resolution"] + "/" + row["tier"]
                    if value["caller_page"]["pagination"]["complete"]:
                        break
                    cursor = value["caller_page"]["pagination"]["next_cursor"]
                    score.require(cursor not in visited, "Impact cursor failed to advance")
                    visited.add(cursor)
                else:
                    raise ValueError("finite Impact traversal exceeded frozen source count")
                traversals.append(impact.complete(pages, gold))
            score.require(all(set(sites) == set(traversals[0]) for sites in traversals), "Impact paging sizes changed source membership")
            seen[gold["query_id"]] = traversals[0]
            progress = {"query_id": gold["query_id"], "verified_site_ids": traversals[0], "page_sizes": [1, 2, 100], "conclusion": gold["expected_conclusion_state"], "cold_first_response_after_explicit_probes": {"page_size": 1, "read": cold}}
            self.report["quality_progress"].append(progress)
            if not gold["query"].get("path_prefix"):
                warm = []
                for _ in range(5):
                    self.read(gold, maximum=self.bounds["warm_request_seconds"], expected=generation)
                    warm.append(dict(self.report["reads"][-1]))
                progress["warm_firstpage_responses"] = {"page_size": 100, "reads": warm}
        score.require(len(citations) == 9, "composed exact citation denominator incomplete")
        source_binding = {"derived_input_commit": self.commit, "files": list(self.files.values())}
        self.report["quality"] = {**impact.metrics(self.oracle, seen), "exact_citations": list(citations.values()), "declaration_sources": self.declaration_sources(), "unit_states": {"resolved": 5, "unavailable": 4}, "observed_resolution_tiers": resolutions, "processed_go_sources": checks.source_accounting(self.root / "source-outcomes", self.inputs["evaluation"]["repository"], source_binding), "cold_caveat": "first success per operation after explicit publication probes; shared source/index/process caches may be warm", "repeated_transport_samples_added": 0}
        batches = []
        for count in (1, 4, 8):
            clock = time.monotonic()
            with ThreadPoolExecutor(max_workers=count) as pool:
                tasks = [pool.submit(self.read, self.oracle["queries"][i % 5], 100, "", self.bounds["cold_request_seconds"], generation) for i in range(count)]
                for task in tasks:
                    task.result(timeout=self.deadline.remaining())
            elapsed = time.monotonic()-clock
            batches.append({"clients": count, "requests": count, "wall_seconds": round(elapsed, 6), "requests_per_second": count/elapsed})
        self.report["finite_firstpage_concurrency"] = batches
        self.check()

    def negative_inputs(self):
        gold = self.oracle["queries"][0]
        score.require(self.get(impact.ROUTE, impact.parameters(gold), authenticated=False)[0] == 401, "anonymous composed report admitted")
        for shape in impact.SECTIONS:
            params = {**impact.parameters(gold), "shape": shape}
            score.require(self.get(impact.ROUTE, params)[0] == 503, "unsupported Impact shape not explicitly unavailable")
            score.require(self.tool(impact.TOOL, impact.mcp_arguments(params)).get("isError") is True, "unsupported MCP report shape returned data")
        score.require(self.get(impact.ROUTE, {**impact.parameters(gold), "field_number": 0})[0] == 400, "mixed field Impact input admitted")
        result = self.tool(impact.TOOL, {**impact.mcp_arguments(impact.parameters(gold)), "field_number": 0})
        score.require(result.get("isError") is True, "MCP unknown/mixed input admitted")
        self.report["unsupported_inputs"] = {"http_mcp_unavailable_shapes": list(impact.SECTIONS), "mixed_field_refused": True, "anonymous_http_401": True, "quality_samples_added": 0}

    def browser(self):
        self.browser_joined = False
        try:
            gold = next(q for q in self.oracle["queries"] if q["query"]["endpoint"]["operation"].endswith("/UnaryEcho") and not q["query"].get("path_prefix"))
            self.report["browser"] = impact_browser.run(self.binding, self.browser_root, {"base": self.base, "token": self.token, "query": gold, "sections": impact.SECTIONS}, self.deadline.remaining)
        finally:
            joined = self.browser_root / "joined-teardown.json"
            if joined.exists():
                ownership = checks.bounded_json(joined, 65536)
                self.browser_joined = ownership["joined"] and ownership["private_input_profile_logs_removed"]
                self.report["browser_evidence"] = {"private_root": str(self.browser_root), "joined": self.browser_joined, "joined_receipt_digest": checks.sha(joined)}
        self.check()

    def publication(self):
        gold = next(q for q in self.oracle["queries"] if q["query"]["endpoint"]["operation"].endswith("/UnaryEcho") and not q["query"].get("path_prefix"))
        before, first = self.exact(gold, 1)
        cursor = before["caller_page"]["pagination"].get("next_cursor")
        full, _, _ = self.read(gold, expected=first["generation"])
        row = next(r for r in full["caller_page"]["rows"] if r["source"]["path"] == self.transition["source_path"])
        token = row["source"]["citation"]
        score.require(cursor and self.get(impact.ROUTE, impact.parameters(gold, 1, cursor))[0] == 200 and self.get("/api/contract_callers/citation", {"citation": token})[0] == 200, "pre-transition cursor/citation not current")
        self.active_roles["primary"] = self.transition
        self.write_rewrite()
        repository = self.inputs["evaluation"]["repository"]
        payload = json.dumps({"repository": {"clone_url": "https://" + repository + ".git"}}).encode()
        signature = "sha256=" + hmac.new(self.webhook_secret.encode(), payload, hashlib.sha256).hexdigest()
        score.require(self.request("/api/webhook", payload=payload, extra={"Content-Type": "application/json", "X-GitHub-Event": "push", "X-Hub-Signature-256": signature})[0] == 202, "Impact source webhook refused")
        with ThreadPoolExecutor(max_workers=8) as pool:
            responses = list(pool.map(lambda _: self.get(impact.ROUTE, impact.parameters(gold, 1), self.bounds["cold_request_seconds"]), range(8)))
        self.commit = self.transition["transition_commit"]
        self.files = copy.deepcopy(self.files)
        changed = self.files[self.transition["source_path"]]
        changed.update(git_blob=self.transition["new_git_blob"], sha256=self.transition["new_sha256"], bytes=changed["bytes"]+len(self.transition["appended_utf8"].encode()))
        self.contents = {**self.contents, self.transition["source_path"]: self.contents[self.transition["source_path"]]+self.transition["appended_utf8"].encode()}
        after, checked = self.exact(gold, 1)
        score.require(after["caller_page"]["generation"] != before["caller_page"]["generation"], "source transition reused caller generation")
        self.bind_native(after)
        outcomes = []
        for status, raw in responses:
            value = json.loads(raw)
            if status in (404, 409):
                score.require("caller_page" not in value, "refusal returned report facts")
                outcomes.append("explicit_refusal")
            elif status == 200 and value["caller_page"]["matching_rows_state"] == "unavailable":
                impact.authority(value, gold, self.commit, unavailable=True)
                outcomes.append("explicit_unavailable")
            else:
                score.require(status == 200, "unexpected parallel Impact transition response")
                epoch = value["caller_page"]["generation"]
                score.require(epoch in (before["caller_page"]["generation"], after["caller_page"]["generation"]), "parallel Impact report mixed publication metadata")
                files = self.files if epoch == after["caller_page"]["generation"] else {r["path"]: r for r in self.inputs["evaluation"]["files"]}
                impact.check_page(value, gold, 1, self.inputs, self.oracle, epoch["commit"], files, epoch)
                impact.declaration_run(value, self.declaration_bindings[epoch["resolver_manifest_digest"]])
                outcomes.append("exact_before_or_after_epoch")
        score.require(self.get(impact.ROUTE, impact.parameters(gold, 1, cursor))[0] == 409 and self.get("/api/contract_callers/citation", {"citation": token})[0] == 409, "old report cursor/citation accepted after source transition")
        fresh, _, _ = self.read(gold, expected=checked["generation"])
        changed_row = next(r for r in fresh["caller_page"]["rows"] if r["source"]["path"] == self.transition["source_path"])
        fresh_citation = self.cite(changed_row, checked["generation"])
        for other in self.oracle["queries"]:
            value, complete, _ = self.read(other, expected=checked["generation"])
            impact.complete([complete], other)
        self.report["publication"] = {"before": before["caller_page"]["generation"], "after": after["caller_page"]["generation"], "parallel_firstpages": 8, "outcomes": outcomes, "old_cursor_and_citation": "409 after source transition; bounded cache eviction also refuses, so no unique denial-cause attribution", "fresh_changed_file_citation": fresh_citation, "repeated_truth_samples_added": 0}

    def withdrawals(self):
        gold = self.oracle["queries"][0]
        self.stop(); self.start("impact-withdrawn", ["rel-" + impact.PACK + "-1.0.0"])
        self.discovery(True, False)
        score.require(self.get(impact.ROUTE, impact.parameters(gold))[0] == 404, "withdrawn own Impact route remains")
        params = impact.parameters(gold); params.pop("shape")
        status, raw = self.get("/api/contract_callers", params)
        page = json.loads(raw)
        score.require(status == 200 and page["matching_rows_state"] == "exact" and page["generation"]["commit"] == self.commit and page["total_matching_rows"] == gold["expected_total_matching_rows"], "own withdrawal displaced exact Caller Map")
        self.stop(); self.start("caller-input-withdrawn", ["rel-phebs.grpc.caller.go-1.0.0"])
        self.discovery(False, False)
        score.require(self.get(impact.ROUTE, impact.parameters(gold))[0] == 404 and self.get("/api/contract_callers", params)[0] == 404, "consumed-input withdrawal left dependent routes")
        self.report["withdrawals"] = {"own_keeps_caller_map": True, "input_removes_dependent_impact": True, "atlas_product_independence": "separate exact-source T542 signed integration regression, no redundant native restart"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binding", type=Path, required=True)
    parser.add_argument("--helpers", type=Path)
    parser.add_argument("--run-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--execute-reviewed-binding", action="store_true")
    args = parser.parse_args()
    score.require(args.execute_reviewed_binding, "Impact execution requires exact reviewed binding and lead native slot")
    args.browser_root = args.out.with_name(args.out.stem + "-browser")
    score.require(not any(p.exists() for p in (args.out, args.run_root, args.browser_root)) and not args.out.resolve().is_relative_to(args.run_root.resolve()) and not args.browser_root.resolve().is_relative_to(args.run_root.resolve()), "Impact attempt output exists or is inside secret scratch")
    try:
        execute(args)
    except BaseException as error:
        if not args.out.exists():
            args.out.write_text(json.dumps({"schema": "phebs-t543-preparation-stop-v1", "outcome": "stopped_no_retry", "reason": str(error), "seconds": 0, "reads": [], "phases": [], "qualification": "preparation only; no native/product outcome", "production_signing": "pending existing key"}, indent=2)+"\n")
        raise


def execute(args):
    plan, inputs, oracle, contents, transition, source_receipt = impact.frozen()
    binding = checks.bounded_json(args.binding, 262144)
    if args.helpers is not None:
        shared.select_helpers(binding, args.helpers)
    metadata = validate_binding(binding)
    source = inputs["evaluation"]
    original_files = source["files"]
    censuses = [shared.verify_input_repository(binding["tools"]["git"]["path"], source["local_source_root"], source["derived_input_commit"], source["derived_input_tree"], original_files)]
    changed = copy.deepcopy(original_files)
    row = next(r for r in changed if r["path"] == transition["source_path"])
    row.update(git_blob=transition["new_git_blob"], bytes=row["bytes"]+len(transition["appended_utf8"].encode()))
    censuses.append(shared.verify_input_repository(binding["tools"]["git"]["path"], transition["local_path"], transition["transition_commit"], transition["transition_tree"], changed))
    origins = binding["source_origins"]
    score.require(len(origins) == 2 and {r["path"] for r in origins} == {source["local_source_root"], transition["local_path"]} and len({r["symbolic_head"] for r in origins}) == 1, "Impact source origins lack same-branch admission")
    for origin in origins:
        score.require(origin["symbolic_head"].startswith("refs/heads/") and qualify.command([binding["tools"]["git"]["path"], "symbolic-ref", "HEAD"], cwd=origin["path"]).strip() == origin["symbolic_head"], "Impact live source origin is detached or changed branch")
    os.umask(0o077); args.run_root.mkdir(parents=True)
    host, headroom = shared.serve.receipts.host_facts(), qualify.physical_headroom()
    free = shutil.disk_usage(args.run_root).free
    score.require(host["sysname"] == "Darwin" and host["machine"] == "arm64" and headroom["available_physical_bytes"] >= plan["bounds"]["minimum_available_physical_headroom_bytes"] and free >= plan["bounds"]["minimum_free_disk_bytes"], "Impact Darwin physical/disk admission refused")
    matrix = ImpactMatrix(args.run_root, binding, plan, inputs, oracle, contents, transition, args.browser_root)
    matrix.report.update(host=host, headroom_preflight=headroom, free_disk_preflight_bytes=free, bounds=plan["bounds"], plan_digest=checks.sha(HERE / "qualification-plan.json"), binding_digest=checks.sha(args.binding), source_commit=binding["source_commit"], binary_metadata=metadata, binary_digest=binding["binary"]["sha256"], tool_bindings=binding["tools"], input_censuses=censuses, source_coordinate_preparation=source_receipt)
    failure = None
    matrix.deadline = checks.Deadline(plan["bounds"]["complete_native_seconds"])
    try:
        artifacts, selection, public, records = sign_selection(args.run_root, binding)
        matrix.deadline.remaining()
        matrix.report["disposable_records"] = records
        matrix.config["release_selection"] = {"path": str(selection), "artifacts_path": str(artifacts), "keys": [{"id": "t543-integration-only", "public_key": public}]}
        matrix.start("impact-baseline")
        matrix.quality(); matrix.negative_inputs(); matrix.browser(); matrix.publication(); matrix.withdrawals()
        matrix.report["outcome"] = "native_matrix_complete_pending_joined_teardown"
    except BaseException as error:
        failure = error
        matrix.report.update(outcome="stopped_no_retry", reason=str(error))
    finally:
        try:
            matrix.stop()
        except BaseException as error:
            failure = failure or error
            matrix.report.update(outcome="stopped_no_retry", cleanup_failure=str(error))
        try:
            score.require(matrix.browser_joined, "Impact browser/client group not proven joined; derived custody retained")
            inherited.cleanup(matrix, args.run_root, failure is not None)
        except BaseException as error:
            failure = failure or error
            matrix.report.update(outcome="stopped_no_retry", custody_cleanup_failure=str(error))
        matrix.report["seconds"] = matrix.deadline.elapsed()
        if matrix.report["seconds"] > plan["bounds"]["complete_native_seconds"]:
            failure = failure or ValueError("Impact global wall exceeded through joined teardown")
            matrix.report.update(outcome="stopped_no_retry", wall_bound_exceeded=True)
        if failure is None:
            matrix.report.update(outcome="passed_finite_impact_native", remaining_release_gates="separate neutral canonical author/fresh comparison and production signing remain pending; legacy v2 pixels do not qualify this view")
        args.out.write_text(json.dumps(matrix.report, indent=2)+"\n")
        print(json.dumps({"outcome": matrix.report["outcome"], "receipt": str(args.out)}))
    if failure:
        raise failure


if __name__ == "__main__":
    main()
