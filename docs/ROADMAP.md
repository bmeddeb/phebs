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

## Now

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

Production evidence/pilot gating remains unchanged. No product ticket is
currently scheduled. Epics 25–28 remain unscheduled drafts in the
[backlog](./BACKLOG.md); none is an implicit next ticket. Epic 25 is an embedded
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
behind the gates below. None of the remaining drafts is an implicit next
ticket.

## Gated product work

Production registration of the evidence and Workbench surfaces still requires:

1. retained validation that satisfies the documented gate rather than an
   operator bypass; and
2. a separate explicit pilot-continuation decision.

Until both exist, the implementation remains dark regardless of feature
completeness or demo quality.

## On demand

After Epic 30's single-node service-scope boundary, the next scale boundary is
the P6 fleet profile. It is intentionally not scheduled until a real
deployment requires it:

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
