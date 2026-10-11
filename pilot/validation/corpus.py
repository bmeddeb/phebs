"""Shared corpus sealing for validation and operating rounds.

Freezes one selection of pinned repositories and their exact declared input
files as content-addressed records, and verifies a sealed lock byte-for-byte
against freshly observed repositories. Machinery only: sealing a corpus
establishes no quality, operating or release claim, and this module encodes
no protocol semantics — protocol-specific sealing rules stay in their own
rounds.

The canonical encoding and digest helpers are the frozen t111 definitions
reused byte-for-byte; this module never edits them.
"""

from __future__ import annotations

import json
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

_T111_DIR = Path(__file__).resolve().parents[2] / "spike" / "t111"
if str(_T111_DIR) not in sys.path:
    sys.path.insert(0, str(_T111_DIR))

import label_protocol as protocol  # noqa: E402  (path bound above; frozen sha256/canonical helper)

LOCK_SCHEMA = "harness-corpus-lock-v1"
BINDING_SCHEMA = "harness-run-binding-v1"
_COMMIT_RE = re.compile(r"\A[0-9a-f]{40}\Z")
_DIGEST_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")
_UTC_RE = re.compile(r"\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")

_REPO_FIELDS = {"name", "git_url", "commit", "license", "inputs"}
_LICENSE_FIELDS = {"spdx", "source"}
_INPUT_FIELDS = {"bytes", "sha256"}


class CorpusError(ValueError):
    """A corpus lock, observed repository record or run binding is inconsistent."""


def canonical_bytes(value: Any) -> bytes:
    """Canonical JSON encoding: compact, sorted keys, one trailing newline."""
    return (
        json.dumps(value, ensure_ascii=False, allow_nan=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
        + b"\n"
    )


def digest_of(value: Any) -> str:
    """The protocol-tagged sha256 of the canonical encoding."""
    return protocol.sha256_bytes(canonical_bytes(value))


def sha256_file(path: Path) -> str:
    return protocol.sha256_bytes(Path(path).read_bytes())


def _text(value: Any, context: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise CorpusError(f"{context} must be a non-empty string")
    return value


def _exact_fields(value: Any, required: set[str], context: str) -> dict:
    if not isinstance(value, dict) or set(value) != required:
        raise CorpusError(f"{context}: missing or unknown required fields")
    return value


def _utc(value: Any, context: str) -> str:
    if not isinstance(value, str) or not _UTC_RE.match(value):
        raise CorpusError(f"{context} must be an RFC3339 UTC second")
    return value


def _commit(value: Any, context: str) -> str:
    if not isinstance(value, str) or not _COMMIT_RE.match(value):
        raise CorpusError(f"{context} must be 40 lowercase hex characters")
    return value


def _digest(value: Any, context: str) -> str:
    if not isinstance(value, str) or not _DIGEST_RE.match(value):
        raise CorpusError(f"{context} must be a tagged sha256 digest")
    return value


def _relative_path(value: Any, context: str) -> str:
    if not isinstance(value, str) or not value:
        raise CorpusError(f"{context} must be a non-empty relative path")
    path = Path(value)
    if path.is_absolute() or ".." in path.parts or path.as_posix() != value:
        raise CorpusError(f"{context} must be a clean relative path")
    return value


def _repository(value: Any) -> dict:
    repo = _exact_fields(value, _REPO_FIELDS, "corpus repository")
    name = _text(repo["name"], "repository.name")
    _text(repo["git_url"], "repository.git_url")
    _commit(repo["commit"], f"repository[{name}].commit")
    license_record = _exact_fields(repo["license"], _LICENSE_FIELDS, f"repository[{name}].license")
    _text(license_record["spdx"], f"repository[{name}].license.spdx")
    _text(license_record["source"], f"repository[{name}].license.source")
    inputs = repo["inputs"]
    if not isinstance(inputs, Mapping) or not inputs:
        raise CorpusError(f"repository[{name}].inputs must name at least one frozen input file")
    for rel, record in inputs.items():
        rel = _relative_path(rel, f"repository[{name}].inputs path")
        item = _exact_fields(record, _INPUT_FIELDS, f"repository[{name}].inputs[{rel}]")
        if type(item["bytes"]) is not int or item["bytes"] < 0:
            raise CorpusError(f"repository[{name}].inputs[{rel}].bytes must be a non-negative integer")
        _digest(item["sha256"], f"repository[{name}].inputs[{rel}].sha256")
    return repo


def build_lock(created_utc: str, selection_rule: str, repositories: Sequence[Mapping[str, Any]]) -> dict:
    """Validate and freeze one corpus selection; a lock removes nothing later."""
    _utc(created_utc, "created_utc")
    _text(selection_rule, "selection_rule")
    if isinstance(repositories, (str, bytes)) or not isinstance(repositories, Sequence) or not repositories:
        raise CorpusError("repositories must be a non-empty sequence")
    records = [_repository(repo) for repo in repositories]
    names = [record["name"] for record in records]
    if len(set(names)) != len(names):
        raise CorpusError("corpus repositories are not uniquely named")
    records.sort(key=lambda record: record["name"])
    return {
        "schema": LOCK_SCHEMA,
        "created_utc": created_utc,
        "selection_rule": selection_rule,
        "repositories": records,
    }


def validate_lock(lock: Any) -> dict:
    """Structurally validate a lock without observing any repository."""
    lock = _exact_fields(lock, {"schema", "created_utc", "selection_rule", "repositories"}, "corpus lock")
    if lock["schema"] != LOCK_SCHEMA:
        raise CorpusError(f"corpus lock is not {LOCK_SCHEMA}")
    _utc(lock["created_utc"], "lock.created_utc")
    _text(lock["selection_rule"], "lock.selection_rule")
    repositories = lock["repositories"]
    if not isinstance(repositories, list) or not repositories:
        raise CorpusError("lock.repositories must be a non-empty list")
    rebuilt = [_repository(repo) for repo in repositories]
    names = [record["name"] for record in rebuilt]
    if len(set(names)) != len(names) or names != sorted(names):
        raise CorpusError("lock repositories are not uniquely named and sorted")
    return lock


def verify_lock(lock: Mapping[str, Any], observed: Sequence[Mapping[str, Any]]) -> None:
    """Fail closed unless every observed repository matches the sealed lock.

    Observed records carry the same shape as lock repositories and are
    re-derived by the caller from the actual pinned clones; :func:`sha256_file`
    supports the file digests. A missing, extra, renamed or byte-different
    repository or input file refuses the whole verification.
    """
    lock = validate_lock(lock)
    sealed = {record["name"]: record for record in lock["repositories"]}
    if isinstance(observed, (str, bytes)) or not isinstance(observed, Sequence):
        raise CorpusError("observed repositories must be a sequence")
    fresh = {}
    for repo in observed:
        record = _repository(repo)
        if record["name"] in fresh:
            raise CorpusError(f"observed repository {record['name']!r} is duplicated")
        fresh[record["name"]] = record
    if set(fresh) != set(sealed):
        missing = sorted(set(sealed) - set(fresh))
        extra = sorted(set(fresh) - set(sealed))
        raise CorpusError(f"observed repositories differ from the lock: missing={missing} extra={extra}")
    for name in sorted(sealed):
        want, got = sealed[name], fresh[name]
        for field in ("git_url", "commit"):
            if want[field] != got[field]:
                raise CorpusError(f"repository[{name}].{field} changed after sealing")
        for field in ("spdx", "source"):
            if want["license"][field] != got["license"][field]:
                raise CorpusError(f"repository[{name}].license.{field} changed after sealing")
        if set(want["inputs"]) != set(got["inputs"]):
            missing = sorted(set(want["inputs"]) - set(got["inputs"]))
            extra = sorted(set(got["inputs"]) - set(want["inputs"]))
            raise CorpusError(f"repository[{name}] input files differ: missing={missing} extra={extra}")
        for rel in sorted(want["inputs"]):
            for field in ("bytes", "sha256"):
                if want["inputs"][rel][field] != got["inputs"][rel][field]:
                    raise CorpusError(f"repository[{name}].inputs[{rel}].{field} changed after sealing")


def run_binding(lock: Mapping[str, Any], source_commit: str) -> dict:
    """Bind one run to its exact source revision and sealed corpus lock."""
    lock = validate_lock(lock)
    _commit(source_commit, "run binding source_commit")
    return {
        "schema": BINDING_SCHEMA,
        "source_commit": source_commit,
        "corpus_lock_sha256": digest_of(lock),
        "repositories": [
            {"name": record["name"], "commit": record["commit"]} for record in lock["repositories"]
        ],
    }


def validate_binding(value: Any) -> dict:
    value = _exact_fields(value, {"schema", "source_commit", "corpus_lock_sha256", "repositories"}, "run binding")
    if value["schema"] != BINDING_SCHEMA:
        raise CorpusError(f"run binding is not {BINDING_SCHEMA}")
    _commit(value["source_commit"], "binding.source_commit")
    _digest(value["corpus_lock_sha256"], "binding.corpus_lock_sha256")
    repositories = value["repositories"]
    if not isinstance(repositories, list) or not repositories:
        raise CorpusError("binding.repositories must be a non-empty list")
    names = []
    for record in repositories:
        record = _exact_fields(record, {"name", "commit"}, "binding repository")
        names.append(_text(record["name"], "binding.repository.name"))
        _commit(record["commit"], f"binding.repository[{record['name']}].commit")
    if len(set(names)) != len(names) or names != sorted(names):
        raise CorpusError("binding repositories are not uniquely named and sorted")
    return value
