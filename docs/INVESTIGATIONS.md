# phebs Investigations — the persistent object around consequential work

*Product-experience spec, rev 3 · July 2026 · post-gate productization
(VISION.md sequencing step 2 given concrete shape). Nothing here expands the
pilot ask. Normative product semantics live in
[INVESTIGATION_DOMAIN_CONTRACT.md](./INVESTIGATION_DOMAIN_CONTRACT.md); this
document defines their user experience.*

One noun everywhere: **Investigation** — UI navigation, API resource naming,
MCP fields (`investigation_id`), and product documentation. Concrete routes and
persistence architecture remain governed by `PLAN.md`, ADRs, and the generated
API contract.

## Thesis

An Investigation is a frozen engineering question plus the durable evidence
needed to act on it: authorized source universe, immutable Revisions and Run
artifacts, evidence-pack versions, findings, coverage gaps, attribution chain,
human records, Decisions, Baselines, and history. Search stays fast and
stateless; users promote consequential or recurring questions into an
Investigation.

The lifecycle productizes machinery phebs already operates or has designed:

| Investigation concept | Foundation |
|---|---|
| Frozen question + scope | analysis manifest and charter Gate-0 freeze |
| Immutable Revision and RunArtifact | atomic extraction publication plus the domain contract |
| Snapshot pinning and export | `PinRun`, proof-aware retention, dossiers |
| Evidence reproduction | `ResolveEvidence` plus authorized occurrence associations |
| Human records | pack-card decision semantics, kept separate from evidence |
| Absence eligibility | claim- and principal-specific computation from coverage and pack rules |
| Regression Watch | publication-event reevaluation feeding Review |

`PinRun` and `ResolveEvidence` are supporting primitives, not the complete
Investigation persistence layer. Revisions, RunEvents and RunArtifacts,
authorization projections, Decisions, Baselines, dispositions, Watches,
ReviewItems, dossiers, audit history, idempotency, and pin ownership complete
the model.

## Flow

```text
search or ask
  → choose a typed question and decision sought
  → preview and freeze scope
  → run released evidence packs asynchronously
  → inspect findings, coverage, and unknowns
  → resolve attribution and human-record gaps
  → review comparable changes
  → record an authorized human Decision
  → seal/export a Dossier
  → Watch for material deltas or expiry
```

## Creation experience

Against a giant monorepo, creation is not synchronous CRUD. Guided creation
shows the normalized claim, intended decision, principal-visible universe,
snapshot/build policy, selected pack versions, enumeration method, required
inputs, authorization preflight, estimated work, and hard resource limits
before freezing a Revision.

After submission, the UI exposes queued/enumerating/analyzing/publishing state,
progress without existence leakage, cancellation, retry status, partial or
failed diagnostics, and the exact immutable RunArtifact on publication. A
failed or canceled attempt never appears as a complete result.

## Navigation and persistent header

Top level: **Search · Investigations · Review · Watches · Activity · Admin**.
Inside an Investigation: **Overview · Census · Changes · Coverage · Evidence ·
Human Records · Decisions · Activity**.

The header always shows:

- normalized question, referent, claim family, and decision sought;
- Revision, source snapshot, build configuration, and current Baseline;
- the requesting principal's authorized universe projection;
- evidence-pack, extractor, rule, and schema versions;
- source and external-input freshness;
- owner and Investigation lifecycle state; and
- per-claim absence-decision eligibility, qualification template, and blocker
  codes computed by the domain contract—never operator-set.

The Overview leads with four cards: **what is evidenced · what remains unknown
· what changed · what needs action**. It separates evidence basis, semantic
resolution, processing coverage, attribution coverage, human records, and
Decision conclusion rather than collapsing them into one confidence state.

## Empty states are safety semantics

Never one generic “No results.” Render distinct, machine-backed states:

- no supported facts in complete, eligible scope;
- no facts among analyzed units, with partial/failed/unreconciled units;
- exclusions not permitted for the requested conclusion;
- unsupported or semantically unresolved construct;
- incomplete deployable/service/owner attribution;
- stale source, metadata, policy, or pack validation;
- current visibility scope cannot be reconciled;
- non-comparable Revision or RunArtifact;
- truncated/incomplete result; and
- failed or canceled analysis.

The UI never tells a principal that a creator or another reviewer saw a larger
inaccessible universe.

## Census, derivation, and Changes

The Census is a dense table first, not a graph first. Rows expand into the
authorized derivation chain from occurrence → build target → deployable →
canonical service → recorded owner, preserving ambiguous and unresolved hops.

Changes renders ordinary added/removed/reintroduced relationships only when
the pack proves comparable identity and semantics. Source, scope,
authorization, analysis-method, build-configuration, external-metadata,
failure, and attribution changes remain visibly distinct causes. Failed,
stale, inaccessible, or non-comparable facts never render as removed.

## Review

Review is a deterministic evidence projection, not work management. Core
queues include:

- new or positively reintroduced relationships since an accepted Baseline;
- changed since the principal's ReviewCursor;
- processing, coverage, freshness, or comparability regression;
- unresolved deployable/service/owner attribution;
- catalog/source ownership conflict;
- evidence challenge awaiting quality adjudication;
- external exception decision or expiry;
- pack release/validation expiry; and
- analysis failure.

Opening an item starts with the comparable delta or exact system condition,
then the evidence chain and permitted human record. Human families remain
separate: action intent (`will_migrate`), classification assertion
(`not_production`), evidence challenge (`false_attribution`), governance
request, and imported external governance decision. `owner_unknown` is a
system-derived attribution condition, not a disposition. No human record
mutates evidence, coverage, or eligibility.

ReviewItems are never hand-created. They are versioned, deduplicated,
superseded or expired deterministically, and acknowledged per principal. There
are no comments, arbitrary assignments, custom states, or due dates.

## Decisions, Baselines, and dossiers

A Decision is an authorized human conclusion tied to an exact claim, visible
scope, Revision, published RunArtifact or Baseline, eligibility result, and
policy identity where applicable. It can be superseded or expire but is never
edited. A new material delta may reopen a concluded Investigation without
erasing the earlier Decision.

A Baseline is an organizational designation; a personal ReviewCursor never
becomes one. A Dossier seals the selected Run artifacts, Baseline, Decision,
manifest, evidence, coverage, eligibility, validation identities, redaction
scope, integrity root, and validity statement. Offline verification proves
integrity/authenticity, not current authorization or freshness.

The Dossier is the product boundary: Workbench and external systems consume
versioned dossiers and proof references rather than reaching into mutable
Investigation internals.

## Agents: governed consumers of evidence

Every evidence-sensitive MCP result uses the same versioned envelope with
separate fields for:

- normalized claim, snapshot, current authorized universe, and filters;
- evidence basis and semantic resolution;
- typed facts and authorized proof references;
- processing coverage and attribution state per required hop;
- pack-defined decision conclusion;
- absence eligibility, blocker codes, and qualification-template ID/version;
- pack release, validation, rule, schema, extractor, and adapter identities;
- required-input freshness; and
- result completeness, pagination, truncation, and provenance.

Bounded tools: `find_contract_edges`, `get_contract_evidence`,
`explain_attribution`, `get_analysis_coverage`,
`compare_contract_snapshots`, `list_new_consumers`,
`verify_proof_reference`, `generate_review_checklist`. No `ask_phebs` prose
oracle. Every tool reauthorizes the principal and shares UI/API semantics.

Human-reserved actions—creating Decisions, approving external exceptions,
changing ownership, or declaring a migration complete—are enforced
server-side by permission and authority rules. Omitting a tool from an agent
menu is not a security boundary.

> phebs does not make an agent authoritative; it makes the agent's claims
> scoped, inspectable, and reproducible.

## Pack-driven modules (P2, internal only)

An `EvidencePackModule` executable manifest is validated against the complete
pack card. It registers predicates and constructs, workflows, facets, census
columns, evidence rendering, coverage/decision/qualification rules,
comparability and identity semantics, Review projections, MCP actions,
authorization behavior, validation state, and operating limits. Card and
manifest share versioned identities; disagreement blocks release. No
marketplace and no third-party SDK.

## First slice (dependency-ordered)

1. Domain entities, append-only lifecycles, atomic publication, pin ownership,
   and authorization projections.
2. Guided creation with scope/authorization preview, idempotent asynchronous
   execution, progress, cancellation, retry, and failure diagnostics.
3. Overview, Census, Coverage, and Evidence views—empty-state taxonomy first.
4. Immutable Revisions, RunArtifacts, eligibility results, Baselines,
   Decisions, and minimal Dossier export.
5. Comparable added/removed/changed relationship diff; non-comparable
   comparison reports remain explicit.
6. Structured MCP envelope on existing tools.
7. Minimal Review projection: new relationships, failed coverage, stale input,
   and unresolved attribution.

The derivation inspector ships only with the pilot-validated attribution
layer. Watches remain sub-notification: versioned saved questions re-evaluated
on eligible publication events, with quotas/coalescing/expiry, feeding Review
only—no email or chat delivery.

## Boundaries

A phebs feature must materially improve population discovery, provenance,
coverage, attribution, change detection, review, or evidence-backed action. Do
not add generic markdown workspaces, PRD/RCA authoring, workflow builders,
task/deadline tracking, policy authoring, portfolio dashboards, generic
assistant chat, a plugin marketplace, or Investigation comment threads.
Discussion, remediation execution, and program management live elsewhere.

phebs owns the Investigation through the sealed Dossier and authorized export.
Workbench owns programs, plans, and cross-Investigation portfolio work. The two
products meet at the Dossier boundary, not inside the evidence plane.