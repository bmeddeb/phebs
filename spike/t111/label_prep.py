#!/usr/bin/env python3
"""Build the Gate 2 probability sample without exposing extractor verdicts.

The measurement has two independent frames:

* recall: every exported Go selector invocation in the pinned corpus.  This
  lexical superset does not use extractor output, so external-module gRPC
  methods remain eligible for sampling.
* precision: every CALLS_OPERATION fact emitted by the extractor.

Holdout units are selected by a precommitted SHA-256 rank inside explicit
system/risk (recall) or system/role (precision) strata.  The rank is
reproducible and extractor-independent, but it is not physical randomness;
gate use therefore fixes the seed before labels exist and records this design
limitation.  Development units are sampled only after every holdout unit has
been excluded.  Both replacement frames first remove every source interval
that overlaps the committed legacy burn ledger.  The hidden key records frame
membership; labeler-facing files contain coordinates only.  Source context can
be materialized separately under the ignored out/ tree with the
``materialize-context`` command.
"""

from __future__ import annotations

import argparse
import bisect
import hashlib
import json
import math
import os
import re
import subprocess
import sys
import tempfile
from collections import defaultdict
from pathlib import Path
from typing import Any, Iterable, Sequence

from gate34_common import (
    ValidationError as EvidenceValidationError,
    coordinate_burned,
    load_burn_ledger,
    load_facts as load_verified_facts,
)


BASE = Path(__file__).resolve().parent
DEFAULT_CORPUS = BASE / "corpus"
DEFAULT_FACTS = BASE / "out"
DEFAULT_ARTIFACTS = BASE / "labeling" / "g2-v2"
DEFAULT_CONTEXT = BASE / "out" / "gate2-label-context"
DEFAULT_LOCK = BASE / "corpus.lock.json"
DEFAULT_BURN_LEDGER = BASE / "labeling" / "burn-ledger.json"
SYSTEMS = ("online-boutique", "dapr", "temporal", "loki")
SCHEMA = "t111-gate2-probability-sample-v2"
SEED = 111
GATE_RECALL_HOLDOUT_PER_SYSTEM = 200
GATE_RECALL_DEV_PER_SYSTEM = 120
GATE_PRECISION_HOLDOUT_PER_SYSTEM = 200
GATE_PRECISION_DEV_PER_SYSTEM = 60
GATE_RECALL_MIN_PER_STRATUM = 30
GATE_PRECISION_MIN_PER_STRATUM = 15
MIN_BLIND_HOLDOUT_FRACTION = 0.30
CODE_ROLES = {"production", "test", "mock", "generated", "vendor"}

SELECTOR_CALL = re.compile(
    r"\.[ \t\r\n]*([A-Z][A-Za-z0-9_]*)[ \t\r\n]*\("
)
PROTO_RPC = re.compile(r"\brpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")
CLIENT_INTERFACE = re.compile(
    r"type\s+[A-Za-z_][A-Za-z0-9_]*Client\s+interface\s*\{(.*?)\n\}", re.S
)
INTERFACE_METHOD = re.compile(r"^\s*([A-Z][A-Za-z0-9_]*)\s*\(", re.M)
WIRE_METHOD = re.compile(r'"/[A-Za-z0-9_.-]+/([A-Za-z_][A-Za-z0-9_]*)"')


class PrepError(RuntimeError):
    """An invariant failed; no measurement artifact should be published."""


def git(root: Path, *arguments: str) -> bytes:
    try:
        result = subprocess.run(
            ["git", "-C", str(root), *arguments],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except OSError as exc:
        raise PrepError(f"cannot run git for {root}: {exc}") from exc
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise PrepError(f"git {' '.join(arguments)} failed for {root}: {detail}")
    return result.stdout


def load_corpus_lock(path: Path, systems: Sequence[str]) -> dict[str, str]:
    try:
        rows = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load corpus lock {path}: {exc}") from exc
    if not isinstance(rows, list):
        raise PrepError(f"{path}: expected a list")
    commits: dict[str, str] = {}
    for row in rows:
        if not isinstance(row, dict) or not isinstance(row.get("name"), str):
            raise PrepError(f"{path}: invalid lock entry")
        name = row["name"]
        commit = row.get("commit")
        if name in commits:
            raise PrepError(f"{path}: duplicate lock entry {name}")
        if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
            raise PrepError(f"{path}: {name} commit must be a full lowercase 40-hex ID")
        commits[name] = commit
    missing = set(systems) - set(commits)
    if missing:
        raise PrepError(f"{path}: missing lock entries {sorted(missing)}")
    return commits


def pinned_tracked_files(root: Path, expected_commit: str) -> tuple[list[str], list[str]]:
    """Verify a clean pin and return regular tracked blobs.

    Gitlinks are rejected because the parent commit does not pin their recursive
    source tree. A symlinked Go/proto path is also rejected; other symlinks are
    recorded but cannot enter the frame, which reads regular Git blobs only.
    """

    if root.is_symlink():
        raise PrepError(f"corpus root may not be a symlink: {root}")
    head = git(root, "rev-parse", "--verify", "HEAD").decode().strip()
    if head != expected_commit:
        raise PrepError(f"{root}: HEAD {head} does not match lock {expected_commit}")
    dirty = git(root, "status", "--porcelain=v1", "--untracked-files=all")
    if dirty:
        first = dirty.decode("utf-8", errors="replace").splitlines()[0]
        raise PrepError(f"{root}: checkout is not clean (tracked and untracked required): {first}")

    raw = git(root, "ls-tree", "-rz", "--full-tree", "HEAD")
    regular: list[str] = []
    special: list[str] = []
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        metadata, separator, raw_path = entry.partition(b"\t")
        if not separator:
            raise PrepError(f"{root}: malformed git tree entry")
        fields = metadata.decode("ascii").split()
        if len(fields) != 3:
            raise PrepError(f"{root}: malformed git tree metadata")
        mode, object_type, _ = fields
        rel = raw_path.decode("utf-8", errors="strict")
        if mode in ("100644", "100755") and object_type == "blob":
            regular.append(rel)
        elif mode == "160000":
            raise PrepError(f"{root}: tracked gitlink makes the source frame incomplete: {rel}")
        elif mode == "120000":
            if rel.endswith((".go", ".proto")):
                raise PrepError(f"{root}: symlinked source makes the source frame incomplete: {rel}")
            special.append(rel)
        else:
            raise PrepError(f"{root}: unsupported tracked tree entry {mode} {object_type} {rel}")
    return regular, special


def git_blob(root: Path, commit: str, rel: str) -> bytes:
    """Read a pinned regular blob without consulting the mutable worktree."""

    if not rel or rel.startswith("/") or ".." in Path(rel).parts:
        raise PrepError(f"unsafe pinned path: {rel!r}")
    return git(root, "cat-file", "blob", f"{commit}:{rel}")


def utf8_blob(root: Path, commit: str, rel: str) -> str:
    try:
        return git_blob(root, commit, rel).decode("utf-8", errors="strict")
    except UnicodeDecodeError as exc:
        raise PrepError(f"{root}:{rel}: source is not valid UTF-8: {exc}") from exc


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def digest_parts(*parts: Any) -> str:
    payload = canonical_json(parts).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    try:
        with path.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                h.update(chunk)
    except OSError as exc:
        raise PrepError(f"cannot hash {path}: {exc}") from exc
    return h.hexdigest()


def verified_burn_ledger(path: Path) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Load the committed ledger and return its exact manifest binding."""

    try:
        burned, coordinates_sha256 = load_burn_ledger(path)
    except EvidenceValidationError as exc:
        raise PrepError(f"invalid burn ledger {path}: {exc}") from exc
    return burned, {
        "sha256": sha256_file(path),
        "coordinate_count": len(burned),
        "coordinates_sha256": coordinates_sha256,
    }


def exclude_burned(
    rows: Sequence[dict[str, Any]], burned: Sequence[dict[str, Any]]
) -> tuple[list[dict[str, Any]], int]:
    """Remove any sampling unit whose source interval overlaps legacy labels."""

    kept: list[dict[str, Any]] = []
    excluded = 0
    for row in rows:
        if coordinate_burned(row, burned):
            excluded += 1
        else:
            kept.append(row)
    return kept, excluded


def site_id(system: str, path: str, byte_offset: int, method: str) -> str:
    return "g2s_" + digest_parts(system, path, byte_offset, method)


def rank(seed: int, purpose: str, stratum: str, sid: str) -> str:
    return digest_parts(seed, purpose, stratum, sid)


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    try:
        handle = path.open(encoding="utf-8")
    except OSError as exc:
        raise PrepError(f"cannot read {path}: {exc}") from exc
    with handle:
        for line_number, line in enumerate(handle, 1):
            if not line.strip():
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as exc:
                raise PrepError(f"{path}:{line_number}: invalid JSON: {exc}") from exc
            if not isinstance(row, dict):
                raise PrepError(f"{path}:{line_number}: expected a JSON object")
            rows.append(row)
    return rows


def write_jsonl(path: Path, rows: Iterable[dict[str, Any]]) -> None:
    text = "".join(canonical_json(row) + "\n" for row in rows)
    atomic_write(path, text)


def atomic_write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def mask_go_source(text: str) -> str:
    """Blank Go comments and literals while preserving offsets and newlines."""

    out = list(text)
    i = 0
    state = "code"
    quote = ""
    while i < len(text):
        c = text[i]
        nxt = text[i + 1] if i + 1 < len(text) else ""
        if state == "code":
            if c == "/" and nxt == "/":
                out[i] = out[i + 1] = " "
                i += 2
                state = "line_comment"
                continue
            if c == "/" and nxt == "*":
                out[i] = out[i + 1] = " "
                i += 2
                state = "block_comment"
                continue
            if c in ('"', "'", "`"):
                quote = c
                out[i] = " "
                i += 1
                state = "raw" if c == "`" else "quoted"
                continue
            i += 1
            continue
        if state == "line_comment":
            if c == "\n":
                state = "code"
            else:
                out[i] = " "
            i += 1
            continue
        if state == "block_comment":
            if c == "*" and nxt == "/":
                out[i] = out[i + 1] = " "
                i += 2
                state = "code"
            else:
                if c != "\n":
                    out[i] = " "
                i += 1
            continue
        if state == "raw":
            if c == "`":
                out[i] = " "
                state = "code"
            elif c != "\n":
                out[i] = " "
            i += 1
            continue
        if state == "quoted":
            if c == "\\":
                out[i] = " "
                if i + 1 < len(text):
                    if text[i + 1] != "\n":
                        out[i + 1] = " "
                    i += 2
                else:
                    i += 1
                continue
            if c == quote:
                out[i] = " "
                state = "code"
            elif c != "\n":
                out[i] = " "
            i += 1
    return "".join(out)


def source_offsets(text: str) -> tuple[list[int], list[int]]:
    char_starts = [0]
    byte_starts = [0]
    char_total = 0
    byte_total = 0
    for line in text.splitlines(keepends=True):
        char_total += len(line)
        byte_total += len(line.encode("utf-8"))
        char_starts.append(char_total)
        byte_starts.append(byte_total)
    return char_starts, byte_starts


def coordinate_at(
    text: str, char_offset: int, char_starts: Sequence[int], byte_starts: Sequence[int]
) -> tuple[int, int, int]:
    line_index = bisect.bisect_right(char_starts, char_offset) - 1
    line_char = char_starts[line_index]
    byte_offset = byte_starts[line_index] + len(
        text[line_char:char_offset].encode("utf-8")
    )
    return line_index + 1, char_offset - line_char + 1, byte_offset


def discover_rpc_methods(
    root: Path, commit: str, tracked_files: Sequence[str]
) -> set[str]:
    """Build a risk signal from source declarations, never extractor facts."""

    methods: set[str] = set()
    for rel in sorted(path for path in tracked_files if path.endswith(".proto")):
        methods.update(PROTO_RPC.findall(utf8_blob(root, commit, rel)))
    for rel in sorted(path for path in tracked_files if path.endswith(".go")):
        text = utf8_blob(root, commit, rel)
        methods.update(WIRE_METHOD.findall(text))
        for block in CLIENT_INTERFACE.finditer(mask_go_source(text)):
            methods.update(INTERFACE_METHOD.findall(block.group(1)))
    return methods


def grpc_risk(masked: str, match: re.Match[str], method: str, rpc_methods: set[str]) -> str:
    if method in rpc_methods:
        return "known_rpc_name"
    before = masked[max(0, match.start() - 120) : match.start()].lower()
    after = masked[match.start(1) : min(len(masked), match.end() + 180)].lower()
    receiver_signal = re.search(r"(?:client|grpc|stub|conn|service)\w*\s*$", before)
    request_signal = re.search(
        r"\(\s*(?:ctx\b|context\b|[^,()]*newcontext\s*\(|[^,()]*context\s*\()",
        after,
    ) and re.search(r"(?:request\s*\{|emptypb\.|context\b|ctx\b)", after)
    if receiver_signal or request_signal:
        return "grpc_lexical_signal"
    return "other_exported_selector"


def scan_recall_frame(
    system: str,
    root: Path,
    commit: str,
    rpc_methods: set[str],
    tracked_files: Sequence[str],
) -> list[dict[str, Any]]:
    population: list[dict[str, Any]] = []
    seen: dict[str, tuple[str, int, str]] = {}
    for rel in sorted(path for path in tracked_files if path.endswith(".go")):
        text = utf8_blob(root, commit, rel)
        masked = mask_go_source(text)
        char_starts, byte_starts = source_offsets(text)
        for match in SELECTOR_CALL.finditer(masked):
            method = match.group(1)
            line, column, byte_offset = coordinate_at(
                text, match.start(1), char_starts, byte_starts
            )
            sid = site_id(system, rel, byte_offset, method)
            coordinate = (rel, byte_offset, method)
            if sid in seen and seen[sid] != coordinate:
                raise PrepError(f"site-id collision for {sid}: {seen[sid]} vs {coordinate}")
            if sid in seen:
                raise PrepError(f"duplicate recall candidate at {system}:{rel}:{line}:{column}")
            seen[sid] = coordinate
            risk = grpc_risk(masked, match, method, rpc_methods)
            population.append(
                {
                    "site_id": sid,
                    "system": system,
                    "path": rel,
                    "line": line,
                    "start_line": line,
                    "end_line": line,
                    "column": column,
                    "byte_offset": byte_offset,
                    "method": method,
                    "stratum": f"{system}|{risk}",
                    "risk": risk,
                }
            )
    return population


def relpath_of(raw_path: str, system: str, root: Path) -> str:
    path = Path(raw_path)
    if path.is_absolute():
        try:
            return path.resolve().relative_to(root.resolve()).as_posix()
        except ValueError:
            marker = f"/corpus/{system}/"
            normalized = raw_path.replace("\\", "/")
            if marker in normalized:
                return normalized.split(marker, 1)[1]
    normalized = raw_path.replace("\\", "/")
    for prefix in (f"corpus/{system}/", f"{system}/"):
        if normalized.startswith(prefix):
            return normalized[len(prefix) :]
    return normalized


def call_facts(
    system: str, facts_dir: Path, expected_commit: str, corpus_dir: Path
) -> list[dict[str, Any]]:
    path = facts_dir / f"{system}.facts.jsonl"
    if not path.is_file():
        raise PrepError(f"missing extractor facts: {path}")
    try:
        verified = load_verified_facts(
            path,
            system=system,
            expected_commit=expected_commit,
            corpus_dir=corpus_dir,
        )
    except EvidenceValidationError as exc:
        raise PrepError(f"unverifiable extractor facts {path}: {exc}") from exc
    facts = [row for row in verified if row.get("predicate") == "CALLS_OPERATION"]
    required = {
        "path",
        "start_byte",
        "end_byte",
        "start_line",
        "end_line",
        "object",
        "code_role",
        "tier",
    }
    for row in facts:
        missing = required - row.keys()
        if missing:
            raise PrepError(f"{path}: CALLS_OPERATION missing {sorted(missing)}")
        if row.get("commit") != expected_commit:
            raise PrepError(
                f"{path}: CALLS_OPERATION commit {row.get('commit')!r} does not match "
                f"locked commit {expected_commit}"
            )
    return facts


def method_position(data: bytes, fact: dict[str, Any]) -> int:
    start = int(fact["start_byte"])
    end = int(fact["end_byte"])
    if start < 0 or end <= start or end > len(data):
        raise PrepError(
            f"invalid fact span {start}:{end} for {fact.get('path')} ({len(data)} bytes)"
        )
    method = str(fact["object"]).rsplit("/", 1)[-1].encode("utf-8")
    matches = list(re.finditer(rb"\b" + re.escape(method) + rb"[ \t\r\n]*\(", data[start:end]))
    if len(matches) != 1:
        raise PrepError(
            f"expected one {method.decode()} call in fact span {fact.get('path')}:{start}:{end}; "
            f"found {len(matches)}"
        )
    return start + matches[0].start()


def byte_coordinate(data: bytes, offset: int) -> tuple[int, int]:
    if offset < 0 or offset > len(data):
        raise PrepError(f"byte offset {offset} is outside a {len(data)}-byte blob")
    line = data.count(b"\n", 0, offset) + 1
    previous = data.rfind(b"\n", 0, offset)
    try:
        column = len(data[previous + 1 : offset].decode("utf-8", errors="strict")) + 1
    except UnicodeDecodeError as exc:
        raise PrepError(f"source prefix before byte {offset} is not valid UTF-8: {exc}") from exc
    return line, column


def build_precision_frame(
    system: str,
    root: Path,
    commit: str,
    facts: Sequence[dict[str, Any]],
    tracked_files: set[str],
) -> list[dict[str, Any]]:
    files: dict[str, bytes] = {}
    population: list[dict[str, Any]] = []
    seen_sites: dict[str, str] = {}
    seen_facts: set[tuple[Any, ...]] = set()
    for fact in facts:
        rel = relpath_of(str(fact["path"]), system, root)
        if rel not in tracked_files:
            raise PrepError(f"emitted fact does not resolve to a pinned tracked file: {system}:{rel}")
        if rel not in files:
            files[rel] = git_blob(root, commit, rel)
        data = files[rel]
        byte_offset = method_position(data, fact)
        line, column = byte_coordinate(data, byte_offset)
        method = str(fact["object"]).rsplit("/", 1)[-1]
        sid = site_id(system, rel, byte_offset, method)
        signature = (
            system,
            rel,
            int(fact["start_byte"]),
            int(fact["end_byte"]),
            str(fact["object"]),
            str(fact["code_role"]),
            str(fact["tier"]),
        )
        if signature in seen_facts:
            raise PrepError(f"duplicate CALLS_OPERATION fact: {signature}")
        seen_facts.add(signature)
        if sid in seen_sites:
            raise PrepError(
                f"multiple emitted facts map to one source invocation {sid}: "
                f"{seen_sites[sid]} and {fact['object']}"
            )
        seen_sites[sid] = str(fact["object"])
        role = str(fact["code_role"])
        if role not in CODE_ROLES:
            raise PrepError(
                f"CALLS_OPERATION has unsupported code_role {role!r}: {system}:{rel}"
            )
        population.append(
            {
                "site_id": sid,
                "system": system,
                "path": rel,
                "line": line,
                "start_line": int(fact["start_line"]),
                "end_line": int(fact["end_line"]),
                "column": column,
                "byte_offset": byte_offset,
                "method": method,
                "stratum": f"{system}|{role}",
                "role": role,
                "object": str(fact["object"]),
                "tier": str(fact["tier"]),
                "atom_id": fact.get("atom_id"),
            }
        )
    return population


def align_recall(
    recall: Sequence[dict[str, Any]], precision: Sequence[dict[str, Any]]
) -> None:
    by_site = {row["site_id"]: row for row in precision}
    for candidate in recall:
        fact = by_site.get(candidate["site_id"])
        candidate["extracted"] = fact is not None
        if fact is not None:
            candidate["object"] = fact["object"]
            candidate["role"] = fact["role"]
            candidate["tier"] = fact["tier"]
            candidate["atom_id"] = fact.get("atom_id")


def allocate(populations: dict[str, int], target: int, minimum: int) -> dict[str, int]:
    """Allocate a capped, deterministic stratified sample."""

    nonempty = {key: value for key, value in populations.items() if value > 0}
    target = min(max(0, target), sum(nonempty.values()))
    result = {key: 0 for key in populations}
    if not nonempty or target == 0:
        return result

    if target < len(nonempty):
        for key, _ in sorted(nonempty.items(), key=lambda item: (-item[1], item[0]))[:target]:
            result[key] = 1
        return result

    for key, size in nonempty.items():
        result[key] = min(size, minimum)
    while sum(result.values()) > target:
        key = max(
            (key for key in nonempty if result[key] > 1),
            key=lambda item: (result[item] / nonempty[item], item),
        )
        result[key] -= 1

    remaining = target - sum(result.values())
    while remaining:
        capacity = {key: nonempty[key] - result[key] for key in nonempty}
        available = {key: value for key, value in capacity.items() if value > 0}
        if not available:
            break
        total_capacity = sum(available.values())
        ideals = {key: remaining * value / total_capacity for key, value in available.items()}
        grants = {key: min(available[key], math.floor(ideals[key])) for key in available}
        granted = sum(grants.values())
        if granted == 0:
            key = max(available, key=lambda item: (ideals[item], available[item], item))
            grants[key] = 1
            granted = 1
        for key, count in grants.items():
            result[key] += count
        remaining -= granted
    return result


def group_by_stratum(rows: Sequence[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        grouped[str(row["stratum"])].append(row)
    return dict(grouped)


def select_by_system(
    population: Sequence[dict[str, Any]],
    per_system: int,
    minimum: int,
    seed: int,
    purpose: str,
    excluded: set[str] | None = None,
) -> list[dict[str, Any]]:
    excluded = excluded or set()
    selected: list[dict[str, Any]] = []
    for system in sorted({str(row["system"]) for row in population}):
        eligible = [
            row for row in population if row["system"] == system and row["site_id"] not in excluded
        ]
        grouped = group_by_stratum(eligible)
        allocation = allocate(
            {stratum: len(rows) for stratum, rows in grouped.items()}, per_system, minimum
        )
        for stratum, rows in grouped.items():
            ordered = sorted(
                rows,
                key=lambda row: (rank(seed, purpose, stratum, str(row["site_id"])), row["site_id"]),
            )
            selected.extend(ordered[: allocation[stratum]])
    return selected


def coordinate_row(row: dict[str, Any]) -> dict[str, Any]:
    return {
        "site_id": row["site_id"],
        "system": row["system"],
        "path": row["path"],
        "line": row["line"],
        "column": row["column"],
        "method": row["method"],
    }


def build_artifacts(args: argparse.Namespace) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    systems = tuple(item.strip() for item in args.systems.split(",") if item.strip())
    if not systems:
        raise PrepError("at least one system is required")
    if len(systems) != len(set(systems)):
        raise PrepError("systems must be unique")
    unknown = set(systems) - set(SYSTEMS)
    if unknown:
        raise PrepError(f"unknown systems: {sorted(unknown)}")

    sampling_config = {
        "recall_holdout_per_system": args.recall_holdout_per_system,
        "recall_dev_per_system": args.recall_dev_per_system,
        "precision_holdout_per_system": args.precision_holdout_per_system,
        "precision_dev_per_system": args.precision_dev_per_system,
        "recall_min_per_stratum": args.recall_min_per_stratum,
        "precision_min_per_stratum": args.precision_min_per_stratum,
    }
    if any(not isinstance(value, int) or value < 0 for value in sampling_config.values()):
        raise PrepError("sample sizes and per-stratum minima must be non-negative integers")
    gate_sampling = {
        "recall_holdout_per_system": GATE_RECALL_HOLDOUT_PER_SYSTEM,
        "recall_dev_per_system": GATE_RECALL_DEV_PER_SYSTEM,
        "precision_holdout_per_system": GATE_PRECISION_HOLDOUT_PER_SYSTEM,
        "precision_dev_per_system": GATE_PRECISION_DEV_PER_SYSTEM,
        "recall_min_per_stratum": GATE_RECALL_MIN_PER_STRATUM,
        "precision_min_per_stratum": GATE_PRECISION_MIN_PER_STRATUM,
    }
    gate_design_fixed = systems == SYSTEMS and args.seed == SEED and sampling_config == gate_sampling

    if gate_design_fixed:
        raise PrepError(
            "sealed Gate-2 generation is blocked before holdout selection: the independent "
            "server-registration recall frame required by the gate is not implemented; use "
            "a custom diagnostic configuration for development only"
        )

    burned, burn_ledger_binding = verified_burn_ledger(DEFAULT_BURN_LEDGER)
    commits = load_corpus_lock(args.corpus_lock, systems)
    recall_population: list[dict[str, Any]] = []
    precision_population: list[dict[str, Any]] = []
    fact_paths: list[Path] = []
    independent_method_counts: dict[str, int] = {}
    excluded_special_entries: dict[str, list[str]] = {}
    tracked_by_system: dict[str, list[str]] = {}
    for system in systems:
        root = args.corpus_dir / system
        if not root.is_dir():
            raise PrepError(f"missing corpus checkout: {root}")
        tracked_files, special_entries = pinned_tracked_files(root, commits[system])
        tracked_by_system[system] = tracked_files
        excluded_special_entries[system] = special_entries
        rpc_methods = discover_rpc_methods(root, commits[system], tracked_files)
        independent_method_counts[system] = len(rpc_methods)
        recall_rows = scan_recall_frame(
            system, root, commits[system], rpc_methods, tracked_files
        )
        facts = call_facts(system, args.facts_dir, commits[system], args.corpus_dir)
        precision_rows = build_precision_frame(
            system, root, commits[system], facts, set(tracked_files)
        )
        align_recall(recall_rows, precision_rows)
        recall_rows, _ = exclude_burned(recall_rows, burned)
        precision_rows, _ = exclude_burned(precision_rows, burned)
        recall_population.extend(recall_rows)
        precision_population.extend(precision_rows)
        fact_paths.append(args.facts_dir / f"{system}.facts.jsonl")

    recall_holdout = select_by_system(
        recall_population,
        args.recall_holdout_per_system,
        args.recall_min_per_stratum,
        args.seed,
        "recall-holdout",
    )
    precision_holdout = select_by_system(
        precision_population,
        args.precision_holdout_per_system,
        args.precision_min_per_stratum,
        args.seed,
        "precision-holdout",
    )
    holdout_ids = {row["site_id"] for row in recall_holdout + precision_holdout}

    recall_dev = select_by_system(
        recall_population,
        args.recall_dev_per_system,
        args.recall_min_per_stratum,
        args.seed,
        "recall-dev",
        holdout_ids,
    )
    precision_dev = select_by_system(
        precision_population,
        args.precision_dev_per_system,
        args.precision_min_per_stratum,
        args.seed,
        "precision-dev",
        holdout_ids,
    )

    selected: dict[str, dict[str, Any]] = {}

    def add_membership(frame: str, split: str, row: dict[str, Any]) -> None:
        sid = str(row["site_id"])
        entry = selected.setdefault(
            sid,
            {
                **coordinate_row(row),
                "byte_offset": row["byte_offset"],
                "split": split,
                "frames": {},
            },
        )
        if entry["split"] != split:
            raise PrepError(f"source site leaked across dev/holdout: {sid}")
        if frame in entry["frames"]:
            raise PrepError(f"duplicate {frame} membership for {sid}")
        membership = {"stratum": row["stratum"]}
        if frame == "recall":
            membership.update(
                extracted=bool(row["extracted"]),
                object=row.get("object"),
                role=row.get("role"),
                tier=row.get("tier"),
                atom_id=row.get("atom_id"),
            )
        else:
            membership.update(
                object=row["object"],
                role=row["role"],
                tier=row["tier"],
                atom_id=row.get("atom_id"),
            )
        entry["frames"][frame] = membership

    for frame, split, rows in (
        ("recall", "holdout", recall_holdout),
        ("precision", "holdout", precision_holdout),
        ("recall", "dev", recall_dev),
        ("precision", "dev", precision_dev),
    ):
        for row in rows:
            add_membership(frame, split, row)

    strata: dict[str, list[dict[str, Any]]] = {}
    population_by_frame = {"recall": recall_population, "precision": precision_population}
    for frame, population in population_by_frame.items():
        grouped = group_by_stratum(population)
        records: list[dict[str, Any]] = []
        for stratum, rows in sorted(grouped.items()):
            hold_n = sum(
                1
                for entry in selected.values()
                if entry["split"] == "holdout"
                and frame in entry["frames"]
                and entry["frames"][frame]["stratum"] == stratum
            )
            dev_n = sum(
                1
                for entry in selected.values()
                if entry["split"] == "dev"
                and frame in entry["frames"]
                and entry["frames"][frame]["stratum"] == stratum
            )
            n_population = len(rows)
            system, label = stratum.split("|", 1)
            records.append(
                {
                    "id": stratum,
                    "system": system,
                    "label": label,
                    "population_size": n_population,
                    "holdout_sample_size": hold_n,
                    "holdout_inclusion_probability": hold_n / n_population,
                    "dev_sample_size": dev_n,
                    "dev_inclusion_probability": None,
                }
            )
        strata[frame] = records

    dev_sites = sorted(
        (coordinate_row(row) for row in selected.values() if row["split"] == "dev"),
        key=lambda row: row["site_id"],
    )
    holdout_sites = sorted(
        (coordinate_row(row) for row in selected.values() if row["split"] == "holdout"),
        key=lambda row: row["site_id"],
    )
    keys = sorted(selected.values(), key=lambda row: row["site_id"])
    validate_rows(dev_sites, holdout_sites, keys, strata, burned)
    selected_total = len(dev_sites) + len(holdout_sites)
    blind_fraction = len(holdout_sites) / selected_total if selected_total else 0.0
    if gate_design_fixed and blind_fraction < MIN_BLIND_HOLDOUT_FRACTION:
        raise PrepError(
            f"gate design has only {blind_fraction:.2%} blind holdout; "
            f"minimum is {MIN_BLIND_HOLDOUT_FRACTION:.0%}"
        )

    # Detect concurrent checkout mutation after every blob and fact has been
    # consumed.  The source itself came from Git objects; this postflight also
    # makes the clean-check provenance stable for the complete run.
    for system in systems:
        post_files, post_special = pinned_tracked_files(
            args.corpus_dir / system, commits[system]
        )
        if (
            post_files != tracked_by_system[system]
            or post_special != excluded_special_entries[system]
        ):
            raise PrepError(f"{system}: pinned tree changed during frame construction")

    manifest = {
        "schema": SCHEMA,
        "seed": args.seed,
        "systems": list(systems),
        "gate_design_fixed": gate_design_fixed,
        "sampling_config": sampling_config,
        "burn_ledger": burn_ledger_binding,
        "coordinate_only_sites": True,
        "holdout": {
            "blind": True,
            "selection": "precommitted SHA-256 rank within every declared stratum",
            "design_limitation": (
                "deterministic hash ranking is reproducible but is not physical randomness; "
                "gate use requires the fixed seed and configuration committed before labeling"
            ),
            "inference": "holdout only; development rows are tuning diagnostics",
            "unique_dev_sites": len(dev_sites),
            "unique_holdout_sites": len(holdout_sites),
            "blind_fraction": blind_fraction,
            "minimum_blind_fraction": MIN_BLIND_HOLDOUT_FRACTION,
        },
        "benchmark_requirements": {
            "minimum_labeled_positives": 200,
            "minimum_labeled_hard_negatives": 100,
            "evaluated_by_scorer": True,
        },
        "protocol_coverage": {
            "client_calls": "measured",
            "registration": "protocol_missing",
            "registration_predicate": "IMPLEMENTS_SERVICE",
            "gate_blocked_until_registration_measured": True,
        },
        "frames": {
            "recall": {
                "definition": (
                    "all exported selector invocations in non-comment/non-literal Go source; "
                    "manual eligibility determines direct Go/gRPC consumer calls; committed "
                    "legacy burn-ledger coordinates are excluded before sampling"
                ),
                "extractor_independent": True,
                "population_size": len(recall_population),
                "strata": strata["recall"],
            },
            "precision": {
                "definition": (
                    "all emitted CALLS_OPERATION facts after committed legacy burn-ledger "
                    "coordinate exclusion"
                ),
                "extractor_independent": False,
                "population_size": len(precision_population),
                "strata": strata["precision"],
            },
        },
        "independent_rpc_method_counts": independent_method_counts,
        "provenance": {
            "locked_commits": {system: commits[system] for system in systems},
            "excluded_symlink_gitlink_entries": excluded_special_entries,
            "corpus_lock_sha256": (
                sha256_file(args.corpus_lock)
                if args.corpus_lock.is_file()
                else None
            ),
            "facts_sha256": {path.name: sha256_file(path) for path in fact_paths},
            "prep_script_sha256": sha256_file(Path(__file__)),
            "fact_verifier_sha256": sha256_file(BASE / "gate34_common.py"),
        },
    }
    return manifest, dev_sites, holdout_sites, keys


def validate_rows(
    dev_sites: Sequence[dict[str, Any]],
    holdout_sites: Sequence[dict[str, Any]],
    keys: Sequence[dict[str, Any]],
    strata: dict[str, list[dict[str, Any]]],
    burned: Sequence[dict[str, Any]] = (),
) -> None:
    def unique_ids(rows: Sequence[dict[str, Any]], name: str) -> set[str]:
        ids = [str(row.get("site_id")) for row in rows]
        if len(ids) != len(set(ids)):
            raise PrepError(f"duplicate site_id in {name}")
        return set(ids)

    dev_ids = unique_ids(dev_sites, "sites.dev")
    holdout_ids = unique_ids(holdout_sites, "sites.holdout")
    key_ids = unique_ids(keys, "key")
    overlap = dev_ids & holdout_ids
    if overlap:
        raise PrepError(f"dev/holdout site leakage: {sorted(overlap)[:3]}")
    if key_ids != dev_ids | holdout_ids:
        raise PrepError("key IDs do not equal the union of site IDs")

    coordinates: dict[tuple[Any, ...], str] = {}
    for row in list(dev_sites) + list(holdout_sites):
        candidate = {
            "system": row["system"],
            "path": row["path"],
            "start_line": row["line"],
            "end_line": row["line"],
        }
        if coordinate_burned(candidate, burned):
            raise PrepError(f"selected site overlaps the legacy burn ledger: {row['site_id']}")
        coordinate = tuple(row[field] for field in ("system", "path", "line", "column", "method"))
        prior = coordinates.setdefault(coordinate, str(row["site_id"]))
        if prior != row["site_id"]:
            raise PrepError(f"coordinate has multiple IDs: {coordinate}")

    expected = {
        (frame, record["id"]): record
        for frame, records in strata.items()
        for record in records
    }
    observed: dict[tuple[str, str, str], int] = defaultdict(int)
    for row in keys:
        split = str(row["split"])
        expected_split = "dev" if row["site_id"] in dev_ids else "holdout"
        if split != expected_split:
            raise PrepError(f"key split mismatch for {row['site_id']}")
        for frame, membership in row["frames"].items():
            stratum = str(membership["stratum"])
            if (frame, stratum) not in expected:
                raise PrepError(f"unknown {frame} stratum {stratum}")
            observed[(frame, stratum, split)] += 1
    for (frame, stratum), record in expected.items():
        if observed[(frame, stratum, "holdout")] != record["holdout_sample_size"]:
            raise PrepError(f"holdout count mismatch for {frame}/{stratum}")
        if observed[(frame, stratum, "dev")] != record["dev_sample_size"]:
            raise PrepError(f"dev count mismatch for {frame}/{stratum}")


def publish_artifacts(
    output: Path,
    manifest: dict[str, Any],
    dev_sites: Sequence[dict[str, Any]],
    holdout_sites: Sequence[dict[str, Any]],
    keys: Sequence[dict[str, Any]],
    force: bool,
) -> None:
    paths = {
        "sites.dev.jsonl": dev_sites,
        "sites.holdout.jsonl": holdout_sites,
        "key.jsonl": keys,
    }
    existing = [output / name for name in (*paths, "manifest.json") if (output / name).exists()]
    if existing and not force:
        raise PrepError(
            f"refusing to replace {', '.join(str(path) for path in existing)}; pass --force"
        )
    output.mkdir(parents=True, exist_ok=True)
    for name, rows in paths.items():
        write_jsonl(output / name, rows)
    manifest["artifacts_sha256"] = {
        name: sha256_file(output / name) for name in paths
    }
    atomic_write(output / "manifest.json", json.dumps(manifest, indent=2, sort_keys=True) + "\n")


def print_summary(
    manifest: dict[str, Any], dev: Sequence[dict[str, Any]], holdout: Sequence[dict[str, Any]]
) -> None:
    print(f"schema={manifest['schema']} seed={manifest['seed']}")
    for frame in ("recall", "precision"):
        detail = manifest["frames"][frame]
        print(f"{frame}: population={detail['population_size']}")
        for stratum in detail["strata"]:
            print(
                f"  {stratum['id']}: N={stratum['population_size']} "
                f"holdout={stratum['holdout_sample_size']} "
                f"pi={stratum['holdout_inclusion_probability']:.8f} "
                f"dev={stratum['dev_sample_size']}"
            )
    print(f"unique label sites: dev={len(dev)} holdout={len(holdout)}")


def materialize_context(args: argparse.Namespace) -> None:
    artifact_dir = args.artifact_dir
    manifest_path = artifact_dir / "manifest.json"
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load {manifest_path}: {exc}") from exc
    if not isinstance(manifest, dict) or manifest.get("schema") != SCHEMA:
        raise PrepError(f"{manifest_path}: unsupported Gate 2 artifact schema")

    artifact_paths = {
        "sites.dev.jsonl": artifact_dir / "sites.dev.jsonl",
        "sites.holdout.jsonl": artifact_dir / "sites.holdout.jsonl",
        "key.jsonl": artifact_dir / "key.jsonl",
    }
    expected_hashes = manifest.get("artifacts_sha256")
    if not isinstance(expected_hashes, dict):
        raise PrepError(f"{manifest_path}: missing artifact digests")
    for name, path in artifact_paths.items():
        if not path.is_file() or expected_hashes.get(name) != sha256_file(path):
            raise PrepError(f"artifact digest mismatch: {path}")

    systems = manifest.get("systems")
    if (
        not isinstance(systems, list)
        or not systems
        or len(systems) != len(set(systems))
        or not all(isinstance(system, str) and system in SYSTEMS for system in systems)
    ):
        raise PrepError("manifest has an invalid system list")
    commits = load_corpus_lock(args.corpus_lock, systems)
    provenance = manifest.get("provenance")
    if not isinstance(provenance, dict):
        raise PrepError("manifest has no provenance")
    if provenance.get("locked_commits") != {system: commits[system] for system in systems}:
        raise PrepError("manifest commits do not match corpus.lock")
    if provenance.get("corpus_lock_sha256") != sha256_file(args.corpus_lock):
        raise PrepError("manifest corpus.lock digest is stale")
    if provenance.get("prep_script_sha256") != sha256_file(Path(__file__)):
        raise PrepError("manifest was not produced by the current label_prep.py")
    if provenance.get("fact_verifier_sha256") != sha256_file(BASE / "gate34_common.py"):
        raise PrepError("manifest was not produced by the current evidence verifier")

    dev = load_jsonl(artifact_paths["sites.dev.jsonl"])
    holdout = load_jsonl(artifact_paths["sites.holdout.jsonl"])
    keys = load_jsonl(artifact_paths["key.jsonl"])
    strata = {
        frame: manifest.get("frames", {}).get(frame, {}).get("strata", [])
        for frame in ("recall", "precision")
    }
    validate_rows(dev, holdout, keys, strata)
    key_by_id = {str(row["site_id"]): row for row in keys}
    allowed_site_fields = {"site_id", "system", "path", "line", "column", "method"}
    tracked: dict[str, set[str]] = {}
    blobs: dict[tuple[str, str], bytes] = {}
    for system in systems:
        files, _ = pinned_tracked_files(args.corpus_dir / system, commits[system])
        tracked[system] = set(files)

    for row in [*dev, *holdout]:
        if set(row) != allowed_site_fields:
            raise PrepError(f"{row.get('site_id')}: context source is not coordinate-only")
        sid = str(row["site_id"])
        hidden = key_by_id[sid]
        for field in allowed_site_fields - {"site_id"}:
            if hidden.get(field) != row[field]:
                raise PrepError(f"{sid}: key/site {field} mismatch")
        system = str(row["system"])
        rel = str(row["path"])
        if system not in tracked or rel not in tracked[system]:
            raise PrepError(f"{sid}: path is not a pinned regular blob: {system}:{rel}")
        blob = blobs.setdefault(
            (system, rel), git_blob(args.corpus_dir / system, commits[system], rel)
        )
        offset = hidden.get("byte_offset")
        method = str(row["method"])
        if not isinstance(offset, int) or blob[offset : offset + len(method.encode())] != method.encode():
            raise PrepError(f"{sid}: byte offset does not resolve to method {method}")
        line, column = byte_coordinate(blob, offset)
        if (row["line"], row["column"]) != (line, column):
            raise PrepError(f"{sid}: line/column do not resolve to the pinned blob")
        if sid != site_id(system, rel, offset, method):
            raise PrepError(f"{sid}: site ID does not bind its pinned coordinate")

    output = args.output_dir
    targets = [output / "sites.dev.local.jsonl", output / "sites.holdout.local.jsonl"]
    if any(path.exists() for path in targets) and not args.force:
        raise PrepError(f"refusing to replace local context under {output}; pass --force")

    def enrich(row: dict[str, Any]) -> dict[str, Any]:
        system = str(row["system"])
        rel = str(row["path"])
        try:
            lines = blobs[(system, rel)].decode("utf-8", errors="strict").splitlines()
        except UnicodeDecodeError as exc:
            raise PrepError(f"cannot decode pinned context {system}:{rel}: {exc}") from exc
        line = int(row["line"])
        lo = max(0, line - 1 - args.context_lines)
        hi = min(len(lines), line + args.context_lines)
        context = "\n".join(f"{index + 1:6d}| {lines[index]}" for index in range(lo, hi))
        return {**row, "context": context}

    output.mkdir(parents=True, exist_ok=True)
    write_jsonl(targets[0], (enrich(row) for row in dev))
    write_jsonl(targets[1], (enrich(row) for row in holdout))
    for system in systems:
        pinned_tracked_files(args.corpus_dir / system, commits[system])
    print(f"local context written under ignored path: {output}")


def run_self_test() -> None:
    sample = '''package p
func f(client Client) {
    client.Real(ctx, &pb.RealRequest{})
    // client.Commented(ctx)
    _ = "client.StringCall(ctx)"
    client.\n        Wrapped(ctx, req)
    x.localCall()
    `raw.External(ctx)`
}\n'''
    masked = mask_go_source(sample)
    methods = [match.group(1) for match in SELECTOR_CALL.finditer(masked)]
    assert methods == ["Real", "Wrapped"], methods
    chars, bytes_ = source_offsets(sample)
    match = next(SELECTOR_CALL.finditer(masked))
    line, column, offset = coordinate_at(sample, match.start(1), chars, bytes_)
    assert (line, column) == (3, 12)
    assert sample.encode("utf-8")[offset : offset + 4] == b"Real"

    populations = {"a": 100, "b": 20, "c": 2}
    allocation = allocate(populations, 30, 3)
    assert sum(allocation.values()) == 30
    assert all(0 <= allocation[key] <= populations[key] for key in populations)
    assert allocation == allocate(populations, 30, 3)

    burned = [{"system": "s", "path": "p.go", "start_line": 4, "end_line": 6}]
    burn_candidates = [
        {"site_id": "before", "system": "s", "path": "p.go", "start_line": 3, "end_line": 3},
        {"site_id": "point", "system": "s", "path": "p.go", "start_line": 5, "end_line": 5},
        {"site_id": "span", "system": "s", "path": "p.go", "start_line": 6, "end_line": 8},
        {"site_id": "other", "system": "s", "path": "q.go", "start_line": 5, "end_line": 5},
    ]
    unburned, excluded = exclude_burned(burn_candidates, burned)
    assert excluded == 2
    assert [row["site_id"] for row in unburned] == ["before", "other"]

    sid = site_id("s", "p.go", 10, "Call")
    assert sid == site_id("s", "p.go", 10, "Call")
    dev = [{"site_id": sid, "system": "s", "path": "p.go", "line": 1, "column": 11, "method": "Call"}]
    hold = [{"site_id": "g2s_other", "system": "s", "path": "q.go", "line": 2, "column": 1, "method": "Other"}]
    keys = [
        {**dev[0], "byte_offset": 10, "split": "dev", "frames": {"recall": {"stratum": "s|risk"}}},
        {**hold[0], "byte_offset": 20, "split": "holdout", "frames": {"recall": {"stratum": "s|risk"}}},
    ]
    strata = {
        "recall": [
            {
                "id": "s|risk",
                "population_size": 10,
                "holdout_sample_size": 1,
                "dev_sample_size": 1,
            }
        ]
    }
    validate_rows(dev, hold, keys, strata)
    try:
        validate_rows(dev, dev, keys, strata)
    except PrepError:
        pass
    else:
        raise AssertionError("dev/holdout overlap was not rejected")
    try:
        validate_rows(dev, hold, keys, strata, [
            {"system": "s", "path": "p.go", "start_line": 1, "end_line": 1}
        ])
    except PrepError:
        pass
    else:
        raise AssertionError("burn-ledger overlap was not rejected")
    print("label_prep self-test: PASS")


def parser() -> argparse.ArgumentParser:
    top = argparse.ArgumentParser(description=__doc__)
    commands = top.add_subparsers(dest="command", required=True)

    prepare = commands.add_parser("prepare", help="enumerate frames and create coordinate artifacts")
    prepare.add_argument("--corpus-dir", type=Path, default=DEFAULT_CORPUS)
    prepare.add_argument("--corpus-lock", type=Path, default=DEFAULT_LOCK)
    prepare.add_argument("--facts-dir", type=Path, default=DEFAULT_FACTS)
    prepare.add_argument("--output-dir", type=Path, default=DEFAULT_ARTIFACTS)
    prepare.add_argument("--systems", default=",".join(SYSTEMS))
    prepare.add_argument("--seed", type=int, default=SEED)
    prepare.add_argument(
        "--recall-holdout-per-system", type=int, default=GATE_RECALL_HOLDOUT_PER_SYSTEM
    )
    prepare.add_argument(
        "--recall-dev-per-system", type=int, default=GATE_RECALL_DEV_PER_SYSTEM
    )
    prepare.add_argument(
        "--precision-holdout-per-system", type=int, default=GATE_PRECISION_HOLDOUT_PER_SYSTEM
    )
    prepare.add_argument(
        "--precision-dev-per-system", type=int, default=GATE_PRECISION_DEV_PER_SYSTEM
    )
    prepare.add_argument(
        "--recall-min-per-stratum", type=int, default=GATE_RECALL_MIN_PER_STRATUM
    )
    prepare.add_argument(
        "--precision-min-per-stratum", type=int, default=GATE_PRECISION_MIN_PER_STRATUM
    )
    prepare.add_argument("--dry-run", action="store_true")
    prepare.add_argument("--force", action="store_true")

    context = commands.add_parser(
        "materialize-context", help="create ignored local labeler context from coordinates"
    )
    context.add_argument("--artifact-dir", type=Path, default=DEFAULT_ARTIFACTS)
    context.add_argument("--corpus-dir", type=Path, default=DEFAULT_CORPUS)
    context.add_argument("--corpus-lock", type=Path, default=DEFAULT_LOCK)
    context.add_argument("--output-dir", type=Path, default=DEFAULT_CONTEXT)
    context.add_argument("--context-lines", type=int, default=15)
    context.add_argument("--force", action="store_true")

    commands.add_parser("self-test", help="run deterministic invariant tests")
    return top


def main(argv: Sequence[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv == ["--self-test"]:
        argv = ["self-test"]
    if not argv or argv[0].startswith("-"):
        argv.insert(0, "prepare")
    args = parser().parse_args(argv)
    try:
        if args.command == "self-test":
            run_self_test()
            return 0
        if args.command == "materialize-context":
            materialize_context(args)
            return 0
        manifest, dev, holdout, keys = build_artifacts(args)
        print_summary(manifest, dev, holdout)
        if args.dry_run:
            print("dry run: no artifacts written")
            return 0
        publish_artifacts(args.output_dir, manifest, dev, holdout, keys, args.force)
        print(f"coordinate-only artifacts written to {args.output_dir}")
        print(
            "materialize local source context with: "
            f"{Path(__file__).name} materialize-context --artifact-dir {args.output_dir}"
        )
        return 0
    except PrepError as exc:
        print(f"label_prep: ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
