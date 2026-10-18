# T51.1 finite protobuf field-reference check

The frozen input is a real public consumer from grpc-go v1.80.0 plus a
separate authored control. `preregistration.json` records the supported
committed input, exclusions, source labels, exact lineage bridge, denominators,
thresholds and stop rules. The lead reviews that record before the first
candidate measurement. `source-labels.jsonl` was derived from the source-only
Go AST census before SCIP preparation; `declarations.json` uses the independent
T49.1 lexer over the source proto files. No candidate output supplies labels.

`input-manifest.json` distinguishes original upstream files from evaluator
outputs and records the complete working-module preparation context. The
upstream commit pins the public source only. `committed-inputs.json` and the
two retained Git bundles bind the actual local evaluation commits containing
source and produced SCIP together. Evaluation repository labels keep those
commits distinct from upstream history. All indexed documents are retained.
The public files retain their copyright headers and Apache-2.0 license.

The lead accepted the corrected frozen authority and source labels at checkpoint
`fe00906d69a46c80294ce7a9b9f3198bc98b4bdc`. The first measurement passed:
14/14 public positives with zero emissions at 56 negatives; 17/17 neutral
positives with zero emissions at seven negatives and 12 unsupported sites.
All 19 descriptor/source/declaration field identities agree. Eleven public
and seven neutral generated-file emissions are outside those denominators.
`result.json` preserves the first result and its exact bridge witnesses;
`measurement-record.json` binds its review, implementation and cost evidence.

The recorded 1,665 µs public and 315 µs neutral times are wall time for the
extractor plus the test corpus and streaming callback verification. Each corpus
read hashes its content; each emitted fact, including generated-file emissions,
also hashes its complete source content and checks both line-prefix counts in
the callback. These are finite harness observations, not isolated extractor CPU
or production latency measurements. Frozen-input preflight, descriptor and
exact definition-witness checks run outside that timed region.

Run the frozen check after preregistration review:

```sh
T511_RESULT_PATH="$PWD/spike/t511/result.json" \
  go test ./spike/t511 -run '^TestFrozenFieldQuality$' -count=1 -timeout=30s -v
go test -race ./spike/t511 -count=1 -timeout=30s
```

The check verifies input, label, authority-record and bundle hashes before
extraction. It compares generated raw descriptors and the existing declaration
reader with the independent source field census. Every positive consumer fact
must have the exact byte citation, numeric field identity, SCIP package family,
version detail and unique generated definition/tag/source witness. The T49
declaration family stays distinct; source, generated and SCIP digests plus
exact repository/commit bind their bridge. Every consumer emission participates
in precision, including spans absent from the AST census. Generated-file
emissions are counted separately. Runtime reflection, dynamic, unknown-field
and oneof controls must produce no exact field references at their use sites.

For source review, reproduce the source-only position census without invoking
SCIP or either extractor:

```sh
go run ./spike/t511/cmd/enumerate \
  spike/t511/testdata/public/profiling/service/service.go \
  spike/t511/testdata/neutral/use/use.go
```

`input-preparation.json` records the real offline scip-go argv, environment,
elapsed/CPU times and joined children. Public preparation used the full
upstream module from the pinned Go module cache, with the selected two package
patterns; only the indexed documents, proto, license and root module checksums
are retained here. Neutral preparation used the complete authored module.
`--module-version` is explicit. Repreparing in another directory can change
SCIP metadata bytes and requires new commits/bundles and preregistration;
do not rewrite a frozen index to hide that difference.

This is a finite direct-reference result. It establishes no general accuracy,
scale, ordinary-runtime API/UI/MCP or production-release claim. Classification
means the raw SCIP role, whose assignment roles can differ from semantic Go
read/write intent. T51.2 owns production registration and fences; T51.3 owns
operating and signed promotion. Production signing remains pending.
