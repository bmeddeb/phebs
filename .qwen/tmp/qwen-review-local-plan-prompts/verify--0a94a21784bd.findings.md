# Verification shard 2 — findings R1-9 .. R1-14

## R1-9 [Suggestion, confidence high] — Searcher reader-pin fence has zero test coverage
- File: internal/search/searcher.go:1073-1075
- Anchor: `if s.generationPins != nil { root, rootErr := focusedindex.ReadSearchGenerationRoot(s.indexDir, repo.Name) if rootErr == nil {`
- Issue: no test constructs a Searcher via OpenWithGenerationPins; every search test uses Open (nil pins), so lease acquisition (searcher.go:1083), stale-root refusal, the searchGenerations field on both compiledSearch return paths (:1168, :1185), and release in compiledSearch.release() (:451-454) are never executed by any test; the only pin tests acquire pins by hand (search_generation_test.go:171).
- Failure scenario (claim under test): deleting the `searchGenerations: searchGenerationLeases` assignment or the release loop in release() leaks every query-acquired lease, Pinned() stays true forever, SweepSearchGenerationLifecycle permanently skips those generations, and no test turns red.
- Suggested fix per finder: add a searcher test through OpenWithGenerationPins asserting Pinned() true while a compiled search is live and false after release(), plus a stale-root refusal case.

## R1-10 [Suggestion, confidence high] — New whole-search crash-recovery branch in reconcile has no test
- File: internal/sync/reconcile.go:373-376
- Anchor: `if repo.IndexedAnalysisUnit == nil || repo.IndexedAnalysisUnit.SearchIndexPosture != analysisunit.SearchIndexFocused { recovered, recoverErr := focusedindex.RecoverSearchPublication( ctx, indexDir, repo.Name, wholeRevisions(repo),`
- Issue: internal/sync/focused_lifecycle_test.go only covers stale focused markers; no sync test ever creates a search transition marker, so the new RecoverSearchPublication branch in reclaimCommittedPublicationMarkers is untested.
- Failure scenario (claim under test): a crash inside installStableSearchGeneration (after removeRepositoryArtifacts stripped the flat view, before writeSearchGenerationRoot) leaves a partial flat view, stale root, and a prior-process publishing + transition marker; the repo row still carries the old revision so Index()'s no-op short-circuit never rebuilds; this reconcile branch is the only automatic repair; if its condition or call regresses, no test turns red and the repository's whole search stays broken until manual reindex.
- Suggested fix per finder: mirror staleFocusedPublication for whole search — PublishWholeGeneration, leave a foreign-token publishing marker + transition marker matching the durable row, run ReconcileArtifacts, assert marker gone and ValidateRepositorySearchGeneration passes.

## R1-11 [Suggestion, confidence high] — The policy refusal fences have no tests
- File: internal/focusedindex/search_generation.go:268-272
- Anchor: `if shardNames[name] && info.Size() > MaxSearchShardLogicalBytes { return 0, 0, "", errors.New("search shard exceeds logical-byte policy") } if info.Size() < 0 || logical > MaxSearchGenerationLogicalBytes-info.Size() { return 0, 0, "", errors.New("search generation exceeds logical-byte policy")`
- Issue: the 512 MiB/shard, 48 GiB/generation, 96 GiB retained-pair (:181-186), 256-shard, and file-count checks appear in tests only as a loop bound (search_generation_test.go:188); the fences are the ticket's core stated safety property.
- Failure scenario (claim under test): a regression in any comparison (e.g. inverting the MaxSearchGenerationLogicalBytes-info.Size() overflow guard) publishes an oversized generation; a 49 GiB current + 48 GiB prior would exceed the 96 GiB replacement-headroom contract and exhaust disk during rollback. Validators are cheaply reachable with plain structs and a sparse-file shard (Truncate past 512 MiB).
- Suggested fix per finder: table tests for SearchGenerationReservation / validateSearchGenerationRef / validateSearchGenerationRoot at/one-over each constant, plus one sparse-shard publication refusal test.

## R1-12 [Suggestion, confidence high] — RecoverSearchPublication prior-selection and no-match branches are untested
- File: internal/focusedindex/search_generation.go:908-910
- Anchor: `case marker.Previous != nil && sameSearchRevisions(marker.Previous.Current.Revisions, revisions): selected = marker.Previous.Current root = *marker.Previous`
- Issue: tests cover only the candidate-matches-durable-row outcome; the prior-generation selection branch (restore the prior complete generation) and the no-match error branch (:911-912) have no test.
- Failure scenario (claim under test): the prior branch is the recovery for a mid-install crash (durable row holds old revisions, marker holds candidate + previous); if it regressed (e.g. selected marker.Candidate unconditionally), startup repair would install and root the generation the store never committed, serving search results for an uncommitted revision set, with no red test.
- Suggested fix per finder: publish A, begin B's transition without finish, RecoverSearchPublication with A's revisions, assert root restored to A; add a mismatched-revisions case asserting the "matches no committed generation" error.

## R1-13 [Nice to have, confidence high] — AdmitDerived field comment contradicts the new measured call
- File: internal/indexer/indexer.go:183-185
- Anchor: `// AdmitDerived runs after the exact no-op fence and before any staging // directory or child is created. T35.3 uses it for hard-watermark refusal. AdmitDerived func(context.Context, int64) error`
- Issue: the diff adds a second, measured AdmitDerived call that runs after NewBuildWorkspace and BuildSourceGeneration, contradicting the documented "before any staging directory or child is created" invariant; today the defer os.RemoveAll(workspace) keeps refusal leak-free.
- Failure scenario (claim under test): a future change relying on the documented guarantee (moving/dropping that defer, or adding pre-staging side effects assumed absent on refusal) would leak staging workspaces on measured-admission refusal without warning.
- Suggested fix per finder: update the comment to state that the zero-byte probe runs before staging while the measured search-generation admission runs after the source census and staging-workspace creation (both cleaned up by the workspace defer on refusal).

## R1-14 [Nice to have, confidence high] — Sweep result.More set unconditionally when >=2 namespaces exist
- File: internal/focusedindex/search_generation_lifecycle.go:71-72
- Anchor: `result.Cursor = repositories[position] result.More = len(repositories) > 1`
- Issue: More conflates "other namespaces exist" with "work remains"; sibling owners set More only on real backlog (delete budget exhausted / page full).
- Failure scenario (claim under test): today the concrete effect is that with >=2 indexed repositories the lifecycle status permanently reports lower_bound completeness for search-generations even when drained; latently, if the owner set ever changes so this owner ends the rotation, result.More || !result.CycleComplete keeps the controller in permanent 5-second backlog mode (two SurrealDB cursor reads/CAS writes plus a capacity statvfs per tick, no idle ever).
- Suggested fix per finder: set More only for actual remaining work (incomplete drain, additional abandoned stages, queued collecting entries).
