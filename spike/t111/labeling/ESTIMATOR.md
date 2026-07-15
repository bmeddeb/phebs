# T11.1 Gate 2 frozen-label estimator proposal

**Date:** 2026-07-15 · **Status:** pending human review — no estimator scoring
executed

This document prospectively specifies a possible alternative to the stopped
Attempt 5 benchmark expansion. It does not authorize an execution. Attempt 3
remains terminal under its original one-score rule; the proposal is a new,
separately named estimator authorization over immutable Attempt-3 inputs, not a
rerun, rescore, or rehabilitation of Attempt 3.

Authoring and sealing this proposal invoked no extractor, labeler, scorer,
sample, commitment, entropy source, or network ceremony. No census site or burn
is removed, and the 30% blind floor and every accuracy/confidence threshold
remain unchanged.

## Decision requested from human reviewers

The frozen labels are conditionally reusable for one mechanical score of the
exact Attempt-3 frame. The condition is that reviewers accept both of these
premises:

1. the failed Attempt-3 process disclosed no correctness result through its
   recorded output or timing; and
2. no person with access to the private key and labels joined them out of band
   or used hidden correctness information to tune the sealed Attempt-3
   extractor, choose a threshold, or alter this estimator.

The local artifacts strongly support the first premise but cannot prove the
second because private-file access was not independently audited. Human
acceptance and an explicit no-access/no-tuning attestation are therefore
mandatory. If either premise is rejected, the frozen-label path stops without
execution; the fallback for review is described below.

## Immutable estimand and inputs

The estimator measures only extractor `spike-0.5.1` on the exact Attempt-3
Gate-2 frame. The source lineage is
`sha256:2d7bab803cf20c36e738534dd73018ecca96e9f87922bf96e7d66a1bbe346cbf`:

| Fixture | Exact commit |
|---|---|
| `online-boutique` | `9a4616e77f0f9cbcbecaf27d711c38890dda1404` |
| `dapr` | `08aebd8b2effa2ed939ad5531e25ff8b21a36ef1` |
| `temporal` | `a5e6d3ed6335256319fff94f38bf74c4b7ba370c` |
| `loki` | `d108ea11a62fbf7be7d25b58d44d396a3ce0c96c` |

The Temporal Helm companion remains fixed at
`9f4d328c31c77c323d272d0c5f615cf02bd46dab`; it is context, not a fifth
scored fixture.

The new estimator authorization must bind all of the following before any
execution:

- Attempt-3 input binding
  `sha256:9164040c050299408b87903a2befdec976bf1a77a38c3bdfd74c77a3d05e5496`;
- sealed artifact manifest
  `sha256:78336c1f04b7055d7d2ecb7d34ba18291aa26443c81c28de38fde36e5615aa6e`;
- hidden key
  `sha256:1a45ab1df82d89101a1ab75303a86df03cef5c8897b9da42cba75b043a22369f`;
- corpus-lock input
  `sha256:a3d52ba715d60ca4f966e5376fc551d49a325380e1112af005e7c2939e4ab7b4`;
- historical fact files: `online-boutique`
  `sha256:27d1fcae10b0199ce6548f3bbe91beaa1a6390db879fd779c005302dcce8f4c0`,
  `dapr`
  `sha256:fb4f412ad5317e49f3a73ff29b98db4a85f844e471f6d47060111490677cfa34`,
  `temporal`
  `sha256:62d8c2d7f5a19992a884d4d2390fd8f5f3db3104419b9b9443e3dfcd3a3640a6`,
  and `loki`
  `sha256:03ededa3c8a532e8ee8be0176d24a569d81373b639d4367929f470373c2f7398`;
- preparation script
  `sha256:9a8a634b6be7029f3d9eb82ec84fe26d17681d129caae6dde87a613d6503a209`,
  fact verifier
  `sha256:1a76c31e94bb69aeead036c5177f67e50615fd4fa12c92dfac17a321e63f4eb7`,
  typed oracle
  `sha256:6430827e04571de54afa23f3f7f5198393c40c33c14e5a96317696b77cfd61a7`,
  and every other manifest artifact digest;
- 5,744 canonical labels with file digest
  `sha256:56bc44d41d44b4adecaa1284db8bd24cde1ae4cf034cbac4893c78e81a95e034`;
- label-commitment document
  `sha256:76ffa1ca8ae366898410198888b9b2aa6e2f51c3a5c1c46050380eb85ae2a1e2`;
- reviewer assignment
  `sha256:cb767b2381decdef2d840d46c6649193644a43b33b5caf9d87f8cc9673d3ca6f`;
- 577 overlap sites, zero disagreements, zero adjudications, and zero
  unresolved labels;
- the hash-only public label commitment at
  [Gist revision `1014884987f1e5b8fd8ae40125f0fcee0d2f5caa`](https://gist.github.com/bmeddeb/896c13b3e7f6e6d99e207198a2523cc7);
- exact producer identity `go version go1.26.5 darwin/arm64`, digest
  `sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0`
  and `git version 2.50.1 (Apple Git-155)`, digest
  `sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818`;
- a newly reviewed estimator implementation digest, command, environment,
  output schema, state-transition rule, and this document's digest.

The sealed frame contains 3,051 permanent-census sites, zero development
sites, and 2,693 blind holdout sites: 5,744 selected unique sites and a realized
blind fraction of 46.8837%. The original 5,743-coordinate burn cohort and its
ledger digest
`sha256:82076bd76092e03f9de16f9c3bf44e1d80e89e2c6ac5973abe13c9eeee1bac87`
remain immutable.

The estimator must recover and verify these historical artifacts. It must not
regenerate facts, use the current eight-system corpus lock, call the current
expansion producer, substitute Attempt-4 heads, or score extractor
`spike-0.5.2`.

The unverified 532-fact Istio run belongs only to the interrupted expansion. It
contains no human correctness comparison and is neither an estimator input nor
a reason for its formulas, thresholds, or scope. The alternative-path decision
rests on the completed label-free prefix-2/prefix-3 capacity stops and the
operator's interrupt, not on candidate-4 fact content or count.

## Attempt-3 failure and information-flow analysis

The private mode-0600 score receipt is
`t111-frozen-labels/ceremony/attempt-3/score.receipt.json`, with digest
`sha256:8b686e0201092622a8a057ab29e5ee225e00dd9b12b50b7f7994a168d8544883`.
It binds the historical scorer digest
`sha256:ee5b38b992dfd182063f0054d3343bcbe8a0a35129142dc1c4b85c5d682a3c55`
and the manifest, assignment, labels, commitment, and public-receipt digests.

The receipt records:

- start `2026-07-15T02:48:14.092544Z` and completion
  `2026-07-15T02:48:19.043043Z`, a 4.950499-second runtime;
- exit code 2 and `exception: null`;
- empty standard output; and
- one standard-error diagnostic: frame recomputation rejected
  `go version go1.26.4 darwin/arm64`, digest
  `sha256:e61851b2c0cde9b9ac4ae044fcce6ba9d55cb15f98ded3964793acaa8632242f`,
  because it differed from the sealed Go 1.26.5 producer identity.

The historical scorer parsed and consistency-checked `key.jsonl` before it
started frame recomputation. That ordering consumed Attempt 3 under its fixed
rule. However, the failure occurred during toolchain resolution on the first
fixture, before frame construction returned. Human labels were not parsed into
semantic values until after frame reconstruction in that scorer, so execution
never joined a label to a hidden prediction and never entered any precision,
recall, role, support, bound, or final-verdict calculation.

The ordinary observable channels were therefore limited to the exit code, the
toolchain-only stderr line, empty stdout, timestamps/runtime, command/path
names, input digests, and any terminal or process log containing the same
facts. No site, label, predicted operation, correctness bit, metric, confidence
bound, role count, partial score, or pass/fail threshold result was emitted.

Runtime is the only plausible recorded side channel. It can reflect fixed file
sizes, JSON parsing, corpus-integrity checks, and first-fixture tool startup.
Those sizes and population cardinalities were already sealed, and the failing
branch depended on the resolved Go identity rather than any label or key value.
This is strong procedural evidence that no correctness signal reached a tuning
surface, but it is not a formal constant-time or noninterference proof.

The only recorded code change directly prompted by the failed run was
mechanical: commit `0df5d1a` made the scorer attest the exact toolchain before
hidden-key access. No extractor decision could use a metric because none
existed. Later expansion choices were driven by committed source order, burn
projection, and label-free capacity bounds. For this proposal, later
extractor/harness changes are irrelevant and forbidden inputs: the candidate
being measured remains the byte-bound Attempt-3 producer and facts. The
estimator formulas and thresholds also remain the ones committed before the
failed run.

On that record, executing this separate estimator would not be a second
statistical look or outcome-driven optional stopping. That conclusion remains
conditional on human attestation that no unlogged manual join or derived score
informed subsequent work.

## Prospective one-score protocol

Human approval must create one new estimator authorization distinct from every
numbered attempt. It must pre-register the immutable inputs above, the exact
implementation and command, the original decision rule, and the following
state machine.

Approval becomes executable only after a later clean Git commit binds this
specification by digest together with the reviewed implementation and a
canonical authorization record. That record must name the authorization ID,
fixed command/environment, consumption-marker path, result-receipt path, and
every bound digest. No Phase-A admission or hidden-file access may precede that
commit.

Implementation and independent review must occur without opening or decoding
the hidden key or frozen-label content. Scoring and bound functions must be
byte-identical to the historical scorer or independently proved mechanically
equivalent. Permitted implementation changes are limited to admission
ordering, authorization state, and canonical receipts; an independent diff
review must accept those limits before authorization.

### Phase A — admission, non-consuming

Admission may run without consuming the authorization because it must not
decode hidden outcomes. It performs all enumerable mechanical checks in this
order:

1. verify the exact Go and Git identities against the sealed producer before
   opening, hashing, or parsing `key.jsonl` or frozen label content;
2. verify the estimator/scorer implementation digest and fixed command;
3. verify public receipts, historical corpus commits, fact files, manifest,
   claim, modes, non-symlink paths, and every non-outcome input digest;
4. verify key and label digests by hashing those files as opaque byte streams,
   without decoding them;
5. reconstruct and validate every possible source/fact frame input that does
   not require the hidden key or labels; and
6. prove that no prior consumption marker or result receipt exists.

Opaque, fixed-output hashing is an admission integrity check, not semantic key
loading; the exact toolchain guard precedes even that hash. JSON decoding or
joining of hidden key and label values is the consumption boundary.

A Phase-A failure emits only a mechanical admission receipt and no metric. It
does not consume the authorization. The environment may be repaired and the
same admission checks repeated, but code, inputs, sample, labels, estimator
math, thresholds, and scope may not change. A changed item requires a new human
review; it cannot be smuggled in as an environment repair.

### Phase B — the sole scoring execution

Immediately before the first semantic decode of either the hidden key or the
frozen labels, the estimator repeats the exact toolchain guard and atomically
creates a durable, exclusive consumption marker. Failure to create and persist
that marker must stop before either file is decoded. Only one transition to
`consumed` is permitted.

After that transition, the estimator decodes the fixed key and labels once,
reconstructs the exact historical frame, applies the original formulas, and
writes one canonical result receipt. Any exit, threshold failure, label or
integrity error, exception, signal, timeout, crash, or incomplete receipt after
the marker consumes the sole execution and forbids a retry. A post-boundary
failure cannot be reclassified as a non-consuming mechanical failure merely
because it emitted no metric.

This boundary is the new one-score rule: pre-key admission failures are **NO
RESULT / not consumed**; there is exactly one post-admission scoring execution,
and it is consumed whether it passes, fails a bound, or aborts. The design puts
the known mechanical checks before the boundary so a toolchain mismatch cannot
again consume an attempt.

## Unchanged estimator and outcome mapping

The score uses the original one-sided exact finite-population hypergeometric
bounds at 95% joint simultaneous confidence. The committed family size is 4,
so the Bonferroni allocation remains `alpha_each = 1/80`. Permanent census and
exhaustive fresh precision outcomes enter exactly; sampled recall strata use
the committed finite-population expansion. No formula, stratum, weight,
threshold, or role rule may change.

Gate 2 is **ESTABLISHED for the Attempt-3 frame only** if and only if the sole
execution completes cleanly and every condition passes simultaneously:

- client-call overall precision lower bound is at least 98%;
- client-call eligible-population recall lower bound is at least 90%;
- client-call precision lower bound is at least 90% in each of the four
  fixtures;
- registration overall precision lower bound is at least 98%;
- registration eligible-population recall lower bound is at least 90%;
- registration precision lower bound is at least 90% in each fixture;
- each of `generated`, `mock`, `production`, `test`, and `vendor` is nonempty
  and classified exactly, with no error in any cohort;
- the score contains at least 200 positive recall label units and at least 100
  hard-negative recall label units;
- every required label is present, none is `unsure` or unresolved, and every
  review, integrity, provenance, configuration, and artifact check passes; and
- the fixed 46.8837% blind fraction remains at least the unchanged 30% floor.

A clean completed result with any failed condition is **NOT ESTABLISHED** and
consumes the execution. An exit or abort after the consumption boundary emits
no performance claim, leaves Gate 2 **NOT ESTABLISHED**, and also consumes the
execution. A Phase-A rejection is **NO RESULT**, supports no claim, and is the
only non-consuming failure.

## Scope of a possible pass

A pass would support only the original Gate-2 Go/gRPC claims for the four
pinned repositories: client-call extraction, server-registration extraction,
the measured code-role taxonomy, and the associated narrow repository/build-
target coverage wedge. Any T13.1/T13.3 unblocking inference must state this
four-fixture, four-commit scope.

It cannot support:

- Attempt-4 commits, `grpc-go`, `etcd`, `containerd`, Istio, candidate 5 or any
  later expansion candidate;
- the expanded population, later repository heads, current extractor
  `spike-0.5.2`, or the current candidate-specific Go analysis policies;
- performance outside the fixed Attempt-3 eligible Go/gRPC population and its
  committed census/probability-sample design, including other languages,
  protocols, dynamic/reflection boundaries, or quarantined heuristics;
- Gate 1 permission, principal, atomic-publication, or production integrity;
- Gate 3 deployable/service identity, reachability, or runtime joins;
- Gate 4 SCIP proto-field references or canonical field lineage;
- T13.2, global repository coverage, current-head performance, bounded negative
  proof, migration completion, or decommission safety; or
- any claim that the interrupted expansion would have passed prefix 4.

## If human review rejects reuse

If reviewers cannot attest the no-access/no-tuning premise, cannot recover the
historical bytes exactly, or reject the residual timing argument, the frozen
labels are not valid estimator inputs and this path stops. A minimal alternative
for separate human review is a newly selected, independently reviewed blind
sample sized prospectively from the exact power analysis, with inputs,
estimator, and decision rule committed before coordinate or outcome exposure.

That alternative is not adopted here. Under the current Gate-2 rule it would
still have to preserve every burn and meet the unchanged 30% blind floor and
all existing accuracy/confidence thresholds; otherwise it would require an
explicitly reviewed new protocol rather than an implementation decision.
