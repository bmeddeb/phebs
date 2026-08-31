import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import { useStyletron } from 'baseui'
import { Spinner } from 'baseui/spinner'
import type { PipelineRefusalReceipt, ServiceRelationshipCitation } from '../api'
import type { Token } from '../highlight'
import type { PaletteName } from '../palette'
import { FONTS, MOTION, NUMERIC, animated, focusRing, toneFor, useMode, usePalette, usePhebsTokens, type Mode, type ToneName } from '../theme'

// The shared evidence kit (T43.3). One implementation per primitive; pages
// keep choosing the tone word, the kit owns the anatomy. Status colors always
// arrive paired with words (charter §3: color never carries meaning alone).

/**
 * Bordered status chip — the single chip dialect (square instrument form,
 * 11px floor). Border wears the solid role, the label wears the AA text role.
 */
export function StatusChip({ tone, children, title, role }: {
  // 'accent' is for informational identity (e.g. new-plane evidence), not a
  // status state — status chips use the closed tone vocabulary.
  tone: ToneName | 'accent'
  children: ReactNode
  title?: string
  role?: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = tone === 'accent' ? { text: tok.accent, solid: tok.accent } : toneFor(tone, tok)
  return (
    <span data-tone={tone} title={title} role={role} className={css({
      display: 'inline-flex',
      alignItems: 'center',
      minHeight: '20px',
      boxSizing: 'border-box',
      padding: '2px 7px',
      border: `1px solid ${color.solid}`,
      color: color.text,
      fontFamily: FONTS.MONO,
      fontSize: '11px',
      lineHeight: '16px',
      fontWeight: 600,
      letterSpacing: '0.01em',
      whiteSpace: 'nowrap',
    })}>{children}</span>
  )
}

/** Status dot paired with its state word. */
export function StatusWord({ tone, children }: { tone: ToneName; children: ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = toneFor(tone, tok)
  return (
    <span className={css({ display: 'inline-flex', alignItems: 'center', gap: '6px' })}>
      <span aria-hidden="true" className={css({ width: '7px', height: '7px', borderRadius: '999px', backgroundColor: color.solid, flexShrink: 0 })} />
      <span className={css({ color: color.text, fontSize: '11px', lineHeight: '16px', fontWeight: 600 })}>{children}</span>
    </span>
  )
}

/**
 * Semantic status band — the side-accented notice re-implemented locally on
 * four surfaces before this kit existed. The 3px accent is the incumbent
 * instrument idiom for claim-bearing bands, not decoration.
 */
export function StateNotice({ tone, title, children }: {
  tone: ToneName
  title?: ReactNode
  children: ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = toneFor(tone, tok)
  return (
    <div className={css({ borderLeft: `3px solid ${color.solid}`, backgroundColor: tok.bandBg, padding: '10px 12px' })}>
      {title && <div className={css({ color: tok.textPrimary, fontSize: '12px', lineHeight: '17px', fontWeight: 600 })}>{title}</div>}
      <div className={css({ color: tok.textSecondary, fontSize: '11px', lineHeight: '17px', marginTop: title ? '4px' : 0 })}>{children}</div>
    </div>
  )
}

/** Labeled loading state — a route or region is fetching, and says so. */
export function LoadingBlock({ label }: { label: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div role="status" className={css({ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '9px', padding: '28px 0', color: tok.textTertiary, fontSize: '12px', lineHeight: '18px' })}>
      <Spinner $size="small" /> {label}
    </div>
  )
}

/** Bounded error with a retry path — never rendered as partial data. */
export function ErrorNotice({ children, onRetry, retryLabel = 'Retry' }: {
  children: ReactNode
  onRetry?: () => void
  retryLabel?: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div role="alert" className={css({ borderLeft: `3px solid ${tok.status.conflict.solid}`, backgroundColor: tok.bandBg, padding: '10px 12px', display: 'flex', alignItems: 'baseline', gap: '12px', flexWrap: 'wrap' })}>
      <span className={css({ color: tok.status.conflict.text, fontSize: '12px', lineHeight: '18px', overflowWrap: 'anywhere', minWidth: 0 })}>{children}</span>
      {onRetry && (
        <button type="button" onClick={onRetry} className={css({ border: 0, padding: 0, background: 'transparent', color: tok.textPrimary, fontSize: '12px', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer', ':focus-visible': focusRing(tok) })}>
          {retryLabel}
        </button>
      )}
    </div>
  )
}

/**
 * Differentiated emptiness — an empty region names its scope and boundary
 * instead of implying absence (charter §2: an empty page is never an absence
 * claim).
 */
export function EmptyState({ title, detail }: { title: ReactNode; detail?: ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ padding: '22px 0', textAlign: 'center' })}>
      <div className={css({ color: tok.textSecondary, fontSize: '13px', lineHeight: '19px', fontWeight: 600 })}>{title}</div>
      {detail && <div className={css({ color: tok.textTertiary, fontSize: '12px', lineHeight: '18px', marginTop: '4px' })}>{detail}</div>}
    </div>
  )
}

/** Monospace identity (path, key, digest, revision) with tabular numerals. */
export function IdentityText({ children, title }: { children: ReactNode; title?: string }) {
  const [css] = useStyletron()
  return (
    <code title={title} className={css({ fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px', overflowWrap: 'anywhere', ...NUMERIC })}>{children}</code>
  )
}

/**
 * Claim boundary (charter §3): a one-line summary in the contract's own
 * words — the caveat's first clause — with an expandable establishes /
 * does-not-establish disclosure carrying the exact contract language.
 * `caveat` is rendered verbatim; server-delivered text is preferred over
 * any client mirror. The two sections are exact substrings split at the
 * contract's own first ';' (the caveats' establishes/negation boundary);
 * a caveat without that shape renders whole, never re-worded. Optional
 * children carry pinned presentation addenda after the contract text.
 */
export function ClaimBoundary({ caveat, summary, children }: {
  caveat: string
  summary?: ReactNode
  children?: ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const cut = caveat.indexOf(';')
  const establishes = cut === -1 ? caveat : caveat.slice(0, cut + 1)
  const doesNotEstablish = cut === -1 ? '' : caveat.slice(cut + 1).trim()
  const label = (text: string) => (
    <span className={css({ display: 'block', color: tok.textPrimary, fontSize: '11px', lineHeight: '15px', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em' })}>{text}</span>
  )
  return (
    <CaveatCollapse summary={summary ?? firstClause(caveat)}>
      <div className={css({ display: 'grid', gap: '7px' })}>
        <div>
          {label('Establishes')}
          <p className={css({ margin: '2px 0 0' })}>{establishes}</p>
        </div>
        {doesNotEstablish && (
          <div>
            {label('Does not establish')}
            <p className={css({ margin: '2px 0 0' })}>{doesNotEstablish}</p>
          </div>
        )}
        {children}
      </div>
    </CaveatCollapse>
  )
}

// The summary is the contract's own opening words, never a paraphrase.
function firstClause(caveat: string): string {
  const cut = caveat.search(/[;—.]/)
  return cut === -1 ? caveat : `${caveat.slice(0, cut).trimEnd()} …`
}

/** One-line caveat that collapses open, never disappears (charter §3). */
export function CaveatCollapse({ summary, children }: { summary: ReactNode; children: ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <details className={css({ minWidth: 0 })}>
      <summary className={css({ color: tok.textSecondary, fontSize: '11px', lineHeight: '17px', cursor: 'pointer', ':focus-visible': focusRing(tok) })}>{summary}</summary>
      <div
        className={css({
          marginTop: '6px',
          color: tok.textSecondary,
          fontSize: '11px',
          lineHeight: '17px',
          // T43.12: disclosure is a state transition — the body fades in on
          // each open (the display toggle restarts the animation).
          ...animated({ from: { opacity: 0 }, to: { opacity: 1 } }, { duration: MOTION.element }),
        })}
      >
        {children}
      </div>
    </details>
  )
}

/**
 * The citation object's trigger (charter §3: a citation renders as
 * path:line). The chip is the citation's identity, not a generic button.
 */
export function CitationChip({ path, span, onOpen, title, expanded }: {
  path: string
  span: { start_line: number; end_line: number }
  onOpen: () => void
  title?: string
  // For inline-toggle disclosures: renders aria-expanded when defined.
  expanded?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const label = span.end_line > span.start_line
    ? `${path}:${span.start_line}–${span.end_line}`
    : `${path}:${span.start_line}`
  return (
    <button
      type="button"
      aria-haspopup="dialog"
      aria-expanded={expanded}
      title={title ?? label}
      onClick={onOpen}
      className={css({
        maxWidth: '100%',
        border: `1px solid ${tok.cardBorder}`,
        padding: '2px 7px',
        background: 'transparent',
        color: tok.textPrimary,
        fontFamily: FONTS.MONO,
        fontSize: '11px',
        lineHeight: '16px',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        cursor: 'pointer',
        ':hover': { backgroundColor: tok.hoverFill },
        ':focus-visible': focusRing(tok),
      })}
    >
      {label}
    </button>
  )
}

/**
 * T40.1 closed refusal receipt as a first-class card (T43.10). Presents
 * exactly the delivered fields — stage, generation kind, classification,
 * dimension, observed scalar, admitted limit — no invented prose.
 */
export function RefusalCard({ refusal }: { refusal: PipelineRefusalReceipt }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const limitClass = refusal.classification === 'limit'
  return (
    <div
      role="status"
      className={css({ borderLeft: `3px solid ${tok.status.conflict.solid}`, backgroundColor: tok.bandBg, padding: '9px 11px', display: 'grid', gap: '3px' })}
      title={refusal.schema}
    >
      <span className={css({ color: tok.status.conflict.text, fontSize: '11px', lineHeight: '16px', fontWeight: 600 })}>
        Refused · {refusal.stage} · {refusal.generation_kind}
      </span>
      <span className={css({ color: tok.textSecondary, fontSize: '11px', lineHeight: '16px' })}>
        {refusal.classification} · dimension <IdentityText>{refusal.dimension}</IdentityText>
        {limitClass && (
          <>
            {' · observed '}
            <span className={css({ ...NUMERIC, color: tok.textPrimary })}>{refusal.observed}</span>
            {' of limit '}
            <span className={css({ ...NUMERIC, color: tok.textPrimary })}>{refusal.limit}</span>
          </>
        )}
      </span>
    </div>
  )
}

/** One shared exact relationship-citation disclosure. */
export function CitationPanel({ id, loading, error, citation, onClose, onRefresh, refreshLabel = 'Refresh evidence rows' }: {
  id: string
  loading: boolean
  error: string
  citation: ServiceRelationshipCitation | null
  onClose: () => void
  // The fail-closed refresh path: an expired or superseded citation cannot
  // be re-read; the caller refetches its rows at the current generation.
  onRefresh?: () => void
  refreshLabel?: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const titleID = `${id}-title`
  const panelRef = useRef<HTMLElement>(null)
  const openerRef = useRef<Element | null>(null)
  // Keyboard completeness: focus enters the panel on mount, Escape closes,
  // and focus returns to the opening chip on unmount.
  useEffect(() => {
    openerRef.current = document.activeElement
    panelRef.current?.focus()
    return () => {
      if (openerRef.current instanceof HTMLElement) openerRef.current.focus()
    }
  }, [])
  return (
    <aside
      ref={panelRef}
      role="dialog"
      aria-modal="false"
      aria-labelledby={titleID}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key === 'Escape') onClose()
      }}
      className={css({
        marginTop: '10px',
        border: `1px solid ${tok.cardBorder}`,
        padding: '14px',
        backgroundColor: tok.bandBg,
        // T43.12: the chip → panel state transition gets the one panel
        // entrance (charter §3) — the evidence surface arrives, it does
        // not teleport.
        ...animated(
          { from: { opacity: 0, transform: 'translateY(9px)' }, to: { opacity: 1, transform: 'translateY(0)' } },
          { duration: MOTION.panel },
        ),
        ':focus-visible': focusRing(tok),
      })}
    >
      <div className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '12px' })}>
        <div>
          <h2 id={titleID} className={css({ margin: 0, fontSize: '12px', lineHeight: '17px', color: tok.textPrimary })}>Exact source citation</h2>
          {citation && <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '11px', lineHeight: '15px' })}>{citation.evidence.path} · lines {citation.evidence.span.start_line}–{citation.evidence.span.end_line}</div>}
        </div>
        <button type="button" onClick={onClose} className={css({ border: 0, padding: 0, background: 'transparent', color: tok.textPrimary, fontSize: '11px', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer', ':focus-visible': focusRing(tok) })}>Close citation</button>
      </div>
      {loading && <div role="status" className={css({ marginTop: '10px', color: tok.textSecondary, fontSize: '11px' })}>Reading immutable source span…</div>}
      {error && (
        <div role="alert" className={css({ marginTop: '10px', display: 'flex', alignItems: 'baseline', gap: '10px', flexWrap: 'wrap' })}>
          <span className={css({ color: tok.status.conflict.text, fontSize: '11px' })}>{error}</span>
          {onRefresh && (
            <button type="button" onClick={onRefresh} className={css({ border: 0, padding: 0, background: 'transparent', color: tok.textPrimary, fontSize: '11px', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer', ':focus-visible': focusRing(tok) })}>
              {refreshLabel}
            </button>
          )}
        </div>
      )}
      {citation && (
        <>
          <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: '8px', marginTop: '10px', '@media screen and (max-width: 720px)': { gridTemplateColumns: '1fr 1fr' } })}>
            <CitationIdentity label="Generation" value={citation.generation} />
            <CitationIdentity label="Root" value={citation.root_digest} />
            <CitationIdentity label="Object" value={citation.evidence.object_id} />
            <CitationIdentity label="Content" value={citation.evidence.content_digest} />
          </div>
          <CitedSource path={citation.evidence.path} content={citation.content} />
        </>
      )}
    </aside>
  )
}

// T44.1: cited bytes render highlighted through the same best-effort,
// line-oriented tokenizer search chunks use. The text is exactly
// `content` — only presentation spans are added — and any failure in
// the lazy tokenizer/language load falls back to the plain bytes. The
// tokenizer and language pack load lazily so the evidence kit adds no
// CodeMirror weight to the initial chunk.
//
// Highlighting ceiling (T44.1f): citations may carry multi-MiB spans, and
// tokenizing recurs on every mount and theme change on the main thread.
// Content over 65,536 UTF-16 units or 1,500 lines renders as the exact
// plain bytes instead — the guard runs before any lazy import, so an
// oversized citation costs nothing beyond the native text node. The
// bounds are documented in docs/guides/WORKFLOWS.md and pinned by
// exact-bound and one-over tests.
const CITATION_HIGHLIGHT_MAX_UNITS = 65_536
const CITATION_HIGHLIGHT_MAX_LINES = 1_500

type CitedSourceHighlight = {
  path: string
  content: string
  mode: Mode
  palette: PaletteName
  lines: string[]
  tokenLines: Token[][]
}

function CitedSource({ path, content }: { path: string; content: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <pre tabIndex={0} className={css({ margin: '10px 0 0', maxHeight: '280px', overflow: 'auto', padding: '12px', border: `1px solid ${tok.cardBorder}`, backgroundColor: tok.pageBg, color: tok.plainCode, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '17px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', ':focus-visible': focusRing(tok) })}>
      <HighlightedCitationBytes path={path} content={content} />
    </pre>
  )
}

export function HighlightedCitationBytes({ path, content }: { path: string; content: string }) {
  const { mode } = useMode()
  const { palette } = usePalette()
  const [highlight, setHighlight] = useState<CitedSourceHighlight | null>(null)
  useEffect(() => {
    let active = true
    setHighlight(null)
    if (content.length > CITATION_HIGHLIGHT_MAX_UNITS) return
    if (!citationLineCountWithinBound(content)) return
    void import('../lang')
      .then(async (lang) => {
        const language = await lang.languageFor(path)
        if (!active || language === null) return
        const tokenizer = await import('../highlight')
        if (!active) return
        const lines = content.split('\n')
        setHighlight({
          path,
          content,
          mode,
          palette,
          lines,
          tokenLines: lines.map((line) => tokenizer.tokenize(line, language, mode, palette)),
        })
      })
      .catch(() => {})
    return () => { active = false }
  }, [path, content, mode, palette])
  const current = highlight?.path === path &&
    highlight.content === content &&
    highlight.mode === mode &&
    highlight.palette === palette
    ? highlight
    : null
  return (
    <>
      {current
        ? current.lines.map((line, index) => (
          <Fragment key={index}>
            {index > 0 ? '\n' : null}
            {(current.tokenLines[index] ?? [{ from: 0, to: line.length }]).map((span, spanIndex) => (
              <span key={spanIndex} style={span.color ? { color: span.color, fontStyle: span.fontStyle } : undefined}>
                {line.slice(span.from, span.to)}
              </span>
            ))}
          </Fragment>
        ))
        : content}
    </>
  )
}

function citationLineCountWithinBound(content: string): boolean {
  let lineCount = 1
  let offset = 0
  while (lineCount <= CITATION_HIGHLIGHT_MAX_LINES) {
    const newline = content.indexOf('\n', offset)
    if (newline === -1) return true
    lineCount += 1
    if (lineCount > CITATION_HIGHLIGHT_MAX_LINES) return false
    offset = newline + 1
  }
  return false
}

function CitationIdentity({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ minWidth: 0, color: tok.textTertiary, fontSize: '11px', lineHeight: '15px' })}>
      {label}
      <IdentityText title={value}>{shortIdentity(value)}</IdentityText>
    </div>
  )
}

function shortIdentity(value: string): string {
  return value.length <= 28 ? value : `${value.slice(0, 17)}…${value.slice(-8)}`
}
