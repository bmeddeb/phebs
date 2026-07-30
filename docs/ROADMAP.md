# phebs roadmap

This is the current sequencing view. Exact acceptance criteria live in the
[active backlog](./BACKLOG.md), completed implementation history lives in the
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
evidence for development and pilot evaluation, while the synthetic demo cohort
remains fixture-backed. Their retained external validation result is
`NOT_ESTABLISHED`; they do not establish runtime use, completeness,
compatibility, migration completion, decommission safety, or extraction
accuracy.

## Now

Epic 30 is in progress to add service-scoped analysis for very large
monorepositories. T30.1 froze the commit-bound analysis-unit contract and
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
scope, and same-HEAD unit changes cannot reuse it. Exact historical
commit/unit/domain publications are intentionally retained outside the current
sweep, so Epic 30 still needs a reviewed bounded-unpinned retention decision
(or an explicit decision to keep that unbounded posture); T30.6m selects it and
T30.6n implements only the selected posture. T30.6a now emits one
non-authoritative, source-free, 64 KiB-capped extraction operation report per
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
v3 commits exact in-unit domain projections: strict open costs
`B_repository + C_caller + ΣP`, each local replay costs `P_d`, and
repository/caller planes remain unchanged. Both issues retain adversarial
tests, full and race gates, detailed pushed fixes, and evidence-backed closure.

The separate large-monorepo review is complete. It did not overturn the
focused-unit architecture and identified the temporary flattened caller
inventory as the next integration boundary; raising global extraction limits
is not the repair. T30.6 remains that caller-overlay umbrella and is split at
each operational-state, scheduler, candidate-lane, catalog, leaf-artifact,
complete-publication, consumer, and retention seam. T30.6a bounded job
receipts, T30.6b durable exact-generation domain outcomes, and T30.6c
aggregate-bounded domain scheduling are shipped; T30.6d candidate-v4
source-lane classification is next. Exact `_test.go` suffix wins when candidate
v4 ships; focused local evidence for a committed non-empty unit consumes the
resulting `base` lane in the following ticket, while empty-unit
whole-repository extraction keeps shipped behavior and current search
continues indexing every admitted test file. A physical test-search overlay,
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

Production evidence/pilot gating remains unchanged. Epics 25–28 are still
unscheduled drafts in the [backlog](./BACKLOG.md). Epic 25 is an embedded
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
