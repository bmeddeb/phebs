import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import ImpactPage from './ImpactPage'
import type { ContractImpactReport } from '../api'

const api = vi.hoisted(() => ({
  fetchOperationImpact: vi.fn(),
  fetchFieldImpact: vi.fn(),
  fetchSavedImpact: vi.fn(),
  postChangeImpact: vi.fn(),
}))

vi.mock('../api', () => api)

const commit = '0123456789abcdef0123456789abcdef01234567'
const report: ContractImpactReport = {
  schema_version: 'contract-impact-report-v2',
  bundle_id: `pb_${'a'.repeat(64)}`,
  query: {
    kind: 'contract_impact_operation',
    operation: '/shop.Cart/Get',
    domains: ['grpc-consumer'],
  },
  conclusion: {
    text: 'Resolved or matching call evidence was found within the stated evidence scope.',
    coverage_digest: `sha256:${'b'.repeat(64)}`,
  },
  resolved_evidence: [],
  matching_call_evidence: [{
    kind: 'operation_call',
    domain: 'grpc-consumer',
    protocol: 'protobuf',
    assertion_id: 'assertion-known',
    evidence_atom_id: 'atom-known',
    predicate: 'CALLS_OPERATION',
    object: '/shop.Cart/Get',
    repository: 'github.com/acme/cart-client',
    commit,
    path: 'client/cart.go',
    start_byte: 100,
    end_byte: 116,
    start_line: 27,
    end_line: 27,
    tier: 'heuristic',
    code_role: 'production',
    classification: 'matching_call_evidence',
    reason: 'operation object matched without declaration identity',
    fresh: true,
  }],
  extractor_abstentions: [{
    kind: 'unresolved_candidate',
    domain: 'grpc-consumer',
    protocol: 'protobuf',
    assertion_id: 'assertion-unresolved',
    evidence_atom_id: 'atom-unresolved',
    predicate: 'UNRESOLVED_GRPC_CALL',
    object: 'Get',
    repository: 'github.com/acme/cart-client',
    commit,
    path: 'client/ambiguous.go',
    start_byte: 200,
    end_byte: 219,
    start_line: 41,
    end_line: 41,
    tier: 'unresolved',
    code_role: 'test',
    classification: 'extractor_abstention',
    reason: 'method Get matches 2 generated services',
    fresh: false,
  }],
  coverage: {
    schema_version: 'coverage-certificate-v1',
    domains: ['grpc-consumer'],
    repository_count: 2,
    repositories: [{
      repository: 'github.com/acme/cart-client',
      indexed_commit: commit,
      scip_index: 'absent',
      runs: [{
        domain: 'grpc-consumer',
        status: 'published',
        run_id: 'run-known',
        extractor: 'grpcgo',
        commit,
        fresh: true,
        protocols: ['grpc'],
        corpus_file_count: 4,
        candidate_file_count: 2,
        read_file_count: 2,
        read_bytes: 512,
        unresolved_count: 1,
        assertion_count: 2,
        atom_count: 2,
      }],
    }, {
      repository: 'github.com/acme/web',
      indexed_commit: commit,
      scip_index: 'absent',
      runs: [{
        domain: 'grpc-consumer',
        status: 'absent',
        fresh: false,
        corpus_file_count: 1,
        candidate_file_count: 0,
        read_file_count: 0,
        read_bytes: 0,
        unresolved_count: 0,
        assertion_count: 0,
        atom_count: 0,
      }],
    }],
    digest: `sha256:${'b'.repeat(64)}`,
  },
  coverage_rows: [{
    repository: 'github.com/acme/cart-client',
    domain: 'grpc-consumer',
    state: 'covered',
    indexed_commit: commit,
    evidence_commit: commit,
    assertion_count: 2,
    unresolved_count: 1,
    candidate_file_count: 2,
    read_file_count: 2,
  }, {
    repository: 'github.com/acme/web',
    domain: 'grpc-consumer',
    state: 'unsupported',
    reason: 'no published evidence for this domain',
    indexed_commit: commit,
    assertion_count: 0,
    unresolved_count: 0,
    candidate_file_count: 0,
    read_file_count: 0,
  }],
  caveat: 'Evidence is bounded to visible repositories and recorded extractor coverage.',
}

const engine = new Client()

function page(params = new URLSearchParams(), compatibilityAvailable = false) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ImpactPage params={params} compatibilityAvailable={compatibilityAvailable} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  window.location.hash = ''
  for (const mock of Object.values(api)) mock.mockReset().mockResolvedValue(report)
})

afterEach(cleanup)

test('operation report renders qualified conclusions, pinned evidence, unresolved candidates, and complete coverage', async () => {
  page()
  fireEvent.change(screen.getByLabelText('Canonical operation'), { target: { value: '/shop.Cart/Get' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))

  await screen.findByText(report.conclusion.text)
  expect(api.fetchOperationImpact).toHaveBeenCalledWith('/shop.Cart/Get', expect.any(AbortSignal))
  expect(screen.getByRole('heading', { name: 'Resolved evidence' })).toBeTruthy()
  expect(screen.getByRole('heading', { name: 'Matching call evidence' })).toBeTruthy()
  expect(screen.getByRole('heading', { name: 'Extractor abstentions' })).toBeTruthy()
  expect(screen.queryByText('Known consumers')).toBeNull()
  expect(screen.getAllByText('protobuf').length).toBeGreaterThan(0)
  expect(screen.getAllByText('grpc-consumer').length).toBeGreaterThan(0)
  expect(screen.getByText('method Get matches 2 generated services')).toBeTruthy()
  expect(screen.getByText('unsupported')).toBeTruthy()
  expect(screen.getByText('no published evidence for this domain')).toBeTruthy()
  expect(screen.getByText(report.caveat)).toBeTruthy()

  const source = screen.getByRole('link', { name: /github.com\/acme\/cart-client\/client\/cart.go:27/ })
  expect(source.getAttribute('href')).toBe(`#/file?repo=github.com%2Facme%2Fcart-client&path=client%2Fcart.go&ref=${commit}&L=27`)
  expect(window.location.hash).toContain(`bundle=${report.bundle_id}`)
})

test('field mode sends the stable field identity', async () => {
  page()
  fireEvent.click(screen.getByRole('tab', { name: 'Field' }))
  fireEvent.change(screen.getByLabelText('Contract lineage'), { target: { value: 'lineage-cart' } })
  fireEvent.change(screen.getByLabelText('Message full name'), { target: { value: 'shop.Cart' } })
  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '7' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  await waitFor(() => expect(api.fetchFieldImpact).toHaveBeenCalledWith('lineage-cart', 'shop.Cart', 7, expect.any(AbortSignal)))
})

test('change tab follows server capability and submits parsed snapshots', async () => {
  const hidden = page()
  expect(screen.queryByRole('tab', { name: 'Contract change' })).toBeNull()
  hidden.unmount()

  page(new URLSearchParams(), true)
  fireEvent.click(screen.getByRole('tab', { name: 'Contract change' }))
  fireEvent.change(screen.getByLabelText('Contract lineage'), { target: { value: 'lineage-cart' } })
  fireEvent.change(screen.getByLabelText('Before files (JSON)'), { target: { value: '[{"path":"cart.proto","content":"before"}]' } })
  fireEvent.change(screen.getByLabelText('After files (JSON)'), { target: { value: '[{"path":"cart.proto","content":"after"}]' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))

  await waitFor(() => expect(api.postChangeImpact).toHaveBeenCalledWith({
    lineage: 'lineage-cart',
    before: [{ path: 'cart.proto', content: 'before' }],
    after: [{ path: 'cart.proto', content: 'after' }],
  }, expect.any(AbortSignal)))
})

test('saved report URLs reauthorize through the report endpoint', async () => {
  page(new URLSearchParams(`bundle=${report.bundle_id}`))
  await screen.findByText(report.conclusion.text)
  expect(api.fetchSavedImpact).toHaveBeenCalledWith(report.bundle_id, expect.any(AbortSignal))
})

test('Atlas handoff pre-populates an operation without automatically submitting it', () => {
  page(new URLSearchParams('operation=%2Fshop.Cart%2FGet'))
  expect((screen.getByLabelText('Canonical operation') as HTMLInputElement).value)
    .toBe('/shop.Cart/Get')
  expect(api.fetchOperationImpact).not.toHaveBeenCalled()
})
