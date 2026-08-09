import type { PhebsTokens } from './theme'
import { FONTS } from './theme'

// T44.4: the mermaid initialization contract, as data. Kept apart from the
// heavy mermaid wrapper so tests pin the security and layout posture
// without loading the renderer:
//   - securityLevel 'strict': labels are escaped, click/callback bindings
//     are refused, scripts never execute — this is the documented mermaid
//     trust boundary for untrusted diagram source (recorded in PLAN.md);
//   - htmlLabels false everywhere: label text renders as SVG text, never
//     foreignObject HTML;
//   - layout 'elk' is the product default (operator decision, Epic 44
//     plan); mermaid falls back per-diagram when a type does not support
//     an alternate layout;
//   - startOnLoad false: rendering happens only through our explicit call.
export function mermaidInitConfig(mode: 'light' | 'dark', tok: PhebsTokens) {
  return {
    startOnLoad: false as const,
    securityLevel: 'strict' as const,
    layout: 'elk',
    theme: 'base' as const,
    flowchart: { htmlLabels: false },
    // classDiagram/state/er inherit htmlLabels from the top-level flag set
    // below where applicable.
    htmlLabels: false,
    fontFamily: FONTS.SANS,
    darkMode: mode === 'dark',
    themeVariables: {
      background: 'transparent',
      primaryColor: tok.fill,
      primaryTextColor: tok.textPrimary,
      primaryBorderColor: tok.cardBorder,
      secondaryColor: tok.bandBg,
      tertiaryColor: tok.pageBg,
      lineColor: tok.textSecondary,
      textColor: tok.textPrimary,
      fontFamily: FONTS.SANS,
      fontSize: '13px',
    },
  }
}
