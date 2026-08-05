# T32.4 — Search-topology and cost spike

> **Retained engineering record.** This directory selects the initial physical
> search topology for the multi-service program from source-free T32.2 evidence
> and T32.3's public oracle. It is not current user guidance, a production
> catalog contract, a target SLO, an accuracy result, or multi-service release
> authorization. Current sequencing remains in
> [`docs/ROADMAP.md`](../../docs/ROADMAP.md).

**Completed:** 2026-08-04 · **Status:** COMPLETE · **Production changes:** none

## Decision

The initial v2 topology is one direct whole-repository zoekt generation per
exact repository revision set. All code reads that generation directly. A
service query adds the complete catalog-derived filename predicate to the
zoekt query alongside its exact revision before ranking and top-K truncation;
it never filters a whole-repository result afterward.

| Alternative | Decision | Reason |
|---|---|---|
| Direct whole-repository shards | **GO to T32.5** | T32.2 completed its prospectively frozen direct envelope, and the neutral oracle is exact with in-query service predicates. |
| Bounded prefix-aware/hybrid cohorts | **NO-GO; trigger not met** | The preregistered direct baseline did not fail. Building cohorts now would add shared-file duplication and cross-cohort ranking/publication complexity without a measured need. |
| P6 fleet | **NO-GO; trigger not met** | No single-node capacity or correctness failure requires fleet escalation. P6 remains demand-driven. |

`NO-GO` here does not claim cohorts or P6 can never work. Either alternative
requires a new named direct-topology failure, a prospectively frozen
experiment, and the same all-code/service equality bar.

## Bound inputs

[`results.json`](./results.json) binds these exact retained inputs:

- T32.2 source-free baseline:
  `sha256:d1ec7b658eef84d2974c50c66d6dca00160a412fd49154c1ad4e232baae695ad`;
- T32.3 artifact receipt:
  `sha256:ce94187fd3b9c1ad42b64f131c9234399a5df918a07c5f452b94393873ab8611`;
- T32.3 Git bundle:
  `sha256:05a1b845a2eaee1c6a2b0beda972aa0ea6ffe9cc636d886014887202728e2194`.

The T32.3 generator now exposes the exact frozen load-profile bytes to later
spikes. Tests verify every materialized path, byte count, and digest against
the already-retained profile inventory; the profile and corpus artifacts did
not change.

## Correctness result

The harness rejects a child whose Go build metadata does not match this
module's exact zoekt version and checksum. That admitted `zoekt-git-index`
child indexed all five neutral revisions in one direct shard set. The retained
run checked 40 independent oracle queries:
33 exact/stale searches executed and seven restricted, removed, or unavailable
queries stopped at authority admission with the oracle's empty result.

All checks passed:

- exact All code and service result sets at every revision;
- catalog-derived service filename atoms present inside the zoekt query;
- a raw-byte-derived broad `package` oracle at every revision;
- stable ordering for a branch-bound all-document adversarial ranking case and
  equality between its full first three results and zoekt's `top-K=3` result;
- exact branch binding and per-revision catalog/oracle equality across all five
  snapshots, plus repeatable prior-revision and static `A → B → A` branch
  selection within one immutable shard set;
- service `top-K=1` execution over the 1,000/5,000-service profiles, proving
  unrelated higher-ranked documents never enter a post-filtering budget.

The correctness test authors a fresh public repository, runs the real child,
and replays the oracle. It compares semantic booleans and the path-result
digest; timings are observations, not reproducibility pins.

## Cost result

Measured on the source-free environment recorded in `results.json` (Darwin
arm64, Go 1.26.5, 10 logical CPUs). The neutral builds disable ctags to isolate
physical text-search topology; T32.2 separately measured the target path under
its frozen production configuration.

| Input | Files | Memberships | Initial build | One-file revision build | Peak child RSS | Shard bytes | Shards | Cold / warm query battery |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Five-revision correctness corpus | 14–15/revision | oracle-defined | 54 ms | n/a | 97.0 MiB | 89,333 | 1 | 1 / 1 ms |
| 1,000 services | 3,151 | 5,000 | 345 ms | 288 ms | 109.2 / 97.3 MiB | 803,142 / 803,361 | 1 | 1,059 / 884 µs |
| 5,000 services | 15,751 | 25,000 | 1,827 ms | 1,300 ms | 135.7 / 167.9 MiB | 3,876,442 / 3,876,661 | 1 | 4,540 / 4,788 µs |

The 1,000- and 5,000-service predicate passes compile all five placements for
every service only to expose the frozen aggregate cost: 10.7/52.8 ms,
369,000/1,845,000 canonical query-tree bytes, and 369 bytes maximum for one
service. A production request compiles one authorized service predicate, not
all services, and indexing remains one repository build rather than a loop
over services.

Each visible shard maps once under the pinned zoekt Unix reader contract. The
run therefore recorded one shard mmap in each case. Numeric process
descriptors were measured with `lsof`: opening each one-shard directory reader
added five descriptors. Peak child RSS comes from `wait4`'s `ru_maxrss`, with
Linux KiB normalized to bytes and Darwin's byte value retained. “Cold” means a
newly opened reader; the experiment did not evict the host page cache. A
measured one-file update uses the same clean staging/full-build posture as
today's `-incremental=false` production path.

The profile no-op check loads desired HEAD from Git and the active branch
version from published shard metadata before timing one identity comparison.
The timed comparison records zero child runs, file scans, shard reads, or
shard-byte change. T32.2's separate real restart measurement supplies the
target-side confirmation: already current, zero index children, zero
shard-byte delta, and zero shard-mtime changes. The neutral replay does not
interrupt publication or transition physical generations; those recovery
semantics remain owned by T34.3.

## Boundaries and steady-state cost

These tiny synthetic source bytes measure service-count metadata and query
predicate behavior; they do not reproduce target source volume and cannot set
a service-count, latency, memory, disk, or shard SLO. Warm being slower than
cold in one 5,000-service micro-run is retained honestly rather than promoted
to a cache claim.

This package is development-only. Production packages do not import it, and
no request, query, startup/restart, sync tick, retry/no-op, publication
transition, store, cache, filesystem, Git, child-process, memory, descriptor,
or mmap work changed because of T32.4. Only explicit measurement or the
targeted replay test creates Git children and a zoekt child; both use temporary
directories and retain only the closed source-free receipt.

## Reproduction

```sh
go run ./spike/t324/cmd/measure \
  -root . \
  -zoekt bin/zoekt-git-index \
  -out spike/t324/results.json
go test ./spike/t323 ./spike/t324/... -count=1
```

Re-authoring timings or environment identity changes the receipt and requires
review. Semantic result digests and input bindings must remain unchanged unless
their owning retained artifact advances explicitly.
