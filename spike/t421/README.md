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
- one existence-hiding unauthorized-repository result with no repository or
  generation read;
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
reads. Visible cases require at least one read and cannot exceed the matching
`product_queries` phase-work maximum; the existence-hiding denial is exact
zero/zero. Overflow-safe sums across both transports must fit the measured
`product_queries` phase totals.

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
