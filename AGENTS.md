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

`cmd/phebs/` · `internal/{api,auth,codenav,compat,config,extract,gitobj,indexer,mcp,recovery,search,servicecatalog,servicecatalogingest,store,sync}` ·
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
- **Parallel tracks (2026-08-07).** The scale track owns Epics 40–42 in this
  checkout on `codex/t4*` branches and owns `internal/`, `cmd/`, `spike/`, the
  store schema, `Makefile`, and `go.mod`. The presentation track owns Epic 43
  in `../phebs-ux` on `ux/t43.*` branches and owns `ui/`,
  `docs/DESIGN_CHARTER.md`, screenshot tooling, and retained design records;
  it is governed by `docs/DESIGN_CHARTER.md` and must not alter a scale plane,
  authority, claim, or caveat wording. Scale work must not modify `ui/` or
  `docs/DESIGN_CHARTER.md` without a flagged handoff. Shared spine docs
  (`PLAN.md`, `docs/BACKLOG.md`,
  `docs/ROADMAP.md`, `AGENTS.md`, `docs/BACKLOG_COMPLETED.md`, and
  `docs/README.md`) are append-only across tracks: edit only the owning epic's
  sections and ADR rows, never reflow the other track's text, and the second
  merger resolves conflicts and reruns `make docs-check` and
  `make verify-glossary`. Stage only explicit paths; never use `git add -A` or
  `git add .`, never stage the other track's files, and leave unfamiliar
  `ui/` or design-document changes uncommitted. Any boundary crossing must be
  identified in the ticket summary and wait for Ben's routing rather than
  crossing silently.
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

**Single-node implementation is complete for Epics 0–24, provisional
Workbench binding Epic 29, service-scope Epic 30, and bounded pipeline
observability Epic 31 (2026-08-04); Epic 32's multi-service v2 contract and
validation gate is also complete.** Search,
repository browsing, authentication,
permissions, audit/analytics, SCIP/history, stateless MCP, bounded `rev:`
indexing, and backup/restore are shipped core behavior. Contract Atlas, Caller
Map, Impact, Investigations/Workbench, Thrift-field, and Kafka evidence are
implemented but remain experimental/default-dark; the Workbench's provisional
store-derived binding creates no production registration. Epic 30's
service-scope program is complete: T30.1 recorded a focused-index spike GO
without production behavior changes; T30.2 added strict analysis-unit
configuration and committed state; T30.3 shipped manifest-bound focused
physical indexing and exact focused backup/restore; T30.4 shipped the reusable
streamed candidate manifest and fail-closed extraction admission; T30.5 shipped
exact focused evidence publication. The post-T30.5 whole-search and
focused-local candidate-replay issue gate closed on 2026-07-29. The separate
large-monorepo design review is complete: T30.6 remains the target-bound
caller-overlay umbrella, decomposed
across PR-sized tickets for operational receipts, durable outcomes, aggregate
scheduling, source-lane classification and consumption, catalog lifecycle and
materialization, leaf execution and complete publication, authorized
consumers, and retention decision and implementation. T30.6a bounded
operational receipts, T30.6b durable exact-generation outcomes, and T30.6c
aggregate-bounded domain scheduling are shipped; T30.6d candidate-v4
source-lane classification, T30.6e focused local-evidence base-lane
consumption, T30.6f resolver-catalog lifecycle, T30.6g bounded resolver
materialization, and T30.6h direct caller-leaf execution artifacts are also
shipped; T30.6i atomic complete caller-generation publication, T30.6j
authorized exact Caller Map reads, T30.6k exact caller comparison integration,
T30.6l exact Workbench Impact caller integration, and T30.6m explicit
unbounded historical-publication retention decision are shipped; T30.6n
bounded job-history reads and startup-migration repair, T30.6o's
authorization-first retention-status shell and capacity warning, and T30.6p's
21 core SurrealDB retention collectors are also shipped. T30.6q's exact
24-table Investigation/Workbench collector and T30.6r's bounded
derived-publication store/filesystem collectors are shipped, completing the
then-52-component retention-status surface; T40.7 adds the durable chunk
ledger as the 53rd component. T30.7 closes the epic with scope-aware
product surfaces, coverage-certificate v3 and durable domain receipts, exact
caller record/progress summaries, HTTP/MCP scope parity, and a neutral
ordinary-worker `make dev` cohort. Its post-review closure preserves retained
v1/v2 proof bytes, validates the real exact Caller Map envelope through MCP,
and keeps failure, explicit-gap, and zero/empty states visible. T31.1 bounded
pipeline diagnostics and T32.1's microservice-first program-contract closure
are complete. Repositories remain shared physical source/search generations
while many logical services become independent catalog, query, evidence, and
workflow scopes; T32.1 adds no runtime behavior or scale claim. T32.2's
authorized whole-monorepo baseline completed on 2026-08-04 with a source-free
receipt at `spike/t322/results.json`; it establishes no SLO and selects no
topology. T32.3's deterministic neutral service-authority/correctness corpus
and 1,000/5,000-service load profiles completed on 2026-08-04 without selecting
a production catalog schema or scale claim. T32.4's source-free topology spike
selected direct shared whole-repository shards for the initial v2 path on
2026-08-04; cohorts and P6 remain evidence-triggered escape hatches. T32.5
recorded a conditional implementation GO with explicit authority, identity,
migration, admission-limit, and deferral boundaries but no runtime or release
authorization. T33.1's strict `phebs-service-catalog-v2` contract and T33.2's
exact catalog ingestion, source-census binding, immutable store authority,
backup/restore, and v1 migration are complete. T33.3's service-local desired,
active, status, incarnation, tombstone, and bounded-summary state is also
complete. T33.4 supplies authorization-first paged HTTP/MCP inventory and
exact bounded service detail over that state; T33.5's accessible source-free
directory and neutral ordinary-worker demo close Epic 33. T34.1's immutable
repository source/search generation, T34.2's exact service-query compiler,
T34.3's fail-closed migration/recovery, and T34.4's shared All code/service
product and neutral demo close Epic 34. T35.1's bounded generation-scoped
scheduler, T35.2's pin-aware lifecycle decision, T35.3's bounded
sweep/capacity control, and T35.4's source-free recovery/operator demo close
Epic 35. T36.1's bounded immutable Git reader and partition contract,
T36.2's shared Go source-observation contract, T36.3's content-addressed
observation publication, and T36.4's authorized progress/neutral multi-pack
demo are complete, closing Epic 36. T37.1's ownership-neutral namespace-sharded
declaration/resolver catalog, T37.2's classified RPC caller postings,
T37.3's Kafka topic postings, and T37.4's service projections and atomic
relationship roots are complete; T37.5's exact readers, comparison,
proof/Workbench integration, and neutral demo are complete, closing Epic 37.
T38.1's exact selected-service overview, T38.2's source-first cross-service
explorer, T38.3's service-aware Impact/Workbench composition, T38.4's strict
MCP microservice parity, and T38.5's product closure and neutral demo are
complete, closing Epic 38. T39.1's neutral correctness, admission, and recovery
gate is complete. T39.2's authorized target run stopped honestly on one
terminal incremental pipeline failure and destroyed its derived custody;
T39.3's independent security/lifecycle matrix is complete. T39.4 stopped
before unsealing because its public evidence/workflow protocols remain
unsealed design drafts. T39.5 recorded no release, kept all packs
experimental-dark, and closed Epic 39; T39.R1 is conditional only before a
separately authorized rerun. T39.R1 subsequently closed the mirror-lock
contention prerequisite without authorizing or superseding that rerun. A later
unfrozen very-large-monorepo diagnostic is retained as source-free engineering
evidence, not a scale pass. Epics 40–42 are the scheduled scale-convergence
program: T40.1's closed refusal envelope/frozen neutral corpus, T40.2's
independent derived-plan ownership, and T40.3's source-partition super-root are
complete, T40.4's hierarchical observation inventory, T40.5's immutable
search-generation lifecycle, T40.6's observation recovery/lifecycle/archive
migration plus reverse-audit closure, and T40.7's constant-cost evidence-stage
accounting, T40.8's sparse candidate/partition contract, T40.9's bounded
partition-result/domain-root contract, T40.10's partitioned extraction and
atomic domain authority, T40.11's downstream adapters plus complete
recovery/lifecycle/archive ownership, and T40.12's authorized product-consumer
compatibility replay are complete; T40.13a's fail-closed process sampling and
T40.13b's cooperative cancellation/shutdown truth and T40.13c's durable
hard-death descendant supervision and T40.13d's shared custody-mutation and
immutable-admission lock and T40.13e's exact executed-tool identity closure are
also complete, and T40.13f is the next
prerequisite inside the T40.13 gate; Epic 40 targets
bounded derived-pipeline convergence for at least two million regular-file
physical owners; Epic 41 separately requires at least 8,000 accepted services
and measures a target of 10,000 accepted services; Epic 42 composes both
dimensions before any topology posture changes.
The current 4,000-service cap remains until its named measured ticket.
`docs/DESIGN_CHARTER.md` is the presentation authority; Epic 43, its
charter-gated presentation-only track, completed on 2026-08-08 (closure
record `spike/t431/CLOSURE.md`; residue queued as T43R.1–5 in the active
backlog) and touched no scale plane, authority, or claim. No private rerun or release is
authorized. Epics 25–28 remain unscheduled.
A physical Go-test search overlay,
test-source association, extractor expansion, automatic authority adapters,
and the distributed P6 profile remain separately reviewed future work.
GATE2-V2 remains `NOT_ESTABLISHED`; no numeric public-corpus accuracy,
completeness, migration-completion, or decommission-safety claim exists.

The public remote and hosted CI exist. `v0.2.0` is an immutable but unverified
historical tag; `v0.2.1-dev` is the current source line and may be tagged only
after its exact main commit passes the required hosted gate. P6 fleet work
remains demand-driven. Active sequencing lives in `docs/ROADMAP.md`; completed
tickets live in `docs/BACKLOG_COMPLETED.md`.
