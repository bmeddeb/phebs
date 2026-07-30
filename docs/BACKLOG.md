# phebs · active backlog

Epic 30 is in progress for service-scoped analysis of very large
monorepositories; T30.1 recorded a GO result, T30.2 committed the strict
configuration/state boundary, T30.3 shipped and then adversarially repaired
focused shard integrity, T30.4 shipped reusable candidate planning, and T30.5
shipped exact focused evidence publication. The post-T30.5 issue-repair gate
is closed. The separate large-monorepo review is complete, T30.6a shipped
bounded extraction job receipts, and T30.6b shipped durable per-domain
outcomes. T30.6c aggregate-bounded domain scheduling is next. Completed Epics
0–24, Epic 29, T30.1–T30.6b, and P5 hardening are
retained in the [completed backlog](./BACKLOG_COMPLETED.md). Current posture
and decision points are summarized in [ROADMAP.md](./ROADMAP.md).

New work starts here only after its product boundary, dependencies, acceptance
criteria, and dated [PLAN.md](../PLAN.md) decision are reviewed. Tickets remain
PR-sized and dependency-ordered for a stacked workflow.

## Scheduled ticket

**T30.6c · Aggregate-bounded domain scheduling** is next. T30.6a emits the
bounded non-authoritative operational receipt that makes shared job cost and
per-domain work visible, while T30.6b persists the exact-generation outcome
and typed retry disposition without inferring authority from that log report.
T30.6c schedules absent and retryable domains beneath aggregate job and lock
bounds. The accepted
large-monorepo review keeps T30.6 as the target-bound repository Caller Map
umbrella, split across PR-sized tickets for operational receipts, durable
outcomes, aggregate scheduling, source-lane classification and consumption,
catalog lifecycle and materialization, leaf execution and complete
publication, authorized consumers, and retention decision and implementation.
It does not raise existing global extraction limits, change current search
semantics, or introduce a physical Go-test search overlay.

Production evidence/pilot gating and the distributed P6 fleet profile remain
explicitly gated or demand-driven in the roadmap. Epics 25–28 below remain
drafted and unscheduled.

T30.5 deliberately retains every exact published commit/unit/domain tuple for
rollback; the existing evidence sweep does not collect a row while it remains
`published`. Before Epic 30 closes, a separate reviewed follow-up must decide a
bounded unpinned historical-publication policy (or explicitly retain the
unbounded posture) without deleting pinned proof. T30.6m owns that decision and
T30.6n implements only its selected posture.

### Post-T30.5 issue closure ✅ *(closed 2026-07-29)*

This completed repair is a prerequisite, not T30.6 implementation.

- **GitHub #2 · whole-repository generation handoff** — publish a durable exact
  whole-shard receipt; make immediate HEAD, branch, tag, and revision-set
  Search/Stream queries bind the committed generation without sleeps; never
  leak or silently omit an old/mixed generation; recover or rebuild missing,
  invalid, marker-covered, and pre-receipt publications; retain bounded,
  non-latching validation and exact cleanup.
- **GitHub #3 · focused-local candidate replay** — advance candidate
  publication identity to v3 with exact per-domain in-unit projections; prove
  repository bytes are read once, caller leaves remain one validation pass,
  and replay is only `P_d` rather than `(D + 1) × B_repository`; keep
  repository/caller planes unchanged; enforce descriptor stability, exact
  coverage, aggregate projection bounds, crash recovery, and v2 replacement.
- **Closure evidence** — repeated adversarial Search and Stream tests,
  candidate and extraction cost instrumentation,
  tamper/marker/reconciliation/migration fixtures, and the refreshed T30.4
  receipt are retained. Full repository and race gates passed; detailed repair
  commits `76f68f2` and `f74fd49` were pushed before evidence-backed closure of
  GitHub issues #2 and #3. No merge into `main` is authorized by this section.

#### Documentation updates

- `PLAN.md` records the exact whole-search handoff, candidate-manifest-v3
  projection contract, resource bounds, and T30.6 pause in dated decisions.
- `docs/guides/OPERATIONS.md` owns publication-upgrade/recovery diagnostics and
  the `B_repository + C_caller + ΣP` strict-open / `P_d` replay cost model.
- `spike/t304/README.md` and `spike/t304/results.json` retain the refreshed
  deterministic v3 measurement and distinguish its 16 MiB fixture gate from
  the production aggregate projection ceiling.
- `docs/ROADMAP.md`, this active backlog, and
  `docs/BACKLOG_COMPLETED.md` must agree on issue closure. The accepted
  monorepo review supersedes only the sequencing pause, not this repair record.

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

## Epic 30 · Service-scoped monorepo analysis *(in progress 2026-07-28 · T30.6c next)*

Make one service inside a very large monorepository a first-class analysis
unit without pretending that a path-filtered query makes a whole-repository
index cheap. Contracts, Topics, source search, related implementation, and the
Workbench operate on the focused unit. Caller Map and caller-backed Impact
retain a bird's-eye repository view through a separate target-bound,
partitioned relationship overlay over the same immutable commit.

This is a single-node scale program. It precedes and does not authorize the P6
distributed fleet profile.

### Analysis-unit contract

- `analysis-unit-v1` has one stable configuration identity: repository,
  operator-chosen unit name, and a unit digest over canonical scope bytes.
  Source commits are generation inputs, not part of that stable digest. An
  indexed generation adds the complete ordered revision set; HEAD-bound
  extraction and relationship generations add the authoritative HEAD commit.
  The scope contains exact **primary roots** and exact **supporting files or
  roots**. Supporting inputs cover only explicitly selected declaration,
  generated-source, module/workspace metadata, attribution, and typed-index
  artifacts; phebs does not execute a build, dependency query, generator, or
  service-discovery command to infer them.
- Paths are clean UTF-8 repository-relative Git paths. A directory admits its
  descendants while preserving their complete original repository-relative
  names. Empty, absolute, parent-traversing, backslash, duplicate, and
  canonically overlapping entries fail startup. A selected path that is
  absent or not a regular blob/directory at the indexed commit fails the unit
  build rather than silently shrinking it.
- The first version admits at most one active unit per repository per
  instance. This keeps the canonical repository name, avoids duplicate
  overlapping shards, and makes every unqualified search deterministic.
  Changing unit name or scope bytes is an index/extraction generation change,
  even when HEAD is unchanged. Multiple simultaneous units require a later
  reviewed storage/query identity design.
- Repositories without an analysis unit retain today's whole-repository
  indexing and extraction behavior. A configured unit is never widened
  automatically to make an extractor succeed.
- The unit digest is part of committed index state and is stamped in zoekt
  repository metadata. Search, startup reconciliation, cleanup, source,
  coverage, evidence, Workbench snapshots, and opaque cursors fail closed on
  a missing or mismatched digest. Repository visibility remains the
  authorization boundary in v1; a unit grants no visibility beyond its
  repository.

### Revision-set matrix

The same canonical scope is evaluated independently at every revision admitted
by the existing T10.4 repository allowlist. Scope never follows a rename or
widens to compensate for historical layout:

| Revision lane | Scope evaluation | Missing selected path | Product behavior |
|---|---|---|---|
| Implicit `HEAD` | Resolve every exact file/root at the indexed HEAD commit | Refuse the complete index generation | Authoritative unqualified search plus all extraction/evidence |
| Explicit branch/tag selector | Resolve the identical exact file/root set at that selector's peeled commit | Refuse the complete index generation; never publish a silently smaller historical scope | Search-only `rev:` lane; no extraction, coverage, proof, or Workbench evidence |
| Same directory, different contents | Admit the regular descendants present under that selected directory at each commit | Not missing when the directory itself exists | Search reflects that revision's immutable contents under the same unit scope |
| Scope bytes change | Re-evaluate every admitted revision even when all commits are unchanged | Any refusal leaves the previous complete generation visible | New unit digest and new index generation |
| Revision set or peeled commit changes | Keep the unit digest; recompute the ordered revision-generation identity | Any refusal leaves the previous complete generation visible | New index generation; HEAD evidence changes only when HEAD changes |

The index-generation digest is domain-separated over repository, unit digest,
the ordered `(selector, shard branch, peeled commit)` set, and the focused
builder policy generation. Extraction continues to bind `(repository, HEAD
commit, unit digest, extractor generation)` and never inherits an explicit
search revision.

### Three partition layers

- The **semantic service unit** is the operator's primary and supporting path
  set. It is the only partition that defines focused product scope and keeps
  one stable unit digest even if implementation details change.
- **Physical zoekt shards** are size-driven outputs of the pinned
  `index.Builder`, not service partitions. One unit may produce one or many
  shards. Every shard carries the same repository name, original revision
  set, unit digest, and index-generation digest plus a stable member ordinal
  and expected member count. A separately checksummed shard-set manifest
  commits the ordered `(ordinal, shard digest, shard metadata digest)` set.
  Shards stage outside the visible set; the manifest becomes visible only
  after every member is durable. The search wrapper validates the manifest,
  exact repository-local membership, every content/metadata digest, and the
  absence of an unexpected member before binding a query to that exact member
  set. Validation may be reused only while the committed identity and every
  already-bound manifest/member/sidecar identity agree. Warm queries inspect
  only those repository-local identities: an undeclared added file cannot
  enter the static reader, while exact-extra rejection remains mandatory on
  cold admission and reconciliation. Shared-directory watcher timing and
  another repository's transient shard state never grant or deny admission.
  Per-shard metadata
  agreement alone is insufficient: a missing member leaves the generation
  unavailable rather than serving a valid-looking subset. Private build and
  restore workspaces carry a process token, so reconciliation preserves
  active same-process work and reclaims only prior-process
  workspace/temporary-marker residue.
- **Repository-overlay caller partitions** are bounded work units over
  repository-wide caller candidates for one focused declaration set. They are
  neither searchable shards nor independently visible evidence. A caller
  partition may cite source outside the semantic unit without admitting that
  source into focused search, Contracts, Topics, or local implementation
  evidence.

### Focused index and local evidence plane

- `zoekt-git-index` has no service-root include contract, so passing a path
  atom only narrows query results after the expensive whole repository has
  already been indexed.
- The selected implementation candidate is a phebs-owned child built from the
  same module as the server. It streams the exact source commit's tree,
  rejects paths outside the unit before blob content is opened, and adds only
  admitted documents to the pinned upstream `index.Builder`. Shard repository
  name, document paths, and branch versions remain the canonical repository,
  original full paths, and original commits. The child retains the current OOM
  isolation, bounded output, atomic replacement, and same-SHA reader/writer
  requirements. Focused builder policy v2 sets zoekt's document limit to the
  trusted reader's 64 MiB blob ceiling and preflights the same pinned content
  classifier before `Add`: accepted text through the size limit is searchable,
  while an oversized, binary, sub-trigram, or over-20,000-distinct-trigram
  blob refuses the complete generation rather than being silently dropped.
  The child retains path/blob plans without preloading the corpus. The pinned
  builder holds only its current 64 MiB shard batch, with at most one
  admitted-document overshoot, and flushes synchronously. The child requires
  its measured out-of-unit counter to remain exactly zero and refuses any
  control output beyond the matching 1 MiB reader envelope. Cancellation
  during pre-child Git configuration remains `context.Canceled` rather than a
  killed-process error that could be mistaken for OOM.
- Search opens only the exact validated member descriptors in a static
  no-watcher composite. One 10-second wall budget covers compilation,
  starter-owned cold validation/materialization, zoekt execution, and
  result-time identity checks. At most two cache-owned fills run at once. The
  same exact-generation query joins an in-flight fill, and saturated cold work
  queues behind those slots; every waiter uses its own query deadline, whose
  expiry fails the query instead of returning a knowingly partial RepoSet. A
  timed-out fill may continue for up to 10 minutes, a later query reuses its
  completed exact binding, and shutdown cancels and joins the loaders. Stable
  negative validation entries retry with bounded 250 ms–30 s exponential
  backoff, while a fingerprint change retries immediately. JSON fan-out has a
  fixed eight-worker ceiling and incrementally retains only the global top K;
  SSE retains the shipped progressive per-shard, arrival-order contract under
  one shared display ceiling. Both focused and whole-result paths recheck
  current committed posture and revision, so a same-HEAD whole-to-focused
  transition fails closed to a conservative short result. Cache pruning
  retires deleted, unindexed, or whole-posture focused bindings after active
  leases release.
- A projected Git tree/commit is explicitly not the default fallback: its
  synthetic commit would become the shard version and force provenance
  rewriting across search, source, SCIP, history, evidence, and Workbench
  readers. If the builder spike fails, implementation stops for a new ADR.
- Focused extraction reads one reusable commit/unit candidate manifest rather
  than independently retaining the complete repository inventory for every
  domain. Contract declaration, field, topic, local consumer, attribution,
  and Workbench implementation evidence publish under the unit digest.
  Source reads still use original blob IDs from the canonical bare mirror.
- A scoped typed-index input must declare and validate its own unit binding.
  The current repository-root `index.scip` contract is not silently treated as
  service-scoped merely because the search index is smaller.

### Repository-wide relationship overlay

- Caller Map does not require a whole-repository search shard. It requires a
  trustworthy repository-wide source census and callers that resolve to the
  focused declaration set.
- One streamed tree census per source commit records total regular-file and
  boundary counts/digests while writing only bounded candidate records into a
  deterministic partition manifest. It never retains all repository paths in
  memory and never weakens a refusal into partial coverage. Candidate policy,
  partition count/ranges, source commit, blob IDs, extractor versions, and
  manifest digest are immutable generation inputs.
- Candidate assignment is exact. For normalized repository-relative path
  `p`, compute
  `H = SHA-256("phebs-caller-path-v1\0" || UTF8(p))`. Blob OID and declared
  size are manifest identity inputs but do not affect `H`, so content changes
  do not arbitrarily move an unchanged path between work partitions.
  Planning begins at one initial hash-prefix bit depth frozen by T30.4.
  Any bucket exceeding either the measured candidate-count limit or summed
  declared-blob-byte limit splits by the next hash bit, recursively. A single
  candidate exceeding the byte limit, or a bucket that cannot split at 256
  bits, refuses the generation rather than weakening a bound.
- Materialized non-empty leaf prefixes are prefix-free and disjoint, and are
  numbered by ascending numeric hash-range lower bound. Candidate records
  within a leaf are ordered by `(H, path UTF-8 bytes, blob OID)`. The initial
  depth, both limits, domain separator, split rule, and record ordering are
  manifest-policy fields; changing any of them creates a new policy
  generation. Every admitted caller candidate belongs to exactly one leaf.
- After the candidate manifest is durable, one bounded immutable
  module/workspace/generated-resolution catalog is built for the same commit
  and focused declaration-set digest. Partition source scans start only after
  that catalog publishes, and every scan consumes its digest; partitions do
  not independently rediscover module or generated-client identity.
- A relationship request is bound to the focused declaration-set digest.
  Partition jobs may read repository-wide caller candidates and the bounded
  module/generated-resolution catalog needed for those declarations, but
  emit only resolved edges, exact name matches, and extractor abstentions
  relevant to that target set. This avoids materializing an unrelated
  repository-global call graph.
- Partition runs remain invisible until one generation transaction proves
  every declared partition terminal at the same repository commit, unit
  digest, declaration-set digest, candidate-manifest digest, and extractor
  generation, plus the same resolver-catalog digest. Failure, cancellation,
  stale HEAD, changed scope, or a missing partition leaves the previous
  complete generation visible.
- Caller Map pages merge the complete generation under the existing strict
  paging and authorization rules. Citations outside the focused search index
  open from the immutable Git mirror and are labeled as
  `repository-overlay`; their presence does not imply that unrelated source
  was indexed for search or local implementation analysis.
- Topics remain focused-unit evidence in Epic 30. A future repository-global
  topic inventory would need its own target and partition contract; the
  Caller Map exception is not a generic authorization for every extractor to
  scan globally.

### Scale and trust boundary

- Do not raise the current 200,000-file, 16 MiB retained-path, 512 MiB
  distinct-read, 12,500-fact, or 15-minute single-run limits as the solution.
  New manifests and partitions have separately measured hard bounds; every
  page and coverage certificate discloses unit roots, global-overlay status,
  candidate counts, partition completion, refusals, and stale state.
- Deterministic tests use generated neutral repositories. Production and
  manual evaluation consume ordinary synced commits and store-published
  evidence; no Contract Atlas or Workbench fixture is required.
- No employer repository name, path, code, schema, build metadata,
  credential, host, measurement, or infrastructure enters source, tests,
  retained records, logs, or documentation. Optional local evaluation is
  operator-only and is never a merge-bar artifact.
- No runtime-use, completeness, extraction-accuracy, migration-completion, or
  decommission-safety claim follows from a complete unit or relationship
  generation.

### Documentation updates

- `PLAN.md` records each production identity, publication, partition, and
  compatibility decision in the same ticket that implements it.
- `docs/guides/CONFIGURATION.md` owns the strict analysis-unit schema,
  path/revision semantics, defaults, limits, and typed-index posture.
- `docs/guides/OPERATIONS.md` owns build/publication diagnostics, reconciliation,
  cleanup, backup/restore, failure recovery, retained-history storage posture,
  and bounded operator verification.
- `docs/guides/WORKFLOWS.md` owns the user-visible distinction between focused
  search/local evidence and repository-overlay callers, including evidence
  caveats and the end-to-end demo.
- `docs/MANUAL.md`, `docs/README.md`, the roadmap, and the active/completed
  backlog update when their routing, posture, sequencing, or ticket state
  changes. Spike records under `spike/` retain decisions and measurements; they
  never substitute for behavior documentation.

**T30.1 ✅ · Service-scope contract and focused-index spike** *(2026-07-28;
GO)* — completed and retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t301--service-scope-contract-and-focused-index-spike)
and [executable spike record](../spike/t301/README.md). It changed no
production config, store, queue, API, or UI behavior.

**T30.2 ✅ · Analysis-unit config and committed state** *(2026-07-28)* —
completed and retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t302--analysis-unit-config-and-committed-state).
It introduced no focused physical indexing; configured and unconfigured
repositories still use the existing whole-repository child.

**T30.3 ✅ · Focused zoekt child and shard integrity** *(2026-07-28; repaired
2026-07-29)* — completed and retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t303--focused-zoekt-child-and-shard-integrity).
Configured repositories now publish only their selected paths through the
manifest-bound focused child and serve each query from the exact validated
generation. Zoekt-admissible 2–64 MiB focused text remains searchable and
every content-policy rejection fails the generation explicitly; stale derived
publications cannot block precious-state backup; absent configuration retains
whole-repository indexing. Typed-index input remains repository-root-unbound.

**T30.4 ✅ · Reusable candidate-partition manifest** *(2026-07-28; repaired
2026-07-29)* — completed and retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t304--reusable-candidate-partition-manifest).
Existing extraction now waits for one current, strictly validated candidate
publication and still consumes its repository-wide view. Exact no-op work
uses only its committed digest identity, while stale/forced work strictly
opens the bytes once. A process-local control fingerprint detects later
manifest/member identity drift and sends it through strict validation/rebuild
without hashing member contents on warm retries; same-stat damage remains a
strict-consumption refusal, not a metadata claim. Unit membership is
precomputed only; T30.5 owns the evidence-scope and identity change.

**T30.5 ✅ · Focused evidence publication** *(2026-07-29)* — completed and
retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t305--focused-evidence-publication).
Local contract, field, topic, consumer, attribution, and Workbench
implementation evidence is now candidate- and store-bound to the exact
repository, indexed HEAD, committed unit digest, and domain. A designated SCIP
input keeps its real supporting path and blob identity; focused navigation and
extraction never fall back to repository-root `index.scip`. Legacy
whole-repository evidence remains readable only in its empty-unit scope, and
no publication can satisfy or supersede a different unit.

**T30.6 · Target-bound repository Caller Map generation** *(needs T30.4–T30.5;
large-monorepo review and post-T30.5 issue repairs complete)* — retain one
focused local-evidence plane and add one independently bounded relationship
plane without raising the existing global extraction limits or building a
whole-repository search index. The umbrella is split into the following
dependency-ordered, one-PR tickets.

**T30.6a ✅ · Bounded extraction job receipts** *(2026-07-29)* — completed and
retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t306a--bounded-extraction-job-receipts).
Every repository extraction job now emits one capped
`phebs-extraction-operation-v1` operational report. Shared queue, mirror-lock,
pointer, and strict-open work remains job-level; nested domains carry only
generic outcomes and bounded phase/count/byte/limit diagnostics. The report is
non-authoritative, source-free, and failure-isolated and changes no store/API
schema or retry disposition.

**T30.6b ✅ · Durable per-domain outcomes and retry disposition** *(2026-07-30;
needs T30.6a)* — completed and retained in the
[completed backlog](./BACKLOG_COMPLETED.md#t306b--durable-per-domain-outcomes-and-retry-disposition).
One latest-only exact-generation outcome per repository/domain now carries a
bounded transactional receipt and typed `published`,
`unavailable_prerequisite`, `terminal_generation_refusal`, or
`retryable_failure` disposition. Exact settled generations survive restart and
short-circuit; retryable and absent generations rerun; every scope, candidate,
extractor, inventory, typed-input, dependency, or candidate-control change
invalidates immediately. Published outcome and evidence commit atomically,
nonpublished outcomes preserve prior visibility, focused missing SCIP records
unavailable before staging, and whole-repository legacy behavior is unchanged.
Strict same-semantic candidate repair advances a durable control revision,
clears only its matching terminal control outcome, and creates exactly one
extraction successor.

**T30.6c · Aggregate-bounded domain scheduling** *(needs T30.6b)* — replace the
one shared domain cancellation context without multiplying wall time or mirror
lock hold by domain count. Freeze one numeric aggregate post-lock job budget,
an equal or smaller cumulative mirror-lock bound, a per-domain cap bounded by
the remaining aggregate budget and strictly smaller than the aggregate budget,
maximum serial domains, memory, and staged-row cost. Starting or retrying a
domain never extends either aggregate bound; work that cannot start within the
remaining budget records a retryable outcome without erasing terminal or
published peers. A successor job schedules never-attempted domains first, then
retryable domains by oldest persisted attempt. AC: early deterministic failure
does not starve later domains, one slow retryable domain cannot start twice
before every configured peer gets one start opportunity, slow-domain cap,
aggregate cap, lock-hold cap, restart scheduling, retry-only execution, bounded
stage cleanup, race/full merge bar.

**T30.6d · Candidate-v4 source-lane classification** *(needs T30.6c)* —
advance manifest/state/record schemas; enumeration/local-projection policies;
policy, generation, inventory, and self-digest domain separators; artifact
namespace; extraction inventory prefix; control fingerprint; projection
identity; and external-merge comparison. Every ordinary record carries
`source_lane: base|go_test`: exact `_test.go` suffix wins even under generated,
mock, fixture, or `testdata` paths, and every other ordinary candidate is
`base`. Strict validation recomputes the lane from the canonical path rather
than trusting stored bytes. Candidate v3 is never current under v4:
reconciliation clears its pointer and force-enqueues replacement. This ticket
changes no extractor consumption, evidence, focused shards, or search
generation. AC: suffix/overlap and forged-lane fixtures, marker and
descriptor-stability boundaries, missing/extra/reordered projections,
backup/restore/cleanup, same-HEAD policy transition, v3 replacement, refreshed
neutral T30.4 receipt retaining `B_repository + C_caller + ΣP`, `P_d`, and zero
additional source-blob reads, full merge bar.

**T30.6e · Focused local-evidence base-lane consumption** *(needs T30.6d)* —
for repositories with a committed non-empty analysis unit, `grpc-consumer`,
`thrift-consumer`, `kafka-producer`, and `kafka-consumer` skip `go_test` rows
before blob open and report excluded files and declared blob bytes. The focused
`scip-proto-field` and `scip-thrift-field` readers retain one designated typed
blob: they open and globally safety-account the complete artifact once, then
classify every canonical document path and remove the complete semantic
contribution of each exact `_test.go` document—definitions, anchors,
occurrences, and joins—before any ordinary source read, resolution, or fact
emission. They report excluded documents/definitions/occurrences and do not
open corresponding ordinary test-source blobs. Repositories with an empty unit
digest retain shipped whole-repository extraction behavior: candidate v4
records the lane, but whole-repository consumers ignore it. Advance every
affected focused extractor and candidate-policy generation so prior
test-bearing focused evidence cannot remain current; keep the exact
`(repository, commit, unit, domain)` publication identity and add no
test-evidence lane. AC: zero ordinary excluded-test blob reads, exact excluded
declared-byte accounting, SCIP global bounds and whole-document filtering,
test-only definition/anchor referenced by a non-test occurrence yields no
resolved fact and no test-source open, no focused test fact leakage, unchanged
empty-unit whole-repository behavior, replacement/freshness fences, and default
Search plus Stream still return a needle present only in an admitted exact
`_test.go` file because candidate-lane changes never alter focused shard/search
identity; full merge bar.

**T30.6f · Resolver-catalog lifecycle** *(needs T30.6e)* — define the immutable
catalog schema, identity, member receipts, publication, validation, recovery,
backup, and cleanup without yet implementing resolver adapters. Identity binds
repository, indexed HEAD, unit, ordered declaration-publication identities,
declaration-set digest, candidate-manifest-v4 digest, source-lane policy,
ordered resolver-pack/version set, and catalog policy. Canonical members carry
name, length, content/metadata digests, and manifest self-digest. Members become
durable first, the manifest renames last under a marker, the store pointer
commits only after manifest durability, and the marker clears only after that
commit. Cold validation is descriptor-stable; warm no-op checks control/file
identity with zero member-content hashing. Freeze numeric record/content,
memory/disk/open-file bounds. A valid publication is archived exactly; an
invalid or marker-covered derived publication is omitted with a bounded report,
restore never installs or retains its exported pointer, and reconciliation
force-enqueues replacement. AC: store-writer/schema compatibility and
migration, empty/neutral fixture catalog, tamper/symlink/descriptor swap, every
crash boundary, prior-process staging, canonical local ownership, exact archive
or bounded omission, restore pointer clearing, reconcile/requeue, cap/cap+1,
full merge bar.

**T30.6g · Bounded resolver materialization** *(needs T30.6f)* — implement the
bounded v1 resolver set over immutable candidate and declaration inputs: only
the existing committed Go module identity and committed generated-attribution
inputs required by the shipped gRPC and Thrift caller resolvers. New workspace
formats or resolver packs require later tickets. No build, `go list`,
dependency query, generator, corpus execution, mutable checkout, or network
request is allowed. Ambiguous or unsupported identity remains explicit rather
than selecting by tie-breaker, and partitions never rerun discovery. AC:
neutral module/generated fixtures, ordered adapter/version identity,
missing/special/stale input, ambiguity, deterministic double run, no unplanned
blob reads, populated-catalog warm no-op with zero input blob reads/hashes,
lifecycle bounds inherited from T30.6f, full merge bar.

**T30.6h · Direct caller-leaf execution artifacts** *(needs T30.6g)* — consume
T30.4 caller leaves directly without rebuilding a flattened repository path
inventory. Default work processes only `base`; `go_test` remains retained
planning input for a future separately authorized generation. Address one
artifact and durable leaf outcome by `(caller domain, leaf prefix, complete
generation identity)`; the expected set includes every declared pair, including
explicit successful empty/abstention artifacts. Each worker opens only blobs
declared by its leaf, measures exactly zero out-of-leaf reads, and writes an
immutable artifact that is not independently product-visible. A terminal
outcome for one pair does not erase a successful sibling domain, but prevents
the complete generation containing that domain from publishing. Leaf state
carries schema/writer generation, exact artifact receipt/digest, and
disposition. Restart descriptor/content-validates a successful artifact once
before reuse; prior-process staging and file-without-state or state-without-file
cases are reclaimed or requeued without trusting decoded cross-repository
content. Freeze per-pair and aggregate-generation limits for artifact count,
result/abstention records, canonical content bytes, staging disk, and
concurrently open files. Aggregate admission is checked before complete
publication; cap+1 refuses replacement while preserving the prior complete
generation. AC: cross-service and unrelated-target neutral fixtures, per-record
abstentions, explicit empty pair, sibling-domain failure isolation, no all-leaf
memory materialization, fixed worker/memory/deadline bounds, per-pair and
aggregate cap/cap+1 output receipts, prior-generation preservation, leaf
tamper/descriptor swap, every restart mismatch, crash/resume, no
aggregate-path-limit dependency, full merge bar.

**T30.6i · Atomic complete caller-generation publication** *(needs T30.6h)* —
coordinate only successfully published caller-domain/leaf artifacts into one
receipt naming the exact ordered expected pair set and each artifact's identity,
canonical name, record count, bytes, content/metadata digests, and abstention
summary. Generation identity binds repository, HEAD, unit, declaration set,
candidate-manifest-v4, `base` lane, resolver catalog, caller policy, and ordered
extractor versions. A terminally refused, missing, stale, or invalid pair
prevents replacement visibility. Result artifacts become durable first, the
checksummed manifest renames last under a marker, the store pointer commits
after durability, and the marker clears after the matching commit. The same
store transaction that publishes, clears, invalidates, or restores the pointer
advances one repository-local monotonic caller-publication revision; an exact
no-op does not advance it, while every real transition, including
`A → unavailable → A`, does. Reconciliation covers every crash boundary,
validates canonical local ownership, reclaims prior-process staging, and never
trusts an artifact-selected cross-repository path. First admission performs
descriptor-stable cold validation of the complete receipt; warm admission
checks stable control/file identity without rehashing every artifact. Active
readers lease retired generations. A valid publication is archived exactly; an
invalid or marker-covered derived publication is omitted with a bounded report,
restore never installs or retains its exported pointer, and reconciliation
force-enqueues replacement. AC: store-writer/schema compatibility and revision
migration, partial/stale invisibility, same-HEAD unit transition, exact no-op
revision, `A → unavailable → A`, marker/tamper/swap/cross-repository fixtures,
lease retirement, cold descriptor stability, exact archive or bounded omission,
restore pointer clearing, zero warm content hashes, full merge bar.

**T30.6j · Authorized exact Caller Map reads** *(needs T30.6i)* — move reverse
lookup and Caller Map paging onto one exact complete generation. Unauthorized
repositories remain absent from rows, gaps, totals, and cursors under the
existing non-disclosure contract. For an already authorized repository,
missing, failed, or stale generation state is explicit and never zero callers
or a partial page. Cursor/result fences bind full generation identity plus a
monotonic caller-publication revision so `A → B → A` cannot evade validation.
Every citation binds the generation commit and blob identity and reads that
immutable object, never mirror HEAD. Citation access discloses only the exact
authorized cited path/range at that commit; it grants no unrelated path
listing, directory browsing, or source access and does not widen focused
search/local evidence. Exact static bindings retire by lease; result-time
authorization is rechecked. AC: 10,000+ caller rows traversed over multiple
pages under the existing maximum page size, no per-page full
hash/materialization, bounded query/read/memory cost, non-disclosure,
permission loss, transition races, immutable citations, HTTP/MCP parity, full
merge bar.

**T30.6k · Caller comparison integration** *(needs T30.6j)* — bind migration
comparison to exact authorized caller-generation snapshots without changing
old/replacement declaration identity or unresolved vocabulary. Missing or
stale authorized input remains a typed gap, cursors bind both sides, and no
caller row is inferred from absence. AC: old-only/both/new-only/unresolved
neutral fixtures, independent side transitions, permission loss, bounded
paging, immutable citations, full merge bar.

**T30.6l · Workbench Impact caller integration** *(needs T30.6k)* — compose the
exact caller generation through the existing Workbench revision/evidence
snapshot and authorization fences. Focused local evidence and
`repository-overlay` callers remain separately typed; caller gaps cannot become
completeness, migration-completion, or retirement-safety claims. AC: current
Workbench revision, stale caller transition, hidden repository, bounded
composition/cursors, immutable citations, API/UI fixture parity, full merge
bar.

**T30.6m · Historical-publication retention decision** *(needs T30.6l)* —
change no cleanup behavior. Build a retained neutral storage/invariant model
and select in a dated ADR either an exact bounded policy or the current
unbounded posture. A bounded selection must freeze count/age/byte dimension,
per-repository/domain versus global scope, default/config surface, batch and
sweep bounds, evidence/caller/catalog/artifact coverage, pin ownership,
transaction and lease ordering, and restore-before-sweep behavior. An
unbounded selection must require bounded retained-count/byte status plus an
explicit capacity warning. The decision must also prove its selected
implementation fits one PR; if a bounded selection crosses independent state
owners, this ticket adds dependency-ordered implementation tickets before
authorizing cleanup and T30.6n is not treated as an umbrella PR. AC: selected
decision and escape hatch, implementation-size proof, pin/current/
failed-replacement/backup matrix, neutral capacity receipt, Operations update,
docs/static gates.

**T30.6n · Selected retention posture** *(needs T30.6m)* — implement only the
posture selected and fully specified by T30.6m. A bounded selection adds its
cap/age/byte, pin/unpin race, batch, active-lease, failed-replacement,
backup/restore, and same-commit-unit tests. An unbounded selection performs no
deletion and adds the required bounded status/capacity warning tests. Either
path preserves pinned proof, active/current generations, immutable cited
inputs, and latest failed-replacement diagnostics. Full merge bar.

### T30.6 documentation updates

- Every T30.6 PR adds its dated identity/publication/resource decision to
  `PLAN.md` and updates this dependency/AC record without rewriting historical
  decisions.
- T30.6a–T30.6c update `docs/guides/OPERATIONS.md`; T30.6b also updates
  durable failure/outcome troubleshooting and backup/restore guidance.
- T30.6d–T30.6e update Operations and `docs/guides/CONFIGURATION.md` while
  stating that source lane is neither semantic unit scope nor search
  configuration; T30.6d also updates backup/restore guidance for candidate-v4
  replacement.
- T30.6f–T30.6i update Operations and backup/restore guidance for catalog,
  leaf, complete-generation, ownership, gap, and recovery behavior.
- T30.6j–T30.6l update Operations and `docs/guides/WORKFLOWS.md` for
  authorization, citations, Caller Map, comparison, and Workbench composition.
- T30.6m–T30.6n update Operations and backup/restore guidance with the selected
  retention posture, and update `docs/guides/CONFIGURATION.md` whenever that
  posture exposes configuration. `docs/MANUAL.md`, `docs/README.md`, roadmap,
  and active or completed backlog change whenever routing or ticket posture
  changes.
- Retained gates use generated neutral repositories and bounded receipts. The
  private operator report and all employer-specific identifiers, paths,
  measurements, code, hosts, and infrastructure remain outside the repository.

**T30.7 · Scope-aware UI, operations, and epic demo** *(needs T30.6a–T30.6n)* —
show the active service unit and exact primary/supporting scope in Search,
Contracts, Topics, Caller Map, Impact, and Workbench; distinguish focused
search/local evidence from repository-overlay callers; render durable
per-domain outcomes and bounded domain receipts, `base` and excluded `go_test`
counts, partition progress, stale state, refusals, and typed-index gaps adjacent
to results, while job-level queue/lock diagnostics remain operational logs.
Reindex controls name the unit they replace. Add a neutral `make dev` cohort
that indexes one service unit, excludes an irrelevant bulk needle, publishes
real focused declarations/topics, and displays a caller from outside the
focused shard through the complete overlay—without Contract Atlas or Workbench
fixtures. This ticket adds no physical test-search overlay or test toggle. AC:
responsive/accessibility/bounded-DOM tests, API/MCP schema parity,
Operations/Workflows updates, end-to-end demo, full merge bar.

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
