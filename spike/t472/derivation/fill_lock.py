#!/usr/bin/env python3
"""Update admitted input measurements while preserving option A and refusals.

This records derivation only. It creates no labels, prediction, score or seal.
The earlier admitted measurements and recipe remain beside the corrected run.
"""
import copy
import json
import os
from pathlib import Path

HERE = Path(__file__).resolve().parent
DER = Path(os.environ.get("T472_ROOT", "/home/ben/phebs-rehearsals/t472-derivation"))
LOCK = HERE.parent / "corpus.lock.json"


def update_lock(lock, facts, receipt):
    out = copy.deepcopy(lock)
    admitted = [r for r in out["repos"] if r["derived"]["corpus_admitted"]]
    if set(facts) != {r["name"].split("/")[1] for r in admitted}:
        raise ValueError("facts must cover exactly the admitted repositories")
    for repo in admitted:
        short = repo["name"].split("/")[1]
        f = facts[short]
        if f["pin"] != repo["commit"] or f["derived_parents"] != [repo["commit"]]:
            raise ValueError(short + ": wrong pin or derived parents")
        if set(f["files"]) != {"index.scip", "layout-snapshot.json", "generated-from-snapshot.json"}:
            raise ValueError(short + ": wrong derived file set")
        if f["files"]["index.scip"]["bytes"] > 64 << 20:
            raise ValueError(short + ": index exceeds frozen bound")
        s = f["snapshots"]
        if s["regular_files"] + 3 > 200_000 or s["max_blob_bytes"] > 10 << 20:
            raise ValueError(short + ": source exceeds frozen bound")
        old = repo["derived"]
        previous = old.get("previous_derived", copy.deepcopy(old))
        old.update({
            "derived_utc": receipt["executed_utc"],
            "derived_commit": f["derived_commit"],
            "derived_tree": f["derived_tree"],
            "parent_commits": f["derived_parents"],
            "files": f["files"],
        })
        m = f["merge"]
        old["index"] = {
            "documents": m["docs"], "occurrences": m["occurrences"], "symbols": m["symbols"],
            "out_of_tree_build_cache_documents_dropped": m["out_of_tree_dropped"],
            "out_of_tree_testmain_documents": m["out_of_tree_testmain"],
            "out_of_tree_other_documents": m["out_of_tree_other"],
            "version_skew_references": m["version_skew_references"],
            "version_skew_sample": m["version_skew_sample"],
            "external_symbols_dropped": m["external_symbols_dropped"],
            "round_trip_unstable_documents": m["round_trip_unstable_docs"],
            "bytes": m["bytes"], "module_runs": m["runs"], "module_rels": m["module_rels"],
        }
        old["snapshots"].update({
            key: s[key] for key in ("clients", "mapped", "abstained", "mappings", "regular_files",
                                   "gitlinks", "symlinks", "max_blob_path", "max_blob_bytes",
                                   "vendored_mapped", "nonvendored_mapped", "mapping_table_sha256")
        })
        old["snapshots"].update(layout_roots=s["roots"], layout_roots_deduped=s["roots_deduped"],
                                gates_passed=s["gates"])
        old["bounds"]["corpus_files"].update(measured=s["regular_files"] + 3, within=True)
        old["bounds"]["max_blob_bytes"].update(measured=s["max_blob_bytes"], within=True,
                                             over_bound=s["over_blob_bound"] or [])
        old["bounds"]["index_scip_bytes"].update(measured=m["bytes"], within=True)
        old["previous_derived"] = previous
    der = out["derivation"]
    der["corrected_run"] = receipt
    der["admitted_totals"] = {
        "repositories": len(admitted),
        "module_runs": sum(r["derived"]["index"]["module_runs"] for r in admitted),
        "generated_clients": sum(r["derived"]["snapshots"]["clients"] for r in admitted),
        "mapped_declarations": sum(r["derived"]["snapshots"]["mapped"] for r in admitted),
        "abstentions": sum(r["derived"]["snapshots"]["abstained"] for r in admitted),
        "version_skew_references": sum(r["derived"]["index"]["version_skew_references"] for r in admitted),
        "out_of_tree_testmain_documents": sum(r["derived"]["index"]["out_of_tree_testmain_documents"] for r in admitted),
        "out_of_tree_other_documents": sum(len(r["derived"]["index"]["out_of_tree_other_documents"]) for r in admitted),
    }
    der["rederivation_required"] = "corrected inputs recorded; cross-module binding decision and independent review remain before sealing"
    return out


def main():
    out = update_lock(json.loads(LOCK.read_text()),
                      json.loads((DER / "derived_facts.json").read_text()),
                      json.loads((DER / "run-receipt.json").read_text()))
    temp = LOCK.with_suffix(".json.tmp")
    temp.write_text(json.dumps(out, indent=2, ensure_ascii=False) + "\n")
    temp.replace(LOCK)
    print("recorded corrected admitted inputs; original disposition and refusals preserved")


if __name__ == "__main__":
    main()
