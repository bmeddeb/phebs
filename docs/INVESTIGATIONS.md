# phebs Investigations — the persistent object around consequential work

*Product-experience spec, revision 2, July 2026 · post-gate productization
(VISION.md sequencing step 2). Nothing here expands the pilot ask.*

One noun everywhere: **Investigation** — UI, API (`/api/investigations`),
MCP (`investigation_id`).

## Thesis

An Investigation is a **productized ceremony**: a frozen engineering
question, its authorized universe, pinned snapshots, pack versions,
findings, coverage gaps, attribution, dispositions, and decision history.
Search stays stateless; users promote consequential questions into
Investigations. The lifecycle reuses existing machinery: analysis
manifests (freeze), published runs (immutability), `PinRun`/proof-aware
retention (persistence), pack-card disposition rules (judgment separate
from evidence).

## Entity model

| Entity | Meaning |
|---|---|
| `Investigation` | stable question and intent |
| `Revision` | immutable scope, configuration, and pack selection |
| `Run` | one execution of a revision against one snapshot |
| `Baseline` | human-accepted organizational comparison point |
| `ReviewCursor` | what an individual reviewer last saw |
| `Disposition` | typed human judgment (see below) |
| `ReviewItem` | deterministic projection of an evidence change |
| `Watch` | reevaluation policy over runs |
| `Dossier` | sealed export |

Change rules: a new source snapshot creates a **Run**; changing the
universe, question, or pack selection requires a new **Revision**; a
materially different question is a new **Investigation**. Runs never
mutate; baselines are set only by humans.

## Absence eligibility — per claim, per revision

"Terminal accounting" is necessary but not sufficient: `failed`,
`partial`, and some `excluded` states are terminal too. Absence
eligibility is computed **per claim and per revision** from: an
independently enumerated universe; every unit terminal; the analyzed /
partial+failed / excluded outcome-rate gates of the governing pack's
decision rules; and required attribution hops resolved for the claim.
Ineligibility carries **structured blocker codes** (e.g.
`UNITS_FAILED`, `EXCLUSION_RATE_EXCEEDED`, `ATTRIBUTION_UNRESOLVED`,
`PACK_VALIDATION_EXPIRED`, `SCOPE_NOT_ENUMERATED`, `STALE_ANALYSIS`) —
derived, never operator-set, surfaced in the header and the envelope.

## Navigation, header, overview

Top level: **Search · Investigations · Review · Watches · Activity ·
Admin**. Inside: **Overview · Census · Changes · Coverage · Evidence ·
Dispositions · Activity**. Persistent header: normalized question;
snapshot + build configuration; authorized universe; pack + extractor
versions; freshness; owner; state; absence eligibility with blocker
codes. Overview cards: **evidenced · unknown · changed · needs action**.

## Empty states are the philosophy

Never one "No results." Distinguish (renderings of existing states):
no supported facts in fully analyzed scope; none among analyzed units but
units failed; unsupported construct/language; stale analysis; incomplete
attribution; inaccessible scope; truncated/paginated result.

## Diff comparability

An edge absent from the newer run is never simply "removed." The diff
engine classifies cause before rendering: **source deletion** (traced),
**extractor/pack version change**, **authorization change**, **catalog
change**, **failed analysis**, **narrowed scope**. Only same-revision,
comparable-coverage runs may render an unqualified removed/added; all
other causes render as their cause, and failed or inaccessible consumers
are blocked from appearing as "removed."

## Review — a projection, not task management

`ReviewItem`s are **generated deterministically from evidence deltas**,
deduplicated, superseded by newer deltas, acknowledged against a
`ReviewCursor`, and expired. Queues: new consumers since baseline;
reintroduced deprecated consumers; changed since my cursor; processing or
coverage regression; unresolved attribution; ownership conflict;
exception requested/expiring; pack validation expired; analysis stale or
failed. Opening an item starts with the delta, then the evidence chain,
then a disposition. No arbitrary tasks, comments, custom states, or
due-date workflows.

## Dispositions — five types, not one enum

| Type | Example | Authority / behavior |
|---|---|---|
| Intent | `will migrate` | owner; expires; carried forward across runs |
| Classification assertion | `not production` | owner; re-verified against pack classification each run |
| Evidence challenge | `false attribution` | **enters pack quality review; never alters the fact** |
| Governance request | `exception requested` | external exception authority; expiry mandatory |
| System condition | `owner unknown` | system-derived; cleared by resolution, not by hand |

Each type has its own authority, expiry, carry-forward, and review
behavior; all record actor, rationale, timestamps, and referenced fact and
coverage identities.

## Authorization of persistent artifacts

Saved objects must not become ACL bypasses. Defined behaviors: sharing is
principal-scoped re-authorization, never a static snapshot of results;
ownership transfer re-evaluates visibility; counts and coverage never leak
inaccessible existence; **revocation applies to pinned runs** (retention
never overrides authorization loss — the deletion/revocation exception);
exports are redacted to the recipient's scope at export time; **reopening
a dossier re-authorizes against current ACLs** before display.

## Dossier — the export contract (the Workbench boundary)

A Dossier is a sealed, versioned artifact: format version; manifest
(question, revision, runs, snapshots, pack cards, digests of every
included artifact); embedded evidence for cited findings and **references
(digest + locator) for the remainder**; the authorization scope it was
redacted to; validity statement (what it proves, as of which snapshot,
under which pack validations); and an offline verification procedure
(digest chain check without a phebs instance). phebs owns everything up to
the Dossier; Workbench and other consumers operate on Dossiers — the
products meet at this boundary, not inside the evidence plane.

## Agents: governed consumers

The MCP envelope **exposes the pack card's orthogonal axes separately** —
no collapsed tri-state: evidence basis (`evidenced`/`derived`); semantic
resolution (`resolved`/`ambiguous`/`unresolved`); processing state and
coverage; attribution state per hop; decision conclusion (pack-specific);
absence eligibility with blocker codes; freshness, pagination, and
truncation; pack validation identity; provenance and versions; and the
permitted-qualification string sourced from the pack card. Bounded tools
(`find_contract_edges`, `get_contract_evidence`, `explain_attribution`,
`get_analysis_coverage`, `compare_contract_snapshots`,
`list_new_consumers`, `verify_proof_reference`,
`generate_review_checklist`); no prose oracle. Human-reserved verbs are
enforced **server-side by principal role**.

## Monorepo-scale creation UX

Guided creation is asynchronous: scope preview from metadata; authorization
preflight; estimated work and resource cost; progress with cancellation;
bounded retries; partial-failure behavior that surfaces failed units in
the coverage ledger rather than aborting silently; and per-investigation
resource limits.

## Pack manifest, validated against the card

Packs register an **executable pack manifest** (predicates, workflows,
facets, census columns, evidence renderer, coverage rules, decision
gates, review projections, diff semantics, MCP actions, operating
limits). The manifest is machine-validated against the human-readable
pack card at release: any divergence between manifest and card blocks
release. Internal, fixed modules only — no marketplace, no third-party
SDK.

## First slice (dependency-ordered)

1. Entity model (Investigation/Revision/Run) + guided async creation.
2. Overview, Census, Coverage, Evidence views — empty-state taxonomy
   first.
3. Immutable runs across snapshots; baseline designation.
4. Cause-classified consumer diff (requires the ledger/retention change in
   VISION architecture notes).
5. Structured MCP envelope on existing tools.
6. Minimal Review projection: new consumers, failed coverage, unresolved
   attribution.

The derivation inspector ships only with pilot-validated attribution.
Watches stay sub-notification (saved queries feeding Review; no
email/Slack delivery). Dispositions and Dossiers follow the slice once
runs and baselines exist.

## Boundaries

A phebs feature must materially improve population discovery, provenance,
coverage, attribution, change detection, review, or evidence-backed
action. Not added: markdown workspaces; PRD/RCA authoring; workflow
builders; task/deadline tracking; policy authoring; portfolio dashboards;
generic assistant chat; plugin marketplace; investigation comment
threads. Decision history is dispositions plus revisions; discussion
lives elsewhere.
