# T52.1 Apache Thrift field input preparation

This directory prepares a finite source-only field oracle. No indexer,
production extractor or candidate scorer has run for this leaf. Registration,
production signing and operating qualification remain separate.

The proposed committed-input recipe recognizes header-declared Apache Thrift
Compiler **0.14.1**, real **scip-go 0.2.7** root `index.scip`, exact generated
struct tags and SCIP identifier/type/span joins. Apache references remain
`derived`, `generator=apache`, `source_binding=none`: these generated files
carry neither embedded IDL bytes nor an authenticated generated-from relation.
Field identity uses the SCIP package family and generated message/numeric ID;
field zero is first-class. T49.2 declaration lineage is a different family.
The pinned external IDLs can supply an independently checked witness; they
never silently grant a production cross-family join. In particular, generated
service-result Go names need not equal the declaration reader's synthetic
message spelling.

`source-inputs.json` records unchanged upstream files and evaluator context.
The two public handwritten files are `jaeger_tag.go` (30 field writes) and
`testutils/sampling_manager.go` (three nested field writes), selected before
any result. Their generated packages and vendored Thrift runtime are copied
unchanged from the same pin. A new `go.mod` evaluates this selected subset;
this is not the complete upstream application context. Cached OpenTracing
1.2.0 is an index-preparation dependency, outside the scored population.
Exact Apache-2.0 licenses were checked before source reads and are retained.
No source stub replaces an application dependency.

The separate authored controls exercise direct reads/writes, nonsequential
IDs, field0, UTF8 byte offsets, ordinary lookalike fields, an Apache accessor,
reflection and duplicate wire IDs. The neutral Go fixture is authored rather
than compiler-produced and says so in its header. It supplies no independent
public-source quality result.

`source-labels.jsonl` contains the complete identifier census of exactly those
three application files, plus one unsupported reflection string. Positives
are explicit source/tag identities; other identifiers are negatives. The
frozen counts are public33positive/266negative and neutral11positive/
60negative/4unsupported. Generated references are reported separately, not
mixed into application recall or the negative denominator. The locator
script reads no SCIP or production parser/extractor. Labels are frozen before
input indexing and receive independent lead review before candidate execution.

Preparation is split into checkpoints in `preregistration.json`. The next
checkpoint is source-label review. Only then may a serialized bounded offline
indexer prepare inputs. Any resulting SCIP bytes must receive actual local
Git evaluation commits/bundles from the lead; the upstream pins remain source
provenance only. Final revision/index/bundle bindings are reviewed before the
first candidate measurement. Missing dependencies, unsupported symbol shapes
or failures stop honestly without relabeling or threshold changes.
