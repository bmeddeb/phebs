import { describe, expect, it } from 'vitest'
import { MOTION, TOKENS, TYPE, type Mode, type PhebsTokens } from './theme'
import { PALETTES, PALETTE_NAMES, type RoleName } from './palette'

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

describe('evidence metadata retains normal-text contrast at its 10px exception', () => {
  it('is the single named size below the 11px caption floor', () => {
    expect(TYPE.caption.fontSize).toBe('11px')
    expect(TYPE.evidenceMetadata.fontSize).toBe('10px')
  })

  for (const mode of ['light', 'dark'] as Mode[]) {
    const tok = TOKENS[mode]
    for (const [surface, bg] of Object.entries(surfaces(tok))) {
      it(`${mode} textTertiary on ${surface}`, () => {
        expect(contrast(tok.textTertiary, bg)).toBeGreaterThanOrEqual(4.5)
      })
    }
  }
})

// T44.2: every curated syntax palette holds the AA floor on every surface
// syntax text can render on — the page and citation-band backgrounds,
// search-row hover, anchor-line tint, and search-match highlight — in both
// modes; high-contrast additionally holds ≥7:1 against the page. This gate
// found the original default's sub-AA comment grays; T44.2f added matchBg
// after comments/operators failed the amber/brown match background it had
// missed, and the closure audit added the real hover/citation surfaces.
describe('syntax palettes are WCAG AA on every code surface', () => {
  const ROLES: RoleName[] = ['keyword', 'func', 'type', 'string', 'comment', 'number', 'operator']
  const SURFACES: (keyof PhebsTokens)[] = ['pageBg', 'bandBg', 'hoverFill', 'selectedLineBg', 'matchBg']
  for (const name of PALETTE_NAMES) {
    for (const mode of ['light', 'dark'] as Mode[]) {
      const tok = TOKENS[mode]
      const roleStyles = PALETTES[name][mode]
      for (const role of ROLES) {
        for (const surface of SURFACES) {
          it(`${name} ${mode} ${role} on ${surface}`, () => {
            // The ≥7:1 high-contrast promise is against the page only; the
            // tinted surfaces hold the shared 4.5 floor.
            const floor = name === 'high-contrast' && surface === 'pageBg' ? 7 : 4.5
            expect(contrast(roleStyles[role].color, tok[surface] as string)).toBeGreaterThanOrEqual(floor)
          })
        }
      }
      it(`${name} ${mode} defines all seven roles`, () => {
        expect(Object.keys(roleStyles).sort()).toEqual([...ROLES].sort())
      })
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
