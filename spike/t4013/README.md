# T40.13 neutral two-million-owner convergence gate

T40.13 runs the frozen T40.1 structural and semantic repositories through the
ordinary phebs server, scheduler, publication, recovery, archive, lifecycle,
and authorized product paths. The retained plan and receipt are source-free;
the authored bare repositories, data directories, logs, query text, returned
source, credentials, and process transcripts remain outside the repository and
are destroyed after the run.

## Prospective freeze

The execution commit is frozen before either giant repository is authored.
`t4013-freeze` verifies the exact retained T40.1 envelope, both manifests, and
the same-SHA blob-reader comparison before emitting a new plan. It refuses an
existing output.

```sh
go run ./spike/t4013/cmd/t4013-freeze \
  -root "$PWD" \
  -source-commit <exact-40-hex-execution-commit> \
  -output /absolute/dedicated/t4013/plan.json
```

The v3 plan freezes 24 GiB memory and 120 GiB available-disk prerequisites, an
eight-hour total safety ceiling, 20 GiB peak RSS, 96 GiB maximum allocated data,
the production five-attempt ceiling, and a 15-minute per-server health-readiness
deadline. These are stop/decision fences for one neutral mechanics run, not a
target SLO or supported-scale claim. They may not be raised after any
measurement begins.

The disk prerequisite reserves the existing filesystem's ten-percent hard
watermark plus two structural search-generation reservations. That is the
minimum shape needed to exercise B while retaining A and then return to A
without converting an expected production pressure refusal into an accidental
host-space result. The 96 GiB ceiling is the unchanged T40.5 retained-search
allocation envelope, not a new production allowance.

The decision rule is closed before the run:

- `continue` only when every exact mechanics gate passes inside production
  bounds and the frozen host-safety envelope;
- `reduce` when a production aggregate bound or host prerequisite refuses
  before complete authority;
- `cohort_experiment` when direct mechanics converge but a frozen wall, RSS,
  allocation, recovery, or collection review ceiling is crossed; and
- `p6_investigation` only when work multiplies by logical service count, cannot
  remain repository-bounded, or direct recovery cannot converge within fixed
  production bounds.

No threshold or production constant may be changed to turn a stop into a pass.

## Ordinary-worker sequence

The dedicated installation owns its port, data directory, scratch repositories,
archive, and credentials. It may not use or terminate either development-track
server. The fixed phase order is:

1. `preflight` — verify plan/source/tool identities, host capacity, empty
   custody paths, T40.5's effective go-git selection, and the small catalog.
2. `cold` — author revision A, start the ordinary server, and wait for exact
   source, search, observation, extraction-domain, resolver, posting, and
   relationship authority.
3. `warm_noop` — request the already-current generation again; require no Git
   or index child, publication write/transaction, or authority movement.
4. `delta_b` — advance only the frozen one-file B revision and prove that only
   its admitted partition changes.
5. `return_a` — advance to the frozen A-return tree, reproduce A content
   identity, and reuse the retained settled generation where specified.
6. `interruption` — stop one in-flight derived publication, restart the same
   binary, keep partial bytes invisible, and converge without moving stale
   authority current.
7. `stale_worker` — let a superseded lease finish and prove its final fence
   cannot publish.
8. `pressure` — exercise the real capacity gate and default-on bounded owner
   rotation without filling the filesystem or weakening the hard watermark.
9. `archive_restore` — create the production backup, restore into a separate
   empty dedicated installation, verify precious authority and explicit
   derived inclusion/omission, then converge.
10. `collection` — retain current, rollback, active lease, marker, and store-pin
    roots while bounded turns reclaim only eligible generations.
11. `authorized_query` — issue the fixed source-free query classes through an
    authenticated API client and retain only counts, equality, authorization,
    root/citation identity, and final-fence results.
12. `teardown` — remove derived data, scratch repositories, archive, raw
    artifacts, logs, and credentials.

Preparation refuses before authoring when the checkout differs from the plan,
the workspace is not a new real directory outside the module, or the host is
below either frozen prerequisite. Partial authoring is removed on error. The
two loopback installations use generated private bearer credentials and
separate ports/data roots:

```sh
go run ./spike/t4013/cmd/t4013-prepare \
  -root "$PWD" \
  -workspace /absolute/dedicated/t4013-custody \
  -plan /absolute/dedicated/t4013-plan.json \
  -output /absolute/dedicated/t4013-prepared.json \
  -base-port 41731 \
  -confirm prepare-neutral-t4013-custody
```

Execution builds the exact frozen checkout's ordinary server and pinned helper
binaries into custody, starts only the two assigned loopback instances, and
checks production controls plus authenticated HTTP surfaces. Its confirmation
authorizes removal of that exact external custody after either completion or a
measured stop. The source-free observation must be outside custody and must not
already exist:

```sh
go run ./spike/t4013/cmd/t4013-execute \
  -root "$PWD" \
  -plan /absolute/dedicated/t4013-plan.json \
  -prepared /absolute/dedicated/t4013-prepared.json \
  -observation /absolute/dedicated/t4013-observation.json \
  -confirm execute-neutral-t4013-and-destroy-custody
```

The structural repository contains 2,000,002 regular-file physical owners and
uses 512 reusable Go blobs. The semantic repository separately contains
262,144 unique Go blobs and 32,768 IDL inputs. A deliberately small catalog has
at most 100 accepted services; directory-prefix memberships and explicit
unowned prefixes cover the complete census while staying below the existing
v2 12,000-distinct-path cap. It is correctness control only and establishes no
service-cardinality result.

## Source-free receipt

The observation records closed phase outcomes and scalar accounting only:
wall/RSS, logical and allocated data, child counts, control/member reads,
publication and orchestration transactions, retries, and reuse. It contains no
repository name, path, object ID, source, query, response, host name, user, or
raw error. Skipped phases are `not_run`, never successful zero work.

RSS is the 250-millisecond peak of the ordinary server and its observed process
tree. Child counts are distinct observed PIDs classified as Git, index, or
other. Logical/allocated bytes are `du`'s apparent/allocated custody gauges at
the phase fence. Control/member reads count the validated bounded controls and
partition/member identities opened by the exact oracle. Publication writes and
transactions count changed authoritative plane identities; orchestration
transactions/retries and reuse come only from the closed job, candidate, and
extraction diagnostic schemas. They are mechanics counters, not kernel I/O or
database-profiler estimates.

```sh
go run ./spike/t4013/cmd/t4013-receipt \
  -plan /absolute/dedicated/t4013/plan.json \
  -plan-digest sha256:<exact-plan-digest> \
  -observation /absolute/dedicated/t4013/source-free-observation.json \
  -output spike/t4013/results.json
```

A completed receipt requires four exact T40.5 search-generation reader
receipts (structural A/B/A-return and semantic A), all with `mode=go_git`, zero
batch reads, and fallback reads equal to offered regular files. Every applicable
partition must settle, all required domains and relationship roots must publish,
and deliberately absent inputs plus unsupported syntax must remain explicit.
The warm no-op must have zero content children and publication mutations.

The result is mechanics evidence for one neutral environment only. It does not
establish a target SLO, supported service cardinality, accuracy, completeness,
freshness, release authority, private-rerun authority, migration completion, or
decommission safety. `GATE2-V2` remains `NOT_ESTABLISHED` and
`DO_NOT_RELEASE` remains in force.

## Measured outcome

The one authorized run on 2026-08-08 used execution commit
`b1b4e808e1987b3bf28e4afac21cc83b72aa27f2` and the retained
[`plan.json`](./plan.json) (`sha256:13863ed6e0e19e3edf5cbaa2e6d2f79eef645341661a5d61c0066f7f009974a0`).
The host passed the frozen 24-GiB-memory and 120-GiB-available-disk
prerequisites. Preflight succeeded, but cold convergence stopped at its exact
oracle before a successful cold phase fence. Every later phase is `not_run`.

The retained [`results.json`](./results.json)
(`sha256:873c373353c540d05e61b243b63befd781e7280b4ec52c0ddd4ef074661e4c85`)
therefore records an `unclassified`, unsubstantiated decision. Exact teardown removed the external authored
repositories, data, logs, credentials, and binaries; the receipt retains no
source or raw diagnostic. The result authorizes no rerun, threshold change,
cohort/P6 selection, release, or scale claim and did not pass independent
review.

The independent review found that the original executor discarded failed-cold
meters and did not retain executable identities, so the earlier `reduce`
classification could not be proved from retained evidence. The corrected
harness builds from an exact `git archive HEAD`, refuses a modified or untracked
checkout, retains four executable digests, finalizes failed-phase meters,
requires diagnostic reuse, preserves a captured observation through shutdown
failure, and leaves operational failures unclassified unless a closed typed
cause selects a frozen rule. Failed meter finalization is sticky. A stopped
receipt after successful preflight requires the four executable identities;
the sole exception is the exact retained historical receipt byte digest above.
The stale-worker case treats log starts as discovery only. It drains the log to
current EOF, resolves the candidate through production's complete extraction
schedule/generation/plan validation, and reads the source-free authoritative
attempt status from the already supervised local store. The exact semantic-B
attempt must remain `running` immediately before and after the semantic-A
source transition before its matching terminal stale fence can pass. A probe
race or early healthy completion is unclassified, not an exact refusal. The
cursor reuses one fixed buffer and retains only unmatched attempts. Real query
matches plus a citation and exact stopped phase/decision coherence remain
required. Those corrections govern only a separately authorized future plan
and do not retroactively strengthen this consumed run.

## Large-Mac ceremony driver

[`run-large-mac-ceremony.sh`](./run-large-mac-ceremony.sh) orchestrates a new
neutral ceremony on a dedicated macOS host. Its defaults are the phebs clone at
`~/phebs`, isolated state at `~/phebs-t4013-ceremony`, and loopback ports 41731
and 41732. The driver verifies a clean exact checkout, the frozen 24-GiB memory
and 120-GiB available-disk prerequisites, and the focused harness tests before
it authors custody. A 64-GiB Mac with more than 500 GiB available exceeds those
host prerequisites; the frozen production and review ceilings remain
unchanged. Preflight also requires free loopback ports, SurrealDB on `PATH`,
and the Go 1.26 toolchain line; it hydrates the checksum-verified module cache
before all ceremony `go run` and measured toolchain builds switch to
`GOPROXY=off`. The committed `go.sum` includes the complete focused-harness
and Buf-command graph, and preflight repeats the clean-checkout guard after
hydration and tests so dependency resolution can never silently change the
frozen source.

The review seam is mandatory: `freeze` creates a new ceremony directory and
prints its plan digest and signing-key fingerprint, then exits. Record and
review both out of band. The v3 plan also binds bounded public versions and
executable SHA-256 identities for the Go driver, Go compiler, Go linker, Git,
and supervised SurrealDB. It additionally freezes the health-readiness deadline
and requires bounded source-free startup observations. Preparation verifies the
same tool inventory before and after custody authoring; execution verifies it
at admission, before result classification, and after teardown. Any drift
destroys custody and leaves a stopped result unclassified. `execute` requires
the exact reviewed digest, the same frozen signer, and the fixed approval
phrase. Choose a new identifier for every attempt; existing directories,
plans, observations, receipts, custody, and packages are never overwritten.

The `t40r1-neutral-01` freeze (plan
`sha256:eb8430b97a543182e89c07b117cb7105e13ee4592171aa0992c7989f8c31ab8b`)
was stopped during independent plan review before custody or execution because
its v1 plan did not bind these host executables. It is permanently retired and
must not be executed or reused.

The independently reviewed `t40r1-neutral-02` v2 plan
(`sha256:22c61633dd2fb1ad1bf008975addc9e58894c77db95cdfddf84475921d2d9e08`)
stopped honestly in preflight before measured binaries or cold work. A normal
Git archive directory header such as `.claude/` was incorrectly rejected as a
custody escape because filesystem cleaning removes its trailing slash. Receipt
`sha256:27722137720b409348caeaeda0b5d3f8532fe399726fe307c3b98a17cb771d15`
is signed, byte-reproducible, `unclassified`, and retains successful teardown
with no custody. It is operational evidence only and is also permanently
retired. Take 3 accepted the canonical directory marker while preserving the
absolute/parent/noncanonical/symlink refusal boundary, and normalizes BSD/macOS
`wc -c` output before enforcing the transfer-package byte ceiling.

The independently verified `t40r1-neutral-03` v2 plan
(`sha256:84b2cc6608ac50e1ebca9e4cc89b7fb7d24317376c1252008706bb3347998ef4`)
at commit `b8856df15ee599e1eba71aded618cdcab1acb3c3` passed preflight, then the
first structural server did not become healthy inside the executor's implicit
90-second wait. The signed source-free package
`sha256:33f437197e94a93aff578db4e28376d05ebceafa1077e26a72c994c9a1f1642d`
and receipt
`sha256:32fd7aacfbfa0c5378568407abae56ea3c95d16d52caa1cef1d70b5bc7446a3c`
verify exactly. They record successful custody destruction and an
`unclassified` stop, but the server was destroyed before its launch meter was
registered, so no retained evidence identifies the stalled startup stage or
complete process accounting. Take 3 is operational evidence only and is
permanently retired.

Take 4 uses the v3 plan/observation/receipt contract. Meter admission happens
immediately after process launch and before health polling. The ordinary server
emits opt-in closed startup stages; the retained observation contains only the
profile/phase identity, outcome, last closed stage, health-error class, attempt
and wall counts, RSS/child counts, and raw-log byte count plus SHA-256. It never
contains raw logs, paths, configuration, credentials, source, or returned
content. The plan prospectively freezes a 15-minute readiness deadline inside
the unchanged eight-hour total wall ceiling. Historical v1/v2 bytes and Take 3
remain verifiable and unchanged.

The independently verified `t40r1-neutral-04` v3 plan
(`sha256:4b791e33dc60714d7993a1f71678ded2b650d7eebcf5606d0c049d6dce6bba15`)
at commit `c2ed1e666f919c9d4d69203e03552f06ddf2dd3c` retained the intended
startup accounting, but stopped at health readiness because the ceremony's
strict application decoder rejected Huma's transport-owned `$schema` field.
Package `sha256:7d241322a814f6bcdd4c14a3d0b69c8b0e490e2ff84444c859b7fb250584415d`
and receipt
`sha256:d6532ac1753d093597f897748f7629f46a3d69fa35dbe7217226882ef92223f3`
verify exactly. The server reached `http_ready`; the stopped cold phase retained
3,764,600,832 peak RSS bytes, 19,980,775,424 allocated custody bytes, three Git
children, one index child, 18 other children, 13 orchestration transactions,
and two retries. Teardown destroyed custody. The result is an `unclassified`
operational stop, not a scale refusal, and Take 4 is permanently retired.

Take 5 keeps the v3 evidence contract and fixes only the response boundary.
Object responses must carry exactly one bounded Huma schema link whose scheme,
loopback authority, and `/schemas/<closed-name>.json` path match the request.
The decoder consumes that transport field, then applies the unchanged strict
application decoder. Top-level arrays remain strict without a schema field.
Missing, duplicate, foreign, malformed, unknown-field, primitive, trailing,
and oversized responses fail closed. Real Huma health-object and repository-
array responses plus every ceremony target type are pinned before a fresh plan.

The independently verified `t40r1-neutral-05` v3 plan
(`sha256:0fde7999aad6830d6f463458c7d3930cb3feb8bb8428db984731ffb2907dc1fc`)
at commit `2b32391c38013e7c238706afc0b8ba1f491cecb2` proved that correction:
the structural server reached `http_ready` and returned exact health in 13,826
ms. Its first cold convergence wait then stopped after 7,214,033 ms. Source-free
package `sha256:35a12c261542f78d9a638cd27e20a65af4630aede3ad46ce471f4b2d02f909a0`,
observation `sha256:da505a4d3ad3c08f5551b811cdf20d9e3d443b5b3a23333f70b4c454f087221d`,
and receipt `sha256:b4fd42257a9695d263ebcd547df0b1c7f149569fe4f0c0a49d436178e989094f`
verify exactly. Peak RSS was 3,712,614,400 bytes, allocated custody was
20,080,287,744 bytes, and teardown destroyed all custody. Six aggregate job
requeue reports do not establish that any one unit exceeded the frozen
five-attempt production limit. The receipt correctly remains `unclassified`:
it crossed neither the 20-GiB RSS, 96-GiB allocation, nor eight-hour total-wall
ceiling, and its generic operational code identifies neither the last
convergence stage nor whether its published control identity changed. Take 5
is permanently retired.

The independently verified `t40r1-neutral-06` v4 plan
(`sha256:3f4111537ee2027d53a774a40d90b14d331cd9ba680c9f2388671560b07495bf`)
at commit `6490e3e0d41c46662d7ac4d3fef4ab8118000407` reached healthy structural
startup in 18,825 ms, then stopped exactly at the signed 7,200,000-ms cold
convergence deadline. Package
`sha256:f367d8ce9ca26c255c7a086e4bafd4ab35d007a0002a19407676c1d528c7c59c`,
observation
`sha256:00941d7f0717e5aa8ce2a9620f4e602b27a929323f51724f3f4c0771f46b9479`,
and receipt
`sha256:02ecfff24e47b787c47473e20b3c3d1249871530ce4cc1d6825096265b591b41`
verify exactly. The 1,440 five-second probes recorded six control-identity
changes and finished at `observation_publication` with different first and
last progress digests. Peak RSS was 4,022,009,856 bytes and allocated custody
was 20,082,331,648 bytes, below the frozen ceilings; teardown destroyed
custody. The result proves that startup and host resources were not the stop
and that the closed controls changed during the wait. It does not prove the
direction or timing of those changes, the exact observation schedule
completion fraction, or that an unspecified extension would converge. Take 6
therefore remains `unclassified` and is permanently retired.

Take 7 uses a four-hour full-convergence review deadline. This is the exact
two-times extension of the right-censored Take 6 interval and consumes at most
half of the unchanged eight-hour total-wall ceiling. It does not reserve four
full hours for later gates: preflight, startup, and earlier phases also consume
the parent ceiling, so later work receives only the actual remaining wall.
It is a prospective diagnostic fence,
not an ETA, target SLO, supported-scale claim, or production-constant change.
The 20-minute revalidation deadline is unchanged. No further extension is
implied: a four-hour expiry still stops and requires a separate review of the
direct topology or total ceremony envelope.

V5 plan, observation, and receipt schemas preserve v1-v4 bytes. In addition to
the v4 digest record, each wait retains its first stage, stage-change count,
last progress-change wall time, and the last bounded source-free observation
progress projection already read by the oracle: closed state plus planning,
schedule, and publication counters. Repository identities, generation
digests, timestamps, paths, errors, source, responses, credentials, and logs
remain private. The ceremony adds no API or filesystem read, and production
request, sync, scheduler, publication, lifecycle, cache, lock, corpus-read,
memory, and child-process behavior is unchanged.

`neutral-07` was signed against an earlier source commit and is permanently
consumed without execution. It cannot authorize the corrected bytes. Take 8
therefore selects `neutral-08` with a new signing key. The v5 deadlines do not
change. If the frozen eight-hour parent expires while meter finalization also
fails, the independently cause-bound `review_ceiling_crossed` stop wins;
measurement unavailability still wins over non-parent failures, including
meter-dependent RSS or allocation ceiling claims.

The independently verified `t40r1-neutral-08` v5 plan
(`sha256:a5e6867a94c594be4c69e36f1f42b3449e5f4ae139832c2336b04519093804e6`)
at commit `a019ec3b399d9b0459b1399b0191b6873c99a557` stopped honestly at the
four-hour structural cold deadline. Package
`sha256:6b7cc1fb775f10d07145f5b01c4d384ad0a0f141e08817034ba815f0cc3caaf9`,
observation
`sha256:b74c8e7f34ee391298bc6b6243814478b6f2839441737d60170c90516d4db2e5`,
and receipt
`sha256:607c7b42eb991613a4312af1f36304c2264d24d34fe50d734e040ee3f30ac6d8`
verify exactly. Startup was healthy in 12,828 ms, peak RSS was 3,874,963,456
bytes, and allocated custody was 20,081,238,016 bytes. The eight-hour parent
was not crossed and teardown destroyed custody.

The last typed observation snapshot was retained at 1,315,017 ms (21m55s),
with 63 of 64 partitions succeeded and one running. No later typed snapshot
was retained for about 3h38m. This diagnoses a post-21m55 observation gap, not
a confirmed visibility loss at 21m55s. The apparent last change at 14,400,001
ms and final `repository_visibility` stage came from observing the
deadline-canceled
inspection before checking the phase fence; they are not forward progress and
do not prove that the partition remained running at four hours. Take 8 is
therefore `unclassified` and `neutral-08` is permanently retired.

Take 9 keeps the four-hour full-convergence, 20-minute revalidation, and
eight-hour total-wall limits unchanged. V6 makes deadline cancellation a
terminal result—never a probe or transition—and gives post-health process exit
the distinct `server_exited_during_convergence` stopped result. The process
channel is checked before each inspection and selected concurrently with the
inspection result. Exit cancels HTTP and returns the terminal result without
waiting for the 30-second client timeout or an already-started bounded
synchronous filesystem/control read. Such a local read cannot be forcibly
interrupted; it drains in the inspection worker, cancellation fences prevent
any later inspection stage, and teardown waits for the worker before destroying
custody. Each retained transition binds wall time, closed stage, failure class,
and progress digest. Only a `pending` or `complete`
inspection advances the separately bound last-successful digest and wall time
(neither alone claims convergence); `transport`, `status`, `response`, and
`control` failures remain timeline-only. These six classes are closed.
The first 32 transitions fit the evidence envelope; a 33rd immediately stops
unclassified as `convergence_transition_limit_exceeded` rather than truncating
the timeline. Raw errors, process output, repository identities, paths,
responses, credentials, and source remain in destroyed custody. The two
unclassified terminal identities remain named through a simultaneous meter
failure; the eight-hour parent still wins first, while missing measurement
still governs every other failure. The added work is constant-size local
accounting plus one inspection worker and one buffered result channel per
sequential inspection over API/control reads the oracle already performs. Exit
or cancellation can leave at most one bounded local read draining; teardown
joins it before custody deletion. It adds no read, and production behavior is
unchanged.

The independently verified `t40r1-neutral-09` v6 plan
(`sha256:09f3d7325dbbc1d3e2d6323bd43b0348731f5ad2a34df7b2f33b564d0cf00e28`)
at commit `17cf54b85c45cbf25f01f30d910ffb7eaade40ec` also stopped honestly at
the four-hour structural cold deadline. Package
`sha256:bc8b7cea48d241bb780cf18da5523a677b5c49efe1b3f19931aa07e0040d6139`,
observation
`sha256:438c1c71f9677b92fa74d209ff797a8bcd8d6cc2e3c4b3993013aa25b1608c0b`,
and receipt
`sha256:4353c49a42204dea6a7f08e1982b66c61a750bcf4bfec1cc27dc4ed9f41b76ae`
verify exactly. Startup was healthy in 12,813 ms; peak RSS was 3,988,865,024
bytes and allocated custody was 20,089,499,648 bytes. Teardown destroyed
custody. The last successful typed snapshot at 1,345,019 ms reported 63 of 64
partitions succeeded and one running. At 1,352,032 ms the progress endpoint
entered one unchanged `status`-class tuple for the rest of 2,880 completed
inspections. V6 did not retain the numeric status or a closed response reason,
so this proves a persistent progress-surface failure but cannot distinguish
409 from 500 or prove whether the underlying final partition later completed.
Take 9 is `unclassified` and `neutral-09` is permanently retired.

Take 10 uses v7 while preserving v1-v6 bytes and every Take 9 deadline and
ceiling. A non-200 inspection retains its exact 100–599 status plus one closed
reason: `409_stale`, `409_control_absent`, `500_store`, `500_projection`,
closed 401/403/404/503 identities, or `status_other`. It also retains the last
completed stage, class, status/reason, progress digest, and wall time even when
an unchanged transition is deduplicated. Error bodies remain bounded to 4 KiB,
are mapped only through Huma's fixed detail strings, and are never retained.
Separately, current observation progress now projects a settled execution
schedule after finalization removes its marker. The current schedule's small
binding survives current-source cleanup, including recovery identities, and
is removed when a new source supersedes it. This makes the ceremony's
`current publication + settled schedule` predicate reachable without weakening
either authority. A current progress request adds one bounded schedule-binding
control read; it adds no member/source read, lock, child, cache invalidation, or
store transaction. Ceremony polling adds no request and performs one bounded
problem-detail decode only on non-200 responses.

The independently frozen `t40r1-neutral-10` v7 plan
(`sha256:c236269dcdfc0dbb580476fd9cda024f40ebb3485bd55b5c01a59d8b46ad9951`)
at commit `dd523dd5da0995c0810c2bfcfbe070119b146038` was consumed by its
approved manual execute attempt. `t4013-prepare` stopped at its host-toolchain
verification before custody creation. No prepared manifest, authored neutral
corpus, server, source-free observation, receipt, or scale/convergence result
exists. The old closed mismatch did not name the differing tool. Re-observing
the current host produces the exact signed plan bytes, so the transient
difference cannot be identified retrospectively.

The failed command then ran an EXIT trap whose `cleanup_pending` variable had
gone out of function scope under `set -u`. This secondary error did not hide
custody: preparation had stopped before custody existed. Take 11 permanently
retires `neutral-10`, uses `neutral-11`, and keeps plan v7 plus every exact Take
10 input, phase, rule, claim, and ceiling:

- 24 GiB minimum physical memory and 120 GiB minimum available disk;
- 15 minutes for server health, four hours for full convergence, 20 minutes
  for revalidation, and eight hours total wall;
- 20 GiB peak process-tree RSS, 96 GiB allocated data, and at most five
  attempts per production unit; and
- the unchanged twelve-phase sequence from preflight through teardown.

A host mismatch now reports only the closed tool name (`go`, `go-compile`,
`go-link`, `git`, or `surreal`), never a version, digest, executable path, or
raw cause. The driver installs a shell-escaped literal cleanup command so the
exact plan and prepared-manifest paths remain available after function scope
ends. These changes affect ceremony admission and cleanup only; they add no
production request, startup, sync, retry/no-op, publication, lifecycle, cache,
lock, corpus-read, memory, disk, or child-process cost. Take 11 still requires
a fresh exact source commit, unique signer, independent plan review, and
explicit execution approval.

Take 11 then stopped honestly at the exact four-hour cold-convergence deadline
with decision `unclassified` and reason `convergence_deadline_expired`.
Source-free transfer archive
`sha256:39e9945622ae32387fcfe5c05b81de8a732a8d075bf38f21e07bee06009abb52`
and its signed inner inventory verified; custody and the prepared manifest were
destroyed. Startup was healthy in 12,570 ms. The last successful typed probe at
19m00s reported all 64 observation partitions materialized, 62 succeeded, and
two running. At 19m07s the progress endpoint changed to HTTP 500
`500_projection`, which persisted through the final completed inspection at
3h59m55s. Peak process-tree RSS was 3,584,458,752 bytes and allocated data was
20,082,884,608 bytes, both below their frozen ceilings. Later gates did not
run. This proves a persistent progress-projection failure, but not whether the
two underlying partitions later completed.

The exact-source investigation reproduced two defects without the large
corpus: an atomic mutable pointer replacement could surface `ErrInvalid`
instead of retryable `ErrStale`, and cancellation during a cold publication
open was collapsed into an invalid member mismatch while leaving the cache
cold. Those defects narrow but do not retrospectively prove the cause of the
multi-hour Take 11 status because v7 retained no projection substage. Take 12
therefore retires `neutral-11` and introduces v8 while preserving every Take
11 input, phase, deadline, ceiling, stop rule, and nonclaim. Mutable pointer
identity changes and a crossed pointer/cache fence return 409 stale; immutable
publication corruption remains a fail-closed 500. Member validation preserves
context cancellation. One provisional current-generation cache entry pins
lifecycle identity and shares a cold validation among concurrent readers; a
failed or canceled open is removed and can be retried. V8 retains one of five
closed 500 substages—`500_projection_control`,
`500_projection_publication`, `500_projection_planning`,
`500_projection_schedule`, or `500_projection_response`—without retaining a
body, raw cause, path, repository/source identity, or process output. This
branch prepares but does not authorize a Take 12 execution; a new exact commit,
unique signer, independent plan review, and explicit approval remain required.

Take 12 was subsequently approved and executed from exact commit
`d91f3f2e01b6997b30984130aea6a58d33364dc5` with frozen v8 plan
`sha256:ff289204f3f8fe68b2dd20d7fe36b278f048f2f3d1f0c7e18d8329be93d36afc`.
It stopped honestly at the exact four-hour cold-convergence deadline with
decision `unclassified` and reason `convergence_deadline_expired`. Startup was
healthy in 13,031 ms. All 2,880 completed probes remained at
`repository_index` with one unchanged progress identity; no observation
progress request was reached. The cold phase observed three Git children,
three index children, two retry lifecycle events, 3,244,900,352 bytes peak
process-tree RSS, and 325,836,800 allocated data bytes. The receipt does not
establish whether the third index attempt was still running or had failed.
Source-free transfer archive
`sha256:91e8dbf962788180774bd7cc655d9507190fe68bb12ee9936c3fe0dbf2ebe3e1`
and its signed inner inventory verified. Custody and the prepared manifest were
destroyed; no later gate ran.

The exact-source `repository_index` investigation records the following
bounded findings. These are deliberately separated into verified facts and
inferences so a destroyed-custody run is not retrospectively overclassified:

- **Verified:** the ceremony polls `/api/repos` and hashes only the repository's
  committed `indexed_commit_hash` and legacy `latest_indexing_job_status`.
  Before a successful index publication, neither field reflects queue
  `pending`, `claimed`, `running`, `requeued`, `failed`, or `canceled`
  transitions. The unchanged Take 12 digest is therefore compatible with both
  live work and an already terminal job.
- **Verified:** the authenticated, permission-filtered `/api/repo-status`
  surface already performs one bounded latest-record-link projection per
  repository. Its `last_index_job_state=exact` projection supplies the current
  job's closed status and attempt count without scanning retained job history.
  The ceremony did not consume this existing authority.
- **Verified:** the ordinary runner permits three index executions by default;
  retryable failures after attempts one and two produce the two observed
  lifecycle retry events, and the process sampler counts distinct child PIDs.
- **Inference only:** Take 12's three observed index children plus two retries
  are consistent with a third attempt. They do not prove whether that attempt
  remained active, exhausted, or which child failure class occurred. V8
  retained neither the latest-job projection nor raw logs/errors, and teardown
  correctly removed the only material that could have resolved that question.

Take 13 implements the correction in ceremony code only. It permanently
retires `neutral-12`, selects `neutral-13`, and introduces v9 while preserving
every Take 12 input, phase, deadline, ceiling, stop rule, and nonclaim. The
inspector consumes the existing `/api/repo-status` projection and binds only
closed projection state, job status, and attempt count into the source-free
progress identity. An exact failed or canceled latest job before expected
publication stops immediately as unclassified `repository_index_terminal`;
unavailable, pending, claimed, running, and done-before-expected-publication
remain pending, while an already-published expected commit proceeds to the
downstream exact controls. The projection target, worker identity, timestamps,
and raw error are neither hashed nor retained. Table-driven small-data tests
pin every state, prove raw errors cannot change the digest, consume a real Huma
array response, and prove a terminal response ends the wait without reaching
its deadline.

This adds no history scan, corpus read, child, production mutation, or new API.
Each five-second ceremony probe replaces its existing `/api/repos` request with
one `/api/repo-status` request whose store work is bounded by current repository
cardinality; in the frozen neutral profiles that cardinality is one. Production
request, sync, startup, retry/no-op, publication, recovery, lifecycle, cache,
lock, memory, disk, and child-process behavior are unchanged. This correction
does not identify Take 12's third-attempt cause and does not authorize Take 13
execution, release, Epic closure, or Epic 41.

Take 13 was approved and executed from exact commit
`e8a1e7eaa112010f5a7b9115a8a3fbfd2b770217` with frozen v9 plan
`sha256:7aaea5863cc88a34f202bd6226d0b983a1dc47893e9d45e7211c6a091c5d83cf`.
The new oracle detected `repository_index_terminal` before publication, but
stopped teardown then returned `unlinkat .../custody: directory not empty`.
The command consequently returned no source-free observation and the driver
sealed no receipt. Its terminal job signal is useful operational information,
not independently verifiable ceremony evidence and not an official
classification. The driver's immediate plan-bound fallback cleanup succeeded;
subsequent inspection found no ceremony process, custody, or prepared manifest.
Only the original signed freeze files remained.

Exact-source reproduction established a bounded teardown race: macOS
`os.RemoveAll` can return transient `ENOTEMPTY` while a late stopped-child write
recreates an entry; an immediate second exact-path removal succeeds. Take 14
permanently retires `neutral-13`, selects `neutral-14`, and preserves the v9
plan/observation/receipt contract and every input, phase, deadline, ceiling,
stop rule, and nonclaim. All destructive custody paths now share one scoped
helper. It validates the absolute non-overlapping real-directory target before
every removal, retries only `ENOTEMPTY` or `EEXIST` at most ten times after the
initial attempt with 100-ms spacing, and requires a further 250-ms stable
absence fence. A symlink, scope change, permission failure, unexpected stat
failure, or exhausted retry bound still returns no observation and leaves the
driver's independently plan-bound cleanup in control. Tests inject the exact
late-writer error and prove retry success, stable absence, boundary retention,
and immediate refusal of non-transient errors.

The ordinary successful path adds one 100-ms post-removal transition plus the
250-ms absence fence because the loop observes removal on its next iteration;
a transient Take 13-shaped failure adds one extra removal call and another
100-ms delay. The hard worst case is eleven exact-path removal calls, one second
of retry spacing, and one 250-ms absence fence, excluding the filesystem syscall
duration. This work changes no production request, startup, sync, retry/no-op,
publication, recovery, lifecycle, cache, lock, memory, disk, or child-process
behavior. It does not retrospectively seal Take 13, identify the index-child
failure class, authorize Take 14 execution, close T40.13, or unblock Epic 41.

Take 14 was approved and executed from exact commit
`c1232d0a4e797eedbef129c178e5281913f20daf` with frozen v9 plan
`sha256:d54f7b63d398a45dbf23ab93bc011dc154d87acfb20b07ffe952d5f50227867e`.
It stopped with sealed decision `unclassified` and reason
`repository_index_terminal`. Startup was healthy in 11,273 ms. The exact
latest-job projection recorded pending attempt 0, running attempt 0, pending
attempt 1 after the 60-second backoff, running attempt 1, pending attempt 2
after the 120-second backoff, running attempt 2, then failed attempt 3 at
280,004 ms. Cold stopped at 291,458 ms with three Git children, three index
children, two retry reports, 3,479,928,832 bytes peak RSS, no publication or
authority change, and no later phase. The 325,738,496-byte allocated-data
value was the post-cleanup phase fence, not a transient peak. Teardown passed
in 1,033 ms and destroyed custody and the prepared manifest. Observation
`sha256:55f576e392352490fa7fbdd2a594df6ec959a0b80e587bae2653a21b0faabf88`,
receipt `sha256:078ade8d03d4023ebc73f448c7ed511ae623642b07624e9a287318c448c9d1fa`,
and source-free package
`sha256:ffefa975acbe463bd8c9e99489115f8ccbf2744ea284a57f53f0376aa5973bc0`
pass their signature, checksum, strict-validation, and byte-reconstruction
checks. This is not a scale, convergence, resource-refusal, release, or Epic
closure result.

## Take 14 harness review ledger

The post-stop review used only the frozen neutral corpus and exact source. Its
temporary authored corpus, indexes, prepared manifest, credentials, logs, and
custody were destroyed after the bounded reproduction. Findings are retained
here so a later take does not repeat a multi-hour gate merely to rediscover a
harness defect.

| Priority | Finding | Evidence | Disposition |
| --- | --- | --- | --- |
| Gate blocker | Fast queue polling also shortened every production job lease. | The exact pinned `zoekt-git-index` indexed all 2,000,002 files into 89 shards in 78.54 seconds at 3,315,744,768 bytes max RSS and 18,988,179,456 allocated bytes. Through Phebs with `sync.poll_interval: 250ms`, the same child was canceled when an 83-ms heartbeat request timed out, then requeued on the exact 60/120-second schedule. With a 15-second interval, its third diagnostic attempt published successfully. | Production runner defaults now use `max(interval/3, 5s)` for heartbeats and four times that for stale recovery. The ordinary 15-second default is byte-for-behavior unchanged; fast polls retain claim cadence but no longer create subsecond leases. V10 reduces a terminal raw error to only `lease_heartbeat` or `other`; raw text still never enters a digest or receipt. |
| Gate blocker | The pressure phase was host-dependent and had no guaranteed fresh capacity observation. | It only read the last in-memory lifecycle status and required `collect`; it created no pressure. On the ceremony filesystem, the retained generations and next reservation remain below the 80% soft watermark, and an idle lifecycle loop may not refresh for an hour. | V10 freezes an 82% target and an 80-GiB ballast maximum inside the existing 96-GiB custody ceiling. Preflight refuses before custody if normal capacity cannot reach that target inside the ceiling. The ceremony stops the ordinary structural server, preallocates one exact custody file, restarts the same production binary, requires a complete real collector cycle at `collect`, proves authority unchanged, removes the file, and requires a complete `normal` cycle. Reaching 90% remains a production refusal. |
| Evidence blocker | Stopped-run allocated bytes were measured only after child cleanup. | Take 14 retained 325,738,496 bytes although the independently successful child allocated about 18.99 GB before its staging data disappeared. | Each active phase now samples the same filesystem's available capacity once per second and combines the conservative trough with exact phase-fence `du` values. This is constant-cost `statfs` work and retains transient allocation without walking the corpus on every sample. |
| Cost/reliability defect | The interruption trigger recursively scanned derived artifacts and discarded traversal errors. | The loop could revisit 500,000 paths per root every second while waiting for a publication marker or stage. | It now reads only the three package roots, the one frozen repository directory in each, at most 4,096 direct controls, and the one named observation-inventory v2 control directory. Any read/scope error fails closed; immutable generations are never traversed. |
| Pre-freeze review defect | A byte target at the upper edge of 82% can report 83% because the lifecycle gate rounds upward, and the first v10 draft admitted `pressure-restart` as a historical startup label. | Both defects were found by boundary/schema review before any Take 15 plan was frozen. | The ballast selects the midpoint of the exact byte interval that reports 82%, leaving filesystem-metadata headroom. The new startup label is accepted only by v10; v1-v9 validation remains closed. Boundary and historical-schema regressions pin both facts. |
| Known bounded ceremony cost | Direct phase-fence disk gauges invoke `du` over custody metadata at meter boundaries. | These are finite ceremony measurements, not production request/sync/publication work, but they scale with the number of retained custody entries. | Retained because they supply an absolute workspace gauge and cross-check the constant-cost capacity trough. They never hash or read source contents, do not run in the one-second sampling loop, are charged to phase wall time, and remain inside the eight-hour parent gate. |
| Verification gap | Unit coverage did not execute the exact coordinator, lease/cadence interaction, pressure reachability, or transient-allocation evidence path. | The pre-review package statement coverage was 49.7%; several recovery and exact-reader helpers were at zero. | Table-driven regression tests now pin the lease floor, v10 pressure arithmetic and bounds, pressure-restart receipt inventory, capacity-trough retention, bounded interruption scanning, and permanent retirement of `neutral-14`. A full exact take remains necessary because authoring the two-million-file corpus is intentionally not a unit test. |
| Take 15 production blocker | A valid complete zero-unsupported observation receipt became invalid only during progress projection. | The canonical publication retained non-nil `unsupported_reasons: []`; two append-to-nil defensive copies changed it to nil and the validator correctly refused it. Take 15 therefore returned HTTP 500 after its last 62-succeeded/two-running snapshot. | Nil-preserving clones now retain `[]` through the real progress reader and HTTP response. The current pointer does not retrospectively prove final 64/64 schedule settlement. |
| Take 16 harness blocker | Recovery invoked live backup only after graceful server shutdown. | `recovery.Create` requires `store.ReadLocalRuntime` and the live supervised endpoint; server close removes the descriptor. Both unexecuted recovery phases therefore had an impossible command order. | V11 gives interruption one measured `interruption-backup` restart and exact A revalidation, takes both backups while their servers are live, then stops/resets/restores. Recovery command process trees and transient allocation are measured; concurrent RSS is conservatively summed. V1-v10 inventories remain closed. |
| Production observation-v2 replacement blocker | After v1 moved to B, the v2 enqueue/worker classified the still-current A inventory as corrupt. | Both B observation chunks exhausted five attempts with `inventory v2 current source authority`; `materialized=10`, `failed=2` was exact retry accounting, not stale schedule carry-over. | A mismatched but valid current inventory is retained only as bounded incremental prior input while B is enqueued and published. A focused A→B authority regression and the structural rehearsal pin the replacement. |
| Production resolver bridge blocker | Partitioned extraction roots were not declaration authority, and a fully settled empty declaration set remained an indefinite no-op. | The T40 whole-repository lane completed partition roots but resolver materialization still read only legacy focused outcomes. | Resolver identity and store publication accept the exact partition plan/root/run tuple; empty/unavailable terminal roots contribute settled absence and publish an exact empty resolver catalog. Legacy encoded bytes remain unchanged through additive omitted fields. |
| Production lost-wakeup blocker | Catalog, partition, resolver, and caller work could finish out of order; a fresh event coalesced into a backed-off pending job without making it claimable. | B could complete all internal authorities yet wait until the four-minute retry. A naive wake predicate also accepted a mixture of A and B domain rows. | Publication callbacks create bounded successors, `EnqueuePending` clears only `not_before` on a fresh event while preserving attempts, and readiness requires every configured domain's exact store plan/root to match one candidate/source/observation triple. Terminal/empty roots are included without requiring a success filesystem pointer. |
| Production recovery/lifecycle blocker | Source-free restore intentionally excludes rebuildable controls, but whole-search and partitioned extraction did not reconstruct all exact ownership required by readiness. | Restored authority could be valid while progress/lifecycle waited on missing sidecars or partition filesystem controls. | Cold restore reconstructs exact controls from store authority, restores whole-search lifecycle and archive receipt, and lets progress omit only a settled observation plan whose publication proves the current source. No reconstruction runs on warm requests. |
| Readiness result | Unit and inspection fixes alone could still miss cross-runner ordering and restore defects. | A real working-tree binary was run with the ceremony's semantic and structural projection profiles on the ordinary machine. | Semantic cold/live-backup/offline-restore/restored-query passed in 105.98s. Structural cold A→B→A-return/live-backup/offline-restore/restored-query passed in 161.45s. Both passed lifecycle; semantic required its citation. This is not a scale result or execution authorization. |

Take 16 is a prospective frozen-plan lineage, not an authorized ceremony. Its
permanently retires `neutral-15`, introduces v11, and preserves the frozen
corpus, twelve-phase order, four-hour full-convergence and 20-minute
revalidation deadlines, eight-hour parent, 20-GiB RSS, 96-GiB allocated-data,
82% pressure target, 80-GiB ballast, five-attempt ceiling, stop rules, and all
nonclaims. Its additional semantic restart exists only to satisfy the already
required production live-backup contract and is fully measured. The later-phase
audit and bounded production-path rehearsal are complete. Freeze still waits
for a clean exact commit, independent plan review, and explicit approval.

Take 16 was subsequently approved and executed from exact commit
`e9ceb4351c001fd0c09b9db98d5ced9f5d37dac4` with frozen v11 plan
`sha256:26c4c1c619bb8d1b985bf62fd74f162491df0b839feec53c8ca59572877c3d04`.
It stopped honestly at the exact four-hour cold-convergence deadline with
decision `unclassified` and reason `convergence_deadline_expired`. Startup was
healthy in 11,272 ms. Its last successful progress snapshot at 2,035,015 ms
reported 64 materialized observation partitions, 62 succeeded, and two
running. The final inspection was a closed `extraction_publication` control
failure. Cold peak RSS was 3,451,912,192 bytes and allocated custody was
25,223,991,296 bytes, both below their frozen ceilings. No later gate ran.
The source-free package
`sha256:a1f134cf27679d0f6214562f70a565bdbe3e2fe04be933f595410e059bf66523`
verified, and teardown destroyed custody and the prepared manifest.

## Take 16 extraction-publication investigation

The signed receipt proves the deadline and closed inspection class, but does
not retain a raw production error. The supervised live log, observed before
teardown, reported that extraction reconciliation exhausted three attempts on
`partitioned extraction publication limit exceeded`; the nested validator
identified partition 41 and `domain result aggregate exceeds its frozen
limit`. That operational diagnostic is not promoted into sealed evidence.
Exact-source inspection independently establishes the following cause:

- `BuildReservedPlan` reserves each admitted candidate-member partition's
  exact `Artifact.ContentBytes` as `MemberBytes` and one `Members` unit.
- `BuildDomainResultPlan` accumulates those reservations in ordinal order and
  rejects the first addition above `MaxDomainResultMemberBytes`, currently
  64 MiB. Therefore the partition-41 diagnostic is a deterministic planning
  refusal, not retry accumulation, root assembly, extraction output, memory
  pressure, or ceremony projection behavior.
- The retained T40.8 maximum-neutral member measurement is 954,368 bytes.
  Applying that already-retained measurement to T40.1's frozen 489-member
  structural shape requires 466,685,952 bytes (about 445.1 MiB), so the
  64-MiB T40.9 aggregate cannot admit even the previously reviewed structural
  model. The contract is internally inconsistent before any Take 16-specific
  path-length or encoding effect is considered.
- The Take 16 live candidate-operation diagnostic reported exactly 2,000,000
  repository-plane records in 489 members and 792,000,000 canonical member
  bytes. Only those source-free aggregate scalars are recorded here; the
  original diagnostic was custody-bound and is not sealed evidence. The next
  binary boundary above that exact population is 1 GiB. A 512-MiB correction
  inferred only from the older T40.8 single-member measurement would still
  refuse the frozen corpus.
- The 64-MiB value was selected as the next binary boundary above the semantic
  profile's 39,182,336 canonical *extractor-output* bytes, then also applied to
  cumulative candidate-member *input-control* bytes. Those populations have
  different scale drivers: output bytes follow semantic facts/rows, while
  member bytes follow candidate record count and path/control encoding.
- Existing exact-bound tests distribute synthetic member reservations inside
  64 MiB, and production integration tests build only tiny one-member domains.
  Both `go test ./internal/candidate ./internal/extractionpublication` packages
  pass, confirming the missing guard is a frozen structural-shape contract
  test rather than a broken subtraction-before-growth validator.

The smallest evidence-backed correction is therefore a reviewed 1-GiB
candidate-member aggregate while leaving the 64-MiB canonical/encoded result
ceilings unchanged. It must not silently replace the scalar with an unbounded
value. The production change should keep per-member validation, aggregate
subtraction-before-growth, immutable plan identity, and all result/output
ceilings unchanged; retain a source-free control-only measurement of the exact
792,000,000-byte structural population; add a regression that this exact shape
is admitted and one byte above 1 GiB is refused; and prove semantic and small
domains remain byte-for-behavior unchanged. Retries should also classify this
deterministic admission refusal terminally so an invalid plan is not rebuilt
three times. None of these findings authorizes a new ceremony, release, T40.13
closure, or Epic 41 progression.

### Take 16 investigation amendments (2026-08-12)

Independent re-verification of the investigation above confirmed every
recorded scalar and the mis-derivation finding, and supersedes the correction
paragraph in two respects and extends it in three. These amendments bind any
ticket that acts on this investigation.

- **Governance sequencing.** This file's discipline states "No threshold or
  production constant may be changed to turn a stop into a pass", and the
  Epic 40 boundary keeps existing per-domain safety limits "fixed unless the
  owning ticket measures that exact dimension and records a reduce-first
  decision"; the T40.13 acceptance criteria require stop decisions "without
  silently raising a bound", and every frozen plan claims
  `raises_production_bound: false`. A limit change landing directly on the
  next take's exact commit would therefore be a constant changed to turn a
  stop into a pass. The conforming sequence is two tickets. The first is a
  readiness ticket with no production-bound change: record this source-free
  derivation; record the governing reduce-first decision in `PLAN.md` and the
  owning ticket; convert aggregate-limit errors to closed `pipelinerefusal`
  data; classify the deterministic planning refusal terminal; add a bounded
  extraction status channel carrying generation state,
  total/pending/running/succeeded/failed partition counts, current-authority
  state, and the closed terminal-refusal dimension, observed value, and
  limit; teach the ceremony harness to retain those source-free fields and
  terminate on a terminal extraction refusal; and preserve raw Take 16
  evidence and v1–v11 validation unchanged. The second is the separately
  reviewed contract ticket described below. Reduce-first adjudication and
  independent review precede the second ticket; nothing in this file
  authorizes it.
- **Split the dual-use bound.** `MaxDomainResultMemberBytes` is both the
  whole-domain aggregate and the single-reservation backstop inside
  `validateDomainResultReservation`. Raising the shared constant to 1 GiB
  would silently widen that per-reservation backstop sixteenfold, above the
  128-MiB `PartitionMaxCandidateContentBytes` cap it backstops. The contract
  ticket must split the constants: the per-partition reservation bound keeps
  its existing protected value; the aggregate candidate-member input bound
  becomes a distinct limit. The earlier claim that per-member validation is
  kept "unchanged" is unachievable without this split.
- **Versioned plan compatibility.** `DomainResultLimits` is embedded in every
  persisted domain plan, digest-bound, and revalidated by exact equality
  against the current frozen limits, so a changed scalar invalidates every
  previously persisted plan control at recovery and restart boundaries. The
  earlier claim that "immutable plan identity" stays unchanged is wrong as
  scoped. The contract ticket must introduce a versioned v2 plan contract
  carrying the 1-GiB aggregate; persisted v1 plans continue validating
  against their original v1 limits; recovery, restart, backup/restore,
  archive, lifecycle, and downstream validators dispatch by schema version.
- **Exact derivation.** The corrected aggregate derives from the frozen
  generator, not from the observed corpus: 489 members × 4,096 records × a
  512-byte per-record encoding bound = 1,025,507,328 bytes, and the next
  established binary boundary is 1 GiB. The observed population is itself
  re-derivable source-free from the frozen generator and record encoder:
  2,000,000 records × 396 encoded bytes = 792,000,000 exactly; each full
  member is 4,096 × 396 = 1,622,016 bytes; ordinals 0–40 accumulate
  66,502,656, within the 67,108,864 allowed, and ordinal 41 reaches
  68,124,672. The recorded
  scalars therefore do not depend on the destroyed live log, and the exact
  792,000,000-byte admission fixture must be pinned from this derivation.
- **Throughput risk before Take 17.** With admission corrected, the four
  `.go`-enumerating domains contribute roughly 1,956 chunk work items
  serialized under `ScheduleRepositoryTokens = 1`; class concurrency cannot
  help within one repository. Take 16's observation phase left roughly 3.4
  hours of the cold window, pricing extraction at about 6.3 seconds per
  partition end to end. Before freeze: measure representative partition
  latency and compute cold-window feasibility from the measured distribution,
  not only the mean; confirm job and worker concurrency are exactly what the
  contract intends; inspect work performed per partition for repeated
  candidate/member reads and for repository-wide validation or hashing
  repeated per chunk; and exercise the new extraction status channel through
  a smaller complete generation, including interruption, retry, terminal
  failure, and stale-worker projections. Neither concurrency nor the
  four-hour deadline may be silently increased; if serialization cannot fit,
  that is a further measured architectural decision. The status channel also
  keeps an honest deadline informative: a future `unclassified` stop will
  still say whether extraction remained active, stalled, or failed, with
  exact bounded counts.

### Reduce-first readiness implementation

The first readiness ticket is implemented without changing the frozen
production limit. Domain-result aggregate failures now retain canonical closed
measurements, including the exact `candidate_member_bytes` pre-sum. A limit
refusal from plan construction is terminal on its first extraction-job
execution. The post-cutover repository projection exposes only the latest
extraction job's closed status, attempt count, and optional validated refusal;
raw error text and queue identity remain private.

`/api/extraction-progress` is authorization-first and authority-rechecked. Its
hot read uses two schedule point reads, one small generation control, and at
most one current-pointer control per configured domain. It does not open a
candidate member, source blob, observation member, partition result, domain
root, or evidence payload. V12 adds that bounded projection to ceremony
evidence while keeping v1-v11 bytes and validation unchanged. A closed limit
refusal terminates as substantiated `reduce`; another failed or canceled
extraction job terminates honestly as `unclassified`.

This closes only the first ticket. The separately reviewed split/versioned
aggregate contract and the representative serialized-throughput measurement
remain required before a Take 17 freeze.

### Versioned aggregate and throughput closure

The second ticket keeps v1 byte-for-byte valid at its original limits and emits
new plans as `phebs-extraction-domain-result-plan-v2`. V2 separates the 64-MiB
per-partition member reservation backstop from a 1-GiB aggregate candidate-
member input ceiling. The latter derives from 489 × 4,096 × 512 =
1,025,507,328 bytes rounded to the next established binary boundary. The
792,000,000-byte structural profile passes v2 and remains an exact v1 refusal.

Five fresh real-binary measurements each exercised four independently observed
serialized completions over exact 4,096-record partitions, frozen 4,608-byte
structural blobs, and 512 blob reuse classes. Total post-observation extraction
times were 13.190564417s, 10.275793666s, 10.251969458s, 10.122023667s, and
10.106630500s. Repeating the slowest whole four-domain sample for all 489
member ordinals is a conservative 6,450.185999913s (107.503m), within the
unchanged roughly 3.4-hour extraction allowance. No concurrency or deadline
changed. The complete representative A→B→A-return and backup/restore rehearsal
also passed.

The first live rehearsal exposed a Huma startup panic because two response
types shared the OpenAPI name `Progress`. Extraction now has the defined wire
type `ExtractionProgress`, and a complete route-registration regression pins
both components. `t40r1-neutral-16` is retired; the next fresh ID is
`t40r1-neutral-17`, subject to merge-bar review and separate execution approval.

### Take 17 transition-limit investigation

Take 17 stopped honestly as unclassified at 5,240,034 ms with
`convergence_transition_limit_exceeded`; signatures, checksum inventory, and
teardown all passed. The stop is a harness observability failure, not a
production bound or resource-ceiling refusal.

The source-free hashes re-derive two early extraction states exactly. The
2,021,622-ms transition is the canonical unavailable schedule plus unavailable
latest-job projection, and the 2,935,011-ms transition is the canonical zero-
generation-control inventory. The following 23 retained transitions can only
be produced after a generation control opened and `Runtime.Status` validated;
their changing hashes therefore bind changing partition-result/domain status.
The 22 intervals between those status snapshots have a 95,000-ms median,
89,999-ms minimum, 100,002-ms maximum, and 95,454.318-ms mean. The next changed
inspection became the unretained 33rd transition and selected the stop.

Four harness mechanisms compose the defect. The 32-transition ceiling was
introduced for Take 9, before typed extraction progress existed. V12 added the
typed endpoint without re-deriving that cardinality. While the endpoint is
valid but not yet current, the inspector falls through into a full
`Runtime.Status` scan; its expected "current extraction generation is
unavailable" result is classified as generic `control`; the returned hash-only
probe overwrites the typed extraction projection; and each changed status
digest consumes one of the 32 diagnostic entries. Thus ordinary forward
partition settlement necessarily exhausts an envelope sized for exceptional
inspection transitions.

Take 17 also invalidates the prior throughput extrapolation. The representative
fixture preserved a 4,096-record partition, 4,608-byte blobs, and 512 reuse
classes, but its repository had only 4,098 files rather than the frozen
2,000,002-file structural tree. On the real tree, one-repository-token,
one-item chunks produced the near-95-second status cadence above, far slower
than the small-repository sample. The hashes do not reveal exact counter deltas,
so Take 17 alone cannot state a final convergence duration, but it establishes
a serious cold-window throughput risk and disproves calling the earlier
107.503-minute projection conservative.

The v13 readiness correction now returns typed pending extraction progress
without scanning all partition-result controls, preserves first/latest digest,
exact aggregate change count, and last-change wall time without letting
ordinary progress consume the bounded anomaly timeline, and retains the
overflow inspection. A diagnostics-enabled execution also emits one bounded
record per attempt for source acquisition, extractor execution, result
installation, assembly, runtime, and scheduler settlement. The ceremony reads
those private records incrementally and seals only source-free counts, totals,
and maxima; diagnostics-off production work is unchanged. Historical v1-v12
evidence remains unchanged and validates through its original schema.
The current real-binary readiness rehearsal passed both semantic and structural
production paths in 258.192 seconds and required both partition-phase records
and nonzero scheduler-settlement durations before accepting cold convergence.

Take 18 is not frozen by this implementation. The versioned correction must
pass the merge bar, then repository-scale timing must be reviewed against the
unchanged cold window before a fresh plan and explicit approval.

### Repository-scale timing review

The opt-in exact structural diagnostic at source commit
`fada9cc8947934cdaac6cd194d4094b7ba95443a` completed its frozen 32-attempt
sample with zero failed or reused attempts. Observation reached current at
1,782,660 ms. The extraction schedule contained 1,956 materialized partitions;
at sample stop it reported 32 succeeded, one running, 1,923 pending, and zero
failed.

Per-attempt runtime was 95,650 ms minimum, 97,899 ms nearest-rank p50,
101,815 ms p95, and 102,067 ms maximum. Applying those repository-scale
measurements to the unchanged serialized 1,956-partition schedule projects
193,273,104 ms (53.687 hours) p50 observation-plus-extraction completion and
200,932,800 ms (55.815 hours) p95 observation-plus-extraction completion. Those
exceed the frozen 14,400,000-ms
four-hour cold window by 178,873,104 ms (49.687 hours) and 186,532,800 ms
(51.815 hours), respectively. Take 18 is therefore **not ready to freeze**.

The phase split identifies the wall precisely. Source acquisition consumed
3,136,191 of 3,149,203 aggregate runtime milliseconds (99.587%); extractor
execution used 12,115 ms, result build/install 297 ms, assembly 250 ms, and
scheduler settlement tracked runtime within 154 ms across the sample. Exact
source review shows `GitSparseSource.AcquirePartition` calls
`Reconciler.OpenDomain` for every scheduler item; that reopens the current
candidate through `Provider.OpenCurrentPublication`, whose strict
`candidate.OpenContext` validates the complete candidate publication before
the bounded sparse domain/partition is selected. The measured source-acquire
time therefore includes a repeated full 792,000,000-byte candidate validation
for each of 1,956 serialized attempts. Git blob reading and extraction are not
the dominant wall.

The next correction must preserve the strict authority fence while moving the
complete candidate validation to a generation-scoped cold-open shared across
partition attempts; each attempt may then perform only bounded immutable
domain/partition control reads plus its admitted blob reads. It requires
single-flight/cancellation/failure-eviction, generation replacement and stale
fencing, restart/recovery, retry/no-op, memory and descriptor accounting, and
an exact old-generation release test. No concurrency, deadline, limit, or
topology increase is justified by this result. The diagnostic stopped after
the planned sample, destroyed all authored source, logs, credentials, derived
data, and its temporary plan, and restored about 157 GiB available disk.

### Generation-scoped strict candidate open

The readiness correction implements that shape without changing an artifact
contract. The long-lived candidate provider retains at most one strictly
validated immutable `candidate.Publication` per repository and coalesces
concurrent opens for the same exact `candidate.State`. It does not trust the
cache as current authority: each `OpenCurrentPublication` call performs the
existing double-read persisted-pointer check before lookup and again after the
open or hit. If the pointer changes during the strict open, the result is stale,
is rejected, and its cache entry is evicted.

A successor state atomically replaces the repository entry, dropping the
cache's only reference to the old publication. Existing callers may finish
only if their post-open fence still matches. Waiting callers can cancel without
canceling another caller's open; a canceled/failed opener or nil publication is
evicted so the next attempt retries strict validation. Process restart owns no
retained cache state and reconstructs from the database pointer plus immutable
candidate bytes. Tests pin concurrent single-flight, waiting-caller
cancellation, opener failure eviction, successor replacement, pointer change
during open, retry, and restart reconstruction under the race detector.

Per settled attempt the change adds no new database work: it preserves the four
bounded pointer reads already made by the pre/post double-read fences. The hot
path adds one brief cache mutex acquisition and retains one bounded manifest and
analysis-unit view per repository. It retains no candidate-member contents,
Git blobs, file descriptors, or repository lock. Sparse root/domain/partition
controls and admitted immutable Git objects remain independently validated and
bounded per attempt. Candidate, sparse, plan, result, and evidence bytes remain
unchanged. The correction changes no concurrency, deadline, limit, topology,
claim, or release posture, and it does not freeze Take 18. A fresh exact
repository-scale timing diagnostic at its committed source is still required.

The required diagnostic passed at exact source commit
`cd5dc7fd669b3f5a902995d0bbaebbea58195604`. Observation became current at
1,813,056 ms and the structural schedule retained 1,956 total partitions. The
bounded inspector observed 36 completions between polls while waiting for its
32-sample threshold; all 36 succeeded with zero failures, terminal refusals,
or reuse. Runtime was 427 ms minimum, 434 ms nearest-rank p50, 442 ms p95, and
445 ms maximum. Projecting the unchanged serialized schedule gives 848,904 ms
p50 and 864,552 ms p95 extraction, or 2,661,960 ms (44.366 minutes) and
2,677,608 ms (44.627 minutes) observation-plus-extraction completion. Headroom
inside the frozen
14,400,000-ms window is 11,738,040 ms (3h15m38.040s) p50 and 11,722,392 ms
(3h15m22.392s) p95.

The phase accounting confirms the intended mechanism. Across 36 attempts,
source acquisition consumed 1,026 ms with a 32-ms maximum; executor work used
13,650 ms with a 388-ms maximum, result work 294/12 ms, assembly 278/12 ms,
and total runtime 15,633/445 ms. The previous repeated full-candidate source
acquisition wall did not recur. Diagnostic custody, private logs, credentials,
authored source, derived data, and the temporary plan were destroyed, and about
156 GiB free disk was restored. This closes the repository-scale timing
prerequisite for a separately reviewed Take 18 freeze decision; it is not a
ceremony pass, execution authorization, Epic 40 closure, release, SLO, or
public scale claim.

That headroom is not an end-to-end ceremony forecast. Resolver, relationship,
delta/replacement, recovery, authenticated product replay, and every later
frozen phase remain unmeasured at the two-million-owner shape inside a
ceremony. Take 18 is the first authorized mechanism capable of testing those
risks under the total-wall and resource ceilings. The diagnostic establishes
enough phase-local room to justify a freeze review, not a predicted pass.

### Take 18 disposition and Take 19 readiness

Take 18 is a verified, source-free `unclassified` stop at the unchanged cold
deadline, not a scale pass. Its retained record is summarized in
`take18-findings.json`. The structural profile nevertheless completed its cold
convergence for the first time: 1,956 of 1,956 extraction partitions completed,
none failed or refused, relationship authority published, and the complete
profile converged in 3,910,284 ms. The corrected generation-scoped candidate
open held: source acquisition totaled 55,100 ms with a 46-ms maximum and the
maximum complete partition runtime was 1,326 ms. The v13 evidence coalescer
also retained the 65-minute run in five transition entries while counting 165
ordinary extraction progress changes.

The semantic profile reached a different terminal state. Observation planning
changed to `failed` by 115,006 ms, with one failed one-item planning schedule,
and every successful probe through 14,395,007 ms decoded that same state. The
v13 inspector did not treat failed observation planning as terminal, so the
harness waited until 14,400,003 ms and recorded the generic deadline. The
sealed record carries no closed planning refusal and therefore cannot say why
the job failed. Peak process-tree RSS was 3,353,821,184 bytes and allocated
data was 25,439,391,744 bytes, below the frozen ceilings. Later gates did not
run; evidence verification and teardown passed and custody was destroyed.

Exact-source reproduction supplies the missing mechanism without claiming it
came from the sealed failure record. The semantic profile freezes 262,144
unique Go blobs. The legacy v1 observation generation admits at most 250,000
records, while the selected segmented v2 inventory admits 4,000,000. At the
Take 18 source, enabling v2 still enqueued and required a fresh v1 publication
before v2 planning. The legacy bound therefore refused the semantic profile
before the admitted selected route could run. This is a production cutover bug,
not a reason to increase either bound or change the corpus.

Take 19 readiness removes that hidden prerequisite. A selected v2 runtime now
binds its one-item inventory schedule directly to the current immutable source
generation and the v2 pointer. Existing v1 publications remain valid historical
authority but are neither rebuilt nor required for current v2 work. Active and
failed legacy schedules cannot block the selected lane; a settled v2 worker
whose store completion response was lost is recovered through a new immutable
schedule identity. Deterministic closed v2 limit/invalid refusals settle on the
first attempt, while stale authority, cancellation, store availability, and
notification failures retain ordinary retry behavior.

The selected `/api/observation-progress` v2 response reads one source manifest,
one current v2 pointer and bounded inventory root, and one one-item schedule.
Only a terminal failed schedule adds one failed-chunk point read, from which
only an exact canonical `pipelinerefusal` receipt may escape; raw durable error
text never enters the API or ceremony evidence. The reader rechecks source,
pointer, and schedule before returning and never scans source or observation
members. Take-plan v14 requires this selected route, seals the last decoded v2
planning/publication counters and optional closed refusal, and stops within one
probe when planning is terminal. A closed limit refusal selects substantiated
`reduce`; any other terminal planning state stays honestly `unclassified`.
Historical v1-v13 plans, observations, and receipts retain their original
validation.

Steady-state cost contracts rather than expands. Startup/source publication
enqueues one one-item v2 schedule and avoids the obsolete full v1 generation.
Progress requests remain constant in corpus size and add no member reads,
child process, cache, or new lock. Successful v2 publication uses the existing
filesystem/store fences and callback; deterministic failed retries contract to
one attempt. The immutable v2 generation has the same existing disk and memory
limits, while skipping v1 removes duplicate generation work. No production
limit, deadline, concurrency, topology, service cap, lifecycle policy, or
release posture changes.

Take 19 is not frozen by this correction. The exact branch must pass the full
merge bar and independent review before a fresh commit-bound plan is prepared;
execution still requires separate explicit approval.

The opt-in production-binary readiness rehearsal was treated as a merge gate,
not a smoke test. Its first restore cycles found three compatibility gaps:
canonical explicit-gap downstream authority was rejected by a dependency-low
validator, the caller resolver adapter discarded the partitioned declaration's
authority schema/plan/root fields, and the schema-full caller publication table
did not declare the v2 generation's optional `upstream_digest`. The rehearsal
also showed lifecycle legitimately collecting a successful observation
planning schedule and an ordinary completed extraction job before a later
probe. Current immutable v2 observation authority now retains the settled
one-item planning proof; current typed extraction authority survives ordinary
job collection, while non-current work and exact terminal jobs remain
fail-closed.

After those corrections, the same production-binary test passed in 283.90
seconds: semantic cold plus restore in 91.50 seconds, and structural cold
A-to-B-to-A-return plus restore in 180.45 seconds. Both profiles passed
lifecycle and authorized query replay on cold and restored instances. The
structural extraction timing sample completed in 3.80 seconds for four
partitions and nine domains. This rehearsal changes no production threshold or
ceremony deadline and does not substitute for Take 19.

The first independent review therefore refused freeze. It found that terminal
observation and extraction probes were decoded but discarded before their
typed projections could enter evidence, while v14 validation required those
same projections. The tracker now retains the bounded terminal projection
without counting it as successful convergence progress, and an end-to-end
wait/stop/validate test pins both closed-bound and other-terminal observation
outcomes. `reduce` additionally requires a canonical refusal on the exact
selected stage, generation, and dimension; malformed or unrelated limit-shaped
receipts remain unclassified. The four new stop codes are v14-only, and freeze
preparation checks that every required frozen observation profile fits selected
v2 admission.

Production review also narrowed terminal invalid handling to deterministic
immutable-input boundaries. Pointer/publication/collection race windows remain
retryable, a selected-v2 worker that already claimed a legacy schedule settles
it without opening source or rebuilding v1, and v2 binding/enqueue now shares
the collection mutation fence. Race CI includes observation publication and
the stale progress fences and publication callback have direct coverage.

The exact-shape fit blocker remains deliberately separate. Run the committed
262,144-blob semantic profile through the ordinary binary and retain only its
cold wall, peak RSS, and logical/allocated data scalars:

```sh
PHEBS_T4013_EXACT_SEMANTIC_TIMING=1 \
  go test ./spike/t4013 -run '^TestExactSemanticColdTiming$' \
    -count=1 -v -timeout=5h30m
```

A passing measurement must fit the unchanged four-hour cold, eight-hour total,
20-GiB RSS, and 96-GiB allocated-data ceilings. Until that committed-source
record and independent re-review exist, Take 19 remains not ready to freeze.

That exact committed run failed the fit gate. At
`bcfd01c871a6c37e4dda7d03d8bfdb7bdb3b4b57`, selected-v2 observation completed
without the legacy v1 refusal and extraction began at about 24 minutes, but the
final `repository_visibility` inspection crossed the four-hour context; the
test ended at 14,462,200 ms. Resource use was not the wall: the workspace was
about 2.6 GB and combined live RSS about 0.15 GB near the end. Cleanup destroyed
the exact fixture, derived data, and credentials and restored disk baseline.

The timeout path did not retain final extraction counters or phase timing, so
the result is intentionally narrow: this commit does not fit the cold window,
but the retained record cannot identify the unfinished count or justify a
specific optimization. Do not freeze Take 19. First add bounded timeout
progress/timing retention, then remeasure and reduce the semantic
extraction/relationship tail without increasing deadlines, concurrency,
production limits, topology, or resource ceilings.

The bounded timeout capture was then rerun at exact committed source
`5d776ef5a982a11987789ea24cf914506d4fd2bc`. It retained the real terminal
extraction state at 14,400,019 ms: the 264-partition schedule had settled with
226 succeeded, 38 failed, zero pending, and zero running; relationship
publication never began. Across 290 materialized attempt reports, 226 completed,
32 failed retryably, and 32 were terminal refusals. Executor work dominated
the retained timing (3,495,321 ms total), with a 300,002-ms maximum; source
acquisition totaled only 5,526 ms and result installation plus assembly totaled
4,075 ms. The maximum aligns with the frozen five-minute partition deadline,
but the source-free fit record does not retain a closed refusal dimension,
observed value, and limit. It therefore proves a terminal extraction wall but
does not yet govern whether the correction is corpus reduction or production
work, and it does not authorize changing the deadline, concurrency, or bounds.

Resources again were not the wall: peak RSS was 1,674,674,176 bytes and
allocated data was 2,618,982,400 bytes, below the unchanged 20-GiB and 96-GiB
ceilings. The exact fixture, derived data, and credentials were destroyed and
the temporary directory was removed. Take 19 remains not ready to freeze. The
next readiness change must retain and validate the closed per-partition refusal
tuple in the ordinary typed extraction projection and the source-free timing
record, then independently review the resulting reduce-or-correct decision.

That attribution prerequisite is now implemented without changing a bound.
The production partition executor closes deterministic failures at domain
inventory, extractor execution, or evidence staging before returning a
terminal partition result. Diagnostics-enabled partition timing v2 carries the
six closed refusal scalars only on terminal attempts, while historical v1
reports remain valid and contribute to an explicit unknown count. The retained
source-free aggregate has at most 32 canonical refusal summaries and must prove
that summarized plus unknown refusals equal every terminal attempt; malformed,
duplicate, mixed-limit, and partial summaries fail validation. The inspector
also stops immediately when an extraction schedule is settled with failures,
even if lifecycle already collected the ordinary job row, while a failed or
canceled exact job retains precedence and an active schedule remains pending.

The change adds no inspection request or corpus read and does not alter the
five-minute partition deadline, worker concurrency, production limits,
topology, resource ceilings, or gate oracle. Take 19 is still not ready to
freeze: the next action is one fresh exact semantic fit diagnostic at committed
source, followed by independent review of the closed refusal summary and a
governed reduce-or-correct decision.

The pre-freeze audit then found one later ceremony-only blocker. The frozen
configuration enables nine extractor domains and `inspectExtraction` sums all
nine current roots, but the final exact oracle still expected the T40.1
IDL-only aggregate: 49,152 facts and 98,304 rows. The semantic Go corpus also
freezes 65,536 literal Kafka producer inputs and 65,536 dynamic-topic producer
gaps. Complete publication therefore means exactly 180,224 facts and 360,448
rows. Finalization now pins those totals, all nine domains for both profiles,
and zero structural facts/rows. Focused tests derive the Kafka addition from
the frozen families and reject the stale IDL-only total, a missing domain, and
unexpected structural evidence. This is a ceremony comparison correction only;
it changes no production behavior or bound. It also does not satisfy the
remaining exact-fit and independent-review prerequisites, so Take 19 stays
unfrozen.

The fresh exact diagnostic at commit
`430523356c821facc63746635f1821784b1ec870` then stopped at 5,262,005 ms with
`extraction_schedule_terminal`. Selected-v2 observation completed and
extraction entered at 1,459,006 ms. The settled 264-partition schedule retained
225 successes, 39 failures, and no pending or running work. Its validated
timing floor accounts for 293 attempts: 225 completed, 36 retry failures, and
32 terminal refusals. All 32 terminal attempts share one closed refusal:
`evidence_staging`, `extraction_domain`, `limit`, `facts`, with limit 768 and
maximum observed 769. Peak RSS was 1,637,482,496 bytes and allocated data was
2,628,976,640 bytes, both below the frozen ceilings; relationship publication
never began. Cleanup destroyed the exact fixture, data, and credentials, left
no process, and returned disk to baseline.

The exact-source derivation explains the 32-way shape. T40.9 freezes 49,152
facts per domain; distributing that aggregate across the Kafka domain's 64
candidate partitions reserves 768 facts per partition. The semantic corpus
contains two contiguous 65,536-input Kafka-producer families, or 16 full
4,096-record partitions each. Every dense partition therefore reaches the
first one-over reservation at fact 769. This is a measured production-contract
refusal, not a cold-window or resource failure. Do not raise only the fact
limit: the owning T40.9 correction must first measure facts, rows, references,
canonical bytes, and encoded bytes for the complete Kafka result, preserve
historical v1/v2 plan validation, record a reduce-first decision, and pass
independent review. Take 19 remains unfrozen.

The follow-up all-dimension replay is retained in
`take19-kafka-result-measurement.json`. It uses the frozen baseline-A author,
the real `kafka-producer` extractor, and the production 256-fact chunk encoder.
Across the 32 emitting partitions it measures exactly 131,072 facts, 262,144
rows, 131,072 references, and 101,386,432 canonical/encoded bytes. Each
emitting partition carries 4,096 facts, 8,192 rows, 4,096 references, and
2,873,414–3,463,238 bytes. Since reservations are currently spread over all
64 Kafka candidate partitions, an equal allocator needs 262,144 facts,
524,288 rows, 262,144 references, and 256 MiB canonical/encoded to admit that
shape, while the 64-MiB per-partition byte backstop can remain separate and
unchanged. These numbers do not change a production bound or authorize a
freeze.

The prior fit also proves a distinct unresolved population: 39 settled failed
partitions minus 32 terminal refusals leaves seven exhausted nonterminal
partitions, and the maximum executor wall was 300,002 ms against the exact
five-minute context. The aggregate cannot prove which domain failed or whether
every one was a deadline. Timing v3 therefore retains only domain, a closed
`deadline`/`canceled`/`other` class, and six fixed executor-duration buckets;
it carries no repository, path, content, partition identity, or raw error.
V1/v2 reports remain valid, and mixed history is assigned to an explicit
unknown bucket. Run one fresh exact committed-source semantic diagnostic with
these diagnostics before selecting a v3 output contract, optimization, or
partition-specific re-shard. Do not lower the shared candidate-v4 4,096-record
packing limit without that attribution. Take 19 remains unfrozen.

The fresh current-bound attribution fit is retained in
`take19-semantic-attribution-fit.json` at exact commit
`9acf808cc8cc86e06184ae92e7cca578f450a05d`. It stopped honestly at
5,088,005 ms with a settled 264-partition schedule: 226 succeeded, 38 failed,
and no pending or running work. The 290-report timing floor reconciles exactly
as 226 completed, 32 retryable failures, and 32 terminal refusals. The refusal
population is unchanged: every terminal refusal is a Kafka-producer facts
limit at 769 over 768. The newly closed population is entirely deadline class:
proto-contract has 12 failed attempts and thrift-contract has 20, with no
canceled, other, or unknown failure. Schedule arithmetic closes those attempts
as two exhausted proto and four exhausted thrift partitions after five tries;
the two completed proto partitions account for the remaining two retry failures.
Proto reached 300,002 ms and thrift 300,001 ms. Peak RSS was 1,576,796,160
bytes and allocated data was 2,631,065,600 bytes, below the frozen ceilings.
The diagnostic destroyed its private fixture, data, credentials, and children.

This attribution rules out a typed-input-specific correction for the measured
wall. `proto-contract` and `thrift-contract` use ordinary IDL candidate members;
their failures select domain-specific smaller packing, measured against the
unchanged five-minute deadline. Do not lower the shared 4,096-record pack or
raise the deadline. The next controlled diagnostic must combine that reduce-
first IDL packing with the separately measured versioned Kafka all-dimension
output contract, preserve historical candidate/result-plan validation, and
pass independent review. It is not a ceremony and does not authorize Take 19.

The coupled provisional implementation now exists for that controlled
diagnostic. New `kafka-producer` result plans use v3 aggregate reservations of
262,144 facts, 524,288 rows, 262,144 references, and 256 MiB each of canonical
and encoded bytes. Canonical and encoded bytes retain separate 64-MiB
per-partition backstops; every non-Kafka domain continues to emit v2, and
persisted v1/v2 plans retain their original exact limits and validation. The
candidate policy identity also carries an optional focused-local record bound.
Its absent value preserves historical 4,096-record policy JSON and packing;
only `proto-contract` and `thrift-contract` select 2,048. This doubles those
two semantic local projections from four to eight members each without
changing repository, caller, Go-plane, typed-input, or structural packing.

Focused tests pin exact V3 dimensions, per-partition one-over rejection,
Kafka-only V3 selection, historical V1/V2 acceptance, omitted historical
candidate identity, strict local-projection replay, and the unchanged default
packer. Candidate construction still scans the tree once and writes eight
additional bounded local artifacts for this profile; the resulting semantic
schedule is expected to rise from 264 to 272 work items. No deadline,
concurrency, retry policy, global record pack, partition quota, topology,
resource ceiling, or lifecycle rule changed. This remains provisional until a
fresh committed-source exact semantic diagnostic proves complete extraction
and relationship convergence with no refusal or deadline exhaustion. Take 19
is still not ready to freeze.

The corrected semantic fit at exact commit
`1dcf8daf179eff17bd9e74e8b8a0eb65d60bcbae` stopped before partition
scheduling at 1,717,012 ms with `extraction_job_terminal`. Observation
completed, extraction entered at 1,455,004 ms, the extraction job exhausted
two attempts, and no partition or timing report materialized. Peak RSS was
1,608,663,040 bytes and allocated data was 2,224,709,632 bytes, below the
unchanged ceilings. Cleanup removed the exact fixture, derived data,
credentials, and children.

Exact-source inspection closes the pre-schedule cause. The reconciler passes
the admitted v3 Kafka reservations to `BeginPartitionedExtractionRun`, but the
store's opaque partition-run and published-domain envelopes still accept only
the original v2 maxima: 49,152 facts, 98,304 rows, and 98,304 references. The
v3 Kafka request is therefore rejected before a run identity or schedule can
exist; its retry is bit-identical. This is an incomplete propagation of the
already measured versioned contract, not an executor, deadline, or resource
failure.

Do not freeze Take 19. The correction must version the store-side partitioned
run/publication envelope for `kafka-producer` v3 while retaining the original
v1/v2 maxima for every historical plan and all other domains. Focused tests
must reject v3-sized limits outside that exact domain/contract and preserve
historical persisted controls. After independent review, rerun the exact
semantic fit; only complete extraction and relationship convergence can
authorize the freeze.

The reviewed store-envelope correction was integrated at exact commit
`7632918dff47a09f465c8b328c0555ccfc53e10d`, and the required fresh diagnostic
is retained as `take19-post-store-envelope-fit.json`. It stopped at
9,009,505 ms with a settled 264-partition schedule: 232 succeeded, 32 failed,
and relationship publication never began. The 394 timing reports reconcile as
232 completed, 162 deadline failures, zero terminal refusals, and zero reuse.
Kafka producer completed 40 attempts and failed 122; Proto and Thrift contract
each failed 20. Only five Kafka failures reached the full 300-second partition
deadline. Kafka's other 117 failures and every Proto/Thrift failure ended below
60 seconds. Peak RSS was 1,749,188,608 bytes and allocated data was
2,773,061,632 bytes, both below the unchanged ceilings. The temporary source,
derived data, credentials, and children were removed.

This result corrects two readiness assumptions. First, the pinned SurrealDB Go
client applies a 30-second WebSocket request timeout. The production executor
stages 256-fact chunks and reads their accounting inside its measured executor
phase; the retained T40.7 writer receipt covers only one sequential
12,500-fact/25,000-row run in 202,272 ms. Kafka v3 now admits an actual
131,072-fact/262,144-row run shared by the production two-worker scheduler.
The sub-60-second deadline population therefore requires an exact-shape,
current-schema writer measurement and bounded append-versus-accounting phase
attribution before selecting an optimization. Do not raise either the client
request timeout or the five-minute partition deadline. Reduce the append
transaction's repeated serialized run-counter work so every bounded chunk and
accounting read completes inside the existing request boundary.

Second, the expected 272 work items never existed on this route. The optional
2,048-record policy controls only focused local projections. The exact v2
service profile intentionally keeps a shared whole-repository physical
candidate generation, so sparse execution continued to map one work item to
each 4,096-record repository member and retained 264 partitions. Do not
repurpose focused-local identity or globally repack candidate artifacts.
Version a whole-repository execution-partition subrange contract, preserve
bounded source authority and historical sparse/result controls, and account
for any repeated candidate-member reads or new derived bytes. Independently
review that correction together with the writer correction before one new
exact fit. Take 19 remains unfrozen and no ceremony command below is
authorized.

### Focused failure-point iteration

Use these opt-in gates while implementing the two corrections; ordinary CI
skips them. The whole-repository shape check uses five source-free Proto
records in one shared candidate member, so it isolates the missing execution
subrange contract without authoring the ceremony corpus:

```sh
T40R1_PARTITION_SHAPE_DIAGNOSTIC=1 \
  go test ./internal/candidate \
  -run '^TestT40R1WholeRepositoryPartitionShapeDiagnostic$' \
  -count=1 -v
```

The pre-correction baseline failed in about 0.18 seconds with one `[5]`
partition instead of `[2,2,1]`. The corrected focused gate is green through
the separately versioned whole-repository execution-subrange contract; it does
not globally repack candidate members or broaden the focused-local identity.

The writer check opens the production disk-backed supervised store and stages
the measured Kafka actual population—32 emitting partitions, 131,072 facts,
262,144 rows, and 131,072 references—as 512 production-sized chunks under two
workers. It records bounded append/accounting phase counts and duration
buckets, exact completed charges, first request failure class, Go allocation,
and SurrealDB identity/RSS, then cancels at the first request failure:

```sh
T40R1_KAFKA_WRITER_DIAGNOSTIC=1 \
  go test ./internal/store \
  -run '^TestT40R1KafkaWriterFailurePointDiagnostic$' \
  -count=1 -v -timeout=90m
```

Set `T40R1_KAFKA_WRITER_RESULTS_PATH` to an absolute path only when retaining
the source-free JSON summary for review. This test bypasses repository
authoring, indexing, observation, unrelated extraction domains, scheduler
retries, relationship publication, backup, and product replay; it does not
replace historical/recovery/maximum-shape tests or the integrated exact
semantic fit. Run that full fit only after both focused gates and their
steady-state-cost review pass.

The first exact writer-only run is retained as
`take19-kafka-writer-failure-point.json`, SHA-256
`2da127865732aa1f8b84b3d17a8f295c85299009f03e9d7488d9f06e29ab55ff`.
It used the production disk-backed store and stopped after 2,169,379 ms with
145 append attempts and 143 completed chunks: 36,608 facts, 73,216 rows, and
36,608 references. The first closed failure was `append` / `deadline` at
30,007 ms and the sibling append canceled. The append histogram contains four
phases below one second, 39 below ten seconds, 62 below thirty seconds, and 40
at or above thirty seconds, with a 348,077-ms maximum. The histogram is not
paired to individual outcomes, so it does not claim every long append failed.
All 143 accounting reads completed below one second, with a 3-ms maximum and
zero failures. Peak SurrealDB RSS was 456,048,640 bytes, its measured delta was
346,636,288 bytes, Go allocation was 2,272,171,560 bytes, and cleanup left no
child.

This assigns the correction to `AddEvidenceChunk`: replace the repeated
per-association and per-assertion updates of the shared extraction-run counters
with a chunk-bounded exact charge while preserving transaction rollback,
new-versus-replay behavior, conflict refusal, exact row/reference accounting,
two-worker concurrency, maximum-shape admission, publication reconciliation,
recovery, and historical controls. Re-run this same command after the change.
Do not raise the SDK request timeout or the partition deadline.

The corrected writer result is retained as
`take19-kafka-writer-aggregate-fix.json`, SHA-256
`c6041cf5ff599cff9b01281286a3324845c578254b5851161fef888e3199963d`.
It completed all 512 chunks and the exact 131,072 facts, 262,144 rows, and
131,072 references in 41,625 ms under the same two workers. All 512 appends
were below one second with a 262-ms maximum; all 512 accounting reads were
below one second with a 3-ms maximum; deadline, canceled, and other failures
were zero. Peak SurrealDB RSS was 932,823,040 bytes, its measured delta was
841,908,224 bytes, and Go allocation was 4,265,987,176 bytes while retaining
the full population.

The production transaction retains its first extraction-run update as the
serialization lock and exact fact/chunk reservation. It now point-reads only
the at-most-256 submitted association/assertion IDs, computes exact row and
reference-union deltas transaction-locally, applies three bulk atom/
association/assertion writes and one final guarded aggregate run charge, then
creates the chunk receipt before the same commit. A full chunk therefore uses
exactly two shared run-row updates instead of as many as 513 and three bulk
writes instead of 768 per-record write statements. Exact replay writes
nothing. Regression coverage proves overlap union, replay, attribute/direction
conflict rollback, one-over-limit rollback, the 256-fact boundary, concurrent
accounting, publication reconciliation, recovery, and historical controls.
The full store suite passed. This clears the focused writer lane only: the
whole-repository execution-subrange correction and independent combined review
remain required before one new integrated exact fit. Take 19 remains unfrozen.

The execution-shape gate is now corrected as well. Proto and Thrift local
policies separately bind `whole-repository-execution-subrange-v1` with a
2,048-record execution maximum. On the unitless shared-repository route, the
sparse builder scans an immutable candidate member once and emits contiguous
domain-relative ranges; each partition digest carries the policy, bound,
start, and end. The reader deliberately reopens the same member for every
range, and the descriptor reserves the full repeated bytes. It authors no new
candidate member or derived source payload. Focused local publications still
use physical `MaxRecords` projection packing and carry none of the new range
fields; historical policy and partition JSON therefore retains its omitted
field bytes.

The ordinary and opt-in five-record proofs both produce `[2,2,1]` with exact
once-only coverage, one construction read, three execution reads, and
result-plan preservation. Rebased overlap, gap, wrong-bound, and
missing-authority shapes fail validation. An ordinary maximum-member test
produces exactly two 2,048-record partitions from one 4,096-record candidate
member while construction still reads that member once. This completes both
focused corrections, not Take 19: independent combined review and one new
integrated exact semantic fit remain required before any freeze decision.

The combined post-integration audit accepts the corrected writer and execution
shape for one committed-source integrated diagnostic. It adds a production
`GitSparseSource` proof: five selected Git paths are visited exactly once over
`[2,2,1]`, three leases bind the same immutable candidate member, and all three
full-member reads are charged. The scoped Kafka diagnostic was rerun from the
merged correction and completed 512/512 chunks, 131,072 facts, 262,144 rows,
and 131,072 references in 41,375 ms; maximum append was 292 ms, maximum
accounting was 3 ms, and every failure class was zero. The exact semantic fit
now emits `t4013-take19-semantic-fit-v4` and requires the final authoritative
snapshot to report exactly 272 applicable and 272 settled extraction
partitions with zero retry exhaustion. A converged historical 264-partition
route therefore fails the gate explicitly.

This review authorizes the exact diagnostic only. Its source-free result still
requires separate evidence review before any Take 19 freeze decision. No
ceremony command below is authorized yet.

That integrated diagnostic is now retained as
`take19-integrated-caller-refusal.json`, SHA-256
`c8134b908665f22c385a7483c99661800d19c6db82f3195434e6018729131f02`, at
exact source commit `1da4ada790bdf56ffcc4f8a03c4ce8c4c9fa00bf`. It proves
the corrected extraction route: selected-v2 observation reached 262,144
records, the schedule materialized exactly 272 partitions, all 272 succeeded,
zero failed, and all nine domains became current. Relationship publication did
not follow. Forty first-attempt-success caller-leaf jobs durably settled 192
pair outcomes, but only 38 outcomes succeeded; 154 are terminal generation
refusals, split as 58 `grpc-caller` and 96 `thrift-caller`. The successful gRPC
artifacts alone contain 100,306 abstentions, 306 above the frozen 100,000
aggregate maximum. Their largest successful pair contains 4,094 abstentions
against the 4,096 per-pair maximum.

Do not infer that all 154 pairs exceeded the abstention limit. The current
caller outcome stores no refusal dimension, observed value, or limit. The
exact harness also does not recognize terminal caller admission and would wait
until the four-hour deadline even though immutable convergence is impossible;
the diagnostic was stopped after 3,573,161 ms and therefore emitted no v4
fit/resource record. Before choosing reduction or correction, add a bounded
typed caller-refusal projection, stop the diagnostic on terminal caller
admission, and run a scoped exact caller-lane gate that classifies every refusal
and remeasures aggregate results, abstentions, canonical bytes, and staging
bytes. Do not raise the per-pair or aggregate limit in isolation. Take 19 is
still unfrozen, and no ceremony command below is authorized.

That prerequisite is now complete. Caller terminal outcomes retain exact
closed pair-execution, artifact-seal/install, or generation-admission receipts.
The terminal admission derives at most 32 source-free summaries from exact
outcomes; excess distinct measurements become one explicit generation-level
unknown summary rather than blocking durable settlement. Failed exact Caller
Map HTTP/MCP pages project the bounded summaries, and the T40.13 inspector now
stops immediately only when complete failed progress proves the frozen caller
generation aggregate-abstention refusal. Untyped or differently typed terminal
state remains unclassified.

Use this focused source-free failure-point diagnostic for caller correction
iterations. Ordinary tests validate the retained receipt without logging it;
the environment flag prints the complete source-free projection:

```sh
T40R1_CALLER_FAILURE_POINT_DIAGNOSTIC=1 \
  go test ./internal/callerexecute \
  -run '^TestT40R1ExactCallerFailurePointMeasurement$' \
  -count=1 -v
```

It invokes production `ExecutePair` at the first and last input of every frozen
source family plus each control for both direct protocols, requires equal
endpoint receipts, then applies the exact frozen cardinalities. It bypasses
repository authoring, indexing, observation,
scheduling, store writes, relationship publication, backup, and product
replay. The retained result is `take19-caller-failure-point.json`, SHA-256
`f320e8f588a4e20e8f553373ae0891d52d2c280c7b13aa10327a1b62cd629304`.

Both frozen profiles have zero resolver descriptors, so every candidate emits
one no-direct-match abstention for each of the two protocols. Semantic exact
output is 0 results, 524,290 abstentions, and 105,906,544 canonical/staging
bytes; only aggregate abstention count crosses, by 424,290. Structural exact
output is 0 results, 4,000,002 abstentions, and 844,000,368 canonical/staging
bytes; abstentions cross by 3,900,002, canonical bytes by 307,129,456, and
staging bytes by 298,740,848. Every per-pair result, abstention, canonical,
record, staging, and source-byte bound fits. Therefore a count-only
increase is invalid, and smaller caller leaves cannot repair generation
aggregates. The next correction must separately version a compact aggregate
no-resolver/no-direct coverage representation while preserving exact candidate
coverage, explicit gaps, publication, recovery, lifecycle, and historical
authority. This diagnostic establishes no scale pass or freeze authority.

That correction is now implemented as
`direct-syntax-compact-coverage-v2`. When and only when the exact protocol
resolver contains zero descriptors, production `ExecutePair` replays the
immutable candidate member without opening Git blobs and emits one certificate
binding the pair/member plus no-direct and explicit gap counts. Use this fast
scoped gate for correction iterations:

```sh
T40R1_CALLER_COMPACT_COVERAGE_DIAGNOSTIC=1 \
  go test ./internal/callerexecute \
  -run '^TestT40R1CompactCallerCoverageMeasurement$' \
  -count=1 -v
```

The retained result is `take19-caller-compact-coverage.json`, SHA-256
`b0486178f8d4af6fd2be03e72ffa49c1075bbd4cb2fe0043d4c90a6a983e2799`.
For both gRPC and Thrift, a maximal 4,096-record member with the maximum legal
member-name length and all four gap kinds produces one 955-byte coverage
record, zero result/abstention rows, zero source reads/bytes, and zero
out-of-leaf reads. At the unchanged 16,384-pair ceiling, conservative
canonical/staging content is 15,646,720 bytes. Exact logical coverage is
4,000,002 for structural-2m-v1 and 524,290 for semantic-262144-v1, both below
the V2 policy's 67,108,864 maximum. The old failure-point receipt remains
historical evidence of V1 behavior.

This clears only the scoped caller gate. Run the full merge bar and obtain an
independent review before separately authorizing a new integrated exact
diagnostic. It does not authorize the ceremony commands below, freeze Take 19,
advance Epic 41, or establish a scale, SLO, release, migration, or
decommission-safety claim.

The separately authorized `t40r1-neutral-19` run then bound exact source commit
`6f02dc2ae6c15b400d6d9f358f558e358d3025ea` and reviewed plan
`sha256:e392bacb787c27f5032874831fab35426cd6412f04aab115e695a33f90ff281e`.
Its outer process sequence advanced from structural server execution to
semantic cold convergence before exact Caller Map authority reported a
terminal caller generation without publication. The executor recorded stage
`caller_generation` and outcome `caller_generation_terminal`; the v14
source-free validator had not enumerated that stage, either caller terminal
outcome, or their terminal-transition coherence. Observation validation failed,
so the driver sealed no receipt and exact teardown destroyed custody, the
prepared manifest, credentials, logs, and processes. This consumed run cannot
establish structural completion, classify the caller cause, or validate the
compact correction.

The harness correction admits `caller_generation` plus
`caller_generation_bound_refusal` and `caller_generation_terminal` only for the
v14 detail contract and requires their last transition to be terminal. Earlier
receipt contracts remain closed. Focused regression coverage drives both states
through the actual convergence recorder, stopped classification, and complete
observation validation. A fresh identifier, exact plan, independent review, and
separate rerun authorization are required; do not reuse `t40r1-neutral-19`.

The separately authorized `t40r1-neutral-20` run then bound exact source commit
`9a9052e74a18abf0bb47f54b08f208a6d4769742` and reviewed plan
`sha256:2813a57a862ed0f498a5522eeb25d4ea616c8e0456cbb4cf13c8da6889e24dad`.
Structural cold convergence completed; semantic cold convergence stopped when
exact Caller Map authority reported a terminal caller generation before
publication. The corrected v14 observation validator sealed the source-free
`pipeline/caller_generation_terminal` stopped observation with successful
teardown. Receipt construction then rejected the observation because its
separate stopped-failure switch omitted both caller classifier codes. No
receipt, signed inventory, or transfer bundle was sealed, and exact teardown
destroyed custody and private material.

The second harness closure binds
`caller_generation_production_bound_refused` to final wait outcome
`caller_generation_bound_refusal` and binds `caller_generation_terminal` to
its matching final wait outcome, both only in v14. The regression now drives
the real terminal probe through stopped classification, observation
validation, receipt construction, and receipt decode. The exact-checkout rule
means corrected code cannot retroactively seal Take 20; use a fresh identifier,
plan, independent review, and separate execution authorization.

The next focused diagnostic stops short of another full ceremony and begins at
the caller boundary Take 20 had reached. Set
`T40R1_CALLER_TERMINAL_WITNESS=1` and run
`TestT40R1CallerTerminalDiskWitness`. It preserves the retained source-free
semantic cardinality—262,145 candidates, 96 leaves, two protocols, and 192
pairs—while prevalidating upstream observation/extraction authority and
supplying the frozen fixture's expected zero-descriptor resolvers. The
production worker then executes one pair per turn through caller artifact
installation, the supervised disk-backed SurrealDB store, admission, complete
publication, and the shared product reader, followed by a final no-op turn.

The retained `caller-terminal-witness.json` has SHA-256
`b6d0b265e6b3e2a80698fe54ea2f8a0fab43a0b3ccca871777d674349cfe7be3`.
All 192 outcomes succeeded; admission retained 192 coverage records, 524,290
covered candidates, zero results, abstentions, or refusal summaries, and
106,856 canonical/staging bytes. Publication authority and the product reader
were both `current`. This rules out compact pair execution, artifact
installation, caller outcome/admission persistence, complete publication, and
reader validation when resolver authority is actually empty. It does not
reconstruct Take 20's destroyed resolver descriptors, exact candidate-member
bytes/distribution, or upstream partitioned-authority state, and therefore
cannot distinguish a terminal admission from deterministic authority or
publication rejection in that consumed run. Classify and retain that actual
closed origin before another full ceremony. This source-free boundary witness
is not a scale pass and authorizes no rerun or freeze.

The next opt-in step moves exactly one boundary upstream. Set
`T40R1_CALLER_UPSTREAM_WITNESS=1` and run
`TestT40R1CallerTerminalUpstreamWitness`. Instead of injecting empty direct
resolvers, it publishes one real protobuf declaration evidence run, executes
production resolver materialization over the retained 262,145-record candidate
projection, and seals/installs/publishes the canonical three-member catalog.
The production caller worker opens that exact catalog for gRPC and Thrift; both
views contain zero descriptors. The same 192 pairs settle and admit, and the
shared publication registry carries the result directly into an authorized
exact Caller Map request.

The retained `caller-terminal-upstream-witness.json` has SHA-256
`8409977bd830fe69880b870719e2b1a4cbb6c9648555c4d1b88a668ce2cec5db`.
It records current resolver, caller-publication, and reader authority plus a
`caller-map-v2` response with exact declaration authority, current generation,
exact matching-row state, zero rows, and pair/candidate/coverage parity with
the durable publication. The diagnostic adds two 262,145-record resolver-input
passes, 524,290 caller candidate callbacks, one declaration publication, one
resolver publication, 192 compact artifact/outcome writes, one caller
admission/publication, one diagnostic resolver reopen, and one exact zero-row
request. It adds no production steady-state work. This clears resolver
materialization and exact product projection for the closed empty-resolver
state; it still deliberately starts before partitioned observation/extraction
authority and cannot recover Take 20's destroyed resolver population or failed
read. Move that exact authority/origin boundary next. This is not a scale pass
and authorizes no rerun or freeze.

The next scoped step crosses that authority boundary. Set
`T40R1_CALLER_PARTITIONED_AUTHORITY_WITNESS=1` and run
`TestT40R1CallerTerminalPartitionedAuthorityWitness`. It creates a small real
Git control repository, publishes its current observation-v2 generation, and
publishes exact current two-partition empty gRPC and Thrift plan/result roots.
The production caller worker must bind their usable aggregate digest before it
can replay the retained semantic 262,145-record, 96-leaf, 192-pair path through
the resolver, durable outcomes/admission/publication, shared reader, and exact
Caller Map.

The first red run isolated a real defect: all 192 outcomes and complete
publication succeeded, but exact Caller Map returned a failed generation. The
worker's caller identity included canonical upstream bytes and their digest;
the store summary kept only the digest, so the reader reconstructed a
historical no-upstream generation and rejected the exact filesystem state. The
fix stores the canonical payload once on the complete publication/summary,
keeps only the digest in each outcome and admission identity, shares exact
worker/reader derivation, validates reconstruction, and rechecks compact
upstream authority after lease acquisition and at result serialization. A
digest-only transitional pointer is available only to startup reconciliation,
which queues replacement before clearing it.

The retained `caller-terminal-partitioned-authority-witness.json` has SHA-256
`e2f222bb799e0d10fdbec223e78c75840f64bf41877b90dbe385d1a43fc9790e`.
It records one observed record, two candidate control records, two current
partitions per required caller domain, exact aggregate digest binding, all 192
successful pairs, and exact current zero-row product parity. The candidate
control contains only two real records while the caller plan deliberately
replays the prior semantic cardinality; this therefore does not prove the
exact physical member bytes/distribution, Take 20's destroyed resolver
population or failed response, a scale pass, or ceremony readiness.

The production cost is bounded control-plane work. Publication stores one
canonical digest-bound at-most-64-KiB payload, not one copy per
as-many-as-16,384 outcomes.
Open derives compact authority once before selection and once after acquiring
the lease; reopen and result fences derive it once, and identical paired reads
are deduplicated. A derivation reads the observation pointer/root/pointer and
one small current-root control for each required caller domain, with no source,
corpus, or shard scan, new child, new lock/cache, or unbounded hashing. Startup
adds at most one payload per already-bounded current publication, 64 MiB at the
1,024-publication maximum. Retry/no-op, sync, and unrelated publication paths
are unchanged. The next scoped boundary is the production physical candidate
plan/provider over exact member distribution, not another ceremony.

The next opt-in witness crosses that physical provider seam. Set
`T40R1_CALLER_PHYSICAL_PROVIDER_WITNESS=1` and run
`TestT40R1CallerTerminalPhysicalProviderWitness`. It builds the small real Git
control directly into the production candidate root, publishes the exact
candidate pointer, replaces the synthetic caller plan with
`candidatejob.Provider`, and opens the narrow physical plan through
`candidate.OpenCallerPlanContext`. The first diagnostic attempt stopped before
provider opening because the fixture omitted the empty candidate-root
directory required by `candidate.Build`; the fixture now creates it explicitly.
No production defect or correction was needed.

The retained `caller-terminal-physical-provider-witness.json` has SHA-256
`e1b73fcc2d783d4d0c90158f7562bb672f1c5726c962b84d2b0c22a77dbd6bd0`.
It binds one immutable 344-byte leaf containing one caller record, including
its exact name, ordinal, prefix, declared bytes, and content digest. Production
membership replay visits that record once for gRPC and once for Thrift. The
worker performs three plan opens across the two pair turns and admission,
settles both pairs, and completes four turns including the final no-op.
Admission and publication retain two coverage records, two covered candidates,
and 1,338 canonical/staging bytes; the shared reader and authorized Caller Map
are current and exact with zero rows.

This source-free control clears the production physical candidate-plan,
store-pointer, leaf-envelope, selected-member replay, and downstream parity
seam. It does not materialize the retained semantic 96-leaf/262,145-record
shape, exercise descriptor-present resolver or Git-blob reads, reconstruct
Take 20's destroyed resolver/response, establish scale, or authorize ceremony.
Production behavior and cost are unchanged. Existing active job turns each
read and parse the bounded candidate manifest and validate all caller leaf
envelopes without opening their contents. Each unsettled pair then reads and
hashes its selected bounded leaf under the existing repository work lock; the
admission turn repeats the plan open without member replay, and the final
empty-queue turn opens neither. Retry repeats the same bounded control/member
work. No query/request, sync/startup, publication/lifecycle, cache, child, new
lock, source/shard scan, or bound changes. A further scoped diagnostic should
either retain a provider-only 96-leaf physical distribution or move to the
descriptor-present Git-blob seam, not launch another ceremony.

The provider-only multi-leaf diagnostic now supplies that retained physical
distribution without entering caller execution. Set
`T40R1_CALLER_PROVIDER_96LEAF_DIAGNOSTIC=1` and run
`TestT40R1CallerProvider96LeafPhysicalDistributionDiagnostic`:

```sh
cd ~/phebs

T40R1_CALLER_PROVIDER_96LEAF_DIAGNOSTIC=1 \
  go test ./spike/t4013 \
  -run '^TestT40R1CallerProvider96LeafPhysicalDistributionDiagnostic$' \
  -count=1 -timeout=20m -v
```

The test authors 261,769 deterministic regular paths in a real bare Git
repository while sharing one 32-byte blob and builds candidate-v4 through the
production enumerator/hash/splitter, publishes the exact store pointer, opens
`candidatejob.Provider`, and resolves the narrow plan through
`candidate.OpenCallerPlanContext`. The retained production shape is 96 leaves:
32 six-bit leaves carrying 129,114 records and 64 seven-bit leaves carrying
132,655 records, with 1,953–4,096 records per leaf. Both gRPC and Thrift replay
every leaf and all 261,769 records with equal digests. Manifest digest, control
revision, and provider leaf envelopes are exact.

The retained source-free receipt is
`caller-provider-96leaf-physical-distribution.json`
(`sha256:48e1b1928cb167611577017f155c4b6ced5d858787e3fc58441c877db024cdc4`).
It binds 8,376,608 declared corpus bytes, 93,975,071 physical leaf-content
bytes, 117,543,780 peak spool bytes, 192 leaf replays, 523,538 record visits,
and 187,950,142 member-read bytes. The retained rerun measured 47.725s and
11,725,784,832 allocated bytes for build, 0.680ms and 676,256 bytes for provider
open, and 1.879s and 1,844,617,040 bytes for the two full replays. Candidate
artifacts occupied 94,015,507 bytes; the shared-blob bare repository occupied
686,208 bytes. These values describe this diagnostic run, not production
ceilings or an SLO.

The exact production splitter also demonstrates why the logical 262,145-record,
96-leaf witness cannot be treated as a physical distribution promise: leaf
count depends on the path hashes, and this deterministic physical family has
98 leaves at 262,145 records while the retained 261,769-path control has 96. This
diagnostic invokes no resolver materialization, descriptor-present resolution,
Git blob read, pair execution, outcome/admission/publication, product request,
sync/startup/retry/lifecycle transition, or ceremony. It changes no production
cost. It clears only the provider multi-leaf physical distribution seam. Move
next to descriptor-present resolver/Git-blob pair execution; do not launch
another ceremony. No rerun, scale/SLO, release, Epic closure, or Epic 41
progression follows.

The next opt-in diagnostic crosses that descriptor-present physical-pair seam.
Set `T40R1_DESCRIPTOR_GIT_BLOB_DIAGNOSTIC=1` and run
`TestT40R1DescriptorPresentGitBlobPairDiagnostic`:

```sh
cd ~/phebs

T40R1_DESCRIPTOR_GIT_BLOB_DIAGNOSTIC=1 \
  go test ./spike/t4013 \
  -run '^TestT40R1DescriptorPresentGitBlobPairDiagnostic$' \
  -count=1 -timeout=10m -v
```

The harness authors a real seven-file neutral Git commit, builds and
store-publishes candidate-v4, and reopens its six caller records in four
immutable leaves through `candidatejob.Provider`. It publishes one exact
protobuf declaration assertion and materializes the committed module, layout,
generated-attribution, and generated-client inputs into a current two-member
resolver catalog. The resulting descriptor binds
`example.invalid/root/gen/grpc.OrdersClient.Get` to `/orders.Orders/Get`, the
exact declaration lineage, and the exact generated Git object.

The selected physical `10` leaf contains only `consumer/call.go`. The harness
passes no `ReadBlob` override to `ExecutePair`, so production's default bounded
`gitobj.ReadBlob` reads exactly that 121-byte object. Direct syntax resolution
emits one `CALLS_OPERATION`; the pair seals, installs, and rereads the exact
result with zero abstentions, compact coverage records, or out-of-leaf reads.
The retained source-free receipt is `descriptor-present-git-blob-pair.json`
(`sha256:1952ebce6ed4b0b3dcafa35962b1375a65565b625525ac70551c4f8555d7288e`).

On the retained rerun, candidate build took 66.0ms, resolver materialization
66.8ms, resolver open 0.426ms, and pair execute/seal 18.0ms. Resolver
materialization made four bounded Git reads totaling 713 bytes; pair execution
made one bounded read totaling 121 bytes. Candidate, resolver, installed caller
artifact, and bare-repository disk were 7,394, 8,050, 1,369, and 27,812 bytes.
These are diagnostic observations, not production ceilings or an SLO.

The opt-in test changes no production behavior or steady-state cost. It
exercises the existing bounded candidate passes, named resolver-input reads,
selected-leaf validation, OID-bound Git read and SHA-256, one Go parse, one
artifact write, and one artifact reread. It does not run the caller worker
queue, outcome/admission/complete publication, product reader/Caller Map,
startup/recovery/lifecycle, multi-descriptor ambiguity, Thrift, Take 20's
destroyed state, or the 96-leaf scale shape. If diagnosis continues, compose
this descriptor-present pair through production worker outcome, admission,
complete publication, and exact product parity; do not launch another
ceremony. No rerun, scale/SLO, release, Epic closure, or Epic 41 progression
follows.

The composed opt-in diagnostic crosses those worker and product boundaries.
Set `T40R1_DESCRIPTOR_PRODUCT_PARITY=1` and run
`TestT40R1DescriptorPresentProductParity`:

```sh
cd ~/phebs

T40R1_DESCRIPTOR_PRODUCT_PARITY=1 \
  go test ./spike/t4013 \
  -run '^TestT40R1DescriptorPresentProductParity$' \
  -count=1 -timeout=10m -v
```

It adds a real current two-record observation-v2 inventory and the required
current gRPC extraction-domain authority to the same seven-file fixture. The
sparse domain has zero partitions, so its complete root is retained honestly
as `unavailable_prerequisite`; this is an explicit usable gap authority, not an
absent generation or fallback. The exact commit is cloned into the managed Git
mirror. Production caller execution then opens and settles all four physical
pairs in six worker turns including the final no-op. All four outcomes succeed,
admission accepts four installed artifacts, and the aggregate is one resolved
result plus five abstentions with zero refusals, compact coverage records, or
covered-candidate substitutions.

The complete caller pointer binds the same canonical observation/extraction
payload and digest that the shared product reader independently rederives.
Authorized exact Caller Map returns one current `caller-map-v2` row for
`consumer/call.go`, classified `resolved_caller` with `syntax` resolution and
the `protobuf` protocol. The operation, declaration lineage, Git object, source
blob digest, heuristic tier, and production code role are exact, while every
generation count matches admission and publication. The declaration fixture
stores the catalog's canonical no-leading-slash object; resolver and caller
facts retain `/orders.Orders/Get`, as required by the exact query contract. The
standalone pair receipt remains byte-identical.

The retained source-free receipt is `descriptor-present-product-parity.json`
(`sha256:a11683cf3a3ab77800de66fec23970182e34df6edd54763c2c683b1588b67ede`).
The retained rerun measured 258.6ms for the six worker turns and 13.0ms for the
reader plus exact product request; caller and observation artifacts occupied
12,152 and 11,298 bytes. These are observations, not production ceilings or an
SLO.

This diagnostic changes no production behavior or steady-state cost. Its
opt-in setup performs one small source census/observation publication, one
zero-partition extraction-root publication, and one managed bare clone. The
existing worker performs one bounded provider/leaf replay and Git blob read per
unsettled pair, four outcome/artifact writes, and one admission/publication
transition; its final no-op only rechecks the bounded current authority.
The exact request performs the existing compact authority derivation, complete
publication validation/lease, bounded record scan, and final authority fence.
It adds no ordinary query, sync, startup, recovery/lifecycle, lock, cache,
child, corpus/shard scan, or bound. Retry repeats the same bounded selected-pair
work.

This clears the small descriptor-present worker outcome/admission/publication
and exact product-parity seam. It does not combine descriptor-present execution
with the retained 96-leaf physical distribution, reconstruct Take 20's
destroyed state, exercise Thrift or ambiguity, establish scale/SLO, or
authorize a ceremony. If diagnosis continues, the next scoped boundary is a
descriptor-present physical multi-leaf worker/product run; do not start another
ceremony. No rerun, release, Epic closure, or Epic 41 progression follows.

The next opt-in diagnostic composes descriptor presence with the exact
multi-leaf physical shape. Set `T40R1_DESCRIPTOR_96LEAF_PRODUCT=1` and run
`TestT40R1DescriptorPresent96LeafPhysicalWorkerProductDiagnostic`:

```sh
cd ~/phebs

T40R1_DESCRIPTOR_96LEAF_PRODUCT=1 \
  go test ./spike/t4013 \
  -run '^TestT40R1DescriptorPresent96LeafPhysicalWorkerProductDiagnostic$' \
  -count=1 -timeout=70m -v
```

The final fixture authors 261,770 regular files over 72 real Git blobs. Its 64
neutral blobs each have at most 4,096 placements, preserving the observation
plane's per-blob bound. Candidate-v4 contains 261,769 caller records in exactly
96 leaves: 32 six-bit leaves with 129,114 records and 64 seven-bit leaves with
132,655 records. One deterministic split-control path restores that exact
shape after substituting the descriptor inputs; it changes no production
policy. The first fixture attempt used one shared neutral blob and correctly
stopped at 261,762 placements against the 4,096 observation limit. A later
one-path adjustment also stopped at 4,097. Both were fixture refusals; the
retained 72-blob run is within the existing authority envelope.

The real consumer path hashes into physical leaf 0. It follows 1,522 earlier
members in that leaf, then resolves to the exact `/orders.Orders/Get`
operation, lineage, Git object, and generated descriptor. Resolver
materialization makes the same four bounded reads totaling 713 bytes. Current
observation-v2 contains 67 observed physical blobs, the required gRPC domain
root retains its complete zero-partition `unavailable_prerequisite` state, and
worker and product derivations bind the same upstream digest.

The run reproduces the terminal caller boundary. Descriptor presence disables
the empty-resolver compact path, so nonmatching candidates become individual
`no_direct_caller` abstention records after their exact source scans. The
worker completes 38 artifacts, including the real caller result, then reaches
100,245 aggregate abstentions against the frozen 100,000 limit. It records the
remaining 58 pairs with one exact `caller_generation_admission` / `caller` /
`limit` / `caller_generation_abstentions` refusal and settles terminal
admission after 40 turns including the final no-op. There is no complete
publication pointer. The shared reader returns `failed` from
`terminal_admission`, and authorized exact Caller Map returns a failed,
unavailable, zero-row page with complete 96/38/58 progress and the identical
refusal tuple. The product does not silently report zero callers.

The retained source-free receipt is `descriptor-present-96leaf-product.json`
(`sha256:17512e7d9c8f46c312051bcfaf27a57d08a10df8662e7f70755475f1d596736d`).
The final run made 100,243 bounded Git reads totaling 9,222,381 bytes and zero
out-of-leaf reads. Candidate build took 45.28s, resolver build 1.51s,
observation build 34.96s, worker convergence 19m50.43s, and product projection
13.54ms. Build and worker allocated 11,025,801,744 and 5,756,329,744 bytes.
Candidate, observation, and caller artifacts occupied 85,639,602, 34,689,060,
and 19,749,671 bytes. These are diagnostic observations, not ceilings or an
SLO.

No production behavior or steady-state cost changes in this diagnostic. The
existing descriptor-present path performs one bounded immutable Git read,
SHA-256, Go parse, and direct scan per eligible source candidate until an
aggregate refusal is known. Each successful turn rereads the bounded manifest,
validates its 96 leaf envelopes, opens one selected member, writes one artifact
and outcome, and holds the existing repository work lock. The terminal turn
derives the aggregate once and records the remaining bounded refusal outcomes;
the no-op rechecks compact authority. Product failure projection performs only
the existing bounded authority, admission, progress, and refusal reads. No new
query, sync, startup, retry, recovery/lifecycle, cache, lock, child, or bound is
introduced by the test.

Do not raise the 100,000 abstention bound and do not start another ceremony.
The reduce-first correction is to finish the exact descriptor-present scan but
compact a pair that emits no resolved or unresolved caller fact into one
count-bearing coverage record. Any pair containing a resolved or unresolved
fact remains fully materialized, so evidence and uncertainty are not hidden.
The correction must preserve exact candidate/gap accounting and historical
bytes, and add schema/backward-compatibility, mixed result/unresolved,
maximum-shape, terminal recovery, complete publication, exact reader/Caller
Map parity, and steady-state-cost tests. Only then should this scoped diagnostic
be rerun. The current receipt is not a scale pass, Take 20 reconstruction,
ceremony authorization, release, Epic closure, or Epic 41 progression.

The versioned reduce-first correction is now implemented as
`direct-syntax-zero-fact-coverage-v3`. Descriptor-present execution still
finishes the exact bounded member scan. Only when that scan emits neither a
resolved caller nor an `UNRESOLVED_CALLER` fact does the stage replace its
input-only abstention stream with one `phebs-caller-leaf-coverage-v2` record.
That record binds the pair and candidate member, retains source-read
count/bytes on the receipt, and partitions every candidate among no-direct,
catalog-owned, domain-unselected, excluded-`go_test`, invalid-UTF-8,
resolver-generated, and source-too-large counts. A resolved or unresolved
fact keeps every record in the pair artifact. Historical V1/V2 generation,
policy, artifact, and receipt bytes remain readable; the adapter digest keeps
V3 output distinct and startup queues replacement before clearing a V2
current pointer.

The scoped physical rerun completed the unchanged 261,770-file,
261,769-candidate, 96-leaf shape. All 96 pairs succeeded and admitted/published
after 98 turns including the no-op. The result-bearing first leaf retained one
resolved result plus 4,055 abstentions. The other 95 zero-fact leaves became
95 coverage records covering 257,713 candidates. Durable receipts retained
261,764 source reads totaling 24,082,313 bytes and zero out-of-leaf reads.
The shared reader returned `current`, and authorized exact Caller Map returned
the one `resolved_caller` row plus pair/result/abstention/coverage counts equal
to admission and publication. The retained receipt is
`descriptor-present-96leaf-product-v2.json`
(`sha256:43e3a82e1c3897bd62f14150a1c0d9352d396030cc4f0bd1a1959f1f282b029b`).
The historical refusal receipt remains byte-exact at
`sha256:17512e7d9c8f46c312051bcfaf27a57d08a10df8662e7f70755475f1d596736d`.

The first successful full execution exposed two diagnostic-oracle defects
after product projection: the success check required a terminal-only reader
admission field, and its leaf-set summary used the new receipt schema as a
salt even though physical membership was unchanged. Correcting those test
oracles changed no production state or retained physical identity.

A subsequent exact reproduction reached worker turn 96 but crossed the
opt-in diagnostic's 60-minute whole-run parent context by 0.33 seconds while
reading one selected blob. The earlier successful execution completed in
59m12s, so this was a diagnostic-envelope edge rather than the production
five-minute per-worker deadline or an output refusal. The opt-in parent context
is now 65 minutes, and the documented command explicitly invokes `go test`
with `-timeout=70m` to retain a five-minute outer cleanup window. This changes
no production context, timeout, retry, candidate, Git-read, or output bound.
With that harness-only correction, the exact reproduction passed in
3,482.55 seconds (54m6.58s in worker convergence) and matched the retained V3
receipt byte-for-byte; no evidence value changed.

Independent review then found that receipt-only and durable-store validation
could not distinguish V3 no-resolver coverage from descriptor-present
zero-fact coverage, and that the artifact did not independently bind the
reported source-byte total. V3 receipts now carry the compact reason;
`no_resolver_descriptors` rejects any claimed source read or byte at both
boundaries. Coverage-v2 zero-fact records embed `source_blob_bytes`, and the
artifact reader requires exact agreement with the receipt as well as the
derived read-count identity. Historical V1/V2 encodings remain unchanged.
Focused corruption tests cover both defects. The two remaining V2-era opt-in
witness generators now skip under the V3 runtime, and an unknown future
fact-free abstention reason keeps the valid materialized artifact instead of
turning compaction vocabulary drift into a pair failure.

The exact corrected 96-leaf reproduction passed in 3,569.10 seconds, including
55m27.89s of worker convergence. It again settled and succeeded 96/96 pairs,
published the same one result, 4,055 abstentions, and 95 coverage records over
257,713 candidates, retained 261,764 reads/24,082,313 bytes with zero
out-of-leaf reads, and matched the retained source-free V3 receipt exactly.
Caller artifact disk measured 1,003,478 bytes (6,175 bytes above the earlier
997,303-byte corrected-oracle run), the bounded cost of embedding the source
byte scalar and receipt reason; this is an observation, not a ceiling or SLO.

Steady-state cost remains deliberately source-bound. Each descriptor-present
pair performs the same selected-member read, bounded Git blob reads, hashes,
Go parses, direct scans, staging writes, and repository work lock. V3 adds
constant counters; a zero-fact completion performs one truncate, seek, and
coverage-record write. It holds no per-candidate memory and introduces no new
Git/corpus/shard read, child, lock, cache, retry, query, sync/startup work,
publication transition, bound, or lifecycle ceiling. Result/unresolved pairs
add only a constant final check. The successful measurements are diagnostic
observations, not ceilings or an SLO.

This closes the scoped caller refusal, not T40.13 or Epic 40. It does not
reconstruct Take 20, establish a scale/SLO pass, authorize a ceremony or
release, or unblock Epic 41. Independent review is required before a separate
fresh-ceremony freeze decision.

### Corrected-V3 custody fence and neutral-21 freeze authorization

Independent re-review authorizes one fresh freeze after correcting the custody
record. Pre-correction V3 commit `ab5f28f` entered pushed `main` through
`c911586`; `origin/main` exposed it from 2026-08-15 18:12:14 -0700 until
corrected `45f473a` replaced it at 20:12:06 -0700. Project custody records no
deployment, service startup, ceremony, or durable execution against that
interval. A custody whose provenance cannot exclude `ab5f28f` must stop for a
separately reviewed purge/rebuild decision: corrected V3 validation is not
compatible with V3 caller state written by that intermediate commit. Merely
bumping `callerLeafWriterMigrationVersion` is not a purge; the current
migration rejects a nonempty version mismatch and stops startup.

The large-Mac ceremony uses a creation-exclusive isolated custody directory,
so it satisfies this fence. Once this ledger change is fast-forwarded to
`main`, `t40r1-neutral-21` freeze is authorized from that exact resulting
`main` commit. The freeze evidence must retain the corrected 96/96 receipt
`descriptor-present-96leaf-product-v2.json`
(`sha256:43e3a82e1c3897bd62f14150a1c0d9352d396030cc4f0bd1a1959f1f282b029b`).
This authorization stops after `freeze`: review the emitted plan digest and
obtain separate explicit execution approval. Invalidate and re-review the
freeze if either a pilot/design-partner requirement changes the 96-leaf scale
need or an explicit charter decision changes the 100,000 aggregate
caller-abstention ceiling.

```sh
cd ~/phebs

./spike/t4013/run-large-mac-ceremony.sh preflight

# Authorized only after this custody ledger is fast-forwarded to main.
CEREMONY_ID=t40r1-neutral-21
./spike/t4013/run-large-mac-ceremony.sh freeze "$CEREMONY_ID"

# Stop here. Review and record the printed sha256 plan digest.
APPROVED_PLAN_DIGEST=sha256:<reviewed-plan-digest>
./spike/t4013/run-large-mac-ceremony.sh execute \
  "$CEREMONY_ID" \
  "$APPROVED_PLAN_DIGEST" \
  execute-reviewed-neutral-t4013-plan

./spike/t4013/run-large-mac-ceremony.sh verify "$CEREMONY_ID"
```

Execution uses the existing plan-bound preparer and destructive teardown. A
new cleanup command removes the exact custody plus the credential-bearing
prepared manifest even when execution stops before its ordinary teardown.
The evidence directory retains only the plan, source-free observation,
validated receipt, a small transfer manifest, checksum inventory, public
signer material, and signature. Each ceremony ID receives a distinct private
signing key under the ceremony root; it is never packaged and must be preserved
separately. Transfer only the generated `*-source-free.tgz` and its checksum—do
not zip or transfer the ceremony root, `signing/`, `private/`, or `custody/`.
The repository ignore rules are defense in depth, not permission to place
ceremony material in the checkout. The script creates
`<ceremony-id>-source-free.tgz` plus its SHA-256 sidecar for transfer back to a
clean checkout of the exact sealed source commit, where it can be checked with:

```sh
./spike/t4013/run-large-mac-ceremony.sh verify-bundle \
  /absolute/path/to/<ceremony-id>-source-free.tgz
```

This driver deliberately accepts no operator-corpus path. It exercises the
frozen neutral two-million-owner profiles only. The separate 1.6-million-file,
5,000-service private-target census/replay remains a later combined-scale
ceremony after the service-cap program; using this driver does not authorize
or imply that target replay.

```sh
go test ./spike/t4013/... -count=1
make docs-check
make verify-glossary
```
