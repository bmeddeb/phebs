# T22.1 — Thrift field-reference validation spike

Charter: freeze the recognition, identity, tier, lineage, and abstention
rules the Epic 22 `thriftfield` extractor must implement, by proving or
refuting each candidate rule against pinned real corpora with executable
gates. Spike code is throwaway; the decisions below and the committed
fixtures are the deliverable. Nothing here is imported by production
packages, and nothing here is or authorizes an accuracy claim — GATE2-V2
remains `NOT_ESTABLISHED`.

## Corpora (corpus.lock.json)

| Name | Commit | Role |
|---|---|---|
| cadence | `98f3d45e` | thriftrw generated Go (`.gen/go`, 12 embedded ThriftModules) + handwritten accessor call sites (`common/types/mapper/thrift`) in one repo; `idls` gitlink at `ee96c0ae` |
| cadence-idl | `ee96c0ae` | the exact commit cadence's gitlink names — the G2 digest join is exact-by-construction |
| jaeger-client-go | `8d8e8fcf` | Apache Thrift generated Go (`thrift-gen`, Thrift Compiler 0.14.1 tags) + handwritten write sites (t191 pin reused) |
| jaeger-idl | `0daa7197` | out-of-repo IDL for the Apache cross-check (t191 pin reused) |

Clones land in `spike/t221/corpus/` (gitignored); `T221_CORPUS_ROOT` reuses
existing pinned clones. Offline, `go test ./spike/t221/` skips the corpus
gates and still verifies the committed index digests, this document's
vocabulary discipline, and the synthetic rule adversaries. `T221_FETCH=1`
clones and pins on demand.
Every corpus gate verifies locked HEAD, unchanged tracked bytes, and rejects
all untracked or ignored filesystem entries. `EnsureCorpus` may add one
untracked `go.mod` fence for a pre-module checkout; only its exact
`corpus.invalid/<lock-name>` content is admitted.

## Gates (all green at the pinned commits)

| Gate | Result |
|---|---|
| G1 module integrity | 12 embedded `thriftreflect.ThriftModule` literals discovered under `.gen/go`; every one carries Name/Package/FilePath/SHA1/Raw and satisfies `sha1(Raw) == SHA1` |
| G2 gitlink + digest join | `idls` gitlink == pinned cadence-idl commit; **12/12 modules** digest-join to `thrift/<FilePath>` in cadence-idl (`sha1(IDL bytes) == embedded SHA1`) |
| G3 field-ID agreement | 267 structs agree between the Raw IDL parse (thriftrw `idl`/`ast`, already allowlisted by thriftdecl) and the generated `wire.Field{ID:}` literals; wire-only structs are exactly the service wrappers; the `workflowId = 10` hand label holds (ten-step IDs — nothing assumes sequential numbering) |
| G4 Apache tag join | 10 tagged structs parse with in-bounds IDs; tags agree with out-of-repo jaeger-idl for Batch/Process/Span; **zero** Apache files embed a ThriftModule — the family has no in-file identity |
| G5 document eligibility | 12 thriftrw + 5 apache eligible files; thriftrw requires the selector qualifier to resolve to the exact `go.uber.org/thriftrw/thriftreflect` import, Apache markers are complete comment lines, parse failures on candidates fail the gate, and the adversarial handwritten directories (`common/types/mapper/thrift`, `transport_udp.go`) produce zero eligible documents |
| G6 exact-span SCIP join | all 6 hand-labeled needles reproduced: 3 bound only after deriving and matching scope/message/name/ID plus exact definition span and enclosing SCIP type (read, field-0, write); 3 adversarial abstained for exactly `span-mismatch`, `unbound-symbol`, `ineligible-document`; offline adversaries additionally pin duplicate-definition abstention, UTF-16 reference spans, malformed-range refusal, and unknown-role preservation |
| G7 field 0 | bounds admit 0 and reject −1/32768; spelling `health.Meta_Health_Result#0`; `wire.Field{ID: 0}` confined to `*_Result` wrappers; scope basename fallback matches thriftdecl's rule and each module's Name |
| G8 lineage | `contract_scip_package_v1` recipe stable, package-distinct, string-prefix-disjoint from `provisional_repo_path_v1` |
| G9 bounded file probe | largest generated file `.gen/go/shared/shared.go` = **3,455,648 bytes — 82% of the inherited 4 MiB per-file ceiling**. The committed indexes are small authored needle fixtures, so they do **not** measure the 64 MiB / 100k-document / 1M-occurrence production SCIP ceilings; those remain unmeasured until T22.2's real-indexer fixture |

## Decision table (frozen for T22.2–T22.5)

| # | Decision |
|---|---|
| D1 | Domain `scip-thrift-field`, extractor `thriftfield`, atom schema `t22-v1`, dark flag `experimental.provisional_thrift_field_extraction`. |
| D2 | Predicate `REFERENCES_THRIFT_FIELD`; object `scope.Message#ID`; bounds 0..32,767 with field 0 (result success slot) first-class (G7). |
| D3 | Tier follows binding strength. thriftrw rows where the embedded module digest verifies (`sha1(Raw) == SHA1`, G1) earn `source_binding=module_digest` and are **exact-eligible**; the G2 gitlink join further proved the digest names real reviewable IDL bytes. Apache rows are `derived`, `source_binding=none` — G4 confirmed the family has no in-file identity to verify. |
| D4 | Recognition is a per-family three-way join: document eligibility → in-file field-identity confirmation (thriftrw: Raw IDL scope/name/ID cross-checked against the generated struct field order and `wire.Field{ID:}` AST; Apache: generated package + `thrift:"name,ID[,flags]"` tag) → one SCIP definition whose exact byte span and enclosing type descriptor match that derived identity (G6). Duplicate symbol definitions abstain; traversal order never selects one. Never a symbol-string regex. |
| D5 | Document eligibility: thriftrw = the file itself declares `var ThriftModule = &thriftreflect.ThriftModule{...}` where the selector qualifier resolves to the exact `go.uber.org/thriftrw/thriftreflect` import and all strings resolve in-file; Apache = a complete generator-header comment line + at least one valid thrift struct tag. A malformed candidate is a gate error, not silent ineligibility. Recorded limitation: thriftrw helper/scaffolding files (`*_yarpc.go`, `metafx`, `metaclient`, …) are out of scope in round one — accessors live in the module-declaring file in the pinned corpus (G5). |
| D6 | Lineage reuses the `contract_scip_package_v1` recipe byte-for-byte (sha256 over scheme/manager/package-name); no third lineage family; never joins thriftdecl's `provisional_repo_path_v1` (G8). |
| D7 | Abstention is silent in production, mirroring scipfield: ineligible document, span mismatch, unbound field identity or symbol, malformed definition/reference range, malformed symbol, and duplicate definition all drop the occurrence without a gap predicate; missing index → `scip-index-absent` coverage; malformed index → hard error, zero facts. The spike surfaces abstention reasons only so the executable gates can prove each refusal path. |
| D8 | Classification matches scipfield's deterministic role precedence: Definition occurrences bind, never emit; `write > read > test > generated > unknown`. Every emitted reference first validates a non-empty source range against the immutable source bytes using its document's declared UTF-8/16/32 position encoding. Unknown is never relabeled read. |
| D9 | Scope spelling reuses thriftdecl's rule — `namespace go` last segment, else IDL file basename. Cadence declares no go namespace; the basename fallback equals each module's embedded Name (G7), so the two packs spell the same scope for the same IDL. |
| D10 | The 4 MiB generated-file bound is measured against the pinned corpus (observed max 3,455,648 B = 82%, G9) and is not raised. The 16 KiB symbol, 64 MiB root-index, 100k-document, and 1M-occurrence bounds remain inherited candidate limits, **not scale-validated by the small authored indexes**; T22.2 must exercise them with its real-indexer fixture or record a narrower production decision. Any later raise requires its own recorded decision. |

## Authoring circularity (disclosed)

The committed SCIP indexes are **authored**, not produced by a real indexer
(the t201 prepared-once-reviewed-and-copied-verbatim policy;
`index.lock.json` pins the bytes). Occurrence ranges are located in the real
pinned corpus bytes, so span discipline, eligibility, classification, and
abstention are proven against genuine files — but symbol strings follow the
asserted scip-go grammar rather than a real indexer's output. Mitigations:
`testdata/needles.json` is hand-labeled from the IDL sources before
authoring; the three adversarial entries were authored to die, and G6
asserts they die for exactly the labeled reasons. A real-indexer comparison
is **deferred, recorded here**: T22.2 fixture authoring must revisit whether
a scip-go-produced index over a small thriftrw module confirms the symbol
shape before the extractor hardcodes it. None of this is or enables an
accuracy claim; the gates prove rule discipline over pinned bytes, and the
result is not an accuracy claim about any extractor.

## Running

```
go test ./spike/t221/                 # offline: digests, vocabulary, rule adversaries
T221_FETCH=1 go test ./spike/t221/    # clones/pins corpora, runs G1–G9
T221_CORPUS_ROOT=<dir> go test ./spike/t221/   # reuse existing pinned clones
```
