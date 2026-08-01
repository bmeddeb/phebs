import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import CallerMapPage from './CallerMapPage'
import type {
  CallerMapCitation,
  CallerMapPage as CallerMapResponse,
  CallerMapRow,
  ContractCatalogClaim,
  CoverageCertificate,
} from '../api'

const api = vi.hoisted(() => ({
  fetchCallerCitation: vi.fn(),
  fetchContractCallers: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const engine = new Client()
const commit = '0123456789abcdef0123456789abcdef01234567'
const operation = '/demo.orders.v1.Orders/Get'
const lineage = `provisional_repo_path_v1_${'a'.repeat(64)}`
const contractRepo = 'github.com/acme/contracts'
const sourceRepo = 'github.com/acme/checkout'

function route() {
  return new URLSearchParams({
    protocol: 'protobuf',
    repository: contractRepo,
    lineage,
    operation,
  })
}

const declaration: ContractCatalogClaim = {
  assertion_id: 'decl-1',
  run_id: 'run-contract',
  predicate: 'DECLARES_OPERATION',
  object: 'demo.orders.v1.Orders/Get',
  lineage,
  tier: 'exact',
  sources: [{
    repository: contractRepo,
    commit,
    path: 'idl/orders.proto',
    start_byte: 20,
    end_byte: 80,
    start_line: 7,
    end_line: 9,
    assertion_id: 'decl-1',
    run_id: 'run-contract',
    atom_id: 'atom-decl',
  }],
  sources_truncated: false,
}

const coverage: CoverageCertificate = {
  schema_version: 'coverage-certificate-v1',
  domains: ['grpc-caller', 'proto-contract'],
  repository_count: 3,
  repositories: [{
    repository: contractRepo,
    indexed_commit: commit,
    scip_index: 'absent',
    runs: [{
      domain: 'proto-contract',
      status: 'published',
      run_id: 'run-contract',
      extractor: 'protodecl@3',
      commit,
      fresh: true,
      protocols: ['protobuf'],
      corpus_file_count: 1,
      candidate_file_count: 1,
      read_file_count: 1,
      read_bytes: 100,
      unresolved_count: 0,
      assertion_count: 1,
      atom_count: 1,
    }, {
      domain: 'grpc-caller',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 1,
      candidate_file_count: 0,
      read_file_count: 0,
      read_bytes: 0,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
    }],
  }, {
    repository: sourceRepo,
    indexed_commit: commit,
    scip_index: 'present',
    runs: [{
      domain: 'proto-contract',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 3,
      candidate_file_count: 0,
      read_file_count: 0,
      read_bytes: 0,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
    }, {
      domain: 'grpc-caller',
      status: 'published',
      run_id: 'run-callers',
      extractor: '1.2.0',
      commit,
      fresh: false,
      protocols: ['grpc', `attribution-${'b'.repeat(64)}`],
      corpus_file_count: 3,
      candidate_file_count: 3,
      read_file_count: 3,
      read_bytes: 300,
      unresolved_count: 1,
      assertion_count: 3,
      atom_count: 3,
      latest_attempt: {
        run_id: 'replacement',
        commit: 'f'.repeat(40),
        extractor: '1.2.0',
        status: 'aborted',
        failure: 'replacement failed',
      },
    }],
  }, {
    repository: 'github.com/acme/unsupported',
    indexed_commit: commit,
    scip_index: 'absent',
    runs: [{
      domain: 'proto-contract',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 0,
      candidate_file_count: 0,
      read_file_count: 0,
      read_bytes: 0,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
    }, {
      domain: 'grpc-caller',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 2,
      candidate_file_count: 2,
      read_file_count: 1,
      read_bytes: 90,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
      latest_attempt: {
        run_id: 'failed',
        commit,
        extractor: '1.2.0',
        status: 'aborted',
        failure: 'index unsupported',
      },
    }],
  }],
  digest: `sha256:${'c'.repeat(64)}`,
}

function callerRow(
  index: number,
  options: {
    unresolved?: boolean
    state?: string
    candidates?: number
  } = {},
): CallerMapRow {
  const unresolved = options.unresolved === true
  const state = options.state ?? 'resolved'
  const candidates = Array.from(
    { length: options.candidates ?? (state === 'resolved' ? 1 : 0) },
    (_, candidate) => ({
      id: `unit-${index}-${candidate}`,
      logical_services: [`checkout-${candidate}`],
      owners: [`team-${candidate}`],
      deployables: [`service-${candidate}`],
      build_targets: [`//src:${index}-${candidate}`],
    }),
  )
  return {
    classification: unresolved ? 'extractor_abstention' : 'resolved_caller',
    resolution: unresolved ? 'unresolved' : index % 2 === 0 ? 'scip' : 'syntax',
    protocol: 'protobuf',
    operation,
    declaration_lineage: unresolved ? undefined : lineage,
    tier: unresolved ? 'unresolved' : index % 2 === 0 ? 'derived' : 'heuristic',
    code_role: 'production',
    fresh: index % 3 !== 0,
    unit_group: state === 'resolved' ? `unit-${index}-0` : `${state}:${index}`,
    unit: {
      state,
      reason: state === 'ambiguous' ? 'multiple matching roots' : undefined,
      candidates,
    },
    source: {
      repository: sourceRepo,
      commit,
      path: `src/caller_${index}.go`,
      object_id: `${index.toString(16)}`.repeat(40).slice(0, 40),
      blob_digest: `sha256:${index.toString(16)}`.padEnd(71, 'd').slice(0, 71),
      plane: 'repository-overlay',
      start_byte: index * 10,
      end_byte: index * 10 + 8,
      start_line: index + 1,
      end_line: index + 1,
      assertion_id: `assertion-${index}`,
      run_id: 'run-callers',
      atom_id: `atom-${index}`,
      citation: `exact-citation-${index}`,
    },
    unresolved_reason: unresolved ? 'unsupported_receiver_flow' : undefined,
  }
}

function callerPage(
  rows: CallerMapRow[],
  total = rows.length,
  nextCursor = '',
): CallerMapResponse {
  return {
    schema_version: 'caller-map-v2',
    query: {
      endpoint: {
        protocol: 'protobuf',
        repository: contractRepo,
        declaration_lineage: lineage,
        operation,
      },
      freshness: 'any',
      resolution: 'any',
      ordering: 'source',
    },
    declaration,
    rows,
    groups: [],
    total_matching_rows: total,
    pagination: {
      complete: nextCursor === '',
      next_cursor: nextCursor || undefined,
    },
    generation: {
      state: 'current',
      plane: 'repository-overlay',
      repository: sourceRepo,
      commit,
      generation_digest: `sha256:${'e'.repeat(64)}`,
      declaration_set_digest: `sha256:${'f'.repeat(64)}`,
      candidate_manifest_digest: `sha256:${'1'.repeat(64)}`,
      resolver_manifest_digest: `sha256:${'2'.repeat(64)}`,
      pair_set_digest: `sha256:${'3'.repeat(64)}`,
      manifest_digest: `sha256:${'4'.repeat(64)}`,
      publication_revision: 7,
      pair_count: 3,
      result_count: rows.filter((row) => row.classification === 'resolved_caller').length,
      abstention_count: rows.filter((row) => row.classification === 'extractor_abstention').length,
      canonical_bytes: 4096,
    },
    matching_rows_state: 'exact',
    coverage_digest: coverage.digest,
    attribution_digest: `sha256:${'b'.repeat(64)}`,
    coverage,
    caveat: 'Static source evidence only; no absence or runtime-completeness conclusion is implied.',
  }
}

function pageElement(params = route(), comparisonAvailable = false) {
  return (
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <CallerMapPage
          params={params}
          comparisonAvailable={comparisonAvailable}
        />
      </BaseProvider>
    </StyletronProvider>
  )
}

function renderPage(params = route(), comparisonAvailable = false) {
  return render(pageElement(params, comparisonAvailable))
}

beforeEach(() => {
  api.fetchCallerCitation.mockReset().mockImplementation(
    async (token: string): Promise<CallerMapCitation> => {
      const index = Number(token.split('-').at(-1))
      const source = callerRow(index).source
      return {
        schema_version: 'caller-map-citation-v1',
        generation: callerPage([]).generation!,
        source,
        content: `client.Get(order${index})`,
      }
    },
  )
  api.fetchContractCallers.mockReset().mockResolvedValue(callerPage([
    callerRow(1),
    callerRow(2, { state: 'ambiguous', candidates: 2 }),
    callerRow(3, { unresolved: true, state: 'unavailable' }),
  ]))
})

afterEach(cleanup)

test('requires a complete Contract Atlas identity before making a request', () => {
  renderPage(new URLSearchParams({ operation }))
  expect(screen.getByText(/requires a protocol, declaration repository/)).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Open Contract Atlas' }).getAttribute('href'))
    .toBe('#/contracts')
  expect(api.fetchContractCallers).not.toHaveBeenCalled()
})

test('announces loading and renders the scoped empty state honestly', async () => {
  let finish!: (page: CallerMapResponse) => void
  api.fetchContractCallers.mockReturnValue(new Promise<CallerMapResponse>((resolve) => {
    finish = resolve
  }))
  renderPage()
  expect(screen.getByRole('status', { name: 'Loading Caller Map' })).toBeTruthy()
  await act(async () => finish(callerPage([])))
  expect(await screen.findByText(/No caller rows matched these filters/)).toBeTruthy()
  expect(screen.getByText(/does not establish runtime use, completeness, migration completion/))
    .toBeTruthy()
})

test('renders a typed generation gap without zero callers or required coverage', async () => {
  const gap = callerPage([])
  gap.matching_rows_state = 'unavailable'
  gap.generation = {
    state: 'missing',
    reason: 'complete caller generation missing',
    plane: 'repository-overlay',
    repository: sourceRepo,
  }
  gap.coverage = undefined
  gap.declaration = undefined
  gap.total_matching_rows = undefined
  gap.coverage_digest = undefined
  gap.attribution_digest = undefined
  gap.caveat = 'Caller totals and absence are unavailable until one exact generation is current.'
  api.fetchContractCallers.mockResolvedValue(gap)
  renderPage()

  expect(await screen.findByText('Caller totals unavailable')).toBeTruthy()
  expect(screen.getByText(/This is not evidence of zero callers/)).toBeTruthy()
  expect(screen.getByText(/This is not zero callers and no partial rows are shown/))
    .toBeTruthy()
  expect(screen.queryByTestId('caller-map-coverage')).toBeNull()
  expect(screen.queryByText('No matching static evidence')).toBeNull()
})

test('renders exact citations, ambiguity, unresolved queue, coverage, and mobile-safe controls', async () => {
  renderPage(route(), true)
  await screen.findByText('Rows 1–3 of 3')
  expect(screen.getByTestId('caller-map-generation').getAttribute('data-matching-rows-state'))
    .toBe('exact')
  expect(screen.getByText('Exact repository-overlay generation')).toBeTruthy()
  expect(screen.getAllByText('Repository-overlay identity and byte span')).toHaveLength(3)
  expect(screen.queryByText('Evidence identity and byte span')).toBeNull()
  expect(screen.getByTestId('caller-map-page').getAttribute('data-responsive-layout'))
    .toBe('desktop-table-mobile-cards')
  expect(screen.getByRole('heading', { name: operation })).toBeTruthy()
  expect(screen.getByText('ambiguous')).toBeTruthy()
  expect(screen.getByText('unavailable')).toBeTruthy()
  expect(screen.getByText(/Needs review: unsupported receiver flow/)).toBeTruthy()
  expect(screen.getByText(/replacement failed/)).toBeTruthy()
  expect(screen.getByText(/index unsupported/)).toBeTruthy()
  expect(screen.getAllByText('stale').length).toBeGreaterThan(0)
  expect(screen.getByText('stale · failed replacement')).toBeTruthy()
  expect(screen.getAllByText('failed replacement').length).toBeGreaterThan(0)
  expect(screen.getAllByText('not published / unsupported').length).toBeGreaterThan(0)
  expect(screen.getByRole('link', { name: 'Compare replacement' }).getAttribute('href')).toBe(
    `#/compare-callers?old_protocol=protobuf&old_repository=${encodeURIComponent(contractRepo)}&old_lineage=${lineage}&old_operation=${encodeURIComponent(operation)}`,
  )

  const declarationLink = screen.getByRole('link', { name: /Declaration/ })
  expect(declarationLink.getAttribute('href')).toBe(
    `#/file?repo=${encodeURIComponent(contractRepo)}&path=idl%2Forders.proto&ref=${commit}&L=7`,
  )
  const sourceLabel = screen.getByText(`${sourceRepo}/src/caller_1.go:2`)
  expect(sourceLabel.closest('a')).toBeNull()
  expect(document.querySelector(
    `a[href="#/file?repo=${encodeURIComponent(sourceRepo)}&path=src%2Fcaller_1.go&ref=${commit}&L=2"]`,
  )).toBeNull()
  const exactCitationButton = sourceLabel.parentElement?.querySelector('button')
  expect(exactCitationButton).not.toBeNull()
  fireEvent.click(exactCitationButton!)
  expect((await screen.findByLabelText(
    `Exact cited bytes for ${sourceRepo}/src/caller_1.go:2`,
  )).textContent).toBe('client.Get(order1)')
  expect(api.fetchCallerCitation).toHaveBeenCalledWith(
    'exact-citation-1', expect.any(AbortSignal),
  )
  expect(screen.getAllByText(/Repository-overlay occurrence; source access is limited/).length)
    .toBe(3)

  const before = screen.getAllByTestId('caller-map-row')
    .map((row) => row.getAttribute('data-occurrence-id'))
    .sort()
  const requestCount = api.fetchContractCallers.mock.calls.length
  const unitView = screen.getByRole('button', { name: 'Unit view' })
  unitView.focus()
  expect(document.activeElement).toBe(unitView)
  fireEvent.click(unitView)
  const after = screen.getAllByTestId('caller-map-row')
    .map((row) => row.getAttribute('data-occurrence-id'))
    .sort()
  expect(after).toEqual(before)
  expect(api.fetchContractCallers).toHaveBeenCalledTimes(requestCount)

  fireEvent.click(screen.getByRole('button', { name: 'Review unresolved' }))
  await waitFor(() => expect(api.fetchContractCallers).toHaveBeenCalledTimes(2))
  expect(api.fetchContractCallers.mock.calls[1][1]).toMatchObject({
    resolution: 'unresolved',
  })
})

test('refuses a citation response that differs from the selected exact occurrence', async () => {
  const source = callerRow(1).source
  api.fetchCallerCitation.mockResolvedValue({
    schema_version: 'caller-map-citation-v1',
    generation: callerPage([]).generation!,
    source: { ...source, path: 'src/another.go' },
    content: 'wrong occurrence',
  } satisfies CallerMapCitation)
  renderPage()
  await screen.findByText('Rows 1–3 of 3')
  fireEvent.click(screen.getAllByRole('button', { name: 'Read exact cited bytes' })[0])

  expect(await screen.findByRole('alert')).toHaveProperty(
    'textContent',
    'Exact citation unavailable: Exact citation response did not match the selected caller occurrence.',
  )
  expect(screen.queryByTestId('caller-map-exact-citation')).toBeNull()
})

test('keeps comparison undiscoverable without its authenticated capability', async () => {
  renderPage()
  await screen.findByText('Rows 1–3 of 3')
  expect(screen.queryByRole('link', { name: 'Compare replacement' })).toBeNull()
})

test('mounts at most one bounded ambiguous-candidate expansion', async () => {
  const truncated = callerRow(1, { state: 'ambiguous', candidates: 64 })
  truncated.unit.candidate_total = 70
  api.fetchContractCallers.mockResolvedValue(callerPage([
    truncated,
    callerRow(2, { state: 'ambiguous', candidates: 64 }),
  ]))
  renderPage()
  await screen.findByText('Rows 1–2 of 2')
  expect(screen.queryAllByTestId('caller-map-unit-candidate')).toHaveLength(0)

  const expand = screen.getAllByRole('button', { name: 'Show 64 candidates' })
  fireEvent.click(expand[0])
  expect(screen.getAllByTestId('caller-map-unit-candidate')).toHaveLength(64)
  expect(screen.getByText('Candidate list is bounded; 6 more not shown.')).toBeTruthy()
  fireEvent.click(expand[1])
  expect(screen.getAllByTestId('caller-map-unit-candidate')).toHaveLength(64)
  expect(screen.queryByText('Candidate list is bounded; 6 more not shown.')).toBeNull()
  expect(screen.getAllByRole('button', { name: 'Hide 64 candidates' })).toHaveLength(1)
})

test('applies every shared filter and resets pagination', async () => {
  renderPage()
  await screen.findByText('Rows 1–3 of 3')
  fireEvent.change(screen.getByLabelText('Unit'), { target: { value: 'unit-a' } })
  fireEvent.change(screen.getByLabelText('Owner'), { target: { value: 'team-a' } })
  fireEvent.change(screen.getByLabelText('Path prefix'), { target: { value: 'src/a/' } })
  fireEvent.change(screen.getByLabelText('Code role'), { target: { value: 'test' } })
  fireEvent.change(screen.getByLabelText('Tier'), { target: { value: 'heuristic' } })
  fireEvent.change(screen.getByLabelText('Freshness'), { target: { value: 'fresh' } })
  fireEvent.change(screen.getByLabelText('Resolution'), { target: { value: 'syntax' } })
  fireEvent.change(screen.getByLabelText('Server ordering'), { target: { value: 'unit' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(api.fetchContractCallers).toHaveBeenCalledTimes(2))
  expect(api.fetchContractCallers.mock.calls[1]).toEqual([
    {
      protocol: 'protobuf',
      repository: contractRepo,
      declaration_lineage: lineage,
      operation,
    },
    {
      unit: 'unit-a',
      owner: 'team-a',
      path_prefix: 'src/a/',
      code_role: 'test',
      tier: 'heuristic',
      freshness: 'fresh',
      resolution: 'syntax',
      ordering: 'unit',
    },
    100,
    '',
    expect.any(AbortSignal),
  ])
})

test('keeps a 10,000-row snapshot bounded to the current page', async () => {
  api.fetchContractCallers.mockReset()
    .mockResolvedValueOnce(callerPage(
      Array.from({ length: 100 }, (_, index) => callerRow(index)),
      10_000,
      'cursor-next',
    ))
    .mockResolvedValueOnce(callerPage(
      Array.from({ length: 100 }, (_, index) => callerRow(index + 100)),
      10_000,
      'cursor-after-second',
    ))
  renderPage()
  await screen.findByText('Page 1')
  expect(screen.getAllByTestId('caller-map-row')).toHaveLength(100)
  expect(document.querySelectorAll('[data-testid="caller-map-row"]')).toHaveLength(100)
  expect(screen.getAllByTestId('caller-map-row')[0]?.getAttribute('data-occurrence-id'))
    .toContain('assertion-0')

  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByText('Rows 101–200 of 10000')
  expect(api.fetchContractCallers).toHaveBeenCalledTimes(2)
  expect(screen.getAllByTestId('caller-map-row')).toHaveLength(100)
  expect(document.querySelectorAll('[data-testid="caller-map-row"]')).toHaveLength(100)
  expect(document.querySelector('[data-occurrence-id*="assertion-0"]')).toBeNull()
  expect(screen.getAllByTestId('caller-map-row').at(-1)?.getAttribute('data-occurrence-id'))
    .toContain('assertion-199')
})

test('renders exact exhaustion when the final bounded page is reached', async () => {
  api.fetchContractCallers.mockReset()
    .mockResolvedValueOnce(callerPage(
      Array.from({ length: 100 }, (_, index) => callerRow(index)),
      101,
      'cursor-final',
    ))
    .mockResolvedValueOnce(callerPage([callerRow(100)], 101))
  renderPage()
  await screen.findByText('Rows 1–100 of 101')
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByText('Rows 101–101 of 101')
  expect(screen.getByText(/Exact snapshot exhausted on page 2\./)).toBeTruthy()
  expect(screen.getAllByTestId('caller-map-row')).toHaveLength(1)
})

test('renders retry and restarts a stale snapshot cursor', async () => {
  api.fetchContractCallers.mockReset()
    .mockResolvedValueOnce(callerPage([callerRow(0)], 2, 'cursor-next'))
    .mockRejectedValueOnce(new Error('409: caller map cursor is no longer valid'))
    .mockResolvedValueOnce(callerPage([callerRow(0)], 2, 'cursor-next'))
  renderPage()
  await screen.findByText('Rows 1–1 of 2')
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByText('Caller snapshot changed.')
  expect(screen.getByText(/cursor is no longer valid/)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Restart from first page' }))
  await screen.findByText('Rows 1–1 of 2')
  expect(api.fetchContractCallers.mock.calls[2][3]).toBe('')
})

test('changes endpoints without carrying the preceding endpoint cursor', async () => {
  api.fetchContractCallers.mockReset()
    .mockResolvedValueOnce(callerPage([callerRow(0)], 2, 'cursor-next'))
    .mockResolvedValueOnce(callerPage([callerRow(1)], 2))
    .mockResolvedValue(callerPage([callerRow(2)]))
  const rendered = renderPage()
  await screen.findByText('Rows 1–1 of 2')
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByText('Page 2')
  api.fetchContractCallers.mockClear()

  const changed = route()
  changed.set('operation', '/demo.orders.v1.Orders/List')
  rendered.rerender(pageElement(changed))

  await waitFor(() => expect(api.fetchContractCallers).toHaveBeenCalledTimes(1))
  expect(api.fetchContractCallers.mock.calls[0][0].operation)
    .toBe('/demo.orders.v1.Orders/List')
  expect(api.fetchContractCallers.mock.calls[0][3]).toBe('')
})

test('retries an ordinary loading failure without changing the endpoint', async () => {
  api.fetchContractCallers.mockReset()
    .mockRejectedValueOnce(new Error('500: temporary failure'))
    .mockResolvedValueOnce(callerPage([callerRow(0)]))
  renderPage()
  await screen.findByText('Caller Map request failed.')
  fireEvent.click(screen.getByRole('button', { name: 'Retry this page' }))
  await screen.findByText('Rows 1–1 of 1')
  expect(api.fetchContractCallers).toHaveBeenCalledTimes(2)
  expect(api.fetchContractCallers.mock.calls[0][0])
    .toEqual(api.fetchContractCallers.mock.calls[1][0])
})
