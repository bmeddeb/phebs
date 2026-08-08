import { useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { createAPIKey, fetchAPIKeys, fetchLifecycleStatus, revokeAPIKey } from '../api'
import type { APIKeyCapability, APIKeySummary, LifecycleStatus } from '../api'
import { CheckIcon, CopyIcon, KeyIcon, TrashIcon } from '../icons'
import { StatusWord, LoadingBlock, StatusChip } from '../components/kit'
import { usePhebsTokens, FONTS } from '../theme'
import { isAbortError, relTime } from '../util'

export default function SettingsPage({ isAdmin = false }: { isAdmin?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [keys, setKeys] = useState<APIKeySummary[]>([])
  const [keysLoaded, setKeysLoaded] = useState(false)
  const [name, setName] = useState('')
  const [investigationWrite, setInvestigationWrite] = useState(false)
  const [createdToken, setCreatedToken] = useState('')
  const [createdCapabilities, setCreatedCapabilities] = useState<APIKeyCapability[]>([])
  const [copied, setCopied] = useState(false)
  const [pendingRevoke, setPendingRevoke] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [lifecycleStatus, setLifecycleStatus] = useState<LifecycleStatus | null>(null)
  const [lifecycleError, setLifecycleError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    fetchAPIKeys(controller.signal)
      .then(({ keys: rows }) => {
        setKeys(rows)
        setKeysLoaded(true)
      })
      .catch((cause) => {
        if (!isAbortError(cause)) setError(String(cause))
      })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (!isAdmin) return
    const controller = new AbortController()
    fetchLifecycleStatus(controller.signal)
      .then(setLifecycleStatus)
      .catch((cause) => {
        if (!isAbortError(cause)) setLifecycleError(String(cause))
      })
    return () => controller.abort()
  }, [isAdmin])

  const create = async (event: React.FormEvent) => {
    event.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) return
    setBusy(true)
    setError('')
    try {
      const capabilities: APIKeyCapability[] = investigationWrite
        ? ['investigation:write']
        : []
      const result = await createAPIKey(trimmed, capabilities)
      setKeys((current) => [result.key, ...current])
      setCreatedToken(result.token)
      setCreatedCapabilities(result.key.capabilities)
      setName('')
      setInvestigationWrite(false)
      setCopied(false)
    } catch (cause) {
      setError(String(cause))
    } finally {
      setBusy(false)
    }
  }

  const revoke = async (id: string) => {
    setBusy(true)
    setError('')
    try {
      await revokeAPIKey(id)
      setKeys((current) => current.filter((key) => key.id !== id))
      setPendingRevoke('')
    } catch (cause) {
      setError(String(cause))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={css({ maxWidth: '880px', margin: '0 auto' })}>
      {isAdmin && (
        <section aria-labelledby="lifecycle-heading" className={css({ marginBottom: '32px' })}>
          <div className={css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '12px', marginBottom: '12px' })}>
            <div>
              <h1 id="lifecycle-heading" className={css({ margin: 0, fontSize: '20px', lineHeight: '28px', fontWeight: 600, color: tok.textPrimary })}>
                Lifecycle maintenance
              </h1>
              <div className={css({ marginTop: '4px', fontSize: '12px', lineHeight: '18px', color: tok.textTertiary })}>
                Bounded owner turns and allocated-disk pressure. This view never lists source paths or retained content.
              </div>
            </div>
            {lifecycleStatus && <LifecycleBadge status={lifecycleStatus} />}
          </div>
          {lifecycleError ? (
            <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
              Lifecycle status unavailable: {lifecycleError}
            </Notification>
          ) : !lifecycleStatus ? (
            <div role="status" aria-label="Loading lifecycle status" className={css({ minHeight: '96px', display: 'grid', placeItems: 'center', border: `1px solid ${tok.cardBorder}`, borderRadius: '8px' })}>
              <Spinner $size="small" />
            </div>
          ) : (
            <LifecyclePanel status={lifecycleStatus} />
          )}
        </section>
      )}

      <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '24px' })}>
        <KeyIcon size={20} />
        <h1 className={css({ margin: 0, fontSize: '20px', lineHeight: '28px', fontWeight: 600, color: tok.textPrimary })}>
          API keys
        </h1>
      </div>

      {error && (
        <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
          {error}
        </Notification>
      )}

      {createdToken && (
        <div className={css({ border: `1px solid ${tok.status.current.solid}`, borderRadius: '8px', padding: '14px', marginBottom: '20px' })}>
          <div className={css({ fontSize: '12px', fontWeight: 600, color: tok.textSecondary, marginBottom: '8px' })}>
            New key
          </div>
          <div className={css({ fontSize: '12px', color: tok.textTertiary, marginBottom: '8px' })}>
            {createdCapabilities.length > 0
              ? 'Capability: investigation:write. This key can attempt durable Investigation mutations within your existing authority.'
              : 'Read-only key. It cannot bind or perform Investigation mutations.'}
          </div>
          <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
            <code className={css({ flex: 1, minWidth: 0, fontFamily: FONTS.MONO, fontSize: '12px', overflowWrap: 'anywhere', color: tok.textPrimary })}>
              {createdToken}
            </code>
            <button
              type="button"
              title="Copy API key"
              aria-label="Copy API key"
              onClick={() => {
                void navigator.clipboard?.writeText(createdToken)
                setCopied(true)
              }}
              className={css(iconButton(tok))}
            >
              {copied ? <CheckIcon /> : <CopyIcon />}
            </button>
          </div>
        </div>
      )}

      <form onSubmit={create} className={css({ marginBottom: '28px' })}>
        <div className={css({ display: 'flex', gap: '8px', '@media screen and (max-width: 560px)': { flexDirection: 'column' } })}>
          <div className={css({ flex: 1 })}>
            <Input value={name} onChange={(event) => setName(event.currentTarget.value)} placeholder="Key name" aria-label="Key name" />
          </div>
          <Button type="submit" isLoading={busy}>Create key</Button>
        </div>
        <label className={css({
          display: 'flex',
          alignItems: 'flex-start',
          gap: '10px',
          marginTop: '14px',
          padding: '12px',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '8px',
          color: tok.textSecondary,
          cursor: 'pointer',
        })}>
          <input
            type="checkbox"
            checked={investigationWrite}
            onChange={(event) => setInvestigationWrite(event.currentTarget.checked)}
            className={css({ marginTop: '3px' })}
          />
          <span>
            <span className={css({ display: 'block', color: tok.textPrimary, fontSize: '13px', fontWeight: 600 })}>
              Allow Investigation writes
            </span>
            <span className={css({ display: 'block', marginTop: '3px', fontSize: '12px', lineHeight: '18px' })}>
              Adds the immutable <code className={css({ fontFamily: FONTS.MONO })}>investigation:write</code> capability.
              It does not expand repository access or ownership. Replace the key to change this authority.
            </span>
          </span>
        </label>
      </form>

      <div className={css({ borderTop: `1px solid ${tok.cardBorder}` })}>
        {!keysLoaded && !error && (
          <LoadingBlock label="Loading API keys…" />
        )}
        {keysLoaded && keys.length === 0 && (
          <div className={css({ padding: '24px 0', color: tok.textTertiary, fontSize: '14px' })}>No API keys.</div>
        )}
        {keys.map((key) => (
          <div key={key.id} className={css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', gap: '16px', alignItems: 'center', minHeight: '68px', borderBottom: `1px solid ${tok.cardBorder}` })}>
            <div className={css({ minWidth: 0 })}>
              <div className={css({ color: tok.textPrimary, fontSize: '14px', fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{key.name}</div>
              <div className={css({ color: tok.textTertiary, fontSize: '12px', marginTop: '4px' })}>
                <span className={css({ fontFamily: FONTS.MONO })}>{key.prefix}</span>
                {' · '}created {formatDate(key.created_at)}
                {key.last_used_at ? ` · used ${formatDate(key.last_used_at)}` : ''}
                {' · '}
                {key.capabilities.length > 0
                  ? key.capabilities.join(', ')
                  : 'read only'}
              </div>
            </div>
            {pendingRevoke === key.id ? (
              <div className={css({ display: 'flex', gap: '6px' })}>
                <Button size={SIZE.compact} kind={BUTTON_KIND.tertiary} onClick={() => setPendingRevoke('')}>Cancel</Button>
                <Button size={SIZE.compact} kind={BUTTON_KIND.secondary} isLoading={busy} onClick={() => void revoke(key.id)}>Revoke</Button>
              </div>
            ) : (
              <button
                type="button"
                title={`Revoke ${key.name}`}
                aria-label={`Revoke ${key.name}`}
                onClick={() => setPendingRevoke(key.id)}
                className={css(iconButton(tok))}
              >
                <TrashIcon />
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function LifecycleBadge({ status }: { status: LifecycleStatus }) {
  const pressure = status.capacity.pressure
  const tone = !status.policy.enabled ? 'neutral'
    : pressure === 'normal' ? 'green'
      : pressure === 'collect' ? 'amber'
        : pressure === 'refuse' ? 'red'
          : 'blue'
  const label = !status.policy.enabled ? 'Collection disabled'
    : pressure === 'normal' ? 'Normal'
      : pressure === 'collect' ? 'Collecting'
        : pressure === 'refuse' ? 'Admission refused'
          : 'Capacity unavailable'
  return <StatusChip tone={tone} role="status">{label}</StatusChip>
}

// T43.10: lifecycle, capacity, and owner state as cards, rendering only the
// existing source-free status fields.
function LifecyclePanel({ status }: { status: LifecycleStatus }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const completed = status.owners.filter((owner) => owner.state !== 'not_run').length
  const failed = status.owners.filter((owner) => owner.state === 'error')
  const backlog = status.owners.filter((owner) => owner.backlog)
  const pressure = status.capacity.pressure
  const pressureTone = !status.policy.enabled ? 'neutral'
    : pressure === 'normal' ? 'green'
      : pressure === 'collect' ? 'amber'
        : pressure === 'refuse' ? 'red'
          : 'blue'
  const disk = status.capacity.completeness === 'exact' && status.capacity.used_percent !== undefined
    ? `${status.capacity.used_percent}% allocated (${formatBytes(status.capacity.used_bytes ?? 0)} of ${formatBytes(status.capacity.total_bytes ?? 0)})`
    : 'Capacity unavailable'
  const observed = status.capacity.observed_at && Number.isFinite(Date.parse(status.capacity.observed_at))
    ? status.capacity.observed_at
    : ''
  return (
    <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: '10px', '@media screen and (max-width: 760px)': { gridTemplateColumns: '1fr' } })}>
      <LifecycleCard title="Pressure">
        <div data-volatile="lifecycle"><StatusWord tone={pressureTone}>{pressureLabel(status)}</StatusWord></div>
        <div data-volatile="lifecycle" className={css({ marginTop: '6px', color: tok.textPrimary, fontSize: '12px', lineHeight: '17px', overflowWrap: 'anywhere' })}>{disk}</div>
        {observed && (
          <div data-volatile="lifecycle" className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '11px' })}>
            observed <time dateTime={observed}>{relTime(observed)} · {new Date(observed).toLocaleString()}</time>
          </div>
        )}
        <div className={css({ marginTop: '7px', color: tok.textTertiary, fontSize: '11px', lineHeight: '16px' })}>
          Accelerates at {status.policy.soft_watermark_percent}% · refuses new derived work at {status.policy.hard_watermark_percent}% · resumes below {status.policy.resume_watermark_percent}%
        </div>
      </LifecycleCard>
      <LifecycleCard title="Owner progress">
        <div data-volatile="lifecycle" className={css({ color: tok.textPrimary, fontSize: '18px', lineHeight: '24px', fontWeight: 600, fontVariantNumeric: 'tabular-nums' })}>
          {completed} / {status.policy.owners}
        </div>
        {/* StatusMonitor retains each owner's most recent result across
            rotations; the payload carries no cycle authority, so this count
            must not claim one. */}
        <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '11px' })}>owners with recorded results</div>
        {failed.length > 0 ? (
          <div data-volatile="lifecycle" className={css({ marginTop: '7px', display: 'grid', gap: '3px' })}>
            {failed.slice(0, 4).map((owner) => (
              <div key={owner.name} className={css({ display: 'flex', gap: '7px', alignItems: 'baseline', minWidth: 0 })}>
                <StatusWord tone="red">error</StatusWord>
                <code className={css({ fontFamily: FONTS.MONO, fontSize: '11px', overflowWrap: 'anywhere', minWidth: 0 })}>{owner.name}</code>
              </div>
            ))}
            {failed.length > 4 && <div className={css({ color: tok.textTertiary, fontSize: '11px' })}>and {failed.length - 4} more failed owners</div>}
          </div>
        ) : (
          <div data-volatile="lifecycle" className={css({ marginTop: '7px' })}><StatusWord tone="green">no failed owners</StatusWord></div>
        )}
        <div className={css({ marginTop: '7px', color: tok.textTertiary, fontSize: '11px', lineHeight: '16px' })}>
          Per turn: at most {status.policy.max_candidates_per_turn} candidates, {status.policy.max_deletes_per_turn} deletions, {status.policy.max_queries_per_turn} store queries
        </div>
      </LifecycleCard>
      <LifecycleCard title="Backlog">
        <div data-volatile="lifecycle" className={css({ color: tok.textPrimary, fontSize: '18px', lineHeight: '24px', fontWeight: 600, fontVariantNumeric: 'tabular-nums' })}>
          {backlog.length}
        </div>
        <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '11px' })}>
          {backlog.length === 1 ? 'owner carries' : 'owners carry'} backlog into the next turn
        </div>
        {backlog.length > 0 && (
          <div data-volatile="lifecycle" className={css({ marginTop: '7px', display: 'grid', gap: '3px' })}>
            {backlog.slice(0, 4).map((owner) => (
              <div key={owner.name} className={css({ display: 'flex', gap: '7px', alignItems: 'baseline', minWidth: 0 })}>
                <code className={css({ fontFamily: FONTS.MONO, fontSize: '11px', overflowWrap: 'anywhere', minWidth: 0 })}>{owner.name}</code>
                {owner.attempted_at && Number.isFinite(Date.parse(owner.attempted_at)) && (
                  <span className={css({ color: tok.textTertiary, fontSize: '10.5px' })}>
                    attempted <time dateTime={owner.attempted_at}>{relTime(owner.attempted_at)} · {new Date(owner.attempted_at).toLocaleString()}</time>
                  </span>
                )}
              </div>
            ))}
            {backlog.length > 4 && <div className={css({ color: tok.textTertiary, fontSize: '11px' })}>and {backlog.length - 4} more</div>}
          </div>
        )}
      </LifecycleCard>
    </div>
  )
}

function pressureLabel(status: LifecycleStatus): string {
  if (!status.policy.enabled) return 'collection disabled'
  switch (status.capacity.pressure) {
    case 'normal': return 'normal'
    case 'collect': return 'collecting'
    case 'refuse': return 'admission refused'
    default: return 'capacity unavailable'
  }
}

function LifecycleCard({ title, children }: { title: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-label={title} className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', padding: '12px 14px', minWidth: 0 })}>
      <h2 className={css({ margin: '0 0 8px', fontSize: '10px', lineHeight: '14px', fontWeight: 600, letterSpacing: '0.06em', textTransform: 'uppercase', color: tok.textTertiary })}>{title}</h2>
      {children}
    </section>
  )
}


function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024
    index++
  }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

function formatDate(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleDateString()
}

function iconButton(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    width: '32px',
    height: '32px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    border: `1px solid ${tok.cardBorder}`,
    borderRadius: '8px',
    backgroundColor: 'transparent',
    color: tok.textSecondary,
    cursor: 'pointer',
    ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
  }
}
