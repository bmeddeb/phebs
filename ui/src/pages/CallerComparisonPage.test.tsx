import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import CallerComparisonPage from './CallerComparisonPage'
import type {
  CallerComparisonExactPage,
  CallerComparisonPage as ComparisonResponse,
  CallerComparisonRow,
  CallerMapCitation,
  CallerMapEndpoint,
  CallerMapGeneration,
  CallerMapRow,
  ContractCatalogClaim,
  ContractCatalogItem,
  ContractCatalogList,
  CoverageCertificate,
} from '../api'

const api = vi.hoisted(() => ({
  fetchCallerCitation: vi.fn(),
  fetchCallerComparison: vi.fn(),
  fetchContractCatalog: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const engine = new Client()
const commit = '0123456789abcdef0123456789abcdef01234567'
const oldEndpoint: CallerMapEndpoint = {
  protocol: 'protobuf',
  repository: 'github.com/acme/old-contracts',
  declaration_lineage: `provisional_repo_path_v1_${'a'.repeat(64)}`,
  operation: '/demo.orders.v1.Orders/Get',
}
const replacementEndpoint: CallerMapEndpoint = {
  protocol: 'thrift',
  repository: 'github.com/acme/new-contracts',
  declaration_lineage: `provisional_repo_path_v1_${'b'.repeat(64)}`,
  operation: '/demo.orders.v2.Orders/get',
}

const coverage: CoverageCertificate = {
  schema_version: 'coverage-certificate-v1',
  domains: ['grpc-caller', 'thrift-consumer'],
  repository_count: 2,
  repositories: [],
  digest: `sha256:${'c'.repeat(64)}`,
}

function declaration(endpoint: CallerMapEndpoint, id: string): ContractCatalogClaim {
  return {
    assertion_id: id,
    run_id: `run-${id}`,
    predicate: 'DECLARES_OPERATION',
    object: endpoint.operation.replace(/^\//, ''),
    lineage: endpoint.declaration_lineage,
    tier: 'exact',
    sources: [{
      repository: endpoint.repository,
      commit,
      path: endpoint.protocol === 'protobuf' ? 'idl/orders.proto' : 'idl/orders.thrift',
      start_byte: 10,
      end_byte: 50,
      start_line: 4,
      end_line: 6,
      assertion_id: id,
      run_id: `run-${id}`,
      atom_id: `atom-${id}`,
    }],
    sources_truncated: false,
  }
}

function callerRow(index: number, endpoint: CallerMapEndpoint, unresolved = false): CallerMapRow {
  const endpointIdentity = endpoint.protocol === 'protobuf' ? 'a' : 'b'
  return {
    classification: unresolved ? 'extractor_abstention' : 'resolved_caller',
    resolution: unresolved ? 'unresolved' : 'syntax',
    protocol: endpoint.protocol,
    operation: endpoint.operation,
    declaration_lineage: unresolved ? undefined : endpoint.declaration_lineage,
    tier: unresolved ? 'unresolved' : 'heuristic',
    code_role: 'production',
    fresh: true,
    unit_group: unresolved ? `unavailable:${index}` : '//src/orders',
    unit: unresolved
      ? { state: 'unavailable', reason: 'no ownership manifest matched' }
      : {
          state: 'resolved',
          candidates: [{
            id: '//src/orders',
            logical_services: ['orders-api'],
            owners: ['team-orders'],
          }],
        },
    source: {
      repository: 'github.com/acme/orders',
      commit,
      path: `src/caller_${index}.go`,
      object_id: endpointIdentity.repeat(40),
      blob_digest: `sha256:${endpointIdentity.repeat(64)}`,
      plane: 'repository-overlay',
      start_byte: index * 10,
      end_byte: index * 10 + 8,
      start_line: index + 1,
      end_line: index + 1,
      assertion_id: `assertion-${endpoint.protocol}-${index}`,
      run_id: `run-${endpoint.protocol}`,
      atom_id: `atom-${endpoint.protocol}-${index}`,
      citation: `exact-citation-${endpoint.protocol}-${index}`,
    },
    unresolved_reason: unresolved ? 'selector_expr' : undefined,
  }
}

function generation(
  endpoint: CallerMapEndpoint,
  state: CallerMapGeneration['state'] = 'current',
): CallerMapGeneration {
  const endpointIdentity = endpoint.protocol === 'protobuf' ? 'a' : 'b'
  return {
    state,
    reason: state === 'current' ? undefined : 'complete generation is stale',
    plane: 'repository-overlay',
    repository: endpoint.repository,
    commit,
    generation_digest: `sha256:${endpointIdentity.repeat(64)}`,
    declaration_set_digest: `sha256:${'c'.repeat(64)}`,
    candidate_manifest_digest: `sha256:${'d'.repeat(64)}`,
    resolver_manifest_digest: `sha256:${'e'.repeat(64)}`,
    pair_set_digest: `sha256:${'f'.repeat(64)}`,
    manifest_digest: `sha256:${'1'.repeat(64)}`,
    publication_revision: endpoint.protocol === 'protobuf' ? 7 : 11,
  }
}

function comparisonRow(
  index: number,
  classification: CallerComparisonRow['classification'],
): CallerComparisonRow {
  const oldRows = classification === 'new_only_evidence'
    ? []
    : [callerRow(index, oldEndpoint, classification === 'unresolved')]
  const replacementRows = classification === 'old_only_evidence'
    ? []
    : [callerRow(index, replacementEndpoint, classification === 'unresolved')]
  return {
    level: 'occurrence',
    key: `github.com/acme/orders@${commit}:src/caller_${index}.go:${index * 10}-${index * 10 + 8}`,
    classification,
    old: {
      occurrence_count: oldRows.length,
      rows: oldRows,
      rows_truncated: false,
    },
    replacement: {
      occurrence_count: replacementRows.length,
      rows: replacementRows,
      rows_truncated: false,
    },
  }
}

function comparisonPage(
  rows: CallerComparisonRow[],
  total = rows.length,
  nextCursor = '',
): CallerComparisonExactPage {
  return {
    schema_version: 'caller-comparison-v2',
    query: {
      old: oldEndpoint,
      replacement: replacementEndpoint,
      freshness: 'any',
      resolution: 'any',
      ordering: 'source',
      level: 'occurrence',
    },
    old: {
      endpoint: oldEndpoint,
      declaration: declaration(oldEndpoint, 'old-declaration'),
      generation: generation(oldEndpoint),
      matching_rows_state: 'exact',
    },
    replacement: {
      endpoint: replacementEndpoint,
      declaration: declaration(replacementEndpoint, 'replacement-declaration'),
      generation: generation(replacementEndpoint),
      matching_rows_state: 'exact',
    },
    rows,
    total_rows: total,
    pagination: {
      complete: nextCursor === '',
      next_cursor: nextCursor || undefined,
    },
    matching_rows_state: 'exact',
    caveat: 'Static source evidence only; comparison does not establish runtime completeness or migration safety.',
  }
}

function unavailableComparisonPage(): CallerComparisonExactPage {
  return {
    ...comparisonPage([]),
    // The unavailable state's caveat mirrors internal/api/callercomparison_exact.go.
    caveat: 'Caller comparison totals, classifications, and absence are unavailable until both authorized exact complete repository-overlay generations are current.',
    old: {
      endpoint: oldEndpoint,
      generation: generation(oldEndpoint, 'stale'),
      matching_rows_state: 'unavailable',
    },
    matching_rows_state: 'unavailable',
    total_rows: undefined,
    rows: [],
    pagination: { complete: true },
  }
}

function route(includeReplacement = true) {
  const params = new URLSearchParams({
    old_protocol: oldEndpoint.protocol,
    old_repository: oldEndpoint.repository,
    old_lineage: oldEndpoint.declaration_lineage,
    old_operation: oldEndpoint.operation,
  })
  if (includeReplacement) {
    params.set('replacement_protocol', replacementEndpoint.protocol)
    params.set('replacement_repository', replacementEndpoint.repository)
    params.set('replacement_lineage', replacementEndpoint.declaration_lineage)
    params.set('replacement_operation', replacementEndpoint.operation)
  }
  return params
}

function catalogItem(): ContractCatalogItem {
  return {
    kind: 'operation',
    protocol: replacementEndpoint.protocol,
    repository: replacementEndpoint.repository,
    declaration_lineage: replacementEndpoint.declaration_lineage,
    package: 'demo.orders.v2',
    service_fqn: 'demo.orders.v2.Orders',
    method: 'get',
    operation: replacementEndpoint.operation,
    declaration: declaration(replacementEndpoint, 'replacement-declaration'),
  }
}

function catalog(items: ContractCatalogItem[] = [catalogItem()]): ContractCatalogList {
  return {
    schema_version: 'contract-atlas-v2',
    query: { protocol: oldEndpoint.protocol },
    items,
    pagination: { complete: true, truncated: false },
    coverage_digest: coverage.digest,
    coverage,
    caveat: 'Provisional source evidence only.',
  }
}

// Decoded URL-truth base for assertions on window.location.hash after an
// interaction navigates (T43.8): filters and cursor live in the URL.
const compareBase = '#/compare-callers' +
  `?old_protocol=${oldEndpoint.protocol}` +
  `&old_repository=${oldEndpoint.repository}` +
  `&old_lineage=${oldEndpoint.declaration_lineage}` +
  `&old_operation=${oldEndpoint.operation}` +
  `&replacement_protocol=${replacementEndpoint.protocol}` +
  `&replacement_repository=${replacementEndpoint.repository}` +
  `&replacement_lineage=${replacementEndpoint.declaration_lineage}` +
  `&replacement_operation=${replacementEndpoint.operation}`

function hashParams() {
  const hash = window.location.hash
  const queryIndex = hash.indexOf('?')
  return new URLSearchParams(queryIndex === -1 ? '' : hash.slice(queryIndex + 1))
}

function pageElement(params = route()) {
  return (
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <CallerComparisonPage params={params} />
      </BaseProvider>
    </StyletronProvider>
  )
}

function renderPage(params = route()) {
  return render(pageElement(params))
}

beforeEach(() => {
  window.location.hash = ''
  api.fetchCallerCitation.mockReset().mockImplementation(
    async (token: string): Promise<CallerMapCitation> => {
      const parts = token.split('-')
      const index = Number(parts.at(-1))
      const endpoint = parts.at(-2) === 'protobuf' ? oldEndpoint : replacementEndpoint
      return {
        schema_version: 'caller-map-citation-v1',
        generation: generation(endpoint),
        source: callerRow(index, endpoint).source,
        content: `return client.Get(order${index})`,
      }
    },
  )
  api.fetchCallerComparison.mockReset().mockResolvedValue(comparisonPage([
    comparisonRow(1, 'old_only_evidence'),
    comparisonRow(2, 'both_evidence'),
    comparisonRow(3, 'new_only_evidence'),
    comparisonRow(4, 'unresolved'),
  ]))
  api.fetchContractCatalog.mockReset().mockResolvedValue(catalog())
})

afterEach(cleanup)

test('requires an exact old endpoint without performing discovery or comparison', () => {
  renderPage(new URLSearchParams({ old_operation: oldEndpoint.operation }))
  expect(screen.getByText(/requires an exact old endpoint/)).toBeTruthy()
  expect(api.fetchContractCatalog).not.toHaveBeenCalled()
  expect(api.fetchCallerComparison).not.toHaveBeenCalled()
})

test('discovers a replacement through bounded catalog pages and submits repository search', async () => {
  renderPage(route(false))
  const link = await screen.findByRole('link', { name: new RegExp(replacementEndpoint.operation) })
  expect(link.getAttribute('href')).toContain('replacement_protocol=thrift')
  expect(link.getAttribute('href')).toContain(
    `replacement_repository=${encodeURIComponent(replacementEndpoint.repository)}`,
  )
  expect(api.fetchCallerComparison).not.toHaveBeenCalled()
  expect(api.fetchContractCatalog).toHaveBeenCalledWith(
    { repository: undefined, protocol: oldEndpoint.protocol },
    100,
    '',
    expect.any(AbortSignal),
  )

  fireEvent.change(screen.getByLabelText('Replacement repository'), {
    target: { value: ' github.com/acme/new-contracts ' },
  })
  expect(api.fetchContractCatalog).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() => expect(api.fetchContractCatalog).toHaveBeenCalledTimes(2))
  expect(api.fetchContractCatalog.mock.calls[1][0]).toEqual({
    repository: replacementEndpoint.repository,
    protocol: oldEndpoint.protocol,
  })
})

test('renders two exact generations, four classifications, and range-only citations', async () => {
  renderPage()
  await screen.findByText('Rows 1–4 of 4')
  expect(screen.getByTestId('caller-comparison-page').getAttribute('data-responsive-layout'))
    .toBe('desktop-columns-mobile-cards')
  const rowText = screen.getAllByTestId('caller-comparison-row')
    .map((row) => row.textContent)
    .join('\n')
  for (const classification of [
    'Old plane only',
    'Both planes',
    'New plane only',
    'Unresolved',
  ]) {
    expect(rowText).toContain(classification)
  }
  expect(screen.getAllByTestId('caller-comparison-row')).toHaveLength(4)
  const generations = screen.getAllByTestId('caller-comparison-generation')
  expect(generations).toHaveLength(2)
  expect(generations.map((item) => item.getAttribute('data-matching-rows-state')))
    .toEqual(['exact', 'exact'])
  expect(screen.getByText(/revision 7/)).toBeTruthy()
  expect(screen.getByText(/revision 11/)).toBeTruthy()

  const sourceLabel = screen.getAllByText(
    'github.com/acme/orders/src/caller_1.go:2',
  )[0]
  expect(sourceLabel.closest('a')).toBeNull()
  expect(document.querySelector(
    `a[href="#/file?repo=github.com%2Facme%2Forders&path=src%2Fcaller_1.go&ref=${commit}&L=2"]`,
  )).toBeNull()
  expect(api.fetchCallerCitation).not.toHaveBeenCalled()
  const exactCitationButton = sourceLabel.parentElement?.querySelector('button')
  expect(exactCitationButton).not.toBeNull()
  fireEvent.click(exactCitationButton!)
  const citedBytes = await screen.findByLabelText(
    'Exact cited bytes for github.com/acme/orders/src/caller_1.go:2',
  )
  expect(citedBytes.textContent).toBe('return client.Get(order1)')
  await waitFor(() => {
    const keyword = Array.from(citedBytes.querySelectorAll('span'))
      .find((span) => span.textContent === 'return') as HTMLElement | undefined
    expect(keyword?.style.color).toBeTruthy()
  })
  expect(api.fetchCallerCitation).toHaveBeenCalledWith(
    'exact-citation-protobuf-1', expect.any(AbortSignal),
  )
  expect(screen.queryByText(/Shared coverage certificate/)).toBeNull()
  expect(screen.getByText(/does not establish runtime completeness or migration safety/))
    .toBeTruthy()
  expect(screen.getByRole('link', { name: 'Old endpoint declaration' }).getAttribute('href'))
    .toBe(
      `#/file?repo=${encodeURIComponent(oldEndpoint.repository)}&path=idl%2Forders.proto&ref=${commit}&L=4`,
    )
  expect(screen.getByRole('link', { name: 'Replacement endpoint declaration' }).getAttribute('href'))
    .toBe(
      `#/file?repo=${encodeURIComponent(replacementEndpoint.repository)}&path=idl%2Forders.thrift&ref=${commit}&L=4`,
    )
})

test('forwards every shared filter, comparison level, and classification with a fixed page size', async () => {
  const rendered = renderPage()
  await screen.findByText('Rows 1–4 of 4')
  fireEvent.change(screen.getByLabelText('Unit'), { target: { value: ' //src/orders ' } })
  fireEvent.change(screen.getByLabelText('Owner'), { target: { value: ' team-orders ' } })
  fireEvent.change(screen.getByLabelText('Path prefix'), { target: { value: ' src/orders/ ' } })
  fireEvent.change(screen.getByLabelText('Level'), { target: { value: 'unit' } })
  fireEvent.change(screen.getByLabelText('Classification'), { target: { value: 'old_only_evidence' } })
  fireEvent.change(screen.getByLabelText('Code role'), { target: { value: 'production' } })
  fireEvent.change(screen.getByLabelText('Tier'), { target: { value: 'heuristic' } })
  fireEvent.change(screen.getByLabelText('Freshness'), { target: { value: 'fresh' } })
  fireEvent.change(screen.getByLabelText('Resolution'), { target: { value: 'syntax' } })
  fireEvent.change(screen.getByLabelText('Ordering'), { target: { value: 'unit' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  // Apply is a navigation: every trimmed filter lands in the URL and the
  // cursor is dropped, so reload reproduces the authorized request.
  expect(decodeURIComponent(window.location.hash)).toBe(
    `${compareBase}&unit=//src/orders&owner=team-orders&path_prefix=src/orders/` +
    '&code_role=production&tier=heuristic&freshness=fresh&resolution=syntax' +
    '&ordering=unit&level=unit&classification=old_only_evidence',
  )
  rendered.rerender(pageElement(hashParams()))
  await waitFor(() => expect(api.fetchCallerComparison).toHaveBeenLastCalledWith(
    oldEndpoint,
    replacementEndpoint,
    {
      unit: '//src/orders',
      owner: 'team-orders',
      path_prefix: 'src/orders/',
      code_role: 'production',
      tier: 'heuristic',
      freshness: 'fresh',
      resolution: 'syntax',
      ordering: 'unit',
      level: 'unit',
      classification: 'old_only_evidence',
    },
    100,
    '',
    expect.any(AbortSignal),
  ))
})

test('keeps only one bounded page mounted and restarts a stale cursor', async () => {
  api.fetchCallerComparison.mockReset()
    .mockResolvedValueOnce(comparisonPage(
      Array.from({ length: 100 }, (_, index) => comparisonRow(index, 'both_evidence')),
      101,
      'cursor-next',
    ))
    .mockRejectedValueOnce(new Error('409: comparison cursor is no longer valid'))
    .mockResolvedValue(comparisonPage(
      Array.from({ length: 100 }, (_, index) => comparisonRow(index, 'both_evidence')),
      101,
      'cursor-next',
    ))
  const rendered = renderPage()
  await screen.findByText('Rows 1–100 of 101')
  expect(screen.getAllByTestId('caller-comparison-row')).toHaveLength(100)
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(decodeURIComponent(window.location.hash)).toBe(`${compareBase}&cursor=cursor-next`)
  rendered.rerender(pageElement(hashParams()))
  await screen.findByText('Comparison snapshot changed.')
  fireEvent.click(screen.getByRole('button', { name: 'Restart from first page' }))
  // The stale cursor is URL state, so restarting must navigate off it; a
  // reload of the produced URL then performs the first-page read.
  expect(decodeURIComponent(window.location.hash)).toBe(compareBase)
  rendered.rerender(pageElement(hashParams()))
  await screen.findByText('Rows 1–100 of 101')
  expect(api.fetchCallerComparison.mock.calls.at(-1)?.[4]).toBe('')
  expect(screen.getAllByTestId('caller-comparison-row')).toHaveLength(100)
})

test('pages the 10,000-row closure profile without retaining prior DOM rows', async () => {
  api.fetchCallerComparison.mockReset()
    .mockResolvedValueOnce(comparisonPage(
      Array.from({ length: 100 }, (_, index) => comparisonRow(index, 'both_evidence')),
      10_000,
      'cursor-next',
    ))
    .mockResolvedValueOnce(comparisonPage(
      Array.from({ length: 100 }, (_, index) => comparisonRow(index + 100, 'both_evidence')),
      10_000,
      'cursor-after-second',
    ))
  const rendered = renderPage()
  await screen.findByText('Rows 1–100 of 10000')
  expect(screen.getAllByTestId('caller-comparison-row')).toHaveLength(100)
  expect(document.querySelectorAll('[data-testid="caller-comparison-row"]').length)
    .toBe(100)
  expect(screen.getAllByText(/src\/caller_0\.go:1/).length)
    .toBeGreaterThan(0)

  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(decodeURIComponent(window.location.hash)).toBe(`${compareBase}&cursor=cursor-next`)
  rendered.rerender(pageElement(hashParams()))
  await screen.findByText('Rows 101–200 of 10000')
  expect(api.fetchCallerComparison.mock.calls[1][4]).toBe('cursor-next')
  expect(screen.getAllByTestId('caller-comparison-row')).toHaveLength(100)
  expect(document.querySelectorAll('[data-testid="caller-comparison-row"]').length)
    .toBe(100)
  expect(screen.queryAllByText(/src\/caller_0\.go:1/)).toHaveLength(0)
  expect(screen.getAllByText(/src\/caller_100\.go:101/).length)
    .toBeGreaterThan(0)
}, 15_000)

test('restarts a stale first page even though its cursor and page index are already zero', async () => {
  api.fetchCallerComparison.mockReset()
    .mockRejectedValueOnce(new Error('409: comparison snapshot changed'))
    .mockResolvedValueOnce(comparisonPage([comparisonRow(1, 'both_evidence')]))
  renderPage()
  await screen.findByText('Comparison snapshot changed.')
  fireEvent.click(screen.getByRole('button', { name: 'Restart from first page' }))
  await screen.findByText('Rows 1–1 of 1')
  expect(api.fetchCallerComparison).toHaveBeenCalledTimes(2)
  expect(api.fetchCallerComparison.mock.calls[1][4]).toBe('')
})

test('changes endpoint pairs without carrying the preceding pair cursor', async () => {
  api.fetchCallerComparison.mockReset()
    .mockResolvedValueOnce(comparisonPage(
      [comparisonRow(1, 'both_evidence')],
      2,
      'cursor-next',
    ))
    .mockResolvedValueOnce(comparisonPage([comparisonRow(2, 'both_evidence')], 2))
    .mockResolvedValue(comparisonPage([comparisonRow(3, 'both_evidence')]))
  const rendered = renderPage()
  await screen.findByText('Rows 1–1 of 2')
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(decodeURIComponent(window.location.hash)).toBe(`${compareBase}&cursor=cursor-next`)
  rendered.rerender(pageElement(hashParams()))
  await screen.findByText('Page 2')
  api.fetchCallerComparison.mockClear()

  const changed = route()
  changed.set('replacement_operation', '/demo.orders.v2.Orders/list')
  rendered.rerender(pageElement(changed))

  await waitFor(() => expect(api.fetchCallerComparison).toHaveBeenCalledTimes(1))
  expect(api.fetchCallerComparison.mock.calls[0][1].operation)
    .toBe('/demo.orders.v2.Orders/list')
  expect(api.fetchCallerComparison.mock.calls[0][4]).toBe('')
})

test('announces loading and renders the empty scope without an absence claim', async () => {
  let finish!: (page: ComparisonResponse) => void
  api.fetchCallerComparison.mockReturnValue(new Promise<ComparisonResponse>((resolve) => {
    finish = resolve
  }))
  renderPage()
  expect(screen.getByRole('status', { name: 'Loading caller comparison' })).toBeTruthy()
  await act(async () => finish(comparisonPage([])))
  expect(await screen.findByText(/No comparison rows matched these filters/))
    .toBeTruthy()
  expect(screen.getByText(/does not establish runtime completion, absence, or decommissioning safety/))
    .toBeTruthy()
})

test('shows independent generation states and suppresses every absence signal on a gap', async () => {
  api.fetchCallerComparison.mockResolvedValue(unavailableComparisonPage())
  renderPage()

  expect(await screen.findByText('Comparison rows and totals unavailable')).toBeTruthy()
  expect(screen.getByTestId('caller-comparison-progress').getAttribute('data-matching-rows-state'))
    .toBe('unavailable')
  expect(screen.getByTestId('caller-comparison-progress').getAttribute('data-tone')).toBe('blue')
  const generations = screen.getAllByTestId('caller-comparison-generation')
  expect(generations.map((item) => item.getAttribute('data-matching-rows-state')))
    .toEqual(['unavailable', 'exact'])
  expect(generations.map((item) => item.getAttribute('data-tone'))).toEqual(['blue', 'green'])
  expect(screen.getAllByText(/until both authorized exact complete repository-overlay generations are current/).length)
    .toBeGreaterThan(0)
  expect(screen.getByText(/this is not evidence of zero callers/)).toBeTruthy()
  expect(screen.queryByText(/No comparison rows matched/)).toBeNull()
  expect(screen.queryByText(/No matching comparison evidence/)).toBeNull()
  expect(screen.queryByRole('navigation', { name: 'Comparison pagination' })).toBeNull()
  expect(screen.queryByTestId('caller-comparison-row')).toBeNull()
})
