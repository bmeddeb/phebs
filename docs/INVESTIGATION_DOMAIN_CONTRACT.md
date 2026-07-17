# Investigation domain contract

*Normative product-domain contract, v0.2 · governs the semantics exposed by
storage, API, UI, MCP, Review, and export. `PLAN.md` remains the authority for
architecture and implementation decisions; adopted implementation choices are
recorded there by ADR. Derived from [INVESTIGATIONS.md](./INVESTIGATIONS.md)
rev 3. Where the two product documents disagree, this contract wins and the
experience spec must be corrected.*

## 1. Core invariants

1. Extracted evidence, deterministic derivations, external observations,
   human assertions, dispositions, and decisions remain distinct records.
2. No empty result becomes an absence conclusion without a positive,
   claim-specific eligibility computation.
3. Every read, aggregate, diff, Review projection, MCP result, and export is
   authorized for the requesting principal at use time.
4. Facts and coverage publish atomically. A failed, canceled, or incomplete
   attempt can never become visible as a complete publication.
5. Semantic records are immutable or append-only. Mutable display metadata,
   current pointers, Watch enablement, and per-principal cursors are audited
   projections; their mutability does not alter historical evidence.
6. Inaccessible source, findings, denominators, and remainder existence are
   never disclosed indirectly through counts, identifiers, status, or diffs.

## 2. Entities and identity

| Entity | Identity | Mutability | Core fields |
|---|---|---|---|
| `Investigation` | ULID | display metadata and current pointers mutable; referent immutable | referent identity, claim family, title, owner, lifecycle state, current Revision |
| `Revision` | `(investigation, seq)` | **immutable** | normalized question/claim, decision sought, declared universe, snapshot policy, build configuration, pack/rule/schema selection, enumeration method, creator |
| `Run` | `(revision, seq)` | request immutable; current state derived from events | idempotency key, resolved input identities, requested snapshot, created-by |
| `RunEvent` | ULID | append-only | Run, attempt, prior/new state, actor, reason, timestamp |
| `RunArtifact` | scoped content digest | **immutable** | terminal status, snapshot and input manifests, coverage ledger, fact references, pin references, eligibility results, diagnostic data |
| `BaselineDesignation` | ULID | append-only; superseded or invalidated by later records | claim/workflow scope, Revision, published RunArtifact, accepting authority, rationale, timestamp |
| `Decision` | ULID | append-only; supersede or expire, never edit | claim scope, conclusion, Revision/RunArtifact/Baseline references, authority, rationale, policy, effective interval |
| `ReviewCursor` | `(principal, investigation)` | mutable audited projection | acknowledged ReviewItem identities and last-viewed comparison |
| `Disposition` | ULID | append-only; supersede, never edit | category, subject kind/identity, actor/authority, rationale, effective interval, policy/reference identities |
| `ReviewItem` | versioned deterministic digest | derived; never hand-created | Investigation, comparison/baseline, projection version, subject, delta, cause, lifecycle state |
| `Watch` | ULID | owner, enabled state, and current revision mutable and audited | owner principal, current WatchRevision, expiry, evaluation cursor |
| `WatchRevision` | `(watch, seq)` | **immutable** | Investigation/Revision binding, typed query, trigger policy, coalescing and noise controls |
| `Dossier` | opaque ULID plus root digest | **immutable** | format version, canonical manifest, authorization/redaction scope, validity statement, signature, predecessor |

Content digests are integrity fields, not globally enumerable public
identifiers. They are namespace-bound and exposed only through an authorized
occurrence or object association so digest equality cannot become an
existence oracle.

## 3. Investigation, decision, and Run lifecycles

### Investigation lifecycle

| From | To | Requirement |
|---|---|---|
| `draft` | `active` | first Revision is frozen and authorized |
| `draft` | `archived` | explicit abandonment record with actor and reason |
| `active` | `concluded` | active Decision of an allowed workflow type, including an explicit `no_decision` close |
| `concluded` | `active` | authorized reopening or a pack-defined material delta that supersedes/invalidates the governing Decision |
| `active` or `concluded` | `archived` | explicit archival record; active Decision history retained |

`archived` is terminal but readable subject to authorization and retention.
Monitoring is represented by a Watch, not by overloading the Investigation
lifecycle. Reopening never erases or edits the earlier Decision.

### Run lifecycle

Run state is the projection of append-only RunEvents:

```text
queued → enumerating → analyzing → publishing → published
   └─────────────── from any pre-terminal state ─→ failed | canceled
```

- `published`, `failed`, and `canceled` are terminal.
- A `published` RunArtifact contains one atomically visible fact set and its
  complete coverage ledger.
- A `failed` or `canceled` RunArtifact may retain diagnostic and coverage
  evidence, but it contains no published fact set.
- Fact diffs and Baseline designations require published RunArtifacts.
  Coverage/status comparisons and diagnostic dossiers may reference failed
  artifacts, but must never present their absent facts as removals.

## 4. Revision versus fork

A new Revision is required when the Investigation's referent and claim family
remain unchanged but any of these changes:

- normalized question or decision sought;
- declared universe, scope rules, or enumeration method;
- snapshot policy or build configuration;
- evidence-pack, extractor, adapter, rule, schema, or identity semantics;
- required external-input contract.

A **fork** creates a new Investigation linked by `forked_from` when the
referent or claim family changes: a different contract, relationship type, or
a claim whose prior Decisions and dispositions are not meaningful for the new
question.

Disposition carry-forward is never inferred from sequence alone. It requires
a stable logical subject identity, compatible pack and policy semantics, and
an explicit carry-forward record under §9. Nothing carries across a fork.

## 5. Execution and publication invariants

- Run creation requires an idempotency key over Revision, resolved snapshot,
  input-manifest digest, and requested pack versions. A concurrent duplicate
  returns the existing Run rather than creating competing publications.
- Attempts are recorded through RunEvents. A retry before terminal completion
  creates a new attempt under the same Run; rerunning a terminal request
  creates a new Run.
- Cancellation and failure close the publication lease. A late worker cannot
  publish after either terminal event.
- Publication commits facts, coverage, provenance, eligibility results, and
  the RunArtifact identity atomically.
- Changing the Investigation's current Revision uses compare-and-swap or an
  equivalent serialized transition and emits an audit/activity event.
- Pin ownership is explicit. Revocation, mandatory deletion, legal policy,
  and approved retention rules override pins. Garbage collection must prove
  that no authorized retained Dossier, Baseline, or active Investigation owns
  the pin.

## 6. Absence-decision eligibility

Eligibility is computed per `(claim, principal visibility projection,
Revision, published RunArtifact)` and is never operator-set. An absence-shaped
conclusion is eligible only when **all** are true:

1. the principal's current eligible universe is independently enumerated by
   the method frozen in the Revision and reconciles to the RunArtifact;
2. every in-scope unit is accounted for, with zero `partial`, `failed`, or
   unreconciled units;
3. the pack's processing-state accounting and outcome-rate gates pass;
4. exclusions are predeclared and the pack's decision mapping explicitly
   permits them for this exact claim and negative wording;
5. every required semantic interpretation and attribution hop is `resolved`
   or validly `not_applicable`;
6. the pack is `released`, its validation is unexpired, and that validation
   applies to the exact extractor, rule, schema, supported constructs, and
   intended workflow;
7. source, build, catalog, ownership, deployment, policy, and other required
   inputs satisfy their individual freshness rules;
8. the analysis itself is fresh for the claim; and
9. the underlying result is complete—no output cap, truncation, unfinished
   pagination, or failed shard is being interpreted as absence.

The result contains a Boolean, a versioned qualification-template ID, and
zero or more machine-readable blockers. Core blocker codes are:

```text
SCOPE_NOT_ENUMERATED
VISIBILITY_SCOPE_NOT_RECONCILED
UNITS_UNRECONCILED
UNITS_PARTIAL
UNITS_FAILED
EXCLUSIONS_NOT_PERMITTED
OUTCOME_GATE_FAILED
SEMANTIC_UNRESOLVED
ATTRIBUTION_UNRESOLVED
PACK_NOT_RELEASED
PACK_VALIDATION_EXPIRED
VALIDATION_NOT_APPLICABLE
INPUT_STALE
STALE_ANALYSIS
RESULT_TRUNCATED
```

Packs may add namespaced, versioned blocker codes. UI copy, MCP qualification,
and dossier wording render from the same eligibility result and template
identity; tools do not author their own negative language.

## 7. Authorization, sharing, and revocation

- Object authorization and evidence authorization are independent. Permission
  to know an Investigation exists does not grant access to its question,
  scope, counts, findings, evidence, or prior visibility projection.
- Every read reauthorizes occurrences and computes a principal-specific view
  over the principal's current universe. A stored RunArtifact is never a
  static grant.
- Sharing grants only the explicitly authorized object metadata. A recipient
  is not told whether the creator saw additional repositories, facts, counts,
  denominators, or a larger universe. The recipient's eligibility is computed
  against their own reconciled visible universe.
- Information about a prior or changed visibility projection is available
  only to a principal independently authorized to know both projections.
- Counts, coverage, diffs, ReviewItems, Watch results, identifiers, and error
  messages obey the same non-disclosure rule as source evidence.
- Ownership transfer re-evaluates object roles, decision authority, evidence
  access, Watches, and affected cursors; it does not indiscriminately erase
  unrelated principals' acknowledgements.
- Revocation applies to pins and cached projections. Retention never overrides
  authorization loss, mandatory deletion, or legal policy.
- Export is generated for the requesting principal's current scope and is
  labeled with handling classification and redaction scope. An exported
  dossier is outside phebs' continuing access control; offline possession does
  not imply current authorization. Reopening it inside phebs reauthorizes every
  displayed object and evidence occurrence.

## 8. Diff comparability and causal classification

Two published RunArtifacts support an ordinary fact delta only when the pack's
versioned comparability rule establishes compatible:

- claim and logical subject identity;
- fact schema, rule, extractor, adapter, and identity semantics;
- declared universe, enumeration method, snapshot policy, and build
  configuration;
- required external-input semantics; and
- principal visibility projection.

Same-Revision Runs are expected to satisfy these constraints but are not
assumed comparable when inputs or authorization violate the pack rule.
Cross-Revision comparison is a **comparison report**, not a delta, unless the
pack proves compatible identity and semantics for the aligned fields.

Core causal categories are:

```text
relationship_added_traced
relationship_removed_traced
source_modified
scope_expanded
scope_narrowed
authorization_changed
pack_or_rule_changed
schema_or_identity_changed
build_configuration_changed
enumeration_changed
external_metadata_changed
analysis_failed
input_stale
reclassified
attribution_shift
disposition_or_decision_changed
unknown_cause
```

Only `relationship_removed_traced`, under comparable semantics and stable
logical identity, may render as `removed`. Scope or authorization changes,
analysis failure, stale input, unresolved attribution, and unknown causes
render as `unknown`, `not comparable`, or their exact cause—never removal.
`reintroduced` requires the same stable logical identity and a retained,
positively traced removal tombstone.

## 9. Human records: assertions, dispositions, governance, and Decisions

These families are distinct:

| Family and example | Who may create it | Expiry/carry-forward | Effect on evidence |
|---|---|---|---|
| Action intent — `will_migrate` | authorized subject owner | expiry required; explicit carry-forward only for stable subject + compatible Revision | none |
| Human classification assertion — `not_production` | pack-declared classifier authority | expiry required; re-evaluated each Run | none; mismatch creates ReviewItem |
| Evidence challenge — `false_attribution` | any principal authorized for the subject and evidence | remains open until append-only adjudication | none; enters pack quality review |
| Governance request — `exception_requested` | authorized requester | open until external decision or expiry | none |
| External governance reference — `exception_granted`, `denied`, `expired` | imported from the external exception authority | follows authority's effective interval; must reference external decision ID | may change workflow disposition; never validation, coverage, or fact truth |

`owner_unknown` is not a disposition. It is a derived attribution condition
and may project a ReviewItem.

Every human record contains a subject kind and stable subject identity, actor,
authority basis, rationale, created/effective/expiry timestamps, superseded
record identity where applicable, and referenced fact, coverage, policy, and
external-decision identities when applicable. Fields that do not apply are
absent rather than filled with invented policy values.

### Decisions

A Decision is an immutable human conclusion over a named claim and exact
Revision, published RunArtifact or Baseline, eligibility result, and visible
scope. It records the authorized decision-maker, authority source, rationale,
policy version where applicable, effective interval, and conclusion type. A
`no_decision` close is an explicit Decision type, not the absence of a record.

Decisions never rewrite evidence, convert unknown into evidenced, waive pack
release/validation rules, or broaden authorization. Supersession, expiry, or a
pack-defined material delta creates a new record and may reopen the
Investigation; the historical Decision remains reproducible.

### Baselines and personal review state

A BaselineDesignation is an organizational comparison decision, scoped to a
claim/workflow and published RunArtifact. Its state is derived from append-only
accept, supersede, and invalidate records. Multiple scopes may have different
active baselines. A ReviewCursor is personal acknowledgement state and can
never create or alter a Baseline or Decision.

## 10. Dossier contract

A Dossier has an opaque identifier and a separately recorded integrity root.
Its versioned format defines canonical serialization, hash algorithm, domain
separation, digest tree, and—when used as a decision artifact—service
signature and verification key identity.

The canonical manifest contains:

- question, Investigation, Revision, primary RunArtifact/Baseline/Decision,
  snapshots, input manifests, and pack-card identities;
- digest of every included artifact and the complete eligibility result;
- embedded evidence for cited findings and digest+authorized-locator
  references for non-embedded material;
- authorization/redaction scope and handling classification;
- supported claims, blockers, issued-at time, freshness/validation state at
  issuance, expiry or review-due rule; and
- predecessor Dossier identity when superseding an earlier export.

Offline verification establishes dossier integrity and signature authenticity
only. Reproducing non-embedded evidence requires authorized source bytes;
successful verification does not establish current authorization, freshness,
or continuing validity. A Dossier never updates. Mandatory deletion and legal
policy may invalidate retained references even though the historical digest
remains cryptographically well formed.

## 11. Review projections and Watches

ReviewItems are deterministic projections of:

```text
Investigation
+ WatchRevision or Baseline/comparison pair
+ pack projection id/version
+ authorized logical subject
+ delta and causal classification
+ relevant human-record and Decision state
```

Their identifiers are domain-separated and versioned. Built-in lifecycle is
`open`, `superseded`, or `expired`; acknowledgement is per-principal in a
ReviewCursor and does not mutate the ReviewItem. Every projection and count is
reauthorized at read time. Review may project processing failure, stale input,
or `owner_unknown` without pretending those conditions are dispositions.

ReviewItems are never hand-created and have no comments, arbitrary
assignments, custom states, or due dates. Discussion and task execution live in
external systems.

A Watch has a stable identity and immutable WatchRevisions. Changing its typed
query, Revision binding, trigger, or thresholds creates a WatchRevision;
enablement and expiry are audited mutable controls. Evaluation occurs only on
eligible publication events, under the Watch owner's current authorization.
Revoked or unreconciled authorization suspends evaluation without revealing
hidden changes. Watches define quotas, coalescing, deduplication, expiry, and
an evaluation cursor; they feed Review only. A failed or non-comparable run
can generate a coverage/comparability ReviewItem but never a false consumer
removal.

## 12. Executable pack and cross-document conformance

An executable pack manifest is validated against the complete evidence-pack
card—not §11 alone. It binds predicates and supported constructs, coverage and
decision rules, qualification templates, comparability and identity semantics,
Review projections, query facets and census columns, MCP actions, validation
status, authorization behavior, and operating limits. The card and manifest
share stable versioned identities; release is blocked when they disagree.

[INVESTIGATIONS.md](./INVESTIGATIONS.md) defines the experience and sequencing
derived from this contract. The evidence-pack card defines each pack's allowed
claim. `PLAN.md` and ADRs decide implementation architecture. No downstream
document may weaken the authorization, evidence, eligibility, or immutability
invariants in this contract.