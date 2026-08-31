import { cleanup, render, screen } from '@testing-library/react'
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

const page = (isAdmin = true) =>
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <AnalyticsPage isAdmin={isAdmin} />
      </BaseProvider>
    </StyletronProvider>,
  )

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

  const alert = await screen.findByRole('alert')
  expect(alert.textContent?.trim()).toBe(expected)
  expect(expected).toHaveLength(512)
  expect(document.body.textContent).not.toContain('Error: analytics unavailable')
})

test('non-admins get a notice, no fetch', () => {
  page(false)
  expect(screen.getByText(/requires administrator access/)).toBeTruthy()
  expect(api.fetchAnalytics).not.toHaveBeenCalled()
})
