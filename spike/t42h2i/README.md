# T42.H2i Linux byte/allocation adapter preparation

This is adapter preparation on Darwin arm64. No Linux test binary, Linux
allocation, isolated pressure volume, restore run or ceremony was executed.
Ben deferred actual Linux native execution until October 20. The first sequence
stopped on the supported Linux arm64 compile error below; it is not a completed
gate or Linux acceptance result.

The separately authorized completion below passed only the remaining compile
and static/documentation gates. It establishes no Linux native result.

## Exact first attempt

The combined H2h/H2i source checkpoint was
`f39756eda109cd1942af649063e2ec8f97f132b5`. The unchanged original
[summary](first-attempt/summary.json) retains exact commands, source hashes,
tool hashes, elapsed times, process/group identities and joined terminals.
[The file census](first-attempt-files.json) binds all thirteen retained files,
357,384 bytes. Compiled binaries were hashed, never executed, then removed;
none is copied here.

The one sequential attempt used repository-pinned Go 1.26.5,
golangci-lint 2.12.2, `GOTOOLCHAIN=local` and `GOMAXPROCS=2`:

| Check | Result | Wall seconds including join |
| --- | --- | ---: |
| Complete `internal/custodybytes`, normal | PASS; 9 top-level / 37 tests and subtests, no skips | 0.667412 |
| Complete `internal/custodybytes`, race | PASS; same population, no skips | 1.736325 |
| Exact four-test T421 selector, normal | PASS; 4 top-level / 69 tests and subtests, no skips | 13.935651 |
| Exact four-test T421 selector, race | PASS; same population, no skips | 20.647886 |
| Two affected packages, vet | PASS | 1.860563 |
| Pinned lint, affected packages, `--new-from-rev=052a46b0` | PASS | 9.223186 |
| Linux amd64 `custodybytes` test-binary compilation | PASS; compile only | 1.193141 |
| Linux amd64 T421 test-binary compilation | PASS; compile only | 29.776378 |
| Linux arm64 `custodybytes` test-binary compilation | PASS; compile only | 4.206800 |
| Linux arm64 T421 test-binary compilation | FAIL; compile only | 30.474501 |
| Docs and glossary | NOT RUN after the first failure | — |

The exact T421 selector appears in the retained command, without widening to
the package, corpus, build graph, pressure or ceremony tests. Test timeouts were
60 seconds; command/compile outer caps were 120 seconds. Every owned process
and group joined cleanly. The full sequence stopped after 114.018154 seconds,
with unchanged source and no forced cleanup.

The [first failure log](first-attempt/compile-linux-arm64-spike-t421.log)
records H2h's `stat.Nlink` being `uint32` on Linux arm64 while
`linuxInputCopyOwnerMatches` takes `uint64`. Root corrected exactly that argument
to `uint64(stat.Nlink)` in `779e86dc`; the prepared continuation HEAD is
`0cbc0f48206660a88c7ee09e9315685169176af4`. No first-attempt receipt was
rewritten. Only the failed compile and remaining gates are eligible for the
separately authorized continuation; already-passed gates are not repeated.

## Authorized completion

The [unchanged completion summary](completion/summary.json), bound by
[its five-file census](completion-files.json), records source
`0cbc0f48206660a88c7ee09e9315685169176af4` and the exact same admitted Go/lint
images. No previously passed test, race, vet or compilation was repeated.

| Remaining check | Result | Wall seconds including join |
| --- | --- | ---: |
| Corrected Linux arm64 T421 test-binary compilation | PASS; compile only | 8.395196 |
| `make docs-check` | PASS | 1.206361 |
| `make verify-glossary` | PASS | 0.123900 |
| Pinned lint, both affected packages, Linux arm64 target, `--new-from-rev=052a46b0` | PASS; source analysis only | 23.775002 |

The Linux-targeted lint pass covers the new Linux-tagged H2h/H2i bodies and the
cast, which the earlier Darwin lint pass did not analyze. The four commands
each retained the 120-second outer cap. All four owned groups joined cleanly
without forced cleanup, source remained unchanged, and the complete
continuation took 33.682638 seconds. The compiled test binary was hashed,
never executed, then removed. Its output and the first compile failure remain
separate; neither receipt was replaced or relabeled.

## Cost and authority limits

The shared `custodybytes.Observer` keeps its existing single serial turn,
fifteen phase maxima, sticky refusal and completed-prefix behavior. Its native
walk reads one linked name at a time and metadata, with no file-body reads,
content hashing or child tools. Regular-file logical bytes and every linked
entry's `st_blocks * 512` are checked separately for overflow. Hard links count
per linked path; these are neither unique physical allocation nor an atomic
filesystem snapshot or a continuous peak.

The existing 4,096-byte path bound permits at most 2,048 directory cursors.
Linux retains at most one traversal descriptor per cursor plus the `os.Root`
anchor, excluding the borrowed owner descriptor. Darwin retains its existing
up-to-4,096 traversal descriptors plus anchor because libc duplicates cursors.
Directory buffering is additional and unmeasured. Exhaustion, metadata drift,
path replacement, filesystem mismatch, cancellation and observation failures
refuse; partial walks do not replace a completed phase maximum. No retry,
descriptor-limit increase or new goroutine is introduced.

Darwin's inode-generation, flags and union-filesystem checks are preserved.
Linux compares actual Linux stat fields and the held filesystem ID. Borrowed
identity observation grants no immutable-input protection, isolated mount,
permission, process ownership or launch authority. Input protection, inherited
workspace adoption and the volume owner remain separate prerequisites.

The Linux allocation seam uses one pre- and one post-`fstat`, at most one delta
`fallocate`, then one truncate and sync. Existing local 80-GiB/4,096-byte bounds
are not a Linux geometry or capacity admission. Cancellation is cooperative
between native calls, not a hard syscall deadline. Failure retains the actual
mutation prefix; it never retries, rolls back, closes the borrowed descriptor or
deletes custody. The post-check is an aggregate allocated-block floor, not
per-range hole proof. A future caller must retain zero-created allocation
lineage and independently admitted capacity/settlement evidence. Filesystem
metadata charges and backing capacity are not inferred from logical size.

Ordinary product queries, requests, sync ticks, startup/restart, retries/no-op,
publication, locks and caches gain no work: no Linux volume owner or caller is
activated. The eight-KiB Linux allocation test and shared Linux observer tests
are authored but unrun. Full isolated volume creation/removal, pressure
geometry, actual restore/cleanup acceptance, new Linux plan versions and
physical/effective-cgroup admission remain pending. Historical APFS plans and
the shared restore source/publication/join fences are unchanged.
