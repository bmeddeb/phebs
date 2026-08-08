import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// T43.12f charter §3 motion contract. Motion is a closed system: the theme's
// helpers (animated / transitioned / panelTransition) are the only place
// motion properties may be written. The helpers admit compositor properties
// only (opacity/transform, typed), token durations and easings only
// (literal-union types — the compiler rejects strays), and embed the
// reduced-motion path by construction. What remains for this test is the
// closure itself, verified at the source level because jsdom cannot observe
// media queries or the compositor:
//   1. WAAPI is banned — no imperative animations can bypass the system;
//   2. raw motion properties exist only inside theme.ts;
//   3. no literal millisecond values outside theme.ts (tokens and the
//      reduced '0ms' live there);
//   4. every helper carries its reduced-motion override.

const SRC = resolve(process.cwd(), 'src')

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) out.push(...sourceFiles(path))
    else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(path)
  }
  return out
}

const files = sourceFiles(SRC).map((path) => ({ path, text: readFileSync(path, 'utf8') }))
const theme = files.find(({ path }) => path.endsWith('theme.ts'))
const outsideTheme = files.filter(({ path }) => !path.endsWith('theme.ts'))

describe('charter §3 motion contract', () => {
  it('WAAPI is banned — all motion is CSS through the theme helpers', () => {
    for (const { path, text } of files) {
      expect(/\.animate\(|getAnimations/.test(text), `${path} uses the Web Animations API`).toBe(false)
    }
  })

  it('raw motion properties exist only in theme.ts', () => {
    const raw = /(?:animation(?:Name|Duration|TimingFunction|IterationCount|Delay)|transition(?:Property|Duration|TimingFunction|Delay)):/
    for (const { path, text } of outsideTheme) {
      const hits = [...text.matchAll(new RegExp(raw, 'g'))]
      expect(hits.length, `${path} writes motion properties directly: ${hits.map((m) => m[0]).join(', ')} — compose through the theme helpers`).toBe(0)
    }
  })

  it('no literal millisecond values outside theme.ts', () => {
    for (const { path, text } of outsideTheme) {
      const literals = [...text.matchAll(/['"](\d+)ms['"]/g)]
      expect(literals.length, `${path} carries literal durations: ${literals.map((m) => m[0]).join(', ')}`).toBe(0)
    }
  })

  it('every theme helper embeds the reduced-motion path', () => {
    expect(theme, 'theme.ts must exist').toBeTruthy()
    const text = theme!.text
    for (const helper of ['export function animated', 'export function transitioned', 'export function panelTransition']) {
      const start = text.indexOf(helper)
      expect(start, `${helper} missing from theme.ts`).toBeGreaterThan(-1)
      const body = text.slice(start, text.indexOf('\n}', start))
      expect(body.includes('[REDUCED_MOTION]'), `${helper} lacks its reduced-motion override`).toBe(true)
    }
  })

  it('the helpers are consumed — the system is closed, not empty', () => {
    const consumers = outsideTheme.filter(({ text }) => /animated\(|transitioned\(|panelTransition\(/.test(text))
    expect(consumers.length).toBeGreaterThanOrEqual(5)
  })
})
