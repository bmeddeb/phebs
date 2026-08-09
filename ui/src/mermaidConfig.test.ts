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
