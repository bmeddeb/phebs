import { useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { createAPIKey, fetchAPIKeys, revokeAPIKey } from '../api'
import type { APIKeySummary } from '../api'
import { CheckIcon, CopyIcon, KeyIcon, TrashIcon } from '../icons'
import { usePhebsTokens, FONTS } from '../theme'
import { isAbortError } from '../util'

export default function SettingsPage() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [keys, setKeys] = useState<APIKeySummary[]>([])
  const [name, setName] = useState('')
  const [createdToken, setCreatedToken] = useState('')
  const [copied, setCopied] = useState(false)
  const [pendingRevoke, setPendingRevoke] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    fetchAPIKeys(controller.signal)
      .then(({ keys: rows }) => setKeys(rows))
      .catch((cause) => {
        if (!isAbortError(cause)) setError(String(cause))
      })
    return () => controller.abort()
  }, [])

  const create = async (event: React.FormEvent) => {
    event.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) return
    setBusy(true)
    setError('')
    try {
      const result = await createAPIKey(trimmed)
      setKeys((current) => [result.key, ...current])
      setCreatedToken(result.token)
      setName('')
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
        <div className={css({ border: `1px solid ${tok.statusGreen}`, borderRadius: '8px', padding: '14px', marginBottom: '20px' })}>
          <div className={css({ fontSize: '12px', fontWeight: 600, color: tok.textSecondary, marginBottom: '8px' })}>
            New key
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

      <form onSubmit={create} className={css({ display: 'flex', gap: '8px', marginBottom: '28px', '@media screen and (max-width: 560px)': { flexDirection: 'column' } })}>
        <div className={css({ flex: 1 })}>
          <Input value={name} onChange={(event) => setName(event.currentTarget.value)} placeholder="Key name" aria-label="Key name" />
        </div>
        <Button type="submit" isLoading={busy}>Create key</Button>
      </form>

      <div className={css({ borderTop: `1px solid ${tok.cardBorder}` })}>
        {keys.length === 0 && (
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
