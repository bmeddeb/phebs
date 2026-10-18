import { expect, test } from 'vitest'
import { validateOperationImpactReport } from './operationImpactContract'
import { emptyOperationReport, operationQuery, operationReport, unavailableOperationReport } from './test-fixtures/operationImpact'
import type { OperationImpactReport } from './api'

test('retains unavailable ID-only attribution without promoting the supplied candidate', () => {
  const report = operationReport()
  const result = validateOperationImpactReport(report, operationQuery, 50)
  expect(result).toBe(report)
  expect(result.caller_page.rows[0].unit).toBe(report.caller_page.rows[0].unit)
  expect(result.caller_page.rows[0].unit).toEqual({ state: 'unavailable', reason: 'mapping_has_no_attribution', candidates: [{ id: 'au_db69_placeholder' }], candidate_total: 1 })
})

test.each([emptyOperationReport, unavailableOperationReport])('keeps exact-empty and unavailable envelopes distinct', (fixture) => {
  const report = fixture()
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
})

function focusedScope(report: OperationImpactReport, digest: string) {
  report.caller_page.scope!.scope_posture = 'focused'
  report.caller_page.scope!.analysis_unit = {
    schema: 'analysis-unit-v1', name: 'current-unit', digest,
    primary_paths: ['client/'], supporting_paths: [], primary_path_count: 1, supporting_path_count: 0,
    search_index_posture: 'focused', typed_index_posture: 'unavailable',
  }
}

test.each<[string, (report: OperationImpactReport) => void]>([
  ['unindexed source', r => { delete r.caller_page.scope!.commit; delete r.caller_page.generation!.commit; r.caller_page.generation!.state = 'missing' }],
  ['indexed before resolver', r => { delete r.caller_page.generation!.commit; r.caller_page.generation!.state = 'missing' }],
  ['replacement indexed before caller', r => { r.caller_page.scope!.commit = 'f'.repeat(40) }],
  ['new focused scope', r => { focusedScope(r, `sha256:${'c'.repeat(64)}`) }],
  ['replaced focused scope', r => { focusedScope(r, `sha256:${'c'.repeat(64)}`); r.caller_page.generation!.unit_digest = `sha256:${'d'.repeat(64)}` }],
  ['removed focused scope', r => { r.caller_page.generation!.unit_digest = `sha256:${'d'.repeat(64)}` }],
])('retains unavailable current scope and noncurrent generation separately: %s', (_, change) => {
  const report = unavailableOperationReport()
  change(report)
  const scope = structuredClone(report.caller_page.scope), generation = structuredClone(report.caller_page.generation)
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
  expect(report.caller_page.scope).toEqual(scope)
  expect(report.caller_page.generation).toEqual(generation)
  expect(report.conclusion.state).toBe('unavailable')
  expect(report.caller_page.rows).toEqual([])
  expect(report.caller_page.total_matching_rows).toBeUndefined()
  expect(report.caller_page.declaration).toBeUndefined()
})

test('current focused evidence requires its exact scope commit and unit digest', () => {
  const report = operationReport(), digest = `sha256:${'c'.repeat(64)}`
  focusedScope(report, digest)
  report.caller_page.generation!.unit_digest = digest
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
  report.caller_page.generation!.unit_digest = `sha256:${'d'.repeat(64)}`
  expect(() => validateOperationImpactReport(report, operationQuery, 50)).toThrow(/authority or bounded response/)
})

test('retains ambiguous candidates, abstention and generated Git SHA256 object IDs', () => {
  const report = operationReport(), row = report.caller_page.rows[0]
  row.unit = { state: 'ambiguous', reason: 'multiple_matching_units', candidate_total: 2, candidates: [{ id: 'unit-a' }, { id: 'unit-b' }] }
  row.classification = 'extractor_abstention'
  delete row.declaration_lineage
  row.resolution = 'unresolved'
  row.tier = 'unresolved'
  row.unresolved_reason = 'unsupported_receiver_flow'
  row.source.object_id = '4'.repeat(64)
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
})

test.each(['build_targets', 'deployables', 'logical_services', 'owners'] as const)('resolved attribution admits one partial %s hop and refuses a conflicting hop', (hop) => {
  const report = operationReport(), row = report.caller_page.rows[0]
  row.unit = { state: 'resolved', candidate_total: 1, candidates: [{ id: 'unit-a', [hop]: ['one'] }] }
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
  row.unit.candidates![0][hop] = ['one', 'two']
  expect(() => validateOperationImpactReport(report, operationQuery, 50)).toThrow(/authority or bounded response/)
  row.unit.state = 'ambiguous'
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
})

test.each(['production', 'test', 'mock', 'generated', 'vendor'])('retains the producer code_role %s', (role) => {
  const report = operationReport()
  report.caller_page.rows[0].code_role = role
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
})

test('zero-valued generation and progress counters may be omitted without inventing work', () => {
  const report = emptyOperationReport(), generation = report.caller_page.generation!
  delete generation.pair_count
  delete generation.result_count
  delete generation.abstention_count
  generation.record_counts = { candidate_records: 0, base_records: 0, excluded_go_test_records: 0 }
  generation.partition_progress = { state: 'complete', total_pair_count: 0 } as never
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
})

test.each([
  { state: 'unavailable' },
  { state: 'partial', settled_pair_count: 1, succeeded_pair_count: 1 },
  { state: 'partial', total_pair_count: 2, settled_pair_count: 1, succeeded_pair_count: 1, refused_pair_count: 0 },
  { state: 'complete', total_pair_count: 1, settled_pair_count: 1, refused_pair_count: 1 },
])('unavailable evidence retains honest progress without a current-generation conclusion: %o', (progress) => {
  const report = unavailableOperationReport()
  report.caller_page.generation!.partition_progress = progress as never
  expect(validateOperationImpactReport(report, operationQuery, 50)).toBe(report)
  expect(report.conclusion.state).toBe('unavailable')
})

test.each<[string, (report: OperationImpactReport) => void]>([
  ['query', (r) => { r.caller_page.query.endpoint.operation = '/shop.Cart/Other' }],
  ['malformed HTTP schema metadata', (r) => { Object.assign(r, { $schema: {} }) }],
  ['oversized HTTP schema metadata', (r) => { Object.assign(r, { $schema: 'x'.repeat(4097) }) }],
  ['conclusion query', (r) => { r.conclusion.query.owner = 'foreign-owner' }],
  ['generation', (r) => { r.conclusion.generation_digest = `sha256:${'f'.repeat(64)}` }],
  ['current scope commit', r => { r.caller_page.scope!.commit = 'f'.repeat(40) }],
  ['current missing scope commit', r => { delete r.caller_page.scope!.commit }],
  ['current removed scope unit', r => { r.caller_page.generation!.unit_digest = `sha256:${'d'.repeat(64)}` }],
  ['unavailable foreign scope', r => { Object.assign(r, unavailableOperationReport()); r.caller_page.scope!.repository = 'foreign.example/repo' }],
  ['unavailable scope commit null', r => { Object.assign(r, unavailableOperationReport()); r.caller_page.scope!.commit = null as never }],
  ['unavailable scope commit object', r => { Object.assign(r, unavailableOperationReport()); r.caller_page.scope!.commit = {} as never }],
  ['unavailable scope commit grammar', r => { Object.assign(r, unavailableOperationReport()); r.caller_page.scope!.commit = 'unknown' }],
  ['unavailable focused unit grammar', r => { Object.assign(r, unavailableOperationReport()); focusedScope(r, 'unknown') }],
  ['source run', (r) => { r.caller_page.rows[0].source.run_id = `sha256:${'f'.repeat(64)}` }],
  ['source blob', (r) => { r.caller_page.rows[0].source.atom_id = `sha256:${'f'.repeat(64)}` }],
  ['missing object', (r) => { delete r.caller_page.rows[0].source.object_id }],
  ['declaration assertion identity', (r) => { delete (r.caller_page.declaration as unknown as Record<string, unknown>).assertion_id; delete (r.caller_page.declaration!.sources[0] as unknown as Record<string, unknown>).assertion_id }],
  ['declaration run identity', (r) => { r.caller_page.declaration!.sources[0].run_id = 'foreign-run' }],
  ['declaration atom identity', (r) => { r.caller_page.declaration!.sources[0].atom_id = '' }],
  ['declaration truncated type', (r) => { r.caller_page.declaration!.sources_truncated = {} as never }],
  ['page bound', (r) => { r.caller_page.rows = Array.from({ length: 51 }, () => r.caller_page.rows[0]) }],
  ['duplicate occurrence', (r) => { r.caller_page.rows.push(r.caller_page.rows[0]); r.caller_page.total_matching_rows = 2; r.caller_page.groups = undefined }],
  ['unit bound', (r) => { r.caller_page.rows[0].unit.candidates = Array.from({ length: 65 }, (_, n) => ({ id: `unit-${n}` })); r.caller_page.rows[0].unit.candidate_total = 65 }],
  ['resolved placeholder count', (r) => { r.caller_page.rows[0].unit = { state: 'resolved', candidates: [], candidate_total: 0 } }],
  ['resolved ID-only placeholder', (r) => { r.caller_page.rows[0].unit.state = 'resolved' }],
  ['code role object', (r) => { r.caller_page.rows[0].code_role = {} as never }],
  ['code role array', (r) => { r.caller_page.rows[0].code_role = ['production'] as never }],
  ['code role vocabulary', (r) => { r.caller_page.rows[0].code_role = 'runtime' }],
  ['unresolved reason object', (r) => { r.caller_page.rows[0].unresolved_reason = {} as never }],
  ['resolved row abstention reason', (r) => { r.caller_page.rows[0].unresolved_reason = 'unresolved_receiver' }],
  ['generation reason object', (r) => { r.caller_page.generation!.reason = {} as never }],
  ['unavailable generation reason object', (r) => { Object.assign(r, unavailableOperationReport()); r.caller_page.generation!.reason = {} as never }],
  ['unavailable generation commit object', (r) => { Object.assign(r, unavailableOperationReport()); r.caller_page.generation!.commit = {} as never }],
  ['unavailable generation count object', (r) => { Object.assign(r, unavailableOperationReport()); r.caller_page.generation!.publication_revision = {} as never }],
  ['unavailable record count object', (r) => { Object.assign(r, unavailableOperationReport()); r.caller_page.generation!.record_counts = { candidate_records: {} } as never }],
  ['generation result count type', (r) => { r.caller_page.generation!.result_count = 'one' as never }],
  ['progress object type', (r) => { r.caller_page.generation!.partition_progress = 'complete' as never }],
  ['progress state vocabulary', (r) => { r.caller_page.generation!.partition_progress!.state = 'ready' as never }],
  ['progress state array', (r) => { r.caller_page.generation!.partition_progress!.state = ['complete'] as never }],
  ['progress count type', (r) => { r.caller_page.generation!.partition_progress!.succeeded_pair_count = {} as never }],
  ['progress count arithmetic', (r) => { r.caller_page.generation!.partition_progress!.succeeded_pair_count = 0 }],
  ['progress total arithmetic', (r) => { r.caller_page.generation!.partition_progress!.total_pair_count = 2 }],
  ['progress generation pair count', (r) => { r.caller_page.generation!.pair_count = 2 }],
  ['current partial progress', (r) => { r.caller_page.generation!.partition_progress = { state: 'partial', settled_pair_count: 0, succeeded_pair_count: 0, refused_pair_count: 0, total_pair_count: 1 } }],
  ['current refused progress', (r) => { r.caller_page.generation!.partition_progress = { state: 'complete', settled_pair_count: 1, succeeded_pair_count: 0, refused_pair_count: 1, total_pair_count: 1 } }],
  ['current unavailable progress', (r) => { r.caller_page.generation!.partition_progress = { state: 'unavailable', settled_pair_count: 0, succeeded_pair_count: 0, refused_pair_count: 0 } }],
  ['promoted syntax', (r) => { r.caller_page.rows[0].tier = 'derived' }],
  ['legacy coverage', (r) => { r.caller_page.coverage_digest = `sha256:${'f'.repeat(64)}` }],
  ['legacy schema', (r) => { r.caller_page.schema_version = 'caller-map-v1' }],
  ['unavailable total', (r) => { Object.assign(r, unavailableOperationReport()); r.caller_page.total_matching_rows = 0 }],
  ['unavailable success', (r) => { Object.assign(r, unavailableOperationReport()); r.conclusion.state = 'exact_empty' }],
  ['unavailable injected declaration', (r) => { const declaration = r.caller_page.declaration; Object.assign(r, unavailableOperationReport()); r.caller_page.declaration = declaration }],
  ['unavailable malformed declaration', (r) => { Object.assign(r, unavailableOperationReport()); r.caller_page.declaration = { sources: {} } as never }],
  ['invented section', (r) => { r.unavailable_sections.saved_report = 'available' as never }],
  ['page-local group count', (r) => { r.caller_page.groups![0].count = 2 }],
])('refuses crossed or inconsistent %s before display', (_, mutate) => {
  const report = operationReport()
  mutate(report)
  expect(() => validateOperationImpactReport(report, operationQuery, 50)).toThrow(/authority or bounded response/)
})
