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

The 2026-07-30 Darwin/arm64 T30.6e policy refresh passed. Candidate manifest
v4 retains explicit focused-local domain projections, path-derived source
lanes, and the frozen caller partition while advancing the affected local
consumer generations. The planner runs took 3.61 s and 3.55 s and peaked at
61,554,688 and 61,456,384 bytes RSS. Each staged 12
files totaling 24,967 bytes. Twice the final caller content conservatively
bounds planner spool/split scratch at 4,386 bytes; the external-validation
scratch bound was 3,514 bytes. Adding the larger phase bound to the final stage
yields 29,353 bytes of conservative peak candidate disk. The complete census
contained 200,008 regular files; policy projection retained five repository
rows and six caller rows. The two-bit caller leaves were `00:1`, `10:3`, and
`11:2`, so no leaf needed a deeper split on this corpus.

The review-observed predecessor receipt measured 7.99 s/5.34 s, or 2.4×/1.6×
its 3.34 s/3.35 s baseline, with the slower run consuming 80% of the frozen
gate. Both the corrected-policy and current policy refreshes returned to the
prior wall-time range; flat RSS and zero added planner blob reads make host
variance the likely cause of the superseded spike rather than the
derived-field work.

The 16 MiB disk threshold remains this neutral fixture's prospective
measurement gate; it is not the production schema's aggregate projection
ceiling. Manifest v4 independently refuses more than 16,384 focused-local
projection artifacts or 4 GiB of their canonical content.

Strict validation retains `B_repository + C_caller + ΣP`, one stale local
replay remains `P_d`, and the policy refresh adds no planner source-blob read.
The repeat run reproduced the exact 12 filenames and all bytes. Its retained
identities are:

- source-corpus digest:
  `sha256:600ddbac4c51b8434a32bc7443747c8070a4738cc5f575e9580978af722b7bb2`;
- streamed regular-census digest:
  `sha256:157e95650c495956bf73b7f24bb668d062e667ff363aeda0aace58f29663602d`;
- policy digest:
  `sha256:73ad1537c150a93f6efaa96ada48c1ce477e5c68f0a1a6e86849455bcfd6a7ba`;
- generation digest:
  `sha256:65d50dac9bede00d8304e8c5e7a288a460062b4c42e59c7c519b0d36a54e0826`;
- manifest digest:
  `sha256:75fd91c2a3686d0193a035b49078f1c58674f9e23e8e4aabe8cc3dafeae880a1`;
- exact staged-output digest:
  `sha256:30ef974fc4f162100b5903526f53967511b7618ac11a3505a757c50d25db3bfd`.

To reproduce the retained observation:

```sh
T304_RESULTS_PATH="$PWD/spike/t304/results.json" \
  go test ./spike/t304 -run '^TestT304CandidatePlannerMeasurement$' -count=1 -v
go test ./spike/t304 -count=1
```

`results.json` is prospective engineering evidence for the selected local
bounds. It is not a public-corpus accuracy, completeness, migration, or
decommission-safety claim.
