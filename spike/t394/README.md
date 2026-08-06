# T39.4 — Evidence-quality and workflow gate

T39.4 closes honestly as `stopped_before_unsealing`. The exact public pilot
charter, accuracy protocol, and workflow-baseline protocol remain design
drafts: Gate 0 is not approved, mandatory thresholds and partner fields are
not frozen, neither independent gold nor an independent workflow baseline is
sealed, and the documents grant no measurement authority. T39.4 therefore
does not access target evidence, unseal a prediction, execute a measurement,
or broaden the pilot scope.

This outcome is a gate success in the procedural sense only: the refusal
worked. It is not an evidence-quality or workflow pass. All thirteen required
projections are retained as `not_run` with `value_state: unmeasured`; there
are no zero-valued measurements to misread:

- Go/gRPC call-site quality and end-to-end service-operation quality;
- processing coverage, evidence reproduction, and unresolved-state rate;
- build-target, deployable, service, and owner-record attribution;
- inventory time and discovery/triage/routing labor;
- post-acceptance correction and owner-routing cost.

Corrections and owner routing remain mandatory in any future calculation.
Underpowered or inconclusive evidence cannot pass, missing values cannot be
imputed, and thresholds cannot move after unsealing. A future attempt requires
a separately approved and digest-frozen Gate-0 authority with sealed
independent gold and baseline records; it cannot edit this stopped receipt.

The exact T39.2 authority is also an input. Its incremental `pipeline` failure
remains terminal, its later phases remain `not_run`, and it is not superseded.
T39.4 cannot convert the target operating-envelope stop into release evidence.

## Reproduction

```sh
t394_tmp=$(mktemp -d)
go run ./spike/t394/cmd/author -root . -out "$t394_tmp/results.json"
cmp "$t394_tmp/results.json" spike/t394/results.json
go test -race ./spike/t394
make docs-check
make verify-glossary
```

The author refuses to overwrite an existing receipt. The retained receipt is
source-free and contains only public paths and digests, the prior stop shape,
the closed authority assessment, the unmeasured gate names, and negative
claims. It adds no production import or behavior and establishes no accuracy,
coverage, workflow value, release, migration-complete, or decommission-safe
claim. `GATE2-V2` remains `NOT_ESTABLISHED`.
