# GATE2-V2 Stage-2 P0 failure review r2 — authorization t111-gate2-v2-p0-79d1442-02

**Verdict: FAILURE ROOT-CAUSED FROM THE DURABLE RECEIPT ALONE — one defect
(hydration/verification scope mismatch). P0-02 is terminally consumed; no
retry under it. Fail-closed behavior correct; R1, R2, and R3 are validated
live.**

Reviewer: Claude, independent of implementation and launch (operator fired).
No ceremony, derived-root, hydration, extraction, enumeration, selection, or
disclosure operation was performed by this review; diagnostics are the
receipt itself plus read-only corpus queries.

## Bindings

- Authorization: `t111-gate2-v2-p0-79d1442-02`
  `sha256:fdccd52f10f7576695fe7e4a2f22194ec48084a5f4192b2c4859947847f9474e`
- Terminal receipt: `…-79d1442-02-ceremony/terminal.json`, schema v2, status
  ABORTED, failure REFUSED, `evidence_receipt_sha256: null`, consumption
  marker `sha256:c148ccc1…`, failing step `hydrate.temporal`, bounded stderr
  `sha256:89d8cf98354c91bbcfc3d0f28780e55015da574432290eb8c30e154eed4cddae`
  (not truncated).

## Validated by this run

- **R1/R2 live validation.** All four corpus bundles were created
  (`bundles/{dapr,loki,online-boutique,temporal}.bundle`) — the ref-bearing
  recipe and unshallowed sources cleared the entire P0-01 failure surface.
- **R3 live validation.** The terminal receipt named the failing step and
  preserved the decisive stderr line verbatim; no reproduction was needed.

## Finding F4: verification scope exceeds hydration scope

The failing command chain was `build bound harnesses → verify hydrated
harness dependencies → go mod verify`, which failed with
`cloud.google.com/go@v0.118.0: module lookup disabled by GOPROXY=off` in the
offline phase. Temporal's go.mod (both old and sealed heads) selects
`cloud.google.com/go v0.123.0`; `v0.118.0` is a non-selected module-graph
version whose metadata `go mod verify` nevertheless demands. The hydration
recipe deliberately fetches only the bound harness targets and each corpus
package/test closure (the campaign's established narrow-hydration posture),
so whole-graph artifacts are absent by design and the offline refusal is
correct. The defect is the verification command, not the hydration or the
environment: a graph-wide `go mod verify` was reintroduced where the prior
campaign had already replaced whole-graph operations with closure-scoped
h1 verification for exactly this reason.

## Remediation requirement binding any successor authorization (P0-03)

- **R6 — verification scope must equal hydration scope.** Replace the
  graph-wide `go mod verify` step with h1 verification of exactly the
  hydrated closure (the established independent-reader pattern), or extend
  hydration to fetch the complete module-graph metadata the chosen verifier
  demands. Either way, add a regression test in which a synthetic module's
  graph contains a non-selected older version: narrow hydration plus the
  chosen verifier must pass offline, and a genuinely missing closure module
  must still refuse.
- R1–R5 carry forward unchanged. Fresh authorization ID and digests; the
  P0-02 ceremony directory is preserved evidence.

No retry, no enumeration, no Stage-2 preparation is authorized by this
record. `gate_status` remains `PENDING`.
