import type { ReactNode } from 'react'
import { useStyletron } from 'baseui'
import { Spinner } from 'baseui/spinner'
import type { ServiceRelationshipCitation } from '../api'
import { FONTS, NUMERIC, focusRing, toneFor, usePhebsTokens, type ToneName } from '../theme'

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
 * words — the caveat's first clause — with the exact full text one gesture
 * away. `caveat` is rendered verbatim; server-delivered text is preferred
 * over any client mirror. Optional children carry pinned presentation
 * addenda after the contract text.
 */
export function ClaimBoundary({ caveat, summary, children }: {
  caveat: string
  summary?: ReactNode
  children?: ReactNode
}) {
  return (
    <CaveatCollapse summary={summary ?? firstClause(caveat)}>
      <p style={{ margin: 0 }}>{caveat}</p>
      {children}
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
      <div className={css({ marginTop: '6px', color: tok.textSecondary, fontSize: '11px', lineHeight: '17px' })}>{children}</div>
    </details>
  )
}

/** One shared exact relationship-citation disclosure. */
export function CitationPanel({ id, loading, error, citation, onClose }: {
  id: string
  loading: boolean
  error: string
  citation: ServiceRelationshipCitation | null
  onClose: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const titleID = `${id}-title`
  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-labelledby={titleID}
      className={css({ marginTop: '10px', border: `1px solid ${tok.cardBorder}`, padding: '14px', backgroundColor: tok.bandBg })}
    >
      <div className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '12px' })}>
        <div>
          <h2 id={titleID} className={css({ margin: 0, fontSize: '12px', lineHeight: '17px', color: tok.textPrimary })}>Exact source citation</h2>
          {citation && <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '10px', lineHeight: '15px' })}>{citation.evidence.path} · lines {citation.evidence.span.start_line}–{citation.evidence.span.end_line}</div>}
        </div>
        <button type="button" onClick={onClose} className={css({ border: 0, padding: 0, background: 'transparent', color: tok.textPrimary, fontSize: '11px', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer', ':focus-visible': focusRing(tok) })}>Close citation</button>
      </div>
      {loading && <div role="status" className={css({ marginTop: '10px', color: tok.textSecondary, fontSize: '11px' })}>Reading immutable source span…</div>}
      {error && <div role="alert" className={css({ marginTop: '10px', color: tok.status.conflict.text, fontSize: '11px' })}>{error}</div>}
      {citation && (
        <>
          <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: '8px', marginTop: '10px', '@media screen and (max-width: 720px)': { gridTemplateColumns: '1fr 1fr' } })}>
            <CitationIdentity label="Generation" value={citation.generation} />
            <CitationIdentity label="Root" value={citation.root_digest} />
            <CitationIdentity label="Object" value={citation.evidence.object_id} />
            <CitationIdentity label="Content" value={citation.evidence.content_digest} />
          </div>
          <pre tabIndex={0} className={css({ margin: '10px 0 0', maxHeight: '280px', overflow: 'auto', padding: '12px', border: `1px solid ${tok.cardBorder}`, backgroundColor: tok.pageBg, color: tok.plainCode, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '17px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', ':focus-visible': focusRing(tok) })}>{citation.content}</pre>
        </>
      )}
    </aside>
  )
}

function CitationIdentity({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ minWidth: 0, color: tok.textTertiary, fontSize: '10px', lineHeight: '15px' })}>
      {label}
      <IdentityText title={value}>{shortIdentity(value)}</IdentityText>
    </div>
  )
}

function shortIdentity(value: string): string {
  return value.length <= 28 ? value : `${value.slice(0, 17)}…${value.slice(-8)}`
}
