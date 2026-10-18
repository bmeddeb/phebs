"""Direct frozen-oracle operation-report checks; no product classifier/join."""
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import sys

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
for folder in ("t541", "t513", "t503"):
    sys.path.insert(0, str(REPO / "spike" / folder))
import check_source as source_check
import requalification_checks as shared
import score

SHAPE = "operation_callers"
SCHEMA = "contract-impact-operation-v1"
PACK = "phebs.contract.impact.operation"
TOOL = "build_operation_impact_report"
ROUTE = "/api/operation_impact_report"
SECTIONS = ("field_references", "proposed_change", "implementations", "wire_verdict", "matching_syntactic_calls", "saved_report")
REPORT_KEYS = frozenset({"schema_version", "shape", "caller_page", "conclusion", "unavailable_sections"})
CURRENT_CAVEAT = "Repository-overlay static source evidence only; a complete generation is not runtime use, completeness, migration completion, or decommissioning safety."
GAP_CAVEAT = "Caller totals and absence are unavailable until one exact complete repository-overlay generation is current."
QUALIFICATIONS = {
    "evidence_present": "Static caller evidence matches this endpoint scope; per-row resolution applies. This is not runtime impact, completeness or retirement safety.",
    "exact_empty": "No matching static caller evidence in this exact filtered source scope; this does not establish absence.",
    "unavailable": "Caller evidence is unavailable for this generation; no zero-caller or absence conclusion is made.",
}


def frozen():
    plan = shared.bounded_json(HERE / "qualification-plan.json", 65536)
    review = shared.bounded_json(HERE / "plan-review.json", 65536)
    score.require(review["accepted"] is True and review["plan_digest"] == shared.sha(HERE / "qualification-plan.json"), "operation qualification plan is not independently accepted")
    for name, digest in plan["protected_artifacts"].items():
        score.require(shared.sha(REPO / name) == digest, "protected operation artifact drift: " + name)
    binding = shared.bounded_json(REPO / "spike/t541/input-binding.json", 65536)
    oracle = shared.bounded_json(REPO / "spike/t541/oracle.json", 65536)
    original_review = shared.bounded_json(REPO / "spike/t541/source-review.json", 65536)
    score.require(original_review["checkpoint_sha256"] == shared.sha(REPO / "spike/t541/checkpoint.json").removeprefix("sha256:"), "frozen source-only review binding differs")
    root = Path(binding["evaluation"]["local_source_root"])
    source_receipt = source_check.verify(binding, oracle, root)
    contents = {row["path"]: (root / row["path"]).read_bytes() for row in binding["evaluation"]["files"]}
    transition = shared.bounded_json(HERE / "planned-transition.json", 65536)["record"]
    raw = contents[transition["source_path"]] + transition["appended_utf8"].encode()
    blob = hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest()
    score.require(blob == transition["new_git_blob"] and hashlib.sha256(raw).hexdigest() == transition["new_sha256"], "frozen operation transition byte binding differs")
    score.require(shared.sha(HERE / "transition.bundle") == "sha256:" + transition["bundle"]["sha256"], "retained exact transition bundle differs")
    return plan, binding, oracle, contents, transition, source_receipt


def query(gold):
    return {**gold["query"], "freshness": "any", "resolution": "any", "ordering": "source"}


def parameters(gold, size=100, cursor=""):
    expected = query(gold)
    endpoint = expected.pop("endpoint")
    return {"shape": SHAPE, "protocol": endpoint["protocol"], "repository": endpoint["repository"], "lineage": endpoint["declaration_lineage"], "operation": endpoint["operation"], **expected, "page_size": size, "cursor": cursor}


def mcp_arguments(params):
    result = dict(params)
    result["declaration_lineage"] = result.pop("lineage")
    return result


def authority(report, gold, commit, expected=None, unavailable=False):
    score.require(type(report) is dict and REPORT_KEYS <= set(report) <= REPORT_KEYS | {"$schema"}, "operation report added/omitted top-level sections")
    if "$schema" in report:
        metadata = report["$schema"]
        score.require(isinstance(metadata, str) and 0 < len(metadata) <= 4096 and len(metadata.encode("utf-8")) <= 4096, "operation report HTTP schema metadata malformed/unbounded")
    score.require(report["schema_version"] == SCHEMA and report["shape"] == SHAPE, "wrong operation report shape/schema")
    page, conclusion = report["caller_page"], report["conclusion"]
    score.require(report["unavailable_sections"] == {key: "not_in_supported_shape" for key in SECTIONS}, "operation report broadened unsupported sections")
    score.require(page["schema_version"] == "caller-map-v2" and page["query"] == query(gold) and conclusion["query"] == page["query"] and not any(key in page for key in ("coverage", "coverage_digest", "attribution_digest")), "report query or synthetic coverage differs")
    generation = page["generation"]
    scope = page["scope"]
    score.require(generation["repository"] == gold["query"]["endpoint"]["repository"] and generation["plane"] == "repository-overlay" and scope["repository"] == generation["repository"] and scope["scope_posture"] == "whole-repository" and not scope.get("analysis_unit"), "report crossed repository or invented focused-local scope")
    for metadata in (scope, generation):
        if "commit" in metadata:
            score.require(isinstance(metadata["commit"], str) and re.fullmatch(r"(?:[a-f0-9]{40}|[a-f0-9]{64})", metadata["commit"]), "report source commit malformed")
    score.require(conclusion.get("generation_digest") == generation.get("generation_digest") and page["caveat"] == (GAP_CAVEAT if unavailable else CURRENT_CAVEAT), "report generation/conclusion/owning caveat differs")
    if unavailable:
        score.require(page["matching_rows_state"] == "unavailable" and generation["state"] in ("missing", "stale", "failed") and "declaration" not in page and page["rows"] == [] and "total_matching_rows" not in page and page["pagination"] == {"complete": True} and conclusion["state"] == "unavailable" and conclusion["qualification"] == QUALIFICATIONS["unavailable"], "unavailable report invented declaration, totals, rows or absence")
        return generation
    score.require(page["matching_rows_state"] == "exact" and generation["state"] == "current" and generation["commit"] == commit and scope.get("commit") == commit and not generation.get("unit_digest"), "report authority unavailable/crossed source commit/unit")
    for key in ("generation_digest", "declaration_set_digest", "candidate_manifest_digest", "resolver_manifest_digest", "pair_set_digest", "manifest_digest"):
        score.require(re.fullmatch(r"sha256:[a-f0-9]{64}", generation[key]), "malformed operation generation metadata")
    score.require(type(generation["publication_revision"]) is int and generation["publication_revision"] > 0, "missing exact publication revision")
    progress = generation["partition_progress"]
    total = generation.get("pair_count", 0)
    score.require(progress["state"] == "complete" and progress.get("settled_pair_count", 0) == progress.get("succeeded_pair_count", 0) == progress["total_pair_count"] == total and not progress.get("refused_pair_count") and not progress.get("refusals"), "current report has incomplete/refused caller publication")
    counts = generation["record_counts"]
    score.require(all(type(counts[k]) is int and counts[k] >= 0 for k in ("candidate_records", "base_records", "excluded_go_test_records")) and counts["base_records"] + counts["excluded_go_test_records"] == counts["candidate_records"], "report candidate census is inconsistent")
    score.require(expected is None or generation == expected, "operation report crossed its complete pinned generation")
    state = gold["expected_conclusion_state"]
    score.require(conclusion["state"] == state and conclusion["qualification"] == QUALIFICATIONS[state] and page["total_matching_rows"] == gold["expected_total_matching_rows"], "operation report conclusion/count differs from direct gold")
    return generation


def check_page(report, gold, size, binding, oracle, commit, files, expected=None):
    generation = authority(report, gold, commit, expected)
    page = report["caller_page"]
    score.require(len(page["rows"]) <= size and isinstance(page["pagination"]["complete"], bool), "operation page exceeded bound")
    if page["pagination"]["complete"]:
        score.require(not page["pagination"].get("next_cursor"), "completed operation page has continuation")
    else:
        score.require(len(page["rows"]) == size and isinstance(page["pagination"].get("next_cursor"), str) and 0 < len(page["pagination"]["next_cursor"]) <= 16384, "operation continuation malformed")
    declaration = next(d for d in oracle["declarations"] if d["operation"] == gold["query"]["endpoint"]["operation"])
    claim, truth = page["declaration"], declaration["declaration"]
    score.require(claim["predicate"] == "DECLARES_OPERATION" and claim["object"] == truth["object"] and claim["lineage"] == gold["query"]["endpoint"]["declaration_lineage"] and claim["tier"] == "exact" and len(claim["sources"]) == 1 and not claim.get("sources_truncated"), "report declaration identity differs")
    source = claim["sources"][0]
    score.require(all(source[k] == truth[k] for k in ("path", "start_byte", "end_byte", "start_line", "end_line")) and source["repository"] == binding["evaluation"]["repository"] and source["commit"] == commit and source["assertion_id"] == claim["assertion_id"] and source["run_id"] == claim["run_id"] and bool(source["atom_id"]), "report declaration source tuple differs")
    sites = {s["site_id"]: s for s in oracle["sites"]}
    observed = []
    resolutions = Counter()
    for row in page["rows"]:
        source = row["source"]; key = score.site_id(source)
        score.require(key in gold["expected_site_ids"] and key not in observed, "extra/wrong-operation/duplicate caller membership")
        truth, file = sites[key], files[source["path"]]
        score.require(row["classification"] == "resolved_caller" and row["protocol"] == "protobuf" and row["operation"] == truth["wire_operation"] and row["declaration_lineage"] == gold["query"]["endpoint"]["declaration_lineage"] and row["fresh"] is True and row["code_role"] == "production" and not row.get("unresolved_reason"), "report source edge/discriminator differs")
        score.require((row["resolution"], row["tier"]) in (("syntax", "heuristic"), ("scip", "derived")), "resolved static edge promoted or abstained")
        resolutions[row["resolution"] + "/" + row["tier"]] += 1
        score.require(source["repository"] == binding["evaluation"]["repository"] and source["commit"] == commit and source["run_id"] == generation["generation_digest"] and source["object_id"] == file["git_blob"] and source["blob_digest"] == "sha256:" + file["sha256"] and source["atom_id"] == source["blob_digest"] and source["plane"] == "repository-overlay" and re.fullmatch(r"sha256:[a-f0-9]{64}", source["assertion_id"]) and isinstance(source["citation"], str) and 0 < len(source["citation"]) <= 16384 and source["start_line"] == truth["start_line"] and source["end_line"] == truth["end_line"], "report occurrence source authority differs")
        unit, target = row["unit"], truth["unit"]
        score.require(unit["state"] == target["state"], "report supplied unit state differs")
        candidates = unit.get("candidates", [])
        if target["state"] == "resolved":
            score.require(unit.get("candidate_total") == 1 and len(candidates) == 1 and not unit.get("reason") and candidates[0]["id"] == target["id"] and candidates[0].get("build_targets", []) == target["build_targets"] and all(not candidates[0].get(k) for k in ("deployables", "logical_services", "owners")), "report supplied unit ID/target changed or invented deployment")
        else:
            score.require(unit.get("reason") == target["reason"] and len(candidates) <= 1 and unit.get("candidate_total", 0) == len(candidates), "report unavailable unit altered")
            for candidate in candidates:
                score.require(re.fullmatch(r"au_[a-f0-9]{64}", candidate["id"]) and all(not candidate.get(k) for k in ("build_targets", "deployables", "logical_services", "owners")), "unavailable ID-only candidate invented attribution")
        score.require(row["unit_group"].startswith(unit["state"] + ":") or row["unit_group"] == unit["state"], "unit grouping contradicted attribution state")
        observed.append(key)
    return {"generation": generation, "site_ids": observed, "resolutions": dict(resolutions)}


def complete(pages, gold):
    sites = [key for p in pages for key in p["site_ids"]]
    score.require(len(sites) == len(set(sites)) and set(sites) == set(gold["expected_site_ids"]), "operation page traversal lost/duplicated direct gold membership")
    return sites


def declaration_run(report, selected):
    score.require(selected["domain"] == "proto-contract" and selected["authority_schema"] == "phebs-partitioned-extraction-domain-v1" and selected["generation_digest"] == selected["root_digest"] and report["caller_page"]["declaration"]["run_id"] == selected["run_id"], "report declaration crossed the actually consumed resolver publication")


def wilson(success, count):
    if count == 0:
        return None
    z, p = 1.959963984540054, success / count
    denominator = 1 + z*z/count
    center = (p + z*z/(2*count))/denominator
    half = z*((p*(1-p)/count + z*z/(4*count*count))**0.5)/denominator
    return [center-half, center+half]


def metrics(oracle, seen):
    score.require(set(seen) == {q["query_id"] for q in oracle["queries"]}, "composed quality matrix incomplete")
    per_operation, per_file = {}, {}
    for gold in oracle["queries"]:
        actual, expected = set(seen[gold["query_id"]]), set(gold["expected_site_ids"])
        if gold["query"].get("path_prefix"):
            score.require(not actual, "scoped empty query returned source evidence")
            continue
        tp, fn = len(actual & expected), len(expected - actual)
        fp, tn = len(actual - expected), len(set(gold["excluded_site_ids"]) - actual)
        per_operation[gold["query"]["endpoint"]["operation"]] = {"tp": tp, "fn": fn, "fp": fp, "tn": tn}
        for site in oracle["sites"]:
            row = per_file.setdefault(site["path"], {"tp": 0, "fn": 0, "fp": 0, "tn": 0})
            state = ("tp" if site["site_id"] in actual else "fn") if site["site_id"] in expected else ("fp" if site["site_id"] in actual else "tn")
            row[state] += 1
    totals = {key: sum(row[key] for row in per_operation.values()) for key in ("tp", "fn", "fp", "tn")}
    score.require(totals == {"tp": 9, "fn": 0, "fp": 0, "tn": 36}, "direct composed quality threshold failed")
    return {"quality_scope": "Actual new operation_callers report memberships over all frozen T5415operations/9source sites, directly scored; no component accuracy transfer or repeated-read samples", "counts": totals, "precision": totals["tp"]/(totals["tp"]+totals["fp"]), "recall": totals["tp"]/(totals["tp"]+totals["fn"]), "positive_wilson_95_descriptive": wilson(totals["tp"], 9), "negative_wilson_95_descriptive": wilson(totals["tn"], 36), "per_operation": per_operation, "per_file": per_file, "scoped_empty_queries": 2, "interval_qualification": "Descriptive finite composition memberships; correlated sites and selected source do not support population inference"}
