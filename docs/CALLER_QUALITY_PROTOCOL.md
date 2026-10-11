# Caller Map caller-quality protocol — preregistration draft

*Draft artifact for T47.2a. This document grants nothing: no release, no
evidence-status change, no production registration, no Thrift equivalence and
no scale or SLO claim. It becomes binding only when every placeholder is
filled, the approved thresholds and sampling rule are bound, the protocol
digest is recorded and Ben Meddeb seals it. It reuses the mechanical label
and statistics machinery of the statistical accuracy-gold protocol and no pilot, employer or
retained-authorization input: the validation population is personal/public
source selected under T47.2b, and the retained pilot corpus, labels, seeds and
results are neither re-scored nor relabelled.*

## 1. Purpose and lineage

This is the preregistrable form of T47.2's caller-quality validation for the
Epic 47 Caller Map. It freezes one recipe at a time; the first and currently
only candidate is the Go / gRPC-Protobuf committed-index
recipe of §2. The mechanical layers — label validation, canonical freezing,
commitments, external receipts, deterministic sampling, minimum sample sizes
and Wilson intervals — are reused byte-for-byte from
[`spike/t111/label_protocol.py`](../spike/t111/label_protocol.py) and
[`pilot/validation/harness.py`](../pilot/validation/harness.py); their digests
are recorded at sealing, and a different byte is a different protocol. No
pilot authorization, environment, corpus, label sheet, beacon seed or result
is reused, and no old validation receipt (T39.4, GATE2-V2 or any other) is
re-scored, reinterpreted or relabelled under this protocol.

**Prospective amendment (Ben, 2026-10-10).** Ben Meddeb is the sole validation
owner and human label reviewer for this new round. This supersedes the draft's
two-reviewer overlap and separate-adjudicator requirement for this round only.
The source universe remains independent of Phebs predictions, and labels stay
blind to those predictions until commitment. Results must disclose one human
reviewer and cannot claim independent-human validation or inter-reviewer
agreement. Historical protocols and receipts retain their exact bytes.
The admitted population is the three option-A repositories in
[`spike/t472/corpus.lock.json`](../spike/t472/corpus.lock.json). Exact SCIP
symbols are retained, including version skew; affected caller sites stay in
the recall population. This round measures call sites and declaration
attribution. It has no `unit-snapshot.json` and makes no logical-service edge
claim. **Ben approved the numerical thresholds and sampling rule below on
2026-10-10 before scoring.** This approval fixes the bar for this prospective
round; it is not a protocol seal, a passing result or release authorization.
The approved numerical inputs are recorded in
[`spike/t472/validation.parameters.json`](../spike/t472/validation.parameters.json)
for the later immutable preregistration binding; this file contains no score.

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

| Claim | phebs evidence | Metric | Denominator | Source / comparison basis |
|---|---|---|---|---|
| Resolved caller | declaration-lineage-resolved `CALLS_OPERATION` | precision | sealed precision frame (§5) | `invocation` |
| Caller recall | resolution over the independent positive frame | recall | independently enumerated true call sites | `invocation` |
| Abstention correctness | explicit unresolved caller evidence | declared-unresolved accuracy | sampled declared-unresolved frame | independent expected-resolution ledger |
| Processing state | per-unit terminal state | analyzed / excluded / partial + failed rates | independently enumerated eligible units | outcome ledger reconciled to the source census |
| Declaration attribution | resolved caller joined to its declaration | attributed-edge precision/recall | that frame's own precision and recall-positive frames | `operation` plus independent declaration-citation ledger |
| End-to-end service edge | unavailable in this round | unavailable | no `unit-snapshot.json` | unavailable |

The canonical service of an end-to-end edge comes from consumer-unit
attribution, so the end-to-end family is measurable only when the round's
tuple includes `unit-snapshot.json`. A round without it declares the
end-to-end family unavailable before sealing; attributed-edge precision and
recall remain measurable.

The reused label schema has `invocation`, `operation`, `registration`,
`service`, role and source-rationale fields; it has no `attribution` or
`processing_state` decision field. `harness.score_claim` scores only its
original `invocation` and `registration` fields. Before sealing, the
caller-specific projection must bind the separate expected-resolution and
declaration-citation ledgers and eligible-unit/outcome census by digest and
define the exact numerator comparisons. Their Bernoulli counts use the
unchanged Wilson helper; they are never passed to `score_claim` as invented
fields. The corrected projection is preparation machinery, not a claim that the old
harness already measures these families. Each sampled site receives an
independently committed expected-resolution record (`resolved`, `unresolved`,
`not_call` or `unsure`, with a source rationale and the complete expected
operation/reason alternative set for abstention) and, for attribution, an independently committed declaration-citation record.
A resolved declaration record binds the canonical operation, provisional
repo/path lineage and same-repository, same-commit `.proto` citation.
Caller recall uses true invocation labels. Attributed-edge recall uses true
independently resolvable declaration edges and requires both operation and
lineage equality; attributed-edge precision uses all decided claimed sites
and the same equality. Correct negative abstentions never enter either
edge numerator. Missing declarations remain in caller recall and the
resolution/outcome accounting; a zero true-edge denominator is unavailable.
Unresolved accuracy compares the candidate's unresolved decision and complete
set of canonical `(operation, reason)` alternatives with the expected-resolution
ledger, never invocation truth alone or a reason selected by input order.
Resolution records require `site_id`, `state`, `rationale` and `alternatives`;
that last field is a list of unique `{operation, reason}` records, nonempty
for `unresolved`/`not_call` and empty for `resolved`/`unsure`. For a true call,
its blind operation label must belong to the independently expected unresolved
alternatives. These records are constructed from source without predictions.
Production may emit different operations and per-binding override reasons at
one unresolved call: retain every alternative, lineage and citation in one
sampling row per source site. Conflicting duplicates of the same operation,
resolved/unresolved mixtures and conflicting resolved claims still refuse.

The 74 individual service-client interface coordinates are source census
units for preparing and checking the declaration-citation ledger. They retain
repository×code_role strata and the unchanged registration/service label
fields, and do not replace the call-site attributed-edge precision/recall
metrics. Every family/stratum result must match its entire preregistered draw;
all five quality families must be present, including explicit empty-family
unavailability. The uncertainty denominator is the entire sampled sheet before
negative/unsure exclusions. An all-unsure stratum invalidates the round;
missing results refuse. Processing outcomes identify repository, commit and
path explicitly and gate the complete census within each repository×role
stratum; whole-corpus totals cannot rescue a failing stratum.

The approved relaxed thresholds use observed point estimates. Wilson 95% intervals
are reported alongside them; passing does not assert that a confidence bound
meets the threshold. Every applicable measure must pass; a conditional result
requires a new remediation round and grants no release authority. Gates apply
to each nonempty `repository × code_role` stratum for the relevant family;
pooling cannot rescue a failing stratum. Empty frames are reported with count
zero and the corresponding metric unavailable, never as a 100% pass.

| Measure | Denominator | Observed count/rate | Pass threshold | Conditional threshold | Stop threshold |
|---|---|---:|---:|---:|---:|
| Caller precision | sealed precision frame | not scored | ≥ 95% | ≥ 90%, < 95% | < 90% |
| Caller recall | independent true-call frame | not scored | ≥ 80% | ≥ 70%, < 80% | < 70% |
| Abstention accuracy | sampled declared-unresolved frame | not scored | ≥ 85% | ≥ 75%, < 85% | < 75% |
| `analyzed` | independent eligible-unit census | not scored | ≥ 90% | ≥ 80%, < 90% | < 80% |
| `partial + failed` | same eligible-unit census | not scored | ≤ 5% | > 5%, ≤ 10% | > 10% |
| `excluded` | same eligible-unit census | not scored | ≤ 10% | > 10%, ≤ 20% | > 20% |
| Attributed-edge precision | own sealed precision frame | not scored | ≥ 95% | ≥ 90%, < 95% | < 90% |
| Attributed-edge recall | own independent true-edge frame | not scored | ≥ 80% | ≥ 70%, < 80% | < 70% |
| `unsure` / unlabelable | every sampled sheet, before exclusions | not scored | ≤ 10% | > 10%, ≤ 20% | > 20% |
| End-to-end service edge | unavailable | unavailable | unavailable | unavailable | unavailable |

The exact frame files, counts and stratum sizes still must be recorded before
sealing. The observed results above stay unfilled until the sealed round runs;
they are not sealing inputs. No claimed denominator may be omitted, and a
metric family that cannot be measured is declared unavailable. Missing
declarations, version-skewed symbols and unsupported paths remain visible in
the universe and outcome accounting; they cannot be removed to improve a rate.

## 4. Eligible universe (independent of the candidate)

The target population is the frozen corpus's source occurrences under the §2
tuple, enumerated **without consulting phebs output** by the committed tool
`spike/t472/enumerate_universe.py`. At each exact derived commit, Git supplies
every regular Go blob to the standard-library parser in
`spike/t472/gosites/main.go`. It inventories **every Go call expression**,
including bare aliases, wrappers, interface calls, factories and dynamic or
reflected calls, plus references to inventoried RPC methods, full-method
strings and generated-client imports. No import, constructor, method-name or
`client_aware` test excludes a call from recall. Imports alone are explicitly
accounted non-call evidence; all other enumerated kinds remain eligible for
blind source judgment. Parse errors refuse enumeration before publication.

Site IDs use the callee's method/identifier token for ordinary selectors and
bare calls, and the whole callee expression for complex calls. Each source row
also freezes its accepted production citation spans: the token, AST callee
span and typed identifier/dot selector spelling. The caller adapter must join
an exact span in the exact repository/commit/path, preserve code role and
declaration lineage, and normalize `/package.Service/Method` to the unchanged
label spelling `package.Service/Method`. Conflicting resolved claims and inconsistent same-operation duplicates refuse;
explicit unresolved alternatives remain represented at one sampling site. `UNRESOLVED_CALLER` is the supported recipe's
unresolved predicate; adapting it for the frozen harness does not change the
harness bytes or reinterpret the older consumer recipe.

The corrected unsealed outputs in [`spike/t472/universe/`](../spike/t472/universe/)
contain **499,771 sites**, **60 generated files / 74 service-client interfaces**
and **390 operations**. The recall population is **498,793** across all eleven
repository×role strata; only **978 imports** are excluded. These are potential
sites, not manufactured true-call labels. Precision, unresolved and withheld
candidate populations remain pending. Gzip members use zero timestamps, with
exact compressed-byte digests; populations live once in their frame members,
while the plan records counts and digest references.

Every unit receives exactly one terminal processing state; the
analyzed / excluded / partial / failed rates use this denominator. The corpus
repositories, their pinned commits and their license/provenance records are
frozen, and the universe file is committed before any phebs prediction is
unsealed.

The preliminary source-kind census is recorded in
[`spike/t472/source.census.json`](../spike/t472/source.census.json): 7,685
regular Go sources and 11 Go-suffixed symlinks, reconciled directly to the
three exact derived trees. This records source kinds only; it is neither the
true-call frame nor a terminal-state measurement. The enumeration and outcome
rules must account explicitly for the listed nonregular paths rather than
silently treating them as parsed Go source or removing them from accounting.

## 5. Sampling frames

- **Precision frame** — the candidate's emitted resolved assertions for the
  claim family, projected from the sealed proof bundle and joined to the
  source-only canonical coordinate; site identity is `repository@commit:path:startByte-endByte`.
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

## 7. Blind labeling and custody

- Owner and sole human reviewer: **Ben Meddeb**. No reviewer overlap or
  independent adjudicator is required in this prospective round. Uncertainty
  becomes an explicit `unsure` label; it is never invented agreement.
- Ben labels from source only, blind to phebs predictions; sheets carry the
  unchanged label schema and are validated by
  `label_protocol.validate_labels` against the exact sealed sample.
- The completed single-reviewer set is frozen with `freeze_labels`, committed
  with `build_label_commitment`, and externally receipted (GitHub gist + NIST
  beacon, the retained discipline) **before phebs predictions are unsealed**.
- The reviewer assignment records Ben on every sampled site. The existing
  commitment counters `overlap_sites`, `disagreements`, `adjudicated` and
  `unresolved` are all zero because there is no inter-reviewer process;
  `unsure` remains in the label sheets and reporting, separate from those
  counters. The reused machinery is not changed.
- Custody: label sheets and source-only review notes live outside the
  candidate's working tree until commitment; the commitment record names every file
  digest.

## 8. Statistics (preregistered)

- Confidence method: two-sided **Wilson score interval**, `z = 1.96` (95%),
  as implemented in `harness.wilson_interval`.
- Approved sampling rule (Ben, 2026-10-10): `harness.minimum_sample_size(margin=0.10,
  proportion=0.5, z=1.96)` gives **97 sites per nonempty stratum per frame**.
  Larger strata sample 97; smaller strata use a complete census. This is a
  planning margin, not a guarantee about a near-threshold result. A small
  census supports only its frozen finite population; no broader inference is
  made from it. Counts and the census flag are recorded before sealing.
- Multiplicity: each §3 claim family is scored once per sealed round; no
  interim looks, no re-rolls; a failed round's remediation requires a fresh
  unseen round.
- Missing/unlabelable: `unsure` labels are excluded from numerator and
  denominator and always reported (`harness.score_claim`), with the §3 cap
  applied before exclusions. A missing label or an all-`unsure` sampled
  stratum invalidates the round; neither can produce a pass.
- Stop rules: a stop-threshold result, missing required frame or label,
  incomplete planned sample/census, changed input or tool identity, corrupted
  commitment, or premature prediction disclosure stops the round. The full
  planned sample is labeled and scored once without interim quality looks;
  no extra sample or threshold adjustment rescues that round.

## 9. Scoring and reporting

Every reported rate carries: count, denominator, point estimate, Wilson
bounds where the denominator is positive (otherwise unavailable), stratum
breakdown, and the error-taxonomy tally — same-name unrelated
methods, imports/aliases, wrappers/interfaces, dynamic paths,
generated/vendored/test code, excluded/failed paths, ambiguous/missing
attribution. Results bind card, manifest, implementation/binary, input and
validation digests, and the admitted recipe is recorded as a caller-specific
result; it becomes release input only through T47.5.

## 10. Sealing checklist

1. The frozen corpus with license/provenance records, and the independent
   universe enumeration (§4) — T47.2b.
2. Every remaining `<T47.2b>` placeholder, exact frame count and stratum size;
   missing sealing inputs block sealing and scoring. The §4 enumeration
   placeholder is filled by the committed enumeration (see §4); frame counts
   and stratum sizes are recorded in
   [`spike/t472/frames/frames.plan.json`](../spike/t472/frames/frames.plan.json),
   and every not-yet-known sealing input is named in
   [`spike/t472/bundle/preregistration.bundle.json`](../spike/t472/bundle/preregistration.bundle.json)
   as an explicit missing binding.
3. Approval is complete: Ben approved §3 thresholds and §8 sampling on
   2026-10-10. Bind those exact parameters into the preregistration record;
   do not adjust them after results. Ben is the sole assigned owner/reviewer;
   no additional human label reviewer is requested.
4. Machinery digests, public randomness seed and card/manifest/binary/input
   digests recorded in the commitment record.
5. A corpus derivation that passes the T47.2b re-derivation checks in
   [`spike/t472/README.md`](../spike/t472/README.md): recorded Go environment
   and resolution tables, every non-test-main build-cache drop listed, and the
   version-skewed in-repo reference count reviewed. The corrected run records
   6,209 references across all symbol kinds, not 6,209 missed RPC callers.
   Ben selected exact-symbol preservation with caller gaps measured in scope.
   All 373 listed hashed-cache document drops were classified from retained
   source bytes as synthesized `go test` mains in
   [`spike/t472/build-cache-classification.json`](../spike/t472/build-cache-classification.json).
   They lie outside the pinned Git trees and remove no eligible in-tree source
   unit; the independent universe still must reconcile every in-tree source.
6. `phebs.grpc.caller.go` remains `experimental-dark` until T47.5 promotes
   the exact validated artifact; an unfilled gate leaves this recipe
   unavailable.
7. The caller-specific label projection, resolution/attribution ledgers and
   state census of §3, including tested numerator and denominator rules —
   implemented in
   [`spike/t472/caller_scoring.py`](../spike/t472/caller_scoring.py) and
   exercised by
   [`spike/t472/test_universe_frames.py`](../spike/t472/test_universe_frames.py);
   the resolution and declaration-citation ledgers remain named missing inputs,
   and the bundle refuses missing structure, malformed digests and stale
   file/provenance identities;
   the eligible-unit ledger consumes only explicit per-source outcome records
   from the sealed run receipt and reconciles fail-closed to the committed
   universe census. The projection is prepared, not executed.

## 11. What this protocol can and cannot produce

A sealed passing round meeting its approved sampling rule produces
single-human-reviewed evidence toward admitting the one §2 recipe, with its
reported uncertainty. It cannot: claim independent-human validation; admit
the Go/Thrift recipe or any other recipe; establish runtime execution, deployment
reachability, current ownership, a complete caller inventory, migration
completion or decommission safety; replace the T47.4 security, lifecycle and
operating acceptance; release anything (T47.5 owns promotion through a
caller-specific release record); re-score or relabel any historical receipt;
or reuse pilot/employer authorization. No intermediate result, trend or near
miss opens anything, and one recipe's result is never inferred onto another.
