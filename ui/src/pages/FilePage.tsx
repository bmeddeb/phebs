import { useCallback, useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, lineNumbers, Decoration } from '@codemirror/view'
import { syntaxHighlighting } from '@codemirror/language'
import {
  fetchDefinition,
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
} from '../api'
import { languageFor, langColor, langName } from '../lang'
import { highlightStyle } from '../highlight'
import { usePhebsTokens, useMode, usePalette, FONTS } from '../theme'
import type { MarkdownSegment } from '../markdown'
import { href, navigate } from '../router'
import { CopyIcon, CheckIcon, CommitIcon, SearchIcon } from '../icons'
import { fileFilter, humanSize, isAbortError, relTime, repoFilter } from '../util'
import { RepositoryTree } from '../RepositoryBrowser'

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
  // T44.3: markdown files offer a rendered preview. Source is the default;
  // a line deep-link is a source concept, so ?L= forces source even under
  // ?view=preview (line numbers have no meaning in rendered prose).
  const markdown = isMarkdownPath(path)
  const preview = markdown && params.get('view') === 'preview' && line <= 0
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
        {repo && effectiveRef && <RepositoryTree repo={repo} ref={effectiveRef} current={path} />}
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
                <CodeHeader path={path} content={content} line={line} meta={meta} markdown={markdown} preview={preview} repo={repo} ref={effectiveRef} pinned={Boolean(ref)} />
                {preview ? (
                  <MarkdownPreview content={content} />
                ) : (
                  <CodeViewer
                    content={content}
                    path={path}
                    focusLine={line}
                    selectedLine={navPosition ? navPosition.line + 1 : 0}
                    onPosition={selectPosition}
                  />
                )}
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

function CodeHeader({ path, content, line, meta, markdown, preview, repo, ref, pinned }: {
  path: string
  content: string
  line: number
  meta: RepoStatus | null
  markdown: boolean
  preview: boolean
  repo: string
  ref: string
  pinned: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const slash = path.lastIndexOf('/')
  const name = slash === -1 ? path : path.slice(slash + 1)
  const lineCount = content.split('\n').length
  const bytes = new Blob([content]).size
  const viewHref = (view: 'source' | 'preview') => href('/file', {
    repo, path, ...(pinned ? { ref } : {}), ...(view === 'preview' ? { view: 'preview' } : {}),
  })
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
      <span className={css({ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: langColor(path, tok) })} />
      <span className={css({ fontSize: '12.5px', fontWeight: 600, color: tok.textPrimary })}>{name}</span>
      <span className={css({ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: '11.5px', color: tok.textTertiary, '@media screen and (max-width: 720px)': { display: 'none' } })}>
        {langName(path)} · {lineCount} lines · {humanSize(bytes)}
        {meta?.indexed_at ? <span title={new Date(meta.indexed_at).toLocaleString()} aria-label={`indexed ${relTime(meta.indexed_at)} (${new Date(meta.indexed_at).toLocaleString()})`}>{` · indexed ${relTime(meta.indexed_at)}`}</span> : ''}
      </span>
      <div className={css({ flex: 1 })} />
      {markdown && (
        <div role="group" aria-label="Markdown view" className={css({ display: 'inline-flex', border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', overflow: 'hidden' })}>
          <ViewTab href={viewHref('source')} label="Markdown" active={!preview} />
          <ViewTab href={viewHref('preview')} label="Preview" active={preview} />
        </div>
      )}
      {line > 0 && (
        <span className={css({ fontFamily: FONTS.MONO, fontSize: '11px', color: tok.selectedText, backgroundColor: tok.selectedLineBg, borderRadius: '5px', padding: '2px 8px' })}>
          L{line}
        </span>
      )}
      <CopyInline text={content} title="Copy file contents" size={13} />
    </div>
  )
}

function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown)$/i.test(path)
}

// Reading-surface prose (charter Read mode): a comfortable measure, clear
// heading rhythm, and code that stays monospace and tinted. Emitted as a
// single stylesheet scoped under a fixed wrapper class and rendered inline
// with the preview — styletron is an atomic engine that turns descendant
// keys into GLOBAL bare-tag rules (h1 {…}, a {…}), which would restyle the
// whole app; a scoped <style> avoids that entirely (T44.3f P1). Every value
// is a design-token constant, never user input, so the string is inert.
const MARKDOWN_PROSE_CLASS = 'phebs-md'

function markdownProseCss(tok: ReturnType<typeof usePhebsTokens>): string {
  const s = MARKDOWN_PROSE_CLASS
  return [
    `.${s}{padding:20px 22px;max-width:760px;color:${tok.textPrimary};font-size:14px;line-height:1.65;overflow-wrap:anywhere}`,
    `.${s}>:first-child{margin-top:0}`,
    `.${s} h1{font-size:24px;line-height:1.3;font-weight:600;margin:28px 0 12px;letter-spacing:-0.02em}`,
    `.${s} h2{font-size:19px;line-height:1.35;font-weight:600;margin:24px 0 10px;padding-bottom:5px;border-bottom:1px solid ${tok.innerSep}}`,
    `.${s} h3{font-size:16px;font-weight:600;margin:20px 0 8px}`,
    `.${s} h4{font-size:14px;font-weight:600;margin:18px 0 6px}`,
    `.${s} h5{font-size:13px;font-weight:600;margin:16px 0 6px}`,
    `.${s} h6{font-size:12px;font-weight:600;color:${tok.textSecondary};margin:16px 0 6px}`,
    `.${s} p{margin:0 0 14px}`,
    `.${s} a{color:${tok.selectedText};text-decoration:underline}`,
    `.${s} ul,.${s} ol{margin:0 0 14px;padding-left:24px}`,
    `.${s} li{margin:3px 0}`,
    `.${s} blockquote{margin:0 0 14px;padding-left:14px;border-left:3px solid ${tok.cardBorder};color:${tok.textSecondary}}`,
    `.${s} code{font-family:${FONTS.MONO};font-size:12.5px;background-color:${tok.fill};border-radius:4px;padding:1px 5px;color:${tok.plainCode}}`,
    `.${s} pre{margin:0 0 14px;padding:12px 14px;background-color:${tok.bandBg};border:1px solid ${tok.innerSep};border-radius:8px;overflow-x:auto}`,
    `.${s} pre code{background-color:transparent;padding:0;font-size:12px;line-height:1.6}`,
    `.${s} hr{border:none;border-top:1px solid ${tok.innerSep};margin:20px 0}`,
    `.${s} table{border-collapse:collapse;margin:0 0 14px;font-size:13px;display:block;overflow-x:auto}`,
    `.${s} th,.${s} td{border:1px solid ${tok.cardBorder};padding:6px 10px;text-align:left}`,
    `.${s} th{background-color:${tok.bandBg};font-weight:600}`,
  ].join('')
}

// /api/source admits up to 10 MiB; marked + DOMPurify run synchronously on
// the main thread, so an adversarial large document is bounded here (T44.3f
// P1) before any render work — 128K UTF-16 units comfortably covers real
// docs while capping the worst case. Over the bound, the source is offered
// instead of freezing the tab.
const MARKDOWN_PREVIEW_MAX_UNITS = 131_072

// T44.4f: aggregate diagram bound. The 128 KiB document limit still admits
// thousands of tiny fences, each of which would import the renderer and
// queue a full ELK render (uncancellable, re-queued on theme change). Only
// the first N fences render; the rest stay source and never touch mermaid,
// so an adversarial document cannot wedge the tab.
const MAX_RENDERED_DIAGRAMS = 20

function ViewTab({ href, label, active }: { href: string; label: string; active: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <a
      href={href}
      aria-current={active ? 'true' : undefined}
      className={css({
        padding: '3px 10px',
        fontSize: '11px',
        lineHeight: '18px',
        fontWeight: active ? 600 : 400,
        textDecoration: 'none',
        color: active ? tok.pageBg : tok.textSecondary,
        backgroundColor: active ? tok.textPrimary : 'transparent',
        ':hover': active ? {} : { backgroundColor: tok.hoverFill, color: tok.textPrimary },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
      })}
    >
      {label}
    </a>
  )
}

// T44.3: rendered markdown. The renderer (marked + DOMPurify) is the trust
// boundary and lives in its own lazy chunk — imported only when a preview
// actually renders, so the initial bundle never carries it. A render
// failure falls back to a bounded notice, never a blank or raw HTML.
function MarkdownPreview({ content }: { content: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  // The bound is checked before the lazy import, so an oversized document
  // never touches marked/DOMPurify on the main thread.
  const tooLarge = content.length > MARKDOWN_PREVIEW_MAX_UNITS
  const [segments, setSegments] = useState<MarkdownSegment[] | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    if (tooLarge) return
    let active = true
    setSegments(null)
    setFailed(false)
    void import('../markdown')
      .then((mod) => {
        if (active) setSegments(mod.segmentMarkdown(content))
      })
      .catch(() => {
        if (active) setFailed(true)
      })
    return () => { active = false }
  }, [content, tooLarge])
  if (tooLarge) {
    return <div role="status" className={css({ padding: '16px', color: tok.textSecondary, fontSize: '12px', lineHeight: '18px' })}>This document is {Math.round(content.length / 1024)} KB; the preview is bounded to {MARKDOWN_PREVIEW_MAX_UNITS / 1024} KB. Switch to Markdown to read the full source.</div>
  }
  if (failed) {
    return <div role="alert" className={css({ padding: '16px', color: tok.status.conflict.text, fontSize: '12px' })}>The preview could not be rendered. Switch to Markdown to read the source.</div>
  }
  if (segments === null) {
    return <div role="status" className={css({ padding: '16px', color: tok.textSecondary, fontSize: '12px' })}>Rendering preview…</div>
  }
  return (
    <>
      {/* Scoped, inert stylesheet (see markdownProseCss) — the class scopes
          every rule to the preview subtree (T44.3f). */}
      <style>{markdownProseCss(tok)}</style>
      {(() => {
        let diagram = 0
        return segments.map((segment, index) => {
          if (segment.kind === 'prose') {
            return (
              <div
                key={index}
                className={MARKDOWN_PROSE_CLASS}
                // eslint-disable-next-line react/no-danger
                dangerouslySetInnerHTML={{ __html: segment.html }}
              />
            )
          }
          const render = diagram < MAX_RENDERED_DIAGRAMS
          diagram += 1
          return <MermaidFence key={index} source={segment.source} render={render} />
        })
      })()}
    </>
  )
}

// T44.4: one mermaid fence. The fence source shows as a code block first —
// that is also the loading state — and the diagram replaces it when the
// lazy renderer (mermaid + ELK, one async chunk fetched only because this
// fence exists) succeeds. A failing fence keeps the source visible with a
// one-line error above it: never a blank.
function MermaidFence({ source, render }: { source: string; render: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { mode } = useMode()
  const [svg, setSvg] = useState<string | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    // Beyond the aggregate cap the fence stays source and never imports the
    // renderer (T44.4f) — this is what bounds an adversarial fence flood.
    if (!render) {
      setError(`preview renders at most ${MAX_RENDERED_DIAGRAMS} diagrams; this one is shown as source`)
      return
    }
    let active = true
    setSvg(null)
    setError('')
    void import('../mermaid')
      .then((mod) => mod.renderMermaid(source, mode, tok))
      .then((rendered) => {
        if (active) setSvg(rendered)
      })
      .catch((cause) => {
        if (active) setError(String(cause).replace(/^Error:\s*/, '').split('\n')[0].slice(0, 200) || 'diagram failed to render')
      })
    return () => { active = false }
  }, [source, mode, tok, render])
  if (svg !== null && error === '') {
    return (
      <div
        role="img"
        aria-label="Mermaid diagram"
        className={css({ margin: '0 0 14px', maxWidth: '760px', padding: '14px', border: `1px solid ${tok.innerSep}`, borderRadius: '8px', overflowX: 'auto', backgroundColor: tok.pageBg })}
        // Mermaid strict-mode output (labels escaped, no click bindings,
        // no HTML labels) — the documented boundary, recorded in PLAN.md.
        // eslint-disable-next-line react/no-danger
        dangerouslySetInnerHTML={{ __html: svg }}
      />
    )
  }
  return (
    <div className={css({ margin: '0 0 14px', maxWidth: '760px' })}>
      {error !== '' && (
        <div role="alert" className={css({ marginBottom: '6px', fontSize: '11px', lineHeight: '16px', color: tok.status.conflict.text })}>
          Diagram not rendered: {error}
        </div>
      )}
      <pre className={css({ margin: 0, padding: '12px 14px', backgroundColor: tok.bandBg, border: `1px solid ${tok.innerSep}`, borderRadius: '8px', overflowX: 'auto', fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '1.6', color: tok.plainCode })}>{source}</pre>
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
      className={css({ display: 'flex', border: 'none', background: 'none', cursor: 'pointer', color: done ? tok.status.current.solid : tok.textTertiary, padding: '3px', borderRadius: '6px', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill } })}
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
  const { palette } = usePalette()
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
        syntaxHighlighting(highlightStyle(mode, palette)),
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
  }, [content, path, focusLine, selectedLine, onPosition, mode, palette, tok])

  return <div ref={host} className={css({ overflow: 'hidden', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' })} />
}
