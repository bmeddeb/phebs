# Assessment B — detector/browser evidence

Scope: `ui/src/pages/SettingsPage.tsx`, Appearance / Phebs Settings / Operate. No Assessment A findings were read. Context was not rerun. Source, Git, builds, tests, installs and baselines were not modified by B.

## Deterministic scan

Exactly one invocation after root confirmed Assessment A complete:
`/Users/ben/.agents/skills/impeccable/scripts/impeccable detect --json ui/src/pages/SettingsPage.tsx`

Exit 0; stdout JSON schema is an empty top-level array `[]`; stderr empty; finding count 0. Thus there are no rule names, file/line locations, or detector false positives to enumerate. Preserve `detect.json`, `detect.stderr`, `detect.exit`; parent should reuse this result without repeating the detector. Clean deterministic output is not a claim of complete usability or accessibility coverage.

## Browser evidence

Browser skill/plugin was unavailable, so used the authorized cached Playwright fallback with installed Darwin Chrome 155, headless only, against the already-produced production bundle and a presentation-only signed-in-reader fixture. Three new isolated contexts and fresh pages, closed after capture:

- Light, 1280×800, ready highlighting.
- Dark, 1280×800, unavailable highlighting.
- Light, 390×900, unavailable highlighting.

The two unavailable variants deliberately returned one 503 each for the production highlight chunk. Those two expected resource errors were the only console errors. There were no page errors or unexpected API requests.

Every page passed mutable preflight: title changed to `[Human] Impeccable · Appearance` and an appended inline script executed. On the first page, this proof preceded live-server startup. Every page then loaded the owned local live server's `/detect.js`, waited 2.6 seconds, and emitted the exact detector console message `[impeccable] No anti-patterns found.` Injection script sources and messages are preserved in `browser-result.json`.

No reliable user-visible overlay is claimed: the browser was headless, all pages were closed, and with zero detected anti-patterns there were no overlay elements. `[Human]` was only the title label; it does not establish visibility. Headless screenshots were visually inspected in B and retained.

Observed scoped behavior/layout: the normal specimen is visibly highlighted; the two failure states retain the whole four-line plain specimen, show `Preview unavailable.` beside `Reload page`, and fit their viewports. Document widths exactly match 1280/390. The recovery button measured 44px high in both failure views (SettingsPage.tsx:315–319). The existing palette select measured 30px high in all views (SettingsPage.tsx:252); this is an existing touch-target polish concern, not a new recovery defect. The four palette options stay within the method's four-choice threshold. No new blocking issue was exposed by B.

## Process custody and limitations

Vite, Chrome, browser client and Impeccable live server were owned process groups. To keep an explicit waitable server child, the engine ran in foreground mode inside the harness background rather than using its detached `--background` mode. It served the identical local `detect.js` endpoint. The wrapper recorded 13.208s, including its first cleanup attempt, within its 90s envelope.

Vite 30975, client 31019 and live-server 31036 joined with clean group teardown. Chrome root 30995 had exited/reaped with code 0, but its first group-drain attempt raised `[Errno 1] Operation not permitted`. The original wrapper status remains `stopped`, with that refusal retained verbatim in `owned-result.json`. This is not silently relabeled a clean initial pass.

A bounded read-only follow-up probed only the four recorded owned groups using `killpg(pgid, 0)`; all were absent. No unknown/permission-denied PID was killed, and neither browser nor detector was rerun. `cleanup_followup` preserves this evidence and the initial Chrome root exit 0. The private Chrome profile was then removed. Final custody has no remaining owned process/group; the initial cleanup-refusal diagnostic remains a limitation of the first wrapper outcome.

Evidence and harness files outside the checkout are intentionally retained. This B pass does not claim native-browser visibility, Noble screenshot equivalence, backend authentication/authority coverage, or broad keyboard/accessibility qualification beyond the observed scoped evidence.
