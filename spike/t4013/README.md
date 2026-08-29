# T40.13 neutral two-million-owner convergence gate

T40.13 runs the frozen T40.1 structural and semantic repositories through the
ordinary phebs server, scheduler, publication, recovery, archive, lifecycle,
and authorized product paths. The retained plan and receipt are source-free;
the authored bare repositories, data directories, logs, query text, returned
source, credentials, and process transcripts remain outside the repository and
are destroyed after the run.

## Supported V25 admission path

`run-large-mac-ceremony.sh` is the only supported V25 ceremony entrypoint. It
requires the dedicated-host stability attestation before preflight, freeze,
execute, seal, verify, or returned-package verification and retains the
reviewed lock, signer, cleanup, and packaging sequence. The direct
`cmd/t4013-*` examples below document low-level library/harness interfaces and
historical construction; they are not V25 operational procedures and must not
be used to admit, freeze, execute, resume, or seal a ceremony. Use the driver
workflow under “Corrected-V3 custody fence and neutral-21 freeze
authorization,” with a fresh separately authorized identifier.

## Prospective freeze

The execution commit is frozen before either giant repository is authored.
`t4013-freeze` verifies the exact retained T40.1 envelope, both manifests, and
the same-SHA blob-reader comparison before emitting a new plan. It refuses an
existing output.

```sh
# Low-level harness reference only; not a supported V25 ceremony command.
go run ./spike/t4013/cmd/t4013-freeze \
  -root "$PWD" \
  -source-commit <exact-40-hex-execution-commit> \
  -data-parent /absolute/dedicated/t4013 \
  -bind-host-toolchain \
  -output /absolute/dedicated/t4013/plan.json
```

The bound-host freeze refuses before emitting a plan when that exact
filesystem cannot satisfy the current V25 pressure projection. Preparation
rechecks the same gate after authoring against the measured custody bytes.

The current V25 plan freezes 24 GiB memory and 120 GiB available-disk
prerequisites, a twelve-hour total safety ceiling, 20 GiB peak RSS, 96 GiB
maximum allocated data, a 72 GiB pre-pressure custody ceiling, the production
five-attempt ceiling, and a 15-minute per-server health-readiness deadline.
These are stop/decision fences for one neutral mechanics run, not a target SLO
or supported-scale claim. They may not be raised after any measurement begins.

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
3. `warm_noop` — request the already-current generation again. V1–V28 require
   no Git child; V29 requires the full phase Git count to equal its paired
   healthy-startup count. V30 incrementally tracks phase-local candidate and job
   lifecycle reports within the existing revalidation deadline. A `done` reuse
   decision of `warm_noop`, `cold_reuse`, or `marker_recovery` is necessary but
   not sufficient: released, failed, or requeued work rejects; claimed, started,
   deferred, and yielded work stays unresolved until its later
   `event=done,outcome=success`; exact authority is re-inspected on every
   attempt; and one existing five-second convergence interval must add no
   candidate/job report after all jobs resolve. The phase meter finishes once
   there. Its single finished `PhaseMetrics` snapshot supplies paired startup
   process counters without a second sampler read, while log/stage/wall refresh
   at that settled boundary. The atomic warm→delta handoff transfers the finished
   process window with the exact log EOF and performs a bounded post-reset tail
   scan: any candidate/job report or partial tail refuses the boundary, complete
   unrelated lines advance the warm EOF, and later exact claimed/started reports
   remain delta before the same equality, so post-boundary Git still fails. Every
   version requires no index child, publication
   write/transaction, or authority movement and requires positive reuse.
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
   rotation without filling the filesystem or weakening the hard watermark,
   prove coherent sorted post-ballast owner attempts, observe final exact-normal
   capacity after the latest owner, and re-confirm protected authority without
   claiming capacity stayed normal throughout the cycle or waiting for hourly
   idle eligibility.
9. `archive_restore` — create the production backup, restore into a separate
   empty dedicated installation, verify precious authority and explicit
   derived inclusion/omission, then converge.
10. `collection` — in V30, restart and prove all 14 owners fresh and
    `state=ok`; require the 13 non-`durable-jobs` owners exact and drained,
    permit truthful `durable-jobs` `lower_bound` plus backlog while live writers
    exist, require exact capacity after the latest owner, preserve unchanged
    current stable authority, and retain bounded per-owner counters. Eligible
    deletion and individual rollback/lease/marker/store-pin protection remain
    mandatory regression-gate semantics, not claims inferred from this live
    phase; V1–V29 retain their frozen collection predicates.
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
# Low-level harness reference only; not a supported V25 ceremony command.
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
# Low-level harness reference only; not a supported V25 ceremony command.
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
# Low-level harness reference only; not a supported V25 ceremony command.
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
The warm no-op must have no post-startup sampled Git lifetime, index child, or
publication/authority mutation.

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
and 120-GiB available-disk prerequisites, closed module graph, and direct V25
operation-command build before it authors custody. The focused process-launching
harness and rehearsal suites remain branch gates on the exact reviewed commit;
the ceremony preflight does not rerun them. A 64-GiB Mac with more than 500 GiB
available exceeds those host prerequisites; the frozen production and review
ceilings remain unchanged. Preflight also requires free loopback ports,
SurrealDB on `PATH`, and the Go 1.26 toolchain line; it hydrates the
checksum-verified module cache before remaining non-custody Go commands and
measured toolchain builds switch to `GOPROXY=off`. The committed `go.sum`
includes the complete focused-harness and Buf-command graph, and preflight
repeats the clean-checkout guard after hydration and prebuild checks so
dependency resolution can never silently change the frozen source.

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

export PHEBS_T4013_HOST_STABILITY_ATTESTATION=dedicated-single-operator-host-with-tool-mutation-disabled
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
REVIEWED_SIGNER_FINGERPRINT='SHA256:<recorded-through-a-separate-channel>'
./spike/t4013/run-large-mac-ceremony.sh verify-bundle \
  /absolute/path/to/<ceremony-id>-source-free.tgz \
  --reviewed-signer-fingerprint "$REVIEWED_SIGNER_FINGERPRINT"
```

The package sidecar is a transfer-integrity convenience, not authentication.
The alternate `--reviewed-package-digest sha256:<64-lowercase-hex-digits>` mode
is valid only when that exact digest was itself reviewed out of band.

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

### Neutral-21 scheduler wedge and scoped recovery

`t40r1-neutral-21` stopped honestly at the exact four-hour semantic cold
deadline. Its verified signed source-free package has SHA-256
`93b54e57c071e2341a3f50f6a62a859b9af9d41c76010ae9f55dfd28eb4806be`.
Structural cold converged. Semantic extraction materialized all 272 scheduled
partitions, executed six gRPC handlers in 942 ms total runtime, and then held
266 pending, one running, five succeeded, and zero failed for roughly 3.6
hours. The custody-bound server log recorded concurrent SurrealDB transaction
conflicts in schedule expansion and the sixth completion; that handler settled
as `completion_failed`. Because handler completion cancels its heartbeat before
the settlement write, the remaining lease was stale rather than alive.

The schedule implementation had two compounding defects. Expansion and lease
transition transactions did not share claim's bounded conflict retry, and one
sequential scheduler loop performed expansion before stale reaping with the
server-lifetime context. A blocked expansion call could therefore prevent the
reaper from returning the repository's single token, starving every pending
chunk. Generation expansion, heartbeat, completion, failure, retry, release,
and stale reaping now reuse the existing 64-attempt explicit-conflict bound.
Completion reconciles an ambiguous response through exact chunk and current
schedule reads without incrementing counters again. Expansion and reaping run
independently, and scheduler store calls have a five-second boundary. A
deterministic regression holds expansion blocked after a failed completion and
requires the reaper to release, reclaim, and settle the exact chunk.

The scoped physical diagnostic uses a real supervised SurrealDB 3.2.0
surrealkv child, two workers, one repository token, and the exact 272 one-item
schedule. It settled in 1,887 ms with 272 handler executions, 272 successes,
zero pending/running/failed rows, no surfaced store error, and 21 internally
recovered expansion conflicts. Its source-free receipt is
`generation-schedule-recovery.json`
(`sha256:def6cd63f0bc7a7b97af753922812da90110787db4da0c5deccade66adab5f7c`).

The fresh production physical replay at exact corrected source
`abbd218712015cc9802b5d9b1c1e8168641f5732` then completed all 272 extraction
schedules and all 272 partition timings, surfaced zero `completion_failed`
events, and internally recovered one schedule-store conflict. The exact
semantic test was intentionally interrupted after that requested boundary
while downstream Caller publication inspection continued; it is not a
full-fit or ceremony pass. Interrupted source and derived custody were
destroyed. The retained source-free observation is
`generation-schedule-production-replay.json`
(`sha256:b562f032d9737b246e456e1d0682e002ff2245cdd5f6e4c230a72971912885d0`).

Fresh V14 plan emission also stamps the actual UTC freeze date. Historical
plan constructors and retained bytes preserve their original `2026-08-08`
value; the signed neutral-21 package is not mutated or relabeled.

```sh
T40R1_GENERATION_SCHEDULE_DIAGNOSTIC=1 \
T40R1_GENERATION_SCHEDULE_RESULTS_PATH="$PWD/spike/t4013/generation-schedule-recovery.json" \
go test ./internal/generationscheduler \
  -run '^TestT40R1GenerationScheduleRecoveryDiagnostic$' -count=1 -v
```

This closes the reproduced schedule wedge and its scoped production extraction
replay only. The structural run's transient 409 remains separately
unclassified, the downstream full-fit replay was not completed, and the
ceremony's later checks and ten not-run phases remain unestablished.
Independent review is required before another ceremony.

### Neutral-23 caller-generation observer stop

`t40r1-neutral-23` is a verified signed `unclassified`
`convergence_deadline_expired` stop at exact source
`75647ac37d495514a74418563180711419a29239`. Its plan digest is
`sha256:f788c6ac989494fed24d324be48fb097bd5ee82d5560550a801ff79b90069999`;
the source-free observation, receipt, and package SHA-256 values are
`1a36664dff85ade81cd94b596cb4d26d0334f5d8f3135a46447d843f53b2e952`,
`830691df1cd0bfe0a39aa098cc43a952a091a9ea5292e65e7c6290d0e3c4a3b0`,
and `e498c4cc9bac8bccc25c76b1dc04f7ab79e7d5cbbdbd642217eb8a5fc90abebc`.
Structural cold converged in 3,045,291 ms. Semantic extraction completed and
scheduler-settled all 272 partitions, with zero failed or terminal-refused
executions, before the inspector retained an unchanged caller-generation 404
for the remainder of the four-hour deadline.

The stop is a ceremony-observer defect. The probe added at `2fb09a0` queried
the hard-coded `t401-neutral` `/neutral.Service/Ping` endpoint. The frozen Go
fixture calls that operation, but the frozen protobuf templates contain only
messages and never declare the operation. Exact Caller Map correctly returns
404 for a missing declaration after a current publication is open; missing,
stale, and failed generations return typed gap pages before declaration lookup.
The observer therefore converted a current caller generation into permanent
pending status.

The prospective correction preserves Caller Map's 404 contract and changes
the inspector to `/api/caller-generation-progress?repository=...`. That
authorization-first projection uses the same exact publication reader,
eight-read semaphore, current/unavailable fence, repository revision,
visibility, and analysis-scope confirmation, but it does not select or read an
endpoint declaration. Its 32-KiB response retains only source-free generation
identity/state, aggregate counts, bounded partition progress/refusals, and
digest/count scope authority without selected paths. Focused tests prove that
exact Caller Map remains 404 with declarations removed while progress reports
the current complete generation and performs no assertion/resolution read;
maximum-shape coverage proves the admitted response fits its bound.

Neutral-23 remains immutable stopped evidence; this correction does not
retroactively pass it or authorize another freeze/execution, release, scale or
SLO claim, topology change, T40.13/Epic 40 closure, or Epic 41 progression.

### Neutral-24 relationship Kafka-envelope stop

`t40r1-neutral-24` is a verified signed `unclassified`
`convergence_deadline_expired` stop at exact source
`38f32061702d3e1712dec75afd1766f2d4bc6d0b`. Its plan, observation, receipt,
and source-free package SHA-256 values are
`5f54755e0f334322e7406efc50805c1a44426d0cf934f34c4d136b5e29ac9dfa`,
`f2571b4befbbad6fc3949701441d532f8cc03de7395290cb7e74d9bbaa515a69`,
`ed0ccb59eca51e05c3108e7e9bd06979760eee47a7dd84edbed4a7d9a03a1c0a`,
and `adc4755266b9d4294cfe8594733db6e2f5a3c88c1846a3b92c44dd1c1aa3df1a`.
Structural cold converged. Semantic extraction executed and scheduler-settled
272/272 partitions with no failure or terminal refusal; caller generation was
current. The last inspection was a stable missing relationship publication,
which the old observer classified as a generic control error until the
four-hour deadline.

The source-free package cannot distinguish the builder's untyped limit return;
exact production replay pins two independent deterministic fences. The
65,536 literal postings share `t401-events`, and the 65,536 dynamic postings
share the unresolved bucket, so each family needs one 65,536-posting member
instead of the old 50,000 maximum. Their production JSON plus conservative
overhead charge is exactly 147,324,928 bytes (140.5 MiB), above the old
128-MiB relationship Kafka resident limit. The correction admits exactly
65,536 postings per current member, retains the historical 50,000 policy as an
exact readable policy, and raises only the Kafka operational resident fence to
160 MiB. Exact parser/model tests pin the 1,118-byte literal and 1,130-byte
dynamic charges. Deterministic Kafka bounds now settle the one-chunk schedule
terminally; the exact resident case carries a closed source-free refusal. New
partitioned schedule bindings also bind the exact builder-policy digest. A
duplicate reconcile retains a same-policy closed failure without another
attempt, while a later policy or bound change creates a distinct recovery
target; historical v2 bindings remain readable.

Run the scoped component and end-to-end diagnostics with:

```sh
T40R1_RELATIONSHIP_KAFKA_DIAGNOSTIC=1 \
go test ./internal/kafkatopicposting ./internal/relationshippublication \
  -run 'Test(FrozenSemanticPostingBuild|ExactKafkaToRelationship)Diagnostic' \
  -count=1 -v -timeout=20m
```

The scoped run built 131,072 Kafka postings in two exact members with
80,347,488 encoded bytes, then published 131,072 relationship projections with
60,358,802 encoded repository bytes and complete relationship authority. The
component and end-to-end process maximum RSS values were 566,460,416 and
740,720,640 bytes; the end-to-end run completed in 9.13 seconds. These are
source-free diagnostic observations, not an SLO or supported-scale claim.

When a relationship root is absent, the ceremony now reads only the current
local `service-relationship` schedule. Missing or active remains pending; a
settled failure is terminal and typed, while a successful settled schedule
with no root is terminal rather than indefinitely pending. The projection
omits repository, stage, timestamp, worker, lease, and raw-error fields. A
present relationship root adds no schedule read. Neutral-24 remains immutable
stopped evidence. A fresh ceremony still requires focused and race gates,
independent review, explicit integration/freeze approval, and a new identifier.

### Neutral-25 extraction-job projection stop

`t40r1-neutral-25` is a verified signed `unclassified`
`extraction_job_terminal` stop at exact source
`00bdec2f9af381f90c66f1af7214826d8576ef94`. Its plan, observation, receipt,
and source-free package SHA-256 values are
`7bb8be9411be8034c796f6c3653db56f44047af3cf1014aff66bfc6890c294b7`,
`568d0011dfd4741b324851224f3b79350d5b947333ff8c9af8b39a0ce22ccc60`,
`9fa9b50b484c380430dfa69ba431b739bb3ba0e79b2694c6a9409cfe2cb6b63c`,
and `f42aaf34e569bacd2146bf27496ac0edbd89e22508920e72d6c8a77403ae1692`.
Both cold profiles converged and structural warm-noop passed. Structural delta
B then stopped after 1,687,039 ms even though its final extraction projection
was fully current: 1,956 total/materialized/succeeded partitions, zero
pending/running/failed, and nine of nine current domains. The latest extraction
job projection was failed at attempt two with no typed refusal.

The source-free tuple itself pins the observer defect. Replaying those exact
scalars through `extractionConvergenceProbe` produces the signed terminal probe
digest
`sha256:d4703c2d327d13d0116fb2795774cb3caf21c8d98ac8c91ab163777bf7c05600`.
The repository-status job is only a repository-keyed orchestration trigger; it
carries no candidate, source, observation, plan, schedule, or extraction-
generation identity. It therefore cannot disprove a later generation-bound
current schedule. The destroyed raw job error is neither required nor guessed.

The prospective correction retains exact typed limit refusals and settled
failed schedules as terminal. An ordinary failed/canceled job remains pending
only beside an active schedule, whose scheduler actors can still settle and
publish without the job, and cannot poison a fully current schedule. In every
other state — unavailable, settled successful awaiting promotion, superseded,
or incomplete settled counters — no actor remains, so the job row is
conclusive and stops with a typed terminal instead of pending to the ceremony
deadline; the job row's typed refusal follows the same conclusiveness rule. A
V15 job-plane stop confirms on a second identical five-second probe so a poll
racing the enqueuer or the final promotion write converges instead of sealing
a spurious terminal. Current progress still has to pass the existing exact
extraction scan (memoized on the unchanged probe digest across polls),
caller-generation check, and relationship source/extraction authority parity
before the inspector can complete. The job state and attempts remain in the
source-free receipt.

Fresh freezes advance the plan, observation, and receipt contracts to V15 for
this precedence. V14 keeps its historical job-first predicate, validation, and
exact serialized bytes; rebuilding the retained neutral-25 receipt remains
byte-identical. V15 changes no safety ceiling, admission limit, production
bound, topology, or concurrency. On its failed/canceled-job plus current-
schedule edge, it newly reaches only the existing bounded downstream checks
that V14 skipped: exact extraction-authority validation globs at most 64
retained generation controls; each matching generation status opens up to 64
domain plans, checks at most 490 expected partition-result records per domain,
and reads domain-root/current authority, stopping at the first complete
generation (about 2,007,040 result checks at the full envelope). One caller-
generation progress request and the applicable relationship-root and service-
catalog authority checks follow. The scan can repeat on the five-second poll
while downstream authority remains pending; it reads no source/content,
candidate members, corpus, shards, or Git objects.

Neutral-25 remains immutable stopped evidence. This correction does not
retroactively pass it or authorize integration, freeze/execution, release,
scale/SLO, topology, T40.13/Epic 40 closure, or Epic 41 progression. A fresh
ceremony requires focused/race/docs gates, independent review, explicit
integration/freeze approval, and a new identifier.

### Neutral-27 relationship pair stop

`t40r1-neutral-27` is a verified signed V15 `unclassified`
`convergence_deadline_expired` stop at source
`566a0ecd5232b4b041f5d824e9150b0c80665da2`. Its plan, observation, receipt,
and source-free package SHA-256 values are
`4a68a03fb15c84bf56945b1ee5032003aeae6f303f333f9d57e1e4af7f0f4cb6`,
`c7ba44081a315c586f0fa63fadf73753ce23ec04de2f1d51e85929466f4ed6e7`,
`107e9315c6c5ca5b2e817bbd0d4a05f106b6f46ef769c97e9fecef778edaf570`,
and `291336d632150b1c0101da65ab2621f7c410d218a08ef53da1d065c5c2a1a758`.
Both cold profiles and structural warm-noop converged. Structural delta B
entered `relationship_publication` as pending at 3,250,113 ms, changed to the
generic `control` class 4,974 ms later, and retained the same control digest
through the 14,395,102-ms final inspection. The retained extraction projection
(1,954 succeeded, one pending, one running) is the last extraction-stage
projection; it does not show that extraction stayed active after inspection
advanced to relationship publication.

The V15 observer reads the current relationship schedule only while the root
is absent. Once a root appears, a malformed current control, incomplete root,
or root/extraction mismatch becomes the same untyped control error. The full
extraction-authority scan is cached only by the extraction probe, so a newly
published relationship root does not itself force parity revalidation. The
source-free package selects that classification/revalidation gap but cannot
identify which private root/schedule condition occurred; custody was destroyed
and the correction does not guess it.

V16 introduced the relationship observer contract that V17 retains unchanged.
Relationship probes retain a closed boundary class:
`current_control`, `authority_incomplete`, `authority_mismatch`,
`successor_absent`, or `successor_settled_without_current`. Missing or
mismatching root authority is paired with the bounded current schedule: active
stays pending, an exact failed schedule keeps its typed refusal/terminal, and
settled-success without matching current authority or malformed/absent control
stops terminally. A non-refusal terminal must repeat as the same exact pair on
the next five-second probe, and the observation/receipt contract requires
exactly two confirmations plus transition/class/outcome parity.

The extraction scan is now cached by extraction-probe and relationship-root
generation. A root transition performs one fresh exact extraction/root check;
later polls over the same pair reuse it. At the full existing envelope that
single scan can check up to 64 generation controls × 64 domain plans × 490
partition-result records plus root/current authority, stopping at the first
complete generation. A missing/mismatching root adds one bounded schedule
projection per poll; a stable valid root adds no schedule read. No production
path, endpoint, bound, timeout, authority, or release posture changes.

Neutral-27 remains stopped and does not pass retroactively. V15 and earlier
receipt bytes and semantics remain exact. Focused/race/docs gates and
independent review precede any request to integrate, freeze, or execute a fresh
identifier.

The V16 observer correction has a separate stacked production prerequisite.
Extraction now releases its Git source/repository lease after durable partition
result installation and before domain assembly or publication fencing.
Artifact reconciliation stops and releases its shared mutation fence after one
250-ms busy repository-lock probe. Relationship mutation-lock acquisition uses
25-ms probes under a five-second total deadline, allowing the scheduler to
return the same relationship chunk to delayed pending without consuming an
attempt and release the repository-wide token to a ready extraction stage.
Runtime sync cleanup likewise defers its exact owning job after a busy audit
probe, while a direct startup audit remains fail-closed. Deterministic tests
reproduce the lock-order edge, non-consuming deferral, and cross-stage token
handoff. These changes do not assert that either private lock state occurred in
neutral-27; they are prospective liveness hardening required before independent
review and a fresh post-V16 freeze request.

### Neutral-28 interruption stop and V17 durable trigger

`t40r1-neutral-28` is immutable V16 `unclassified` evidence. The verified
source-free package
`sha256:ba1a583b08494d932ee1e769161e1e4ee9343720b72d8fc30b26245f98597f5b`
binds source `26ca6d7e0375eb82be8731a4a6779a88107b8d86`, plan
`sha256:95727097f715ad639aec35ba6738f5ec82bd797dce05a2046e65f359fcf4b429`,
observation
`sha256:c6acb78fbb9036f09e64af86bfd169e52a07ea51190b818949f1024b78af6a4`,
and receipt
`sha256:c8760b5a0416973124d632b9fc2eb43f933f8f4b41fb224cc8c88d097271b89c`.
Cold, warm-noop, delta B, and return A converged. Interruption then stopped as
generic `operational_failure`; teardown destroyed derived and scratch custody.
V16 did not retain an interruption substage, so this package cannot distinguish
backup, restore, ephemeral-control discovery, first stop, or restart failure.

The historical interruption trigger was passive. After starting the restored
server it polled shallow `publishing.json` and `.stage-*` controls once per
second. It did not command a source transition, did not prove a worker lease
was active, and its relationship traversal did not reach the hashed
`relationship-publications` generation layout. A timeout therefore proved only
that this scanner did not observe its marker.

Fresh plans use V17. After live backup, restore, and exact-A verification, the
harness starts the semantic server and advances its local source from A to B.
It incrementally consumes the closed generation-lifecycle stream beginning at
that server's measured log offset. Observation planning, inventory, execution,
extraction, and relationship lifecycle reports are accepted; only extraction
starts are candidates. The generation artifact must bind the exact B revision,
and a local source-free store projection must still match the exact chunk
identity, repository, stage, generation, attempt, and `running` status. Only
then does the harness stop the server, return B to A while offline, restart,
and require exact A authority. The graceful stop drain can settle or release
the selected lease before process exit, so the receipt does not assert an
interrupted lease: after restart the harness re-projects the trigger chunk and
requires a recovered, non-running fate within a bounded window (recorded as
the trigger's recovered state), requires the `interruption-restart` startup
observation once the substage reaches restart convergence, and requires that
no partial derived publication state — including the hashed
`relationship-publications` layout the old scanner missed — survived the
restart. A B-bound schedule that settles before any lease is selectable stops
the wait immediately as a typed unsatisfiable result, and a transient store or
authority read retries on the next poll instead of aborting the ceremony.

V17 observations and receipts retain the last closed interruption substage and
the selected trigger's stage, generation digest, chunk digest, explicit attempt
(including zero), phase-relative wall time, and recovered post-restart state.
They do not retain paths,
source content, worker identity, lease tokens, durable timestamps, raw errors,
logs, credentials, or responses. V17 deliberately reuses V16 relationship
classification; the change is limited to interruption control and evidence.
The diagnostic is pointer-backed and omitted from V1–V16, whose validation and
serialized bytes remain exact.

The V17 harness removes the old shallow marker scan. It adds one semantic A→B
update and one offline B→A update, each through one bounded `git update-ref`
child, and runs the existing bounded pipeline only until the first authoritative
B extraction lease, not through complete B publication. Log reads are
incremental in 64-KiB chunks, with a 1-MiB partial
line and 400,000-report per-poll ceiling. Lifecycle validation is
version-gated: V16 execution keeps its frozen extraction-only fatal contract,
while V17 checks structure fatally and validates-then-discards vocabulary
drift, because the store lease projection — not the log — is the selection
authority. Non-extraction reports are validated
and discarded. At 250-ms cadence the harness examines only active extraction
reports, revalidates each immutable generation-to-revision binding, and reads
one bounded lease projection per candidate until selection. At most once per
five seconds it also reads current schedule progress even when stale lifecycle
starts remain active, so an exact B-bound settled schedule seals the typed
unsatisfiable stop. Post-restart recovery opens one local reader, polls the
trigger lease at one-second cadence for at most five minutes with a 30-second
per-call bound, closes the reader, and performs one bounded single-repository
partial-control scan across the derived and hashed relationship layouts. No
production request, sync/restart owner, handler, retry, authority, API, schema, bound,
lock, cache, memory/disk ceiling, or release posture changes.

Neutral-28 does not pass retroactively. V17 requires focused/race/docs gates,
independent review, explicit integration, a new signed freeze, and a new
ceremony identifier before execution.

### Neutral-29 no-trigger stop and V18 store-authoritative discovery

`t40r1-neutral-29` is immutable V17 `unclassified` evidence. Package
`sha256:4ba484f9b22902edda41179d0b790cec018c2ecc12fa7baaed66049c8315fcd8`
binds source `5707856b2ce72404fff7eca34a384e69b9e1169b`, plan
`sha256:c18621ef92664c042103592dfbfd12918a7eb4e974fa0f6a9041168b60ba22b7`,
observation
`sha256:d44420f7e179c54d76bb125a1396b90aaf3ac3ce60a59b36279d8d544ce0c825`,
and receipt
`sha256:3049b7a452f1932bfcf21d734cb7afb8c4a61a4a9f3ab202aa26eb9af8cfdd51`.
It stopped at `interruption/active_lease_wait` with no selected trigger. The
record does not establish whether B lacked an extraction schedule, remained
upstream, or crossed a lifecycle start/settle boundary between polls.

V18 makes the exact current schedule authoritative. Its two local-store readers,
lifecycle cursor, and exact inspector are opened before A→B. At 250-ms cadence it
selects at most one running extraction chunk, rechecks the current schedule,
identity, generation, and local runtime, and accepts it only when the immutable
generation binds revision B. Lifecycle lines continue to receive bounded
structural validation but cannot hide or create the selected lease. Every five
seconds the existing V16 exact inspector also records a fixed-size last
stage/class/probe digest/wall/change projection. A terminal projection, a
no-trigger deadline, and a completed or settled-without-selectable-lease
pipeline therefore produce distinct sealable stopped receipts. The exact
inspector runs single-flight on the second connection, so its 30-second bound
cannot pause lease sampling; exit cancels and joins it before closing readers.
Stale-worker uses the same store-authoritative selector.

The selector performs bounded current-pointer, one-row, schedule, identity,
and runtime-fence reads and exposes no lease token or private store detail. The
inspector keeps its 30-second call bound and existing authority/inventory
limits, including at most 64 generation controls × 64 domains × 490 partition
results at its worst extraction envelope. Log reads retain the V17 250-ms,
64-KiB, 1-MiB-line, and 400,000-report bounds. No production behavior, schema,
API, retry, worker, authority, lock, cache, limit, or release claim changes;
V1–V17 bytes and validation remain exact. Neutral-29 does not pass
retroactively, and this branch authorizes no freeze or execution.

The opt-in real-binary rehearsal uses separate fresh semantic repositories for
interruption and stale-worker, avoiding a vacuous second transition through an
already-materialized B generation. It retains structural delta/return,
live-backup/offline-restore, lifecycle, and authorized-query coverage. The
V25 rehearsal now observes and binds the complete exact host toolchain before
creating its closed execution controls, so SurrealDB and child-tool identities
cannot fall back to ambient `PATH` lookup. The
first corrected run exposed a production stale-authority retry failure during
the interruption return-to-A restart; that stacked production blocker must be
closed before the rehearsal, integration, or freeze gate can pass. The small
rehearsal establishes no pressure or scale claim.

### V19 prior-gate recovery closure

The stacked correction revisited the earlier semantic, structural, restore, and
stale-worker gates before another giant run. Extraction fence contention now
defers without consuming an attempt; stale active schedules are superseded even
beside exact prior domain authority; whole-search return-to-A reactivates the
retained validated generation; stale caller pointers repair normally; and
restore clears restartable schedule/token/domain-root state before ordinary
reconciliation. Offline restore verification proves exact archived caller and
relationship bytes, while online parity compares reminted product semantics.
Current extraction progress accepts bounded retry materialization above the
logical total without weakening success, current-domain, or attempt limits.

The stale-worker rehearsal uses an exact store diagnostic fence after selecting
the running chunk, making stale completion deterministic. A stable relationship
semantic digest excludes only the monotonic service-summary transition fence
for A→B→A recovery comparison. Partial derived controls receive a bounded
asynchronous-clear window rather than an immediate race-prone assertion. Fresh
plan/observation/receipt schemas advance to V19; earlier schemas and retained
bytes remain valid. The combined real production-binary rehearsal passed all
three profiles in one process. It remains small-corpus readiness evidence and
does not establish pressure, scale, SLO, accuracy/completeness, release,
migration, decommissioning, or a retroactive ceremony pass.

### Neutral-30 stopped recovery and resolver v2

Neutral-30 passed cold, warm-noop, delta-B, and return-A. Its interruption
restart also converged through the exact extraction boundary, then the strict
V19 authority oracle stopped because caller generation changed. The verified
source-free package is
`sha256:145eb7a7125a86b1af76bc1785c9997e411696fda0cf46250b9a9cae5dc9fa8a`;
it binds source `2b32ceea709bf47eacf0a5ce2f62f3bb83cf9711`, plan
`sha256:5ed41ebb4c23f1c2944f7dbfd6691f7f43297d558e19c41b998814c6084cbeaf`,
and observation
`sha256:ed05de9d98d8290f7d0ca2abff18622c5fe09b6764bdd4008a01657bdcf2f550`.

The production defect was extraction `RunID` provenance entering all three
resolver authority hashes: declaration set, generation, and manifest. The
follow-through audit found the same lineage field independently entering the
partitioned downstream-upstream digest bound by caller. A fresh run over
byte-equivalent A content could therefore rekey caller and relationship
authority through either path. Resolver v2 keeps `RunID` inside the exact
manifest integrity hash but excludes it from a separate semantic authority
projection consumed by caller; downstream authority v2 retains the serialized
provenance while excluding it from its digest. V1 bytes retain historical
validation, and a supported fail-closed writer migration retires v1 current
resolver/caller pointers before the existing startup fan-out rebuilds v2. The
strict V19 recovery oracle is not weakened. Neutral-30 remains immutable
stopped evidence and does not authorize another freeze or execution.

### Neutral-31 caller publication stop and V20 fence

Neutral-31 is immutable V19 stopped evidence. Its verified source-free package
`sha256:7e6301197b505dfd07718e97caf2565ead7037d70fb65a5d3098f5b91ab72543`
binds source `5434bb382182251f356040eee15ac8766e2292d2`, plan
`sha256:dc61a3cdab6eb44ffc43f9cac9e69ff28d2ab2445d023d127ba19d6bc818ad82`,
observation
`sha256:6f2312000780f7f6b6a7b94892c2b3a7af5f6c0a7f4cd7bc07254d6dcd3c9c6d`,
and results
`sha256:9f348b0d00cd97148fe6d88e0cb8df1a475f8f513a79f3af555e05e1f976dcec`.
Structural cold settled extraction and then retained one unchanged caller
projection through the four-hour deadline.

The production defect was a private V1-only upstream-authority decoder in the
caller publication store. Neutral-30 had correctly advanced downstream
authority to V2, and every caller pair plus admission could settle, but the
final publication transaction rejected that canonical V2 envelope. The store
now delegates to the shared dependency-low V1/V2 validator, including exact
provenance, semantic digest, usability, repository, byte-canonicality, and
64-KiB checks. A real Surreal test commits and reopens a non-empty V2 caller
publication. V1 validation and retained evidence remain unchanged.

Fresh plan, observation, and receipt schemas advance to V20. A missing or stale
caller pointer accompanied by complete, all-success, zero-refusal pair
accounting becomes a terminal candidate only in V20. The convergence wait
requires the same source-free probe twice, five seconds apart, before sealing
`caller_generation_terminal`; an ordinary publish/settle race changes the
probe and continues. The normal request cadence and safety envelope are
unchanged. The publication validation performs bounded canonical work over at
most 64 small domain headers and adds no query, lock, child, source/content
read, startup scan, retry, or production bound. Neutral-31 cannot pass
retroactively, and the correction authorizes no integration, freeze,
execution, release, SLO, or scale claim.

### Pre-ceremony readiness closure and V21

A multi-agent readiness review of the integrated tree, shaped by the c21 and
neutral-25…31 failure classes, adversarially confirmed six blockers before any
fresh freeze. Five lived in this harness: the relationship semantic digest
hashed upstream `RunID`/`ProvenanceDigest` and would have re-killed the
interruption exact-authority gate for byte-equivalent content; the
stale_worker phase never armed the exclusive mutation lock and diagnostic
schedule fence its `stale_fenced` oracle requires; the V20 caller terminal
could seal while a live publisher sat in its ≥60-second requeue backoff (the
admission commits before the publication transaction, so the probe freezes);
a caller-leaf job that died with partial pair progress was unclassifiable and
pended to the wall; and a deadline landing inside a terminal-confirmation
window produced a stopped observation the validator rejected after custody was
already destroyed. The sixth was production: the generic job runner killed a
healthy handler on its first transient heartbeat error.

V21 closes the caller evidence gap with a repository-keyed caller-leaf job
projection, written by every creation path (generic queue and the three
domain transactions) and read beside the caller progress page: an active job
holds every caller terminal, a settled-failed job with incomplete pairs
seals `caller_generation_terminal` in seconds, and the recorded wait carries
the bounded caller progress/job projection under detail-V17 coherence in
both wait validators. The V21 harness digest (v2 label) clears upstream run
provenance and the six component transition digests while frozen V19/V20
contracts keep their derivation; stale_worker arms the rehearsal's verified
fence order with bounded re-selection against the ~1-second chunk settle
race; the terminal-coherence fences exempt unconfirmed-terminal external
stops on V21 evidence only, at the three confirmation-window stages; and
`stopAfterFailure` validates the stopped observation before destroying
custody — an unsealable stop fails closed with custody retained, the
executed marker refuses re-execution against it, and the ceremony wrapper
preserves it for the reviewed purge. A recorded follow-up stays open: death
upstream of caller-job creation still pends the caller wait. V20 and earlier
receipts remain byte- and semantics-exact; nothing here authorizes
integration, a freeze, or execution.

### V21 freeze-readiness correction

The final V21 audit made the live caller classifier and sealed evidence use one
complete projection. `caller-generation-progress-v2` includes the exact
repository-keyed live caller job beside digest validity, aggregate pair
counters, and at most 32 typed refusal summaries. The harness consumes that
single response instead of issuing a second installation-wide repo-status
request on every caller tick. Terminal and bound-refusal receipt predicates
are the same mutually exclusive predicates used at runtime: current
all-success authority cannot be forged terminal, missing/stale partial
authority without a dead exact caller job remains pending, active work holds
non-refusal terminals, and the typed admission refusal cannot be relabeled.

Generic and domain enqueue transactions repair the repo projection when they
coalesce a pre-cutover pending caller job. The custody execution marker is
created atomically only after read-only preflight succeeds, so a prerequisite
failure remains retryable without permitting a second state-mutating run.
Historical V1–V20 receipt semantics remain exact. Integration, freeze, and
execution remain separate decisions.

### Resolver-plane projection closes the upstream caller-death gap

The V21 caller evidence could hold or seal on the caller-leaf job, but a
pipeline killed upstream of caller-job creation — a resolver-catalog job that
died before its fan-out minted the successor — left the caller projection on
a settled predecessor and pended the wait to the four-hour deadline. The repo
row now carries a creation-linked resolver-catalog job projection written by
every resolver-job creation path (generic queue plus all seven domain fan-out
transactions through one shared fragment; coalescing repairs pre-cutover
rows). Caller-generation progress v3 returns it beside the caller projection
in the same bounded authorization-first request, and V21 classification treats
the resolver job as the pipeline's immediate upstream: an active resolver job
holds every caller terminal, a settled dead resolver job beside a missing
pointer and incomplete pairs seals `caller_generation_terminal` through the
existing second-probe confirmation, an active sibling job always outranks a
dead one, and both detail-V17 wait validators refute a claimed caller
terminal whose recorded resolver job is still active. Death upstream of the
resolver job itself remains visible through the extraction plane's existing
job projection and is the recorded residual boundary. V20 and earlier
receipts remain byte- and semantics-exact; nothing here authorizes
integration, a freeze, or execution.

### Neutral-32 recovery-retention race and V22

Neutral-32 was the first ceremony to reach `recovery_verification`. Its
source-free evidence showed a harness false negative: restart correctly reaped
the selected B-bound extraction chunk, but the proof waited until restart-A
had fully converged. Two extraction incarnations and the enabled pressure
lifecycle sweep then legitimately collected the retired B schedule under the
retained-two policy. The late exact-row lookup saw only `not found` and stopped
after five minutes, even though the lease had recovered before collection.
The immutable neutral-32 source-free package is
`sha256:fb21fc83643134c45bf8efce78def76e432b51549a77793c6e60c41855c562d`;
it remains stopped and establishes no gate pass.

Fresh plan, observation, and receipt schemas advance to V22. The trigger now
records the exact schedule digest read from its authoritative running chunk.
Immediately after restart HTTP readiness—before any A-convergence work—the
harness re-projects that row. A present chunk must be non-running. An absent
chunk seals `collected` only after two consecutive one-second-separated
current-pointer/exact-digest/current-pointer fences prove the selected schedule
non-current beside a distinct current successor, and absent or closed
(settled/superseded). Missing-current,
current/active authority, a moving pointer, identity drift, read failure, or a
running chunk cannot pass. Restart-A convergence and
the partial-publication-clear oracle follow that proof.

V22 separately records the generation-schedule lifecycle owner's bounded
latest-cycle state/completeness/scanned/deleted/backlog projection after
recovery and after convergence. The counters expose collection without
claiming a cumulative total. V17–V21 retain their frozen ordering and evidence
rules. The new diagnostic work reuses the exact trigger selection and moves
the bounded recovery poll. Store reads retain their existing at-most-64 retry
bound; a missing-row poll adds two such current reads around one direct digest
read, within at most 300 outer polls/five minutes and 30-second call deadlines.
Two lifecycle-status requests serialize at most 32 in-memory owner summaries
and retain one each, with no store/disk read. The change adds no source/content
scan, production write, lock, job attempt, child process, authority, or scale
claim.

### V23 never-executed phase hardening

Fresh V23 execution closes the collection, pressure, stale-worker, and
authorized-query edges found by enumerating every writer that can move their
oracles. A restarted lifecycle runner cannot sleep after only the suffix of a
persisted rotation; it observes a full local owner cycle first. Collection and
authorized-query restart use stable source/extraction/caller/relationship
semantic authority and refresh their baseline after a successful projection,
so a legitimate late relationship re-mint is not mislabeled drift.

Pressure still proves production `collect`, but the final shared-filesystem
reading may be 81–83% around frozen 82%. Before the executed marker, two `du`
passes charge the actual prepared workspace against the remaining 96-GiB
custody allocation and the 80-GiB ballast cap. Pressure and collection
lifecycle-entry deadlines now map to their frozen decisions rather than an
unclassified operational stop; delayed post-removal pressure recovery is the
separate unclassified environment result described below.

V23 stale-worker follows exact authority after `completion_failed` until the
reaper records `canceled` or the exact retired schedule proves the row was
collected; other settled fates fail immediately. Unknown settled lifecycle
vocabulary is reduced to closed `unknown` and fails promptly.
Authorized-query transport and 409 responses receive at most three
30-second-bounded attempts separated by one second; a terminal failure retains
only profile, endpoint name, class, status, and attempt count. Archive restore
keeps its outer direct-recovery P6 classification for every inner cause.
Historical V1–V22 bytes and validators do not inherit
these semantics, and no freeze, execution, release, or scale claim is
authorized by this change.

The V23 post-review closure makes the first cut sealable without weakening its
oracles. Error-state lifecycle evidence may retain bounded sweep counters only
in V23; correlated authorized-query plus meter/ceiling failures keep the
source-free query projection, while local setup failures do not fabricate one.
Historical unauthorized-probe response classification is restored exactly.
Pressure entry remains the substantiated production gate, but slow reclaim
after ballast removal is an unclassified environment recovery deadline, and
the pressure restart uses stable semantic authority.

Completed observations are prevalidated with a synthetic successful teardown
record before custody deletion, then validated again with real teardown
metrics. A future serializer/validator drift therefore retains custody instead
of destroying the only diagnostic state.

V23 also requires a closed interruption fate: `pending` is still reclaimable
and cannot pass as recovery. V22 retains its frozen pending behavior. After a
stale-worker `completion_failed`, the harness consults the store; a missing row
can pass only when its selected schedule digest is non-current beside a
distinct successor and is absent or closed. Normal stale-fence completion does
no store polling. Transient lifecycle-status reads retry inside the two fixed
30-second evidence windows. All store and HTTP calls keep their existing
deadlines, V1-V22 bytes remain historical, and this changes no production API,
authority, queue, attempt, or release posture.

### Neutral-33 and V24 corroborated requeue

Neutral-33's verified source-free package
`sha256:2c995686612db0a78d11e6f4dc216a8d337ddeb56d01070d225ac001f457ab2b`
binds source `29b727df789120ca86dc84f8c202868d5e94353f` and plan
`sha256:e494b38c27c9c23c66cc79ea859c2850e36a471f00344e5c5f326aeb6f96105d`.
Cold, warm-noop, delta-B, and return-A passed; interruption stopped at
`recovery_verification`. The early V22/V23 placement correctly beats lifecycle
collection, but V23 rejected the only immediate normal recovery shape:
pending at stale priority with ownership cleared.

Fresh V24 retains that placement and accepts `requeued` only after the exact
trigger row repeats on two one-second polls with the same schedule, scope,
generation, and attempt, `priority == GenerationPriorityStale`, and no lease.
Priority-zero pending, leased pending, running, or one observation does not
pass. Closed and corroborated-collected fates are unchanged. The source-free
projection adds only priority and lease presence; no lease token or private
worker detail is retained. The poll/query/deadline bounds and all production
behavior are unchanged. V1-V23 schemas remain historical, and this authorizes
no freeze, execution, release, or gate claim.

### V25 complete-run feasibility

Neutral-33's signed phase durations prove the former eight-hour total wall
cannot fund the remaining stale-worker, pressure, archive/restore, collection,
and authorized-query phases after a successful interruption. Fresh V25 freezes
a twelve-hour ceremony-only review ceiling and a measured 72-GiB pre-pressure
custody ceiling. Preflight projects that growth, requires the filesystem to
remain below lifecycle collection pressure, and proves the ballast still fits
inside the unchanged 96-GiB custody maximum.

V25 also makes extraction schedule identity transition-scoped after the first
schedule, so repeated A→B→A content authority never attempts to resurrect an
immutable superseded row. Full receipt validation now precedes destructive
teardown for both completed and stopped outcomes; lifecycle owner errors retry
without the one-hour park; heartbeat-loss stale fences use the exact store
proof; parent cancellation stays typed as cancellation; and transient `du`
failures receive two bounded retries. The ceremony wrapper's `seal` command can
resume missing receipt, incomplete seal, or package work while refusing to
rewrite a complete invalid seal. Historical V1-V24 schemas remain exact. V25
does not establish an SLO, scale result, release, freeze, or execution approval.

### Neutral-34 relationship-stage abort closure

Neutral-34's verified source-free package
`sha256:eac7ae816cfad54b27bda6ad86a2683374af5c468c268cd0848d00543b44da05`
binds source `26bd526` and plan
`sha256:9a0a0701bb5215ea0312aa0a16035ec0a35f3d8b86a0d97916f1ea74ff6c95ee`.
V24's corroborated `requeued` fate passed and exact A authority converged.
Interruption then stopped at `partial_verification`: a relationship build lost
a late fence after creating its `.stage-*` root, while startup repair had
already run and the lifecycle sweep was inside its one-hour idle interval.

Relationship publication now defers an idempotent stage abort after every
successful build. Publish closes the stage, making the defer a no-op; any late
fence, pin, or pre-commit publish error removes and syncs the unpublished
directory. The five-minute cleanliness gate and V25 evidence contract are
unchanged.

| Gate | Scanner-visible writer | Immediate cleanup | Fallback cleaner | Gate clock |
|---|---|---|---|---|
| interruption `partial_verification` | relationship root build | deferred abort on every unpublished return | startup repair or relationship lifecycle sweep (up to one-hour idle) | 300 seconds |

Any future change that widens an accepted recovery fate must re-derive this
downstream row against its writers, immediate cleaners, fallback cadence, and
gate clock before another ceremony is authorized.

### V25 pre-freeze production and harness audit

A fresh freeze must start with `run-large-mac-ceremony.sh preflight`. Before it
creates a ceremony identity or signing key, that command builds the prospective
V25 host-bound plan in a temporary directory and checks the exact ceremony
filesystem against the full 72-GiB pre-pressure projection and the required
fsync, hard-link, rename, and directory-sync protocol. It verifies the closed
module graph and prebuilds direct V25 Prepare, Execute, Cleanup, and receipt
commands. The process-launching production/harness packages, focused real-tool
proof, and semantic plus stale-worker rehearsals remain branch gates on the
exact reviewed commit; the expensive ceremony preflight does not rerun them
outside durable custody. Freeze repeats the host check to close the
preflight/freeze gap.

V25 builds from `plan.source_commit`, not the checkout's later `HEAD`, through
an isolated Git namespace that excludes ambient config, replacement refs, and
uncommitted attributes while preserving committed `.gitattributes`. Its Go
build and runtime controls are closed to ambient `GO*`, `GIT_*`, `PHEBS_*`, and
zoekt toggles. Historical V1-V24 source and execution environments remain
unchanged. Each private server owns a process session; graceful shutdown must
remove the whole session, and a forced or canceled shutdown remains an error. The
V25 wall clock begins at executor entry, including admission preflight.

Before either a completed or stopped run deletes custody, it durably publishes
an incomplete `observation.json.teardown` checkpoint beside the future
observation. After deletion it proves custody absent, rechecks the frozen host
toolchain, safety, deadline, and a 30-second persistence reserve, then
atomically publishes `observation.json` and retires the checkpoint. If the
process dies, `t4013-receipt` treats the checkpoint as authoritative over any
provisional final or `.tmp`, reconstructs a conservative stopped result, and
removes both checkpoint names before sealing. The wrapper drops its cleanup
trap after successful preparation; surviving custody is retained for reviewed
purge and is never inferred safe to delete from observation existence. A
crash-released directory lock serializes Execute and resume. Wrapper signals,
abnormal execution children with retained custody, refused cleanup, and
incomplete preparation all retain private state rather than deleting it.

The production failure-path review also keeps a retry bit for an entire
lifecycle owner cycle, durably discards every unpublished resolver, RPC,
Kafka, and composite relationship stage, and performs cancellation-independent
pin rollback before publication installation. Once installation is ambiguous,
pins remain for recovery rather than risking collection of current authority.
Partial-state inspection reads at most one repository beneath each of four
relationship roots and refuses more than 4,096 direct controls.

These changes add no query/request work, corpus scan, production schema, or
authority. A partitioned relationship transition adds one bounded current-root
open; successful stage-discard defers add only in-memory closed checks. Failure
cleanup, the five-second failed-cycle retry, bounded shutdown, the pre-freeze
probe, and constant-size teardown persistence occur only at their named
transitions.

This review does not authorize preflight or freeze yet. Session identities live
only in executor memory and a process snapshot cannot prove absence after
SIGKILL/OOM, PID reuse, session escape, or fork/exit churn. A stable reviewed
supervisor/sentinel or equivalent external proof must close that boundary.
Frozen host digests also do not yet bind every later executed path, private
binaries are not reverified across restarts, and HOME/module/control caches
remain ambient. The shell serializes supported operations, but direct
Prepare/Cleanup/Destroy calls still lack the shared V25 run-root lock needed to
exclude custody mutation races. After those corrections, passing the exact
module, filesystem, bounded-package, semantic, and stale-worker checks will be
readiness evidence, not freeze, execution, release, scale/SLO, accuracy,
completeness, migration, or decommission authorization.

### T40.13a fail-closed process sampling

V25 accepts a process sample only after complete bounded enumeration, coherent
identity/RSS observation, root checks, and child-lifetime classification all
succeed. Darwin uses native parent traversal and one task-all-info record per
accepted process. A denied descendant task-all-info row is absent only when
privilege-free short BSD info proves the PID missing or no longer parented to
the process that discovered it; a still-parented denial fails, and root denial
is always sticky. Linux retains bounded `ps`
enumeration plus `/proc` identity reads and at most two fresh whole-sample
retries for a typed disappeared child under the same two-second deadline. Every
other error is immediately sticky. A failed logical sample changes no metrics
and retains one typed first cause
plus a saturating count; phase reset does not erase it. Root absence fails
until the command's sole `Wait` owner records exit, after which an empty final
sample is valid only when no still-parented descendant is visible. The
concurrent server `Wait` handoff is reconciled before deciding that a missing
root was still expected live.

Exact clean commit `afa297966f7129bf7930c0834e8808c3992f35c5` passed the
complete package/race, real-launcher, production-path rehearsal, and
`internal/store` gates and independent review with no critical, high, or medium
finding. This is readiness to request integration only: no identifier is
selected or consumed, and freeze and execution remain separately authorized.

One synchronous pre-`Wait` root read binds the root before sampling. One Darwin
attempt performs at most 129 task-all-info reads, 129 child-list calls, and 128
short-info revalidations across at most 128 discovered candidates. One Linux
attempt retains its 128-KiB/8,192-row `ps` snapshot and at most 129
`/proc/<pid>/stat` identity reads. Per phase, the sampler retains only the
current accepted descendants and three cumulative counters, counts an accepted
absence/reappearance or changed kernel identity as another sampled lifetime,
refuses ambiguous continuity or category drift, and stops before exceeding
8,192 sampled lifetimes. Linux retry raises one logical sample to at most three
128-KiB/8,192-row snapshots and three sets of at most 129 native identities
within the same two seconds. Retries wait a fixed 25 ms, at most 50 ms total,
to leave the observed exit burst. A disappeared short-lived child may be absent
from the accepted observation, as it may already be absent between ordinary
250-ms samples;
zero means zero in accepted identity-bound samples, not proof of no transient
fork. All attempts and the one permitted root-exit handoff share one two-second
cap. Native identity reads have no separate timer, and
reset waits for the whole in-flight sample so an old snapshot cannot land in a
new phase. Strict paths take one initial sample and then at most one sample each
250 ms; close gives stop priority. The one-second allocation sampler also
retains only its first failure plus a saturating count. Sanitized
measured-command errors preserve both typed
sampler sentinels, which V25 treats as unavailable measurement rather than a
substantiated recovery result. V1–V24 keep their prior process-sampling cadence.
Neither native traversal nor the Linux `ps` row proves every transient fork;
same-kernel-token reuse and durable hard-death/escape absence remain T40.13c.
This changes no public schema or production work and authorizes no freeze or
ceremony.

Darwin now closes the split-observation boundary with native bounded parent
traversal. Each accepted process is one coherent `PROC_PIDTASKALLINFO` record:
PID, PPID, start identity, command class, and RSS cannot cross an exec/exit
transition between separate reads. A discovered child gone before that record
is absent from the accepted sampled-lifetime observation. Every other invalid,
drifting, over-bound, or unreadable record remains sticky. The sampler captures
the root-exit marker before observation; a false-to-true crossing discards the
whole attempt and permits exactly one fresh handoff observation under the same
two-second deadline. An already-observed exit requires an empty descendant set.
No failed attempt commits evidence. Linux retains the split-observation retry.
The sustained-churn regression, corrected real-binary rehearsal, bounded
package suite, and race suite pass. Full repository/store gates and independent
exact-clean-commit review remain mandatory before identifier selection.

### T40.13b cooperative cancellation and shutdown truth

V25 now keeps shutdown uncertainty sticky. A custody command that exits by a
signal, a server that requires forced termination, or a process session that
survives its first bounded shutdown wait returns the static
`errPrivateServerShutdownUnproven` sentinel even when a later poll sees no
process. Measured-command sanitization preserves that sentinel without
retaining private child output. This is a retention decision, not durable
absence proof.

Once V25 Prepare attempts its `.preparing` control or creates its workspace,
any incomplete return retains the workspace, manifest staging/final bytes,
control, and custody-local Git export namespace. Cleanup treats a preexisting
matching `.preparing` file plus a present workspace as an incomplete
preparation and refuses deletion; if custody is already absent, it may retire
only the matching manifest and control. V1–V24 keep their historical automatic
failed-Prepare cleanup.

Execute checks the original failure, server shutdown, operator cancellation,
and an external parent deadline before checkpointing and deletion. The frozen
total-wall deadline has a distinct reviewed cause and remains sealable. Execute
checks interruption again after the
durable teardown checkpoint, after custody deletion, and across terminal
staging, publication, validation, and checkpoint retirement. A detected
retention cause returns no observation, removes provisional terminal bytes,
and leaves the teardown checkpoint authoritative. The final pre-delete check is
the destructive linearization point: cancellation first observed after
recursive deletion starts cannot recreate custody, but it still prevents a
terminal observation and retains checkpoint/shell operation state. Final
checkpoint retirement is the terminal-publication commit boundary.

The supported shell runs Prepare and Execute as tracked process groups. Its
INT, TERM, and HUP traps forward the signal, reap the group leader, and retain
the operation lock, Go cache, controls, logs, and any surviving custody. It
does not run fallback cleanup, receipt construction, or sealing after such a
signal. Opt-in real-binary diagnostics likewise remove state only after a clean
server stop; failed stops report the retained absolute path.

Healthy execution adds constant-time context/error checks and shell
process-group bookkeeping only. A failed operation can retain the entire
bounded custody, cache, logs, controls, Git namespace, and checkpoint until a
separately reviewed purge. SIGKILL/OOM of the executor, PID reuse, session
escape, fork/exit churn, and registration of every child start still require
T40.13c's durable external supervisor/proof. No freeze, ceremony, release,
T40.13/Epic-40 closure, topology/bound change, or scale/SLO claim is authorized.

### T40.13c durable hard-death descendant supervision

V25 now uses `<workspace>.t4013-supervision` as its external descendant-absence
authority on Darwin and Linux. Prepare durably writes one random 256-bit token
to `prepared.json.preparing`, builds a token-named `.creating.*` directory with
strict `controller.lock`, `descendants.lock`, and bounded `state.json` files,
then atomically publishes and syncs the stable control before launching any
host-tool, Git/archive, authoring, server, backup/restore, SurrealDB, or indexer
child. A crash before publication can be completed only with that exact token;
an unrelated, malformed, or ambiguous stage refuses.

The controller holds the controller lock with close-on-exec. While state is
`live` or `finalizing`, it also holds a lease descriptor reserved at 64 or
higher with close-on-exec disabled, so every supported child and grandchild
inherits the same kernel lease even after reparenting, `setsid`, or an
intermediate process exit. To record `drained` or `terminal`, the controller
closes its lease copy and must acquire a new exclusive nonblocking lock. Any
remaining descendant prevents that acquisition. Executor SIGKILL/OOM releases
only its own copies: a surviving child still reports live, and once the last
child exits the unchanged active state reports indeterminate. Restart never
converts an active state to drained merely because its locks became free, and
it does not scan unrelated host processes.

The strict state binds token, plan digest, canonical workspace, Prepare versus
Execute ownership, phase, and the exact teardown-checkpoint digest. Prepare
hands Execute only an exact drained control. Cleanup, Destroy, and resume may
act only on the matching created/drained/terminal authority; live,
indeterminate, malformed, or mismatched state retains custody. Execute drains
before finalization, reopens inheritance for post-delete verifier children,
deletes custody, and drains again to terminal. It then retires supervision while
the external prepared/checkpoint controls remain authoritative, confirms exact
retirement durably, and only then removes those controls. Retirement first
renames the directory to `.retiring`, moves the exact terminal state to
`.retired` while locks and the directory are removed, and syncs each parent
transition. An exact restart can finish any committed retirement boundary; a
different identity cannot.

The supported driver prebuilds direct V25 Prepare, Execute, Cleanup, and
receipt binaries once before admission and starts them in its closed active
environment. There is no outer `go run` or immediately preceding Go/module
verification process around those operation roots. The full process-launching
test/rehearsal suites remain branch gates, not ceremony-runtime work. Receipt
resume may finish only exact drained/terminal supervision; any path remaining
after that recovery refuses further publication and seal. Abnormal command
state retains the operation lock, closed Go cache, controls, and custody for
review. Historical V1–V24 bytes and supported CLI flow remain unchanged;
direct legacy Destroy intentionally tightens symlink and
stable/retiring/retired V25-supervision refusal.

Each active controller holds two locks/two descriptors, and each descendant
inherits one descriptor. State is at most 2 KiB; transition and recovery work
is a fixed number of small reads, lock attempts, renames, and directory syncs.
There is no production request/query, sync, startup/restart, retry/no-op,
publication, source/corpus/shard, cache, or child-process cost. Tests cover
atomic create/retire recovery, controller death, escaped and intermediate-exit
descendants, finalizer drain, exact resume/cleanup, direct shell roots, residue
refusal, real Git/archive, and controlled server/backup/restore inheritance
with exact lease-inode checks. On Darwin, four isolated opt-in gates passed for
Go-run transitive inheritance, direct `zoekt-git-index`, Phebs-to-Surreal
hard-death inheritance after sampler shutdown, and direct Phebs backup/restore
roots. These are branch evidence, not independent proof of every
compiler/linker/indexer/restore descendant and not a rehearsal or ceremony
pass. T40.13d's shared V25 custody-mutation/admission lock remains next. No
preflight result, test, branch, or this closure authorizes freeze, ceremony
execution, release, T40.13/Epic-40 closure, topology/bound change, or a
scale/SLO claim.

### T40.13d custody mutation serialization and immutable admission

V25 now serializes direct Prepare, Cleanup, Destroy, Execute, Resume, and the
supported shell on one persistent `<run-root>/.t4013-operation.lock`. Darwin
and Linux use a nonblocking kernel lock; the empty 0600 inode is never removed,
so stale existence is harmless and SIGKILL releases ownership only after every
inherited descriptor closes. The shell prebuilds `t4013-lock`, re-executes
execute/seal beneath it, and passes the same descriptor through its closed
environment. Other platforms fail closed.

Plan, prepared, cleanup-control, and teardown-checkpoint bytes are bounded and
used only as preliminary locators before locking. Each mutator rereads and
compares the exact authority under the lock before any custody or output
mutation. Execute checkpoints also bind the prepared digest. Prepare refuses a
prepared output inside custody or the reviewed module checkout, and its CLI
uses the library's single plan decode. A no-checkpoint Resume is read-only: it
returns an already-settled final observation or refuses staged output.

One run root is deliberately serialized; different run roots remain
independent. The fixed admission bounds are 64 KiB for plan, 256 KiB for
prepared authority, 4 KiB for cleanup control, and 260 KiB for a teardown
checkpoint. There is one zero-byte inode and one held descriptor per direct
operation; the shell descriptor may remain inherited by a surviving descendant
until it exits. Retry performs one lock attempt and the same bounded reads. No
production query/request, sync, startup, publication, corpus/shard scan, or
tree hash gains work. Tests cover contention from every mutator, exact-byte
replacement, output boundaries, stale-inode reuse, inherited ownership, and
SIGKILL release while retaining historical V1–V24 behavior.

### T40.13e exact executed-tool identity

Fresh V25 plans bind a source-free digest of every canonical host-tool path as
well as version/content identity. Prepare, Execute, and resumed teardown retain
the matching private Go, Git/core, and SurrealDB paths. Every Go build command
and every Git export, authoring, checkout, or revision command rehashes and
invokes that exact path. Private server environments receive the exact
SurrealDB path and expected SurrealDB, zoekt, focused-index, and Buf digests.

The four custody-built binaries form one bounded digest snapshot. The harness
rehashes all four before each serve, backup, and restore launch, before creating
its log or renaming data. Phebs rechecks SurrealDB immediately before start and
zoekt, focused-index, and Buf immediately before each child launch. The
supported shell similarly retains and rehashes
its exact host tools and all five prebuilt V25 commands, including across the
run-lock re-exec. Replacement, symlink, and PATH drift therefore refuse before
launch mutation. Historical V1–V24 bytes and launch behavior remain exact.

Full Go/Git tree hashing occurs only at fixed admission and terminal snapshot
boundaries, never per poll or phase; later teardown checks rehash only the four
retained host executables. Each full tree remains capped at 100,000 entries and
2 GiB, host executables at 256 MiB, and private executables at 2 GiB. Empty
expected-digest settings add no production file hashing or child work. T40.13f
hermetic execution controls remains next. This ticket authorizes no freeze,
ceremony, release, T40.13/Epic-40 closure, topology/bound change, or scale/SLO
claim.

### T40.13e review correction: dedicated-host boundary

The complete-gate review supersedes the atomic “exact executed-tool” reading
above. The implementation rehashes a pathname and then launches it. Those
checks reject pre-check and persistent drift, but they do not prove the bytes
the kernel executes after the final check or a transient replacement restored
before the next snapshot. `ObserveHostToolchain` commits only the Go execution
core's bounded identities. The Bash interpreter and builtins plus driver
utilities `awk`, `basename`, `chmod`, `cmp`, `cp`, `date`, `df`, `dirname`,
`du`, `env`, `find`, `grep`, `lsof`, `mkdir`, `mktemp`, `pgrep`, `ps`,
`readlink`, `rm`, `rmdir`, `sed`, `shasum`, `sort`, `ssh-keygen`, `sysctl`,
`tar`, `uname`, `uniq`, and `wc` remain an enumerated trusted host TCB.

The supported envelope is therefore one dedicated, single-operator host.
Disable automatic/manual package, OS, and tool updates and stop every other
same-UID writer/process before preflight; keep that state through source-free
packaging. Every operational driver command refuses unless
`PHEBS_T4013_HOST_STABILITY_ATTESTATION` exactly equals
`dedicated-single-operator-host-with-tool-mutation-disabled`. Existing path,
content, tree, and closed-environment checks remain defense in depth. An
adversarial same-UID ceremony requires a separately reviewed atomic
snapshot/fd-execution design and is not authorized by V25.

### T40.13f hermetic execution controls

Fresh V25 Prepare binds one at-most-4-KiB execution-control manifest inside
custody. Authoring, source export, private module/build work, server/recovery
launches, and restarts use its custody-local HOME, XDG, temporary, module, and
build paths plus the reviewed Git exec directory. A fresh module cache is
hydrated only for the exact custody binaries' build dependency closures,
then checksum-verified online to admit lazy graph metadata before it is hashed
under the 100,000-entry/2-GiB tree bound. The
digest is compared after offline builds, and both caches are removed before runtime. Execute
reopens the exact manifest and cache-absence state before mutation and every
private launch. The supported shell mirrors the controls beneath the ceremony
root; historical V1–V24 execution remains unchanged.

### T40.13g authenticated returned-evidence firewall

`verify-bundle` requires an out-of-band reviewed signer fingerprint or exact
package SHA-256 digest. The package sidecar, bundled public key, and bundled
allowlist cannot authorize themselves. Signer mode matches the bounded
in-memory key to the reviewed fingerprint and creates a private allowlist;
package-digest mode authenticates the complete package before archive
inspection. The checksum-manifest signature is verified with that trust root
before its exact
eight canonical basenames are parsed or hashed. The frozen envelope signature,
plan digest, and exact checkout commit then use the same trust root.

The digest-bound `t4013-bundle` command reads at most 4 MiB of compressed input
and one MiB of expanded tar bytes. Before writing output it requires one
`evidence/` directory plus exactly one regular, non-link header for each of ten
fixed basenames, rejects duplicates and trailing streams, and applies 1-KiB
signer, 4-KiB control, 64-KiB plan, and 256-KiB observation/receipt limits. It
writes only 0600 files beneath one fresh 0700 temporary root. The shell now
builds and rehashes this eighth hermetic command and removes the root on every
exit. Small tests cover traversal, links, devices, expansion, unexpected
checksum paths, package-digest mismatch, and wholesale re-signing without a
ceremony run.

### T40.13h resumable seal and keypair integrity

An existing Ed25519 private/public signing pair must derive the same canonical
public identity before freeze or seal. The source-free manifest, checksum
signature, and checksum inventory are now one recoverable transaction. Their
three fixed stages are validated, authenticated, and durably synced before
publication; final order is manifest, signature, then checksum. A crash after
zero, one, two, or three final promotions resumes the exact retained bytes, and
a differing final is never overwritten. An incomplete non-authority stage is
durably discarded and regenerated after validation fails. Only the exact
ten-file evidence inventory passing full verification is complete.

The existing digest-bound promotion command supplies both stage-only durability
and byte-identical resume cleanup, syncing the file and every parent through
the ceremony root at each transition. Cheap keypair and fault-injection tests
cover the complete crash matrix without preparing a corpus. T40.13i remains
next; no freeze or ceremony is authorized.

### T40.13i bounded exact-control inspection

Fresh V25 control authority is read through one shared no-follow regular-file
boundary. It reads no more than the control's declared maximum plus one byte,
checks the same file identity before open and after read through both the open
descriptor and published path, and requires typed JSON to equal the exact
compact or indented bytes emitted by its writer with EOF immediately after the
single value. Final symlinks, path replacement, in-place metadata drift,
trailing values, and oversized controls refuse. Historical V1–V24 plan and
evidence bytes retain their existing decoders.

The driver prebuilds and rehashes `t4013-inspect` with the other V25 commands.
Plan schema/digest, freeze and transfer values, the eight-entry checksum
inventory, exact evidence/private directories, supervision residue, and the
closed shell manifest all use that command. Exact directories read only their
expected entry count plus one; the supervision-parent scan is capped at 4,096
entries. Current extraction inspection uses bounded descriptor enumeration at
both directory levels and admits no more than 64 canonical generation
controls. The closed shell manifest is not consulted as authority until the
inspector has been built and digest-bound.

These checks are control-plane-only. They add fixed identity stats around
already bounded reads, one binary to the existing batch build, and no new
cache, poll, production request/query, publication write, corpus/shard read, or
service child. Cheap tests cover symlink, replacement, trailing data,
maximum-plus-one byte/entry refusal, valid maxima, and retained V24 decoding.
### T40.13j overflow-safe ceremony arithmetic

All T40.13 phase, wall, byte, count, resource, timing, construction, and
receipt aggregations now use shared checked signed addition/multiplication and
refuse on overflow before any terminal comparison. MaxInt64 boundary tests cover
the arithmetic and metric merge paths; ordinary values and historical receipt
decoders remain exact. The checks add constant work per already-visited scalar,
with no wider wire representation or saturation. T40.13k remains next; no
freeze or ceremony is authorized.

### T40.13k complete executor-admission accounting

V25 Execute now brackets pre-phase-one admission with the existing bounded
fail-closed process sampler. Its wall time, peak RSS, and Git/index/other child
lifetime facts merge into the explicit `preflight` phase alongside private
toolchain-build metrics. A sampler or metric failure leaves phase accounting
incomplete and stops before completed evidence can be published or custody
destroyed. Historical V1–V24 accounting remains unchanged. The meter adds only
one bounded admission interval and no corpus read, production child, or wire
field; T40.13 remains open and no freeze or ceremony is authorized.

### T40.13l cost-first operator gates

The shell driver now checks immutable refusal facts before costly gates: freeze
rejects duplicate IDs and signer admission before host preflight; seal checks
run-root, frozen-plan, signer, and marker-bearing custody state before verifier
command construction; verify checks the run and plan before building verifier
custody; and preflight checks checkout cleanliness before cache or command
construction. Required semantic verification still runs after cheap admission
succeeds. Call-order regression tests prove the expensive gates are skipped on
these refusals. T40.13 remains open; no freeze or ceremony is authorized.

### Neutral-35 process-image accounting stop and T40.13m

`t40r1-neutral-35` is an immutable signed V25 stop. The structural convergence
observer reached `complete` after 3,660,371 ms and all 1,956 extraction
partitions completed without failure. The cold meter then surfaced 12 failed
samples sharing the sampler's first sticky cause: one coherent process identity
appeared with a different normalized class across accepted snapshots. V25
could retain only the generic `failed_phase_measurement_unavailable` code, so
the exact transition direction cannot be reconstructed after custody teardown.
Later phases correctly remain `not_run`; this result is neither a pipeline
failure nor a gate pass.

T40.13m treats `(PID, kernel start token, parent, class)` as the sampled
executable-image epoch. The same PID/start token/parent and same class is
unchanged. A new class on the same coherent identity and parent increments the
destination Git/index/other epoch once, updates the active epoch, and consumes
the existing 8,192 cumulative ceiling. A changed token or accepted
absence/reappearance remains a new process lifetime without an exec-transition
fact. Failed samples still commit nothing.

Fresh contracts advance to V26. Each phase can retain six optional integer
counters: other-to-Git, other-to-index, Git-to-other, Git-to-index,
index-to-other, and index-to-Git. They reveal no path, command, token, source,
or process output and must be absent/zero in V1–V25. Counter merge is checked
for overflow and total transitions cannot exceed the phase's retained sampled
epochs. Receipt construction requires the exact observation schema selected by
the plan, and each direction's incoming and outgoing counts must be covered by
the corresponding class epochs.

The change adds no process probe, retry, deadline, child, or production work.
Same-snapshot candidate/kernel class disagreement, parent or start drift,
unreadable identity, duplicates, cycles, root-exit handoff, the 128-descendant
bound, 250-ms cadence, and shared two-second attempt deadline remain
fail-closed. A deterministic Darwin test holds a real `git hash-object --stdin`
exec open long enough to prove the same kernel identity crosses from other to
Git; synthetic tests fence repeats, bounds, ambiguity, merge, and historical
schema behavior. Neutral-35 cannot be reused, and neutral-36 is not selected or
authorized by this correction.

Exact clean code commit `97772bb69fba77feb06fa79317b401d1e0815575`
passed the complete package/race, real-launcher, production-path readiness,
full `internal/store`, repository, and deterministic transition gates. Fresh
independent review reported critical 0, high 0, medium 0, and no actionable low
finding. T40.13m is complete and requests integration only; exact-main
preflight, identifier selection, freeze, frozen-plan review, and execution
remain separate steps.

### Neutral-36 restart measurement stop and T40.13n

`t40r1-neutral-36` is an immutable signed V26 stop at exact source
`acc5a23f046229c580b972bcbb0107f2f7062882`. Its plan is
`sha256:e2403ee87df84383e47b5b78a1f7fc1085425da3ec1b5af5f3214fa4e03ca9e7`,
observation is
`sha256:141750ff0ae7da9af7e006bfb59cc260ff973abe02509e2e269474dea7c8d22d`,
receipt is
`sha256:9d9ec605ad90ccd1010a920cb86c405656851349d85ccb0ac2243b18606e6ee6`,
and source-free package is
`sha256:e5ec0c04338b17d91064c160f34a1a78b6ba174773107bfd592d2bf80f0e0677`.
The first five mechanics phases succeeded. Interruption selected an
attempt-zero extraction partition, returned source to A, and stopped after
6,059,839 ms at `restart_start` with the generic
`failed_phase_measurement_unavailable` code. V26 does not retain the failed
data gauge or its raw cause, so the decision remains unsubstantiated
`unclassified`, not a pipeline failure or pass. Recovery verification and all
later mechanics phases were not run. Teardown completed in 187,542 ms without
retaining derived data or scratch source.

T40.13n advances only fresh plan, observation, and receipt contracts to V27.
The successful first-server meter can return a private
`dataMeasurementBoundary` containing its raw end allocated bytes and canonical
workspace. The immediately following restart accepts it only once and only for
that workspace. The value never enters evidence or durable authority. Without
the handoff, restart takes an allocated-only baseline before process launch;
the allocation sampler and wall clock start there. A failed prelaunch baseline
creates no expected/active meter. A launched server is registered before
health, so health failure still leaves a complete meter inventory. Allocated
and logical gauges keep the existing 30-second deadline.

V27 may retain one optional path-free `data_measurement_failure` with schema
`t4013-data-measurement-failure-v1`, scope `custody`, gauge `allocated` or
`logical`, reason `deadline`, and `deadline_ms=30000`. It contains no path,
command, output, identity, or raw error; it is permitted only for a stopped V27
measurement-unavailable result and must be absent on success and in V1–V26.
Archive/restore also retains the logical/allocated maxima already merged from
backup, restore, and restarted-server meters instead of overwriting them with
a terminal re-gauge.

Regressions must cover same-workspace one-shot use, missing/mismatched/reused
boundaries, failed prelaunch inventory, launched health failure, exact
diagnostic validation and returned-bundle reconstruction, historical bytes,
simultaneous failures, and archive maxima. The real readiness sequence must
finish both ordered start meters, leave expected/tracked counts equal at two
with no active meter, and retain both healthy source-free startup records. The
two meters perform five sequential strict gauge boundaries—one more than the
prior rehearsal—with at most three `/usr/bin/du` attempts each, an individual
30-second cap, at most 15 child attempts, and at most 150 seconds aggregate.
That rehearsal also adds one private server launch/health cycle and its process/
allocation samplers relative to the prior sequence. The real interruption path
falls from 20 to 16 gauge boundaries (at most 60 to 48 child attempts), while
archive/restore falls from 18 to 15 (at most 54 to 45). Production ceremony
server count is unchanged; one allocation-sampler goroutine now probes capacity
at 1 Hz during the existing bounded executable-revalidation/prelaunch window.
Evidence/checkpoint decoding adds one bounded raw-JSON presence check to retain
historical absent-field/null semantics; V27 receipt decode also performs one
bounded canonical JSON re-encode and byte comparison to refuse duplicate keys.
This is bounded ceremony-only work: it
changes no product request/query, sync, publication,
source/corpus/shard read, persistent state, topology, or service bound.

The first V27 readiness attempt retained a source-free recovery defect: exact A
domain pointers were current while the operational current schedule still
targeted settled B. The runtime now subjects both completed-generation
reconcile and exact-authority reuse to the same schedule-coherence check. A
nonzero active or settled target mismatch uses the existing immutable
transition enqueue. A zero-applicable mismatch instead retires the exact
current projection, leaves immutable history lifecycle-owned and exact roots
authoritative, writes no binding, and returns the established `unavailable`
operational state rather than claiming one fictional partition. Focused
active/settled and new/reused zero-work regressions supplement the crash/no-op
test that proves an A-targeted schedule, `Progress.State == current`, and no
repeated source or extractor work. The corrected real-binary rehearsal passed
structural A→B→A/restore, semantic interruption/restore, and stale-worker
recovery. Exact active
reuse keeps its early return. Absent reuse adds a second bounded schedule query
and no binding read; settled reuse adds that query plus two pointer-sized
binding reads across initial and repeated target resolution. On a nonzero
mismatch, reuse totals three schedule-query/binding-read pairs—two before
enqueue and one inside—while completed reconciliation totals two—one before
and one inside. Enqueue adds the pointer-sized binding write and bounded
schedule transaction under the existing shard lock and chunk limits. A
zero-work mismatch stops before enqueue: reuse performs two pairs and completed
or new reconciliation performs one, then the exact retirement transaction does
its current/schedule point reads, active-only status update, and current-row
deletion. Concurrent successor movement makes it stale without mutation. No
new corpus/member read, source lease, extractor, goroutine, lock,
API shape, persistent schema, topology, or service-bound work follows.

Implementation and exact-tree gates are complete. The complete package
(97.258s), race package (109.786s), real-launcher proof (60.902s), 20 repeated
V27/schema/accounting runs (248.065s), semantic (124.67s), stale-worker
(31.30s), structural (138.58s), full `internal/store` (1065.618s standalone;
1109.512s in the uncached repository run), module, vet, lint, docs, glossary,
shell, whitespace, and every `internal/` package passed. A host-native sampler
`EPERM` invalidated one earlier structural attempt after healthy startup; no
process survives, the diagnostic root is retained, and the same tree passed
the bounded rerun. The extra repository aggregate is baseline-red only on
inherited T30.6m budgets, Git-2.54 T32.3 retained bytes on Git 2.50.1, and
T32.4 pre-repin bindings, all reproduced unchanged at the base commit. It is
not claimed green and those fixtures are outside this ticket. Independent
review found exact source commit
`b5d6b74da8644811c5e1bfffd658b73661797ee2` functionally clean and one low
cost-record issue, resolved by the source-identical correction above. T40.13n
requests integration only. Integration, exact-main
preflight, fresh-ID selection, freeze, plan review, and execution remain
separate explicit decisions; no gate or Epic closure is claimed.

### Neutral-37 partial-verification stop and T40.13o

The recent ceremony history contains three distinct stops:

- neutral-35: V25, exact source
  `158dc6c9d87c26e4e7fc6a2f2ce38cc900da2119`, plan
  `sha256:d9c1a646a7722c0d6496d1866c3a1450cbcbdfbf5c17c340d324173fe2ea543c`,
  cold measurement stop after 63.325 minutes;
- neutral-36: V26, exact source
  `acc5a23f046229c580b972bcbb0107f2f7062882`, plan
  `sha256:e2403ee87df84383e47b5b78a1f7fc1085425da3ec1b5af5f3214fa4e03ca9e7`,
  `interruption/restart_start` stop after 327.939 minutes; and
- neutral-37: V27, exact source
  `3d6ecf294e655c9121ea57cdec24b23b91a1cf4e`, plan
  `sha256:52b6c9d519358d84c34cbdb5b49bc44eff22005298e4a281ed3a598d82896f5b`,
  `interruption/partial_verification` stop after 317.565 minutes.

Neutral-37 proved requeued recovery for the selected lease and completed exact
clean teardown. Its reconciled controlling signed attribution is
`recovery/direct_recovery_failed/p6_investigation/substantiated`. V27 cannot
name the retained partial owner/kind, prove simultaneous capture failure, or
separate a partial-clear timeout from a scanner error. Neutral-36 likewise
establishes neither a pipeline failure nor a scale pass. The V26 meter defect
predated neutral-35, so these stops are not a causal chain in which each repair
introduced the next defect.

T40.13o rejects a typed-nil data-measurement deadline before either V27
classification or command-failure sanitization can consume it. Nondeadline
causes therefore remain byte-for-byte classification-equivalent across the
V26/V27 boundary, and a sanitized backup/restore failure cannot panic on a nil
deadline.

Relationship and resolver publication now have one cancellation boundary
before commit. New/reused generations are completely validated before a marker
can exist; after that point the bounded marker→pointer→marker-removal sequence
finishes without repeating the full generation validation. Existing hard-death
and control-I/O recovery authority is unchanged.

Extraction publication now gives package-owned stages bounded startup recovery
before workers and real lifecycle ownership. Startup validates every raw
generation, restore, and sparse stage tree, atomically moves it into a retired
namespace, and durably syncs the parent. It performs no deletion or drain and
preserves bytes and modification time. Scheduled lifecycle promotes a retired
stage to collecting, with a durable parent sync, only when it is at least 24
hours old or older than the newest two for its repository and kind. Collecting
stages then drain unconditionally across bounded turns and restarts under every
controller limit. A raw stage created after startup remains untouched and makes
completeness lower-bound. Sparse-candidate residue is production-owned here but
excluded from ceremony partial attribution.

Startup checks cancellation before each raw-stage collision preflight and
rename, then syncs any already-renamed prefix before returning.

Fresh ceremony contracts advance to V28. A stopped
`interruption/partial_verification` may retain exactly one paired owner/kind:

- owner: `observation_publication`, `extraction_publication`,
  `relationship_publication`, `resolver_namespace`, `rpc_caller_postings`, or
  `kafka_topic_postings`;
- kind: `publishing_marker` or `stage_directory`.

The fixed-order ceremony scan visits only those six bounded publication roots,
prefers marker over stage, and retains no path, name, timestamp, raw error,
source, or content. V1–V27 reject either field; V28 rejects incomplete, null,
hidden-duplicate/mixed-case, unknown, or outcome-incoherent fields through the
observation, receipt, returned-evidence, and teardown-checkpoint boundaries.

Publication removes one full validation and adds one constant cancellation
check. V28 adds one bounded six-root scan to each existing partial-verification
poll. Startup acquires and holds the shared lifecycle-mutation lock once for one
pass capped at 2,000,000 charged work operations, stats, and stage candidates,
eight scanner-charged peak descriptors, and 510,000,000 name bytes; the eight
exclude the one existing mutation-lock descriptor. It reads names/types/metadata, not
content, hashes nothing, and deletes nothing. Startup inventories at most 4,096
regular plus 4,096 sparse repository namespaces and may retain those 8,192
bounded identities. Each lifecycle turn acquires that existing shared lock once
and holds it while inventorying at most 4,096 repositories in one publication
or sparse phase. Either path accepts at most 20,000 direct
entries from one selected repository directory and retains at most one extra
entry only to detect overflow. Each turn also admits at most 64 candidates,
sixteen removals, 256 stats including descriptor-open stats, eight peak
descriptors, and 1 MiB of names. Clean completion is exact
and idle; raw post-start residue is lower-bound without permanent backlog.
Lifecycle alone promotes eligible retired residue and drains collecting
residue. Product
query/request, repository sync-tick, corpus/shard/content, hash, cache, worker,
and child costs are unchanged. No new lock primitive is added. New non-reused generation creation adds
one serial result-directory fsync per accepted domain (zero through 64) before
the existing final stage sync/rename. Reuse/no-op adds none; a failed absent-
generation rebuild repeats those bounded syncs, while restore's existing per-
domain sync is unchanged. The new syncs extend the existing one-of-64
reconciler shard-mutex hold.

The pre-review tree passed 20 deterministic V28/typed-nil repetitions (1.495s),
complete package (103.560s), full package race (113.820s), focused publication
race, real-launcher custody proof (62.074s), every uncached `internal/` package
including standalone `internal/store` (983.068s), and module/vet/lint/docs/
glossary/shell/whitespace. One complete readiness attempt ran 233.93s; semantic
and stale-worker passed, while structural alone met host-native sampler `EPERM`
after healthy startup. No PID/session survived, diagnostic root
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-403545186`
is retained, and the one bounded structural rerun passed in 194.515s. Recorded
independent review of exact commit
`704c2360e75e8a7d7068cbf3cd49b492a84cb50d` found critical/high 0, medium 1,
and low 1. The cancellation and cost-record findings above are corrected. The
corrected tree passed 20 cancellation repetitions (0.597s), extraction normal/
race (7.560s/9.354s), lifecycle (0.615s), command (12.288s), and static/docs
gates. Its two structural confirmations were invalidated by repeated native
sampler `EPERM` after healthy `http_ready` (82.405s and 80.245s); PIDs
79356/81088 are gone, roots
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-2026572958`
and
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-180141300`
remain retained, and no third retry is permitted. Fresh review of exact
corrected source commit `710f66f440464c4dabf1723f98134cb941c07232` found
critical/high/medium 0 and one low lock-cost wording gap. Source-identical docs
commit `c4dfdabbd594b5f841b92058923343382d6cf5aa` corrected it and passed exact
re-review with all severity counts zero. No code-review finding remains; later
host-clean structural confirmation remains.
No integration, fresh ID, rerun, freeze, execution, release, T40.13/Epic-40
closure, topology/bound change, or scale/SLO claim is authorized.

### Neutral-38 warm-noop stop and V29 coherence

Neutral-38 is an immutable V28 phase-3 stop. It binds source
`b79406d12f517caed08f07120ca91b0ac1fbe471`, plan
`sha256:da1804e13afb7b04a45a462552b75627ebb3a6e58bbe95c03c4fbad8080d2506`,
observation
`sha256:5d6482c6bd64a7e6cca44a5975d5f56290f1de3e8c6ab7d3e57675565fa40d2f`,
receipt
`sha256:d24439bab4d275a93f8c8cd5ddaa0b5e36e7e6455df3f3fb3cc2a97d594e9ad5`,
and source-free package
`sha256:16f0794e5aee06fd7396c401faf31945331376f130bbd4e7db50bb20c8a96658`.
Preflight passed in 66.663s and cold passed in 79m56.010s. Warm restart stopped
after 5.069s because V28 rejected two sampled Git lifetimes. The paired healthy
startup also retained exactly two Git lifetimes; snapshot authority did not
move, index children and publication writes/transactions were zero, and two
controls were reused. Teardown passed in 89.630s and destroyed derived and
scratch-source custody. The retained `member_reads=2,001,958` is the cumulative
post-phase fallback-read plus settled-partition cardinality, not a warm-phase
delta and not an oracle input.

The ordinary server intentionally enqueues connection freshness work at every
boot. V29 therefore treats startup Git as part of restart admission: the full
warm phase must retain exactly the same sampled Git lifetime count as its own
healthy `warm-noop` startup. A newly sampled post-health Git lifetime still
fails, as do any index child, publication write/transaction, authority or exact
snapshot change, or missing control reuse. V1–V28 keep their frozen zero-Git
predicate and serialized meanings.

The production-binary readiness rehearsal now calls the same `warmNoop`
implementation after cold convergence on the 4,096-file structural projection.
V29 changes no production startup, sync, watcher, request, retry, publication,
or lifecycle behavior. Ceremony execution adds fixed startup-record and bounded
schema lookups, three closed identity/outcome checks, two checked three-counter
sums against the existing 8,192-lifetime ceiling, and one scalar equality over
already-retained values; readiness adds one bounded restart/health/revalidation
cycle. A fresh ceremony still requires the complete gate, independent review,
integration, exact-main preflight, and separate freeze and execution authority.

Exact implementation commit `06b6e61e2316b33b5cad326e9efa2c9b97194309`
passed fresh independent review with critical/high/medium/low all zero after
two medium custody/bound corrections and one low cost-record correction. Its
corrected content passed 20 V29 repetitions, complete package/race,
real-launcher custody, all non-store `internal/` packages, standalone
`internal/store`, module, vet, lint, docs, glossary, shell, and whitespace. A
pre-review structural run crossed production `warmNoop` with startup/phase Git
counts 3/3. The final content still lacks host-clean readiness: one attempt
stopped before launch on fresh-module network timeout and one unchanged retry
reached healthy `http_ready` before sticky Darwin native sampler `EPERM`.
Retained roots are `phebs-t4013-readiness-2696656312` and
`phebs-t4013-readiness-2146731620`; PID 66440 is gone and no process survives.
No further same-host retry, integration, freeze, execution, release, Epic
closure, topology/bound change, or scale/SLO claim is authorized. The next gate
is one later host-clean complete exact-commit readiness pass.

### Neutral-39 source-free disposition and V30 integrity

Neutral-39 returned execution status 1 and sealed a source-free package at
`sha256:681aef5bb4ebe77c63ed564f5dfe499609a76738c3172b7a58e9c9f87d6a43cb`.
The wrapper monitor's last useful output was `prepare/admission` with unknown
custody age. Its terminal-summary `jq` expression then failed on shell quoting.
That script defect did not disturb the execution, but the returned custody
contains no raw private error. Do not infer sampler `EPERM`, phase 6, or any
other named phase/cause from this evidence.

Fresh V30 contracts retain an otherwise-valid startup log, health boundary, and
stage when process sampling fails; the unavailable process counters use the
existing zero sentinel, carry `process_sampling_unavailable=true`, and are not
measured-zero claims. A process-only failure
seals `failed_phase_process_sampling_unavailable`; an allocation-only failure
seals `failed_phase_allocation_sampling_unavailable`, while a mixed measurement
failure keeps `failed_phase_measurement_unavailable`. Historical
V1–V29 contracts remain exact.

V30 warm restart incrementally tracks phase-local candidate and job lifecycle
reports within the existing revalidation deadline. A candidate report with
`outcome=done` and `decision=warm_noop|cold_reuse|marker_recovery` is necessary
but does not complete the boundary. Released, failed, or requeued work rejects;
claimed, started, deferred, and yielded work remains unresolved until its later
`event=done,outcome=success`; exact authority is re-inspected on every attempt;
and one existing five-second convergence interval must add no candidate or job
report after all jobs resolve. The phase meter finishes once at that bounded
quiescent boundary. Its single finished `PhaseMetrics` snapshot refreshes paired
startup process counters without another sampler read; log/stage/wall refresh
there. The atomic warm→delta handoff transfers the finished process window with
the exact log EOF and performs a bounded post-reset tail scan: any candidate/job
report or partial tail refuses the boundary, complete unrelated lines advance
the warm EOF, and later exact claimed/started reports remain delta before
complete phase/startup Git comparison. This accepts health-before-sync/reuse
without accepting sampled post-boundary Git. V1–V29
keep their frozen warm predicates.

V30 ceremony servers enable synchronous exact candidate output and lifecycle
sinks for the sync, fetch, candidate, extraction, resolver, caller, and index
job runners. Any encode, size-cap, sink, or panic failure latches cancellation;
after background workers join, the server returns a nonzero terminal error.
Ordinary runtime sinks remain advisory when exact-control mode is absent.

V30 phase 7 fences lifecycle evidence immediately after ballast removal. Every
owner attempt must be newer than the fence and preserve one coherent sorted
cycle; one final exact-normal capacity observation must follow the newest owner.
Phase 7 then re-inspects protected authority and requires it unchanged. It does
not claim capacity stayed normal throughout that cycle and does not wait for the
runner's hourly-idle eligibility. Independently, the shared production runner
requires exact-normal capacity immediately before and after the sorted
cycle-start owner, then one wholly normal, error-free, drained sorted cycle
before hourly idle.

The V30 phase-8 restore comparator clears only `IndexedCommit`,
`RelationshipGeneration`, and `RelationshipRootDigest` before comparison. It
retains `CallerGeneration`, `RelationshipSemanticDigest`, and all remaining
product content. The restored readiness rehearsal
uses this same comparator and rejects stale or all-`not_run` lifecycle status by
requiring all 14 owners fresh and `state=ok`, exact drained results for the 13
non-`durable-jobs` owners, truthful `durable-jobs` `lower_bound` with backlog
permitted, and exact capacity after the latest owner.

V30 phase 9 restarts the server and accepts only a phase-local cycle with all
sorted frozen 14 owners fresh and `state=ok`. The 13 non-`durable-jobs` owners
must be exact and drained; `durable-jobs` must truthfully remain `lower_bound`
and may retain backlog because live writers prevent an exact oracle. Capacity
must be exact after the latest owner and current stable authority unchanged.
Observation and receipt bytes record one bounded source-free row per owner:
name/state/completeness/scanned/deleted/backlog. The phase does not require a
deletion and does not claim to live-prove rollback, active-lease, marker, or
store-pin roots individually. Those protection guarantees remain mandatory
`internal/lifecycle`, `internal/store`, and publication regression gates.

Production lifecycle scheduling retains the five-second ordinary backlog/error
cadence and one-hour healthy idle cadence. While capacity is `collect`/`refuse`
or the existing pressure-recovery latch remains set, it caps the serial
owner-turn delay at 250 milliseconds. The latch still clears only after
exact-normal capacity and one wholly normal, error-free, drained sorted cycle.
Only that clean path has the existing at-most-28-turn bound for this 14-owner
inventory and at-most-64-turn bound under the 32-owner controller ceiling; its
scheduled delay is now at most seven or sixteen seconds, respectively, plus
sweep work. An owner error/backlog or unavailable/non-exact capacity removes
that turn bound. If pressure has been observed, the 250-millisecond cadence
remains latched until a later clean cycle; a never-pressure-latched ordinary
retry remains five seconds. The phase-7 and phase-9 waiters make no 28/64 claim
and remain governed by their fixed phase deadlines. Truthful `durable-jobs`
backlog does not block either waiter. A later unpressured phase-9 restart and
its unresolved owner progression retain the ordinary five-second cadence.
Healthy normal hourly steady state is unchanged. No product
query, request, repository sync tick, retry/no-op, publication transition, lock,
cache, corpus/shard read, persistent schema, memory/disk bound, or child-process
work changes. Ordinary startup adds one closed exact-control environment lookup
and branch; when absent it allocates no report channel or sink and adds no
persistent work. Ceremony phase 9 adds one
restart, the same bounded owner/status work, and scalar-plus-14-row validation
with 13 exact/drained predicates and one durable-job lower-bound predicate.
Warm restart adds incremental phase-local candidate/job lifecycle parsing,
exact-authority reinspection per attempt, one existing five-second quiet
interval, one finished-metrics startup refresh without a second process read,
one bounded post-reset log-tail scan, and the atomic process/log-EOF handoff
under the unchanged revalidation deadline. Phase 7 adds one existing
post-recovery protected-authority inspection. Only exact-control mode adds
synchronous per-report log writes for candidate output and all seven job
runners; ordinary steady-state reporting is unchanged. No production request,
job, child, or new deadline is added.

The 250-millisecond value is a post-sweep delay, so elevated mode offers at
most four owner-turn starts per second before sweep duration; it does not claim
four completed turns. Each start also performs the existing capacity gate
check, timer allocation, and in-memory status update. The host gate check uses
two `Lstat` calls plus open, `fstat`, `fstatfs`, and close. These operations may
run at 20x the former pressure cadence until the latch clears; owner limits and
serial execution remain unchanged.

The pre-review focused and bounded regressions passed, including the complete
package (104.564s), full package race (129.985s), real-launcher custody proof
(115.754s), complete production-path readiness rehearsal (884.324s), every
`internal/` package including `internal/store` (1081.231s) and `internal/sync`
(61.915s), module verification and compilation, vet, repository-pinned
golangci-lint 2.12.2, documentation, glossary, shell, and whitespace.
Independent review of exact commit
`4b40beb28e1549a4d269a7a7e0d9ed604c775c4b` recorded 0 critical, 0 high, 3
medium, and 3 low findings. The correction tree latches every non-exact capacity
sample through its whole lifecycle cycle, refuses exact-mode stale-reap
mutations/errors, preserves and seals same-phase process-sampling evidence after
review-ceiling teardown, continues warm confirmation from its settled cursor,
allocates no exact-report state when disabled, and corrects the lifecycle owner
count to fourteen. Exact correction commit
`ec4f2500d1b68dcbe539667d5833fdf694bc5adc` passed the complete machine gate:
package 101.806s, race 116.836s, real launcher 64.460s, readiness 820.702s,
command 12.869s, `internal/store` 1019.180s, `internal/sync` 64.082s, and all
static/documentation checks. Re-review recorded 0 critical, 0 high, 0 medium,
and 2 low findings: deterministic warm-cursor close on two pre-confirmation
failure paths, and two contradictory startup-cost sentences. The next
correction keeps the proven cursor FD under unconditional phase-meter finish
cleanup and fixes those sentences; focused normal/race, docs, and glossary pass.
A new immutable commit, complete gate, and fresh independent review remain
pending. No merge, exact-main preflight, fresh-ID freeze, execution, release,
T40.13/Epic-40 closure, topology/bound change, or scale/SLO claim is passed or
authorized here.

Final exact source commit `50df638ad065814f4a9ea75c4f7493a622df3de0`
closes cleanup-only safety-metric loss and passed independent review with all
severity counts zero. Complete package 106.392s, race 134.820s, real launcher
61.472s, command 19.006s, module/compile, vet, pinned lint, docs, glossary,
shell, and whitespace pass. The gate remains open: a complete readiness run and
its one bounded unchanged confirmation both reached healthy structural
`http_ready` before sticky Darwin root-sampler `EPERM`; semantic 302.46s and
stale-worker 31.04s passed in the complete run. Root denial remains fail-closed
because retry could miss a descendant lifetime. The full internal command also
timed out in store schema application at 1320.596s; the
isolated exact subtest passed in 11.349s and all completed packages were green.
No process survives. A later host-clean complete readiness and full
internal/store pass are mandatory before merge, exact-main preflight, fresh-ID
freeze, or execution can be requested.

## T40.13r Darwin denied-descendant helper removal

The prior `root-sampler EPERM` attribution is superseded. Retained readiness
custody proves PID 554 was a still-parented descendant of the healthy Phebs
startup root, but it did not retain that PID's executable identity. A bounded
live-host reproduction establishes the causal mechanism without claiming that
missing historical field: the production compatibility monitor launched this
host's setuid-root `/bin/ps` every 50 ms, and coherent task-all-info inspection
of that live helper returns the same `EPERM` while short identity succeeds.
The Darwin private-session custodian also retained a separate `ps` launch that
could reproduce the class while admission accounting samples the executor.

T40.13r removes both Darwin helper launches. Compatibility RSS monitoring uses
one bounded native process-group inventory plus coherent resident-byte records;
private-session custody uses bounded native all-PID enumeration, `getsid`, short
zombie-state records, and final session revalidation. Linux behavior is
unchanged. The strict ceremony
sampler, including its frozen denied-child reconciliation, and V1-V30 schemas
remain exact. The new paths retain the existing compatibility
50-ms/512-MiB/three-failure policy and 1,024-session-member bound, introduce
128-member process-group and 8,192-non-kernel-host-PID bounds, and retain
existing shutdown cadence/deadlines. They remove repeated setuid
children and text parsing; malformed, duplicate, permission, capacity, and
arithmetic failures remain closed.

The earlier exact candidate's later serial `internal/store` run passed in
1003.037s. This ticket still requires focused package/race and custody gates,
static/documentation checks, a fresh immutable commit and independent review,
then one host-clean exact-commit readiness rehearsal. It selects no identifier
and authorizes no merge, exact-main preflight, freeze, execution, release,
Epic closure, or scale/SLO claim.

Exact gate result (2026-08-26): implementation commit
`9bee810cd692d831993ff2e4784fb067f628b768` passed focused native/custody
repetitions, changed-package normal/race, module verification, vet, pinned lint,
documentation, glossary, shell, and whitespace. Independent exact-commit
review reported critical/high/medium/low all zero. The unsandboxed host-clean
structural rehearsal passed in 373.567s and completed exact teardown. The
serial `internal/...` command reached the default ten-minute package alarm in
fresh store initialization after every earlier completed package passed; the
exact standalone full `internal/store` package then passed under its 30-minute
allowance in 993.780s. No SurrealDB child or port-65499 listener survives. This
closes T40.13r's review, readiness, and full-store blockers and makes the
source-identical branch eligible for a separate integration request; it does
not authorize merge, exact-main preflight, identifier selection, freeze,
execution, release, Epic closure, or a scale/SLO claim.

Independent review found one medium gap in that record: structural readiness
alone did not establish the inherited complete-readiness gate. The unchanged
implementation then passed the remaining `semantic` and
`semantic-stale-worker` legs together in 390.868s. The earlier 373.567s
structural result remains valid across the documentation-only commit because
no compiled, embedded, fixture, or harness input changed. All three readiness
legs are green for exact implementation commit
`9bee810cd692d831993ff2e4784fb067f628b768`; no rehearsal process or
port-65499 listener survives. Fresh exact-HEAD documentation re-review remains
the final branch-close check, and all integration/freeze/execution boundaries
remain unchanged.

## T40.13s neutral-40 phase-6 recovery

Neutral-40 stopped honestly at phase-6 partial verification with retained
`observation_publication` / `stage_directory` custody. Exact implementation
commit `0e5eba0109e632b9a1bd8f24c9f876aca5146e68` retires an exactly validated
same-generation stage through the existing collecting lifecycle after durable
root/marker completion and admits V28 retained-partial attribution in later
frozen versions. V1–V27 remain exact.

Affected normal/race, focused repeated, vet, documentation, glossary, and
whitespace gates pass. The exact-clean real-binary semantic rehearsal passed
the phase-6 interruption/restart and partial-state-clear boundary: first and
restart meters were 8,889/6,961 ms, final data gauges were 219,816,960 logical
and 220,921,856 allocated bytes, the semantic subtest took 295.38s, and the
top-level rehearsal test including tool builds took 456.36s; the package
command completed in 456.991s. Backup/restore, restored lifecycle, and
authorized query also passed. Successful cleanup removed the temporary
workspace and no matching process remains. This is focused readiness, not a
phase 7–11, full-ceremony, release, Epic-closure, or scale/SLO result.

The separately run human Phase-7 `semantic-stale-worker` selector subsequently
emitted `semantic stale-worker boundary passed`; its subtest completed in 32.22
seconds, the top-level readiness test in 197.17 seconds, and the package command
in 197.835 seconds. Successful cleanup removed that run's private workspace.
The retained terminal record does not bind an exact HEAD and clean-checkout
proof, so exact candidate attribution remains open. This is focused Phase-7
evidence only, not prior-phase handoff, full-scale custody, release, or SLO.

## Focused Phase-8 pressure rehearsal

Human Phase 8 is `pressure` (`phaseOrder[7]`). The earlier `structural`
readiness leg skips it and proceeds to backup/restore, so that leg is not
pressure evidence. The separately opt-in `structural-pressure` leg reaches the
small structural A→B→A-return boundary and then calls the production
`execution.pressure()` coordinator. It requires a real `collect` lifecycle
cycle, unchanged protected authority, verified ballast removal, a fresh
coherent V30 normal owner cycle with final exact-normal capacity, unchanged
recovered authority, complete metering, and proven server shutdown.

Never run this selector with `TMPDIR` on the host data volume: at the current
host capacity the production target would allocate about 57 GiB. The supported
wrapper admits one run at a time, requires at least 32 GiB available on the
backing `/private/tmp` filesystem, creates a dedicated 16-GiB APFS sparse
image, and gives the test a separate 16-GiB allocation ceiling. The Go test
refuses a larger or already-pressured filesystem before profile authoring and
again immediately before ballast:

```sh
./spike/t4013/run-phase8-pressure-rehearsal.sh
```

Success detaches and deletes the image and includes
`structural pressure collect/recovery boundary passed`. Failure attempts a
non-forced detach and retains the sparse image; a detach failure reports and
retains the mount, image, and exclusive lock. The retained image contains private logs,
credentials, and synthetic authored source, so do not rerun blindly, share it,
or delete it before review. A hard-dead wrapper may also leave the exclusive
`/private/tmp/phebs-t4013-phase8.lock`; prove process absence before removing
that lock. The bounded rehearsal exercises real APFS allocation and all
production lifecycle owners, but it does not prove the two-million-owner
accumulated state, root-volume ballast scale, Phase 1–7 handoff, signed ceremony
custody/evidence, or any later phase. It intentionally builds the working tree;
an immutable clean commit and unchanged exact-commit rerun are required before
carrying the result into candidate readiness.

The 2026-08-26 host audit found about 142.4 GiB available while the V25
pre-pressure projection requires about 168.7 GiB before operating margin. A
focused pass therefore does not make the current host ready for another full
ceremony; recheck and free at least the remaining projected deficit before any
freeze.

The first focused run reached real pressure and ballast removal, then failed
honestly after 947.94 seconds because `observation-v2-generations` still had
410 deletion units pending after nine selected-owner turns. The detached image
was reviewed, proved free of a live process, mount, and lock, then permanently
purged; about 15.7 GiB was reclaimed. Read-only inspection of retained full
structural custody found 1,546 deletion units for generation A and 1,547 for B.
At sixteen deletes per selected-owner turn, B needs 97 turns. Fourteen-owner
fair rotation makes a one-second delay incapable of meeting the unchanged
ten-minute recovery deadline. The corrected production runner therefore uses
250 milliseconds only during `collect`/`refuse` or latched pressure recovery.
A deterministic real-collector regression drains the exact 1,547-entry shape
and reserves at most 350 seconds of scheduled delay after worst alignment and
two fresh cycles, leaving more than four minutes for bounded work and status
polling. This is headroom, not a Phase-8 pass; do not rerun the focused wrapper
until the correction is on an immutable clean commit and its review gates pass.

That corrected rerun subsequently passed from exact clean commit
`37fba2896f500104fa8283914ed19b8a003e3a24`. It emitted
`structural pressure collect/recovery boundary passed`; the Phase-8 subtest
completed in 252.48 seconds, the top-level readiness test in 310.35 seconds,
and the package command in 311.030 seconds. The correction commit is integrated
into `main`; a later read-only host check found no matching Phase-8 diagnostic
path or live rehearsal/Surreal process. This supersedes only the focused-rerun
requirement. Prior-phase handoff, the full corpus/root-volume shape, signed
custody, later phases, release, closure, and scale/SLO claims remain open.

## Focused Phase-9 archive/restore rehearsal

Human Phase 9 is `archive_restore` (`phaseOrder[8]`). The V30 text that calls
`collection` “phase 9” uses its zero-based index; that is human Phase 10. The
separately opt-in `structural-archive-restore` leg reaches the small structural
A→B→A-return boundary and calls the production `execution.archiveRestore()`
coordinator directly:

```sh
PHEBS_T4013_READINESS_REHEARSAL=1 \
PHEBS_T4013_ARCHIVE_RESTORE_REHEARSAL=1 \
go test ./spike/t4013 \
  -run '^TestProductionPathReadinessRehearsal$/^structural-archive-restore$' \
  -count=1 -v -timeout=35m
```

A pass includes `structural archive/restore authority boundary passed`. It
proves the small-profile live backup, stopped source server, offline restore,
restored-boundary inspection, restored server convergence, V30 product
comparison, exact Phase-9 metrics, and shutdown through the production
coordinator. It does not run human Phase 10 collection and proves neither the
Phase-8 handoff nor the two-million-owner, signed-custody, or full-ceremony
shape.

No sparse image, ballast, or fixed lock is used. Success removes the temporary
workspace. Failure reports and retains the exact private
`phebs-t4013-readiness-*` root containing logs, credentials, synthetic source,
backup, and derived data; prove its processes and listener absent before
review, purge, or rerun. The rehearsal builds the working tree, so repeat it
unchanged on an immutable clean commit before carrying the result into
candidate readiness.

The first dedicated working-tree run passed: the Phase-9 subtest completed in
88.70 seconds, the top-level readiness test in 147.32 seconds, and the package
command in 147.917 seconds. Exact two-meter/two-server accounting and final
shutdown passed; successful cleanup removed the private workspace, and a
separate host check found no matching Phebs/Surreal process. This claim is only
about the current run; older retained readiness roots remain untouched. Because
the run preceded the immutable implementation commit, repeat the same selector
on the clean commit before treating it as candidate evidence.

## Focused Phase-10 collection rehearsal

Human Phase 10 is `collection` (`phaseOrder[9]`). Run its separately opt-in
real-binary selector directly:

```sh
PHEBS_T4013_READINESS_REHEARSAL=1 \
PHEBS_T4013_COLLECTION_REHEARSAL=1 \
go test ./spike/t4013 \
  -run '^TestProductionPathReadinessRehearsal$/^structural-collection$' \
  -count=1 -v -timeout=35m
```

The fixture authors the small structural repository at A-return before its
first server launch, converges that clean phase-entry state, and then calls the
production V30 `execution.collection()` coordinator. A pass includes
`structural collection fresh-cycle boundary passed` followed by `PASS`. It
proves the real stop/restart, a fresh sorted 14-owner cycle, the owner-specific
exact/drained rules, exact capacity after the latest owner, unchanged stable
authority, bounded collection evidence, exact two-meter/two-server accounting,
and shutdown.

Do not change this isolated fixture to replay A→B→A. That transition creates
same-generation collecting residue whose drain belongs to Phase 8; replaying it
without the Phase-8 handoff would turn this into a predictable timeout instead
of a Phase-10 test. The passing focused Phase-8 rehearsal covers that drain.
This selector therefore does not prove Phase-8 or Phase-9 handoff residue, the
full structural corpus, eligible deletion, individual rollback/lease/marker/
store-pin roots, signed ceremony custody, or later phases.

No sparse image, ballast, or fixed lock is used. Success removes the temporary
workspace. Failure reports and retains the exact private
`phebs-t4013-readiness-*` root containing logs, credentials, synthetic source,
and derived data; prove its processes and listener absent before review, purge,
or rerun. The selector builds the working tree, so an immutable clean commit
and unchanged exact-commit pass are required for candidate evidence.

The unchanged selector passed from an exact clean checkout at commit
`15487bbf15b602b04d81fbae6b989777b5cac44d`: the Phase-10 subtest completed in
147.89 seconds, the top-level readiness test in 205.45 seconds, and the package
command in 206.121 seconds. It emitted the required boundary marker, removed
its successful workspace, retained no diagnostic root, and left no matching
Phebs/Surreal process. That commit is integrated into and pushed as `main`.
This remains clean-entry focused evidence; the earlier Phase-9 exact-commit
rerun and every broader custody, scale, and release gate remain open.

## Historical focused Phase-11 authorized-query rehearsal

This pre-T40.13x contract is retained only to interpret the 2026-08-26 result.
The same selector now owns the combined Phase-9→10→11 gate documented under
`Neutral-43 Phase-11 stop and T40.13x`; use that current command and marker.

The fixture converges semantic A, starts structural A-return while the semantic
port is occupied, then stops semantic, re-reserves that port, and converges
structural before calling the unchanged production V30 authorized-query
coordinator. It required the historical marker
`authorized-query dual-profile boundary passed` followed by `PASS`. This
covers the semantic restart, stable authority revalidation for both profiles,
the fixed unauthenticated, search, service-inventory, relationship, and
citation checks, exact control-read and two-meter accounting, at least the three
mandatory query-member reads, and shutdown. Existing unit tests separately pin
the retry and terminal-failure projections.

That historical clean-entry selector did not replay Phase 8, 9, or 10 and did not prove
handoff residue, full structural corpus, signed ceremony custody, release, or
scale claim. It uses no sparse image, ballast, archive, or fixed lock. Success
removes the temporary workspace. Failure reports and retains the exact private
`phebs-t4013-readiness-*` root. A query failure logs the bounded source-free
projection when present; prove its processes and listener absent before review,
purge, or rerun. The 30-minute test context and 35-minute command timeout are
bounds for those healthy rehearsal profiles, not the production ceremony's
combined phase ceiling. It is superseded and must not be rerun as the current
candidate gate.

Exact focused result (2026-08-26): the unchanged selector passed from exact
clean commit `c2e6eed8faab01854f3af94264ec3054487c877e`. It emitted
`authorized-query dual-profile boundary passed`; the Phase-11 subtest completed
in 44.46 seconds, the top-level readiness test in 104.93 seconds, and the
package command in 105.614 seconds. Both stable-authority waits, every fixed
query/citation oracle, exact control-read and two-meter accounting, the
mandatory query-member minimum, listener transfer, semantic restart, and final
shutdown checks passed. The preceding `readiness pending` messages were bounded
nonterminal convergence observations, not failures.

The exact-HEAD and clean-worktree guards passed. Successful cleanup retained no
current-run diagnostic root, and a separate host check found no matching
Phebs/Surreal process. This closes only the focused Phase-11 exact-run requirement. It does
not prove Phase-8/9/10 handoff residue, full structural scale, signed ceremony
custody/evidence, a complete ceremony, release, Epic closure, or a scale/SLO
claim. The earlier Phase-9 exact-clean rerun remains formally open. This result
record is source-identical documentation after the tested commit.

## Focused Phase-12 teardown rehearsal

Human Phase 12 is `teardown` (`phaseOrder[11]`). Its separately opt-in test has
its own private parent because the production coordinator destroys the exact
`custody/` child:

```sh
PHEBS_T4013_READINESS_REHEARSAL=1 \
PHEBS_T4013_TEARDOWN_REHEARSAL=1 \
go test ./spike/t4013 \
  -run '^TestProductionPathTeardownRehearsal$' \
  -count=1 -v -timeout=20m
```

The fixture builds the working-tree toolchain and both existing bounded
profiles inside custody, publishes a schema-valid synthetic V30 prepared
manifest outside it whose copied control labels use the frozen ceremony
identities while the authored bytes retain their bounded projection identities,
and represents Phases 1–11 with the existing receipt-valid source-free
completed-prefix fixture. It acquires the production run-root lock and real
prepare→execute supervision before launching one structural Phebs/Surreal
session through `http_ready`; Phase 11 normally leaves only structural live.
Before any authoring or build it binds `HEAD` and rejects modified, staged, or
untracked files. It repeats that exact check before destructive entry and after
terminal retirement, then prints `teardown exact source commit: <commit>` next
to the pass marker. It also performs the same terminal host-tool and completed-
receipt prechecks as the dispatcher and calls the unchanged production
`execution.teardown()`.
Require `teardown custody retirement boundary passed` followed by `PASS`.

A pass proves the live process/session stopped without an unproven forced
shutdown; exact nonzero data gauges; terminal checkpoint retirement;
exact-scope custody deletion and stable absence; final observation publication
and completed receipt validation; terminal descendant drain; supervision,
prepared-publication, provisional-file, and checkpoint retirement; survival of
the outside sentinel and module root; and run-root-lock retention until the
simulated Execute return. Existing unit and atomic tests separately own
cancellation, hard-death, deletion/publication failure, and resume coverage.
The deterministic package regression separately owns checkpoint-before-delete
ordering.

The test has a 15-minute context and a 20-minute package timeout. One run pays
seven bounded Git control invocations, private dependency hydration/
verification, four Go builds, two bounded-profile
authors without convergence, one structural server startup, two serial
whole-custody `du` traversals, one serial server stop, recursive exact-custody
deletion, small atomic checkpoint/observation writes, descendant drain, fsync,
and host-tool revalidation. The two 30-second teardown reserves are recorded
in evidence rather than slept. This focused bound is not a maximum for deleting
the full ceremony custody.

No sparse image, ballast, convergence, query, archive, pressure cycle, or full
corpus is used. On success the test removes only its new
`phebs-t4013-teardown-*` parent after every assertion. On failure it attempts
tracked-server shutdown and retains that complete exact parent. A pre-delete
failure can retain custody; a post-delete publication or retirement failure can
instead retain external checkpoint, stage, or supervision authority after
custody is absent. Do not rerun, resume, share, or purge it before proving the
session absent and reviewing the exact retained state.

This is isolated Phase-12 evidence only. The source-free fixture does not prove
Phases 1–11, their handoff, full structural scale, signed ceremony custody or
evidence, a complete ceremony, release, Epic closure, or a scale/SLO claim.
The correction still required an independent review and unchanged exact-clean
run before planning the full ceremony.

The first exact-clean attempt at commit
`cbbb873d251b56c0a2cd645ab02c99ee3a60d90a` stopped before prepared
publication, supervision, or supervised Phebs/Surreal server launch because
those copied labels still used the projection-profile names. Review found no matching process and purged
the retained 207 MiB temporary root. The corrected fixture maps only the
manifest copies to the existing frozen profile constants and has a fast
schema-invariant regression; the authored profile bytes remain unchanged.

The corrected selector passed unchanged from exact clean commit
`81d0a7a73214dbfa906e01eb3a8d611e8e950b2a`:

```text
teardown exact source commit: 81d0a7a73214dbfa906e01eb3a8d611e8e950b2a
teardown custody retirement boundary passed
--- PASS: TestProductionPathTeardownRehearsal (87.79s)
PASS
ok github.com/bmeddeb/phebs/spike/t4013 88.348s
```

All selector assertions passed, and successful cleanup left no matching
temporary root or process. This closes only the focused Phase-12 exact-run
requirement. At that point, the Phase-7 and Phase-9 exact-clean reruns,
prior-phase handoff, complete ceremony, release, Epic closure, and scale/SLO
claims remained open. This
source-identical result record grants no integration, preflight, freeze,
execution, or push authority.

The unchanged Phase-7 `semantic-stale-worker` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`ce6212974f40fc452a124345c751a2b5bd473f9f`. It emitted
`semantic stale-worker boundary passed`; the subtest took 32.52 seconds, the
top-level readiness test 91.40 seconds, and the package command 92.025 seconds.
Its pending and HTTP-409 lines were bounded nonterminal convergence
observations superseded by the terminal pass. Successful cleanup removed the
current workspace, and a separate check found no matching rehearsal process.
This closes only Phase-7 exact attribution. Phase-9 exact-clean attribution,
prior-phase handoff, complete ceremony, release, Epic closure, and scale/SLO
claims remain open. This source-identical record grants no integration,
preflight, freeze, execution, or push authority.

The unchanged Phase-9 `structural-archive-restore` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`0d4cd82132bca5a0c48d1d1df9e377a0720c4bb9`. It emitted
`structural archive/restore authority boundary passed`; the subtest took 89.04
seconds, the top-level readiness test 148.55 seconds, and the package command
149.292 seconds. The final active relationship-progress row was a bounded
nonterminal convergence observation superseded by the terminal pass. Exact
two-meter/two-server accounting, shutdown, and successful current-workspace
cleanup passed; a separate check found no matching rehearsal process. This
closes Phase-9 exact attribution and the separately identified Phase-7/9
reruns. Cross-phase handoff, complete signed ceremony, release, Epic closure,
and scale/SLO claims remain open. Integration, exact-main gates, fresh-ID
selection, freeze and frozen-plan review, execution, and push remain separate
actions requiring their own authorization.

## Neutral-41 sealed stop and T40.13t

The exact V30 `t40r1-neutral-41` ceremony bound source
`a28e0573f0089c22dda610ad1bf065328d47865d` and frozen plan
`sha256:8799f5e63f61b44ecea7b3e08f607922715589a0832b0b2802f75824ad9fd507`.
Returned evidence verification passed. The sealed source-free package is
`sha256:8b29e86c7227752964addd1c5dc06c729ed53288d0371b6926c78dc4dc555423`.

Phases 1–6 passed. Phase 7 completed its functional stale-worker fence and
convergence, then the terminal exact allocated-custody `du` exceeded the
30,000-ms gauge deadline. Phases 8–11 did not run. Teardown proved clean
custody and process retirement. The separately reviewed host-pressure
reservation was durably removed after package disposition. Treat this as an
honest harness-accounting stop, not a pipeline failure, scale pass, release, or
Epic closure. The identifier is consumed and must never be reused.

T40.13t advances only fresh ceremony bytes to V31. It retains exact `du` as
the sole strict logical/allocated meter and gives each whole-custody invocation
300,000 ms, for at most 10 minutes per serial pair. All strict callers
must propagate the same deadline. A fresh
`t4013-data-measurement-failure-v2` projection may report only the exact gauge,
`reason=deadline`, and `deadline_ms=300000`; it retains no path or raw cause.
V1–V30 schemas and predicates remain exact. The permanent identifier fence
rejects neutral-41 and first admits neutral-42.

Before another freeze, run the focused timeout, v2 diagnostic/round-trip,
strict-caller propagation, historical-version, and identifier tests, then the
exact full-profile replay through Phase 7's terminal gauge. This is
harness-only: each pair retains two serial gauges, and each gauge permits the
unchanged maximum of three serial `/usr/bin/du` attempts inside its one
deadline. A healthy first-attempt pair launches two children; a completed
retrying pair may launch six and repeat the metadata walk six times. Meter or
measured-command begin and finish can consume two pairs, while the V27 restart
start consumes one allocated-only gauge plus its finish pair. All return early
and remain inside the unchanged 12-hour ceremony ceiling. No production query,
worker, sync, store, lifecycle, publication, lock, cache, corpus read,
memory/disk allocation, or child-process cost changes.

Neutral-41 retained no physical custody. Its 2,294,916 logical Git-tree owners
and Phase-6 byte maxima do not reproduce Phase 7's filesystem entries,
directories, hardlinks, cache posture, or concurrent work. Neither a synthetic
file tree nor the small `semantic-stale-worker` rehearsal satisfies the gate.
A new freeze remains forbidden until an exact full-profile Phase-7 replay
passes and its cleanup is confirmed.

### Exact full-profile Phase-7 replay

The dedicated runner now exercises the real frozen profiles and the unchanged
production prefix through `stale_worker`; it is not the small semantic
rehearsal and creates no synthetic proxy. Run it only from the separately
reviewed exact-clean commit on the dedicated host. The supported entry path
starts with fixed `/usr/bin/env -i` and `/bin/bash --noprofile --norc`, then
materializes and verifies the committed wrapper outside the checkout before
executing it; do not invoke the live worktree copy directly:

```sh
/usr/bin/env -i \
  HOME=/dev/null PATH=/usr/bin:/bin:/opt/homebrew/bin LC_ALL=C LANG=C TZ=UTC \
  PHEBS_T4013_PHASE7_REPLAY_REVIEWED_COMMIT=REPLACE_WITH_REVIEWED_40_HEX_COMMIT \
  /bin/bash --noprofile --norc -c '
    set -euo pipefail
    umask 077
    checkout="$(cd "$1" && pwd -P)"
    expected="$PHEBS_T4013_PHASE7_REPLAY_REVIEWED_COMMIT"
    [[ "$expected" =~ ^[0-9a-f]{40}$ ]]
    bootstrap="$(/usr/bin/mktemp -d /private/tmp/phebs-t4013-phase7-bootstrap.XXXXXX)"
    runner="$bootstrap/run-phase7-full-profile-replay.sh"
    cleanup_bootstrap() {
      status=$?
      trap - EXIT
      /bin/rm -f -- "$runner" || status=1
      /bin/rmdir -- "$bootstrap" || status=1
      exit "$status"
    }
    trap cleanup_bootstrap EXIT
    closed_git() {
      /usr/bin/env -i HOME=/dev/null PATH=/usr/bin:/bin LC_ALL=C LANG=C TZ=UTC \
        GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
        GIT_ATTR_NOSYSTEM=1 GIT_NO_LAZY_FETCH=1 GIT_NO_REPLACE_OBJECTS=1 \
        GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 \
        /usr/bin/git -c core.hooksPath=/dev/null -c core.attributesFile=/dev/null \
          -c core.excludesFile=/dev/null -c core.fsmonitor=false "$@"
    }
    blob="$(closed_git -C "$checkout" rev-parse \
      "$expected:spike/t4013/run-phase7-full-profile-replay.sh")"
    [[ "$blob" =~ ^[0-9a-f]{40}$ ]]
    closed_git -C "$checkout" cat-file blob "$blob" > "$runner"
    [[ "$(closed_git -C "$checkout" hash-object --no-filters "$runner")" == "$blob" ]]
    /bin/chmod 700 "$runner"
    export PHEBS_T4013_PHASE7_REPLAY_CHECKOUT="$checkout"
    export PHEBS_T4013_HOST_STABILITY_ATTESTATION=dedicated-single-operator-host-with-tool-mutation-disabled
    printf 'Phase 7 replay bootstrap: %s\n' "$bootstrap"
    source "$runner"
    main "$expected"
    trap - EXIT
    /bin/rm -f -- "$runner"
    /bin/rmdir -- "$bootstrap"
  ' phase7-bootstrap /Users/ben/phebs.com
```

To place the fresh run root somewhere other than `/private/tmp`, add an
explicit `PHEBS_T4013_PHASE7_REPLAY_PARENT=/absolute/private/path` assignment
to the leading `/usr/bin/env -i` invocation; it must remain
outside the source checkout. The wrapper
prints that root before authoring. It holds a fixed exclusive replay lock and
the Go test holds the inherited run-root lock across preparation, execution,
and cleanup. The wrapper binds and revalidates its canonical Go driver, clears
ambient Go environment, workspace, and overlay controls, and builds with fresh
private Go build and module caches. It also binds Git, creates an owner-only
shared clone with an empty template, closed system/global config and attributes,
disabled hooks/excludes/fsmonitor/replacement objects, force-detaches it at the
reviewed commit, and compiles and runs only from that source. The Go child
inherits the same closed Git environment for VCS stamping and module fallback.
Its own pre/post blob checks are drift fences after
the external bootstrap establishes the executing bytes. Modified, untracked,
and ignored private-source inputs are rejected
before and after execution; an ignored live `_test.go` therefore cannot enter
the replay. The clone shares immutable Git objects and performs no fetch.
Clone, checkout, and Go each run synchronously below a shell sentinel that
remains the owned process-group leader until the direct workload exits. The
wrapper stops that pinned group, requires an exact snapshot containing only the
live sentinel, resumes it, and releases it through a parent-held descriptor for
its private FIFO; any dead, other, malformed, or uninspectable member retains
the sentinel, control root, and fixed lock. A nested launcher installs
terminating traps before it emits ready; the parent retries an interrupted
ready read and forwards a latched signal only after consuming ready.
Deterministic before-ready and after-ready regressions prove a signal cannot
cross into an unstarted workload. Per boundary this adds one sentinel shell,
one nested launcher shell,
two FIFOs, one
parent-held read/write release descriptor, one parent read-only notification
descriptor, three empty-marker creates, one status write plus rename, two
notification writes, one release write, one marker unlink, and normally one full-host process
snapshot; fail-closed quiescence permits at most 100 snapshots and about one
second of ten-millisecond waits. The sentinel alone holds the notification
writer while its workload runs with that descriptor closed, so completion or
hard death wakes the blocking parent read with a record or EOF and launches no
polling process. Exact job comparison adds one short command-substitution Bash
child at drain entry and one more only when a signal handler enters. Clone,
checkout, Go, and recursive private-cache/source
retirement make four such children. Hydration and compilation retain their
already-documented lack of a wrapper deadline.
The printed owner-only bootstrap contains only the reviewed wrapper. Success
removes it; a refusal, failure, or signal after wrapper admission retains it
beside the reported replay state for the same post-absence housekeeping review.
Caches and the private source can add checkout, dependency-download, compile,
disk, and wall cost; they are removed on success and retained beside the run
root on failure. Inside the test binary, plan observation and exact
two-profile preparation have a four-hour context deadline. The unchanged V31
execution deadline then starts with execution admission and remains 12 hours.
The Go test alarm is 20 hours from binary start, leaving four hours beyond
those two maxima for stop/retirement and diagnostics. Fresh module download
and compilation happen before that alarm, have no separate wrapper wall
deadline, and remain cancellable through the documented signal path.

After Phase 7's terminal exact logical/allocated meter succeeds, no pressure
function is called. A V31-only deliberate boundary uses the normal resumable
stopped-teardown protocol to stop/join descendants, checkpoint, delete custody,
publish its cleanup observation, and retire supervision and the prepared
authority. The final `phase7-replay.json` is a distinct source-free record. A
pass requires phases 0–6 succeeded, nonzero terminal data gauges, pressure not
started, re-bindable ports, exact-clean source unchanged, and durable custody
and supervision retirement. A late server-stop error keeps its real failure
identity and cannot be accepted as the deliberate boundary. It explicitly establishes no ceremony pass,
scale/SLO, freeze, or release authority.

INT, TERM, and HUP are forwarded to the test's process group. On any failure
the wrapper prints the retained run/control roots. Do not rerun, share, or
purge them before process-absence review; the fixed exclusive lock is also
retained to block an accidental second attempt. After hashing the result and
retiring private caches, success atomically writes the exact commit, result
path, and digest to `completion` inside that lock. Only the wrapper's terminal
zero-status `PASS` plus a matching completion marker admits
`phase7-replay.json`; a file or marker left by an interrupted wrapper is not a
pass. The lock requires separately reviewed retirement after result and
process-absence review. The exact expensive replay and review of its resulting
digest remain mandatory before a new freeze.

## Failed V31 full-profile replay and T40.13u

The exact V31 full-profile replay ran for 23,469.777 seconds and stopped in
`stale_worker` with `T40.13 convergence transition limit exceeded`. Its
2,540.013-second wait recorded 509 attempts, 155 progress changes, and exactly
32 transitions. Fifteen extraction-progress HTTP 409 responses classified as
`status_other` alternated with pending projections. This defeated adjacent
coalescing even though the schedule remained active at 272 materialized, 70
succeeded, 202 pending, and zero failed. The final successful projection was
fresh, so the evidence does not establish a stalled pipeline.

The source-free cleanup observation is bound by
`sha256:c69ce4124464f22934a2cd5972898ad1a7143604dbe1fcabdddcefa2689d675d`.
The replay stopped before the later explicit stale-chunk fence, published no
Phase-7 pass result, and authorizes no new replay, freeze, ceremony, release,
Epic closure, or scale/SLO claim. Its separately reviewed process-absence and
cleanup result does not retire the external replay lock.

T40.13u advances only fresh ceremony evidence to V32. Under that contract,
seven closed snapshot/authority 409 details map to the existing `409_stale`
reason only on their exact endpoints: two observation-progress details, two
extraction-progress details, and three caller-generation-progress details.
The convergence tracker counts every such conflict, keeps exact first and last
wall times, and holds only the latest transition. A later recognized conflict,
including one from another eligible stage, replaces the hold. Same-stage
pending progress clears it and resumes the existing coalesced pending
transition. Any non-recognized class or stage materializes the conflict first.
Recording also materializes an unresolved hold, so a wait ending on one or
containing only conflicts still has a timeline tail matching its last
inspection.

The 32-transition ceiling is unchanged. Observation control-absence, unknown
409s, transport errors, 5xx, 503s, response/control failures, terminals,
non-progress stages, and overflow inspections remain distinct. With 31
existing entries, a held conflict can
become entry 32; a following terminal remains the overflow inspection and the
existing diagnostic-limit priority keeps its typed projection. The historical
relationship-tail fence is relaxed only when that counted conflict is the
retained tail or overflow inspection at the exact aggregate last-conflict wall
time; unrelated and conflict-free limits remain strict. V1–V31
classification and receipt validation remain exact. Fresh full-profile pass
records use `t4013-full-profile-phase7-replay-v2` and bind a V32 plan.

V32 also names the two exact extraction-progress 500 details without making
them transparent: `Read` is `500_store` and `Invalid` is `500_response`.
Either detail on another endpoint and every V1–V31 occurrence remains
`status_other`. These 500s never enter the retry hold, so adjacent identical
shapes use only the historical coalescer while 500/pending alternation and
distinct 500 faults continue consuming the diagnostic inventory.

This changes no production wire behavior or work. Existing caller-generation
detail strings become named constants. Each V32 HTTP error performs one closed
endpoint/detail match and each convergence probe performs one closed predicate;
a recognized conflict updates one counter, two times, and one fixed
transition value. At most one transition is materialized before the existing
bounded clone, and the source-free wait adds at most three scalar fields. No
request count, five-second poll cadence, deadline, lock, hash, child,
memory/disk ceiling, or production authority changes. Observation and receipt
decode plus teardown-checkpoint resume each perform one extra in-memory
field-presence scan over their already-read bounded bytes (256 KiB, 256 KiB,
and 260 KiB respectively) and at most 16 convergence waits; this adds no I/O.

## Exact V32 full-profile Phase-7 pass

The exact-clean V32 candidate
`968311621f389643365587f4ae588ba83c832e68` passed the dedicated full-profile
replay. The test took 21,280.34 seconds and the wrapper returned terminal PASS
after 21,281.087 seconds. Preflight, cold, warm no-op, delta B, return A,
interruption, and stale worker all succeeded with exact oracles. Phase 6
`interruption` took 7,253.516 seconds and Phase 7 `stale_worker` took
2,303.216 seconds.

The v2 source-free result is retained at
`t4013u-v32-full-profile-phase7-replay.json` with digest
`sha256:0e17da4500e8000713ca8e3abc6f97041772b3d78bdb2bf3661589f5e5b84c75`.
It binds the exact V32 plan digest
`sha256:8784172854b86275d55705e920e6bf6e0499910e3d254c961a41639a0f5a3005`
and clean-teardown observation digest
`sha256:6eaef4eb7cea706c2e9b5874a5e09e0e3978e6cdb6363fd316263c9650a8a426`.
Five converged waits counted six recognized progress-retry conflicts and
retained only five to seven transitions, so the run exercised V32's correction
without reaching or changing the 32-transition limit.

The deliberate `after_stale_worker_before_pressure` boundary started no
pressure work. Terminal logical and allocated gauges were nonzero; stopped
teardown retired custody, supervision, the private driver, and the bootstrap.
Separate result and process-absence review matched the plan, cleanup, result,
and completion-marker hashes and found no surviving process, listener, holder,
or mount. Preserve the source-free result before retiring the temporary result
root, fixed completion lock, and host-pressure reservation.

This closes only T40.13u's exact Phase-7 replay/result gate. It establishes no
Phase-8-or-later handoff, complete ceremony, scale/SLO, freeze, release, or
Epic-40 pass.

## Neutral-42 Phase-9 stop and T40.13v

The exact-main `t40r1-neutral-42` ceremony passed its frozen preflight and
advanced through archive/restore, then returned status 1 with a verified
source-free package. Its exact source is
`4496d5e12ebc026e2a12e8011505207f6582aaf1`, plan digest is
`sha256:6818fa92a235ecad3978b48e3a6d6d4f67eba9e9647035d5eb2cd134207ae080`,
and sealed package digest is
`sha256:9bb96d6c0dc059f6f34573c0b4469f8968eaf8fe3b89009ab39312ce5f94ec74`.
The bundle is an honest stop, not a ceremony or Phase-9 pass.

The restored store correctly omitted restartable generation schedules and
derived publications. It did not invalidate repo-level latest extraction,
resolver, and caller job projections, whose pointer, ordering timestamp, and
writer-version marker still belonged to the pre-backup control epoch. Phase 9
therefore saw no current extraction schedule but repeatedly read the same
historical failed extraction projection and applied the unchanged current-job
terminal oracle. The archive/restore boundary itself and server health had
already passed; the evidence establishes a stale authority projection, not a
new extraction failure.

T40.13v moves invalidation to the production generation-control restore
transaction. It unsets all three downstream projection triples without
decoding or deleting any job history and leaves the independent index-job
projection intact. The next generic enqueue now projects an exact coalesced
extraction row just as the existing caller/resolver paths do. Candidate
publication likewise projects its exact returned extraction successor, whether
new or coalesced. Clearing the ordering timestamp is necessary: an older
pending row cannot otherwise supersede the imported newer failed row.

This adds one restore-only repository-table update inside an existing
transaction. Each affected writer adds at most one guarded point update inside
its existing transaction; there is no extra round trip, lock, history scan,
requeue, backfill, deletion, child, or schema/evidence change. The current
terminal oracle remains strict for a genuinely current failed successor.
Focused normal/race, live recovery, vet/docs, independent review, and the small
Phase-9 archive/restore rehearsal remain required before integration or any
new freeze. A full ceremony is not required to test this correction.

### Exact T40.13v Phase-9 focused pass

Exact implementation commit
`d6fe7d41fef76750cf6454baf0fd2161c4c82378` passed the focused store and live
restore regressions normally and under the race detector, the complete
`internal/recovery` package, repo-status projections, module verification,
vet, focused lint, documentation, glossary, format, and whitespace. Independent
review traced the restore call and every fresh/coalesced extraction writer and
reported critical, high, medium, and low findings all zero.

The exact-clean real-binary selector then observed restored extraction schedule
absence followed by a current active schedule and one benign 409, without
resurrecting the imported terminal projection. The
`structural-archive-restore` boundary passed in 90.55 seconds; the top-level
readiness test took 228.48 seconds and the package command completed in
229.179 seconds. Successful teardown removed the diagnostic workspace.

This supersedes neutral-42's specific Phase-9 restore-projection blocker. It
does not rerun earlier phases, exercise Phases 10–12, establish a full ceremony
or scale/SLO pass, or authorize merge, exact-main preflight, freeze, execution,
release, or Epic-40 closure.

## T40.13w neutral-42 identifier retirement

`t40r1-neutral-42` is consumed by its sealed Phase-9 stop. Its retained run
root currently prevents overwrite, but that directory may later be retired by
separately reviewed housekeeping and is not permanent non-reuse authority.
The launcher therefore advances its constant numeric review-stopped fence
through 42; neutral-43 is the first admissible fresh identifier.

This changes one comparison constant and one focused regression table. It adds
no ceremony phase, plan/evidence field, custody action, product work, child,
scan, lock, deadline, or memory/disk cost and removes no neutral-42 material.
After focused review and integration, repeat exact-main preflight, freeze
neutral-43, and stop for independent plan review before any execution.

## Neutral-43 Phase-11 stop and T40.13x

The exact-main `t40r1-neutral-43` ceremony passed preflight, cold, warm-noop,
delta, return, interruption, stale-worker, pressure, archive/restore, and
collection before the first structural authorized search returned HTTP 500.
Teardown completed without retained derived or scratch-source custody. The
committed `t4013x-neutral43-authorized-query-stop.json` binds the source, plan,
signed evidence, receipt, manifest, and package digests while retaining only
source-free facts; its digest is
`sha256:76c7515ffb926b945b4bec01c9e4913c765bcd5a90add407754d405b1369ebe4`.
It does not name a private server error or assert a root cause that the sealed
evidence cannot prove.

T40.13x permanently retires neutral-43 and tests the real handoff that isolated
selectors miss: archive/restore, the Phase-10 server restart and fresh
collection, then the first authorized search. The discriminator records the
publication-marker state, exact root/store revision equality, and a later
search outcome. Only after those facts identify the boundary may production
code change. Generic 500 retry, a longer product query timeout, and weakened
root/marker/content validation are not acceptable substitutes.

The focused reproduction identified a request/cache lifetime mismatch without
attributing that private cause to the sanitized ceremony receipt. Whole-reader
startup remains lazy. When a ten-second request expires while the same
cache-owned shared validation or exact-reader fill is still active, or when
the deadline races that task's same-generation successful completion, the
reader returns a typed warming sentinel that preserves
`context.DeadlineExceeded`.
REST maps only that state to fixed status 409 detail
`search generation is warming; retry`; service search does not enqueue repair.
Cancellation, completed validation failure, the ten-minute cache-task deadline,
publication marker, stale root, corrupt content, and query/backend deadlines
retain their terminal behavior.

V32 retains three authorized-query attempts. The first warming retry waits one
second; when the first search reported exact warming and the second attempt
remains retryable, the final attempt waits the reader's ten-minute cache ceiling
inside a twenty-minute query context. A generic 409 keeps the existing
one-second cadence unless it is the second retryable response after
first-attempt warming; status 500 remains a single attempt, and cancellation
during a wait is recorded as transport/deadline. V1–V31 keep their historical
classifier, context, retry schedule, and wait-cancellation evidence. No schema
or sealed field changes.

Focused normal/race tests cover shared and exact cold loads, task/request
deadline coincidence, cancellation, repair suppression, HTTP privacy, status
classification, and the exact retry schedule. The combined real-binary selector
crosses archive/restore, the actual Phase-10 restart and fresh collection,
records the first and later structural search outcomes, and verifies unchanged
store/source/root/receipt revisions and counts with no publication marker. The
corrected-tree selector recorded both the first and later structural searches
as single-attempt successes, emitted the combined boundary marker, and passed
in 184.94 seconds for the selected subtest and 238.435 seconds for the package.
Complete affected-package tests, focused race, vet, repository-pinned lint,
module verification, docs, glossary, shell, and whitespace passed. Two final
independent reviews reported critical/high/medium/low findings all zero. This
is focused evidence only, not a complete ceremony or scale/SLO pass.
