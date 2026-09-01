import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  MARKDOWN_PARSE_TIMEOUT_MS,
  MARKDOWN_PREVIEW_MAX_UNITS,
  MAX_MARKDOWN_WORKER_RESULT_UNITS,
} from './markdownBounds'
import { renderMarkdownPreview } from './markdownPreview'
import type { MarkdownWorkerResponse } from './markdown'

class FakeWorker {
  static instances: FakeWorker[] = []
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent<MarkdownWorkerResponse>) => void) | null = null
  posted: unknown[] = []
  terminated = false

  constructor() {
    FakeWorker.instances.push(this)
  }

  postMessage(value: unknown) {
    this.posted.push(value)
  }

  terminate() {
    this.terminated = true
  }

  respond(data: MarkdownWorkerResponse) {
    this.onmessage?.({ data } as MessageEvent<MarkdownWorkerResponse>)
  }
}

beforeEach(() => {
  FakeWorker.instances = []
  vi.stubGlobal('Worker', FakeWorker)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('renderMarkdownPreview worker boundary', () => {
  it('sanitizes one bounded worker result and terminates the worker', async () => {
    const rendered = renderMarkdownPreview('# Safe')
    const worker = FakeWorker.instances[0]
    expect(worker.posted).toEqual([{ source: '# Safe' }])
    worker.respond({
      ok: true,
      segments: [{ kind: 'prose', html: '<h1>Safe</h1><script>bad()</script>' }],
    })
    const segments = await rendered
    expect(segments).toEqual([{ kind: 'prose', html: '<h1>Safe</h1>' }])
    expect(worker.terminated).toBe(true)
  })

  it('terminates and refuses a parser that exceeds its deadline', async () => {
    vi.useFakeTimers()
    const rendered = renderMarkdownPreview('[x]('.repeat(32_768))
    const worker = FakeWorker.instances[0]
    const refused = expect(rendered).rejects.toThrow('markdown parsing exceeded its time limit')
    await vi.advanceTimersByTimeAsync(MARKDOWN_PARSE_TIMEOUT_MS + 1)
    await refused
    expect(worker.terminated).toBe(true)
  })

  it('terminates immediately when the owning preview is aborted', async () => {
    const controller = new AbortController()
    const rendered = renderMarkdownPreview('# Cancelled', controller.signal)
    const worker = FakeWorker.instances[0]
    const refused = expect(rendered).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await refused
    expect(worker.terminated).toBe(true)
  })

  it('revalidates the returned-result bound before sanitizing worker HTML', async () => {
    const rendered = renderMarkdownPreview('# Bounded')
    const worker = FakeWorker.instances[0]
    const refused = expect(rendered).rejects.toThrow('markdown worker exceeded the returned-result limit')
    worker.respond({
      ok: true,
      segments: [{ kind: 'prose', html: 'x'.repeat(MAX_MARKDOWN_WORKER_RESULT_UNITS + 1) }],
    })
    await refused
    expect(worker.terminated).toBe(true)
  })

  it('terminates when the parser worker itself fails', async () => {
    const rendered = renderMarkdownPreview('# Failed')
    const worker = FakeWorker.instances[0]
    const refused = expect(rendered).rejects.toThrow('markdown parser worker failed')
    worker.onerror?.(new Event('error'))
    await refused
    expect(worker.terminated).toBe(true)
  })

  it('refuses an oversized source before allocating a worker', async () => {
    await expect(renderMarkdownPreview('x'.repeat(MARKDOWN_PREVIEW_MAX_UNITS + 1))).rejects.toThrow(
      'markdown source exceeds the preview limit',
    )
    expect(FakeWorker.instances).toHaveLength(0)
  })
})
