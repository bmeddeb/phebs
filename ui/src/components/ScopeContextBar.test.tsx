import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useLayoutEffect } from 'react'
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
    repository,
    key: 'orders-api',
    status: 'stale',
    changed_at: '2026-08-07T10:00:00Z',
    active_desired_generation: 'gen-r4',
  },
  successors: [],
  memberships: [],
} as unknown as ServiceDetail

function view(serviceKey = 'orders-api', generation = '', principal = 'user-a@localhost.test') {
  return (
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          <ScopeContextBar
            scope={{ repository, serviceKey, generation }}
            path="/services"
            params={new URLSearchParams({ repository, service_key: serviceKey })}
            principal={principal}
          />
        </ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>
  )
}
const mount = (serviceKey = 'orders-api', generation = '') => render(view(serviceKey, generation))

describe('ScopeContextBar', () => {
  it.each(['billing-api', ''])('returning from scope %s waits for a fresh authority read', async intermediate => {
    api.fetchServiceDetail.mockResolvedValueOnce(detail).mockImplementation(() => new Promise(() => {}))
    const rendered = render(view())
    await screen.findByText('stale')
    rendered.rerender(view(intermediate))
    rendered.rerender(view())
    expect(screen.getByText('reading authority…')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Directory' }).getAttribute('href')).not.toContain('scope_generation')
  })
  it('does not carry the previous service generation during the first render of a new scope', async () => {
    api.fetchServiceDetail.mockResolvedValueOnce(detail).mockImplementation(() => new Promise(() => {}))
    const observed: string[] = []
    function Observe({ serviceKey }: { serviceKey: string }) {
      useLayoutEffect(() => { observed.push(screen.getByRole('link', { name: 'Directory' }).getAttribute('href') ?? '') }, [serviceKey])
      return view(serviceKey)
    }
    const rendered = render(<Observe serviceKey="orders-api" />)
    await screen.findByText('stale')
    rendered.rerender(<Observe serviceKey="billing-api" />)
    expect(observed.at(-1)).toBe('#/services?repository=local%2Ffixtures%2Ft307.bundle&service_key=billing-api')
  })
  it.each(['success', 'error'] as const)('ignores a late %s from an abandoned scope read', async outcome => {
    let finish!: (value: ServiceDetail) => void
    let fail!: (error: Error) => void
    api.fetchServiceDetail.mockImplementationOnce(() => new Promise<ServiceDetail>((resolve, reject) => { finish = resolve; fail = reject }))
      .mockImplementation(() => new Promise(() => {}))
    const rendered = render(view())
    const signal = api.fetchServiceDetail.mock.calls[0][2] as AbortSignal
    rendered.rerender(view('billing-api'))
    expect(signal.aborted).toBe(true)
    await act(async () => { if (outcome === 'success') finish(detail); else fail(new Error('old scope failure')) })
    expect(screen.getByText('reading authority…')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Directory' }).getAttribute('href')).not.toContain('scope_generation')
    expect(screen.queryByText('authority unavailable')).toBeNull()
  })
  it('refuses a service record from a different repository', async () => {
    api.fetchServiceDetail.mockResolvedValue({ ...detail, service: { ...detail.service, repository: 'foreign/repo' } })
    mount()
    expect(await screen.findByText('authority mismatch')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Directory' }).getAttribute('href')).not.toContain('scope_generation')
  })
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
