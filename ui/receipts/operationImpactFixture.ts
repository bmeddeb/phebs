import { createHash } from 'node:crypto'
import type { Page } from '@playwright/test'
import type { CallerMapCitation, CallerMapQuery, CallerMapRow, OperationImpactReport } from '../src/api'

export type OperationImpactReceiptState = 'current' | 'unavailable'
export const OPERATION_IMPACT_REPOSITORY = 'example.invalid/operation-impact-receipt'
export const OPERATION_IMPACT_OPERATION = '/demo.v1.Cart/Get'
const commit = 'a'.repeat(40)
const declarationPath = 'api/cart.proto'
const sha = (value: string) => createHash('sha256').update(value).digest('hex')
const digest = (value: string) => `sha256:${sha(value)}`
const query: CallerMapQuery = {
  endpoint: {
    protocol: 'protobuf', repository: OPERATION_IMPACT_REPOSITORY,
    declaration_lineage: `provisional_repo_path_v1_${sha(`${OPERATION_IMPACT_REPOSITORY}\0${declarationPath}`)}`,
    operation: OPERATION_IMPACT_OPERATION,
  },
  freshness: 'any', resolution: 'any', ordering: 'source',
}
export const OPERATION_IMPACT_PATH = '/impact?' + new URLSearchParams({
  shape: 'operation_callers', protocol: query.endpoint.protocol,
  repository: OPERATION_IMPACT_REPOSITORY, lineage: query.endpoint.declaration_lineage,
  operation: OPERATION_IMPACT_OPERATION, page_size: '100',
})

const content = 'client.Get(ctx, request)'
const contentBytes = Buffer.from(content)
const blob = createHash('sha1').update(`blob ${contentBytes.length}\0`).update(contentBytes).digest('hex')
const generationDigest = digest('neutral operation receipt generation')
const resolvedUnit = `au_${sha('neutral supplied cart-client unit')}`
const unavailableUnit = `au_${sha('neutral ID-only unit')}`
function row(path: string, attributed: boolean): CallerMapRow {
  return {
    classification: 'resolved_caller', resolution: 'syntax', tier: 'heuristic', protocol: 'protobuf',
    operation: OPERATION_IMPACT_OPERATION, declaration_lineage: query.endpoint.declaration_lineage,
    code_role: 'production', fresh: true,
    unit_group: attributed ? `resolved:${resolvedUnit}` : `unavailable:${unavailableUnit}`,
    unit: attributed
      ? { state: 'resolved', candidates: [{ id: resolvedUnit, build_targets: ['//client:cart'], owners: ['team-cart'] }], candidate_total: 1 }
      : { state: 'unavailable', reason: 'mapping_has_no_attribution', candidates: [{ id: unavailableUnit }], candidate_total: 1 },
    source: {
      repository: OPERATION_IMPACT_REPOSITORY, commit, path, object_id: blob,
      blob_digest: `sha256:${sha(content)}`, atom_id: `sha256:${sha(content)}`,
      assertion_id: digest(path), run_id: generationDigest, plane: 'repository-overlay',
      start_byte: 0, end_byte: contentBytes.length, start_line: 1, end_line: 1,
      citation: `neutral-operation-receipt:${path}`,
    },
  }
}

// Synthetic presentation bytes only. These records never enter a repository,
// signed selection or quality denominator; the actual T54.3 source population
// and ordinary-runtime proof remain separate. IDs/byte spans are deterministic.
function report(state: OperationImpactReceiptState): OperationImpactReport {
  const rows = [row('client/cart.go', true), row('client/metadata.go', false)]
  const declarationID = digest('neutral operation declaration')
  const declarationRun = 'neutral-operation-receipt-declarations'
  const declaration = 'rpc Get (Request) returns (Response);'
  const value: OperationImpactReport = {
    schema_version: 'contract-impact-operation-v1', shape: 'operation_callers',
    conclusion: {
      state: 'evidence_present', query, generation_digest: generationDigest,
      qualification: 'Static caller evidence matches this endpoint scope; per-row resolution applies. This is not runtime impact, completeness or retirement safety.',
    },
    unavailable_sections: {
      field_references: 'not_in_supported_shape', proposed_change: 'not_in_supported_shape',
      implementations: 'not_in_supported_shape', wire_verdict: 'not_in_supported_shape',
      matching_syntactic_calls: 'not_in_supported_shape', saved_report: 'not_in_supported_shape',
    },
    caller_page: {
      schema_version: 'caller-map-v2', query,
      scope: { repository: OPERATION_IMPACT_REPOSITORY, commit, scope_posture: 'whole-repository' },
      declaration: {
        assertion_id: declarationID, run_id: declarationRun, predicate: 'DECLARES_OPERATION',
        object: OPERATION_IMPACT_OPERATION.slice(1), lineage: query.endpoint.declaration_lineage,
        tier: 'exact', sources_truncated: false,
        sources: [{ repository: OPERATION_IMPACT_REPOSITORY, commit, path: declarationPath,
          start_byte: 0, end_byte: Buffer.byteLength(declaration), start_line: 1, end_line: 1,
          assertion_id: declarationID, run_id: declarationRun, atom_id: digest(declaration) }],
      },
      generation: {
        state: 'current', plane: 'repository-overlay', repository: OPERATION_IMPACT_REPOSITORY, commit,
        generation_digest: generationDigest, declaration_set_digest: digest('neutral declarations'),
        candidate_manifest_digest: digest('neutral candidates'), resolver_manifest_digest: digest('neutral resolver'),
        pair_set_digest: digest('neutral pairs'), manifest_digest: digest('neutral publication'),
        publication_revision: 1, pair_count: 1, result_count: 1, abstention_count: 0,
        record_counts: { candidate_records: 2, base_records: 2, excluded_go_test_records: 0 },
        partition_progress: { state: 'complete', settled_pair_count: 1, succeeded_pair_count: 1, refused_pair_count: 0, total_pair_count: 1 },
      },
      matching_rows_state: 'exact', total_matching_rows: rows.length, rows,
      groups: [{ key: `resolved:${resolvedUnit}`, state: 'resolved', count: 1 }, { key: `unavailable:${unavailableUnit}`, state: 'unavailable', count: 1 }],
      pagination: { complete: true },
      caveat: 'Repository-overlay static source evidence only; a complete generation is not runtime use, completeness, migration completion, or decommissioning safety.',
    },
  }
  if (state === 'unavailable') {
    value.caller_page.generation = { state: 'stale', plane: 'repository-overlay', repository: OPERATION_IMPACT_REPOSITORY, commit, reason: 'source_changed' }
    value.caller_page.matching_rows_state = 'unavailable'
    value.caller_page.rows = []
    delete value.caller_page.groups
    delete value.caller_page.declaration
    delete value.caller_page.total_matching_rows
    value.caller_page.caveat = 'Caller totals and absence are unavailable until one exact complete repository-overlay generation is current.'
    value.conclusion.state = 'unavailable'
    delete value.conclusion.generation_digest
    value.conclusion.qualification = 'Caller evidence is unavailable for this generation; no zero-caller or absence conclusion is made.'
  }
  return value
}

export async function installOperationImpactFixture(page: Page, state: OperationImpactReceiptState) {
  const value = report(state)
  const unexpected: string[] = []
  const expected = new URLSearchParams({
    shape: 'operation_callers', protocol: query.endpoint.protocol, repository: OPERATION_IMPACT_REPOSITORY,
    lineage: query.endpoint.declaration_lineage, operation: OPERATION_IMPACT_OPERATION,
    freshness: 'any', resolution: 'any', ordering: 'source', page_size: '100',
  })
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    if (url.pathname === '/api/auth/status') return route.fallback() // existing neutral identity fixture
    const json = (body: unknown) => route.fulfill({ contentType: 'application/json; charset=utf-8', body: JSON.stringify(body) })
    if (route.request().method() === 'GET' && url.pathname === '/api/version' && url.search === '') {
      return json({ version: 'neutral-operation-impact-receipt', capabilities: ['contract-caller-map', 'contract-impact-operation'] })
    }
    if (route.request().method() === 'GET' && url.pathname === '/api/operation_impact_report' &&
      [...url.searchParams].length === [...expected].length && [...expected].every(([key, entry]) => url.searchParams.get(key) === entry)) {
      // Huma's actual transport adds this top-level string; it carries no
      // authority and is included so the fixture exercises the real boundary.
      const body: OperationImpactReport & { $schema: string } = {
        $schema: new URL('/schemas/OperationImpactReport.json', url).href, ...value,
      }
      return json(body)
    }
    const selected = value.caller_page.rows.find(entry => entry.source.citation === url.searchParams.get('citation'))
    if (route.request().method() === 'GET' && url.pathname === '/api/contract_callers/citation' &&
      [...url.searchParams].length === 1 && selected && value.caller_page.generation) {
      const citation: CallerMapCitation = { schema_version: 'caller-map-citation-v1', generation: value.caller_page.generation, source: selected.source, content }
      return json(citation)
    }
    // No fabricated default or live-backend fallthrough for an invented repo.
    unexpected.push(`${route.request().method()} ${url.pathname}${url.search}`)
    return route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: 'unexpected operation impact receipt request' }) })
  })
  return { unexpected }
}
