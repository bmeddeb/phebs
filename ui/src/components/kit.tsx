import type { ReactNode } from 'react'
import { useStyletron } from 'baseui'
import { Spinner } from 'baseui/spinner'
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
