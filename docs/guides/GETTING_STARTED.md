# Getting started

[← User guide](../MANUAL.md)

Install the required tools, build one local binary, and establish the first
administrator. Configuration beyond the minimal checked-in example belongs in
the [configuration guide](./CONFIGURATION.md).

## Prerequisites


| Requirement                        | Why                                                                  | Install                                                                                |
| ---------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `git`                              | clone/fetch mirrors, serve file content                              | usually present                                                                        |
| `surreal` (stable SurrealDB 3.x)   | the state/queue database child                                       | `brew install surrealdb/tap/surreal` or `curl -sSf https://install.surrealdb.com | sh` |
| Go ≥ 1.26                          | build from source                                                    | go.dev/dl                                                                              |
| Node ≥ 24                          | build the web UI                                                     | nodejs.org                                                                             |
| `universal-ctags` *(optional)*     | symbol search (`sym:`) at index time                                 | `brew install universal-ctags`                                                         |
| language SCIP indexer *(optional)* | precise definitions/references/hover; commit its `index.scip` output | [scip-code.org](https://scip-code.org/)                                                |
| `bubblewrap` *(Linux, optional)*    | network/filesystem namespace for the experimental Buf compatibility child | distribution package `bubblewrap`; macOS uses built-in `sandbox-exec`                 |

Release verification uses the exact tool versions recorded in
`.go-version`, `.node-version`, `.golangci-lint-version`, and
`.surrealdb-version`. Ordinary development supports the broader prerequisite
ranges above, including package-manager builds that append SemVer build
metadata such as `3.2.3+20260721.40522d1`; the runtime still records the exact
reported SurrealDB version string and executable SHA-256 for backup/restore
identity. The `make ci-*` targets fail early when the release toolchain does
not match.

Experimental evidence features are opt-in under `experimental:`. Enabling
`provisional_workbench` exposes the Change Workbench over the same
store-derived Contract Atlas evidence as the instance and therefore also
requires either `provisional_proto_extraction` or
`provisional_thrift_extraction`.



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
