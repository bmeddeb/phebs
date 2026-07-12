import { useCallback, useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, lineNumbers, Decoration } from '@codemirror/view'
import { syntaxHighlighting } from '@codemirror/language'
import {
  fetchDefinition,
  fetchFolderContents,
  fetchHover,
  fetchReferences,
  fetchRepoStatus,
  fetchSource,
} from '../api'
import type {
  CodeLocation,
  DefinitionResult,
  HoverResult,
  ReferencesResult,
  RepoStatus,
  TreeEntry,
} from '../api'
import { languageFor, langColor, langName } from '../lang'
import { highlightStyle } from '../highlight'
import { usePhebsTokens, useMode, FONTS } from '../theme'
import { href, navigate } from '../router'
import { CopyIcon, CheckIcon, CommitIcon, SearchIcon, ChevronRight, ChevronDown } from '../icons'
import { ancestorFolders, fileFilter, humanSize, isAbortError, relTime, repoFilter } from '../util'

interface SourcePosition {
  line: number
  character: number
}

interface CodeNavigationState {
  loading: boolean
  definition?: DefinitionResult
  references?: ReferencesResult
  hover?: HoverResult
  error?: string
}

// T5.3/T5.5: CodeMirror 6 read-only viewer with breadcrumbs, a sticky
// metadata header, ?L= deep-link line, and syntax highlighting.
export default function FilePage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const path = params.get('path') ?? ''
  const ref = params.get('ref') ?? ''
  const line = Number(params.get('L') ?? '0')
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [binary, setBinary] = useState(false)
  const [meta, setMeta] = useState<RepoStatus | null>(null)
  const [metaLoaded, setMetaLoaded] = useState(false)
  const [navPosition, setNavPosition] = useState<SourcePosition | null>(null)
  const [navigation, setNavigation] = useState<CodeNavigationState | null>(null)
  const sourceGeneration = useRef(0)
  const metaGeneration = useRef(0)
  const navGeneration = useRef(0)
  const resolvingRef = !ref && !metaLoaded
  const effectiveRef = ref || meta?.indexed_commit_hash || ''

  useEffect(() => {
    const generation = ++sourceGeneration.current
    const controller = new AbortController()
    setContent(null)
    setError('')
    setBinary(false)
    setNavPosition(null)
    setNavigation(null)
    if (resolvingRef) return
    if (!repo || !path) {
      setError('missing repo or path')
      return
    }
    if (!effectiveRef) {
      setError('repository has no indexed revision')
      return
    }
    fetchSource(repo, path, effectiveRef, controller.signal)
      .then((f) => {
        if (generation !== sourceGeneration.current) return
        if (f.encoding === 'base64') setBinary(true)
        else setContent(f.content)
      })
      .catch((e) => {
        if (!isAbortError(e) && generation === sourceGeneration.current) setError(String(e))
      })
    return () => controller.abort()
  }, [repo, path, effectiveRef, resolvingRef])

  useEffect(() => {
    const generation = ++metaGeneration.current
    const controller = new AbortController()
    setMeta(null)
    setMetaLoaded(false)
    fetchRepoStatus(controller.signal)
      .then((rows) => {
        if (generation === metaGeneration.current) {
          setMeta(rows.find((r) => r.name === repo) ?? null)
          setMetaLoaded(true)
        }
      })
      .catch((error) => {
        if (!isAbortError(error) && generation === metaGeneration.current) setMetaLoaded(true)
      })
    return () => controller.abort()
  }, [repo])

  useEffect(() => {
    const generation = ++navGeneration.current
    const controller = new AbortController()
    if (!navPosition || !repo || !path || !effectiveRef) return
    setNavigation({ loading: true })
    Promise.all([
      fetchDefinition(repo, effectiveRef, path, navPosition.line, navPosition.character, controller.signal),
      fetchReferences(repo, effectiveRef, path, navPosition.line, navPosition.character, controller.signal),
      fetchHover(repo, effectiveRef, path, navPosition.line, navPosition.character, controller.signal),
    ])
      .then(([definition, references, hover]) => {
        if (generation === navGeneration.current) {
          setNavigation({ loading: false, definition, references, hover })
        }
      })
      .catch((error) => {
        if (!isAbortError(error) && generation === navGeneration.current) {
          setNavigation({ loading: false, error: String(error) })
        }
      })
    return () => controller.abort()
  }, [repo, path, effectiveRef, navPosition])

  const selectPosition = useCallback((line: number, character: number) => {
    setNavPosition({ line, character })
  }, [])

  return (
    <div>
      <Breadcrumb repo={repo} path={path} ref={effectiveRef} pinned={Boolean(ref)} meta={meta} />
      <div className={css({
        display: 'flex',
        gap: '16px',
        alignItems: 'flex-start',
        '@media screen and (max-width: 720px)': { flexDirection: 'column' },
      })}>
        {repo && effectiveRef && <FileTree repo={repo} ref={effectiveRef} current={path} />}
        <div className={css({ flex: 1, minWidth: 0, '@media screen and (max-width: 720px)': { width: '100%' } })}>
          {error && (
            <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto', marginTop: 0 } } }}>
              {error}
            </Notification>
          )}
          {binary && <div className={css({ color: tok.textTertiary, padding: '24px 0' })}>Binary file — not rendered.</div>}
          {content === null && !error && !binary && <Spinner $size="small" />}
          {content !== null && (
            <div className={css({ display: 'flex', alignItems: 'flex-start', gap: '16px', '@media screen and (max-width: 960px)': { flexDirection: 'column' } })}>
              <div className={css({ flex: 1, minWidth: 0, width: '100%', border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflow: 'clip' })}>
                <CodeHeader path={path} content={content} line={line} meta={meta} />
                <CodeViewer
                  content={content}
                  path={path}
                  focusLine={line}
                  selectedLine={navPosition ? navPosition.line + 1 : 0}
                  onPosition={selectPosition}
                />
              </div>
              {navPosition && (
                <CodeNavigationPanel
                  position={navPosition}
                  navigation={navigation}
                />
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function CodeNavigationPanel({
  position,
  navigation,
}: {
  position: SourcePosition
  navigation: CodeNavigationState | null
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const definition = navigation?.definition
  const references = navigation?.references
  const hover = navigation?.hover?.hover
  const unavailable = navigation && !navigation.loading && !navigation.error &&
    !definition?.available && !references?.available && !navigation.hover?.available
  const empty = navigation && !navigation.loading && !navigation.error && !unavailable &&
    !definition?.location && !hover && !references?.locations.length

  return (
    <aside
      aria-label="Code navigation"
      className={css({
        width: '280px',
        boxSizing: 'border-box',
        flexShrink: 0,
        position: 'sticky',
        top: '68px',
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        maxHeight: 'calc(100vh - 84px)',
        overflowY: 'auto',
        '@media screen and (max-width: 960px)': {
          width: '100%',
          position: 'static',
          maxHeight: 'none',
        },
      })}
    >
      <div className={css({ height: '36px', display: 'flex', alignItems: 'center', gap: '8px', paddingLeft: '12px', paddingRight: '12px', backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.innerSep}`, borderTopLeftRadius: '8px', borderTopRightRadius: '8px' })}>
        <h2 className={css({ margin: 0, fontSize: '12.5px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary })}>Code navigation</h2>
        <span className={css({ fontFamily: FONTS.MONO, fontSize: '11px', color: tok.textTertiary })}>
          {position.line + 1}:{position.character + 1}
        </span>
      </div>
      <div className={css({ padding: '12px' })}>
        {navigation?.loading && <Spinner $size="small" />}
        {navigation?.error && (
          <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto', margin: 0 } } }}>
            {navigation.error}
          </Notification>
        )}
        {unavailable && <PanelMessage>SCIP data is not available for this revision.</PanelMessage>}
        {empty && <PanelMessage>No precise symbol exists at this position.</PanelMessage>}
        {hover && (
          <section className={css({ marginBottom: '16px' })}>
            <PanelTitle>Hover</PanelTitle>
            <div className={css({ fontSize: '12.5px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary, overflowWrap: 'anywhere' })}>
              {hover.display_name || hover.symbol}
            </div>
            {hover.kind && <div className={css({ marginTop: '3px', fontSize: '11px', color: tok.textTertiary })}>{hover.kind}</div>}
            {hover.signature && (
              <pre className={css({ marginTop: '8px', marginBottom: 0, padding: '8px', overflowX: 'auto', whiteSpace: 'pre-wrap', fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px', color: tok.plainCode, backgroundColor: tok.fill, borderRadius: '6px' })}>
                {hover.signature}
              </pre>
            )}
            {hover.documentation?.map((paragraph, index) => (
              <p key={`${index}:${paragraph}`} className={css({ marginTop: '8px', marginBottom: 0, fontSize: '12px', lineHeight: '18px', color: tok.textSecondary, overflowWrap: 'anywhere' })}>
                {paragraph}
              </p>
            ))}
          </section>
        )}
        {definition?.location && (
          <section className={css({ marginBottom: '16px' })}>
            <PanelTitle>Definition</PanelTitle>
            <LocationLink location={definition.location} />
          </section>
        )}
        {!!references?.locations.length && (
          <section>
            <PanelTitle>References ({references.locations.length}{references.truncated ? '+' : ''})</PanelTitle>
            <div className={css({ display: 'grid' })}>
              {references.locations.slice(0, 100).map((location, index) => (
                <LocationLink
                  key={`${location.path}:${location.range.start.line}:${location.range.start.character}:${index}`}
                  location={location}
                />
              ))}
            </div>
            {references.locations.length > 100 && (
              <div className={css({ marginTop: '6px', fontSize: '11px', color: tok.textTertiary })}>
                Showing the first 100 references.
              </div>
            )}
          </section>
        )}
      </div>
    </aside>
  )
}

function PanelTitle({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <h3 className={css({ marginTop: 0, marginBottom: '7px', fontSize: '10.5px', lineHeight: '14px', fontWeight: 600, letterSpacing: '0.05em', color: tok.textTertiary, textTransform: 'uppercase' })}>{children}</h3>
}

function PanelMessage({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css({ fontSize: '12px', color: tok.textTertiary })}>{children}</div>
}

function LocationLink({ location }: { location: CodeLocation }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const line = location.range.start.line + 1
  return (
    <a
      href={href('/file', { repo: location.repo, path: location.path, ref: location.revision, L: String(line) })}
      className={css({ display: 'block', minWidth: 0, paddingTop: '3px', paddingBottom: '3px', color: tok.accent, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px', textDecoration: 'none', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', ':hover': { textDecoration: 'underline' } })}
      title={`${location.path}:${line}`}
    >
      {location.path}:{line}
    </a>
  )
}

interface FolderRecord {
  entries?: TreeEntry[]
  error?: string
  loading: boolean
}

const folderKey = (repo: string, ref: string, path: string) =>
  `${repo}\0${ref}\0${path}`

function joinPath(parent: string, name: string): string {
  return parent ? `${parent}/${name}` : name
}

function sortEntries(entries: TreeEntry[]): TreeEntry[] {
  return [...entries].sort((left, right) => {
    const leftDirectory = left.type === 'dir'
    const rightDirectory = right.type === 'dir'
    if (leftDirectory !== rightDirectory) return leftDirectory ? -1 : 1
    return left.name.localeCompare(right.name)
  })
}

function FileTree({ repo, ref, current }: { repo: string; ref: string; current: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const cache = useRef(new Map<string, FolderRecord>())
  const controller = useRef(new AbortController())
  const generation = useRef(0)
  const [, renderCache] = useState(0)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const loadFolder = useCallback(
    (path: string) => {
      const key = folderKey(repo, ref, path)
      const cached = cache.current.get(key)
      if (cached?.loading || cached?.entries) return

      const requestGeneration = generation.current
      cache.current.set(key, { loading: true })
      renderCache((value) => value + 1)
      fetchFolderContents(repo, ref, path, controller.current.signal)
        .then(({ entries }) => {
          if (requestGeneration !== generation.current) return
          cache.current.set(key, { entries: sortEntries(entries), loading: false })
          renderCache((value) => value + 1)
        })
        .catch((error) => {
          if (isAbortError(error) || requestGeneration !== generation.current) return
          cache.current.set(key, { error: String(error), loading: false })
          renderCache((value) => value + 1)
        })
    },
    [repo, ref],
  )

  useEffect(() => {
    controller.current.abort()
    const activeController = new AbortController()
    controller.current = activeController
    generation.current++
    for (const [key, record] of cache.current) {
      if (record.loading) cache.current.delete(key)
    }
    setExpanded(new Set())
    return () => {
      activeController.abort()
    }
  }, [repo, ref])

  useEffect(() => {
    const ancestors = ancestorFolders(current)
    setExpanded((value) => {
      const next = new Set(value)
      for (const path of ancestors) next.add(path)
      return next
    })
    for (const path of ['', ...ancestors]) loadFolder(path)
  }, [current, loadFolder])

  const toggle = (path: string) => {
    setExpanded((value) => {
      const next = new Set(value)
      if (next.has(path)) next.delete(path)
      else {
        next.add(path)
        loadFolder(path)
      }
      return next
    })
  }

  const record = cache.current.get(folderKey(repo, ref, ''))
  return (
    <aside
      aria-label="Repository files"
      className={css({
        width: '220px',
        boxSizing: 'border-box',
        flexShrink: 0,
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        position: 'sticky',
        top: '68px',
        maxHeight: 'calc(100vh - 84px)',
        overflowY: 'auto',
        paddingTop: '4px',
        paddingBottom: '4px',
        '@media screen and (max-width: 720px)': {
          width: '100%',
          position: 'static',
          maxHeight: '280px',
        },
      })}
    >
      {record?.loading && <TreeMessage text="Loading files…" />}
      {record?.error && (
        <TreeRetry message={record.error} onRetry={() => loadFolder('')} />
      )}
      {record?.entries?.map((entry) => (
        <TreeRow
          key={`${entry.type}:${entry.name}`}
          entry={entry}
          parentPath=""
          depth={0}
          current={current}
          repo={repo}
          ref={ref}
          expanded={expanded}
          cache={cache.current}
          onToggle={toggle}
          onRetry={loadFolder}
        />
      ))}
    </aside>
  )
}

function TreeRow({
  entry,
  parentPath,
  depth,
  current,
  repo,
  ref,
  expanded,
  cache,
  onToggle,
  onRetry,
}: {
  entry: TreeEntry
  parentPath: string
  depth: number
  current: string
  repo: string
  ref: string
  expanded: Set<string>
  cache: Map<string, FolderRecord>
  onToggle: (path: string) => void
  onRetry: (path: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const path = joinPath(parentPath, entry.name)
  const isDirectory = entry.type === 'dir'
  const isOpen = isDirectory && expanded.has(path)
  const active = !isDirectory && path === current
  const rowStyle = css({
    display: 'flex',
    alignItems: 'center',
    gap: '4px',
    width: '100%',
    boxSizing: 'border-box',
    height: '26px',
    paddingLeft: `${8 + depth * 10}px`,
    paddingRight: '8px',
    border: 'none',
    fontSize: '12.5px',
    fontFamily: 'inherit',
    textAlign: 'left',
    cursor: 'pointer',
    whiteSpace: 'nowrap',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    textDecoration: 'none',
    color: active ? tok.textPrimary : tok.textSecondary,
    fontWeight: active ? 500 : 400,
    backgroundColor: active ? tok.fill : 'transparent',
    boxShadow: active ? `inset 2px 0 0 ${tok.textPrimary}` : 'none',
    ':hover': { backgroundColor: active ? tok.fill : tok.hoverFill },
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
  })

  if (isDirectory) {
    const record = cache.get(folderKey(repo, ref, path))
    return (
      <div>
        <button
          type="button"
          aria-expanded={isOpen}
          onClick={() => onToggle(path)}
          className={rowStyle}
        >
          <span className={css({ display: 'flex', color: tok.textTertiary, flexShrink: 0 })}>
            {isOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </span>
          <span className={css({ overflow: 'hidden', textOverflow: 'ellipsis' })}>{entry.name}</span>
        </button>
        {isOpen && record?.loading && <TreeMessage text="Loading…" depth={depth + 1} />}
        {isOpen && record?.error && (
          <TreeRetry message={record.error} depth={depth + 1} onRetry={() => onRetry(path)} />
        )}
        {isOpen &&
          record?.entries?.map((child) => (
            <TreeRow
              key={`${child.type}:${child.name}`}
              entry={child}
              parentPath={path}
              depth={depth + 1}
              current={current}
              repo={repo}
              ref={ref}
              expanded={expanded}
              cache={cache}
              onToggle={onToggle}
              onRetry={onRetry}
            />
          ))}
      </div>
    )
  }

  const fileParams = { repo, path, ...(ref ? { ref } : {}) }
  return (
    <a href={href('/file', fileParams)} className={rowStyle} aria-current={active ? 'page' : undefined}>
      <span className={css({ width: '7px', height: '7px', borderRadius: '2px', backgroundColor: langColor(path), flexShrink: 0, marginLeft: '2px', marginRight: '2px' })} />
      <span className={css({ overflow: 'hidden', textOverflow: 'ellipsis' })}>{entry.name}</span>
    </a>
  )
}

function TreeMessage({ text, depth = 0 }: { text: string; depth?: number }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div
      role="status"
      className={css({
        height: '26px',
        paddingLeft: `${12 + depth * 10}px`,
        display: 'flex',
        alignItems: 'center',
        fontSize: '12px',
        color: tok.textTertiary,
      })}
    >
      {text}
    </div>
  )
}

function TreeRetry({
  message,
  depth = 0,
  onRetry,
}: {
  message: string
  depth?: number
  onRetry: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <button
      type="button"
      title={message}
      onClick={onRetry}
      className={css({
        width: '100%',
        height: '26px',
        paddingLeft: `${12 + depth * 10}px`,
        border: 'none',
        backgroundColor: 'transparent',
        color: tok.statusRed,
        fontSize: '12px',
        textAlign: 'left',
        cursor: 'pointer',
        ':hover': { backgroundColor: tok.hoverFill },
      })}
    >
      Retry folder
    </button>
  )
}

function Breadcrumb({
  repo,
  path,
  ref,
  pinned,
  meta,
}: {
  repo: string
  path: string
  ref: string
  pinned: boolean
  meta: RepoStatus | null
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const slash = path.lastIndexOf('/')
  const dir = slash === -1 ? '' : path.slice(0, slash + 1)
  const name = slash === -1 ? path : path.slice(slash + 1)
  return (
    <div className={css({ height: '32px', display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px', flexWrap: 'nowrap', '@media screen and (max-width: 720px)': { height: 'auto', minHeight: '32px', flexWrap: 'wrap' } })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '4px', flex: '1 1 240px', fontSize: '13px', minWidth: 0, maxWidth: '100%' })}>
        <a href={href('/search', { q: repoFilter(repo) })} className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textTertiary, textDecoration: 'none', ':hover': { color: tok.textPrimary, textDecoration: 'underline' } })}>
          {repo}
        </a>
        <span className={css({ color: tok.textTertiary })}>/</span>
        {dir && <span className={css({ color: tok.textTertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{dir}</span>}
        <span className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: tok.textPrimary, fontWeight: 600 })}>{name}</span>
        <CopyInline text={path} title="Copy path" size={12} />
      </div>
      {(ref || meta?.indexed_commit_hash) && (
        <span className={css({ display: 'flex', alignItems: 'center', gap: '5px', fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px', color: tok.textSecondary, border: `1px solid ${tok.cardBorder}`, borderRadius: '999px', padding: '2px 9px', whiteSpace: 'nowrap' })}>
          <CommitIcon size={12} />
          {pinned ? `commit · ${ref.slice(0, 7)}` : `${meta?.default_branch ?? 'HEAD'} · ${(ref || meta?.indexed_commit_hash)?.slice(0, 7)}`}
        </span>
      )}
      <button
        type="button"
        onClick={() => navigator.clipboard?.writeText(
          `${window.location.href.split('#')[0]}${href('/file', { repo, path, ...(ref ? { ref } : {}) })}`,
        )}
        className={css(btnStyle(tok))}
      >
        Permalink
      </button>
      <button
        type="button"
        aria-label="Open in search"
        onClick={() => navigate('/search', { q: fileFilter(path) })}
        className={css(btnStyle(tok))}
      >
        <SearchIcon size={13} /> Search
      </button>
      {ref && (
        <button
          type="button"
          onClick={() => navigate('/blame', { repo, path, ref })}
          className={css(btnStyle(tok))}
        >
          Blame
        </button>
      )}
      {ref && (
        <button
          type="button"
          onClick={() => navigate('/history', { repo, path, ref })}
          className={css(btnStyle(tok))}
        >
          <CommitIcon size={13} /> History
        </button>
      )}
    </div>
  )
}

function btnStyle(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    display: 'flex',
    alignItems: 'center',
    gap: '5px',
    height: '28px',
    fontSize: '12.5px',
    fontFamily: 'inherit',
    color: tok.textSecondary,
    backgroundColor: tok.fill,
    border: 'none',
    borderRadius: '7px',
    padding: '0 10px',
    whiteSpace: 'nowrap',
    cursor: 'pointer',
    ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
  }
}

function CodeHeader({ path, content, line, meta }: { path: string; content: string; line: number; meta: RepoStatus | null }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const slash = path.lastIndexOf('/')
  const name = slash === -1 ? path : path.slice(slash + 1)
  const lineCount = content.split('\n').length
  const bytes = new Blob([content]).size
  return (
    <div
      className={css({
        position: 'sticky',
        top: '52px',
        zIndex: 5,
        height: '36px',
        display: 'flex',
        alignItems: 'center',
        gap: '8px',
        paddingLeft: '12px',
        paddingRight: '12px',
        backgroundColor: tok.bandBg,
        borderBottom: `1px solid ${tok.innerSep}`,
        borderTopLeftRadius: '8px',
        borderTopRightRadius: '8px',
        '@media screen and (max-width: 720px)': {
          height: 'auto',
          minHeight: '36px',
        },
      })}
    >
      <span className={css({ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: langColor(path) })} />
      <span className={css({ fontSize: '12.5px', fontWeight: 600, color: tok.textPrimary })}>{name}</span>
      <span className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: '11.5px', color: tok.textTertiary, '@media screen and (max-width: 720px)': { display: 'none' } })}>
        {langName(path)} · {lineCount} lines · {humanSize(bytes)}
        {meta?.indexed_at ? ` · indexed ${relTime(meta.indexed_at)}` : ''}
      </span>
      <div className={css({ flex: 1 })} />
      {line > 0 && (
        <span className={css({ fontFamily: FONTS.MONO, fontSize: '11px', color: tok.selectedText, backgroundColor: tok.selectedLineBg, borderRadius: '5px', padding: '2px 8px' })}>
          L{line}
        </span>
      )}
      <CopyInline text={content} title="Copy file contents" size={13} />
    </div>
  )
}

function CopyInline({ text, title, size = 14 }: { text: string; title: string; size?: number }) {
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
      className={css({ display: 'flex', border: 'none', background: 'none', cursor: 'pointer', color: done ? tok.statusGreen : tok.textTertiary, padding: '3px', borderRadius: '6px', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill } })}
    >
      {done ? <CheckIcon size={size} /> : <CopyIcon size={size} />}
    </button>
  )
}


function CodeViewer({
  content,
  path,
  focusLine,
  selectedLine,
  onPosition,
}: {
  content: string
  path: string
  focusLine: number
  selectedLine: number
  onPosition: (line: number, character: number) => void
}) {
  const [css] = useStyletron()
  const { mode } = useMode()
  const tok = usePhebsTokens()
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!host.current) return
    let cancelled = false
    let view: EditorView | undefined

    // deep-link line: blue background + 2px accent left border (design handoff)
    const focusDeco = Decoration.line({
      attributes: { style: `background:${tok.selectedLineBg}; box-shadow: inset 2px 0 0 ${tok.accent}` },
    })

    languageFor(path).then((lang) => {
      if (cancelled || !host.current) return
      const extensions: Extension[] = [
        lineNumbers(),
        EditorView.editable.of(false),
        EditorState.readOnly.of(true),
        syntaxHighlighting(highlightStyle(mode)),
        EditorView.theme(
          {
            '&': { fontSize: '12.5px', lineHeight: '18px', color: tok.plainCode, backgroundColor: tok.pageBg },
            '.cm-content': { fontFamily: 'ui-monospace, "SF Mono", Menlo, Monaco, monospace', lineHeight: '18px' },
            '.cm-gutters': { backgroundColor: 'transparent', border: 'none', color: tok.gutter },
            '.cm-lineNumbers .cm-gutterElement': { paddingRight: '10px', minWidth: '40px' },
            '.cm-cursor': { display: 'none' },
          },
          { dark: mode === 'dark' },
        ),
      ]
      if (lang) extensions.push(lang)

      const probe = EditorState.create({ doc: content })
      let anchor = -1
      const highlightedLine = selectedLine || focusLine
      if (highlightedLine > 0 && highlightedLine <= probe.doc.lines) {
        anchor = probe.doc.line(highlightedLine).from
        extensions.push(EditorView.decorations.of(Decoration.set([focusDeco.range(anchor)])))
      }

      extensions.push(EditorView.domEventHandlers({
        click(event, activeView) {
          if (!activeView.contentDOM.contains(event.target as Node)) return false
          const offset = activeView.posAtCoords({ x: event.clientX, y: event.clientY })
          if (offset === null) return false
          const sourceLine = activeView.state.doc.lineAt(offset)
          onPosition(sourceLine.number - 1, offset - sourceLine.from)
          return false
        },
      }))

      view = new EditorView({
        state: EditorState.create({ doc: content, extensions }),
        parent: host.current,
      })
      if (anchor >= 0) {
        view.dispatch({ effects: EditorView.scrollIntoView(anchor, { y: 'center' }) })
      }
    })

    return () => {
      cancelled = true
      view?.destroy()
    }
  }, [content, path, focusLine, selectedLine, onPosition, mode, tok])

  return <div ref={host} className={css({ overflow: 'hidden', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' })} />
}
