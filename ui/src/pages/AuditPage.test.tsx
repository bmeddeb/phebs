import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
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

const page = (isAdmin = true) =>
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <AuditPage isAdmin={isAdmin} />
      </BaseProvider>
    </StyletronProvider>,
  )

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

test('non-admins get a notice, no fetch', () => {
  page(false)
  expect(screen.getByText(/requires administrator access/)).toBeTruthy()
  expect(api.fetchAudit).not.toHaveBeenCalled()
})
