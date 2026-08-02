import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import ReposPage from './ReposPage'
import type { RepoStatus } from '../api'

const api = vi.hoisted(() => ({
  fetchRepoStatus: vi.fn(),
  postReindex: vi.fn(),
}))

vi.mock('../api', () => api)

const repos: RepoStatus[] = [
  { name: 'github.com/one/shared', clone_url: '', orphaned: false, last_index_job_state: 'unavailable' },
  { name: 'github.com/two/shared', clone_url: '', orphaned: false, last_index_job_state: 'unavailable' },
]

const engine = new Client()

beforeEach(() => {
  window.location.hash = ''
  api.fetchRepoStatus.mockReset().mockResolvedValue(repos)
  api.postReindex.mockReset().mockResolvedValue(undefined)
})

afterEach(cleanup)

test('reindex all queues every repository and refreshes status once', async () => {
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ReposPage isAdmin />
      </BaseProvider>
    </StyletronProvider>,
  )
  const button = await screen.findByRole('button', { name: 'Reindex all' })
  expect(api.fetchRepoStatus).toHaveBeenCalledTimes(1)

  fireEvent.click(button)
  await waitFor(() => expect(api.postReindex).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(api.fetchRepoStatus).toHaveBeenCalledTimes(2))
  expect(api.postReindex.mock.calls.map((call) => call[0])).toEqual([
    'github.com/one/shared',
    'github.com/two/shared',
  ])
})

test('repo search actions use anchored full-name filters', async () => {
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ReposPage />
      </BaseProvider>
    </StyletronProvider>,
  )
  const buttons = await screen.findAllByTitle('Search in this repo')
  fireEvent.click(buttons[1])
  expect(decodeURIComponent(window.location.hash)).toBe(
    '#/search?q=repo:"^github\\\\.com/two/shared$"+',
  )
  expect(screen.queryByRole('button', { name: 'Reindex all' })).toBeNull()
})

test('legacy job state is unavailable rather than never indexed', async () => {
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ReposPage isAdmin />
      </BaseProvider>
    </StyletronProvider>,
  )
  expect((await screen.findAllByText('status unavailable')).length).toBe(2)
  expect(screen.queryByText('never indexed')).toBeNull()
  expect(screen.getByText(/indexing status unavailable/)).toBeTruthy()
  expect(screen.queryByText(/0 indexing/)).toBeNull()
  for (const button of screen.getAllByRole('button', { name: 'Reindex' })) {
    expect((button as HTMLButtonElement).disabled).toBe(true)
  }
})
