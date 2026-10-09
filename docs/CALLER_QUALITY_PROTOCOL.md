# Caller Map caller-quality protocol — preregistration draft

*Draft artifact for T47.2a. This document grants nothing: no release, no
evidence-status change, no production registration, no Thrift equivalence and
no scale or SLO claim. It becomes binding only when every placeholder is
filled, the protocol digest is recorded and the independent validation owner
named in T47.2b seals it. It reuses the mechanical label and statistics
machinery of the statistical accuracy-gold protocol and no pilot, employer or
retained-authorization input: the validation population is personal/public
source selected under T47.2b, and the retained pilot corpus, labels, seeds and
results are neither re-scored nor relabelled.*

## 1. Purpose and lineage

This is the preregistrable form of T47.2's independent caller-quality
validation for the Epic 47 Caller Map. It freezes one recipe at a time; the
first and currently only candidate is the Go / gRPC-Protobuf committed-index
recipe of §2. The mechanical layers — label validation, canonical freezing,
commitments, external receipts, deterministic sampling, minimum sample sizes
and Wilson intervals — are reused byte-for-byte from
[`spike/t111/label_protocol.py`](../spike/t111/label_protocol.py) and
[`pilot/validation/harness.py`](../pilot/validation/harness.py); their digests
are recorded at sealing, and a different byte is a different protocol. No
pilot authorization, environment, corpus, label sheet, beacon seed or result
is reused, and no old validation receipt (T39.4, GATE2-V2 or any other) is
re-scored, reinterpreted or relabelled under this protocol.

## 2. Frozen supported tuple (T47.2a)

| Input | Value |
|---|---|
| Language / protocol | Go callers of gRPC-Protobuf operations |
| Generated client | Checked-in `*_grpc.pb.go` with the unique `Code generated ... DO NOT EDIT` header; mock and vendored copies follow §6 |
| Declaration identity | The repository/path declaration lineage defined by [PROTOBUF_DECLARATION_IDENTITY.md](./PROTOBUF_DECLARATION_IDENTITY.md) §7, within its single-declaration-repository scope: a caller joins only a `.proto` declaration committed in the same repository. It is not canonical descriptor or module lineage, which is not guessed here |
| Input format | Repository-committed at the exact commit: root `index.scip` (≤ 64 MiB), `layout-snapshot.json` (`t20-layout-snapshot-v1`), `generated-from-snapshot.json` (`t20-generated-from-v1`), optionally `unit-snapshot.json` (`t20-unit-snapshot-v1`), plus the checked-in declarations and generated clients; corpus inventory ≤ 200,000 files and every source blob ≤ 10 MiB, measured on the tree phebs reads (`index.scip` is bounded separately) |
| Extractor | `grpc-caller` 1.5.0, schema `t20-caller-v1`; a resolved occurrence requires the SCIP call edge, generated-client wire operation and declaration attribution to agree on exactly one lineage and operation |
| Managed-generated inputs | Not consumed. The committed-index recipe reads only repository-committed bytes; a recipe that consumes managed-generated SCIP evidence would require its prospective T45.9 admission and a same-change PLAN decision before it may be scored. When the validation corpus's committed index is evaluator-derived (T47.2b), it stands in for an owner-committed index and is admissible only with its complete recipe, Go environment, tool digests and input tables recorded |

The declaration token follows the T49.1 contract: a canonical
descriptor/module identity arrives as a separate prefix-disjoint family under
its own ticket, never a silent rename, and T47.3 owns only the Caller Map's
compatibility handling of such a family. A different tuple is a different
recipe and needs its own preregistration.

## 3. Claim families and metrics

| Claim | phebs evidence | Metric | Denominator | Label decision field |
|---|---|---|---|---|
| Resolved caller | declaration-lineage-resolved `CALLS_OPERATION` | precision | sealed precision frame (§5) | `invocation` |
| Caller recall | resolution over the independent positive frame | recall | independently enumerated true call sites | `invocation` |
| Abstention correctness | explicit unresolved caller evidence | declared-unresolved accuracy | sampled abstention frame | `invocation` (unresolved) |
| Processing state | per-unit terminal state | analyzed / excluded / partial + failed rates | independently enumerated eligible units | `processing_state` |
| Attribution and end-to-end | resolved caller joined to its declaration | attributed-edge and `(canonical service, operation)` edge precision/recall | that frame's own precision and recall-positive frames | `attribution` |

The canonical service of an end-to-end edge comes from consumer-unit
attribution, so the end-to-end family is measurable only when the round's
tuple includes `unit-snapshot.json`. A round without it declares the
end-to-end family unavailable before sealing; attributed-edge precision and
recall remain measurable.

Thresholds are prospective and filled before scoring:

| Measure | Denominator | Observed count/rate | Pass threshold | Conditional threshold | Stop threshold |
|---|---|---:|---:|---:|---:|
| Caller precision | `<T47.2b: sealed frame>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| Caller recall | `<T47.2b: independent frame>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| Abstention accuracy | `<T47.2b: sampled abstentions>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| `analyzed` | `<T47.2b: eligible units>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| `partial + failed` | `<same denominator>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| `excluded` | `<same denominator>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| Attributed edge | `<T47.2b: that frame>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |
| End-to-end edge | `<T47.2b: that frame>` | | `<T47.2b>` | `<T47.2b>` | `<T47.2b>` |

Blanks block sealing and scoring; no claimed denominator may be omitted, and
a metric family that cannot be measured for the recipe is declared
unavailable rather than estimated.

## 4. Eligible universe (independent of the candidate)

The target population is the frozen corpus's source occurrences under the §2
tuple, enumerated **without consulting phebs output** by
`<T47.2b: enumeration method — e.g. exhaustive scan for uses of admitted
generated clients plus declaration operation inventory, tools and versions
pinned>`. Every unit receives exactly one terminal processing state; the
analyzed / excluded / partial / failed rates use this denominator. The corpus
repositories, their pinned commits and their license/provenance records are
frozen, and the universe file is committed before any phebs prediction is
unsealed.

## 5. Sampling frames

- **Precision frame** — the candidate's emitted resolved assertions for the
  claim family, projected from the sealed proof bundle; site identity is the
  immutable citation coordinate `repository@commit:path:startByte-endByte`.
- **Recall-positive frame** — true caller sites constructed independently of
  the candidate from the §4 enumeration, covering direct calls,
  imports/aliases, wrappers/interfaces and acknowledged dynamic paths.
- **Abstention frame** — declared unresolved states, sampled and labeled for
  correct abstention versus wrong abstention (a resolvable caller reported
  unresolved).
- **End-to-end frame** — its own independently constructed precision and
  recall-positive frames over `(canonical service, operation)` edges.
  Multiplying call-site recall by attribution coverage is diagnostic only.

## 6. Sampling unit, strata, and anti-domination

The sampling unit is one site ID as defined in §5. Strata are
`repository × code_role` with `code_role ∈ {production, test, generated, mock,
vendor}`, the exact roles `grpc-caller` assigns; the independent frames assign
the same roles by the same path and header rules. The collapse rule from the accuracy-gold
protocol carries over: occurrences inside generated, vendored or wrapper
files are sampled as their own strata so repeated machine-produced patterns
cannot dominate any estimate, and each of the T47.2 label categories —
resolved callers, same-name unrelated methods, imports/aliases,
wrappers/interfaces/dynamic paths, generated/vendored/test code,
excluded/failed paths, ambiguous/missing attribution — is represented in the
error taxonomy of §9 even when a stratum is small. Per-stratum sizes are
computed with `harness.minimum_sample_size` and recorded before sealing;
selection is `harness.stratified_sample` under the sealed seed,
deterministic and third-party reproducible.

## 7. Blind labeling, custody, and adjudication

- Reviewers: `<T47.2b: at least two named reviewers per sheet>`, overlap
  `<T47.2b: ≥ 2 on every sampled unit>`, adjudicator `<T47.2b>`.
- Reviewers label from source only, blind to phebs predictions and to each
  other; sheets carry the label schema and are validated by
  `label_protocol.validate_labels` against the exact sealed sample.
- The adjudicated set is frozen with `freeze_labels`, committed with
  `build_label_commitment`, and externally receipted (GitHub gist + NIST
  beacon, the retained discipline) **before phebs predictions are unsealed**.
- Custody: label sheets and adjudication notes live outside the candidate's
  working tree until commitment; the commitment record names every file
  digest.

## 8. Statistics (preregistered)

- Confidence method: two-sided **Wilson score interval**, `z = 1.96` (95%),
  as implemented in `harness.wilson_interval`.
- Power / minimum n: per-stratum sizes from `harness.minimum_sample_size`
  with margin `<T47.2b>` at the threshold-relevant proportion; an undersized
  stratum makes the round underpowered and no claim issues.
- Multiplicity: each §3 claim family is scored once per sealed round; no
  interim looks, no re-rolls; a failed round's remediation requires a fresh
  unseen round.
- Missing/unlabelable: `unsure` labels are excluded from numerator and
  denominator and always reported (`harness.score_claim`); a sampled unit
  with no adjudicated label invalidates the round — the harness fails closed
  rather than degrading.
- Stop rules: `<T47.2b: e.g. stop at a stop threshold, an underpowered
  stratum, or a corrupted commitment rather than continuing>`.

## 9. Scoring and reporting

Every reported number carries: count, denominator, point estimate, Wilson
bounds, stratum breakdown, and the error-taxonomy tally — same-name unrelated
methods, imports/aliases, wrappers/interfaces, dynamic paths,
generated/vendored/test code, excluded/failed paths, ambiguous/missing
attribution. Results bind card, manifest, implementation/binary, input and
validation digests, and the admitted recipe is recorded as a caller-specific
result; it becomes release input only through T47.5.

## 10. Open items that block sealing

1. The frozen corpus with license/provenance records, and the independent
   universe enumeration (§4) — T47.2b.
2. Every `<T47.2b>` placeholder above; any blank blocks sealing and scoring.
3. The independent validation owner and reviewers — the caller card
   currently records "none assigned; release-blocking".
4. Machinery digests, public randomness seed and card/manifest/binary/input
   digests recorded in the commitment record.
5. A corpus derivation that passes the T47.2b re-derivation checks in
   [`spike/t472/README.md`](../spike/t472/README.md): recorded Go environment
   and resolution tables, every non-test-main build-cache drop listed, and the
   version-skewed in-repo reference count reviewed.
6. `phebs.grpc.caller.go` remains `experimental-dark` until T47.5 promotes
   the exact validated artifact; an unfilled gate leaves this recipe
   unavailable.

## 11. What this protocol can and cannot produce

A sealed, adequately powered passing round under this protocol produces
evidence toward admitting the one §2 recipe. It cannot: admit the Go/Thrift
recipe or any other recipe; establish runtime execution, deployment
reachability, current ownership, a complete caller inventory, migration
completion or decommission safety; replace the T47.4 security, lifecycle and
operating acceptance; release anything (T47.5 owns promotion through a
caller-specific release record); re-score or relabel any historical receipt;
or reuse pilot/employer authorization. No intermediate result, trend or near
miss opens anything, and one recipe's result is never inferred onto another.
