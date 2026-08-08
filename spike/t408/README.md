# T40.8 sparse-partition measurement

`results.json` is the source-free receipt from the opt-in maximum neutral
partition measurement:

```sh
T408_MEASURE=1 \
T408_RESULTS_PATH="$PWD/spike/t408/results.json" \
go test ./internal/candidate \
  -run '^TestT408MaximumNeutralPartitionMeasurement$' -count=1 -v
```

The test authors 4,096 deterministic small Go blobs in a temporary Git
repository, builds and fully validates the existing v4 candidate generation,
derives the T40.8 sparse controls, and replays the exact maximum candidate
partition through the production sparse reader. The receipt separates the
cold one-pass control build from shallow control open and selected-member
replay, and compares the latter with the unchanged five-minute domain
deadline. The broader contract requires the builder/authority's trusted root
digest, creates private controls exclusively, gives present typed inputs their
own scheduled partitions, excludes unavailable domains before member scans,
and caps aggregate indexes at 131,072 partitions and 128 MiB.

This receipt is implementation evidence only. It establishes no extractor
SLO, supported-scale, accuracy/completeness, migration/decommission, topology,
pilot, release, or private-rerun claim.
