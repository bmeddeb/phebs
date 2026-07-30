# AGENTS.md — phebs

Self-hosted code search in one Go binary. Ground-up, **reference-only** port of
Sourcebot: zoekt in-process, SurrealDB 3.0, huma OpenAPI, Vite + React +
CodeMirror 6 UI embedded via `go:embed`. Pronounced "febz".

## Source of truth (read before working)

- `PLAN.md` — architecture + dated ADR bullets. Every decision lands here as an
  ADR bullet **in the same PR** as the change. No other design docs.
- `docs/PORT_MAP.md` — removed 2026-07-12 (historical; see git history).
- `docs/BACKLOG.md` — epics + PR-sized tickets. Work proceeds in ticket order;
  branch names carry ticket IDs (e.g. `t1.3-job-claim-spike`).
- `docs/ROADMAP.md` — current posture and sequencing; completed tickets live in
  `docs/BACKLOG_COMPLETED.md`.
- `docs/MANUAL.md` — the user-guide index. Behavior changes update the owning
  task guide under `docs/guides/` in the same PR.
- `docs/README.md` — documentation map; the adoption suite
  (VISION/INVESTIGATIONS/PITCH/PILOT_CHARTER/EVIDENCE_PACK_CARD) lives in
  docs/ and must stay mutually consistent: no doc expands the ask of the
  one above it.

## Stack

Go (latest stable, 1.26 line) · `github.com/sourcegraph/zoekt` as a library for
**serving** (`query.Parse`, `shards.DirectorySearcher`); index **builds** via a
child `zoekt-git-index` compiled from the same go.mod SHA (OOM isolation) ·
SurrealDB 3.0 as a **supervised local child** (`surrealkv://`, official Go SDK
over WS — no embedded Go engine, 2026-07-09 ADR) for state **and** job queues
(jittered polling — no Redis, no BullMQ; server mode only in the P6 fleet
profile) · huma v2 for the API (OpenAPI free) · exec `git` for clone/fetch into
bare repos · Vite + React + TS + CodeMirror 6 in `ui/`, embedded in the binary.

## Layout

`cmd/phebs/` · `internal/{api,auth,codenav,compat,config,extract,gitobj,indexer,mcp,recovery,search,store,sync}` ·
`ui/` · `docs/` · shards at `$DATA/index`, bare repos at
`$DATA/repos/<host>/<path>.git`

## Conventions

- PR-sized, stacked changes; one ticket per PR; ACs in BACKLOG.md are the merge bar.
- `main` is the integration branch. Ticket worktrees and branches are temporary:
 remove them after a verified fast-forward merge; retain unmerged validation
 lineages until an explicit archival decision.
- **Agents never merge into `main` without Ben's explicit request** (2026-07-22).
 Passing the merge bar authorizes a merge *request*, not the merge itself;
 completed ticket work stays on its ticket branch until Ben says to integrate.
- Table-driven tests. Every epic ends demoable via `make dev` — an epic that
  can't be shown end-to-end is not done.
- Every implementation review includes a steady-state-cost pass: enumerate work
  performed per query/request, sync tick, startup/restart, retry/no-op, and
  publication transition; identify held locks, repeated full-corpus/shard
  reads or hashing, cache invalidation, concurrency bounds, and worst-case
  memory/disk/child-process cost. Green functional gates do not replace this
  pass.
- golangci-lint clean. `context.Context` first param. Errors wrapped with `%w`,
  classified at boundaries (T3.3 taxonomy).
- HEAD is the default and authoritative revision. T10.4 may add at most seven
  explicit branch/tag revisions per repository for `rev:` search; extraction,
  SCIP defaults, coverage, and proof bundles remain HEAD-bound. Single-tenant
  posture; the per-user RepoSet hook in the search pre-pass is
  `Searcher.Visible` (T10.3), enabled only when the config has a `permissions:`
  block.

## Hard rules

- **Never open, copy, or paraphrase upstream Sourcebot source.** Behavior, docs,
  and API shapes are the only reference. Never read any path under `ee/` in the
  upstream repo under any circumstances. Upstream is FSL-1.1 + proprietary ee/;
  phebs is Apache-2.0 (confirmed, T0.2) and must stay uncontaminated.
- Depend on upstream `github.com/sourcegraph/zoekt`, not the sourcebot-dev fork.
- No employer code, credentials, hosts, or infrastructure. Personal project,
  personal hardware.

## Current state

**Single-node implementation complete through Epic 23; documentation Epic 24
and provisional Workbench binding Epic 29 are complete (2026-07-28).** Search,
repository browsing, authentication,
permissions, audit/analytics, SCIP/history, stateless MCP, bounded `rev:`
indexing, and backup/restore are shipped core behavior. Contract Atlas, Caller
Map, Impact, Investigations/Workbench, Thrift-field, and Kafka evidence are
implemented but remain experimental/default-dark; the Workbench's provisional
store-derived binding creates no production registration. Epic 30 service-scope
work is in progress: T30.1 recorded a focused-index spike GO without production
behavior changes; T30.2 added strict analysis-unit configuration and committed
state; T30.3 shipped manifest-bound focused physical indexing and exact focused
backup/restore; T30.4 shipped the reusable streamed candidate manifest and
fail-closed extraction admission; T30.5 shipped exact focused evidence
publication. The post-T30.5 whole-search and focused-local candidate-replay
issue gate closed on 2026-07-29. T30.6 remains paused pending a separate design
review of the operator's large-monorepo results and proposed directions.
GATE2-V2 remains `NOT_ESTABLISHED`; no numeric public-corpus accuracy,
completeness, migration-completion, or decommission-safety claim exists.

The public remote and hosted CI exist. `v0.2.0` is an immutable but unverified
historical tag; `v0.2.1-dev` is the current source line and may be tagged only
after its exact main commit passes the required hosted gate. P6 fleet work
remains demand-driven. Active sequencing lives in `docs/ROADMAP.md`; completed
tickets live in `docs/BACKLOG_COMPLETED.md`.
