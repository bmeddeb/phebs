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
AUTHORIZATION_PATH = HERE / "stage2-prebuild-authorization.json"

AUTH_SCHEMA = "t111-gate2-v2-stage2-prebuild-authorization-v1"
ADMISSION_SCHEMA = "t111-gate2-v2-stage2-prebuild-admission-v1"
RECEIPT_FIXTURES = ("temporal", "dapr", "loki", "online-boutique")

# P0 binds every value known before a derived tree/cache/fact exists.  The
# actual digests produced by prebuild deliberately do not appear here; they
# belong to a later evidence record and final enumeration authorization.
AUTHORIZATION_FIELDS = {
    "schema",
    "status",
    "authorization_id",
    "implementation",
    "bootstrap",
    "inputs",
    "toolchain",
    "environment",
    "derived_root",
    "fact_runs",
    "operations",
    "state",
    "scope",
    "implementation_review",
    "implementation_binding",
}
BOOTSTRAP_FIELDS = {"t111_path", "t111_sha256"}
IMPLEMENTATION_FIELDS = {
    "prebuild_runner_sha256",
    "enumerator_sha256",
    "enumerator_review_sha256",
}
INPUT_FIELDS = {
    "stage0_inventory_sha256",
    "base_lock_sha256",
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
PHASE_ENVIRONMENT_FIELDS = {"variables", "proxy", "sumdb"}
DERIVED_ROOT_FIELDS = {"root", "lock_path", "cache_path", "corpus_path", "facts_root"}
FACT_RUN_FIELDS = {"run_id", "path", "commands"}
FACT_COMMAND_FIELDS = {"subcommand", "system"}
OPERATIONS_FIELDS = {"hydrate", "extract"}
HYDRATE_PLAN_FIELDS = {"order", "commands", "cow"}
HYDRATE_COMMAND_FIELDS = {"argv", "cwd", "capture"}
EXTRACT_PLAN_FIELDS = {"run1", "run2"}
EXTRACT_RUN_PLAN_FIELDS = {"argv", "cwd", "capture"}
COW_FIELDS = {"mode", "source_path", "destination_path"}
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

STATIC_ENVIRONMENT = {
    "GIT_ASKPASS": "/usr/bin/false",
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
DERIVED_FACTS_RELATIVE = Path("spike/t111/stage2-facts")
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


def _absolute(value: Any, label: str) -> Path:
    if not isinstance(value, str) or not value or "\n" in value or "\r" in value:
        raise PrebuildError(f"{label} is not an absolute path")
    path = Path(value)
    if not path.is_absolute() or path.name in {"", ".", ".."} or ".." in path.parts:
        raise PrebuildError(f"{label} is not an absolute path")
    return path


def _relative(value: Any, label: str) -> Path:
    if not isinstance(value, str) or not value or "\n" in value or "\r" in value:
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


def _fact_runs(value: Any, facts_root: Path) -> dict[str, Path]:
    if not isinstance(value, dict) or set(value) != {"run1", "run2"}:
        raise PrebuildError("prebuild authorization has an invalid fact-run plan")
    seen_ids: set[str] = set()
    seen_paths: set[Path] = set()
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
        seen_ids.add(run_id)
        seen_paths.add(path)
        paths[run] = path
        _commands(item.get("commands"))
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
    if not all(isinstance(item, str) and item and "\n" not in item and "\r" not in item for item in value):
        raise PrebuildError(f"{label} has an invalid argv")
    return value


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
    bootstrap: dict[str, Any],
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
    cow = value.get("cow")
    if not isinstance(cow, dict) or set(cow) != COW_FIELDS or cow.get("mode") != "copy-on-write":
        raise PrebuildError("prebuild authorization has an invalid copy-on-write plan")
    source = _absolute(cow.get("source_path"), "copy-on-write source")
    if _absolute(cow.get("destination_path"), "copy-on-write destination") != root:
        raise PrebuildError("copy-on-write destination differs from the derived root")
    if _paths_overlap(source, root) or _paths_overlap(source, ceremony):
        raise PrebuildError("copy-on-write source overlaps a writable P0 namespace")
    captures: set[Path] = set()
    for fixture in RECEIPT_FIXTURES:
        command = commands[fixture]
        if not isinstance(command, dict) or set(command) != HYDRATE_COMMAND_FIELDS:
            raise PrebuildError("hydration plan has an invalid fixture command")
        argv = _argv(command.get("argv"), "hydration")
        expected_argv = [
            bootstrap["t111_path"],
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
    bootstrap: dict[str, Any],
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
            if command != [bootstrap["t111_path"], "extract", "-system", fixture]:
                raise PrebuildError("extraction argv expands the authorized command")
        captures.update(_capture(plan.get("capture"), run_paths[run], "extraction capture"))
        if plan["capture"] != {
            "stdout_path": str(run_paths[run] / "extract.stdout"),
            "stderr_path": str(run_paths[run] / "extract.stderr"),
        }:
            raise PrebuildError("extraction capture differs from the fixed run path")
    return captures


def load_authorization(path: Path | None = None) -> tuple[dict[str, Any], bytes]:
    """Parse P0 without touching any bound source or live derived input."""
    value, raw = _canonical_json(AUTHORIZATION_PATH if path is None else path)
    if not isinstance(value, dict) or set(value) != AUTHORIZATION_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid schema")
    if value.get("schema") != AUTH_SCHEMA or value.get("status") != "AUTHORIZED":
        raise PrebuildError("prebuild authorization is not authorized")
    if (
        not isinstance(value.get("authorization_id"), str)
        or not value["authorization_id"]
        or "\n" in value["authorization_id"]
        or "\r" in value["authorization_id"]
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
    bootstrap_t111 = _absolute(bootstrap.get("t111_path"), "bootstrap t111 path")
    if bootstrap_t111.name != "t111":
        raise PrebuildError("bootstrap executable is not named t111")
    _sha256(bootstrap.get("t111_sha256"), "bootstrap t111 digest")

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
        if not isinstance(toolchain.get(field), str) or not toolchain[field] or "\n" in toolchain[field]:
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
        variables = phase_environment.get("variables")
        if not isinstance(variables, dict) or set(variables) != {"PATH", "GOROOT", *STATIC_ENVIRONMENT}:
            raise PrebuildError(f"prebuild authorization has an invalid {phase} variables binding")
        if (
            not isinstance(variables.get("PATH"), str)
            or variables["PATH"] != sealed_path(
                executable_paths["go_executable"], executable_paths["git_executable"]
            )
            or "\n" in variables["PATH"]
            or "\r" in variables["PATH"]
            or any(variables[key] != expected for key, expected in STATIC_ENVIRONMENT.items())
        ):
            raise PrebuildError(f"prebuild authorization has an invalid {phase} variables binding")
        goroot = _absolute(variables.get("GOROOT"), f"{phase} GOROOT")
        if goroot != _absolute(toolchain["go_executable"], "go executable").parent.parent:
            raise PrebuildError(f"prebuild authorization has an invalid {phase} GOROOT binding")
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
        for field in ("lock_path", "cache_path", "corpus_path", "facts_root")
    }
    for field, path in topology.items():
        _path_within(path, root, field)
    expected_topology = {
        "lock_path": root / DERIVED_LOCK_RELATIVE,
        "cache_path": root / DERIVED_CACHE_RELATIVE,
        "corpus_path": root / DERIVED_CORPUS_RELATIVE,
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
    run_paths = _fact_runs(value["fact_runs"], topology["facts_root"])
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

    operations = value["operations"]
    if not isinstance(operations, dict) or set(operations) != OPERATIONS_FIELDS:
        raise PrebuildError("prebuild authorization has an invalid operation plan")
    capture_paths = _hydrate_plan(operations["hydrate"], bootstrap, root, ceremony, hydration)
    capture_paths.update(_extract_plan(operations["extract"], bootstrap, root, run_paths))
    if any(
        _paths_overlap(state_path, capture_path)
        for state_path in state_targets.values()
        for capture_path in capture_paths
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
