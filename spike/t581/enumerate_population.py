#!/usr/bin/env python3
"""T58.1: enumerate the locked corpus into population units.

A unit is **one repository at one pinned commit**, not one file blob: the
`grpc-consumer` extractor is two-pass and repository-scoped (GD6), so a single
file in isolation has no stub index and would score entirely as out of domain.

This tool reads no Go file's contents. It lists paths with `git ls-tree`, records
each unit's complete `.go` file set and its `*_grpc.pb.go` subset, and writes:

  population/units.jsonl.gz   one t581-unit-v1 record per unit, gzip JSON Lines
  population/population.json  counts plus the enumerator and units digests

Symlinks are inventoried and never listed as files, per the T47.2 etcd lesson:
a symlinked `.go` path would otherwise be read through to a target outside the
pinned tree. Runs before the seal; the exporter is what must not.

Usage:
    python3 spike/t581/enumerate_population.py \
        --corpus ~/phebs-t581-corpus \
        --lock spike/t581/corpus.lock.json \
        --out-dir spike/t581/population
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import subprocess
import sys
from pathlib import Path

UNIT_SCHEMA = "t581-unit-v1"
POPULATION_SCHEMA = "t581-grpc-consumer-population-v1"
STUB_SUFFIX = "_grpc.pb.go"
GIT_LINK_MODE = "120000"
GIT_LINK_COMMIT_MODE = "160000"


def run(argv: list[str]) -> bytes:
    return subprocess.run(argv, check=True, capture_output=True).stdout


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return "sha256:" + digest.hexdigest()


def sha256_bytes(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def list_tree(clone: Path, commit: str) -> tuple[list[str], list[str], list[str]]:
    """Return (regular .go paths, symlinked .go paths, .go-named gitlinks).

    Uses -z so paths with unusual bytes survive, and reads the mode so a
    symlink or a submodule gitlink is never mistaken for a regular file.
    `ls-tree -r` does not recurse into submodules, so a gitlink's contents are
    invisible here exactly as they are to a plain checkout; any gitlink whose
    path ends in .go is recorded separately rather than silently dropped.
    """
    raw = run(["git", "-C", str(clone), "ls-tree", "-r", "-z", commit])
    regular: list[str] = []
    symlinks: list[str] = []
    gitlinks: list[str] = []
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        meta, _, path = entry.partition(b"\t")
        fields = meta.split(b" ")
        if len(fields) < 3:
            raise SystemExit(f"unparsable ls-tree entry: {entry!r}")
        mode, kind = fields[0].decode(), fields[1].decode()
        if not path.endswith(b".go"):
            continue
        name = path.decode("utf-8", "surrogateescape")
        if mode in ("100644", "100755") and kind == "blob":
            regular.append(name)
        elif mode == GIT_LINK_MODE or kind == "link":
            symlinks.append(name)
        elif mode == GIT_LINK_COMMIT_MODE or kind == "commit":
            gitlinks.append(name)
        else:
            raise SystemExit(f"unexpected .go tree entry mode={mode} kind={kind}: {name}")
    regular.sort()
    symlinks.sort()
    gitlinks.sort()
    return regular, symlinks, gitlinks


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", type=Path, default=Path.home() / "phebs-t581-corpus")
    parser.add_argument("--lock", type=Path, default=Path("spike/t581/corpus.lock.json"))
    parser.add_argument("--out-dir", type=Path, default=Path("spike/t581/population"))
    args = parser.parse_args()

    corpus = args.corpus.expanduser()
    lock = json.loads(args.lock.read_text(encoding="utf-8"))
    members = lock["repos"]
    if not members:
        raise SystemExit("the corpus lock has no members")

    units: list[dict] = []
    by_repository: dict[str, dict[str, int]] = {}
    for member in sorted(members, key=lambda entry: entry["name"]):
        clone = corpus / member["dir"]
        if not clone.is_dir():
            raise SystemExit(f"missing clone for {member['name']}: {clone}")
        head = run(["git", "-C", str(clone), "rev-parse", "HEAD"]).decode().strip()
        if head != member["commit"]:
            raise SystemExit(
                f"{member['name']}: clone HEAD {head[:12]} is not the locked commit "
                f"{member['commit'][:12]}; re-run lock_corpus.py rather than enumerating drift"
            )
        go_files, go_symlinks, go_gitlinks = list_tree(clone, member["commit"])
        stubs = sorted(p for p in go_files if p.endswith(STUB_SUFFIX))
        # The unit record is the published contract the independent oracle is
        # written against, so it carries exactly the agreed fields. Symlink and
        # gitlink counts are diagnostics and stay in population.json only.
        unit = {
            "schema": UNIT_SCHEMA,
            "unit": member["name"],
            "repository": member["name"],
            "commit": member["commit"],
            "dir": member["dir"],
            "go_files": go_files,
            "stub_files": stubs,
        }
        units.append(unit)
        by_repository[member["name"]] = {
            "go_files": len(go_files),
            "stub_files": len(stubs),
            "go_symlinks": len(go_symlinks),
            "go_gitlinks": len(go_gitlinks),
        }
        print(
            f"  {member['name']:<38} go={len(go_files):<6} stubs={len(stubs):<4} "
            f"symlinks={len(go_symlinks)} gitlinks={len(go_gitlinks)}",
            flush=True,
        )

    payload = "".join(json.dumps(unit, sort_keys=True) + "\n" for unit in units)
    compressed = gzip.compress(payload.encode("utf-8"), mtime=0)

    args.out_dir.mkdir(parents=True, exist_ok=True)
    units_path = args.out_dir / "units.jsonl.gz"
    units_path.write_bytes(compressed)

    enumerator = Path(__file__).resolve()
    population = {
        "schema": POPULATION_SCHEMA,
        "unit_schema": UNIT_SCHEMA,
        "unit": "one repository at one pinned commit; whole .go tree per unit",
        "protocol": "docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md",
        "lock": str(args.lock),
        "lock_schema": lock["schema"],
        "selected_utc": lock["selected_utc"],
        "total": len(units),
        "total_go_files": sum(v["go_files"] for v in by_repository.values()),
        "total_stub_files": sum(v["stub_files"] for v in by_repository.values()),
        "total_go_symlinks": sum(v["go_symlinks"] for v in by_repository.values()),
        "total_go_gitlinks": sum(v["go_gitlinks"] for v in by_repository.values()),
        "by_repository": by_repository,
        "enumerator_sha256": sha256_file(enumerator),
        "units_sha256": sha256_bytes(compressed),
        "units_uncompressed_sha256": sha256_bytes(payload.encode("utf-8")),
    }
    population_path = args.out_dir / "population.json"
    population_path.write_text(
        json.dumps(population, indent=2, sort_keys=True) + "\n", encoding="utf-8",
    )

    print(
        f"\nwrote {units_path} ({len(units)} units, "
        f"{population['total_go_files']} .go files, "
        f"{population['total_stub_files']} stubs)\n"
        f"wrote {population_path}\n"
        f"units_sha256 = {population['units_sha256']}",
        flush=True,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
