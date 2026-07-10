import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Input } from 'baseui/input'
import { Button } from 'baseui/button'
import { Tag, KIND as TAG_KIND } from 'baseui/tag'
import { Notification, KIND } from 'baseui/notification'
import type { LanguageSupport } from '@codemirror/language'
import { streamSearch } from '../api'
import type { FileResult, Range, Stats } from '../api'
import { href, navigate } from '../router'
import { usePhebsTokens, useMode, FONTS } from '../theme'
import { languageFor, langColor } from '../lang'
import { tokenize } from '../highlight'
import { SearchIcon, CopyIcon, CheckIcon, OpenIcon, ChevronRight, ChevronDown } from '../icons'
import { FOCUS_SEARCH } from '../App'

type Phase = 'idle' | 'streaming' | 'done' | 'error'

export default function SearchPage({ params }: { params: URLSearchParams }) {
  const urlQuery = params.get('q') ?? ''
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [input, setInput] = useState(urlQuery)
  const [files, setFiles] = useState<FileResult[]>([])
  const [stats, setStats] = useState<Stats | null>(null)
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState('')
  const stopRef = useRef<() => void>(() => {})
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onFocus = () => inputRef.current?.focus()
    window.addEventListener(FOCUS_SEARCH, onFocus)
    return () => window.removeEventListener(FOCUS_SEARCH, onFocus)
  }, [])

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
    setError('')
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

  const repoCount = new Set(files.map((f) => f.repo)).size

  return (
    <div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (input.trim()) navigate('/search', { q: input.trim() })
        }}
        className={css({ display: 'flex', gap: '8px' })}
      >
        <div className={css({ flex: 1 })}>
          <Input
            inputRef={inputRef as React.RefObject<HTMLInputElement>}
            value={input}
            onChange={(e) => setInput(e.currentTarget.value)}
            placeholder='Search code — try  func.*Parse  repo:zoekt  lang:go  "exact phrase"'
            clearable
            autoFocus
            startEnhancer={<SearchIcon />}
            overrides={{
              Root: { style: { height: '48px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } },
              Input: { style: { fontFamily: FONTS.MONO, fontSize: '15px' } },
            }}
          />
        </div>
        {phase === 'streaming' ? (
          <Button
            type="button"
            kind="secondary"
            onClick={() => {
              stopRef.current()
              setPhase('done')
            }}
            overrides={{ BaseButton: { style: { height: '48px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } } }}
          >
            Stop
          </Button>
        ) : (
          <Button
            type="submit"
            overrides={{ BaseButton: { style: { height: '48px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } } }}
          >
            Search
          </Button>
        )}
      </form>

      <HelperChips input={input} setInput={setInput} inputRef={inputRef} />

      {phase !== 'idle' && (
        <div
          className={css({
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            fontSize: '13px',
            color: tok.textTertiary,
            marginTop: '12px',
            marginBottom: '4px',
          })}
        >
          {phase === 'streaming' && (
            <span
              className={css({
                width: '8px',
                height: '8px',
                borderRadius: '50%',
                backgroundColor: tok.statusBlue,
                animationName: { '0%,100%': { opacity: 1 }, '50%': { opacity: 0.35 } },
                animationDuration: '1.4s',
                animationIterationCount: 'infinite',
              })}
            />
          )}
          <span>
            <b className={css({ color: tok.textPrimary })}>{countMatches(files)}</b> matches in{' '}
            <b className={css({ color: tok.textPrimary })}>{files.length}</b> files
            {stats ? ` · ${stats.duration_ms}ms` : ''}
            {repoCount > 0 ? ` · ${repoCount} ${repoCount === 1 ? 'repository' : 'repositories'}` : ''}
            {phase === 'streaming' ? ' · searching…' : ''}
          </span>
        </div>
      )}

      <div className={css({ marginTop: '8px' })}>
        {phase === 'error' && (
          <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
            {error}
          </Notification>
        )}
        {phase === 'streaming' && files.length === 0 && <SkeletonCards />}
        {phase === 'done' && files.length === 0 && (
          <div className={css({ padding: '48px 0', textAlign: 'center', color: tok.textTertiary })}>
            No results for <span className={css({ fontFamily: FONTS.MONO, color: tok.textPrimary })}>{urlQuery}</span>.
          </div>
        )}
        <ResultList files={files} />
      </div>
    </div>
  )
}

const OPERATORS = ['repo:', 'lang:', 'file:', 'sym:', 'case:yes', '-', '"exact phrase"']

function HelperChips({
  input,
  setInput,
  inputRef,
}: {
  input: string
  setInput: (s: string) => void
  inputRef: React.RefObject<HTMLInputElement | null>
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const insert = (op: string) => {
    const sep = input && !input.endsWith(' ') ? ' ' : ''
    setInput(input + sep + op)
    inputRef.current?.focus()
  }
  return (
    <div className={css({ display: 'flex', alignItems: 'center', gap: '6px', marginTop: '10px', flexWrap: 'wrap' })}>
      {OPERATORS.map((op) => (
        <button
          key={op}
          type="button"
          onClick={() => insert(op)}
          className={css({
            fontFamily: FONTS.MONO,
            fontSize: '12px',
            padding: '4px 8px',
            borderRadius: '6px',
            border: 'none',
            backgroundColor: tok.fill,
            color: tok.textSecondary,
            cursor: 'pointer',
            ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
          })}
        >
          {op}
        </button>
      ))}
      <div className={css({ flex: 1 })} />
      <a
        href="https://github.com/sourcegraph/zoekt/blob/main/doc/query_syntax.md"
        target="_blank"
        rel="noreferrer"
        className={css({ fontSize: '12px', color: tok.textTertiary, textDecoration: 'none', ':hover': { color: tok.accent } })}
      >
        Search syntax ↗
      </a>
    </div>
  )
}

function countMatches(files: FileResult[]): number {
  return files.reduce((n, f) => n + f.chunks.reduce((m, c) => m + c.ranges.length, 0), 0)
}

function ResultList({ files }: { files: FileResult[] }) {
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
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [open, setOpen] = useState(true)
  const matches = countMatches(files)
  return (
    <section className={css({ marginTop: '28px' })}>
      <button
        onClick={() => setOpen((o) => !o)}
        className={css({
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
          width: '100%',
          border: 'none',
          background: 'none',
          padding: '0 0 8px 0',
          cursor: 'pointer',
          color: tok.textPrimary,
          textAlign: 'left',
        })}
      >
        <span className={css({ color: tok.textTertiary, display: 'flex' })}>
          {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </span>
        <span className={css({ fontSize: '16px', fontWeight: 500 })}>{repo}</span>
        <Tag closeable={false} kind={TAG_KIND.neutral}>
          {matches} {matches === 1 ? 'match' : 'matches'} · {files.length} {files.length === 1 ? 'file' : 'files'}
        </Tag>
        <span className={css({ flex: 1, height: '1px', backgroundColor: tok.innerSep })} />
      </button>
      {open && files.map((f) => <FileBlock key={f.repo + f.path} file={f} />)}
    </section>
  )
}

function FileBlock({ file }: { file: FileResult }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [lang, setLang] = useState<LanguageSupport | null>(null)
  useEffect(() => {
    let live = true
    languageFor(file.path).then((l) => live && setLang(l))
    return () => {
      live = false
    }
  }, [file.path])

  const firstLine = file.chunks[0]?.ranges[0]?.start_line
  const matches = file.chunks.reduce((m, c) => m + c.ranges.length, 0)
  const slash = file.path.lastIndexOf('/')
  const dir = slash === -1 ? '' : file.path.slice(0, slash + 1)
  const name = slash === -1 ? file.path : file.path.slice(slash + 1)
  const fileHref = href('/file', { repo: file.repo, path: file.path, ...(firstLine ? { L: String(firstLine) } : {}) })

  return (
    <div
      className={css({
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        marginTop: '10px',
        overflow: 'hidden',
        backgroundColor: tok.pageBg,
      })}
    >
      <div
        className={css({
          height: '40px',
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
          paddingLeft: '12px',
          paddingRight: '10px',
          borderBottom: `1px solid ${tok.innerSep}`,
        })}
      >
        <span className={css({ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: langColor(file.path), flexShrink: 0 })} />
        <a href={fileHref} className={css({ textDecoration: 'none', fontSize: '13px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>
          <span className={css({ color: tok.textTertiary })}>{dir}</span>
          <span className={css({ color: tok.textPrimary, fontWeight: 500 })}>{name}</span>
        </a>
        <div className={css({ flex: 1 })} />
        <span className={css({ fontSize: '12px', color: tok.textTertiary, whiteSpace: 'nowrap' })}>{matches} {matches === 1 ? 'match' : 'matches'}</span>
        <CopyButton text={file.path} title="Copy path" />
        <a href={fileHref} title="Open file" className={css({ display: 'flex', color: tok.textTertiary, padding: '4px', borderRadius: '6px', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill } })}>
          <OpenIcon />
        </a>
      </div>
      {file.chunks.map((chunk, i) => (
        <ChunkView key={i} chunk={chunk} file={file} lang={lang} first={i === 0} />
      ))}
    </div>
  )
}

function ChunkView({
  chunk,
  file,
  lang,
  first,
}: {
  chunk: FileResult['chunks'][number]
  file: FileResult
  lang: LanguageSupport | null
  first: boolean
}) {
  const [css] = useStyletron()
  const { mode } = useMode()
  const tok = usePhebsTokens()
  const lines = chunk.content.replace(/\n$/, '').split('\n')
  return (
    <div
      className={css({
        borderTop: first ? 'none' : `1px solid ${tok.innerSep}`,
        paddingTop: '4px',
        paddingBottom: '4px',
      })}
    >
      {lines.map((line, i) => {
        const lineNo = chunk.start_line + i
        return (
          <div
            key={i}
            className={css({
              display: 'flex',
              fontFamily: FONTS.MONO,
              fontSize: '13px',
              lineHeight: '20px',
              ':hover': { backgroundColor: tok.hoverFill },
            })}
          >
            <a
              href={href('/file', { repo: file.repo, path: file.path, L: String(lineNo) })}
              className={css({
                flexShrink: 0,
                width: '48px',
                paddingRight: '12px',
                textAlign: 'right',
                color: tok.gutter,
                textDecoration: 'none',
                userSelect: 'none',
                ':hover': { color: tok.accent },
              })}
            >
              {lineNo}
            </a>
            <code className={css({ whiteSpace: 'pre', overflowX: 'auto', tabSize: 4, color: tok.plainCode, paddingRight: '12px' })}>
              {renderLine(line, lineNo, chunk.ranges, lang, mode, tok.matchBg)}
            </code>
          </div>
        )
      })}
    </div>
  )
}

// renderLine tokenizes one source line and overlays match ranges, emitting
// styled spans (syntax color + match background where they intersect).
function renderLine(
  line: string,
  lineNo: number,
  ranges: Range[],
  lang: LanguageSupport | null,
  mode: 'light' | 'dark',
  matchBg: string,
) {
  const tokens = tokenize(line, lang, mode)
  const matches = matchSpans(line, lineNo, ranges)
  const nodes: React.ReactNode[] = []
  let key = 0
  for (const t of tokens) {
    const base = { color: t.color, fontStyle: t.fontStyle }
    if (matches.length === 0) {
      nodes.push(<span key={key++} style={base}>{line.slice(t.from, t.to)}</span>)
      continue
    }
    const cuts = new Set([t.from, t.to])
    for (const m of matches) {
      if (m.to > t.from && m.from < t.to) {
        cuts.add(Math.max(t.from, m.from))
        cuts.add(Math.min(t.to, m.to))
      }
    }
    const sorted = [...cuts].sort((a, b) => a - b)
    for (let i = 0; i < sorted.length - 1; i++) {
      const a = sorted[i]
      const b = sorted[i + 1]
      if (a === b) continue
      const isMatch = matches.some((m) => m.from <= a && b <= m.to)
      nodes.push(
        <span key={key++} style={isMatch ? { ...base, backgroundColor: matchBg, borderRadius: '2px' } : base}>
          {line.slice(a, b)}
        </span>,
      )
    }
  }
  return nodes
}

// matchSpans returns the [from,to) column ranges of matches on lineNo.
function matchSpans(line: string, lineNo: number, ranges: Range[]): { from: number; to: number }[] {
  const spans: { from: number; to: number }[] = []
  for (const r of ranges) {
    if (lineNo < r.start_line || lineNo > r.end_line) continue
    const from = lineNo === r.start_line ? r.start_col - 1 : 0
    const to = lineNo === r.end_line ? r.end_col - 1 : line.length
    spans.push({ from: Math.max(0, from), to: Math.min(line.length, to) })
  }
  return spans
}

function CopyButton({ text, title }: { text: string; title: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      title={title}
      onClick={() => {
        navigator.clipboard?.writeText(text)
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
      className={css({
        display: 'flex',
        border: 'none',
        background: 'none',
        cursor: 'pointer',
        color: done ? tok.statusGreen : tok.textTertiary,
        padding: '4px',
        borderRadius: '6px',
        ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill },
      })}
    >
      {done ? <CheckIcon /> : <CopyIcon />}
    </button>
  )
}

function SkeletonCards() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const shimmer = css({
    backgroundImage: `linear-gradient(90deg, ${tok.fill} 0%, ${tok.cardBorder} 50%, ${tok.fill} 100%)`,
    backgroundSize: '200% 100%',
    animationName: { '0%': { backgroundPosition: '100% 0' }, '100%': { backgroundPosition: '0 0' } },
    animationDuration: '1.4s',
    animationIterationCount: 'infinite',
    borderRadius: '4px',
  })
  return (
    <div>
      {[0, 1].map((k) => (
        <div key={k} className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', marginTop: '10px', overflow: 'hidden' })}>
          <div className={css({ height: '40px', display: 'flex', alignItems: 'center', paddingLeft: '12px', borderBottom: `1px solid ${tok.innerSep}` })}>
            <div className={`${shimmer} ${css({ width: '220px', height: '12px' })}`} />
          </div>
          <div className={css({ padding: '12px' })}>
            {[60, 80, 45].map((w, i) => (
              <div key={i} className={`${shimmer} ${css({ width: `${w}%`, height: '10px', marginBottom: '8px' })}`} />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

