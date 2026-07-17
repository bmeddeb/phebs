# Investigation domain contract

*Normative contract, v0.1 · governs storage, API, UI, MCP, and review.
Derived from [INVESTIGATIONS.md](./INVESTIGATIONS.md) rev 2; where they
disagree, this contract wins and the other document is corrected.*

## 1. Entities

| Entity | Identity | Mutability | Core fields |
|---|---|---|---|
| `Investigation` | ULID | mutable metadata only | normalized question, intent, owner, state, current revision |
| `Revision` | `(investigation, seq)` | **immutable** | scope universe, snapshot policy, pack selection + versions, enumeration method, created-by |
| `Run` | `(revision, seq)` | **immutable** | snapshot digest, manifest, coverage ledger, facts refs, pins, eligibility result |
| `Baseline` | `(revision, run)` | append-only designations | accepting principal, rationale, timestamp |
| `ReviewCursor` | `(principal, investigation)` | last-acknowledged run + item set | — |
| `Disposition` | ULID | append-only; supersede, never edit | type, subject fact/coverage ids, actor, rationale, expiry |
| `ReviewItem` | deterministic hash of (delta, cause, subject) | derived; never hand-created | delta, cause, state |
| `Watch` | ULID | mutable policy | query, revision binding, trigger = publication events only |
| `Dossier` | content digest | **immutable** | format version, manifest, redaction scope, validity statement |

## 2. Lifecycle and transitions

Investigation states: `draft → active → concluded → archived`; `concluded`
requires a recorded decision (disposition of type *intent* or *governance*
at investigation scope) or an explicit no-decision close. No state skips;
`archived` is terminal but readable.

Run states: `queued → enumerating → analyzing → published | failed |
canceled`. Only `published` runs may be baselines, diffed, or exported.
`failed` runs retain their coverage ledger — failure is evidence.

Allowed creations: new snapshot ⇒ new **Run**; changed universe, question
normalization, pack selection, or enumeration method ⇒ new **Revision**;
materially different question ⇒ new **Investigation** (see §3). Nothing is
ever edited in place; supersession is the only correction mechanism.

## 3. Revision vs. fork

A new Revision is permitted when the question's *referent* is unchanged
(same contract, same claim shape) and only scope/configuration changes. A
**fork** (new Investigation linked `forked_from`) is required when the
referent changes: different contract, different relationship type, or a
claim the original question's dispositions could not carry forward.
Dispositions carry forward across Revisions per their type rules (§7);
they never carry across forks.

## 4. Absence eligibility

Computed per `(claim, revision, run)`; never stored as operator input.
Eligible iff **all**: (a) the eligible universe was independently
enumerated by the method frozen in the Revision; (b) every unit is in a
terminal processing state; (c) the governing pack's outcome-rate gates
(analyzed / partial+failed / excluded) pass; (d) every attribution hop the
claim requires is `resolved` or `not_applicable`; (e) the pack's
validation is unexpired; (f) the run is not stale per the pack's freshness
rule. Failure yields one or more **blocker codes** from the pack's
declared vocabulary: `UNITS_FAILED`, `EXCLUSION_RATE_EXCEEDED`,
`OUTCOME_GATE_FAILED`, `ATTRIBUTION_UNRESOLVED`,
`PACK_VALIDATION_EXPIRED`, `SCOPE_NOT_ENUMERATED`, `STALE_ANALYSIS`,
`AUTHORIZATION_NARROWED`. The header badge, envelope field, and
negative-result wording are all derived from this one computation.

## 5. Authorization, sharing, revocation

- Every read re-evaluates the requesting principal's scope at query time;
  a stored Run is never a static grant. Sharing grants the *object*, not
  its results: a recipient sees the intersection of the Run and their own
  universe, with narrowed visibility surfaced as `AUTHORIZATION_NARROWED`
  (never silently smaller counts).
- Ownership transfer re-evaluates visibility and voids cursors.
- Counts, coverage denominators, and diffs never disclose inaccessible
  existence.
- Revocation applies to pinned runs: retention never overrides
  authorization loss, mandatory deletion, or legal policy.
- Dossier export redacts to the recipient scope at export time; reopening
  a dossier inside phebs re-authorizes against current ACLs before
  display. The exported artifact itself is outside phebs' control and is
  labeled with its redaction scope.

## 6. Diff comparability and delta causes

Two Runs are comparable iff same Revision, same pack versions, and
compatible coverage class (per the pack's declared comparability rule).
Diffs between non-comparable runs must render as a *revision comparison*
with per-fact cause classification, never as unqualified added/removed.

Delta causes (exhaustive): `source_added`, `source_deleted` (traced),
`extractor_changed`, `authorization_changed`, `catalog_changed`,
`analysis_failed`, `scope_narrowed`, `reclassified`, `unresolved_shift`.
`analysis_failed` and `authorization_changed` are prohibited from
rendering as `removed` in any surface, including MCP.

## 7. Disposition taxonomy

| Type | Authority | Expiry | Carry-forward | On conflict with evidence |
|---|---|---|---|---|
| Intent (`will migrate`) | subject owner | required | across revisions until expiry | flagged, not blocked |
| Classification assertion (`not production`) | subject owner | required | re-verified each run against pack classification | mismatch generates a ReviewItem |
| Evidence challenge (`false attribution`) | any authorized principal | until adjudicated | n/a | enters pack quality review; **never mutates the fact** |
| Governance request (`exception requested`) | external exception authority | mandatory | per authority's grant | expired grant reverts to default |
| System condition (`owner unknown`) | system-derived | n/a | recomputed each run | cleared only by resolution |

All dispositions record actor, rationale, timestamps, referenced fact and
coverage identities, and the policy version in force.

## 8. Dossier contract

Format-versioned, sealed, self-describing: manifest (question, revision,
runs, snapshots, pack card identities, digest of every included artifact);
embedded evidence for cited findings, digest+locator references for the
remainder; redaction scope; validity statement (claims supported, as of
snapshot, under which pack validations, with eligibility and blockers);
offline verification procedure (digest-chain check requiring no phebs
instance). A Dossier never updates; a newer export is a new Dossier
referencing its predecessor. The Dossier is the product boundary:
Workbench and all external consumers integrate here.

## 9. Review projection

ReviewItems are pure functions of (delta, cause, disposition state,
pack projections): deterministic ids, deduplicated, superseded by newer
deltas, acknowledged per-principal via ReviewCursor, expired by rule.
No hand-created items, no comments, no custom states, no due dates.
