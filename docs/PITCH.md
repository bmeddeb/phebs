# phebs pilot proposal

**One monorepo. One Go/gRPC migration. One pinned baseline. Every authorized
eligible target.**

This document owns the bounded adoption ask. The
[pilot charter](./PILOT_CHARTER.md) is the authority for scope, roles, gates,
measurements, teardown, and final decisions; nothing here broadens it. Product
direction belongs in [VISION.md](./VISION.md), current sequencing in
[ROADMAP.md](./ROADMAP.md), and implementation posture in the
[README](../README.md).

## Executive summary

Large API migrations begin with an inventory assembled from code search, build
queries, service catalogs, owner outreach, and runtime observations. Each
channel is useful, but the combined result is often difficult to reproduce,
scope, compare, or hand to another reviewer.

phebs tests a narrower hypothesis: a versioned static call-site inventory,
joined to explicit build/deployable/service/owner metadata and accompanied by
coverage and immutable citations, can reduce the time needed to produce a
reviewable migration inventory.

The proposal is a six-week, read-only pilot over one active Go/gRPC migration.
Results remain advisory. No deprecation, migration, or ownership decision may
rely solely on phebs.

## The pilot question

Can phebs improve the current workflow for identifying and routing potential
consumers of one exact RPC while making uncertainty more visible?

The unit of analysis is fixed before ingestion:

```text
(canonical service candidate, gRPC operation, monorepo snapshot,
 build configuration)
```

The artifact must keep separate:

- exact source occurrences and immutable citations;
- resolved callers, name matches, and unresolved candidates;
- production/test/tooling and first-party/generated/vendor roles;
- source occurrence → build target → deployable → service → owner hops;
- analyzed, excluded, partial, failed, and inaccessible scope;
- static evidence, declared metadata, and runtime observations; and
- machine evidence, human dispositions, and authorized Decisions.

## What the artifact establishes

| Assurance | Supports | Does not establish |
|---|---|---|
| Provenance | reproduction from pinned source | runtime execution |
| Coverage | what authorized scope was processed or failed | absence of unknown extractor blind spots |
| Pack validation | measured behavior of one version on one named population | accuracy on this internal query or another pack |
| Attribution | derivation through pinned metadata | correctness or freshness of the metadata itself |
| Runtime overlay | observed execution in a declared window | dormant or unobserved source consumers |

“Proof-grade” means reproducible evidence, explicit scope, versioned
derivation, and quantified uncertainty—not proof of a universal negative.

## Current readiness

The shipped foundation provides repository sync, search, browsing, SCIP, Git
history, authentication, permissions, audit, OpenAPI, MCP, backup, and restore.
The contract-evidence, Caller Map, proof, and Workbench implementations exist
but remain experimental/default-dark or fixture-bound.

The long-term product direction now makes many services per repository
first-class over shared repository generations. That direction does not expand
this proposal: the pilot still evaluates one exact Go/gRPC migration, one
pinned source universe, and one independently measured workflow. Its scale
checkpoint may inform the multi-service program, but it cannot by itself
authorize a multi-service release or a scale claim.

The retained external Go/gRPC validation campaign ended at a valid
protocol-defined capacity stop before labeling or scoring. Its result is
`NOT_ESTABLISHED`; no numeric accuracy claim exists. The terminal record is
[spike/t111/REPORT.md](../spike/t111/REPORT.md).

The internal pilot validation is therefore the only remaining accuracy gate.
It must independently measure call-site extraction, every attribution hop, and
the end-to-end service-operation edge. An operator bypass or successful demo
does not satisfy that gate.

## The ask

Approve the chartered six-week evaluation with:

- an isolated approved deployment;
- a least-privilege monorepo identity;
- one engineering sponsor and one migration owner;
- build-graph/service-catalog expertise;
- independent label reviewers;
- an environment owner and Security/OSS/Legal review capacity; and
- sanctioned maintainer time for prerequisites, execution, analysis, and
  teardown.

Before any retained source copy, complete the charter’s authorization, threat,
dependency, secrets, egress, negative-ACL, retention, and reproducible-build
gates. A non-source feasibility preflight may verify metadata shape,
authentication, connectivity, and capacity assumptions without ingesting
source.

After entry gates pass:

1. freeze the exact IDL identity, source/build universe, roles, thresholds, and
   current-workflow baseline;
2. perform the authorized clone/index scale checkpoint;
3. produce source evidence and attribution candidates over immutable
   snapshots;
4. run blind internal validation under the preregistered protocol;
5. compare workflow time, usefulness, gaps, and operating cost;
6. stop and tear down, continue incubation, sponsor deployment, or evaluate an
   ownership transfer according to the frozen rubric.

Company source and derived artifacts remain on approved company
infrastructure under company access, confidentiality, retention, and ownership
rules. They are destroyed at pilot end unless an authorized continuation
decision says otherwise.

## Measurements

The charter freezes pass, conditional-pass, and stop values before source
ingestion. The evaluation records:

- call-site precision and recall;
- build-target, deployable, service, and owner attribution accuracy/coverage
  per hop;
- direct end-to-end service-operation edge precision and recall;
- processing completion, failure, exclusion, and unresolved rates;
- authorization isolation and revocation behavior;
- cold/incremental processing cost and freshness;
- bounded query latency and evidence reproducibility; and
- time and reviewer usefulness versus the independently captured current
  workflow.

The current-workflow baseline is governed by
[CURRENT_WORKFLOW_BASELINE_PROTOCOL.md](./CURRENT_WORKFLOW_BASELINE_PROTOCOL.md).

## Required decision artifact

The final packet contains:

- the frozen question, source universe, build configuration, and thresholds;
- tool, pack, rule, schema, and input identities;
- findings and exact citations;
- coverage, exclusions, failures, unresolved sites, and attribution gaps;
- blind-label and scoring records;
- workflow and operating measurements;
- security/authorization findings;
- teardown or continuation evidence; and
- separate validation-gate and human continuation Decisions.

An empty result is never presented without the population and coverage that
bound it.

## Risks and stop posture

- **Single maintainer:** adoption requires named maintenance capacity and
  normal production hardening before organizational dependence.
- **Unestablished extraction accuracy:** all evidence remains advisory until an
  adequately powered internal result passes.
- **Unmeasured internal attribution:** a call site is not a service or owner;
  every hop is evaluated independently and end to end.
- **Scale:** open-source and synthetic results do not establish monorepo
  freshness, memory, or operating cost.
- **Authorization:** read-only Git access still creates another sensitive
  source copy; any leakage, revocation failure, or custody breach stops the
  pilot.
- **Scope pressure:** Thrift, Kafka, other protocols, runtime integration, and
  broader product ideas do not enter this pilot.

Stop conditions and authority live only in the
[pilot charter](./PILOT_CHARTER.md).

## Intellectual property and provenance

phebs is Apache-2.0 and developed as a personal, reference-only implementation.
Its commit history, dependencies, SBOM, and development record are available
for ordinary employment-invention, open-source, security, and provenance
review. Internal deployment, sponsorship, licensing, or assignment requires
the company’s normal approvals.
