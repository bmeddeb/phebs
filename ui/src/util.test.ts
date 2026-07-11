import { describe, expect, it } from 'vitest'
import { ancestorFolders, fileFilter, repoFilter, splitQueryTerms } from './util'

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
