# Source-free large-monorepo diagnostic observation — 2026-08-06

> **Retained diagnostic record.** This report reduces one private runtime log
> to source-free operational evidence. It was not prospectively frozen, is not
> a validation receipt, and grants no supported-scale, SLO, topology, accuracy,
> completeness, migration, decommission, pilot, or release claim.

## Authority and sanitization boundary

The source was one private `phebs 0.2.1-dev` runtime log containing 420
physical lines and 148,458 bytes. The observed window was 2026-08-06
11:30:20–12:12:52 local time, or 42 minutes 32 seconds. One malformed leading
timestamp spelled the year as `026`; this report normalizes it to 2026 because
every following timestamp establishes the intended year.

The private log remains outside this repository. This report retains exact
times, durations, counts, byte totals, admitted limits, public component and
extractor versions, closed dispositions, and source-free state transitions.
It replaces the two observed source revisions and their derived generations
with the report-local names **revision A** and **revision B**.

The following values are deliberately not retained:

- repository, connection, organization, host, user, runner, and target names;
- filesystem paths, filenames, listener endpoint, and data-directory identity;
- Git object IDs and policy, source, candidate, manifest, extraction, and
  publication digests;
- job IDs, response-channel tokens, build-directory identifiers, and raw
  retry-row identities;
- raw errors containing a path, filename, run identity, or other private
  value;
- the exact 200-row per-shard byte/file vector, which is a high-entropy corpus
  fingerprint even after shard names are removed; and
- an unsalted digest of the private log.

Shard count, total, minimum, median, p95, maximum, mean, overhead range, and
load/unload batch aggregates are retained instead. The report was checked for
private names, paths, raw object IDs, and source-derived digests before commit.

## Run posture and nonclaims

- Watch mode polled one local repository.
- No analysis unit was configured; primary and supporting path counts were
  both zero.
- Search used the whole-repository posture.
- The typed-index posture was repository-root-unbound; a typed input was
  configured but absent from both complete candidate generations.
- Startup emitted the existing `configure_analysis_unit_for_service_scope`
  recommendation. That recommendation is recorded as diagnostics, not adopted
  here as a sufficient scale solution.
- Ten experimental domains were enabled:
  `grpc-caller`, `grpc-consumer`, `kafka-consumer`, `kafka-producer`,
  `proto-contract`, `scip-proto-field`, `scip-thrift-field`, `thrift-caller`,
  `thrift-consumer`, and `thrift-contract`.
- Provisional protobuf, Thrift, Thrift-field, Kafka, and Change Workbench
  warnings were active. Their existing nonclaims remained in force.
- The log contains no RSS, heap, CPU, filesystem allocation, remaining disk
  capacity, IOPS, query latency, query equality, backup, restore, retention,
  teardown, or SLO observation.
- No caller-leaf job appears. This run therefore neither exercises nor
  validates T39.R1's caller-leaf boundary change.
- The process is not shown stopping; the log ends after extraction attempt
  exhaustion.

At 11:30:22 OpenAPI/schema linking emitted a duplicate `Schema` field warning
for `observationpublication.Progress`. This was nonfatal but is a distinct
schema-generation defect, not a scale result.

The raw stream is not safely line-oriented: at least one physical line joined
two shard events, one generated shard identifier wrapped across a newline, and
the final joined error occupied multiple physical lines. Any future reducer
must parse event framing rather than assume one record per input line.

## Job-lifecycle ledger

Private job IDs are replaced by logical aliases. Requeue eligibility is shown
as the actual backoff rather than the private queue-row identity.

| Logical job | Attempt | Claimed | Terminal | Queue wait | Handle | Result | Backoff |
|---|---:|---|---|---:|---:|---|---:|
| Connection sync A | 1 | 11:30:24 | 11:30:25 | 3,998 ms | 416 ms | success | — |
| Indexing | 1 | 11:30:31 | 11:38:20 | 6,428 ms | 468,817 ms | retryable | 60 s |
| Connection sync B | 1 | 11:31:24 | 11:38:41 | 747 ms | 436,403 ms | success | — |
| Candidate A | 1 | 11:37:41 | 11:46:19 | 1,984 ms | 518,358 ms | success | — |
| Indexing | 2 | 11:39:26 | 11:54:03 | 6,927 ms | 876,949 ms | retryable | 120 s |
| Extraction | 1 | 11:46:20 | 11:54:03 | 824 ms | 463,567 ms | retryable | 240 s |
| Resolver | 1 | 11:46:23 | 11:54:03 | 3,504 ms | 460,874 ms | retryable | 240 s |
| Candidate B | 1 | 11:53:23 | 12:01:49 | 350 ms | 506,248 ms | success | — |
| Indexing | 3 | 11:56:06 | 12:02:29 | 3,492 ms | 383,115 ms | attempts exhausted | — |
| Extraction | 2 | 11:58:04 | 11:58:04 | 1,162 ms | 1 ms | retryable | 480 s |
| Resolver | 2 | 11:58:08 | 12:02:29 | 5,678 ms | 260,939 ms | success | — |
| Candidate B warm no-op | 1 | 12:01:52 | 12:02:29 | 2,845 ms | 37,352 ms | success | — |
| Resolver no-op | 1 | 12:02:29 | 12:02:29 | 40,290 ms | 0 ms | success | — |
| Extraction | 3 | 12:06:05 | 12:12:52 | 1,083 ms | 407,229 ms | attempts exhausted | — |

The exact next-eligible instants retained in the source log were:

| Job attempt | Next eligible, UTC |
|---|---|
| Indexing 1 | 2026-08-06T18:39:20.598263Z |
| Indexing 2 | 2026-08-06T18:56:03.891422Z |
| Resolver 1 | 2026-08-06T18:58:03.893020Z |
| Extraction 1 | 2026-08-06T18:58:03.901164Z |
| Extraction 2 | 2026-08-06T19:06:04.188594Z |

## Source and search generations

Both builds explicitly reported that cat-file batching was disabled through
the environment. Each build covered one exact revision, used `force=false`,
and the zoekt child read every file through its go-git path.

| Metric | Revision A | Revision B | Delta |
|---|---:|---:|---:|
| Census owners | 1,611,125 | 1,613,537 | +2,412 |
| Census placements | 1,611,125 | 1,613,537 | +2,412 |
| Census members | 394 | 394 | 0 |
| Census encoded-member bytes | 450,418,426 | 451,106,819 | +688,393 |
| Files offered to zoekt | 1,611,125 | 1,613,537 | +2,412 |
| Files read through cat-file | 0 | 0 | 0 |
| Files read through go-git | 1,611,125 | 1,613,537 | +2,412 |
| Shards | 100 | 100 | 0 |
| Child duration | 6m29.419s | 6m20.440s | −8.979s |
| Reported generation bytes | 28,724,410,446 | 28,775,361,432 | +50,950,986 |
| Sum of shard payload bytes | 28,273,665,176 | 28,323,928,149 | +50,262,973 |
| Non-shard generation bytes | 450,745,270 | 451,433,283 | +688,013 |
| Minimum shard bytes | 121,994,423 | 167,774,197 | — |
| Median shard bytes | 284,332,349 | 284,261,180 | — |
| P95 shard bytes | 290,495,679 | 290,368,647 | — |
| Maximum shard bytes | 298,727,551 | 302,106,596 | — |
| Mean shard bytes | 282,736,651.76 | 283,239,281.49 | — |
| Minimum files in one shard | 2,489 | 2,701 | — |
| Median files in one shard | 15,775 | 15,628 | — |
| P95 files in one shard | 23,931 | 24,981 | — |
| Maximum files in one shard | 47,615 | 49,673 | — |
| Mean files per shard | 16,111.25 | 16,135.37 | — |
| Logged overhead range | 2.6–2.8 | 2.6–2.9 | — |
| Mean logged overhead | 2.711 | 2.709 | — |

Revision A's census ran from 11:30:31 through 11:30:47. Its index child was
announced at 11:30:48, reported file access at 11:31:01, completed individual
shards from 11:31:05 through 11:37:17, and reported complete at 11:37:39. A
revision movement was observed at 11:31:24 while this build was active.

The reader loaded revision A's 100 shards at 11:37:38–11:37:39 in 43 logged
batches, with batch sizes from one through three: 72 shards in 29 batches at
11:37:38 and 28 shards in 14 batches at 11:37:39. Revision B's census ran
from 11:46:19 through 11:46:40. Its child was announced at 11:46:40, reported
file access at 11:46:53, completed individual shards from 11:46:57 through
11:53:00, and reported complete at 11:53:22. At 11:53:20 the reader unloaded
the prior 100 shards in 19 batches, sized one through 22. It loaded the new
100 shards at 11:53:22 in 44 batches, sized one through four.

Using reported generation bytes, the two child runs averaged approximately
70.3 MiB/s and 72.1 MiB/s respectively. Using offered file counts, they
averaged approximately 4,137 and 4,241 files/s. These are arithmetic summaries
of this environment, not general throughput claims.

Both generations were committed. A third indexing attempt later found
revision B already current and skipped the child. The two physical shard sets
sum to 56,597,593,325 bytes; adding each generation's reported non-shard bytes
gives 57,499,771,878 bytes of logical old-plus-new generation material. The
log does not report filesystem allocation or remaining capacity, so this is a
replacement-volume observation rather than a disk-headroom claim.

The +2,412 owner difference is only an aggregate count delta. It is not a
proved changed-file count and establishes no record- or content-equality
percentage. One observed revision movement is also insufficient to establish
a commit frequency.

## Post-index coupling

- Revision A was committed at 11:37:39, but indexing attempt 1 requeued at
  11:38:20.
- Revision B was committed at 11:53:22, but indexing attempt 2 requeued at
  11:54:03.
- Indexing attempt 3 found revision B already current at 12:01:49, queued a
  candidate operation that became a warm no-op, and then exhausted its
  attempts at 12:02:29.
- Only attempt 3 exposes the final closed boundary:
  `observation partition unavailable`. The log does not identify which
  observation/source-partition dimension refused the generation.

The timing similarity does not prove that attempts 1 and 2 had the same
undisclosed cause. The code path nevertheless permits a successfully committed
index to be reported as a failed indexing job when synchronous post-index work
fails. This is an operational-semantics observation, not evidence that the
committed search bytes were corrupt.

## Candidate generations

Phase durations are copied as emitted and may overlap; they must not be added
to derive a new total.

| Metric | Candidate A | Candidate B | B warm no-op |
|---|---:|---:|---:|
| Decision | rebuild | rebuild | warm no-op |
| Control revision | 1 | 1 | 1 |
| Queue wait | 1,984 ms | 350 ms | 2,845 ms |
| Mirror-lock wait | 60,011 ms | 40,782 ms | 37,340 ms |
| Tree | 47,150 ms | 56,656 ms | 0 |
| Spooling | 266,724 ms | 268,777 ms | 0 |
| External sort | 289,851 ms | 288,888 ms | 0 |
| Peak spool bytes | 464,929,743 | 465,749,119 | 0 |
| Publication | 55,724 ms | 56,115 ms | 0 |
| Fingerprint | 16 ms | 5 ms | 0 |
| Database commit | 11 ms | 7 ms | 9 ms |
| Marker finish | 5 ms | 4 ms | 0 |
| Total | 518,358 ms | 506,248 ms | 37,352 ms |
| Declared source bytes | 11,000,255,148 | 11,018,513,468 | 0 |
| Typed configured | 1 | 1 | 0 |
| Typed present | 0 | 0 | 0 |
| Manifest summary present | true | true | false |

| Plane | A records | A members | A canonical bytes | A declared bytes | B records | B members | B canonical bytes | B declared bytes |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Repository | 901,428 | 228 | 422,938,942 | 7,521,426,694 | 902,935 | 228 | 423,660,630 | 7,535,811,236 |
| Local | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| Caller | 856,442 | 256 | 371,778,563 | 7,309,773,856 | 857,908 | 256 | 372,425,285 | 7,323,913,469 |

Candidate A to B increased declared source bytes by 18,258,320,
repository records by 1,507, caller records by 1,466, and peak spool by
819,376 bytes; total rebuild time decreased by 12,110 ms. These differences
do not establish which records or contents changed. Repository- and
caller-plane declared bytes overlap and must not be summed as unique source.

The warm no-op spent 37,340 of 37,352 ms—99.97%—waiting for the mirror lock.
It performed no tree walk, spool, sort, fingerprint, or publication work.

## Extraction attempts

### Attempts 1 and 2: no domain work

Attempt 1 preflight found a current pointer, zero settled domains, incomplete
settlement, required strict open, and control revision 1. It then spent
463,555 ms waiting for the mirror, 1 ms on pointer work, 0 ms on strict open,
and 463,558 ms in the reported operation. All ten domains were `not_ready` and
reported zero inventory, source opens, facts, staging, publication, abort, and
cleanup. Mirror wait was 99.999% of operation time, and the no-work turn
consumed an ordinary attempt.

Attempt 2 found the pointer missing. It required no strict open, reported a
0-ms operation and 1-ms lifecycle handle, classified all ten domains
`not_ready` with zero work, and consumed a second attempt.

### Attempt 3: complete strict open and domain execution

Attempt 3 again found a current pointer with zero settled domains, incomplete
settlement, required strict open, and control revision 1. Strict open took
52,732 ms and validated both complete planes. Its queue wait was 1,083 ms,
mirror-lock wait was 0, and pointer work took 4 ms:

| Plane | Records | Members | Canonical bytes | Declared bytes |
|---|---:|---:|---:|---:|
| Repository | 902,935 | 228 | 423,660,630 | 7,535,811,236 |
| Local | 0 | 0 | 0 | 0 |
| Caller | 857,908 | 256 | 372,425,285 | 7,323,913,469 |
| Repository + caller | 1,760,843 | 484 | 796,085,915 | 14,859,724,705 |

The combined declared-byte value is not a unique-source total because the
planes overlap. Typed input was configured but absent. Every scheduled domain
started `never_attempted` with 827,261 ms of aggregate budget remaining. The
execution order was `proto-contract`, `grpc-consumer`, `scip-proto-field`,
`grpc-caller`, `thrift-contract`, `thrift-consumer`, `thrift-caller`,
`scip-thrift-field`, `kafka-producer`, and `kafka-consumer`. Typed input was
explicitly absent for positions 3, 4, 7, and 8.

The operation took 407,228 ms and its lifecycle handle took 407,229 ms. It
ended with attempts exhausted. No domain published.

### Emitted extraction limits

Every domain carried the same closed limits:

| Limit | Value |
|---|---:|
| Corpus files | 200,000 |
| Opened-source attempts | 800,000 |
| Opened-source files | 200,000 |
| Opened-source bytes | 536,870,912 |
| Facts | 12,500 |
| Source blob bytes | 10,485,760 |
| Typed-input bytes | 67,108,864 |
| Aggregate wall | 900,000 ms |
| Mirror-lock wall | 890,000 ms |
| Domain wall | 300,000 ms |
| Abort wall | 5,000 ms |
| Outcome wall | 5,000 ms |
| Maximum serial domains | 16 |
| Scheduler identity bytes | 65,536 |
| Aggregate staged rows | 100,000 |
| Per-domain staged rows | 25,000 |

### Final domain ledger

| Domain | Version | Result | Inventory | Corpus / candidates | Source opened | Extractor / staging | Evidence | Publication |
|---|---|---|---:|---|---|---|---|---:|
| proto-contract | 3.0.0 | retryable `failed` | 4,171 ms | 1,613,157 / 19,049 | 53 attempts/files; 331,909 B; 12,773 ms | 41,661 / 28,822 ms | 4,537 facts; 4,352 atoms/assertions; 0 unresolved; 17 chunks; 8,704 rows | 0 |
| grpc-consumer | 1.2.0 | terminal `limit_refusal` | 1,156 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| scip-proto-field | 1.4.0 | terminal `limit_refusal` | 1,046 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| grpc-caller | 1.5.0 | terminal `limit_refusal` | 924 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| thrift-contract | 1.0.0 | retryable `domain_budget` | 4,197 ms | 1,613,157 / 26,014 | 128 attempts/files; 703,238 B; 30,745 ms | 295,799 / 264,914 ms | 9,728 facts; 9,472 atoms/assertions; 0 unresolved; 37 chunks; 18,944 rows | 0 |
| thrift-consumer | 1.2.0 | terminal `limit_refusal` | 1,192 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| thrift-caller | 1.5.0 | terminal `limit_refusal` | 960 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| scip-thrift-field | 1.4.0 | terminal `limit_refusal` | 1,099 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| kafka-producer | 1.2.0 | terminal `limit_refusal` | 1,113 ms | refused before counters grew | 0 | 0 | 0 | 0 |
| kafka-consumer | 1.2.0 | terminal `limit_refusal` | 1,076 ms | refused before counters grew | 0 | 0 | 0 | 0 |

The protobuf domain planned 91,117,800 declared bytes. Excluded source files,
excluded declared bytes, and excluded SCIP documents, definitions, and
occurrences were all zero. Abort took 6 ms; cleanup took 0. The closed source-
free failure class was: protobuf extension fields require descriptor linking
to establish extendee lineage. The private path and filename are not retained.

The Thrift domain planned 120,928,052 declared bytes. Its excluded counters
were likewise zero. Abort took 5 ms; cleanup took 0. It reached the context
deadline while adding evidence. Every outcome reported no prior outcome for
the exact generation, and every domain reported zero publication time.

The extraction inventory count of 1,613,157 was 380 below the revision B
source-owner count. The log does not classify that difference, so this report
does not assign a cause.

One opaque unavailable-response-channel event occurred at 12:12:51. The
channel token is removed. The log provides no evidence that this event caused
the already-established domain refusals or deadline.

## Thrift-specific audit

### Observed result: staging throughput, not a source-type failure

No problematic Thrift file, syntax, or special type was identified. The file
named by the private deadline error was merely active when the deadline
expired; the log does not make it causal.

The contract domain admitted 26,014 IDL candidates but opened only 128
(0.492%), totaling 703,238 bytes. It produced 9,728 facts. Thirty-seven
complete 256-fact chunks staged 9,472 atoms/assertions and 18,944 rows before
the next store operation encountered the deadline. Staging consumed 264,914
of 295,799 extractor milliseconds, or 89.56%, at approximately 71.5 rows per
second and 7.16 seconds per completed chunk.

No parser/preflight error and no file-level unresolved/gap assertion appeared
before the timeout. This proves that the 128 opened files parsed and passed
file-wide field-ID admission. It does **not** prove that every imported or
nested type reference resolved; those classifications live inside field
details rather than the top-level unresolved counter.

After 0.492% of candidates, the run had already consumed 77.82% of the
12,500-fact cap and 75.78% of the 25,000-row cap. This is an extrapolation
warning, not a prediction: faster staging alone would soon encounter the
current admission bounds. Partitioning is required before this candidate
population can be represented completely.

The store performs one synchronous transaction for each 256-fact chunk and
recomputes run-wide row and reference totals in that transaction. The worker
serially awaits each chunk. This implementation shape is consistent with the
measured staging dominance; it does not prove a database or host-independent
throughput constant.

### Known construct boundaries from phebs code

These are code-derived risks, not findings about the private corpus:

- Supported and regression-tested forms include base scalar types including
  `binary`; required, optional, and default fields; structs, unions,
  exceptions, `throws`, `oneway`; direct maps, lists, and sets; and same-file
  typedefs.
- An implicit, nonpositive, or greater-than-32,767 field ID becomes one
  file-level `THRIFT_EXTRACTION_GAP`, rather than aborting the domain.
- Include-qualified references do not cross-file link. Deeply nested
  containers, missing or duplicate declarations, and typedef chains beyond
  16 steps abstain. Service inheritance is recorded but inherited operations
  are not expanded. Enums and typedefs are resolution-only. Constants,
  annotations, and field default values are not emitted as evidence.
- A read, lexical-preflight, or parser error still aborts the complete staged
  domain. One IDL file is capped at 4 MiB, 500,000 tokens, and nesting depth
  128.
- The Go consumer/caller extractor recognizes Apache compiler headers and
  specific generated `processorMap` and client-`Call` shapes. It does not
  model thriftrw/YARPC consumer/caller conventions. It scans non-generated Go
  files twice and caps each candidate at 4 MiB.
- The SCIP field extractor separately supports digest-bound thriftrw modules
  and Apache struct tags, with limits of 32 MiB per index, 50,000 documents,
  500,000 occurrences, 8 KiB per symbol, and 4 MiB per generated file.

The `thrift-consumer`, `thrift-caller`, and `scip-thrift-field` domains did not
execute in this run: all three refused during inventory against the large Go
plane. This observation therefore says nothing about the private corpus's
generated-code compatibility, caller patterns, or field-reference joins.

Future source-free instrumentation can safely count field-ID gaps,
include-qualified references, nested-container abstentions, typedef-limit
abstentions, parser/preflight refusals, and per-partition staging throughput.
Determining private construct prevalence otherwise requires a separately
authorized analysis; this report did not inspect private source.

The code audit used the production
[Thrift declaration extractor](../../internal/extract/extractors/thriftdecl/thriftdecl.go),
[shared IDL preflight](../../internal/idlpreflight/preflight.go),
[chunk staging path](../../internal/extract/extract.go), and
[evidence transaction](../../internal/store/evidence.go), together with the
retained [T19.1 rules](../t191/README.md) and current
[Thrift pack cards](../../docs/THRIFT_PACK_CARDS.md).

## Evidence-backed conclusions

1. Source census and direct search publication completed twice at about 1.61
   million files, 100 shards, and 28.7–28.8 billion reported generation bytes.
2. One revision movement occurred during the first build, and a second full
   search and candidate generation followed.
3. Both search generations committed, yet indexing exhausted its attempts
   after chained observation work remained unavailable.
4. Candidate rebuilds succeeded twice but each took about 8.4–8.6 minutes; a
   warm no-op took 37.352 seconds almost entirely waiting for the mirror.
5. Extraction consumed two attempts without executing a domain: one after
   463.555 seconds of mirror wait and one after a missing pointer.
6. On the only executing attempt, eight domains refused before growth at the
   current inventory boundary, one protobuf domain aborted on a classified
   unsupported feature, and one Thrift domain exhausted its wall budget while
   staging evidence.
7. Zero extraction domains published. Resolver work eventually succeeded,
   but observation and extraction did not converge.
8. No caller-leaf work occurred, so this run cannot evaluate T39.R1 directly.

## Inferences this record does not support

This record must not be cited to claim:

- that exactly 2,412 files changed or any percentage of records was unchanged;
- a fixed repository commit frequency or completed end-to-end chain duration;
- comfortable search headroom, query performance, or direct-topology support;
- that a particular Thrift file or type caused the deadline;
- that T39.R1 passed or failed;
- that a particular observation/source-partition cap caused the generic
  refusal;
- that raising a timeout, retry count, or admission cap establishes support;
  or
- any scale, SLO, accuracy, completeness, migration, decommission, pilot, or
  release conclusion beyond the exact mechanics recorded above.

This diagnostic may inform a prospectively frozen neutral scale gate and
future ticket acceptance criteria. It cannot substitute for either.
