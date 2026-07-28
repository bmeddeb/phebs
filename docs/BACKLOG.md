# phebs · active backlog

This file contains current and unscheduled work only. Completed Epics 0–23 and
P5 hardening are retained in the [completed backlog](./BACKLOG_COMPLETED.md).
Current sequencing and product posture are summarized in
[ROADMAP.md](./ROADMAP.md).

Tickets are PR-sized and dependency-ordered for a stacked workflow. Acceptance
criteria (AC) are the merge bar. Decisions land as dated ADR rows in
[PLAN.md](../PLAN.md).

Conventions: `T<epic>.<n>` · dependencies are listed only where they cross
epics or gates.

---

## EPIC 24 — Documentation information architecture *(in progress)*

### Product outcome

A reader can reach the right answer without reconciling duplicate documents:
the public README explains the product and first run, the manual owns
user-visible behavior, the backlog owns active work, PLAN owns dated decisions,
and the documentation map routes every deeper contract or retained record.
Historical validation remains intact and current product claims remain bounded.

### Safety boundary

- `spike/t111/` is sealed evidence. No Epic 24 ticket may rewrite, relocate,
  delete, or silently reclassify those files.
- Historical PLAN ADR rows are append-only. A current summary may link to them
  but cannot replace them.
- Consolidation may remove duplicated prose only after its surviving authority
  is named and inbound links are updated.
- Documentation cleanup changes no runtime behavior, capability registration,
  evidence tier, validation result, or production gate.

**T24.1 ✅ · Ownership map and documentation guards** *(2026-07-27)* — make
`docs/README.md` the complete routing and ownership index; repair existing
local-link drift; add an executable check for tracked Markdown links,
documentation-map reachability, and the sealed T11.1 tree digest; record the
boundary in PLAN. AC: every tracked document under `docs/` is reachable from
the map; every tracked local Markdown/HTML link resolves; the sealed-tree
digest matches the pre-cleanup baseline; focused Go test green.

**T24.2 ✅ · Concise public README** *(2026-07-27; needs T24.1)* — reduce the root README to a
product-first landing page: problem, shipped capabilities, clearly separated
experimental/default-dark capabilities, architecture image, five-minute local
start, and links to authoritative detail. Remove deep operational and planning
duplication. AC: a new reader can identify what phebs is, what is safe to claim,
and how to start without reading PLAN or BACKLOG; README links pass T24.1.

**T24.3 ✅ · Task-oriented user guide** *(2026-07-27; needs T24.2)* — split the large manual
into a short navigation page plus task-oriented install, configuration,
workflows, and operations guides while preserving the generated glossary
boundary and updating code references atomically. AC: no user workflow is lost;
configuration remains canonical in `config.example.yaml`; generated glossary
verification and documentation guards pass.

**T24.4 ✅ · Active roadmap and historical archive** *(2026-07-27; needs T24.3)* — introduce a
short active roadmap and move completed ticket narratives out of the working
backlog into a linked, immutable historical archive without changing their
text. Add a current architecture summary above the PLAN ledger while leaving
all historical ADR rows byte-untouched. AC: BACKLOG contains only active or
unscheduled work plus archive links; PLAN retains its decision history; old
ticket and ADR anchors remain discoverable.

**T24.5 · Product and adoption consolidation** *(needs T24.4)* — remove repeated
product explanations across VISION, INVESTIGATIONS, PITCH, and pilot material by
assigning one concept to one authority and replacing copies with links.
Normative domain, envelope, pack, and pilot contracts remain separate. AC: no
downstream document expands the pilot ask or product claim; terminology and
status are consistent across the surviving suite.

**T24.6 · Contributor and retained-record cleanup** *(needs T24.5)* — make
AGENTS/CLAUDE a single maintained contributor contract with a thin compatibility
pointer, replace the generic UI README with repository-specific instructions,
and classify non-sealed spikes and design handoff material as retained
engineering records. AC: contributor instructions have one authority; all
retained records are reachable; no sealed T11.1 byte changes; full merge bar.

## Deliberate non-goals *(per historical PORT_MAP §7/§12)*

SCIM provisioning, multi-org RBAC / seats, and a cloned "Ask" chat app —
phebs stays **MCP-first** (agents bring their own chat) and **single-tenant**.
Kubernetes/Helm waits for the P6 fleet profile. Anonymous-access and
entitlement gating are deleted outright (config bool, no license backend).

---

## Standing rules

- Decisions land as dated ADR bullets in PLAN.md, same PR as the change.
- Every epic ends with a `make dev` demo state — no epic is "done" if it
  can't be shown end-to-end.
- Upstream repo is behavior reference only; `ee/` paths never opened.
- Personal hardware, personal time, no employer code or credentials.
