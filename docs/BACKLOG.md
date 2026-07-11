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

## EPIC 10 — Enterprise surface *(Wave 4 — build-our-own, PORT_MAP §12)*

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

**T10.4 · Multi-branch / tag indexing (`rev:`)** — *Sourcebot free (up to 64 revs/repo)*
**Architectural, not a ticket-sized change** — HEAD-only is a core P1
assumption (indexer, watch, freshness all lean on it). Gated on real demand;
sequence last. AC: an explicit per-repo branch allowlist (cap ≈8 per PLAN §1)
indexes + serves multiple revisions behind `rev:`.

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
