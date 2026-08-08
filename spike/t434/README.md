# T43.4 — Deterministic screenshot receipts

> **Retained engineering record.** The receipts harness lives in
> [ui/receipts/](../../ui/receipts/) with committed baselines under
> `ui/receipts/baselines/`. Receipts prove presentation state on the neutral
> dev cohorts; they establish no usability, accuracy, scale, or release
> claim.

**Implementation landed:** 2026-08-07 · **AC status:** open only on the
T43.11 dense-mode dependency · **Current coverage:** 23 authenticated routed
states + the sign-in page × light/dark = 48 baselines · **Determinism proof:**
two consecutive complete check runs at 49/49 against the reviewed baselines ·
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

The repository-level equivalents are `make ui-receipts` and the explicit
review path `make ui-receipts-update`.

## Determinism measures

- **Fixed desktop viewport** 1280×800 @1x, UTC, en-US, reduced motion,
  Playwright `animations: 'disabled'`, caret hidden, single worker. Named
  narrow states may override the viewport explicitly; T43.5's open authority
  drawer is also pinned at 390×844.
- **Frozen `Date.now()`** (`page.clock.setFixedTime`) so relative-time copy
  cannot drift between capture and check; timers and rAF stay live so
  CodeMirror and streaming surfaces paint normally.
- **Session reuse:** the auth setup reuses a still-valid stored session so
  a receipts run does not append its own login to the audit trail it
  captures. Session state lives in ignored `ui/receipts/.auth/`, outside the
  Playwright output directory that is cleared at the start of each run.
- **State readiness, not elapsed time:** an injected fetch counter must reach
  zero, loading statuses must disappear, and `main` must remain unchanged for
  three consecutive 250 ms samples. The former fixed 1.2-second sleep is gone.
- **Masking is for self-mutation only:** running the harness necessarily
  changes audit values, so only dynamic audit-cell children are masked; table
  headers, rows, spacing, borders, and status-chip layout remain compared.
  Analytics content is usage-derived in whole and remains masked. The audit
  page-1 row cap (50) must be saturated for its geometry to pin — a fresh
  instance needs ~50 audited events before its audit baselines stabilize.

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
  lands the product density control; `DENSITIES` in routes.ts is the extension
  point. T43.4 therefore remains open on this one AC rather than fabricating a
  dense state the product cannot select.
- **Deep parameterized states:** caller map and caller comparison now retain
  their honest routed required-identity states. Populated tuples, explorer row
  selection, and Workbench steps join as their owning tickets provide stable
  neutral-fixture deep links; they are states within already covered routes,
  not omitted routed surfaces.
