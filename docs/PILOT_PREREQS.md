# Pilot prerequisites — gate-neutral documentation track

*Index v0.1. This work is documentation and test design only. **It grants no
authority to begin Epic 16**, whose implementation remains blocked until
GATE2-V2 is `ESTABLISHED` and the pilot decision authorizes continuation.
It also grants no authority over the sealed validation ceremony.*

## Ordered work items

| # | Item | Owner | Reviewer | Evidence artifact | State | Blocking finding |
|---|---|---|---|---|---|---|
| 1 | Threat model and trust boundaries | `<name>` | `<name>` | `THREAT_MODEL.md` | not started | — |
| 2 | Role/capability checklist | `<name>` | `<name>` | charter §5 completed table | not started | — |
| 3 | Negative-test matrix (derived from 1 + 2) | `<name>` | Security reviewer per charter | matrix + fixture-06-shaped expected responses | not started | — |
| 4 | VM sizing and operating assumptions | `<name>` | `<name>` | sizing worksheet with stated assumptions | not started | — |
| 5 | Backup/restore checklist + witnessed restore acceptance | `<name>` | environment owner | restore transcript, witnessed | not started | — |

Rules: items proceed in order (3 depends on 1 and 2); each item is complete
only when its evidence artifact exists, its reviewer accepted it, and any
blocking finding is closed; a blank owner or reviewer blocks the item, not
the recording of it.

## Ceremony decision records

Every GATE2-V2 stage outcome — beginning with the Stage-1 snapshot — is
captured as a decision record in PLAN.md with exactly:

- the snapshot/artifact digests produced;
- the gate result;
- the governing protocol/policy version;
- timestamp and acting principal;
- the single authorized next action.

Any result other than an eventual `ESTABLISHED` leaves Epic 16 frozen
**without interpretation** — no partial result, promising trend, or near
miss authorizes implementation work.
