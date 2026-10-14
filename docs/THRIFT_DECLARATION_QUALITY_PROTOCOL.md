# T49.2 Thrift declaration quality protocol

Frozen before selection (Ben's decisions, 2026-10-13). The commit that adds
this file is the seal: nothing below changes after scoring starts, and a
failed round is recorded as failed rather than rescored. Protobuf results
(T49.1) grant no Thrift support, and this round grants none to protobuf.

## 1. Decisions (Ben, 2026-10-13)

- **Oracle:** the official Apache Thrift compiler, pinned (§3). Its `-gen
  json` output is a complete oracle for every file it compiles. It shares no
  code with thriftrw or `thriftdecl`, so no human label sample is drawn.
- **Adjudicator:** Claude, as named by Ben. Every disagreement is
  adjudicated, and none may be left `unsure`. This is not an independent
  human review.
- **Thresholds** (point estimates, §6): declaration agreement ≥ 99.5%,
  reference-state agreement ≥ 99.5%, and field-ID agreement 100%. Every file
  the oracle rejects is reported with phebs's behavior on it, never silently
  dropped.
- **Population:** fresh, by the frozen search rule in §4.

## 2. Claim card (`thrift-contract`, declaration identity)

**Claim.** For each `.thrift` file the reference compiler accepts, the
`thrift-contract` extractor declares exactly the oracle's declarations, under
the identities frozen by T19.1 (D1, D2, D8) and implemented by
`internal/extract/extractors/thriftdecl` (version 1.0.0, last changed at
`b8081248`):

| Kind | Identity |
|---|---|
| Service | `DECLARES_SERVICE scope.Service` |
| Operation | `DECLARES_OPERATION scope.Service/function` |
| Message (struct, union or exception) | `DECLARES_MESSAGE scope.Name` |
| Synthetic message | `scope.Service.function_args` always; `scope.Service.function_result` unless the function is `oneway` |
| Field | `DECLARES_FIELD message#id`: struct fields, arguments in `_args`, `throws` fields in `_result`, and the success slot `_result#0` when the return type is not `void` |

`scope` (D1) is the last dot-segment of `namespace go`, then of `namespace *`,
and otherwise the file basename.

**Non-claims.**
- No include resolution or cross-file linking.
- Enums, enum values, constants, typedefs as declarations, and annotations
  are out of scope, and never count as misses.
- Implicit field IDs are a deliberate file-level gap (D2), so those files
  form their own reported stratum (§5) and are not scored.
- Nested containers below one level, and a map directly inside a container,
  abstain (D8).
- No fbthrift dialect.

## 3. Oracle

The oracle is `thrift` 0.19.0 from Ubuntu package
`thrift-compiler=0.19.0-2.1build5`, installed on
`ubuntu@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55`.
`spike/t492/oracle/Dockerfile` builds it, and the round records the image
digest.

Each file is compiled once, as `thrift --gen json`. The include search path
is the file's own directory, then the repository root, then every repository
directory containing a `.thrift` file, in sorted order, capped at 64. A
non-zero exit is an **oracle rejection**, recorded with its first error line.

The oracle JSON maps to §2 identities as follows:

- **Scope:** `namespaces.go`, then `namespaces["*"]` (last dot-segment),
  else `name`.
- **Declarations:**
  - `services[].functions[]` give the operations and the synthetic messages
    (`oneway` omits `_result`).
  - `returnTypeId != "void"` gives `#0`.
  - `arguments[].key` and `exceptions[].key` give the synthetic fields.
  - `structs[]` (any `isException` or `isUnion` value) gives the messages,
    and `fields[].key` their fields.
- **Requiredness:** `required` → `required`, `optional` → `optional`,
  `req_out` → `default`.
- **Reference state** of a field, and of each map key and map value:
  - a base type is `intrinsic`;
  - a `struct`, `union` or `exception` class without a `.` is `same_file`
    message `scope.Class`, and an `enum` class likewise is `same_file` enum;
  - a class containing `.` is include-qualified, and `unresolved`;
  - `list` and `set` contribute their element (one level);
  - a container nested inside a container is `unresolved`.

  Apache flattens typedefs to their target, and phebs chases same-file
  typedefs (D8), so both sides compare the resolved target.

## 4. Population (run once, after this seal)

Run `gh search code --extension thrift --limit 100` for each query, in order:

1. `"namespace go"`
2. `"namespace java"`
3. `"exception"`
4. `"union"`

Candidates are repositories in order of first appearance. Excluded:

- the T19.1 corpus: jaegertracing/jaeger-idl, jaegertracing/jaeger-client-go
  and jaegertracing/jaeger;
- apache/thrift, whose test IDLs are the reference compiler's own suite;
- facebook/fbthrift and repositories whose name contains `fbthrift`;
- forks and archived repositories.

Each candidate is pinned at default-branch HEAD (depth-1 clone on the
selection date). A candidate is dropped, with the reason recorded, when:

- its checkout exceeds 200 MiB, or it has more than 2,000 `.thrift` files;
- or the oracle compiles none of its `.thrift` files.

Eligible candidates are admitted in order until both at least 10
repositories and at least 1,000 oracle declarations are held (power floor),
capped at 30 repositories. Reaching the cap below the floor makes the round
underpowered, recorded as not established, never as a pass. The lock is
committed before phebs runs on any candidate.

**Strata:** repository × role × kind. A `vendor` path segment makes a file
vendor; otherwise `test`, `tests` or `testdata` makes it test; everything
else is production.

## 5. Candidate and measurement

The candidate is `thriftdecl` at the lock commit, run directly over every
`.thrift` blob by `spike/t492/export`. It is the same code the served path
runs; T48.5 and T49.3 prove the served read path separately.

Files fall into strata that are counted and reported separately:

- **Scored:** the oracle accepts the file, and phebs emits facts with no
  file-level gap.
- **Phebs D2 gap:** the oracle accepts, and phebs emits an `implicit_field_id`
  gap. A non-claim; reported, not scored.
- **Phebs other gap or parse failure, oracle accepts:** every oracle
  declaration in the file counts as a **miss** in the agreement denominator.
- **Oracle rejects:** reported with phebs's behavior on the file (facts, gap
  or error). Not scored.

The measures, over scored files:

- **Declaration agreement:** |oracle ∩ phebs| / |oracle ∪ phebs| over
  (predicate, object) pairs, per file.
- **Field-ID agreement:** for every oracle field, phebs declares the same
  `message#id` with the same field name. This must be 100%.
- **Reference-state agreement:** over fields both sides declare, the
  fraction whose requiredness and every reference state (field, or map key
  and value) agree.

## 6. Thresholds and adjudication

- **Pass:** declaration agreement ≥ 99.5%, reference-state agreement
  ≥ 99.5% and field-ID agreement 100%, with every disagreement adjudicated
  and none `unsure`.
- **Adjudication verdicts** for each disagreement: `phebs_wrong`,
  `oracle_quirk` (a reference-compiler behavior outside §2's identity, with
  evidence), or `out_of_scope` (a §2 non-claim).
- **Scores are reported twice:** as measured, and after removing only
  `oracle_quirk` and `out_of_scope` items. The pass decision uses the
  post-adjudication figure.
- **Disclosure:** every adjudicated item is listed with its verdict. Any
  `phebs_wrong` item is a defect, reported whether or not the thresholds pass.

## 7. Result (2026-10-13): fails

**Population.** Run as frozen:
- the four searches returned 44 unique repositories; queries 1 and 2
  returned no results as run;
- in order, 10 were eligible and 19 were dropped (5 excluded, 13 over the
  envelope, 1 where the oracle compiled nothing);
- the lock is `spike/t492/corpus.lock.json` (`03cd8c85`), committed before
  phebs ran.

It holds 279 files: 252 scored, 5 phebs D2 gaps, 4 phebs parse errors, 1
other phebs gap, and 17 the oracle rejects. The oracle declares 20,012
declarations. Disclosed: the population is dominated by Impala and Doris IDL
families, and 217 of the 279 files have distinct contents.

| Measure | As measured | After adjudication | Distinct contents | Threshold |
|---|---|---|---|---|
| Declaration agreement | 98.11% | 98.11% | 97.88% | ≥ 99.5% — **fails** |
| Field-ID agreement | 100% (14,091) | 100% | 100% | 100% — passes |
| Reference-state agreement | 93.47% | 98.52% | 98.33% | ≥ 99.5% — **fails** |

**Declarations.** In the 252 scored files, every declaration matched exactly:
zero oracle-only and zero phebs-only rows. All 368 missed declarations come
from 5 files phebs fails on.

**Adjudication** (`spike/t492/adjudication.jsonl`, by Claude as named by Ben;
none `unsure`):

- **`out_of_scope`, 722 fields.** Include-qualified references, such as
  `Types.TPlanNodeId`, which Apache resolves across the include, often to a
  base type. phebs abstains with `IMPORT_LINKING_UNAVAILABLE`, as §2's
  include non-claim says.
- **`phebs_wrong`, 198 fields.** Union members. Thrift unions are implicitly
  optional, and Apache reports them so; phebs reports `default`.
- **`phebs_wrong`, 4 files (cloudwego/thriftgo).** thriftrw rejects what the
  reference compiler accepts:
  - the reserved words `goto`, `alias` and `def` used as identifiers;
  - a namespace annotation, `namespace xsd test (uri = '…')`.

  A single unparseable file fails the whole repository's `thrift-contract`
  run in production, so on the served path this repository would publish
  none of its Thrift declarations.
- **`phebs_wrong`, 1 file.** Negative explicit field IDs (`-1: i32 a`). They
  are valid Apache wire IDs, but phebs gaps the file as `invalid_field_id`,
  which §2 does not declare as a non-claim.
- **Reported, 17 files** the reference compiler rejects (errors and phebs
  behavior are in the adjudication file).

**Defects** (`phebs_wrong`), for a fix before any fresh round:
- union-member cardinality;
- reserved-word identifiers and namespace annotations failing the parse;
- the repository-wide blast radius of one parse failure;
- negative explicit field IDs.

This population has now been seen, so a passing claim needs a fresh round
after those fixes. Field-ID identity, the wire identity Atlas and field
references join on, was 100% exact.

**Correction (2026-10-13, before any fix).** The negative-explicit-ID file was
wrongly adjudicated `phebs_wrong`. Apache 0.19.0 does not honor nonpositive
explicit keys: it warns ("Nonpositive value (-5) not allowed as a field key")
and auto-assigns negative keys, so `-5` becomes `-1` and `0` becomes `-2`.
The written ID is therefore not the wire ID, and phebs's `invalid_field_id`
gap is the D2 fail-closed posture. Its verdict is now `out_of_scope`.
Declaration agreement after adjudication moves from 98.11% to 98.25%
(19,129 / 19,471). The round still fails, and nothing else changes.

## 8. Round 2 (frozen 2026-10-13, after Ben chose targeted fixes)

Ben chose to keep thriftrw (exact positions, pure reader) and fix the
defects round 1 found. The candidate is `thriftdecl` 1.1.0:

- unspecified union members are `optional`;
- a file thriftrw cannot parse is one `THRIFT_EXTRACTION_GAP parse_error` at
  its first non-blank line, instead of failing the repository's run.

Negative and zero explicit IDs stay a fail-closed gap, by the §7
correction. §2's claim, §3's oracle and mapping, and §6's thresholds and
adjudication are unchanged. Two non-claims are added. Like D2, they form
reported strata and are not scored:

- **`parse_error` gaps the oracle accepts.** Each is adjudicated by cause. A
  reserved word used as an identifier, or a namespace annotation, is the
  declared thriftrw dialect limit (`out_of_scope`). Any other cause is
  `phebs_wrong`, and its oracle declarations count as misses.
- **`invalid_field_id` gaps** (nonpositive or out-of-range explicit IDs),
  where Apache auto-assigns keys.

**Population (run once, after this section is committed).** Run
`gh search code --extension thrift --limit 100` for each query, in order:

1. `service`
2. `struct`
3. `namespace`
4. `include`

Candidates are repositories in order of first appearance. §4's exclusions
and envelope still apply, plus:

- the 10 round-1 population repositories;
- any candidate where more than half of its `.thrift` files are
  byte-identical to a round-1 file (`spike/t492/round1-thrift-sha256.txt`).

Eligibility, the floors (at least 10 repositories and at least 1,000 oracle
declarations, capped at 30) and the lock-before-phebs rule are as in §4.
Distinct-content figures are reported beside the per-file ones.

## 9. Round 2 result (2026-10-13): passes

**Population.** The four §8 searches returned 146 unique repositories, and
the first 10 examined were all eligible; none was mostly byte-identical to
round 1. The lock is `spike/t492/round2/corpus.lock.json` (`21ae7ae2`),
committed before phebs ran. It holds 29 files, all with distinct contents:
27 scored and 2 the oracle rejects. The oracle declares 1,353 declarations.

The candidate is `thriftdecl` 1.1.0 at `d215ab98`. It produced no parse or
field-ID gaps on this population.

| Measure | Result | Threshold |
|---|---|---|
| Declaration agreement | **100%** (1,353 / 1,353) | ≥ 99.5% |
| Field-ID agreement | **100%** (718 / 718) | 100% |
| Reference-state agreement | **99.86%** (717 / 718) | ≥ 99.5% |

**Adjudication** (`spike/t492/round2/adjudication.jsonl`, by Claude as named
by Ben):

- **The one disagreement is `phebs_wrong`.** It is a field typed by a
  typedef of a top-level map (`typedef map<string, string> Data`). phebs
  chases the typedef and labels the map nested, so it abstains where the
  reference compiler resolves the key and value. This is an over-abstention,
  not a false claim. The score is unchanged after adjudication.
- **The 2 oracle rejections** are files whose include
  (`ThriftTest.thrift`) is absent from the repository. They are reported and
  not scored; phebs emits facts for both.

**Disclosed.**
- The population is small: 29 files, with two repositories holding 825 of
  the 1,353 declarations.
- No parse-gap or field-ID non-claim stratum was exercised, so the
  `parse_error` path is proven by unit tests and round 1's files, not by
  this round.
- The round passes the thresholds Ben froze. It is not an independent human
  review, and it makes no release decision; that is T49.3/T49.4.

**Open defect:** the typedef-of-map over-abstention, which is small and
still unfixed.

## 10. Round 3 (frozen 2026-10-14, before selection)

The candidate is `thriftdecl` 1.1.1. It fixes round 2's one defect: a field
typed by a same-file typedef of a map now resolves as a top-level map,
following unique typedefs within the D8 chase bound. A map typedef inside a
container still abstains.

Everything else in §8 applies unchanged: claim, oracle, thresholds,
non-claims, the four search queries in order, the floors, and
lock-before-phebs. The exclusions now cover both earlier rounds: the
round-1 and round-2 population repositories, and any candidate where more
than half of its `.thrift` files are byte-identical to a file from either
round (`spike/t492/round1-thrift-sha256.txt`,
`spike/t492/round2-thrift-sha256.txt`). The selector is
`spike/t492/select_r3.py`.

## 11. Round 3 result (2026-10-14): passes the thresholds; one false shape claim found

**Population.** The four §8 searches returned 146 unique repositories, and
21 were examined in order:

- 10 were eligible;
- 10 were round-2 repositories and excluded;
- 1 had no file the oracle compiles.

None of the eligible repositories was mostly byte-identical to an earlier
round. The lock is `spike/t492/round3/corpus.lock.json` (`3b8ed93d`),
committed before phebs ran. It holds 34 files: 31 scored, 2 phebs
`parse_error` gaps the oracle accepts, and 1 the oracle rejects. The oracle
declares 1,673 declarations. The candidate is `thriftdecl` 1.1.1 at
`dead94a2`.

| Measure | As measured | After adjudication | Threshold |
|---|---|---|---|
| Declaration agreement | 73.10% | **100%** (1,223 / 1,223) | ≥ 99.5% |
| Field-ID agreement | 100% (692) | **100%** | 100% |
| Reference-state agreement | 99.86% | **99.86%** (691 / 692) | ≥ 99.5% |

**Adjudication** (`spike/t492/round3/adjudication.jsonl`):

- **Two `parse_error` gaps are `out_of_scope`**, as §8's declared thriftrw
  dialect limit (450 declarations):
  - `end`, a thriftrw reserved word, used as a field name;
  - `string`, a Thrift base-type keyword, used as a field name. Apache
    accepts it and thriftrw's grammar does not. It is disclosed as a type
    keyword rather than a reserved word.
- **One reference-state miss is `phebs_wrong`, a false shape claim.**
  `list<InputIds>`, with `typedef list<Token> InputIds`, is
  `list<list<i64>>`: a nested container, which D8 abstains on. phebs chases
  the element typedef through the inner list and reports the element as
  intrinsic. This is the mirror of the round-2 defect, and it has been
  present since 1.0.0.
- **One file the oracle rejects** is reported; phebs gaps it as
  `parse_error`.

The round passes the frozen thresholds. Unlike round 2's over-abstention,
though, this defect is a false claim, not an abstention: it misstates a
field's shape. It is recorded for a fix before any release decision.
