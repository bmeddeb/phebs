import { useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Drawer } from 'baseui/drawer'
import type { SearchScopeReceipt } from '../api'
import { CheckIcon, CopyIcon } from '../icons'
import { FONTS, MOTION, NUMERIC, focusRing, statusToneFor, usePhebsTokens, type PhebsTokens } from '../theme'
import { IdentityText, StatusWord } from './kit'

// T43.5 chip → drawer → receipt (charter §3 Disclosure). The chip is the
// compact authority on the answer; the drawer is the authority altitude
// (state, scope, generations, counts); the receipt section is the audit
// altitude with copyable digests. Digests never lead a surface — they live
// only here, behind two gestures, complete at their own level.

export function AuthorityChipButton({ receipt, onOpen }: {
  receipt: SearchScopeReceipt
  onOpen: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const service = receipt.kind === 'service'
  const tone = service && receipt.service_status
    ? statusToneFor(receipt.service_status, tok) ?? tok.status.removed
    : tok.status.removed
  const label = service
    ? `${receipt.service_status ?? 'service'} · as of ${receipt.revisions[0]?.commit.slice(0, 7) ?? 'unpinned'}`
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

export function AuthorityDrawer({ receipt, open, onClose }: {
  receipt: SearchScopeReceipt
  open: boolean
  onClose: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const service = receipt.kind === 'service'
  return (
    <Drawer
      isOpen={open}
      onClose={onClose}
      autoFocus
      overrides={{
        DrawerContainer: {
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
      <div role="document" aria-label="Search scope authority" className={css({ color: tok.textPrimary })}>
        <h2 className={css({ margin: '4px 0 0', fontSize: '15px', lineHeight: '22px', fontWeight: 600 })}>
          Search scope authority
        </h2>

        <Section title="State">
          {service ? (
            <>
              <StatusWord tone={receipt.service_status === 'current' ? 'green' : receipt.service_status === 'stale' ? 'amber' : receipt.service_status === 'conflict' ? 'red' : receipt.service_status === 'removed' ? 'neutral' : 'blue'}>
                {receipt.service_status ?? 'unavailable'}
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

        <Section title="Scope">
          <Row label="Kind">{service ? 'Service' : 'All code'}</Row>
          {receipt.repository && <Row label="Repository"><IdentityText>{receipt.repository}</IdentityText></Row>}
          {receipt.service_key && <Row label="Service key"><IdentityText>{receipt.service_key}</IdentityText></Row>}
        </Section>

        <Section title={`Generations (${receipt.revisions.length})`}>
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
        </Section>

        <Section title="Result set">
          <Row label="Cited files"><span className={css({ ...NUMERIC })}>{receipt.result_files}</span></Row>
          <Row label="Matches"><span className={css({ ...NUMERIC })}>{receipt.result_matches}</span></Row>
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

function CopyButton({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [done, setDone] = useState(false)
  const timer = useRef(0)
  return (
    <button
      type="button"
      aria-label={done ? `Copied ${label}` : `Copy ${label}`}
      onClick={() => {
        void navigator.clipboard?.writeText(value)
        setDone(true)
        window.clearTimeout(timer.current)
        timer.current = window.setTimeout(() => setDone(false), 1200)
      }}
      className={css({
        flexShrink: 0,
        display: 'inline-flex',
        alignItems: 'center',
        border: 0,
        padding: '3px',
        background: 'transparent',
        color: done ? tok.status.current.solid : tok.textTertiary,
        cursor: 'pointer',
        ':hover': { color: tok.textPrimary },
        ':focus-visible': focusRing(tok),
      })}
    >
      {done ? <CheckIcon size={13} /> : <CopyIcon size={13} />}
    </button>
  )
}

function CopyReceiptButton({ receipt }: { receipt: SearchScopeReceipt }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [done, setDone] = useState(false)
  const timer = useRef(0)
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard?.writeText(JSON.stringify(receipt, null, 2))
        setDone(true)
        window.clearTimeout(timer.current)
        timer.current = window.setTimeout(() => setDone(false), 1200)
      }}
      className={css({
        justifySelf: 'start',
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
      {done ? 'Receipt copied' : 'Copy receipt JSON'}
    </button>
  )
}

function body(tok: PhebsTokens) {
  return { margin: '6px 0 0', color: tok.textSecondary, fontSize: '12px', lineHeight: '18px' }
}
