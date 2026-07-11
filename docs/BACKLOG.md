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

**T2.2 · GitHub adapter (PAT)** ✅ 2026-07-09 *(fake-API tests; live PAT run pending)*
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

## EPIC 6 — Parity quick wins *(Wave 0 — days each, no architecture change)*

**T6.1 · Broaden syntax highlighting** — *Sourcebot free (100+ langs)*
Add the common CM6 language packs beyond the current ~6 (Rust, Java, C/C++,
C#, Ruby, PHP, SQL, HTML/CSS, YAML, shell, …) to `ui/src/lang.ts` and the
Lezer chunk tokenizer. AC: a file in each added language renders highlighted
in the viewer and in search-result chunks; bundle stays code-split (packs load
lazily). Moves "syntax highlighting" partial → have.

**T6.2 · File-tree navigation column** — *Sourcebot free (file explorer)*
Wire the deferred 240px tree column in `FilePage` over the existing
`/api/tree` + `/api/folder_contents` endpoints: collapsed siblings, expanded
path to the current file, active-row highlight. AC: clicking a directory
expands it; clicking a file navigates the viewer; deep-linkable. Moves "file
explorer" partial → have.

**T6.3 · Live GitHub PAT verification** — *closes a testing caveat*
Run the T2.2 GitHub adapter end-to-end against a real PAT (org/user/repo
listing, rate-limit handling, private-repo clone). AC: a private repo syncs +
indexes + searches; findings (if any) fixed; ADR notes the verified run.

**T6.4 · UI test harness (Vitest)** — *review gap: zero UI tests*
Add Vitest + Testing Library; cover the streaming reducer, keyboard nav
(j/k/enter/o and the collapse guard), facet toggling, and SSE error handling
(the crash and error-clobber bugs would have been caught). AC: `npm test`
runs in CI; the streaming/keyboard/facet paths have tests.

## EPIC 7 — Connectors & freshness *(Wave 1 — the biggest free-tier gap)*

**T7.1 · GitLab connector** — *Sourcebot free*
`type: gitlab` connection (PAT): list by group/user with include/exclude
filters, rate-limit-aware pagination, §5 metadata, authenticated clone.
Closest analog to the GitHub adapter. AC: a GitLab group syncs + indexes;
metadata persisted; incremental refetch.

**T7.2 · Gitea connector** — *Sourcebot free*
`type: gitea` connection (PAT + base URL for self-hosted). AC: a Gitea org
syncs + indexes. *(Bitbucket Cloud/DC, Azure DevOps, Gerrit follow as
T7.x by demand — same adapter shape.)*

**T7.3 · GitHub App auth** — *Sourcebot paid/EE — OSS in phebs*
`ghinstallation` installation-token auth as an alternative to PATs (higher
rate limits, per-install scoping). AC: an App-authed connection syncs; tokens
refresh; falls back cleanly if not configured.
Deps: T2.2.

**T7.4 · Webhook-driven reindex** — *Sourcebot paid/EE — OSS in phebs*
`POST /api/webhook` (GitHub push/repository events, HMAC-verified) →
event-driven sync/reindex of the affected repo. AC: a push webhook makes the
change searchable without waiting for a poll; signature rejection tested.
Deps: T7.3.

**T7.5 · Periodic re-sync cadence** — *Sourcebot free (auto-freshness)*
Debounced periodic re-sync for remote connections (config interval), on top
of boot-time + watch + manual. AC: a remote repo's new commits become
searchable within the configured cadence without restart; monorepo debounce
respected. Moves "periodic re-sync" partial → have.

## EPIC 8 — Differentiators: paid features, OSS in phebs *(Wave 2 — high value)*

**T8.1 · Search contexts** — *Sourcebot paid/EE — OSS in phebs*
`search_context` table + `context:<name>` pre-pass compiled to a `query.RepoSet`
(the hook already exists in `internal/search/query.go`); contexts defined in
config. AC: `context:foo` restricts results to the named repo set; table-driven
tests; CRUD or config-defined contexts documented.

**T8.2 · MCP server** — *Sourcebot paid/EE — OSS in phebs; flagship differentiator (PLAN P4)*
Official `modelcontextprotocol/go-sdk` server exposing `search_code`,
`read_file`, `list_repos` over the existing search/source/store internals,
token-auth. AC: the server is reachable from Claude Code; each tool returns
correct results against a fixture corpus; MANUAL documents setup.
Deps: T8.1 (contexts usable as a search scope).

**T8.3 · MCP integration + polish**
Streaming/large-result handling, error surfaces, and a MANUAL section with a
copy-paste Claude Code config. AC: a real agent session searches + reads files
end-to-end.

## EPIC 9 — Auth & code navigation *(Wave 3 — heavier lifts)*

**T9.1 · DB-backed users, sessions, multiple API keys** — *Sourcebot free (login/members)*
Activate the reserved `user`/`api_key` tables: scs sessions, hashed multi-key
auth, a minimal login. UI attaches the bearer token. AC: a user logs in; keys
are created/revoked; the UI no longer relies on an open API.

**T9.2 · OIDC / SSO** — *Sourcebot paid/EE (enterprise SSO)*
`coreos/go-oidc` login, no seat gating (SAML only on demand). AC: OIDC login
against a test IdP; sessions bridge to the T9.1 model.
Deps: T9.1.

**T9.3 · Code navigation (SCIP)** — *Sourcebot paid/EE (Pro code nav) — PLAN P4*
Beyond base `sym:`: ingest SCIP indexes (Apache-2.0) for precise
go-to-definition / find-references / hover. AC: def/ref/hover on a fixture repo
with a committed SCIP index; graceful when absent.

**T9.4 · Git history / blame / commit / diff** — *Sourcebot free (history + blame)*
`/api/blame`, `/api/commits`, `/api/commit`, `/api/diff` off the existing bare
mirrors via git plumbing (`gitread.go`), plus viewer surfaces. AC: blame lines
map to commits; a commit's diff renders; path-safe like the other read
endpoints.

## EPIC 10 — Enterprise surface *(Wave 4 — build-our-own, PORT_MAP §12)*

**T10.1 · Audit log** — *Sourcebot paid/EE*
Append-only SurrealDB table + huma middleware recording admin/user actions;
retention config. AC: mutating actions land in the log; a read endpoint/page
lists them; near-zero overhead.

**T10.2 · Analytics** — *Sourcebot paid/EE*
Local usage events + aggregations, one minimal dashboard page. **Zero
telemetry** (deliberate divergence from upstream's phone-home). AC: search
volume / top repos render from local data only.

**T10.3 · Permission syncing + permission-aware search** — *Sourcebot paid/EE; the durable moat*
Mirror code-host ACLs into repo↔user edges; compile a per-user `RepoSet` at
query time (the hook is reserved in the search pre-pass — native, not
post-filter). AC: a user sees only repos their code-host grants; no results
leak across the boundary; the filter is applied in the query, not after.
Deps: T9.1.

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
