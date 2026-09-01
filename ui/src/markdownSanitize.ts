import DOMPurify from 'dompurify'
import type { RawMarkdownSegment } from './markdown'

// T44.3 sanitizer half of the preview boundary. marked runs in a terminable
// worker; only its bounded returned HTML enters this isolated DOMPurify
// instance on the main thread.
const purify = DOMPurify(window)

purify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    const href = node.getAttribute('href')?.trim()
    if (!href) {
      // Relative and unsafe targets are an explicit v1 deferral. Do not leave
      // behind an underlined, focusable pseudo-link. Empty hrefs would open a
      // second copy of Phebs, so they are inert under the same rule. Retain
      // the exact child content as ordinary prose.
      const children = document.createDocumentFragment()
      while (node.firstChild) children.appendChild(node.firstChild)
      node.replaceWith(children)
      return
    }
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer nofollow')
  }
  if (node.tagName === 'IMG') {
    // No repository or remote image is fetched in v1. textContent makes the
    // placeholder inert even when alt text contains HTML-like bytes.
    const alt = node.getAttribute('alt') ?? ''
    const marker = document.createElement('span')
    marker.textContent = alt ? `Image unavailable: ${alt}` : 'Image unavailable'
    node.replaceWith(marker)
  }
})

const PURIFY_CONFIG = {
  ALLOWED_TAGS: [
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'br', 'hr',
    'a', 'em', 'strong', 'del', 'code', 'pre', 'blockquote',
    'ul', 'ol', 'li', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
    'img', 'span',
  ],
  ALLOWED_ATTR: ['href', 'title', 'alt', 'align'],
  ALLOWED_URI_REGEXP: /^(?:https?|mailto):/i,
  FORBID_TAGS: ['style', 'form', 'input', 'button', 'textarea', 'select', 'iframe', 'object', 'embed', 'svg', 'math', 'script'],
  FORBID_ATTR: ['style', 'srcset', 'sizes', 'srcdoc', 'formaction'],
  ALLOW_DATA_ATTR: false,
  ALLOW_ARIA_ATTR: false,
}

export type MarkdownSegment =
  | { kind: 'prose'; html: string }
  | { kind: 'mermaid'; source: string }

export function sanitizeMarkdownHtml(raw: string): string {
  return purify.sanitize(raw, PURIFY_CONFIG)
}

export function sanitizeMarkdownSegments(segments: RawMarkdownSegment[]): MarkdownSegment[] {
  return segments.map((segment) => segment.kind === 'prose'
    ? { kind: 'prose', html: sanitizeMarkdownHtml(segment.html) }
    : segment)
}
