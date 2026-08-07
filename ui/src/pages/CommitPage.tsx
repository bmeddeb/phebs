import { useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchCommit, fetchDiff } from '../api'
import type { CommitResult, DiffResult, GitFileChange } from '../api'
import { href } from '../router'
import { CommitIcon, WarningIcon } from '../icons'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'

export default function CommitPage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const ref = params.get('ref') ?? ''
  const path = params.get('path') ?? ''
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [commit, setCommit] = useState<CommitResult | null>(null)
  const [diff, setDiff] = useState<DiffResult | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setCommit(null)
    setDiff(null)
    setError('')
    Promise.all([
      fetchCommit(repo, ref, controller.signal),
      fetchDiff(repo, ref, '', path, controller.signal),
    ]).then(([commitResult, diffResult]) => {
      setCommit(commitResult)
      setDiff(diffResult)
    }).catch((cause) => {
      if (!isAbortError(cause)) setError(String(cause))
    })
    return () => controller.abort()
  }, [repo, ref, path])

  return (
    <div className={css({ maxWidth: '1200px', margin: '0 auto' })}>
      {error && <Notification kind={NOTIFICATION_KIND.negative}>{error}</Notification>}
      {!commit && !error && <Spinner $size="small" />}
      {commit && (
        <>
          <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '10px' })}>
            <CommitIcon size={20} />
            <code className={css({ fontFamily: FONTS.MONO, fontSize: '13px', color: tok.textSecondary })}>{commit.commit.short_id}</code>
          </div>
          <h1 className={css({ marginTop: 0, marginBottom: '12px', fontSize: '22px', lineHeight: '30px', fontWeight: 600, color: tok.textPrimary })}>
            {commit.commit.subject || '(no subject)'}
          </h1>
          <div className={css({ fontSize: '13px', color: tok.textTertiary, marginBottom: '18px' })}>
            {commit.commit.author.name} · {commit.commit.author.email} · {formatTime(commit.commit.author.time)}
          </div>
          {commit.commit.message && commit.commit.message !== commit.commit.subject && (
            <pre className={css({ margin: '0 0 24px', whiteSpace: 'pre-wrap', fontFamily: 'inherit', fontSize: '14px', lineHeight: '22px', color: tok.textSecondary })}>{commit.commit.message}</pre>
          )}
          <ChangeList repo={repo} ref={commit.revision} changes={commit.changes} />
          {diff?.truncated && <div className={css({ display: 'flex', alignItems: 'center', gap: '6px', color: tok.status.stale.text, marginTop: '16px' })}><WarningIcon /> Diff truncated</div>}
          {diff && <PatchView patch={diff.patch} />}
        </>
      )}
    </div>
  )
}

function ChangeList({ repo, ref, changes }: { repo: string; ref: string; changes: GitFileChange[] }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ borderTop: `1px solid ${tok.cardBorder}`, marginBottom: '24px' })}>
      {changes.map((change) => (
        <div key={`${change.status}:${change.path}`} className={css({ display: 'grid', gridTemplateColumns: '32px minmax(0, 1fr) auto', gap: '8px', alignItems: 'center', minHeight: '38px', borderBottom: `1px solid ${tok.cardBorder}`, fontSize: '13px' })}>
          <span className={css({ fontFamily: FONTS.MONO, color: statusColor(change.status, tok), textAlign: 'center' })}>{statusLabel(change.status)}</span>
          {change.status === 'deleted' ? (
            <span className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textSecondary })}>
              {change.path}
            </span>
          ) : (
            <a href={href('/file', { repo, path: change.path, ref })} className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textSecondary, textDecoration: 'none' })}>
              {change.old_path ? `${change.old_path} → ${change.path}` : change.path}
            </a>
          )}
          <span className={css({ color: tok.textTertiary, paddingRight: '8px' })}>
            {change.binary ? 'binary' : <><span className={css({ color: tok.status.current.text })}>+{change.additions ?? 0}</span>{' '}<span className={css({ color: tok.status.conflict.text })}>-{change.deletions ?? 0}</span></>}
          </span>
        </div>
      ))}
    </div>
  )
}

function PatchView({ patch }: { patch: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflowX: 'auto' })}>
      {patch.split('\n').map((line, index) => {
        const kind = line.startsWith('+') && !line.startsWith('+++') ? 'add' : line.startsWith('-') && !line.startsWith('---') ? 'del' : line.startsWith('@@') ? 'hunk' : 'plain'
        return (
          <div key={index} className={css({ minHeight: '20px', paddingLeft: '10px', paddingRight: '14px', whiteSpace: 'pre', fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '20px', color: kind === 'add' ? tok.status.current.text : kind === 'del' ? tok.status.conflict.text : kind === 'hunk' ? tok.status.unavailable.text : tok.plainCode, backgroundColor: kind === 'add' ? tok.addedLineBg : kind === 'del' ? tok.deletedLineBg : 'transparent' })}>
            {line || ' '}
          </div>
        )
      })}
    </div>
  )
}

function statusColor(status: string, tok: ReturnType<typeof usePhebsTokens>): string {
  if (status === 'added') return tok.status.current.text
  if (status === 'deleted') return tok.status.conflict.text
  return tok.status.unavailable.text
}

function statusLabel(status: string): string {
  if (status === 'added') return 'A'
  if (status === 'deleted') return 'D'
  if (status === 'renamed' || status === 'copied') return 'R'
  return 'M'
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString()
}
