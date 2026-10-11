"""Read-only convergence controls and explicit processing receipts; no predictions."""
from __future__ import annotations

import argparse
import base64
import datetime
import hashlib
import json
import re
from pathlib import Path
from urllib.request import Request, urlopen

import caller_scoring

DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
REASONS = {
    "source_scan_complete": {"analyzed"},
    "source_parse_failed": {"failed", "partial"},
    "source_byte_limit": {"excluded", "partial"},
    "source_invalid_utf8": {"excluded", "partial"},
    "no_generated_methods": {"excluded", "partial"},
    "typed_input_invalid": {"partial"},
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def load(path):
    require(path.stat().st_size <= 8 << 20, "control exceeds read bound")
    return json.loads(path.read_text())


def sha(path):
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def convergence(run_root: Path, source_commit: str, universe: dict) -> dict:
    require(re.fullmatch(r"[0-9a-f]{40}", source_commit), "invalid implementation commit")
    runtime = load(run_root / "data/.surreal-runtime.json")
    # Secrets stay inside the request; never persist runtime state or raw logs.
    tables = ("generation_schedule_chunk", "caller_leaf_job")
    queries = ["SELECT status, partition_sealed, partition_active, partition_plan_digest, partition_root_digest, partition_candidate_digest, run_id, repo, commit, domain FROM extraction_run LIMIT 100;",
               "SELECT repository, domain, run_id, root_digest, plan_digest, candidate_digest, source_digest, observation_digest, facts, rows, references, root, plan FROM extraction_domain_root LIMIT 100;"]
    queries += [f"SELECT status, count() AS n FROM {table} GROUP BY status;" for table in tables]
    queries += [f"SELECT count() AS n FROM {table} GROUP ALL;" for table in
                ("caller_generation_publication", "resolver_catalog_publication", "extraction_domain_outcome")]
    queries += ["SELECT repository, generation, upstream, manifest_digest, pair_count FROM caller_generation_publication LIMIT 10;",
                "SELECT repository, head_commit, candidate_manifest_digest, generation_digest, manifest_digest, authority_digest FROM resolver_catalog_publication LIMIT 10;"]
    request = Request(runtime["endpoint"].replace("ws://", "http://") + "/sql",
                      data="\n".join(queries).encode(), method="POST", headers={
                          "Authorization": "Basic " + base64.b64encode(("root:" + runtime["pass"]).encode()).decode(),
                          "surreal-ns": "phebs", "surreal-db": "phebs", "Accept": "application/json",
                          "Content-Type": "text/plain"})
    with urlopen(request, timeout=20) as response:
        raw = response.read((8 << 20) + 1)
    require(len(raw) <= 8 << 20, "store controls exceed read bound")
    results = json.loads(raw)
    require(len(results) == len(queries) and all(r["status"] == "OK" for r in results), "store control query failed")
    runs, domains = results[0]["result"], results[1]["result"]
    require(len(runs) == len(domains) == 12, "incomplete domain census")
    runs = {r["run_id"]: r for r in runs}
    clone_names = {"etcd-io/etcd": "etcd", "grpc/grpc-go": "grpc-go", "containerd/containerd": "containerd"}
    repositories = {"local" + str(run_root.resolve() / "corpus" / clone_names[r["repo"]]):
                    (r["repo"], r["derived_commit"]) for r in universe["repositories"]}
    controls = []
    seen = set()
    for row in sorted(domains, key=lambda d: (d["repository"], d["domain"])):
        root, plan = json.loads(row["root"]), json.loads(row["plan"])
        run = runs[row["run_id"]]
        key = (row["repository"], row["domain"])
        require(key not in seen and row["repository"] in repositories, "duplicate or unknown domain")
        seen.add(key)
        require(root["disposition"] == "success" and root["digest"] == row["root_digest"] == run["partition_root_digest"], "domain did not publish successfully")
        require(root["plan_digest"] == plan["digest"] == row["plan_digest"] == run["partition_plan_digest"], "plan mismatch")
        require(plan["repository"] == root["repository"] == run["repo"] == row["repository"] and
                plan["domain"] == root["domain"] == run["domain"] == row["domain"], "domain identity mismatch")
        require(plan["candidate_manifest_digest"] == row["candidate_digest"] == run["partition_candidate_digest"] and
                plan["source_generation_digest"] == row["source_digest"] and
                plan["observation_generation_digest"] == row["observation_digest"], "upstream binding mismatch")
        require(root["result_count"] == root["expected_results"] == len(root["results"]) == len(plan["expected"]), "incomplete results")
        require(all(r["disposition"] in {"success", "empty"} and r["plan_digest"] == plan["digest"] for r in root["results"]), "unusable partition")
        require(all(root["totals"][name] == row[name] for name in ("facts", "rows", "references")), "totals mismatch")
        require(run["commit"] == repositories[row["repository"]][1], "source commit mismatch")
        require(run["status"] == "staged" and run["partition_sealed"] and not run["partition_active"], "unsealed run")
        controls.append({"repository": row["repository"], "census_repository": repositories[row["repository"]][0], "commit": repositories[row["repository"]][1],
                         "domain": row["domain"], "run_id": row["run_id"], "plan": plan, "root": root})
    pointers = list((run_root / "data/extraction-publications").glob("*/current-*.json"))
    require(len(pointers) == 12, "incomplete current-pointer census")
    pointed = set()
    for path in pointers:
        pointer = load(path)
        require(DIGEST.fullmatch(pointer["generation_digest"]) and re.fullmatch(r"[0-9a-f]{64}/root[.]json", pointer["root_name"]), "invalid pointer")
        root = load(path.parent / pointer["generation_digest"][7:] / pointer["root_name"])
        match = [c for c in controls if c["root"]["digest"] == pointer["root_digest"] and c["plan"]["digest"] == pointer["plan_digest"]]
        require(len(match) == 1 and root == match[0]["root"] and root["digest"] not in pointed, "filesystem/store authority mismatch")
        pointed.add(root["digest"])
    counts = {table: {r["status"]: r["n"] for r in results[index+2]["result"]} for index, table in enumerate(tables)}
    require(counts == {"generation_schedule_chunk": {"done": 33}, "caller_leaf_job": {"done": 18}}, "work has not converged")
    require(results[4]["result"] == [{"n": 3}] and results[5]["result"] == [{"n": 3}], "publication census incomplete")
    callers, resolvers = results[7]["result"], results[8]["result"]
    require(len(callers) == len(resolvers) == 3 and {c["repository"] for c in callers} == {r["repository"] for r in resolvers} == set(repositories), "downstream repository census mismatch")
    for caller in callers:
        generation = caller["generation"]
        repository = caller["repository"]
        resolver = next(r for r in resolvers if r["repository"] == repository)
        domain = next(c for c in controls if c["repository"] == repository and c["domain"] == "grpc-caller")
        require(generation["head_commit"] == resolver["head_commit"] == domain["commit"] and
                generation["candidate_manifest_digest"] == resolver["candidate_manifest_digest"] == domain["plan"]["candidate_manifest_digest"] and
                generation["resolver_generation_digest"] == resolver["generation_digest"] and
                generation["resolver_manifest_digest"] == resolver["authority_digest"], "stale downstream publication")
        upstream = json.loads(caller["upstream"])
        require(upstream["repository"] == generation["repository"] == repository and
                upstream["digest"] == generation["upstream_digest"], "caller upstream binding mismatch")
        for authority in upstream["domains"]:
            bound = next(c for c in controls if c["repository"] == repository and c["domain"] == authority["domain"])
            require(authority["root_digest"] == bound["root"]["digest"] and authority["plan_digest"] == bound["plan"]["digest"], "stale caller upstream")
    return {"schema": "t472-convergence-v1", "observed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "run_root": run_root.name, "source_commit": source_commit,
            "run_identity_sha256": sha(run_root / "RUN_IDENTITY.md"), "config_sha256": sha(run_root / "phebs.yaml"),
            "result": "converged", "acceptance_met": False, "queues": counts,
            "current_domain_roots": 12, "caller_publications": 3, "resolver_publications": 3,
            "legacy_domain_outcome_rows": sum(row["n"] for row in results[6]["result"]),
            "extraction_run_state": "staged, sealed, inactive (partition publication contract)",
            "controls": controls, "caller_publication_controls": callers, "resolver_publication_controls": resolvers,
            "processing_outcomes": {"state": "not_checked", "required_regular_sources": 7685,
                                    "nonregular_go_paths": 11},
            "boundaries": "No predictions, labels, quality scores or seal; custody unchanged."}


def nonregular_ledger(universe: dict) -> dict:
    rows = []
    for repo in universe["repositories"]:
        inventory = repo["nonregular"]
        require(set(inventory["paths"]) == set(inventory["git_modes"]) == set(inventory["link_targets"]), "nonregular inventory mismatch")
        for path in inventory["paths"]:
            require(inventory["git_modes"][path] == "120000", "unexpected nonregular mode")
            rows.append({"repository": repo["repo"], "commit": repo["derived_commit"], "path": path,
                         "git_mode": "120000", "link_target": inventory["link_targets"][path],
                         "state": "outside_regular_source_census"})
    require(len(rows) == 11, "nonregular census mismatch")
    return {"count": len(rows), "records": rows, "treatment": "source-only census inventoried symlinks; never inferred as analyzed"}


def reconcile(receipt: dict, capture_root: Path, universe: dict) -> dict:
    require(receipt.get("schema") == "t472-convergence-v1" and receipt.get("result") == "converged", "requires convergence controls")
    records, files = [], []
    commits = {r["repo"]: r["derived_commit"] for r in universe["repositories"]}
    controls = [c for c in receipt["controls"] if c["domain"] == "grpc-caller"]
    require(len(controls) == len(commits) and {c["census_repository"] for c in controls} == set(commits), "caller domain census incomplete")
    for control in controls:
        require(control["commit"] == commits[control["census_repository"]] and control["plan"]["extractor_version"] == "1.6.0" and
                control["root"]["disposition"] == "success" and control["root"]["plan_digest"] == control["plan"]["digest"], "caller binding mismatch")
        for expected in control["root"]["results"]:
            require(DIGEST.fullmatch(expected["digest"]), "invalid result digest")
            path = capture_root / (expected["digest"][7:] + ".jsonl")
            require(path.is_file(), "successful partition lacks source outcome receipt")
            # Receipt size is bounded by this round's fixed 7,685-source census.
            require(path.stat().st_size <= 8 << 20, "source outcomes exceed read bound")
            lines = [json.loads(line) for line in path.read_text().splitlines()]
            require(len(lines) >= 2 and len(lines) <= 7687 and lines[-1] == {"result": expected}, "incomplete or stale partition receipt")
            header = lines[0]
            require(header == {"schema": "t472-source-outcomes-v1", "repository": control["repository"],
                               "commit": control["commit"], "run_id": control["run_id"],
                               "plan_digest": control["plan"]["digest"], "partition_ordinal": expected["partition_ordinal"]}, "source outcome identity mismatch")
            for row in lines[1:-1]:
                require(set(row) == {"path", "state", "reason", "blob_digest"} and
                        row["state"] in REASONS.get(row["reason"], set()) and DIGEST.fullmatch(row["blob_digest"]), "invalid source processing decision")
                records.append({"repository": control["census_repository"], "commit": control["commit"],
                                "path": row["path"], "state": row["state"]})
            files.append({"name": path.name, "sha256": sha(path)})
    ledger = caller_scoring.eligible_unit_ledger(records, universe)
    require(ledger["total"] == 7685, "wrong source census")
    return {"schema": "t472-source-outcome-reconciliation-v1", "convergence_content_sha256": "sha256:" + hashlib.sha256(json.dumps(receipt,sort_keys=True).encode()).hexdigest(),
            "universe_content_sha256": "sha256:" + hashlib.sha256(json.dumps(universe,sort_keys=True).encode()).hexdigest(),
            "digest_encoding": "json.dumps(sort_keys=True), UTF-8; canonical logical content",
            "receipt_files": files, "records": records, "ledger": ledger,
            "nonregular_go_paths": nonregular_ledger(universe),
            "acceptance_met": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    record = sub.add_parser("record")
    record.add_argument("--run-root", type=Path, required=True)
    record.add_argument("--source-commit", required=True)
    join = sub.add_parser("reconcile")
    join.add_argument("--convergence", type=Path, required=True)
    join.add_argument("--capture-root", type=Path, required=True)
    parser.add_argument("--universe", type=Path, default=Path(__file__).parent / "universe/universe.json")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    universe = load(args.universe)
    result = convergence(args.run_root, args.source_commit, universe) if args.command == "record" else reconcile(load(args.convergence), args.capture_root, universe)
    # Exclusive creation preserves earlier receipts and never writes run custody.
    with args.output.open("x") as stream:
        json.dump(result, stream, sort_keys=True, indent=2)
        stream.write("\n")


if __name__ == "__main__":
    main()
