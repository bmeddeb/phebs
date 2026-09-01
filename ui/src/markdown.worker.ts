import { segmentMarkdown, type MarkdownWorkerResponse } from './markdown'

// One request per one-shot worker. The owner always terminates us after the
// first response (or at the timeout/abort boundary).
self.onmessage = (event: MessageEvent<{ source: unknown }>) => {
  let response: MarkdownWorkerResponse
  try {
    if (typeof event.data.source !== 'string') throw new Error('markdown source is not a string')
    response = { ok: true, segments: segmentMarkdown(event.data.source) }
  } catch (cause) {
    response = {
      ok: false,
      error: cause instanceof Error ? cause.message : 'markdown parsing failed',
    }
  }
  self.postMessage(response)
}
