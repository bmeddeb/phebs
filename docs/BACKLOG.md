# phebs · active backlog

Epic 30 is in progress for service-scoped analysis of very large
monorepositories; T30.1 recorded a GO result and T30.2 is the next ticket.
Completed Epics 0–24, Epic 29, T30.1, and P5 hardening are retained in the
[completed backlog](./BACKLOG_COMPLETED.md). Current posture and decision
points are summarized in [ROADMAP.md](./ROADMAP.md).

New work starts here only after its product boundary, dependencies, acceptance
criteria, and dated [PLAN.md](../PLAN.md) decision are reviewed. Tickets remain
PR-sized and dependency-ordered for a stacked workflow.

## Scheduled ticket

**T30.2 · Analysis-unit config and committed state** is next. T30.1's retained
focused-index and shard-set spike recorded GO without changing production
behavior.

Production evidence/pilot gating and the distributed P6 fleet profile remain
explicitly gated or demand-driven in the roadmap. Epics 25–28 below remain
drafted and unscheduled.

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

## Epic 30 · Service-scoped monorepo analysis *(in progress 2026-07-28 · T30.2 next)*

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
  exact membership, every content/metadata digest, and the absence of an
  unexpected member before constructing a searcher. Per-shard metadata
  agreement alone is insufficient: a missing member leaves the generation
  unavailable rather than serving a valid-looking subset.
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
  requirements.
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
  cleanup, backup/restore, failure recovery, and bounded operator verification.
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

**T30.2 · Analysis-unit config and committed state** *(T30.1 GO recorded · next)* —
add the strict repository-keyed config, canonical digest, one-active-unit
validation, and store state that binds indexed revisions to the unit digest.
Changing only scope bytes queues a rebuild. Repo status and operator
diagnostics expose the unit name, digest, selected paths/counts, and exact
typed-index posture without leaking source content. Absent configuration is
byte-compatible whole-repository behavior. AC: strict YAML/schema/example
coverage, upgrade/reopen tests, scope-change job tests, path safety and
non-disclosure tests, PLAN and Configuration/Operations updates; full merge
bar.

**T30.3 · Focused zoekt child and shard integrity** *(needs T30.2)* —
ship the T30.1-proven child in development and release builds, invoke it for a
configured unit, retain `zoekt-git-index` for whole repositories, and commit
index state only after every scoped shard is durable. Search startup,
directory watching, reconciliation, force rebuild, revision allowlists,
backup/restore, orphan cleanup, and failed state commits validate both exact
branch/commit metadata, unit digest, index-generation digest, and the complete
shard-set manifest. Instrument the production Git-object reader itself so
opened-blob count/bytes and zero out-of-unit reads are measured at the trusted
read boundary rather than inferred from admitted paths or search results.
Backup/restore preserves and revalidates the exact published manifest,
sidecars, and shard bytes; an independent rebuild with the same semantic unit
and generation may have different publication digests because the pinned
builder embeds build identity and time, and is not byte-equal restoration. AC:
the reader-boundary counter proves zero out-of-unit blob reads and an
out-of-scope needle is absent from the physical shard; an admitted needle
searches under the original commit/path; scope-only changes replace shards;
stale/mixed/missing/extra-member sets never serve; the revision-set matrix is
pinned; backup/restore is byte-exact while equivalent rebuild semantics are
tested separately; child OOM/error classification remains intact; packaged
binary parity; and size-driven shard splits preserve identical
unit/revision/generation metadata plus exact expected membership; full merge
bar.

**T30.4 · Reusable candidate-partition manifest** *(needs T30.2)* — replace
per-domain complete-tree retention with one streamed commit census that
produces a content-addressed manifest: repository/unit candidates for focused
domains and deterministic repository-global caller partitions. The trusted
walker records blob IDs, total regular-file and gitlink digests, candidate
policy versions, per-domain counts/digests, and bounded partition files/rows
without retaining the full path set. Manifest publication, reuse, stale
replacement, retention, crash cleanup, and queue fan-out are atomic and
resumable. Implement the domain-separated path-hash prefix algorithm above;
freeze its initial bit depth and per-leaf candidate/declared-byte limits from
the spike measurements. AC: the T30.1 over-limit corpus plans successfully
inside frozen memory/disk/wall gates; every candidate is assigned exactly
once; leaf prefixes are prefix-free, disjoint, canonically ordered, and
deterministic across repeated runs; content-only changes preserve assignment
while changing manifest identity; an oversized singleton and an unsplittable
collision fail closed; noncandidate paths affect corpus coverage but consume
no retained path row; malformed, partial, duplicate, or stale manifests
cannot start extraction; full merge bar.

**T30.5 · Focused evidence publication** *(needs T30.3, T30.4)* — key
extraction attempts/runs and published evidence by repository, source commit,
unit digest, and domain; execute provisional contract, field, topic, local
consumer, attribution, and Workbench implementation readers only over the
unit manifest. Add a commit/unit-bound typed-index input contract instead of
relabeling a repository-root SCIP file. Store migrations preserve readable
legacy whole-repository evidence while preventing a scoped run from
superseding or satisfying a different unit. Contract Atlas, Topics, coverage,
source links, and provisional Workbench target/implementation views consume
real store-published focused evidence without fixture authority. AC:
scope/stale/rollback/mixed-writer tests, exact coverage disclosure,
deterministic ordinary-worker acceptance, full merge bar.

**T30.6 · Target-bound repository Caller Map generation** *(needs T30.4,
T30.5)* — build the bounded module/generated-resolution catalog for one
focused declaration set before any source-partition scan, execute
repository-global gRPC/Thrift caller partitions against that immutable
catalog, and atomically publish a complete generation. Extend reverse
lookup and Caller Map/comparison services to page across that generation
without weakening exact declaration identity, repository authorization,
cursor snapshots, or unresolved vocabulary. Focused and overlay evidence
remain visibly distinct; a missing/failed partition is a coverage gap, never
zero callers. AC: neutral cross-service callers outside the focused shard
appear with immutable citations, unrelated operation calls do not enter the
target generation, partial/stale generations remain invisible, 10,000+ caller
pages preserve existing bounds, Workbench Impact composes the overlay, full
merge bar.

**T30.7 · Scope-aware UI, operations, and epic demo** *(needs T30.3–T30.6)* —
show the active service unit and exact primary/supporting scope in Search,
Contracts, Topics, Caller Map, Impact, and Workbench; distinguish focused
search/local evidence from repository-overlay callers; render partition
progress, stale state, refusals, and typed-index gaps adjacent to results.
Reindex controls name the unit they replace. Add a neutral `make dev` cohort
that indexes one service unit, excludes an irrelevant bulk needle, publishes
real focused declarations/topics, and displays a caller from outside the
focused shard through the complete overlay—without Contract Atlas or
Workbench fixtures. AC: responsive/accessibility/bounded-DOM tests, API/MCP
schema parity, Operations/Workflows updates, end-to-end demo, full merge bar.

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
