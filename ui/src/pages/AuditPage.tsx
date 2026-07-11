// T10.1: admin-only audit trail — newest-first table over GET /api/audit.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchAudit, type AuditEvent } from '../api'
import { usePhebsTokens } from '../theme'
import { AuditIcon } from '../icons'
import { isAbortError, relTime } from '../util'

const PAGE = 50

export default function AuditPage({ isAdmin }: { isAdmin: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const generation = useRef(0)

  const load = useCallback((offset: number) => {
    const gen = ++generation.current
    const controller = new AbortController()
    setLoading(true)
    fetchAudit(offset, PAGE, controller.signal)
      .then((page) => {
        if (gen !== generation.current) return
        setEvents((prev) => (offset === 0 ? page.events : [...prev, ...page.events]))
        setHasMore(page.has_more)
        setError('')
      })
      .catch((cause) => {
        if (gen !== generation.current || isAbortError(cause)) return
        setError(String(cause))
      })
      .finally(() => {
        if (gen === generation.current) setLoading(false)
      })
    return controller
  }, [])

  useEffect(() => {
    if (!isAdmin) return
    const controller = load(0)
    return () => controller.abort()
  }, [load, isAdmin])

  if (!isAdmin) {
    return <div className={css({ color: tok.textTertiary, padding: '32px 0' })}>The audit log requires administrator access.</div>
  }

  return (
    <div className={css({ maxWidth: '1040px', margin: '0 auto' })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '20px', color: tok.textPrimary })}>
        <AuditIcon size={20} />
        <h1 className={css({ fontSize: '20px', fontWeight: 600, margin: 0 })}>Audit log</h1>
      </div>

      {error && (
        <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
          {error}
        </Notification>
      )}

      {!loading && !error && events.length === 0 ? (
        <div className={css({ color: tok.textTertiary, padding: '32px 0' })}>No recorded actions yet.</div>
      ) : (
        <div className={css({ overflowX: 'auto' })}>
          <table className={css({ width: '100%', borderCollapse: 'collapse', fontSize: '14px' })}>
            <thead>
              <tr className={css({ borderBottom: `1px solid ${tok.cardBorder}` })}>
                {['When', 'Action', 'Actor', 'Target', 'Status', 'Source IP'].map((h, i) => (
                  <th key={i} className={css({ textAlign: 'left', fontSize: '13px', fontWeight: 500, color: tok.textTertiary, padding: '0 12px 10px 0', whiteSpace: 'nowrap' })}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <Row key={e.id} event={e} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className={css({ display: 'flex', alignItems: 'center', gap: '12px', padding: '16px 0' })}>
        {loading && <Spinner $size="small" />}
        {!loading && hasMore && (
          <button
            type="button"
            onClick={() => load(events.length)}
            className={css({ fontSize: '13px', color: tok.textSecondary, backgroundColor: tok.fill, border: 'none', borderRadius: '8px', padding: '8px 14px', cursor: 'pointer', ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary } })}
          >
            Load more
          </button>
        )}
      </div>
    </div>
  )
}

function Row({ event }: { event: AuditEvent }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const cell = css({ padding: '12px 12px 12px 0', verticalAlign: 'top' })
  const ok = event.status < 400
  return (
    <tr className={css({ borderBottom: `1px solid ${tok.innerSep}`, ':hover': { backgroundColor: tok.hoverFill } })}>
      <td className={cell}>
        <div className={css({ color: tok.textSecondary, whiteSpace: 'nowrap' })}>{relTime(event.created_at)}</div>
        <div className={css({ fontSize: '12px', color: tok.textTertiary, whiteSpace: 'nowrap' })}>{new Date(event.created_at).toLocaleString()}</div>
      </td>
      <td className={cell}>
        <span className={css({ fontWeight: 500, color: tok.textPrimary, whiteSpace: 'nowrap' })}>{event.action}</span>
      </td>
      <td className={cell}>
        {event.actor_email ? (
          <div>
            <div className={css({ color: tok.textSecondary })}>{event.actor_email}</div>
            {event.auth_method === 'api_key' && <div className={css({ fontSize: '12px', color: tok.textTertiary })}>via API key</div>}
          </div>
        ) : event.api_key_id ? (
          <span className={css({ color: tok.textSecondary })}>API key {event.api_key_id}</span>
        ) : (
          <span className={css({ color: tok.textTertiary })}>—</span>
        )}
      </td>
      <td className={cell}>
        {event.target ? (
          <span className={css({ color: tok.textSecondary, wordBreak: 'break-all' })}>{event.target}</span>
        ) : (
          <span className={css({ color: tok.textTertiary })}>—</span>
        )}
      </td>
      <td className={cell}>
        <span
          className={css({
            display: 'inline-block',
            fontSize: '12px',
            fontWeight: 500,
            borderRadius: '999px',
            padding: '2px 10px',
            backgroundColor: tok.fill,
            color: ok ? tok.statusGreen : tok.statusRed,
          })}
        >
          {event.status}
        </span>
      </td>
      <td className={cell}>
        <span className={css({ color: tok.textTertiary, fontSize: '13px', whiteSpace: 'nowrap' })}>{event.source_ip || '—'}</span>
      </td>
    </tr>
  )
}
