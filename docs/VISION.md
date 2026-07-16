# phebs — evidence-backed service and contract intelligence for the monorepo

*Starting with Go/gRPC migrations; expanding to change assurance,
operational response, platform conformance, and control evidence.*

*Vision document, July 2026. This is a direction, not an ask — the bounded
pilot proposal lives in [PITCH.md](./PITCH.md), and nothing here expands
it.*

**Companion documents:** [pilot charter](./PILOT_CHARTER.md) ·
[evidence-pack card template](./EVIDENCE_PACK_CARD.md)

## Thesis

phebs is a **versioned, permission-aware census of service-to-contract
relationships**. It answers: which deployables have relationship R, who
owns them, what changed, what evidence supports the answer, and what could
not be determined.

Migration is the first workflow over that census — not the product
boundary. The expansion is not "migrations plus more searches"; it is a
reusable **evidence plane for population questions** across the monorepo.
Every workflow below decomposes into the same five-part decision shape,
which is why this is one product and not a feature list.

## What "contract" means

Any named interface that creates coupling between independently owned
software:

- RPC operations and message fields
- Event topics and schemas
- Workflow engines' workflows, activities, signals, and queries
- Shared libraries and platform SDKs
- Feature flags and configuration keys
- Database tables, entities, and fields
- Error codes and policy decisions
- IAM entitlements and required controls

## The evidence-pack model

Each relationship type ships as a **separately measured evidence pack**.
Passing validation for `CALLS_OPERATION` establishes nothing about
`READS_FLAG` or `PUBLISHES_TOPIC`. Every pack ships with an
**evidence-pack card**: its supported claim, blind spots, coverage
semantics, validation result on a named benchmark, and stop criteria.
The reusable card format lives in
[EVIDENCE_PACK_CARD.md](./EVIDENCE_PACK_CARD.md).
"No match" never silently becomes "compliant" — conformance-shaped packs
return three-valued conclusions: evidenced conforming, evidenced
nonconforming, unknown (coverage or semantics insufficient).

**The validation rig is itself the moat.** The most expensive artifact
phebs owns is not any extractor — it is the measurement machinery: the
preregistered fail-closed staging, exact power analysis, burn-on-doubt
disclosure census, one-shot sealed scoring, and enforced
implementer/reviewer separation. Those custody, preregistration, sealing,
disclosure, and release controls are reusable. A new pack still may require
a different target population, sampling design, labeling method, estimator,
ground-truth construction, and reviewer expertise — especially for
interprocedural, control, and lineage claims. Extraction and validation can
both be bottlenecks; the reusable rig makes neither free, but prevents each
pack from inventing its governance from scratch. In the products and public
documentation reviewed as of July 2026, I have not found an incumbent that
attaches extractor-level measured accuracy and declared coverage to these
service-level decision artifacts. That pipeline, not the facts themselves,
is the durable differentiation hypothesis.

## Expansion opportunities

| Priority | Workflow and artifact | Benefit measured by | Distance |
|---|---|---|---|
| 1 | New-consumer and dependency-drift ledger: first/last-seen service-operation edges; deprecated/restricted API regressions | detection latency; net-new deprecated consumers escaping review; spreadsheet hours | Near (cheapest: see architecture notes) |
| 2 | PR/change-impact packet: typed base-vs-head edge changes, affected deployables, owners, reviewers, unresolved impact | time to identify affected owners; reviewer-routing precision; post-merge breakage discovery | Near (requires multi-revision architecture round) |
| 3 | Living contract atlas and ownership reconciliation: providers, consumers, build paths, catalog identity, owner conflicts | time to an accepted owner; bounced requests; unmapped rates | Near |
| 4 | Incident impact roster: direct/transitive candidates at deployed revisions, owners, gaps, separate runtime overlay | time from clue to reviewable blast radius; unnecessary pages | Near–medium |
| 5 | Deployment regression packet: contract-edge changes between last-known-good and suspect deployments | time to narrow hypotheses; rollback-decision time | Near–medium |
| 6 | Endpoint disposition queue: registered operations with static candidates, telemetry status, gaps, owner disposition | investigation hours per endpoint; stale surface removed | Near |
| 7 | Platform-adoption campaign: actual use of approved factories, SDKs, interceptors, retry libraries, legacy frameworks | reliable campaign denominator; outreach hours saved | Adjacent |
| 8 | Architecture-conformance ledger: new cross-domain calls, gateway bypasses, forbidden dependencies, expiring waivers | violations caught pre-merge; waiver age and closure | Adjacent |
| 9 | Feature-flag/configuration lifecycle dossier: readers, owners, dynamic unresolved uses, new readers after freeze | flag lifetime; cleanup investigation time; post-freeze escapes | Adjacent |
| 10 | Event/workflow contract atlas: producers, consumers, workflow starters, activity registrations, signals, owners | schema-change prep time; unknown-consumer rate | New extractor |
| 11 | Dependency/security remediation roster: affected-symbol use, deployable candidates, owners | time to owner-routed list; narrowing vs SBOM presence | New evidence inputs |
| 12 | Control and lineage evidence: deadline/retry/auth configuration, entitlements, field use, privacy/deletion candidates | audit-prep time; known/unknown control rate | Later / deeper semantics |

### The strongest day-to-day workflows

**Contract-aware PR review.** A PR changes a shared client, operation,
generated type, or common library. The impact packet lists
added/removed/modified typed relationships, direct vs transitive affected
targets, deployable/owner attribution, suggested reviewers, base/head
evidence, and unsupported impact. Search returns references; the build
graph returns thousands of dependents; phebs filters that population to
semantically evidenced users attributed to services and owners. The best
route from occasional migration use to daily use.

**New-consumer regression detection.** "A deprecated RPC had 15 consumers
yesterday — did a new one appear today?" A contract ledger with
first/last-seen snapshots, new/removed service-operation edges, lifecycle
status, owner, evidence, coverage, and approved exceptions with expiry.
Prevents migration targets from silently moving; also covers restricted
APIs, capacity-sensitive operations, and architectural boundaries.

**Platform and framework adoption.** Which services actually instantiate
the legacy client or bypass the approved factory — not merely carry its
package transitively? A defensible campaign denominator: platform teams
stop ticketing every build-dependent service and focus on evidenced users.

**Incident blast radius and regression archaeology.** Candidate services
ranked direct/transitive/unresolved at their deployed revisions, exact
code evidence, on-call routes, typed diffs between good and suspect
revisions, with runtime-observed callers as a separately sourced overlay.
Output says "could be affected," never "is affected."

**Architecture governance.** Did this PR introduce a gateway bypass or a
forbidden cross-domain dependency? The conformance artifact carries the
evidenced edge, the applicable policy, the introducing snapshot/PR, the
owner, any waiver with expiry, and coverage — turning an architecture
decision record from prose into an observable, reviewable control.

**Flag and configuration lifecycle.** `DECLARES_FLAG`, `READS_FLAG`,
`READS_CONFIG`, `USES_DEFAULT`, `CONSTRUCTS_DYNAMIC_KEY`. Distinguishes
declarations, tests, production reads, wrappers, dynamic unresolved keys,
deployables, owners. The cleanest adjacent pack: it reuses the entire
attribution, snapshot, and coverage pipeline.

**Async contract intelligence.** `PUBLISHES_TOPIC`, `CONSUMES_TOPIC`,
`STARTS_WORKFLOW`, `REGISTERS_WORKFLOW`, `REGISTERS_ACTIVITY`,
`SIGNALS_WORKFLOW` — event-schema impact, workflow change scoping,
activity retirement. Dynamic topic names and runtime registrations remain
explicit gaps; a source-derived candidate is not proof of message flow.

**Error and policy behavior (research tier).** Which callers map error
class X to retry, fail open, fail closed, suppress, propagate?
(`HANDLES_ERROR_CODE`, `MAPS_ERROR_ACTION`, `RETRIES_ON_ERROR`, …).
Requires interprocedural control-flow analysis — and, critically, **a
different validation game**: interprocedural claims cannot be labeled by
two reviewers reading a call site, so ground truth itself becomes
contested. Presented as research until that protocol exists.

**Privacy and retention evidence (research tier).** Which service
candidates write classified identifier F, and which show evidence of a
deletion handler, TTL, or explicit disposition? (`READS_FIELD`,
`WRITES_FIELD`, `PERSISTS_FIELD`, `REGISTERS_DELETION_HANDLER`,
`DECLARES_TTL`). The output is an owner-routed review inventory, never a
compliance certificate: static presence of a handler does not prove
complete or successful deletion.

## Reusable primitives

| Primitive | Workflows unlocked |
|---|---|
| Typed facts + service attribution | contract atlas, ownership routing, migration inventories |
| Snapshot diff | PR impact, new-consumer detection, regression archaeology |
| **Query surface** (API, UI, MCP tools, with SLOs) | every daily workflow above — the hidden step between census and product |
| Policy + exceptions + dispositions | architecture controls, adoption campaigns, lifecycle governance |
| Deployment and runtime overlays | incident scoping, rollout planning, endpoint disposition |
| New narrow evidence packs | flags, events, workflows, SDKs, security primitives |
| Field-sensitive/dataflow analysis | proto compatibility, error behavior, privacy and retention |

**Every artifact has an agent-consumer twin.** The PR packet's likeliest
daily consumer is a review agent citing proof bundles in comments, not a
human on a dashboard. The evidence plane is also the grounding layer for
the organization's agentic workflows: permission-filtered facts an agent
can cite and a human can independently reproduce. phebs' MCP surface
already exists; the packs give it something worth citing.

## Architecture notes (from the implementation)

- **The new-consumer ledger is the cheapest near-term pack.** Extraction
  already re-runs per index event and publications supersede atomically
  per repo/domain; the ledger is a diff plus a compact
  first-seen/last-seen edge table. One real change: retention currently
  sweeps superseded runs, so history needs either per-snapshot pins or
  that ledger table — small, well-understood work.
- **PR impact packets break a load-bearing posture: phebs is HEAD-only by
  design.** Base-vs-head analysis requires extraction at non-HEAD
  revisions — ephemeral corpora and runs that must never pollute the
  published census, plus CI-latency expectations. That is an architecture
  round (the long-gated multi-branch demand), not a feature; sequencing
  must budget for it or the first estimate will miss by a quarter.
- **Ownership reconciliation is politically loaded — design its voice
  now.** When source evidence disagrees with the catalog, the artifact
  **reports conflicts and never arbitrates**: evidenced-consistent,
  evidenced-conflicting, unknown. That principle is the difference between
  adoption by platform teams and resentment from them.

## Durable differentiation

| Existing system | What it establishes | What phebs adds |
|---|---|---|
| Code search | exact source occurrences | typed relationships, coverage, service attribution, history |
| Build graph | what may compile or depend | semantic filtering to the operation or primitive actually used |
| Service catalog | declared identity and ownership | source-derived evidence and explicit reconciliation failures |
| Runtime telemetry | what executed during an observed interval | dormant/conditional candidates and immutable source context |
| SAST/SBOM | findings or component presence | build paths, deployable/owner attribution, campaign state, proof bundle |
| LLM agent | synthesis and interaction | permission-filtered facts that can be reproduced and independently reviewed |

Search finds occurrences; build systems compute dependencies; catalogs
declare ownership; telemetry records recent execution. **phebs joins those
views into a versioned, owner-attributed decision artifact while
preserving what remains unknown.**

## Sequencing

1. **Prove the migration foundation** — the external Go/gRPC measurement
   and the internal attribution pilot (the entirety of the current ask).
2. **Productize the contract atlas** — proof bundles, coverage,
   dispositions, ownership reconciliation, **and the query surface**
   (assertion API, UI, MCP tools, SLOs). Search UI/MCP are operational
   today; contract-intelligence queries and their SLOs remain post-gate
   productization work.
3. **Add daily change assurance** — the new-consumer ledger first (cheap,
   reuses the event-driven pipeline), then PR impact packets after the
   multi-revision architecture round, reviewer routing, endpoint hygiene.
4. **Add adoption and policy campaigns** — factories, SDKs, interceptors,
   architecture boundaries, flags/configuration.
5. **Add operational overlays** — deployment revisions, runtime
   observations, dependency/SBOM inputs, incident packets.
6. **Add new contract families** — events/topics, workflow engines, HTTP,
   data stores.
7. **Only then pursue deep semantics** — proto-field lineage, error
   handling, privacy and retention — each behind its own,
   yet-to-be-designed validation protocol.

Accuracy established for one pack is never generalized to another; every
pack independently declares its supported claim, blind spots, validation
result, and stop criteria on its pack card.

## What phebs must not become

- generic semantic search or generic agent chat;
- a replacement for observability;
- a universal compiler, call graph, or static analyzer;
- a project-management or GRC system;
- an autonomous mass-refactoring engine;
- a policy-authoring system — phebs evaluates evidence against policies
  owned elsewhere; it never becomes the place where policy is written.

Specialized analyzers can supply versioned facts *to* phebs; phebs' role
is to attribute, reconcile, scope, diff, and package those facts into
decision artifacts.