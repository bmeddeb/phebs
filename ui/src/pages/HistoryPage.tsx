import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND } from 'baseui/button'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchCommits } from '../api'
import type { GitCommit } from '../api'
import { href } from '../router'
import { CommitIcon } from '../icons'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError, repoFilter } from '../util'

export default function HistoryPage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const path = params.get('path') ?? ''
  const ref = params.get('ref') ?? ''
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const generation = useRef(0)
  const requestKey = `${repo}\u0000${ref}\u0000${path}`
  const latestRequestKey = useRef(requestKey)
  const loadMoreController = useRef<AbortController | null>(null)
  latestRequestKey.current = requestKey
  const [commits, setCommits] = useState<GitCommit[]>([])
  const [revision, setRevision] = useState(ref)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const current = ++generation.current
    const currentRequestKey = requestKey
    const controller = new AbortController()
    loadMoreController.current?.abort()
    loadMoreController.current = null
    setLoading(true)
    setLoadingMore(false)
    setError('')
    setCommits([])
    fetchCommits(repo, ref, path, 50, 0, controller.signal)
      .then((result) => {
        if (current !== generation.current || currentRequestKey !== latestRequestKey.current) return
        setCommits(result.commits)
        setRevision(result.revision)
        setHasMore(result.has_more)
      })
      .catch((cause) => {
        if (!isAbortError(cause) && current === generation.current && currentRequestKey === latestRequestKey.current) {
          setError(String(cause))
        }
      })
      .finally(() => {
        if (current === generation.current && currentRequestKey === latestRequestKey.current) setLoading(false)
      })
    return () => {
      controller.abort()
      loadMoreController.current?.abort()
    }
  }, [repo, ref, path, requestKey])

  const loadMore = async () => {
    const current = generation.current
    const currentRequestKey = requestKey
    const controller = new AbortController()
    loadMoreController.current?.abort()
    loadMoreController.current = controller
    setLoadingMore(true)
    setError('')
    try {
      const result = await fetchCommits(repo, revision, path, 50, commits.length, controller.signal)
      if (controller.signal.aborted || current !== generation.current || currentRequestKey !== latestRequestKey.current) return
      setCommits((current) => [...current, ...result.commits])
      setHasMore(result.has_more)
    } catch (cause) {
      if (!isAbortError(cause) && current === generation.current && currentRequestKey === latestRequestKey.current) {
        setError(String(cause))
      }
    } finally {
      if (loadMoreController.current === controller) {
        loadMoreController.current = null
        setLoadingMore(false)
      }
    }
  }

  return (
    <div className={css({ maxWidth: '1040px', margin: '0 auto' })}>
      <HistoryHeader repo={repo} path={path} ref={revision} />
      {error && (
        <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
          {error}
        </Notification>
      )}
      {loading ? <Spinner $size="small" /> : (
        <div className={css({ borderTop: `1px solid ${tok.cardBorder}` })}>
          {commits.length === 0 && <div className={css({ padding: '28px 0', color: tok.textTertiary })}>No commits.</div>}
          {commits.map((commit) => (
            <a
              key={commit.id}
              href={href('/commit', { repo, ref: commit.id, ...(path ? { path } : {}) })}
              className={css({
                display: 'grid',
                gridTemplateColumns: 'minmax(0, 1fr) 180px 100px',
                gap: '18px',
                alignItems: 'center',
                minHeight: '68px',
                borderBottom: `1px solid ${tok.cardBorder}`,
                color: 'inherit',
                textDecoration: 'none',
                contentVisibility: 'auto',
                ':hover': { backgroundColor: tok.hoverFill },
                '@media screen and (max-width: 720px)': { gridTemplateColumns: 'minmax(0, 1fr) auto', gap: '10px' },
              })}
            >
              <div className={css({ minWidth: 0, paddingLeft: '8px' })}>
                <div className={css({ fontSize: '14px', fontWeight: 500, color: tok.textPrimary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>
                  {commit.subject || '(no subject)'}
                </div>
                <div className={css({ fontSize: '12px', color: tok.textTertiary, marginTop: '4px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>
                  {commit.author.name} · {commit.author.email}
                </div>
              </div>
              <div className={css({ fontSize: '12px', color: tok.textTertiary, '@media screen and (max-width: 720px)': { display: 'none' } })}>
                {formatTime(commit.author.time)}
              </div>
              <code className={css({ fontFamily: FONTS.MONO, fontSize: '12px', color: tok.textSecondary, paddingRight: '8px' })}>
                {commit.short_id}
              </code>
            </a>
          ))}
        </div>
      )}
      {hasMore && (
        <div className={css({ marginTop: '18px' })}>
          <Button kind={BUTTON_KIND.secondary} isLoading={loadingMore} onClick={() => void loadMore()}>Load more</Button>
        </div>
      )}
    </div>
  )
}

function HistoryHeader({ repo, path, ref }: { repo: string; path: string; ref: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ marginBottom: '22px' })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
        <CommitIcon size={20} />
        <h1 className={css({ margin: 0, fontSize: '20px', lineHeight: '28px', fontWeight: 600, color: tok.textPrimary })}>History</h1>
      </div>
      <div className={css({ display: 'flex', gap: '5px', marginTop: '8px', minWidth: 0, fontSize: '13px', color: tok.textTertiary })}>
        <a href={href('/search', { q: repoFilter(repo) })} className={css({ color: tok.textSecondary, textDecoration: 'none' })}>{repo}</a>
        {path && <><span>/</span><a href={href('/file', { repo, path, ref })} className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textSecondary, textDecoration: 'none' })}>{path}</a></>}
      </div>
    </div>
  )
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString()
}
