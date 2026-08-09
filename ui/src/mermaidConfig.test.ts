import { describe, expect, it } from 'vitest'
import { mermaidInitConfig } from './mermaidConfig'
import { TOKENS } from './theme'

// T44.4: the security and layout posture, pinned as data — strict mode,
// no HTML labels, ELK layout, no auto-start — without loading mermaid.
describe('mermaid initialization contract', () => {
  for (const mode of ['light', 'dark'] as const) {
    const config = mermaidInitConfig(mode, TOKENS[mode])
    it(`${mode}: strict security, no HTML labels, ELK, explicit start`, () => {
      expect(config.securityLevel).toBe('strict')
      expect(config.startOnLoad).toBe(false)
      expect(config.layout).toBe('elk')
      expect(config.htmlLabels).toBe(false)
      expect(config.flowchart.htmlLabels).toBe(false)
    })
    it(`${mode}: themed from the design tokens`, () => {
      expect(config.themeVariables.textColor).toBe(TOKENS[mode].textPrimary)
      expect(config.themeVariables.lineColor).toBe(TOKENS[mode].textSecondary)
      expect(config.darkMode).toBe(mode === 'dark')
    })
  }
})

import { hasRendererDirective, svgViolatesPolicy } from './mermaidConfig'

// T44.4f: the load-bearing security predicates. Mermaid cannot render in
// jsdom (it needs real browser layout APIs), so the renderer wrapper
// enforces these two checks around it — before render (refuse config
// directives) and after (refuse policy-violating output). Tested directly
// on the exact override payloads and dangerous SVG shapes.
describe('mermaid renderer-directive refusal (T44.4f)', () => {
  it('refuses an %%{init}%% directive that flips htmlLabels/layout', () => {
    expect(hasRendererDirective("%%{init: {'flowchart': {'htmlLabels': true}} }%%\ngraph TD\nA-->B")).toBe(true)
    expect(hasRendererDirective("%%{ init: {'layout':'dagre'} }%%\ngraph TD\nA-->B")).toBe(true)
  })
  it('refuses config-bearing YAML frontmatter', () => {
    expect(hasRendererDirective('---\nconfig:\n  layout: dagre\n---\ngraph TD\nA-->B')).toBe(true)
    expect(hasRendererDirective('---\ntitle: fine\nconfig:\n  htmlLabels: true\n---\ngraph TD')).toBe(true)
  })
  it('allows a plain diagram and benign title-only frontmatter', () => {
    expect(hasRendererDirective('graph TD\nA-->B')).toBe(false)
    expect(hasRendererDirective('---\ntitle: My Flow\n---\ngraph TD\nA-->B')).toBe(false)
  })
})

describe('mermaid output-policy enforcement (T44.4f)', () => {
  it('rejects foreignObject (HTML labels), scripts, and event handlers', () => {
    expect(svgViolatesPolicy('<svg><foreignObject><b>x</b></foreignObject></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><script>alert(1)</script></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><g onclick="x()"></g></svg>')).toBe(true)
  })
  it('accepts SVG-text-only output', () => {
    expect(svgViolatesPolicy('<svg><g><text>orders-api</text></g></svg>')).toBe(false)
  })
})
