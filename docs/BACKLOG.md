# phebs · backlog

Epics 0–10 historically mirrored the removed `PORT_MAP.md`; later epics extend
the same ticket ledger. Tickets are PR-sized and dependency-ordered for a
stacked workflow. Acceptance criteria (AC) are the merge bar. Decisions get
appended to PLAN.md as dated ADR bullets — no new architecture docs.

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

**TD.4 · Shared bounded Git object reader** ✅ 2026-07-15 — *internal consolidation*
Factor source/history/SCIP Git reads onto one immutable-OID primitive with
per-call byte limits and shared not-found classification. Do not route SCIP
through the current source helper: its global 10 MiB blob contract conflicts
with SCIP's independent 64 MiB index and per-source limits. AC: one tested error
classifier and bounded reader serve all three callers without weakening any cap.

## EPIC 10 — Enterprise surface ✅ 2026-07-11 *(Wave 4 — build-our-own, historical PORT_MAP §12)* — demoed live via `make dev`: audit trail, analytics dashboard, and admin-vs-non-admin visibility over API + stateless MCP. T10.4 was subsequently completed on demand on 2026-07-22.

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

**T10.4 ✅ · Multi-branch / tag indexing (`rev:`)** — *Sourcebot free (up to 64 revs/repo)*
**Architectural, not a ticket-sized change** — HEAD-only is a core P1
assumption (indexer, watch, freshness all lean on it). Gated on real demand;
sequence last. AC: an explicit per-repo branch allowlist (cap ≈8 per PLAN §1)
indexes + serves multiple revisions behind `rev:`.

Implemented on `t10.4-multirev-indexing`: the config admits seven aliased full
branch/tag refs plus implicit HEAD; one child build atomically publishes the
sorted selector/branch/commit set; startup repair verifies exact shard branch
metadata; unqualified queries are forced to HEAD and one `rev:` scope is
resolved only across the principal's visible repositories, with a second
commit check on serialization. Local watch includes only the admitted refs.
Extraction/proof semantics remain HEAD-bound by design.

---

## Contract-intelligence annex *(adopted 2026-07-12 — see the two PLAN.md ADRs of that date)*

> Before you change an RPC or field, phebs identifies who may be affected,
> cites the exact source evidence, and tells you what remains unknown.

Annex, not pivot: "self-hosted code search in one binary" stays the identity;
T11.1 is complete by a human-accepted terminal capacity-stop disposition, and
Epics 12–15 are complete under the explicit 2026-07-22 governance disposition.
Their completion does not establish GATE2-V2 or create a numeric accuracy or
completion claim. Commodity surfaces (spec-to-spec diffing, runtime topology,
catalog UX, PR delivery, scorecards) are integrated or deferred, never rebuilt.
phebs produces immutable, permission-safe proof bundles; workflow layers
(Workbench) reference bundle IDs and never recompute or weaken phebs's
conclusions. Public corpora validate mechanics only; an authorized **external**
design partner is required before broader graph or completion claims — no
implicit employer-estate exception. The retained post-Epic-15 platform-pivot
freeze is in force; Epic 16 remains separately blocked.

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
permission, coverage, and abstention requirements. At the time of that
disposition, the remaining acceptance criteria in Epics 13–15 stayed
unsatisfied until implemented and verified. They are now complete under their
individual tickets, including Epic 15's separately retained independent
acceptance gate recorded below.

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

## EPIC 13 — Implementations, consumers, field references ✅ 2026-07-22 *(unblocked by the accepted T11.1 terminal disposition)*

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

**T13.2 · SCIP proto-field references** ✅ 2026-07-22
`REFERENCES_PROTO_FIELD` assertions from cross-repo SCIP references over
generated accessors; read/write/test/generated/unknown classification; field
lineage across consumer dependency versions. AC: a renamed field (same number,
same lineage) tracks as one identity across two consumer versions. Implemented
as a pure reader over the immutable committed `index.scip`: exact definition
ranges bind generated Go struct fields/getters through protobuf tags and the
source `.proto` declaration; reference occurrences cite exact source spans.
Canonical identity is `(SCIP package lineage excluding dependency version,
message full name, field number)`, with ambiguous or incomplete joins
abstaining instead of guessing. The acceptance fixture renames field 1 across
two repositories and dependency versions while retaining one lineage/object.

**T13.3 · Coverage manifests** ✅ 2026-07-22
Per-answer coverage certificate: repositories searched (the caller's visible
universe only), revisions, protocols supported, extractors applied + failures,
SCIP availability, unresolved candidates, freshness. AC: the certificate
provably changes when one repo's extraction fails; adversarial test shows no
invisible-repo leakage through names or counts. Implemented as
`extract.BuildCoverageCertificate` over the narrow `RunSource` read surface:
deterministic `coverage-certificate-v1` with a sha256 digest over canonical
JSON, every visible repository present (including evidence-free ones), per-run
identity plus complete source-scope counters/digest, freshness against the
indexed commit, and SCIP availability derived only from a current-revision
publication. A durable latest-attempt marker makes staged/aborted replacements
certificate-visible without publishing partial evidence; the same-commit
forced-failure AC runs through the real worker and keeps the prior fresh run
while moving the digest. Store tests pin abort and killed-run sweep lifecycle.
Publication times are excluded from canonical content. The adversarial test
mutates hidden-repo run and attempt state and requires byte-identical
certificates, no hidden names, and zero invisible-repository queries. API
exposure is T14.1.

## EPIC 14 — Query, proof bundles & MCP ✅ 2026-07-22 *(unblocked by the accepted T11.1 terminal disposition)*

**T14.1 · Query API + proof bundles** ✅ 2026-07-22
huma endpoints for `find_operation_consumers`, `find_proto_field_references`,
`get_extraction_coverage`; immutable self-contained proof bundles embedding
assertions, coverage certificate, extractor versions, and `visibility_context`
(principal, authorization provider, permission snapshot, visible-repo-set
digest). Bundles re-authorize on read — a bundle ID is not a bearer
credential; revoked repository access revokes old bundles. AC: admin and
member asking the same question produce different immutable bundle IDs; the
member bundle names no invisible repository.
Implemented as default-dark Huma GET routes over the existing experimental
reader flag. Permission filtering produces the complete visible repository
slice before any evidence lookup. A query resolves all cited atoms and
occurrences, confirms the coverage digest did not move during construction,
and stores canonical `proof-bundle-v1` bytes under a sha256-derived ID while
atomically pinning every named published run. The visibility context commits
to principal, authorization-provider generation, and visible-repository-set
digest. `GET /api/proof_bundles/{id}` reauthorizes every scoped repository on
each read and returns indistinguishable not-found responses for missing or
revoked scope. Tests prove different admin/member IDs, byte-identical repeat
queries, no hidden repository names or hidden evidence calls, retroactive
revocation, one-snapshot read authorization, `422` bounded-query refusals,
single-domain filtered-query invariants, immutable persistence, and proof-aware
retention. MCP exposure is deliberately T14.2; compatibility remains T14.3.

**T14.2 · MCP tools** ✅ 2026-07-22
The proof-backed annex tools on the existing stateless `/api/mcp` server:
`find_operation_consumers`, `find_proto_field_references`, and
`get_extraction_coverage`. Implemented as a transport-only projection of the
same `api.ProofService` used by Huma, returning complete `proof-bundle-v1`
structured content; no MCP-specific filtering, aggregation, or qualification
logic exists. The tools are registered only under the existing experimental
reader opt-in and are otherwise undiscoverable. AC runs one official-SDK
stateless Streamable HTTP agent session through RPC consumers, field
references, and coverage, verifies exact source citations and the agent's
visibility context, and proves zero hidden-repository evidence calls or names.
`check_contract_compatibility` registers with its real pinned-Buf engine in
T14.3; T14.2 intentionally does not advertise a failing placeholder or freeze
a speculative schema.

**T14.3 · Contract compatibility via pinned Buf child** ✅ 2026-07-22
`check_contract_compatibility`: version-pinned `buf` child built from go.mod
(zoekt-git-index house pattern), sandboxed per the PLAN ADR — phebs-produced
descriptor inputs or sanitized temp tree, no network, CPU/memory/time limits,
never `buf generate`/protoc plugins/repository scripts; Buf version, args, and
result recorded in the extraction run. phebs enriches Buf's spec-level
verdicts with evidence-derived consumers and registers the corresponding Huma
endpoint plus MCP tool. AC: a wire-breaking field change
reports the breaking rule **and** the affected consumers with call-site
citations.
Implemented with Buf v1.72.0 as a Go tool and sibling child binary. The only
operation is fixed-policy `buf breaking` (`WIRE`, structured JSON,
symlinks disabled) over validated source files copied into a fresh temp tree;
network and writes outside that tree are denied, with independent input,
output, wall, CPU, and memory ceilings. A startup version/sandbox probe keeps
both transports undiscoverable on a missing or mismatched engine. Canonical
results commit to source digests without retaining blobs and embed a
bundle-local compatibility extraction-run record (engine/version/exact
relative args/exit/result); they do not publish into the repository extraction
table because caller-supplied before/after sets have no indexed repo revision.
Structured violation spans map to `(lineage,message,field_number)` and feed a
bounded multi-filter extension of the shared proof builder, so SCIP consumers
and exact occurrences remain permission-filtered, coverage-confirmed, and
identical over Huma and MCP. Real-Buf tests pin a wire-type break, exact rule,
span, stable identity, compatible rename, source/temp-path non-retention, and
determinism; HTTP and official-SDK MCP acceptance pin affected visible
consumers/citations and hidden-repository non-interference.

**T14.4 · Proof-bundle retention before pilot exposure** ✅ 2026-07-22
Add config-gated proof-bundle expiry before any pilot enables the annex query
surface. Expiration uses store metadata outside canonical bundle content, so
it never changes content IDs. Deleting an expired bundle and its
`proof-bundle:<id>` evidence pins is one transaction; it must not remove any
other bundle/checkpoint pin, and the existing evidence sweeper remains solely
responsible for reclaiming newly unpinned runs. Default retention is disabled
until an operator explicitly configures a lifetime. Reads of expired IDs fail
closed with the same not-found response as missing or unauthorized bundles.
AC: two bundles pin one superseded run; expiring the first preserves the run,
expiring the second makes it sweep-eligible, while an unexpired bundle remains
byte-identical and readable. Must land before Epic 15 pilot exposure.
Implemented with opt-in `proof_bundles.retention`, measured from the latest
successful materialization in store metadata (legacy rows fall back to
creation time). Reads and the bounded boot/hourly sweeper share the same
cutoff. Each bundle deletion transaction rechecks expiry and removes only its
exact `proof-bundle:<id>` pins; independent bundle/checkpoint pins survive,
and only the existing evidence sweeper can reclaim a newly unpinned run. The
AC additionally pins indistinguishable expired/missing/unauthorized 404s,
default-off config, active-bundle byte identity, and checkpoint isolation.

## EPIC 15 — Contract impact report ✅ 2026-07-22 *(read-only; independent acceptance gate closed by operator acceptance)*

Independent acceptance was recorded after review of the shared proof-service
projection, default-dark posture, exact immutable citations, permission
non-interference, bounded conclusions, and the retained full-suite acceptance
run. This closes Epic 15 only: GATE2-V2 remains `NOT_ESTABLISHED`, the broader
platform pivot stays frozen. Epic 16 implementation proceeds under the explicit
2026-07-22 operator bypass in PLAN.md; the validation and continuation records
remain incomplete and authorize no claim.

**T15.1 · Report API + page** ✅ 2026-07-22
For a contract, or a contract change between two extraction runs: known
consumers with exact call sites, field references, compatibility
classification, unresolved candidates, unsupported repositories/patterns,
evidence freshness — every row clickable through to pinned evidence.
Explicitly absent: PR comments/checks (separate ADR — read-only →
code-host-writer posture change), diagrams, service dossiers, runtime data.
AC: demoable via `make dev` on the fixture corpus; bounded-proof language
throughout ("no blockers found within the stated evidence scope"), coverage
certificate rendered with every conclusion.
Implemented as a deterministic `contract-impact-report-v1` projection of the
same immutable, permission-safe proof bundles used by HTTP and MCP; there is
no second persisted report or authorization path. The default-dark Huma
surface builds reports for canonical operations, stable protobuf field
identities, and proposed before/after contract inputs, then reauthorizes saved
report URLs by bundle ID. Each result separates known exact source occurrences
from unresolved syntactic candidates and renders every visible repository's
covered, stale, failed, processing, or unsupported domain state plus the full
coverage certificate. Conclusions bind to that certificate's digest and use
bounded-scope language. Repository evidence rows deep-link to their pinned
commit/path/line; coverage metadata and Buf spans over deliberately unretained
caller inputs are not misrepresented as repository evidence. Following the
T14.3 ADR, proposed changes compare bounded before/after source commitments and
record a bundle-local compatibility run rather than fabricating repository
publication rows for caller-owned inputs. Authenticated `/api/version`
capability discovery keeps both navigation and routes absent by default and
independently gates the contract-change tab on the pinned Buf probe; the
always-open anonymous response reveals no capability names. Go acceptance
proves deterministic readback, exact citations, unresolved and unsupported
rows, hidden-repository non-interference, revocation, change enrichment, and
dark posture; UI tests pin
all three forms, saved reads, caveat/certificate rendering, and immutable source
links. No code-host writer, diagrams, dossiers, or runtime plane was added.

## EPIC 16 — Investigations product slice *(operator-bypassed for implementation 2026-07-22; validation and continuation gates remain evidentially unsatisfied; all code on a post-gate branch)*

Productizes the contract suite: [domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md)
v0.2, [experience spec](./INVESTIGATIONS.md) rev 3, [MCP envelope](./MCP_ENVELOPE.md)
v0.2, [pack manifest](./PACK_MANIFEST.md) v0.2. The synthetic fixtures under
`docs/fixtures/investigations/` are the conformance bar wherever cited.

**T16.1 ✅ · Investigation domain storage** — schema.surql tables and store
methods for Investigation, Revision, Run, RunEvent, RunArtifact, Decision,
Disposition, BaselineDesignation, Watch/WatchRevision per contract §2
mutability rules. AC: table-driven tests prove in-place edits of immutable
entities fail; run state derives only from RunEvents; creation idempotency
key returns the existing Run; supersession is the sole correction path.

Implementation is present on `codex/t16.1-investigation-domain-storage`: ULID
and tuple/content identities, create-once semantic rows, checked mutable
Investigation/Watch projections, contiguous digest-checked RunEvents,
event-derived Run state, concurrent idempotent Run creation, scoped-content
RunArtifacts, and explicit correction records. The schema and every embedded
SurrealQL statement validate; repository-wide compilation/vet and the touched
store package's lint are clean. The
SurrealDB-backed AC suite passed locally with `-run '^TestInvestigation'` on
2026-07-22, including 24-way concurrent Run creation and the full immutable
entity/supersession table.

**T16.2 ✅ · Immutable revisions, pins, retention ownership** *(needs T16.1)* —
Revision freeze; RunArtifact publication binds `PinRun`; pin ownership and
GC per contract §5. AC: pinned artifacts survive sweep; revocation/legal
policy overrides pins; GC refuses while an authorized owner exists.

Implementation is present on `codex/t16.2-investigation-pins-retention`:
artifact plus compatible extraction-run pins publish atomically; immutable
Investigation/Baseline/Dossier owner claims and append-only releases serialize
against bounded collection; Baseline creation acquires its owner in the same
transaction; and four explicit policy overrides can supersede active owners.
Artifact collection releases only its exact evidence-pin namespace and leaves
evidence deletion to the existing proof-aware sweeper. Schema and embedded
SurrealQL validate, package compilation/vet and focused lint are clean. The
SurrealDB-backed `-run '^TestInvestigation'` suite passed on 2026-07-22 in
42.041 seconds, including every owner, override, atomic-publication, and
pin-isolation AC plus the retained T16.1 regressions.

**T16.3 ✅ · Authorization invariants** *(needs T16.1)* — query-time principal
projection on every read; count/existence non-disclosure; opaque ids;
refusal shape of fixture 06; re-authorization on sharing and transfer.
AC: negative-test matrix passes incl. fixtures 05/06 shapes; the suite
executes BOTH an unknown-identity input and an unauthorized-identity input
and asserts identical canonical response bytes, each compared against the
same golden fixture (fixture 06 is the expected
shape, not a one-time validation target); cursors void on ownership
transfer.

Implementation is present on `t16.3-authorization-invariants`:
`store.InvestigationAuthzStore` principal-projects every domain read (unknown
and unauthorized are the identical `ErrNotFound`, and integrity errors surface
only after authorization); owner-authorized reader grants re-check at query
time; ownership moves only through the audited transfer path, which bumps the
investigation's authorization revision and thereby voids per-principal cursors
without deleting them; Watches stay owner-only. `mcp.NotAvailableRefusal`
renders the canonical fixture-06 refusal as a pure function with no
denial-reason input, golden-tested byte-for-byte, and a shape test proves the
minimal refusal carries none of fixture 05's authorized-only sections. The
SurrealDB-backed matrix (all ten reads × owner/grantee/stranger/unknown), the
identical-bytes AC, and the sharing/transfer/cursor lifecycle passed locally
on 2026-07-22 within the `-run '^TestInvestigation'` suite.

Post-merge review hardening on `codex/t16.3-review-fixes` closes the concurrent
forms of the same AC: projected reads bind and finally recheck an exact
transfer-revision plus grant-generation epoch; grant/revoke/cursor changes
serialize with transfer on the Investigation row; revoke/regrant cannot revive
an old cursor; promoting a grantee consumes that grant; and each grant, revoke,
transfer, or cursor mutation commits atomically with its audit event. Focused
regressions cover authorization-epoch ABA, revoke/regrant cursor invalidation,
promoted-grant consumption, and all four audit action classes.
The complete SurrealDB-backed Investigation suite passed after remediation on
2026-07-22 in 63.007 seconds.

**T16.4 ✅ · Guided creation and async run state** *(needs T16.1–T16.3)* —
creation API with scope preview, authorization preflight, estimate, cancel,
bounded retries; publication lease. AC: failed/canceled attempts can never
publish (late-worker test); partial failures surface in the coverage
ledger; creation is idempotent under concurrent submission.

Implementation is present on `codex/t16.4-guided-creation-run-state`: the
permission-first preview resolves only caller-visible indexed repository
snapshots, returns deterministic estimates/blockers, and binds submission to a
digest that is re-preflighted at create time. One transaction freezes the
active Investigation, Revision, queued Run/event, idempotency mapping, audit
record, and `investigation_run_job`; concurrent exact submissions cannot mint
competing objects or queue slots. Attempt leases fence progress, bounded retry
rollover, owner cancellation, and atomic published/failed artifacts. Terminal
publication includes the RunEvent, artifact, evidence pins, active-
Investigation retention owner, and audit row; failure artifacts retain
reconciled partial/failed coverage but structurally reject facts and pins.
The adapter remains unregistered in the production binary until an executable
released pack is available, so the post-gate implementation cannot expose an
undrainable workflow. Pure/API/compile/vet/lint and SurrealQL validation are
green. The complete live SurrealDB-backed Investigation suite passed on
2026-07-22 in 79.323 seconds, including concurrent guided creation, bounded
retry rollover, cancellation and failure late-worker fences, partial-failure
coverage publication, and the retained T16.1–T16.3 regressions.

**T16.5 ✅ · Core views** *(needs T16.4)* — Overview (four cards, derived
eligibility badge with blocker codes), Census, Coverage, Evidence;
empty-state taxonomy first. AC: fixtures 01–05 each render their distinct
state; the eligibility badge has no write path; `make dev` demoable.

Implementation is present on `codex/t16.5-core-views`: one read-only,
principal-scoped API projection carries the existing Investigation envelope
into four table-first views without inventing a parallel confidence or
eligibility model. Overview renders four open summary regions and a derived,
read-only eligibility result; Census preserves facts while separating
unresolved service and owner attribution; Coverage surfaces processing and
hop completeness; Evidence retains proof, snapshot, occurrence, and
verification-action identifiers. Canonical fixtures 01–05 pin five distinct
rendered states, including authoritative bounded zero, incomplete processing,
unresolved attribution, and a refusal that withholds all claim-bearing counts.
Production stays structurally dark when no source is bound. `make dev`
explicitly binds only the checked-in synthetic fixture corpus so this
operator-bypassed slice is demoable without presenting the fixtures as
published evidence. All 77 UI tests, the focused API and whole-repository
compile gates, vet, touched-package lint, and the production UI build are
green. Authenticated browser acceptance exercised all five fixtures at
1440×1000 in light/dark themes and the 390×844 responsive layout with no page
overflow or console diagnostics.

**T16.6 ✅ · MCP envelope implementation** *(needs T16.3; parallel to T16.5)* —
envelope v0.2 on the existing MCP tools; generated JSON Schemas checked in.
AC: all eight fixtures validate against the generated schemas — incl.
fixture 08's irreversible-truncation semantics in result views; refusal
indistinguishability test; server-rendered qualification text only.

Implemented on `codex/t16.6-mcp-envelope`: the shared proof service now
projects its already-authorized immutable answers into the typed
`envelope_version: "1.0"` contract for all four enabled proof/compatibility
MCP tools. Nine deterministic draft-2020-12 schemas are generated from the
same Go source used for MCP `outputSchema` advertisement and checked in under
`schemas/`; a drift test pins the bytes. All eight canonical fixtures pass
both structural and cross-field semantic validation. Fixture 08 additionally
has negative mutations proving hard truncation cannot regain completeness,
continuation, or absence eligibility. Fixture 06 remains byte-identical for
unknown and unauthorized identity reads, and zero-result qualifications are
selected and rendered solely by the server. Stateless proof queries
deliberately expose pack-defined processing counts as `withheld` and remain
`partial`; they do not manufacture an eligible universe or negative proof.

**T16.7 ✅ · Consumer ledger and comparable diff** *(needs T16.2)* —
first/last-seen edge ledger (the retention change from VISION architecture
notes); cause-classified diff per contract §8 with comparison-report
fallback. AC: prohibited causes never render as removals; fixture 07 semantics
enforced plus a new comparable traced-addition/removal fixture added with
this ticket (fixture 08 is truncation and belongs to T16.6); ledger rows
survive run sweep.

Implemented on `codex/t16.7-consumer-ledger`: immutable, principal-scoped
consumer snapshots freeze the claim/schema/identity, pack/rule/extractor,
authorized universe/enumeration, build, snapshot/external-input, completeness,
freshness, and visibility dimensions required by contract §8. Any changed or
failed dimension produces a sorted comparison report with per-side coverage
only and structurally no deltas. Fully comparable snapshots emit only
positively traced relationship additions/removals. A compact first/last-seen
ledger retains inactive removal tombstones and recognizes later
reintroductions; it is authorization-projected and stored independently of
sweepable RunArtifacts. Fixture 07 pins the fallback, fixture 09 pins one
traced addition and removal, pure tests cover every prohibited cause, and the
live store AC covers ledger persistence after artifact sweep.

**T16.8 ✅ · Review projection** *(needs T16.7)* — deterministic ReviewItems,
queues (new consumers, coverage regression, unresolved attribution),
per-principal cursors. AC: identical deltas yield identical item ids; items
supersede and expire by rule; no hand-creation path exists.

Implemented on `codex/t16.8-review-projection`: a versioned pack projection
derives the three fixed queues only from an authorized immutable consumer
snapshot, its comparison, its typed coverage ledger, and unresolved hops whose
fact IDs occur in that snapshot. ReviewItem identities domain-separate the
principal, Investigation, source comparison, projection version, subject,
delta/cause, evidence reference, and human-record-state digest while excluding
evaluation time, so identical semantic inputs reproduce the same IDs.
Publishing a later source sequence supersedes the prior projection; the
versioned lifecycle rule computes expiry from immutable publication time.
Acknowledgement and last-viewed comparison are canonical payloads in the
existing authorization-epoch-bound per-principal cursor and never mutate an
item. The store interface exposes materialize/list/cursor operations but no
ReviewItem create/put method. Pure tests pin deterministic IDs, all three
queues, supersession/expiry and the missing hand-creation path; the live store
test pins idempotent materialization, acknowledgement, lifecycle transition,
and unauthorized non-disclosure.

**T16.9 ✅ · Dossier export** *(needs T16.2, T16.3, T16.5)* — sealed export per
contract §10 with offline verification script. AC: digest chain verifies
offline with no phebs instance; export redacts to recipient scope at export
time; reopening re-authorizes against current ACLs.

Implemented on `codex/t16.9-dossier-export` against the domain contract's
Dossier §10: canonical `phebs-investigation-dossier-v1` JSON contains a
recipient-redacted manifest, separately digested object/finding entries,
authorized locators for non-embedded source material, a domain-separated
SHA-256 root, and an Ed25519 signature with key identity. The standalone
`scripts/verify-dossier.go` checks the complete chain and optional independent
trust anchor without a phebs process or network access, while always stating
that offline integrity is not current authorization or validity. Export
requires a current scope resolver, intersects the principal-scoped snapshot,
omits facts without a positively authorized unit identity, derives
recipient-only snapshot/input manifests and eligibility, and rechecks both
Investigation and repository-scope epochs before returning bytes. Persistence
and the primary-artifact Dossier retention owner commit atomically. Reopen
verifies the sealed bytes, reauthorizes the Investigation, Revision,
RunArtifact, consumer snapshot, optional Baseline/Decision/predecessor, every
included fact and its current unit scope, then rechecks the authorization
epoch. Pure and script tests cover deterministic sealing, every tamper layer,
offline operation, and fail-closed redaction; the live store AC covers
retention and post-export scope revocation.

**Epic 16 implementation is complete.** The operator bypass authorizes this
post-gate implementation only; it does not retroactively satisfy the retained
validation or continuation evidence gates, release a pack, or enable the dark
production creation/export surfaces.

## EPIC 17 — Contract Atlas ✅ 2026-07-23 *(T17.1 stable, T17.2–T17.5 experimental-dark)*

Turns the existing contract facts into a discoverable, read-only catalog.
Users must be able to browse from a visible repository to a declared operation
without already knowing its canonical identifier, inspect the exact evidence
and bounded shape that phebs can support, and hand that operation to the
existing Impact workflow. This is a presentation and query layer over the
annex evidence model, not a new conclusion engine. It remains orthogonal to
Epic 16 and to every validation gate: provisional labels, coverage, tiers,
abstentions, and the ban on accuracy or absence claims all remain in force.

T17.1 uses only established repository-reading boundaries and is always
available. T17.2–T17.5 are registered only when the existing provisional
extraction feature is enabled and advertise an authenticated-only
`contract-atlas` capability; the default and anonymous surfaces remain dark.
Runtime traffic is explicitly absent. Any future runtime overlay requires its
own ADR, partner-supplied observations, and strict source/runtime separation
under the zero-telemetry posture.

**T17.1 · Persistent repository explorer** ✅ 2026-07-23
Extract the FilePage's lazy tree into a shared repository-browser component and
render it as a permanent left rail on Search. The rail lists every repository
visible to the caller through `/api/repo-status`, loads exactly one directory
level at a time through `/api/folder_contents`, supports explicit `repo:`
filter insertion, and opens files without issuing a search. It never calls the
recursive `/api/tree` route. At mobile widths it becomes a collapsible drawer;
the search input and results remain independently usable.

AC: a non-admin rail lists exactly the caller-visible repositories; an
adversarial hidden repository appears in neither rendered state nor the UI's
folder-request ledger. A file is reachable from a cold Search page with zero
search requests. Expanding a directory issues one request and re-expansion
uses the cache. Filter insertion preserves the user's query, avoids duplicate
repo atoms, and quotes repository names through the existing helper. UI tests
cover desktop rail, mobile drawer, loading/error/retry, repository changes,
direct file navigation, and permission-filtered empty state.

Implemented by sharing the revision-pinned lazy folder reader between Search
and FilePage. The Search rail sorts only the permission-filtered status
projection, changes repositories without speculative folder reads, caches
expanded paths by repository/revision/path, links directly to immutable file
routes, and inserts an exact quoted repository atom into the existing query.
The focused UI suite pins cold-page zero-search navigation, hidden-repository
non-disclosure, per-directory request counts and cache reuse, filter
deduplication, retry and empty states, and the mobile drawer lifecycle.

**T17.2 · Same-file protobuf shape facts (`protodecl` v3)** ✅ 2026-07-23
Enrich the declared plane without pretending to link a protobuf module.
`protodecl` adds exact-span `DECLARES_SERVICE` and `DECLARES_MESSAGE` facts,
request/response raw type references and client/server streaming flags to
`DECLARES_OPERATION`, and field type, cardinality, map, and oneof attributes
to `DECLARES_FIELD`. Operation and field canonical objects remain stable. Any
type reference is resolved only when protobuf lexical lookup finds exactly one
declaration in the same source file and therefore the same provisional
lineage. Imported or otherwise unresolved references retain their raw spelling
and import context with an explicit unresolved reason; they are never labeled
external, because the pure-reader has no trusted module/import-root identity.
Shape traversal is an API concern and must be cycle-aware and bounded; the
extractor emits facts, not recursive blobs.

The extractor, evidence schema, and registry pin versions advance together.
The one-blob streaming invariant, all parser complexity bounds, atomic
publication, deterministic ordering, and file-scoped provisional lineage stay
unchanged.

AC: the fixture corpus resolves request and response types declared in the
same file and cites exact operation/message/field spans; imported, missing,
and ambiguous references abstain with distinct reason codes and no external
claim. Scalar, repeated, map, nested-message, and oneof fields preserve their
shape attributes. Empty services and empty messages are still discoverable
through declaration facts. Recursive message definitions do not recurse in
the extractor. Two complete runs are byte-identical, and the registry-pin and
pure-reader guards pass.

Implemented in `protodecl` 3.0.0 with `t17-v1` atoms and v3 rules. Typed JSON
details preserve RPC raw request/response names and streaming flags plus field
type, cardinality, map key/value, and oneof membership. Protobuf lexical lookup
binds only a unique declaration in the same file; unresolved imports, missing
declarations, duplicate declarations, and wrong declaration kinds have
separate reason codes, bounded digest-bound import context, and no external
classification. The fixture suite pins exact spans, scalar/repeated/map/nested
message/oneof shape, empty declarations, finite recursive definitions, and
two-run byte identity. The pure-reader and exact registry-version guards pass;
the persisted worker test is retained for the live SurrealDB-backed gate.

**T17.3 · Bounded, snapshot-consistent Contract Catalog API** ✅ 2026-07-23
Add a read-only `contract-atlas-v1` projection with a paged service/operation
listing and a bounded operation-detail read. Declaration identity is
`(repository, declaration_lineage, service_fqn)`; an operation adds its method.
The API may expose `/service.fqn/Method` as the canonical query spelling, but
name equality never merges declaration lineages. Same-named declarations in
different files or repositories remain separate. SCIP package lineage is a
field-dependency identity and is not presented as a canonical
service/operation lineage.

Visibility filtering happens before every evidence read or count. Each request
builds a coverage certificate over the already-visible repository universe,
selects assertions only from the exact run ids named by that certificate, and
confirms the certificate digest after projection; a changed digest retries or
returns a conflict rather than mixing revisions. Browse responses are
ephemeral and create no proof bundle, but every declaration, message, field,
implementation, caller, and unresolved candidate includes its immutable
repository, commit, path, byte/line span, assertion id, and run id. Coverage
metadata is kept distinct from source evidence.

Lists use stable opaque cursors bound to the complete authorization projection
(principal, provider generation, permission snapshot, and visible-repository
set) and coverage digest. Server-side constants bound page size, expanded
message depth/node count, and relationship rows. Crossing a bound returns an
explicit truncation/completeness state and continuation where safe; it never
silently drops rows or turns a partial result into an absence statement.
Caller and registration facts retain their own lineage and tier. If the
declared-plane relationship cannot be proven, the API labels the name match or
extractor abstention as unresolved rather than attaching it to one declaration
by guess.

AC: unknown, unauthorized, and deleting repositories reveal no names, counts,
cursor differences, or evidence calls. Duplicate service FQNs in two files of
one repository and in two repositories produce distinct declaration rows.
Continuation pages are stable and non-overlapping; a cursor is rejected after
its authorization or coverage binding changes. A publication race cannot
produce a mixed-revision response. Limit tests pin every boundary and
truncation shape. Every claim-bearing detail row has a resolvable immutable
source locator and every response carries the exact coverage digest/state used
to build it.

Implemented as the authenticated-only `contract-atlas` capability and the
ephemeral `GET /api/contract_atlas` and
`GET /api/contract_atlas/operation` projections. Listing scans exact
certificate-selected `proto-contract` runs in stable assertion order and uses
an opaque checksum-bound cursor over query, principal, authorization provider,
permission snapshot, visible-repository digest, coverage digest, repository,
and assertion position. Detail expands only proven same-file message links and
keeps registration/caller name matches in their own lineage and tier, labeling
unproven joins and extractor abstentions separately. Fixed limits cover page
size, assertion scans, source locators, message depth/nodes/fields, and joined
relationships; every crossing is explicit and continuable where the underlying
ordered assertion scan is safe. Both projections rebuild the coverage
certificate after reading and retry or conflict on publication races. The
adversarial suite pins dark/anonymous posture, zero evidence calls for
unknown/hidden/deleting scopes, duplicate-FQN separation, stable pages,
authorization/coverage cursor invalidation, immutable locators, no bundle
write, field/depth/node/relationship/scan bounds, and a mid-read publication
change.

**T17.4 · Contract Atlas UI (protobuf/gRPC)** ✅ 2026-07-23
Add the authenticated capability-gated **Contracts** navigation item and
**Contract Atlas** page. The table-first interface supports repository,
package, protocol, and provisional-lineage filters; a service → operation
tree; bounded request/response message trees; declaration, implementation,
caller, and unresolved evidence links; and tier, freshness, completeness, and
coverage chips. Duplicate declarations and unresolved joins remain visibly
separate. Every source row opens the pinned file revision.

**Analyze impact** navigates to `#/impact?operation=/service.fqn/Method`,
pre-populating but not automatically submitting the existing operation form.
The nav taxonomy decision—Contracts discovers interfaces, Impact answers a
bounded change question, and Investigations manages a durable workflow—lands
as the implementation ADR.

AC: `make dev` exposes the Atlas only for its explicit fixture binding. From a
cold page, a user with no operation identifier can browse to a declaration,
inspect both bounded shapes and evidence limitations, follow a pinned source
link, and open the pre-populated Impact form. Disabled and anonymous servers
advertise no capability, route, OpenAPI operation, or navigation item. UI tests
cover pagination, truncation, duplicate declarations, unsupported/failed/stale
coverage, empty states, and desktop/mobile layouts.

Implemented as the capability-gated `#/contracts` workspace and navigation
item. Exact filters drive a paged, lineage-preserving service → operation
index; selecting an operation loads its bounded message trees, source claims,
separately qualified implementations/callers/abstentions, coverage certificate,
freshness/tier/completeness chips, and immutable file links. Duplicate FQNs
remain separate by repository and provisional lineage. Stale responses are
canceled and generation-guarded. Analyze impact transfers only the canonical
operation into the existing form and does not submit it. Responsive tests pin
the desktop split/mobile stack, pagination, truncation, duplicates, all
coverage states, empty results, exact filters, source links, and stale-request
suppression. Production still derives the capability only from real
provisional evidence. `make dev` explicitly binds the validated synthetic
`docs/fixtures/contracts/contract-atlas.json` adapter, which selects a visible
indexed repository for a resolvable pinned source link, writes no evidence,
and labels every response as synthetic.

**T17.5 · Accessible focused dependency map** ✅ 2026-07-23
Render one deterministic one-hop neighborhood for the selected operation from
the already-authorized T17.3 response: declaration/registration providers,
name-bound caller evidence, and separately labeled unresolved candidates. No
global graph and no graph-only data surface exist. The table remains the
authoritative accessible representation; the diagram has keyboard-readable
labels and a compact mobile fallback. It performs no additional evidence
requests and contains no producer/consumer or runtime edges until separately
released evidence packs provide them.

AC: the graph and table contain the same authorized edge identities and
labels; hidden repositories cannot affect layout, node/edge counts, or empty
state. Identical input yields identical layout. A contract with no edges
renders an honest empty neighborhood naming the exact coverage scope and
completeness state.

Implemented as a pure client projection of the selected operation detail.
Implementation, caller, and unresolved-candidate claims are flattened by
immutable source locator into one stable, sorted edge list. The deterministic
SVG and authoritative accessible table carry the same edge ids and labels;
every diagram node is a keyboard-focusable pinned-source link. Registration
providers, name-bound callers, and dashed extractor abstentions remain
visually and textually distinct. The wide SVG yields to the same table plus a
compact count on mobile. A zero-edge state includes visible-repository count,
coverage digest, and relationship completeness while explicitly refusing a
runtime-absence conclusion. Tests prove graph/table parity, input-order
determinism, coverage-only repository independence, empty/truncated
qualification, mobile fallback, and that rendering performs no additional
catalog or evidence request.

## EPIC 18 — First public release *(complete 2026-07-23)*

The first tag is a product boundary, not a Git bookkeeping event. It must bind
one inspectable version, the server plus its same-module helper binaries,
the supported SurrealDB line, reproducible checksums, a fresh-data smoke, and a
green hosted run. Local success alone cannot authorize publication.

**T18.1 · Release identity contract** ✅ 2026-07-23
Add a dependency-free `phebs version` command and make `make build` accept,
validate, stamp, and verify an explicit SemVer `VERSION`. The exact value must
remain shared by CLI output, startup logs, `/api/version`, and backup manifests.
Ordinary source builds retain a development identity; Git state is never
silently converted into release authority.

AC: `make build VERSION=v0.1.0` produces a binary whose exact stdout is
`v0.1.0\n`; invalid or padded versions fail before compilation; `phebs version`
accepts no arguments and initializes no configuration, database, child
process, or network listener. Existing API and backup tests continue to bind
the same process-global value.

Implemented with a SemVer-validation prerequisite and a post-build execution
check. The main package exposes a narrow writer-injected version command while
all existing version consumers retain the same linker-stamped variable.

**T18.2 · Hermetic local/hosted verification** ✅ 2026-07-23
Pin the Go linter and SurrealDB installer to reviewed versions, separate fast
static/build checks from the live SurrealDB suite, add timeouts and explicit
version assertions, and make the same commands runnable locally. The workflow
must never silently skip store tests because `surreal` is absent.

AC: workflow review shows no floating tool version; a missing or wrong-major
SurrealDB fails before tests; Go test, race/static checks, UI test/lint/build,
and embedded-UI compilation all have named green jobs; local targets execute
the same command families.

Implemented with repository-owned pins for Go 1.26.5, Node 24.18.0 LTS,
golangci-lint 2.12.2, and SurrealDB 3.2.0. Full action commit SHAs, annotated
with their reviewed release tags, replace major-only refs. Static, full Go,
concurrency race, and UI/embedded
builds are separate bounded jobs backed by `make ci-*` targets. The SurrealDB
jobs download the exact Linux release archive, verify its upstream-published
SHA-256 and reported version before tests, and cannot silently skip a missing
database. A permanent source test pins every tool, action, named job, and
download-verification step. The workflow is locally verified; its first hosted
execution remains T18.4 because this repository still has no remote. The
sealed `spike/t111` validation subtree remains compiled and tested but is
excluded from lint because fixing its pre-existing findings would mutate the
preserved evidence artifact.

**T18.3 · Release bundle and fresh-data smoke** ✅ 2026-07-23
Assemble the versioned phebs server, same-module `zoekt-git-index` and Buf
children, license/readme, and a machine-readable manifest with SHA-256
digests. Exercise that assembled bundle from an empty temporary data directory:
bootstrap authentication, sync/index a local fixture repository, search it,
browse a pinned file, and verify the Contract Atlas stays dark without its
explicit fixture/evidence binding.

AC: two builds from the same commit/toolchain produce equal file manifests;
tampering with any bundled executable fails verification; the smoke uses only
the bundle and declared prerequisites, reaches a healthy authenticated server,
and proves sync → index → search plus dark experimental posture.

Implemented with a host-native `make release` boundary that refuses
non-v-prefixed release identities and existing output directories. Its
canonical `phebs-release-manifest-v1` contains no time or output-path field and
binds the explicit commit, pinned Go toolchain, target, stable modes, sizes,
and SHA-256 payload digests. Strict verification rejects manifest schema or
canonicalization drift, unsafe/duplicate/unsorted paths, missing or extra
entries, symlinks/non-regular files, and all mode/size/digest changes.
Permanent tests compare two independently assembled manifests and exercise
tampering and fail-closed input cases. The standalone smoke runner strips
development fixture and child overrides, pins the bundled children plus the
declared SurrealDB prerequisite, and from empty temporary state proves
authenticated startup, local-repository sync/index, exact-commit search and
browse, and the absent Contract Atlas capability/404 route. Hosted clean
checkout execution remains T18.4.

Acceptance closed with the operator's live invocation against the verified
`v0.1.0` bundle for commit
`5a9847ac8d2e33b774d3c27e08eda865ddc53540`; the runner reported
`sync->index->search, pinned browse, Contract Atlas dark`. This authorizes
T18.4 to begin but does not authorize a remote, tag, or publication.

**T18.4 · Public remote, hosted gate, and `v0.1.0`** ✅ 2026-07-23
Create or bind the explicitly approved GitHub repository, push `main`, run the
hosted workflow, then create and push the annotated first tag only from the
exact green commit. Publish checksums and prerequisite/validation caveats; do
not claim the `NOT_ESTABLISHED` contract-intelligence accuracy gate passed.

AC: the repository URL and visibility are operator-approved; branch protection
or the documented equivalent requires the hosted gate; the tag commit equals
the tested main commit; a clean checkout verifies the release manifest and
fresh-data smoke; release notes preserve the default-dark and validation
caveats.

Accepted at the approved public remote
`https://github.com/bmeddeb/phebs`. Push run `30027844408` passed all five
named jobs at exact commit
`fe7b692706017fc57916f8b985d380f868dee2a6`; annotated tag and Latest release
`v0.1.0` resolve to that commit. The published Linux/amd64 archive and adjacent
checksum verify as
`63103500a6b86aa3e4533fb1693065009585f6be509e48aab7b26373405daaf6`.
The canonical manifest binds six payloads, including portable
`phebs-otel-demo.yaml`. Release notes name the Linux-only binary boundary,
macOS source-build path, Git and SurrealDB prerequisites, default-dark and
provisional posture, no-runtime-absence rule, and the still-closed external
`NOT_ESTABLISHED` validation result.

## EPIC 19 — Thrift protocol pack *(adopted 2026-07-25; full protobuf parity)*

The operator named the driving combination (Thrift IDL + Apache Thrift Go
runtime, jaeger corpus), satisfying the protocol-pack gate below. Scope:
declarations, Go consumer evidence, catalog/impact/proof/MCP surfaces, UI, and
a `make dev` demo. Non-goals: Buf-style wire-compatibility checking (no Thrift
engine), field-consumer proofs (scip-proto-field is protobuf-only), cross-repo
lineage promotion (shared gRPC-pack limitation, T13.2 direction), and
`extends` inherited-operation expansion (recorded in service detail only).
Pack metadata per the preamble: evidence-pack cards land with T19.2/T19.3;
dark flag `experimental.provisional_thrift_extraction`; extractor versions
`thrift-contract` 1.0.0 / `thrift-consumer` 1.0.0; validation is the T19.1
executable rule gates (no public accuracy claim; GATE2-V2 remains
`NOT_ESTABLISHED`).

**T19.1 ✅ · Thrift validation spike** — `spike/t191/` pins jaeger-idl,
jaeger-client-go (archived → HEAD-frozen), and jaeger; executable gates prove
100% corpus parse rate, scope-precedence reproducibility (`namespace go` last
segment, else file basename), zero false resolutions on the hand-labeled
consumer sample, live cross-corpus name-match joins, and honest abstention
without in-repo stubs. Binding decisions D1–D9 in `spike/t191/README.md`.

**T19.2 ✅ · `thriftdecl` extractor + dark flag** — thrift-contract 1.0.0 per
D1/D2/D8: wire-honest `Service.method_args`/`method_result` synthetic
messages (field 0 success, throws as result fields, oneway ⇒ no result
struct), thriftrw on the pure-reader allowlist, `.thrift` symlinks fail
closed, registry pin matrix, evidence-pack card, ADR + MANUAL. AC: T19.1
construct coverage via synthetic fixtures; byte-identical double run; worker
staged→published regression; full suite/vet/lint clean.

**T19.3 ✅ · `thriftgo` consumer extractor** — thrift-consumer 1.0.0 per
D3–D6: generated-header gate, processorMap wire-name index,
unique-match-or-abstain scan; `REGISTERS_THRIFT_SERVICE`, `CALLS_OPERATION`,
`UNRESOLVED_THRIFT_CALL`/`_REGISTRATION`, `THRIFT_EXTRACTION_GAP`. AC:
labeled-sample fixtures from the spike corpus; abstention tests; e2e green.

**T19.4 ✅ · Protocol registry + catalog generalization** — data-only registry
map (protocol → domains, detail schemas, relationship triple, field bounds);
`protocol=thrift` accepted; per-protocol field bounds (thrift 0..32767 —
field 0 is the result success slot); `Item.Protocol` from run domain;
protocol-major pagination cursor. AC: thrift operation detail expands through
unchanged `expandType`; protobuf responses byte-stable modulo cursor shape.

**T19.5 ✅ · Proof/impact/envelope/MCP** — protocol-blind entry points query
both consumer domains; `canonicalProofDomains` → 5; (domain, predicate) →
envelope identity kind with `rpc_operation` subjects; MCP prose de-gRPC'd
(tool names wire-frozen). AC: gRPC outputs unchanged except honest no-run
coverage rows; bundle determinism.

**T19.6 ✅ · Contract Atlas UI** — protocol filter option, oneway chip vs
streaming chips, union/exception badge, thrift relationship labels,
`.thrift` language entry. AC: Vitest green; protobuf pages unchanged.

**T19.7 · Demo + closure** — `phebs-thrift-demo.yaml` with the three D7
connections; MANUAL walkthrough; epic closure absorbs the Thrift bullet
below. AC: end-to-end `make dev` demo incl. ≥1 live name-match join and the
oneway chip on `agent.Agent/emitBatch`.

### On-demand protocol-pack candidates after Epic 17

These are direction, not scheduled T17 tickets, and do not block completion of
the protobuf/gRPC Atlas. Each claim family requires its own evidence-pack card,
extractor version, coverage semantics, validation plan, ADR, MANUAL update,
registry pin, dark flag, and PR-sized acceptance criteria.

- **HTTP:** separate OpenAPI declaration parsing from language/framework route
  registrations and from client-call extraction. `METHOD /normalized/path` is
  only a shared catalog key after template, mount, gateway, and middleware
  resolution states have been modeled; ambiguous joins abstain.
- **Kafka:** separate topic/schema declarations, producer evidence, and
  consumer evidence. A source literal topic name has no proven
  cluster/environment identity; consumer groups and dynamic configuration
  remain unresolved without an authorized deployment or registry connector.
  The UI is topic-centered—producers → topic/schema → consumers—not an
  endpoint metaphor.
- **Thrift:** separate declarations, server registrations, and client calls.
  `namespace.Service.method` and argument/exception/return shapes enter the
  Atlas only after a partner names the language/runtime combination and the
  resulting packs have executable acceptance bars.

## P5 hardening *(unscheduled — pull on demand)*

**T-P5.1 ✅ · `phebs backup` / `phebs restore` subcommands** — cold copy works
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

Implemented on `t-p5.1-backup-restore`: a lifecycle-owned private runtime
descriptor exposes only the healthy local child needed for live export; a
canonical `phebs-backup-manifest-v1` binds both executable digests, raw config,
database/store identities, classified inventory, and export bytes. Restore
verifies every binding before touching an absent/empty target, imports through
an undiscoverable isolated child, and validates the store. The acceptance test
also deletes all derived state and pins the normal reconcile → sync → index
startup chain rebuilding the shard without an operator reindex request.

## Deliberate non-goals *(per historical PORT_MAP §7/§12)*

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
