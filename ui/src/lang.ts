import { LanguageSupport, StreamLanguage } from '@codemirror/language'
import type { StreamParser } from '@codemirror/language'
import type { PhebsTokens } from './theme'

// languageFor lazily loads the CM6 language pack for a path's extension (or
// bare filename, e.g. Dockerfile), so language bundles stay out of the initial
// chunk — every import() below is its own lazy chunk. Cached per key.
const cache = new Map<string, Promise<LanguageSupport | null>>()

export function languageFor(path: string): Promise<LanguageSupport | null> {
  const key = langKey(path)
  let p = cache.get(key)
  if (!p) {
    p = load(key)
    cache.set(key, p)
  }
  return p
}

// langKey is the lowercased extension, or the lowercased basename when there is
// no extension (Dockerfile, Makefile).
function langKey(path: string): string {
  const base = path.slice(path.lastIndexOf('/') + 1)
  const dot = base.lastIndexOf('.')
  return (dot === -1 ? base : base.slice(dot + 1)).toLowerCase()
}

const stream = (p: StreamParser<unknown>) => new LanguageSupport(StreamLanguage.define(p))

async function load(key: string): Promise<LanguageSupport | null> {
  switch (key) {
    // official Lezer grammars (precise tokenization, work in the chunk tokenizer too)
    case 'go':
      return (await import('@codemirror/lang-go')).go()
    case 'js':
    case 'jsx':
    case 'mjs':
    case 'cjs':
      return (await import('@codemirror/lang-javascript')).javascript({ jsx: true })
    case 'ts':
      return (await import('@codemirror/lang-javascript')).javascript({ typescript: true })
    case 'tsx':
      return (await import('@codemirror/lang-javascript')).javascript({ jsx: true, typescript: true })
    case 'py':
    case 'pyi':
      return (await import('@codemirror/lang-python')).python()
    case 'md':
    case 'markdown':
      return (await import('@codemirror/lang-markdown')).markdown()
    case 'json':
    case 'jsonc':
      return (await import('@codemirror/lang-json')).json()
    case 'rs':
      return (await import('@codemirror/lang-rust')).rust()
    case 'java':
      return (await import('@codemirror/lang-java')).java()
    case 'c':
    case 'h':
    case 'cpp':
    case 'cc':
    case 'cxx':
    case 'hpp':
    case 'hxx':
    case 'cu':
      return (await import('@codemirror/lang-cpp')).cpp()
    case 'php':
      return (await import('@codemirror/lang-php')).php()
    case 'sql':
      return (await import('@codemirror/lang-sql')).sql()
    case 'html':
    case 'htm':
    case 'vue':
      return (await import('@codemirror/lang-html')).html()
    case 'css':
    case 'scss':
    case 'less':
      return (await import('@codemirror/lang-css')).css()
    case 'xml':
    case 'svg':
    case 'xsl':
      return (await import('@codemirror/lang-xml')).xml()
    case 'yaml':
    case 'yml':
      return (await import('@codemirror/lang-yaml')).yaml()
    // legacy stream modes (line-oriented; highlight in the viewer, best-effort in chunks)
    case 'rb':
      return stream((await import('@codemirror/legacy-modes/mode/ruby')).ruby)
    case 'sh':
    case 'bash':
    case 'zsh':
    case 'ksh':
      return stream((await import('@codemirror/legacy-modes/mode/shell')).shell)
    case 'cs':
      return stream((await import('@codemirror/legacy-modes/mode/clike')).csharp)
    case 'kt':
    case 'kts':
      return stream((await import('@codemirror/legacy-modes/mode/clike')).kotlin)
    case 'scala':
    case 'sc':
      return stream((await import('@codemirror/legacy-modes/mode/clike')).scala)
    case 'dart':
      return stream((await import('@codemirror/legacy-modes/mode/clike')).dart)
    case 'm':
      return stream((await import('@codemirror/legacy-modes/mode/clike')).objectiveC)
    case 'swift':
      return stream((await import('@codemirror/legacy-modes/mode/swift')).swift)
    case 'lua':
      return stream((await import('@codemirror/legacy-modes/mode/lua')).lua)
    case 'pl':
    case 'pm':
      return stream((await import('@codemirror/legacy-modes/mode/perl')).perl)
    case 'toml':
      return stream((await import('@codemirror/legacy-modes/mode/toml')).toml)
    case 'dockerfile':
      return stream((await import('@codemirror/legacy-modes/mode/dockerfile')).dockerFile)
    // The contract surface's own language (T44.1) — name and dot color were
    // always mapped; the loader case was the missing piece.
    case 'proto':
      return stream((await import('@codemirror/legacy-modes/mode/protobuf')).protobuf)
    default:
      return null
  }
}

// langColor is the language dot color for a path (design handoff palette,
// extended). Falls back to the mode's gutter gray for unmapped extensions.
const DOT: Record<string, string> = {
  go: '#016974',
  ts: '#175BCC', tsx: '#175BCC', mjs: '#DE9800', cjs: '#DE9800', js: '#DE9800', jsx: '#DE9800',
  py: '#0E8345', pyi: '#0E8345',
  rs: '#A33B04', java: '#B07219', kt: '#7C3EC3', kts: '#7C3EC3', scala: '#DE1135',
  c: '#5E5E5E', h: '#5E5E5E', cpp: '#016974', cc: '#016974', cxx: '#016974', hpp: '#016974',
  cs: '#166C3B', php: '#7C3EC3', rb: '#DE1135', swift: '#DE9800', lua: '#175BCC', pl: '#016974',
  sql: '#A95F03', html: '#A33B04', htm: '#A33B04', vue: '#0E8345',
  css: '#175BCC', scss: '#175BCC', less: '#175BCC',
  xml: '#5E5E5E', svg: '#A33B04',
  yaml: '#5E5E5E', yml: '#5E5E5E', toml: '#5E5E5E', json: '#A33B04', jsonc: '#A33B04',
  md: '#5E5E5E', markdown: '#5E5E5E',
  sh: '#0E8345', bash: '#0E8345', zsh: '#0E8345',
  dockerfile: '#175BCC', proto: '#7C3EC3', thrift: '#7C3EC3',
}

export function langColor(path: string, tok: PhebsTokens): string {
  return DOT[langKey(path)] ?? tok.gutter
}

// langName is the human display name for a path's language (file-viewer header).
const NAME: Record<string, string> = {
  go: 'Go', ts: 'TypeScript', tsx: 'TypeScript', js: 'JavaScript', jsx: 'JavaScript',
  mjs: 'JavaScript', cjs: 'JavaScript', py: 'Python', pyi: 'Python', rs: 'Rust', java: 'Java',
  c: 'C', h: 'C', cpp: 'C++', cc: 'C++', cxx: 'C++', hpp: 'C++', hxx: 'C++', cs: 'C#',
  php: 'PHP', rb: 'Ruby', kt: 'Kotlin', kts: 'Kotlin', scala: 'Scala', swift: 'Swift',
  lua: 'Lua', pl: 'Perl', pm: 'Perl', dart: 'Dart', sql: 'SQL', html: 'HTML', htm: 'HTML',
  vue: 'Vue', css: 'CSS', scss: 'SCSS', less: 'Less', xml: 'XML', svg: 'SVG',
  yaml: 'YAML', yml: 'YAML', toml: 'TOML', json: 'JSON', jsonc: 'JSON', md: 'Markdown',
  markdown: 'Markdown', sh: 'Shell', bash: 'Shell', zsh: 'Shell', dockerfile: 'Dockerfile',
  proto: 'Protobuf', thrift: 'Thrift',
}

export function langName(path: string): string {
  const k = langKey(path)
  return NAME[k] ?? (k ? k.toUpperCase() : 'Text')
}
