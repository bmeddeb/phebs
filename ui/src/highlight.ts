import { HighlightStyle } from '@codemirror/language'
import { tags as t, highlightTree } from '@lezer/highlight'
import type { LanguageSupport } from '@codemirror/language'
import type { Mode } from './theme'

// Syntax palette mapped from Base Web primitives (design handoff). Used both
// by the CodeMirror viewer (FilePage) and by search-result chunk rendering.
const LIGHT: Record<string, { color: string; fontStyle?: string }> = {
  keyword: { color: '#7C3EC3' },
  func: { color: '#175BCC' },
  type: { color: '#016974' },
  string: { color: '#166C3B' },
  comment: { color: '#868686', fontStyle: 'italic' },
  number: { color: '#A95F03' },
  operator: { color: '#5E5E5E' },
}
const DARK: Record<string, { color: string; fontStyle?: string }> = {
  keyword: { color: '#BDA7E4' },
  func: { color: '#93B4EE' },
  type: { color: '#72C1CD' },
  string: { color: '#8FC19C' },
  comment: { color: '#717171', fontStyle: 'italic' },
  number: { color: '#DEA85E' },
  operator: { color: '#ABABAB' },
}

// role assigns each Lezer tag group a palette role; one place drives both the
// CM6 HighlightStyle and the standalone tokenizer.
const roleTags: Record<string, unknown[]> = {
  keyword: [t.keyword, t.modifier, t.moduleKeyword, t.controlKeyword, t.operatorKeyword, t.definitionKeyword],
  func: [t.function(t.variableName), t.function(t.propertyName)],
  type: [t.typeName, t.className, t.standard(t.name), t.namespace],
  string: [t.string, t.special(t.string), t.regexp],
  comment: [t.comment, t.lineComment, t.blockComment, t.docComment],
  number: [t.number, t.atom, t.bool, t.null, t.integer, t.float],
  operator: [t.operator, t.punctuation, t.derefOperator, t.separator],
}

function styleFor(mode: Mode): Record<string, { color: string; fontStyle?: string }> {
  return mode === 'dark' ? DARK : LIGHT
}

export function highlightStyle(mode: Mode): HighlightStyle {
  const p = styleFor(mode)
  const specs = Object.entries(roleTags).map(([role, tags]) => ({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tag: tags as any,
    ...p[role],
  }))
  return HighlightStyle.define(specs)
}

// A styletron-agnostic HighlightStyle exposes classes; we instead tokenize
// standalone chunks (no editor) and emit inline-styled spans. The lezer
// Highlighter interface wants a `style(tags)` returning a class name; we map
// tag → role → color via a lookup built from roleTags.
export interface Token {
  from: number
  to: number
  color?: string
  fontStyle?: string
}

// tokenizeLine highlights a single source line using a CM6 language pack,
// returning colored spans in line-local coordinates. Falls back to one plain
// token when no language is given.
export function tokenize(line: string, lang: LanguageSupport | null, mode: Mode): Token[] {
  if (!lang) return [{ from: 0, to: line.length }]
  const p = styleFor(mode)
  const tree = lang.language.parser.parse(line)
  const style = {
    // highlightTree calls style(tags) per node; resolve the first tag that
    // maps to a role.
    style(tags: readonly unknown[]): string | null {
      for (const [role, roleTagList] of Object.entries(roleTags)) {
        if (tags.some((tag) => roleTagList.includes(tag))) return role
      }
      return null
    },
  }
  const tokens: Token[] = []
  let pos = 0
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  highlightTree(tree, style as any, (from, to, role) => {
    if (from > pos) tokens.push({ from: pos, to: from })
    tokens.push({ from, to, ...p[role] })
    pos = to
  })
  if (pos < line.length) tokens.push({ from: pos, to: line.length })
  return tokens.length ? tokens : [{ from: 0, to: line.length }]
}
