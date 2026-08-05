import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { BaseProvider, LightTheme } from 'baseui'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import SettingsPage from './SettingsPage'

const api = vi.hoisted(() => ({
  createAPIKey: vi.fn(),
  fetchAPIKeys: vi.fn(),
  fetchLifecycleStatus: vi.fn(),
  revokeAPIKey: vi.fn(),
}))

vi.mock('../api', () => api)

const engine = new Client()

function renderPage(isAdmin = false) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <SettingsPage isAdmin={isAdmin} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  api.fetchAPIKeys.mockReset().mockResolvedValue({
    keys: [
      {
        id: 'write-key',
        name: 'Investigation agent',
        prefix: 'phebs_write',
        capabilities: ['investigation:write'],
        created_at: '2026-07-27T12:00:00Z',
      },
      {
        id: 'read-key',
        name: 'Read-only client',
        prefix: 'phebs_read',
        capabilities: [],
        created_at: '2026-07-27T11:00:00Z',
      },
    ],
  })
  api.createAPIKey.mockReset().mockImplementation(
    async (name: string, capabilities: string[]) => ({
      key: {
        id: 'new-key',
        name,
        prefix: 'phebs_new',
        capabilities,
        created_at: '2026-07-27T13:00:00Z',
      },
      token: 'phebs_new.secret',
    }),
  )
  api.revokeAPIKey.mockReset().mockResolvedValue(undefined)
  api.fetchLifecycleStatus.mockReset().mockResolvedValue({
    schema: 'phebs-lifecycle-status-v1',
    policy: {
      enabled: true,
      owners: 13,
      soft_watermark_percent: 80,
      hard_watermark_percent: 90,
      resume_watermark_percent: 75,
      max_candidates_per_turn: 64,
      max_deletes_per_turn: 16,
      max_queries_per_turn: 16,
    },
    capacity: {
      completeness: 'exact',
      pressure: 'collect',
      total_bytes: 1_000,
      used_bytes: 810,
      used_percent: 81,
      observed_at: '2026-08-05T20:00:00Z',
    },
    owners: Array.from({ length: 13 }, (_, index) => ({
      name: `owner-${String(index).padStart(2, '0')}`,
      state: index < 4 ? 'ok' : 'not_run',
      completeness: index < 4 ? 'exact' : 'unavailable',
      scanned: index === 0 ? 11 : 0,
      deleted: index === 0 ? 1 : 0,
      backlog: index === 0,
    })),
  })
})

afterEach(cleanup)

test('lists reviewed capability names without secret material', async () => {
  renderPage()
  expect(await screen.findByText('Investigation agent')).toBeTruthy()
  expect(screen.getByText('investigation:write')).toBeTruthy()
  expect(screen.getByText(/read only/)).toBeTruthy()
  expect(screen.queryByText(/secret/)).toBeNull()
})

test('key creation is read-only unless Investigation write is explicit', async () => {
  const { unmount } = renderPage()
  await screen.findByText('Read-only client')
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), {
    target: { value: 'Default client' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  await waitFor(() => {
    expect(api.createAPIKey).toHaveBeenCalledWith('Default client', [])
  })
  expect(await screen.findByText(/Read-only key/)).toBeTruthy()

  unmount()
  renderPage()
  await screen.findByText('Read-only client')
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), {
    target: { value: 'Agent' },
  })
  fireEvent.click(
    screen.getByRole('checkbox', { name: /Allow Investigation writes/ }),
  )
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  await waitFor(() => {
    expect(api.createAPIKey).toHaveBeenLastCalledWith(
      'Agent',
      ['investigation:write'],
    )
  })
  expect(
    await screen.findByText(/This key can attempt durable Investigation mutations/),
  ).toBeTruthy()
  expect(
    screen.getByText(/Replace the key to change this authority/),
  ).toBeTruthy()
})

test('administrator sees bounded lifecycle pressure without owner content', async () => {
  renderPage(true)
  expect(await screen.findByRole('heading', { name: 'Lifecycle maintenance' })).toBeTruthy()
  expect(await screen.findByText('Collecting')).toBeTruthy()
  expect(screen.getByText('81% allocated (810 B of 1000 B)')).toBeTruthy()
  expect(screen.getByText('4 / 13')).toBeTruthy()
  expect(screen.getByText(/at most 64 candidates, 16 deletions, and 16 store queries/)).toBeTruthy()
  expect(screen.queryByText(/owner-00/)).toBeNull()
})
