import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { streamSearch } from './api'

type Listener = (e: { data?: string }) => void

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  closed = false
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
    const b1 = { files: [{ repo: 'r1', path: 'a.go', chunks: [] }], stats: { match_count: 1, file_count: 1, duration_ms: 2 } }
    const b2 = { files: [{ repo: 'r2', path: 'b.go', chunks: [] }], stats: { match_count: 3, file_count: 1, duration_ms: 4 } }
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
})
