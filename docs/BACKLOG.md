# phebs · P1 backlog

Epics mirror PORT_MAP.md §10. Tickets are PR-sized and dependency-ordered for a
stacked workflow. Acceptance criteria (AC) are the merge bar. Decisions get
appended to PLAN.md as dated ADR bullets — no new design docs.

Conventions: `T<epic>.<n>` · deps listed only where they cross epics or gate.

---

## EPIC 0 — Bootstrap

**T0.1 · Repo home + module path** ✅ 2026-07-09 — *decision ticket; remote push deferred (local-only for now)*
Pick GitHub home (`phebs` handle is squatted; options: under your user, or org
`phebs-dev` / `getphebs`). AC: repo exists, README.md skeleton pushed, module
path fixed in PLAN.md ADR.

**T0.2 · License** ✅ 2026-07-09 — *decision ticket*
Recommend Apache-2.0 (patent grant, matches zoekt ecosystem); MIT acceptable.
AC: LICENSE + copyright line committed; README license section unblocked.

**T0.3 · Scaffold** ✅ 2026-07-09
`go.mod` (latest stable Go, 1.26 line), layout: `cmd/phebs/`,
`internal/{config,store,sync,indexer,search,api}`, `ui/` (Vite app),
`go:embed` stub for UI. AC: `go build ./...` green on empty skeleton.

**T0.4 · CI** ✅ 2026-07-09 *(workflow committed; first real run needs the remote)*
golangci-lint + `go test ./...` + UI build job. AC: PR gate green on main.

**T0.5 · Dev environment** ✅ 2026-07-09
Makefile/justfile targets (`dev`, `test`, `lint`). Embedded SurrealDB
(`surrealkv://`) is the dev default per PLAN §1; optional compose file exists
only for server-mode testing. AC: `make dev` boots the phebs skeleton on
embedded storage with zero external services.

## EPIC 1 — Skeleton & storage (slice 1) ✅ 2026-07-09 — demoed via `make dev`

**T1.1 · Config schema + loader** ✅ 2026-07-09
Own YAML schema (NOT upstream's JSON schemas): server block, connections list
(github, generic-git), index dir, auth token. AC: invalid config fails fast
with line-level errors; example config in `docs/`.

**T1.2 · SurrealDB schema + store layer** ✅ 2026-07-09 *(supervised-child pivot — see PLAN ADR)*
`DEFINE TABLE` .surql applied at boot for §5 tables: `repo`, `connection`,
`repo_connection`, `connection_sync_job`, `indexing_job`, `user`, `api_key`.
AC: idempotent apply; store package with typed CRUD for repo + jobs behind an
interface (keeps the Postgres exit open per PLAN §3); unit tests against
ephemeral embedded (`surrealkv://`) stores.

**T1.3 · SPIKE: job-claim semantics** ✅ 2026-07-09 *(optimistic claim won — see PLAN ADR)* — *gates EPIC 2*
Benchmark optimistic `UPDATE … WHERE status = 'pending'` claim vs
record-lock pattern under N concurrent pollers; measure double-claim rate and
latency. AC: decision ADR in PLAN.md + chosen claim statement landed in store
layer with a concurrency test proving zero double-claims.

**T1.4 · huma skeleton** ✅ 2026-07-09
`/api/health`, `/api/version`, `/api/openapi.json`, `GET /api/repos` (read
from store). Bearer-token middleware (single API key). AC: OpenAPI doc
renders; endpoints covered by httptest.

## EPIC 2 — Sync (slice 2) ✅ 2026-07-09 — demoed: live sync of a public repo via `make dev`

**T2.1 · Generic-git adapter** ✅ 2026-07-09
Connection → repo rows; clone/fetch **bare** repos via exec git into
deterministic disk layout (`$DATA/repos/<host>/<path>.git`). AC: add a
connection, repos appear in DB and on disk; refetch is incremental.

**T2.2 · GitHub adapter (PAT)** ✅ 2026-07-09 *(fake-API tests; live PAT run done in T6.3)*
List repos by org/user with include/exclude filters (name, archived, fork).
AC: rate-limit aware pagination; repo metadata (defaultBranch, pushedAt,
webUrl, external_*) persisted per §5.

**T2.3 · Jittered poller + sync-job lifecycle** ✅ 2026-07-09
Poll loop with jitter per PLAN.md; job states pending→claimed→running→
done/failed; heartbeat + stale-claim reaper; bounded retries with backoff.
Depends: T1.3. AC: kill -9 during a job → job recovered by reaper; no
double-execution under 3 concurrent pollers (test).

**T2.4 · Repo status + orphan policy** ✅ 2026-07-09

**T2.5 · Local repos + watch mode** ✅ 2026-07-09 — *added post-P1*
Plain local paths as generic-git URLs (hardlink mirrors); `watch: true` polls
the source HEAD and re-syncs/re-indexes on movement; mirror follows the
source's checked-out branch; `sync.poll_interval` tunes end-to-end latency.
AC: commit in a watched repo becomes searchable within seconds (measured
~1.4s at 1s cadence); watcher enqueues exactly one deduped job per HEAD move.

**TD.1 · User manual + README refresh** ✅ 2026-07-10 — *added post-P1*
`docs/MANUAL.md`: install, config reference, connections (GitHub/git/local/
watch), search syntax, UI, API, operations, troubleshooting, development.
README brought to P1 reality (quick start, status, Apache-2.0). Doc rule
added to CLAUDE.md: behavior changes update the manual in the same PR.
Drive-by fixes from verifying the quickstart: `make build` binary now finds
`bin/zoekt-git-index`; UI page title.
`GET /api/repo-status`; orphaned repos (no connection) flagged; cleanup behind
config flag (default off, mirroring upstream's isAutoCleanupDisabled
semantics). AC: status endpoint reflects sync/index state transitions live.

## EPIC 3 — Index (slice 3) ✅ 2026-07-09 — demoed: sync→index chain to shards via `make dev`

**T3.1 · Indexer (same-SHA child builder)** ✅ 2026-07-09 *(>1GB memory soak deferred to P6 capacity spike)*
`indexing_job` consumer → child `zoekt-git-index` **built from our own go.mod
SHA** (`go build github.com/sourcegraph/zoekt/cmd/zoekt-git-index`), OOM-isolated
per PLAN §1; HEAD-only; shards to `$DATA/index`. Search stays in-process. AC: P0 spike behavior reproduced inside the job system; shard
appears after sync completes; memory bounded on a large repo (pick one >1GB).

**T3.2 · Incremental short-circuit** ✅ 2026-07-09
Skip when `indexedCommitHash == HEAD`; `--force` path for manual reindex.
AC: no-op reindex completes <100ms without touching shards.

**T3.3 · Failure taxonomy + metrics** ✅ 2026-07-09
Classified errors (clone-auth, index-oom, corrupt-shard), backoff per class;
Prometheus counters (jobs by state, index duration, shard bytes). AC: /metrics
exposes counters; forced failure lands in the right class with retry schedule.

## EPIC 4 — Search (slice 4) ✅ 2026-07-09 — demoed: search/stream/source/tree live via `make dev`

**T4.1 · Query pipeline** ✅ 2026-07-09 *(visibility via zoekt's `public:` atom; `archived:`/`fork:`/`public:` → RepoSet)*
`zoekt/query.Parse` + metadata pre-pass: `archived:` `fork:` `visibility:`
compiled to `query.RepoSet` from DB. AC: table-driven tests mapping input
strings → expected zoekt query trees.

**T4.2 · `/api/search` (JSON)** ✅ 2026-07-09
`shards.NewDirectorySearcher` singleton with shard watch; bounded result
options (maxMatches, context lines). AC: golden-file tests over a fixture
repo; p50 latency budget recorded in PLAN.md.

**T4.3 · `/api/stream_search` (SSE)** ✅ 2026-07-09 *(flush cadence: per shard batch — documented in search.Stream)*
Streamed via `Searcher.StreamSearch`; decide + document flush cadence
inline (per-chunk vs time-batched). AC: curl shows progressive events;
cancellation propagates (client disconnect stops the search).

**T4.4 · File serving: `/api/source`, `/api/tree`, `/api/folder_contents`** ✅ 2026-07-09
Serve from bare repos via git plumbing (`cat-file`, `ls-tree`) rather than
zoekt content tricks. AC: content matches checkout byte-for-byte; path
traversal fuzz test.

## EPIC 5 — UI (slice 5) ✅ 2026-07-09 — demoed in-browser: search/viewer/repos on Base Web via `make dev`

**T5.5 · UI redesign (Base Web design handoff)** ✅ 2026-07-10 — *added post-P1*
Applied the Uber design-language handoff on stock `baseui` (LightTheme/DarkTheme).
- **a** — theme foundation: dark mode + toggle (localStorage), mono/sans fonts,
  role→hex token map for non-semantic colors, redesigned 56px header shell.
- **b** — syntax highlighting: CM6 `HighlightStyle` in the file viewer + a Lezer
  standalone tokenizer for search-result chunks; styled match highlight;
  redesigned file cards; blue deep-link line.
- **c** — search workbench: facet rail (repos/languages, client-derived),
  keyboard nav (j/k/enter/y/o), collapsible groups, streaming indicator bar +
  skeleton + Stop.
- **d** — file viewer (breadcrumb, sticky metadata header, commit pill,
  permalink/open-in-search) + repos table (status dots, connection pills,
  commit chips, per-row search/reindex, Reindex all).
Deferred (need API additions, flagged in handoff): did-you-mean & speculative
looseners (2b–2d), per-shard progress / stuck-search (3c/3d), file-tree column,
context expander, auto-retry on stream drop.

**T5.1 · Vite + React + TS scaffold, embedded** ✅ 2026-07-09 *(build-tag embed: no committed artifacts, `go build` green without npm)*
`go:embed` production build; dev proxy to huma. AC: single binary serves UI.

**T5.2 · Search page** ✅ 2026-07-09 *(Base Web — public open-source `baseui`, no internal assets)*
Query box + SSE-driven result list (repo/file grouping, match counts).
AC: streaming renders incrementally; empty/error states.

**T5.3 · File viewer** ✅ 2026-07-09
CodeMirror 6 read-only, match decorations, line anchors (`#L42`), language
by extension. AC: deep link to file+line from a search result works.

**T5.4 · Repos page** ✅ 2026-07-09
Table over `/api/repo-status`: sync/index state, timestamps, force-reindex
button. AC: reindex button enqueues job visible in status within one poll.

---

## P1 cut line

Everything above ships = P1 (complete 2026-07-10). The post-P1 roadmap below
closes the Sourcebot free/paid feature gaps (derived 2026-07-10 from a
public-sources feature comparison + PORT_MAP §12). Waves are ordered by
value-over-effort and dependency; tickets are PR-sized, ACs are the merge bar.
Sourcebot tier in each ticket = where that feature sits upstream (free vs
paid/EE), from public docs/pricing only — never their source, never `ee/`.

---

## EPIC 6 — Parity quick wins ✅ 2026-07-10 *(Wave 0 — days each, no architecture change)*

**T6.1 · Broaden syntax highlighting** ✅ 2026-07-10 — *Sourcebot free (100+ langs)*
Added CM6 language packs beyond the initial ~6: official Lezer grammars
(Rust, Java, C/C++, PHP, SQL, HTML, CSS, XML, YAML) + legacy stream modes
(Ruby, shell, C#, Kotlin, Scala, Swift, Lua, Perl, Dart, TOML, Dockerfile) in
`ui/src/lang.ts`, each a lazy code-split chunk. Shared `langName`/`langColor`.
Verified: YAML + TypeScript highlight in the viewer; header shows the language.

**T6.2 · File-tree navigation column** ✅ 2026-07-10 — *Sourcebot free (file explorer)*
240px sticky tree column in `FilePage` over `/api/tree` (`fetchTree`): builds a
nested tree, auto-expands the path to the current file, active-row bar, dirs
toggle, files navigate. Verified: auto-expand + active highlight; clicking a
collapsed dir reveals its files; file rows deep-link. Moves "file explorer"
partial → have. *(buildTree unit test landed with T6.4.)*

**T6.3 · Live GitHub PAT verification** ✅ 2026-07-10 — *closes a testing caveat*
Ran the adapter live (200-repo account): `users:` pagination, `orgs:`,
`repos:`, exclude globs — 5 kept repos (4 private) synced, indexed, searched;
token verified absent from mirrors/data/API. Finding fixed: `users:` naming
the token's own login now lists via `/user/repos` so its private repos are
seen (public endpoint omits them); regression test added. ADR 2026-07-10.

**T6.4 · UI test harness (Vitest)** ✅ 2026-07-10 — *review gap: zero UI tests*
Vitest + jsdom + Testing Library (`test` block in vite.config.ts, `npm test` /
`make ui-test`). 23 cases: streamSearch SSE contract incl. the error-clobber
distinction (fake EventSource), SearchPage streaming/keyboard nav with clamp +
typing guard + collapse guard/facet toggling (styletron+baseui render, mocked
lang/highlight), and the T6.2 buildTree unit tests. CI wiring lands with the
first CI pipeline (none exists yet).

## EPIC 7 — Connectors & freshness ✅ 2026-07-11 *(Wave 1 — the biggest free-tier gap)*

**T7.1 · GitLab connector** ✅ 2026-07-11 — *Sourcebot free* *(fake-API tests; live run pending)*
`type: gitlab` (PAT, optional self-hosted `url:`): groups (subgroups
included), users (requester-scoped — own private projects appear), explicit
repos, excludes, Link-header pagination + 429 Retry-After, §5 metadata,
oauth2-basic authenticated clone. Shared `hostClient` extracted from the
GitHub adapter. Path-traversal guard on server-supplied project paths.

**T7.2 · Gitea connector** ✅ 2026-07-11 — *Sourcebot free*
`type: gitea` (PAT + required base URL): orgs/users/repos on the shared
`hostClient`. **Verified live against a real Gitea 1.26 container**: private
org repo synced, indexed, searched; token-as-basic-username clone auth
confirmed; token absent from data dir. *(Bitbucket Cloud/DC, Azure DevOps,
Gerrit follow as T7.x by demand — same adapter shape.)*

**T7.3 · GitHub App auth** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs* *(fake-API tests; live App run pending)*
`app:` block (id, installation_id, PEM key by path or inline env) on github
connections; stdlib RS256 JWT → installation token per sync run (always
fresh — AC "tokens refresh"), so no `ghinstallation`/go-github dependency.
No selectors → syncs the installation's granted repos. PAT/anon connections
unchanged (AC "falls back cleanly").
Deps: T2.2.

**T7.4 · Webhook-driven reindex** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs*
`POST /api/webhook` (HMAC `X-Hub-Signature-256`, constant-time, 404 when no
secret): push → targeted `repo_fetch_job` (fetch + reindex, no host
re-listing); repository/installation events → remote-connection re-sync.
**Verified live**: Gitea container webhook (GitHub-compat headers) → push →
searchable with `resync_interval: "0"`; bad signature 401 live + tested.
Deps: T7.3.

**T7.5 · Periodic re-sync cadence** ✅ 2026-07-11 — *Sourcebot free (auto-freshness)*
`sync.resync_interval` (default `1h`, `"0"` disables) enqueues re-syncs for
remote connections; `EnqueueUnlessInFlight` is the debounce, local repos stay
watch/boot-owned. Moves "periodic re-sync" partial → have.

## EPIC 8 — Differentiators: paid features, OSS in phebs ✅ 2026-07-11 *(Wave 2 — high value)*

**T8.1 · Search contexts** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs*
Config-defined contexts (name → repo-name globs) + string-level `context:`
extraction ahead of `query.Parse` (zoekt has no such atom), compiled to one
`RepoSet` AND'd over the query; multiple atoms union. Table-driven Compile
tests + e2e over real shards. No DB table — config is the single source;
add CRUD when a UI needs it.

**T8.2 · MCP server** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs; flagship differentiator (PLAN P4)*
Official go-sdk server at `/api/mcp` (Streamable HTTP, same authentication as
the API): `search_code` (full query syntax incl. `context:`), `read_file`
(ranged), `list_repos`. **Verified live from Claude Code**: a headless
session listed repos, searched the needle, and read the file — all correct.
Deps: T8.1.

**T8.3 · MCP integration + polish** ✅ 2026-07-11
Large-file truncation on line boundaries with a `truncated` flag + ranged
re-reads; binary/unknown-repo tool errors; MANUAL §8 with copy-paste
`claude mcp add` + `.mcp.json` config. AC met by the live agent session.

**TD.2 · Epics 1–8 stabilization review** ✅ 2026-07-11 — *release hardening*
Closed the cross-epic correctness gaps found after Wave 2: fail-closed clone
credential handling and pagination, atomic pending successors + fenced leases,
repo-wide fetch/index/cleanup locking, startup artifact and shard-revision
reconciliation, immutable search/file/MCP refs, lazy request-safe file browsing,
exact query filters, Unicode highlights, accessibility, and durable UI tests /
lint in CI. Regression coverage includes concurrent enqueue/reaper races,
cleanup traversal/collisions, stale shards, failed index-state commits, private
App selectors, malformed URLs, transactional membership rollback, literal
artifact paths, and responsive UI corrections.

## EPIC 9 — Auth & code navigation ✅ 2026-07-11 *(Wave 3 — heavier lifts)*

**T9.1 · DB-backed users, sessions, multiple API keys** ✅ 2026-07-11 — *Sourcebot free (login/members)*
Surreal-backed users/SCS sessions, Argon2id local passwords, hashed named API
keys, one-time setup/bootstrap, login/logout, CSRF, Settings key lifecycle,
and always-on API/MCP auth. Tests cover atomic first-user creation, session
restart persistence, key create/use/revoke, legacy config-key hash migration,
rate limiting, malformed credentials, and CSRF enforcement.

**T9.2 · OIDC / SSO** ✅ 2026-07-11 — *Sourcebot paid/EE (enterprise SSO)*
`coreos/go-oidc` discovery + authorization code/PKCE/state/nonce, verified
ID-token email, stable issuer/subject linking, and T9.1 session bridging with
no seat gate. A fake IdP test completes redirect → callback → persisted user
→ authenticated session, verifies PKCE + nonce wiring, and rejects callback
replay after one-time state consumption.

**T9.3 · Code navigation (SCIP)** ✅ 2026-07-11 — *Sourcebot paid/EE (Pro code nav) — PLAN P4*
Lazy immutable `index.scip` ingestion from the indexed commit; precise
definition/reference/hover API, file-viewer panel, and MCP tools with UTF-8/
UTF-16/UTF-32 conversion and 500-reference cap. Fixture tests prove def/ref/
hover, Unicode positions, revision replacement, strict revision/path
validation, deterministic reference bounds, canceled-ingest snapshot
preservation, bounded semantic/cache/source budgets, oversized hover rejection,
LRU eviction, and graceful `available:false` when the index is absent.

**T9.4 · Git history / blame / commit / diff** ✅ 2026-07-11 — *Sourcebot free (history + blame)*
Revision-pinned `/api/blame`, `/api/commits`, `/api/commit`, `/api/diff`, three
viewer routes, and four MCP tools over bare-mirror Git plumbing. Multi-commit
fixture tests cover blame line mapping, rename-following history, parents/root
commits, binary and truncated diffs, literal/path-safe inputs, invalid refs,
cancellation, paging, mutable-HEAD isolation, producer termination at output
limits, aggregate metadata bounds, and race execution.

**TD.3 · Epic 9 corrective review** ✅ 2026-07-11 — *release hardening*
Closed proxy-aware authentication throttling, credential-free quota exhaustion,
OIDC email auto-linking, reconcile/index publication races, deletion rollback,
SCIP stale-range tolerance/reference semantics/negative caching, non-UTF-8
blame, explicit zero-context diffs, and history pagination request races. Also
made repository locks cancellable, idempotent, and idle-evicted; unreadable shard
audits and filesystem reconciliation are now non-destructive and cancellable.

**TD.4 · Shared bounded Git object reader** — *internal consolidation*
Factor source/history/SCIP Git reads onto one immutable-OID primitive with
per-call byte limits and shared not-found classification. Do not route SCIP
through the current source helper: its global 10 MiB blob contract conflicts
with SCIP's independent 64 MiB index and per-source limits. AC: one tested error
classifier and bounded reader serve all three callers without weakening any cap.

## EPIC 10 — Enterprise surface ✅ 2026-07-11 *(Wave 4 — build-our-own, PORT_MAP §12)* — demoed live via `make dev`: audit trail, analytics dashboard, and admin-vs-non-admin visibility over API + stateless MCP. T10.4 stays gated on real demand per its own ticket text.

**T10.1 · Audit log** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs*
Append-only `audit_event` table (narrow `AuditStore`), huma middleware
recording every mutating operation by operation ID + injected recorder for
the non-huma auth surface (logins incl. failures, setup, logout, key
lifecycle, OIDC); synchronous non-fatal writes; `audit.retention` sweep
(default 90d); admin-gated `GET /api/audit` + `#/audit` page with paging.

**T10.2 · Analytics** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs*
`usage_event` recorded by one `Searcher.Usage` hook (covers REST/SSE/MCP);
no query text stored; Go-side windowed aggregation; admin-gated
`GET /api/analytics` + `#/analytics` dashboard (tiles, per-day bars, top
repos) with zero chart dependencies. **Zero telemetry** — events never leave
the machine. `analytics.retention` (default 365d) shares the 12h sweep.

**T10.3 · Permission syncing + permission-aware search** ✅ 2026-07-11 — *Sourcebot paid/EE — OSS in phebs; the durable moat*
`repo_permission` edges (`<host>:<login>` → repo) mirrored in the adapters'
per-repo loops for private repos (GitHub/GitLab/Gitea; replace-set writes,
stale-kept on failure); config map links users → identities; enforcement
opt-in via the `permissions:` block. Per-user `RepoSet` compiled in the
search pre-pass (`Searcher.Visible` fills the reserved hook — in-query, not
post-filter); the same predicate gates listings, file/history/code-nav reads,
and all MCP tools (denial ≡ 404). MCP runs stateless to stop session-principal
smearing. Leak tests across search/stream/API/MCP. Deps: T9.1.

**TD.5 · Epic 10 corrective review** ✅ 2026-07-11 — *release hardening*
Closed the adversarial-review findings: permission-denial responses now match
missing-repo responses in body and work performed (no existence/timing
oracle); path-hosted GitLab/Gitea identity hosts are expressible in config;
permissions email keys share auth's NFC normalization; a bare `permissions:`
key enables enforcement as documented; retention prunes count-then-delete
without materializing rows; shutdown drains in-flight requests (and their
audit/usage writes) before the store closes; the audit page dedupes pages
shifted by live writes. Regression tests pin each.

**T10.4 · Multi-branch / tag indexing (`rev:`)** — *Sourcebot free (up to 64 revs/repo)*
**Architectural, not a ticket-sized change** — HEAD-only is a core P1
assumption (indexer, watch, freshness all lean on it). Gated on real demand;
sequence last. AC: an explicit per-repo branch allowlist (cap ≈8 per PLAN §1)
indexes + serves multiple revisions behind `rev:`.

---

## Contract-intelligence annex *(adopted 2026-07-12 — see the two PLAN.md ADRs of that date)*

> Before you change an RPC or field, phebs identifies who may be affected,
> cites the exact source evidence, and tells you what remains unknown.

Annex, not pivot: "self-hosted code search in one binary" stays the identity;
T11.1 is complete by a human-accepted terminal capacity-stop disposition, and
Epics 12–15 may proceed under the explicit 2026-07-22 governance ADR. That
sequencing decision does not establish GATE2-V2 or create a numeric accuracy or
completion claim. Commodity surfaces (spec-to-spec diffing, runtime topology,
catalog UX, PR delivery, scorecards) are integrated or deferred, never rebuilt.
phebs produces immutable, permission-safe proof bundles; workflow layers
(Workbench) reference bundle IDs and never recompute or weaken phebs's
conclusions. Public corpora validate mechanics only; an authorized **external**
design partner is required before broader graph or completion claims — no
implicit employer-estate exception. Absent a partner by Epic 15 completion, the
broader platform pivot freezes.

## EPIC 11 — Validation gate *(annex Stage 0)*

**T11.1 · SPIKE: Validate revision-pinned Go/gRPC consumer evidence and service identity resolution** ✅ 2026-07-22 — *human-accepted terminal outcome; GATE2-V2 remains `NOT_ESTABLISHED`; Epics 12–15 are released by explicit governance disposition, not by an empirical gate pass. See the superseding PLAN ADR and `spike/t111/REPORT.md`.*
Corpus: ≥4 systems including a multi-service monorepo, shared libraries
containing gRPC calls, multiple versions/vendored copies of generated protobuf
code, conflicting image/deployment/Helm/`service.name` identities, and
tests/mocks/generated/vendor directories. ≥200 positive examples, ≥100 hard
negatives, 30% blind holdout. Public systems validate mechanics, not
production completeness. The spike also fixes the evidence schema (atoms +
`snapshot_evidence` associations, per the PLAN evidence-model ADR) before any
extractor breadth — retrofitting provenance is costlier than starting with it.
AC — four gates, measured separately:
1. **Evidence integrity (fail ⇒ stop the initiative):** 100% of emitted
   evidence resolves to the exact repo/commit/file/span; identical input +
   extractor version ⇒ identical `evidence_atom_id`s; a failed extraction
   never publishes a partial replacement set; permission filtering precedes
   aggregation; no invisible repository or service name — or count — leaks
   through any result, including coverage manifests.
2. **Operation extraction:** ≥98% precision and ≥90% recall on eligible direct
   Go/gRPC patterns; ≥90% precision within every individual fixture;
   registration / client call / test / mock / generated / vendor references
   classified separately. Contract identity is the fully-qualified proto
   service+method — no global service identity required for this gate.
3. **Service/deployable identity:** ≥99% pairwise precision on high-confidence
   merges (target zero false merges in the blind holdout); ≥95% end-to-end
   precision on caller-deployable → operation edges; low coverage acceptable;
   **abstention is success**. Logical service, deployable, build target,
   image, workload, and runtime `service.name` stay distinct related entities.
4. **Field references:** the assertion is `REFERENCES_PROTO_FIELD` only (never
   `READS_RPC_RESPONSE_FIELD`); ≥98% direct getter/selector precision; ≥90%
   recall within the SCIP-eligible population; read/write/test/generated/
   unknown classification; canonical field identity
   `(contract_lineage_id, message_full_name, field_number)` mapped across ≥2
   consumer dependency versions.
Exit: gates 2+3 pass → Epics 12–15 as designed · gate 2 passes, 3 fails →
wedge ships with consumers grouped by repository/build target · gate 1 or 2
fails → stop; post-mortem ADR.

Recorded terminal disposition (2026-07-22): the independently reviewed sealed
campaign ended in a valid capacity stop before selection or disclosure. All
required human reviews and acceptance are recorded complete by the operator.
T11.1 is therefore complete as a validation process, while GATE2-V2 remains
`NOT_ESTABLISHED` and supplies no accuracy number. Governance releases
implementation sequencing for Epics 12–15 under the existing bounded-evidence,
permission, coverage, and abstention requirements. The remaining acceptance
criteria in Epics 13–15 stay unsatisfied until implemented and verified.

## EPIC 12 — Provenance schema & protobuf facts ✅ 2026-07-12 *(implemented dark and hardened; T11.1 sequencing release recorded 2026-07-22; runtime posture unchanged)*

**T12.1 · Evidence & assertion store** ✅ 2026-07-12
Content-addressed `evidence_atom` + `snapshot_evidence` associations, semantic
assertions (supporting **and contradicting** atoms), identity assertions,
coverage manifests, extraction runs — per the PLAN evidence-model ADR. Narrow
`EvidenceStore` interface (house style); deterministic confidence tiers only.
AC: an identical blob vendored in two repos yields one atom and two
associations with independent visibility; atomic staged publish survives a
mid-publish kill (prior facts intact, no partial set); proof-aware retention
honored — no deletion of evidence referenced by a retained bundle.

**T12.2 · Extraction job kind** ✅ 2026-07-12
`extraction_job` chained after indexing (as index chains after sync);
extracted-commit short-circuit (T3.2 pattern); `extract` class in the T3.3
failure taxonomy; **pure-reader invariant enforced** (no exec, no dynamic
loading, no corpus writes, no network while parsing). AC: a reindex chains
exactly one extraction per new commit; kill mid-run leaves published facts
intact; invariant violations are structurally impossible in the runner (no
exec/network capability in the extractor context), asserted by test.

**T12.3 · Protobuf declared-plane extractor (dark/provisional scope per the 2026-07-13 ADR)** ✅ 2026-07-12
protocompile-based: `.proto` → services, methods, messages, fields with exact
spans and deterministic `provisional_repo_path_v1_<sha256>` lineage IDs. AC for
this dark stage: on the fixture corpus every declared operation/field is an
assertion whose evidence atom resolves to the pinned commit and span in the
file viewer. Promotion to canonical cross-repository `contract_lineage_id`
remains T13.2 work and provisional facts cannot support product conclusions.

## EPIC 13 — Implementations, consumers, field references *(unblocked 2026-07-22 by the accepted T11.1 terminal disposition; pending)*

**T13.1 · Implementation & consumer resolution (Go/gRPC) — dark/provisional
scope per the 2026-07-22 disposition** ✅ 2026-07-22
`RegisterXServer` implementation pinning; typed generated-client call-site
resolution; `code_role` classification (production/test/mock/generated/
vendor). Dark-stage AC (T12.3 precedent): the productized extractor runs
inside the T12.2 pure-reader pipeline on the fixture corpus; every
implementation/consumer edge cites evidence atoms resolving to pinned
commit + span in the file viewer; classification and resolution are
deterministic across two runs; no output states or implies measured
accuracy. The original blind-evaluation clause (≥98% precision, ≥90%
recall, ≥90% per-fixture under the T11.1 measurement contract) is
**deferred to the pilot's internal validation gate** — it cannot be
satisfied post-capacity-stop and remains the promotion bar for any
accuracy-bearing claim.

**T13.2 · SCIP proto-field references**
`REFERENCES_PROTO_FIELD` assertions from cross-repo SCIP references over
generated accessors; read/write/test/generated/unknown classification; field
lineage across consumer dependency versions. AC: a renamed field (same number,
same lineage) tracks as one identity across two consumer versions.

**T13.3 · Coverage manifests**
Per-answer coverage certificate: repositories searched (the caller's visible
universe only), revisions, protocols supported, extractors applied + failures,
SCIP availability, unresolved candidates, freshness. AC: the certificate
provably changes when one repo's extraction fails; adversarial test shows no
invisible-repo leakage through names or counts.

## EPIC 14 — Query, proof bundles & MCP *(unblocked 2026-07-22 by the accepted T11.1 terminal disposition; pending)*

**T14.1 · Query API + proof bundles**
huma endpoints for `find_operation_consumers`, `find_proto_field_references`,
`get_extraction_coverage`; immutable self-contained proof bundles embedding
assertions, coverage certificate, extractor versions, and `visibility_context`
(principal, authorization provider, permission snapshot, visible-repo-set
digest). Bundles re-authorize on read — a bundle ID is not a bearer
credential; revoked repository access revokes old bundles. AC: admin and
member asking the same question produce different immutable bundle IDs; the
member bundle names no invisible repository.

**T14.2 · MCP tools**
The annex tools on the existing stateless `/api/mcp` server:
`find_operation_consumers`, `find_proto_field_references`,
`check_contract_compatibility`, `get_extraction_coverage`. AC: a live agent
session answers "who consumes this RPC/field?" with citations and coverage.

**T14.3 · Contract compatibility via pinned Buf child**
`check_contract_compatibility`: version-pinned `buf` child built from go.mod
(zoekt-git-index house pattern), sandboxed per the PLAN ADR — phebs-produced
descriptor inputs or sanitized temp tree, no network, CPU/memory/time limits,
never `buf generate`/protoc plugins/repository scripts; Buf version, args, and
result recorded in the extraction run. phebs enriches Buf's spec-level
verdicts with evidence-derived consumers. AC: a wire-breaking field change
reports the breaking rule **and** the affected consumers with call-site
citations.

## EPIC 15 — Contract impact report *(unblocked 2026-07-22; read-only; the first user-facing annex workflow — independent acceptance gate retained)*

**T15.1 · Report API + page**
For a contract, or a contract change between two extraction runs: known
consumers with exact call sites, field references, compatibility
classification, unresolved candidates, unsupported repositories/patterns,
evidence freshness — every row clickable through to pinned evidence.
Explicitly absent: PR comments/checks (separate ADR — read-only →
code-host-writer posture change), diagrams, service dossiers, runtime data.
AC: demoable via `make dev` on the fixture corpus; bounded-proof language
throughout ("no blockers found within the stated evidence scope"), coverage
certificate rendered with every conclusion.

## EPIC 16 — Investigations product slice *(post-gate: requires GATE2-V2 ESTABLISHED and pilot continuation decision; all code on a post-gate branch)*

Productizes the contract suite: [domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md)
v0.2, [experience spec](./INVESTIGATIONS.md) rev 3, [MCP envelope](./MCP_ENVELOPE.md)
v0.2, [pack manifest](./PACK_MANIFEST.md) v0.2. The synthetic fixtures under
`docs/fixtures/investigations/` are the conformance bar wherever cited.

**T16.1 · Investigation domain storage** — schema.surql tables and store
methods for Investigation, Revision, Run, RunEvent, RunArtifact, Decision,
Disposition, BaselineDesignation, Watch/WatchRevision per contract §2
mutability rules. AC: table-driven tests prove in-place edits of immutable
entities fail; run state derives only from RunEvents; creation idempotency
key returns the existing Run; supersession is the sole correction path.

**T16.2 · Immutable revisions, pins, retention ownership** *(needs T16.1)* —
Revision freeze; RunArtifact publication binds `PinRun`; pin ownership and
GC per contract §5. AC: pinned artifacts survive sweep; revocation/legal
policy overrides pins; GC refuses while an authorized owner exists.

**T16.3 · Authorization invariants** *(needs T16.1)* — query-time principal
projection on every read; count/existence non-disclosure; opaque ids;
refusal shape of fixture 06; re-authorization on sharing and transfer.
AC: negative-test matrix passes incl. fixtures 05/06 shapes; the suite
executes BOTH an unknown-identity input and an unauthorized-identity input
and asserts identical canonical response bytes, each compared against the
same golden fixture (fixture 06 is the expected
shape, not a one-time validation target); cursors void on ownership
transfer.

**T16.4 · Guided creation and async run state** *(needs T16.1–T16.3)* —
creation API with scope preview, authorization preflight, estimate, cancel,
bounded retries; publication lease. AC: failed/canceled attempts can never
publish (late-worker test); partial failures surface in the coverage
ledger; creation is idempotent under concurrent submission.

**T16.5 · Core views** *(needs T16.4)* — Overview (four cards, derived
eligibility badge with blocker codes), Census, Coverage, Evidence;
empty-state taxonomy first. AC: fixtures 01–05 each render their distinct
state; the eligibility badge has no write path; `make dev` demoable.

**T16.6 · MCP envelope implementation** *(needs T16.3; parallel to T16.5)* —
envelope v0.2 on the existing MCP tools; generated JSON Schemas checked in.
AC: all eight fixtures validate against the generated schemas — incl.
fixture 08's irreversible-truncation semantics in result views; refusal
indistinguishability test; server-rendered qualification text only.

**T16.7 · Consumer ledger and comparable diff** *(needs T16.2)* —
first/last-seen edge ledger (the retention change from VISION architecture
notes); cause-classified diff per contract §8 with comparison-report
fallback. AC: prohibited causes never render as removals; fixture 07 semantics
enforced plus a new comparable traced-addition/removal fixture added with
this ticket (fixture 08 is truncation and belongs to T16.6); ledger rows
survive run sweep.

**T16.8 · Review projection** *(needs T16.7)* — deterministic ReviewItems,
queues (new consumers, coverage regression, unresolved attribution),
per-principal cursors. AC: identical deltas yield identical item ids; items
supersede and expire by rule; no hand-creation path exists.

**T16.9 · Dossier export** *(needs T16.2, T16.3, T16.5)* — sealed export per
contract §8 with offline verification script. AC: digest chain verifies
offline with no phebs instance; export redacts to recipient scope at export
time; reopening re-authorizes against current ACLs.

## P5 hardening *(unscheduled — pull on demand)*

**T-P5.1 · `phebs backup` / `phebs restore` subcommands** — cold copy works
today (MANUAL §9 *Backup & restore*) but costs downtime. `phebs backup`:
exec the supervised pinned `surreal` binary's `export` against the running
instance into one artifact plus a manifest binding config digest, binary
digest, and store schema version. `phebs restore`: refuse unless `$DATA` is
absent or empty, import, then let sync + reconcile backfill mirrors and
shards. Also supplies the version-pinned export/import commands that
docs/RESTORE_PROCEDURE.md's acceptance boundary needs named. AC: backup
succeeds against a live server without stopping writes and its manifest
digests verify; restore into an empty `$DATA` reaches a serving instance
that reindexes with no operator action; both refuse existing/partial
targets; MANUAL §9 updated in the same PR.

## Deliberate non-goals *(per PORT_MAP §7/§12)*

SCIM provisioning, multi-org RBAC / seats, and a cloned "Ask" chat app —
phebs stays **MCP-first** (agents bring their own chat) and **single-tenant**.
Kubernetes/Helm waits for the P6 fleet profile. Anonymous-access and
entitlement gating are deleted outright (config bool, no license backend).

---

## Standing rules

- Decisions land as dated ADR bullets in PLAN.md, same PR as the change.
- Every epic ends with a `make dev` demo state — no epic is "done" if it
  can't be shown end-to-end.
- Upstream repo is behavior reference only; `ee/` paths never opened.
- Personal hardware, personal time, no employer code or credentials.
