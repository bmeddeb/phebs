# T11.1 Gate 2 — second protocol (GATE2-V2)

**Status: DRAFT — pending human review. Nothing in this document is
executable until an explicit approval decision lands in PLAN.md and an
authorization record binds this file by digest.**

This protocol supersedes the original Gate-2 census/blind-fraction rule by
explicit reviewed decision. It does not amend the first protocol in place:
the original rule, its census, and its four terminations remain sealed
history (REPORT.md, ATTEMPTS.md, PLAN.md 2026-07-15 entries). Gates 1, 3,
and 4 are untouched. Every accuracy threshold, confidence structure, and
estimator formula is carried over unchanged; what changes is how the blind
population is obtained and what provenance an input must prove before it may
be sealed.

## 1. Why a second protocol is justified

Four terminations, each with a committed root cause, exhausted the first
protocol without emitting a metric:

1. **Attempt 3** — the one permitted scoring execution was consumed by a
   toolchain-resolution slip after key load (terminal by rule; receipt
   sealed).
2. **Attempt 4** — the append-only census left 4 fresh sites against a
   5,745-site denominator; the 30% blind floor was arithmetically
   unattainable (preflight sealed).
3. **Benchmark expansion** — interrupted at a 0.203 seed-independent lower
   bound against the unchanged 0.30 floor, with diminishing per-candidate
   gains (prefix-2/3 preflights sealed).
4. **Frozen-label estimator** — terminated because the sealed Attempt-3
   loki facts (`sha256:03ededa3…`) are not reproducible from their declared
   provenance: the input commitment names a historical cohort identity
   (`sha256:da800af3…` @ `aa5e221a…`) whose module closure was never
   declared; two byte-identical rebuilds of the sealed producer at the
   sealed corpus commit yield `sha256:f92312ff…` (commit `30ddcce`).

Finding 4 does more than end a path: it falsifies the premise of the
permanent census. Burn carry-forward assumed every disclosed cohort's inputs
were fully self-describing and recomputable; cohort `gate2-…9164…` provably
is not. Carrying forward burns whose generating inputs cannot be recomputed
is not conservative — it is unfounded. The census is therefore retired on
integrity grounds, not convenience.

The overfitting risk that burns guard against requires an outcome signal to
overfit toward. No attempt ever emitted a metric, partial score, or
correctness bit (receipts sealed; operator attestation
`estimator-attestation.json`, 2026-07-15). Disclosed coordinates were seen
by reviewers and harness code but never joined to extractor performance.
**Census v2 therefore starts empty.** This reasoning is part of what the
reviewer must explicitly accept or reject.

## 2. Candidate under measurement

The candidate is the **current spike extractor**, pinned at authorization
time as an exact producer identity: source commit, builder toolchain
identity and digest, binary digest, and the declared build closure. The
measured claims transfer to T13.x productization only as stated in §10. The
prior protocol's candidate (the Attempt-3-era producer) is not measurable —
its loki input is not reproducible — and no claim about it survives.

## 3. Population and fixtures

Four fixtures, one snapshot: `temporal`, `dapr`, `loki`, `online-boutique`,
each pinned to its default-branch head from **one** ref snapshot taken at a
prospectively fixed cutoff timestamp recorded in the authorization. The
snapshot query and response are committed. No later head may be substituted;
a fixture that fails eligibility (unavailable, non-buildable closure) is
recorded and the protocol stops rather than substituting a repository.

The eligible-site population and all sampling frames (client-call precision,
client-call recall, registration precision, registration recall, role) are
enumerated by the committed frame-construction code, unchanged from the
first protocol.

## 4. Reproducibility admission rule (new, load-bearing)

No input may be sealed unless it has already been recomputed from its own
declaration. Concretely, before the input commitment is written:

- every fixture's **module closure is declared** as a manifest of exact
  module versions and content digests (the istio-style "declared production
  closure" mechanism), sealed alongside the corpus commit;
- every fact file is produced **twice, byte-identically**, by the pinned
  producer, offline, from only: the pinned corpus commit, the declared
  closure, and the pinned toolchain — and this recomputation proof (both
  run digests) is itself sealed;
- the input commitment binds: fixture commits, closure manifests, fact
  digests, recomputation proofs, producer identity, corpus lock, frame
  code digests, and this protocol's digest.

An input that cannot pass recomputation **before sealing** stops the
protocol at zero cost. The defect class that terminated the frozen-label
path becomes structurally impossible to seal.

## 5. Sampling, blindness, and power

The old rule ("≥30% of selected unique sites blind against the permanent
census") is replaced by **blind-by-construction**: with census v2 empty,
every sampled site is blind at selection time. Blindness is preserved by
process, not by fraction:

- site selection uses the committed audited domain-separated SHA-256 rank
  within declared strata, seeded by a public randomness commitment fixed
  before frame enumeration;
- no coordinate is shown to any tuning surface before labels freeze;
- disclosure begins only at reviewer-kit creation, and every disclosed
  coordinate enters census v2 as a burn for any future protocol round.

**Sample sizes are computed prospectively by the committed exact power
analysis**, not chosen: for each frame, the minimum sample size such that,
at the design-point parameters (true overall precision 99.5%, true
eligible-population recall 95%, per-fixture precision 97%), the one-sided
exact finite-population hypergeometric lower bounds clear the §9 thresholds
with at least **90% assurance** at `alpha_each = 1/80`. The power script,
its inputs, and its outputs are sealed with the commitment. The first
protocol's floors on label mass are retained as absolute minimums: at least
200 positive recall label units and at least 100 hard-negative recall label
units. If the population cannot support the computed sizes, the preflight
fails closed before any labeling begins.

## 6. Census v2 and disclosure accounting

Census v2 begins empty (§1 rationale). From this protocol onward it records,
append-only: every coordinate disclosed to a reviewer, every labeled site,
and the closure-complete provenance of the cohort that disclosed it. A
future cohort whose provenance fails recomputation cannot be admitted into
the census — the census inherits §4.

## 7. Labeling

Unchanged in structure from the first protocol: two independent reviewers
labeling from source only (no extractor output visible); a reviewer-overlap
subset of at least 10% of sites for agreement measurement; disagreements
adjudicated and counted; any unresolved or `unsure` site fails the affected
frame closed. Labels freeze into one canonical file; its digest is
committed publicly (hash-only, new gist revision) **before** any scoring
authorization becomes executable.

## 8. Commitment and one-score execution

Execution reuses the validated estimator machinery unchanged in substance:
canonical-JSON authorization record; nested review/binding state with
ancestor binding-commit semantics; exact-toolchain guard **before any key
or label access**; non-consuming, repeatable Phase A admission that
verifies every §4 digest opaquely and reconstructs all public frames; a
durable exclusive consumption marker created immediately before the first
semantic decode; exactly one Phase B execution; canonical receipts for
every phase. A new authorization ID is created for this protocol; the
Attempt-3 authorization is spent history. Implementation changes beyond
new input bindings require the same independent implementer/reviewer
separation as before.

## 9. Unchanged decision rule

One-sided exact finite-population hypergeometric bounds, 95% joint
simultaneous confidence, Bonferroni family of 4, `alpha_each = 1/80`.
Gate 2 is **ESTABLISHED for this frame** iff the sole execution completes
cleanly and simultaneously:

- client-call overall precision lower bound ≥ 98%;
- client-call eligible-population recall lower bound ≥ 90%;
- client-call precision lower bound ≥ 90% in each fixture;
- registration overall precision lower bound ≥ 98%;
- registration eligible-population recall lower bound ≥ 90%;
- registration precision lower bound ≥ 90% in each fixture;
- each of `generated`, `mock`, `production`, `test`, `vendor` nonempty and
  classified exactly, with no error in any cohort;
- label-mass floors met (§5); every review, integrity, provenance, and
  configuration check passes.

A clean completion failing any condition is **NOT ESTABLISHED** and
consumes the execution. A post-boundary abort consumes the execution with
no claim. Phase-A rejections are NO RESULT and non-consuming.

## 10. Scope of a pass

A pass supports: Go/gRPC client-call extraction, server-registration
extraction, and the code-role taxonomy for the pinned producer on the four
pinned fixture heads, under the committed census/probability-sample design
— and unblocks T13.1/T13.3 with that scope stated. It does not support:
other languages or protocols, other repositories or heads, dynamic or
reflection-based call boundaries, Gates 1/3/4, T13.2, bounded negative
proof, or any claim about the retired first-protocol census or attempts.

## 11. Termination semantics

Any of the following stops the protocol fail-closed with a sealed root
cause and no substitution: snapshot ineligibility (§3), recomputation
failure (§4), power infeasibility (§5), labeling integrity failure (§7),
or a consumed Phase B (§9). A stopped protocol round may only be followed
by a new reviewed round; nothing is retried in place.

## 12. Reviewer checklist

The approver must explicitly accept or reject each of:

1. the census v2 reset argument (§1) — no outcome signal ever existed, and
   the old census fails its own reproducibility premise;
2. measuring the **current** producer rather than the unmeasurable
   Attempt-3 producer (§2);
3. fresh heads at one committed snapshot (§3);
4. the reproducibility admission rule as a sealing precondition (§4);
5. blind-by-construction plus power-computed sizes replacing the 30% rule,
   with the 90% assurance target and design-point parameters (§5);
6. unchanged thresholds, alpha structure, and one-score semantics (§8–§9).

Approval is a dated PLAN.md decision naming this file's digest. Rejection
of any item stops this protocol before any snapshot, enumeration, or
randomness commitment exists.
