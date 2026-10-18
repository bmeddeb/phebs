# T42.H2m owned Linux pressure filesystem preparation

This is source and adapter validation on Darwin arm64. No Linux binary,
filesystem setup, allocation, pressure, restore or ceremony was executed.
Ben deferred Linux native work until October 20. The explicit
`linux && phebs_t42_native` mechanism test remains authored and unrun; it
fails missing host prerequisites under that tag rather than granting a pass.

## First attempt

Source checkpoint `e8ccd1babd21043a6c87ec996f19375d9a4291e1` was independently
reviewed before execution. The unchanged [summary](first-attempt/summary.json)
binds exact source/tool hashes, commands, process/group identities, wall times
and actual joined terminals. The [eight-file census](first-attempt-files.json)
binds its original logs and runner. The sequence stopped at its first failure;
it is not a completed gate or a Linux native result.

The serial runner used pinned Go 1.26.5 and golangci-lint 2.12.2,
`GOTOOLCHAIN=local`, `GOMAXPROCS=2`, 60-second test timeouts and 120-second
outer command limits. Every owned process/group joined cleanly, with no forced
cleanup. Source remained unchanged. Total wall time including joins was
122.223156 seconds.

| Check | Result | Wall seconds including join |
| --- | --- | ---: |
| `^TestLinuxPressure`, normal Darwin models | PASS; 7 top-level / 64 including subtests, no skips | 19.128856 |
| Same selector, race | PASS; same population, no skips | 61.337119 |
| Affected T421 package, Darwin vet | PASS | 6.840774 |
| Affected T421 package, Linux amd64 tagged vet | FAIL; source analysis only | 34.603962 |
| Darwin/Linux lint, tagged Linux amd64/arm64 compile, docs, glossary | NOT RUN after the first failure | — |

The [first failure](first-attempt/linux-amd64-tagged-vet.log) is a
Linux-tagged identifier collision: the fixed formatter path constant and its
path-metadata type both use `linuxPressureFormatterPath`. It changes no native
outcome because none ran. Root changed only the private metadata type and its
four references to `linuxPressureFormatterComponent` in reviewed correction
`aaae2797ab3b0460ba19a1a9782e61251feab6e6`. The formatter path constant and
recipe bytes did not change. Already-passed portable tests and Darwin vet
were carried forward because the correction changes only Linux-tagged source.
No first-attempt receipt or outcome was replaced.

## Authorized remaining-only completion

The unchanged [completion summary](completion/summary.json) binds the
correction checkpoint, exact tool images and the first failed summary. Its
[nine-file census](completion-files.json) binds logs and the new runner.
Only the failed Linux vet and previously unrun gates were executed.

| Remaining check | Result | Wall seconds including join |
| --- | --- | ---: |
| Affected T421 package, Linux amd64 tagged vet | PASS; source analysis only | 2.708848 |
| Pinned lint, Darwin target, `--new-from-rev=ef6b2551` | PASS | 13.109030 |
| Same lint scope, Linux amd64 and `phebs_t42_native` tag | PASS; source analysis only | 16.084562 |
| Linux amd64 tagged test-binary compilation | PASS; compile only | 5.998955 |
| Linux arm64 tagged test-binary compilation | PASS; compile only | 37.580293 |
| `make docs-check` | PASS | 2.442255 |
| `make verify-glossary` | PASS | 0.773109 |

The completion took 79.061879 seconds including joins. All seven owned process
groups joined cleanly without forced cleanup; source remained unchanged.
Previously admitted Go and lint hashes matched the first attempt. The same
120-second outer command caps applied. Both tagged Linux test binaries were
hashed and removed without execution; the tag checks authored required native
source at compile time only. No native Linux setup, allocation, filesystem,
pressure, restore or host-admission result is established by these gates.

## Combined-source integration

Root merged the separately reviewed namespace-input owner into checkpoint
`fa05d0dcf3aa8d355ea74300d335e8a7e7428ab7`. The eight pressure-owner files
remained unchanged. The [integration summary](integration/summary.json) and
[five-file census](integration-files.json) retain only the three authorized
combined-tree checks; no portable test, race, vet, lint or arm64 compile was
repeated.

| Combined-tree check | Result | Wall seconds including join |
| --- | --- | ---: |
| Linux amd64 tagged test-binary compilation | PASS; compile only | 29.950515 |
| `make docs-check` | PASS | 2.849911 |
| `make verify-glossary` | PASS | 0.843974 |

All three owned groups joined cleanly without forced cleanup, source stayed
unchanged and total elapsed time including joins was 34.062782 seconds. The
combined tagged binary was hashed and removed without execution. This verifies
source coexistence at compile time; no native Linux custody or pressure result
is claimed. The first failure and its separately authorized completion retain
their original bytes and outcomes above.

## Cost and authority limits

No ordinary query, request, sync tick, startup/restart, retry/no-op or
publication caller is enabled. Historical Darwin/APFS geometry, plans,
refusals and the production 48-GiB per-unit lifecycle admission API stay exact.

The explicit constructor owns one new 64-MiB through 96-GiB image with full
new-file `fallocate` lineage. Its initial backing floor remains 120 GiB. Full
image plus two rounded eight-KiB records and sixteen filesystem allocation
units is projected separately against the existing normal watermark. The
production gate's zero estimate is explicitly an observation of its persistent
capacity latch, not a zero-byte admission/reservation of the image. Usable
ext4 capacity, filesystem/device and mount identities are actual observations;
nominal image size supplies no historical APFS geometry or pressure margins.

Construction hashes one supplied-digest-bound fixed formatter image, at most
32 MiB using a 32-KiB buffer, and checks a bounded root-owned fixed symlink
chain. It runs one formatter child with a 60-second operation timeout, one
creating OS thread locked through its sole native wait, and actual private
session checks. Output is counted and discarded with a 64-KiB ceiling. Forced
cleanup remains failure and may consume the existing six-second join tail.
No `losetup`/`mount` tool child, source scan or repeated image hash is added.
The direct formatter-image binding is not loader/library/helper closure.

The owner holds at most nine descriptors; a serialized temporary control,
journal, mount-table, inventory or loan descriptor adds one, apart from
ordinary formatter pipes and inherited child duplicates. Each mount-table
observation reads at most one MiB plus a refusal sentinel. `Geometry` reads
two tables; a successful loan checks twice and reads four, plus bounded journal
and native metadata. Thirteen durable journal phases use at most eight KiB
per record, atomic replacement and file/directory syncs. There is no recursive
filesystem walk. One mutex serializes all methods and holds through the whole
trusted callback. Context checks are cooperative between native calls; a
blocking kernel syscall or arbitrary callback has no invented hard deadline.

The backing mount must already be private, as it could be in a future admitted
privileged launcher's owned namespace. The owner observes this before
allocation and mounting; it never changes host propagation to obtain it.
Initial-user-namespace privilege remains external admission: EUID0 and current
`CAP_SYS_ADMIN` are necessary checks only. Normal `root:disk` device access and
other privileged-host aliases are trusted; the owner grants no entitlement,
changes no node ownership/mode and launches no unprivileged executor. A future
executor binder must independently exclude block-admin groups, capabilities
and device handles.

The opaque descriptor callback has a trusted joined-use obligation and returns
no process-drain certificate. Actual nonforced unmount, exact loop-detached
status and revealed underlay checks precede backing-image removal. Failure
retains the actual prefix and cleanup is one attempt, with no lazy/forced
unmount or recursive deletion. The formatter session is actual in-memory
owner custody, not a durable general process supervisor. Full executor
hard-death census, restore integration, exact native tool/geometry admission,
effective memory admission and new Linux plan versions remain pending.
