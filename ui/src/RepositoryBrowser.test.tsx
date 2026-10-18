import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import { RepositoryTree } from './RepositoryBrowser'

const api = vi.hoisted(() => ({ fetchFolderContents: vi.fn() }))
vi.mock('./api', () => api)
const engine = new Client()
const revision = 'a-ref'
const tree = (repo = 'example.test/alpha') => <StyletronProvider value={engine}><BaseProvider theme={LightTheme}><RepositoryTree repo={repo} ref={revision} /></BaseProvider></StyletronProvider>
afterEach(() => { cleanup(); vi.restoreAllMocks(); api.fetchFolderContents.mockReset() })

test('nested folder retry keeps its exact path and revision and re-expansion reuses the result', async () => {
  api.fetchFolderContents.mockResolvedValueOnce({ entries: [{ name: 'src', type: 'dir' }] })
    .mockRejectedValueOnce(new Error('folder unavailable'))
    .mockResolvedValue({ entries: [{ name: 'main.go', type: 'file' }] })
  render(tree())
  const folder = await screen.findByRole('button', { name: 'src' })
  fireEvent.click(folder)
  fireEvent.click(await screen.findByRole('button', { name: 'Retry folder' }))
  const file = await screen.findByRole('link', { name: 'main.go' })
  expect(file.getAttribute('href')).toBe('#/file?repo=example.test%2Falpha&path=src%2Fmain.go&ref=a-ref')
  fireEvent.click(folder)
  fireEvent.click(folder)
  expect(screen.getByRole('link', { name: 'main.go' })).toBeTruthy()
  expect(api.fetchFolderContents.mock.calls.map(call => call.slice(0, 3))).toEqual([
    ['example.test/alpha', 'a-ref', ''],
    ['example.test/alpha', 'a-ref', 'src'],
    ['example.test/alpha', 'a-ref', 'src'],
  ])
})

test('a late folder response cannot populate another repository tree', async () => {
  let finish!: (value: { entries: { name: string; type: string }[] }) => void
  api.fetchFolderContents.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue({ entries: [{ name: 'beta.go', type: 'file' }] })
  const rendered = render(tree())
  const signal = api.fetchFolderContents.mock.calls[0][3] as AbortSignal
  rendered.rerender(tree('example.test/beta'))
  expect(await screen.findByRole('link', { name: 'beta.go' })).toBeTruthy()
  expect(signal.aborted).toBe(true)
  await act(async () => finish({ entries: [{ name: 'alpha.go', type: 'file' }] }))
  expect(screen.queryByRole('link', { name: 'alpha.go' })).toBeNull()
})
