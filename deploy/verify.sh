#!/bin/bash
# Check that the phebs instance in the cluster works: health, login, version
# and evidence-pack capabilities, per-repository index status, and optionally
# wait for one repository to finish indexing and run a search. Uses
# port-forward by default, so it does not depend on the NodePort.
#
# Usage: ./verify.sh
# Optional environment variables:
#   NS             namespace, default phebs
#   PORT           local forwarded port, default 13070
#   ADMIN_EMAIL    admin email, default admin@example.com
#   ADMIN_PASSWORD admin password; when unset only /api/health is checked
#                  (all post-login checks are skipped)
#   WAIT_REPO      wait for this repository name to finish indexing
#                  (not waited for by default)
#   WAIT_SECONDS   how long to wait at most, default 300
#   Q              additionally run one search and print match counts
set -euo pipefail

NS="${NS:-phebs}"
PORT="${PORT:-13070}"
BASE="http://127.0.0.1:${PORT}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
EXPECT_VERSION="${EXPECT_VERSION:-v0.3.5}"
WAIT_REPO="${WAIT_REPO:-}"
WAIT_SECONDS="${WAIT_SECONDS:-300}"
Q="${Q:-}"

step() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

command -v kubectl >/dev/null || fail "kubectl is required"
command -v jq >/dev/null || fail "jq is required"

# The admin password comes from the environment (kept in a variable only —
# never printed and never visible in process arguments).
password="${ADMIN_PASSWORD:-}"

kubectl -n "$NS" port-forward svc/phebs "${PORT}:80" >/dev/null 2>&1 &
forward=$!
jar=$(mktemp)
trap 'kill "$forward" 2>/dev/null || true; rm -f "$jar"' EXIT

deadline=$(( $(date +%s) + 60 ))
until curl -fsS -m 5 "${BASE}/api/health" >/dev/null 2>&1; do
  [ "$(date +%s)" -lt "$deadline" ] || fail "/api/health did not become ready within 60 seconds"
  sleep 2
done
step "health: ok"

if [ -z "$password" ]; then
  step "ADMIN_PASSWORD not set, skipping login-dependent checks (login/version/index status/search)"
  step "PASS"
  exit 0
fi

jq -n --arg email "$ADMIN_EMAIL" --arg password "$password" '{email: $email, password: $password}' \
  | curl -fsS -m 60 -c "$jar" -o /dev/null -H 'Content-Type: application/json' --data-binary @- \
      "${BASE}/api/auth/login" || fail "login failed"
step "login: ok (${ADMIN_EMAIL})"
# With cookie_secure false (plaintext HTTP access) the cookie jar works as-is.
api() { curl -fsS -m 60 -b "$jar" "$@"; }

version_json=$(api "${BASE}/api/version")
echo "$version_json" | jq -e --arg v "$EXPECT_VERSION" '.version == $v' >/dev/null \
  || fail "version is not ${EXPECT_VERSION}: ${version_json}"
step "version: $(echo "$version_json" | jq -r '.version'); evidence-pack capabilities: $(echo "$version_json" | jq '.capabilities | length') items: $(echo "$version_json" | jq -r '.capabilities | join(", ")')"

repo_state() {
  api "${BASE}/api/repo-status" | jq -r --arg r "$1" '
    [.[] | select(.name == $r)]
    | if length == 0 then "absent"
      else .[0] | "\(.indexed_commit_hash // "-") \(.latest_indexing_job_status // "-") \(.last_index_job.status // "-")" end'
}

if [ -n "$WAIT_REPO" ]; then
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while :; do
    s=$(repo_state "$WAIT_REPO" || true)
    commit=${s%% *}
    rest=${s#* }
    if [ "$commit" != "-" ] && [ "$commit" != "absent" ] && [ "$rest" = "done done" ]; then break; fi
    case "$s" in *" failed") fail "repository ${WAIT_REPO} failed to index";; esac
    [ "$(date +%s)" -lt "$deadline" ] || fail "timed out waiting for ${WAIT_REPO} to index; last state: ${s:-repository not seen yet}"
    sleep 5
  done
  step "index: ${WAIT_REPO} indexed at ${commit:0:12}"
fi

step "repositories:"
api "${BASE}/api/repo-status" | jq -r '
  .[] | "  \(.name)  indexed=\((.indexed_commit_hash // "")[0:12])  status=\(.latest_indexing_job_status // "-")  job=\(.last_index_job.status // "-")  error=\((.last_index_job.error // "") | tostring | .[0:160])"'

if [ -n "$Q" ]; then
  search=$(api -G "${BASE}/api/search" --data-urlencode "q=${Q}" --data-urlencode "max_matches=10")
  step "search \"${Q}\": $(echo "$search" | jq '.files | length') matching files, match_count=$(echo "$search" | jq '.stats.match_count')"
  echo "$search" | jq -r '.files[] | "  \(.repo)  \(.path)"' | head -10
fi

step "PASS"
