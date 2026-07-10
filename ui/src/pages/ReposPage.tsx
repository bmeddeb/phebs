import { useCallback, useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { HeadingSmall } from 'baseui/typography'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchRepoStatus, postReindex } from '../api'
import type { RepoStatus } from '../api'
import { usePhebsTokens, FONTS } from '../theme'
import { navigate } from '../router'
import { SearchIcon, CopyIcon, CheckIcon } from '../icons'
import { relTime } from '../util'

// T5.4/T5.5: repo table over /api/repo-status, polled so job-state
// transitions and the reindex buttons' effects show up within one cycle.
const POLL_MS = 3000

export default function ReposPage() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [repos, setRepos] = useState<RepoStatus[] | null>(null)
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    fetchRepoStatus()
      .then((r) => {
        setRepos(r)
        setError('')
      })
      .catch((e) => setError(String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, POLL_MS)
    return () => clearInterval(id)
  }, [refresh])

  const reindex = (name: string) => {
    postReindex(name, true).then(refresh).catch((e) => setError(String(e)))
  }

  if (error)
    return (
      <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
        {error}
      </Notification>
    )
  if (repos === null) return <Spinner $size="small" />

  const indexed = repos.filter((r) => r.indexed_commit_hash).length
  const running = repos.filter((r) => r.last_index_job && !['done', 'failed'].includes(r.last_index_job.status)).length

  return (
    <div>
      <div className={css({ display: 'flex', alignItems: 'flex-end', gap: '16px', marginBottom: '20px' })}>
        <div>
          <HeadingSmall margin="0 0 4px 0">Repositories</HeadingSmall>
          <div className={css({ fontSize: '13px', color: tok.textTertiary })}>
            {repos.length} {repos.length === 1 ? 'repository' : 'repositories'} · {indexed} indexed
            {running > 0 ? ` · ${running} indexing` : ''}
          </div>
        </div>
        <div className={css({ flex: 1 })} />
        <button
          onClick={() => repos.forEach((r) => reindex(r.name))}
          className={css({ fontSize: '13px', color: tok.textSecondary, backgroundColor: tok.fill, border: 'none', borderRadius: '8px', padding: '8px 14px', cursor: 'pointer', ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary } })}
        >
          Reindex all
        </button>
      </div>

      {repos.length === 0 ? (
        <div className={css({ color: tok.textTertiary, padding: '32px 0' })}>
          No repos yet — add a connection to the config and restart.
        </div>
      ) : (
        <div className={css({ overflowX: 'auto' })}>
          <table className={css({ width: '100%', borderCollapse: 'collapse', fontSize: '14px' })}>
            <thead>
              <tr className={css({ borderBottom: `1px solid ${tok.cardBorder}` })}>
                {['Repository', 'Connection', 'Status', 'Last indexed', 'Commit', ''].map((h, i) => (
                  <th
                    key={i}
                    className={css({ textAlign: 'left', fontSize: '13px', fontWeight: 500, color: tok.textTertiary, padding: '0 12px 10px 0', whiteSpace: 'nowrap' })}
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {repos.map((r) => (
                <Row key={r.name} repo={r} onReindex={() => reindex(r.name)} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Row({ repo, onReindex }: { repo: RepoStatus; onReindex: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const short = repo.name.slice(repo.name.lastIndexOf('/') + 1)
  const job = repo.last_index_job
  const running = !!job && !['done', 'failed'].includes(job.status)
  const cell = css({ padding: '12px 12px 12px 0', verticalAlign: 'top' })
  return (
    <tr className={css({ borderBottom: `1px solid ${tok.innerSep}`, ':hover': { backgroundColor: tok.hoverFill } })}>
      <td className={cell}>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
          <span className={css({ fontWeight: 500, color: tok.textPrimary })}>{repo.name}</span>
          {repo.orphaned && <Pill text="orphaned" bg="#FFF0E9" fg="#A33B04" />}
        </div>
      </td>
      <td className={cell}>
        <div className={css({ display: 'flex', gap: '4px', flexWrap: 'wrap' })}>
          {(repo.connections ?? []).map((c) => (
            <Pill key={c} text={c} bg={tok.fill} fg={tok.textSecondary} />
          ))}
          {(repo.connections ?? []).length === 0 && <span className={css({ color: tok.textTertiary })}>—</span>}
        </div>
      </td>
      <td className={cell}>
        <Status job={job} />
      </td>
      <td className={cell}>
        {repo.indexed_at ? (
          <div>
            <div className={css({ color: tok.textSecondary })}>{relTime(repo.indexed_at)}</div>
            <div className={css({ fontSize: '12px', color: tok.textTertiary })}>{new Date(repo.indexed_at).toLocaleString()}</div>
          </div>
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
            title="Search in this repo"
            onClick={() => navigate('/search', { q: `repo:${short} ` })}
            className={css(iconBtn(tok))}
          >
            <SearchIcon size={14} />
          </button>
          <button
            disabled={running}
            onClick={onReindex}
            className={css({
              fontSize: '13px',
              color: running ? tok.textTertiary : tok.textSecondary,
              backgroundColor: tok.fill,
              border: 'none',
              borderRadius: '8px',
              padding: '6px 12px',
              cursor: running ? 'default' : 'pointer',
              opacity: running ? 0.6 : 1,
              ':hover': running ? {} : { backgroundColor: tok.hoverFill, color: tok.textPrimary },
            })}
          >
            {job?.status === 'failed' ? 'Retry' : 'Reindex'}
          </button>
        </div>
      </td>
    </tr>
  )
}

function Status({ job }: { job: RepoStatus['last_index_job'] }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (!job) return <span className={css({ display: 'flex', alignItems: 'center', gap: '6px', color: tok.textTertiary })}><Dot color={tok.gutter} /> never indexed</span>
  const map: Record<string, { color: string; label: string }> = {
    done: { color: tok.statusGreen, label: 'Indexed' },
    failed: { color: tok.statusRed, label: 'Failed' },
  }
  const running = !['done', 'failed'].includes(job.status)
  const s = map[job.status] ?? { color: tok.statusBlue, label: 'Indexing…' }
  return (
    <div>
      <span className={css({ display: 'flex', alignItems: 'center', gap: '6px', color: tok.textSecondary })}>
        <Dot color={s.color} pulse={running} /> {s.label}
      </span>
      {job.status === 'failed' && job.error && (
        <div className={css({ fontFamily: FONTS.MONO, fontSize: '12px', color: tok.statusRed, marginTop: '2px', maxWidth: '280px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })} title={job.error}>
          {job.error}
        </div>
      )}
    </div>
  )
}

function Dot({ color, pulse }: { color: string; pulse?: boolean }) {
  const [css] = useStyletron()
  return (
    <span
      className={css({
        width: '8px',
        height: '8px',
        borderRadius: '50%',
        backgroundColor: color,
        flexShrink: 0,
        ...(pulse
          ? { animationName: { '0%,100%': { opacity: 1 }, '50%': { opacity: 0.35 } }, animationDuration: '1.4s', animationIterationCount: 'infinite' }
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
      title="Copy commit"
      onClick={() => {
        navigator.clipboard?.writeText(hash)
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
      className={css({ display: 'inline-flex', alignItems: 'center', gap: '6px', fontFamily: FONTS.MONO, fontSize: '12px', color: tok.textSecondary, backgroundColor: tok.hoverFill, border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', padding: '3px 8px', cursor: 'pointer', ':hover': { color: tok.textPrimary } })}
    >
      {hash.slice(0, 7)}
      <span className={css({ display: 'flex', color: done ? tok.statusGreen : tok.textTertiary })}>{done ? <CheckIcon size={13} /> : <CopyIcon size={13} />}</span>
    </button>
  )
}

function iconBtn(tok: ReturnType<typeof usePhebsTokens>) {
  return { display: 'flex', alignItems: 'center', justifyContent: 'center', width: '32px', height: '32px', color: tok.textTertiary, backgroundColor: 'transparent', border: 'none', borderRadius: '8px', cursor: 'pointer', ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary } }
}
