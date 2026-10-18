import type { CallerMapQuery, OperationImpactReport } from '../api'

export const operationQuery: CallerMapQuery = {
  endpoint: {
    protocol: 'protobuf', repository: 'example.test/shop',
    declaration_lineage: `provisional_repo_path_v1_${'a'.repeat(64)}`,
    operation: '/shop.Cart/Get',
  },
  freshness: 'any', resolution: 'any', ordering: 'source',
}

const digest = (character: string) => `sha256:${character.repeat(64)}`
const commit = 'a'.repeat(40)

/** Transport fixtures, not an independently measured quality population. */
export function operationReport(): OperationImpactReport {
  return {
    schema_version: 'contract-impact-operation-v1', shape: 'operation_callers',
    conclusion: {
      state: 'evidence_present', query: structuredClone(operationQuery),
      generation_digest: digest('b'),
      qualification: 'Static caller evidence matches this endpoint scope; per-row resolution applies. This is not runtime impact, completeness or retirement safety.',
    },
    unavailable_sections: {
      field_references: 'not_in_supported_shape', proposed_change: 'not_in_supported_shape',
      implementations: 'not_in_supported_shape', wire_verdict: 'not_in_supported_shape',
      matching_syntactic_calls: 'not_in_supported_shape', saved_report: 'not_in_supported_shape',
    },
    caller_page: {
      schema_version: 'caller-map-v2', query: structuredClone(operationQuery),
      scope: { repository: operationQuery.endpoint.repository, commit, scope_posture: 'whole-repository' },
      declaration: {
        assertion_id: 'declaration', run_id: 'declaration-run', predicate: 'DECLARES_OPERATION',
        object: 'shop.Cart/Get', lineage: operationQuery.endpoint.declaration_lineage, tier: 'exact',
        sources_truncated: false,
        sources: [{ repository: operationQuery.endpoint.repository, commit, path: 'shop.proto', start_byte: 0, end_byte: 5, start_line: 1, end_line: 1, assertion_id: 'declaration', run_id: 'declaration-run', atom_id: 'declaration-atom' }],
      },
      generation: {
        state: 'current', plane: 'repository-overlay', repository: operationQuery.endpoint.repository, commit,
        generation_digest: digest('b'), declaration_set_digest: digest('c'), candidate_manifest_digest: digest('d'),
        resolver_manifest_digest: digest('e'), pair_set_digest: digest('f'), manifest_digest: digest('0'),
        publication_revision: 1, pair_count: 1, result_count: 1, abstention_count: 0,
        record_counts: { candidate_records: 1, base_records: 1, excluded_go_test_records: 0 },
        partition_progress: { state: 'complete', settled_pair_count: 1, succeeded_pair_count: 1, refused_pair_count: 0, total_pair_count: 1 },
      },
      matching_rows_state: 'exact', total_matching_rows: 1, pagination: { complete: true },
      rows: [{
        classification: 'resolved_caller', resolution: 'syntax', tier: 'heuristic', protocol: 'protobuf',
        operation: operationQuery.endpoint.operation, declaration_lineage: operationQuery.endpoint.declaration_lineage,
        code_role: 'production', fresh: true, unit_group: 'unavailable:mapping_has_no_attribution',
        // Real exact-caller semantics: a supplied ID can survive without any
        // ownership/deployable/service attribution. It must stay unavailable.
        unit: { state: 'unavailable', reason: 'mapping_has_no_attribution', candidates: [{ id: 'au_db69_placeholder' }], candidate_total: 1 },
        source: {
          repository: operationQuery.endpoint.repository, commit, path: 'client.go', object_id: '1'.repeat(40),
          blob_digest: digest('2'), atom_id: digest('2'), assertion_id: digest('3'), run_id: digest('b'),
          plane: 'repository-overlay', start_byte: 0, end_byte: 6, start_line: 1, end_line: 1, citation: 'exact-citation',
        },
      }],
      groups: [{ key: 'unavailable:mapping_has_no_attribution', state: 'unavailable', count: 1 }],
      caveat: 'Static evidence only; current exact source authority does not establish runtime use or completeness.',
    },
  }
}

export function emptyOperationReport(): OperationImpactReport {
  const report = operationReport()
  report.caller_page.rows = []
  report.caller_page.groups = []
  report.caller_page.total_matching_rows = 0
  report.conclusion.state = 'exact_empty'
  report.conclusion.qualification = 'No matching static caller evidence in this exact filtered source scope; this does not establish absence.'
  return report
}

export function unavailableOperationReport(): OperationImpactReport {
  const report = emptyOperationReport()
  report.caller_page.generation = { state: 'stale', plane: 'repository-overlay', repository: operationQuery.endpoint.repository, commit, reason: 'source_changed' }
  report.caller_page.matching_rows_state = 'unavailable'
  delete report.caller_page.declaration
  delete report.caller_page.total_matching_rows
  report.conclusion.state = 'unavailable'
  delete report.conclusion.generation_digest
  report.conclusion.qualification = 'Caller evidence is unavailable for this generation; no zero-caller or absence conclusion is made.'
  return report
}
