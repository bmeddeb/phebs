import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import AnalyticsPage from './AnalyticsPage'
import type { AnalyticsSummary } from '../api'

const api = vi.hoisted(() => ({
  fetchAnalytics: vi.fn(),
}))

vi.mock('../api', () => api)

const summary: AnalyticsSummary = {
  total_searches: 42,
  avg_duration_ms: 17,
  daily: [
    { date: '2026-07-10', count: 0 },
    { date: '2026-07-11', count: 5 },
  ],
  top_repos: [
    { name: 'github.com/foo/bar', count: 30 },
    { name: 'github.com/baz/qux', count: 12 },
  ],
}

const engine = new Client()

const view = (isAdmin = true) => (
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <AnalyticsPage isAdmin={isAdmin} />
      </BaseProvider>
    </StyletronProvider>
  )
const page = (isAdmin = true) => render(view(isAdmin))

beforeEach(() => {
  api.fetchAnalytics.mockReset().mockResolvedValue(summary)
})

afterEach(cleanup)

test('renders tiles, daily bars, and top repos from local data', async () => {
  page()
  expect(await screen.findByText('42')).toBeTruthy() // total searches tile
  expect(screen.getByText('17 ms')).toBeTruthy()
  expect(api.fetchAnalytics).toHaveBeenCalledWith(30, expect.anything())

  expect(screen.getByText('5')).toBeTruthy() // today tile from the last daily bucket
  const bar = screen.getByTestId('bar-2026-07-11')
  expect(bar.getAttribute('title')).toContain('5 searches')
  const empty = screen.getByTestId('bar-2026-07-10')
  expect(empty.getAttribute('title')).toContain('0 searches')

  expect(screen.getByText('github.com/foo/bar')).toBeTruthy()
  expect(screen.getByText('30')).toBeTruthy()
})

test('bounds analytics request failures', async () => {
  const oversized = `analytics unavailable ${'x'.repeat(600)}`
  const expected = `${oversized.slice(0, 511)}…`
  api.fetchAnalytics.mockRejectedValueOnce(new Error(oversized))

  page()

  expect(await screen.findByText(expected, { exact: true })).toBeTruthy()
  expect(expected).toHaveLength(512)
  expect(document.body.textContent).not.toContain('Error: analytics unavailable')
})

test('non-admins get a notice, no fetch', () => {
  page(false)
  expect(screen.getByText(/requires administrator access/)).toBeTruthy()
  expect(api.fetchAnalytics).not.toHaveBeenCalled()
})

test('labels the returned UTC bucket instead of inventing a current-day count', async () => {
  page()
  expect(await screen.findByText('Searches on 2026-07-11 (UTC)')).toBeTruthy()
  expect(screen.queryByText('Searches today')).toBeNull()
})

test('a missing daily slice is unavailable rather than a measured zero', async () => {
  api.fetchAnalytics.mockResolvedValueOnce({ ...summary, daily: null })
  page()
  expect(await screen.findByText('Unavailable')).toBeTruthy()
  expect(screen.getByText('No daily counts returned.')).toBeTruthy()
})

test('failed analytics can retry exactly the 30-day read', async () => {
  api.fetchAnalytics.mockRejectedValueOnce(new Error('unavailable'))
  page()
  const retry = await screen.findByRole('button', { name: 'Retry analytics' })
  expect(api.fetchAnalytics).toHaveBeenCalledTimes(1)
  expect(screen.queryByText('42')).toBeNull()
  fireEvent.click(retry)
  expect(await screen.findByText('42')).toBeTruthy()
  expect(api.fetchAnalytics.mock.calls.map(call => call[0])).toEqual([30, 30])
  expect(screen.queryByRole('alert')).toBeNull()
})

test.each(['success', 'failure'])('access loss clears private analytics and fences a late %s', async outcome => {
  let resolve!: (value: unknown) => void
  let reject!: (reason: Error) => void
  api.fetchAnalytics
    .mockImplementationOnce(() => new Promise((yes, no) => { resolve = yes; reject = no }))
    .mockRejectedValueOnce(new Error('current session unavailable'))
  const mounted = page()
  const signal = api.fetchAnalytics.mock.calls[0][1] as AbortSignal
  mounted.rerender(view(false))
  expect(signal.aborted).toBe(true)
  mounted.rerender(view(true))
  await screen.findByText('current session unavailable')
  await act(async () => {
    if (outcome === 'success') resolve(summary)
    else reject(new Error('obsolete failure'))
  })
  expect(screen.queryByText('42')).toBeNull()
  expect(screen.queryByText('obsolete failure')).toBeNull()
  expect(screen.getByRole('button', { name: 'Retry analytics' })).toBeTruthy()
})
