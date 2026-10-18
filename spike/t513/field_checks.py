"""Source/proof checks for the prospective T513 matrix; no extractor or joins."""
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import sys

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.path.insert(0, str(REPO / "spike/t503"))
import requalification_checks as shared
import score

DOMAIN = "scip-proto-field"
VERSION = "1.4.0-released.1"
PACK = "phebs.protobuf.field.go"
ENDPOINTS = ("find_proto_field_references", "find_field_references")


def canonical(value):
    # The parsed HTTP bundle preserves Go struct order; map keys were already
    # sorted by Go. Match encoding/json's compact UTF-8/HTML escaping.
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    for old, new in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
        raw = raw.replace(old, new)
    return raw.encode()


def git_blob(raw):
    return hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest()


def frozen():
    plan = shared.bounded_json(HERE / "operating-plan.json", 65536)
    review = shared.bounded_json(HERE / "plan-review.json", 65536)
    score.require(review["plan_digest"] == shared.sha(HERE / "operating-plan.json"), "reviewed plan drift")
    for name, expected in plan["protected_artifacts"].items():
        score.require(shared.sha(REPO / name) == expected, "protected field artifact drift: " + name)
    prior = shared.bounded_json(REPO / "spike/t511/preregistration.json", 65536)
    population = next(p for p in prior["populations"] if p["id"] == "public")
    labels = [json.loads(line) for line in (REPO / "spike/t511/source-labels.jsonl").read_text().splitlines()]
    labels = [row for row in labels if row["population"] == "public"]
    score.require(Counter(row["label"] for row in labels) == {"positive": 14, "negative": 56}, "frozen field denominator drift")
    files, contents = [], {}
    for row in plan["population"]["files"]:
        path = REPO / "spike/t511/testdata/public" / row["path"]
        score.require(path.stat().st_size == row["bytes"] and shared.sha(path) == row["digest"], "field source bytes drift")
        raw = path.read_bytes()
        contents[row["path"]] = raw
        files.append({**row, "git_blob": git_blob(raw)})
    for row in labels:
        raw = contents[row["path"]]
        score.require("sha256:" + hashlib.sha256(raw).hexdigest() == row["source_digest"] and raw[row["start_byte"]:row["end_byte"]].decode() == row["token"], "frozen source label range drift")
        score.require(raw[:row["start_byte"]].count(b"\n") + 1 == row["line"], "frozen source label line drift")
    transition = shared.bounded_json(HERE / "planned-transition.json", 65536)
    changed = contents[transition["path"]] + transition["appended_utf8"].encode()
    score.require(git_blob(changed) == transition["new_git_blob"] and hashlib.sha256(changed).hexdigest() == transition["new_sha256"], "transition source binding drift")
    declarations = shared.bounded_json(REPO / "spike/t511/declarations.json", 65536)
    names = {d["object"]: d["name"] for d in declarations if d["population"] == "public"}
    score.require(Counter(row["object"] for row in labels if row["label"] == "positive") == plan["population"]["objects"], "field object population drift")
    return plan, population, labels, files, contents, transition, names


def query(population, object_id):
    message, number = object_id.rsplit("#", 1)
    return {"lineage": population["field_lineage"], "message": message, "field_number": int(number)}


def authority(envelope, repository, commit):
    bundle = envelope["bundle"]
    score.require(bundle["schema_version"] == "proof-bundle-v1", "wrong proof schema")
    score.require(envelope["id"] == "pb_" + hashlib.sha256(canonical(bundle)).hexdigest(), "proof content identity differs")
    score.require(bundle["query"]["domains"] == [DOMAIN], "unrelated proof domain admitted")
    repositories = bundle["coverage"]["repositories"]
    score.require(bundle["coverage"]["domains"] == [DOMAIN] and len(repositories) == 1 and repositories[0]["repository"] == repository and repositories[0]["indexed_commit"] == commit, "crossed coverage source")
    roots = bundle.get("exact_authorities", [])
    score.require(len(roots) == 1, "field authority unavailable; cannot infer exact empty")
    root = roots[0]
    score.require(root["repository"] == repository and root["commit"] == commit and root["domain"] == DOMAIN and bool(root["run_id"]), "crossed native field authority")
    score.require(all(re.fullmatch(r"sha256:[0-9a-f]{64}", root[key]) for key in ("plan_digest", "root_digest")), "malformed native field authority")
    score.require(bundle["extractor_versions"] == [{"repository": repository, "domain": DOMAIN, "run_id": root["run_id"], "extractor": VERSION}], "field recipe version/run differs")
    return root


def check_bundle(envelope, endpoint, params, repository, commit, contents, labels, names, expected=None):
    root = authority(envelope, repository, commit)
    if expected is not None:
        score.require(root == expected, "whole proof crossed its pinned publication epoch")
    bundle = envelope["bundle"]
    score.require(bundle["query"] == {"kind": endpoint, **params, "domains": [DOMAIN]}, "proof query echo differs")
    object_id = params["message"] + "#" + str(params["field_number"])
    truth = {(r["path"], r["start_byte"], r["end_byte"]): r for r in labels if r.get("object") == object_id and r["label"] == "positive"}
    evidence = {}
    for item in bundle["evidence"]:
        atom = item["atom"]
        key = atom["atom_id"]
        score.require(key not in evidence and item["repository"] == repository and item["run_id"] == root["run_id"], "duplicate/crossed field atom")
        score.require(atom["schema_version"] == "t13-v1" and atom["rule_id"] == "scip-proto-field-reference-v1" and atom["extractor_version"] == VERSION, "foreign atom recipe")
        score.require(len(item["occurrences"]) == 1, "unexpected field atom placement cardinality")
        occurrence = item["occurrences"][0]
        score.require(occurrence["repo"] == repository and occurrence["commit"] == commit and occurrence["run_id"] == root["run_id"] and occurrence["atom_id"] == key and occurrence["visibility_scope"] == "repo:" + repository, "crossed field occurrence")
        path = occurrence["path"]
        score.require(path in contents, "unexpected field source path")
        raw = contents[path]
        start, end = atom["start_byte"], atom["end_byte"]
        score.require(isinstance(start, int) and isinstance(end, int) and 0 <= start < end <= len(raw), "invalid field byte span")
        score.require(atom["blob_digest"] == "sha256:" + hashlib.sha256(raw).hexdigest(), "field atom source digest differs")
        score.require(occurrence["start_line"] == raw[:start].count(b"\n") + 1 and occurrence["end_line"] == raw[:end-1].count(b"\n") + 1, "field line/byte span differs")
        evidence[key] = item
    seen, generated, used, assertions = set(), [], set(), set()
    for assertion in bundle["assertions"]:
        score.require(assertion["id"] not in assertions, "duplicate field assertion")
        assertions.add(assertion["id"])
        score.require(assertion["repo"] == repository and assertion["run_id"] == root["run_id"] and assertion["predicate"] == "REFERENCES_PROTO_FIELD" and assertion["object"] == object_id and assertion["lineage"] == params["lineage"] and assertion["tier"] == "exact", "wrong field claim/source/family/tier")
        detail = json.loads(assertion["detail"])
        score.require(detail["schema"] == "proto-field-reference-detail-v1" and detail["name"] == names[object_id] and detail["dependency_version"] == "v1.80.0" and detail["classification"] in ("read", "write", "test", "generated", "unknown"), "wrong field detail/version")
        score.require(len(assertion["supporting"]) == 1 and not assertion.get("contradicting"), "unexpected field assertion evidence shape")
        key = assertion["supporting"][0]
        score.require(key in evidence and key not in used, "missing/reused field atom")
        used.add(key)
        item = evidence[key]; atom, occurrence = item["atom"], item["occurrences"][0]
        location = (occurrence["path"], atom["start_byte"], atom["end_byte"])
        score.require(assertion["subject"] == location[0] + ":" + str(location[1]) + "-" + str(location[2]), "field subject/source span differs")
        if occurrence["path"] == "profiling/service/service.go":
            score.require(location in truth and location not in seen and assertion["code_role"] == "production", "extra/negative/duplicate consumer field emission")
            seen.add(location)
        else:
            score.require(occurrence["path"] == "profiling/proto/service.pb.go" and assertion["code_role"] == "generated", "unexpected non-consumer field emission")
            generated.append(location)
    score.require(used == set(evidence) and seen == set(truth), "missing consumer/orphan field evidence")
    score.require(len(set(generated)) == len(generated), "duplicate generated field span")
    return {"authority": root, "consumer_spans": sorted(seen), "generated_spans_outside_quality_denominator": sorted(generated), "proof_id": envelope["id"], "bundle_digest": "sha256:" + hashlib.sha256(canonical(bundle)).hexdigest()}


def check_mcp(result, endpoint, http):
    score.require(not result.get("isError") and isinstance(result.get("structuredContent"), dict), "MCP field tool failed")
    value = result["structuredContent"]; bundle = http["bundle"]
    score.require(value["request_id"] == http["id"] and value["tool"] == {"name": endpoint, "semantic_version": "1.0"} and value["outcome"] == "partial", "MCP field contract changed")
    score.require(value["coverage"]["visibility"] == "withheld" and value["coverage"]["processing"] is None and value["coverage"]["processing_reconciled"] is False and value["result_window"]["result_set_complete"] is False and value["result_window"]["page_exhausted"] is True and not value["result_window"]["truncated"], "MCP partial/withheld semantics lost")
    score.require(value["payload"]["kind"] == "contract_edges", "wrong MCP payload")
    facts = value["payload"]["data"]["facts"]
    score.require(value["result_window"]["returned"] == len(facts) and value["absence_eligibility"]["eligible"] is False, "MCP window/absence semantics differ")
    assertions = {a["id"]: a for a in bundle["assertions"]}
    score.require(len(facts) == len(assertions) and {f["fact_id"] for f in facts} == set(assertions), "HTTP/MCP field fact census differs")
    evidence = {e["atom"]["atom_id"]: e for e in bundle["evidence"]}
    for fact in facts:
        assertion = assertions[fact["fact_id"]]
        score.require(fact["logical_relationship_id"] == assertion["id"] and fact["predicate"] == assertion["predicate"] and fact["object"] == {"kind": "proto_field", "id": assertion["lineage"] + ":" + assertion["object"]} and fact["semantic_resolution"]["value"] == "resolved" and fact["evidence_basis"] == "evidenced" and fact["verification_tool"] == "proof_bundle_api", "MCP field identity differs")
        score.require(all(fact["attribution_states"][hop]["state"] == "unresolved" for hop in ("build_target", "deployable", "owner", "service")), "MCP invented field deployment attribution")
        expected = []
        first = None
        for atom_id in assertion["supporting"]:
            for occurrence in evidence[atom_id]["occurrences"]:
                first = first or occurrence
                expected.append({"occurrence_id": occurrence["occurrence_id"], "proof_id": http["id"] + "#" + atom_id, "snapshot_id": occurrence["commit"], "verification_action": "GET /api/proof_bundles/" + http["id"]})
        score.require(fact["proof_references"] == expected and fact["subject"] == {"kind": "source_occurrence", "id": first["occurrence_id"]}, "MCP proof/source references differ")
        qualifiers = fact["qualifiers"]
        score.require(all(qualifiers[k] == v for k, v in {"repository": assertion["repo"], "tier": assertion["tier"], "lineage": assertion["lineage"], "path": first["path"], "commit": first["commit"], "start_line": str(first["start_line"]), "end_line": str(first["end_line"]), "code_role": assertion["code_role"]}.items()), "MCP source qualifiers differ")
    score.require(value["provenance"]["input_manifest_id"] == bundle["coverage"]["digest"] and value["provenance"]["extractor_version"] == VERSION, "MCP provenance differs")
    return value


def publication_metadata(data, root):
    """Five selected bounded metadata reads, never source/row replay or a scan."""
    namespace = Path(data) / "extraction-publications" / hashlib.sha256(root["repository"].encode()).hexdigest()
    domain = hashlib.sha256(DOMAIN.encode()).hexdigest()
    pointer = shared.bounded_json(namespace / ("current-" + domain + ".json"), 8192)
    score.require(pointer["repository"] == root["repository"] and pointer["domain"] == DOMAIN and pointer["plan_digest"] == root["plan_digest"] and pointer["root_digest"] == root["root_digest"] and re.fullmatch(r"sha256:[0-9a-f]{64}", pointer["generation_digest"]), "native publication pointer differs")
    directory = namespace / pointer["generation_digest"].removeprefix("sha256:")
    generation = shared.bounded_json(directory / "generation.json", 65536)
    score.require(len(generation["domains"]) == 1 and generation["repository"] == root["repository"] and generation["digest"] == pointer["generation_digest"], "unexpected native field generation")
    descriptor = generation["domains"][0]
    score.require(descriptor["domain"] == DOMAIN and descriptor["run_id"] == root["run_id"] and descriptor["plan_digest"] == root["plan_digest"] and re.fullmatch(r"plan-[0-9]{3}\.json", descriptor["plan_name"]), "native descriptor crossed field authority")
    plan = shared.bounded_json(directory / descriptor["plan_name"], 1048576)
    content = plan["plan"]
    score.require(plan["run_id"] == root["run_id"] and content["repository"] == root["repository"] and content["domain"] == DOMAIN and content["extractor_version"] == VERSION and content["digest"] == root["plan_digest"] and content["availability"] == "admitted", "native field plan differs")
    for key in ("candidate_manifest_digest", "candidate_generation_digest", "source_generation_digest", "observation_generation_digest"):
        score.require(re.fullmatch(r"sha256:[0-9a-f]{64}", content[key]), "native field plan lacks source/candidate binding")
    published = shared.bounded_json(directory / domain / "root.json", 1048576)
    score.require(published["repository"] == root["repository"] and published["domain"] == DOMAIN and published["plan_digest"] == root["plan_digest"] and published["digest"] == root["root_digest"] and published["disposition"] == "success" and published["expected_results"] == published["result_count"], "native field root incomplete or crossed")
    score.require(shared.bounded_json(namespace / ("current-" + domain + ".json"), 8192) == pointer, "native field pointer moved during metadata capture")
    return {"authority": root, "candidate_manifest_digest": content["candidate_manifest_digest"], "candidate_generation_digest": content["candidate_generation_digest"], "source_generation_digest": content["source_generation_digest"], "observation_generation_digest": content["observation_generation_digest"], "generation_digest": generation["digest"], "root_totals": published["totals"], "plan_bytes_digest": shared.sha(directory / descriptor["plan_name"])}
