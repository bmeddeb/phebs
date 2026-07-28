# phebs product vision

phebs is an evidence plane for consequential changes in large codebases.
Migrations are the first workflow, not the product boundary.

This document owns long-term direction. It is not a description of current
behavior and does not expand the bounded [pilot ask](./PITCH.md). Use the
[roadmap](./ROADMAP.md) for current sequencing, the [README](../README.md) for
shipped posture, and the [evidence-pack card](./EVIDENCE_PACK_CARD.md) for
claim and validation rules.

## Thesis

Teams repeatedly ask the same population question in different forms:

> Which deployables have relationship R, who owns them, what changed, what
> evidence supports the answer, and what could not be determined?

Code search finds occurrences, build systems find possible dependencies,
catalogs declare identity and ownership, and telemetry records recent
execution. phebs joins those inputs into a versioned, permission-aware decision
artifact while preserving uncertainty and source provenance.

## What counts as a contract?

A contract is any named interface that couples independently owned software:

- RPC operations and message fields;
- event topics, schemas, workflows, activities, signals, and queries;
- shared libraries, platform SDKs, feature flags, and configuration keys;
- database entities and fields;
- error codes, policies, entitlements, and required controls.

phebs should support only relationships for which it can define a bounded
claim, evidence identity, coverage denominator, unresolved state, and
validation method.

## Evidence packs are the unit of trust

Each relationship family is governed as a separate evidence pack and must be
measured independently before its claims are promoted. Passing validation for
one predicate establishes nothing about another. A pack declares:

- the exact claim and unit of analysis;
- supported constructs and explicit non-claims;
- stable identities and evidence provenance;
- coverage, exclusion, failure, and unresolved semantics;
- validation result and operating envelope;
- release, suspension, expiry, and retirement rules.

Conformance-shaped packs return three states: evidenced conforming, evidenced
nonconforming, or unknown. “No match” never silently becomes “compliant.”

The durable differentiation hypothesis is not a particular extractor. It is
the reusable custody and measurement system around extractors: preregistration,
independent review, sealed scoring, explicit disclosure, fail-closed release,
and reproducible evidence. New packs still require their own population,
labels, estimator, expertise, and result.

## Product directions

| Direction | Decision artifact | Value to measure |
|---|---|---|
| Contract atlas and ownership reconciliation | providers, callers, implementations, shapes, owner conflicts, and gaps | time to an accepted owner; unmapped rate |
| Migration and dependency ledger | first/last-seen relationships, replacement comparison, and dispositions | discovery latency; spreadsheet/outreach time |
| Change-impact review | base-versus-head relationship changes, affected deployables, owners, and unresolved impact | reviewer-routing precision; breakage discovery |
| Incident scoping | direct, transitive, and unresolved candidates at deployed revisions with a separate runtime overlay | time to a reviewable blast radius |
| Platform adoption | evidenced use or bypass of approved factories, SDKs, interceptors, and libraries | reliable denominator; outreach avoided |
| Architecture conformance | new forbidden relationships, applicable external policy, and expiring exceptions | violations caught; waiver age |
| Configuration lifecycle | declarations, readers, defaults, dynamic keys, owners, and post-freeze changes | cleanup time; escaped readers |
| Async contracts | topic producers/consumers and workflow registrations/starters | change-preparation time; unknown-consumer rate |
| Security and dependency response | affected-symbol use, build/deployable attribution, and ownership | time to an owner-routed remediation list |
| Control and lineage research | retry/auth/deadline behavior, sensitive-field use, and retention/deletion candidates | known/unknown rate; review effort |

The last category is research until its ground truth and interprocedural
validation protocol exist. Static evidence may produce candidates; it must not
be presented as a compliance certificate.

## Reusable primitives

| Primitive | What it unlocks |
|---|---|
| Typed facts and immutable citations | reproducible contract and dependency inventories |
| Source-to-unit/service/owner attribution | routing and reconciliation |
| Snapshot comparison | migration ledgers, change impact, and regression archaeology |
| Coverage and unresolved semantics | honest population and negative-result questions |
| Human dispositions kept separate from evidence | review without mutating facts |
| Dossiers and proof references | portable, independently inspectable decisions |
| Permission-aware API, UI, and MCP projections | human and agent consumers sharing one engine |
| Runtime/deployment overlays with distinct provenance | incident and rollout context without rewriting source facts |

Every human-facing artifact should have an agent-consumer twin. Agents receive
the same scoped facts, gaps, and citations; phebs does not make the agent
authoritative.

## Durable differentiation

| Existing system | Establishes | phebs adds |
|---|---|---|
| Code search | source occurrences | typed relationships, history, coverage, and attribution |
| Build graph | possible dependency | evidence of the specific operation or primitive used |
| Service catalog | declared identity and ownership | source-derived evidence and explicit conflicts |
| Runtime telemetry | observed execution in a window | dormant/conditional candidates and immutable source context |
| SAST or SBOM | finding or component presence | deployable/owner routing and reviewable proof |
| LLM agent | synthesis and interaction | permission-filtered facts a human can reproduce |

## Directional sequence

1. Keep the shipped search, browsing, code-intelligence, security, and
   operational foundation dependable.
2. Establish each evidence pack independently before promoting its claims.
3. Productize recurring questions as
   [Investigations](./INVESTIGATIONS.md), proof material, and comparable
   snapshots.
4. Add daily change-assurance and ownership-routing workflows.
5. Add deployment/runtime overlays without merging their provenance into
   static evidence.
6. Add new contract families only when a narrow pack and validation design
   exist.
7. Pursue deep control-flow and data-lineage semantics only after their ground
   truth is defensible.

Current order and gates live only in [ROADMAP.md](./ROADMAP.md).

## Boundaries

phebs must not become:

- generic semantic search or generic agent chat;
- a replacement for observability, build systems, or service catalogs;
- a universal compiler, call graph, or autonomous refactoring engine;
- a project-management, policy-authoring, portfolio, or GRC system;
- a source of runtime, compatibility, completeness, or compliance claims that
  its evidence cannot establish.

Specialized analyzers may publish versioned facts into the evidence plane.
phebs owns attribution, reconciliation, scoping, comparison, and proof—not
every analysis technique or the external policy being evaluated.
