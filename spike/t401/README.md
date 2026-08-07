# T40.1 neutral scale envelope

This directory retains only source-free controls and receipts. The explicit
scratch repositories, index shards, process observations, and author binaries
were destroyed after validation.

| Artifact | SHA-256 | Authority |
| --- | --- | --- |
| `envelope.json` | `92cce848e6e42942c24e2fa066968571fb5693252b7b41b7a91c889881fe7f94` | Frozen structural/semantic profiles and independent oracles |
| `comparison.json` | `3527bec297c80c71b6c5081b1b386d25efc9ec8894643f599c7c57848be3b402` | Environment-bound same-SHA small-projection reader comparison |
| `reproducibility.json` | `b7b0491af659007eb8e903279ca63c6f8178878a8af114a9af0cd407e52ccb1a` | Two separate frozen author invocations per profile |
| `structural/manifest.json` | `4ae92b8efa58d459fe8fa10ba23c5cedad3adc7b2dddbd7618ea8d96c306604b` | Deterministic two-million-owner Git identity |
| `structural/receipt.json` | `bd80bef34f61f35c2f701d0877d4c013ec3c7d0ce62ec3756b32b7a4f103b2c2` | One source-free explicit authoring observation |
| `semantic/manifest.json` | `ca4925f3ca3ddad42955e5c3dc0e9b5610e7fa8ac4ce3e614a9ad091e23362a8` | Deterministic semantic Git identity |
| `semantic/receipt.json` | `e096b17faccd3ace38f0272234bc7fdfff97b0dfb1ccd23fa388e888d966d6d3` | One source-free explicit authoring observation |

Normal tests build only the retained projections. The two frozen repositories
are explicit external artifacts and require `-confirm-frozen`; the author also
refuses any output within the symlink-resolved module worktree.

```text
go run ./spike/t401/cmd/t401-envelope -output ./spike/t401/envelope.json
go run ./spike/t401/cmd/t401-author \
  -module-root "$PWD" -profile structural -confirm-frozen \
  -output /absolute/external/structural-first
go run ./spike/t401/cmd/t401-author \
  -module-root "$PWD" -profile structural -confirm-frozen \
  -output /absolute/external/structural-second
go run ./spike/t401/cmd/t401-author \
  -module-root "$PWD" -profile semantic -confirm-frozen \
  -output /absolute/external/semantic-first
go run ./spike/t401/cmd/t401-author \
  -module-root "$PWD" -profile semantic -confirm-frozen \
  -output /absolute/external/semantic-second
go run ./spike/t401/cmd/t401-author \
  -module-root "$PWD" -profile structural -scale projection \
  -output /absolute/external/projection
go run ./spike/t401/cmd/t401-reproducibility \
  -structural-first /absolute/external/structural-first \
  -structural-second /absolute/external/structural-second \
  -semantic-first /absolute/external/semantic-first \
  -semantic-second /absolute/external/semantic-second \
  -output ./spike/t401/reproducibility.json
go run ./spike/t401/cmd/t401-compare \
  -module-root "$PWD" -binary /absolute/pinned/zoekt-git-index \
  -repository /absolute/projection/repository.git \
  -work-dir /absolute/new/comparison \
  -output ./spike/t401/comparison.json
```

An interrupted author retains `<output>.building/author-state.json`; invoking
the same command resumes only after the profile digest and Git commit count
agree. The same-SHA comparison is intentionally limited to the small authored
structural projection. It requires semantic indexed-content and ordered
per-query returned-file/content projection equality while recording raw
shard-byte equality as an observation only.

Two separately invoked authored instances of each frozen profile produced
identical profile, oracle, manifest, commit, and tree identities. The strict
source-free `reproducibility.json` retains both build observations, including
the intentionally non-identity bare-Git logical and allocated bytes. The
structural shape contains 2,000,000 eligible Go paths plus two separately
reported controls, 512 reusable Go blobs, 1,000,000 synthetic caller
placements, and 9,216,000,000 declared Go placement bytes. Its current
source-partition and observation limits admit the modeled shape; no downstream
pipeline stage was run. The semantic shape contains 262,144 distinct Go blobs
and 32,768 IDL inputs. Its source-partition model is admitted, but current
observation publication is expected to refuse `generation_records` at
262,144 > 250,000; every downstream stage is therefore `not_run`, never a
successful zero.

The semantic templates are parity-checked at their first and last frozen
ordinals through the real source-observation adapters and Proto/Thrift
extractors. The frozen supported and explicit-gap families include resolved
RPC, literal Kafka, dynamic receiver/topic gaps, supported Proto declarations,
unresolved Proto declarations (`DECLARATION_NOT_FOUND`), supported Thrift
declarations, and a Thrift typedef cycle (`TYPEDEF_CHAIN_LIMIT`). A separate
production-extractor parity case proves that package-less Proto is supported
rather than classified as a gap.

The small reader comparison used the same verified zoekt binary for both
modes. Both modes indexed 4,098 files and produced equal indexed-content and
ordered per-query returned-file/content projection digests. Their
5,168,672-byte shard files had different raw digests. Missing objects completed
with an explicit gap in both modes; a corrupt object was refused by the go-git
default but completed with a gap under
the cat-file candidate. The 10 ms process-tree samples observed 87,506,944
bytes / 6 descriptors for go-git and 87,785,472 bytes / 9 descriptors for the
candidate. Those sampled values are neither peaks between samples nor giant-
profile cost evidence. The candidate remains unselected.

The repository lane models every eligible Go path. The caller lane is the
synthetic first-offset projection defined by the profile and deliberately does
not claim parity with production candidate selection. The reported 4,096-item
partitions are neutral modeled input chunks, not measured production
`sourcepartition` hash leaves.

Profile, oracle, manifest, commit, and tree identities are deterministic across
independent builds. Declared and logical source bytes belong to that manifest
identity. Bare-Git logical and filesystem-allocated bytes remain exact per-run
receipt observations: they are never summed with source bytes and allocated
bytes may differ across equivalent builds, filesystems, or toolchain versions.

The historical private diagnostic remains `unknown`. Successful corpus
authoring is not a successful phebs convergence run. These records establish no
target SLO, supported scale, accuracy/completeness, commit-cadence freshness,
topology, migration completion, decommission safety, pilot continuation, or
release. `GATE2-V2` remains `NOT_ESTABLISHED` and `DO_NOT_RELEASE` remains in
force.
