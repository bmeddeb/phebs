#!/usr/bin/env python3
"""Build the sealed Gate 2 probability sample without exposing verdicts.

Client-call and server-registration recall use extractor-independent source
oracles; precision samples the complete emitted fact populations. A fifth
frame samples emitted references by an independently source-derived expected
code role. Holdout ranks use post-commitment externally auditable entropy,
domain-separated per frame. Development units are selected only after every
holdout coordinate is excluded.

All frames preserve the committed legacy burn history. The hidden key records
frame membership and extractor alignment; labeler-facing files contain only
coordinates. Source context is materialized separately under the ignored
``out`` tree.
"""

from __future__ import annotations

import argparse
import bisect
import datetime as dt
import hashlib
import json
import math
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections import Counter, defaultdict
from fractions import Fraction
from pathlib import Path
from typing import Any, Iterable, Iterator, Sequence

from gate34_common import (
    ValidationError as EvidenceValidationError,
    build_burn_ledger,
    coordinate_burned,
    load_burn_ledger,
    load_burn_ledger_cohorts,
    load_facts as load_verified_facts,
)
from label_protocol import (
    LabelProtocolError,
    build_github_initial_gist_receipt,
    build_label_commitment,
    canonical_label_commitment_document,
    freeze_bytes_exclusive,
    freeze_labels as freeze_canonical_labels,
    github_json_fetcher,
    nist_beacon_json_fetcher,
    validate_github_gist_receipt,
    verify_github_initial_gist_receipt,
)
from label_adjudication import (
    AdjudicationError,
    adjudicate_labels,
    validate_assignment_manifest,
)
from reviewer_kits import (
    ReviewerKitError,
    build_reviewer_kits,
    freeze_reviewer_kits,
    load_source_partitions,
    sha256_bytes as reviewer_sha256_bytes,
)


BASE = Path(__file__).resolve().parent
REPO_ROOT = BASE.parent.parent
TYPED_CALL_ORACLE = BASE / "typedcalloracle"
DEFAULT_CORPUS = BASE / "corpus"
DEFAULT_FACTS = BASE / "out"
DEFAULT_ARTIFACTS = BASE / "labeling" / "g2-v3"
DEFAULT_CONTEXT = BASE / "out" / "gate2-label-context"
DEFAULT_LOCK = BASE / "corpus.lock.json"
DEFAULT_BURN_LEDGER = BASE / "labeling" / "burn-ledger.json"
SEALED_CLAIM_ROOT = BASE / "labeling"
SYSTEMS = ("online-boutique", "dapr", "temporal", "loki")
SCHEMA = "t111-gate2-probability-sample-v3"
SEED_RECORD_SCHEMA = "t111-gate2-audit-seed-v3"
NIST_BEACON_API_ROOT = "https://beacon.nist.gov/beacon/2.0"
COMMITMENT_ACTIVATION_LEAD = dt.timedelta(minutes=30)
LEGACY_DIAGNOSTIC_SEED = 111
# Compatibility name imported by the legacy diagnostic scorer path.
SEED = LEGACY_DIAGNOSTIC_SEED
GATE_RECALL_HOLDOUT_PER_SYSTEM = 200
GATE_RECALL_DEV_PER_SYSTEM = 120
GATE_PRECISION_HOLDOUT_PER_SYSTEM = 800
GATE_PRECISION_DEV_PER_SYSTEM = 60
GATE_RECALL_MIN_PER_STRATUM = 30
GATE_PRECISION_MIN_PER_STRATUM = 15
GATE_REGISTRATION_RECALL_HOLDOUT_PER_SYSTEM = 1_000_000
GATE_REGISTRATION_RECALL_DEV_PER_SYSTEM = 0
GATE_REGISTRATION_PRECISION_HOLDOUT_PER_SYSTEM = 1_000_000
GATE_REGISTRATION_PRECISION_DEV_PER_SYSTEM = 0
GATE_REGISTRATION_RECALL_MIN_PER_STRATUM = 10
GATE_REGISTRATION_PRECISION_MIN_PER_STRATUM = 10
GATE_ROLE_HOLDOUT_PER_ROLE = 20
GATE_ROLE_DEV_PER_ROLE = 10
MIN_BLIND_HOLDOUT_FRACTION = 0.30
CODE_ROLES = {"production", "test", "mock", "generated", "vendor"}
FRAME_CALL_RECALL = "recall"
FRAME_CALL_PRECISION = "precision"
FRAME_REGISTRATION_RECALL = "registration_recall"
FRAME_REGISTRATION_PRECISION = "registration_precision"
FRAME_ROLE = "role"
FRAMES = (
    FRAME_CALL_RECALL,
    FRAME_CALL_PRECISION,
    FRAME_REGISTRATION_RECALL,
    FRAME_REGISTRATION_PRECISION,
    FRAME_ROLE,
)
GATE_CONFIDENCE = Fraction(95, 100)
GATE_PRECISION_THRESHOLD = Fraction(98, 100)
GATE_RECALL_THRESHOLD = Fraction(90, 100)
GATE_FIXTURE_PRECISION_THRESHOLD = Fraction(90, 100)
MIN_POSITIVES = 200
MIN_HARD_NEGATIVES = 100

# The legacy ledger deliberately remains byte-for-byte unchanged.  This map
# scopes those 700 exposed coordinates to the exact populations in which they
# were shown to labelers.  A refreshed commit is a new source population; the
# old coordinates remain auditable but are not silently deleted from it.
LEGACY_BURN_COMMITS = {
    "online-boutique": "ea0fcf429ff59d861867370d75586095dd253813",
    "dapr": "1991f9484f4dd47502e7329c838ceae42f6ca179",
    "temporal": "1d4be84b3771a03e870f09db74bee9e437469b0d",
    "loki": "fbf98eb42291cd09cf019956a1812e35dbbadbbf",
}

SELECTOR_CALL = re.compile(
    r"\.[ \t\r\n]*([A-Z][A-Za-z0-9_]*)[ \t\r\n]*\("
)
PROTO_RPC = re.compile(r"\brpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")
CLIENT_INTERFACE = re.compile(
    r"type\s+[A-Za-z_][A-Za-z0-9_]*Client\s+interface\s*\{(.*?)\n\}", re.S
)
INTERFACE_METHOD = re.compile(r"^\s*([A-Z][A-Za-z0-9_]*)\s*\(", re.M)
WIRE_METHOD = re.compile(r'"/[A-Za-z0-9_.-]+/([A-Za-z_][A-Za-z0-9_]*)"')
REGISTER_CALL = re.compile(
    r"(?<![A-Za-z0-9_])(?:[A-Za-z_][A-Za-z0-9_]*[ \t\r\n]*\.[ \t\r\n]*)?"
    r"(Register[A-Z][A-Za-z0-9_]*Server)[ \t\r\n]*\("
)
DIRECT_REGISTER_SERVICE_CALL = re.compile(
    r"\.[ \t\r\n]*(RegisterService)[ \t\r\n]*\("
)
FULL_SERVICE_RE = re.compile(
    r"^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$"
)
FULL_OPERATION_RE = re.compile(
    r"^/?[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+"
    r"/[A-Za-z_][A-Za-z0-9_]*$"
)


class PrepError(RuntimeError):
    """An invariant failed; no measurement artifact should be published."""


def gate_decision_rule() -> dict[str, Any]:
    return {
        "joint_confidence": "95/100",
        "overall_precision_threshold": "98/100",
        "population_recall_threshold": "90/100",
        "per_fixture_precision_threshold": "90/100",
        "minimum_labeled_positives": MIN_POSITIVES,
        "minimum_labeled_hard_negatives": MIN_HARD_NEGATIVES,
        "minimum_blind_holdout_fraction": MIN_BLIND_HOLDOUT_FRACTION,
        "role_rule": "all-five-nonempty-and-exact",
    }


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


def load_corpus_entries(path: Path, systems: Sequence[str]) -> dict[str, dict[str, Any]]:
    try:
        rows = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load corpus lock {path}: {exc}") from exc
    if not isinstance(rows, list):
        raise PrepError(f"{path}: expected a list")
    entries: dict[str, dict[str, Any]] = {}
    for row in rows:
        if not isinstance(row, dict) or not isinstance(row.get("name"), str):
            raise PrepError(f"{path}: invalid lock entry")
        name = row["name"]
        commit = row.get("commit")
        if name in entries:
            raise PrepError(f"{path}: duplicate lock entry {name}")
        if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
            raise PrepError(f"{path}: {name} commit must be a full lowercase 40-hex ID")
        raw_exclusions = row.get("excluded_gitlinks", [])
        if not isinstance(raw_exclusions, list):
            raise PrepError(f"{path}: {name} excluded_gitlinks must be a list")
        exclusions: list[dict[str, str]] = []
        seen_paths: set[str] = set()
        for exclusion in raw_exclusions:
            if not isinstance(exclusion, dict) or set(exclusion) != {"path", "object_id", "reason"}:
                raise PrepError(
                    f"{path}: {name} excluded_gitlinks entries require path, object_id, reason"
                )
            rel = exclusion.get("path")
            object_id = exclusion.get("object_id")
            reason = exclusion.get("reason")
            if (
                not isinstance(rel, str)
                or not rel
                or rel.startswith("/")
                or ".." in Path(rel).parts
                or rel in seen_paths
            ):
                raise PrepError(f"{path}: {name} has invalid/duplicate excluded gitlink {rel!r}")
            if not isinstance(object_id, str) or not re.fullmatch(r"[0-9a-f]{40}", object_id):
                raise PrepError(f"{path}: {name}:{rel} object_id must be full lowercase 40-hex")
            if not isinstance(reason, str) or not reason.strip():
                raise PrepError(f"{path}: {name}:{rel} exclusion needs a nonempty reason")
            seen_paths.add(rel)
            exclusions.append({"path": rel, "object_id": object_id, "reason": reason.strip()})
        tags = row.get("go_build_tags", [])
        if (
            not isinstance(tags, list)
            or not all(isinstance(tag, str) and re.fullmatch(r"[A-Za-z0-9_.-]+", tag) for tag in tags)
            or len(tags) != len(set(tags))
        ):
            raise PrepError(f"{path}: {name} go_build_tags must be unique safe strings")
        entries[name] = {
            "commit": commit,
            "excluded_gitlinks": sorted(exclusions, key=lambda item: item["path"]),
            "go_build_tags": sorted(tags),
        }
    missing = set(systems) - set(entries)
    if missing:
        raise PrepError(f"{path}: missing lock entries {sorted(missing)}")
    return {system: entries[system] for system in systems}


def load_corpus_lock(path: Path, systems: Sequence[str]) -> dict[str, str]:
    """Compatibility projection used by callers that need commits only."""

    return {
        system: str(entry["commit"])
        for system, entry in load_corpus_entries(path, systems).items()
    }


def pinned_tracked_files(
    root: Path,
    expected_commit: str,
    excluded_gitlinks: Sequence[dict[str, str]] = (),
) -> tuple[list[str], list[dict[str, str]]]:
    """Verify a clean pin and return regular tracked blobs.

    Gitlinks are accepted only when their exact path, object ID, and reviewed
    exclusion rationale are locked. A symlinked Go/proto path is rejected;
    other symlinks are recorded but cannot enter a frame that reads regular Git
    blobs only.
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
    special: list[dict[str, str]] = []
    declared = {item["path"]: item for item in excluded_gitlinks}
    observed_exclusions: set[str] = set()
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        metadata, separator, raw_path = entry.partition(b"\t")
        if not separator:
            raise PrepError(f"{root}: malformed git tree entry")
        fields = metadata.decode("ascii").split()
        if len(fields) != 3:
            raise PrepError(f"{root}: malformed git tree metadata")
        mode, object_type, object_id = fields
        rel = raw_path.decode("utf-8", errors="strict")
        if mode in ("100644", "100755") and object_type == "blob":
            regular.append(rel)
        elif mode == "160000":
            exclusion = declared.get(rel)
            if exclusion is None:
                raise PrepError(f"{root}: undeclared gitlink makes the source frame incomplete: {rel}")
            if exclusion["object_id"] != object_id:
                raise PrepError(
                    f"{root}: excluded gitlink {rel} object {object_id} does not match "
                    f"lock {exclusion['object_id']}"
                )
            observed_exclusions.add(rel)
            special.append({"kind": "gitlink", **exclusion})
        elif mode == "120000":
            if rel.endswith((".go", ".proto")):
                raise PrepError(f"{root}: symlinked source makes the source frame incomplete: {rel}")
            special.append({"kind": "symlink", "path": rel, "object_id": object_id})
        else:
            raise PrepError(f"{root}: unsupported tracked tree entry {mode} {object_type} {rel}")
    missing_exclusions = set(declared) - observed_exclusions
    if missing_exclusions:
        raise PrepError(
            f"{root}: declared excluded gitlinks are absent from the pinned tree: "
            f"{sorted(missing_exclusions)}"
        )
    return regular, sorted(special, key=lambda item: (item["kind"], item["path"]))


def git_blob(root: Path, commit: str, rel: str) -> bytes:
    """Read a pinned regular blob without consulting the mutable worktree."""

    if not rel or rel.startswith("/") or ".." in Path(rel).parts:
        raise PrepError(f"unsafe pinned path: {rel!r}")
    return git(root, "cat-file", "blob", f"{commit}:{rel}")


def git_blobs(root: Path, commit: str, paths: Sequence[str]) -> Iterator[tuple[str, bytes]]:
    """Yield pinned blobs from one NUL-safe, bounded-memory cat-file process."""

    ordered = list(paths)
    if len(ordered) != len(set(ordered)):
        raise PrepError("duplicate path in pinned blob batch")
    for rel in ordered:
        if not rel or rel.startswith("/") or ".." in Path(rel).parts or "\x00" in rel:
            raise PrepError(f"unsafe pinned path: {rel!r}")

    def read_header(stream: Any, rel: str) -> bytes:
        header = bytearray()
        while True:
            try:
                byte = stream.read(1)
            except OSError as exc:
                raise PrepError(f"cannot read Git batch header for {root}:{rel}: {exc}") from exc
            if not byte:
                raise PrepError(f"truncated Git batch header for {root}:{rel}")
            if byte == b"\0":
                return bytes(header)
            header.extend(byte)

    def read_content(stream: Any, rel: str, size: int) -> bytes:
        chunks: list[bytes] = []
        remaining = size
        while remaining:
            try:
                chunk = stream.read(min(remaining, 1024 * 1024))
            except OSError as exc:
                raise PrepError(f"cannot read Git batch content for {root}:{rel}: {exc}") from exc
            if not chunk:
                raise PrepError(f"truncated Git batch content for {root}:{rel}")
            chunks.append(chunk)
            remaining -= len(chunk)
        try:
            terminator = stream.read(1)
        except OSError as exc:
            raise PrepError(f"cannot read Git batch content for {root}:{rel}: {exc}") from exc
        if terminator != b"\0":
            raise PrepError(f"truncated Git batch content for {root}:{rel}")
        return b"".join(chunks)

    def stream_blobs() -> Iterator[tuple[str, bytes]]:
        with tempfile.TemporaryFile() as stderr:
            try:
                process = subprocess.Popen(
                    ["git", "-C", str(root), "cat-file", "--batch", "-Z"],
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=stderr,
                )
            except OSError as exc:
                raise PrepError(f"cannot batch-read Git blobs for {root}: {exc}") from exc
            if process.stdin is None or process.stdout is None:
                process.kill()
                process.wait()
                raise PrepError(f"cannot open Git batch pipes for {root}")

            try:
                for rel in ordered:
                    request = f"{commit}:{rel}".encode("utf-8") + b"\0"
                    try:
                        written = process.stdin.write(request)
                        process.stdin.flush()
                    except OSError as exc:
                        raise PrepError(f"cannot request Git blob for {root}:{rel}: {exc}") from exc
                    if written != len(request):
                        raise PrepError(f"truncated Git batch request for {root}:{rel}")

                    fields = read_header(process.stdout, rel).split()
                    if len(fields) != 3 or fields[1] != b"blob" or not fields[2].isdigit():
                        raise PrepError(f"Git batch did not resolve a regular blob for {root}:{rel}")
                    yield rel, read_content(process.stdout, rel, int(fields[2]))

                try:
                    process.stdin.close()
                except OSError as exc:
                    raise PrepError(f"cannot close Git batch input for {root}: {exc}") from exc
                try:
                    trailing = process.stdout.read(1)
                except OSError as exc:
                    raise PrepError(f"cannot finish Git batch read for {root}: {exc}") from exc
                if trailing:
                    process.kill()
                    process.wait()
                    raise PrepError(f"Git batch emitted trailing data for {root}")
                returncode = process.wait()
                stderr.seek(0)
                detail = stderr.read().decode("utf-8", errors="replace").strip()
                if returncode:
                    raise PrepError(f"git cat-file batch failed for {root}: {detail}")
            finally:
                if not process.stdin.closed:
                    try:
                        process.stdin.close()
                    except OSError:
                        pass
                if process.poll() is None:
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()
                process.stdout.close()

    return stream_blobs()


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


def verify_code_snapshots(
    prep_script_sha256: str,
    fact_verifier_sha256: str,
    typed_call_oracle_sha256: str | None = None,
) -> None:
    if sha256_file(Path(__file__)) != prep_script_sha256:
        raise PrepError("label_prep.py changed during frame construction")
    if sha256_file(BASE / "gate34_common.py") != fact_verifier_sha256:
        raise PrepError("gate34_common.py changed during frame construction")
    if (
        typed_call_oracle_sha256 is not None
        and typed_oracle_code_digest() != typed_call_oracle_sha256
    ):
        raise PrepError("typed call oracle changed during frame construction")


def parse_rfc3339(field: str, value: Any) -> dt.datetime:
    if not isinstance(value, str):
        raise PrepError(f"audit seed {field} must be RFC3339 text")
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise PrepError(f"audit seed {field} must be valid RFC3339") from exc
    if parsed.tzinfo is None:
        raise PrepError(f"audit seed {field} must include a timezone")
    return parsed.astimezone(dt.timezone.utc)


def validate_seed_record(
    value: Any,
    *,
    expected_input_binding: str | None = None,
    expected_input_committed_at: str | None = None,
) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != {
        "schema",
        "input_binding",
        "input_committed_at",
        "commitment_reference",
        "commitment_published_at",
        "commitment_document_sha256",
        "commitment_github_receipt",
        "source",
        "source_reference",
        "source_output_hex",
        "source_payload",
        "source_payload_sha256",
        "recorded_at",
        "seed_hex",
    }:
        raise PrepError(
            "audit seed record requires the complete input commitment, source provenance, "
            "source output, payload digest, timestamps, and derived seed"
        )
    if value.get("schema") != SEED_RECORD_SCHEMA:
        raise PrepError(f"unsupported audit seed schema {value.get('schema')!r}")
    input_binding = value.get("input_binding")
    if not isinstance(input_binding, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", input_binding):
        raise PrepError("audit seed input_binding must be a sha256 digest")
    if expected_input_binding is not None and input_binding != expected_input_binding:
        raise PrepError("audit seed was issued for different locked inputs")
    commitment_reference = value.get("commitment_reference")
    commitment_document_sha256 = value.get("commitment_document_sha256")
    if (
        not isinstance(commitment_reference, str)
        or not commitment_reference.startswith("https://")
    ):
        raise PrepError("input commitment requires an independently published HTTPS reference")
    if not isinstance(commitment_document_sha256, str) or not re.fullmatch(
        r"sha256:[0-9a-f]{64}", commitment_document_sha256
    ):
        raise PrepError("published input commitment requires its exact document digest")
    try:
        commitment_receipt = validate_github_gist_receipt(
            value.get("commitment_github_receipt")
        )
    except LabelProtocolError as exc:
        raise PrepError(f"input commitment has no valid GitHub receipt: {exc}") from exc
    if (
        commitment_receipt["api_url"] != commitment_reference
        or commitment_receipt["created_at"] != value.get("commitment_published_at")
        or commitment_receipt["document_sha256"] != commitment_document_sha256
    ):
        raise PrepError("input commitment fields do not match its GitHub receipt")
    source = value.get("source")
    if source not in {"public_randomness_beacon", "external_witness"}:
        raise PrepError(
            "audit seed source must be an independently auditable public_randomness_beacon "
            "or external_witness"
        )
    source_reference = value.get("source_reference")
    if not isinstance(source_reference, str) or not source_reference.strip():
        raise PrepError("audit seed source_reference must be nonempty")
    if source == "public_randomness_beacon" and not source_reference.startswith(
        "https://beacon.nist.gov/"
    ):
        raise PrepError("public beacon receipts must reference the official NIST HTTPS beacon")
    source_output_hex = value.get("source_output_hex")
    if (
        not isinstance(source_output_hex, str)
        or not re.fullmatch(r"[0-9a-f]{64,512}", source_output_hex)
        or len(source_output_hex) % 2
    ):
        raise PrepError("audit source output must be 32-256 bytes of lowercase hex")
    source_payload_sha256 = value.get("source_payload_sha256")
    if not isinstance(source_payload_sha256, str) or not re.fullmatch(
        r"sha256:[0-9a-f]{64}", source_payload_sha256
    ):
        raise PrepError("audit source payload requires a sha256 digest")
    source_payload = value.get("source_payload")
    if not isinstance(source_payload, dict):
        raise PrepError("audit source payload must be a JSON object")
    actual_payload_sha256 = "sha256:" + hashlib.sha256(
        canonical_json(source_payload).encode("utf-8")
    ).hexdigest()
    if source_payload_sha256 != actual_payload_sha256:
        raise PrepError("audit source payload digest mismatch")
    input_committed_at = value.get("input_committed_at")
    if (
        expected_input_committed_at is not None
        and input_committed_at != expected_input_committed_at
    ):
        raise PrepError("audit seed does not match the persistent input commitment time")
    recorded_at = value.get("recorded_at")
    committed_time = parse_rfc3339("input_committed_at", input_committed_at)
    commitment_published_at = value.get("commitment_published_at")
    published_time = parse_rfc3339(
        "commitment_published_at", commitment_published_at
    )
    if published_time >= committed_time:
        raise PrepError(
            "input commitment must be independently published before its scheduled activation"
        )
    source_time = parse_rfc3339("recorded_at", recorded_at)
    if source_time <= committed_time:
        raise PrepError("audit source output must be recorded after the input commitment")
    if source == "public_randomness_beacon":
        if set(source_payload) != {"pulse"} or not isinstance(
            source_payload.get("pulse"), dict
        ):
            raise PrepError("NIST beacon payload must contain exactly one pulse object")
        pulse = source_payload["pulse"]
        uri = pulse.get("uri")
        if uri != source_reference or not re.fullmatch(
            r"https://beacon\.nist\.gov/beacon/2\.0/chain/[0-9]+/pulse/[0-9]+",
            str(uri),
        ):
            raise PrepError("NIST beacon reference must equal the canonical pulse URI")
        if pulse.get("version") != "2.0" or pulse.get("statusCode") != 0:
            raise PrepError("NIST beacon pulse must be a successful version 2.0 pulse")
        if pulse.get("period") != 60_000:
            raise PrepError("NIST beacon pulse must use the documented 60-second period")
        uri_match = re.fullmatch(
            r"https://beacon\.nist\.gov/beacon/2\.0/chain/([0-9]+)/pulse/([0-9]+)",
            str(uri),
        )
        if (
            uri_match is None
            or isinstance(pulse.get("chainIndex"), bool)
            or isinstance(pulse.get("pulseIndex"), bool)
            or pulse.get("chainIndex") != int(uri_match.group(1))
            or pulse.get("pulseIndex") != int(uri_match.group(2))
        ):
            raise PrepError("NIST beacon pulse indices do not match its canonical URI")
        pulse_output = pulse.get("outputValue")
        if not isinstance(pulse_output, str) or pulse_output.lower() != source_output_hex:
            raise PrepError("NIST beacon output does not match source_output_hex")
        pulse_time = parse_rfc3339("NIST pulse timeStamp", pulse.get("timeStamp"))
        if pulse_time != source_time:
            raise PrepError("NIST beacon timeStamp does not match recorded_at")
        first_future_pulse = committed_time.replace(second=0, microsecond=0) + dt.timedelta(
            minutes=1
        )
        if pulse_time != first_future_pulse:
            raise PrepError(
                "NIST receipt must use the first one-minute pulse after input commitment"
            )
        required_hex = {
            "certificateId": 128,
            "localRandomValue": 128,
            "precommitmentValue": 128,
            "outputValue": 128,
        }
        for field, length in required_hex.items():
            if not re.fullmatch(rf"[0-9A-Fa-f]{{{length}}}", str(pulse.get(field, ""))):
                raise PrepError(f"NIST beacon pulse has invalid {field}")
        signature = pulse.get("signatureValue")
        if not isinstance(signature, str) or not re.fullmatch(
            r"[0-9A-Fa-f]{512,4096}", signature
        ):
            raise PrepError("NIST beacon pulse has an invalid signatureValue")
        links = pulse.get("listValues")
        if not isinstance(links, list) or not any(
            isinstance(link, dict)
            and link.get("type") == "previous"
            and bool(
                re.fullmatch(
                    r"https://beacon\.nist\.gov/beacon/2\.0/chain/[0-9]+/pulse/[0-9]+",
                    str(link.get("uri", "")),
                )
            )
            and bool(re.fullmatch(r"[0-9A-Fa-f]{128}", str(link.get("value", ""))))
            for link in links
        ):
            raise PrepError("NIST beacon pulse is missing its previous-pulse chain link")
    else:
        if set(source_payload) != {
            "reference",
            "output_hex",
            "recorded_at",
            "witness",
            "signature",
        }:
            raise PrepError("external witness payload must use the fixed signed envelope")
        if (
            source_payload.get("reference") != source_reference
            or source_payload.get("output_hex") != source_output_hex
            or source_payload.get("recorded_at") != recorded_at
        ):
            raise PrepError("external witness payload does not match receipt provenance")
        if not all(
            isinstance(source_payload.get(field), str)
            and bool(source_payload[field].strip())
            for field in ("witness", "signature")
        ):
            raise PrepError("external witness payload requires witness and signature text")
    expected_seed = digest_parts(
        "t111-gate2-audit-seed-derive-v2",
        input_binding,
        input_committed_at,
        commitment_reference,
        commitment_published_at,
        commitment_document_sha256,
        commitment_receipt["revision"],
        source,
        source_reference,
        source_output_hex,
        source_payload_sha256,
        recorded_at,
    )
    seed_hex = value.get("seed_hex")
    if not isinstance(seed_hex, str) or not re.fullmatch(r"[0-9a-f]{64}", seed_hex):
        raise PrepError("audit seed must be 256 bits encoded as 64 lowercase hex characters")
    forbidden = {
        "0" * 64,
        f"{LEGACY_DIAGNOSTIC_SEED:064x}",
        hashlib.sha256(str(LEGACY_DIAGNOSTIC_SEED).encode()).hexdigest(),
        input_binding.removeprefix("sha256:"),
        hashlib.sha256(input_binding.encode()).hexdigest(),
        hashlib.sha256(input_binding.removeprefix("sha256:").encode()).hexdigest(),
        hashlib.sha256(bytes.fromhex(input_binding.removeprefix("sha256:"))).hexdigest(),
    }
    if seed_hex in forbidden:
        raise PrepError("legacy, zero, or deterministically derived audit seed is forbidden")
    # This cannot prove physical entropy, but it rejects obvious hand-written
    # placeholders while the audit source records how the value was drawn.
    periodic = any(
        64 % period == 0 and seed_hex == seed_hex[:period] * (64 // period)
        for period in range(1, 17)
    )
    if len(set(seed_hex)) < 8 or periodic:
        raise PrepError("audit seed lacks plausible entropy")
    if seed_hex != expected_seed:
        raise PrepError("audit seed is not the canonical derivation of its source receipt")
    return {
        "schema": SEED_RECORD_SCHEMA,
        "input_binding": input_binding,
        "input_committed_at": str(input_committed_at),
        "commitment_reference": commitment_reference,
        "commitment_published_at": str(commitment_published_at),
        "commitment_document_sha256": commitment_document_sha256,
        "commitment_github_receipt": commitment_receipt,
        "source": str(source),
        "source_reference": source_reference,
        "source_output_hex": source_output_hex,
        "source_payload": source_payload,
        "source_payload_sha256": source_payload_sha256,
        "recorded_at": str(recorded_at),
        "seed_hex": seed_hex,
    }


def first_eligible_nist_pulse_time(input_committed_at: str) -> dt.datetime:
    """Return the first 60-second NIST pulse strictly after activation."""

    committed = parse_rfc3339("input_committed_at", input_committed_at)
    return committed.replace(second=0, microsecond=0) + dt.timedelta(minutes=1)


def validate_input_commitment_document(document: bytes) -> dict[str, Any]:
    """Validate the exact coordinate-free document emitted by ``commit-inputs``."""

    if not isinstance(document, bytes) or not document or len(document) > 8 * 1024 * 1024:
        raise PrepError("input commitment must be nonempty bytes no larger than 8 MiB")
    try:
        text = document.decode("utf-8", errors="strict")
        value = json.loads(text)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PrepError(f"input commitment is not valid UTF-8 JSON: {exc}") from exc
    if not isinstance(value, dict) or set(value) != {
        "schema",
        "committed_at",
        "input_binding",
        "attempt_claim",
        "systems",
        "sampling_config",
        "decision_rule",
        "burn_ledger",
        "protocol_coverage",
        "frames",
        "power_analysis",
        "provenance",
    }:
        raise PrepError("input commitment does not have the exact Gate-2 commitment fields")
    canonical = (json.dumps(value, indent=2, sort_keys=True) + "\n").encode("utf-8")
    if document != canonical:
        raise PrepError("input commitment bytes are not the canonical commit-inputs document")
    if value.get("schema") != "t111-gate2-input-commitment-v1":
        raise PrepError("unsupported Gate-2 input commitment schema")
    binding = value.get("input_binding")
    if not isinstance(binding, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", binding):
        raise PrepError("input commitment input_binding must be a sha256 digest")
    committed_at = value.get("committed_at")
    parse_rfc3339("input commitment committed_at", committed_at)
    if value.get("systems") != list(SYSTEMS):
        raise PrepError("input commitment systems do not match the fixed Gate-2 systems")
    frames = value.get("frames")
    if not isinstance(frames, dict) or set(frames) != set(FRAMES):
        raise PrepError("input commitment does not bind all fixed Gate-2 frames")
    for frame, detail in frames.items():
        if not isinstance(detail, dict) or set(detail) != {
            "definition", "extractor_independent", "population_size", "strata"
        }:
            raise PrepError(f"input commitment frame {frame} has invalid fields")
    power = value.get("power_analysis")
    if not isinstance(power, dict) or power.get("attainable") is not True:
        raise PrepError("input commitment design is not statistically attainable")
    provenance = value.get("provenance")
    commits = provenance.get("locked_commits") if isinstance(provenance, dict) else None
    if not isinstance(commits, dict):
        raise PrepError("input commitment provenance has no locked commit set")
    claim = value.get("attempt_claim")
    if not isinstance(claim, dict):
        raise PrepError("input commitment has no persistent attempt claim")
    validated_claim = validate_attempt_claim(
        claim,
        commits=commits,
        base_input_binding=str(claim.get("base_input_binding")),
        input_binding=binding,
    )
    if validated_claim["committed_at"] != committed_at:
        raise PrepError("input commitment timestamp does not match its attempt claim")
    return value


def build_nist_seed_record(
    commitment_document: bytes,
    github_receipt: Any,
    *,
    github_fetcher: Any = github_json_fetcher,
    nist_fetcher: Any = nist_beacon_json_fetcher,
) -> dict[str, Any]:
    """Build seed-v3 from the precommitted first eligible NIST pulse.

    The time lookup is checked against the immutable canonical pulse endpoint.
    No latest-pulse or next-pulse fallback is attempted.
    """

    commitment = validate_input_commitment_document(commitment_document)
    try:
        receipt = validate_github_gist_receipt(github_receipt)
        verify_github_initial_gist_receipt(
            receipt, commitment_document, github_fetcher
        )
    except LabelProtocolError as exc:
        raise PrepError(f"input commitment GitHub receipt cannot be verified: {exc}") from exc
    committed_time = parse_rfc3339(
        "input commitment committed_at", commitment["committed_at"]
    )
    published_time = parse_rfc3339("GitHub receipt created_at", receipt["created_at"])
    if published_time >= committed_time:
        raise PrepError("GitHub initial revision was not published before activation")

    pulse_time = first_eligible_nist_pulse_time(commitment["committed_at"])
    pulse_millis = int(pulse_time.timestamp() * 1000)
    time_url = f"{NIST_BEACON_API_ROOT}/pulse/time/{pulse_millis}"
    try:
        time_response = nist_fetcher(time_url)
    except Exception as exc:
        raise PrepError(
            f"exact first eligible NIST pulse is unavailable at {time_url}: {exc}"
        ) from exc
    if (
        not isinstance(time_response, dict)
        or set(time_response) != {"pulse"}
        or not isinstance(time_response.get("pulse"), dict)
    ):
        raise PrepError("NIST time lookup must return exactly one complete pulse object")
    pulse = dict(time_response["pulse"])
    canonical_uri = pulse.get("uri")
    if not isinstance(canonical_uri, str) or not re.fullmatch(
        r"https://beacon\.nist\.gov/beacon/2\.0/chain/[0-9]+/pulse/[0-9]+",
        canonical_uri,
    ):
        raise PrepError("NIST time lookup returned no canonical pulse URI")
    try:
        canonical_response = nist_fetcher(canonical_uri)
    except Exception as exc:
        raise PrepError(f"cannot verify canonical NIST pulse {canonical_uri}: {exc}") from exc
    if canonical_response != time_response:
        raise PrepError("NIST time lookup and canonical pulse endpoint disagree")

    source_payload = {"pulse": pulse}
    source_payload_sha256 = "sha256:" + hashlib.sha256(
        canonical_json(source_payload).encode("utf-8")
    ).hexdigest()
    output = pulse.get("outputValue")
    if not isinstance(output, str):
        raise PrepError("NIST pulse has no outputValue")
    record = {
        "schema": SEED_RECORD_SCHEMA,
        "input_binding": commitment["input_binding"],
        "input_committed_at": commitment["committed_at"],
        "commitment_reference": receipt["api_url"],
        "commitment_published_at": receipt["created_at"],
        "commitment_document_sha256": receipt["document_sha256"],
        "commitment_github_receipt": receipt,
        "source": "public_randomness_beacon",
        "source_reference": canonical_uri,
        "source_output_hex": output.lower(),
        "source_payload": source_payload,
        "source_payload_sha256": source_payload_sha256,
        "recorded_at": pulse.get("timeStamp"),
    }
    record["seed_hex"] = digest_parts(
        "t111-gate2-audit-seed-derive-v2",
        record["input_binding"],
        record["input_committed_at"],
        record["commitment_reference"],
        record["commitment_published_at"],
        record["commitment_document_sha256"],
        receipt["revision"],
        record["source"],
        record["source_reference"],
        record["source_output_hex"],
        record["source_payload_sha256"],
        record["recorded_at"],
    )
    return validate_seed_record(
        record,
        expected_input_binding=commitment["input_binding"],
        expected_input_committed_at=commitment["committed_at"],
    )


def load_seed_record(
    path: Path,
    *,
    expected_input_binding: str | None = None,
    expected_input_committed_at: str | None = None,
) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load audit seed record {path}: {exc}") from exc
    return validate_seed_record(
        value,
        expected_input_binding=expected_input_binding,
        expected_input_committed_at=expected_input_committed_at,
    )


def seed_input_binding(
    *,
    systems: Sequence[str],
    sampling_config: dict[str, int],
    corpus_lock_sha256: str,
    facts_sha256: dict[str, str],
    burn_binding: dict[str, Any],
    corpus_inputs: dict[str, Any],
    population_binding: dict[str, Any],
    selection_bindings: dict[str, str] | None = None,
    input_committed_at: str | None = None,
    prep_script_sha256: str | None = None,
    fact_verifier_sha256: str | None = None,
    typed_call_oracle_sha256: str | None = None,
    typed_call_oracle_diagnostics: dict[str, Any] | None = None,
) -> str:
    """Bind randomization to every input fixed before coordinates exist."""

    return "sha256:" + digest_parts(
        SCHEMA,
        list(systems),
        sampling_config,
        gate_decision_rule(),
        corpus_lock_sha256,
        facts_sha256,
        burn_binding,
        corpus_inputs,
        population_binding,
        selection_bindings or {},
        input_committed_at,
        prep_script_sha256 or sha256_file(Path(__file__)),
        fact_verifier_sha256 or sha256_file(BASE / "gate34_common.py"),
        typed_call_oracle_sha256,
        typed_call_oracle_diagnostics or {},
    )


def frame_selection_bindings(
    *,
    systems: Sequence[str],
    sampling_config: dict[str, int],
    corpus_lock_sha256: str,
    burn_binding: dict[str, Any],
    corpus_inputs: dict[str, Any],
    populations: dict[str, Sequence[dict[str, Any]]],
    prep_script_sha256: str | None = None,
    typed_call_oracle_sha256: str | None = None,
    typed_call_oracle_diagnostics: dict[str, Any] | None = None,
) -> dict[str, str]:
    """Bind each rank only to information allowed to define that frame.

    Source frames deliberately project away extractor alignment fields such as
    ``extracted`` and ``emitted_roles``.  Their selected coordinates therefore
    remain unchanged if an extractor fact changes while the corpus, sampling
    policy, and external entropy stay fixed.  Precision frames still bind to
    their fact-derived site and stratum populations.
    """

    bindings: dict[str, str] = {}
    for frame in FRAMES:
        projection = sorted(
            (
                {
                    "site_id": str(row["site_id"]),
                    "system": str(row["system"]),
                    "stratum": str(row["stratum"]),
                }
                for row in populations[frame]
            ),
            key=canonical_json,
        )
        bindings[frame] = "sha256:" + digest_parts(
            "t111-gate2-frame-selection-v1",
            SCHEMA,
            frame,
            list(systems),
            sampling_config,
            corpus_lock_sha256,
            burn_binding,
            corpus_inputs,
            projection,
            prep_script_sha256 or sha256_file(Path(__file__)),
            typed_call_oracle_sha256,
            typed_call_oracle_diagnostics or {},
        )
    return bindings


def audited_selection_seeds(
    selection_bindings: dict[str, str], seed_record: dict[str, Any]
) -> dict[str, str]:
    """Domain-separate public entropy from the global receipt binding."""

    entropy = digest_parts(
        "t111-gate2-audit-entropy-v2",
        seed_record["seed_hex"],
        seed_record["commitment_reference"],
        seed_record["commitment_published_at"],
        seed_record["commitment_document_sha256"],
        seed_record["commitment_github_receipt"]["revision"],
        seed_record["source"],
        seed_record["source_reference"],
        seed_record["source_output_hex"],
        seed_record["source_payload_sha256"],
        seed_record["recorded_at"],
    )
    return {
        frame: digest_parts(
            "t111-gate2-rank-v2", frame, selection_bindings[frame], entropy
        )
        for frame in FRAMES
    }


def commit_set_lineage_binding(commits: dict[str, str]) -> str:
    if set(commits) != set(SYSTEMS) or any(
        not re.fullmatch(r"[0-9a-f]{40}", commit) for commit in commits.values()
    ):
        raise PrepError("attempt lineage requires all four full fixture commits")
    return "sha256:" + digest_parts(
        "t111-gate2-commit-set-lineage-v1", dict(sorted(commits.items()))
    )


def validate_attempt_claim(
    value: Any,
    *,
    commits: dict[str, str],
    base_input_binding: str,
    input_binding: str | None = None,
) -> dict[str, Any]:
    expected_fields = {
        "schema",
        "source_lineage_binding",
        "commits",
        "base_input_binding",
        "input_binding",
        "committed_at",
    }
    if not isinstance(value, dict) or set(value) != expected_fields:
        raise PrepError("invalid Gate-2 commit-set attempt claim")
    expected_lineage = commit_set_lineage_binding(commits)
    if (
        value.get("schema") != "t111-gate2-commit-set-attempt-v1"
        or value.get("source_lineage_binding") != expected_lineage
        or value.get("commits") != dict(sorted(commits.items()))
        or value.get("base_input_binding") != base_input_binding
        or not re.fullmatch(r"sha256:[0-9a-f]{64}", str(value.get("input_binding")))
    ):
        raise PrepError(
            "Gate-2 commit set is already bound to a different input lineage"
        )
    if input_binding is not None and value.get("input_binding") != input_binding:
        raise PrepError(
            "Gate-2 commit set is already bound to a different final input binding"
        )
    parse_rfc3339("attempt committed_at", value.get("committed_at"))
    return dict(value)


def locked_attempt_claim(
    *,
    commits: dict[str, str],
    base_input_binding: str,
    input_binding: str | None,
    committed_at: str | None,
    create: bool,
    allow_missing: bool = False,
) -> dict[str, Any] | None:
    """Bind a four-commit source population to one Gate-2 attempt forever."""

    lineage = commit_set_lineage_binding(commits)
    root = SEALED_CLAIM_ROOT.absolute()
    root.mkdir(parents=True, exist_ok=True)
    path = root / (
        f".t111-gate2-source-{lineage.removeprefix('sha256:')}.attempt.json"
    )
    if create:
        if input_binding is None or committed_at is None:
            raise PrepError("attempt creation requires final input binding and timestamp")
        record = {
            "schema": "t111-gate2-commit-set-attempt-v1",
            "source_lineage_binding": lineage,
            "commits": dict(sorted(commits.items())),
            "base_input_binding": base_input_binding,
            "input_binding": input_binding,
            "committed_at": committed_at,
        }
        validate_attempt_claim(
            record,
            commits=commits,
            base_input_binding=base_input_binding,
            input_binding=input_binding,
        )
        try:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError:
            pass
        else:
            try:
                os.write(fd, (canonical_json(record) + "\n").encode())
                os.fsync(fd)
            finally:
                os.close(fd)
            fsync_directory(root)
    if not path.exists() and not path.is_symlink() and allow_missing:
        return None
    if path.is_symlink() or not path.is_file():
        raise PrepError(
            "missing persistent Gate-2 commit-set attempt; run commit-inputs first: "
            f"{path}"
        )
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load Gate-2 attempt claim {path}: {exc}") from exc
    return validate_attempt_claim(
        value,
        commits=commits,
        base_input_binding=base_input_binding,
        input_binding=input_binding,
    )


def source_identity(system: str, commit: str) -> str:
    return "sha256:" + digest_parts("t111-source-v1", system, commit)


def verified_burn_ledger(
    path: Path,
) -> tuple[list[dict[str, Any]], dict[str, Any], list[dict[str, Any]]]:
    """Load append-only burn cohorts and return their immutable base binding."""

    try:
        burned, coordinates_sha256 = load_burn_ledger(path)
        try:
            cohorts, cohort_projection_sha256 = load_burn_ledger_cohorts(path)
        except EvidenceValidationError:
            # Historical v1 compatibility. New writes always use v2 cohorts.
            cohorts = [
                {
                    "cohort_id": "legacy-v1-disclosures",
                    "input_binding": None,
                    "source_commits": dict(sorted(LEGACY_BURN_COMMITS.items())),
                    "source_artifacts": [],
                    "coordinates": burned,
                    "coordinate_count": len(burned),
                    "coordinates_sha256": coordinates_sha256,
                }
            ]
            cohort_projection_sha256 = coordinates_sha256
    except EvidenceValidationError as exc:
        raise PrepError(f"invalid burn ledger {path}: {exc}") from exc
    cohort_binding = [
        {
            "cohort_id": cohort["cohort_id"],
            "input_binding": cohort["input_binding"],
            "source_commits": cohort["source_commits"],
            "coordinate_count": cohort["coordinate_count"],
            "coordinates_sha256": cohort["coordinates_sha256"],
        }
        for cohort in cohorts
    ]
    return burned, {
        "schema": "t111-burn-scope-v2",
        "ledger_sha256": sha256_file(path),
        "coordinate_count": len(burned),
        "coordinates_sha256": coordinates_sha256,
        "cohort_projection_sha256": cohort_projection_sha256,
        "cohort_count": len(cohorts),
        "cohorts": cohort_binding,
        "source_identities": {
            str(cohort["cohort_id"]): {
                system: source_identity(system, commit)
                for system, commit in sorted(cohort["source_commits"].items())
            }
            for cohort in cohorts
        },
    }, cohorts


def scoped_burn_binding(
    path: Path,
    commits: dict[str, str],
    corpus_dir: Path = DEFAULT_CORPUS,
) -> tuple[list[dict[str, Any]], dict[str, Any], dict[str, list[dict[str, Any]]]]:
    """Carry every disclosed source site into a deterministic census stratum.

    Commit changes do not restore blindness. Byte-identical blobs keep their
    exposed intervals (including relocated copies); changed files translate an
    exact unique line span, otherwise the whole current path is treated as
    disclosed. An unresolved deleted/renamed-and-modified path fails closed.
    """

    burned, binding, cohorts = verified_burn_ledger(path)
    active: dict[str, list[dict[str, Any]]] = {}
    resolutions: list[dict[str, Any]] = []
    dispositions: Counter[str] = Counter()
    identical_commits: list[str] = []
    for system, commit in commits.items():
        root = corpus_dir / system
        current_tree = git_tree_blobs(root, commit)
        current_by_oid: dict[str, list[str]] = defaultdict(list)
        for rel, oid in current_tree.items():
            current_by_oid[oid].append(rel)
        for paths in current_by_oid.values():
            paths.sort()

        intervals: dict[str, list[tuple[int, int]]] = defaultdict(list)
        quarantined_paths: set[str] = set()
        old_trees: dict[str, dict[str, str]] = {}
        for cohort in cohorts:
            cohort_id = str(cohort["cohort_id"])
            cohort_rows = [
                row for row in cohort["coordinates"] if row.get("system") == system
            ]
            if not cohort_rows:
                continue
            old_commit = cohort["source_commits"].get(system)
            if not isinstance(old_commit, str):
                raise PrepError(
                    f"burn cohort {cohort_id} has no source commit for {system}"
                )
            if old_commit == commit:
                identical_commits.append(f"{cohort_id}:{system}")
            if old_commit not in old_trees:
                old_trees[old_commit] = git_tree_blobs(root, old_commit)
            old_tree = old_trees[old_commit]
            for row in cohort_rows:
                rel = str(row["path"])
                start, end = int(row["start_line"]), int(row["end_line"])
                old_oid = old_tree.get(rel)
                if old_oid is None:
                    raise PrepError(
                        "burn source is unavailable at "
                        f"{cohort_id}:{system}:{old_commit}:{rel}"
                    )
                current_oid = current_tree.get(rel)
                identical_paths = current_by_oid.get(old_oid, [])
                if identical_paths:
                    for target in identical_paths:
                        intervals[target].append((start, end))
                        resolutions.append(
                            {
                                "cohort_id": cohort_id,
                                "system": system,
                                "source_commit": old_commit,
                                "source_path": rel,
                                "source_oid": old_oid,
                                "target_path": target,
                                "target_oid": old_oid,
                                "source_start_line": start,
                                "source_end_line": end,
                                "target_start_line": start,
                                "target_end_line": end,
                                "disposition": (
                                    "identical_path"
                                    if target == rel
                                    else "identical_blob_relocated"
                                ),
                            }
                        )
                        dispositions[resolutions[-1]["disposition"]] += 1
                    # A relocated byte-identical copy preserves the old
                    # exposure, but a modified original path must also be
                    # translated or quarantined.
                    if current_oid is None or current_oid == old_oid:
                        continue

                if current_oid is None:
                    raise PrepError(
                        "disclosed source cannot be mapped into the current tree: "
                        f"{cohort_id}:{system}:{rel}"
                    )
                old_lines = git_blob(root, old_commit, rel).splitlines(keepends=True)
                current_lines = git_blob(root, commit, rel).splitlines(keepends=True)
                if start <= 0 or end < start or end > len(old_lines):
                    raise PrepError(
                        "burn interval is outside its pinned blob: "
                        f"{cohort_id}:{system}:{rel}"
                    )
                needle = old_lines[start - 1 : end]
                matches = [
                    index + 1
                    for index in range(0, len(current_lines) - len(needle) + 1)
                    if current_lines[index : index + len(needle)] == needle
                ]
                if len(matches) == 1:
                    translated_start = matches[0]
                    translated_end = translated_start + len(needle) - 1
                    intervals[rel].append((translated_start, translated_end))
                    disposition = "unique_line_span_translation"
                    resolutions.append(
                        {
                            "cohort_id": cohort_id,
                            "system": system,
                            "source_commit": old_commit,
                            "source_path": rel,
                            "source_oid": old_oid,
                            "target_path": rel,
                            "target_oid": current_oid,
                            "source_start_line": start,
                            "source_end_line": end,
                            "target_start_line": translated_start,
                            "target_end_line": translated_end,
                            "disposition": disposition,
                        }
                    )
                    dispositions[disposition] += 1
                else:
                    quarantined_paths.add(rel)
                    disposition = "changed_path_census"
                    resolutions.append(
                        {
                            "cohort_id": cohort_id,
                            "system": system,
                            "source_commit": old_commit,
                            "source_path": rel,
                            "source_oid": old_oid,
                            "target_path": rel,
                            "target_oid": current_oid,
                            "source_start_line": start,
                            "source_end_line": end,
                            "target_start_line": 1,
                            "target_end_line": max(1, len(current_lines)),
                            "disposition": disposition,
                        }
                    )
                    dispositions[disposition] += 1

        active_rows: list[dict[str, Any]] = []
        for rel in sorted(set(intervals) | quarantined_paths):
            if rel in quarantined_paths:
                line_count = len(git_blob(root, commit, rel).splitlines())
                active_rows.append(
                    {
                        "system": system,
                        "path": rel,
                        "start_line": 1,
                        "end_line": max(1, line_count),
                    }
                )
                continue
            for start, end in sorted(set(intervals[rel])):
                active_rows.append(
                    {
                        "system": system,
                        "path": rel,
                        "start_line": start,
                        "end_line": end,
                    }
                )
        active[system] = active_rows

    active_projection = sorted(
        (row for rows in active.values() for row in rows), key=canonical_json
    )
    binding = {
        **binding,
        "carry_forward_schema": "t111-burn-carry-forward-census-v2",
        "active_source_identities": {
            system: source_identity(system, commit)
            for system, commit in sorted(commits.items())
        },
        "identical_source_commit_systems": sorted(identical_commits),
        "active_coordinate_count": len(active_projection),
        "active_coordinates_sha256": "sha256:" + digest_parts(
            "t111-active-burn-census-v2", active_projection
        ),
        "resolution_count": len(resolutions),
        "resolution_dispositions": dict(sorted(dispositions.items())),
        "resolution_sha256": "sha256:" + digest_parts(
            "t111-burn-resolution-v2", sorted(resolutions, key=canonical_json)
        ),
    }
    return burned, binding, active


def git_tree_blobs(root: Path, commit: str) -> dict[str, str]:
    raw = git(root, "ls-tree", "-rz", "--full-tree", commit)
    result: dict[str, str] = {}
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        metadata, separator, raw_path = entry.partition(b"\t")
        fields = metadata.decode("ascii").split()
        if not separator or len(fields) != 3:
            raise PrepError(f"{root}: malformed Git tree while resolving burns")
        mode, kind, oid = fields
        rel = raw_path.decode("utf-8", errors="strict")
        if mode in {"100644", "100755"} and kind == "blob":
            result[rel] = oid
    return result


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


def partition_burned(
    rows: Sequence[dict[str, Any]], burned: Sequence[dict[str, Any]]
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    fresh: list[dict[str, Any]] = []
    census: list[dict[str, Any]] = []
    for row in rows:
        (census if coordinate_burned(row, burned) else fresh).append(row)
    return fresh, census


def site_coordinate_burned(
    row: dict[str, Any], burned: Sequence[dict[str, Any]]
) -> bool:
    line = int(row["line"])
    return coordinate_burned(
        {
            "system": row["system"],
            "path": row["path"],
            "start_line": line,
            "end_line": line,
        },
        burned,
    )


def site_id(system: str, commit: str, path: str, byte_offset: int, method: str) -> str:
    return "g2s_" + digest_parts(system, commit, path, byte_offset, method)


def rank(seed: str | int, purpose: str, stratum: str, sid: str) -> str:
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


def fsync_directory(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


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


GENERATED_HEADER = re.compile(r"(?m)^(//|#).*Code generated .* DO NOT EDIT")
MOCK_HEADER = re.compile(
    r"Code generated by MockGen|Code generated by mockery|Code generated by counterfeiter"
)


def source_code_role(path: str, content: str) -> str:
    """Independent source classifier matching the precommitted role taxonomy."""

    parts = path.split("/")
    if "vendor" in parts:
        return "vendor"
    leaf = parts[-1]
    if (
        MOCK_HEADER.search(content[:4096])
        or any(part in {"mock", "mocks", "mocksgen", "fake", "fakes"} for part in parts)
        or leaf.startswith(("mock_", "fake_"))
        or leaf.endswith(("_mock.go", "_mock_test.go", "_fake.go", "_fake_test.go"))
    ):
        return "mock"
    if GENERATED_HEADER.search(content[:4096]):
        return "generated"
    if leaf.endswith("_test.go") or any(part in {"tests", "testdata"} for part in parts):
        return "test"
    return "production"


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
    before = masked[max(0, match.start() - 120) : match.start()].lower()
    after = masked[match.start(1) : min(len(masked), match.end() + 180)].lower()
    receiver_signal = re.search(
        r"(?:client|grpc|stub|conn|service)\w*\s*(?:\([^()]*\))?\s*$", before
    )
    request_signal = re.search(
        r"\(\s*(?:[A-Za-z_][A-Za-z0-9_]*(?:ctx|context)[A-Za-z0-9_]*\b|"
        r"[^,()]*newcontext\s*\(|[^,()]*context\s*\()",
        after,
    )
    lexical_signal = bool(receiver_signal or request_signal)
    if method in rpc_methods and lexical_signal:
        return "known_rpc_with_signal"
    if lexical_signal:
        return "grpc_lexical_signal"
    if method in rpc_methods:
        return "ambiguous_known_rpc_name"
    return "other_exported_selector"


def in_call_recall_target(row: dict[str, Any]) -> bool:
    """Operational source-only high-recall predicate for direct gRPC calls."""

    return row.get("risk") in {
        "known_rpc_with_signal",
        "grpc_lexical_signal",
        "ambiguous_known_rpc_name",
    }


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
        candidate_role = source_code_role(rel, text)
        char_starts, byte_starts = source_offsets(text)
        for match in SELECTOR_CALL.finditer(masked):
            method = match.group(1)
            line, column, byte_offset = coordinate_at(
                text, match.start(1), char_starts, byte_starts
            )
            sid = site_id(system, commit, rel, byte_offset, method)
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
                    "candidate_role": candidate_role,
                }
            )
    return population


TYPED_ORACLE_STRATA = {
    "generated_client_interface",
    "concrete_implements_generated_client",
    "interface_embeds_generated_client",
    "structural_grpc_client_interface",
    "ambiguous_multiple_generated_clients",
}
TYPED_ORACLE_DIAGNOSTICS_SCHEMA = "t111-typed-call-oracle-diagnostics-v3"
TYPED_ORACLE_DIAGNOSTIC_COUNTS = {
    "modules",
    "loaded_packages",
    "root_source_files",
    "external_source_files",
    "generated_client_interfaces",
    "structural_client_interfaces",
    "selector_call_sites",
    "matched_sites",
    "ambiguous_sites",
    "deduplicated_matches",
}
TOOLCHAIN_IDENTITY_RE = re.compile(
    r'^go_version="([^"]+)";go_digest=(sha256:[0-9a-f]{64});'
    r'git_version="([^"]+)";git_digest=(sha256:[0-9a-f]{64})$'
)


def resolve_oracle_toolchain(expected_identity: str) -> dict[str, str]:
    """Resolve and bind the exact producer Go/Git toolchain for the oracle."""

    match = TOOLCHAIN_IDENTITY_RE.fullmatch(expected_identity)
    if match is None:
        raise PrepError("extractor facts have an invalid toolchain identity")
    expected_go_version, expected_go_digest, expected_git_version, expected_git_digest = (
        match.groups()
    )
    resolved: dict[str, str] = {}
    for name, version_args, expected_version, expected_digest in (
        ("go", ("version",), expected_go_version, expected_go_digest),
        ("git", ("version",), expected_git_version, expected_git_digest),
    ):
        found = shutil.which(name)
        if found is None:
            raise PrepError(f"required exact {name} executable is unavailable")
        try:
            path = Path(found).resolve(strict=True)
        except OSError as exc:
            raise PrepError(f"cannot resolve {name} executable: {exc}") from exc
        digest = "sha256:" + sha256_file(path)
        try:
            result = subprocess.run(
                [str(path), *version_args],
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env={"PATH": str(path.parent), "GOENV": "off", "GOTOOLCHAIN": "local"},
            )
        except OSError as exc:
            raise PrepError(f"cannot identify {name} executable: {exc}") from exc
        version = result.stdout.decode("utf-8", errors="replace").strip()
        if result.returncode or version != expected_version or digest != expected_digest:
            raise PrepError(
                f"{name} toolchain differs from the fact producer: "
                f"version={version!r} digest={digest}"
            )
        resolved[f"{name}_path"] = str(path)
        resolved[f"{name}_version"] = version
        resolved[f"{name}_digest"] = digest

    go_parts = resolved["go_version"].split()
    if len(go_parts) != 4 or go_parts[:2] != ["go", "version"] or "/" not in go_parts[3]:
        raise PrepError(f"unsupported Go version identity {resolved['go_version']!r}")
    goos, goarch = go_parts[3].split("/", 1)
    resolved.update(
        identity=expected_identity,
        runtime_version=go_parts[2],
        goos=goos,
        goarch=goarch,
    )
    return resolved


def oracle_subprocess_env(toolchain: dict[str, str], tool_dir: Path, cache_dir: Path) -> dict[str, str]:
    home = Path(os.environ.get("HOME", str(Path.home()))).resolve()
    gopath = Path(os.environ.get("GOPATH", str(home / "go"))).resolve()
    modcache = Path(os.environ.get("GOMODCACHE", str(gopath / "pkg" / "mod"))).resolve()
    gocache = Path(os.environ.get("GOCACHE", str(cache_dir))).resolve()
    return {
        "CGO_ENABLED": "0",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_TERMINAL_PROMPT": "0",
        "GO111MODULE": "on",
        "GOARCH": toolchain["goarch"],
        "GOCACHE": str(gocache),
        "GOENV": "off",
        "GOFLAGS": "-mod=readonly -buildvcs=false",
        "GOMODCACHE": str(modcache),
        "GOOS": toolchain["goos"],
        "GOPATH": str(gopath),
        "GOPROXY": "off",
        "GOSUMDB": "off",
        "GOTOOLCHAIN": "local",
        "GOWORK": "off",
        "HOME": str(home),
        "PATH": str(tool_dir),
        "TMPDIR": str(cache_dir.parent / "tmp"),
    }


def typed_oracle_code_digest() -> str:
    files = sorted(TYPED_CALL_ORACLE.rglob("*.go"))
    if not files:
        raise PrepError(f"typed call oracle source is missing: {TYPED_CALL_ORACLE}")
    return "sha256:" + digest_parts(
        "t111-typed-call-oracle-code-v1",
        [
            {
                "path": path.relative_to(REPO_ROOT).as_posix(),
                "sha256": sha256_file(path),
            }
            for path in files
        ],
        sha256_file(REPO_ROOT / "go.mod"),
        sha256_file(REPO_ROOT / "go.sum"),
    )


def scan_typed_call_recall_frame(
    system: str,
    root: Path,
    commit: str,
    build_tags: Sequence[str],
    tracked_files: set[str],
    toolchain: dict[str, str] | None = None,
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Run the fact-blind go/types oracle and validate every coordinate."""

    command = [
        "go",
        "run",
        "./spike/t111/typedcalloracle",
        "-root",
        str(root),
        "-commit",
        commit,
    ]
    if build_tags:
        command.extend(["-tags", ",".join(sorted(build_tags))])
    try:
        with tempfile.TemporaryDirectory(prefix="t111-typed-oracle-") as raw_tmp:
            run_env = None
            if toolchain is not None:
                temporary = Path(raw_tmp)
                tool_dir = temporary / "tools"
                cache_dir = temporary / "cache"
                tool_dir.mkdir()
                cache_dir.mkdir()
                (temporary / "tmp").mkdir()
                os.symlink(toolchain["go_path"], tool_dir / "go")
                os.symlink(toolchain["git_path"], tool_dir / "git")
                run_env = oracle_subprocess_env(toolchain, tool_dir, cache_dir)
            result = subprocess.run(
                command,
                cwd=REPO_ROOT,
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=20 * 60,
                env=run_env,
            )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise PrepError(f"{system}: typed call oracle failed to run: {exc}") from exc
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise PrepError(f"{system}: typed call oracle failed: {detail}")
    try:
        output = result.stdout.decode("utf-8", errors="strict")
        diagnostic_output = result.stderr.decode("utf-8", errors="strict")
    except UnicodeDecodeError as exc:
        raise PrepError(
            f"{system}: typed call oracle emitted non-UTF-8 output or diagnostics"
        ) from exc

    required = {"path", "line", "column", "byte_offset", "method", "stratum"}
    blobs: dict[str, bytes] = {}
    population: list[dict[str, Any]] = []
    seen: set[str] = set()
    for number, line in enumerate(output.splitlines(), 1):
        try:
            raw = json.loads(line)
        except json.JSONDecodeError as exc:
            raise PrepError(f"{system}: typed oracle line {number} is invalid JSON") from exc
        if not isinstance(raw, dict) or set(raw) != required:
            raise PrepError(f"{system}: typed oracle line {number} has invalid fields")
        rel = raw["path"]
        method = raw["method"]
        stratum = raw["stratum"]
        if (
            not isinstance(rel, str)
            or rel not in tracked_files
            or not rel.endswith(".go")
            or not isinstance(method, str)
            or not re.fullmatch(r"[A-Z][A-Za-z0-9_]*", method)
            or stratum not in TYPED_ORACLE_STRATA
        ):
            raise PrepError(f"{system}: typed oracle line {number} is outside its contract")
        numeric = {field: raw[field] for field in ("line", "column", "byte_offset")}
        if any(not isinstance(value, int) or value <= 0 for value in numeric.values()):
            raise PrepError(f"{system}: typed oracle line {number} has invalid coordinates")
        byte_offset = int(raw["byte_offset"])
        if rel not in blobs:
            blobs[rel] = git_blob(root, commit, rel)
        data = blobs[rel]
        token = method.encode("utf-8")
        if (
            byte_offset + len(token) > len(data)
            or data[byte_offset : byte_offset + len(token)] != token
            or not re.match(rb"[ \t\r\n]*\(", data[byte_offset + len(token) :])
        ):
            raise PrepError(
                f"{system}: typed oracle coordinate does not point to method call on line {number}"
            )
        actual_line, actual_column = byte_coordinate(data, byte_offset)
        if (actual_line, actual_column) != (raw["line"], raw["column"]):
            raise PrepError(f"{system}: typed oracle line/column cross-check failed")
        sid = site_id(system, commit, rel, byte_offset, method)
        if sid in seen:
            raise PrepError(f"{system}: typed oracle emitted duplicate site {sid}")
        seen.add(sid)
        text = data.decode("utf-8", errors="strict")
        population.append(
            {
                "site_id": sid,
                "system": system,
                "path": rel,
                "line": actual_line,
                "start_line": actual_line,
                "end_line": actual_line,
                "column": actual_column,
                "byte_offset": byte_offset,
                "method": method,
                "stratum": f"{system}|{stratum}",
                "risk": stratum,
                "candidate_role": source_code_role(rel, text),
            }
        )
    diagnostic_lines = diagnostic_output.splitlines()
    if len(diagnostic_lines) != 1 or not diagnostic_lines[0]:
        raise PrepError(f"{system}: typed oracle must emit exactly one diagnostic record")
    try:
        diagnostics = json.loads(diagnostic_lines[0])
    except json.JSONDecodeError as exc:
        raise PrepError(f"{system}: typed oracle diagnostics are invalid JSON") from exc
    required_diagnostics = {
        "schema",
        "runtime_version",
        "goos",
        "goarch",
        "semantic_inputs_digest",
        *TYPED_ORACLE_DIAGNOSTIC_COUNTS,
    }
    if not isinstance(diagnostics, dict) or set(diagnostics) != required_diagnostics:
        raise PrepError(f"{system}: typed oracle diagnostics have invalid fields")
    if diagnostics.get("schema") != TYPED_ORACLE_DIAGNOSTICS_SCHEMA:
        raise PrepError(f"{system}: unsupported typed oracle diagnostic schema")
    if toolchain is not None and any(
        diagnostics.get(field) != toolchain[field]
        for field in ("runtime_version", "goos", "goarch")
    ):
        raise PrepError(f"{system}: typed oracle runtime differs from the bound producer toolchain")
    semantic_digest = diagnostics.get("semantic_inputs_digest")
    if not isinstance(semantic_digest, str) or not re.fullmatch(
        r"sha256:[0-9a-f]{64}", semantic_digest
    ):
        raise PrepError(f"{system}: typed oracle semantic input digest is invalid")
    if any(
        type(diagnostics.get(field)) is not int or diagnostics[field] < 0
        for field in TYPED_ORACLE_DIAGNOSTIC_COUNTS
    ):
        raise PrepError(f"{system}: typed oracle diagnostic counts are invalid")
    if diagnostics["modules"] <= 0:
        raise PrepError(f"{system}: typed oracle diagnostics report no modules")
    if diagnostics["matched_sites"] != len(population):
        raise PrepError(f"{system}: typed oracle diagnostic match count is inconsistent")
    if diagnostics["matched_sites"] and (
        diagnostics["loaded_packages"] == 0
        or diagnostics["root_source_files"] == 0
        or (
            diagnostics["generated_client_interfaces"]
            + diagnostics["structural_client_interfaces"]
            == 0
        )
    ):
        raise PrepError(f"{system}: typed oracle diagnostics contradict emitted matches")
    if toolchain is not None:
        diagnostics = {
            **diagnostics,
            "launcher_toolchain_identity": toolchain["identity"],
        }
    return sorted(population, key=lambda row: row["site_id"]), diagnostics


def registration_matches(masked: str) -> list[re.Match[str]]:
    """Return broad, call-shaped Go/gRPC server registration candidates.

    Generated function declarations are excluded using only source syntax, not
    extractor facts. Direct ``server.RegisterService`` calls form a distinct
    risk stratum. Interface declarations and unrelated functions remain in the
    superset and are resolved by blind labels.
    """

    matches: list[re.Match[str]] = []
    for match in REGISTER_CALL.finditer(masked):
        prefix = masked[max(0, match.start() - 16) : match.start()]
        if re.search(r"\bfunc\s*$", prefix):
            continue
        matches.append(match)
    matches.extend(DIRECT_REGISTER_SERVICE_CALL.finditer(masked))
    return sorted(matches, key=lambda match: match.start(1))


def scan_registration_recall_frame(
    system: str,
    root: Path,
    commit: str,
    tracked_files: Sequence[str],
) -> list[dict[str, Any]]:
    population: list[dict[str, Any]] = []
    seen: set[str] = set()
    go_paths = sorted(path for path in tracked_files if path.endswith(".go"))
    for rel, blob in git_blobs(root, commit, go_paths):
        try:
            text = blob.decode("utf-8", errors="strict")
        except UnicodeDecodeError as exc:
            raise PrepError(f"{root}:{rel}: source is not valid UTF-8: {exc}") from exc
        masked = mask_go_source(text)
        char_starts, byte_starts = source_offsets(text)
        candidate_role = source_code_role(rel, text)
        for match in registration_matches(masked):
            method = match.group(1)
            shape = (
                "direct_register_service"
                if method == "RegisterService"
                else "generated_register_helper"
            )
            line, column, byte_offset = coordinate_at(
                text, match.start(1), char_starts, byte_starts
            )
            sid = site_id(system, commit, rel, byte_offset, method)
            if sid in seen:
                raise PrepError(f"duplicate registration candidate at {system}:{rel}:{line}:{column}")
            seen.add(sid)
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
                    "stratum": f"{system}|{shape}:{candidate_role}",
                    "registration_shape": shape,
                    "candidate_role": candidate_role,
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
    prefix = f"corpus/{system}/"
    if normalized.startswith(prefix):
        return normalized[len(prefix) :]
    return normalized


def operation_facts(
    system: str, facts_dir: Path, expected_commit: str, corpus_dir: Path
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
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
    calls = [row for row in verified if row.get("predicate") == "CALLS_OPERATION"]
    registrations = [row for row in verified if row.get("predicate") == "IMPLEMENTS_SERVICE"]
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
    for row in [*calls, *registrations]:
        missing = required - row.keys()
        if missing:
            raise PrepError(f"{path}: {row.get('predicate')} missing {sorted(missing)}")
        if row.get("commit") != expected_commit:
            raise PrepError(
                f"{path}: {row.get('predicate')} commit {row.get('commit')!r} does not match "
                f"locked commit {expected_commit}"
            )
    for row in registrations:
        if not FULL_SERVICE_RE.fullmatch(str(row.get("object", ""))):
            raise PrepError(
                f"{path}: IMPLEMENTS_SERVICE object must be a fully-qualified proto service: "
                f"{row.get('object')!r}"
            )
    return calls, registrations


def gate_call_facts(
    system: str, calls: Sequence[dict[str, Any]]
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Separate canonical exact/derived calls from disclosed abstention debt."""

    eligible: list[dict[str, Any]] = []
    quarantined: list[dict[str, Any]] = []
    for fact in calls:
        tier = str(fact.get("tier"))
        operation = str(fact.get("object"))
        canonical = bool(FULL_OPERATION_RE.fullmatch(operation)) and "?" not in operation
        if tier in {"exact", "derived"}:
            if not canonical:
                raise PrepError(
                    f"{system}: {tier} CALLS_OPERATION has non-canonical identity {operation!r}"
                )
            eligible.append(fact)
        else:
            quarantined.append(fact)
    tiers = dict(sorted(Counter(str(row.get("tier")) for row in quarantined).items()))
    return eligible, {
        "count": len(quarantined),
        "by_tier": tiers,
        "reason": "non-canonical heuristic/unresolved calls are abstention debt, not Gate-2 edges",
        "fact_fingerprints_sha256": "sha256:" + digest_parts(
            "t111-gate2-quarantined-call-facts-v1",
            sorted(str(row.get("fact_fingerprint")) for row in quarantined),
        ),
    }


def call_facts(
    system: str, facts_dir: Path, expected_commit: str, corpus_dir: Path
) -> list[dict[str, Any]]:
    """Compatibility projection retained for diagnostic callers."""

    calls, _ = operation_facts(system, facts_dir, expected_commit, corpus_dir)
    return calls


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
        sid = site_id(system, commit, rel, byte_offset, method)
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


def registration_position(data: bytes, fact: dict[str, Any]) -> tuple[int, str]:
    start = int(fact["start_byte"])
    end = int(fact["end_byte"])
    if start < 0 or end <= start or end > len(data):
        raise PrepError(
            f"invalid registration span {start}:{end} for {fact.get('path')} ({len(data)} bytes)"
        )
    service = str(fact.get("object", ""))
    short_service = service.rsplit(".", 1)[-1]
    methods = (f"Register{short_service}Server", "RegisterService")
    matches: list[tuple[int, str]] = []
    for method in methods:
        matches.extend(
            (match.start(), method)
            for match in re.finditer(
                rb"\b" + re.escape(method.encode()) + rb"[ \t\r\n]*\(",
                data[start:end],
            )
        )
    if len(matches) != 1:
        raise PrepError(
            f"expected one {methods[0]} or RegisterService call in registration fact span "
            f"{fact.get('path')}:{start}:{end}; found {len(matches)}"
        )
    relative_offset, method = matches[0]
    return start + relative_offset, method


def build_registration_precision_frame(
    system: str,
    root: Path,
    commit: str,
    facts: Sequence[dict[str, Any]],
    tracked_files: set[str],
) -> list[dict[str, Any]]:
    files: dict[str, bytes] = {}
    population: list[dict[str, Any]] = []
    seen: set[str] = set()
    for fact in facts:
        rel = relpath_of(str(fact["path"]), system, root)
        if rel not in tracked_files:
            raise PrepError(
                f"emitted registration does not resolve to a pinned tracked file: {system}:{rel}"
            )
        data = files.setdefault(rel, git_blob(root, commit, rel))
        byte_offset, method = registration_position(data, fact)
        line, column = byte_coordinate(data, byte_offset)
        sid = site_id(system, commit, rel, byte_offset, method)
        if sid in seen:
            raise PrepError(f"multiple IMPLEMENTS_SERVICE facts map to source site {sid}")
        seen.add(sid)
        role = str(fact["code_role"])
        if role not in CODE_ROLES:
            raise PrepError(
                f"IMPLEMENTS_SERVICE has unsupported code_role {role!r}: {system}:{rel}"
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


def align_registration(
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


def build_role_population(
    calls: Sequence[dict[str, Any]],
    registrations: Sequence[dict[str, Any]],
    *,
    emitted_only: bool = False,
) -> list[dict[str, Any]]:
    """Build source-derived role references, optionally from emitted facts only."""

    by_site: dict[str, dict[str, Any]] = {}
    for family, rows in (("client_call", calls), ("registration", registrations)):
        for row in rows:
            if emitted_only and row.get("extracted") is not True:
                continue
            # Role accuracy needs independently selected references, but a
            # uniform draw from every exported selector would fill vendor and
            # generated strata with unrelated calls. Keep the source-only
            # gRPC risk signals and all broad registration candidates; labels
            # still make the final eligibility decision.
            if family == "client_call" and row.get("risk") == "other_exported_selector":
                continue
            sid = str(row["site_id"])
            prior = by_site.get(sid)
            if prior is not None:
                if prior["candidate_role"] != row["candidate_role"]:
                    raise PrepError(f"conflicting source-derived roles at {sid}")
                prior["families"] = sorted(set(prior["families"] + [family]))
                if row.get("extracted"):
                    prior["emitted_roles"] = sorted(
                        set(prior["emitted_roles"] + [str(row.get("role"))])
                    )
                continue
            emitted_roles = [str(row.get("role"))] if row.get("extracted") else []
            by_site[sid] = {
                **row,
                "role": row["candidate_role"],
                "stratum": f"role|{row['candidate_role']}",
                "families": [family],
                "emitted_roles": emitted_roles,
            }
    return sorted(by_site.values(), key=lambda row: row["site_id"])


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
    seed: str | int,
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


def select_role_cohort(
    population: Sequence[dict[str, Any]],
    per_role: int,
    seed: str | int,
    purpose: str,
    excluded: set[str] | None = None,
    *,
    require_full_quota: bool,
) -> list[dict[str, Any]]:
    excluded = excluded or set()
    selected: list[dict[str, Any]] = []
    for role in sorted(CODE_ROLES):
        eligible = [
            row
            for row in population
            if row.get("role") == role and row["site_id"] not in excluded
        ]
        if require_full_quota and len(eligible) < per_role:
            raise PrepError(
                f"role cohort cannot sample {per_role} {role} references; only {len(eligible)} eligible"
            )
        ordered = sorted(
            eligible,
            key=lambda row: (rank(seed, purpose, role, str(row["site_id"])), row["site_id"]),
        )
        selected.extend(ordered[:per_role])
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


def gate_sampling_config() -> dict[str, int]:
    return {
        "recall_holdout_per_system": GATE_RECALL_HOLDOUT_PER_SYSTEM,
        "recall_dev_per_system": GATE_RECALL_DEV_PER_SYSTEM,
        "precision_holdout_per_system": GATE_PRECISION_HOLDOUT_PER_SYSTEM,
        "precision_dev_per_system": GATE_PRECISION_DEV_PER_SYSTEM,
        "recall_min_per_stratum": GATE_RECALL_MIN_PER_STRATUM,
        "precision_min_per_stratum": GATE_PRECISION_MIN_PER_STRATUM,
        "registration_recall_holdout_per_system": GATE_REGISTRATION_RECALL_HOLDOUT_PER_SYSTEM,
        "registration_recall_dev_per_system": GATE_REGISTRATION_RECALL_DEV_PER_SYSTEM,
        "registration_precision_holdout_per_system": GATE_REGISTRATION_PRECISION_HOLDOUT_PER_SYSTEM,
        "registration_precision_dev_per_system": GATE_REGISTRATION_PRECISION_DEV_PER_SYSTEM,
        "registration_recall_min_per_stratum": GATE_REGISTRATION_RECALL_MIN_PER_STRATUM,
        "registration_precision_min_per_stratum": GATE_REGISTRATION_PRECISION_MIN_PER_STRATUM,
        "role_holdout_per_role": GATE_ROLE_HOLDOUT_PER_ROLE,
        "role_dev_per_role": GATE_ROLE_DEV_PER_ROLE,
    }


def requested_sampling_config(args: argparse.Namespace) -> dict[str, int]:
    defaults = gate_sampling_config()
    return {name: getattr(args, name, value) for name, value in defaults.items()}


def _hypergeom_probability(N: int, K: int, n: int, low: int, high: int) -> Fraction:
    low = max(low, 0, n - (N - K))
    high = min(high, n, K)
    if low > high:
        return Fraction(0, 1)
    return Fraction(
        sum(math.comb(K, x) * math.comb(N - K, n - x) for x in range(low, high + 1)),
        math.comb(N, n),
    )


def _lower_total(N: int, n: int, k: int, alpha: Fraction) -> int:
    if n == 0 or k == 0:
        return 0
    if n == N:
        return k
    lo, hi = k, N - (n - k)
    while lo < hi:
        mid = (lo + hi) // 2
        if _hypergeom_probability(N, mid, n, k, n) >= alpha:
            hi = mid
        else:
            lo = mid + 1
    return lo


def _upper_total(N: int, n: int, k: int, alpha: Fraction) -> int:
    if n == 0:
        return N
    if n == N:
        return k
    if k == n:
        return N
    lo, hi = k, N - (n - k)
    while lo < hi:
        mid = (lo + hi + 1) // 2
        if _hypergeom_probability(N, mid, n, 0, k) >= alpha:
            lo = mid
        else:
            hi = mid - 1
    return lo


def confidence_bound_family_size(frames: dict[str, Any]) -> int:
    """Count only genuinely uncertain finite-population quantities.

    Census strata are observed exactly and consume no simultaneous-error
    budget. Precision contributes one success-total bound; recall contributes
    separate captured and missed totals.
    """

    total = 0
    for frame in (
        FRAME_CALL_PRECISION,
        FRAME_CALL_RECALL,
        FRAME_REGISTRATION_PRECISION,
        FRAME_REGISTRATION_RECALL,
    ):
        multiplier = 2 if frame in {
            FRAME_CALL_RECALL,
            FRAME_REGISTRATION_RECALL,
        } else 1
        total += multiplier * sum(
            int(row["holdout_sample_size"]) < int(row["sample_population_size"])
            for row in frames[frame]["strata"]
            if int(row["sample_population_size"]) > 0
        )
    return total


def preflight_gate_design(
    manifest: dict[str, Any],
    *,
    raise_on_failure: bool = True,
) -> dict[str, Any]:
    """Reject a statistically incapable design without inspecting a realized sample."""

    frames = manifest["frames"]
    bounded = (
        FRAME_CALL_PRECISION,
        FRAME_CALL_RECALL,
        FRAME_REGISTRATION_PRECISION,
        FRAME_REGISTRATION_RECALL,
    )
    family_size = confidence_bound_family_size(frames)
    reasons: list[str] = []
    if family_size <= 0:
        alpha_each = Fraction(0, 1)
    else:
        alpha_each = (Fraction(1, 1) - GATE_CONFIDENCE) / family_size

    report: dict[str, Any] = {
        "schema": "t111-gate2-power-v1",
        "family_size": family_size,
        "alpha_each": f"{alpha_each.numerator}/{alpha_each.denominator}",
        "best_case": {},
    }
    for frame in bounded:
        for row in frames[frame]["strata"]:
            if (
                int(row["sample_population_size"]) > 0
                and int(row["holdout_sample_size"]) <= 0
            ):
                reasons.append(
                    f"{frame} stratum {row['id']} has zero holdout inclusion probability"
                )

    def precision_bound(frame: str, scope: str | None = None) -> Fraction:
        selected = [
            row
            for row in frames[frame]["strata"]
            if scope is None or row["system"] == scope
        ]
        total = sum(int(row["population_size"]) for row in selected)
        lower = sum(
            int(row["census_size"])
            + _lower_total(
                int(row["sample_population_size"]),
                int(row["holdout_sample_size"]),
                int(row["holdout_sample_size"]),
                alpha_each,
            )
            for row in selected
        )
        return Fraction(lower, total) if total else Fraction(0, 1)

    def recall_bound(frame: str) -> Fraction:
        lower = upper_missed = 0
        for row in frames[frame]["strata"]:
            N, n = int(row["sample_population_size"]), int(row["holdout_sample_size"])
            # Power is a design property. Never inspect the selected unit's
            # hidden extractor outcome here: after public entropy fixes one
            # sample, even an unfavorable sample must be published and scored.
            lower += int(row["census_size"]) + _lower_total(N, n, n, alpha_each)
            upper_missed += _upper_total(N, n, 0, alpha_each)
        return Fraction(lower, lower + upper_missed) if lower + upper_missed else Fraction(0, 1)

    for family, precision_frame, recall_frame in (
        ("client_call", FRAME_CALL_PRECISION, FRAME_CALL_RECALL),
        ("registration", FRAME_REGISTRATION_PRECISION, FRAME_REGISTRATION_RECALL),
    ):
        overall = precision_bound(precision_frame)
        recall = recall_bound(recall_frame)
        fixtures = {
            system: precision_bound(precision_frame, system)
            for system in manifest["systems"]
        }
        report["best_case"][family] = {
            "precision_lower": f"{overall.numerator}/{overall.denominator}",
            "recall_lower": f"{recall.numerator}/{recall.denominator}",
            "fixture_precision_lower": {
                system: f"{bound.numerator}/{bound.denominator}"
                for system, bound in fixtures.items()
            },
        }
        if overall < GATE_PRECISION_THRESHOLD:
            reasons.append(f"{family} best-case precision bound is below 98%")
        if recall < GATE_RECALL_THRESHOLD:
            reasons.append(f"{family} best-case recall bound is below 90%")
        for system, bound in fixtures.items():
            if bound < GATE_FIXTURE_PRECISION_THRESHOLD:
                reasons.append(
                    f"{family} {system} best-case fixture precision bound is below 90%"
                )

    role_strata = {row["label"]: row for row in frames[FRAME_ROLE]["strata"]}
    for role in sorted(CODE_ROLES):
        row = role_strata.get(role)
        if (
            row is None
            or int(row["population_size"]) <= 0
            or int(row["holdout_sample_size"])
            != min(GATE_ROLE_HOLDOUT_PER_ROLE, int(row["sample_population_size"]))
            or int(row["census_size"]) + int(row["holdout_sample_size"]) <= 0
        ):
            reasons.append(f"role cohort lacks the fixed/census {role} holdout quota")

    # These are worst-case unique-site bounds fixed by N/n and the disclosed
    # census before external entropy exists.  Frame overlap can only increase
    # the realized blind fraction: the holdout union is at least the largest
    # frame sample, while the development union is at most the sum of all
    # frame quotas.
    census_total = int(manifest.get("holdout", {}).get("unique_census_sites", 0))
    holdout_unique_floor = max(
        (
            sum(int(row["holdout_sample_size"]) for row in frames[frame]["strata"])
            for frame in FRAMES
        ),
        default=0,
    )
    dev_unique_ceiling = sum(
        int(row["dev_sample_size"])
        for frame in FRAMES
        for row in frames[frame]["strata"]
    )
    selected_unique_ceiling = census_total + holdout_unique_floor + dev_unique_ceiling
    blind_fraction_lower_bound = (
        holdout_unique_floor / selected_unique_ceiling
        if selected_unique_ceiling
        else 0.0
    )
    if blind_fraction_lower_bound < MIN_BLIND_HOLDOUT_FRACTION:
        reasons.append("seed-independent blind holdout lower bound is below 30%")
    maximum_recall_label_units = sum(
        int(row["census_size"]) + int(row["holdout_sample_size"])
        for frame in (FRAME_CALL_RECALL, FRAME_REGISTRATION_RECALL)
        for row in frames[frame]["strata"]
    )
    if maximum_recall_label_units < MIN_POSITIVES + MIN_HARD_NEGATIVES:
        reasons.append("sample cannot contain the required 200 positives and 100 hard negatives")
    report["best_case"]["census_unique_sites"] = census_total
    report["best_case"]["holdout_unique_floor"] = holdout_unique_floor
    report["best_case"]["dev_unique_ceiling"] = dev_unique_ceiling
    report["best_case"]["selected_unique_ceiling"] = selected_unique_ceiling
    report["best_case"]["maximum_recall_label_units"] = maximum_recall_label_units
    report["best_case"]["blind_fraction_lower_bound"] = blind_fraction_lower_bound
    report["attainable"] = not reasons
    report["reasons"] = reasons
    if reasons and raise_on_failure:
        raise PrepError("sealed Gate-2 design is statistically incapable: " + "; ".join(reasons))
    return report


def build_artifacts(args: argparse.Namespace) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    systems = tuple(item.strip() for item in args.systems.split(",") if item.strip())
    if not systems or len(systems) != len(set(systems)):
        raise PrepError("systems must be a nonempty unique list")
    unknown = set(systems) - set(SYSTEMS)
    if unknown:
        raise PrepError(f"unknown systems: {sorted(unknown)}")
    sampling_config = requested_sampling_config(args)
    if any(not isinstance(value, int) or value < 0 for value in sampling_config.values()):
        raise PrepError("sample sizes and per-stratum minima must be non-negative integers")
    gate_candidate = systems == SYSTEMS and sampling_config == gate_sampling_config()
    commitment_only = bool(getattr(args, "commitment_only", False))
    if commitment_only and not gate_candidate:
        raise PrepError(
            "commit-inputs is fixed to all four fixtures and the precommitted gate design"
        )
    if gate_candidate and not getattr(args, "commitment_only", False):
        if getattr(args, "dry_run", False):
            raise PrepError(
                "--dry-run is forbidden for sealed Gate-2 generation; use commit-inputs"
            )
        output_dir = getattr(args, "output_dir", None)
        if output_dir is not None:
            output = Path(output_dir).absolute()
            if output.exists() or output.is_symlink():
                raise PrepError(f"sealed Gate-2 destination already exists: {output}")
    if gate_candidate and getattr(args, "force", False):
        raise PrepError("--force is forbidden for sealed Gate-2 generation")
    if gate_candidate and getattr(args, "seed", LEGACY_DIAGNOSTIC_SEED) != LEGACY_DIAGNOSTIC_SEED:
        raise PrepError("--seed is diagnostic-only; sealed Gate-2 requires --audit-seed-file")

    prep_script_digest = sha256_file(Path(__file__))
    fact_verifier_digest = sha256_file(BASE / "gate34_common.py")
    typed_oracle_digest = typed_oracle_code_digest()

    lock_sha256 = sha256_file(args.corpus_lock)
    entries = load_corpus_entries(args.corpus_lock, systems)
    if sha256_file(args.corpus_lock) != lock_sha256:
        raise PrepError("corpus.lock changed while it was being parsed")
    commits = {system: str(entries[system]["commit"]) for system in systems}
    burn_ledger_path = Path(getattr(args, "burn_ledger", DEFAULT_BURN_LEDGER))
    _, burn_binding, active_burns = scoped_burn_binding(
        burn_ledger_path, commits, args.corpus_dir
    )

    populations: dict[str, list[dict[str, Any]]] = {frame: [] for frame in FRAMES}
    census_populations: dict[str, list[dict[str, Any]]] = {
        frame: [] for frame in FRAMES
    }
    facts_sha256: dict[str, str] = {}
    independent_method_counts: dict[str, int] = {}
    typed_oracle_diagnostics: dict[str, dict[str, Any]] = {}
    quarantined_call_facts: dict[str, dict[str, Any]] = {}
    special_entries: dict[str, list[dict[str, str]]] = {}
    tracked_by_system: dict[str, list[str]] = {}
    oracle_toolchain: dict[str, str] | None = None
    producer_toolchain_identity: str | None = None
    for system in systems:
        root = args.corpus_dir / system
        if not root.is_dir():
            raise PrepError(f"missing corpus checkout: {root}")
        tracked, special = pinned_tracked_files(
            root, commits[system], entries[system]["excluded_gitlinks"]
        )
        tracked_by_system[system] = tracked
        special_entries[system] = special
        fact_path = args.facts_dir / f"{system}.facts.jsonl"
        fact_digest = sha256_file(fact_path)
        call_raw, registration_raw = operation_facts(
            system, args.facts_dir, commits[system], args.corpus_dir
        )
        identities = {
            str(row.get("toolchain_identity"))
            for row in [*call_raw, *registration_raw]
        }
        if len(identities) != 1:
            raise PrepError(f"{system}: operation facts do not bind one exact toolchain")
        identity = next(iter(identities))
        if producer_toolchain_identity is None:
            producer_toolchain_identity = identity
            oracle_toolchain = resolve_oracle_toolchain(identity)
        elif identity != producer_toolchain_identity:
            raise PrepError("fixture fact files were produced by different toolchains")
        call_raw, quarantined_call_facts[system] = gate_call_facts(system, call_raw)
        all_call_candidates, oracle_diagnostics = scan_typed_call_recall_frame(
            system,
            root,
            commits[system],
            entries[system]["go_build_tags"],
            set(tracked),
            oracle_toolchain,
        )
        typed_oracle_diagnostics[system] = oracle_diagnostics
        independent_method_counts[system] = len(all_call_candidates)
        registration_recall = scan_registration_recall_frame(
            system, root, commits[system], tracked
        )
        if sha256_file(fact_path) != fact_digest:
            raise PrepError(f"extractor facts changed during frame construction: {fact_path}")
        call_precision = build_precision_frame(
            system, root, commits[system], call_raw, set(tracked)
        )
        registration_precision = build_registration_precision_frame(
            system, root, commits[system], registration_raw, set(tracked)
        )
        align_recall(all_call_candidates, call_precision)
        align_registration(registration_recall, registration_precision)
        typed_sites = {row["site_id"] for row in all_call_candidates}
        outside_target = [row for row in call_precision if row["site_id"] not in typed_sites]
        if outside_target:
            raise PrepError(
                f"{system}: independent typed recall oracle excludes "
                f"{len(outside_target)} emitted calls; fix the oracle before sampling"
            )
        registration_sites = {row["site_id"] for row in registration_recall}
        registration_outside_target = [
            row for row in registration_precision if row["site_id"] not in registration_sites
        ]
        if registration_outside_target:
            raise PrepError(
                f"{system}: independent registration recall frame excludes "
                f"{len(registration_outside_target)} emitted registrations"
            )
        role_population = build_role_population(
            all_call_candidates, registration_recall, emitted_only=True
        )
        active = active_burns[system]
        frame_rows = {
            FRAME_CALL_RECALL: all_call_candidates,
            FRAME_CALL_PRECISION: call_precision,
            FRAME_REGISTRATION_RECALL: registration_recall,
            FRAME_REGISTRATION_PRECISION: registration_precision,
            FRAME_ROLE: role_population,
        }
        burned_site_ids = {
            str(row["site_id"])
            for rows in frame_rows.values()
            for row in rows
            if site_coordinate_burned(row, active)
        }
        for frame, rows in frame_rows.items():
            fresh = [row for row in rows if str(row["site_id"]) not in burned_site_ids]
            census = [row for row in rows if str(row["site_id"]) in burned_site_ids]
            populations[frame].extend(fresh)
            census_populations[frame].extend(census)
        facts_sha256[fact_path.name] = fact_digest

    population_binding = {
        frame: {
            "population_size": len(rows) + len(census_populations[frame]),
            "sample_population_size": len(rows),
            "census_population_size": len(census_populations[frame]),
            "sha256": "sha256:" + digest_parts(
                "t111-gate2-frame-v1",
                frame,
                {
                    "sample": sorted(rows, key=canonical_json),
                    "census": sorted(census_populations[frame], key=canonical_json),
                },
            ),
        }
        for frame, rows in populations.items()
    }
    corpus_inputs = {
        system: {
            "commit": commits[system],
            "excluded_gitlinks": entries[system]["excluded_gitlinks"],
            "go_build_tags": entries[system]["go_build_tags"],
        }
        for system in systems
    }
    selection_bindings = frame_selection_bindings(
        systems=systems,
        sampling_config=sampling_config,
        corpus_lock_sha256=lock_sha256,
        burn_binding=burn_binding,
        corpus_inputs=corpus_inputs,
        populations=populations,
        prep_script_sha256=prep_script_digest,
        typed_call_oracle_sha256=typed_oracle_digest,
        typed_call_oracle_diagnostics=typed_oracle_diagnostics,
    )
    binding_arguments = dict(
        systems=systems,
        sampling_config=sampling_config,
        corpus_lock_sha256=lock_sha256,
        facts_sha256=facts_sha256,
        burn_binding=burn_binding,
        corpus_inputs=corpus_inputs,
        population_binding=population_binding,
        selection_bindings=selection_bindings,
        prep_script_sha256=prep_script_digest,
        fact_verifier_sha256=fact_verifier_digest,
        typed_call_oracle_sha256=typed_oracle_digest,
        typed_call_oracle_diagnostics=typed_oracle_diagnostics,
    )
    base_input_binding = seed_input_binding(**binding_arguments)
    input_committed_at: str | None = None
    attempt_claim: dict[str, Any] | None = None
    if gate_candidate:
        rebuild_commitment_time = getattr(args, "rebuild_input_committed_at", None)
        rebuild_attempt_claim = getattr(args, "rebuild_attempt_claim", None)
        if rebuild_commitment_time is not None:
            parse_rfc3339("rebuild input committed_at", rebuild_commitment_time)
            input_committed_at = str(rebuild_commitment_time)
            attempt_claim = validate_attempt_claim(
                rebuild_attempt_claim,
                commits=commits,
                base_input_binding=base_input_binding,
            )
            if attempt_claim["committed_at"] != input_committed_at:
                raise PrepError("bundled attempt timestamp does not match audit seed")
        else:
            attempt_claim = locked_attempt_claim(
                commits=commits,
                base_input_binding=base_input_binding,
                input_binding=None,
                committed_at=None,
                create=False,
                allow_missing=commitment_only,
            )
            input_committed_at = (
                str(attempt_claim["committed_at"])
                if attempt_claim is not None
                else (
                    dt.datetime.now(dt.timezone.utc) + COMMITMENT_ACTIVATION_LEAD
                ).isoformat().replace("+00:00", "Z")
            )
    input_binding = seed_input_binding(
        **binding_arguments, input_committed_at=input_committed_at
    )
    if attempt_claim is not None:
        attempt_claim = validate_attempt_claim(
            attempt_claim,
            commits=commits,
            base_input_binding=base_input_binding,
            input_binding=input_binding,
        )
    seed_path = getattr(args, "audit_seed_file", None)
    if gate_candidate:
        if seed_path is None and not commitment_only:
            raise PrepError(
                "sealed Gate-2 generation requires --audit-seed-file after inputs are locked"
            )
        if commitment_only:
            # The private deterministic rank exists only to exercise allocation
            # and exact power before an auditor supplies entropy. No coordinate
            # or hidden extractor outcome is emitted by ``commit-inputs``.
            seed_record = None
            selection_seeds = {
                frame: digest_parts(
                    "t111-gate2-input-preflight-v2",
                    frame,
                    selection_bindings[frame],
                )
                for frame in FRAMES
            }
            randomization = {
                "mode": "input-commitment-preflight",
                "input_binding": input_binding,
                "input_committed_at": input_committed_at,
                "base_input_binding": base_input_binding,
                "selection_bindings": selection_bindings,
            }
        else:
            seed_record = load_seed_record(
                seed_path,
                expected_input_binding=input_binding,
                expected_input_committed_at=input_committed_at,
            )
            selection_seeds = audited_selection_seeds(
                selection_bindings, seed_record
            )
            randomization = {
                "mode": "audited-unpredictable-sha256-rank-v2",
                "input_binding": input_binding,
                "input_committed_at": input_committed_at,
                "base_input_binding": base_input_binding,
                "seed_record": seed_record,
                "selection_bindings": selection_bindings,
                "selection_seed_digests": {
                    frame: "sha256:" + hashlib.sha256(
                        selection_seeds[frame].encode()
                    ).hexdigest()
                    for frame in FRAMES
                },
            }
    else:
        seed_record = None
        diagnostic_seed = getattr(args, "seed", LEGACY_DIAGNOSTIC_SEED)
        selection_seeds = {frame: diagnostic_seed for frame in FRAMES}
        randomization = {
            "mode": "legacy-diagnostic-fixed-seed",
            "input_binding": input_binding,
            "seed": diagnostic_seed,
        }

    holdouts = {
        FRAME_CALL_RECALL: select_by_system(
            populations[FRAME_CALL_RECALL], sampling_config["recall_holdout_per_system"],
            sampling_config["recall_min_per_stratum"], selection_seeds[FRAME_CALL_RECALL], "call-recall-holdout"
        ),
        FRAME_CALL_PRECISION: select_by_system(
            populations[FRAME_CALL_PRECISION], sampling_config["precision_holdout_per_system"],
            sampling_config["precision_min_per_stratum"], selection_seeds[FRAME_CALL_PRECISION], "call-precision-holdout"
        ),
        FRAME_REGISTRATION_RECALL: select_by_system(
            populations[FRAME_REGISTRATION_RECALL], sampling_config["registration_recall_holdout_per_system"],
            sampling_config["registration_recall_min_per_stratum"], selection_seeds[FRAME_REGISTRATION_RECALL], "registration-recall-holdout"
        ),
        FRAME_REGISTRATION_PRECISION: select_by_system(
            populations[FRAME_REGISTRATION_PRECISION], sampling_config["registration_precision_holdout_per_system"],
            sampling_config["registration_precision_min_per_stratum"], selection_seeds[FRAME_REGISTRATION_PRECISION], "registration-precision-holdout"
        ),
        FRAME_ROLE: select_role_cohort(
            populations[FRAME_ROLE], sampling_config["role_holdout_per_role"], selection_seeds[FRAME_ROLE],
            "role-holdout", require_full_quota=False
        ),
    }
    holdout_ids = {row["site_id"] for rows in holdouts.values() for row in rows}
    dev = {
        FRAME_CALL_RECALL: select_by_system(
            populations[FRAME_CALL_RECALL], sampling_config["recall_dev_per_system"],
            sampling_config["recall_min_per_stratum"], selection_seeds[FRAME_CALL_RECALL], "call-recall-dev", holdout_ids
        ),
        FRAME_CALL_PRECISION: select_by_system(
            populations[FRAME_CALL_PRECISION], sampling_config["precision_dev_per_system"],
            sampling_config["precision_min_per_stratum"], selection_seeds[FRAME_CALL_PRECISION], "call-precision-dev", holdout_ids
        ),
        FRAME_REGISTRATION_RECALL: select_by_system(
            populations[FRAME_REGISTRATION_RECALL], sampling_config["registration_recall_dev_per_system"],
            sampling_config["registration_recall_min_per_stratum"], selection_seeds[FRAME_REGISTRATION_RECALL], "registration-recall-dev", holdout_ids
        ),
        FRAME_REGISTRATION_PRECISION: select_by_system(
            populations[FRAME_REGISTRATION_PRECISION], sampling_config["registration_precision_dev_per_system"],
            sampling_config["registration_precision_min_per_stratum"], selection_seeds[FRAME_REGISTRATION_PRECISION], "registration-precision-dev", holdout_ids
        ),
        FRAME_ROLE: select_role_cohort(
            populations[FRAME_ROLE], sampling_config["role_dev_per_role"], selection_seeds[FRAME_ROLE],
            "role-dev", holdout_ids, require_full_quota=False
        ),
    }

    selected: dict[str, dict[str, Any]] = {}

    def add_membership(frame: str, split: str, row: dict[str, Any]) -> None:
        sid = str(row["site_id"])
        entry = selected.setdefault(
            sid,
            {**coordinate_row(row), "byte_offset": row["byte_offset"], "split": split, "frames": {}},
        )
        if entry["split"] != split:
            raise PrepError(f"source site leaked across census/dev/holdout: {sid}")
        if frame in entry["frames"]:
            raise PrepError(f"duplicate {frame} membership for {sid}")
        membership: dict[str, Any] = {"stratum": row["stratum"]}
        if frame in {FRAME_CALL_RECALL, FRAME_REGISTRATION_RECALL}:
            membership.update(
                extracted=bool(row["extracted"]), object=row.get("object"), role=row.get("role"),
                tier=row.get("tier"), atom_id=row.get("atom_id")
            )
        elif frame == FRAME_ROLE:
            membership.update(
                candidate_role=row["candidate_role"], emitted_roles=row["emitted_roles"],
                families=row["families"]
            )
        else:
            membership.update(
                object=row["object"], role=row["role"], tier=row["tier"], atom_id=row.get("atom_id")
            )
        entry["frames"][frame] = membership

    for frame in FRAMES:
        for row in census_populations[frame]:
            add_membership(frame, "census", row)
        for row in holdouts[frame]:
            add_membership(frame, "holdout", row)
        for row in dev[frame]:
            add_membership(frame, "dev", row)

    strata: dict[str, list[dict[str, Any]]] = {}
    for frame, population in populations.items():
        records: list[dict[str, Any]] = []
        fresh_by_stratum = group_by_stratum(population)
        census_by_stratum = group_by_stratum(census_populations[frame])
        for stratum in sorted(set(fresh_by_stratum) | set(census_by_stratum)):
            rows = fresh_by_stratum.get(stratum, [])
            census_rows = census_by_stratum.get(stratum, [])
            hold_n = sum(
                entry["split"] == "holdout"
                and frame in entry["frames"]
                and entry["frames"][frame]["stratum"] == stratum
                for entry in selected.values()
            )
            dev_n = sum(
                entry["split"] == "dev"
                and frame in entry["frames"]
                and entry["frames"][frame]["stratum"] == stratum
                for entry in selected.values()
            )
            census_n = sum(
                entry["split"] == "census"
                and frame in entry["frames"]
                and entry["frames"][frame]["stratum"] == stratum
                for entry in selected.values()
            )
            if census_n != len(census_rows):
                raise PrepError(f"{frame}/{stratum}: census membership mismatch")
            system, label = stratum.split("|", 1)
            records.append({
                "id": stratum, "system": system, "label": label,
                "population_size": len(rows) + census_n,
                "sample_population_size": len(rows),
                "census_size": census_n,
                "census_inclusion_probability": 1,
                "holdout_sample_size": hold_n,
                "holdout_inclusion_probability": hold_n / len(rows) if rows else 0,
                "dev_sample_size": dev_n, "dev_inclusion_probability": None,
            })
        strata[frame] = records

    dev_sites = sorted(
        (coordinate_row(row) for row in selected.values() if row["split"] == "dev"),
        key=lambda row: row["site_id"],
    )
    holdout_sites = sorted(
        (coordinate_row(row) for row in selected.values() if row["split"] == "holdout"),
        key=lambda row: row["site_id"],
    )
    census_sites = sorted(
        (coordinate_row(row) for row in selected.values() if row["split"] == "census"),
        key=lambda row: row["site_id"],
    )
    keys = sorted(selected.values(), key=lambda row: row["site_id"])
    active_burned = [row for rows in active_burns.values() for row in rows]
    validate_rows(dev_sites, holdout_sites, keys, strata, active_burned, census_sites)
    selected_total = len(census_sites) + len(dev_sites) + len(holdout_sites)
    blind_fraction = len(holdout_sites) / selected_total if selected_total else 0.0

    for system in systems:
        post_files, post_special = pinned_tracked_files(
            args.corpus_dir / system, commits[system], entries[system]["excluded_gitlinks"]
        )
        if post_files != tracked_by_system[system] or post_special != special_entries[system]:
            raise PrepError(f"{system}: pinned tree changed during frame construction")
    if sha256_file(args.corpus_lock) != lock_sha256:
        raise PrepError("corpus.lock changed during frame construction")
    for name, digest in facts_sha256.items():
        if sha256_file(args.facts_dir / name) != digest:
            raise PrepError(f"extractor facts changed during frame construction: {name}")
    verify_code_snapshots(
        prep_script_digest, fact_verifier_digest, typed_oracle_digest
    )

    frame_definitions = {
        FRAME_CALL_RECALL: (
            "typed Go call expressions whose receiver implements an independently "
            "discovered generated or structural gRPC *Client interface; exact named, "
            "concrete, embedded, structural, and multiply-matching receivers are separate strata"
        ),
        FRAME_CALL_PRECISION: (
            "all emitted exact/derived CALLS_OPERATION facts with canonical operation identity"
        ),
        FRAME_REGISTRATION_RECALL: (
            "all call-shaped Register<Name>Server and direct RegisterService source candidates"
        ),
        FRAME_REGISTRATION_PRECISION: "all emitted IMPLEMENTS_SERVICE facts",
        FRAME_ROLE: (
            "fixed probability sample (or sparse-role census) of emitted client and "
            "registration references, stratified by "
            "source-derived expected code role"
        ),
    }
    manifest = {
        "schema": SCHEMA,
        "systems": list(systems),
        "gate_design_fixed": gate_candidate,
        "sampling_config": sampling_config,
        "decision_rule": gate_decision_rule(),
        "burn_ledger": burn_binding,
        "coordinate_only_sites": True,
        "randomization": randomization,
        "attempt_claim": attempt_claim,
        "holdout": {
            "blind": True,
            "selection": "audited domain-separated SHA-256 rank within declared strata",
            "inference": (
                "exact disclosed-site census plus probability-sampled blind holdout; "
                "development rows are tuning diagnostics"
            ),
            "unique_dev_sites": len(dev_sites), "unique_holdout_sites": len(holdout_sites),
            "unique_census_sites": len(census_sites),
            "blind_fraction": blind_fraction, "minimum_blind_fraction": MIN_BLIND_HOLDOUT_FRACTION,
        },
        "benchmark_requirements": {
            "minimum_labeled_positives": MIN_POSITIVES,
            "minimum_labeled_hard_negatives": MIN_HARD_NEGATIVES,
            "evaluated_by_scorer": True,
        },
        "protocol_coverage": {
            "client_calls": "measured",
            "registration": "measured",
            "registration_predicate": "IMPLEMENTS_SERVICE",
            "role_classification": "emitted_reference_sample_with_source_roles",
            "heuristic_call_facts": "quarantined_as_abstention_debt",
            "disclosed_source_sites": "exact_census_combined_with_blind_probability_bounds",
        },
        "frames": {
            frame: {
                "definition": frame_definitions[frame],
                "extractor_independent": frame in {
                    FRAME_CALL_RECALL, FRAME_REGISTRATION_RECALL
                },
                "population_size": len(populations[frame]) + len(census_populations[frame]),
                "sample_population_size": len(populations[frame]),
                "census_population_size": len(census_populations[frame]),
                "strata": strata[frame],
            }
            for frame in FRAMES
        },
        "independent_typed_call_counts": independent_method_counts,
        "quarantined_call_facts": quarantined_call_facts,
        "provenance": {
            "locked_commits": commits,
            "corpus_inputs": corpus_inputs,
            "excluded_special_entries": special_entries,
            "corpus_lock_sha256": lock_sha256,
            "facts_sha256": facts_sha256,
            "population_binding": population_binding,
            "selection_bindings": selection_bindings,
            "prep_script_sha256": prep_script_digest,
            "fact_verifier_sha256": fact_verifier_digest,
            "typed_call_oracle_sha256": typed_oracle_digest,
            "typed_call_oracle_diagnostics": typed_oracle_diagnostics,
            "typed_call_oracle_toolchain_identity": producer_toolchain_identity,
        },
    }
    if gate_candidate:
        manifest["power_analysis"] = preflight_gate_design(
            manifest,
            raise_on_failure=not commitment_only,
        )
        if commitment_only and manifest["power_analysis"]["attainable"]:
            verify_code_snapshots(
                prep_script_digest, fact_verifier_digest, typed_oracle_digest
            )
            attempt_claim = locked_attempt_claim(
                commits=commits,
                base_input_binding=base_input_binding,
                input_binding=input_binding,
                committed_at=input_committed_at,
                create=True,
            )
            manifest["attempt_claim"] = attempt_claim
        elif seed_record is not None:
            verify_input_commitment_publication(manifest, seed_record)
    else:
        manifest["power_analysis"] = {"schema": "t111-gate2-power-v1", "attainable": False, "diagnostic_only": True}
    return manifest, dev_sites, holdout_sites, keys


def validate_rows(
    dev_sites: Sequence[dict[str, Any]],
    holdout_sites: Sequence[dict[str, Any]],
    keys: Sequence[dict[str, Any]],
    strata: dict[str, list[dict[str, Any]]],
    burned: Sequence[dict[str, Any]] = (),
    census_sites: Sequence[dict[str, Any]] = (),
) -> None:
    def unique_ids(rows: Sequence[dict[str, Any]], name: str) -> set[str]:
        ids = [str(row.get("site_id")) for row in rows]
        if len(ids) != len(set(ids)):
            raise PrepError(f"duplicate site_id in {name}")
        return set(ids)

    dev_ids = unique_ids(dev_sites, "sites.dev")
    holdout_ids = unique_ids(holdout_sites, "sites.holdout")
    census_ids = unique_ids(census_sites, "sites.census")
    key_ids = unique_ids(keys, "key")
    overlap = (dev_ids & holdout_ids) | (dev_ids & census_ids) | (holdout_ids & census_ids)
    if overlap:
        raise PrepError(f"census/dev/holdout site leakage: {sorted(overlap)[:3]}")
    if key_ids != dev_ids | holdout_ids | census_ids:
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
            raise PrepError(f"blind/dev site overlaps the disclosed census: {row['site_id']}")
    for row in census_sites:
        candidate = {
            "system": row["system"],
            "path": row["path"],
            "start_line": row["line"],
            "end_line": row["line"],
        }
        if not coordinate_burned(candidate, burned):
            raise PrepError(f"census site is not covered by burn carry-forward: {row['site_id']}")
    for row in list(census_sites) + list(dev_sites) + list(holdout_sites):
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
        expected_split = (
            "census"
            if row["site_id"] in census_ids
            else "dev"
            if row["site_id"] in dev_ids
            else "holdout"
        )
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
        if observed[(frame, stratum, "census")] != record["census_size"]:
            raise PrepError(f"census count mismatch for {frame}/{stratum}")


def publish_artifacts(
    output: Path,
    manifest: dict[str, Any],
    dev_sites: Sequence[dict[str, Any]],
    holdout_sites: Sequence[dict[str, Any]],
    keys: Sequence[dict[str, Any]],
    force: bool,
) -> None:
    census_sites = sorted(
        (coordinate_row(row) for row in keys if row.get("split") == "census"),
        key=lambda row: row["site_id"],
    )
    rows_by_name = {
        "sites.census.jsonl": census_sites,
        "sites.dev.jsonl": dev_sites,
        "sites.holdout.jsonl": holdout_sites,
        "key.jsonl": keys,
    }
    sealed = manifest.get("gate_design_fixed") is True
    if not sealed:
        existing = [
            output / name for name in (*rows_by_name, "manifest.json")
            if (output / name).exists()
        ]
        if existing and not force:
            raise PrepError(
                f"refusing to replace {', '.join(str(path) for path in existing)}; pass --force"
            )
        output.mkdir(parents=True, exist_ok=True)
        for name, rows in rows_by_name.items():
            write_jsonl(output / name, rows)
        manifest["artifacts_sha256"] = {
            name: sha256_file(output / name) for name in rows_by_name
        }
        atomic_write(
            output / "manifest.json", json.dumps(manifest, indent=2, sort_keys=True) + "\n"
        )
        return

    if force:
        raise PrepError("--force is forbidden for sealed Gate-2 publication")
    randomization = manifest.get("randomization")
    if not isinstance(randomization, dict):
        raise PrepError("sealed manifest has no randomization record")
    input_binding = randomization.get("input_binding")
    if not isinstance(input_binding, str) or not re.fullmatch(
        r"sha256:[0-9a-f]{64}", input_binding
    ):
        raise PrepError("sealed manifest has no valid input binding")
    seed_record = validate_seed_record(
        randomization.get("seed_record"),
        expected_input_binding=input_binding,
        expected_input_committed_at=randomization.get("input_committed_at"),
    )
    commits = manifest.get("provenance", {}).get("locked_commits")
    if not isinstance(commits, dict):
        raise PrepError("sealed manifest has no locked commit set")
    attempt_claim = validate_attempt_claim(
        manifest.get("attempt_claim"),
        commits={str(k): str(v) for k, v in commits.items()},
        base_input_binding=str(randomization.get("base_input_binding")),
        input_binding=input_binding,
    )
    if attempt_claim["committed_at"] != seed_record["input_committed_at"]:
        raise PrepError("attempt claim and audit seed timestamps differ")
    output = output.absolute()
    parent = output.parent
    parent.mkdir(parents=True, exist_ok=True)
    if output.exists() or output.is_symlink():
        raise PrepError(f"sealed Gate-2 destination already exists: {output}")
    # The claim namespace is fixed by the repository protocol, not by a
    # caller-controlled lock or output location.  Moving a byte-identical lock
    # therefore cannot reopen the one-shot publication window.
    claim_root = SEALED_CLAIM_ROOT.absolute()
    claim_root.mkdir(parents=True, exist_ok=True)
    claim = claim_root / f".t111-gate2-{input_binding.removeprefix('sha256:')}.sealed-claim"
    try:
        fd = os.open(claim, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError as exc:
        raise PrepError(f"sealed Gate-2 generation was already claimed: {claim}") from exc
    try:
        claim_record = canonical_json(
            {
                "schema": "t111-gate2-sealed-claim-v1",
                "input_binding": input_binding,
                "destination": str(output),
                "seed_sha256": "sha256:" + hashlib.sha256(
                    seed_record["seed_hex"].encode()
                ).hexdigest(),
            }
        )
        os.write(fd, (claim_record + "\n").encode())
        os.fsync(fd)
    finally:
        os.close(fd)
    fsync_directory(claim_root)

    staging = Path(tempfile.mkdtemp(prefix=f".{output.name}.staging-", dir=parent))
    published = False
    try:
        for name, rows in rows_by_name.items():
            write_jsonl(staging / name, rows)
        atomic_write(staging / "seed.json", canonical_json(seed_record) + "\n")
        atomic_write(staging / "attempt.json", canonical_json(attempt_claim) + "\n")
        burn_snapshot = staging / "burn-ledger.input.json"
        try:
            shutil.copyfile(DEFAULT_BURN_LEDGER, burn_snapshot)
            os.chmod(burn_snapshot, 0o600)
        except OSError as exc:
            raise PrepError(f"cannot snapshot input burn ledger: {exc}") from exc
        artifact_names = [
            *rows_by_name,
            "seed.json",
            "attempt.json",
            "burn-ledger.input.json",
        ]
        manifest["artifacts_sha256"] = {
            name: sha256_file(staging / name) for name in artifact_names
        }
        manifest["artifact_set"] = sorted(artifact_names)
        atomic_write(
            staging / "manifest.json", json.dumps(manifest, indent=2, sort_keys=True) + "\n"
        )
        for path in staging.iterdir():
            with path.open("rb") as handle:
                os.fsync(handle.fileno())
        fsync_directory(staging)
        if output.exists() or output.is_symlink():
            raise PrepError(f"sealed Gate-2 destination appeared during publication: {output}")
        os.rename(staging, output)
        published = True
        fsync_directory(parent)
    finally:
        if not published:
            shutil.rmtree(staging, ignore_errors=True)


def print_summary(
    manifest: dict[str, Any], dev: Sequence[dict[str, Any]], holdout: Sequence[dict[str, Any]]
) -> None:
    print(
        f"schema={manifest['schema']} randomization="
        f"{manifest['randomization']['mode']}"
    )
    for frame in FRAMES:
        detail = manifest["frames"][frame]
        print(f"{frame}: population={detail['population_size']}")
        for stratum in detail["strata"]:
            print(
                f"  {stratum['id']}: N={stratum['population_size']} "
                f"holdout={stratum['holdout_sample_size']} "
                f"pi={stratum['holdout_inclusion_probability']:.8f} "
                f"dev={stratum['dev_sample_size']}"
            )
    print(
        f"unique label sites: census={manifest['holdout']['unique_census_sites']} "
        f"dev={len(dev)} holdout={len(holdout)}"
    )


def input_commitment_value(manifest: dict[str, Any]) -> dict[str, Any]:
    """Return only the information an independent seed issuer may inspect."""

    return {
        "schema": "t111-gate2-input-commitment-v1",
        "committed_at": manifest["randomization"]["input_committed_at"],
        "input_binding": manifest["randomization"]["input_binding"],
        "attempt_claim": manifest["attempt_claim"],
        "systems": manifest["systems"],
        "sampling_config": manifest["sampling_config"],
        "decision_rule": manifest["decision_rule"],
        "burn_ledger": manifest["burn_ledger"],
        "protocol_coverage": manifest["protocol_coverage"],
        "frames": {
            frame: {
                "definition": manifest["frames"][frame]["definition"],
                "extractor_independent": manifest["frames"][frame][
                    "extractor_independent"
                ],
                "population_size": manifest["frames"][frame]["population_size"],
                "strata": manifest["frames"][frame]["strata"],
            }
            for frame in FRAMES
        },
        "power_analysis": manifest["power_analysis"],
        "provenance": manifest["provenance"],
    }


def input_commitment_document(manifest: dict[str, Any]) -> bytes:
    return (json.dumps(input_commitment_value(manifest), indent=2, sort_keys=True) + "\n").encode(
        "utf-8"
    )


def verify_input_commitment_publication(
    manifest: dict[str, Any],
    seed_record: dict[str, Any],
    fetcher: Any = github_json_fetcher,
) -> None:
    """Verify GitHub's initial revision before accepting public entropy."""

    try:
        verify_github_initial_gist_receipt(
            seed_record["commitment_github_receipt"],
            input_commitment_document(manifest),
            fetcher,
        )
    except (KeyError, LabelProtocolError) as exc:
        raise PrepError(f"input commitment publication cannot be verified: {exc}") from exc


def print_input_commitment(manifest: dict[str, Any]) -> None:
    """Emit the exact canonical external-publication document."""

    sys.stdout.buffer.write(input_commitment_document(manifest))


def materialize_context(args: argparse.Namespace) -> None:
    artifact_dir = args.artifact_dir
    manifest_path = artifact_dir / "manifest.json"
    if artifact_dir.is_symlink() or manifest_path.is_symlink():
        raise PrepError("artifact paths may not be symlinks")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load {manifest_path}: {exc}") from exc
    if not isinstance(manifest, dict) or manifest.get("schema") != SCHEMA:
        raise PrepError(f"{manifest_path}: unsupported Gate 2 artifact schema")

    artifact_paths = {
        "sites.census.jsonl": artifact_dir / "sites.census.jsonl",
        "sites.dev.jsonl": artifact_dir / "sites.dev.jsonl",
        "sites.holdout.jsonl": artifact_dir / "sites.holdout.jsonl",
        "key.jsonl": artifact_dir / "key.jsonl",
    }
    if manifest.get("gate_design_fixed") is True:
        artifact_paths["seed.json"] = artifact_dir / "seed.json"
        artifact_paths["attempt.json"] = artifact_dir / "attempt.json"
        artifact_paths["burn-ledger.input.json"] = artifact_dir / "burn-ledger.input.json"
    expected_hashes = manifest.get("artifacts_sha256")
    if not isinstance(expected_hashes, dict):
        raise PrepError(f"{manifest_path}: missing artifact digests")
    if manifest.get("gate_design_fixed") is True:
        if set(expected_hashes) != set(artifact_paths):
            raise PrepError("sealed artifact digest set is incomplete or contains extras")
        if manifest.get("artifact_set") != sorted(artifact_paths):
            raise PrepError("sealed artifact set is incomplete or contains extras")
        try:
            children = list(artifact_dir.iterdir())
        except OSError as exc:
            raise PrepError(f"cannot enumerate sealed artifact directory: {exc}") from exc
        if {path.name for path in children} != set(artifact_paths) | {"manifest.json"}:
            raise PrepError("sealed artifact directory has a missing or extra entry")
        if any(path.is_symlink() or not path.is_file() for path in children):
            raise PrepError("sealed artifact directory contains a non-regular entry")
    for name, path in artifact_paths.items():
        if path.is_symlink() or not path.is_file() or expected_hashes.get(name) != sha256_file(path):
            raise PrepError(f"artifact digest mismatch: {path}")
    if manifest.get("gate_design_fixed") is True:
        randomization = manifest.get("randomization")
        if not isinstance(randomization, dict):
            raise PrepError("sealed manifest has no randomization record")
        input_binding = randomization.get("input_binding")
        if not isinstance(input_binding, str) or not re.fullmatch(
            r"sha256:[0-9a-f]{64}", input_binding
        ):
            raise PrepError("sealed manifest has no valid input binding")
        seed_record = load_seed_record(
            artifact_paths["seed.json"],
            expected_input_binding=input_binding,
            expected_input_committed_at=randomization.get("input_committed_at"),
        )
        if randomization.get("seed_record") != seed_record:
            raise PrepError("manifest audit seed does not match seed.json")

    systems = manifest.get("systems")
    if (
        not isinstance(systems, list)
        or not systems
        or len(systems) != len(set(systems))
        or not all(isinstance(system, str) and system in SYSTEMS for system in systems)
    ):
        raise PrepError("manifest has an invalid system list")
    entries = load_corpus_entries(args.corpus_lock, systems)
    commits = {system: str(entries[system]["commit"]) for system in systems}
    provenance = manifest.get("provenance")
    if not isinstance(provenance, dict):
        raise PrepError("manifest has no provenance")
    if provenance.get("locked_commits") != {system: commits[system] for system in systems}:
        raise PrepError("manifest commits do not match corpus.lock")
    if manifest.get("gate_design_fixed") is True:
        try:
            bundled_attempt = json.loads(
                artifact_paths["attempt.json"].read_text(encoding="utf-8")
            )
        except (OSError, json.JSONDecodeError) as exc:
            raise PrepError(f"cannot load bundled attempt claim: {exc}") from exc
        attempt_claim = validate_attempt_claim(
            bundled_attempt,
            commits=commits,
            base_input_binding=str(randomization.get("base_input_binding")),
            input_binding=str(randomization.get("input_binding")),
        )
        if manifest.get("attempt_claim") != attempt_claim:
            raise PrepError("manifest attempt claim does not match attempt.json")
        if attempt_claim["committed_at"] != seed_record["input_committed_at"]:
            raise PrepError("attempt claim and seed timestamps differ")
    expected_inputs = {
        system: {
            "commit": commits[system],
            "excluded_gitlinks": entries[system]["excluded_gitlinks"],
            "go_build_tags": entries[system]["go_build_tags"],
        }
        for system in systems
    }
    if provenance.get("corpus_inputs") != expected_inputs:
        raise PrepError("manifest corpus input policy does not match corpus.lock")
    if provenance.get("corpus_lock_sha256") != sha256_file(args.corpus_lock):
        raise PrepError("manifest corpus.lock digest is stale")
    if provenance.get("prep_script_sha256") != sha256_file(Path(__file__)):
        raise PrepError("manifest was not produced by the current label_prep.py")
    if provenance.get("fact_verifier_sha256") != sha256_file(BASE / "gate34_common.py"):
        raise PrepError("manifest was not produced by the current evidence verifier")
    if provenance.get("typed_call_oracle_sha256") != typed_oracle_code_digest():
        raise PrepError("manifest was not produced by the current typed call oracle")
    recorded_facts = provenance.get("facts_sha256")
    expected_fact_names = {f"{system}.facts.jsonl" for system in systems}
    if not isinstance(recorded_facts, dict) or set(recorded_facts) != expected_fact_names:
        raise PrepError("manifest has an incomplete facts provenance set")
    for name in sorted(expected_fact_names):
        fact_path = args.facts_dir / name
        if not fact_path.is_file() or recorded_facts[name] != sha256_file(fact_path):
            raise PrepError(f"extractor fact digest mismatch: {fact_path}")
    burn_ledger_path = artifact_paths.get(
        "burn-ledger.input.json", DEFAULT_BURN_LEDGER
    )
    _, burn_binding, active_burns = scoped_burn_binding(
        burn_ledger_path, commits, args.corpus_dir
    )
    if manifest.get("burn_ledger") != burn_binding:
        raise PrepError("manifest burn scope does not match the current source populations")

    census = load_jsonl(artifact_paths["sites.census.jsonl"])
    dev = load_jsonl(artifact_paths["sites.dev.jsonl"])
    holdout = load_jsonl(artifact_paths["sites.holdout.jsonl"])
    keys = load_jsonl(artifact_paths["key.jsonl"])
    strata = {
        frame: manifest.get("frames", {}).get(frame, {}).get("strata", [])
        for frame in FRAMES
    }
    active_burned = [row for rows in active_burns.values() for row in rows]
    validate_rows(dev, holdout, keys, strata, active_burned, census)
    key_by_id = {str(row["site_id"]): row for row in keys}
    allowed_site_fields = {"site_id", "system", "path", "line", "column", "method"}
    tracked: dict[str, set[str]] = {}
    blobs: dict[tuple[str, str], bytes] = {}
    for system in systems:
        files, _ = pinned_tracked_files(
            args.corpus_dir / system,
            commits[system],
            entries[system]["excluded_gitlinks"],
        )
        tracked[system] = set(files)

    for row in [*census, *dev, *holdout]:
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
        blob_key = (system, rel)
        if blob_key not in blobs:
            blobs[blob_key] = git_blob(
                args.corpus_dir / system, commits[system], rel
            )
        blob = blobs[blob_key]
        offset = hidden.get("byte_offset")
        method = str(row["method"])
        if not isinstance(offset, int) or blob[offset : offset + len(method.encode())] != method.encode():
            raise PrepError(f"{sid}: byte offset does not resolve to method {method}")
        line, column = byte_coordinate(blob, offset)
        if (row["line"], row["column"]) != (line, column):
            raise PrepError(f"{sid}: line/column do not resolve to the pinned blob")
        if sid != site_id(system, commits[system], rel, offset, method):
            raise PrepError(f"{sid}: site ID does not bind its pinned coordinate")

    output = args.output_dir
    targets = [
        output / "sites.census.local.jsonl",
        output / "sites.dev.local.jsonl",
        output / "sites.holdout.local.jsonl",
    ]
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
    write_jsonl(targets[0], (enrich(row) for row in census))
    write_jsonl(targets[1], (enrich(row) for row in dev))
    write_jsonl(targets[2], (enrich(row) for row in holdout))
    for system in systems:
        pinned_tracked_files(
            args.corpus_dir / system,
            commits[system],
            entries[system]["excluded_gitlinks"],
        )
    print(f"local context written under ignored path: {output}")


def append_exposed_sample_to_burn_ledger(
    artifact_dir: Path,
    ledger_path: Path = DEFAULT_BURN_LEDGER,
) -> str:
    """Append a sealed sample before any reviewer-facing bytes are published."""

    manifest_path = artifact_dir / "manifest.json"
    if artifact_dir.is_symlink() or manifest_path.is_symlink():
        raise PrepError("artifact paths may not be symlinks")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load sealed manifest {manifest_path}: {exc}") from exc
    if (
        not isinstance(manifest, dict)
        or manifest.get("schema") != SCHEMA
        or manifest.get("gate_design_fixed") is not True
    ):
        raise PrepError("only a sealed Gate-2 artifact may be added to the burn ledger")
    randomization = manifest.get("randomization")
    provenance = manifest.get("provenance")
    if not isinstance(randomization, dict) or not isinstance(provenance, dict):
        raise PrepError("sealed artifact lacks randomization or provenance")
    input_binding = randomization.get("input_binding")
    commits = provenance.get("locked_commits")
    if not isinstance(input_binding, str) or not re.fullmatch(
        r"sha256:[0-9a-f]{64}", input_binding
    ):
        raise PrepError("sealed artifact has no canonical input binding")
    if not isinstance(commits, dict) or not all(
        isinstance(system, str)
        and isinstance(commit, str)
        and re.fullmatch(r"[0-9a-f]{40}", commit)
        for system, commit in commits.items()
    ):
        raise PrepError("sealed artifact has no complete source commit set")

    coordinates: list[dict[str, Any]] = []
    source_artifacts: list[dict[str, Any]] = []
    for name in (
        "sites.census.jsonl",
        "sites.dev.jsonl",
        "sites.holdout.jsonl",
    ):
        source = artifact_dir / name
        expected_digest = manifest.get("artifacts_sha256", {}).get(name)
        if (
            source.is_symlink()
            or not source.is_file()
            or expected_digest != sha256_file(source)
        ):
            raise PrepError(f"sealed coordinate artifact is missing or changed: {source}")
        rows = load_jsonl(source)
        for row in rows:
            if set(row) != {"site_id", "system", "path", "line", "column", "method"}:
                raise PrepError(f"{name}: reviewer coordinates are not source-only")
            coordinates.append(
                {
                    "system": row["system"],
                    "path": row["path"],
                    "start_line": row["line"],
                    "end_line": row["line"],
                }
            )
        source_artifacts.append(
            {
                "path": f"{artifact_dir.name}/{name}",
                "records": len(rows),
                "sha256": "sha256:" + sha256_file(source),
            }
        )

    try:
        cohorts, _ = load_burn_ledger_cohorts(ledger_path)
    except EvidenceValidationError as exc:
        raise PrepError(f"current burn ledger is not append-only v2: {exc}") from exc
    cohort_id = "gate2-" + input_binding.removeprefix("sha256:")
    new_cohort = {
        "cohort_id": cohort_id,
        "input_binding": input_binding,
        "source_commits": dict(sorted(commits.items())),
        "source_artifacts": source_artifacts,
        "coordinates": coordinates,
    }
    prior = next(
        (cohort for cohort in cohorts if cohort["cohort_id"] == cohort_id), None
    )
    try:
        candidate = build_burn_ledger([*cohorts, new_cohort] if prior is None else cohorts)
    except EvidenceValidationError as exc:
        raise PrepError(f"cannot append exposed Gate-2 cohort: {exc}") from exc
    if prior is not None:
        expected = build_burn_ledger([new_cohort])["cohorts"][0]
        if prior != expected:
            raise PrepError("burn cohort ID already exists with different coordinates")
        return str(candidate["ledger_digest"])

    before = sha256_file(ledger_path)
    if before != manifest.get("burn_ledger", {}).get("ledger_sha256"):
        # The live ledger may already contain later cohorts, but the input
        # snapshot must still be an exact prefix. Verify that separately.
        snapshot = artifact_dir / "burn-ledger.input.json"
        try:
            input_cohorts, _ = load_burn_ledger_cohorts(snapshot)
        except EvidenceValidationError as exc:
            raise PrepError(f"invalid bundled burn-ledger snapshot: {exc}") from exc
        current_by_id = {str(row["cohort_id"]): row for row in cohorts}
        if any(current_by_id.get(str(row["cohort_id"])) != row for row in input_cohorts):
            raise PrepError("live burn ledger is not an append-only superset of the input")
    document = json.dumps(candidate, indent=2, sort_keys=True) + "\n"
    if sha256_file(ledger_path) != before:
        raise PrepError("burn ledger changed concurrently; no reviewer kit was written")
    atomic_write(ledger_path, document)
    fsync_directory(ledger_path.parent)
    return str(candidate["ledger_digest"])


def export_labeler_kits(args: argparse.Namespace) -> None:
    """Publish two source-only reviewer kits after recording their burn cohort."""

    artifact_dir = args.artifact_dir
    context_dir = args.context_dir
    try:
        manifest = json.loads((artifact_dir / "manifest.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load sealed Gate-2 manifest: {exc}") from exc
    input_binding = manifest.get("randomization", {}).get("input_binding")
    if not isinstance(input_binding, str):
        raise PrepError("sealed Gate-2 manifest has no input binding")
    partitions: dict[str, list[dict[str, Any]]] = {}
    expected: dict[str, list[str]] = {}
    try:
        for split in ("census", "dev", "holdout"):
            partitions[split] = load_source_partitions(
                {split: context_dir / f"sites.{split}.local.jsonl"}
            )[split]
            expected[split] = [
                str(row["site_id"])
                for row in load_jsonl(artifact_dir / f"sites.{split}.jsonl")
            ]
        reviewers = tuple(
            item.strip() for item in args.reviewers.split(",") if item.strip()
        )
        bundle = build_reviewer_kits(
            partitions,
            expected,
            input_binding=input_binding,
            reviewers=reviewers,
        )
    except ReviewerKitError as exc:
        raise PrepError(f"cannot build source-only reviewer kits: {exc}") from exc

    # Burn before publication: a later filesystem failure cannot accidentally
    # leave exposed coordinates absent from future census carry-forward.
    ledger_digest = append_exposed_sample_to_burn_ledger(
        artifact_dir, args.burn_ledger
    )
    try:
        assignment_digest = freeze_reviewer_kits(args.output_dir, bundle)
    except (ReviewerKitError, OSError) as exc:
        raise PrepError(f"cannot publish reviewer kits: {exc}") from exc
    print(
        f"reviewer kits published: assignment={assignment_digest} "
        f"burn_ledger={ledger_digest}"
    )


def freeze_labels_command(args: argparse.Namespace) -> None:
    """Canonicalize adjudicated labels and create a hash-only commitment."""

    artifact_dir = args.artifact_dir
    try:
        manifest_bytes = (artifact_dir / "manifest.json").read_bytes()
        manifest = json.loads(manifest_bytes)
        assignment_bytes = args.assignment_manifest.read_bytes()
        assignment = json.loads(assignment_bytes)
        assignment_digest = args.assignment_digest.read_text(encoding="ascii").strip()
        adjudication = json.loads(args.adjudication.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PrepError(f"cannot load label-freeze input: {exc}") from exc
    input_binding = manifest.get("randomization", {}).get("input_binding")
    if assignment.get("schema") != "t111-two-reviewer-assignment-v1":
        raise PrepError("unsupported reviewer assignment manifest")
    if assignment.get("input_binding") != input_binding:
        raise PrepError("reviewer assignment belongs to different Gate-2 inputs")
    try:
        validate_assignment_manifest(assignment_bytes, assignment_digest)
    except AdjudicationError as exc:
        raise PrepError(f"reviewer assignment cannot be verified: {exc}") from exc
    known_ids = sorted(
        str(row["site_id"])
        for split in ("census", "dev", "holdout")
        for row in load_jsonl(artifact_dir / f"sites.{split}.jsonl")
    )
    labels = load_jsonl(args.labels)
    if any(
        row.get(field) == "unsure"
        for row in labels
        for field in ("invocation", "registration")
    ):
        raise PrepError("frozen Gate-2 labels cannot contain unsure decisions")
    committed_at = (
        dt.datetime.now(dt.timezone.utc)
        .replace(microsecond=0)
        .isoformat()
        .replace("+00:00", "Z")
    )
    try:
        commitment = build_label_commitment(
            labels,
            known_ids,
            committed_at,
            input_binding=str(input_binding),
            artifact_manifest_sha256=reviewer_sha256_bytes(manifest_bytes),
            reviewer_assignment_sha256=reviewer_sha256_bytes(assignment_bytes),
            adjudication=adjudication,
        )
    except LabelProtocolError as exc:
        raise PrepError(f"cannot freeze Gate-2 labels: {exc}") from exc
    output = args.output_dir
    if output.exists() or output.is_symlink():
        raise PrepError(f"label-freeze output already exists: {output}")
    if output.parent.is_symlink() or not output.parent.is_dir():
        raise PrepError("label-freeze output parent must already be a regular directory")
    created = False
    try:
        os.mkdir(output, 0o700)
        created = True
        freeze_canonical_labels(output / "labels.frozen.jsonl", labels, known_ids)
        freeze_bytes_exclusive(
            output / "labels.commitment.json",
            canonical_label_commitment_document(commitment),
        )
        fsync_directory(output)
        fsync_directory(output.parent)
    except (LabelProtocolError, OSError) as exc:
        if created:
            shutil.rmtree(output, ignore_errors=True)
        raise PrepError(f"cannot publish frozen labels: {exc}") from exc
    print(f"frozen labels written under {output}")


def adjudicate_labels_command(args: argparse.Namespace) -> None:
    """Merge exact reviewer returns and third-reviewer resolutions."""

    try:
        manifest_document = args.assignment_manifest.read_bytes()
        manifest_digest = args.assignment_digest.read_text(encoding="ascii").strip()
        manifest = json.loads(manifest_document)
        reviewers = manifest.get("reviewers")
        if not isinstance(reviewers, list) or len(reviewers) != 2:
            raise PrepError("assignment manifest does not name exactly two reviewers")
        reviewer_rows = {
            str(reviewers[0]): load_jsonl(args.reviewer_a_labels),
            str(reviewers[1]): load_jsonl(args.reviewer_b_labels),
        }
        result = adjudicate_labels(
            manifest_document,
            manifest_digest,
            reviewer_rows,
            load_jsonl(args.adjudicator_labels),
        )
    except (OSError, UnicodeError, json.JSONDecodeError, AdjudicationError) as exc:
        raise PrepError(f"cannot adjudicate reviewer labels: {exc}") from exc
    output = args.output_dir
    if output.exists() or output.is_symlink():
        raise PrepError(f"adjudication output already exists: {output}")
    if output.parent.is_symlink() or not output.parent.is_dir():
        raise PrepError("adjudication output parent must already be a regular directory")
    created = False
    try:
        os.mkdir(output, 0o700)
        created = True
        freeze_bytes_exclusive(output / "adjudicated-labels.jsonl", result.document)
        freeze_bytes_exclusive(
            output / "adjudication.json",
            (canonical_json(dict(result.counters)) + "\n").encode("utf-8"),
        )
        fsync_directory(output)
        fsync_directory(output.parent)
    except OSError as exc:
        if created:
            shutil.rmtree(output, ignore_errors=True)
        raise PrepError(f"cannot publish adjudicated labels: {exc}") from exc
    print(
        f"adjudicated labels written under {output}: "
        f"sha256={result.document_sha256}"
    )


def create_github_receipt_command(args: argparse.Namespace) -> None:
    """Create an immutable local receipt for one public initial Gist revision."""

    if args.document.is_symlink() or not args.document.is_file():
        raise PrepError("published commitment document must be a regular non-symlink file")
    try:
        document = args.document.read_bytes()
        receipt = build_github_initial_gist_receipt(
            args.api_url, document, github_json_fetcher
        )
        freeze_bytes_exclusive(
            args.output, (canonical_json(receipt) + "\n").encode("utf-8")
        )
    except (LabelProtocolError, OSError) as exc:
        raise PrepError(f"cannot create GitHub commitment receipt: {exc}") from exc
    print(f"verified GitHub receipt written to {args.output}")


def create_nist_seed_command(
    args: argparse.Namespace,
    *,
    github_fetcher: Any = github_json_fetcher,
    nist_fetcher: Any = nist_beacon_json_fetcher,
) -> None:
    """Freeze seed-v3 from the exact first eligible NIST Beacon pulse."""

    for path, description in (
        (args.commitment, "input commitment"),
        (args.github_receipt, "GitHub receipt"),
    ):
        if path.is_symlink() or not path.is_file():
            raise PrepError(f"{description} must be a regular non-symlink file")
    try:
        commitment_document = args.commitment.read_bytes()
        receipt_document = args.github_receipt.read_bytes()
        if len(receipt_document) > 1024 * 1024:
            raise PrepError("GitHub receipt exceeds 1 MiB")
        github_receipt = json.loads(receipt_document)
        record = build_nist_seed_record(
            commitment_document,
            github_receipt,
            github_fetcher=github_fetcher,
            nist_fetcher=nist_fetcher,
        )
        seed_document = (
            json.dumps(record, indent=2, sort_keys=True) + "\n"
        ).encode("utf-8")
        freeze_bytes_exclusive(args.output, seed_document)
    except (OSError, UnicodeError, json.JSONDecodeError, LabelProtocolError) as exc:
        raise PrepError(f"cannot create NIST audit seed: {exc}") from exc
    print(
        f"verified first eligible NIST seed written to {args.output}: "
        f"pulse={record['source_reference']}"
    )


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

    sid = site_id("s", "commit-a", "p.go", 10, "Call")
    assert sid == site_id("s", "commit-a", "p.go", 10, "Call")
    assert sid != site_id("s", "commit-b", "p.go", 10, "Call")
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


def add_sampling_arguments(command: argparse.ArgumentParser) -> None:
    command.add_argument("--corpus-dir", type=Path, default=DEFAULT_CORPUS)
    command.add_argument("--corpus-lock", type=Path, default=DEFAULT_LOCK)
    command.add_argument("--facts-dir", type=Path, default=DEFAULT_FACTS)
    command.add_argument("--systems", default=",".join(SYSTEMS))
    command.add_argument(
        "--recall-holdout-per-system", type=int, default=GATE_RECALL_HOLDOUT_PER_SYSTEM
    )
    command.add_argument(
        "--recall-dev-per-system", type=int, default=GATE_RECALL_DEV_PER_SYSTEM
    )
    command.add_argument(
        "--precision-holdout-per-system", type=int, default=GATE_PRECISION_HOLDOUT_PER_SYSTEM
    )
    command.add_argument(
        "--precision-dev-per-system", type=int, default=GATE_PRECISION_DEV_PER_SYSTEM
    )
    command.add_argument(
        "--recall-min-per-stratum", type=int, default=GATE_RECALL_MIN_PER_STRATUM
    )
    command.add_argument(
        "--precision-min-per-stratum", type=int, default=GATE_PRECISION_MIN_PER_STRATUM
    )
    command.add_argument(
        "--registration-recall-holdout-per-system",
        type=int,
        default=GATE_REGISTRATION_RECALL_HOLDOUT_PER_SYSTEM,
    )
    command.add_argument(
        "--registration-recall-dev-per-system",
        type=int,
        default=GATE_REGISTRATION_RECALL_DEV_PER_SYSTEM,
    )
    command.add_argument(
        "--registration-precision-holdout-per-system",
        type=int,
        default=GATE_REGISTRATION_PRECISION_HOLDOUT_PER_SYSTEM,
    )
    command.add_argument(
        "--registration-precision-dev-per-system",
        type=int,
        default=GATE_REGISTRATION_PRECISION_DEV_PER_SYSTEM,
    )
    command.add_argument(
        "--registration-recall-min-per-stratum",
        type=int,
        default=GATE_REGISTRATION_RECALL_MIN_PER_STRATUM,
    )
    command.add_argument(
        "--registration-precision-min-per-stratum",
        type=int,
        default=GATE_REGISTRATION_PRECISION_MIN_PER_STRATUM,
    )
    command.add_argument(
        "--role-holdout-per-role", type=int, default=GATE_ROLE_HOLDOUT_PER_ROLE
    )
    command.add_argument(
        "--role-dev-per-role", type=int, default=GATE_ROLE_DEV_PER_ROLE
    )


def parser() -> argparse.ArgumentParser:
    top = argparse.ArgumentParser(description=__doc__)
    commands = top.add_subparsers(dest="command", required=True)

    commitment = commands.add_parser(
        "commit-inputs",
        help="lock inputs and prove design power before an auditor supplies entropy",
    )
    add_sampling_arguments(commitment)
    commitment.set_defaults(
        commitment_only=True,
        force=False,
        seed=LEGACY_DIAGNOSTIC_SEED,
        audit_seed_file=None,
    )

    prepare = commands.add_parser(
        "prepare", help="enumerate frames and create coordinate artifacts"
    )
    add_sampling_arguments(prepare)
    prepare.add_argument("--output-dir", type=Path, default=DEFAULT_ARTIFACTS)
    prepare.add_argument("--seed", type=int, default=LEGACY_DIAGNOSTIC_SEED)
    prepare.add_argument(
        "--audit-seed-file",
        type=Path,
        help="input-bound sealed v3 seed receipt issued after commit-inputs",
    )
    prepare.set_defaults(commitment_only=False)
    prepare.add_argument("--dry-run", action="store_true")
    prepare.add_argument("--force", action="store_true")

    context = commands.add_parser(
        "materialize-context", help="create ignored local labeler context from coordinates"
    )
    context.add_argument("--artifact-dir", type=Path, default=DEFAULT_ARTIFACTS)
    context.add_argument("--corpus-dir", type=Path, default=DEFAULT_CORPUS)
    context.add_argument("--corpus-lock", type=Path, default=DEFAULT_LOCK)
    context.add_argument("--facts-dir", type=Path, default=DEFAULT_FACTS)
    context.add_argument("--output-dir", type=Path, default=DEFAULT_CONTEXT)
    context.add_argument("--context-lines", type=int, default=15)
    context.add_argument("--force", action="store_true")

    reviewer_export = commands.add_parser(
        "export-labeler-kits",
        help="publish two source-only reviewer kits and append their burn cohort",
    )
    reviewer_export.add_argument("--artifact-dir", type=Path, default=DEFAULT_ARTIFACTS)
    reviewer_export.add_argument("--context-dir", type=Path, default=DEFAULT_CONTEXT)
    reviewer_export.add_argument("--output-dir", type=Path, required=True)
    reviewer_export.add_argument(
        "--reviewers",
        required=True,
        help="two distinct reviewer identifiers separated by a comma",
    )
    reviewer_export.add_argument(
        "--burn-ledger", type=Path, default=DEFAULT_BURN_LEDGER
    )

    label_freeze = commands.add_parser(
        "freeze-labels",
        help="canonicalize adjudicated labels and emit a hash-only commitment",
    )
    label_freeze.add_argument("--artifact-dir", type=Path, default=DEFAULT_ARTIFACTS)
    label_freeze.add_argument("--labels", type=Path, required=True)
    label_freeze.add_argument("--assignment-manifest", type=Path, required=True)
    label_freeze.add_argument("--assignment-digest", type=Path, required=True)
    label_freeze.add_argument("--adjudication", type=Path, required=True)
    label_freeze.add_argument("--output-dir", type=Path, required=True)

    adjudicate = commands.add_parser(
        "adjudicate-labels",
        help="merge two exact reviewer returns with third-reviewer resolutions",
    )
    adjudicate.add_argument("--assignment-manifest", type=Path, required=True)
    adjudicate.add_argument("--assignment-digest", type=Path, required=True)
    adjudicate.add_argument("--reviewer-a-labels", type=Path, required=True)
    adjudicate.add_argument("--reviewer-b-labels", type=Path, required=True)
    adjudicate.add_argument("--adjudicator-labels", type=Path, required=True)
    adjudicate.add_argument("--output-dir", type=Path, required=True)

    github_receipt = commands.add_parser(
        "github-receipt",
        help="verify a public Gist's initial revision and freeze its receipt",
    )
    github_receipt.add_argument("--document", type=Path, required=True)
    github_receipt.add_argument("--api-url", required=True)
    github_receipt.add_argument("--output", type=Path, required=True)

    nist_seed = commands.add_parser(
        "nist-seed",
        help="freeze seed-v3 from the exact first eligible NIST Beacon pulse",
    )
    nist_seed.add_argument("--commitment", type=Path, required=True)
    nist_seed.add_argument("--github-receipt", type=Path, required=True)
    nist_seed.add_argument("--output", type=Path, required=True)

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
        if args.command == "export-labeler-kits":
            export_labeler_kits(args)
            return 0
        if args.command == "freeze-labels":
            freeze_labels_command(args)
            return 0
        if args.command == "adjudicate-labels":
            adjudicate_labels_command(args)
            return 0
        if args.command == "github-receipt":
            create_github_receipt_command(args)
            return 0
        if args.command == "nist-seed":
            create_nist_seed_command(args)
            return 0
        manifest, dev, holdout, keys = build_artifacts(args)
        if args.command == "commit-inputs":
            print_input_commitment(manifest)
            if not manifest["power_analysis"].get("attainable"):
                print(
                    "label_prep: ERROR: committed Gate-2 design is statistically incapable; "
                    "do not issue a seed",
                    file=sys.stderr,
                )
                return 2
            return 0
        print_summary(manifest, dev, holdout)
        if args.dry_run:
            print("dry run: no artifacts written")
            return 0
        publish_artifacts(
            args.output_dir,
            manifest,
            dev,
            holdout,
            keys,
            args.force,
        )
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
