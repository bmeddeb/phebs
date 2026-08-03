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

These directories preserve executable gates, locked inputs, synthetic
fixtures, and decision tables used by their completed tickets. They may be
maintained when a reproducibility defect is found, but they do not become
current behavior documentation and do not inherit T11.1’s sealed status.
Production packages must not import spike packages.

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
entries—163,840 aggregate—with 256-name directory batches, 2,048 charged stats,
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

### Deterministic product fixtures

- [Change Workbench closure fixture](./fixtures/change-workbench/README.md)
- [Investigation envelope fixtures](./fixtures/investigations/README.md)
- [Thrift field-reference fixture](./fixtures/thrift-field/README.md)

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
