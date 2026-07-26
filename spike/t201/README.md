# T20.1 — Synthetic monorepo contract and validation spike

**Date:** 2026-07-25 · **Status:** MEASUREMENT PENDING · **Scope:** freeze the
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

`testdata/index.scip` was prepared in the separately reviewable first commit.
`index.lock.json` pins its SHA-256. Normal generation only embeds, verifies,
and copies those bytes verbatim. It never invokes `prepare-index` or another
indexer. The preparation command is retained solely so a future reviewed
change can explain how replacement bytes were made:

```sh
go run ./spike/t201/cmd/prepare-index
```

The frozen current input shape is one repository-root `index.scip` blob under
the production 64 MiB cap. It contains generated method definitions and five
typed caller references. It is intentionally not a proposed sharding format.

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

The reviewed metrics and go/no-go table are recorded in `docs/BACKLOG.md` and
the T20.1 PLAN ADR after that command completes on the operator's machine.
