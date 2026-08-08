# T43.4 — Deterministic screenshot receipts

> **Retained engineering record.** The receipts harness lives in
> [ui/receipts/](../../ui/receipts/) with committed baselines under
> `ui/receipts/baselines/`. Receipts prove presentation state on the neutral
> dev cohorts; they establish no usability, accuracy, scale, or release
> claim.

**Landed:** 2026-08-07 · **Coverage:** 16 routed surfaces + the sign-in
page × light/dark = 34 baselines (1.7 MB) · **Determinism proof:** two
consecutive check runs at 35/35 against freshly written baselines ·
**Threshold:** `maxDiffPixelRatio 0.001`; drift fails the run and emits a
visual diff into `ui/receipts/.artifacts/`.

## Running

Against a running dev instance (`make dev ARGS="-config phebs-ux-dev.yaml"`):

```
cd ui
PHEBS_RECEIPT_EMAIL=<operator email> PHEBS_RECEIPT_PASSWORD=<password> npm run receipts
```

`npm run receipts:update` is the **explicit reviewed update** — the only
way baselines change. A refreshed baseline is reviewed like any retained
artifact diff. `PHEBS_RECEIPTS_URL` rebinds the target instance. Uses the
system Chrome channel (no browser download); credentials are environment
supplied, never committed.

## Determinism measures

- **Fixed viewport** 1280×800 @1x, UTC, en-US, reduced motion, Playwright
  `animations: 'disabled'`, caret hidden, single worker.
- **Frozen `Date.now()`** (`page.clock.setFixedTime`) so relative-time copy
  cannot drift between capture and check; timers and rAF stay live so
  CodeMirror and streaming surfaces paint normally.
- **Session reuse:** the auth setup reuses a still-valid stored session so
  a receipts run does not append its own login to the audit trail it
  captures.
- **Masking is for self-mutation only:** the audit log table is masked
  because running the harness necessarily writes to it; nothing else is
  masked. The audit page-1 row cap (50) must be saturated for its geometry
  to pin — a fresh instance needs ~50 audited events before its audit
  baselines stabilize.

## Environment-boundness (by design, per the AC)

Baselines bind to: this checkout's absolute repository names (`local/…`
paths in [routes.ts](../../ui/receipts/routes.ts)), the t307 bundle pin
`b7f443ed`, and the dev instance's index generation (server-side timestamps
change when the instance re-indexes). Rebuilding the instance is a baseline
refresh event: run `receipts:update` and review the diff. The harness
proved this boundary live — first-run drift on repos/atlas was the boot's
queued index rebuilds landing between capture and check, and it vanished
once reconciliation settled.

## Deferred / handoffs

- **Densities:** the matrix loops over `comfortable` only until T43.11
  lands the density control; `DENSITIES` in routes.ts is the extension
  point.
- **Deep parameterized surfaces** (caller map 4-tuple, comparison,
  explorer row selection, workbench steps) join the manifest as their
  fixtures gain stable deep links in T43.5–T43.10.
- **Make target (scale-track handoff):** the Makefile is scale-track
  owned. Proposed one-liner for routing:
  `screenshot-receipts: ; cd ui && npm run receipts` (and
  `screenshot-receipts-update` accordingly). Until routed, the npm
  scripts are the entry point.
