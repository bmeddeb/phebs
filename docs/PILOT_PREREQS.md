# Pilot prerequisites — gate-neutral track

*Index v0.2. **No item here grants authority to begin Epic 16**, whose
implementation remains blocked until the pilot internal-validation record is
`ESTABLISHED` and the separate pilot decision authorizes continuation. Nothing
here reopens or reinterprets the sealed GATE2-V2 ceremony.*

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
| 12 | Current-workflow baseline protocol (§8.1 preregistration) | design | none | TBD | TBD | draft to be produced | blocked_unassigned | not_assessed |

`TBD` carries state `blocked_unassigned` so a mechanical blank-field check
cannot be bypassed by placeholder text.

Draft artifacts exist for items 1–5 and 9–11. Items 3–5 and 11 are dependency previews: they do
not imply acceptance of items 1 or 2 and must be reconciled against those
artifacts after their prerequisites are accepted. Drafting does not advance any
state: every item still requires an explicit owner, an eligible named reviewer,
and the acceptance record defined above.

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
