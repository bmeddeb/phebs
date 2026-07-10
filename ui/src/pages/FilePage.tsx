import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { LabelMedium, ParagraphSmall } from 'baseui/typography'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, lineNumbers, Decoration } from '@codemirror/view'
import { syntaxHighlighting } from '@codemirror/language'
import { fetchSource } from '../api'
import { languageFor } from '../lang'
import { highlightStyle } from '../highlight'
import { usePhebsTokens, useMode } from '../theme'

// T5.3: CodeMirror 6 read-only viewer with line anchors (?L=42 deep links)
// and language by extension.
export default function FilePage({ params }: { params: URLSearchParams }) {
  const repo = params.get('repo') ?? ''
  const path = params.get('path') ?? ''
  const line = Number(params.get('L') ?? '0')
  const [css, theme] = useStyletron()
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [binary, setBinary] = useState(false)

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

  return (
    <div>
      <div className={css({ marginBottom: theme.sizing.scale500 })}>
        <LabelMedium>
          {repo}
          <span className={css({ color: theme.colors.contentTertiary })}> / </span>
          {path}
        </LabelMedium>
      </div>
      {error && (
        <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
          {error}
        </Notification>
      )}
      {binary && <ParagraphSmall>Binary file — not rendered.</ParagraphSmall>}
      {content === null && !error && !binary && <Spinner $size="small" />}
      {content !== null && <CodeViewer content={content} path={path} focusLine={line} />}
    </div>
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

  return (
    <div
      ref={host}
      className={css({
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        overflow: 'hidden',
      })}
    />
  )
}
