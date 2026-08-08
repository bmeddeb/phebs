import { useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Drawer } from 'baseui/drawer'
import type { SearchScopeReceipt } from '../api'
import { CheckIcon, CopyIcon } from '../icons'
import { FONTS, MOTION, NUMERIC, focusRing, statusToneFor, usePhebsTokens, type PhebsTokens } from '../theme'
import { IdentityText, StatusWord } from './kit'

// T43.5 chip → drawer → receipt (charter §3 Disclosure). The chip is the
// compact authority on the answer; the drawer is the authority altitude
// (state, service authority, scope, revision pins, citations); the receipt
// section is the audit altitude with copyable digests. Digests never lead a
// surface — they live only here, complete at their own level. Receipts are
// validated fail-closed at the fetch boundary (api.ts), so nothing here
// renders placeholder identity.

/** A rendered result the receipt can be checked against. */
export interface ScopeCitation {
  repository: string
  label: string
  href: string
}

export function AuthorityChipButton({ receipt, onOpen }: {
  receipt: SearchScopeReceipt
  onOpen: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const service = receipt.kind === 'service'
  const tone = service
    ? statusToneFor(receipt.service_status ?? '', tok) ?? tok.status.removed
    : tok.status.removed
  const label = service
    ? receipt.revisions.length > 0
      ? `${receipt.service_status} · as of ${receipt.revisions[0].commit.slice(0, 7)}`
      : `${receipt.service_status} · no cited results`
    : `${receipt.revisions.length} revision pin${receipt.revisions.length === 1 ? '' : 's'}`
  return (
    <button
      type="button"
      aria-haspopup="dialog"
      onClick={onOpen}
      className={css({
        display: 'inline-flex',
        alignItems: 'center',
        minHeight: '20px',
        boxSizing: 'border-box',
        padding: '2px 7px',
        border: `1px solid ${tone.solid}`,
        background: 'transparent',
        color: tone.text,
        fontFamily: FONTS.MONO,
        fontSize: '11px',
        lineHeight: '16px',
        fontWeight: 600,
        letterSpacing: '0.01em',
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

const CITATION_DISPLAY_CAP = 20

export function AuthorityDrawer({ receipt, citations = [], open, onClose }: {
  receipt: SearchScopeReceipt
  citations?: ScopeCitation[]
  open: boolean
  onClose: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const service = receipt.kind === 'service'
  const authority = receipt.service_authority
  const shown = citations.slice(0, CITATION_DISPLAY_CAP)
  return (
    <Drawer
      isOpen={open}
      onClose={onClose}
      autoFocus
      overrides={{
        DrawerContainer: {
          props: { role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Search scope authority' },
          style: {
            transitionDuration: MOTION.panel,
            backgroundColor: tok.pageBg,
            '@media (prefers-reduced-motion: reduce)': { transitionDuration: '0ms' },
          },
        },
        DrawerBody: { style: { marginTop: '52px', marginBottom: '20px', marginLeft: '20px', marginRight: '20px' } },
        Close: { style: { color: tok.textSecondary, ':focus-visible': focusRing(tok) } },
      }}
    >
      <div className={css({ color: tok.textPrimary })}>
        <h2 className={css({ margin: '4px 0 0', fontSize: '15px', lineHeight: '22px', fontWeight: 600 })}>
          Search scope authority
        </h2>

        <Section title="State">
          {service ? (
            <>
              <StatusWord tone={receipt.service_status === 'current' ? 'green' : receipt.service_status === 'stale' ? 'amber' : receipt.service_status === 'conflict' ? 'red' : receipt.service_status === 'removed' ? 'neutral' : 'blue'}>
                {receipt.service_status}
              </StatusWord>
              <p className={css(body(tok))}>
                Shared paths are included; unowned paths are excluded. Membership
                policy <IdentityText>{receipt.membership_policy}</IdentityText>.
              </p>
            </>
          ) : (
            <p className={css(body(tok))}>
              Every visible indexed repository, at the exact revision pins below.
              Visibility is authorization-bounded; this is not a completeness claim.
            </p>
          )}
        </Section>

        {service && authority && (
          <Section title="Service authority">
            <Row label="Status">
              <span>{authority.status} · incarnation <span className={css({ ...NUMERIC })}>{authority.incarnation}</span></span>
            </Row>
            <Row label="Revision">
              <IdentityText>{authority.revision_branch ? `${authority.revision_branch} @ ` : ''}{authority.revision_commit}</IdentityText>
            </Row>
            <Row label="Catalog"><GenerationPair current={authority.current_catalog_generation} active={authority.active_catalog_generation} /></Row>
            <Row label="Source gen."><IdentityText>{authority.active_source_generation}</IdentityText></Row>
            <Row label="Desired gen."><IdentityText>{authority.active_desired_generation}</IdentityText></Row>
            <Row label="Repo source"><IdentityText>{authority.repository_source_generation}</IdentityText></Row>
            <Row label="Repo search"><IdentityText>{authority.repository_search_generation}</IdentityText></Row>
            <Row label="Paths"><span className={css({ ...NUMERIC })}>{authority.path_count}</span></Row>
          </Section>
        )}

        <Section title="Scope">
          <Row label="Kind">{service ? 'Service' : 'All code'}</Row>
          {receipt.repository && <Row label="Repository"><IdentityText>{receipt.repository}</IdentityText></Row>}
          {receipt.service_key && <Row label="Service key"><IdentityText>{receipt.service_key}</IdentityText></Row>}
        </Section>

        <Section title={`Revision pins (${receipt.revisions.length})`}>
          {receipt.revisions.length === 0 ? (
            <p className={css(body(tok))}>No revisions were pinned: the result set is empty.</p>
          ) : (
            <ul className={css({ margin: 0, padding: 0, listStyle: 'none', display: 'grid', gap: '7px' })}>
              {receipt.revisions.map((revision) => (
                <li key={`${revision.repository}@${revision.commit}`} className={css({ display: 'flex', alignItems: 'baseline', gap: '8px', minWidth: 0 })}>
                  <span className={css({ minWidth: 0, flex: 1 })}>
                    <IdentityText>{revision.repository}</IdentityText>
                    <span className={css({ display: 'block', color: tok.textSecondary })}>
                      <IdentityText>{revision.commit}</IdentityText>
                    </span>
                  </span>
                  <CopyButton label={`commit ${revision.commit.slice(0, 7)}`} value={revision.commit} />
                </li>
              ))}
            </ul>
          )}
        </Section>

        <Section title={`Citations (${receipt.result_files})`}>
          {citations.length === 0 ? (
            <p className={css(body(tok))}>
              {receipt.result_files === 0
                ? 'The result set cites no files.'
                : 'The cited files are the results rendered on the page.'}
            </p>
          ) : (
            <>
              <ul className={css({ margin: 0, padding: 0, listStyle: 'none', display: 'grid', gap: '5px' })}>
                {shown.map((citation) => (
                  <li key={citation.href} className={css({ minWidth: 0 })}>
                    <a href={citation.href} className={css({ color: tok.textPrimary, textDecoration: 'none', ':hover': { textDecoration: 'underline' }, ':focus-visible': focusRing(tok) })}>
                      <IdentityText>{citation.label}</IdentityText>
                    </a>
                  </li>
                ))}
              </ul>
              {citations.length > CITATION_DISPLAY_CAP && (
                <p className={css(body(tok))}>…and {citations.length - CITATION_DISPLAY_CAP} more cited files on the page.</p>
              )}
              <VerifyAgainstResults receipt={receipt} citations={citations} />
            </>
          )}
        </Section>

        <details className={css({ margin: '16px 0 8px' })}>
          <summary className={css({ fontSize: '12px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary, cursor: 'pointer', ':focus-visible': focusRing(tok) })}>
            Receipt
          </summary>
          <div className={css({ marginTop: '10px', display: 'grid', gap: '10px' })}>
            <Row label="Schema"><IdentityText>{receipt.schema}</IdentityText></Row>
            <DigestRow label="Receipt digest" value={receipt.digest} />
            <DigestRow label="Expression digest" value={receipt.expression_digest} />
            <DigestRow label="Result-set digest" value={receipt.result_set_digest} />
            {service && authority && (
              <>
                <DigestRow label="Authority digest" value={authority.digest} />
                <DigestRow label="State digest" value={authority.service_state_digest} />
                <DigestRow label="Summary digest" value={authority.state_summary_digest} />
                <DigestRow label="Path digest" value={authority.path_digest} />
              </>
            )}
            <p className={css(body(tok))}>
              Digests identify this receipt's exact scope expression and result
              set. Copy the full receipt for independent verification.
            </p>
            <CopyReceiptButton receipt={receipt} />
          </div>
        </details>
      </div>
    </Drawer>
  )
}

function GenerationPair({ current, active }: { current: string; active: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <span>
      <IdentityText>{active}</IdentityText>
      {current !== active && (
        <span className={css({ display: 'block', color: tok.status.stale.text, fontSize: '11px', lineHeight: '16px' })}>
          current is <IdentityText>{current}</IdentityText> — active lags
        </span>
      )}
    </span>
  )
}

/**
 * Presentation-level verification: the receipt is checked against what the
 * page actually rendered — cited-file count and revision-pin coverage. No
 * cryptographic or backend claim is made or implied.
 */
function VerifyAgainstResults({ receipt, citations }: {
  receipt: SearchScopeReceipt
  citations: ScopeCitation[]
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [verdict, setVerdict] = useState<{ ok: boolean; detail: string } | null>(null)
  const check = () => {
    const pinned = new Set(receipt.revisions.map((revision) => revision.repository))
    const unpinned = citations.filter((citation) => !pinned.has(citation.repository))
    const countOK = citations.length === receipt.result_files
    if (countOK && unpinned.length === 0) {
      setVerdict({ ok: true, detail: `${citations.length} rendered cited files match the receipt count; every cited repository has a revision pin.` })
    } else {
      const parts = []
      if (!countOK) parts.push(`receipt counts ${receipt.result_files} cited files but ${citations.length} are rendered`)
      if (unpinned.length > 0) parts.push(`${unpinned.length} rendered files come from repositories with no revision pin`)
      setVerdict({ ok: false, detail: parts.join('; ') + '.' })
    }
  }
  return (
    <div className={css({ marginTop: '10px', display: 'grid', gap: '6px', justifyItems: 'start' })}>
      <button
        type="button"
        onClick={check}
        className={css({
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '5px',
          padding: '5px 10px',
          background: 'transparent',
          color: tok.textPrimary,
          fontSize: '12px',
          fontWeight: 600,
          cursor: 'pointer',
          ':hover': { backgroundColor: tok.hoverFill },
          ':focus-visible': focusRing(tok),
        })}
      >
        Check receipt against rendered results
      </button>
      {verdict && (
        <div role="status" className={css({ display: 'flex', alignItems: 'baseline', gap: '7px' })}>
          <StatusWord tone={verdict.ok ? 'green' : 'red'}>{verdict.ok ? 'consistent' : 'mismatch'}</StatusWord>
          <span className={css({ color: tok.textSecondary, fontSize: '11px', lineHeight: '17px' })}>{verdict.detail}</span>
        </div>
      )}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section className={css({ marginTop: '16px' })}>
      <h3 className={css({ margin: '0 0 7px', fontSize: '12px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary })}>{title}</h3>
      {children}
    </section>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ display: 'flex', alignItems: 'baseline', gap: '10px', minWidth: 0, marginTop: '4px' })}>
      <span className={css({ flexShrink: 0, width: '110px', color: tok.textTertiary, fontSize: '11px', lineHeight: '17px' })}>{label}</span>
      <span className={css({ minWidth: 0, fontSize: '12px', lineHeight: '17px', overflowWrap: 'anywhere' })}>{children}</span>
    </div>
  )
}

function DigestRow({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ display: 'flex', alignItems: 'baseline', gap: '8px', minWidth: 0 })}>
      <span className={css({ flexShrink: 0, width: '110px', color: tok.textTertiary, fontSize: '11px', lineHeight: '17px' })}>{label}</span>
      <span className={css({ minWidth: 0, flex: 1 })}><IdentityText>{value}</IdentityText></span>
      <CopyButton label={label} value={value} />
    </div>
  )
}

type CopyState = 'idle' | 'done' | 'failed'

function useCopy(): [CopyState, (value: string) => void] {
  const [state, setState] = useState<CopyState>('idle')
  const timer = useRef(0)
  const copy = (value: string) => {
    const settle = (next: CopyState) => {
      setState(next)
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setState('idle'), 1600)
    }
    if (!navigator.clipboard?.writeText) {
      settle('failed')
      return
    }
    navigator.clipboard.writeText(value).then(
      () => settle('done'),
      () => settle('failed'),
    )
  }
  return [state, copy]
}

function CopyButton({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [state, copy] = useCopy()
  return (
    <button
      type="button"
      aria-label={state === 'done' ? `Copied ${label}` : state === 'failed' ? `Copy failed: ${label}` : `Copy ${label}`}
      onClick={() => copy(value)}
      className={css({
        flexShrink: 0,
        display: 'inline-flex',
        alignItems: 'center',
        border: 0,
        padding: '3px',
        background: 'transparent',
        color: state === 'done' ? tok.status.current.solid : state === 'failed' ? tok.status.conflict.text : tok.textTertiary,
        cursor: 'pointer',
        ':hover': { color: tok.textPrimary },
        ':focus-visible': focusRing(tok),
      })}
    >
      {state === 'done' ? <CheckIcon size={13} /> : <CopyIcon size={13} />}
    </button>
  )
}

function CopyReceiptButton({ receipt }: { receipt: SearchScopeReceipt }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [state, copy] = useCopy()
  return (
    <button
      type="button"
      onClick={() => copy(JSON.stringify(receipt, null, 2))}
      className={css({
        justifySelf: 'start',
        border: `1px solid ${state === 'failed' ? tok.status.conflict.solid : tok.cardBorder}`,
        borderRadius: '5px',
        padding: '5px 10px',
        background: 'transparent',
        color: state === 'failed' ? tok.status.conflict.text : tok.textPrimary,
        fontSize: '12px',
        fontWeight: 600,
        cursor: 'pointer',
        ':hover': { backgroundColor: tok.hoverFill },
        ':focus-visible': focusRing(tok),
      })}
    >
      {state === 'done' ? 'Receipt copied' : state === 'failed' ? 'Copy failed — clipboard unavailable' : 'Copy receipt JSON'}
    </button>
  )
}

function body(tok: PhebsTokens) {
  return { margin: '6px 0 0', color: tok.textSecondary, fontSize: '12px', lineHeight: '18px' }
}
