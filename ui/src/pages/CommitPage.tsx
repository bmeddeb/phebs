import { useEffect, useMemo, useState } from 'react'
import type { CSSProperties } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { fetchCommit, fetchDiff } from '../api'
import type { CommitResult, DiffResult, GitFileChange } from '../api'
import { href } from '../router'
import { CommitIcon, WarningIcon } from '../icons'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'
import { SectionHelp } from '../components/SectionHelp'

const HISTORY_EVIDENCE_CAPABILITIES: ReadonlySet<string> = new Set(['history'])

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
            <SectionHelp
              termId="implementation_evidence"
              enabledCapabilities={HISTORY_EVIDENCE_CAPABILITIES}
              triggerLabel="Commit"
            />
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
          {diff && <PatchView diff={diff} />}
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

type PatchGroup = {
  lines: string[]
  change?: GitFileChange
  fileNumber?: number
  prelude: boolean
}

type PatchLineKind = 'add' | 'del' | 'hunk' | 'plain'

const PATCH_SECTION_STYLE: CSSProperties = {
  contentVisibility: 'auto',
  containIntrinsicSize: 'auto 320px',
}

function PatchView({ diff }: { diff: DiffResult }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const groups = useMemo(() => groupPatch(diff.patch, diff.files), [diff.patch, diff.files])
  const lineBase = {
    minHeight: '20px',
    paddingLeft: '10px',
    paddingRight: '14px',
    whiteSpace: 'pre' as const,
    fontFamily: FONTS.MONO,
    fontSize: '12px',
    lineHeight: '20px',
  }
  const lineClasses: Record<PatchLineKind, string> = {
    add: css({ ...lineBase, color: tok.status.current.text, backgroundColor: tok.addedLineBg }),
    del: css({ ...lineBase, color: tok.status.conflict.text, backgroundColor: tok.deletedLineBg }),
    hunk: css({ ...lineBase, color: tok.status.unavailable.text, backgroundColor: 'transparent' }),
    plain: css({ ...lineBase, color: tok.plainCode, backgroundColor: 'transparent' }),
  }
  if (groups.length === 0) return null

  return (
    <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflow: 'hidden' })}>
      {groups.map((group, groupIndex) => {
        const headingId = `commit-diff-file-${groupIndex}`
        return (
          <section
            key={`${group.fileNumber ?? 'patch'}:${groupIndex}`}
            aria-labelledby={headingId}
            style={PATCH_SECTION_STYLE}
            className={css({ borderBottom: `1px solid ${tok.cardBorder}`, ':last-child': { borderBottom: 'none' } })}
          >
            <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', minHeight: '38px', paddingLeft: '10px', paddingRight: '10px', backgroundColor: tok.fill, borderTopLeftRadius: groupIndex === 0 ? '7px' : 0, borderTopRightRadius: groupIndex === 0 ? '7px' : 0 })}>
              {group.change && (
                <span
                  aria-hidden="true"
                  className={css({ flexShrink: 0, minWidth: '18px', fontFamily: FONTS.MONO, fontSize: '11px', fontWeight: 600, color: statusColor(group.change.status, tok), textAlign: 'center' })}
                >
                  {statusLabel(group.change.status)}
                </span>
              )}
              <h2 id={headingId} className={css({ minWidth: 0, flex: 1, margin: 0, fontSize: '12px', lineHeight: '20px', fontWeight: 500, color: tok.textSecondary, overflow: 'hidden' })}>
                {group.change && (
                  <span className={css({ position: 'absolute', width: '1px', height: '1px', overflow: 'hidden', clipPath: 'inset(50%)', whiteSpace: 'nowrap' })}>
                    {statusText(group.change.status)} file:{' '}
                  </span>
                )}
                <code className={css({ display: 'block', fontFamily: FONTS.MONO, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })} title={patchGroupHeading(group)}>
                  {patchGroupHeading(group)}
                </code>
              </h2>
              {group.change && (
                <span className={css({ flexShrink: 0, fontFamily: FONTS.MONO, fontSize: '11px', color: tok.textTertiary, whiteSpace: 'nowrap' })}>
                  {group.change.binary ? 'binary' : <><span className={css({ color: tok.status.current.text })}>+{group.change.additions ?? 0}</span>{' '}<span className={css({ color: tok.status.conflict.text })}>-{group.change.deletions ?? 0}</span></>}
                </span>
              )}
            </div>
            <div className={css({ overflowX: 'auto', overscrollBehaviorX: 'contain' })}>
              <PatchLines lines={group.lines} lineClasses={lineClasses} />
            </div>
          </section>
        )
      })}
    </div>
  )
}

function groupPatch(patch: string, files: GitFileChange[]): PatchGroup[] {
  if (patch === '') return []
  const lines = patch.split('\n')
  if (lines.at(-1) === '') lines.pop()
  if (!lines.some((line) => line.startsWith('diff --git '))) {
    return [{ lines, change: files.length === 1 ? files[0] : undefined, prelude: false }]
  }

  const groups: PatchGroup[] = []
  let currentLines: string[] = []
  let currentChange: GitFileChange | undefined
  let currentFileNumber: number | undefined
  let nextFileIndex = 0

  const flush = () => {
    if (currentLines.length === 0) return
    groups.push({
      lines: currentLines,
      change: currentChange,
      fileNumber: currentFileNumber,
      prelude: currentFileNumber === undefined,
    })
  }

  for (const line of lines) {
    if (line.startsWith('diff --git ')) {
      flush()
      currentLines = [line]
      currentChange = files[nextFileIndex]
      currentFileNumber = nextFileIndex + 1
      nextFileIndex += 1
      continue
    }
    currentLines.push(line)
  }
  flush()

  return groups
}

function patchGroupHeading(group: PatchGroup): string {
  if (group.change) {
    return group.change.old_path ? `${group.change.old_path} → ${group.change.path}` : group.change.path
  }
  if (group.prelude) return 'Patch prelude'
  if (group.fileNumber) return `File ${group.fileNumber}`
  return 'Patch'
}

function PatchLines({ lines, lineClasses }: { lines: string[]; lineClasses: Record<PatchLineKind, string> }) {
  let inHunk = false
  return lines.map((line, lineIndex) => {
    const kind = patchLineKind(line, inHunk)
    if (kind === 'hunk') inHunk = true
    return <div key={lineIndex} className={lineClasses[kind]}>{line}</div>
  })
}

function patchLineKind(line: string, inHunk: boolean): PatchLineKind {
  if (line.startsWith('@@')) return 'hunk'
  if (inHunk && line.startsWith('+')) return 'add'
  if (inHunk && line.startsWith('-')) return 'del'
  return 'plain'
}

function statusColor(status: string, tok: ReturnType<typeof usePhebsTokens>): string {
  if (status === 'added') return tok.status.current.text
  if (status === 'deleted') return tok.status.conflict.text
  return tok.status.unavailable.text
}

function statusLabel(status: string): string {
  if (status === 'added') return 'A'
  if (status === 'deleted') return 'D'
  if (status === 'modified') return 'M'
  if (status === 'renamed') return 'R'
  if (status === 'copied') return 'C'
  if (status === 'type_changed') return 'T'
  if (status === 'unmerged') return 'U'
  return '?'
}

function statusText(status: string): string {
  if (status === 'added' || status === 'deleted' || status === 'modified' || status === 'renamed' || status === 'copied' || status === 'unmerged') return status
  if (status === 'type_changed') return 'type changed'
  return 'unknown'
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString()
}
