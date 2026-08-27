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
The dedicated human Phase-9 `archive_restore` rehearsal now calls the production
coordinator after a small structural A→B→A-return. Its first working-tree run
passed in 88.70 seconds for the subtest and 147.917 seconds for the package
command, with exact meter/server accounting, successful shutdown, no retained
current-run workspace, and no matching process. Older retained readiness roots
remain untouched. Because the run preceded its immutable implementation commit,
an unchanged exact-clean-commit rerun remains required before candidate
attribution. Human Phase 10 collection and every broader ceremony, release,
Epic-closure, and scale/SLO claim remain open.
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
