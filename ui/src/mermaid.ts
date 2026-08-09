import mermaid from 'mermaid'
import elkLayouts from '@mermaid-js/layout-elk'
import type { PhebsTokens } from './theme'
import { hasRendererDirective, mermaidInitConfig, svgViolatesPolicy } from './mermaidConfig'

// T44.4: the mermaid wrapper — one async chunk carrying mermaid 11 and the
// ELK layout engine, imported only when a rendered document actually
// contains a fence. The security posture lives in mermaidConfig.ts (strict
// mode, no HTML labels, no click bindings) and is pinned by tests there.

mermaid.registerLayoutLoaders(elkLayouts)

let initializedFor = ''

function ensureInitialized(mode: 'light' | 'dark', tok: PhebsTokens) {
  // Re-initialize when the theme mode changes; mermaid config is global.
  if (initializedFor === mode) return
  mermaid.initialize(mermaidInitConfig(mode, tok))
  initializedFor = mode
}

let renderSeq = 0

export async function renderMermaid(source: string, mode: 'light' | 'dark', tok: PhebsTokens): Promise<string> {
  // Refuse renderer-reconfiguring source before touching mermaid (T44.4f):
  // a directive/config-frontmatter fence cannot flip layout or htmlLabels.
  if (hasRendererDirective(source)) {
    throw new Error('diagram configuration directives are not permitted')
  }
  ensureInitialized(mode, tok)
  const id = `phebs-mermaid-${++renderSeq}`
  const { svg } = await mermaid.render(id, source)
  // Enforce the output contract regardless of mermaid's config path: no
  // HTML labels, no script, no event handlers reach the DOM.
  if (svgViolatesPolicy(svg)) {
    throw new Error('diagram produced disallowed output')
  }
  return svg
}
