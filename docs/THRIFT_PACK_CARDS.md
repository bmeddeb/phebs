# Thrift protocol-pack cards

Completed instances of the [evidence-pack card template](./EVIDENCE_PACK_CARD.md)
for the Epic 19 Thrift packs. Both packs are **experimental-dark**: the release
invariant's blank-field prohibitions bind at `released` status, which neither
pack holds or requests. The PackRelease binding machinery remains design-only
(see [PACK_MANIFEST.md](./PACK_MANIFEST.md)); fields that depend on it say so
explicitly rather than simulating it.

---

## Pack: Thrift declared contracts

| Field | Value |
|---|---|
| Pack ID | `phebs.thrift.contract` |
| Status | `experimental-dark` |
| Predicate(s) | `DECLARES_SERVICE, DECLARES_OPERATION, DECLARES_MESSAGE, DECLARES_FIELD, THRIFT_EXTRACTION_GAP` |
| Language/framework | Thrift IDL (language-neutral source); parsed by `go.uber.org/thriftrw` v1.34.0 `idl`/`ast` |
| Pack version | `1.0.0` (`thrift-contract` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/thriftdecl`; no separate binary — the phebs release binary digest governs |
| Schema version | atom `t19-v1`; details `thrift-service-detail-v1`, `thrift-operation-detail-v1`, `thrift-message-detail-v1`, `thrift-field-detail-v1`, `thrift-gap-detail-v1` |
| Release binding | none — experimental-dark; PackRelease machinery is design-only |
| Owner | Ben Meddeb |
| Independent validation owner | none — single-operator project; see validation section |
| Last measured | 2026-07-25 (T19.1 spike gates at pinned corpora) |
| Validation expires/review due | re-run `spike/t191` gates on any parser upgrade, rule change, or corpus re-pin |
| Exception authority | operator (Ben Meddeb); the pack itself grants no exceptions |
| Current decision | dark — enabled only by `experimental.provisional_thrift_extraction` |

**Claim.** For each `.thrift` file readable at the indexed commit, the pack
asserts the services, operations (`scope.Service/method`), struct/union/
exception shapes, and numbered fields that file declares, with exact
declaration-start-line source locators, tier `exact`, and file-scoped
provisional lineage (`provisional_repo_path_v1`). Operation request/response
shapes are the wire-implicit argument/result structs (field `0` success,
`throws` as result fields, no result struct for `oneway`), emitted as
synthetic same-file messages and marked `synthetic`.

**Non-claims.** No cross-file or cross-repository identity: include-qualified
types stay `unresolved` with include context; lineage equality across repos is
never asserted. No runtime behavior, no absence claims, no accuracy
percentage (GATE2-V2 remains `NOT_ESTABLISHED`). No wire-compatibility
verdicts — Buf checking is protobuf-only and has no Thrift engine. Inherited
operations of `extends` services are not expanded (the parent name is
recorded in service detail).

**Incomplete analysis representation.** Files with implicit or out-of-range
field identifiers produce exactly one tier-`unresolved`
`THRIFT_EXTRACTION_GAP` assertion (`implicit_field_id` / `invalid_field_id`)
and no shape claims — Thrift assigns negative wire identifiers implicitly,
and emitting a guess would fabricate identity. Parse, read, complexity-bound,
and publication failures abort the whole staged run, leaving prior published
facts intact. Same-file typedef chains are chased at most 16 hops; nested
containers beyond one level abstain (`NESTED_CONTAINER_SHAPE`).

**Validation.** T19.1 executable rule gates (`spike/t191`, corpora pinned in
`corpus.lock.json`): 100% parse rate over jaeger-idl; scope precedence
(`namespace go` last segment, else file basename) reproduced by every
generated package in the same corpus; live cross-corpus operation-object
joins; determinism by byte-identical double extraction (unit suite). These
are rule-validation gates, not accuracy measurements.

**Operating envelope.** 4 MiB / 500k tokens / 128 nesting levels per source
file; 64 include paths / 4 KiB include context; worker bounds unchanged
(5,000 facts, 15-minute run). `.thrift` symlinks fail the corpus walk closed.

---

## Pack: Thrift Go consumers

*Reserved for T19.3 (`thrift-consumer` 1.0.0). Rules and labeled-sample
validation are already recorded in `spike/t191/README.md` decisions D3–D6;
this card completes when the extractor lands.*
