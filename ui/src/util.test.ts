import { describe, expect, it } from 'vitest'
import {
  ancestorFolders,
  boundedError,
  fileFilter,
  repoFilter,
  splitQueryTerms,
} from './util'

describe('boundedError', () => {
  it('stringifies plain values without changing their message', () => {
    expect(boundedError('plain failure')).toBe('plain failure')
    expect(boundedError(503)).toBe('503')
  })

  it('removes the prefix added by Error stringification', () => {
    expect(boundedError(new Error('request failed'))).toBe('request failed')
  })

  it('strips only a leading Error prefix and its following whitespace', () => {
    expect(boundedError('Error: \t\n request failed')).toBe('request failed')
    expect(boundedError('context: Error: request failed')).toBe(
      'context: Error: request failed',
    )
  })

  it('preserves 511 and 512 UTF-16 code units and caps 513 at 512', () => {
    const below = 'x'.repeat(511)
    const exact = 'x'.repeat(512)
    const oversized = 'x'.repeat(513)

    expect(boundedError(below)).toBe(below)
    expect(boundedError(exact)).toBe(exact)
    expect(boundedError(oversized)).toBe(`${'x'.repeat(511)}…`)
    expect(boundedError(oversized)).toHaveLength(512)
  })

  it('measures the cap in UTF-16 code units', () => {
    const exact = '😀'.repeat(256)
    expect(exact).toHaveLength(512)
    expect(boundedError(exact)).toBe(exact)
  })
})

describe('ancestorFolders', () => {
  it('returns every directory needed to reveal a nested file', () => {
    expect(ancestorFolders('src/components/search/Page.tsx')).toEqual([
      'src',
      'src/components',
      'src/components/search',
    ])
  })

  it('does not treat a root file as a folder', () => {
    expect(ancestorFolders('README.md')).toEqual([])
  })

  it('normalizes stray slash-only segments', () => {
    expect(ancestorFolders('/src//main.go')).toEqual(['src'])
  })
})

describe('repoFilter', () => {
  it('anchors and escapes the full repository name', () => {
    expect(repoFilter('git.example.com/org/a+b.repo')).toBe(
      'repo:"^git\\\\.example\\\\.com/org/a\\\\+b\\\\.repo$"',
    )
  })

  it('keeps same-basename repositories distinct', () => {
    expect(repoFilter('github.com/one/shared')).not.toBe(
      repoFilter('github.com/two/shared'),
    )
  })

  it('quotes whitespace, quotes, and backslashes as one query atom', () => {
    const filter = repoFilter('local/My Project/quote"and\\slash')
    expect(splitQueryTerms(`needle ${filter} lang:go`)).toEqual([
      'needle',
      filter,
      'lang:go',
    ])
  })
})

describe('fileFilter', () => {
  it('quotes and anchors a filename with whitespace', () => {
    expect(fileFilter('docs/My File.md')).toBe('file:"My File\\\\.md$"')
  })
})
