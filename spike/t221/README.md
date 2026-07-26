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
gates and still verifies the committed index digests and this document's
vocabulary discipline. `T221_FETCH=1` clones and pins on demand.

## Gates (all green at the pinned commits)

| Gate | Result |
|---|---|
| G1 module integrity | 12 embedded `thriftreflect.ThriftModule` literals discovered under `.gen/go`; every one carries Name/Package/FilePath/SHA1/Raw and satisfies `sha1(Raw) == SHA1` |
| G2 gitlink + digest join | `idls` gitlink == pinned cadence-idl commit; **12/12 modules** digest-join to `thrift/<FilePath>` in cadence-idl (`sha1(IDL bytes) == embedded SHA1`) |
| G3 field-ID agreement | 267 structs agree between the Raw IDL parse (thriftrw `idl`/`ast`, already allowlisted by thriftdecl) and the generated `wire.Field{ID:}` literals; wire-only structs are exactly the service wrappers; the `workflowId = 10` hand label holds (ten-step IDs — nothing assumes sequential numbering) |
| G4 Apache tag join | 10 tagged structs parse with in-bounds IDs; tags agree with out-of-repo jaeger-idl for Batch/Process/Span; **zero** Apache files embed a ThriftModule — the family has no in-file identity |
| G5 document eligibility | 12 thriftrw + 5 apache eligible files; the adversarial handwritten directories (`common/types/mapper/thrift`, `transport_udp.go`) produce zero eligible documents |
| G6 exact-span SCIP join | all 6 hand-labeled needles reproduced: 3 bound (read, field-0, write) with exact classifications; 3 adversarial abstained for exactly `span-mismatch`, `unbound-symbol`, `ineligible-document` |
| G7 field 0 | bounds admit 0 and reject −1/32768; spelling `health.Meta_Health_Result#0`; `wire.Field{ID: 0}` confined to `*_Result` wrappers; scope basename fallback matches thriftdecl's rule and each module's Name |
| G8 lineage | `contract_scip_package_v1` recipe stable, package-distinct, string-prefix-disjoint from `provisional_repo_path_v1` |
| G9 scale probe | largest generated file `.gen/go/shared/shared.go` = **3,455,648 bytes — 82% of the inherited 4 MiB per-file ceiling**; indexes and symbols within the 64 MiB / 16 KiB bounds |

## Decision table (frozen for T22.2–T22.5)

| # | Decision |
|---|---|
| D1 | Domain `scip-thrift-field`, extractor `thriftfield`, atom schema `t22-v1`, dark flag `experimental.provisional_thrift_field_extraction`. |
| D2 | Predicate `REFERENCES_THRIFT_FIELD`; object `scope.Message#ID`; bounds 0..32,767 with field 0 (result success slot) first-class (G7). |
| D3 | Tier follows binding strength. thriftrw rows where the embedded module digest verifies (`sha1(Raw) == SHA1`, G1) earn `source_binding=module_digest` and are **exact-eligible**; the G2 gitlink join further proved the digest names real reviewable IDL bytes. Apache rows are `derived`, `source_binding=none` — G4 confirmed the family has no in-file identity to verify. |
| D4 | Recognition is a per-family three-way join: document eligibility → in-file field-identity confirmation (thriftrw: Raw IDL parse cross-checked against `wire.Field{ID:}` AST, G3; Apache: `thrift:"name,ID[,flags]"` tags cross-checked against IDL where available, G4) → SCIP definition binding by exact byte-span identifier equality (G6). Never a symbol-string regex. |
| D5 | Document eligibility: thriftrw = the file itself declares `var ThriftModule = &thriftreflect.ThriftModule{...}` with in-file-resolvable strings; Apache = generator header marker + at least one thrift struct tag. Recorded limitation: thriftrw helper/scaffolding files (`*_yarpc.go`, `metafx`, `metaclient`, …) are out of scope in round one — accessors live in the module-declaring file in the pinned corpus (G5). |
| D6 | Lineage reuses the `contract_scip_package_v1` recipe byte-for-byte (sha256 over scheme/manager/package-name); no third lineage family; never joins thriftdecl's `provisional_repo_path_v1` (G8). |
| D7 | Abstention is silent in production, mirroring scipfield: ineligible document, span mismatch, unbound symbol, and malformed symbol all drop the occurrence without a gap predicate; missing index → `scip-index-absent` coverage; malformed index → hard error, zero facts. The spike surfaces abstention reasons only so G6 can assert each adversarial entry died for the intended reason. |
| D8 | Classification from SCIP symbol roles: Definition occurrences bind, never emit; WriteAccess → `write`; otherwise `read` (G6 needles cover read, write, and field-0 definition-only). |
| D9 | Scope spelling reuses thriftdecl's rule — `namespace go` last segment, else IDL file basename. Cadence declares no go namespace; the basename fallback equals each module's embedded Name (G7), so the two packs spell the same scope for the same IDL. |
| D10 | Bounds are inherited from scipfield, not silently raised: 4 MiB per generated file (observed max 3,455,648 B = 82%, G9), 16 KiB symbol, 64 MiB root index, 100k documents / 1M occurrences. Any exceedance found later requires its own recorded decision. |

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
go test ./spike/t221/                 # offline: digests + vocabulary only
T221_FETCH=1 go test ./spike/t221/    # clones/pins corpora, runs G1–G9
T221_CORPUS_ROOT=<dir> go test ./spike/t221/   # reuse existing pinned clones
```
