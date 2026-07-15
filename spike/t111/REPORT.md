# T11.1 validation rerun — Attempt 3 sealed result

**Date:** 2026-07-14 · **Branch:** `t11.1-gate-revalidation` · **Status:**
**STOP / NOT ESTABLISHED**

Attempt 3 completed its independent source-only review and public label freeze,
but the single permitted holdout-score execution failed closed before emitting
any metric. The frozen population contains 5,744 canonical labels
(`sha256:56bc44d41d44b4adecaa1284db8bd24cde1ae4cf034cbac4893c78e81a95e034`).
The two reviewers agreed on all 577 overlap sites, neither returned `unsure`,
and the final adjudication counters are 577 overlap, 0 disagreements,
0 adjudicated, and 0 unresolved.

The hash-only label commitment was published as a new initial
[GitHub Gist](https://gist.github.com/bmeddeb/896c13b3e7f6e6d99e207198a2523cc7),
revision `1014884987f1e5b8fd8ae40125f0fcee0d2f5caa`; the scorer's pre-key
publication verification passed. The one-shot execution then returned exit 2:

```text
score: NOT ESTABLISHED: frame recomputation failed: go toolchain differs from
the fact producer: version='go version go1.26.4 darwin/arm64'
digest=sha256:e61851b2c0cde9b9ac4ae044fcce6ba9d55cb15f98ded3964793acaa8632242f
```

The sealed facts require the exact Go 1.26.5 binary recorded by the fact
producer. The scoring shell resolved `go` from the module toolchain cache at
1.26.4 instead of the documented Homebrew 1.26.5 path. No confidence bound or
role result was produced, so Attempt 3 supports no performance claim. Because
the scorer loaded `key.jsonl` before frame recomputation, the fixed one-score
rule makes the attempt terminal: it must not be rerun with a corrected PATH.
The private mode-0600 execution receipt is
`sha256:8b686e0201092622a8a057ab29e5ee225e00dd9b12b50b7f7994a168d8544883`.

The next-attempt scorer now has a reusable `--preflight-toolchain` command and
repeats the same exact-binary attestation before opening `key.jsonl`; a
regression test proves a mismatch cannot call the hidden-key loader. This is a
prospective protocol correction only. Establishing Gate 2 still requires a new
official-head lineage, full append-only burn carry-forward, fresh public input
and label commitments, independent review, and one new score. Epics 13–15
remain gated; Epic 12 remains dark-only under its existing carve-out.

---

# T11.1 validation rerun — Attempt 2 sealed result

**Date:** 2026-07-14 · **Branch:** `t11.1-gate-revalidation` · **Status:**
**STOP / NOT ESTABLISHED**

The corrected Gate 2 v3 protocol completed one sealed, publicly committed
holdout evaluation over 3,028 frozen labels. Every exact confidence bound
passed:

- client calls: 100.00% point precision, 98.77% lower bound; 98.57%
  population recall, 95.93% lower bound; every fixture above 98.51%;
- registrations: 99.21% precision and lower bound; 93.33% population recall
  and lower bound; every fixture above 98.50%;
- benchmark support: 1,266 positives and 148 hard negatives; and
- blind holdout: 75.46% against the 30% minimum.

Gate 2 is nevertheless **NOT ESTABLISHED** because role classification is an
exact all-cohort requirement: `generated` 38/38, `mock` 1/1, `production`
249/249, `vendor` 21/21, but `test` 148/149. The one-score rule forbids
relabeling or rescoring this attempt.

The burned-coordinate postmortem isolated the miss to reusable test support
under an exact `testing` path segment. Both the producer and independent source
classifier recognized only `tests` and `testdata`, emitted `production`, and
therefore shared the same blind spot. The human source label `test` was
correct. Extractor `spike-0.5.1` prospectively adds the exact `testing` segment,
bumps the evidence version, and adds matching Go/Python regressions and explicit
reviewer guidance. Proving that correction requires a new official-head
four-commit lineage, full burn-ledger carry-forward, a fresh public seed and
labels, and one new score. Attempt 2 remains immutable. See
`spike/t111/labeling/ATTEMPTS.md` for public commitments and lineage.

Epics 13–15 remain gated.

---

# T11.1 validation rerun — prior outcome retracted

**Date:** 2026-07-12 · **Branch:** `t11.1-validation-redo` · **Status:**
**STOP / NOT ESTABLISHED**

This branch supersedes the completion claim below. The first review found that
the recorded measurements did not prove the ticket's stated populations and
that the Gate-1 harness could publish degraded loads as replacement truth.
Until fresh, independently labeled artifacts pass the corrected protocols,
Epics 12–15 remain gated.

| Gate | Current status | Why the prior result cannot be used |
|---|---|---|
| 1 — Evidence integrity | **NOT ESTABLISHED** | The old harness checked only `HEAD`, accepted a dirty corpus, published partial module loads, used non-atomic final-path writes, and had no exercised permission/principal path. The v3 harness is hardened and unit-tested, but a complete corpus rerun and permission-safe product path have not passed. |
| 2 — Operation extraction | **NOT ESTABLISHED** | The reported 98.4% recall was unweighted recall inside an enriched case-control sample, not recall over the eligible population. The replacement probability-frame protocol requires a fresh blind holdout; the legacy labels are development history only. Registration and role-classification coverage must also be measured. |
| 3 — Service/deployable identity | **NOT PASSED** | The original measurement was already below the 99% threshold. Its scorer measured sampled assertions, not the required pairwise merge population plus caller-deployable → operation edges. No retry or relabeling turns that result into a pass. |
| 4 — Field references | **NOT ESTABLISHED** | The prior sample measured emitted-assertion precision only. It did not measure recall over a SCIP-eligible population or prove canonical `(contract_lineage_id, message_full_name, field_number)` identity across two consumer dependency versions. The current producer does not emit that canonical identity. |

Per the ticket's exit rule, Gate 1 or Gate 2 being unestablished is a stop,
not permission to continue downstream. A new outcome ADR may be written only
after the corrected artifacts, labels, scorers, and immutable corpus inputs
are committed and independently reproducible.

## Rerun protocol

1. Preserve the legacy JSONL only as disclosed development history; do not
   reuse it as a blind holdout.
2. Generate coordinate-only artifacts from exact Git objects at the locked
   commits. Keep labeler context local and ignored.
3. Bind each benchmark generation to corpus, extractor, configuration, fact,
   script, population, sample, and artifact digests. Any mismatch fails closed.
4. Tune only on development data. Freeze and label the holdout once. Exposed
   coordinates are burned for blindness, but they remain part of the AC target
   population: a replacement decision must either refresh the locked corpus or
   combine their prior outcomes in a reviewed estimator. Silently dropping
   known coordinates from numerator and denominator is forbidden.
5. Report `NOT ESTABLISHED` when the producer schema, benchmark population,
   sample size, classification cohort, or lineage evidence is incomplete.

## Rerun verification to date

- The v3 harness unit tests and `go vet ./spike/t111` pass.
- A clean-room Online Boutique run emitted and Git-object-verified 361 operation
  facts, 155 identity facts, and 564 field-reference facts. Every external Go
  module used for type-derived facts was rehashed against the pinned snapshot's
  `go.sum`; the verified semantic-input set is recorded in each such atom. Two
  complete operation reruns were byte-identical (`sha256:78ea786aa5b66ea416c7df844a2ca1fc659e5a5783021b18e68597e6b56d3e57`).
- Dapr's full operation run found 220 package-load errors and published no
  partial replacement. Its declared-plane diagnostic is kept under a separate
  filename and cannot overwrite a complete operation result.
- Temporal stops on unpinned gitlink
  `develop/docker-compose/grafana/provisioning/temporalio-dashboards`; Loki
  stops on unpinned gitlink `operator/website/themes/doks`. These recursive
  inputs must be separately locked or explicitly excluded by a reviewed
  coverage policy before either corpus can be measured.
- Gate 2 remains blocked by the absent independent `IMPLEMENTS_SERVICE`
  registration cohort; sealed generation now stops before selecting or
  exposing a holdout. Gate 3 remains blocked by the absent
  deployable-to-operation cohort. Gate 4 remains blocked because the producer
  has no explicit canonical field identity. Gate 3/4 generation likewise
  checks these capabilities before writing any benchmark artifact.
- The 700 exposed legacy coordinates make the current pinned population
  ineligible for another blind gate sample. Diagnostic sampling may exclude
  them, but cannot establish an AC pass; a future gate needs refreshed locked
  revisions or a reviewed estimator that incorporates the prior outcomes.
  The former "holdout-2" retry path is disabled for the same reason.

---

## Superseded historical report

The text below records what the first run claimed. It is retained for audit
history and must not be cited as the current EPIC 11 decision.

# T11.1 spike report — SUPERSEDED

**Date:** 2026-07-12 · **Branch:** `t11.1-consumer-evidence-spike` · corpus pinned in `corpus.lock.json`.

## Exit decision (per the ticket's outcome table)

**Operation extraction passes; identity fails as specified** →
**the wedge ships with consumers grouped by repository/build target.**
Epics 12–15 are unblocked in that configuration. Deployable-identity work
returns as an explicit Epic-12+ decision: either real helm rendering or
template-scanner abstention-by-default, re-validated on a fresh sample then —
not retried now.

## Gate status

| Gate | Status | Result |
|---|---|---|
| 1 — Evidence integrity | **Partially proven** | Deterministic atom IDs unit-tested (injective length-delimited encoding); atomic publish by construction; 100% of ~770 labeled sites resolved to exact repo/commit/file/span from the evidence alone. **Deferred to Epic 12 integration:** permission-filtering-before-aggregation and name-leak checks — the spike harness has no principal model; design-carried only (visibility_scope field present). |
| 2 — Operation extraction | **PASS** | Holdout (frozen, once): 98.4% raw / **100% adjudicated precision**, **98.4% recall**, role accuracy 100%, worst fixture ≥94.1% raw. |
| 3 — Service/deployable identity | **NOT PASSED as specified** | Two frozen holdouts: 98.1% and 98.0% pairwise vs the ≥99% bar. Every failure in one component — the render-free helm-template scanner (a `$patch` file, an empty-image partial patch, a `{{ if }}`-guarded image line resolved without control-flow evaluation). All other components — import-closure reachability, strict K8s manifests, skaffold joins, `service.name` literals — **100% across all rounds (117 verified merges, zero false)**. Abstention machinery worked as designed (106 unresolved identities correctly withheld). |
| 4 — Field references | **PASS** | Dev 100% (81/81 incl. adjudications); holdout (frozen, once) **100% (29/29)**; access-kind classification 108/108 across rounds. |

## Gate 3/4 detail

Benchmark: 243 assertions sampled (132 identity joins, 111 field refs), 171
dev / 72 holdout, 10 blind verifiers; plus a fresh 56-site G3 holdout-2 with
3 new verifiers after the first holdout surfaced a fix (protocol: a
holdout-discovered bug burns the holdout; fixes land, a *fresh independent
sample* is scored once; after the second miss we stop — no third sample).

Dev-driven fixes (legitimate tuning): tier propagation through reachability
composition (weakest link wins); kustomize `$patch: delete` files no longer
assert workload existence; **declared-identity joins gated to in-repo message
types**. The last one was proven in the wild during holdout adjudication:
`WorkerDeploymentVersion` exists both in-repo (deployment_name=1, build_id=2)
and in the external `go.temporal.io/api` module (**build_id=1,
deployment_name=2 — the exact opposite wire numbering**). Same name, same
fields, different contracts — the contract-lineage key
`(contract_lineage_id, message_full_name, field_number)` is not theoretical.

G4 evidence at scale: ~152k `REFERENCES_PROTO_FIELD` facts across the corpus
(temporal ~97k, loki ~35k, dapr ~19k, boutique 564), each with access kind
(getter_read/field_read/field_write/construct) and wire-true field numbers
from protobuf struct tags — tags work even for export-data-loaded dependency
modules.

## Gate 2 numbers

Methodology: extractor-independent candidate superset from proto-declared RPC
names (grep incl. wrapped-call continuation lines); 478 sites = 220 extracted
positives (sampled from facts, stratified by system × code_role) + 258
non-extracted candidates (stratified 2:1 toward pb-adjacent paths); 335 dev /
143 holdout split at seed 111; 20 independent labelers (blind to extractor
output, corpus read access, receiver-type chasing required); 476/478 labeled,
0 unsure. Tuning on dev only; holdout scored once, frozen.

**Dev (after two dev-driven fixes: tests/-segment role classification;
embedded-interface receiver resolution with ambiguity abstention):**
precision 100% (159/159), sample recall 100% (159/159), role accuracy 100%,
per-fixture precision 100% ×4. 5 stub-internal sites excluded by definition
(see below).

**Holdout (single frozen evaluation):**

| | precision | sample recall | role acc | tp/fp/fn/tn |
|---|---|---|---|---|
| raw | 98.4% | 98.4% | 100% | 60/1/1/77 |
| after adjudication | **100%** (61/61) | **98.4%** (61/62) | 100% | 61/0/1/77 |

Adjudication (evidence-based, applied once): the single raw "fp"
(`loki:pkg/indexgateway/client.go:321`) was a labeler error — the labeler
reported proto package `logproto` (the *Go* package from `option go_package`);
`pkg/logproto/indexgateway.proto:3` declares `package indexgatewaypb` and the
generated wire literal is `/indexgatewaypb.IndexGateway/GetStats`, exactly what
the extractor emitted. The extractor's answer is the wire truth.

Gate thresholds: ≥98% precision (met raw and adjudicated), ≥90% recall (met),
≥90% precision within every fixture (met: worst fixture 94.1% raw, 100%
adjudicated). **Gate 2 passes.**

## Population definitions fixed during dev (documented, not post-hoc)

- **Stub internals excluded:** `cc.Invoke` inside generated `*.pb.go` stub
  method bodies is the operation's own plumbing, not a consumer call site.
  Counting them would double-count every operation. Reported separately
  (5 dev / 2 holdout sites).
- **Consumer-eligible roles:** facts carry
  vendor/mock/generated/test/production classification; temporal's generated
  wrapper layers (853 call sites) are correctly separated as `generated` so
  consumer queries can exclude wrapper internals.

## Known coverage boundaries (the honest-recall ledger)

1. **Wrapper indirection:** calls through hand-written wrappers that do NOT
   embed/implement the generated client interface are not extracted (loki's
   dskit `httpgrpc` tunnel hides frontend↔querier entirely). Tier `heuristic`
   exists for client-shaped hand-written interfaces (temporal DI layer, 54
   sites); embedding interfaces resolve when unambiguous, abstain otherwise.
2. **Vendored dependency trees:** module-mode loading bypasses committed
   `vendor/` sources — the one true holdout miss (etcd client call inside
   loki's vendor tree). Line item for the coverage manifest, not a wrong edge.
3. **External-module contracts:** operations declared in dependency modules
   (temporal's `go.temporal.io/api`, ~811 call sites) are extracted as calls
   but have no in-repo DECLARES facts — cross-module lineage is Epic 12's
   linker.
4. **Dynamic registration:** temporal's chasm registry
   (`RegisterServices(s.server)`) and dapr's
   `workflowEngine.RegisterGrpcServer` have no `RegisterXServer` literal —
   IMPLEMENTS recall boundary, not measured by this gate.
5. **Test-variant load errors:** dapr's `unit`-build-tag mock package fails
   test-variant type-checking (LOAD_ERRORS fact recorded; production facts
   unaffected).

## Corpus (pinned)

| System | Commit | Hard property it contributes |
|---|---|---|
| online-boutique | `ea0fcf42` | per-service copied generated protos; polyglot eligibility boundary; documented architecture = ground truth (all 19 Go edges recovered exactly) |
| dapr | `1991f948` | 6 deployables one repo; shared-lib call attribution (gate 3); generator-vintage diversity incl. legacy embedded stubs; sidecar identity inversion |
| temporal | `1d4be84b` | 3-layer generated wrapper chain; stub burial in connection pools; dynamic registration; helm in separate repo |
| loki | `fbf98eb4` | single binary → ~20 workload identities via `-target`; gogo codegen; committed vendor/; cross-module alias re-exports; pool + type-assertion call sites |

## Corpus additions since the Gate-2 report

`temporal-helm` (temporalio/helm-charts @ `9f4d328c`) as the fifth,
manifest-only entry — the cross-repo identity case. Its computed helm names
(`{{ include "temporal.fullname" }}`) all abstained without rendering, as
expected; this is the concrete evidence that the deployable-identity claim
needs real template evaluation, not shortcuts.

## Next (Epics 12–15, per the exit decision)

1. Epic 12 productizes the evidence schema + proto facts on the phebs job
   chassis; the permission-before-aggregation Gate-1 criterion is exercised
   there (the spike carried the design only).
2. Consumers are grouped by repository/build target — no deployable identity
   claims in the wedge.
3. Deployable identity returns behind its own gate when template handling is
   real (render or abstain-by-default), validated on a fresh sample.
4. Coverage-manifest line items carried forward: vendored dependency trees,
   external-module contract lineage (the linker), wrapper indirection beyond
   interface embedding, dynamic registration.
