"""Compare returned production rows with frozen source truth; never classify."""

from collections import Counter
import math
import hashlib


class Mismatch(ValueError):
    pass


def require(value, message):
    if not value:
        raise Mismatch(message)


def site_id(source):
    return f"{source['path']}:{source['start_byte']}-{source['end_byte']}"


def row_signature(row):
    return (
        row["classification"],
        tuple(sorted(site_id(item["source"]) for item in row["old"]["rows"])),
        tuple(sorted(site_id(item["source"]) for item in row["replacement"]["rows"])),
        (row.get("unit") or {}).get("id") if row["level"] == "unit" else None,
    )


def expected_signatures(labels, level):
    sites = {row["site_id"]: row for row in labels["sites"]}
    result = []
    for row in labels[f"expected_{level}_rows"]:
        result.append((
            row["classification"],
            tuple(sorted(key for key in row["site_ids"] if sites[key]["pair_side"] == "old")),
            tuple(sorted(key for key in row["site_ids"] if sites[key]["pair_side"] == "replacement")),
            row.get("unit_id") if level == "unit" else None,
        ))
    return Counter(result)


def check_rows(pages, labels, binding, level, repository):
    """Validate exact public-source memberships and supplied metadata."""
    expected_sites = {row["site_id"]: row for row in labels["sites"] if row["pair_side"] != "outside_pair"}
    files = {row["path"]: row for row in binding["files"]}
    keys, rows, citations = set(), [], {}
    for page in pages:
        require(page["schema_version"] == "caller-comparison-v2", "wrong comparison schema")
        require(page["matching_rows_state"] == "exact", "comparison is not exact")
        require(page["query"]["level"] == level, "wrong echoed comparison level")
        require(page["total_rows"] == len(labels[f"expected_{level}_rows"]), "wrong complete comparison total")
        for side in ("old", "replacement"):
            snapshot = page[side]
            truth_endpoint = labels["endpoints"][side]
            expected_lineage = "provisional_repo_path_v1_" + hashlib.sha256((repository + "\0" + truth_endpoint["declaration_path"]).encode()).hexdigest()
            for endpoint in (snapshot["endpoint"], page["query"][side]):
                require(endpoint["protocol"] == "protobuf" and endpoint["repository"] == repository, "wrong echoed endpoint protocol/repository")
                require(endpoint["operation"] == truth_endpoint["wire_operation"] and endpoint["declaration_lineage"] == expected_lineage, "wrong echoed source declaration/operation")
            require(snapshot["matching_rows_state"] == "exact", "endpoint is not exact")
            require(snapshot["generation"]["state"] == "current", "endpoint generation is not current")
            require(snapshot["generation"]["repository"] == repository, "crossed generation repository")
            require(snapshot["generation"]["commit"] == binding["derived_input_commit"], "crossed evaluation commit")
        for row in page["rows"]:
            require(row["key"] not in keys, "duplicate comparison row across pages")
            keys.add(row["key"])
            require(row["level"] == level, "wrong row level")
            for side in ("old", "replacement"):
                value = row[side]
                require(not value["rows_truncated"], "unexpected truncated side")
                require(value["occurrence_count"] == len(value["rows"]), "wrong side count")
                for item in value["rows"]:
                    source = item["source"]
                    key = site_id(source)
                    require(key in expected_sites, f"extra source occurrence: {key}")
                    truth = expected_sites[key]
                    require(truth["pair_side"] == side, f"crossed endpoint side: {key}")
                    require(item["operation"] == truth["wire_operation"], f"wrong wire operation: {key}")
                    require(item["protocol"] == "protobuf" and item["classification"] == "resolved_caller", f"unresolved or foreign caller: {key}")
                    require(truth["expected_resolution"] == "resolved" and item["resolution"] in {"scip", "syntax"} and item["fresh"] is True, f"unsupported or nonfresh resolved evidence: {key}")
                    require(item["tier"] == {"scip": "derived", "syntax": "heuristic"}[item["resolution"]] and not item.get("unresolved_reason"), f"resolution tier/reason was promoted or unresolved: {key}")
                    endpoint = page[side]["endpoint"]
                    require(item["declaration_lineage"] == endpoint["declaration_lineage"], f"wrong declaration lineage: {key}")
                    require(source["repository"] == repository and source["commit"] == binding["derived_input_commit"], f"wrong immutable source revision: {key}")
                    require(source["object_id"] == files[truth["path"]]["git_blob"], f"wrong source Git blob: {key}")
                    require(source["blob_digest"] == truth["upstream_blob_sha256"], f"wrong source digest: {key}")
                    require(source["start_line"] == truth["start_line"] and source["end_line"] == truth["end_line"], f"wrong source lines: {key}")
                    require(bool(source.get("citation")) and bool(source.get("run_id")) and bool(source.get("atom_id")), f"missing immutable source capability: {key}")
                    actual_unit, expected_unit = item["unit"], truth["unit"]
                    require(actual_unit["state"] == expected_unit["state"], f"wrong unit state: {key}")
                    if expected_unit["state"] == "resolved":
                        candidates = actual_unit["candidates"]
                        require(actual_unit["candidate_total"] == 1 and len(candidates) == 1 and candidates[0]["id"] == expected_unit["id"], f"wrong supplied unit ID: {key}")
                        require(candidates[0].get("build_targets", []) == expected_unit["build_targets"], f"wrong supplied package target: {key}")
                        require(not any(candidates[0].get(name) for name in ("deployables", "logical_services", "owners")), f"invented deployment attribution: {key}")
                    else:
                        require(actual_unit.get("reason") == expected_unit["reason"], f"wrong unavailable-unit reason: {key}")
                    citations[key] = source["citation"]
            rows.append(row)
    actual = Counter(row_signature(row) for row in rows)
    require(actual == expected_signatures(labels, level), f"{level} rows disagree with frozen class/membership/unit truth")
    require(set(citations) == set(expected_sites), "selected public site census is incomplete")
    return rows, citations


def wilson(successes, total):
    if not total:
        return None
    z = 1.959963984540054
    p = successes / total
    center = (p + z * z / (2 * total)) / (1 + z * z / total)
    half = z * math.sqrt(p * (1 - p) / total + z * z / (4 * total * total)) / (1 + z * z / total)
    return [max(0, center - half), min(1, center + half)]


def exact_metric(n):
    return {"numerator": n, "denominator": n, "observed_rate": 1.0 if n else None, "wilson95": wilson(n, n), "qualification": "Selected finite census only; interval is descriptive, not a confidence-bound pass."}
