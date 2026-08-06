# T39.3 — Security and lifecycle gate

T39.3 is a source-free, gate-only review of the final multi-service security
and lifecycle boundaries. It changes no production behavior. The retained
receipt binds the exact T39.1 correctness gate, the honestly stopped T39.2
target gate, and the T35.4 lifecycle receipt before naming the executable
production tests that exercise every acceptance category.

T39.2 is an input authority, not a prerequisite pass. Its incremental
`pipeline` failure remains terminal, its later phases remain `not_run`, and
T39.3 cannot mark it superseded. A passing T39.3 receipt therefore does not
turn the target operating-envelope stop into release evidence.

## Frozen matrix

| Case | Required boundary |
|---|---|
| authorization non-disclosure | hidden service names/counts stay hidden; authorization precedes repository, state, and filesystem work |
| shared and cross-service authority | shared paths and cross-service edges retain every accepted, nonaccepted, and unowned placement without inventing authority |
| revocation, cursor, and proof reuse | in-flight revocation suppresses output; cursors bind principal/revision/incarnation; proof reads authorize again |
| partial and stale roots | invalid v2 never falls back; incomplete generations never move pointers; stale transitions fail the final fence |
| malicious catalog | open/duplicate fields, wrong types, cycles, overlaps, invalid roles, and pre-growth limit crossings refuse |
| malicious source and Git | replacements cannot rewrite bytes; alternates, symlinked shards, missing objects, and oversized blobs fail closed without identity leaks |
| disk pressure and admission | exact 80/90/75 hysteresis, unavailable capacity, root identity changes, and symlinks fail closed |
| pin and lease retention | current/rollback roots and active reader leases survive collection; proof expiry releases only its own pins |
| bounded sweep | owner rotation is restart-fair, failures stay local, and query/delete/trim budgets plus running leases remain fenced |
| backup, restore, and teardown | precious authority restores exactly, composite derived authority is validated or omitted, and teardown removes only its dedicated workspace |

Every case is required. A failure stops release; a warning is not approval; a
skipped case is not a pass; and thresholds cannot be raised to manufacture a
green receipt. The receipt cannot certify its own review. Merge requires a
reviewer independent of the implementer, outside the receipt.

The teardown probe creates a bounded temporary security-gate workspace with
derived bytes and a credential, removes that exact workspace, proves it is
gone, and proves a sibling sentinel remains. It never operates on repository,
home, shared, or unresolved paths.

## Reproduction

```sh
t393_tmp=$(mktemp -d)
go run ./spike/t393/cmd/author -root . -out "$t393_tmp/results.json"
cmp "$t393_tmp/results.json" spike/t393/results.json
go test -race ./spike/t393 ./internal/servicecatalog ./internal/sourcepartition \
  ./internal/search ./internal/relationshippublication \
  ./internal/observationpublication ./internal/lifecycle ./internal/api
go test ./internal/store -run \
  'Test(ProofBundleExpiryReleasesOnlyOwnedPins|GenerationLifecycleProtectsCurrentRollbackFloorAndRunningLease)$' \
  -count=1 -timeout=25m
go test ./internal/recovery \
  -run '^TestLiveBackupRestoreAndStartupExactSearchRecovery$' -count=1
make docs-check
make verify-glossary
```

The author command refuses to overwrite an existing receipt. The retained
receipt contains only public input paths/digests, closed case/assertion/test
names, the preserved T39.2 stop shape, review/stop policy, teardown mechanics,
and negative claims. It contains no repository identity, service name, source
path or bytes, query text, object ID, credential, host, raw error, private
topology, or target measurement.

T39.3 establishes neither comprehensive security nor a general SLO, accuracy,
release, migration-complete, or decommission-safe claim. `GATE2-V2` remains
`NOT_ESTABLISHED`.
