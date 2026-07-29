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
now ships the focused child, exact shard-set publication, fail-closed search
admission, trusted-reader counters, byte-exact focused backup/restore, and
packaged-binary parity. Configured status says `focused` and
`repository-root-unbound`: source search is physically scoped, while root
SCIP is still not treated as unit-bound. T30.4 is next: produce the reusable
streamed candidate census and bounded partition manifest.

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
not path assignment. A bounded resolver catalog publishes before those
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
