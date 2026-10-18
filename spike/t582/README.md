# T58.2 — finite Thrift Go observation qualification

**Prospective source contract; reader/scorer execution is held.** This is an
offline source-only qualification of the existing `thrift-consumer` reader. It
does not register a recipe, release a pack, activate lookup-by-operation, or
establish receiver, cross-module, wire, runtime, deployment or exact Caller Map
identity. Root and a separate reviewer must review the complete explicit oracle
and checkpoint these inputs before candidate execution.

## Claim and decision table

The useful observation is a source-supported **candidate** for a generated
Thrift method call or processor construction, at an immutable source location.
`CALLS_OPERATION` remains `heuristic`; `REGISTERS_THRIFT_SERVICE` remains
`derived` and denotes a processor-constructor expression only. Construction
does not prove server registration, serving, or traffic. An operation's package,
service and wire-literal spellings are generated-source hints, not independently
established IDL identity. The frozen contract measures those hints for this
finite input; it does not promote their lineage to exact.

| Source condition | Required classification |
|---|---|
| Direct selector call on an explicitly generated-client-typed parameter/field, or the selected public wrapper's fully cited delegation chain | One heuristic method-call candidate at that expression, with the source-supported package/service/wire hint |
| Direct, unshadowed imported or dot-imported generated processor constructor, with a cited generated declaration | One derived processor-construction candidate; no runtime-registration assertion |
| Two generated declarations supply the same method/constructor spelling | Explicit unresolved ambiguity with both source-distinct candidates; never a promoted single target |
| Unrelated local receiver/function, foreign/unavailable import, or shadowed constructor alias with the same spelling | No positive call/registration claim; an explicit unknown or silence is recorded as abstention separately |
| Method value or constructor copied to a variable, callback table/index, reflection, dynamic callee or wrapper's outer invocation | Unresolved/unsupported observation, with no positive target; never silently counted as a true client call |
| A wrapper's inner direct supported call | Score the inner expression independently; wrapper runtime/use is not inferred |
| Client construction, method declaration, comments/string text, or generated-source body | Excluded from handwritten consumer/registration positives; any positive emission is false for this contract |
| Malformed Go or more than 4 MiB of Go bytes | Exactly one whole-file gap (`parse_error` / `source_too_large`), no call/registration positives from that file |
| Empty source | Complete empty source; no positive or gap |
| Generator header/version/shape outside the frozen admitted inputs, or no same-repository generated context | Unsupported context; no fabricated target. Silence and explicit gap are separately recorded, never called analyzed coverage |

The foreign/lookalike controls deliberately test a limit that a name-only reader
may fail. A matching method spelling alone does not make an unrelated receiver a
true client call, even at a heuristic tier. No rule is weakened after candidate
output. A failed finite claim is retained as **NO-GO**, rather than relabelling
these controls as positives. Generic lexical name observations, if useful, would
need their own explicit future predicate/contract; this round does not invent it.

## Public population and independence

`source-inputs.json` binds the complete Apache-2.0 archive of
`jaegertracing/jaeger-client-go` at commit
`8d8e8fcfd04de42b8482476abac6a902fca47c18`. The LICENSE was fetched and its
Apache text verified **before** the newly acquired application bytes were read.
The archive SHA-256 equals the prior T52 source receipt. The selected inputs are
three complete handwritten files (`transport_udp.go`, `utils/udp_client.go`,
`testutils/mock_agent.go`), all fifteen complete `thrift-gen/**/*.go` files, and
LICENSE. Other archive paths remain inventoried but are not candidate input.
There is no root `go.mod` in the pinned archive (a metadata-only attempted
selection found none); no parent module version is invented. Generated source
imports the repository's own `github.com/uber/jaeger-client-go/thrift` library;
its only admitted source identity is this repository commit. That library is
neither executed nor independently semver-admitted by this round.

This is **one purposive, historically reused repository**, not a random or fresh
blind population. These three files were chosen because they contain the actual
transport/client/processor-construction use absent from the T52 field subset.
The author previously saw the current reader's first-pass/name-index comments
and historical pack-card limits during dependency auditing. This exposure is
disclosed; the labels are explicitly source-derived and independently reviewed
before scoring, not blind. No T19/T52 label, quality outcome, field identity,
SCIP bundle, runtime behavior or result is borrowed as truth.

All five generated service/interface/constructor sections and six generated
client-method/`Call`-literal/processor-map correspondences were read directly.
Their exact literal source spans are in `generated-witnesses.json`. These are
context witnesses, not six public consumer positives. `public-sites.json`
contains a complete manually authored call-expression census of the three
handwritten files, including nested calls, builtins and conversions. The
literal helper only finds the explicitly chosen callee text on its specified
source line; it does not parse Go, discover sites, invoke the reader or derive
expected classifications. Full-file census permits unmatched/extra emissions
to remain false positives, rather than disappearing from a handpicked list.

The two public `EmitBatch` sites are supported by separate field/delegation
citations, not an assumption that any `EmitBatch` receiver is a client. The
public `NewAgentProcessor` site is in `MockAgent.serve`; a same-spelled local
variable in `StartMockAgent` is in a different function scope and does not
shadow that import at the constructor expression. These observations still
assert neither exact receiver identity nor runtime service registration.

## Authored controls and version limits

`controls.json` and the inert `inputs/controls/` files freeze direct calls,
`go`/`defer`, dot-import construction, inner wrapper versus outer wrapper,
unrelated receivers/functions, unavailable/foreign imports, lexical alias
shadowing, dynamic method/constructor values, indexed callbacks, duplicate
generated names, header lookalikes, unsupported generator, malformed, oversized,
empty and generated-only cases. Every case is isolated: its declared generated
context list is the complete list, never the union of other cases. Expected
labels, context mutations and their source bytes are authored before execution.
The oversized source is a closed deterministic byte recipe with a frozen byte
length and hash; it is materialized only for that case, not committed as 4 MiB
of repeated comment text.

Only Apache Thrift Compiler **0.14.1 modern-header committed Go** is observed in
the public source. A legacy-header copy and modern 0.19.0-header copy are
synthetic header/shape controls with disclosed byte changes, not public
generator-version validation. Byte-exact generated context remains separate
from those authored copies. A named `thriftgo` header is an unsupported control;
this reader does not acquire CloudWeGo/thriftgo support by sharing its package
name. No compiler, client, server, Go application or Redis/SQL program runs.

## Measures, stops and go/no-go

Freeze separate denominators for public calls, public processor construction,
all other public call expressions, public generated context, authored call
positives, authored processor positives, ambiguity, dynamic/unsupported cases,
semantic lookalikes, file gaps and excluded cases. `preregistration.json` states
the exact counts. Every output assertion enters a complete emission census;
unmatched positives, wrong candidates, duplicate site rows, false exact tier,
wrong source, or generated-body leakage fail. State, target hint and source
witness checks are separate. Silence in unsupported/dynamic cases is tracked
as silence, not an explicit unresolved success or analyzed coverage.

The finite engineering pass requires **100%** of the frozen supported positives
and exact required gaps/ambiguities, **zero** promoted lookalike/dynamic/
unsupported/excluded sites, **zero** unmatched/duplicate assertions and **zero**
source-witness disagreements. All unsupported/dynamic sites remain in their
denominators. A nonzero disagreement produces NO-GO; do not tune the reader,
labels or threshold to this corpus. A passing finite engineering result does
not satisfy T13.1's independent accuracy/promotion bar or T58.3's common bar.
Those remain separate and unperformed. No sample size supports population
accuracy, confidence bound, recall-at-large, absence, scale or SLO claims here.

Any input/hash, license, source-span, tool/source binding or output-bound
failure stops before interpreting quality. Keep the first stop and all safe
bytes. No retries, replacement files or population expansion after outputs.
The reader/exporter/scorer contract must itself receive source review before
the first candidate, and may only adapt representation—not oracle truth.

## Minimum later implementation and cost plan

After the source checkpoint, use the existing reader unchanged through a tiny
offline in-memory corpus/export adapter. Do not add a recognizer, production
dependency, registry entry, store, UI or release artifact. The adapter must bind
the actual evaluator Git/source digest and all reader dependencies; preserve raw
safe assertions and separately normalize source coordinates for the frozen
oracle. It must export unavailable/coverage states honestly and examine every
emission, not just rows whose predicates happen to match labels.

One bounded build (120 seconds, first result retained) and exactly two sequential
fresh reader processes are proposed, subject to root's serialized slot. Each
process handles the public unit and all 26 isolated controls sequentially under
one **global 30-second process deadline**, with no per-case deadline reset or
retry. There are two parser children total, not 54 case processes. Each process
receives only the frozen unit/case inventory,
rejects symlinks/unknown paths, admits at most 64 Go files per unit, at most
8 MiB admitted source bytes including the oversize sentinel, and at most 4,096
assertions/8 MiB encoded output. A file over 4 MiB is offered only to the reader's
oversize refusal path; no higher memory capacity is inferred. Each complete pass
also admits at most 16 MiB of offered source across all
27 units, at most 8,192 assertions and at most 16 MiB of total stdout; the
per-case caps do not multiply the process deadline or global output bound.
Output encoding must be bounded, stderr bounded to 64 KiB per process, and
every child stopped and joined.
Record wall and observed direct-process RSS; CPU is unmeasured unless an actual
measurement is retained. Observed RSS is not a hard heap guarantee.

The current two-pass reader may read generated context once and handwritten Go
twice. Each process constructs a fresh reader, corpus and name catalog for every
unit; no catalog may survive into the next case. At most one parser process
runs, and two fresh passes must produce identical deterministic facts, while timing/accounting is stored
separately. Catalogs and complete results are bounded but retained for the
unit; there is no cache invalidation, query/sync/publication work, persistent
lock, background goroutine, network or application child in the spike. Source
preregistration alone performs only finite source reads/hashes and adds no
product steady-state cost.

## Pre-checkpoint independent review corrections

The first source-freeze draft omitted the package-level `errors.New` initializer
at `transport_udp.go:39`. Independent review caught it before any candidate
execution: the corrected complete public census is **102** expressions
(**2** supported calls, **1** processor construction, **1** unproved name site,
**98** excluded calls). All three files' package-level declarations were
rechecked. The five authored generated-context copies now carry prominent
in-file modification notices under Apache License section 4(b), preserving the
header-test semantics. The process plan is explicitly two globally bounded
passes over 27 fresh-state units, rather than a per-case deadline expansion.
`precheckpoint-review-correction.json` records this prospective correction and
the superseded draft hashes; `pre-review/` is historical preparation only.
No candidate, scorer, parser or build outcome informed any change.
