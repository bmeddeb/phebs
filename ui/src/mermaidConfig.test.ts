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

import {
  hasExternalResourceReference,
  hasRendererDirective,
  svgViolatesPolicy,
} from './mermaidConfig'

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
    expect(hasRendererDirective('---\n"config":\n  themeCSS: dangerous\n---\ngraph TD')).toBe(true)
    expect(hasRendererDirective('---\n{config: {htmlLabels: true}}\n---\ngraph TD')).toBe(true)
    expect(hasRendererDirective('---\n"con\\u0066ig":\n  layout: dagre\n---\ngraph TD')).toBe(true)
  })
  it('allows a plain diagram and benign title-only frontmatter', () => {
    expect(hasRendererDirective('graph TD\nA-->B')).toBe(false)
    expect(hasRendererDirective('---\ntitle: My Flow\n---\ngraph TD\nA-->B')).toBe(false)
  })
})

describe('mermaid output-policy enforcement (T44.4f)', () => {
  it('rejects foreignObject, scripts, images, resource CSS, links, and event handlers', () => {
    expect(svgViolatesPolicy('<svg><foreignObject><b>x</b></foreignObject></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><script>alert(1)</script></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><image href="https://example.invalid/x.png"></image></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><path fill="url(https://example.invalid/x.svg)"></path></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><a href="https://example.invalid/"><text>x</text></a></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><a href="/local"><text>x</text></a></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><use xlink:href="//example.invalid/icon.svg#x"></use></svg>')).toBe(true)
    expect(svgViolatesPolicy('<svg><g onclick="x()"></g></svg>')).toBe(true)
  })
  it('accepts text and same-document marker paint servers', () => {
    expect(svgViolatesPolicy('<svg><g><text>orders-api</text></g></svg>')).toBe(false)
    expect(svgViolatesPolicy('<svg><path marker-end="url(#flowchart-pointEnd)"></path></svg>')).toBe(false)
    expect(svgViolatesPolicy('<svg><path fill="url(\'#local-gradient\')"></path></svg>')).toBe(false)
    expect(svgViolatesPolicy('<svg><use xlink:href="#local-icon"></use></svg>')).toBe(false)
  })
})

describe('mermaid external-resource refusal (T44.3/T44.4 closure)', () => {
  it('refuses image-shape, URL, CSS-resource, and import forms before render', () => {
    expect(hasExternalResourceReference('flowchart TD\nA@{ img: "./local.png" }')).toBe(true)
    expect(hasExternalResourceReference("flowchart TD\nA@{ 'img': 'https://example.invalid/x.png' }")).toBe(true)
    expect(hasExternalResourceReference('flowchart TD\nclassDef x fill:url(/texture.svg)')).toBe(true)
    expect(hasExternalResourceReference('flowchart TD\nA[https://example.invalid/]')).toBe(true)
    expect(hasExternalResourceReference('flowchart TD\n@import "theme.css"')).toBe(true)
    expect(hasExternalResourceReference('sequenceDiagram\nparticipant A@{ "icon": "//example.invalid/x.png" }')).toBe(true)
    expect(hasExternalResourceReference('sequenceDiagram\nparticipant A@{ icon: "/local.png" }')).toBe(true)
    expect(hasExternalResourceReference('flowchart TD\nA[![alt](./local.png)]')).toBe(true)
    expect(hasExternalResourceReference('C4Context\nPerson(a, "A", "", $sprite="//example.invalid/x.png")')).toBe(true)
  })

  it('refuses decoded property keys, positional sprites, KaTeX, HTML, and authored CSS', () => {
    expect(hasExternalResourceReference(String.raw`flowchart TD
A@{ "\u0069mg": "\u002f\u002fevil/x.png" }`)).toBe(true)
    expect(hasExternalResourceReference(String.raw`sequenceDiagram
participant A@{ "\u0069con": "\u002f\u002fevil/x.png" }`)).toBe(true)
    expect(hasExternalResourceReference('C4Context\nPerson(a, "A", "desc", "./sprite.png")')).toBe(true)
    expect(hasExternalResourceReference(String.raw`sequenceDiagram
A->>B: $$\includegraphics{./pixel.png}$$`)).toBe(true)
    expect(hasExternalResourceReference('flowchart TD\nA["&lt;img src=./pixel.png&gt;"]')).toBe(true)
    expect(hasExternalResourceReference('flowchart TD\nA-->B; classDef x background-image:image-set("./pixel.png")')).toBe(true)
    expect(hasExternalResourceReference(String.raw`flowchart TD
A-->B
classDef ex fill:\75rl\28//example.invalid/pixel.svg\29
class A ex`)).toBe(true)
  })

  it('allows resource-free diagram labels and edges', () => {
    expect(hasExternalResourceReference('flowchart TD\nA[image unavailable]-->B')).toBe(false)
    expect(hasExternalResourceReference('flowchart TD\nA[price $5 & quality]-->B')).toBe(false)
    expect(hasExternalResourceReference('sequenceDiagram\nA->>B: ordinary message')).toBe(false)
  })
})
