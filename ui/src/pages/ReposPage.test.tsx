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
  const button = await screen.findByRole('button', { name: 'Reindex all scopes' })
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
  expect(screen.queryByRole('button', { name: 'Reindex all scopes' })).toBeNull()
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
  for (const button of screen.getAllByRole('button', { name: 'Reindex whole repository' })) {
    expect((button as HTMLButtonElement).disabled).toBe(true)
  }
})

test('reindex controls name a focused unit instead of implying a whole-repository rebuild', async () => {
  api.fetchRepoStatus.mockResolvedValueOnce([{
    ...repos[0],
    last_index_job_state: 'exact',
    last_index_job: {
      target: repos[0].name,
      status: 'done',
      created_at: '2026-08-02T00:00:00Z',
      finished_at: '2026-08-02T00:00:00Z',
    },
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'checkout-api',
      digest: `sha256:${'a'.repeat(64)}`,
      primary_paths: ['services/checkout/**'],
      supporting_paths: ['pkg/shared/**'],
      primary_path_count: 1,
      supporting_path_count: 1,
      search_index_posture: 'focused',
      typed_index_posture: 'unit-bound',
      typed_index: { kind: 'scip', path: 'index.scip' },
    },
  } satisfies RepoStatus])

  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ReposPage isAdmin />
      </BaseProvider>
    </StyletronProvider>,
  )

  const button = await screen.findByRole('button', { name: 'Reindex checkout-api unit' })
  expect(button.getAttribute('title')).toBe('Reindex checkout-api unit')
  fireEvent.click(button)
  await waitFor(() => expect(api.postReindex).toHaveBeenCalledWith(repos[0].name, true))
})

test('reindex controls preserve a retained whole-repository posture', async () => {
  api.fetchRepoStatus.mockResolvedValueOnce([{
    ...repos[0],
    last_index_job_state: 'exact',
    last_index_job: {
      target: repos[0].name,
      status: 'done',
      created_at: '2026-08-02T00:00:00Z',
      finished_at: '2026-08-02T00:00:00Z',
    },
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'legacy-service',
      digest: `sha256:${'7'.repeat(64)}`,
      primary_paths: ['services/legacy/**'],
      supporting_paths: [],
      primary_path_count: 1,
      supporting_path_count: 0,
      search_index_posture: 'whole-repository',
      typed_index_posture: 'repository-root-unbound',
    },
  } satisfies RepoStatus])

  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ReposPage isAdmin />
      </BaseProvider>
    </StyletronProvider>,
  )

  expect(await screen.findByRole('button', {
    name: 'Reindex whole repository',
  })).toBeTruthy()
  expect(screen.queryByRole('button', {
    name: 'Reindex legacy-service unit',
  })).toBeNull()
})
