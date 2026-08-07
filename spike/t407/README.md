# T40.7 evidence-stage accounting measurement

`results.json` is the source-free receipt from the opt-in production-store
measurement:

```sh
T407_MEASURE_STORE=1 \
T407_RESULTS_PATH="$PWD/spike/t407/results.json" \
go test ./internal/store \
  -run '^TestT407MaximumShapeEvidenceAccountingMeasurement$' -count=1 -v
```

The test stages one maximum admitted run (12,500 facts, 25,000 evidence rows,
and 12,500 reference edges) through the exact production append transaction
over a one-fact published baseline, publishes it, and completely sweeps the
superseded baseline. It
records append query count, maximum encoded transaction input, wall time, Go
allocation, SurrealDB RSS, publication, paging, and sweep observations.

This receipt is implementation evidence only. It establishes no SLO,
supported-scale, accuracy/completeness, migration/decommission, topology,
pilot, release, or private-rerun claim.
