import {
  MARKDOWN_PARSE_TIMEOUT_MS,
  MARKDOWN_PREVIEW_MAX_UNITS,
  MAX_MARKDOWN_SEGMENTS,
  MAX_MARKDOWN_WORKER_RESULT_UNITS,
} from './markdownBounds'
import type { MarkdownWorkerResponse, RawMarkdownSegment } from './markdown'
import { sanitizeMarkdownSegments, type MarkdownSegment } from './markdownSanitize'

function validateWorkerSegments(value: unknown): RawMarkdownSegment[] {
  if (!Array.isArray(value) || value.length > MAX_MARKDOWN_SEGMENTS) {
    throw new Error('markdown worker returned an invalid segment count')
  }
  let units = 0
  for (const segment of value) {
    if (!segment || typeof segment !== 'object' || !('kind' in segment)) {
      throw new Error('markdown worker returned an invalid segment')
    }
    if (segment.kind === 'prose' && 'html' in segment && typeof segment.html === 'string') {
      units += segment.html.length
    } else if (segment.kind === 'mermaid' && 'source' in segment && typeof segment.source === 'string') {
      units += segment.source.length
    } else {
      throw new Error('markdown worker returned an invalid segment')
    }
    if (units > MAX_MARKDOWN_WORKER_RESULT_UNITS) {
      throw new Error('markdown worker exceeded the returned-result limit')
    }
  }
  return value as RawMarkdownSegment[]
}

export function renderMarkdownPreview(source: string, signal?: AbortSignal): Promise<MarkdownSegment[]> {
  if (source.length > MARKDOWN_PREVIEW_MAX_UNITS) {
    return Promise.reject(new Error('markdown source exceeds the preview limit'))
  }
  if (signal?.aborted) return Promise.reject(new DOMException('Aborted', 'AbortError'))

  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL('./markdown.worker.ts', import.meta.url), { type: 'module' })
    let settled = false

    const cleanup = () => {
      clearTimeout(timeout)
      signal?.removeEventListener('abort', abort)
      worker.terminate()
    }
    const fail = (cause: unknown) => {
      if (settled) return
      settled = true
      cleanup()
      reject(cause)
    }
    const abort = () => fail(new DOMException('Aborted', 'AbortError'))
    const timeout = setTimeout(
      () => fail(new Error('markdown parsing exceeded its time limit')),
      MARKDOWN_PARSE_TIMEOUT_MS,
    )

    signal?.addEventListener('abort', abort, { once: true })
    worker.onerror = () => fail(new Error('markdown parser worker failed'))
    worker.onmessage = (event: MessageEvent<MarkdownWorkerResponse>) => {
      if (settled) return
      try {
        if (!event.data?.ok) throw new Error(event.data?.error || 'markdown parsing failed')
        const segments = sanitizeMarkdownSegments(validateWorkerSegments(event.data.segments))
        settled = true
        cleanup()
        resolve(segments)
      } catch (cause) {
        fail(cause)
      }
    }
    worker.postMessage({ source })
  })
}
