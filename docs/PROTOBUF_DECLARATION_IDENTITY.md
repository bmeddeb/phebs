# Protobuf declaration identity and input contract — T49.1 review draft

*Design artifact for ticket T49.1 (Epic 49). This document grants nothing: no
release, no pack promotion, no accuracy or completeness claim, no pilot or
environment authority, and no runtime behavior. It changes no extractor, no
identity token, no schema, and no published assertion. `GATE2-V2` remains
`NOT_ESTABLISHED`; the `phebs.protobuf.contract` pack remains
`experimental-dark` per [PROTO_GRPC_PACK_CARDS.md](./PROTO_GRPC_PACK_CARDS.md),
and nothing here reopens, retries, or reinterprets that closed campaign.*

This contract does two of the three things T49.1 names, and defers the third
honestly:

1. It **resolves** the provisional repo/path versus descriptor/module identity
   gate (§4) and **shares one identity/input contract** with T47.2 (§7). These
   are design decisions grounded in the shipped code and are frozen here.
2. It **freezes** the declaration/operation/message/field fact contract and the
   explicit parser-gap taxonomy as the exact scorable surface (§5, §6). Freezing
   a description of what the shipped extractor already emits requires no new
   authority.
3. It **defines the shape** of independent scoring (§8) but does **not** seal it.
   Sealing is blocked on the same two non-fabricable inputs that block
   [ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md) and the deferred
   T47.2b caller-quality protocol: named independent humans and a real randomness
   beacon (§9). No name, owner, seed, threshold, or digest is invented here.

## 1. Purpose and lineage

`phebs.protobuf.contract` reports Protobuf declarations from `.proto` source with
exact immutable evidence and explicit gaps. Its declaration identity is the join
key for the exact Caller Map (T47.2), for resolver materialization, and for the
field-reference recipe (T51.1). Epic 49 owns this pack separately from the caller
product; T49.1 is the shared identity/input contract that
[ROADMAP.md](./ROADMAP.md) and the 2026-10-09 T48.1 decision in
[PLAN.md](../PLAN.md) sequence *before* release-loader integration (T48.2):
"Identity/corpus preparation can proceed before release-loader integration; only
activation waits on the applicable runtime and evidence gates."

The identity gate is long-declared, not new. `internal/extract/extractors/protodecl`
has always minted a **provisional** repository/path token and marked it
machine-visibly; the operating guide records that "T12.3 still lacks the trusted
protobuf module/root identity needed for canonical descriptor lineage." This
contract states precisely why that limitation is correct for the pure-reader
scope, what would be required to lift it, and how a future canonical identity
must be introduced without corrupting the present one.

The authoritative implementation is
[`internal/extract/extractors/protodecl/protodecl.go`](../internal/extract/extractors/protodecl/protodecl.go)
(domain `proto-contract`, version `3.0.0`, schema `t17-v1`). Where this document
and the code disagree, the code wins and this document is corrected in the same
PR; the frozen constants in §5 are quoted from source, not paraphrased.

## 2. The identity gate

A Protobuf declaration must carry a **declaration lineage**: a stable identity
that says *which declaration set* a service, operation, message, or field belongs
to. Two candidate definitions exist, and they are not interchangeable:

- **(A) Provisional repository/path identity** — file-scoped. Each `.proto` file
  is its own lineage, keyed on `(repository, protoPath)`. This is what ships
  today:

  ```
  provisional_repo_path_v1_<hex(sha256(repository + "\x00" + protoPath))>
  ```

  minted by `lineageID` in `protodecl.go` and, byte-identically, by
  `declarationLineageID` in
  [`internal/extract/attribution.go`](../internal/extract/attribution.go).

- **(B) Canonical descriptor/module identity** — descriptor-set-scoped. A lineage
  would identify one *linked* Protobuf descriptor set (one module / import root),
  so that every file compiled into the same `FileDescriptorSet` collapses to a
  single canonical identity, and two files that merely reuse a package name under
  mutually-exclusive import roots stay separate.

The gate is which of these `phebs.protobuf.contract` may claim as its declaration
identity, and on what input.

## 3. The input contract (what the extractor is actually given)

[`internal/extract/sdk/sdk.go`](../internal/extract/sdk/sdk.go) is explicit that
`Corpus` is the **only** input capability. It supplies exactly four things:

| Capability | Returns | Identity-bearing? |
|---|---|---|
| `RepoName()` | repository name string | yes — repository |
| `Commit()` | commit string | yes — revision (not used in the lineage token) |
| `WalkFiles(ctx, fn)` | each relative path | yes — path |
| `Read(ctx, path)` | `Blob{Content, Digest}` | content bytes + content digest |

The optional richer inputs — `SCIPCorpus`, `SCIPDocumentScope`,
`SCIPTypedPartition`, `SCIPDocumentFilter`, and
[`AttributionCorpus`](../internal/extract/sdk/attribution.go) — add SCIP
documents, classification, and generated-from provenance. **None of them supplies
a Go module path, a Protobuf import root, a Buf module name, or a compiled
`FileDescriptorSet`.** `SnapshotProvenance.external_digest` is a reserved slot for
a future adapter; it is not populated by the pure reader. The candidate policy for
`proto-contract` enumerates `hasSuffix(".proto")` only — unlike `scip-proto-field`,
it does **not** admit `buf.yaml`, so no module-root declaration is even read.

`protodecl` correspondingly performs **no import resolution and no cross-file
linking** (its package doc says so). `collectFileContext` reads a file's own
`package` and its literal `import` strings for context and gap accounting, and
never resolves an import to another file. The parser is in-process
(`github.com/bufbuild/protocompile/parser`) and is preflighted by
[`internal/idlpreflight`](../internal/idlpreflight/preflight.go) at a 4 MiB source
ceiling (`MaxFileBytes = 4 << 20`), a 500,000-token ceiling (`MaxTokens`), and a
128-deep structural ceiling (`MaxStructuralDepth`); `protodecl` adds its own
bounded import-context capture (at most 64 paths, `maxImportContextPaths`, within
4 KiB, `maxImportContextBytes`).

**Consequence.** Canonical descriptor/module identity (B) is *not derivable* from
the input the pure reader is given. Producing it would require admitting a new,
heavier input — a trusted module/import-root manifest plus linked-descriptor
extraction (compile the transitive import closure to a `FileDescriptorSet`, or
accept a checked-in descriptor set with provenance). That input is exactly what
Epics 51/53 and T51.1 gate on and what the `external_digest` slot reserves. It is
out of scope for T49.1 and cannot be synthesized from repo/path/bytes.

## 4. Gate resolution

**(A) is the admitted declaration identity for the `proto-contract` pure-reader
scope. (B) is not rejected — it is a separate, heavier future recipe that must
arrive as a new, prefix-disjoint lineage family, never as a silent in-place rename
of (A).**

Why (A) is correct *here*, not merely expedient:

- **It is the only identity the input can prove.** §3 shows the reader cannot
  obtain a trusted module/import root. Claiming (B) without that input would
  assert a canonical descriptor set the extractor never linked — a false claim.
- **File-scoping is the conservative choice against false merges.** The
  `lineageID` comment states it directly: parser-only extraction "cannot prove
  that same-directory files belong to one canonical descriptor set; separating
  them avoids false merges for mutually-exclusive roots with duplicate FQNs."
  Two import roots may declare the same fully-qualified name; collapsing them on
  name alone would merge declarations that are not the same declaration.
  `TestSameDirectoryDuplicateFQNsStaySeparate` pins this — it is the exact test a
  descriptor-set merge would invert.
- **It matches the standing ADRs.** Epic 17 records that unlinked types are
  "never called external without trusted module/import-root identity," and the
  descriptor-fork ADR requires that "forked or unrelated descriptor sets reusing
  names/numbers stay separate until identity resolution proves shared lineage."
  (A) keeps them separate by construction; (B) is the "identity resolution" those
  ADRs defer to, and it does not exist yet.
- **Its limitation is machine-visible, not hidden.** The coverage protocol set is
  `["protobuf", "lineage-provisional-repo-path-v1"]` (§5), and the token prefix
  `provisional_repo_path_v1_` names the provisionality in every stored value. A
  consumer can see the identity is repo/path-scoped without reading this document.

Why (B) must be a **new prefix-disjoint family**, never a rename of (A):

- **The token is a primary-key component.** `store.ComputeAssertionID` hashes
  `Lineage` into every assertion ID. Changing the token shape in place re-keys
  every published assertion, every Surreal index and cursor, the
  `resolvernamespace` candidate key and digests, `rpccallerposting` digests, and
  the `gocaller` fact fingerprint. A rename is a data migration, not a relabel.
- **Two families must be distinguishable to coexist.** A canonical
  descriptor/module lineage and a provisional repo/path lineage can both be live
  during any migration window (different repositories, different recipe
  generations). Distinct prefixes keep an old and a new generation from silently
  colliding or double-counting, and let a reader tell which identity it holds.
- **The non-join fence must be preserved.** [`spike/t221`](../spike/t221/README.md)
  gate G8 pins that the SCIP-derived `contract_scip_package_v1_…` family **never**
  joins the `provisional_repo_path_v1_` family. A future (B) family inherits that
  discipline: distinct spelling, explicit join policy, no accidental unification.

A future (B) recipe is therefore its own ticket with its own admitted input, its
own prefix, its own correctness evidence, and its own review. T49.1 records the
gate as **resolved for (A)** and **open for (B) pending a trusted module/import-root
input**; it does not authorize, design, or schedule (B).

## 5. Frozen declaration fact contract (the exact scorable surface)

These are the facts `phebs.protobuf.contract` emits today, quoted from
`protodecl.go`. They are frozen as the surface any independent scoring round
(§8) may measure. Every fact is `Tier: "exact"` and carries an immutable
half-open byte span `[StartByte, EndByte)` derived from the parser's exact
`RawText`, with a fail-closed check that the span is in range and that
`Content[StartByte:EndByte]` equals the parser text.

| Predicate | Object | RuleID | Detail schema | Detail carries |
|---|---|---|---|---|
| `DECLARES_SERVICE` | `joinFullName(package, service)` | `proto-service-v3` | `proto-service-detail-v1` | schema, name |
| `DECLARES_OPERATION` | `<serviceFQN>/<rpcName>` | `proto-rpc-v3` | `proto-operation-detail-v1` | request/response type references (§6), `ClientStreaming`, `ServerStreaming` |
| `DECLARES_MESSAGE` | fully-qualified message name | `proto-message-v3` | `proto-message-detail-v1` | schema, name |
| `DECLARES_FIELD` | `<messageFQN>#<tag>` | `proto-field-v3` | `proto-field-detail-v2` | type reference or `map{key,value}`, cardinality, `oneof` |

A proto2 `group` emits **both** a `DECLARES_FIELD` (field name lower-cased, per the
descriptor convention) and a `DECLARES_MESSAGE` for the synthetic nested message.
Field cardinality is one of `repeated`, `required`, `optional`, `singular`
(`fieldCardinality`).

Common invariants on every emitted fact:

- `SchemaVersion = "t17-v1"`; `AdapterConfigDigest = "sha256:e3b0…b855"` (the
  SHA-256 of the empty adapter configuration — a real digest, not a sentinel).
- `Subject = relPath`; `Lineage = provisional_repo_path_v1_<…>` per §2/§4;
  `CodeRole = roleFor(relPath)`, one of `vendor`, `test`, `production` with
  vendor > test > production precedence.
- `FactFingerprint = predicate + "|" + object`. **Lineage is deliberately
  excluded from content identity**, so identical vendored blobs in different
  repositories share atoms (`TestIdenticalCrossRepoBlobDeduplicatesAtom`). This
  asymmetry matters for §7: a lineage change re-keys *caller* atoms, whose
  fingerprint includes lineage, but **not** declaration atoms.
- Coverage is `sdk.Coverage{Protocols: ["protobuf", "lineage-provisional-repo-path-v1"]}`.
  Protocols are sorted, deduplicated, and capped at 64 downstream; the run
  fails closed rather than emit partial coverage.

**Candidate scope.** `Candidate(path)` is `hasSuffix(path, ".proto")`. Extraction
is parser-only: no import resolution, no cross-file linking, no descriptor
compilation, no code execution, and no Buf invocation.

**Fail-closed, never partial.** Any candidate read, parse, tree-walk, span, or
complexity failure aborts the whole staged run: `Extract` returns an empty
`Coverage` and the error, because "returning successful partial coverage would
replace known evidence with a subset." A malformed or unsupported declaration is
therefore a run failure, never a silent empty contract.

## 6. Type-reference resolution states and the explicit gap taxonomy

Each operation request/response and each field type is a `typeReferenceDetail`
whose `Resolution` and `Reason` are the extractor's honest statement of what it
could and could not prove **from the same file**. These are the abstention states
a scoring round must count, and they are the precise pressure point of the §2 gate:

| Resolution | Reason | Meaning |
|---|---|---|
| `intrinsic` | — | scalar/built-in type; nothing to link |
| `same_file` | — | resolved to exactly one declaration in the same file |
| `unresolved` | `AMBIGUOUS_SAME_FILE_DECLARATION` | more than one same-file candidate for the name |
| `unresolved` | `INVALID_DECLARATION_KIND` | resolved to a non-message where a message was required (e.g. an RPC input/output) |
| `unresolved` | `DECLARATION_NOT_FOUND` | no same-file candidate and the file declares no imports |
| `unresolved` | `IMPORT_LINKING_UNAVAILABLE` | no same-file candidate **and** the file imports something — the reference may resolve through an import the pure reader does not link |

`IMPORT_LINKING_UNAVAILABLE` is the exact state that a canonical descriptor/module
identity (§4 (B)) plus import linking would turn into `same_file`/cross-file
resolution. Until that input exists, it is an **explicit, counted abstention** —
evidence of the reader's boundary, not an error and not a miss. This is the state
§7 shares with T47.2.

Beyond type references, the extractor fails closed (whole-run error, §5) on:
extension fields (`ExtendNode`, at file top level or inside a message body) with
"extension fields require descriptor linking for extendee lineage"; a missing
name/tag/type on any service, rpc, message, field, oneof, group, or map; an
invalid parser span or a parser-text/source mismatch; and any input exceeding the
§3 complexity ceilings.

## 7. Shared identity/input contract with T47.2 (the exact Caller Map join)

T49.1 and T47.2 share **one** identity/input contract, and this section is its
authority. The `attribution.go` comment states the requirement exactly:

> `declarationLineageID` is the same provisional repository/path identity used by
> protodecl and thriftdecl. Generated-from provenance must join the declaration
> evidence key itself; exposing a separate repo:path spelling would make an exact
> Caller Map impossible even when both sides are proven.

The shared contract:

- **`DeclarationLineage` is the join key** between the declaration side
  (`protodecl` / `attribution`) and the caller side (`gocaller`). Its value is
  `provisional_repo_path_v1_<hex(sha256(repository + "\x00" + declarationPath))>`,
  where `declarationPath` is the **`.proto` source path**, not the generated
  `*_grpc.pb.go` stub path. `protodecl` mints it over the `.proto` path;
  `attribution` mints the identical token over the snapshot `DeclarationPath` so
  the join is byte-identical.
- **The syntactic consumer lane does not join, by design.** The `grpcgo`
  extractor mints the *same prefix* over the **stub** path — a disjoint value that
  never equals a declaration lineage. Stubs declare no facts of their own. This is
  intentional: the exact join is declaration↔caller, not stub↔caller.
- **Four validators enforce the contract in code.** `resolvernamespace`
  ([`model.go`](../internal/resolvernamespace/model.go)), `resolvermaterialize`
  ([`view.go`](../internal/resolvermaterialize/view.go)), `rpccallerposting`
  ([`model.go`](../internal/rpccallerposting/model.go)), and `gocaller`
  ([`direct.go`](../internal/extract/extractors/gocaller/direct.go)) each require a
  **non-empty** `DeclarationLineage` for a `resolved` record and an **empty** one
  for every abstention; `gocaller` rejects a "resolved direct descriptor [that]
  lacks declaration authority." The `__syntax__` sentinel in `direct.go` is the
  direct/syntactic lane's own non-declaration identity; the declaration contract
  neither mints nor joins it.
- **The shared boundary for scoring.** `IMPORT_LINKING_UNAVAILABLE` (§6) is a
  **declaration-side** abstention. T47.2 caller-quality scoring must treat it as an
  explicit counted gap in the declaration denominator — never as a caller miss,
  never as an extraction error, and never as evidence that a caller edge is wrong.
  Symmetrically, a resolved caller edge inherits the provisional scope of the
  declaration lineage it joins: it is exact *for that repo/path declaration set*,
  and it is not a cross-repository, cross-module, or runtime claim.

Because declaration atoms exclude lineage from their fingerprint while caller atoms
include it (§5), this contract is stable under the identity freeze: freezing (A)
changes no caller atom, and any future move to (B) is a caller-side re-key that
must be planned as such.

## 8. Prospective independent scoring (shape only — unsealed)

The frozen surface (§5, §6) is what an independent round would score. The shape
below reuses the sealed V2 label machinery byte-for-byte, exactly as
[ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md) does:
[`spike/t111/label_protocol.py`](../spike/t111/label_protocol.py) governs label
validation, canonical freezing, commitments, and external receipts, and
[`pilot/validation/harness.py`](../pilot/validation/harness.py) supplies the
mechanical candidate projection, deterministic sampling, and Wilson-interval
scoring. A different byte is a different protocol. **This section is a design; it
is not a round, produces no number, and seals nothing.**

The declaration claim families are *complementary to, and non-overlapping with,*
the caller/registration/end-to-end families in the gold protocol, which explicitly
excludes proto field-level lineage. T49.1 owns the declaration families:

- **Exact-emission precision** — for each emitted `DECLARES_*` fact, does the
  cited immutable span in the cited blob contain exactly that declaration, with
  the correct object identity, detail, and cardinality? Site identity is the
  immutable citation coordinate `repository@commit:path:startByte-endByte`.
- **Gap-classification correctness** — for each abstention, is the `Resolution` /
  `Reason` (§6) the correct one, and is a fail-closed condition (§5) correctly a
  run failure rather than a silent subset?
- **Declaration recall** — against a universe enumerated **independently of phebs
  output** (§4-style: `<Gate 0: enumeration method — e.g. exhaustive .proto scan
  with package/message/service/field expansion, tool and version pinned>`), what
  fraction of real declarations in the snapshot did the extractor emit?

Preregistration discipline (mirrors the gold protocol, all values `<Gate 0>` until
sealed): sampling unit and strata (`<Gate 0: e.g. predicate × code_role ×
repository>`, with generated/vendored content isolated so repeated machine
patterns cannot dominate); per-stratum minimum n and margin
(`<Gate 0>`); two-sided Wilson interval at `z = 1.96`; `unsure` labels excluded
from numerator and denominator and always reported; a sampled unit with no
adjudicated label invalidates the round (fail closed, never degrade); one score
per sealed round, no interim looks, and a failed round requires a fresh unseen
round. Blind labeling, custody, commitment, and external receipting happen
**before** phebs predictions are unsealed, per
[PILOT_CHARTER.md](./PILOT_CHARTER.md) Gate 0 and the ceremony rules in
[PILOT_PREREQS.md](./PILOT_PREREQS.md). Results would bind into
[DECISION_RECORDS.md](./DECISION_RECORDS.md).

## 9. Sealing blockers (non-fabricable)

A round under §8 becomes real only when every `<Gate 0>` placeholder is filled and
sealed. Two inputs cannot be manufactured by an agent and are **not** provided
here:

1. **Independent humans.** A validation owner plus at least two blind reviewers
   per sheet and an adjudicator, named and signed per Gate 0. The pack cards record
   "Independent validation owner: none assigned; release-blocking," and the
   PILOT_CHARTER Gate 0 roles are still literal `<name>` placeholders. The
   deferred T47.2b caller-quality protocol is blocked on the same missing humans.
2. **A real public randomness seed.** A NIST beacon pulse URI and its 64-hex
   output for deterministic, third-party-reproducible sampling. The sibling gold
   protocol uses a conspicuous **mock** beacon (`6666…`) and states it does "not
   fill or seal Gate 0." No real pulse is fabricated here.

Also unrun and required before any round: the §8 independent universe enumeration
that fixes the strata and the recall denominators. Until these exist, T49.1's
scoring half stays prospective. This mirrors, and does not relax, the deferral
already recorded for T47.2b.

## 10. What this contract can and cannot produce

**Can.** Freeze the declaration identity as (A) for the pure-reader scope; state
the input boundary that makes (B) underivable today; fix the shared
`DeclarationLineage` join contract with T47.2; freeze the exact `DECLARES_*` fact
surface and gap taxonomy as a scorable target; and define an unsealed, reusable
independent-scoring shape.

**Cannot.** Grant a release, a pack promotion, or any status change; produce an
accuracy, recall, completeness, migration-completion, or decommission-safety
number; authorize or schedule the (B) canonical descriptor/module recipe; change
any token, extractor, schema, or published assertion; reopen `GATE2-V2`; or
substitute for the independent humans and real beacon that §9 requires. The
`phebs.protobuf.contract` pack stays `experimental-dark`; a resolved identity gate
is a design result, not an operating or accuracy result.
