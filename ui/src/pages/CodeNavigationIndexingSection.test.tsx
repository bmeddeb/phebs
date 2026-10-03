import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { BaseProvider } from 'baseui'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { lightTheme } from '../theme'
import { CodeNavigationIndexingSection } from './CodeNavigationIndexingSection'

const api = vi.hoisted(() => ({ fetchRepoStatus: vi.fn() }))
const typed = vi.hoisted(() => ({ fetchTypedProviders: vi.fn(), fetchTypedView: vi.fn(), planTypedIndex: vi.fn(), enqueueTypedIndex: vi.fn() }))
vi.mock('../api', () => api)
vi.mock('../typedIndex', () => typed)
const engine = new Client()
const digest = 'sha256:' + 'a'.repeat(64)
const repository = 'example.test/repo'
const view = { schema: 'phebs-typed-index-status-v1', repository, commit: 'a'.repeat(40), revision: digest, available: true, state: 'absent', provider: 'bazel-rules-go-scip-v1', profile: 'reduced', target_profile: 'reduced', config_profile: 'ordinary', resource_profile: 'native-arm64-bounded-v1', request_digest: '', job_state: '', current_commit: '', reason: '', checked_purpose: '' }
const providers = { schema: 'phebs-typed-index-providers-v1', providers: [{ id: view.provider, name: 'Bazel', available: true }, { id: 'go-module-scip-v1', name: 'Go module / workspace', available: false }, { id: 'imported-artifact-scip-v1', name: 'Existing artifact', available: false }] }
function renderSection() { return render(<StyletronProvider value={engine}><BaseProvider theme={lightTheme}><CodeNavigationIndexingSection /></BaseProvider></StyletronProvider>) }
beforeEach(() => {
  window.location.hash = '#/settings?repo=' + repository
  vi.clearAllMocks()
  api.fetchRepoStatus.mockResolvedValue([{ name: repository }])
  typed.fetchTypedProviders.mockResolvedValue(providers)
  typed.fetchTypedView.mockResolvedValue(view)
  typed.planTypedIndex.mockImplementation(async selection => ({ schema: 'phebs-typed-index-preview-v1', selection, commit: view.commit, request_digest: digest, idempotency_key: 'a'.repeat(64), resource_profile: view.resource_profile }))
  typed.enqueueTypedIndex.mockResolvedValue({ ...view, state: 'planning', request_digest: digest, job_state: 'pending' })
})
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

test('renders server-bound Bazel-first profiles and exact commit', async () => {
  renderSection()
  expect(await screen.findByText('01 · Bazel first')).toBeTruthy()
  expect(await screen.findByText(view.commit)).toBeTruthy()
  expect(screen.getByRole('combobox', { name: 'Indexing repository' })).toBeTruthy()
  expect(screen.getAllByRole('article')).toHaveLength(3)
  expect(screen.getByRole('button', { name: 'Review indexing plan' })).toBeTruthy()
})
test('unavailable build exposes no planning or enqueue action', async () => {
  typed.fetchTypedProviders.mockResolvedValue({ ...providers, providers: providers.providers.map(p => ({ ...p, available: false })) })
  typed.fetchTypedView.mockResolvedValue({ ...view, available: false, profile: '', provider: '', resource_profile: '' })
  renderSection()
  expect(await screen.findByText('No installed profile is available for this repository.')).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Review indexing plan' })).toBeNull()
  expect(typed.planTypedIndex).not.toHaveBeenCalled()
})
test('review precedes explicit enqueue and transport retry keeps exact identity', async () => {
  typed.enqueueTypedIndex.mockRejectedValueOnce(new Error('private details')).mockResolvedValueOnce({ ...view, state: 'planning' })
  renderSection()
  fireEvent.click(await screen.findByRole('button', { name: 'Review indexing plan' }))
  expect(await screen.findByText('Exact commit:', { exact: false })).toBeTruthy()
  expect(typed.enqueueTypedIndex).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Generate navigation' }))
  const retry = await screen.findByRole('button', { name: 'Retry exact request' })
  expect(screen.queryByText('private details')).toBeNull()
  fireEvent.click(retry)
  await waitFor(() => expect(typed.enqueueTypedIndex).toHaveBeenCalledTimes(2))
  expect(typed.enqueueTypedIndex.mock.calls[0][0]).toEqual(typed.enqueueTypedIndex.mock.calls[1][0])
})
test('retains confirmed active status on a poll failure and only polls the selected status', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  typed.fetchTypedView.mockResolvedValueOnce({ ...view, state: 'indexing' }).mockRejectedValueOnce(new Error('raw output'))
  renderSection()
  expect(await screen.findByText('indexing')).toBeTruthy()
  await vi.advanceTimersByTimeAsync(5100)
  expect(await screen.findByRole('alert')).toBeTruthy()
  expect(screen.getByText('indexing')).toBeTruthy()
  expect(typed.fetchTypedView).toHaveBeenCalledTimes(2)
  expect(typed.fetchTypedProviders).toHaveBeenCalledTimes(1)
  expect(api.fetchRepoStatus).toHaveBeenCalledTimes(1)
})
test.each(['absent', 'current', 'stale', 'planning', 'indexing', 'validating', 'publishing', 'failed', 'canceled'])('renders %s as text', async state => {
  typed.fetchTypedView.mockResolvedValue({ ...view, state })
  renderSection()
  expect(await screen.findByText(state)).toBeTruthy()
})
