import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { BaseProvider, LightTheme } from 'baseui'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import SettingsPage from './SettingsPage'

const api = vi.hoisted(() => ({
  createAPIKey: vi.fn(),
  fetchAPIKeys: vi.fn(),
  revokeAPIKey: vi.fn(),
}))

vi.mock('../api', () => api)

const engine = new Client()

function renderPage() {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <SettingsPage />
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
