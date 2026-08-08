import { describe, expect, it } from 'vitest'
import { DEFAULT_PALETTE, PALETTE_NAMES, isPaletteName } from './palette'

describe('palette persisted-value guard (T44.2f)', () => {
  it('accepts every curated palette name', () => {
    for (const name of PALETTE_NAMES) expect(isPaletteName(name)).toBe(true)
  })

  it('rejects null, unknown, and empty', () => {
    expect(isPaletteName(null)).toBe(false)
    expect(isPaletteName('')).toBe(false)
    expect(isPaletteName('midnight')).toBe(false)
  })

  it('rejects inherited object-property names (prototype pollution)', () => {
    // `in PALETTES` would accept all of these and then index to undefined,
    // breaking highlighting — the own-property guard must reject them.
    for (const inherited of ['constructor', 'toString', 'hasOwnProperty', '__proto__', 'valueOf']) {
      expect(isPaletteName(inherited), inherited).toBe(false)
    }
  })

  it('the default palette is itself valid', () => {
    expect(isPaletteName(DEFAULT_PALETTE)).toBe(true)
  })
})
