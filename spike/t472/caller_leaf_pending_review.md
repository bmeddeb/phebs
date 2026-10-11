# Caller-leaf indefinite pending review — 2026-10-10 (T47.2g)

Scope: Ben-routed A-slice review of the caller-leaf's behavior when one
partitioned extraction domain ends as a terminal refusal. This is a review
artifact; it changes no production behavior and routes the fix decision.

## Finding

A domain whose partition results include a terminal refusal never binds its
domain root as current authority, so caller-leaf convergence stalls
indefinitely and silently:

- `Runtime.publishRoot` skips `Publisher.PublishDomain` and the current
  pointer for `terminal_refusal` and `retryable` roots and only invokes
  `OnSettled` (`internal/extractionpublication/runtime.go:1254-1260`).
- `CurrentDomainAuthority` therefore finds no current pointer and reports
  `store.ErrNotFound`; no store authority row exists for the refused domain.
  Retained 20261010b state is an instance: 11 generation roots, `grpc-caller`
  missing, zero outcome rows, all extraction-partitions schedules settled.
- `buildPartitionedAuthority` treats absent domains as pending and joins
  `ErrPartitionedCallerPending` (`internal/callerexecute/worker.go:34`,
  `1144-1147`); a missing observation inventory (`worker.go:1114-1117`), a
  stale candidate authority (`worker.go:1061-1065`), a stale domain version
  (`worker.go:1153`) and a stale domain authority (`worker.go:1155-1162`)
  join the same marker.
- Both caller-leaf authority checks convert that marker into
  `store.WithDeferral` (`worker.go:242-244`, `329-331`). The runner's
  deferral case preserves the attempt and re-paces the job at the worker
  interval (`internal/store/runner.go:293-307`); `DeferJob` explicitly
  returns "an upstream-blocked active lease to pending **without consuming
  an attempt**" (`internal/store/surreal.go:4225-4237`).
- The bounded settle path requires the opposite shape: only a store-visible
  authority whose disposition is not success/empty/unavailable_prerequisite
  escalates through `ErrPartitionedCallerUnavailable` into
  `ClearCallerGenerationPublication` plus `publications.Retire`
  (`internal/downstreamauthority/authority.go:90-109`;
  `internal/callerexecute/worker.go:245-251`). A terminal refusal never
  produces such an authority, so that path is unreachable for refusals.

Net behavior: a permanently refused domain leaves its caller job pending
forever — paced retries, no attempt burn-down, no settlement, no
operator-visible outcome row, and no state distinguishing "upstream
refused" from "still converging". The extraction side fails terminally and
visibly (after T47.2g the chunk job's durable error names the owning stage
and classification), but nothing connects that terminal fact to the caller
job.

## Assessment

- Plausible intent: "pending until upstream becomes usable again after an
  operator correction and rerun". The defect is that pending is unbounded
  and indistinguishable from slow convergence; nothing ever escalates and
  no record of the refusal reaches the caller side.
- Blast radius: any repository whose partitioned extraction refuses
  terminally silently stalls caller-map convergence for that repository.
- Not a regression of this slice: identical behavior before T47.2g; this
  review makes it explicit and durable-diagnosable on the extraction side.

## Options (route to Ben; none implemented)

(a) Bind refused roots as store-visible authority — e.g. current pointer
    with the terminal_refusal disposition. `RequireUsable` already rejects
    that disposition (`authority.go:101-106`), so the caller job would
    settle through the existing `ErrPartitionedCallerUnavailable` path
    (clear + retire = explicit gap). Touches publication/reader semantics;
    needs its own review and exact fence reasoning.
(b) Accept and document — callers stay pending by design; operators resolve
    by correcting and re-running. Requires an OPERATIONS-guide statement so
    a permanently pending caller job is diagnosable, plus operator tooling
    to see the refusal (the result-file reason now carries it).
(c) Bounded pending with escalation — cap consecutive pending deferrals and
    convert to an explicit unavailable/gap outcome. Runner/store change;
    larger surface than (a) or (b).

## Boundaries

- No production behavior change, no label/prediction/score/seal, no
  configuration change. The retained 20261010b and reproduction custody is
  untouched and stays read-only.
