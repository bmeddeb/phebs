import { readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { DENSITIES, ROUTES, THEMES } from '../receipts/routes'

// T43.12f: the retained-receipt cardinality is derived from the executable
// manifest, never hand-counted — the closure record cites this assertion.
// Authenticated routes capture in every theme × density; the sign-in page
// precedes any user preference, so it captures per theme only.
describe('receipt manifest cardinality', () => {
  it('baselines equal routes × themes × densities plus the themed sign-in pair', () => {
    const baselines = readdirSync(resolve(process.cwd(), 'receipts', 'baselines'))
      .filter((name) => name.endsWith('.png'))
    expect(baselines.length).toBe(ROUTES.length * THEMES.length * DENSITIES.length + THEMES.length)
  })
})
