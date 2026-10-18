# T26.1 measured SQL comparison outcome — 2026-10-18

The finite spike is measured; **no production parser, dependency or pack is
adopted**. The imported-front-end pipelines share the bounded source projector
with the subset pipelines. Their agreement cannot establish independent
third-party extraction or native identifier-position support. No database
connection, query, migration or sqlc command ran during reader comparison.

The immutable input checkpoint is
`1f8970fe58f82bc648bf0e9fe1b7dd5b4443b76c`; all 419 frozen files / 4,439,033
bytes and `history.txt` retain their hashes. The common pipeline contract was
checkpointed separately at `004e3224dcd499524fe52692cd14c446144b3c5d` before
first parser outcomes. Original SQL/labels/sampling/controls were never tuned.
The exact first execution source is retained in `cold-v1/executed-source/`, with
all 14 file hashes verified against `run-receipt.json`. That receipt explicitly
binds uncommitted evaluator source separately from the upstream repository
commits and checkpoint Git lineage; it does not invent an evaluator commit.

## Four separate denominators per dialect

| Lane | Current public declaration objects | Selected migration statements | Authored query table sites | Uniquely bound opportunities |
|---|---:|---:|---:|---:|
| postgresql-subset | 1,584 / 1,843 | 24 exact / 43; 19 gaps | 98 / 98 | 98 / 98 |
| postgresql-imported | 1,584 / 1,843 | 24 exact / 43; 19 gaps | 98 / 98 | 98 / 98 |
| mysql-subset | 488 / 488 | 0; unmeasured (no corpus) | 48 / 48 | 48 / 48 |
| mysql-imported | 477 / 488 | 0; unmeasured (no corpus) | 48 / 48 | 48 / 48 |

The separately generated PostgreSQL18.6 fixture has **1/8 exact declaration
witnesses in both PostgreSQL lanes**: its table qualifies; six columns and one
PK omit the explicit parent schema witness. It has zero query/binding
opportunities and is not migration history. The earlier score's 0/8 is retained;
review corrected its false miss when the known Go `schema_quoted:false` bool was
omitted by JSON `omitempty`. An explicit true value still fails the matcher.

The 43 selected PostgreSQL migration statements contain 33 expected history
events and ten prelabelled unsupported/procedural statements. Both pipelines
emit 33 events, but only 24 satisfy every labelled predicate, relation, ordered
member, direction and exact statement/token witness. Nine CREATE INDEX events
lack required relation/member metadata and stay unqualified. The remaining ten
statements stay gaps. Both Goose directions remain independent occurrences;
there is no current-shape fold. MySQL's zero real migration denominator is
unmeasured, never a vacuous accuracy pass.

All 98 PostgreSQL and 48 MySQL labelled physical usage occurrences match exact
name/token/access witnesses with zero unmatched query emissions, and all bind
uniquely in the one committed manifest set. Six PostgreSQL sites bind only to
explicit sqlc tooling stubs. Public binding outcomes are name-only0, missing0,
ambiguous0 and unrecognized-site0. Separate neutral controls exercise those
outcomes; no runtime ownership, rows-touched, deployment or schema-validity
inference follows.

The five PostgreSQL schemas and the complete MySQL dump are fully labelled.
Only six of 214 PostgreSQL migration files and six of 36 query files are in the
preregistered syntax-quality sample. The other 208 migrations and 30 query files
participate in source-class/panic/span/bounds traversal only; no quality
inference extends to them. Each pass receives all 296 retained corpus files,
one generated dump and 60 stripped-control requests. Foreign-dialect,
generated-Go and manifest roles never become primary SQL facts.

## Strict declaration witnesses by kind

The table reports complete frozen object populations. Unsupported objects stay
in the denominator; identity/type-expression spelling alone is insufficient
when a label also requires ordered keys, explicit schema or source role.
Opaque type strings are not validated against a database and are not part of
these identity/key witness matches.

| Dialect / kind | Subset exact / expected | Imported exact / expected |
|---|---:|---:|
| postgresql / check | 0 / 10 | 0 / 10 |
| postgresql / column | 1091 / 1160 | 1091 / 1160 |
| postgresql / enum | 55 / 55 | 55 / 55 |
| postgresql / exclusion | 0 / 2 | 0 / 2 |
| postgresql / foreign_key | 76 / 76 | 76 / 76 |
| postgresql / index | 39 / 200 | 39 / 200 |
| postgresql / primary_key | 111 / 119 | 111 / 119 |
| postgresql / range_type | 1 / 1 | 1 / 1 |
| postgresql / sequence | 1 / 1 | 1 / 1 |
| postgresql / table | 124 / 133 | 124 / 133 |
| postgresql / unique | 2 / 2 | 2 / 2 |
| postgresql / view | 6 / 6 | 6 / 6 |
| postgresql / view_column | 78 / 78 | 78 / 78 |
| mysql / column | 275 / 275 | 275 / 275 |
| mysql / foreign_key | 47 / 47 | 47 / 47 |
| mysql / index | 52 / 52 | 52 / 52 |
| mysql / primary_key | 36 / 36 | 36 / 36 |
| mysql / table | 36 / 36 | 36 / 36 |
| mysql / unique | 31 / 31 | 31 / 31 |
| mysql / view | 1 / 1 | 0 / 1 |
| mysql / view_column | 10 / 10 | 0 / 10 |

PostgreSQL's 259 unresolved public witnesses include unsupported complex table
or expression-index projection, ten CHECK and two EXCLUDE witnesses lacking
ordered members, 159 index source-role mismatches, and the additional LIKE
tooling-model source-role mismatch. The first raw outputs retain those partial
facts: **188 unmatched strict witnesses per PostgreSQL lane**, comprising 179
declaration witnesses (including seven generated-dump witnesses) and nine
migration witnesses. These are not hidden or treated as additional true
objects. Their exact names/spans often match while required metadata does not.
Both MySQL lanes have zero unmatched strict witnesses; the imported lane omits
the final view and ten view outputs because its syntax parser refuses that
statement. Temporary constant-view scaffolding contributes no current objects.

## Migration recognition by syntax class and direction

| Syntax class / direction | Expected statements | Exact event statements | Gaps |
|---|---:|---:|---:|
| alter-add-column:up | 2 | 2 | 0 |
| alter-add-fk:up | 1 | 1 | 0 |
| alter-drop-column:down | 1 | 1 | 0 |
| create-enum:down | 1 | 1 | 0 |
| create-enum:up | 4 | 4 | 0 |
| create-function:down | 2 | 0 | 2 |
| create-index-concurrently:up | 1 | 0 | 1 |
| create-index:down | 2 | 0 | 2 |
| create-index:up | 6 | 0 | 6 |
| create-table:down | 2 | 2 | 0 |
| create-table:up | 5 | 5 | 0 |
| drop-function:up | 2 | 0 | 2 |
| drop-index-concurrently:down | 1 | 1 | 0 |
| drop-table:down | 2 | 2 | 0 |
| drop-table:up | 2 | 2 | 0 |
| drop-type:down | 2 | 2 | 0 |
| drop-type:up | 1 | 1 | 0 |
| select-function-call:down | 2 | 0 | 2 |
| select-function-call:up | 4 | 0 | 4 |

Imported and subset values are identical for this finite migration sample.
`scores-review-v2.json` requires the actual candidate event's Goose direction
and its enclosing statement to fit the labelled statement envelope. The first
scorer omitted those checks; its unqualified migration counts remain in
`scores.json`. Offline review rescoring consumed the original eight output
files and changed no parser bytes or thresholds.

## Frontend versus projector gaps

These counters cover the complete relevant-dialect traversal, including neutral
controls and the unlabelled safety population. They are separate from the
quality denominators above. Actual imported syntax errors and refused root
categories are distinguished from common source-projection gaps.

| Lane | Imported syntax refusals | Imported root-category refusals | Source-projection gaps after frontend | Binding gaps |
|---|---:|---:|---:|---:|
| mysql-imported | 4 | 0 | 1 | 1 |
| mysql-subset | 0 | 0 | 3 | 1 |
| postgresql-imported | 2 | 141 | 161 | 36 |
| postgresql-subset | 0 | 0 | 303 | 36 |

Subset controls pass30/30 for each dialect; imported PostgreSQL28/30 and
MySQL27/30. Both imported pipelines return parser-gap for the dynamic-identifier
and unsupported-GRANT controls rather than the intended narrower gap class;
legacy MySQL additionally refuses the CTE query. Those remain measured failures.
The old MySQL parser may accept only a DDL prefix: `*sqlparser.DDL` proves its
category admission, not full native AST fidelity. Common projection supplies
all byte positions and bounds, with no deparsing. The actual pinned PostgreSQL
candidate sources define PG_VERSION_NUM170007 (PG17.7); parsing the requested
PG18.6 dump is not PG18.6 grammar/vendor validation.

## Determinism, build and process cost

All four fresh-state pairs are byte-identical, with zero panics and zero
out-of-blob/statement or hash-invalid citation spans. Original outputs, stderr,
argv, binary hashes, source inventories and joined receipts remain exact.
Fresh GOCACHE measurements isolate the Go build cache only: module downloads
and OS caches were already warm, and no machine-cold claim is made. The initial
host-cache builds are separately retained under `first/` and never relabelled
cold. The first PG/CGO build had the frozen240-second cap and passed once.

| Lane | Fresh-GOCACHE build s | Binary bytes | Run1 / run2 s | Exact direct-child wait4 peak bytes | Per-pass output bytes |
|---|---:|---:|---:|---:|---:|
| mysql-imported | 2.086080 | 4,007,634 | 0.354470 / 0.024493 | 11,681,792 | 356,260 |
| mysql-subset | 1.875245 | 3,513,298 | 0.228413 / 0.027520 | 9,551,872 | 355,542 |
| postgresql-imported | 6.485057 | 14,463,042 | 0.364163 / 0.100848 | 29,655,040 | 1,489,448 |
| postgresql-subset | 1.908333 | 3,513,298 | 0.307238 / 0.075705 | 18,219,008 | 1,413,931 |

Imported binary increments are10,949,744 bytes for PostgreSQL and494,336 for
MySQL over their subset binaries. Exact Go/module build information and
per-module dependency/license notices are retained. The parser executables
create no children; their direct PID RSS is therefore relevant to parser cost.
RSS sampling uses Darwin native `proc_pid_rusage` every20ms with scheduling and
measurement overshoot; wait4 captures a different, often higher exact-child
peak. The1GiB observed stop is **not a hard heap/allocation guarantee**. Build
RSS fields cover only the Go driver, excluding concurrent compiler descendants;
no aggregate compiler-family memory number is claimed. Post-build tool hashes
are explicitly later observations, not fabricated pre-execution attestation.

Each cold lane retains a separate47,600,037–96,877,018-byte regular-file Go
cache plus its binary until lead cleanup. Module downloads and all build/cache
work are isolated to spike modules/temporary roots; production go.mod/go.sum are
unchanged. Go1.26.5 darwin/arm64, trimpath and buildvcs=false were observed.

There is no production per-request, query, sync tick, startup, retry/no-op,
publication, lock, cache or child cost. Within a spike pass, schema inputs are
read once before streamed query/migration reads; the catalog retains bounded
exact table/view identities, not raw SQL or AST. **Main also retains the full
metadata []BlobResult until final encoding.** The per-blob4MiB output bound is
checked after allocation/encoding; it is not a global result-heap bound. At the
1024-request admission ceiling, nominal encoded metadata can approach4GiB
before Go object overhead, so the observed process stop remains necessary and
can overshoot. This small corpus's measured output is at most1,489,448 bytes.
No scale or hard-memory proof is inferred. The64MiB outer output and64KiB stderr
stops are also sampled checks. Compilation and result files are finite
engineering evidence, not a new persistent production cache.

## Preserved stops and independent review corrections

Before any corpus parser child, strict metadata preparation stopped twice:
first on sqlc's valid first-field `- schema:` form, then on a whitespace regex
that consumed the following list line. Their receipts remain; closed inventory
membership prevented guessed paths from reaching a parser. One post-run scorer
stop on JSONnull empty collections was corrected offline. No SQL execution,
failed-build retry or parser corpus rerun followed those corrections.

Independent review found two trust bugs in the measured reader: asymmetric
mixed-header admission, and catalog mutation before the complete identity delta
passed its budget. Corrected source now rejects either header order and stages
all catalog/counter changes before admission. Separate supplemental controls
prove that a refused blob cannot leave identities for a later query. Normal,
race, vet, pinned lint and four evaluator/metadata tests pass in
`review-corrections/checks.json`; checks bind corrected source and unchanged
419 inputs before/after. Both nested imported wrappers additionally pass their
own vet and pinned lint checks in `review-corrections/nested-checks.json`.
A small ad-hoc control-orchestration path error stopped
before any check child and is retained separately. The original full-corpus
measurement source remains exact in `executed-source/`; **the corrected trust
source has not been presented as a fresh full-corpus measurement**.

This closes the isolated measurement work with explicit abstentions. Production
continuation remains a separate owning decision/ticket with exact schema-role,
qualifier, CHECK/EXCLUDE/index metadata and parser-boundary work. The owning PLAN
record and any ticket status are lead-owned; no released claim, pack
registration, independent accuracy or production database validation follows.
