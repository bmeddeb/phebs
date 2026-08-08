// Wire types mirrored from the huma API (internal/search, internal/store).
// openapi-typescript codegen can replace this file once the API stabilizes.

import { csrfHeaders, notifyAuthRequired } from './authSession'

export interface Range {
  start_line: number
  start_col: number
  end_line: number
  end_col: number
}

export interface Chunk {
  content: string
  start_line: number
  ranges: Range[]
}

export interface FileResult {
  repo: string
  path: string
  ref: string
  language?: string
  chunks: Chunk[]
}

export interface Stats {
  match_count: number
  file_count: number
  duration_ms: number
}

export interface SearchResult {
  files: FileResult[]
  stats: Stats
  scope_receipt?: SearchScopeReceipt
}

export interface SearchScopeRevision {
  repository: string
  commit: string
}

// Mirrors internal/servicequery.Authority — the exact service authority a
// service-scoped search executed under.
export interface ServiceScopeAuthority {
  schema: string
  predicate_policy: string
  topology_policy: string
  repository: string
  service_key: string
  status: string
  incarnation: number
  revision_selector: string
  revision_branch: string
  revision_commit: string
  expression_digest: string
  current_catalog_generation: string
  catalog_control_revision: number
  active_catalog_generation: string
  active_source_generation: string
  active_desired_generation: string
  service_state_digest: string
  service_state_revision: number
  state_summary_digest: string
  state_summary_revision: number
  repository_source_generation: string
  repository_search_generation: string
  path_digest: string
  path_count: number
  path_bytes: number
  predicate_atoms: number
  predicate_bytes: number
  digest: string
}

export interface SearchScopeReceipt {
  schema: 'phebs-search-scope-v1'
  kind: 'all_code' | 'service'
  repository?: string
  service_key?: string
  service_status?: ServiceStatus
  membership_policy: string
  expression_digest: string
  service_authority?: ServiceScopeAuthority
  revisions: SearchScopeRevision[]
  result_set_digest: string
  result_files: number
  result_matches: number
  digest: string
}

export interface IndexJob {
  target: string
  status: string
  error?: string
  created_at: string
  finished_at?: string
}

export interface IndexedRevision {
  selector: string
  branch: string
  commit: string
}

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

export interface RepoStatus {
  name: string
  clone_url: string
  default_branch?: string
  indexed_at?: string
  indexed_commit_hash?: string
  indexed_revisions?: IndexedRevision[]
  latest_indexing_job_status?: string
  orphaned: boolean
  connections?: string[]
  last_index_job?: IndexJob
  last_index_job_state: 'exact' | 'unavailable'
  analysis_unit?: AnalysisUnitState
}

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

export interface ServiceAuthority {
  kind: string
  id: string
  version: string
  override_id?: string
  override_version?: string
}

export interface ServiceRepository {
  repository: string
  source_kind: string
  source_commit: string
  source_file_count: number
  accepted_file_count: number
  unowned_file_count: number
  authority: ServiceAuthority
  catalog_digest: string
  catalog_generation: string
  catalog_control_revision: number
  state_control_revision: number
  catalog_service_count: number
  live_service_count: number
  current_count: number
  stale_count: number
  unavailable_count: number
  conflict_count: number
  tombstone_count: number
  published_at: string
  state_updated_at: string
}

export interface ServiceRoleCounts {
  primary: number
  supporting: number
  shared: number
  generated: number
  typed: number
}

export interface ServiceRecord {
  repository: string
  key: string
  display_name: string
  disposition: ServiceDisposition
  origin: string
  reason?: string
  successor_count: number
  incarnation: number
  desired_generation?: string
  desired_source_generation?: string
  desired_catalog_generation?: string
  active_desired_generation?: string
  active_source_generation?: string
  active_catalog_generation?: string
  status: ServiceStatus
  removed: boolean
  membership_count: number
  distinct_path_count: number
  role_counts: ServiceRoleCounts
  state_digest: string
  control_revision: number
  changed_at: string
}

export interface ServiceMembership {
  path: string
  role: ServiceMembershipRole
  origin: string
}

export interface ServicePagination {
  order: string
  page_size: number
  returned: number
  next_cursor?: string
}

export interface ServiceInventory {
  schema: string
  repository: ServiceRepository
  filters: {
    repository: string
    status?: ServiceStatus
    disposition?: ServiceDisposition
    include_removed?: boolean
  }
  services: ServiceRecord[]
  pagination: ServicePagination
}

export interface ServiceDetail {
  schema: string
  repository: ServiceRepository
  service: ServiceRecord
  successors: string[]
  memberships: ServiceMembership[]
}

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
  generation?: string
  root_digest?: string
  authority_digest?: string
  service_key: string
  service_incarnation?: number
  service_generation?: string
  reference_count: number
  repository_complete?: boolean
  all_services_complete?: boolean
  failed_service_count?: number
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
  generation: string
  root_digest: string
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

export interface SourceFile {
  content: string
  encoding: 'utf8' | 'base64'
  size: number
}

export interface TreeEntry {
  name: string
  type: 'file' | 'dir' | 'symlink' | 'submodule'
  size?: number
}

export type APIKeyCapability = 'investigation:write'

export interface APIKeySummary {
  id: string
  name: string
  prefix: string
  capabilities: APIKeyCapability[]
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

export interface LifecycleStatus {
  schema: 'phebs-lifecycle-status-v1'
  policy: {
    enabled: boolean
    owners: number
    soft_watermark_percent: number
    hard_watermark_percent: number
    resume_watermark_percent: number
    max_candidates_per_turn: number
    max_deletes_per_turn: number
    max_queries_per_turn: number
  }
  capacity: {
    completeness: 'exact' | 'unavailable'
    pressure: LifecyclePressure
    total_bytes?: number
    used_bytes?: number
    used_percent?: number
    observed_at?: string
  }
  owners: LifecycleOwnerStatus[]
}

export interface CreatedAPIKey {
  key: APIKeySummary
  token: string
}

// T10.1: one append-only audit record
export interface AuditEvent {
  id: string
  action: string
  target?: string
  actor_id?: string
  actor_email?: string
  api_key_id?: string
  auth_method?: string
  source_ip?: string
  status: number
  created_at: string
}

export interface AuditPage {
  events: AuditEvent[]
  has_more: boolean
}

// T10.2: local usage aggregates (zero telemetry — computed from local data)
export interface AnalyticsSummary {
  total_searches: number
  avg_duration_ms: number
  daily: { date: string; count: number }[]
  top_repos: { name: string; count: number }[]
}

export interface VersionInfo {
  version: string
  capabilities?: string[]
}

export type WorkbenchTicketKind = 'add' | 'modify' | 'migrate' | 'retire'
export type WorkbenchSelectionRole = 'current' | 'replacement' | 'analogous'

export interface WorkbenchContractSelection {
  role: WorkbenchSelectionRole
  protocol: string
  repository: string
  declaration_lineage: string
  canonical_operation: string
}

export interface WorkbenchProposalSource {
  path: string
  content: string
}

export interface WorkbenchPlan {
  investigation_id?: string
  expected_revision_id?: string
  referent: string
  claim_family: string
  title: string
  revision: {
    normalized_question: string
    decision_sought: string
    snapshot_policy: string
    build_configuration: string
    enumeration_method: string
  }
  brief: {
    ticket_kind: WorkbenchTicketKind
    problem: string
    desired_outcome: string
    success_criteria: string[]
    non_goals: string[]
    assumptions: string[]
    open_questions: string[]
    external_reference?: string
    what: {
      selections: WorkbenchContractSelection[]
      proposal_protocol?: string
      proposal_sources?: WorkbenchProposalSource[]
    }
  }
  repositories: string[]
  capabilities: string[]
}

export interface WorkbenchProposalCommitment {
  protocol: string
  files: {
    path: string
    content_hash: string
    size_bytes: number
  }[]
  source_set_digest: string
}

export interface WorkbenchBriefWhat {
  selections: WorkbenchContractSelection[]
  proposal?: WorkbenchProposalCommitment
}

export interface WorkbenchView {
  investigation: {
    investigation_id: string
    referent: string
    claim_family: string
    title: string
    owner: string
    lifecycle: string
    current_revision_id?: string
    created_at: string
    updated_at: string
  }
  revision: {
    revision_id: string
    investigation_id: string
    seq: number
    normalized_question: string
    decision_sought: string
    declared_universe: string
    snapshot_policy: string
    build_configuration: string
    pack_selection: string
    enumeration_method: string
    creator: string
    created_at: string
    content_digest: string
  }
  brief: {
    brief_id: string
    schema_version: string
    investigation_id: string
    revision_id: string
    ticket_kind: WorkbenchTicketKind
    problem: string
    desired_outcome: string
    success_criteria: string[]
    non_goals: string[]
    assumptions: string[]
    open_questions: string[]
    external_reference?: string
    what: WorkbenchBriefWhat
    creator: string
    created_at: string
    content_digest: string
  }
}

export interface WorkbenchPreview {
  schema_version: string
  operation: 'create' | 'revise'
  ready: boolean
  preview_digest?: string
  investigation_id?: string
  expected_revision_id?: string
  next_revision_sequence: number
  referent: string
  claim_family: string
  title: string
  revision: {
    normalized_question: string
    decision_sought: string
    declared_universe: string
    snapshot_policy: string
    build_configuration: string
    pack_selection: string
    enumeration_method: string
  }
  brief: {
    schema_version: string
    ticket_kind: WorkbenchTicketKind
    problem: string
    desired_outcome: string
    success_criteria: string[]
    non_goals: string[]
    assumptions: string[]
    open_questions: string[]
    external_reference?: string
    what: WorkbenchBriefWhat
    creator: string
  }
  authorization_digest?: string
  repositories: {
    name: string
    commit: string
    scope_posture: 'whole-repository' | 'focused'
    unit_digest?: string
  }[]
  endpoints: {
    selection: WorkbenchContractSelection
    declaration_commit: string
    declaration_digest: string
    scope_posture: 'whole-repository' | 'focused'
    unit_digest?: string
    declaration_sources: {
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
    }[]
    sources_truncated: boolean
  }[]
  capabilities: {
    id: string
    available: boolean
    version?: string
    content_digest?: string
  }[]
  compatibility: {
    status: 'available' | 'unavailable'
    protocol?: string
    reason?: string
    limits?: Record<string, string | number>
  }
  estimate: {
    repository_count: number
    endpoint_count: number
    proposal_files: number
    proposal_bytes: number
    capability_count: number
    analysis_units: number
  }
  blockers: { code: string; input?: string }[]
}

export interface WorkbenchMutationRequest {
  plan: WorkbenchPlan
  preview_digest: string
  idempotency_key: string
}

export interface WorkbenchImpactFilters {
  unit?: string
  owner?: string
  path_prefix?: string
  code_role?: string
  tier?: string
  freshness?: string
  resolution?: string
  ordering?: string
  level?: string
  service_repository?: string
  source_service?: string
  target_service?: string
}

export interface WorkbenchServiceRelationshipSnapshotRow {
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
}

export interface WorkbenchServiceRelationshipSnapshot {
  schema: 'phebs-service-relationship-snapshot-v1'
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
  rows: WorkbenchServiceRelationshipSnapshotRow[]
  coverage: ServiceRelationshipPage['coverage']
  truncated: boolean
  caveat: string
}

export interface WorkbenchServiceImpactSide {
  role: 'source' | 'target'
  selection: WorkbenchContractSelection
  snapshot: WorkbenchServiceRelationshipSnapshot
}

export interface WorkbenchServiceImpact {
  source?: WorkbenchServiceImpactSide
  target?: WorkbenchServiceImpactSide
  authority: {
    schema: string
    visibility: {
      principal: string
      authorization_provider: string
      permission_snapshot: string
      visible_repository_set_digest: string
    }
    visible_repository_count: number
    exact_root_count: number
    gap_count: number
    state: 'exact' | 'gap'
    reason?: string
    roots: ServiceRelationshipRootReceipt[]
    digest: string
  }
  caveat: string
}

export interface WorkbenchResourcePlaneRelationship {
  kind: string
  subject: string
  object: string
  classification: string
  sources: ContractCatalogSource[]
}

export interface WorkbenchResourcePlane {
  id: string
  label: string
  state: 'enabled' | 'unsupported' | 'failed' | 'stale' | 'human_asserted'
  reason?: string
  relationships: WorkbenchResourcePlaneRelationship[]
}

export interface WorkbenchCallerImpact {
  selection: WorkbenchContractSelection
  query: CallerMapQuery
  scope?: AnalysisScopeProjection
  generation: CallerMapGeneration
  matching_rows_state: 'exact' | 'unavailable'
  // Exact generation gaps deliberately omit the declaration and total. A
  // missing value is unavailable authority, never an inferred zero.
  declaration?: ContractCatalogClaim
  resolved_callers: CallerMapRow[]
  extractor_abstentions: CallerMapRow[]
  groups?: CallerMapGroup[]
  total_matching_rows?: number
  pagination: CallerMapPage['pagination']
  caveat: string
}

export interface WorkbenchImpactPage {
  schema_version: 'workbench-impact-inventory-v3'
  investigation_id: string
  revision_id: string
  ticket_kind: WorkbenchTicketKind
  scenario_emphasis: string[]
  atlas: {
    selection: WorkbenchContractSelection
    declaration: ContractCatalogClaim
    fact_detail: ContractCatalogOperationFactDetail
    request: ContractCatalogMessage
    response: ContractCatalogMessage
    implementations: ContractCatalogRelationship[]
    name_matches: ContractCatalogRelationship[]
    extractor_abstentions: ContractCatalogRelationship[]
    relationships_truncated: boolean
    shape_truncated: boolean
    coverage_digest: string
  }[]
  callers: WorkbenchCallerImpact[]
  comparison?: CallerComparisonExactPage
  compatibility?: {
    status: string
    run_id?: string
    compatible?: boolean
    violations: CompatibilityViolation[]
    affected_fields: {
      lineage: string
      message: string
      field_number: number
    }[]
    failure?: string
    content_digest?: string
  }
  field_references?: {
    schema_version: string
    rows: {
      field: {
        lineage: string
        message: string
        field_number: number
      }
      assertion: {
        id: string
        predicate: string
        subject: string
        object: string
        lineage?: string
        tier: string
        code_role?: string
        repo: string
        run_id: string
        detail?: string
      }
      evidence: BundleEvidenceEntry[]
    }[]
    total_rows: number
    pagination: CallerMapPage['pagination']
    coverage_digest: string
    caveat: string
  }
  resource_planes: WorkbenchResourcePlane[]
  service_impact?: WorkbenchServiceImpact
  analysis_scope: {
    coverage: {
      capability: string
      target: string
      digest: string
      certificate: CoverageCertificate
    }[]
    capabilities: {
      id: string
      state: string
      reason?: string
    }[]
    gaps: {
      capability: string
      target?: string
      state: string
      code: string
    }[]
  }
  pagination: {
    complete: boolean
    next_cursor?: string
  }
  caveat: string
}

export interface WorkbenchImplementationAnchor {
  repository: string
  commit: string
  path: string
  line: number
  character: number
  encoding?: 'utf-8' | 'utf-16' | 'utf-32'
}

export interface WorkbenchImplementationPage {
  schema_version: string
  investigation_id: string
  revision_id: string
  ticket_kind: WorkbenchTicketKind
  rows: {
    id: string
    kind: string
    code_role: string
    boundary?: string
    review_state: string
    selection_rule: string
    selection_input: string
    symbol?: string
    source: {
      repository: string
      commit: string
      path: string
      start_byte?: number
      end_byte?: number
      start_line?: number
      start_column?: number
      end_line?: number
      end_column?: number
      content_digest?: string
      size_bytes?: number
    }
    commit?: {
      id: string
      parent_ids: string[]
      subject: string
      subject_truncated?: boolean
      author_name?: string
      author_time?: string
    }
    diff?: {
      base?: string
      head: string
      digest: string
      patch_excerpt: string
      patch_truncated: boolean
      files: {
        status: string
        path: string
        old_path?: string
        similarity?: number
        additions?: number
        deletions?: number
        binary?: boolean
      }[]
      files_truncated?: boolean
    }
  }[]
  capabilities: {
    id: string
    state: string
    reason?: string
  }[]
  gaps: {
    capability: string
    target?: string
    state: string
    code: string
  }[]
  pagination: {
    total_rows: number
    complete: boolean
    next_cursor?: string
  }
  snapshot_digest: string
  caveat: string
}

export interface WorkbenchChecklistEvidenceInput {
  compatibility_run_id?: string
  impact_filters: WorkbenchImpactFilters
  anchors: WorkbenchImplementationAnchor[]
}

export interface WorkbenchSuggestionEvidence {
  plane: string
  kind: string
  id: string
  digest: string
  repository?: string
  commit?: string
  path?: string
  start_byte?: number
  end_byte?: number
  start_line?: number
  end_line?: number
}

export interface WorkbenchSuggestion {
  schema_version: string
  suggestion_id: string
  investigation_id: string
  revision_id: string
  kind: string
  summary: string
  selection_rule: string
  evidence_snapshot_digest: string
  evidence: WorkbenchSuggestionEvidence[]
  content_digest: string
}

export type WorkbenchDispositionCategory =
  'accepted' | 'rejected' | 'completed' | 'reopened' | 'waived'

export interface WorkbenchDisposition {
  schema_version: string
  disposition_id: string
  investigation_id: string
  revision_id: string
  suggestion: WorkbenchSuggestion
  category: WorkbenchDispositionCategory
  actor: string
  authority: string
  rationale?: string
  sequence: number
  supersedes?: string
  created_at: string
  content_digest: string
}

export interface WorkbenchChecklistEntry {
  suggestion: WorkbenchSuggestion
  evidence_state: 'current' | 'stale'
  state: 'unaccepted' | WorkbenchDispositionCategory
  disposition?: WorkbenchDisposition
  disposition_history: WorkbenchDisposition[]
}

export interface WorkbenchChecklistPage {
  schema_version: string
  investigation_id: string
  revision_id: string
  ticket_kind: WorkbenchTicketKind
  evidence_snapshot: {
    impact_digest: string
    implementation_digest: string
    combined_digest: string
    impact_truncated: boolean
    implementation_truncated: boolean
  }
  entries: WorkbenchChecklistEntry[]
  pagination: {
    total_entries: number
    complete: boolean
    next_cursor?: string
  }
  snapshot_digest: string
  caveat: string
}

export interface WorkbenchDispositionMutation {
  investigation_id: string
  expected_revision_id: string
  idempotency_key: string
  evidence: WorkbenchChecklistEvidenceInput
  suggestion: WorkbenchSuggestion
  category: WorkbenchDispositionCategory
  rationale?: string
  supersedes?: string
}

export class WorkbenchAPIError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'WorkbenchAPIError'
    this.status = status
  }
}

export type InvestigationOutcome = 'ok' | 'partial' | 'refused' | 'error'
export type InvestigationAttributionState = 'resolved' | 'ambiguous' | 'unresolved' | 'not_applicable'

export interface InvestigationViewSummary {
  id: string
  title: string
  state: string
  outcome: InvestigationOutcome
}

export interface InvestigationProofReference {
  occurrence_id: string
  proof_id: string
  snapshot_id: string
  verification_action: string
}

export interface InvestigationAttributionValue {
  state: InvestigationAttributionState
  reason_codes: string[]
}

export interface InvestigationFact {
  fact_id: string
  logical_relationship_id: string
  predicate: string
  evidence_basis: string
  subject: { kind: string; id: string }
  object: { kind: string; id: string }
  semantic_resolution: { value: string; reason_codes: string[] }
  attribution_states: Record<string, InvestigationAttributionValue>
  proof_references: InvestigationProofReference[]
  qualifiers: Record<string, string>
  verification_tool: string
}

export interface InvestigationCoverageHop {
  denominator: number
  resolved: number
  ambiguous: number
  unresolved: number
  not_applicable: number
}

export interface InvestigationEnvelope {
  envelope_version: string
  request_id: string
  generated_at: string
  outcome: InvestigationOutcome
  scope: {
    normalized_question: string
    investigation_id: string
    revision_id: string
    run_artifact_id: string
    snapshot_id: string
    build_configuration_id: string
    visibility_projection_id: string
    claim: {
      claim_family: string
      predicate: string
      subject: { kind: string; id: string }
      object: { kind: string; id: string } | null
      filters: Record<string, string[]>
      decision_sought: string
    }
  }
  payload?: {
    kind: string
    data: { facts: InvestigationFact[] }
  }
  coverage?: {
    visibility: string
    eligible_units: number
    processing: {
      analyzed: number
      excluded: number
      partial: number
      failed: number
    }
    processing_reconciled: boolean
    exclusions_by_reason: Record<string, number>
    outcome_gates: {
      status: string
      rule_id: string
      rule_version: string
      reason_codes: string[]
    }
    attribution: Record<string, InvestigationCoverageHop>
  }
  analysis_conclusion?: {
    value: string
    evidence_basis: string
    rule_id: string
    rule_version: string
    reason_codes: string[]
    eligible_workflows: string[]
  }
  absence_eligibility: {
    applicable: boolean
    eligible: boolean
    rule_version: string
    blocker_codes: string[]
    qualification: {
      template_id: string
      template_version: string
      parameters: Record<string, string | number>
      authoritative_text: string
    } | null
  }
  validation?: {
    pack_id: string
    pack_version: string
    pack_status: string
    workflow_eligibility: string
    validation_reference: string
    applies: boolean
    applicability_blockers: string[]
    measured_claim_id: string
    expires_at: string
    expiry_trigger: string | null
  }
  freshness?: {
    analysis: {
      status: string
      published: string
      snapshot_committed: string
    }
    inputs: unknown[]
  }
  result_window?: {
    result_set_complete: boolean
    page_exhausted: boolean
    truncated: boolean
    truncated_reason: string | null
    returned: number
    next_page_token: string | null
    ordering: string
  }
  refusal?: {
    code: string
    authoritative_text: string
  } | null
  errors: unknown[]
}

export interface InvestigationViewDocument extends InvestigationViewSummary {
  envelope: InvestigationEnvelope
}

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
  // Retained only for compatibility with historical v1 payload fixtures;
  // Workbench impact v2 accepts the exact snapshot shape exclusively.
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
  // Workbench impact v2 never exposes this legacy evidence-plane field.
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

export interface GitCommit {
  id: string
  short_id: string
  parent_ids: string[]
  subject: string
  message: string
  author: GitIdentity
  committer: GitIdentity
}

export interface BlameLine {
  line: number
  original_line: number
  commit_id: string
  original_path: string
  content: string
}

export interface BlameResult {
  revision: string
  path: string
  lines: BlameLine[]
  commits: GitCommit[]
  truncated: boolean
}

export interface CommitListResult {
  revision: string
  commits: GitCommit[]
  offset: number
  has_more: boolean
}

export interface GitFileChange {
  status: string
  path: string
  old_path?: string
  similarity?: number
  additions?: number
  deletions?: number
  binary?: boolean
}

export interface CommitResult {
  revision: string
  commit: GitCommit
  changes: GitFileChange[]
}

export interface DiffResult {
  base?: string
  head: string
  patch: string
  files: GitFileChange[]
  truncated: boolean
}

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

export interface DefinitionResult {
  available: boolean
  symbol?: string
  location?: CodeLocation
}

export interface ReferencesResult {
  available: boolean
  symbol?: string
  locations: CodeLocation[]
  truncated?: boolean
}

export interface HoverInfo {
  symbol: string
  display_name?: string
  kind?: string
  signature?: string
  language?: string
  documentation?: string[]
  range: CodeRange
  encoding: PositionEncoding
}

export interface HoverResult {
  available: boolean
  hover?: HoverInfo
}

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
  getJSON<{ entries: TreeEntry[] }>(
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

async function workbenchJSON<T>(
  url: string,
  init: RequestInit = {},
): Promise<T> {
  const response = await request(url, init)
  if (!response.ok) {
    const body = await response.text()
    let message = body
    if (body) {
      try {
        const problem = JSON.parse(body) as { detail?: unknown }
        if (typeof problem.detail === 'string' && problem.detail.trim()) {
          message = problem.detail
        }
      } catch {
        // Preserve a non-JSON boundary response verbatim.
      }
    }
    throw new WorkbenchAPIError(
      response.status,
      message || `Workbench request failed (${response.status})`,
    )
  }
  return response.json() as Promise<T>
}

export const previewWorkbench = (
  plan: WorkbenchPlan,
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchPreview>('/api/workbench_previews', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
  body: JSON.stringify(plan),
  signal,
})

export const fetchWorkbench = (
  investigationID: string,
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchView>(
  `/api/workbenches/${encodeURIComponent(investigationID)}`,
  { signal },
)

export const createWorkbench = (
  mutation: WorkbenchMutationRequest,
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchView>('/api/workbenches', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
  body: JSON.stringify(mutation),
  signal,
})

export const reviseWorkbench = (
  investigationID: string,
  mutation: WorkbenchMutationRequest,
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchView>(
  `/api/workbenches/${encodeURIComponent(investigationID)}/revisions`,
  {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
    body: JSON.stringify(mutation),
    signal,
  },
)

function workbenchEvidenceURL(
  investigationID: string,
  revisionID: string,
  surface: 'impact' | 'implementation' | 'checklist',
  query: Record<string, string | number | undefined>,
) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== '') {
      params.set(key, String(value))
    }
  }
  const suffix = params.size > 0 ? `?${params.toString()}` : ''
  return `/api/workbenches/${encodeURIComponent(investigationID)}` +
    `/revisions/${encodeURIComponent(revisionID)}/${surface}${suffix}`
}

export const fetchWorkbenchImpact = (
  investigationID: string,
  revisionID: string,
  options: {
    compatibilityRun?: string
    filters: WorkbenchImpactFilters
    pageSize?: number
    cursor?: string
  },
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchImpactPage>(
  workbenchEvidenceURL(investigationID, revisionID, 'impact', {
    compatibility_run_id: options.compatibilityRun,
    filters: JSON.stringify(options.filters),
    page_size: options.pageSize,
    cursor: options.cursor,
  }),
  { signal },
)

export const fetchWorkbenchImplementation = (
  investigationID: string,
  revisionID: string,
  options: {
    anchors: WorkbenchImplementationAnchor[]
    pageSize?: number
    cursor?: string
  },
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchImplementationPage>(
  workbenchEvidenceURL(investigationID, revisionID, 'implementation', {
    anchors: JSON.stringify(options.anchors),
    page_size: options.pageSize,
    cursor: options.cursor,
  }),
  { signal },
)

export const fetchWorkbenchChecklist = (
  investigationID: string,
  revisionID: string,
  options: {
    evidence: WorkbenchChecklistEvidenceInput
    pageSize?: number
    cursor?: string
  },
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchChecklistPage>(
  workbenchEvidenceURL(investigationID, revisionID, 'checklist', {
    evidence: JSON.stringify(options.evidence),
    page_size: options.pageSize,
    cursor: options.cursor,
  }),
  { signal },
)

export const recordWorkbenchDisposition = (
  investigationID: string,
  revisionID: string,
  mutation: WorkbenchDispositionMutation,
  signal?: AbortSignal,
) => workbenchJSON<WorkbenchDisposition>(
  `/api/workbenches/${encodeURIComponent(investigationID)}` +
    `/revisions/${encodeURIComponent(revisionID)}/dispositions`,
  {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
    body: JSON.stringify(mutation),
    signal,
  },
)

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

export const fetchInvestigationViews = (signal?: AbortSignal) =>
  getJSON<InvestigationViewSummary[]>('/api/investigation_views', signal)

export const fetchInvestigationView = (id: string, signal?: AbortSignal) =>
  getJSON<InvestigationViewDocument>(
    `/api/investigation_views/${encodeURIComponent(id)}`,
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

export async function createAPIKey(
  name: string,
  capabilities: APIKeyCapability[] = [],
): Promise<CreatedAPIKey> {
  const res = await request('/api/auth/keys', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
    body: JSON.stringify({ name, capabilities }),
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
