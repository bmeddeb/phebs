# Microservice change workflow

This standalone walkthrough exercises phebs' experimental service-centered
workflow from code discovery through exact static evidence. It uses only the
neutral development cohort supplied by `make dev`; you do not need PLAN,
backlog, or architecture documents to follow it.

The workflow is advisory. Service identities come from an explicit catalog;
relationships come from static source observations; Workbench suggestions are
evidence summaries; and human Dispositions are review annotations. None of
them is runtime topology, a task, a deployment decision, or proof that a
migration or retirement is safe.

## Start the neutral cohort

From the repository root:

```sh
make dev
```

Sign in and wait for both retained neutral repositories to become indexed. A
repeat start with the same data directory exercises the ordinary no-op and
recovery paths; it must preserve the same current authorities rather than
minting a second fixture universe.

The development cohort deliberately keeps two authorities separate:

- `t323-neutral-corpus` is the whole-repository All code/service-search cohort.
  Its current accepted identities are `svc.orders-api` and `svc.fulfillment`;
  its other explicit states are `svc.proposed`, `svc.conflicted`, and the
  removed `svc.shipping` successor lineage. See the retained
  [search demo](../fixtures/t34.4-service-search/README.md).
- `t307-neutral-service` is the focused extraction and workflow-state cohort.
  It supplies the five identities used by the four change stories below.

Do not carry a service key or generation from one repository into the other.
The T37.5 relationship receipt is a separate three-repository source-free
envelope; `make dev` does not seed synthetic relationship or Workbench
responses to make the walkthrough look complete.

The workflow-state catalog contains five intentionally different identities:

| Service | Catalog state | Purpose in the walkthrough |
|---|---|---|
| `orders-api` | accepted | current RPC service and focused search scope |
| `orders-events` | accepted | shared-source Kafka-facing service |
| `returns-proposal` | proposal | explicit unaccepted candidate for an add story |
| `billing-control` | conflict | visible authority-conflict failure state |
| `legacy-orders` | removed/rejected, successor `orders-api` | migration and retire lineage without a safety claim |

## Follow the evidence chain

1. **All code discovery.** Open Search with **All code**, select the
   `t323-neutral-corpus` repository, and search for `Orders`. Confirm the
   result cites the exact indexed revision, then open Services and choose
   `svc.orders-api` → **Search this service**. Search for `Orders` again. The
   service receipt must name the exact catalog, source, search, and service
   generations and must never fall back to All code. Return to All code and
   search for `package risk` to confirm explicitly unowned source remains
   searchable there but outside either accepted service scope.
2. **Service overview.** In the `t323-neutral-corpus` directory, inspect
   `svc.orders-api` and its current primary, supporting, generated, typed, and
   shared placements. Then switch to `t307-neutral-service`, include removed
   identities, and open `orders-api` and `orders-events` to inspect the five
   workflow-state identities. These are repository-local catalog claims, not
   inferred ownership. A desired service without an exact active generation
   remains visibly unavailable.
3. **Dependency evidence.** Choose **Explore relationships**. Keep the exact
   service key and repository in the route, inspect RPC and Kafka rows, open
   one immutable citation, and note every root/incarnation/generation receipt.
   Unowned, proposal, conflict, ambiguous, empty, failed, unavailable, and
   truncated states stay explicit.
4. **Comparison.** When two exact retained relationship roots are available,
   compare them with both generation and root digests. Otherwise record the
   explicit unavailable/gap state; do not substitute current rows or infer an
   empty delta.
5. **Workbench.** Start from the exact
   `/demo.orders.v1.Orders/Create` contract identity. Fill the human-owned Why
   fields, preserve the exact repository/lineage/operation selection, then use
   the story table below. Preview before any creation. Where must preserve
   service authority, affected/unowned rows, caller and comparison gaps, and
   resource-plane states. How may record only an explicit human Disposition;
   it creates no task or Decision.
6. **Proof.** Open the proof/coverage views from the exact operation or topic.
   Keep supported facts, extractor abstentions, failed/processing domains,
   excluded `go_test` input, and unsupported planes separate. A zero row page
   is meaningful only inside its displayed eligible and completed scope.
7. **MCP parity.** With a named API key, call `list_services`, `get_service`,
   `list_service_relationships`, `compare_service_relationships`, and
   `read_service_relationship_citation`. When the Workbench annex is present,
   call `get_change_workbench_impact` with the exact Investigation/revision and
   optional service filters. The tool result must carry the same authority,
   gaps, cursor, errors, and caveat as HTTP. Agent output remains evidence,
   never Decision authority.

## Run the four change stories

| Story | Exact selections | Evidence question | Honest stopping state |
|---|---|---|---|
| Add | current/analogous `orders-api`; proposal `returns-proposal` | Which existing contracts, callers, topics, and service placements are relevant to the proposed service? | Proposal and unowned rows remain unaccepted; no inferred future callers |
| Modify | current `orders-api`; affected `orders-events` | Which exact callers, shared placements, and topic rows cite the current operation or files? | Current rows plus explicit gaps; no runtime-use or completeness conclusion |
| Migrate | current `legacy-orders`; replacement `orders-api` | What differs between exact retained caller/relationship generations, and which service claims cover each side? | Added/removed/unchanged evidence or an unavailable comparison; successor lineage is not migration completion |
| Retire | current `legacy-orders`; related `orders-api`/`orders-events` evidence | Which exact callers, topics, unresolved sites, and coverage gaps remain visible? | Evidence and gaps only; never a decommission-safe conclusion |

For every story, reload the exact route and exercise browser back/forward.
Service repository, source/target keys, Investigation, revision, step, filters,
and cursor must remain reproducible. A stale revision, expired citation,
changed root/incarnation, malformed authority, permission loss, interrupted
publication, or restored-but-unrebuilt derived generation must fail closed
with a bounded retry/restart path.

## Responsive and accessibility check

Run the journey once at a desktop viewport and once at 390 px wide. At both
sizes:

- the page title and primary heading identify the current surface;
- keyboard focus reaches repository/service selectors, tabs, filters,
  citations, Workbench steps, and retry controls in a sensible order;
- controls have stable accessible names and visible focus;
- exact tables may scroll locally but the document does not overflow;
- gaps and state changes are conveyed by text, not color alone;
- loading, empty, partial, error, and retry states remain announced; and
- the browser console has no relevant warning or error.

## What this demo establishes

The retained T38.5 receipt binds the deterministic neutral story shape to the
production-path tests for search, directory, relationships, comparison,
Workbench, proof, MCP, failure, restart, responsive layout, and accessibility.
It establishes those mechanics only. The surfaces remain experimental until
Epic 39 separately evaluates correctness, scale, recovery, security,
operations, and release posture. `GATE2-V2` remains `NOT_ESTABLISHED`.
