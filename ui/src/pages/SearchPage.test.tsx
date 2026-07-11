import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import SearchPage from './SearchPage'
import type { Chunk, FileResult, Range, SearchResult, Stats } from '../api'

// streamSearch fake: tests drive the captured callbacks to simulate the SSE stream.
type Callbacks = {
  onBatch?: (r: SearchResult) => void
  onDone?: (s: Stats) => void
  onError?: (msg: string) => void
}
const stream = vi.hoisted(() => ({} as Callbacks))

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  streamSearch: (
    _q: string,
    onBatch: Callbacks['onBatch'],
    onDone: Callbacks['onDone'],
    onError: Callbacks['onError'],
  ) => {
    Object.assign(stream, { onBatch, onDone, onError })
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
  language: 'Go',
  chunks: [chunk('func main() {\n', 3, [range(3, 1, 3, 5)])],
}
const aUtil: FileResult = {
  repo: 'github.com/a/one',
  path: 'pkg/util.go',
  language: 'Go',
  chunks: [chunk('func util() {\n', 10, [range(10, 1, 10, 5)])],
}
const bIndex: FileResult = {
  repo: 'github.com/b/two',
  path: 'src/index.ts',
  language: 'TypeScript',
  chunks: [chunk('function index() {\n', 7, [range(7, 1, 7, 9)])],
}
const batch = (files: FileResult[]): SearchResult => ({
  files,
  stats: { match_count: files.length, file_count: files.length, duration_ms: 5 },
})

const engine = new Client()
const renderSearch = () =>
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <SearchPage params={new URLSearchParams('q=foo')} />
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
})
afterEach(cleanup)

test('streaming: batches render incrementally while phase stays streaming', async () => {
  renderSearch()
  await act(async () => stream.onBatch!(batch([aMain, aUtil])))
  expect(document.body.textContent).toContain('2 matches in 2 files')
  expect(document.body.textContent).toContain('searching…')
  expect(screen.getByText('main.go')).toBeTruthy()
  expect(screen.getByText('util.go')).toBeTruthy()
  expect(screen.queryByText('index.ts')).toBeNull()

  await act(async () => stream.onBatch!(batch([bIndex])))
  expect(document.body.textContent).toContain('3 matches in 3 files')
  expect(document.body.textContent).toContain('searching…')
  expect(screen.getByText('index.ts')).toBeTruthy()
  expect(screen.getByText('github.com/b/two')).toBeTruthy()
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
for (const [name, keys] of [
  ['j then Enter opens the first file', ['j']],
  ['k at top clamps: Enter still opens the first file', ['j', 'k', 'k']],
] as const) {
  test(name, async () => {
    await allFiles()
    for (const k of keys) key(k)
    key('Enter')
    expect(hash()).toBe('#/file?repo=github.com/a/one&path=cmd/main.go&L=3')
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
  expect(hash()).toBe('#/file?repo=github.com/b/two&path=src/index.ts&L=7')
})

test('typing guard: keys are ignored while the input is focused', async () => {
  await allFiles()
  await act(async () => screen.getByRole<HTMLInputElement>('textbox').focus())
  key('j')
  key('Enter')
  expect(window.location.hash).toBe('')
})

test('facet rows toggle lang:/repo: terms into the query', async () => {
  await allFiles()
  fireEvent.click(screen.getByText('go')) // language facet
  expect(hash()).toBe('#/search?q=foo+lang:go')
  fireEvent.click(screen.getByText('one')) // repo facet (short name)
  expect(hash()).toBe('#/search?q=foo+repo:one')
})
