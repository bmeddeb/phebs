#!/usr/bin/env bash

# Run and seal one newly frozen T40.13 neutral ceremony on a dedicated Mac.
# This driver never reads an operator corpus. It authors the frozen T40.1
# structural and semantic profiles and retains only source-free evidence.

set -euo pipefail
set -o noclobber
umask 077

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIRECTORY="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly SCRIPT_PATH="${SCRIPT_DIRECTORY}/$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_REPO_ROOT="$(cd "${SCRIPT_DIRECTORY}/../.." && pwd -P)"
readonly DEFAULT_BASE_PORT=41731
readonly EXECUTE_APPROVAL="execute-reviewed-neutral-t4013-plan"
readonly HOST_STABILITY_CONFIRMATION="dedicated-single-operator-host-with-tool-mutation-disabled"
readonly PREPARE_CONFIRM="prepare-neutral-t4013-custody"
readonly EXECUTE_CONFIRM="execute-neutral-t4013-and-destroy-custody"
readonly CLEANUP_CONFIRM="cleanup-neutral-t4013-custody"
readonly SIGNATURE_NAMESPACE="phebs-t4013"
readonly FREEZE_SIGNATURE_NAMESPACE="phebs-t4013-freeze"
readonly LAST_REVIEW_STOPPED_CEREMONY_NUMBER=46
readonly RETIRED_SIGNER_FINGERPRINT="SHA256:BqFeTpCclBV0Z6Dz/Lc0dmpb75q7lZSAgH5rc6AK2nw"
readonly SIGNER_IDENTITY="phebs-ceremony"
readonly MINIMUM_MEMORY_BYTES=$((24 * 1024 * 1024 * 1024))
readonly MINIMUM_DISK_BYTES=$((120 * 1024 * 1024 * 1024))
readonly MAXIMUM_TRANSFER_PACKAGE_BYTES=$((4 * 1024 * 1024))
readonly CLOSED_SYSTEM_PATH="/usr/bin:/bin:/usr/sbin:/sbin"
readonly HOST_PATH="${PATH:-}"

REPO_ROOT="${PHEBS_REPO_ROOT:-$SCRIPT_REPO_ROOT}"
CEREMONY_ROOT="${PHEBS_CEREMONY_ROOT:-${HOME:-}/phebs-t4013-ceremony}"
BASE_PORT="${PHEBS_T4013_BASE_PORT:-$DEFAULT_BASE_PORT}"
HOST_STABILITY_ATTESTATION="${PHEBS_T4013_HOST_STABILITY_ATTESTATION:-}"
SIGNING_KEY=""
SIGNING_ROOT=""
REPO_REAL=""
CEREMONY_REAL=""
CLOSED_CONTROL_ROOT="${CLOSED_CONTROL_ROOT:-}"
CLOSED_CONTROL_SHA256="${CLOSED_CONTROL_SHA256:-}"
CLOSED_CONTROL_MANIFEST="${CLOSED_CONTROL_MANIFEST:-}"
CLOSED_HOME="${CLOSED_HOME:-}"
CLOSED_TMP="${CLOSED_TMP:-}"
CLOSED_GO_CACHE="${CLOSED_GO_CACHE:-}"
CLOSED_GO_MODULE_CACHE="${CLOSED_GO_MODULE_CACHE:-}"
CLOSED_COMMAND_ROOT="${CLOSED_COMMAND_ROOT:-}"
CLOSED_CACHES_ABSENT="${CLOSED_CACHES_ABSENT:-0}"
CLOSED_GO_PATH="${CLOSED_GO_PATH:-}"
CLOSED_GO_SHA256="${CLOSED_GO_SHA256:-}"
CLOSED_GIT_PATH="${CLOSED_GIT_PATH:-}"
CLOSED_GIT_SHA256="${CLOSED_GIT_SHA256:-}"
CLOSED_GIT_CORE_PATH="${CLOSED_GIT_CORE_PATH:-}"
CLOSED_GIT_CORE_SHA256="${CLOSED_GIT_CORE_SHA256:-}"
CLOSED_GIT_EXEC_PATH="${CLOSED_GIT_EXEC_PATH:-}"
CLOSED_SURREAL_PATH="${CLOSED_SURREAL_PATH:-}"
CLOSED_SURREAL_SHA256="${CLOSED_SURREAL_SHA256:-}"
V25_CLEANUP_COMMAND="${V25_CLEANUP_COMMAND:-}"
V25_CLEANUP_SHA256="${V25_CLEANUP_SHA256:-}"
V25_BUNDLE_COMMAND="${V25_BUNDLE_COMMAND:-}"
V25_BUNDLE_SHA256="${V25_BUNDLE_SHA256:-}"
V25_EXECUTE_COMMAND="${V25_EXECUTE_COMMAND:-}"
V25_EXECUTE_SHA256="${V25_EXECUTE_SHA256:-}"
V25_FREEZE_COMMAND="${V25_FREEZE_COMMAND:-}"
V25_FREEZE_SHA256="${V25_FREEZE_SHA256:-}"
V25_INSPECT_COMMAND="${V25_INSPECT_COMMAND:-}"
V25_INSPECT_SHA256="${V25_INSPECT_SHA256:-}"
V25_LOCK_COMMAND="${V25_LOCK_COMMAND:-}"
V25_LOCK_SHA256="${V25_LOCK_SHA256:-}"
V25_PREPARE_COMMAND="${V25_PREPARE_COMMAND:-}"
V25_PREPARE_SHA256="${V25_PREPARE_SHA256:-}"
V25_PROMOTE_COMMAND="${V25_PROMOTE_COMMAND:-}"
V25_PROMOTE_SHA256="${V25_PROMOTE_SHA256:-}"
V25_RECEIPT_COMMAND="${V25_RECEIPT_COMMAND:-}"
V25_RECEIPT_SHA256="${V25_RECEIPT_SHA256:-}"
if [[ -z "${T4013_RUN_LOCK_FD:-}" ]]; then
  CLOSED_CONTROL_ROOT=""
  CLOSED_CONTROL_SHA256=""
  CLOSED_CONTROL_MANIFEST=""
  CLOSED_HOME=""
  CLOSED_TMP=""
  CLOSED_GO_CACHE=""
  CLOSED_GO_MODULE_CACHE=""
  CLOSED_COMMAND_ROOT=""
  CLOSED_CACHES_ABSENT=0
  CLOSED_GO_PATH=""
  CLOSED_GO_SHA256=""
  CLOSED_GIT_PATH=""
  CLOSED_GIT_SHA256=""
  CLOSED_GIT_CORE_PATH=""
  CLOSED_GIT_CORE_SHA256=""
  CLOSED_GIT_EXEC_PATH=""
  CLOSED_SURREAL_PATH=""
  CLOSED_SURREAL_SHA256=""
  V25_CLEANUP_COMMAND=""
  V25_CLEANUP_SHA256=""
  V25_BUNDLE_COMMAND=""
  V25_BUNDLE_SHA256=""
  V25_EXECUTE_COMMAND=""
  V25_EXECUTE_SHA256=""
  V25_FREEZE_COMMAND=""
  V25_FREEZE_SHA256=""
  V25_INSPECT_COMMAND=""
  V25_INSPECT_SHA256=""
  V25_LOCK_COMMAND=""
  V25_LOCK_SHA256=""
  V25_PREPARE_COMMAND=""
  V25_PREPARE_SHA256=""
  V25_PROMOTE_COMMAND=""
  V25_PROMOTE_SHA256=""
  V25_RECEIPT_COMMAND=""
  V25_RECEIPT_SHA256=""
fi
EXIT_PREPARED_PLAN=""
EXIT_PREPARED_MANIFEST=""
EXIT_PREPARED_WORKSPACE=""
RUN_LOCK_DIRECTORY=""
RUN_LOCK_TOKEN=""
RUN_LOCK_INHERITED=0
EXIT_UNPROVEN_REASON=""
ACTIVE_CHILD_PID=""
RETURNED_BUNDLE_TEMPORARY_ROOT=""

die() {
  printf '%s: %s\n' "$SCRIPT_NAME" "$*" >&2
  exit 1
}

note() {
  printf '%s\n' "$*"
}

canonical_executable_path() {
  local path="$1" target directory links=0
  [[ "$path" == /* ]] || path="$(command -v "$path")"
  while [[ -L "$path" ]]; do
    (( links += 1 ))
    (( links <= 40 )) || die "executable symlink chain is too deep: $1"
    target="$(readlink "$path")"
    if [[ "$target" == /* ]]; then
      path="$target"
    else
      path="$(dirname "$path")/$target"
    fi
  done
  directory="$(cd "$(dirname "$path")" && pwd -P)"
  path="${directory}/$(basename "$path")"
  [[ -f "$path" && ! -L "$path" && -x "$path" ]] ||
    die "executable is not a canonical regular file: $1"
  printf '%s\n' "$path"
}

executable_digest() {
  printf 'sha256:%s\n' "$(shasum -a 256 "$1" | awk '{print $1}')"
}

require_bound_executable() {
  local path="$1" expected="$2" name="$3"
  [[ "$path" == /* && -f "$path" && ! -L "$path" && -x "$path" &&
    "$expected" =~ ^sha256:[0-9a-f]{64}$ && "$(executable_digest "$path")" == "$expected" ]] || {
    printf '%s: bound %s executable changed before launch\n' "$SCRIPT_NAME" "$name" >&2
    return 1
  }
}

initialize_closed_go_path() {
  if [[ -n "$CLOSED_GO_PATH" || -n "$CLOSED_GO_SHA256" ]]; then
    [[ -n "$CLOSED_GO_PATH" && -n "$CLOSED_GO_SHA256" ]] ||
      die "closed Go identity state is incomplete"
    return
  fi
  CLOSED_GO_PATH="$(canonical_executable_path go)"
  CLOSED_GO_SHA256="$(executable_digest "$CLOSED_GO_PATH")"
}

initialize_closed_git_paths() {
  local exec_path
  if [[ -n "$CLOSED_GIT_PATH" || -n "$CLOSED_GIT_SHA256" ||
    -n "$CLOSED_GIT_CORE_PATH" || -n "$CLOSED_GIT_CORE_SHA256" || -n "$CLOSED_GIT_EXEC_PATH" ]]; then
    [[ -n "$CLOSED_GIT_PATH" && -n "$CLOSED_GIT_SHA256" &&
      -n "$CLOSED_GIT_CORE_PATH" && -n "$CLOSED_GIT_CORE_SHA256" && -n "$CLOSED_GIT_EXEC_PATH" ]] ||
      die "closed Git identity state is incomplete"
    return
  fi
  CLOSED_GIT_PATH="$(canonical_executable_path git)"
  CLOSED_GIT_SHA256="$(executable_digest "$CLOSED_GIT_PATH")"
  require_bound_executable "$CLOSED_GIT_PATH" "$CLOSED_GIT_SHA256" Git || die "closed Git identity is invalid"
  exec_path="$(env -i \
    LC_ALL=C LANG=C TZ=UTC \
    GIT_ATTR_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
    GIT_NO_LAZY_FETCH=1 GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 \
    "$CLOSED_GIT_PATH" --exec-path)"
  [[ "$exec_path" == /* && -d "$exec_path" && ! -L "$exec_path" ]] ||
    die "closed Git executable directory is invalid"
  CLOSED_GIT_CORE_PATH="$(canonical_executable_path "${exec_path}/git")"
  CLOSED_GIT_CORE_SHA256="$(executable_digest "$CLOSED_GIT_CORE_PATH")"
  CLOSED_GIT_EXEC_PATH="$exec_path"
}

initialize_closed_surreal_path() {
  if [[ -n "$CLOSED_SURREAL_PATH" || -n "$CLOSED_SURREAL_SHA256" ]]; then
    [[ -n "$CLOSED_SURREAL_PATH" && -n "$CLOSED_SURREAL_SHA256" ]] ||
      die "closed SurrealDB identity state is incomplete"
    return
  fi
  CLOSED_SURREAL_PATH="$(canonical_executable_path surreal)"
  CLOSED_SURREAL_SHA256="$(executable_digest "$CLOSED_SURREAL_PATH")"
}

closed_control_manifest_content() {
  printf '%s\n' \
    'schema=t4013-shell-execution-controls-v1' \
    "root=$CLOSED_CONTROL_ROOT" \
    "home=$CLOSED_HOME" \
    "tmp=$CLOSED_TMP" \
    "go_build=$CLOSED_GO_CACHE" \
    "go_module=$CLOSED_GO_MODULE_CACHE" \
    "commands=$CLOSED_COMMAND_ROOT" \
    "go=$CLOSED_GO_PATH" \
    "go_sha256=$CLOSED_GO_SHA256" \
    "git=$CLOSED_GIT_PATH" \
    "git_sha256=$CLOSED_GIT_SHA256" \
    "git_core=$CLOSED_GIT_CORE_PATH" \
    "git_core_sha256=$CLOSED_GIT_CORE_SHA256" \
    "git_exec=$CLOSED_GIT_EXEC_PATH" \
    "surreal=$CLOSED_SURREAL_PATH" \
    "surreal_sha256=$CLOSED_SURREAL_SHA256" \
    "system_path=$CLOSED_SYSTEM_PATH"
}

validate_closed_controls() {
  local expected expected_digest actual_digest
  PATH="$CLOSED_SYSTEM_PATH"
  export PATH
  [[ -n "$CEREMONY_REAL" && -d "$CEREMONY_REAL" && ! -L "$CEREMONY_REAL" &&
    "$CLOSED_CONTROL_ROOT" == "$CEREMONY_REAL"/.t4013-controls.* &&
    -d "$CLOSED_CONTROL_ROOT" && ! -L "$CLOSED_CONTROL_ROOT" &&
    "$CLOSED_CONTROL_MANIFEST" == "$CLOSED_CONTROL_ROOT/control" &&
    -f "$CLOSED_CONTROL_MANIFEST" && ! -L "$CLOSED_CONTROL_MANIFEST" &&
    "$CLOSED_HOME" == "$CLOSED_CONTROL_ROOT/home" && -d "$CLOSED_HOME" && ! -L "$CLOSED_HOME" &&
    "$CLOSED_TMP" == "$CLOSED_CONTROL_ROOT/tmp" && -d "$CLOSED_TMP" && ! -L "$CLOSED_TMP" &&
    "$CLOSED_GO_CACHE" == "$CLOSED_CONTROL_ROOT/go-build" &&
    "$CLOSED_GO_MODULE_CACHE" == "$CLOSED_CONTROL_ROOT/go-mod" &&
    "$CLOSED_COMMAND_ROOT" == "$CLOSED_CONTROL_ROOT/commands" &&
    -d "$CLOSED_GIT_EXEC_PATH" && ! -L "$CLOSED_GIT_EXEC_PATH" ]] ||
    die "closed execution control paths are invalid"
  case "$CLOSED_CACHES_ABSENT" in
    0)
      [[ -d "$CLOSED_GO_CACHE" && ! -L "$CLOSED_GO_CACHE" &&
        -d "$CLOSED_GO_MODULE_CACHE" && ! -L "$CLOSED_GO_MODULE_CACHE" ]] ||
        die "closed Go caches are invalid"
      ;;
    1)
      [[ ! -e "$CLOSED_GO_CACHE" && ! -L "$CLOSED_GO_CACHE" &&
        ! -e "$CLOSED_GO_MODULE_CACHE" && ! -L "$CLOSED_GO_MODULE_CACHE" ]] ||
        die "retired closed Go caches reappeared"
      ;;
    *) die "closed Go cache state is invalid" ;;
  esac
  expected="$(closed_control_manifest_content)"
  expected_digest="sha256:$(printf '%s\n' "$expected" | shasum -a 256 | awk '{print $1}')"
  [[ "$CLOSED_CONTROL_SHA256" =~ ^sha256:[0-9a-f]{64}$ &&
    "$CLOSED_CONTROL_SHA256" == "$expected_digest" ]] ||
    die "closed execution control manifest changed"
  if [[ -n "$V25_INSPECT_COMMAND" ]]; then
    require_v25_custody_command "$V25_INSPECT_COMMAND" ||
      die "bounded exact-control inspector changed"
    actual_digest="$("$V25_INSPECT_COMMAND" -file-digest "$CLOSED_CONTROL_MANIFEST" -maximum-bytes 4096)" ||
      die "closed execution control manifest cannot be inspected"
    [[ "$actual_digest" == "$CLOSED_CONTROL_SHA256" ]] ||
      die "closed execution control manifest changed"
  else
    # The fresh 0700 build root does not treat this write-only marker as
    # authority until the exact inspector has been compiled and bound.
    [[ "$CLOSED_CACHES_ABSENT" == 0 ]] ||
      die "bounded exact-control inspector is absent after command build"
  fi
}

closed_go() {
  local argument command=() command_seen=0
  validate_closed_controls
  for argument in "$@"; do
    if (( command_seen == 0 )) && [[ "$argument" != *=* ]]; then
      command_seen=1
      if [[ "$argument" == go ]]; then
        initialize_closed_go_path
        require_bound_executable "$CLOSED_GO_PATH" "$CLOSED_GO_SHA256" Go || return 1
        argument="$CLOSED_GO_PATH"
      elif [[ -n "$CLOSED_GO_PATH" && "$argument" == "$CLOSED_GO_PATH" ]]; then
        require_bound_executable "$CLOSED_GO_PATH" "$CLOSED_GO_SHA256" Go || return 1
      fi
    fi
    command+=("$argument")
  done
  env -i \
    HOME="$CLOSED_HOME" \
    PATH="$CLOSED_GIT_EXEC_PATH" \
    TMPDIR="$CLOSED_TMP" TEMP="$CLOSED_TMP" TMP="$CLOSED_TMP" \
    XDG_CONFIG_HOME="$CLOSED_HOME" XDG_CACHE_HOME="$CLOSED_TMP" XDG_DATA_HOME="$CLOSED_HOME" \
    LC_ALL=C LANG=C TZ=UTC \
    CGO_ENABLED=0 \
    GOENV=off \
    GOCACHE="$CLOSED_GO_CACHE" \
    GOMODCACHE="$CLOSED_GO_MODULE_CACHE" \
    GOTMPDIR="$CLOSED_TMP" \
    GOEXPERIMENT= \
    GOFLAGS= \
    GOTOOLCHAIN=local \
    GOTELEMETRY=off \
    GOWORK=off \
    CLOSED_GO_PATH="$CLOSED_GO_PATH" \
    CLOSED_GIT_PATH="$CLOSED_GIT_PATH" \
    CLOSED_GIT_CORE_PATH="$CLOSED_GIT_CORE_PATH" \
    CLOSED_SURREAL_PATH="$CLOSED_SURREAL_PATH" \
    T4013_RUN_LOCK_FD="${T4013_RUN_LOCK_FD:-}" \
    "${command[@]}"
}

run_active_child() {
  local status=0 monitor_was_enabled=0
  [[ -z "$ACTIVE_CHILD_PID" ]] || die "another custody child is already active"
  [[ $- == *m* ]] && monitor_was_enabled=1
  set -m
  "$@" &
  ACTIVE_CHILD_PID=$!
  (( monitor_was_enabled == 1 )) || set +m
  wait "$ACTIVE_CHILD_PID" || status=$?
  ACTIVE_CHILD_PID=""
  return "$status"
}

closed_go_active() {
  local argument command=() command_seen=0
  validate_closed_controls
  for argument in "$@"; do
    if (( command_seen == 0 )) && [[ "$argument" != *=* ]]; then
      command_seen=1
      if [[ "$argument" == go ]]; then
        initialize_closed_go_path
        require_bound_executable "$CLOSED_GO_PATH" "$CLOSED_GO_SHA256" Go || return 1
        argument="$CLOSED_GO_PATH"
      elif [[ -n "$CLOSED_GO_PATH" && "$argument" == "$CLOSED_GO_PATH" ]]; then
        require_bound_executable "$CLOSED_GO_PATH" "$CLOSED_GO_SHA256" Go || return 1
      fi
    fi
    command+=("$argument")
  done
  run_active_child env -i \
    HOME="$CLOSED_HOME" \
    PATH="$CLOSED_GIT_EXEC_PATH" \
    TMPDIR="$CLOSED_TMP" TEMP="$CLOSED_TMP" TMP="$CLOSED_TMP" \
    XDG_CONFIG_HOME="$CLOSED_HOME" XDG_CACHE_HOME="$CLOSED_TMP" XDG_DATA_HOME="$CLOSED_HOME" \
    LC_ALL=C LANG=C TZ=UTC \
    CGO_ENABLED=0 \
    GOENV=off \
    GOCACHE="$CLOSED_GO_CACHE" \
    GOMODCACHE="$CLOSED_GO_MODULE_CACHE" \
    GOTMPDIR="$CLOSED_TMP" \
    GOEXPERIMENT= \
    GOFLAGS= \
    GOTOOLCHAIN=local \
    GOTELEMETRY=off \
    GOWORK=off \
    CLOSED_GO_PATH="$CLOSED_GO_PATH" \
    CLOSED_GIT_PATH="$CLOSED_GIT_PATH" \
    CLOSED_GIT_CORE_PATH="$CLOSED_GIT_CORE_PATH" \
    CLOSED_SURREAL_PATH="$CLOSED_SURREAL_PATH" \
    T4013_RUN_LOCK_FD="${T4013_RUN_LOCK_FD:-}" \
    "${command[@]}"
}

initialize_closed_go_cache() {
  if [[ -n "$CLOSED_CONTROL_ROOT" ]]; then
    validate_closed_controls
    return 0
  fi
  [[ -n "$CEREMONY_REAL" ]] || die "ceremony root must be initialized before closed execution controls"
  [[ -z "$CLOSED_CONTROL_SHA256" && -z "$CLOSED_CONTROL_MANIFEST" &&
    -z "$CLOSED_HOME" && -z "$CLOSED_TMP" && -z "$CLOSED_GO_CACHE" &&
    -z "$CLOSED_GO_MODULE_CACHE" && -z "$CLOSED_COMMAND_ROOT" ]] ||
    die "closed execution control state is incomplete"
  initialize_closed_go_path
  initialize_closed_git_paths
  initialize_closed_surreal_path
  CLOSED_CONTROL_ROOT="$(mktemp -d "$CEREMONY_REAL/.t4013-controls.XXXXXX")"
  CLOSED_CONTROL_MANIFEST="$CLOSED_CONTROL_ROOT/control"
  CLOSED_HOME="$CLOSED_CONTROL_ROOT/home"
  CLOSED_TMP="$CLOSED_CONTROL_ROOT/tmp"
  CLOSED_GO_CACHE="$CLOSED_CONTROL_ROOT/go-build"
  CLOSED_GO_MODULE_CACHE="$CLOSED_CONTROL_ROOT/go-mod"
  CLOSED_COMMAND_ROOT="$CLOSED_CONTROL_ROOT/commands"
  CLOSED_CACHES_ABSENT=0
  mkdir -m 700 "$CLOSED_HOME" "$CLOSED_TMP" "$CLOSED_GO_CACHE" "$CLOSED_GO_MODULE_CACHE"
  closed_control_manifest_content > "$CLOSED_CONTROL_MANIFEST"
  chmod 600 "$CLOSED_CONTROL_MANIFEST"
  CLOSED_CONTROL_SHA256="sha256:$(closed_control_manifest_content | shasum -a 256 | awk '{print $1}')"
  validate_closed_controls
}

initialize_historical_go_cache() {
  [[ -z "$CLOSED_CONTROL_ROOT" ]] || die "historical execution cannot use V25 controls"
  [[ -z "$CLOSED_GO_CACHE" ]] || return 0
  CLOSED_GO_CACHE="$(mktemp -d "${TMPDIR:-/tmp}/phebs-t4013-go-cache.XXXXXX")"
  chmod 700 "$CLOSED_GO_CACHE"
}

historical_closed_go() {
  local argument command=() command_seen=0
  [[ -n "$CLOSED_GO_CACHE" ]] || die "historical closed Go cache is not initialized"
  for argument in "$@"; do
    if (( command_seen == 0 )) && [[ "$argument" != *=* ]]; then
      command_seen=1
      if [[ "$argument" == go ]]; then
        initialize_closed_go_path
        require_bound_executable "$CLOSED_GO_PATH" "$CLOSED_GO_SHA256" Go || return 1
        argument="$CLOSED_GO_PATH"
      fi
    fi
    command+=("$argument")
  done
  env -i \
    HOME="$HOME" PATH="$HOST_PATH" TMPDIR="${TMPDIR:-/tmp}" LC_ALL=C \
    CGO_ENABLED=0 GOENV=off GOCACHE="$CLOSED_GO_CACHE" GOEXPERIMENT= GOFLAGS= \
    GOTOOLCHAIN=local GOWORK=off T4013_RUN_LOCK_FD="${T4013_RUN_LOCK_FD:-}" \
    "${command[@]}"
}

initialize_v25_custody_commands() {
  local command_root command_path
  [[ -n "$REPO_REAL" ]] || die "repository must be initialized before building custody commands"
  initialize_closed_go_cache
  if [[ -n "$V25_BUNDLE_COMMAND" && -n "$V25_BUNDLE_SHA256" &&
    -n "$V25_CLEANUP_COMMAND" && -n "$V25_CLEANUP_SHA256" &&
    -n "$V25_EXECUTE_COMMAND" && -n "$V25_EXECUTE_SHA256" &&
    -n "$V25_FREEZE_COMMAND" && -n "$V25_FREEZE_SHA256" &&
    -n "$V25_INSPECT_COMMAND" && -n "$V25_INSPECT_SHA256" &&
    -n "$V25_LOCK_COMMAND" && -n "$V25_LOCK_SHA256" &&
    -n "$V25_PREPARE_COMMAND" && -n "$V25_PREPARE_SHA256" &&
    -n "$V25_PROMOTE_COMMAND" && -n "$V25_PROMOTE_SHA256" &&
    -n "$V25_RECEIPT_COMMAND" && -n "$V25_RECEIPT_SHA256" ]]; then
    validate_closed_controls
    [[ "$CLOSED_CACHES_ABSENT" == 1 ]] || die "prebuilt V25 commands retained mutable Go caches"
    for command_path in "$V25_BUNDLE_COMMAND" "$V25_CLEANUP_COMMAND" "$V25_EXECUTE_COMMAND" "$V25_FREEZE_COMMAND" \
      "$V25_INSPECT_COMMAND" "$V25_LOCK_COMMAND" "$V25_PREPARE_COMMAND" "$V25_PROMOTE_COMMAND" "$V25_RECEIPT_COMMAND"; do
      require_v25_custody_command "$command_path" ||
        die "prebuilt V25 custody command became invalid: $command_path"
    done
    return 0
  fi
  [[ -z "$V25_BUNDLE_COMMAND" && -z "$V25_BUNDLE_SHA256" &&
    -z "$V25_CLEANUP_COMMAND" && -z "$V25_CLEANUP_SHA256" &&
    -z "$V25_EXECUTE_COMMAND" && -z "$V25_EXECUTE_SHA256" &&
    -z "$V25_FREEZE_COMMAND" && -z "$V25_FREEZE_SHA256" &&
    -z "$V25_INSPECT_COMMAND" && -z "$V25_INSPECT_SHA256" &&
    -z "$V25_LOCK_COMMAND" && -z "$V25_LOCK_SHA256" &&
    -z "$V25_PREPARE_COMMAND" && -z "$V25_PREPARE_SHA256" &&
    -z "$V25_PROMOTE_COMMAND" && -z "$V25_PROMOTE_SHA256" &&
    -z "$V25_RECEIPT_COMMAND" && -z "$V25_RECEIPT_SHA256" ]] ||
    die "prebuilt V25 custody-command state is incomplete"
  [[ "$CLOSED_CACHES_ABSENT" == 0 && ! -e "$CLOSED_COMMAND_ROOT" && ! -L "$CLOSED_COMMAND_ROOT" ]] ||
    die "closed build controls are not fresh"
  (cd "$REPO_REAL" && closed_go GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org \
    go list -deps ./spike/t4013/cmd/... >/dev/null)
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org go mod verify)
  command_root="$CLOSED_COMMAND_ROOT"
  mkdir -m 700 "$command_root"
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
    go build -o "${command_root}/" \
    ./spike/t4013/cmd/t4013-bundle \
    ./spike/t4013/cmd/t4013-cleanup \
    ./spike/t4013/cmd/t4013-execute \
    ./spike/t4013/cmd/t4013-freeze \
    ./spike/t4013/cmd/t4013-inspect \
    ./spike/t4013/cmd/t4013-lock \
    ./spike/t4013/cmd/t4013-prepare \
    ./spike/t4013/cmd/t4013-promote \
    ./spike/t4013/cmd/t4013-receipt)
  V25_INSPECT_COMMAND="${command_root}/t4013-inspect"
  V25_INSPECT_SHA256="$(executable_digest "$V25_INSPECT_COMMAND")"
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify)
  V25_BUNDLE_COMMAND="${command_root}/t4013-bundle"
  V25_BUNDLE_SHA256="$(executable_digest "$V25_BUNDLE_COMMAND")"
  V25_CLEANUP_COMMAND="${command_root}/t4013-cleanup"
  V25_CLEANUP_SHA256="$(executable_digest "$V25_CLEANUP_COMMAND")"
  V25_EXECUTE_COMMAND="${command_root}/t4013-execute"
  V25_EXECUTE_SHA256="$(executable_digest "$V25_EXECUTE_COMMAND")"
  V25_FREEZE_COMMAND="${command_root}/t4013-freeze"
  V25_FREEZE_SHA256="$(executable_digest "$V25_FREEZE_COMMAND")"
  V25_LOCK_COMMAND="${command_root}/t4013-lock"
  V25_LOCK_SHA256="$(executable_digest "$V25_LOCK_COMMAND")"
  V25_PREPARE_COMMAND="${command_root}/t4013-prepare"
  V25_PREPARE_SHA256="$(executable_digest "$V25_PREPARE_COMMAND")"
  V25_PROMOTE_COMMAND="${command_root}/t4013-promote"
  V25_PROMOTE_SHA256="$(executable_digest "$V25_PROMOTE_COMMAND")"
  V25_RECEIPT_COMMAND="${command_root}/t4013-receipt"
  V25_RECEIPT_SHA256="$(executable_digest "$V25_RECEIPT_COMMAND")"
  for command_path in "$V25_BUNDLE_COMMAND" "$V25_CLEANUP_COMMAND" "$V25_EXECUTE_COMMAND" "$V25_FREEZE_COMMAND" \
    "$V25_INSPECT_COMMAND" "$V25_LOCK_COMMAND" "$V25_PREPARE_COMMAND" "$V25_PROMOTE_COMMAND" "$V25_RECEIPT_COMMAND"; do
    [[ -f "$command_path" && ! -L "$command_path" && -x "$command_path" ]] ||
      die "prebuilt V25 custody command is invalid: $command_path"
  done
  (cd "$REPO_REAL" && closed_go GOPROXY=off GOSUMDB=off go clean -modcache)
  rm -rf -- "$CLOSED_GO_CACHE"
  CLOSED_CACHES_ABSENT=1
  validate_closed_controls
}

run_v25_custody_command_in_repo_active() {
  local command_path="$1" previous_directory="$PWD" status=0
  shift
  require_v25_custody_command "$command_path" || return 1
  cd "$REPO_REAL" || return 1
  closed_go_active GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
    "$command_path" "$@" || status=$?
  cd "$previous_directory" || return 1
  return "$status"
}

require_v25_custody_command() {
  local command_path="$1" expected=""
  case "$command_path" in
    "$V25_BUNDLE_COMMAND") expected="$V25_BUNDLE_SHA256" ;;
    "$V25_CLEANUP_COMMAND") expected="$V25_CLEANUP_SHA256" ;;
    "$V25_EXECUTE_COMMAND") expected="$V25_EXECUTE_SHA256" ;;
    "$V25_FREEZE_COMMAND") expected="$V25_FREEZE_SHA256" ;;
    "$V25_INSPECT_COMMAND") expected="$V25_INSPECT_SHA256" ;;
    "$V25_LOCK_COMMAND") expected="$V25_LOCK_SHA256" ;;
    "$V25_PREPARE_COMMAND") expected="$V25_PREPARE_SHA256" ;;
    "$V25_PROMOTE_COMMAND") expected="$V25_PROMOTE_SHA256" ;;
    "$V25_RECEIPT_COMMAND") expected="$V25_RECEIPT_SHA256" ;;
  esac
  [[ "$command_path" == "${CLOSED_COMMAND_ROOT}/"* &&
    -n "$expected" && -f "$command_path" && ! -L "$command_path" && -x "$command_path" &&
    "$(executable_digest "$command_path")" == "$expected" ]] ||
    {
      printf '%s: V25 custody command was not prebuilt before operation admission\n' "$SCRIPT_NAME" >&2
      return 1
    }
}

cleanup_on_exit() {
  local status=$?
  trap - EXIT
  if [[ -n "$RETURNED_BUNDLE_TEMPORARY_ROOT" ]]; then
    rm -rf -- "$RETURNED_BUNDLE_TEMPORARY_ROOT" || status=1
    RETURNED_BUNDLE_TEMPORARY_ROOT=""
  fi
  if [[ -z "$EXIT_UNPROVEN_REASON" &&
    -n "$EXIT_PREPARED_PLAN" && -n "$EXIT_PREPARED_MANIFEST" &&
    -n "$EXIT_PREPARED_WORKSPACE" &&
    ! -e "${EXIT_PREPARED_WORKSPACE}/.t4013-executed" &&
    ! -L "${EXIT_PREPARED_WORKSPACE}/.t4013-executed" ]]; then
    if ! cleanup_prepared "$EXIT_PREPARED_PLAN" "$EXIT_PREPARED_MANIFEST"; then
      status=1
      EXIT_UNPROVEN_REASON="prepared cleanup refused"
    fi
  fi
  if [[ -n "$EXIT_UNPROVEN_REASON" ]]; then
    printf '%s: operation state retained (%s); child exit is unproven\n' \
      "$SCRIPT_NAME" "$EXIT_UNPROVEN_REASON" >&2
    exit "$status"
  fi
  if [[ -n "$RUN_LOCK_DIRECTORY" || -n "$RUN_LOCK_TOKEN" ]]; then
    release_run_lock || status=1
  fi
  if [[ -n "$CLOSED_CONTROL_ROOT" ]]; then
    validate_closed_controls
    if [[ "$CLOSED_CACHES_ABSENT" == 0 ]]; then
      find "$CLOSED_GO_CACHE" "$CLOSED_GO_MODULE_CACHE" -type d -exec /bin/chmod 700 {} + || status=1
    fi
    rm -rf -- "$CLOSED_CONTROL_ROOT" || status=1
    CLOSED_CONTROL_ROOT=""
  elif [[ -n "$CLOSED_GO_CACHE" ]]; then
    rm -rf -- "$CLOSED_GO_CACHE" || status=1
    CLOSED_GO_CACHE=""
  fi
  exit "$status"
}

retain_on_signal() {
  local signal_name="$1" status="$2"
  EXIT_UNPROVEN_REASON="signal ${signal_name}"
  trap - INT TERM HUP
  if [[ -n "$ACTIVE_CHILD_PID" ]]; then
    if ! kill -s "$signal_name" -- "-${ACTIVE_CHILD_PID}" 2>/dev/null; then
      EXIT_UNPROVEN_REASON="${EXIT_UNPROVEN_REASON}; child signal forwarding failed"
    fi
    wait "$ACTIVE_CHILD_PID" 2>/dev/null || :
    ACTIVE_CHILD_PID=""
  fi
  exit "$status"
}

acquire_run_lock() {
  local run_root="$1" owner
  [[ "$run_root" == /* && -d "$run_root" && ! -L "$run_root" ]] ||
    die "ceremony run directory is invalid for locking"
  [[ -z "$RUN_LOCK_DIRECTORY" && -z "$RUN_LOCK_TOKEN" ]] ||
    die "this driver already owns a ceremony operation lock"
  RUN_LOCK_DIRECTORY="${run_root}/.t4013-operation.lock"
  if [[ -n "${T4013_RUN_LOCK_FD:-}" ]]; then
    [[ "$T4013_RUN_LOCK_FD" =~ ^[0-9]+$ && "$T4013_RUN_LOCK_FD" -ge 3 ]] ||
      die "inherited V25 ceremony operation lock is invalid"
    require_v25_custody_command "$V25_LOCK_COMMAND" ||
      die "V25 run-root lock command is unavailable while adopting inherited custody"
    "$V25_LOCK_COMMAND" -run-root "$run_root" -adopt ||
      die "inherited V25 ceremony operation lock cannot be adopted"
    RUN_LOCK_TOKEN="inherited:${T4013_RUN_LOCK_FD}"
    RUN_LOCK_INHERITED=1
    return
  fi
  if ! mkdir -m 700 -- "$RUN_LOCK_DIRECTORY"; then
    RUN_LOCK_DIRECTORY=""
    die "ceremony operation lock is retained; prove no operation owns it before reviewed removal"
  fi
  RUN_LOCK_TOKEN="$$:${RANDOM}:${RANDOM}"
  owner="${RUN_LOCK_DIRECTORY}/owner"
  printf '%s\n' "$RUN_LOCK_TOKEN" > "$owner" ||
    die "ceremony operation lock owner could not be recorded; lock retained for review"
}

release_run_lock() {
  local owner actual unexpected
  if [[ -z "$RUN_LOCK_DIRECTORY" || -z "$RUN_LOCK_TOKEN" ]]; then
    printf '%s: ceremony operation lock ownership is incomplete; lock retained for review\n' "$SCRIPT_NAME" >&2
    return 1
  fi
  if (( RUN_LOCK_INHERITED == 1 )); then
    if [[ ! -f "$RUN_LOCK_DIRECTORY" || -L "$RUN_LOCK_DIRECTORY" ]]; then
      printf '%s: inherited V25 ceremony operation lock changed; lock retained by the process\n' "$SCRIPT_NAME" >&2
      return 1
    fi
    RUN_LOCK_DIRECTORY=""
    RUN_LOCK_TOKEN=""
    return 0
  fi
  owner="${RUN_LOCK_DIRECTORY}/owner"
  if [[ ! -d "$RUN_LOCK_DIRECTORY" || -L "$RUN_LOCK_DIRECTORY" ||
    ! -f "$owner" || -L "$owner" ]]; then
    printf '%s: ceremony operation lock ownership is unprovable; lock retained for review\n' "$SCRIPT_NAME" >&2
    return 1
  fi
  actual="$(<"$owner")"
  if [[ "$actual" != "$RUN_LOCK_TOKEN" ]]; then
    printf '%s: ceremony operation lock owner changed; lock retained for review\n' "$SCRIPT_NAME" >&2
    return 1
  fi
  if [[ -n "$V25_INSPECT_COMMAND" ]]; then
    if ! run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" \
      -exact-directory "$RUN_LOCK_DIRECTORY" -- owner; then
      printf '%s: ceremony operation lock contents changed; lock retained for review\n' "$SCRIPT_NAME" >&2
      return 1
    fi
  elif ! unexpected="$(find "$RUN_LOCK_DIRECTORY" -mindepth 1 -maxdepth 1 ! -name owner -print -quit)" ||
    [[ -n "$unexpected" ]]; then
    # Historical V1-V24 operation locks retain their original shell inspection.
    printf '%s: ceremony operation lock contents changed; lock retained for review\n' "$SCRIPT_NAME" >&2
    return 1
  fi
  if ! rm -- "$owner" || ! rmdir -- "$RUN_LOCK_DIRECTORY"; then
    printf '%s: ceremony operation lock release failed; lock retained for review\n' "$SCRIPT_NAME" >&2
    return 1
  fi
  RUN_LOCK_DIRECTORY=""
  RUN_LOCK_TOKEN=""
}

enter_v25_run_lock() {
  local command_name="${1:-}" ceremony_id run_root plan_path
  [[ "$command_name" == execute || "$command_name" == seal ]] || return 0
  if [[ "$command_name" == execute && $# -ne 4 ]] ||
    [[ "$command_name" == seal && $# -ne 2 ]]; then
    return 0
  fi
  [[ -z "${T4013_RUN_LOCK_FD:-}" ]] || return 0
  ceremony_id="${2:-}"
  validate_id "$ceremony_id"
  initialize_repository
  initialize_ceremony_root
  run_root="$(run_root_for "$ceremony_id")"
  plan_path="${run_root}/evidence/plan.json"
  [[ -d "$run_root" && ! -L "$run_root" && -f "$plan_path" && ! -L "$plan_path" ]] ||
    return 0
  initialize_v25_custody_commands
  is_v25_plan "$plan_path" || return 0
  require_v25_custody_command "$V25_LOCK_COMMAND" ||
    die "V25 run-root lock command was not prebuilt before operation admission"
  exec /usr/bin/env -i \
    HOME="$CLOSED_HOME" PATH="$CLOSED_SYSTEM_PATH" \
    TMPDIR="$CLOSED_TMP" TEMP="$CLOSED_TMP" TMP="$CLOSED_TMP" \
    XDG_CONFIG_HOME="$CLOSED_HOME" XDG_CACHE_HOME="$CLOSED_TMP" XDG_DATA_HOME="$CLOSED_HOME" \
    LC_ALL=C LANG=C TZ=UTC \
    PHEBS_REPO_ROOT="$REPO_REAL" PHEBS_CEREMONY_ROOT="$CEREMONY_REAL" \
    PHEBS_T4013_BASE_PORT="$BASE_PORT" \
    PHEBS_T4013_HOST_STABILITY_ATTESTATION="$HOST_STABILITY_ATTESTATION" \
    CLOSED_CONTROL_ROOT="$CLOSED_CONTROL_ROOT" CLOSED_CONTROL_SHA256="$CLOSED_CONTROL_SHA256" \
    CLOSED_CONTROL_MANIFEST="$CLOSED_CONTROL_MANIFEST" CLOSED_HOME="$CLOSED_HOME" CLOSED_TMP="$CLOSED_TMP" \
    CLOSED_GO_CACHE="$CLOSED_GO_CACHE" CLOSED_GO_MODULE_CACHE="$CLOSED_GO_MODULE_CACHE" \
    CLOSED_COMMAND_ROOT="$CLOSED_COMMAND_ROOT" CLOSED_CACHES_ABSENT="$CLOSED_CACHES_ABSENT" \
    CLOSED_GO_PATH="$CLOSED_GO_PATH" CLOSED_GO_SHA256="$CLOSED_GO_SHA256" \
    CLOSED_GIT_PATH="$CLOSED_GIT_PATH" CLOSED_GIT_SHA256="$CLOSED_GIT_SHA256" \
    CLOSED_GIT_CORE_PATH="$CLOSED_GIT_CORE_PATH" CLOSED_GIT_CORE_SHA256="$CLOSED_GIT_CORE_SHA256" \
    CLOSED_GIT_EXEC_PATH="$CLOSED_GIT_EXEC_PATH" \
    CLOSED_SURREAL_PATH="$CLOSED_SURREAL_PATH" CLOSED_SURREAL_SHA256="$CLOSED_SURREAL_SHA256" \
    V25_BUNDLE_COMMAND="$V25_BUNDLE_COMMAND" V25_BUNDLE_SHA256="$V25_BUNDLE_SHA256" \
    V25_CLEANUP_COMMAND="$V25_CLEANUP_COMMAND" V25_CLEANUP_SHA256="$V25_CLEANUP_SHA256" \
    V25_EXECUTE_COMMAND="$V25_EXECUTE_COMMAND" V25_EXECUTE_SHA256="$V25_EXECUTE_SHA256" \
    V25_FREEZE_COMMAND="$V25_FREEZE_COMMAND" V25_FREEZE_SHA256="$V25_FREEZE_SHA256" \
    V25_INSPECT_COMMAND="$V25_INSPECT_COMMAND" V25_INSPECT_SHA256="$V25_INSPECT_SHA256" \
    V25_LOCK_COMMAND="$V25_LOCK_COMMAND" V25_LOCK_SHA256="$V25_LOCK_SHA256" \
    V25_PREPARE_COMMAND="$V25_PREPARE_COMMAND" V25_PREPARE_SHA256="$V25_PREPARE_SHA256" \
    V25_PROMOTE_COMMAND="$V25_PROMOTE_COMMAND" V25_PROMOTE_SHA256="$V25_PROMOTE_SHA256" \
    V25_RECEIPT_COMMAND="$V25_RECEIPT_COMMAND" V25_RECEIPT_SHA256="$V25_RECEIPT_SHA256" \
    "$V25_LOCK_COMMAND" -run-root "$run_root" -- "$SCRIPT_PATH" "$@"
}

closed_git() {
  initialize_closed_go_cache
  initialize_closed_git_paths
  require_bound_executable "$CLOSED_GIT_CORE_PATH" "$CLOSED_GIT_CORE_SHA256" Git-core || return 1
  validate_closed_controls
  env -i \
    HOME="$CLOSED_HOME" PATH="$CLOSED_GIT_EXEC_PATH" \
    TMPDIR="$CLOSED_TMP" TEMP="$CLOSED_TMP" TMP="$CLOSED_TMP" \
    XDG_CONFIG_HOME="$CLOSED_HOME" XDG_CACHE_HOME="$CLOSED_TMP" XDG_DATA_HOME="$CLOSED_HOME" \
    LC_ALL=C LANG=C TZ=UTC \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_OPTIONAL_LOCKS=0 \
    GIT_TERMINAL_PROMPT=0 \
    "$CLOSED_GIT_CORE_PATH" "$@"
}

closed_surreal() {
  initialize_closed_go_cache
  initialize_closed_surreal_path
  require_bound_executable "$CLOSED_SURREAL_PATH" "$CLOSED_SURREAL_SHA256" SurrealDB || return 1
  validate_closed_controls
  env -i HOME="$CLOSED_HOME" PATH="$CLOSED_GIT_EXEC_PATH" \
    TMPDIR="$CLOSED_TMP" TEMP="$CLOSED_TMP" TMP="$CLOSED_TMP" \
    XDG_CONFIG_HOME="$CLOSED_HOME" XDG_CACHE_HOME="$CLOSED_TMP" XDG_DATA_HOME="$CLOSED_HOME" \
    LC_ALL=C LANG=C TZ=UTC \
    "$CLOSED_SURREAL_PATH" "$@"
}

plan_git() {
  local plan_path="$1"
  shift
  if is_v25_plan "$plan_path"; then
    closed_git "$@"
    return
  fi
  initialize_closed_git_paths
  require_bound_executable "$CLOSED_GIT_CORE_PATH" "$CLOSED_GIT_CORE_SHA256" Git-core || return 1
  env -i \
    HOME="$HOME" PATH="$HOST_PATH" TMPDIR="${TMPDIR:-/tmp}" LC_ALL=C \
    GIT_ATTR_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
    GIT_NO_LAZY_FETCH=1 GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 \
    "$CLOSED_GIT_CORE_PATH" "$@"
}

plan_go() {
  local plan_path="$1"
  shift
  if is_v25_plan "$plan_path"; then
    closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off "$@" || return 1
  else
    env PATH="$HOST_PATH" GOPROXY=off "$@"
  fi
}

plan_go_active() {
  local plan_path="$1"
  shift
  if is_v25_plan "$plan_path"; then
    closed_go_active GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off "$@" || return 1
  else
    env PATH="$HOST_PATH" GOPROXY=off "$@"
  fi
}

plan_go_in_repo_active() {
  local plan_path="$1" previous_directory="$PWD" status=0
  shift
  cd "$REPO_REAL" || return 1
  plan_go_active "$plan_path" "$@" || status=$?
  cd "$previous_directory" || return 1
  return "$status"
}

is_v25_plan() {
  local schema
  require_v25_custody_command "$V25_INSPECT_COMMAND" ||
    die "bounded exact-control inspector is unavailable"
  schema="$(run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" -plan-schema "$1")" ||
    die "plan failed bounded exact-control inspection"
  [[ "$schema" == "t4013-neutral-convergence-plan-v25" ||
    "$schema" == "t4013-neutral-convergence-plan-v26" ||
    "$schema" == "t4013-neutral-convergence-plan-v27" ||
    "$schema" == "t4013-neutral-convergence-plan-v28" ||
    "$schema" == "t4013-neutral-convergence-plan-v29" ||
    "$schema" == "t4013-neutral-convergence-plan-v30" ||
    "$schema" == "t4013-neutral-convergence-plan-v31" ||
    "$schema" == "t4013-neutral-convergence-plan-v32" ]]
}

usage() {
  cat <<EOF
Usage:
  PHEBS_T4013_HOST_STABILITY_ATTESTATION=$HOST_STABILITY_CONFIRMATION \
    $SCRIPT_NAME preflight
  $SCRIPT_NAME freeze <ceremony-id>
  $SCRIPT_NAME execute <ceremony-id> <approved-plan-digest> $EXECUTE_APPROVAL
  $SCRIPT_NAME seal <ceremony-id>
  $SCRIPT_NAME verify <ceremony-id>
  $SCRIPT_NAME verify-bundle </absolute/path/to/source-free.tgz> \
    --reviewed-signer-fingerprint <SHA256:...>
  $SCRIPT_NAME verify-bundle </absolute/path/to/source-free.tgz> \
    --reviewed-package-digest <sha256:...>

Defaults:
  phebs checkout:  ~/phebs
  ceremony root:   ~/phebs-t4013-ceremony
  loopback ports:  41731 and 41732

The freeze and execute commands are deliberately separate. Review and record
the printed plan digest before invoking execute. No command accepts a private
monorepo path; this is the neutral two-million-owner ceremony only.

Every operational command requires PHEBS_T4013_HOST_STABILITY_ATTESTATION to
equal the phrase shown above. It attests that this is a dedicated,
single-operator host and that package, OS, tool, and other same-UID mutation is
disabled from preflight through source-free packaging.
EOF
}

require_host_stability_attestation() {
  [[ "$HOST_STABILITY_ATTESTATION" == "$HOST_STABILITY_CONFIRMATION" ]] ||
    die "dedicated-host stability attestation is missing or invalid"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command is unavailable: $1"
}

durable_promote() {
  local temporary="$1" final="$2" filesystem_root="$3" plan_path="$4"
  [[ "$temporary" == /* && "$final" == /* && "$filesystem_root" == /* &&
    "${temporary%/*}" == "${final%/*}" &&
    -f "$temporary" && ! -L "$temporary" &&
    ! -L "$final" && (! -e "$final" || -f "$final") &&
    -d "$filesystem_root" && ! -L "$filesystem_root" ]] ||
    die "durable evidence promotion is invalid"
  if is_v25_plan "$plan_path"; then
    initialize_v25_custody_commands
    require_v25_custody_command "$V25_PROMOTE_COMMAND" || die "V25 promotion command is unavailable"
    (cd "$REPO_REAL" && closed_go \
      "$V25_PROMOTE_COMMAND" -temporary "$temporary" -output "$final" -root "$CEREMONY_REAL") ||
      die "durable evidence promotion failed"
  else
    (cd "$REPO_REAL" && historical_closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
      go run ./spike/t4013/cmd/t4013-promote \
      -temporary "$temporary" -output "$final" -root "$CEREMONY_REAL") ||
      die "durable evidence promotion failed"
  fi
}

durable_stage() {
  local stage="$1" filesystem_root="$2" plan_path="$3"
  [[ "$stage" == /* && "$filesystem_root" == /* &&
    -f "$stage" && ! -L "$stage" &&
    -d "$filesystem_root" && ! -L "$filesystem_root" &&
    "${stage%/*}" == "$filesystem_root" ]] ||
    die "durable evidence stage is invalid"
  if is_v25_plan "$plan_path"; then
    initialize_v25_custody_commands
    require_v25_custody_command "$V25_PROMOTE_COMMAND" || die "V25 stage command is unavailable"
    (cd "$REPO_REAL" && closed_go \
      "$V25_PROMOTE_COMMAND" -stage "$stage" -root "$CEREMONY_REAL") ||
      die "durable evidence stage failed"
  else
    (cd "$REPO_REAL" && historical_closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
      go run ./spike/t4013/cmd/t4013-promote -stage "$stage" -root "$CEREMONY_REAL") ||
      die "durable evidence stage failed"
  fi
}

durable_discard_stage() {
  local stage="$1" filesystem_root="$2" plan_path="$3"
  [[ "$stage" == /* && "$filesystem_root" == /* &&
    -f "$stage" && ! -L "$stage" &&
    -d "$filesystem_root" && ! -L "$filesystem_root" &&
    "${stage%/*}" == "$filesystem_root" ]] ||
    die "discarded evidence stage is invalid"
  if is_v25_plan "$plan_path"; then
    initialize_v25_custody_commands
    require_v25_custody_command "$V25_PROMOTE_COMMAND" || die "V25 stage command is unavailable"
    (cd "$REPO_REAL" && closed_go \
      "$V25_PROMOTE_COMMAND" -discard "$stage" -root "$CEREMONY_REAL") ||
      die "durable evidence stage discard failed"
  else
    (cd "$REPO_REAL" && historical_closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
      go run ./spike/t4013/cmd/t4013-promote -discard "$stage" -root "$CEREMONY_REAL") ||
      die "durable evidence stage discard failed"
  fi
}

canonical_existing_directory() {
  local path="$1"
  [[ -d "$path" && ! -L "$path" ]] || die "directory is missing, invalid, or a symlink: $path"
  (cd "$path" && pwd -P)
}

validate_id() {
  local value="$1"
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "ceremony id is invalid"
  [[ "$value" != "." && "$value" != ".." ]] || die "ceremony id is invalid"
}

reject_review_stopped_id() {
  local value="$1" number
  if [[ "$value" =~ ^t40r1-neutral-([0-9]{2,})$ ]]; then
    number=$((10#${BASH_REMATCH[1]}))
    (( number > LAST_REVIEW_STOPPED_CEREMONY_NUMBER )) ||
      die "ceremony id $value is permanently review-stopped; use a fresh id"
  fi
}

initialize_repository() {
  [[ "$REPO_ROOT" == /* ]] || die "repository path must be absolute"
  REPO_REAL="$(canonical_existing_directory "$REPO_ROOT")"
}

initialize_ceremony_root() {
  [[ -n "${HOME:-}" && "$HOME" == /* && "$HOME" != "/" ]] || die "HOME must be an absolute non-root directory"
  [[ "$CEREMONY_ROOT" == /* ]] || die "ceremony root must be absolute"
  [[ -n "$REPO_REAL" ]] || initialize_repository
  mkdir -p -m 700 "$CEREMONY_ROOT"
  chmod 700 "$CEREMONY_ROOT"
  CEREMONY_REAL="$(canonical_existing_directory "$CEREMONY_ROOT")"
  case "$CEREMONY_REAL" in
    "/"|"$REPO_REAL"|"$REPO_REAL"/*) die "ceremony root overlaps the phebs checkout" ;;
  esac
  case "$REPO_REAL" in
    "$CEREMONY_REAL"/*) die "phebs checkout is inside the ceremony root" ;;
  esac
  SIGNING_ROOT="${CEREMONY_REAL}/signing"
  if [[ -e "$SIGNING_ROOT" || -L "$SIGNING_ROOT" ]]; then
    [[ -d "$SIGNING_ROOT" && ! -L "$SIGNING_ROOT" ]] || die "ceremony signing directory is invalid or symlinked"
  else
    mkdir -m 700 "$SIGNING_ROOT"
  fi
  chmod 700 "$SIGNING_ROOT"
}

select_signing_key() {
  local ceremony_id="$1"
  validate_id "$ceremony_id"
  [[ -n "$SIGNING_ROOT" ]] || die "ceremony root must be initialized before selecting a signer"
  SIGNING_KEY="${SIGNING_ROOT}/${ceremony_id}_ed25519"
  [[ "$SIGNING_KEY" == /* && "$(dirname "$SIGNING_KEY")" == "$SIGNING_ROOT" ]] ||
    die "signing key must be directly inside the dedicated signing directory"
}

require_clean_checkout() {
  local status commit
  status="$(closed_git -C "$REPO_REAL" status --porcelain=v1 --untracked-files=all)"
  [[ -z "$status" ]] || die "phebs checkout is not clean; commit or remove every tracked/untracked change"
  commit="$(closed_git -C "$REPO_REAL" rev-parse --verify HEAD)"
  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || die "phebs HEAD is not an exact commit"
  closed_git -C "$REPO_REAL" cat-file -e "${commit}^{commit}"
}

host_preflight() {
  local memory_bytes available_kib available_bytes go_version port
  [[ "$(uname -s)" == "Darwin" ]] || die "the large-machine driver supports macOS only"
  case "$BASE_PORT" in
    ''|*[!0-9]*) die "base port is invalid" ;;
  esac
  (( BASE_PORT >= 1024 && BASE_PORT <= 65533 )) || die "base port must leave room for two loopback listeners"
  memory_bytes="$(sysctl -n hw.memsize)"
  available_kib="$(df -Pk "$CEREMONY_REAL" | awk 'NR == 2 { print $4 }')"
  [[ "$memory_bytes" =~ ^[0-9]+$ && "$available_kib" =~ ^[0-9]+$ ]] || die "host capacity probes returned invalid values"
  available_bytes=$((available_kib * 1024))
  (( memory_bytes >= MINIMUM_MEMORY_BYTES )) || die "host has less than the frozen 24 GiB memory prerequisite"
  (( available_bytes >= MINIMUM_DISK_BYTES )) || die "host has less than the frozen 120 GiB available-disk prerequisite"
  go_version="$(closed_go go env GOVERSION)"
  [[ "$go_version" == go1.26.* ]] || die "the ceremony requires the Go 1.26 toolchain line"
  for port in "$BASE_PORT" "$((BASE_PORT + 1))"; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      die "ceremony loopback port is already in use: $port"
    fi
  done
  note "macOS host prerequisite: PASS"
  note "physical memory bytes: $memory_bytes"
  note "available ceremony bytes: $available_bytes"
  note "phebs commit: $(closed_git -C "$REPO_REAL" rev-parse HEAD)"
  note "Go toolchain: $(closed_go go version)"
  note "Git toolchain: $(closed_git --version)"
  note "SurrealDB toolchain: $(closed_surreal version)"
}

preflight() {
  local command_name commit host_plan_root
  for command_name in awk cmp cp date df du env find grep lsof mkdir mktemp pgrep ps rm sed shasum sort ssh-keygen sysctl tar uname uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  initialize_ceremony_root
  require_clean_checkout
  initialize_closed_go_cache
  host_preflight
  initialize_v25_custody_commands
  commit="$(closed_git -C "$REPO_REAL" rev-parse HEAD)"
  host_plan_root="$(mktemp -d "$CLOSED_TMP/phebs-t4013-host-plan.XXXXXX")"
  if ! run_v25_custody_command_in_repo_active "$V25_FREEZE_COMMAND" \
    -root "$REPO_REAL" \
    -source-commit "$commit" \
    -data-parent "$CEREMONY_REAL" \
    -bind-host-toolchain \
    -output "${host_plan_root}/plan.json" >/dev/null; then
    rm -rf -- "$host_plan_root"
    die "exact V25 prospective host preflight failed"
  fi
  rm -rf -- "$host_plan_root"
  validate_closed_controls
  require_clean_checkout
  note "T40.13 host, module, and prebuilt custody-command checks: PASS"
  note "process-launching regression and readiness suites are branch gates and are not re-run outside durable custody"
}

verification_preflight() {
  local command_name
  for command_name in awk cmp du env find grep mktemp pgrep ps rm sed shasum sort ssh-keygen tar uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  initialize_ceremony_root
  require_clean_checkout
  initialize_closed_go_cache
  initialize_v25_custody_commands
  note "source-free verifier checkout: PASS"
}

historical_require_clean_checkout() {
  local status commit
  status="$(git -C "$REPO_REAL" status --porcelain=v1 --untracked-files=all)"
  [[ -z "$status" ]] || die "phebs checkout is not clean; commit or remove every tracked/untracked change"
  commit="$(git -C "$REPO_REAL" rev-parse --verify HEAD)"
  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || die "phebs HEAD is not an exact commit"
  git -C "$REPO_REAL" cat-file -e "${commit}^{commit}"
}

historical_host_preflight() {
  local memory_bytes available_kib available_bytes go_version port
  [[ "$(uname -s)" == "Darwin" ]] || die "the large-machine driver supports macOS only"
  case "$BASE_PORT" in
    ''|*[!0-9]*) die "base port is invalid" ;;
  esac
  (( BASE_PORT >= 1024 && BASE_PORT <= 65533 )) || die "base port must leave room for two loopback listeners"
  memory_bytes="$(sysctl -n hw.memsize)"
  available_kib="$(df -Pk "$CEREMONY_REAL" | awk 'NR == 2 { print $4 }')"
  [[ "$memory_bytes" =~ ^[0-9]+$ && "$available_kib" =~ ^[0-9]+$ ]] || die "host capacity probes returned invalid values"
  available_bytes=$((available_kib * 1024))
  (( memory_bytes >= MINIMUM_MEMORY_BYTES )) || die "host has less than the frozen 24 GiB memory prerequisite"
  (( available_bytes >= MINIMUM_DISK_BYTES )) || die "host has less than the frozen 120 GiB available-disk prerequisite"
  go_version="$(go env GOVERSION)"
  [[ "$go_version" == go1.26.* ]] || die "the ceremony requires the Go 1.26 toolchain line"
  for port in "$BASE_PORT" "$((BASE_PORT + 1))"; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      die "ceremony loopback port is already in use: $port"
    fi
  done
  note "macOS host prerequisite: PASS"
  note "physical memory bytes: $memory_bytes"
  note "available ceremony bytes: $available_bytes"
  note "phebs commit: $(git -C "$REPO_REAL" rev-parse HEAD)"
  note "Go toolchain: $(go version)"
  note "Git toolchain: $(git --version)"
  note "SurrealDB toolchain: $(surreal version)"
}

historical_preflight() {
  local command_name
  PATH="$HOST_PATH"
  export PATH
  for command_name in awk cmp cp date df find git go grep lsof mkdir mktemp mv rm sed shasum sort ssh-keygen surreal sysctl tar uname uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  initialize_ceremony_root
  historical_require_clean_checkout
  historical_host_preflight
  (cd "$REPO_REAL" && go mod download all)
  (cd "$REPO_REAL" && go test ./spike/t4013/... -count=1)
  historical_require_clean_checkout
  note "T40.13 harness tests: PASS"
}

historical_verification_preflight() {
  local command_name
  PATH="$HOST_PATH"
  export PATH
  for command_name in awk cmp find git go mktemp rm sed shasum sort ssh-keygen tar uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  historical_require_clean_checkout
  (cd "$REPO_REAL" && go test ./spike/t4013/... -count=1)
  note "source-free verifier checkout: PASS"
}

preflight_for_plan() {
  if is_v25_plan "$1"; then
    preflight
  else
    historical_preflight
  fi
}

verification_preflight_for_plan() {
  if is_v25_plan "$1"; then
    verification_preflight
  else
    historical_verification_preflight
  fi
}

prove_signing_keypair() {
  local private_public public_key
  [[ -f "$SIGNING_KEY" && ! -L "$SIGNING_KEY" && -f "${SIGNING_KEY}.pub" && ! -L "${SIGNING_KEY}.pub" ]] ||
    die "ceremony signing keypair is partial, invalid, or symlinked"
  private_public="$(ssh-keygen -y -f "$SIGNING_KEY" | awk 'NR == 1 && NF >= 2 { print $1 " " $2 }')" ||
    die "ceremony signing private key cannot derive its public identity"
  public_key="$(awk 'NR == 1 && NF >= 2 { print $1 " " $2; next } { exit 2 } END { if (NR != 1) exit 2 }' "${SIGNING_KEY}.pub")" ||
    die "ceremony signing public key is unreadable"
  [[ "$private_public" == ssh-ed25519\ * && "$private_public" == "$public_key" ]] ||
    die "ceremony signing private/public keypair does not match"
}

admit_signing_key() {
  local fingerprint
  prove_signing_keypair
  fingerprint="$(ssh-keygen -lf "${SIGNING_KEY}.pub" -E sha256 | awk '{ print $2 }')"
  [[ "$fingerprint" == SHA256:* ]] || die "ceremony signer fingerprint is invalid"
  [[ "$fingerprint" != "$RETIRED_SIGNER_FINGERPRINT" ]] ||
    die "the selected ceremony signer is retired and may not sign new evidence"
}

ensure_signing_key() {
  if [[ ! -e "$SIGNING_KEY" && ! -L "$SIGNING_KEY" && ! -e "${SIGNING_KEY}.pub" && ! -L "${SIGNING_KEY}.pub" ]]; then
    ssh-keygen -q -t ed25519 -N "" -C "phebs-t4013-ceremony" -f "$SIGNING_KEY"
    chmod 600 "$SIGNING_KEY"
    chmod 644 "${SIGNING_KEY}.pub"
    note "created ceremony signing key: $SIGNING_KEY"
    note "back up this key separately before relying on its identity"
  fi
  admit_signing_key
}

run_root_for() {
  local ceremony_id="$1"
  validate_id "$ceremony_id"
  printf '%s/%s\n' "$CEREMONY_REAL" "$ceremony_id"
}

plan_digest_for() {
  local plan_path="$1"
  require_v25_custody_command "$V25_INSPECT_COMMAND" ||
    die "bounded exact-control inspector is unavailable"
  run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" -plan-digest "$plan_path" ||
    die "plan failed bounded exact-control inspection"
}

freeze() {
  local ceremony_id="$1" run_root evidence_root private_root commit digest frozen_at public_key fingerprint
  local signer_tmp allowed_tmp freeze_tmp freeze_signature_tmp
  reject_review_stopped_id "$ceremony_id"
  initialize_repository
  initialize_ceremony_root
  select_signing_key "$ceremony_id"
  ensure_signing_key
  run_root="$(run_root_for "$ceremony_id")"
  [[ ! -e "$run_root" && ! -L "$run_root" ]] || die "ceremony id already exists and may not be overwritten: $ceremony_id"
  preflight
  evidence_root="${run_root}/evidence"
  private_root="${run_root}/private"
  mkdir -m 700 "$run_root" "$evidence_root" "$private_root"
  commit="$(closed_git -C "$REPO_REAL" rev-parse HEAD)"
  run_v25_custody_command_in_repo_active "$V25_FREEZE_COMMAND" \
    -root "$REPO_REAL" \
    -source-commit "$commit" \
    -data-parent "$CEREMONY_REAL" \
    -bind-host-toolchain \
    -output "${evidence_root}/plan.json" >/dev/null
  digest="$(plan_digest_for "${evidence_root}/plan.json")"
  frozen_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  public_key="$(awk 'NF >= 2 { print $1 " " $2; exit }' "${SIGNING_KEY}.pub")"
  fingerprint="$(ssh-keygen -lf "${SIGNING_KEY}.pub" -E sha256 | awk '{print $2}')"
  [[ "$public_key" == ssh-ed25519\ * && "$fingerprint" == SHA256:* ]] ||
    die "ceremony signing identity is invalid"
  signer_tmp="${evidence_root}/signer.pub.tmp"
  allowed_tmp="${evidence_root}/allowed_signers.tmp"
  freeze_tmp="${evidence_root}/freeze.json.tmp"
  freeze_signature_tmp="${freeze_tmp}.sig"
  cp -p "${SIGNING_KEY}.pub" "$signer_tmp"
  durable_promote "$signer_tmp" "${evidence_root}/signer.pub" "$evidence_root" "${evidence_root}/plan.json"
  printf '%s %s\n' "$SIGNER_IDENTITY" "$public_key" > "$allowed_tmp"
  durable_promote "$allowed_tmp" "${evidence_root}/allowed_signers" "$evidence_root" "${evidence_root}/plan.json"
  printf '{\n  "schema": "t4013-freeze-envelope-v1",\n  "ceremony_id": "%s",\n  "source_commit": "%s",\n  "plan_digest": "%s",\n  "signer_fingerprint": "%s",\n  "frozen_at": "%s"\n}\n' \
    "$ceremony_id" "$commit" "$digest" "$fingerprint" "$frozen_at" > "$freeze_tmp"
  ssh-keygen -Y sign -f "$SIGNING_KEY" -n "$FREEZE_SIGNATURE_NAMESPACE" \
    "$freeze_tmp" >/dev/null
  durable_promote "$freeze_tmp" "${evidence_root}/freeze.json" "$evidence_root" "${evidence_root}/plan.json"
  durable_promote "$freeze_signature_tmp" "${evidence_root}/freeze.json.sig" "$evidence_root" "${evidence_root}/plan.json"
  note "frozen ceremony: $ceremony_id"
  note "source commit: $commit"
  note "plan path: ${evidence_root}/plan.json"
  note "plan digest: $digest"
  note "signer fingerprint: $fingerprint"
  note "STOP: independently review this plan before execute."
}

cleanup_prepared() {
  local plan_path="$1" prepared_path="$2" path
  if [[ -e "$prepared_path" || -L "$prepared_path" ||
    -e "${prepared_path}.tmp" || -L "${prepared_path}.tmp" ||
    -e "${prepared_path}.preparing" || -L "${prepared_path}.preparing" ]]; then
    for path in "$prepared_path" "${prepared_path}.tmp" "${prepared_path}.preparing"; do
      if [[ -e "$path" || -L "$path" ]]; then
        [[ -f "$path" && ! -L "$path" ]] || die "private prepared cleanup control is invalid: $path"
      fi
    done
    if is_v25_plan "$plan_path"; then
      require_v25_custody_command "$V25_CLEANUP_COMMAND" || return 1
      run_v25_custody_command_in_repo_active "$V25_CLEANUP_COMMAND" \
        -root "$REPO_REAL" \
        -plan "$plan_path" \
        -prepared "$prepared_path" \
        -confirm "$CLEANUP_CONFIRM"
    else
      (cd "$REPO_REAL" && plan_go "$plan_path" \
        go run ./spike/t4013/cmd/t4013-cleanup \
        -root "$REPO_REAL" \
        -plan "$plan_path" \
        -prepared "$prepared_path" \
        -confirm "$CLEANUP_CONFIRM")
    fi
  fi
}

refuse_supervision_residue() {
  local supervision_path="${1}.t4013-supervision"
  run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" \
    -directory "$(dirname "$supervision_path")" \
    -forbid-prefix "$(basename "$supervision_path")" -maximum-entries 4096 ||
    die "durable custody supervision remains or exceeds its inspection bound; receipt and seal are refused"
}

manifest_value() {
  local manifest="$1" key="$2"
  run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" \
    -json-value "$manifest" -key "$key"
}

require_exact_inventory() {
  local directory="$1"
  shift
  run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" \
    -exact-directory "$directory" -- "$@" ||
    die "evidence directory contains an unexpected or missing path"
}

verify_frozen_identity() {
  local evidence_root="$1" trusted_allowed_signers="${2:-${1}/allowed_signers}"
  local plan_path source_commit plan_digest
  plan_path="${evidence_root}/plan.json"
  ssh-keygen -Y verify \
    -f "$trusted_allowed_signers" \
    -I "$SIGNER_IDENTITY" \
    -n "$FREEZE_SIGNATURE_NAMESPACE" \
    -s "${evidence_root}/freeze.json.sig" < "${evidence_root}/freeze.json" ||
    die "frozen plan identity signature is not authentic"
  source_commit="$(manifest_value "${evidence_root}/freeze.json" source_commit)"
  plan_digest="$(manifest_value "${evidence_root}/freeze.json" plan_digest)"
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_commit" == "$(plan_git "$plan_path" -C "$REPO_REAL" rev-parse HEAD)" ]] ||
    die "verification checkout differs from the frozen execution commit"
  [[ "$plan_digest" == "$(plan_digest_for "${evidence_root}/plan.json")" ]] || die "frozen plan digest differs"
}

verify_checksum_inventory() {
  local evidence_root="$1" trusted_allowed_signers="$2"
  run_v25_custody_command_in_repo_active "$V25_INSPECT_COMMAND" \
    -checksums "${evidence_root}/SHA256SUMS" ||
    die "checksum inventory is not one bounded canonical value"
  ssh-keygen -Y verify \
    -f "$trusted_allowed_signers" \
    -I "$SIGNER_IDENTITY" \
    -n "$SIGNATURE_NAMESPACE" \
    -s "${evidence_root}/SHA256SUMS.sig" < "${evidence_root}/SHA256SUMS" ||
    die "returned checksum inventory signature is not authentic"
  (cd "$evidence_root" && shasum -a 256 -c SHA256SUMS) ||
    die "returned evidence checksum differs"
}

verify_evidence_directory() {
  local evidence_root="$1" trusted_allowed_signers="${2:-${1}/allowed_signers}"
  local manifest source_commit plan_digest temporary_root rebuilt temporary_parent
  local required
  require_exact_inventory "$evidence_root" \
    allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json \
    SHA256SUMS SHA256SUMS.sig signer.pub
  for required in allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json SHA256SUMS SHA256SUMS.sig signer.pub; do
    [[ -f "${evidence_root}/${required}" && ! -L "${evidence_root}/${required}" ]] ||
      die "source-free evidence is missing or symlinked: $required"
  done
  manifest="${evidence_root}/manifest.json"
  verify_checksum_inventory "$evidence_root" "$trusted_allowed_signers"
  cmp -s "$trusted_allowed_signers" "${evidence_root}/allowed_signers" ||
    die "bundled allowed signer identity differs from the authenticated signer"
  verify_frozen_identity "$evidence_root" "$trusted_allowed_signers"
  source_commit="$(manifest_value "$manifest" source_commit)"
  plan_digest="$(manifest_value "$manifest" plan_digest)"
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_commit" == "$(plan_git "${evidence_root}/plan.json" -C "$REPO_REAL" rev-parse HEAD)" ]] ||
    die "verification checkout differs from the sealed execution commit"
  [[ "$plan_digest" == "$(plan_digest_for "${evidence_root}/plan.json")" ]] || die "sealed plan digest differs"
  temporary_parent="${TMPDIR:-/tmp}"
  if is_v25_plan "${evidence_root}/plan.json"; then
    temporary_parent="$CLOSED_TMP"
  fi
  temporary_root="$(mktemp -d "$temporary_parent/phebs-t4013-verify.XXXXXX")"
  rebuilt="${temporary_root}/results.json"
  if is_v25_plan "${evidence_root}/plan.json"; then
    run_v25_custody_command_in_repo_active "$V25_RECEIPT_COMMAND" \
      -plan "${evidence_root}/plan.json" \
      -plan-digest "$plan_digest" \
      -observation "${evidence_root}/observation.json" \
      -output "$rebuilt" || {
        rm -rf -- "$temporary_root"
        die "source-free receipt could not be rebuilt"
      }
  elif ! (cd "$REPO_REAL" && plan_go "${evidence_root}/plan.json" \
    go run ./spike/t4013/cmd/t4013-receipt \
      -plan "${evidence_root}/plan.json" \
      -plan-digest "$plan_digest" \
      -observation "${evidence_root}/observation.json" \
      -output "$rebuilt"); then
    rm -rf -- "$temporary_root"
    die "source-free receipt could not be rebuilt"
  fi
  if ! cmp -s "$rebuilt" "${evidence_root}/results.json"; then
    rm -rf -- "$temporary_root"
    die "rebuilt receipt differs from the sealed receipt"
  fi
  rm -rf -- "$temporary_root"
  note "source-free evidence verification: PASS"
}

seal_manifest_matches() {
  local path="$1" ceremony_id="$2" source_commit="$3" plan_digest="$4" sealed_at
  [[ "$(manifest_value "$path" schema)" == "t4013-source-free-transfer-v1" &&
    "$(manifest_value "$path" ceremony_id)" == "$ceremony_id" &&
    "$(manifest_value "$path" source_commit)" == "$source_commit" &&
    "$(manifest_value "$path" plan_digest)" == "$plan_digest" ]] || return 1
  sealed_at="$(manifest_value "$path" sealed_at)"
  [[ "$sealed_at" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]
}

complete_evidence_seal() {
  local ceremony_id="$1" evidence_root="$2" plan_path="$3" source_commit="$4" plan_digest="$5"
  local generated_at manifest_tmp checksums_tmp signature_tmp checksum_input seal_name expected_checksums
  local -a expected
  manifest_tmp="${evidence_root}/manifest.json.tmp"
  checksums_tmp="${evidence_root}/SHA256SUMS.tmp"
  signature_tmp="${checksums_tmp}.sig"

  prove_signing_keypair
  cmp -s "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub" ||
    die "ceremony signing key changed after freeze"
  verify_frozen_identity "$evidence_root"
  expected=(allowed_signers freeze.json freeze.json.sig observation.json plan.json results.json signer.pub)
  for seal_name in manifest.json manifest.json.tmp SHA256SUMS SHA256SUMS.tmp SHA256SUMS.sig SHA256SUMS.tmp.sig; do
    if [[ -e "${evidence_root}/${seal_name}" || -L "${evidence_root}/${seal_name}" ]]; then
      [[ -f "${evidence_root}/${seal_name}" && ! -L "${evidence_root}/${seal_name}" ]] ||
        die "partial source-free seal path is invalid: $seal_name"
      expected+=("$seal_name")
    fi
  done
  require_exact_inventory "$evidence_root" "${expected[@]}"

  if [[ -e "${evidence_root}/manifest.json" ]]; then
    seal_manifest_matches "${evidence_root}/manifest.json" "$ceremony_id" "$source_commit" "$plan_digest" ||
      die "source-free seal manifest authority differs from the frozen run"
    if [[ -e "$manifest_tmp" ]]; then
      if cmp -s "$manifest_tmp" "${evidence_root}/manifest.json"; then
        durable_promote "$manifest_tmp" "${evidence_root}/manifest.json" "$evidence_root" "$plan_path"
      else
        durable_discard_stage "$manifest_tmp" "$evidence_root" "$plan_path"
      fi
    fi
  else
    if [[ -e "$manifest_tmp" ]] &&
      ! seal_manifest_matches "$manifest_tmp" "$ceremony_id" "$source_commit" "$plan_digest"; then
      durable_discard_stage "$manifest_tmp" "$evidence_root" "$plan_path"
    fi
    if [[ ! -e "$manifest_tmp" ]]; then
      generated_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
      printf '{\n  "schema": "t4013-source-free-transfer-v1",\n  "ceremony_id": "%s",\n  "source_commit": "%s",\n  "plan_digest": "%s",\n  "sealed_at": "%s"\n}\n' \
        "$ceremony_id" "$source_commit" "$plan_digest" "$generated_at" > "$manifest_tmp"
    fi
    seal_manifest_matches "$manifest_tmp" "$ceremony_id" "$source_commit" "$plan_digest" ||
      die "new source-free seal manifest is invalid"
    durable_stage "$manifest_tmp" "$evidence_root" "$plan_path"
    durable_promote "$manifest_tmp" "${evidence_root}/manifest.json" "$evidence_root" "$plan_path"
  fi

  if [[ -e "${evidence_root}/SHA256SUMS" && -e "${evidence_root}/SHA256SUMS.sig" &&
    ! -e "$manifest_tmp" && ! -e "$checksums_tmp" && ! -e "$signature_tmp" ]]; then
    require_exact_inventory "$evidence_root" \
      allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json \
      SHA256SUMS SHA256SUMS.sig signer.pub
    verify_evidence_directory "$evidence_root"
    return
  fi

  expected_checksums="$(cd "$evidence_root" && shasum -a 256 \
    allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json signer.pub)"
  if [[ -e "${evidence_root}/SHA256SUMS" ]]; then
    cmp -s "${evidence_root}/SHA256SUMS" <(printf '%s\n' "$expected_checksums") ||
      die "partial source-free checksum authority differs from the frozen run"
  fi
  if [[ -e "$checksums_tmp" ]]; then
    if ! cmp -s "$checksums_tmp" <(printf '%s\n' "$expected_checksums"); then
      durable_discard_stage "$checksums_tmp" "$evidence_root" "$plan_path"
    fi
  fi
  if [[ ! -e "$checksums_tmp" && (! -e "${evidence_root}/SHA256SUMS" ||
    (! -e "${evidence_root}/SHA256SUMS.sig" && ! -e "$signature_tmp")) ]]; then
    printf '%s\n' "$expected_checksums" > "$checksums_tmp"
  fi
  if [[ -e "$checksums_tmp" ]]; then
    durable_stage "$checksums_tmp" "$evidence_root" "$plan_path"
    checksum_input="$checksums_tmp"
  else
    checksum_input="${evidence_root}/SHA256SUMS"
  fi

  if [[ -e "${evidence_root}/SHA256SUMS.sig" ]]; then
    ssh-keygen -Y verify -f "${evidence_root}/allowed_signers" -I "$SIGNER_IDENTITY" \
      -n "$SIGNATURE_NAMESPACE" -s "${evidence_root}/SHA256SUMS.sig" < "$checksum_input" >/dev/null 2>&1 ||
      die "partial source-free checksum signature authority is not authentic"
  fi
  if [[ -e "$signature_tmp" ]]; then
    if ! ssh-keygen -Y verify -f "${evidence_root}/allowed_signers" -I "$SIGNER_IDENTITY" \
      -n "$SIGNATURE_NAMESPACE" -s "$signature_tmp" < "$checksum_input" >/dev/null 2>&1; then
      durable_discard_stage "$signature_tmp" "$evidence_root" "$plan_path"
    elif [[ -e "${evidence_root}/SHA256SUMS.sig" ]] &&
      ! cmp -s "$signature_tmp" "${evidence_root}/SHA256SUMS.sig"; then
      durable_discard_stage "$signature_tmp" "$evidence_root" "$plan_path"
    fi
  fi
  if [[ ! -e "$signature_tmp" && ! -e "${evidence_root}/SHA256SUMS.sig" ]]; then
    [[ "$checksum_input" == "$checksums_tmp" ]] ||
      die "partial source-free seal cannot stage its missing signature"
    ssh-keygen -Y sign -f "$SIGNING_KEY" -n "$SIGNATURE_NAMESPACE" "$checksums_tmp" >/dev/null
    ssh-keygen -Y verify -f "${evidence_root}/allowed_signers" -I "$SIGNER_IDENTITY" \
      -n "$SIGNATURE_NAMESPACE" -s "$signature_tmp" < "$checksums_tmp" >/dev/null 2>&1 ||
      die "new source-free checksum signature is not authentic"
  fi

  if [[ -e "$signature_tmp" ]]; then
    durable_stage "$signature_tmp" "$evidence_root" "$plan_path"
    durable_promote "$signature_tmp" "${evidence_root}/SHA256SUMS.sig" "$evidence_root" "$plan_path"
  fi
  if [[ -e "$checksums_tmp" ]]; then
    durable_promote "$checksums_tmp" "${evidence_root}/SHA256SUMS" "$evidence_root" "$plan_path"
  fi
  require_exact_inventory "$evidence_root" \
    allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json \
    SHA256SUMS SHA256SUMS.sig signer.pub
  verify_evidence_directory "$evidence_root"
}

seal_evidence() {
  local ceremony_id="$1" run_root evidence_root plan_path source_commit plan_digest
  local package package_tmp package_digest package_sidecar package_sidecar_tmp package_bytes
  local reviewed_signer_fingerprint
  run_root="$(run_root_for "$ceremony_id")"
  evidence_root="${run_root}/evidence"
  plan_path="${evidence_root}/plan.json"
  source_commit="$(plan_git "$plan_path" -C "$REPO_REAL" rev-parse HEAD)"
  plan_digest="$(plan_digest_for "$plan_path")"
  complete_evidence_seal "$ceremony_id" "$evidence_root" "$plan_path" "$source_commit" "$plan_digest"
  reviewed_signer_fingerprint="$(ssh-keygen -lf "${SIGNING_KEY}.pub" -E sha256 | awk 'NR == 1 { print $2 }')"
  [[ "$reviewed_signer_fingerprint" =~ ^SHA256:[A-Za-z0-9+/]{43}$ ]] ||
    die "ceremony signer fingerprint is invalid before package verification"
  package="${run_root}/${ceremony_id}-source-free.tgz"
  package_tmp="${package}.tmp"
  package_sidecar="${package}.sha256"
  package_sidecar_tmp="${package_sidecar}.tmp"
  if [[ -e "$package_sidecar_tmp" || -L "$package_sidecar_tmp" ]]; then
    [[ -f "$package_sidecar_tmp" && ! -L "$package_sidecar_tmp" ]] ||
      die "source-free package sidecar temporary file is invalid"
    rm -- "$package_sidecar_tmp"
  fi
  if [[ -e "$package" || -L "$package" ]]; then
    [[ -f "$package" && ! -L "$package" && ! -e "$package_tmp" && ! -L "$package_tmp" ]] ||
      die "source-free package is partial or invalid"
    verify_bundle "$package" --reviewed-signer-fingerprint "$reviewed_signer_fingerprint"
    package_digest="$(shasum -a 256 "$package" | awk '{print $1}')"
    if [[ -e "$package_sidecar" || -L "$package_sidecar" ]]; then
      [[ -f "$package_sidecar" && ! -L "$package_sidecar" ]] || die "source-free package sidecar is invalid"
      [[ "$(awk 'NR == 1 { print $1, $2 }' "$package_sidecar")" == "$package_digest $(basename "$package")" ]] ||
        die "source-free package sidecar differs"
    else
      printf '%s  %s\n' "$package_digest" "$(basename "$package")" > "$package_sidecar_tmp"
      durable_promote "$package_sidecar_tmp" "$package_sidecar" "$run_root" "$plan_path"
    fi
    note "sealed source-free package: $package"
    note "package sha256: $package_digest"
    return
  fi
  if [[ -e "$package_tmp" || -L "$package_tmp" ]]; then
    [[ -f "$package_tmp" && ! -L "$package_tmp" ]] || die "source-free package temporary file is invalid"
    rm -- "$package_tmp"
  fi
  if [[ -e "$package_sidecar" || -L "$package_sidecar" ]]; then
    die "source-free package sidecar exists without its package"
  fi
  COPYFILE_DISABLE=1 tar -C "$run_root" -czf "$package_tmp" evidence
  package_bytes="$(wc -c < "$package_tmp" | awk '{ print $1 }')"
  [[ "$package_bytes" =~ ^[0-9]+$ ]] || die "source-free package size is invalid"
  (( package_bytes > 0 && package_bytes <= MAXIMUM_TRANSFER_PACKAGE_BYTES )) ||
    die "source-free package exceeds its fixed 4-MiB transfer bound"
  durable_promote "$package_tmp" "$package" "$run_root" "$plan_path"
  verify_bundle "$package" --reviewed-signer-fingerprint "$reviewed_signer_fingerprint"
  package_digest="$(shasum -a 256 "$package" | awk '{print $1}')"
  printf '%s  %s\n' "$package_digest" "$(basename "$package")" > "$package_sidecar_tmp"
  durable_promote "$package_sidecar_tmp" "$package_sidecar" "$run_root" "$plan_path"
  note "sealed source-free package: $package"
  note "package sha256: $package_digest"
}

prepare_receipt_for_seal() {
  local plan_path="$1" plan_digest="$2" observation_path="$3" results_path="$4"
  if is_v25_plan "$plan_path"; then
    run_v25_custody_command_in_repo_active "$V25_RECEIPT_COMMAND" \
      -plan "$plan_path" \
      -plan-digest "$plan_digest" \
      -observation "$observation_path" \
      -output "$results_path"
  elif [[ ! -e "$results_path" && ! -L "$results_path" ]]; then
    # Historical publication retains its original create-only behavior.
    (cd "$REPO_REAL" && plan_go "$plan_path" \
      go run ./spike/t4013/cmd/t4013-receipt \
      -plan "$plan_path" \
      -plan-digest "$plan_digest" \
      -observation "$observation_path" \
      -output "$results_path")
  else
    [[ -f "$results_path" && ! -L "$results_path" ]] ||
      die "historical source-free receipt is absent or invalid"
  fi
}

seal_run() {
  local ceremony_id="$1" run_root evidence_root private_root plan_path prepared_path custody_path plan_digest
  local private_name
  local -a private_expected=()
  initialize_repository
  initialize_ceremony_root
  run_root="$(run_root_for "$ceremony_id")"
  evidence_root="${run_root}/evidence"
  private_root="${run_root}/private"
  plan_path="${evidence_root}/plan.json"
  prepared_path="${private_root}/prepared.json"
  custody_path="${run_root}/custody"
  [[ -d "$run_root" && ! -L "$run_root" ]] || die "ceremony run directory is invalid"
  [[ -f "$plan_path" && ! -L "$plan_path" ]] || die "frozen plan is missing or symlinked"
  select_signing_key "$ceremony_id"
  admit_signing_key
  if [[ -e "$custody_path" || -L "$custody_path" ]]; then
    [[ -d "$custody_path" && ! -L "$custody_path" ]] || die "private custody is invalid"
    [[ ! -e "${custody_path}/.t4013-executed" && ! -L "${custody_path}/.t4013-executed" ]] ||
      die "marker-bearing executed custody remains for separately reviewed purge"
  fi
  if ! is_v25_plan "$plan_path"; then
    initialize_historical_go_cache
  fi
  acquire_run_lock "$run_root"
  verification_preflight_for_plan "$plan_path"
  if is_v25_plan "$plan_path"; then
    initialize_v25_custody_commands
  fi
  [[ -d "$evidence_root" && ! -L "$evidence_root" && -d "$private_root" && ! -L "$private_root" ]] ||
    die "ceremony evidence or private directory is invalid"
  plan_digest="$(plan_digest_for "$plan_path")"
  if is_v25_plan "$plan_path" &&
    [[ -e "${evidence_root}/observation.json.teardown" || -L "${evidence_root}/observation.json.teardown" ||
      -e "${evidence_root}/observation.json.teardown.tmp" || -L "${evidence_root}/observation.json.teardown.tmp" ]]; then
    prepare_receipt_for_seal \
      "$plan_path" "$plan_digest" \
      "${evidence_root}/observation.json" "${evidence_root}/results.json"
  fi
  if [[ -e "$prepared_path" || -L "$prepared_path" ||
    -e "${prepared_path}.tmp" || -L "${prepared_path}.tmp" ||
    -e "${prepared_path}.preparing" || -L "${prepared_path}.preparing" ]]; then
    for private_name in prepared.json prepared.json.tmp prepared.json.preparing; do
      if [[ -e "${private_root}/${private_name}" || -L "${private_root}/${private_name}" ]]; then
        private_expected+=("$private_name")
      fi
    done
    require_exact_inventory "$private_root" "${private_expected[@]}"
    if ! cleanup_prepared "${evidence_root}/plan.json" "$prepared_path"; then
      EXIT_UNPROVEN_REASON="resumable prepared cleanup refused"
      die "resumable private prepared manifest cleanup failed"
    fi
  fi
  [[ ! -e "$custody_path" && ! -L "$custody_path" ]] || die "private custody remains"
  require_exact_inventory "$private_root"
  refuse_supervision_residue "$custody_path"
  require_clean_checkout
  prepare_receipt_for_seal \
    "$plan_path" "$plan_digest" \
    "${evidence_root}/observation.json" "${evidence_root}/results.json"
  [[ -f "${evidence_root}/observation.json" && ! -L "${evidence_root}/observation.json" ]] ||
    die "source-free observation is absent after resume"
  refuse_supervision_residue "$custody_path"
  seal_evidence "$ceremony_id"
}

execute_ceremony() {
  local ceremony_id="$1" approved_digest="$2" approval="$3"
  local run_root evidence_root private_root plan_path prepared_path observation_path results_path custody_path
  local actual_digest prepare_status execute_status path supervision_path
  reject_review_stopped_id "$ceremony_id"
  [[ "$approval" == "$EXECUTE_APPROVAL" ]] || die "execution approval phrase is invalid"
  initialize_repository
  initialize_ceremony_root
  run_root="$(run_root_for "$ceremony_id")"
  evidence_root="${run_root}/evidence"
  private_root="${run_root}/private"
  plan_path="${evidence_root}/plan.json"
  prepared_path="${private_root}/prepared.json"
  observation_path="${evidence_root}/observation.json"
  results_path="${evidence_root}/results.json"
  custody_path="${run_root}/custody"
  supervision_path="${custody_path}.t4013-supervision"
  [[ -d "$run_root" && ! -L "$run_root" && -d "$evidence_root" && -d "$private_root" ]] ||
    die "frozen ceremony directory is missing or invalid"
  acquire_run_lock "$run_root"
  [[ -f "$plan_path" && ! -L "$plan_path" ]] || die "frozen plan is missing or symlinked"
  select_signing_key "$ceremony_id"
  admit_signing_key
  if ! is_v25_plan "$plan_path"; then
    initialize_historical_go_cache
  fi
  for path in "$prepared_path" "${prepared_path}.tmp" "${prepared_path}.preparing" \
    "$observation_path" "${observation_path}.tmp" \
    "${observation_path}.teardown" "${observation_path}.teardown.tmp" \
    "$results_path" "${results_path}.tmp" "$custody_path" "$supervision_path"; do
    [[ ! -e "$path" && ! -L "$path" ]] || die "ceremony output or custody already exists: $path"
  done
  refuse_supervision_residue "$custody_path"
  actual_digest="$(plan_digest_for "$plan_path")"
  [[ "$approved_digest" == "$actual_digest" ]] || die "approved plan digest differs from the frozen plan"
  require_exact_inventory "$evidence_root" allowed_signers freeze.json freeze.json.sig plan.json signer.pub
  cmp -s "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub" || die "ceremony signing key changed after freeze"
  verify_frozen_identity "$evidence_root"
  # Run the costly production/rehearsal preflight only after the requested
  # frozen identity and empty output state have passed their cheap checks.
  preflight_for_plan "$plan_path"
  [[ -d "$run_root" && ! -L "$run_root" && -d "$evidence_root" && -d "$private_root" ]] ||
    die "frozen ceremony directory changed during preflight"
  [[ -f "$plan_path" && ! -L "$plan_path" ]] || die "frozen plan changed during preflight"
  for path in "$prepared_path" "${prepared_path}.tmp" "${prepared_path}.preparing" \
    "$observation_path" "${observation_path}.tmp" \
    "${observation_path}.teardown" "${observation_path}.teardown.tmp" \
    "$results_path" "${results_path}.tmp" "$custody_path" "$supervision_path"; do
    [[ ! -e "$path" && ! -L "$path" ]] || die "ceremony output or custody appeared during preflight: $path"
  done
  refuse_supervision_residue "$custody_path"
  actual_digest="$(plan_digest_for "$plan_path")"
  [[ "$approved_digest" == "$actual_digest" ]] || die "approved plan digest changed during preflight"
  require_exact_inventory "$evidence_root" allowed_signers freeze.json freeze.json.sig plan.json signer.pub
  cmp -s "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub" || die "ceremony signing key changed during preflight"
  verify_frozen_identity "$evidence_root"
  EXIT_PREPARED_PLAN="$plan_path"
  EXIT_PREPARED_MANIFEST="$prepared_path"
  EXIT_PREPARED_WORKSPACE="$custody_path"
  prepare_status=0
  if is_v25_plan "$plan_path"; then
    require_v25_custody_command "$V25_PREPARE_COMMAND" ||
      die "V25 Prepare cannot start without its prebuilt command"
    run_v25_custody_command_in_repo_active "$V25_PREPARE_COMMAND" \
      -root "$REPO_REAL" \
      -workspace "$custody_path" \
      -plan "$plan_path" \
      -output "$prepared_path" \
      -base-port "$BASE_PORT" \
      -confirm "$PREPARE_CONFIRM" || prepare_status=$?
  else
    plan_go_in_repo_active "$plan_path" \
      go run ./spike/t4013/cmd/t4013-prepare \
      -root "$REPO_REAL" \
      -workspace "$custody_path" \
      -plan "$plan_path" \
      -output "$prepared_path" \
      -base-port "$BASE_PORT" \
      -confirm "$PREPARE_CONFIRM" || prepare_status=$?
  fi
  if (( prepare_status != 0 )); then
    if is_v25_plan "$plan_path" && [[ -e "$custody_path" || -L "$custody_path" ||
      -e "$supervision_path" || -L "$supervision_path" ||
      -e "$prepared_path" || -L "$prepared_path" ||
      -e "${prepared_path}.tmp" || -L "${prepared_path}.tmp" ||
      -e "${prepared_path}.preparing" || -L "${prepared_path}.preparing" ]]; then
      EXIT_UNPROVEN_REASON="prepare child status ${prepare_status} with retained operation state"
    fi
    die "preparation stopped with status ${prepare_status}"
  fi
  # The fallback owns only an interrupted Prepare. From this boundary onward,
  # Execute owns custody and any retained residue.
  EXIT_PREPARED_PLAN=""
  EXIT_PREPARED_MANIFEST=""
  EXIT_PREPARED_WORKSPACE=""
  execute_status=0
  if is_v25_plan "$plan_path"; then
    require_v25_custody_command "$V25_EXECUTE_COMMAND" ||
      die "V25 Execute cannot start without its prebuilt command"
    run_v25_custody_command_in_repo_active "$V25_EXECUTE_COMMAND" \
      -root "$REPO_REAL" \
      -plan "$plan_path" \
      -prepared "$prepared_path" \
      -observation "$observation_path" \
      -confirm "$EXECUTE_CONFIRM" || execute_status=$?
  else
    plan_go_in_repo_active "$plan_path" \
      go run ./spike/t4013/cmd/t4013-execute \
      -root "$REPO_REAL" \
      -plan "$plan_path" \
      -prepared "$prepared_path" \
      -observation "$observation_path" \
      -confirm "$EXECUTE_CONFIRM" || execute_status=$?
  fi
  if [[ -e "$custody_path" || -L "$custody_path" ]]; then
    # Persistence and custody destruction are separate boundaries. Even when
    # a complete observation exists, any surviving custody means Execute did
    # not authorize the wrapper's destructive cleanup fallback.
    EXIT_UNPROVEN_REASON="execution child status ${execute_status} with custody"
    die "execution stopped (status ${execute_status}) with private custody RETAINED at ${custody_path} for the separately reviewed purge — do not re-execute against it"
  fi
  if is_v25_plan "$plan_path" && (( execute_status != 0 )) &&
    [[ ! -f "$observation_path" || -L "$observation_path" ]]; then
    EXIT_UNPROVEN_REASON="execution child status ${execute_status} without final observation"
    die "execution stopped without final source-free authority; operation state retained"
  fi
  if is_v25_plan "$plan_path"; then
    if ! prepare_receipt_for_seal \
      "$plan_path" "$actual_digest" "$observation_path" "$results_path"; then
      if [[ -e "$supervision_path" || -L "$supervision_path" ]]; then
        EXIT_UNPROVEN_REASON="execution child status ${execute_status} with durable supervision"
      fi
      die "execution result could not reach exact terminal supervision and evidence state"
    fi
  fi
  if ! cleanup_prepared "$plan_path" "$prepared_path"; then
    die "exact private prepared manifest cleanup failed"
  fi
  for path in "$custody_path" "$supervision_path" "$prepared_path" "${prepared_path}.tmp" "${prepared_path}.preparing"; do
    [[ ! -e "$path" && ! -L "$path" ]] || die "private custody survived execution cleanup: $path"
  done
  refuse_supervision_residue "$custody_path"
  require_clean_checkout
  if ! is_v25_plan "$plan_path"; then
    prepare_receipt_for_seal \
      "$plan_path" "$actual_digest" "$observation_path" "$results_path"
  fi
  [[ -f "$observation_path" && ! -L "$observation_path" ]] ||
    die "execution produced no source-free observation after resume; no evidence was sealed"
  refuse_supervision_residue "$custody_path"
  seal_evidence "$ceremony_id"
  if (( execute_status == 0 )); then
    note "ceremony completed; the sealed receipt still requires independent review"
  else
    note "execution command returned status $execute_status; its resumed source-free outcome receipt was sealed for review"
  fi
}

verify_run() {
  local ceremony_id="$1" run_root
  initialize_ceremony_root
  run_root="$(run_root_for "$ceremony_id")"
  [[ -d "$run_root" && ! -L "$run_root" ]] || die "ceremony run directory is invalid"
  [[ -f "${run_root}/evidence/plan.json" && ! -L "${run_root}/evidence/plan.json" ]] ||
    die "frozen plan is missing or symlinked"
  verification_preflight_for_plan "${run_root}/evidence/plan.json"
  verify_evidence_directory "${run_root}/evidence"
}

verify_bundle() {
  local package="$1" authentication="$2" reviewed_identity="$3"
  local evidence_root trusted_allowed_signers
  local -a bundle_arguments
  [[ "$package" == /* && -f "$package" && ! -L "$package" ]] || die "bundle must be an absolute regular-file path"
  case "$authentication" in
    --reviewed-signer-fingerprint)
      [[ "$reviewed_identity" =~ ^SHA256:[A-Za-z0-9+/]{43}$ ]] ||
        die "reviewed signer fingerprint is invalid"
      ;;
    --reviewed-package-digest)
      [[ "$reviewed_identity" =~ ^sha256:[0-9a-f]{64}$ ]] ||
        die "reviewed package digest is invalid"
      ;;
    *) die "returned bundle requires one explicit out-of-band authentication mode" ;;
  esac
  verification_preflight
  require_v25_custody_command "$V25_BUNDLE_COMMAND" ||
    die "bounded returned-bundle command is unavailable"
  RETURNED_BUNDLE_TEMPORARY_ROOT="$(mktemp -d "$CLOSED_TMP/phebs-t4013-bundle.XXXXXX")"
  bundle_arguments=(-package "$package" -output "$RETURNED_BUNDLE_TEMPORARY_ROOT")
  if [[ "$authentication" == --reviewed-signer-fingerprint ]]; then
    bundle_arguments+=(-signer-fingerprint "$reviewed_identity")
  else
    bundle_arguments+=(-package-digest "$reviewed_identity")
  fi
  run_v25_custody_command_in_repo_active "$V25_BUNDLE_COMMAND" "${bundle_arguments[@]}" ||
    die "returned bundle failed bounded archive inspection"
  evidence_root="${RETURNED_BUNDLE_TEMPORARY_ROOT}/evidence"
  trusted_allowed_signers="${RETURNED_BUNDLE_TEMPORARY_ROOT}/reviewed_allowed_signers"
  verify_evidence_directory "$evidence_root" "$trusted_allowed_signers"
  rm -rf -- "$RETURNED_BUNDLE_TEMPORARY_ROOT"
  RETURNED_BUNDLE_TEMPORARY_ROOT=""
  note "returned bundle verification: PASS"
}

main() {
  local command_name="${1:-}"
  trap cleanup_on_exit EXIT
  trap 'retain_on_signal INT 130' INT
  trap 'retain_on_signal TERM 143' TERM
  trap 'retain_on_signal HUP 129' HUP
  if [[ "$command_name" == preflight || "$command_name" == freeze ||
    "$command_name" == execute || "$command_name" == seal ||
    "$command_name" == verify || "$command_name" == verify-bundle ]]; then
    require_host_stability_attestation
  fi
  enter_v25_run_lock "$@"
  case "$command_name" in
    preflight)
      [[ $# -eq 1 ]] || { usage; exit 2; }
      preflight
      ;;
    freeze)
      [[ $# -eq 2 ]] || { usage; exit 2; }
      freeze "$2"
      ;;
    execute)
      [[ $# -eq 4 ]] || { usage; exit 2; }
      execute_ceremony "$2" "$3" "$4"
      ;;
    seal)
      [[ $# -eq 2 ]] || { usage; exit 2; }
      seal_run "$2"
      ;;
    verify)
      [[ $# -eq 2 ]] || { usage; exit 2; }
      verify_run "$2"
      ;;
    verify-bundle)
      [[ $# -eq 4 ]] || { usage; exit 2; }
      verify_bundle "$2" "$3" "$4"
      ;;
    -h|--help|help|"")
      usage
      ;;
    *)
      usage
      exit 2
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
