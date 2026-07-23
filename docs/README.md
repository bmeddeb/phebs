# phebs documentation map

Read in this order for your role; each document states its own authority.

## Engineering (source of truth)

| Document | Role |
|---|---|
| [../PLAN.md](../PLAN.md) | architecture + dated ADR decision ledger — **the** authority; every decision lands here in the same PR |
| [BACKLOG.md](./BACKLOG.md) | epics and PR-sized tickets; work proceeds in ticket order |
| [MANUAL.md](./MANUAL.md) | user manual; behavior changes update it in the same PR |
| PORT_MAP.md *(removed 2026-07-12; historical — see git history)* | upstream analysis, scope, license posture |
| [config.example.yaml](./config.example.yaml) | annotated configuration reference |

## Adoption suite (internal circulation)

Read top to bottom; each narrows the previous:

| Document | Role |
|---|---|
| [VISION.md](./VISION.md) | the direction: evidence plane, packs, expansion workflows, sequencing |
| [INVESTIGATIONS.md](./INVESTIGATIONS.md) | the product shape: the Investigation object, UX, envelope, review |
| [INVESTIGATION_DOMAIN_CONTRACT.md](./INVESTIGATION_DOMAIN_CONTRACT.md) | the normative product semantics: identities, lifecycles, authorization, eligibility, diffs, Decisions, Review, and dossiers |
| [PITCH.md](./PITCH.md) | the ask: bounded six-week monorepo pilot |
| [PILOT_CHARTER.md](./PILOT_CHARTER.md) | the execution contract: gates, frozen thresholds, teardown |
| [EVIDENCE_PACK_CARD.md](./EVIDENCE_PACK_CARD.md) | the per-pack capability/validation contract (template) |
| [PILOT_PREREQS.md](./PILOT_PREREQS.md) | gate-neutral prerequisite index: owners, reviewers, evidence, decision-record rules |

The suite's invariant: nothing downstream expands the ask upstream, and no
claim outruns its measurement.

## Pilot design artifacts

| Document | Role |
|---|---|
| [THREAT_MODEL.md](./THREAT_MODEL.md) | draft threat model and trust-boundary record for pilot prerequisite item 1; grants no environment or implementation authority |
| [NEGATIVE_TEST_DESIGN.md](./NEGATIVE_TEST_DESIGN.md) | dependency-preview authorization and integrity test matrix for prerequisite item 3; execution remains separately gated |
| [SIZING_ASSUMPTIONS.md](./SIZING_ASSUMPTIONS.md) | dependency-preview workload and capacity worksheet for prerequisite item 4; contains no environment measurements |
| [RESTORE_PROCEDURE.md](./RESTORE_PROCEDURE.md) | dependency-preview backup and witnessed-restore design for prerequisite item 5; grants no backup or environment authority |
| [ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md) | preregistration draft of the pilot internal-validation protocol for prerequisite item 9; reuses the sealed V2 label machinery; grants no pilot, accuracy, or Epic 16 authority |
| [DECISION_RECORDS.md](./DECISION_RECORDS.md) | fail-closed templates for the validation gate decision and the pilot continuation decision, prerequisite item 10; filling a template creates no decision |
| [ATTRIBUTION_HOP_SHEETS.md](./ATTRIBUTION_HOP_SHEETS.md) | dependency-preview hop label sheet formats for prerequisite item 11; partner catalog shapes freeze at Gate 0; grants nothing |
| [CURRENT_WORKFLOW_BASELINE_PROTOCOL.md](./CURRENT_WORKFLOW_BASELINE_PROTOCOL.md) | design-accepted preregistration form for prerequisite item 12; freezes the manual-vs-phebs timing, labor, usefulness, evidence, and custody comparison; grants no measurement authority and is not Gate 0-sealed |
| [NO_CONFLICTING_DEPENDENCY_STATEMENT.md](./NO_CONFLICTING_DEPENDENCY_STATEMENT.md) | Gate 0 worksheet for production/pilot non-dependency, resource and schedule conflicts, circularity, conditions, and signatures; currently an unapproved draft |
| [EXTRACTOR_BRIDGE_WORKSHEET.md](./EXTRACTOR_BRIDGE_WORKSHEET.md) | Gate 0 typed-benchmark-to-pure-reader artifact bridge; binds identity/reproducibility/mechanics only and permanently prohibits accuracy transfer |
| [GATE0_READINESS.md](./GATE0_READINESS.md) | descriptive audit of every Gate 0 requirement plus the Owner path to unlock the gate; includes the accepted v0.2 resolution of the external-benchmark conflict; decides nothing and cannot substitute for Gate 0 signatures |

The role/capability model for prerequisite item 2 lives in
[PILOT_CHARTER.md §5](./PILOT_CHARTER.md#5-roles-and-authority) so authority
semantics do not drift into a second document.

## Validation records (sealed history)

The T11.1/GATE2-V2 validation protocol, attempt records, and stage
artifacts live under `spike/t111/` (`labeling/GATE2-V2.md`, `REPORT.md`,
`labeling/ATTEMPTS.md`, `labeling/gate2-v2/`). These are sealed records:
amended by dated append, never rewritten.
