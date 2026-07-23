import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { fetchFolderContents } from './api'
import type { RepoStatus, TreeEntry } from './api'
import { langColor } from './lang'
import { href } from './router'
import { usePhebsTokens, FONTS } from './theme'
import { ancestorFolders, isAbortError } from './util'
import { ChevronDown, ChevronRight } from './icons'

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

export function RepositoryTree({
  repo,
  ref,
  current = '',
  standalone = true,
}: {
  repo: string
  ref: string
  current?: string
  standalone?: boolean
}) {
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
    return () => activeController.abort()
  }, [repo, ref])

  useEffect(() => {
    if (!repo || !ref) return
    const ancestors = ancestorFolders(current)
    setExpanded((value) => {
      const next = new Set(value)
      for (const path of ancestors) next.add(path)
      return next
    })
    for (const path of ['', ...ancestors]) loadFolder(path)
  }, [repo, ref, current, loadFolder])

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
    <nav
      aria-label="Repository files"
      className={css({
        width: standalone ? '220px' : '100%',
        boxSizing: 'border-box',
        flexShrink: 0,
        border: standalone ? `1px solid ${tok.cardBorder}` : 'none',
        borderRadius: standalone ? '8px' : 0,
        position: standalone ? 'sticky' : 'static',
        top: standalone ? '68px' : undefined,
        maxHeight: standalone ? 'calc(100vh - 84px)' : 'none',
        overflowY: standalone ? 'auto' : 'visible',
        paddingTop: '4px',
        paddingBottom: '4px',
        '@media screen and (max-width: 720px)': standalone ? {
          width: '100%',
          position: 'static',
          maxHeight: '280px',
        } : {},
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
      {!record?.loading && !record?.error && record?.entries?.length === 0 && (
        <TreeMessage text="Repository is empty." />
      )}
    </nav>
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
        minHeight: '26px',
        boxSizing: 'border-box',
        paddingLeft: `${12 + depth * 10}px`,
        paddingRight: '8px',
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
        minHeight: '26px',
        paddingLeft: `${12 + depth * 10}px`,
        paddingRight: '8px',
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

export default function RepositoryBrowser({
  repositories,
  loading,
  error,
  onRetry,
  onInsertFilter,
}: {
  repositories: RepoStatus[]
  loading: boolean
  error: string
  onRetry: () => void
  onInsertFilter: (repo: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [selected, setSelected] = useState('')
  const sorted = useMemo(
    () => [...repositories].sort((left, right) => left.name.localeCompare(right.name)),
    [repositories],
  )

  const selectedName = sorted.some((repository) => repository.name === selected)
    ? selected
    : sorted.find((repository) => repository.indexed_commit_hash)?.name ?? sorted[0]?.name ?? ''
  const active = sorted.find((repository) => repository.name === selectedName)
  const body = (
    <>
      {loading && sorted.length === 0 && <ExplorerMessage>Loading repositories…</ExplorerMessage>}
      {error && sorted.length === 0 && (
        <button
          type="button"
          onClick={onRetry}
          className={css({
            width: '100%',
            border: 'none',
            padding: '12px',
            backgroundColor: 'transparent',
            color: tok.statusRed,
            textAlign: 'left',
            cursor: 'pointer',
          })}
        >
          Retry repositories
        </button>
      )}
      {!loading && !error && sorted.length === 0 && (
        <ExplorerMessage>No visible repositories.</ExplorerMessage>
      )}
      {sorted.length > 0 && (
        <>
          <div className={css({ display: 'grid', gap: '8px', padding: '10px', borderBottom: `1px solid ${tok.innerSep}` })}>
            <label className={css({ display: 'grid', gap: '5px', fontSize: '10px', fontWeight: 600, letterSpacing: '0.05em', textTransform: 'uppercase', color: tok.textTertiary })}>
              Repository
              <select
                aria-label="Repository"
                value={selectedName}
                onChange={(event) => setSelected(event.currentTarget.value)}
                className={css({
                  width: '100%',
                  height: '32px',
                  boxSizing: 'border-box',
                  border: `1px solid ${tok.cardBorder}`,
                  borderRadius: '6px',
                  paddingLeft: '8px',
                  paddingRight: '8px',
                  backgroundColor: tok.pageBg,
                  color: tok.textPrimary,
                  fontFamily: FONTS.SANS,
                  fontSize: '12px',
                })}
              >
                {sorted.map((repository) => (
                  <option key={repository.name} value={repository.name}>{repository.name}</option>
                ))}
              </select>
            </label>
            <button
              type="button"
              onClick={() => active && onInsertFilter(active.name)}
              disabled={!active}
              className={css({
                height: '28px',
                border: `1px solid ${tok.cardBorder}`,
                borderRadius: '6px',
                backgroundColor: 'transparent',
                color: tok.textSecondary,
                fontSize: '11.5px',
                cursor: active ? 'pointer' : 'default',
                ':hover': active ? { backgroundColor: tok.hoverFill, color: tok.textPrimary } : {},
                ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
              })}
            >
              Add repository filter
            </button>
          </div>
          {active?.indexed_commit_hash ? (
            <RepositoryTree repo={active.name} ref={active.indexed_commit_hash} standalone={false} />
          ) : (
            <ExplorerMessage>Repository is not indexed yet.</ExplorerMessage>
          )}
        </>
      )}
    </>
  )

  return (
    <section
      aria-label="Repository explorer"
      className={css({
        width: '260px',
        flexShrink: 0,
        '@media screen and (max-width: 720px)': { width: '100%' },
      })}
    >
      <button
        type="button"
        aria-label="Browse repositories"
        aria-expanded={mobileOpen}
        aria-controls="search-repository-explorer"
        onClick={() => setMobileOpen((value) => !value)}
        className={css({
          display: 'none',
          width: '100%',
          height: '38px',
          boxSizing: 'border-box',
          alignItems: 'center',
          justifyContent: 'space-between',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '8px',
          paddingLeft: '12px',
          paddingRight: '12px',
          backgroundColor: 'transparent',
          color: tok.textPrimary,
          fontSize: '12.5px',
          cursor: 'pointer',
          '@media screen and (max-width: 720px)': { display: 'flex' },
        })}
      >
        Browse repositories
        {mobileOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
      </button>
      <aside
        id="search-repository-explorer"
        data-mobile-open={mobileOpen ? 'true' : 'false'}
        className={css({
          position: 'sticky',
          top: '68px',
          maxHeight: 'calc(100vh - 84px)',
          overflowY: 'auto',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '8px',
          backgroundColor: tok.pageBg,
          '@media screen and (max-width: 720px)': {
            display: mobileOpen ? 'block' : 'none',
            position: 'static',
            maxHeight: '360px',
            marginTop: '8px',
          },
        })}
      >
        <div className={css({ height: '36px', display: 'flex', alignItems: 'center', paddingLeft: '10px', paddingRight: '10px', borderBottom: `1px solid ${tok.innerSep}`, backgroundColor: tok.bandBg, fontSize: '12.5px', fontWeight: 600, color: tok.textPrimary })}>
          Repository explorer
        </div>
        {body}
      </aside>
    </section>
  )
}

function ExplorerMessage({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ padding: '12px', fontSize: '12px', lineHeight: '18px', color: tok.textTertiary })}>
      {children}
    </div>
  )
}
