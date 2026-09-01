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
// directive block (%%{ init ... }%%, or any %%{...}%%) or YAML frontmatter
// other than one conservative plain `title:` line is refused outright and
// shown as source. This intentionally avoids approximating Mermaid/js-yaml
// semantics: quoted, escaped, and flow-map config keys all fail closed.
export function hasRendererDirective(source: string): boolean {
  if (/%%\{/.test(source)) return true
  const frontmatter = source.match(/^\s*---\r?\n([\s\S]*?)\r?\n---/)
  if (frontmatter === null) return false
  const body = frontmatter[1].trim()
  return !/^title:\s*[a-z0-9][a-z0-9 ._:/()-]*$/i.test(body)
}

// Mermaid creates live DOM while rendering. Some grammars can allocate an
// Image, embed XHTML/KaTeX, or apply repository-authored CSS before the final
// SVG exists, so post-render inspection is too late to prevent a fetch.
// Refuse those grammar surfaces before Mermaid parses or renders:
//   - property objects (`@{...}`) carry flowchart images and sequence icons;
//   - C4's positional sprite argument cannot be distinguished lexically from
//     ordinary strings, so C4 fails closed as a family;
//   - raw/entity HTML, Markdown images, KaTeX, backslash escapes, and authored
//     style directives are pre-DOM resource paths.
// URL-looking labels also fail closed. The fence source remains visible.
export function hasExternalResourceReference(source: string): boolean {
  return /@\s*\{/i.test(source) ||
    /(?:^|\n)\s*C4(?:Context|Container|Component|Dynamic|Deployment)\b/i.test(source) ||
    /!\s*\[/i.test(source) ||
    /<\s*\/?\s*[a-z!]/i.test(source) ||
    /&(?:#[a-z0-9]+|[a-z][a-z0-9]+);/i.test(source) ||
    /\\/.test(source) ||
    /\$\$/.test(source) ||
    /(?:^|[;\n])\s*(?:classDef|style|linkStyle)\b/i.test(source) ||
    /\burl\s*\(/i.test(source) ||
    /\b(?:https?|data|blob|file):/i.test(source) ||
    /@import\b/i.test(source)
}

// Enforce the rendered-output contract regardless of how mermaid was
// configured: no HTML labels (foreignObject), no script, no inline event
// handlers. A violating SVG is refused and the source is shown instead.
export function svgViolatesPolicy(svg: string): boolean {
  // Mermaid uses same-document fragment paint servers for ordinary markers
  // (for example marker-end="url(#arrowhead)"). Those cannot fetch a
  // resource. Remove only that exact closed form, then refuse every remaining
  // url() so remote, protocol-relative, data, blob, and relative resources
  // still fail closed.
  const withoutFragmentPaintServers = svg.replace(
    /\burl\(\s*(["']?)#[a-z0-9_.:-]+\1\s*\)/gi,
    '',
  )
  const withoutFragmentLinks = withoutFragmentPaintServers.replace(
    /\b(?:xlink:)?href\s*=\s*(["'])#[a-z0-9_.:-]+\1/gi,
    '',
  )
  return /<(?:foreignObject|script|image|video|audio|iframe|object|embed|link)\b/i.test(svg) ||
    /\son[a-z]+\s*=/i.test(svg) || /\burl\s*\(/i.test(withoutFragmentLinks) ||
    /\b(?:xlink:)?href\s*=/i.test(withoutFragmentLinks)
}
