# T39.1 — Neutral correctness, scale-admission, and recovery gate

This directory retains the source-free T39.1 gate over the final
multi-service writers and readers. It replays the independent five-revision
T32.3 oracle through the production `phebs-service-catalog-v2` contract and
binds the named production tests that own search, relationship publication,
partial-publication refusal, authorization, recovery, restore, lifecycle, and
retained-proof behavior.

The fixture adapter is explicit. T32.3's `typed` role predates the final
contract's rule that a typed placement must also carry exact `supporting`;
the adapter adds that required role and removes only that adapter-added role
when comparing back to the frozen fixture vocabulary. No accepted fixture
membership is added, removed, or relabeled by the comparison.

## Frozen scale outcomes

Both load profiles are executed unchanged against final admission:

| Profile | Frozen shape | Final result |
|---|---|---|
| 1,000 services | 5,000 memberships, 3,151 files, fan-out 25 | expected refusal at the accepted-path fan-out cap of 20 |
| 5,000 services | 25,000 memberships, 15,751 files | expected refusal at the service cap of 4,000 |

Those refusals are passing fail-closed checks, not supported-scale evidence.
No final reader starts after either refusal. The receipt therefore records no
target-corpus latency, throughput, memory, disk, freshness, availability, or
service-count SLO. T39.2 owns the separately authorized target operating
envelope.

## Reproduction

```sh
go run ./spike/t391/cmd/author -root . -out spike/t391/results.json
go test -race ./spike/t391 ./internal/servicecatalog ./internal/servicecatalogingest \
  ./internal/servicequery \
  ./internal/search ./internal/resolvernamespace ./internal/observationpublication \
  ./internal/rpccallerposting ./internal/kafkatopicposting \
  ./internal/relationshippublication ./internal/lifecycle ./internal/api
go test ./internal/recovery -run '^TestLiveBackupRestoreAndStartupExactSearchRecovery$'
```

The retained receipt contains only input digests, neutral cardinalities,
closed outcomes, limit identities, assertions, and executable gate names. It
contains no repository identity, source path or bytes, query text, object ID,
credential, host, raw error, private topology, or target measurement.
`GATE2-V2` remains `NOT_ESTABLISHED`; T39.1 does not authorize the target run,
release, accuracy/completeness, migration-complete, or decommission-safe
claims.
