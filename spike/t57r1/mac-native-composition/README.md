# T57R.1 Mac native-composition follow-up

The three inherited Mac component failures are closed on unchanged source
`aa5cf5798e43559e6c35b02487f81198b5e23633`. The October 14 diagnosis remains
correct: the earlier host had 93% used capacity and the real pressure gate refused.
This run required an actual `lifecycle.NewGate(path).Check(ctx,0)` result of
normal and less than the 75% resume threshold before spending fixture time.
The host already met the capacity precondition; no capacity injection, source
repair or deadline extension was used.

| Required native test | Test time | Time including wrapper join |
|---|---:|---:|
| `TestT422WorkspaceNativeComposition` |11.07s|11.871446s|
| `TestT422WorkspaceCleanupNativeComposition` |128.51s|128.552063s|
| `TestT422WorkspaceAllOwnersNativeComposition` |232.54s|232.595100s|

All three pass once, without skips or retries. The pinned Go 1.26.5
Darwin/arm64 test binary compiled offline once in 15.398700s; SurrealDB 3.2.0
was present and inspected. Original fixture contexts remain 3/10/10 minutes,
with Go-test ceilings 200/630/630 seconds and outer work limits 215/645/645 seconds.
Every finished step fits its work limit plus the 15-second cleanup reserve;
these are observed completed timings, not a hard kernel or whole-workload wall
guarantee. No signal or forced cleanup was needed.

Fourteen real gate observations cover both checkout and actual fixture TMPDIR
before compilation and before/after each test. Every sample reports 73% used,
normal and below resume, with 133,928,050,688–136,140,582,912 available bytes.
These brackets establish sampled preconditions, not continuous normality or
physical-memory admission.

The [exact logs](logs/) and [execution record](result.json) retain the assertions'
results. Cleanup reports 24,000 observation objects, 600 inert shards, 24,605 deleted
units, 360 owner turns, 720 cursor writes and 362 workspace samples. The all-owner
fixture reports 600 turns, 603 workspace samples, 1,251 store transactions and
1,318 store rows. Its existing assertions preserve all 15 owner names, exact
non-job drainage, truthful durable-job lower-bound/backlog, historical/current
catalog boundaries, SDK resumption, authenticated FD6/workspace observations,
EOF and private-session joins. Successful fixtures removed their private data;
the dedicated fixture TMPDIR is empty.

Scope remains the existing component contract. Base has 15 supplied owner
callbacks and modeled bearer auth. Cleanup has two real cleanup owners and 13
explicit no-ops. All-owner uses ten concrete and five closed owners, 10,001
inert relationship files and four real 10,000-service/31,500-membership catalog
generations. No corpus is indexed; these fixtures do not prove complete current
publication/pin semantics, pressure transitions, archive/restore or whole phases.
The original failed fixtures and diagnostics are untouched.

The [outcome](outcome.json), [source binding](source.json) and
[custody inventory](custody.json) bind tools, test source, test-binary hash and
reports. `capacity-probe.go.txt` is the executed temporary program retained as
text so this archive adds no Go package. `run-native.py` is a historical execution
record with absolute original paths; do not execute it unchanged. A repeat, only
when needed, must rebind fresh private output and module-local probe paths,
verify actual normal capacity, then run only these three selectors on the same
pinned tools. Full package/race matrices were deliberately not run.

No production or test source changes. Query/request, sync, startup/restart,
retry/no-op, publication, lock, cache, corpus-read, memory/disk and child costs
are unchanged. Test-only work is one command-test compile, one probe compile,
14 zero-reservation gate checks and three serial native helper/Surreal lifetimes,
plus the unchanged finite fixture seeding, byte walks and store/cursor work.
CPU and peak RSS were not measured.

This closes the Mac inherited-failure follow-up only. It establishes no
[Linux #124](https://github.com/bmeddeb/phebs/issues/124) acceptance, complete
readiness, ceremony, scale, SLO, release or production-signing result.
Production signing stays pending.
