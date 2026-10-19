import type { Page, Route } from '@playwright/test'
import { navigationFixture, navigationCommits, navigationRepositories } from './navigationFixture'

export const historyRepo = navigationRepositories[0]
export const historyRef = navigationCommits[0]
export const historyCommit = (id = historyRef) => ({
  id, short_id: id.slice(0, 7), parent_ids: [navigationCommits[1]],
  subject: `Neutral change ${id.slice(0, 7)}`, message: `Neutral change ${id.slice(0, 7)}`,
  author: { name: 'Reader', email: 'reader@localhost.test', time: '2026-10-19T11:00:00Z' },
  committer: { name: 'Reader', email: 'reader@localhost.test', time: '2026-10-19T11:00:00Z' },
})
export async function codeHistoryFixture(page: Page) {
  const base = await navigationFixture(page)
  const requests: { path: string; query: Record<string, string> }[] = []
  let failure = ''
  let invalid = ''
  let hold = ''
  let held: { route: Route; value: unknown } | undefined
  const endpoints = ['/api/commits', '/api/commit', '/api/diff', '/api/blame', '/api/find_definitions', '/api/find_references', '/api/hover']
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (!endpoints.includes(path)) { await route.fallback(); return }
    requests.push({ path, query: Object.fromEntries(url.searchParams) })
    if (failure === path) { await route.fulfill({ status: 503, body: 'neutral read outage' }); return }
    const revision = url.searchParams.get('ref') === 'main' || !url.searchParams.get('ref') ? historyRef : url.searchParams.get('ref')!
    let value: unknown
    if (path === '/api/commits') {
      const offset = Number(url.searchParams.get('offset'))
      value = { revision, offset, commits: offset ? [historyCommit('c'.repeat(40))] : [historyCommit(revision), historyCommit(navigationCommits[1])], has_more: offset === 0 }
    } else if (path === '/api/commit') value = { revision, commit: historyCommit(revision), changes: [{ status: 'modified', path: 'main.go', additions: 1, deletions: 1 }] }
    else if (path === '/api/diff') {
      const head = url.searchParams.get('head') === 'main' ? navigationCommits[1] : url.searchParams.get('head')!
      value = { head: invalid === 'diff' ? 'f'.repeat(40) : head, base: navigationCommits[1], files: [{ status: 'modified', path: 'main.go', additions: 1, deletions: 1 }], patch: `@@ -1 +1 @@\n-old\n+${'neutral '.repeat(35)}`, truncated: false }
    } else if (path === '/api/blame') {
      const sourcePath = url.searchParams.get('path')!
      value = { revision, path: sourcePath, commits: [historyCommit()], lines: [1, 2].map(line => ({ line, original_line: line, commit_id: historyRef, original_path: sourcePath, content: `package ${sourcePath === 'old.go' ? 'obsolete' : 'current'} ${line}` })), truncated: false }
    } else {
      const start = { line: 2, character: 0 }
      const range = { start, end: { line: 2, character: 4 } }
      const location = { repo: invalid === 'location' ? 'example.test/other' : historyRepo, revision, path: 'lib.go', range, encoding: 'utf16' }
      value = path === '/api/find_definitions' ? { available: true, location } : path === '/api/find_references' ? { available: true, locations: [location], truncated: false } : { available: true, hover: { symbol: 'neutral', display_name: `Symbol at ${url.searchParams.get('character')}`, range, encoding: 'utf16', documentation: ['Neutral local SCIP fixture.'] } }
    }
    if (hold === path) { hold = ''; held = { route, value }; return }
    await route.fulfill({ json: value })
  })
  return {
    ...base, requests,
    fail: (path: string) => { failure = path }, bad: (kind: string) => { invalid = kind },
    hold: (path: string) => { hold = path }, held: () => held !== undefined,
    release: async () => { await held!.route.fulfill({ json: held!.value }); held = undefined },
  }
}
