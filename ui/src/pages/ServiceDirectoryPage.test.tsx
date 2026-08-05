import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import ServiceDirectoryPage from './ServiceDirectoryPage'
import type {
  ServiceDetail,
  ServiceInventory,
  ServiceRecord,
  ServiceRepository,
} from '../api'

const api = vi.hoisted(() => ({
  fetchServiceInventory: vi.fn(),
  fetchServiceDetail: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const engine = new Client()
const repositoryName = 'example.invalid/neutral mono'

beforeEach(() => {
  window.location.hash = ''
  api.fetchServiceInventory.mockReset().mockResolvedValue(inventory())
  api.fetchServiceDetail.mockReset().mockResolvedValue(detail())
})

afterEach(cleanup)

test('renders exact authority, page summaries, lifecycle states, roles, and source-free detail', async () => {
  renderPage(new URLSearchParams({
    repository: repositoryName,
    include_removed: 'true',
    service_key: 'orders-api',
  }))

  expect(await screen.findByRole('heading', { name: 'Service directory' })).toBeTruthy()
  expect(screen.getByText('t335-demo')).toBeTruthy()
  expect(screen.getByText('2/4 accepted files')).toBeTruthy()
  expect(screen.getByText('Unowned files')).toBeTruthy()
  expect(screen.getByText('Shared roles · page')).toBeTruthy()
  expect(screen.getByRole('link', { name: /Orders API/ })).toBeTruthy()
  expect(screen.getByRole('link', { name: /Billing control/ })).toBeTruthy()
  expect(screen.getByRole('link', { name: /Legacy orders/ })).toBeTruthy()
  expect(screen.getAllByText('Stale').length).toBeGreaterThan(0)
  expect(screen.getAllByText('Conflict').length).toBeGreaterThan(0)
  expect(screen.getAllByText('Removed').length).toBeGreaterThan(0)
  expect(screen.getByText('Active generation is stale')).toBeTruthy()
  expect(screen.getByText('service/orders')).toBeTruthy()
  expect(decodeURIComponent(
    screen.getByRole('link', { name: 'Search this service' }).getAttribute('href') ?? '',
  )).toBe(
    '#/search?q=&scope=service&repository=example.invalid/neutral+mono&service_key=orders-api',
  )
  expect(screen.getByText(/Paths are authority identities/)).toBeTruthy()
  expect(api.fetchServiceInventory).toHaveBeenCalledWith({
    repository: repositoryName,
    status: undefined,
    disposition: undefined,
    include_removed: true,
    page_size: 50,
    cursor: undefined,
  }, expect.any(AbortSignal))
  expect(api.fetchServiceDetail).toHaveBeenCalledWith(
    repositoryName, 'orders-api', expect.any(AbortSignal),
  )
})

test('deep links retain filters and pagination while filter changes reset cursor and detail', async () => {
  api.fetchServiceInventory.mockResolvedValueOnce(inventory('opaque/next'))
  renderPage(new URLSearchParams({
    repository: repositoryName,
    status: 'stale',
    include_removed: 'true',
    cursor: 'opaque/current',
    service_key: 'orders-api',
  }))
  await screen.findByRole('heading', { name: 'Orders API' })

  const detailLink = screen.getByRole('link', { name: /Billing control/ })
  expect(decodeURIComponent(detailLink.getAttribute('href') ?? '')).toContain(
    'repository=example.invalid/neutral+mono&status=stale&include_removed=true&cursor=opaque/current&service_key=billing-control',
  )
  const next = screen.getByRole('link', { name: 'Next page' })
  expect(decodeURIComponent(next.getAttribute('href') ?? '')).toContain(
    'cursor=opaque/next',
  )
  const first = screen.getByRole('link', { name: 'First page' })
  expect(decodeURIComponent(first.getAttribute('href') ?? '')).toBe(
    '#/services?repository=example.invalid/neutral+mono&status=stale&include_removed=true',
  )

  fireEvent.change(screen.getByLabelText('Disposition'), {
    target: { value: 'conflict' },
  })
  expect(decodeURIComponent(window.location.hash)).toBe(
    '#/services?repository=example.invalid/neutral+mono&status=stale&disposition=conflict&include_removed=true',
  )
})

test('distinguishes a sparse empty scan from terminal empty state', async () => {
  api.fetchServiceInventory.mockResolvedValueOnce({
    ...inventory('scan/next'), services: [],
    pagination: { order: 'service_key:asc', page_size: 50, returned: 0, next_cursor: 'scan/next' },
  })
  renderPage(new URLSearchParams({ repository: repositoryName, status: 'current' }))
  expect(await screen.findByText('No matches in this scan window')).toBeTruthy()
  expect(screen.getByText(/empty page is not an absence claim/)).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Next page' })).toBeTruthy()
})

test('shows bounded errors and retries the same exact route', async () => {
  api.fetchServiceInventory
    .mockRejectedValueOnce(new Error('409: catalog changed'))
    .mockResolvedValueOnce(inventory())
  renderPage(new URLSearchParams({ repository: repositoryName }))
  expect(await screen.findByText('409: catalog changed')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() => expect(api.fetchServiceInventory).toHaveBeenCalledTimes(2))
  expect(await screen.findByRole('link', { name: /Orders API/ })).toBeTruthy()
})

test('keeps inventory usable when an exact detail deep link is unavailable', async () => {
  api.fetchServiceDetail.mockRejectedValueOnce(new Error('404: service not found'))
  renderPage(new URLSearchParams({
    repository: repositoryName,
    service_key: 'removed-service',
  }))
  expect(await screen.findByText('Service detail unavailable')).toBeTruthy()
  expect(screen.getByText('The inventory is still current')).toBeTruthy()
  expect(screen.getByText('404: service not found')).toBeTruthy()
  expect(screen.getByRole('link', { name: /Orders API/ })).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Clear selection' }).getAttribute('href')).toBe(
    '#/services?repository=example.invalid%2Fneutral+mono',
  )
  fireEvent.click(screen.getByRole('button', { name: 'Retry detail' }))
  await waitFor(() => expect(api.fetchServiceDetail).toHaveBeenCalledTimes(2))
})

test('bounds an oversized error body before rendering it', async () => {
  api.fetchServiceInventory.mockRejectedValueOnce(new Error(`500: ${'x'.repeat(700)}`))
  renderPage(new URLSearchParams({ repository: repositoryName }))
  const problem = await screen.findByText(/^500: x+/)
  expect(problem.textContent?.length).toBe(512)
  expect(problem.textContent?.endsWith('…')).toBe(true)
})

test('requires an exact repository deep link before any catalog request', () => {
  renderPage(new URLSearchParams())
  expect(screen.getByRole('heading', { name: 'Service directory' })).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Choose a repository' }).getAttribute('href')).toBe('#/repos')
  expect(api.fetchServiceInventory).not.toHaveBeenCalled()
  expect(api.fetchServiceDetail).not.toHaveBeenCalled()
})

function renderPage(params: URLSearchParams) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ServiceDirectoryPage params={params} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

function repository(): ServiceRepository {
  return {
    repository: repositoryName,
    source_kind: 'operator',
    source_commit: '1'.repeat(40),
    source_file_count: 4,
    accepted_file_count: 2,
    unowned_file_count: 2,
    authority: { kind: 'operator', id: 't335-demo', version: 'v1' },
    catalog_digest: `sha256:${'a'.repeat(64)}`,
    catalog_generation: `sha256:${'b'.repeat(64)}`,
    catalog_control_revision: 2,
    state_control_revision: 7,
    catalog_service_count: 3,
    live_service_count: 1,
    current_count: 0,
    stale_count: 1,
    unavailable_count: 0,
    conflict_count: 1,
    tombstone_count: 1,
    published_at: '2026-08-05T12:00:00Z',
    state_updated_at: '2026-08-05T12:01:00Z',
  }
}

function record(overrides: Partial<ServiceRecord> = {}): ServiceRecord {
  return {
    repository: repositoryName,
    key: 'orders-api',
    display_name: 'Orders API',
    disposition: 'accepted',
    origin: 'base',
    successor_count: 0,
    incarnation: 2,
    desired_generation: `sha256:${'c'.repeat(64)}`,
    desired_source_generation: `sha256:${'d'.repeat(64)}`,
    desired_catalog_generation: `sha256:${'e'.repeat(64)}`,
    active_desired_generation: `sha256:${'f'.repeat(64)}`,
    active_source_generation: `sha256:${'1'.repeat(64)}`,
    active_catalog_generation: `sha256:${'2'.repeat(64)}`,
    status: 'stale',
    removed: false,
    membership_count: 2,
    distinct_path_count: 2,
    role_counts: { primary: 1, supporting: 0, shared: 1, generated: 0, typed: 0 },
    state_digest: `sha256:${'3'.repeat(64)}`,
    control_revision: 4,
    changed_at: '2026-08-05T12:02:00Z',
    ...overrides,
  }
}

function inventory(nextCursor = ''): ServiceInventory {
  return {
    schema: 'phebs-service-inventory-v1',
    repository: repository(),
    filters: { repository: repositoryName, include_removed: true },
    services: [
      record(),
      record({
        key: 'billing-control', display_name: 'Billing control',
        disposition: 'conflict', status: 'conflict', reason: 'two claims',
        incarnation: 1, membership_count: 0, distinct_path_count: 0,
        role_counts: { primary: 0, supporting: 0, shared: 0, generated: 0, typed: 0 },
      }),
      record({
        key: 'legacy-orders', display_name: 'Legacy orders',
        disposition: 'rejected', status: 'removed', removed: true,
        reason: 'replaced', successor_count: 1, incarnation: 1,
        membership_count: 0, distinct_path_count: 0,
        role_counts: { primary: 0, supporting: 0, shared: 0, generated: 0, typed: 0 },
      }),
    ],
    pagination: {
      order: 'service_key:asc', page_size: 50, returned: 3,
      next_cursor: nextCursor || undefined,
    },
  }
}

function detail(): ServiceDetail {
  return {
    schema: 'phebs-service-detail-v1',
    repository: repository(),
    service: record(),
    successors: [],
    memberships: [
      { path: 'service/orders', role: 'primary', origin: 'base' },
      { path: 'go.mod', role: 'shared', origin: 'base' },
    ],
  }
}
