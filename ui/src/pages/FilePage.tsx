import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, lineNumbers, Decoration } from '@codemirror/view'
import { syntaxHighlighting } from '@codemirror/language'
import { fetchSource, fetchRepoStatus, fetchTree } from '../api'
import type { RepoStatus } from '../api'
import { languageFor, langColor, langName } from '../lang'
import { highlightStyle } from '../highlight'
import { usePhebsTokens, useMode, FONTS } from '../theme'
import { href, navigate } from '../router'
import { CopyIcon, CheckIcon, CommitIcon, SearchIcon, ChevronRight, ChevronDown } from '../icons'
import { humanSize, relTime } from '../util'

// T5.3/T5.5: CodeMirror 6 read-only viewer with breadcrumbs, a sticky
// metadata header, ?L= deep-link line, and syntax highlighting.
export default function FilePage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const path = params.get('path') ?? ''
  const line = Number(params.get('L') ?? '0')
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [binary, setBinary] = useState(false)
  const [meta, setMeta] = useState<RepoStatus | null>(null)

  useEffect(() => {
    setContent(null)
    setError('')
    setBinary(false)
    if (!repo || !path) {
      setError('missing repo or path')
      return
    }
    fetchSource(repo, path)
      .then((f) => {
        if (f.encoding === 'base64') setBinary(true)
        else setContent(f.content)
      })
      .catch((e) => setError(String(e)))
  }, [repo, path])

  useEffect(() => {
    fetchRepoStatus()
      .then((rows) => setMeta(rows.find((r) => r.name === repo) ?? null))
      .catch(() => {})
  }, [repo])

  return (
    <div>
      <Breadcrumb repo={repo} path={path} meta={meta} />
      <div className={css({ display: 'flex', gap: '16px', alignItems: 'flex-start' })}>
        {repo && <FileTree repo={repo} current={path} />}
        <div className={css({ flex: 1, minWidth: 0 })}>
          {error && (
            <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto', marginTop: 0 } } }}>
              {error}
            </Notification>
          )}
          {binary && <div className={css({ color: tok.textTertiary, padding: '24px 0' })}>Binary file — not rendered.</div>}
          {content === null && !error && !binary && <Spinner $size="small" />}
          {content !== null && (
            <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px' })}>
              <CodeHeader path={path} content={content} line={line} meta={meta} />
              <CodeViewer content={content} path={path} focusLine={line} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

export interface TreeNode {
  name: string
  path: string
  dir: boolean
  children: TreeNode[]
}

// exported for tests (T6.4)
export function buildTree(paths: string[]): TreeNode {
  const root: TreeNode = { name: '', path: '', dir: true, children: [] }
  for (const p of paths) {
    const parts = p.split('/')
    let cur = root
    let acc = ''
    parts.forEach((part, i) => {
      acc = acc ? acc + '/' + part : part
      const isFile = i === parts.length - 1
      let child = cur.children.find((c) => c.name === part && c.dir === !isFile)
      if (!child) {
        child = { name: part, path: acc, dir: !isFile, children: [] }
        cur.children.push(child)
      }
      cur = child
    })
  }
  const sort = (n: TreeNode) => {
    n.children.sort((a, b) => (a.dir !== b.dir ? (a.dir ? -1 : 1) : a.name.localeCompare(b.name)))
    n.children.forEach(sort)
  }
  sort(root)
  return root
}

// FileTree renders the repo's file tree over /api/tree, with the path to the
// current file expanded. Dirs collapse/expand; files navigate the viewer.
function FileTree({ repo, current }: { repo: string; current: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [root, setRoot] = useState<TreeNode | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    let live = true
    fetchTree(repo)
      .then((r) => {
        if (!live) return
        setRoot(buildTree(r.paths))
        // expand every ancestor dir of the current file
        const anc = new Set<string>()
        const parts = current.split('/')
        let acc = ''
        for (let i = 0; i < parts.length - 1; i++) {
          acc = acc ? acc + '/' + parts[i] : parts[i]
          anc.add(acc)
        }
        setExpanded(anc)
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [repo, current])

  const toggle = (p: string) =>
    setExpanded((s) => {
      const n = new Set(s)
      n.has(p) ? n.delete(p) : n.add(p)
      return n
    })

  if (!root) return null
  return (
    <aside
      className={css({
        width: '240px',
        flexShrink: 0,
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        position: 'sticky',
        top: '72px',
        maxHeight: 'calc(100vh - 96px)',
        overflowY: 'auto',
        paddingTop: '6px',
        paddingBottom: '6px',
      })}
    >
      {root.children.map((c) => (
        <TreeRow key={c.path} node={c} depth={0} current={current} repo={repo} expanded={expanded} toggle={toggle} />
      ))}
    </aside>
  )
}

function TreeRow({
  node,
  depth,
  current,
  repo,
  expanded,
  toggle,
}: {
  node: TreeNode
  depth: number
  current: string
  repo: string
  expanded: Set<string>
  toggle: (p: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const isOpen = expanded.has(node.path)
  const active = !node.dir && node.path === current
  const rowStyle = css({
    display: 'flex',
    alignItems: 'center',
    gap: '4px',
    height: '28px',
    paddingLeft: `${8 + depth * 12}px`,
    paddingRight: '8px',
    fontSize: '13px',
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
  })

  if (node.dir) {
    return (
      <div>
        <div className={rowStyle} onClick={() => toggle(node.path)}>
          <span className={css({ display: 'flex', color: tok.textTertiary, flexShrink: 0 })}>
            {isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          </span>
          <span className={css({ overflow: 'hidden', textOverflow: 'ellipsis' })}>{node.name}</span>
        </div>
        {isOpen &&
          node.children.map((c) => (
            <TreeRow key={c.path} node={c} depth={depth + 1} current={current} repo={repo} expanded={expanded} toggle={toggle} />
          ))}
      </div>
    )
  }
  return (
    <a href={href('/file', { repo, path: node.path })} className={rowStyle}>
      <span className={css({ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: langColor(node.path), flexShrink: 0, marginLeft: '2px', marginRight: '2px' })} />
      <span className={css({ overflow: 'hidden', textOverflow: 'ellipsis' })}>{node.name}</span>
    </a>
  )
}

function Breadcrumb({ repo, path, meta }: { repo: string; path: string; meta: RepoStatus | null }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const short = repo.slice(repo.lastIndexOf('/') + 1)
  const slash = path.lastIndexOf('/')
  const dir = slash === -1 ? '' : path.slice(0, slash + 1)
  const name = slash === -1 ? path : path.slice(slash + 1)
  return (
    <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '12px', flexWrap: 'wrap' })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '14px', minWidth: 0 })}>
        <a href={href('/search', { q: `repo:${short}` })} className={css({ color: tok.textTertiary, textDecoration: 'none', ':hover': { color: tok.textPrimary, textDecoration: 'underline' } })}>
          {repo}
        </a>
        <span className={css({ color: tok.textTertiary })}>/</span>
        {dir && <span className={css({ color: tok.textTertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{dir}</span>}
        <span className={css({ color: tok.textPrimary, fontWeight: 600 })}>{name}</span>
        <CopyInline text={path} title="Copy path" />
      </div>
      <div className={css({ flex: 1 })} />
      {meta?.indexed_commit_hash && (
        <span className={css({ display: 'flex', alignItems: 'center', gap: '5px', fontFamily: FONTS.MONO, fontSize: '12px', color: tok.textSecondary, border: `1px solid ${tok.cardBorder}`, borderRadius: '999px', padding: '3px 10px' })}>
          <CommitIcon size={13} />
          {meta.default_branch ?? 'HEAD'} · {meta.indexed_commit_hash.slice(0, 7)}
        </span>
      )}
      <button
        onClick={() => navigator.clipboard?.writeText(window.location.href)}
        className={css(btnStyle(tok))}
      >
        Copy permalink
      </button>
      <button
        onClick={() => navigate('/search', { q: `file:${path.slice(slash + 1)}` })}
        className={css(btnStyle(tok))}
      >
        <SearchIcon size={13} /> Open in search
      </button>
    </div>
  )
}

function btnStyle(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    display: 'flex',
    alignItems: 'center',
    gap: '5px',
    fontSize: '13px',
    color: tok.textSecondary,
    backgroundColor: tok.fill,
    border: 'none',
    borderRadius: '8px',
    padding: '6px 12px',
    cursor: 'pointer',
    ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
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
        top: '56px',
        zIndex: 5,
        height: '44px',
        display: 'flex',
        alignItems: 'center',
        gap: '8px',
        paddingLeft: '12px',
        paddingRight: '12px',
        backgroundColor: tok.pageBg,
        borderBottom: `1px solid ${tok.innerSep}`,
        borderTopLeftRadius: '8px',
        borderTopRightRadius: '8px',
      })}
    >
      <span className={css({ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: langColor(path) })} />
      <span className={css({ fontSize: '13px', fontWeight: 600, color: tok.textPrimary })}>{name}</span>
      <span className={css({ fontSize: '12px', color: tok.textTertiary })}>
        {langName(path)} · {lineCount} lines · {humanSize(bytes)}
        {meta?.indexed_at ? ` · indexed ${relTime(meta.indexed_at)}` : ''}
      </span>
      <div className={css({ flex: 1 })} />
      {line > 0 && (
        <span className={css({ fontFamily: FONTS.MONO, fontSize: '12px', color: tok.selectedText, backgroundColor: tok.selectedLineBg, borderRadius: '6px', padding: '2px 8px' })}>
          L{line}
        </span>
      )}
      <CopyInline text={content} title="Copy file contents" />
    </div>
  )
}

function CopyInline({ text, title }: { text: string; title: string }) {
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
      {done ? <CheckIcon size={14} /> : <CopyIcon size={14} />}
    </button>
  )
}


function CodeViewer({
  content,
  path,
  focusLine,
}: {
  content: string
  path: string
  focusLine: number
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
            '&': { fontSize: '13px', color: tok.plainCode, backgroundColor: tok.pageBg },
            '.cm-content': { fontFamily: 'ui-monospace, "SF Mono", Menlo, Monaco, monospace' },
            '.cm-gutters': { backgroundColor: 'transparent', border: 'none', color: tok.gutter },
            '.cm-lineNumbers .cm-gutterElement': { paddingRight: '12px', minWidth: '44px' },
            '.cm-cursor': { display: 'none' },
          },
          { dark: mode === 'dark' },
        ),
      ]
      if (lang) extensions.push(lang)

      const probe = EditorState.create({ doc: content })
      let anchor = -1
      if (focusLine > 0 && focusLine <= probe.doc.lines) {
        anchor = probe.doc.line(focusLine).from
        extensions.push(EditorView.decorations.of(Decoration.set([focusDeco.range(anchor)])))
      }

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
  }, [content, path, focusLine, mode, tok])

  return <div ref={host} className={css({ overflow: 'hidden', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' })} />
}
