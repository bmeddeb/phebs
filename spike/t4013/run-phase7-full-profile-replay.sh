#!/usr/bin/env bash
set -euo pipefail

umask 077

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
module_root="$(cd -- "$script_dir/../.." && pwd -P)"
attestation=dedicated-single-operator-host-with-tool-mutation-disabled
lock=/private/tmp/phebs-t4013-phase7-full.lock
lock_acquired=0
run_root=
control_root=
active_child_pid=
exit_unproven_reason=

cleanup() {
  local status=$?
  trap - EXIT
  if [[ "$lock_acquired" -eq 1 && "$status" -ne 0 ]]; then
    echo "Phase 7 full-profile exclusive lock retained at $lock" >&2
  fi
  if [[ "$status" -ne 0 && -n "$run_root" ]]; then
    echo "Phase 7 full-profile replay state retained at $run_root" >&2
    if [[ -n "$control_root" ]]; then
      echo "Phase 7 full-profile build controls retained at $control_root" >&2
    fi
    if [[ -n "$exit_unproven_reason" ]]; then
      echo "Phase 7 full-profile child exit is unproven: $exit_unproven_reason" >&2
    fi
    echo "Do not rerun, share, or purge it before process-absence review." >&2
  fi
  exit "$status"
}

retain_on_signal() {
  local signal_name=$1 status=$2
  exit_unproven_reason="signal $signal_name"
  trap - INT TERM HUP
  if [[ -n "$active_child_pid" ]]; then
    if ! kill -s "$signal_name" -- "-$active_child_pid" 2>/dev/null; then
      exit_unproven_reason="$exit_unproven_reason; child signal forwarding failed"
    fi
    wait "$active_child_pid" 2>/dev/null || :
    if kill -0 -- "-$active_child_pid" 2>/dev/null; then
      exit_unproven_reason="$exit_unproven_reason; child process group survived"
    fi
    active_child_pid=
  fi
  exit "$status"
}

run_active_child() {
  local status=0 monitor_was_enabled=0 child_pid
  [[ -z "$active_child_pid" ]] || return 1
  [[ $- == *m* ]] && monitor_was_enabled=1
  set -m
  "$@" &
  active_child_pid=$!
  [[ "$monitor_was_enabled" -eq 1 ]] || set +m
  child_pid=$active_child_pid
  wait "$child_pid" || status=$?
  active_child_pid=
  if kill -0 -- "-$child_pid" 2>/dev/null; then
    exit_unproven_reason="child process group survived status $status"
    return 1
  fi
  return "$status"
}

canonical_executable_path() {
  local name=$1 path target directory links=0
  path=$(command -v "$name") || return 1
  while [[ -L "$path" ]]; do
    links=$((links + 1))
    [[ "$links" -le 40 ]] || return 1
    target=$(readlink "$path") || return 1
    if [[ "$target" == /* ]]; then
      path=$target
    else
      path="$(dirname "$path")/$target"
    fi
  done
  directory=$(cd -- "$(dirname "$path")" && pwd -P) || return 1
  path="$directory/$(basename "$path")"
  [[ -f "$path" && ! -L "$path" && -x "$path" ]] || return 1
  printf '%s\n' "$path"
}

file_sha256() {
  local value
  value=$(shasum -a 256 "$1" | awk 'NR == 1 { print $1 }') || return 1
  [[ "$value" =~ ^[0-9a-f]{64}$ ]] || return 1
  printf 'sha256:%s\n' "$value"
}

main() {
trap cleanup EXIT
trap 'retain_on_signal INT 130' INT
trap 'retain_on_signal TERM 143' TERM
trap 'retain_on_signal HUP 129' HUP

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Phase 7 full-profile replay requires macOS" >&2
  exit 1
fi
if [[ "$#" -ne 1 ]]; then
  echo "usage: $0 <reviewed-lowercase-40-hex-HEAD>" >&2
  exit 1
fi
if [[ ! "$1" =~ ^[0-9a-f]{40}$ ]]; then
  echo "usage: $0 <reviewed-lowercase-40-hex-HEAD>" >&2
  exit 1
fi
expected_commit=$1
if [[ "${PHEBS_T4013_HOST_STABILITY_ATTESTATION:-}" != "$attestation" ]]; then
  echo "Set PHEBS_T4013_HOST_STABILITY_ATTESTATION=$attestation" >&2
  exit 1
fi
if [[ -n "${T4013_RUN_LOCK_FD:-}" ]]; then
  echo "Phase 7 full-profile replay refuses an ambient run-root lock descriptor" >&2
  exit 1
fi

cd -- "$module_root"
if [[ "$(git rev-parse HEAD)" != "$expected_commit" ]]; then
  echo "Phase 7 full-profile replay HEAD differs from the reviewed commit" >&2
  exit 1
fi
if [[ -n "$(git status --porcelain=v1 --untracked-files=all)" ]]; then
  echo "Phase 7 full-profile replay requires an exact-clean checkout" >&2
  exit 1
fi

parent=${PHEBS_T4013_PHASE7_REPLAY_PARENT:-/private/tmp}
if [[ "$parent" != /* || ! -d "$parent" || -L "$parent" ]]; then
  echo "Phase 7 full-profile replay parent must be an absolute real directory" >&2
  exit 1
fi
parent="$(cd -- "$parent" && pwd -P)"
if ! mkdir -m 700 "$lock"; then
  echo "Phase 7 full-profile replay is already active; review $lock before removal" >&2
  exit 1
fi
lock_acquired=1
run_root="$(mktemp -d "$parent/phebs-t4013-phase7-full.XXXXXX")"
chmod 700 "$run_root"
echo "Phase 7 full-profile replay root: $run_root"

go_path=$(canonical_executable_path go) || {
  echo "Phase 7 full-profile replay cannot bind the Go driver" >&2
  exit 1
}
go_sha256=$(file_sha256 "$go_path") || {
  echo "Phase 7 full-profile replay Go driver digest is invalid" >&2
  exit 1
}
control_root="$(mktemp -d "$parent/phebs-t4013-phase7-driver.XXXXXX")"
mkdir -m 700 "$control_root/home" "$control_root/tmp" "$control_root/go-build" "$control_root/go-mod"
driver_path="$(dirname "$go_path"):$PATH"

child_status=0
run_active_child env -i \
  HOME="$control_root/home" \
  PATH="$driver_path" \
  TMPDIR="$control_root/tmp" TEMP="$control_root/tmp" TMP="$control_root/tmp" \
  XDG_CONFIG_HOME="$control_root/home" XDG_CACHE_HOME="$control_root/tmp" XDG_DATA_HOME="$control_root/home" \
  LC_ALL=C LANG=C TZ=UTC \
  CGO_ENABLED=0 \
  GOENV=off \
  GOCACHE="$control_root/go-build" \
  GOMODCACHE="$control_root/go-mod" \
  GOTMPDIR="$control_root/tmp" \
  GOEXPERIMENT= \
  GOFLAGS=-mod=readonly \
  GOTOOLCHAIN=local \
  GOTELEMETRY=off \
  GOWORK=off \
  PHEBS_T4013_FULL_PROFILE_PHASE7_REPLAY=1 \
  PHEBS_T4013_PHASE7_REPLAY_ROOT="$run_root" \
  PHEBS_T4013_PHASE7_REPLAY_COMMIT="$expected_commit" \
  PHEBS_T4013_PHASE7_REPLAY_GO_SHA256="$go_sha256" \
  PHEBS_T4013_HOST_STABILITY_ATTESTATION="$attestation" \
  "$go_path" test ./spike/t4013 \
    -run '^TestProductionFullProfilePhase7Replay$' \
    -count=1 -v -timeout=20h || child_status=$?
if [[ "$child_status" -ne 0 ]]; then
  exit "$child_status"
fi

if [[ "$(git rev-parse HEAD)" != "$expected_commit" || -n "$(git status --porcelain=v1 --untracked-files=all)" ]]; then
  echo "Phase 7 full-profile replay checkout changed during execution" >&2
  exit 1
fi
result="$run_root/phase7-replay.json"
if [[ ! -f "$result" || -L "$result" ]]; then
  echo "Phase 7 full-profile replay produced no regular source-free result" >&2
  exit 1
fi
observed_go_sha256=$(file_sha256 "$go_path") || {
  echo "Phase 7 full-profile replay cannot revalidate the Go driver" >&2
  exit 1
}
if [[ "$observed_go_sha256" != "$go_sha256" ]]; then
  echo "Phase 7 full-profile replay Go driver changed during execution" >&2
  exit 1
fi
result_sha256=$(file_sha256 "$result") || {
  echo "Phase 7 full-profile replay result digest is unavailable" >&2
  exit 1
}
chmod -R u+w "$control_root"
rm -rf -- "$control_root"
if [[ -e "$control_root" || -L "$control_root" ]]; then
  echo "Phase 7 full-profile replay build controls were not retired" >&2
  exit 1
fi
control_root=
completion_tmp="$lock/completion.tmp.$$"
completion_marker="$lock/completion"
printf '%s\n' \
  'schema=t4013-full-profile-phase7-replay-completion-v1' \
  "source_commit=$expected_commit" \
  "result=$result" \
  "result_sha256=$result_sha256" > "$completion_tmp"
chmod 600 "$completion_tmp"
mv "$completion_tmp" "$completion_marker"
echo "source-free result: $result"
echo "result sha256: ${result_sha256#sha256:}"
echo "Phase 7 full-profile replay: PASS"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
