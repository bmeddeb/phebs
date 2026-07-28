# Investigations product experience

An **Investigation** is the persistent object around consequential engineering
work. Search remains fast and stateless; users promote a recurring or
decision-bearing question into an immutable, reviewable evidence history.

This document owns the user experience. Normative identities, lifecycle,
authorization, eligibility, comparison, and dossier semantics live in the
[Investigation domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md). Agent
projection lives in the [MCP envelope](./MCP_ENVELOPE.md). The implemented
surface remains default-dark or fixture-bound as stated in the
[roadmap](./ROADMAP.md).

## Product model

An Investigation binds:

- one normalized engineering question and intended decision;
- an authorized source universe and immutable Revision;
- asynchronous Runs and published RunArtifacts;
- exact pack, extractor, rule, schema, and input identities;
- findings, coverage, exclusions, failures, unresolved relationships, and
  attribution chains;
- immutable human records kept separate from machine evidence;
- comparable Baselines and Decisions; and
- audit history, proof references, and Dossier exports.

One noun appears in UI navigation, API resources, MCP fields
(`investigation_id`), and documentation.

## Flow

```text
search or ask
  → choose a typed question and decision sought
  → preview and freeze scope
  → run eligible evidence packs asynchronously
  → inspect findings, coverage, and unknowns
  → review comparable changes and attribution gaps
  → append an authorized human record or Decision
  → seal/export a Dossier
  → reevaluate on material publication or expiry
```

## Guided creation

Creation is a previewed workflow rather than synchronous CRUD. Before freezing
a Revision, show:

- normalized claim, referent, and decision sought;
- the principal-visible repository and input universe;
- snapshot and build policy;
- selected pack versions and enumeration method;
- authorization preflight and required inputs;
- estimated work and hard limits.

After submission, expose queued, enumerating, analyzing, and publishing states
without leaking inaccessible counts. Cancellation, retries, partial/failure
diagnostics, and the exact published RunArtifact remain visible. A failed or
canceled attempt never looks complete.

## Navigation

Top level: **Search · Investigations · Review · Watches · Activity · Admin**.

Inside an Investigation:
**Overview · Census · Changes · Coverage · Evidence · Human Records ·
Decisions · Activity**.

The persistent header identifies the question, Revision, source snapshot,
build configuration, Baseline, current authorized universe, pack/toolchain
versions, freshness, owner, lifecycle, and absence-eligibility blockers.

Overview answers four questions:

1. What is evidenced?
2. What remains unknown?
3. What changed?
4. What needs human action?

Do not collapse evidence basis, semantic resolution, processing coverage,
attribution coverage, human records, and Decision conclusion into one
confidence score.

## Empty states are safety semantics

Never render one generic “No results.” Distinguish:

- complete eligible scope with no supported facts;
- partial, failed, excluded, or unreconciled processing;
- unsupported or semantically unresolved constructs;
- incomplete deployable, service, or owner attribution;
- stale source, metadata, policy, or validation;
- visibility that can no longer be reconciled;
- non-comparable snapshots or artifacts;
- truncated/incomplete result windows; and
- failed or canceled analysis.

The UI never reveals that another principal saw a larger inaccessible universe.

## Census and changes

Census is a dense table before it is a graph. A row expands through the
authorized derivation chain:

```text
source occurrence → build target → deployable → logical service → recorded owner
```

Ambiguous, missing, and conflicting hops stay visible.

Changes renders added, removed, or reintroduced relationships only when the
pack proves comparable identity and semantics. Source, scope, authorization,
analysis method, build configuration, metadata, failure, and attribution
changes remain distinct causes. Failed, stale, inaccessible, or
non-comparable facts never render as removals.

## Review and human records

Review is a deterministic evidence projection, not work management. It may
surface:

- new or reintroduced relationships since a Baseline;
- processing, coverage, freshness, or comparability regression;
- unresolved attribution or catalog/source conflict;
- evidence challenges and external exception expiry;
- pack release/validation expiry; and
- analysis failure.

ReviewItems are derived, versioned, deduplicated, superseded or expired, and
acknowledged per principal. They are not hand-created tasks.

Human record families remain distinct: action intent, classification
assertion, evidence challenge, governance request, and imported external
decision. No human record mutates evidence, coverage, or eligibility.

## Decisions, Baselines, and Dossiers

A Decision is an authorized human conclusion bound to an exact claim, visible
scope, Revision, artifact/Baseline, eligibility result, and applicable policy.
It may be superseded or expire but is never edited.

A Baseline is an organizational designation, not a personal cursor. A Dossier
seals selected artifacts, Baseline, Decision, manifest, evidence, coverage,
eligibility, validation identities, redaction scope, integrity root, and
validity statement. Offline verification proves integrity and authenticity,
not current authorization or freshness.

The Dossier is the handoff boundary to Workbench and external systems.

## Agents

Agents consume the same versioned, authorization-scoped evidence as the UI and
API. MCP adapters do not invent evidence, conclusions, checklists, or
permissions. Durable human-reserved actions are enforced server-side; omitting
a tool from a menu is not a security boundary.

> phebs does not make an agent authoritative; it makes the agent’s claims
> scoped, inspectable, and reproducible.

## Boundaries

Investigations improve population discovery, provenance, coverage,
attribution, comparison, review, and evidence-backed action. They do not add:

- generic document or markdown workspaces;
- comments, arbitrary assignments, custom states, priorities, or due dates;
- PRD/RCA authoring, workflow builders, portfolio dashboards, or policy
  authoring;
- generic assistant chat or a plugin marketplace;
- implicit migration completion or autonomous remediation.

Discussion, execution, and program management live elsewhere. phebs owns the
evidence history through the Dossier boundary.
