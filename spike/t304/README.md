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

The 2026-07-28 Darwin/arm64 run passed. The planner runs took 3.55 s and 3.43 s
and peaked at 61,145,088 and 60,801,024 bytes RSS. Each staged five files
totaling 13,049 bytes. Twice the final caller content conservatively bounds
planner spool/split scratch at 4,134 bytes; the external-validation scratch
bound was 3,514 bytes. Adding the larger phase bound to the final stage yields
17,183 bytes of conservative peak candidate disk. The complete census
contained 200,008 regular files; policy projection retained five repository
rows and six caller rows. The two-bit caller leaves were `00:1`, `10:3`, and
`11:2`, so no leaf needed a deeper split on this corpus.

The repeat run reproduced the exact five filenames and all bytes. Its retained
identities are:

- source-corpus digest:
  `sha256:600ddbac4c51b8434a32bc7443747c8070a4738cc5f575e9580978af722b7bb2`;
- streamed regular-census digest:
  `sha256:5e183c73730e5233c96568e69ca3bfb4abc2e8c1eed15e183a296163cb69e841`;
- policy digest:
  `sha256:8e29086fefcac6741e39ae66df82bf31b8983bbec9077250aca3d45a1033ff05`;
- generation digest:
  `sha256:726362f9fa59fcb5e18c9dd0d7e83b4f5970658ec0699c12b43d003bdbacd871`;
- manifest digest:
  `sha256:a52a02522c918e1bcf6878842207f0cf3e7816dcf74bb235f68e1f0c05db6826`;
- exact staged-output digest:
  `sha256:1f37b8d8b9306354328d18fddf48130bf1fb457859ecb6de8b1375a8b79e8403`.

To reproduce the retained observation:

```sh
T304_RESULTS_PATH="$PWD/spike/t304/results.json" \
  go test ./spike/t304 -run '^TestT304CandidatePlannerMeasurement$' -count=1 -v
go test ./spike/t304 -count=1
```

`results.json` is prospective engineering evidence for the selected local
bounds. It is not a public-corpus accuracy, completeness, migration, or
decommission-safety claim.
