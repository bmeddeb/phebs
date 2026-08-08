import type {
  ServiceRelationshipCitation,
  ServiceRelationshipPage,
  ServiceRelationshipRow,
  ServiceRelationshipUpstreamAuthority,
} from '../api'

const CITATION_SCHEMA = 'phebs-service-relationship-citation-v1'
const ROOT_SCHEMAS = new Set(['phebs-relationship-root-v1', 'phebs-relationship-root-v2'])
const ROOT_SCHEMA_V2 = 'phebs-relationship-root-v2'
const UNAVAILABLE_SCHEMA = 'phebs-relationship-unavailable-v1'
const UPSTREAM_SCHEMA = 'phebs-downstream-upstream-authority-v1'
const SHA256_DIGEST = /^sha256:[0-9a-f]{64}$/
const GAP_DISPOSITIONS = new Set(['unavailable_prerequisite', 'terminal_refusal', 'retryable'])

/**
 * Validate the generation seam before a product surface treats a root receipt
 * as exact. An unavailable marker is a selected non-authority, not an empty or
 * synthetic root, and therefore carries its own exact upstream identity.
 */
export function validateServiceRelationshipRoot(
  root: ServiceRelationshipPage['roots'][number],
): string {
  if (root.root_schema !== undefined) {
    if (!ROOT_SCHEMAS.has(root.root_schema) || !isDigest(root.generation) ||
        !isDigest(root.root_digest) || !isDigest(root.authority_digest) || root.unavailable !== undefined) {
      return 'relationship root generation authority is invalid'
    }
    if (root.root_schema === ROOT_SCHEMA_V2 &&
        (root.authority?.repository !== root.repository || !validUpstream(root.authority.upstream, root.repository))) {
      return 'relationship v2 upstream authority is invalid'
    }
    return ''
  }

  const marker = root.unavailable
  if (root.state !== 'unavailable' || root.generation !== undefined || root.root_digest !== undefined ||
      root.authority_digest !== undefined || root.authority !== undefined || marker?.schema !== UNAVAILABLE_SCHEMA ||
      marker.reason !== root.reason || !isDigest(marker.digest) ||
      !validUpstream(marker.upstream, root.repository)) {
    return 'relationship unavailable marker authority is invalid'
  }
  return ''
}

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
  const rootError = validateServiceRelationshipRoot(root)
  if (rootError) return rootError
  const span = value.evidence.span
  const expected = row.evidence.span
  if (value.schema !== CITATION_SCHEMA || value.repository !== row.repository ||
      value.root_schema !== root.root_schema || value.generation !== root.generation ||
      value.root_digest !== root.root_digest || value.authority_digest !== root.authority_digest ||
      value.projection.digest !== row.projection_digest || value.projection.posting_digest !== row.posting_digest ||
      value.evidence.posting_digest !== row.evidence.posting_digest || value.evidence.path !== row.evidence.path ||
      value.evidence.object_id !== row.evidence.object_id || value.evidence.content_digest !== row.evidence.content_digest ||
      span.start_byte !== expected.start_byte || span.end_byte !== expected.end_byte ||
      span.start_line !== expected.start_line || span.end_line !== expected.end_line) {
    return 'citation response authority differs from the selected exact relationship row'
  }
  return ''
}

function validUpstream(value: ServiceRelationshipUpstreamAuthority | undefined, repository: string): boolean {
  if (value?.schema !== UPSTREAM_SCHEMA || value.repository !== repository || !isDigest(value.digest)) return false
  const observation = value.observation
  if (!observation || observation.repository !== repository || !observation.version ||
      !isDigest(observation.source_generation_digest) || !isDigest(observation.source_root_digest) ||
      !isDigest(observation.observation_generation_digest) || !isDigest(observation.observation_root_digest) ||
      !isDigest(observation.partition_policy_digest) || !isDigest(observation.observation_policy_digest) ||
      (observation.inventory_policy_digest !== undefined && !isDigest(observation.inventory_policy_digest)) ||
      !Number.isInteger(observation.record_count) || observation.record_count < 0 ||
      !Number.isInteger(observation.observed_count) || observation.observed_count < 0 ||
      observation.observed_count > observation.record_count) return false
  return Number.isInteger(value.required_domain_count) && value.required_domain_count >= 0 &&
    Number.isInteger(value.published_domain_count) && value.published_domain_count >= 0 &&
    value.published_domain_count <= value.required_domain_count && Array.isArray(value.gaps) &&
    value.gaps.length === value.required_domain_count - value.published_domain_count &&
    value.gaps.every((gap) => gap.domain.length > 0 && gap.version.length > 0 && GAP_DISPOSITIONS.has(gap.disposition))
}

function isDigest(value: unknown): value is string {
  return typeof value === 'string' && SHA256_DIGEST.test(value)
}
