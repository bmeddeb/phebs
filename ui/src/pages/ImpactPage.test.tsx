import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import ImpactPage from './ImpactPage'
import type { ContractImpactReport } from '../api'
import { glossaryTerms } from '../glossary.generated'
import { impactCommit as commit, impactReport as report } from '../test-fixtures/impactKafka'

const api = vi.hoisted(() => ({
  fetchOperationImpact: vi.fn(),
  fetchFieldImpact: vi.fn(),
  fetchSavedImpact: vi.fn(),
  postChangeImpact: vi.fn(),
}))

vi.mock('../api', () => api)

const engine = new Client()

function page(
  params = new URLSearchParams(),
  compatibilityAvailable = false,
  capabilities: readonly string[] = ['contract-impact-report'],
) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ImpactPage
          params={params}
          compatibilityAvailable={compatibilityAvailable}
          capabilities={capabilities}
        />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  window.location.hash = ''
  for (const mock of Object.values(api)) mock.mockReset().mockResolvedValue(report)
})

afterEach(cleanup)

test('operation report preserves mode-specific vocabulary with accessible glossary help', async () => {
  page()
  fireEvent.change(screen.getByLabelText('Canonical operation'), { target: { value: '/shop.Cart/Get' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))

  await screen.findByText(report.conclusion.text)
  expect(api.fetchOperationImpact).toHaveBeenCalledWith('/shop.Cart/Get', expect.any(AbortSignal))
  expect(screen.getByRole('heading', { name: 'Resolved evidence' })).toBeTruthy()
  expect(screen.getByRole('heading', { name: 'Matching call evidence' })).toBeTruthy()
  expect(screen.getByRole('heading', { name: 'Extractor abstentions' })).toBeTruthy()
  expect(screen.getAllByText('protobuf').length).toBeGreaterThan(0)
  expect(screen.getAllByText('grpc-consumer').length).toBeGreaterThan(0)
  expect(screen.getByText('method Get matches 2 generated services')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Help for Matching static evidence' })).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Help for Could not resolve' })).toBeTruthy()
  expect(screen.getByRole('heading', { name: 'Analysis scope & gaps' })).toBeTruthy()
  expect(screen.queryByRole('heading', { name: 'Known consumers' })).toBeNull()
  expect(screen.queryByRole('heading', { name: 'Unresolved candidates' })).toBeNull()
  expect(screen.getByText(/They are not an accuracy,/)).toBeTruthy()
  const certificate = screen.getByTestId('coverage-certificate-detail') as HTMLDetailsElement
  expect(certificate.open).toBe(false)
  fireEvent.click(screen.getByText('Coverage certificate'))
  expect(certificate.open).toBe(true)
  expect(screen.getAllByText('unsupported').length).toBeGreaterThan(0)
  expect(screen.getAllByText('no published evidence for this domain').length)
    .toBeGreaterThan(0)
  expect(screen.getByText(report.caveat)).toBeTruthy()

  const source = screen.getByRole('link', { name: /github.com\/acme\/cart-client\/client\/cart.go:27/ })
  expect(source.getAttribute('href')).toBe(`#/file?repo=github.com%2Facme%2Fcart-client&path=client%2Fcart.go&ref=${commit}&L=27`)
  expect(window.location.hash).toContain(`bundle=${report.bundle_id}`)
})

test('field mode sends the stable field identity', async () => {
  api.fetchFieldImpact.mockResolvedValueOnce({
    ...report,
    query: {
      kind: 'contract_impact_field',
      lineage: 'lineage-cart',
      message: 'shop.Cart',
      field_number: 7,
      domains: ['protobuf-field'],
    },
    resolved_evidence: [{
      ...report.matching_call_evidence[0],
      kind: 'field_reference',
      classification: 'field reference',
    }],
    matching_call_evidence: [],
  })
  page()
  fireEvent.click(screen.getByRole('tab', { name: 'Field' }))
  fireEvent.change(screen.getByLabelText('Contract lineage'), { target: { value: 'lineage-cart' } })
  fireEvent.change(screen.getByLabelText('Message full name'), { target: { value: 'shop.Cart' } })
  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '7' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  await waitFor(() => expect(api.fetchFieldImpact).toHaveBeenCalledWith('lineage-cart', 'shop.Cart', 7, expect.any(AbortSignal)))
  await screen.findByRole('heading', { name: 'Resolved evidence' })
  expect(screen.queryByRole('heading', { name: /consumer/i })).toBeNull()
  expect(screen.getByText(report.conclusion.text)).toBeTruthy()
  expect(screen.getByText(/field reference/)).toBeTruthy()
})

test('field mode admits Thrift field zero and renders its exact domain and citation', async () => {
  const thriftReport: ContractImpactReport = {
    ...report,
    query: {
      kind: 'contract_impact_field',
      lineage: `contract_scip_package_v1_${'c'.repeat(64)}`,
      message: 'health.Meta_Health_Result',
      field_number: 0,
      domains: ['scip-thrift-field'],
    },
    resolved_evidence: [{
      ...report.matching_call_evidence[0],
      kind: 'field_reference',
      domain: 'scip-thrift-field',
      protocol: 'thrift',
      predicate: 'REFERENCES_THRIFT_FIELD',
      object: 'health.Meta_Health_Result#0',
      path: 'consumer/use.go',
      start_line: 6,
      end_line: 6,
      classification: 'resolved_field_reference',
      reason: 'exact field identity matched',
    }],
    matching_call_evidence: [],
  }
  api.fetchFieldImpact.mockResolvedValueOnce(thriftReport)
  page()
  fireEvent.click(screen.getByRole('tab', { name: 'Field' }))
  fireEvent.change(screen.getByLabelText('Field protocol'), { target: { value: 'thrift' } })
  fireEvent.change(screen.getByLabelText('Contract lineage'), { target: { value: thriftReport.query.lineage } })
  fireEvent.change(screen.getByLabelText('Message full name'), { target: { value: 'health.Meta_Health_Result' } })
  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '0' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))

  await waitFor(() => expect(api.fetchFieldImpact).toHaveBeenCalledWith(
    thriftReport.query.lineage,
    'health.Meta_Health_Result',
    0,
    expect.any(AbortSignal),
  ))
  expect(await screen.findByText('scip-thrift-field')).toBeTruthy()
  expect(screen.getAllByText('thrift').length).toBeGreaterThan(0)
  expect(screen.getByRole('link', { name: /consumer\/use.go:6/ })).toBeTruthy()
})

test('field mode keeps protocol-specific field bounds without weakening protobuf', async () => {
  page()
  fireEvent.click(screen.getByRole('tab', { name: 'Field' }))
  fireEvent.change(screen.getByLabelText('Contract lineage'), { target: { value: 'lineage-cart' } })
  fireEvent.change(screen.getByLabelText('Message full name'), { target: { value: 'shop.Cart' } })

  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '0' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  expect(await screen.findByText('Protobuf field numbers must be between 1 and 536870911.')).toBeTruthy()
  expect(api.fetchFieldImpact).not.toHaveBeenCalled()

  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '19000' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  expect(await screen.findByText('Protobuf field numbers 19000 through 19999 are reserved.')).toBeTruthy()
  expect(api.fetchFieldImpact).not.toHaveBeenCalled()

  fireEvent.change(screen.getByLabelText('Field protocol'), { target: { value: 'thrift' } })
  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  expect(await screen.findByText('Enter a whole field number.')).toBeTruthy()
  expect(api.fetchFieldImpact).not.toHaveBeenCalled()

  fireEvent.change(screen.getByLabelText('Field number'), { target: { value: '32768' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  expect(await screen.findByText('Thrift field numbers must be between 0 and 32767.')).toBeTruthy()
  expect(api.fetchFieldImpact).not.toHaveBeenCalled()
})

test('unknown coverage schema does not enable coverage certificate help', async () => {
  api.fetchOperationImpact.mockResolvedValueOnce({
    ...report,
    coverage: {
      ...report.coverage,
      schema_version: 'coverage-certificate-future',
    },
  })
  page()
  fireEvent.change(screen.getByLabelText('Canonical operation'), { target: { value: '/shop.Cart/Get' } })
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))

  const help = await screen.findByRole('button', { name: 'Help for Coverage certificate' })
  fireEvent.click(help)
  const term = glossaryTerms.find((candidate) => candidate.id === 'coverage_certificate')
  expect((await screen.findByRole('status')).textContent).toBe(term?.availability.unavailableHelp)
  expect((screen.getByTestId('coverage-certificate-detail') as HTMLDetailsElement).open).toBe(false)
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

function reroute(view: ReturnType<typeof page>, query: string) {
  view.rerender(<StyletronProvider value={engine}><BaseProvider theme={LightTheme}>
    <ImpactPage params={new URLSearchParams(query)} compatibilityAvailable={false} capabilities={['contract-impact-report']} />
  </BaseProvider></StyletronProvider>)
}

test('a failed replacement hides the preceding saved report', async () => {
  const view = page(new URLSearchParams(`bundle=${report.bundle_id}`))
  await screen.findByText(report.conclusion.text)
  api.fetchSavedImpact.mockRejectedValueOnce(new Error('replacement unavailable'))
  reroute(view, 'bundle=pb_replacement')
  await screen.findByText(/replacement unavailable/)
  expect(screen.queryByText(report.conclusion.text)).toBeNull()
})

test.each(['success', 'error'])('clearing a saved report rejects its late %s', async (outcome) => {
  let resolve!: (value: ContractImpactReport) => void
  let reject!: (reason: Error) => void
  api.fetchSavedImpact.mockImplementationOnce(() => new Promise<ContractImpactReport>((yes, no) => { resolve = yes; reject = no }))
  const view = page(new URLSearchParams(`bundle=${report.bundle_id}`))
  reroute(view, '')
  await act(async () => { if (outcome === 'success') resolve(report); else reject(new Error('abandoned saved report')) })
  expect(screen.queryByText(report.conclusion.text)).toBeNull()
  expect(screen.queryByText(/abandoned saved report/)).toBeNull()
})

test('an abandoned manual report cannot navigate back to Impact', async () => {
  let resolve!: (value: ContractImpactReport) => void
  api.fetchOperationImpact.mockImplementationOnce(() => new Promise<ContractImpactReport>(yes => { resolve = yes }))
  const view = page(new URLSearchParams('operation=%2Fshop.Cart%2FGet'))
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  const signal = api.fetchOperationImpact.mock.calls[0][1] as AbortSignal
  view.unmount()
  window.location.hash = '#/topics'
  await act(async () => { resolve(report) })
  expect(window.location.hash).toBe('#/topics')
  expect(signal.aborted).toBe(true)
})

test('back and forward operation handoffs update the draft without building reports', () => {
  const view = page(new URLSearchParams('operation=%2Fshop.Cart%2FGet'))
  reroute(view, 'operation=%2Fshop.Cart%2FPut')
  expect((screen.getByLabelText('Canonical operation') as HTMLInputElement).value).toBe('/shop.Cart/Put')
  expect(api.fetchOperationImpact).not.toHaveBeenCalled()
})

test('saved reports reject a different bundle identity', async () => {
  api.fetchSavedImpact.mockResolvedValueOnce({ ...report, bundle_id: 'pb_crossed' })
  page(new URLSearchParams(`bundle=${report.bundle_id}`))
  expect(await screen.findByText(/different saved report/)).toBeTruthy()
  expect(screen.queryByText(report.conclusion.text)).toBeNull()
})

test('saved report retry reauthorizes only the selected bundle', async () => {
  api.fetchSavedImpact.mockRejectedValueOnce(new Error('temporary report failure'))
  page(new URLSearchParams(`bundle=${report.bundle_id}`))
  await screen.findByText(/temporary report failure/)
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await screen.findByText(report.conclusion.text)
  expect(api.fetchSavedImpact.mock.calls.map(call => call[0])).toEqual([report.bundle_id, report.bundle_id])
  expect(api.fetchOperationImpact).not.toHaveBeenCalled()
})

test('manual validation on a saved URL never offers a retry of the preceding bundle', async () => {
  page(new URLSearchParams(`bundle=${report.bundle_id}`))
  await screen.findByText(report.conclusion.text)
  fireEvent.click(screen.getByRole('button', { name: 'Build report' }))
  await screen.findByText('Enter a canonical /package.Service/Method operation.')
  expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
  expect(screen.queryByText(report.conclusion.text)).toBeNull()
  expect(api.fetchSavedImpact).toHaveBeenCalledTimes(1)
})
