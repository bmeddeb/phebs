import mermaid from 'mermaid'
import elkLayouts from '@mermaid-js/layout-elk'
import type { PhebsTokens } from './theme'
import { MAX_RENDERED_DIAGRAMS } from './markdownBounds'
import {
  hasExternalResourceReference,
  hasRendererDirective,
  mermaidInitConfig,
  svgViolatesPolicy,
} from './mermaidConfig'

// T44.4: the mermaid wrapper — one async chunk carrying mermaid 11 and the
// ELK layout engine, imported only when a rendered document actually
// contains a fence. The security posture lives in mermaidConfig.ts (strict
// mode, no HTML labels, no click bindings) and is pinned by tests there.

mermaid.registerLayoutLoaders(elkLayouts)

let initializedFor = ''

function ensureInitialized(mode: 'light' | 'dark', tok: PhebsTokens) {
  // Mermaid config is global. Include every token consumed by the config so a
  // same-mode palette change cannot reuse stale colors.
  const key = [
    mode,
    tok.fill,
    tok.textPrimary,
    tok.cardBorder,
    tok.bandBg,
    tok.pageBg,
    tok.textSecondary,
  ].join('\u0000')
  if (initializedFor === key) return
  mermaid.initialize(mermaidInitConfig(mode, tok))
  initializedFor = key
}

let renderSeq = 0

async function renderMermaidNow(source: string, mode: 'light' | 'dark', tok: PhebsTokens): Promise<string> {
  ensureInitialized(mode, tok)
  const id = `phebs-mermaid-${++renderSeq}`
  // Mermaid needs a live DOM node for layout. Own that node explicitly so a
  // parser/drawer rejection cannot strand Mermaid's temporary subtree (its
  // default error path throws before its normal cleanup tail).
  const host = document.createElement('div')
  host.dataset.phebsMermaidHost = id
  host.style.cssText = 'position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none'
  document.body.append(host)
  let svg: string
  try {
    const rendered = await mermaid.render(id, source, host)
    svg = rendered.svg
  } finally {
    host.remove()
  }
  if (svgViolatesPolicy(svg)) {
    throw new Error('diagram produced disallowed output')
  }
  return svg
}

interface PendingRender {
  source: string
  mode: 'light' | 'dark'
  tok: PhebsTokens
  signal?: AbortSignal
  resolve: (svg: string) => void
  reject: (cause: unknown) => void
  abort?: () => void
}

const pendingRenders: PendingRender[] = []
let renderActive = false

function abortError() {
  return new DOMException('Aborted', 'AbortError')
}

function drainRenderQueue() {
  if (renderActive) return
  const request = pendingRenders.shift()
  if (!request) return
  if (request.abort) request.signal?.removeEventListener('abort', request.abort)
  if (request.signal?.aborted) {
    request.reject(abortError())
    drainRenderQueue()
    return
  }

  // Mermaid owns global configuration and an ELK render cannot be cancelled
  // once it starts. Serialize that one active render; aborted queued work is
  // removed below, so navigation/theme churn cannot accumulate stale waves.
  renderActive = true
  void renderMermaidNow(request.source, request.mode, request.tok)
    .then(request.resolve, request.reject)
    .finally(() => {
      renderActive = false
      drainRenderQueue()
    })
}

function enqueueRender(
  source: string,
  mode: 'light' | 'dark',
  tok: PhebsTokens,
  signal?: AbortSignal,
): Promise<string> {
  if (signal?.aborted) return Promise.reject(abortError())
  if (pendingRenders.length >= MAX_RENDERED_DIAGRAMS) {
    return Promise.reject(new Error('diagram render queue is full'))
  }
  return new Promise((resolve, reject) => {
    const request: PendingRender = { source, mode, tok, signal, resolve, reject }
    request.abort = () => {
      const index = pendingRenders.indexOf(request)
      if (index < 0) return
      pendingRenders.splice(index, 1)
      reject(abortError())
    }
    signal?.addEventListener('abort', request.abort, { once: true })
    pendingRenders.push(request)
    drainRenderQueue()
  })
}

export function renderMermaid(
  source: string,
  mode: 'light' | 'dark',
  tok: PhebsTokens,
  signal?: AbortSignal,
): Promise<string> {
  // Refuse renderer-reconfiguring source before touching mermaid (T44.4f):
  // a directive/config-frontmatter fence cannot flip layout or htmlLabels.
  if (hasRendererDirective(source)) {
    return Promise.reject(new Error('diagram configuration directives are not permitted'))
  }
  if (hasExternalResourceReference(source)) {
    return Promise.reject(new Error('diagram external resources are not permitted'))
  }
  return enqueueRender(source, mode, tok, signal)
}
