#!/usr/bin/env bash
set -euo pipefail

umask 077

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
module_root=
script_path="$script_dir/run-phase7-full-profile-replay.sh"
attestation=dedicated-single-operator-host-with-tool-mutation-disabled
lock=/private/tmp/phebs-t4013-phase7-full.lock
lock_acquired=0
run_root=
control_root=
source_root=
active_child_pid=
active_child_root=
active_child_retiring=0
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

active_child_job_is_owned() {
  local job_pid
  [[ -n "$active_child_pid" && "$1" == "$active_child_pid" ]] || return 1
  job_pid=$(jobs -p %+) || return 1
  [[ "$job_pid" == "$1" ]]
}

active_child_group_is_drained() {
  local attempt listing pid pgid state extra valid sentinel_rows sentinel_state other_rows result=2
  active_child_job_is_owned "$1" || return 2
  kill -STOP %+ 2>/dev/null || return 2
  for attempt in {1..100}; do
    listing=$(/bin/ps -Ao pid=,pgid=,state=) || break
    valid=1
    sentinel_rows=0
    sentinel_state=
    other_rows=0
    while read -r pid pgid state extra; do
      [[ -n "$pid" ]] || continue
      if [[ ! "$pid" =~ ^[0-9]+$ || ! "$pgid" =~ ^[0-9]+$ || -z "$state" || -n "$extra" ]]; then
        valid=0
        break
      fi
      [[ "$pgid" == "$1" ]] || continue
      if [[ "$pid" == "$1" ]]; then
        sentinel_rows=$((sentinel_rows + 1))
        sentinel_state=$state
      else
        other_rows=$((other_rows + 1))
      fi
    done <<< "$listing"
    if [[ "$valid" -ne 1 || "$sentinel_rows" -ne 1 || "$sentinel_state" == Z* ]]; then
      return 2
    fi
    if [[ "$sentinel_state" == T* ]]; then
      if [[ "$other_rows" -eq 0 ]]; then
        result=0
      else
        result=1
      fi
      kill -CONT %+ 2>/dev/null || return 2
      return "$result"
    fi
    [[ "$attempt" -lt 100 ]] || break
    /bin/sleep 0.01
    kill -STOP %+ 2>/dev/null || return 2
  done
  return 2
}

read_active_child_notification() {
  local expected=$1 notification= status
  while :; do
    notification=
    status=0
    ready_read_interrupted=0
    IFS= read -r notification <&9 || status=$?
    if [[ "$status" -eq 0 ]]; then
      [[ "$notification" == "$expected" ]]
      return
    fi
    if [[ "$expected" == ready && "$ready_read_interrupted" -eq 1 ]]; then
      continue
    fi
    return 1
  done
}

wait_for_active_child_done() {
  if [[ -f "$active_child_root/done" && ! -L "$active_child_root/done" ]]; then
    return 0
  fi
  read_active_child_notification done &&
    [[ -f "$active_child_root/done" && ! -L "$active_child_root/done" ]]
}

retire_active_child_sentinel() {
  local child_pid=$active_child_pid child_root=$active_child_root status=0
  active_child_retiring=1
  printf 'release\n' >&7 || return 1
  active_child_pid=
  active_child_root=
  exec 9<&-
  exec 7>&-
  active_child_retiring=0
  wait "$child_pid" || status=$?
  if [[ "$status" -ne 0 || -e "$child_root/alive" || -L "$child_root/alive" ]]; then
    return 1
  fi
  rm -rf -- "$child_root"
}

retain_on_signal() {
  local signal_name=$1 status=$2 group_status
  exit_unproven_reason="signal $signal_name"
  trap - INT TERM HUP
  if [[ "$active_child_retiring" -eq 1 ]]; then
    exit_unproven_reason="$exit_unproven_reason; child sentinel retirement was interrupted"
    exit "$status"
  fi
  if [[ -n "$active_child_pid" ]]; then
    if ! active_child_job_is_owned "$active_child_pid"; then
      exit_unproven_reason="$exit_unproven_reason; child job is no longer owned; signal not forwarded"
    elif ! kill -s "$signal_name" %+ 2>/dev/null; then
      exit_unproven_reason="$exit_unproven_reason; child signal forwarding failed"
    elif ! wait_for_active_child_done; then
      exit_unproven_reason="$exit_unproven_reason; child sentinel completion is unproven"
    else
      group_status=0
      active_child_group_is_drained "$active_child_pid" || group_status=$?
      if [[ "$group_status" -eq 1 ]]; then
        exit_unproven_reason="$exit_unproven_reason; child process group survived"
      elif [[ "$group_status" -ne 0 ]]; then
        exit_unproven_reason="$exit_unproven_reason; child process group inspection failed"
      elif ! retire_active_child_sentinel; then
        exit_unproven_reason="$exit_unproven_reason; child sentinel retirement failed"
      fi
    fi
  fi
  exit "$status"
}

run_active_child() {
  local status=0 group_status=0 monitor_was_enabled=0 child_pid sentinel_root
  local pending_signal= pending_status=0 ready_read_interrupted=0
  [[ -z "$active_child_pid" && -z "$active_child_root" ]] || return 1
  [[ "$lock_acquired" -eq 1 && -d "$lock" && ! -L "$lock" ]] || return 1
  if jobs -p %+ >/dev/null 2>&1; then
    exit_unproven_reason="an ambient shell job prevents exact child ownership"
    return 1
  fi
  if : 2>/dev/null >&7; then
    exit_unproven_reason="release descriptor 7 is already open"
    return 1
  fi
  if : 2>/dev/null >&8; then
    exit_unproven_reason="notification bootstrap descriptor 8 is already open"
    return 1
  fi
  if : 2>/dev/null <&9; then
    exit_unproven_reason="notification read descriptor 9 is already open"
    return 1
  fi
  sentinel_root="$(mktemp -d "$lock/.phase7-child.XXXXXX")"
  /usr/bin/mkfifo "$sentinel_root/release"
  /usr/bin/mkfifo "$sentinel_root/notify"
  chmod 600 "$sentinel_root/release" "$sentinel_root/notify"
  exec 7<> "$sentinel_root/release"
  exec 8<> "$sentinel_root/notify"
  active_child_root=$sentinel_root
  trap 'pending_signal=INT; pending_status=130; ready_read_interrupted=1' INT
  trap 'pending_signal=TERM; pending_status=143; ready_read_interrupted=1' TERM
  trap 'pending_signal=HUP; pending_status=129; ready_read_interrupted=1' HUP
  [[ $- == *m* ]] && monitor_was_enabled=1
  set -m
  (
    set +m
    trap ':' INT TERM HUP
    : > "$sentinel_root/alive"
    trap 'rm -f -- "$sentinel_root/alive"' EXIT
    workload_status=0
    set +e
    (
      trap 'exit 130' INT
      trap 'exit 143' TERM
      trap 'exit 129' HUP
      : > "$sentinel_root/ready"
      printf 'ready\n' >&8
      exec 8>&-
      "$@"
    )
    workload_status=$?
    set -e
    printf '%s\n' "$workload_status" > "$sentinel_root/status.tmp"
    mv "$sentinel_root/status.tmp" "$sentinel_root/status"
    : > "$sentinel_root/done"
    printf 'done\n' >&8
    exec 8>&-
    exec </dev/null >/dev/null 2>&1
    release_token=
    while [[ "$release_token" != release ]]; do
      IFS= read -r release_token <&7 || :
    done
  ) &
  active_child_pid=$!
  exec 9< "$sentinel_root/notify"
  exec 8>&-
  [[ "$monitor_was_enabled" -eq 1 ]] || set +m
  if ! read_active_child_notification ready ||
      [[ ! -f "$sentinel_root/ready" || -L "$sentinel_root/ready" ]]; then
    trap 'retain_on_signal INT 130' INT
    trap 'retain_on_signal TERM 143' TERM
    trap 'retain_on_signal HUP 129' HUP
    if [[ -n "$pending_signal" ]]; then
      retain_on_signal "$pending_signal" "$pending_status"
    fi
    exit_unproven_reason="child sentinel startup is unproven"
    return 1
  fi
  trap 'retain_on_signal INT 130' INT
  trap 'retain_on_signal TERM 143' TERM
  trap 'retain_on_signal HUP 129' HUP
  if [[ -n "$pending_signal" ]]; then
    retain_on_signal "$pending_signal" "$pending_status"
  fi
  if ! wait_for_active_child_done; then
    exit_unproven_reason="child sentinel completion is unproven"
    return 1
  fi
  if [[ ! -f "$sentinel_root/status" || -L "$sentinel_root/status" ]] ||
      ! IFS= read -r status < "$sentinel_root/status" ||
      [[ ! "$status" =~ ^[0-9]+$ || "$status" -gt 255 ]]; then
    exit_unproven_reason="child status is unproven"
    return 1
  fi
  active_child_group_is_drained "$active_child_pid" || group_status=$?
  if [[ "$group_status" -eq 1 ]]; then
    exit_unproven_reason="child process group survived status $status"
    return 1
  fi
  if [[ "$group_status" -ne 0 ]]; then
    exit_unproven_reason="child process group inspection failed"
    return 1
  fi
  child_pid=$active_child_pid
  if ! retire_active_child_sentinel; then
    exit_unproven_reason="child sentinel $child_pid retirement failed"
    return 1
  fi
  return "$status"
}

retire_control_tree() {
  chmod -R u+w "$1" && rm -rf -- "$1"
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

run_closed_git() {
  local git_home=/dev/null
  if [[ -n "$control_root" ]]; then
    git_home="$control_root/home"
  fi
  env -i \
    HOME="$git_home" \
    PATH="$(dirname "$git_path"):/usr/bin:/bin" \
    LC_ALL=C LANG=C TZ=UTC \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_CONFIG_SYSTEM=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    GIT_OPTIONAL_LOCKS=0 \
    GIT_TERMINAL_PROMPT=0 \
    "$git_path" -c core.hooksPath=/dev/null -c core.attributesFile=/dev/null \
      -c core.excludesFile=/dev/null -c core.fsmonitor=false "$@"
}

verify_replay_script() {
  local committed_blob live_blob
  committed_blob=$(run_closed_git -C "$module_root" rev-parse \
    "$expected_commit:spike/t4013/run-phase7-full-profile-replay.sh") || return 1
  live_blob=$(run_closed_git -C "$module_root" hash-object --no-filters "$script_path") || return 1
  [[ "$committed_blob" =~ ^[0-9a-f]{40}$ && "$live_blob" == "$committed_blob" ]]
}

verify_exact_source() {
  local commit status
  commit=$(run_closed_git -C "$source_root" rev-parse HEAD) || return 1
  [[ "$commit" == "$expected_commit" ]] || return 1
  status=$(run_closed_git -C "$source_root" status \
    --porcelain=v1 --untracked-files=all --ignored=matching) || return 1
  [[ -z "$status" ]]
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
checkout=${PHEBS_T4013_PHASE7_REPLAY_CHECKOUT:-}
if [[ "$checkout" != /* || ! -d "$checkout" || -L "$checkout" ]]; then
  echo "Set PHEBS_T4013_PHASE7_REPLAY_CHECKOUT to the absolute real source checkout" >&2
  exit 1
fi
module_root="$(cd -- "$checkout" && pwd -P)"
if [[ "$module_root" != "$checkout" ]]; then
  echo "Phase 7 full-profile replay source checkout must be canonical" >&2
  exit 1
fi
if [[ "${PHEBS_T4013_HOST_STABILITY_ATTESTATION:-}" != "$attestation" ]]; then
  echo "Set PHEBS_T4013_HOST_STABILITY_ATTESTATION=$attestation" >&2
  exit 1
fi
if [[ -n "${T4013_RUN_LOCK_FD:-}" ]]; then
  echo "Phase 7 full-profile replay refuses an ambient run-root lock descriptor" >&2
  exit 1
fi

git_path=$(canonical_executable_path git) || {
  echo "Phase 7 full-profile replay cannot bind Git for its exact source export" >&2
  exit 1
}
git_sha256=$(file_sha256 "$git_path") || {
  echo "Phase 7 full-profile replay Git digest is invalid" >&2
  exit 1
}
cd -- "$module_root"
if [[ "$(run_closed_git -C "$module_root" rev-parse HEAD)" != "$expected_commit" ]]; then
  echo "Phase 7 full-profile replay HEAD differs from the reviewed commit" >&2
  exit 1
fi
if [[ -n "$(run_closed_git -C "$module_root" status --porcelain=v1 --untracked-files=all)" ]]; then
  echo "Phase 7 full-profile replay requires an exact-clean checkout" >&2
  exit 1
fi
if ! verify_replay_script; then
  echo "Phase 7 full-profile replay wrapper differs from the reviewed commit" >&2
  exit 1
fi

parent=${PHEBS_T4013_PHASE7_REPLAY_PARENT:-/private/tmp}
if [[ "$parent" != /* || ! -d "$parent" || -L "$parent" ]]; then
  echo "Phase 7 full-profile replay parent must be an absolute real directory" >&2
  exit 1
fi
parent="$(cd -- "$parent" && pwd -P)"
if [[ "$parent" == "$module_root" || "$parent" == "$module_root/"* ]]; then
  echo "Phase 7 full-profile replay parent must be outside the source checkout" >&2
  exit 1
fi
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
git_template="$control_root/git-template"
mkdir -m 700 "$git_template"
source_root="$control_root/source"
run_active_child run_closed_git -C "$control_root" clone --quiet --shared --no-checkout \
  --template="$git_template" \
  --config core.hooksPath=/dev/null \
  --config core.attributesFile=/dev/null \
  --config core.excludesFile=/dev/null \
  --config core.fsmonitor=false \
  "$module_root" "$source_root"
chmod 700 "$source_root" "$source_root/.git"
run_active_child run_closed_git -c core.autocrlf=false -C "$source_root" \
  checkout --quiet --detach --force "$expected_commit"
if ! verify_exact_source; then
  echo "Phase 7 full-profile exact source export is invalid" >&2
  exit 1
fi
driver_path="$(dirname "$git_path"):$(dirname "$go_path"):$PATH"

child_status=0
cd -- "$source_root"
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
  GIT_CONFIG_NOSYSTEM=1 \
  GIT_CONFIG_GLOBAL=/dev/null \
  GIT_CONFIG_SYSTEM=/dev/null \
  GIT_ATTR_NOSYSTEM=1 \
  GIT_NO_LAZY_FETCH=1 \
  GIT_NO_REPLACE_OBJECTS=1 \
  GIT_OPTIONAL_LOCKS=0 \
  GIT_TERMINAL_PROMPT=0 \
  GIT_PAGER=cat \
  PHEBS_T4013_FULL_PROFILE_PHASE7_REPLAY=1 \
  PHEBS_T4013_PHASE7_REPLAY_ROOT="$run_root" \
  PHEBS_T4013_PHASE7_REPLAY_COMMIT="$expected_commit" \
  PHEBS_T4013_PHASE7_REPLAY_GO_SHA256="$go_sha256" \
  PHEBS_T4013_PHASE7_REPLAY_GIT_SHA256="$git_sha256" \
  PHEBS_T4013_PHASE7_REPLAY_SOURCE_ROOT="$source_root" \
  PHEBS_T4013_HOST_STABILITY_ATTESTATION="$attestation" \
  "$go_path" test ./spike/t4013 \
    -run '^TestProductionFullProfilePhase7Replay$' \
    -count=1 -v -timeout=20h || child_status=$?
cd -- "$module_root"
if [[ "$child_status" -ne 0 ]]; then
  exit "$child_status"
fi

if [[ "$(run_closed_git -C "$module_root" rev-parse HEAD)" != "$expected_commit" ||
      -n "$(run_closed_git -C "$module_root" status --porcelain=v1 --untracked-files=all)" ]]; then
  echo "Phase 7 full-profile replay checkout changed during execution" >&2
  exit 1
fi
if ! verify_replay_script; then
  echo "Phase 7 full-profile replay wrapper changed during execution" >&2
  exit 1
fi
if ! verify_exact_source; then
  echo "Phase 7 full-profile exact source changed during execution" >&2
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
observed_git_sha256=$(file_sha256 "$git_path") || {
  echo "Phase 7 full-profile replay cannot revalidate Git" >&2
  exit 1
}
if [[ "$observed_git_sha256" != "$git_sha256" ]]; then
  echo "Phase 7 full-profile replay Git changed during execution" >&2
  exit 1
fi
result_sha256=$(file_sha256 "$result") || {
  echo "Phase 7 full-profile replay result digest is unavailable" >&2
  exit 1
}
run_active_child retire_control_tree "$control_root"
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
