#!/usr/bin/env python3
"""GATE2-V2 Stage 2 — sealed frame-enumeration verifier.

This is deliberately narrower than ``label_prep.py prepare``.  It verifies
a *prebuilt* Stage-1 derived execution root, consumes two byte-identical,
offline fact runs, runs the bound typed-call oracle twice, and emits only the
four Stage-2 cardinality and membership inputs.  It never fetches, clones,
hydrates a module cache, invokes ``t111 extract``, selects samples, or invokes
``stage2_prepare.py``.

The derived root is a future, separately-authorized input: Stage-0's cache
only proves the original corpus-lock heads.  A caller must supply a signed
enumeration authorization binding the derived lock, sealed cache, harness,
and fact evidence before this program reads the derived source tree.
"""

from __future__ import annotations

# This check intentionally precedes every non-builtin import.  A sealed run
# must be launched as ``/usr/bin/python3 -I -S stage2_enumerate.py ...`` so
# neither the worktree/cwd nor user-site hooks can shadow the verifier before
# it validates the committed authorization.
import sys as _bootstrap_sys

if not (
    _bootstrap_sys.flags.isolated
    and _bootstrap_sys.flags.no_site
    and _bootstrap_sys.flags.ignore_environment
):
    _bootstrap_sys.stderr.write(
        "stage2-enumeration: refusing — isolated no-site interpreter required\n"
    )
    raise SystemExit(2)

import argparse
import ctypes
import errno
import hashlib
import json
import os
import re
import secrets
import signal
import stat
import subprocess
import sys
from pathlib import Path
from types import ModuleType
from typing import Any, Iterable


# The live verifier must not leave ``__pycache__`` files in either the current
# worktree or the derived root it is auditing.
sys.dont_write_bytecode = True


HERE = Path(__file__).resolve().parent
T111_ROOT = HERE.parents[1]
REPO_ROOT = T111_ROOT.parents[1]
STAGE0_DIR = HERE / "stage0"
STAGE0_INVENTORY = STAGE0_DIR / "code-path-inventory.tsv"
STAGE0_LOCK = T111_ROOT / "corpus.lock.json"
STAGE0_HARNESS_MANIFEST = T111_ROOT / ".module-cache" / "manifest.json"
STAGE1_RESPONSE_PATH = HERE / "stage1" / "response.json"
PROTOCOL_REL = "spike/t111/labeling/GATE2-V2.md"

AUTH_SCHEMA = "t111-gate2-v2-stage2-enumeration-authorization-v1"
PREBUILD_EVIDENCE_SCHEMA = "t111-gate2-v2-stage2-prebuild-evidence-v1"
P0_AUTHORIZATION_SCHEMA = "t111-gate2-v2-stage2-prebuild-authorization-v1"
P0_CONSUMPTION_MARKER_SCHEMA = "t111-gate2-v2-stage2-prebuild-consumption-v1"
P0_EVIDENCE_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-prebuild-evidence-receipt-v1"
P0_TERMINAL_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-prebuild-terminal-receipt-v2"
RECEIPT_SCHEMA = "t111-gate2-v2-stage1-receipt-v1"
FACT_RUN_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-fact-run-receipt-v1"
CONSUMPTION_MARKER_SCHEMA = "t111-gate2-v2-stage2-enumeration-consumption-v1"
TERMINAL_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-enumeration-terminal-receipt-v1"
RECEIPT_FIXTURES = ("temporal", "dapr", "loki", "online-boutique")
EXTERNAL_FRAMES = (
    "client_call_precision",
    "client_call_recall",
    "registration_precision",
    "registration_recall",
)
FAILURE_PREDICATES = {"LOAD_ERRORS", "EXTRACTION_FAILURE"}
AUTHORIZATION_PATH = HERE / "stage2-enumeration-authorization.json"
P0_AUTHORIZATION_PATH = HERE / "stage2-prebuild-authorization.json"
PREBUILD_EVIDENCE_PATH = HERE / "stage2-prebuild-evidence.json"
PLAN_PATH = REPO_ROOT / "PLAN.md"
AUTHORIZATION_REL = AUTHORIZATION_PATH.relative_to(REPO_ROOT).as_posix()
P0_AUTHORIZATION_REL = P0_AUTHORIZATION_PATH.relative_to(REPO_ROOT).as_posix()
PREBUILD_EVIDENCE_REL = PREBUILD_EVIDENCE_PATH.relative_to(REPO_ROOT).as_posix()
ENUMERATOR_REL = Path(__file__).resolve().relative_to(REPO_ROOT).as_posix()
PREBUILD_RUNNER_PATH = HERE / "stage2_prebuild.py"
PREBUILD_RUNNER_REL = PREBUILD_RUNNER_PATH.relative_to(REPO_ROOT).as_posix()
PREBUILD_EXECUTOR_PATH = HERE / "stage2_prebuild_execute.py"
PREBUILD_EXECUTOR_REL = PREBUILD_EXECUTOR_PATH.relative_to(REPO_ROOT).as_posix()
PREBUILD_EXECUTOR_REVIEW_PATH = HERE / "stage2-prebuild-execute-review-r3.md"
PREBUILD_EXECUTOR_REVIEW_REL = PREBUILD_EXECUTOR_REVIEW_PATH.relative_to(REPO_ROOT).as_posix()
STAGE1_SNAPSHOT_REL = (HERE / "stage1_snapshot.py").relative_to(REPO_ROOT).as_posix()
# This implementation revision must be accepted afresh because its P0
# implementation-review dependency changed.  Every executable path must name
# the same r6 review; older r2/r3/r4/r5 records remain historical and cannot
# bless this revised verifier.
ENUMERATION_REVIEW_PATH = HERE / "stage2-enumerate-review-r6.md"
ENUMERATION_REVIEW_REL = ENUMERATION_REVIEW_PATH.relative_to(REPO_ROOT).as_posix()
PREBUILD_EVIDENCE_REVIEW_PATH = HERE / "stage2-prebuild-evidence-review-r1.md"
PREBUILD_EVIDENCE_REVIEW_REL = PREBUILD_EVIDENCE_REVIEW_PATH.relative_to(REPO_ROOT).as_posix()
ESTIMATOR_AUTHORIZATION_PATH = REPO_ROOT / "spike" / "t111" / "labeling" / "estimator-authorization.json"
ESTIMATOR_AUTHORIZATION_REL = ESTIMATOR_AUTHORIZATION_PATH.relative_to(REPO_ROOT).as_posix()
PLAN_AUTHORIZATION_MARKER = "GATE2-V2 Stage-2 enumeration authorization"
PLAN_AUTHORIZATION_APPROVAL = "AUTHORIZATION: APPROVED"
PLAN_P0_AUTHORIZATION_MARKER = "GATE2-V2 Stage-2 P0 prebuild authorization"
PLAN_P0_AUTHORIZATION_APPROVAL = "AUTHORIZATION: APPROVED"
PLAN_P0_IMPLEMENTATION_MARKER = "GATE2-V2 Stage-2 P0 executor"
PLAN_P0_IMPLEMENTATION_APPROVAL = "ACCEPT"
# Version the exact marker as well as the review filename.  Older accepted
# rows are retained in PLAN.md as history, so an unversioned substring would
# make the current executable approval permanently ambiguous.
PLAN_IMPLEMENTATION_MARKER = "GATE2-V2 Stage-2 enumeration verifier, r6"
PLAN_IMPLEMENTATION_APPROVAL = "ACCEPT"
PYTHON_MODE = "isolated-no-site"
TOOLCHAIN_IDENTITY_RE = re.compile(
    r'^go_version="([^"]+)";go_digest=(sha256:[0-9a-f]{64});'
    r'git_version="([^"]+)";git_digest=(sha256:[0-9a-f]{64})$'
)
PYTHON_EXECUTABLE_RE = re.compile(r"^python3\.[0-9]+$")
GIT_CONFIG_SECTION_RE = re.compile(r"\s*\[\s*([A-Za-z][A-Za-z0-9-]*)\s*\]\s*")
GIT_CONFIG_ASSIGNMENT_RE = re.compile(
    r"\s*([A-Za-z][A-Za-z0-9-]*)\s*=\s*([A-Za-z0-9]+)\s*"
)
SAFE_GIT_CORE_VALUES = {
    "repositoryformatversion": {"0"},
    "filemode": {"true", "false"},
    "ignorecase": {"true", "false"},
    "precomposeunicode": {"true", "false"},
    "symlinks": {"true", "false"},
}


AUTHORIZATION_FIELDS = {
    "schema",
    "status",
    "authorization_id",
    "p0_authorization",
    "enumerator_sha256",
    "stage1_snapshot_sha256",
    "receipt_sha256",
    "response_sha256",
    "stage0_inventory_sha256",
    "base_lock_sha256",
    "derived_lock_sha256",
    "stage0_harness_manifest_sha256",
    "derived_harness_manifest_sha256",
    "cache_tree_sha256",
    "t111_binary_sha256",
    "typedcalloracle_binary_sha256",
    "python_executable",
    "python_version",
    "python_mode",
    "python_sha256",
    "git_executable",
    "git_sha256",
    "go_executable",
    "go_sha256",
    "producer_toolchain_identity",
    "review",
    "binding",
    "environment",
    "prior_authorizations",
    "prebuild_evidence",
    "heads",
    "fact_runs",
    "state",
}

FACT_RUN_BINDING_FIELDS = {"run_id", "receipt_sha256", "facts_sha256"}
AUTHORIZATION_REVIEW_FIELDS = {"status", "accepted_commit", "record_sha256"}
AUTHORIZATION_BINDING_FIELDS = {"status", "commit"}
AUTHORIZATION_ENVIRONMENT_FIELDS = {"network", "variables"}
AUTHORIZATION_PRIOR_AUTHORIZATION_FIELDS = {"path", "sha256"}
AUTHORIZATION_PREBUILD_EVIDENCE_FIELDS = {
    "path",
    "sha256",
    "accepted_commit",
    "review",
}
AUTHORIZATION_PREBUILD_EVIDENCE_REVIEW_FIELDS = {"path", "sha256"}
P0_AUTHORIZATION_BINDING_FIELDS = {"path", "sha256", "authorization_commit"}
AUTHORIZATION_STATE_FIELDS = {
    "ceremony_directory",
    "output_dir",
    "consumption_marker",
    "terminal_receipt",
}
PREBUILD_EVIDENCE_FIELDS = {
    "schema",
    "status",
    "prebuild_id",
    "p0_authorization",
    "receipt_sha256",
    "response_sha256",
    "stage0_inventory_sha256",
    "base_lock_sha256",
    "derived_lock_sha256",
    "stage0_harness_manifest_sha256",
    "derived_harness_manifest_sha256",
    "cache_tree_sha256",
    "t111_binary_sha256",
    "typedcalloracle_binary_sha256",
    "python_executable",
    "python_version",
    "python_mode",
    "python_sha256",
    "git_executable",
    "git_sha256",
    "go_executable",
    "go_sha256",
    "producer_toolchain_identity",
    "environment",
    "heads",
    "execution_root",
    "fact_runs",
    "p0_result",
}
PREBUILD_EVIDENCE_P0_AUTHORIZATION_FIELDS = P0_AUTHORIZATION_BINDING_FIELDS
PREBUILD_EVIDENCE_FACT_RUN_FIELDS = {"run_id", "root", "receipt_sha256", "facts_sha256"}
PREBUILD_EVIDENCE_P0_RESULT_FIELDS = {
    "consumption_marker_sha256",
    "terminal_receipt_sha256",
    "evidence_receipt_sha256",
}
# P0 is not a generic approval token.  The enumeration verifier does not
# import the P0 runner (that would make its fire-time trust closure depend on
# a second source module), but it still requires the full P0 envelope and the
# no-enumeration scope that the P0 parser accepts.  The P0 runner separately
# validates every nested operation and topology before it can do any work.
P0_AUTHORIZATION_FIELDS = {
    "schema",
    "status",
    "authorization_id",
    "implementation",
    "bootstrap",
    "inputs",
    "prior_authorizations",
    "toolchain",
    "environment",
    "derived_root",
    "fact_runs",
    "operations",
    "state",
    "record_projections",
    "scope",
    "implementation_review",
    "implementation_binding",
}
P0_STATE_FIELDS = {
    "ceremony_directory",
    "consumption_marker",
    "terminal_receipt",
    "evidence_receipt",
}
P0_IMPLEMENTATION_FIELDS = {
    "prebuild_runner_sha256",
    "prebuild_executor_sha256",
    "enumerator_sha256",
    "enumerator_review_sha256",
}
P0_IMPLEMENTATION_REVIEW_FIELDS = {"status", "accepted_commit", "record_sha256"}
P0_IMPLEMENTATION_BINDING_FIELDS = {"status", "commit"}
P0_PRIOR_AUTHORIZATION_FIELDS = {"estimator"}
P0_PRIOR_ESTIMATOR_FIELDS = {"path", "sha256"}
P0_RECORD_PROJECTIONS_FIELDS = {"evidence", "terminal"}
P0_EVIDENCE_PROJECTION_FIELDS = {
    "schema",
    "status",
    "prebuild_id",
    "authorization_schema",
    "authorization_id",
    "record_path",
    "execution_root",
    "fact_runs",
}
P0_TERMINAL_PROJECTION_FIELDS = {
    "schema",
    "prebuild_id",
    "authorization_schema",
    "authorization_id",
    "record_path",
    "execution_root",
    "fact_runs",
}
P0_RESULT_MARKER_FIELDS = {
    "schema",
    "status",
    "authorization_id",
    "authorization_sha256",
}
P0_RESULT_EVIDENCE_FIELDS = {
    "schema",
    "status",
    "authorization_id",
    "authorization_sha256",
    "consumption_marker_sha256",
    "implementation",
    "inputs",
    "toolchain",
    "environment",
    "derived",
    "hydration",
    "fact_runs",
}
P0_RESULT_DERIVED_FIELDS = {
    "execution_root",
    "derived_lock_sha256",
    "derived_harness_manifest_sha256",
    "cache_tree_sha256",
    "corpus_heads",
}
P0_RESULT_HYDRATION_FIELDS = {"exit_code", "stdout_sha256", "stderr_sha256"}
P0_RESULT_FACT_RUN_FIELDS = {
    "run_id",
    "root",
    "receipt_sha256",
    "facts_sha256",
    "capture",
}
P0_RESULT_FACT_CAPTURE_FIELDS = {"stdout_sha256", "stderr_sha256"}
P0_RESULT_TERMINAL_FIELDS = {
    "schema",
    "status",
    "authorization_sha256",
    "consumption_marker_sha256",
    "exit_code",
    "failure",
    "failure_diagnostic",
    "evidence_receipt_sha256",
}
P0_EXPECTED_SCOPE = {
    "construct_derived_root": True,
    "hydrate_modules": True,
    "extract_facts": True,
    "enumerate_frames": False,
    "prepare_stage2": False,
    "select_samples": False,
    "disclose_coordinates": False,
}
FACT_RUN_RECEIPT_FIELDS = {
    "schema",
    "status",
    "run_id",
    "execution_mode",
    "commands",
    "derived_lock_sha256",
    "cache_tree_sha256",
    "t111_binary_sha256",
    "typedcalloracle_binary_sha256",
    "git_sha256",
    "go_sha256",
    "producer_toolchain_identity",
    "heads",
    "facts_sha256",
}

# The verifier clears its inherited environment and installs exactly this
# envelope (with PATH derived from the authorization-bound Git/Go binaries).
# It is an authorization input rather than an implementation default so a
# signed record cannot silently run under a different no-network contract.
SEALED_ENVIRONMENT_STATIC = {
    "GIT_CONFIG_NOSYSTEM": "1",
    "GIT_CONFIG_GLOBAL": os.devnull,
    "GIT_TERMINAL_PROMPT": "0",
    "GIT_ASKPASS": "/usr/bin/false",
    "GIT_NO_REPLACE_OBJECTS": "1",
    "GIT_NO_LAZY_FETCH": "1",
    "GIT_OPTIONAL_LOCKS": "0",
    "GIT_CONFIG_COUNT": "3",
    "GIT_CONFIG_KEY_0": "core.hooksPath",
    "GIT_CONFIG_VALUE_0": "/dev/null",
    "GIT_CONFIG_KEY_1": "core.fsmonitor",
    "GIT_CONFIG_VALUE_1": "false",
    "GIT_CONFIG_KEY_2": "core.useBuiltinFSMonitor",
    "GIT_CONFIG_VALUE_2": "false",
    "GOENV": "off",
    "GOTOOLCHAIN": "local",
    "GOPROXY": "off",
    "GOSUMDB": "off",
    "GONOSUMDB": "*",
    "GOPRIVATE": "*",
    "GOTELEMETRY": "off",
    "GOWORK": "off",
    "LANG": "C",
    "LC_ALL": "C",
    "PYTHONDONTWRITEBYTECODE": "1",
    "PYTHONHASHSEED": "0",
    "TZ": "UTC",
}


class EnumerationError(Exception):
    """A checked, non-disclosing sealed-mode refusal."""


class MarkerTransitionError(EnumerationError):
    """Marker persistence failed after the one-shot may have been consumed."""

    def __init__(self, message: str, marker_sha256: str):
        super().__init__(message)
        self.marker_sha256 = marker_sha256


class EnumerationInterrupted(EnumerationError):
    """A catchable signal interrupted the one-shot after its guard was armed."""

    def __init__(self, signum: int):
        super().__init__("sealed enumeration was interrupted")
        self.signum = signum


class TerminalReceiptError(RuntimeError):
    """The consumed one-shot could not durably publish its terminal state."""


class PostConsumptionFailure(RuntimeError):
    """A BaseException after consumption normalized to the canonical exit 4."""


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def sha256_bytes(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as handle:
            for block in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(block)
    except OSError as exc:
        raise EnumerationError("required input cannot be hashed") from exc
    return "sha256:" + digest.hexdigest()


def _required_open_flag(name: str, label: str) -> int:
    value = getattr(os, name, None)
    if not isinstance(value, int) or value == 0:
        raise EnumerationError(f"{label} requires {name}")
    return value


def _no_follow_flag(label: str) -> int:
    return _required_open_flag("O_NOFOLLOW", label)


def _directory_flag(label: str) -> int:
    return _required_open_flag("O_DIRECTORY", label)


def _regular_file(path: Path, label: str) -> None:
    try:
        info = path.lstat()
    except OSError as exc:
        raise EnumerationError(f"{label} is unavailable") from exc
    if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode):
        raise EnumerationError(f"{label} is not a regular file")


def load_json_bytes(path: Path, label: str) -> tuple[Any, bytes]:
    _regular_file(path, label)
    try:
        raw = path.read_bytes()
        return json.loads(raw.decode("utf-8")), raw
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise EnumerationError(f"{label} is not valid JSON") from exc


def load_json(path: Path, label: str) -> Any:
    value, _raw = load_json_bytes(path, label)
    return value


def load_canonical_json_bytes(path: Path, label: str) -> tuple[Any, bytes]:
    """Load one canonical UTF-8 JSON value and retain its verified bytes."""
    _regular_file(path, label)
    try:
        raw = path.read_bytes()
        value = json.loads(raw.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise EnumerationError(f"{label} is not valid JSON") from exc
    if raw != (canonical_json(value) + "\n").encode("utf-8"):
        raise EnumerationError(f"{label} is not canonical JSON")
    return value, raw


def load_canonical_json(path: Path, label: str) -> Any:
    """Load one UTF-8 JSON value only if its bytes use the ceremony form."""
    value, _raw = load_canonical_json_bytes(path, label)
    return value


def require_sha256(value: Any, label: str) -> str:
    if not isinstance(value, str) or len(value) != 71 or not value.startswith("sha256:"):
        raise EnumerationError(f"{label} is not a sha256 digest")
    try:
        int(value[7:], 16)
    except ValueError as exc:
        raise EnumerationError(f"{label} is not a sha256 digest") from exc
    return value


def within(path: Path, root: Path, label: str = "derived input") -> Path:
    """Require a pre-existing, non-symlink path wholly inside ``root``.

    ``Path.resolve`` alone is not sufficient here: a symlink in an
    intermediate component could make a lexically in-root path load code or
    cache data from elsewhere.  Every component is therefore lstat'd before
    resolving it.
    """
    try:
        root_info = root.lstat()
        if not stat.S_ISDIR(root_info.st_mode) or stat.S_ISLNK(root_info.st_mode):
            raise EnumerationError(f"{label} root is not a real directory")
        if not path.is_absolute():
            raise EnumerationError(f"{label} must be absolute")
        root_resolved = root.resolve(strict=True)
        try:
            relative = path.relative_to(root)
            current = root
        except ValueError:
            # macOS exposes /var as an alias of /private/var.  A prior call
            # may therefore hand us a canonical child while this caller still
            # holds the lexical /var root.  Accept that benign alias while
            # retaining a component-by-component symlink check below.
            relative = path.relative_to(root_resolved)
            current = root_resolved
        if ".." in relative.parts:
            raise EnumerationError(f"{label} escapes the execution root")
        for part in relative.parts:
            current = current / part
            info = current.lstat()
            if stat.S_ISLNK(info.st_mode):
                raise EnumerationError(f"{label} contains a symlink")
        resolved = path.resolve(strict=True)
        resolved.relative_to(root_resolved)
    except (OSError, ValueError) as exc:
        raise EnumerationError(f"{label} is outside the execution root") from exc
    return resolved


def _parse_inventory(raw: bytes) -> dict[str, str]:
    try:
        lines = raw.decode("utf-8").splitlines()
    except UnicodeDecodeError as exc:
        raise EnumerationError("Stage-0 inventory cannot be read") from exc
    result: dict[str, str] = {}
    for line in lines:
        if not line or line.startswith("#"):
            continue
        fields = line.split("\t")
        if len(fields) != 2:
            raise EnumerationError("Stage-0 inventory has an invalid row")
        name, digest = fields
        if name in result:
            raise EnumerationError("Stage-0 inventory has duplicate paths")
        result[name] = require_sha256(digest, "Stage-0 inventory digest")
    required = {
        "spike/t111/label_prep.py",
        "spike/t111/gate34_common.py",
        "spike/t111/main.go",
        "spike/t111/module_cache.go",
        "spike/t111/corpus.lock.json",
        PROTOCOL_REL,
    }
    if not required <= set(result):
        raise EnumerationError("Stage-0 inventory lacks enumeration dependencies")
    return result


def read_inventory_sealed(path: Path) -> tuple[dict[str, str], str]:
    _regular_file(path, "Stage-0 inventory")
    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise EnumerationError("Stage-0 inventory cannot be read") from exc
    return _parse_inventory(raw), sha256_bytes(raw)


def read_inventory(path: Path) -> dict[str, str]:
    inventory, _digest = read_inventory_sealed(path)
    return inventory


def _valid_absolute_path_text(value: Any) -> bool:
    if not isinstance(value, str) or not value or "\n" in value or "\r" in value:
        return False
    path = Path(value)
    return path.is_absolute() and path.name not in {"", ".", ".."} and ".." not in path.parts


def _state_paths_overlap(first: Path, second: Path) -> bool:
    """Reject both identical paths and a state target nested below another."""
    try:
        first.relative_to(second)
        return True
    except ValueError:
        try:
            second.relative_to(first)
            return True
        except ValueError:
            return False


def state_paths(authorization: dict[str, Any]) -> tuple[Path, Path, Path]:
    """Return the authorization-bound output, marker, and terminal paths."""
    state = authorization.get("state")
    if not isinstance(state, dict):
        raise EnumerationError("enumeration authorization has invalid state paths")
    try:
        return (
            Path(state["output_dir"]),
            Path(state["consumption_marker"]),
            Path(state["terminal_receipt"]),
        )
    except (KeyError, TypeError) as exc:
        raise EnumerationError("enumeration authorization has invalid state paths") from exc


def ceremony_directory(authorization: dict[str, Any]) -> Path:
    state = authorization.get("state")
    if not isinstance(state, dict):
        raise EnumerationError("enumeration authorization has invalid state paths")
    try:
        return Path(state["ceremony_directory"])
    except (KeyError, TypeError) as exc:
        raise EnumerationError("enumeration authorization has invalid state paths") from exc


def _valid_full_commit(value: Any) -> bool:
    if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
        return False
    return True


def load_authorization() -> tuple[dict[str, Any], bytes]:
    value, raw = load_canonical_json_bytes(AUTHORIZATION_PATH, "enumeration authorization")
    if not isinstance(value, dict) or set(value) != AUTHORIZATION_FIELDS:
        raise EnumerationError("enumeration authorization has an invalid schema")
    if value["schema"] != AUTH_SCHEMA or value["status"] != "AUTHORIZED":
        raise EnumerationError("enumeration authorization is not signed")
    if (
        not isinstance(value["authorization_id"], str)
        or not value["authorization_id"]
        or "\n" in value["authorization_id"]
        or "\r" in value["authorization_id"]
    ):
        raise EnumerationError("enumeration authorization has no identifier")
    for key in AUTHORIZATION_FIELDS - {
        "schema", "status", "authorization_id", "heads", "fact_runs",
        "producer_toolchain_identity", "python_executable", "python_version",
        "python_mode", "git_executable", "go_executable", "review", "binding",
        "environment", "prior_authorizations", "p0_authorization",
        "prebuild_evidence", "state",
    }:
        require_sha256(value[key], f"authorization {key}")
    for field, basename in (("python_executable", "python3"), ("git_executable", "git"), ("go_executable", "go")):
        executable = value[field]
        if field == "python_executable":
            valid_name = (
                isinstance(executable, str)
                and PYTHON_EXECUTABLE_RE.fullmatch(Path(executable).name) is not None
            )
        else:
            valid_name = isinstance(executable, str) and Path(executable).name == basename
        if (
            not valid_name
            or not executable.startswith("/")
            or "\n" in executable
            or "\r" in executable
        ):
            raise EnumerationError(f"enumeration authorization has an invalid {field}")
    if not isinstance(value["python_version"], str) or not value["python_version"]:
        raise EnumerationError("enumeration authorization has an invalid python version")
    if value["python_mode"] != PYTHON_MODE:
        raise EnumerationError("enumeration authorization has an invalid python mode")
    if (
        not isinstance(value["producer_toolchain_identity"], str)
        or not value["producer_toolchain_identity"]
        or "\n" in value["producer_toolchain_identity"]
        or "\r" in value["producer_toolchain_identity"]
    ):
        raise EnumerationError("enumeration authorization has an invalid producer toolchain")
    review = value["review"]
    if (
        not isinstance(review, dict)
        or set(review) != AUTHORIZATION_REVIEW_FIELDS
        or review.get("status") != "accepted"
        or not _valid_full_commit(review.get("accepted_commit"))
    ):
        raise EnumerationError("enumeration authorization lacks an accepted review binding")
    require_sha256(review.get("record_sha256"), "authorization review record digest")
    binding = value["binding"]
    if (
        not isinstance(binding, dict)
        or set(binding) != AUTHORIZATION_BINDING_FIELDS
        or binding.get("status") != "executable"
        or not _valid_full_commit(binding.get("commit"))
        or binding["commit"] != review["accepted_commit"]
    ):
        raise EnumerationError("enumeration authorization lacks an executable binding")
    environment = value["environment"]
    if (
        not isinstance(environment, dict)
        or set(environment) != AUTHORIZATION_ENVIRONMENT_FIELDS
        or environment.get("network") != "disabled"
        or not isinstance(environment.get("variables"), dict)
    ):
        raise EnumerationError("enumeration authorization has an invalid environment binding")
    variables = environment["variables"]
    if set(variables) != {"PATH", *SEALED_ENVIRONMENT_STATIC} or not all(
        isinstance(item, str) and item and "\n" not in item and "\r" not in item
        for item in variables.values()
    ):
        raise EnumerationError("enumeration authorization has an invalid environment binding")
    if any(variables[key] != expected for key, expected in SEALED_ENVIRONMENT_STATIC.items()):
        raise EnumerationError("enumeration authorization has an invalid environment binding")
    if not variables["PATH"].startswith("/") or "\n" in variables["PATH"]:
        raise EnumerationError("enumeration authorization has an invalid environment binding")
    prior_authorizations = value["prior_authorizations"]
    if not isinstance(prior_authorizations, dict) or set(prior_authorizations) != {"estimator"}:
        raise EnumerationError("enumeration authorization lacks the sealed prior ceremony registry")
    prior = prior_authorizations["estimator"]
    if (
        not isinstance(prior, dict)
        or set(prior) != AUTHORIZATION_PRIOR_AUTHORIZATION_FIELDS
        or prior.get("path") != ESTIMATOR_AUTHORIZATION_REL
    ):
        raise EnumerationError("enumeration authorization has an invalid prior ceremony binding")
    require_sha256(prior.get("sha256"), "authorization prior ceremony digest")
    p0 = value["p0_authorization"]
    if (
        not isinstance(p0, dict)
        or set(p0) != P0_AUTHORIZATION_BINDING_FIELDS
        or p0.get("path") != P0_AUTHORIZATION_REL
        or not _valid_full_commit(p0.get("authorization_commit"))
    ):
        raise EnumerationError("enumeration authorization has an invalid P0 authorization binding")
    require_sha256(p0.get("sha256"), "authorization P0 authorization digest")
    prebuild = value["prebuild_evidence"]
    if (
        not isinstance(prebuild, dict)
        or set(prebuild) != AUTHORIZATION_PREBUILD_EVIDENCE_FIELDS
        or prebuild.get("path") != PREBUILD_EVIDENCE_REL
        or not _valid_full_commit(prebuild.get("accepted_commit"))
    ):
        raise EnumerationError("enumeration authorization has an invalid prebuild evidence binding")
    require_sha256(prebuild.get("sha256"), "authorization prebuild evidence digest")
    prebuild_review = prebuild.get("review")
    if (
        not isinstance(prebuild_review, dict)
        or set(prebuild_review) != AUTHORIZATION_PREBUILD_EVIDENCE_REVIEW_FIELDS
        or prebuild_review.get("path") != PREBUILD_EVIDENCE_REVIEW_REL
    ):
        raise EnumerationError("enumeration authorization has an invalid prebuild evidence review binding")
    require_sha256(prebuild_review.get("sha256"), "authorization prebuild evidence review digest")
    if not isinstance(value["heads"], dict) or set(value["heads"]) != set(RECEIPT_FIXTURES):
        raise EnumerationError("enumeration authorization has an invalid fixture set")
    for fixture, oid in value["heads"].items():
        if not isinstance(oid, str) or len(oid) != 40:
            raise EnumerationError(f"enumeration authorization head is invalid for {fixture}")
        try:
            int(oid, 16)
        except ValueError as exc:
            raise EnumerationError(f"enumeration authorization head is invalid for {fixture}") from exc
    fact_runs = value["fact_runs"]
    if not isinstance(fact_runs, dict) or set(fact_runs) != {"run1", "run2"}:
        raise EnumerationError("enumeration authorization has invalid fact-run bindings")
    for run, binding in fact_runs.items():
        if not isinstance(binding, dict) or set(binding) != FACT_RUN_BINDING_FIELDS:
            raise EnumerationError("enumeration authorization has invalid fact-run bindings")
        if not _valid_run_id(binding.get("run_id")):
            raise EnumerationError("enumeration authorization has invalid fact-run bindings")
        require_sha256(binding.get("receipt_sha256"), f"authorization {run} receipt digest")
        facts = binding.get("facts_sha256")
        if not isinstance(facts, dict) or set(facts) != set(RECEIPT_FIXTURES):
            raise EnumerationError("enumeration authorization has invalid fact bindings")
        for digest in facts.values():
            require_sha256(digest, "authorization fact digest")
    if fact_runs["run1"]["run_id"] == fact_runs["run2"]["run_id"]:
        raise EnumerationError("enumeration authorization reuses a fact-run id")
    state = value["state"]
    if not isinstance(state, dict) or set(state) != AUTHORIZATION_STATE_FIELDS:
        raise EnumerationError("enumeration authorization has invalid state paths")
    if not all(_valid_absolute_path_text(state[field]) for field in AUTHORIZATION_STATE_FIELDS):
        raise EnumerationError("enumeration authorization has invalid state paths")
    ceremony = ceremony_directory(value)
    output, marker, terminal = state_paths(value)
    if any(
        _state_paths_overlap(first, second)
        for first, second in ((output, marker), (output, terminal), (marker, terminal))
    ):
        raise EnumerationError("enumeration authorization state paths collide")
    for target in (output, marker, terminal):
        try:
            target.relative_to(ceremony)
        except ValueError as exc:
            raise EnumerationError("enumeration authorization state path escapes ceremony directory") from exc
        if target == ceremony:
            raise EnumerationError("enumeration authorization state path collides with ceremony directory")
    return value, raw


PREBUILD_EVIDENCE_DIGEST_FIELDS = {
    "receipt_sha256",
    "response_sha256",
    "stage0_inventory_sha256",
    "base_lock_sha256",
    "derived_lock_sha256",
    "stage0_harness_manifest_sha256",
    "derived_harness_manifest_sha256",
    "cache_tree_sha256",
    "t111_binary_sha256",
    "typedcalloracle_binary_sha256",
    "python_sha256",
    "git_sha256",
    "go_sha256",
}
PREBUILD_EVIDENCE_AUTHORIZATION_FIELDS = {
    "receipt_sha256",
    "response_sha256",
    "stage0_inventory_sha256",
    "base_lock_sha256",
    "derived_lock_sha256",
    "stage0_harness_manifest_sha256",
    "derived_harness_manifest_sha256",
    "cache_tree_sha256",
    "t111_binary_sha256",
    "typedcalloracle_binary_sha256",
    "python_executable",
    "python_version",
    "python_mode",
    "python_sha256",
    "git_executable",
    "git_sha256",
    "go_executable",
    "go_sha256",
    "producer_toolchain_identity",
    "environment",
}


def _valid_prebuild_identifier(value: Any) -> bool:
    return isinstance(value, str) and bool(value) and "\n" not in value and "\r" not in value


def _exact_boolean_scope(value: Any, expected: dict[str, bool]) -> bool:
    """Keep JSON numbers from widening a boolean-only authorization scope."""
    return (
        isinstance(value, dict)
        and set(value) == set(expected)
        and all(type(value[name]) is bool and value[name] is permitted for name, permitted in expected.items())
    )


def _validate_prebuild_fact_roots(value: dict[str, Any], execution_root: Path) -> None:
    if not isinstance(value, dict) or set(value) != {"run1", "run2"}:
        raise EnumerationError("prebuild evidence has invalid fact-run bindings")
    roots: list[Path] = []
    for run in ("run1", "run2"):
        binding = value[run]
        if not isinstance(binding, dict) or set(binding) != PREBUILD_EVIDENCE_FACT_RUN_FIELDS:
            raise EnumerationError("prebuild evidence has invalid fact-run bindings")
        if not _valid_run_id(binding.get("run_id")):
            raise EnumerationError("prebuild evidence has invalid fact-run bindings")
        root = binding.get("root")
        if not _valid_absolute_path_text(root):
            raise EnumerationError("prebuild evidence has an invalid fact-run root")
        path = Path(root)
        try:
            path.relative_to(execution_root)
        except ValueError as exc:
            raise EnumerationError("prebuild evidence fact-run root escapes the execution root") from exc
        if path == execution_root:
            raise EnumerationError("prebuild evidence fact-run root collides with the execution root")
        roots.append(path)
        require_sha256(binding.get("receipt_sha256"), "prebuild evidence fact-run receipt digest")
        facts = binding.get("facts_sha256")
        if not isinstance(facts, dict) or set(facts) != set(RECEIPT_FIXTURES):
            raise EnumerationError("prebuild evidence has invalid fact digests")
        for digest in facts.values():
            require_sha256(digest, "prebuild evidence fact digest")
    if roots[0] == roots[1] or value["run1"]["run_id"] == value["run2"]["run_id"]:
        raise EnumerationError("prebuild evidence reuses a fact-run root or id")


def load_prebuild_evidence() -> tuple[dict[str, Any], bytes]:
    """Load the future prebuild record without opening its derived inputs.

    This record is deliberately just a canonical committed declaration at
    this point.  It names the execution root and the two offline fact roots,
    but it never resolves, stats, or otherwise reads them before the one-shot
    consumption transition.
    """
    value, raw = load_canonical_json_bytes(PREBUILD_EVIDENCE_PATH, "prebuild evidence")
    if not isinstance(value, dict) or set(value) != PREBUILD_EVIDENCE_FIELDS:
        raise EnumerationError("prebuild evidence has an invalid schema")
    if value.get("schema") != PREBUILD_EVIDENCE_SCHEMA or value.get("status") != "COMPLETE":
        raise EnumerationError("prebuild evidence is not complete")
    if not _valid_prebuild_identifier(value.get("prebuild_id")):
        raise EnumerationError("prebuild evidence has no identifier")
    for field in PREBUILD_EVIDENCE_DIGEST_FIELDS:
        require_sha256(value.get(field), f"prebuild evidence {field}")
    p0 = value.get("p0_authorization")
    if (
        not isinstance(p0, dict)
        or set(p0) != PREBUILD_EVIDENCE_P0_AUTHORIZATION_FIELDS
        or p0.get("path") != P0_AUTHORIZATION_REL
        or not _valid_full_commit(p0.get("authorization_commit"))
    ):
        raise EnumerationError("prebuild evidence has an invalid P0 authorization binding")
    require_sha256(p0.get("sha256"), "prebuild evidence P0 authorization digest")
    if not isinstance(value.get("heads"), dict) or set(value["heads"]) != set(RECEIPT_FIXTURES):
        raise EnumerationError("prebuild evidence has an invalid fixture set")
    for fixture, oid in value["heads"].items():
        if not _valid_full_commit(oid):
            raise EnumerationError(f"prebuild evidence head is invalid for {fixture}")
    if not _valid_absolute_path_text(value.get("execution_root")):
        raise EnumerationError("prebuild evidence has an invalid execution root")
    _validate_prebuild_fact_roots(value.get("fact_runs"), Path(value["execution_root"]))
    p0_result = value.get("p0_result")
    if not isinstance(p0_result, dict) or set(p0_result) != PREBUILD_EVIDENCE_P0_RESULT_FIELDS:
        raise EnumerationError("prebuild evidence has an invalid P0 result binding")
    for field in PREBUILD_EVIDENCE_P0_RESULT_FIELDS:
        require_sha256(p0_result.get(field), f"prebuild evidence P0 result {field}")
    return value, raw


def verify_prebuild_evidence(
    authorization: dict[str, Any], evidence: dict[str, Any], evidence_bytes: bytes,
    args: argparse.Namespace,
) -> dict[str, Any]:
    """Bind a reviewed, committed prebuild to its sealed source/fact inputs.

    The P0 evidence is committed before its review.  The final authorization
    binds a later accepted commit which must retain the exact P0 evidence and
    add the independently reviewed record.  Keeping acceptance out of the
    evidence avoids a self-referential Git hash.  This all runs before the
    marker and before any stat/open of the declared derived root or fact
    directories.
    """
    binding = authorization["prebuild_evidence"]
    if sha256_bytes(evidence_bytes) != binding["sha256"]:
        raise EnumerationError("prebuild evidence digest does not match authorization")
    if evidence_bytes != _head_blob(PREBUILD_EVIDENCE_REL):
        raise EnumerationError("prebuild evidence is not the committed authorization version")
    review_binding = binding["review"]
    _regular_file(PREBUILD_EVIDENCE_REVIEW_PATH, "prebuild evidence review")
    try:
        review_bytes = PREBUILD_EVIDENCE_REVIEW_PATH.read_bytes()
    except OSError as exc:
        raise EnumerationError("prebuild evidence review cannot be read") from exc
    if sha256_bytes(review_bytes) != review_binding["sha256"]:
        raise EnumerationError("prebuild evidence review digest does not match authorization")
    if review_bytes != _head_blob(PREBUILD_EVIDENCE_REVIEW_REL):
        raise EnumerationError("prebuild evidence review is not the committed authorization version")
    accepted_commit = binding["accepted_commit"]
    _require_ancestor(accepted_commit, "prebuild evidence accepted commit")
    if evidence_bytes != _commit_blob(accepted_commit, PREBUILD_EVIDENCE_REL):
        raise EnumerationError("accepted prebuild commit does not contain the evidence bytes")
    if review_bytes != _commit_blob(accepted_commit, PREBUILD_EVIDENCE_REVIEW_REL):
        raise EnumerationError("accepted prebuild commit does not contain the review bytes")
    _review_binds_exact_artifacts(
        review_bytes,
        label="prebuild evidence review",
        artifacts=((PREBUILD_EVIDENCE_REL, binding["sha256"]),),
    )
    if evidence["p0_authorization"] != authorization["p0_authorization"]:
        raise EnumerationError("prebuild evidence differs on its P0 authorization binding")
    p0 = verify_p0_authorization_binding(authorization, evidence, accepted_commit)
    for field in PREBUILD_EVIDENCE_AUTHORIZATION_FIELDS:
        if evidence[field] != authorization[field]:
            raise EnumerationError("prebuild evidence differs from enumeration authorization")
    if evidence["heads"] != authorization["heads"]:
        raise EnumerationError("prebuild evidence does not bind the authorized Stage-1 heads")
    for run in ("run1", "run2"):
        if (
            evidence["fact_runs"][run]["run_id"]
            != authorization["fact_runs"][run]["run_id"]
            or
            evidence["fact_runs"][run]["receipt_sha256"]
            != authorization["fact_runs"][run]["receipt_sha256"]
            or evidence["fact_runs"][run]["facts_sha256"]
            != authorization["fact_runs"][run]["facts_sha256"]
        ):
            raise EnumerationError("prebuild evidence fact runs differ from enumeration authorization")
    verify_p0_result_chain(authorization, evidence, p0)
    for name, expected in (
        ("execution_root", evidence["execution_root"]),
        ("facts_run1", evidence["fact_runs"]["run1"]["root"]),
        ("facts_run2", evidence["fact_runs"]["run2"]["root"]),
    ):
        actual = getattr(args, name, None)
        if not isinstance(actual, str) or actual != expected:
            raise EnumerationError(f"{name.replace('_', '-')} differs from the prebuild evidence")
    # ``p0`` is a completed, independently verified predecessor at this point.
    # The final one-shot must reserve its state namespace before it creates its
    # own irreversible marker.
    return p0


def verify_authorized_toolchain_identity(authorization: dict[str, Any]) -> None:
    match = TOOLCHAIN_IDENTITY_RE.fullmatch(authorization["producer_toolchain_identity"])
    if match is None:
        raise EnumerationError("enumeration authorization has an invalid producer toolchain")
    _go_version, go_digest, _git_version, git_digest = match.groups()
    if go_digest != authorization["go_sha256"] or git_digest != authorization["git_sha256"]:
        raise EnumerationError("authorization toolchain fields are internally inconsistent")


def _control_git(*args: str) -> bytes:
    """Read a committed local control record without user Git configuration."""
    git = Path("/usr/bin/git")
    _regular_file(git, "control Git executable")
    environment = {
        "PATH": "/usr/bin:/bin",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_ASKPASS": "/usr/bin/false",
        "GIT_NO_REPLACE_OBJECTS": "1",
        "GIT_NO_LAZY_FETCH": "1",
        "GIT_OPTIONAL_LOCKS": "0",
    }
    command = [
        str(git),
        "-C", str(REPO_ROOT),
        "-c", "core.hooksPath=/dev/null",
        "-c", "core.fsmonitor=false",
        "-c", "core.useBuiltinFSMonitor=false",
        *args,
    ]
    try:
        result = subprocess.run(
            command,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=environment,
            timeout=15,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise EnumerationError("committed authorization cannot be read") from exc
    if result.returncode:
        raise EnumerationError("committed authorization cannot be read")
    return result.stdout


def _head_blob(relative: str) -> bytes:
    return _control_git("show", f"HEAD:{relative}")


def _commit_blob(commit: str, relative: str) -> bytes:
    if not _valid_full_commit(commit):
        raise EnumerationError("committed authorization has an invalid binding commit")
    return _control_git("show", f"{commit}:{relative}")


def _require_ancestor(commit: str, label: str) -> None:
    if not _valid_full_commit(commit):
        raise EnumerationError(f"{label} is not a full commit oid")
    # ``merge-base --is-ancestor`` returns 1 for a non-ancestor and 128 for
    # an invalid/missing object.  Both are an authorization refusal.
    try:
        _control_git("merge-base", "--is-ancestor", commit, "HEAD")
    except EnumerationError as exc:
        raise EnumerationError(f"{label} is not an ancestor of HEAD") from exc


def _require_ancestor_of(ancestor: str, descendant: str, label: str) -> None:
    """Require an already-bound record to predate a later bound commit."""
    if not _valid_full_commit(ancestor) or not _valid_full_commit(descendant):
        raise EnumerationError(f"{label} has an invalid commit binding")
    if ancestor == descendant:
        raise EnumerationError(f"{label} does not strictly predate its later binding")
    try:
        _control_git("merge-base", "--is-ancestor", ancestor, descendant)
    except EnumerationError as exc:
        raise EnumerationError(f"{label} does not predate its later binding") from exc


def _p0_plan_anchor(plan_bytes: bytes, authorization_id: str, authorization_sha256: str) -> None:
    """Require the P0 approval to be durable at its own prebuild commit."""
    _require_plan_acceptance(
        plan_bytes,
        marker=PLAN_P0_AUTHORIZATION_MARKER,
        approval=PLAN_P0_AUTHORIZATION_APPROVAL,
        bindings=(
            f"authorization_id={authorization_id}",
            f"authorization_sha256={authorization_sha256}",
        ),
        label="P0 authorization commit",
    )


def _p0_implementation_plan_anchor(
    plan_bytes: bytes,
    *,
    implementation_review_digest: str,
    parser_digest: str,
    executor_digest: str,
    enumerator_digest: str,
    enumerator_review_digest: str,
) -> None:
    """Require the P0 executor acceptance at the implementation commit.

    P0's authorization is necessarily committed later, so its parser can only
    establish the review's *syntax*.  E1 must instead verify the earlier
    committed implementation row which binds both the independent review and
    the executor source it accepted.
    """
    _require_plan_acceptance(
        plan_bytes,
        marker=PLAN_P0_IMPLEMENTATION_MARKER,
        approval=PLAN_P0_IMPLEMENTATION_APPROVAL,
        bindings=(
            f"{PREBUILD_RUNNER_REL}={parser_digest}",
            f"{PREBUILD_EXECUTOR_REL}={executor_digest}",
            f"{ENUMERATOR_REL}={enumerator_digest}",
            f"{ENUMERATION_REVIEW_REL}={enumerator_review_digest}",
            f"{PREBUILD_EXECUTOR_REVIEW_REL}={implementation_review_digest}",
        ),
        label="P0 implementation commit",
    )


def _verify_p0_implementation_binding(p0: dict[str, Any], p0_commit: str) -> None:
    """Bind P0's executor provenance before accepting any E1-derived state.

    The P0 parser validates its own authorization envelope but does not read
    Git history.  Requiring its complete transitive acceptance chain here
    prevents a syntactically plausible P0 from blessing M0/R0/T0 produced by
    an unreviewed parser or executor.  All reads are immutable local Git
    blobs; no derived root or prebuild output is opened on this path.
    """
    implementation = p0.get("implementation")
    review = p0.get("implementation_review")
    binding = p0.get("implementation_binding")
    if (
        not isinstance(implementation, dict)
        or set(implementation) != P0_IMPLEMENTATION_FIELDS
        or not isinstance(review, dict)
        or set(review) != P0_IMPLEMENTATION_REVIEW_FIELDS
        or review.get("status") != "accepted"
        or not isinstance(binding, dict)
        or set(binding) != P0_IMPLEMENTATION_BINDING_FIELDS
        or binding.get("status") != "executable"
        or not _valid_full_commit(review.get("accepted_commit"))
        or binding.get("commit") != review["accepted_commit"]
    ):
        raise EnumerationError("P0 authorization lacks an accepted implementation binding")
    digests = {
        field: require_sha256(implementation.get(field), f"P0 implementation {field}")
        for field in P0_IMPLEMENTATION_FIELDS
    }
    review_digest = require_sha256(
        review.get("record_sha256"), "P0 implementation review digest"
    )
    accepted_commit = review["accepted_commit"]
    _require_ancestor(accepted_commit, "P0 implementation review commit")
    _require_ancestor_of(
        accepted_commit, p0_commit, "P0 implementation review commit"
    )
    for relative, expected, label in (
        (PREBUILD_RUNNER_REL, digests["prebuild_runner_sha256"], "P0 admission parser"),
        (PREBUILD_EXECUTOR_REL, digests["prebuild_executor_sha256"], "P0 executor"),
        (ENUMERATOR_REL, digests["enumerator_sha256"], "enumeration verifier"),
        (ENUMERATION_REVIEW_REL, digests["enumerator_review_sha256"], "enumeration review"),
        (PREBUILD_EXECUTOR_REVIEW_REL, review_digest, "P0 implementation review"),
    ):
        if sha256_bytes(_commit_blob(accepted_commit, relative)) != expected:
            raise EnumerationError(f"P0 implementation commit does not contain the accepted {label}")
    _enumerator_review_record(
        _commit_blob(accepted_commit, ENUMERATION_REVIEW_REL),
        digests["enumerator_sha256"],
    )
    _p0_implementation_review_record(
        _commit_blob(accepted_commit, PREBUILD_EXECUTOR_REVIEW_REL),
        parser_digest=digests["prebuild_runner_sha256"],
        executor_digest=digests["prebuild_executor_sha256"],
        enumerator_digest=digests["enumerator_sha256"],
        enumerator_review_digest=digests["enumerator_review_sha256"],
    )
    _p0_implementation_plan_anchor(
        _commit_blob(accepted_commit, "PLAN.md"),
        implementation_review_digest=review_digest,
        parser_digest=digests["prebuild_runner_sha256"],
        executor_digest=digests["prebuild_executor_sha256"],
        enumerator_digest=digests["enumerator_sha256"],
        enumerator_review_digest=digests["enumerator_review_sha256"],
    )


def _verify_p0_admission_parser(p0: dict[str, Any], p0_bytes: bytes) -> None:
    """Re-run the exact P0 admission parser from digest-bound source bytes.

    The enumeration verifier deliberately does not duplicate P0's large
    clone/cache/operation grammar.  It does, however, execute the exact
    parser source whose digest is sealed in P0, using the already-loaded P0
    path, and requires that second canonical read to return the same bytes.
    This remains admission-only: the parser has no subprocess, network, or
    derived-root access path.
    """
    implementation = p0.get("implementation")
    if not isinstance(implementation, dict):
        raise EnumerationError("P0 authorization has no prebuild runner binding")
    runner_sha256 = require_sha256(
        implementation.get("prebuild_runner_sha256"), "P0 prebuild runner digest"
    )
    try:
        source = _read_verified_source(
            PREBUILD_RUNNER_PATH, runner_sha256, "P0 prebuild admission parser"
        )
        module = _execute_verified_source(
            "_t111_stage2_prebuild", PREBUILD_RUNNER_PATH, source,
            "P0 prebuild admission parser",
        )
        loader = getattr(module, "load_authorization", None)
        if not callable(loader):
            raise EnumerationError("P0 prebuild admission parser lacks its loader")
        parsed, parsed_bytes = loader(P0_AUTHORIZATION_PATH)
    except EnumerationError:
        raise
    except BaseException as exc:
        raise EnumerationError("P0 authorization does not pass its bound admission parser") from exc
    if parsed_bytes != p0_bytes or parsed != p0:
        raise EnumerationError("P0 admission parser did not retain the bound authorization bytes")


def _p0_execution_root(p0: dict[str, Any]) -> Path:
    derived = p0.get("derived_root")
    if not isinstance(derived, dict) or not _valid_absolute_path_text(derived.get("root")):
        raise EnumerationError("P0 authorization has an invalid derived-root projection")
    return Path(derived["root"])


def _p0_state_targets(p0: dict[str, Any]) -> dict[str, Path]:
    """Return P0's state paths without resolving or opening a derived root."""
    state = p0.get("state")
    if not isinstance(state, dict) or set(state) != P0_STATE_FIELDS:
        raise EnumerationError("P0 authorization has invalid result state paths")
    if not all(_valid_absolute_path_text(state.get(field)) for field in P0_STATE_FIELDS):
        raise EnumerationError("P0 authorization has invalid result state paths")
    ceremony = Path(state["ceremony_directory"])
    targets = {
        "consumption_marker": Path(state["consumption_marker"]),
        "terminal_receipt": Path(state["terminal_receipt"]),
        "evidence_receipt": Path(state["evidence_receipt"]),
    }
    values = list(targets.values())
    if any(
        _state_paths_overlap(first, second)
        for index, first in enumerate(values)
        for second in values[index + 1 :]
    ):
        raise EnumerationError("P0 authorization reuses result state paths")
    for path in values:
        if path.parent != ceremony:
            raise EnumerationError("P0 authorization result state is not a direct ceremony leaf")
    return {"ceremony_directory": ceremony, **targets}


def _canonical_p0_state_targets(p0: dict[str, Any]) -> dict[str, Path]:
    """Open-normalize P0's completed M0/R0/T0 namespace before its records.

    The P0 authorization is lexical.  A completed predecessor is safe to use
    as a reservation only after its ceremony and each direct record parent are
    walked with ``O_NOFOLLOW`` under an owner-private directory.  The actual
    record files are opened separately (also no-follow) before any bytes are
    read in :func:`verify_p0_result_chain`.
    """
    raw = _p0_state_targets(p0)
    ceremony = _canonical_private_directory(
        raw["ceremony_directory"], "P0 ceremony directory"
    )
    targets = {
        name: _canonical_state_target(raw[name], f"P0 {label}")
        for name, label in (
            ("consumption_marker", "consumption marker"),
            ("evidence_receipt", "evidence receipt"),
            ("terminal_receipt", "terminal receipt"),
        )
    }
    values = list(targets.values())
    if any(
        _state_paths_overlap(first, second)
        for index, first in enumerate(values)
        for second in values[index + 1 :]
    ):
        raise EnumerationError("P0 canonical result state paths collide")
    if any(path.parent != ceremony for path in values):
        raise EnumerationError("P0 canonical result state escapes its ceremony")
    return {"ceremony_directory": ceremony, **targets}


def _p0_fact_run_projection(p0: dict[str, Any], execution_root: Path) -> dict[str, dict[str, Any]]:
    """Validate the stable P0 run identities and paths used by R0/E1."""
    value = p0.get("fact_runs")
    if not isinstance(value, dict) or set(value) != {"run1", "run2"}:
        raise EnumerationError("P0 authorization has invalid fact-run projections")
    roots: set[Path] = set()
    run_ids: set[str] = set()
    receipts: set[Path] = set()
    result: dict[str, dict[str, Any]] = {}
    for run in ("run1", "run2"):
        planned = value[run]
        if (
            not isinstance(planned, dict)
            or not {"run_id", "path", "receipt_path"} <= set(planned)
            or not _valid_run_id(planned.get("run_id"))
            or not _valid_absolute_path_text(planned.get("path"))
            or not _valid_absolute_path_text(planned.get("receipt_path"))
        ):
            raise EnumerationError("P0 authorization has invalid fact-run projections")
        root = Path(planned["path"])
        receipt = Path(planned["receipt_path"])
        try:
            root.relative_to(execution_root)
        except ValueError as exc:
            raise EnumerationError("P0 authorization fact run escapes the derived root") from exc
        if root == execution_root or receipt != root / "fact-run-receipt.json":
            raise EnumerationError("P0 authorization has invalid fact-run projections")
        if root in roots or planned["run_id"] in run_ids or receipt in receipts:
            raise EnumerationError("P0 authorization reuses a fact-run projection")
        roots.add(root)
        run_ids.add(planned["run_id"])
        receipts.add(receipt)
        result[run] = {
            "run_id": planned["run_id"],
            "root": root,
            "receipt_path": receipt,
        }
    return result


def _p0_record_projections(
    p0: dict[str, Any],
    state_targets: dict[str, Path],
    execution_root: Path,
    fact_runs: dict[str, dict[str, Any]],
) -> dict[str, dict[str, Any]]:
    """Verify the P0-declared locations and static shape of M0/R0/T0."""
    value = p0.get("record_projections")
    if not isinstance(value, dict) or set(value) != P0_RECORD_PROJECTIONS_FIELDS:
        raise EnumerationError("P0 authorization has invalid result projections")
    expected_runs = {
        run: {
            "run_id": fact_runs[run]["run_id"],
            "root": str(fact_runs[run]["root"]),
            "receipt_path": str(fact_runs[run]["receipt_path"]),
        }
        for run in ("run1", "run2")
    }
    evidence = value.get("evidence")
    expected_evidence = {
        "schema": P0_EVIDENCE_RECEIPT_SCHEMA,
        "status": "COMPLETE",
        "prebuild_id": p0["authorization_id"],
        "authorization_schema": P0_AUTHORIZATION_SCHEMA,
        "authorization_id": p0["authorization_id"],
        "record_path": str(state_targets["evidence_receipt"]),
        "execution_root": str(execution_root),
        "fact_runs": expected_runs,
    }
    if (
        not isinstance(evidence, dict)
        or set(evidence) != P0_EVIDENCE_PROJECTION_FIELDS
        or evidence != expected_evidence
    ):
        raise EnumerationError("P0 authorization has an invalid evidence receipt projection")
    terminal = value.get("terminal")
    expected_terminal = {
        "schema": P0_TERMINAL_RECEIPT_SCHEMA,
        "prebuild_id": p0["authorization_id"],
        "authorization_schema": P0_AUTHORIZATION_SCHEMA,
        "authorization_id": p0["authorization_id"],
        "record_path": str(state_targets["terminal_receipt"]),
        "execution_root": str(execution_root),
        "fact_runs": expected_runs,
    }
    if (
        not isinstance(terminal, dict)
        or set(terminal) != P0_TERMINAL_PROJECTION_FIELDS
        or terminal != expected_terminal
    ):
        raise EnumerationError("P0 authorization has an invalid terminal receipt projection")
    return {"evidence": evidence, "terminal": terminal}


def _verify_p0_evidence_projection(p0: dict[str, Any], evidence: dict[str, Any]) -> None:
    """Require P0 to authorize the exact prebuild whose evidence is offered.

    A reviewed P0 document is necessary but insufficient on its own: its
    planned root, heads, static inputs, toolchain, and two fact roots must be
    the very values later asserted by E1.  Otherwise an authority for one
    derived tree could be reused to bless evidence from another.
    """
    inputs = p0.get("inputs")
    toolchain = p0.get("toolchain")
    derived = p0.get("derived_root")
    fact_runs = p0.get("fact_runs")
    bootstrap = p0.get("bootstrap")
    if not all(isinstance(value, dict) for value in (inputs, toolchain, derived, fact_runs, bootstrap)):
        raise EnumerationError("P0 authorization lacks a usable evidence projection")
    execution_root = _p0_execution_root(p0)
    planned_runs = _p0_fact_run_projection(p0, execution_root)
    _p0_record_projections(p0, _p0_state_targets(p0), execution_root, planned_runs)
    input_map = {
        "stage1_receipt_sha256": "receipt_sha256",
        "stage1_response_sha256": "response_sha256",
        "stage0_inventory_sha256": "stage0_inventory_sha256",
        "base_lock_sha256": "base_lock_sha256",
        "derived_lock_sha256": "derived_lock_sha256",
        "stage0_harness_manifest_sha256": "stage0_harness_manifest_sha256",
        "t111_binary_sha256": "t111_binary_sha256",
        "typedcalloracle_binary_sha256": "typedcalloracle_binary_sha256",
    }
    if any(inputs.get(p0_name) != evidence.get(evidence_name) for p0_name, evidence_name in input_map.items()):
        raise EnumerationError("P0 authorization does not bind the prebuild evidence inputs")
    toolchain_map = {
        "python_executable": "python_executable",
        "python_version": "python_version",
        "python_sha256": "python_sha256",
        "git_executable": "git_executable",
        "git_sha256": "git_sha256",
        "go_executable": "go_executable",
        "go_sha256": "go_sha256",
        "producer_toolchain_identity": "producer_toolchain_identity",
    }
    if any(toolchain.get(p0_name) != evidence.get(evidence_name) for p0_name, evidence_name in toolchain_map.items()):
        raise EnumerationError("P0 authorization does not bind the prebuild evidence toolchain")
    if inputs.get("heads") != evidence.get("heads"):
        raise EnumerationError("P0 authorization does not bind the prebuild evidence heads")
    if (
        bootstrap.get("source_t111_sha256") != evidence.get("t111_binary_sha256")
        or bootstrap.get("derived_t111_sha256") != evidence.get("t111_binary_sha256")
    ):
        raise EnumerationError("P0 bootstrap differs from the prebuild evidence producer")
    operations = p0.get("operations")
    lock_rewrite = operations.get("lock_rewrite") if isinstance(operations, dict) else None
    if (
        not isinstance(lock_rewrite, dict)
        or lock_rewrite.get("derived_lock_sha256") != evidence.get("derived_lock_sha256")
        or lock_rewrite.get("derived_lock_path") != derived.get("lock_path")
    ):
        raise EnumerationError("P0 lock rewrite differs from the prebuild evidence root")
    require_sha256(lock_rewrite.get("derived_lock_sha256"), "P0 lock rewrite digest")
    if derived.get("root") != evidence.get("execution_root"):
        raise EnumerationError("P0 authorization root differs from the prebuild evidence root")
    if evidence.get("prebuild_id") != p0.get("authorization_id"):
        raise EnumerationError("P0 authorization does not bind the prebuild identifier")
    evidence_runs = evidence.get("fact_runs")
    if not isinstance(evidence_runs, dict) or set(evidence_runs) != {"run1", "run2"}:
        raise EnumerationError("prebuild evidence has invalid fact-run bindings")
    for run in ("run1", "run2"):
        produced = evidence_runs[run]
        planned = planned_runs[run]
        if not isinstance(produced, dict):
            raise EnumerationError("P0 authorization lacks a usable fact-run projection")
        if (
            produced.get("run_id") != planned["run_id"]
            or produced.get("root") != str(planned["root"])
        ):
            raise EnumerationError("P0 authorization fact run differs from the prebuild evidence")


def verify_p0_authorization_binding(
    authorization: dict[str, Any], evidence: dict[str, Any], evidence_accepted_commit: str,
) -> dict[str, Any]:
    """Re-verify the actual reviewed P0 authority, not merely its digest.

    P0 is committed and PLAN-approved before it can create the derived root.
    Its evidence is committed next and independently reviewed later.  The
    final enumeration authorization ties all three historical records
    together, avoiding a digest-only claim that a P0 authority existed.
    """
    binding = authorization["p0_authorization"]
    if evidence["p0_authorization"] != binding:
        raise EnumerationError("prebuild evidence differs on its P0 authorization binding")
    p0_value, p0_bytes = load_canonical_json_bytes(P0_AUTHORIZATION_PATH, "P0 authorization")
    if (
        not isinstance(p0_value, dict)
        or set(p0_value) != P0_AUTHORIZATION_FIELDS
        or p0_value.get("schema") != P0_AUTHORIZATION_SCHEMA
        or p0_value.get("status") != "AUTHORIZED"
        or not isinstance(p0_value.get("authorization_id"), str)
        or not p0_value["authorization_id"]
        or "\n" in p0_value["authorization_id"]
        or "\r" in p0_value["authorization_id"]
        or not _exact_boolean_scope(p0_value.get("scope"), P0_EXPECTED_SCOPE)
    ):
        raise EnumerationError("P0 authorization has an invalid signed state")
    if sha256_bytes(p0_bytes) != binding["sha256"]:
        raise EnumerationError("P0 authorization digest does not match enumeration authorization")
    p0_prior = p0_value.get("prior_authorizations")
    if (
        not isinstance(p0_prior, dict)
        or set(p0_prior) != P0_PRIOR_AUTHORIZATION_FIELDS
        or not isinstance(p0_prior.get("estimator"), dict)
        or set(p0_prior["estimator"]) != P0_PRIOR_ESTIMATOR_FIELDS
        or p0_prior != authorization.get("prior_authorizations")
    ):
        raise EnumerationError("P0 authorization does not bind the sealed prior ceremony registry")
    if p0_bytes != _head_blob(P0_AUTHORIZATION_REL):
        raise EnumerationError("P0 authorization is not the committed authorization version")
    p0_commit = binding["authorization_commit"]
    _require_ancestor(p0_commit, "P0 authorization commit")
    _require_ancestor_of(p0_commit, evidence_accepted_commit, "P0 authorization commit")
    if p0_bytes != _commit_blob(p0_commit, P0_AUTHORIZATION_REL):
        raise EnumerationError("P0 authorization commit does not contain the bound P0 bytes")
    if p0_bytes != _commit_blob(evidence_accepted_commit, P0_AUTHORIZATION_REL):
        raise EnumerationError("accepted prebuild review does not retain the bound P0 bytes")
    _p0_plan_anchor(
        _commit_blob(p0_commit, "PLAN.md"),
        p0_value["authorization_id"],
        binding["sha256"],
    )
    _verify_p0_implementation_binding(p0_value, p0_commit)
    _verify_p0_admission_parser(p0_value, p0_bytes)
    _verify_p0_evidence_projection(p0_value, evidence)
    return p0_value


def _p0_result_marker(
    marker: Any, *, authorization_id: str, authorization_sha256: str,
) -> None:
    if (
        not isinstance(marker, dict)
        or set(marker) != P0_RESULT_MARKER_FIELDS
        or marker.get("schema") != P0_CONSUMPTION_MARKER_SCHEMA
        or marker.get("status") != "CONSUMED"
        or marker.get("authorization_id") != authorization_id
        or marker.get("authorization_sha256") != authorization_sha256
    ):
        raise EnumerationError("P0 consumption marker has an invalid binding")


def _p0_result_evidence(
    receipt: Any,
    *,
    p0: dict[str, Any],
    authorization_sha256: str,
    marker_sha256: str,
    evidence: dict[str, Any],
) -> None:
    """Verify R0 as the completed, P0-authorized source for E1's projections."""
    if (
        not isinstance(receipt, dict)
        or set(receipt) != P0_RESULT_EVIDENCE_FIELDS
        or receipt.get("schema") != P0_EVIDENCE_RECEIPT_SCHEMA
        or receipt.get("status") != "COMPLETE"
        or receipt.get("authorization_id") != p0["authorization_id"]
        or receipt.get("authorization_sha256") != authorization_sha256
        or receipt.get("consumption_marker_sha256") != marker_sha256
    ):
        raise EnumerationError("P0 evidence receipt has an invalid provenance chain")
    for field in ("implementation", "inputs", "toolchain", "environment"):
        if receipt.get(field) != p0.get(field):
            raise EnumerationError("P0 evidence receipt differs from its authorization projection")

    execution_root = _p0_execution_root(p0)
    derived = receipt.get("derived")
    inputs = receipt["inputs"]
    if not isinstance(inputs, dict) or not isinstance(receipt.get("toolchain"), dict):
        raise EnumerationError("P0 evidence receipt has invalid sealed projections")
    if (
        not isinstance(derived, dict)
        or set(derived) != P0_RESULT_DERIVED_FIELDS
        or derived.get("execution_root") != str(execution_root)
        or derived.get("corpus_heads") != inputs.get("heads")
        or derived.get("derived_lock_sha256") != inputs.get("derived_lock_sha256")
    ):
        raise EnumerationError("P0 evidence receipt has an invalid derived-root projection")
    for field in (
        "derived_lock_sha256",
        "derived_harness_manifest_sha256",
        "cache_tree_sha256",
    ):
        require_sha256(derived.get(field), f"P0 evidence receipt {field}")
    heads = derived["corpus_heads"]
    if not isinstance(heads, dict) or set(heads) != set(RECEIPT_FIXTURES):
        raise EnumerationError("P0 evidence receipt has an invalid corpus head set")
    for head in heads.values():
        if not _valid_full_commit(head):
            raise EnumerationError("P0 evidence receipt has an invalid corpus head")

    hydration = receipt.get("hydration")
    if not isinstance(hydration, dict) or set(hydration) != set(RECEIPT_FIXTURES):
        raise EnumerationError("P0 evidence receipt has an invalid hydration record")
    for fixture in RECEIPT_FIXTURES:
        capture = hydration[fixture]
        if (
            not isinstance(capture, dict)
            or set(capture) != P0_RESULT_HYDRATION_FIELDS
            or type(capture.get("exit_code")) is not int
            or capture["exit_code"] != 0
        ):
            raise EnumerationError("P0 evidence receipt does not prove hydration")
        require_sha256(capture.get("stdout_sha256"), "P0 hydration stdout digest")
        require_sha256(capture.get("stderr_sha256"), "P0 hydration stderr digest")

    planned_runs = _p0_fact_run_projection(p0, execution_root)
    fact_runs = receipt.get("fact_runs")
    offered_runs = evidence.get("fact_runs")
    if (
        not isinstance(fact_runs, dict)
        or set(fact_runs) != {"run1", "run2"}
        or not isinstance(offered_runs, dict)
        or set(offered_runs) != {"run1", "run2"}
    ):
        raise EnumerationError("P0 evidence receipt has invalid fact-run records")
    all_facts: dict[str, dict[str, str]] = {}
    for run in ("run1", "run2"):
        result = fact_runs[run]
        planned = planned_runs[run]
        if (
            not isinstance(result, dict)
            or set(result) != P0_RESULT_FACT_RUN_FIELDS
            or result.get("run_id") != planned["run_id"]
            or result.get("root") != str(planned["root"])
        ):
            raise EnumerationError("P0 evidence receipt has an invalid fact-run projection")
        require_sha256(result.get("receipt_sha256"), "P0 fact-run receipt digest")
        facts = result.get("facts_sha256")
        if not isinstance(facts, dict) or set(facts) != set(RECEIPT_FIXTURES):
            raise EnumerationError("P0 evidence receipt has invalid fact digests")
        for digest in facts.values():
            require_sha256(digest, "P0 fact digest")
        capture = result.get("capture")
        if not isinstance(capture, dict) or set(capture) != P0_RESULT_FACT_CAPTURE_FIELDS:
            raise EnumerationError("P0 evidence receipt has invalid extraction captures")
        require_sha256(capture.get("stdout_sha256"), "P0 extraction stdout digest")
        require_sha256(capture.get("stderr_sha256"), "P0 extraction stderr digest")
        all_facts[run] = facts
        offered = offered_runs[run]
        if (
            offered.get("run_id") != result["run_id"]
            or offered.get("root") != result["root"]
            or offered.get("receipt_sha256") != result["receipt_sha256"]
            or offered.get("facts_sha256") != facts
        ):
            raise EnumerationError("prebuild evidence differs from the P0 fact-run result")
    if all_facts["run1"] != all_facts["run2"]:
        raise EnumerationError("P0 evidence receipt has non-reproducible fact digests")

    input_map = {
        "stage1_receipt_sha256": "receipt_sha256",
        "stage1_response_sha256": "response_sha256",
        "stage0_inventory_sha256": "stage0_inventory_sha256",
        "base_lock_sha256": "base_lock_sha256",
        "derived_lock_sha256": "derived_lock_sha256",
        "stage0_harness_manifest_sha256": "stage0_harness_manifest_sha256",
        "t111_binary_sha256": "t111_binary_sha256",
        "typedcalloracle_binary_sha256": "typedcalloracle_binary_sha256",
    }
    if any(inputs.get(p0_name) != evidence.get(evidence_name) for p0_name, evidence_name in input_map.items()):
        raise EnumerationError("P0 evidence receipt differs from the E1 input projection")
    toolchain = receipt["toolchain"]
    toolchain_map = {
        "python_executable": "python_executable",
        "python_version": "python_version",
        "python_sha256": "python_sha256",
        "git_executable": "git_executable",
        "git_sha256": "git_sha256",
        "go_executable": "go_executable",
        "go_sha256": "go_sha256",
        "producer_toolchain_identity": "producer_toolchain_identity",
    }
    if any(toolchain.get(p0_name) != evidence.get(evidence_name) for p0_name, evidence_name in toolchain_map.items()):
        raise EnumerationError("P0 evidence receipt differs from the E1 toolchain projection")
    if (
        derived["corpus_heads"] != evidence.get("heads")
        or derived["execution_root"] != evidence.get("execution_root")
        or derived["derived_lock_sha256"] != evidence.get("derived_lock_sha256")
        or derived["derived_harness_manifest_sha256"] != evidence.get("derived_harness_manifest_sha256")
        or derived["cache_tree_sha256"] != evidence.get("cache_tree_sha256")
    ):
        raise EnumerationError("P0 evidence receipt differs from the E1 derived-root projection")


def _p0_result_terminal(
    terminal: Any, *, authorization_sha256: str, marker_sha256: str, evidence_sha256: str,
) -> None:
    if (
        not isinstance(terminal, dict)
        or set(terminal) != P0_RESULT_TERMINAL_FIELDS
        or terminal.get("schema") != P0_TERMINAL_RECEIPT_SCHEMA
        or terminal.get("status") != "COMPLETED"
        or type(terminal.get("exit_code")) is not int
        or terminal.get("exit_code") != 0
        or terminal.get("failure") is not None
        or terminal.get("failure_diagnostic") is not None
        or terminal.get("authorization_sha256") != authorization_sha256
        or terminal.get("consumption_marker_sha256") != marker_sha256
        or terminal.get("evidence_receipt_sha256") != evidence_sha256
    ):
        raise EnumerationError("P0 terminal receipt does not prove a completed prebuild")


def verify_p0_result_chain(
    authorization: dict[str, Any], evidence: dict[str, Any], p0: dict[str, Any],
) -> None:
    """Bind E1 to the actual completed P0 one-shot before enum consumption.

    Only P0's three state paths are opened here.  The derived root, cache,
    corpus, and fact files remain unopened until after the independent
    enumeration marker has been created.
    """
    authorization_sha256 = authorization["p0_authorization"]["sha256"]
    state_targets = _canonical_p0_state_targets(p0)
    execution_root = _p0_execution_root(p0)
    planned_runs = _p0_fact_run_projection(p0, execution_root)
    _p0_record_projections(p0, state_targets, execution_root, planned_runs)

    records = _open_p0_state_records(state_targets)
    marker, marker_bytes = records["consumption_marker"]
    marker_sha256 = sha256_bytes(marker_bytes)
    _p0_result_marker(
        marker,
        authorization_id=p0["authorization_id"],
        authorization_sha256=authorization_sha256,
    )
    result, result_bytes = records["evidence_receipt"]
    result_sha256 = sha256_bytes(result_bytes)
    _p0_result_evidence(
        result,
        p0=p0,
        authorization_sha256=authorization_sha256,
        marker_sha256=marker_sha256,
        evidence=evidence,
    )
    terminal, terminal_bytes = records["terminal_receipt"]
    terminal_sha256 = sha256_bytes(terminal_bytes)
    _p0_result_terminal(
        terminal,
        authorization_sha256=authorization_sha256,
        marker_sha256=marker_sha256,
        evidence_sha256=result_sha256,
    )
    if evidence["p0_result"] != {
        "consumption_marker_sha256": marker_sha256,
        "terminal_receipt_sha256": terminal_sha256,
        "evidence_receipt_sha256": result_sha256,
    }:
        raise EnumerationError("prebuild evidence does not bind the completed P0 result chain")


def _plan_row_cells(line: str) -> tuple[str, str, str, str] | None:
    """Parse the shared four-content-cell P0/enum PLAN record.

    The executor and final verifier deliberately share this literal format:
    ``| YYYY-MM-DD | marker | approval | semicolon-bound-digests |``.  It
    avoids treating narrative prose, quoted examples, or ``ACCEPTED`` as an
    executable approval.
    """
    parts = line.split("|")
    if len(parts) != 6 or parts[0] != "" or parts[-1] != "":
        return None
    cells: list[str] = []
    for part in parts[1:-1]:
        if (
            len(part) < 2
            or part[0] != " "
            or part[-1] != " "
            or part[1:-1].strip() != part[1:-1]
        ):
            return None
        cells.append(part[1:-1])
    if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}", cells[0]):
        return None
    return cells[0], cells[1], cells[2], cells[3]


def _require_plan_acceptance(
    plan_bytes: bytes,
    *,
    marker: str,
    approval: str,
    bindings: Iterable[str],
    label: str,
) -> None:
    """Require one exact shared-format PLAN row and ordered bindings."""
    try:
        text = plan_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise EnumerationError(f"{label} PLAN cannot be read") from exc
    if "\r" in text:
        raise EnumerationError(f"{label} PLAN is not LF-normalized")
    candidates = [line for line in text.split("\n") if marker in line]
    if len(candidates) != 1:
        raise EnumerationError(f"{label} lacks one structurally accepted PLAN row")
    cells = _plan_row_cells(candidates[0])
    if cells is None:
        raise EnumerationError(f"{label} PLAN row is not structurally exact")
    _date, row_marker, row_approval, row_bindings = cells
    if (
        row_marker != marker
        or row_approval != approval
        or row_bindings != ";".join(bindings)
    ):
        raise EnumerationError(f"{label} lacks its structurally accepted PLAN row")


def _accepted_review_anchor(
    plan_bytes: bytes, review_digest: str, enumerator_digest: str,
) -> None:
    try:
        _require_plan_acceptance(
            plan_bytes,
            marker=PLAN_IMPLEMENTATION_MARKER,
            approval=PLAN_IMPLEMENTATION_APPROVAL,
            bindings=(
                f"{ENUMERATION_REVIEW_REL}={review_digest}",
                f"{ENUMERATOR_REL}={enumerator_digest}",
            ),
            label="enumeration implementation binding commit",
        )
    except EnumerationError as exc:
        raise EnumerationError("binding commit lacks the accepted implementation review") from exc


def _accepted_review_record(review_bytes: bytes) -> str:
    try:
        text = review_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise EnumerationError("binding review record cannot be read") from exc
    if "\r" in text:
        raise EnumerationError("binding review record is not LF-normalized")
    verdicts = [line for line in text.split("\n") if line.startswith("**Verdict:")]
    if verdicts != ["**Verdict: ACCEPT.**"]:
        raise EnumerationError("binding review record is not accepted")
    return text


def _review_binds_exact_artifacts(
    review_bytes: bytes,
    *,
    label: str,
    artifacts: Iterable[tuple[str, str]],
) -> None:
    """Require an accepted review to name each exact artifact/digest pair."""
    text = _accepted_review_record(review_bytes)
    lines = text.split("\n")
    for relative, digest in artifacts:
        if lines.count(f"- `{relative}`: `{digest}`") != 1:
            raise EnumerationError(f"{label} does not bind the accepted artifact bytes")


def _enumerator_review_record(review_bytes: bytes, enumerator_digest: str) -> None:
    _review_binds_exact_artifacts(
        review_bytes,
        label="enumeration review",
        artifacts=((ENUMERATOR_REL, enumerator_digest),),
    )


def _p0_implementation_review_record(
    review_bytes: bytes,
    *,
    parser_digest: str,
    executor_digest: str,
    enumerator_digest: str,
    enumerator_review_digest: str,
) -> None:
    """Require the executor review to bind the complete P0 trust closure."""
    _review_binds_exact_artifacts(
        review_bytes,
        label="P0 implementation review",
        artifacts=(
            (PREBUILD_RUNNER_REL, parser_digest),
            (PREBUILD_EXECUTOR_REL, executor_digest),
            (ENUMERATOR_REL, enumerator_digest),
            (ENUMERATION_REVIEW_REL, enumerator_review_digest),
        ),
    )


def verify_reviewed_binding(authorization: dict[str, Any]) -> None:
    """Bind fire-time executable bytes to an independently accepted ancestor.

    The final authorization cannot itself name the commit that first contains
    it (that would be a Git-hash fixed point).  Instead its binding points at
    the reviewed implementation commit B.  B must contain the exact runner,
    snapshot loader, review record, and a PLAN acceptance anchor; the final
    HEAD independently commits the canonical authorization and its operator
    approval row.
    """
    review = authorization["review"]
    binding = authorization["binding"]
    binding_commit = binding["commit"]
    _require_ancestor(binding_commit, "authorization binding commit")
    _require_ancestor(review["accepted_commit"], "authorization review commit")
    for relative, expected, label in (
        (ENUMERATOR_REL, authorization["enumerator_sha256"], "enumerator"),
        (STAGE1_SNAPSHOT_REL, authorization["stage1_snapshot_sha256"], "Stage-1 snapshot runner"),
        (ENUMERATION_REVIEW_REL, review["record_sha256"], "enumeration review record"),
    ):
        if sha256_bytes(_commit_blob(binding_commit, relative)) != expected:
            raise EnumerationError(f"binding commit does not contain the accepted {label}")
    _enumerator_review_record(
        _commit_blob(binding_commit, ENUMERATION_REVIEW_REL),
        authorization["enumerator_sha256"],
    )
    _accepted_review_anchor(
        _commit_blob(binding_commit, "PLAN.md"),
        review["record_sha256"],
        authorization["enumerator_sha256"],
    )


def _require_clean_tracked_worktree() -> None:
    """Keep a live authorization from borrowing uncommitted implementation bytes."""
    git = Path("/usr/bin/git")
    _regular_file(git, "control Git executable")
    environment = {
        "PATH": "/usr/bin:/bin",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_ASKPASS": "/usr/bin/false",
        "GIT_NO_REPLACE_OBJECTS": "1",
        "GIT_NO_LAZY_FETCH": "1",
        "GIT_OPTIONAL_LOCKS": "0",
    }
    for arguments in (("diff", "--quiet"), ("diff", "--cached", "--quiet")):
        try:
            result = subprocess.run(
                [
                    str(git), "-C", str(REPO_ROOT),
                    "-c", "core.hooksPath=/dev/null",
                    "-c", "core.fsmonitor=false",
                    "-c", "core.useBuiltinFSMonitor=false",
                    *arguments,
                ],
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                env=environment,
                timeout=15,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise EnumerationError("tracked worktree cleanliness cannot be checked") from exc
        if result.returncode:
            raise EnumerationError("tracked worktree or index is not clean")


def verify_committed_authorization(
    authorization: dict[str, Any], authorization_bytes: bytes
) -> str:
    """Require the authorization, plan, and trusted runners to be HEAD blobs.

    A JSON file may describe itself as authorized, but it is not authority on
    its own.  The operator's durable approval is the exact PLAN row and fixed
    authorization artifact committed together at the repository HEAD.  This
    avoids accepting an ad-hoc file pair while avoiding a circular need to
    predict a future Git commit oid inside the authorization itself.
    """
    _control_git("rev-parse", "--verify", "HEAD^{commit}")
    _require_clean_tracked_worktree()
    checked = (
        (AUTHORIZATION_PATH, AUTHORIZATION_REL, "enumeration authorization"),
        (PLAN_PATH, "PLAN.md", "PLAN"),
        (Path(__file__).resolve(), ENUMERATOR_REL, "enumerator"),
        (HERE / "stage1_snapshot.py", STAGE1_SNAPSHOT_REL, "Stage-1 snapshot runner"),
        (ENUMERATION_REVIEW_PATH, ENUMERATION_REVIEW_REL, "enumeration review record"),
    )
    authorization_sha256: str | None = None
    plan_bytes: bytes | None = None
    review_bytes: bytes | None = None
    for current, relative, label in checked:
        _regular_file(current, label)
        if current == AUTHORIZATION_PATH:
            bytes_now = authorization_bytes
        else:
            try:
                bytes_now = current.read_bytes()
            except OSError as exc:
                raise EnumerationError(f"{label} cannot be read") from exc
        if bytes_now != _head_blob(relative):
            raise EnumerationError(f"{label} is not the committed authorization version")
        if current == AUTHORIZATION_PATH:
            authorization_sha256 = sha256_bytes(bytes_now)
        if current == PLAN_PATH:
            plan_bytes = bytes_now
        if current == ENUMERATION_REVIEW_PATH:
            review_bytes = bytes_now
    if authorization_sha256 is None:
        raise EnumerationError("enumeration authorization cannot be bound")
    if plan_bytes is None:
        raise EnumerationError("signed plan cannot be bound")
    if review_bytes is None:
        raise EnumerationError("enumeration review cannot be bound")
    try:
        _require_plan_acceptance(
            plan_bytes,
            marker=PLAN_AUTHORIZATION_MARKER,
            approval=PLAN_AUTHORIZATION_APPROVAL,
            bindings=(
                f"authorization_id={authorization['authorization_id']}",
                f"authorization_sha256={authorization_sha256}",
            ),
            label="signed plan",
        )
    except EnumerationError as exc:
        raise EnumerationError("signed plan does not contain the authorization identifier") from exc
    if sha256_bytes(review_bytes) != authorization["review"]["record_sha256"]:
        raise EnumerationError("enumeration review record does not match authorization")
    verify_reviewed_binding(authorization)
    return authorization_sha256


def _authorized_executable(
    authorization: dict[str, Any], path_field: str, digest_field: str, label: str
) -> Path:
    path = Path(authorization[path_field])
    _regular_file(path, label)
    try:
        if not os.access(path, os.X_OK):
            raise EnumerationError(f"{label} is not executable")
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be inspected") from exc
    try:
        resolved = path.resolve(strict=True)
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be resolved") from exc
    _regular_file(resolved, label)
    if sha256_file(resolved) != authorization[digest_field]:
        raise EnumerationError(f"{label} does not match authorization")
    return resolved


def verify_interpreter(authorization: dict[str, Any]) -> None:
    """Bind the verifier itself to the fire-time interpreter identity."""
    expected = _authorized_executable(
        authorization, "python_executable", "python_sha256", "authorized Python interpreter"
    )
    actual = Path(sys.executable)
    try:
        resolved_actual = actual.resolve(strict=True)
        _regular_file(resolved_actual, "running Python interpreter")
        if resolved_actual != expected.resolve(strict=True):
            raise EnumerationError("running Python interpreter differs from authorization")
    except OSError as exc:
        raise EnumerationError("running Python interpreter cannot be resolved") from exc
    version = ".".join(str(part) for part in sys.version_info[:3])
    if version != authorization["python_version"]:
        raise EnumerationError("running Python version differs from authorization")
    if sha256_file(actual) != authorization["python_sha256"]:
        raise EnumerationError("running Python interpreter differs from authorization")


def _unsafe_child_environment_key(key: str) -> bool:
    return (
        key.startswith("GIT_")
        or key.startswith("DYLD_")
        or key.startswith("LD_")
        or key in {
            "http_proxy", "https_proxy", "all_proxy",
            "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
            "SSH_ASKPASS", "SSH_AUTH_SOCK",
        }
    )


def _sanitize_environment_mapping(environment: dict[str, str]) -> None:
    for key in list(environment):
        if _unsafe_child_environment_key(key):
            environment.pop(key, None)


def _sanitize_child_environment() -> None:
    """Remove inherited variables that can redirect a verified child binary."""
    for key in list(os.environ):
        if _unsafe_child_environment_key(key):
            os.environ.pop(key, None)


def _sealed_environment(git: Path, go: Path) -> dict[str, str]:
    ordered_dirs: list[str] = []
    for directory in (str(git.parent), str(go.parent), "/usr/bin", "/bin"):
        if directory not in ordered_dirs:
            ordered_dirs.append(directory)
    return {"PATH": os.pathsep.join(ordered_dirs), **SEALED_ENVIRONMENT_STATIC}


def configure_sealed_execution_environment(authorization: dict[str, Any]) -> dict[str, Path]:
    """Make inherited PATH/Git configuration unable to affect source checks.

    ``label_prep`` invokes ``git`` before it resolves the typed-oracle
    toolchain.  Both Git and Go must therefore be verified first and be the
    *only* candidates placed on PATH.  The later resolver independently
    checks their version/digests against the producer identity embedded in
    every fact record and bound in this authorization.
    """
    git = _authorized_executable(
        authorization, "git_executable", "git_sha256", "authorized Git executable"
    )
    go = _authorized_executable(
        authorization, "go_executable", "go_sha256", "authorized Go executable"
    )
    expected = _sealed_environment(git, go)
    if authorization["environment"].get("variables") != expected:
        raise EnumerationError("running environment differs from authorization")
    # There is no inherited-variable allowlist.  Clearing the process
    # environment before any derived-root or fact input is read prevents a
    # loader, proxy, dynamic linker, Git, Go, or config redirection from
    # surviving simply because it was not known when this runner was written.
    os.environ.clear()
    os.environ.update(expected)
    return {"git": git.resolve(), "go": go.resolve()}


def install_oracle_environment_guard(lp: ModuleType) -> None:
    """Restore the verifier's no-network/no-user-config Git envelope.

    The sealed Stage-0 helper deliberately constructs a fresh, minimal
    environment for the typed oracle.  Its original envelope predates this
    runner's hardened Git controls, so install a narrow wrapper before any
    oracle invocation rather than modifying the Stage-0 bytes.
    """
    original = getattr(lp, "oracle_subprocess_env", None)
    if not callable(original):
        raise EnumerationError("sealed label_prep lacks the oracle environment builder")

    def guarded(toolchain: dict[str, str], tool_dir: Path, cache_dir: Path) -> dict[str, str]:
        try:
            environment = dict(original(toolchain, tool_dir, cache_dir))
        except Exception as exc:
            raise EnumerationError("typed oracle environment cannot be constructed") from exc
        _sanitize_environment_mapping(environment)
        environment.update({
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_TERMINAL_PROMPT": "0",
            "GIT_ASKPASS": "/usr/bin/false",
            "GIT_NO_REPLACE_OBJECTS": "1",
            "GIT_NO_LAZY_FETCH": "1",
            "GIT_OPTIONAL_LOCKS": "0",
            "GIT_CONFIG_COUNT": "3",
            "GIT_CONFIG_KEY_0": "core.hooksPath",
            "GIT_CONFIG_VALUE_0": "/dev/null",
            "GIT_CONFIG_KEY_1": "core.fsmonitor",
            "GIT_CONFIG_VALUE_1": "false",
            "GIT_CONFIG_KEY_2": "core.useBuiltinFSMonitor",
            "GIT_CONFIG_VALUE_2": "false",
        })
        return environment

    lp.oracle_subprocess_env = guarded


def _read_verified_source(path: Path, expected_digest: str, label: str) -> bytes:
    """Return exactly the source bytes just verified for an executed module.

    SourceFileLoader is intentionally not used: a valid hash-based ``.pyc``
    can otherwise run different bytes than the verified ``.py``.  Reading,
    hashing, compiling, and executing one byte string closes that boundary.
    """
    _regular_file(path, label)
    try:
        source = path.read_bytes()
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be read") from exc
    if sha256_bytes(source) != expected_digest:
        raise EnumerationError(f"{label} does not match its sealed digest")
    return source


def _execute_verified_source(name: str, path: Path, source: bytes, label: str) -> ModuleType:
    """Execute trusted source bytes without consulting import paths or pyc files."""
    module = ModuleType(name)
    module.__file__ = str(path)
    module.__package__ = ""
    previous = sys.modules.get(name)
    sys.modules[name] = module
    try:
        code = compile(source, str(path), "exec", dont_inherit=True)
        exec(code, module.__dict__)
    except Exception as exc:
        if previous is None:
            sys.modules.pop(name, None)
        else:
            sys.modules[name] = previous
        raise EnumerationError(f"{label} cannot be loaded") from exc
    return module


def verify_receipt(path: Path, authorization: dict[str, Any]) -> tuple[dict[str, Any], str]:
    """Verify the receipt with the independently accepted snapshot runner.

    Do not delegate this trust anchor to the mutable Stage-2 preparation
    script.  The authorization binds the exact Stage-1 runner bytes and its
    loader rechecks the sealed Stage-0 query/constants manifest without I/O.
    """
    receipt, receipt_bytes = load_json_bytes(path, "Stage-1 receipt")
    receipt_sha256 = sha256_bytes(receipt_bytes)
    if receipt_sha256 != authorization["receipt_sha256"]:
        raise EnumerationError("Stage-1 receipt digest does not match authorization")
    snapshot_path = HERE / "stage1_snapshot.py"
    try:
        source = _read_verified_source(
            snapshot_path,
            authorization["stage1_snapshot_sha256"],
            "Stage-1 snapshot runner",
        )
        module = _execute_verified_source(
            "_t111_stage1_snapshot", snapshot_path, source, "Stage-1 snapshot runner"
        )
        query_bytes, constants = module.load_sealed_inputs()
        if receipt.get("schema") != RECEIPT_SCHEMA:
            raise EnumerationError("Stage-1 receipt has an invalid schema")
        if receipt.get("status") != "ADMITTED":
            raise EnumerationError("Stage-1 receipt is not admitted")
        if receipt.get("cutoff") != constants["proposed_cutoff"]:
            raise EnumerationError("Stage-1 receipt cutoff does not match sealed inputs")
        if receipt.get("query_sha256") != module.sha256_bytes(query_bytes):
            raise EnumerationError("Stage-1 receipt query digest does not match sealed inputs")
        heads = receipt.get("heads")
        response_sha256 = receipt.get("response_sha256")
        if not isinstance(heads, dict):
            raise EnumerationError("Stage-1 receipt lacks sealed response data")
        require_sha256(response_sha256, "Stage-1 receipt response digest")
        if response_sha256 != authorization["response_sha256"]:
            raise EnumerationError("Stage-1 response digest does not match authorization")
        _regular_file(STAGE1_RESPONSE_PATH, "Stage-1 response")
        if sha256_file(STAGE1_RESPONSE_PATH) != response_sha256:
            raise EnumerationError("Stage-1 response digest does not match the receipt")
        if set(heads) != set(constants["fixtures"]):
            raise EnumerationError("Stage-1 receipt has an invalid fixture set")
        for fixture, head in heads.items():
            oid = head.get("head_oid") if isinstance(head, dict) else None
            if not isinstance(oid, str) or not module.OID_RE.fullmatch(oid):
                raise EnumerationError("Stage-1 receipt lacks a full sealed head")
    except Exception as exc:
        if isinstance(exc, EnumerationError):
            raise
        raise EnumerationError("Stage-1 receipt is not an admitted sealed receipt") from exc
    heads = receipt.get("heads")
    if not isinstance(heads, dict) or set(heads) != set(RECEIPT_FIXTURES):
        raise EnumerationError("Stage-1 receipt has an invalid fixture set")
    for fixture in RECEIPT_FIXTURES:
        if heads[fixture].get("head_oid") != authorization["heads"][fixture]:
            raise EnumerationError("enumeration authorization does not bind Stage-1 heads")
    return receipt, receipt_sha256


def _parse_lock(value: Any, label: str) -> list[dict[str, Any]]:
    if not isinstance(value, list) or not value:
        raise EnumerationError(f"{label} is not a nonempty corpus lock")
    names: set[str] = set()
    entries: list[dict[str, Any]] = []
    for item in value:
        if not isinstance(item, dict) or not isinstance(item.get("name"), str):
            raise EnumerationError(f"{label} has an invalid corpus entry")
        name = item["name"]
        if name in names:
            raise EnumerationError(f"{label} has duplicate fixture names")
        names.add(name)
        entries.append(item)
    return entries


def load_lock_sealed(path: Path, label: str) -> tuple[list[dict[str, Any]], str]:
    """Parse and hash exactly one lock byte string (no hash/parse reread)."""
    value, raw = load_json_bytes(path, label)
    return _parse_lock(value, label), sha256_bytes(raw)


def load_lock(path: Path, label: str) -> list[dict[str, Any]]:
    value, _digest = load_lock_sealed(path, label)
    return value


def verify_derived_lock(
    base_path: Path,
    derived_path: Path,
    receipt: dict[str, Any],
    authorization: dict[str, Any],
    inventory: dict[str, str],
) -> dict[str, dict[str, Any]]:
    base_rows, base_digest = load_lock_sealed(base_path, "base corpus lock")
    derived_rows, derived_digest = load_lock_sealed(derived_path, "derived corpus lock")
    if base_digest != inventory["spike/t111/corpus.lock.json"]:
        raise EnumerationError("base corpus lock no longer matches Stage-0 inventory")
    if base_digest != authorization["base_lock_sha256"]:
        raise EnumerationError("base corpus lock digest does not match authorization")
    if derived_digest != authorization["derived_lock_sha256"]:
        raise EnumerationError("derived corpus lock digest does not match authorization")
    base = {entry["name"]: entry for entry in base_rows}
    derived = {entry["name"]: entry for entry in derived_rows}
    if set(base) != set(derived):
        raise EnumerationError("derived corpus lock changes the fixture set")
    receipt_heads = receipt["heads"]
    for name, original in base.items():
        candidate = derived[name]
        expected = dict(original)
        if name in RECEIPT_FIXTURES:
            expected["commit"] = receipt_heads[name]["head_oid"]
        if candidate != expected:
            raise EnumerationError("derived corpus lock changes sealed policy")
    return {name: derived[name] for name in RECEIPT_FIXTURES}


def _tree_digest(path: Path) -> str:
    """Hash a sealed cache tree without following links or tolerating writes."""
    try:
        root_info = path.lstat()
    except OSError as exc:
        raise EnumerationError("derived module cache is unavailable") from exc
    if not stat.S_ISDIR(root_info.st_mode) or stat.S_ISLNK(root_info.st_mode):
        raise EnumerationError("derived module cache is not a real directory")
    if stat.S_IMODE(root_info.st_mode) & 0o222:
        raise EnumerationError("derived module cache is writable")
    digest = hashlib.sha256()
    try:
        entries = sorted(path.rglob("*"), key=lambda item: item.relative_to(path).as_posix())
    except OSError as exc:
        raise EnumerationError("derived module cache cannot be traversed") from exc
    for item in entries:
        try:
            info = item.lstat()
            rel = item.relative_to(path).as_posix()
        except (OSError, ValueError) as exc:
            raise EnumerationError("derived module cache has an unsafe entry") from exc
        if stat.S_ISLNK(info.st_mode) or not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
            raise EnumerationError("derived module cache has an unsafe entry")
        if stat.S_IMODE(info.st_mode) & 0o222:
            raise EnumerationError("derived module cache is writable")
        kind = b"d" if stat.S_ISDIR(info.st_mode) else b"f"
        digest.update(kind + b"\0" + rel.encode("utf-8") + b"\0")
        digest.update(f"{stat.S_IMODE(info.st_mode):04o}".encode("ascii") + b"\0")
        if stat.S_ISREG(info.st_mode):
            digest.update(sha256_file(item).encode("ascii") + b"\0")
    return "sha256:" + digest.hexdigest()


def _parse_manifest(value: Any, label: str) -> dict[str, Any]:
    expected = {
        "schema", "created_at", "source_head", "source_clean", "go_version",
        "go_sha256", "goos", "goarch", "sources", "artifacts",
    }
    if not isinstance(value, dict) or set(value) != expected:
        raise EnumerationError(f"{label} has an invalid schema")
    if value.get("schema") != "t111-bound-harness-v1" or value.get("source_clean") is not True:
        raise EnumerationError(f"{label} is not a clean bound harness")
    if not isinstance(value.get("sources"), dict) or not isinstance(value.get("artifacts"), dict):
        raise EnumerationError(f"{label} has invalid bindings")
    return value


def load_manifest_sealed(path: Path, label: str) -> tuple[dict[str, Any], str]:
    """Parse and hash exactly one harness-manifest byte string."""
    value, raw = load_json_bytes(path, label)
    return _parse_manifest(value, label), sha256_bytes(raw)


def _manifest(path: Path, label: str) -> dict[str, Any]:
    value, _digest = load_manifest_sealed(path, label)
    return value


def _validate_manifest_sources(root: Path, sources: dict[str, Any]) -> None:
    if not sources:
        raise EnumerationError("derived harness source binding is empty")
    for relative, expected in sources.items():
        relative_path = Path(relative) if isinstance(relative, str) else Path("/")
        if (
            not isinstance(relative, str)
            or not relative
            or relative_path.is_absolute()
            or ".." in relative_path.parts
        ):
            raise EnumerationError("derived harness source binding is unsafe")
        require_sha256(expected, "derived harness source digest")
        candidate = within(root / relative, root, "derived harness source")
        _regular_file(candidate, "derived harness source")
        if sha256_file(candidate) != expected:
            raise EnumerationError("derived harness source differs from its manifest")


def _bound_artifact(manifest: dict[str, Any], name: str, expected_digest: str, cache: Path) -> None:
    artifact = manifest["artifacts"].get(name)
    if not isinstance(artifact, dict) or set(artifact) != {"path", "sha256"}:
        raise EnumerationError("derived harness has an invalid artifact binding")
    if artifact.get("path") != f"bin/{name}":
        raise EnumerationError("derived harness artifact is mislocated")
    recorded = require_sha256(artifact.get("sha256"), "derived harness artifact digest")
    if recorded != expected_digest:
        raise EnumerationError("derived harness artifact does not match authorization")
    path = cache / artifact["path"]
    _regular_file(path, "derived harness artifact")
    try:
        writable = stat.S_IMODE(path.lstat().st_mode) & 0o222
    except OSError as exc:
        raise EnumerationError("derived harness artifact cannot be inspected") from exc
    if writable or sha256_file(path) != recorded:
        raise EnumerationError("derived harness artifact fails its binding")


def verify_bound_harness(root: Path, authorization: dict[str, Any]) -> None:
    base_manifest_path = STAGE0_HARNESS_MANIFEST
    module_dir = within(root / "spike" / "t111", root, "derived t111 root")
    derived_cache = within(module_dir / ".module-cache", root, "derived module cache")
    derived_manifest_path = within(derived_cache / "manifest.json", root, "derived harness manifest")
    base, base_manifest_digest = load_manifest_sealed(base_manifest_path, "Stage-0 harness manifest")
    derived, derived_manifest_digest = load_manifest_sealed(
        derived_manifest_path, "derived harness manifest"
    )
    if base_manifest_digest != authorization["stage0_harness_manifest_sha256"]:
        raise EnumerationError("Stage-0 harness manifest digest does not match authorization")
    if derived_manifest_digest != authorization["derived_harness_manifest_sha256"]:
        raise EnumerationError("derived harness manifest digest does not match authorization")
    if (
        derived["sources"] != base["sources"]
        or derived["go_version"] != base["go_version"]
        or derived["go_sha256"] != base["go_sha256"]
        or derived["goos"] != base["goos"]
        or derived["goarch"] != base["goarch"]
    ):
        raise EnumerationError("derived harness changes the sealed producer identity")
    if base["go_sha256"] != authorization["go_sha256"]:
        raise EnumerationError("authorization Go executable differs from the sealed harness")
    _validate_manifest_sources(root, derived["sources"])
    for name, authorization_field in (
        ("t111", "t111_binary_sha256"),
        ("typedcalloracle", "typedcalloracle_binary_sha256"),
    ):
        base_artifact = base["artifacts"].get(name)
        if not isinstance(base_artifact, dict):
            raise EnumerationError("Stage-0 harness has an invalid artifact binding")
        base_digest = require_sha256(
            base_artifact.get("sha256"), "Stage-0 harness artifact digest"
        )
        if authorization[authorization_field] != base_digest:
            raise EnumerationError("authorization changes the sealed producer binary")
    _bound_artifact(derived, "t111", authorization["t111_binary_sha256"], derived_cache)
    _bound_artifact(
        derived,
        "typedcalloracle",
        authorization["typedcalloracle_binary_sha256"],
        derived_cache,
    )
    if _tree_digest(derived_cache) != authorization["cache_tree_sha256"]:
        raise EnumerationError("derived module-cache digest does not match authorization")


def load_execution_label_prep(root: Path, inventory: dict[str, str]) -> ModuleType:
    """Load only the inventory-bound Stage-0 frame builder from the derived root."""
    module_dir = within(root / "spike" / "t111", root, "derived t111 root")
    closure = (
        ("gate34_common", "gate34_common.py", "derived fact verifier"),
        ("label_protocol", "label_protocol.py", "derived label protocol"),
        ("reviewer_kits", "reviewer_kits.py", "derived reviewer kits"),
        ("label_adjudication", "label_adjudication.py", "derived adjudication"),
        ("label_prep", "label_prep.py", "derived label_prep"),
    )
    verified: list[tuple[str, Path, bytes, str]] = []
    for name, filename, label in closure:
        inventory_key = f"spike/t111/{filename}"
        expected = inventory.get(inventory_key)
        if expected is None:
            raise EnumerationError("Stage-0 inventory lacks a label_prep import")
        source_path = within(module_dir / filename, root, "derived label_prep import")
        source = _read_verified_source(source_path, expected, label)
        verified.append((name, source_path, source, label))
    # Do not put the derived directory on sys.path.  That would let unrelated
    # files (for example ``subprocess.py``) shadow stdlib imports while the
    # closure is loaded.  Seed only the exact Stage-0 local closure, in its
    # dependency order, from verified source byte strings.
    for name, _path, _source, _label in verified:
        sys.modules.pop(name, None)
    loaded: dict[str, ModuleType] = {}
    for name, source_path, source, label in verified:
        loaded[name] = _execute_verified_source(name, source_path, source, label)
    return loaded["label_prep"]


def _raw_facts(path: Path) -> Iterable[dict[str, Any]]:
    _regular_file(path, "fact run")
    try:
        with path.open(encoding="utf-8") as handle:
            for line in handle:
                if not line.strip():
                    raise EnumerationError("fact run contains an empty record")
                value = json.loads(line)
                if not isinstance(value, dict):
                    raise EnumerationError("fact run contains an invalid record")
                yield value
    except (OSError, json.JSONDecodeError) as exc:
        raise EnumerationError("fact run is not valid JSONL") from exc


def _readonly_directory(path: Path, label: str) -> None:
    try:
        info = path.lstat()
    except OSError as exc:
        raise EnumerationError(f"{label} is unavailable") from exc
    if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
        raise EnumerationError(f"{label} is not a real directory")
    if stat.S_IMODE(info.st_mode) & 0o222:
        raise EnumerationError(f"{label} is writable")


def _readonly_tree(
    path: Path,
    label: str,
    *,
    reject_symlinks: bool,
    skip_root_children: set[str] | None = None,
) -> None:
    """Require an already-sealed tree without following link indirection."""
    _readonly_directory(path, label)
    try:
        for directory, children, files in os.walk(path, topdown=True, followlinks=False):
            current = Path(directory)
            for name in [*children, *files]:
                candidate = current / name
                if current == path and skip_root_children and name in skip_root_children:
                    if name in children:
                        children.remove(name)
                    continue
                info = candidate.lstat()
                if stat.S_ISLNK(info.st_mode):
                    if reject_symlinks:
                        raise EnumerationError(f"{label} contains a symlink")
                    if name in children:
                        children.remove(name)
                    continue
                if stat.S_ISDIR(info.st_mode):
                    if stat.S_IMODE(info.st_mode) & 0o222:
                        raise EnumerationError(f"{label} is writable")
                elif stat.S_ISREG(info.st_mode):
                    if stat.S_IMODE(info.st_mode) & 0o222:
                        raise EnumerationError(f"{label} is writable")
                else:
                    raise EnumerationError(f"{label} has an unsafe entry")
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be traversed") from exc


def _exists_no_follow(path: Path) -> bool:
    try:
        path.lstat()
    except FileNotFoundError:
        return False
    except OSError as exc:
        raise EnumerationError("Git metadata cannot be inspected") from exc
    return True


def verify_safe_local_git_config(config: Path) -> None:
    """Allow only inert, minimal repository-local Git configuration.

    Frame construction calls ``git status`` before the typed oracle.  Merely
    neutralizing global and system configuration is not enough: a derived
    repository's local ``core.worktree``, attributes path, or filter driver
    can redirect that call outside the sealed root or activate a command.
    Rather than attempting to maintain a denylist of Git's active directives,
    the prebuilt corpus must carry the narrow, declarative core configuration
    below.  The derived-root producer can write this small form without
    affecting any committed object or worktree byte.
    """
    _regular_file(config, "derived Git config")
    try:
        raw = config.read_bytes()
        text = raw.decode("ascii")
    except (OSError, UnicodeDecodeError) as exc:
        raise EnumerationError("derived Git config is not a safe local configuration") from exc
    section: str | None = None
    seen: set[str] = set()
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith(("#", ";")):
            continue
        # Continuations, quoted subsections, valueless booleans, and escaped
        # values all create parser surface that this sealed runner does not
        # need.  The authorizing prebuild must normalize them away.
        if line.rstrip().endswith("\\"):
            raise EnumerationError("derived Git config is not a safe local configuration")
        header = GIT_CONFIG_SECTION_RE.fullmatch(line)
        if header is not None:
            section = header.group(1).lower()
            if section != "core":
                raise EnumerationError("derived Git config has an active local directive")
            continue
        assignment = GIT_CONFIG_ASSIGNMENT_RE.fullmatch(line)
        if assignment is None or section != "core":
            raise EnumerationError("derived Git config is not a safe local configuration")
        key, value = assignment.groups()
        key = key.lower()
        value = value.lower()
        if key in seen or value not in SAFE_GIT_CORE_VALUES.get(key, set()):
            raise EnumerationError("derived Git config has an active local directive")
        seen.add(key)
    if "repositoryformatversion" not in seen:
        raise EnumerationError("derived Git config is missing repository format")


def verify_sealed_git_repository(root: Path, execution_root: Path) -> None:
    """Keep all Git metadata used by frame/oracle checks inside the root."""
    _readonly_tree(
        root,
        "derived corpus fixture",
        reject_symlinks=True,
        skip_root_children={".git"},
    )
    git_dir = within(root / ".git", execution_root, "derived Git metadata")
    _readonly_tree(git_dir, "derived Git metadata", reject_symlinks=True)
    for relative in ("commondir", "gitdir", "objects/info/alternates"):
        if _exists_no_follow(git_dir / relative):
            raise EnumerationError("derived Git metadata has external linkage")
    verify_safe_local_git_config(git_dir / "config")


def _readonly_regular_file(path: Path, label: str) -> None:
    _regular_file(path, label)
    try:
        if stat.S_IMODE(path.lstat().st_mode) & 0o222:
            raise EnumerationError(f"{label} is writable")
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be inspected") from exc


def _readonly_ancestry(root: Path, path: Path, label: str) -> None:
    """Require the root-to-input directory chain to be immutable at rest."""
    try:
        relative = path.relative_to(root)
    except ValueError as exc:
        raise EnumerationError(f"{label} is outside the execution root") from exc
    current = root
    _readonly_directory(current, "execution root")
    for part in relative.parts:
        current = current / part
        _readonly_directory(current, label)


def _valid_run_id(value: Any) -> bool:
    if not isinstance(value, str) or not (8 <= len(value) <= 128):
        return False
    return all(character.isascii() and (character.isalnum() or character in "._-") for character in value)


def _validate_fact_run_commands(value: Any) -> None:
    if not isinstance(value, dict) or set(value) != set(RECEIPT_FIXTURES):
        raise EnumerationError("fact-run receipt has an invalid command set")
    for fixture, command in value.items():
        if not isinstance(command, dict) or set(command) != {"subcommand", "system", "exit_code"}:
            raise EnumerationError("fact-run receipt has an invalid command")
        if (
            command["subcommand"] != "extract"
            or command["system"] != fixture
            or command["exit_code"] != 0
        ):
            raise EnumerationError("fact-run receipt does not prove successful extraction")


def _load_fact_run_receipt(
    path: Path, run: str, authorization: dict[str, Any]
) -> dict[str, Any]:
    binding = authorization["fact_runs"][run]
    _readonly_regular_file(path, "fact-run receipt")
    value, raw = load_canonical_json_bytes(path, "fact-run receipt")
    if sha256_bytes(raw) != binding["receipt_sha256"]:
        raise EnumerationError("fact-run receipt digest does not match authorization")
    if not isinstance(value, dict) or set(value) != FACT_RUN_RECEIPT_FIELDS:
        raise EnumerationError("fact-run receipt has an invalid schema")
    if value.get("schema") != FACT_RUN_RECEIPT_SCHEMA or value.get("status") != "COMPLETE":
        raise EnumerationError("fact-run receipt is not complete")
    if value.get("execution_mode") != "offline" or not _valid_run_id(value.get("run_id")):
        raise EnumerationError("fact-run receipt has invalid execution provenance")
    if value["run_id"] != binding["run_id"]:
        raise EnumerationError("fact-run receipt does not match the authorized run id")
    _validate_fact_run_commands(value.get("commands"))
    for field in (
        "derived_lock_sha256",
        "cache_tree_sha256",
        "t111_binary_sha256",
        "typedcalloracle_binary_sha256",
        "git_sha256",
        "go_sha256",
    ):
        if value.get(field) != authorization[field]:
            raise EnumerationError("fact-run receipt does not match sealed producer inputs")
    if value.get("producer_toolchain_identity") != authorization["producer_toolchain_identity"]:
        raise EnumerationError("fact-run receipt does not match sealed producer toolchain")
    if value.get("heads") != authorization["heads"]:
        raise EnumerationError("fact-run receipt does not match sealed heads")
    facts = value.get("facts_sha256")
    if not isinstance(facts, dict) or set(facts) != set(RECEIPT_FIXTURES):
        raise EnumerationError("fact-run receipt has invalid fact digests")
    if facts != binding["facts_sha256"]:
        raise EnumerationError("fact-run receipt does not match authorized facts")
    for digest in facts.values():
        require_sha256(digest, "fact-run receipt fact digest")
    return value


def _fact_digests(paths: dict[str, Path], authorization: dict[str, Any]) -> dict[str, str]:
    expected = authorization["fact_runs"]["run1"]["facts_sha256"]
    result: dict[str, str] = {}
    for fixture in RECEIPT_FIXTURES:
        digest = sha256_file(paths[fixture])
        if digest != expected[fixture]:
            raise EnumerationError("fact input changed after its sealed verification")
        result[fixture] = digest
    return result


def verify_fact_pairs(
    run1: Path, run2: Path, authorization: dict[str, Any]
) -> tuple[dict[str, Path], dict[str, Any]]:
    _readonly_directory(run1, "first fact run")
    _readonly_directory(run2, "second fact run")
    try:
        if run1.resolve(strict=True) == run2.resolve(strict=True):
            raise EnumerationError("fact recomputation runs must use distinct directories")
    except OSError as exc:
        raise EnumerationError("fact-run inputs cannot be resolved") from exc
    receipt1 = _load_fact_run_receipt(run1 / "fact-run-receipt.json", "run1", authorization)
    receipt2 = _load_fact_run_receipt(run2 / "fact-run-receipt.json", "run2", authorization)
    if receipt1["run_id"] == receipt2["run_id"]:
        raise EnumerationError("fact recomputation receipts do not identify separate runs")
    paths: dict[str, Path] = {}
    for fixture in RECEIPT_FIXTURES:
        first = run1 / f"{fixture}.facts.jsonl"
        second = run2 / f"{fixture}.facts.jsonl"
        _readonly_regular_file(first, "first fact run")
        _readonly_regular_file(second, "second fact run")
        digest = sha256_file(first)
        if (
            digest != sha256_file(second)
            or digest != authorization["fact_runs"]["run1"]["facts_sha256"][fixture]
            or digest != authorization["fact_runs"]["run2"]["facts_sha256"][fixture]
        ):
            raise EnumerationError("fact recomputation proof does not match authorization")
        try:
            first_info = first.stat()
            second_info = second.stat()
        except OSError as exc:
            raise EnumerationError("fact recomputation proof cannot be inspected") from exc
        if (first_info.st_dev, first_info.st_ino) == (second_info.st_dev, second_info.st_ino):
            raise EnumerationError("fact recomputation runs reuse the same fact file")
        for record in _raw_facts(first):
            if record.get("predicate") in FAILURE_PREDICATES:
                raise EnumerationError("fact run contains a producer failure")
        paths[fixture] = first
    return paths, {"run1": receipt1, "run2": receipt2}


def _typed_digest(rows: list[dict[str, Any]], diagnostics: dict[str, Any]) -> dict[str, str]:
    return {
        "population_sha256": sha256_bytes(canonical_json(rows).encode("utf-8")),
        "diagnostics_sha256": sha256_bytes(canonical_json(diagnostics).encode("utf-8")),
    }


def enumerate_raw_frames(
    lp: ModuleType,
    *,
    entries: dict[str, dict[str, Any]],
    corpus_dir: Path,
    facts: dict[str, Path],
    expected_toolchain_identity: str,
    authorized_tools: dict[str, Path],
) -> tuple[dict[str, list[dict[str, Any]]], dict[str, dict[str, str]]]:
    """Project the exact four population frames, with no burn or selection step."""
    prepared: dict[str, tuple[list[str], list[dict[str, Any]], list[dict[str, Any]], str]] = {}
    identities: set[str] = set()
    for fixture in RECEIPT_FIXTURES:
        entry = entries[fixture]
        root = corpus_dir / fixture
        try:
            tracked, _ = lp.pinned_tracked_files(
                root, entry["commit"], entry.get("excluded_gitlinks", [])
            )
            verified = lp.load_verified_facts(
                facts[fixture],
                system=fixture,
                expected_commit=entry["commit"],
                corpus_dir=corpus_dir,
            )
            call_raw, registration_raw = lp.operation_facts(
                fixture, facts[fixture].parent, entry["commit"], corpus_dir
            )
        except Exception as exc:
            raise EnumerationError("derived facts or corpus do not verify") from exc
        current = {str(row.get("toolchain_identity")) for row in verified}
        if len(current) != 1 or "None" in current:
            raise EnumerationError("fact run does not bind one producer toolchain")
        identities.update(current)
        prepared[fixture] = (tracked, call_raw, registration_raw, next(iter(current)))
    if len(identities) != 1:
        raise EnumerationError("fixtures were produced by different toolchains")
    if next(iter(identities)) != expected_toolchain_identity:
        raise EnumerationError("fact producer toolchain differs from authorization")
    try:
        oracle_toolchain = lp.resolve_oracle_toolchain(next(iter(identities)))
    except Exception as exc:
        raise EnumerationError("fact toolchain cannot bind the typed oracle") from exc
    if (
        oracle_toolchain.get("go_digest") != sha256_file(authorized_tools["go"])
        or oracle_toolchain.get("git_digest") != sha256_file(authorized_tools["git"])
    ):
        raise EnumerationError("typed oracle toolchain differs from authorization")
    try:
        if (
            Path(oracle_toolchain["go_path"]).resolve(strict=True) != authorized_tools["go"]
            or Path(oracle_toolchain["git_path"]).resolve(strict=True) != authorized_tools["git"]
        ):
            raise EnumerationError("typed oracle executable paths differ from authorization")
    except (KeyError, OSError) as exc:
        raise EnumerationError("typed oracle toolchain cannot be resolved") from exc

    frames = {frame: [] for frame in EXTERNAL_FRAMES}
    typed_proofs: dict[str, dict[str, str]] = {}
    for fixture in RECEIPT_FIXTURES:
        entry = entries[fixture]
        root = corpus_dir / fixture
        tracked, call_raw, registration_raw, _ = prepared[fixture]
        try:
            calls, _quarantined = lp.gate_call_facts(fixture, call_raw)
            first, first_diagnostics = lp.scan_typed_call_recall_frame(
                fixture,
                root,
                entry["commit"],
                entry.get("go_build_tags", []),
                entry["goos"],
                entry["goarch"],
                entry["go_tests"],
                set(tracked),
                oracle_toolchain,
            )
            second, second_diagnostics = lp.scan_typed_call_recall_frame(
                fixture,
                root,
                entry["commit"],
                entry.get("go_build_tags", []),
                entry["goos"],
                entry["goarch"],
                entry["go_tests"],
                set(tracked),
                oracle_toolchain,
            )
            if canonical_json(first) != canonical_json(second) or canonical_json(first_diagnostics) != canonical_json(second_diagnostics):
                raise EnumerationError("typed oracle did not reproduce byte-identically")
            registration_recall = lp.scan_registration_recall_frame(
                fixture, root, entry["commit"], tracked
            )
            call_precision = lp.build_precision_frame(
                fixture, root, entry["commit"], calls, set(tracked)
            )
            registration_precision = lp.build_registration_precision_frame(
                fixture, root, entry["commit"], registration_raw, set(tracked)
            )
            lp.align_recall(first, call_precision)
            lp.align_registration(registration_recall, registration_precision)
            typed_sites = {row["site_id"] for row in first}
            registration_sites = {row["site_id"] for row in registration_recall}
            if any(row["site_id"] not in typed_sites for row in call_precision):
                raise EnumerationError("precision frame is outside its typed recall frame")
            if any(row["site_id"] not in registration_sites for row in registration_precision):
                raise EnumerationError("registration precision frame is outside its recall frame")
        except EnumerationError:
            raise
        except Exception as exc:
            raise EnumerationError("sealed frame enumeration rejected") from exc
        frames["client_call_recall"].extend(first)
        frames["client_call_precision"].extend(call_precision)
        frames["registration_recall"].extend(registration_recall)
        frames["registration_precision"].extend(registration_precision)
        typed_proofs[fixture] = _typed_digest(first, first_diagnostics)
    return frames, typed_proofs


def stage2_inputs(frames: dict[str, list[dict[str, Any]]]) -> tuple[dict[str, dict[str, int]], dict[str, list[str]]]:
    if set(frames) != set(EXTERNAL_FRAMES):
        raise EnumerationError("frame construction did not yield the exact Stage-2 frame set")
    cardinalities: dict[str, dict[str, int]] = {}
    membership: dict[str, list[str]] = {}
    for frame in EXTERNAL_FRAMES:
        rows = frames[frame]
        keys: set[str] = set()
        for row in rows:
            try:
                system = row["system"]
                path = row["path"]
                raw_line = row["line"]
                line = int(raw_line)
            except (KeyError, TypeError, ValueError) as exc:
                raise EnumerationError("frame row lacks a valid site coordinate") from exc
            if (
                not isinstance(system, str)
                or not isinstance(path, str)
                or isinstance(raw_line, bool)
                or system not in RECEIPT_FIXTURES
                or not path
                or line <= 0
            ):
                raise EnumerationError("frame row lacks a valid site coordinate")
            if (
                any(character in system or character in path for character in (":", "\n", "\r"))
                or path.startswith("/")
                or any(part in {"", ".", ".."} for part in path.split("/"))
            ):
                raise EnumerationError("frame row has an ambiguous sampling-site coordinate")
            # Stage-2's mapper and burn ledger operate on the sampling-site
            # coordinate.  Precision rows retain wider extractor evidence
            # spans, but those spans are not the census unit.
            key = f"{system}:{path}:{line}:{line}"
            if key in keys:
                raise EnumerationError("frame has duplicate sampling-site coordinates")
            keys.add(key)
        if len(keys) != len(rows):
            raise EnumerationError("frame membership does not cover every sampling site")
        cardinalities[frame] = {"population": len(rows), "census": 0}
        membership[frame] = sorted(keys)
    return cardinalities, membership


def _write_durable_at(directory_fd: int, name: str, payload: bytes) -> None:
    if not name or "/" in name or name in {".", ".."}:
        raise EnumerationError("output artifact name is unsafe")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag("output publication")
    descriptor = os.open(name, flags, 0o600, dir_fd=directory_fd)
    try:
        os.fchmod(descriptor, 0o600)
        view = memoryview(payload)
        while view:
            written = os.write(descriptor, view)
            if written <= 0:
                raise OSError("short write")
            view = view[written:]
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _rename_directory_no_replace(parent_fd: int, source: str, destination: str) -> None:
    """Atomically rename a staged directory only if its destination is absent.

    The accepted fire-time platform is macOS.  ``renameatx_np(RENAME_EXCL)``
    is the OS primitive that closes the check-then-rename overwrite race;
    ordinary POSIX ``rename`` would replace a concurrent destination.  There
    is intentionally no unsafe fallback on a platform without this primitive.
    """
    if sys.platform != "darwin":
        raise EnumerationError("sealed output publication requires macOS no-replace rename support")
    try:
        libc = ctypes.CDLL(None, use_errno=True)
        renameatx_np = libc.renameatx_np
        renameatx_np.argtypes = (
            ctypes.c_int,
            ctypes.c_char_p,
            ctypes.c_int,
            ctypes.c_char_p,
            ctypes.c_uint,
        )
        renameatx_np.restype = ctypes.c_int
    except (AttributeError, OSError) as exc:
        raise EnumerationError("sealed output publication lacks no-replace rename support") from exc
    result = renameatx_np(
        parent_fd,
        source.encode("utf-8"),
        parent_fd,
        destination.encode("utf-8"),
        0x00000004,  # RENAME_EXCL, available on the sealed macOS target.
    )
    if result == 0:
        return
    error = ctypes.get_errno()
    if error == errno.EEXIST:
        raise EnumerationError("output already exists at publication")
    raise EnumerationError("output directory cannot be atomically published")


def _remove_private_staging(
    parent_fd: int, staging_name: str, expected_names: Iterable[str]
) -> None:
    """Remove only files this invocation created; leave foreign state intact."""
    flags = os.O_RDONLY | _directory_flag("output publication") | _no_follow_flag("output publication")
    try:
        staging_fd = os.open(staging_name, flags, dir_fd=parent_fd)
    except OSError:
        return
    try:
        for name in expected_names:
            try:
                os.unlink(name, dir_fd=staging_fd)
            except FileNotFoundError:
                pass
        os.fsync(staging_fd)
    except OSError:
        return
    finally:
        os.close(staging_fd)
    try:
        os.rmdir(staging_name, dir_fd=parent_fd)
        os.fsync(parent_fd)
    except OSError:
        # A concurrent unexpected entry is a durable tombstone, not a reason
        # to traverse or delete an attacker-controlled directory tree.
        pass


def _open_private_state_parent(path: Path, label: str) -> int:
    """Open an owner-only state parent without following a path component."""
    if not path.is_absolute() or path.name in {"", ".", ".."}:
        raise EnumerationError(f"{label} has an invalid path")
    if ".." in path.parts:
        raise EnumerationError(f"{label} has an invalid path")
    flags = os.O_RDONLY | _directory_flag(label) | _no_follow_flag(label)
    try:
        descriptor = os.open(path.anchor, flags)
    except OSError as exc:
        raise EnumerationError(f"{label} parent is unavailable") from exc
    try:
        for part in path.parts[1:-1]:
            next_descriptor = os.open(part, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = next_descriptor
        info = os.fstat(descriptor)
        if not stat.S_ISDIR(info.st_mode):
            raise EnumerationError(f"{label} parent is not a real directory")
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise EnumerationError(f"{label} parent must be owner-only")
        return descriptor
    except OSError as exc:
        os.close(descriptor)
        raise EnumerationError(f"{label} parent is unavailable") from exc
    except BaseException:
        os.close(descriptor)
        raise


def _canonical_private_directory(path: Path, label: str) -> Path:
    """Resolve an existing owner-only directory after an O_NOFOLLOW walk."""
    if not path.is_absolute() or path.name in {"", ".", ".."} or ".." in path.parts:
        raise EnumerationError(f"{label} has an invalid path")
    # Opening this artificial child's parent walks *through* path itself with
    # O_DIRECTORY|O_NOFOLLOW and verifies the final directory ownership/mode.
    descriptor = _open_private_state_parent(path / ".state-probe", label)
    try:
        try:
            resolved = path.resolve(strict=True)
        except OSError as exc:
            raise EnumerationError(f"{label} cannot be resolved") from exc
        try:
            info = resolved.lstat()
        except OSError as exc:
            raise EnumerationError(f"{label} cannot be inspected") from exc
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise EnumerationError(f"{label} is not a real directory")
        return resolved
    finally:
        os.close(descriptor)


def _canonical_state_target(path: Path, label: str) -> Path:
    """Canonicalize a future state target through its checked private parent."""
    descriptor = _open_private_state_parent(path, label)
    try:
        try:
            parent = path.parent.resolve(strict=True)
        except OSError as exc:
            raise EnumerationError(f"{label} parent cannot be resolved") from exc
        return parent / path.name
    finally:
        os.close(descriptor)


def _open_private_state_record(path: Path, label: str) -> int:
    """Open an existing owner-private direct state record without links."""
    parent_fd = _open_private_state_parent(path, label)
    try:
        descriptor = os.open(
            path.name, os.O_RDONLY | _no_follow_flag(label), dir_fd=parent_fd
        )
    except OSError as exc:
        os.close(parent_fd)
        raise EnumerationError(f"{label} is unavailable") from exc
    os.close(parent_fd)
    try:
        info = os.fstat(descriptor)
        if (
            not stat.S_ISREG(info.st_mode)
            or info.st_uid != os.getuid()
            or stat.S_IMODE(info.st_mode) & 0o077
        ):
            raise EnumerationError(f"{label} is not an owner-private regular file")
        return descriptor
    except BaseException:
        os.close(descriptor)
        raise


def _read_opened_canonical_json(descriptor: int, label: str) -> tuple[Any, bytes]:
    """Read a no-follow descriptor whose ancestry was already checked."""
    chunks: list[bytes] = []
    try:
        while True:
            block = os.read(descriptor, 1024 * 1024)
            if not block:
                break
            chunks.append(block)
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be read") from exc
    raw = b"".join(chunks)
    try:
        value = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise EnumerationError(f"{label} is not valid JSON") from exc
    if raw != (canonical_json(value) + "\n").encode("utf-8"):
        raise EnumerationError(f"{label} is not canonical JSON")
    return value, raw


def _open_p0_state_records(
    state_targets: dict[str, Path],
) -> dict[str, tuple[Any, bytes]]:
    """Open all three P0 records before reading any one of them.

    This holds a descriptor for M0, R0, and T0 only after each has passed the
    owner-private, no-symlink walk.  It avoids a read-time name swap between
    separately verified state paths.
    """
    labels = {
        "consumption_marker": "P0 consumption marker",
        "evidence_receipt": "P0 evidence receipt",
        "terminal_receipt": "P0 terminal receipt",
    }
    descriptors: dict[str, int] = {}
    try:
        for name in ("consumption_marker", "evidence_receipt", "terminal_receipt"):
            descriptors[name] = _open_private_state_record(state_targets[name], labels[name])
        return {
            name: _read_opened_canonical_json(descriptors[name], labels[name])
            for name in ("consumption_marker", "evidence_receipt", "terminal_receipt")
        }
    finally:
        for descriptor in descriptors.values():
            try:
                os.close(descriptor)
            except OSError:
                pass


def _canonical_reserved_namespace(path: Path, label: str) -> Path:
    """Normalize a sealed historical namespace even if it was later removed.

    A prior ceremony directory is a reservation, not evidence that its
    contents remain on disk.  Walk every existing component without links,
    then append any absent suffix to the strict canonical parent.  New
    Stage-2 state still uses the stronger owner-private O_NOFOLLOW opener.
    """
    if not path.is_absolute() or path.name in {"", ".", ".."} or ".." in path.parts:
        raise EnumerationError(f"{label} has an invalid path")
    current = Path(path.anchor)
    for index, part in enumerate(path.parts[1:]):
        candidate = current / part
        try:
            info = candidate.lstat()
        except FileNotFoundError:
            try:
                canonical_parent = current.resolve(strict=True)
            except OSError as exc:
                raise EnumerationError(f"{label} parent cannot be resolved") from exc
            return canonical_parent.joinpath(*path.parts[index + 1 :])
        except OSError as exc:
            raise EnumerationError(f"{label} cannot be inspected") from exc
        if stat.S_ISLNK(info.st_mode):
            raise EnumerationError(f"{label} contains a symlink")
        if index < len(path.parts[1:]) - 1 and not stat.S_ISDIR(info.st_mode):
            raise EnumerationError(f"{label} has a non-directory path component")
        current = candidate
    try:
        return current.resolve(strict=True)
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be resolved") from exc


def _prior_ceremony_namespace(path: Path, label: str) -> Path | None:
    """Return the specific child of a ``ceremony`` root named by a path."""
    if not path.is_absolute() or ".." in path.parts:
        raise EnumerationError(f"{label} has an invalid state path")
    try:
        index = path.parts.index("ceremony")
    except ValueError:
        return None
    if index + 1 >= len(path.parts):
        raise EnumerationError(f"{label} names a ceremony root without a namespace")
    # Preserve the leading slash in Path.parts while selecting the particular
    # historical ceremony child (for example, ``attempt-3`` rather than the
    # broad parent that may legitimately contain a new Stage-2 ceremony).
    return Path(*path.parts[: index + 2])


def _ceremony_paths_in(value: Any, label: str) -> Iterable[Path]:
    if isinstance(value, dict):
        for child in value.values():
            yield from _ceremony_paths_in(child, label)
    elif isinstance(value, list):
        for child in value:
            yield from _ceremony_paths_in(child, label)
    elif isinstance(value, str) and Path(value).is_absolute():
        if not _valid_absolute_path_text(value):
            raise EnumerationError(f"{label} has an invalid absolute path")
        namespace = _prior_ceremony_namespace(Path(value), label)
        if namespace is not None:
            yield namespace


def _load_prior_ceremony_roots(authorization: dict[str, Any]) -> tuple[set[str], set[Path]]:
    """Load the independently sealed historical authorization registry.

    It is deliberately not an optional list embedded only in this new
    authorization: the prior estimator authorization is an existing,
    committed artifact with a separately bound digest.  Its three state
    files must identify one real ceremony directory.
    """
    binding = authorization["prior_authorizations"]["estimator"]
    value, raw = load_json_bytes(ESTIMATOR_AUTHORIZATION_PATH, "prior estimator authorization")
    if sha256_bytes(raw) != binding["sha256"]:
        raise EnumerationError("prior estimator authorization differs from its binding")
    if raw != _head_blob(ESTIMATOR_AUTHORIZATION_REL):
        raise EnumerationError("prior estimator authorization is not a committed sealed record")
    if (
        not isinstance(value, dict)
        or value.get("schema") != "t111-estimator-authorization-v1"
        or not isinstance(value.get("authorization_id"), str)
        or not value["authorization_id"]
        or not isinstance(value.get("state"), dict)
    ):
        raise EnumerationError("prior estimator authorization has an invalid state record")
    state = value["state"]
    if set(state) != {"admission_receipt", "consumption_marker", "result_receipt"}:
        raise EnumerationError("prior estimator authorization has an invalid state record")
    paths: list[Path] = []
    for field, text in state.items():
        if not _valid_absolute_path_text(text):
            raise EnumerationError("prior estimator authorization has an invalid state path")
        paths.append(_canonical_state_target(Path(text), f"prior estimator {field}"))
    parents = {path.parent for path in paths}
    if len(parents) != 1:
        raise EnumerationError("prior estimator authorization state paths do not share one ceremony directory")
    raw_namespaces = set(_ceremony_paths_in(value, "prior estimator authorization"))
    if not raw_namespaces:
        raise EnumerationError("prior estimator authorization has no sealed ceremony namespace")
    namespaces = {
        _canonical_reserved_namespace(path, "prior estimator ceremony directory")
        for path in raw_namespaces
    }
    if next(iter(parents)) not in namespaces:
        raise EnumerationError("prior estimator authorization state is outside its sealed ceremony namespace")
    return {value["authorization_id"]}, namespaces


def verify_authorized_state_namespaces(
    authorization: dict[str, Any], completed_p0: dict[str, Any],
) -> None:
    """Enforce fresh private state outside estimator and completed-P0 ceremony.

    ``completed_p0`` comes only from :func:`verify_prebuild_evidence`, after
    its M0/R0/T0 chain has been opened and proven complete.  Treating it as a
    prior reservation closes the otherwise available exact/ancestor/descendant
    collision between P0's irreversible state and the final marker.
    """
    prior_ids, prior_roots = _load_prior_ceremony_roots(authorization)
    if (
        not isinstance(completed_p0, dict)
        or not isinstance(completed_p0.get("authorization_id"), str)
        or not completed_p0["authorization_id"]
    ):
        raise EnumerationError("completed P0 has no usable ceremony reservation")
    p0_state = _canonical_p0_state_targets(completed_p0)
    prior_ids = {*prior_ids, completed_p0["authorization_id"]}
    prior_roots = {*prior_roots, p0_state["ceremony_directory"]}
    if authorization["authorization_id"] in prior_ids:
        raise EnumerationError("enumeration authorization reuses a prior authorization identifier")
    ceremony = _canonical_private_directory(
        ceremony_directory(authorization), "enumeration ceremony directory"
    )
    output, marker, terminal = state_paths(authorization)
    canonical_targets = (
        _canonical_state_target(output, "output path"),
        _canonical_state_target(marker, "consumption marker"),
        _canonical_state_target(terminal, "terminal receipt"),
    )
    for target in canonical_targets:
        try:
            target.relative_to(ceremony)
        except ValueError as exc:
            raise EnumerationError("enumeration state target escapes its ceremony directory") from exc
        if target == ceremony:
            raise EnumerationError("enumeration state target collides with its ceremony directory")
    if any(
        _state_paths_overlap(first, second)
        for first, second in (
            (canonical_targets[0], canonical_targets[1]),
            (canonical_targets[0], canonical_targets[2]),
            (canonical_targets[1], canonical_targets[2]),
        )
    ):
        raise EnumerationError("enumeration state targets collide")
    for prior in prior_roots:
        for candidate in (ceremony, *canonical_targets):
            if _state_paths_overlap(candidate, prior):
                raise EnumerationError("enumeration state overlaps a sealed prior ceremony")


def _state_target_exists(parent_fd: int, name: str, label: str) -> bool:
    try:
        os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
    except FileNotFoundError:
        return False
    except OSError as exc:
        raise EnumerationError(f"{label} cannot be inspected") from exc
    return True


def _verify_unused_terminal_state(authorization: dict[str, Any]) -> None:
    """Ensure the two irreversible state files are fresh before O_EXCL."""
    _out, marker, terminal = state_paths(authorization)
    for path, label in ((marker, "consumption marker"), (terminal, "terminal receipt")):
        parent_fd = _open_private_state_parent(path, label)
        try:
            if _state_target_exists(parent_fd, path.name, label):
                raise EnumerationError(f"enumeration {label} already exists")
        finally:
            os.close(parent_fd)


def _write_all(descriptor: int, payload: bytes) -> None:
    view = memoryview(payload)
    while view:
        written = os.write(descriptor, view)
        if written <= 0:
            raise OSError("short write")
        view = view[written:]


def _consume_authorization(
    authorization: dict[str, Any], authorization_sha256: str, transition: dict[str, Any]
) -> str:
    """Durably burn this enumeration authorization before derived-root I/O."""
    _out, marker, _terminal = state_paths(authorization)
    payload = (canonical_json({
        "authorization_sha256": authorization_sha256,
        "output_dir": authorization["state"]["output_dir"],
        "schema": CONSUMPTION_MARKER_SCHEMA,
        "status": "CONSUMED",
    }) + "\n").encode("utf-8")
    marker_sha256 = sha256_bytes(payload)
    parent_fd = _open_private_state_parent(marker, "consumption marker")
    transition["marker_sha256"] = marker_sha256
    prior_mask = _block_catchable_signals()
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag("consumption marker")
    try:
        pending = _pending_catchable_signal()
        if pending is not None:
            raise EnumerationInterrupted(pending)
        descriptor = os.open(marker.name, flags, 0o600, dir_fd=parent_fd)
        # The caller consults this mutable state directly, closing the
        # return-value handoff between O_EXCL and post-transition handling.
        transition["started"] = True
    except FileExistsError as exc:
        os.close(parent_fd)
        _restore_signal_mask(prior_mask)
        raise EnumerationError("enumeration authorization was already consumed") from exc
    except OSError as exc:
        os.close(parent_fd)
        _restore_signal_mask(prior_mask)
        raise EnumerationError("enumeration consumption marker cannot be created") from exc
    except BaseException:
        os.close(parent_fd)
        _restore_signal_mask(prior_mask)
        raise
    try:
        try:
            os.fchmod(descriptor, 0o600)
            _write_all(descriptor, payload)
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        os.fsync(parent_fd)
    except BaseException as exc:
        # The O_EXCL creation already burns the authorization.  Do not remove
        # this tombstone on any post-create failure.
        raise MarkerTransitionError(
            "enumeration authorization consumption is indeterminate", marker_sha256
        ) from exc
    finally:
        os.close(parent_fd)
        _restore_signal_mask(prior_mask)
    return marker_sha256


def _terminal_failure(exc: BaseException) -> dict[str, str]:
    """Persist a root-cause category without serializing potentially sensitive text."""
    if isinstance(exc, MarkerTransitionError):
        code = "marker-persistence-indeterminate"
    elif isinstance(exc, EnumerationInterrupted):
        code = "interrupted"
    elif isinstance(exc, subprocess.TimeoutExpired):
        code = "timeout"
    elif isinstance(exc, EnumerationError):
        code = "refused"
    else:
        code = "unexpected-failure"
    detail = f"{type(exc).__name__}: {exc}".encode("utf-8", errors="replace")
    return {"code": code, "detail_sha256": sha256_bytes(detail), "type": type(exc).__name__}


def _write_terminal_receipt(
    authorization: dict[str, Any],
    authorization_sha256: str,
    marker_sha256: str,
    terminal_state: dict[str, Any],
    *,
    status: str,
    exit_code: int,
    failure: dict[str, str] | None,
    outputs: dict[str, str] | None,
) -> str:
    """Publish the single, non-disclosing terminal result after consumption."""
    if status not in {"COMPLETED", "ABORTED"}:
        raise TerminalReceiptError("invalid terminal receipt status")
    if (status == "COMPLETED") != (failure is None and outputs is not None and exit_code == 0):
        raise TerminalReceiptError("invalid terminal receipt completion state")
    if status == "ABORTED" and (failure is None or outputs is not None or exit_code not in {2, 4}):
        raise TerminalReceiptError("invalid terminal receipt abort state")
    _out, _marker, terminal = state_paths(authorization)
    payload = (canonical_json({
        "authorization_sha256": authorization_sha256,
        "consumption_marker_sha256": marker_sha256,
        "exit_code": exit_code,
        "failure": failure,
        "outputs": outputs,
        "schema": TERMINAL_RECEIPT_SCHEMA,
        "status": status,
    }) + "\n").encode("utf-8")
    prior_mask = _block_catchable_signals()
    ignored_signals: dict[int, Any] = {}
    parent_fd: int | None = None
    temporary = f".{terminal.name}.{secrets.token_hex(16)}.tmp"
    try:
        try:
            try:
                parent_fd = _open_private_state_parent(terminal, "terminal receipt")
            except EnumerationError as exc:
                raise TerminalReceiptError("terminal receipt parent cannot be opened") from exc
            try:
                flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag("terminal receipt")
                try:
                    descriptor = os.open(temporary, flags, 0o600, dir_fd=parent_fd)
                except FileExistsError as exc:
                    raise TerminalReceiptError("terminal receipt staging path already exists") from exc
                except OSError as exc:
                    raise TerminalReceiptError("terminal receipt staging cannot be created") from exc
                try:
                    os.fchmod(descriptor, 0o600)
                    _write_all(descriptor, payload)
                    os.fsync(descriptor)
                finally:
                    os.close(descriptor)
                try:
                    os.link(
                        temporary,
                        terminal.name,
                        src_dir_fd=parent_fd,
                        dst_dir_fd=parent_fd,
                        follow_symlinks=False,
                    )
                except FileExistsError as exc:
                    raise TerminalReceiptError("terminal receipt already exists") from exc
                except OSError as exc:
                    raise TerminalReceiptError("terminal receipt cannot be published") from exc
                os.fsync(parent_fd)
                # This is the exact durable boundary.  It must happen before
                # deferred signals are unblocked back into the caller.
                terminal_state["written"] = True
                terminal_state["status"] = status
                try:
                    os.unlink(temporary, dir_fd=parent_fd)
                    temporary = ""
                    os.fsync(parent_fd)
                except OSError:
                    # The final linked receipt is already durable; a stale
                    # private staging file is a non-consuming cleanup issue.
                    pass
            finally:
                if temporary:
                    try:
                        os.unlink(temporary, dir_fd=parent_fd)
                    except OSError:
                        pass
                os.close(parent_fd)
        except TerminalReceiptError:
            raise
        except BaseException as exc:
            # Never delete a terminal file after O_EXCL.  Its incomplete
            # presence is itself an indeterminate, consumed one-shot state.
            raise TerminalReceiptError("terminal receipt persistence is indeterminate") from exc
    finally:
        _restore_signal_mask(prior_mask)
        _restore_signal_guards(ignored_signals)
    return sha256_bytes(payload)


def _catchable_signals() -> set[int]:
    return {
        getattr(signal, name)
        for name in (
            "SIGINT", "SIGTERM", "SIGHUP", "SIGQUIT", "SIGALRM", "SIGUSR1", "SIGUSR2",
            "SIGXCPU", "SIGXFSZ",
        )
        if hasattr(signal, name)
    }


def _require_signal_transition_support() -> None:
    """Refuse unless this interpreter can close the O_EXCL signal handoff."""
    blocker = getattr(signal, "pthread_sigmask", None)
    sig_block = getattr(signal, "SIG_BLOCK", None)
    sig_setmask = getattr(signal, "SIG_SETMASK", None)
    pending = getattr(signal, "sigpending", None)
    if not callable(blocker) or sig_block is None or sig_setmask is None or not callable(pending):
        raise EnumerationError("sealed enumeration requires pthread signal-mask support")
    try:
        previous = set(blocker(sig_block, set()))
        pending()
        blocker(sig_setmask, previous)
    except (OSError, RuntimeError, ValueError, TypeError) as exc:
        raise EnumerationError("sealed enumeration signal transition support is unavailable") from exc


def _block_catchable_signals() -> set[int]:
    """Defer catchable signals across an exclusive state transition."""
    blocker = getattr(signal, "pthread_sigmask", None)
    sig_block = getattr(signal, "SIG_BLOCK", None)
    if not callable(blocker) or sig_block is None:
        raise EnumerationError("sealed enumeration signal transition support is unavailable")
    try:
        return set(blocker(sig_block, _catchable_signals()))
    except (OSError, RuntimeError, ValueError, TypeError) as exc:
        raise EnumerationError("sealed enumeration signal transition support is unavailable") from exc


def _restore_signal_mask(previous: set[int]) -> None:
    blocker = getattr(signal, "pthread_sigmask", None)
    sig_setmask = getattr(signal, "SIG_SETMASK", None)
    if not callable(blocker) or sig_setmask is None:
        return
    try:
        blocker(sig_setmask, previous)
    except (OSError, RuntimeError, ValueError, TypeError):
        pass


def _pending_catchable_signal() -> int | None:
    pending = getattr(signal, "sigpending", None)
    if pending is None:
        return None
    try:
        caught = set(pending()) & _catchable_signals()
    except (OSError, RuntimeError, ValueError):
        return None
    return min(caught) if caught else None


def _install_signal_guards() -> dict[int, Any]:
    """Turn catchable stop/timeout signals into a terminal-receipt outcome."""
    previous: dict[int, Any] = {}

    def interrupted(signum: int, _frame: Any) -> None:
        raise EnumerationInterrupted(signum)

    for signum in _catchable_signals():
        try:
            previous[signum] = signal.getsignal(signum)
            signal.signal(signum, interrupted)
        except (OSError, RuntimeError, ValueError):
            pass
    return previous


def _restore_signal_guards(previous: dict[int, Any]) -> None:
    for signum, handler in previous.items():
        try:
            signal.signal(signum, handler)
        except (OSError, RuntimeError, ValueError):
            pass


def verify_authorized_output(out: Path, authorization: dict[str, Any]) -> bool:
    authorized_out, _marker, _terminal = state_paths(authorization)
    if not out.is_absolute() or out != authorized_out:
        raise EnumerationError("output path differs from enumeration authorization")
    parent_fd = _open_private_state_parent(out, "output path")
    try:
        staging_name = f".{out.name}.tmp"
        return not (
            _state_target_exists(parent_fd, out.name, "output path")
            or _state_target_exists(parent_fd, staging_name, "output staging path")
        )
    finally:
        os.close(parent_fd)


def publish_outputs(out: Path, outputs: dict[str, Any]) -> dict[str, str]:
    """Atomically publish the complete private output directory without replace."""
    expected_names = (
        "cardinalities.json",
        "frame-membership.json",
        "enumeration-receipt.json",
    )
    if set(outputs) != set(expected_names):
        raise EnumerationError("output set has an invalid artifact set")
    parent_fd = _open_private_state_parent(out, "output path")
    staging_name = f".{out.name}.tmp"
    staging_created = False
    staging_fd: int | None = None
    try:
        if _state_target_exists(parent_fd, out.name, "output path"):
            raise EnumerationError("output already exists at publication")
        if _state_target_exists(parent_fd, staging_name, "output staging path"):
            raise EnumerationError("output staging path already exists")
        os.mkdir(staging_name, mode=0o700, dir_fd=parent_fd)
        staging_created = True
        staging_fd = os.open(
            staging_name,
            os.O_RDONLY | _directory_flag("output publication") | _no_follow_flag("output publication"),
            dir_fd=parent_fd,
        )
        staging_info = os.fstat(staging_fd)
        if not stat.S_ISDIR(staging_info.st_mode) or staging_info.st_uid != os.getuid():
            raise EnumerationError("output staging directory is unsafe")
        digests: dict[str, str] = {}
        for name in expected_names:
            value = outputs[name]
            payload = (canonical_json(value) + "\n").encode("utf-8")
            _write_durable_at(staging_fd, name, payload)
            digests[name] = sha256_bytes(payload)
        os.fsync(staging_fd)
        _rename_directory_no_replace(parent_fd, staging_name, out.name)
        staging_created = False
        os.fsync(parent_fd)
        return digests
    except BaseException:
        if staging_created:
            _remove_private_staging(parent_fd, staging_name, expected_names)
        raise
    finally:
        if staging_fd is not None:
            try:
                os.close(staging_fd)
            except OSError:
                pass
        os.close(parent_fd)


def run(args: argparse.Namespace) -> int:
    out = Path(args.out)

    # Keep this gate first: without a future signed amendment, do not inspect
    # a derived root, source tree, cache, corpus, or fact file.
    authorization, authorization_bytes = load_authorization()
    prebuild_evidence, prebuild_evidence_bytes = load_prebuild_evidence()
    verify_authorized_toolchain_identity(authorization)
    if sha256_file(Path(__file__)) != authorization["enumerator_sha256"]:
        raise EnumerationError("enumerator bytes do not match authorization")
    authorization_sha256 = verify_committed_authorization(authorization, authorization_bytes)
    completed_p0 = verify_prebuild_evidence(
        authorization, prebuild_evidence, prebuild_evidence_bytes, args
    )
    verify_interpreter(authorization)
    # Verify and install the exact no-network Git/Go environment before any
    # Stage-0 key, receipt, derived lock, cache, corpus, or fact byte is
    # opened or hashed.
    authorized_tools = configure_sealed_execution_environment(authorization)
    verify_authorized_state_namespaces(authorization, completed_p0)
    inventory, stage0_inventory_sha256 = read_inventory_sealed(STAGE0_INVENTORY)
    if stage0_inventory_sha256 != authorization["stage0_inventory_sha256"]:
        raise EnumerationError("Stage-0 inventory digest does not match authorization")
    for relative in (
        PROTOCOL_REL,
        "spike/t111/labeling/gate2-v2/stage0/snapshot-query.graphql",
        "spike/t111/labeling/gate2-v2/stage0/snapshot-constants.json",
    ):
        expected = inventory.get(relative)
        if expected is None or sha256_file(REPO_ROOT / relative) != expected:
            raise EnumerationError("Stage-1 sealed input does not match Stage-0 inventory")
    receipt, receipt_sha256 = verify_receipt(Path(args.receipt), authorization)
    _verify_unused_terminal_state(authorization)
    if not verify_authorized_output(out, authorization):
        print("stage2-enumeration: refusing — output state already exists", file=sys.stderr)
        return 3
    _require_signal_transition_support()
    previous_signals = _install_signal_guards()
    transition: dict[str, Any] = {"started": False, "marker_sha256": ""}
    terminal_state: dict[str, Any] = {"written": False, "status": None}
    try:
        try:
            consumption_marker_sha256 = _consume_authorization(
                authorization, authorization_sha256, transition
            )
        except MarkerTransitionError as exc:
            # O_EXCL succeeded, so this is a consumed, indeterminate run even
            # when the marker file or parent fsync could not be confirmed.
            transition["marker_sha256"] = exc.marker_sha256
            raise

        supplied_root = Path(args.execution_root)
        if not supplied_root.is_absolute():
            raise EnumerationError("execution root must be absolute")
        try:
            supplied_info = supplied_root.lstat()
            if stat.S_ISLNK(supplied_info.st_mode):
                raise EnumerationError("execution root may not be a symlink")
            root = supplied_root.resolve(strict=True)
        except OSError as exc:
            raise EnumerationError("execution root is unavailable") from exc
        if not root.is_dir() or root.is_symlink():
            raise EnumerationError("execution root is not a real directory")
        module_dir = within(root / "spike" / "t111", root, "derived t111 root")
        _readonly_ancestry(root, module_dir, "derived t111 root")
        derived_lock = within(module_dir / "corpus.lock.json", root, "derived corpus lock")
        _readonly_regular_file(derived_lock, "derived corpus lock")
        entries = verify_derived_lock(STAGE0_LOCK, derived_lock, receipt, authorization, inventory)
        verify_bound_harness(root, authorization)
        lp = load_execution_label_prep(root, inventory)
        install_oracle_environment_guard(lp)

        corpus_dir = within(module_dir / "corpus", root, "derived corpus")
        _readonly_directory(corpus_dir, "derived corpus")
        for fixture in RECEIPT_FIXTURES:
            fixture_root = within(corpus_dir / fixture, root, "derived corpus fixture")
            verify_sealed_git_repository(fixture_root, root)
        facts_run1 = within(Path(args.facts_run1), root, "first fact run")
        facts_run2 = within(Path(args.facts_run2), root, "second fact run")
        _readonly_ancestry(root, facts_run1, "first fact run")
        _readonly_ancestry(root, facts_run2, "second fact run")
        facts, fact_receipts = verify_fact_pairs(facts_run1, facts_run2, authorization)
        frames, typed_proofs = enumerate_raw_frames(
            lp,
            entries=entries,
            corpus_dir=corpus_dir,
            facts=facts,
            expected_toolchain_identity=authorization["producer_toolchain_identity"],
            authorized_tools=authorized_tools,
        )
        fact_digests = _fact_digests(facts, authorization)
        cardinalities, membership = stage2_inputs(frames)
        receipt_body = {
            "schema": "t111-gate2-v2-stage2-enumeration-receipt-v1",
            "mode": "sealed",
            "authorization_sha256": authorization_sha256,
            "consumption_marker_sha256": consumption_marker_sha256,
            "plan_sha256": sha256_file(PLAN_PATH),
            "receipt_sha256": receipt_sha256,
            "stage0_inventory_sha256": sha256_file(STAGE0_INVENTORY),
            "protocol_sha256": sha256_file(REPO_ROOT / PROTOCOL_REL),
            "base_lock_sha256": sha256_file(STAGE0_LOCK),
            "derived_lock_sha256": sha256_file(derived_lock),
            "derived_harness_manifest_sha256": sha256_file(root / "spike" / "t111" / ".module-cache" / "manifest.json"),
            "cache_tree_sha256": authorization["cache_tree_sha256"],
            "python_sha256": authorization["python_sha256"],
            "git_sha256": authorization["git_sha256"],
            "go_sha256": authorization["go_sha256"],
            "producer_toolchain_identity": authorization["producer_toolchain_identity"],
            "facts_sha256": fact_digests,
            "fact_runs": {
                run: {
                    "receipt_sha256": authorization["fact_runs"][run]["receipt_sha256"],
                    "run_id": fact_receipts[run]["run_id"],
                }
                for run in ("run1", "run2")
            },
            "typed_oracle": typed_proofs,
            "frame_counts": {frame: cardinalities[frame]["population"] for frame in EXTERNAL_FRAMES},
        }
        output_values: dict[str, Any] = {
            "cardinalities.json": cardinalities,
            "frame-membership.json": membership,
        }
        preliminary = {
            name: sha256_bytes((canonical_json(value) + "\n").encode("utf-8"))
            for name, value in output_values.items()
        }
        receipt_body["outputs"] = preliminary
        output_values["enumeration-receipt.json"] = receipt_body
        output_digests = publish_outputs(out, output_values)
        terminal_receipt_sha256 = _write_terminal_receipt(
            authorization,
            authorization_sha256,
            consumption_marker_sha256,
            terminal_state,
            status="COMPLETED",
            exit_code=0,
            failure=None,
            outputs=output_digests,
        )
        summary = {
            "schema": "t111-gate2-v2-stage2-enumeration-summary-v1",
            "frame_counts": receipt_body["frame_counts"],
            "output_digests": output_digests,
            "terminal_receipt_sha256": terminal_receipt_sha256,
        }
        print(json.dumps(summary, sort_keys=True))
        return 0
    except BaseException as exc:
        if terminal_state["written"] and terminal_state["status"] == "COMPLETED":
            # A terminal receipt committed before stdout/handler handoff is
            # authoritative; do not turn a completed one-shot into a retry.
            return 0
        if transition["started"] and not terminal_state["written"] and not isinstance(exc, TerminalReceiptError):
            exit_code = 2 if isinstance(exc, EnumerationError) else 4
            try:
                _write_terminal_receipt(
                    authorization,
                    authorization_sha256,
                    str(transition["marker_sha256"]),
                    terminal_state,
                    status="ABORTED",
                    exit_code=exit_code,
                    failure=_terminal_failure(exc),
                    outputs=None,
                )
            except BaseException as terminal_exc:
                if terminal_state["written"]:
                    # A deferred signal may arrive after the final fsync but
                    # before the helper returns.  The canonical abort receipt
                    # already exists; preserve the original outcome.
                    pass
                else:
                    # A failed terminal write is itself a consumed,
                    # indeterminate state.  Do not retry or replace either
                    # exclusive receipt.
                    raise TerminalReceiptError(
                        "terminal receipt persistence failed after consumption"
                    ) from terminal_exc
        if transition["started"] and not isinstance(exc, Exception):
            # ``main`` deliberately maps unexpected post-consumption exits to
            # 4.  Never let SystemExit/KeyboardInterrupt bypass that map.
            raise PostConsumptionFailure("unexpected post-consumption base exception") from exc
        raise
    finally:
        _restore_signal_guards(previous_signals)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--receipt", required=True)
    parser.add_argument("--execution-root", required=True)
    parser.add_argument("--facts-run1", required=True)
    parser.add_argument("--facts-run2", required=True)
    parser.add_argument("--out", required=True)
    try:
        return run(parser.parse_args())
    except EnumerationError as exc:
        print(f"stage2-enumeration: refusing — {exc}", file=sys.stderr)
        return 2
    except Exception as exc:
        print(f"stage2-enumeration: unexpected failure: {type(exc).__name__}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    raise SystemExit(main())
