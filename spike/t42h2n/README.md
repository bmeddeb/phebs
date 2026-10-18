# T42.H2n bounded source-check outcome

Mac preparation handoff, 2026-10-18. Source checkpoint:
`96169bdef354fe0dbbbd20ee31c4425cbcc1bac3`.
The production dispatch contract still has its existing sixteen sites and roles.
This change reconciles the closed source inventory with existing wrappers and
separately refuses a non-nil managed SCIP installation in private epoch config.
It adds no installed writer, launcher, active plan, host admission, or capacity claim.

The retained `receipt.json` and ten adjacent stdout/stderr files are exact first
outputs from one serial sequence on Darwin/arm64 using Go 1.26.5 and repository-
pinned golangci-lint 2.12.2. The focused race selector passed (package 2.452s;
outer wall 29.062s), affected-package vet passed (1.736s), changed-file lint
reported zero issues (5.831s), docs-check passed (1.023s), and glossary verification
passed (0.475s). Total outer wall was 38.131s. All five launched groups were
joined and observed absent; the three source hashes were unchanged. Environment
inherited the host environment with the recorded offline tool overrides and the
pinned Go directory first on PATH; it was not a closed execution environment.
CPU and aggregate RSS were not measured. No compile-only command was required or
run in this sequence; the receipt's empty artifact object records that absence.

Earlier exact raw outputs are preserved without replacement under `preparation/`:

- `darwin-base.log`: the inherited closed-inventory failure, including missing
  controlled wrapper calls and stale complete-boundary expectations.
- `updated.log`: the corrected complete inventory passed for Darwin/arm64 and
  Linux/amd64 source/type analysis (19.30s and 28.36s), and the unlisted/alias
  negative fixture passed (0.42s); package wall was 48.909s. Those source analyses
  were not repeated. The analyzed production `internal/`, `cmd/` and `go.mod`
  Git objects are unchanged between parent and this source checkpoint:
  `f57f3511fcbfe4c6c98044728beab4fb5bb88ecd`,
  `8c0495fc904d274e9a5a680697b51a80708fb881`, and
  `0cf290c2c3604620d8da8ea8294c0647c14e088b`, respectively.
- `managed-valid-base.log`: the ordinary parser accepted the valid raw YAML
  managed installation, and the pre-guard epoch regression failed because it
  admitted that unaccounted installation (package 1.159s).
- `managed-base.log`: unqualified earlier preparation only. A struct-marshal
  mutation passed because unrelated YAML round-trip refusal rejected it; this
  output is not regression proof for the managed-installation guard.
- `managed-corrected.log`: the valid raw-YAML managed regression and existing
  semantic-drift table passed after the nil guard (package 0.829s). This normal
  regression was carried forward rather than repeated. The final focused race
  selector independently covers those same controls and the alias negative.

The inventory uses typed source references and exact closed counts; the three
managed Bazel preparation boundaries remain separate from the sixteen runtime
roles. The config guard adds one pointer comparison to an existing bounded
private parse, with no new I/O, locks, cache, children, or steady-state work.
The two complete source/type scans are test-only work already retained above.
The source inventory test bytes at the final checkpoint match the tested bytes;
only the separately tested private epoch parser changed subsequently.

No Linux binary, kernel probe, namespace, or native workload was executed.
Linux/amd64 inventory evidence is source/type analysis only. Actual Linux native
qualification remains deferred until the office run on October 20. These
checks establish neither a new Linux plan nor a capacity or rehearsal PASS.
