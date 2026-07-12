# PHEBS design tokens (extracted from ui/src/theme.ts, highlight.ts, lang.ts + baseui defaults)

## App tokens (PhebsTokens)
token            | light    | dark
pageBg           | #FFFFFF  | #000000
fill             | #F3F3F3  | #292929
hoverFill        | #F6F6F6  | #161616
textPrimary      | #000000  | #FFFFFF
textSecondary    | #4B4B4B  | #C4C4C4
textTertiary     | #5E5E5E  | #ABABAB
gutter           | #A6A6A6  | #5D5D5D
cardBorder       | #E8E8E8  | #383838
innerSep         | #F3F3F3  | #292929
kbdBorder        | #DDDDDD  | #383838
accent           | #276EF1  | #5E8BDB
selectedLineBg   | #EFF4FE  | #182946
selectedText     | #175BCC  | #93B4EE
matchBg          | #FBE5B6  | #4C3111
plainCode        | #000000  | #DEDEDE
addedLineBg      | #ECF8F0  | #10291A
deletedLineBg    | #FFF0F2  | #35161B
statusGreen      | #0E8345  | #5C9D70
statusBlue       | #276EF1  | #5E8BDB
statusRed        | #DE1135  | #DE5B5D
statusAmber      | #FFC043  | #FFC043

## Syntax palette (highlight.ts)
role     | light            | dark
keyword  | #7C3EC3          | #BDA7E4
func     | #175BCC          | #93B4EE
type     | #016974          | #72C1CD
string   | #166C3B          | #8FC19C
comment  | #868686 italic   | #717171 italic
number   | #A95F03          | #DEA85E
operator | #5E5E5E          | #ABABAB

## Fonts
MONO: ui-monospace, "SF Mono", Menlo, Monaco, "Cascadia Code", monospace
SANS: system-ui, "Helvetica Neue", Helvetica, Arial, sans-serif

## Lang dot colors (lang.ts DOT)
go #016974 · ts/tsx #175BCC · js #DE9800 · py #0E8345 · rs #A33B04 · md/yaml/xml #5E5E5E · json #A33B04 · sh #0E8345 · css #175BCC · html #A33B04 · dockerfile #175BCC · proto #7C3EC3 · fallback #A6A6A6

## Base Web component defaults (from node_modules/baseui)
- Input (default size): root border 2px solid #F3F3F3, bg #F3F3F3, radius 8px, text 16/24, padding 10px 14px (h≈48). Focus: border #000, bg #FFF. Dark: border/bg #292929, focus border #FFF bg #000. Placeholder = contentTertiary (#5E5E5E / #ABABAB).
- Button primary (default): bg #000 text #FFF hover #333333 (light); dark bg #C4C4C4 text #000. font 16/20 w500, padding 14px 16px, radius 8px.
- Button secondary: bg #F3F3F3 text #000 hover #DDDDDD (light); dark bg #292929 text #FFF hover #484848.
- Tag (neutral, non-clickable): h24, padding 4px 6px, radius 4px, font 14/16 w500, margin 5px, bg #F3F3F3 text #5E5E5E (light); dark bg #292929 text #ABABAB. No border.
- Notification negative: bg #FFF0EE text #000 (light); dark bg #4A1216 text #FFF. radius 8, padding 16.
- Spinner: track #E8E8E8 (light) / #383838 (dark), arc #276EF1, small ≈16px, border 2px.
- HeadingSmall: 24px/32px w700.
- kbd (app): mono 11px, padding 1px 5px, 1px border kbdBorder, radius 4, color textSecondary.

## Header spec (App.tsx)
h56, border-bottom 1px cardBorder, padding 0 24, gap 24, sticky. Wordmark "phebs" 20px w700. NavLink 14px; active w500 + inset 0 -2px 0 textPrimary. Icon buttons 32×32, 1px cardBorder, radius 8, icons 16px stroke 1.5.
Main: padding 24px 24px 48px.

## Icons: 16×16 viewBox, stroke currentColor 1.5, round caps/joins (ui/src/icons.tsx)

## Sample/real content sources
- internal/search/searcher.go L41-70 (func Open, usageRepoCap) — used in recreations
- Repos: connections github/zoekt/local; statuses done/running/failed/never; orphaned pill #FFF0E9/#A33B04
- README story: Phoebe — first moon discovered by photography (1899, Pickering); "one moving signal in a massive static corpus"
