# phebs documentation

This page is the documentation index. Pick the shortest path that matches what
you are trying to do; the ownership table below identifies which document wins
when two pages appear to overlap.

## Start here

| Goal | Read |
|---|---|
| Understand the product | [Project README](../README.md), then [VISION.md](./VISION.md) |
| Install, configure, or operate phebs | [MANUAL.md](./MANUAL.md), then its task guide |
| Plan or implement a change | [ROADMAP.md](./ROADMAP.md), then [BACKLOG.md](./BACKLOG.md) and [PLAN.md](../PLAN.md) |
| Evaluate a pilot | [PITCH.md](./PITCH.md), then [PILOT_CHARTER.md](./PILOT_CHARTER.md) |
| Review an evidence pack | [EVIDENCE_PACK_CARD.md](./EVIDENCE_PACK_CARD.md) and [PACK_MANIFEST.md](./PACK_MANIFEST.md) |

## Content ownership

| Information | Authority | Maintenance rule |
|---|---|---|
| Public overview and first successful run | [Project README](../README.md) | Keep short; link to detail instead of repeating it |
| User-visible behavior, workflows, and operations | [MANUAL.md](./MANUAL.md) and its task guides | Update the owning guide with every behavior change |
| Annotated configuration | [config.example.yaml](./config.example.yaml) | Canonical option names, defaults, and examples |
| Current sequencing | [ROADMAP.md](./ROADMAP.md) | Summarize current posture and next decisions |
| Active and proposed work | [BACKLOG.md](./BACKLOG.md) | Tickets and acceptance criteria are the merge bar |
| Completed ticket history | [BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md) | Append through a reviewed archive move; do not rewrite completed narratives |
| Architecture and decisions | [PLAN.md](../PLAN.md) | Append dated ADR rows; do not rewrite historical decisions |
| Product direction | [VISION.md](./VISION.md) | Describe direction, not current behavior or setup |
| Pilot authority and claims | [PILOT_CHARTER.md](./PILOT_CHARTER.md) | Downstream documents cannot broaden its authority |
| Validation records | [`spike/t111/`](../spike/t111/) | Sealed evidence: never rewrite, relocate, or summarize as a new result |

## User guides

- [Getting started](./guides/GETTING_STARTED.md) — prerequisites, build, first
  run, and administrator setup.
- [Configuration and connections](./guides/CONFIGURATION.md) — authentication,
  connectors, synchronization, webhooks, watch mode, and cleanup.
- [Product workflows](./guides/WORKFLOWS.md) — demos, Workbench, search, UI,
  SCIP/history, HTTP, and MCP.
- [Operations and development](./guides/OPERATIONS.md) — storage, backup,
  security, extraction operations, metrics, troubleshooting, and contributor
  commands.
- [Annotated configuration](./config.example.yaml) — exhaustive accepted
  options and defaults.

## Product and interface contracts

These documents specify product concepts and transport shapes. They do not
replace the user manual.

- [VISION.md](./VISION.md) — product direction and sequencing.
- [INVESTIGATIONS.md](./INVESTIGATIONS.md) — Investigation product shape and UX.
- [INVESTIGATION_DOMAIN_CONTRACT.md](./INVESTIGATION_DOMAIN_CONTRACT.md) —
  normative Investigation identities, lifecycle, authorization, and review
  semantics.
- [MCP_ENVELOPE.md](./MCP_ENVELOPE.md) — normative MCP projection of the
  Investigation contract; generated schemas live in [`../schemas/`](../schemas/).
- [PITCH.md](./PITCH.md) — bounded pilot proposal.

## Evidence-pack contracts

- [EVIDENCE_PACK_CARD.md](./EVIDENCE_PACK_CARD.md) — capability and validation
  template.
- [PACK_MANIFEST.md](./PACK_MANIFEST.md) — manifest schema and lifecycle.
- [THRIFT_PACK_CARDS.md](./THRIFT_PACK_CARDS.md) — Thrift declaration,
  consumer, and field-reference pack cards.
- [KAFKA_PACK_CARDS.md](./KAFKA_PACK_CARDS.md) — Kafka producer and consumer
  pack cards.

These packs remain experimental-dark unless the manual and capability response
explicitly say otherwise. Their retained validation result is not an accuracy
or completeness claim.

## Pilot governance and preparation

Read [PILOT_CHARTER.md](./PILOT_CHARTER.md) first. The remaining documents
prepare or record narrower parts of that process and grant no authority by
themselves.

- [PILOT_PREREQS.md](./PILOT_PREREQS.md) — prerequisite ownership and status.
- [THREAT_MODEL.md](./THREAT_MODEL.md) — trust boundaries.
- [NEGATIVE_TEST_DESIGN.md](./NEGATIVE_TEST_DESIGN.md) — authorization and
  integrity test matrix.
- [SIZING_ASSUMPTIONS.md](./SIZING_ASSUMPTIONS.md) — capacity worksheet.
- [RESTORE_PROCEDURE.md](./RESTORE_PROCEDURE.md) — backup and restore
  acceptance design.
- [ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md) — preregistered
  internal-validation protocol.
- [DECISION_RECORDS.md](./DECISION_RECORDS.md) — validation and continuation
  decision templates.
- [ATTRIBUTION_HOP_SHEETS.md](./ATTRIBUTION_HOP_SHEETS.md) — attribution label
  sheets.
- [CURRENT_WORKFLOW_BASELINE_PROTOCOL.md](./CURRENT_WORKFLOW_BASELINE_PROTOCOL.md)
  — manual-versus-phebs workflow comparison.
- [NO_CONFLICTING_DEPENDENCY_STATEMENT.md](./NO_CONFLICTING_DEPENDENCY_STATEMENT.md)
  — Gate 0 dependency worksheet.
- [EXTRACTOR_BRIDGE_WORKSHEET.md](./EXTRACTOR_BRIDGE_WORKSHEET.md) — typed
  benchmark to pure-reader identity bridge.
- [GATE0_READINESS.md](./GATE0_READINESS.md) — descriptive readiness audit.
- [GATE0.md](./GATE0.md) — synthetic Gate 0 fixture and bypass boundary.
- [GATE0_REHEARSAL.md](./GATE0_REHEARSAL.md) — synthetic ceremony rehearsal.

## Retained fixtures and design references

- [Change Workbench fixture](./fixtures/change-workbench/README.md)
- [Investigation envelope fixtures](./fixtures/investigations/README.md)
- [Thrift field-reference fixture](./fixtures/thrift-field/README.md)
- [Brand and UI handoff](./design_handoff_phebs_brand_and_ui/README.md), including
  its [token notes](./design_handoff_phebs_brand_and_ui/notes/tokens.md)

Fixtures are deterministic test inputs, not public-corpus validation or product
claims.

## Validation and measurement records

- [T11.1 / GATE2-V2 report](../spike/t111/REPORT.md) and
  [sealed record index](../spike/t111/labeling/README.md)
- [T19.1 Thrift validation spike](../spike/t191/README.md)
- [T20.1 store and scale spike](../spike/t201/README.md)
- [T21.1 Workbench inventory and glossary](../spike/t211/README.md)
- [T22.1 Thrift field-reference spike](../spike/t221/README.md)
- [T23.1 Kafka evidence spike](../spike/t231/README.md)

The T11.1 tree is sealed history. Later spike reports are retained engineering
records and remain distinct from current user documentation.
