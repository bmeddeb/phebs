// Wire types for the huma API. Response shapes are generated from the backend
// OpenAPI schema (see src/api.generated.ts, refreshed with `npm run gen:api`);
// the exported aliases below keep the UI's historical names while tracking the
// wire truth, so backend/frontend type drift fails the build instead of
// hiding behind `as` casts.

import type { components } from './api.generated'
import { csrfHeaders, notifyAuthRequired } from './authSession'

export type Schemas = components['schemas']

// Search core (backend: search.Result and friends).
export type Range = Schemas['Range']
export type Chunk = Schemas['Chunk']
export type FileResult = Schemas['FileResult']
export type Stats = Schemas['Stats']
export type SearchResult = Schemas['Result']
export type SearchScopeRevision = Schemas['ScopeRevision']
// Mirrors internal/servicequery.Authority — the exact service authority a
// service-scoped search executed under.
export type ServiceScopeAuthority = Schemas['Authority']
export type SearchScopeReceipt = Schemas['ScopeReceipt']

export interface IndexJob {
  target: string
  status: string
  error?: string
  created_at: string
  finished_at?: string
}

export type IndexedRevision = Schemas['IndexedRevision']

export interface AnalysisUnitTypedIndex {
  kind: string
  path: string
}

// Public committed analysis-unit state returned by /api/repo-status and by
// result-bound scope projections. The selected paths are identities, never
// source content.
export interface AnalysisUnitState {
  schema: string
  name: string
  digest: string
  // The strict transport schema admits null. Valid focused committed state
  // carries a non-empty array, while consumers normalize a defensive null.
  primary_paths: string[] | null
  supporting_paths: string[] | null
  primary_path_count: number
  supporting_path_count: number
  search_index_posture: 'whole-repository' | 'focused'
  typed_index_posture: string
  typed_index?: AnalysisUnitTypedIndex
}

export interface AnalysisScopeProjection {
  repository: string
  commit?: string
  scope_posture: 'whole-repository' | 'focused'
  analysis_unit?: AnalysisUnitState
}

export type RepoStatus = Schemas['RepoStatus']

export type ServiceStatus =
  | 'current'
  | 'stale'
  | 'unavailable'
  | 'conflict'
  | 'removed'

export type ServiceDisposition =
  | 'accepted'
  | 'proposal'
  | 'conflict'
  | 'rejected'

export type ServiceMembershipRole =
  | 'primary'
  | 'supporting'
  | 'shared'
  | 'generated'
  | 'typed'

export type ServiceAuthority = Schemas['ServiceAuthority']

export type ServiceRepository = Schemas['ServiceRepository']

export type ServiceRoleCounts = Schemas['ServiceRoleCounts']

export type ServiceRecord = Schemas['Service']

export type ServiceMembership = Schemas['ServiceMembership']

export type ServicePagination = Schemas['ServicePagination']

export type ServiceInventory = Schemas['ServiceInventory']

export type ServiceDetail = Schemas['ServiceDetail']

export type ServiceRelationshipView = 'all' | 'dependencies' | 'callers' | 'topics'

export interface ServiceRelationshipRoleClaim {
  role: string
  origin: string
}

export interface ServiceRelationshipClaim {
  service_key: string
  disposition: string
  roles: ServiceRelationshipRoleClaim[]
}

export interface ServiceRelationshipPlacement {
  path: string
  unowned: boolean
  claims: ServiceRelationshipClaim[]
}

export interface ServiceRelationshipSpan {
  start_byte: number
  end_byte: number
  start_line: number
  end_line: number
}

export interface ServiceRelationshipEvidence {
  kind: string
  plane: string
  class: string
  reason?: string
  path: string
  object_id: string
  content_digest: string
  span: ServiceRelationshipSpan
  source_role: string
  operation?: string
  candidate_operations?: string[]
  declaration_path?: string
  declaration_lineage?: string
  resolver_record_digests?: string[]
  topic_spelling?: string
  group_id_spelling?: string
  library?: string
  shape?: string
  binding?: string
  posting_digest: string
}

export interface ServiceRelationshipRow {
  repository: string
  service_key: string
  service_incarnation: number
  service_generation: string
  kind: string
  plane: string
  class: string
  lookup_key?: string
  participation: string[]
  counterpart_services: string[]
  projection_digest: string
  posting_digest: string
  source: ServiceRelationshipPlacement
  target?: ServiceRelationshipPlacement
  evidence: ServiceRelationshipEvidence
  citation: string
}

export interface ServiceRelationshipRootReceipt {
  repository: string
  state: 'complete' | 'empty' | 'failed' | 'unavailable'
  reason?: string
  root_schema?: 'phebs-relationship-root-v1' | 'phebs-relationship-root-v2'
  generation?: string
  root_digest?: string
  authority_digest?: string
  authority?: ServiceRelationshipAuthority
  unavailable?: ServiceRelationshipUnavailableAuthority
  service_key: string
  service_incarnation?: number
  service_generation?: string
  reference_count: number
  repository_complete?: boolean
  all_services_complete?: boolean
  failed_service_count?: number
}

export interface ServiceRelationshipAuthority {
  repository: string
  catalog_generation_digest: string
  catalog_digest: string
  catalog_source_generation: string
  service_state_set_digest: string
  service_state_summary_digest?: string
  service_state_control_revision?: number
  observation_generation_digest: string
  observation_manifest_digest: string
  observation_source_digest: string
  resolver_generation_digest: string
  resolver_root_digest: string
  rpc_generation_digest: string
  rpc_root_digest: string
  kafka_generation_digest: string
  kafka_root_digest: string
  policy_digest: string
  upstream?: ServiceRelationshipUpstreamAuthority
}

export interface ServiceRelationshipUpstreamAuthority {
  schema: string
  repository: string
  observation: {
    version: string
    repository: string
    source_generation_digest: string
    source_root_digest: string
    observation_generation_digest: string
    observation_root_digest: string
    partition_policy_digest: string
    observation_policy_digest: string
    inventory_policy_digest?: string
    record_count: number
    observed_count: number
  }
  required_domain_count: number
  published_domain_count: number
  gaps: Array<{
    domain: string
    version: string
    disposition: string
  }>
  digest: string
}

export interface ServiceRelationshipUnavailableAuthority {
  schema: string
  reason: string
  digest: string
  upstream: ServiceRelationshipUpstreamAuthority
}

export interface ServiceRelationshipPage {
  schema: string
  query: {
    repositories: string[]
    service_key: string
    view: ServiceRelationshipView
    kind?: string
    plane?: string
    lookup_key?: string
  }
  rows_state: 'nonempty' | 'empty' | 'gap'
  roots: ServiceRelationshipRootReceipt[]
  rows: ServiceRelationshipRow[]
  coverage: {
    authorized_repositories: number
    complete_roots: number
    empty_roots: number
    failed_roots: number
    unavailable_roots: number
    scanned_references: number
    returned_rows: number
    truncated: boolean
  }
  pagination: {
    order: string
    page_size: number
    returned: number
    next_cursor?: string
  }
  caveat: string
}

export interface ServiceRelationshipCitation {
  schema: string
  repository: string
  root_schema: 'phebs-relationship-root-v1' | 'phebs-relationship-root-v2'
  generation: string
  root_digest: string
  authority_digest: string
  projection: {
    kind: string
    posting_digest: string
    class: string
    plane: string
    lookup_key?: string
    source: ServiceRelationshipPlacement
    target?: ServiceRelationshipPlacement
    digest: string
  }
  evidence: ServiceRelationshipEvidence
  content: string
}

export type SourceFile = Schemas['SourceOutBody']

export type TreeEntry = Schemas['TreeEntry']

export type FolderContents = Schemas['FolderOutBody']

export interface APIKeySummary {
  id: string
  name: string
  prefix: string
  created_at: string
  last_used_at?: string
  expires_at?: string
}

export type LifecycleCompleteness = 'exact' | 'lower_bound' | 'unavailable'
export type LifecyclePressure = 'normal' | 'collect' | 'refuse' | 'unavailable'

export interface LifecycleOwnerStatus {
  name: string
  state: 'not_run' | 'ok' | 'error'
  completeness: LifecycleCompleteness
  scanned: number
  deleted: number
  backlog: boolean
  attempted_at?: string
}

export type LifecycleStatus = Schemas['Status']

export interface CreatedAPIKey {
  key: APIKeySummary
  token: string
}

// T10.1: one append-only audit record
export type AuditEvent = Schemas['AuditEvent']

export type AuditPage = Schemas['AuditOutBody']

// T10.2: local usage aggregates (zero telemetry — computed from local data)
export type AnalyticsSummary = Schemas['AnalyticsOutBody']

export type VersionInfo = Schemas['VersionOutBody']

export interface ProofQuery {
  kind: string
  operation?: string
  lineage?: string
  message?: string
  field_number?: number
  topic?: string
  before_digest?: string
  after_digest?: string
  domains: string[]
}

export interface BundleAssertion {
  id: string
  predicate: string
  subject: string
  object: string
  lineage?: string
  tier: 'exact' | 'derived' | 'heuristic' | 'unresolved'
  code_role?: string
  repo: string
  supporting: string[]
  contradicting?: string[]
  run_id: string
  detail?: string
}

export interface BundleEvidenceAtom {
  atom_id: string
  schema_version: string
  blob_digest: string
  start_byte: number
  end_byte: number
  rule_id: string
  extractor_version: string
  adapter_config_digest: string
  fact_fingerprint: string
  first_seen: string
}

export interface BundleEvidenceOccurrence {
  occurrence_id: string
  atom_id: string
  repo: string
  commit: string
  path: string
  start_line: number
  end_line: number
  visibility_scope: string
  run_id: string
  observed_at: string
}

export interface BundleEvidenceEntry {
  repository: string
  run_id: string
  atom: BundleEvidenceAtom
  occurrences: BundleEvidenceOccurrence[]
}

// KafkaTopicCensus always carries every frozen abstention shape class in
// both planes, zeros included — completeness can never be implied by
// omission. published_runs distinguishes "nothing ran" from "nothing was
// unresolved"; truncated lists "plane:class" entries whose counts are
// lower bounds.
export interface KafkaTopicCensus {
  schema_version: string
  producer: Record<string, number>
  consumer: Record<string, number>
  published_runs: number
  producer_published_runs: number
  consumer_published_runs: number
  truncated?: string[]
}

export interface BundleExtractor {
  repository: string
  domain: string
  run_id: string
  extractor: string
}

export interface ProofBundle {
  schema_version: string
  query: ProofQuery
  assertions: BundleAssertion[]
  evidence: BundleEvidenceEntry[]
  unresolved_census?: KafkaTopicCensus
  coverage: CoverageCertificate
  extractor_versions: BundleExtractor[]
  caveat: string
}

export interface ProofBundleEnvelope {
  id: string
  bundle: ProofBundle
}

export interface CoverageAttempt {
  run_id: string
  commit: string
  unit_digest?: string
  extractor: string
  status: string
  failure?: string
}

export interface CoverageReceiptCounts {
  corpus_files: number
  candidate_files: number
  // Absent on receipt_state 'legacy_exclusion_shape': receipts recorded
  // before these counters existed keep their exact retained wire shape.
  excluded_source_files?: number
  excluded_scip_documents?: number
  excluded_scip_definitions?: number
  excluded_scip_occurrences?: number
  opened_source_attempts: number
  opened_source_files: number
  facts: number
  atoms: number
  assertions: number
  unresolved: number
  staged_chunks: number
  staged_rows: number
}

export interface CoverageReceiptBytes {
  planned_declared: number
  // Absent on receipt_state 'legacy_exclusion_shape'.
  excluded_source_declared?: number
  opened_source: number
}

export interface CoverageReceiptLimits {
  corpus_files: number
  opened_source_attempts: number
  opened_source_files: number
  opened_source_bytes: number
  facts: number
  source_blob_bytes: number
  typed_input_bytes: number
  aggregate_wall_ms: number
  mirror_lock_ms: number
  domain_wall_ms: number
  abort_wall_ms: number
  outcome_wall_ms: number
  max_serial_domains: number
  scheduler_identity_bytes: number
  aggregate_staged_rows: number
  domain_staged_rows: number
}

export type CoverageDomainDisposition =
  | 'published'
  | 'unavailable_prerequisite'
  | 'terminal_generation_refusal'
  | 'retryable_failure'

// T40.1 closed refusal projection (phebs-pipeline-refusal-v1): a source-free
// operational envelope. Observed and limit are meaningful only for the
// 'limit' classification; other classes carry canonical zeroes.
export interface PipelineRefusalReceipt {
  schema: string
  stage: string
  generation_kind: string
  classification: string
  dimension: string
  observed: number
  limit: number
}

export interface CoverageDomainOutcomeReceipt {
  schema: string
  domain: string
  extractor_version: string
  disposition: CoverageDomainDisposition
  reason: string
  inventory_ms: number
  opened_source_ms: number
  extractor_ms: number
  staging_ms: number
  counts: CoverageReceiptCounts
  bytes: CoverageReceiptBytes
  limits: CoverageReceiptLimits
  // Optional on retained v1 receipts, which predate the projection.
  refusal?: PipelineRefusalReceipt
}

export interface CoverageDomainOutcome {
  disposition: CoverageDomainDisposition
  generation_digest: string
  extractor: string
  candidate_control_failure?: boolean
  receipt_schema: string
  receipt_state: 'full' | 'schema_only' | 'legacy_exclusion_shape'
  receipt?: CoverageDomainOutcomeReceipt
}

export interface CoverageTypedInput {
  kind: string
  path?: string
  object_id?: string
  declared_bytes: number
  digest?: string
  present: boolean
}

export interface CoverageCandidateScope {
  manifest_digest: string
  plane: string
  corpus_file_count: number
  corpus_declared_bytes: number
  corpus_digest: string
  planned_file_count: number
  planned_required_file_count: number
  planned_declared_bytes: number
  planned_digest: string
  // Added by coverage-certificate-v3. Optional keeps retained v2 bundles
  // readable without inventing zero-valued partition claims.
  base_source_file_count?: number
  excluded_go_test_file_count?: number
  excluded_source_file_count?: number
  excluded_source_required_count?: number
  excluded_source_declared_bytes?: number
  excluded_scip_document_count?: number
  excluded_scip_definition_count?: number
  excluded_scip_occurrence_count?: number
  typed_input?: CoverageTypedInput
}

export interface CoverageRun {
  domain: string
  status: string
  run_id?: string
  extractor?: string
  commit?: string
  unit_digest?: string
  fresh: boolean
  protocols?: string[]
  failures?: string[]
  corpus_file_count: number
  candidate_file_count: number
  read_file_count: number
  read_bytes: number
  source_scope_digest?: string
  unresolved_count: number
  assertion_count: number
  atom_count: number
  // Absent inventory_policy identifies a legacy run whose gitlink boundary
  // status is unknown — never render the absent count as zero.
  inventory_policy?: string
  gitlink_count?: number
  gitlink_digest?: string
  gitlink_sample_paths?: string[]
  gitlink_sample_truncated?: boolean
  evidence_scope_posture?: string
  candidate_scope?: CoverageCandidateScope
  latest_attempt?: CoverageAttempt
  outcome?: CoverageDomainOutcome
}

export interface CoverageAnalysisUnit {
  name: string
  digest: string
  primary_paths: string[] | null
  supporting_paths: string[] | null
  typed_index_posture: string
  typed_index_kind?: string
  typed_index_path?: string
}

export interface CoverageRepository {
  repository: string
  indexed_commit?: string
  scope_posture?: 'whole-repository' | 'focused'
  analysis_unit?: CoverageAnalysisUnit
  scip_index: string
  runs: CoverageRun[]
}

export interface CoverageCertificate {
  schema_version: string
  domains: string[]
  repository_count: number
  repositories: CoverageRepository[]
  digest: string
}

export interface ContractCatalogSource {
  repository: string
  commit: string
  path: string
  start_byte: number
  end_byte: number
  start_line: number
  end_line: number
  assertion_id: string
  run_id: string
  atom_id: string
}

export interface ContractCatalogClaim {
  assertion_id: string
  run_id: string
  predicate: string
  object: string
  lineage?: string
  tier: string
  code_role?: string
  detail?: Record<string, unknown>
  sources: ContractCatalogSource[]
  sources_truncated: boolean
}

export interface ContractCatalogItem {
  kind: 'service' | 'operation'
  protocol: string
  repository: string
  declaration_lineage: string
  package?: string
  service_fqn: string
  method?: string
  operation?: string
  declaration: ContractCatalogClaim
}

export interface ContractCatalogPagination {
  complete: boolean
  truncated: boolean
  reason?: string
  next_cursor?: string
}

export interface ContractCatalogList {
  schema_version: string
  query: {
    repository?: string
    package?: string
    protocol?: string
    lineage?: string
  }
  items: ContractCatalogItem[]
  pagination: ContractCatalogPagination
  coverage_digest: string
  coverage: CoverageCertificate
  caveat: string
}

export interface ContractCatalogImportContext {
  count: number
  digest: string
  paths?: string[]
  truncated: boolean
}

export interface ContractCatalogTypeReference {
  raw: string
  kind?: string
  resolution: string
  declaration?: string
  reason?: string
  imports?: ContractCatalogImportContext
}

export interface ContractCatalogMapShape {
  key: ContractCatalogTypeReference
  value: ContractCatalogTypeReference
}

export interface ContractCatalogFieldDetail {
  schema: string
  name: string
  type?: ContractCatalogTypeReference
  cardinality: string
  map?: ContractCatalogMapShape
  oneof?: string
}

export interface ContractCatalogOperationFactDetail {
  schema: string
  request: ContractCatalogTypeReference
  response: ContractCatalogTypeReference
  // Protobuf operations carry streaming flags; Thrift operations carry
  // oneway. The absent family is undefined, which OperationDetail uses to
  // pick the right chip set.
  client_streaming?: boolean
  server_streaming?: boolean
  oneway?: boolean
}

export interface ContractCatalogFieldShape {
  object: string
  field_number: number
  detail: ContractCatalogFieldDetail
  declaration: ContractCatalogClaim
  nested?: ContractCatalogMessage
}

export interface ContractCatalogMessage {
  raw: string
  state: 'resolved' | 'unresolved' | 'cycle' | 'depth_limit' | 'node_limit'
  reason?: string
  declaration_name?: string
  kind?: 'struct' | 'union' | 'exception'
  synthetic?: boolean
  declaration?: ContractCatalogClaim
  fields?: ContractCatalogFieldShape[]
  truncated: boolean
  truncation_reasons?: string[]
}

export interface ContractCatalogRelationship {
  kind: 'implementation' | 'caller' | 'unresolved_candidate'
  classification: string
  reason?: string
  claim: ContractCatalogClaim
}

export interface ContractCatalogOperation {
  schema_version: string
  repository: string
  declaration_lineage: string
  service_fqn: string
  method: string
  operation: string
  declaration: ContractCatalogClaim
  fact_detail: ContractCatalogOperationFactDetail
  request: ContractCatalogMessage
  response: ContractCatalogMessage
  implementations: ContractCatalogRelationship[]
  callers: ContractCatalogRelationship[]
  unresolved_candidates: ContractCatalogRelationship[]
  relationships_truncated: boolean
  relationship_limit_reason?: string
  shape_truncated: boolean
  coverage_digest: string
  coverage: CoverageCertificate
  caveat: string
}

export interface CallerMapEndpoint {
  protocol: string
  repository: string
  declaration_lineage: string
  operation: string
}

export interface CallerMapQuery {
  endpoint: CallerMapEndpoint
  unit?: string
  owner?: string
  path_prefix?: string
  code_role?: string
  tier?: string
  freshness: 'any' | 'fresh' | 'stale'
  resolution: 'any' | 'scip' | 'syntax' | 'unresolved'
  ordering: 'source' | 'unit'
}

export interface CallerMapUnitCandidate {
  id: string
  build_targets?: string[]
  deployables?: string[]
  logical_services?: string[]
  owners?: string[]
}

export interface CallerMapUnitAttribution {
  state: string
  reason?: string
  candidates?: CallerMapUnitCandidate[]
  // Pre-truncation count: the server serializes at most 64 candidates per
  // row so one ambiguous unit cannot breach the frozen DOM bound.
  candidate_total?: number
}

export interface CallerMapSource {
  repository: string
  commit: string
  path: string
  object_id?: string
  blob_digest?: string
  plane?: 'repository-overlay'
  start_byte: number
  end_byte: number
  start_line: number
  end_line: number
  assertion_id: string
  run_id: string
  atom_id: string
  // Opaque capability for the dedicated exact-range endpoint. It does not
  // grant generic repository browsing or whole-file source access.
  citation?: string
}

export type CallerMapGenerationState =
  | 'missing'
  | 'stale'
  | 'failed'
  | 'current'

export interface CallerMapGeneration {
  state: CallerMapGenerationState
  reason?: string
  plane: 'repository-overlay'
  repository: string
  commit?: string
  unit_digest?: string
  generation_digest?: string
  declaration_set_digest?: string
  candidate_manifest_digest?: string
  resolver_manifest_digest?: string
  pair_set_digest?: string
  manifest_digest?: string
  publication_revision?: number
  pair_count?: number
  result_count?: number
  abstention_count?: number
  canonical_bytes?: number
  record_counts?: {
    candidate_records: number
    base_records: number
    excluded_go_test_records: number
  }
  // Retained for pre-T30.7 responses. Current exact generations publish the
  // grouped record_counts object so explicit zeroes remain distinguishable
  // from unavailable authority.
  excluded_go_test_records?: number
  partition_progress?: {
    state: 'complete' | 'partial' | 'unavailable'
    settled_pair_count: number
    succeeded_pair_count: number
    refused_pair_count: number
    total_pair_count?: number
  }
}

export interface CallerMapCitation {
  schema_version: 'caller-map-citation-v1'
  generation: CallerMapGeneration
  source: CallerMapSource
  // The server returns only source.start_byte through source.end_byte from the
  // immutable, object-ID- and digest-verified blob.
  content: string
}

export interface CallerMapRow {
  classification: 'resolved_caller' | 'extractor_abstention'
  resolution: string
  protocol: string
  operation: string
  declaration_lineage?: string
  tier: string
  code_role?: string
  fresh: boolean
  unit_group: string
  unit: CallerMapUnitAttribution
  source: CallerMapSource
  unresolved_reason?: string
}

export interface CallerMapGroup {
  key: string
  state: string
  count: number
}

export interface CallerMapPage {
  schema_version: string
  query: CallerMapQuery
  declaration?: ContractCatalogClaim
  rows: CallerMapRow[]
  groups?: CallerMapGroup[]
  total_matching_rows?: number
  pagination: {
    complete: boolean
    next_cursor?: string
  }
  scope?: AnalysisScopeProjection
  generation?: CallerMapGeneration
  matching_rows_state?: 'exact' | 'unavailable'
  coverage_digest?: string
  attribution_digest?: string
  coverage?: CoverageCertificate
  caveat: string
}

export interface CallerComparisonQuery {
  old: CallerMapEndpoint
  replacement: CallerMapEndpoint
  unit?: string
  owner?: string
  path_prefix?: string
  code_role?: string
  tier?: string
  freshness: 'any' | 'fresh' | 'stale'
  resolution: 'any' | 'scip' | 'syntax' | 'unresolved'
  ordering: 'source' | 'unit'
  level: 'occurrence' | 'unit'
  classification?: 'old_only_evidence' | 'both_evidence' | 'new_only_evidence' | 'unresolved'
}

interface CallerComparisonSnapshotBase {
  endpoint: CallerMapEndpoint
  declaration?: ContractCatalogClaim
  // Retained only for compatibility with historical v1 payload fixtures.
  coverage_digest?: string
  attribution_digest?: string
}

export interface CallerComparisonExactSnapshot extends CallerComparisonSnapshotBase {
  generation: CallerMapGeneration
  matching_rows_state: 'exact' | 'unavailable'
}

export interface CallerComparisonLegacySnapshot extends CallerComparisonSnapshotBase {
  generation?: undefined
  matching_rows_state?: undefined
}

export type CallerComparisonSnapshot =
  | CallerComparisonExactSnapshot
  | CallerComparisonLegacySnapshot

export interface CallerComparisonSide {
  occurrence_count: number
  rows: CallerMapRow[]
  rows_truncated: boolean
}

export interface CallerComparisonRow {
  level: 'occurrence' | 'unit'
  key: string
  classification: 'old_only_evidence' | 'both_evidence' | 'new_only_evidence' | 'unresolved'
  unit?: CallerMapUnitCandidate
  old: CallerComparisonSide
  replacement: CallerComparisonSide
}

interface CallerComparisonPageBase {
  query: CallerComparisonQuery
  old: CallerComparisonSnapshot
  replacement: CallerComparisonSnapshot
  rows: CallerComparisonRow[]
  pagination: {
    complete: boolean
    next_cursor?: string
  }
  caveat: string
}

export interface CallerComparisonExactPage extends CallerComparisonPageBase {
  schema_version: 'caller-comparison-v2'
  old: CallerComparisonExactSnapshot
  replacement: CallerComparisonExactSnapshot
  // Omitted when either side has no current complete generation. In
  // particular, an unavailable comparison never serializes a misleading 0.
  total_rows?: number
  matching_rows_state: 'exact' | 'unavailable'
  coverage?: undefined
}

export interface CallerComparisonLegacyPage extends CallerComparisonPageBase {
  schema_version: 'caller-comparison-v1'
  old: CallerComparisonLegacySnapshot
  replacement: CallerComparisonLegacySnapshot
  total_rows: number
  matching_rows_state?: undefined
  coverage?: CoverageCertificate
}

export type CallerComparisonPage =
  | CallerComparisonExactPage
  | CallerComparisonLegacyPage

export interface ImpactEvidenceRow {
  kind: 'operation_call' | 'field_reference' | 'unresolved_candidate'
  domain: string
  protocol?: 'protobuf' | 'thrift'
  assertion_id: string
  evidence_atom_id: string
  predicate: string
  object: string
  lineage?: string
  repository: string
  commit: string
  path: string
  start_byte: number
  end_byte: number
  start_line: number
  end_line: number
  tier: string
  code_role?: string
  classification: string
  reason?: string
  fresh: boolean
}

export interface ImpactCoverageRow {
  repository: string
  domain: string
  state: string
  reason?: string
  indexed_commit?: string
  evidence_commit?: string
  run_id?: string
  extractor?: string
  protocols?: string[]
  failures?: string[]
  assertion_count: number
  unresolved_count: number
  candidate_file_count: number
  read_file_count: number
  source_scope_digest?: string
}

export interface CompatibilityFile {
  path: string
  content: string
}

export interface CompatibilityRequest {
  lineage: string
  before: CompatibilityFile[]
  after: CompatibilityFile[]
}

export interface CompatibilityViolation {
  snapshot: string
  path: string
  start_line: number
  start_column: number
  end_line: number
  end_column: number
  rule: string
  message: string
  affected_field?: { lineage: string; message: string; field_number: number }
}

export interface CompatibilityResult {
  compatible: boolean
  before: { digest: string; files: { path: string; digest: string }[] }
  after: { digest: string; files: { path: string; digest: string }[] }
  violations: CompatibilityViolation[]
  affected_fields: { lineage: string; message: string; field_number: number }[]
  extraction_run: {
    engine: string
    version: string
    policy: string
    arguments: string[]
    exit_code: number
    result: string
  }
}

export interface ContractImpactReport {
  schema_version: string
  bundle_id: string
  query: ProofQuery
  conclusion: { text: string; coverage_digest: string }
  resolved_evidence: ImpactEvidenceRow[]
  matching_call_evidence: ImpactEvidenceRow[]
  extractor_abstentions: ImpactEvidenceRow[]
  compatibility?: CompatibilityResult
  coverage: CoverageCertificate
  coverage_rows: ImpactCoverageRow[]
  caveat: string
}

export interface GitIdentity {
  name: string
  email: string
  time: string
}

export type GitCommit = Schemas['GitCommit']

export type BlameLine = Schemas['BlameLine']

export type BlameResult = Schemas['BlameResult']

export type CommitListResult = Schemas['CommitListResult']

export interface GitFileChange {
  status: string
  path: string
  old_path?: string
  similarity?: number
  additions?: number
  deletions?: number
  binary?: boolean
}

export type CommitResult = Schemas['CommitResult']

export type DiffResult = Schemas['DiffResult']

export type PositionEncoding = 'utf8' | 'utf16' | 'utf32'

export interface CodePosition {
  line: number
  character: number
}

export interface CodeRange {
  start: CodePosition
  end: CodePosition
}

export interface CodeLocation {
  repo: string
  revision: string
  path: string
  range: CodeRange
  encoding: PositionEncoding
}

export type DefinitionResult = Schemas['DefinitionResult']

export type ReferencesResult = Schemas['ReferencesResult']

export type HoverInfo = Schemas['HoverInfo']

export type HoverResult = Schemas['HoverResult']

async function request(url: string, init: RequestInit = {}): Promise<Response> {
  const res = await fetch(url, { credentials: 'same-origin', ...init })
  if (res.status === 401) notifyAuthRequired()
  return res
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await request(url, { signal })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`${res.status}: ${body}`)
  }
  return res.json() as Promise<T>
}

const query = (values: Record<string, string | number | boolean | undefined>) =>
  new URLSearchParams(
    Object.entries(values)
      .filter(([, value]) => value !== '' && value !== undefined)
      .map(([key, value]) => [key, String(value)]),
  ).toString()

export const fetchRepoStatus = (signal?: AbortSignal) =>
  getJSON<RepoStatus[]>('/api/repo-status', signal)

export const fetchServiceInventory = (
  values: {
    repository: string
    status?: ServiceStatus
    disposition?: ServiceDisposition
    include_removed?: boolean
    page_size?: number
    cursor?: string
  },
  signal?: AbortSignal,
) => getJSON<ServiceInventory>(`/api/services?${query(values)}`, signal)

export const fetchServiceDetail = (
  repository: string,
  serviceKey: string,
  signal?: AbortSignal,
) => getJSON<ServiceDetail>(
  `/api/service?${query({ repository, service_key: serviceKey })}`,
  signal,
)

export const fetchServiceRelationships = (
  values: {
    repository?: string
    service_key: string
    view: ServiceRelationshipView
    kind?: 'rpc' | 'kafka'
    plane?: string
    lookup_key?: string
    page_size?: number
    cursor?: string
  },
  signal?: AbortSignal,
) => getJSON<ServiceRelationshipPage>(
  `/api/service-relationships?${query(values)}`,
  signal,
)

export const fetchServiceRelationshipCitation = (
  citation: string,
  signal?: AbortSignal,
) => getJSON<ServiceRelationshipCitation>(
  `/api/service-relationship-citation?${query({ citation })}`,
  signal,
)

export const fetchFolderContents = (
  repo: string,
  ref: string,
  path: string,
  signal?: AbortSignal,
) =>
  getJSON<FolderContents>(
    `/api/folder_contents?${query({ repo, ref, path })}`,
    signal,
  )

export const fetchSource = (
  repo: string,
  path: string,
  ref: string,
  signal?: AbortSignal,
) =>
  getJSON<SourceFile>(
    `/api/source?${query({ repo, path, ref })}`,
    signal,
  )

export const postReindex = (repo: string, force: boolean) =>
  request('/api/reindex', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
    body: JSON.stringify({ repo, force }),
  }).then((res) => {
    if (!res.ok) throw new Error(`reindex failed: ${res.status}`)
  })

export const fetchAudit = (offset: number, limit = 50, signal?: AbortSignal) =>
  getJSON<AuditPage>(`/api/audit?${query({ offset, limit })}`, signal)

export const fetchAnalytics = (days = 30, signal?: AbortSignal) =>
  getJSON<AnalyticsSummary>(`/api/analytics?${query({ days })}`, signal)

export const fetchVersion = (signal?: AbortSignal) =>
  getJSON<VersionInfo>('/api/version', signal)

export const fetchContractCatalog = (
  filters: {
    repository?: string
    package?: string
    protocol?: string
    lineage?: string
  },
  pageSize = 50,
  cursor = '',
  signal?: AbortSignal,
) => getJSON<ContractCatalogList>(
  `/api/contract_atlas?${query({
    ...filters,
    page_size: pageSize,
    cursor,
  })}`,
  signal,
)

export const fetchContractOperation = (
  repository: string,
  lineage: string,
  operation: string,
  signal?: AbortSignal,
) => getJSON<ContractCatalogOperation>(
  `/api/contract_atlas/operation?${query({ repository, lineage, operation })}`,
  signal,
)

export const fetchContractCallers = (
  endpoint: CallerMapEndpoint,
  filters: {
    unit?: string
    owner?: string
    path_prefix?: string
    code_role?: string
    tier?: string
    freshness?: 'any' | 'fresh' | 'stale'
    resolution?: 'any' | 'scip' | 'syntax' | 'unresolved'
    ordering?: 'source' | 'unit'
  },
  pageSize = 50,
  cursor = '',
  signal?: AbortSignal,
) => getJSON<CallerMapPage>(
  `/api/contract_callers?${query({
    protocol: endpoint.protocol,
    repository: endpoint.repository,
    lineage: endpoint.declaration_lineage,
    operation: endpoint.operation,
    ...filters,
    page_size: pageSize,
    cursor,
  })}`,
  signal,
)

export const fetchCallerCitation = (
  citation: string,
  signal?: AbortSignal,
) => getJSON<CallerMapCitation>(
  `/api/contract_callers/citation?${query({ citation })}`,
  signal,
)

export const fetchCallerComparison = (
  oldEndpoint: CallerMapEndpoint,
  replacementEndpoint: CallerMapEndpoint,
  filters: {
    unit?: string
    owner?: string
    path_prefix?: string
    code_role?: string
    tier?: string
    freshness?: 'any' | 'fresh' | 'stale'
    resolution?: 'any' | 'scip' | 'syntax' | 'unresolved'
    ordering?: 'source' | 'unit'
    level?: 'occurrence' | 'unit'
    classification?: 'old_only_evidence' | 'both_evidence' | 'new_only_evidence' | 'unresolved'
  },
  pageSize = 50,
  cursor = '',
  signal?: AbortSignal,
) => getJSON<CallerComparisonPage>(
  `/api/compare_operation_callers?${query({
    old_protocol: oldEndpoint.protocol,
    old_repository: oldEndpoint.repository,
    old_lineage: oldEndpoint.declaration_lineage,
    old_operation: oldEndpoint.operation,
    replacement_protocol: replacementEndpoint.protocol,
    replacement_repository: replacementEndpoint.repository,
    replacement_lineage: replacementEndpoint.declaration_lineage,
    replacement_operation: replacementEndpoint.operation,
    ...filters,
    page_size: pageSize,
    cursor,
  })}`,
  signal,
)

export const fetchOperationImpact = (operation: string, signal?: AbortSignal) =>
  getJSON<ContractImpactReport>(
    `/api/contract_impact_report?${query({ operation })}`,
    signal,
  )

export const fetchFieldImpact = (
  lineage: string,
  message: string,
  fieldNumber: number,
  signal?: AbortSignal,
) => getJSON<ContractImpactReport>(
  `/api/contract_impact_report?${query({ lineage, message, field_number: fieldNumber })}`,
  signal,
)

export const fetchSavedImpact = (id: string, signal?: AbortSignal) =>
  getJSON<ContractImpactReport>(`/api/contract_impact_reports/${encodeURIComponent(id)}`, signal)

export const fetchKafkaTopicUsage = (topic: string, signal?: AbortSignal) =>
  getJSON<ProofBundleEnvelope>(`/api/find_kafka_topic_usage?${query({ topic })}`, signal)

export async function postChangeImpact(requestBody: CompatibilityRequest, signal?: AbortSignal): Promise<ContractImpactReport> {
  const res = await request('/api/contract_impact_report', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
    body: JSON.stringify(requestBody),
    signal,
  })
  if (!res.ok) throw new Error((await res.text()) || `build impact report failed (${res.status})`)
  return res.json() as Promise<ContractImpactReport>
}

export const fetchAPIKeys = (signal?: AbortSignal) =>
  getJSON<{ keys: APIKeySummary[] }>('/api/auth/keys', signal)

export const fetchLifecycleStatus = (signal?: AbortSignal) =>
  getJSON<LifecycleStatus>('/api/lifecycle-status', signal)

export async function createAPIKey(name: string): Promise<CreatedAPIKey> {
  const res = await request('/api/auth/keys', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
    body: JSON.stringify({ name }),
  })
  if (!res.ok) throw new Error((await res.text()) || `create key failed (${res.status})`)
  return res.json() as Promise<CreatedAPIKey>
}

export async function revokeAPIKey(id: string): Promise<void> {
  const res = await request(`/api/auth/keys/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: csrfHeaders(),
  })
  if (!res.ok) throw new Error((await res.text()) || `revoke key failed (${res.status})`)
}

export const fetchBlame = (repo: string, path: string, ref: string, signal?: AbortSignal) =>
  getJSON<BlameResult>(`/api/blame?${query({ repo, path, ref })}`, signal)

export const fetchCommits = (
  repo: string,
  ref: string,
  path: string,
  limit = 50,
  offset = 0,
  signal?: AbortSignal,
) => getJSON<CommitListResult>(
  `/api/commits?${query({ repo, ref, path, limit, offset })}`,
  signal,
)

export const fetchCommit = (repo: string, ref: string, signal?: AbortSignal) =>
  getJSON<CommitResult>(`/api/commit?${query({ repo, ref })}`, signal)

export const fetchDiff = (
  repo: string,
  head: string,
  base = '',
  path = '',
  signal?: AbortSignal,
) => getJSON<DiffResult>(`/api/diff?${query({ repo, head, base, path })}`, signal)

const codePositionQuery = (
  repo: string,
  ref: string,
  path: string,
  line: number,
  character: number,
) => query({ repo, ref, path, line, character, encoding: 'utf16' })

export const fetchDefinition = (
  repo: string,
  ref: string,
  path: string,
  line: number,
  character: number,
  signal?: AbortSignal,
) => getJSON<DefinitionResult>(
  `/api/find_definitions?${codePositionQuery(repo, ref, path, line, character)}`,
  signal,
)

export const fetchReferences = (
  repo: string,
  ref: string,
  path: string,
  line: number,
  character: number,
  signal?: AbortSignal,
) => getJSON<ReferencesResult>(
  `/api/find_references?${codePositionQuery(repo, ref, path, line, character)}`,
  signal,
)

export const fetchHover = (
  repo: string,
  ref: string,
  path: string,
  line: number,
  character: number,
  signal?: AbortSignal,
) => getJSON<HoverResult>(
  `/api/hover?${codePositionQuery(repo, ref, path, line, character)}`,
  signal,
)

// streamSearch subscribes to /api/stream_search; each `results` batch is
// delivered as it arrives (T4.3 per-shard flush shows up here as
// incremental rendering).
export function streamSearch(
  q: string,
  onBatch: (r: SearchResult) => void,
  onDone: (s: Stats) => void,
  onError: (msg: string) => void,
  scope: { kind: 'all_code' | 'service'; repository?: string; serviceKey?: string } = { kind: 'all_code' },
  onScope: (receipt: SearchScopeReceipt) => void = () => {},
): () => void {
  const params = new URLSearchParams({ q, scope: scope.kind })
  if (scope.repository) params.set('repository', scope.repository)
  if (scope.serviceKey) params.set('service_key', scope.serviceKey)
  const es = new EventSource(`/api/stream_search?${params.toString()}`)
  let ended = false
  const fail = (message: string) => {
    if (ended) return
    ended = true
    es.close()
    onError(message)
  }
  es.addEventListener('results', (e) => {
    if (ended) return
    const payload = parseEvent(e.data)
    if (!isSearchResult(payload)) {
      fail('invalid search results event')
      return
    }
    onBatch(payload)
  })
  es.addEventListener('done', (e) => {
    if (ended) return
    const payload = parseEvent(e.data)
    if (!isStats(payload)) {
      fail('invalid search done event')
      return
    }
    ended = true
    es.close()
    onDone(payload)
  })
  es.addEventListener('scope', (e) => {
    if (ended) return
    const payload = parseEvent(e.data)
    if (!isSearchScopeReceipt(payload)) {
      fail('invalid search scope event')
      return
    }
    onScope(payload)
  })
  // A single 'error' handler: EventSource fires this event type for BOTH a
  // server-sent `event: error` (has e.data — a real backend message) and a
  // connection-level failure (no data). A separate es.onerror would fire for
  // the same event and clobber the real message with 'connection lost'.
  es.addEventListener('error', (e: MessageEvent) => {
    if (ended) return
    if (e.data) {
      const payload = parseEvent(e.data)
      fail(
        isRecord(payload) && typeof payload.message === 'string' && payload.message
          ? payload.message
          : 'search failed',
      )
    } else {
      fail('connection lost')
    }
  })
  return () => {
    ended = true
    es.close()
  }
}

function parseEvent(data: string): unknown {
  try {
    return JSON.parse(data)
  } catch {
    return null
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function isStats(value: unknown): value is Stats {
  return (
    isRecord(value) &&
    isFiniteNumber(value.match_count) &&
    isFiniteNumber(value.file_count) &&
    isFiniteNumber(value.duration_ms)
  )
}

function isRange(value: unknown): value is Range {
  return (
    isRecord(value) &&
    isFiniteNumber(value.start_line) &&
    isFiniteNumber(value.start_col) &&
    isFiniteNumber(value.end_line) &&
    isFiniteNumber(value.end_col)
  )
}

function isChunk(value: unknown): value is Chunk {
  return (
    isRecord(value) &&
    typeof value.content === 'string' &&
    isFiniteNumber(value.start_line) &&
    Array.isArray(value.ranges) &&
    value.ranges.every(isRange)
  )
}

function isFileResult(value: unknown): value is FileResult {
  return (
    isRecord(value) &&
    typeof value.repo === 'string' &&
    typeof value.path === 'string' &&
    typeof value.ref === 'string' &&
    (value.language === undefined || typeof value.language === 'string') &&
    Array.isArray(value.chunks) &&
    value.chunks.every(isChunk)
  )
}

function isSearchResult(value: unknown): value is SearchResult {
  return (
    isRecord(value) &&
    Array.isArray(value.files) &&
    value.files.every(isFileResult) &&
    isStats(value.stats)
  )
}

// Fail-closed (charter §2): a receipt that cannot carry its own authority is
// rejected here, so the drawer never renders placeholder identity.
const isSHA256Digest = (value: unknown): value is string =>
  typeof value === 'string' && /^sha256:[0-9a-f]{64}$/.test(value)
const isCount = (value: unknown): value is number =>
  typeof value === 'number' && Number.isInteger(value) && value >= 0
const isNonEmptyString = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0

function isServiceScopeAuthority(value: unknown): value is ServiceScopeAuthority {
  return (
    isRecord(value) &&
    isNonEmptyString(value.schema) &&
    isNonEmptyString(value.repository) &&
    isNonEmptyString(value.service_key) &&
    isNonEmptyString(value.status) &&
    isCount(value.incarnation) &&
    isNonEmptyString(value.revision_commit) &&
    isSHA256Digest(value.expression_digest) &&
    isNonEmptyString(value.current_catalog_generation) &&
    isNonEmptyString(value.active_catalog_generation) &&
    isNonEmptyString(value.active_source_generation) &&
    isNonEmptyString(value.active_desired_generation) &&
    isSHA256Digest(value.service_state_digest) &&
    isSHA256Digest(value.state_summary_digest) &&
    isSHA256Digest(value.path_digest) &&
    isCount(value.path_count) &&
    isSHA256Digest(value.digest)
  )
}

function isSearchScopeReceipt(value: unknown): value is SearchScopeReceipt {
  if (!(
    isRecord(value) &&
    value.schema === 'phebs-search-scope-v1' &&
    (value.kind === 'all_code' || value.kind === 'service') &&
    typeof value.membership_policy === 'string' &&
    isSHA256Digest(value.expression_digest) &&
    Array.isArray(value.revisions) &&
    value.revisions.every((revision) =>
      isRecord(revision) &&
      isNonEmptyString(revision.repository) &&
      isNonEmptyString(revision.commit)) &&
    isSHA256Digest(value.result_set_digest) &&
    isCount(value.result_files) &&
    isCount(value.result_matches) &&
    isSHA256Digest(value.digest)
  )) return false
  // Cited files without a revision pin are incoherent; an empty pin set is
  // valid only for an empty result set.
  if (value.result_files > 0 && value.revisions.length === 0) return false
  if (value.kind === 'service') {
    return (
      isNonEmptyString(value.repository) &&
      isNonEmptyString(value.service_key) &&
      isNonEmptyString(value.service_status) &&
      isServiceScopeAuthority(value.service_authority)
    )
  }
  return true
}
