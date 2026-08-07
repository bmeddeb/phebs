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
search authority. T40.3 is next. Epic 41 separately targets 10,000
accepted services with an 8,000 accepted-service floor; Epic 42 composes both
dimensions. No private rerun is authorized.

On the same day the [design charter](./DESIGN_CHARTER.md) became the
presentation authority, and Epic 43 was scheduled as its parallel
presentation-only track: twelve charter-gated tickets from audit ledger and
semantic tokens through authority drawers, contract-exact caveats, citation
objects, scope continuity, keyboard navigation, operator cards, and
ten-thousand-row density, closing with a motion pass. It touches no scale
plane, authority, or claim; T40.3 remains the next scale ticket.

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
  T40.1–T40.2 complete, T40.3 next):** exact source-free refusal attribution,
  the frozen neutral envelope, and independent generation-scoped planning
  ownership are complete. Next evolve existing partition/
  observation members behind measured aggregate roots, replace the static
  search lifecycle owner, make evidence append cost proportional to one chunk,
  partition extraction behind atomic domain authority, preserve recovery/
  lifecycle/archive behavior, replay downstream consumers, and close on a
  neutral two-million-owner gate.
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

- T40.3 is the only next scale ticket. Later tickets stay dependency-ordered
  and may be refined by preceding retained measurements, but may not broaden
  identity, authority, authorization, or release semantics.
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
