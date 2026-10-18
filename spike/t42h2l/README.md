# T42.H2l source-preparation checks

Exact reviewed source: `3c9fcfc5a870ef2f3e12ae41a95bdfc189631ab8`.
This is the Ben-authorized Mac preparation handoff for Linux-track code.
The retained receipt and raw command outputs are byte-exact copies of the first
serial validation sequence. No failure, correction, or retry occurred.

The focused `^TestLinuxMemory` selector ran once normally and once with race
instrumentation on Darwin arm64 using pinned Go 1.26.5. It includes the existing
H2j arithmetic controls, explicit absent-counter hierarchy-root controls,
modeled filesystem collection/refusals/bounds/cancellation/cleanup, and actual
non-Linux public-observer refusal. Linux-specific native tests are excluded by
build tags on this host. They remain authored and unrun; an opt-in or a skipped
native test would not qualify them. Ben defers actual Linux execution until
October 20, 2026, and no Linux binary or probe ran in these checks.

All commands passed: focused normal and race, affected-package vet, pinned
2.12.2 lint against the new files since `ef6b2551`, Linux amd64 and arm64 test
binary compilation, docs-check, and glossary verification. The receipt records
exact argv, explicit offline-toolchain overrides, individual walls and statuses,
source hashes before/after, stream hashes, and owned child-group joins/absence.
The two Linux images were hashed, never executed, and removed. All nine command
groups joined and were observed absent. Total command-sequence wall was
76.051s. CPU usage and aggregate peak memory were not measured.

The observer itself has no active launcher or plan caller. It creates no child,
changes no namespace/cgroup/mount, grants no capability, writes no input, and
selects no budget. It locks one OS thread while collecting fixed kernel roots.
Two serial samples check stable caller translation, lifetime, namespace,
mount/membership, held directory identities, physical total and limits. They
retain minimum available memory and maximum per-node current use. Those are
sequential observations, not an atomic snapshot, continuous bound, enforcement,
resource reservation, or authority to run a ceremony.

Source bounds are 64 ancestor nodes, 4096 membership-path bytes, 64-KiB meminfo,
8-KiB membership, 256-KiB/4096-record mountinfo, and 128-byte controls. At most
64 first-pass directory descriptors plus two second-pass descriptors are held;
the complete native source and temporary namespace checks bound the observer
at 74 descriptors. Each non-root ancestor has eight type/counter reads over
two passes; other reads are fixed and bounded. The five-second context is a
cooperative observation deadline, not a hard deadline for a kernel syscall.
No per-query/sync/retry/publication work, cache, global mutex, or full process,
corpus, source, or shard census is added.

The explicit `HierarchyRoot` form records absent root counters. It never
invents `memory.max=max` or a measured zero current value. Actual Linux
qualification still must establish accessible authentic proc/cgroup roots,
complete memory-enabled domain ancestry and required kernel resolution APIs.
New Linux plan/profile/admission wiring, complete reservation coverage
(including non-RSS custody/tmpfs payloads), namespace/input and isolated pressure
ownership, and target readiness remain separate. Historical signed bytes,
active Linux refusal, old versions and limits, and production signing remain
unchanged.
