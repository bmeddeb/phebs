#!/usr/bin/env python3
"""Verify frozen public bytes and supplied oracle coordinates, never predictions."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path


HERE = Path(__file__).resolve().parent


def digest(path):
    return "sha256:" + hashlib.sha256(Path(path).read_bytes()).hexdigest()


def require(value, reason):
    if not value:
        raise ValueError(reason)


def verify(binding, oracle, root):
    root = Path(root)
    files = {row["path"]: row for row in binding["evaluation"]["files"]}
    require(len(files) == 19, "evaluation census changed")
    for name, expected in files.items():
        path = root / name
        require(path.is_file() and not path.is_symlink(), "missing/linked public input")
        raw = path.read_bytes()
        require(len(raw) == expected["bytes"] and digest(path) == "sha256:" + expected["sha256"], "public/input bytes changed: " + name)
        object_id = hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest()
        require(object_id == expected["git_blob"], "Git blob identity changed: " + name)
    require(digest(root / "LICENSE") == binding["license"]["sha256"], "license binding changed")
    sites = {site["site_id"]: site for site in oracle["sites"]}
    require(len(sites) == 9, "source site census changed")
    for site in sites.values():
        raw = (root / site["path"]).read_bytes()
        require(raw[site["start_byte"]:site["end_byte"]].decode() == site["selector"], "source selector differs")
        require(raw[:site["start_byte"]].count(b"\n") + 1 == site["start_line"], "source line differs")
        require(site["site_id"] == f'{site["path"]}:{site["start_byte"]}-{site["end_byte"]}', "source tuple differs")
    declarations = {row["operation"]: row for row in oracle["declarations"]}
    require(len(declarations) == 5, "declaration census changed")
    for operation, item in declarations.items():
        source = item["declaration"]
        raw = (root / source["path"]).read_bytes()
        require(raw[source["start_byte"]:source["end_byte"]].decode() == source["statement"], "RPC statement differs")
        require(source["git_blob"] == files[source["path"]]["git_blob"] and source["blob_digest"] == "sha256:" + files[source["path"]]["sha256"], "RPC source binding differs")
        require('"' + operation + '"' in (root / item["generated_path"]).read_text(), "generated wire literal differs")
        lineage = "provisional_repo_path_v1_" + hashlib.sha256((binding["evaluation"]["repository"] + "\0" + source["path"]).encode()).hexdigest()
        require(item["endpoint"]["declaration_lineage"] == lineage, "declaration lineage differs")
    positives, negatives, empty = 0, 0, 0
    for query in oracle["queries"]:
        endpoint = query["query"]["endpoint"]
        require(endpoint == declarations[endpoint["operation"]]["endpoint"], "query endpoint crossed source identity")
        prefix = query["query"].get("path_prefix", "")
        expected = {key for key, site in sites.items() if site["wire_operation"] == endpoint["operation"] and site["path"].startswith(prefix)}
        require(set(query["expected_site_ids"]) == expected and query["expected_total_matching_rows"] == len(expected), "query source membership differs")
        require(set(query["excluded_site_ids"]) == set(sites) - expected, "negative source census differs")
        if prefix:
            require(not expected and query["expected_conclusion_state"] == "exact_empty", "filtered-empty scope changed")
            empty += 1
        else:
            require(query["expected_conclusion_state"] == "evidence_present", "source-backed conclusion label differs")
            positives += len(expected)
            negatives += len(sites) - len(expected)
    require((positives, negatives, empty) == (9, 36, 2), "composition denominators changed")
    require(Counter(site["unit"]["state"] for site in sites.values()) == {"resolved": 5, "unavailable": 4}, "supplied unit uncertainty census changed")
    return {"schema": "phebs-t541-source-check-v1", "date": "2026-10-18", "outcome": "frozen_source_coordinates_verified_no_predictions", "files": 19, "rpc_declarations": 5, "source_sites": 9, "query_memberships": {"positive": 9, "negative": 36, "filtered_empty_queries": 2}, "report_predictions_read": 0, "native_runs": 0, "qualification": "Source-coordinate/internal-census verification only. Independent review, actual composed quality and operating measurement remain pending."}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    require(not args.out.exists(), "source-check receipt already exists")
    binding = json.loads((HERE / "input-binding.json").read_text())
    oracle = json.loads((HERE / "oracle.json").read_text())
    require(digest(HERE / "base.bundle") == binding["evaluation"]["bundle"]["sha256"], "frozen input bundle changed")
    result = verify(binding, oracle, args.source_root)
    args.out.write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
