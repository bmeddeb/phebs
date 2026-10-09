#!/usr/bin/env python3
"""Fill the T47.2b corpus lock's `derived` objects from the measured derivation.

Reads the verified facts emitted by commit_derived.py plus the retained refusal
measurements, and rewrites spike/t472/corpus.lock.json in place: the three
in-bound repositories get their committed §2 input digests, the two repositories
that missed a frozen §2 bound get an explicit refusal record instead of `null`,
and a top-level `derivation` section records the recipe, the tool pins, the
bounds and the open disposition. No bound is adjusted and no refusal is
silently dropped.
"""

import collections
import json
import os

DER = "/home/ben/phebs-rehearsals/t472-derivation"
LOCK = "/home/ben/.codex/worktrees/t47-2-caller-identity/phebs/spike/t472/corpus.lock.json"

BOUND_FILES = 200_000
BOUND_BLOB = 10 << 20
BOUND_INDEX = 64 << 20

IN_BOUND = ["etcd", "containerd", "grpc-go"]

REFUSALS = {
    "vitess": {
        "merged_index_bytes": 188191177,
        "merged_index_sha256": "ea5a71708e8b680bbea78b92e71c4a6e84d9130ac25be015667a9161df05ec86",
        "raw_root_index_bytes": 191695210,
        "raw_root_index_sha256": "4be9ea65d7cafd3455b8d1d5010990e30d2959f74fb18f25267a923f5a618ac5",
        "retained_evidence": "/home/ben/phebs-rehearsals/t472-derivation/merged/vitess/index.scip.tmp",
    },
    "istio": {
        "merged_index_bytes": 77222752,
        "merged_index_sha256": "bea6784bca8592358ef34f6eb054cd63f5588bf85777925f5fa6099c879c8616",
        "raw_root_index_bytes": 78943875,
        "raw_root_index_sha256": "1c25282b728fc84cc6702dff9920f9bf2306bb9946734adef9bb0a6c09bdfdd3",
        "retained_evidence": "/home/ben/phebs-rehearsals/t472-derivation/merged/istio/index.scip.tmp",
    },
}


def load(p):
    with open(p) as f:
        return json.load(f, object_pairs_hook=collections.OrderedDict)


def main():
    facts = load(os.path.join(DER, "derived_facts.json"))
    lock = load(LOCK)

    refusals = []
    all_snap = {}
    for repo in lock["repos"]:
        short = repo["name"].split("/")[1]
        snap = load(os.path.join(DER, "snapshots", "%s.stats.json" % short))
        all_snap[short] = snap
        mappings = load(os.path.join(DER, "mappings", "%s.json" % short))
        abstentions = [
            collections.OrderedDict([
                ("generated_path", a["generated_path"]),
                ("reason", a["reason"]),
                ("source_line", a.get("source_line", "")),
                ("vendored", a["vendored"]),
                ("generated_header", a["generated_header"]),
            ])
            for a in mappings["abstentions"]
        ]
        snapshot_record = collections.OrderedDict([
            ("clients", snap["clients"]),
            ("mapped", snap["mapped"]),
            ("abstained", snap["abstained"]),
            ("mappings", snap["mappings"]),
            ("layout_roots", snap["roots"]),
            ("layout_roots_deduped", snap["roots_deduped"]),
            ("regular_files", snap["regular_files"]),
            ("gitlinks", snap["gitlinks"]),
            ("symlinks", snap["symlinks"]),
            ("max_blob_path", snap["max_blob_path"]),
            ("max_blob_bytes", snap["max_blob_bytes"]),
            ("vendored_mapped", snap["vendored_mapped"]),
            ("nonvendored_mapped", snap["nonvendored_mapped"]),
            ("abstentions", abstentions),
            ("gates_passed", snap["gates"]),
        ])
        bounds = collections.OrderedDict([
            ("corpus_files", {"measured": snap["regular_files"], "max": BOUND_FILES, "within": snap["files_within_bound"]}),
            ("max_blob_bytes", {"measured": snap["max_blob_bytes"], "max": BOUND_BLOB, "within": snap["blobs_within_bound"],
                                "over_bound": snap["over_blob_bound"] or []}),
        ])

        if short in IN_BOUND:
            f = facts[short]
            if f["pin"] != repo["commit"]:
                raise SystemExit("%s: facts pin != lock commit" % short)
            index_bytes = f["files"]["index.scip"]["bytes"]
            derived = collections.OrderedDict([
                ("status", "committed"),
                ("derived_utc", "2026-10-09"),
                ("clone", "spike/t472/corpus/%s" % short),
                ("derived_commit", f["derived_commit"]),
                ("derived_tree", f["derived_tree"]),
                ("parent_commits", f["derived_parents"]),
                ("pushed", False),
                ("files", collections.OrderedDict([
                    (rel, collections.OrderedDict([
                        ("sha256", f["files"][rel]["sha256"]),
                        ("bytes", f["files"][rel]["bytes"]),
                        ("git_blob_sha1", f["files"][rel]["git_blob_sha1"]),
                    ])) for rel in ["index.scip", "layout-snapshot.json", "generated-from-snapshot.json"]
                ])),
                ("index", collections.OrderedDict([
                    ("documents", f["merge"]["docs"]),
                    ("occurrences", f["merge"]["occurrences"]),
                    ("symbols", f["merge"]["symbols"]),
                    ("out_of_tree_cgo_documents_dropped", f["merge"]["out_of_tree_dropped"]),
                    ("external_symbols_dropped", f["merge"]["external_symbols_dropped"]),
                    ("round_trip_unstable_documents", f["merge"]["round_trip_unstable_docs"]),
                    ("bytes", index_bytes),
                    ("module_runs", f["merge"]["runs"]),
                    ("module_rels", f["merge"]["module_rels"]),
                ])),
                ("snapshots", snapshot_record),
                ("bounds", collections.OrderedDict(list(bounds.items()) + [
                    ("index_scip_bytes", {"measured": index_bytes, "max": BOUND_INDEX, "within": index_bytes <= BOUND_INDEX}),
                ])),
            ])
            repo["derived"] = derived
            continue

        ref = REFUSALS[short]
        ratio = round(ref["merged_index_bytes"] / BOUND_INDEX, 2)
        missed = ["index_scip_bytes"]
        if not snap["blobs_within_bound"]:
            missed.append("max_blob_bytes")
        refusals.append(collections.OrderedDict([
            ("name", repo["name"]),
            ("missed_bounds", missed),
            ("index_scip_bytes", {"measured": ref["merged_index_bytes"], "max": BOUND_INDEX,
                                  "ratio": ratio, "sha256": ref["merged_index_sha256"]}),
            ("max_blob_bytes", {"measured": snap["max_blob_bytes"], "max": BOUND_BLOB,
                                "within": snap["blobs_within_bound"],
                                "over_bound": snap["over_blob_bound"] or []}),
            ("raw_root_index", {"bytes": ref["raw_root_index_bytes"], "sha256": ref["raw_root_index_sha256"]}),
            ("retained_evidence", ref["retained_evidence"]),
        ]))
        repo["derived"] = collections.OrderedDict([
            ("status", "refused_bound_miss"),
            ("refused_utc", "2026-10-09"),
            ("missed_bounds", missed),
            ("index_scip_bytes", {"measured": ref["merged_index_bytes"], "max": BOUND_INDEX,
                                  "ratio": ratio, "sha256": ref["merged_index_sha256"]}),
            ("max_blob_bytes", bounds["max_blob_bytes"]),
            ("raw_root_index", {"bytes": ref["raw_root_index_bytes"], "sha256": ref["raw_root_index_sha256"]}),
            ("merge_refusal", "scipmerge measured the merged stream over the frozen 67108864-byte bound and refused to place an over-bound artifact; the measured stream is retained unrenamed at %s" % ref["retained_evidence"]),
            ("snapshots_authored_unplaced", collections.OrderedDict([
                ("layout_snapshot", {"sha256": snap["layout_sha256"], "bytes": snap["layout_bytes"]}),
                ("generated_from_snapshot", {"sha256": snap["generated_from_sha256"], "bytes": snap["generated_from_bytes"]}),
                ("detail", snapshot_record),
                ("note", "both snapshots were authored and passed every production gate, but §2 admits a repository only as a whole tuple; with no admissible index.scip nothing was placed in the clone and no derived commit exists"),
            ])),
            ("clone_state", "spike/t472/corpus/%s remains at the pinned commit %s with a clean working tree and no derived commit" % (short, repo["commit"])),
            ("disposition", "open — recorded as a §2 selection change awaiting Ben's routing; see derivation.disposition"),
        ])

    derivation = collections.OrderedDict([
        ("derived_utc", "2026-10-09"),
        ("status", "partial: 3 of 5 selected repositories carry committed §2 inputs; vitessio/vitess and istio/istio were refused at the frozen bounds and their disposition is open"),
        ("bounds", collections.OrderedDict([
            ("source", "docs/CALLER_QUALITY_PROTOCOL.md §2, frozen by T47.2a; the bounds were enforced as written and are not adjusted here"),
            ("corpus_files_max", BOUND_FILES),
            ("blob_bytes_max", BOUND_BLOB),
            ("index_scip_bytes_max", BOUND_INDEX),
        ])),
        ("recipe", collections.OrderedDict([
            ("index_command", "GOTOOLCHAIN=go1.27.1 scip-go index --module-root <abs module dir> --repository-remote github.com/<owner>/<repo> --module-version <pin12> --skip-implementations --output <abs path>, one run per Go module in run-plan order"),
            ("gotoolchain", "go1.27.1"),
            ("scip_go", collections.OrderedDict([
                ("version", "v0.2.7"),
                ("sha256", "31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4"),
                ("bytes", 18104856),
                ("pin_source", "spike/t457/native_tool_pins_amd64.json"),
            ])),
            ("merge_tool", collections.OrderedDict([
                ("source", "spike/t472/derivation/merge"),
                ("sha256", "4c22c00f3ed7a17097da5fa32c55561703394a8f8bf80a2400c3c169e3324cce"),
                ("bytes", 6807487),
                ("role", "canonicalizes every run to sorted unique document paths under one tool version and canonical field order, drops out-of-tree cgo build-cache documents, then refuses a merged stream over the frozen bound"),
            ])),
            ("snapshot_tool", collections.OrderedDict([
                ("source", "spike/t472/derivation/snapshots"),
                ("sha256", "8bd8fd8c4924b4ab9839acd1632b98b8648796431f88895b6f24200c8d534a2f"),
                ("bytes", 4163718),
                ("role", "authors t20-layout-snapshot-v1 and t20-generated-from-v1 from the checked-in tree and validates both through the production decoders and limits in internal/resolverinput plus the §2 corpus bounds"),
            ])),
            ("runner", collections.OrderedDict([
                ("script", "spike/t472/derivation/run_scip.sh"),
                ("policy", "serial and fail-closed; preflight re-verifies the scip-go digest before any run"),
                ("module_runs", 27),
                ("ledger", "RUN_STATUS in the derivation workspace; one honest FAIL row for the containerd root module caused by a runner IFS bug that collapsed the empty module rel, corrected with an explicit '.' marker and re-run"),
            ])),
            ("derived_commit_policy", "the three §2 files are committed inside each clone on a local derived commit whose only parent is the pinned upstream commit; the clones are gitignored in phebs, nothing is pushed, and no upstream file is modified"),
            ("host", "Ubuntu Linux amd64, the rehearsal and ceremony host baseline recorded in AGENTS.md"),
        ])),
        ("totals", collections.OrderedDict([
            ("repositories_committed", len(IN_BOUND)),
            ("repositories_refused", len(refusals)),
            ("module_runs", 27),
            ("generated_clients", sum(all_snap[s]["clients"] for s in all_snap)),
            ("mapped_declarations", sum(all_snap[s]["mapped"] for s in all_snap)),
            ("abstentions", sum(all_snap[s]["abstained"] for s in all_snap)),
            ("abstention_reasons", collections.OrderedDict([("declaration_not_found", 14), ("no_source_line", 2)])),
        ])),
        ("refusals", refusals),
        ("disposition", collections.OrderedDict([
            ("status", "open — Ben's routing"),
            ("rule", "a §2 bound miss is a selection change recorded here, never a silent adjustment; the frozen bound is not raised after measurement because the §2 thresholds were preregistered in T47.2a and a different byte is a different protocol"),
            ("options", [
                collections.OrderedDict([
                    ("id", "A"),
                    ("option", "drop vitessio/vitess and istio/istio, leaving a three-repository corpus"),
                    ("consequence", "loses the largest production multi-module monorepo and the degenerate test/generated-only stratum; the §6 strata and the §3 denominators shrink accordingly and are re-recorded before sealing"),
                ]),
                collections.OrderedDict([
                    ("id", "B"),
                    ("option", "replace one or both through a fresh four-axis selection run"),
                    ("consequence", "each replacement must itself pass §2 at its own pin, including the 64 MiB index bound and the 10 MiB blob bound; the selection table and this lock are re-dated and the derivation re-run for the replacement"),
                ]),
                collections.OrderedDict([
                    ("id", "C"),
                    ("option", "amend the §2 bound"),
                    ("consequence", "rejected as a post-hoc preregistration violation unless Ben explicitly directs it, in which case it is recorded as a dated protocol amendment with its own digest and the pre-amendment measurements are preserved beside it"),
                ]),
            ]),
        ])),
    ])

    out = collections.OrderedDict()
    for key, value in lock.items():
        out[key] = value
        if key == "selected_utc":
            out["derived_utc"] = "2026-10-09"
    out["derivation"] = derivation

    with open(LOCK, "w") as f:
        json.dump(out, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print("wrote %s" % LOCK)
    for repo in out["repos"]:
        short = repo["name"].split("/")[1]
        print("  %-12s %s" % (short, repo["derived"]["status"]))


if __name__ == "__main__":
    main()
