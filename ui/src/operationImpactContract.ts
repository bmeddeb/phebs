import type { CallerMapQuery, OperationImpactReport } from './api'

const unavailable = ['field_references', 'proposed_change', 'implementations', 'wire_verdict', 'matching_syntactic_calls', 'saved_report'] as const
const digest = (value: unknown): value is string => typeof value === 'string' && /^sha256:[a-f0-9]{64}$/.test(value)
const commit = (value: unknown): value is string => typeof value === 'string' && /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(value)
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)
const integer = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
const oneOf = (value: unknown, values: readonly string[]): value is string => typeof value === 'string' && values.includes(value)
const encoder = new TextEncoder()
const text = (value: unknown, max = 4096): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && encoder.encode(value).length <= max
const keys = (value: Record<string, unknown>, allowed: readonly string[]) => Object.keys(value).every((key) => allowed.includes(key))
function invalid(): never { throw new Error('Operation impact authority or bounded response is inconsistent. Retry this page.') }

function sameQuery(value: unknown, expected: CallerMapQuery): boolean {
  if (!record(value) || !record(value.endpoint) || !keys(value, ['endpoint', 'unit', 'owner', 'path_prefix', 'code_role', 'tier', 'freshness', 'resolution', 'ordering']) || !keys(value.endpoint, ['protocol', 'repository', 'declaration_lineage', 'operation'])) return false
  for (const key of ['protocol', 'repository', 'declaration_lineage', 'operation'] as const) {
    if (value.endpoint[key] !== expected.endpoint[key]) return false
  }
  for (const key of ['unit', 'owner', 'path_prefix', 'code_role', 'tier', 'freshness', 'resolution', 'ordering'] as const) {
    if ((value[key] ?? '') !== (expected[key] ?? '')) return false
  }
  return true
}

function validateUnit(value: unknown) {
  if (!record(value) || !oneOf(value.state, ['resolved', 'ambiguous', 'unavailable']) || (value.reason !== undefined && !text(value.reason)) || (value.candidate_total !== undefined && !integer(value.candidate_total))) invalid()
  const candidates = value.candidates === undefined ? [] : value.candidates
  const total = value.candidate_total ?? 0
  // An unavailable mapping can retain an ID-only candidate supplied by the
  // committed snapshot. A candidate is not resolved ownership or attribution.
  if (!Array.isArray(candidates) || candidates.length > 64 || !integer(total) || total < candidates.length || (value.state === 'resolved' && (total !== 1 || candidates.length !== 1))) invalid()
  const seen = new Set<string>()
  for (const candidate of candidates) {
    if (!record(candidate) || !text(candidate.id) || seen.has(candidate.id)) invalid()
    seen.add(candidate.id)
    let attributedValues = 0
    for (const key of ['build_targets', 'deployables', 'logical_services', 'owners']) {
      const values = candidate[key] === undefined ? [] : candidate[key]
      if (!Array.isArray(values) || values.length > 64 || values.some((entry) => !text(entry))) invalid()
      // Match extract.unitCandidateState: multiple values in any hop are
      // ambiguous, and an ID alone supplies no attribution.
      if (value.state === 'resolved' && values.length > 1) invalid()
      attributedValues += values.length
    }
    if (value.state === 'resolved' && attributedValues === 0) invalid()
  }
}

function validateProgress(value: unknown, generation: Record<string, unknown>) {
  if (value === undefined) return
  if (!record(value) || !oneOf(value.state, ['complete', 'partial', 'unavailable'])) invalid()
  const settled = value.settled_pair_count === undefined ? 0 : value.settled_pair_count
  const succeeded = value.succeeded_pair_count === undefined ? 0 : value.succeeded_pair_count
  const refused = value.refused_pair_count === undefined ? 0 : value.refused_pair_count
  const total = value.total_pair_count
  if (!integer(settled) || !integer(succeeded) || !integer(refused) || succeeded > settled || refused !== settled - succeeded || (total !== undefined && !integer(total))) invalid()
  if (value.state === 'complete' && total !== settled || value.state === 'partial' && (total === undefined ? settled === 0 : total <= settled) || value.state === 'unavailable' && (total !== undefined || settled !== 0)) invalid()
  const refusals = value.refusals === undefined ? [] : value.refusals
  if (!Array.isArray(refusals) || refusals.length > 32) invalid()
  let refusalOutcomes = 0
  for (const refusal of refusals) {
    if (!record(refusal) || !text(refusal.stage, 256) || refusal.generation_kind !== 'caller' || !oneOf(refusal.classification, ['limit', 'invalid', 'unknown']) || !text(refusal.dimension, 256) || !integer(refusal.observed) || !integer(refusal.limit) || !integer(refusal.outcome_count) || refusal.outcome_count > refused - refusalOutcomes) invalid()
    refusalOutcomes += refusal.outcome_count
  }
  if (refusals.length > 0 && refusalOutcomes !== refused) invalid()
  if (generation.state === 'current' && (value.state !== 'complete' || refused !== 0 || refusals.length !== 0 || total !== (generation.pair_count ?? 0))) invalid()
}

function validateGeneration(value: Record<string, unknown>) {
  if (value.reason !== undefined && !text(value.reason) || value.commit !== undefined && !commit(value.commit)) invalid()
  for (const key of ['unit_digest', 'generation_digest', 'declaration_set_digest', 'candidate_manifest_digest', 'resolver_manifest_digest', 'pair_set_digest', 'manifest_digest']) {
    if (value[key] !== undefined && !digest(value[key])) invalid()
  }
  for (const key of ['publication_revision', 'pair_count', 'result_count', 'abstention_count', 'coverage_record_count', 'covered_candidate_count', 'canonical_bytes', 'excluded_go_test_records']) {
    if (value[key] !== undefined && !integer(value[key])) invalid()
  }
  const counts = value.record_counts
  if (counts !== undefined && (!record(counts) || !integer(counts.candidate_records) || !integer(counts.base_records) || !integer(counts.excluded_go_test_records) || counts.base_records + counts.excluded_go_test_records !== counts.candidate_records)) invalid()
  validateProgress(value.partition_progress, value)
}

function validateScope(value: unknown, generation: Record<string, unknown>) {
  if (!record(value) || value.repository !== generation.repository || value.commit !== generation.commit || !oneOf(value.scope_posture, ['whole-repository', 'focused'])) invalid()
  const unit = value.analysis_unit
  if (unit === undefined) {
    if (value.scope_posture !== 'whole-repository' || generation.unit_digest) invalid()
    return
  }
  if (!record(unit) || !text(unit.schema) || !text(unit.name) || !digest(unit.digest) || unit.digest !== generation.unit_digest || unit.search_index_posture !== value.scope_posture || !text(unit.typed_index_posture)) invalid()
  if (unit.typed_index !== undefined && (!record(unit.typed_index) || !text(unit.typed_index.kind) || !text(unit.typed_index.path))) invalid()
  let paths = 0, bytes = 0
  for (const key of ['primary', 'supporting']) {
    const values = unit[`${key}_paths`] ?? []
    if (!Array.isArray(values) || unit[`${key}_path_count`] !== values.length) invalid()
    for (const path of values) {
      if (!text(path)) invalid()
      paths++; bytes += encoder.encode(path).length
    }
  }
  if (paths > 128 || bytes > 65536) invalid()
}

/** Validates one report at the fetch boundary; no row is rendered on failure.
 * It retains the original page and rows, with no derived classifier or cache.
 */
export function validateOperationImpactReport(value: unknown, expected: CallerMapQuery, pageSize: number): OperationImpactReport {
  if (!record(value) || !keys(value, ['$schema', 'schema_version', 'shape', 'caller_page', 'conclusion', 'unavailable_sections']) || value.$schema !== undefined && !text(value.$schema) || value.schema_version !== 'contract-impact-operation-v1' || value.shape !== 'operation_callers' || expected.endpoint.protocol !== 'protobuf' || !record(value.caller_page) || !record(value.conclusion) || !record(value.unavailable_sections)) invalid()
  const page = value.caller_page, conclusion = value.conclusion, generation = page.generation
  if (page.schema_version !== 'caller-map-v2' || !sameQuery(page.query, expected) || !sameQuery(conclusion.query, expected) || !record(generation) || generation.repository !== expected.endpoint.repository || generation.plane !== 'repository-overlay' || !oneOf(generation.state, ['current', 'missing', 'stale', 'failed']) || !Array.isArray(page.rows) || page.rows.length > pageSize || !integer(pageSize) || pageSize < 1 || pageSize > 100 || !record(page.pagination) || typeof page.pagination.complete !== 'boolean' || !text(page.caveat) || !text(conclusion.qualification) || conclusion.generation_digest !== generation.generation_digest || page.coverage !== undefined || page.coverage_digest !== undefined || page.attribution_digest !== undefined) invalid()
  validateGeneration(generation)
  validateScope(page.scope, generation)
  const sections = value.unavailable_sections
  if (!keys(sections, unavailable) || unavailable.some((key) => sections[key] !== 'not_in_supported_shape')) invalid()
  if (page.matching_rows_state === 'unavailable') {
    if (generation.state === 'current' || page.declaration !== undefined || page.rows.length !== 0 || page.total_matching_rows !== undefined || !page.pagination.complete || page.pagination.next_cursor !== undefined || conclusion.state !== 'unavailable') invalid()
    return value as unknown as OperationImpactReport
  }
  if (page.matching_rows_state !== 'exact' || generation.state !== 'current' || !commit(generation.commit) || !integer(page.total_matching_rows) || page.total_matching_rows > 200000 || page.total_matching_rows < page.rows.length || (page.total_matching_rows > 0 && page.rows.length === 0) || (page.pagination.complete && page.pagination.next_cursor !== undefined) || (!page.pagination.complete && (!text(page.pagination.next_cursor, 16384) || page.rows.length !== pageSize)) || (page.total_matching_rows === 0 && !page.pagination.complete)) invalid()
  for (const key of ['generation_digest', 'declaration_set_digest', 'candidate_manifest_digest', 'resolver_manifest_digest', 'pair_set_digest', 'manifest_digest']) if (!digest(generation[key])) invalid()
  if (!integer(generation.publication_revision) || generation.publication_revision < 1 || !record(generation.record_counts)) invalid()
  if (conclusion.state !== (page.total_matching_rows === 0 ? 'exact_empty' : 'evidence_present')) invalid()
  if (!record(page.declaration) || !text(page.declaration.assertion_id) || !text(page.declaration.run_id) || typeof page.declaration.sources_truncated !== 'boolean' || page.declaration.predicate !== 'DECLARES_OPERATION' || page.declaration.object !== expected.endpoint.operation.slice(1) || page.declaration.lineage !== expected.endpoint.declaration_lineage || !Array.isArray(page.declaration.sources) || page.declaration.sources.length < 1 || page.declaration.sources.length > 16) invalid()
  for (const source of page.declaration.sources) {
    if (!record(source) || source.repository !== generation.repository || source.commit !== generation.commit || source.assertion_id !== page.declaration.assertion_id || source.run_id !== page.declaration.run_id || !text(source.atom_id) || !text(source.path) || !integer(source.start_byte) || !integer(source.end_byte) || source.end_byte <= source.start_byte || !integer(source.start_line) || source.start_line < 1 || !integer(source.end_line) || source.end_line < source.start_line) invalid()
  }
  if (page.groups !== undefined) {
    if (!Array.isArray(page.groups) || page.groups.length > page.rows.length) invalid()
    let total = 0
    for (const group of page.groups) {
      if (!record(group) || !text(group.key) || !text(group.state) || !integer(group.count) || group.count < 1) invalid()
      total += group.count
    }
    if (total !== page.rows.length) invalid()
  }
  const seen = new Set<string>()
  for (const row of page.rows) {
    if (!record(row) || row.protocol !== 'protobuf' || row.operation !== expected.endpoint.operation || !oneOf(row.classification, ['resolved_caller', 'extractor_abstention']) || !oneOf(row.resolution, ['syntax', 'scip', 'unresolved']) || !oneOf(row.tier, ['heuristic', 'derived', 'unresolved']) || row.fresh !== true || !text(row.unit_group) || !record(row.source)) invalid()
    if (row.code_role !== undefined && !oneOf(row.code_role, ['production', 'test', 'mock', 'generated', 'vendor']) || row.unresolved_reason !== undefined && !text(row.unresolved_reason)) invalid()
    if (row.classification === 'resolved_caller' && (row.declaration_lineage !== expected.endpoint.declaration_lineage || !oneOf(row.resolution, ['syntax', 'scip']) || (row.resolution === 'syntax' && row.tier !== 'heuristic') || (row.resolution === 'scip' && row.tier !== 'derived'))) invalid()
    if (row.classification === 'resolved_caller' && row.unresolved_reason !== undefined) invalid()
    if (row.classification === 'extractor_abstention' && (row.declaration_lineage !== undefined || row.resolution !== 'unresolved' || row.tier !== 'unresolved' || !text(row.unresolved_reason))) invalid()
    validateUnit(row.unit)
    const source = row.source
    if (source.repository !== generation.repository || source.commit !== generation.commit || source.run_id !== generation.generation_digest || source.plane !== 'repository-overlay' || !text(source.path) || !commit(source.object_id) || !digest(source.blob_digest) || source.atom_id !== source.blob_digest || !digest(source.assertion_id) || !text(source.citation, 16384) || !integer(source.start_byte) || !integer(source.end_byte) || source.end_byte <= source.start_byte || source.end_byte > 4194304 || !integer(source.start_line) || source.start_line < 1 || !integer(source.end_line) || source.end_line < source.start_line || source.end_line > 4194305) invalid()
    const identity = `${source.repository}\u0000${source.run_id}\u0000${source.assertion_id}`
    if (seen.has(identity)) invalid()
    seen.add(identity)
  }
  return value as unknown as OperationImpactReport
}
