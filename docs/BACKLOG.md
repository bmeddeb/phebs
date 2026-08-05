# phebs · active backlog

T31.1 bounded pipeline diagnostics and T32.1's microservice-first program
contract completed on 2026-08-04 and are retained in the completed backlog.
T32.2's authorized whole-monorepo baseline, T32.3's neutral
service-authority/correctness corpus, and T32.4's direct search-topology
decision completed on 2026-08-04 with source-free retained receipts. T32.5's
multi-service v2 gate is scheduled. Epics 33–39 are dependency-ordered program
drafts, not implementation authorization.
Epics 25–28 remain drafted and unscheduled; none is an implicit next ticket.
Epic 30's service-scoped
monorepo program completed on 2026-08-02, including the scope-aware UI,
operations guidance, and neutral ordinary-worker demo in T30.7. Its retained
completion receipt also records the post-review compatibility closure:
immutable v1/v2 proof bytes remain readable and the production exact Caller
Map envelope is validated through the strict MCP boundary. Completed
Epics 0–24, Epic 29, Epic 30, Epic 31, and P5 hardening are retained in the
[completed backlog](./BACKLOG_COMPLETED.md). Current posture and decision
points are summarized in [ROADMAP.md](./ROADMAP.md).

New work starts here only after its product boundary, dependencies, acceptance
criteria, and dated [PLAN.md](../PLAN.md) decision are reviewed. Tickets remain
PR-sized and dependency-ordered for a stacked workflow.

## Epic 32 · Microservice program contract and validation *(drafted 2026-08-03 · T32.1–T32.4 complete 2026-08-04 · T32.5 scheduled)*

Freeze the product and system contract before converting the current
single-unit path into a multi-service implementation. The invariant is one
shared repository source/search generation with many independently current
logical services; no ticket may loop repository-sized work over the service
count.

### Boundary

- Repositories remain physical Git, authorization, index, and rebuild owners.
  Services become first-class catalog, query, evidence, comparison, and
  workflow scopes.
- The private target-monorepo run may retain only source-free measurements in
  this repository. No employer names, paths, code, sizes tied to a named host,
  credentials, or infrastructure enter fixtures or reports.
- Synthetic 1,000/5,000-service profiles are load evidence, not a target SLO or
  accuracy result. Pack validation and pilot continuation remain separate.
- T32.4 selected direct whole-repository shards for the initial v2 path.
  Bounded cohorts and P6 remain evidence-triggered escape hatches.

**T32.5 · Multi-service v2 gate** *(needs T32.2–T32.4)* — freeze the
implementation input: service/catalog identity, authority precedence,
membership roles, current/stale behavior, selected physical topology,
repository/source/catalog generation identities, migration rule, initial
limits, and explicit deferrals. AC: every open question from the retained
multi-unit exploration is answered or assigned to a later ticket; measured
limits name their population/environment and safety margin; a dated PLAN ADR
records GO, conditional GO, or STOP; no unmeasured scale or accuracy claim.

## Epic 33 · Versioned service catalog and lifecycle *(draft · needs T32.5)*

Make many services per repository durable, inspectable, and independently
current before any high-cardinality relationship plane depends on them.

**T33.1 · Canonical service-catalog contract** — implement the selected
strict, versioned catalog input and pure validator. AC: stable logical service
keys distinct from repository placement identities;
canonical primary/supporting/shared/generated/typed/unowned roles;
many-to-many normalized path membership; authority/provenance and conflict
states; duplicate/unknown fields, unsafe paths, cycles, overlaps forbidden by
the selected policy, and all frozen count/byte/depth limits fail without a
partial catalog; canonical bytes/digest and table-driven neutral fixtures.

**T33.2 · Catalog ingestion and v1 migration** *(needs T33.1)* — bind each
repository to the selected operator/committed authority, publish one immutable
commit-bound catalog, and import today's configured analysis unit as one
semantically identical entry. AC: `analysis-unit-v1` digest preservation,
side-by-side v2 namespaces, exact HEAD/catalog fencing, no generic directory
guessing, proposal versus accepted authority separation, restart/reconcile,
failed replacement preserves prior authority, backup/restore parity.

**T33.3 · Independent service desired/active state** *(needs T33.2)* — add
per-service desired catalog identity, active source/catalog generation,
current/stale/unavailable/conflict status, monotonic incarnation, and removed
tombstone. AC: one service transition cannot relabel siblings; rename,
split/merge, removal/re-add, `A → B → A`, concurrent writers, rollback,
and restore fail closed; repository summary work is bounded and does not scan
all historical service generations.

**T33.4 · Authorized catalog API and MCP** *(needs T33.3)* — add paged
repository/service inventory and exact detail over the same shared service
type. AC: permission checks precede repository, service, count, membership,
and cursor work; hidden and absent are indistinguishable; cursors bind catalog
generation, authorization projection, ordering, filters, and incarnation;
page/response/path bounds and OpenAPI/MCP schema parity pass.

**T33.5 · Service directory and epic demo** *(needs T33.4)* — add an
accessible repository → service directory with status, authority, conflicts,
roles, and unowned/shared summaries. AC: exact deep links, reload/back/forward,
loading/empty/error/stale/conflict/removed states, desktop/mobile/keyboard,
source-free operations guidance, and a neutral multi-service `make dev` demo;
no relationship or accuracy claim.

## Epic 34 · Shared repository search with exact service scope *(draft · needs Epic 33)*

Index repository content once under the topology selected by T32.5, while
making All code and service-scoped search equally exact.

**T34.1 · Repository source/search generation** — publish the selected
single-node physical topology from one immutable revision set and one streamed
census. AC: every path has one physical owner; shared logical membership does
not duplicate indexed bytes; exact root/member manifests, metadata, size
splitting, limits, atomic visibility, mmap/descriptor retirement, and
same-module zoekt pin pass the neutral and target-derived gates.

**T34.2 · Service query compiler** *(needs T34.1)* — compile one service's
complete authorized placement membership into repository/cohort selection plus
exact zoekt path predicates before ranking. AC: no result-time filtering;
single- and multi-repository placement behavior remains explicit; equality to
T32.3's oracle under broad queries, ties, truncation, branches/tags, shared and
unowned paths, and catalog transitions; query/cursor authority binds exact
source and catalog generations; bounded predicate bytes and complexity.

**T34.3 · Search publication migration and recovery** *(needs T34.2)* —
dual-read/single-write through v1/v2 transition, cut over only with an exact
replacement, and retain fail-closed repair, backup/restore, repository removal,
and active-reader behavior. AC: old whole/focused results are never relabeled
as v2 service results; interrupted build/publication, stale shards, invalid
catalog, restore, and rollback keep the prior exact state or become visibly
unavailable; derived bytes remain rebuildable.

**T34.4 · All code/service search product and demo** *(needs T34.3)* — add
one shared scope selector and scope receipt across UI, HTTP, and MCP. AC:
service deep links, query preservation, exact result/citation scope, explicit
shared/unowned behavior, stale/current transitions, mobile/accessibility, and
an end-to-end neutral multi-service search demo; no evidence-pack promotion.

## Epic 35 · Bounded scheduling, retention, and recovery *(draft · needs T32.5, Epic 33; blocks Epics 36–37)*

Prevent thousands of services and generations from multiplying unbounded job
history, partial artifacts, or retained publications.

**T35.1 · Generation-scoped chunk scheduler** — add bounded chunk identity,
fan-out pages, repository fairness, resource classes, per-repository tokens,
commit coalescing, stale-worker fencing, and successor semantics. AC: one turn
does bounded work; no queue transaction enumerates all services; never-run,
retryable, and stale priorities are deterministic; cancellation/restart and
attempt exhaustion preserve settled siblings; process-wide concurrency and
memory/descriptor bounds pass.

**T35.2 · Pin-aware lifecycle and retention decision** *(needs T35.1)* —
freeze roots and owner lifecycles for catalogs, source/search generations,
observations, resolver namespaces, relationships, proofs, Investigations,
readers, jobs, tombstones, and partial stages. AC: explicit age/count/byte and
disk-watermark behavior; proof/Investigation pins and active leases win;
removed services tombstone before collection; defaults, disable behavior, and
non-combinable byte metrics are documented by a dated ADR.

**T35.3 · Bounded sweep and capacity control** *(needs T35.2)* — implement
restart-resumable bounded collectors/sweepers without startup full-history
work. AC: fair progress, exact/lower-bound/unavailable status, disk-pressure
admission before unsafe work, no live-root/pin/lease deletion, symlink/path
hardening, weak-consistency disclosure, per-tick and aggregate I/O/query/stat/
descriptor/memory ceilings, backup interaction, and failure localization.

**T35.4 · Lifecycle recovery demo** *(needs T35.3)* — exercise catalog churn,
commit coalescing, interrupted stages, reader leases, pin/release, disk
pressure, sweep, restart, backup, and restore across the neutral scale profile.
AC: deterministic source-free receipts, no false-current state, retained proof
readability, operations/UI status, and a bounded `make dev` scenario.

## Epic 36 · Shared source-observation plane *(draft · needs Epics 34–35)*

Read and parse supported source once per exact repository generation and pack
policy, then reuse bounded target-independent observations across services and
relationship families.

**T36.1 · Batch immutable Git reader and partitions** — add bounded
`git cat-file --batch` sessions over manifest-selected immutable blobs and a
versioned source-partition contract. AC: pure-reader rules, no lazy fetch or
replacement refs, output/time/blob/aggregate/descriptor limits, cancellation,
missing-object classification, path-free diagnostics, deterministic
partitioning, and one admitted blob read per generation/policy.

**T36.2 · Go source-observation contract** *(needs T36.1)* — parse imports,
aliases, receivers/generated-client clues, constructor origin, selector spans,
bounded local propagation, Kafka call/topic shapes, and explicit dynamic or
unsupported states once. AC: the representation is not a generic serialized
AST; gRPC, Thrift, Kafka producer, and Kafka consumer adapters consume the same
source observation without merging their semantic failures; exact citations,
limits, double-run determinism, and existing supported-case parity pass.

**T36.3 · Content-addressed observation publication** *(needs T36.2)* —
publish immutable partition members and one atomic root keyed by source
generation, language/policy, and content identity. AC: unchanged blobs reuse
only after exact semantic compatibility; changed blobs invalidate affected
partitions; current pointer, marker recovery, active leases, warm/cold cache,
backup/restore, and T35 collection are fenced; no SurrealDB row-per-observation
hot path.

**T36.4 · Observation progress and epic demo** *(needs T36.3)* — expose
authorized bounded progress, failure/unsupported census, input/read/reuse
counts, and source-free operations receipts. AC: no raw paths or samples in
logs, permission-before-counts, exact current/stale state, HTTP/MCP parity,
neutral multi-pack demo, and steady-state cost proof.

## Epic 37 · Cross-service relationship index *(draft · needs Epics 35–36)*

Join repository-shared observations to declarations once, publish keyed
relationship postings, and project them onto services without claiming runtime
or universal completeness.

**T37.1 · Namespace-sharded declaration/resolver catalog** — partition exact
contract and generated-symbol identity by language/protocol/namespace under
one complete root. AC: declaration identity stays independent of unit
ownership; conflict/ambiguity/unsupported inputs remain records; only affected
namespaces rebuild; atomic root, bounds, recovery, and keyed lookup pass.

**T37.2 · RPC caller postings** *(needs T37.1)* — join shared Go observations
to gRPC/Thrift resolver identities once and publish sorted postings by exact
operation. AC: resolved/name-match/unresolved classes and source roles remain
separate; dynamic/ambiguous sites never guess; sparse keyed reads, partition
limits, duplicate-content occurrence identity, deterministic output, and
current direct-caller behavior parity pass.

**T37.3 · Kafka producer/consumer postings** *(needs T37.1)* — publish
separate literal-topic producer and consumer postings from the shared
observations. AC: topic remains a source spelling, not cluster/runtime
identity; dynamic/cross-file unsupported topics remain unresolved; pack
limits, keyed reads, exact citations, and current supported-case parity pass.

**T37.4 · Service projections and atomic relationship roots** *(needs
T37.2–T37.3)* — project source and target membership onto zero, one, or many
services and publish exact per-service plus repository-complete roots. AC:
shared/unowned/conflicting memberships stay explicit; a broken service does
not block unrelated service roots; an all-services claim requires every named
partition against identical source/catalog/resolver roots; progress cannot
masquerade as complete; authorization precedes indexes, counts, and files.

**T37.5 · Exact readers, comparison, proof, and demo** *(needs T37.4)* —
serve paged service dependency/caller/topic reads and two-generation comparison
through shared HTTP/MCP types; compose only authorized repository-placement
roots and integrate exact roots into coverage, proof, and Workbench without
rewriting retained v1–v3 bytes. AC: permission resolution precedes cross-repo
aggregation; cursor/incarnation fences, citation leases, truncation, zero/gap
states, migration parity, bounded caches/concurrency, and a cross-service
neutral demo.

## Epic 38 · Microservice product workflows *(draft · needs Epic 37)*

Turn the catalog and relationship engine into the service-centered experience
that differentiates phebs from repository/file search.

**T38.1 · Service overview** — build one service page showing identity and
authority, source roles, contracts provided/used, callers, topics, dependent
and dependency services, owner/deployable attribution, currentness, and gaps.
AC: every count links to exact rows/citations; ambiguity, unowned/shared,
partial, failed, stale, unsupported, and empty states remain distinct;
authorization, paging, mobile, keyboard, and screen-reader gates pass.

**T38.2 · Cross-service relationship explorer** *(needs T38.1)* — add
source-first filtered tables and an optional deterministic visualization over
the same authoritative rows. AC: service/contract/topic/direction/evidence
filters; no graph-invented edges or transitivity; shared table/diagram ids,
bounded layout, truncation and coverage, exact source navigation, and broad
repository fallback.

**T38.3 · Service-aware Impact and Workbench** *(needs T38.1)* — compose
exact contract changes, affected services, unresolved/unowned candidates,
comparison, and human dispositions into existing Investigation revisions. AC:
current authorization and root identity rechecked at every seam; no implicit
write, task completion, migration-complete, or decommission-safe conclusion;
source/target service changes invalidate stale previews.

**T38.4 · MCP microservice parity** *(needs T38.1–T38.3)* — expose service
inventory/detail, dependencies, change impact, gaps, and citations through
strict bounded tools or extensions to existing tools. AC: same service types,
authority, pagination, errors, authorization, and capability gating as HTTP;
tool-count/schema digests, hostile clients, cancellation, and output ceilings
pass; agents receive evidence, not decision authority.

**T38.5 · Product closure and neutral demo** *(needs T38.1–T38.4)* — run an
add/modify/migrate/retire scenario across multiple neutral services from All
code discovery through service overview, dependency evidence, comparison,
Workbench, proof, and MCP. AC: desktop/390px/accessibility/browser, failure and
restart states, operations guidance, full merge bar, and a demo understandable
without source-of-truth documents; surfaces remain experimental until Epic 39.

## Epic 39 · Multi-service validation and release decision *(draft · needs Epic 38)*

Validate the implemented system and decide a narrow shadow/advisory release;
feature completeness alone cannot promote it.

**T39.1 · Neutral correctness, scale, and recovery gate** — execute the
T32.3 oracles and frozen 1,000/5,000 profiles against the final writers/readers.
AC: membership/search/relationship equality, deterministic rebuild, bounded
cold/warm/no-op/update/GC costs, partial publication, auth, recovery, restore,
and retained-proof gates pass or record STOP; no target-corpus SLO claim.

**T39.2 · Authorized target operating-envelope gate** *(needs T39.1)* — run
the approved target monorepo under frozen resource, freshness, query,
availability, and retention thresholds. AC: exact artifact/tool/config/source
identities, source-free retained report, cold/incremental/no-op/query/recovery
measurements, failure census, teardown/custody, and direct/cohort/P6 decision;
no result generalizes beyond the named environment.

**T39.3 · Security and lifecycle gate** *(needs T39.1)* — independently
exercise hidden service names/counts, shared paths, cross-service edges,
revocation, cursor/proof reuse, partial/stale roots, malicious catalog/source
inputs, disk pressure, pin/lease retention, sweep, backup/restore, and teardown.
AC: every negative case passes or stops release; review is independent of the
implementer; no bypass or warning becomes approval.

**T39.4 · Evidence-quality and workflow gate** *(needs T39.2)* — under the
existing preregistered pilot authority, measure pack-specific call-site
quality, service-attribution hops, end-to-end service relationships,
processing coverage, unresolved states, and migration-inventory workflow cost
against the independent baseline. AC: thresholds were frozen before unsealing;
underpowered/inconclusive is not a pass; every correction and owner-routing
cost remains counted; existing pilot scope is not broadened.

**T39.5 · Release, suspension, and continuation decision** *(needs
T39.1–T39.4)* — complete the relevant evidence-pack cards and record separate
validation and human continuation decisions. AC: release is limited to the
measured service/language/framework/workflow/envelope; default-dark, shadow,
advisory, suspension, expiry, rollback, and revalidation semantics are exact;
no "all runtime callers," migration-complete, or decommission-safe claim; STOP
and teardown are first-class valid outcomes.

## Epic 25 · Embedded documentation browser *(drafted 2026-07-27 · unscheduled nice-to-have)*

Serve the repository's markdown documentation, rendered, from the phebs binary
itself. The tracked `docs/` tree stays the single source of truth: plain
markdown, still rendered identically by GitHub, with no external docs
toolchain, static-site generator, or content fork.

### Boundary

- One new pure-Go dependency (`goldmark` plus its GFM extension); no new
  runtime children and no build-pipeline stage.
- Served behind the existing session/API-key authentication like the UI; no
  anonymous surface and no new capability.
- `docs/fixtures/` and `docs/design_handoff_phebs_brand_and_ui/` are excluded
  from the embedded set; retained records remain repository-only.
- No docs versioning, search, or navigation chrome: `docs/README.md` and
  `docs/MANUAL.md` are the navigation, exactly as on GitHub.

**T25.1 · Rendered docs at their markdown URLs** — embed the tracked docs
markdown, docs SVGs, and `config.example.yaml`, plus root `README.md` and
`PLAN.md`, via `go:embed` (same build-tag pattern as `ui/`); render GFM to
HTML with goldmark once at startup; serve each page inside one branded HTML
shell at its repo-relative path under an authenticated docs route, so every
tracked relative link works unrewritten (`.md` URLs return HTML, SVG and YAML
pass through). AC: every local link the T24.1 contract test validates also
resolves in the served site; excluded fixture and design-handoff bytes are
absent from the binary; the route requires authentication; `make dev` demo —
open the served `docs/README.md`, follow a link into a task guide and the
architecture SVG; dated PLAN ADR bullet in the same PR; full merge bar.

## Epic 26 · SQL schema-set evidence *(drafted 2026-07-27 · unscheduled — spike first)*

Outline relational models from committed repository bytes: declaration-first,
usage-second, joined only through committed binding artifacts. Round one
scopes PostgreSQL and MySQL current-schema snapshots, including a requested
schema-only dump, plus schemas and authored queries bound by a committed sqlc
manifest. A dump alone is sufficient for a citable catalog when its committed
header establishes the dialect; the manifest is the stronger artifact that
deterministically ties an engine, schema files, and authored query files
together without inference.

### Boundary

- Pure reader over committed blobs at the indexed commit. No database
  connection, no `information_schema` introspection, no migration execution,
  no runtime naming resolution.
- PostgreSQL and MySQL are separate dialect lanes. A sqlc `engine` value or a
  recognized dump-generator header establishes the dialect; grammar sniffing
  never does. Headerless standalone schema files require a committed binding
  manifest. No assertion or measurement merges identities across dialects.
- Three evidence planes, no more:
  - `sql-schema` — current-schema declarations from a committed schema-only
    dump or from inputs explicitly listed by a committed sqlc manifest; the
    only source of `DECLARES_TABLE` / `DECLARES_COLUMN` / key, index, and
    FK-edge facts. Evidence records whether the source is an authored schema
    input or a captured dump; either proves only what the committed bytes say,
    never the live database. A dump without a query-binding manifest produces
    the catalog but no usage join.
  - `sql-query` — authored `.sql` query definitions producing read/write
    relation references. Generated `.sql.go` is never a primary usage source;
    it duplicates the authored query and joins later via generated-from
    evidence.
  - `sql-migration-event` — per-statement migration history
    (`MIGRATION_CREATES_TABLE`, `MIGRATION_ADDS_COLUMN`,
    `MIGRATION_DROPS_TABLE`, …) with **no current-shape claim**. No fold in
    round one: a folded "current schema" has no single citable blob under the
    extractor's streaming contract, and repeated alterations of one column
    need occurrence-distinct identities because assertion identity excludes
    detail.
- Shared join lineage `sql_schema_set_v1(repo, manifest-or-migration-root,
  dialect)`. An unqualified table literal stays a named-table reference until
  exactly one declaration set selects it; multiple candidate roots refuse as
  `ambiguous-schema-set`, never a repo-wide guess.
- Identifier canonicalization is dialect-specific. PostgreSQL quoted and
  unquoted identifiers retain their distinct rules. MySQL source spelling and
  quoting are preserved; absent committed server configuration, phebs never
  assumes a `lower_case_table_names` value or joins case variants.
- A repository with no admitted schema artifact reports
  `schema-artifact-missing` and may produce a checklist action to request a
  schema-only dump. The request and any human-supplied capture metadata are
  workflow state, not schema evidence; facts begin only when immutable dump
  bytes are available to the reader.
- Column references get their own eventual surface
  (`find_sql_column_references` over `(schema lineage, relation, column)`);
  the existing numeric wire-field surface stays byte-stable and untouched.
- Out of scope, stated as decisions: Redis (Epic 28 scopes deterministic
  declaration islands and provable key usage; a universal keyspace model
  stays out); raw document-store dialects (Epic 27
  instead proposes one employer-neutral normalized manifest); Cassandra/CQL
  and explicit-only ORM packs (separate later spikes with their own decision
  tables); any ER visualization (rendering is commodity once facts exist);
  the Workbench SQL resource plane (stays `unsupported` until a proven
  operation → query → table join exists).
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production registration
  sits behind the documented validation and pilot-continuation gate.

### Safety boundary

- Spike artifacts live under `spike/t261/` as retained records; production
  packages must not import spike packages.
- A requested dump enters the pure-reader boundary only after it is committed
  to an indexed repository, including a dedicated evidence repository if the
  application source repository cannot own it. Ad hoc uploads and mutable
  filesystem paths are outside round one.
- Dump admission is schema-only and fails closed on data-bearing `COPY`,
  `INSERT`, `REPLACE`, or `LOAD DATA` sections. PostgreSQL dump wrappers and
  MySQL versioned comments, delimiters, engine/charset/collation clauses,
  prefix indexes, and generated-column syntax are parsed or reported by
  frozen abstention classes, never silently discarded. Repository placement
  and an optional human-asserted capture time do not establish database,
  cluster, environment, or runtime identity.
- Public corpus only; no employer names, code, schemas, or infrastructure.

**T26.1 · Pinned-corpus SQL evidence spike** — pin the public Hatchet
repository at `559b5021e418f12ded175f950b709b7fa66be5a5` (214 SQL migrations,
5 sqlc schema inputs, 36 authored query files, matching generated Go; FKs,
composite keys, CTEs, PL/pgSQL, triggers, partitions, and dynamic `EXECUTE`
for credible positives *and* honest abstentions). Pin the public
`ntppool/monitor` repository at
`e03c40a06ae8f9bd4906001c2ede0c7296ec8e96` for the MySQL lane: its committed
MySQL-compatible dump, sqlc `engine: mysql` manifest, authored query file, and
generated Go exercise FKs, composite keys, views, backticks, versioned
comments, engine/charset/collation clauses, joins, index hints, and
`ON DUPLICATE KEY UPDATE`. Add one version-pinned PostgreSQL-generated
schema-only dump of a committed public fixture, retaining the exact generator
command, version, input digest, and output digest; together the real MySQL
dump and generated PostgreSQL dump validate the requested-dump path without
executing corpus SQL in production. Hand-label both dumps and all manifest
schema inputs completely, plus a preregistered stratified random sample of
migration and query files; freeze the sampling rule and all four measurement
denominators in the spike README **before** the first parser run. Evaluate at
least one imported parser candidate and one bounded subset grammar for each
dialect against identical gates (parser bounds, panic safety on the full
corpus, binary-size and build-time impact, byte-exact source positions,
deterministic double-run output). Freeze a decision table covering:
authored-schema versus captured-dump source classification; dialect admission
from committed metadata; schema-only admission and data-section refusal;
missing artifact/request-dump state; PostgreSQL and MySQL identifier
canonicalization (never generic lowercasing or an assumed
`lower_case_table_names` value); schema-set lineage and ambiguous-root
refusal; the exact `DECLARES_*` versus `MIGRATION_*` predicate split; FK
identity including composite-column order and multiple FKs between the same
table pair; common read/write semantics for `SELECT`, `INSERT … SELECT`,
`UPDATE`, and CTEs plus PostgreSQL `DELETE … USING` and MySQL multi-table
delete / `ON DUPLICATE KEY UPDATE`; generated-source deduplication;
occurrence-distinct migration-event identity; single-pass versus bounded
two-pass reads under the SDK's one-blob citation contract; and a frozen
per-dialect unresolved vocabulary (unsupported statement, dynamic identifier,
ambiguous schema set, unknown declaration, procedural SQL, generated
duplicate, parser gap). AC: locked corpus pins, generated-dump receipt, labels,
and measurements committed under `spike/t261/`; the decision table answers
every frozen question or records an explicit deferral; the measurement report
states four separate denominators for each dialect — declaration objects
parsed vs. hand labels, migration statements recognized vs. gaps by syntax
class, query table sites recognized vs. unresolved, and uniquely bound joins
vs. name-only/missing/ambiguous — with no cross-dialect blended percentage;
double-run bytes identical in both lanes; no production code paths changed
and no pack registered.

## Epic 27 · Document schema-manifest evidence *(drafted 2026-07-27 · unscheduled — spike first)*

Make schema-on-write document-store structure useful during everyday ticket
work without teaching phebs a private vendor or employer dialect. Round one
admits one project-owned, strict JSON interchange artifact,
`phebs-document-schema-v1`, produced by a repository author or an out-of-tree
adapter and committed at the indexed revision. It supplies an Atlas-shaped
catalog — schema set → table → nested field — plus ordered key, index,
association, and materialized-view declarations with exact citations.

The first workflow is deliberately declaration-only: locate the table a ticket
mentions, inspect its nested fields and exact type spellings, verify primary
and partition-key membership and order, follow explicitly declared
associations/views, and cite the committed snapshot. If the artifact is absent,
phebs reports a request-schema-export checklist state. It does not infer a
model from client code.

### Boundary

- Pure reader over one committed manifest blob at the indexed commit. No
  service connection, administrative API, runtime introspection, schema
  registry call, generator execution, or mutable upload.
- The manifest has a closed, versioned vocabulary for tables, flattened nested
  field paths, exact source type spellings, ordered primary and partition
  keys, ordered indexes, associations, and materialized views. A field path
  is a JSON array of typed segment objects — a field segment carrying one
  exact name, or a traversal segment from the closed set {array element, map
  value} — never a delimiter-split or escaped string, so every exact field
  name, including `[]`, remains representable. Primary-key, view-key, and
  index entries may carry an explicit `asc`/`desc` direction (default
  ascending); partition keys are path-only and carry no direction. An index
  column may carry an optional positive prefix length whose unit is defined
  by the declaring store — phebs cites it and never interprets or converts
  it. An index names an opaque, citable `kind` spelling and is owned by
  either a table or a view. A view declares its own output namespace:
  ordered output fields with exact names, each carrying zero or more source
  `(table, field-path)` references; view keys and view-owned indexes
  reference output paths only, and duplicate output names fail closed. Type
  and kind spellings are citable opaque declarations in round one; phebs
  does not invent cross-store equivalence.
- Lineage is
  `document_schema_set_v1(repo, manifest_path, format_version)`. Exact table
  spelling and exact field-path segments remain part of identity. Display
  labels, capture timestamps, source revision strings, repository placement,
  and adapter names never establish service, cluster, database, environment,
  ownership, deployment, or runtime identity.
- Every table declares a non-empty ordered primary key. An optional partition
  key must be a non-empty ordered path-prefix of that primary key, with
  direction excluded from the prefix comparison; an absent
  partition key means undeclared, never an assumed default. Associations are
  optional — some stores declare none — and the association graph may
  contain cycles, including self-associations: associations are declared
  edges phebs never traverses. View source references must form an acyclic
  dependency graph; a view-dependency cycle fails closed. Every referenced
  field path must resolve exactly within the same manifest; duplicate tables,
  fields, key entries, indexes, associations, or view identities fail closed.
- Associations are explicit logical edges, not foreign keys and not proof of
  referential integrity. A materialized view is a declared derived resource,
  not proof that it is deployed, fresh, populated, or read by code.
- An `authored` manifest records repository intent. A `captured` manifest
  records only the structure present in the supplied export. Both are
  committed declarations; neither is evidence of the live store. The source
  class is mandatory and the two classes are never silently merged.
- A repository with no admitted manifest reports
  `document-schema-artifact-missing` and may produce a checklist action to
  request a schema-only export in this neutral format. The request, adapter
  instructions, and human capture metadata are workflow state rather than
  schema evidence.
- The eventual catalog gets its own table and field identity surfaces; the
  numeric protocol field reader and Contract Atlas service identities remain
  unchanged. The Workbench document-store resource plane stays `unsupported`
  until a separate public contract can prove operation → usage → table
  relationships. Declaration-only catalog rows cannot populate that
  relationship registry.
- Raw proprietary schema files, private client APIs, private adapters,
  inferred client-code usage, query semantics, live-schema comparison, and
  cross-manifest current-shape folding are outside round one. An organization
  may maintain an adapter elsewhere, but only its normalized committed output
  crosses the phebs evidence boundary.
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production registration
  sits behind the documented validation and pilot-continuation gate.

### Safety boundary

- Spike artifacts live under `spike/t271/` as retained records; production
  packages must not import spike packages.
- Strict decoding rejects unknown and duplicate object keys, invalid UTF-8,
  control text, invalid or unnormalized paths, unresolved references, cycles
  where the contract forbids them, unsupported format versions, trailing
  bytes, and any record/sample/default/example/value payload. The manifest is
  structural metadata only.
- Freeze limits before implementation for manifest bytes, tables, fields per
  table, path depth and segment bytes, type bytes, key width, indexes,
  associations, views, aggregate declarations, and emitted citations.
  Limit failures are typed abstentions; no partial catalog is emitted. phebs
  validates only these frozen bounds — store-vendor and deployment limits are
  never encoded or enforced.
- A captured export enters the reader only after immutable placement in an
  indexed repository, including a dedicated evidence repository if the
  application source repository cannot own it. No employer artifacts, names,
  code, credentials, hosts, or infrastructure enter phebs fixtures or retained
  records.

**T27.1 · Neutral document-schema contract spike** — freeze the
`phebs-document-schema-v1` JSON Schema, a neutral synthetic fixture family,
and one independently authored public-reference derivation before
implementing the reader. The positive synthetic fixtures cover nested
objects/lists/maps traversed through typed array-element and map-value
segments, a literal field exactly named `[]`, opaque scalar type spellings,
composite primary keys including a descending key column, path-only
partition-key prefixes, an undeclared partition key, multiple ordered
indexes including a store-defined prefix-length column, distinct opaque
index kinds, and a view-owned index over output paths, two associations
between the same table pair, a self-association, a table with no
associations, a materialized view with multi-source output fields, a
zero-source declared output field, and its own ordered keys over output
names, authored and captured source classes, and exact non-ASCII names. The
negative synthetic fixtures cover every strict-decoder, reference-integrity,
view-dependency-cycle, key-order, malformed-segment-object, duplicate
view-output-name, direction-on-partition-key, invalid direction or
prefix-length, data-payload, and size/depth refusal.

Pin the Apache-2.0 public `jeffreyscarpenter/cassandra-guide` repository at
commit `bed58e7e51768f9f30dd435e74f31e5b3b2649a4` and hand-derive one captured
neutral manifest from `resources/reservation.cql` (Git blob
`2c1c098ff706f63f28be772514e7b7a664878774`, 2,218 bytes, SHA-256
`38992bd1f0882ebefe7e16570d05886ade150bf2036ea6119ab08fc11b3bb39e`).
The source independently exercises a nested user-defined type, set/list/map
collections, a map value containing the nested type, scalar and composite
partition/clustering keys, and a materialized view. Retain the source pin,
license and digest receipt, plus a complete declaration-by-declaration
CQL-source → neutral-manifest derivation table with exact source spans and
explicit decisions for collection traversal, path-only partition prefixes,
default key direction, and view output fields. Freeze that derivation before
the reader's first run. The CQL file is an external oracle only: phebs neither
parses nor admits CQL in Epic 27, and this fixture makes no Cassandra-pack
claim. Synthetic fixtures remain authoritative for neutral-contract features
the public source does not contain.

Hand-label every synthetic declaration and refusal and every declaration in
the derived public manifest. Freeze a decision table covering
schema-set/table/field identity; source-class semantics; exact-name handling;
typed field-path segment representation; type, index-kind, and
prefix-length-unit opacity; key, index, association, and view semantics —
including per-column direction, path-only partition prefixes, view-owned
indexes, view output namespaces and their source references,
association-cycle legality versus view-dependency acyclicity, and
undeclared-partition-key meaning; source positions for every emitted object
and edge; missing-artifact/request-export state; single-snapshot versus
explicit two-snapshot comparison; canonical ordering; typed unresolved
vocabulary; and all parser/output bounds — phebs's own, with no vendor-limit
validation. Run a bounded reader twice over every fixture and compare
byte-identical facts, citations, catalog projection, refusal census, and
snapshot comparison. AC: schema, synthetic and public-derived fixtures,
source/digest/license receipt, complete derivation table, labels, decision
table, bounds, and measurements committed under `spike/t271/`; byte-exact
UTF-8 source spans round-trip to the cited JSON; the derived manifest is fully
accounted for against the pinned CQL without a parser; malformed, cyclic,
data-bearing, and oversized inputs fail without partial facts or panics; the
comparison distinguishes added/removed/changed declarations without calling
either snapshot live or current; the retained README demonstrates the
locate-table → inspect-key/field → copy-citation ticket workflow and the
missing-artifact checklist; no private dialect, adapter, CQL parser,
production code path, pack registration, or runtime claim is added.

## Epic 28 · Redis keyspace evidence *(drafted 2026-07-27 · unscheduled — spike first)*

A general Redis keyspace model is out of scope; deterministic Redis
declaration islands and provable key usage are in scope. Redis repositories
divide by the artifacts they commit, so round one is two spike lanes —
native declaration islands and key-specification-driven usage binding — and
the neutral keyspace manifest is explicitly deferred behind the spike's gap
measurement: it is drafted only if native declarations leave the expected
gap, entering as the same request-artifact workflow as the SQL dump and the
document-schema manifest.

### Boundary

- Pure reader over committed blobs at the indexed commit. No server
  connection, no `TYPE`/`SCAN` probing, no RDB/AOF inputs, no runtime state,
  and no claim that any cluster, database, or key exists.
- Lane one, native declaration islands: committed ACL key-pattern files and
  recognizable literal command vectors that declare indexes (`FT.CREATE`),
  time series (`TS.CREATE`, `TS.CREATERULE`), and stream groups
  (`XGROUP CREATE`). Core-command semantics come from the pinned command
  metadata; the named module subset uses bounded phebs-authored recognizers
  derived from public command documentation, not copied module source or
  metadata. An index schema is only the indexed projection over declared key
  prefixes, never a complete value model; a series or group declaration names
  a resource, not its entry fields. Most such declarations execute from
  application startup code, so this lane shares the command-vector machinery
  and its recognizer limits — the spike's headline number is what fraction of
  these declarations exist as recognizable committed literals at all.
- Redis object-mapping model classes are the strongest committed declaration
  artifact but sit across a language frontier; round one measures feasibility
  only and commits to no model-class reader.
- Lane two, usage binding over the Redis 7.2.15 command key specifications
  pinned below. Only provable command vectors bind: literal raw commands or
  arrays; one `github.com/redis/go-redis/v9` major-version adapter whose
  method-to-command table is validated against each pinned corpus's exact
  dependency version; literal or same-file-constant key arguments; and
  bounded format expressions whose segment structure is statically known.
  Other client majors and unproved wrapper mappings abstain.
- Explicit `FCALL`/`EVAL` key lists can emit
  `PASSES_REDIS_KEY_TO_SCRIPT`: they prove only which keys the caller supplies
  under the command contract. They never inherit the script's read, write, or
  expected-value-kind semantics, and round one does not analyze function or
  script bodies.
- Access modes adopt the key-specification vocabulary verbatim — `RO`, `RW`,
  `OW`, `RM` with access/update/insert/delete detail — never flattened to
  read/write. `variable_flags` are resolved only when the admitted argument
  vector proves the applicable branch; otherwise access mode abstains as
  `variable-access-mode`. An incomplete specification may support a fact only
  when the exact key position and applicable flags are fixed for that admitted
  vector; every omitted or unknown position remains an
  `incomplete-key-specification` gap.
- Expected value kind is not present in the key specifications. It comes from
  a separate, frozen command-family table hand-labeled from public command
  documentation. A table entry may assert only what the code expects — for
  example, a hash command expects a hash — never runtime existence or type.
  Missing or argument-dependent entries yield
  `unknown-expected-value-kind` without discarding separately provable
  key/access evidence.
- ACL key patterns are a separate authorization-intent evidence class; they
  never declare key existence or value structure and are never merged with
  declaration or usage rows. An ACL fact may identify the exact username token
  as its authorization principal, but it proves neither authentication nor
  deployment. Only username-token and `%R~`/`%W~`/`%RW~`/`~` pattern-token
  spans may be emitted or cited. Cleartext-password tokens, password-hash
  tokens, command grants, and line-context snippets are neither retained nor
  rendered; diagnostics name only structural refusal classes.
- Frozen unresolved vocabulary: `dynamic-concatenation`, `opaque-helper`,
  `runtime-namespace`, `unknown-client-adapter`,
  `unsupported-core-command-after-pin`, `unsupported-module-command`,
  `incomplete-key-specification`, `variable-access-mode`,
  `unknown-expected-value-kind`, and `ambiguous-key-family`.
- The deferred `phebs-redis-keyspace-v1` manifest, if the gap measurement
  justifies it, declares key families as ordered structured binary-safe
  segments (literal, parameter, hash-tag grouping — typed segments, never
  format strings), with intended value kind, optional field intent, expiry
  intent, associated index/group/series resources, and authored/captured
  source class. Hash-tag validation uses the exact documented byte semantics
  without claiming a cluster exists. A concrete key or statically known
  format binds only when exactly one declared family matches; overlaps
  refuse as `ambiguous-key-family`, never precedence guessing.
- Out of scope as decisions: universal keyspace modeling; runtime
  introspection; RDB/AOF or any data-bearing input; the Workbench Redis
  resource plane (stays `unsupported`).
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production
  registration sits behind the documented validation and pilot-continuation
  gate.

### Safety boundary

- Spike artifacts live under `spike/t281/` as retained records; production
  packages must not import spike packages.
- The only admitted upstream command-metadata bytes are Redis 7.2.15 commit
  `316753259b4db132cf494292a1b3a702d9e9ddb2`: BSD-3-Clause `COPYING` blob
  `a381681a1c2524ed586c6a87dfeb9ccdf1e86ded` and the 392 JSON files under
  `src/commands/` tree `59da020b9c7d8847fa0f7012b1fa2b3a09f47297`.
  T28.1 records per-file SHA-256 digests and a license receipt before use.
  Core commands added after that pin are unsupported.
- The named declaration subset (`FT.CREATE`, `TS.CREATE`,
  `TS.CREATERULE`) uses bounded phebs-authored recognizers derived from public
  command documentation. No Redis module implementation or metadata bytes
  enter the repository; every other module command is
  `unsupported-module-command`. Phebs parses committed source artifacts and
  neither links, embeds, starts, nor connects to Redis.
- ACL readers are token-streaming and output-safe by construction. Retained
  fixtures and test snapshots must prove that credentials adjacent to a
  pattern cannot enter facts, citations, diagnostics, logs, or rendered
  context.
- Public corpus only; no employer names, code, schemas, credentials, hosts, or
  infrastructure.

**T28.1 · Two-lane Redis evidence spike** — preregister these immutable public
inputs and their exact file scopes before the first recognizer run:
MIT-licensed `hibiken/asynq` commit
`d135f1439bee74e989b7f9b41ecd542cc87f024a` with
`github.com/redis/go-redis/v9` v9.14.1 for the broad usage census;
Apache-2.0 `cloudwego/eino-examples` commit
`6dc0d214c0eb392babf2d001e9be85f57ac10952` with go-redis v9.17.2 for
literal/constant raw-command and `FT.CREATE` declaration shapes; and the
Redis 7.2.15 pin above for command metadata plus the public ACL safety
fixtures. Record the hand-label sampling rule, source/license receipts, and
all denominators before execution. Measure separately, with no blended
percentage: declaration coverage (index/series/group sites with exactly
bindable identities versus dynamic construction — the manifest go/no-go
number); ACL username/pattern pairs parsed and credential-token non-retention;
recognized command sites versus unresolved by frozen shape class; resolved
key arguments; resolved access modes; expected-kind coverage; script-key
pass-through rows; and uniquely bound key spellings or families. Freeze a
decision table covering command-vector recognition and declaration
partial-fact atomicity; the go-redis method-to-command rule at both exact
versions with no cross-major claim; format-expression bounds;
incomplete-specification and `variable_flags` handling; exact access
vocabulary; the separately sourced expected-kind table;
`PASSES_REDIS_KEY_TO_SCRIPT`; ACL token-only citations; hash-tag byte
semantics; the clean-room named-module boundary; the object-mapping
feasibility verdict; and the explicit manifest go/no-go criterion. AC: pins,
receipts, labels, decision table, and measurements committed under
`spike/t281/`; double-run facts, citations, censuses, and retained outputs are
byte-identical; every refusal lands in the frozen vocabulary; an output scan
proves ACL credential tokens absent; no production code path changed and no
pack registered.

## Deliberate non-goals *(per historical PORT_MAP §7/§12)*

SCIM provisioning, multi-org RBAC / seats, and a cloned "Ask" chat app —
phebs stays **MCP-first** (agents bring their own chat) and **single-tenant**.
Kubernetes/Helm waits for the P6 fleet profile. Anonymous-access and
entitlement gating are deleted outright (config bool, no license backend).

---

## Standing rules

- Decisions land as dated ADR bullets in PLAN.md, same PR as the change.
- Every epic ends with a `make dev` demo state — no epic is "done" if it
  can't be shown end-to-end.
- Upstream repo is behavior reference only; `ee/` paths never opened.
- Personal hardware, personal time, no employer code or credentials.
