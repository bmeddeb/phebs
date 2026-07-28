# Getting started

[← User guide](../MANUAL.md)

Install the required tools, build one local binary, and establish the first
administrator. Configuration beyond the minimal checked-in example belongs in
the [configuration guide](./CONFIGURATION.md).

## Prerequisites


| Requirement                        | Why                                                                  | Install                                                                                |
| ---------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `git`                              | clone/fetch mirrors, serve file content                              | usually present                                                                        |
| `surreal` (SurrealDB ≥ 3.0)        | the state/queue database child                                       | `brew install surrealdb/tap/surreal` or `curl -sSf https://install.surrealdb.com | sh` |
| Go ≥ 1.26                          | build from source                                                    | go.dev/dl                                                                              |
| Node ≥ 24                          | build the web UI                                                     | nodejs.org                                                                             |
| `universal-ctags` *(optional)*     | symbol search (`sym:`) at index time                                 | `brew install universal-ctags`                                                         |
| language SCIP indexer *(optional)* | precise definitions/references/hover; commit its `index.scip` output | [scip-code.org](https://scip-code.org/)                                                |
| `bubblewrap` *(Linux, optional)*    | network/filesystem namespace for the experimental Buf compatibility child | distribution package `bubblewrap`; macOS uses built-in `sandbox-exec`                 |

Release verification uses the exact tool versions recorded in
`.go-version`, `.node-version`, `.golangci-lint-version`, and
`.surrealdb-version`. Ordinary development supports the broader prerequisite
ranges above; the `make ci-*` targets fail early when the release toolchain
does not match.



## Build and run

```bash
git clone <your-clone-of-phebs> && cd phebs
make build          # builds the UI, zoekt and Buf children, and ./phebs
./phebs version     # 0.2.1-dev for an ordinary source build
./phebs serve -config phebs.yaml
```

`make build VERSION=vX.Y.Z` creates a release-identified binary. The same
exact value is printed by `phebs version`, returned by `/api/version`, written
to backup manifests, and included in startup logs. Release builds refuse a
non-SemVer `VERSION`.

To assemble and exercise the distributable directory with the exact pinned
release toolchain:

```bash
make release verify-release smoke-release VERSION=v0.2.1
```

The result is `dist/phebs-v0.2.1-<goos>-<goarch>/` containing `phebs`,
same-module `bin/zoekt-git-index` and `bin/buf` children, `LICENSE`,
`README.md`, the ready-to-run `phebs-otel-demo.yaml`, and
`release-manifest.json`. The canonical manifest binds the version, source
commit, target, Go toolchain, stable installed modes, sizes, and SHA-256
digest of every payload. `verify-release` rejects missing, additional,
symlinked, mode-changed, or byte-modified payloads. The manifest is an
integrity inventory, not a signature or independent proof of who built it.

`smoke-release` requires `git` and the exact `.surrealdb-version` binary on
`PATH`. It verifies the bundle before starting anything, creates an empty
temporary data directory and local Git fixture, then proves bootstrap login,
sync → index → search, and immutable source/folder browsing. It also removes
all development fixture variables and requires the authenticated capability
list and `/api/contract_atlas` route to retain the default-dark posture. The
temporary repository and data directory are deleted after shutdown.

For `v0.2.1`, the hosted `Release bundle and fresh-data smoke` job is part of
the required `ci` workflow. From a clean checkout it performs two independent
Linux/amd64 builds, compares their manifests, runs the empty-data smoke, then
retains a deterministic `.tar.gz` and adjacent `.sha256` file. The release
archive is not accepted from a local workspace.

This single-maintainer repository uses a documented release gate in place of
branch protection: an annotated release tag may be created only when its exact
`main` commit has a successful **push** run of all five named jobs in
`.github/workflows/ci.yml`, including the release job. The tag commit and run
SHA must match byte-for-byte; tags are never force-moved. Release notes must
link the run and checksum and state that Contract Atlas and proof features are
default-dark, provisional, and do not establish the closed
`NOT_ESTABLISHED` accuracy gate.

The published `v0.2.0` tag remains an immutable historical tag but is not a
verified release candidate: its exact push run failed the Linux fixture-bundle
HEAD portability checks and the Caller Map UI scale harness. `v0.2.1` carries
those corrections; the earlier tag is not deleted or moved.

The published
[`v0.1.0`](https://github.com/bmeddeb/phebs/releases/tag/v0.1.0) binary bundle
is Linux/amd64 only. Its archive SHA-256 is
`63103500a6b86aa3e4533fb1693065009585f6be509e48aab7b26373405daaf6`.
macOS users build the exact tag from source with the pinned Go and Node
versions; this first release does not provide a signed or notarized macOS
binary.
