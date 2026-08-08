import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// T43.12 charter §3 motion contract, pinned at the source level so it holds
// for every future surface without per-component style assertions:
//   1. every duration/easing is a MOTION token — no literal millisecond
//      values outside reduced-motion '0ms' overrides;
//   2. every file that animates carries a reduced-motion path.
// jsdom cannot observe media-query branches, so the source is the truth
// being verified — the same pattern as the caveat byte-identity test.

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

describe('charter §3 motion contract', () => {
  it('every animating file carries a reduced-motion path', () => {
    const animating = files.filter(({ path, text }) =>
      !path.endsWith('theme.ts') && /animationDuration|transitionDuration/.test(text))
    expect(animating.length).toBeGreaterThan(0)
    for (const { path, text } of animating) {
      expect(
        /REDUCED_MOTION|prefers-reduced-motion/.test(text),
        `${path} animates without a reduced-motion path`,
      ).toBe(true)
    }
  })

  it('durations are tokens — no literal milliseconds beyond reduced-motion 0ms', () => {
    for (const { path, text } of files) {
      if (path.endsWith('theme.ts')) continue
      const literals = [...text.matchAll(/['"](\d+)ms['"]/g)].filter((match) => match[1] !== '0')
      expect(literals.length, `${path} carries literal durations: ${literals.map((m) => m[0]).join(', ')}`).toBe(0)
    }
  })

  it('animation declarations use the token easings', () => {
    for (const { path, text } of files) {
      if (path.endsWith('theme.ts')) continue
      if (!/animationTimingFunction/.test(text)) continue
      const nonToken = [...text.matchAll(/animationTimingFunction:\s*([^,\n]+)/g)]
        .filter((match) => !match[1].trim().startsWith('MOTION.'))
      expect(nonToken.length, `${path} uses a non-token easing: ${nonToken.map((m) => m[0]).join('; ')}`).toBe(0)
    }
  })
})
