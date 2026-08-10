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
the distinct `server_exited_during_convergence` stopped result. Each retained
transition binds wall time, closed stage, failure class, and progress digest;
the wait separately binds the digest and wall time of its last successfully
completed inspection (which does not itself claim convergence). Classes are
closed (`pending`, `transport`, `status`, `response`, `control`, `complete`).
The first 32 transitions fit the evidence envelope; a 33rd immediately stops
unclassified as `convergence_transition_limit_exceeded` rather than truncating
the timeline. Raw errors, process output, repository identities, paths,
responses, credentials, and source remain in destroyed custody. The two
unclassified terminal identities remain named through a simultaneous meter
failure; the eight-hour parent still wins first, while missing measurement
still governs every other failure. The added work is constant-size local
accounting over API/control reads the oracle already performs; production
behavior is unchanged.

```sh
cd ~/phebs

./spike/t4013/run-large-mac-ceremony.sh preflight

CEREMONY_ID=t40r1-neutral-09
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
