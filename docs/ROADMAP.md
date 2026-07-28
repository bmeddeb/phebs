# phebs roadmap

This is the current sequencing view. Exact acceptance criteria live in the
[active backlog](./BACKLOG.md), completed implementation history lives in the
[completed backlog](./BACKLOG_COMPLETED.md), and architecture decisions live in
[PLAN.md](../PLAN.md).

## Current product posture

phebs ships as a self-hosted, single-node Go application with:

- Git repository synchronization, zoekt search, repository browsing, SCIP code
  navigation, and Git history;
- local authentication, revocable API keys, optional OIDC, permissions, audit,
  OpenAPI, and stateless MCP;
- supervised local SurrealDB state, bounded index-builder children, backup and
  restore, and deterministic release tooling.

The contract-intelligence, Caller Map, Change Workbench, Thrift-field, and
Kafka evidence stacks are implemented but remain experimental/default-dark or
fixture-bound. Their retained external validation result is
`NOT_ESTABLISHED`; they do not establish runtime use, completeness,
compatibility, migration completion, decommission safety, or extraction
accuracy.

## Now

Epic 24’s documentation reset is complete: public entry points, task guides,
planning/history separation, product/adoption ownership, contributor
instructions, and retained-record classification are all in place.

No implementation ticket is currently scheduled. New work requires an
explicit decision to pursue the gated evidence/pilot path or the demand-driven
P6 fleet profile below.

## Gated product work

Production registration of the evidence and Workbench surfaces still requires:

1. retained validation that satisfies the documented gate rather than an
   operator bypass; and
2. a separate explicit pilot-continuation decision.

Until both exist, the implementation remains dark regardless of feature
completeness or demo quality.

## On demand

The next scale boundary is the P6 fleet profile. It is intentionally not
scheduled until a real deployment requires it:

- measure index size, memory, full-monorepo build time, and freshness on the
  target corpus;
- decide from measurements whether delta builds are required;
- design replica placement, peer search, and per-client admission budgets;
- cut SurrealDB from the supervised local child to an external deployment only
  when distributed ownership requires it;
- rerun queue consistency characterization against the selected distributed
  store;
- add image, Kubernetes, or Helm packaging only for an actual fleet operator.

Standalone remains the supported development and small-deployment profile.

## Deliberate non-goals

- cloning an Ask/chat product; phebs stays MCP-first;
- SCIM, multi-organization seats, billing, or entitlement infrastructure;
- anonymous access;
- silent filesystem-wide repository discovery;
- treating runtime telemetry or static evidence as a substitute for the other.
