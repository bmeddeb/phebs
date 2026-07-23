# Current-workflow baseline protocol — preregistration draft

*Draft artifact for pilot prerequisite item 12 (design phase). This protocol
grants no pilot, source-access, environment, measurement, Gate 0, or Epic 16
authority. It becomes binding only when every `<Gate 0>` field is filled, the
named migration owner and Gate 0 signatories approve it, and its digest is
recorded before any phebs prediction for the frozen baseline is available to a
participant in this study.*

## 1. Purpose and boundary

This protocol implements [PILOT_CHARTER.md](./PILOT_CHARTER.md) §8.1. It
measures the existing human process for producing a reviewable consumer
inventory and compares that process with the later phebs-assisted workflow.
It does **not** measure extractor accuracy, establish a static recall
population, or turn manual inventory entries, traffic observations, tickets,
or owner statements into source facts. Accuracy remains governed separately by
[ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md).

The result is one bounded comparison for one frozen RPC, source snapshot,
authorized universe, input set, and required review outcome. It is not a
general productivity estimate and cannot be generalized to another migration,
team, language, evidence pack, or source estate.

## 2. Record status

| Field | Value |
|---|---|
| Protocol schema | `current-workflow-baseline-v1-draft` |
| Prerequisite item | 12 |
| Owner | `<Gate 0>` |
| Migration owner / usefulness authority | `<Gate 0>` |
| Reviewer | `<Gate 0>` |
| State | `blocked_unassigned` |
| Required acceptance evidence | artifact digest, reviewer identity, timestamp, decision, unresolved findings |
| Measurement authority | none until charter gates authorize the relevant activity |

## 3. Frozen design inputs

Every field below is filled and signed at Gate 0. A blank, placeholder,
unexplained `N/A`, or value selected after either workflow result is visible
invalidates the comparison.

| Input | Frozen value |
|---|---|
| Canonical RPC identity (`/package.Service/Method`) | `<Gate 0>` |
| Source snapshot `S0` and tree/repository commitments | `<Gate 0>` |
| Authorized source and build-target universe | `<Gate 0>` |
| Required inventory row identity and fields | `<Gate 0>` |
| Reviewability checklist and migration-owner decision authority | `<Gate 0>` |
| Baseline mode: `prospective_observation` or `historical_reconstruction` | `<Gate 0>` |
| Manual-workflow tools and admitted evidence channels | `<Gate 0>` |
| Inputs that must be available before either timer starts | `<Gate 0>` |
| Participating roles and named principals | `<Gate 0>` |
| Start event and end event | `<Gate 0>` |
| Post-acceptance correction cutoff | `<Gate 0; no later than final pilot decision>` |
| Labor-recording increment and rounding rule | `<Gate 0>` |
| Time zone and timestamp source | `<Gate 0>` |
| `T_INITIAL_INVENTORY` / `T_INITIAL_INVENTORY_CONDITIONAL` | `<Gate 0>` |
| `T_LABOR_PASS` / `T_LABOR_CONDITIONAL` | `<Gate 0>` |
| Commitment, receipt, storage, and access-log mechanism | `<Gate 0>` |
| Protocol digest and record-schema digest | `<sealing>` |

The manual and phebs-assisted workflows use the same frozen question, `S0`,
authorized universe, required inventory shape, reviewability checklist, and
end authority. Any necessary difference in tools or evidence inputs is frozen
and reported as part of the intervention; it is never introduced after seeing
a result.

## 4. Baseline mode

Exactly one primary mode is preregistered.

### 4.1 Prospective observation (preferred)

The named participants execute the current manual workflow against `S0` using
only the frozen current-practice tools and evidence channels. The event log is
recorded contemporaneously. No phebs extraction or prediction for this frozen
question may be run or exposed until the baseline artifacts and commitment are
sealed.

### 4.2 Historical reconstruction

Historical reconstruction is allowed only when the migration already has an
auditable manual run tied to the same frozen question and source snapshot.
Start, acceptance, labor, additions, and corrections must be recoverable from
timestamped source records. Interviews may explain a record but cannot invent
missing time or labor. Missing mandatory events or labor categories make the
baseline descriptive-only and unable to satisfy the workflow-improvement gate.

A prospective run may be reported as sensitivity context for a historical
baseline, or vice versa, but the two modes are never pooled and only the frozen
primary mode supplies the gate comparison.

## 5. Prediction blindness and order

1. The manual baseline is completed, reviewed, frozen, and receipted before
   any participant sees a phebs prediction for the frozen question.
2. Existing migration documents and inventories remain admissible when they
   are genuine inputs to current practice; their provenance and prior exposure
   are recorded rather than erased.
3. A principal who sees an unsealed phebs prediction cannot add, remove, or
   reinterpret manual-baseline rows, events, labor, or acceptance decisions.
4. If a prediction is exposed before the baseline commitment, the comparison
   is `INVALID`. It may be redesigned only around a new unseen freeze; the
   contaminated run is retained as a failed record, not edited into validity.
5. The phebs-assisted workflow begins only after the manual commitment. Its
   participants may learn from ordinary migration work, so the report names
   the fixed order and does not claim a randomized causal effect.

## 6. Timer and labor rules

### 6.1 Elapsed time

The timer starts only when the frozen question, `S0`, universe, and every
preregistered required input are available to the workflow. Queueing or delay
after that point remains inside elapsed time. The timer ends at the first
recorded instant when the named migration owner accepts the inventory as
reviewable under the frozen checklist.

Rejected submissions, corrections, reruns, missing-input recovery, and owner
routing before acceptance remain inside elapsed time. An informal preview,
partial list, tool completion, or pilot-lead declaration cannot stop the timer.

### 6.2 Labor

Each principal records active labor in exactly one category per interval:

- `discovery` — finding candidate occurrences, deployables, or services;
- `triage` — classifying, deduplicating, excluding, or resolving candidates;
- `routing` — identifying and contacting a service or recorded owner;
- `rework` — correcting, rerunning, or rebuilding rejected work;
- `owner_review` — migration-owner or delegated review of the inventory.

Simultaneous work by two people counts as two people’s labor. Overlapping
entries for one principal are rejected. Waiting time is not active labor but
remains in elapsed time. Uncertain reconstructed labor is recorded as an
explicit interval or bound; it is never silently rounded down. When a bounded
record remains admissible, gate evaluation uses the conservative reduction
bound:

```text
labor_reduction_lower =
  (L_DTR_manual_lower - L_DTR_phebs_upper) / L_DTR_manual_lower
```

The charter's labor gate uses:

```text
L_DTR(workflow) = discovery + triage + routing labor hours
labor_reduction = (L_DTR(manual) - L_DTR(phebs)) / L_DTR(manual)
```

`L_DTR(manual) <= 0`, a missing mandatory category, or incompatible recording
rules make the labor comparison undefined and therefore unable to pass.
`rework` and `owner_review` are separately mandatory and are also summed into:

```text
L_FULL(workflow) = L_DTR(workflow) + rework + owner_review
```

`L_FULL` cannot replace the preregistered gate formula, but it must accompany
it so a nominal gate result cannot hide higher total human cost. One-time
security/provisioning/integration labor and steady-state repeat-query labor are
recorded in separate ledgers and reported, never selectively folded into one
workflow.

## 7. Required event log

The canonical event log is UTF-8 JSON Lines sorted by `(started_at, event_id)`.
Each row contains:

| Field | Rule |
|---|---|
| `event_id` | unique opaque identifier |
| `workflow` | `manual` or `phebs_assisted` |
| `principal`, `role` | named Gate 0 participant and frozen role |
| `event_type` | `availability | work | waiting | submission | rejection | acceptance | addition | correction` |
| `category` | one labor category from §6.2 iff `event_type=work`; otherwise `null` |
| `started_at`, `ended_at` | RFC3339 timestamps; end must be at or after start, with equality allowed only for marker events |
| `active_minutes` | derived under the frozen rounding rule for `work`; zero otherwise |
| `action` | concise description without hidden source content |
| `evidence_refs` | zero or more identifiers from the evidence manifest |
| `rework_of` | prior event ID when applicable, otherwise `null` |
| `notes` | optional explanation; never used to override structured fields |

Start availability, each submission, rejection, acceptance, later addition,
and correction are separate events even when they carry no active labor.
Raw confidential content stays in its authorized system; the baseline stores
only the minimum approved reference and digest needed for reproduction.

## 8. Evidence-channel manifest

Every admitted input is preserved as a distinct channel. Minimum channel
tokens are `migration_document`, `ticket`, `owner_outreach`, `build_query`,
and `traffic_observation`; Gate 0 may add bounded tokens before execution.

Each manifest row records `evidence_id`, channel, authorized locator, immutable
digest or provider revision, observation/query time, applicable source or
traffic window, collector, access scope, and the proposition it supports. For
traffic evidence it also records what absence cannot prove. Conflicts remain
separate rows and are surfaced in the inventory; they are not silently
arbitrated.

The evidence manifest demonstrates how current practice worked. It is not an
accuracy-gold set, an independently enumerated recall frame, or permission to
copy content outside its source system.

## 9. Inventory and usefulness record

The manual and phebs-assisted inventories use the same Gate 0-frozen row
schema. At minimum each row has an opaque row ID, canonical RPC, `S0`, proposed
service/deployable/owner identities where available, status
`candidate | reviewed | excluded | unresolved`, evidence references, creator,
creation time, and supersession reference. Corrections supersede rows; they do
not mutate history.

Before either workflow begins, the migration owner freezes a binary
reviewability checklist covering at least:

- the required scope and row fields are present;
- candidates are actionable enough to route for review;
- evidence and uncertainty are visible;
- unresolved and excluded scope is not hidden;
- the artifact format is usable in the migration's current process.

Every submission receives `accepted` or `rejected` plus checklist results and
rationale. Acceptance is usefulness judgment, not source-truth certification.
The first accepted record stops elapsed time. Every addition, removal,
identity correction, ownership correction, and status correction through the
frozen correction cutoff is appended with actor, timestamp, reason, and
evidence; post-acceptance correction labor is reported separately for both
workflows.

## 10. Calculation and gate projection

For each workflow the sealed calculation report derives:

- elapsed time to first reviewable inventory;
- labor by category and role, `L_DTR`, `L_FULL`, and any conservative bounds;
- number of submissions and rejections before acceptance;
- inventory row counts by status at acceptance;
- additions and corrections through the frozen cutoff;
- one-time setup and repeat-query labor in their separate ledgers.

The charter §9 projection is mechanical:

| Verdict | Rule |
|---|---|
| `pass` | phebs elapsed time ≤ `T_INITIAL_INVENTORY` and exact labor reduction (or its conservative lower bound) ≥ `T_LABOR_PASS`, with a migration-owner accepted artifact |
| `conditional` | phebs elapsed time ≤ `T_INITIAL_INVENTORY_CONDITIONAL` and exact labor reduction (or its conservative lower bound) ≥ `T_LABOR_CONDITIONAL`, at least one pass threshold missed, with a migration-owner accepted artifact |
| `stop` | either conditional threshold missed, comparison undefined/invalid, or the migration owner rejects the artifact as unusable |

Thresholds are applied exactly as frozen; missing data cannot be imputed into a
pass. The report shows the manual and phebs values, absolute difference,
relative difference where defined, and all companion measures. With one anchor
workflow pair, it makes no statistical population or causal-effect claim.

## 11. Custody, commitment, and disclosure

The baseline artifact set contains:

1. filled protocol and frozen-input record;
2. participant/exposure attestations;
3. canonical event log;
4. evidence-channel manifest;
5. immutable inventory versions and reviewability decisions;
6. correction ledger through the frozen cutoff;
7. calculation output and gate projection;
8. manifest of filenames, byte lengths, and SHA-256 digests.

The manual set and manifest are committed and receipted through the Gate
0-approved mechanism before phebs predictions are unsealed. The phebs set uses
the same schemas and receives its own later commitment. Access is limited to
the frozen roles and logged. Published summaries omit confidential locators,
person-level labor unless approved, inaccessible object names, and row counts
that would disclose unauthorized scope.

Revocation, mandatory deletion, legal policy, and charter teardown rules
override retention. A digest proves which bytes were evaluated; it does not
authorize continued possession.

## 12. Fail-closed conditions

The comparison is `INVALID` and cannot satisfy the workflow gate if any of the
following occurs:

- RPC, `S0`, universe, required outcome, mode, or thresholds change after
  execution begins;
- a participant sees an unsealed phebs prediction before the manual
  commitment;
- the start event, first acceptance, required labor, or evidence provenance is
  missing or irreconcilable;
- the manual and phebs workflows use incompatible timer, labor, inventory, or
  reviewability rules;
- a rejection, rerun, correction, participant, or admitted channel is omitted;
- the migration owner cannot or will not decide reviewability under the frozen
  checklist;
- artifacts fail canonicalization, digest verification, authorization, or
  custody checks.

An invalid or unfavorable baseline remains part of the record. Remediation
requires a newly frozen, unseen comparison; it never edits the failed run into
a pass.

## 13. Open items that block sealing

1. Every `<Gate 0>` field in §§2–3.
2. The partner's actual inventory row identity, evidence locators, and
   reviewability checklist.
3. The approved confidential receipt and storage mechanism.
4. A record validator/calculator, if the Gate 0 signatories require automated
   enforcement; its exact bytes and digest must then be frozen before use.

Drafting this protocol completes no item state by itself. Item 12 remains
`blocked_unassigned` until its owner, eligible reviewer, digest-bound artifact,
decision, timestamp, and unresolved-findings record satisfy
[PILOT_PREREQS.md](./PILOT_PREREQS.md).
