import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
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
  return { commit, fetchCommits: vi.fn(), fetchDiff: vi.fn() }
})

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  fetchCommits: fixture.fetchCommits,
  fetchCommit: async () => ({
    revision: fixture.commit.id,
    commit: fixture.commit,
    changes: [
      { status: 'added', path: 'src/new.go', additions: 2, deletions: 0 },
      { status: 'deleted', path: 'src/old.go', additions: 0, deletions: 3 },
    ],
  }),
  fetchDiff: fixture.fetchDiff,
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

beforeEach(() => {
  fixture.fetchCommits.mockReset()
  fixture.fetchCommits.mockResolvedValue({ revision: fixture.commit.id, commits: [fixture.commit], offset: 0, has_more: false })
  fixture.fetchDiff.mockReset()
  fixture.fetchDiff.mockResolvedValue({
    base: 'b'.repeat(40),
    head: fixture.commit.id,
    files: [{ status: 'added', path: 'src/new.go', additions: 2, deletions: 0 }],
    patch: '@@ -1 +1 @@\n-old\n+new',
    truncated: false,
  })
})

afterEach(cleanup)

test('history links commits at the immutable revision', async () => {
  render(page(<HistoryPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&path=src%2Fnew.go&ref=' + fixture.commit.id)} />))
  const link = await screen.findByRole('link', { name: /Add launch status/ })
  expect(link.getAttribute('href')).toContain(`ref=${fixture.commit.id}`)
  expect(document.body.textContent).toContain('Ada · ada@example.com')
})

test('history bounds an initial request failure', async () => {
  fixture.fetchCommits.mockRejectedValueOnce(new Error('initial history failure'))

  render(page(<HistoryPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp')} />))

  const alert = await screen.findByRole('alert')
  expect(alert.textContent?.trim()).toBe('initial history failure')
  expect(document.body.textContent).not.toContain('Error: initial history failure')
  expect(document.body.textContent).not.toContain('No commits are visible')
})

test('history bounds a load-more request failure', async () => {
  const oversized = `load-more failure ${'x'.repeat(600)}`
  const expected = `${oversized.slice(0, 511)}…`
  fixture.fetchCommits
    .mockResolvedValueOnce({ revision: fixture.commit.id, commits: [fixture.commit], offset: 0, has_more: true })
    .mockRejectedValueOnce(new Error(oversized))

  render(page(<HistoryPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp')} />))
  await screen.findByText('Add launch status')
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }))

  const alert = await screen.findByRole('alert')
  expect(alert.textContent?.trim()).toBe(expected)
  expect(expected).toHaveLength(512)
  expect(document.body.textContent).not.toContain('Error: load-more failure')
  expect(screen.getByRole('link', { name: /Add launch status/ })).toBeTruthy()
})

test('history ignores a stale load-more response after navigation', async () => {
  const oldCommit = { ...fixture.commit, id: 'c'.repeat(40), short_id: 'ccccccc', subject: 'Old repository commit' }
  const staleCommit = { ...fixture.commit, id: 'd'.repeat(40), short_id: 'ddddddd', subject: 'Stale page commit' }
  const newCommit = { ...fixture.commit, id: 'e'.repeat(40), short_id: 'eeeeeee', subject: 'New repository commit' }
  let resolveStale!: (value: { revision: string; commits: Array<typeof fixture.commit>; offset: number; has_more: boolean }) => void
  const stalePage = new Promise<{ revision: string; commits: Array<typeof fixture.commit>; offset: number; has_more: boolean }>((resolve) => {
    resolveStale = resolve
  })
  fixture.fetchCommits
    .mockResolvedValueOnce({ revision: oldCommit.id, commits: [oldCommit], offset: 0, has_more: true })
    .mockReturnValueOnce(stalePage)
    .mockResolvedValueOnce({ revision: newCommit.id, commits: [newCommit], offset: 0, has_more: false })

  const { rerender } = render(page(<HistoryPage params={new URLSearchParams('repo=example.com%2Fold')} />))
  await screen.findByText('Old repository commit')
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
  await waitFor(() => expect(fixture.fetchCommits).toHaveBeenCalledTimes(2))
  const staleSignal = fixture.fetchCommits.mock.calls[1][5] as AbortSignal

  rerender(page(<HistoryPage params={new URLSearchParams('repo=example.com%2Fnew')} />))
  await screen.findByText('New repository commit')
  expect(staleSignal.aborted).toBe(true)

  resolveStale({ revision: oldCommit.id, commits: [staleCommit], offset: 50, has_more: false })
  await waitFor(() => expect(screen.queryByText('Stale page commit')).toBeNull())
})

test('commit renders bounded patch rows and does not link a deleted file at the new revision', async () => {
  render(page(<CommitPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&ref=' + fixture.commit.id)} />))
  expect(await screen.findByRole('heading', { name: 'Add launch status' })).toBeTruthy()
  expect(screen.getByRole('region', { name: 'added file: src/new.go' })).toBeTruthy()
  expect(screen.getByText('+new')).toBeTruthy()
  expect(screen.getByText('-old')).toBeTruthy()
  expect(screen.getByRole('link', { name: 'src/new.go' }).getAttribute('href')).toContain('/file?')
  expect(screen.queryByRole('link', { name: 'src/old.go' })).toBeNull()
})

test('commit groups an ordered multi-file patch into deferred semantic regions', async () => {
  const patchLines = [
    'diff --git "a/raw before.go" "b/raw after.go"',
    'similarity index 82%',
    '',
    '--- "a/raw before.go"',
    '+++ "b/raw after.go"',
    '@@ -1 +1 @@',
    '-package before',
    '---leading minus content',
    '+package after',
    '+++leading plus content',
    'diff --git a/src/old.go b/src/old.go',
    'deleted file mode 100644',
    '--- a/src/old.go',
    '+++ /dev/null',
    '@@ -1 +0,0 @@',
    '-package old',
    'diff --git a/assets/logo.bin b/assets/logo.bin',
    'new file mode 100644',
    'Binary files /dev/null and b/assets/logo.bin differ',
  ]
  fixture.fetchDiff.mockResolvedValueOnce({
    base: 'b'.repeat(40),
    head: fixture.commit.id,
    files: [
      { status: 'renamed', old_path: 'src/structured before.go', path: 'src/structured after.go', additions: 2, deletions: 2 },
      { status: 'deleted', path: 'src/old.go', additions: 0, deletions: 1 },
      { status: 'added', path: 'assets/logo.bin', binary: true },
    ],
    patch: `${patchLines.join('\n')}\n`,
    truncated: false,
  })

  render(page(<CommitPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&ref=' + fixture.commit.id)} />))

  const regions = await screen.findAllByRole('region')
  expect(regions).toHaveLength(3)
  expect(regions[0].getAttribute('aria-labelledby')).toBe('commit-diff-file-0')
  expect(regions[0].textContent).toContain('src/structured before.go → src/structured after.go')
  expect(regions[0].textContent).toContain('diff --git "a/raw before.go" "b/raw after.go"')
  expect(regions[0].textContent).toContain('+package after')
  expect(regions[0].textContent).not.toContain('-package old')
  expect(regions[1].getAttribute('aria-labelledby')).toBe('commit-diff-file-1')
  expect(regions[1].textContent).toContain('src/old.go')
  expect(regions[1].textContent).toContain('-package old')
  expect(regions[2].textContent).toContain('assets/logo.bin')
  expect(regions[2].textContent).toContain('binary')
  expect(regions[2].textContent).toContain('Binary files /dev/null and b/assets/logo.bin differ')
  const renderedLines = regions.flatMap((region) => Array.from(region.children[1].children, (line) => line.textContent))
  expect(renderedLines).toEqual(patchLines)
  expect(screen.getByText('+++leading plus content').className).toBe(screen.getByText('+package after').className)
  expect(screen.getByText('+++leading plus content').className).not.toBe(screen.getByText('+++ "b/raw after.go"').className)
  expect(screen.getByText('---leading minus content').className).toBe(screen.getByText('-package before').className)
  expect(screen.getByText('---leading minus content').className).not.toBe(screen.getByText('--- "a/raw before.go"').className)
  expect(regions.every((region) => region.style.contentVisibility === 'auto')).toBe(true)
  expect(regions.every((region) => region.style.containIntrinsicSize === 'auto 320px')).toBe(true)
})

test('commit preserves an unmatched patch prelude without assigning file authority', async () => {
  fixture.fetchDiff.mockResolvedValueOnce({
    base: 'b'.repeat(40),
    head: fixture.commit.id,
    files: [
      { status: 'added', path: 'src/new.go', additions: 1, deletions: 0 },
      { status: 'modified', path: 'src/partial.go', additions: 1, deletions: 0 },
    ],
    patch: 'partial patch context\ndiff --git a/src/new.go b/src/new.go\n@@ -0,0 +1 @@\n+package new\ndiff --git a/src/partial.go b/src/partial.go\n@@ -1 +1 @@\n+partial line\ndiff --git a/raw/unmatched.go b/raw/unmatched.go\npartial final line',
    truncated: true,
  })

  render(page(<CommitPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&ref=' + fixture.commit.id)} />))

  expect(await screen.findByText('Diff truncated')).toBeTruthy()
  const regions = screen.getAllByRole('region')
  expect(regions).toHaveLength(4)
  expect(regions[0].textContent).toContain('Patch prelude')
  expect(regions[0].textContent).toContain('partial patch context')
  expect(regions[1].textContent).toContain('src/new.go')
  expect(regions[1].textContent).toContain('+package new')
  expect(regions[2].textContent).toContain('src/partial.go')
  expect(regions[2].textContent).toContain('+partial line')
  expect(regions[3].textContent).toContain('File 3')
  expect(regions[3].textContent).toContain('diff --git a/raw/unmatched.go b/raw/unmatched.go')
  expect(regions[3].textContent).toContain('partial final line')
})

test('commit keeps a headerless multi-file patch identity neutral', async () => {
  fixture.fetchDiff.mockResolvedValueOnce({
    base: 'b'.repeat(40),
    head: fixture.commit.id,
    files: [
      { status: 'modified', path: 'src/one.go', additions: 1, deletions: 1 },
      { status: 'modified', path: 'src/two.go', additions: 1, deletions: 1 },
    ],
    patch: '@@ -1 +1 @@\n-old\n+new\n',
    truncated: false,
  })

  render(page(<CommitPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&ref=' + fixture.commit.id)} />))

  const region = await screen.findByRole('region', { name: 'Patch' })
  expect(region.textContent).not.toContain('src/one.go')
  expect(region.textContent).not.toContain('src/two.go')
  expect(Array.from(region.children[1].children, (line) => line.textContent)).toEqual(['@@ -1 +1 @@', '-old', '+new'])
})

test('commit omits a phantom patch region when Git returns no patch text', async () => {
  fixture.fetchDiff.mockResolvedValueOnce({
    base: 'b'.repeat(40),
    head: fixture.commit.id,
    files: [{ status: 'modified', path: 'src/new.go', additions: 0, deletions: 0 }],
    patch: '',
    truncated: false,
  })

  render(page(<CommitPage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&ref=' + fixture.commit.id)} />))

  expect(await screen.findByRole('heading', { name: 'Add launch status' })).toBeTruthy()
  expect(screen.queryByRole('region')).toBeNull()
})

test('blame maps source lines to commit metadata', async () => {
  render(page(<BlamePage params={new URLSearchParams('repo=example.com%2Facme%2Fapp&path=src%2Fnew.go&ref=' + fixture.commit.id)} />))
  expect(await screen.findByText('package main')).toBeTruthy()
  const commitLink = screen.getByRole('link', { name: /Ada aaaaaaaa/ })
  expect(commitLink.getAttribute('href')).toBe(`#/commit?repo=example.com%2Facme%2Fapp&ref=${fixture.commit.id}`)
})
