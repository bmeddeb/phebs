import { marked } from 'marked'
import DOMPurify from 'dompurify'

// T44.3: repository markdown → sanitized HTML. This module is the entire
// trust boundary for rendering untrusted corpus content, so it is small,
// isolated, and conservative by construction:
//
//   1. marked parses with no HTML pass-through of its own concern — every
//      byte it emits still goes through DOMPurify;
//   2. DOMPurify strips scripts, event handlers, and any active content,
//      and we further forbid the tags/attributes that could exfiltrate or
//      execute (style, form controls, srcset, etc.);
//   3. links are neutralised — no javascript:/data: targets survive, and
//      every surviving anchor is forced to open safely;
//   4. repo-relative images are NOT fetched: their src is dropped and the
//      alt text is kept (T44.3 defers real image resolution, honestly,
//      rather than half-fetching against the wrong origin).
//
// Loaded only from a lazy chunk (the preview path), so marked/DOMPurify
// never touch the initial bundle.

// An isolated DOMPurify instance, not the shared default singleton, so this
// boundary's hook and config are private: no other call site can inherit
// our anchor-rewriting/image-stripping, and no future hook registered
// elsewhere can alter our output. Created once at module load (the module
// is a lazy chunk imported once).
const purify = DOMPurify(window)

purify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    // Any anchor that survives sanitize opens in a new context with no
    // opener and no referrer; sanitize has already removed unsafe hrefs.
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer nofollow')
  }
  if (node.tagName === 'IMG') {
    // Do not resolve or fetch repo-relative (or any) image sources in v1.
    // Replace with a placeholder built by construction from trusted parts
    // only — a static class and the alt as textContent (never innerHTML,
    // never an attacker-derived attribute). Nodes inserted here are NOT
    // guaranteed to be re-sanitized, so the marker must carry nothing an
    // attacker controls beyond inert text.
    const alt = node.getAttribute('alt') ?? ''
    const marker = document.createElement('span')
    marker.setAttribute('class', 'md-image-deferred')
    marker.textContent = alt ? `🖼 ${alt}` : '🖼 image'
    node.replaceWith(marker)
  }
})

const PURIFY_CONFIG = {
  // Allow structural + inline prose markup and code; forbid everything
  // that could execute, style, submit, or load cross-origin.
  ALLOWED_TAGS: [
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'br', 'hr',
    'a', 'em', 'strong', 'del', 'code', 'pre', 'blockquote',
    'ul', 'ol', 'li', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
    'img', 'span',
  ],
  ALLOWED_ATTR: ['href', 'title', 'alt', 'class', 'align'],
  // No data:/blob: URIs anywhere; only conventional link schemes.
  ALLOWED_URI_REGEXP: /^(?:https?|mailto):/i,
  FORBID_TAGS: ['style', 'form', 'input', 'button', 'textarea', 'select', 'iframe', 'object', 'embed', 'svg', 'math', 'script'],
  FORBID_ATTR: ['style', 'srcset', 'sizes', 'srcdoc', 'formaction'],
  ALLOW_DATA_ATTR: false,
}

export function renderMarkdown(source: string): string {
  // marked passes raw HTML through by default; every byte it emits still
  // goes through the isolated sanitizer below.
  const raw = marked.parse(source, { gfm: true, breaks: false, async: false }) as string
  return purify.sanitize(raw, PURIFY_CONFIG)
}
