# Screenshot receipts

Deterministic Playwright screenshot receipts over the routed UI surfaces
(`routes.ts` is the manifest). The CI workflow boots a receipt instance with
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
  cat >>"$receipt_config" <<EOF
  connections:
    - name: t30-service-scope-demo
      type: git
      url: "$PHEBS_T307_NEUTRAL_SERVICE_REPO"
    - name: t34-service-search-demo
      type: git
      url: "$PHEBS_T344_SERVICE_SEARCH_REPO"
  service_catalogs:
    "local$PHEBS_T307_NEUTRAL_SERVICE_REPO":
      kind: operator
      id: t335-demo
      version: v1
      path: "$PHEBS_T335_SERVICE_CATALOG"
    "local$PHEBS_T344_SERVICE_SEARCH_REPO":
      kind: operator
      id: t344-demo
      version: v1
      path: "$PHEBS_T344_SERVICE_SEARCH_CATALOG"
  experimental:
    provisional_proto_extraction: true
    provisional_kafka_extraction: true
  EOF
  set -m
  make bin/zoekt-git-index bin/phebs-focused-index bin/buf ui
  PHEBS_ZOEKT_GIT_INDEX="$(pwd)/bin/zoekt-git-index" \
    PHEBS_FOCUSED_INDEX="$(pwd)/bin/phebs-focused-index" \
    PHEBS_BUF="$(pwd)/bin/buf" \
    PHEBS_T307_NEUTRAL_SERVICE_REPO= PHEBS_T335_SERVICE_CATALOG= \
    PHEBS_T344_SERVICE_SEARCH_REPO= PHEBS_T344_SERVICE_SEARCH_CATALOG= \
    PHEBS_CONTRACT_ATLAS_FIXTURE= PHEBS_THRIFT_FIELD_DEMO_REPO= \
    go run -tags ui ./cmd/phebs serve -config "$receipt_config" >"$receipt_run/phebs-dev.log" 2>&1 &
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

  The receipt launch explicitly selects whole-repository indexing for both
  bundles and their retained catalogs. It clears demo injectors so this
  presentation launch stays explicit and independent of demo environment binding.
  Strict real-cohort readiness remains mandatory. The staging script copies
  the bundles and operator catalogs into the fixed root; ordinary `make dev` / `make dev-api` honor the pre-set
  `PHEBS_T307_NEUTRAL_SERVICE_REPO` / `PHEBS_T344_SERVICE_SEARCH_REPO`
  paths and fall back to the checkout-relative defaults when unset.
  Production repository-identity semantics (`internal/sync` RepoName) are
  unchanged — only the receipt environment's input to that rule is fixed.
  The exported `PHEBS_T335_SERVICE_CATALOG` and
  `PHEBS_T344_SERVICE_SEARCH_CATALOG` paths also bind operator catalog
  `SourcePath` independently of the checkout. Production generation identity
  still includes that path; identical catalog bytes in different checkout
  paths must not be treated as the same source authority.

- **Staging safety contract.** The fixture root is a shared `/tmp` path,
  so the staging script validates before every write: the root must be a
  real directory owned by the current user, mode 0700 without an ACL
  (symlinks, other modes/ACLs, and foreign owners are refused).
  Existing roots are never chmodded automatically; inspect an old root and
  stop its receipt runs before changing its permissions. Each destination
  is refused if it is a symlink, a non-regular file, or not owned by the current user. A
  staged fixture whose bytes already match the source is reused untouched.
  A staged fixture whose bytes **differ** is refused, never truncated or
  overwritten in place — remove it by hand if restaging is intended. New
  fixtures publish through the POSIX `link` utility from a complete temp file
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
| Audit trail rows (the harness's own logins/searches mutate them) | Four page-scoped usage fixture rows; existing `mask: ['main tbody td > *']` keeps values masked and anatomy compared |
| Analytics page (usage-derived in whole) | Page-scoped usage fixture fixes body height; existing `mask: ['main']` still compares chrome only |
| Settings lifecycle values (disk pressure, capacity badge) | precise `[data-volatile="lifecycle"]` / status masks |
| Physical search generation in the service-authority drawer | Require the exact field and SHA-256 shape, then mask only its value; label, geometry, revision and all other authority values remain compared |
| Indexing transitions ("Indexing…", "Cloning") | `awaitAbsent` terminal-state guards; `Failed` is terminal and fails the capture |
| Fixture content drift | the t307 bundle pin (`T307_COMMIT`) binds the content; operator catalogs stage at the same fixed root, preserving their generation's `SourcePath` binding across checkouts |
| Markdown-preview surface | page-scoped synthetic fixture (`receipt-fixture/markdown-preview`), never instance state |

## T45.8b managed-index workflow fixtures

The `typed-index` project exercises the administrator workflow through explicit
page-scoped API fixtures; it installs no provider and proves no native execution
or publication. It covers exact request preview/enqueue/retry, all nine states,
last-good active-status polling, ordinary-user denial and File-to-Settings links.
Desktop/390px, light/dark and comfortable/dense receipts exercise keyboard focus,
native labels, reduced motion, document overflow and console errors. Capture fits
the complete indexing panel below sticky chrome without masking its anatomy.

Run `npm run receipts -- --project typed-index` against an already running Vite
or product UI. Canonical pixels use the same digest-pinned Noble/Chromium image
as CI. Local macOS captures belong in temporary output, never in `baselines/`.
Synthetic profile availability is rendering evidence only; ordinary production
serve remains unavailable. New typed transport reads cap streamed bodies at 32 KiB.
The existing repository-list fetch is parsed before its 4,096-row admission check;
only active selected status repeats at five seconds. Fixture response timings
measure a local presentation path and are not production API or native-work SLOs.

### T45.10a two-profile presentation checks

The same page-scoped fixture now supplies either exact admitted resource profile:
`native-arm64-bounded-v1` or `native-amd64-bounded-v1`. Each profile exercises
exact preview and CSRF-bound enqueue/retry requests, all nine states and
last-confirmed polling. Four refusal cases cover an unknown or empty profile
on an available status or preview. An empty repository inventory makes no
managed status request and offers no review or generation action; an unavailable
status with empty profile fields remains readable. These are presentation
controls, with no compiler, installed runtime or generated-navigation inference.

The project has 27 cases: the eight existing ARM64 canonical screenshot cases,
eight native QA preview captures (two profiles × two themes × desktop/390px,
comfortable density), two exact interaction/state flows, four malformed-profile
refusals, one empty inventory and four existing workflow/access controls. The
eight historical `typed-index-preview--…` names and baseline PNGs are unchanged.
Native QA captures are attachments named
`typed-index-native--<profile>--<theme>--<width>.png` under the test output path;
they add no canonical baseline expectations or baseline-count requirement.

For a separate native lineage, use an output directory outside the checkout:

```sh
npm run receipts -- --project typed-index --grep 'native profile preview' --output /tmp/phebs-typed-index-native-author
```

Author and fresh captures must use the same reviewed source, renderer, viewport
and preferences, with independently retained source/tool hashes and the existing
zero-retry, 0.1% difference limit. The capture tests themselves emit images; they
do not assert that a later comparison or accessibility audit passed. Native
images do not establish Noble/Chromium baseline equivalence and must not be
copied into `baselines/`. Browser, accessibility, comparison and measured timing
outcomes remain pending until the bounded run is recorded. Browser plugin not
available in the source-preparation session; the existing Playwright dependency
is used without additional browser or accessibility installations.

`fixtureReadyMillis` covers navigation through the keyboard-ready Review action.
`fixturePreviewMillis` stops immediately after the exact preview is visible and
Generate is enabled, before font readiness, geometry checks or capture. It is a
local fixture interaction measurement, not API latency or native generation time.
The source change adds no production request, poll, lock, cache, sync/startup,
publication, compiler or provider work. The existing one-worker, zero-retry
receipt bounds apply; only the two active-state flows intentionally wait for the
existing five-second polling intervals.


T45.8b canonical evidence is retained in `t458b.json`: eight preview fixture
pixels and eight ordinary Settings pixels were authored and compared with the
pinned image. The complete panel is scrolled below sticky chrome, then its
position is checked; no indexing anatomy is masked. The ordinary instance uses
the make-dev-built binary with an explicit whole-repository configuration for
both unchanged staged neutral bundles, their retained operator catalogs, and
provisional protobuf/Kafka packs. It has no analysis units or typed runtime.
The unchanged strict setup checks both exact HEADs, settled jobs, current
required services and complete orders-api relationship authority before capture.
The configuration digest and bundle/chunk metrics are in the evidence record.

The historical default focused T30.7 configuration separately indexed but lacked its v2
source-generation control, leaving service/relationship authority unavailable.
That diagnostic is not a receipt PASS or focused-pipeline closure. T45.8c changes
`make dev`/`make dev-api` to explicit whole mode for both retained bundles;
`PHEBS_DEV_COHORT=focused` keeps this separate unavailable focused pipeline.
The retained `t458b.json` limitation and source bytes remain unchanged. The ordinary
neutral data uses a disposable 1 GiB APFS volume with unchanged disk admission
limits; it proves this small cohort only. Chromium runs in the untouched pinned
AMD64 image under Rosetta in a temporary 2-CPU/6-GiB renderer VM. A TCP bridge to
trustworthy loopback changes no browser flags or product behavior. Hosted Actions
is account-disabled; these are local pinned-renderer checks, not hosted CI.

The Settings chunk is 27,095 bytes (8.04 kB reported gzip); no dependency was
added. Fixture readiness includes keyboard traversal. Fixture preview timing
includes interaction and screenshot bookkeeping; neither timing is a production
request, indexing or user-validation claim. Existing full command checks also
retain three phase-nine lifecycle deadline failures, distinct from the passing
affected typed/restore checks; the receipt record never labels them green.


The unchanged short workspace composition subsequently passes in 21.858s, and
full `cmd/phebs` passes in 946.515s with test temporary roots on the bounded
1 GiB APFS volume. This closes that package gate for the stated filesystem;
it preserves the earlier failed host-volume runs and does not invent their
exact cause. Source, admission policy, fixture budgets and deadlines are
unchanged. The volume is detached only after all test children join.

T45.8b review corrections add a consumed-request refusal fixture and verify that
a transient active poll continues to a terminal response. Preview pixels retain
their original provenance; a fresh corrected-source comparison checks them without
reauthoring. Exact transport retry remains separate from starting another run.
The corrected Settings chunk is 28,072 bytes (8.33 kB reported gzip). All 746
UI tests pass with the executable preview matrix included in manifest cardinality;
13 fresh pinned Chromium checks pass, comparing eight original preview pixels
unchanged. Original ordinary-cohort and native receipts retain their old source.

T57R.2 waits up to the existing 30-second readiness budget for required lazy
presentation markers. The Settings regression holds the real highlight chunk
for eight seconds; the ordinary five-second assertion budget fails this case.
Missing readiness still fails capture; thresholds and retries are unchanged.
Browser plugin not available; these checks use the repository's Playwright flow.

The independent fresh-instance comparison also exposed the physical search
publication's generation digest in eight drawer images. Its precise value-only
mask follows a required-selector and SHA-256 assertion. All previous masks stay
intact. Initial application-main readiness uses the same 30-second budget;
an eight-second identity-response delay tests the pre-main loading boundary.

DOM quiet still requires three equal rendered samples at 250 ms, bounded by
30 seconds. The comparison now runs inside the browser instead of sending full
HTML over the protocol every sample. A changing-markup test pins the quiet-state
predicate; the earlier browser-read timeout and traced non-reproduction remain
part of the receipt record, without an invented host-cause attribution.

The subsequent persistent-browser trace captured a JavaScript HTTP 500 with
the server's session-save transaction conflict. Under Ben's explicit backend
Mac handoff, public UI pages/assets now bypass session persistence; auth, API,
MCP and metrics retain session handling and CSRF protections. This diagnoses
the captured failure without assigning every earlier timeout the same cause.
The repaired source passes the complete fresh-data matrix: 135/135, zero
skips/retries/flaky results, unchanged 0.1% threshold. The 106 reviewed changed
PNGs include 90 stale baselines (eight also receive the precise generation-value
mask), eight usage-fixture repairs and eight relationship-validator repairs;
20 controls remain byte-identical. [T57R.2 provenance](./t57r2.json) records
per-route classification, hashes, regression and interaction evidence. These
are local pinned-renderer checks, not hosted Actions or focused-pipeline closure.
Forty subsequent captures in one persistent pinned browser pass with no
console/page/request errors. Eight fresh desktop/mobile palette and keyboard
relationship/citation journeys pass without overflow or errors.

T44.5's header refresh uses a shared capture-only scroll helper that measures
its rendered height and requires Settings targets to clear it before capture.
The final explicit full author (`--update-snapshots=all`) and independent
comparison over another fresh data directory each pass 135/135. The one
reviewed PNG commit changes 121 images; both login controls and three desktop
panel images remain byte-identical. Existing masks, zero retries and 0.1%
tolerance are unchanged. [T44.5 provenance](./t445.json) records both source
commits, all before/after hashes, scoped accessibility/keyboard checks,
bundle measurements, generation-value and minor raster differences, and
retained failed/interrupted diagnostics. These remain local pinned-renderer
checks and do not close the separate focused pipeline or validation labels.

Integration review adds eight non-screenshot sidebar scroll cases at
721/800/960/961px, bringing the future full matrix to 143 tests. They first
reproduce the tablet overlap, then pass with the shared narrow-header geometry;
four native mobile checks preserve the existing static layout. No PNG changes.
The recorded 135/135 canonical matrices remain bound to their original source,
and the new cases are native functional evidence. Cross-checkout catalog-path
portability remains a separate inherited receipt-environment follow-up.

T57R.3 closes the portability follow-up by staging both operator catalogs beside the two
bundles and selects their exported fixed paths in local and CI receipt boots.
The production `SourcePath` generation binding stays intact. Native captures
pass 18/18 across the ticket worktree and a disposable second fixture root;
canonical authoring passes 9/9 and fresh second-root comparison passes 33/33.
Both real catalog authorities match exactly across all four fresh instances.
The eight reviewed authority images update Catalog generation text; all other
118 PNGs remain byte-identical. Existing masks, strict cohort checks, zero
retries and 0.1% tolerance are unchanged. The bounded comparison covers the
service search/directory and relationship routes; the complete 143-case
matrix was not rerun. [Portability provenance](./t57r3.json)
records hashes, local pinned-renderer qualification, retained diagnostics,
cost and teardown. No hosted Actions or production release claim follows.


### T44.2 complete Appearance frames

The `appearance` project supplies a strict page-scoped, non-administrator
reader fixture. It captures the entire Appearance heading, palette select and
code specimen at 1280×800 and 390×900, in both themes and densities. The two
keyboard cases choose each palette with native type-ahead after Tab refocus, check live token
colors without a navigation or API request, reload the saved preference, and
refuse unknown and inherited-property names. No backend or release authority is
exercised.

```sh
npm run receipts -- --project appearance --max-failures=1 --output /tmp/phebs-appearance-author
npm run receipts -- --project appearance --grep 'native Appearance frame' --max-failures=1 --output /tmp/phebs-appearance-fresh
```

The ten-case author and eight-case fresh passes emit captures; a separate
comparison must use the same source, renderer and tools, one worker, zero
retries and the existing 0.001 ratio. The eight
`palette-appearance--<theme>--<width>--<density>.png` files form a separate
native lineage. They add no canonical baseline expectation and never belong in
`baselines/`. The older desktop Settings frame omits Appearance and the narrow
indexing frame clips its specimen; their historical pixels remain exact.
The bounded outcome, comparison, accessibility and cost record is
[T44.2](./t442.json). A local presentation fixture establishes no production
API latency, Linux execution, signing or release result.
