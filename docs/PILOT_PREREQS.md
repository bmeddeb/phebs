# Pilot prerequisites — gate-neutral track

*Index v0.4. Epic 16 implementation is proceeding under Ben Meddeb's explicit
2026-07-22 operator bypass. The validation and continuation records remain
incomplete; [GATE0.md](./GATE0.md) is a synthetic fixture, not authorization.
Nothing here reopens or reinterprets the sealed GATE2-V2 ceremony.*

## Two phases, one authority rule

- **Design phase** — documentation and test design only: threat model,
  role model, negative-test design, sizing assumptions, restore procedure.
  Requires no environment.
- **Verification phase** — negative-test execution, capacity checks,
  witnessed restore. Requires **separate pilot-environment authorization**
  (charter Gate 1), and still grants no Epic 16 authority.

## Item states

`blocked_unassigned · not_started · in_progress · awaiting_review ·
accepted · blocked`. An item is `accepted` only when its evidence artifact
exists and its reviewer acceptance record is complete: artifact digest,
reviewer identity, timestamp, decision, unresolved findings (or `none`).

## Work items and dependencies

| # | Item | Phase | Depends on | Owner | Reviewer | Evidence artifact | State | Findings |
|---|---|---|---|---|---|---|---|---|
| 1 | Threat model and trust boundaries | design | none | TBD | TBD | [THREAT_MODEL.md](./THREAT_MODEL.md) draft | blocked_unassigned | not_assessed |
| 2 | Role/capability model | design | none (parallel with 1) | TBD | TBD | [charter §5 capability model](./PILOT_CHARTER.md#51-capability-model-draft-prerequisite-item-2) draft | blocked_unassigned | not_assessed |
| 3 | Negative-test design | design | 1 and 2 | TBD | Security reviewer per charter | [NEGATIVE_TEST_DESIGN.md](./NEGATIVE_TEST_DESIGN.md) draft — matrix + golden fixture-06 expected bytes | blocked_unassigned | not_assessed |
| 4 | Sizing assumptions | design | 1 + declared workload assumptions | TBD | TBD | [SIZING_ASSUMPTIONS.md](./SIZING_ASSUMPTIONS.md) draft — sizing worksheet, assumptions stated | blocked_unassigned | not_assessed |
| 5 | Restore procedure | design | 2 | TBD | TBD | [RESTORE_PROCEDURE.md](./RESTORE_PROCEDURE.md) draft | blocked_unassigned | not_assessed |
| 6 | Negative-test execution | verification | 3 + environment authorization | TBD | Security reviewer | two executions per case — unknown identity and unauthorized identity — each compared byte-for-byte against the same golden fixture-06 canonical response | blocked_unassigned | not_assessed |
| 7 | Capacity checks | verification | 4 + environment authorization | TBD | TBD | measured results vs assumptions | blocked_unassigned | not_assessed |
| 8 | Witnessed restore | verification | 5, 7, environment authorization | TBD | witness **independent of the restore operator** | restore transcript, witness attestation | blocked_unassigned | not_assessed |
| 9 | Statistical accuracy-gold protocol (preregistration) | design | none (sealing requires charter Gate 0) | TBD | Gate 0 signatories per charter | [ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md) draft + `pilot/validation/` harness tests | blocked_unassigned | not_assessed |
| 10 | Gate and continuation decision-record templates | design | none | TBD | TBD | [DECISION_RECORDS.md](./DECISION_RECORDS.md) draft | blocked_unassigned | not_assessed |
| 11 | Attribution-hop label sheet formats (dependency preview) | design | 9; shapes freeze at Gate 0 | TBD | Gate 0 signatories per charter | [ATTRIBUTION_HOP_SHEETS.md](./ATTRIBUTION_HOP_SHEETS.md) draft | blocked_unassigned | not_assessed |
| 12 | Current-workflow baseline protocol (§8.1 preregistration) | design | none | Ben Meddeb | Claudia; Dave | [CURRENT_WORKFLOW_BASELINE_PROTOCOL.md](./CURRENT_WORKFLOW_BASELINE_PROTOCOL.md) @ `913c765` | accepted | design form accepted; synthetic fixture values do not seal it |

`TBD` carries state `blocked_unassigned` so a mechanical blank-field check
cannot be bypassed by placeholder text.

Draft artifacts exist for items 1–5 and 9–12. Item 12 is design-accepted per
the record below; items 1–5 and 9–11 remain unaccepted. Items 3–5 and 11 are
dependency previews: they do not imply acceptance of items 1 or 2 and must be
reconciled against those artifacts after their prerequisites are accepted.
Drafting does not advance any state by itself: every unaccepted item still
requires an explicit owner, an eligible named reviewer, and the acceptance
record defined above. Design acceptance of a preregistration form does not
seal partner-shaped fields, close Gate 0, authorize measurement, or unlock
Epic 16.

**Owner aim (item 12):** use design acceptance as the first cleared Gate 0
form and proceed along the unlock path in
[GATE0_READINESS.md](./GATE0_READINESS.md) — design-accept items 9–11, complete
the drafted no-conflict and extractor-bridge worksheets with partner evidence,
solicit the remaining fills, and assemble the Gate 0 freeze package for
signature. Gate 0 unlocks only when that package is signed.

## Acceptance records

### Item 12 — Current-workflow baseline protocol

| Field | Value |
|---|---|
| Decision | `accepted` (design-phase preregistration form only) |
| Owner | Ben Meddeb |
| Reviewers | Claudia; Dave |
| Owner acceptance timestamp | `2026-07-22T17:22:00-07:00` (Owner also recorded a dual-hat design accept before the independent reviewer was named) |
| Claudia acceptance timestamp | `2026-07-22T17:37:00-07:00` |
| Dave acceptance timestamp | `2026-07-22T17:52:00-07:00` |
| Accepted commit | `913c7654d9efdf37fb45720c9f3d78522960d851` (`913c765`) |
| Accepted git blob | `ec8cc751c99323e35b968f4ac01b5f6c54dfc0dc` |
| Accepted artifact digest (SHA-256) | `c8e631854e177b5e540117d4fe70296c4cc1687f2d9777fd60fd00d985d056bc` |
| Unresolved findings | synthetic values do not fill or seal Gate 0; real partner evidence remains absent; optional §13.4 validator not required for design acceptance |
| Explicit non-decisions | not Gate 0 closure; not a pilot or external claim; not real partner evidence |
| Gate 0 | locked; [GATE0.md](./GATE0.md) is a synthetic fixture |
| Epic 16 implementation | proceeding under the explicit PLAN.md operator bypass, not Template A/B |
| Owner aim | unlock charter Gate 0 |
| Authorized next action | Epic 16 implementation on a post-gate branch only |

## Ceremony decision records

Every GATE2-V2 stage outcome — beginning with the Stage-1 snapshot — is
recorded in PLAN.md with **at minimum**:

- `stage` and `stage_result`;
- `gate_status`: `PENDING | ESTABLISHED | NOT_ESTABLISHED | INVALID |
  ABORTED` — **a stage success never implies `ESTABLISHED`**; Stage-1
  admission records `gate_status: PENDING`;
- artifact digests produced;
- governing protocol version and digest;
- timestamp and acting principal;
- one authorized next action, including explicit `none`.

Additional fields (signatures, finding references, invalidation reasons)
are permitted; omission of a minimum field is not. No intermediate result,
trend, or near miss opens Epic 16 — only `gate_status: ESTABLISHED` plus
the pilot continuation decision.
