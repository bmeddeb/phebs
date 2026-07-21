#!/usr/bin/env python3
"""GATE2-V2 Stage-2 P0 admission-only authorization parser.

This file intentionally cannot construct a derived root, hydrate modules,
invoke ``t111``, extract facts, enumerate frames, or make network requests.
It validates only a future canonical P0 authorization.  Live prebuild work
requires separately reviewed plumbing added after this narrow admission slice.
"""

from __future__ import annotations

import sys as _bootstrap_sys

if not (
    _bootstrap_sys.flags.isolated
    and _bootstrap_sys.flags.no_site
    and _bootstrap_sys.flags.ignore_environment
):
    _bootstrap_sys.stderr.write(
        "stage2-prebuild: refusing — isolated no-site interpreter required\n"
    )
    raise SystemExit(2)

import argparse
import hashlib
import json
import re
import stat
import sys
from pathlib import Path
from typing import Any


sys.dont_write_bytecode = True


HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parents[3]
AUTHORIZATION_PATH = HERE / "stage2-prebuild-authorization.json"

AUTH_SCHEMA = "t111-gate2-v2-stage2-prebuild-authorization-v1"
ADMISSION_SCHEMA = "t111-gate2-v2-stage2-prebuild-admission-v1"
RECEIPT_FIXTURES = ("temporal", "dapr", "loki", "online-boutique")
BASE_LOCK_FIXTURE_COMMITS = {
    "temporal": "8224a5375112079ad905c4ea829420306431462c",
    "dapr": "08aebd8b2effa2ed939ad5531e25ff8b21a36ef1",
    "loki": "1362d2770ee2abba5e130d67cf30bcc4eefa0da0",
    "online-boutique": "9a4616e77f0f9cbcbecaf27d711c38890dda1404",
}

# P0 binds every value known before a derived tree/cache/fact exists.  The
# deterministic derived-lock digest is known in advance and is therefore an
# input binding; other actual prebuild digests belong to later evidence and
# final enumeration authorization.
AUTHORIZATION_FIELDS = {
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
BOOTSTRAP_FIELDS = {
    "source_t111_path",
    "source_t111_sha256",
    "source_t111_access",
    "derived_t111_path",
    "derived_t111_sha256",
    "binary_copy_policy",
}
IMPLEMENTATION_FIELDS = {
    "prebuild_runner_sha256",
    "prebuild_executor_sha256",
    "enumerator_sha256",
    "enumerator_review_sha256",
}
INPUT_FIELDS = {
    "stage0_inventory_sha256",
    "base_lock_sha256",
    "derived_lock_sha256",
    "stage0_harness_manifest_sha256",
    "stage1_snapshot_sha256",
    "stage1_receipt_sha256",
    "stage1_response_sha256",
    "protocol_sha256",
    "estimator_authorization_sha256",
    "t111_binary_sha256",
    "typedcalloracle_binary_sha256",
    "heads",
}
PRIOR_AUTHORIZATION_FIELDS = {"estimator"}
PRIOR_ESTIMATOR_AUTHORIZATION_FIELDS = {"path", "sha256"}
ESTIMATOR_AUTHORIZATION_REL = "spike/t111/labeling/estimator-authorization.json"
TOOLCHAIN_FIELDS = {
    "python_executable",
    "python_version",
    "python_sha256",
    "git_executable",
    "git_sha256",
    "go_executable",
    "go_sha256",
    "producer_toolchain_identity",
}
ENVIRONMENT_FIELDS = {"hydration", "offline"}
PHASE_ENVIRONMENT_FIELDS = {"variables", "proxy", "sumdb", "scratch_root"}
PHASE_SCRATCH_VARIABLES = {
    "TMPDIR",
    "TMP",
    "TEMP",
    "HOME",
    "XDG_CONFIG_HOME",
    "XDG_CACHE_HOME",
    "XDG_DATA_HOME",
}
DERIVED_ROOT_FIELDS = {
    "root",
    "lock_path",
    "cache_path",
    "corpus_path",
    "out_path",
    "facts_root",
}
FACT_RUN_FIELDS = {"run_id", "path", "receipt_path", "commands", "publication"}
FACT_COMMAND_FIELDS = {"subcommand", "system"}
FACT_PUBLICATION_FIELDS = {
    "source_root",
    "destination_root",
    "reset_before_run",
    "copy_mode",
    "hardlink_policy",
    "reuse_policy",
    "files",
}
FACT_PUBLICATION_FILE_FIELDS = {"source_path", "destination_path"}
FACT_PUBLICATION_RESET_FIELDS = {"out_path", "destination_path"}
OPERATIONS_FIELDS = {
    "order",
    "source_clone",
    "lock_rewrite",
    "corpus",
    "cache",
    "hydrate",
    "fixture_finalize",
    "extract",
}
SOURCE_CLONE_FIELDS = {
    "source_repo_path",
    "source_commit",
    "destination_path",
    "sequence",
    "clone_argv",
    "normalization",
    "checkout_argv",
}
CORPUS_PLAN_FIELDS = {"order", "fixtures"}
CORPUS_FIXTURE_FIELDS = {
    "source_repo_path",
    "source_commit",
    "destination_path",
    "bundle_path",
    "sequence",
    "bundle_argv",
    "init_argv",
    "normalization",
    "fetch_argv",
    "checkout_argv",
}
LOCK_REWRITE_FIELDS = {
    "base_lock_path",
    "base_lock_access",
    "base_lock_sha256",
    "derived_lock_path",
    "derived_lock_sha256",
    "serialization",
    "transform",
}
LOCK_SERIALIZATION_FIELDS = {
    "encoding",
    "indent",
    "ensure_ascii",
    "sort_keys",
    "newline",
    "trailing_newline",
}
LOCK_TRANSFORM_FIELDS = {
    "kind",
    "fixtures",
    "preserve_array_order",
    "preserve_object_key_order",
    "preserve_non_commit_fields",
    "reject_missing_fixtures",
    "reject_duplicate_fixtures",
    "reject_extra_fixtures",
}
LOCK_REPLACEMENT_FIELDS = {"field", "source_commit", "target_commit"}
FIXTURE_FINALIZATION_PLAN_FIELDS = {"order", "fixtures"}
FIXTURE_FINALIZATION_FIELDS = {
    "repo_path",
    "config_path",
    "mode",
    "encoding",
    "newline",
    "trailing_newline",
    "only_core",
    "core_values",
    "remove_origin",
    "forbid_alternates",
    "forbid_hooks",
    "forbid_fsmonitor",
    "forbid_bare",
    "forbid_logallrefupdates",
    "after_hydration",
    "before_extract",
}
FINAL_CORE_ALLOWED_VALUES = {
    "repositoryformatversion": {"0"},
    "filemode": {"true", "false"},
    "ignorecase": {"true", "false"},
    "precomposeunicode": {"true", "false"},
    "symlinks": {"false"},
}
NORMALIZATION_FIELDS = {
    "core_hooks_path",
    "core_fsmonitor",
    "core_use_builtin_fsmonitor",
    "core_symlinks",
    "remove_origin",
    "forbid_alternates",
    "before_checkout",
}
CACHE_PLAN_FIELDS = {
    "path",
    "mode",
    "copy_from",
    "reset_before_hydration",
    "hardlink_policy",
    "reuse_policy",
}
HYDRATE_PLAN_FIELDS = {"order", "commands"}
HYDRATE_COMMAND_FIELDS = {"argv", "cwd", "capture"}
EXTRACT_PLAN_FIELDS = {"run1", "run2"}
EXTRACT_RUN_PLAN_FIELDS = {"argv", "cwd", "capture"}
CAPTURE_FIELDS = {"stdout_path", "stderr_path"}
STATE_FIELDS = {
    "ceremony_directory",
    "consumption_marker",
    "terminal_receipt",
    "evidence_receipt",
}
SCOPE_FIELDS = {
    "construct_derived_root",
    "hydrate_modules",
    "extract_facts",
    "enumerate_frames",
    "prepare_stage2",
    "select_samples",
    "disclose_coordinates",
}
IMPLEMENTATION_REVIEW_FIELDS = {"status", "accepted_commit", "record_sha256"}
IMPLEMENTATION_BINDING_FIELDS = {"status", "commit"}
RECORD_PROJECTIONS_FIELDS = {"evidence", "terminal"}
EVIDENCE_PROJECTION_FIELDS = {
    "schema",
    "status",
    "prebuild_id",
    "authorization_schema",
    "authorization_id",
    "record_path",
    "execution_root",
    "fact_runs",
}
TERMINAL_PROJECTION_FIELDS = {
    "schema",
    "prebuild_id",
    "authorization_schema",
    "authorization_id",
    "record_path",
    "execution_root",
    "fact_runs",
}
RECORD_PROJECTION_FACT_RUN_FIELDS = {"run_id", "root", "receipt_path"}

STATIC_ENVIRONMENT = {
    "GIT_ASKPASS": "/usr/bin/false",
    "GIT_ALLOW_PROTOCOL": "file",
    "GIT_CONFIG_COUNT": "3",
    "GIT_CONFIG_GLOBAL": "/dev/null",
    "GIT_CONFIG_KEY_0": "core.hooksPath",
    "GIT_CONFIG_KEY_1": "core.fsmonitor",
    "GIT_CONFIG_KEY_2": "core.useBuiltinFSMonitor",
    "GIT_CONFIG_NOSYSTEM": "1",
    "GIT_CONFIG_VALUE_0": "/dev/null",
    "GIT_CONFIG_VALUE_1": "false",
    "GIT_CONFIG_VALUE_2": "false",
    "GIT_NO_LAZY_FETCH": "1",
    "GIT_NO_REPLACE_OBJECTS": "1",
    "GIT_OPTIONAL_LOCKS": "0",
    "GIT_TERMINAL_PROMPT": "0",
    "GOENV": "off",
    "GOTOOLCHAIN": "local",
    "GOTELEMETRY": "off",
    "GOWORK": "off",
    "LANG": "C",
    "LC_ALL": "C",
    "PYTHONDONTWRITEBYTECODE": "1",
    "PYTHONHASHSEED": "0",
    "TZ": "UTC",
}
DERIVED_LOCK_RELATIVE = Path("spike/t111/corpus.lock.json")
DERIVED_CACHE_RELATIVE = Path("spike/t111/.module-cache")
DERIVED_CORPUS_RELATIVE = Path("spike/t111/corpus")
DERIVED_OUT_RELATIVE = Path("spike/t111/out")
DERIVED_FACTS_RELATIVE = Path("spike/t111/stage2-facts")
DERIVED_T111_RELATIVE = DERIVED_CACHE_RELATIVE / "bin" / "t111"
PREBUILD_EVIDENCE_SCHEMA = "t111-gate2-v2-stage2-prebuild-evidence-v1"
PREBUILD_EVIDENCE_RECEIPT_SCHEMA = "t111-gate2-v2-stage2-prebuild-evidence-receipt-v1"
PREBUILD_TERMINAL_SCHEMA = "t111-gate2-v2-stage2-prebuild-terminal-receipt-v1"
OPERATION_ORDER = [
    "source_clone",
    "lock_rewrite",
    "corpus",
    "cache",
    "hydrate",
    "fixture_finalize",
    "extract",
]
SOURCE_CLONE_SEQUENCE = ["clone", "normalize", "checkout"]
CORPUS_FIXTURE_SEQUENCE = ["bundle", "init", "normalize", "fetch", "checkout"]
TOOLCHAIN_IDENTITY_RE = re.compile(
    r'^go_version="([^"]+)";go_digest=(sha256:[0-9a-f]{64});'
    r'git_version="([^"]+)";git_digest=(sha256:[0-9a-f]{64})$'
)
EXPECTED_SCOPE = {
    "construct_derived_root": True,
    "hydrate_modules": True,
    "extract_facts": True,
    "enumerate_frames": False,
    "prepare_stage2": False,
    "select_samples": False,
    "disclose_coordinates": False,
}


class PrebuildError(Exception):
    """A non-disclosing prebuild-admission refusal."""


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def sha256_bytes(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def _sha256(value: Any, label: str) -> str:
    if not isinstance(value, str) or re.fullmatch(r"sha256:[0-9a-f]{64}", value) is None:
        raise PrebuildError(f"{label} is not a sha256 digest")
    return value


def _oid(value: Any, label: str) -> str:
    if not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{40}", value) is None:
        raise PrebuildError(f"{label} is not a full commit oid")
    return value


def _safe_text(value: Any) -> bool:
    """Accept non-empty text only when it cannot alter a command/path boundary."""
    return (
        isinstance(value, str)
        and bool(value)
        and not any(ord(character) < 0x20 or 0x7F <= ord(character) <= 0x9F for character in value)
    )


def _absolute(value: Any, label: str) -> Path:
    if not _safe_text(value):
        raise PrebuildError(f"{label} is not an absolute path")
    path = Path(value)
    if not path.is_absolute() or path.name in {"", ".", ".."} or ".." in path.parts:
        raise PrebuildError(f"{label} is not an absolute path")
    return path


def _relative(value: Any, label: str) -> Path:
    if not _safe_text(value):
        raise PrebuildError(f"{label} is not a safe relative path")
    path = Path(value)
    if path.is_absolute() or path.name in {"", ".", ".."} or ".." in path.parts:
        raise PrebuildError(f"{label} is not a safe relative path")
    return path


def _exact_boolean_scope(value: Any) -> bool:
    """Reject numeric JSON stand-ins for the P0 scope booleans."""
    return (
        isinstance(value, dict)
        and set(value) == set(EXPECTED_SCOPE)
        and all(type(value[name]) is bool and value[name] is permitted for name, permitted in EXPECTED_SCOPE.items())
    )


def sealed_path(go_executable: Path, git_executable: Path) -> str:
    """Return the only PATH a future phase may inherit.

    Parent directories are derived from P0's digest-bound executable paths,
    never from the caller's PATH.  Repeated directories are removed without
    changing first-use order.
    """
    directories: list[str] = []
    for directory in (str(git_executable.parent), str(go_executable.parent), "/usr/bin", "/bin"):
        if directory not in directories:
            directories.append(directory)
    return ":".join(directories)


def sealed_phase_environment(
    *,
    go_executable: Path,
    git_executable: Path,
    scratch_root: Path,
) -> dict[str, str]:
    """Return P0's complete child environment for one named phase.

    ``t111`` consults ``os.TempDir`` before it assembles any inner child
    environment.  The P0 envelope therefore owns every temp/home/XDG root;
    none may quietly inherit from the operator's login environment.
    """
    temporary = scratch_root / "tmp"
    return {
        "PATH": sealed_path(go_executable, git_executable),
        "GOROOT": str(go_executable.parent.parent),
        "TMPDIR": str(temporary),
        "TMP": str(temporary),
        "TEMP": str(temporary),
        "HOME": str(scratch_root / "home"),
        "XDG_CONFIG_HOME": str(scratch_root / "xdg-config"),
        "XDG_CACHE_HOME": str(scratch_root / "xdg-cache"),
        "XDG_DATA_HOME": str(scratch_root / "xdg-data"),
        **STATIC_ENVIRONMENT,
    }


def _validate_phase_environments(
    environment: dict[str, Any],
    *,
    ceremony: Path,
    go_executable: Path,
    git_executable: Path,
) -> dict[str, Path]:
    """Require separately-owned scratch namespaces for hydration/offline.

    The roots are deliberately declared in P0 rather than inferred from a
    platform temporary directory.  This keeps both Go's pre-child tempfile
    behavior and any Git/XDG lookup inside a private, reviewable namespace.
    """
    scratch_roots: dict[str, Path] = {}
    expected_keys = {"PATH", "GOROOT", *STATIC_ENVIRONMENT, *PHASE_SCRATCH_VARIABLES}
    for phase in ("hydration", "offline"):
        phase_environment = environment[phase]
        scratch = _absolute(phase_environment.get("scratch_root"), f"{phase} scratch root")
        expected_scratch = ceremony / "scratch" / phase
        if scratch != expected_scratch:
            raise PrebuildError(f"prebuild authorization has an invalid {phase} scratch root")
        variables = phase_environment.get("variables")
        if (
            not isinstance(variables, dict)
            or set(variables) != expected_keys
            or variables
            != sealed_phase_environment(
                go_executable=go_executable,
                git_executable=git_executable,
                scratch_root=scratch,
            )
        ):
            raise PrebuildError(f"prebuild authorization has an invalid {phase} variables binding")
        scratch_roots[phase] = scratch
    if _paths_overlap(scratch_roots["hydration"], scratch_roots["offline"]):
        raise PrebuildError("prebuild authorization reuses a phase scratch namespace")
    return scratch_roots


def _canonical_json(path: Path) -> tuple[Any, bytes]:
    # This is the only filesystem read performed before P0 validates.  Do not
    # add derived-root, cache, corpus, fact, or Stage-0/Stage-1 reads here.
    try:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise OSError("not a regular authorization file")
        raw = path.read_bytes()
        value = json.loads(raw.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PrebuildError("prebuild authorization is unavailable or invalid") from exc
    if raw != (canonical_json(value) + "\n").encode("utf-8"):
        raise PrebuildError("prebuild authorization is not canonical JSON")
    return value, raw


def _commands(value: Any) -> None:
    if not isinstance(value, dict) or set(value) != set(RECEIPT_FIXTURES):
        raise PrebuildError("prebuild authorization has an invalid fact command set")
    for fixture, command in value.items():
        if not isinstance(command, dict) or set(command) != FACT_COMMAND_FIELDS:
            raise PrebuildError("prebuild authorization has an invalid fact command")
        if command.get("subcommand") != "extract" or command.get("system") != fixture:
            raise PrebuildError("prebuild authorization expands the fact command scope")


def _fact_publication(
    value: Any,
    *,
    out_path: Path,
    run_path: Path,
    receipt_path: Path,
) -> None:
    """Bind each offline run to fresh byte copies of t111's fixed output.

    ``t111 extract`` has no output flag: it always writes
    ``spike/t111/out/<fixture>.facts.jsonl``.  The authorization must make
    both the reset and subsequent byte-copy handoff explicit, so a later
    executor cannot silently retain a prior run or hardlink its fact files.
    """
    if not isinstance(value, dict) or set(value) != FACT_PUBLICATION_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid fact publication plan")
    if _absolute(value.get("source_root"), "fact publication source root") != out_path:
        raise PrebuildError("fact publication source differs from t111's fixed output root")
    if _absolute(value.get("destination_root"), "fact publication destination root") != run_path:
        raise PrebuildError("fact publication destination differs from its fact-run root")
    reset = value.get("reset_before_run")
    if (
        not isinstance(reset, dict)
        or set(reset) != FACT_PUBLICATION_RESET_FIELDS
        or _absolute(reset.get("out_path"), "fact publication reset output") != out_path
        or _absolute(reset.get("destination_path"), "fact publication reset destination") != run_path
        or value.get("copy_mode") != "copy-regular-files"
        or value.get("hardlink_policy") != "forbid"
        or value.get("reuse_policy") != "forbid"
    ):
        raise PrebuildError("fact publication does not require a fresh non-hardlinked copy")
    files = value.get("files")
    if not isinstance(files, dict) or set(files) != set(RECEIPT_FIXTURES):
        raise PrebuildError("fact publication has an invalid fixture file set")
    for fixture in RECEIPT_FIXTURES:
        item = files[fixture]
        if not isinstance(item, dict) or set(item) != FACT_PUBLICATION_FILE_FIELDS:
            raise PrebuildError("fact publication has an invalid fixture copy")
        expected_source = out_path / f"{fixture}.facts.jsonl"
        expected_destination = run_path / f"{fixture}.facts.jsonl"
        if (
            _absolute(item.get("source_path"), "fact publication source file") != expected_source
            or _absolute(item.get("destination_path"), "fact publication destination file")
            != expected_destination
        ):
            raise PrebuildError("fact publication differs from the fixed t111 output handoff")
        if expected_destination == receipt_path:
            raise PrebuildError("fact publication collides with its fact-run receipt")


def _fact_runs(value: Any, facts_root: Path, out_path: Path) -> dict[str, Path]:
    if not isinstance(value, dict) or set(value) != {"run1", "run2"}:
        raise PrebuildError("prebuild authorization has an invalid fact-run plan")
    seen_ids: set[str] = set()
    seen_paths: set[Path] = set()
    seen_receipts: set[Path] = set()
    paths: dict[str, Path] = {}
    for run in ("run1", "run2"):
        item = value[run]
        if not isinstance(item, dict) or set(item) != FACT_RUN_FIELDS:
            raise PrebuildError("prebuild authorization has an invalid fact-run plan")
        run_id = item.get("run_id")
        if (
            not isinstance(run_id, str)
            or not 8 <= len(run_id) <= 128
            or not all(char.isascii() and (char.isalnum() or char in "._-") for char in run_id)
        ):
            raise PrebuildError("prebuild authorization has an invalid fact-run id")
        path = _absolute(item.get("path"), "fact-run path")
        try:
            path.relative_to(facts_root)
        except ValueError as exc:
            raise PrebuildError("fact-run path escapes the declared facts root") from exc
        if path == facts_root:
            raise PrebuildError("fact-run path collides with the facts root")
        if run_id in seen_ids or path in seen_paths:
            raise PrebuildError("prebuild authorization reuses a fact-run id or path")
        receipt_path = _absolute(item.get("receipt_path"), "fact-run receipt path")
        if receipt_path != path / "fact-run-receipt.json" or receipt_path in seen_receipts:
            raise PrebuildError("fact-run receipt differs from the fixed run path")
        seen_ids.add(run_id)
        seen_paths.add(path)
        seen_receipts.add(receipt_path)
        paths[run] = path
        _commands(item.get("commands"))
        _fact_publication(
            item.get("publication"),
            out_path=out_path,
            run_path=path,
            receipt_path=receipt_path,
        )
    return paths


def _path_within(path: Path, root: Path, label: str, *, permit_root: bool = False) -> None:
    try:
        path.relative_to(root)
    except ValueError as exc:
        raise PrebuildError(f"{label} escapes its declared root") from exc
    if not permit_root and path == root:
        raise PrebuildError(f"{label} collides with its declared root")


def _paths_overlap(first: Path, second: Path) -> bool:
    try:
        first.relative_to(second)
        return True
    except ValueError:
        try:
            second.relative_to(first)
            return True
        except ValueError:
            return False


def _argv(value: Any, label: str) -> list[str]:
    if not isinstance(value, list) or not value:
        raise PrebuildError(f"{label} has an invalid argv")
    if not all(_safe_text(item) for item in value):
        raise PrebuildError(f"{label} has an invalid argv")
    return value


def _normalization(value: Any, label: str) -> None:
    """Require local clone normalization before any checkout can occur."""
    expected = {
        "core_hooks_path": "/dev/null",
        "core_fsmonitor": "false",
        "core_use_builtin_fsmonitor": "false",
        "core_symlinks": "false",
        "remove_origin": True,
        "forbid_alternates": True,
        "before_checkout": True,
    }
    if (
        not isinstance(value, dict)
        or set(value) != NORMALIZATION_FIELDS
        or value != expected
        or any(type(value[field]) is not bool for field in ("remove_origin", "forbid_alternates", "before_checkout"))
    ):
        raise PrebuildError(f"{label} lacks the fixed clone normalization")


def _source_clone_plan(
    value: Any,
    *,
    root: Path,
    ceremony: Path,
    git: Path,
) -> tuple[Path, str]:
    """Validate a real independent clone, never a COW/worktree copy."""
    if not isinstance(value, dict) or set(value) != SOURCE_CLONE_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid source-clone plan")
    source = _absolute(value.get("source_repo_path"), "source repository path")
    # The control checkout is the only source P0 may clone.  Allowing an
    # arbitrary sibling repository would let a later authorization bind a
    # differently-shaped control history than the committed P0/PLAN chain.
    # Keep this lexical: admission is deliberately read-only and must not
    # resolve or probe a future source tree.
    if source != REPO_ROOT:
        raise PrebuildError("source repository differs from the canonical control repository")
    source_commit = _oid(value.get("source_commit"), "source commit")
    if _absolute(value.get("destination_path"), "source-clone destination") != root:
        raise PrebuildError("source-clone destination differs from the derived root")
    if _paths_overlap(source, root) or _paths_overlap(source, ceremony):
        raise PrebuildError("source repository overlaps a writable P0 namespace")
    if value.get("sequence") != SOURCE_CLONE_SEQUENCE:
        raise PrebuildError("source-clone sequence is not normalized before checkout")
    expected_clone = [
        str(git),
        "clone",
        "--no-checkout",
        "--no-local",
        "--no-hardlinks",
        "--no-recurse-submodules",
        "--no-tags",
        "--no-single-branch",
        str(source),
        str(root),
    ]
    if _argv(value.get("clone_argv"), "source clone") != expected_clone:
        raise PrebuildError("source-clone argv permits a shared or COW checkout")
    _normalization(value.get("normalization"), "source clone")
    expected_checkout = [str(git), "-C", str(root), "checkout", "--detach", source_commit]
    if _argv(value.get("checkout_argv"), "source clone checkout") != expected_checkout:
        raise PrebuildError("source-clone checkout differs from its sealed commit")
    return source, source_commit


def _bootstrap_binding(
    value: dict[str, Any],
    *,
    source_repo: Path,
    root: Path,
    inputs: dict[str, Any],
) -> tuple[str, str]:
    """Bind the pre-hydration source binary separately from the derived one.

    A fresh derived cache cannot contain ``t111`` before hydration.  Hydration
    therefore runs the sealed Stage-0 binary directly from the read-only
    source tree, with the *derived* checkout as its cwd.  A future executor
    must first lstat it as a regular non-symlink and verify its digest before
    hydration. Hydration then builds the equally digest-bound binary in the
    fresh derived cache; only that derived binary may run extraction. Neither
    cache nor binary is copied.
    """
    source_t111 = _absolute(value.get("source_t111_path"), "source bootstrap t111 path")
    derived_t111 = _absolute(value.get("derived_t111_path"), "derived t111 path")
    if source_t111 != source_repo / DERIVED_T111_RELATIVE:
        raise PrebuildError("source bootstrap t111 differs from the read-only Stage-0 source path")
    if derived_t111 != root / DERIVED_T111_RELATIVE:
        raise PrebuildError("derived t111 differs from the planned fresh cache path")
    if source_t111 == derived_t111:
        raise PrebuildError("source bootstrap and derived extraction t111 must be distinct paths")
    if (
        value.get("source_t111_access") != "read-only"
        or value.get("binary_copy_policy") != "forbid"
        or value.get("source_t111_sha256") != inputs["t111_binary_sha256"]
        or value.get("derived_t111_sha256") != inputs["t111_binary_sha256"]
    ):
        raise PrebuildError("bootstrap binding permits a copied or unbound t111 binary")
    return str(source_t111), str(derived_t111)


def _lock_rewrite_plan(
    value: Any,
    *,
    source_repo: Path,
    root: Path,
    inputs: dict[str, Any],
    heads: dict[str, Any],
) -> None:
    """Bind the one deterministic lock derivation before any corpus action.

    The source lock remains a read-only Stage-0 input.  A future executor must
    hash it, parse it as the locked JSON array, require exactly one entry for
    every named replacement, change only those four ``commit`` values, compare
    every other parsed value and ordering unchanged, write the declared UTF-8
    serialization to the derived checkout, and require the declared expected
    digest.  The Go binary then sees the derived lock through its cwd; it
    never needs the source lock rewritten.
    """
    if not isinstance(value, dict) or set(value) != LOCK_REWRITE_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid lock-rewrite plan")
    base_lock = _absolute(value.get("base_lock_path"), "base lock path")
    derived_lock = _absolute(value.get("derived_lock_path"), "derived lock path")
    if base_lock != source_repo / DERIVED_LOCK_RELATIVE:
        raise PrebuildError("lock rewrite base path differs from the read-only source lock")
    if derived_lock != root / DERIVED_LOCK_RELATIVE:
        raise PrebuildError("lock rewrite destination differs from the derived lock path")
    if base_lock == derived_lock or value.get("base_lock_access") != "read-only":
        raise PrebuildError("lock rewrite does not preserve the source lock as read-only")
    base_digest = _sha256(value.get("base_lock_sha256"), "lock rewrite base digest")
    derived_digest = _sha256(value.get("derived_lock_sha256"), "lock rewrite derived digest")
    if (
        base_digest != inputs["base_lock_sha256"]
        or derived_digest != inputs["derived_lock_sha256"]
        or derived_digest == base_digest
    ):
        raise PrebuildError("lock rewrite digest binding is invalid")
    serialization = value.get("serialization")
    expected_serialization = {
        "encoding": "utf-8",
        "indent": 2,
        "ensure_ascii": False,
        "sort_keys": False,
        "newline": "LF",
        "trailing_newline": True,
    }
    if (
        not isinstance(serialization, dict)
        or set(serialization) != LOCK_SERIALIZATION_FIELDS
        or serialization != expected_serialization
        or type(serialization.get("indent")) is not int
        or any(type(serialization[field]) is not bool for field in ("ensure_ascii", "sort_keys", "trailing_newline"))
    ):
        raise PrebuildError("lock rewrite serialization is not fixed UTF-8 indent-2 LF")
    transform = value.get("transform")
    if not isinstance(transform, dict) or set(transform) != LOCK_TRANSFORM_FIELDS:
        raise PrebuildError("lock rewrite transform is invalid")
    required_flags = {
        "preserve_array_order": True,
        "preserve_object_key_order": True,
        "preserve_non_commit_fields": True,
        "reject_missing_fixtures": True,
        "reject_duplicate_fixtures": True,
        "reject_extra_fixtures": True,
    }
    if (
        transform.get("kind") != "replace-fixture-commit-only"
        or any(transform.get(field) is not expected for field, expected in required_flags.items())
    ):
        raise PrebuildError("lock rewrite transform expands beyond commit-only replacement")
    replacements = transform.get("fixtures")
    if not isinstance(replacements, dict) or set(replacements) != set(RECEIPT_FIXTURES):
        raise PrebuildError("lock rewrite transform does not name exactly four fixtures")
    for fixture in RECEIPT_FIXTURES:
        replacement = replacements[fixture]
        if not isinstance(replacement, dict) or set(replacement) != LOCK_REPLACEMENT_FIELDS:
            raise PrebuildError("lock rewrite has an invalid fixture replacement")
        if (
            replacement.get("field") != "commit"
            or replacement.get("source_commit") != BASE_LOCK_FIXTURE_COMMITS[fixture]
            or replacement.get("target_commit") != heads[fixture]
        ):
            raise PrebuildError("lock rewrite fixture replacement does not match the sealed heads")


def _corpus_plan(
    value: Any,
    *,
    source_repo: Path,
    root: Path,
    ceremony: Path,
    git: Path,
    heads: dict[str, Any],
) -> set[Path]:
    """Validate local bundle/init/fetch/checkout for all sealed fixture heads.

    The sealed Stage-1 object may not have a retaining local ref.  Building a
    bundle directly from its full oid makes the object closure explicit and
    avoids both a hidden network fallback and a shared Git object store.
    """
    if not isinstance(value, dict) or set(value) != CORPUS_PLAN_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid corpus-clone plan")
    if value.get("order") != list(RECEIPT_FIXTURES):
        raise PrebuildError("corpus-clone plan does not use the four-fixture order")
    fixtures = value.get("fixtures")
    if not isinstance(fixtures, dict) or set(fixtures) != set(RECEIPT_FIXTURES):
        raise PrebuildError("corpus-clone plan has an invalid fixture set")
    bundles: set[Path] = set()
    for fixture in RECEIPT_FIXTURES:
        plan = fixtures[fixture]
        if not isinstance(plan, dict) or set(plan) != CORPUS_FIXTURE_FIELDS:
            raise PrebuildError("corpus-clone plan has an invalid fixture recipe")
        source = _absolute(plan.get("source_repo_path"), "corpus source mirror path")
        expected_source = source_repo / DERIVED_CORPUS_RELATIVE / fixture
        if source != expected_source:
            raise PrebuildError("corpus source mirror differs from the bound source repository")
        if _paths_overlap(source, root) or _paths_overlap(source, ceremony):
            raise PrebuildError("corpus source mirror overlaps a writable P0 namespace")
        commit = _oid(plan.get("source_commit"), "corpus source commit")
        if commit != heads[fixture]:
            raise PrebuildError("corpus source commit differs from the sealed Stage-1 head")
        destination = _absolute(plan.get("destination_path"), "corpus destination path")
        if destination != root / DERIVED_CORPUS_RELATIVE / fixture:
            raise PrebuildError("corpus destination differs from the fixed derived topology")
        bundle = _absolute(plan.get("bundle_path"), "corpus bundle path")
        if bundle != ceremony / "bundles" / f"{fixture}.bundle" or bundle in bundles:
            raise PrebuildError("corpus bundle path differs from the fixed fixture path")
        bundles.add(bundle)
        if plan.get("sequence") != CORPUS_FIXTURE_SEQUENCE:
            raise PrebuildError("corpus fixture recipe does not normalize before checkout")
        if _argv(plan.get("bundle_argv"), "corpus bundle") != [
            str(git), "-C", str(source), "bundle", "create", str(bundle), commit,
        ]:
            raise PrebuildError("corpus bundle argv differs from the sealed local bundle recipe")
        if _argv(plan.get("init_argv"), "corpus init") != [
            str(git), "init", "--quiet", str(destination),
        ]:
            raise PrebuildError("corpus init argv differs from the sealed local recipe")
        _normalization(plan.get("normalization"), "corpus fixture")
        ref = f"refs/gate2-v2/{fixture}"
        if _argv(plan.get("fetch_argv"), "corpus fetch") != [
            str(git), "-C", str(destination), "fetch", "--no-tags", str(bundle), f"{commit}:{ref}",
        ]:
            raise PrebuildError("corpus fetch argv differs from the sealed local bundle recipe")
        if _argv(plan.get("checkout_argv"), "corpus checkout") != [
            str(git), "-C", str(destination), "checkout", "--detach", commit,
        ]:
            raise PrebuildError("corpus checkout argv differs from the sealed fixture head")
    return bundles


def _fixture_finalization_plan(value: Any, *, root: Path) -> None:
    """Require enum-safe, core-only fixture configs after hydration.

    Checkout normalization may install temporary hardening controls.  Before
    extraction, a future executor must rewrite and parse each generated
    ``.git/config`` so the final file contains only this explicitly authorized
    core map and no remote, hook, fsmonitor, alternates, or other directives.
    """
    if not isinstance(value, dict) or set(value) != FIXTURE_FINALIZATION_PLAN_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid fixture-finalization plan")
    if value.get("order") != list(RECEIPT_FIXTURES):
        raise PrebuildError("fixture finalization does not use the four-fixture order")
    fixtures = value.get("fixtures")
    if not isinstance(fixtures, dict) or set(fixtures) != set(RECEIPT_FIXTURES):
        raise PrebuildError("fixture finalization has an invalid fixture set")
    required_booleans = (
        "only_core",
        "remove_origin",
        "forbid_alternates",
        "forbid_hooks",
        "forbid_fsmonitor",
        "forbid_bare",
        "forbid_logallrefupdates",
        "after_hydration",
        "before_extract",
    )
    for fixture in RECEIPT_FIXTURES:
        plan = fixtures[fixture]
        if not isinstance(plan, dict) or set(plan) != FIXTURE_FINALIZATION_FIELDS:
            raise PrebuildError("fixture finalization has an invalid fixture plan")
        repo = _absolute(plan.get("repo_path"), "fixture finalization repository")
        expected_repo = root / DERIVED_CORPUS_RELATIVE / fixture
        if repo != expected_repo:
            raise PrebuildError("fixture finalization repository differs from the derived corpus path")
        if _absolute(plan.get("config_path"), "fixture final config path") != repo / ".git" / "config":
            raise PrebuildError("fixture final config path differs from the generated repository config")
        if (
            plan.get("mode") != "minimal-core-only"
            or plan.get("encoding") != "ascii"
            or plan.get("newline") != "LF"
            or plan.get("trailing_newline") is not True
            or any(plan.get(field) is not True for field in required_booleans)
        ):
            raise PrebuildError("fixture finalization does not require an enum-safe core-only config")
        core_values = plan.get("core_values")
        if (
            not isinstance(core_values, dict)
            or not {"repositoryformatversion", "symlinks"} <= set(core_values)
            or not set(core_values) <= set(FINAL_CORE_ALLOWED_VALUES)
            or any(
                not isinstance(core_values[field], str)
                or core_values[field] not in FINAL_CORE_ALLOWED_VALUES[field]
                for field in core_values
            )
        ):
            raise PrebuildError("fixture finalization has an unsafe core config closure")


def _cache_plan(value: Any, cache_path: Path) -> None:
    if not isinstance(value, dict) or set(value) != CACHE_PLAN_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid cache plan")
    if (
        _absolute(value.get("path"), "fresh cache path") != cache_path
        or value.get("mode") != "fresh-empty"
        or value.get("copy_from") is not None
        or value.get("reset_before_hydration") is not True
        or value.get("hardlink_policy") != "forbid"
        or value.get("reuse_policy") != "forbid"
    ):
        raise PrebuildError("prebuild authorization does not require a fresh derived module cache")


def _capture(value: Any, root: Path, label: str) -> tuple[Path, Path]:
    if not isinstance(value, dict) or set(value) != CAPTURE_FIELDS:
        raise PrebuildError(f"{label} has an invalid capture plan")
    paths = tuple(_absolute(value[field], f"{label} {field}") for field in ("stdout_path", "stderr_path"))
    if paths[0] == paths[1]:
        raise PrebuildError(f"{label} reuses one capture path")
    for path in paths:
        _path_within(path, root, label)
    return paths


def _hydrate_plan(
    value: Any,
    source_t111: str,
    root: Path,
    ceremony: Path,
    hydration_environment: dict[str, Any],
) -> set[Path]:
    if not isinstance(value, dict) or set(value) != HYDRATE_PLAN_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid hydration plan")
    if value.get("order") != list(RECEIPT_FIXTURES):
        raise PrebuildError("hydration plan does not use the four-fixture order")
    commands = value.get("commands")
    if not isinstance(commands, dict) or set(commands) != set(RECEIPT_FIXTURES):
        raise PrebuildError("hydration plan has an invalid fixture command mapping")
    captures: set[Path] = set()
    for fixture in RECEIPT_FIXTURES:
        command = commands[fixture]
        if not isinstance(command, dict) or set(command) != HYDRATE_COMMAND_FIELDS:
            raise PrebuildError("hydration plan has an invalid fixture command")
        argv = _argv(command.get("argv"), "hydration")
        expected_argv = [
            source_t111,
            "hydrate",
            "-system",
            fixture,
            "-proxy",
            hydration_environment["proxy"],
            "-sumdb",
            hydration_environment["sumdb"],
        ]
        if argv != expected_argv:
            raise PrebuildError("hydration argv expands the four-fixture plan")
        if _absolute(command.get("cwd"), "hydration cwd") != root:
            raise PrebuildError("hydration cwd differs from the derived root")
        captures.update(_capture(command.get("capture"), ceremony / "hydrate" / fixture, "hydration capture"))
        if command["capture"] != {
            "stdout_path": str(ceremony / "hydrate" / fixture / "stdout"),
            "stderr_path": str(ceremony / "hydrate" / fixture / "stderr"),
        }:
            raise PrebuildError("hydration capture differs from the fixed fixture path")
    return captures


def _extract_plan(
    value: Any,
    derived_t111: str,
    root: Path,
    run_paths: dict[str, Path],
) -> set[Path]:
    if not isinstance(value, dict) or set(value) != EXTRACT_PLAN_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid extraction plan")
    captures: set[Path] = set()
    for run in ("run1", "run2"):
        plan = value[run]
        if not isinstance(plan, dict) or set(plan) != EXTRACT_RUN_PLAN_FIELDS:
            raise PrebuildError("prebuild authorization has an invalid extraction plan")
        if _absolute(plan.get("cwd"), "extraction cwd") != root:
            raise PrebuildError("extraction cwd differs from the derived root")
        argv = plan.get("argv")
        if not isinstance(argv, dict) or set(argv) != set(RECEIPT_FIXTURES):
            raise PrebuildError("extraction plan has an invalid argv set")
        for fixture in RECEIPT_FIXTURES:
            command = _argv(argv[fixture], "extraction")
            if command != [derived_t111, "extract", "-system", fixture]:
                raise PrebuildError("extraction argv expands the authorized command")
        captures.update(_capture(plan.get("capture"), run_paths[run], "extraction capture"))
        if plan["capture"] != {
            "stdout_path": str(run_paths[run] / "extract.stdout"),
            "stderr_path": str(run_paths[run] / "extract.stderr"),
        }:
            raise PrebuildError("extraction capture differs from the fixed run path")
    return captures


def _record_projections(
    value: Any,
    *,
    authorization_id: str,
    root: Path,
    state_targets: dict[str, Path],
    fact_runs: dict[str, Any],
    run_paths: dict[str, Path],
) -> None:
    """Bind only the static links a later P0 evidence/terminal record needs.

    The authorization's own byte digest and authorizing Git commit are omitted
    intentionally: they can be derived only after this canonical document is
    written and committed.  Embedding either here would reintroduce the hash
    fixed point this admission layer is meant to avoid.
    """
    if not isinstance(value, dict) or set(value) != RECORD_PROJECTIONS_FIELDS:
        raise PrebuildError("prebuild authorization has invalid record projections")
    expected_runs = {
        run: {
            "run_id": fact_runs[run]["run_id"],
            "root": str(run_paths[run]),
            "receipt_path": fact_runs[run]["receipt_path"],
        }
        for run in ("run1", "run2")
    }
    evidence = value.get("evidence")
    expected_evidence = {
        "schema": PREBUILD_EVIDENCE_RECEIPT_SCHEMA,
        "status": "COMPLETE",
        "prebuild_id": authorization_id,
        "authorization_schema": AUTH_SCHEMA,
        "authorization_id": authorization_id,
        "record_path": str(state_targets["evidence_receipt"]),
        "execution_root": str(root),
        "fact_runs": expected_runs,
    }
    if (
        not isinstance(evidence, dict)
        or set(evidence) != EVIDENCE_PROJECTION_FIELDS
        or not isinstance(evidence.get("fact_runs"), dict)
        or set(evidence["fact_runs"]) != {"run1", "run2"}
        or any(
            not isinstance(evidence["fact_runs"][run], dict)
            or set(evidence["fact_runs"][run]) != RECORD_PROJECTION_FACT_RUN_FIELDS
            for run in ("run1", "run2")
        )
        or evidence != expected_evidence
    ):
        raise PrebuildError("prebuild authorization has an invalid evidence projection")
    terminal = value.get("terminal")
    expected_terminal = {
        "schema": PREBUILD_TERMINAL_SCHEMA,
        "prebuild_id": authorization_id,
        "authorization_schema": AUTH_SCHEMA,
        "authorization_id": authorization_id,
        "record_path": str(state_targets["terminal_receipt"]),
        "execution_root": str(root),
        "fact_runs": expected_runs,
    }
    if (
        not isinstance(terminal, dict)
        or set(terminal) != TERMINAL_PROJECTION_FIELDS
        or not isinstance(terminal.get("fact_runs"), dict)
        or set(terminal["fact_runs"]) != {"run1", "run2"}
        or any(
            not isinstance(terminal["fact_runs"][run], dict)
            or set(terminal["fact_runs"][run]) != RECORD_PROJECTION_FACT_RUN_FIELDS
            for run in ("run1", "run2")
        )
        or terminal != expected_terminal
    ):
        raise PrebuildError("prebuild authorization has an invalid terminal projection")


def load_authorization(path: Path | None = None) -> tuple[dict[str, Any], bytes]:
    """Parse P0 without touching any bound source or live derived input."""
    value, raw = _canonical_json(AUTHORIZATION_PATH if path is None else path)
    if not isinstance(value, dict) or set(value) != AUTHORIZATION_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid schema")
    if value.get("schema") != AUTH_SCHEMA or value.get("status") != "AUTHORIZED":
        raise PrebuildError("prebuild authorization is not authorized")
    if (
        not _safe_text(value.get("authorization_id"))
    ):
        raise PrebuildError("prebuild authorization has no identifier")

    implementation = value["implementation"]
    if not isinstance(implementation, dict) or set(implementation) != IMPLEMENTATION_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid implementation binding")
    for field in IMPLEMENTATION_FIELDS:
        _sha256(implementation.get(field), f"implementation {field}")

    bootstrap = value["bootstrap"]
    if not isinstance(bootstrap, dict) or set(bootstrap) != BOOTSTRAP_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid bootstrap binding")
    for field in ("source_t111_path", "derived_t111_path"):
        if _absolute(bootstrap.get(field), field).name != "t111":
            raise PrebuildError("bootstrap executable is not named t111")
    for field in ("source_t111_sha256", "derived_t111_sha256"):
        _sha256(bootstrap.get(field), field)

    inputs = value["inputs"]
    if not isinstance(inputs, dict) or set(inputs) != INPUT_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid input binding")
    for field in INPUT_FIELDS - {"heads"}:
        _sha256(inputs.get(field), f"input {field}")
    heads = inputs["heads"]
    if not isinstance(heads, dict) or set(heads) != set(RECEIPT_FIXTURES):
        raise PrebuildError("prebuild authorization has an invalid Stage-1 head set")
    for fixture in RECEIPT_FIXTURES:
        _oid(heads[fixture], f"head {fixture}")

    prior_authorizations = value["prior_authorizations"]
    if (
        not isinstance(prior_authorizations, dict)
        or set(prior_authorizations) != PRIOR_AUTHORIZATION_FIELDS
        or not isinstance(prior_authorizations["estimator"], dict)
        or set(prior_authorizations["estimator"])
        != PRIOR_ESTIMATOR_AUTHORIZATION_FIELDS
        or prior_authorizations["estimator"].get("path")
        != ESTIMATOR_AUTHORIZATION_REL
        or prior_authorizations["estimator"].get("sha256")
        != inputs["estimator_authorization_sha256"]
    ):
        raise PrebuildError("prebuild authorization has an invalid prior ceremony binding")
    _sha256(
        prior_authorizations["estimator"].get("sha256"),
        "prior estimator authorization digest",
    )

    toolchain = value["toolchain"]
    if not isinstance(toolchain, dict) or set(toolchain) != TOOLCHAIN_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid toolchain binding")
    executable_paths: dict[str, Path] = {}
    for path_field, basename in (("python_executable", "python3"), ("git_executable", "git"), ("go_executable", "go")):
        executable = _absolute(toolchain.get(path_field), path_field)
        if executable.name != basename:
            raise PrebuildError(f"prebuild authorization has an invalid {path_field}")
        executable_paths[path_field] = executable
    for field in ("python_sha256", "git_sha256", "go_sha256"):
        _sha256(toolchain.get(field), field)
    for field in ("python_version", "producer_toolchain_identity"):
        if not _safe_text(toolchain.get(field)):
            raise PrebuildError(f"prebuild authorization has an invalid {field}")
    identity = TOOLCHAIN_IDENTITY_RE.fullmatch(toolchain["producer_toolchain_identity"])
    if identity is None:
        raise PrebuildError("prebuild authorization has an invalid producer toolchain identity")
    _go_version, identity_go_digest, _git_version, identity_git_digest = identity.groups()
    if identity_go_digest != toolchain["go_sha256"] or identity_git_digest != toolchain["git_sha256"]:
        raise PrebuildError("producer toolchain identity does not match bound executable digests")

    environment = value["environment"]
    if not isinstance(environment, dict) or set(environment) != ENVIRONMENT_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid environment binding")
    for phase in ("hydration", "offline"):
        phase_environment = environment[phase]
        if not isinstance(phase_environment, dict) or set(phase_environment) != PHASE_ENVIRONMENT_FIELDS:
            raise PrebuildError(f"prebuild authorization has an invalid {phase} environment")
    hydration = environment["hydration"]
    if hydration.get("proxy") != "https://proxy.golang.org" or hydration.get("sumdb") != "sum.golang.org":
        raise PrebuildError("hydration environment is not allowlisted")
    offline = environment["offline"]
    if offline.get("proxy") != "off" or offline.get("sumdb") != "off":
        raise PrebuildError("offline environment is not fail-closed")

    derived = value["derived_root"]
    if not isinstance(derived, dict) or set(derived) != DERIVED_ROOT_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid derived-root plan")
    root = _absolute(derived.get("root"), "derived root")
    topology = {
        field: _absolute(derived.get(field), field)
        for field in ("lock_path", "cache_path", "corpus_path", "out_path", "facts_root")
    }
    for field, path in topology.items():
        _path_within(path, root, field)
    expected_topology = {
        "lock_path": root / DERIVED_LOCK_RELATIVE,
        "cache_path": root / DERIVED_CACHE_RELATIVE,
        "corpus_path": root / DERIVED_CORPUS_RELATIVE,
        "out_path": root / DERIVED_OUT_RELATIVE,
        "facts_root": root / DERIVED_FACTS_RELATIVE,
    }
    if topology != expected_topology:
        raise PrebuildError("derived-root topology differs from the fixed P0 layout")
    if len(set(topology.values())) != len(topology):
        raise PrebuildError("derived-root topology reuses a fixed path")
    topology_paths = list(topology.values())
    if any(
        _paths_overlap(first, second)
        for index, first in enumerate(topology_paths)
        for second in topology_paths[index + 1 :]
    ):
        raise PrebuildError("derived-root topology has overlapping fixed paths")
    run_paths = _fact_runs(value["fact_runs"], topology["facts_root"], topology["out_path"])
    if run_paths != {
        "run1": topology["facts_root"] / "run1",
        "run2": topology["facts_root"] / "run2",
    }:
        raise PrebuildError("fact-run paths differ from the fixed P0 layout")
    if _paths_overlap(run_paths["run1"], run_paths["run2"]):
        raise PrebuildError("prebuild authorization has overlapping fact-run paths")

    state = value["state"]
    if not isinstance(state, dict) or set(state) != STATE_FIELDS:
        raise PrebuildError("prebuild authorization has invalid state paths")
    ceremony = _absolute(state.get("ceremony_directory"), "ceremony directory")
    if _paths_overlap(ceremony, root):
        raise PrebuildError("ceremony directory overlaps the derived root")
    state_targets = {
        field: _absolute(state.get(field), field)
        for field in ("consumption_marker", "terminal_receipt", "evidence_receipt")
    }
    if len(set(state_targets.values())) != len(state_targets):
        raise PrebuildError("prebuild authorization reuses a terminal state path")
    state_paths = list(state_targets.values())
    if any(
        _paths_overlap(first, second)
        for index, first in enumerate(state_paths)
        for second in state_paths[index + 1 :]
    ):
        raise PrebuildError("prebuild authorization has overlapping terminal state paths")
    for field, path in state_targets.items():
        _path_within(path, ceremony, field)
        if (
            path.parent != ceremony
            or not path.name.isascii()
            or not path.name
            or any(not (character.isalnum() or character in "._-") for character in path.name)
        ):
            raise PrebuildError("prebuild authorization has an unsafe terminal state leaf")

    scratch_roots = _validate_phase_environments(
        environment,
        ceremony=ceremony,
        go_executable=executable_paths["go_executable"],
        git_executable=executable_paths["git_executable"],
    )
    if any(
        _paths_overlap(scratch_root, state_path)
        for scratch_root in scratch_roots.values()
        for state_path in state_targets.values()
    ):
        raise PrebuildError("prebuild authorization overlaps phase scratch with terminal state")

    _record_projections(
        value["record_projections"],
        authorization_id=value["authorization_id"],
        root=root,
        state_targets=state_targets,
        fact_runs=value["fact_runs"],
        run_paths=run_paths,
    )

    operations = value["operations"]
    if not isinstance(operations, dict) or set(operations) != OPERATIONS_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid operation plan")
    if operations.get("order") != OPERATION_ORDER:
        raise PrebuildError("prebuild authorization has an invalid operation order")
    source_repo, source_commit = _source_clone_plan(
        operations["source_clone"],
        root=root,
        ceremony=ceremony,
        git=executable_paths["git_executable"],
    )
    _lock_rewrite_plan(
        operations["lock_rewrite"],
        source_repo=source_repo,
        root=root,
        inputs=inputs,
        heads=heads,
    )
    source_t111, derived_t111 = _bootstrap_binding(
        bootstrap,
        source_repo=source_repo,
        root=root,
        inputs=inputs,
    )
    bundle_paths = _corpus_plan(
        operations["corpus"],
        source_repo=source_repo,
        root=root,
        ceremony=ceremony,
        git=executable_paths["git_executable"],
        heads=heads,
    )
    _cache_plan(operations["cache"], topology["cache_path"])
    capture_paths = _hydrate_plan(operations["hydrate"], source_t111, root, ceremony, hydration)
    _fixture_finalization_plan(operations["fixture_finalize"], root=root)
    capture_paths.update(_extract_plan(operations["extract"], derived_t111, root, run_paths))
    if any(
        _paths_overlap(state_path, reserved_path)
        for state_path in state_targets.values()
        for reserved_path in capture_paths | bundle_paths
    ):
        raise PrebuildError("prebuild authorization overlaps terminal state with operation capture")

    if not _exact_boolean_scope(value.get("scope")):
        raise PrebuildError("prebuild authorization has an expanded scope")

    # These fields bind a *prior implementation review*.  They deliberately
    # do not claim to approve this P0 document: doing so in P0 would create a
    # self-referential commit/digest loop.  A later committed-PLAN gate will
    # bind P0's own bytes separately.
    review = value["implementation_review"]
    binding = value["implementation_binding"]
    if (
        not isinstance(review, dict)
        or set(review) != IMPLEMENTATION_REVIEW_FIELDS
        or review.get("status") != "accepted"
        or not isinstance(binding, dict)
        or set(binding) != IMPLEMENTATION_BINDING_FIELDS
        or binding.get("status") != "executable"
    ):
        raise PrebuildError("prebuild authorization lacks a prior implementation binding")
    accepted = _oid(review.get("accepted_commit"), "review accepted commit")
    if binding.get("commit") != accepted:
        raise PrebuildError("implementation binding does not match the accepted review")
    if source_commit != accepted:
        raise PrebuildError("source-clone commit does not match the accepted implementation binding")
    _sha256(review.get("record_sha256"), "review record digest")
    return value, raw


def run_admission() -> dict[str, str]:
    """Parse P0 and stop; this must remain before any live input access."""
    authorization, raw = load_authorization()
    return {
        "schema": ADMISSION_SCHEMA,
        "status": "P0_PARSED",
        "authorization_sha256": sha256_bytes(raw),
        "authorization_id": authorization["authorization_id"],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--admission-only", action="store_true")
    args = parser.parse_args()
    if not args.admission_only:
        print("stage2-prebuild: refusing — admission-only interface", file=sys.stderr)
        return 2
    try:
        print(canonical_json(run_admission()))
        return 0
    except PrebuildError as exc:
        print(f"stage2-prebuild: refusing — {exc}", file=sys.stderr)
        return 2
    except Exception as exc:
        print(f"stage2-prebuild: unexpected failure: {type(exc).__name__}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    raise SystemExit(main())
