# T20.1 — Synthetic monorepo contract and validation spike

**Date:** 2026-07-25 · **Status:** COMPLETE · **Scope:** freeze the
neutral correctness and scale populations, current limits, and measured
publication/query/retention behavior before Epic 20 production work.

This spike makes no accuracy, runtime-traffic, migration-completion, or
decommission-safety claim. Every source byte is generated from the neutral
templates in this directory. No employer or external-corpus byte is used.

## Profiles

`small` is the labeled correctness profile. It has protobuf and Thrift
declarations under `idl/`, checked-in generated Go under `gen/`, handwritten
callers and wrappers under `src/`, duplicate vendored IDLs, two protobuf
generated copies for one IDL, missing and conflicting Thrift generated-from
mappings, common-method collisions, aliases, embedding, dynamic dispatch, and
production/test/mock/vendor roles.

`scale-10000` includes the complete ten-call/five-mapping calibration profile
so its pinned SCIP documents always name files present in the corpus, then
adds:

- 10,000 source call occurrences for one canonical operation;
- 10,000 separately named logical services and immutable unit mappings;
- 100 caller files with 100 calls each;
- 101 repository placements of one identical content atom;
- 10,000 supporting references for the same operation, exceeding the current
  4,096 per-assertion ceiling without constructing a production assertion that
  violates it.

The resulting frozen totals are 10,010 call occurrences and 10,005 unit
mappings/distinct logical-unit labels. The store measurement uses all 10,010
calls (20,020 association-plus-assertion rows).

The Go oracle enumerates every call, generated-from relation, and unit mapping
without consuming extractor output. Tests require two independently generated
file maps and Git trees to be byte-identical.

## Prepared symbol-index input

`testdata/index.scip` and `testdata/scale.index.scip` were prepared in
separately reviewable commits. `index.lock.json` pins both SHA-256 digests.
Normal generation only embeds, verifies, and copies the profile's bytes
verbatim. It never invokes `prepare-index` or another indexer. The preparation
command is retained solely so a future reviewed change can explain how
replacement bytes were made:

```sh
go run ./spike/t201/cmd/prepare-index
```

The frozen input shape is one repository-root `index.scip` blob under the
production 64 MiB cap. The small artifact contains 7 documents and 5 typed
caller references. The scale artifact contains 107 documents and 10,005 typed
caller references; at 893,956 bytes it is only 1.33% of the 64 MiB cap.
T20.6 therefore retains the root-only capability for this target and does not
introduce a shard manifest.

## Executable gates

The default offline gates are:

```sh
go test ./spike/t201/... -count=1
go test ./internal/extract ./internal/store -run T201 -count=1
```

They pin deterministic generation, the prepared-index digest and shape, all
correctness cases, scale cardinalities, current extraction output, deliberate
global-name abstention, neutral naming, and production hard limits.

The target-size store measurement is opt-in because it stages two 20,000-row
runs and sweeps one of them. It admits only larger limit variables through a
test-only seam; it invokes the exact production `addEvidenceSQL`,
`publishExtractionRunSQL`, and `sweepRunSQL` constants:

```sh
T201_MEASURE_STORE=1 \
T201_RESULTS_PATH=/private/tmp/phebs-t201-results.json \
GOCACHE=/private/tmp/phebs-t201-go-cache \
go test ./internal/store \
  -run '^TestT201TargetPublicationAndSweepMeasurement$' -count=1 -v
```

The reviewed metrics are committed in `results.json`; the go/no-go table is in
`docs/BACKLOG.md` and the binding decision is in the T20.1 PLAN ADR.

## Frozen result

The reference run used Go 1.26.5 on darwin/arm64 (10 GOMAXPROCS) and
SurrealDB 3.2.0. At 10,010 facts / 20,020 rows:

- exact publication recount plus atomic supersession took 156 ms, allocated
  190,048 Go bytes, and observed 236,732,416 bytes peak Surreal RSS;
- a 100-row reverse page plus continuation sentinel took 175 ms, but the query
  plan scanned all 10,010 `assertion_run` rows before filtering and sorting;
- exact one-run sweep took 1,024 ms and observed 332,562,432 bytes peak Surreal
  RSS; it removed the superseded run and preserved the current shared rows.

Publication and sweep pass the frozen 2 s / 512 MiB gates. The reverse
wall-time passes the 250 ms reference budget but its plan fails: T20.4 must
land the composite reverse index. Current 5,000-fact and 10,000-row admission
limits fail the target, so T20.2/T20.3 proceed. T20.2's frozen worker ceiling
is 256 MiB incremental Go heap. The root-only scale SCIP input passes at
893,956 bytes, so T20.6 adds no shard manifest at this target.
