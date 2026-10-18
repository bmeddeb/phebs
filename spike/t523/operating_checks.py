"""Prospective T523 source/proof checks; no extractor or synthetic joins."""
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import sys

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.path.insert(0, str(REPO / "spike/t513"))
import field_checks as common
import qualify_fields as lifecycle
import requalification_checks as checks
import qualify
import score

DOMAIN = "scip-thrift-field"
ENDPOINT = "find_field_references"
PACKS = {"apache": "phebs.thrift.field.apache", "thriftrw": "phebs.thrift.field.thriftrw"}


def frozen():
    plan = checks.bounded_json(HERE / "operating-plan.json", 65536)
    review = checks.bounded_json(HERE / "operating-plan-review.json", 65536)
    score.require(review["plan_digest"] == checks.sha(HERE / "operating-plan.json") and review["accepted"] is True, "T523 plan is not independently accepted")
    for name, digest in plan["protected_artifacts"].items():
        score.require(checks.sha(REPO / name) == digest, "protected Thrift artifact drift: " + name)
    inputs = checks.bounded_json(HERE / "operating-inputs.json", 65536)
    records, labels, contents, names = inputs["records"], {}, {}, {}
    score.require([r["generator"] for r in records] == ["apache", "thriftrw"], "Thrift input roles changed")
    for record in records:
        generator, ticket = record["generator"], record["source_ticket"]
        rows = [json.loads(line) for line in (REPO / "spike" / ticket / "source-labels.jsonl").read_text().splitlines()]
        labels[generator] = [r for r in rows if r["population"] == "public"]
        score.require(Counter(r["expected"] for r in labels[generator]) == {"positive": record["positive"], "negative": record["negative"]}, "frozen Thrift label denominator differs")
        contents[generator] = {}
        for row in record["files"]:
            path = REPO / row["retained_path"]
            score.require(path.is_file() and not path.is_symlink() and path.stat().st_size == row["bytes"] and checks.sha(path) == row["digest"], "frozen Thrift source/index bytes differ")
            raw = path.read_bytes()
            score.require(common.git_blob(raw) == row["git_blob"], "Thrift Git blob binding differs")
            contents[generator][row["path"]] = raw
        for row in labels[generator]:
            raw = contents[generator][row["path"]]
            score.require("sha256:" + hashlib.sha256(raw).hexdigest() == row["source_digest"] and raw[row["start_byte"]:row["end_byte"]].decode() == row["text"] and raw[:row["start_byte"]].count(b"\n") + 1 == row["line"], "frozen Thrift label span differs")
        score.require(Counter(r["object"] for r in labels[generator] if r["expected"] == "positive") == record["objects"], "frozen Thrift object census differs")
        witnesses = checks.bounded_json(REPO / "spike" / ticket / "field-witnesses.json", 65536)["fields"]
        names[generator] = {}
        for witness in witnesses:
            if witness["population"] != "public":
                continue
            object_id = witness.get("object")
            if object_id is None:
                scope = Path(witness["generated_path"]).parent.name
                object_id = scope + "." + witness["generated_message"] + "#" + str(witness["field_number"])
            score.require(object_id not in names[generator], "duplicate frozen Thrift field witness")
            names[generator][object_id] = witness["thrift_name"]
        score.require(set(record["objects"]) <= set(names[generator]), "Thrift public object witness missing")
    score.require(sum(len(r["files"]) for r in records) == 48 and sum(f["bytes"] for r in records for f in r["files"]) == 1291607, "frozen complete Thrift input census differs")
    return plan, records, labels, contents, names


def verify_repository(git, record):
    """Exact frozen census, not the narrower T503 32-file admission helper."""
    path, commit = record["local_path"], record["commit"]
    score.require(qualify.command([git, "symbolic-ref", "HEAD"], cwd=path).strip() == record["symbolic_head"], "Thrift live symbolic default branch differs")
    score.require(qualify.command([git, "rev-parse", "HEAD"], cwd=path).strip() == commit and qualify.command([git, "rev-parse", commit + "^{tree}"], cwd=path).strip() == record["tree"], "Thrift evaluation commit/tree drift")
    score.require(not qualify.command([git, "status", "--porcelain", "--untracked-files=all"], cwd=path).strip(), "Thrift evaluation clone is not pristine")
    census = {}
    raw = qualify.command([git, "ls-tree", "-rl", "-z", "--full-tree", commit], cwd=path)
    for item in raw.split("\0"):
        if not item:
            continue
        metadata, name = item.split("\t", 1)
        mode, kind, oid, size = metadata.split()
        score.require(mode == "100644" and kind == "blob" and name not in census and len(census) < 64, "Thrift tree file admission differs")
        census[name] = {"git_blob": oid, "bytes": int(size)}
    expected = {r["path"]: {"git_blob": r["git_blob"], "bytes": r["bytes"]} for r in record["files"]}
    score.require(census == expected and sum(r["bytes"] for r in census.values()) <= 16777216, "Thrift complete committed census differs")
    score.require(checks.sha(REPO / record["bundle"]) == record["bundle_digest"], "Thrift committed input bundle drift")
    return {"repository": record["repository"], "commit": commit, "tree": record["tree"], "symbolic_head": record["symbolic_head"], "files": len(census), "bytes": sum(r["bytes"] for r in census.values()), "census_digest": "sha256:" + hashlib.sha256(json.dumps(census, sort_keys=True, separators=(",", ":")).encode()).hexdigest()}


def query(record, object_id):
    message, number = object_id.rsplit("#", 1)
    return {"lineage": record["field_lineage"], "message": message, "field_number": int(number)}


def authority(envelope, records, version):
    bundle = envelope["bundle"]
    score.require(bundle["schema_version"] == "proof-bundle-v1" and envelope["id"] == "pb_" + hashlib.sha256(common.canonical(bundle)).hexdigest(), "Thrift proof content identity differs")
    score.require(bundle["query"]["domains"] == [DOMAIN] and bundle["coverage"]["domains"] == [DOMAIN], "foreign field proof domain")
    commits = {r["repository"]: r["commit"] for r in records}
    coverage = bundle["coverage"]["repositories"]
    score.require(len(coverage) == len(commits) and {r["repository"]: r["indexed_commit"] for r in coverage} == commits, "Thrift global coverage source census differs")
    roots = bundle.get("exact_authorities", [])
    score.require(len(roots) == len(commits) and {r["repository"] for r in roots} == set(commits), "Thrift current root unavailable; cannot infer empty")
    for root in roots:
        score.require(root["commit"] == commits[root["repository"]] and root["domain"] == DOMAIN and bool(root["run_id"]) and all(re.fullmatch(r"sha256:[0-9a-f]{64}", root[k]) for k in ("plan_digest", "root_digest")), "crossed/malformed Thrift root")
    expected_versions = [{"repository": r["repository"], "domain": DOMAIN, "run_id": r["run_id"], "extractor": version} for r in roots]
    score.require(bundle["extractor_versions"] == expected_versions, "Thrift selected-set version/run differs")
    return roots


def check_bundle(envelope, params, record, records, selected, version, contents, labels, names, expected=None):
    roots = authority(envelope, records, version)
    score.require(expected is None or roots == expected, "Thrift whole proof crossed publication epoch")
    bundle = envelope["bundle"]
    score.require(bundle["query"] == {"kind": ENDPOINT, **params, "domains": [DOMAIN]}, "Thrift field query echo differs")
    object_id = params["message"] + "#" + str(params["field_number"])
    generator, repository = record["generator"], record["repository"]
    root = next(r for r in roots if r["repository"] == repository)
    truth = {(r["path"], r["start_byte"], r["end_byte"]): r for r in labels[generator] if r.get("object") == object_id and r["expected"] == "positive" and generator in selected}
    evidence = {}
    for item in bundle["evidence"]:
        atom = item["atom"]; key = atom["atom_id"]
        score.require(key not in evidence and item["repository"] == repository and item["run_id"] == root["run_id"] and generator in selected, "duplicate/foreign/unselected Thrift evidence")
        score.require(atom["schema_version"] == "t22-v1" and atom["rule_id"] == "scip-thrift-field-reference-v1" and atom["extractor_version"] == version and len(item["occurrences"]) == 1, "Thrift atom recipe/cardinality differs")
        occurrence = item["occurrences"][0]
        score.require(occurrence["repo"] == repository and occurrence["commit"] == record["commit"] and occurrence["run_id"] == root["run_id"] and occurrence["atom_id"] == key and occurrence["visibility_scope"] == "repo:" + repository, "crossed Thrift occurrence")
        path, start, end = occurrence["path"], atom["start_byte"], atom["end_byte"]
        score.require(path in contents[generator] and type(start) is int and type(end) is int, "foreign source or non-integer Thrift span")
        raw = contents[generator][path]
        score.require(0 <= start < end <= len(raw) and atom["blob_digest"] == "sha256:" + hashlib.sha256(raw).hexdigest(), "Thrift source bytes/span differ")
        score.require(occurrence["start_line"] == raw[:start].count(b"\n") + 1 and occurrence["end_line"] == raw[:end-1].count(b"\n") + 1, "Thrift line/span differs")
        evidence[key] = item
    seen, generated, used, ids = set(), set(), set(), set()
    for assertion in bundle["assertions"]:
        score.require(assertion["id"] not in ids and assertion["repo"] == repository and assertion["run_id"] == root["run_id"] and assertion["predicate"] == "REFERENCES_THRIFT_FIELD" and assertion["object"] == object_id and assertion["lineage"] == params["lineage"] and assertion["tier"] == record["tier"], "wrong Thrift claim/tier/source/family")
        ids.add(assertion["id"])
        detail = json.loads(assertion["detail"])
        score.require(detail["schema"] == "thrift-field-reference-detail-v1" and detail["name"] == names[generator][object_id] and detail["dependency_version"] == record["module_version"] and detail["generator"] == generator and detail["source_binding"] == record["source_binding"] and detail["classification"] == "read", "Thrift field detail/version/binding differs")
        score.require(len(assertion["supporting"]) == 1 and not assertion.get("contradicting"), "unexpected Thrift support shape")
        key = assertion["supporting"][0]
        score.require(key in evidence and key not in used, "missing/reused Thrift atom")
        used.add(key)
        item = evidence[key]; atom, occurrence = item["atom"], item["occurrences"][0]
        location = (occurrence["path"], atom["start_byte"], atom["end_byte"])
        score.require(assertion["subject"] == location[0] + ":" + str(location[1]) + "-" + str(location[2]), "Thrift subject/span differs")
        if location[0] in record["application"]:
            score.require(location in truth and location not in seen and assertion["code_role"] == "production", "extra/negative/duplicate Thrift consumer fact")
            seen.add(location)
        else:
            # Current codeRole recognizes .gen/go or SCIP's Generated bit,
            # not Apache headers. These pinned indexes report raw read roles;
            # generated source stays outside consumer truth by frozen path.
            expected_role = "production" if generator == "apache" else "generated"
            score.require(location[0].startswith(record["generated_prefix"]) and location not in generated and assertion["code_role"] == expected_role, "unexpected/duplicate nonconsumer Thrift fact")
            generated.add(location)
    score.require(used == set(evidence) and seen == set(truth), "missing Thrift consumer or orphan atom")
    receipt = {"authorities": roots, "consumer_spans": sorted(seen), "generated_spans_outside_quality_denominator": sorted(generated), "proof_id": envelope["id"], "generator_selected": generator in selected}
    if params["field_number"] == 0:
        receipt["field0_mechanics"] = {
            "scope": "numeric_zero_transport_admission_only; no generated-reference recall denominator",
            "returned_generated_references": len(generated),
            "live_reference_claim": bool(generated),
            "reference_scope": "only returned exact source/span facts were validated" if generated else "zero returned facts establish no live reference or source/runtime absence",
        }
    return receipt


def check_mcp(result, http, version):
    score.require(not result.get("isError") and isinstance(result.get("structuredContent"), dict), "Thrift MCP tool failed")
    value, bundle = result["structuredContent"], http["bundle"]
    score.require(value["request_id"] == http["id"] and value["tool"] == {"name": ENDPOINT, "semantic_version": "1.0"} and value["outcome"] == "partial", "Thrift MCP contract differs")
    score.require(value["coverage"]["visibility"] == "withheld" and value["coverage"]["processing"] is None and value["coverage"]["processing_reconciled"] is False and value["result_window"]["result_set_complete"] is False and value["result_window"]["page_exhausted"] is True and not value["result_window"]["truncated"] and value["absence_eligibility"]["eligible"] is False, "Thrift MCP uncertainty lost")
    score.require(value["payload"]["kind"] == "contract_edges", "wrong Thrift MCP payload")
    facts = value["payload"]["data"]["facts"]
    assertions = {a["id"]: a for a in bundle["assertions"]}
    score.require(value["result_window"]["returned"] == len(facts) == len(assertions) and {f["fact_id"] for f in facts} == set(assertions), "Thrift HTTP/MCP census differs")
    evidence = {e["atom"]["atom_id"]: e for e in bundle["evidence"]}
    for fact in facts:
        assertion = assertions[fact["fact_id"]]
        score.require(fact["logical_relationship_id"] == assertion["id"] and fact["predicate"] == assertion["predicate"] and fact["object"] == {"kind": "thrift_field", "id": assertion["lineage"] + ":" + assertion["object"]} and fact["semantic_resolution"]["value"] == "resolved" and fact["evidence_basis"] == "evidenced" and fact["verification_tool"] == "proof_bundle_api", "Thrift MCP fact identity differs")
        score.require(all(fact["attribution_states"][k]["state"] == "unresolved" for k in ("build_target", "deployable", "owner", "service")), "Thrift MCP invented attribution")
        atom_id = assertion["supporting"][0]; occurrence = evidence[atom_id]["occurrences"][0]
        expected = [{"occurrence_id": occurrence["occurrence_id"], "proof_id": http["id"] + "#" + atom_id, "snapshot_id": occurrence["commit"], "verification_action": "GET /api/proof_bundles/" + http["id"]}]
        score.require(fact["proof_references"] == expected and fact["subject"] == {"kind": "source_occurrence", "id": occurrence["occurrence_id"]}, "Thrift MCP proof references differ")
        qualifiers = {"repository": assertion["repo"], "tier": assertion["tier"], "lineage": assertion["lineage"], "path": occurrence["path"], "commit": occurrence["commit"], "start_line": str(occurrence["start_line"]), "end_line": str(occurrence["end_line"]), "code_role": assertion["code_role"]}
        score.require(all(fact["qualifiers"][k] == v for k, v in qualifiers.items()), "Thrift MCP source qualifiers differ")
    score.require(value["provenance"]["input_manifest_id"] == bundle["coverage"]["digest"] and value["provenance"]["extractor_version"] == version, "Thrift MCP provenance differs")


def publication_metadata(data, root, version):
    """Five selected metadata reads per visible repository and phase."""
    namespace = Path(data) / "extraction-publications" / hashlib.sha256(root["repository"].encode()).hexdigest()
    domain = hashlib.sha256(DOMAIN.encode()).hexdigest()
    pointer_path = namespace / ("current-" + domain + ".json")
    pointer = checks.bounded_json(pointer_path, 8192)
    score.require(all(pointer[k] == root[k] for k in ("repository", "domain", "plan_digest", "root_digest")) and re.fullmatch(r"sha256:[0-9a-f]{64}", pointer["generation_digest"]), "Thrift native pointer differs")
    directory = namespace / pointer["generation_digest"].removeprefix("sha256:")
    generation = checks.bounded_json(directory / "generation.json", 65536)
    score.require(len(generation["domains"]) == 1 and generation["repository"] == root["repository"] and generation["digest"] == pointer["generation_digest"], "Thrift native generation differs")
    descriptor = generation["domains"][0]
    score.require(all(descriptor[k] == root[k] for k in ("domain", "run_id", "plan_digest")) and re.fullmatch(r"plan-[0-9]{3}\.json", descriptor["plan_name"]), "Thrift native descriptor differs")
    content = checks.bounded_json(directory / descriptor["plan_name"], 1048576)
    plan = content["plan"]
    score.require(content["run_id"] == root["run_id"] and plan["repository"] == root["repository"] and plan["domain"] == DOMAIN and plan["extractor_version"] == version and plan["digest"] == root["plan_digest"] and plan["availability"] in ("admitted", "empty"), "Thrift native plan differs")
    for key in ("candidate_manifest_digest", "candidate_generation_digest", "source_generation_digest", "observation_generation_digest"):
        score.require(re.fullmatch(r"sha256:[0-9a-f]{64}", plan[key]), "Thrift native candidate/source binding missing")
    published = checks.bounded_json(directory / domain / "root.json", 1048576)
    score.require(all(published[k] == root[k] for k in ("repository", "domain", "plan_digest")) and published["digest"] == root["root_digest"] and published["disposition"] in ("success", "empty") and published["expected_results"] == published["result_count"], "Thrift native root incomplete/crossed")
    score.require(checks.bounded_json(pointer_path, 8192) == pointer, "Thrift pointer moved during metadata capture")
    return {"authority": root, "selected_set_version": version, "generation_digest": generation["digest"], "candidate_manifest_digest": plan["candidate_manifest_digest"], "candidate_generation_digest": plan["candidate_generation_digest"], "source_generation_digest": plan["source_generation_digest"], "observation_generation_digest": plan["observation_generation_digest"], "root_totals": published["totals"], "disposition": published["disposition"], "plan_bytes_digest": checks.sha(directory / descriptor["plan_name"])}
