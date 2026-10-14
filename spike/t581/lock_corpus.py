#!/usr/bin/env python3
"""T58.1: clone and lock the frozen gRPC consumer/registration corpus.

This tool prepares the population. It runs **no** phebs code, no extractor and no
oracle, and reads no Go file's contents: the preregistration discipline in
docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md §7 requires the lock to exist before the
extractor touches the population.

What it does, per the protocol:

  * clones each frozen §2 name at depth 1 (§2 forbids history expansion, and the
    unit is one repository at one pinned commit);
  * records the exact HEAD commit, so the round is reproducible from the lock;
  * applies the single pre-declared drop rule — no same-repository
    ``*_grpc.pb.go`` stub at the pinned HEAD — and records every drop verbatim;
  * inventories symlinks and never parses or follows them (the T47.2 etcd
    lesson);
  * enforces a free-space floor before each clone and aborts cleanly, recording
    where it stopped, rather than filling the volume.

Usage:
    python3 spike/t581/lock_corpus.py [--corpus ~/phebs-t581-corpus]
                                      [--out spike/t581/corpus.lock.json]
                                      [--floor-gib 40] [--dry-run]
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

SCHEMA = "t581-grpc-consumer-corpus-lock-v1"

STUB_SUFFIX = "_grpc.pb.go"

# The frozen §2 list, approved by Ben on 2026-10-13 and closed to amendment.
# Order is the protocol's; it carries no meaning for selection.
FROZEN_NAMES = (
    "containerd/containerd",
    "etcd-io/etcd",
    "spiffe/spire",
    "hyperledger/fabric",
    "vitessio/vitess",
    "thanos-io/thanos",
    "argoproj/argo-cd",
    "moby/buildkit",
    "tikv/client-go",
    "grpc-ecosystem/grpc-gateway",
    "hashicorp/nomad",
    "hashicorp/go-plugin",
    "grafana/loki",
    "goharbor/harbor",
    "projectcontour/contour",
    "kubevirt/kubevirt",
    "temporalio/temporal",
    "envoyproxy/go-control-plane",
)

SELECTION_RULE = (
    "Public Go repositories whose non-test code contains at least one "
    "same-repository generated *_grpc.pb.go stub and at least one call or "
    "registration site against it, chosen by name from prior knowledge before any "
    "phebs, extractor or oracle output was seen. Approved and closed by Ben on "
    "2026-10-13, including etcd-io/etcd with its T47.2 grpc-caller overlap "
    "disclosed and accepted. Pinned at the default-branch HEAD of a depth-1 clone "
    "on the selection date; the population is one commit per repository and is not "
    "history-expanded. A member is dropped only for the pre-declared reason "
    "'no same-repository *_grpc.pb.go stub at the pinned HEAD'; no result, score, "
    "site count or oracle disagreement may retire one."
)

DROP_REASON = "no same-repository *_grpc.pb.go stub at the pinned HEAD"


def run(argv: list[str], cwd: Path | None = None) -> str:
    completed = subprocess.run(
        argv, cwd=cwd, check=True, capture_output=True, text=True,
    )
    return completed.stdout.strip()


def free_bytes(path: Path) -> int:
    return shutil.disk_usage(path).free


def directory_for(name: str) -> str:
    return name.replace("/", "__")


def clone(name: str, corpus: Path, dry: bool) -> tuple[Path, str]:
    """Clone one member at depth 1 and return its directory and exact commit."""
    target = corpus / directory_for(name)
    url = f"https://github.com/{name}.git"
    if target.is_dir():
        # Idempotent: an existing clone is reused only when it is a clean
        # depth-1 checkout we can still read a commit from.
        commit = run(["git", "rev-parse", "HEAD"], cwd=target)
        print(f"  reuse {name} @ {commit[:12]}", flush=True)
        return target, commit
    if dry:
        print(f"  would clone {name}", flush=True)
        return target, ""
    print(f"  clone {name}", flush=True)
    run(["git", "clone", "--depth", "1", "--quiet", url, str(target)])
    return target, run(["git", "rev-parse", "HEAD"], cwd=target)


def scan(root: Path) -> tuple[int, list[str], list[str]]:
    """Count stubs, and inventory symlinks and stub-named symlinks separately.

    Symlinks are never followed and never parsed; they are counted so the lock
    records that they were seen, which is what T47.2's etcd symlinks required.
    """
    stubs = 0
    symlinks: list[str] = []
    stub_symlinks: list[str] = []
    for directory, dirnames, filenames in os.walk(root, followlinks=False):
        dirnames[:] = [d for d in dirnames if d != ".git"]
        for filename in filenames:
            path = Path(directory) / filename
            relative = str(path.relative_to(root))
            if path.is_symlink():
                symlinks.append(relative)
                if filename.endswith(STUB_SUFFIX):
                    stub_symlinks.append(relative)
                continue
            if filename.endswith(STUB_SUFFIX):
                stubs += 1
    return stubs, symlinks, stub_symlinks


def go_version() -> str:
    if shutil.which("go") is None:
        return ""
    try:
        return run(["go", "version"])
    except subprocess.CalledProcessError:
        return ""


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--corpus", type=Path, default=Path.home() / "phebs-t581-corpus",
        help="corpus root, outside the repository and never committed",
    )
    parser.add_argument(
        "--out", type=Path, default=Path("spike/t581/corpus.lock.json"),
    )
    parser.add_argument(
        "--floor-gib", type=float, default=40.0,
        help="abort before a clone that would leave less than this free",
    )
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    corpus: Path = args.corpus.expanduser()
    floor = int(args.floor_gib * (1 << 30))
    corpus.parent.mkdir(parents=True, exist_ok=True)
    if not args.dry_run:
        corpus.mkdir(parents=True, exist_ok=True)

    started_free = free_bytes(corpus.parent)
    print(
        f"corpus={corpus} free={started_free / (1 << 30):.1f} GiB "
        f"floor={args.floor_gib} GiB members={len(FROZEN_NAMES)}",
        flush=True,
    )

    repos: list[dict] = []
    dropped: list[dict] = []
    stopped: str | None = None
    for name in FROZEN_NAMES:
        if not args.dry_run and free_bytes(corpus.parent) < floor:
            stopped = name
            print(
                f"  ABORT before {name}: free space below the "
                f"{args.floor_gib} GiB floor",
                flush=True,
            )
            break
        try:
            target, commit = clone(name, corpus, args.dry_run)
        except subprocess.CalledProcessError as error:
            print(f"  FAILED to clone {name}: {error.stderr.strip()}", flush=True)
            dropped.append({
                "name": name, "commit": "",
                "reason": f"clone failed: {error.stderr.strip()[:200]}",
            })
            continue
        if args.dry_run:
            continue
        stubs, symlinks, stub_symlinks = scan(target)
        entry = {
            "name": name,
            "dir": directory_for(name),
            "git_url": f"https://github.com/{name}.git",
            "commit": commit,
            "stub_files": stubs,
            "symlinks_inventoried": len(symlinks),
            "stub_named_symlinks": sorted(stub_symlinks),
        }
        if stubs == 0:
            entry["reason"] = DROP_REASON
            dropped.append(entry)
            print(f"  drop  {name} @ {commit[:12]}: {DROP_REASON}", flush=True)
            continue
        repos.append(entry)
        print(
            f"  keep  {name} @ {commit[:12]}: {stubs} stubs, "
            f"{len(symlinks)} symlinks inventoried",
            flush=True,
        )

    lock = {
        "schema": SCHEMA,
        "selected_utc": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d"),
        "selection_rule": SELECTION_RULE,
        "protocol": "docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md",
        "claim": "site-coverage",
        "unit": "one repository at one pinned commit; whole .go tree per unit",
        "history_expanded": False,
        "host": {
            "system": platform.system(),
            "machine": platform.machine(),
            "release": platform.release(),
            "go": go_version(),
            "free_bytes_at_start": started_free,
            "floor_bytes": floor,
        },
        "stopped_before": stopped,
        "repos": repos,
        "dropped": dropped,
    }

    if args.dry_run:
        print("dry run: no lock written", flush=True)
        return 0
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(lock, indent=2) + "\n", encoding="utf-8")
    print(
        f"\nwrote {args.out}: {len(repos)} members, {len(dropped)} dropped"
        + (f", STOPPED before {stopped}" if stopped else ""),
        flush=True,
    )
    if stopped:
        # A partial lock is not a lock. Fail loudly so nobody scores a
        # truncated population as if it were the frozen one.
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
