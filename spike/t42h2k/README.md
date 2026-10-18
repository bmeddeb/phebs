# T42.H2k source-preparation checks

The first serial check of exact source
`94a49c6e972fbc7d96b899c0c850121a386eaa68` passed on the Mac. The
[receipt](first-gates/receipt.json) retains every argv, exit status, outer wall
time, process join/group-absence result, tool hash, stdout/stderr hash, and the
eleven owned Go-source hashes before and after. Those source hashes are equal.
There was no first failure or retry. All fourteen command groups joined cleanly;
the four hashed cross-compile outputs and their scratch directory were removed.

The existing pinned Go 1.26.5 and golangci-lint 2.12.2 were used with
`GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOMAXPROCS=2`, and the
pinned Go bin directory first in `PATH`. Ordinary checks used the native Darwin
target; Linux static/compile checks explicitly used `CGO_ENABLED=0`. No package,
toolchain, image or dependency was downloaded.

| Check | Result | Outer wall seconds |
| --- | --- | ---: |
| Six portable `^TestLinuxInputTree` selectors, normal | pass; package 0.792s | 24.481 |
| Same selectors, race | pass; package 2.153s | 46.436 |
| Affected package and helper command vet, Darwin | pass | 14.040 |
| Same vet, Linux amd64 with native tag | pass | 45.403 |
| Scoped pinned lint, Darwin | 0 issues | 11.353 |
| Scoped pinned lint, Linux amd64 with native tag | 0 issues | 41.124 |
| Tagged Linux amd64 test compile only | pass | 9.889 |
| Linux amd64 helper cross-compile | pass | 1.541 |
| Tagged Linux arm64 test compile only | pass | 38.278 |
| Linux arm64 helper cross-compile | pass | 14.932 |
| Documentation links/map/sealed historical tree | pass | 2.510 |
| Glossary verification | pass | 1.506 |

Total wall time, including version checks, compilation, source hashes and joins,
was 251.969 seconds. Package test time is not isolated extractor CPU or runtime
performance; no RSS, throughput, physical-capacity or scale result was measured.
Lint was scoped to this change from `ef6b2551` in `spike/t421` and the new helper
command. This was not the broad historical T421 test suite or a reference-helper
rebuild/admission.

The eight default Linux controls were compiled and statically checked but were
not executed. Both required helper tests and their fixtures are isolated behind
`linux && phebs_t42_native`; all Linux test compilation used
`-tags phebs_t42_native`, so these authored tests were checked too. An explicit
future native invocation must supply `PHEBS_T42_INPUT_TREE_REQUEST` for the exact
independently rebuildable helper. Missing helper/kernel/LSM facts fail rather
than skip or qualify. No Linux binary, test, probe or namespace was executed.

This is a source-preparation result. Actual read-only mount/descriptor-transfer,
mutation refusal and joined-use acceptance remain pending Ben's October 20
Linux session. Historical constructors, source/tool/plan admission, entitlement
policy and production signing are unchanged. The owning contract and cost
boundaries remain in PLAN, BACKLOG and the operations guide; this record grants
no current protected-tree or ceremony admission.
