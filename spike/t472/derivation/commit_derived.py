#!/usr/bin/env python3
"""Create the T47.2b derived local commits in the in-bound corpus clones.

For each repository this stages exactly three derived paths (index.scip,
layout-snapshot.json, generated-from-snapshot.json), commits them on top of the
frozen pin inside the clone's own git repository, verifies the commit contents,
and emits every measured fact to derived_facts.json for the phebs corpus lock.

Nothing here is pushed. The clones are detached at their pins, so each commit is
a local derived commit whose only parent is the pinned upstream commit. The
commit identity and both dates are fixed (the pin's committer date), so the
derived commit SHA is reproducible from the same three files.
"""

import hashlib
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DER = os.environ.get("T472_ROOT", "/home/ben/phebs-rehearsals/t472-derivation")
CORPUS = os.environ.get("CORPUS_DIR", os.path.normpath(os.path.join(HERE, "..", "corpus")))
LOCK = os.path.normpath(os.path.join(HERE, "..", "corpus.lock.json"))
DERIVED_IDENTITY = ("phebs t472 derivation", "t472-derivation@phebs.invalid")

SCIP_GO_SHA = "31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4"
SCIP_GO_BYTES = 18104856
GOTOOLCHAIN = "go1.27.1"
BOUND_INDEX_BYTES = 64 << 20
BOUND_BLOB_BYTES = 10 << 20
BOUND_FILES = 200_000

IN_BOUND = ["etcd", "containerd", "grpc-go"]
DERIVED_PATHS = ["index.scip", "layout-snapshot.json", "generated-from-snapshot.json"]


def run(cmd, cwd=None):
    p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    if p.returncode != 0:
        raise SystemExit("command failed rc=%d: %s\n%s\n%s" % (p.returncode, " ".join(cmd), p.stdout, p.stderr))
    return p.stdout


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def remotes():
    lock = json.load(open(LOCK))
    out = {}
    for repo in lock["repos"]:
        short = repo["name"].split("/")[1]
        out[short] = repo["name"]
    return out


def rels_for(repo):
    rels = []
    with open(os.path.join(DER, "scip", "run_plan.tsv")) as f:
        for line in f:
            parts = line.rstrip("\n").split("\t")
            if parts[0] == repo:
                rels.append("." if parts[1] == "." else parts[1])
    if not rels:
        raise SystemExit("no run plan rows for %s" % repo)
    return rels


def other_paths(merge):
    return sorted(p for run in merge["runs"] for p in run["out_of_tree_other"] or [])


def main():
    remote_of = remotes()
    merge_tool = os.path.join(DER, "tool", "bin", "scipmerge")
    snapshot_tool = os.path.join(DER, "tool", "bin", "t472snapshots")
    merge_sha, merge_bytes = sha256(merge_tool), os.path.getsize(merge_tool)
    snapshot_sha, snapshot_bytes = sha256(snapshot_tool), os.path.getsize(snapshot_tool)
    scip_tool = os.path.join(DER, "tool", "bin", "scip-go")
    if sha256(scip_tool) != SCIP_GO_SHA or os.path.getsize(scip_tool) != SCIP_GO_BYTES:
        raise SystemExit("scip-go no longer matches the frozen tool pin")
    facts = {}
    for repo in IN_BOUND:
        clone = os.path.join(CORPUS, repo)
        merge = json.load(open(os.path.join(DER, "merged", "%s.merge.json" % repo)))
        snap = json.load(open(os.path.join(DER, "snapshots", "%s.stats.json" % repo)))
        remote = "github.com/" + remote_of[repo]
        pin = merge["pin"]
        if snap["commit"] != pin:
            raise SystemExit("%s: snapshot commit %s != merge pin %s" % (repo, snap["commit"], pin))
        table = os.path.join(HERE, "mappings", repo + ".json")
        if sha256(table) != snap["mapping_table_sha256"]:
            raise SystemExit("%s: committed resolution table no longer matches snapshot stats" % repo)
        head = run(["git", "rev-parse", "HEAD"], cwd=clone).strip()
        if head != pin:
            raise SystemExit("%s: clone HEAD %s != pin %s" % (repo, head, pin))
        status = run(["git", "status", "--porcelain", "--"] + DERIVED_PATHS, cwd=clone)
        untracked = [l for l in status.splitlines() if l.strip()]
        if any(not l.startswith("?? ") for l in untracked):
            raise SystemExit("%s: unexpected non-untracked status:\n%s" % (repo, status))

        rels = rels_for(repo)
        files = {}
        for rel in DERIVED_PATHS:
            path = os.path.join(clone, rel)
            files[rel] = {"sha256": sha256(path), "bytes": os.path.getsize(path)}

        if files["index.scip"]["bytes"] != merge["bytes"]:
            raise SystemExit("%s: placed index.scip bytes != merge report bytes" % repo)
        if files["index.scip"]["sha256"] != sha256(os.path.join(DER, "merged", repo, "index.scip")):
            raise SystemExit("%s: placed index.scip digest != merged stream digest" % repo)
        if files["layout-snapshot.json"]["sha256"] != snap["layout_sha256"]:
            raise SystemExit("%s: placed layout digest != snapshot stats digest" % repo)
        if files["generated-from-snapshot.json"]["sha256"] != snap["generated_from_sha256"]:
            raise SystemExit("%s: placed generated-from digest != snapshot stats digest" % repo)
        if files["index.scip"]["bytes"] > BOUND_INDEX_BYTES:
            raise SystemExit("%s: index.scip over the frozen 64 MiB bound" % repo)
        if snap["max_blob_bytes"] > BOUND_BLOB_BYTES:
            raise SystemExit("%s: over-bound blob %s" % (repo, snap["max_blob_path"]))
        if snap["regular_files"] + len(DERIVED_PATHS) > BOUND_FILES:
            raise SystemExit("%s: corpus over the frozen file bound" % repo)

        rels_text = ", ".join("`%s`" % ("." if r == "." else r) for r in rels)
        message = """t47.2b derived caller-quality inputs at %s

Local derived commit for the phebs T47.2b caller-quality corpus
(docs/CALLER_QUALITY_PROTOCOL.md \u00a72). It is not pushed and is not part of the
upstream repository; its only parent is the pinned commit %s. No upstream
file is modified.

index.scip \u2014 root merged SCIP index, %d bytes, sha256 %s, %d documents,
%d occurrences, %d symbols, %d out-of-tree build-cache documents dropped (%d
synthesized test mains, %d other), %d external symbols dropped, %d
round-trip-unstable documents, %d version-skewed in-repo references. Built with
scip-go v0.2.7 (sha256 %s, %d bytes) under GOTOOLCHAIN=%s, one run per Go
module with --skip-implementations, --repository-remote %s and
--module-version %s, over module roots: %s. The runs were canonicalized and
merged by the T47.2b scipmerge tool (sha256 %s, %d bytes), which enforces
sorted unique document paths, one tool version, canonical field order and the
frozen 64 MiB output bound.

layout-snapshot.json \u2014 t20-layout-snapshot-v1, %d bytes, sha256 %s, %d roots
over %d regular corpus files.
generated-from-snapshot.json \u2014 t20-generated-from-v1, %d bytes, sha256 %s,
%d generated clients, %d mapped, %d abstained.

Both snapshots were authored from the resolution table (sha256 %s) by the
T47.2b snapshot tool (sha256 %s, %d bytes), decoded by the production
internal/resolverinput decoders and limits and checked against re-implemented
production refusal conditions, plus the corpus bounds measured at the pin
(\u2264 %d files, blob \u2264 10 MiB; largest blob %s at %d bytes).

Digests, counts and the recipe are locked in phebs
spike/t472/corpus.lock.json.
""" % (
            pin[:12], pin,
            merge["bytes"], files["index.scip"]["sha256"], merge["docs"],
            merge["occurrences"], merge["symbols"], merge["out_of_tree_dropped"],
            merge["out_of_tree_testmain"], len(other_paths(merge)),
            merge["external_symbols_dropped"], merge["round_trip_unstable_docs"],
            merge["version_skew_references"],
            SCIP_GO_SHA, SCIP_GO_BYTES, GOTOOLCHAIN, remote, pin[:12], rels_text,
            merge_sha, merge_bytes,
            snap["layout_bytes"], snap["layout_sha256"], snap["roots"], snap["regular_files"],
            snap["generated_from_bytes"], snap["generated_from_sha256"],
            snap["clients"], snap["mapped"], snap["abstained"],
            snap["mapping_table_sha256"], snapshot_sha, snapshot_bytes,
            BOUND_FILES, snap["max_blob_path"], snap["max_blob_bytes"],
        )

        run(["git", "add", "--"] + DERIVED_PATHS, cwd=clone)
        staged = run(["git", "diff", "--cached", "--name-only"], cwd=clone).split()
        if sorted(staged) != sorted(DERIVED_PATHS):
            raise SystemExit("%s: staged set %s != %s" % (repo, staged, DERIVED_PATHS))
        date = run(["git", "show", "-s", "--format=%cI", pin], cwd=clone).strip()
        env = dict(os.environ,
                   GIT_AUTHOR_NAME=DERIVED_IDENTITY[0], GIT_AUTHOR_EMAIL=DERIVED_IDENTITY[1],
                   GIT_COMMITTER_NAME=DERIVED_IDENTITY[0], GIT_COMMITTER_EMAIL=DERIVED_IDENTITY[1],
                   GIT_AUTHOR_DATE=date, GIT_COMMITTER_DATE=date)
        p = subprocess.run(["git", "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-F", "-"],
                           cwd=clone, input=message, text=True, capture_output=True, env=env)
        if p.returncode != 0:
            raise SystemExit("%s: commit failed\n%s\n%s" % (repo, p.stdout, p.stderr))

        derived = run(["git", "rev-parse", "HEAD"], cwd=clone).strip()
        tree = run(["git", "rev-parse", "HEAD^{tree}"], cwd=clone).strip()
        parents = run(["git", "rev-list", "--parents", "-n", "1", "HEAD"], cwd=clone).split()
        if parents[1:] != [pin]:
            raise SystemExit("%s: derived commit parents %s != [%s]" % (repo, parents[1:], pin))
        after = run(["git", "status", "--porcelain"], cwd=clone)
        if after.strip():
            raise SystemExit("%s: clone not clean after commit:\n%s" % (repo, after))

        blobs = {}
        for rel in DERIVED_PATHS:
            blobs[rel] = run(["git", "rev-parse", "HEAD:%s" % rel], cwd=clone).strip()
            p = subprocess.run(["git", "cat-file", "blob", "HEAD:%s" % rel], cwd=clone,
                               capture_output=True)
            if p.returncode != 0:
                raise SystemExit("%s: cat-file %s failed: %s" % (repo, rel, p.stderr.decode("utf8", "replace")))
            if hashlib.sha256(p.stdout).hexdigest() != files[rel]["sha256"]:
                raise SystemExit("%s: committed %s digest != measured working-tree digest" % (repo, rel))
            if len(p.stdout) != files[rel]["bytes"]:
                raise SystemExit("%s: committed %s bytes != measured working-tree bytes" % (repo, rel))
        shown = run(["git", "show", "--stat", "--oneline", "-s", "HEAD"], cwd=clone).strip()

        facts[repo] = {
            "name": remote_of[repo],
            "pin": pin,
            "derived_commit": derived,
            "derived_tree": tree,
            "derived_parents": parents[1:],
            "files": {rel: dict(files[rel], git_blob_sha1=blobs[rel]) for rel in DERIVED_PATHS},
            "merge": {
                "docs": merge["docs"],
                "occurrences": merge["occurrences"],
                "symbols": merge["symbols"],
                "out_of_tree_dropped": merge["out_of_tree_dropped"],
                "out_of_tree_testmain": merge["out_of_tree_testmain"],
                "out_of_tree_other": other_paths(merge),
                "version_skew_references": merge["version_skew_references"],
                "version_skew_sample": merge["version_skew_sample"],
                "external_symbols_dropped": merge["external_symbols_dropped"],
                "round_trip_unstable_docs": merge["round_trip_unstable_docs"],
                "bytes": merge["bytes"],
                "module_rels": rels,
                "runs": len(rels),
            },
            "snapshots": {
                "clients": snap["clients"], "mapped": snap["mapped"], "abstained": snap["abstained"],
                "mappings": snap["mappings"], "roots": snap["roots"], "roots_deduped": snap["roots_deduped"],
                "regular_files": snap["regular_files"], "gitlinks": snap["gitlinks"], "symlinks": snap["symlinks"],
                "max_blob_path": snap["max_blob_path"], "max_blob_bytes": snap["max_blob_bytes"],
                "over_blob_bound": snap["over_blob_bound"],
                "files_within_bound": snap["files_within_bound"], "blobs_within_bound": snap["blobs_within_bound"],
                "vendored_mapped": snap["vendored_mapped"], "nonvendored_mapped": snap["nonvendored_mapped"],
                "gates": snap["gates"],
                "mapping_table_sha256": snap["mapping_table_sha256"],
                "abstention_summary": snap["abstention_summary"],
            },
            "remote": remote,
            "commit_line": shown,
        }
        print("== %s derived commit %s (parent %s)" % (repo, derived, pin[:12]))
        print("   index.scip %d bytes %s" % (files["index.scip"]["bytes"], files["index.scip"]["sha256"]))

    out = os.path.join(DER, "derived_facts.json")
    with open(out, "w") as f:
        json.dump(facts, f, indent=2, sort_keys=True)
        f.write("\n")
    print("wrote %s" % out)


if __name__ == "__main__":
    sys.exit(main())
