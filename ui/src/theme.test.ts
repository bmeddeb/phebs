import { describe, expect, it } from 'vitest'
import { MOTION, TOKENS, type Mode, type PhebsTokens } from './theme'

// WCAG relative luminance / contrast ratio.
function luminance(hex: string): number {
  const c = hex.replace('#', '')
  const [r, g, b] = [0, 2, 4].map((i) => {
    const v = parseInt(c.slice(i, i + 2), 16) / 255
    return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

const surfaces = (tok: PhebsTokens) => ({
  pageBg: tok.pageBg,
  bandBg: tok.bandBg,
  fill: tok.fill,
  hoverFill: tok.hoverFill,
})

describe('status text tones are WCAG AA on every surface', () => {
  for (const mode of ['light', 'dark'] as Mode[]) {
    const tok = TOKENS[mode]
    for (const [state, tone] of Object.entries(tok.status)) {
      for (const [surface, bg] of Object.entries(surfaces(tok))) {
        it(`${mode} ${state}.text on ${surface}`, () => {
          expect(contrast(tone.text, bg)).toBeGreaterThanOrEqual(4.5)
        })
      }
    }
  }
})

describe('diff surfaces carry AA status text (CommitPage layers text on tinted lines)', () => {
  for (const mode of ['light', 'dark'] as Mode[]) {
    const tok = TOKENS[mode]
    it(`${mode} current.text on addedLineBg`, () => {
      expect(contrast(tok.status.current.text, tok.addedLineBg)).toBeGreaterThanOrEqual(4.5)
    })
    it(`${mode} conflict.text on deletedLineBg`, () => {
      expect(contrast(tok.status.conflict.text, tok.deletedLineBg)).toBeGreaterThanOrEqual(4.5)
    })
  }
})

describe('motion tokens stay inside the charter bounds', () => {
  it('element transitions are 120–200ms', () => {
    const ms = parseInt(MOTION.element, 10)
    expect(ms).toBeGreaterThanOrEqual(120)
    expect(ms).toBeLessThanOrEqual(200)
  })
  it('panels are at most 300ms', () => {
    expect(parseInt(MOTION.panel, 10)).toBeLessThanOrEqual(300)
  })
})
