import { useEffect, useMemo, useState } from 'react'
import { useStyletron } from 'baseui'
import { Spinner } from 'baseui/spinner'
import { fetchBlame } from '../api'
import type { BlameLine, BlameResult, GitCommit } from '../api'
import { href } from '../router'
import { CommitIcon, WarningIcon } from '../icons'
import { FONTS, usePhebsTokens } from '../theme'
import { boundedError, isAbortError } from '../util'
import { SectionHelp } from '../components/SectionHelp'
import { ErrorNotice } from '../components/kit'

const HISTORY_EVIDENCE_CAPABILITIES: ReadonlySet<string> = new Set(['history'])

export default function BlamePage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const path = params.get('path') ?? ''
  const ref = params.get('ref') ?? ''
  return <BlamePageRead key={JSON.stringify([repo, path, ref])} repo={repo} path={path} ref={ref} />
}

function BlamePageRead({ repo, path, ref }: { repo: string; path: string; ref: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [result, setResult] = useState<BlameResult | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setResult(null)
    setError('')
    fetchBlame(repo, path, ref, controller.signal)
      .then(value => { if (!controller.signal.aborted) setResult(value) })
      .catch((cause) => {
        if (!controller.signal.aborted && !isAbortError(cause)) setError(boundedError(cause))
      })
    return () => controller.abort()
  }, [repo, path, ref, attempt])

  const commits = useMemo(() => new Map((result?.commits ?? []).map((commit) => [commit.id, commit])), [result])

  return (
    <div>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' })}>
        <CommitIcon size={20} />
        <h1 className={css({ margin: 0, fontSize: '20px', lineHeight: '28px', fontWeight: 600, color: tok.textPrimary })}>Blame</h1>
        <SectionHelp
          termId="implementation_evidence"
          enabledCapabilities={HISTORY_EVIDENCE_CAPABILITIES}
          triggerLabel="Blame"
        />
      </div>
      <div className={css({ display: 'flex', gap: '5px', marginBottom: '18px', minWidth: 0, fontSize: '13px', color: tok.textTertiary })}>
        <span>{repo}</span><span>/</span>
        <a href={href('/file', { repo, path, ref: result?.revision || ref })} className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textSecondary, textDecoration: 'none' })}>{path}</a>
      </div>
      {error && <ErrorNotice onRetry={() => setAttempt(value => value + 1)} retryLabel="Retry blame">{error}</ErrorNotice>}
      {!result && !error && <Spinner $size="small" />}
      {result?.truncated && (
        <div className={css({ display: 'flex', gap: '6px', alignItems: 'center', marginBottom: '10px', color: tok.status.stale.text, fontSize: '12px' })}>
          <WarningIcon /> Result truncated
        </div>
      )}
      {result && !result.lines?.length && <div className={css({ color: tok.textTertiary, padding: '24px 0' })}>No blame lines returned for this path at this revision.</div>}
      {result && !!result.lines?.length && (
        <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflowX: 'auto' })}>
          {(result.lines ?? []).map((line, index) => (
            <BlameRow
              key={line.line}
              repo={repo}
              line={line}
              commit={commits.get(line.commit_id)}
              repeated={index > 0 && (result.lines ?? [])[index - 1].commit_id === line.commit_id}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function BlameRow({ repo, line, commit, repeated }: { repo: string; line: BlameLine; commit?: GitCommit; repeated: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ display: 'grid', gridTemplateColumns: '180px 54px minmax(max-content, 1fr)', minHeight: '24px', borderBottom: `1px solid ${tok.innerSep}`, fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '24px', contentVisibility: 'auto' })}>
      <a aria-label={repeated ? `Commit ${line.commit_id.slice(0, 8)}` : undefined} href={href('/commit', { repo, ref: line.commit_id })} className={css({ paddingLeft: '8px', paddingRight: '8px', color: tok.textTertiary, textDecoration: 'none', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', backgroundColor: tok.fill })}>
        {repeated ? '' : `${commit?.author.name ?? ''} ${line.commit_id.slice(0, 8)}`}
      </a>
      <span className={css({ textAlign: 'right', paddingRight: '10px', color: tok.gutter, userSelect: 'none' })}>{line.line}</span>
      <code className={css({ whiteSpace: 'pre', paddingRight: '16px', color: tok.plainCode })}>{line.content}</code>
    </div>
  )
}
