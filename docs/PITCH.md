# phebs — evidence-backed contract intelligence for monorepo API migrations

*Prepared by Ben Meddeb · July 2026 · draft for internal circulation*

## Executive summary

phebs is a working, self-hosted code-search system and an experimental
evidence layer for Go/gRPC migrations. For a pinned monorepo baseline and a
declared authorized build-target universe, it is designed to produce a
**versioned consumer-candidate inventory**: classified client-call and
server-registration facts with reproducible source evidence, explicit
analysis gaps, extractor-version metadata — and, as the pilot's central
question, the derivation from each source occurrence to build target,
deployable, canonical service, and owner.

Search and MCP access are operational today. Contract extraction remains
dark until it passes a preregistered external benchmark followed by an
internal shadow evaluation. I am proposing a bounded pilot to determine
whether phebs reduces consumer-discovery time and makes the remaining
uncertainty in migration decisions explicit. The ask: a six-week
read-only pilot — one VM, a least-privilege identity, two partner roles,
advisory-only results.

## The problem

The highest-stakes code questions are population questions: which pinned
source snapshots contain supported call sites for the API we want to
retire, and which deployable services are potential consumers and
therefore require migration review or an explicit disposition. Today that
inventory is assembled from code search, tribal knowledge, and traffic
sampling. Each misses differently, none records what was and was not
analyzed, and the result is not an artifact a decision-maker can review.

Our monorepo makes repository-level counting meaningless: one repository
contains thousands of independently built and deployed services. A search
match identifies a source occurrence — not necessarily the deployable that
contains it or the team responsible for removing it. A useful consumer
inventory must preserve the derivation from occurrence to build target,
deployable, canonical service, and owner, while distinguishing direct
source callers, potential transitive consumers, and runtime-observed
consumers. Ambiguous or unresolved attribution must remain explicit rather
than being silently dropped.

This is why excellent search is necessary but insufficient: deprecations
stall not because a caller is hard to find, but because nobody can state
the analyzed universe, the service-level attribution, the known gaps, and
the residual uncertainty explicitly.

## The unit of analysis

In a monorepo, a repository is not a consumer. The evidence model
distinguishes:

- **Occurrence** — exact file, blob, and byte span containing the call;
- **Build target** — the package or target compiling that occurrence;
- **Deployable** — the binary or workload that can contain the target;
- **Service** — the canonical service-catalog identity;
- **Owner** — the owner recorded in versioned ownership metadata.

These relationships are many-to-many: one shared-library call site may
flow into fifty services; a service may consume an API through an internal
wrapper without containing a direct generated-client call. Output
therefore distinguishes directly evidenced call sites, potential
transitive consumers through the build graph, mapped deployables and
services, recorded owners, and **unresolved or ambiguous mappings, kept
explicit**.
The pilot compares phebs' direct and transitive consumer candidates with
runtime-observed consumers supplied independently; runtime observations
are not represented as phebs-derived facts.

A useful result reads like this (illustrative — target output shape, not
measured data):

> 212 source occurrences identified; 198 mapped to build targets; 184
> mapped to 137 deployable services; 14 target mappings and 9 service
> mappings remain unresolved.

That is what a migration owner can act on — not a repository count.

## Target pilot artifact and capability status

| Status | Capability |
|---|---|
| Operational | search, connectors, authentication/OIDC, audit, code navigation, MCP search tools |
| Implemented but dark | Go/gRPC call-site extraction, evidence storage, atomic fact publication |
| Pilot hypothesis | build-target, deployable, service, and owner attribution; internal evaluation artifacts |
| Post-gate productization | consumer queries, exportable proof bundles, durable coverage certificates, inventory history (diffable across snapshots) |

The target artifact is a versioned consumer-candidate inventory bound to
an **analysis manifest** recording: the monorepo commit/tree digest;
included and excluded paths; build targets and build configuration; build
tags, generated inputs, and dependency locks; the service-catalog and
ownership snapshots used for attribution; extractor and adapter versions;
and the requester's visibility scope.

Per finding, classification is modeled on orthogonal dimensions rather
than one flat label:

- **target role** — production, test, tooling, benchmark;
- **source origin** — first-party, generated, vendored;
- **behavioral role** — client call, server registration, mock/fake;
- **reachability** — direct target, transitive deployable candidate,
  unresolved;

plus file/blob/span provenance reproducible from pinned source and the
occurrence → target → deployable → service → owner derivation with
unresolved steps marked. Per inventory: a coverage statement listing which
eligible files and build targets were analyzed, excluded, partial, or
failed — scoped to the requester's authorized source, build-target,
deployable, and service universe, revealing nothing about inaccessible
scope.

## What this does and does not establish

"Proof-grade" here means reproducible evidence, explicit scope, versioned
derivation, and quantified uncertainty — not proof of a universal
negative. The assurances are separable:

| Assurance | Supports | Does not establish |
|---|---|---|
| Provenance | a finding is reproducible from pinned source | that the code executes |
| Coverage statement | what authorized scope was processed or failed | that the extractor has no unknown blind spots |
| Benchmark bounds | extractor version E's measured performance on benchmark B | recall for a particular query or internal corpus |
| Attribution mapping | derivation via pinned build/catalog metadata | correctness or freshness of that metadata itself |
| Runtime telemetry (independent, not phebs) | recent execution by a deployed consumer | dormant or unobserved source consumers |

Known analysis failures become explicit, while extractor error is measured
on a named benchmark; residual misses for an individual internal query
remain uncertain. Field-level proto dependency and compatibility analysis
("what breaks if this field changes") is roadmap, not current capability.

## What exists today

- **Operational search MVP:** zoekt trigram search (regex-capable,
  streaming), single-process deployment with no separately operated
  database, queue, or frontend; connectors with webhook reindexing;
  permission-aware search mirroring host ACLs; users/sessions/API keys,
  OIDC, audit log; SCIP code navigation and git history. Application
  analytics remain local; there is no outbound product telemetry (MCP
  clients do retrieve code, under the same ACLs).
- **Agent integration:** a built-in MCP endpoint whose tools return
  permission-filtered source evidence that agents can cite. Verified live
  from agent sessions.
- **Evidence layer (dark):** content-addressed fact storage with atomic
  publication and a fail-closed Go/gRPC call-site extractor, shipping
  behind an experimental flag until measured (mechanics in Appendix B).

Cold-index cost, incremental freshness, resource use, and recovery under
monorepo commit volume are measured during the pilot, not asserted here.

**Before any internal source ingestion** — read-only describes the Git
identity, but phebs still creates and serves another sensitive copy of
source — the following are prerequisites, not roadmap: threat model,
reproducible build, dependency scan, secrets-handling review, ACL negative
tests, an egress policy for the pilot host, and retention rules including
a deletion/revocation exception (a retained proof bundle must not override
loss of authorization, mandatory deletion, or legal policy).

## The validation discipline

No product claim ships on an unmeasured extractor. **The external
benchmark validates call-site extraction only** — client-call and
server-registration facts on four open systems (Temporal, Dapr, Loki,
Online Boutique). Its protocol: two independent reviewers label assigned
blind samples from source alone, with a preregistered overlap subset used
to measure agreement; disagreements in the overlap are adjudicated into
the frozen gold set, whose hash is published before scoring. The
recall-positive population is constructed independently of the candidate
extractor, so the extractor cannot define the population in which its own
false negatives are measured. Precision and recall use separate sampling
frames; clustering is handled in the sampling design; scoring is one
sealed execution against inputs that must first prove byte-level
reproducibility, with implementer/reviewer separation throughout. Bar:
≥98% precision and ≥90% recall lower bounds at 95% joint confidence
across a Bonferroni family of four — client-call precision, client-call
recall, server-registration precision, server-registration recall — plus
per-fixture floors and role-classification checks under the sealed
protocol — claimed for the measured extractor version on that benchmark.
First result targeted for August 2026; the protocol fails closed rather
than slipping silently. (Appendix A.)

The external benchmark does **not** validate the mapping from call site to
internal service and owner. The internal shadow evaluation therefore
measures separately: call-site precision/recall on internal code
(wrappers, macros, build tags, generated sources, custom frameworks);
build-target attribution accuracy and coverage; deployable/service
attribution accuracy and coverage; and owner-resolution coverage. If the
product claim is a service-consumer inventory, the internal evaluation
unit is *(canonical service, gRPC operation, monorepo snapshot, build
configuration)*. Until that mapping exists and is evaluated, phebs claims
a **versioned call-site inventory with consumer candidates**, not an
unqualified consumer inventory.

This discipline is not overhead — it is the product. If the external
measurement fails, the contract-intelligence pilot does not advance: the
failed round produces a committed diagnosis, and a revised extractor must
be evaluated against a fresh, unseen holdout before reconsideration.

## Comparison with the current workflow

The relevant comparison is against how a consumer inventory is built
today. For a concrete anchor, the pilot would run against **[one active
Go/gRPC migration — named example and one narrowly selected RPC to be
inserted before circulation, with a measured baseline reconstructed from
the manual inventory, owner outreach, build queries, and traffic
evidence]**:

| | Current practice | Pilot hypothesis (measured, not promised) |
|---|---|---|
| Time to a reviewable candidate inventory | days–weeks; to be baselined from the named migration | target: same-day initial inventory; actual time measured |
| Snapshot/target scope recorded | often implicit or manually documented | manifest-bound, every run |
| Production vs test/tooling classification | manual | orthogonal-dimension classification; accuracy measured |
| Service/owner attribution | manual, ambiguous | derived from catalog snapshots; coverage measured |
| Known analysis failures exposed | not systematically attached to the result | per file/target, with reason, in the coverage statement |
| Reproducible review artifact | not systematically attached to the result | manifest-bound inventory with per-finding provenance |
| Inventory tracked across snapshots | not systematically attached to the result | diffable (prototype during pilot; productized post-gate) |

Mature platforms provide excellent search, navigation, and agent
integration. I have not identified, in the tools and workflows evaluated,
a single artifact combining typed service-contract relationships,
reproducible evidence, declared coverage, and measured extractor
performance — that artifact, at monorepo service granularity, is the
hypothesis this pilot tests.

## Beyond migrations

Migrations are the initial proving ground, not the product boundary.
Across a monorepo containing thousands of independently deployed services,
many recurring engineering tasks share the same decision shape: which
deployables have a particular code-level relationship, who owns them, what
changed, what evidence supports the conclusion, and what could not be
evaluated?

The same evidence foundation can support pre-change impact analysis,
new-consumer regression detection, contract-aware PR review, platform-SDK
adoption campaigns, service-catalog reconciliation, incident blast-radius
assessment, architecture-policy enforcement, feature-flag cleanup,
dependency-remediation routing, and audit-evidence preparation. Additional
measured evidence packs can extend the contract model from Go/gRPC
operations to event topics and schemas, workflow activities and signals,
configuration keys, shared-library APIs, security controls, and eventually
field-level data lineage.

The expansion thesis is not that phebs should analyze everything. It is
that a deliberately small set of measured relationship types, joined to
the monorepo build and service-identity graph, can turn recurring
source-discovery work into versioned, owner-attributed decision artifacts.
Accuracy established for one evidence pack is never generalized to
another; every new relationship type must declare its supported claim,
coverage semantics, validation bar, and unresolved cases.

The six-week pilot remains focused on one Go/gRPC migration. The broader
use cases belong to the product vision and staged roadmap (see
`docs/VISION.md`), where they strengthen the adoption case without
expanding this ask.

## Roadmap

1. **Now:** external benchmark validation of the Go/gRPC call-site
   extractor.
2. **Pilot:** the attribution chain (build target → deployable → service
   → owner) as read-only enrichment from build-graph, catalog, deployment,
   and ownership metadata; internal shadow evaluation of both layers.
3. **On passing measurements:** consumer-candidate queries, proof bundles,
   and coverage statements over the analyzed target and deployable
   universe; inventory history across snapshots.
4. **Later:** additional extractors; proto field-level dependency and
   compatibility analysis.
5. **Production hardening:** enforced CI gates, signed releases, upgrade
   and rollback procedures, backup/restore automation, operational SLOs,
   incident response, and production topology.

## The ask

Approve a **six-week, read-only pilot beginning from one pinned monorepo
baseline and processing subsequent commits during the evaluation period**,
against one active Go/gRPC migration. The pilot performs monorepo-wide
candidate discovery, typed analysis across the declared eligible
build-target universe, and read-only enrichment from build-graph,
service-catalog, deployment, and ownership metadata. Analysis results
remain bound to individual immutable snapshots; the pilot measures change
between them.

Requirements: an isolated internal deployment, a least-privilege monorepo
identity, one migration owner, a partner with build-graph and
service-catalog access (one person may cover both roles), and a
sanctioned allocation of my time covering the pilot and its
prerequisites. Contract results remain advisory; no deprecation decision
will rely solely on phebs.

Shape: a prerequisite phase before ingestion (threat model, reproducible
build, dependency scan, ACL negative tests — my time plus Security review
capacity); a week-one checkpoint verifying the monorepo is git-accessible
to phebs at cloneable scale **before further prerequisite spend**; then
baseline ingestion, analysis and attribution, and evaluation against the
preregistered gates. All pilot data and derived artifacts — source
mirror, indexes, evidence — remain company property, never leave company
infrastructure, and are destroyed at pilot end unless continuation is
approved.

**Before ingestion begins, the sponsor, migration owner, and platform
partner will agree to preregistered pass, conditional-pass, and stop
criteria** for accuracy, attribution coverage, authorization isolation,
freshness, operating cost, and workflow improvement — so the final
decision is non-political. The evaluation reports against those gates:
call-site accuracy, build-target and service-attribution coverage,
unresolved-mapping rate, authorization isolation and revocation behavior,
cold and incremental processing cost, freshness lag, query latency,
evidence reproducibility, and workflow time saved. The decision is then to
stop, continue incubation, sponsor an internal deployment, or evaluate an
ownership transfer with named maintenance allocation.

**One monorepo. One API migration. One pinned baseline. Every authorized
eligible target.** A representative service slice can seed an initial
operational check, but only the full authorized universe supports a
completeness-shaped statement — preselecting known services would exclude
exactly the unknown consumer that matters.

## Honest risks and adoption criteria

- **Single maintainer.** The decision record and test suite help handoff
  but do not resolve the risk. The pre-ingestion security controls above
  are prerequisites, not future work; beyond them, adoption criteria
  before any dependence are a named second maintainer and the production
  hardening track (roadmap item 5).
- **Extractor accuracy is not yet established**, and service attribution
  is a pilot hypothesis with its own separate evaluation. Both stay
  advisory until measured.
- **Go/gRPC first.** Other languages, protocols, and build systems are
  roadmap, not promises; each gets the same measurement bar.
- **Ingestion assumes a git-accessible monorepo mirror** at cloneable
  scale; phebs is proven on open-source repository scale, not yet on ours.
  Both are verified at the week-one checkpoint, before deeper
  prerequisite and partner time is spent.

## Intellectual property and provenance

phebs is currently distributed under Apache-2.0. Its full commit history,
dependency inventory, SBOM, and development record are available for the
company's normal employment-invention, open-source, and provenance review.
Any internal deployment, sponsorship, licensing, or assignment would be
subject to Legal, OSS, and Security approval.

---

## Appendix A — external validation protocol summary

Preregistered, staged, fail-closed; scope: call-site extraction on the
four-system open benchmark. Stage 0 (sealed): candidate extractor identity
pinned by source commit, toolchain digest, and binary digest; every input
must reproduce byte-identically, twice, from its declared provenance
before sealing; exact finite-population power analysis sizes the sample
per frame (~700 sites at the design points). Stage 1: one automated
snapshot at a sealed cutoff. Stage 2: population enumeration — the
recall-positive frame is constructed independently of the candidate
extractor; previously disclosed sites are conservatively excluded ("burn
on doubt"); exact power is recomputed against actual cardinalities and
fails closed before any human sees a site. Stage 3: two pinned reviewers
with independence attestations label assigned blind samples from source
only; a preregistered overlap subset measures agreement; overlap
disagreements are adjudicated into the frozen gold set, whose hash is
published before scoring. Stage 4: one sealed scoring execution; exact
one-sided hypergeometric lower bounds; Bonferroni family of four
(client-call precision, client-call recall, server-registration
precision, server-registration recall) at 95% joint confidence. Role
classification is additionally checked under the benchmark's sealed
taxonomy (the orthogonal product taxonomy in the main text is the pilot's
model; the external benchmark predates it). Any failure stops the round
with a committed root cause; nothing is retried in place. The internal
shadow evaluation (attribution layers) is specified separately at pilot
start with the same preregistration discipline.

## Appendix B — evidence-layer mechanics

Content-addressed evidence atoms (identical vendored code deduplicates);
associations carry snapshot/path/span placement; assertions carry typed
predicates — CALLS_OPERATION, REGISTERS_GRPC_SERVICE (deliberately
narrower than "implements": implementing an interface does not establish
that the service is registered or served). Publication is atomic and
staged: readers see the previous complete fact set until a new one
commits. Retention is proof-aware — evidence referenced by a retained
bundle is not aged out — subject to an explicit deletion/revocation
exception: retention never overrides loss of authorization, mandatory
deletion, or legal policy. Extraction is fail-closed end to end:
unreadable paths, unsupported constructs, and analysis failures surface in
the coverage statement rather than silently shrinking it.
