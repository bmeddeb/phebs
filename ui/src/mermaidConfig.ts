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
//   - startOnLoad false: rendering happens only through our explicit call;
//   - secure: our policy keys are added to mermaid's non-overridable list so
//     a diagram's own %%{init}%% directive cannot flip layout or htmlLabels
//     (T44.4f). This is the mermaid-native lock; hasRendererDirective and
//     svgViolatesPolicy below are the load-bearing guarantees around it.
export function mermaidInitConfig(mode: 'light' | 'dark', tok: PhebsTokens) {
  return {
    startOnLoad: false as const,
    securityLevel: 'strict' as const,
    secure: ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'maxEdges', 'layout', 'htmlLabels', 'flowchart', 'theme', 'themeVariables', 'fontFamily', 'darkMode'],
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

// T44.4f: the load-bearing security predicates, pure and testable (mermaid
// itself cannot render in jsdom, so the renderer wrapper enforces these two
// checks around it and the browser verifies the end-to-end path).

// Untrusted diagram source may not reconfigure the renderer. A mermaid
// directive block (%%{ init ... }%%, or any %%{...}%%) or a config-bearing
// YAML frontmatter block can override layout/htmlLabels even with the secure
// list set on some mermaid paths — so such a fence is refused outright and
// shown as source. Benign title-only frontmatter is allowed.
export function hasRendererDirective(source: string): boolean {
  if (/%%\{/.test(source)) return true
  const frontmatter = source.match(/^\s*---\r?\n([\s\S]*?)\r?\n---/)
  return frontmatter !== null && /(^|\n)\s*config\s*:/.test(frontmatter[1])
}

// Enforce the rendered-output contract regardless of how mermaid was
// configured: no HTML labels (foreignObject), no script, no inline event
// handlers. A violating SVG is refused and the source is shown instead.
export function svgViolatesPolicy(svg: string): boolean {
  return /<foreignObject/i.test(svg) || /<script/i.test(svg) || /\son[a-z]+\s*=/i.test(svg)
}
