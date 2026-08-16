# phebs roadmap

This is the current sequencing view. Unscheduled draft acceptance criteria live
in the [active backlog](./BACKLOG.md), completed implementation history lives in the
[completed backlog](./BACKLOG_COMPLETED.md), and architecture decisions live in
[PLAN.md](../PLAN.md).

## Current product posture

phebs ships as a self-hosted, single-node Go application with:

- Git repository synchronization, zoekt search, repository browsing, SCIP code
  navigation, and Git history;
- local authentication, revocable API keys, optional OIDC, permissions, audit,
  OpenAPI, and stateless MCP;
- supervised local SurrealDB state, bounded index-builder children, backup and
  restore, and deterministic release tooling.

The contract-intelligence, Caller Map, Change Workbench, Thrift-field, and
Kafka evidence stacks are implemented but remain experimental/default-dark.
The Workbench can bind provisionally to real store-derived protobuf or Thrift
evidence for development and pilot evaluation. The legacy synthetic cohort
remains fixture-backed, while Epic 30's neutral service cohort flows through
ordinary sync, focused indexing, extraction, resolver, caller-leaf, and
complete-publication workers. Their retained external validation result is
`NOT_ESTABLISHED`; they do not establish runtime use, completeness,
compatibility, migration completion, decommission safety, or extraction
accuracy.

The current service model is intentionally bounded: its monolithic catalog,
state convergence, and relationship authority admit at most 4,000 services and
are not the selected shape for the new 8,000-or-more requirement. The product
direction remains microservice-first. Repositories share physical source/index
generations; services are first-class catalog, search, relationship, and
workflow scopes over those generations. Epics 40–42 now evolve the physical
pipeline and logical authority independently before one combined scale gate.

## Now

T31.1 completed on 2026-08-04. Bounded, source-free, component-specific
pipeline receipts now cover generic durable jobs, candidate planning,
extraction scheduling/outcomes, and fixed extractor counters while remaining
outside evidence, proof, and publication identity. Startup analysis-unit
posture reports selected path counts rather than selected path strings. The
diagnostics add no store read, member reopen, directory scan, hash pass, or
child solely for logging, and disabled recovery/repair paths retain no
diagnostic manifest clone. Its first decision-bearing use was T32.2's
whole-monorepo baseline.

T32.1 completed on 2026-08-04. It freezes the microservice program contract and
validation matrix across the source of truth without adding multi-service
runtime behavior or a scale claim. T32.2's authorized whole-monorepo baseline
completed on 2026-08-04. The source-free receipt at
`spike/t322/results.json` records one completed private authorized run; it
establishes no SLO and selects no topology. T32.3's neutral service-authority
and correctness corpus completed on 2026-08-04 with five deterministic Git
revisions, independent membership/search/relationship/currentness oracles, and
complete 1,000/5,000-service load profiles. It selects neither a production
catalog schema nor a topology or target SLO. T32.4's source-free topology and
cost spike completed on 2026-08-04: direct shared whole-repository shards are
the initial v2 topology, service membership is compiled inside the exact
revision-bound zoekt query, and bounded cohorts/P6 remain trigger-gated.
T32.5 completed on 2026-08-04 with a conditional implementation GO: it freezes
the v2 identities, explicit authority precedence, membership roles,
independent current/stale behavior, side-by-side migration, conservative
initial admission caps, and named deferrals without authorizing runtime
registration or release. T33.1's strict canonical service-catalog contract
and T33.2's exact ingestion, census binding, immutable publication,
backup/restore, and v1 migration completed on 2026-08-04. T33.3's independent
desired/active/status/incarnation/tombstone state and bounded repository
summary, T33.4's authorization-first paged HTTP/MCP inventory and exact
detail, and T33.5's accessible service directory and neutral epic demo
completed on 2026-08-05, closing Epic 33. T34.1's immutable repository
source/search generation, T34.2's exact service-query compiler, T34.3's
fail-closed publication migration, activation, and recovery, and T34.4's
shared All code/service product and neutral demo completed on 2026-08-05,
closing Epic 34. T35.1's generation-scoped chunk scheduler, T35.2's pin-aware
lifecycle decision, T35.3's bounded sweep/capacity control, and T35.4's
lifecycle recovery/operator demo completed on 2026-08-05, closing Epic 35.
T36.1's bounded immutable Git reader and source-partition contract completed
on 2026-08-05. T36.2's Go source-observation contract, T36.3's
content-addressed observation publication, and T36.4's authorized progress
and neutral multi-pack demo completed on 2026-08-05, closing Epic 36. T37.1's
namespace-sharded declaration/resolver catalog, T37.2's RPC caller postings,
T37.3's Kafka topic postings, and T37.4's service projections and atomic
relationship roots completed on 2026-08-05. T37.5's exact readers,
comparison, proof/Workbench integration, and neutral demo completed on
2026-08-06, closing Epic 37. T38.1's exact selected-service overview,
T38.2's cross-service explorer, T38.3's service-aware Impact/Workbench
composition, T38.4's strict MCP parity, and T38.5's neutral product closure
completed on 2026-08-06, closing Epic 38. T39.1's neutral correctness,
scale-admission, and recovery gate completed on 2026-08-06. T39.2's authorized
target run stopped on one terminal incremental pipeline failure and completed
its teardown on 2026-08-06. T39.3's independent security/lifecycle gate
completed on 2026-08-06. T39.4 stopped before unsealing because the
evidence/workflow protocols remain unsealed design drafts. T39.5 retained the
no-release decision and closed Epic 39 on 2026-08-06. T39.R1 then closed the
conditional mirror-lock contention prerequisite without authorizing a rerun:
caller execution now receives its unchanged five-minute budget only after the
shared repository lock is acquired.

One later unfrozen private diagnostic was reduced to the source-free retained
[large-monorepo report](../spike/large-monorepo-20260806/REPORT.md). It shows
that direct search can commit beyond 1.6 million regular-file physical owners
while derived observation/extraction work can still fail to converge. It
supplies an engineering direction, not a supported-scale, target-SLO,
accuracy, topology, or release result. On 2026-08-06 Epics 40–42 became the
explicit next program:
T40.1 closed refusal attribution and froze the two-million-owner neutral
envelope on 2026-08-06; T40.2 then detached derived planning from committed
search authority, T40.3 added the bounded source-partition super-root, T40.4
added the non-authoritative hierarchical observation inventory, and T40.5
installed immutable search-generation replacement/lifecycle ownership, and
T40.6 installed restart-safe joint v2 observation authority plus bounded
lifecycle/archive recovery, and T40.7 installed constant-cost evidence-stage
accounting, T40.8 installed sparse candidate/partition controls, and T40.9
closed bounded nonproduct partition results and atomic domain roots on
2026-08-07, T40.10 installed partitioned extraction and atomic domain
authority, T40.11 migrated downstream generations plus their recovery,
archive, rollback, pin, and lifecycle ownership, and T40.12 replayed authorized
product consumers across retained v1 and current v2 roots. T40.13 is next. Epic 41 separately targets 10,000
accepted services with an 8,000 accepted-service floor; Epic 42 composes both
dimensions. No private rerun is authorized.

On the same day the [design charter](./DESIGN_CHARTER.md) became the
presentation authority, and Epic 43 ran as its parallel presentation-only
track: twelve charter-gated tickets from audit ledger and semantic tokens
through authority drawers, contract-exact caveats, citation objects, scope
continuity, keyboard navigation, operator cards, and ten-thousand-row
density, closing with a motion pass. It completed on 2026-08-08 — closure
record `spike/t431/CLOSURE.md`, re-critique 37/40 against the 23/40
baseline, zero open blockers, five residue items queued as T43R.1–5. It
touched no scale plane, authority, or claim; T40.13 remains the next scale
ticket.

Epic 30 completed its single-node service-scoped analysis boundary on
2026-08-02. T30.1 froze the commit-bound analysis-unit contract and
recorded GO after proving a focused zoekt child and exact shard-set validation
over a neutral generated corpus. T30.2 now supplies strict repository-keyed
analysis-unit configuration, canonical identity, revision-bound committed
state, same-HEAD rebuild reconciliation, and bounded operator status. T30.3
now ships the focused child, exact shard-set publication, trusted-reader
counters, byte-exact valid focused backup/restore, and packaged-binary parity.
Its 2026-07-29 repair binds each query to the exact repository-local validated
generation, caches validation only while the complete repository-local
bound manifest/member identities agree without repeating a shared-directory
scan per focused repository,
keeps zoekt-admissible selected text through 64 MiB searchable while refusing
every content-policy tombstone before publication, isolates validation from
unrelated repositories, bounds JSON fan-out/top-K retention, retires unused
focused mmaps, gates same-HEAD whole-to-focused results, lets precious-state
backup omit invalid rebuildable publications, rejects sparse restore input,
and pins invalid-claim forced reconciliation. Publication content remains
recovery content rather than semantic identity. Configured status says
`focused`; typed input remains `repository-root-unbound` unless configuration
explicitly designates one supporting SCIP artifact. T30.4 now inserts one durable
candidate-planning stage between indexing and extraction: a streamed HEAD
census produces a strict content-addressed repository/unit manifest and
bounded caller leaves, and no extraction run begins without its current
publication. T30.5 now consumes the unit projection for local contract, field,
topic, consumer, attribution, and Workbench implementation evidence; binds a
designated typed index to its real supporting path; and keys attempts, runs,
coverage, and consumers by exact repository, indexed HEAD, unit digest, and
domain. Legacy whole-repository evidence remains readable only in its empty-unit
scope, and same-HEAD unit changes cannot reuse it. T30.6m now explicitly keeps
exact historical commit/unit/domain publications, retention-quarantined
evidence, attempts, failed candidate-transition residue, caller rows, and
incomplete-generation caller artifacts unbounded; sweep backlog also occupies
capacity until maintenance drains it. Durable pins accumulate according to
their mixed owner lifecycles rather than one global policy. Review grounding
expanded that inventory from nine to twelve grouped owners: terminal history
in all eight job
tables, the 24-table Investigation/Workbench graph, and default-retained proof
bundles also accumulate. Proof bundles already expose a positive-duration TTL,
but it defaults disabled and governs only that owner. Inventorying the job and
Investigation groups records incidental current behavior; it does not select
or justify that growth as a future policy. No neutral evidence
justifies a destructive rollback depth, and a generation count would not bind
bytes across pins, leases, and independent owners. T30.6n now bounds
job-history reads to one 257-record physical-ID window and at most 100 returned
rows, gives `RepoStatuses` a prospective exact-or-unavailable latest-job
projection, and fences active-only first-upgrade repair durably without
scanning terminal history or deleting terminal diagnostics. Its keyset pages
use direct record-range continuation seeks and are weakly consistent with
concurrent random-ID inserts rather than a frozen snapshot. T30.6o now adds
the administrator-only `phebs-retention-status-v1` status shell, complete
52-component registry, fair 4,096-report/4,148-scan aggregate allocation, and
unconditional pre-store-open capacity warning. Every endpoint response carries
the warning header, and successful bodies repeat the code. Its zero-inventory
shell fixture is 19,955 bytes and preserves ordered non-combinable byte kinds
plus the proof-bundle-only retention control; live completed responses vary
with observed numeric metrics and remain subject to the 64-KiB gate. T30.6p
now fills 21 core
Surreal components with bounded aggregate table/namespace totals. T30.6q now
adds one aggregate row total for each of the exact 24
Investigation/Workbench tables. Together they populate 45 components under
3,550 report and 3,595 scan identities. T30.6r now completes the remaining
seven derived store/filesystem components through bounded metadata-only
authority reconciliation. Resolver/caller canonical metrics require the
supported rooted nonblocking regular-file opener and remain typed unavailable
where it is absent; physical inventory continues. Independently, supported
operating systems expose both installation data-volume metrics, while platforms
without the capacity primitive retain typed unavailable capacity with a
localized cause. T30.6q uses
one catalog preflight
that returns at most the 24 allowlisted table names and at most 24 direct
bounded record-ID table scans—25 calls, or 53 together
with T30.6p—with no new index, backfill, or startup reconstruction. T30.6r
adds at most nine further store client calls. Its one batched caller fence also
performs at most 312 bounded server-internal point reads—four for each of at
most 78 authorities—plus its marker check. Incremental directory scans are
bounded to 163,840 aggregate entry observations, 4,096 charged stats, 64 MiB of
manifest metadata I/O, 256 simultaneously queued caller directories, and five
simultaneous structural descriptors—at most three
collector-retained handles plus up to two Go/platform directory-iterator
duplicates or rooted traversal internals. Manifest parsing is serial, with at
most 32 MiB of caller raw bytes live beside its bounded decoded
pair structure. All 52 components now have collectors; runtime I/O failures
remain explicitly unavailable or lower-bound, never exact zero, with at most
nine localized T30.6r diagnostics and 54 events across the complete surface.
The stat ceiling includes explicit descriptor-rooted `Lstat` checks,
conservative open-time `fstat` charges, and one conservative slot per name-batch
(`Readdirnames`) call for the Windows error-classification `File.Stat` fallback.
The 78-report/79-scan slots allocate the response envelope rather than promise
universal exactness. The 4,096-stat ceiling covers the regression-gated lean
maximum allocation; recognized residue, nested stages, or the independent
64-MiB metadata limit may still localize a lower-bound or unavailable metric.
Directories are read in 256-name batches. Every returned raw name consumes the
observation budget. Names are otherwise names-only; only recognized names
receive explicit descriptor-rooted `Lstat` checks.
Concurrent authorized requests independently multiply these per-request bounds
because the surface adds no retention-specific cache or concurrency gate. None
of T30.6n–T30.6r adds deletion, retention configuration, or owner lifecycle
mutation. T30.6a now emits one non-authoritative, source-free, 64 KiB-capped
extraction operation report per
repository job. Shared queue, mirror-lock, pointer, and strict-open work is
recorded once at job level; nested domains carry only frozen generic outcomes
and bounded phase/count/byte/limit diagnostics. Report failure cannot affect
publication or retry disposition, and the recorder adds no corpus pass,
candidate/member hash, publication open, or blob read. T30.6b now persists one
latest-only exact-generation outcome per repository/domain with a bounded
source-free receipt and typed published, unavailable, terminal, or retryable
disposition. Published evidence and outcome commit atomically; nonpublished
outcomes preserve prior publication visibility. Exact settled generations
short-circuit across restart, while any scope, candidate, extractor, inventory,
typed-input, dependency, or candidate-control change invalidates them. A
strict same-semantic candidate repair advances its control revision, clears
only the matching terminal control outcome, and enqueues one extraction
successor. Focused missing SCIP is unavailable before staging; legacy
whole-repository behavior is unchanged. T30.6c now fixes one 15-minute
post-lock aggregate deadline, a 14-minute-50-second cumulative mirror bound,
and clipped five-minute serial domain caps. Never-attempted current
generations run before retryables ordered by their durable attempt time;
deferrals preserve settled peers and record retryable outcomes after mirror
release. Scheduling is capped at 16 domains, 64 KiB of identity, 100,000
staged rows per job, and 25,000 per domain, with no concurrency fan-out.
Verifier follow-up fences nonpublication outcomes against migration, committed
publication, and successor-attempt races; keeps transient strict-open failures
from erasing settled rows; makes terminal refusals visibly fail their queue
job; clears restored control-bound refusals with the candidate pointer; and
yields after a durable settle or new attempt when a persisted never-attempted
deferral remains, without consuming the ordinary failure-attempt budget.
Zero-progress and pre-run retries retain the normal cap. The
identity-generation upgrade causes one disclosed
all-enabled-domain re-extraction per indexed repository before steady-state
no-op cost resumes.

The post-T30.5 repair gate closed two reported integration issues:
whole-repository Search/Stream binds an exact committed shard generation across
asynchronous watcher handoff, and focused local candidate consumption no longer
replays repository-member bytes once per stale domain. Whole publications
carry an exact canonical shard receipt; startup validation is lazy, while a
runtime replacement remains on a receipt-bound static reader and every stale
boundary is a loud error rather than a false empty result. Candidate manifest
v4 commits exact in-unit domain projections and path-derived source lanes:
strict open costs
`B_repository + C_caller + ΣP`, each local replay costs `P_d`, and
repository/caller planes remain unchanged. Both issues retain adversarial
tests, full and race gates, detailed pushed fixes, and evidence-backed closure.

The separate large-monorepo review is complete. It did not overturn the
focused-unit architecture and identified the temporary flattened caller
inventory as the next integration boundary; raising global extraction limits
is not the repair. T30.6 remains that caller-overlay umbrella and is split at
each operational-state, scheduler, candidate-lane, catalog, leaf-artifact,
complete-publication, consumer, and retention seam. T30.6a bounded job
receipts, T30.6b durable exact-generation domain outcomes, T30.6c
aggregate-bounded domain scheduling, T30.6d candidate-v4 source-lane
classification, T30.6e focused local-evidence base-lane consumption, and
T30.6f resolver-catalog lifecycle, T30.6g bounded resolver materialization,
T30.6h direct caller-leaf execution artifacts, T30.6i atomic complete
caller-generation publication, T30.6j authorized exact Caller Map reads,
T30.6k exact caller comparison integration, T30.6l exact Workbench Impact
caller integration, T30.6m historical-publication retention decision,
T30.6n bounded job-history reads and startup migration, T30.6o's status shell,
T30.6p core Surreal collectors, T30.6q's Investigation/Workbench collector,
and T30.6r derived collectors are shipped. T30.7 closes the sequence with one
shared scope panel across Search, Contracts, Topics, Caller Map, Impact, and
Workbench; coverage-certificate v3 durable outcome receipts; exact caller
record counts and bounded partition progress; authorization-fenced HTTP/MCP
scope parity; and the neutral ordinary-worker epic demo. Focused search and
local evidence remain `focused-local`, while complete callers remain the
separately labeled `repository-overlay`; this UI/demo closure adds no physical
test-search overlay, automatic unit discovery, production evidence
registration, or new retention behavior.
The post-implementation review closure keeps retained v1/v2 proof bundles
byte-canonical, accepts the worker's legitimate pre-publication receipt
reasons under retryable outcomes, validates the production exact Caller Map
envelope through strict MCP output, and keeps legal null paths, retained
failures, explicit gaps, and zero/empty product states visible. These are
compatibility, transport, and presentation corrections; the request, worker,
publication, retention, and steady-state cost boundaries above are unchanged.
T30.6g registers the ordered gRPC/Thrift resolver adapters and materializes one
immutable catalog
from the exact candidate/declaration generation. It opens committed `go.mod`,
`layout-snapshot.json`, and `generated-from-snapshot.json` inputs plus each
mapped generated `base`-lane Go source required by resolver pack 1.1.0,
retains explicit unavailable/ambiguous/unsupported states, and never runs a
build, dependency query, generator, mutable checkout, corpus code, or network
request. Candidate and relevant declaration publication atomically fan out
resolver work; startup reconciles and backfills it. A matching process-cached
catalog takes zero candidate/input blob reads or content hashes; stale work is
bounded to five minutes, 100,000 input reads, 512 MiB of input bytes, 128
layout roots, 25,000 generated mappings, 128 generator invocations, 25,000
declaration records, and 16 MiB of declaration paths in addition to the
lifecycle caps. Recovery preserves authority across transient catalog I/O and
same-generation manifest conflicts, while deterministic damaged markers or
malformed store pointers use a durable forced successor before clear. Every
publication also ensures a non-forced successor before creating its marker,
closing the final-attempt crash boundary. T30.6h consumes only the selected
candidate leaf, excludes `go_test` before blob open, and retains direct-v1
results or per-record abstentions in independently durable, non-visible pair
artifacts. Resolver packs advance to 1.1.0 to carry exact generated Go symbol
identity parsed once during catalog materialization under one symmetric
catalog-wide unique-source-plus-descriptor identity budget; an oversized
mapped source becomes a zero-read unsupported input without discarding valid
siblings. SCIP remains outside this
syntax-only leaf generation. Per-pair and aggregate receipts fence result,
abstention, byte, stage, and descriptor bounds, while terminal siblings prevent
complete admission without erasing successful outcomes. Exact `_test.go` suffix
wins in candidate v4; focused local evidence for a committed non-empty unit
now consumes only `base`, while empty-unit whole-repository extraction keeps
shipped behavior and current search continues indexing every admitted test
file. T30.6i now coordinates only the exact admitted successful pair set into
one immutable complete manifest and one store pointer fenced by the live caller
job, repository/candidate/resolver authority, and a repository-local monotonic
publication revision. Exact retries do not advance the revision; every real
publish or invalidation does, including `A → unavailable → A`. Pair bytes and
the manifest become durable before the pointer, marker recovery spans every
crash edge, and process leases delay retired leaf reclamation until the final
reader releases. A cold open validates every referenced leaf once; the warm
path checks store authority and captured file identities with zero content
hashes, mirror lock, candidate replay, tree walk, source read, or child. The
process cache is capped at eight parsed publications and 16,384 pair
references. Evicted cleanup authority retains only a fixed repository-directory
key and exact manifest basename, capped at the same 65,536 current-publication
installation limit (15 MiB maximum raw identity payload), rather than a full
store state or dormant transition slot. Pair-free pointer summaries carry a
writer-owned pair-payload
commitment and actual length; the warm path applies one scalar precheck, no
`P`-element Go copy, and one final server-side pair-metadata hash while retaining
zero leaf-content hashes. Startup keyset-pages eligible
repository repair, and refuses more than 65,536 current publication rows or
manifest-plus-leaf references, 65,536 caller-root entries, or 1 TiB of
declared canonical caller bytes. Backup manifest v5 carries a hardened exact
caller archive under a 4 TiB physical/logical envelope and
bounded omission receipt. Restore validates and installs those bytes but raw-clears imported
caller authority, advances a real imported visibility edge once, and leaves
startup to force-queue reconstruction. T30.6j now consumes that authority for
the public Caller Map only: authorization precedes caller pointer, filesystem,
and cache access; `current`, `missing`, `failed`, and `stale` are explicit;
and only a current exact generation can
produce rows. HMAC cursors bind the authorization projection, full generation,
manifest, pair set, monotonic publication revision, and the store-owned
writer-claim-plus-nonce incarnation that cannot repeat across delete/recreate, so `A → B → A`
fails closed. A process-bounded reverse index is built lazily after an
authorized request, request-scoped leases protect immutable records, and warm
continuations reread only selected rows plus bounded complete-publication
identity-stat sweeps; restart or shared-registry eviction
may perform the existing bounded cold publication validation, never an
unbounded reconstruction. Eight active exact service reads, two citation Git
phases, and two cross-repository cold admissions are the process caps. Stable
cold-validation, semantic-projection, and
index-limit refusals use exact-key bounded negative entries rather than
repeating full scans. HTTP, UI, and MCP share compact request-binding-backed
exact-range citations that
reauthorize and verify commit, path, Git object, blob digest, record, and range
before returning only the cited bytes. T30.6k now uses that same exact engine
for migration comparison in one authorization-first, jointly fenced two-
generation read. Either unavailable side makes the whole page a typed gap
without classifications or a numeric total; a current pair uses one compact
shared-registry binding and a cursor over both complete publication identities.
T30.6l now composes those exact single- or two-generation caller snapshots
through the current Workbench Revision, typed Analysis-scope gaps, and a final
Investigation fence. Completed subordinate streams are confirmed through a
hidden signed full-incarnation authority token without relisting, minting
citations, or creating a new request binding. Focused-local coverage remains a
separate plane, and checklist identity excludes rotating cursors and citation
tokens. Exact comparison and Workbench composition create no completeness,
migration-completion, decommission-safety, or bounded historical-retention
claim. T30.6m records the separate explicit unbounded capacity posture.
A physical test-search overlay,
test-source association, automatic unit discovery, SCIP generation,
pack-specific recognizer expansion, and per-file parser degradation remain
separately reviewed future work. The private operator evaluation is not a
retained source or merge-bar artifact; neutral generated fixtures reproduce
only the accepted behavior classes.

The selected direction is dual-plane. Search, Contracts, Topics, source
browsing, related implementation, and the Workbench use one physically
focused service unit with explicit declaration/generated/module/typed-index
supporting paths. Caller Map and caller-backed Impact use a separate
target-bound, partitioned repository overlay over the same immutable commit.
The focused shard therefore does not need to contain the whole monorepository
to retain a bird's-eye caller view. Merely increasing current extraction
limits or applying a logical path query to a whole-repository shard is not the
scale plan. This is single-node scope work; the distributed P6 fleet profile
remains demand-driven.

The word "partition" has three precise meanings here. The semantic service
unit is the configured product scope. The zoekt builder may split that unit
into size-driven physical shards, but every shard retains one unit digest and
the original revision metadata. A second index-generation digest binds the
complete ordered HEAD-plus-allowlisted revision set. A checksummed visibility
manifest names every expected physical shard by ordinal and content/metadata
digest, so agreeing shards cannot serve when another member is missing. The
same exact configured paths are evaluated at every admitted revision; a
missing selected path refuses the complete replacement, while extraction and
evidence remain HEAD-bound. Repository-wide Caller Map work is split again,
independently, by domain-separated SHA-256 prefixes of normalized candidate
paths. Over-limit hash buckets recursively split by the next bit under frozen
candidate-count and declared-byte bounds; blob identity changes the manifest,
not path assignment. T30.4 freezes the initial depth at two bits and each
artifact at 4,096 records and 64 MiB of declared blob bytes. A bounded resolver
catalog publishes before those
target-bound source partitions run, and no caller generation becomes visible
until every declared partition publishes against the same complete set of
commit, unit, declaration, manifest, catalog, and extractor digests.

Production evidence/pilot gating remains unchanged. T31.1 bounded pipeline
observability and Epic 32 are complete. T32.4 selected direct shared
whole-repository shards for the initial v2 path after the T32.2 direct baseline
completed and all T32.3 neutral topology gates passed; cohorts and P6 remain
trigger-gated. T32.5 then closed the conditional v2 implementation gate, and
T33.1 completed the pure catalog contract, T33.2 ingests it with exact legacy
migration, T33.3 supplies independent service lifecycle state, T33.4 exposes
its authorization-first bounded HTTP/MCP reads, and T33.5 closes the epic with
the accessible directory and neutral ordinary-worker demo. T34.1 publishes
the immutable repository source/search generation, T34.2 supplies the exact
pre-ranking service compiler, T34.3 supplies the v2-only service reader,
atomic activation, final fences, and exact recovery, and T34.4 supplies the
shared All code/service product and neutral demo. T35.1 supplies the bounded
generation scheduler, T35.2 freezes owner-separated lifecycle policy, T35.3
implements bounded sweeping and pressure admission, and T35.4 closes the epic
with deterministic recovery receipts plus bounded administrator status. T36.1
supplies the unregistered bounded immutable Git reader and deterministic
source-partition input contract, and T36.2 supplies the unregistered shared Go
observation schema and four independent protocol projections. T36.3 supplies
the durable content-addressed observation schedule, publication, recovery,
cache/lease, backup/restore, and bounded lifecycle owner; T36.4 closes the epic
with authorization-first progress, operation receipts, exhausted-schedule
recovery, HTTP/MCP parity, and the neutral multi-pack demo. T37.1's
ownership-neutral, namespace-sharded resolver contract, affected-only
member reuse, atomic publication/recovery, and sparse keyed reads are complete;
T37.2's classified, occurrence-exact RPC posting partitions and sparse readers
and T37.3's source-spelled producer/consumer posting partitions are complete.
T37.4's registered bounded workload, placement projection, atomic
repository/service roots, lifecycle owner, and backup/restore are complete.
T37.5 completed on 2026-08-06. T38.1's exact service overview, T38.2's
cross-service explorer, T38.3's service-aware Impact/Workbench composition,
T38.4's strict MCP parity, and T38.5's neutral product closure are complete,
closing Epic 38. T39.1 is complete; T39.2 retained an honest stopped result;
T39.3's independent security/lifecycle matrix is complete. T39.4 retained an
honest pre-unsealing stop; T39.5 retained no release and closed Epic 39.
T39.R1's bounded-serialization repair is complete; T39.2 remains stopped and
no rerun is authorized.
Epics 25–28 remain unscheduled drafts in the [backlog](./BACKLOG.md); none is
the next ticket. Epic 25 is an embedded
documentation-browser nice-to-have. Epic 26 is a spike-first SQL schema-set
evidence proposal:
committed PostgreSQL or MySQL schema-only dumps can independently supply
citable, dialect-separated catalogs; schema and authored-query inputs may
join only through committed sqlc manifests; and migration events remain a
separate history plane with no current-shape claim. A missing schema artifact
is an explicit request-dump workflow state, not permission to infer a model.
Epic 27 applies the same declaration-first posture to schema-on-write document
stores through a strict, employer-neutral committed JSON manifest: it can
produce a citable table/key/nested-field catalog or an explicit
request-schema-export state, while raw private dialects and client-code usage
remain outside phebs. T27.1's contract is implementation-ready but
unscheduled: synthetic fixtures cover the full neutral vocabulary, while one
pinned Apache-licensed Cassandra schema must be hand-derived as an independent
reference without adding a CQL parser or Cassandra pack. Epic 28 revises the
Redis verdict: a universal keyspace model stays out of scope, while a
two-lane spike measures deterministic declaration islands (index, time-series,
stream-group, and ACL declarations) and provable key usage bound through
the exact BSD-licensed Redis 7.2.15 command-metadata pin. Its named
`FT.CREATE`/`TS.*` subset is parsed by bounded, documentation-derived
phebs recognizers without incorporating module code or metadata; script key
lists and ACL patterns retain deliberately narrower semantics. The spike pins
public Asynq and CloudWeGo go-redis/v9 corpora plus Redis ACL fixtures before
execution, and drafts a neutral keyspace manifest only if the measured
declaration gap justifies it. Completed Epic 29 now conditionally binds the
existing Change Workbench to the store-derived Contract Atlas behind one
development-only flag alongside an already-enabled provisional protobuf or
Thrift extraction lane. The flag does not independently expose the evidence
store, add a route or capability identifier, or permit a simultaneous
synthetic/fixture catalog authority. It lets a pilot exercise Workbenches over
real published evidence but changes no production registration, which stays
behind the gates below. None of Epics 25–28 is an implicit next ticket; the
completed microservice sequence below is the foundation for the explicit
Epics 40–42 scale program.

## Completed foundation: microservice architecture program

The program goal is not merely to index a large repository. It is to make
services first-class without performing repository-sized work once per
service. An authorized user must be able to search all code or one service,
inspect that service's contracts and supported dependencies, follow
cross-service evidence, compare exact snapshots, and see every ambiguity,
failure, exclusion, stale partition, and unowned path that bounds the answer.

The target steady-state cost is approximately:

```text
repository entries + distinct admitted source bytes + changed partitions
  + emitted observations
```

It must not approach:

```text
service count × repository bytes
```

### Foundation already achieved

| Foundation | What it establishes | What it does not establish |
|---|---|---|
| Whole-repository zoekt path and exact publication handoff | all-code indexing/search exists with immutable revision and shard/store fencing; T32.2 observed one target environment and T32.4 selected direct shards initially | a target SLO, general capacity limit, or guarantee for a different corpus/environment |
| T30 focused analysis unit | exact primary/supporting paths, unit identity, focused shards, backup/restore, and scope-aware results | more than one service per repository |
| Streamed candidate manifest | one bounded HEAD census with deterministic repository, local, and caller partitions | a reusable catalog containing thousands of services |
| Resolver and caller-leaf pipeline | exact declaration catalog, target-bound partition work, complete atomic caller generations, and authorized reads | parse-once source observations or one join shared by every service |
| Coverage, outcomes, proofs, and Workbench | explicit failed/stale/unavailable states and immutable evidence authority | validated accuracy, complete relationships, or multi-service currentness |
| Retention-status and T31 diagnostics | bounded visibility into accumulated state and pipeline cost without source leakage | bounded deletion/GC or a large-monorepo operating envelope |

### Required system capabilities

| Capability | Required boundary |
|---|---|
| Service catalog | stable logical service keys separate from repository placements; many placements per repository; explicit authority; primary/supporting/shared/generated/typed/unowned roles; many-to-many path membership; conflicts and proposals never silently become truth |
| Repository source/search generation | one streamed census and one selected physical topology per exact commit; all-code search plus service predicates inside the zoekt query |
| Independent service state | desired and active commit/catalog/publication identity per service; one service's success or failure cannot relabel another |
| Shared observations and resolver data | bounded source blobs parsed once per commit/policy; declaration namespaces and target-independent observations partitioned for reuse |
| Cross-service relationships | caller and topic postings joined once by exact contract/topic identity, then projected to zero, one, or many services with unresolved states preserved |
| Scheduling and lifecycle | generation-scoped bounded chunks, commit coalescing, fairness, leases, retry fences, progress, tombstones, pin-aware retention, and disk-pressure behavior |
| Product surfaces | All code/service switcher, service inventory/detail, cross-service impact, comparison, coverage/gaps, operations, HTTP, and MCP parity |

### Validation gates

1. **Target whole-monorepo baseline (complete, T32.2):** the prospectively
   frozen run exercised whole-repository search with provisional packs
   disabled, then candidate/extraction stages one at a time. Its retained
   source-free receipt classifies index, candidate, extraction, relationship,
   and retention outcomes without becoming a target SLO.
2. **Neutral correctness oracle (complete, T32.3):** independently enumerate service membership,
   shared/generated/unowned paths, rename/delete/conflict states, all-code and
   per-service search results, cross-service callers/topics, partial
   publication, authorization, and migration behavior.
3. **Cardinality and cost (initial evidence complete, final replay T39):**
   generated 1,000- and 5,000-service profiles measured direct-search builds,
   readers, descriptors, predicates, no-op comparison, and updates. Catalog,
   scheduler, retention, and GC writers must replay their applicable profile
   dimensions as they ship. These are load tests, not target-corpus SLO
   evidence.
4. **Search-topology equality (complete for the initial path, T32.4):** direct
   whole-repository indexing met the frozen envelope and the neutral oracle,
   so the cohort trigger did not fire. Bounded cohorts must prove equality
   under broad queries, ranking ties, truncation, cold mmap/FD pressure, and
   real generation transitions before any future selection. T34.3 completed
   those direct-path transition/recovery tests; this gate reopens only after a
   named limit fails.
5. **Evidence quality:** every pack separately measures call-site extraction,
   service attribution, and end-to-end service relationship precision/recall,
   processing coverage, and unresolved behavior. `GATE2-V2` remains
   `NOT_ESTABLISHED` until a permitted internal gate establishes a named scope.
6. **Security and operations:** permissions precede service names, counts,
   catalog data, relationships, and proof material; revocation, partial
   failure, restart, restore, stale generations, retention, and disk pressure
   fail closed under bounded work.
7. **Workflow value:** a named migration/impact workflow must beat or
   materially improve the independently captured manual inventory without
   hiding correction, owner-routing, or operating cost.

### Program sequence

- **Epic 32 — contract and validation (complete):** freeze the v2 program,
  measure the target whole repository, build the neutral
  authority/correctness profiles, and select direct shards, cohorts, or a P6
  escalation.
- **Epic 33 — service catalog (complete):** T33.1 supplies the strict pure
  multi-service authority and membership contract, T33.2 supplies exact
  ingestion and v1 migration, T33.3 supplies independent service state, T33.4
  supplies authorized bounded HTTP/MCP reads, and T33.5 supplies the accessible
  source-free directory and neutral ordinary-worker demo.
- **Epic 34 — shared search (complete):** the immutable
  source/search generation, direct physical root, exact pre-ranking service
  predicate, v2-only service reader, atomic activation/recovery, shared
  HTTP/MCP/UI selector and receipt, and neutral whole-repository demo are complete.
- **Epic 35 — bounded lifecycle (complete):** the
  generation-scoped paged scheduler, fair resource pools, repository tokens,
  coalescing, retry successors, and stale-worker fences are complete. The
  root-first owner matrix, protected-pin/lease precedence, independent
  age/count/typed-byte limits, tombstone fence, and 80/90/75% disk-watermark
  posture are frozen and enforced by durable fair cursors, root/lease rechecks,
  bounded catalog/scheduler/job collection, hard-watermark admission,
  deterministic recovery receipt, exact cursor backup/restore, and
  administrator-only source-free status are complete.
- **Epic 36 — shared source observations (complete):**
  bounded batch immutable Git reads, deterministic source partitions, the
  shared Go observation/adapters, exact content-addressed publication,
  authorization-first progress, source-free receipts, exhausted-schedule
  recovery, and the neutral multi-pack demo are complete.
- **Epic 37 — relationship index (complete):**
  the ownership-neutral namespace-sharded declaration/resolver root and exact
  classified RPC caller postings, Kafka topic postings, service placement
  projections, atomic repository/service relationship roots, authorized keyed
  HTTP/MCP readers, exact two-generation comparison, lease-pinned citations,
  proof/Workbench root coverage, and the neutral demo are complete.
- **Epic 38 — microservice product (complete):** the exact selected-service
  overview, source-first cross-service explorer, service-aware
  Impact/Workbench composition, strict shared-service MCP parity, and
  end-to-end neutral product demo are complete. Surfaces remain experimental.
- **Epic 39 — validation and release (complete):** the
  neutral correctness/admission/recovery gate is complete. The authorized
  target gate stopped on a terminal incremental pipeline failure, skipped all
  later phases under its frozen STOP rule, and destroyed derived custody.
  The independent security/lifecycle matrix passes ten named negative-case
  groups without relabeling that stop. The evidence/workflow gate then stopped
  before unsealing because no execution-binding Gate-0 protocol, independent
  gold, or workflow baseline exists. The final decision is `DO_NOT_RELEASE`:
  all packs remain experimental-dark, human continuation is ineligible, and
  that decision authorized no rerun or next implementation ticket. T39.R1
  subsequently moved the unchanged caller execution budget after mirror-lock
  acquisition, closing the contention prerequisite without superseding T39.2.
  The later post-Epic-39 ADR independently schedules Epics 40–42 without
  changing the no-release or no-private-rerun decision.

T32.1 completed the decision/documentation closure after T31.1 diagnostics;
T32.2–T32.4 supplied the source-free target observation, neutral oracle/load
profiles, and direct shared-search selection. T32.5 closed Epic 32 with a
conditional implementation GO and conservative admission/refusal caps below
the largest synthetic envelope. T33.1 implements those caps, strict
authority/membership validation, and canonical identity; T33.2 now binds that
contract to exact source censuses, immutable precious store authority, and
side-by-side v1 migration, and T33.3 now owns independent state. Those caps are
not release limits. Target operating limits, pack accuracy, workflow value,
and any release decision need a separately authorized successor to the stopped
T39 gates; Epics 40–42 establish bounded mechanics and neutral scale evidence
only. Every epic ends in a demoable state.

## Next: two-million-owner and 10,000-service convergence

The next program separates two scale dimensions that prior evidence never
measured together:

| Dimension | Minimum program target | Why it is separate |
|---|---:|---|
| Shared physical repository | at least 2,000,000 regular-file owners | the retained diagnostic had no accepted multi-service catalog and stopped in derived work after search publication |
| Logical service authority | 8,000 accepted floor; 10,000 accepted target; accepted-only 12,500-service comparator, with total records separate | the current 4,000-service catalog, state, and relationship layouts are monolithic; the large-repository run provides no service-cardinality evidence |
| Combined product | at least 2,000,000 owners × 10,000 accepted services | only the combined gate can prove that logical membership does not multiply physical Git, index, observation, or source-storage work |

The intended cost remains:

```text
physical owners + distinct admitted source bytes + changed bounded partitions
  + emitted observations + logical membership/state changes
```

It must not become:

```text
service count × repository bytes
```

### Program sequence

- **Epic 40 — very-large-monorepo derived-pipeline convergence (in progress;
  T40.1–T40.12 complete, T40.13 next):** exact source-free refusal attribution,
  the frozen neutral envelope, independent generation-scoped planning,
  hierarchical source/observation roots, immutable search lifecycle, joint
  observation recovery/archive ownership, chunk-proportional evidence append,
  sparse candidate/partition controls, and bounded pure partition-result/domain
  roots, partitioned extraction behind atomic domain authority, and downstream
  recovery/lifecycle/archive ownership and authorized product-consumer replay
  are complete. Take 16 stopped unclassified after observation convergence
  reached extraction planning and the frozen 64-MiB aggregate candidate-member
  input bound refused the exact 792,000,000-byte structural population. The
  bound was mis-derived from semantic extractor-output bytes and is
  unsatisfiable for the already-frozen 489-member shape. The recorded
  reduce-first sequence first adds closed terminal refusal and bounded
  extraction-progress attribution without changing a production bound, then
  separately splits the per-partition and aggregate controls and introduces a
  versioned v2 plan with a measured 1-GiB aggregate while preserving v1 restart
  validity. Serialized extraction throughput must also be measured before any
  Take 17 freeze. No rerun or bound change is authorized by this record.
- **Epic 41 — 10,000-service authority and sparse consumers (after Epic 40):**
  freeze production-valid 8,000/10,000/12,500 profiles, retain v2 semantics in
  a v3 root with dual service/path member views, publish immutable precious
  authority with real lifecycle ownership, reconcile and activate state under
  separate resumable fences, make authorized point/page reads member-local,
  bucket relationship publications, and close on a neutral
  10,000-service recovery/lifecycle/product gate. T41.1 owns final aggregate
  limits; no constants-only increase is permitted.
- **Epic 42 — combined scale gate and topology decision (after Epics 40–41):**
  freeze a deterministic combined corpus and independent oracle, execute cold,
  no-op, delta, A→B→A, interruption, restore, pressure, lifecycle, and product
  paths through ordinary workers, then retain single-node direct topology,
  request a bounded cohort experiment, request a P6 investigation, or stop
  according to the frozen rule.

The scale corpus is first-party and profile-separated. A streaming author
creates external scratch bare-Git generations with fixed identities and A→B→A
transitions; the repository retains generators, independent oracles, schemas,
and source-free receipts rather than the giant generated tree. The physical
profile separates two-million eligible-Go-path and declared-byte pressure from unique
semantic content; the semantic profile separately exceeds the observation and
IDL dimensions; the logical profiles carry 8,000/10,000/12,500 explicit
accepted-service authority with shared fan-out at most 20; and T42 composes the
10,000-service target without duplicating physical bytes. Bazel/Gazelle and
bulk third-party code generators are not corpus dependencies. Runtime-trace
datasets may inform neutral aggregate graph shapes only; they are not source,
catalog, relationship, or correctness authority.

### Decision and claim boundary

- T40.13 is the only next scale ticket. Later tickets stay dependency-ordered
  and may be refined by preceding retained measurements, but may not broaden
  identity, authority, authorization, or release semantics.
- T40.R1 Take 3 is a verified `unclassified` startup-readiness stop, not a
  convergence result. Take 4 is the only authorized preparation: its v3 plan
  freezes the per-server readiness deadline, meters from process launch, and
  retains a closed source-free startup diagnostic. It still requires a new
  signer and independent plan review before execution.
- T40.R1 Take 4 is a verified `unclassified` response-contract stop, not a
  convergence result. Take 5 is the only authorized preparation: it consumes
  Huma's bounded loopback `$schema` transport field before applying unchanged
  strict application decoding, and pins the real object and array shapes. It
  still requires a fresh commit, signer, and independent plan review before
  execution.
- T40.R1 Take 5 is a verified `unclassified` cold-convergence stop, not a
  convergence or resource-ceiling result. Take 6 is the only authorized
  preparation: it keeps the same two-hour boundary and adds signed, bounded,
  source-free last-stage and progress-change evidence. It still requires a
  fresh commit, signer, and independent plan review before execution.
- T40.R1 Take 6 is a verified `unclassified` two-hour convergence-deadline
  stop at `observation_publication`, with six bounded control changes but no
  retained schedule fraction or change timing. Take 7 is the only authorized
  preparation: it uses a four-hour diagnostic deadline—twice the censored
  interval and half the unchanged eight-hour total ceiling—and adds bounded
  typed progress/timing evidence without changing production behavior. It
  still requires a fresh commit, signer, independent plan review, and explicit
  execution approval.
- T40.R1 `neutral-07` is consumed without execution because it was signed
  against an earlier source commit. Take 8 is the only authorized preparation:
  it selects `neutral-08` and makes the frozen eight-hour parent timeout win
  over a simultaneous meter-finalization failure. The four-hour diagnostic,
  production behavior, and claim posture remain unchanged.
- T40.R1 Take 8 is a verified `unclassified` four-hour cold stop. Its last
  typed observation snapshot occurred at 21m55s with 63/64 partitions
  succeeded; this establishes a later observation gap, not visibility loss at
  21m55s. The terminal four-hour stage/change was a canceled inspection, not
  forward progress. Take 9 is the only authorized preparation: unchanged
  deadlines, `neutral-09`, terminal-only cancellation, a distinct convergence
  server-exit result with pre/in-flight process monitoring, immediate terminal
  selection independent of an already-started bounded synchronous control read,
  teardown drainage before custody deletion, and at most 32
  wall/stage/class/progress-digest transitions plus the last successful
  pending/complete probe. Failed inspections remain timeline-only. The 33rd
  transition fails closed.
- T40.R1 Take 9 is a verified `unclassified` four-hour cold stop. It retained
  63/64 succeeded at 22m25s, then an unchanged observation-progress `status`
  tuple from 22m32s through the deadline; V6 cannot distinguish 409 from 500.
  Successful publication also removed the marker required to project its
  settled schedule, making the final oracle unreachable. Take 10 is the only
  authorized preparation: unchanged ceilings, `neutral-10`, a marker-independent
  settled current schedule, exact bounded HTTP status plus closed reason, and
  the last completed inspection identity/time. It adds no poll or corpus read.
- T40.R1 Take 10 is consumed without convergence execution. Its approved manual
  attempt stopped at the pre-custody host-toolchain recheck, then exposed an
  EXIT-trap scope bug; it produced no observation, receipt, or scale result.
  The transient differing tool cannot be reconstructed because the old error
  was generic and current exact observation reproduces the frozen plan. Take 11
  is the only authorized preparation: unchanged v7 evidence semantics and exact
  Take 10 ceilings, `neutral-11`, a closed tool-name-only mismatch, and literal
  shell-escaped cleanup paths that survive function scope. It still requires a
  fresh exact commit, signer, independent freeze review, and explicit execution
  approval.
- T40.R1 Take 11 is a verified `unclassified` four-hour cold-convergence stop.
  The last successful probe reported 64 materialized, 62 succeeded, and two
  running; seven seconds later progress entered persistent HTTP 500
  `500_projection`, so underlying partition terminality remains unknown. Take
  12 is the only prospective preparation: unchanged ceilings and phases,
  `neutral-12`, retryable mutable-pointer snapshot crossings, context-preserving
  cold validation, one provisional shared/pinned current cache fill, and v8
  closed projection substages. It authorizes no execution, release, Epic
  closure, or progression to Epic 41 without a separately reviewed plan and
  explicit approval.
- T40.R1 Take 12 is a verified `unclassified` four-hour cold-convergence stop.
  All 2,880 probes remained at `repository_index`; three index children and two
  retry reports do not establish the third attempt's final state. Investigation
  proved a ceremony-only blind spot: `/api/repos` cannot expose live or
  terminal index-job state, while existing bounded `/api/repo-status` can.
  Take 13 is the only prospective preparation: unchanged ceilings and phases,
  `neutral-13`, v9 closed projection/status/attempt progress, and immediate
  unclassified `repository_index_terminal` for failed or canceled latest jobs.
  It changes no production path and authorizes no execution, release, Epic
  closure, or progression to Epic 41 without a separately reviewed plan and
  explicit approval.
- T40.R1 Take 13 is consumed without sealed evidence. Its v9 terminal-index
  diagnostic fired, but stopped teardown hit transient macOS `ENOTEMPTY`; the
  fallback removed custody and the prepared manifest after the Go command had
  already returned no observation. Take 14 is the only prospective preparation:
  unchanged v9 evidence and ceilings, `neutral-14`, exact-path retries only for
  `ENOTEMPTY`/`EEXIST`, and a stable-absence fence. The unsealed Take 13 signal
  is not an official classification, and Take 14 remains subject to independent
  plan review and explicit execution approval.
- T40.R1 Take 14 is a verified sealed `unclassified`
  `repository_index_terminal` stop after all three attempts. Exact frozen-corpus
  reproduction proved the child succeeds and isolated a production robustness
  defect: the 250-ms queue poll also derived an 83-ms lease-heartbeat deadline,
  canceling healthy index children. The harness review separately found that
  pressure was host-dependent and stale, transient allocated bytes disappeared
  before the phase fence, and interruption detection recursively rescanned
  derived members. Take 15 is the only prospective preparation: `neutral-14`
  is retired; v10 keeps every corpus, phase, deadline, primary ceiling, rule,
  and nonclaim; runner leases have a five-second heartbeat floor; pressure is a
  bounded 82% custody exercise with a complete collect/normal recovery cycle;
  transient allocation uses constant-cost capacity sampling; and interruption
  reads bounded fixed-depth controls, including observation inventory v2. It
  still requires a fresh exact commit, signer,
  independent plan review, and explicit execution approval.
- T40.R1 Take 15 is a verified sealed `unclassified` four-hour cold stop. A
  production nil-versus-empty copy defect made the valid zero-unsupported
  progress receipt fail projection after 62/64 success; that copy path now has
  real-reader and exact HTTP `[]` regressions. Review of the still-unexecuted
  phases also found that recovery called the production live-backup command
  only after stopping its required server. Take 16 is readiness-only: v11 keeps
  every v10 workload and bound, retires `neutral-15`, takes backups while exact
  servers remain live, measures the recovery command trees, and only then
  stops/restores. The later-phase audit and real-binary readiness bar are now
  complete: semantic cold/backup/restore passed in 105.98s and structural
  A→B→A-return/backup/restore passed in 161.45s. Their production corrections
  cover observation-v2 replacement, exact partition-root resolver input,
  settled-empty authority, generation-exact downstream wakeups, fresh-event
  retry wakeups, bounded Git batch reads, and exact search/extraction restore.
  Freeze remains gated on a clean exact commit, independent plan review, and
  explicit approval; none is implied by readiness.
- T40.R1 Take 16 is a verified sealed `unclassified`
  `convergence_deadline_expired` stop. Its source-free bundle verifies; startup
  and resource ceilings passed, cold stopped before extraction authority, and
  no later phase ran. The deterministic cause is independently re-derived from
  the frozen generator: 2,000,000 records encode to 792,000,000 candidate-member
  bytes in 489 members, crossing the frozen 67,108,864-byte domain aggregate at
  partition 41. T40.9 incorrectly applied a ceiling derived from semantic
  extractor-output bytes to structural candidate-member input. The governing
  disposition is reduce-first in two separately reviewed tickets: closed
  terminal refusal plus bounded extraction status with no bound change, then a
  split and versioned v2 aggregate contract that continues validating prior v1
  plans. Roughly 1,956 serialized extraction chunks also require measured
  cold-window feasibility before Take 17. This record authorizes neither
  ticket's implementation, a Take 17 freeze or execution, T40.13/Epic 40
  closure, a scale/SLO claim, release, nor Epic 41 progression.
- The first Take 16 reduce-first readiness ticket is implemented on its scale
  branch without changing the 64-MiB production bound. Aggregate refusals now
  retain exact closed measurements, deterministic plan-build limits terminate
  on the first execution, an authorization-fenced bounded extraction-progress
  endpoint exposes schedule/current-domain counts, and v12 ceremony evidence
  can stop immediately with the exact closed refusal. The split/versioned
  1-GiB aggregate-contract ticket and serialized-throughput measurement remain
  prerequisites to any Take 17 freeze.
- The second Take 16 disposition ticket is implemented on its scale branch.
  New domain plans are v2 with the 64-MiB per-partition backstop preserved and
  a distinct measured 1-GiB aggregate; persisted v1 validation is unchanged.
  Five full-sized one-partition-per-domain production rehearsals project the
  slowest complete four-domain sample across 1,956 serialized chunks at
  107.503 minutes, within the unchanged cold-window budget. A full-server
  OpenAPI component-name collision found by rehearsal is closed and pinned.
  After merge-bar review, the next permissible fresh ID is `t40r1-neutral-17`;
  execution still requires explicit approval.
- Take 17 stopped honestly on the ceremony's 32-transition evidence envelope:
  healthy typed extraction digest churn consumed the v6-era anomaly inventory.
  V13 coalesces digest-only progress, keeps the fail-closed diversity guard,
  and adds source-free partition timing. Repository-scale measurement then
  found and corrected repeated full-candidate validation per partition; the
  corrected diagnostic projected structural observation plus extraction at
  about 44.6 minutes p95 inside the unchanged four-hour window.
- Take 18 is a verified `unclassified` deadline stop, but its structural cold
  profile is the first sealed two-million-owner end-to-end convergence:
  1,956/1,956 extraction partitions completed with no failure/refusal and
  relationship publication in 3,910,284 ms. Semantic observation planning was
  visibly failed from 115,006 ms through the last successful four-hour probe;
  v13 lacked the terminal classification. Exact-source reproduction separately
  proves selected v2 was still gated by legacy v1's 250,000-record generation,
  which cannot admit the semantic profile's 262,144 unique blobs although v2's
  4,000,000-record contract can. Take 19 readiness removes that hidden v1
  prerequisite, adds bounded v2 planning/refusal progress, and makes failed
  planning terminal without changing a limit, deadline, concurrency, or
  topology. A Take 19 freeze still requires the full merge bar, independent
  review, and separate explicit approval; T40.13 and Epic 40 remain open.
- The Take 19 production-binary rehearsal closed three restore compatibility
  defects (explicit-gap usability, partitioned resolver-authority field
  preservation, and the optional v2 caller upstream schema field) plus two
  post-lifecycle projection false negatives. It then passed semantic
  cold/restore and structural A/B/A-return cold/restore with lifecycle and
  authorized queries in 283.90 seconds. This completes implementation
  readiness only; independent review, freeze, and execution remain distinct.
- The first Take 19 independent review refused freeze on terminal-evidence loss,
  overly broad refusal classification, three production cutover/retry races,
  missing schema/freeze guards, and absent exact semantic fit evidence. The
  code-side remediation now seals typed terminal observation/extraction state,
  pins exact validated refusal identities, preserves retryability across
  mutable windows, settles claimed legacy schedules without a v1 census, and
  guards v2 enqueue against collection. Take 19 remains not freeze-ready until
  committed-source measurement of the exact 262,144-blob semantic route fits
  the unchanged cold/total-wall/RSS/allocation envelope and the corrected
  branch passes independent re-review.
- The exact committed semantic fit run then refused freeze: selected-v2
  observation admitted and published all 262,144 records, but semantic cold
  reached `repository_visibility` only at the four-hour boundary and the
  diagnostic ended at 14,462,200 ms. Resources stayed far below ceilings and
  no typed production refusal appeared. Because timeout evidence did not retain
  final extraction counters, the next step is bounded progress/timing capture
  and measured reduction of the semantic extraction/relationship tail—not a
  blind retry or a deadline/concurrency/limit increase.
- The bounded rerun at `5d776ef5` proved the semantic wall is terminal
  extraction, not an unfinished long tail: at the four-hour boundary the
  264-partition schedule was settled with 226 successes and 38 failures, and
  relationship publication had not begun. Of 290 attempt reports, 32 were
  terminal refusals; executor maximum was 300,002 ms, aligned with the frozen
  five-minute partition deadline. Resources remained far below ceilings. The
  retained record lacks the closed refusal tuple, so the next work is typed
  refusal retention and independent reduce-or-correct review—not a deadline,
  concurrency, bound, or topology change. Take 19 remains unfrozen.
- The partition-refusal attribution prerequisite is now code-complete without
  moving a production bound. Terminal partition execution closes its owning
  stage and diagnostics timing v2 retains a bounded closed refusal projection;
  historical v1 reports remain valid, unknown terminals remain explicit, and
  the aggregate requires exact accounting across at most 32 summaries. The
  ceremony now stops a settled failed extraction schedule immediately while
  preserving exact job-terminal precedence and active-work retryability. The
  next step is a fresh committed-source semantic fit diagnostic followed by
  independent reduce-or-correct review—not a Take 19 freeze.
- A subsequent pre-freeze audit closed a ceremony-only final-oracle mismatch:
  inspection summed all nine enabled domain roots but compared them with the
  IDL-only 49,152-fact/98,304-row aggregate. The complete frozen semantic
  oracle is 180,224 facts and 360,448 rows after its two Kafka producer
  families; finalization also requires all nine domains and zero structural
  evidence. No production behavior or bound moved. Take 19 still requires the
  fresh exact semantic fit diagnostic and independent review before freeze.
- The fresh exact fit at `43052335` stopped early and classified the semantic
  wall: 32 terminal partitions each reached the first one-over facts refusal,
  observed 769 against a 768 reservation. T40.9's 49,152-fact domain envelope
  was derived from the combined IDL population and distributes to 768 across
  the Kafka plane's 64 partitions, while the frozen two Kafka-producer families
  occupy 32 dense partitions. Resources remained below ceilings and cleanup
  completed. Take 19 is not freeze-ready; the next step is a T40.9-owned
  all-dimension result-envelope measurement and independently reviewed
  reduce-or-correct decision, not an isolated fact-limit increase.
- The all-dimension replay is now exact and retained: Kafka producer output is
  131,072 facts, 262,144 rows, 131,072 references, and 101,386,432 encoded
  bytes, with a 3,463,238-byte maximum partition. Equal reservation across all
  64 candidate partitions requires 262,144 facts, 524,288 rows, 262,144
  references, and 256 MiB bytes; these are measurements, not enacted bounds.
  Seven exhausted nonterminal partitions remain independently unresolved.
  Timing v3 closes their domain and deadline/canceled/other attribution with
  fixed duration buckets while preserving v1/v2. Rerun that diagnostic before
  selecting any output-contract or partition-shape change; Take 19 stays
  unfrozen and Epic 41 stays blocked.
- The fresh timing-v3 fit closes that attribution. The schedule settled after
  5,088,005 ms with 226 successes and 38 failures. All 32 nonterminal attempt
  failures were deadlines: 12 in proto-contract and 20 in thrift-contract;
  canceled, other, and unknown were zero. That represents two exhausted proto
  and four exhausted thrift ordinary member partitions after five attempts,
  alongside the unchanged 32 Kafka fact refusals. Proceed only with a reviewed
  controlled diagnostic that combines the measured Kafka output contract and
  domain-specific smaller IDL member packing. Do not raise the five-minute
  deadline or repack every domain globally. Take 19 remains unfrozen.
- The first corrected-contract semantic fit at `1dcf8daf` stopped before
  scheduling: observation completed, extraction entered at 1,455,004 ms, the
  exact job failed twice, and zero of the expected 272 partitions materialized.
  Result-plan v3 reached the store with measured Kafka reservations, but the
  store partitioned-run/publication envelope still enforces v2's
  49,152-fact/98,304-row/98,304-reference maxima. Version that envelope only
  for Kafka v3, preserve historical v1/v2 controls, independently review it,
  and rerun the exact fit. Take 19 and Epic 41 remain blocked.
- The store envelope now dispatches on the exact (`kafka-producer`, v3 plan
  schema) binding: the measured 262,144/524,288/262,144 aggregate is admitted
  only for that pair, every other domain and any absent or historical schema
  keeps v2's maxima, and an oversized publication must prove the binding from
  its retained canonical plan bytes. Historical controls and reads pay no new
  work. Independent review and one fresh complete exact semantic fit remain
  mandatory before any Take 19 freeze.
- Independent review accepts that correction for integration after adding
  schema-forwarding and cross-package v3-contract drift guards and preserving
  T30.4's historical zero-override policy identity. Focused, static, race,
  exact-Node UI, documentation, glossary, and shell gates pass. The full Go
  run retains only the T30.6 and T32.3/T32.4 failures reproduced unchanged at
  `main@8ca176e`; this baseline-qualified result is not a wholly green CI
  claim. A fresh complete committed-source semantic fit and independent
  evidence review remain required before a separate Take 19 freeze decision.
- The post-envelope exact fit at `7632918d` removed the old terminal refusal
  but still stopped extraction at 9,009,505 ms: 232 of 264 partitions
  succeeded, 32 failed, and relationship publication never began. All 162
  failed attempts were deadlines; 117 Kafka failures and every Proto/Thrift
  failure ended below 60 seconds, while the pinned client has a 30-second
  WebSocket request boundary. The retained writer evidence covers only one
  sequential 12,500-fact run, not Kafka v3's actual 131,072-fact run under the
  production two-worker contention. The expected 272 work items also did not
  materialize because the 2,048-record policy is focused-local and this route
  deliberately uses the shared whole-repository candidate generation. Do not
  rerun or freeze. First measure and reduce current-schema evidence-writer
  transaction cost at the exact Kafka shape, and separately version a bounded
  whole-repository execution-partition subrange. Preserve deadlines,
  concurrency, global candidate packing, topology, and historical authority;
  independently review both corrections before a new exact fit.
- The correction loop now has two opt-in failure-point gates. A five-record
  whole-repository Proto fixture reproduces the missing execution subranges in
  0.18 seconds, while a store-only Kafka diagnostic retains the exact actual
  131,072-fact population, 512 production chunks, v3 reservations, and two
  workers but stops on the first append/accounting request failure. Ordinary
  CI skips both. They remove unrelated setup and downstream work from local
  iteration; they do not replace historical/recovery/maximum-shape checks, the
  integrated exact semantic fit, independent review, or a separate freeze.
- The exact scoped Kafka writer run now closes its failure point: after
  2,169,379 ms, 143 of 145 attempted chunks completed before append returned
  one 30,007-ms deadline and one sibling cancellation. All 143 accounting
  reads completed in at most 3 ms; append had 40 at-or-above-30-second phases
  and a 348,077-ms maximum. Peak SurrealDB RSS was 456,048,640 bytes and
  cleanup completed. Optimize the append transaction's per-row shared-run
  counter updates into one chunk-bounded exact charge, preserve atomic replay,
  accounting, publication, recovery, and historical behavior, then rerun the
  same focused gate. Do not raise timeout, deadline, concurrency, or bounds.
- The focused writer correction is now green. `AddEvidenceChunk` preserves its
  transaction and initial extraction-run serialization lock, but replaces up
  to 513 shared counter updates and 768 per-record writes per full chunk with
  exactly two run-row updates, two bounded submitted-ID reads, and three bulk
  writes. Overlap/replay/conflict/one-over/concurrency/publication/recovery/
  historical coverage and the full store suite pass. The retained exact
  after-fix receipt completed 512/512 two-worker chunks and exact 131,072 facts,
  262,144 rows, and 131,072 references in 41,625 ms; every append was below one
  second (262-ms maximum), every accounting read was below one second (3-ms
  maximum), and no request failed. The whole-repository execution-subrange
  correction and combined independent review remain required before one new
  integrated fit; Take 19 remains unfrozen.
- The whole-repository shape correction is now green without repacking the
  shared candidate generation. Proto and Thrift bind the separate
  `whole-repository-execution-subrange-v1` identity at 2,048 records; only
  unitless local sparse execution emits contiguous domain-relative ranges over
  a shared 4,096-record member. Focused local projection packing and historical
  omitted-field bytes remain unchanged. The five-record proof is exactly
  `[2,2,1]`, malformed range authority fails closed, result-plan binding passes,
  and a maximum member becomes two 2,048-record work items. Sparse construction
  scans the member once; execution intentionally rereads the full immutable
  member once per subrange and reserves those bytes explicitly, with no new
  derived payload or changed aggregate ceiling. Both focused corrections are
  now complete; combined independent review remains mandatory before one new
  integrated fit, and Take 19 remains unfrozen.
- The combined post-integration review accepts both corrections for one exact
  committed-source diagnostic. It adds a production `GitSparseSource` proof
  for exact `[2,2,1]` once-only path coverage and three explicitly charged
  shared-member reads. A fresh scoped Kafka rerun completed all 512 chunks and
  exact 131,072 facts, 262,144 rows, and 131,072 references in 41,375 ms with a
  292-ms maximum append, 3-ms maximum accounting read, and zero failures. The
  exact fit now emits schema v4 and requires exactly 272 applicable and settled
  partitions with zero retry exhaustion, so the historical 264-partition shape
  cannot pass even if downstream authority converges. This authorizes only the
  integrated diagnostic and separate evidence review, not a ceremony or
  freeze; Take 19 remains unfrozen.
- The integrated exact diagnostic at `1da4ada7` proves the two corrections:
  selected-v2 observation reached 262,144 records and extraction became current
  with exact 272/272 success, zero failures, and 9/9 current domains. It then
  exposed a new downstream stop. Forty caller-leaf jobs durably settled 192
  outcomes, but only 38 succeeded; 154 are terminal generation refusals (58
  gRPC, 96 Thrift), and relationship authority did not publish. The successful
  artifacts alone contain 100,306 abstentions, 306 over the frozen 100,000
  aggregate cap, while the current schema retains no typed reason for the 154
  refused pairs. The run was stopped at 3,573,161 ms once convergence was
  impossible; no v4 fit/resource record was emitted. Next add typed caller
  refusal retention plus terminal diagnostic projection, then run a scoped
  caller-lane attribution gate before selecting reduction or correction. Do
  not raise either caller limit in isolation; Take 19 remains unfrozen.
- Typed caller refusal retention and terminal diagnostic projection are now
  implemented. Failed exact Caller Map HTTP/MCP responses expose at most 32
  source-free summaries over exact pair outcomes; summary overflow becomes an
  explicit unknown and historical untyped terminal state rebuilds through the
  existing generation recovery. The scoped production-`ExecutePair` gate runs
  in about one second and retains
  `take19-caller-failure-point.json` (`sha256:f320e8f588a4e20e8f553373ae0891d52d2c280c7b13aa10327a1b62cd629304`).
  Zero resolver descriptors yield one abstention per candidate/protocol:
  semantic exact output fails only aggregate abstention count at 524,290,
  while structural exact output has 4,000,002 abstentions and 844,000,368
  canonical/staging bytes, crossing all three aggregate limits. Per-pair
  bounds already fit, so neither a count-only increase nor smaller leaves is a
  valid correction. The next ticket is a versioned compact aggregate
  no-resolver/no-direct coverage representation with explicit coverage/gaps
  and complete publication/recovery/lifecycle review. Take 19 remains
  unfrozen; no exact rerun, freeze, release, or Epic 41 progression follows.
- The compact caller correction now versions new generations as
  `direct-syntax-compact-coverage-v2`. An exact zero-descriptor resolver emits
  one pair/member-bound certificate whose no-direct and explicit gap counts
  partition the complete candidate member; it emits no per-candidate
  abstentions and reads no Git blobs. Descriptor-present execution is
  unchanged, and V1 bytes remain readable while existing reconciliation
  schedules the V2 successor. The scoped maximal-member gate retains
  `take19-caller-compact-coverage.json`
  (`sha256:b0486178f8d4af6fd2be03e72ffa49c1075bbd4cb2fe0043d4c90a6a983e2799`):
  with the maximum legal member name and all four gap kinds, both protocols
  emit one 955-byte record for 4,096 candidates with zero results, abstentions,
  source reads, or out-of-leaf reads; the conservative 16,384-pair generation
  ceiling is 15,646,720 bytes, and both frozen profiles
  fit the new logical-coverage bound. This clears only the scoped caller gate.
  Full validation and independent review remain prerequisites to any
  separately authorized integrated exact diagnostic; Take 19 is unfrozen and
  Epic 41 remains blocked.
- The authorized `t40r1-neutral-19` exact run at source commit
  `6f02dc2ae6c15b400d6d9f358f558e358d3025ea` and plan
  `sha256:e392bacb787c27f5032874831fab35426cd6412f04aab115e695a33f90ff281e`
  advanced in the outer process sequence from structural server execution to
  semantic cold convergence, then stopped on a terminal caller generation
  before publication. Its v14
  executor recorded `caller_generation` / `caller_generation_terminal`, but
  the source-free validator omitted those newly introduced values and rejected
  the stopped observation. No receipt was sealed; teardown destroyed custody
  and private material. The harness-only correction closes the v14 stage,
  outcome, and terminal-transition contract while keeping historical versions
  closed. The consumed run cannot classify the caller cause or validate the
  compact correction or establish structural completion. A fresh reviewed plan and separately authorized rerun
  remain required; Take 19 is unfrozen and Epic 41 remains blocked.
- The authorized `t40r1-neutral-20` exact run at source commit
  `9a9052e74a18abf0bb47f54b08f208a6d4769742` and plan
  `sha256:2813a57a862ed0f498a5522eeb25d4ea616c8e0456cbb4cf13c8da6889e24dad`
  completed structural cold convergence and stopped during semantic cold
  convergence on a terminal caller generation before publication. Its v14
  source-free stopped observation validated and retained the unclassified
  `pipeline/caller_generation_terminal` identity plus successful teardown, but
  receipt construction failed because the stopped-receipt validator omitted
  both caller classifier codes. No receipt, signed inventory, or transfer
  bundle was sealed. The parity closure admits those two codes only for v14 and
  drives the actual terminal recorder through classification, observation,
  receipt construction, and decode. Exact-checkout verification prevents
  retroactive sealing of this consumed run. A fresh reviewed plan and explicit
  authorization remain required; Take 19 is unfrozen and Epic 41 remains
  blocked.
- A focused disk-backed caller-terminal witness now replays the retained
  semantic cardinality—262,145 candidates, 96 leaves, two protocols, and 192
  pairs—after prevalidating upstream authority and supplying the frozen
  fixture's expected empty resolvers. All 192 V2 compact outcomes succeeded;
  admission and publication were current with 192 coverage records, 524,290
  covered candidates, zero results/abstentions/refusals, and 106,856 bytes,
  and the shared reader returned `current`. The source-free record is
  `caller-terminal-witness.json`
  (`sha256:b6d0b265e6b3e2a80698fe54ea2f8a0fab43a0b3ccca871777d674349cfe7be3`).
  This clears the caller executor/artifact/store/publication/reader boundary
  only for an actually empty resolver; it cannot reconstruct Take 20's
  destroyed resolver state or distinguish terminal admission from a
  deterministic authority/publication rejection. Classify that actual origin
  and retain its closed scalars before another full ceremony. No production
  fix, rerun, freeze, scale/SLO, release, or Epic 41 progression follows.
- The one-boundary-upstream witness now builds and publishes the real canonical
  three-member resolver catalog from a disk-backed declaration run and the
  retained candidate projection, then lets production caller resolution open
  both zero-descriptor protocol views. All 192 pairs still admit and publish;
  the shared reader and authorized exact Caller Map return a current,
  declaration-backed, exact zero-row page with durable pair/candidate/coverage
  parity. The source-free record is
  `caller-terminal-upstream-witness.json`
  (`sha256:8409977bd830fe69880b870719e2b1a4cbb6c9648555c4d1b88a668ce2cec5db`).
  Resolver materialization and exact product projection are therefore not the
  failing boundary for this closed empty-resolver state. Production
  partitioned observation/extraction authority and the destroyed Take 20
  failed-read origin remain unproved; move that boundary next. No production
  fix, rerun, freeze, scale/SLO, release, or Epic 41 progression follows.
- The partitioned-authority witness now publishes a real current one-record
  observation-v2 inventory and exact current two-partition empty roots for both
  required caller domains, then replays the retained 262,145-record, 96-leaf,
  192-pair caller shape. Its first run exposed the actual product defect: the
  worker bound canonical upstream authority and published all 192 successful
  outcomes, but the summary retained only the digest, so exact Caller Map
  reconstructed a different historical generation and reported failure. The
  fix shares worker/reader derivation, stores the at-most-64-KiB canonical
  payload once on the publication/summary, keeps only its digest in leaf and
  admission identities, validates exact reconstruction, and repeats compact
  upstream checks at acquire/result fences. Digest-only transitional pointers
  use existing queue-before-clear startup repair. The source-free record is
  `caller-terminal-partitioned-authority-witness.json`
  (`sha256:e2f222bb799e0d10fdbec223e78c75840f64bf41877b90dbe385d1a43fc9790e`).
  It proves authority/classification/receipt parity for its closed controls,
  not physical semantic candidate membership, Take 20's destroyed resolver or
  failed page, scale, or ceremony readiness. The next boundary is production
  physical candidate-plan/provider membership. No rerun, freeze, scale/SLO,
  release, or Epic 41 progression follows.
- The physical-provider witness now installs one real Git-backed candidate-v4
  generation under the production candidate root and replaces the synthetic
  caller plan with `candidatejob.Provider` plus
  `candidate.OpenCallerPlanContext`. The first missing-root refusal was a
  fixture setup omission, not a product defect. The corrected run binds the
  store pointer's manifest digest and control revision, exposes one immutable
  one-record caller leaf, replays it for both protocols, settles both pairs,
  and preserves current exact Caller Map parity. The source-free record is
  `caller-terminal-physical-provider-witness.json`
  (`sha256:e1b73fcc2d783d4d0c90158f7562bb672f1c5726c962b84d2b0c22a77dbd6bd0`).
  It clears the small zero-descriptor physical-provider/member seam, not the
  retained 96-leaf physical distribution, descriptor-present Git-blob path,
  Take 20's destroyed state, scale, or ceremony readiness. Production behavior
  is unchanged. If diagnosis continues, use a provider-only 96-leaf control or
  the descriptor-present execution seam; do not start another ceremony. No
  rerun, freeze, scale/SLO, release, or Epic 41 progression follows.
- The provider-only physical-distribution diagnostic now builds 261,769
  deterministic Git paths through production candidate-v4 and reopens the
  exact store pointer through the narrow production provider. It retains 96
  immutable leaves—32 at six prefix bits and 64 at seven—then replays all 96
  once for gRPC and once for Thrift: 192 leaf reads and 523,538 exact record
  visits with matching replay digests. The source-free receipt is
  `caller-provider-96leaf-physical-distribution.json`
  (`sha256:48e1b1928cb167611577017f155c4b6ced5d858787e3fc58441c877db024cdc4`).
  The earlier 262,145-record synthetic shape does not establish a physical
  leaf count; this deterministic physical path family produces 98 leaves at
  that cardinality. The retained exact 96-leaf rerun measured 47.725s build,
  0.680ms provider open, and 1.879s for both replays, as observations rather
  than ceilings or an SLO. This clears only provider multi-leaf distribution,
  not descriptor-present resolver/Git-blob execution, Take 20's destroyed
  state, scale, or ceremony readiness. Move next through that descriptor-
  present pair-execution seam; do not start another ceremony. No rerun,
  release, Epic closure, or Epic 41 progression follows.
- The descriptor-present physical-pair diagnostic now builds a real seven-file
  Git commit into four candidate-v4 leaves, materializes a current one-
  descriptor gRPC catalog from exact declaration/generated authority, selects
  the one-record consumer leaf through the production provider, and runs
  `ExecutePair` with its default Git reader. The pair reads exactly one
  121-byte consumer blob, emits and rereads one exact `CALLS_OPERATION`, and
  records zero abstentions, compact coverage, or out-of-leaf reads. The
  source-free receipt is `descriptor-present-git-blob-pair.json`
  (`sha256:1952ebce6ed4b0b3dcafa35962b1375a65565b625525ac70551c4f8555d7288e`).
  The retained rerun measured 66.0ms candidate build, 66.8ms resolver build,
  0.426ms resolver open, and 18.0ms pair execute/seal as observations, not an
  SLO. This clears only descriptor materialization, physical leaf selection,
  bounded Git read, direct scan, artifact install, and reread. It does not
  prove worker outcome/admission/publication/product parity, multi-descriptor
  or Thrift behavior, Take 20's destroyed state, scale, or ceremony readiness.
  If diagnosis continues, compose this exact pair through those downstream
  production boundaries; do not start another ceremony. No rerun, release,
  Epic closure, or Epic 41 progression follows.
- The composed descriptor-present diagnostic now publishes current
  observation-v2 plus the exact required gRPC domain root, retains its
  zero-partition `unavailable_prerequisite` state as an explicit usable gap,
  and drives the real managed Git mirror and all four physical caller pairs
  through the production worker. Six turns including the no-op leave four
  successful outcomes, four artifacts, admitted/current complete authority,
  one resolved result, five abstentions, and no refusal or compact coverage.
  The shared reader independently rederives the same canonical upstream
  authority, and authorized exact Caller Map returns one current
  `resolved_caller` row with exact operation, lineage, object, and blob
  identity. The source-free receipt is
  `descriptor-present-product-parity.json`
  (`sha256:a11683cf3a3ab77800de66fec23970182e34df6edd54763c2c683b1588b67ede`).
  This clears the small descriptor-present worker/outcome/admission/
  publication/product seam, not its composition with the retained 96-leaf
  physical distribution, Take 20's destroyed state, Thrift/ambiguity, scale,
  or ceremony readiness. No production path or cost changed. If diagnosis
  continues, combine descriptor-present execution with the retained physical
  multi-leaf shape before any fresh ceremony decision. No rerun, release,
  Epic closure, or Epic 41 progression follows.
- The descriptor-present physical multi-leaf diagnostic now carries 261,769
  caller records through exactly 96 production-built leaves. One real resolved
  consumer lands in leaf 0. Descriptor presence disables the empty-resolver
  compact path, and the production worker then reaches 100,245 exact
  abstentions against the 100,000 generation limit after 38 successful pair
  artifacts. The remaining 58 pairs receive one closed
  `caller_generation_admission` / `caller` / `limit` /
  `caller_generation_abstentions` refusal; terminal admission creates no
  complete pointer. The shared reader and authorized exact Caller Map expose
  the same failed generation, complete 96/38/58 progress, and refusal scalars.
  The source-free receipt is `descriptor-present-96leaf-product.json`
  (`sha256:17512e7d9c8f46c312051bcfaf27a57d08a10df8662e7f70755475f1d596736d`).
  This classifies the scoped caller terminal and selects a reduce-first
  correction, not a higher bound: compact only descriptor-present pairs that
  complete their scan with zero caller or unresolved facts into count-bearing
  coverage; retain result/unresolved pairs in full. Historical schema,
  candidate/gap counts, recovery, publication/product parity, maximum-shape,
  and cost tests must pass before a fresh scoped rerun. No ceremony, scale/SLO,
  release, Epic closure, or Epic 41 progression follows.
- The versioned reduce-first correction and scoped rerun now pass. V3 compacts
  only a descriptor-present pair whose complete exact scan emits zero resolved
  or unresolved caller facts; it emits one pair/member-bound coverage-v2
  record with exact no-direct/gap counts and retains source-read count/bytes.
  Result-bearing and unresolved pairs remain fully materialized. Historical
  V1/V2 bytes remain exact, and existing startup reconciliation queues a V3
  successor before retiring a V2 current pointer. The unchanged 96-leaf
  physical run settled and succeeded 96/96 pairs, admitted/published one
  result, 4,055 abstentions, and 95 coverage records covering 257,713
  candidates, then returned the one exact current Caller Map row with matching
  progress and aggregate scalars. The source-free receipt is
  `descriptor-present-96leaf-product-v2.json`
  (`sha256:43e3a82e1c3897bd62f14150a1c0d9352d396030cc4f0bd1a1959f1f282b029b`).
  The scoped refusal is closed; T40.13/Epic 40, scale/SLO, Take 20, ceremony,
  release, and Epic 41 remain unclosed. Independent review is next before any
  separately authorized fresh-ceremony freeze.
- Independent review then found two V3 receipt-consistency holes and two stale
  V2 witness generators. The correction makes every current compact receipt
  name its reason, rejects source reads/bytes for no-resolver coverage at the
  receipt and store boundaries, embeds zero-fact source bytes in coverage-v2,
  and verifies exact artifact/receipt byte agreement. The partitioned and
  physical-provider opt-in generators now skip as retained V2 evidence;
  unknown future input-only abstention reasons preserve materialized output.
  The documented 96-leaf command now explicitly uses a 70-minute outer test
  timeout around its 65-minute diagnostic parent. No production bound or
  authority claim changes, and no ceremony/release/Epic progression follows.
  The corrected exact 96-leaf rerun subsequently passed 96/96 with unchanged
  current publication/product scalars and byte-identical source-free receipt.
- Independent re-review authorizes one fresh `t40r1-neutral-21` freeze after a
  custody correction. Pre-correction V3 commit `ab5f28f` was exposed through
  pushed `main` from 2026-08-15 18:12:14 to 20:12:06 -0700, superseding the
  earlier “unpushed” statement, but project custody records no deployment,
  startup, ceremony, or durable execution in that interval. Unknown custody
  is fail-closed pending a separately reviewed purge/rebuild; the ceremony's
  creation-exclusive isolated directory is eligible. After this ledger lands,
  freeze must bind the exact resulting `main` commit and corrected 96/96
  receipt `sha256:43e3a82e1c3897bd62f14150a1c0d9352d396030cc4f0bd1a1959f1f282b029b`.
  Execution remains separately approval-gated. Pilot/design-partner need for
  the 96-leaf shape or a charter decision changing the 100,000 abstention
  ceiling invalidates the freeze and requires re-review; no release, scale/SLO
  claim, T40.13/Epic 40 closure, or Epic 41 progression follows.
- `t40r1-neutral-21` executed and stopped at the exact semantic four-hour
  deadline with 266 pending, one running, five succeeded, and zero failed of
  272 materialized extraction partitions. All six observed handlers completed;
  the sixth settlement and concurrent expansion hit SurrealDB write conflicts,
  after which the single repository token never recovered. The correction
  gives every schedule transaction bounded conflict retry, separates reaping
  from expansion, bounds scheduler store calls, reconciles ambiguous
  completion, and stamps fresh V14 freeze dates. A real surrealkv diagnostic
  settled the exact 272/272 one-token shape with zero leaked token or surfaced
  error while recovering 21 expansion conflicts. Independent review plus a
  fresh exact semantic production-path diagnostic are next; another ceremony
  is not authorized.
- Search, derived observations, extraction domains, service state, and
  relationship roots remain separately visible authorities. A failure in one
  cannot erase or relabel a valid sibling plane.
- Existing per-service/path/fan-out limits and v1/v2 bytes remain exact.
  Aggregate bounds move only in their named measured ticket, with pre-growth
  refusal, maximum-shape, recovery, lifecycle, and steady-state-cost tests.
- Epic 42 freezes individual source transitions and proves convergence; it does
  not measure sustained commit velocity, queue catch-up, or freshness under a
  commit cadence. Those remain target operating evidence for a separately
  authorized successor to the stopped T39.2 operating gate.
- The retained unfrozen private diagnostic authorizes no target rerun. Every
  neutral receipt denies target SLO, supported customer scale, accuracy/completeness,
  release, migration completion, and decommission safety. `GATE2-V2` remains
  `NOT_ESTABLISHED` and `DO_NOT_RELEASE` remains in force.

## Gated product work

Production registration of the evidence and Workbench surfaces still requires:

1. retained validation that satisfies the documented gate rather than an
   operator bypass; and
2. a separate explicit pilot-continuation decision.

Until both exist, the implementation remains dark regardless of feature
completeness or demo quality.

## On demand

Epic 32 measured the target whole repository and did not trigger P6. P6 remains
the escape hatch only when the selected single-node topology later cannot
satisfy a prospectively frozen envelope or a real deployment requires
distributed ownership. It remains unscheduled:

- measure index size, memory, full-monorepo build time, and freshness on the
  target corpus;
- decide from measurements whether delta builds are required;
- design replica placement, peer search, and per-client admission budgets;
- cut SurrealDB from the supervised local child to an external deployment only
  when distributed ownership requires it;
- rerun queue consistency characterization against the selected distributed
  store;
- add image, Kubernetes, or Helm packaging only for an actual fleet operator.

Standalone remains the supported development and small-deployment profile.

## Deliberate non-goals

- cloning an Ask/chat product; phebs stays MCP-first;
- SCIM, multi-organization seats, billing, or entitlement infrastructure;
- anonymous access;
- silent filesystem-wide repository discovery;
- treating runtime telemetry or static evidence as a substitute for the other.
