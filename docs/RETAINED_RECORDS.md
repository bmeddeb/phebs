# Retained engineering records

These files explain how decisions were reached or provide deterministic test
inputs. They are not current user instructions, active roadmap items, or
product claims.

## Record classes

### Sealed validation evidence

- [T11.1 / GATE2-V2 report](../spike/t111/REPORT.md)
- [T11.1 sealed record index](../spike/t111/labeling/README.md)

The complete tracked `spike/t111/` tree is sealed. Do not rewrite, relocate, or
delete it. `make docs-check` pins its tree digest. A later document may cite
the terminal `NOT_ESTABLISHED` result but cannot reinterpret it as accuracy
evidence.

### Retained validation and design spikes

- [T19.1 Thrift protocol-pack spike](../spike/t191/README.md)
- [T20.1 monorepo correctness/scale spike](../spike/t201/README.md)
- [T21.1 Workbench inventory and vocabulary contract](../spike/t211/README.md)
- [T22.1 Thrift field-reference spike](../spike/t221/README.md)
- [T23.1 Kafka topic-evidence spike](../spike/t231/README.md)
- [T30.1 focused-index and shard-set spike](../spike/t301/README.md)
- [T30.6m historical-publication retention decision and owner inventory](../spike/t306m/README.md)
- [T32.2 authorized whole-monorepo baseline protocol](../spike/t322/README.md)
  and [source-free receipt](../spike/t322/results.json)
- [T32.3 neutral service-authority/correctness corpus](../spike/t323/README.md)
  and [artifact receipt](../spike/t323/receipt.json)
- [T32.4 search-topology and cost spike](../spike/t324/README.md)
  and [source-free measurement receipt](../spike/t324/results.json)
- [T34.1 repository source/search generation gates](../spike/t341/README.md)
- [T34.2 exact service-query compiler gate](../spike/t342/README.md)
- [Source-free 2026-08-06 large-monorepo diagnostic](../spike/large-monorepo-20260806/REPORT.md)
- [T40.1 closed scale-refusal and neutral-envelope record](../spike/t401/README.md),
  including the [frozen envelope](../spike/t401/envelope.json),
  [blob-reader comparison](../spike/t401/comparison.json), and source-free
  [paired-build receipt](../spike/t401/reproducibility.json),
  [structural](../spike/t401/structural/manifest.json) and
  [semantic](../spike/t401/semantic/manifest.json) authoring records
- [T42.1 combined-gate freeze contract](../spike/t421/README.md) and canonical
  [source-free plan](../spike/t421/plan.json). The plan is not an exact-main
  execution freeze and grants no T42.2 execution or release authority.

These directories preserve executable gates, locked inputs, synthetic
fixtures, and decision tables used by their completed tickets. They may be
maintained when a reproducibility defect is found, but they do not become
current behavior documentation and do not inherit T11.1’s sealed status.
Production packages must not import spike packages.

The T32.2 directory retains the strict source-free receipt builder,
invalid-until-completed templates, tests, private-run protocol, and the
completed `results.json` from the 2026-08-04 authorized run. The receipt
contains no target identity, source, query, path, host identity/profile,
credential, or raw error. It grants no topology or scale decision and
establishes no SLO.

The T32.3 directory retains a five-revision neutral Git bundle, closed catalog
inputs, independent exact membership/search/RPC/topic/currentness/tombstone
oracles, and complete synthetic 1,000/5,000-service load profiles. Its
authority-input schema is fixture vocabulary rather than a production catalog
contract. Deterministic authoring tests pin every Git tree and artifact byte;
the record selects no topology, target SLO, extraction accuracy, or production
registration.

The T32.4 directory binds the completed T32.2 receipt and T32.3 corpus, replays
the five-revision independent search oracle through a real same-module zoekt
child, and measures the complete 1,000/5,000-service synthetic profiles. Its
closed receipt selects direct whole-repository shards for the initial v2 path;
cohorts and P6 remain trigger-gated. The ctags-disabled synthetic timings set
no target SLO, scale limit, extraction accuracy, or release authority.

The T34.1 directory runs the production source-generation census over the
frozen T32.3 1,000-service profile and proves that its 3,151 files remain 3,151
physical owners despite 5,000 logical memberships. Its second gate revalidates
the T32.4 digest binding to T32.2's completed source-free target receipt and
the retained direct-topology decision without reading private input. It adds
no target timing, scale, accuracy, or release claim.

The T34.2 directory applies the production compiler to all 18 exact/stale
service-query expectations in T32.3's five-revision oracle through an
in-process zoekt reader. It separately proves that one-result/one-match
truncation cannot be consumed by 100 matching out-of-service files because
the path predicate is inside the query. It retains no generated index or
measurement and adds no target timing, scale, accuracy, migration, or release
claim.

The 2026-08-06 large-monorepo diagnostic reduces one unfrozen private runtime
log to source-free counts, timings, limits, lifecycle transitions, and closed
failure classes. Private names, paths, filenames, revisions, digests, job
identities, raw errors, and the high-entropy per-shard vector remain outside
the repository. The record establishes mechanical behavior only: it is not a
validation receipt, supported-scale result, SLO, topology decision, accuracy
or completeness result, T39.R1 evaluation, or release authority.

The T40.1 directory retains the deterministic profile author, independent
oracles, semantic parity gates, strict source-free schemas, and seven explicit
artifacts. Their SHA-256 digests are:

| Artifact | SHA-256 |
| --- | --- |
| `envelope.json` | `92cce848e6e42942c24e2fa066968571fb5693252b7b41b7a91c889881fe7f94` |
| `comparison.json` | `3527bec297c80c71b6c5081b1b386d25efc9ec8894643f599c7c57848be3b402` |
| `reproducibility.json` | `b7b0491af659007eb8e903279ca63c6f8178878a8af114a9af0cd407e52ccb1a` |
| `structural/manifest.json` | `4ae92b8efa58d459fe8fa10ba23c5cedad3adc7b2dddbd7618ea8d96c306604b` |
| `structural/receipt.json` | `bd80bef34f61f35c2f701d0877d4c013ec3c7d0ce62ec3756b32b7a4f103b2c2` |
| `semantic/manifest.json` | `ca4925f3ca3ddad42955e5c3dc0e9b5610e7fa8ac4ce3e614a9ad091e23362a8` |
| `semantic/receipt.json` | `e096b17faccd3ace38f0272234bc7fdfff97b0dfb1ccd23fa388e888d966d6d3` |

The structural profile freezes 2,000,000 eligible Go paths plus two controls
and more than 8 GiB of declared placement bytes; the semantic profile freezes
262,144 distinct Go blobs plus 32,768 IDL inputs and an explicit current-limit
observation refusal. The comparison binds one small structural projection to
the same verified zoekt binary: indexed content and ordered per-query returned-
file/content projections are equal, raw shard bytes differ, and missing/corrupt-
object outcomes are recorded without selecting the cat-file candidate. Author
receipts report bare-Git logical and filesystem-allocated bytes as
environment-bound observations,
separate from deterministic semantic identity. The external scratch
repositories and process artifacts were destroyed after validation.

These records freeze input shape, expected admission/refusal posture, and
mechanical parity only. They are not a two-million-owner phebs convergence
run, supported-scale result, target SLO, exact peak-resource measurement,
accuracy/completeness evidence, freshness result, topology decision, or
release authority. T40.1 changes no production bound or blob-reader selection.

The corrected T30.6m record separates its selected unbounded-retention posture
for historical evidence and adjacent candidate/caller residue from the mixed
evidence-pin lifecycles, the unchanged configured proof-bundle lifecycle,
other unchanged owner lifecycles, and incidental growth in the eight durable
job tables and exact 24-table Investigation/Workbench domain. It authorizes no
cleanup. T30.6n owns bounded
job reads and startup-migration repair. T30.6o owns the authorization-first
status shell, fixed 52-component registry, zero-scan unavailable projection,
and warning. T30.6p now owns 21 core SurrealDB components as bounded aggregate
per-table or per-pin-namespace row totals; lifecycle/status classifications are
neither computed nor separate v1 wire fields. The API uses the fixed allocations
at registry indices 0–17 and 48–50 for at most 1,677 scanned identities, while
the store accepts any report allocation from 1 through 79 only with scan equal
to report plus one and enforces the unchanged 1,656/1,677 aggregate ceilings. It
reports only attributable logical outcome-receipt, canonical proof-content, and
canonical caller-receipt bytes. At the T30.6p boundary, physical database
attribution, both data-volume metrics, and the other 31 components remained
unavailable. Each authorized request produces 21 bounded
component summaries using at most 23 row-range queries after four cached
writer/migration-marker point checks and one pin-index catalog check. Every
one-statement query requires exactly one result envelope; a failed component
remains unavailable without hiding successful siblings and emits one log event
from the closed operational class set—`not_ready` or `query_error`—at most 21
per request. The existing schema batch adds a scalar string definition for
`evidence_pin.kind` and reuses the existing kind index; it adds no row backfill,
writer-generation bump, new
query index, sync-tick work, or writer work. T30.6q now owns the exact 24
Investigation/Workbench tables. Registry indices 18–39 retain 79 report slots
plus one sentinel and the two Watch components at 40–41 retain 78 plus one:
1,894 reported and at most 1,918 scanned identities. One catalog preflight plus
at most 24 direct bounded record-ID scans costs at most 25 queries, retains at
most 80 selected IDs for the active table, returns at most the 24 fixed
allowlisted catalog names, and adds no index or backfill.
Missing tables and read failures remain localized, and successful summaries
are weakly consistent. T30.6p plus T30.6q populate 45 components within
3,550-report/3,595-scan and 53-query ceilings. T30.6r now owns the final seven
components and completes all 52: four bounded authority selections accept at
most 312 reported/316 scanned store rows behind one catalog query, three
readiness point checks, four direct row reads, and one batched caller
current-authority fence (nine client calls, or 62 across T30.6p+q+r). The fence
performs at most 312 bounded server-internal point reads—four for each of at
most 78 caller authorities—plus its marker check. Its
metadata-only incremental filesystem inventory observes at most 32,768
candidate, 32,768 focused, 32,768 resolver, and 65,536 caller
entries—163,840 aggregate—with 256-name directory batches, 4,096 charged stats,
64 MiB of manifest metadata, 256 queued caller directories, and five
simultaneous structural descriptors: at most three collector-retained
handles plus up to two Go/platform directory-iterator duplicates or rooted
traversal internals. The metadata allowance is aggregate I/O rather than a heap
meter: serial caller parsing may retain 32 MiB of raw bytes beside a bounded
decoded pair structure. It reports stable managed residue/apparent bytes and
store-authorized resolver/caller canonical bytes independently.
The stat ceiling includes explicit descriptor-rooted `Lstat` checks,
conservative open-time `fstat` charges, and one conservative slot per name-batch
(`Readdirnames`) call for the Windows error-classification `File.Stat` fallback.
The 78-report/79-scan slots allocate the response envelope rather than promise
universal exactness. The 4,096-stat ceiling covers the regression-gated lean
maximum allocation; recognized residue, nested stages, or the independent
64-MiB metadata limit may still localize a lower-bound or unavailable metric.
Every returned raw name consumes the observation budget. Names are otherwise
names-only; only recognized names receive explicit descriptor-rooted `Lstat`
checks.
Resolver/caller canonical metrics require the supported rooted nonblocking
regular-file opener; platforms without it retain typed unavailable canonical
metrics while physical inventory continues. Separately, on operating systems
with the descriptor-bound filesystem-capacity primitive it also reports
installation total/available capacity; platforms without that primitive retain
typed unavailable capacity with a localized cause. Every physical-database byte
metric remains unavailable. Invalid roots and partial work remain
unavailable or lower-bound, never false exact zero. At most nine localized
T30.6r diagnostics extend the complete p+q+r operational-event ceiling from 45
to 54. Concurrent authorized requests independently multiply the per-request
query, identity, descriptor, stat, and metadata bounds; this surface adds no
separate cache or concurrency gate.
None of these tickets changes deletion, configuration, or owner lifecycle
semantics.

### Exploratory discussion records

- [Multi-unit large-monorepo exploration](./MULTI_UNIT_MONOREPO_EXPLORATION.md)

This record preserves the discussion that informed the now-selected direction
for thousands of logical services over shared repository search and
relationship generations. The architecture decision is the dated
microservice-first and T32.5 implementation-gate rows in
[PLAN.md](../PLAN.md); completed Epic 32 and dependency-ordered Epics 33–39 are
tracked in ROADMAP/BACKLOG. The retained discussion itself remains historical
and freezes no independent scale bound, accuracy result, or current behavior.

### Deterministic product fixtures

- [Change Workbench closure fixture](./fixtures/change-workbench/README.md)
- [Investigation envelope fixtures](./fixtures/investigations/README.md)
- [Thrift field-reference fixture](./fixtures/thrift-field/README.md)
- [T30.7 neutral focused-service cohort](./fixtures/t30.7-neutral-service/README.md)
- [T34.4 neutral All code/service-search cohort](./fixtures/t34.4-service-search/README.md)

Fixtures prove bounded software behavior. Synthetic or authored evidence is
not public-corpus accuracy or completeness evidence.

### Design handoff

- [Context Port brand and rail UI handoff](./design_handoff_phebs_brand_and_ui/README.md)
- [Design token notes](./design_handoff_phebs_brand_and_ui/notes/tokens.md)

The handoff records the origin of the current visual language. Its prototypes
and support files are references, not production code or an active UI
specification; current UI behavior belongs in the user guides and tests.

### Planning history

- [Completed ticket archive](./BACKLOG_COMPLETED.md)
- [Append-only architecture and decision ledger](../PLAN.md)

The active [roadmap](./ROADMAP.md) and [backlog](./BACKLOG.md) supersede old
sequencing statements without erasing their history.

## Preservation rules

- Keep sealed evidence byte-identical.
- Keep receipts, locks, digests, and fixture provenance with the artifact they
  bind.
- Do not quote a historical status as current product behavior; link the
  active authority.
- Do not turn a spike observation, synthetic fixture, or design prototype into
  a product, scale, or accuracy claim.
- Update this index when a new retained record class or entry point is added.
