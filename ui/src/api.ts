// Wire types mirrored from the huma API (internal/search, internal/store).
// openapi-typescript codegen can replace this file once the API stabilizes.

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

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`${res.status}: ${body}`)
  }
  return res.json() as Promise<T>
}

export const fetchRepoStatus = () => getJSON<RepoStatus[]>('/api/repo-status')

export const fetchTree = (repo: string) =>
  getJSON<{ paths: string[] }>(`/api/tree?repo=${encodeURIComponent(repo)}`)

export const fetchSource = (repo: string, path: string) =>
  getJSON<SourceFile>(
    `/api/source?repo=${encodeURIComponent(repo)}&path=${encodeURIComponent(path)}`,
  )

export const postReindex = (repo: string, force: boolean) =>
  fetch('/api/reindex', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ repo, force }),
  }).then((res) => {
    if (!res.ok) throw new Error(`reindex failed: ${res.status}`)
  })

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
  es.addEventListener('results', (e) => onBatch(JSON.parse(e.data)))
  es.addEventListener('done', (e) => {
    es.close()
    onDone(JSON.parse(e.data))
  })
  // A single 'error' handler: EventSource fires this event type for BOTH a
  // server-sent `event: error` (has e.data — a real backend message) and a
  // connection-level failure (no data). A separate es.onerror would fire for
  // the same event and clobber the real message with 'connection lost'.
  es.addEventListener('error', (e: MessageEvent) => {
    es.close()
    if (e.data) {
      let msg = 'search failed'
      try {
        msg = JSON.parse(e.data).message ?? msg
      } catch {
        /* keep default */
      }
      onError(msg)
    } else {
      onError('connection lost')
    }
  })
  return () => es.close()
}
