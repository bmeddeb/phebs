import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import SearchPage from './SearchPage'
import { repoFilter, runeColumnToUTF16Offset } from '../util'
import type { Chunk, FileResult, Range, RepoStatus, SearchResult, SearchScopeReceipt, Stats, TreeEntry } from '../api'

// streamSearch fake: tests drive the captured callbacks to simulate the SSE stream.
type Callbacks = {
  calls?: string[]
  onBatch?: (r: SearchResult) => void
  onDone?: (s: Stats) => void
  onError?: (msg: string) => void
  onScope?: (receipt: SearchScopeReceipt) => void
  selectors?: { kind: string; repository?: string; serviceKey?: string }[]
}
const stream = vi.hoisted(() => ({} as Callbacks))
const repositoryAPI = vi.hoisted(() => ({
  statusCalls: 0,
  statusFailures: 0,
  statuses: [] as RepoStatus[],
  folderCalls: [] as { repo: string; ref: string; path: string; signal: AbortSignal }[],
  folderFailures: new Map<string, number>(),
  folders: new Map<string, TreeEntry[]>(),
}))

const folderFixtureKey = (repo: string, path: string) => `${repo}\0${path}`

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  fetchRepoStatus: async () => {
    repositoryAPI.statusCalls++
    if (repositoryAPI.statusFailures > 0) {
      repositoryAPI.statusFailures--
      throw new Error('repository status unavailable')
    }
    return repositoryAPI.statuses
  },
  fetchFolderContents: async (repo: string, ref: string, path: string, signal: AbortSignal) => {
    repositoryAPI.folderCalls.push({ repo, ref, path, signal })
    const key = folderFixtureKey(repo, path)
    const failures = repositoryAPI.folderFailures.get(key) ?? 0
    if (failures > 0) {
      repositoryAPI.folderFailures.set(key, failures - 1)
      throw new Error('folder unavailable')
    }
    return { entries: repositoryAPI.folders.get(key) ?? [] }
  },
  streamSearch: (
    q: string,
    onBatch: Callbacks['onBatch'],
    onDone: Callbacks['onDone'],
    onError: Callbacks['onError'],
    selector: { kind: string; repository?: string; serviceKey?: string },
    onScope: Callbacks['onScope'],
  ) => {
    stream.calls = [...(stream.calls ?? []), q]
    stream.selectors = [...(stream.selectors ?? []), selector]
    Object.assign(stream, { onBatch, onDone, onError, onScope })
    return () => {}
  },
}))
vi.mock('../lang', () => ({
  languageFor: async () => null,
  langColor: () => '#888',
  langName: () => 'x',
}))
vi.mock('../highlight', () => ({
  tokenize: (line: string) => [{ from: 0, to: line.length, color: '' }],
  highlightStyle: () => ({}), // unused; satisfies FilePage's import via ../App
}))

const range = (start_line: number, start_col: number, end_line: number, end_col: number): Range => ({
  start_line,
  start_col,
  end_line,
  end_col,
})
const chunk = (content: string, start_line: number, ranges: Range[]): Chunk => ({ content, start_line, ranges })

const aMain: FileResult = {
  repo: 'github.com/a/one',
  path: 'cmd/main.go',
  ref: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  language: 'Go',
  chunks: [chunk('func main() {\n', 3, [range(3, 1, 3, 5)])],
}
const aUtil: FileResult = {
  repo: 'github.com/a/one',
  path: 'pkg/util.go',
  ref: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  language: 'Go',
  chunks: [chunk('func util() {\n', 10, [range(10, 1, 10, 5)])],
}
const bIndex: FileResult = {
  repo: 'github.com/b/two',
  path: 'src/index.ts',
  ref: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
  language: 'TypeScript',
  chunks: [chunk('function index() {\n', 7, [range(7, 1, 7, 9)])],
}
const contextBeforeMatch: FileResult = {
  repo: 'github.com/context/lines',
  path: 'context.go',
  ref: 'cccccccccccccccccccccccccccccccccccccccc',
  language: 'Go',
  chunks: [chunk('// context\nfunc match() {', 2, [range(3, 6, 3, 11)])],
}
const batch = (files: FileResult[]): SearchResult => ({
  files,
  stats: { match_count: files.length, file_count: files.length, duration_ms: 5 },
})

const engine = new Client()
const renderSearch = (params = 'q=foo') =>
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <SearchPage params={new URLSearchParams(params)} />
      </BaseProvider>
    </StyletronProvider>,
  )

// The search input autofocuses on mount; keyboard tests need it blurred.
const blurInput = () => act(async () => (document.activeElement as HTMLElement | null)?.blur())
const allFiles = async () => {
  renderSearch()
  await act(async () => stream.onBatch!(batch([aMain, aUtil, bIndex])))
  await blurInput()
}
const key = (k: string) => fireEvent.keyDown(window, { key: k })
const hash = () => decodeURIComponent(window.location.hash)

beforeEach(() => {
  window.location.hash = ''
  delete stream.onBatch
  delete stream.onDone
  delete stream.onError
  delete stream.onScope
  stream.selectors = []
  stream.calls = []
  repositoryAPI.statusCalls = 0
  repositoryAPI.statusFailures = 0
  repositoryAPI.statuses = []
  repositoryAPI.folderCalls = []
  repositoryAPI.folderFailures = new Map()
  repositoryAPI.folders = new Map()
})
afterEach(cleanup)

const repoStatus = (name: string, commit?: string): RepoStatus => ({
  name,
  clone_url: `https://${name}.git`,
  indexed_commit_hash: commit,
  orphaned: false,
  last_index_job_state: 'unavailable',
})

test('cold Search browses only visible repositories and opens a pinned file without searching', async () => {
  repositoryAPI.statuses = [
    repoStatus('github.com/a/visible', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'),
    repoStatus('github.com/b/visible', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'),
  ]
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', ''), [
    { name: 'README.md', type: 'file', size: 12 },
  ])

  renderSearch('')

  expect(await screen.findByRole('option', { name: 'github.com/a/visible' })).toBeTruthy()
  expect(screen.getByRole('option', { name: 'github.com/b/visible' })).toBeTruthy()
  expect(screen.queryByText('github.com/hidden/secret')).toBeNull()
  const file = await screen.findByRole('link', { name: 'README.md' })
  expect(file.getAttribute('href')).toBe(
    '#/file?repo=github.com%2Fa%2Fvisible&path=README.md&ref=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  )
  expect(repositoryAPI.folderCalls.map(({ repo, ref, path }) => ({ repo, ref, path }))).toEqual([{
    repo: 'github.com/a/visible',
    ref: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    path: '',
  }])
  expect(stream.calls).toEqual([])
})

test('repository selection loads one visible root and never requests an absent repository', async () => {
  repositoryAPI.statuses = [
    repoStatus('github.com/a/visible', 'a-ref'),
    repoStatus('github.com/b/visible', 'b-ref'),
  ]
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', ''), [])
  repositoryAPI.folders.set(folderFixtureKey('github.com/b/visible', ''), [])

  renderSearch('')
  const picker = await screen.findByRole<HTMLSelectElement>('combobox', { name: 'Repository' })
  await act(async () => fireEvent.change(picker, { target: { value: 'github.com/b/visible' } }))

  await vi.waitFor(() => expect(repositoryAPI.folderCalls).toHaveLength(2))
  expect(repositoryAPI.folderCalls.map(({ repo }) => repo)).toEqual([
    'github.com/a/visible',
    'github.com/b/visible',
  ])
  expect(repositoryAPI.folderCalls.some(({ repo }) => repo === 'github.com/hidden/secret')).toBe(false)
})

test('directory expansion is one-level lazy and re-expansion uses the cache', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/visible', 'a-ref')]
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', ''), [
    { name: 'src', type: 'dir' },
  ])
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', 'src'), [
    { name: 'main.go', type: 'file' },
  ])

  renderSearch('')
  const directory = await screen.findByRole('button', { name: 'src' })
  fireEvent.click(directory)
  expect(await screen.findByRole('link', { name: 'main.go' })).toBeTruthy()
  fireEvent.click(directory)
  fireEvent.click(directory)
  expect(screen.getByRole('link', { name: 'main.go' })).toBeTruthy()
  expect(repositoryAPI.folderCalls.map(({ path }) => path)).toEqual(['', 'src'])
})

test('repository filter insertion preserves the query and never duplicates its atom', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/visible', 'a-ref')]
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', ''), [])

  renderSearch('q=needle+lang%3Ago')
  const add = await screen.findByRole('button', { name: 'Add repository filter' })
  fireEvent.click(add)
  const input = screen.getByRole<HTMLInputElement>('textbox', { name: 'Search code' })
  expect(input.value).toBe('needle lang:go repo:"^github\\\\.com/a/visible$"')
  fireEvent.click(add)
  expect(input.value).toBe('needle lang:go repo:"^github\\\\.com/a/visible$"')
})

test('repository and folder failures have bounded retry paths', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/visible', 'a-ref')]
  repositoryAPI.statusFailures = 1
  repositoryAPI.folderFailures.set(folderFixtureKey('github.com/a/visible', ''), 1)
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', ''), [
    { name: 'README.md', type: 'file' },
  ])

  renderSearch('')
  fireEvent.click(await screen.findByRole('button', { name: 'Retry repositories' }))
  expect(await screen.findByRole('option', { name: 'github.com/a/visible' })).toBeTruthy()
  fireEvent.click(await screen.findByRole('button', { name: 'Retry folder' }))
  expect(await screen.findByRole('link', { name: 'README.md' })).toBeTruthy()
  expect(repositoryAPI.statusCalls).toBe(2)
  expect(repositoryAPI.folderCalls).toHaveLength(2)
})

test('mobile repository drawer exposes an explicit collapsed lifecycle', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/visible', 'a-ref')]
  repositoryAPI.folders.set(folderFixtureKey('github.com/a/visible', ''), [])

  renderSearch('')
  const toggle = document.querySelector<HTMLButtonElement>(
    'button[aria-controls="search-repository-explorer"]',
  )
  expect(toggle).not.toBeNull()
  if (!toggle) throw new Error('missing mobile repository drawer toggle')
  expect(toggle.getAttribute('aria-expanded')).toBe('false')
  fireEvent.click(toggle)
  expect(toggle.getAttribute('aria-expanded')).toBe('true')
  expect(document.querySelector('#search-repository-explorer')?.getAttribute('data-mobile-open')).toBe('true')
})

test('permission-filtered empty repository state is explicit', async () => {
  renderSearch('')
  expect(await screen.findByText('No visible repositories.')).toBeTruthy()
  expect(repositoryAPI.folderCalls).toHaveLength(0)
})

test('streaming: batches render incrementally while phase stays streaming', async () => {
  renderSearch()
  await act(async () => stream.onBatch!(batch([aMain, aUtil])))
  expect(document.body.textContent).toContain('2 matches · 2 files')
  expect(document.body.textContent).toContain('searching…')
  expect(screen.getByTestId('streaming-skeleton')).toBeTruthy()
  expect(screen.getByText('main.go')).toBeTruthy()
  expect(screen.getByText('util.go')).toBeTruthy()
  expect(screen.queryByText('index.ts')).toBeNull()

  await act(async () => stream.onBatch!(batch([bIndex])))
  expect(document.body.textContent).toContain('3 matches · 3 files')
  expect(document.body.textContent).toContain('searching…')
  expect(screen.getByTestId('streaming-skeleton')).toBeTruthy()
  expect(screen.getByText('index.ts')).toBeTruthy()
  expect(screen.getByText('github.com/b/two')).toBeTruthy()
})

test('search results expose their focused unit without mounting exact paths by default', async () => {
  repositoryAPI.statuses = [{
    ...repoStatus('github.com/a/one', aMain.ref),
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'service-one',
      digest: `sha256:${'a'.repeat(64)}`,
      primary_paths: ['cmd/**'],
      supporting_paths: ['pkg/**'],
      primary_path_count: 1,
      supporting_path_count: 1,
      search_index_posture: 'focused',
      typed_index_posture: 'repository-root-unbound',
    },
  }]
  renderSearch()
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onBatch!(batch([aMain])))

  expect(screen.getByTestId('analysis-scope-panel')).toBeTruthy()
  expect(screen.getByText('service unit service-one')).toBeTruthy()
  expect(screen.queryByText('cmd/**')).toBeNull()
  const detail = screen.getByTestId('analysis-scope-detail')
  fireEvent.click(within(detail).getByText('Exact repository scope'))
  fireEvent.click(within(detail).getByRole('button', { name: /github.com\/a\/one/ }))
  expect(screen.getByText('cmd/**')).toBeTruthy()
  expect(screen.getByText(/does not widen focused source scope/)).toBeTruthy()
})

test('an explicit revision result does not inherit the current focused analysis unit', async () => {
  const historicalRef = 'dddddddddddddddddddddddddddddddddddddddd'
  repositoryAPI.statuses = [{
    ...repoStatus('github.com/a/one', aMain.ref),
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'service-one',
      digest: `sha256:${'a'.repeat(64)}`,
      primary_paths: ['cmd/**'],
      supporting_paths: ['pkg/**'],
      primary_path_count: 1,
      supporting_path_count: 1,
      search_index_posture: 'focused',
      typed_index_posture: 'repository-root-unbound',
    },
  }]
  renderSearch('q=foo+rev%3Arelease')
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onBatch!(batch([{ ...aMain, ref: historicalRef }])))

  expect(screen.getByText('search_revision_scope_not_projectable')).toBeTruthy()
  expect(screen.getByText(/current analysis unit is not projected across revisions/)).toBeTruthy()
  expect(screen.queryByText('service unit service-one')).toBeNull()
  expect(screen.queryByText('1 focused')).toBeNull()
})

test('an indexing transition does not relabel earlier results with the newer unit scope', async () => {
  const newerIndexedRef = 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee'
  repositoryAPI.statuses = [{
    ...repoStatus('github.com/a/one', newerIndexedRef),
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'service-two',
      digest: `sha256:${'b'.repeat(64)}`,
      primary_paths: ['services/two/**'],
      supporting_paths: [],
      primary_path_count: 1,
      supporting_path_count: 0,
      search_index_posture: 'focused',
      typed_index_posture: 'repository-root-unbound',
    },
  }]
  renderSearch()
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onBatch!(batch([aMain])))

  expect(screen.getByText('search_revision_scope_not_projectable')).toBeTruthy()
  expect(screen.getByText(new RegExp(aMain.ref))).toBeTruthy()
  expect(screen.getByText(new RegExp(newerIndexedRef))).toBeTruthy()
  expect(screen.queryByText('service unit service-two')).toBeNull()
})

test('a zero-result query still exposes its exact focused repository scope', async () => {
  repositoryAPI.statuses = [{
    ...repoStatus('github.com/a/one', aMain.ref),
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'service-one',
      digest: `sha256:${'a'.repeat(64)}`,
      primary_paths: ['cmd/**'],
      supporting_paths: ['pkg/**'],
      primary_path_count: 1,
      supporting_path_count: 1,
      search_index_posture: 'focused',
      typed_index_posture: 'unit-bound',
      typed_index: { kind: 'scip', path: 'pkg/index.scip' },
    },
  }]
  const query = `${repoFilter('github.com/a/one')} outside-unit-needle`
  renderSearch(`q=${encodeURIComponent(query)}`)
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onDone!({
    match_count: 0,
    file_count: 0,
    duration_ms: 3,
  }))

  expect(screen.getByText(/No results for/)).toBeTruthy()
  expect(screen.getByText('service unit service-one')).toBeTruthy()
  expect(screen.getByText('1 focused')).toBeTruthy()
  expect(screen.queryByText('cmd/**')).toBeNull()
})

test('a zero-result query reports an unprojectable repository filter as a gap', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/one', aMain.ref)]
  renderSearch(`q=${encodeURIComponent('repo:acme outside-unit-needle')}`)
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onDone!({
    match_count: 0,
    file_count: 0,
    duration_ms: 3,
  }))

  expect(screen.getByText('search_scope_filter_not_projectable')).toBeTruthy()
  expect(screen.getByText(/cannot be mapped exactly/)).toBeTruthy()
  expect(screen.queryByText('whole-repository scope')).toBeNull()
})

test('a zero-result query does not promote an unindexed repository to exact scope', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/one')]
  const query = `${repoFilter('github.com/a/one')} outside-unit-needle`
  renderSearch(`q=${encodeURIComponent(query)}`)
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onDone!({
    match_count: 0,
    file_count: 0,
    duration_ms: 3,
  }))

  expect(screen.getByText('search_index_scope_unavailable')).toBeTruthy()
  expect(screen.getByText(/no current indexed commit/)).toBeTruthy()
  expect(screen.queryByText('whole-repository scope')).toBeNull()
})

test('a zero-result revision query reports that exact revision scope is unavailable', async () => {
  repositoryAPI.statuses = [repoStatus('github.com/a/one', aMain.ref)]
  const query = `${repoFilter('github.com/a/one')} rev:release outside-unit-needle`
  renderSearch(`q=${encodeURIComponent(query)}`)
  await screen.findByRole('option', { name: 'github.com/a/one' })
  await act(async () => stream.onDone!({
    match_count: 0,
    file_count: 0,
    duration_ms: 3,
  }))

  expect(screen.getByText('search_revision_scope_not_projectable')).toBeTruthy()
  expect(screen.getByText(/no result commit/)).toBeTruthy()
  expect(screen.queryByText('whole-repository scope')).toBeNull()
})

test('streaming skeleton unmounts when the stream completes', async () => {
  renderSearch()
  await act(async () => stream.onBatch!(batch([aMain])))
  expect(screen.getByTestId('streaming-skeleton')).toBeTruthy()

  await act(async () => stream.onDone!({ match_count: 1, file_count: 1, duration_ms: 3 }))
  expect(screen.queryByTestId('streaming-skeleton')).toBeNull()
  expect(screen.getByRole('button', { name: /github\.com\/a\/one/, expanded: true })).toBeTruthy()
})

test('stopping a stream preserves partial-state semantics', async () => {
  renderSearch()
  fireEvent.click(screen.getByRole('button', { name: 'Stop' }))

  expect(document.body.textContent).toContain('stopped')
  expect(document.body.textContent).not.toContain('No results for foo.')
  expect(screen.queryByTestId('streaming-skeleton')).toBeNull()
  expect(screen.getByRole('button', { name: 'Search' })).toBeTruthy()
})

test('done with zero files shows no-results message', async () => {
  renderSearch()
  await act(async () => stream.onDone!({ match_count: 0, file_count: 0, duration_ms: 2 }))
  expect(document.body.textContent).toContain('No results for foo.')
  expect(document.body.textContent).not.toContain('searching…')
})

test('stream error renders a notification', async () => {
  renderSearch()
  await act(async () => stream.onError!('boom'))
  expect(screen.getByText('boom')).toBeTruthy()
})

// j/k clamp to [0, len-1]; Enter deep-links the selected file with its first match line.
for (const [name, keys, want] of [
  ['j then Enter opens the first file', ['j'], '#/file?repo=github.com/a/one&path=cmd/main.go&ref=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&L=3'],
  ['k at top clamps: Enter still opens the first file', ['j', 'k', 'k'], '#/file?repo=github.com/a/one&path=cmd/main.go&ref=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&L=3'],
  ['j past the end clamps: Enter opens the last file', ['j', 'j', 'j', 'j'], '#/file?repo=github.com/b/two&path=src/index.ts&ref=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb&L=7'],
] as const) {
  test(name, async () => {
    await allFiles()
    for (const k of keys) key(k)
    key('Enter')
    expect(hash()).toBe(want)
  })
}

test("o folds the selected file's repo group", async () => {
  await allFiles()
  key('j')
  key('o')
  expect(screen.queryByText('main.go')).toBeNull()
  expect(screen.queryByText('util.go')).toBeNull()
  expect(screen.getByText('index.ts')).toBeTruthy() // other group unaffected
  expect(screen.getByText('github.com/a/one')).toBeTruthy() // header stays
})

test('collapse guard: j selects from visible files only', async () => {
  await allFiles()
  fireEvent.click(screen.getByText('github.com/a/one')) // fold first repo
  key('j')
  key('Enter')
  expect(hash()).toBe('#/file?repo=github.com/b/two&path=src/index.ts&ref=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb&L=7')
})

test('typing guard: keys are ignored while the input is focused', async () => {
  await allFiles()
  await act(async () => screen.getByRole<HTMLInputElement>('textbox').focus())
  key('j')
  key('Enter')
  expect(window.location.hash).toBe('')
})

test('keyboard shortcuts are ignored while another control is focused', async () => {
  await allFiles()
  key('j')
  const fold = screen.getByRole('button', { name: /github\.com\/a\/one/, expanded: true })
  fold.focus()
  key('Enter')
  expect(window.location.hash).toBe('')
})

test('keyboard selection highlights the first match rather than leading context', async () => {
  renderSearch()
  await act(async () => stream.onBatch!(batch([contextBeforeMatch])))
  await blurInput()
  key('j')
  expect(document.querySelector('[data-selected-line="true"]')?.textContent).toContain('3func match() {')
})

test('the search textbox has a stable accessible name', () => {
  renderSearch()
  expect(screen.getByRole('textbox', { name: 'Search code' })).toBeTruthy()
})

test('facet rows toggle lang:/repo: terms into the query', async () => {
  await allFiles()
  fireEvent.click(screen.getByText('go')) // language facet
  expect(hash()).toBe('#/search?q=foo+lang:go')
  fireEvent.click(screen.getByText('one'))
  expect(hash()).toBe('#/search?q=foo+repo:"^github\\\\.com/a/one$"')
})

test('service deep link preserves query while switching scope and renders receipt posture', async () => {
  renderSearch('q=needle&scope=service&repository=example.invalid%2Fmono&service_key=orders-api')
  expect(stream.selectors).toEqual([{
    kind: 'service', repository: 'example.invalid/mono', serviceKey: 'orders-api',
  }])
  const digest64 = (fill: string) => `sha256:${fill.repeat(64).slice(0, 64)}`
  await act(async () => stream.onScope!({
    schema: 'phebs-search-scope-v1', kind: 'service',
    repository: 'example.invalid/mono', service_key: 'orders-api',
    service_status: 'stale',
    membership_policy: 'accepted-roles-union-shared-included-unowned-excluded-v1',
    expression_digest: digest64('a'),
    service_authority: {
      schema: 'phebs-service-scope-authority-v1', predicate_policy: 'p', topology_policy: 't',
      repository: 'example.invalid/mono', service_key: 'orders-api', status: 'stale',
      incarnation: 1, revision_selector: 'branch', revision_branch: 'main',
      revision_commit: 'abcdef1234567890abcdef1234567890abcdef12',
      expression_digest: digest64('a'),
      current_catalog_generation: 'cat-r2', catalog_control_revision: 2,
      active_catalog_generation: 'cat-r2', active_source_generation: 'src-r2',
      active_desired_generation: 'des-r2',
      service_state_digest: digest64('b'), service_state_revision: 1,
      state_summary_digest: digest64('c'), state_summary_revision: 1,
      repository_source_generation: 'repo-src-r2', repository_search_generation: 'repo-search-r2',
      path_digest: digest64('d'), path_count: 3, path_bytes: 90,
      predicate_atoms: 1, predicate_bytes: 16, digest: digest64('e'),
    },
    revisions: [{ repository: 'example.invalid/mono', commit: 'abcdef1234567890abcdef1234567890abcdef12' }],
    result_set_digest: digest64('f'), result_files: 1, result_matches: 1,
    digest: digest64('0'),
  }))
  expect(screen.getByText(/stale · shared paths included · unowned paths excluded/)).toBeTruthy()
  // T43.5: the receipt is a compact authority chip; digests never lead the band.
  const chip = screen.getByRole('button', { name: 'stale · as of abcdef1' })
  expect(chip.getAttribute('aria-haspopup')).toBe('dialog')
  expect(screen.queryByText(/sha256:/)).toBeNull()
  expect(decodeURIComponent(
    screen.getByRole('link', { name: 'All code' }).getAttribute('href') ?? '',
  )).toBe('#/search?q=needle')
})

test('submitting the current query starts a fresh search generation', async () => {
  renderSearch()
  expect(stream.calls).toEqual(['foo'])
  fireEvent.submit(screen.getByRole('textbox').closest('form')!)
  expect(stream.calls).toEqual(['foo', 'foo'])
})

test('repository facet buttons expose their pressed state', async () => {
  await allFiles()
  const button = screen.getByRole('button', { name: 'Add repository filter github.com/a/one' })
  expect(button.getAttribute('aria-pressed')).toBe('false')
  fireEvent.click(button)
})

test('rune columns convert to JavaScript UTF-16 offsets', () => {
  expect(runeColumnToUTF16Offset('🙂match', 1)).toBe(0)
  expect(runeColumnToUTF16Offset('🙂match', 2)).toBe(2)
  expect(runeColumnToUTF16Offset('🙂match', 7)).toBe(7)
})
