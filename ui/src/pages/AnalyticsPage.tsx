// T10.2: admin-only usage dashboard over GET /api/analytics. All data is
// local — phebs never phones home. Marks are hand-rolled divs in the token
// palette (single-series magnitude → one accent hue, no legend needed).
import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchAnalytics, type AnalyticsSummary } from '../api'
import { usePhebsTokens } from '../theme'
import { ChartIcon } from '../icons'
import { isAbortError } from '../util'

const DAYS = 30

export default function AnalyticsPage({ isAdmin }: { isAdmin: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [summary, setSummary] = useState<AnalyticsSummary | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const generation = useRef(0)

  useEffect(() => {
    if (!isAdmin) return
    const gen = ++generation.current
    const controller = new AbortController()
    setLoading(true)
    fetchAnalytics(DAYS, controller.signal)
      .then((s) => {
        if (gen !== generation.current) return
        setSummary(s)
        setError('')
      })
      .catch((cause) => {
        if (gen !== generation.current || isAbortError(cause)) return
        setError(String(cause))
      })
      .finally(() => {
        if (gen === generation.current) setLoading(false)
      })
    return () => controller.abort()
  }, [isAdmin])

  if (!isAdmin) {
    return <div className={css({ color: tok.textTertiary, padding: '32px 0' })}>Analytics requires administrator access.</div>
  }

  const today = summary?.daily.at(-1)?.count ?? 0

  return (
    <div className={css({ maxWidth: '880px', margin: '0 auto' })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '4px', color: tok.textPrimary })}>
        <ChartIcon size={20} />
        <h1 className={css({ fontSize: '20px', fontWeight: 600, margin: 0 })}>Analytics</h1>
      </div>
      <div className={css({ fontSize: '13px', color: tok.textTertiary, marginBottom: '24px' })}>
        Local usage over the last {DAYS} days. Nothing leaves this machine.
      </div>

      {error && (
        <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
          {error}
        </Notification>
      )}
      {loading && <Spinner $size="small" />}

      {summary && !loading && (
        <>
          <div className={css({ display: 'flex', gap: '16px', flexWrap: 'wrap', marginBottom: '28px' })}>
            <Tile label={`Searches (${DAYS}d)`} value={String(summary.total_searches)} />
            <Tile label="Searches today" value={String(today)} />
            <Tile label="Avg duration" value={`${summary.avg_duration_ms} ms`} />
          </div>

          <SectionTitle text="Searches per day" />
          <DailyBars daily={summary.daily} />

          <SectionTitle text="Top repositories in results" />
          {summary.top_repos.length === 0 ? (
            <div className={css({ color: tok.textTertiary, padding: '12px 0' })}>No searches with matches yet.</div>
          ) : (
            <TopRepos repos={summary.top_repos} />
          )}
        </>
      )}
    </div>
  )
}

function SectionTitle({ text }: { text: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <h2 className={css({ fontSize: '14px', fontWeight: 600, color: tok.textPrimary, margin: '0 0 12px' })}>{text}</h2>
}

function Tile({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ flex: '1 1 160px', border: `1px solid ${tok.cardBorder}`, borderRadius: '12px', padding: '14px 16px' })}>
      <div className={css({ fontSize: '12px', color: tok.textTertiary, marginBottom: '4px' })}>{label}</div>
      <div className={css({ fontSize: '24px', fontWeight: 600, color: tok.textPrimary, fontVariantNumeric: 'tabular-nums' })}>{value}</div>
    </div>
  )
}

function DailyBars({ daily }: { daily: { date: string; count: number }[] }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const max = Math.max(1, ...daily.map((d) => d.count))
  return (
    <div className={css({ marginBottom: '28px' })}>
      <div className={css({ display: 'flex', alignItems: 'flex-end', gap: '2px', height: '96px', borderBottom: `1px solid ${tok.cardBorder}` })}>
        {daily.map((d) => (
          <div
            key={d.date}
            role="img"
            aria-label={`${d.date}: ${d.count} search${d.count === 1 ? '' : 'es'}`}
            title={`${d.date} — ${d.count} search${d.count === 1 ? '' : 'es'}`}
            data-testid={`bar-${d.date}`}
            className={css({
              flex: 1,
              minWidth: '4px',
              // A zero day renders a visible neutral tick: zero is a
              // measured value, not a gap (audit F36). Neutral text ink, not
              // innerSep — the hairline token is ~1.1:1 against the page and
              // fails the 3:1 graphical-object floor.
              height: d.count === 0 ? '2px' : `${Math.max(3, Math.round((d.count / max) * 96))}px`,
              backgroundColor: d.count === 0 ? tok.textTertiary : tok.accent,
              borderTopLeftRadius: '3px',
              borderTopRightRadius: '3px',
            })}
          />
        ))}
      </div>
      <div className={css({ display: 'flex', justifyContent: 'space-between', fontSize: '11px', color: tok.textTertiary, paddingTop: '6px' })}>
        <span>{daily[0]?.date}</span>
        <span>{daily.at(-1)?.date}</span>
      </div>
    </div>
  )
}

function TopRepos({ repos }: { repos: { name: string; count: number }[] }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const max = Math.max(1, ...repos.map((r) => r.count))
  return (
    <div className={css({ marginBottom: '28px' })}>
      {repos.map((r) => (
        <div key={r.name} className={css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', alignItems: 'center', gap: '12px', padding: '7px 0', borderBottom: `1px solid ${tok.innerSep}` })}>
          <div className={css({ minWidth: 0 })}>
            <div className={css({ fontSize: '14px', color: tok.textPrimary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{r.name}</div>
            <div
              className={css({
                marginTop: '4px',
                height: '4px',
                width: `${Math.max(2, Math.round((r.count / max) * 100))}%`,
                backgroundColor: tok.accent,
                borderRadius: '2px',
              })}
            />
          </div>
          <span className={css({ fontSize: '13px', color: tok.textSecondary, fontVariantNumeric: 'tabular-nums' })}>{r.count}</span>
        </div>
      ))}
    </div>
  )
}
