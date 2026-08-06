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
  ServiceRelationshipCitation,
  ServiceRelationshipPage,
  ServiceRelationshipView,
  ServiceRepository,
} from '../api'

const api = vi.hoisted(() => ({
  fetchServiceInventory: vi.fn(),
  fetchServiceDetail: vi.fn(),
  fetchServiceRelationships: vi.fn(),
  fetchServiceRelationshipCitation: vi.fn(),
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
  api.fetchServiceRelationships.mockReset().mockImplementation(
    ({ view }: { view: ServiceRelationshipView }) => Promise.resolve(relationshipPage(view)),
  )
  api.fetchServiceRelationshipCitation.mockReset().mockResolvedValue(relationshipCitation())
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

test('links exact relationship counts to fenced rows and reads immutable citations', async () => {
  api.fetchServiceDetail.mockResolvedValueOnce({
    ...detail(),
    service: record({
      status: 'current',
      active_desired_generation: `sha256:${'c'.repeat(64)}`,
    }),
  })
  renderPage(new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
  }), true)

  expect(await screen.findByRole('heading', { name: 'Exact relationship overview' })).toBeTruthy()
  await waitFor(() => expect(api.fetchServiceRelationships).toHaveBeenCalledTimes(3))
  expect(screen.getByRole('link', { name: /Contracts used & dependencies3/ })).toBeTruthy()
  expect(screen.getByRole('link', { name: /Contracts provided, callers & dependents2/ })).toBeTruthy()
  expect(screen.getByRole('link', { name: /Topics1/ })).toBeTruthy()
  expect(screen.getByText(/Current · all views share the current service authority/)).toBeTruthy()
  expect(screen.getAllByText('payments').length).toBeGreaterThan(0)
  expect(screen.getByText('Shared')).toBeTruthy()
  expect(screen.getByText('Unowned')).toBeTruthy()
  expect(screen.getByText('Ambiguous')).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Next exact page' }).getAttribute('href')).toContain(
    'relationship_cursor=next%2Fdependencies',
  )

  fireEvent.click(screen.getAllByRole('button', { name: 'View citation' })[0])
  expect(await screen.findByRole('dialog', { name: 'Exact source citation' })).toBeTruthy()
  expect(screen.getByText('client.Call(ctx, request)')).toBeTruthy()
  expect(api.fetchServiceRelationshipCitation).toHaveBeenCalledWith(
    'citation-dependencies-0', expect.any(AbortSignal),
  )

  fireEvent.click(screen.getByRole('button', { name: 'Close citation' }))
  api.fetchServiceRelationshipCitation.mockResolvedValueOnce({
    ...relationshipCitation(),
    root_digest: `sha256:${'9'.repeat(64)}`,
    content: 'forged content',
  })
  fireEvent.click(screen.getAllByRole('button', { name: 'View citation' })[1])
  expect(await screen.findByText(/citation response authority differs/)).toBeTruthy()
  expect(screen.queryByText('forged content')).toBeNull()
})

test('keeps unsupported, failed, empty, and mixed-generation relationship states distinct', async () => {
  const first = renderPage(new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
  }))
  expect(await screen.findByText(/Unsupported · relationship runtime is not registered/)).toBeTruthy()
  first.unmount()

  api.fetchServiceRelationships.mockImplementation(
    ({ view }: { view: ServiceRelationshipView }) => Promise.resolve({
      ...relationshipPage(view),
      rows: [],
      rows_state: view === 'dependencies' ? 'gap' : 'empty',
      roots: view === 'dependencies'
        ? [{ ...relationshipPage(view).roots[0], state: 'failed', reason: 'service_limit' }]
        : relationshipPage(view).roots,
      coverage: {
        ...relationshipPage(view).coverage,
        scanned_references: 0,
        returned_rows: 0,
        failed_roots: view === 'dependencies' ? 1 : 0,
        complete_roots: view === 'dependencies' ? 0 : 1,
      },
      pagination: { ...relationshipPage(view).pagination, returned: 0, next_cursor: undefined },
    }),
  )
  const second = renderPage(new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
  }), true)
  expect(await screen.findByText(/Failed · service_limit/)).toBeTruthy()
  expect(screen.getByText(/root is unavailable or failed/)).toBeTruthy()
  second.unmount()

  api.fetchServiceRelationships.mockImplementation(
    ({ view }: { view: ServiceRelationshipView }) => Promise.resolve({
      ...relationshipPage(view),
      rows: [], rows_state: 'empty',
      roots: [{ ...relationshipPage(view).roots[0], generation: view === 'topics' ? `sha256:${'9'.repeat(64)}` : `sha256:${'4'.repeat(64)}` }],
      coverage: { ...relationshipPage(view).coverage, scanned_references: 0, returned_rows: 0 },
      pagination: { ...relationshipPage(view).pagination, returned: 0, next_cursor: undefined },
    }),
  )
  renderPage(new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
  }), true)
  expect(await screen.findByText(/Failed · relationship views crossed publication identities/)).toBeTruthy()
  expect(screen.getByText(/Exact empty publication/)).toBeTruthy()
})

test('reuses three first-page bindings across tabs and advances only the selected cursor', async () => {
  const initial = new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
  })
  const rendered = renderPage(initial, true)
  await waitFor(() => expect(api.fetchServiceRelationships).toHaveBeenCalledTimes(3))

  rendered.rerender(pageView(new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
    relationship_view: 'callers',
  }), true))
  expect(await screen.findByRole('heading', { name: 'Contracts provided, callers & dependents' })).toBeTruthy()
  expect(api.fetchServiceRelationships).toHaveBeenCalledTimes(3)

  rendered.rerender(pageView(new URLSearchParams({
    repository: repositoryName,
    service_key: 'orders-api',
    relationship_view: 'callers',
    relationship_cursor: 'bound/caller-page',
  }), true))
  await waitFor(() => expect(api.fetchServiceRelationships).toHaveBeenCalledTimes(4))
  expect(api.fetchServiceRelationships).toHaveBeenLastCalledWith({
    repository: repositoryName,
    service_key: 'orders-api',
    view: 'callers',
    page_size: 25,
    cursor: 'bound/caller-page',
  }, expect.any(AbortSignal))
})

function renderPage(params: URLSearchParams, relationshipsAvailable = false) {
  return render(pageView(params, relationshipsAvailable))
}

function pageView(params: URLSearchParams, relationshipsAvailable = false) {
  return (
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ServiceDirectoryPage params={params} relationshipsAvailable={relationshipsAvailable} />
      </BaseProvider>
    </StyletronProvider>
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

function relationshipPage(view: ServiceRelationshipView): ServiceRelationshipPage {
  const counts = { dependencies: 3, callers: 2, topics: 1 }
  const rows = view === 'dependencies'
    ? [relationshipRow(view, 0, 'shared'), relationshipRow(view, 1, 'unowned'), relationshipRow(view, 2, 'ambiguous')]
    : [relationshipRow(view, 0, 'owned')]
  return {
    schema: 'phebs-service-relationships-v1',
    query: { repositories: [repositoryName], service_key: 'orders-api', view },
    rows_state: 'nonempty',
    roots: [{
      repository: repositoryName,
      state: 'complete',
      generation: `sha256:${'4'.repeat(64)}`,
      root_digest: `sha256:${'5'.repeat(64)}`,
      authority_digest: `sha256:${'6'.repeat(64)}`,
      service_key: 'orders-api',
      service_incarnation: 2,
      service_generation: `sha256:${'c'.repeat(64)}`,
      reference_count: counts[view],
      repository_complete: true,
      all_services_complete: true,
      failed_service_count: 0,
    }],
    rows,
    coverage: {
      authorized_repositories: 1,
      complete_roots: 1,
      empty_roots: 0,
      failed_roots: 0,
      unavailable_roots: 0,
      scanned_references: counts[view],
      returned_rows: rows.length,
      truncated: false,
    },
    pagination: {
      order: 'repository,reference_digest:asc',
      page_size: 25,
      returned: rows.length,
      next_cursor: view === 'dependencies' ? 'next/dependencies' : undefined,
    },
    caveat: 'Static source evidence only.',
  }
}

function relationshipRow(
  view: ServiceRelationshipView,
  index: number,
  posture: 'owned' | 'shared' | 'unowned' | 'ambiguous',
): ServiceRelationshipPage['rows'][number] {
  const sourceClaims = posture === 'shared'
    ? [
        { service_key: 'orders-api', disposition: 'accepted', roles: [{ role: 'primary', origin: 'base' }] },
        { service_key: 'shared-runtime', disposition: 'accepted', roles: [{ role: 'shared', origin: 'base' }] },
      ]
    : posture === 'ambiguous'
      ? [{ service_key: 'orders-api', disposition: 'conflict', roles: [{ role: 'primary', origin: 'proposal' }] }]
      : [{ service_key: 'orders-api', disposition: 'accepted', roles: [{ role: 'primary', origin: 'base' }] }]
  return {
    repository: repositoryName,
    service_key: 'orders-api',
    service_incarnation: 2,
    service_generation: `sha256:${'c'.repeat(64)}`,
    kind: view === 'topics' ? 'kafka' : 'rpc',
    plane: view === 'topics' ? 'producer' : 'grpc',
    class: posture === 'ambiguous' ? 'conflict' : 'resolved',
    lookup_key: view === 'topics' ? 'orders-v1' : 'orders.v1.Orders/Get',
    participation: view === 'callers' ? ['target'] : ['source'],
    counterpart_services: view === 'callers' ? ['checkout'] : view === 'dependencies' ? ['payments'] : [],
    projection_digest: `sha256:${String(index + 1).repeat(64)}`,
    posting_digest: `sha256:${String(index + 4).repeat(64)}`,
    source: {
      path: `services/orders/client-${index}.go`,
      unowned: posture === 'unowned',
      claims: sourceClaims,
    },
    target: view === 'topics' ? undefined : {
      path: 'proto/payments/v1/service.proto',
      unowned: false,
      claims: [{ service_key: 'payments', disposition: 'accepted', roles: [{ role: 'typed', origin: 'base' }] }],
    },
    evidence: {
      kind: view === 'topics' ? 'kafka' : 'rpc',
      plane: view === 'topics' ? 'producer' : 'grpc',
      class: posture === 'ambiguous' ? 'conflict' : 'resolved',
      reason: posture === 'ambiguous' ? 'multiple_targets' : undefined,
      path: `services/orders/client-${index}.go`,
      object_id: 'a'.repeat(40),
      content_digest: `sha256:${'7'.repeat(64)}`,
      span: { start_byte: 10, end_byte: 36, start_line: 4, end_line: 4 },
      source_role: view === 'topics' ? 'producer' : 'caller',
      operation: view === 'topics' ? undefined : 'orders.v1.Orders/Get',
      topic_spelling: view === 'topics' ? 'orders-v1' : undefined,
      posting_digest: `sha256:${String(index + 4).repeat(64)}`,
    },
    citation: `citation-${view}-${index}`,
  }
}

function relationshipCitation(): ServiceRelationshipCitation {
  const row = relationshipRow('dependencies', 0, 'shared')
  return {
    schema: 'phebs-service-relationship-citation-v1',
    repository: repositoryName,
    generation: `sha256:${'4'.repeat(64)}`,
    root_digest: `sha256:${'5'.repeat(64)}`,
    projection: {
      kind: row.kind,
      posting_digest: row.posting_digest,
      class: row.class,
      plane: row.plane,
      lookup_key: row.lookup_key,
      source: row.source,
      target: row.target,
      digest: row.projection_digest,
    },
    evidence: row.evidence,
    content: 'client.Call(ctx, request)',
  }
}
