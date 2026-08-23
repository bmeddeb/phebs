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
readonly PREPARE_CONFIRM="prepare-neutral-t4013-custody"
readonly EXECUTE_CONFIRM="execute-neutral-t4013-and-destroy-custody"
readonly CLEANUP_CONFIRM="cleanup-neutral-t4013-custody"
readonly SIGNATURE_NAMESPACE="phebs-t4013"
readonly FREEZE_SIGNATURE_NAMESPACE="phebs-t4013-freeze"
readonly LAST_REVIEW_STOPPED_CEREMONY_NUMBER=34
readonly RETIRED_SIGNER_FINGERPRINT="SHA256:BqFeTpCclBV0Z6Dz/Lc0dmpb75q7lZSAgH5rc6AK2nw"
readonly SIGNER_IDENTITY="phebs-ceremony"
readonly MINIMUM_MEMORY_BYTES=$((24 * 1024 * 1024 * 1024))
readonly MINIMUM_DISK_BYTES=$((120 * 1024 * 1024 * 1024))
readonly MAXIMUM_TRANSFER_PACKAGE_BYTES=$((4 * 1024 * 1024))

REPO_ROOT="${PHEBS_REPO_ROOT:-$SCRIPT_REPO_ROOT}"
CEREMONY_ROOT="${PHEBS_CEREMONY_ROOT:-${HOME:-}/phebs-t4013-ceremony}"
BASE_PORT="${PHEBS_T4013_BASE_PORT:-$DEFAULT_BASE_PORT}"
SIGNING_KEY=""
SIGNING_ROOT=""
REPO_REAL=""
CEREMONY_REAL=""
CLOSED_GO_CACHE="${CLOSED_GO_CACHE:-}"
V25_CLEANUP_COMMAND="${V25_CLEANUP_COMMAND:-}"
V25_EXECUTE_COMMAND="${V25_EXECUTE_COMMAND:-}"
V25_LOCK_COMMAND="${V25_LOCK_COMMAND:-}"
V25_PREPARE_COMMAND="${V25_PREPARE_COMMAND:-}"
V25_RECEIPT_COMMAND="${V25_RECEIPT_COMMAND:-}"
EXIT_PREPARED_PLAN=""
EXIT_PREPARED_MANIFEST=""
EXIT_PREPARED_WORKSPACE=""
RUN_LOCK_DIRECTORY=""
RUN_LOCK_TOKEN=""
RUN_LOCK_INHERITED=0
EXIT_UNPROVEN_REASON=""
ACTIVE_CHILD_PID=""

die() {
  printf '%s: %s\n' "$SCRIPT_NAME" "$*" >&2
  exit 1
}

note() {
  printf '%s\n' "$*"
}

closed_go() {
  [[ -n "$CLOSED_GO_CACHE" ]] || die "closed Go cache is not initialized"
  env -i \
    HOME="$HOME" \
    PATH="$PATH" \
    TMPDIR="${TMPDIR:-/tmp}" \
    LC_ALL=C \
    CGO_ENABLED=0 \
    GOENV=off \
    GOCACHE="$CLOSED_GO_CACHE" \
    GOEXPERIMENT= \
    GOFLAGS= \
    GOTOOLCHAIN=local \
    GOWORK=off \
    T4013_RUN_LOCK_FD="${T4013_RUN_LOCK_FD:-}" \
    "$@"
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
  [[ -n "$CLOSED_GO_CACHE" ]] || die "closed Go cache is not initialized"
  run_active_child env -i \
    HOME="$HOME" \
    PATH="$PATH" \
    TMPDIR="${TMPDIR:-/tmp}" \
    LC_ALL=C \
    CGO_ENABLED=0 \
    GOENV=off \
    GOCACHE="$CLOSED_GO_CACHE" \
    GOEXPERIMENT= \
    GOFLAGS= \
    GOTOOLCHAIN=local \
    GOWORK=off \
    T4013_RUN_LOCK_FD="${T4013_RUN_LOCK_FD:-}" \
    "$@"
}

initialize_closed_go_cache() {
  [[ -z "$CLOSED_GO_CACHE" ]] || return 0
  CLOSED_GO_CACHE="$(mktemp -d "${TMPDIR:-/tmp}/phebs-t4013-go-cache.XXXXXX")"
  chmod 700 "$CLOSED_GO_CACHE"
}

initialize_v25_custody_commands() {
  local command_root command_path
  [[ -n "$REPO_REAL" ]] || die "repository must be initialized before building custody commands"
  initialize_closed_go_cache
  if [[ -n "$V25_CLEANUP_COMMAND" && -n "$V25_EXECUTE_COMMAND" &&
    -n "$V25_LOCK_COMMAND" &&
    -n "$V25_PREPARE_COMMAND" && -n "$V25_RECEIPT_COMMAND" ]]; then
    for command_path in "$V25_CLEANUP_COMMAND" "$V25_EXECUTE_COMMAND" "$V25_LOCK_COMMAND" \
      "$V25_PREPARE_COMMAND" "$V25_RECEIPT_COMMAND"; do
      require_v25_custody_command "$command_path" ||
        die "prebuilt V25 custody command became invalid: $command_path"
    done
    return 0
  fi
  [[ -z "$V25_CLEANUP_COMMAND" && -z "$V25_EXECUTE_COMMAND" && -z "$V25_LOCK_COMMAND" &&
    -z "$V25_PREPARE_COMMAND" && -z "$V25_RECEIPT_COMMAND" ]] ||
    die "prebuilt V25 custody-command state is incomplete"
  command_root="${CLOSED_GO_CACHE}/t4013-custody-commands"
  mkdir -m 700 "$command_root"
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
    go build -o "${command_root}/" \
    ./spike/t4013/cmd/t4013-cleanup \
    ./spike/t4013/cmd/t4013-execute \
    ./spike/t4013/cmd/t4013-lock \
    ./spike/t4013/cmd/t4013-prepare \
    ./spike/t4013/cmd/t4013-receipt)
  V25_CLEANUP_COMMAND="${command_root}/t4013-cleanup"
  V25_EXECUTE_COMMAND="${command_root}/t4013-execute"
  V25_LOCK_COMMAND="${command_root}/t4013-lock"
  V25_PREPARE_COMMAND="${command_root}/t4013-prepare"
  V25_RECEIPT_COMMAND="${command_root}/t4013-receipt"
  for command_path in "$V25_CLEANUP_COMMAND" "$V25_EXECUTE_COMMAND" "$V25_LOCK_COMMAND" \
    "$V25_PREPARE_COMMAND" "$V25_RECEIPT_COMMAND"; do
    [[ -f "$command_path" && ! -L "$command_path" && -x "$command_path" ]] ||
      die "prebuilt V25 custody command is invalid: $command_path"
  done
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
  local command_path="$1"
  [[ "$command_path" == "${CLOSED_GO_CACHE}/t4013-custody-commands/"* &&
    -f "$command_path" && ! -L "$command_path" && -x "$command_path" ]] ||
    {
      printf '%s: V25 custody command was not prebuilt before operation admission\n' "$SCRIPT_NAME" >&2
      return 1
    }
}

cleanup_on_exit() {
  local status=$?
  trap - EXIT
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
  if [[ -n "$CLOSED_GO_CACHE" ]]; then
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
  if ! unexpected="$(find "$RUN_LOCK_DIRECTORY" -mindepth 1 -maxdepth 1 ! -name owner -print -quit)" ||
    [[ -n "$unexpected" ]]; then
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
  is_v25_plan "$plan_path" || return 0
  initialize_v25_custody_commands
  require_v25_custody_command "$V25_LOCK_COMMAND" ||
    die "V25 run-root lock command was not prebuilt before operation admission"
  export CLOSED_GO_CACHE V25_CLEANUP_COMMAND V25_EXECUTE_COMMAND V25_LOCK_COMMAND
  export V25_PREPARE_COMMAND V25_RECEIPT_COMMAND
  exec "$V25_LOCK_COMMAND" -run-root "$run_root" -- "$SCRIPT_PATH" "$@"
}

closed_git() {
  local git_driver exec_path
  git_driver="$(command -v git)"
  exec_path="$(env -i \
    HOME="$HOME" PATH="$PATH" TMPDIR="${TMPDIR:-/tmp}" LC_ALL=C \
    GIT_ATTR_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
    GIT_NO_LAZY_FETCH=1 GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 \
    "$git_driver" --exec-path)"
  [[ "$exec_path" == /* && -d "$exec_path" && ! -L "$exec_path" && \
    -f "${exec_path}/git" && -x "${exec_path}/git" ]] ||
    die "closed Git core executable is invalid"
  env -i \
    HOME="$HOME" \
    PATH="$PATH" \
    TMPDIR="${TMPDIR:-/tmp}" \
    LC_ALL=C \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_OPTIONAL_LOCKS=0 \
    GIT_TERMINAL_PROMPT=0 \
    "${exec_path}/git" "$@"
}

plan_go() {
  local plan_path="$1"
  shift
  if is_v25_plan "$plan_path"; then
    closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify || return 1
    closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off "$@" || return 1
  else
    env GOPROXY=off "$@"
  fi
}

plan_go_active() {
  local plan_path="$1"
  shift
  if is_v25_plan "$plan_path"; then
    closed_go_active GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify || return 1
    closed_go_active GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off "$@" || return 1
  else
    env GOPROXY=off "$@"
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
  grep -Eq '"schema"[[:space:]]*:[[:space:]]*"t4013-neutral-convergence-plan-v25"' "$1"
}

usage() {
  cat <<EOF
Usage:
  $SCRIPT_NAME preflight
  $SCRIPT_NAME freeze <ceremony-id>
  $SCRIPT_NAME execute <ceremony-id> <approved-plan-digest> $EXECUTE_APPROVAL
  $SCRIPT_NAME seal <ceremony-id>
  $SCRIPT_NAME verify <ceremony-id>
  $SCRIPT_NAME verify-bundle </absolute/path/to/source-free.tgz>

Defaults:
  phebs checkout:  ~/phebs
  ceremony root:   ~/phebs-t4013-ceremony
  loopback ports:  41731 and 41732

The freeze and execute commands are deliberately separate. Review and record
the printed plan digest before invoking execute. No command accepts a private
monorepo path; this is the neutral two-million-owner ceremony only.
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command is unavailable: $1"
}

durable_promote() {
  local temporary="$1" final="$2" filesystem_root="$3"
  [[ "$temporary" == /* && "$final" == /* && "$filesystem_root" == /* &&
    "${temporary%/*}" == "${final%/*}" &&
    -f "$temporary" && ! -L "$temporary" &&
    ! -e "$final" && ! -L "$final" &&
    -d "$filesystem_root" && ! -L "$filesystem_root" ]] ||
    die "durable evidence promotion is invalid"
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
    go run ./spike/t4013/cmd/t4013-promote \
    -temporary "$temporary" -output "$final" -root "$CEREMONY_REAL") ||
    die "durable evidence promotion failed"
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
  note "SurrealDB toolchain: $(surreal version)"
}

preflight() {
  local command_name commit host_plan_root
  for command_name in awk cmp cp date df du env find git go grep lsof mkdir mktemp pgrep ps rm sed shasum sort ssh-keygen surreal sysctl tar uname uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  initialize_ceremony_root
  initialize_closed_go_cache
  require_clean_checkout
  host_preflight
  (cd "$REPO_REAL" && closed_go go mod download all)
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify)
  commit="$(closed_git -C "$REPO_REAL" rev-parse HEAD)"
  host_plan_root="$(mktemp -d "${TMPDIR:-/tmp}/phebs-t4013-host-plan.XXXXXX")"
  if ! (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
    go run ./spike/t4013/cmd/t4013-freeze \
    -root "$REPO_REAL" \
    -source-commit "$commit" \
    -data-parent "$CEREMONY_REAL" \
    -bind-host-toolchain \
    -output "${host_plan_root}/plan.json") >/dev/null; then
    rm -rf -- "$host_plan_root"
    die "exact V25 prospective host preflight failed"
  fi
  rm -rf -- "$host_plan_root"
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify)
  initialize_v25_custody_commands
  require_clean_checkout
  note "T40.13 host, module, and prebuilt custody-command checks: PASS"
  note "process-launching regression and readiness suites are branch gates and are not re-run outside durable custody"
}

verification_preflight() {
  local command_name
  for command_name in awk cmp du env find git go grep mktemp pgrep ps rm sed shasum sort ssh-keygen tar uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  initialize_closed_go_cache
  require_clean_checkout
  (cd "$REPO_REAL" && closed_go go mod download all)
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify)
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

ensure_signing_key() {
  local fingerprint
  if [[ -e "$SIGNING_KEY" || -L "$SIGNING_KEY" || -e "${SIGNING_KEY}.pub" || -L "${SIGNING_KEY}.pub" ]]; then
    [[ -f "$SIGNING_KEY" && ! -L "$SIGNING_KEY" && -f "${SIGNING_KEY}.pub" && ! -L "${SIGNING_KEY}.pub" ]] ||
      die "ceremony signing keypair is partial, invalid, or symlinked"
  else
    ssh-keygen -q -t ed25519 -N "" -C "phebs-t4013-ceremony" -f "$SIGNING_KEY"
    chmod 600 "$SIGNING_KEY"
    chmod 644 "${SIGNING_KEY}.pub"
    note "created ceremony signing key: $SIGNING_KEY"
    note "back up this key separately before relying on its identity"
  fi
  fingerprint="$(ssh-keygen -lf "${SIGNING_KEY}.pub" -E sha256 | awk '{ print $2 }')"
  [[ "$fingerprint" == SHA256:* ]] || die "ceremony signer fingerprint is invalid"
  [[ "$fingerprint" != "$RETIRED_SIGNER_FINGERPRINT" ]] ||
    die "the selected ceremony signer is retired and may not sign new evidence"
}

run_root_for() {
  local ceremony_id="$1"
  validate_id "$ceremony_id"
  printf '%s/%s\n' "$CEREMONY_REAL" "$ceremony_id"
}

plan_digest_for() {
  local plan_path="$1"
  printf 'sha256:%s\n' "$(shasum -a 256 "$plan_path" | awk '{print $1}')"
}

freeze() {
  local ceremony_id="$1" run_root evidence_root private_root commit digest frozen_at public_key fingerprint
  local signer_tmp allowed_tmp freeze_tmp freeze_signature_tmp
  reject_review_stopped_id "$ceremony_id"
  preflight
  select_signing_key "$ceremony_id"
  ensure_signing_key
  run_root="$(run_root_for "$ceremony_id")"
  [[ ! -e "$run_root" && ! -L "$run_root" ]] || die "ceremony id already exists and may not be overwritten: $ceremony_id"
  evidence_root="${run_root}/evidence"
  private_root="${run_root}/private"
  mkdir -m 700 "$run_root" "$evidence_root" "$private_root"
  commit="$(closed_git -C "$REPO_REAL" rev-parse HEAD)"
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off go mod verify)
  (cd "$REPO_REAL" && closed_go GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off \
    go run ./spike/t4013/cmd/t4013-freeze \
    -root "$REPO_REAL" \
    -source-commit "$commit" \
    -data-parent "$CEREMONY_REAL" \
    -bind-host-toolchain \
    -output "${evidence_root}/plan.json") >/dev/null
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
  durable_promote "$signer_tmp" "${evidence_root}/signer.pub" "$evidence_root"
  printf '%s %s\n' "$SIGNER_IDENTITY" "$public_key" > "$allowed_tmp"
  durable_promote "$allowed_tmp" "${evidence_root}/allowed_signers" "$evidence_root"
  printf '{\n  "schema": "t4013-freeze-envelope-v1",\n  "ceremony_id": "%s",\n  "source_commit": "%s",\n  "plan_digest": "%s",\n  "signer_fingerprint": "%s",\n  "frozen_at": "%s"\n}\n' \
    "$ceremony_id" "$commit" "$digest" "$fingerprint" "$frozen_at" > "$freeze_tmp"
  ssh-keygen -Y sign -f "$SIGNING_KEY" -n "$FREEZE_SIGNATURE_NAMESPACE" \
    "$freeze_tmp" >/dev/null
  durable_promote "$freeze_tmp" "${evidence_root}/freeze.json" "$evidence_root"
  durable_promote "$freeze_signature_tmp" "${evidence_root}/freeze.json.sig" "$evidence_root"
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
  local supervision_path="${1}.t4013-supervision" path
  for path in "$supervision_path" "${supervision_path}.retiring" "${supervision_path}.retired"; do
    [[ ! -e "$path" && ! -L "$path" ]] ||
      die "durable custody supervision remains; receipt and seal are refused: $path"
  done
  if compgen -G "${supervision_path}.creating.*" >/dev/null; then
    die "durable custody supervision creation remains; receipt and seal are refused: $supervision_path"
  fi
}

manifest_value() {
  local manifest="$1" key="$2"
  awk -F '"' -v wanted="$key" '$2 == wanted { print $4; exit }' "$manifest"
}

require_exact_inventory() {
  local directory="$1" expected actual
  shift
  expected="$(printf '%s\n' "$@" | LC_ALL=C sort)"
  actual="$(cd "$directory" && find . -mindepth 1 -maxdepth 1 -print | sed 's#^\./##' | LC_ALL=C sort)"
  [[ "$actual" == "$expected" ]] || die "evidence directory contains an unexpected or missing path"
}

verify_frozen_identity() {
  local evidence_root="$1" source_commit plan_digest
  source_commit="$(manifest_value "${evidence_root}/freeze.json" source_commit)"
  plan_digest="$(manifest_value "${evidence_root}/freeze.json" plan_digest)"
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_commit" == "$(closed_git -C "$REPO_REAL" rev-parse HEAD)" ]] ||
    die "verification checkout differs from the frozen execution commit"
  [[ "$plan_digest" == "$(plan_digest_for "${evidence_root}/plan.json")" ]] || die "frozen plan digest differs"
  ssh-keygen -Y verify \
    -f "${evidence_root}/allowed_signers" \
    -I "$SIGNER_IDENTITY" \
    -n "$FREEZE_SIGNATURE_NAMESPACE" \
    -s "${evidence_root}/freeze.json.sig" < "${evidence_root}/freeze.json"
}

verify_evidence_directory() {
  local evidence_root="$1" manifest source_commit plan_digest temporary_root rebuilt
  local required
  require_exact_inventory "$evidence_root" \
    allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json \
    SHA256SUMS SHA256SUMS.sig signer.pub
  for required in allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json SHA256SUMS SHA256SUMS.sig signer.pub; do
    [[ -f "${evidence_root}/${required}" && ! -L "${evidence_root}/${required}" ]] ||
      die "source-free evidence is missing or symlinked: $required"
  done
  manifest="${evidence_root}/manifest.json"
  verify_frozen_identity "$evidence_root"
  source_commit="$(manifest_value "$manifest" source_commit)"
  plan_digest="$(manifest_value "$manifest" plan_digest)"
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_commit" == "$(closed_git -C "$REPO_REAL" rev-parse HEAD)" ]] ||
    die "verification checkout differs from the sealed execution commit"
  [[ "$plan_digest" == "$(plan_digest_for "${evidence_root}/plan.json")" ]] || die "sealed plan digest differs"
  (cd "$evidence_root" && shasum -a 256 -c SHA256SUMS)
  ssh-keygen -Y verify \
    -f "${evidence_root}/allowed_signers" \
    -I "$SIGNER_IDENTITY" \
    -n "$SIGNATURE_NAMESPACE" \
    -s "${evidence_root}/SHA256SUMS.sig" < "${evidence_root}/SHA256SUMS"
  temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/phebs-t4013-verify.XXXXXX")"
  rebuilt="${temporary_root}/results.json"
  if ! (cd "$REPO_REAL" && plan_go "${evidence_root}/plan.json" \
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

seal_evidence() {
  local ceremony_id="$1" run_root evidence_root source_commit plan_digest generated_at
  local package package_tmp package_digest package_sidecar package_sidecar_tmp package_bytes
  local manifest_tmp checksums_tmp signature_tmp
  local seal_count=0 seal_name
  local -a expected
  run_root="$(run_root_for "$ceremony_id")"
  evidence_root="${run_root}/evidence"
  source_commit="$(closed_git -C "$REPO_REAL" rev-parse HEAD)"
  plan_digest="$(plan_digest_for "${evidence_root}/plan.json")"
  generated_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  manifest_tmp="${evidence_root}/manifest.json.tmp"
  checksums_tmp="${evidence_root}/SHA256SUMS.tmp"
  signature_tmp="${checksums_tmp}.sig"
  cmp -s "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub" || die "ceremony signing key changed after freeze"
  verify_frozen_identity "$evidence_root"
  for seal_name in "$manifest_tmp" "$checksums_tmp" "$signature_tmp"; do
    if [[ -e "$seal_name" || -L "$seal_name" ]]; then
      [[ -f "$seal_name" && ! -L "$seal_name" ]] || die "partial source-free seal temporary file is invalid"
      rm -- "$seal_name"
    fi
  done
  for seal_name in manifest.json SHA256SUMS SHA256SUMS.sig; do
    if [[ -e "${evidence_root}/${seal_name}" || -L "${evidence_root}/${seal_name}" ]]; then
      [[ -f "${evidence_root}/${seal_name}" && ! -L "${evidence_root}/${seal_name}" ]] ||
        die "partial source-free seal is invalid: $seal_name"
      seal_count=$((seal_count + 1))
    fi
  done
  if (( seal_count == 3 )); then
    verify_evidence_directory "$evidence_root"
  else
    expected=(allowed_signers freeze.json freeze.json.sig observation.json plan.json results.json signer.pub)
    for seal_name in manifest.json SHA256SUMS SHA256SUMS.sig; do
      [[ -e "${evidence_root}/${seal_name}" ]] && expected+=("$seal_name")
    done
    require_exact_inventory "$evidence_root" "${expected[@]}"
    if (( seal_count > 0 )); then
      die "partial source-free seal is retained for review"
    fi
    printf '{\n  "schema": "t4013-source-free-transfer-v1",\n  "ceremony_id": "%s",\n  "source_commit": "%s",\n  "plan_digest": "%s",\n  "sealed_at": "%s"\n}\n' \
      "$ceremony_id" "$source_commit" "$plan_digest" "$generated_at" > "$manifest_tmp"
    durable_promote "$manifest_tmp" "${evidence_root}/manifest.json" "$evidence_root"
    (cd "$evidence_root" && shasum -a 256 \
      allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json signer.pub > "$checksums_tmp")
    ssh-keygen -Y sign -f "$SIGNING_KEY" -n "$SIGNATURE_NAMESPACE" "$checksums_tmp" >/dev/null
    durable_promote "$checksums_tmp" "${evidence_root}/SHA256SUMS" "$evidence_root"
    durable_promote "$signature_tmp" "${evidence_root}/SHA256SUMS.sig" "$evidence_root"
    verify_evidence_directory "$evidence_root"
  fi
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
    verify_bundle "$package"
    package_digest="$(shasum -a 256 "$package" | awk '{print $1}')"
    if [[ -e "$package_sidecar" || -L "$package_sidecar" ]]; then
      [[ -f "$package_sidecar" && ! -L "$package_sidecar" ]] || die "source-free package sidecar is invalid"
      [[ "$(awk 'NR == 1 { print $1, $2 }' "$package_sidecar")" == "$package_digest $(basename "$package")" ]] ||
        die "source-free package sidecar differs"
    else
      printf '%s  %s\n' "$package_digest" "$(basename "$package")" > "$package_sidecar_tmp"
      durable_promote "$package_sidecar_tmp" "$package_sidecar" "$run_root"
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
  durable_promote "$package_tmp" "$package" "$run_root"
  package_digest="$(shasum -a 256 "$package" | awk '{print $1}')"
  printf '%s  %s\n' "$package_digest" "$(basename "$package")" > "$package_sidecar_tmp"
  durable_promote "$package_sidecar_tmp" "$package_sidecar" "$run_root"
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
  acquire_run_lock "$run_root"
  verification_preflight_for_plan "$plan_path"
  if is_v25_plan "$plan_path"; then
    initialize_v25_custody_commands
  fi
  select_signing_key "$ceremony_id"
  ensure_signing_key
  [[ -d "$evidence_root" && ! -L "$evidence_root" && -d "$private_root" && ! -L "$private_root" ]] ||
    die "ceremony evidence or private directory is invalid"
  plan_digest="$(plan_digest_for "$plan_path")"
  if [[ -e "$custody_path" || -L "$custody_path" ]]; then
    [[ -d "$custody_path" && ! -L "$custody_path" ]] || die "private custody is invalid"
    if [[ -e "${custody_path}/.t4013-executed" || -L "${custody_path}/.t4013-executed" ]]; then
      die "marker-bearing executed custody remains for separately reviewed purge"
    fi
  fi
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
    [[ -z "$(find "$private_root" -mindepth 1 -maxdepth 1 \
      ! -name prepared.json ! -name prepared.json.tmp ! -name prepared.json.preparing -print -quit)" ]] ||
      die "unexpected private ceremony state remains"
    if ! cleanup_prepared "${evidence_root}/plan.json" "$prepared_path"; then
      EXIT_UNPROVEN_REASON="resumable prepared cleanup refused"
      die "resumable private prepared manifest cleanup failed"
    fi
  fi
  [[ ! -e "$custody_path" && ! -L "$custody_path" ]] || die "private custody remains"
  [[ -z "$(find "$private_root" -mindepth 1 -maxdepth 1 -print -quit)" ]] || die "private ceremony state remains"
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
  initialize_closed_go_cache
  select_signing_key "$ceremony_id"
  ensure_signing_key
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
  verification_preflight
  initialize_ceremony_root
  run_root="$(run_root_for "$ceremony_id")"
  verify_evidence_directory "${run_root}/evidence"
}

verify_bundle() {
  local package="$1" temporary_root listing entry evidence_root package_bytes
  [[ "$package" == /* && -f "$package" && ! -L "$package" ]] || die "bundle must be an absolute regular-file path"
  package_bytes="$(wc -c < "$package" | awk '{ print $1 }')"
  [[ "$package_bytes" =~ ^[0-9]+$ ]] || die "bundle size is invalid"
  (( package_bytes > 0 && package_bytes <= MAXIMUM_TRANSFER_PACKAGE_BYTES )) ||
    die "bundle exceeds the fixed 4-MiB transfer bound"
  verification_preflight
  temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/phebs-t4013-bundle.XXXXXX")"
  listing="${temporary_root}/listing"
  COPYFILE_DISABLE=1 tar -tzf "$package" > "$listing"
  while IFS= read -r entry; do
    case "$entry" in
      evidence/|evidence/allowed_signers|evidence/freeze.json|evidence/freeze.json.sig|evidence/manifest.json|evidence/observation.json|evidence/plan.json|evidence/results.json|evidence/SHA256SUMS|evidence/SHA256SUMS.sig|evidence/signer.pub) ;;
      *) rm -rf -- "$temporary_root"; die "bundle contains an unexpected path: $entry" ;;
    esac
  done < "$listing"
  [[ -z "$(LC_ALL=C sort "$listing" | uniq -d)" ]] || {
    rm -rf -- "$temporary_root"
    die "bundle contains duplicate paths"
  }
  COPYFILE_DISABLE=1 tar -xzf "$package" -C "$temporary_root"
  evidence_root="${temporary_root}/evidence"
  verify_evidence_directory "$evidence_root"
  rm -rf -- "$temporary_root"
  note "returned bundle verification: PASS"
}

main() {
  local command_name="${1:-}"
  trap cleanup_on_exit EXIT
  trap 'retain_on_signal INT 130' INT
  trap 'retain_on_signal TERM 143' TERM
  trap 'retain_on_signal HUP 129' HUP
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
      [[ $# -eq 2 ]] || { usage; exit 2; }
      verify_bundle "$2"
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
