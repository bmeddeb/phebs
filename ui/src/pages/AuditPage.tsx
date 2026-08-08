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
        // New events arriving between pages shift offset paging; dedupe by id
        // so a shifted page never renders duplicate rows.
        // ponytail: rows pruned mid-scroll can still be skipped; move to a
        // created_at cursor if that ever matters.
        setEvents((prev) => {
          if (offset === 0) return page.events
          const seen = new Set(prev.map((e) => e.id))
          return [...prev, ...page.events.filter((e) => !seen.has(e.id))]
        })
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
          {/* Fixed layout: column geometry is declared, never derived from
              event content — one long target must not reflow the table
              anatomy (long values ellipsize in place, full value on title). */}
          <table className={css({ width: '100%', tableLayout: 'fixed', minWidth: '860px', borderCollapse: 'collapse', fontSize: '14px' })}>
            <thead>
              <tr className={css({ borderBottom: `1px solid ${tok.cardBorder}` })}>
                {([['When', '190px'], ['Action', '170px'], ['Actor', '230px'], ['Target', ''], ['Status', '76px'], ['Source IP', '128px']] as const).map(([h, w]) => (
                  <th key={h} style={w ? { width: w } : undefined} className={css({ textAlign: 'left', fontSize: '13px', fontWeight: 500, color: tok.textTertiary, padding: '0 12px 10px 0', whiteSpace: 'nowrap' })}>
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
        <div title={event.action} className={css({ fontWeight: 500, color: tok.textPrimary, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>{event.action}</div>
      </td>
      <td className={cell}>
        {event.actor_email ? (
          <div>
            <div title={event.actor_email} className={css({ color: tok.textSecondary, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>{event.actor_email}</div>
            {event.auth_method === 'api_key' && <div className={css({ fontSize: '12px', color: tok.textTertiary })}>via API key</div>}
          </div>
        ) : event.api_key_id ? (
          <div title={`API key ${event.api_key_id}`} className={css({ color: tok.textSecondary, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>API key {event.api_key_id}</div>
        ) : (
          <div className={css({ color: tok.textTertiary })}>—</div>
        )}
      </td>
      <td className={cell}>
        {/* Every cell's direct child is a block: the receipt mask covers
            `td > *`, and a shrink-wrapped span would size its mask box to
            the event's content — geometry must not depend on which event
            occupies the row. */}
        {event.target ? (
          <div title={event.target} className={css({ color: tok.textSecondary, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>{event.target}</div>
        ) : (
          <div className={css({ color: tok.textTertiary })}>—</div>
        )}
      </td>
      <td className={cell}>
        <div>
          <span
            className={css({
              display: 'inline-block',
              fontSize: '12px',
              fontWeight: 500,
              borderRadius: '999px',
              padding: '2px 10px',
              backgroundColor: tok.fill,
              color: ok ? tok.status.current.text : tok.status.conflict.text,
            })}
          >
            {event.status}
          </span>
        </div>
      </td>
      <td className={cell}>
        <div className={css({ color: tok.textTertiary, fontSize: '13px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>{event.source_ip || '—'}</div>
      </td>
    </tr>
  )
}
