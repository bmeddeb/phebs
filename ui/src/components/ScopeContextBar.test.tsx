import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import type { ServiceDetail } from '../api'
import { ModeContext, lightTheme } from '../theme'
import { ScopeContextBar } from './ScopeContextBar'
import { recentScopes, scopeParams } from '../scope'

const api = vi.hoisted(() => ({ fetchServiceDetail: vi.fn() }))
vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

afterEach(cleanup)

const storage = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (key: string) => storage.get(key) ?? null,
  setItem: (key: string, value: string) => storage.set(key, value),
  removeItem: (key: string) => storage.delete(key),
  clear: () => storage.clear(),
})
beforeEach(() => {
  window.location.hash = ''
  api.fetchServiceDetail.mockReset()
})

const engine = new Styletron()
const repository = 'local/fixtures/t307.bundle'

const detail = {
  schema: 'phebs-service-detail-v1',
  repository: { repository },
  service: {
    key: 'orders-api',
    status: 'stale',
    changed_at: '2026-08-07T10:00:00Z',
    active_desired_generation: 'gen-r4',
  },
  successors: [],
  memberships: [],
} as unknown as ServiceDetail

function mount(serviceKey = 'orders-api', generation = '') {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          <ScopeContextBar
            scope={{ repository, serviceKey, generation }}
            path="/services"
            params={new URLSearchParams({ repository, service_key: serviceKey })}
            principal="user-a@localhost.test"
          />
        </ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>,
  )
}

describe('ScopeContextBar', () => {
  it('renders the scope identities and its catalog authority with as-of time', async () => {
    api.fetchServiceDetail.mockResolvedValue(detail)
    mount()
    expect(screen.getByRole('region', { name: 'Active scope' })).toBeTruthy()
    expect(screen.getByText('orders-api')).toBeTruthy()
    await waitFor(() => expect(screen.getByText('stale')).toBeTruthy())
    expect(screen.getByText(/as of/)).toBeTruthy()
  })
  it('fails closed on an authority read error — unavailable, never invented', async () => {
    api.fetchServiceDetail.mockRejectedValue(new Error('boom'))
    mount()
    await waitFor(() => expect(screen.getByText('authority unavailable')).toBeTruthy())
  })
  it('preserves scope on its surface links, carrying the verified generation', async () => {
    api.fetchServiceDetail.mockResolvedValue(detail)
    mount()
    await waitFor(() => expect(screen.getByText('stale')).toBeTruthy())
    const explorer = screen.getByRole('link', { name: 'Explorer' })
    const explorerHref = decodeURIComponent(explorer.getAttribute('href') ?? '')
    expect(explorerHref).toContain(`repository=${repository}`)
    expect(explorerHref).toContain('service_key=orders-api')
    expect(explorerHref).toContain('scope_generation=gen-r4')
    // The Workbench consumes scope under its own param names.
    const workbench = decodeURIComponent(screen.getByRole('link', { name: 'Workbench' }).getAttribute('href') ?? '')
    expect(workbench).toContain(`service_repository=${repository}`)
    expect(workbench).toContain('source_service=orders-api')
  })
  it('renders authority moved when the pinned generation no longer matches', async () => {
    api.fetchServiceDetail.mockResolvedValue(detail)
    mount('orders-api', 'gen-r3')
    await waitFor(() => expect(screen.getByText('authority moved')).toBeTruthy())
    expect(screen.getByTitle(/URL pins generation gen-r3; the active identity is now gen-r4/)).toBeTruthy()
  })
  it('fails closed on a response that is not the scoped service', async () => {
    api.fetchServiceDetail.mockResolvedValue({
      ...detail,
      service: { ...(detail as { service: object }).service, key: 'other-service' },
    } as unknown as ServiceDetail)
    mount()
    await waitFor(() => expect(screen.getByText('authority mismatch')).toBeTruthy())
  })
  it('records the scope as recent only after the authorized read succeeds', async () => {
    storage.clear()
    api.fetchServiceDetail.mockRejectedValue(new Error('denied'))
    mount()
    await waitFor(() => expect(screen.getByText('authority unavailable')).toBeTruthy())
    expect(recentScopes('user-a@localhost.test')).toEqual([])
    cleanup()
    api.fetchServiceDetail.mockResolvedValue(detail)
    mount()
    await waitFor(() => expect(screen.getByText('stale')).toBeTruthy())
    expect(recentScopes('user-a@localhost.test').map((entry) => entry.repository)).toEqual([repository])
  })
  it('clears scope explicitly, removing only scope params from the URL', async () => {
    api.fetchServiceDetail.mockResolvedValue(detail)
    mount()
    fireEvent.click(screen.getByRole('button', { name: 'Clear scope' }))
    expect(window.location.hash).toBe('#/services')
  })
})

describe('scopeParams', () => {
  it('appends only present scope fields', () => {
    expect(scopeParams({ repository: 'r', serviceKey: '', generation: '' })).toEqual({ repository: 'r' })
    expect(scopeParams(null)).toEqual({})
  })
})
