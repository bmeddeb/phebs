# T30.1 — Focused-index and shard-set spike

> **Retained engineering record.** This spike supports an architecture
> decision. It is not production indexing behavior, a deployment SLA, or an
> accuracy/completeness claim. See
> [`docs/RETAINED_RECORDS.md`](../../docs/RETAINED_RECORDS.md).

**Date:** 2026-07-28 · **Decision:** GO for T30.2 · **Production changes:** none

T30.1 proves that one exact service scope can be indexed from immutable Git
objects with the pinned upstream zoekt builder while retaining canonical
repository paths and HEAD-plus-allowlisted revision commits. It also proves
that phebs can make a size-split shard generation visible only through an
exact, checksummed member manifest.

## Generated neutral corpus

The generator creates one bare repository directly from Git objects; it never
expands the bulk population into a worktree or retains generated source files
in this repository. Its `payments` and `shipping` roots, central protobuf
declaration, checked-in generated source, nested Go modules, and cross-service
caller are employer-neutral.

The retained run contains:

- 200,008 regular files, exceeding the current 200,000-file corpus limit;
- 21,000,305 aggregate path bytes, exceeding the current 16 MiB path limit;
- 588,006 bytes of compressed loose Git objects because every irrelevant path
  reuses one content blob;
- exact HEAD, branch, and tag commits, plus deterministic missing-scope,
  selected-symlink, and selected-gitlink commits.

The selected `payments` unit admits `services/payments/src` as its primary root
and three exact supporting inputs: `contracts/payment.proto`,
`services/payments/generated`, and `services/payments/go.mod`. The shipping
caller and every bulk blob remain outside the focused index.

## Pinned upstream surface

The spike compiles directly against
`github.com/sourcegraph/zoekt`
`v0.0.0-20260709064101-33f1f18af292` and uses only:

- `index.Options` and `index.NewBuilder`;
- `index.Builder.Add` and `index.Builder.Finish`;
- `index.Document`;
- `index.ReadMetadataPath`;
- `search.NewDirectorySearcher`;
- `query.Parse` and the ordinary zoekt search interface.

The upstream `index` package explicitly states that it is not a public API.
The module-version assertion and ordinary compilation are therefore required
gates for every change that touches this spike. T30.3 must keep a same-module
writer/reader pin and fail the build on surface or metadata drift.

## Identity and publication result

`analysis-unit-v1` canonicalizes the repository, operator unit name, sorted
primary paths, and sorted supporting paths. Changing commits preserves that
unit digest but changes the domain-separated index-generation digest over the
ordered revision set and builder policy.

The child process added seven revision-distinct documents totaling 528 bytes
and opened zero out-of-unit blobs. A deliberately small spike shard ceiling
produced three physical shards totaling 13,129 bytes. Every zoekt shard
re-read the canonical repository name, original branch/commit set, unit
digest, generation digest, builder policy, and corpus digest.

The phebs-owned visibility manifest orders every member by ordinal and binds
its regular-file SHA-256 plus a digest of re-read zoekt metadata. A per-member
sidecar carries ordinal/count and unit/generation identity. Validation compares
the exact on-disk `.zoekt` set before constructing a directory searcher.
Missing, extra, mixed, stale, interrupted, and trailing-JSON sets all refused.

Two isolated builds retained identical corpus, unit, and generation digests and
equivalent focused search results. Their shard/manifest digests intentionally
differ because the pinned builder embeds a build time and generated build ID;
T30.3 must treat those bytes as publication content, not semantic unit
identity.

## Revision and failure matrix

| Case | Retained result |
|---|---|
| HEAD + branch + tag, identical exact scope | all original commits and full repository-relative paths searchable |
| Selected path missing from one admitted revision | complete replacement refused before blob reads |
| Directory contents differ while the selected directory exists | each revision indexes its immutable descendants |
| Scope bytes change | unit and generation digests change |
| Only revision commit changes | unit digest stable; generation digest changes |
| Selected symlink or gitlink | refused as non-regular scope content |
| Git replacement ref | ignored by immutable blob reads |
| Missing promisor object | lazy fetch disabled; read refused |
| Child failure after document admission | no visibility manifest published |
| Missing, extra, mixed, or stale shard | searcher construction refused |
| Trailing JSON in the manifest | strict decoding refused |

## Frozen local gates and observation

The merge-bar test preregisters these local ceilings:

| Measurement | Gate | Retained observation |
|---|---:|---:|
| Corpus generation wall time | ≤ 30 s | 4.547 s |
| Focused child build wall time | ≤ 5 s | 0.228 s maximum |
| Search wall time | ≤ 250 ms | 1.394 ms |
| Child peak RSS | ≤ 512 MiB | 99,336,192 bytes |
| Out-of-unit blob reads | 0 | 0 |

The complete machine-readable observation is
[`results.json`](./results.json), SHA-256
`a4147f5e767c5f566a7b793ad1d87f908dd7d3cc2d0dc6b7f51e7b5e9c7faebe`.
Reproduce without overwriting it:

```sh
GOCACHE=/private/tmp/phebs-t301-go-cache \
T301_RESULTS_PATH=/private/tmp/t301-results.json \
go test ./spike/t301 -run '^TestT301FocusedIndexSpike$' \
  -count=1 -timeout=10m -v
```

Normal validation runs the same gates with:

```sh
go test ./spike/t301 -count=1 -timeout=10m
```

## Decision

**GO:** the exact pinned builder supports the selected focused-index design
without a projected commit or whole-repository blob reads. T30.2 may implement
strict configuration, canonical unit identity, and committed state.

This does not authorize T30.3 production indexing yet. T30.2 must first land
the configuration and state boundary, and T30.3 must port the proven child,
exact manifest validation, atomic visibility, revision matrix, cleanup, and
packaging behavior under the full merge bar. No runtime-use, completeness,
extraction-accuracy, migration-completion, or decommission-safety claim is
established.
