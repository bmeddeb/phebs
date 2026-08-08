import type {
  ServiceRelationshipCitation,
  ServiceRelationshipPage,
  ServiceRelationshipRow,
} from '../api'

const CITATION_SCHEMA = 'phebs-service-relationship-citation-v1'

/**
 * Fail closed when a citation response does not reproduce the exact row and
 * root authority selected by its caller. Both relationship surfaces use this
 * one validator so their authority boundary cannot drift independently.
 */
export function validateServiceRelationshipCitation(
  row: ServiceRelationshipRow,
  root: ServiceRelationshipPage['roots'][number],
  value: ServiceRelationshipCitation,
): string {
  const span = value.evidence.span
  const expected = row.evidence.span
  if (value.schema !== CITATION_SCHEMA || value.repository !== row.repository ||
      value.generation !== root.generation || value.root_digest !== root.root_digest ||
      value.projection.digest !== row.projection_digest || value.projection.posting_digest !== row.posting_digest ||
      value.evidence.posting_digest !== row.evidence.posting_digest || value.evidence.path !== row.evidence.path ||
      value.evidence.object_id !== row.evidence.object_id || value.evidence.content_digest !== row.evidence.content_digest ||
      span.start_byte !== expected.start_byte || span.end_byte !== expected.end_byte ||
      span.start_line !== expected.start_line || span.end_line !== expected.end_line) {
    return 'citation response authority differs from the selected exact relationship row'
  }
  return ''
}
