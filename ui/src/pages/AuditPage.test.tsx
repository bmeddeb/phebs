import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import AuditPage from './AuditPage'
import type { AuditEvent } from '../api'

const api = vi.hoisted(() => ({
  fetchAudit: vi.fn(),
}))

vi.mock('../api', () => api)

const event = (i: number, extra: Partial<AuditEvent> = {}): AuditEvent => ({
  id: `e${i}`,
  action: 'auth.login',
  status: 200,
  actor_email: 'admin@example.com',
  created_at: '2026-07-11T12:00:00Z',
  ...extra,
})

const engine = new Client()

const view = (isAdmin = true) => (
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <AuditPage isAdmin={isAdmin} />
      </BaseProvider>
    </StyletronProvider>
  )
const page = (isAdmin = true) => render(view(isAdmin))

beforeEach(() => {
  api.fetchAudit.mockReset()
})

afterEach(cleanup)

test('renders events and pages with load more', async () => {
  api.fetchAudit
    .mockResolvedValueOnce({
      events: [event(1, { action: 'post-api-reindex', target: 'github.com/foo/bar' }), event(2)],
      has_more: true,
    })
    .mockResolvedValueOnce({ events: [event(3, { status: 401, actor_email: undefined })], has_more: false })

  page()
  expect(await screen.findByText('post-api-reindex')).toBeTruthy()
  expect(screen.getByText('github.com/foo/bar')).toBeTruthy()
  expect(api.fetchAudit).toHaveBeenCalledWith(0, 50, expect.anything())

  fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
  await waitFor(() => expect(api.fetchAudit).toHaveBeenCalledTimes(2))
  expect(api.fetchAudit.mock.calls[1][0]).toBe(2) // offset = events loaded so far
  expect(await screen.findByText('401')).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
})

test('load more dedupes rows shifted into the next page by new events', async () => {
  api.fetchAudit
    .mockResolvedValueOnce({ events: [event(1), event(2)], has_more: true })
    // a new event arrived server-side, shifting event 2 into page two
    .mockResolvedValueOnce({ events: [event(2), event(3)], has_more: false })

  page()
  await screen.findAllByText('auth.login')
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
  await waitFor(() => expect(api.fetchAudit).toHaveBeenCalledTimes(2))
  // header row + 3 unique events — the duplicate e2 must not render twice
  await waitFor(() => expect(screen.getAllByRole('row')).toHaveLength(4))
})

test('non-admins get a notice, no fetch', () => {
  page(false)
  expect(screen.getByText(/requires administrator access/)).toBeTruthy()
  expect(api.fetchAudit).not.toHaveBeenCalled()
})

test('paging advances by received rows even when a shifted page is deduplicated', async () => {
  api.fetchAudit
    .mockResolvedValueOnce({ events: [event(1), event(2)], has_more: true })
    .mockResolvedValueOnce({ events: [event(2), event(3)], has_more: true })
    .mockResolvedValueOnce({ events: [event(4)], has_more: false })
  page()
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
  await waitFor(() => expect(screen.getAllByRole('row')).toHaveLength(4))
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
  await waitFor(() => expect(screen.getAllByRole('row')).toHaveLength(5))
  expect(api.fetchAudit.mock.calls.map(call => call[0])).toEqual([0, 2, 4])
})

test('initial and append failures retry the failed offset without automatic requests', async () => {
  api.fetchAudit
    .mockRejectedValueOnce(new Error('unavailable'))
    .mockResolvedValueOnce({ events: [event(1), event(2)], has_more: true })
    .mockRejectedValueOnce(new Error('next page unavailable'))
    .mockResolvedValueOnce({ events: [event(3)], has_more: false })
  page()
  fireEvent.click(await screen.findByRole('button', { name: 'Retry audit page' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
  const retry = await screen.findByRole('button', { name: 'Retry audit page' })
  expect(screen.getAllByRole('row')).toHaveLength(3)
  expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
  expect(api.fetchAudit).toHaveBeenCalledTimes(3)
  fireEvent.click(retry)
  await waitFor(() => expect(screen.getAllByRole('row')).toHaveLength(4))
  expect(api.fetchAudit.mock.calls.map(call => call[0])).toEqual([0, 0, 2, 2])
  expect(screen.queryByRole('alert')).toBeNull()
})

test.each(['success', 'failure'])('access loss aborts the latest page and fences its late %s', async outcome => {
  let resolve!: (value: unknown) => void
  let reject!: (reason: Error) => void
  api.fetchAudit
    .mockResolvedValueOnce({ events: [event(1)], has_more: true })
    .mockImplementationOnce(() => new Promise((yes, no) => { resolve = yes; reject = no }))
    .mockResolvedValueOnce({ events: [event(9, { action: 'current session' })], has_more: false })
  const mounted = page()
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
  const signal = api.fetchAudit.mock.calls[1][2] as AbortSignal
  mounted.rerender(view(false))
  expect(signal.aborted).toBe(true)
  mounted.rerender(view(true))
  await screen.findByText('current session')
  await act(async () => {
    if (outcome === 'success') resolve({ events: [event(2, { action: 'obsolete session' })], has_more: true })
    else reject(new Error('obsolete failure'))
  })
  expect(screen.queryByText('auth.login')).toBeNull()
  expect(screen.queryByText('obsolete session')).toBeNull()
  expect(screen.queryByText('obsolete failure')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
})

test('audit failures use the shared bounded error display', async () => {
  const detail = 'x'.repeat(600)
  api.fetchAudit.mockRejectedValueOnce(new Error(detail))
  page()
  expect(await screen.findByText(`${detail.slice(0, 511)}…`, { exact: true })).toBeTruthy()
})

test('audit table scrolling is a named keyboard stop', async () => {
  api.fetchAudit.mockResolvedValueOnce({ events: [event(1)], has_more: false })
  page()
  await screen.findByText('auth.login')
  expect(screen.getByRole('region', { name: 'Audit events' }).tabIndex).toBe(0)
})
