# Gate 0 readiness audit

*Working audit for the pilot's entry gate, produced by attempting to run
Gate 0 against current reality (2026-07-22). This document grants nothing
and decides nothing: it maps every [PILOT_CHARTER.md](./PILOT_CHARTER.md)
§6 Gate 0 requirement to its current state, owner, and next action. States
here are descriptive; only the charter's own signatures close Gate 0.*

## Legend

- **drafted** — a reviewable artifact exists on the gate-neutral track.
- **draftable now** — phebs-side design work, no partner required.
- **partner-blocked** — requires a named design partner or their people.
- **approval-blocked** — requires a named human review or sign-off.
- **charter-conflict** — the requirement cannot be satisfied as written;
  only a separately reviewed charter revision can resolve it.

## Audit

| Gate 0 requirement (charter §6) | State | Owner | Next action |
|---|---|---|---|
| Sponsor, roles, time allocation, proposed RPC, decision authority named | partner-blocked | sponsor + Ben | every §5 role except pilot lead is `<name>`; naming happens with the partner |
| Canonical IDL identity and source universe confirmed | partner-blocked | migration owner | requires the partner's contract and repository set |
| IP/OSS/provenance review approved or cleared for the bounded evaluation | approval-blocked | OSS/Legal reviewers | reviewers unnamed; review not started |
| **External Go/gRPC benchmark passes its published gate** | **charter-conflict** | **operator (charter revision)** | GATE2-V2 closed `NOT_ESTABLISHED` by valid capacity stop (2026-07-22 ADR) and no V3 is chartered. As written, [line 246](./PILOT_CHARTER.md) can never be satisfied, and §9's "External extraction" row ([line 376](./PILOT_CHARTER.md)) makes the missing pass a hard stop. The closure ADR names the pilot's internal validation as the accuracy vehicle; the charter predates that outcome and still assumes an external pass. Resolving this requires a **separately reviewed charter revision** (the standard the 2026-07-22 scope-correction ADR set); no prerequisite artifact may reinterpret it. Until revised, Gate 0 is structurally unsatisfiable. |
| Pilot extractor artifact equals the benchmark-measured artifact, or a versioned bridging statement | charter-conflict (dependent) | operator (charter revision) | inherits the row above: with no benchmark-measured artifact, only the bridging-statement arm can exist, and what it bridges *to* must be defined by the same charter revision |
| Evidence-pack card at status `shadow` for the Go/gRPC pack, internal shadow evaluation filling the internal/domain-shift table | approval-blocked | Ben + security reviewer | the card's own ladder (experimental-dark → shadow) requires security approval, frozen validation design, reproducible artifact, and a complete authorization model; the internal shadow evaluation has no protocol document yet |
| Current-workflow baseline protocol preregistered (§8.1) | draftable now | Ben | prerequisite item 12 (registered, no draft yet); methodology needs no partner data to draft |
| Statistical accuracy-gold protocol preregistered (§8.2) | drafted | Gate 0 signatories | [ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md), scope-corrected; awaiting owners, reviewers, and `<Gate 0>` fills |
| Accuracy protocol defines population, frames, unit, strata, missing-label handling, power, custody, and distinct call-site / attribution-hop / end-to-end labels | drafted + draftable now | Ben | call-site and end-to-end are in the protocol; attribution-hop sheet formats now drafted as [ATTRIBUTION_HOP_SHEETS.md](./ATTRIBUTION_HOP_SHEETS.md) (item 11), with partner-catalog shapes finalized at Gate 0 |
| §9 pass / conditional / stop thresholds filled and signed | partner-blocked + approval-blocked | sponsor + Ben | every `T_*` is `<value>`; filling is a Gate 0 act with the partner's workload |
| Every role, date, resource limit, `T_*`, and protocol field filled; blanks block release | blocked by all above | Gate 0 signatories | mechanical once the rows above resolve |
| No conflicting pilot or production dependency | draftable now | Ben | one-page statement at Gate 0; nothing known today |

## What this audit establishes

Running the gate today halts at two classes of blocker:

1. **One structural conflict only the operator can resolve** — the external
   benchmark clause. Every other path is ordinary work. A charter revision
   is a reviewed change to the execution contract, not a prerequisite
   edit; this audit deliberately does not propose its wording.
2. **The partner** — roles, RPC, universe, catalogs, thresholds, and both
   verification-phase environments remain partner-shaped, as
   [PILOT_PREREQS.md](./PILOT_PREREQS.md) always said.

Everything phebs-side and partner-free is either done or registered:
items 9–10 drafted and scope-corrected, item 11 drafted with this audit,
item 12 registered as the last partner-free draft. Epic 16 remains blocked
on `ESTABLISHED` plus continuation; nothing in this audit moves either.
