# Statistical accuracy-gold protocol — preregistration draft

*Draft artifact for pilot prerequisite item 9 (design phase). This document
grants nothing: no pilot authority, no environment authority, no Epic 16
authority, and no accuracy claim. It becomes binding only when every
placeholder is filled, both reviewers named in
[PILOT_CHARTER.md](./PILOT_CHARTER.md) Gate 0 sign it, and its digest is
recorded in the Gate 0 freeze. GATE2-V2 remains `NOT_ESTABLISHED`; nothing
here reopens, retries, or reinterprets that closed campaign.*

The values in §2 are conspicuously synthetic fixtures added for the
2026-07-22 Epic 16 implementation bypass. They do not fill or seal Gate 0,
create a validation round, or establish any score.

## 1. Purpose and lineage

This is the preregistrable form of the charter §8.2 statistical accuracy
gold set for the pilot's internal validation — the chartered vehicle the
2026-07-22 GATE2-V2 closure names for any decision-critical accuracy result.
It reuses the sealed V2 label machinery byte-for-byte:
[`spike/t111/label_protocol.py`](../spike/t111/label_protocol.py) governs
label validation, canonical freezing, commitments, and external receipts,
and [`pilot/validation/harness.py`](../pilot/validation/harness.py) supplies
the mechanical candidate projection, deterministic sampling, and scoring.
Both file digests are recorded at sealing; a different byte is a different
protocol.

## 2. Frozen inputs (filled and sealed at Gate 0)

| Input | Value |
|---|---|
| Pilot RPC identity (canonical `/package.Service/Method`) | `/example.payment.v1.PaymentService/Authorize` |
| Source snapshot `S0` (repository set + commit digests) | `example/consumer@2222222222222222222222222222222222222222`; tree `sha256:3333333333333333333333333333333333333333333333333333333333333333` |
| Candidate extractor artifact (phebs source commit, binary digest, toolchain digest) | `example/phebs@4444444444444444444444444444444444444444`; binary `sha256:5555555555555555555555555555555555555555555555555555555555555555`; toolchain `go1.26.0/linux-amd64` |
| Bridging statement (only if artifact ≠ benchmark-bound candidate artifact; no accuracy transfer from an unscored benchmark) | synthetic identity/reproducibility/mechanics-only bridge; no accuracy transfer |
| This protocol's digest and the two machinery file digests | synthetic fixture `sha256:f7efa36fe9a81ea9c56b31a22969c44f3185ccae6c3486cca75746111d879409`; mandatory machinery digests absent |
| Public randomness seed (NIST beacon pulse URI + 64-hex output) | `mock-beacon:2026-07-22-round1`; output `6666666666666666666666666666666666666666666666666666666666666666` |

## 3. Claim families and their charter gates

| Claim | phebs evidence | Charter §9 gate | Label decision field |
|---|---|---|---|
| Source call-site extraction | `CALLS_OPERATION` assertions | Internal call-site quality (`T_INTERNAL_PRECISION`, `T_INTERNAL_RECALL`) | `invocation` |
| Service registration | `REGISTERS_GRPC_SERVICE` assertions | Service attribution (with partner-catalog hops) | `registration` |
| Attribution hops (build-target, deployable, service, owner) | partner catalogs joined to evidence | Deployable / Service attribution | per-hop sheets, `<Gate 0>` |
| End-to-end `(canonical service, operation)` edge | joined evidence, scored directly | End-to-end service-operation edge quality | independent frame, §6 |
| Abstentions | `UNRESOLVED_*` assertions, coverage `UnresolvedCount` | unresolved-rate denominators | not labeled; counted |

## 4. Eligible universe (independent of the candidate)

The target population is enumerated **without consulting phebs output**:
every source occurrence in `S0` matching the language/build eligibility
rules stated here, produced by `<Gate 0: enumeration method — e.g. exhaustive
grep-class scan plus build-metadata expansion, tool and version pinned>`.
Every unit receives exactly one terminal processing state; the analyzed /
excluded / partial / failed rates use this denominator (charter operational
definitions). The universe file is frozen and committed
(`rows_commitment`) before any phebs prediction is unsealed.

## 5. Sampling frames

- **Precision frame** — the candidate's emitted assertions for the claim
  family, projected by `harness.candidate_rows` from the sealed proof
  bundle. Site identity is the immutable citation coordinate
  `repository@commit:path:startByte-endByte`.
- **Recall-positive frame** — constructed independently of the candidate
  from `<Gate 0: documented method — e.g. §8.1 evidence channels: migration
  documents, build queries, owner outreach>`. Traffic observations may
  corroborate but never define membership.
- **End-to-end frame** — its own independently constructed precision and
  recall-positive frames over `(canonical service, operation)` edges.
  Multiplying call-site recall by attribution coverage is diagnostic only.

## 6. Sampling unit, strata, and anti-domination

The sampling unit is one site ID as defined above. Strata are
`code_role × repository`, with one collapse rule: occurrences inside
generated or wrapper files (`code_role` `generated`, or a path matching the
wrapper pattern list frozen at Gate 0) are sampled as their own stratum so
repeated machine-produced patterns cannot dominate any estimate. Per-stratum
sizes are computed with `harness.minimum_sample_size` and recorded before
sealing. Sample selection is `harness.stratified_sample` under the §2 beacon
seed — deterministic and third-party reproducible.

## 7. Blind labeling, custody, and adjudication

- Reviewers: `<Gate 0: at least two named reviewers per sheet>`, overlap
  `<Gate 0: ≥ 2 on every sampled unit>`, adjudicator `<Gate 0>`.
- Reviewers label from source only, blind to phebs predictions and to each
  other; sheets carry the full t111 label schema and are validated by
  `label_protocol.validate_labels` against the exact sealed sample.
- The adjudicated set is frozen with `freeze_labels`, committed with
  `build_label_commitment`, and externally receipted (GitHub gist + NIST
  beacon, the V2 discipline) **before phebs predictions are unsealed**.
- Custody: label sheets and adjudication notes live outside the candidate's
  working tree until commitment; the commitment record names every file
  digest.

## 8. Statistics (preregistered)

- Confidence method: two-sided **Wilson score interval**, `z = 1.96` (95%),
  as implemented in `harness.wilson_interval`.
- Power / minimum n: per-stratum sizes from `harness.minimum_sample_size`
  with margin `<Gate 0: e.g. 0.05>` at the threshold-relevant proportion;
  an undersized stratum makes the round underpowered and no claim issues
  (charter: "an underpowered or inconclusive round carries no accuracy
  claim").
- Multiplicity: each §3 claim family is scored once per sealed round; no
  interim looks, no re-rolls; a failed round's remediation requires a fresh
  unseen round (charter §9).
- Missing/unlabelable: `unsure` labels are excluded from numerator and
  denominator and always reported (`harness.score_claim`); a sampled unit
  with no adjudicated label invalidates the round — the harness fails
  closed rather than degrading.

## 9. Scoring and reporting

Accuracy and coverage are separate claims per hop; unresolved rates use the
declared denominators; end-to-end edges are scored directly against their
own frames. Every reported number carries: count, denominator, point
estimate, Wilson bounds, stratum breakdown, and the error-taxonomy tally
(disagreements, wrappers, macros, build tags, generated sources, catalog
conflicts, unsupported patterns). Results bind into the decision records
defined in [DECISION_RECORDS.md](./DECISION_RECORDS.md).

## 10. Open items that block sealing

1. **Attribution-hop label sheets** — draft formats exist as
   [ATTRIBUTION_HOP_SHEETS.md](./ATTRIBUTION_HOP_SHEETS.md) (prerequisite
   item 11); the partner's catalog coordinate shapes are frozen at Gate 0
   and remain the sealing blocker.
2. Every `<Gate 0>` placeholder above; any blank blocks release (charter
   Gate 0 rule).

Proto field-level lineage is excluded from this protocol by
`PILOT_CHARTER.md` §3.4. `REFERENCES_PROTO_FIELD` is therefore rejected by the
harness rather than assigned a pilot label schema; changing that boundary
requires a separately reviewed charter revision, not a prerequisite edit.

## 11. What this protocol can and cannot produce

A sealed, adequately powered round under this protocol produces evidence
that the Gate 0 signatories may weigh toward a `gate_status: ESTABLISHED`
decision recorded per [DECISION_RECORDS.md](./DECISION_RECORDS.md). It
cannot: reopen GATE2-V2, produce a public-corpus claim, satisfy the
continuation decision (a separate human judgment), or unlock Epic 16 by
itself. No intermediate result, trend, or near miss opens anything
([PILOT_PREREQS.md](./PILOT_PREREQS.md) ceremony rules).
