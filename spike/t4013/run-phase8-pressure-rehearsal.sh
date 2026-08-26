#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Phase 8 pressure rehearsal requires macOS APFS" >&2
  exit 1
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
module_root="$(cd -- "$script_dir/../.." && pwd -P)"
minimum_backing_available_kib=$((32 * 1024 * 1024))
lock=/private/tmp/phebs-t4013-phase8.lock
run_root=
image=
mount=
mount_parent_device=
lock_acquired=0
attach_attempted=0

require_backing_space() {
  local available_kib
  available_kib="$(LC_ALL=C df -Pk /private/tmp | awk 'NR == 2 { print $4 }')"
  case "$available_kib" in
    ''|*[!0-9]*) echo "Phase 8 backing capacity is unavailable" >&2; return 1 ;;
  esac
  if [[ "${#available_kib}" -gt 18 ]]; then
    echo "Phase 8 backing capacity is outside the supported numeric range" >&2
    return 1
  fi
  if [[ "$available_kib" -lt "$minimum_backing_available_kib" ]]; then
    echo "Phase 8 pressure rehearsal requires at least 32 GiB available on /private/tmp" >&2
    return 1
  fi
}

cleanup() {
  status=$?
  trap - EXIT
  detach_status=0
  mounted=0
  if [[ "$attach_attempted" -eq 1 ]]; then
    current_device="$(stat -f %d "$mount" 2>/dev/null || true)"
    if [[ -z "$current_device" || "$current_device" != "$mount_parent_device" ]]; then
      mounted=1
    fi
  fi
  if [[ "$mounted" -eq 1 ]]; then
    hdiutil detach -quiet "$mount" || detach_status=$?
  fi
  lock_status=0
  lock_retained=0
  if [[ "$lock_acquired" -eq 1 && "$detach_status" -ne 0 ]]; then
    lock_retained=1
  elif [[ "$lock_acquired" -eq 1 ]]; then
    rmdir "$lock" || lock_status=$?
  fi
  if [[ "$status" -eq 0 && "$detach_status" -eq 0 && "$lock_status" -eq 0 ]]; then
    rm -rf "$run_root"
  else
    if [[ -n "$image" ]]; then
      echo "Phase 8 diagnostic sparse image retained at $image" >&2
    fi
    if [[ "$detach_status" -ne 0 ]]; then
      echo "Phase 8 diagnostic volume remains mounted at $mount" >&2
    fi
    if [[ "$lock_retained" -eq 1 ]]; then
      echo "Phase 8 exclusive lock retained at $lock" >&2
    fi
  fi
  if [[ "$status" -eq 0 ]]; then
    status=$detach_status
  fi
  if [[ "$status" -eq 0 ]]; then
    status=$lock_status
  fi
  exit "$status"
}
trap cleanup EXIT

require_backing_space
if ! mkdir -m 700 "$lock"; then
  echo "Phase 8 pressure rehearsal is already active; review the lock before removal: $lock" >&2
  exit 1
fi
lock_acquired=1

run_root="$(mktemp -d /private/tmp/phebs-t4013-phase8.XXXXXX)"
case "$run_root" in
  /private/tmp/phebs-t4013-phase8.*) ;;
  *) echo "Phase 8 temporary root is invalid" >&2; exit 1 ;;
esac
image="$run_root/phase8.sparseimage"
mount="$run_root/mnt"
mkdir -m 700 "$mount"
mount_parent_device="$(stat -f %d "$mount")"

hdiutil create -quiet -nospotlight -size 16g -fs APFS -volname PHEBS_T4013_PHASE8 -type SPARSE "$image"
attach_attempted=1
hdiutil attach -quiet -nobrowse -mountpoint "$mount" "$image"
require_backing_space

cd -- "$module_root"
TMPDIR="$mount/" \
PHEBS_T4013_READINESS_REHEARSAL=1 \
PHEBS_T4013_PRESSURE_REHEARSAL=1 \
go test ./spike/t4013 \
  -run '^TestProductionPathReadinessRehearsal$/^structural-pressure$' \
  -count=1 -v -timeout=35m
