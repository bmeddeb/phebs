# Handoff: phebs — Context Port brand + Refined rail UI

## Overview
Two coordinated changes to the phebs web UI (`ui/` in the phebs repo — React 19 + TypeScript + Vite + Base Web/styletron, hash routing):

1. **Brand**: a new mark, "Context Port" — a rounded-square port with a gap on the right edge and a blue context dot inside, plus a dashed approach line. Applied to the header lockup, login, favicon, and README banner. Includes a hover micro-interaction and a splash/loading animation.
2. **UI density pass, "Refined rail"**: same information architecture as today, tighter chrome (52px header), carded surfaces with 36–38px band headers, one card per repo on search, 26–40px rows, 18px-line-height code, and a specified streaming-results motion sequence.

## About the Design Files
The `.dc.html` files in this bundle are **design references created in HTML** — prototypes showing intended look and behavior, not production code to copy. The task is to **recreate them inside the existing `ui/` codebase** using its established patterns: styletron `css()` objects, `usePhebsTokens()` for colors, Base Web `Input`/`Button`/`Notification`/`Spinner`, the inline 16×16 stroke-icon set in `src/icons.tsx`, and the existing hash router. Open the `.dc.html` files in a browser to inspect; `support.js`, `animations.jsx`, and `mark-scene.jsx` exist only to make these references render — do not port them.

`Current UI - *.dc.html` are pixel-faithful recreations of the app **as it exists today** (light mode) with only the new logo applied — use them as the baseline. `Enhanced UI.dc.html` is the **target**: Turn 2 (top, ids 2a/2b/2c) covers File/Repos/Login; option 1a below covers Search. Every target screen is shown in light AND dark, side by side.

## Fidelity
**High-fidelity.** All colors are the app's existing `PhebsTokens` / Base Web values; spacing and type sizes in this README are exact. Recreate pixel-perfectly.

## Design Tokens
All colors already exist in `src/theme.ts` (`PhebsTokens`) except one:

- **NEW `bandBg`**: card band-header background — light `#FAFAFA`, dark `#161616`. Add to `PhebsTokens` (dark value equals existing `hoverFill`; light is new).

Everything else maps to existing tokens — implement with tokens, never hex literals:
`pageBg` #FFFFFF/#000000 · `fill` #F3F3F3/#292929 · `hoverFill` #F6F6F6/#161616 · `textPrimary` #000/#FFF · `textSecondary` #4B4B4B/#C4C4C4 · `textTertiary` #5E5E5E/#ABABAB · `gutter` #A6A6A6/#5D5D5D · `cardBorder` #E8E8E8/#383838 · `innerSep` #F3F3F3/#292929 · `kbdBorder` #DDDDDD/#383838 · `accent` #276EF1/#5E8BDB · `selectedLineBg` #EFF4FE/#182946 · `selectedText` #175BCC/#93B4EE · `matchBg` #FBE5B6/#4C3111 · status colors unchanged. Syntax palette unchanged (`src/highlight.ts`).

Spacing scale in this pass: 4 / 6 / 8 / 10 / 12 / 16 / 20 / 24. Radii: 5 (tiny chips), 6–7 (buttons/chips), 8 (cards/inputs), 999 (pills). Fonts unchanged (`FONTS.MONO`, `FONTS.SANS`).

## The Mark (SVG geometry, 24×24 viewBox)
```
port:     M21 8.5 V7.5 A4.5 4.5 0 0 0 16.5 3 H7.5 A4.5 4.5 0 0 0 3 7.5
          V16.5 A4.5 4.5 0 0 0 7.5 21 H16.5 A4.5 4.5 0 0 0 21 16.5 V15.5
          stroke: textPrimary, width 1.8, linecap round   (right edge open y8.5–15.5)
approach: M23.5 12 H18.5  stroke: gutter, width 0.9, dasharray 1.8 1.6
dot:      circle cx15.5 cy12 r1.9  fill: accent
```
- Add a `PhebsMark({ size })` component to `src/icons.tsx` using `currentColor` for the port and token colors for dot/approach.
- **≤20px sizes (favicon)**: drop the approach line, bump port stroke to 2.4 and dot to r2.6 (`assets/favicon.svg` does this and auto-switches light/dark via `prefers-color-scheme`).
- Lockup: mark + wordmark, gap ≈ 0.4× mark size; wordmark stays system-ui 700 (header 20px→17px in Refined rail; login 24px; the mark carries the brand).
- Wiring: `assets/favicon.svg` → `ui/public/favicon.svg` + `<link rel="icon" type="image/svg+xml" href="/favicon.svg">` in `ui/index.html`; `phebs-banner.svg` → `docs/` + README hero; `phebs-mark[-white].svg` for docs.

## Screens / Views

### Header shell (all pages) — `src/App.tsx` `Header`
- Height 56→**52px**; padding 0 20px; gap 20px.
- Lockup: `PhebsMark` 18px + "phebs" 17px/700, gap 8px, one `<a href="#/">`.
- NavLink: 13px (was 14); gap 18px; active = weight 500 + `inset 0 -2px 0 textPrimary`; inactive `textTertiary`, hover `textPrimary`.
- Right: email 12px `textTertiary`; icon buttons **28×28** (was 32), radius 7, 1px `cardBorder`, icons 14px.

### Search — `src/pages/SearchPage.tsx` (reference: Enhanced UI option 1a)
- Main padding: 20px sides, 20px top, 40px bottom.
- **Search row**: single 44px field (Base Input override height 44, radius 8, mono 14px) with the submit button embedded right-inside the field: 32px primary button radius 6 padding 0 14, plus a `/` kbd hint (mono 11, 1px kbdBorder, radius 4) beside it. While streaming the button swaps to secondary "Stop".
- **Operators row** (mt 8): mono 11px chips, padding 3px 7px, radius 5, `fill` bg, `textSecondary`; right-aligned on the same row: stats text 12px ("**11** matches · **4** files · 96ms · 2 repositories", counts `textPrimary` 600, tabular-nums), 1px×12px divider, j/k kbd hints, "Syntax ↗" link 12px.
- **Facet rail**: 200px (was 224). Section title 11px/600/uppercase/0.05em `textTertiary` mb 6. Rows 30px, radius 6, hover `hoverFill`; repo checkbox 13px radius 3 border `kbdBorder` (active: `accent` fill + white ✓); lang dot 8px; counts 11px tabular-nums.
- **Results — one card per repo** (replaces per-file cards): card = 1px `cardBorder`, radius 8, gap 14 between cards.
  - Repo band (a `<button>`, toggles fold): 38px, `bandBg`, bottom 1px `innerSep`; chevron 13; name 14px/600; "7 matches · 2 files" 12px `textTertiary`; right "indexed 2 h ago" 11px `gutter`.
  - File row: 32px, padding 0 12px; lang square 8px radius 2; path 12.5px (dir `textTertiary`, name `textPrimary` 500); right: match count 11px, copy + open icon buttons 13px (`gutter`, hover `textPrimary` + `hoverFill`).
  - Code lines: mono **12.5px / 18px line-height**; gutter link 40px right-aligned `gutter` (hover `accent`); match highlight `matchBg` radius 2; row hover `hoverFill`; selected line `selectedLineBg` + `inset 2px 0 0 accent`. Files inside a card separated by 1px `innerSep`.

### File view — `src/pages/FilePage.tsx` (reference: Enhanced UI 2a)
- **Single 32px toolbar row** replaces the old breadcrumb block (mb 12): breadcrumb 13px (repo link `textTertiary` hover underline, `/` `gutter`, dir `textTertiary`, filename `textPrimary` 600, copy icon 12); right: commit pill (mono 11, 1px `cardBorder`, radius 999, padding 2 9, commit icon 12, "main · a3f82c1"), then 28px chip buttons radius 7 padding 0 10, 12.5px: Permalink / Search (icon) / Blame / History.
- **Tree**: 220px card (1px `cardBorder`, radius 8, padding 4px 0). Rows **26px**, 12.5px; indent 8/18/28px per depth; chevrons 12px `gutter`; file dots 7px; active row: `fill` bg + `inset 2px 0 0 textPrimary` + 500.
- **Code card**: band header 36px `bandBg` (lang dot, filename 12.5/600, meta 11.5 `textTertiary` "Go · 302 lines · 9.4 KB · indexed 2 h ago", right L-pill mono 11 `selectedText` on `selectedLineBg` radius 5, copy 13). CodeMirror theme: 12.5px, line-height 18px, gutter min-width 40px `gutter`; deep-linked line `selectedLineBg` + `inset 2px 0 0 accent`.
- **Code-nav panel**: 300→**280px**, now a card matching the tree (band header 36px: "Code navigation" 12.5/600 + "46:6" mono 11). Body padding 12: section titles 10.5px/600/uppercase `textTertiary`; hover name 12.5/600; kind 11 `textTertiary`; signature `<pre>` mono 11 on `fill` radius 6 padding 8; location links mono 11 `accent`, 3px vertical padding.

### Repos — `src/pages/ReposPage.tsx` (reference: Enhanced UI 2b)
- Whole table moves into a card. Band 38px `bandBg`: "Repositories" 14/600 + "4 · 2 indexed · 1 indexing" 12 `textTertiary` + right "Reindex all" 26px chip.
- Header cells: 11.5px/500 `textTertiary`, padding 8px 12px. Rows **40px**, single-line, cells padding 0 12px, 13px; row hover `hoverFill`; separators `innerSep`.
- Last-indexed: relative time only, full timestamp in `title`. Commit chip: mono 11.5, `hoverFill` bg, 1px `cardBorder`, radius 5, padding 2 7 (keeps copy behavior). Failed: red dot + "Failed" + truncated mono 11 error inline (full error in `title`). Search icon button 28×28; Reindex/Retry 26px chips; running = disabled 0.6 opacity.

### Login — `src/pages/LoginPage.tsx` (reference: Enhanced UI 2c)
- Lockup: mark 24px + "phebs" 24/700, gap 9, mb 24. "Sign in" 18/600 mb 16.
- Fields 44px (Base Input radius 8), grid gap 12. Primary 44px. Between local login and SSO: hairline row — 1px `innerSep` lines + "or" 11px `gutter`. SSO = secondary 44px.

## Interactions & Behavior

### Logo hover micro-interaction (reference: `Header Hover Micro.dc.html`)
On lockup `mouseenter`, two Web-Animations calls on SVG refs (no CSS classes, no re-render). Both elements need `transform-box: fill-box; transform-origin: center`:
- dot: `[{transform:'translateX(11px)',opacity:0},{transform:'translateX(-0.8px)',opacity:1,offset:0.72},{transform:'translateX(0)',opacity:1}]`, 550ms, `cubic-bezier(0.22,1,0.36,1)` (px = SVG user units; dot slides in through the gap, clipped by the svg viewport).
- pulse ring (r7 circle at 15.5,12, stroke `accent` 0.6, static opacity 0): `[{transform:'scale(0.25)',opacity:0.45},{transform:'scale(1)',opacity:0}]`, 450ms, delay 400ms, ease-out, `fill:'backwards'`.
- Guard with `matchMedia('(prefers-reduced-motion: reduce)')`.

### Streaming search sequence (reference: `Streaming Search.dc.html`, maps to existing `streamSearch` callbacks)
- On submit: 2px sweep bar under the search row (`linear-gradient(90deg, transparent, accent 50%, transparent)`, background-size 50% 100%, keyframes background-position −60%→160%, 1.2s linear infinite); 8px pulsing `statusBlue` dot (opacity 1→0.35→1, 1.4s) before the stats; suffix "· searching…"; skeleton card below arrived results (band + 3 shimmer lines: gradient `fill`→`cardBorder`→`fill`, background-size 200%, 1.4s).
- **Each arriving batch** (`onBatch`): its new repo card / file section enters with `opacity 0→1, translateY(7px)→0`, 320ms `cubic-bezier(0.22,1,0.36,1)`. Animate only newly appended nodes (keyed by fileKey; CSS animation on mount). Match/file counters and facet counts tick with each batch (tabular-nums so nothing shifts).
- On done: bar + pulse + skeleton unmount; duration ("96ms") and repo count land; Stop button swaps back to Search. Skip enter animations under reduced-motion.

### Splash / loading animation (reference: `Mark Animation.dc.html`, optional)
4.6s loop: port stroke draws on 0–0.9s (pathLength 100 dashoffset 100→0, easeInOutCubic) → approach line fades 0.75–1.05 → dot flies in x 26.5→15.5 over 1.1–2.0s (ease-out-back) → pop r×1.25 at 1.95–2.35 → pulse ring r2.2→7.5 fading 2.15–2.9 → wordmark fades up 2.6–3.15. Suitable for the auth-loading screen or docs.

## State Management
No new data requirements. Search already streams via `streamSearch`; the motion attaches to existing `phase`/`files` state. New UI state: per-repo fold (exists), enter-animation bookkeeping (render-only). Add `bandBg` to both `LIGHT`/`DARK` in `theme.ts`.

## Assets
- `assets/favicon.svg` — auto light/dark favicon (port stroke 2.4, dot r2.6, no approach line)
- `assets/phebs-mark.svg` / `assets/phebs-mark-white.svg` — full mark, light/dark
- `assets/phebs-banner.svg` — 960×240 README banner (white card, hairline border, lockup + tagline)
- All original: no third-party assets. Icons remain `src/icons.tsx` (16×16, stroke 1.5, round caps).

## Files
- `Enhanced UI.dc.html` — **target designs**: Turn 2 = 2a File, 2b Repos, 2c Login; Turn 1 option 1a = Search (option 1b is a rejected alternative, ignore). Each light + dark.
- `Current UI - Search/File/Repos/Login.dc.html` — baseline (today's app + new logo), light.
- `Brand - Context Port.dc.html` — mark geometry, lockups, favicon tab mocks, banner, file map.
- `Header Hover Micro.dc.html` — hover interaction, live in both themes.
- `Streaming Search.dc.html` — streaming sequence, live loop (speed/loop tweakable).
- `Mark Animation.dc.html` + `animations.jsx` + `mark-scene.jsx` — splash animation reference.
- `notes/tokens.md` — token/Base-Web value crib sheet.
- `support.js` — runtime for opening the `.dc.html` files; not part of the design.
