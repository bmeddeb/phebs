#!/usr/bin/env python3
"""One-score Attempt-3 estimator with a durable consumption boundary.

This wrapper deliberately keeps scoring and confidence-bound code in
``score.py``.  Phase A admits only the exact historical Attempt-3 inputs and
reconstructs the complete public source/fact frame.  Phase B repeats the
producer-toolchain guard, durably consumes the authorization, and only then
decodes the hidden key and frozen labels.

The checked-in authorization is intentionally non-executable until an
independent cumulative-diff review accepts it and a later clean commit binds
that acceptance.  Do not run either phase against ceremony inputs before that
binding commit exists.
"""

from __future__ import annotations

import argparse
import contextlib
import dataclasses
import datetime as dt
import errno
import fcntl
import hashlib
import importlib.util
import io
import json
import os
import re
import secrets
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path, PurePosixPath
from types import ModuleType, SimpleNamespace
from typing import Any, Callable, Mapping, Sequence


BASE = Path(__file__).resolve().parent
REPO_ROOT = BASE.parent.parent
DEFAULT_AUTHORIZATION = BASE / "labeling" / "estimator-authorization.json"

AUTHORIZATION_ID = "t111-estimator-auth-1"
CANONICAL_AUTHORIZATION_PATH = Path("spike/t111/labeling/estimator-authorization.json")
AUTHORIZATION_SCHEMA = "t111-estimator-authorization-v1"
ADMISSION_RECEIPT_SCHEMA = "t111-estimator-admission-receipt-v1"
CONSUMPTION_MARKER_SCHEMA = "t111-estimator-consumption-marker-v1"
RESULT_RECEIPT_SCHEMA = "t111-estimator-result-receipt-v1"

SPEC_DIGEST = "sha256:e19b1e4f6abf6cf3603627c1f4753e593fbe56a327ea1810e26dfcd75b1e4fa3"
ATTESTATION_DIGEST = "sha256:81d6eb07bdba4134242fa8ce37a15887515d66fa6a2452d3e76a186dd29a6c04"
HISTORICAL_RUNTIME_COMMIT = "9f639149e1bb2879618a269128382d9b08e71a32"
HISTORICAL_SCORER_DIGEST = "sha256:ee5b38b992dfd182063f0054d3343bcbe8a0a35129142dc1c4b85c5d682a3c55"

EXPECTED_TOOLCHAIN = {
    "go": {
        "version": "go version go1.26.5 darwin/arm64",
        "digest": "sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0",
    },
    "git": {
        "version": "git version 2.50.1 (Apple Git-155)",
        "digest": "sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818",
    },
}

HISTORICAL_ARCHIVE_PATHS = (
    "go.mod",
    "go.sum",
    "spike/t111/corpus.lock.json",
    "spike/t111/gate34_common.py",
    "spike/t111/label_adjudication.py",
    "spike/t111/label_prep.py",
    "spike/t111/label_protocol.py",
    "spike/t111/reviewer_kits.py",
    "spike/t111/typedcalloracle",
    "spike/t111/labeling/burn-ledger.json",
)

_SHA256_RE = re.compile(r"sha256:[0-9a-f]{64}\Z")
_COMMIT_RE = re.compile(r"[0-9a-f]{40}\Z")


class EstimatorError(RuntimeError):
    """The estimator rejected a mechanical or state invariant."""


class AuthorizationConsumed(EstimatorError):
    """The exclusive authorization has already transitioned to consumed."""


class MarkerTransitionError(EstimatorError):
    """Marker persistence failed after O_EXCL may have consumed the run."""

    def __init__(self, message: str, *, transition_started: bool) -> None:
        super().__init__(message)
        self.transition_started = transition_started


class EstimatorInterrupted(BaseException):
    """A catchable signal interrupted a post-marker execution."""


class _PublicPrefixComplete(BaseException):
    pass


@dataclasses.dataclass(frozen=True)
class HistoricalRuntime:
    root: Path
    score: ModuleType


@dataclasses.dataclass(frozen=True)
class AdmissionContext:
    authorization: dict[str, Any]
    authorization_digest: str
    toolchain: dict[str, str]
    runtime: HistoricalRuntime | None = None
    bundle: dict[str, Any] | None = None
    label_commitment: dict[str, Any] | None = None


@dataclasses.dataclass(frozen=True)
class AdmissionOutcome:
    admitted: bool
    receipt: dict[str, Any]
    context: AdmissionContext | None
    failure_code: str | None = None


def canonical_json_bytes(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode(
        "utf-8"
    )


def sha256_bytes(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def sha256_file_opaque(path: Path) -> str:
    """Hash bytes without text or JSON decoding."""

    digest = hashlib.sha256()
    try:
        with path.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as exc:
        raise EstimatorError(f"cannot hash {path}: {exc}") from exc
    return "sha256:" + digest.hexdigest()


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="microseconds").replace(
        "+00:00", "Z"
    )


def _write_all(fd: int, payload: bytes) -> None:
    offset = 0
    while offset < len(payload):
        written = os.write(fd, payload[offset:])
        if written <= 0:
            raise OSError("short write")
        offset += written


def _open_private_directory(path: Path) -> int:
    """Open an absolute state directory without following any ancestor symlink."""

    if not path.is_absolute() or ".." in path.parts:
        raise EstimatorError(f"state directory must be absolute and normalized: {path}")
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open("/", flags)
    try:
        for part in path.parts[1:]:
            next_descriptor = os.open(part, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = next_descriptor
        info = os.fstat(descriptor)
        if not stat.S_ISDIR(info.st_mode):
            raise EstimatorError(f"state directory is not a directory: {path}")
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise EstimatorError(f"state directory must be owner-only mode 0700: {path}")
        return descriptor
    except BaseException:
        os.close(descriptor)
        raise


def _state_target_absent_or_regular(directory_fd: int, name: str) -> None:
    try:
        info = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
    except FileNotFoundError:
        return
    if not stat.S_ISREG(info.st_mode):
        raise EstimatorError(f"state target must be absent or a regular file: {name}")


def _read_state_file(path: Path) -> bytes:
    directory_fd = _open_private_directory(path.parent)
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(path.name, flags, dir_fd=directory_fd)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
                raise EstimatorError(f"state file must be a mode-0600 regular file: {path}")
            chunks: list[bytes] = []
            while True:
                chunk = os.read(fd, 1024 * 1024)
                if not chunk:
                    break
                chunks.append(chunk)
            return b"".join(chunks)
        finally:
            os.close(fd)
    except OSError as exc:
        raise EstimatorError(f"cannot read state file {path}: {exc}") from exc
    finally:
        os.close(directory_fd)


def atomic_replace_receipt(path: Path, value: Mapping[str, Any]) -> None:
    """Durably replace the repeatable, non-consuming Phase-A receipt."""

    payload = canonical_json_bytes(dict(value))
    directory_fd = _open_private_directory(path.parent)
    temporary = f".{path.name}.{secrets.token_hex(16)}.tmp"
    try:
        _state_target_absent_or_regular(directory_fd, path.name)
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
        fd = os.open(temporary, flags, 0o600, dir_fd=directory_fd)
        os.fchmod(fd, 0o600)
        try:
            _write_all(fd, payload)
            os.fsync(fd)
        finally:
            os.close(fd)
        os.replace(
            temporary,
            path.name,
            src_dir_fd=directory_fd,
            dst_dir_fd=directory_fd,
        )
        temporary = ""
        os.fsync(directory_fd)
    except OSError as exc:
        raise EstimatorError(f"cannot persist admission receipt {path}: {exc}") from exc
    finally:
        if temporary:
            try:
                os.unlink(temporary, dir_fd=directory_fd)
            except OSError:
                pass
        os.close(directory_fd)


def durable_create_exclusive(path: Path, value: Mapping[str, Any]) -> None:
    """Create one canonical file with O_EXCL and fsync file plus directory."""

    payload = canonical_json_bytes(dict(value))
    directory_fd = _open_private_directory(path.parent)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    flags |= getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(path.name, flags, 0o600, dir_fd=directory_fd)
    except FileExistsError as exc:
        os.close(directory_fd)
        raise AuthorizationConsumed(f"exclusive state already exists: {path}") from exc
    except OSError as exc:
        os.close(directory_fd)
        raise MarkerTransitionError(
            f"cannot create exclusive state {path}: {exc}", transition_started=False
        ) from exc
    try:
        try:
            os.fchmod(fd, 0o600)
            _write_all(fd, payload)
            os.fsync(fd)
        finally:
            os.close(fd)
        os.fsync(directory_fd)
    except BaseException as exc:
        # Never unlink after O_EXCL succeeds.  Even a directory-fsync failure is
        # conservatively consumed because the directory entry may survive.
        raise MarkerTransitionError(
            f"exclusive state persistence failed for {path}: {exc}",
            transition_started=True,
        ) from exc
    finally:
        os.close(directory_fd)


def durable_publish_exclusive(path: Path, value: Mapping[str, Any]) -> None:
    """Publish only complete fsynced result bytes, without replacing a receipt."""

    payload = canonical_json_bytes(dict(value))
    directory_fd = _open_private_directory(path.parent)
    temporary = f".{path.name}.{secrets.token_hex(16)}.tmp"
    linked = False
    try:
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
        fd = os.open(temporary, flags, 0o600, dir_fd=directory_fd)
        try:
            os.fchmod(fd, 0o600)
            _write_all(fd, payload)
            os.fsync(fd)
        finally:
            os.close(fd)
        try:
            os.link(
                temporary,
                path.name,
                src_dir_fd=directory_fd,
                dst_dir_fd=directory_fd,
                follow_symlinks=False,
            )
            linked = True
        except FileExistsError as exc:
            raise AuthorizationConsumed(f"result receipt already exists: {path}") from exc
        os.fsync(directory_fd)
        os.unlink(temporary, dir_fd=directory_fd)
        temporary = ""
        os.fsync(directory_fd)
    except AuthorizationConsumed:
        raise
    except BaseException as exc:
        raise MarkerTransitionError(
            f"cannot durably publish result receipt {path}: {exc}",
            transition_started=linked,
        ) from exc
    finally:
        if temporary:
            try:
                os.unlink(temporary, dir_fd=directory_fd)
            except OSError:
                pass
        os.close(directory_fd)


def _regular_file(path: Path, description: str) -> None:
    try:
        info = path.lstat()
    except OSError as exc:
        raise EstimatorError(f"{description} is unavailable: {path}: {exc}") from exc
    if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode):
        raise EstimatorError(f"{description} must be a regular non-symlink file: {path}")


def _resolve_path(raw: str, root: Path = REPO_ROOT) -> Path:
    path = Path(raw)
    return path if path.is_absolute() else root / path


def _expected_digest(value: Any, description: str) -> str:
    if not isinstance(value, str) or _SHA256_RE.fullmatch(value) is None:
        raise EstimatorError(f"authorization has invalid {description} digest")
    return value


def exact_toolchain_guard() -> dict[str, str]:
    """Resolve, hash, and identify the exact sealed producer Go and Git."""

    resolved: dict[str, str] = {}
    for name in ("go", "git"):
        expected = EXPECTED_TOOLCHAIN[name]
        found = shutil.which(name)
        if found is None:
            raise EstimatorError(f"required exact {name} executable is unavailable")
        try:
            path = Path(found).resolve(strict=True)
        except OSError as exc:
            raise EstimatorError(f"cannot resolve {name} executable: {exc}") from exc
        digest = sha256_file_opaque(path)
        try:
            completed = subprocess.run(
                [str(path), "version"],
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env={"PATH": str(path.parent), "GOENV": "off", "GOTOOLCHAIN": "local"},
            )
        except OSError as exc:
            raise EstimatorError(f"cannot identify {name} executable: {exc}") from exc
        version = completed.stdout.decode("utf-8", errors="replace").strip()
        if (
            completed.returncode != 0
            or version != expected["version"]
            or digest != expected["digest"]
        ):
            raise EstimatorError(
                f"{name} toolchain differs from the sealed producer: "
                f"version={version!r} digest={digest}"
            )
        resolved[f"{name}_path"] = str(path)
        resolved[f"{name}_version"] = version
        resolved[f"{name}_digest"] = digest
    return resolved


def load_authorization(path: Path) -> tuple[dict[str, Any], str]:
    _regular_file(path, "estimator authorization")
    try:
        document = path.read_bytes()
        value = json.loads(document.decode("utf-8", errors="strict"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise EstimatorError(f"cannot load estimator authorization: {exc}") from exc
    if not isinstance(value, dict):
        raise EstimatorError("estimator authorization must be a JSON object")
    if document != canonical_json_bytes(value):
        raise EstimatorError("estimator authorization is not canonical JSON")
    return value, sha256_bytes(document)


def _git(
    git_path: str, args: Sequence[str], *, stdout: int = subprocess.PIPE
) -> subprocess.CompletedProcess[bytes]:
    completed = subprocess.run(
        [git_path, *args],
        cwd=REPO_ROOT,
        check=False,
        stdout=stdout,
        stderr=subprocess.PIPE,
        env={
            "PATH": str(Path(git_path).parent),
            "HOME": os.environ.get("HOME", ""),
            "LC_ALL": "C",
        },
    )
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", errors="replace").strip()
        raise EstimatorError(f"Git verification failed: {detail}")
    return completed


def _verify_fixed_environment(auth: Mapping[str, Any], toolchain: Mapping[str, str]) -> None:
    environment = auth.get("environment")
    if not isinstance(environment, dict):
        raise EstimatorError("authorization has no fixed environment")
    if environment.get("cwd") != str(REPO_ROOT):
        raise EstimatorError("authorization cwd is not the estimator repository")
    if Path.cwd().resolve() != REPO_ROOT:
        raise EstimatorError("estimator must run from its fixed repository cwd")
    required = environment.get("variables")
    if not isinstance(required, dict) or not all(
        isinstance(key, str) and isinstance(value, str) for key, value in required.items()
    ):
        raise EstimatorError("authorization environment variables are invalid")
    drift = {
        key: {"expected": value, "actual": os.environ.get(key)}
        for key, value in required.items()
        if os.environ.get(key) != value
    }
    if drift:
        raise EstimatorError(f"fixed environment differs: {sorted(drift)}")
    python = environment.get("python")
    if not isinstance(python, dict):
        raise EstimatorError("authorization has no Python identity")
    expected_path = Path(str(python.get("path", "")))
    try:
        actual_path = Path(sys.executable).resolve(strict=True)
    except OSError as exc:
        raise EstimatorError(f"cannot resolve Python executable: {exc}") from exc
    if actual_path != expected_path or sha256_file_opaque(actual_path) != python.get("digest"):
        raise EstimatorError("Python executable differs from the reviewed environment")
    if sys.version.split()[0] != python.get("version"):
        raise EstimatorError("Python version differs from the reviewed environment")
    producer = environment.get("producer_toolchain")
    actual_producer = {
        "go": {
            "path": toolchain["go_path"],
            "version": toolchain["go_version"],
            "digest": toolchain["go_digest"],
        },
        "git": {
            "path": toolchain["git_path"],
            "version": toolchain["git_version"],
            "digest": toolchain["git_digest"],
        },
    }
    if producer != actual_producer:
        raise EstimatorError("authorization producer-toolchain identity differs")


def _verify_fixed_command(auth: Mapping[str, Any], actual_command: Sequence[str]) -> None:
    expected_name = "admission_command" if "--admission-only" in actual_command else "fixed_command"
    expected = auth.get(expected_name)
    if not isinstance(expected, list) or not all(isinstance(item, str) for item in expected):
        raise EstimatorError(f"authorization has no valid {expected_name}")
    if list(actual_command) != expected:
        raise EstimatorError(f"invocation differs from the reviewed {expected_name}")


def verify_implementation_and_command(
    auth: Mapping[str, Any], toolchain: Mapping[str, str], actual_command: Sequence[str]
) -> None:
    if auth.get("schema") != AUTHORIZATION_SCHEMA:
        raise EstimatorError("unsupported estimator authorization schema")
    if auth.get("authorization_id") != AUTHORIZATION_ID:
        raise EstimatorError("wrong estimator authorization ID")
    specification = auth.get("specification")
    attestation = auth.get("attestation")
    if not isinstance(specification, dict) or specification.get("digest") != SPEC_DIGEST:
        raise EstimatorError("authorization is not bound to the reviewed specification")
    if not isinstance(attestation, dict) or attestation.get("digest") != ATTESTATION_DIGEST:
        raise EstimatorError("authorization is not bound to the operator attestation")
    if sha256_file_opaque(_resolve_path(str(specification.get("path")))) != SPEC_DIGEST:
        raise EstimatorError("reviewed specification bytes differ")
    if sha256_file_opaque(_resolve_path(str(attestation.get("path")))) != ATTESTATION_DIGEST:
        raise EstimatorError("operator attestation bytes differ")
    _verify_fixed_environment(auth, toolchain)
    _verify_fixed_command(auth, actual_command)

    implementation = auth.get("implementation")
    if not isinstance(implementation, dict):
        raise EstimatorError("authorization has no implementation binding")
    files = implementation.get("files")
    if not isinstance(files, dict) or not files:
        raise EstimatorError("authorization has no implementation file digests")
    for raw_path, expected in sorted(files.items()):
        path = _resolve_path(str(raw_path))
        _regular_file(path, "implementation file")
        if sha256_file_opaque(path) != _expected_digest(expected, str(raw_path)):
            raise EstimatorError(f"implementation digest mismatch: {raw_path}")

    if implementation.get("historical_runtime_commit") != HISTORICAL_RUNTIME_COMMIT:
        raise EstimatorError("historical reconstruction commit differs")
    historical_scorer = _git(
        toolchain["git_path"],
        ["show", f"{HISTORICAL_RUNTIME_COMMIT}:spike/t111/score.py"],
    ).stdout
    if sha256_bytes(historical_scorer) != HISTORICAL_SCORER_DIGEST:
        raise EstimatorError("historical scorer anchor is unavailable or changed")

    review = auth.get("review")
    binding = auth.get("binding")
    if not isinstance(review, dict) or not isinstance(binding, dict):
        raise EstimatorError("authorization review/binding state is invalid")
    if review.get("status") != "accepted" or binding.get("status") != "executable":
        raise EstimatorError("authorization is pending independent diff review")
    binding_commit = binding.get("commit")
    if not isinstance(binding_commit, str) or _COMMIT_RE.fullmatch(binding_commit) is None:
        raise EstimatorError("authorization has no binding commit")
    head = _git(toolchain["git_path"], ["rev-parse", "HEAD"]).stdout.decode().strip()
    ancestor = subprocess.run(
        [toolchain["git_path"], "merge-base", "--is-ancestor", binding_commit, head],
        cwd=REPO_ROOT,
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
    )
    if ancestor.returncode != 0:
        raise EstimatorError("current commit is not a clean descendant of the binding commit")
    for diff_args in (["diff", "--quiet"], ["diff", "--cached", "--quiet"]):
        completed = subprocess.run(
            [toolchain["git_path"], *diff_args],
            cwd=REPO_ROOT,
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
        )
        if completed.returncode != 0:
            raise EstimatorError("tracked worktree differs from the binding commit")


def _safe_extract_archive(document: bytes, destination: Path) -> None:
    with tarfile.open(fileobj=io.BytesIO(document), mode="r:") as archive:
        for member in archive.getmembers():
            pure = PurePosixPath(member.name)
            if pure.is_absolute() or ".." in pure.parts:
                raise EstimatorError("historical Git archive has an unsafe member")
            if member.issym() or member.islnk() or member.isdev():
                raise EstimatorError("historical Git archive has a non-regular member")
        archive.extractall(destination, filter="data")


def _load_module(name: str, path: Path) -> ModuleType:
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise EstimatorError(f"cannot load reviewed implementation module {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


@contextlib.contextmanager
def historical_runtime(
    auth: Mapping[str, Any], toolchain: Mapping[str, str]
) -> Any:
    """Materialize only the reviewed historical code/public inputs, never key data."""

    with tempfile.TemporaryDirectory(prefix="t111-estimator-runtime-") as raw:
        root = Path(raw)
        archive = _git(
            toolchain["git_path"],
            ["archive", "--format=tar", HISTORICAL_RUNTIME_COMMIT, *HISTORICAL_ARCHIVE_PATHS],
        ).stdout
        _safe_extract_archive(archive, root)
        historical = auth["implementation"]["historical_files"]
        for raw_path, expected in sorted(historical.items()):
            path = root / raw_path
            _regular_file(path, "historical implementation file")
            if sha256_file_opaque(path) != _expected_digest(expected, raw_path):
                raise EstimatorError(f"historical implementation digest mismatch: {raw_path}")

        t111 = root / "spike" / "t111"
        module_names = (
            "gate34_common",
            "label_protocol",
            "reviewer_kits",
            "label_adjudication",
            "label_prep",
        )
        saved = {name: sys.modules.get(name) for name in (*module_names, "_t111_estimator_score")}
        old_path = list(sys.path)
        try:
            sys.path.insert(0, str(t111))
            for name in (*module_names, "_t111_estimator_score"):
                sys.modules.pop(name, None)
            prep = _load_module("label_prep", t111 / "label_prep.py")
            if prep.typed_oracle_code_digest() != auth["bound_digests"]["typed_oracle"]:
                raise EstimatorError("historical typed-oracle digest mismatch")
            score = _load_module("_t111_estimator_score", BASE / "score.py")
            yield HistoricalRuntime(root=root, score=score)
        finally:
            sys.path[:] = old_path
            for name in (*module_names, "_t111_estimator_score"):
                sys.modules.pop(name, None)
                if saved.get(name) is not None:
                    sys.modules[name] = saved[name]


def _read_canonical_json(path: Path, description: str) -> tuple[dict[str, Any], bytes]:
    _regular_file(path, description)
    try:
        document = path.read_bytes()
        value = json.loads(document.decode("utf-8", errors="strict"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise EstimatorError(f"cannot load {description}: {exc}") from exc
    if not isinstance(value, dict):
        raise EstimatorError(f"{description} must be a JSON object")
    return value, document


def _verify_digest(path: Path, expected: str, description: str) -> None:
    _regular_file(path, description)
    if sha256_file_opaque(path) != _expected_digest(expected, description):
        raise EstimatorError(f"{description} digest mismatch: {path}")


def _artifact_paths(auth: Mapping[str, Any]) -> tuple[Path, dict[str, str]]:
    inputs = auth["inputs"]
    artifact_dir = _resolve_path(inputs["artifact_directory"])
    payloads = auth["bound_digests"]["artifact_payloads"]
    if not isinstance(payloads, dict):
        raise EstimatorError("authorization artifact payload digests are invalid")
    return artifact_dir, {str(name): str(digest) for name, digest in payloads.items()}


def _verify_public_bundle_prefix(
    auth: Mapping[str, Any], runtime: HistoricalRuntime
) -> tuple[dict[str, Any], dict[str, Any]]:
    score = runtime.score
    inputs = auth["inputs"]
    digests = auth["bound_digests"]
    artifact_dir, payloads = _artifact_paths(auth)
    manifest_path = artifact_dir / "manifest.json"
    _verify_digest(manifest_path, digests["artifact_manifest"], "artifact manifest")
    manifest, _ = _read_canonical_json(manifest_path, "artifact manifest")
    if (
        manifest.get("schema") != "t111-gate2-probability-sample-v3"
        or manifest.get("systems")
        != ["online-boutique", "dapr", "temporal", "loki"]
        or manifest.get("gate_design_fixed") is not True
        or manifest.get("coordinate_only_sites") is not True
        or manifest.get("randomization", {}).get("mode")
        != "audited-unpredictable-sha256-rank-v2"
        or manifest.get("randomization", {}).get("input_binding")
        != digests["attempt_input_binding"]
    ):
        raise EstimatorError("artifact manifest is not the fixed Attempt-3 design")
    provenance = manifest.get("provenance")
    if not isinstance(provenance, dict):
        raise EstimatorError("artifact manifest has no provenance")
    if provenance.get("locked_commits") != auth["source_commits"]:
        raise EstimatorError("artifact manifest has the wrong source commits")
    if provenance.get("corpus_lock_sha256") != digests["corpus_lock"].removeprefix(
        "sha256:"
    ):
        raise EstimatorError("artifact manifest has the wrong corpus-lock digest")
    if provenance.get("prep_script_sha256") != digests["historical_prep"].removeprefix(
        "sha256:"
    ):
        raise EstimatorError("artifact manifest has the wrong preparation script")
    if provenance.get("fact_verifier_sha256") != digests[
        "historical_fact_verifier"
    ].removeprefix("sha256:"):
        raise EstimatorError("artifact manifest has the wrong fact verifier")
    if provenance.get("typed_call_oracle_sha256") != digests["typed_oracle"]:
        raise EstimatorError("artifact manifest has the wrong typed oracle")
    expected_fact_provenance = {
        name: digest.removeprefix("sha256:")
        for name, digest in digests["facts"].items()
    }
    if provenance.get("facts_sha256") != expected_fact_provenance:
        raise EstimatorError("artifact manifest has the wrong historical facts")
    if provenance.get("typed_call_oracle_toolchain_identity") != auth[
        "environment"
    ]["producer_toolchain_identity"]:
        raise EstimatorError("artifact manifest has the wrong producer toolchain")
    if manifest.get("artifacts_sha256") != {
        name: digest.removeprefix("sha256:") for name, digest in payloads.items()
    }:
        raise EstimatorError("artifact manifest payload digest map differs")
    if manifest.get("artifact_set") != sorted(payloads):
        raise EstimatorError("artifact manifest payload set differs")

    expected_entries = set(payloads) | {"manifest.json"}
    try:
        children = list(artifact_dir.iterdir())
    except OSError as exc:
        raise EstimatorError(f"cannot enumerate sealed artifact directory: {exc}") from exc
    if {child.name for child in children} != expected_entries:
        raise EstimatorError("sealed artifact directory has a missing or extra entry")
    for child in children:
        _regular_file(child, "sealed artifact entry")
    for name, expected in payloads.items():
        if name == "key.jsonl":
            continue
        _verify_digest(artifact_dir / name, expected, f"artifact {name}")

    historical_lock = runtime.root / "spike" / "t111" / "corpus.lock.json"
    _verify_digest(historical_lock, digests["corpus_lock"], "historical corpus lock")
    facts_dir = _resolve_path(inputs["facts_directory"])
    for name, expected in sorted(digests["facts"].items()):
        _verify_digest(facts_dir / name, expected, f"historical fact {name}")
    claim_path = _resolve_path(inputs["sealed_claim"])
    _verify_digest(claim_path, digests["sealed_claim"], "sealed claim")
    claim, _ = _read_canonical_json(claim_path, "sealed claim")
    if (
        claim.get("schema") != "t111-gate2-sealed-claim-v1"
        or claim.get("input_binding") != digests["attempt_input_binding"]
    ):
        raise EstimatorError("sealed claim is not bound to Attempt 3")

    seed_value, _ = _read_canonical_json(artifact_dir / "seed.json", "audit seed")
    attempt_value, _ = _read_canonical_json(artifact_dir / "attempt.json", "attempt claim")
    try:
        seed_record = score.validate_seed_record(
            seed_value,
            expected_input_binding=manifest["randomization"]["input_binding"],
            expected_input_committed_at=manifest["randomization"].get("input_committed_at"),
        )
        attempt_claim = score.validate_attempt_claim(
            attempt_value,
            commits={str(k): str(v) for k, v in manifest["provenance"]["locked_commits"].items()},
            base_input_binding=str(manifest["randomization"].get("base_input_binding")),
            input_binding=str(manifest["randomization"]["input_binding"]),
        )
    except Exception as exc:
        raise EstimatorError(f"public Attempt-3 receipts are invalid: {exc}") from exc
    if manifest.get("randomization", {}).get("seed_record") != seed_record:
        raise EstimatorError("manifest seed receipt differs")
    if manifest.get("attempt_claim") != attempt_claim:
        raise EstimatorError("manifest attempt claim differs")

    original_validator = score.validate_payloads_after_toolchain_attestation

    def stop_after_public_prefix(*_args: Any, **_kwargs: Any) -> None:
        raise _PublicPrefixComplete()

    score.validate_payloads_after_toolchain_attestation = stop_after_public_prefix
    try:
        try:
            score.load_bundle(
                artifact_dir,
                _resolve_path(inputs["corpus_directory"]),
                historical_lock,
                facts_dir,
            )
        except _PublicPrefixComplete:
            pass
        else:
            raise EstimatorError("public bundle prefix did not stop before hidden-key access")
    finally:
        score.validate_payloads_after_toolchain_attestation = original_validator
    return manifest, seed_record


def _verify_label_publication_metadata(
    auth: Mapping[str, Any], runtime: HistoricalRuntime, manifest: Mapping[str, Any]
) -> dict[str, Any]:
    score = runtime.score
    inputs = auth["inputs"]
    digests = auth["bound_digests"]
    artifact_dir, _ = _artifact_paths(auth)
    commitment_path = _resolve_path(inputs["label_commitment"])
    receipt_path = _resolve_path(inputs["label_publication_receipt"])
    assignment_path = _resolve_path(inputs["reviewer_assignment"])
    _verify_digest(commitment_path, digests["label_commitment"], "label commitment")
    _verify_digest(assignment_path, digests["reviewer_assignment"], "reviewer assignment")
    commitment_value, commitment_document = _read_canonical_json(
        commitment_path, "label commitment"
    )
    receipt_value, _ = _read_canonical_json(receipt_path, "label publication receipt")
    assignment_document = assignment_path.read_bytes()
    manifest_document = (artifact_dir / "manifest.json").read_bytes()
    try:
        commitment = score.validate_label_commitment(commitment_value)
        receipt = score.validate_github_gist_receipt(receipt_value)
        if commitment_document != score.canonical_label_commitment_document(commitment):
            raise EstimatorError("label commitment is not canonical")
        score.validate_assignment_manifest(
            assignment_document, commitment["reviewer_assignment_sha256"]
        )
    except Exception as exc:
        raise EstimatorError(f"label publication metadata is invalid: {exc}") from exc
    public_anchor = auth["public_label_anchor"]
    if (
        receipt.get("api_url") != public_anchor["api_url"]
        or receipt.get("revision") != public_anchor["revision"]
        or receipt.get("document_sha256") != sha256_bytes(commitment_document)
    ):
        raise EstimatorError("label publication receipt differs from the fixed public anchor")
    if commitment.get("labels_sha256") != digests["labels"]:
        raise EstimatorError("label commitment has the wrong frozen-label digest")
    if commitment.get("artifact_manifest_sha256") != sha256_bytes(manifest_document):
        raise EstimatorError("label commitment has the wrong artifact manifest")
    if commitment.get("reviewer_assignment_sha256") != sha256_bytes(assignment_document):
        raise EstimatorError("label commitment has the wrong reviewer assignment")
    if commitment.get("input_binding") != manifest["randomization"]["input_binding"]:
        raise EstimatorError("label commitment has the wrong Attempt-3 input binding")
    if commitment.get("adjudication") != auth["label_population"]["adjudication"]:
        raise EstimatorError("label commitment adjudication counters differ")
    committed_at = dt.datetime.strptime(
        commitment["committed_at"], "%Y-%m-%dT%H:%M:%SZ"
    ).replace(tzinfo=dt.timezone.utc)
    published_at = dt.datetime.strptime(receipt["created_at"], "%Y-%m-%dT%H:%M:%SZ").replace(
        tzinfo=dt.timezone.utc
    )
    if published_at < committed_at:
        raise EstimatorError("label publication predates the frozen commitment")
    return commitment


def verify_public_inputs(
    auth: Mapping[str, Any], runtime: HistoricalRuntime
) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    manifest, seed_record = _verify_public_bundle_prefix(auth, runtime)
    commitment = _verify_label_publication_metadata(auth, runtime, manifest)
    return manifest, seed_record, commitment


def verify_outcome_digests_opaque(auth: Mapping[str, Any]) -> None:
    inputs = auth["inputs"]
    digests = auth["bound_digests"]
    artifact_dir, payloads = _artifact_paths(auth)
    key_path = artifact_dir / "key.jsonl"
    labels_path = _resolve_path(inputs["labels"])
    _regular_file(key_path, "hidden key")
    _regular_file(labels_path, "frozen labels")
    if sha256_file_opaque(key_path) != payloads["key.jsonl"]:
        raise EstimatorError("hidden key opaque digest mismatch")
    if sha256_file_opaque(labels_path) != digests["labels"]:
        raise EstimatorError("frozen labels opaque digest mismatch")


def _reconstruction_args(
    auth: Mapping[str, Any], runtime: HistoricalRuntime, manifest: Mapping[str, Any]
) -> SimpleNamespace:
    inputs = auth["inputs"]
    artifact_dir, _ = _artifact_paths(auth)
    config = manifest.get("sampling_config")
    if not isinstance(config, dict):
        raise EstimatorError("manifest sampling configuration is invalid")
    return SimpleNamespace(
        systems=",".join(manifest["systems"]),
        seed=runtime.score.LEGACY_DIAGNOSTIC_SEED,
        audit_seed_file=artifact_dir / "seed.json",
        force=False,
        corpus_dir=_resolve_path(inputs["corpus_directory"]),
        corpus_lock=runtime.root / "spike" / "t111" / "corpus.lock.json",
        facts_dir=_resolve_path(inputs["facts_directory"]),
        burn_ledger=artifact_dir / "burn-ledger.input.json",
        rebuild_input_committed_at=manifest["randomization"]["seed_record"][
            "input_committed_at"
        ],
        rebuild_attempt_claim=manifest.get("attempt_claim"),
        **config,
    )


def reconstruct_public_frame(
    auth: Mapping[str, Any],
    runtime: HistoricalRuntime,
    manifest: Mapping[str, Any],
) -> dict[str, Any]:
    """Rebuild and validate everything possible without decoding outcomes."""

    score = runtime.score
    args = _reconstruction_args(auth, runtime, manifest)
    try:
        rebuilt = score.build_artifacts(args)
    except Exception as exc:
        raise EstimatorError(f"Attempt-3 source/fact frame reconstruction failed: {exc}") from exc
    rebuilt_manifest, _, _, rebuilt_key_rows = rebuilt
    rebuilt_without_hashes = dict(rebuilt_manifest)
    recorded_without_hashes = dict(manifest)
    recorded_without_hashes.pop("artifacts_sha256", None)
    recorded_without_hashes.pop("artifact_set", None)
    if score.canonical_json(recorded_without_hashes) != score.canonical_json(
        rebuilt_without_hashes
    ):
        raise EstimatorError("public manifest differs from reconstructed frame")

    rebuilt_key = score.unique_by_id(rebuilt_key_rows, "reconstructed key")
    original_key_loader = score.load_hidden_key_after_toolchain_attestation
    original_builder = score.build_artifacts

    def reconstructed_key_only(*_args: Any, **_kwargs: Any) -> dict[str, dict[str, Any]]:
        return rebuilt_key

    score.load_hidden_key_after_toolchain_attestation = reconstructed_key_only
    score.build_artifacts = lambda _args: rebuilt
    try:
        inputs = auth["inputs"]
        bundle = score.load_bundle(
            _resolve_path(inputs["artifact_directory"]),
            _resolve_path(inputs["corpus_directory"]),
            runtime.root / "spike" / "t111" / "corpus.lock.json",
            _resolve_path(inputs["facts_directory"]),
        )
    except Exception as exc:
        raise EstimatorError(f"reconstructed Attempt-3 bundle is invalid: {exc}") from exc
    finally:
        score.load_hidden_key_after_toolchain_attestation = original_key_loader
        score.build_artifacts = original_builder

    score_args = SimpleNamespace(
        confidence=0.95,
        precision_threshold=0.98,
        recall_threshold=0.90,
        fixture_precision_threshold=0.90,
    )
    reasons = score.gate_configuration_reasons(bundle, score_args)
    if reasons:
        raise EstimatorError("Attempt-3 gate configuration rejected: " + "; ".join(reasons))
    return bundle


def state_paths(auth: Mapping[str, Any]) -> tuple[Path, Path, Path]:
    state = auth.get("state")
    if not isinstance(state, dict):
        raise EstimatorError("authorization has no state paths")
    return (
        _resolve_path(str(state["admission_receipt"])),
        _resolve_path(str(state["consumption_marker"])),
        _resolve_path(str(state["result_receipt"])),
    )


def prove_unused_state(auth: Mapping[str, Any]) -> None:
    _, marker, result = state_paths(auth)
    for path, description in ((marker, "consumption marker"), (result, "result receipt")):
        if path.exists() or path.is_symlink():
            raise AuthorizationConsumed(f"prior {description} exists: {path}")


class AdmissionServices:
    """Injectable Phase-A operations; production is the exact protocol implementation."""

    def __init__(
        self,
        authorization: dict[str, Any],
        authorization_digest: str,
        actual_command: Sequence[str],
    ) -> None:
        self.authorization = authorization
        self.authorization_digest = authorization_digest
        self.actual_command = list(actual_command)
        self._runtime_manager: Any = None
        self._runtime: HistoricalRuntime | None = None
        self._manifest: dict[str, Any] | None = None
        self._commitment: dict[str, Any] | None = None

    def guard_toolchain(self) -> dict[str, str]:
        return exact_toolchain_guard()

    def verify_implementation(self, toolchain: Mapping[str, str]) -> None:
        verify_implementation_and_command(
            self.authorization, toolchain, self.actual_command
        )
        self._runtime_manager = historical_runtime(self.authorization, toolchain)
        self._runtime = self._runtime_manager.__enter__()

    def verify_public(self) -> None:
        assert self._runtime is not None
        self._manifest, _, self._commitment = verify_public_inputs(
            self.authorization, self._runtime
        )

    def hash_outcomes(self) -> None:
        verify_outcome_digests_opaque(self.authorization)

    def reconstruct(self) -> dict[str, Any]:
        assert self._runtime is not None and self._manifest is not None
        return reconstruct_public_frame(self.authorization, self._runtime, self._manifest)

    def prove_unused(self) -> None:
        prove_unused_state(self.authorization)

    def context(
        self, toolchain: dict[str, str], bundle: dict[str, Any]
    ) -> AdmissionContext:
        assert self._runtime is not None and self._commitment is not None
        return AdmissionContext(
            authorization=self.authorization,
            authorization_digest=self.authorization_digest,
            toolchain=toolchain,
            runtime=self._runtime,
            bundle=bundle,
            label_commitment=self._commitment,
        )

    def close(self) -> None:
        if self._runtime_manager is not None:
            self._runtime_manager.__exit__(None, None, None)
            self._runtime_manager = None


def _mechanical_failure(exc: BaseException) -> tuple[str, str]:
    if isinstance(exc, AuthorizationConsumed):
        return "authorization-already-consumed", "authorization state is not unused"
    if isinstance(exc, EstimatorInterrupted):
        return "interrupted", "admission was interrupted"
    return "mechanical-rejection", str(exc)


def run_phase_a(
    authorization: dict[str, Any],
    authorization_digest: str,
    actual_command: Sequence[str],
    *,
    services: AdmissionServices | None = None,
) -> AdmissionOutcome:
    """Run ordered, repeatable Phase A and persist one mechanical receipt."""

    service = services or AdmissionServices(
        authorization, authorization_digest, actual_command
    )
    checks: list[dict[str, str]] = []
    context: AdmissionContext | None = None
    failure_code: str | None = None
    failure_message: str | None = None
    try:
        toolchain = service.guard_toolchain()
        checks.append({"check": "exact_producer_toolchain", "status": "pass"})
        service.verify_implementation(toolchain)
        checks.append({"check": "implementation_and_fixed_command", "status": "pass"})
        service.verify_public()
        checks.append({"check": "public_receipts_manifests_and_inputs", "status": "pass"})
        service.hash_outcomes()
        checks.append({"check": "opaque_key_and_label_digests", "status": "pass"})
        bundle = service.reconstruct()
        checks.append({"check": "complete_public_source_fact_frame", "status": "pass"})
        service.prove_unused()
        checks.append({"check": "unused_authorization_state", "status": "pass"})
        context = service.context(toolchain, bundle)
    except BaseException as exc:
        failure_code, failure_message = _mechanical_failure(exc)
        checks.append({"check": "admission", "status": "fail"})

    receipt = {
        "schema": ADMISSION_RECEIPT_SCHEMA,
        "authorization_id": str(authorization.get("authorization_id", AUTHORIZATION_ID)),
        "authorization_sha256": authorization_digest,
        "phase": "A",
        "completed_at": utc_now(),
        "status": "ADMITTED" if context is not None else "REJECTED",
        "decision": "NO RESULT",
        "consumed": False,
        "checks": checks,
        "failure": (
            None
            if context is not None
            else {"code": failure_code, "message": failure_message}
        ),
    }
    admission_path, _, _ = state_paths(authorization)
    atomic_replace_receipt(admission_path, receipt)
    if context is None:
        service.close()
    return AdmissionOutcome(
        admitted=context is not None,
        receipt=receipt,
        context=context,
        failure_code=failure_code,
    )


def _post_marker_failure(exc: BaseException) -> dict[str, str]:
    detail = f"{type(exc).__name__}: {exc}".encode("utf-8", errors="replace")
    return {
        "code": "post-consumption-abort",
        "type": type(exc).__name__,
        "detail_sha256": sha256_bytes(detail),
    }


def _install_signal_guards() -> dict[int, Any]:
    prior: dict[int, Any] = {}

    def interrupted(signum: int, _frame: Any) -> None:
        raise EstimatorInterrupted(f"signal {signum}")

    guarded = {
        getattr(signal, name)
        for name in (
            "SIGINT",
            "SIGTERM",
            "SIGHUP",
            "SIGQUIT",
            "SIGALRM",
            "SIGUSR1",
            "SIGUSR2",
            "SIGXCPU",
            "SIGXFSZ",
        )
        if hasattr(signal, name)
    }
    for signum in guarded:
        try:
            prior[signum] = signal.getsignal(signum)
            signal.signal(signum, interrupted)
        except (OSError, RuntimeError, ValueError):
            pass
    return prior


def _restore_signal_guards(prior: Mapping[int, Any]) -> None:
    for signum, handler in prior.items():
        try:
            signal.signal(signum, handler)
        except (OSError, RuntimeError, ValueError):
            pass


def execute_semantic_score(context: AdmissionContext) -> int:
    """First decode hidden outcomes, verify them, then call unchanged scoring code."""

    if context.runtime is None or context.bundle is None or context.label_commitment is None:
        raise EstimatorError("admission context is incomplete")
    score = context.runtime.score
    auth = context.authorization
    artifact_dir, _ = _artifact_paths(auth)
    key_rows = score.load_jsonl(artifact_dir / "key.jsonl")
    hidden_key = score.unique_by_id(key_rows, "key")
    if hidden_key != context.bundle["key"]:
        raise EstimatorError("decoded hidden key differs from the reconstructed frame")
    bundle = dict(context.bundle)
    bundle["key"] = hidden_key
    labels = score.load_labels(
        _resolve_path(auth["inputs"]["labels"]), set(hidden_key)
    )
    try:
        score.verify_label_commitment(
            context.label_commitment, labels.values(), set(hidden_key)
        )
    except Exception as exc:
        raise EstimatorError(f"frozen labels fail their commitment: {exc}") from exc
    score_args = SimpleNamespace(
        confidence=0.95,
        precision_threshold=0.98,
        recall_threshold=0.90,
        fixture_precision_threshold=0.90,
    )
    return int(score.score_holdout(bundle, labels, score_args))


def run_phase_b(
    context: AdmissionContext,
    *,
    guard: Callable[[], dict[str, str]] = exact_toolchain_guard,
    execute: Callable[[AdmissionContext], int] = execute_semantic_score,
) -> int:
    """Consume exactly once, then emit exactly one canonical result receipt."""

    auth = context.authorization
    admission_path, marker_path, result_path = state_paths(auth)
    admission_digest = sha256_file_opaque(admission_path)
    # Allocate catch-path state and install handlers before the repeated guard.
    # Handlers stay active through result file and directory fsync.
    prior_signals = _install_signal_guards()
    stdout = io.StringIO()
    stderr = io.StringIO()
    consumed = False
    exit_code = 2
    try:
        # This repeat guard is intentionally the last check before O_EXCL.
        repeated_toolchain = guard()
        marker = {
            "schema": CONSUMPTION_MARKER_SCHEMA,
            "authorization_id": AUTHORIZATION_ID,
            "authorization_sha256": context.authorization_digest,
            "phase_a_receipt_sha256": admission_digest,
            "transition": "consumed",
            "consumed_at": utc_now(),
            "producer_toolchain": repeated_toolchain,
        }
        try:
            durable_create_exclusive(marker_path, marker)
            consumed = True
        except MarkerTransitionError as exc:
            consumed = exc.transition_started
            raise

        try:
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                exit_code = execute(context)
            if exit_code not in (0, 1):
                raise EstimatorError(f"scorer returned invalid exit code {exit_code}")
            result = {
                "schema": RESULT_RECEIPT_SCHEMA,
                "authorization_id": AUTHORIZATION_ID,
                "authorization_sha256": context.authorization_digest,
                "consumed": True,
                "status": "COMPLETED",
                "gate_2": "ESTABLISHED" if exit_code == 0 else "NOT ESTABLISHED",
                "performance_claim": (
                    "Attempt-3 frame only" if exit_code == 0 else None
                ),
                "completed_at": utc_now(),
                "exit_code": exit_code,
                "failure": None,
                "score_stdout": stdout.getvalue(),
                "score_stderr": stderr.getvalue(),
            }
        except BaseException as exc:
            exit_code = 2
            result = {
                "schema": RESULT_RECEIPT_SCHEMA,
                "authorization_id": AUTHORIZATION_ID,
                "authorization_sha256": context.authorization_digest,
                "consumed": True,
                "status": "ABORTED",
                "gate_2": "NOT ESTABLISHED",
                "performance_claim": None,
                "completed_at": utc_now(),
                "exit_code": exit_code,
                "failure": _post_marker_failure(exc),
                "score_stdout": None,
                "score_stderr": None,
            }
        durable_publish_exclusive(result_path, result)
        return exit_code
    except AuthorizationConsumed:
        raise
    except BaseException as exc:
        if not consumed:
            raise
        # This covers marker write/fsync failures and any post-marker wrapper
        # failure outside the semantic scorer's inner catch.
        result = {
            "schema": RESULT_RECEIPT_SCHEMA,
            "authorization_id": AUTHORIZATION_ID,
            "authorization_sha256": context.authorization_digest,
            "consumed": True,
            "status": "ABORTED",
            "gate_2": "NOT ESTABLISHED",
            "performance_claim": None,
            "completed_at": utc_now(),
            "exit_code": 2,
            "failure": _post_marker_failure(exc),
            "score_stdout": None,
            "score_stderr": None,
        }
        try:
            durable_publish_exclusive(result_path, result)
        except AuthorizationConsumed:
            # A canonical receipt already exists; never overwrite it.
            pass
        return 2
    finally:
        _restore_signal_guards(prior_signals)


def close_admission_context(outcome: AdmissionOutcome) -> None:
    if outcome.context is None or outcome.context.runtime is None:
        return
    # AdmissionServices owns the context manager in normal execution; this
    # helper exists for callers that stop after Phase A.  TemporaryDirectory
    # cleanup is driven by the service close in main.


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--authorization", type=Path, required=True)
    result.add_argument(
        "--admission-only",
        action="store_true",
        help="emit the non-consuming Phase-A receipt and stop",
    )
    return result


def main(argv: Sequence[str] | None = None) -> int:
    raw_argv = list(sys.argv[1:] if argv is None else argv)
    args = parser().parse_args(raw_argv)
    try:
        requested = Path(args.authorization)
        if requested != CANONICAL_AUTHORIZATION_PATH:
            raise EstimatorError("only the tracked canonical authorization path is executable")
        authorization, authorization_digest = load_authorization(args.authorization)
        if authorization.get("status") != "executable" or authorization.get("review", {}).get("status") != "accepted":
            print("estimator: authorization pending independent diff review", file=sys.stderr)
            return 2
        actual_command = list(getattr(sys, "orig_argv", [sys.executable, sys.argv[0], *raw_argv]))
        services = AdmissionServices(
            authorization, authorization_digest, actual_command
        )
        outcome = run_phase_a(
            authorization,
            authorization_digest,
            actual_command,
            services=services,
        )
        if not outcome.admitted:
            print(canonical_json_bytes(outcome.receipt).decode("utf-8"), end="")
            return 2
        if args.admission_only:
            print(canonical_json_bytes(outcome.receipt).decode("utf-8"), end="")
            services.close()
            return 0
        try:
            return run_phase_b(outcome.context)
        finally:
            services.close()
    except EstimatorError as exc:
        print(f"estimator: NO RESULT: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
