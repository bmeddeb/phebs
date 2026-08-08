import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import ContractAtlasPage from './ContractAtlasPage'
import type {
  ContractCatalogClaim,
  ContractCatalogItem,
  ContractCatalogList,
  ContractCatalogOperation,
  ContractCatalogRelationship,
  ContractCatalogSource,
  CoverageCertificate,
} from '../api'

const api = vi.hoisted(() => ({
  fetchContractCatalog: vi.fn(),
  fetchContractOperation: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const commit = '0123456789abcdef0123456789abcdef01234567'

function source(
  repository: string,
  assertionID: string,
  path = 'proto/catalog.proto',
  line = 7,
): ContractCatalogSource {
  return {
    repository,
    commit,
    path,
    start_byte: 20,
    end_byte: 48,
    start_line: line,
    end_line: line,
    assertion_id: assertionID,
    run_id: `run-${repository.split('/').at(-1)}`,
    atom_id: `atom-${assertionID}`,
  }
}

function claim(
  repository: string,
  assertionID: string,
  predicate: string,
  object: string,
  lineage: string,
  tier = 'exact',
  path?: string,
): ContractCatalogClaim {
  return {
    assertion_id: assertionID,
    run_id: `run-${repository.split('/').at(-1)}`,
    predicate,
    object,
    lineage,
    tier,
    sources: [source(repository, assertionID, path)],
    sources_truncated: false,
  }
}

function item(
  repository: string,
  lineage: string,
  kind: 'service' | 'operation',
  method = '',
): ContractCatalogItem {
  const service = 'demo.Catalog'
  const assertionID = `${repository.split('/').at(-1)}-${lineage}-${kind}-${method || 'service'}`
  return {
    kind,
    protocol: 'protobuf',
    repository,
    declaration_lineage: lineage,
    package: 'demo',
    service_fqn: service,
    method: method || undefined,
    operation: method ? `/${service}/${method}` : undefined,
    declaration: claim(
      repository,
      assertionID,
      kind === 'service' ? 'DECLARES_SERVICE' : 'DECLARES_OPERATION',
      method ? `${service}/${method}` : service,
      lineage,
    ),
  }
}

const coverage: CoverageCertificate = {
  schema_version: 'coverage-certificate-v1',
  domains: ['grpc-consumer', 'proto-contract'],
  repository_count: 2,
  repositories: [{
    repository: 'github.com/acme/contracts',
    indexed_commit: commit,
    scip_index: 'absent',
    runs: [{
      domain: 'proto-contract',
      status: 'published',
      run_id: 'run-contracts',
      extractor: 'protodecl@3',
      commit,
      fresh: true,
      protocols: ['protobuf'],
      corpus_file_count: 3,
      candidate_file_count: 1,
      read_file_count: 1,
      read_bytes: 512,
      unresolved_count: 0,
      assertion_count: 4,
      atom_count: 4,
      inventory_policy: 'gitlink-boundary-v2',
      gitlink_count: 2,
      gitlink_digest: 'sha256:' + 'a'.repeat(64),
      gitlink_sample_paths: ['idl', 'vendor/idl'],
      gitlink_sample_truncated: true,
    }, {
      domain: 'grpc-consumer',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 3,
      candidate_file_count: 0,
      read_file_count: 0,
      read_bytes: 0,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
      latest_attempt: {
        run_id: 'failed-grpc',
        commit,
        extractor: 'grpcgo@1',
        status: 'aborted',
        failure: 'parse failure',
      },
    }],
  }, {
    repository: 'github.com/acme/consumer',
    indexed_commit: commit,
    scip_index: 'absent',
    runs: [{
      domain: 'proto-contract',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 2,
      candidate_file_count: 0,
      read_file_count: 0,
      read_bytes: 0,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
    }, {
      domain: 'grpc-consumer',
      status: 'published',
      run_id: 'run-consumer',
      extractor: 'grpcgo@1',
      commit: 'f'.repeat(40),
      fresh: false,
      protocols: ['grpc'],
      corpus_file_count: 2,
      candidate_file_count: 2,
      read_file_count: 2,
      read_bytes: 256,
      unresolved_count: 1,
      assertion_count: 3,
      atom_count: 3,
      inventory_policy: 'gitlink-boundary-v3',
      gitlink_count: 9,
      gitlink_digest: 'sha256:' + 'b'.repeat(64),
    }],
  }],
  digest: `sha256:${'d'.repeat(64)}`,
}

const firstPage: ContractCatalogList = {
  schema_version: 'contract-atlas-v2',
  query: { protocol: 'protobuf' },
  items: [
    item('github.com/acme/contracts', 'lineage-a', 'service'),
    item('github.com/acme/contracts', 'lineage-a', 'operation', 'Get'),
    item('github.com/acme/contracts', 'lineage-b', 'service'),
  ],
  pagination: {
    complete: false,
    truncated: true,
    reason: 'page_size',
    next_cursor: 'cursor-next',
  },
  coverage_digest: coverage.digest,
  coverage,
  caveat: 'Provisional source evidence only; no completeness conclusion is implied.',
}

const secondPage: ContractCatalogList = {
  ...firstPage,
  items: [
    item('github.com/other/contracts', 'lineage-c', 'service'),
    item('github.com/other/contracts', 'lineage-c', 'operation', 'List'),
  ],
  pagination: { complete: true, truncated: false },
}

function relationship(
  kind: ContractCatalogRelationship['kind'],
  classification: string,
  id: string,
  tier: string,
  lineage: string,
  reason?: string,
): ContractCatalogRelationship {
  const evidence = claim(
    'github.com/acme/consumer',
    id,
    kind === 'implementation'
      ? 'REGISTERS_GRPC_SERVICE'
      : kind === 'caller'
        ? 'CALLS_OPERATION'
        : 'UNRESOLVED_GRPC_CALL',
    kind === 'unresolved_candidate' ? 'Get' : kind === 'caller' ? '/demo.Catalog/Get' : 'demo.Catalog',
    lineage,
    tier,
    `go/${id}.go`,
  )
  evidence.code_role = 'production'
  return { kind, classification, reason, claim: evidence }
}

const detail: ContractCatalogOperation = {
  schema_version: 'contract-atlas-v2',
  repository: 'github.com/acme/contracts',
  declaration_lineage: 'lineage-a',
  service_fqn: 'demo.Catalog',
  method: 'Get',
  operation: '/demo.Catalog/Get',
  declaration: firstPage.items[1].declaration,
  fact_detail: {
    schema: 'proto-operation-detail-v1',
    request: {
      raw: 'Request',
      kind: 'message',
      resolution: 'same_file',
      declaration: 'demo.Request',
    },
    response: {
      raw: 'Reply',
      kind: 'message',
      resolution: 'same_file',
      declaration: 'demo.Reply',
    },
    client_streaming: false,
    server_streaming: true,
  },
  request: {
    raw: 'Request',
    state: 'resolved',
    declaration_name: 'demo.Request',
    declaration: claim(
      'github.com/acme/contracts',
      'message-request',
      'DECLARES_MESSAGE',
      'demo.Request',
      'lineage-a',
    ),
    fields: [{
      object: 'demo.Request#1',
      field_number: 1,
      detail: {
        schema: 'proto-field-detail-v2',
        name: 'next',
        type: {
          raw: 'Request',
          kind: 'message',
          resolution: 'same_file',
          declaration: 'demo.Request',
        },
        cardinality: 'optional',
      },
      declaration: claim(
        'github.com/acme/contracts',
        'field-next',
        'DECLARES_FIELD',
        'demo.Request#1',
        'lineage-a',
      ),
      nested: {
        raw: 'Request',
        state: 'cycle',
        reason: 'RECURSIVE_MESSAGE_REFERENCE',
        declaration_name: 'demo.Request',
        truncated: false,
      },
    }],
    truncated: false,
  },
  response: {
    raw: 'Reply',
    state: 'resolved',
    declaration_name: 'demo.Reply',
    declaration: claim(
      'github.com/acme/contracts',
      'message-reply',
      'DECLARES_MESSAGE',
      'demo.Reply',
      'lineage-a',
    ),
    fields: [],
    truncated: true,
    truncation_reasons: ['fields_per_message_limit'],
  },
  implementations: [
    relationship('implementation', 'proven', 'registration', 'derived', 'lineage-a'),
  ],
  callers: [
    relationship('caller', 'unresolved_name_match', 'caller', 'heuristic', 'grpc-lineage', 'DECLARATION_LINEAGE_UNPROVEN'),
  ],
  unresolved_candidates: [
    relationship('unresolved_candidate', 'extractor_abstention', 'ambiguous', 'unresolved', '', 'OPERATION_DECLARATION_UNRESOLVED'),
  ],
  relationships_truncated: true,
  relationship_limit_reason: 'relationship_row_limit',
  shape_truncated: true,
  coverage_digest: coverage.digest,
  coverage,
  caveat: firstPage.caveat,
}

const engine = new Client()

function page(
  params = new URLSearchParams(),
  callerMapAvailable = false,
  workbenchAvailable = false,
) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ContractAtlasPage
          params={params}
          callerMapAvailable={callerMapAvailable}
          workbenchAvailable={workbenchAvailable}
        />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  api.fetchContractCatalog.mockReset()
    .mockResolvedValueOnce(firstPage)
    .mockResolvedValueOnce(secondPage)
  api.fetchContractOperation.mockReset().mockResolvedValue(detail)
})

afterEach(cleanup)

test('browses duplicate declarations through stable bounded pages', async () => {
  page()
  await screen.findByText('3 rows')
  expect(screen.getByTestId('contract-atlas-workspace').getAttribute('data-responsive-layout'))
    .toBe('desktop-split-mobile-stack')
  expect(api.fetchContractCatalog).toHaveBeenCalledWith(
    { repository: undefined, package: undefined, protocol: undefined, lineage: undefined },
    50,
    '',
    expect.any(AbortSignal),
  )
  expect(screen.getAllByText('demo.Catalog').length).toBe(2)
  expect(screen.getAllByText('duplicate name').length).toBe(2)
  expect(screen.getByText(/github.com\/acme\/contracts · lineage-a/)).toBeTruthy()
  expect(screen.getByText(/github.com\/acme\/contracts · lineage-b/)).toBeTruthy()
  expect(screen.getByText('page size')).toBeTruthy()
  expect(screen.getByText('1 stale runs')).toBeTruthy()
  expect(screen.getByText('2 unpublished')).toBeTruthy()
  expect(screen.getAllByText('1 failed replacement').length).toBeGreaterThan(0)
  expect(screen.getByTestId('analysis-scope-panel')).toBeTruthy()
  expect(screen.getByText('Scope of declared interfaces')).toBeTruthy()

  fireEvent.click(screen.getByRole('button', { name: 'Load next bounded page' }))
  await screen.findByText('5 rows')
  expect(api.fetchContractCatalog).toHaveBeenLastCalledWith(
    { repository: undefined, package: undefined, protocol: undefined, lineage: undefined },
    50,
    'cursor-next',
    expect.any(AbortSignal),
  )
  expect(screen.getByText(/github.com\/other\/contracts · lineage-c/)).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Load next bounded page' })).toBeNull()
})

test('renders bounded shapes, qualified relationships, and pinned source links', async () => {
  page(new URLSearchParams(), true, true)
  await screen.findByText('3 rows')
  fireEvent.click(screen.getByRole('listitem', { name: /Get/ }))
  await screen.findByTestId('contract-operation-detail')

  expect(api.fetchContractOperation).toHaveBeenCalledWith(
    'github.com/acme/contracts',
    'lineage-a',
    '/demo.Catalog/Get',
    expect.any(AbortSignal),
  )
  expect(api.fetchContractOperation).toHaveBeenCalledTimes(1)
  expect(api.fetchContractCatalog).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('heading', { name: '/demo.Catalog/Get' })).toBeTruthy()
  expect(screen.getAllByTestId('analysis-scope-panel')).toHaveLength(2)
  expect(screen.getByText('Scope of this operation')).toBeTruthy()
  expect(screen.getByText('server stream')).toBeTruthy()
  expect(screen.getByText('optional Request · field 1')).toBeTruthy()
  expect(screen.getByText(/cycle · recursive message reference/)).toBeTruthy()
  expect(screen.getByText('proven')).toBeTruthy()
  expect(screen.getByText('unresolved name match')).toBeTruthy()
  expect(screen.getByText('extractor abstention')).toBeTruthy()
  // T19.8: the coverage table names each run's gitlink-boundary state, and a
  // run without an understood inventory policy reads as unknown — never zero.
  expect(screen.getByText('2 gitlinks')).toBeTruthy()
  expect(screen.getByText('idl, vendor/idl')).toBeTruthy()
  expect(screen.getByText('sample truncated')).toBeTruthy()
  expect(screen.getAllByText('unknown').length).toBeGreaterThan(0)
  expect(screen.queryByText('9 gitlinks')).toBeNull()
  expect(screen.getAllByText('relationships truncated').length).toBeGreaterThan(0)
  expect(screen.getByText(/Omitted rows are not evidence of absence/)).toBeTruthy()
  // ClaimBoundary splits the contract text at its ';' into the establishes /
  // does-not-establish sections — assert both exact halves and the labels.
  expect(screen.getAllByText('Provisional source evidence only;').length).toBeGreaterThanOrEqual(1)
  expect(screen.getAllByText('no completeness conclusion is implied.').length).toBeGreaterThanOrEqual(1)
  expect(screen.getAllByText('Establishes').length).toBeGreaterThanOrEqual(1)
  expect(screen.getAllByText('Does not establish').length).toBeGreaterThanOrEqual(1)

  const declaration = screen.getByRole('link', {
    name: /github.com\/acme\/contracts\/proto\/catalog.proto:7/,
  })
  expect(declaration.getAttribute('href')).toBe(
    `#/file?repo=github.com%2Facme%2Fcontracts&path=proto%2Fcatalog.proto&ref=${commit}&L=7`,
  )
  expect(screen.getByRole('link', { name: 'Analyze impact' }).getAttribute('href'))
    .toBe('#/impact?operation=%2Fdemo.Catalog%2FGet')
  expect(screen.getByRole('link', { name: 'View callers' }).getAttribute('href'))
    .toBe(
      '#/callers?protocol=protobuf&repository=github.com%2Facme%2Fcontracts&lineage=lineage-a&operation=%2Fdemo.Catalog%2FGet',
    )
  expect(screen.getByRole('link', { name: 'Start Workbench' }).getAttribute('href'))
    .toBe(
      '#/workbench?source=atlas&step=why&protocol=protobuf&repository=github.com%2Facme%2Fcontracts&lineage=lineage-a&operation=%2Fdemo.Catalog%2FGet',
    )
})

test('keeps the Workbench launch undiscoverable without its capability', async () => {
  page()
  await screen.findByText('3 rows')
  fireEvent.click(screen.getByRole('listitem', { name: /Get/ }))
  await screen.findByTestId('contract-operation-detail')
  expect(screen.queryByRole('link', { name: 'Start Workbench' })).toBeNull()
})

test('applies exact server filters and renders an honest bounded empty state', async () => {
  const empty: ContractCatalogList = {
    ...firstPage,
    items: [],
    pagination: { complete: true, truncated: false },
  }
  api.fetchContractCatalog.mockReset()
    .mockResolvedValueOnce(firstPage)
    .mockResolvedValueOnce(empty)
  page()
  await screen.findByText('3 rows')
  fireEvent.change(screen.getByLabelText('Repository'), {
    target: { value: 'github.com/acme/missing' },
  })
  fireEvent.change(screen.getByLabelText('Package'), {
    target: { value: 'demo.v2' },
  })
  fireEvent.change(screen.getByLabelText('Provisional lineage'), {
    target: { value: 'lineage-missing' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await screen.findByText(/No declaration rows were returned/)
  expect(api.fetchContractCatalog).toHaveBeenLastCalledWith(
    {
      repository: 'github.com/acme/missing',
      package: 'demo.v2',
      protocol: undefined,
      lineage: 'lineage-missing',
    },
    50,
    '',
    expect.any(AbortSignal),
  )
  expect(screen.getByText(/This does not establish absence/)).toBeTruthy()
})

test('ignores an operation response after a newer selection', async () => {
  let resolveFirst!: (value: ContractCatalogOperation) => void
  api.fetchContractOperation.mockReset()
    .mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve }))
    .mockResolvedValueOnce({ ...detail, method: 'List', operation: '/demo.Catalog/List' })
  const listWithTwo = {
    ...firstPage,
    items: [
      ...firstPage.items,
      item('github.com/acme/contracts', 'lineage-a', 'operation', 'List'),
    ],
  }
  api.fetchContractCatalog.mockReset().mockResolvedValue(listWithTwo)
  page()
  await screen.findByText('4 rows')
  fireEvent.click(screen.getByRole('listitem', { name: /Get/ }))
  fireEvent.click(screen.getByRole('listitem', { name: /List/ }))
  await screen.findByRole('heading', { name: '/demo.Catalog/List' })
  resolveFirst(detail)
  await waitFor(() => expect(screen.queryByRole('heading', { name: '/demo.Catalog/Get' })).toBeNull())
})

test('renders a thrift operation with oneway chip, field 0, and union badge', async () => {
  const thriftDetail: ContractCatalogOperation = {
    ...detail,
    operation: '/agent.Agent/emitBatch',
    service_fqn: 'agent.Agent',
    method: 'emitBatch',
    fact_detail: {
      schema: 'thrift-operation-detail-v1',
      request: {
        raw: 'Agent.emitBatch_args',
        kind: 'message',
        resolution: 'same_file',
        declaration: 'agent.Agent.emitBatch_args',
      },
      response: {
        raw: 'Agent.emitBatch_result',
        kind: 'message',
        resolution: 'same_file',
        declaration: 'agent.Agent.emitBatch_result',
      },
      oneway: true,
    },
    request: {
      raw: 'Agent.emitBatch_args',
      state: 'resolved',
      declaration_name: 'agent.Agent.emitBatch_args',
      kind: 'union',
      synthetic: true,
      declaration: {
        ...claim(
          'github.com/acme/idl',
          'args-decl',
          'DECLARES_MESSAGE',
          'agent.Agent.emitBatch_args',
          'lineage-t',
        ),
        detail: { schema: 'thrift-message-detail-v1', kind: 'union' },
      },
      fields: [{
        object: 'agent.Agent.emitBatch_result#0',
        field_number: 0,
        detail: {
          schema: 'thrift-field-detail-v1',
          name: 'success',
          type: { raw: 'Batch', kind: 'message', resolution: 'same_file', declaration: 'agent.Batch' },
          cardinality: 'default',
        },
        declaration: claim(
          'github.com/acme/idl',
          'field-success',
          'DECLARES_FIELD',
          'agent.Agent.emitBatch_result#0',
          'lineage-t',
        ),
      }],
      truncated: false,
    },
  }
  vi.mocked(api.fetchContractOperation).mockResolvedValue(thriftDetail)
  page()
  await screen.findByText('3 rows')
  fireEvent.click(screen.getAllByRole('listitem')[0])
  await screen.findByTestId('contract-operation-detail')
  expect(screen.getByText('oneway')).toBeTruthy()
  expect(screen.queryByText('client stream')).toBeNull()
  expect(screen.queryByText('single request')).toBeNull()
  expect(screen.getByText('union')).toBeTruthy()
  expect(screen.getByText(/field 0/)).toBeTruthy()
  expect(screen.getByText('success')).toBeTruthy()
})

test('changing the protocol filter survives lazy updater evaluation', async () => {
  page()
  await screen.findByText('3 rows')
  // Regression: the protocol select's onChange read event.currentTarget
  // inside the functional state updater; React nulls currentTarget after
  // dispatch, so the queued updater crashed the whole tree on the next
  // render. A single-option select could never fire onChange, which kept
  // the bug latent until the thrift option existed.
  fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'thrift' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await screen.findByText('2 rows')
  expect(api.fetchContractCatalog).toHaveBeenLastCalledWith(
    { repository: undefined, package: undefined, protocol: 'thrift', lineage: undefined },
    50,
    '',
    expect.any(AbortSignal),
  )
  expect(screen.getByTestId('contract-atlas-workspace')).toBeTruthy()
})
