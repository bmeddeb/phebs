# Gate 0 readiness audit

*Working audit for the pilot's entry gate, produced by attempting to run
Gate 0 against current reality (2026-07-22). This document grants nothing
and decides nothing: it maps every [PILOT_CHARTER.md](./PILOT_CHARTER.md)
§6 Gate 0 requirement to its current state, owner, and next action. States
here are descriptive; only the charter's own signatures close Gate 0.*

**Owner aim:** Ben Meddeb, as pilot lead and item-12 owner, is driving to
**unlock Gate 0** — meaning complete every charter §6 freeze requirement so
the pilot clock can start. Unlock requires partner fills and named Gate 0
signatures; Owner action alone cannot close the gate.

## Legend

- **drafted** — a reviewable artifact exists on the gate-neutral track.
- **design-accepted** — the gate-neutral prerequisite is accepted as a form;
  partner-shaped fields and Gate 0 sealing remain open.
- **draftable now** — phebs-side design work, no partner required.
- **partner-blocked** — requires a named design partner or their people.
- **approval-blocked** — requires a named human review or sign-off.
- **satisfied-conditionally** — the governing prerequisite is satisfied only
  under an explicit condition that remains binding on later gates.

## Audit

| Gate 0 requirement (charter §6) | State | Owner | Next action toward unlock |
|---|---|---|---|
| Sponsor, roles, time allocation, proposed RPC, decision authority named | partner-blocked | sponsor + Ben | Owner solicits named §5 principals from the partner; every role except pilot lead is still `<name>` |
| Canonical IDL identity and source universe confirmed | partner-blocked | migration owner | Owner requests frozen RPC + repository set from the migration owner |
| IP/OSS/provenance review approved or cleared for the bounded evaluation | approval-blocked | OSS/Legal reviewers | Owner packages the bounded-evaluation ask and names/requests OSS/Legal reviewers |
| Terminal external Go/gRPC benchmark record satisfies the §9 disposition | satisfied-conditionally | operator | already accepted under charter v0.2; Gate 0 freeze must still record `internal-validation-required` |
| Pilot extractor equals the benchmark-bound candidate, or has an approved versioned bridge | approval-blocked | Gate 0 signatories | Owner prepares the unscored identity/reproducibility/mechanics bridge draft for Gate 0 binding |
| Evidence-pack card at status `shadow` for the Go/gRPC pack, internal shadow evaluation filling the internal/domain-shift table | approval-blocked | Ben + security reviewer | Owner advances the card ladder inputs; security approval and authorization model still required |
| Current-workflow baseline protocol preregistered (§8.1) | design-accepted | Ben → Gate 0 signatories | form accepted at `913c765` / `c8e631854e177b5e540117d4fe70296c4cc1687f2d9777fd60fd00d985d056bc` by Owner Ben Meddeb and reviewer Claudia; Gate 0 still locked; Owner next collects partner fills and brings the sealed §8.1 package to Gate 0 |
| Statistical accuracy-gold protocol preregistered (§8.2) | drafted | Ben → Gate 0 signatories | Owner next: design-accept item 9, then collect `<Gate 0>` fills and seal with item 12 |
| Accuracy protocol defines population, frames, unit, strata, missing-label handling, power, custody, and distinct call-site / attribution-hop / end-to-end labels | drafted | Ben → Gate 0 signatories | Owner next: design-accept item 11 dependency preview; partner catalog shapes still freeze only at Gate 0 |
| §9 pass / conditional / stop thresholds filled and signed | partner-blocked + approval-blocked | sponsor + Ben | Owner proposes threshold worksheet against partner workload; sponsor + migration owner must sign at Gate 0 |
| Every role, date, resource limit, `T_*`, and protocol field filled; blanks block release | blocked by all above | Gate 0 signatories | mechanical once the rows above resolve; Owner maintains the blank-field checklist |
| No conflicting pilot or production dependency | draftable now | Ben | Owner drafts the one-page statement now; freeze/sign only at Gate 0 against then-current schedule |

## Owner path to unlock Gate 0

Ordered Owner work. None of these steps closes Gate 0 by itself; each removes
a phebs-side or coordination blocker so the gate can be signed.

| Step | Owner action | Unlocks when |
|---|---|---|
| 1 | Design-accept remaining partner-free Gate 0 forms: items 9, 10, and 11 | acceptance records exist (forms only; not seals) |
| 2 | Draft the no-conflicting-dependency one-pager and the unscored extractor-bridge worksheet | reviewable drafts ready for Gate 0 |
| 3 | Advance evidence-pack card inputs toward `shadow` with the security reviewer | card status `shadow` + internal/domain-shift table path defined |
| 4 | Solicit partner naming of §5 roles, frozen RPC/`S0`/universe, catalogs, inventory shape, reviewability checklist, and §9 `T_*` values | every partner-shaped `<Gate 0>` / `<name>` / `<value>` filled |
| 5 | Assemble the Gate 0 freeze package: sealed §8.1 + §8.2 digests, bridge, evidence-pack card, thresholds, blank-field checklist, `internal-validation-required` disposition | Gate 0 signatories can sign |
| 6 | Obtain Gate 0 signatures | **Gate 0 unlocks; pilot clock may start** |

Hard stops the Owner cannot waive: unnamed partner roles, blank protocol
fields, missing OSS/Legal or security approvals, and any attempt to transfer
accuracy from the unscored external benchmark.

## What this audit establishes

The accepted charter v0.2 closes the structural conflict. Running the gate
today now halts only on ordinary prerequisites:

1. **The partner** — roles, RPC, universe, catalogs, thresholds, and both
   verification-phase environments remain partner-shaped, as
   [PILOT_PREREQS.md](./PILOT_PREREQS.md) always said.
2. **Human approvals** — IP/OSS/provenance, security, artifact bridging,
   evidence-pack promotion, and final Gate 0 signatures remain ungranted.
3. **Owner unlock track** — item 12 is design-accepted by Owner Ben Meddeb
   and reviewer Claudia; the Owner's authorized next actions above are aimed
   at Gate 0 unlock. Design acceptance is not sealing, does not unlock
   Gate 0, and does not start the pilot clock.

Every partner-free protocol artifact is now drafted: items 9–10 are drafted and
scope-corrected, item 11 supplies the dependency-preview hop sheets, and item
12 is design-accepted as the workflow-baseline preregistration form. The
Owner's aim is to clear the remaining phebs-side forms (items 9–11), draft the
Gate 0 worksheets, and bring a complete freeze package to the partner and
signatories. Epic 16 remains blocked on an `ESTABLISHED` pilot
internal-validation record plus continuation; unlocking Gate 0 is necessary
but not sufficient for Epic 16.
