# T26.1 SQL evidence spike — preregistered inputs

Status: complete source/label preparation; parser comparison awaits the lead's
independent immutable input checkpoint. No imported SQL parser or bounded subset
grammar has executed. This is an isolated spike; no production dependency,
schema, reader, pack registration, accuracy claim or release is authorized.
The lead must independently review and checkpoint the complete input/label
inventory, including the actual generated PostgreSQL dump, before the first
parser execution. Architecture adoption belongs to the owning dated PLAN ADR.

## Corpus and permission

The exact public pins are Hatchet
`559b5021e418f12ded175f950b709b7fa66be5a5` and ntppool/monitor
`e03c40a06ae8f9bd4906001c2ede0c7296ec8e96`. Their applicable MIT permission
grants were read before application/schema content. Exact notices remain in
`licenses/`. Repository-tree metadata was inspected for relevant nested
license overrides before source download; none applies to these SQL paths.
Every downloaded file's size and Git blob SHA1 matched the pinned tree before
its SHA256 entered `corpus-files.json`. The retained 296 files total
2,024,806 bytes: Hatchet's 214 migrations, five manifest schema inputs,
36 authored queries, their 36 generated Go duplicates and manifest; monitor's
dump, authored query, generated Go duplicate and manifest.

The Hatchet sqlc manifest establishes PostgreSQL; monitor's manifest says
MySQL and its committed MariaDB dump header independently establishes a
MySQL-compatible captured dump. The public dump's header, capture time and
placement are source bytes, never proof of an actual database, environment,
service, host, ownership or deployment. No corpus query or migration runs.

The five complete Hatchet declaration populations are:

| Input | Bytes | Statement envelopes | Declaration objects |
|---|---:|---:|---:|
| `sql/schema/v0.sql` | 65,225 | 342 | 981 |
| `sql/schema/v1-core.sql` | 90,280 | 126 | 546 |
| `sql/schema/v1-olap.sql` | 45,131 | 79 | 231 |
| `sql/schema/pg-stubs.sql` | 3,120 | 6 | 84 |
| `pkg/repository/sqlcv1/concurrency-additional-tables.sql` | 198 | 1 | 1 |

Objects include tables, columns, explicitly written keys/FKs/indexes/views and
view outputs, enums, sequences, exclusions and range types. Implicit indexes,
identity/BIGSERIAL sequences and dynamically created partitions are not
invented. Every other top-level statement has an explicit wrapper or gap
disposition. `pg-stubs.sql`'s six views/78 outputs are committed tooling models;
they never become real pg_catalog or runtime facts. The additional LIKE model
declares its exact table name but contributes no inferred copied columns,
indexes or keys; expansion is an explicit gap. These source roles remain visible.

The entire MySQL dump has 216 statement/envelope dispositions and 488 current
objects: 36 tables, 275 columns, 36 primary keys, 31 unique constraints,
52 explicit indexes, 47 foreign keys, one final view and ten view outputs.
Its temporary constant stand-in view is a separate wrapper and supplies zero
current objects. The final split versioned-comment view supplies the single
current view population. SET/DROP dump scaffolding is never migration history.

## Sampling frozen before any parser

`sampling.json` freezes all 250 eligible Hatchet migration/query paths,
source hashes, sizes, stratum scores and selection flags, plus the 238 explicit
unsampled paths. Seed:
`phebs-t261-stratified-files-v1-2026-10-18`. Score is SHA256 of UTF-8
`seed + NUL + plane + NUL + source_commit + NUL + repository_relative_path`.
Ties sort by path. No SQL syntax or candidate outcome drives selection.

- Migrations: lowest score in each nonempty year (2024/2025/2026) × file size
  (at most 1,024 bytes/greater) cell: six complete files of 214. Both explicit
  Goose Up and Down branches are labelled as separate history occurrences.
- Queries: two lowest scores in each size cell (at most 1,024,
  1,025–8,192, greater than 8,192 bytes): six complete files of 36.
- Monitor: its sole authored query file is labelled completely; there is no
  real MySQL migration population. All schema inputs/dumps are complete.

These are finite file samples, not an independent population accuracy study.
No result generalizes to the 208 unsampled migrations, 30 unsampled queries,
other repositories, syntax families, databases or operating loads.

## Four frozen denominators per dialect

These source-only counts are expectations, not measured parser success.
`labels/*.json` and `LABEL_FORMAT.md` freeze exact identifier-token positions,
full statement spans/hashes, explicit names, ordered key/FK members and access
intent. Literal helpers serialize authored choices; they do not discover SQL
syntax. `audit_source_labels.py` checks all hashes/ranges, nonoverlap and that
subtracting statement envelopes leaves only comments/whitespace.

| Dialect and population | 1. Declaration objects | 2. Migration statements | 3. Authored query table sites | 4. Binding opportunities |
|---|---:|---:|---:|---:|
| PostgreSQL public manifest/sample | 1,843 across all five schemas | 43 across six files | 98 across 44 queries/six files | 98 |
| PostgreSQL generated dump | 8: 1 table +6 columns +1 PK from exact supplementary input | Not migration history | No query binding manifest | 0 |
| MySQL-compatible public dump/query | 488 | 0, unmeasured: no corpus migration files | 48 across all 36 queries/one file | 48 |

Report each denominator separately by dialect, source and syntax class. For
declarations, report exact objects recognized and unresolved objects by kind;
never remove unsupported objects from the denominator. For migration statements,
the authored disposition is 33 history events, two procedural-function gaps,
two unsupported DROP FUNCTION statements and six unsupported partition-creation
SELECT calls, totaling 43. Retain syntax-class counts and both branches, never
claim current shape. For query sites, every explicitly authored physical
relation-literal occurrence counts; aliases, CTE names, table functions, column
qualifiers and generated Go do not inflate the count. For joins, all 98/48
public opportunities have exactly one manifest-selected matching declaration;
report uniquely bound/name-only/missing/ambiguous separately. The six health
query sites bind only to visibly marked sqlc tooling stubs. Additional authored
missing/ambiguous/name-only/negative controls are a separate population.
There is no cross-dialect blended percentage.

`controls.json` freezes 60 separately authored neutral controls (30 per dialect)
and their exact fixture hashes/token labels before candidate execution. They
cover source classification, exact/non-ASCII/case names, ordered distinct FKs,
CTEs and common read/write semantics, PG DELETE USING/conflict update/restrict
wrappers, MySQL multi-delete/conflict update/versioned data/prefix indexes and
generated columns, all frozen gap classes, missing/ambiguous/name-only binding,
malformed UTF-8/quotes and the input byte ceiling. The missing-position case is
an explicit projection-fault control, not an observed library defect. These
controls are never blended into the public declaration/query denominators;
the parser comparison must retain each unsupported outcome honestly.

## Requested PostgreSQL dump

`fixtures/requested-pg-input.sql` contains the complete exact six-column
Dispatcher CREATE TABLE statement from pinned `v0.sql`, with attribution.
`pg-generator-input.json` freezes the original full-source hash, exact source
span/hash and supplementary fixture span/hash. The five full public schemas
above remain unchanged. This small generated fixture tests mechanical capture
and reader admission, not vendor/schema coverage.

`pg-generator-plan.json` freezes the exact loader/dump argv templates before
execution. The installed pg_dump 18.6 client alone is insufficient: the lead
must admit exact PostgreSQL 18.6 server/initdb/client/pg_ctl identities, resolve
literal private paths, and retain executed argv/binary hashes. One bounded
ephemeral server loads only this public fixture, listens only on a private Unix
socket, dumps twice with a fixed restrict key, requires identical bytes, then
stops and joins. The retained receipt must prove teardown and input/output
digests. No credentials or data directory survives. No production reader
connects to any database. The authorized first actual generation passed:
1,311 output bytes, SHA256
`712e9380d8b580971cc891b5cfbf3f2b0817b84782e3d67df3118a9877e0d2b2`,
two identical dumps and 0.589942 seconds of container execution. Complete hand
labels cover 16 statement envelopes: 14 wrappers and table/PK statements with
all eight declarations. `pg-generation-receipt-accepted.json` proves the exact
image, six PG 18.6 binary versions/hashes, literal argv, zero exit and joined
PostgreSQL/Docker children, actual docker wait 0/PID 0, volume removal, container
absence, restored context and stopped unchanged VM. Parser work stays held
until the independent input checkpoint.

The prospective container adaptation is `pg-container-generator-plan.json` and
its literal source/argv runner. The official Linux arm64 manifest and config
are retained under `registry/`. One initial image-admission check stopped before
any container/initdb/SQL execution because it incorrectly expected Docker 29's
image Id to equal the config digest. The original receipt/source/plan are
retained unchanged. A subsequent read-only cached-image diagnosis proved that
Id is the manifest digest; Descriptor, RepoDigests, exact referenced config
hash, all 13 rootfs diff IDs and runtime config agree. The lead accepted that
correction before the first actual generation; no second image pull or database
retry occurred. This correction changes no SQL fixture, label or parser gate.
The initial admission receipt and original source/plan remain exact.

Mechanical capture cost is separate from reader cost: one 5.420581-second image
pull of 161,098,692 compressed layer bytes, then cached-image execution under
the actual 256 MiB memory/no-extra-swap, 1 CPU and 64 PID container limits. Work tmpfs
was 128 MiB plus 16 MiB for the image's unused data-volume target; no persistent
data volume remains. The serving postmaster used 16 MiB shared buffers/four
connections; initdb separately reported its bootstrap defaults of 128 MiB/100.
No peak-RSS or unpacked-disk measurement is invented. Retained VM startup,
readmission and teardown records disclose preparation overhead separately.

## Frozen decision table

| Question | Decision or explicit deferral |
|---|---|
| Source class | Authored manifest schema and captured schema-only dump stay distinct. Both cite committed bytes only. Authored query and migration history stay separate. |
| Dialect admission | Exact committed sqlc `engine` or recognized PostgreSQL/MySQL/MariaDB dump header. Contradictory metadata refuses. Headerless unbound schema refuses; grammar sniffing forbidden. Explicit spike migration-root admission references the committed PostgreSQL engine metadata. |
| Dump data | Any data-bearing COPY, INSERT, REPLACE or LOAD DATA in a dump, including executable versioned comments, atomically refuses the whole dump. Quoted strings/comments containing those words do not themselves carry data. SQL query INSERT is allowed only in the authored-query plane. |
| Dump wrappers | PostgreSQL SET, restrict/unrestrict and pg_catalog.set_config metadata; MySQL SET/DROP/versioned comments, engine/charset/collation and temporary-view envelope are explicit wrappers, never executed or silently counted as history. Unknown wrapper/procedural syntax receives a gap. |
| Missing artifact | Return `schema-artifact-missing` with request-schema-only-dump checklist state; no schema inferred from application/generated code. Human request/capture metadata is not evidence. |
| PG identifier | Preserve exact source spelling and quotedness; unquoted identifiers use PostgreSQL ASCII folding only for dialect identity, quoted names remain exact. No global Unicode/language folding. |
| MySQL identifier | Preserve exact spelling and backticks; no assumed lower_case_table_names, no case-variant join. |
| Lineage | `sql_schema_set_v1(repo, manifest_path-or-migration-root, dialect)`. Repo/source commit/digests fence provenance separately from stable lineage. No cross-dialect identity. |
| Binding | Manifest selects schema/query inputs; normalized paths must stay repo-relative. Unbound literal remains name-only; missing declaration remains explicit; multiple candidate roots refuse ambiguous-schema-set, never repo-wide guess. |
| Declaration/history predicates | Only current-schema inputs emit DECLARES_* objects. Migrations emit occurrence-distinct MIGRATION_* events with source offset/predicate/direction; no Up/Down execution or current-shape fold. Dump DROP/CREATE scaffold remains current capture context. |
| FK identity | Ordered source columns, target relation, ordered target columns and exact named/anonymous occurrence; preserve two FKs between the same pair, composite order, self-FKs and actions as citable syntax. No runtime referential-integrity claim. |
| SELECT and CTE | Physical source literals read. Lexical CTE/derived aliases and table functions are not physical tables. CTE bodies recurse only within explicit token/depth bounds; never interpret dynamic SQL. |
| INSERT SELECT | Target write and physical SELECT sources read; do not invent a second source from RETURNING, EXCLUDED, VALUES or aliases. |
| UPDATE | Target read-write, physical FROM/JOIN/subquery sources read; source text does not prove runtime touched rows. |
| PostgreSQL DELETE USING | Delete target read-write, physical USING/subquery sources read. |
| MySQL multi-delete | Explicit target aliases resolve only within the same statement's declared physical relation aliases; targets read-write, remaining sources read. Unresolved/dynamic targets refuse or gap. |
| Conflict update | PG ON CONFLICT DO UPDATE and MySQL ON DUPLICATE KEY UPDATE target read-write once; plain INSERT/DO NOTHING target write. No second physical target from VALUES/EXCLUDED. |
| Generated source | Generated .sql.go is a duplicate-negative source class, zero primary table sites. Exact manifest/generated-from evidence is a possible later join, not implemented here. |
| SDK one-blob reads | Bounded two-pass manifest/schema-set assembly: pass1 retains only bounded exact declaration identities and span provenance; pass2 streams one query/migration blob at a time. Citations always point to one exact source blob; never fabricate a folded multi-blob current declaration. No DB/sqlc execution. |
| Procedural SQL | Function/trigger/DO bodies, dynamic EXECUTE and runtime-created partitions are explicit procedural/dynamic gaps. Do not scan their interior as current declarations or primary authored-query usage. |
| LIKE and tooling models | Cite explicit model table/view and source role. Cross-blob copied column/key/index expansion deferred; no live/catalog claims. |
| Complex declarations | Prefix indexes, generated columns, partition clauses, expression indexes, operator classes, range and exclusion declarations must be parsed with exact citations or remain explicit gaps; no silent loss from denominator. |
| Per-dialect unresolved vocabulary | Both lanes freeze `unsupported-statement`, `dynamic-identifier`, `ambiguous-schema-set`, `unknown-declaration`, `procedural-sql`, `generated-duplicate`, `parser-gap`. Boundary refusals additionally include metadata-conflict, dialect-unavailable, schema-artifact-missing, data-section, malformed-input and explicit resource-limit. |
| Scope | Experimental-dark spike; no Redis/document-store/ORM reader, ER UI, column-reference product, Workbench SQL plane or pack registration. Production continuation requires a new owning ticket and its actual gate. |

## Candidates and identical bounded comparison gates

Candidate licenses were checked before README/source inspection. Retained
notices permit the investigation: pg_query_go/v6 v6.2.5 commit
`7ac09d4bd830e591a69c6ebc756bb8f71de0281e` (BSD3 and PostgreSQL license),
and standalone xwb1989/sqlparser commit
`120387863bf27d04bc07db8015110a6e96d0146c` (Apache2). No compatible imported
SQL parser exists in the production go.mod; dependencies will stay in isolated
spike modules. The PG candidate uses CGO/server C parser code; its published
example identifies PG17 grammar, so PG 18.6 dump-wrapper compatibility is a
measurement, never assumed vendor-version validation. The old MySQL candidate's
source-position and modern-dump support remain unknown. No deparser is used.

After the immutable checkpoint, compare one imported candidate and one bounded
subset grammar **for each dialect**. Freeze common admission and projection
before outcomes; score all four against the same source labels, not each
other. Unsupported syntax produces visible gaps. Imported AST success alone
is not a declaration, exact-position or binding success.

Per candidate, process one immutable blob at a time: input maximum 262,144 bytes,
statement maximum 4,096, token maximum 65,536, nesting maximum 64, source name
maximum 1,024 UTF-8 bytes, output maximum 8,192 facts/sites/events and 4 MiB encoded
bytes. Two-pass set assembly accepts at most 64 schema blobs / 8 MiB total and
16,384 retained declaration identities; ambiguous roots never trigger another
corpus scan. Refuse overflows before publication; malformed/admission/data
failure returns zero facts for the blob. A well-formed unsupported statement
returns an explicit gap and no facts for that statement while unrelated admitted
statements remain visible. No partial data-bearing dump is accepted.

Imported parser internals cannot promise those structural allocation limits;
invoke each parser lane in a joined child with a fixed 30-second whole-corpus
wall cap and 1 GiB observed RSS stop, disclose monitoring granularity/overshoot
and any unmeasured internal bound. No goroutine timeout masquerades as child
termination. Bounds/admission/output gates apply equally to both candidates.
All296 retained corpus files enter the source-class/panic-safety traversal
(generated Go/manifests refuse their SQL primary role), all public SQL bytes
remain unchanged, and every scored output must round-trip its exact token bytes.
Run each candidate twice from fresh state; sorted canonical JSON must match
byte-for-byte, including gaps, refusal classes, object/site spans and bindings.
No thresholds or labels may change to fit results.

Record compile wall, binary bytes, dependency/license notices, execution wall,
peak observed RSS and output bytes separately per dialect/candidate. Bound a
first cold PG/CGO build at 240 seconds; investigate failure rather than retrying
or hiding that dependency cost. No full repository/native/rehearsal matrix.
Report steady cost honestly: no production request/tick/startup/retry/cache/
publication changes; spike pass1 reads each admitted schema once, pass2 each
query/migration once, with one parser child per lane, bounded retained identity
map and no persistent cache, locks, workers or database connections. The later
mechanical dump generator is a separately recorded temporary child workload.
