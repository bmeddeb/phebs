#!/bin/sh
# Regression tests for scripts/stage-receipt-fixtures.sh (R3 defects 6 & 7).
#
# Self-contained: every case builds a disposable repo skeleton under a temp
# dir, copies the staging script into the skeleton (so the tested bytes are
# the committed bytes), and points the skeleton's fixtureRoot.ts at a fake
# fixture root under that temp dir. Nothing here ever touches the real
# /tmp/phebs-receipts-fixtures.
#
# Run: sh scripts/test-stage-receipt-fixtures.sh
# Exit 0 only if every case passes; any failure prints its case name.
set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
stage_src="$repo_root/scripts/stage-receipt-fixtures.sh"

work="$(mktemp -d "${TMPDIR:-/tmp}/stage-fixtures-test-XXXXXX")"
children=""
cleanup() {
  # Release any test barriers before joining our direct staging children.
  # Each barrier also has its own five-second bound, including on failure.
  touch "$work/release-all"
  for child in $children; do
    wait "$child" 2>/dev/null || :
  done
  rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

n=0
pass=0
fail=0
skip=0
ok() { n=$((n + 1)); pass=$((pass + 1)); printf 'ok %d - %s\n' "$n" "$1"; }
bad() { n=$((n + 1)); fail=$((fail + 1)); printf 'not ok %d - %s\n' "$n" "$1: $2"; }

# make_skeleton <name> [spike-dir-override]
# Builds $work/<name> as a fake repo root whose scripts/stage-receipt-fixtures.sh
# is a fresh copy of the committed script, whose docs/ and spike/ resolve to
# the real source bundles, and whose ui/receipts/fixtureRoot.ts points at a
# fake fixture root. Prints the skeleton path.
make_skeleton() {
  name="$1"
  skel="$work/$name"
  mkdir -p "$skel/scripts" "$skel/ui/receipts"
  cp "$stage_src" "$skel/scripts/stage-receipt-fixtures.sh"
  ln -s "$repo_root/docs" "$skel/docs"
  if [ "${2-}" != "" ]; then
    ln -s "$2" "$skel/spike"
  else
    ln -s "$repo_root/spike" "$skel/spike"
  fi
  fakeroot="$skel/fixture-root"
  printf "export const RECEIPT_FIXTURE_ROOT = '%s'\n" "$fakeroot" >"$skel/ui/receipts/fixtureRoot.ts"
  printf '%s' "$skel"
}

# staged_paths <skeleton> ; prints "<t307-dst> <t323-dst> <fakeroot>"
staged_paths() {
  skel="$1"
  fakeroot="$skel/fixture-root"
  printf '%s %s %s' \
    "$fakeroot/t307-neutral-service.bundle" \
    "$fakeroot/t323-neutral-corpus.bundle" \
    "$fakeroot"
}

mode644() {
  # $1: path. True iff it is a regular file with exactly -rw-r--r--.
  case "$(ls -ld "$1" 2>/dev/null)" in
    -rw-r--r--*) return 0 ;;
    *) return 1 ;;
  esac
}

no_temp_leftovers() {
  # $1: fixture root. True iff no .stage-bundle-* temp files remain.
  [ -z "$(find "$1" -maxdepth 1 -name '.stage-bundle-*' -print 2>/dev/null)" ]
}

same_mtime() {
  [ ! "$1" -nt "$2" ] && [ ! "$1" -ot "$2" ]
}

# ---------------------------------------------------------------- defect 6

# 1: successful staging returns 0 and emits exactly the expected exports.
skel="$(make_skeleton d6-success)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
out="$work/d6-success.out"
if sh "$skel/scripts/stage-receipt-fixtures.sh" --env >"$out" 2>"$work/d6-success.err"; then
  if [ "$(wc -l <"$out")" -eq 2 ] \
    && grep -qx "export PHEBS_T307_NEUTRAL_SERVICE_REPO='$t307_dst'" "$out" \
    && grep -qx "export PHEBS_T344_SERVICE_SEARCH_REPO='$t323_dst'" "$out" \
    && [ -f "$t307_dst" ] && [ -f "$t323_dst" ] \
    && cmp -s "$repo_root/docs/fixtures/t30.7-neutral-service/t307-neutral-service.bundle" "$t307_dst" \
    && cmp -s "$repo_root/spike/t323/t323-neutral-corpus.bundle" "$t323_dst" \
    && mode644 "$t307_dst" && mode644 "$t323_dst" \
    && no_temp_leftovers "$fakeroot"; then
    ok "d6: successful staging returns 0 and emits the expected exports"
  else
    bad "d6: successful staging returns 0 and emits the expected exports" "output or staged bytes wrong"
  fi
else
  cat "$work/d6-success.err" >&2
  bad "d6: successful staging returns 0 and emits the expected exports" "nonzero exit"
fi

# 2: a failing staging run makes the CI wrapper exit nonzero BEFORE eval
# (and therefore before make dev could start). The wrapper below mirrors the
# checked capture-then-eval pattern in .github/workflows/ci.yml verbatim.
mkdir -p "$work/empty-spike"
skel="$(make_skeleton d6-failure "$work/empty-spike")"
script="$skel/scripts/stage-receipt-fixtures.sh"
if sh "$script" --env >/dev/null 2>&1; then
  bad "d6: failing staging script exits nonzero" "script exited 0 with a missing bundle"
else
  ok "d6: failing staging script exits nonzero"
fi
PHEBS_T307_NEUTRAL_SERVICE_REPO=""
PHEBS_T344_SERVICE_SEARCH_REPO=""
export PHEBS_T307_NEUTRAL_SERVICE_REPO PHEBS_T344_SERVICE_SEARCH_REPO
if (
  receipt_env="$(sh "$script" --env)" || exit 1
  eval "$receipt_env"
  touch "$work/d6-dev-started"
); then
  bad "d6: failing staging makes the CI wrapper exit nonzero before eval" "wrapper exited 0"
else
  if [ ! -e "$work/d6-dev-started" ] \
    && [ -z "$PHEBS_T307_NEUTRAL_SERVICE_REPO" ] \
    && [ -z "$PHEBS_T344_SERVICE_SEARCH_REPO" ]; then
    ok "d6: failing staging makes the CI wrapper exit nonzero before eval"
  else
    bad "d6: failing staging makes the CI wrapper exit nonzero before eval" "eval ran or dev sentinel exists"
  fi
fi

# ---------------------------------------------------------------- defect 7

# 3: missing root is created safely (real dir, owned by us, bundles staged).
skel="$(make_skeleton d7-missing-root)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>&1 \
  && [ -d "$fakeroot" ] && [ ! -L "$fakeroot" ] \
  && [ "$(LC_ALL=C ls -ld "$fakeroot" | awk '{print substr($1, 1, 10)}')" = "drwx------" ] \
  && [ -f "$t307_dst" ] && [ -f "$t323_dst" ] \
  && mode644 "$t307_dst" && mode644 "$t323_dst" \
  && no_temp_leftovers "$fakeroot"; then
  ok "d7: missing root created safely"
else
  bad "d7: missing root created safely" "root not created or bundles wrong"
fi

# 4: identical bytes are reused; mtime and content are preserved.
skel="$(make_skeleton d7-identical)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>&1
# A fixed old timestamp is exact on macOS and Linux. A rewrite now must
# differ from it; no wall-clock sleep or subsecond ordering is involved.
touch -t 200001010000 "$t307_dst" "$t323_dst" "$work/d7-ref"
chmod 600 "$t307_dst" "$t323_dst"
if sh "$skel/scripts/stage-receipt-fixtures.sh" --env >/dev/null 2>"$work/d7-identical.err"; then
  if same_mtime "$t307_dst" "$work/d7-ref" && same_mtime "$t323_dst" "$work/d7-ref" \
    && cmp -s "$repo_root/docs/fixtures/t30.7-neutral-service/t307-neutral-service.bundle" "$t307_dst" \
    && cmp -s "$repo_root/spike/t323/t323-neutral-corpus.bundle" "$t323_dst" \
    && [ "$(LC_ALL=C ls -ld "$t307_dst" | awk '{print substr($1, 1, 10)}')" = "-rw-------" ] \
    && [ "$(LC_ALL=C ls -ld "$t323_dst" | awk '{print substr($1, 1, 10)}')" = "-rw-------" ] \
    && no_temp_leftovers "$fakeroot"; then
    ok "d7: identical bytes reused, mtime and content preserved"
  else
    bad "d7: identical bytes reused, mtime and content preserved" "bundles were rewritten"
  fi
else
  bad "d7: identical bytes reused, mtime and content preserved" "restage exited nonzero"
fi
# Prove that the same timestamp predicate rejects an actual rewrite.
touch "$t307_dst"
if same_mtime "$t307_dst" "$work/d7-ref"; then
  bad "d7: timestamp oracle rejects a rewrite" "rewritten file still looks unchanged"
else
  ok "d7: timestamp oracle rejects a rewrite"
fi

# 5: mismatched bytes are refused; the staged file is left untouched.
skel="$(make_skeleton d7-mismatch)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>&1
printf 'tampered-by-test-not-the-source-bundle\n' >"$t307_dst"
cp "$t307_dst" "$work/d7-tampered-copy"
if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>"$work/d7-mismatch.err"; then
  bad "d7: mismatched bytes refused, staged file untouched" "restage exited 0 over differing bytes"
else
  if cmp -s "$t307_dst" "$work/d7-tampered-copy" \
    && grep -q "different bytes" "$work/d7-mismatch.err" \
    && no_temp_leftovers "$fakeroot"; then
    ok "d7: mismatched bytes refused, staged file untouched"
  else
    bad "d7: mismatched bytes refused, staged file untouched" "staged file was modified"
  fi
fi

# 6: a symlink at a destination is refused and its target is untouched.
skel="$(make_skeleton d7-dst-symlink)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>&1
printf 'decoy-target-content\n' >"$work/d7-decoy"
rm "$t307_dst"
ln -s "$work/d7-decoy" "$t307_dst"
if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>"$work/d7-dst-symlink.err"; then
  bad "d7: symlink at destination refused, target untouched" "restage exited 0 over a symlink"
else
  if [ -L "$t307_dst" ] \
    && [ "$(cat "$work/d7-decoy")" = "decoy-target-content" ] \
    && grep -q "is a symlink" "$work/d7-dst-symlink.err"; then
    ok "d7: symlink at destination refused, target untouched"
  else
    bad "d7: symlink at destination refused, target untouched" "symlink followed or target modified"
  fi
fi

# 7: a symlink at the fixture root is refused and never traversed.
skel="$(make_skeleton d7-root-symlink)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
mkdir -p "$work/d7-root-target"
rm -rf "$fakeroot"
ln -s "$work/d7-root-target" "$fakeroot"
if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>"$work/d7-root-symlink.err"; then
  bad "d7: symlink at fixture root refused, never traversed" "staging exited 0 through a symlinked root"
else
  if [ -z "$(ls -A "$work/d7-root-target" 2>/dev/null)" ] \
    && grep -q "is a symlink" "$work/d7-root-symlink.err"; then
    ok "d7: symlink at fixture root refused, never traversed"
  else
    bad "d7: symlink at fixture root refused, never traversed" "wrote through the symlink"
  fi
fi

# 8: a non-directory at the fixture root is refused.
skel="$(make_skeleton d7-root-file)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
printf 'not-a-directory\n' >"$fakeroot"
if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>&1; then
  bad "d7: non-directory at fixture root refused" "staging exited 0 over a regular file"
else
  ok "d7: non-directory at fixture root refused"
fi

# 9: a root owned by another user is refused (needs privilege to set up).
skel="$(make_skeleton d7-root-owner)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
mkdir -p "$fakeroot"
if [ "$(id -u)" -eq 0 ] && id nobody >/dev/null 2>&1; then
  chown "$(id -u nobody):$(id -g nobody)" "$fakeroot"
  if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>"$work/d7-root-owner.err"; then
    bad "d7: wrong-owner root refused" "staging exited 0 on a foreign-owned root"
  else
    if grep -q "not owned by" "$work/d7-root-owner.err"; then
      ok "d7: wrong-owner root refused"
    else
      bad "d7: wrong-owner root refused" "refused for the wrong reason"
    fi
  fi
  chown "$(id -u):$(id -g)" "$fakeroot"
else
  skip=$((skip + 1))
  printf 'SKIP d7: wrong-owner root refused (needs uid 0 to chown; running as uid %s)\n' "$(id -u)"
fi

# 10: --print-root is a read-only probe: exits 0, prints the root, stages nothing.
skel="$(make_skeleton d7-print-root)"
# shellcheck disable=SC2086
set -- $(staged_paths "$skel"); t307_dst="$1"; t323_dst="$2"; fakeroot="$3"
if [ "$(sh "$skel/scripts/stage-receipt-fixtures.sh" --print-root 2>/dev/null)" = "$fakeroot" ] \
  && [ ! -e "$fakeroot" ]; then
  ok "d7: --print-root is a read-only probe"
else
  bad "d7: --print-root is a read-only probe" "wrong output or staged something"
fi

# 11: unknown flags still exit 2.
skel="$(make_skeleton d7-usage)"
rc=0
sh "$skel/scripts/stage-receipt-fixtures.sh" --bogus >/dev/null 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then
  ok "d7: unknown flag exits 2"
else
  bad "d7: unknown flag exits 2" "exit was $rc"
fi

# --- R4: private roots and deterministic concurrent first publications. ---
for unsafe_mode in 755 770 777; do
  skel="$(make_skeleton "r4-mode-$unsafe_mode")"
  fakeroot="$skel/fixture-root"
  mkdir -m "$unsafe_mode" "$fakeroot"
  if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>"$work/mode.err"; then
    bad "r4: mode $unsafe_mode root refused" "staging accepted a non-private root"
  elif grep -q "must have mode 0700" "$work/mode.err" && [ -z "$(ls -A "$fakeroot")" ]; then
    ok "r4: mode $unsafe_mode root refused"
  else
    bad "r4: mode $unsafe_mode root refused" "wrong refusal or files were staged"
  fi
done

if [ "$(uname -s)" = Darwin ]; then
  skel="$(make_skeleton r4-acl)"
  fakeroot="$skel/fixture-root"
  mkdir -m 700 "$fakeroot"
  chmod +a 'everyone allow add_file' "$fakeroot"
  if sh "$skel/scripts/stage-receipt-fixtures.sh" >/dev/null 2>"$work/acl.err"; then
    bad "r4: ACL-bearing 0700 root refused" "staging accepted an ACL"
  elif grep -q "must have mode 0700" "$work/acl.err" && [ -z "$(ls -A "$fakeroot")" ]; then
    ok "r4: ACL-bearing 0700 root refused"
  else
    bad "r4: ACL-bearing 0700 root refused" "wrong refusal or files were staged"
  fi
else
  skip=$((skip + 1))
  printf 'SKIP r4: Darwin ACL marker check (requires macOS)\n'
fi

# Pause after the script's absence check but before the real atomic link.
# No production hook is needed: the shim changes scheduling only, then execs
# the native utility with the original arguments. All paths are disposable.
real_link="$(command -v link)"
mkdir "$work/bin"
cat >"$work/bin/link" <<'EOF'
#!/bin/sh
set -eu
case "$2" in
  */t307-neutral-service.bundle)
    touch "$STAGING_TEST_BARRIER.ready"
    tries=0
    while [ ! -f "$STAGING_TEST_BARRIER.release" ] && [ ! -f "$STAGING_TEST_RELEASE_ALL" ]; do
      tries=$((tries + 1))
      [ "$tries" -le 100 ] || exit 124
      sleep 0.05
    done
    ;;
esac
exec "$STAGING_TEST_LINK" "$@"
EOF
chmod 700 "$work/bin/link"
STAGING_TEST_LINK="$real_link"
STAGING_TEST_RELEASE_ALL="$work/release-all"
export STAGING_TEST_LINK STAGING_TEST_RELEASE_ALL

wait_ready() {
  ready_tries=0
  while [ ! -f "$1.ready" ] || [ ! -f "$2.ready" ]; do
    ready_tries=$((ready_tries + 1))
    [ "$ready_tries" -le 100 ] || return 1
    sleep 0.05
  done
}

for race_kind in identical differing; do
  first="$(make_skeleton "r4-$race_kind-a")"
  second="$(make_skeleton "r4-$race_kind-b")"
  shared_root="$first/fixture-root"
  printf "export const RECEIPT_FIXTURE_ROOT = '%s'\n" "$shared_root" >"$second/ui/receipts/fixtureRoot.ts"
  if [ "$race_kind" = differing ]; then
    # Replace only this skeleton's docs symlink, never the real fixture.
    rm "$second/docs"
    mkdir -p "$second/docs/fixtures/t30.7-neutral-service"
    printf 'different bundle bytes\n' >"$second/docs/fixtures/t30.7-neutral-service/t307-neutral-service.bundle"
  fi
  STAGING_TEST_BARRIER="$first/barrier" PATH="$work/bin:$PATH" \
    sh "$first/scripts/stage-receipt-fixtures.sh" >"$first/out" 2>"$first/err" &
  first_pid=$!
  children="$first_pid"
  STAGING_TEST_BARRIER="$second/barrier" PATH="$work/bin:$PATH" \
    sh "$second/scripts/stage-receipt-fixtures.sh" >"$second/out" 2>"$second/err" &
  second_pid=$!
  children="$children $second_pid"
  ready=0
  wait_ready "$first/barrier" "$second/barrier" || ready=1
  touch "$first/barrier.release"
  first_rc=0
  wait "$first_pid" || first_rc=$?
  children="$second_pid"
  touch "$second/barrier.release"
  second_rc=0
  wait "$second_pid" || second_rc=$?
  children=""
  expected_second=0
  [ "$race_kind" = identical ] || expected_second=1
  if [ "$ready" -eq 0 ] && [ "$first_rc" -eq 0 ] && [ "$second_rc" -eq "$expected_second" ] \
    && cmp -s "$repo_root/docs/fixtures/t30.7-neutral-service/t307-neutral-service.bundle" "$shared_root/t307-neutral-service.bundle" \
    && no_temp_leftovers "$shared_root"; then
    ok "r4: concurrent $race_kind publishers preserve the winner"
  else
    bad "r4: concurrent $race_kind publishers preserve the winner" "ready=$ready first=$first_rc second=$second_rc"
  fi
done

# A target directory or symlink appearing at the publication boundary must
# cause refusal, not ln's implicit creation of a file inside that directory.
for collision in directory symlink; do
  skel="$(make_skeleton "r4-collision-$collision")"
  fakeroot="$skel/fixture-root"
  STAGING_TEST_BARRIER="$skel/barrier" PATH="$work/bin:$PATH" \
    sh "$skel/scripts/stage-receipt-fixtures.sh" >"$skel/out" 2>"$skel/err" &
  collision_pid=$!
  children="$collision_pid"
  ready=0
  wait_ready "$skel/barrier" "$skel/barrier" || ready=1
  target="$fakeroot/t307-neutral-service.bundle"
  if [ "$collision" = symlink ]; then
    mkdir "$skel/untouched"
    ln -s "$skel/untouched" "$target"
    target="$skel/untouched"
  else
    mkdir "$target"
  fi
  touch "$skel/barrier.release"
  collision_rc=0
  wait "$collision_pid" || collision_rc=$?
  children=""
  if [ "$ready" -eq 0 ] && [ "$collision_rc" -eq 1 ] \
    && [ -z "$(ls -A "$target")" ] && no_temp_leftovers "$fakeroot"; then
    ok "r4: $collision publication collision refuses without traversal"
  else
    bad "r4: $collision publication collision refuses without traversal" "ready=$ready exit=$collision_rc"
  fi
done

# ---------------------------------------------------------------- summary
printf '%s\n' "----"
printf 'passed %d/%d; skipped %d\n' "$pass" "$n" "$skip"
if [ "$fail" -ne 0 ]; then
  printf '%d case(s) FAILED\n' "$fail" >&2
  exit 1
fi
exit 0
