import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, DarkTheme, LightTheme } from 'baseui'
import ImpactPage from './ImpactPage'
import { ModeContext } from '../theme'
import { emptyOperationReport, operationQuery, operationReport, unavailableOperationReport } from '../test-fixtures/operationImpact'
import type { OperationImpactReport } from '../api'

const api = vi.hoisted(() => ({
  fetchOperationImpactReport: vi.fn(), fetchCallerCitation: vi.fn(), fetchContractCallers: vi.fn(),
  fetchOperationImpact: vi.fn(), fetchFieldImpact: vi.fn(), fetchSavedImpact: vi.fn(), postChangeImpact: vi.fn(),
}))
vi.mock('../api', async (importOriginal) => ({ ...await importOriginal<typeof import('../api')>(), ...api }))

const engine = new Client()
const stable = ['contract-impact-operation']
function route(extra: Record<string, string> = {}) {
  return new URLSearchParams({ shape: 'operation_callers', protocol: 'protobuf', repository: operationQuery.endpoint.repository, lineage: operationQuery.endpoint.declaration_lineage, operation: operationQuery.endpoint.operation, ...extra })
}
function hashParams() { return new URLSearchParams(window.location.hash.split('?')[1] ?? '') }
function tree(params = route(), capabilities = stable, dark = false) {
  return <StyletronProvider value={engine}><BaseProvider theme={dark ? DarkTheme : LightTheme}><ModeContext.Provider value={{ mode: dark ? 'dark' : 'light', toggle: () => {} }}>
    <ImpactPage params={params} compatibilityAvailable={false} capabilities={capabilities} />
  </ModeContext.Provider></BaseProvider></StyletronProvider>
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

beforeEach(() => {
  window.location.hash = ''
  for (const mock of Object.values(api)) mock.mockReset()
  api.fetchOperationImpactReport.mockResolvedValue(operationReport())
  const report = operationReport()
  api.fetchCallerCitation.mockResolvedValue({ schema_version: 'caller-map-citation-v1', generation: report.caller_page.generation, source: report.caller_page.rows[0].source, content: 'call()' })
})
afterEach(cleanup)

test.each([false, true])('uses the exact shared rows and citation without promoting unavailable attribution (dark=%s)', async (dark) => {
  render(tree(route(), stable, dark))
  await screen.findByText(operationReport().conclusion.qualification)
  expect(api.fetchOperationImpactReport).toHaveBeenCalledTimes(1)
  expect(api.fetchContractCallers).not.toHaveBeenCalled()
  expect(api.fetchOperationImpact).not.toHaveBeenCalled()
  const row = screen.getByTestId('caller-map-row')
  expect(within(row).getByText('mapping has no attribution')).toBeTruthy()
  expect(within(row).getByText('unavailable')).toBeTruthy()
  expect(within(row).getByText('1 candidate')).toBeTruthy()
  expect(within(row).getByText('au_db69_placeholder')).toBeTruthy()
  expect(within(row).queryByText('resolved')).toBeNull()
  fireEvent.click(within(row).getByRole('button', { name: /client.go:1/ }))
  expect(await screen.findByTestId('caller-map-exact-citation')).toBeTruthy()
  expect(api.fetchCallerCitation).toHaveBeenCalledWith('exact-citation', expect.any(AbortSignal))
  expect(screen.queryByRole('link', { name: /Open source/ })).toBeNull()
  expect(screen.queryByRole('tab', { name: 'Field' })).toBeNull()
  expect(screen.getByText('Other report sections · unavailable')).toBeTruthy()
  expect(screen.getByText('Source authority and scope').closest('details')?.open).toBe(false)
})

test.each([emptyOperationReport, unavailableOperationReport])('distinguishes exact-empty from unavailable without an absence claim', async (fixture) => {
  const report = fixture()
  api.fetchOperationImpactReport.mockResolvedValue(report)
  render(tree())
  await screen.findByText(report.conclusion.qualification)
  expect(screen.queryAllByTestId('caller-map-row')).toHaveLength(0)
  expect(screen.getByText(report.conclusion.state === 'unavailable' ? 'Caller totals unavailable' : 'No matching static evidence')).toBeTruthy()
})

const unsupported: Record<string, string>[] = [
  { shape: 'saved_report' }, { shape: 'field_references' }, { shape: 'proposed_change' },
  { field_number: '1' }, { bundle: 'pb_retained' }, { protocol: 'thrift' }, { page_size: '101' },
]
test.each(unsupported)('unsupported or mixed input stays named and does not fall back to legacy: %o', async (extra) => {
  render(tree(route(extra), [...stable, 'contract-impact-report']))
  expect(screen.getByText(/This report supports one exact protobuf operation/)).toBeTruthy()
  expect(api.fetchOperationImpactReport).not.toHaveBeenCalled()
  expect(api.fetchSavedImpact).not.toHaveBeenCalled()
  expect(api.fetchOperationImpact).not.toHaveBeenCalled()
})

test('missing capability and missing endpoint make no data request', () => {
  const view = render(tree(route(), ['contract-impact-report']))
  expect(screen.getByText('Operation impact is unavailable on this server.')).toBeTruthy()
  view.rerender(tree(new URLSearchParams({ shape: 'operation_callers' })))
  expect(screen.getByText(/requires a protocol/)).toBeTruthy()
  expect(api.fetchOperationImpactReport).not.toHaveBeenCalled()
})

test('repeated route identity is refused instead of silently selecting its first value', () => {
  const params = route()
  params.append('operation', '/shop.Cart/Other')
  render(tree(params))
  expect(screen.getByText(/This report supports one exact protobuf operation/)).toBeTruthy()
  expect(api.fetchOperationImpactReport).not.toHaveBeenCalled()
})

test('filter, view and cursor navigation preserve exact identity and custom page size', async () => {
  const first = operationReport()
  first.caller_page.total_matching_rows = 2
  first.caller_page.pagination = { complete: false, next_cursor: 'next-exact-page' }
  api.fetchOperationImpactReport.mockResolvedValue(first)
  const view = render(tree(route({ page_size: '1', owner: 'team-a' })))
  await screen.findByText('Rows 1–1 of 2')
  fireEvent.click(screen.getByRole('button', { name: 'Unit view' }))
  expect(hashParams().get('shape')).toBe('operation_callers')
  expect(hashParams().get('owner')).toBe('team-a')
  expect(hashParams().get('page_size')).toBe('1')
  view.rerender(tree(hashParams()))
  await screen.findByText('Rows 1–1 of 2')
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  const next = hashParams()
  expect(next.get('cursor')).toBe('next-exact-page')
  expect(next.get('view')).toBe('group')
  expect(next.get('lineage')).toBe(operationQuery.endpoint.declaration_lineage)
  const second = operationReport()
  second.caller_page.total_matching_rows = 2
  api.fetchOperationImpactReport.mockResolvedValue(second)
  view.rerender(tree(next))
  await screen.findByText('Rows 2–2 of 2')
  expect(api.fetchOperationImpactReport).toHaveBeenLastCalledWith(operationQuery.endpoint, expect.objectContaining({ owner: 'team-a' }), 1, 'next-exact-page', expect.any(AbortSignal))
  fireEvent.change(screen.getByLabelText('Owner'), { target: { value: 'team-b' } })
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  expect(hashParams().get('owner')).toBe('team-b')
  expect(hashParams().has('cursor')).toBe(false)
  expect(hashParams().get('page_size')).toBe('1')
})

test('a stale cursor offers an exact restart and does not retain previous rows', async () => {
  api.fetchOperationImpactReport.mockRejectedValueOnce(new Error('409: caller generation changed'))
  const view = render(tree(route({ cursor: 'old-cursor', owner: 'team-a' })))
  await screen.findByText('Caller snapshot changed.')
  expect(screen.queryAllByTestId('caller-map-row')).toHaveLength(0)
  fireEvent.click(screen.getByRole('button', { name: 'Restart from first page' }))
  expect(hashParams().has('cursor')).toBe(false)
  expect(hashParams().get('owner')).toBe('team-a')
  view.rerender(tree(hashParams()))
  await screen.findByText(operationReport().conclusion.qualification)
})

test('a late response cannot replace the selected endpoint report', async () => {
  const late = deferred<OperationImpactReport>()
  api.fetchOperationImpactReport.mockReturnValueOnce(late.promise)
  const view = render(tree())
  view.rerender(tree(route({ owner: 'other-owner' })))
  await screen.findByText(operationReport().conclusion.qualification)
  await act(async () => { late.resolve(unavailableOperationReport()) })
  await waitFor(() => expect(screen.queryByText(unavailableOperationReport().conclusion.qualification)).toBeNull())
  expect(screen.getByTestId('caller-map-row')).toBeTruthy()
})
