import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import FilePage from './FilePage'
import type { SourceFile, TreeEntry } from '../api'
import { PaletteContext } from '../theme'
import type { PaletteName } from '../palette'

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
const highlighting = vi.hoisted(() => ({
  highlightStyle: vi.fn(() => ({})),
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
  highlightStyle: highlighting.highlightStyle,
}))

const engine = new Client()
const view = (params: string, palette: PaletteName = 'phebs') => (
  <StyletronProvider value={engine}>
    <BaseProvider theme={LightTheme}>
      <PaletteContext.Provider value={{ palette, setPalette: () => {} }}>
        <FilePage params={new URLSearchParams(params)} />
      </PaletteContext.Provider>
    </BaseProvider>
  </StyletronProvider>
)

beforeEach(() => {
  api.sourceCalls = []
  api.folderCalls = []
  api.folders = new Map()
  api.statuses = []
  highlighting.highlightStyle.mockClear()
})

afterEach(cleanup)

test('late source responses cannot overwrite the current route', async () => {
  const rendered = render(view('repo=r&path=old.go&ref=old-ref'))
  const help = screen.getByRole('button', { name: 'Help for File' })
  fireEvent.click(help)
  const helpDialog = await screen.findByRole('dialog', { name: 'File help' })
  expect(within(helpDialog).getByText('Cited source or history that may inform how the change is implemented.')).toBeTruthy()
  expect(within(helpDialog).queryByText('Implementation evidence is unavailable because search, code navigation, and history capabilities are not available.')).toBeNull()
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

test('re-colors the mounted source viewer without refetching the file (T44.2)', async () => {
  const rendered = render(view('repo=r&path=main.go&ref=abc123'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: 'package main', encoding: 'utf8', size: 12 })
  })
  await waitFor(() => expect(highlighting.highlightStyle).toHaveBeenCalledWith('light', 'phebs'))
  expect(api.sourceCalls).toHaveLength(1)

  rendered.rerender(view('repo=r&path=main.go&ref=abc123', 'classic'))

  await waitFor(() => expect(highlighting.highlightStyle).toHaveBeenCalledWith('light', 'classic'))
  expect(api.sourceCalls).toHaveLength(1)
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

test('markdown files default to source and offer a preview toggle (T44.3)', async () => {
  render(view('repo=r&path=docs%2FREADME.md&ref=abc123'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: '# Title\n\ntext', encoding: 'utf8', size: 13 })
  })
  // Source is the default: the toggle is present, Markdown active.
  const markdownTab = screen.getByRole('link', { name: 'Markdown' })
  const previewTab = screen.getByRole('link', { name: 'Preview' })
  expect(markdownTab.getAttribute('aria-current')).toBe('true')
  expect(previewTab.getAttribute('aria-current')).toBeNull()
  expect(previewTab.getAttribute('href')).toBe('#/file?repo=r&path=docs%2FREADME.md&ref=abc123&view=preview')
  // Default view shows raw source, not rendered HTML.
  expect(screen.queryByRole('heading', { name: 'Title' })).toBeNull()
})

test('preview renders sanitized markdown; non-markdown files get no toggle (T44.3)', async () => {
  render(view('repo=r&path=docs%2FREADME.md&ref=abc123&view=preview'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: '# Rendered\n\n[x](javascript:alert(1))', encoding: 'utf8', size: 30 })
  })
  expect(await screen.findByRole('heading', { name: 'Rendered' })).toBeTruthy()
  // The dangerous link did not survive the render boundary.
  expect(document.body.innerHTML).not.toContain('javascript:')
  cleanup()

  render(view('repo=r&path=main.go&ref=abc123'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: 'package main', encoding: 'utf8', size: 12 })
  })
  expect(screen.queryByRole('link', { name: 'Preview' })).toBeNull()
})

test('a line deep-link forces source even under view=preview (T44.3)', async () => {
  render(view('repo=r&path=docs%2FREADME.md&ref=abc123&view=preview&L=3'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: '# Title\n\ntext', encoding: 'utf8', size: 13 })
  })
  // ?L= is a source concept; preview is suppressed and Markdown stays active.
  expect(screen.queryByRole('heading', { name: 'Title' })).toBeNull()
  expect(screen.getByRole('link', { name: 'Markdown' }).getAttribute('aria-current')).toBe('true')
})

test('a document over the preview ceiling offers source instead of freezing (T44.3f)', async () => {
  const huge = '# Heading\n\n' + 'x'.repeat(140_000)
  render(view('repo=r&path=docs%2FBIG.md&ref=abc123&view=preview'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: huge, encoding: 'utf8', size: huge.length })
  })
  // No rendered heading — the bound short-circuits before marked runs.
  expect(screen.queryByRole('heading', { name: 'Heading' })).toBeNull()
  expect(screen.getByText(/preview is bounded to 128 KB/)).toBeTruthy()
})

test('preview markup is scoped — no global bare-tag styles leak (T44.3f)', async () => {
  render(view('repo=r&path=docs%2FREADME.md&ref=abc123&view=preview'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: '# Title\n\ntext', encoding: 'utf8', size: 13 })
  })
  await screen.findByRole('heading', { name: 'Title' })
  // The rendered heading lives under the fixed scoping wrapper class.
  const heading = screen.getByRole('heading', { name: 'Title' })
  expect(heading.closest('.phebs-md')).not.toBeNull()
})

vi.mock('../mermaid', () => ({
  renderMermaid: vi.fn(async (source: string) => {
    if (source.includes('boom')) throw new Error('Parse error on line 1')
    return '<svg data-testid="rendered-diagram"><g></g></svg>'
  }),
}))

test('a mermaid fence renders as a diagram; the source shows while loading (T44.4)', async () => {
  render(view('repo=r&path=docs%2FD.md&ref=abc123&view=preview'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: '# D\n\n```mermaid\ngraph TD\nA-->B\n```', encoding: 'utf8', size: 40 })
  })
  const diagram = await screen.findByRole('img', { name: 'Mermaid diagram' })
  expect(diagram.querySelector('svg')).not.toBeNull()
  // Prose around the fence rendered too.
  expect(screen.getByRole('heading', { name: 'D' })).toBeTruthy()
})

test('a failing fence keeps its source with a one-line error — never a blank (T44.4)', async () => {
  render(view('repo=r&path=docs%2FE.md&ref=abc123&view=preview'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: '```mermaid\nboom this is not a diagram\n```', encoding: 'utf8', size: 42 })
  })
  expect(await screen.findByText(/Diagram not rendered: Parse error on line 1/)).toBeTruthy()
  expect(screen.getByText(/boom this is not a diagram/)).toBeTruthy()
  expect(screen.queryByRole('img', { name: 'Mermaid diagram' })).toBeNull()
})

test('beyond the diagram cap, excess fences stay source and never call the renderer (T44.4f)', async () => {
  const mermaidMod = await import('../mermaid')
  const renderSpy = mermaidMod.renderMermaid as unknown as { mock: { calls: unknown[] } }
  const before = renderSpy.mock.calls.length
  // 22 distinct fences; only the first MAX_RENDERED_DIAGRAMS (20) may render.
  const fences = Array.from({ length: 22 }, (_, i) => `\`\`\`mermaid\ngraph TD\nN${i}-->M${i}\n\`\`\``).join('\n\n')
  render(view('repo=r&path=docs%2FMANY.md&ref=abc123&view=preview'))
  await act(async () => {
    api.sourceCalls[0].resolve({ content: fences, encoding: 'utf8', size: fences.length })
  })
  // The 2 excess fences stay source with the cap note (proves the cap fired).
  await waitFor(() => expect(screen.getAllByText(/preview renders at most 20 diagrams/).length).toBe(2))
  // Security invariant: the renderer is invoked at most 20 times for this
  // document — the excess fences never import or call mermaid.
  expect(renderSpy.mock.calls.length - before).toBeLessThanOrEqual(20)
})
