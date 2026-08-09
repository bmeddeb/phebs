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

The plan freezes 24 GiB memory and 120 GiB available-disk prerequisites, an
eight-hour total safety ceiling, 20 GiB peak RSS, 96 GiB maximum allocated data,
and the production five-attempt ceiling. These are stop/decision fences for one
neutral mechanics run, not a target SLO or supported-scale claim. They may not
be raised after any measurement begins.

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
review both out of band. The v2 plan also binds bounded public versions and
executable SHA-256 identities for the Go driver, Go compiler, Go linker, Git,
and supervised SurrealDB. Preparation verifies the same inventory before and
after custody authoring; execution verifies it at admission, before result
classification, and after teardown. Any drift destroys custody and leaves a
stopped result unclassified. `execute` requires the exact reviewed digest, the
same frozen signer, and the fixed approval phrase. Choose a new identifier for
every attempt; existing directories, plans, observations, receipts, custody,
and packages are never overwritten.

The `t40r1-neutral-01` freeze (plan
`sha256:eb8430b97a543182e89c07b117cb7105e13ee4592171aa0992c7989f8c31ab8b`)
was stopped during independent plan review before custody or execution because
its v1 plan did not bind these host executables. It is permanently retired and
must not be executed or reused.

```sh
cd ~/phebs

./spike/t4013/run-large-mac-ceremony.sh preflight

CEREMONY_ID=t40r1-neutral-02
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
signer material, and signature. The private signing key stays under the
ceremony root and is never packaged. The script creates
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
