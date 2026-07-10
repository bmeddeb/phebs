import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Input } from 'baseui/input'
import { Button } from 'baseui/button'
import { Tag, KIND as TAG_KIND } from 'baseui/tag'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { LabelMedium, ParagraphSmall } from 'baseui/typography'
import { streamSearch } from '../api'
import type { FileResult, Stats } from '../api'
import { href, navigate } from '../router'

type Phase = 'idle' | 'streaming' | 'done' | 'error'

export default function SearchPage({ params }: { params: URLSearchParams }) {
  const urlQuery = params.get('q') ?? ''
  const [input, setInput] = useState(urlQuery)
  const [files, setFiles] = useState<FileResult[]>([])
  const [stats, setStats] = useState<Stats | null>(null)
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState('')
  const stopRef = useRef<() => void>(() => {})

  // the hash is the source of truth: searching = navigating
  useEffect(() => {
    setInput(urlQuery)
    if (!urlQuery) {
      setPhase('idle')
      setFiles([])
      setStats(null)
      return
    }
    setFiles([])
    setStats(null)
    setPhase('streaming')
    stopRef.current = streamSearch(
      urlQuery,
      (batch) => setFiles((prev) => [...prev, ...batch.files]),
      (s) => {
        setStats(s)
        setPhase('done')
      },
      (msg) => {
        setError(msg)
        setPhase('error')
      },
    )
    return () => stopRef.current()
  }, [urlQuery])

  return (
    <div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (input.trim()) navigate('/search', { q: input.trim() })
        }}
        style={{ display: 'flex', gap: '8px' }}
      >
        <Input
          value={input}
          onChange={(e) => setInput(e.currentTarget.value)}
          placeholder='Search code — try phebsNeedle repo:foo lang:go fork:no "exact phrase"'
          clearable
          autoFocus
        />
        <Button type="submit">Search</Button>
      </form>

      <div style={{ marginTop: '16px' }}>
        {phase === 'error' && (
          <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
            {error}
          </Notification>
        )}
        {phase === 'streaming' && files.length === 0 && <Spinner $size="small" />}
        {phase === 'done' && files.length === 0 && (
          <ParagraphSmall>No results for “{urlQuery}”.</ParagraphSmall>
        )}
        {stats && files.length > 0 && (
          <ParagraphSmall>
            {stats.match_count} matches in {stats.file_count} files ({stats.duration_ms}ms)
          </ParagraphSmall>
        )}
        <ResultList files={files} />
      </div>
    </div>
  )
}

function ResultList({ files }: { files: FileResult[] }) {
  // group results by repo, preserving stream arrival order
  const byRepo = new Map<string, FileResult[]>()
  for (const f of files) {
    const list = byRepo.get(f.repo) ?? []
    list.push(f)
    byRepo.set(f.repo, list)
  }
  return (
    <div>
      {[...byRepo.entries()].map(([repo, repoFiles]) => (
        <RepoGroup key={repo} repo={repo} files={repoFiles} />
      ))}
    </div>
  )
}

function RepoGroup({ repo, files }: { repo: string; files: FileResult[] }) {
  const [css, theme] = useStyletron()
  const matches = files.reduce(
    (n, f) => n + f.chunks.reduce((m, c) => m + c.ranges.length, 0),
    0,
  )
  return (
    <section className={css({ marginTop: theme.sizing.scale700 })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: theme.sizing.scale300 })}>
        <LabelMedium>{repo}</LabelMedium>
        <Tag closeable={false} kind={TAG_KIND.neutral}>
          {matches} {matches === 1 ? 'match' : 'matches'}
        </Tag>
      </div>
      {files.map((f) => (
        <FileBlock key={f.repo + f.path} file={f} />
      ))}
    </section>
  )
}

function FileBlock({ file }: { file: FileResult }) {
  const [css, theme] = useStyletron()
  const firstLine = file.chunks[0]?.ranges[0]?.start_line
  return (
    <div
      className={css({
        border: `1px solid ${theme.colors.borderOpaque}`,
        borderRadius: theme.borders.radius300,
        marginTop: theme.sizing.scale400,
        overflow: 'hidden',
      })}
    >
      <div
        className={css({
          backgroundColor: theme.colors.backgroundSecondary,
          paddingLeft: theme.sizing.scale500,
          paddingRight: theme.sizing.scale500,
          paddingTop: theme.sizing.scale200,
          paddingBottom: theme.sizing.scale200,
          borderBottom: `1px solid ${theme.colors.borderOpaque}`,
        })}
      >
        <a
          href={href('/file', {
            repo: file.repo,
            path: file.path,
            ...(firstLine ? { L: String(firstLine) } : {}),
          })}
          className={css({ color: theme.colors.contentPrimary, fontSize: '14px' })}
        >
          {file.path}
        </a>
      </div>
      {file.chunks.map((_, i) => (
        <ChunkView key={i} file={file} chunkIndex={i} />
      ))}
    </div>
  )
}

function ChunkView({ file, chunkIndex }: { file: FileResult; chunkIndex: number }) {
  const [css, theme] = useStyletron()
  const chunk = file.chunks[chunkIndex]
  const lines = chunk.content.replace(/\n$/, '').split('\n')
  return (
    <pre
      className={css({
        margin: 0,
        paddingLeft: theme.sizing.scale500,
        paddingRight: theme.sizing.scale500,
        paddingTop: theme.sizing.scale300,
        paddingBottom: theme.sizing.scale300,
        fontFamily: theme.typography.MonoParagraphSmall.fontFamily,
        fontSize: '13px',
        lineHeight: '20px',
        overflowX: 'auto',
        borderTop: chunkIndex > 0 ? `1px dashed ${theme.colors.borderOpaque}` : 'none',
      })}
    >
      {lines.map((line, i) => {
        const lineNo = chunk.start_line + i
        return (
          <div key={i}>
            <a
              href={href('/file', { repo: file.repo, path: file.path, L: String(lineNo) })}
              className={css({
                color: theme.colors.contentTertiary,
                textDecoration: 'none',
                display: 'inline-block',
                width: '48px',
                userSelect: 'none',
              })}
            >
              {lineNo}
            </a>
            {highlightLine(line, lineNo, chunk.ranges)}
          </div>
        )
      })}
    </pre>
  )
}

// highlightLine wraps the matched spans of one line in <mark>. Ranges are
// 1-based and file-relative (may span lines).
function highlightLine(line: string, lineNo: number, ranges: { start_line: number; start_col: number; end_line: number; end_col: number }[]) {
  const spans: { from: number; to: number }[] = []
  for (const r of ranges) {
    if (lineNo < r.start_line || lineNo > r.end_line) continue
    const from = lineNo === r.start_line ? r.start_col - 1 : 0
    const to = lineNo === r.end_line ? r.end_col - 1 : line.length
    spans.push({ from: Math.max(0, from), to: Math.min(line.length, to) })
  }
  if (spans.length === 0) return <span>{line}</span>
  spans.sort((a, b) => a.from - b.from)
  const parts: React.ReactNode[] = []
  let pos = 0
  spans.forEach((s, i) => {
    if (s.from > pos) parts.push(<span key={`t${i}`}>{line.slice(pos, s.from)}</span>)
    parts.push(<mark key={`m${i}`}>{line.slice(s.from, s.to)}</mark>)
    pos = Math.max(pos, s.to)
  })
  if (pos < line.length) parts.push(<span key="tail">{line.slice(pos)}</span>)
  return <>{parts}</>
}
