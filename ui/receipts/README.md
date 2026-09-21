# Screenshot receipts

Deterministic Playwright screenshot receipts over the routed UI surfaces
(`routes.ts` is the manifest). The CI workflow boots a dev instance with
the neutral-demo fixtures, then `make ui-receipts` compares every
theme × density × route against the committed PNGs in `baselines/`.

## The deterministic receipt environment

Repository identities reach the pixels **unmasked** (repo lists, file
headers, drawers), so the identities themselves must be identical on every
machine — otherwise macOS and Ubuntu CI can never share one baseline set.

- The fixed fixture root is `RECEIPT_FIXTURE_ROOT` in
  [`fixtureRoot.ts`](./fixtureRoot.ts): `/tmp/phebs-receipts-fixtures`.
  It is the single source of truth; `scripts/stage-receipt-fixtures.sh`
  extracts it from that file and `routes.ts` derives every neutral-demo
  repository identity from it with the server's `local/` RepoName rule.
- **Boot the receipt instance like this** (from the repo root):

  ```bash
  required_surreal="$(tr -d '[:space:]' < .surrealdb-version)"
  command -v surreal >/dev/null || exit 1
  test "$(surreal version | awk '{print $1}')" = "$required_surreal" || exit 1

  export PHEBS_RECEIPT_EMAIL=receipts@localhost.test
  export PHEBS_RECEIPT_PASSWORD="$(openssl rand -hex 16)"
  umask 077
  receipt_run="$(mktemp -d "${TMPDIR:-/tmp}/phebs-receipts.XXXXXX")"
  receipt_config="$receipt_run/phebs-receipts.yaml"
  cat >"$receipt_config" <<EOF
  server:
    addr: "127.0.0.1:3073"
    data_dir: "$receipt_run/data"
  auth:
    bootstrap_user:
      email: "$PHEBS_RECEIPT_EMAIL"
      display_name: Receipt Operator
      password: "$PHEBS_RECEIPT_PASSWORD"
  EOF
  receipt_env="$(sh scripts/stage-receipt-fixtures.sh --env)" || exit 1
  eval "$receipt_env"
  set -m
  make dev ARGS="-config $receipt_config" >"$receipt_run/phebs-dev.log" 2>&1 &
  receipt_server_pid=$!
  cleanup_receipt_server() {
    kill -TERM -- "-$receipt_server_pid" 2>/dev/null || true
    wait "$receipt_server_pid" 2>/dev/null || true
  }
  trap cleanup_receipt_server EXIT
  ready_deadline=$((SECONDS + 600))
  until curl -fsS --connect-timeout 2 --max-time 5 http://127.0.0.1:3073/api/version >/dev/null; do
    kill -0 "$receipt_server_pid" 2>/dev/null || { wait "$receipt_server_pid"; exit 1; }
    (( SECONDS < ready_deadline )) || exit 1
    sleep 5
  done
  ```

  Capture the staging output and check it **before** `eval`: a failed
  staging run must fail the boot here, not start `make dev` on an empty
  environment (eval of a failed command substitution's empty output
  succeeds silently). Invoking via `sh` keeps the boot independent of the
  script's executable bit.

  Run `make ui-receipts` (or the explicit owner-only update command below)
  in this same shell; it retains the fresh instance's two exported operator
  credentials. Exiting the shell stops the server. The `surreal` binary on
  `PATH` must match `.surrealdb-version`.

  The staging script copies the neutral-demo bundles into the fixed root;
  `make dev` / `make dev-api` honor the pre-set
  `PHEBS_T307_NEUTRAL_SERVICE_REPO` / `PHEBS_T344_SERVICE_SEARCH_REPO`
  paths and fall back to the checkout-relative defaults when unset.
  Production repository-identity semantics (`internal/sync` RepoName) are
  unchanged — only the receipt environment's input to that rule is fixed.

- **Staging safety contract.** The fixture root is a shared `/tmp` path,
  so the staging script validates before every write: the root must be a
  real directory owned by the current user, mode 0700 without an ACL
  (symlinks, other modes/ACLs, and foreign owners are refused).
  Existing roots are never chmodded automatically; inspect an old root and
  stop its receipt runs before changing its permissions. Each destination
  is refused if it is a symlink, a non-regular file, or not owned by the current user. A
  staged bundle whose bytes already match the source is reused untouched.
  A staged bundle whose bytes **differ** is refused, never truncated or
  overwritten in place — remove it by hand if restaging is intended. New
  bundles publish through the POSIX `link` utility from a complete temp file
  in that directory: publication never replaces an existing name or follows
  a destination symlink. A simultaneous publisher revalidates and reuses an
  identical winner, or refuses differing bytes. No lock or stale-lock recovery
  is needed, and concurrent readers never see a partial copy. The regression
  tests (`sh scripts/test-stage-receipt-fixtures.sh`, also run in CI's
  static job) cover all of these cases against disposable fake roots.

- The resulting identities are
  `local/tmp/phebs-receipts-fixtures/t307-neutral-service.bundle` and
  `local/tmp/phebs-receipts-fixtures/t323-neutral-corpus.bundle` on every
  machine, regardless of checkout location.
- Before capture, the authenticated setup waits for that exact two-repository
  cohort, settled index and extraction jobs, the required current services,
  and complete `orders-api` relationship authority. Empty, partial, failed,
  canceled, or unexpected state cannot become a baseline.

## Baselines

- `baselines/` holds the reviewed truth. They are **produced in the pinned
  CI rendering environment** — Playwright 1.62.1's Noble container at the
  digest recorded in `.github/workflows/ci.yml`, using its package-matched
  bundled Chromium. A single baseline set serves all platforms; macOS and Linux are
  **not** compared against separate image sets. (Font rasterization differs
  across platforms, so a local macOS `make ui-receipts` run is expected to
  report diffs; the canonical gate is the CI run.)
- If macOS and Linux baselines are ever both retained, they must be
  selected explicitly per platform (e.g. via `snapshotPathTemplate`),
  never compared cross-platform against one image set.
- Zero retries (`retries: 0`) and the 0.1% pixel-diff threshold
  (`maxDiffPixelRatio: 0.001`) are load-bearing: a failure is a signal,
  never retried away.

### Regenerating baselines (owner step)

Regeneration is always explicit and always re-reviewed — never automatic.

1. In the digest-pinned receipt CI environment, boot the receipt instance
   exactly as above with a **fresh data dir**, then:

   ```bash
   cd ui && npm run receipts:update   # or: make ui-receipts-update
   ```

2. Review the diff **before committing**:
   - `git status` must show only `ui/receipts/baselines/*.png` changes —
     no code, no fixture, no config changes riding along.
   - Spot-check that repository names render as
     `local/tmp/phebs-receipts-fixtures/...` (never a checkout path) and
     that the operator chip still reads the pinned neutral identity
     (`UX audit`), not a personal or bootstrap address.
   - Confirm masked regions (audit table cells, analytics body, settings
     lifecycle values) did not gain or lose coverage.
3. Commit the PNGs with a message naming the rendering environment
   (e.g. `test(receipts): refresh baselines on pinned Playwright Chromium`).

## What else is instance-generation-dependent

Every displayed value that a fresh instance generation could change is
pinned or masked — the fixture root is only the repository-identity piece:

| Value | How it is pinned |
|---|---|
| Operator email/display name in the header | `installIdentityFixture` pins the neutral `UX audit` / `ux-audit@localhost.test` identity on every captured page (`fixture.ts`) |
| Relative-time copy ("indexed … ago") | `page.clock.setFixedTime(FROZEN_NOW)` per capture (`routes.ts`, `receipts.spec.ts`) |
| Audit trail rows (the harness's own logins/searches mutate them) | `mask: ['main tbody td > *']` — anatomy stays compared, dynamic cells masked |
| Analytics page (usage-derived in whole) | `mask: ['main']` — chrome only |
| Settings lifecycle values (disk pressure, capacity badge) | precise `[data-volatile="lifecycle"]` / status masks |
| Indexing transitions ("Indexing…", "Cloning") | `awaitAbsent` terminal-state guards; `Failed` is terminal and fails the capture |
| Fixture content drift | the t307 bundle pin (`T307_COMMIT`) binds the content |
| Markdown-preview surface | page-scoped synthetic fixture (`receipt-fixture/markdown-preview`), never instance state |
