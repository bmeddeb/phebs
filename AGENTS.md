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
also complete; T40.13f's hermetic execution controls, T40.13g's authenticated
returned-evidence firewall, T40.13h's resumable seal/keypair integrity, and
T40.13i's bounded exact-control inspection, T40.13j's overflow-safe ceremony
arithmetic, T40.13k's complete executor-admission accounting, and T40.13l's
cost-first operator gates are complete; T40.13m's bounded executable-image
epoch accounting and V26 evidence fence are also complete; the exact-main
`t40r1-neutral-36` ceremony then stopped honestly at
`interruption/restart_start` with generic unsubstantiated
`failed_phase_measurement_unavailable`, completed teardown without retained
derived or scratch-source custody, and established neither a pipeline failure
nor a scale pass. T40.13n's V27 coherent restart accounting, path-free typed
data-gauge diagnostic, and settled-schedule recovery repair are implemented;
the corrected bounded real-binary readiness rehearsal and exact-tree package,
race, launcher, internal/store, module, vet, lint, docs, glossary, and shell
gates passed. Independent review of exact source commit
`b5d6b74da8644811c5e1bfffd658b73661797ee2` found the code clean and one low
cost-record issue, corrected in source-identical documentation: exact
mismatched-current retirement invents no partition, and its pre-retirement
schedule reads are now stated separately from nonzero enqueue. An extra
uncached repository run was baseline-red only on inherited
T30.6m/T32.3/T32.4 retained-artifact assertions reproduced unchanged at the
base commit; no T40.13n or `internal/` package failed. The original T40.13 gate
then advanced through one integrated exact-main ceremony:
`t40r1-neutral-37` stopped after 317.565 minutes at
`interruption/partial_verification`, after its selected lease proved `requeued`,
and completed exact clean teardown. Its controlling signed attribution is
`recovery/direct_recovery_failed/p6_investigation/substantiated`; V27 cannot
identify the retained owner/kind or distinguish timeout from scanner failure.
T40.13o's typed-nil accounting guard, marker commit-point correction, bounded
startup retirement plus policy-preserving extraction-stage lifecycle, and V28
retained-partial owner/kind evidence are implemented. The pre-review tree passed
targeted, package, race, real-launcher, readiness, complete `internal/`,
standalone `internal/store`, module, vet, lint, docs, glossary, shell, and
whitespace gates. One complete readiness
attempt was invalidated only by host-native sampler `EPERM` after healthy
structural startup; no process survived, its diagnostic root is retained, and
the one bounded structural rerun passed. Startup deletes no stage and inventories
at most 4,096 regular plus 4,096 sparse repositories; either startup or one
lifecycle turn accepts at most 20,000 direct entries from one selected
repository directory plus one overflow sentinel. Scheduled lifecycle alone
applies the 24-hour/newest-two promotion policy and drains collecting residue.
Startup checks cancellation before each raw-stage preflight and rename and
syncs any completed prefix before returning. New-generation creation adds one
serial result-directory sync per accepted domain (zero to 64); reuse/no-op adds
none, while an absent-generation rebuild repeats that bounded work.
Startup and each scheduled stage turn each acquire the existing shared
lifecycle-mutation lock once for their complete bounded pass; the scanner's
eight-descriptor budget excludes that already-existing lock descriptor. The
new-generation syncs extend the existing one-of-64 reconciler shard-lock hold.
Independent review of exact commit `704c2360e75e8a7d7068cbf3cd49b492a84cb50d`
reported critical 0, high 0, medium 1, and low 1: the startup cancellation gap
and omitted per-domain sync cost above. Both are corrected. Twenty cancellation
repetitions plus extraction normal/race, lifecycle, command, vet, lint, docs,
glossary, shell, format, and whitespace pass. Two corrected-tree structural
confirmations were invalidated by repeated host-native sampler `EPERM` after
healthy `http_ready`; both diagnostic roots remain, no process survives, and no
third retry was attempted. Fresh review of exact corrected source commit
`710f66f440464c4dabf1723f98134cb941c07232` found critical/high/medium 0 and
one low lock-cost wording gap. Source-identical documentation commit
`c4dfdabbd594b5f841b92058923343382d6cf5aa` corrected it and passed exact
re-review with all severity counts zero. No code-review finding remains; only a
later host-clean structural confirmation remained before integration readiness.
That confirmation, integration, and exact-main preparation led to
`t40r1-neutral-38`, which passed preflight and the 79m56.010s cold phase at
source `b79406d12f517caed08f07120ca91b0ac1fbe471`, then stopped honestly in
phase 3 `warm_noop`: the V28 phase meter sampled the same two Git lifetimes as
its healthy startup and rejected them despite unchanged authority, zero index
and publication movement, and positive reuse. Teardown retained no derived or
scratch-source custody. T40.13p's prospective V29 contract requires the whole
warm phase Git count to equal its paired healthy-startup count, while retaining
exact snapshot, index, publication, authority, and reuse gates; V1–V28 remain
exact. The small real-binary readiness path now calls production `warmNoop`.
The corrected content passed 20 V29 repetitions, complete package/race,
real-launcher custody, all 69 non-store `internal/` packages, standalone
`internal/store`, module, vet, lint, docs, glossary, shell, and whitespace.
Independent review of exact commit
`06b6e61e2316b33b5cad326e9efa2c9b97194309` found critical, high, medium, and
low counts all zero. One pre-review structural rehearsal passed the production
warm boundary with startup Git children 3 and phase Git children 3. The final
content's first confirmation stopped before launch on module-network timeout;
its one unchanged retry reached healthy `http_ready` and then met the existing
sticky Darwin sampler `EPERM`. Both diagnostic roots remain, no process
survives, and no further retry was attempted. A later host-clean complete
exact-commit readiness pass remains mandatory before integration readiness.
Neutral-39 then returned status 1 with source-free package
`sha256:681aef5bb4ebe77c63ed564f5dfe499609a76738c3172b7a58e9c9f87d6a43cb`.
Its wrapper monitor last showed `prepare/admission` with unknown custody age and
then failed only its terminal-summary `jq` quoting; no raw private cause
survived, so the run establishes neither sampler `EPERM` nor a named phase
failure. T40.13q's prospective V30 contracts retain valid startup
log/health/stage evidence beside an unavailable process sample, mark its zero
process counters with explicit `process_sampling_unavailable=true`, type a
single process-only or allocation-only measurement stop while keeping mixed
failures generic, and make warm restart incrementally track phase-local candidate and job
lifecycle reports within the existing revalidation deadline. A `done` reuse
decision of `warm_noop`, `cold_reuse`, or `marker_recovery` is necessary but not
sufficient: released, failed, or requeued work rejects, while claimed, started,
deferred, and yielded work remains unresolved until the same observed job later
reports `event=done,outcome=success`; exact authority is re-inspected on each
attempt; and one existing five-second convergence interval must add no new
candidate/job report after every job resolves. The phase meter finishes once at
that settled boundary. Its single finished `PhaseMetrics` process snapshot
refreshes the paired startup process counters without a second sampler read,
while log/stage/wall facts refresh there. The atomic warm→delta handoff
transfers that process boundary with the exact log EOF and performs a bounded
post-reset tail scan: any candidate/job report or partial tail refuses the
boundary, complete unrelated lines advance the warm EOF, and later exact
claimed/started reports remain delta, so the paired Git-count oracle still
rejects post-boundary Git. V30 ceremony servers enable synchronous
exact candidate/job sinks for all seven runners; any encode, cap, sink, or panic
failure latches cancellation and a nonzero terminal error after worker join,
while ordinary runtime reporting stays advisory. V30 phase 7 requires coherent
sorted post-ballast owner attempts, one final exact-normal capacity observation
after the latest owner, and then unchanged protected authority; it neither
claims capacity stayed normal throughout that owner cycle nor waits for hourly
idle eligibility. Independently, production lifecycle recovery requires
exact-normal capacity immediately before and after the sorted cycle-start owner
and one wholly normal, error-free, drained sorted cycle before hourly idle. V30
restore comparison retains caller generation, relationship
semantic digest, and all product content except indexed commit and the
replaceable relationship generation/root digest. Phase 9 restarts and requires
all 14 owners fresh and `state=ok`; the 13 non-`durable-jobs` owners must be
exact and drained, while `durable-jobs` truthfully remains `lower_bound` and may
retain backlog because live writers preclude an exact oracle. Capacity must be
exact after the latest owner and current stable authority unchanged. It records
bounded owner counters but does not claim
eligible deletion or individually live-prove rollback, lease, marker, or
store-pin roots, which remain mandatory lifecycle/store/publication regression
gates. Restored readiness uses the same fresh-cycle rule and comparator;
V1–V29 remain exact. Only the independent production idle-recovery path has the
28/64 turn bound: while capacity stays exact-normal and every owner result is
error-free and drained, a pressure transition costs a cursor suffix plus one
full cycle—at most 28 selected turns for 14 owners and at most 64 under the
32-owner bound—at the existing five-second cadence. Any owner error/backlog or
unavailable/non-exact capacity removes that bound and keeps the runner at five
seconds. The phase-7 and phase-9 waiters make no 28/64 claim and remain governed
by their fixed phase deadlines. Healthy normal hourly state and query, sync,
retry/no-op, publication, lock, cache, schema, memory/disk, and child costs
remain unchanged. Ordinary startup adds one closed exact-control environment
lookup and branch; when absent it allocates no report channel or sink and adds
no persistent work. Only V30 exact-control server mode adds synchronous
per-report log writes for candidate output and the seven job runners; ordinary
steady-state reporting remains advisory. The warm
harness adds incremental phase-local parsing, exact-authority reinspection per
attempt, one existing five-second quiet interval, a single finished-metrics
startup refresh with no second process sample, a bounded post-reset log-tail
scan, and an atomic process/log-EOF handoff under the unchanged revalidation
deadline. Phase 7 adds one existing post-recovery protected-authority
inspection. These add no production request, job, or child. Ceremony phase 9
adds one restart and bounded 14-row evidence whose validation applies 13
exact/drained predicates and one durable-job lower-bound predicate. Truthful
`durable-jobs` backlog does not block either V30 lifecycle waiter; an owner error
or backlog in one of the 13 required drained rows keeps owner progression at the
five-second cadence until the fixed phase deadline. Its pre-review focused and
bounded regressions, complete package/race, real-launcher custody,
production-path readiness, every `internal/` package including full store,
module verification/compilation, vet, repository-pinned lint, documentation,
glossary, shell, and whitespace gates passed on 2026-08-25. Independent review
of exact commit `4b40beb28e1549a4d269a7a7e0d9ed604c775c4b` recorded 0 critical,
0 high, 3 medium, and 3 low findings. The correction tree now latches any
non-exact capacity sample through its whole lifecycle cycle, refuses exact-mode
stale reap mutations/errors, preserves same-phase process-sampling evidence
through review-ceiling teardown, reuses the settled warm cursor, allocates no
absent exact-report state, and names all fourteen lifecycle owners. Exact
correction commit `ec4f2500d1b68dcbe539667d5833fdf694bc5adc` passed the complete
machine gate, but re-review recorded 0 critical, 0 high, 0 medium, and 2 low
findings: a settled warm cursor was not deterministically closed on two
pre-confirmation failure paths, and two owning cost sections contradicted the
startup lookup record. The next correction keeps that exact FD under an
unconditional phase-meter finish cleanup and removes the contradictory wording;
focused normal/race and documentation checks pass. A new immutable commit,
complete exact-tree gates, and independent review remain pending. Integration,
exact-main preflight, freeze, and execution are not yet passed or authorized.
Final exact source commit `50df638ad065814f4a9ea75c4f7493a622df3de0`
closes the cleanup-only safety-metric loss and passed fresh independent review
with critical/high/medium/low all zero. It passed the complete T40.13 package
(106.392s), full race package (134.820s), real-launcher custody proof (61.472s),
command package (19.006s), module verification/compilation, vet, pinned lint,
documentation, glossary, shell, and whitespace gates. Readiness remains open:
one complete rehearsal reached healthy structural `http_ready` and then retained
the deliberately sticky Darwin root-sampler `EPERM`; semantic (302.46s) and
stale-worker (31.04s) passed. One bounded unchanged confirmation reproduced the
same structural refusal, so no third attempt was made. Retrying a denied root
could miss a short-lived descendant and remains forbidden. No rehearsal PID or
session survives; the second diagnostic root is retained. The exact full
`internal/...` command separately timed out in `internal/store` (1320.596s)
while opening a fresh schema; that exact isolated subtest then
passed in 11.349s, every completed internal package passed, and no SurrealDB
child survives. A later host-clean complete readiness and full internal/store
pass are mandatory before integration can be requested.
T40.13r supersedes those two remaining-host-gate statements. A later serial
exact-candidate `internal/store` run passed in 1003.037s. Retained readiness
custody proves the Darwin-denied PID 554 was a still-parented descendant, not
the Phebs root, but did not retain its executable identity. A bounded live-host
reproduction establishes the causal mechanism without inventing that missing
field: the 50-ms production compatibility monitor launched setuid-root
`/bin/ps`, for which coherent task-all-info returned the same `EPERM`; Darwin
ceremony session custody separately retained another `ps` launch that could
recreate the class under admission accounting. T40.13r removes both Darwin
helpers through bounded native process records while leaving the sticky
fail-closed ceremony sampler and V1-V30 evidence exact. Focused changed-path
gates, an immutable commit, fresh independent review, and one host-clean
exact-commit readiness rehearsal remain required before integration can be
requested.
Exact implementation commit `9bee810cd692d831993ff2e4784fb067f628b768`
then passed the focused native/custody repetitions, changed-package normal and
race gates, module verification, vet, pinned lint, documentation, glossary,
shell, and whitespace gates and received independent review with
critical/high/medium/low all zero. Its unsandboxed host-clean structural
readiness rehearsal crossed the formerly denied startup boundary and passed in
373.567s with exact clean teardown. The serial `internal/...` run reached the
default ten-minute package alarm while `internal/store` was opening a fresh
engine; every completed package passed, the exact standalone full
`internal/store` package then passed under its 30-minute allowance in 993.780s,
and no SurrealDB child or port-65499 listener survives. These close T40.13r's
code-review, readiness, and full-store blockers and make the source-identical
branch eligible for a separate integration request; they do not authorize a
merge, exact-main preflight, identifier selection, freeze, or execution.
Independent review of that source-identical gate record found one medium
preservation gap: structural alone did not comprise complete readiness. The
remaining `semantic` and `semantic-stale-worker` legs then passed together in
390.868s without changing any built source; the earlier 373.567s structural
result is preserved across the documentation-only commit because no compiled,
embedded, fixture, or harness input changed. All three readiness legs are now
green for the exact implementation bytes, no rehearsal process or port-65499
listener survives, and fresh exact-HEAD documentation re-review remains the
final branch-close check.
The integrated `t40r1-neutral-40` ceremony then stopped honestly in phase 6
with retained `observation_publication` / `stage_directory` custody. T40.13s
fixes the exact same-generation live-stage residue and the V29/V30
retained-partial sealing rejection. Exact implementation commit
`0e5eba0109e632b9a1bd8f24c9f876aca5146e68` passed its affected normal/race,
focused repeated, vet, documentation, glossary, and whitespace gates. Its
exact-clean real-binary semantic rehearsal passed phase-6 interruption/restart,
partial-state clearance, backup/restore, restored lifecycle, and authorized
query in 295.38s of subtest time and 456.36s total including private tool
builds for the top-level rehearsal test; the package command completed in
456.991s. Successful cleanup removed the temporary workspace and no matching
process remains. Phases 7–11 and a full ceremony remain unestablished and must
be reviewed independently before a fresh freeze.
The first focused Phase-8 pressure rehearsal then reached real `collect` and
ballast removal but expired while the observation-v2 owner still had backlog;
its reviewed detached image was purged. Retained full structural generations
contain 1,546/1,547 deletion units and require 97 observation-v2 turns at the
unchanged sixteen-delete cap, so one-second fair rotation would still exceed
the fixed ten-minute recovery deadline. The pending correction keeps ordinary
backlog/error/capacity retry at five seconds and healthy idle at one hour, while
only `collect`/`refuse` or the existing pressure-recovery latch caps the serial
turn delay at 250 milliseconds. V30 predicates, fair order, and per-turn limits
remain exact. That post-sweep delay offers at most four turn starts and four
existing capacity probes per second before sweep duration, plus the existing
timer allocation and in-memory status update; it does not claim four completed
turns. This provides deterministic scheduler headroom, not a Phase-8 or
full-ceremony pass; focused rerun and exact-tree review gates remain required.
The separately run human Phase-7 `semantic-stale-worker` selector passed in
32.22s for the subtest, 197.17s for the top-level readiness test, and 197.835s
for the package command. Its retained terminal record does not bind exact HEAD
and clean-checkout proof, so exact candidate attribution remains open. The
corrected Phase-8 wrapper then passed from exact clean commit
`37fba2896f500104fa8283914ed19b8a003e3a24`: its subtest took 252.48s, the
top-level readiness test 310.35s, and the package command 311.030s. That commit
is integrated into `main`; a later host check found no matching diagnostic path
or live rehearsal/Surreal process. These focused results supersede the earlier
Phase-7/8 run requirements only; prior-phase handoff, full-scale custody,
release, Epic closure, and scale/SLO claims remain open.
The dedicated human Phase-9 `archive_restore` rehearsal now calls the production
coordinator after a small structural A→B→A-return. Its first working-tree run
passed in 88.70 seconds for the subtest and 147.917 seconds for the package
command, with exact meter/server accounting, successful shutdown, no retained
current-run workspace, and no matching process. Older retained readiness roots
remain untouched. Because the run preceded its immutable implementation commit,
an unchanged exact-clean-commit rerun remains required before candidate
attribution. Human Phase 10 collection and every broader ceremony, release,
Epic-closure, and scale/SLO claim remain open.
The unchanged human Phase-10 selector subsequently passed from exact clean
commit `15487bbf15b602b04d81fbae6b989777b5cac44d`: its subtest took 147.89s, the
top-level readiness test 205.45s, and the package command 206.121s. Successful
cleanup retained no diagnostic root and left no matching process. That
commit was integrated into and pushed as `main`. The Phase-10 exact-rerun
requirement is closed, but the earlier Phase-9 exact-clean rerun remains
formally open. A separately opt-in human Phase-11 `authorized_query` rehearsal
now drives the unchanged production coordinator over converged semantic-A and
structural-A-return profiles, with real restart, fixed query/citation oracles,
authority revalidation, exact metering, and shutdown. It adds no production
code and no sparse image, ballast, archive, or fixed lock. Its exact-clean run,
all broader ceremony/custody evidence, release, Epic closure, and scale/SLO
claims remain open.
The unchanged Phase-11 selector then passed from exact clean commit
`c2e6eed8faab01854f3af94264ec3054487c877e`: its subtest took 44.46s, the
top-level readiness test 104.93s, and the package command 105.614s. Both exact
authority waits, all fixed query/citation oracles, exact control-read/two-meter
accounting, the mandatory query-member minimum, listener transfer, restart,
and shutdown passed. Successful cleanup retained no current-run diagnostic root
and left no matching process. This closes only the focused Phase-11 exact-run
requirement; Phase-9 exact-clean attribution, prior-phase handoff, a complete
ceremony, release, Epic closure, and scale/SLO claims remain open. Ben
authorized fast-forward integration after the source-identical result record,
superseding only the following historical integration-open wording; exact-main
preflight, freeze, execution, and push remain separate.
A separately opt-in human Phase-12 rehearsal now exercises the unchanged V30
`teardown` coordinator against one live supervised structural Phebs/Surreal
session and a receipt-valid source-free completed-prefix fixture. It holds the
real run-root lock, recursively deletes only a new private `custody/` child,
retires the named external protocol artifacts, and requires checkpoint
ordering, durable absence, terminal observation publication, completed receipt
validation, sibling preservation, and lock release after the simulated Execute
return. Successful test cleanup then removes the private parent. It adds no production code. The
fixture does not replay or prove Phases 1–11, full-scale or signed custody, a
complete ceremony, release, Epic closure, or a scale/SLO claim. Complete gates,
independent review, an immutable commit, and an unchanged exact-clean human run
remain required before the next full ceremony is planned.
The first exact-clean attempt at commit
`cbbb873d251b56c0a2cd645ab02c99ee3a60d90a` stopped before prepared
publication, supervision, or supervised Phebs/Surreal server launch because its
synthetic prepared copies retained bounded projection-profile names instead of the frozen ceremony
identities. Review found no matching process and purged the retained 207 MiB
temporary root. The correction maps only the manifest copies through the
existing frozen constants and adds a fast schema invariant; production
validation and authored profile bytes remain unchanged. At that point, a
corrected immutable commit, complete bounded gates, re-review, and exact-clean
run remained pending.
The corrected selector then passed unchanged from exact clean commit
`81d0a7a73214dbfa906e01eb3a8d611e8e950b2a`. It logged that exact source
commit and `teardown custody retirement boundary passed`; the test took 87.79s
and the package command 88.348s. Graceful session shutdown, both nonzero data
gauges, terminal checkpoint retirement, durable custody absence, terminal observation
and completed-receipt validation, supervision/prepared/checkpoint retirement,
sibling preservation, lock lifetime and reacquisition, frozen-host validation,
and all three clean-checkout boundaries passed. Successful cleanup left no
matching temporary root or process. This closes only the focused Phase-12
exact-run requirement. The exact package gate separately passed the
deterministic checkpoint-before-delete ordering regression. At that point, the
Phase-7 and Phase-9 exact-clean reruns, prior-phase handoff, a complete ceremony,
release, Epic closure, and scale/SLO claims remained open; this source-identical result record authorizes none of the
separate integration, preflight, freeze, execution, or push actions.
The unchanged Phase-7 `semantic-stale-worker` selector subsequently passed
under fixed-HEAD and clean-worktree guards at exact commit
`ce6212974f40fc452a124345c751a2b5bd473f9f`. It emitted the required boundary
marker; the subtest took 32.52s, the top-level readiness test 91.40s, and the
package command 92.025s. Pending and HTTP-409 lines were bounded nonterminal
convergence observations superseded by the pass. Successful cleanup removed
the current workspace, and a separate check found no matching rehearsal
process. This closes only Phase-7 exact attribution. Phase-9 exact-clean
attribution, prior-phase handoff, a complete ceremony, release, Epic closure,
and scale/SLO claims remain open; the source-identical record grants no
integration, preflight, freeze, execution, or push authority.
The unchanged Phase-9 `structural-archive-restore` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`0d4cd82132bca5a0c48d1d1df9e377a0720c4bb9`. It emitted the required
archive/restore boundary marker; the subtest took 89.04s, the top-level
readiness test 148.55s, and the package command 149.292s. The final active
relationship-progress row was a bounded nonterminal convergence observation
superseded by the pass. Exact two-meter/two-server accounting, shutdown, and
successful current-workspace cleanup passed; a separate check found no
matching rehearsal process. This closes Phase-9 exact attribution and the
separately identified Phase-7/9 reruns. Cross-phase handoff, a complete signed
ceremony, release, Epic closure, and scale/SLO claims remain open. Integration,
exact-main gates, fresh-ID selection, freeze and frozen-plan review, execution,
and push remain separate actions requiring their own authorization.
Exact main then advanced the permanent consumed-identifier fence through
neutral-40. The first prospective host preflight passed clean-checkout,
toolchain, memory, ports, and module checks but refused before freeze because
138.13 GiB available did not satisfy the 168.69-GiB V30 projected minimum. A
separate read-only review proved neutral-40 process, listener, mount, and lock
absence and reverified its frozen plan digest and signature. After Ben's
explicit approval, only its whole 45.61-GiB `custody` directory was durably
removed; signed evidence, prepared authority, supervision state, operation
lock, ceremony root, and signing key remain. The preserved freeze envelope has
no sealed observation or receipt, so the phase-6 cause remains reviewed
repository/terminal attribution rather than signed ceremony proof. This purge
establishes no pass or readiness result. A fresh exact-main preflight and an
unused neutral-41 freeze are authorized separately; execution is not.
The first post-purge prospective preflight then refused the other side of the
same V30 pressure contract: 197,643,706,368 available bytes left more pressure
ballast than could fit after the frozen 72-GiB pre-pressure projection inside
the 96-GiB custody ceiling. On this exact 494,384,795,648-byte volume, the
admissible available-space window is 181,130,218,415 through
194,540,402,299 bytes. An owner-only 7,000,000,000-byte logical
(7,013,421,056-byte allocated) reservation now lives beside all run roots as
`t40r1-neutral-41.host-pressure-reservation.bin`; it is neither custody nor
evidence. With it present, exact-main prospective preflight passed after
reporting 190,598,098,944 available bytes. Keep the reservation unchanged
through execution and source-free package review, then retire it only after a
separate terminal disposition. Its size is host-specific; never copy it in
place of rerunning preflight. This workaround changes no frozen bound and
establishes no ceremony result. Neutral-41 freeze is authorized; execution is
not.
The original T40.13 gate remains open; integration, exact-main preflight,
fresh-ID selection, freeze, and execution remain separately authorized. Epic 40
targets
bounded derived-pipeline convergence for at least two million regular-file
physical owners; Epic 41 separately requires at least 8,000 accepted services
and measures a target of 10,000 accepted services; Epic 42 composes both
dimensions before any topology posture changes.
T40.13r clarifies that “remain separately authorized” grants no present
authority: integration, exact-main preflight, fresh-ID selection, freeze, and
execution each require a later explicit authorization.
That later authorization is now present only for the completed integration and
push plus exact-final-source preflight, fresh neutral-41 selection, and freeze:
Ben explicitly requested those actions. Freeze remains conditional on the
final committed and pushed exact `main` passing preflight. Ceremony execution,
seal, release, and reservation retirement remain unauthorized.
Neutral-41 was later explicitly executed and sealed. Its verified source-free
package `sha256:8b29e86c7227752964addd1c5dc06c729ed53288d0371b6926c78dc4dc555423`
proves Phases 1–6 passed; Phase 7 completed its stale-worker functional boundary
and then stopped on the inherited 30-second exact allocated-custody gauge.
Phases 8–11 did not run, teardown retained no custody or process, and the
separately reviewed pressure reservation was durably retired after Ben's
explicit cleanup approval. T40.13t is the prospective V31 harness correction;
neutral-41 is consumed and neutral-42 is first admissible. No fresh freeze,
execution, release, Epic closure, or scale/SLO claim is authorized.
Independent correction review rejected a synthetic owner-count/byte-floor
fixture as non-equivalent to real Phase-7 custody: the retained evidence does
not bound its physical entries, directories, hardlinks, cache posture, or
concurrent work. No such fixture is shipped. An exact full-profile replay
through Phase 7's terminal gauge remains mandatory before any fresh freeze.
That exact V31 replay later stopped after 23,469.777 seconds in the stale-worker
wait when 15 extraction-progress HTTP 409 `status_other` rows alternated with
fresh pending projections and filled the 32-entry diagnostic inventory. The
schedule remained active at 272 materialized, 70 succeeded, 202 pending, and
zero failed; source-free cleanup observation
`sha256:c69ce4124464f22934a2cd5972898ad1a7143604dbe1fcabdddcefa2689d675d`
is coherent, but the later explicit stale-chunk fence was not reached.
T40.13u's prospective V32 contract endpoint-fences seven closed
snapshot/authority 409 details across observation, extraction, and caller
generation progress, holds one latest conflict while same-stage pending
progress resumes, materializes it for every non-recognized next event or
terminal receipt, and records bounded generic count/first/last-wall evidence.
The 32-entry cap, non-benign transition accounting, diagnostic-limit priority,
and V1–V31 bytes remain exact. V32 also names only extraction-progress `Read`
and `Invalid` 500s as `500_store` and `500_response`; they remain outside the
hold and retain ordinary transition cost. This changes no product wire behavior
and authorizes no replay, freeze, execution,
release, Epic closure, or scale/SLO claim.
Exact-clean source `968311621f389643365587f4ae588ba83c832e68` then passed
the V32 full-profile prefix through Phase 7 in 21,281.087 seconds. All seven
phase oracles succeeded, and five converged waits counted six recognized
progress-retry conflicts while retaining only five to seven transitions. The
source-free v2 result
`sha256:0e17da4500e8000713ca8e3abc6f97041772b3d78bdb2bf3661589f5e5b84c75`
binds plan
`sha256:8784172854b86275d55705e920e6bf6e0499910e3d254c961a41639a0f5a3005`
and clean-teardown observation
`sha256:6eaef4eb7cea706c2e9b5874a5e09e0e3978e6cdb6363fd316263c9650a8a426`;
the exact result is retained at
`spike/t4013/t4013u-v32-full-profile-phase7-replay.json`. Separate review
proved matching completion evidence and no surviving process, listener,
holder, mount, custody, supervision, driver, or bootstrap. This closes only
T40.13u's replay/result gate and makes the reviewed branch integration-ready;
pressure and every later phase, exact-main freeze, complete ceremony, release,
Epic closure, and scale/SLO evidence remain open.
The later exact-main `t40r1-neutral-43` ceremony passed Phases 1–10 and clean
teardown but stopped on the first structural authorized search with one HTTP
500. Its sealed source-free package
`sha256:0ecacfe9dda34ec601c9391ac73d9b9b3ba84b7481588041541477ea9e26c1ee`
does not retain the private cause. T40.13x preserves a compact source-free stop
summary at
`spike/t4013/t4013x-neutral43-authorized-query-stop.json`, permanently retires
identifier 43, and independently reproduces the cold-reader boundary without
attributing that cause to the sealed receipt. Whole-reader startup remains
lazy: only a request deadline that leaves the same cache-owned validation or
exact fill active, or races its same-generation successful completion, returns
typed warming; a completed error or cache-task expiry wins the deadline race,
service search schedules no false repair, and every
marker/root/content fence remains mandatory. REST maps warming to a fixed 409.
Only V32 gives the final query attempt the ten-minute cache-ceiling delay after
a first-attempt warming response and a second retryable response; V1–V31 keep
their historical classifier, context, cadence, and wait-cancellation evidence.
The corrected combined
Phase-9→10→11 real-binary selector recorded the first and later structural
searches as single-attempt successes, preserved exact store/source/root/receipt
authority and counts with no publication marker, and passed in 184.94 seconds
for the selected subtest and 238.435 seconds for the package. Focused normal and
race, complete affected-package, vet, repository-pinned lint, module, docs,
glossary, shell, and whitespace gates pass. Two final independent reviews
reported critical/high/medium/low findings all zero. The corrected tree is
eligible for a separate integration request; no merge, fresh freeze, execution,
release, Epic closure, or scale/SLO claim follows automatically.
T40.13x is now integrated locally at exact source
`356f155ba21a156bfbb26cd1d317feb0c0b8fe89`; push remains separate. T40.13y
owns the final bounded completed Phase-9→12 handoff rehearsal. It changes no
production path: an explicitly synthetic completed Phase 1–8/global-oracle
prefix is truncated before every late startup, wait, phase, collection, query,
and teardown field, then the unchanged small real-binary Phase 9–12
coordinators must supply the complete receipt-valid suffix while Phase 12 owns
live structural shutdown and custody retirement. Fast splice/package gates,
complete normal/race, vet, pinned lint, module, docs, glossary, and whitespace
gates passed. Independent corrected-diff review reported no finding. Exact clean
implementation commit `1f589e3ece12d60a625fa28fbad06156419359a5` then passed
the real Phase 9–12 selector in 244.12 seconds for the test and 244.816 seconds
for the package, emitted the completed custody-retirement marker, removed its
unique private parent, left no matching process or port-65499 listener, and
preserved a clean checkout. This establishes neither full-custody deletion
cost, signed ceremony evidence, a complete ceremony, release, Epic closure,
nor scale/SLO.
Exact-main `t40r1-neutral-47` then completed the frozen V32 T40.13 mechanics
gate at source `fb88c1d7fed7f32c1c3dd07303268366535cfa0c`. Its independently
verified source-free package
`sha256:7130d80bd6c4b59ae8d4cfe0fdefd456d6287a6aef35781577b53ce2acb6c2e0`
records substantiated `continue/all_exact_mechanics_passed`: all twelve phases
and sixteen checks passed exact oracles, all eleven startups were healthy, all
fourteen waits converged, interruption recovered the selected lease as
`requeued`, and completed teardown retained no derived or scratch-source
custody. T40.13 is complete. The receipt remains mechanics evidence only and
authorizes no release or private rerun; it establishes no target SLO, service
scale, accuracy, completeness, migration, or decommissioning. Epic 40
closed by Ben's explicit 2026-08-30 disposition, which advances Epic 41 while
leaving every evidence nonclaim and `DO_NOT_RELEASE` intact. The exact unheld
66,500,000,000-byte host-pressure ballast was retired after the signed package
reverified; evidence, signer, and identifier history remain. T41.1's reviewed
runtime-dark 8,000/10,000/12,500-service profiles select the reduce-only v3
aggregate envelope and at-most-512-claim relationship buckets without changing
the current 4,000-service production cap or runtime registration. T41.2's
reviewed runtime-dark v3 contract is integrated. T41.3's dark ingestion and
immutable authority, T41.4's recovery/lifecycle ownership, and T41.5's
resumable state reconcile/activation are also integrated. T41.6's sparse
catalog/state/search backend and T41.7's authorized directory/search transport
parity are integrated. T41.8's runtime-dark bucketed relationship publication,
canonical recovery/archive ownership, exact pins, sparse reads, and sixteenth
bounded lifecycle owner are integrated. T41.8a's catalog-absence and
exhausted-result recovery is integrated; T41.9 is next and alone owns runtime
selection. Epic 42 still owns combined-scale proof.
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
