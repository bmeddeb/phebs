import { useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { fetchServiceDetail, type ServiceDetail } from '../api'
import { href, navigate } from '../router'
import { focusRing, statusToneFor, usePhebsTokens } from '../theme'
import { isAbortError, relTime } from '../util'
import { IdentityText, StatusWord } from './kit'
import { scopeParams, type ActiveScope } from '../scope'

export type { ActiveScope }

// T43.8 scope context bar (charter §2 deep-link discipline). One exact scope
// — repository and service key, read from the URL — persists across
// search → directory → explorer → Workbench. The bar renders the scope with
// its catalog authority; clearing it is an explicit action, never a side
// effect of navigation. Authority is fetched fail-closed: a failed read
// renders "unavailable", never an invented status.


type Authority =
  | { state: 'loading' }
  | { state: 'unavailable'; reason: string }
  | { state: 'ready'; detail: ServiceDetail }

export function ScopeContextBar({ scope, path, params }: {
  scope: ActiveScope
  path: string
  params: URLSearchParams
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [authority, setAuthority] = useState<Authority>({ state: 'loading' })
  const service = scope.serviceKey

  useEffect(() => {
    if (!scope.repository || !scope.serviceKey) return
    const controller = new AbortController()
    setAuthority({ state: 'loading' })
    fetchServiceDetail(scope.repository, scope.serviceKey, controller.signal)
      .then((detail) => setAuthority({ state: 'ready', detail }))
      .catch((cause) => {
        if (!isAbortError(cause)) {
          setAuthority({ state: 'unavailable', reason: cause instanceof Error ? cause.message : String(cause) })
        }
      })
    return () => controller.abort()
  }, [scope.repository, scope.serviceKey])

  const clearScope = () => {
    const next = new URLSearchParams(params)
    next.delete('repository')
    next.delete('service_key')
    next.delete('scope')
    navigate(path, Object.fromEntries(next.entries()))
  }

  const jump = (target: string, extra: Record<string, string> = {}) =>
    href(target, { ...scopeParams(scope), ...extra })

  return (
    <div
      role="region"
      aria-label="Active scope"
      className={css({
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        flexWrap: 'wrap',
        minHeight: '34px',
        padding: '4px 20px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
        fontSize: '11px',
        lineHeight: '16px',
        '@media screen and (max-width: 720px)': { paddingLeft: '16px', paddingRight: '16px' },
      })}
    >
      <span className={css({ color: tok.textTertiary, fontWeight: 600, letterSpacing: '0.05em', textTransform: 'uppercase', fontSize: '10px' })}>
        Scope
      </span>
      <span className={css({ minWidth: 0, color: tok.textSecondary })}>
        <IdentityText title={scope.repository}>{shortRepository(scope.repository)}</IdentityText>
        {service && (
          <>
            {' · '}
            <IdentityText>{service}</IdentityText>
          </>
        )}
      </span>
      {service && scope.repository && <AuthorityCell authority={authority} />}
      <span className={css({ flex: 1 })} />
      <nav aria-label="Scoped surfaces" className={css({ display: 'flex', gap: '12px', alignItems: 'center', flexWrap: 'wrap' })}>
        {service && <BarLink href={jump('/search', { scope: 'service', q: '' })}>Search</BarLink>}
        <BarLink href={jump('/services')}>Directory</BarLink>
        {service && <BarLink href={jump('/relationships')}>Explorer</BarLink>}
        <BarLink href={jump('/workbench')}>Workbench</BarLink>
      </nav>
      <button
        type="button"
        onClick={clearScope}
        className={css({ border: 0, padding: 0, background: 'transparent', color: tok.textPrimary, fontSize: '11px', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer', ':focus-visible': focusRing(tok) })}
      >
        Clear scope
      </button>
    </div>
  )
}

function BarLink({ href: target, children }: { href: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <a
      href={target}
      className={css({ color: tok.textSecondary, textDecoration: 'none', fontWeight: 500, ':hover': { color: tok.textPrimary, textDecoration: 'underline' }, ':focus-visible': focusRing(tok) })}
    >
      {children}
    </a>
  )
}

function AuthorityCell({ authority }: { authority: Authority }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (authority.state === 'loading') {
    return <span role="status" className={css({ color: tok.textTertiary })}>reading authority…</span>
  }
  if (authority.state === 'unavailable') {
    return (
      <span title={authority.reason}>
        <StatusWord tone="blue">authority unavailable</StatusWord>
      </span>
    )
  }
  const record = authority.detail.service
  const tone = statusToneFor(record.status, tok) ? record.status : 'unavailable'
  const changed = record.changed_at && Number.isFinite(Date.parse(record.changed_at))
    ? record.changed_at
    : ''
  return (
    <span className={css({ display: 'inline-flex', alignItems: 'center', gap: '7px' })}>
      <StatusWord tone={tone === 'current' ? 'green' : tone === 'stale' ? 'amber' : tone === 'conflict' ? 'red' : tone === 'removed' ? 'neutral' : 'blue'}>
        {record.status}
      </StatusWord>
      {changed && (
        <span className={css({ color: tok.textTertiary })}>
          as of{' '}
          <time dateTime={changed} title={new Date(changed).toLocaleString()}>
            {relTime(changed)}
          </time>
        </span>
      )}
    </span>
  )
}

function shortRepository(repository: string): string {
  if (repository.length <= 44) return repository
  return `…${repository.slice(-43)}`
}
