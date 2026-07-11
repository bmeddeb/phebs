import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import HistoryPage from './HistoryPage'
import CommitPage from './CommitPage'
import BlamePage from './BlamePage'

const fixture = vi.hoisted(() => {
  const commit = {
    id: 'a'.repeat(40),
    short_id: 'aaaaaaa',
    parent_ids: ['b'.repeat(40)],
    subject: 'Add launch status',
    message: 'Add launch status\n\nDetails.',
    author: { name: 'Ada', email: 'ada@example.com', time: '2026-07-11T10:00:00Z' },
    committer: { name: 'Ada', email: 'ada@example.com', time: '2026-07-11T10:00:00Z' },
  }
  return { commit }
})

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  fetchCommits: async () => ({ revision: fixture.commit.id, commits: [fixture.commit], offset: 0, has_more: false }),
  fetchCommit: async () => ({
    revision: fixture.commit.id,
    commit: fixture.commit,
    changes: [
      { status: 'added', path: 'src/new.go', additions: 2, deletions: 0 },
      { status: 'deleted', path: 'src/old.go', additions: 0, deletions: 3 },
    ],
  }),
  fetchDiff: async () => ({
    base: 'b'.repeat(40),
    head: fixture.commit.id,
    files: [],
    patch: '@@ -1 +1 @@\n-old\n+new',
    truncated: false,
  }),
  fetchBlame: async () => ({
    revision: fixture.commit.id,
    path: 'src/new.go',
    lines: [{ line: 1, original_line: 1, commit_id: fixture.commit.id, original_path: 'src/new.go', content: 'package main' }],
    commits: [fixture.commit],
    truncated: false,
  }),
}))

const engine = new Client()
const page = (child: React.ReactNode) => (
  <StyletronProvider value={engine}>
    <BaseProvider theme={LightTheme}>{child}</BaseProvider>
  </StyletronProvider>
)

afterEach(cleanup)

test('history links commits at the immutable revision', async () => {
  render(page(<HistoryPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&path=src%2Fnew.go&ref=' + fixture.commit.id)} />))
  const link = await screen.findByRole('link', { name: /Add launch status/ })
  expect(link.getAttribute('href')).toContain(`ref=${fixture.commit.id}`)
  expect(document.body.textContent).toContain('Ada · ada@example.com')
})

test('commit renders bounded patch rows and does not link a deleted file at the new revision', async () => {
  render(page(<CommitPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&ref=' + fixture.commit.id)} />))
  expect(await screen.findByRole('heading', { name: 'Add launch status' })).toBeTruthy()
  expect(screen.getByText('+new')).toBeTruthy()
  expect(screen.getByText('-old')).toBeTruthy()
  expect(screen.getByRole('link', { name: 'src/new.go' }).getAttribute('href')).toContain('/file?')
  expect(screen.queryByRole('link', { name: 'src/old.go' })).toBeNull()
})

test('blame maps source lines to commit metadata', async () => {
  render(page(<BlamePage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&path=src%2Fnew.go&ref=' + fixture.commit.id)} />))
  expect(await screen.findByText('package main')).toBeTruthy()
  const commitLink = screen.getByRole('link', { name: /Ada aaaaaaaa/ })
  expect(commitLink.getAttribute('href')).toBe(`#/commit?repo=example.com%2Facme%2Fapp&ref=${fixture.commit.id}`)
})
