import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import RelationshipExplorerPage from './RelationshipExplorerPage'
import type {
  ServiceRelationshipCitation,
  ServiceRelationshipPage,
  ServiceRelationshipView,
} from '../api'

const api = vi.hoisted(() => ({
  fetchServiceRelationships: vi.fn(),
  fetchServiceRelationshipCitation: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const engine = new Client()
const repositories = ['example.invalid/mono-a', 'example.invalid/mono-b']

interface RequestValues {
  repository?: string
  service_key: string
  view: ServiceRelationshipView
  kind?: 'rpc' | 'kafka'
  plane?: string
  lookup_key?: string
  page_size: number
  cursor?: string
}

beforeEach(() => {
  window.location.hash = ''
  api.fetchServiceRelationships.mockReset().mockImplementation(
    (values: RequestValues) => Promise.resolve(relationshipPage(values)),
  )
  api.fetchServiceRelationshipCitation.mockReset().mockImplementation(
    () => Promise.resolve(relationshipCitation(relationshipPage(defaultRequest()).rows[0])),
  )
})

afterEach(cleanup)

test('waits for one exact service key before creating a relationship binding', () => {
  renderPage(new URLSearchParams())
  expect(screen.getByRole('heading', { name: 'Relationship explorer' })).toBeTruthy()
  expect(screen.getByText(/Leave repository blank/)).toBeTruthy()
  expect(api.fetchServiceRelationships).not.toHaveBeenCalled()
})

test('uses broad authorized fallback and shares deterministic ids between exact rows and the optional diagram', async () => {
  renderPage(new URLSearchParams({ service_key: 'orders-api', diagram: '1' }))
  expect(await screen.findByRole('heading', { name: 'Exact source rows' })).toBeTruthy()
  expect(api.fetchServiceRelationships).toHaveBeenCalledWith(defaultRequest(), expect.any(AbortSignal))
  expect(screen.getByText('Authorized repositories').parentElement?.textContent).toContain('2')
  expect(screen.getByText(/Partial · 1 failed or unavailable root/)).toBeTruthy()
  expect(screen.getByText('Current page only · table remains authoritative')).toBeTruthy()
  expect(screen.getAllByText('R-01').length).toBeGreaterThanOrEqual(2)
  expect(screen.getAllByText('orders-api').length).toBeGreaterThan(0)
  expect(screen.getAllByText('Ambiguous').length).toBeGreaterThan(0)
  expect(screen.getAllByText('Unowned').length).toBeGreaterThan(0)
  expect(screen.getByRole('link', { name: 'Next exact page' }).getAttribute('href')).toContain('cursor=bound%2Fnext')
})

test('maps repository, direction, evidence, and exact contract filters into one bounded reader query', async () => {
  const params = new URLSearchParams({
    service_key: 'orders-api',
    repository: repositories[0],
    direction: 'consumes',
    evidence: 'kafka',
    lookup_key: 'orders.created.v1',
    cursor: 'bound/current',
  })
  renderPage(params)
  await screen.findByRole('heading', { name: 'Exact source rows' })
  expect(api.fetchServiceRelationships).toHaveBeenCalledWith({
    repository: repositories[0],
    service_key: 'orders-api',
    view: 'topics',
    kind: 'kafka',
    plane: 'consumer',
    lookup_key: 'orders.created.v1',
    page_size: 50,
    cursor: 'bound/current',
  }, expect.any(AbortSignal))
  expect(screen.getByRole('link', { name: 'First exact page' }).getAttribute('href')).not.toContain('cursor=')
})

test('reauthorizes and validates an immutable citation before showing source content', async () => {
  const request = defaultRequest()
  const page = relationshipPage(request)
  api.fetchServiceRelationshipCitation.mockResolvedValueOnce(relationshipCitation(page.rows[0]))
  renderPage(new URLSearchParams({ service_key: 'orders-api' }))
  await screen.findByRole('heading', { name: 'Exact source rows' })
  fireEvent.click(screen.getAllByRole('button', { name: 'View citation' })[0])
  expect(await screen.findByRole('dialog', { name: 'Exact source citation' })).toBeTruthy()
  expect(screen.getByText('client.Call(ctx, request)')).toBeTruthy()
  expect(api.fetchServiceRelationshipCitation).toHaveBeenCalledWith('citation-0', expect.any(AbortSignal))

  fireEvent.click(screen.getByRole('button', { name: 'Close citation' }))
  api.fetchServiceRelationshipCitation.mockResolvedValueOnce({
    ...relationshipCitation(page.rows[1]),
    generation: `sha256:${'9'.repeat(64)}`,
    content: 'forged source',
  })
  fireEvent.click(screen.getAllByRole('button', { name: 'View citation' })[1])
  expect(await screen.findByText(/citation response authority differs/)).toBeTruthy()
  expect(screen.queryByText('forged source')).toBeNull()
})

test('refuses malformed response authority and keeps a root gap distinct from exact empty', async () => {
  api.fetchServiceRelationships.mockResolvedValueOnce({
    ...relationshipPage(defaultRequest()),
    schema: 'wrong-schema',
  })
  const malformed = renderPage(new URLSearchParams({ service_key: 'orders-api' }))
  expect(await screen.findByText(/query authority differs/)).toBeTruthy()
  malformed.unmount()

  const gap = relationshipPage(defaultRequest())
  gap.rows = []
  gap.rows_state = 'gap'
  gap.coverage.returned_rows = 0
  gap.pagination.returned = 0
  api.fetchServiceRelationships.mockResolvedValueOnce(gap)
  renderPage(new URLSearchParams({ service_key: 'orders-api' }))
  expect(await screen.findByText(/No empty result is inferred/)).toBeTruthy()
  expect(screen.queryByText(/Exact empty page/)).toBeNull()
})

test('submitting filters writes a source-first deep link and resets any prior cursor', async () => {
  renderPage(new URLSearchParams({ service_key: 'old', cursor: 'old/cursor' }))
  await waitFor(() => expect(api.fetchServiceRelationships).toHaveBeenCalled())
  fireEvent.change(screen.getByRole('textbox', { name: 'Service key' }), { target: { value: 'orders-api' } })
  fireEvent.change(screen.getByRole('textbox', { name: 'Repository' }), { target: { value: repositories[0] } })
  fireEvent.change(screen.getByRole('combobox', { name: 'Direction' }), { target: { value: 'uses' } })
  fireEvent.change(screen.getByRole('combobox', { name: 'Evidence' }), { target: { value: 'rpc' } })
  fireEvent.change(screen.getByRole('textbox', { name: 'Contract or topic' }), { target: { value: 'orders.v1.Orders/Get' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  expect(decodeURIComponent(window.location.hash)).toBe('#/relationships?service_key=orders-api&repository=example.invalid/mono-a&direction=uses&evidence=rpc&lookup_key=orders.v1.Orders/Get')
})

function renderPage(params: URLSearchParams) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <RelationshipExplorerPage params={params} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

function defaultRequest(): RequestValues {
  return {
    repository: undefined,
    service_key: 'orders-api',
    view: 'all',
    kind: undefined,
    plane: undefined,
    lookup_key: undefined,
    page_size: 50,
    cursor: undefined,
  }
}

function relationshipPage(request: RequestValues): ServiceRelationshipPage {
  const selectedRepositories = request.repository ? [request.repository] : repositories
  const rows = [relationshipRow(selectedRepositories[0], 0, request, 'ambiguous'), relationshipRow(selectedRepositories[0], 1, request, 'unowned')]
  return {
    schema: 'phebs-service-relationship-page-v1',
    query: {
      repositories: selectedRepositories,
      service_key: request.service_key,
      view: request.view,
      kind: request.kind,
      plane: request.plane,
      lookup_key: request.lookup_key,
    },
    rows_state: 'nonempty',
    roots: selectedRepositories.map((repository, index) => ({
      repository,
      state: index === 0 ? 'complete' : 'unavailable',
      reason: index === 0 ? undefined : 'publication_missing',
      generation: `sha256:${'4'.repeat(64)}`,
      root_digest: `sha256:${'5'.repeat(64)}`,
      service_key: request.service_key,
      service_incarnation: 2,
      service_generation: `sha256:${'c'.repeat(64)}`,
      reference_count: index === 0 ? rows.length : 0,
    })),
    rows,
    coverage: {
      authorized_repositories: selectedRepositories.length,
      complete_roots: 1,
      empty_roots: 0,
      failed_roots: 0,
      unavailable_roots: Math.max(0, selectedRepositories.length - 1),
      scanned_references: rows.length,
      returned_rows: rows.length,
      truncated: false,
    },
    pagination: {
      order: 'repository,reference_digest:asc',
      page_size: 50,
      returned: rows.length,
      next_cursor: request.cursor ? undefined : 'bound/next',
    },
    caveat: 'Static source evidence only.',
  }
}

function relationshipRow(repository: string, index: number, request: RequestValues, posture: 'ambiguous' | 'unowned'): ServiceRelationshipPage['rows'][number] {
  const kafka = request.kind === 'kafka' || request.view === 'topics'
  const posting = `sha256:${String(index + 6).repeat(64)}`
  return {
    repository,
    service_key: request.service_key,
    service_incarnation: 2,
    service_generation: `sha256:${'c'.repeat(64)}`,
    kind: kafka ? 'kafka' : 'rpc',
    plane: kafka ? request.plane || 'producer' : 'grpc',
    class: posture === 'ambiguous' ? 'conflict' : 'resolved',
    lookup_key: request.lookup_key || (kafka ? 'orders.created.v1' : 'orders.v1.Orders/Get'),
    participation: request.view === 'callers' ? ['target'] : ['source'],
    counterpart_services: kafka ? [] : ['payments'],
    projection_digest: `sha256:${String(index + 1).repeat(64)}`,
    posting_digest: posting,
    source: {
      path: `services/orders/client-${index}.go`,
      unowned: posture === 'unowned',
      claims: posture === 'ambiguous'
        ? [{ service_key: 'orders-api', disposition: 'conflict', roles: [{ role: 'primary', origin: 'proposal' }] }]
        : [{ service_key: 'orders-api', disposition: 'accepted', roles: [{ role: 'primary', origin: 'base' }] }],
    },
    target: kafka ? undefined : {
      path: 'proto/payments/v1/service.proto',
      unowned: false,
      claims: [{ service_key: 'payments', disposition: 'accepted', roles: [{ role: 'typed', origin: 'base' }] }],
    },
    evidence: {
      kind: kafka ? 'kafka' : 'rpc',
      plane: kafka ? request.plane || 'producer' : 'grpc',
      class: posture === 'ambiguous' ? 'conflict' : 'resolved',
      reason: posture === 'ambiguous' ? 'multiple_targets' : undefined,
      path: `services/orders/client-${index}.go`,
      object_id: 'a'.repeat(40),
      content_digest: `sha256:${'7'.repeat(64)}`,
      span: { start_byte: 10, end_byte: 36, start_line: 4 + index, end_line: 4 + index },
      source_role: kafka ? request.plane || 'producer' : 'caller',
      operation: kafka ? undefined : request.lookup_key || 'orders.v1.Orders/Get',
      topic_spelling: kafka ? request.lookup_key || 'orders.created.v1' : undefined,
      posting_digest: posting,
    },
    citation: `citation-${index}`,
  }
}

function relationshipCitation(row: ServiceRelationshipPage['rows'][number]): ServiceRelationshipCitation {
  return {
    schema: 'phebs-service-relationship-citation-v1',
    repository: row.repository,
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
