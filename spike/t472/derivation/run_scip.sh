#!/usr/bin/env bash
# T47.2b §2 derivation runner.
#
# Builds one scip-go index per Go module of each frozen corpus repository at
# its pinned commit (spike/t472/corpus.lock.json). Serial by design: one
# module build runs at a time. Fail-closed: the first failed build aborts the
# sequence; nothing is skipped silently.
#
# Pinned recipe (spike/t457/native_tool_pins_amd64.json):
#   scip-go 0.2.7, amd64,
#   sha256 31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4
# Run environment: GOTOOLCHAIN=go1.27.1 (the only cached toolchain that
# satisfies every corpus module's go directive).
#
# Inputs:
#   T472_ROOT   rehearsal root with tool/bin/scip-go, modules.json, scip/
#   CORPUS_DIR  clone root (default: <repo>/spike/t472/corpus)
# Outputs (under $T472_ROOT/scip):
#   <repo>/<module>.scip   one per built module (root.scip for the root module)
#   <repo>/<module>.log    scip-go stdout/stderr
#   <repo>/<module>.time   GNU time wall/user/sys/maxrss
#   run_plan.tsv, skips.tsv, RUN_STATUS (append-only run ledger)
#   go_env.json   the effective Go environment of the runs (build tags, cgo,
#                 platform, proxy and cache locations change index bytes)
set -euo pipefail

T472_ROOT="${T472_ROOT:-/home/ben/phebs-rehearsals/t472-derivation}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
CORPUS_DIR="${CORPUS_DIR:-$REPO_ROOT/spike/t472/corpus}"
SCIPGO="$T472_ROOT/tool/bin/scip-go"
SCIPGO_SHA_PIN="31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4"
GO_TOOLCHAIN="go1.27.1"
MODULES_JSON="$T472_ROOT/modules.json"
OUT="$T472_ROOT/scip"
STATUS="$OUT/RUN_STATUS"

fail() { echo "ABORT: $*" | tee -a "$STATUS"; exit 1; }

mkdir -p "$OUT"
touch "$STATUS"

# --- preflight -------------------------------------------------------------
[ -x "$SCIPGO" ] || fail "missing scip-go at $SCIPGO"
sha="$(sha256sum "$SCIPGO" | cut -d' ' -f1)"
[ "$sha" = "$SCIPGO_SHA_PIN" ] || fail "scip-go sha256 $sha != pinned $SCIPGO_SHA_PIN"
[ -f "$MODULES_JSON" ] || fail "missing $MODULES_JSON"
env GOTOOLCHAIN="$GO_TOOLCHAIN" go env -json GOVERSION GOFLAGS CGO_ENABLED GOOS GOARCH \
  GOPROXY GONOSUMDB GOCACHE GOMODCACHE GOENV GOEXPERIMENT GOTOOLCHAIN \
  > "$OUT/go_env.json.tmp" || fail "cannot record go env"
if [ -f "$OUT/go_env.json" ]; then
  cmp -s "$OUT/go_env.json" "$OUT/go_env.json.tmp" || fail "Go environment changed before resume"
  rm "$OUT/go_env.json.tmp"
else
  mv "$OUT/go_env.json.tmp" "$OUT/go_env.json"
fi

# The recorded option-A disposition excludes the two refused repositories.
python3 - "$REPO_ROOT/spike/t472/corpus.lock.json" > "$OUT/corpus_plan.tsv" <<'PY'
import json, sys
lock = json.load(open(sys.argv[1]))
for repo in sorted(lock["repos"], key=lambda r: r["name"]):
    if repo["derived"]["corpus_admitted"]:
        print(repo["name"].split("/")[1], repo["commit"], "github.com/" + repo["name"], sep="\t")
PY
declare -A PIN=() REMOTE=()
ORDER=""
while IFS=$'\t' read -r repo pin remote; do
  PIN[$repo]="$pin"; REMOTE[$repo]="$remote"
  ORDER="${ORDER:+$ORDER }$repo"
done < "$OUT/corpus_plan.tsv"
[ -n "$ORDER" ] || fail "no admitted corpus repositories"

for repo in $ORDER; do
  clone="$CORPUS_DIR/$repo"
  [ -d "$clone/.git" ] || fail "missing clone $clone"
  head="$(git -C "$clone" rev-parse HEAD)"
  [ "$head" = "${PIN[$repo]}" ] || fail "$repo HEAD $head != ${PIN[$repo]}"
  dirty="$(git -C "$clone" status --porcelain)"
  [ -z "$dirty" ] || fail "$repo clone is not clean: $dirty"
done

# --- run plan --------------------------------------------------------------
python3 - "$MODULES_JSON" "$OUT/run_plan.tsv" "$OUT/skips.tsv" "$ORDER" <<'PY'
import json, sys
modules_json, plan_path, skips_path, order = sys.argv[1:5]
m = json.load(open(modules_json))
with open(plan_path, "w") as plan, open(skips_path, "w") as skips:
    for repo in order.split():
        for mo in m[repo]["modules"]:
            rel = mo["rel"]
            if mo["go_files"] == 0:
                skips.write(f"{repo}\t{rel}\tzero-Go module ({mo['files']} files)\n")
                continue
            # "." marks the root module: a literally empty field would be
            # collapsed by bash IFS tab splitting and shift all fields.
            plan.write(f"{repo}\t{rel or '.'}\t{mo['go_files']}\n")
PY

# --- serial runs -----------------------------------------------------------
declare -A SUB_OF=()
while IFS=$'\t' read -r repo rel gofiles; do
  clone="$CORPUS_DIR/$repo"
  if [ "$rel" = "." ]; then
    moddir="$clone"; sub="root"; rel=""
  else
    moddir="$clone/$rel"; sub="${rel//\//-}"
  fi
  # "a/b" and "a-b" would share one output name.
  [ -z "${SUB_OF[$repo/$sub]:-}" ] || fail "$repo modules '${SUB_OF[$repo/$sub]}' and '$rel' share output $sub"
  SUB_OF[$repo/$sub]="$rel"
  out="$OUT/$repo/$sub.scip"
  log="$OUT/$repo/$sub.log"
  timef="$OUT/$repo/$sub.time"
  mkdir -p "$OUT/$repo"
  envf="$OUT/$repo/$sub.go-env.json"
  ( cd "$moddir" && env GOTOOLCHAIN="$GO_TOOLCHAIN" go env -json \
      GOVERSION GOFLAGS CGO_ENABLED GOOS GOARCH GOPROXY GONOSUMDB GOCACHE GOMODCACHE \
      GOENV GOEXPERIMENT GOTOOLCHAIN GOMOD GOWORK ) > "$envf.tmp"
  if [ -f "$envf" ]; then
    cmp -s "$envf" "$envf.tmp" || fail "$repo $sub: Go environment changed before resume"
    rm "$envf.tmp"
  else
    mv "$envf.tmp" "$envf"
  fi
  recorded="$(awk -F'\t' -v r="$repo" -v s="$sub" \
    '$1 == "OK" && $2 == r && $3 == s { print $1 "\t" $6 }' "$STATUS" | tail -n 1)"
  if [ -n "$recorded" ]; then
    want="${recorded#*$'\t'}"
    [ -f "$out" ] && [ "$(sha256sum "$out" | cut -d' ' -f1)" = "$want" ] ||
      fail "$repo $sub: recorded output is missing or no longer matches $want"
    echo "skip (already recorded): $repo $sub"
    continue
  fi
  echo "=== run $repo $sub (rel='$rel', go_files=$gofiles) $(date -u +%FT%TZ)"
  rm -f "$out" "$log" "$timef"
  rc=0
  ( cd "$moddir" && exec /usr/bin/time -f 'wall_s=%e user_s=%U sys_s=%S maxrss_kb=%M' -o "$timef" \
      env GOTOOLCHAIN="$GO_TOOLCHAIN" "$SCIPGO" index \
        --module-root "$PWD" \
        --repository-remote "${REMOTE[$repo]}" \
        --module-version "${PIN[$repo]:0:12}" \
        --skip-implementations \
        --output "$out" ) > "$log" 2>&1 || rc=$?
  if [ $rc -ne 0 ]; then
    echo "FAIL $repo $sub rc=$rc" | tee -a "$STATUS"
    tail -n 20 "$log" >&2 || true
    exit 1
  fi
  bytes="$(stat -c %s "$out")"
  fsha="$(sha256sum "$out" | cut -d' ' -f1)"
  printf 'OK\t%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sub" "$rel" "$bytes" "$fsha" "$(tr '\n' ' ' < "$timef")" >> "$STATUS"
  echo "ok $repo $sub bytes=$bytes sha=${fsha:0:12} $(cat "$timef")"
done < "$OUT/run_plan.tsv"

echo "ALL RUNS COMPLETE"
