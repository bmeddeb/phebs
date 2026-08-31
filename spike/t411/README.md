# T41.1 production-aligned service-load profiles

T41.1 freezes source-free logical-service inputs for the later Epic 41
implementation. It does not register catalog v3, change a production limit,
start SurrealDB, or authorize the neutral closure gate.

## Decision

The accepted-service floor is 8,000, the target is 10,000, and the
accepted-only comparator is 12,500. The comparator fits the reduce-only
aggregate envelope, so the prospective hard limits are:

| Dimension | Limit |
|---|---:|
| Total service records, independent of disposition | 12,500 |
| Memberships | 75,000 |
| Distinct membership or unowned paths | 40,000 |
| Aggregate successor edges | 12,500 |
| Successors on one service | 512 |
| Logical canonical bytes | 16 MiB |
| Encoded catalog root and members | 32 MiB |
| Total claims on one placement | 4,000 |

Accepted fan-out remains 20. Existing service-key, display-name, path,
per-service path-count/path-byte, role, disposition, and typed-requires-
supporting rules do not increase.

The maximum two-sided relationship projection with one 4,096-byte path, 4,000
128-byte service keys, and all five roles per claim is 3,052,846 bytes. It
cannot fit the existing 1-MiB projection wire. T41.1 therefore selects the
planned `placement-claim-buckets-v1` representation: at most 512 claims per
bucket and eight buckets for the 4,000-claim boundary. The measured maximum
two-sided bucket is 408,942 bytes. T41.8 owns the real versioned schema,
aggregate identity, publication, recovery, and lifecycle behavior; this spike
does not alter the existing relationship wire.

## Frozen profiles

`envelope.json` retains aggregate recipes and identities rather than a giant
generated tree:

| Accepted services | Memberships | Distinct paths/files | Logical catalog | Encoded root/members | Members |
|---:|---:|---:|---:|---:|---:|
| 8,000 | 48,000 | 25,280 | 6,060,113 B | 12,320,318 B | 29 |
| 10,000 | 60,000 | 31,600 | 7,575,094 B | 15,400,231 B | 36 |
| 12,500 | 75,000 | 39,500 | 9,468,819 B | 19,250,171 B | 45 |

Each accepted service has six explicit memberships: one primary, one shared,
two generated, and exact supporting plus typed claims on the same contract
path. Shared paths group at most 20 services and grouped generated paths at
most 10. One deterministic unowned file exists per 100 services. Authority is
operator-explicit and never inferred from a generated directory, import, or
relationship.

The separate small transition profile covers proposal-to-accepted, persistent
conflict, rejected successor, omission tombstone, re-add at incarnation two,
and A→B→A. Its three catalogs pass the real `phebs-service-catalog-v2`
validator; those semantic cases do not alter accepted-only cardinality.

The 12,500-service comparator's largest ordinary service member is 387,721
bytes, largest ordinary placement member is 1,550,808 bytes, and root is 9,193
bytes. The combined maximum service shape (128 paths/64 KiB, 512 successors,
maximum key/display/reason) is 158,158 bytes. The maximum 4,000-claim placement
member is 1,528,421 bytes. All remain below the prospective 2-MiB member bound;
the root remains below 256 KiB and the total member count remains below 64.

`receipt.json` binds the preserved T32.3 receipt and Git 2.54 bundle, exact
serialization/projection/filesystem/lifecycle byte kinds, estimated immutable
store rows and pointer transaction, build wall/allocation, and the author
process's `getrusage` high-water RSS sampled after receipt validation and
before final artifact marshaling and writes. Store and lifecycle numbers are
design estimates for later tickets, not executed SurrealDB evidence.

## Reproduction

From the repository root:

```sh
T411_OUTPUT="$(mktemp -d)"
go run ./spike/t411/cmd/author "$T411_OUTPUT"
go test ./spike/t411 -count=1
```

Two envelope builds must be byte-identical. Normal tests rebuild only the
aggregate recipes and bounded in-memory projections; they never retain the
generated 39,500-file tree. Re-authoring changes host wall/allocation/RSS
observations and therefore requires review before explicitly running
`go run ./spike/t411/cmd/author spike/t411` to replace the pinned evidence.

The package is development-only. Production startup, restart, requests,
queries, sync ticks, retries/no-ops, publication transitions, caches, locks,
workers, stores, source reads, Git children, and lifecycle turns perform no
T41.1 work. The profiles establish no supported service count, target SLO,
accuracy/completeness, migration/decommission result, release posture, large-
repository result, topology decision, freeze, or ceremony authorization. The
separate 2026-08-30 program decision advances the backlog to T41.2 without
changing any of those evidence nonclaims.
