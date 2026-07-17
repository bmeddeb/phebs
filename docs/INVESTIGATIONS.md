# phebs Investigations — the persistent object around consequential work

*Product-experience spec, July 2026 · post-gate productization (VISION.md
sequencing step 2 given concrete shape). Nothing here expands the pilot ask.*

One noun everywhere: **Investigation** — UI navigation, API resource
(`/api/investigations`), MCP fields (`investigation_id`), and this document.

## Thesis

An Investigation is a **productized ceremony**: a frozen engineering
question, its authorized source universe, pinned snapshots, evidence-pack
versions, findings, coverage gaps, attribution chain, human dispositions,
and decision history. Search stays fast and stateless; users promote
important or recurring questions into an Investigation.

The lifecycle is the validation machinery phebs already runs by hand:

| Investigation concept | Existing machinery |
|---|---|
| Frozen question + scope | analysis manifest (charter Gate-0 freeze) |
| Immutable revisions | published extraction runs, superseding atomically |
| Snapshot pinning / export | `PinRun`, proof-aware retention, proof bundles |
| Dispositions | pack-card §8: stored separately, never rewrite facts |
| Absence-based eligibility | charter negative-result rule, **derived** |
| Regression watch | publication-event re-evaluation feeding Review |

`PinRun`/`ResolveEvidence` — until now API surface without a consumer — are
the Investigation's persistence layer.

## Flow

search or ask → freeze question and scope → run evidence packs → inspect
findings and gaps → route unresolved items → review changes → record a
human decision → export proof bundle → watch for regressions.

## Navigation and header

Top level: **Search · Investigations · Review · Watches · Activity ·
Admin**. Inside an Investigation: **Overview · Census · Changes ·
Coverage · Evidence · Dispositions · Activity**.

Persistent header, always visible: normalized question/contract; source
snapshot and build configuration; authorized universe; evidence-pack and
extractor versions; freshness; owner; investigation state; and
**absence-decision eligibility — a derived field computed from the
coverage ledger (independently enumerated universe + every unit in a
terminal processing state), never operator-set.**

Overview leads with four cards: **what is evidenced · what remains
unknown · what changed · what needs action.**

## Empty states are the philosophy

Never one generic "No results." Distinguish, as renderings of the existing
state machine (no new semantics): no supported facts in fully analyzed
scope; no facts among analyzed units but some units failed; unsupported
construct or language; stale analysis; incomplete attribution;
inaccessible scope; truncated/paginated result.

## Review Center

Delta-first queues: new consumers since accepted baseline; reintroduced
deprecated consumers; changed since my last review; processing or coverage
regression; unresolved deployable/service/owner; catalog/source ownership
conflict; exception requested; exception expiring; pack validation
expired; analysis stale or failed. Opening an item starts with the delta,
then the evidence chain, then a disposition (`will migrate`,
`not production`, `false attribution`, `exception requested`,
`owner unknown`) — recorded with actor, rationale, expiry; never mutating
evidence.

## Agents: governed consumers of evidence

Every MCP result shares a versioned structured envelope: result state
(`evidenced` / `conflicting` / `unknown`); normalized scope and snapshot;
typed facts; processing and attribution coverage; unresolved conditions;
pack validation identity; provenance and versions; pagination/truncation
state; and a **permitted-qualification string sourced from the pack card's
negative-result wording** — never authored per-tool.

Bounded tools: `find_contract_edges`, `get_contract_evidence`,
`explain_attribution`, `get_analysis_coverage`,
`compare_contract_snapshots`, `list_new_consumers`,
`verify_proof_reference`, `generate_review_checklist`. No `ask_phebs`
prose oracle. Human-reserved verbs (approve an investigation, create an
exception, change ownership, declare a migration complete) are **enforced
server-side by principal role** — not by omitting tools from a list.

> phebs does not make an agent authoritative; it makes the agent's claims
> scoped, inspectable, and reproducible.

## Pack-driven modules (P2, internal only)

An `EvidencePackModule` registers predicates, workflows, facets, census
columns, evidence renderer, coverage rules, decision gates, review
projections, diff semantics, and MCP actions — as **the pack card's §11
made executable**, one source of truth. No marketplace, no third-party SDK.

## First slice (dependency-ordered)

1. Investigation data model + guided creation (manifest freeze, pins).
2. Overview, Census, Coverage, Evidence views — **empty-state taxonomy
   first** (cheapest differentiator; pure rendering of existing states).
3. Immutable revisions across successive snapshots.
4. Added/removed/changed consumer diff (requires the ledger/retention
   change noted in VISION architecture notes).
5. Structured MCP envelope on existing tools.
6. Minimal Review queue: new consumers, failed coverage, unresolved
   attribution.

The **derivation inspector** (occurrence → target → deployable → service →
owner, per finding) ships only with the pilot-validated attribution layer.
**Watches** stay sub-notification: saved queries re-evaluated on
publication events, feeding Review — no email/Slack delivery.

## Boundaries

A phebs feature must materially improve population discovery, provenance,
coverage, attribution, change detection, review, or evidence-backed
action. Do not add: generic markdown workspaces; PRD/RCA authoring;
workflow builders; task/deadline tracking; policy authoring; portfolio
dashboards; generic assistant chat; a plugin marketplace; **or
investigation comment threads** — decision history is dispositions plus
revisions; discussion lives elsewhere.

**The Workbench line:** phebs owns the Investigation up to the sealed
dossier and its exports. Programs, plans, and cross-investigation
portfolio work belong to Workbench, which consumes proof bundles and
dossiers — the two products meet at the export boundary, not inside the
evidence plane.
