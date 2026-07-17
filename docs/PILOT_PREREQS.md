# Pilot prerequisites — gate-neutral track

*Index v0.2. **No item here grants authority to begin Epic 16**, whose
implementation remains blocked until GATE2-V2 is `ESTABLISHED` and the
pilot decision authorizes continuation. Nothing here touches the sealed
validation ceremony.*

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
| 1 | Threat model and trust boundaries | design | none | TBD | TBD | `THREAT_MODEL.md` | blocked_unassigned | not_assessed |
| 2 | Role/capability model | design | none (parallel with 1) | TBD | TBD | charter §5 completed table | blocked_unassigned | not_assessed |
| 3 | Negative-test design | design | 1 and 2 | TBD | Security reviewer per charter | matrix + golden fixture-06 expected bytes | blocked_unassigned | not_assessed |
| 4 | Sizing assumptions | design | 1 + declared workload assumptions | TBD | TBD | sizing worksheet, assumptions stated | blocked_unassigned | not_assessed |
| 5 | Restore procedure | design | 2 | TBD | TBD | written procedure | blocked_unassigned | not_assessed |
| 6 | Negative-test execution | verification | 3 + environment authorization | TBD | Security reviewer | two executions per case — unknown identity and unauthorized identity — each compared byte-for-byte against the same golden fixture-06 canonical response | blocked_unassigned | not_assessed |
| 7 | Capacity checks | verification | 4 + environment authorization | TBD | TBD | measured results vs assumptions | blocked_unassigned | not_assessed |
| 8 | Witnessed restore | verification | 5, 7, environment authorization | TBD | witness **independent of the restore operator** | restore transcript, witness attestation | blocked_unassigned | not_assessed |

`TBD` carries state `blocked_unassigned` so a mechanical blank-field check
cannot be bypassed by placeholder text.

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
