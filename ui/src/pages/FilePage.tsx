import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { LabelMedium, ParagraphSmall } from 'baseui/typography'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, lineNumbers, Decoration } from '@codemirror/view'
import type { LanguageSupport } from '@codemirror/language'
import { fetchSource } from '../api'

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

const focusLineDeco = Decoration.line({ attributes: { style: 'background: #FFEA8A66' } })

function CodeViewer({
  content,
  path,
  focusLine,
}: {
  content: string
  path: string
  focusLine: number
}) {
  const [css, theme] = useStyletron()
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!host.current) return
    let cancelled = false
    let view: EditorView | undefined

    languageFor(path).then((lang) => {
      if (cancelled || !host.current) return
      const extensions: Extension[] = [
        lineNumbers(),
        EditorView.editable.of(false),
        EditorState.readOnly.of(true),
        EditorView.theme({
          '&': { fontSize: '13px' },
          '.cm-gutters': { backgroundColor: 'transparent', border: 'none' },
        }),
      ]
      if (lang) extensions.push(lang)

      // deep link (?L=42): static line decoration + scroll on mount
      const probe = EditorState.create({ doc: content })
      let anchor = -1
      if (focusLine > 0 && focusLine <= probe.doc.lines) {
        anchor = probe.doc.line(focusLine).from
        extensions.push(EditorView.decorations.of(Decoration.set([focusLineDeco.range(anchor)])))
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
  }, [content, path, focusLine])

  return (
    <div
      ref={host}
      className={css({
        border: `1px solid ${theme.colors.borderOpaque}`,
        borderRadius: theme.borders.radius300,
        overflow: 'hidden',
      })}
    />
  )
}

// languageFor lazily loads the CM6 language pack for the file extension so
// language bundles stay out of the initial chunk.
async function languageFor(path: string): Promise<LanguageSupport | null> {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  switch (ext) {
    case 'go':
      return (await import('@codemirror/lang-go')).go()
    case 'js':
    case 'jsx':
    case 'ts':
    case 'tsx':
      return (await import('@codemirror/lang-javascript')).javascript({
        jsx: ext.endsWith('x'),
        typescript: ext.startsWith('t'),
      })
    case 'py':
      return (await import('@codemirror/lang-python')).python()
    case 'md':
    case 'markdown':
      return (await import('@codemirror/lang-markdown')).markdown()
    case 'json':
      return (await import('@codemirror/lang-json')).json()
    default:
      return null
  }
}
