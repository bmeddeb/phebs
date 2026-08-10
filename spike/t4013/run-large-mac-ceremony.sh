#!/usr/bin/env bash

# Run and seal one newly frozen T40.13 neutral ceremony on a dedicated Mac.
# This driver never reads an operator corpus. It authors the frozen T40.1
# structural and semantic profiles and retains only source-free evidence.

set -euo pipefail
set -o noclobber
umask 077

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIRECTORY="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly SCRIPT_REPO_ROOT="$(cd "${SCRIPT_DIRECTORY}/../.." && pwd -P)"
readonly DEFAULT_BASE_PORT=41731
readonly EXECUTE_APPROVAL="execute-reviewed-neutral-t4013-plan"
readonly PREPARE_CONFIRM="prepare-neutral-t4013-custody"
readonly EXECUTE_CONFIRM="execute-neutral-t4013-and-destroy-custody"
readonly CLEANUP_CONFIRM="cleanup-neutral-t4013-custody"
readonly SIGNATURE_NAMESPACE="phebs-t4013"
readonly FREEZE_SIGNATURE_NAMESPACE="phebs-t4013-freeze"
readonly REVIEW_STOPPED_CEREMONY_ID_1="t40r1-neutral-01"
readonly REVIEW_STOPPED_CEREMONY_ID_2="t40r1-neutral-02"
readonly REVIEW_STOPPED_CEREMONY_ID_3="t40r1-neutral-03"
readonly REVIEW_STOPPED_CEREMONY_ID_4="t40r1-neutral-04"
readonly REVIEW_STOPPED_CEREMONY_ID_5="t40r1-neutral-05"
readonly REVIEW_STOPPED_CEREMONY_ID_6="t40r1-neutral-06"
readonly REVIEW_STOPPED_CEREMONY_ID_7="t40r1-neutral-07"
readonly REVIEW_STOPPED_CEREMONY_ID_8="t40r1-neutral-08"
readonly REVIEW_STOPPED_CEREMONY_ID_9="t40r1-neutral-09"
readonly REVIEW_STOPPED_CEREMONY_ID_10="t40r1-neutral-10"
readonly REVIEW_STOPPED_CEREMONY_ID_11="t40r1-neutral-11"
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

die() {
  printf '%s: %s\n' "$SCRIPT_NAME" "$*" >&2
  exit 1
}

note() {
  printf '%s\n' "$*"
}

usage() {
  cat <<EOF
Usage:
  $SCRIPT_NAME preflight
  $SCRIPT_NAME freeze <ceremony-id>
  $SCRIPT_NAME execute <ceremony-id> <approved-plan-digest> $EXECUTE_APPROVAL
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
  local value="$1"
  case "$value" in
    "$REVIEW_STOPPED_CEREMONY_ID_1"|"$REVIEW_STOPPED_CEREMONY_ID_2"|"$REVIEW_STOPPED_CEREMONY_ID_3"|"$REVIEW_STOPPED_CEREMONY_ID_4"|"$REVIEW_STOPPED_CEREMONY_ID_5"|"$REVIEW_STOPPED_CEREMONY_ID_6"|"$REVIEW_STOPPED_CEREMONY_ID_7"|"$REVIEW_STOPPED_CEREMONY_ID_8"|"$REVIEW_STOPPED_CEREMONY_ID_9"|"$REVIEW_STOPPED_CEREMONY_ID_10"|"$REVIEW_STOPPED_CEREMONY_ID_11")
      die "ceremony id $value is permanently review-stopped; use a fresh id"
      ;;
  esac
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
  status="$(git -C "$REPO_REAL" status --porcelain=v1 --untracked-files=all)"
  [[ -z "$status" ]] || die "phebs checkout is not clean; commit or remove every tracked/untracked change"
  commit="$(git -C "$REPO_REAL" rev-parse --verify HEAD)"
  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || die "phebs HEAD is not an exact commit"
  git -C "$REPO_REAL" cat-file -e "${commit}^{commit}"
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

preflight() {
  local command_name
  for command_name in awk cmp cp date df find git go grep lsof mkdir mktemp mv rm sed shasum sort ssh-keygen surreal sysctl tar uname uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  initialize_ceremony_root
  require_clean_checkout
  host_preflight
  (cd "$REPO_REAL" && go mod download all)
  (cd "$REPO_REAL" && go test ./spike/t4013/... -count=1)
  require_clean_checkout
  note "T40.13 harness tests: PASS"
}

verification_preflight() {
  local command_name
  for command_name in awk cmp find git go mktemp rm sed shasum sort ssh-keygen tar uniq wc; do
    require_command "$command_name"
  done
  initialize_repository
  require_clean_checkout
  (cd "$REPO_REAL" && go test ./spike/t4013/... -count=1)
  note "source-free verifier checkout: PASS"
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
  reject_review_stopped_id "$ceremony_id"
  preflight
  select_signing_key "$ceremony_id"
  ensure_signing_key
  run_root="$(run_root_for "$ceremony_id")"
  [[ ! -e "$run_root" && ! -L "$run_root" ]] || die "ceremony id already exists and may not be overwritten: $ceremony_id"
  evidence_root="${run_root}/evidence"
  private_root="${run_root}/private"
  mkdir -m 700 "$run_root" "$evidence_root" "$private_root"
  commit="$(git -C "$REPO_REAL" rev-parse HEAD)"
  (cd "$REPO_REAL" && env GOPROXY=off go run ./spike/t4013/cmd/t4013-freeze \
    -root "$REPO_REAL" \
    -source-commit "$commit" \
	-bind-host-toolchain \
    -output "${evidence_root}/plan.json") >/dev/null
  digest="$(plan_digest_for "${evidence_root}/plan.json")"
  frozen_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  public_key="$(awk 'NF >= 2 { print $1 " " $2; exit }' "${SIGNING_KEY}.pub")"
  fingerprint="$(ssh-keygen -lf "${SIGNING_KEY}.pub" -E sha256 | awk '{print $2}')"
  [[ "$public_key" == ssh-ed25519\ * && "$fingerprint" == SHA256:* ]] ||
    die "ceremony signing identity is invalid"
  cp -p "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub"
  printf '%s %s\n' "$SIGNER_IDENTITY" "$public_key" > "${evidence_root}/allowed_signers"
  printf '{\n  "schema": "t4013-freeze-envelope-v1",\n  "ceremony_id": "%s",\n  "source_commit": "%s",\n  "plan_digest": "%s",\n  "signer_fingerprint": "%s",\n  "frozen_at": "%s"\n}\n' \
    "$ceremony_id" "$commit" "$digest" "$fingerprint" "$frozen_at" > "${evidence_root}/freeze.json"
  ssh-keygen -Y sign -f "$SIGNING_KEY" -n "$FREEZE_SIGNATURE_NAMESPACE" \
    "${evidence_root}/freeze.json" >/dev/null
  note "frozen ceremony: $ceremony_id"
  note "source commit: $commit"
  note "plan path: ${evidence_root}/plan.json"
  note "plan digest: $digest"
  note "signer fingerprint: $fingerprint"
  note "STOP: independently review this plan before execute."
}

cleanup_prepared() {
  local plan_path="$1" prepared_path="$2"
  if [[ -f "$prepared_path" && ! -L "$prepared_path" ]]; then
    (cd "$REPO_REAL" && env GOPROXY=off go run ./spike/t4013/cmd/t4013-cleanup \
      -root "$REPO_REAL" \
      -plan "$plan_path" \
      -prepared "$prepared_path" \
      -confirm "$CLEANUP_CONFIRM")
  fi
}

cleanup_trap_command() {
  local command
  printf -v command 'cleanup_prepared %q %q || true' "$1" "$2"
  printf '%s' "$command"
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
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_commit" == "$(git -C "$REPO_REAL" rev-parse HEAD)" ]] ||
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
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ && "$source_commit" == "$(git -C "$REPO_REAL" rev-parse HEAD)" ]] ||
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
  if ! (cd "$REPO_REAL" && env GOPROXY=off go run ./spike/t4013/cmd/t4013-receipt \
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
  local package package_tmp package_digest package_sidecar
  run_root="$(run_root_for "$ceremony_id")"
  evidence_root="${run_root}/evidence"
  source_commit="$(git -C "$REPO_REAL" rev-parse HEAD)"
  plan_digest="$(plan_digest_for "${evidence_root}/plan.json")"
  generated_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  require_exact_inventory "$evidence_root" \
    allowed_signers freeze.json freeze.json.sig observation.json plan.json results.json signer.pub
  cmp -s "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub" || die "ceremony signing key changed after freeze"
  verify_frozen_identity "$evidence_root"
  [[ ! -e "${evidence_root}/manifest.json" && ! -L "${evidence_root}/manifest.json" &&
     ! -e "${evidence_root}/SHA256SUMS" && ! -L "${evidence_root}/SHA256SUMS" &&
     ! -e "${evidence_root}/SHA256SUMS.sig" && ! -L "${evidence_root}/SHA256SUMS.sig" ]] ||
    die "source-free evidence was already sealed"
  printf '{\n  "schema": "t4013-source-free-transfer-v1",\n  "ceremony_id": "%s",\n  "source_commit": "%s",\n  "plan_digest": "%s",\n  "sealed_at": "%s"\n}\n' \
    "$ceremony_id" "$source_commit" "$plan_digest" "$generated_at" > "${evidence_root}/manifest.json"
  (cd "$evidence_root" && shasum -a 256 \
    allowed_signers freeze.json freeze.json.sig manifest.json observation.json plan.json results.json signer.pub > SHA256SUMS)
  ssh-keygen -Y sign -f "$SIGNING_KEY" -n "$SIGNATURE_NAMESPACE" "${evidence_root}/SHA256SUMS" >/dev/null
  verify_evidence_directory "$evidence_root"
  package="${run_root}/${ceremony_id}-source-free.tgz"
  package_tmp="${package}.tmp"
  package_sidecar="${package}.sha256"
  [[ ! -e "$package" && ! -L "$package" && ! -e "$package_tmp" && ! -L "$package_tmp" &&
     ! -e "$package_sidecar" && ! -L "$package_sidecar" ]] ||
    die "source-free package already exists"
  COPYFILE_DISABLE=1 tar -C "$run_root" -czf "$package_tmp" evidence
  mv "$package_tmp" "$package"
  (( $(wc -c < "$package") <= MAXIMUM_TRANSFER_PACKAGE_BYTES )) ||
    die "source-free package exceeds its fixed 4-MiB transfer bound"
  package_digest="$(shasum -a 256 "$package" | awk '{print $1}')"
  printf '%s  %s\n' "$package_digest" "$(basename "$package")" > "$package_sidecar"
  note "sealed source-free package: $package"
  note "package sha256: $package_digest"
}

execute_ceremony() {
  local ceremony_id="$1" approved_digest="$2" approval="$3"
  local run_root evidence_root private_root plan_path prepared_path observation_path results_path custody_path
  local actual_digest execute_status cleanup_trap path
  reject_review_stopped_id "$ceremony_id"
  [[ "$approval" == "$EXECUTE_APPROVAL" ]] || die "execution approval phrase is invalid"
  preflight
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
  [[ -d "$run_root" && ! -L "$run_root" && -d "$evidence_root" && -d "$private_root" ]] ||
    die "frozen ceremony directory is missing or invalid"
  [[ -f "$plan_path" && ! -L "$plan_path" ]] || die "frozen plan is missing or symlinked"
  for path in "$prepared_path" "$observation_path" "$results_path" "$custody_path"; do
    [[ ! -e "$path" && ! -L "$path" ]] || die "ceremony output or custody already exists: $path"
  done
  actual_digest="$(plan_digest_for "$plan_path")"
  [[ "$approved_digest" == "$actual_digest" ]] || die "approved plan digest differs from the frozen plan"
  require_exact_inventory "$evidence_root" allowed_signers freeze.json freeze.json.sig plan.json signer.pub
  cmp -s "${SIGNING_KEY}.pub" "${evidence_root}/signer.pub" || die "ceremony signing key changed after freeze"
  verify_frozen_identity "$evidence_root"
  cleanup_trap="$(cleanup_trap_command "$plan_path" "$prepared_path")"
  trap "$cleanup_trap" EXIT
  (cd "$REPO_REAL" && env GOPROXY=off go run ./spike/t4013/cmd/t4013-prepare \
    -root "$REPO_REAL" \
    -workspace "$custody_path" \
    -plan "$plan_path" \
    -output "$prepared_path" \
    -base-port "$BASE_PORT" \
    -confirm "$PREPARE_CONFIRM")
  execute_status=0
  (cd "$REPO_REAL" && env GOPROXY=off go run ./spike/t4013/cmd/t4013-execute \
    -root "$REPO_REAL" \
    -plan "$plan_path" \
    -prepared "$prepared_path" \
    -observation "$observation_path" \
    -confirm "$EXECUTE_CONFIRM") || execute_status=$?
  if ! cleanup_prepared "$plan_path" "$prepared_path"; then
    die "exact private prepared manifest cleanup failed"
  fi
  trap - EXIT
  [[ ! -e "$custody_path" && ! -e "$prepared_path" ]] || die "private custody survived execution cleanup"
  [[ -f "$observation_path" && ! -L "$observation_path" ]] ||
    die "execution produced no source-free observation; no evidence was sealed"
  (cd "$REPO_REAL" && env GOPROXY=off go run ./spike/t4013/cmd/t4013-receipt \
    -plan "$plan_path" \
    -plan-digest "$actual_digest" \
    -observation "$observation_path" \
    -output "$results_path")
  seal_evidence "$ceremony_id"
  if (( execute_status == 0 )); then
    note "ceremony completed; the sealed receipt still requires independent review"
  else
    note "ceremony stopped with command status $execute_status; its stopped receipt was sealed for review"
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
