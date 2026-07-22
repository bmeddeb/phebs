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
}

export interface IndexJob {
  target: string
  status: string
  error?: string
  created_at: string
  finished_at?: string
}

export interface RepoStatus {
  name: string
  clone_url: string
  default_branch?: string
  indexed_at?: string
  indexed_commit_hash?: string
  latest_indexing_job_status?: string
  orphaned: boolean
  connections?: string[]
  last_index_job?: IndexJob
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

export interface APIKeySummary {
  id: string
  name: string
  prefix: string
  created_at: string
  last_used_at?: string
  expires_at?: string
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

export interface ProofQuery {
  kind: string
  operation?: string
  lineage?: string
  message?: string
  field_number?: number
  before_digest?: string
  after_digest?: string
  domains: string[]
}

export interface CoverageAttempt {
  run_id: string
  commit: string
  extractor: string
  status: string
  failure?: string
}

export interface CoverageRun {
  domain: string
  status: string
  run_id?: string
  extractor?: string
  commit?: string
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
  latest_attempt?: CoverageAttempt
}

export interface CoverageRepository {
  repository: string
  indexed_commit?: string
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

export interface ImpactEvidenceRow {
  kind: 'operation_call' | 'field_reference' | 'unresolved_candidate'
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
  known_consumers: ImpactEvidenceRow[]
  unresolved_candidates: ImpactEvidenceRow[]
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

const query = (values: Record<string, string | number | undefined>) =>
  new URLSearchParams(
    Object.entries(values)
      .filter(([, value]) => value !== '' && value !== undefined)
      .map(([key, value]) => [key, String(value)]),
  ).toString()

export const fetchRepoStatus = (signal?: AbortSignal) =>
  getJSON<RepoStatus[]>('/api/repo-status', signal)

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
): () => void {
  const es = new EventSource(`/api/stream_search?q=${encodeURIComponent(q)}`)
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
