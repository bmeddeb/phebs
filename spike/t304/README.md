# T30.4 candidate-planner prospective measurement

This retained harness freezes the production candidate planner’s initial
caller depth, artifact bounds, and local resource gates. It generates the
deterministic T30.1 neutral bare repository (200,008 regular files), constructs
the production extractor registry with every independent experimental gate
enabled, derives the shared `extract.CandidatePolicies`, and invokes
`candidate.Build` in an isolated child process.

The measured plan must stay within:

- planner wall time: 10 seconds;
- child peak RSS: 256 MiB;
- peak candidate disk, including the staged manifest/NDJSON publication plus
  the higher of bounded planner spool/split scratch and bounded validation
  scratch: 16 MiB.

The production partition contract frozen by this measurement starts at two
SHA-256 prefix bits, permits at most 4,096 records or 64 MiB of declared blob
bytes per artifact, and splits an overflowing caller leaf by the next hash
bit. The result reports the complete streamed census, repository and caller
candidate-row counts, caller-leaf distribution, and the corpus, policy,
generation, manifest, and canonical staged-output digests.

The harness performs a second build into an independent directory and compares
every staged filename and byte. Candidate publications intentionally contain
no build timestamp, so repeat planning of identical commit, unit, and policy
inputs must preserve both semantic identities and exact publication bytes.

## Retained observation

The 2026-07-30 Darwin/arm64 T30.6d identity refresh passed. Candidate manifest
v4 retains explicit focused-local domain projections and the frozen caller
partition while adding path-derived source lanes. The planner runs took 3.61 s
and 3.62 s and peaked at 61,112,320 and 61,947,904 bytes RSS. Each staged 12
files totaling 24,967 bytes. Twice the final caller content conservatively
bounds planner spool/split scratch at 4,386 bytes; the external-validation
scratch bound was 3,514 bytes. Adding the larger phase bound to the final stage
yields 29,353 bytes of conservative peak candidate disk. The complete census
contained 200,008 regular files; policy projection retained five repository
rows and six caller rows. The two-bit caller leaves were `00:1`, `10:3`, and
`11:2`, so no leaf needed a deeper split on this corpus.

The review-observed predecessor receipt measured 7.99 s/5.34 s, or 2.4×/1.6×
its 3.34 s/3.35 s baseline, with the slower run consuming 80% of the frozen
gate. This corrected-policy rerun returned to the prior wall-time range; flat
RSS and zero added blob reads make host variance the likely cause of the
superseded spike rather than the derived-field work.

The 16 MiB disk threshold remains this neutral fixture's prospective
measurement gate; it is not the production schema's aggregate projection
ceiling. Manifest v4 independently refuses more than 16,384 focused-local
projection artifacts or 4 GiB of their canonical content.

Strict validation retains `B_repository + C_caller + ΣP`, one stale local
replay remains `P_d`, and lane classification adds no source-blob read: it is
derived from the canonical path already present in the streamed tree census.
The repeat run reproduced the exact 12 filenames and all bytes. Its retained
identities are:

- source-corpus digest:
  `sha256:600ddbac4c51b8434a32bc7443747c8070a4738cc5f575e9580978af722b7bb2`;
- streamed regular-census digest:
  `sha256:157e95650c495956bf73b7f24bb668d062e667ff363aeda0aace58f29663602d`;
- policy digest:
  `sha256:b2d6df428a480f48a6258266576a2acdd95c29d59aef1fc2ca18fa552f3f90bb`;
- generation digest:
  `sha256:6a0b94c42ca967fe8f2bca8763f77b287a3d1d37974a9432971a3fdd076f0be9`;
- manifest digest:
  `sha256:b6b5ad4e18b8c34df6050acc92c5af781cca50aff4498871f3d76cf20ccadc67`;
- exact staged-output digest:
  `sha256:6ff6f0622128512aafc09bbcc57336242a2c86ab1f08fa0d24eb2b2cb339067b`.

To reproduce the retained observation:

```sh
T304_RESULTS_PATH="$PWD/spike/t304/results.json" \
  go test ./spike/t304 -run '^TestT304CandidatePlannerMeasurement$' -count=1 -v
go test ./spike/t304 -count=1
```

`results.json` is prospective engineering evidence for the selected local
bounds. It is not a public-corpus accuracy, completeness, migration, or
decommission-safety claim.
