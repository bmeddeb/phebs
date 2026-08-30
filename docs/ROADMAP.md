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
product consumers across retained v1 and current v2 roots. T40.13 remains open after T40.13l; the original gate still requires review and authorization. Epic 41 separately targets 10,000
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

- T41.1R1 is a repository merge-bar compatibility prerequisite: it moves only
  current synthetic SCIP producers to typed ranges, preserves deliberate
  legacy-read fixtures and retained bytes, and may integrate as maintenance
  without advancing Epic 41. T41.1 still waits for Epic 40 closure.
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
  error while recovering 21 expansion conflicts. The fresh exact semantic
  production-path diagnostic at source `abbd218` then completed all 272
  extraction schedules and 272 partition timings, recovered one store
  conflict internally, and emitted no `completion_failed`; it was deliberately
  stopped after that scoped boundary while downstream Caller publication
  inspection continued, so it is not a full-fit pass. Independent review is
  next; another ceremony is not authorized.
- `t40r1-neutral-23` moved the production boundary upstream of the repaired
  scheduler: semantic extraction completed and scheduler-settled all 272
  partitions with no failed or terminal-refused execution. It then retained
  one caller-generation 404 for roughly 3.5 hours. The frozen corpus has Go
  calls to `/neutral.Service/Ping` but no matching operation declaration, so
  the exact Caller Map probe added at `2fb09a0` correctly returned endpoint
  404 only after publication became current and the ceremony misread that as
  pending. An authorization-fenced, declaration-independent, 32-KiB caller
  generation progress route now supplies the exact state and bounded partition
  projection plus digest/count scope authority while Caller Map keeps its 404
  contract. The observer accepts only current all-success authority, retains
  its digest across revalidation, and tests the admitted maximum response shape.
  Neutral-23 remains immutable `unclassified` evidence; another
  ceremony still requires separate freeze review and execution authorization.
- `t40r1-neutral-24` completed all 272 semantic extraction schedules and
  reached current caller authority, then retained a missing relationship root
  until the four-hour deadline. Exact production projection pins two earlier
  deterministic Kafka fences: both fixed logical buckets contain 65,536
  postings against the old 50,000-member limit, and their 147,324,928-byte
  resident charge exceeds the old 128-MiB Kafka fence. The current member
  policy now admits exactly 65,536 while the historical 50,000 policy remains
  readable, and the relationship Kafka charge is 160 MiB within the unchanged
  one-GiB worker class. The scoped Kafka-to-relationship diagnostic published
  all 131,072 postings/projections and complete relationship authority.
  The partitioned schedule binds the exact builder policy so a duplicate
  same-policy reconcile retains a closed refusal while a policy change creates
  a recovery target; historical v2 bindings remain valid. Deterministic Kafka
  limits settle terminally, and an absent relationship root
  now consults bounded source-free schedule progress so active remains pending
  while exhaustion stops immediately. Neutral-24 remains immutable stopped
  evidence; focused/race/docs gates and independent review precede any request
  to integrate and authorize a fresh ceremony.
- `t40r1-neutral-25` passed both cold profiles and structural warm-noop, then
  stopped honestly during structural delta B as unclassified
  `extraction_job_terminal`. Its final extraction schedule was nevertheless
  exact current: 1,956/1,956 successful partitions, zero pending/running/failed,
  and 9/9 current domains. The latest repository-keyed extraction job had
  failed at attempt two, and the observer incorrectly gave that untyped,
  generation-unbound queue projection precedence over the generation-bound
  schedule. The signed tuple reproduces the exact terminal probe digest
  `sha256:d4703c2d327d13d0116fb2795774cb3caf21c8d98ac8c91ab163777bf7c05600`.
  Typed refusals and settled failed schedules remain terminal; an ordinary
  failed job is pending only beside active work, non-authoritative once the
  schedule is fully current and the existing exact downstream authority checks
  can run, and conclusive (typed terminal, confirmed on a second identical
  probe) in every other schedule state so a dead pipeline stops in seconds
  instead of pending to the ceremony deadline. Fresh plans, observations, and
  receipts advance to V15 for that precedence; V14 retains its historical
  job-first classification and validation, receipt coherence checks are
  outcome-restricted, so neutral-25 and every earlier
  signed receipt remain byte- and semantics-compatible. Safety and production
  bounds are unchanged. Neutral-25 remains immutable and does not pass
  retroactively; independent review precedes integration and any fresh
  freeze/execution request.
- `t40r1-neutral-27` again passed both cold profiles and structural warm-noop.
  Structural delta B then advanced from relationship pending to a stable
  generic relationship-control error and stopped at the four-hour deadline.
  The signed V15 package
  `sha256:291336d632150b1c0101da65ab2621f7c410d218a08ef53da1d065c5c2a1a758`
  proves the boundary but cannot select the destroyed private root/schedule
  condition. V16 closes the evidence gap prospectively: relationship probes
  retain a closed current-control/authority/successor class, pair absent or
  mismatching roots with bounded schedule state, invalidate the extraction
  scan once per new root generation, and require two identical samples before
  sealing a non-refusal terminal. V15 and earlier receipt bytes remain exact;
  safety and production bounds are unchanged. Neutral-27 remains immutable
  `unclassified` evidence. Focused/race/docs gates and independent review
  precede any request to integrate and authorize a fresh freeze.
- A stacked production liveness correction closes three lock-wait exposures
  before that fresh request: extraction releases its repository/source lease
  after durable result installation and before publication fencing; artifact
  reconciliation aborts its shared-fence audit after one 250-ms busy
  repository-lock probe; and relationship mutation acquisition uses 25-ms
  probes under a five-second overall deadline so the scheduler can defer the
  same chunk without consuming an attempt and return the repository-wide token
  to another ready stage. Runtime sync cleanup uses the same non-consuming
  ordinary-job deferral on a busy repository probe, while direct startup audits
  remain fail-closed. Deterministic tests cover the exact lock order and
  relationship-to-extraction token yield. This
  is prospective hardening, not a retroactive diagnosis or gate pass;
  independent review and explicit integration/freeze authorization still
  precede a new ceremony identifier.
- `t40r1-neutral-28` passed cold, warm-noop, delta B, and return A on exact
  V16 source `26ca6d7e0375eb82be8731a4a6779a88107b8d86`, then stopped
  `unclassified` during interruption. Its signed source-free package is
  `sha256:ba1a583b08494d932ee1e769161e1e4ee9343720b72d8fc30b26245f98597f5b`.
  V16 retained no interruption substage and its passive ephemeral-control scan
  did not itself trigger new work or prove an active durable worker, so the
  evidence cannot choose a backup, restore, discovery, stop, or restart cause.
  V17 prospectively replaces that ambiguity: after exact-A restore it commands
  A→B, selects one exact B-bound extraction lifecycle start whose current store
  lease is still running, stops there, returns B→A offline, and requires exact A
  after restart. The receipt retains only the closed substage and source-free
  stage/generation/chunk/attempt/wall trigger plus its verified non-running
  post-restart fate. A settled exact-B schedule with no selectable lease seals
  `interruption_trigger_unsatisfiable` without a 90-minute wait, even beside a
  stale prior-revision lifecycle start. V1–V16 bytes and validation,
  V16 relationship classification, every safety ceiling, and every production
  bound remain unchanged. Focused/race/docs gates and independent review
  precede any request to integrate or authorize a fresh freeze.
- `t40r1-neutral-29` is immutable V17 `unclassified` evidence: its verified
  package `sha256:4ba484f9b22902edda41179d0b790cec018c2ecc12fa7baaed66049c8315fcd8`
  stopped at `interruption/active_lease_wait` without selecting a trigger, so
  it cannot distinguish an upstream B stall, an absent extraction schedule,
  or a lifecycle timing gap. V18 keeps V17 historical semantics closed, makes
  the exact current extraction schedule the read-only discovery authority,
  and seals a bounded last exact stage/class/digest when no lease is selected.
  Terminal, deadline, and completed/settled-no-lease outcomes are distinct.
  The readers/cursor/inspector are now pre-armed before A→B, the 250-ms exact
  selector continues while one inspector runs independently, and stale-worker
  uses the same store authority. A fresh-repository production-binary rehearsal
  selected the lease and then exposed a failed B extraction schedule during
  interruption return-to-A; that production stale-authority retry defect is the
  next stacked blocker. Merge, freeze, and execution remain separately
  authorized steps.
- The stacked prior-gate closure now passes the opt-in real production-binary
  rehearsal across semantic interruption/restore, structural A→B→A/restore,
  and deterministic stale-worker fencing in one process. It closes stale
  extraction schedule ownership, retained whole-search reactivation, stale
  caller pointers, restore-time restartable schedule/root residue, and retry-
  shaped current progress validation. Fresh ceremony contracts advance to V19;
  the result is rehearsal evidence only until an exact integrated V19 freeze
  and fresh ceremony pass.
- `t40r1-neutral-30` then passed cold, warm-noop, delta-B, and return-A before
  stopping after interruption restart convergence: extraction `RunID`
  provenance was hashed into resolver declaration-set, generation, and
  manifest authority, rekeying caller and relationship authority for
  byte-equivalent A content. The resolver v2 correction keeps exact RunID
  provenance integrity while exposing a separate RunID-independent semantic
  authority digest to caller and relationship consumers; downstream authority
  v2 closes the independent RunID-sensitive upstream-digest path found by the
  same audit. Its supported resolver v1→v2
  migration retires current resolver/caller pointers and rebuilds through the
  existing candidate startup fan-out; retained v1 evidence stays historically
  valid. Neutral-30 remains immutable stopped evidence. Integration, a fresh
  freeze, and execution remain separately authorized; T40.13 and Epic 40 are
  not closed.
- `t40r1-neutral-31` is immutable V19 stopped evidence at exact source
  `5434bb382182251f356040eee15ac8766e2292d2`. Structural cold settled
  extraction but remained at caller generation until the four-hour deadline.
  A production-binary rehearsal proved the caller pairs and admission were
  complete while the store's private V1-only upstream validator rejected the
  canonical downstream-authority V2 envelope. Caller publication now uses the
  shared V1/V2 validator, and a real store test proves a non-empty V2 authority
  commits and reopens. Fresh schemas advance to V20, whose complete/all-success
  but missing/stale caller projection stops only after an identical second
  five-second probe. Earlier schemas remain historical. Neutral-31 does not
  pass retroactively; integration, freeze, and execution remain separately
  authorized, and T40.13/Epic 40 are not closed.
- The pre-freeze caller audit proved the existing candidate→resolver→caller
  startup chain repairs the exact pointerless failed neutral-31 shape and that
  current caller callbacks replay relationship reconciliation after a transient
  failure. It also found a production maximum-shape mismatch: a valid
  64-domain downstream authority is 138,832 bytes, above the former 64-KiB
  caller-store ceiling. Authority validation, caller identity, and publication
  now share a 256-KiB limit; real-store tests cover maximum V2 and historical
  V1 publication. The installation-wide scalar startup inventory can still
  materialize all pointer summaries at once, so its theoretical 16-GiB
  all-maximum authority envelope is recorded as a separate capacity follow-up,
  not evidence established by the one-repository T40.13 ceremony.
- The pre-ceremony readiness review swept every historical failure class and
  closed six verified blockers before any fresh freeze. Production: the job
  runner's heartbeat now tolerates transient store errors until `StaleAfter`
  (the post-c21 scheduler policy applied to the generic queue), and the repo
  row carries a caller-leaf job projection on `/api/repo-status`. Fresh
  ceremony schemas advance to V21: an active caller job holds every caller
  terminal (a live publisher's requeue window repeats the probe exactly; the
  projection is written by every creation path including the domain
  transactions), a settled-failed caller job with partial pairs is a typed
  terminal instead of a four-hour pend, and the wait records the caller job
  projection under detail-V17 coherence in both wait validators. Harness:
  the V21 relationship semantic digest (v2 label) clears upstream
  RunID/provenance and the six component transition digests so
  interruption-restart re-mints cannot re-key the exact-authority gate,
  while frozen V19/V20 contracts keep their derivation; stale_worker arms
  the rehearsal's mutation-lock + diagnostic-fence order with bounded
  re-selection so `stale_fenced` is reachable; V21 deadline/cancel/
  server-exit stops may retain an unconfirmed terminal probe at the three
  confirmation-window stages (V15..V20 semantics stay closed); and a stopped
  observation validates before custody is destroyed — unsealable stops fail
  closed with custody retained, an executed marker refuses re-execution
  against retained custody, and the ceremony wrapper preserves it for the
  reviewed purge. Recorded follow-up: death upstream of caller-job creation
  (a dead resolver-catalog job) still pends the caller wait. V20 and
  earlier bytes remain exact. Integration, freeze, and execution remain
  separately authorized; T40.13 and Epic 40 are not closed.
- The resolver-plane projection closes that recorded follow-up before any
  freeze: the repo row carries a creation-linked resolver-catalog job
  projection written by the generic queue and all seven domain fan-out
  transactions (coalesce repairs pre-cutover rows), caller-generation
  progress v3 returns it beside the caller projection in the same bounded
  read, and V21 caller classification holds every caller terminal behind an
  active resolver job while sealing `caller_generation_terminal` in seconds
  when the resolver job settled dead before minting the successor — with the
  same active-over-dead precedence and detail-V17 receipt-fence lockstep in
  both wait validators. Death upstream of the resolver job itself remains
  visible through the extraction plane's existing job projection and is the
  recorded residual boundary. V20 and earlier bytes remain exact; nothing
  here authorizes integration, a freeze, or execution.
- The V21 freeze-readiness correction makes the caller stop evidence exact.
  Caller progress v2 includes the repository-keyed live caller-job projection,
  so the harness no longer performs an additional installation-wide
  `/api/repo-status` request on every caller tick. The receipt records digest
  validity, complete pair scalars, at most 32 typed refusals, and job state;
  its terminal/refusal validators use the same mutually exclusive predicates
  as the live classifier. Existing pre-cutover pending caller jobs repair the
  repo projection transactionally when coalesced, and the custody execution
  marker is written atomically only after read-only preflight passes. V1–V20
  receipt semantics remain historical; integration, freeze, and execution
  remain separately authorized.
- Neutral-32 reached interruption recovery verification for the first time and
  exposed a harness/lifecycle retention race, not an unrecovered production
  lease. The restarted server reaped the exact B-bound chunk, then two A
  extraction incarnations and the five-second pressure lifecycle sweep moved
  its retired schedule beyond the retained-two window before the harness
  looked for the row after full convergence. Fresh schemas advance to V22:
  the selected row contributes its exact schedule digest, recovery is verified
  immediately after restart readiness, and an absent chunk is `collected` only
  after two consecutive one-second-separated fenced exact-schedule reads prove
  that digest non-current beside a distinct current successor, and absent or
  closed. Missing-current,
  current/active, moving, mismatched, unreadable, or still-running
  authority continues to fail closed. Restart convergence and the partial-state
  oracle follow recovery; two bounded generation-lifecycle owner snapshots
  retain latest-cycle scanned/deleted/backlog facts without a cumulative claim.
  V17–V21 ordering and receipt semantics remain exact. Neutral-32 stays stopped
  and cannot pass retroactively; integration, freeze, and execution remain
  separately authorized.
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

The next T40.R1 freeze is fenced by fresh V23 ceremony semantics. A restarted
lifecycle runner completes a process-observed owner cycle before its one-hour
idle; late relationship re-mints are compared through stable semantic
authority and adopted; prepared workspace allocation is charged before the
pressure phase; collection and pressure-entry deadlines are typed; stale-worker
can observe reaper recovery or exact retired collection after a failed
completion write; lifecycle vocabulary
drift fails promptly; authorized-query operational failures retry inside a
closed bound and retain source-free endpoint diagnostics; and archive direct
recovery keeps precedence over every inner cause. V1–V22 execution and
receipt semantics remain frozen. This hardening does not authorize a freeze,
ceremony, release, or scale/SLO/accuracy/completeness claim.

The V23 post-review fence additionally preserves bounded lifecycle progress on
an error owner, seals correlated authorized-query/measurement failures,
restores frozen unauthorized-probe classification, separates substantiated
pressure entry from unclassified pressure recovery, and requires a closed or
corroborated-collected interruption fate rather than accepting reclaimable
`pending`. stale-worker consults exact retention only after
`completion_failed`, and V1-V22 evidence semantics remain unchanged. A fresh
freeze still requires the complete corrected gate; this row authorizes none.

Neutral-33 proved those V23 seal and decision fixes, but exposed an
unsatisfiable interruption proof: it checked before B-schedule supersession
while refusing the scheduler's correct pending-stale, unleased recovery shape.
Fresh V24 accepts that shape only as twice-observed `requeued` evidence bound to
the exact trigger attempt; priority-zero, leased, running, or single-sample
rows still fail closed. V22 and V23 remain historical. No freeze, execution,
release, or scale claim is authorized by this correction.

The subsequent full-ceremony audit found that V24's eight-hour review wall
cannot fund all twelve measured phases and that content-addressed extraction
schedule reuse makes stale-worker's A→B→A transition unschedulable. Fresh V25
uses a twelve-hour ceremony-only review ceiling, predecessor-bound operational
schedule identities, and a measured 72-GiB pre-pressure custody projection
that refuses an unsuitable host before execution. It also validates the full
receipt before custody destruction, retries transient lifecycle/metering
failures inside existing bounds, corroborates heartbeat-loss recovery, and
makes source-free sealing resumable. These are feasibility and evidence
integrity corrections, not a scale pass, SLO, production-bound increase,
freeze, execution, release, or Epic-40 closure authorization.

Neutral-34 then advanced through V24 requeue recovery and exact return-to-A,
but stopped at interruption partial verification on an orphan relationship
`.stage-*` directory. Late relationship fence, extraction-pin, and pre-commit
publish failures now abort their unpublished stage immediately; successful
publish remains immutable and the one-hour lifecycle sweep remains only a
fallback. This closes a production artifact-hygiene defect without changing
the V25 schema, gate clock, authority, scheduler, or release posture.

The subsequent production/harness audit closes the cheap-testable failure
boundaries: full lifecycle cycles retain backlog cadence after any owner error;
all relationship component stages and pre-install pins have immediate bounded
cleanup; V25 source/tool execution closes ambient Git/Go controls; its total
clock starts at executor entry; concurrent Execute/resume is serialized; shell
signals and cleanup refusals retain private state; incomplete preparation does
not authorize deletion; and teardown has a durable resumable checkpoint before
custody destruction. Prospective freeze also checks the exact ceremony
filesystem against V25 pressure growth and atomic evidence operations. V1-V24
remain historical.

The same audit therefore refuses freeze. Process sessions are only in-memory
and a one-shot process inventory cannot prove descendant absence after
SIGKILL/OOM, PID reuse, session escape, or fork/exit churn; a stable reviewed
supervisor/sentinel or equivalent external proof is still required. Frozen host
digests also do not bind each later executed path, private binaries are not
reverified across restarts, and HOME/module/control caches remain ambient. The
shell serializes supported operations, but direct Prepare/Cleanup/Destroy calls
still lack one shared run-root lock and can race custody mutation. The
review host initially failed `go mod verify` on a modified shared module-cache
directory and its V25 72-GiB projection missed the below-80% watermark by about
4 GiB. Explicit cleanup removed the orphan test server and rebuildable Go
caches, restored module verification, and recovered about 45 GiB; the exact
prospective filesystem check still runs at every freeze. The remaining
structural gaps are direct refusals, not bypass candidates.
No freeze, ceremony, release, T40.13/Epic-40 closure, bound/topology change, or
scale/SLO claim is authorized.

T40.13a now closes fail-closed process sampling without changing a public wire
or V1–V24 behavior. V25 accepts only complete 128-KiB/two-second snapshots,
retains one bounded failure cause and count, and validates each accepted row's
kernel start identity, PPID, and normalized class. It keeps only 128 active
descendants and three counters and serializes an in-flight sample against phase
reset. The root binds synchronously before its sole `Wait`; the concurrent
handoff is reconciled before a root-absence decision, and a missing live root
still fails. Startup adds one root kernel read and one initial snapshot; steady
state performs at most one snapshot plus 129 bounded kernel-record reads each
250 ms, with an 8,192-row/8,192-sampled-lifetime phase ceiling and no
production-path cost. The one-second allocation sampler likewise retains one
cause and scalar count. Sanitized command failures retain both sampler
sentinels and cannot substantiate a V25 recovery decision. A `ps` candidate is
not every-fork proof; same-token reuse, escape, and hard-death absence remain
T40.13c.

T40.13b now closes cooperative cancellation and shutdown truth without claiming
durable descendant absence. Signaled/forced command exits and initially
surviving sessions keep a sticky unproven sentinel; incomplete V25 Prepare,
preexisting `.preparing` custody, operator cancellation, external parent
deadlines, and failed real-binary stops retain their controls and diagnostics.
The distinct frozen total-wall cause remains sealable. The shell forwards INT/TERM/HUP to
the tracked Prepare/Execute process group and suppresses cleanup, receipt, and
seal work. Execute publishes no terminal evidence once cancellation or shutdown
uncertainty is observed; if cancellation first becomes visible after deletion
starts, the durable checkpoint and shell operation state remain authoritative.
V1–V24 remain historical, and no production path or public schema changes.

T40.13c now closes durable hard-death descendant supervision for V25 on Darwin
and Linux. A token-bound sibling control is atomically created before the first
operation child; its crash-released controller lock and inherited kernel lease
make descendant drain independent of PID, session, and intermediate lifetime.
Only the owning controller may record drained/terminal state, so a hard-dead
controller becomes live while a descendant holds the lease and indeterminate
afterward, never falsely absent. Exact restart inspection is O(1), finalizer
children stay inside the lease, and teardown deletes custody and proves terminal
drain before retiring supervision. The external prepared/checkpoint controls
remain authoritative until exact retirement is durably confirmed, then are
removed. The supported shell prebuilds direct V25 Prepare, Execute, Cleanup,
and receipt roots; full process-launching regression/rehearsal suites remain
branch gates rather than ceremony-preflight work. On Darwin, four isolated
opt-in gates passed for Go-run transitive inheritance, direct
`zoekt-git-index`, Phebs-to-Surreal hard-death inheritance after sampler
shutdown, and direct Phebs backup/restore roots. These are branch evidence, not
independent proof of every compiler/linker/indexer/restore descendant and not a
rehearsal or ceremony pass. Exact receipt resume may finish terminal retirement,
but any supervision residue after that recovery blocks further publication and
seal. V1–V24 retained bytes and supported CLI flow remain historical; direct
legacy Destroy has stricter symlink and stable/retiring/retired
V25-supervision refusal. Production paths gain no work, and no freeze or
ceremony is authorized.

T40.13d is complete. Every V25 custody mutator now shares one persistent
crash-released run-root lock between direct APIs and the supported shell,
acquires it before custody/output admission, and revalidates bounded exact
plan, prepared, cleanup-control, or checkpoint bytes under that lock. Prepare
also refuses prepared output inside custody or the reviewed module checkout,
and its CLI no longer decodes the plan separately. Execute checkpoints bind
the prepared digest, while a no-checkpoint Resume is read-only unless a final
observation is already settled. Historical V1–V24 bytes and supported paths
remain compatible.

T40.13e is complete. V25 plan bytes now bind source-free digests of every
canonical host-tool path, while Prepare, Execute, and resumed teardown retain
the matching private paths and invoke them directly. Go is rehashed before
each private build command; Git core is rehashed before every source export,
authoring, checkout, and revision command; the exact SurrealDB path and digest
are passed to each private server. The four built private binaries form one
bounded digest snapshot that is rechecked before every serve, backup, and
restore launch; Phebs also rechecks exact SurrealDB identity immediately before
start and exact zoekt, focused-index, and Buf identities immediately before
each child launch. The supported shell retains and
rehashes its exact Go, Git/core, and SurrealDB tools plus all five prebuilt V25
commands across run-lock re-exec. Replacement, symlink, and PATH drift fail
before launch mutation. Full Go/Git tree hashing is fixed to admission and
terminal snapshot checks, never a poll or phase loop; later teardown checks
rehash only four executable files. Historical V1–V24 bytes remain exact.

T40.13f is complete. Fresh V25 prepared authority now digest-binds one
canonical custody-local execution-control manifest. Authoring, source export,
private module/build work, server/recovery launches, and restarts use only its
private HOME/XDG/temp/cache paths and reviewed Git exec path; ambient Go, Git,
HOME, module, temp, PATH, and shell controls are absent. A fresh module cache
is verified and hashed under the existing 100,000-entry/2-GiB tree bound,
compared once after offline builds, and removed with the un-hashed build cache
before runtime. The shell now builds eight exact commands from fresh private
caches, including the returned-bundle inspector added by T40.13g, removes both
caches, reopens one small digest-bound control across lock re-exec, and cleans
only its owned ceremony-root directory.
Historical V1–V24 execution behavior remains unchanged.

T40.13g is complete. A returned package is parsed as untrusted input within
fixed compressed, expanded, entry, type, and per-file bounds before any output
is created. Verification requires a reviewed signer fingerprint or package
digest supplied out of band; the bundled allowlist and sidecar cannot authorize
the package. The authenticated checksum manifest must name exactly its eight
canonical evidence files before they are opened, and the same trust root binds
the frozen plan identity. One owned temporary extraction root is removed on
success, error, or signal.

T40.13h is complete. Existing Ed25519 private/public files must derive the same
canonical public identity before freeze or seal. The three final seal files now
form one authenticated resume transaction: exact manifest and checksum stages
are validated against the frozen run, the checksum signature is authenticated
before publication, and the existing promotion helper syncs each stage and
every parent-directory transition. Zero-, one-, two-, and three-promotion
crashes converge to the same retained bytes; a differing final authority is
never overwritten, while an invalid interrupted non-authority stage is durably
discarded and regenerated. Only the exact ten-file result can verify as
complete.

T40.13i is complete. V25 private controls now share one no-follow regular-file
reader with byte-bound-plus-one admission and stable pre/open/post identity;
their typed decoders require canonical single-value bytes through EOF. The
supported driver uses a ninth digest-bound inspector for exact plan/envelope,
checksum, shell-control, and directory decisions, while live extraction and
custody scans use bounded descriptor enumeration instead of glob/full reads.
Historical V1–V24 plan/evidence bytes retain their prior decoder behavior.

T40.13j's overflow-safe ceremony arithmetic, T40.13k's complete
executor-admission accounting, and T40.13l's cost-first operator gates are
complete. The original T40.13 gate remains open for its clean exact commit,
bounded regressions, independent review, and separate authorization.

The full-gate review of `97576e3319b565ab3af3fb407b7a361e552ee974`
returned `FIX-FIRST` on two boundaries. The remediation now preserves both
operation and aggregation failures at every shared metric merge and preserves
an existing measurement error alongside incomplete meter inventory. It also
supersedes the earlier atomic “exact executed-tool” interpretation: V25 uses a
dedicated, single-operator host whose package, OS, tool, and other same-UID
mutation is explicitly prohibited from preflight through source-free
packaging. The shell driver is the only supported V25 ceremony admission path;
its commands require the fixed host-stability attestation before admission,
while direct `cmd/t4013-*` binaries remain low-level harness/library interfaces.
Path/content/tree checks remain bounded defense
in depth against pre-check and persistent drift; they do not prove kernel-
executed bytes after pathname verification. The Go inventory is only its
committed execution-core subset. The Bash interpreter/builtins and `awk`,
`basename`, `chmod`, `cmp`, `cp`, `date`, `df`, `dirname`, `du`, `env`, `find`,
`grep`, `lsof`, `mkdir`, `mktemp`, `pgrep`, `ps`, `readlink`, `rm`, `rmdir`,
`sed`, `shasum`, `sort`, `ssh-keygen`, `sysctl`, `tar`, `uname`, `uniq`, and
`wc` driver utilities are an enumerated trusted host TCB. Independent re-review
accepted exact clean commit `7696f047e8e936d96887af736c707991f494a94b`
with no critical, high, or medium finding, closing the code/review prerequisite
only. Full bounded regressions, real-binary rehearsals, the full
`internal/store` gate, a fresh unconsumed identifier, and separate freeze and
execution authorizations remain mandatory.
The exact-commit production-path rehearsal then refused the fresh 2.3-GiB
private module cache created by `go mod download all` under the unchanged 2-GiB
bound. V25 now hydrates only the exact admitted command dependency closures;
the four-tool closure measured 1.5 GiB and built offline without cache growth.
This correction requires a new exact clean commit, full gates, and independent
review before identifier selection or any separately authorized freeze.
The same rehearsal then exposed its pre-existing incomplete closed-host binding:
only Git-core was retained, so the source-free server could not receive exact
SurrealDB authority. The harness now reuses the complete V25 host observation
and plan-binding boundary before controls or builds. Corrected rehearsal and
independent review remain mandatory.
Real convergence then reproduced Darwin's `ps`-to-`kern.proc.pid` exit race.
T40.13a now permits at most two fresh complete retries only for a typed disappeared child,
under the same two-second deadline; all other identity failures remain sticky,
and failed attempts commit no evidence. This sampled-lifetime correction also
requires the corrected rehearsal and independent review.
The Darwin sampler correction closes those three rehearsal blockers without
widening retry or weakening identity equality. It replaces the split `ps` and
kernel-identity observation with bounded native parent traversal and one
coherent PID/PPID/start/class/RSS record per accepted process. A root-exit
marker crossing discards the whole attempt and permits exactly one fresh
handoff observation under the same two-second deadline; an already-observed
exit requires no descendants. Corrected real-binary, package, race, full
`internal/store`, and exact-clean-commit independent-review gates pass on
`afa297966f7129bf7930c0834e8808c3992f35c5` with no critical, high, or medium
finding. Integration, dedicated-host preflight, and separate freeze and
execution authorizations remain mandatory; no fresh ceremony identifier is
selected or consumed.
Neutral-35 subsequently consumed its exact V25 identifier and stopped honestly
after structural convergence because the sampler treated a coherent
same-PID/start-token executable-class change as impossible. Its sealed receipt
is `unclassified / failed_phase_measurement_unavailable`; teardown and the
source-free package verified, and the result establishes neither pipeline
failure nor scale success. T40.13m is complete on exact clean code commit
`97772bb69fba77feb06fa79317b401d1e0815575`: V26 models the event as one
bounded executable-image epoch with six fixed source-free directions, exact
plan/observation provenance, and source/destination epoch coherence while
leaving every identity, root, descendant, cadence, deadline, and cumulative
ceiling refusal intact. Complete exact package/race, real-launcher, readiness,
store, repository, and independent-review gates pass with no critical, high,
medium, or actionable low finding. Integration and exact-main dedicated-host
preflight are next. `t40r1-neutral-36` is only the nominal candidate;
identifier selection, freeze, frozen-plan review, and execution retain their
separate approvals.

Neutral-36 subsequently consumed its V26 identifier at exact main source
`acc5a23f046229c580b972bcbb0107f2f7062882`. Its signed source-free evidence
binds plan
`sha256:e2403ee87df84383e47b5b78a1f7fc1085425da3ec1b5af5f3214fa4e03ca9e7`,
observation
`sha256:141750ff0ae7da9af7e006bfb59cc260ff973abe02509e2e269474dea7c8d22d`,
receipt
`sha256:9d9ec605ad90ccd1010a920cb86c405656851349d85ccb0ac2243b18606e6ee6`,
and package
`sha256:e5ec0c04338b17d91064c160f34a1a78b6ba174773107bfd592d2bf80f0e0677`.
Preflight through return A succeeded. Interruption selected an attempt-zero
`extraction-partitions` chunk, restored source A, and stopped after 6,059,839
ms at `restart_start` with generic unsubstantiated
`failed_phase_measurement_unavailable`. The receipt does not retain the failed
data gauge or raw cause, recovery verification and later phases did not run,
and the result is neither a pipeline failure nor a scale pass. Teardown took
187,542 ms and retained neither derived data nor scratch source. The exact ID
and plan are permanently consumed; no private rerun is authorized.

T40.13n is now the only next scale ticket. Its implementation, corrected
bounded real-binary readiness rehearsal, exact-tree gates, and independent
exact-source review are complete. It requests integration only.
Fresh V27 accounting will carry one successful first-server raw end allocated
gauge through a private same-workspace one-shot boundary into restart, or take
an allocated-only baseline before launch when no boundary exists. Allocation
sampling and wall time begin at that prelaunch boundary; a failed prelaunch
creates no expected/active meter, while a launched server is tracked before
health. One optional path-free `data_measurement_failure` may retain only
schema `t4013-data-measurement-failure-v1`, `scope=custody`, an allocated or
logical gauge, `reason=deadline`, and the unchanged 30,000-ms deadline.
Archive/restore retains merged peak gauges rather than replacing them with one
terminal reading. Historical V1–V26 evidence remains exact, including explicit
`null` rejection at bounded observation/receipt/checkpoint decode; V27 receipt
decode adds one bounded canonical JSON re-encode/comparison to refuse duplicate
keys. Interruption
falls from 20 to 16 gauge boundaries (at most 60 to 48 `/usr/bin/du` attempts)
and archive/restore from 18 to 15 (at most 54 to 45); each boundary retains a
30-second cap. Readiness has five boundaries (at most 15 attempts/150 seconds)
and one additional private server launch/health cycle relative to its earlier
sequence. Ceremony server count is unchanged; one allocation sampler now begins
before the existing executable revalidation/launch and probes capacity at 1 Hz
during that bounded prelaunch window. The implementation, bounded regressions,
real-launcher, full internal/store, module, vet, lint, documentation, glossary,
shell, whitespace, and steady-state-cost gates passed on one exact tree.
Independent review found exact source commit
`b5d6b74da8644811c5e1bfffd658b73661797ee2` functionally clean; its one low
cost-record issue is corrected in source-identical documentation. Ben must
separately authorize integration. Only after integration may a
clean exact-main dedicated-host preflight precede fresh-ID selection, separate
freeze authorization, frozen-plan review, and separate exact-ID/digest
execution authorization. No completion, identifier, freeze, release,
T40.13/Epic-40 closure, topology/bound change, or scale/SLO claim exists yet.

The first V27 readiness attempt stopped honestly after 12 minutes of semantic
restart convergence. Read-only retained-state audit proved the product bytes
and all A extraction pointers were current, but the operational current row
still named a settled B schedule, so progress remained non-current with no
actor able to change it. This was not a measurement, timeout, or resource
failure. The shared extraction runtime now routes both completed-generation
reconciliation and exact-authority reuse through one schedule-coherence check.
A mismatched active or settled predecessor gets the existing fresh immutable
transition schedule for nonzero work. Zero-applicable authority instead retires
the exact mismatched current projection, keeps immutable history lifecycle-owned
and exact roots authoritative, writes no binding, and retains the established
`unavailable` progress state rather than fabricating a partition. Focused
active/settled and new/reused zero-work regressions supplement the exact
settled-B → reused-A crash/no-op regression and corrected rehearsal. Steady
exact active reuse is unchanged. Absent reuse adds a second
schedule query and no binding read; settled reuse adds that query plus two
pointer-sized binding reads across initial and repeated target resolution. On a
nonzero mismatch, reuse totals three schedule-query/binding-read pairs—two
before enqueue and one inside—while completed reconciliation totals two—one
before and one inside. Enqueue then adds one pointer-sized binding write and
one existing schedule transaction under the existing shard lock and chunk
limits. A zero-work mismatch stops before enqueue: reuse performs two pairs and
completed or new reconciliation performs one, followed by one exact
current/schedule point-read retirement transaction, active-only status update,
and current-row deletion. A concurrent successor makes it stale without
mutation. No API shape or persistent schema changes.

Exact-tree evidence includes the complete T40.13 package (97.258s), race
package (109.786s), real-launcher proof (60.902s), 20 repeated
V27/schema/accounting runs (248.065s), final semantic (124.67s), stale-worker
(31.30s), and structural (138.58s) readiness cohorts, and full
`internal/store` (1065.618s standalone; 1109.512s within an uncached repository
run). Every `internal/` package passed. An earlier host-native process-sampler
`EPERM` made one structural attempt invalid after healthy startup; the exact
tree passed its bounded rerun and no process survives. The extra repository
aggregate is baseline-red only on inherited T30.6m component budgets, Git-2.54
T32.3 retained bytes on the Git-2.50.1 host, and T32.4's pre-repin bindings;
all four assertions fail identically at the base commit. This ticket neither
claims `go test ./...` green nor reauthors those unrelated fixtures.

Neutral-37 subsequently consumed its V27 identifier at exact main source
`3d6ecf294e655c9121ea57cdec24b23b91a1cf4e` and plan
`sha256:52b6c9d519358d84c34cbdb5b49bc44eff22005298e4a281ed3a598d82896f5b`.
It ran for 317.565 minutes, proved the selected interruption lease was
requeued, and stopped at `interruption/partial_verification`; exact teardown
then left no derived or scratch-source custody. The reconciled controlling
signed attribution is
`recovery/direct_recovery_failed/p6_investigation/substantiated`. Its V27
evidence cannot identify which publication owner or partial kind remained,
prove simultaneous capture failure, or distinguish the partial-clear deadline
from a scanner error. Neutral-35, neutral-36, and neutral-37 are three distinct
outcomes: respectively the 63.325-minute V25 cold stop, the 327.939-minute V26
`restart_start` stop, and this V27 `partial_verification` stop. The V26 meter
defect predated neutral-35; no causal sequence in which each fix introduced the
next defect is established.

T40.13o is now the only next scale ticket. Its implementation guards the two
V27 typed-nil deadline paths; moves relationship/resolver cancellation before
the publication commit point and removes redundant full validation after a
marker exists; validates and atomically retires every raw generation, restore,
and sparse extraction stage before workers with a durable parent sync; and
advances fresh ceremony evidence to V28. Startup preserves bytes and
modification time and performs no deletion. Scheduled lifecycle alone promotes
retired stages that are at least 24 hours old or older than the newest two for
their repository and kind into collecting with a durable sync; collecting
stages then drain unconditionally across bounded turns and restarts. A
post-start raw stage remains untouched and makes completeness lower-bound. V28
may pair one closed owner with
`publishing_marker|stage_directory` only on a stopped
`interruption/partial_verification`. Its existing poll performs one bounded,
fixed-order six-root scan, exposes no path/raw error/content, and excludes the
candidate namespace from ceremony attribution. Production stage recovery still
owns package candidate residue. Publication transitions are cheaper by one
full validation. Startup checks cancellation before every raw collision
preflight and rename, syncing any changed prefix before return. Extraction
validation/retirement is one startup pass under one acquisition of the shared
lifecycle-mutation lock, capped at 2,000,000 charged work operations, stats,
and candidates, eight scanner-charged peak descriptors, and 510,000,000 name
bytes; the eight excludes the existing lock descriptor. It reads
names/types/stats, not contents, and deletes nothing.
Startup inventories at most 4,096 regular plus 4,096 sparse repository
namespaces and may retain those 8,192 bounded identities. Each lifecycle turn
acquires that existing shared lock once and holds it while inventorying at most
4,096 repositories in one publication or sparse phase.
Either path accepts at most 20,000 direct entries from one selected repository
directory and retains at most one additional entry only to detect overflow.
Each lifecycle turn also admits at most 64 stage candidates, sixteen removals,
256 stats including descriptor-open stats, eight peak descriptors, and 1 MiB of names.
A clean pass becomes exact/idle; raw post-start residue is lower-bound without
permanent five-second backlog. Eligibility and drain are lifecycle-only,
adding no product request/query, repository sync-tick, corpus/shard read, hash,
cache, worker, or child. No new lock primitive is added. New non-reused generation construction adds one
serial result-directory fsync per accepted domain, zero through 64, before the
existing final stage sync/rename. Reuse/no-op adds none; a failed rebuild retry
repeats that bounded work, restore's existing per-domain sync is unchanged, and
the new syncs extend the existing one-of-64 reconciler shard-mutex hold.

The pre-review tree passed 20 deterministic V28/typed-nil repetitions (1.495s),
complete T40.13 package (103.560s), full package race (113.820s), focused
publication race, real-launcher custody proof (62.074s), readiness, every
uncached `internal/` package including standalone `internal/store` (983.068s),
and module/vet/lint/docs/glossary/shell/whitespace. One 233.93s complete
readiness attempt was invalidated only when structural met host-native sampler
`EPERM` after healthy startup; semantic and stale-worker passed, no PID/session
survived, the diagnostic root remains retained at
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-403545186`,
and the one bounded structural rerun passed in 194.515s. The inherited
T30.6m/T32.3/T32.4 repository-aggregate fixture reds were not needlessly
duplicated; the required internal/store bar is green. Recorded independent
review of exact commit `704c2360e75e8a7d7068cbf3cd49b492a84cb50d`
reported critical/high 0, medium 1, and low 1; the cancellation and cost-record
findings above are corrected. Corrected-tree cancellation (20 repetitions),
extraction normal/race, lifecycle, command, and static/docs gates pass. Its two
structural confirmations were both invalidated after healthy `http_ready` by
host-native sampler `EPERM`; both diagnostic roots are retained, PIDs 79356 and
81088 are gone, and the bounded rule permits no third retry. Fresh review of
exact corrected source `710f66f440464c4dabf1723f98134cb941c07232` found
critical/high/medium 0 and one low lock-cost wording gap; source-identical docs
commit `c4dfdabbd594b5f841b92058923343382d6cf5aa` corrected it and passed exact
re-review with every severity count zero. Only a later host-clean structural
confirmation remains. No integration, identifier, rerun,
freeze, execution, release, T40.13/Epic-40 closure, topology/bound change, or
scale/SLO claim is authorized.

`t40r1-neutral-38` then passed preflight and cold on exact V28 source
`b79406d12f517caed08f07120ca91b0ac1fbe471` and plan
`sha256:da1804e13afb7b04a45a462552b75627ebb3a6e58bbe95c03c4fbad8080d2506`,
but stopped in phase 3 `warm_noop`. Its healthy startup and complete phase meter
both sampled two Git lifetimes; authority stayed unchanged, no index or
publication mutation occurred, two controls were reused, and teardown retained
no derived or scratch-source custody. This exposes a ceremony-contract mismatch,
not a production pipeline failure: ordinary boot intentionally enqueues
connection freshness work, while V28 required zero Git children across the
whole restart.

T40.13p advances only fresh ceremony contracts to V29. Warm reuse now requires
the phase Git count to equal its paired healthy-startup count, so any sampled
post-health Git lifetime still fails. Exact snapshot equality, zero index and
publication movement, unchanged authority, and positive reuse remain mandatory;
V1–V28 remain exact. The small real-binary structural readiness path now invokes
production `warmNoop` after cold convergence. Production behavior and costs are
unchanged; ceremony execution adds fixed startup-record and bounded schema
lookups, closed identity/outcome checks, checked three-counter ceiling sums, and
one scalar equality, while readiness adds one bounded restart. Complete gates,
exact-source independent review, integration,
exact-main preflight, and separate neutral-39 freeze/review/execution decisions
remain ahead.

The corrected content passed 20 V29 repetitions, complete package/race,
real-launcher custody, all non-store `internal/` packages, standalone
`internal/store`, module, vet, lint, docs, glossary, shell, and whitespace.
Fresh independent review of exact commit
`06b6e61e2316b33b5cad326e9efa2c9b97194309` found no critical, high, medium, or
low issue after two medium trust/custody findings and one low cost-record
finding were corrected. One pre-review structural run passed production
`warmNoop` with startup/phase Git counts 3/3. The final content still lacks its
host-clean exact-commit readiness gate: one attempt stopped before launch on a
fresh-module network timeout and one unchanged retry reached healthy
`http_ready` before the intentionally sticky Darwin sampler `EPERM`. Both
diagnostic roots remain, no process survives, and no further retry was made.
The next step is one later host-clean complete readiness pass, not integration
or freeze.

Neutral-39 subsequently returned status 1 in a source-free package whose digest
is `sha256:681aef5bb4ebe77c63ed564f5dfe499609a76738c3172b7a58e9c9f87d6a43cb`.
The wrapper monitor's last useful line showed `prepare/admission` with unknown
custody age; its terminal-summary `jq` command then failed because of shell
quoting. That monitor failure did not alter the ceremony, but the returned
custody preserves no raw private error. The run therefore establishes neither
sampler `EPERM` nor a named phase failure.

T40.13q advances only fresh ceremony contracts to V30. A process-sampler
failure after successful startup inspection retains the valid log/health/stage
projection, with unavailable counters represented by the existing zero
sentinel and explicit `process_sampling_unavailable=true`. Process-only and
allocation-only measurement stops become typed;
mixed measurement failure stays generic. Warm restart now incrementally tracks
phase-local candidate and job lifecycle reports within its existing
revalidation deadline. A `done` candidate decision of `warm_noop`,
`cold_reuse`, or `marker_recovery` is necessary but not sufficient: released,
failed, or requeued work rejects; claimed, started, deferred, and yielded work
remains unresolved until its later `event=done,outcome=success`; exact authority
is re-inspected on each attempt; and one existing five-second convergence
interval must contain no new candidate/job report after all jobs resolve. The
phase meter finishes once there. Its single finished `PhaseMetrics` process
snapshot refreshes paired startup process counters without another sampler read,
while log/stage/wall facts refresh at the settled boundary. The atomic
warm→delta handoff transfers that process boundary with the exact log EOF and
performs a bounded post-reset tail scan: any candidate/job report or partial
tail refuses the boundary, complete unrelated lines advance the warm EOF, and
later exact claimed/started reports remain delta, so the Git oracle admits
health-before-sync/reuse but no post-boundary Git;
V1–V29 stay exact. Phase 7 requires coherent sorted post-ballast owner attempts,
one final exact-normal capacity observation after the latest owner, and then
unchanged protected authority; it does not claim capacity stayed normal through
that cycle or wait for hourly-idle eligibility. Independently, production
recovery brackets the sorted cycle-start owner with exact-normal capacity and
requires a wholly normal, error-free, drained sorted cycle before hourly idle.
Phase 8 compares caller generation, relationship semantic digest, and every
other product value while ignoring only indexed commit and the replaceable
relationship generation/root digest.

Phase 9 restarts the server and requires all sorted 14 owners fresh and
`state=ok`. Thirteen non-`durable-jobs` owners must be exact and drained;
`durable-jobs` truthfully remains `lower_bound` and may retain backlog because
live writers prevent an exact oracle. Capacity must be exact after the latest
owner and current stable authority unchanged. Its source-free receipt records
the bounded state/completeness/scanned/deleted/backlog rows. This narrows the
live oracle: it does not claim eligible deletion or individually prove
rollback, active lease, marker, or store-pin roots. Those protection semantics
remain mandatory `internal/lifecycle`, `internal/store`, and publication
regression gates. Restored readiness uses the same owner-specific freshness
rule and V30 comparator.

V30 ceremony servers enable synchronous exact candidate and job lifecycle sinks
for all seven runners. Any encode, size-cap, sink, or panic failure latches
cancellation and produces a nonzero terminal error after worker join. Ordinary
runtime reporting remains advisory when exact-control mode is absent.

Production lifecycle scheduling adds a pressure-recovery cursor suffix plus one
full normal cycle at the existing five-second cadence. Only while capacity stays
exact-normal and every owner result is error-free and drained is that at most 28
turns for this 14-owner inventory and at most 64 under the at-most-32-owner
controller bound. Any owner error/backlog or unavailable/non-exact capacity
removes the 28/64 turn bound and keeps the runner at five-second cadence. The
phase-7 and phase-9 waiters make no 28/64 claim and remain governed by their
fixed phase deadlines. Truthful `durable-jobs` backlog does not block either
waiter; an owner error or backlog in one of the 13 required drained rows keeps
owner progression at five-second cadence until the deadline. Healthy normal
hourly cadence and all product query/request, repository sync, retry/no-op,
publication, lock, cache, persistent schema, disk, memory, and child-process
paths are unchanged. Ordinary startup adds one closed exact-control environment
lookup and branch; when absent it allocates no report channel or sink and adds
no persistent work.
Ceremony phase 9 adds one restart, the same bounded owner/status work, and
14-row validation with 13 exact/drained predicates and one durable-job
lower-bound predicate. Warm restart adds incremental phase-local candidate/job
lifecycle parsing, exact-authority reinspection per attempt, one existing
five-second quiet interval, one finished-metrics startup refresh without a
second process sample, one bounded post-reset log-tail scan, and the atomic
process/log-EOF handoff under the unchanged revalidation deadline. Phase 7
adds one existing post-recovery authority inspection. Only exact-control mode
adds synchronous per-report log writes; ordinary steady-state reporting remains
advisory. No production request, job, child, or new deadline is added.

T40.13 Phase-8 cadence correction (2026-08-26; supersedes only the pressure
cadence wording above): the failed focused run and read-only retained-custody
count show that the larger frozen structural observation tree has 1,547
deletion units and requires 97 observation-v2 turns at sixteen deletes per
turn. One-second fair rotation still exceeds the fixed ten-minute deadline.
Ordinary backlog/error/capacity retry remains five seconds and healthy idle one
hour; only `collect`/`refuse` or the existing pressure-recovery latch caps the
serial turn delay at 250 milliseconds. The exact-tree regression budgets no
more than 350 seconds of scheduled delay after worst alignment and fresh
cycles, leaving runtime headroom but establishing neither an SLO nor a Phase-8
or full-ceremony pass. Elevated mode offers at most four bounded owner-turn
starts per second before sweep duration until a clean cycle, 20x the prior
pressure scheduling frequency; per-turn limits,
fair order, lock scope, concurrency, schema, and V30 predicates remain exact.
The focused real-binary rerun and complete review gates remain mandatory.

The separately run human Phase-7 `semantic-stale-worker` selector passed in
32.22 seconds for the subtest, 197.17 seconds for the top-level readiness test,
and 197.835 seconds for the package command. Its retained terminal record does
not bind exact HEAD and clean-checkout proof, so exact candidate attribution
remains open. The corrected Phase-8 wrapper subsequently passed from exact
clean commit `37fba2896f500104fa8283914ed19b8a003e3a24`: the subtest took
252.48 seconds, the top-level readiness test 310.35 seconds, and the package
command 311.030 seconds. That commit is integrated into `main`; a later host
check found no matching Phase-8 diagnostic path or live rehearsal/Surreal
process. These focused results supersede only the earlier Phase-7/8 run
requirements. Prior-phase handoff, full-corpus/root-volume scale, signed
custody, release, Epic closure, and scale/SLO claims remain open.

The separately opt-in human Phase-9 (`archive_restore`, `phaseOrder[8]`)
rehearsal now calls the exact production archive/restore coordinator after a
small structural A→B→A-return. Its first working-tree run passed in 88.70
seconds for the subtest and 147.917 seconds for the package command, including
exact meter/server accounting and shutdown; cleanup removed its current-run
private workspace and left no matching process. Older retained readiness roots
remain untouched. The run preceded the immutable implementation commit, so
repeat it unchanged on the clean commit before candidate attribution. This is
not human Phase 10 collection, a full-scale or signed-custody result, release,
Epic closure, or a scale/SLO claim.

Human Phase 10 subsequently passed its unchanged selector from exact clean
commit `15487bbf15b602b04d81fbae6b989777b5cac44d`: the subtest took 147.89
seconds, the top-level readiness test 205.45 seconds, and the package command
206.121 seconds. It emitted the production boundary marker, removed its
successful workspace, retained no diagnostic root, and left no matching
process. That commit is integrated into and pushed as `main`. This closes only
the Phase-10 exact-rerun requirement. The earlier Phase-9 exact-clean rerun and
all full-custody, scale, release, and Epic-closure gates remain open.

The next isolated seam is human Phase 11 (`authorized_query`,
`phaseOrder[10]`). Its separately opt-in real-binary rehearsal enters with
semantic A converged and stopped plus structural A-return converged and live,
then invokes the unchanged production V30 coordinator. It requires the real
semantic restart, stable authority for both profiles, the fixed unauthorized,
search, service-inventory, relationship, and citation oracles, exact two-meter
accounting, and shutdown. Existing unit tests retain retry and source-free
failure-projection coverage, so the ticket adds no production path or duplicate
query harness. The rehearsal uses no sparse image, ballast, archive, or fixed
lock. Bounded gates, independent review, an immutable commit, and an unchanged
exact-clean run remain required before candidate attribution; the clean entry
does not prove prior-phase handoff, full-scale custody, release, closure, or SLO.

The unchanged human Phase-11 selector subsequently passed from exact clean
commit `c2e6eed8faab01854f3af94264ec3054487c877e`. It emitted the required
authorized-query boundary marker; the subtest took 44.46 seconds, the top-level
readiness test 104.93 seconds, and the package command 105.614 seconds. Both
stable-authority waits, the fixed query/citation oracles, exact control-read and
two-meter accounting, the mandatory query-member minimum, listener transfer,
restart, cleanup, and shutdown passed. No current-run diagnostic root or
matching process remained. This closes only the focused Phase-11 exact-run
requirement. The earlier Phase-9 exact-clean rerun, prior-phase handoff, full custody, complete
ceremony, release, Epic closure, and scale/SLO claims remain open. Ben
authorized fast-forward integration after this source-identical result record;
preflight, freeze, execution, and push remain separate.

The last isolated seam is human Phase 12 (`teardown`, `phaseOrder[11]`). Its
separate opt-in real-binary rehearsal uses a receipt-valid source-free fixture
for the completed prefix, but takes the real run-root lock and
prepare→execute supervision, starts one structural Phebs/Surreal session, and
calls the unchanged V30 coordinator. A new private `custody/` child is the only
recursive data-deletion target; evidence, prepared publication, the lock, and a
sentinel are siblings, while named external protocol artifacts are retired and
successful test cleanup removes the parent. A pass requires proven session shutdown, nonzero data gauges,
terminal checkpoint retirement, durable exact absence, terminal observation publication and completed receipt validation,
terminal supervision/prepared/checkpoint retirement, sibling preservation, and
lock lifetime through simulated Execute return. No production code is added.
The deterministic package regression remains authoritative for
checkpoint-before-delete ordering.
Bounded gates, independent review, an immutable commit, and an unchanged
exact-clean human run remain required. The fixture does not prove Phases 1–11,
their handoff, full scale, signed custody/evidence, a complete ceremony,
release, closure, or SLO; only after this focused gate passes should the next
full ceremony be planned.

The first exact-clean Phase-12 attempt at commit
`cbbb873d251b56c0a2cd645ab02c99ee3a60d90a` stopped before prepared
publication, supervision, or supervised Phebs/Surreal server launch because its
synthetic manifest copies retained bounded projection-profile names rather than the frozen
ceremony identities. Review found no matching process and purged the retained
207 MiB root. The correction maps only those copied labels through the existing
frozen constants and adds a fast schema invariant; production validation and
authored bytes stay unchanged. At that point, corrected exact gates, re-review,
and the exact-clean run remained required before full-ceremony planning.

The corrected Phase-12 selector then passed unchanged from exact clean commit
`81d0a7a73214dbfa906e01eb3a8d611e8e950b2a`. It emitted the exact-commit and
custody-retirement markers; the test took 87.79 seconds and the package command
88.348 seconds. Its graceful shutdown, exact gauges, terminal checkpoint
retirement, durable absence, terminal observation and completed-receipt
validation, external protocol retirement, sibling preservation, lock
lifetime/reacquisition, frozen-host validation, and three clean-checkout
assertions passed. Successful cleanup left no matching temporary root or
process. This closes only the focused Phase-12 exact-run requirement. The
exact package gate separately passed the deterministic checkpoint-before-delete
ordering regression. At that point, the Phase-7 and Phase-9 exact-clean reruns
and all full-ceremony, release, closure, and scale/SLO claims remained open;
integration, preflight, freeze, execution, and push remain
separate actions requiring their own authorization.

The unchanged Phase-7 `semantic-stale-worker` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`ce6212974f40fc452a124345c751a2b5bd473f9f`. It emitted the required boundary
marker; the subtest took 32.52 seconds, the top-level readiness test 91.40
seconds, and the package command 92.025 seconds. Its pending and HTTP-409 lines
were bounded nonterminal convergence observations superseded by the terminal
pass. Successful cleanup removed the current workspace, and a separate check
found no matching rehearsal process. This closes only Phase-7 exact
attribution. Phase-9 exact-clean attribution and all full-ceremony, release,
closure, and scale/SLO claims remain open; integration, preflight, freeze,
execution, and push remain separate actions requiring their own authorization.

The unchanged Phase-9 `structural-archive-restore` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`0d4cd82132bca5a0c48d1d1df9e377a0720c4bb9`. It emitted the required boundary
marker; the subtest took 89.04 seconds, the top-level readiness test 148.55
seconds, and the package command 149.292 seconds. The final active
relationship-progress row was a bounded nonterminal convergence observation
superseded by the terminal pass. Exact two-meter/two-server accounting,
shutdown, and successful current-workspace cleanup passed; a separate check
found no matching rehearsal process. This closes Phase-9 exact attribution and
the separately identified Phase-7/9 reruns. Cross-phase handoff, complete
signed ceremony, release, closure, and scale/SLO claims remain open.
Integration, exact-main gates, fresh-ID selection, freeze and frozen-plan
review, execution, and push remain separate actions requiring their own
authorization.

The pre-review focused and bounded regressions, complete package/race,
real-launcher custody, production-path readiness, every `internal/` package
including full store, module verification/compilation, vet,
repository-pinned lint, documentation, glossary, shell, and whitespace gates
passed on 2026-08-25. Independent review of exact commit
`4b40beb28e1549a4d269a7a7e0d9ed604c775c4b` recorded 0 critical, 0 high, 3
medium, and 3 low findings. The correction tree closes the whole-cycle capacity
latch, exact stale-reap, teardown-sentinel, warm-cursor, absent-allocation, and
owner-count findings. Exact correction commit
`ec4f2500d1b68dcbe539667d5833fdf694bc5adc` passed every machine gate, but
re-review recorded 0 critical, 0 high, 0 medium, and 2 low findings: two
pre-confirmation paths did not deterministically close the settled warm cursor,
and two owning cost sections contradicted the startup lookup record. The next
correction keeps the proven FD under unconditional phase-meter finish cleanup
and fixes those sentences; focused normal/race and documentation checks pass. A
new immutable commit, complete exact-tree gate, and fresh independent review
remain pending. Merge, exact-main preflight, fresh-ID freeze, execution,
release, T40.13/Epic-40 closure, topology/bound changes, and scale/SLO claims
remain unauthorized.

Final exact source commit `50df638ad065814f4a9ea75c4f7493a622df3de0`
closes the cleanup-only metric-retention finding and passed fresh independent
review with all severity counts zero. Package/race, real-launcher, command,
module/compile, vet, pinned lint, documentation, glossary, shell, and whitespace
gates pass. The integration gate remains red on host evidence: a complete
readiness run and its one bounded unchanged confirmation both reached healthy
structural `http_ready` before sticky Darwin root-sampler `EPERM`; semantic and
stale-worker passed in the complete run. Root denial is not retried because a
blind interval could omit a descendant lifetime. The full internal command also
timed out in fresh store schema application after 1320.596s, while the exact
isolated subtest passed in 11.349s and every completed
package was green. No child survives. A later host-clean complete readiness and
full internal/store pass are required before integration can be requested; no
merge, exact-main preflight, identifier selection, freeze, or execution is
authorized.

T40.13r supersedes two parts of that host-gate record. A later serial
`internal/store` run on the exact pre-ticket candidate passed in 1003.037s, so
store is no longer an unresolved blocker. The Darwin refusal was not a root
denial: retained custody proves PID 554 was a still-parented descendant but did
not retain its executable identity. A bounded host reproduction establishes
the mechanism: the production compatibility monitor's setuid-root `/bin/ps`
child yields the same coherent task-all-info `EPERM`, and a separate Darwin
ceremony-session inventory could create the same helper during admission.
T40.13r removes both Darwin helper launches through bounded native process
records while preserving the sticky fail-closed sampler and V1-V30 evidence.
Focused changed-path gates and fresh independent review precede one host-clean
exact-commit readiness rehearsal. Integration, exact-main preflight,
identifier selection, freeze, execution, release, Epic closure, and scale/SLO
claims remain unauthorized.

Exact implementation commit `9bee810cd692d831993ff2e4784fb067f628b768`
subsequently passed the focused native/custody repetitions, changed-package
normal/race gates, module verification, vet, pinned lint, documentation,
glossary, shell, and whitespace and received independent review with all
severity counts zero. Its unsandboxed host-clean structural readiness rehearsal
passed in 373.567s with exact teardown. The serial `internal/...` run reached
the default ten-minute package alarm while `internal/store` opened a fresh
engine, but every completed package was green; the unchanged exact standalone
full `internal/store` package passed under its 30-minute allowance in 993.780s.
No SurrealDB child or port-65499 listener survives. T40.13r's review, readiness,
and full-store blockers are closed, making the source-identical branch eligible
for a separate integration request. No merge, exact-main preflight, identifier
selection, freeze, or execution is authorized by this record.

Independent review selected one medium correction to that record: structural
alone did not satisfy the inherited complete-readiness gate. The unchanged
implementation then passed the remaining `semantic` and
`semantic-stale-worker` legs together in 390.868s. The 373.567s structural
result is preserved across the intervening documentation-only commit because
no compiled, embedded, fixture, or harness input changed. All three readiness
legs are green for exact implementation commit
`9bee810cd692d831993ff2e4784fb067f628b768`; no rehearsal process or
port-65499 listener survives. A fresh exact-HEAD documentation re-review is the
only remaining T40.13r branch-close check, with all merge and ceremony
authorizations still separate.

The integrated `t40r1-neutral-40` run then stopped honestly in phase 6 and
retained custody for reviewed purge. Its closed attribution identifies an
`observation_publication` `stage_directory`, not a scale result. T40.13s is the
only next ticket: retire an exactly validated redundant same-generation
`.stage-*` through the existing `collecting-stage-*` lifecycle and accept the
V28 retained-partial schema in later frozen versions. Focused package/race,
documentation, and independent review must pass before deciding whether to run
one small phase-6 rehearsal. Do not re-execute or purge neutral-40 custody, and
do not merge, freeze a fresh identifier, execute another ceremony, advance
Epic 41, or claim scale/SLO evidence from this stop.

Exact implementation commit `0e5eba0109e632b9a1bd8f24c9f876aca5146e68`
then passed the affected normal/race, repeated focused, vet, documentation,
glossary, and whitespace gates. Its exact-clean real-binary semantic rehearsal
passed the phase-6 interruption/restart and partial-state-clear boundary in a
295.38s subtest; the top-level rehearsal test took 456.36s and the package
command completed in 456.991s. Backup/restore and restored lifecycle/query
verification also passed, successful cleanup removed the temporary workspace,
and no matching process remains. T40.13s is ready for the explicitly requested
fast-forward integration. This focused result establishes no phase 7–11,
full-ceremony, release, Epic-closure, or scale/SLO claim; review the phase-7
stale-worker boundary separately before another full ceremony.

The later exact-main freeze attempt first advanced the permanent consumed-ID
fence through neutral-40, then stopped prospectively because 138.13 GiB free
did not satisfy V30's 168.69-GiB projected minimum. A separate read-only purge
review reverified the frozen digest/signature and proved process, listener,
mount, and lock absence. With explicit operator approval, only neutral-40's
whole 45.61-GiB `custody` directory was durably removed; its signed evidence,
prepared authority, supervision state, operation lock, ceremony root, and
signing key remain. There is no sealed observation or receipt, so the reviewed
phase-6 attribution did not become signed proof. This irreversible cleanup
establishes no phase pass, host-ready result, fresh freeze, release, closure,
or scale/SLO claim. Exact-main preflight and a fresh neutral-41 freeze remain
next; execution remains separately unauthorized.

The first post-purge preflight then refused the upper side of V30 pressure
reachability: 197,643,706,368 available bytes left too much ballast to fit
after the 72-GiB pre-pressure projection inside the 96-GiB custody ceiling. On
this 494,384,795,648-byte volume the exact available-space window is
181,130,218,415–194,540,402,299 bytes. A dedicated owner-only
7,000,000,000-byte logical (7,013,421,056-byte allocated) reservation now sits
beside, not inside, ceremony run roots. Prospective preflight passed with it
present after reporting 190,598,098,944 available bytes. Keep it unchanged
through execution and source-free package review; it is host-specific, is not
custody/evidence, and must not become a copied constant. The workaround changes
no frozen limit and establishes no ceremony result. Exact-final-source
preflight and neutral-41 freeze remain next; execution remains unauthorized.

Neutral-41 then froze and executed exact source
`a28e0573f0089c22dda610ad1bf065328d47865d` under reviewed V30 plan
`sha256:8799f5e63f61b44ecea7b3e08f607922715589a0832b0b2802f75824ad9fd507`.
Its independently verified source-free package
`sha256:8b29e86c7227752964addd1c5dc06c729ed53288d0371b6926c78dc4dc555423`
proves Phases 1–6 passed. Phase 7 completed its functional stale fence and
convergence but stopped when the final exact allocated-data `du` exceeded the
30,000-ms gauge deadline. Phases 8–11 did not run; teardown cleanly retired
custody and processes. The reviewed host-pressure reservation was then durably
removed. This is a harness-accounting stop, not a pipeline failure or scale
result.

T40.13t is next. V31 keeps exact `du` as the only strict data meter, raises
each whole-custody gauge deadline to 300,000 ms for a bounded 10-minute reserve
per allocated/logical pair, propagates that bound through every strict caller,
and adds only a closed path-free v2 typed deadline diagnostic. V1–V30 remain exact;
neutral-41 is consumed and neutral-42 becomes first admissible. Focused
timeout, propagation, diagnostic, historical-version, and identifier gates plus
an exact full-profile replay through Phase 7's terminal gauge are mandatory
before another freeze. Neutral-41 retained no physical custody, and its logical
owner counts plus Phase-6 byte maxima cannot reconstruct the filesystem shape;
a synthetic proxy is not accepted. The change adds no production steady-state
work and authorizes no merge, freeze, execution, release, Epic closure, or
scale/SLO claim.

The full-profile Phase-7 runner is now implemented on the T40.13t continuation
branch. It factors the exact production prefix through `stale_worker` once,
then uses the existing resumable stopped-teardown protocol at a V31-only
replay boundary before publishing a separate source-free result. The result
requires the real terminal data gauges, says pressure never started, and grants
no ceremony, scale, freeze, or release claim. Its fixed external lock and
inherited run-root lock span preparation through cleanup; any cancellation,
unproven shutdown, measurement failure, or cleanup uncertainty retains the
fresh private root. Exact-main `d18fde43` was rejected before replay because a
live-worktree build could consume ignored or index-hidden inputs. The correction
requires the reviewed commit as an independent 40-hex input, then materializes
and hash-checks that commit's wrapper with absolute host utilities inside a
fixed `/usr/bin/env -i` plus `/bin/bash --noprofile --norc` bootstrap;
direct live-wrapper invocation is unsupported. It then binds Git and Go,
compiles and runs from a fresh owner-only shared clone detached
at the exact commit under closed Git config/attributes/excludes/fsmonitor/
replacement-object/hooks controls, requires its
parent outside the original checkout, rejects every modified,
untracked, or ignored private-source input before and after execution, removes
ambient Go overlay/workspace controls, applies closed Git controls to nested Go
VCS work, uses fresh private build/module caches, and runs clone, checkout, and
Go beneath identity-pinning sentinels whose exact stopped group must contain a
live sentinel and no other member before release through a parent-held FIFO
descriptor. Any dead, extra, or uninspectable member is retained with the fixed
lock. A nested launcher installs terminating traps before it emits ready; the
parent retries an interrupted ready read and forwards a latched signal only
after consuming ready, so a signal before or immediately after readiness
cannot cross into an unstarted workload. Each boundary adds one sentinel
shell, one nested launcher shell,
two FIFOs, one parent-held read/write release
descriptor, one parent read-only notification descriptor, three empty-marker
creates, one status write plus rename, two notification writes, one release
write, one marker unlink,
and normally one but at most 100 host process snapshots plus about one second
of bounded quiescence waits. The sentinel alone holds the notification writer
while its workload runs with that descriptor closed, so a record or EOF wakes
the blocking parent read without polling. Exact job comparison adds one short
command-substitution Bash child at drain entry and one more only when a signal
handler enters. The same fixed-lock sentinel
supervises recursive private-cache/source retirement, making four child
boundaries. It forwards INT/TERM/HUP to the
pinned child group and rejects any late
server-stop error as the deliberate boundary. Success atomically records the
exact commit, result path, and digest inside the retained fixed lock before its
zero-status terminal PASS; both are required, and reviewed lock retirement is
separate. Preparation has a four-hour deadline,
execution retains its independent 12-hour ceiling, and the test alarm is 20
hours from binary start; fresh cache hydration and compilation precede that
alarm. Lightweight gates alone do not close T40.13t. A new immutable
exact-source commit, independent code review, the expensive replay, and review
of its source-free result remain next; freeze stays forbidden.

That exact V31 full-profile replay then stopped honestly in `stale_worker`
after 23,469.777 seconds. The 2,540.013-second convergence wait was still
moving—509 probes, 155 digest changes, and a last successful projection only
about one poll old—but its 32-entry diagnostic inventory filled with 15
alternating extraction-progress HTTP 409 `status_other` rows and pending rows.
The active schedule retained 272 materialized partitions, with 70 succeeded,
202 pending, and zero failed. Source-free cleanup observation
`sha256:c69ce4124464f22934a2cd5972898ad1a7143604dbe1fcabdddcefa2689d675d`
is coherent; the explicit later stale-chunk fence was not reached, so this is
neither a stall finding nor a Phase-7 pass.

T40.13u is now the active correction. Fresh V32 bytes endpoint-fence and
classify seven closed snapshot/authority 409 details as `409_stale`: two from
observation progress, two from extraction progress, and three from caller
generation progress. They retain one aggregate count plus first/last wall
times and one latest conflict outside the transition inventory. A later
recognized conflict replaces the hold; same-stage pending progress clears it.
Any non-recognized next probe, or wait recording, materializes the hold so
deadline, server-exit, all-conflict, and terminal evidence still seals.
Control-absence, unknown 409s, 5xx, 503, transport, and every other class keep
consuming the unchanged 32-entry bound. V32 additionally names the two exact
extraction-progress 500 details as `500_store` and `500_response` without
placing either in the retry hold; V1–V31 remain exact. The change adds
constant-time, constant-memory harness accounting only.
Its observation/receipt/checkpoint compatibility fence also adds one bounded
in-memory scan of already-read evidence and at most 16 waits on each decode or
resume path, with no extra I/O or child.
Focused/package/race/vet/docs gates, independent review, and a clean immutable
candidate precede any separately authorized full-profile replay. Merge,
freeze, ceremony, release, Epic closure, and scale/SLO evidence remain open.

The exact-clean V32 candidate
`968311621f389643365587f4ae588ba83c832e68` then passed the real full-profile
prefix through Phase 7 in 21,281.087 seconds. All seven phase oracles succeeded;
five waits counted six recognized progress-retry conflicts and still converged
with only five to seven retained transitions. The v2 replay result is
`sha256:0e17da4500e8000713ca8e3abc6f97041772b3d78bdb2bf3661589f5e5b84c75`,
bound to plan `sha256:8784172854b86275d55705e920e6bf6e0499910e3d254c961a41639a0f5a3005`
and clean-teardown observation
`sha256:6eaef4eb7cea706c2e9b5874a5e09e0e3978e6cdb6363fd316263c9650a8a426`.
The exact source-free result is retained under `spike/t4013/`. This closes the
T40.13u replay/result gate and makes the reviewed branch eligible for
integration; it does not exercise Phase 8 or later, authorize a freeze or
ceremony, establish a scale/SLO claim, or close Epic 40.

The integrated `t40r1-neutral-42` ceremony then stopped honestly in Phase 9
after archive/restore. Exact source
`4496d5e12ebc026e2a12e8011505207f6582aaf1`, plan
`sha256:6818fa92a235ecad3978b48e3a6d6d4f67eba9e9647035d5eb2cd134207ae080`,
and sealed source-free package
`sha256:9bb96d6c0dc059f6f34573c0b4469f8968eaf8fe3b89009ab39312ce5f94ec74`
show that restartable generation schedules were correctly absent after restore
while imported extraction/resolver/caller job projections still described the
discarded epoch. The retained failed extraction projection was therefore
misread as a current terminal successor before a restored schedule existed.
T40.13v is the active production correction: restore clears those three
downstream pointer/timestamp/version triples with generation controls, retains
all job history and the independent index projection, and lets the next exact
generic or candidate writer rebind its returned pending row. The terminal
oracle and every evidence/schema contract remain unchanged. Focused gates,
independent review, and the small Phase-9 rehearsal precede any integration or
new freeze; the sealed stop establishes no pipeline failure, Phase-9 pass,
ceremony pass, release, Epic closure, or scale/SLO claim.

Exact implementation commit
`d6fe7d41fef76750cf6454baf0fd2161c4c82378` then passed focused normal/race,
complete recovery, module, vet, lint, documentation, and independent review
with all severity counts zero. Its exact-clean real-binary Phase-9 rehearsal
crossed restored schedule absence, current successor creation, and a benign
409 before passing the archive/restore authority boundary in 90.55 seconds;
the package completed in 229.179 seconds. T40.13v's focused correction gate is
closed. Integration, exact-main preflight, freeze, a complete ceremony,
release, Epic closure, and scale/SLO claims remain separate.

Before neutral-43 freeze, T40.13w advances the launcher's permanent consumed-ID
fence through neutral-42. The retained neutral-42 directory blocks reuse today
but cannot substitute for the durable numeric fence after later reviewed
housekeeping. The focused change rejects 42 and first admits 43, adding no
phase, plan/evidence, custody, production, or resource work. Focused review and
integration followed by repeated exact-main preflight precede freeze; execution
remains a separate decision.

The integrated `t40r1-neutral-43` ceremony then passed exact preflight through
collection, including the T40.13v archive/restore correction and the first
complete fresh collection cycle, before its first structural authorized search
returned status 500. Teardown completed without retained derived or scratch
source custody. T40.13x preserves the signed source-free stop summary, retires
identifier 43, and adds a bounded Phase-9→10→11 discriminator before selecting
a correction. The leading hypothesis is deferred cold whole-reader validation
after the Phase-10 restart, but the sanitized receipt cannot prove that cause;
stale root revisions, an active publication marker, and other search failures
remain fenced until the focused test distinguishes them. No complete ceremony,
release, Epic closure, or scale/SLO claim follows.

Focused reproduction established the independently testable defect without
retroactively attributing it to the sanitized receipt: a request could expire
while its same-generation cache-owned exact validation remained healthy, and
the API collapsed that state into status 500. T40.13x now types only that live
deadline as warming, maps it to a fixed private-cause-free 409, suppresses
service repair, and leaves lazy startup and every exact validation fence intact.
The three-attempt V32 runner defers its final attempt only when the first search
reported exact warming and the second attempt remained retryable; arbitrary 500
is never retried, and V1–V31 retain their historical classifier, cadence, and
wait-cancellation evidence.
The combined Phase-9→10→11 small-profile selector also records the first and a
later structural search outcome beside unchanged exact authority. Both were
single-attempt successes in the corrected-tree pass; the selected subtest took
184.94 seconds and the package 238.435 seconds. Complete affected-package,
focused-race, vet, pinned-lint, module, docs, glossary, shell, and whitespace
gates passed; two final independent reviews reported all severity counts zero.
Integration can now be requested but remains separately unauthorized.

T40.13x is integrated locally at exact source
`356f155ba21a156bfbb26cd1d317feb0c0b8fe89`; push remains separate. T40.13y is
the final bounded late-phase handoff gate before another expensive ceremony.
It preserves the standalone teardown selector and introduces no production
behavior: a V32 hybrid fixture retains an explicitly synthetic completed Phase
1–8/global full-profile prefix, deletes every synthetic Phase 9–12 startup,
wait, phase, collection, query, and teardown field, then runs the unchanged
bounded archive/restore, collection, authorized-query, and teardown
coordinators. The structural server remains live until teardown owns shutdown.
The fast splice regression, complete normal/race, vet, pinned-lint, module,
docs, glossary, and whitespace gates passed; independent corrected-diff review
reported no finding. Exact clean implementation commit
`1f589e3ece12d60a625fa28fbad06156419359a5` then passed the real selector in
244.12 seconds for the test and 244.816 seconds for the package. It emitted the
completed Phase 9–12 custody-retirement marker, removed its unique private
parent, and left no matching process or port-65499 listener. This establishes
only the small completed late-phase handoff and custody-retirement protocol;
full-corpus deletion cost, signed ceremony evidence, complete ceremony,
scale/SLO, freeze, release, and Epic closure remain open.

The integrated `t40r1-neutral-44` ceremony then passed preflight through stale
worker and stopped honestly at Phase 8 before `pressure-restart` or ballast
creation. Its sealed V32 receipt records
`lifecycle/production_pressure_gate_refused` with the frozen substantiated
`reduce` verdict and clean teardown; archive/restore, collection, authorized
query, and the full-size T40.13x confirmation did not run. Execution preflight
had only an at-most 230,893,179-byte nominal zero-workspace margin below the
host-pressure upper boundary. The package omits the prepared allocation,
Phase-8 capacity, and selected inner refusal arm, so later external freeing is
an operational diagnosis rather than a sealed cause or product-capacity
result. T40.13z retires identifier 44, preserves V32 and every pressure/custody
bound, and returns host preparation to the existing stable same-volume
reservation protocol before any separately authorized neutral-45 freeze.

closes low-risk cost-first refusal ordering.
The original T40.13 neutral convergence gate follows only after the complete
stack, bounded regressions, independent review, and separate authorization. No
ticket may use a freeze, rehearsal, or giant authoring run as its regression
test.

Executed-tool binding is ceremony-only. Path commitments add sixteen fixed
SHA-256 strings to fresh V25 plan bytes without exposing paths. Each full host
snapshot remains bounded by 100,000 entries and 2 GiB; each direct executable
or private-tool check is bounded by 256 MiB or 2 GiB respectively. The shell
hashes only a command it is about to launch. Empty expected-digest settings
perform no production file hashing, child launch, corpus/shard read, or
retained allocation; ordinary query, worker, sync, and publication paths are
unchanged. No freeze, ceremony, release, topology/bound change, or scale/SLO
claim is authorized.

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
