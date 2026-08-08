import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import type { ServiceDetail } from '../api'
import { ModeContext, lightTheme } from '../theme'
import { ScopeContextBar } from './ScopeContextBar'
import { scopeParams } from '../scope'

const api = vi.hoisted(() => ({ fetchServiceDetail: vi.fn() }))
vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

afterEach(cleanup)
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
  },
  successors: [],
  memberships: [],
} as unknown as ServiceDetail

function mount(serviceKey = 'orders-api') {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          <ScopeContextBar
            scope={{ repository, serviceKey }}
            path="/services"
            params={new URLSearchParams({ repository, service_key: serviceKey })}
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
  it('preserves scope on its surface links', async () => {
    api.fetchServiceDetail.mockResolvedValue(detail)
    mount()
    const explorer = screen.getByRole('link', { name: 'Explorer' })
    expect(decodeURIComponent(explorer.getAttribute('href') ?? '')).toContain(`repository=${repository}`)
    expect(decodeURIComponent(explorer.getAttribute('href') ?? '')).toContain('service_key=orders-api')
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
    expect(scopeParams({ repository: 'r', serviceKey: '' })).toEqual({ repository: 'r' })
    expect(scopeParams(null)).toEqual({})
  })
})
