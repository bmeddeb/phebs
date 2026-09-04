# T42.1 combined gate freeze

T42.1 freezes the source-free plan for the later T42.2 combined execution.
It does not execute the two-million-file gate, change a production limit,
select a topology, or authorize release. Its production replay selected two
narrow executor correctness fixes that are part of this ticket.

## Exact inputs

The plan binds the reviewed T40 structural authority, the neutral-47
mechanics pass, the T41 target profile, and the T41.10 neutral closure. The
canonical input inventory in `plan.go` includes every retained digest plus the
exact T41.10 implementation and integrated-main commits. It deliberately does
not bind the older stopped `spike/t4013/results.json` as a pass.

| Selected authority | Frozen identity |
| --- | --- |
| T40.1 envelope | `sha256:92cce848e6e42942c24e2fa066968571fb5693252b7b41b7a91c889881fe7f94` |
| T40.1 structural profile | `sha256:4227b0a75cc6a2cf1120e5d9e4c228fe23c0dbc2261313f513b6ae809364d430` |
| T40.1 structural oracle | `sha256:8974c843fb9a9bcdb8864367f5e42394d97069058e87481d9d7e8f21e77944df` |
| Neutral-47 package | `sha256:7130d80bd6c4b59ae8d4cfe0fdefd456d6287a6aef35781577b53ce2acb6c2e0` |
| T41.1 envelope | `sha256:99ec8a3dc79537bf1db842234f6fe054abd03c9af7503987f78c5530fdfd525f` |
| T41.1 target profile | `sha256:f54f6c634dea5ce780df1f82591d876ddd229e125444549c1288d7ee4483cf91` |
| T41.10 receipt | `sha256:e751ea4c16284a5f3e69e7b7dde3b2bcaa9274f242d1cf4914bc2757c3b2e680` |
| T41.10 integrated main | `d92b6673db6d4b582c2223536fe52358629ae60e` |

## Combined corpus adapter

The adapter reuses `t401.FrozenProfiles` and the digest-checked
`t411.BuildTargetCorpus`. It walks the 31,600-file T41 overlay, adds one
production-format generated-source snapshot and one committed typed-index
blob, and never creates a
service-count by physical-file-count map or byte tree.

| Dimension | Frozen value |
| --- | ---: |
| T40 physical owners | 2,000,002 |
| Combined regular files | 2,031,604 |
| Combined unique contents (A / B / A-return) | 32,116 / 32,117 / 32,116 |
| Accepted services | 10,000 |
| Memberships | 60,000 |
| Distinct combined paths | 31,602 |
| Distinct service/path claims | 50,000 |
| Duplicate role memberships | 10,000 |
| Maximum accepted path fan-out | 20 |
| Potential Cartesian owner pairs | 20,316,040,000 |
| Materialized Cartesian owner pairs | 0 |

The 31,500 accepted overlay files retain T41's six memberships per service.
One contract path carries both supporting and typed roles. Shared paths group
at most 20 services and generated shared paths group at most ten. The 100 T41
unowned files remain exact; the generated-source snapshot and typed-index blob
are two explicit base-origin unowned files. Three override-origin selectors classify the
2,000,002-file T40 complement without enumerating it, yielding 105 catalog
unowned entries, 31,605 catalog selectors, and 2,000,104 unowned physical
files. Inherited placement claims are measured separately and remain zero.

The adapter replaces each per-service placeholder contract, generated client,
and main file with deterministic source-backed protobuf, gRPC, and Kafka
content. The generated client path becomes `api_grpc.pb.go`, matching the
production generated-source recognizer. The remaining 1,600 invalid Go
placeholders become valid neutral Go. A canonical 10,000-record,
1,940,048-byte generated-source snapshot binds every generated file to its
contract. Production limits remain unchanged: 10,100 resolver declarations
leave 14,900 records of headroom and 10,000 generated mappings leave 15,000.
The two-byte `index.scip` blob is a first-class typed input with identity
`sha256:102b51b9765a56a3e899f7cf0ee38e5251f9c503b357b330a49183eb7b155604`;
it is not synthesized from the extraction result.
The plan separately records structural, overlay, generated-control, combined
logical, per-revision unique-content, catalog logical/encoded, and inherited-
claim bytes; allocated bytes remain explicitly unmeasured.

## Independent oracles

The author and independent logical traversals use different construction
paths and are compared before a plan can be built. They close exact service,
membership, placement, unowned-prefix, and service-query set identities. The
independent path calls neither the corpus author, Git, nor Phebs results.

Relationship source and relationship oracle enumeration are also separate:

| Family | Protocol | Edges | Max in | Max out | Acyclic |
| --- | --- | ---: | ---: | ---: | --- |
| Chain | gRPC | 9,999 | 1 | 1 | yes |
| Layered DAG | gRPC | 200 | 2 | 2 | yes |
| Bounded fan-out | gRPC | 800 | 8 | 8 | no |
| Hotspot groups | Kafka | 9,500 | 1 | 19 | yes |

Every expected edge is a framed canonical semantic record. The two edge
enumerators must produce identical record counts, degrees, framed byte counts,
and SHA-256 identities. Product extraction separately freezes 10,999 RPC
postings, 500 Kafka producers, 9,500 Kafka consumers, 20,999 projections, and
31,998 service references. Kafka semantic pairs are not claimed as product
co-occurrence rows.

The production paths remain deliberately separate. Repository-partition
`grpc-consumer` extraction emits 10,950 local facts. Repository-partition
`grpc-caller` emits only prefix `110`'s 165 facts, 330 rows, and 165
references. The ordinary catalog-wide direct resolver then consumes all
21,603 caller candidates and publishes 10,999 resolved postings, 11,603
abstentions, 22,602 records, and 21,656,043 encoded bytes across eight exact
leaves. Only that last publication is the product RPC oracle.
Repository-wide attribution controls remain admitted caller inputs for that
direct resolver, but a single hash partition does not expose them as a
complete attribution corpus. Partition extraction still inventories every
admitted record and must read every extractor-`Required` record; it does not
invent a read obligation for an `Enumerate`-only downstream control.

The runnable source-free HTTP/MCP inventory freezes:

- one bounded structural All-code marker result while binding the exact
  combined physical authority;
- one existence-hiding unauthorized service-search result with one repository
  authorization point read and no service-runtime, generation, or search read;
- first-service detail with six memberships over five paths;
- one 20-claim shared placement;
- one unowned exclusion;
- chain callers and dependency, layered-DAG dependency, and bounded-fan-out
  dependency cases with cursor exhaustion;
- Kafka producer and consumer participation without a pair claim.

Selectors are source-free enums. Their projection digests commit the exact
independent records rather than embedding generated source or paths in the
retained plan.
Every HTTP and MCP result separately records request-local control and member
reads. Visible cases require at least one control read and cannot exceed the
matching `product_queries` phase-work maximum; corrected V2 permits a zero
member count for a warm or empty result. Retained V1 keeps its historical
zero/zero denial fixture; corrected V2 uses the production-shaped service
selector and requires exactly one control read and zero member reads for each
denied transport. Overflow-safe sums across both transports must fit the
measured `product_queries` phase totals.

## Frozen execution contract

Physical and logical histories are each exact A→B→A-return sequences. The
plan binds deterministic source tree/commit recipes over the exact T40 base
commits; T42.2 records the actual authored combined Git object IDs. The
physical delta changes one T40 file and returns to the original combined tree.
The logical delta changes one service and returns to the original semantic
digest under a new authority digest.

The phase order freezes preflight, cold, warm no-op, physical and logical
deltas, A return, stale-lease and hard-restart recovery, 80/90/75 pressure
transitions, archive/restore, lifecycle collection, product queries, and
teardown. The physical delta holds the old search reader while publishing and
retiring the new generation. Each injection leaves one bounded recoverable
target; a stopped receipt marks the remaining suffix `not_run`.

Each failure point is bound to a native production selector rather than only a
harness label. Catalog activation selects service 5,000, member 9, source range
`[4608,5120)`, and member range `[0,512)`. Interrupted publication selects the
exact relationship generation/root unit. Stale-lease recovery selects
`grpc-caller` prefix `110`, partition 6, source range `[16126,18830)`, and its
whole member. Hard restart selects `proto-contract` partition 2, source range
`[4096,6144)`, and member 1 range `[0,2048)`. Every selector also binds its
native generation, schedule, plan, and unit identities.

The safety envelope retains the 24-GiB memory and 120-GiB available-disk
minimums, 20-GiB peak-RSS and 96-GiB allocated-data ceilings, five-attempt
unit retry ceiling, 15-minute server-health, four-hour convergence,
20-minute revalidation, 80-GiB ballast, 68-GiB pre-pressure, and 18-hour total
wall ceilings. Pressure uses an exact 96-GiB sparse APFS data volume and first
requires live used and allocated bytes in the 8–68-GiB range. Every ballast,
data-allocation, and volume-used delta must reconcile within the frozen
allocation-unit tolerance: 80 percent collects, 90 and 75 percent refuse, and
zero ballast plus at most 74 percent recovers to normal. The 90-percent target
preserves at least the frozen 8-GiB custody margin. Before execution, a
separate canonical freeze binds
the clean integration and execution commits, executed-file SHA-256 identities,
bounded public tool versions, and source-free host/disk geometry. The closed
ordinary-production profile additionally binds normalized serve, backup, and
restore commands; a closed public environment projection plus private recovery
and server-environment digests; an exact raw-config digest plus its source-free
semantic projection; runtime constants; symbolic root/volume roles; private
harness and pressure-command digests; and the final invocation digest. Its
tool inventory includes the exact Go, Git, SurrealDB, Zoekt, Buf, Phebs, and
repository-built `phebs-focused-index` images actually executed by the closed
server environment. Phase runtime evidence binds epoch 1 from `cold` through
`stale_lease`, epoch 2 from `process_restart` through `pressure_75`, and epoch
3 from `archive_restore` through `product_queries`. Every passed phase and
every stop after its epoch server starts retains that exact runtime binding;
only an epoch-launch stop whose phase meter records zero Phebs children may
omit it.

The receipt schema requires that freeze plus typed
physical/logical/allocated byte metrics, store rows and transactions, source
and Git reads, observation and relationship work, cache/reuse, retries,
children, per-phase wall/RSS, decision, nonclaims, and clean teardown.
One fail-closed measurement stop may name a sorted unique subset of the six
frozen gauges; every named gauge must be absent in that phase while every
unnamed required gauge remains present.

The physical-delta reader evidence binds distinct one-record A and B
projections while the old generation remains leased, then requires the retired
old generation to return `not_found` after deletion. Archive evidence binds six
exact components and five omission-free reports, destroys the original
installation, observes an empty restore target, restores into that target, and
compares content-addressed native authority snapshots plus the semantic state;
no scratch-source path may remain.

Receipt authority stores each distinct nine-domain/56-partition root inventory
once, then references it from sorted, unique, content-addressed authority
snapshots and per-phase references. Successful state evidence retains the exact
observed projection digest; an `exact_oracle_mismatch` must additionally retain
the full mismatched projection for diagnosis. Unused root or authority
snapshots, snapshots on `not_run`, and mismatched native authority bindings are
refused. Every current authority snapshot binds one shared candidate-generation
digest, and every extraction root must name that same generation. A logical-only
delta must retain the complete extraction-root inventory byte-for-byte. A
stopped transition embeds only the same compact authority state and
its extraction-root snapshot digest; canonical decoding rehydrates roots from
the globally validated inventory rather than duplicating them.
The canonical plan, freeze, receipt, package, and expanded-package ceilings are
256 KiB, 64 KiB, 512 KiB, 4 MiB, and 4 MiB respectively. The fully populated
successful receipt fixture is 479,496 bytes, leaving 44,792 bytes of receipt
headroom.

## Reproduction

From a clean checkout at exact implementation
`8ca0d92410e3763b5c6c6664b26dc44ef2773edf`, the author writes a mode-0600
temporary file, syncs it, and atomically hard-links a destination that must not
exist.

```sh
T421_SOURCE_COMMIT=8ca0d92410e3763b5c6c6664b26dc44ef2773edf
go run ./spike/t421/cmd/author \
  -repository-root . \
  -source-commit "$T421_SOURCE_COMMIT" \
  -out /absolute/new/plan.json
go test -count=1 ./spike/t421/...
```

Two builds must be byte-identical. Strict decode rejects oversized,
unknown-field, trailing-value, noncanonical, source-bearing, or generator-
divergent plans. The retained `plan.json` is authored only after the exact
implementation commit is clean and independently reviewed.
Freeze mutation and receipt-fixture tests reuse the freeze and plan already
validated by their shared fixture instead of rebuilding the 10,000-service
corpus for every table row or binding. The public freeze and receipt-binding
entry points still reconstruct and compare the exact plan on every call. The
receipt binder owns a complete copy of the at-most-64-KiB freeze before that
one-time validation and retention.

## Retained plan and branch closure

Exact clean implementation
`8ca0d92410e3763b5c6c6664b26dc44ef2773edf` authored canonical source-free
`spike/t421/plan.json`: 199,561 bytes at
`sha256:96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34`,
62,583 bytes below the 256-KiB ceiling. An independent second build and strict
decode/re-encode were byte-identical. The complete T42.1 package passed
normally in 363.839 seconds and under the race detector in 1,614.282 seconds;
the production extraction packages passed normally in 15.727 seconds and
under the race detector in 78.127 seconds. Scoped lint, vet, module
verification, glossary, and whitespace checks pass, and independent
exact-commit review reports critical/high/medium/low `0/0/0/0`.

Repository-wide documentation validation retains only the exact-base UI-owned
missing `ui/receipts/fixtures/service-boundary.png` target referenced by
`ui/receipts/fixtures/markdown-preview.md`; repository-wide lint retains only
exact-base unused
`internal/relationshippublication/runtime_v3.go:719 matchesRuntimeAuthorityV3`.
Both reproduce unchanged from exact base
`d92b6673db6d4b582c2223536fe52358629ae60e`, and no T42.1-owned path causes
either finding.

Ben integrated and pushed closure
`ea9dd555e5b19a752255fb099ae43721b4df971f`. This retained plan is not an
exact-main execution freeze and does not authorize T42.2 execution, a combined
gate result, topology selection, SLO, supported limit, accuracy/completeness,
private replay, or release.

## T42.2 execution-readiness hold

The 2026-09-02 runner implementation audit found conflicts between this
contract and ordinary production behavior. The historical contract tests and
bounded replay results above remain exact; they did not prove that an actual
run could satisfy all phase predicates together.

- `receipt.go` requires replacement resolver/caller generations on
  `logical_delta_b` while preserving their immutable source, candidate, and
  extraction inputs. Production `resolvercatalog.NewIdentity` and
  `callerexecute.GenerationIdentity` do not bind the service catalog, so an
  unchanged-input rebuild cannot supply the required new identities.
- `plan.go` allows only 64 cold Git children, but the ordinary resolver uses
  10,002 one-child `gitobj.ReadBlob` calls before caller and other Git work.
  Logical delta requires those same 10,002 resolver reads while allowing zero
  Git reads and children. The required watched local connection also invokes
  `git rev-parse HEAD` every three seconds, including zero-child phases.
  The frozen meter counts distinct executed descendants, not concurrency.
- Stale/checkpoint phases follow complete exact authority, whose ordinary
  reconciler takes the reuse path. Their required unfinished same-generation
  targets need an explicit preparation transition; resetting durable jobs or
  deleting published controls is not an implicit runner action. Their zero
  publication-write budgets also do not cover recovery assembly.

T42.2 runner implementation is authorized on
`codex/t42.2-combined-ceremony`, but the approved T42.1r1 prospective contract
correction and its gates must finish before it can continue. Native failure controls, complete server
telemetry, real private admission constructors, and source-free sealing remain
implementation work. Do not substitute fixture counters, change production
identities to satisfy the oracle, silently enlarge budgets, or reuse the old
T40.13/T41.10 launchers. The retained plan bytes remain untouched. No ceremony
has been launched, and no execution command is ready.

### Prospective correction

The owning decision is the T42.1r1 row in [PLAN.md](../../PLAN.md). V1 authoring
and validation remain available internally for exact retained-byte tests;
new plan authoring targets v2. No superseding artifact has been sealed yet.
The production identity derivation table and checked budget formulas are part
of that prospective plan, not changes to the retained `plan.json`.
The compact table's `changes.physical_revision` expands to `physical_delta_b`
and `return_a`; other keys name their exact phase. Cold means initial, and
every unlisted operational phase requires equality. The strict expander checks
all thirteen cells and rejects overlapping aliases; the plan ceiling is unchanged.

The constructor acceptance test authors the exact frozen Git commits and
uses native source, candidate, extraction, resolver, caller, catalog, activation,
and relationship constructors. Its search leaf and measurements are explicit
test models: acceptance proves a coherent constructor graph can satisfy the
contract, not that an ordinary combined-scale run has passed. The separate
small server restart regression must cover the logical-update/census/read path.
Both are required before independent review and new canonical plan authoring.

The first small-server attempt stopped before restart (373.143 seconds): native
proto extraction completed with four facts/eight rows, but resolver declaration
reads returned empty. The worker selects the partitioned RunID, then calls legacy
`ListAssertions`, which accepts only `published` runs; native partitioned
publication seals the run while deliberately retaining `staged` status. Both
the resolver mock and the constructor replay bypass that visibility predicate.
T42.1r2 subsequently repaired that prerequisite on its separate reviewed branch:
exact-source upgrade from the actual predecessor and a later logical restart
passed in 179.87 seconds. The logical restart recorded one census, zero source
blob reads, and zero index children. Ben integrated its closure `529cb1d5`
locally; the strict native replay mock and successful server regression remain
unchanged. The contract draft is preserved at `afcaef1d`, and correction now
continues on `codex/t42.1r1-contract-correction`.

The full constructor fixture remains unpassed. The resumed audit found that
return-A's schedule must retain the cold/B predecessor chain, configured
startup deadlines lack independent readiness evidence, and the preparation
fixture models settled counters over unfinished files. An ordinary reconcile
of genuinely complete same-target files reuses its schedule; it does not
establish the frozen forced-recovery preparation. Resolve that executable
preparation boundary before another expensive acceptance run or new artifact.

The contract-only lineage and startup checks are now corrected and pass focused
normal/race tests. Each passed V2 server epoch requires a digest-bound readiness
event and elapsed duration within its frozen deadline; stopped startup preserves
not-ready, late-ready, or unavailable-duration evidence without claiming success.
Retained V1 bytes stay exact. The constructor helper no longer substitutes fake
settled counters: it requires actual current roots and successful real-store
settlement, and rejects a recovery schedule that never advances. The small native
runtime regression creates completed files and proves ordinary reconciliation
reuses the schedule and source work; its scheduler settlement remains a labeled
test double, not a real-store or zero-publication-write claim. The separately
reviewed T42.1r3 same-process preparation control is now integrated locally
through `3b16f721783d443b57c76505ec37fa31a8bac5aa`, and this draft is rebased
onto it. Genuine worker completion must be wired while each physical candidate
is current, before calling the same live reconciler's preparation hook; a later
second execution pass cannot reopen already-replaced candidate members. The
hook's control-read costs also need prospective accounting: the inherited
topology proxy is not a read-event meter, store query retries and candidate-cache
miss validation need charges, and one enqueue call is not one transaction
attempt. Derive the full-phase bound under unchanged ceilings. No complete
constructor rerun, new artifact, remote push, freeze, or ceremony is claimed.

Integration checks pass for the focused contract suite normally (45.747s),
and the real-store preparation plus ordinary same-target reuse regressions
normally (1.838s) and under race (2.979s). The broad contract race selector
hit its five-minute package allowance while rebuilding the independent oracle
inside `TestCorrectionSupersedesWithoutChangingCorpusOrSafety` (300.715s).
It is incomplete, not a race or acceptance pass. The isolated compact
work/startup/recovery/retained-runtime validators pass under race (2.400s),
without repeating that oracle construction; no test process remains.

Earlier draft checks: focused identity/table checks passed in 23.128 seconds;
retained-v1 preservation and v2 canonical/size/framing checks passed in 54.876
seconds. The prospective plan encodes to 259,673 bytes under the unchanged
262,144-byte cap; no artifact was authored. Epoch/preparation checks passed
normally and under race, and `go vet ./spike/t421`, glossary, and whitespace
checks passed. `make docs-check` retains the independently reproduced baseline
missing `ui/receipts/fixtures/service-boundary.png` link. Full-package, complete
constructor and independent exact-source review gates remain open. The separate
integrated real-server restart/upgrade gate above supersedes its earlier hold.

### September 3 native completion and read accounting

The constructor now replaces its mocked extraction pass with one real volatile
store and live reconciler/runtime. It finishes each physical generation before
replacing its candidate, executes the 56 native partitions and publishes all
nine roots, and carries actual run provenance into resolver and relationship
construction. The zero-partition Thrift root remains downstream authority, not
a fabricated resolver declaration. Operational schedule generations retain the
native cold/B/return-A predecessor chain independently of immutable extraction
generations. After genuine return-A settlement, the same enabled reconciler
prepares and drains both native recovery modes. Bounded result-file hashes,
store publications, source acquisitions and evidence appends fence reuse.
This is not stale-lease/process-death injection, trigger arming or archive
execution; those remain explicitly modeled, as do receipt resource readings,
signing and the search leaf. Source/observation files are real constructor
outputs with test-selected references, not ordinary-server publication proof.

Prospective V2 read accounting covers inspection, readiness, native preparation
and public-query work; it does not claim every pipeline I/O. A control read is
one file-control read attempt or read-only store query attempt. Metadata probes
are excluded. The inherited T40 topology proxy is not an event count. For one
successful native preparation, with D domains, N partitions and checkpoint
indicator C:

- Warm file attempts are `24 + 4D + N + C + B`, where B is zero or one
  existing-successor-binding reread. One cold candidate open adds one manifest
  read. For D=9 and N=56 this is 116–117 or 117–118 before the cold addition.
- Read-only store attempts are `D + 10 + sum(A1..A4)`, with each of the four
  schedule reads using the native 1–64 attempt bound: 23–275 here. One native
  enqueue is one schedule-write call but 1–64 attempted write transactions.
- Phase control reads must equal preparation file attempts plus preparation
  query attempts plus the separately recorded other scoped controls. The
  existing 4,096 phase cap still applies. The maximum preparation subtotals
  are 393/394, leaving at most 3,703/3,702 for other work at that maximum;
  this subtraction is admission arithmetic, not a full-phase cost proof.
- A warm candidate cache adds zero member visits. A cold open charges every
  actual artifact and projection-spool record visit, including rereads. The
  maximum per-domain population within each of the repository and caller
  artifact planes supplies a conservative 53,204-record floor without summing
  overlapping domains. Native 512-record binary-carry/final-merge arithmetic
  makes its artifact/spool floor 470,732 visits. The actual event count may be
  higher and remains charged to the unchanged phase member-read ceiling.

The fixed 64-level run-length calculation holds no source records, uses the
native admitted population bound and performs no I/O; native sorter or retry
changes require contract review. These are prospective receipt predicates,
not an implemented event meter. The complete other-inspection call graph,
cadence and retries must fit the remaining allowance, with a real scoped event
ledger, before T42 execution readiness. No production query, sync tick,
startup/restart, retry/no-op, publication, lock, cache, child or schema changes.
Full constructor acceptance, complete branch gates, independent review and
new canonical artifact authoring remain pending.

The first native-completion attempt stopped after 558.563 seconds. All three
physical generations settled their 56 chunks/nine roots; both completed-state
preparations then recovered 56 existing results with unchanged result-file
bytes/store publications and zero source acquisition/evidence appends. The
first logical catalog also activated before the constructor failed on an absent
resolver-namespace base directory. Four downstream constructor calls had passed
uncreated nested bases even though each native builder owns its own distinct
namespace. They now receive the existing shared temporary root; no production
directory policy changes. The test removed its workspace and both local stores;
no matching constructor or database process survived. This is partial fixture
evidence, not complete acceptance. The corrected full-package outcome follows.

Focused correction normal tests pass in 46.145 seconds and compact race tests
in 2.345 seconds. Plan sizing passes at 260,554/262,144 bytes. Repository-wide
compilation, scoped vet/pinned lint, module verification, glossary and whitespace
checks pass; docs-check retains only the known UI screenshot link. Independent
working-tree manual source and documentation reviews found one operational vs
immutable schedule comparison defect, corrected before the first native run,
and no other findings in their stated scope. Supplemental external review was
blocked before launch pending permission to transmit the changed source to its
configured provider; no external-review result exists.
The broader contract race selector subsequently passed in 567.092 seconds under
its 15-minute test allowance, including retained V1 decode/re-encode, prospective
corpus/safety preservation, epochs, work formulas and preparation accounting.
This closes that selector's earlier five-minute timeout, not the separate full
package/constructor race bar or any ceremony deadline.

The corrected full normal suite failed in 990.689 seconds: 54 of 55 top-level
tests passed, including the separate strict production pipeline replay
(287.29 seconds). Complete constructor acceptance alone failed (567.01 seconds).
It again passed all three physical generations and both result-preserving
recoveries, then reached `RPC caller posting bound exceeded` in ordinary RPC
component construction. No completed constructor receipt was produced, and all
test/database processes exited.

The binding arm is the shared namespace cache, not posting volume or recovery.
RPC probes every nonblank import for both gRPC and Thrift. The frozen overlay's
10,000 provider namespaces plus Sarama require 20,002 distinct protocol/import
keys: 10,000 present namespaces and 10,002 successful-empty misses. The cache
charges and retains all of them against `MaxNamespaceReads=16,384`, even though
the immutable resolver root answers misses without reading a member. The
ordinary V3 runtime uses the same complete inventory, descriptors and builder;
prior-member reuse occurs after this walk and cannot avoid the refusal.

Separately, the fixture's three component requests omitted native resident
limits. They now pin `ResolverResidentLimit`, `RPCResidentLimit` and
`KafkaResidentLimit` (128/192/160 MiB). Their previous unrestricted defaults
did not cause the namespace refusal; this tightening may expose another bound
and is not a completed acceptance result. No expensive unchanged rerun is
useful. Proposed T42.1r4 requires separate production authorization and review
for bounded namespace handling with unchanged caps, corpus and exact output/root
authority, including policy/identity compatibility and steady-state-cost checks.
Production retains this bound as a closed same-target terminal failure. The
separate prerequisite must explicitly review an existing build-policy-target
revision and test old-terminal replacement plus historical publication/reuse/
archive compatibility; an implementation-only optimization would leave existing
failures stuck, while a blind RPC artifact-policy bump could break old readers.
The contract draft remains held; no production fix, new canonical plan, merge,
push, freeze or ceremony is claimed.
After pinning the native resident limits, compact preparation/runtime race tests
pass in 1.629 seconds; scoped vet, pinned lint, glossary and whitespace checks
also pass. This compiles but does not execute the complete constructor. The
unchanged missing UI screenshot remains the only docs-check failure. Retained
plan, T41.10 receipt and logical-restart regression hashes remain unchanged.

### Checkpoint integration decision

Ben subsequently requested commit and local fast-forward merge of this
checkpoint, with the known-red constructor explicitly retained. This is not
T42.1r1 acceptance or closure and does not waive its remaining review/metering
gates. T42.1r4 implementation still requires separate approval. The original
unmerged draft remains at `afcaef1d` on `codex/t42.2-combined-ceremony`; no push,
new canonical artifact, freeze or ceremony is authorized by this integration.

### T42.1r4 prerequisite validation

Ben then approved the separate production prerequisite on
`codex/t42.1r4-bounded-namespace-lookup`, based on checkpoint `a57a4bd9`.
The shared RPC builder now proves absence from its already-validated immutable
namespace inventory before attempting a member read. Absent keys retain no
cache entry and consume no member-read budget; present keys retain validation,
integrity checks and the unchanged 16,384 ceiling. Serialized component policies,
output identities, frozen corpus and resident limits are unchanged.

Both selected-V2 and direct catalog-V3 scheduling now bind the revised
operational build policy. Queue-model regressions exercise the real reconcile
and target derivation paths: old terminal/active targets can be superseded,
same-new-policy terminals remain retained, and exact-current direct-V3
publications remain reusable before admission. Historical direct bindings and
legacy shadow continuation remain readable. A genuine selected-V2 publication
test exposes its unchanged resolver-catalog vs resolver-namespace comparison
defect; it preserves that baseline redundant scheduling, not a fabricated V2
no-op. These tests are not a live-server upgrade or recovery receipt.

`TestFrozenOverlayNativeRelationshipComponents` first passed in 301.80 seconds
(302.233 seconds for the package). It constructs all frozen overlay inputs
through real source/observation, proto extraction, resolver and posting builders:
21,601 observations, 20,002 distinct lookup keys, 10,000 present namespaces,
10,002 absent keys, 10,999 resolved RPC rows, and 500 producer plus 9,500 consumer
Kafka rows. Resolver/RPC/Kafka resident fences remain 128/192/160 MiB. Structural
inputs are the existing replay's representative census; declaration publication
and current selection use its strict in-memory store model. This test does not
execute the other eight extraction domains, caller leaves, service projections,
durable scheduling, archive, authorization or an ordinary server. Kafka row
counts are not a service relationship-pair proof.

The initial test used a five-minute context. Its final context is ten minutes,
twice that rounded observed runtime, solely for host-variance headroom; no
production or ceremony deadline changes. The context-only revision passed its
single final run in 404.22 seconds (404.803 seconds for the package), with the
same exact counts and limits; its temporary source and known children were
removed by normal test cleanup. Full affected package normal/race, scoped vet
and pinned lint, repository compilation, module verification, glossary and
whitespace checks pass. The missing UI screenshot remains the only docs-check
failure. The remaining T42 normal suite stopped in 433.566 seconds with only
the complete constructor failing on sandbox local-port denial before opening
its database; the separate native pipeline replay passed in 277.71 seconds.

The host-permitted constructor then ran for 926.30 seconds (926.833 seconds for
the package). It completed A/B/return-A's 56 chunks and nine roots, both
56-result recoveries with zero source acquisition/evidence appends, and all
native downstream construction before `ValidateReceipt` rejected
`phase "pressure_80" pressure facts are invalid`. Thus the RPC namespace
refusal is cleared, but complete constructor acceptance is still red. The
pressure rows are explicitly modeled measurements, not an executed pressure
ceremony. Their shared test helper hardcodes `ServerEpoch: 2`; the corrected
phase table and checkpoint injection derive post-restart epoch 4, which the
unchanged validator correctly requires. The contract-fixture follow-up must
derive pressure epochs from that same phase table for both retained and
corrected plans, with a cheap pressure-only counterexample before another full
constructor run. Do not weaken the validator, hardcode 4, or call this a host
pressure failure. The existing phase-table regression passes in 0.609 seconds;
the constructor workspace was removed and its known processes exited.
Independent working-tree implementation, component-test and documentation
reviews report no remaining findings in their stated scope. Retained plan,
T41.10 receipt and logical-restart regression hashes remain unchanged.
Source review also found a separate cross-version fixture hazard:
`mustExecutionFreeze` caches one freeze under an unkeyed `sync.Once`, despite
receiving either a V1 or V2 plan. The constructor uses the private receipt
binding helper, which assumes prior freeze admission; unlike the public binding
entry point, it does not validate that freeze against the supplied plan. Mixed
test order can therefore supply the wrong profile. This did not cause the
isolated epoch-4 failure above. `ValidateReceipt` still rejects the wrong
plan digest after construction; no false-positive complete acceptance is
established. The contract-fixture follow-up must isolate
caches by exact inputs, cover both V1/V2 orders without native construction,
and exercise real freeze admission before claiming constructor acceptance.
These are test-fixture changes, not permission to weaken the public validator.
No complete-constructor acceptance, exact-commit review, new canonical artifact,
merge, push, freeze or ceremony is claimed here.

### T42.1r5 fixture-only follow-up

Ben approved this follow-up on `codex/t42.1r5-fixture-epoch-admission`, stacked
above local namespace-fix checkpoint `1ba00f7d`. Pressure rows derive their
server epoch from the admitted phase table. A single exact-plan/commit cache
owns publicly admitted freeze bindings and returns defensive copies; the
constructor obtains one before native work. Mock tool provenance follows the
supplied source commit. The native authority/recovery witness remains a single
exact-plan result rather than a multi-corpus cache.

Complete production-derived constructor acceptance now passes in 786.40s,
including the formerly invalid `pressure_80` boundary. Its completed fixture is
484,348 bytes. All three physical revisions settle 56 extraction chunks and
nine current domain roots; both result-reuse preparations complete with zero
source acquisition and evidence appends. Measurements/search-leaf evidence
remain modeled, and these preparations do not inject a stale lease or process
death. This is constructor acceptance, not ceremony evidence.

The complete normal command,
`go test -json ./spike/t421/... -count=1 -timeout=60m`, passes in 1799.598s:
62 top-level tests, no failures and no skipped
tests (the author command package has no tests). This includes the separate
production pipeline replay (300.28s), full-overlay native relationship
components (301.09s), cross-version/exact-input/copy-isolation/public-admission
refusals, pressure counterexamples, and retained receipt/recovery gates.
Both cache version orders also pass under race in 1336.37s. The broad
race selector was intentionally interrupted after that result (1486.973s total)
because full-plan regeneration under instrumentation made it expensive; that
command is not an aggregate pass. The final targeted ownership/pressure race
selector passes in 802.655s, covering both versions and all six stale-epoch
counterexamples. No full-package race pass is claimed.

Independent working-tree source/documentation reviews have no findings. No test
or Surreal process remains after the complete run. Scoped vet/pinned
lint, module verification, glossary, formatting and whitespace pass; docs-check
retains the known missing UI screenshot only. Production code, oracle checks,
retained artifacts and safety/ceremony limits stay unchanged. No merge, push,
new canonical plan, freeze or ceremony is authorized. The owning decision and
cost record are in `PLAN.md`.

### T42.1r6 bounded native read-accounting prerequisite

The separate approved prerequisite starts from integrated T42.1r4/r5 commit
`d776e1d7`. Its first slice attaches an explicit operation-context ledger to
native recovery preparation and candidate reads. There is no ordinary server
caller, environment switch, HTTP endpoint, background collector or event log.
Four fixed counters distinguish control-file attempts, read-query attempts,
decoded member visits and enqueue-write attempts. Actual retry-loop attempts
are charged individually; topology and expected-result formulas are not events.
Invalid/nested scopes, invalid events, late charges and the first over-limit
charge make the ledger unusable as successful evidence. The first over-limit
counter is a refusal sentinel, not a claim that its denied I/O executed.
Owners finish only after all scoped operations have returned; scope context is
never stored on a reusable publication. A successful ledger result does not
replace the operation's error or cancellation result. Independent metadata
probes are not control reads, and failed decoding invents no decoded record.
Successful decoding is charged even when subsequent canonical/semantic
validation refuses.

The existing one-partition real-store test scopes only `PrepareRecovery`,
excluding setup, verification and later recovery. With D domains, N results
and checkpoint C, its file-read derivation is `8 + 4D + N + C`, plus one
optional identical-binding reread. Its smaller authority callbacks yield
`D + 4 + sum(four schedule-query attempt counts)` store reads. For D=N=1 and
no retries, that means 13/14 file attempts, nine read queries, one enqueue
attempt and zero member visits. The callback source/observation inputs remain
modeled. Ordinary server callbacks and the full constructor have different
read costs; this test cannot establish those totals.

Candidate strict-open accounting includes the manifest attempt and decoded
artifact records plus every projection-spool merge/final-scan visit. Member
replays use their current caller context; retaining an already validated
publication does not itself reread members. Ordinary operations add bounded
context lookups/branches and no ledger allocation or locking. Each temporary
projection reader also carries its operation context; at most two merge input
readers are open, adding two interface fields (32 bytes on 64-bit hosts).
An active ledger retains four counts, four limits and one local mutex; charges
hold that mutex only for checked integer updates, never I/O or callbacks.

The genuine real-store preparation passes with the exact counts above and
unchanged result/evidence preservation. Complete ledger/candidate/extraction
normal and race packages pass, together with scoped store accounting tests
normally and under race, repository compilation, scoped vet, repository-pinned
2.12.2 lint, module verification and glossary. Additional late-control-limit
tests preserve the committed successor and checkpoint prefix without retry or
rollback. Independent source cross-review has no actionable findings within
this scope. Docs-check retains only the known UI screenshot gap. This is a
working-tree result, not a full-store/full-repository runtime or ceremony pass.

That first slice did not close ordinary callback, public-query or complete
inspector coverage, and changed no prospective phase budget or retained freeze.
Sparse-root/domain controls and publication-marker reads are not covered by
the candidate slice. Whole-phase cadence/retry/member derivation remains
T42.1r1 work before T42.2 can execute.

The subsequent bounded slice exercises the actual ordinary candidate provider
against native candidate output and a real store: cold open observes four
queries, one manifest and four decoded visits for its two-record fixture;
warm open observes four queries only, and a compact reference two queries.
Marker checks on these paths are metadata-only `IsPublishing` probes, not
marker-content reads. A later replay charges its own scope. Final pointer
refusal can evict the existing cache and make a subsequent attempt cold;
that existing behavior must remain in any retry budget.

The ordinary observation-fence callback is now tested through its shared
server helper and native source/inventory builders: one initial pending probe
plus the selected pointer, selected super-root and confirming pointer means
four control attempts. The compact reference alone costs three. Existing
authority, error and cancellation behavior stays unchanged; accounting does
not add per-read cancellation checks. Each of its four recovery-preparation
invocations therefore adds four controls. Combining the measured components
with the production call graph gives ordinary preparation
`24 + 4D + N + C + B + H` file attempts and
`D + 10 + sum(A1..A4)` read-query attempts: C is checkpoint, B is an optional
identical-binding reread, H is a cold manifest open (each 0/1), and each A is
one schedule read's actual 1–64 attempts. This composition is not an end-to-end
ordinary preparation run. Cold member work is the actual strict-open/projection
work already described, not one visit per logical member.

Direct extraction inspections also carry the scope through their existing
readers. With complete current authority, `Current` costs four controls;
`Status` costs `1 + 6D + N`; `Progress` costs `2 + D` controls and two native
schedule reads, each with 1–64 attempts. Missing/invalid pointers can shorten
Status without making it current. Scoped accounting errors cannot be swallowed
as ordinary not-current results. Real-store one-partition Progress observes
three controls and two queries. These paths add no I/O, children, persistent
state or new locks: only the existing inactive lookup or scoped counter update.
Full-stage source/inventory scans, sparse candidate controls and HTTP/MCP
attribution remain outside this proof. No whole-inspector budget, phase cap,
retained plan, freeze or ceremony is changed.

The first real transport slice started narrower than the unfinished inspector.
With `PHEBS_T421_EXACT_READS=source-free-v1`, the server wraps the
post-authentication legacy-config-key `GET /api/extraction-progress` handler.
Exact requests carry `X-Phebs-T421-Exact-Reads: source-free-v1` and the
next canonical `X-Phebs-T421-Exact-Read-Ordinal`. The server, not the client,
sets limits: `2 + extractionpublication.MaxDomains` control reads, two
authorization reads plus two independently retried schedule reads, and zero
members/writes. Ordinals are canonical, strictly sequential, single-active and
bounded against integer overflow. The exact per-epoch call inventory is still
open, so this slice deliberately claims no smaller cadence-derived quota; the
final runner must derive and freeze one from its cumulative phase inventory.

After the synchronous handler returns, one canonical
`t421-source-free-read-accounting-v1` object is written both to the exact log
sink and the base64url `X-Phebs-T421-Exact-Read-Report` response trailer. It
contains only schema, ordinal, accounting status and the four counters. Exact
admission, ledger, report or incomplete-handler failure cancels the server and
survives as a distinct terminal error. Authentication remains outside the
scope, and anonymous headers on public routes cannot reach the failure latch.
`complete` means the accounting scope closed, not that HTTP semantics passed;
the runner must also bind the response oracle, server epoch and phase.
Ordinary mode returns the original handler without a wrapper; it gains only the
already-recorded inactive context checks in instrumented readers.

The subsequent compact-inventory slice derives calls from the existing phase,
deadline and five-epoch tables instead of copying T40's full scan. `H` is
epoch-launch health at 250 ms; `X` is exact extraction progress, immediate then
at five-second cadence only until stage readiness; `F` is one coherent final
current-authority and authorized-semantic pass; `L` is lifecycle status only
for pressure-80, pressure-75 and lifecycle collection; `R` is transition-local
evidence, including archive/destroy/restore comparison; and `Q` is the frozen
HTTP/MCP page inventory. Product queries use
`F,Q,F`; every other operational phase has one final `F`. The maximum attempt
formula is `1 + floor(deadline_ms/cadence_ms)`, conservatively admitting a
timer/ticker tie at the deadline. At that pre-T slice this yielded
accounted-server-call maxima of 5,763, 2,881, 5,762, 3,366 and 5,802 for epochs
one through five.
Stale-lease recovery is part of X readiness and therefore retains the full
five-second retry inventory rather than one optimistic call.

V2 binds the inventory schema, both cadences, every phase row and every epoch
aggregate through `inspection_inventory_sha256`; plan validation rejects a
changed digest. The rows stay derived from the existing phase/deadline tables
instead of being duplicated into the plan.

Q runs cases in plan order, HTTP then MCP, without unrelated product traffic.
All-code uses the shared-current reader. Startup's current service-runtime pin
already warms the shared relationship generation cache; F cannot warm the
separate catalog root/member cache, which therefore misses once on the first
visible detail. With six relationship first pages and eight continuations, the
two transports derive `C=2*(2+2*16+6*5+8*2)=160` file controls and
`S=2*(2+1+7+2*11+6*4+8*3)+3+1=164` store attempts, so `K=324`; writes are zero.
These are contract formulas awaiting transport instrumentation, not a measured
Q pass. A member visit is one application record successfully decoded from a
bounded immutable member payload, charged before any later framing, canonical,
semantic or consumer refusal. The frozen units are candidate artifacts and
projection rows, source owners, catalog services/memberships/inherited entries/
placements, relationship fragments/services, RPC/Kafka postings and caller
leaves. Rereads count; roots, pointers, receipts/descriptors, response wrappers,
derived objects and cache hits do not; warm and empty reads may visit zero.
The first HTTP service detail and both relationship transports must be positive;
the following MCP detail cache hit and other warm/empty cases must be zero.

Native M instrumentation now lives at the shared immutable-member decoders:
source owners, caller leaves, catalog services/memberships/inherited entries/
placements, relationship fragments/services, and RPC/Kafka postings. A charge
follows successful application-record decode but precedes later framing,
canonical, semantic, or consumer checks. Bounded array-backed members charge
their decoded cardinality once. Refusal happens before delivery or cache
insertion; retries and explicit rereads therefore count again. Roots, pointers,
receipts/descriptors, wrappers, derived projections, current checks and cache
hits remain zero. The context-aware complete-catalog form is `CatalogContext`;
ordinary `Catalog` retains its prior behavior through a background wrapper.
These sites enable later F/Q receipts but do not yet derive their totals.

All affected current-source packages pass normally and under race, together
with repository compilation, scoped vet, pinned 2.12.2 lint, module
verification, glossary and whitespace. Independent review found one catalog
ordering defect: whole-input preflight rejected a trailing value before the
typed decode could charge its already-decoded records. The shared decoder now
preflights the bounded first value, performs typed decode and M charge, then
rejects trailing or noncanonical input; strict collection preflight retains its
whole-input EOF check. The trailing-value regression passes normally and under
race, and exact correction re-review is critical/high/medium/low `0/0/0/0`.
Docs-check still reaches only the known UI-owned missing screenshot. F must use
`CatalogContext`, Q must use the cache-backed catalog reader, and relationship
reads must remain V3-only for this coverage to be complete.

F's first bounded foundation is the private V3 relationship snapshot. The
cache entry's existing lease pins the selected generation through a full scan;
the reader validates every service and repository member and every reference
join, collects only deterministic projections, then rechecks the current
pointer before exposing the result. It returns no partial object on a late
failure. One physical snapshot therefore charges one C per service/repository
member file and exact M of `service_count + projection_fragment_count`;
repeated calls reread and recharge because no semantic result cache was added.
The retained projection charge is
canonical JSON bytes plus the builder's 512-byte allowance per projection,
bounded by the existing 512 MiB resident fence. That fence is not a process-RSS
bound: one bounded member, validation maps, marshal temporaries and slice growth
are additional transient memory. The ordinary complete validator supplies no
collector, retaining no projections and doing no new semantic marshal.

The focused suite covers exact completion and caller ownership, exact resident
admission, corrupt final-member refusal after full decoded-record charging,
M-limit refusal, cancellation and final-pointer supersession. The independent
correction re-review is critical/high/medium/low `0/0/0/0`. The complete
relationship-publication package passes normally in 32.125s and under race in
207.095s; repository compilation, scoped vet/pinned 2.12.2 lint, glossary and
whitespace pass. Docs-check reaches only the unchanged UI-owned missing
screenshot. This slice does not implement F's catalog/state/search/source/
extraction authority, authorization, RPC/Kafka composition, private semantic
cache, exact C/S/M totals, handler, phase caps or replacement freeze.

F1 now supplies the selected-catalog foundation without publishing a partial F
surface. `ReadCatalogContext` accepts the exact root supplied by the later
outer authority reader, takes ownership of each service/placement member byte
slice, and performs the complete `CatalogContext` validation. The cold M term
is therefore the records actually decoded before any later refusal: 10,000
services + 60,000 memberships + zero inherited placement entries + 31,605
placements = 101,605 for the frozen corpus. The validated catalog is reduced
once to its logical digest, authority-neutral semantic digest, canonical source
identity, and catalog, membership, placement, override-unowned-prefix, and
service-query set identities. The common five-set derivation now serves both
plan authoring and exact F, while the independent arithmetic oracle remains the
plan's separate cross-check; a runtime regression compares all eight fields for
A, B, and A-return. Member decoders batch-charge once per member, so the cold
M total takes at most 64 short ledger mutex updates rather than 101,605 locks.

The private cache holds only those scalar identities in one slot keyed by the
complete comparable runtime selector. A miss returns a pending value and does
not warm the slot; only the future whole-F reader may call the deliberately
named `commitAfterFinalFence` after reauthorization and all final authority
checks. Cancellation, decoded-record limit, corruption, derivation failure, or
an omitted commit leaves the old slot unchanged. A committed hit charges zero
M, performs no member read or semantic walk, and returns its own exact-root
descriptor slices. A replacement selector displaces the one old slot only
after its successful final fence, and a restarted process begins cold. Q's
existing catalog root/member cache is never consulted or warmed.

Cold transient memory remains bounded but is not equated with the logical-byte
limit: at most 64 owned encoded members/32 MiB plus one at-most-2-MiB
source-returned member buffer, the normalized catalog Go object whose canonical
logical encoding is at most 16 MiB, an at-most-16-MiB canonical source buffer,
sorted shallow views, bounded placement/query maps, and hash state can coexist
before being discarded; this inventory is not a process-RSS bound. The retained
slot is the full selector key, eight
scalar identities, a flag, and a mutex. A warm hit uses one short mutex section,
separately performs the complete at-most-256-KiB root validation/digest pass,
and clones at most 64 root descriptors; it does no member read or catalog walk. The
at-most-64 member reads become production Surreal queries, but F1 does not yet
charge or derive their S total. This slice is still deliberately unwired. The
outer F reader must add initial/final repository authorization, initial/final
complete runtime selector and selected-state fences, fresh exact-root
acquisition, every other authority plane, the route/receipt, and exact C/S/M
totals. No phase cap, ordinal ceiling, plan freeze, merge, execution, release,
or scale claim follows.

Independent F1 review found one medium cost defect in the first grouping pass:
each membership scanned the claims already accumulated for its path. The
membership sort makes the current service's claim the last claim for that path,
so the corrected implementation uses one constant-time last-claim check.
Correction re-review is critical/high/medium/low `0/0/0/0`. Complete normal
catalog/projection/command suites and the exact focused race/parity gate pass;
repository-wide compilation, scoped vet, pinned lint with zero issues, module
verification, glossary, formatting, and whitespace pass. The complete package
crossed a 30-minute cumulative allowance while the inherited Kafka
relationship-component test was syncing a file; that test passes alone in
296.036s, and no test process remains. Docs-check still reaches only the known
UI-owned missing screenshot.

Only immutable decoded member inventories may be reused within one live server
epoch after a fresh complete authority-key match. Logical delta B and process
restart launch new epochs and therefore start cold. Mutable pointers,
authorization, epoch/config identity, lifecycle/capacity, recovery residue and
pagination remain fresh. F reads selected activation authority from the store
and uses a private catalog projection cache, isolated from Q's catalog cache.
V2 therefore names F's semantic reader as private exact-current authority;
retained V1 keeps the historical public-product-reader label.
Archive does not borrow extraction/relationship evidence across destruction;
its exact catalog binding continues to use the intentionally census-free native
reuse path selected by the later correction ADR.

V2's closed server environment now enables the exact-read mode. The same
post-auth wrapper also admits `GET /api/lifecycle-status` with zero native-read
limits: the endpoint returns its already-maintained bounded in-memory snapshot,
so a future instrumented file/query/member/write event fails closed. This adds
one ledger/report only to marked exact-mode calls and no ordinary endpoint work.

This is still not complete HTTP/MCP or phase accounting. Local `F/R/Q` reads,
transition polling, product protocol overhead, final ordinal ceiling and
replacement phase budgets remain uninstrumented. The audit also proves
the retained prospective caps cannot yet be frozen: warm/logical phases still
allow zero controls despite native nonzero reads, while one fresh inherited
T40 extraction scan needs at least 294 controls for the T42 profile, already
above the 255 full-pass cap. The correction therefore requires a compact T42
inspector budget; neither retained artifact nor phase cap changes here.

Final validation closes this transport slice. Complete `cmd/phebs` and
`internal/extractionpublication` suites pass normally in 53.881s/41.746s and
under race in 55.947s/39.722s. The real-store regression observes the complete
one-domain HTTP/Huma/authorization/runtime composition at three controls and
four store reads; the wire test reads a 200 body to EOF before comparing its
trailer with the synchronous sink. Affected vet and pinned lint, all-package
compile, module verification, glossary, formatting and whitespace pass.
Independent review passes plus the final correction re-review leave
critical/high/medium/low `0/0/0/0`. The one
documentation failure is the unchanged UI-owned missing
`ui/receipts/fixtures/service-boundary.png`.

The expanded working tree passes full normal and race suites for ledger,
candidate, candidatejob, extractionpublication, sourcepartition,
observationpublication and cmd/phebs; scoped store accounting normal/race;
repository compilation; scoped vet and pinned 2.12.2 lint; module verification,
glossary and whitespace. Independent source/cost review is clear after one
cost-comment wording correction. Docs-check still reports only the known
missing UI screenshot. These are package/component gates, not a full store,
full repository runtime, ordinary-server replay or ceremony pass.

#### F2 final-authority reader, transport, and accounting

F is one private exact-mode `GET /api/t421/final-authority`, admitted only after
legacy-config-key authentication, the exact activation header and the next
canonical ordinal. It accepts no query and permits one active marked request.
The response uses `t421-final-authority-source-free-v1` with the
`t421-final-state-projection-source-free-v1` projection and contains no
repository path, directory, private error, cause or outcome text.

The reader authorizes the repository before touching private authority. It then
reads the complete runtime selector, selected state and exact activated
plan/settled schedule/unit; acquires the selected catalog root and complete
catalog; pins the immutable search generation and validates equal HEAD-only
receipt/search/source controls; derives the physical tree and source-free
search, observation and candidate identities; reads current observation and
candidate authority; and opens every exact extraction plan/root and physical
candidate-member partition. Final confirmation repeats every extraction
authority, candidate, observation, catalog root, selected state, activation,
caller, relationship pointer, runtime selector and repository
authorization/commit fence.

Source candidate membership is checked through two independent physical
planes. The source walk derives an order-independent proof over each selected
regular record's path, object ID, declared bytes and required flag. Extraction
replay rebuilds that proof from every candidate-member execution partition and
requires exact cardinality and exact proof equality. Repeated execution
subranges are physical rereads and are charged again.

The relationship side reads and validates every V3 service and projection
member, derives the source-free semantic families and product projection, then
opens the exact resolver, RPC and Kafka component generations. Resolver
validation reads every namespace member. RPC and Kafka walk every posting
member, and the resulting multiset must exactly equal the relationship
projections by kind, plane, class, lookup key and posting digest; missing, extra
or self-consistently replaced components refuse. The caller generation must
bind the same commit, candidate, resolver and relationship upstream authority
and remain current at the final fence.

F owns two process-local one-slot caches. The source key is repository, search
generation, source generation, commit and policy digest; the catalog key is the
complete runtime selector. A miss is only prepared. After all authority fences,
the caller lease is explicitly released and any release error refuses the read;
the deferred release remains idempotent cleanup. The transport must then write
the complete body and finish the ledger cleanly before committing the pending
source and catalog values together under the fixed source-before-catalog lock
order. No cache lock spans I/O or projection work. Q's catalog cache and the
shared candidate, relationship and caller caches retain their independent
policies.

For one F call, with indicators in `{0,1}`:

`C = 29 + 3D + T + V_f + R_f + U + I_s + I_c + I_r + 5I_l`

`S = 30 + 6D + 3A + G*I_g + H`

`M = V + E + R_m + Z + 2N*I_s + P*I_c + K*I_g + J*I_l`

`W = 0`

`D` is the extraction-domain count; `T` is the number of present typed-scope
controls; `A` is the caller-adapter count; `V_f` is the V3 relationship service-
plus-repository-member-file count; `R_f` is the resolver namespace
member-file count; `R_m` is the resolver decoded-record count; `U` and `Z` are
the combined RPC/Kafka member-file and decoded-posting counts; `G` is the
selected catalog member-query count; and `H` is the actual zero-through-two
selected-state historical-preimage fallback reads. `N` is the source-owner
count; `P` is one candidate repository-plus-caller artifact traversal, every
input record reread by the existing 512-record binary-carry and final-fold
merges, and one final projection scan. Whole-repository exact F admits no local
projections. `K` is the decoded catalog-record count; `V` is V3 relationship service records plus
projection fragments; `E` is physical candidate records decoded by extraction
replay, repeated for every execution subrange; and `J` is caller-leaf records.
`I_s`, `I_c`, `I_r`, `I_g` and `I_l` respectively indicate source, candidate,
relationship-root, catalog and caller cache misses.

Thus the empty-reader-cache cold envelope on the current-summary path is
`C=37+3D+T+V_f+R_f+U`, `S=30+6D+3A+G`,
`M=2N+P+K+V+E+R_m+Z+J`, `W=0`. Fully warm is
`C=29+3D+T+V_f+R_f+U`, `S=30+6D+3A`,
`M=V+E+R_m+Z`, `W=0`. Warm still rereads extraction candidate members,
complete relationship semantics, resolver namespaces and RPC/Kafka postings.
“Warm” means these reader caches, not OS page cache. A ceremony epoch may
already have a warm relationship root, but the receipt records the observed
`I_r`; admission never substitutes that startup assumption.

The no-slack per-request maximum is derived from the owning production limits:

`Cmax = 37 + 3*extractionpublication.MaxDomains
          + extractionpublication.MaxDomains
          + relationshippublication.MaxServiceMembersV3
          + relationshippublication.RepositoryBuckets
          + resolvernamespace.MaxNamespaces
          + rpccallerposting.MaxMembers
          + kafkatopicposting.MaxMembers
       = 18,469`

`Smax = 32 + 6*extractionpublication.MaxDomains
          + 3*callerleaf.MaxCallerDomains
          + servicecatalogv3.MaxMembers
       = 528`

`Mmax = 2*repositoryindex.MaxOwners
          + candidate.MaxWholeRepositoryStrictOpenMemberVisits()
          + extractionpublication.MaxDomains
            * candidate.MaxDomainResultPartitions
            * candidate.MaxRecordsPerArtifact
          + servicecatalogv3.MaxTotalServices
          + servicecatalogv3.MaxMemberships
          + servicecatalogv3.MaxDistinctPaths
          + servicecatalogv3.MaxMembers*servicecatalogv3.MaxDistinctPaths
          + resolvernamespace.MaxRecords
          + relationshippublication.MaxProjectionRecords
            * relationshippublication.MaxProjectionBucketsV3
          + relationshippublication.MaxServicesV3
          + rpccallerposting.MaxPostings
          + kafkatopicposting.MaxPostings
          + callerleaf.MaxAggregateCoveredCandidates
       = 589,656,064`

The candidate term is exactly
`4*candidate.MaxCorpusEntries + B512(2*candidate.MaxCorpusEntries)
= 353,296,640`, where `B512(n)` sums every input-run length consumed by the
production binary-carry and final-fold merges. It is neither slack nor a local-
projection allowance.

`Wmax=0`. These are request-admission ceilings with an exact limit-plus-one
refusal sentinel, not frozen phase totals, expected ceremony counts, memory/RSS
bounds or supported production scale.

Only exact mode installs the server root as `http.Server.BaseContext`; terminal
exact failure therefore cancels other in-flight work and blocks pending cache
commit. Disabled mode returns the ordinary handler directly and leaves the root
context nil. Caller release uses the existing registry transition token, so a
concurrent publication transition may delay F and a last reader may perform
retired-byte cleanup. That cleanup is existing publication-transition work,
not a native read/write event; release is deliberately not request-cancellable,
and cleanup failure refuses F. F adds no Git/blob read, child, store write, new
publication mutation, persistent artifact, ordinary request, sync tick,
startup scan or retry/no-op work beyond the default-inactive accounting
branches and existing lease-release behavior.

Focused accounting, candidate-proof, component-composition, source-free-shape,
limit/refusal, cancellation and cache-order tests pass normally and under race.
Complete `cmd/phebs` and resolver-namespace normal suites, the complete
resolver-namespace race suite, all-package compilation, scoped vet, module
verification, formatting, whitespace, and glossary checks pass; docs-check
reaches only the pre-existing UI-owned
missing `ui/receipts/fixtures/service-boundary.png`. Final independent review
reports critical/high/medium/low `0/0/0/0`. This is an implemented F2
surface, not a completed replacement-plan gate. It does not close R/Q totals,
cumulative phase budgets, the final ordinal, corrected-plan freeze or execution,
and establishes no public API, release, accuracy/completeness, supported limit,
SLO, topology, migration, decommission or Epic-42 claim.

#### F2 post-X tail-readiness correction

Extraction-current is necessary but not sufficient before the phase's one F
pass: downstream relationship and caller authority can still be settling, and
a logical phase's selected runtime intentionally remains on A until B is ready
to publish. Exact mode therefore adds one source-free `T` read after X. T reads
the selected runtime, relationship pointer, relationship root, resolver
namespace root, caller summary/current predicate, relationship pointer again,
and selected-runtime confirmation. Its caller comparison uses the resolver
namespace root's underlying resolver-catalog generation and manifest. F's
resolver fields and caller comparison use the same identities; the enclosing
namespace generation/root is storage authority, not the resolver-catalog
identity consumed by caller publication.

A missing relationship or resolver immutable root is pending only when a
second relationship-pointer read proves the first authority was superseded.
When the pointer is unchanged, missing bytes are terminal corruption. The ready
path is exactly `C=4,S=4,M=0,W=0`: two pointer reads, one relationship-root read,
one resolver-root read, the selected-runtime read and confirm, and caller
summary/current reads. A deep attempt transiently decodes at most 256 KiB of
relationship root and 8 MiB of resolver root and hashes at most the bounded
caller summary. It adds no write, child, lock class, retained cache, cache
invalidation, or ordinary-mode handler/state. At five-second cadence a
four-hour phase can start at most 2,881 attempts.

The prospective runner must combine T with the digest-bound phase-transition
table in the compact inventory. Relative to the prior accepted F: cold is
initial; warm, stale, restart, pressure, lifecycle, and product phases retain
both relationship and caller pairs; physical B and return A replace both pairs;
logical B replaces only the relationship pair; after the archive comparison R
oracle completes, archive permits the relationship pair to remain or be wholly
replaced while caller remains equal. Mixed generation/root changes refuse. The
logical old-A counterexample proves the transition predicate rejects old
logical A. The table itself participates
in the inspection-inventory digest.

With T, the five epoch accounted-request maxima are
`11,529/5,763/11,526/6,254/8,689`; each ready T is exactly four C plus four S,
and every attempt is budgeted at that at-most-four/four ceiling. Focused normal/race, supersession-versus-corruption, typed
resolver identity, transition, inventory, contract, and whitespace checks pass.
Production-shaped review then removed an invalid cross-scope upstream-digest
comparison, counted candidate execution partitions instead of distinct backing
members while retaining one physical decode charge per execution, and restored
the exact frozen structural Go sentinel. A production-policy fixture preflight
now checks every frozen domain count before server launch. The source-equivalent
production/exercised-regression path passed in 465.56 seconds: ready T was `4/4/0/0`, cold F
was `10,763/126/415,836/0`, warm F was `10,761/90/251,021/0`, and the
candidate/relationship/caller cache-miss tuple was `0/1/0`. Deterministic
late-selector supersession refused stale success, stopped exact mode nonzero,
and bounded cleanup left no helper or Surreal process. The future runner still
owns phase-table execution, R/Q accounting, replacement phase caps, the final
ordinal, a superseding plan, freeze, and execution.

#### F/Q exact native-accounting closure

F's candidate strict-open term includes two complete repository/caller input
walks, every input run consumed by the 512-record binary-carry and final-fold
merges, the finished projection scan, and any local-projection records. Exact
whole-repository F admits no local projections. Therefore
`P=4*candidate.MaxCorpusEntries+B512(2*candidate.MaxCorpusEntries)
=353,296,640`, and the corrected no-slack F member maximum is `589,656,064`.

Q keeps exact `C/S/W=160/164/0`. Its M evidence is the checked plan-order sum
of each query result's HTTP and MCP `member_reads`; it is not a plan-authored
fixture scalar. The production-shaped regression separately derives each
request's exact M from the current catalog member containing the selected
service and the distinct current relationship projection and RPC/Kafka
component members touched by that page. The first relationship page also
charges its selected service member. This bar binds those independent reads to
the same immutable roots used by the request.

Repository identity is deliberately part of declaration lineage. It reaches
resolver records, RPC postings, relationship projection digests, and the first
digest byte that selects one of 256 repository members. Fresh repositories with
equal commits can therefore redistribute the same projections and change the
physical member counts charged by Q; co-resident RPC fragments can also change
a Kafka page's member cost. A literal observed Q-M total is invalid, while the
root-bound page oracle remains exact. This is expected physical packing, not
cache drift or a product defect.

The exact-tree two-server witness must build publication custody, restart on
that custody, pass cold F, all 38 plan-order HTTP/MCP reports, warm F, and a
deterministic late-selector conflict, then stop exact mode nonzero and leave no
helper or Surreal child. This closes F/Q native-accounting validation only. R,
cumulative phase bounds, the final ordinal, replacement freeze, execution,
release, and scale/SLO claims remain open.

#### Physical replacement R correction

The production search root retains exactly two durable roles after A to B:
B is `current` and A is `prior`. Lifecycle skips both roles, so releasing a
separate A reader pin cannot delete A. V2 therefore replaces its impossible
delete-A oracle with two exact drained lifecycle turns, each scanning and
deleting zero, and reprobes A through the same retained exact reader after the
pin release. V1 receipt bytes and its historical predicate remain unchanged.

The exact operation order is pin A, publish B once, bind B/current and A/prior,
run the held lifecycle turn, synchronously open and query A and B, release only
the A generation pin, run the second lifecycle turn, and reprobe A before
closing its reader. Opening A before B is invalid: B publication legitimately
changes A's hard-link ctime, and the exact reader's file-identity fence must
reject that mutation. No second physical build or fabricated legacy
publication state is admitted.

Physical R is one operation report with
`C/S/M/W=41/0/(2*combined_physical_owners)/0`; the frozen member subtotal is
4,063,208. Each exact reader contributes C17 and N decoded source-owner visits,
the settled current/prior root contributes C1, and the two lifecycle turns
contribute C3 each. The dedicated R inventory records this subtotal, and the
first epoch's shared report-ordinal inventory rises from 11,529 to 11,530.
Other non-`none` R classes remain nullable and open rather than appearing as
zero. The generic physical phase read caps remain unchanged because adding R
alone would mask still-open inspector work. Whole-phase cross-scope sums, the final ordinal, replacement freeze,
runner execution, release, and scale/SLO claims remain open.

Each exact open performs two complete shard-byte hash passes: whole-publication
validation and static-shard materialization. The A/B pair therefore performs
four shard passes and temporarily retains two mmap-backed reader sets, each
bounded by the existing 256-shard generation cap. Construction is capped at
ten minutes and holds one of two process-wide exact-reader sessions until
`Close`; the one frozen R report runs serially, avoiding two transitions each
holding one session while waiting for the other. A query holds only its reader
mutex and fences the complete retained file-identity set before and after the
bounded search; the static searcher also performs its existing per-shard
metadata checks. Those metadata probes and byte hashes are not C/S/M/W events;
the decoded source-owner validations are the recorded M visits. No ordinary
query, startup, sync, retry, publication, lifecycle, cache, store, child, or
persistent-state path calls the exact reader.

#### Logical activation R accounting

Logical B now has an exact transition-local witness rather than borrowing F's
settled activation proof. The default-inactive controller hook recognizes only
the fresh attempt-0 offset-9 activation commit. It runs before the handler
returns, so the target unit remains leased and the schedule's one repository
token prevents a next claim. Replays have zero newly read rows and do not
re-report the hit.

The hit snapshot binds the prior selector, its unchanged physical search
generation, a running plan at `next_chunk=10`, a fully materialized active
schedule with nine succeeded/one running, and the leased target unit. The
recovered snapshot binds the final selector and the
same immutable plan/schedule/unit identities with an activated plan, settled
all-success schedule, and a done, unleased, released/reclaimed stale-priority
attempt-0 target with internal failure provenance. Both snapshots reject
malformed worker, lease, defer, claim/heartbeat/finish ordering, and retained
error shapes. The real-store regression runs an A-to-B logical catalog
transition over the actual target member, proves the token exclusion and that
the real controller callback sees the changed member only after commit under
the transition lock, and proves a zero-row same-lease replay cannot report. It
refuses the released target as a hit and refuses the real
intermediate state where the plan and selector are final but the scheduler's
last lease is still active.

V2 requires exactly one same-attempt stale-priority requeue for that stop;
V1 keeps its frozen zero. The final activation-authority pass accepts either a
clean attempt-0 completion or this exact stale-success residue, and rejects
mixed priority/error states.

Each snapshot reads the selector, exact plan, immutable schedule, exact unit,
and selector confirmation once. Logical R is therefore two reports with
report-scoped `C/S/M/W=0/10/0/0`; controls are not decoded application-member
visits. Epoch two's shared report inventory is 5,765. The forced V2 stop
separately pays existing pipeline recovery outside that subtotal: one scheduler
release transaction, one bounded claim-candidate read plus one later claim
transaction, one replay plan point read returning zero member rows and changes,
one completion transaction, and the controller's existing settled advance/no-op
handoff. The replay also creates the existing per-claim heartbeat goroutine,
ticker, and channel and emits configured lifecycle reports; its plan-point work
normally finishes before a heartbeat write and adds no concurrency class.
Limit-plus-one refusal occurs before the unavailable query, ordinary callers
attach no ledger, and the reader performs no write, poll, cache, file read,
child, or persistent mutation.
The default hook adds only a nil branch to ordinary nonterminal service-state
handling. When exact control installs it, synchronous bounded read/report I/O
extends the existing shared filesystem-mutation lock, controller mutex, target
lease, and repository-token hold; the callback receives the operation context
and owns timeout/report failure after the already-durable commit. It does not
retry the report or transaction; the later scheduler replay is idempotent and
cannot re-report.

Return, stale-lease, restart, pressure, archive/restore, and lifecycle R remain
nullable and open. Cumulative phase accounting, final ordinal, replacement
freeze, runner execution, release, and scale/SLO claims remain open.

#### Return-A relationship marker-recovery R accounting

Return A now has an exact transition-local relationship witness instead of
equating the marker hit with either complete logical B or the later final
return-A authority. Exact control's synchronous hit callback runs only after a
canonical `publishing.json` marker owns the complete target generation and the
stage rename/removal plus repository-directory sync has completed, but before
the relationship `current.json` pointer moves. The hit reader reads the current
pointer, marker, exact target root, pointer confirmation, and marker
confirmation. It accepts only an unchanged logical-B pointer, byte-identical
canonical marker, and target root matching the marker pointer. The marker's
nonempty named stage and `publishing.json.tmp` must both be absent. Those
zero-charged metadata checks prevent an already-existing target from making the
earlier marker-plus-stage window look like the frozen boundary.

On restart, `RecoverV3` invokes the recovered callback only after it installs
the marker pointer, removes the temporary and marker, and completes the final
repository-directory sync. The same five-read shape then requires the
return-A pointer twice, the marker absent twice, the same exact target root,
and no temporary. Because recovery returns only after marker removal and the
directory sync, `ResidueAfter=0` is a durable statement. Symlinks, wrong file
types, noncanonical bytes, mismatched roots, unexpected residue, or ambiguous
I/O refuse the report.

The reader deliberately performs no selected-runtime query. The selector is
still logical B at both the hit and startup-recovery callbacks; only a later
controller advance selects return A. It also reads no caller, application
member, or store row. Instead, V2's hit authority uses the existing source-free
authority identity over the exact mixed current projection: clone final
return-A authority and replace caller generation/root plus relationship
generation/root/provenance with their accepted logical-B values. The later F
still proves the complete selected return-A authority. Retained V1 hit
authority, target projection, validation, and bytes do not change.

The V2 injection target keeps the distinct production identities straight:
`GenerationSHA256` is the marker output generation,
`UnitSHA256` is its root digest, `PlanSHA256` is
`binding.TargetGeneration`, and `ScheduleSHA256` is
`chunk.ScheduleDigest`. The hook strict-validates the runtime binding and
requires `chunk.Generation == binding.ScheduleGeneration`; neither scheduler
generation nor schedule-row digest is the published relationship generation,
and the stable runtime target must not be replaced with the attempt-specific
binding digest.

One hit and one recovered report each perform exactly five control-file reads:
pointer, marker, root, pointer confirmation, and marker confirmation. The
return-A R subtotal is therefore two reports with
`C/S/M/W=10/0/0/0`, and epoch three's shared exact-report maximum is 11,528.
Metadata checks are zero-charge. The reader is synchronous, source-free,
one-shot, nonpolling, uncached, child-free, member-free, store-free, and
write-free.

At the hit, exact read/report work extends the existing relationship
filesystem-mutation lock, publication-transition mutex, claimed schedule lease,
and repository token. The deliberate stop or a report failure leaves the
already-durable marker-owned target intact, latches exact cancellation, and
terminates the process nonzero before pointer publication. Recovery reporting
extends the existing exclusive startup mutation hold. If that report fails,
the recovered pointer and cleared marker are already directory-synced: recovery
does not roll back, recreate residue, or retry, startup fails closed, and the
next startup cannot duplicate the report because no marker remains. Ordinary
publication and recovery pay only inactive nil-hook branches and add no ledger,
allocation, I/O, goroutine, lock class, or persistent state.

This reader does not supply a runner, whole-phase accounting, a final ordinal,
a replacement freeze, ceremony execution, release evidence, a scale pass, or
an SLO. Stale-lease, restart, pressure, archive/restore, and lifecycle R remain
open, and T42.2 remains next only after this and the remaining T42.1 gates
close.

#### Prepared stale-lease schedule/result R accounting

The stale-lease transition now owns an exact
`prepared-stale-lease-schedule-and-result` witness. Exact control installs the
generation scheduler's default-nil pre-heartbeat gate before the heartbeat
goroutine starts, allowing the selected attempt-0 chunk to cross the existing
stale cutoff without a refresh race. An optional store observer runs
immediately before and after the existing heartbeat-fenced durable stale
requeue. The pre-mutation callback emits the hit report. The post-mutation
callback only releases exact-control synchronization; it is not recovery. The
recovered report is emitted only after a worker reclaims that same attempt-0
row and completes it successfully.

Each report uses the same self-confirming source-free reader. It reads four
control files exactly once: the immutable recovery-preparation binding, exact
extraction generation, exact domain plan, and canonical partition result. It
then reads the current generation schedule and exact target chunk, and repeats
both store reads as confirmation. The hit requires the two schedule/chunk
pairs to agree on an active current schedule and a priority-0 attempt-0 row
that is running, claimed, leased, and stale under the reaper's observed
heartbeat. The recovered state requires a settled schedule with no pending,
running, or failed chunk, plus the same row at priority 2, done and unleased,
with bounded `stale worker lease reaped` provenance. Pending after requeue is
deliberately not recovered.

V2 maps the target directly to the prepared immutable work:
`GenerationSHA256` is the exact extraction generation,
`PlanSHA256` is its domain plan, `ScheduleSHA256` is the preparation's recovery
schedule, and `UnitSHA256` plus ordinal/kind/member/source bounds identify the
canonical partition result. The recovery-schedule generation is a separate
prepared identity; its canonical binding must map it to that exact target
generation and predecessor schedule. The plan and result are then read from
the target generation. These identities cannot be aliased, and the legacy
root-level schedule field cannot stand in for the recovery schedule. The stale
phase authority must be byte-identical to accepted return-A authority at both
reports. Retained V1 mapping, authority, validation, and bytes remain exact.

The hit and recovered report each cost exactly
`C/S/M/W=4/4/0/0`, for stale-lease subtotal `8/8/0/0`. Epoch three's shared
exact-report maximum is therefore 11,530. Limit-plus-one refusal occurs before
the unavailable read. The reader is synchronous, one-shot, nonpolling,
uncached, child-free, member-free, and write-free. It decodes only those four
bounded controls and metadata-checks one generation plus at most 64 domain
directories. A successful report executes exactly four one-row store queries;
any retry consumes the fixed ledger and refuses. Exact mode deliberately
extends the stale window and runs bounded callbacks on the existing scheduler,
reaper, and completion paths; its recovered callback adds one bounded timeout
context/timer after completion. Ordinary work pays only inactive nil branches;
there is no global hook, store-schema change, ledger, persistent state,
additional goroutine, lock class, or production I/O.

This slice does not supply the runner, whole-phase sums, final ordinal,
corrected-plan freeze, ceremony execution, release evidence, a scale pass, or
an SLO. Restart, pressure, archive/restore, and lifecycle R remain open. T42.2
remains unauthorized until the remaining T42.1 gates close.

#### Prepared checkpoint hard-restart R accounting

The V2 process-restart transition now owns an exact
`prepared-checkpoint-hard-restart` witness. Exact control installs a
default-nil runtime hook after the existing worker has selected and validated
the canonical result for reuse, but before domain assembly. The old server in
epoch three emits the checkpoint hit. One later stale requeue is synchronization
only and emits no R report. The reaper's preceding store-owned Hit event is also
private synchronization, not evidence. The recovered report belongs to the
distinct new server in epoch four and remains unavailable until the same
reclaimed chunk has completed successfully. This slice supplies that production
hook/reader and prospective receipt validation; it does not supply the
controller, runner, or hard kill that will drive the boundary.

Each report uses the same self-confirming, source-free read. It opens exactly
seven controls: the immutable recovery-preparation binding, exact target
generation, target domain plan, canonical partition result, completion control,
exact root attempt, and current pointer. A current-schedule read plus exact-chunk
read runs before those controls, and the same two store reads run again as a
confirmation fence. An absent root or current pointer at the hit is still one
charged read attempt. Independent metadata probes remain zero-cost.

V2 maps `GenerationSHA256` to the immutable target extraction generation,
`PlanSHA256` to its domain plan, `ScheduleSHA256` to the preparation's recovery
schedule, and `UnitSHA256` plus ordinal/kind/member/source bounds to the exact
canonical result. The recovery-schedule generation is a distinct prepared
identity whose canonical binding maps to that target generation and its
predecessor schedule. The receipt derives the exact scheduler-row identity from
that schedule, the target's generation-global partition offset, and attempt 0.
None of those identity roles may be aliased.

At the hit, the schedule is active and the selected attempt-0 row is priority 0,
running, claimed, and leased. The canonical result already exists durably, the
completion file exists with exactly that result's bit clear, and the exact root
and current pointer are absent. A worker may renew its heartbeat while the
reader runs, so the checkpoint fingerprint deliberately excludes mutable
`HeartbeatAt`; it still binds every fixed row field and an opaque digest of the
private lease token. Any other row or schedule movement refuses the snapshot.

Recovery requires a settled all-success schedule and the same attempt-0 chunk
at priority 2, done and unleased, with no retry successor and its final row token
cleared. The new-epoch controller compares the opaque killed-token fingerprint
carried by the reaper's private Hit event with the recovered scheduler
callback's new-claim fingerprint. They must differ. The prepared checkpoint hit
may keep its fingerprint private in-process; no raw token or private reaper
event enters the receipt or R subtotal. The canonical result bytes are
identical, the completion file is complete with the selected bit restored, the
exact root contains that result, and the current pointer names that target
generation, plan, and root. The old and new Phebs process identities are
distinct, while the prepared target mapping and complete protected return-A
authority are byte-identical before and after. Requeue alone, a new attempt, a
replacement result, mixed authority, incomplete assembly, or an aliased
process/lease identity cannot satisfy recovery.

The hit and recovered reports each cost exactly
`C/S/M/W=7/4/0/0`, for process-restart subtotal `14/8/0/0`. Because the reports
straddle the restart, the hit increases epoch three's request maximum from
11,530 to 11,531 and its transition `calls/C/S` from `4/18/8` to `5/25/12`.
Recovery increases epoch four from 6,254 to 6,255 requests and transition
`calls/C/S` from `0/0/0` to `1/7/4`. V1 keeps its historical process-restart
semantics and bytes and gains no subtotal. The plan policy encodes the new shape
as `p2C7S4`. The spelling change from `metadata=0` to `meta=0` only compacts the
same zero-cost metadata rule to preserve the existing plan-byte ceiling.

The read is synchronous, one-shot, nonpolling, uncached, child-free,
member-free, and write-free. It decodes only the seven bounded controls,
metadata-checks one generation plus at most 64 domain directories, and executes
exactly four one-row store queries. Each report acquires the existing per-plan
assembly mutex once through the existing context-bounded acquisition around
completion/root/current inspection, then releases it before the second store
confirmation and before any report or wait. It creates no retained cache, state,
or lock class. A limit, malformed control, store drift,
identity mismatch, hook error,
reaper-synchronization error, or report error fails exact mode closed and
invents no report. Hit failure returns before assembly, leaving the durable
prepared checkpoint for explicit recovery. A recovered-report failure occurs
only after the completion transaction and exact root/current installation are
durable; it cannot roll them back and is surfaced through the scheduler report
path. The recovered callback uses the existing bounded post-completion timeout
context/timer. Ordinary reused-result handling adds one nil branch and no global
hook, store schema, persistent state, goroutine, lock class, cache, retry, lock
acquisition, or I/O.

This slice does not provide actual controller/runner hard death, whole-phase
sums, a final ordinal, corrected-plan freeze, ceremony execution, release
evidence, a scale pass, or an SLO. Pressure, archive/restore, and lifecycle R
remain open, and T42.2 remains unauthorized until the remaining T42.1 gates
close.

#### V2 production lifecycle-owner inventory

Pressure and lifecycle evidence must cover the production rotation, not the
older T40 list. The T42 base already registers `catalog-v3-generations` and
`relationship-v3-namespaces`, so V2 now requires those rows alongside the
historical fourteen. Both are exact and drained; `durable-jobs` remains the
only lower-bound row. V1 construction and retained bytes stay unchanged.

The two names add 68 canonical plan bytes. Rewriting only the versioned
physical/logical R policy into the same explicit compact grammar saves 108,
leaving the plan at 262,101 of 262,144 bytes. The lifecycle transition class is
now `fresh-sixteen-owner-cycle`, and either missing V3 row fails the common
receipt validator. No production status collector, lifecycle work, storage,
I/O, lock, cache, child, cadence, or safety ceiling changes. Pressure readers,
archive/restore, final lifecycle R, whole-phase accounting, freeze, and
execution remain open.

#### Pressure-80 lifecycle R reader

V2 pressure-80 R now owns exactly two source-free, zero-native-read
observations. An exact-only one-shot collector consumes the existing serial
`lifecycle.Run` owner and capacity callbacks after a phase-local fence. It
cumulatively admits at most 4,096 turns and retains only scalar work totals,
the final sixteen owner rows, and final capacity. Success requires one complete
sorted cycle with every row `state=ok` and drained: the fifteen non-job owners
are exact, `durable-jobs` is lower-bound with backlog false, and every paired
capacity callback is exact-normal, including the final callback. Ownerless,
unpaired, malformed, out-of-order, overflow, and limit callbacks fail closed.
It does not use `StatusMonitor`, start a second scanner, retain source or
persistent state, or run a lifecycle turn.

After the later controller mutates ballast and records its fence, the second
reader calls the existing `Gate.Check(ctx, 0)` once. It requires exact collect
pressure at 80%, unchanged total bytes, increased used bytes, and decreased
available bytes, without waiting for another lifecycle turn. Callback order
is authoritative, so V2 admits nondecreasing Unix-millisecond projections;
retained V1 remains strict, and neither path invents timestamps.

Both reports are exactly `C/S/M/W=0/0/0/0`. Epoch-four transition calls rise
from 1 to 3, epoch-four requests from 6,255 to 6,257, and the five-epoch total
from 43,770 to 43,772. The compact policy token is
`;80=2xC0S0M0W0`, leaving the plan at 262,115 of 262,144 bytes. Ordinary mode
is unchanged. Only later exact mode constructs the collector; its bounded work
is one mutex, one capacity-one result channel, bounded sixteen-row copies,
and one direct capacity metadata probe. It adds no I/O, cache, child, schema,
persistent state, source retention, or lock nesting.

This slice does not wire the server. The later controller/runner must reject
disabled lifecycle, arm and wake the existing potentially hour-idle runner,
and own ballast and authority fences, event ordinals, and report delivery.
Deletion and prepared-residue proof; pressure-90, pressure-75, archive, and
lifecycle R; cross-phase sums; final ordinal; corrected freeze; execution;
release; and scale/SLO evidence remain open.

## Cost and nonclaims

The production correction is confined to the existing partition executor. A
partitioned caller invocation returns no repository-wide attribution projection;
the global direct-resolver path remains its owner. Completion reuses the
verified corpus's existing `Required` read ledger instead of comparing reads to
all admitted records. A zero-total partition now returns the canonical empty
result without hashing an empty chunk list. Partitioned caller work removes the
invalid repository-wide attribution load. Every attribution request adds one
constant immutable partition-scope branch. A partition request additionally
checks cancellation and takes one short snapshot under the existing corpus
mutex before returning no repository-wide source; full-source work is otherwise
unchanged. This adds no lock class or persistent work. Startup, restart,
requests, queries, sync ticks, retries/no-ops, publication transitions,
lifecycle turns, caches, stores, children, limits, and schemas gain no work.

Offline plan construction holds the existing 10,000-service/60,000-membership
T41 catalog, a 31,602-entry path map, and bounded digest/claim maps. The corpus
path transforms the 31,600-file overlay once; the independent oracle instead
enumerates closed logical and relationship records. Construction creates one
10,000-record control snapshot, samples 512 reused T40 content classes,
enumerates 10,999 RPC plus 9,500 Kafka semantic pairs through independent
paths, inventories 31,602 addition records, and streams the 2,000,002-record
structural inventory twice to compute the exact A and B combined tree
identities. The reader probe stops after the first changed structural record
in each revision.
Plan construction authors no two-million-file working tree and performs no
corpus Git read, SurrealDB work, network access, or Cartesian source
materialization. The canonical CLI separately runs the existing bounded
clean-HEAD Git metadata checks before authoring.

The merge-bar production replay authors a temporary candidate-equivalent
31,604-file bare repository, builds and reopens candidate and sparse authority,
executes all 56 production partitions, materializes the resolver catalog from
exactly 10,002 bounded blob reads, executes and installs sixteen protocol/leaf
pairs with exactly 11,601 further source reads, and publishes and reopens one
complete caller generation. Those reads use the existing one-shot
`git cat-file blob` path, so the replay launches about 21,603 sequential Git
children plus its candidate, fixture, and batch Git commands; observed runs
took about 381--384 seconds. It is bounded by a 15-minute test context, uses an
in-memory evidence store, and removes its temporary Git and artifact custody
through the test harness. The fixture omits the 1,999,999 structural `.txt`
owners that match no production candidate policy, so this test-only replay is
not a second complete physical-corpus scan or a scale execution claim and adds
no ordinary steady-state child.

This plan establishes no combined execution pass, target SLO, supported
customer scale, accuracy/completeness, commit cadence, queue catch-up,
freshness-under-cadence, migration/decommission, topology, private replay,
release, or `GATE2-V2` result. T42.2 requires separate authorization and
remains unexecuted.
