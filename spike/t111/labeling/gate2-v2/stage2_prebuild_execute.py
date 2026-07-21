#!/usr/bin/env python3
"""GATE2-V2 Stage-2 P0 — separately reviewed one-shot executor.

This runner is intentionally separate from ``stage2_prebuild.py``.  The
latter is an admission-only parser; this file is the only component allowed
to turn an already committed P0 authorization into a derived root, a fresh
module cache, and two offline fact runs.  It has no default action and is
safe to import only under the isolated/no-site interpreter used at fire time.

The implementation is structured around a small backend seam.  Unit tests
use a recording backend and never run a subprocess, touch a corpus mirror,
create a derived root, or contact a network.  ``LiveBackend`` is used only by
the explicit ``--execute`` interface after all control-plane checks pass.
"""

from __future__ import annotations

import sys as _bootstrap_sys

if not (
    _bootstrap_sys.flags.isolated
    and _bootstrap_sys.flags.no_site
    and _bootstrap_sys.flags.ignore_environment
):
    _bootstrap_sys.stderr.write(
        "stage2-prebuild-execute: refusing — isolated no-site interpreter required\n"
    )
    raise SystemExit(2)

import argparse
import hashlib
import json
import os
import re
import secrets
import signal
import stat
import subprocess
import sys
import threading
from dataclasses import dataclass
from pathlib import Path
from types import ModuleType
from typing import Any, Protocol


sys.dont_write_bytecode = True


HERE = Path(__file__).resolve().parent
T111_ROOT = HERE.parents[1]
REPO_ROOT = T111_ROOT.parents[1]
P0_AUTHORIZATION_PATH = HERE / "stage2-prebuild-authorization.json"
PARSER_PATH = HERE / "stage2_prebuild.py"
EXECUTOR_PATH = Path(__file__).resolve()
EXECUTOR_REVIEW_PATH = HERE / "stage2-prebuild-execute-review-r3.md"
ENUMERATOR_PATH = HERE / "stage2_enumerate.py"
# P0 terminal schema v2 changes the enumeration trust closure.  Do not
# silently fall back to the superseded r5 review when this dependency changes.
ENUMERATOR_REVIEW_PATH = HERE / "stage2-enumerate-review-r6.md"
PLAN_PATH = REPO_ROOT / "PLAN.md"
STAGE0_DIR = HERE / "stage0"
STAGE0_INVENTORY = STAGE0_DIR / "code-path-inventory.tsv"
STAGE1_PATH = HERE / "stage1" / "receipt.json"
STAGE1_RESPONSE_PATH = HERE / "stage1" / "response.json"
STAGE1_RUNNER_PATH = HERE / "stage1_snapshot.py"
PROTOCOL_PATH = T111_ROOT / "labeling" / "GATE2-V2.md"
ESTIMATOR_AUTHORIZATION_PATH = T111_ROOT / "labeling" / "estimator-authorization.json"
ESTIMATOR_AUTHORIZATION_REL = ESTIMATOR_AUTHORIZATION_PATH.relative_to(REPO_ROOT).as_posix()

P0_CONSUMPTION_MARKER_SCHEMA = "t111-gate2-v2-stage2-prebuild-consumption-v1"
P0_EVIDENCE_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-prebuild-evidence-receipt-v1"
P0_TERMINAL_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-prebuild-terminal-receipt-v2"
FACT_RUN_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-fact-run-receipt-v1"
RECEIPT_FIXTURES = ("temporal", "dapr", "loki", "online-boutique")
FAILURE_PREDICATES = {"LOAD_ERRORS", "EXTRACTION_FAILURE"}
FAILURE_STDERR_LIMIT = 4096
GIT_STDOUT_LIMIT = 8192

P0_AUTHORIZATION_REL = P0_AUTHORIZATION_PATH.relative_to(REPO_ROOT).as_posix()
PARSER_REL = PARSER_PATH.relative_to(REPO_ROOT).as_posix()
EXECUTOR_REL = EXECUTOR_PATH.relative_to(REPO_ROOT).as_posix()
EXECUTOR_REVIEW_REL = EXECUTOR_REVIEW_PATH.relative_to(REPO_ROOT).as_posix()
ENUMERATOR_REL = ENUMERATOR_PATH.relative_to(REPO_ROOT).as_posix()
ENUMERATOR_REVIEW_REL = ENUMERATOR_REVIEW_PATH.relative_to(REPO_ROOT).as_posix()
PLAN_REL = PLAN_PATH.relative_to(REPO_ROOT).as_posix()

PLAN_EXECUTOR_MARKER = "GATE2-V2 Stage-2 P0 executor"
PLAN_EXECUTOR_APPROVAL = "ACCEPT"
PLAN_P0_AUTHORIZATION_MARKER = "GATE2-V2 Stage-2 P0 prebuild authorization"
PLAN_P0_AUTHORIZATION_APPROVAL = "AUTHORIZATION: APPROVED"
REVIEW_ACCEPT_VERDICT = "**Verdict: ACCEPT.**"

OID_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
DATE_RE = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}$")
TOOLCHAIN_IDENTITY_RE = re.compile(
    r'^go_version="([^"]+)";go_digest=(sha256:[0-9a-f]{64});'
    r'git_version="([^"]+)";git_digest=(sha256:[0-9a-f]{64})$'
)
COMMAND_STEP_RE = re.compile(r"^[a-z][a-z0-9._-]{0,95}$")
SENSITIVE_KEY_RE = (
    r"[A-Za-z0-9_-]*(?:token|secret|password|passwd|authorization|cookie|"
    r"api[-_]?key|credential|session)[A-Za-z0-9_-]*"
)
URL_USERINFO_RE = re.compile(r"(?i)((?:https?|ssh)://)[^/\s@]+@")
URL_UNTERMINATED_USERINFO_RE = re.compile(
    r"(?i)((?:https?|ssh)://)[^/\s@]*:[^/\s@]*(?=$|[/?\s])"
)
URL_SENSITIVE_QUERY_RE = re.compile(rf"(?i)([?&]{SENSITIVE_KEY_RE}=)[^&#\s]*")
SECRET_ASSIGNMENT_RE = re.compile(
    rf"(?i)\b({SENSITIVE_KEY_RE})\b(\s*[:=]\s*)(?:\"[^\"]*\"|'[^']*'|[^\s,;]+)"
)
JSON_SECRET_RE = re.compile(
    rf'(?i)("{SENSITIVE_KEY_RE}"\s*:\s*)(?:"(?:\\.|[^"\\])*"|[^\s,}}\]]+)'
)
SECRET_HEADER_RE = re.compile(
    r"(?im)^(\s*(?:authorization|proxy-authorization|cookie|set-cookie|"
    r"x[-_]?api[-_]?key|x[-_]?auth[-_]?token)\s*:\s*)[^\n]*"
)
CREDENTIAL_SCHEME_RE = re.compile(r"(?i)\b(bearer|basic|token)\s+[A-Za-z0-9._~+/=-]+")
TOKEN_VALUE_RE = re.compile(r"\b(?:gh[pousr]_|github_pat_|glpat-|glc_|gla_)[A-Za-z0-9_-]+")


class PrebuildExecutionError(Exception):
    """Expected fail-closed refusal; it maps to exit 2 after consumption."""


@dataclass(frozen=True)
class FailureDiagnostic:
    """Fixed-shape, bounded, sanitized stderr for an authorized command."""

    step: str
    exit_code: int
    stderr_sha256: str
    stderr_preview: str
    stderr_truncated: bool

    def record(self) -> dict[str, Any]:
        return {
            "step": self.step,
            "exit_code": self.exit_code,
            "stderr_sha256": self.stderr_sha256,
            "stderr_preview": self.stderr_preview,
            "stderr_truncated": self.stderr_truncated,
        }


class CommandFailure(PrebuildExecutionError):
    """An authorized command refused with a durable safe diagnostic."""

    def __init__(self, diagnostic: FailureDiagnostic):
        super().__init__("authorized command failed")
        self.diagnostic = diagnostic


class MarkerTransitionError(PrebuildExecutionError):
    """M0 may have been created but could not be durably confirmed."""

    def __init__(self, message: str, marker_sha256: str):
        super().__init__(message)
        self.marker_sha256 = marker_sha256


class P0Interrupted(PrebuildExecutionError):
    """A catchable stop signal reached the one-shot while its guard was armed."""

    def __init__(self, signum: int):
        super().__init__("P0 execution was interrupted")
        self.signum = signum


class TerminalPersistenceError(RuntimeError):
    """A consumed P0 could not durably publish its terminal state."""


@dataclass(frozen=True)
class CommandResult:
    exit_code: int
    stdout_sha256: str
    stderr_sha256: str


@dataclass(frozen=True)
class DerivedResult:
    derived_lock_sha256: str
    derived_harness_manifest_sha256: str
    cache_tree_sha256: str


@dataclass(frozen=True)
class FactRunResult:
    run_id: str
    root: str
    receipt_sha256: str
    facts_sha256: dict[str, str]
    capture: dict[str, str]


@dataclass(frozen=True)
class P0Context:
    authorization: dict[str, Any]
    authorization_bytes: bytes
    authorization_sha256: str
    root: Path
    source_repo: Path
    ceremony: Path
    marker: Path
    evidence_receipt: Path
    terminal_receipt: Path
    git: Path
    go: Path
    source_t111: Path
    derived_t111: Path


class ExecutionBackend(Protocol):
    """The narrow mutation/subprocess seam used by the orchestration layer."""

    def consume(self, context: P0Context, transition: dict[str, Any]) -> str: ...
    def clone_source(self, context: P0Context) -> None: ...
    def rewrite_lock(self, context: P0Context) -> None: ...
    def materialize_corpus(self, context: P0Context) -> None: ...
    def prepare_fresh_cache(self, context: P0Context) -> None: ...
    def hydrate(self, context: P0Context) -> dict[str, dict[str, Any]]: ...
    def finalize_fixture_configs(self, context: P0Context) -> None: ...
    def bind_and_seal_cache(self, context: P0Context) -> DerivedResult: ...
    def extract_facts(
        self, context: P0Context, derived: DerivedResult
    ) -> dict[str, FactRunResult]: ...
    def seal_execution_root(self, context: P0Context) -> None: ...
    def publish_evidence(self, context: P0Context, value: dict[str, Any]) -> str: ...
    def publish_terminal(self, context: P0Context, value: dict[str, Any]) -> str: ...


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def sha256_bytes(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _require_sha256(value: Any, label: str) -> str:
    if not isinstance(value, str) or SHA256_RE.fullmatch(value) is None:
        raise PrebuildExecutionError(f"{label} is not a sha256 digest")
    return value


def _require_oid(value: Any, label: str) -> str:
    if not isinstance(value, str) or OID_RE.fullmatch(value) is None:
        raise PrebuildExecutionError(f"{label} is not a full commit oid")
    return value


def _regular_bytes(path: Path, label: str) -> bytes:
    try:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise OSError("not a regular file")
        return path.read_bytes()
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} is unavailable") from exc


def sha256_file(path: Path, label: str = "required input") -> str:
    return sha256_bytes(_regular_bytes(path, label))


def _canonical_json_file(path: Path, label: str) -> tuple[Any, bytes]:
    raw = _regular_bytes(path, label)
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=_no_duplicate_object)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PrebuildExecutionError(f"{label} is not canonical JSON") from exc
    if raw != (canonical_json(value) + "\n").encode("utf-8"):
        raise PrebuildExecutionError(f"{label} is not canonical JSON")
    return value, raw


def _no_duplicate_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON object key")
        result[key] = value
    return result


def _execute_verified_source(name: str, path: Path, source: bytes, label: str) -> ModuleType:
    module = ModuleType(name)
    module.__file__ = str(path)
    module.__package__ = ""
    previous = sys.modules.get(name)
    sys.modules[name] = module
    try:
        exec(compile(source, str(path), "exec", dont_inherit=True), module.__dict__)
    except BaseException as exc:
        if previous is None:
            sys.modules.pop(name, None)
        else:
            sys.modules[name] = previous
        raise PrebuildExecutionError(f"{label} cannot be loaded") from exc
    return module


def _terminate_process_group(process: subprocess.Popen[Any], *, drain: bool = True) -> None:
    """Terminate and reap an entire child process group after a timeout.

    ``subprocess.run(..., timeout=...)`` kills only its immediate child.  The
    authorized tools can themselves start helpers, so every child gets its
    own session and a timeout tears down that whole group before control
    returns to the one-shot.
    """
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except (ProcessLookupError, PermissionError, OSError):
        pass
    try:
        if drain:
            process.communicate(timeout=5)
        else:
            process.wait(timeout=5)
        return
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError, OSError):
        pass
    try:
        if drain:
            process.communicate(timeout=5)
        else:
            process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        # There is no safe continuation if a child group cannot be reaped.
        # The caller still receives its original timeout and refuses P0.
        pass


def _communicate_with_group_timeout(
    process: subprocess.Popen[Any], timeout: float,
) -> tuple[bytes | None, bytes | None]:
    try:
        return process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        _terminate_process_group(process)
        raise
    except P0Interrupted:
        # A guarded post-M0 signal must not strand a child process after the
        # parent pivots to its ABORTED terminal receipt.
        _terminate_process_group(process)
        raise


class _BoundedStderr:
    """Drain stderr while retaining only a fixed prefix and a full digest."""

    def __init__(self, limit: int = FAILURE_STDERR_LIMIT) -> None:
        self._limit = limit
        self._digest = hashlib.sha256()
        self._prefix = bytearray()
        self._total = 0
        self.error: BaseException | None = None

    def add(self, chunk: bytes) -> None:
        self._digest.update(chunk)
        self._total += len(chunk)
        remaining = self._limit - len(self._prefix)
        if remaining > 0:
            self._prefix.extend(chunk[:remaining])

    def diagnostic(self, step: str, exit_code: int) -> FailureDiagnostic:
        if COMMAND_STEP_RE.fullmatch(step) is None:
            raise PrebuildExecutionError("authorized command has an invalid diagnostic step")
        if not isinstance(exit_code, int) or exit_code == 0:
            raise PrebuildExecutionError("authorized command has an invalid diagnostic exit")
        return FailureDiagnostic(
            step=step,
            exit_code=exit_code,
            stderr_sha256="sha256:" + self._digest.hexdigest(),
            stderr_preview=_sanitize_stderr(bytes(self._prefix)),
            stderr_truncated=self._total > self._limit,
        )


class _BoundedOutput:
    """Drain an expected-small command stdout stream without unbounded memory."""

    def __init__(self, limit: int) -> None:
        self._limit = limit
        self._value = bytearray()
        self.truncated = False
        self.error: BaseException | None = None

    def add(self, chunk: bytes) -> None:
        remaining = self._limit - len(self._value)
        if remaining > 0:
            self._value.extend(chunk[:remaining])
        if len(chunk) > remaining:
            self.truncated = True

    def value(self) -> bytes:
        return bytes(self._value)


def _sanitize_stderr(raw: bytes) -> str:
    """Emit printable, credential-safe, newline-normalized bounded text only."""
    text = raw.decode("utf-8", "replace").replace("\r\n", "\n").replace("\r", "\n")
    text = "".join(character if character == "\n" or " " <= character <= "~" else "?" for character in text)
    text = URL_USERINFO_RE.sub(r"\1[REDACTED]@", text)
    # The bounded prefix can end before an ``@``.  Conservatively redact a
    # colon-bearing URL authority at its truncated end rather than retaining a
    # possible user:password fragment.  This also hides a host:port authority,
    # which is harmless and preferable to a credential escape.
    text = URL_UNTERMINATED_USERINFO_RE.sub(r"\1[REDACTED]", text)
    text = URL_SENSITIVE_QUERY_RE.sub(r"\1[REDACTED]", text)
    text = SECRET_HEADER_RE.sub(r"\1[REDACTED]", text)
    text = JSON_SECRET_RE.sub(r'\1"[REDACTED]"', text)
    # Redact a credential scheme before the generic assignment pass.  Were
    # the assignment pass first, ``Authorization=Bearer value`` would redact
    # only ``Bearer`` and leave the credential as a separate word.
    text = CREDENTIAL_SCHEME_RE.sub(lambda match: f"{match.group(1)} [REDACTED]", text)
    text = SECRET_ASSIGNMENT_RE.sub(
        lambda match: f"{match.group(1)}{match.group(2)}[REDACTED]", text,
    )
    return TOKEN_VALUE_RE.sub("[REDACTED]", text).rstrip("\n")


def _timeout_diagnostic(
    capture: _BoundedStderr, step: str, process: subprocess.Popen[Any],
) -> FailureDiagnostic:
    """Use the post-termination signal status for a timed-out command."""
    exit_code = process.returncode
    if not isinstance(exit_code, int) or exit_code == 0:
        exit_code = -getattr(signal, "SIGKILL", 9)
    return capture.diagnostic(step, exit_code)


def _drain_stderr(
    stream: Any, capture: _BoundedStderr, sink_descriptor: int | None = None,
) -> None:
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            if not isinstance(chunk, bytes):
                raise OSError("stderr stream is not bytes")
            capture.add(chunk)
            if sink_descriptor is not None:
                _write_all(sink_descriptor, chunk)
    except BaseException as exc:
        capture.error = exc
    finally:
        try:
            stream.close()
        except OSError:
            pass


def _drain_stdout(stream: Any, capture: _BoundedOutput) -> None:
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            if not isinstance(chunk, bytes):
                raise OSError("stdout stream is not bytes")
            capture.add(chunk)
    except BaseException as exc:
        capture.error = exc
    finally:
        try:
            stream.close()
        except OSError:
            pass


def _wait_with_bounded_stderr(
    process: subprocess.Popen[Any], timeout: float, capture: _BoundedStderr,
    *, sink_descriptor: int | None = None, stdout_capture: _BoundedOutput | None = None,
) -> None:
    if process.stderr is None:
        raise PrebuildExecutionError("authorized command has no stderr capture")
    if stdout_capture is not None and process.stdout is None:
        raise PrebuildExecutionError("authorized command has no stdout capture")
    reader = threading.Thread(
        target=_drain_stderr,
        args=(process.stderr, capture, sink_descriptor),
        daemon=True,
    )
    reader.start()
    stdout_reader: threading.Thread | None = None
    if stdout_capture is not None:
        stdout_reader = threading.Thread(
            target=_drain_stdout,
            args=(process.stdout, stdout_capture),
            daemon=True,
        )
        stdout_reader.start()
    try:
        process.wait(timeout=timeout)
    except (subprocess.TimeoutExpired, P0Interrupted):
        _terminate_process_group(process, drain=False)
        raise
    finally:
        reader.join()
        if stdout_reader is not None:
            stdout_reader.join()
    if capture.error is not None:
        raise PrebuildExecutionError("authorized command stderr cannot be captured") from capture.error
    if stdout_capture is not None:
        if stdout_capture.error is not None:
            raise PrebuildExecutionError("authorized command stdout cannot be captured") from stdout_capture.error
        if stdout_capture.truncated:
            raise PrebuildExecutionError("authorized command stdout exceeds its bound")


def _popen_without_signal_handoff_gap(*args: Any, **kwargs: Any) -> subprocess.Popen[Any]:
    """Spawn only while stop signals are masked, then reap on delivery.

    A Python signal can otherwise interrupt ``Popen`` after the child exists
    but before its object is assigned, leaving a mutating child alive after
    P0 writes an ABORTED terminal receipt.  The mask closes that handoff; a
    deferred signal sees the assigned process and tears down its session.
    """
    process: subprocess.Popen[Any] | None = None
    prior_mask = _block_catchable_signals()
    try:
        try:
            process = subprocess.Popen(*args, **kwargs)
        finally:
            _restore_signal_mask(prior_mask)
        return process
    except P0Interrupted:
        if process is not None:
            _terminate_process_group(process)
        raise


def _control_git(*args: str, git: Path | None = None) -> bytes:
    """Read committed control data with a sterile, explicitly selected Git.

    Bootstrap reads use the platform control Git solely to bind the P0 parser
    before its toolchain can be trusted.  Once P0's Git binary is digest
    verified, every authoritative control read is repeated through that
    sealed binary.  The optional argument keeps those two phases visibly
    separate rather than silently trusting a bootstrap view forever.
    """
    if git is None:
        git = Path("/usr/bin/git")
    _regular_bytes(git, "control Git executable")
    environment = {
        "PATH": "/usr/bin:/bin",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_ASKPASS": "/usr/bin/false",
        "GIT_NO_REPLACE_OBJECTS": "1",
        "GIT_NO_LAZY_FETCH": "1",
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_ALLOW_PROTOCOL": "file",
        "HOME": "/var/empty",
        "TMPDIR": "/var/empty",
    }
    command = [
        str(git), "-C", str(REPO_ROOT),
        "-c", "core.hooksPath=/dev/null",
        "-c", "core.fsmonitor=false",
        "-c", "core.useBuiltinFSMonitor=false",
        *args,
    ]
    try:
        result = _popen_without_signal_handoff_gap(
            command,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            env=environment,
            start_new_session=True,
        )
        stdout, _stderr = _communicate_with_group_timeout(result, 20)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise PrebuildExecutionError("committed control record cannot be read") from exc
    if result.returncode:
        raise PrebuildExecutionError("committed control record cannot be read")
    return stdout or b""


def _head_blob(relative: str, *, git: Path | None = None) -> bytes:
    return _control_git("show", f"HEAD:{relative}", git=git)


def _commit_blob(commit: str, relative: str, *, git: Path | None = None) -> bytes:
    _require_oid(commit, "committed implementation binding")
    return _control_git("show", f"{commit}:{relative}", git=git)


def _require_ancestor(commit: str, label: str, *, git: Path | None = None) -> None:
    _require_oid(commit, label)
    try:
        _control_git("merge-base", "--is-ancestor", commit, "HEAD", git=git)
    except PrebuildExecutionError as exc:
        raise PrebuildExecutionError(f"{label} is not an ancestor of HEAD") from exc


def _head_commit(*, git: Path | None = None) -> str:
    value = _control_git("rev-parse", "HEAD", git=git).decode("ascii", "strict").strip()
    return _require_oid(value, "HEAD")


def _ascii_leaf(path: Path, parent: Path, label: str) -> None:
    if path.parent != parent or not path.name or not path.name.isascii() or any(
        not (character.isalnum() or character in "._-") for character in path.name
    ):
        raise PrebuildExecutionError(f"{label} does not have an ASCII direct state leaf")


def context_from_authorization(authorization: dict[str, Any], raw: bytes) -> P0Context:
    """Turn a parser-validated P0 document into immutable execution paths."""
    try:
        root = Path(authorization["derived_root"]["root"])
        source = Path(authorization["operations"]["source_clone"]["source_repo_path"])
        state = authorization["state"]
        ceremony = Path(state["ceremony_directory"])
        marker = Path(state["consumption_marker"])
        evidence = Path(state["evidence_receipt"])
        terminal = Path(state["terminal_receipt"])
        toolchain = authorization["toolchain"]
        bootstrap = authorization["bootstrap"]
        git = Path(toolchain["git_executable"])
        go = Path(toolchain["go_executable"])
        source_t111 = Path(bootstrap["source_t111_path"])
        derived_t111 = Path(bootstrap["derived_t111_path"])
    except (KeyError, TypeError) as exc:
        raise PrebuildExecutionError("P0 authorization has an unusable execution topology") from exc
    for path, label in (
        (root, "derived root"),
        (source, "source repository"),
        (ceremony, "ceremony directory"),
        (marker, "consumption marker"),
        (evidence, "evidence receipt"),
        (terminal, "terminal receipt"),
    ):
        if not path.is_absolute() or ".." in path.parts:
            raise PrebuildExecutionError(f"{label} is not an absolute safe path")
    if source != REPO_ROOT:
        raise PrebuildExecutionError(
            "source repository differs from the canonical control repository"
        )
    for path, label in ((marker, "consumption marker"), (evidence, "evidence receipt"), (terminal, "terminal receipt")):
        _ascii_leaf(path, ceremony, label)
    if len({marker.name, evidence.name, terminal.name}) != 3:
        raise PrebuildExecutionError("P0 terminal state leaves are not distinct")
    return P0Context(
        authorization=authorization,
        authorization_bytes=raw,
        authorization_sha256=sha256_bytes(raw),
        root=root,
        source_repo=source,
        ceremony=ceremony,
        marker=marker,
        evidence_receipt=evidence,
        terminal_receipt=terminal,
        git=git,
        go=go,
        source_t111=source_t111,
        derived_t111=derived_t111,
    )


def _load_parser_admission() -> tuple[dict[str, Any], bytes, bytes]:
    """Use the P0 parser only to obtain its canonical admission result.

    Do not execute a parser merely because it lives at the expected path.  A
    stdlib-only canonical read first binds the P0 bytes to committed HEAD and
    binds the parser source to the digest carried by those bytes.  Only then
    may the parser confirm the complete P0 grammar.  The parser itself
    performs no subprocess or derived-root I/O, so this bootstrap remains
    entirely pre-consumption.
    """
    preliminary, raw = _canonical_json_file(P0_AUTHORIZATION_PATH, "P0 authorization")
    if not isinstance(preliminary, dict):
        raise PrebuildExecutionError("P0 authorization has an invalid bootstrap envelope")
    if raw != _head_blob(P0_AUTHORIZATION_REL):
        raise PrebuildExecutionError("P0 authorization is not the committed HEAD version")
    implementation = preliminary.get("implementation")
    if not isinstance(implementation, dict):
        raise PrebuildExecutionError("P0 authorization lacks a parser binding")
    parser_digest = _require_sha256(
        implementation.get("prebuild_runner_sha256"), "P0 parser digest"
    )
    source = _regular_bytes(PARSER_PATH, "P0 admission parser")
    if sha256_bytes(source) != parser_digest:
        raise PrebuildExecutionError("P0 admission parser bytes do not match P0")
    if source != _head_blob(PARSER_REL):
        raise PrebuildExecutionError("P0 admission parser is not the committed HEAD version")
    module = _execute_verified_source("_t111_stage2_prebuild_p0", PARSER_PATH, source, "P0 admission parser")
    loader = getattr(module, "load_authorization", None)
    if not callable(loader):
        raise PrebuildExecutionError("P0 admission parser lacks its loader")
    try:
        authorization, raw = loader(P0_AUTHORIZATION_PATH)
    except BaseException as exc:
        raise PrebuildExecutionError("P0 authorization does not pass admission") from exc
    if not isinstance(authorization, dict) or not isinstance(raw, bytes):
        raise PrebuildExecutionError("P0 admission parser returned an invalid result")
    if raw != _regular_bytes(P0_AUTHORIZATION_PATH, "P0 authorization") or authorization != preliminary:
        raise PrebuildExecutionError("P0 admission parser does not agree with committed P0 bytes")
    return authorization, raw, source


def _authorized_executable(path: Path, expected_digest: str, label: str) -> Path:
    _require_sha256(expected_digest, f"{label} digest")
    _regular_bytes(path, label)
    try:
        if not os.access(path, os.X_OK):
            raise OSError("not executable")
        resolved = path.resolve(strict=True)
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} is not executable") from exc
    if sha256_file(resolved, label) != expected_digest:
        raise PrebuildExecutionError(f"{label} does not match P0")
    return resolved


def _regular_executable(path: Path, label: str) -> Path:
    """Require a regular, non-symlink executable without resolving a link."""
    try:
        info = path.lstat()
        if (
            not stat.S_ISREG(info.st_mode)
            or stat.S_ISLNK(info.st_mode)
            or not (stat.S_IMODE(info.st_mode) & 0o111)
        ):
            raise OSError("not a regular executable")
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} is not executable") from exc
    return path


def _review_binding_line(relative: str, digest: str) -> str:
    """The only accepted review spelling for an implementation byte binding."""
    return f"- `{relative}`: `{digest}`"


def _require_accepted_review(
    review_bytes: bytes,
    *,
    label: str,
    bindings: tuple[tuple[str, str], ...],
) -> None:
    """Require one literal verdict and one literal line per sealed artifact.

    A substring test turns ``ACCEPTED``, a quotation, or a counterexample in a
    code block into authority.  The execution gate instead recognizes only
    a unique whole line and a compact, ordered spelling for each independently
    digest-bound source/review pair.
    """
    try:
        text = review_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise PrebuildExecutionError(f"{label} cannot be read") from exc
    if "\r" in text:
        raise PrebuildExecutionError(f"{label} does not use exact LF review lines")
    lines = text.split("\n")
    verdict_lines = [line for line in lines if line.startswith("**Verdict:")]
    if verdict_lines != [REVIEW_ACCEPT_VERDICT]:
        raise PrebuildExecutionError(f"{label} lacks the exact acceptance verdict")
    for relative, digest in bindings:
        expected = _review_binding_line(relative, digest)
        if lines.count(expected) != 1:
            raise PrebuildExecutionError(f"{label} lacks an exact sealed binding")


def _implementation_bindings(
    parser_digest: str,
    executor_digest: str,
    enumerator_digest: str,
    enumerator_review_digest: str,
) -> tuple[tuple[str, str], ...]:
    """Return the fixed implementation envelope in its review/PLAN order."""
    return (
        (PARSER_REL, parser_digest),
        (EXECUTOR_REL, executor_digest),
        (ENUMERATOR_REL, enumerator_digest),
        (ENUMERATOR_REVIEW_REL, enumerator_review_digest),
    )


def _plan_binding_text(bindings: tuple[tuple[str, str], ...]) -> str:
    return ";".join(f"{relative}={digest}" for relative, digest in bindings)


def _strict_plan_row(
    plan_bytes: bytes,
    *,
    marker: str,
    approval: str,
    bindings: str,
    label: str,
) -> str:
    """Return one structural Markdown row, never a substring approximation."""
    try:
        text = plan_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise PrebuildExecutionError(f"{label} PLAN cannot be read") from exc
    if "\r" in text:
        raise PrebuildExecutionError(f"{label} PLAN is not LF-normalized")
    lines = text.split("\n")
    candidates = [line for line in lines if marker in line]
    if len(candidates) != 1:
        raise PrebuildExecutionError(f"{label} lacks a unique {marker} PLAN row")
    line = candidates[0]
    parts = line.split("|")
    if len(parts) != 6 or parts[0] != "" or parts[-1] != "":
        raise PrebuildExecutionError(f"{label} PLAN row is not structurally exact")
    cells: list[str] = []
    for part in parts[1:-1]:
        if (
            len(part) < 2
            or part[0] != " "
            or part[-1] != " "
            or part[1:-1].strip() != part[1:-1]
        ):
            raise PrebuildExecutionError(f"{label} PLAN row is not structurally exact")
        cells.append(part[1:-1])
    if (
        DATE_RE.fullmatch(cells[0]) is None
        or cells[1] != marker
        or cells[2] != approval
        or cells[3] != bindings
    ):
        raise PrebuildExecutionError(f"{label} PLAN row does not carry exact approval bindings")
    return line


def _executor_plan_anchor(
    plan_bytes: bytes,
    *,
    review_digest: str,
    implementation_bindings: tuple[tuple[str, str], ...],
    label: str,
) -> str:
    """Return the unique exact P0 executor acceptance row."""
    return _strict_plan_row(
        plan_bytes,
        marker=PLAN_EXECUTOR_MARKER,
        approval=PLAN_EXECUTOR_APPROVAL,
        bindings=_plan_binding_text(
            (*implementation_bindings, (EXECUTOR_REVIEW_REL, review_digest))
        ),
        label=label,
    )


def _p0_authorization_plan_anchor(
    plan_bytes: bytes, *, authorization_id: str, authorization_sha256: str,
) -> str:
    return _strict_plan_row(
        plan_bytes,
        marker=PLAN_P0_AUTHORIZATION_MARKER,
        approval=PLAN_P0_AUTHORIZATION_APPROVAL,
        bindings=(
            f"authorization_id={authorization_id};"
            f"authorization_sha256={authorization_sha256}"
        ),
        label="committed HEAD",
    )


def verify_implementation_chain(
    context: P0Context,
    *,
    parser_source: bytes,
    executor_source: bytes,
    review_bytes: bytes,
    enumerator_source: bytes,
    enumerator_review_bytes: bytes,
    plan_bytes: bytes,
    head_blob: Any,
    commit_blob: Any,
    require_ancestor: Any,
    head_commit: Any,
) -> None:
    """Close the admission/self-byte/review/PLAN chain before M0.

    ``implementation_binding.commit`` is the accepted implementation-review
    commit.  The P0 document cannot contain its own future Git hash without a
    fixed point, so its separate authority is the *current* HEAD copy plus a
    current-HEAD PLAN approval line carrying its exact canonical digest.  The
    earlier executor acceptance must nevertheless be byte-identical at both
    the accepted implementation commit and current HEAD.
    """
    authorization = context.authorization
    implementation = authorization.get("implementation")
    review = authorization.get("implementation_review")
    binding = authorization.get("implementation_binding")
    if not isinstance(implementation, dict) or not isinstance(review, dict) or not isinstance(binding, dict):
        raise PrebuildExecutionError("P0 authorization lacks implementation bindings")
    parser_digest = _require_sha256(implementation.get("prebuild_runner_sha256"), "P0 parser digest")
    executor_digest = _require_sha256(implementation.get("prebuild_executor_sha256"), "P0 executor digest")
    enumerator_digest = _require_sha256(implementation.get("enumerator_sha256"), "enumerator digest")
    enumerator_review_digest = _require_sha256(implementation.get("enumerator_review_sha256"), "enumerator review digest")
    if sha256_bytes(parser_source) != parser_digest:
        raise PrebuildExecutionError("P0 admission parser bytes do not match P0")
    if sha256_bytes(executor_source) != executor_digest:
        raise PrebuildExecutionError("P0 executor bytes do not match P0")
    if sha256_bytes(enumerator_source) != enumerator_digest:
        raise PrebuildExecutionError("enumerator bytes do not match P0")
    if sha256_bytes(enumerator_review_bytes) != enumerator_review_digest:
        raise PrebuildExecutionError("enumerator review bytes do not match P0")
    implementation_bindings = _implementation_bindings(
        parser_digest,
        executor_digest,
        enumerator_digest,
        enumerator_review_digest,
    )

    review_digest = _require_sha256(review.get("record_sha256"), "implementation review digest")
    accepted = _require_oid(review.get("accepted_commit"), "implementation review commit")
    if (
        review.get("status") != "accepted"
        or binding.get("commit") != accepted
        or binding.get("status") != "executable"
    ):
        raise PrebuildExecutionError("P0 implementation binding does not name its review")
    require_ancestor(accepted, "implementation review commit")

    if sha256_bytes(review_bytes) != review_digest:
        raise PrebuildExecutionError("P0 executor review bytes do not match P0")
    for relative, bytes_now, label in (
        (PARSER_REL, parser_source, "P0 admission parser"),
        (EXECUTOR_REL, executor_source, "P0 executor"),
        (EXECUTOR_REVIEW_REL, review_bytes, "P0 executor review"),
        (ENUMERATOR_REL, enumerator_source, "enumerator"),
        (ENUMERATOR_REVIEW_REL, enumerator_review_bytes, "enumerator review"),
    ):
        if bytes_now != head_blob(relative):
            raise PrebuildExecutionError(f"{label} is not the committed HEAD version")
        if bytes_now != commit_blob(accepted, relative):
            raise PrebuildExecutionError(f"accepted implementation commit lacks {label} bytes")
    if plan_bytes != head_blob(PLAN_REL):
        raise PrebuildExecutionError("PLAN is not the committed HEAD version")
    _require_accepted_review(
        review_bytes,
        label="accepted executor review",
        bindings=implementation_bindings,
    )
    _require_accepted_review(
        enumerator_review_bytes,
        label="accepted enumerator review",
        bindings=((ENUMERATOR_REL, enumerator_digest),),
    )
    accepted_anchor = _executor_plan_anchor(
        commit_blob(accepted, PLAN_REL),
        review_digest=review_digest,
        implementation_bindings=implementation_bindings,
        label="accepted implementation commit",
    )
    head_anchor = _executor_plan_anchor(
        plan_bytes,
        review_digest=review_digest,
        implementation_bindings=implementation_bindings,
        label="committed HEAD",
    )
    if head_anchor != accepted_anchor:
        raise PrebuildExecutionError(
            "committed HEAD executor PLAN anchor differs from the accepted implementation commit"
        )
    if context.authorization_bytes != head_blob(P0_AUTHORIZATION_REL):
        raise PrebuildExecutionError("P0 authorization is not the committed HEAD version")
    authorization_id = authorization.get("authorization_id")
    if not isinstance(authorization_id, str) or not authorization_id:
        raise PrebuildExecutionError("P0 authorization has an invalid identifier")
    _p0_authorization_plan_anchor(
        plan_bytes,
        authorization_id=authorization_id,
        authorization_sha256=context.authorization_sha256,
    )
    current_head = head_commit()  # reject a malformed/unreadable HEAD first.
    if accepted == current_head:
        raise PrebuildExecutionError(
            "implementation review commit must strictly predate the P0 authorization HEAD"
        )


def _verify_committed_implementation(
    context: P0Context, parser_source: bytes, *, control_git: Path,
) -> None:
    """Repeat all control reads through P0's digest-verified Git binary."""
    verify_implementation_chain(
        context,
        parser_source=parser_source,
        executor_source=_regular_bytes(EXECUTOR_PATH, "P0 executor"),
        review_bytes=_regular_bytes(EXECUTOR_REVIEW_PATH, "P0 executor review"),
        enumerator_source=_regular_bytes(ENUMERATOR_PATH, "enumerator"),
        enumerator_review_bytes=_regular_bytes(ENUMERATOR_REVIEW_PATH, "enumerator review"),
        plan_bytes=_regular_bytes(PLAN_PATH, "PLAN"),
        head_blob=lambda relative: _head_blob(relative, git=control_git),
        commit_blob=lambda commit, relative: _commit_blob(
            commit, relative, git=control_git
        ),
        require_ancestor=lambda commit, label: _require_ancestor(
            commit, label, git=control_git
        ),
        head_commit=lambda: _head_commit(git=control_git),
    )


def _validate_stage1(context: P0Context) -> None:
    """Re-verify the admitted receipt through its sealed runner loader."""
    inputs = context.authorization["inputs"]
    runner = _regular_bytes(STAGE1_RUNNER_PATH, "Stage-1 snapshot runner")
    if sha256_bytes(runner) != inputs["stage1_snapshot_sha256"]:
        raise PrebuildExecutionError("Stage-1 runner differs from P0")
    module = _execute_verified_source("_t111_stage1_p0", STAGE1_RUNNER_PATH, runner, "Stage-1 snapshot runner")
    try:
        query_bytes, constants = module.load_sealed_inputs()
    except BaseException as exc:
        raise PrebuildExecutionError("sealed Stage-1 inputs cannot be loaded") from exc
    receipt, receipt_bytes = _canonical_json_file(STAGE1_PATH, "Stage-1 receipt")
    if sha256_bytes(receipt_bytes) != inputs["stage1_receipt_sha256"]:
        raise PrebuildExecutionError("Stage-1 receipt differs from P0")
    if (
        not isinstance(receipt, dict)
        or receipt.get("schema") != getattr(module, "RECEIPT_SCHEMA", None)
        or receipt.get("status") != "ADMITTED"
        or receipt.get("cutoff") != constants.get("proposed_cutoff")
        or receipt.get("query_sha256") != module.sha256_bytes(query_bytes)
        or receipt.get("response_sha256") != inputs["stage1_response_sha256"]
    ):
        raise PrebuildExecutionError("Stage-1 receipt is not the admitted sealed receipt")
    if sha256_file(STAGE1_RESPONSE_PATH, "Stage-1 response") != receipt["response_sha256"]:
        raise PrebuildExecutionError("Stage-1 response differs from its receipt")
    heads = receipt.get("heads")
    if not isinstance(heads, dict) or set(heads) != set(RECEIPT_FIXTURES):
        raise PrebuildExecutionError("Stage-1 receipt has an invalid fixture set")
    for fixture in RECEIPT_FIXTURES:
        head = heads[fixture]
        oid = head.get("head_oid") if isinstance(head, dict) else None
        if oid != inputs["heads"].get(fixture) or not isinstance(oid, str) or not OID_RE.fullmatch(oid):
            raise PrebuildExecutionError("Stage-1 receipt heads differ from P0")


def _load_manifest(path: Path, label: str) -> tuple[dict[str, Any], str]:
    raw = _regular_bytes(path, label)
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=_no_duplicate_object)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PrebuildExecutionError(f"{label} is invalid") from exc
    if not isinstance(value, dict):
        raise PrebuildExecutionError(f"{label} is invalid")
    return value, sha256_bytes(raw)


def _verify_harness_source(context: P0Context) -> None:
    inputs = context.authorization["inputs"]
    source = context.source_repo
    manifest_path = source / "spike" / "t111" / ".module-cache" / "manifest.json"
    manifest, digest = _load_manifest(manifest_path, "Stage-0 harness manifest")
    if digest != inputs["stage0_harness_manifest_sha256"]:
        raise PrebuildExecutionError("Stage-0 harness manifest differs from P0")
    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, dict):
        raise PrebuildExecutionError("Stage-0 harness manifest lacks artifacts")
    for name, expected in (("t111", inputs["t111_binary_sha256"]), ("typedcalloracle", inputs["typedcalloracle_binary_sha256"])):
        artifact = artifacts.get(name)
        if not isinstance(artifact, dict) or artifact.get("path") != f"bin/{name}" or artifact.get("sha256") != expected:
            raise PrebuildExecutionError("Stage-0 harness artifact differs from P0")
        if sha256_file(manifest_path.parent / artifact["path"], f"Stage-0 {name}") != expected:
            raise PrebuildExecutionError("Stage-0 harness artifact bytes differ from P0")
    _verify_source_t111(context)


def _verify_source_t111(context: P0Context) -> None:
    """Bind the bootstrap binary immediately before and after hydration."""
    _regular_executable(context.source_t111, "source bootstrap t111")
    if sha256_file(context.source_t111, "source bootstrap t111") != context.authorization["inputs"]["t111_binary_sha256"]:
        raise PrebuildExecutionError("source bootstrap t111 differs from P0")


def _verify_toolchain(context: P0Context) -> Path:
    toolchain = context.authorization["toolchain"]
    python = _authorized_executable(Path(toolchain["python_executable"]), toolchain["python_sha256"], "authorized Python")
    running = Path(sys.executable)
    try:
        if running.resolve(strict=True) != python.resolve(strict=True):
            raise PrebuildExecutionError("running Python differs from P0")
    except OSError as exc:
        raise PrebuildExecutionError("running Python cannot be verified") from exc
    if ".".join(str(part) for part in sys.version_info[:3]) != toolchain["python_version"]:
        raise PrebuildExecutionError("running Python version differs from P0")
    git = _authorized_executable(context.git, toolchain["git_sha256"], "authorized Git")
    _authorized_executable(context.go, toolchain["go_sha256"], "authorized Go")
    identity = TOOLCHAIN_IDENTITY_RE.fullmatch(toolchain["producer_toolchain_identity"])
    if identity is None or identity.group(2) != toolchain["go_sha256"] or identity.group(4) != toolchain["git_sha256"]:
        raise PrebuildExecutionError("P0 toolchain identity is inconsistent")
    return git


def _safe_local_git_environment(phase: dict[str, Any]) -> dict[str, str]:
    variables = phase.get("variables")
    if not isinstance(variables, dict) or not all(isinstance(key, str) and isinstance(value, str) for key, value in variables.items()):
        raise PrebuildExecutionError("P0 phase environment is invalid")
    result = dict(variables)
    result["GOPROXY"] = str(phase["proxy"])
    result["GOSUMDB"] = str(phase["sumdb"])
    # The parser binds this too; repeating it here makes the capability
    # boundary obvious at the call site and prevents ambient protocol use.
    result["GIT_ALLOW_PROTOCOL"] = "file"
    return result


def _git_quiet(
    git: Path,
    argv: list[str],
    *,
    cwd: Path,
    env: dict[str, str],
    diagnostic_step: str | None = None,
) -> bytes:
    """Run the digest-verified Git binary, with diagnostics only post-M0."""
    capture = _BoundedStderr() if diagnostic_step is not None else None
    stdout_capture = _BoundedOutput(GIT_STDOUT_LIMIT) if capture is not None else None
    try:
        result = _popen_without_signal_handoff_gap(
            argv,
            cwd=str(cwd),
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE if capture is not None else subprocess.DEVNULL,
            start_new_session=True,
        )
    except OSError as exc:
        raise PrebuildExecutionError("authorized Git operation failed") from exc
    try:
        if capture is None:
            stdout, _stderr = _communicate_with_group_timeout(result, 120)
        else:
            _wait_with_bounded_stderr(
                result,
                120,
                capture,
                stdout_capture=stdout_capture,
            )
            if stdout_capture is None:
                raise PrebuildExecutionError("authorized Git operation lacks stdout capture")
            stdout = stdout_capture.value()
    except subprocess.TimeoutExpired as exc:
        if capture is not None and diagnostic_step is not None:
            raise CommandFailure(_timeout_diagnostic(capture, diagnostic_step, result)) from exc
        raise PrebuildExecutionError("authorized Git operation failed") from exc
    if result.returncode:
        if capture is not None and diagnostic_step is not None:
            raise CommandFailure(capture.diagnostic(diagnostic_step, result.returncode))
        raise PrebuildExecutionError("authorized Git operation failed")
    return stdout or b""


def _verify_source_repositories(context: P0Context) -> None:
    """Reject linkages/missing objects before the irreversible M0 transition."""
    source = context.source_repo
    _require_real_directory(source, "source repository")
    environment = _safe_local_git_environment(context.authorization["environment"]["offline"])
    source_commit = _require_oid(
        context.authorization["operations"]["source_clone"]["source_commit"],
        "source clone commit",
    )
    implementation = context.authorization.get("implementation_binding")
    if not isinstance(implementation, dict) or source_commit != implementation.get("commit"):
        raise PrebuildExecutionError("source clone commit does not match the implementation binding")
    _git_quiet(context.git, [str(context.git), "-C", str(source), "cat-file", "-e", f"{source_commit}^{{commit}}"], cwd=source, env=environment)
    _git_quiet(context.git, [str(context.git), "-C", str(source), "merge-base", "--is-ancestor", source_commit, "HEAD"], cwd=source, env=environment)
    for command in ("diff", "--quiet"), ("diff", "--cached", "--quiet"):
        _git_quiet(context.git, [str(context.git), "-C", str(source), *command], cwd=source, env=environment)
    _verify_git_metadata(source, context.root, "source repository")
    corpus = context.authorization["operations"]["corpus"]["fixtures"]
    for fixture in RECEIPT_FIXTURES:
        plan = corpus[fixture]
        mirror = Path(plan["source_repo_path"])
        _require_real_directory(mirror, "source corpus mirror")
        _verify_git_metadata(mirror, context.root, "source corpus mirror")
        shallow = _git_quiet(
            context.git,
            [str(context.git), "-C", str(mirror), "rev-parse", "--is-shallow-repository"],
            cwd=mirror,
            env=environment,
        )
        if shallow != b"false\n":
            raise PrebuildExecutionError("source corpus mirror is shallow")
        history = plan.get("source_history")
        if not isinstance(history, dict):
            raise PrebuildExecutionError("source corpus history is unavailable")
        old_commit = _require_oid(history.get("old_commit"), "source corpus old commit")
        sealed_commit = _require_oid(history.get("sealed_commit"), "source corpus sealed commit")
        if sealed_commit != plan["source_commit"] or history.get("old_commit_is_ancestor") is not True:
            raise PrebuildExecutionError("source corpus history differs from P0")
        _git_quiet(
            context.git,
            [str(context.git), "-C", str(mirror), "cat-file", "-e", f"{old_commit}^{{commit}}"],
            cwd=mirror,
            env=environment,
        )
        _git_quiet(
            context.git,
            [str(context.git), "-C", str(mirror), "cat-file", "-e", f"{sealed_commit}^{{commit}}"],
            cwd=mirror,
            env=environment,
        )
        _git_quiet(
            context.git,
            [str(context.git), "-C", str(mirror), "merge-base", "--is-ancestor", old_commit, sealed_commit],
            cwd=mirror,
            env=environment,
        )
        source_ref = plan.get("source_ref")
        if not isinstance(source_ref, str):
            raise PrebuildExecutionError("source corpus ref is unavailable")
        ref_value = _git_quiet(
            context.git,
            [
                str(context.git), "-C", str(mirror), "for-each-ref",
                "--format=%(objectname)", "--count=2", source_ref,
            ],
            cwd=mirror,
            env=environment,
        )
        if ref_value not in (b"", f"{sealed_commit}\n".encode("ascii")):
            raise PrebuildExecutionError("source corpus ref conflicts with P0")
        for command in (("diff", "--quiet"), ("diff", "--cached", "--quiet")):
            _git_quiet(context.git, [str(context.git), "-C", str(mirror), *command], cwd=mirror, env=environment)


def _require_real_directory(path: Path, label: str) -> None:
    try:
        info = path.lstat()
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise OSError("not a real directory")
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} is unavailable") from exc


def _verify_git_metadata(repo: Path, forbidden_root: Path, label: str) -> None:
    git_dir = repo / ".git"
    _require_real_directory(git_dir, f"{label} metadata")
    for relative in ("commondir", "gitdir", "objects/info/alternates"):
        candidate = git_dir / relative
        try:
            candidate.lstat()
        except FileNotFoundError:
            continue
        except OSError as exc:
            raise PrebuildExecutionError(f"{label} metadata cannot be inspected") from exc
        raise PrebuildExecutionError(f"{label} has external Git linkage")
    try:
        repo.relative_to(forbidden_root)
    except ValueError:
        return
    raise PrebuildExecutionError(f"{label} overlaps the future derived root")


def _stage0_inventory_lock_digest(raw: bytes) -> str:
    """Read the one Stage-0 inventory row that seals the base corpus lock."""
    try:
        lines = raw.decode("utf-8").splitlines()
    except UnicodeDecodeError as exc:
        raise PrebuildExecutionError("Stage-0 inventory cannot be read") from exc
    rows: dict[str, str] = {}
    for line in lines:
        if not line or line.startswith("#"):
            continue
        fields = line.split("\t")
        if len(fields) != 2 or not fields[0] or fields[0] in rows:
            raise PrebuildExecutionError("Stage-0 inventory has an invalid row")
        rows[fields[0]] = _require_sha256(fields[1], "Stage-0 inventory digest")
    try:
        return rows["spike/t111/corpus.lock.json"]
    except KeyError as exc:
        raise PrebuildExecutionError("Stage-0 inventory lacks the base corpus lock") from exc


def _verify_stage0_inventory_lock_binding(
    inventory_raw: bytes, base_lock_raw: bytes, inputs: dict[str, Any],
) -> None:
    """Cross-bind the two independently sealed Stage-0 lock declarations."""
    inventory_digest = sha256_bytes(inventory_raw)
    base_digest = sha256_bytes(base_lock_raw)
    if inventory_digest != inputs.get("stage0_inventory_sha256"):
        raise PrebuildExecutionError("Stage-0 inventory differs from P0")
    if base_digest != inputs.get("base_lock_sha256"):
        raise PrebuildExecutionError("base corpus lock differs from P0")
    if _stage0_inventory_lock_digest(inventory_raw) != base_digest:
        raise PrebuildExecutionError("base corpus lock does not match the Stage-0 inventory")


def _verify_sealed_inputs(context: P0Context) -> None:
    """Rehash all P0 inputs and validate real source topology before M0."""
    inputs = context.authorization["inputs"]
    inventory_raw = _regular_bytes(STAGE0_INVENTORY, "Stage-0 inventory")
    base_lock_raw = _regular_bytes(
        context.source_repo / "spike" / "t111" / "corpus.lock.json", "base corpus lock"
    )
    _verify_stage0_inventory_lock_binding(inventory_raw, base_lock_raw, inputs)
    expected_files = {
        "stage0_harness_manifest_sha256": context.source_repo / "spike" / "t111" / ".module-cache" / "manifest.json",
        "protocol_sha256": PROTOCOL_PATH,
        "estimator_authorization_sha256": ESTIMATOR_AUTHORIZATION_PATH,
        "typedcalloracle_binary_sha256": context.source_repo / "spike" / "t111" / ".module-cache" / "bin" / "typedcalloracle",
    }
    for field, path in expected_files.items():
        if sha256_file(path, field) != inputs[field]:
            raise PrebuildExecutionError("sealed P0 input differs from its authorization")
    _validate_stage1(context)
    _verify_harness_source(context)
    _verify_source_repositories(context)


def _paths_overlap(first: Path, second: Path) -> bool:
    """Compare normalized, lexical absolute P0 reservations without I/O."""
    try:
        first.relative_to(second)
        return True
    except ValueError:
        try:
            second.relative_to(first)
            return True
        except ValueError:
            return False


def _canonical_reserved_namespace(path: Path, label: str) -> Path:
    """Canonicalize a historical reservation even after it was removed.

    A state directory is a durable reservation, not evidence that its result
    remains on disk.  Existing path components must be real (not links); an
    absent suffix is appended to the canonical last real parent.
    """
    if not path.is_absolute() or path.name in {"", ".", ".."} or ".." in path.parts:
        raise PrebuildExecutionError(f"{label} has an invalid path")
    current = Path(path.anchor)
    parts = path.parts[1:]
    for index, part in enumerate(parts):
        candidate = current / part
        try:
            info = candidate.lstat()
        except FileNotFoundError:
            try:
                canonical_parent = current.resolve(strict=True)
            except OSError as exc:
                raise PrebuildExecutionError(f"{label} parent cannot be resolved") from exc
            return canonical_parent.joinpath(*parts[index:])
        except OSError as exc:
            raise PrebuildExecutionError(f"{label} cannot be inspected") from exc
        if stat.S_ISLNK(info.st_mode):
            raise PrebuildExecutionError(f"{label} contains a symlink")
        if index < len(parts) - 1 and not stat.S_ISDIR(info.st_mode):
            raise PrebuildExecutionError(f"{label} has a non-directory path component")
        current = candidate
    try:
        return current.resolve(strict=True)
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} cannot be resolved") from exc


def _prior_ceremony_namespace(path: Path, label: str) -> Path | None:
    """Return the one namespace directly below a ``ceremony`` path part."""
    if not path.is_absolute() or ".." in path.parts:
        raise PrebuildExecutionError(f"{label} has an invalid absolute path")
    try:
        index = path.parts.index("ceremony")
    except ValueError:
        return None
    if index + 1 >= len(path.parts):
        raise PrebuildExecutionError(f"{label} names a ceremony root without a namespace")
    return Path(*path.parts[: index + 2])


def _ceremony_paths_in(value: Any, label: str) -> set[Path]:
    """Find every historical ceremony namespace in a sealed JSON value."""
    result: set[Path] = set()
    if isinstance(value, dict):
        for child in value.values():
            result.update(_ceremony_paths_in(child, label))
    elif isinstance(value, list):
        for child in value:
            result.update(_ceremony_paths_in(child, label))
    elif isinstance(value, str) and Path(value).is_absolute():
        if not value or "\x00" in value or "\n" in value or "\r" in value:
            raise PrebuildExecutionError(f"{label} has an invalid absolute path")
        namespace = _prior_ceremony_namespace(Path(value), label)
        if namespace is not None:
            result.add(namespace)
    return result


def _prior_ceremony_namespaces(value: Any) -> tuple[str, set[Path]]:
    """Read fixed historical ceremony reservations, never their outcomes."""
    if (
        not isinstance(value, dict)
        or value.get("schema") != "t111-estimator-authorization-v1"
        or not isinstance(value.get("authorization_id"), str)
        or not value["authorization_id"]
        or not isinstance(value.get("state"), dict)
        or set(value["state"]) != {
            "admission_receipt", "consumption_marker", "result_receipt"
        }
    ):
        raise PrebuildExecutionError("prior estimator authorization has an invalid ceremony registry")
    raw_namespaces = _ceremony_paths_in(value, "prior estimator authorization")
    if not raw_namespaces:
        raise PrebuildExecutionError("prior estimator authorization has no ceremony reservation")
    namespaces = {
        _canonical_reserved_namespace(path, "prior estimator ceremony namespace")
        for path in raw_namespaces
    }
    return value["authorization_id"], namespaces


def _verify_prior_ceremony_registry(
    context: P0Context, *, head_blob: Any | None = None,
) -> None:
    """Reserve historical state namespaces before P0 creates any new state."""
    if head_blob is None:
        head_blob = _head_blob
    binding = context.authorization.get("prior_authorizations")
    if (
        not isinstance(binding, dict)
        or set(binding) != {"estimator"}
        or not isinstance(binding.get("estimator"), dict)
        or set(binding["estimator"]) != {"path", "sha256"}
        or binding["estimator"].get("path") != ESTIMATOR_AUTHORIZATION_REL
        or binding["estimator"].get("sha256")
        != context.authorization["inputs"].get("estimator_authorization_sha256")
    ):
        raise PrebuildExecutionError("P0 prior ceremony binding is invalid")
    value, raw = _canonical_json_file(
        ESTIMATOR_AUTHORIZATION_PATH, "prior estimator authorization"
    )
    if sha256_bytes(raw) != context.authorization["inputs"]["estimator_authorization_sha256"]:
        raise PrebuildExecutionError("prior estimator authorization differs from P0")
    if raw != head_blob(ESTIMATOR_AUTHORIZATION_REL):
        raise PrebuildExecutionError("prior estimator authorization is not the committed HEAD version")
    prior_id, prior_ceremonies = _prior_ceremony_namespaces(value)
    if context.authorization["authorization_id"] == prior_id:
        raise PrebuildExecutionError("P0 authorization reuses a prior authorization identifier")
    canonical_ceremony = _canonical_reserved_namespace(
        context.ceremony, "P0 ceremony directory"
    )
    candidates = (
        (_canonical_reserved_namespace(context.root, "P0 derived root"), "derived root"),
        (canonical_ceremony, "ceremony directory"),
        (canonical_ceremony / context.marker.name, "consumption marker"),
        (canonical_ceremony / context.evidence_receipt.name, "evidence receipt"),
        (canonical_ceremony / context.terminal_receipt.name, "terminal receipt"),
    )
    for prior_ceremony in prior_ceremonies:
        for candidate, label in candidates:
            if _paths_overlap(candidate, prior_ceremony):
                raise PrebuildExecutionError(f"P0 {label} overlaps a sealed prior ceremony")


def _require_private_parent(path: Path, label: str) -> None:
    parent = path.parent
    try:
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise OSError("not a directory")
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise OSError("not owner-private")
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} parent is not owner-private") from exc


def _assert_absent(path: Path, label: str) -> None:
    try:
        path.lstat()
    except FileNotFoundError:
        return
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} cannot be inspected") from exc
    raise PrebuildExecutionError(f"{label} already exists")


def _verify_fresh_namespaces(context: P0Context) -> None:
    _require_private_parent(context.root, "derived root")
    _require_private_parent(context.ceremony, "ceremony directory")
    _assert_absent(context.root, "derived root")
    _assert_absent(context.ceremony, "ceremony directory")
    for path, label in ((context.marker, "consumption marker"), (context.evidence_receipt, "evidence receipt"), (context.terminal_receipt, "terminal receipt")):
        _assert_absent(path, label)


def _require_clean_control_worktree(*, control_git: Path) -> None:
    """Reject tracked or indexed control changes before interpreting P0."""
    for arguments in (("diff", "--quiet"), ("diff", "--cached", "--quiet")):
        try:
            _control_git(*arguments, git=control_git)
        except PrebuildExecutionError as exc:
            raise PrebuildExecutionError("control worktree or index is not clean") from exc


def admit_live() -> P0Context:
    """Complete every non-mutating admission/control check before M0."""
    authorization, raw, parser_source = _load_parser_admission()
    context = context_from_authorization(authorization, raw)
    control_git = _verify_toolchain(context)
    # The bootstrap Git was necessary only to bind the parser.  Its HEAD must
    # agree with the sealed Git's HEAD, and every subsequent control decision
    # is re-read through the sealed binary below.
    if _head_commit() != _head_commit(git=control_git):
        raise PrebuildExecutionError("bootstrap and P0 Git disagree about control HEAD")
    _require_clean_control_worktree(control_git=control_git)
    _verify_committed_implementation(context, parser_source, control_git=control_git)
    _verify_sealed_inputs(context)
    _verify_prior_ceremony_registry(
        context, head_blob=lambda relative: _head_blob(relative, git=control_git)
    )
    _verify_fresh_namespaces(context)
    return context


def _write_all(fd: int, data: bytes) -> None:
    offset = 0
    while offset < len(data):
        count = os.write(fd, data[offset:])
        if count <= 0:
            raise OSError("short write")
        offset += count


def _no_follow_flag() -> int:
    value = getattr(os, "O_NOFOLLOW", None)
    if not isinstance(value, int) or value == 0:
        raise PrebuildExecutionError("platform lacks O_NOFOLLOW")
    return value


def _fsync_directory(path: Path, label: str) -> None:
    try:
        fd = os.open(path, os.O_RDONLY | _no_follow_flag())
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} cannot be fsynced") from exc
    try:
        os.fsync(fd)
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} cannot be fsynced") from exc
    finally:
        os.close(fd)


def _catchable_signals() -> set[int]:
    return {
        getattr(signal, name)
        for name in (
            "SIGINT", "SIGTERM", "SIGHUP", "SIGQUIT", "SIGALRM", "SIGUSR1",
            "SIGUSR2", "SIGXCPU", "SIGXFSZ",
        )
        if hasattr(signal, name)
    }


def _require_signal_transition_support() -> None:
    """Refuse before M0 unless O_EXCL/signal handoff is atomic on this host."""
    blocker = getattr(signal, "pthread_sigmask", None)
    sig_block = getattr(signal, "SIG_BLOCK", None)
    sig_setmask = getattr(signal, "SIG_SETMASK", None)
    pending = getattr(signal, "sigpending", None)
    if not callable(blocker) or sig_block is None or sig_setmask is None or not callable(pending):
        raise PrebuildExecutionError("P0 requires pthread signal-mask support")
    try:
        previous = set(blocker(sig_block, set()))
        pending()
        blocker(sig_setmask, previous)
    except (OSError, RuntimeError, ValueError, TypeError) as exc:
        raise PrebuildExecutionError("P0 signal transition support is unavailable") from exc


def _block_catchable_signals() -> set[int]:
    blocker = getattr(signal, "pthread_sigmask", None)
    sig_block = getattr(signal, "SIG_BLOCK", None)
    if not callable(blocker) or sig_block is None:
        raise PrebuildExecutionError("P0 signal transition support is unavailable")
    try:
        return set(blocker(sig_block, _catchable_signals()))
    except (OSError, RuntimeError, ValueError, TypeError) as exc:
        raise PrebuildExecutionError("P0 signal transition support is unavailable") from exc


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
    if not callable(pending):
        return None
    try:
        caught = set(pending()) & _catchable_signals()
    except (OSError, RuntimeError, ValueError):
        return None
    return min(caught) if caught else None


def _install_signal_guards(transition: dict[str, Any]) -> dict[int, Any]:
    """Raise before M0, then latch catchable stops until a terminal outcome.

    Once ``O_EXCL`` has crossed the M0 boundary, a Python exception delivered
    at an arbitrary bytecode boundary can skip the code that publishes T0.
    Latching post-M0 stops instead makes the state machine observe them at
    explicit checkpoints; the next checkpoint writes exactly one ABORTED
    terminal receipt.  Before M0 the same signal remains an ordinary refusal.
    """
    previous: dict[int, Any] = {}
    catchable = _catchable_signals()

    def interrupted(signum: int, _frame: Any) -> None:
        if transition.get("started") is True:
            transition["latched_signal"] = signum
            return
        raise P0Interrupted(signum)

    try:
        for signum in catchable:
            try:
                previous[signum] = signal.getsignal(signum)
                signal.signal(signum, interrupted)
            except (OSError, RuntimeError, ValueError):
                pass
        if set(previous) != catchable:
            raise PrebuildExecutionError("P0 signal guards are unavailable")
    except BaseException:
        _restore_signal_guards(previous)
        raise
    return previous


def _restore_signal_guards(previous: dict[int, Any]) -> None:
    for signum, handler in previous.items():
        try:
            signal.signal(signum, handler)
        except (OSError, RuntimeError, ValueError):
            pass


def _raise_latched_signal(transition: dict[str, Any]) -> None:
    """Pivot at a controlled post-M0 checkpoint after a latched stop."""
    signum = transition.get("latched_signal")
    if isinstance(signum, int) and signum in _catchable_signals():
        raise P0Interrupted(signum)


def _write_exclusive_durable(
    path: Path, data: bytes, label: str, *, transition: dict[str, Any] | None = None,
) -> str:
    """Create M0 with O_EXCL and report indeterminate persistence distinctly."""
    created = False
    digest = sha256_bytes(data)
    prior_mask = _block_catchable_signals()
    try:
        pending = _pending_catchable_signal()
        if pending is not None:
            raise P0Interrupted(pending)
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag(), 0o600)
        created = True
        if transition is not None:
            # This is deliberately before the write/fsync.  Once O_EXCL
            # succeeds, deferred signal delivery must reach the post-M0
            # terminal path even if the marker persistence becomes uncertain.
            transition["started"] = True
            transition["marker_sha256"] = digest
        try:
            _write_all(fd, data)
            os.fsync(fd)
        finally:
            os.close(fd)
        _fsync_directory(path.parent, label)
        return digest
    except FileExistsError as exc:
        raise PrebuildExecutionError(f"{label} already exists") from exc
    except OSError as exc:
        if created:
            raise MarkerTransitionError(f"{label} persistence is indeterminate", digest) from exc
        raise PrebuildExecutionError(f"{label} cannot be created") from exc
    finally:
        _restore_signal_mask(prior_mask)


def _publish_once_durable(path: Path, data: bytes, label: str) -> str:
    """Publish R0/T0 with temp+fsync+exclusive hard-link, never rename-overwrite."""
    temporary = path.parent / f".{path.name}.{secrets.token_hex(16)}.tmp"
    try:
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag(), 0o600)
        try:
            _write_all(fd, data)
            os.fsync(fd)
        finally:
            os.close(fd)
        # link(2) atomically fails if the destination exists, unlike rename.
        os.link(temporary, path, follow_symlinks=False)
        _fsync_directory(path.parent, label)
        os.unlink(temporary)
        _fsync_directory(path.parent, label)
    except FileExistsError as exc:
        raise TerminalPersistenceError(f"{label} already exists") from exc
    except OSError as exc:
        raise TerminalPersistenceError(f"{label} cannot be published") from exc
    return sha256_bytes(data)


def _delete_tree(path: Path, root: Path) -> None:
    """Delete only an owned derived child; never follow a symlink."""
    try:
        path.relative_to(root)
    except ValueError as exc:
        raise PrebuildExecutionError("attempted deletion escapes the derived root") from exc
    if path == root:
        raise PrebuildExecutionError("attempted deletion targets the derived root")
    try:
        info = path.lstat()
    except FileNotFoundError:
        return
    except OSError as exc:
        raise PrebuildExecutionError("derived path cannot be inspected") from exc
    if stat.S_ISLNK(info.st_mode) or stat.S_ISREG(info.st_mode):
        os.unlink(path)
        return
    if not stat.S_ISDIR(info.st_mode):
        raise PrebuildExecutionError("derived path has an unsafe entry")
    try:
        names = list(os.listdir(path))
    except OSError as exc:
        raise PrebuildExecutionError("derived path cannot be traversed") from exc
    for name in names:
        _delete_tree(path / name, root)
    try:
        os.rmdir(path)
    except OSError as exc:
        raise PrebuildExecutionError("derived path cannot be removed") from exc


def _mkdir_private(path: Path) -> None:
    try:
        path.mkdir(mode=0o700)
    except OSError as exc:
        raise PrebuildExecutionError("private execution directory cannot be created") from exc


def _mkdir_private_below(path: Path, anchor: Path) -> None:
    """Create every missing child at 0700 without following an ancestor link."""
    try:
        relative = path.relative_to(anchor)
    except ValueError as exc:
        raise PrebuildExecutionError("private execution directory escapes its anchor") from exc
    _require_real_directory(anchor, "private execution anchor")
    current = anchor
    for part in relative.parts:
        current = current / part
        try:
            info = current.lstat()
        except FileNotFoundError:
            _mkdir_private(current)
            continue
        except OSError as exc:
            raise PrebuildExecutionError("private execution directory cannot be inspected") from exc
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise PrebuildExecutionError("private execution directory has an unsafe ancestor")


def _clear_and_mkdir(path: Path, root: Path) -> None:
    _delete_tree(path, root)
    _mkdir_private_below(path, root)


def _overwrite_regular_durable(path: Path, data: bytes, label: str) -> None:
    try:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise OSError("not a regular file")
        fd = os.open(path, os.O_WRONLY | os.O_TRUNC | _no_follow_flag())
        try:
            _write_all(fd, data)
            os.fsync(fd)
        finally:
            os.close(fd)
        _fsync_directory(path.parent, label)
    except OSError as exc:
        raise PrebuildExecutionError(f"{label} cannot be written") from exc


def _copy_regular_no_hardlink(source: Path, destination: Path) -> None:
    try:
        source_info = source.lstat()
        if (
            not stat.S_ISREG(source_info.st_mode)
            or stat.S_ISLNK(source_info.st_mode)
            or source_info.st_nlink != 1
        ):
            raise OSError("source is not a regular file")
        source_fd = os.open(source, os.O_RDONLY | _no_follow_flag())
        destination_fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag(), 0o600)
        try:
            while True:
                block = os.read(source_fd, 1024 * 1024)
                if not block:
                    break
                _write_all(destination_fd, block)
            os.fsync(destination_fd)
        finally:
            os.close(source_fd)
            os.close(destination_fd)
        destination_info = destination.lstat()
        if (
            not stat.S_ISREG(destination_info.st_mode)
            or stat.S_ISLNK(destination_info.st_mode)
            or destination_info.st_nlink != 1
        ):
            raise OSError("destination is not a regular file")
        if (source_info.st_dev, source_info.st_ino) == (destination_info.st_dev, destination_info.st_ino):
            raise OSError("hardlink detected")
    except OSError as exc:
        raise PrebuildExecutionError("fact publication cannot make an independent regular copy") from exc


def _validate_fact_jsonl(path: Path) -> None:
    """Reject malformed or producer-failure fact output before publication."""
    _regular_bytes(path, "published fact file")
    try:
        descriptor = os.open(path, os.O_RDONLY | _no_follow_flag())
    except OSError as exc:
        raise PrebuildExecutionError("published fact file cannot be read") from exc
    try:
        with os.fdopen(descriptor, "rb", closefd=False) as handle:
            for raw_line in handle:
                if not raw_line.strip():
                    raise PrebuildExecutionError("published fact file contains an empty record")
                try:
                    record = json.loads(raw_line.decode("utf-8"))
                except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                    raise PrebuildExecutionError("published fact file is not valid JSONL") from exc
                if not isinstance(record, dict):
                    raise PrebuildExecutionError("published fact file contains an invalid record")
                if record.get("predicate") in FAILURE_PREDICATES:
                    raise PrebuildExecutionError("published fact file records a producer failure")
    finally:
        os.close(descriptor)


def _make_tree_readonly(
    path: Path, *, reject_symlinks: bool, reject_hardlinks: bool = False,
) -> None:
    """Remove every write bit without following a link outside the root.

    The fresh module cache has a stronger invariant than the derived tree as
    a whole: every regular cache entry must be a private inode.  Check it
    before chmod so an external hard-link target is never modified as a side
    effect of sealing the cache.
    """
    try:
        root_info = path.lstat()
        if not stat.S_ISDIR(root_info.st_mode) or stat.S_ISLNK(root_info.st_mode):
            raise OSError("not directory")
        for directory, children, files in os.walk(path, topdown=False, followlinks=False):
            current = Path(directory)
            for name in [*files, *children]:
                item = current / name
                info = item.lstat()
                if stat.S_ISLNK(info.st_mode):
                    if reject_symlinks:
                        raise OSError("symlink")
                    continue
                if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
                    raise OSError("unsafe file type")
                if reject_hardlinks and stat.S_ISREG(info.st_mode) and info.st_nlink != 1:
                    raise OSError("hardlink")
                os.chmod(item, stat.S_IMODE(info.st_mode) & ~0o222, follow_symlinks=False)
            info = current.lstat()
            os.chmod(current, stat.S_IMODE(info.st_mode) & ~0o222, follow_symlinks=False)
    except OSError as exc:
        raise PrebuildExecutionError("execution tree cannot be sealed read-only") from exc


def _cache_tree_digest(path: Path) -> str:
    try:
        root_info = path.lstat()
        if not stat.S_ISDIR(root_info.st_mode) or stat.S_ISLNK(root_info.st_mode) or stat.S_IMODE(root_info.st_mode) & 0o222:
            raise OSError("unsafe root")
        items = sorted(path.rglob("*"), key=lambda item: item.relative_to(path).as_posix())
    except OSError as exc:
        raise PrebuildExecutionError("derived cache cannot be sealed") from exc
    digest = hashlib.sha256()
    for item in items:
        try:
            info = item.lstat()
            relative = item.relative_to(path).as_posix()
        except (OSError, ValueError) as exc:
            raise PrebuildExecutionError("derived cache cannot be sealed") from exc
        if (
            stat.S_ISLNK(info.st_mode)
            or not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode))
            or stat.S_IMODE(info.st_mode) & 0o222
            or (stat.S_ISREG(info.st_mode) and info.st_nlink != 1)
        ):
            raise PrebuildExecutionError("derived cache has an unsafe entry")
        digest.update((b"d" if stat.S_ISDIR(info.st_mode) else b"f") + b"\0")
        digest.update(relative.encode("utf-8") + b"\0")
        digest.update(f"{stat.S_IMODE(info.st_mode):04o}".encode("ascii") + b"\0")
        if stat.S_ISREG(info.st_mode):
            digest.update(sha256_file(item, "derived cache entry").encode("ascii") + b"\0")
    return "sha256:" + digest.hexdigest()


INERT_GIT_CONFIG = b"[core]\n\trepositoryformatversion = 0\n\tsymlinks = false\n"


def _verify_inert_git_repository(
    repo: Path, forbidden_root: Path, label: str,
) -> None:
    """Require a direct, core-only config before a derived repo is used."""
    git_dir = repo / ".git"
    _require_real_directory(git_dir, f"{label} metadata")
    if _regular_bytes(git_dir / "config", f"{label} config") != INERT_GIT_CONFIG:
        raise PrebuildExecutionError(f"{label} does not have the inert core-only config")
    for path, item_label in (
        (git_dir / "hooks", "hooks"),
        (git_dir / "objects" / "info" / "alternates", "alternates"),
    ):
        try:
            path.lstat()
        except FileNotFoundError:
            continue
        except OSError as exc:
            raise PrebuildExecutionError(f"{label} {item_label} cannot be inspected") from exc
        raise PrebuildExecutionError(f"{label} retains {item_label}")
    _verify_git_metadata(repo, forbidden_root, label)


def _install_inert_git_repository(
    repo: Path, execution_root: Path, forbidden_root: Path, label: str,
) -> None:
    """Erase init/clone controls before any fetch, checkout, or hydration."""
    git_dir = repo / ".git"
    _require_real_directory(git_dir, f"{label} metadata")
    _require_real_directory(git_dir / "objects", f"{label} object directory")
    _require_real_directory(git_dir / "objects" / "info", f"{label} object-info directory")
    _delete_tree(git_dir / "hooks", execution_root)
    _delete_tree(git_dir / "objects" / "info" / "alternates", execution_root)
    _overwrite_regular_durable(
        git_dir / "config", INERT_GIT_CONFIG, f"{label} inert Git config"
    )
    _verify_inert_git_repository(repo, forbidden_root, label)


class LiveBackend:
    """Production backend.  It is never constructed by the synthetic tests."""

    def __init__(self) -> None:
        # A P0 phase owns one scratch namespace for its whole lifetime.  The
        # cache prevents a later operation from treating that mandatory
        # directory as stale state, while the authorization digest keeps a
        # backend instance from ever sharing it across ceremonies.
        self._phase_environments: dict[tuple[str, str], dict[str, str]] = {}

    def _phase_environment(self, context: P0Context, phase: str) -> dict[str, str]:
        key = (context.authorization_sha256, phase)
        cached = self._phase_environments.get(key)
        if cached is not None:
            return dict(cached)
        phase_value = context.authorization["environment"][phase]
        scratch = Path(phase_value["scratch_root"])
        if scratch.exists():
            raise PrebuildExecutionError("phase scratch state already exists")
        _mkdir_private_below(scratch, context.ceremony)
        for child in ("tmp", "home", "xdg-config", "xdg-cache", "xdg-data"):
            _mkdir_private(scratch / child)
        environment = _safe_local_git_environment(phase_value)
        self._phase_environments[key] = dict(environment)
        return environment

    def _run_quiet(
        self, argv: list[str], *, cwd: Path, environment: dict[str, str], step: str,
    ) -> None:
        capture = _BoundedStderr()
        try:
            result = _popen_without_signal_handoff_gap(
                argv,
                cwd=str(cwd),
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                start_new_session=True,
            )
        except OSError as exc:
            raise PrebuildExecutionError("authorized command could not run") from exc
        try:
            _wait_with_bounded_stderr(result, 30 * 60, capture)
        except subprocess.TimeoutExpired as exc:
            raise CommandFailure(_timeout_diagnostic(capture, step, result)) from exc
        except OSError as exc:
            raise PrebuildExecutionError("authorized command could not run") from exc
        if result.returncode:
            raise CommandFailure(capture.diagnostic(step, result.returncode))

    def _run_capture_many(
        self,
        argv_values: list[list[str]],
        *,
        steps: list[str],
        cwd: Path,
        environment: dict[str, str],
        stdout_path: Path,
        stderr_path: Path,
        private_anchor: Path,
    ) -> None:
        try:
            _mkdir_private_below(stdout_path.parent, private_anchor)
            if stdout_path.parent.is_symlink() or stderr_path.parent != stdout_path.parent:
                raise OSError("unsafe capture parent")
            stdout_fd = os.open(stdout_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag(), 0o600)
            stderr_fd = os.open(stderr_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | _no_follow_flag(), 0o600)
        except OSError as exc:
            raise PrebuildExecutionError("command capture cannot be created") from exc
        if len(argv_values) != len(steps):
            raise PrebuildExecutionError("authorized command capture has an invalid step plan")
        failure: FailureDiagnostic | None = None
        try:
            for argv, step in zip(argv_values, steps):
                capture = _BoundedStderr()
                try:
                    result = _popen_without_signal_handoff_gap(
                        argv,
                        cwd=str(cwd),
                        env=environment,
                        stdin=subprocess.DEVNULL,
                        stdout=stdout_fd,
                        stderr=subprocess.PIPE,
                        start_new_session=True,
                    )
                except OSError as exc:
                    raise PrebuildExecutionError("authorized command could not run") from exc
                try:
                    _wait_with_bounded_stderr(
                        result,
                        30 * 60,
                        capture,
                        sink_descriptor=stderr_fd,
                    )
                except subprocess.TimeoutExpired:
                    failure = _timeout_diagnostic(capture, step, result)
                    break
                except OSError as exc:
                    raise PrebuildExecutionError("authorized command could not run") from exc
                if result.returncode:
                    failure = capture.diagnostic(step, result.returncode)
                    break
            os.fsync(stdout_fd)
            os.fsync(stderr_fd)
        finally:
            os.close(stdout_fd)
            os.close(stderr_fd)
        _fsync_directory(stdout_path.parent, "command capture")
        if failure is not None:
            raise CommandFailure(failure)

    def _normalize_repo(self, context: P0Context, repo: Path, _environment: dict[str, str]) -> None:
        _install_inert_git_repository(
            repo, context.root, context.source_repo, "derived repository"
        )

    def _verify_checkout(
        self,
        context: P0Context,
        repo: Path,
        expected: str,
        environment: dict[str, str],
        *,
        diagnostic_step: str,
    ) -> None:
        head = _git_quiet(
            context.git,
            [str(context.git), "-C", str(repo), "rev-parse", "HEAD"],
            cwd=repo,
            env=environment,
            diagnostic_step=f"{diagnostic_step}.head",
        ).decode("ascii", "strict").strip()
        if head != expected:
            raise PrebuildExecutionError("derived repository HEAD differs from P0")
        _git_quiet(
            context.git,
            [str(context.git), "-C", str(repo), "diff", "--quiet"],
            cwd=repo,
            env=environment,
            diagnostic_step=f"{diagnostic_step}.worktree",
        )
        _git_quiet(
            context.git,
            [str(context.git), "-C", str(repo), "diff", "--cached", "--quiet"],
            cwd=repo,
            env=environment,
            diagnostic_step=f"{diagnostic_step}.index",
        )
        _verify_inert_git_repository(repo, context.source_repo, "derived repository")

    def consume(self, context: P0Context, transition: dict[str, Any]) -> str:
        _mkdir_private(context.ceremony)
        # Persist the namespace creation before the marker's O_EXCL edge.
        # Otherwise a power failure could leave an ambiguous parent/marker
        # ordering despite a fully fsynced marker file.
        _fsync_directory(context.ceremony.parent, "ceremony parent")
        marker = {
            "schema": P0_CONSUMPTION_MARKER_SCHEMA,
            "status": "CONSUMED",
            "authorization_id": context.authorization["authorization_id"],
            "authorization_sha256": context.authorization_sha256,
        }
        return _write_exclusive_durable(
            context.marker,
            (canonical_json(marker) + "\n").encode("utf-8"),
            "consumption marker",
            transition=transition,
        )

    def clone_source(self, context: P0Context) -> None:
        plan = context.authorization["operations"]["source_clone"]
        environment = self._phase_environment(context, "hydration")
        self._run_quiet(
            plan["clone_argv"],
            cwd=context.source_repo.parent,
            environment=environment,
            step="source.clone",
        )
        self._normalize_repo(context, context.root, environment)
        self._run_quiet(
            plan["checkout_argv"],
            cwd=context.root,
            environment=environment,
            step="source.checkout",
        )
        self._verify_checkout(
            context,
            context.root,
            plan["source_commit"],
            environment,
            diagnostic_step="source.checkout",
        )

    def rewrite_lock(self, context: P0Context) -> None:
        plan = context.authorization["operations"]["lock_rewrite"]
        source = Path(plan["base_lock_path"])
        destination = Path(plan["derived_lock_path"])
        raw = _regular_bytes(source, "base corpus lock")
        if sha256_bytes(raw) != plan["base_lock_sha256"]:
            raise PrebuildExecutionError("base corpus lock differs from P0")
        try:
            rows = json.loads(raw.decode("utf-8"), object_pairs_hook=_no_duplicate_object)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise PrebuildExecutionError("base corpus lock is invalid") from exc
        if not isinstance(rows, list):
            raise PrebuildExecutionError("base corpus lock is invalid")
        names: set[str] = set()
        replacements = plan["transform"]["fixtures"]
        seen: set[str] = set()
        for row in rows:
            if not isinstance(row, dict) or not isinstance(row.get("name"), str) or row["name"] in names:
                raise PrebuildExecutionError("base corpus lock is invalid")
            names.add(row["name"])
            replacement = replacements.get(row["name"])
            if replacement is not None:
                if row.get("commit") != replacement["source_commit"]:
                    raise PrebuildExecutionError("base corpus lock source commit differs from P0")
                row["commit"] = replacement["target_commit"]
                seen.add(row["name"])
        if seen != set(RECEIPT_FIXTURES):
            raise PrebuildExecutionError("base corpus lock lacks a P0 fixture")
        payload = (json.dumps(rows, indent=2, ensure_ascii=False, sort_keys=False) + "\n").encode("utf-8")
        if sha256_bytes(payload) != plan["derived_lock_sha256"]:
            raise PrebuildExecutionError("derived corpus lock does not match P0")
        _overwrite_regular_durable(destination, payload, "derived corpus lock")
        if sha256_file(destination, "derived corpus lock") != plan["derived_lock_sha256"]:
            raise PrebuildExecutionError("derived corpus lock cannot be sealed")

    def materialize_corpus(self, context: P0Context) -> None:
        plan = context.authorization["operations"]["corpus"]
        # The first offline Git command must not inherit a declared but
        # uncreated TMPDIR/HOME/XDG tree.  This initializes the exact P0
        # scratch namespace before bundle/init/fetch/checkout starts; later
        # extraction receives the cached same-phase environment.
        environment = self._phase_environment(context, "offline")
        bundles = context.ceremony / "bundles"
        _mkdir_private_below(bundles, context.ceremony)
        for fixture in RECEIPT_FIXTURES:
            item = plan["fixtures"][fixture]
            source_ref = item["source_ref"]
            expected_head = item["source_commit"]
            ref_output = _git_quiet(
                context.git,
                [
                    str(context.git), "-C", item["source_repo_path"], "for-each-ref",
                    "--format=%(objectname)", "--count=2", source_ref,
                ],
                cwd=Path(item["source_repo_path"]),
                env=environment,
                diagnostic_step=f"corpus.{fixture}.ref.verify",
            ).decode("ascii", "strict")
            if ref_output:
                if ref_output != f"{expected_head}\n":
                    raise PrebuildExecutionError("source bundle ref differs from P0")
            else:
                self._run_quiet(
                    item["update_ref_argv"],
                    cwd=context.source_repo,
                    environment=environment,
                    step=f"corpus.{fixture}.ref",
                )
            self._run_quiet(
                item["bundle_argv"],
                cwd=context.source_repo,
                environment=environment,
                step=f"corpus.{fixture}.bundle",
            )
            destination = Path(item["destination_path"])
            _clear_and_mkdir(destination, context.root)
            # init requires the target to be absent; reset created its parent only.
            try:
                destination.rmdir()
            except OSError as exc:
                raise PrebuildExecutionError("derived corpus destination cannot be reset") from exc
            self._run_quiet(
                item["init_argv"],
                cwd=context.root,
                environment=environment,
                step=f"corpus.{fixture}.init",
            )
            self._normalize_repo(context, destination, environment)
            self._run_quiet(
                item["fetch_argv"],
                cwd=destination,
                environment=environment,
                step=f"corpus.{fixture}.fetch",
            )
            self._run_quiet(
                item["checkout_argv"],
                cwd=destination,
                environment=environment,
                step=f"corpus.{fixture}.checkout",
            )
            self._verify_checkout(
                context,
                destination,
                item["source_commit"],
                environment,
                diagnostic_step=f"corpus.{fixture}.checkout",
            )

    def prepare_fresh_cache(self, context: P0Context) -> None:
        cache = Path(context.authorization["derived_root"]["cache_path"])
        _clear_and_mkdir(cache, context.root)

    def hydrate(self, context: P0Context) -> dict[str, dict[str, Any]]:
        plan = context.authorization["operations"]["hydrate"]
        environment = self._phase_environment(context, "hydration")
        _verify_source_t111(context)
        result: dict[str, dict[str, Any]] = {}
        for fixture in RECEIPT_FIXTURES:
            _verify_source_t111(context)
            command = plan["commands"][fixture]
            capture = command["capture"]
            self._run_capture_many(
                [command["argv"]],
                steps=[f"hydrate.{fixture}"],
                cwd=context.root,
                environment=environment,
                stdout_path=Path(capture["stdout_path"]),
                stderr_path=Path(capture["stderr_path"]),
                private_anchor=context.ceremony,
            )
            _verify_source_t111(context)
            result[fixture] = {
                "exit_code": 0,
                "stdout_sha256": sha256_file(Path(capture["stdout_path"]), "hydration stdout"),
                "stderr_sha256": sha256_file(Path(capture["stderr_path"]), "hydration stderr"),
            }
        return result

    def finalize_fixture_configs(self, context: P0Context) -> None:
        plan = context.authorization["operations"]["fixture_finalize"]
        for fixture in RECEIPT_FIXTURES:
            item = plan["fixtures"][fixture]
            repo = Path(item["repo_path"])
            if item["core_values"] != {
                "repositoryformatversion": "0", "symlinks": "false"
            }:
                raise PrebuildExecutionError("P0 fixture config differs from the inert core-only policy")
            if Path(item["config_path"]) != repo / ".git" / "config":
                raise PrebuildExecutionError("P0 fixture config path differs from the inert core-only policy")
            _verify_inert_git_repository(repo, context.source_repo, "derived corpus fixture")

    def bind_and_seal_cache(self, context: P0Context) -> DerivedResult:
        cache = Path(context.authorization["derived_root"]["cache_path"])
        inputs = context.authorization["inputs"]
        if sha256_file(context.derived_t111, "derived t111") != inputs["t111_binary_sha256"]:
            raise PrebuildExecutionError("derived t111 differs from P0")
        oracle = cache / "bin" / "typedcalloracle"
        if sha256_file(oracle, "derived typed call oracle") != inputs["typedcalloracle_binary_sha256"]:
            raise PrebuildExecutionError("derived typed call oracle differs from P0")
        base, _base_digest = _load_manifest(context.source_repo / "spike" / "t111" / ".module-cache" / "manifest.json", "Stage-0 harness manifest")
        derived, derived_digest = _load_manifest(cache / "manifest.json", "derived harness manifest")
        for field in ("sources", "go_version", "go_sha256", "goos", "goarch"):
            if derived.get(field) != base.get(field):
                raise PrebuildExecutionError("derived harness changes the sealed producer")
        if derived.get("go_sha256") != context.authorization["toolchain"]["go_sha256"]:
            raise PrebuildExecutionError("derived harness Go identity differs from P0")
        artifacts = derived.get("artifacts")
        if not isinstance(artifacts, dict):
            raise PrebuildExecutionError("derived harness lacks artifacts")
        for name, expected in (("t111", inputs["t111_binary_sha256"]), ("typedcalloracle", inputs["typedcalloracle_binary_sha256"])):
            item = artifacts.get(name)
            if not isinstance(item, dict) or item.get("path") != f"bin/{name}" or item.get("sha256") != expected:
                raise PrebuildExecutionError("derived harness artifact differs from P0")
        _make_tree_readonly(cache, reject_symlinks=True, reject_hardlinks=True)
        cache_digest = _cache_tree_digest(cache)
        lock_digest = sha256_file(Path(context.authorization["derived_root"]["lock_path"]), "derived corpus lock")
        if lock_digest != inputs["derived_lock_sha256"]:
            raise PrebuildExecutionError("derived corpus lock changed after hydration")
        return DerivedResult(lock_digest, derived_digest, cache_digest)

    def extract_facts(self, context: P0Context, derived: DerivedResult) -> dict[str, FactRunResult]:
        environment = self._phase_environment(context, "offline")
        out_path = Path(context.authorization["derived_root"]["out_path"])
        extract_plan = context.authorization["operations"]["extract"]
        results: dict[str, FactRunResult] = {}
        for run in ("run1", "run2"):
            planned = context.authorization["fact_runs"][run]
            publication = planned["publication"]
            run_root = Path(planned["path"])
            _clear_and_mkdir(out_path, context.root)
            _clear_and_mkdir(run_root, context.root)
            capture = extract_plan[run]["capture"]
            commands = [extract_plan[run]["argv"][fixture] for fixture in RECEIPT_FIXTURES]
            self._run_capture_many(
                commands,
                steps=[f"extract.{run}.{fixture}" for fixture in RECEIPT_FIXTURES],
                cwd=context.root,
                environment=environment,
                stdout_path=Path(capture["stdout_path"]),
                stderr_path=Path(capture["stderr_path"]),
                private_anchor=run_root,
            )
            facts: dict[str, str] = {}
            for fixture in RECEIPT_FIXTURES:
                item = publication["files"][fixture]
                source = Path(item["source_path"])
                destination = Path(item["destination_path"])
                _copy_regular_no_hardlink(source, destination)
                _validate_fact_jsonl(destination)
                facts[fixture] = sha256_file(destination, "published fact file")
            receipt = {
                "schema": FACT_RUN_RECEIPT_SCHEMA,
                "status": "COMPLETE",
                "run_id": planned["run_id"],
                "execution_mode": "offline",
                "commands": {
                    fixture: {"subcommand": "extract", "system": fixture, "exit_code": 0}
                    for fixture in RECEIPT_FIXTURES
                },
                "derived_lock_sha256": derived.derived_lock_sha256,
                "cache_tree_sha256": derived.cache_tree_sha256,
                "t111_binary_sha256": context.authorization["inputs"]["t111_binary_sha256"],
                "typedcalloracle_binary_sha256": context.authorization["inputs"]["typedcalloracle_binary_sha256"],
                "git_sha256": context.authorization["toolchain"]["git_sha256"],
                "go_sha256": context.authorization["toolchain"]["go_sha256"],
                "producer_toolchain_identity": context.authorization["toolchain"]["producer_toolchain_identity"],
                "heads": context.authorization["inputs"]["heads"],
                "facts_sha256": facts,
            }
            receipt_sha256 = _publish_once_durable(Path(planned["receipt_path"]), (canonical_json(receipt) + "\n").encode("utf-8"), "fact-run receipt")
            results[run] = FactRunResult(
                run_id=planned["run_id"],
                root=str(run_root),
                receipt_sha256=receipt_sha256,
                facts_sha256=facts,
                capture={
                    "stdout_sha256": sha256_file(Path(capture["stdout_path"]), "extraction stdout"),
                    "stderr_sha256": sha256_file(Path(capture["stderr_path"]), "extraction stderr"),
                },
            )
        if results["run1"].facts_sha256 != results["run2"].facts_sha256:
            raise PrebuildExecutionError("offline fact runs are not byte-identical")
        return results

    def seal_execution_root(self, context: P0Context) -> None:
        _make_tree_readonly(context.root, reject_symlinks=True)

    def publish_evidence(self, context: P0Context, value: dict[str, Any]) -> str:
        return _publish_once_durable(context.evidence_receipt, (canonical_json(value) + "\n").encode("utf-8"), "P0 evidence receipt")

    def publish_terminal(self, context: P0Context, value: dict[str, Any]) -> str:
        return _publish_once_durable(context.terminal_receipt, (canonical_json(value) + "\n").encode("utf-8"), "P0 terminal receipt")


def _evidence_record(
    context: P0Context,
    marker_sha256: str,
    hydration: dict[str, dict[str, Any]],
    derived: DerivedResult,
    facts: dict[str, FactRunResult],
) -> dict[str, Any]:
    return {
        "schema": P0_EVIDENCE_RECEIPT_SCHEMA,
        "status": "COMPLETE",
        "authorization_id": context.authorization["authorization_id"],
        "authorization_sha256": context.authorization_sha256,
        "consumption_marker_sha256": marker_sha256,
        "implementation": context.authorization["implementation"],
        "inputs": context.authorization["inputs"],
        "toolchain": context.authorization["toolchain"],
        "environment": context.authorization["environment"],
        "derived": {
            "execution_root": str(context.root),
            "derived_lock_sha256": derived.derived_lock_sha256,
            "derived_harness_manifest_sha256": derived.derived_harness_manifest_sha256,
            "cache_tree_sha256": derived.cache_tree_sha256,
            "corpus_heads": context.authorization["inputs"]["heads"],
        },
        "hydration": hydration,
        "fact_runs": {
            run: {
                "run_id": facts[run].run_id,
                "root": facts[run].root,
                "receipt_sha256": facts[run].receipt_sha256,
                "facts_sha256": facts[run].facts_sha256,
                "capture": facts[run].capture,
            }
            for run in ("run1", "run2")
        },
    }


def _terminal_record(
    context: P0Context,
    marker_sha256: str,
    *,
    status: str,
    exit_code: int,
    failure: str | None,
    failure_diagnostic: FailureDiagnostic | None,
    evidence_sha256: str | None,
) -> dict[str, Any]:
    if status == "COMPLETED":
        if (
            exit_code != 0
            or failure is not None
            or failure_diagnostic is not None
            or evidence_sha256 is None
        ):
            raise TerminalPersistenceError("invalid completed P0 terminal state")
    elif status == "ABORTED":
        if exit_code not in {2, 4} or failure is None or evidence_sha256 is not None:
            raise TerminalPersistenceError("invalid aborted P0 terminal state")
    else:
        raise TerminalPersistenceError("invalid P0 terminal state")
    return {
        "schema": P0_TERMINAL_RECEIPT_SCHEMA,
        "status": status,
        "authorization_sha256": context.authorization_sha256,
        "consumption_marker_sha256": marker_sha256,
        "exit_code": exit_code,
        "failure": failure,
        "failure_diagnostic": (
            None if failure_diagnostic is None else failure_diagnostic.record()
        ),
        "evidence_receipt_sha256": evidence_sha256,
    }


@dataclass(frozen=True)
class ExecutionOutcome:
    exit_code: int
    status: str
    marker_sha256: str | None
    evidence_sha256: str | None
    terminal_sha256: str | None


def _terminal_outcome(
    marker_sha256: str, terminal_state: dict[str, Any],
) -> ExecutionOutcome:
    """Return the already-durable terminal result after deferred delivery."""
    if (
        terminal_state.get("written") is not True
        or not isinstance(terminal_state.get("terminal_sha256"), str)
        or terminal_state.get("status") not in {"COMPLETED", "ABORTED"}
        or terminal_state.get("exit_code") not in {0, 2, 4}
    ):
        raise TerminalPersistenceError("P0 terminal state is indeterminate")
    completed = terminal_state["status"] == "COMPLETED"
    return ExecutionOutcome(
        terminal_state["exit_code"],
        terminal_state["status"],
        marker_sha256,
        terminal_state.get("evidence_sha256") if completed else None,
        terminal_state["terminal_sha256"],
    )


def _publish_terminal_guarded(
    backend: ExecutionBackend,
    context: P0Context,
    value: dict[str, Any],
    terminal_state: dict[str, Any],
) -> str:
    """Mask stop signals until T0 is linked, fsynced, and remembered locally."""
    prior_mask = _block_catchable_signals()
    try:
        terminal_sha256 = backend.publish_terminal(context, value)
        terminal_state.update(
            {
                "written": True,
                "status": value["status"],
                "exit_code": value["exit_code"],
                "evidence_sha256": value["evidence_receipt_sha256"],
                "terminal_sha256": terminal_sha256,
            }
        )
        return terminal_sha256
    finally:
        # A signal deferred here can raise only after the terminal result is
        # known durable in memory, so the caller never attempts a replacement.
        _restore_signal_mask(prior_mask)


def _abort_after_consumption(
    context: P0Context,
    backend: ExecutionBackend,
    marker_sha256: str,
    terminal_state: dict[str, Any],
    exc: BaseException,
) -> ExecutionOutcome:
    # A second catchable signal must not land in the narrow period after M0
    # but before T0's own guarded publisher has installed its mask.  Build
    # the terminal payload and enter that publisher under one outer mask.
    prior_mask = _block_catchable_signals()
    outcome: ExecutionOutcome | None = None
    try:
        if isinstance(exc, MarkerTransitionError):
            exit_code, failure = 4, "MARKER_PERSISTENCE"
        elif isinstance(exc, P0Interrupted):
            exit_code, failure = 4, "INTERRUPTED"
        elif isinstance(exc, PrebuildExecutionError):
            exit_code, failure = 2, "REFUSED"
        else:
            exit_code, failure = 4, "UNEXPECTED"
        failure_diagnostic = exc.diagnostic if isinstance(exc, CommandFailure) else None
        try:
            _publish_terminal_guarded(
                backend,
                context,
                _terminal_record(
                    context,
                    marker_sha256,
                    status="ABORTED",
                    exit_code=exit_code,
                    failure=failure,
                    failure_diagnostic=failure_diagnostic,
                    evidence_sha256=None,
                ),
                terminal_state,
            )
        except BaseException as terminal_exc:
            if terminal_state.get("written") is True:
                outcome = _terminal_outcome(marker_sha256, terminal_state)
            else:
                raise TerminalPersistenceError(
                    "consumed P0 could not publish an aborted terminal receipt"
                ) from terminal_exc
        else:
            outcome = _terminal_outcome(marker_sha256, terminal_state)
    finally:
        try:
            _restore_signal_mask(prior_mask)
        except P0Interrupted:
            # T0 is already durable.  A deferred second stop cannot change
            # the completed abort result or cause a replacement terminal.
            if terminal_state.get("written") is not True:
                raise
    if outcome is None:
        raise TerminalPersistenceError("consumed P0 terminal outcome is indeterminate")
    return outcome


def _abort_until_terminal(
    context: P0Context,
    backend: ExecutionBackend,
    marker_sha256: str,
    terminal_state: dict[str, Any],
    exc: BaseException,
) -> ExecutionOutcome:
    """Keep arming the post-M0 abort transition until T0 is authoritative.

    A signal can interrupt the first instruction that masks signals in
    :func:`_abort_after_consumption`.  That is already after M0, so treating
    the interrupt as an ordinary top-level failure would strand consumption
    without a terminal receipt.  Retry only that narrow arming race; a signal
    storm can delay T0 but can never turn it into an unrecorded outcome.
    """
    current_exc = exc
    while True:
        try:
            return _abort_after_consumption(
                context, backend, marker_sha256, terminal_state, current_exc
            )
        except P0Interrupted as interrupted:
            if terminal_state.get("written") is True:
                return _terminal_outcome(marker_sha256, terminal_state)
            current_exc = interrupted


def _terminalize_after_m0(
    context: P0Context,
    backend: ExecutionBackend,
    marker_sha256: str,
    terminal_state: dict[str, Any],
    exc: BaseException,
) -> ExecutionOutcome:
    """Defensively retry the call boundary into the post-M0 terminalizer.

    Real post-M0 OS signals are latched by the guard above rather than raised.
    This second loop covers a synthetic/internal ``P0Interrupted`` delivered
    exactly as the terminalizer is called, so that invariant is preserved even
    if a future helper reintroduces a raising stop path.
    """
    current_exc = exc
    while True:
        try:
            return _abort_until_terminal(
                context, backend, marker_sha256, terminal_state, current_exc
            )
        except P0Interrupted as interrupted:
            if terminal_state.get("written") is True:
                return _terminal_outcome(marker_sha256, terminal_state)
            current_exc = interrupted


def execute_context(context: P0Context, backend: ExecutionBackend) -> ExecutionOutcome:
    """Run the post-admission one-shot in a deliberately rigid order."""
    transition: dict[str, Any] = {
        "started": False,
        "marker_sha256": None,
        "latched_signal": None,
    }
    terminal_state: dict[str, Any] = {
        "written": False,
        "status": None,
        "exit_code": None,
        "evidence_sha256": None,
        "terminal_sha256": None,
    }
    try:
        _require_signal_transition_support()
        previous_signals = _install_signal_guards(transition)
    except PrebuildExecutionError:
        return ExecutionOutcome(2, "REFUSED", None, None, None)
    except BaseException:
        return ExecutionOutcome(4, "FAILED", None, None, None)
    try:
        try:
            marker_sha256 = backend.consume(context, transition)
            if (
                transition.get("started") is not True
                or transition.get("marker_sha256") != marker_sha256
                or not isinstance(marker_sha256, str)
                or SHA256_RE.fullmatch(marker_sha256) is None
            ):
                raise TerminalPersistenceError("P0 consumption transition is indeterminate")
            _raise_latched_signal(transition)
        except BaseException as exc:
            marker = transition.get("marker_sha256")
            if isinstance(exc, MarkerTransitionError):
                marker = exc.marker_sha256
                transition["started"] = True
                transition["marker_sha256"] = marker
            if transition.get("started") is True:
                if not isinstance(marker, str) or SHA256_RE.fullmatch(marker) is None:
                    raise TerminalPersistenceError("P0 consumption transition is indeterminate") from exc
                return _terminalize_after_m0(
                    context, backend, marker, terminal_state, exc
                )
            if isinstance(exc, P0Interrupted):
                return ExecutionOutcome(4, "FAILED", None, None, None)
            if isinstance(exc, PrebuildExecutionError):
                return ExecutionOutcome(2, "REFUSED", None, None, None)
            return ExecutionOutcome(4, "FAILED", None, None, None)

        try:
            backend.clone_source(context)
            _raise_latched_signal(transition)
            backend.rewrite_lock(context)
            _raise_latched_signal(transition)
            backend.materialize_corpus(context)
            _raise_latched_signal(transition)
            backend.prepare_fresh_cache(context)
            _raise_latched_signal(transition)
            hydration = backend.hydrate(context)
            _raise_latched_signal(transition)
            backend.finalize_fixture_configs(context)
            _raise_latched_signal(transition)
            derived = backend.bind_and_seal_cache(context)
            _raise_latched_signal(transition)
            facts = backend.extract_facts(context, derived)
            _raise_latched_signal(transition)
            if (
                set(facts) != {"run1", "run2"}
                or facts["run1"].facts_sha256 != facts["run2"].facts_sha256
                or facts["run1"].run_id == facts["run2"].run_id
                or facts["run1"].root == facts["run2"].root
            ):
                raise PrebuildExecutionError(
                    "offline fact runs are not independent and byte-identical"
                )
            backend.seal_execution_root(context)
            _raise_latched_signal(transition)
            evidence = _evidence_record(context, marker_sha256, hydration, derived, facts)
            _raise_latched_signal(transition)
            # R0 and T0 form one terminal chain.  A stop signal cannot land
            # between their durability boundaries and fabricate an ABORTED
            # state after a completed evidence receipt.
            prior_mask = _block_catchable_signals()
            try:
                # This is the explicit completion cutover.  A stop already
                # latched (or queued while acquiring the mask) must still
                # become ABORTED before R0; only a signal after this check is
                # completion-safe because R0/T0 are now one masked chain.
                pending = _pending_catchable_signal()
                if pending is not None:
                    transition["latched_signal"] = pending
                _raise_latched_signal(transition)
                evidence_sha256 = backend.publish_evidence(context, evidence)
                _publish_terminal_guarded(
                    backend,
                    context,
                    _terminal_record(
                        context,
                        marker_sha256,
                        status="COMPLETED",
                        exit_code=0,
                        failure=None,
                        failure_diagnostic=None,
                        evidence_sha256=evidence_sha256,
                    ),
                    terminal_state,
                )
            finally:
                _restore_signal_mask(prior_mask)
            return _terminal_outcome(marker_sha256, terminal_state)
        except BaseException as exc:
            if terminal_state.get("written") is True:
                return _terminal_outcome(marker_sha256, terminal_state)
            return _terminalize_after_m0(
                context, backend, marker_sha256, terminal_state, exc
            )
    finally:
        _restore_signal_guards(previous_signals)


def run_with_admission(admitter: Any, backend: ExecutionBackend) -> ExecutionOutcome:
    """Testable wrapper proving admission failures cannot create M0."""
    try:
        context = admitter()
    except PrebuildExecutionError:
        return ExecutionOutcome(2, "REFUSED", None, None, None)
    except BaseException:
        return ExecutionOutcome(4, "FAILED", None, None, None)
    return execute_context(context, backend)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    if not args.execute:
        print("stage2-prebuild-execute: refusing — explicit --execute required", file=sys.stderr)
        return 2
    try:
        outcome = run_with_admission(admit_live, LiveBackend())
    except TerminalPersistenceError:
        # Once M0 may exist, neither an uncaught crash nor a failed T0 write
        # is a capacity/refusal result.  Keep this line deliberately
        # non-disclosing: terminal evidence, not stderr, owns the detail.
        print("stage2-prebuild-execute: execution failed", file=sys.stderr)
        return 4
    except BaseException:
        print("stage2-prebuild-execute: execution failed", file=sys.stderr)
        return 4
    if outcome.status == "COMPLETED":
        print(canonical_json({
            "schema": "t111-gate2-v2-stage2-prebuild-execution-result-v1",
            "status": "COMPLETED",
            "terminal_receipt_sha256": outcome.terminal_sha256,
        }))
        return 0
    if outcome.exit_code == 4:
        print("stage2-prebuild-execute: execution failed", file=sys.stderr)
        return 4
    print(f"stage2-prebuild-execute: {outcome.status.lower()}", file=sys.stderr)
    return outcome.exit_code


if __name__ == "__main__":
    raise SystemExit(main())
