# T43.1 — Surface audit and findings ledger

> **Retained design record.** This directory holds the Epic 43 baseline audit
> of every routed phebs product surface against
> [docs/DESIGN_CHARTER.md](../../docs/DESIGN_CHARTER.md). It records findings
> and their charter rules; it changes no production file and establishes no
> usability, accuracy, scale, or release claim.

**Audited:** 2026-08-07 · **Method:** dual-agent (isolated design review +
isolated deterministic detector) plus a bounded live-browser round ·
**Score:** Nielsen 23/40 (Operate mode, all ten heuristics applicable) ·
**Findings:** 38 (4 blocker · 22 major · 12 minor) in [LEDGER.md](./LEDGER.md)

## Method

- **Assessment A (design review):** an isolated reviewer read the charter,
  `App.tsx`, `router.ts`, `theme.ts`, `Brand.tsx`, all 19 routed pages, and
  the shared evidence components in full, and returned the specificity
  verdict, heuristic scores, per-surface charter rubric, findings, cognitive
  load, emotional journey, and persona walkthroughs recorded here.
- **Assessment B (deterministic detector):** an isolated run of the
  impeccable detector over all 69 `ui/src` source files. Result: 4 findings,
  all one rule (`side-tab` accent border), all judged likely false positives
  on semantic status-callout bands — with the true signal being that the
  same band is re-implemented locally in four pages (ContractAtlasPage:1197,
  InvestigationPage:431, ServiceDirectoryPage:539,
  WorkbenchEvidenceSteps:1567), a shared-kit candidate for T43.3.
- **Live browser round (bounded):** first-run setup, sign-in, Search shell,
  and Repos were exercised against a dedicated dev instance
  (`127.0.0.1:3073`, own data dir). Fixture indexing was refused by the
  T35.3 hard disk watermark (host disk at 92%), so populated-surface
  confirmation is deferred; every ledger finding is source-verified with
  exact file/line references and does not depend on that confirmation. The
  refusal itself is retained as live evidence for F25/operator findings:
  the Repos table showed only red "Failed index" with a truncated path
  while the actual cause (disk pressure) appeared only in server logs.

## Reading the ledger

Each finding names severity (blocker = violates a charter MUST or makes a
journey unusable; major = clear charter violation or significant usability
cost; minor = polish), surface, exact element/line, the charter rule
violated, the defect, and a concrete fix direction. The per-surface rubric
table marks hierarchy-of-trust, density, states, keyboard, motion, and
voice per routed page. Ticket routing for every finding is recorded in
[LEDGER.md](./LEDGER.md) §4 and seeds T43.2–T43.12.
