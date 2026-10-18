import { afterEach, expect, test, vi } from 'vitest'
import { fetchOperationImpactReport } from './api'
import { operationQuery, operationReport } from './test-fixtures/operationImpact'

afterEach(() => vi.unstubAllGlobals())

test('sends one authenticated exact query and validates its unchanged response', async () => {
  const report = { ...operationReport(), $schema: 'http://localhost/api/schemas/OperationImpactReport.json' }
  report.caller_page.query.owner = 'team-a'
  report.conclusion.query.owner = 'team-a'
  const read = vi.fn().mockResolvedValue({ ok: true, json: async () => report })
  vi.stubGlobal('fetch', read)
  const signal = new AbortController().signal
  expect(await fetchOperationImpactReport(operationQuery.endpoint, { owner: 'team-a' }, 0, 'opaque-cursor', signal)).toBe(report)
  expect(read).toHaveBeenCalledTimes(1)
  const [url, options] = read.mock.calls[0]
  const params = new URL(String(url), 'http://localhost').searchParams
  expect(params.get('shape')).toBe('operation_callers')
  expect(params.get('lineage')).toBe(operationQuery.endpoint.declaration_lineage)
  expect(params.get('owner')).toBe('team-a')
  expect(params.get('page_size')).toBe('0')
  expect(params.get('cursor')).toBe('opaque-cursor')
  expect(options).toEqual({ signal, credentials: 'same-origin' })
})

test('a crossed endpoint payload is rejected at the fetch boundary without a fallback request', async () => {
  const report = operationReport()
  report.caller_page.rows[0].source.run_id = `sha256:${'f'.repeat(64)}`
  const read = vi.fn().mockResolvedValue({ ok: true, json: async () => report })
  vi.stubGlobal('fetch', read)
  await expect(fetchOperationImpactReport(operationQuery.endpoint, {})).rejects.toThrow(/authority or bounded response/)
  expect(read).toHaveBeenCalledTimes(1)
})
