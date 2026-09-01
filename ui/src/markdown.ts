import { marked, type Token } from 'marked'
import {
  MARKDOWN_PREVIEW_MAX_UNITS,
  MAX_MARKDOWN_SEGMENTS,
  MAX_MARKDOWN_WORKER_RESULT_UNITS,
  MAX_RENDERED_DIAGRAMS,
} from './markdownBounds'

// T44.3/T44.4 parser half of the preview boundary. This module is loaded only
// inside markdown.worker.ts in production: pathological Markdown can consume
// at most the worker timeout and never blocks the UI thread.
export type RawMarkdownSegment =
  | { kind: 'prose'; html: string }
  | { kind: 'mermaid'; source: string }

export type MarkdownWorkerResponse =
  | { ok: true; segments: RawMarkdownSegment[] }
  | { ok: false; error: string }

export function segmentMarkdown(source: string): RawMarkdownSegment[] {
  if (source.length > MARKDOWN_PREVIEW_MAX_UNITS) {
    throw new Error('markdown source exceeds the preview limit')
  }

  const tokens = marked.lexer(source, { gfm: true, breaks: false })
  const segments: RawMarkdownSegment[] = []
  let prose: Token[] = []
  let diagrams = 0
  let returnedUnits = 0

  const flush = () => {
    if (prose.length === 0) return
    // Render the already-lexed slice. Re-lexing raw text here would lose the
    // document-wide reference-definition table at a Mermaid boundary, so a
    // link defined on either side could render as literal Markdown.
    const html = marked.parser(prose, { gfm: true, breaks: false, async: false }) as string
    prose = []
    if (html.trim() === '') return
    returnedUnits += html.length
    if (returnedUnits > MAX_MARKDOWN_WORKER_RESULT_UNITS) {
      throw new Error('markdown render exceeds the returned-result limit')
    }
    segments.push({ kind: 'prose', html })
  }

  for (const token of tokens) {
    const mermaid = token.type === 'code' && (token.lang ?? '').trim().toLowerCase() === 'mermaid'
    if (mermaid && diagrams < MAX_RENDERED_DIAGRAMS) {
      flush()
      returnedUnits += token.text.length
      if (returnedUnits > MAX_MARKDOWN_WORKER_RESULT_UNITS) {
        throw new Error('markdown render exceeds the returned-result limit')
      }
      segments.push({ kind: 'mermaid', source: token.text })
      diagrams += 1
    } else {
      // Non-Mermaid content and every fence beyond the cap stay in one prose
      // stream. marked renders excess fences as ordinary code blocks, keeping
      // their exact source visible without mounting another React component.
      prose.push(token)
    }
  }
  flush()

  if (segments.length > MAX_MARKDOWN_SEGMENTS) {
    throw new Error('markdown render exceeds the segment limit')
  }
  return segments
}
