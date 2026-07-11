import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import FilePage from './FilePage'
import type { SourceFile, TreeEntry } from '../api'

interface SourceCall {
  path: string
  ref: string
  signal: AbortSignal
  resolve: (file: SourceFile) => void
}

const api = vi.hoisted(() => ({
  sourceCalls: [] as SourceCall[],
  folderCalls: [] as { path: string; ref: string; signal: AbortSignal }[],
  folders: new Map<string, TreeEntry[]>(),
  statuses: [] as { name: string; indexed_commit_hash?: string; clone_url: string; orphaned: boolean }[],
}))

vi.mock('../api', () => ({
  fetchRepoStatus: async () => api.statuses,
  fetchSource: (_repo: string, path: string, ref: string, signal: AbortSignal) =>
    new Promise<SourceFile>((resolve) => api.sourceCalls.push({ path, ref, signal, resolve })),
  fetchFolderContents: async (_repo: string, ref: string, path: string, signal: AbortSignal) => {
    api.folderCalls.push({ path, ref, signal })
    return { entries: api.folders.get(path) ?? [] }
  },
}))

vi.mock('../lang', () => ({
  languageFor: async () => null,
  langColor: () => '#888',
  langName: () => 'text',
}))

vi.mock('../highlight', () => ({
  highlightStyle: () => ({}),
}))

const engine = new Client()
const view = (params: string) => (
  <StyletronProvider value={engine}>
    <BaseProvider theme={LightTheme}>
      <FilePage params={new URLSearchParams(params)} />
    </BaseProvider>
  </StyletronProvider>
)

beforeEach(() => {
  api.sourceCalls = []
  api.folderCalls = []
  api.folders = new Map()
  api.statuses = []
})

afterEach(cleanup)

test('late source responses cannot overwrite the current route', async () => {
  const rendered = render(view('repo=r&path=old.go&ref=old-ref'))
  expect(api.sourceCalls).toHaveLength(1)

  rendered.rerender(view('repo=r&path=new.go&ref=new-ref'))
  expect(api.sourceCalls).toHaveLength(2)
  expect(api.sourceCalls[0].signal.aborted).toBe(true)

  await act(async () => {
    api.sourceCalls[1].resolve({ content: 'one\ntwo', encoding: 'utf8', size: 7 })
  })
  expect(screen.getByText(/2 lines/)).toBeTruthy()

  await act(async () => {
    api.sourceCalls[0].resolve({ content: 'stale', encoding: 'utf8', size: 5 })
  })
  expect(screen.getByText(/2 lines/)).toBeTruthy()
  expect(screen.queryByText(/1 lines/)).toBeNull()
})

test('loads only the root and current-file ancestor folders at the requested ref', async () => {
  render(view('repo=r&path=src/components/Page.tsx&ref=abc123'))
  await waitFor(() => {
    expect(api.folderCalls.map((call) => [call.path, call.ref])).toEqual([
      ['', 'abc123'],
      ['src', 'abc123'],
      ['src/components', 'abc123'],
    ])
  })
})

test('a route without ref resolves the indexed revision before reading', async () => {
  api.statuses = [{
    name: 'r',
    clone_url: '',
    orphaned: false,
    indexed_commit_hash: 'indexed-commit',
  }]
  render(view('repo=r&path=README.md'))
  await waitFor(() => expect(api.sourceCalls).toHaveLength(1))
  expect(api.sourceCalls[0].ref).toBe('indexed-commit')
  expect(api.folderCalls.every((call) => call.ref === 'indexed-commit')).toBe(true)
})

test('a route without an indexed revision never falls back to mutable HEAD', async () => {
  render(view('repo=r&path=README.md'))
  expect(await screen.findByText('repository has no indexed revision')).toBeTruthy()
  expect(api.sourceCalls).toHaveLength(0)
  expect(api.folderCalls).toHaveLength(0)
})

test('open-in-search safely quotes filenames with whitespace', async () => {
  render(view('repo=r&path=docs%2FMy+File.md&ref=abc123'))
  fireEvent.click(screen.getByRole('button', { name: 'Open in search' }))
  expect(new URLSearchParams(window.location.hash.split('?')[1]).get('q')).toBe(
    'file:"My File\\\\.md$"',
  )
})

test('directory rows are buttons and load their children on expansion', async () => {
  api.folders.set('', [{ name: 'src', type: 'dir' }])
  api.folders.set('src', [{ name: 'main.go', type: 'file', size: 12 }])
  render(view('repo=r&path=README.md&ref=abc123'))

  const directory = await screen.findByRole('button', { name: 'src' })
  expect(directory.getAttribute('aria-expanded')).toBe('false')
  fireEvent.click(directory)
  expect(directory.getAttribute('aria-expanded')).toBe('true')
  expect((await screen.findByRole('link', { name: 'main.go' })).getAttribute('href')).toBe(
    '#/file?repo=r&path=src%2Fmain.go&ref=abc123',
  )
})
