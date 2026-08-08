# T43.12 charter closure — the T43.1 ledger bound to its resolutions

Source-free presentation record. Binds every finding in
[LEDGER.md](./LEDGER.md) to the ticket that resolved it, re-scores the
T43.1 heuristics against the shipped tree (`ux/t43.12-motion`), and
records the open residue honestly. Claims here are presentation
mechanics only — nothing about evidence accuracy, coverage, or scale
behavior.

## 1. Finding resolutions

Sev **B** blocker · **M** major · **m** minor.

| ID | Sev | Resolution | Where |
|---|---|---|---|
| F1 | B | Resolved | T43.1f hotfix — per-prefix capability gate pages (CapabilityGate); a failed capability read renders "unknown", never absence |
| F2 | B | Resolved | T43.1f — bare fragments removed; T43.11f made row jumps URL pins (`sel_repo`+`sel_row`) |
| F3 | B | Resolved | T43.8 (+f) — Atlas/CallerMap/Comparison filters, cursors, selection, continuation in the URL; T43.11 extended the discipline to narrow/group/row pins |
| F4 | B | Resolved | T43.2 — AA-passing status text tones both themes, pinned by theme.test |
| F5 | M | Resolved | T43.1f — fixture ID removed from chrome |
| F6 | M | Resolved | T43.1f — loading state precedes the empty claim |
| F7 | M | Resolved | T43.3 — shared labeled `LoadingBlock` on route/gate loads |
| F8 | M | Resolved | T43.3 — poll errors keep the last-good table with a bounded error band and retry |
| F9 | M | Resolved | T43.5 — digests are identity objects at the audit altitude, copyable, never leading |
| F10 | M | Resolved | T43.5 — authority collapses to the chip row; the answer leads |
| F11 | M | Resolved | T43.6 — one `ClaimBoundary` with establishes / does-not-establish disclosure |
| F12 | M | Resolved | T43.5 — the authority drawer exists; status chips open it; generations named; staleness worn relative+absolute |
| F13 | M | Resolved | T43.11 — product density control (comfortable/dense), tokens from T43.2 |
| F14 | M | **Partial** | T43.2 landed the type-scale tokens; the 11px floor is not universal — 138 sub-11px sites remain as the evidence-metadata dialect (9–10.5px mono/meta). Carried forward below |
| F15 | M | Resolved | T43.2 — `textTertiary` minimum for legible text |
| F16 | M | Resolved | Charter §1 census exception (adjudication 2, 2026-08-07) + T43.6 one-line naming |
| F17 | M | Resolved | T43.1f — tabs disabled/annotated on refusal |
| F18 | M | Resolved | T43.8 — step rail carries locked/done/available with reasons |
| F19 | M | Resolved | T43.8 — one filter surface, one Apply |
| F20 | M | Resolved | T43.9 — keyboard symbol selection and panel shortcut |
| F21 | M | Resolved | T43.9 (+f) — aria-activedescendant cursor; streamed batches no longer steal focus |
| F22 | M | Resolved | T43.9 — caller pages keyboard-complete; cursor-cap explained |
| F23 | M | Resolved | T43.3 — humanized labels; closed-vocabulary tones |
| F24 | M | Resolved | T43.2 tokens + **T43.12**: zero literal durations outside reduced `0ms`, easings tokenized (incl. `MOTION.linear` for indicator loops), pinned by a source-level motion contract test |
| F25 | M | Resolved | T43.2/T43.10 — `statusToneFor`; capacity-unavailable is blue |
| F26 | m | Resolved | T43.8 — service routing carries repository context |
| F27 | m | Resolved | T43.6 — voice pass; no in-product persuasion |
| F28 | M | Resolved | T43.3 — per-file path+content rows replace JSON textareas |
| F29 | M | Resolved | T43.6 — copy pass; "exact" reserved for contrastive use (voice residue noted in §3 scoring) |
| F30 | m | Resolved | T43.6 — in-product syntax help, neutral example |
| F31 | m | Resolved | T43.6 — absence claims name their bounds |
| F32 | M | Resolved | T43.2 + T43.10 adjudication — `unresolved` is stale/amber everywhere |
| F33 | m | Resolved | T43.3 — sticky offsets and label parity |
| F34 | m | **Open** | Routed to T43.11, not absorbed: Commit `PatchView` stays a flat div-per-line diff (no per-file grouping, no `contentVisibility`). Carried forward below |
| F35 | m | Resolved | T43.3 — dead quarter removed |
| F36 | m | Resolved | T43.10 (+f) — per-bar accessible values; zero tick at the 3:1 graphical floor |
| F37 | M | Resolved | T43.3 — the kit; one chip dialect, one focus ring, shared truncation and validation |
| F38 | m | Resolved | T43.8 — every route titles itself |

**36 resolved, 1 partial (F14), 1 open minor (F34). Zero blockers open.**

## 2. T43.12 motion pass

Motion is a closed system (T43.12f). The theme's helpers —
`animated`, `transitioned`, `panelTransition` — are the only place
motion properties may be written: they admit compositor properties only
(opacity/transform, typed so the compiler rejects paint properties),
token durations and easings only (literal-union types), and embed the
reduced-motion path by construction. `ui/src/motion.test.ts` closes the
system at the source level: WAAPI is banned, raw motion properties and
literal durations are banned outside `theme.ts`, and each helper is
verified to carry its reduced override. Under this contract the brand
lockup's hover dock (decorative WAAPI motion on navigation) was
removed, the loader was rebuilt as CSS choreography through the helper
(its stroke draw became an opacity reveal — compositor-only; with
animations removed the attribute state is the complete settled logo),
the shimmer sweeps became translating overlays (no background
repaints), and both drawer layers ride the panel token so container and
backdrop settle together. The pass added the two entrances the sweep
justified and nothing else: the citation panel (chip → panel arrives,
260ms panel token) and the caveat disclosure body (160ms element
token). Deliberately not animated, recorded as correct: the command
navigator and all list navigation (keyboard-frequency disqualifier),
detail-region swaps (keyboard-committed, data being read), the scope
bar and status words (trust surfaces), density flips, and load-time
notices (no user state transition).

## 3. Re-critique (same instrument, same mode as T43.1 §2)

| # | Heuristic | T43.1 | Now | Evidence |
|---|---|---|---|---|
| 1 | Visibility of system status | 3 | 4 | F6/F7 fixed; lifecycle cards wear observed-at; streaming meta unchanged. No auto-refresh keeps it off 5 |
| 2 | Match system/real world | 2 | 3 | F23/F29/F30 landed; contract-shaped phrasing still surfaces in places |
| 3 | User control & freedom | 2 | 4 | URL discipline is now product-wide, incl. narrow/group/row pins; one native `window.confirm` remains (S1) |
| 4 | Consistency & standards | 1 | 4 | One kit, one vocabulary, one focus ring, one status module; radii still vary by surface |
| 5 | Error prevention | 4 | 4 | Unchanged strengths |
| 6 | Recognition over recall | 2 | 3 | Required-identity guidance landed; Workbench resume still asks for a pasted ULID |
| 7 | Flexibility & efficiency | 2 | 5 | Global navigator, shortcuts, density control, virtualized keyboard lists — now a defining strength |
| 8 | Aesthetic & minimalist | 2 | 3 | Digests behind the audit altitude; caveats collapse; the sub-11px metadata dialect (F14 residue) holds it at 3 |
| 9 | Error recovery | 3 | 4 | F8 fixed; bounded errors on catalog surfaces; raw `String(cause)` persists on History/Analytics (S2) |
| 10 | Help & documentation | 2 | 3 | Re-audited (T43.12f): SectionHelp reaches Search, Contract Atlas, Caller Map, Impact, Kafka Topics, and the Workbench through the analysis scope panel, plus Impact's direct use — the first pass undercounted it as one surface. The catalog (directory/explorer) and git surfaces still carry no in-product help, which holds the score at 3 |

**Total: 37/40** (T43.1 baseline 23/40).

## 4. Open residue (post-epic candidates, none blocking)

- **F14 (partial, M):** universal 11px floor vs. the 9–10.5px evidence-metadata dialect — needs a charter adjudication (floor as written vs. a named metadata exception) before enforcement.
- **F34 (open, m):** Commit diff per-file grouping + `contentVisibility`.
- **S1 (m):** replace the Workbench native `window.confirm` with the house dialog.
- **S2 (m):** bounded errors on History/Analytics (`boundedError` exists; two pages bypass it).
- **S3 (m):** extend SectionHelp to the catalog (directory, explorer) and git (file, history, blame, commit) surfaces; it already reaches six surfaces through the analysis scope panel.

## 5. Receipts at closure

The retained matrix is 118 baselines — 29 authenticated routes ×
2 themes × 2 densities, plus the themed sign-in pair (sign-in precedes
any user preference, so it captures per theme only). The cardinality is
derived from the executable manifest, not hand-counted:
`ui/src/receiptManifest.test.ts` asserts baseline count = routes ×
themes × densities + themes. The matrix verified green in independent
runs, with the repos pair restored-then-recovered under the T43.6
retention rule. Motion changes alter no settled pixels; the closing
verify run is recorded in the T43.12 backlog note.
