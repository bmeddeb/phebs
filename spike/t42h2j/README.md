# T42.H2j modeled Linux memory contract checks

This is Mac preparation for the Linux track, authorized by Ben's all-ticket
handoff. The pure calculator uses supplied physical/available bytes, cgroup
ancestry, and a source-supplied scope/concurrency census. It does not observe or
authenticate those facts, enforce memory limits, admit a native host, issue an
execution permission, or establish a capacity PASS. Actual Linux runs are
deferred until October 20, 2026.

The first exact source, `6be48fee10625f63fbb8caaae3603b40e4a676fc`, passed the
focused normal selector, focused race selector, and affected-package vet, then
stopped at pinned golangci-lint 2.12.2 on three ST1005 capitalization findings.
`first-gates/` preserves every first receipt and command log unchanged. Its
normal/race package times were 0.575s/1.894s; outer wall times were
4.493s/11.208s. Vet took 1.022s. The first sequence took 20.859s.

Checkpoint `e6d757340dbd8767b8bb655314e8b21ff38163f6` changes only the initial
`Linux` to `linux` in seven static error strings. No condition, arithmetic,
return value, test, or historical artifact changed. The earlier race and vet
results carry over only on that source-equivalence basis; neither was rerun.
`remaining-gates/` retains the corrected normal selector (0.691s package,
4.308s outer), pinned lint (zero issues, 2.443s), previously unrun Linux amd64
and arm64 test-binary compilations (6.913s/7.169s), docs-check (0.990s), and
glossary verification (0.127s). The remaining sequence took 21.954s. Both Linux
binaries were compile-only, never executed, and removed after their bytes and
SHA-256 were recorded. All eleven launched validation process groups were
joined and observed absent; no test binary is retained here.

Tests cover synthetic arithmetic and refusal cases, including the historical
H1 physical-size scalar. Their available-memory values and reservations are
modeled, not measurements of the Linux target. The calculation requires the
full reservation plus explicit headroom to fit observed slack without credit
for already occupied owned memory. At most 64 ancestor rows and two matching
32-row scope vectors are scanned; identities and scope names are bounded
before map lookup. Evaluation retains no caller input, performs no I/O, holds
no lock, and adds no production request, sync, retry, publication, or startup
work because no active execution path calls it.

Native physical/effective-cgroup observation and ancestry stability, complete
nonoverlapping scope coverage (including non-RSS custody payloads), actual
namespace/mount capability and input protection, isolated pressure/restore,
new Linux plan versions, and target readiness remain separate dependencies.
The old signed versions, 120-GiB backing-disk prerequisite, non-memory limits,
and all historical receipt bytes remain unchanged.
