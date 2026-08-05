# T35.4 lifecycle recovery receipt

This directory retains the deterministic, source-free closure receipt for
Epic 35. It binds the frozen synthetic T32.3 profiles to named production-path
tests for catalog churn, generation coalescing, interrupted publication,
reader leases, proof-pin release, pressure hysteresis, bounded sweeping,
restart, backup/restore, and the administrator-only lifecycle status.

`results.json` contains no repository source, path, query, error text,
credential, host, or operator identity. The 1,000- and 5,000-service profiles
are synthetic mechanics inputs, not supported-scale or SLO claims. The
retained test regenerates the receipt twice and requires byte equality with the
checked-in file.

Run the closure gates with:

```sh
go test ./spike/t354 ./internal/lifecycle ./internal/api
go test ./internal/store -run '^(TestLifecycle|TestGenerationLifecycle|TestJobLifecycle|TestCatalogLifecycle)'
go test ./internal/recovery -run '^TestLiveBackupRestoreAndStartupExactSearchRecovery$'
```
