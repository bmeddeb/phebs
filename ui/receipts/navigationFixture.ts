import type { Page, Route } from '@playwright/test'

export const navigationRepositories = ['example.test/alpha', 'example.test/beta']
export const navigationCommits = ['a'.repeat(40), 'b'.repeat(40)]
export const navigationMarkdown = '# Navigation preview\n\nThird line.\n'

export async function navigationFixture(page: Page) {
  const sources: { repo: string; ref: string; path: string }[] = []
  const symbols: { repo: string; ref: string; path: string }[] = []
  const errors: string[] = []
  const unexpected: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (['error', 'warning'].includes(message.type())) errors.push(message.text()) })
  let holdMetadata = false
  let held: Route | undefined
  const repositories = navigationRepositories.map((name, index) => ({
    name, indexed_commit_hash: navigationCommits[index], clone_url: '', orphaned: false,
    is_fork: false, is_archived: false, is_public: true,
    last_index_job_state: 'unavailable', last_extraction_job_state: 'unavailable',
    last_caller_job_state: 'unavailable', last_resolver_job_state: 'unavailable',
  }))
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    const repo = url.searchParams.get('repo') ?? ''
    const ref = url.searchParams.get('ref') ?? ''
    const path = url.searchParams.get('path') ?? ''
    let value: unknown
    if (url.pathname === '/api/auth/status') value = {
      authenticated: true, auth_required: true, setup_required: false,
      oidc_enabled: false, password_enabled: true, csrf_token: 'navigation-fixture',
      user: { id: 'navigation-reader', email: 'navigation@localhost.test', is_admin: false },
    }
    else if (url.pathname === '/api/version') value = { version: 'navigation-fixture', capabilities: [] }
    else if (url.pathname === '/api/repo-status') {
      if (holdMetadata) { holdMetadata = false; held = route; return }
      value = repositories
    }
    else if (url.pathname === '/api/folder_contents') value = { entries: path ? [] : [
      { name: 'main.go', type: 'file', size: 40 },
      { name: 'README.md', type: 'file', size: navigationMarkdown.length },
    ] }
    else if (url.pathname === '/api/source' || ['/api/find_definitions', '/api/find_references', '/api/hover'].includes(url.pathname)) {
      if (navigationCommits[navigationRepositories.indexOf(repo)] !== ref) unexpected.push(`${url.pathname}: crossed revision`)
      if (url.pathname === '/api/source') {
        sources.push({ repo, ref, path })
        const content = path.endsWith('.md') ? navigationMarkdown : `package ${repo.endsWith('alpha') ? 'alpha' : 'beta'}\n\nfunc Main() {}\n`
        value = { content, encoding: 'utf8', size: content.length }
      } else {
        symbols.push({ repo, ref, path })
        value = { available: false, locations: [] }
      }
    }
    else if (url.pathname === '/api/stream_search') {
      const files = [0, 0, 1].map((index, ordinal) => ({
        repo: navigationRepositories[index], ref: navigationCommits[index],
        path: ordinal === 1 ? 'helper.go' : 'main.go', language: 'Go',
        chunks: [{ start_line: 3, content: 'func Main() {}\n', ranges: [{ start_line: 3, start_col: 6, end_line: 3, end_col: 10 }] }],
      }))
      const stats = { file_count: 3, match_count: 3, duration_ms: 1 }
      const receipt = {
        schema: 'phebs-search-scope-v1', kind: 'all_code', membership_policy: 'all-visible',
        expression_digest: 'sha256:' + '1'.repeat(64), result_set_digest: 'sha256:' + '2'.repeat(64),
        digest: 'sha256:' + '3'.repeat(64), result_files: 3, result_matches: 3,
        revisions: repositories.map(repo => ({ repository: repo.name, commit: repo.indexed_commit_hash })),
      }
      await route.fulfill({ contentType: 'text/event-stream', body:
        `event: results\ndata: ${JSON.stringify({ files, stats })}\n\nevent: scope\ndata: ${JSON.stringify(receipt)}\n\nevent: done\ndata: ${JSON.stringify(stats)}\n\n`,
      })
      return
    }
    else { unexpected.push(url.pathname); value = { error: 'unexpected navigation fixture request' } }
    await route.fulfill({ json: value })
  })
  return {
    sources, symbols, errors, unexpected,
    holdNextMetadata: () => { holdMetadata = true },
    metadataHeld: () => held !== undefined,
    releaseMetadata: async () => { await held!.fulfill({ json: repositories }); held = undefined },
  }
}
