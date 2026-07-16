# T11.1 Gate 2 — second protocol (GATE2-V2, revision 2)

**Status: DRAFT — pending human review. Nothing in this document is
executable until an explicit approval decision lands in PLAN.md naming this
file's digest, and no disclosure-bearing stage may begin until every Stage-0
gate artifact is sealed.**

Revision 2 incorporates the adversarial review of revision 1 (14 findings,
verdict REJECT). This protocol supersedes the original Gate-2
census/blind-fraction rule **as an explicit, reviewed design change** — it
does not claim continuity with the sealed 30% blind-floor condition, which
it removes and replaces (§9). Gates 1, 3, and 4 are untouched. Accuracy
thresholds, the confidence structure, and the estimator formulas are carried
over unchanged; the blindness rule, census treatment, and input-admission
discipline are new and are exactly what the reviewer is approving.

## 1. Why a second protocol is justified — scoped honestly

Four terminations with committed root causes exhausted the first protocol:
Attempt 3 (toolchain consumption), Attempt 4 (capacity), the interrupted
expansion (0.203 vs 0.30 lower bound), and the frozen-label estimator
(sealed loki facts `sha256:03ededa3…` not reproducible from declared
provenance; historical cohort closure never declared; commit `30ddcce`).

**Outcome exposure, stated precisely.** Attempt 3 and the frozen-label
estimator emitted no metric, partial score, or correctness bit (sealed
receipts; `estimator-attestation.json`). Earlier sealed attempts did emit
outcomes — including Attempt 2's retracted precision/recall/role figures —
and those numbers were observed. Therefore this protocol does **not** argue
"no outcome ever existed." It argues, and the reviewer must accept or
reject: (a) aggregate retracted metrics identify no individual site, so
their residual risk is to *design-point optimism*, mitigated by the fixed
thresholds and by power design points chosen below the observed aggregates;
and (b) site-level disclosure risk is handled structurally by the
carry-forward rule in §6, not dismissed.

**Census integrity.** Burn carry-forward assumed every disclosed cohort's
inputs were recomputable; cohort provenance for loki provably is not
(`30ddcce`). The old census cannot be verified against its own generating
inputs. It is therefore retired as an *accounting structure* — but its
disclosed coordinates are not forgotten: they seed census v2 under §6.

## 2. Candidate under measurement

The **current spike extractor**, pinned at Stage-0 sealing as an exact
producer identity: source commit, builder toolchain identity and digests,
binary digest, and the producer's own declared build closure. No Attempt-era
outcome, population semantics, or claim wording may be inherited: a pass
produces new T13.x ADR wording scoped per §10, and citing any Attempt-2/3
figure in support of any downstream claim is prohibited.

## 3. Staged execution with fail-closed gates

Every stage seals its artifacts before the next may begin; any gate failure
stops the round with a committed root cause. Nothing is retried in place.

- **Stage 0 — tooling and feasibility (no disclosure, repeatable).**
  Seal: closure-manifest schema and resolver, demonstrated on all four
  fixtures at their *current* mirrors with two-run byte-identical
  recomputation proofs (§4); the exact power script, design points, alpha
  partition transcription (§5); the census-v2 carry-forward mapper (§6);
  the snapshot query bytes and cutoff (§7); digests of every protocol code
  path (§4, list); and a bounding feasibility proof: exact per-frame sample
  sizes computed against the sealed Attempt-3 frame cardinalities as a
  population proxy, including the online-boutique per-fixture feasibility
  result at `alpha_each = 1/80`. If the proxy population cannot clear the
  thresholds at the design points, the protocol stops **before approval to
  proceed**.
- **Stage 1 — snapshot.** One automated ref snapshot at the sealed cutoff
  (§7). Any failure, partial response, or ineligible fixture stops the
  round; no substitution, no re-cut without a new reviewed cutoff.
- **Stage 2 — enumeration, carry-forward, and power sealing.** Frames
  enumerated by the sealed code; census-v2 seed burns computed by the
  sealed mapper (§6); the exact power analysis run against the *actual*
  frame cardinalities net of burns, its full inputs and outputs sealed. If
  any frame cannot meet its computed size from the unburned population, the
  round stops here — before any human sees a coordinate.
- **Stage 3 — selection, disclosure, labeling.** Committed-randomness
  selection (§5), burn append (§6), reviewer kits, labeling, freeze, and
  public hash-only commitment (§8).
- **Stage 4 — authorization and one-score execution (§9 machinery, §10
  rule).**

## 4. Reproducibility admission rule

No input may be sealed unless already recomputed from its own declaration.

- Every fixture declares a **module closure**: a manifest of exact module
  versions and content digests produced by the sealed resolver. The schema
  and resolver are Stage-0 artifacts, and the mechanism must be
  demonstrated for loki, temporal, dapr, and online-boutique **before**
  any snapshot — the defect class of `30ddcce` must be impossible for
  every fixture, not asserted generically.
- Every fact file is produced twice, byte-identically, offline, from only:
  pinned corpus commit, declared closure, pinned producer, pinned
  toolchain. Both run digests are sealed.
- The input commitment binds, by path, commit, and byte digest: fixture
  commits and closures, fact digests and recomputation proofs, producer and
  toolchain identities, corpus lock, **and every code path that shapes the
  population or ceremony**: frame-construction code, sampling/rank
  implementation, randomness-commitment generator, carry-forward mapper,
  reviewer-kit generator, label schema and adjudication code, the power
  script, and this protocol file.

## 5. Sampling, blindness, and power

The sealed 30% blind-floor rule is **removed and replaced** (this is the
substantive rule change under review): with census-v2 burns excluded at
selection time, every *sampled* site is blind by construction.

- Selection uses the committed audited domain-separated SHA-256 rank within
  declared strata, seeded by a public randomness commitment fixed at Stage
  0, before enumeration.
- Sample sizes are outputs of the sealed exact power script, never chosen:
  per frame, the minimum size such that at the design points — true overall
  precision 99.5%, true eligible-population recall 95%, true per-fixture
  precision 97% — the one-sided exact finite-population hypergeometric
  lower bounds clear §10 with ≥90% assurance. Design points sit below the
  retracted Attempt-2 aggregates deliberately (§1a).
- The Bonferroni family partition and alpha allocation are **transcribed
  from the sealed scorer implementation, not restated informally**: the
  Stage-0 power artifact must print the exact partition (the four estimand
  families — client-call precision, client-call recall, registration
  precision, registration recall — with per-fixture bounds and role-cohort
  exactness as joint conditions within their families, exactly as
  `score.py` implements them) and allocate `alpha_each = 1/80`. Any
  divergence between that transcription and the scorer is a Stage-0 stop.
- Label-mass floors are retained as absolute minimums: ≥200 positive and
  ≥100 hard-negative recall label units.

## 6. Census v2: seeding, carry-forward, and capacity

Census v2 is append-only and inherits §4 (a cohort that cannot recompute
its provenance cannot enter). It does **not** start empty:

- **Seed burns.** Every coordinate ever disclosed in any prior cohort
  (including the legacy and Attempt cohorts of the retired census) is
  mapped to the Stage-1 heads by the sealed carry-forward mapper: a
  coordinate burns iff its file path exists at the new head **and** the
  labeled line-range content is byte-identical after the mapper's committed
  normalization. Unchanged content is recognizable content; it stays
  burned. Changed, moved, or deleted content is free. The full mapping
  (inputs, rules, outputs, digests) is sealed at Stage 2.
- **Capacity consequence, quantified.** Stage 2 fails closed if the
  unburned population cannot support the power-computed sizes — the
  Attempt-4 failure mode is confronted with sealed arithmetic *before*
  disclosure, not discovered after. Stage 0's proxy computation gives the
  reviewer a feasibility estimate before approving anything.
- **New burns.** Selection output is appended to census v2 **before**
  reviewer-kit creation (§8 timeline), so an abandoned round still burns
  what it disclosed.

## 7. Snapshot mechanics

One request, fully specified at Stage 0: committed GraphQL query bytes (the
expansion's `expansion-source-snapshot.graphql` pattern); target = each
fixture repository's default branch ref at response time; single
non-paginated request for the four fixtures; fired automatically at the
sealed cutoff instant (RFC3339 UTC, system clock cross-checked against the
response's server timestamp); no retry — any transport failure, partial
body, schema surprise, or moved/renamed repository stops the round. Query
bytes, response bytes, receipt, and digests are sealed as Stage-1
artifacts.

## 8. Labeling, identities, and the disclosure timeline

- **Identities pinned.** Reviewer identities (exactly two), their
  independence attestations (no extractor authorship, no access to producer
  internals during labeling), the adjudication procedure and adjudicator
  identity, and the label schema version are sealed **before kit
  creation**. Silent reviewer substitution or schema drift after sealing is
  a round-terminating integrity failure.
- **Assignment and overlap** are generated from the committed randomness by
  sealed code; the overlap subset is ≥10% of sites; disagreements are
  adjudicated and counted; any unresolved or `unsure` site fails its frame
  closed.
- **Disclosure timeline (durable edges, in order):** E1 selection sealed
  (no human views coordinates) → E2 census-v2 burns appended and committed
  → E3 reviewer kits created (*the* disclosure edge) → E4 labeling → E5
  label freeze + public hash-only commitment (new gist revision) → E6
  authorization binding → E7 Phase A (repeatable) → E8 Phase B (sole
  consumption). Selection may be regenerated only before E2. After E2, a
  restarted round starts at Stage 2 against the enlarged census. After E5,
  labels are immutable.

## 9. Authorization and one-score machinery

Execution reuses the validated estimator implementation with a **new
authorization ID, fresh state paths, and no reuse of any Attempt-3 receipt,
marker path, or ceremony directory**. The concrete gates, enumerated:
canonical-JSON authorization bytes; nested `review.status = "accepted"` and
`binding.status = "executable"`; a full-hash binding commit that is an
ancestor of HEAD; clean tracked and index worktree; exact implementation
digests verified in admission; the exact pinned environment variables; and
the toolchain guard executed **before any key or label byte is opened or
hashed**. Consumption semantics are the estimator's exact transition table:
the sole transition is a successful `O_CREAT|O_EXCL|O_NOFOLLOW` marker
creation with file **and** directory fsync; every pre-transition failure is
NO RESULT and non-consuming; marker-persistence ambiguity after `O_EXCL`
is treated as consumed; every post-transition exit — pass, failed bound,
error, signal, timeout — writes one canonical terminal receipt and forbids
retry. Implementation changes beyond new input bindings require the same
implementer/reviewer separation as before.

## 10. Decision rule (thresholds unchanged; blind rule replaced)

One-sided exact finite-population hypergeometric bounds, 95% joint
simultaneous confidence, family partition per §5, `alpha_each = 1/80`.
Gate 2 is **ESTABLISHED for this frame** iff the sole execution completes
cleanly and simultaneously: client-call overall precision LB ≥ 98%;
client-call eligible-population recall LB ≥ 90%; client-call per-fixture
precision LB ≥ 90%; registration overall precision LB ≥ 98%; registration
eligible-population recall LB ≥ 90%; registration per-fixture precision LB
≥ 90%; all five role cohorts (`generated`, `mock`, `production`, `test`,
`vendor`) nonempty and classified exactly with no error; label-mass floors
met; every integrity, provenance, and configuration check passes. The
original protocol's additional condition — the fixed blind fraction
remaining ≥ 30% — is **intentionally absent**, replaced by §5/§6
blind-by-construction plus sealed capacity arithmetic; approving this
document approves that replacement. A clean completion failing any
condition is NOT ESTABLISHED and consumes the execution.

## 11. Scope of a pass

A pass supports: Go/gRPC client-call extraction, server-registration
extraction, and the code-role taxonomy for the pinned current producer on
the four pinned fixture heads under the committed census/sample design —
and unblocks T13.1/T13.3 with new, freshly-worded ADR scope statements. It
does not support: other languages, protocols, repositories, or heads;
dynamic/reflection boundaries; Gates 1/3/4; T13.2; bounded negative proof;
any claim resting on Attempt-2/3 outcomes or the retired census; or any
statement about the first protocol's attempts beyond their sealed record.

## 12. Reviewer checklist

Accept or reject each explicitly:

1. the scoped outcome-exposure argument and census retirement (§1) —
   including that aggregate retracted metrics are mitigated by design-point
   placement, not ignored;
2. the current producer as candidate with Attempt-era claim isolation (§2);
3. the staged gate structure, with Stage-0 proxy feasibility and Stage-2
   exact power sealing replacing pre-approval exact outputs (§3, §5);
4. the reproducibility admission rule and its Stage-0 four-fixture
   demonstration requirement (§4);
5. the carry-forward seeding rule and its quantified capacity consequence
   (§6);
6. the snapshot mechanics (§7);
7. identity pinning and the disclosure timeline (§8);
8. the explicit removal of the 30% blind-floor condition (§10) — the one
   substantive decision-rule change in this protocol.

Approval is a dated PLAN.md decision naming this file's digest. Rejection
of any item stops the protocol before Stage 0.
