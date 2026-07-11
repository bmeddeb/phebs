import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Input } from 'baseui/input'
import { Button } from 'baseui/button'
import { Tag, KIND as TAG_KIND } from 'baseui/tag'
import { Notification, KIND } from 'baseui/notification'
import type { LanguageSupport } from '@codemirror/language'
import { streamSearch } from '../api'
import type { FileResult, Range, Stats } from '../api'
import { FOCUS_SEARCH, href, navigate } from '../router'
import { usePhebsTokens, useMode, FONTS } from '../theme'
import { languageFor, langColor } from '../lang'
import { tokenize } from '../highlight'
import { SearchIcon, CopyIcon, CheckIcon, OpenIcon, ChevronRight, ChevronDown } from '../icons'
import { repoFilter, runeColumnToUTF16Offset, splitQueryTerms } from '../util'

type Phase = 'idle' | 'streaming' | 'done' | 'error'

const fileKey = (f: FileResult) => f.repo + '\0' + f.ref + '\0' + f.path

export default function SearchPage({ params }: { params: URLSearchParams }) {
  const urlQuery = params.get('q') ?? ''
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [input, setInput] = useState(urlQuery)
  const [files, setFiles] = useState<FileResult[]>([])
  const [stats, setStats] = useState<Stats | null>(null)
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState('')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState(-1)
  const [searchGeneration, setSearchGeneration] = useState(0)
  const stopRef = useRef<() => void>(() => {})
  const inputRef = useRef<HTMLInputElement>(null)
  const rowRefs = useRef(new Map<string, HTMLDivElement>())

  useEffect(() => {
    const onFocus = () => inputRef.current?.focus()
    window.addEventListener(FOCUS_SEARCH, onFocus)
    return () => window.removeEventListener(FOCUS_SEARCH, onFocus)
  }, [])

  // the hash is the source of truth: searching = navigating
  useEffect(() => {
    setInput(urlQuery)
    setSelected(-1)
    setCollapsed(new Set())
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
  }, [urlQuery, searchGeneration])

  // group by repo, preserving arrival order
  const groups = useMemo(() => {
    const m = new Map<string, FileResult[]>()
    for (const f of files) {
      const list = m.get(f.repo) ?? []
      list.push(f)
      m.set(f.repo, list)
    }
    return [...m.entries()]
  }, [files])

  // files reachable by keyboard: those in expanded groups, in render order
  const visible = useMemo(
    () => groups.filter(([repo]) => !collapsed.has(repo)).flatMap(([, fs]) => fs),
    [groups, collapsed],
  )

  // keyboard navigation: j/k move a file cursor, Enter opens, y copies, o folds
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement
      const typing = el instanceof HTMLElement && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'j' || e.key === 'k') {
        e.preventDefault()
        setSelected((s) => {
          const next = e.key === 'j' ? s + 1 : s - 1
          return Math.max(0, Math.min(visible.length - 1, next))
        })
      } else if (e.key === 'Enter' && selected >= 0 && visible[selected]) {
        const f = visible[selected]
        navigate('/file', {
          repo: f.repo,
          path: f.path,
          ref: f.ref,
          L: String(f.chunks[0]?.ranges[0]?.start_line ?? 1),
        })
      } else if (e.key === 'y' && selected >= 0 && visible[selected]) {
        navigator.clipboard?.writeText(visible[selected].path)
      } else if (e.key === 'o' && selected >= 0 && visible[selected]) {
        const repo = visible[selected].repo
        setCollapsed((c) => {
          const n = new Set(c)
          if (n.has(repo)) n.delete(repo)
          else n.add(repo)
          return n
        })
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visible, selected])

  // keep the selected card in view
  useEffect(() => {
    if (selected < 0 || !visible[selected]) return
    rowRefs.current.get(fileKey(visible[selected]))?.scrollIntoView({ block: 'nearest' })
  }, [selected, visible])

  const toggleGroup = (repo: string) =>
    setCollapsed((c) => {
      const n = new Set(c)
      if (n.has(repo)) n.delete(repo)
      else n.add(repo)
      return n
    })

  const repoCount = groups.length

  return (
    <div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          const next = input.trim()
          if (!next) return
          if (next === urlQuery) setSearchGeneration((generation) => generation + 1)
          else navigate('/search', { q: next })
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

      {phase === 'streaming' && (
        <div
          className={css({
            height: '2px',
            marginTop: '10px',
            borderRadius: '2px',
            backgroundImage: `linear-gradient(90deg, transparent 0%, ${tok.accent} 50%, transparent 100%)`,
            backgroundSize: '50% 100%',
            backgroundRepeat: 'no-repeat',
            animationName: { '0%': { backgroundPosition: '-60% 0' }, '100%': { backgroundPosition: '160% 0' } },
            animationDuration: '1.2s',
            animationTimingFunction: 'linear',
            animationIterationCount: 'infinite',
          })}
        />
      )}

      {phase !== 'idle' && (
        <div
          role="status"
          aria-live="polite"
          aria-atomic="true"
          className={css({
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            fontSize: '13px',
            color: tok.textTertiary,
            marginTop: '12px',
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
          <div className={css({ flex: 1 })} />
          {visible.length > 0 && (
            <span className={css({ display: 'flex', gap: '10px', color: tok.textTertiary, '@media screen and (max-width: 720px)': { display: 'none' } })}>
              <Kbd>j</Kbd><Kbd>k</Kbd> navigate · <Kbd>↵</Kbd> open · <Kbd>y</Kbd> copy · <Kbd>o</Kbd> fold
            </span>
          )}
        </div>
      )}

      <div className={css({
        display: 'flex',
        gap: '28px',
        marginTop: '16px',
        alignItems: 'flex-start',
        '@media screen and (max-width: 720px)': { flexDirection: 'column', gap: '12px' },
      })}>
        {files.length > 0 && (
          <FacetRail files={files} query={urlQuery} />
        )}
        <div className={css({ flex: 1, minWidth: 0 })}>
          {phase === 'error' && (
            <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto', marginTop: 0 } } }}>
              {error}
            </Notification>
          )}
          {phase === 'streaming' && files.length === 0 && <SkeletonCards />}
          {phase === 'done' && files.length === 0 && (
            <div className={css({ padding: '48px 0', textAlign: 'center', color: tok.textTertiary })}>
              No results for <span className={css({ fontFamily: FONTS.MONO, color: tok.textPrimary })}>{urlQuery}</span>.
            </div>
          )}
          {groups.map(([repo, repoFiles]) => (
            <RepoGroup
              key={repo}
              repo={repo}
              files={repoFiles}
              open={!collapsed.has(repo)}
              onToggle={() => toggleGroup(repo)}
              selectedKey={selected >= 0 && visible[selected] ? fileKey(visible[selected]) : ''}
              registerRef={(k, el) => {
                if (el) rowRefs.current.set(k, el)
                else rowRefs.current.delete(k)
              }}
            />
          ))}
        </div>
      </div>
    </div>
  )
}

function Kbd({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <kbd
      className={css({
        fontFamily: FONTS.MONO,
        fontSize: '11px',
        padding: '1px 5px',
        border: `1px solid ${tok.kbdBorder}`,
        borderRadius: '4px',
        color: tok.textSecondary,
      })}
    >
      {children}
    </kbd>
  )
}

// FacetRail derives repo and language counts from the streamed files; each
// facet toggles a repo:/lang: term in the query and re-navigates.
function FacetRail({ files, query }: { files: FileResult[]; query: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()

  const repos = useMemo(() => {
    const m = new Map<string, number>()
    for (const f of files) m.set(f.repo, (m.get(f.repo) ?? 0) + 1)
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [files])

  const langs = useMemo(() => {
    const m = new Map<string, number>()
    for (const f of files) {
      const l = (f.language || extLang(f.path)).toLowerCase()
      m.set(l, (m.get(l) ?? 0) + 1)
    }
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [files])

  const terms = new Set(splitQueryTerms(query))
  const toggle = (term: string) => {
    const next = new Set(terms)
    if (next.has(term)) next.delete(term)
    else next.add(term)
    navigate('/search', { q: [...next].join(' ') })
  }
  const shortRepo = (r: string) => r.slice(r.lastIndexOf('/') + 1)

  return (
    <aside className={css({
      width: '224px',
      flexShrink: 0,
      '@media screen and (max-width: 720px)': {
        display: 'flex',
        gap: '16px',
        width: '100%',
        overflowX: 'auto',
        paddingBottom: '4px',
      },
    })}>
      <FacetSection title="Repositories">
        {repos.map(([repo, n]) => {
          const term = repoFilter(repo)
          const active = terms.has(term)
          return (
            <FacetRow
              key={repo}
              active={active}
              label={`${active ? 'Remove' : 'Add'} repository filter ${repo}`}
              onClick={() => toggle(term)}
            >
              <span
                aria-hidden="true"
                className={css({
                  width: '14px',
                  height: '14px',
                  border: `1px solid ${active ? tok.accent : tok.kbdBorder}`,
                  borderRadius: '3px',
                  backgroundColor: active ? tok.accent : 'transparent',
                  color: tok.pageBg,
                  fontSize: '11px',
                  lineHeight: '12px',
                  textAlign: 'center',
                  flexShrink: 0,
                })}
              >
                {active ? '✓' : ''}
              </span>
              <span className={css({ flex: 1, fontSize: '13px', color: tok.textSecondary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>
                {shortRepo(repo)}
              </span>
              <span className={css({ fontSize: '12px', color: tok.textTertiary })}>{n}</span>
            </FacetRow>
          )
        })}
      </FacetSection>
      {langs.length > 0 && (
        <FacetSection title="Languages">
          {langs.map(([lang, n]) => {
            const term = `lang:${lang}`
            const active = terms.has(term)
            return (
              <FacetRow
                key={lang}
                active={active}
                label={`${active ? 'Remove' : 'Add'} language filter ${lang}`}
                onClick={() => toggle(term)}
              >
                <span className={css({ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: langColor('x.' + lang) })} />
                <span className={css({ flex: 1, fontSize: '13px', fontWeight: active ? 600 : 400, color: active ? tok.textPrimary : tok.textSecondary })}>
                  {lang}
                </span>
                <span className={css({ fontSize: '12px', color: tok.textTertiary })}>{n}</span>
              </FacetRow>
            )
          })}
        </FacetSection>
      )}
    </aside>
  )
}

function FacetSection({ title, children }: { title: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      marginBottom: '20px',
      '@media screen and (max-width: 720px)': {
        flex: '1 0 180px',
        marginBottom: 0,
      },
    })}>
      <div className={css({ fontSize: '11px', fontWeight: 600, letterSpacing: '0.05em', textTransform: 'uppercase', color: tok.textTertiary, marginBottom: '6px' })}>
        {title}
      </div>
      {children}
    </div>
  )
}

function FacetRow({
  active,
  label,
  onClick,
  children,
}: {
  active: boolean
  label: string
  onClick: () => void
  children: React.ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={active}
      onClick={onClick}
      className={css({
        display: 'flex',
        alignItems: 'center',
        gap: '8px',
        height: '32px',
        paddingLeft: '4px',
        paddingRight: '4px',
        borderRadius: '6px',
        width: '100%',
        border: 'none',
        backgroundColor: 'transparent',
        color: 'inherit',
        textAlign: 'left',
        cursor: 'pointer',
        ':hover': { backgroundColor: tok.hoverFill },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
      })}
    >
      {children}
    </button>
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

function RepoGroup({
  repo,
  files,
  open,
  onToggle,
  selectedKey,
  registerRef,
}: {
  repo: string
  files: FileResult[]
  open: boolean
  onToggle: () => void
  selectedKey: string
  registerRef: (key: string, el: HTMLDivElement | null) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const matches = countMatches(files)
  return (
    <section className={css({ marginTop: '28px' })}>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className={css({ display: 'flex', alignItems: 'center', gap: '8px', width: '100%', border: 'none', background: 'none', padding: '0 0 8px 0', cursor: 'pointer', color: tok.textPrimary, textAlign: 'left' })}
      >
        <span className={css({ color: tok.textTertiary, display: 'flex' })}>
          {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </span>
        <span className={css({ fontSize: '16px', fontWeight: 500, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{repo}</span>
        <Tag closeable={false} kind={TAG_KIND.neutral}>
          {matches} {matches === 1 ? 'match' : 'matches'} · {files.length} {files.length === 1 ? 'file' : 'files'}
        </Tag>
        <span className={css({ flex: 1, height: '1px', backgroundColor: tok.innerSep })} />
      </button>
      {open &&
        files.map((f) => (
          <FileBlock key={fileKey(f)} file={f} selected={fileKey(f) === selectedKey} registerRef={registerRef} />
        ))}
    </section>
  )
}

function FileBlock({
  file,
  selected,
  registerRef,
}: {
  file: FileResult
  selected: boolean
  registerRef: (key: string, el: HTMLDivElement | null) => void
}) {
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
  const fileHref = href('/file', {
    repo: file.repo,
    path: file.path,
    ref: file.ref,
    ...(firstLine ? { L: String(firstLine) } : {}),
  })

  return (
    <div
      ref={(el) => registerRef(fileKey(file), el)}
      className={css({
        border: `1px solid ${selected ? tok.accent : tok.cardBorder}`,
        boxShadow: selected ? `0 0 0 1px ${tok.accent}` : 'none',
        borderRadius: '8px',
        marginTop: '10px',
        overflow: 'hidden',
        backgroundColor: tok.pageBg,
      })}
    >
      <div className={css({ height: '40px', display: 'flex', alignItems: 'center', gap: '8px', paddingLeft: '12px', paddingRight: '10px', borderBottom: `1px solid ${tok.innerSep}` })}>
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
    <div className={css({ borderTop: first ? 'none' : `1px solid ${tok.innerSep}`, paddingTop: '4px', paddingBottom: '4px' })}>
      {lines.map((line, i) => {
        const lineNo = chunk.start_line + i
        return (
          <div key={i} className={css({ display: 'flex', fontFamily: FONTS.MONO, fontSize: '13px', lineHeight: '20px', ':hover': { backgroundColor: tok.hoverFill } })}>
            <a
              href={href('/file', {
                repo: file.repo,
                path: file.path,
                ref: file.ref,
                L: String(lineNo),
              })}
              className={css({ flexShrink: 0, width: '48px', paddingRight: '12px', textAlign: 'right', color: tok.gutter, textDecoration: 'none', userSelect: 'none', ':hover': { color: tok.accent } })}
            >
              {lineNo}
            </a>
            <code className={css({ flex: '1 1 0', minWidth: 0, whiteSpace: 'pre', overflowX: 'auto', tabSize: 4, color: tok.plainCode, paddingRight: '12px' })}>
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
function renderLine(line: string, lineNo: number, ranges: Range[], lang: LanguageSupport | null, mode: 'light' | 'dark', matchBg: string) {
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

function matchSpans(line: string, lineNo: number, ranges: Range[]): { from: number; to: number }[] {
  const spans: { from: number; to: number }[] = []
  for (const r of ranges) {
    if (lineNo < r.start_line || lineNo > r.end_line) continue
    const from = lineNo === r.start_line ? runeColumnToUTF16Offset(line, r.start_col) : 0
    const to = lineNo === r.end_line ? runeColumnToUTF16Offset(line, r.end_col) : line.length
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
      className={css({ display: 'flex', border: 'none', background: 'none', cursor: 'pointer', color: done ? tok.statusGreen : tok.textTertiary, padding: '4px', borderRadius: '6px', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill } })}
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

// extLang maps a file extension to a lang: filter name when the API doesn't
// supply a language.
function extLang(path: string): string {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  const map: Record<string, string> = {
    go: 'go', ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
    py: 'python', md: 'markdown', json: 'json', proto: 'protobuf', sh: 'shell', yaml: 'yaml', yml: 'yaml',
  }
  return map[ext] ?? ext ?? 'text'
}
