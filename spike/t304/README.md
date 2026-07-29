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

The 2026-07-29 Darwin/arm64 post-T30.5 repair refresh passed. Candidate
manifest v3 adds explicit focused-local domain projections without changing
the frozen caller partition. The planner runs took 3.80 s and 3.62 s and
peaked at 60,604,416 and 61,652,992 bytes RSS. Each staged 12 files totaling
24,288 bytes. Twice the final caller content conservatively bounds planner
spool/split scratch at 4,134 bytes; the external-validation scratch bound was
3,514 bytes. Adding the larger phase bound to the final stage yields 28,422
bytes of conservative peak candidate disk. The complete census contained
200,008 regular files; policy projection retained five repository rows and six
caller rows. The two-bit caller leaves were `00:1`, `10:3`, and `11:2`, so no
leaf needed a deeper split on this corpus.

The 16 MiB disk threshold remains this neutral fixture's prospective
measurement gate; it is not the production schema's aggregate projection
ceiling. Manifest v3 independently refuses more than 16,384 focused-local
projection artifacts or 4 GiB of their canonical content.

The repeat run reproduced the exact 12 filenames and all bytes. Its retained
identities are:

- source-corpus digest:
  `sha256:600ddbac4c51b8434a32bc7443747c8070a4738cc5f575e9580978af722b7bb2`;
- streamed regular-census digest:
  `sha256:5e183c73730e5233c96568e69ca3bfb4abc2e8c1eed15e183a296163cb69e841`;
- policy digest:
  `sha256:ed5e5d784dfeca1f52bd1a1f77a373907dcb21470799350a9e93b6999dc91d3d`;
- generation digest:
  `sha256:667fd53e187f433ac3ecdd024e4dd8fe6a48ab8c47f1f15a6842676d65b52641`;
- manifest digest:
  `sha256:8a4ece5d2e1a4684b7ce937717db78c2780221f2ff2d4477067240236a666022`;
- exact staged-output digest:
  `sha256:3dbf0db9649f1f51745a0a9d8eca92f7215d38f8540c294570ed44e799190b21`.

To reproduce the retained observation:

```sh
T304_RESULTS_PATH="$PWD/spike/t304/results.json" \
  go test ./spike/t304 -run '^TestT304CandidatePlannerMeasurement$' -count=1 -v
go test ./spike/t304 -count=1
```

`results.json` is prospective engineering evidence for the selected local
bounds. It is not a public-corpus accuracy, completeness, migration, or
decommission-safety claim.
