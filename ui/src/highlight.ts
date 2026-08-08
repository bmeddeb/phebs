import { HighlightStyle } from '@codemirror/language'
import { tags as t, highlightTree } from '@lezer/highlight'
import type { LanguageSupport } from '@codemirror/language'
import type { Mode } from './theme'
import { DEFAULT_PALETTE, PALETTES, type PaletteName, type RoleStyle } from './palette'

// T44.2: role colors come from the curated palette registry (palette.ts),
// selected product-wide via the Appearance preference. Used both by the
// CodeMirror viewer (FilePage) and by chunk rendering (search results,
// citations).

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

function styleFor(mode: Mode, palette: PaletteName): Record<string, RoleStyle> {
  return PALETTES[palette][mode]
}

export function highlightStyle(mode: Mode, palette: PaletteName = DEFAULT_PALETTE): HighlightStyle {
  const p = styleFor(mode, palette)
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
export function tokenize(line: string, lang: LanguageSupport | null, mode: Mode, palette: PaletteName = DEFAULT_PALETTE): Token[] {
  if (!lang) return [{ from: 0, to: line.length }]
  const p = styleFor(mode, palette)
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
