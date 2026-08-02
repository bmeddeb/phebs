import { useCallback, useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchRepoStatus, postReindex } from '../api'
import type { RepoStatus } from '../api'
import { usePhebsTokens, FONTS } from '../theme'
import { navigate } from '../router'
import { SearchIcon, CopyIcon, CheckIcon } from '../icons'
import { isAbortError, relTime, repoFilter } from '../util'

// T5.4/T5.5: repo table over /api/repo-status, polled so job-state
// transitions and the reindex buttons' effects show up within one cycle.
const POLL_MS = 3000

export default function ReposPage({ isAdmin = false }: { isAdmin?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [repos, setRepos] = useState<RepoStatus[] | null>(null)
  const [error, setError] = useState('')
  const [reindexingAll, setReindexingAll] = useState(false)
  const refreshGeneration = useRef(0)
  const refreshController = useRef<AbortController | null>(null)

  const refresh = useCallback(async () => {
    const generation = ++refreshGeneration.current
    refreshController.current?.abort()
    const controller = new AbortController()
    refreshController.current = controller
    try {
      const rows = await fetchRepoStatus(controller.signal)
      if (generation !== refreshGeneration.current) return
      setRepos(rows)
      setError('')
    } catch (caught) {
      if (!isAbortError(caught) && generation === refreshGeneration.current) {
        setError(String(caught))
      }
    }
  }, [])

  useEffect(() => {
    void refresh()
    const id = setInterval(() => void refresh(), POLL_MS)
    return () => {
      clearInterval(id)
      refreshController.current?.abort()
    }
  }, [refresh])

  const reindex = async (name: string) => {
    try {
      await postReindex(name, true)
      await refresh()
    } catch (caught) {
      setError(String(caught))
    }
  }

  const reindexAll = async () => {
    setReindexingAll(true)
    try {
      const outcomes = await Promise.allSettled(
        (repos ?? []).map((repo) => postReindex(repo.name, true)),
      )
      await refresh()
      const failed = outcomes.find((outcome) => outcome.status === 'rejected')
      if (failed?.status === 'rejected') throw failed.reason
    } catch (caught) {
      setError(String(caught))
    } finally {
      setReindexingAll(false)
    }
  }

  if (error)
    return (
      <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
        {error}
      </Notification>
    )
  if (repos === null) return <Spinner $size="small" />

  const indexed = repos.filter((r) => r.indexed_commit_hash).length
  const running = repos.filter((r) => r.last_index_job && !['done', 'failed', 'canceled'].includes(r.last_index_job.status)).length
  const indexingStateUnavailable = repos.some((r) => r.last_index_job_state === 'unavailable')

  return (
    <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflow: 'hidden' })}>
      <div className={css({ height: '38px', display: 'flex', alignItems: 'center', gap: '10px', padding: '0 12px', backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.cardBorder}` })}>
        <span className={css({ fontSize: '14px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary })}>
          Repositories
        </span>
        <span className={css({ fontSize: '12px', lineHeight: '16px', color: tok.textTertiary, '@media screen and (max-width: 520px)': { display: 'none' } })}>
          {repos.length} · {indexed} indexed · {indexingStateUnavailable ? 'indexing status unavailable' : `${running} indexing`}
        </span>
        <span className={css({ flex: 1 })} />
        {isAdmin && (
          <button
            type="button"
            disabled={reindexingAll}
            onClick={() => void reindexAll()}
            className={css({
              height: '26px',
              display: 'inline-flex',
              alignItems: 'center',
              fontSize: '12.5px',
              lineHeight: '16px',
              color: tok.textSecondary,
              backgroundColor: tok.fill,
              border: 'none',
              borderRadius: '7px',
              padding: '0 10px',
              cursor: reindexingAll ? 'default' : 'pointer',
              opacity: reindexingAll ? 0.6 : 1,
              ':hover': reindexingAll ? {} : { backgroundColor: tok.hoverFill, color: tok.textPrimary },
            })}
          >
            {reindexingAll ? 'Queuing…' : 'Reindex all'}
          </button>
        )}
      </div>

      {repos.length === 0 ? (
        <div className={css({ color: tok.textTertiary, padding: '28px 12px', fontSize: '13px' })}>
          No repos yet — add a connection to the config and restart.
        </div>
      ) : (
        <div className={css({ overflowX: 'auto' })}>
          <table className={css({ width: '100%', minWidth: '860px', borderCollapse: 'collapse', fontSize: '13px' })}>
            <thead>
              <tr className={css({ borderBottom: `1px solid ${tok.innerSep}` })}>
                {['Repository', 'Connection', 'Status', 'Last indexed', 'Commit', ''].map((h, i) => (
                  <th
                    key={i}
                    className={css({ textAlign: i === 5 ? 'right' : 'left', fontSize: '11.5px', lineHeight: '14px', fontWeight: 500, color: tok.textTertiary, padding: '8px 12px', whiteSpace: 'nowrap' })}
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {repos.map((r) => (
                <Row key={r.name} repo={r} canReindex={isAdmin} onReindex={() => void reindex(r.name)} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Row({ repo, canReindex, onReindex }: { repo: RepoStatus; canReindex: boolean; onReindex: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const job = repo.last_index_job
  const running = !!job && !['done', 'failed', 'canceled'].includes(job.status)
  const reindexDisabled = running || repo.last_index_job_state === 'unavailable'
  const cell = css({ height: '40px', padding: '0 12px', verticalAlign: 'middle', whiteSpace: 'nowrap' })
  return (
    <tr className={css({ height: '40px', borderBottom: `1px solid ${tok.innerSep}`, ':last-child': { borderBottom: 'none' }, ':hover': { backgroundColor: tok.hoverFill } })}>
      <td className={cell}>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
          <span className={css({ fontWeight: 500, color: tok.textPrimary })}>{repo.name}</span>
          {repo.orphaned && <Pill text="orphaned" bg={tok.deletedLineBg} fg={tok.statusRed} />}
          {(repo.indexed_revisions?.length ?? 0) > 1 && (
            <Pill text={`${repo.indexed_revisions?.length} revs`} bg={tok.fill} fg={tok.textSecondary} />
          )}
        </div>
      </td>
      <td className={cell}>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '4px', flexWrap: 'nowrap' })}>
          {(repo.connections ?? []).map((c) => (
            <Pill key={c} text={c} bg={tok.fill} fg={tok.textSecondary} />
          ))}
          {(repo.connections ?? []).length === 0 && <span className={css({ color: tok.textTertiary })}>—</span>}
        </div>
      </td>
      <td className={cell}>
        <Status job={job} state={repo.last_index_job_state} />
      </td>
      <td className={cell}>
        {repo.indexed_at ? (
          <span className={css({ color: tok.textSecondary })} title={new Date(repo.indexed_at).toLocaleString()}>
            {relTime(repo.indexed_at)}
          </span>
        ) : (
          <span className={css({ color: tok.textTertiary })}>—</span>
        )}
      </td>
      <td className={cell}>
        {repo.indexed_commit_hash ? <CommitChip hash={repo.indexed_commit_hash} /> : <span className={css({ color: tok.textTertiary })}>—</span>}
      </td>
      <td className={cell}>
        <div className={css({ display: 'flex', gap: '4px', justifyContent: 'flex-end' })}>
          <button
            type="button"
            title="Search in this repo"
            onClick={() => navigate('/search', { q: `${repoFilter(repo.name)} ` })}
            className={css(iconBtn(tok))}
          >
            <SearchIcon size={13} />
          </button>
          {canReindex && (
            <button
              type="button"
              disabled={reindexDisabled}
              title={repo.last_index_job_state === 'unavailable' ? 'Index job status unavailable' : undefined}
              onClick={onReindex}
              className={css({
                height: '26px',
                display: 'inline-flex',
                alignItems: 'center',
                fontSize: '12.5px',
                lineHeight: '16px',
                color: reindexDisabled ? tok.textTertiary : tok.textSecondary,
                backgroundColor: tok.fill,
                border: 'none',
                borderRadius: '7px',
                padding: '0 10px',
                cursor: reindexDisabled ? 'default' : 'pointer',
                opacity: reindexDisabled ? 0.6 : 1,
                ':hover': reindexDisabled ? {} : { backgroundColor: tok.hoverFill, color: tok.textPrimary },
              })}
            >
              {job?.status === 'failed' ? 'Retry' : 'Reindex'}
            </button>
          )}
        </div>
      </td>
    </tr>
  )
}

function Status({ job, state }: { job: RepoStatus['last_index_job']; state: RepoStatus['last_index_job_state'] }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (!job && state === 'unavailable') {
    return <span className={css({ display: 'flex', alignItems: 'center', gap: '6px', color: tok.textTertiary })}><Dot color={tok.gutter} /> status unavailable</span>
  }
  if (!job) return <span className={css({ display: 'flex', alignItems: 'center', gap: '6px', color: tok.textTertiary })}><Dot color={tok.gutter} /> never indexed</span>
  const map: Record<string, { color: string; label: string }> = {
    done: { color: tok.statusGreen, label: 'Indexed' },
    failed: { color: tok.statusRed, label: 'Failed' },
    canceled: { color: tok.gutter, label: 'Canceled' },
  }
  const running = !['done', 'failed', 'canceled'].includes(job.status)
  const s = map[job.status] ?? { color: tok.statusBlue, label: 'Indexing…' }
  return (
    <span className={css({ display: 'flex', alignItems: 'center', gap: '6px', minWidth: 0, color: tok.textSecondary })}>
      <Dot color={s.color} pulse={running} />
      <span className={css({ flexShrink: 0 })}>{s.label}</span>
      {job.status === 'failed' && job.error && (
        <span className={css({ minWidth: 0, maxWidth: '220px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: FONTS.MONO, fontSize: '11px', color: tok.statusRed })} title={job.error}>
          {job.error}
        </span>
      )}
    </span>
  )
}

function Dot({ color, pulse }: { color: string; pulse?: boolean }) {
  const [css] = useStyletron()
  return (
    <span
      className={css({
        width: '7px',
        height: '7px',
        borderRadius: '50%',
        backgroundColor: color,
        flexShrink: 0,
        ...(pulse
          ? {
              animationName: { '0%,100%': { opacity: 1 }, '50%': { opacity: 0.35 } },
              animationDuration: '1.4s',
              animationIterationCount: 'infinite',
              '@media (prefers-reduced-motion: reduce)': { animationName: 'none' },
            }
          : {}),
      })}
    />
  )
}

function Pill({ text, bg, fg }: { text: string; bg: string; fg: string }) {
  const [css] = useStyletron()
  return (
    <span className={css({ fontSize: '12px', backgroundColor: bg, color: fg, borderRadius: '999px', padding: '2px 8px', whiteSpace: 'nowrap' })}>{text}</span>
  )
}

function CommitChip({ hash }: { hash: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      title="Copy commit"
      onClick={() => {
        navigator.clipboard?.writeText(hash)
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
      className={css({ display: 'inline-flex', alignItems: 'center', gap: '5px', fontFamily: FONTS.MONO, fontSize: '11.5px', lineHeight: '15px', color: tok.textSecondary, backgroundColor: tok.hoverFill, border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', padding: '2px 7px', cursor: 'pointer', ':hover': { color: tok.textPrimary } })}
    >
      {hash.slice(0, 7)}
      <span className={css({ display: 'flex', color: done ? tok.statusGreen : tok.textTertiary })}>{done ? <CheckIcon size={12} /> : <CopyIcon size={12} />}</span>
    </button>
  )
}

function iconBtn(tok: ReturnType<typeof usePhebsTokens>) {
  return { display: 'flex', alignItems: 'center', justifyContent: 'center', width: '28px', height: '28px', color: tok.textTertiary, backgroundColor: 'transparent', border: 'none', borderRadius: '7px', cursor: 'pointer', ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary } }
}
