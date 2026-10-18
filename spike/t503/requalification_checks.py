"""Bounded evidence checks for the prospective T503 completion; no classifier."""
import copy
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import time

import score


def sha(path):
    return "sha256:" + hashlib.sha256(Path(path).read_bytes()).hexdigest()


def bounded_json(path, maximum):
    path = Path(path)
    score.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= maximum, "missing, linked or oversized evidence file")
    return json.loads(path.read_bytes())


class Deadline:
    def __init__(self, seconds, clock=time.monotonic):
        self.clock = clock
        self.started = clock()
        self.limit = self.started + seconds

    def remaining(self):
        left = self.limit - self.clock()
        score.require(left > 0, "global native wall deadline exceeded")
        return left

    def timeout(self, maximum):
        return min(maximum, self.remaining())

    def elapsed(self):
        return self.clock() - self.started


def check_frozen(here):
    plan = bounded_json(here / "requalification-plan.json", 65536)
    for path, expected in plan["protected_artifacts"].items():
        score.require(sha(here.parents[1] / path) == expected, "frozen artifact drift: " + path)
    first = bounded_json(here / "first-attempt-checkpoint.json", 65536)
    for item in first["files"]:
        score.require(sha(here / item["path"]) == "sha256:" + item["sha256"], "first-attempt artifact drift")
    return plan


def filtered_truth(labels, level, filters):
    """Select complete prereviewed gold rows; never derive a new class/join."""
    result = copy.deepcopy(labels)
    sites = {s["site_id"]: s for s in labels["sites"]}
    selected = []
    for row in labels["expected_" + level + "_rows"]:
        if filters.get("classification") and row["classification"] != filters["classification"]:
            continue
        members = [sites[key] for key in row["site_ids"]]
        accepted = [not filters.get("path_prefix") or s["path"].startswith(filters["path_prefix"]) for s in members]
        if any(accepted) and not all(accepted):
            raise ValueError("fixture filter would require a new group/class label")
        if not any(accepted):
            continue
        if filters.get("unit") and not all(s["unit"].get("id") == filters["unit"] for s in members):
            continue
        selected.append(row)
    keys = {key for row in selected for key in row["site_ids"]}
    result["expected_" + level + "_rows"] = selected
    result["sites"] = [s for s in result["sites"] if s["site_id"] in keys]
    return result


def unit_sample_projection(pages):
    """Expand actual outer attribution for the frozen occurrence-shaped scorer."""
    projected = copy.deepcopy(pages)
    for page in projected:
        score.require(page["query"]["level"] == "unit", "unit projection received another level")
        for row in page["rows"]:
            outer = row.get("unit")
            if outer is None:
                continue
            score.require(row["level"] == "unit" and row["classification"] in {
                "old_only_evidence", "new_only_evidence", "both_evidence"
            } and isinstance(outer, dict) and bool(outer.get("id")), "invalid promoted unit row")
            for side in ("old", "replacement"):
                for item in row[side]["rows"]:
                    unit = item["unit"]
                    score.require(unit.get("state") == "resolved" and not unit.get("reason") and
                                  not unit.get("candidates") and not unit.get("candidate_total"),
                                  "promoted sample contains conflicting attribution")
                    unit.update(candidates=[copy.deepcopy(outer)], candidate_total=1)
    return projected


def check_rows(pages, labels, binding, level, repository):
    if level == "unit":
        pages = unit_sample_projection(pages)
    return score.check_rows(pages, labels, binding, level, repository)


def generation_key(generation):
    return tuple(generation.get(key) for key in (
        "repository", "commit", "generation_digest", "candidate_manifest_digest",
        "resolver_manifest_digest", "pair_set_digest", "manifest_digest", "publication_revision",
    ))


def assert_authorities(page, endpoints, commits):
    score.require(page["schema_version"] == "caller-comparison-v2", "wrong comparison schema")
    score.require(page["matching_rows_state"] == "exact", "comparison authority is unavailable")
    for side, repository in endpoints.items():
        generation = page[side]["generation"]
        score.require(page[side]["matching_rows_state"] == "exact" and generation["state"] == "current", "noncurrent exact side")
        score.require(generation["repository"] == repository and generation["commit"] == commits[repository], "crossed side revision")
        score.require(all(generation.get(key) for key in ("generation_digest", "candidate_manifest_digest", "resolver_manifest_digest", "pair_set_digest", "manifest_digest", "publication_revision")), "incomplete authority binding")
        for row in page["rows"]:
            for item in row[side]["rows"]:
                source = item["source"]
                score.require(source["repository"] == repository and source["commit"] == commits[repository], "mixed source generation")
                score.require(source["run_id"] == generation["generation_digest"], "source run differs from caller authority")


def assert_transition_response(status, page, endpoints, before, after):
    if status in (404, 409):
        score.require(not page.get("rows") and "total_rows" not in page, "refusal leaked comparison rows")
        return "explicit_refusal"
    score.require(status == 200, "unexpected publication-transition status")
    if page.get("matching_rows_state") == "unavailable":
        score.require(page.get("rows") == [] and page.get("total_rows") is None, "unavailable authority fabricated exact empty")
        return "explicit_unavailable"
    commits = {repository: page[side]["generation"]["commit"] for side, repository in endpoints.items()}
    assert_authorities(page, endpoints, commits)
    observed = tuple(generation_key(page[side]["generation"]) for side in ("old", "replacement"))
    allowed = {tuple(generation_key(epoch[side]["generation"]) for side in ("old", "replacement")) for epoch in (before, after)}
    score.require(observed in allowed, "parallel response mixed independent publication epochs")
    return "exact_epoch"


def assert_quality_authorities(page, initial, commits):
    endpoints = {side: initial[side]["generation"]["repository"] for side in ("old", "replacement")}
    assert_authorities(page, endpoints, commits)
    score.require(all(generation_key(page[side]["generation"]) == generation_key(initial[side]["generation"]) for side in endpoints), "quality page crossed its initial exact generation")


def check_citation(value, truth, repository, commit, object_id, blob_digest, expected_generation):
    source = value["source"]
    score.require(value["generation"]["state"] == "current", "citation generation is not current")
    score.require(generation_key(value["generation"]) == generation_key(expected_generation) and source.get("run_id") == expected_generation["generation_digest"], "citation crossed exact page generation/run")
    score.require(source["repository"] == repository and source["commit"] == commit, "citation crossed repository/revision")
    score.require(source["object_id"] == object_id and source["blob_digest"] == blob_digest, "citation crossed immutable blob")
    score.require(score.site_id(source) == truth["site_id"] and source["start_line"] == truth["start_line"] and source["end_line"] == truth["end_line"], "citation crossed exact range")
    score.require(value["content"] == truth["selector"], "citation bytes differ from frozen selector")


def source_accounting(directory, repository, binding):
    expected = {f["path"]: "sha256:" + f["sha256"] for f in binding["files"] if f["path"].endswith(".go")}
    observations = []
    for path in sorted(Path(directory).glob("*.jsonl")):
        score.require(path.stat().st_size <= 1048576, "source accounting file exceeds finite fixture bound")
        lines = [json.loads(line) for line in path.read_text().splitlines()]
        header = lines[0]
        if header["repository"] != repository or header["commit"] != binding["derived_input_commit"]:
            continue
        rows = [row for row in lines[1:] if "path" in row]
        for row in rows:
            score.require(row["path"] in expected and row["blob_digest"] == expected[row["path"]], "source processing identity mismatch")
            score.require(row["state"] == "analyzed" and row["reason"] == "source_scan_complete", "partial/failed public source processing")
        if rows:
            observations.append({"run_id": header["run_id"], "plan_digest": header["plan_digest"], "rows": rows, "sha256": sha(path)})
    score.require(observations and all(len(o["rows"]) == 9 and {r["path"] for r in o["rows"]} == set(expected) for o in observations), "incomplete source processing census")
    return {"unique_files": 9, "complete_observations": observations, "quality_denominator": 9}


def resolver_unit_binding(data, repository, commit, generation, unit_file):
    """Inspect already-admitted production catalog; do not mint authority."""
    base = "phebs-resolver-catalog-" + hashlib.sha256(repository.encode()).hexdigest()
    directory = Path(data) / "resolver-catalogs"
    manifest = bounded_json(directory / (base + ".manifest.json"), 1048576)
    identity = manifest["identity"]
    score.require(identity["repository"] == repository and identity["commit"] == commit, "resolver catalog crossed source binding")
    score.require(manifest["authority_digest"] == generation["resolver_manifest_digest"], "API resolver authority differs from admitted catalog")
    packs = {p["name"]: p["version"] for p in identity["resolver_packs"]}
    score.require(packs == {"go-module": "1.2.0", "grpc-generated-attribution": "1.2.0"}, "unit-less or unrelated resolver recipe")
    members = [m for m in manifest["members"] if m["name"] == "go-unit-attribution-v1.ndjson"]
    score.require(len(members) == 1, "mandatory unit member missing or duplicated")
    policy = members[0]["metadata"]["policy"]
    for key, value in {"consumer_unit_record_schema": "phebs-resolver-consumer-unit-v1", "consumer_unit_scope": "selected-caller-overlay-path-oid-v1", "max_consumer_unit_snapshot_bytes": 4194304, "max_consumer_unit_mappings": 25000, "max_consumer_unit_identity_bytes": 16777216, "max_consumer_unit_member_bytes": 16777216}.items():
        score.require(policy[key] == value, "unit materialization policy differs")
    name = base + "-v1-" + identity["generation_digest"].removeprefix("sha256:") + "-" + members[0]["name"]
    path = directory / name
    score.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 16777216, "unit member unavailable or oversized")
    rows = [json.loads(line) for line in path.read_text().splitlines()]
    status = rows[0]
    score.require(status["kind"] == "consumer_unit_input" and status["state"] == "resolved" and not status.get("reason"), "unit snapshot not fully admitted")
    score.require(status["input_path"] == "unit-snapshot.json" and status["input_object_id"] == unit_file["git_blob"] and status["input_digest"] == "sha256:" + unit_file["sha256"], "unit snapshot source differs")
    score.require(status["mapping_total"] == 4 and status.get("excluded_mappings", 0) == 0 and len(rows) == 5, "unit projection census differs")
    return {"repository": repository, "commit": commit, "resolver_generation": identity["generation_digest"], "resolver_authority": manifest["authority_digest"], "pack_set_digest": identity["resolver_pack_set_digest"], "unit_member_digest": members[0]["content_digest"], "unit_input": status, "materialization_policy": policy, "catalog_identity": identity}


def caller_binding(data, repository, commit, generation):
    """Record production-admitted exact metadata, without replaying source/rows."""
    directory = Path(data) / "caller-leaves" / ("phebs-caller-overlay-" + hashlib.sha256(repository.encode()).hexdigest())
    name = "complete-v1-" + generation["generation_digest"].removeprefix("sha256:") + "-" + generation["manifest_digest"].removeprefix("sha256:") + ".manifest.json"
    manifest = bounded_json(directory / name, 1048576)
    identity = manifest["generation"]
    score.require(manifest["schema"] == "phebs-caller-generation-publication-manifest-v1" and manifest["digest"] == generation["manifest_digest"] and manifest["pair_set_digest"] == generation["pair_set_digest"], "caller manifest differs from current API binding")
    score.require(identity["repository"] == repository and identity["head_commit"] == commit and identity["digest"] == generation["generation_digest"], "caller manifest crossed repository/source generation")
    score.require(identity["candidate_manifest_digest"] == generation["candidate_manifest_digest"] and identity["resolver_manifest_digest"] == generation["resolver_manifest_digest"], "caller manifest crossed admitted candidate/resolver authority")
    score.require({row["domain"]: row["version"] for row in identity["extractors"]} == {"grpc-caller": "1.6.0-released.1"}, "caller recipe differs from frozen qualification")
    return {"generation_identity": identity, "manifest_digest": manifest["digest"], "pair_set_digest": manifest["pair_set_digest"], "aggregate": manifest["aggregate"], "manifest_bytes_digest": sha(directory / name), "publication_revision": generation["publication_revision"]}


def cross_unit_namespace(page):
    score.require(page["total_rows"] == 5 and len(page["rows"]) == 5, "cross-repository unit probe incomplete")
    score.require(Counter(r["classification"] for r in page["rows"]) == {"old_only_evidence": 2, "new_only_evidence": 2, "unresolved": 1}, "cross-repository units were joined or lost")
    score.require(all(not (r["old"]["occurrence_count"] and r["replacement"]["occurrence_count"]) for r in page["rows"]), "unit candidate ID crossed repository namespace")
