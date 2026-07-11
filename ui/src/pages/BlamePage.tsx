import { useEffect, useMemo, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchBlame } from '../api'
import type { BlameResult, GitCommit } from '../api'
import { href } from '../router'
import { CommitIcon, WarningIcon } from '../icons'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'

export default function BlamePage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const path = params.get('path') ?? ''
  const ref = params.get('ref') ?? ''
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [result, setResult] = useState<BlameResult | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setResult(null)
    setError('')
    fetchBlame(repo, path, ref, controller.signal)
      .then(setResult)
      .catch((cause) => {
        if (!isAbortError(cause)) setError(String(cause))
      })
    return () => controller.abort()
  }, [repo, path, ref])

  const commits = useMemo(() => new Map(result?.commits.map((commit) => [commit.id, commit]) ?? []), [result])

  return (
    <div>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' })}>
        <CommitIcon size={20} />
        <h1 className={css({ margin: 0, fontSize: '20px', lineHeight: '28px', fontWeight: 600, color: tok.textPrimary })}>Blame</h1>
      </div>
      <div className={css({ display: 'flex', gap: '5px', marginBottom: '18px', minWidth: 0, fontSize: '13px', color: tok.textTertiary })}>
        <span>{repo}</span><span>/</span>
        <a href={href('/file', { repo, path, ref: result?.revision || ref })} className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textSecondary, textDecoration: 'none' })}>{path}</a>
      </div>
      {error && <Notification kind={NOTIFICATION_KIND.negative}>{error}</Notification>}
      {!result && !error && <Spinner $size="small" />}
      {result?.truncated && (
        <div className={css({ display: 'flex', gap: '6px', alignItems: 'center', marginBottom: '10px', color: tok.statusAmber, fontSize: '12px' })}>
          <WarningIcon /> Result truncated
        </div>
      )}
      {result && (
        <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflowX: 'auto' })}>
          {result.lines.map((line, index) => (
            <BlameRow
              key={line.line}
              repo={repo}
              line={line}
              commit={commits.get(line.commit_id)}
              repeated={index > 0 && result.lines[index - 1].commit_id === line.commit_id}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function BlameRow({ repo, line, commit, repeated }: { repo: string; line: BlameResult['lines'][number]; commit?: GitCommit; repeated: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ display: 'grid', gridTemplateColumns: '180px 54px minmax(max-content, 1fr)', minHeight: '24px', borderBottom: `1px solid ${tok.innerSep}`, fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '24px', contentVisibility: 'auto' })}>
      <a href={href('/commit', { repo, ref: line.commit_id })} className={css({ paddingLeft: '8px', paddingRight: '8px', color: tok.textTertiary, textDecoration: 'none', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', backgroundColor: tok.fill })}>
        {repeated ? '' : `${commit?.author.name ?? ''} ${line.commit_id.slice(0, 8)}`}
      </a>
      <span className={css({ textAlign: 'right', paddingRight: '10px', color: tok.gutter, userSelect: 'none' })}>{line.line}</span>
      <code className={css({ whiteSpace: 'pre', paddingRight: '16px', color: tok.plainCode })}>{line.content}</code>
    </div>
  )
}
