#!/usr/bin/env python3
"""GATE2-V2 §6 census-v2 carry-forward mapper — burn on doubt.

Maps every previously disclosed coordinate (system, path, start_line,
end_line at an old pinned commit) onto a new pinned head and decides burn
vs free. The decision rules, in order:

  identical            file exists at the new head and the normalized
                       old line-range appears contiguously anywhere in the
                       new file -> BURN
  correspondence       file exists and shares any normalized non-empty
                       anchor line with the old range (edited-but-matching
                       declaration) -> BURN
  uncertain-default    file exists but no correspondence can be
                       established -> BURN (doubt burns)
  renamed-successor    file absent, but git rename tracking (-M) traces a
                       successor at the new head -> BURN
  absent               file absent with no traced successor: the only
                       positively-established free case -> FREE

Normalization (committed rule): strip trailing whitespace, collapse every
internal whitespace run to one space, drop lines that normalize to empty
when matching anchors.
"""

from __future__ import annotations

import json
import subprocess
import sys
from dataclasses import dataclass


def _git(repo: str, *args: str) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", "-C", repo, *args], check=False,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )


def normalize_line(line: str) -> str:
    return " ".join(line.strip().split())


def normalize_range(lines: list[str]) -> list[str]:
    return [normalize_line(line) for line in lines]


def _file_lines(repo: str, commit: str, path: str) -> list[str] | None:
    completed = _git(repo, "show", f"{commit}:{path}")
    if completed.returncode != 0:
        return None
    return completed.stdout.decode("utf-8", errors="replace").splitlines()


def _renamed_successor(repo: str, old: str, new: str, path: str) -> str | None:
    # No pathspec: limiting the diff to the old path suppresses git's rename
    # pairing, which would misreport a rename as a plain deletion (free).
    completed = _git(repo, "diff", "-M", "--name-status", old, new)
    if completed.returncode != 0:
        return "?"  # tracing failed: uncertain, caller burns
    for row in completed.stdout.decode("utf-8", errors="replace").splitlines():
        fields = row.split("\t")
        if fields and fields[0].startswith("R") and len(fields) == 3 and fields[1] == path:
            return fields[2]
    return None


@dataclass
class Decision:
    decision: str  # "burn" | "free"
    rule: str


def classify(repo: str, old_commit: str, new_commit: str,
             path: str, start_line: int, end_line: int) -> Decision:
    old_lines = _file_lines(repo, old_commit, path)
    if old_lines is None or start_line < 1 or end_line < start_line:
        return Decision("burn", "uncertain-default")
    old_range = normalize_range(old_lines[start_line - 1:end_line])
    anchors = [line for line in old_range if line]

    new_lines = _file_lines(repo, new_commit, path)
    if new_lines is None:
        successor = _renamed_successor(repo, old_commit, new_commit, path)
        if successor is None:
            return Decision("free", "absent")
        return Decision("burn", "renamed-successor")

    new_normalized = normalize_range(new_lines)
    span = len(old_range)
    if span and any(new_normalized[i:i + span] == old_range
                    for i in range(len(new_normalized) - span + 1)):
        return Decision("burn", "identical")
    if anchors and any(line in set(new_normalized) for line in anchors):
        return Decision("burn", "correspondence")
    return Decision("burn", "uncertain-default")


def map_coordinates(repo: str, old_commit: str, new_commit: str,
                    coordinates: list[dict]) -> list[dict]:
    results = []
    for coordinate in coordinates:
        decision = classify(
            repo, old_commit, new_commit, coordinate["path"],
            int(coordinate["start_line"]), int(coordinate["end_line"]),
        )
        results.append({**coordinate, "decision": decision.decision,
                        "rule": decision.rule})
    return results


def main() -> int:
    if len(sys.argv) != 5:
        print("usage: carry_forward.py <repo> <old_commit> <new_commit> "
              "<coordinates.jsonl>", file=sys.stderr)
        return 2
    repo, old_commit, new_commit, coordinate_path = sys.argv[1:]
    with open(coordinate_path, encoding="utf-8") as handle:
        coordinates = [json.loads(line) for line in handle if line.strip()]
    for row in map_coordinates(repo, old_commit, new_commit, coordinates):
        print(json.dumps(row, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
