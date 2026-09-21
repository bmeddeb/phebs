#!/bin/sh
# Stage the neutral-demo receipt bundles at the fixed, documented receipt
# fixture root so the dev instance derives the same `local/...` repository
# identities on every machine (macOS checkout, Ubuntu CI, or any other
# checkout location).
#
# The fixture root is defined once, in ui/receipts/fixtureRoot.ts
# (RECEIPT_FIXTURE_ROOT); this script extracts it from there and fails
# closed if the extraction yields nothing usable, so the shell and
# TypeScript sides cannot drift apart.
#
# Safety contract (the fixture root is a shared /tmp path, so every write
# is validated before it happens):
#   * the root must be a real, owner-only (0700), current-user directory
#     without an ACL; existing permissions are never changed;
#   * a missing root is created with mkdir -m 700, then re-validated;
#   * each destination is inspected BEFORE writing: symlinks, non-regular
#     files, and files not owned by the current user are refused;
#   * a staged bundle whose bytes already match the source is reused
#     untouched (content, mtime, and mode preserved);
#   * a staged bundle whose bytes differ is refused, never truncated or
#     overwritten in place -- remove it by hand if restaging is intended;
#   * first publication hard-links a complete temp file into place without
#     replacement; a losing publisher revalidates the winner's bytes.
#
# Usage:
#   scripts/stage-receipt-fixtures.sh             stage, print a summary
#   scripts/stage-receipt-fixtures.sh --env       stage, print `export` lines for eval
#   scripts/stage-receipt-fixtures.sh --print-root print the fixture root only
set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
fixture_ts="$repo_root/ui/receipts/fixtureRoot.ts"

# Single source of truth: the canonical root lives in fixtureRoot.ts.
# shellcheck disable=SC2016
fixture_root="$(sed -n "s/^export const RECEIPT_FIXTURE_ROOT = '\([^']*\)'.*/\1/p" "$fixture_ts")"
case "$fixture_root" in
  /*) ;;
  *)
    printf 'stage-receipt-fixtures: could not read an absolute RECEIPT_FIXTURE_ROOT from %s\n' "$fixture_ts" >&2
    exit 1
    ;;
esac
case "$fixture_root" in
  */)
    printf 'stage-receipt-fixtures: refusing fixture root with trailing slash: %s\n' "$fixture_root" >&2
    exit 1
    ;;
esac

t307_src="$repo_root/docs/fixtures/t30.7-neutral-service/t307-neutral-service.bundle"
t323_src="$repo_root/spike/t323/t323-neutral-corpus.bundle"
t307_dst="$fixture_root/t307-neutral-service.bundle"
t323_dst="$fixture_root/t323-neutral-corpus.bundle"

for src in "$t307_src" "$t323_src"; do
  if [ ! -f "$src" ]; then
    printf 'stage-receipt-fixtures: missing fixture bundle %s\n' "$src" >&2
    exit 1
  fi
done

mode="${1---stage}"
case "$mode" in
  --print-root)
    printf '%s\n' "$fixture_root"
    exit 0
    ;;
  --env | --stage) ;;
  *)
    printf 'usage: stage-receipt-fixtures.sh [--env|--print-root]\n' >&2
    exit 2
    ;;
esac

refuse() {
  printf 'stage-receipt-fixtures: refusing: %s\n' "$1" >&2
  exit 1
}

owned_by_me() {
  # $1: an existing path that is not a symlink. True iff its owner uid is
  # the current uid. Any lookup failure compares unequal, i.e. fail closed.
  [ "$(ls -ldn "$1" 2>/dev/null | awk '{print $3}')" = "$(id -u)" ]
}

# A failed creation may mean another publisher won; validate either result.
if [ ! -e "$fixture_root" ] && [ ! -L "$fixture_root" ]; then
  mkdir -m 700 "$fixture_root" 2>/dev/null || :
fi
if [ -L "$fixture_root" ]; then
  refuse "fixture root $fixture_root is a symlink"
fi
if [ ! -d "$fixture_root" ]; then
  refuse "fixture root $fixture_root is not a directory"
fi
if ! owned_by_me "$fixture_root"; then
  refuse "fixture root $fixture_root is not owned by uid $(id -u)"
fi
# On Darwin, an xattr marker can hide the ACL marker, so inspect ACL rows
# too. Plain xattrs do not grant access. GNU ls marks an ACL with '+'.
if [ "$(uname -s)" = Darwin ]; then
  root_metadata="$(LC_ALL=C ls -lden "$fixture_root")" || refuse "could not inspect fixture root"
else
  root_metadata="$(LC_ALL=C ls -ldn "$fixture_root")" || refuse "could not inspect fixture root"
fi
case "$(printf '%s\n' "$root_metadata" | awk '{print $1}')" in
  drwx------ | drwx------@) ;;
  *) refuse "fixture root $fixture_root must have mode 0700 without an ACL" ;;
esac

tmp=""
trap '[ -z "$tmp" ] || rm -f "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

reuse_existing() {
  # src/dst belong to stage_one. Return false only when dst is absent.
  if [ -L "$dst" ]; then
    refuse "destination $dst is a symlink"
  fi
  if [ -e "$dst" ]; then
    if [ ! -f "$dst" ]; then
      refuse "destination $dst is not a regular file"
    fi
    if ! owned_by_me "$dst"; then
      refuse "destination $dst is not owned by uid $(id -u)"
    fi
    if cmp -s "$src" "$dst"; then
      printf 'stage-receipt-fixtures: reusing identical staged bundle %s\n' "$dst" >&2
      return 0
    fi
    refuse "destination $dst exists with different bytes; refusing to overwrite in place (remove it first to restage)"
  fi
  return 1
}

stage_one() {
  src="$1"
  dst="$2"
  if reuse_existing; then
    return 0
  fi
  tmp="$(mktemp "$fixture_root/.stage-bundle-XXXXXX")" || refuse "could not create temp file in $fixture_root"
  if ! cp "$src" "$tmp" || ! chmod 644 "$tmp"; then
    refuse "could not prepare $dst"
  fi
  # POSIX link performs link(2) on this exact destination, unlike ln's
  # directory-target behavior. It never replaces a file or follows a
  # destination symlink to a directory. No stale lock or wait is needed.
  if ! link "$tmp" "$dst" 2>/dev/null; then
    if ! reuse_existing; then
      refuse "could not publish $dst"
    fi
  fi
  rm -f "$tmp"
  tmp=""
}

stage_one "$t307_src" "$t307_dst"
stage_one "$t323_src" "$t323_dst"

if [ "$mode" = "--env" ]; then
  # Stdout carries ONLY the export lines in --env mode: every diagnostic
  # above goes to stderr so eval sees exactly the two assignments.
  printf 'export PHEBS_T307_NEUTRAL_SERVICE_REPO=%s\n' "'$t307_dst'"
  printf 'export PHEBS_T344_SERVICE_SEARCH_REPO=%s\n' "'$t323_dst'"
else
  printf 'staged receipt fixtures at %s\n' "$fixture_root"
  printf '  %s\n  %s\n' "$t307_dst" "$t323_dst"
fi
