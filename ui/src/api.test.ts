import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createAPIKey,
  fetchContractCallers,
  fetchContractCatalog,
  fetchContractOperation,
  fetchDefinition,
  fetchFolderContents,
  fetchHover,
  fetchReferences,
  fetchRepoStatus,
  fetchSource,
  revokeAPIKey,
  streamSearch,
} from './api'
import { setCSRFToken } from './authSession'

type Listener = (e: { data?: string }) => void

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  closed = false
  onerror: Listener | null = null
  private listeners = new Map<string, Listener[]>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }

  close() {
    this.closed = true
  }

  emit(type: string, data?: string) {
    for (const fn of this.listeners.get(type) ?? []) fn({ data })
    // real EventSource dispatches error events to the onerror IDL attribute
    // too — modeling it is what pins the error-clobber bug (see api.ts).
    if (type === 'error') this.onerror?.({ data })
  }
}

const last = () => FakeEventSource.instances.at(-1)!

describe('streamSearch', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('URL-encodes the query', () => {
    streamSearch('repo:a/b lang:Go "x y"', vi.fn(), vi.fn(), vi.fn())
    expect(last().url).toBe(
      '/api/stream_search?q=repo%3Aa%2Fb%20lang%3AGo%20%22x%20y%22',
    )
  })

  it('delivers parsed results batches to onBatch in order', () => {
    const batches: unknown[] = []
    streamSearch('q', (r) => batches.push(r), vi.fn(), vi.fn())
    const b1 = { files: [{ repo: 'r1', path: 'a.go', ref: 'aaa', chunks: [] }], stats: { match_count: 1, file_count: 1, duration_ms: 2 } }
    const b2 = { files: [{ repo: 'r2', path: 'b.go', ref: 'bbb', chunks: [] }], stats: { match_count: 3, file_count: 1, duration_ms: 4 } }
    last().emit('results', JSON.stringify(b1))
    last().emit('results', JSON.stringify(b2))
    expect(batches).toEqual([b1, b2])
    expect(last().closed).toBe(false)
  })

  it('done closes the source and passes parsed stats to onDone', () => {
    const onDone = vi.fn()
    streamSearch('q', vi.fn(), onDone, vi.fn())
    const stats = { match_count: 5, file_count: 2, duration_ms: 7 }
    last().emit('done', JSON.stringify(stats))
    expect(onDone).toHaveBeenCalledTimes(1)
    expect(onDone.mock.calls[0][0]).toEqual(stats)
    expect(last().closed).toBe(true)
  })

  // One 'error' listener handles both server-sent errors (with data) and
  // connection failures (without) — see comment in api.ts.
  const errorCases: [name: string, data: string | undefined, msg: string][] = [
    ['server error with message', '{"message":"boom"}', 'boom'],
    ['server error with invalid JSON', 'not json', 'search failed'],
    ['server error with no message field', '{}', 'search failed'],
    ['connection failure (no data)', undefined, 'connection lost'],
  ]
  it.each(errorCases)('error: %s', (_name, data, msg) => {
    const onError = vi.fn()
    streamSearch('q', vi.fn(), vi.fn(), onError)
    last().emit('error', data)
    expect(onError).toHaveBeenCalledTimes(1)
    expect(onError.mock.calls[0][0]).toBe(msg)
    expect(last().closed).toBe(true)
  })

  it('returned cancel function closes the EventSource', () => {
    const cancel = streamSearch('q', vi.fn(), vi.fn(), vi.fn())
    expect(last().closed).toBe(false)
    cancel()
    expect(last().closed).toBe(true)
  })

  it.each([
    ['invalid JSON', 'not json'],
    ['missing files', JSON.stringify({ stats: { match_count: 0, file_count: 0, duration_ms: 1 } })],
    ['missing immutable ref', JSON.stringify({ files: [{ repo: 'r', path: 'x', chunks: [] }], stats: { match_count: 0, file_count: 1, duration_ms: 1 } })],
  ])('rejects malformed results events: %s', (_name, data) => {
    const onBatch = vi.fn()
    const onError = vi.fn()
    streamSearch('q', onBatch, vi.fn(), onError)
    last().emit('results', data)
    expect(onBatch).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledWith('invalid search results event')
    expect(last().closed).toBe(true)
  })

  it('rejects a malformed done event through the same error callback', () => {
    const onDone = vi.fn()
    const onError = vi.fn()
    streamSearch('q', vi.fn(), onDone, onError)
    last().emit('done', '{"duration_ms":"fast"}')
    expect(onDone).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledWith('invalid search done event')
    expect(last().closed).toBe(true)
  })
})

describe('request helpers', () => {
  afterEach(() => {
    setCSRFToken()
    vi.unstubAllGlobals()
  })

  it('passes AbortSignal through repository status requests', async () => {
    const signal = new AbortController().signal
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => [] })
    vi.stubGlobal('fetch', fetchMock)
    await fetchRepoStatus(signal)
    expect(fetchMock).toHaveBeenCalledWith('/api/repo-status', {
      credentials: 'same-origin',
      signal,
    })
  })

  it('includes immutable refs in source and lazy folder requests', async () => {
    const signal = new AbortController().signal
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ entries: [] }),
    })
    vi.stubGlobal('fetch', fetchMock)
    await fetchSource('github.com/a/repo', 'src/a b.go', 'abc123', signal)
    await fetchFolderContents('github.com/a/repo', 'abc123', 'src', signal)
    expect(fetchMock.mock.calls[0]).toEqual([
      '/api/source?repo=github.com%2Fa%2Frepo&path=src%2Fa+b.go&ref=abc123',
      { credentials: 'same-origin', signal },
    ])
    expect(fetchMock.mock.calls[1]).toEqual([
      '/api/folder_contents?repo=github.com%2Fa%2Frepo&ref=abc123&path=src',
      { credentials: 'same-origin', signal },
    ])
  })

  it('uses zero-based UTF-16 source positions for precise navigation', async () => {
    const signal = new AbortController().signal
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ available: false }) })
    vi.stubGlobal('fetch', fetchMock)
    await fetchDefinition('github.com/a/repo', 'a'.repeat(40), 'src/rocket.go', 7, 13, signal)
    await fetchReferences('github.com/a/repo', 'a'.repeat(40), 'src/rocket.go', 7, 13, signal)
    await fetchHover('github.com/a/repo', 'a'.repeat(40), 'src/rocket.go', 7, 13, signal)
    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      `/api/find_definitions?repo=github.com%2Fa%2Frepo&ref=${'a'.repeat(40)}&path=src%2Frocket.go&line=7&character=13&encoding=utf16`,
      `/api/find_references?repo=github.com%2Fa%2Frepo&ref=${'a'.repeat(40)}&path=src%2Frocket.go&line=7&character=13&encoding=utf16`,
      `/api/hover?repo=github.com%2Fa%2Frepo&ref=${'a'.repeat(40)}&path=src%2Frocket.go&line=7&character=13&encoding=utf16`,
    ])
  })

  it('binds contract catalog filters, continuation, and operation identity', async () => {
    const signal = new AbortController().signal
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ items: [] }),
    })
    vi.stubGlobal('fetch', fetchMock)
    await fetchContractCatalog({
      repository: 'github.com/acme/contracts',
      package: 'demo.v1',
      protocol: 'protobuf',
      lineage: 'lineage one',
    }, 25, 'opaque+/cursor', signal)
    await fetchContractOperation(
      'github.com/acme/contracts',
      'lineage one',
      '/demo.v1.Catalog/Get',
      signal,
    )
    await fetchContractCallers({
      protocol: 'protobuf',
      repository: 'github.com/acme/contracts',
      declaration_lineage: 'lineage one',
      operation: '/demo.v1.Catalog/Get',
    }, {
      owner: 'team one',
      freshness: 'fresh',
      resolution: 'scip',
      ordering: 'unit',
    }, 100, 'caller+/cursor', signal)
    expect(fetchMock.mock.calls).toEqual([
      [
        '/api/contract_atlas?repository=github.com%2Facme%2Fcontracts&package=demo.v1&protocol=protobuf&lineage=lineage+one&page_size=25&cursor=opaque%2B%2Fcursor',
        { credentials: 'same-origin', signal },
      ],
      [
        '/api/contract_atlas/operation?repository=github.com%2Facme%2Fcontracts&lineage=lineage+one&operation=%2Fdemo.v1.Catalog%2FGet',
        { credentials: 'same-origin', signal },
      ],
      [
        '/api/contract_callers?protocol=protobuf&repository=github.com%2Facme%2Fcontracts&lineage=lineage+one&operation=%2Fdemo.v1.Catalog%2FGet&owner=team+one&freshness=fresh&resolution=scip&ordering=unit&page_size=100&cursor=caller%2B%2Fcursor',
        { credentials: 'same-origin', signal },
      ],
    ])
  })

  it('attaches the in-memory CSRF token to API-key mutations', async () => {
    setCSRFToken('csrf-settings')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ key: { id: 'key1' }, token: 'phebs_token' }),
      })
      .mockResolvedValueOnce({ ok: true })
    vi.stubGlobal('fetch', fetchMock)
    await createAPIKey('CI')
    await revokeAPIKey('key1')
    expect(fetchMock.mock.calls[0][1]).toMatchObject({
      credentials: 'same-origin',
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'csrf-settings' },
    })
    expect(fetchMock.mock.calls[1]).toEqual([
      '/api/auth/keys/key1',
      {
        credentials: 'same-origin',
        method: 'DELETE',
        headers: { 'X-CSRF-Token': 'csrf-settings' },
      },
    ])
  })
})
