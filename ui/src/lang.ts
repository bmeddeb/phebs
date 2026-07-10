import type { LanguageSupport } from '@codemirror/language'

// languageFor lazily loads the CM6 language pack for a path's extension, so
// language bundles stay out of the initial chunk. Cached per extension.
const cache = new Map<string, Promise<LanguageSupport | null>>()

export function languageFor(path: string): Promise<LanguageSupport | null> {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  let p = cache.get(ext)
  if (!p) {
    p = load(ext)
    cache.set(ext, p)
  }
  return p
}

async function load(ext: string): Promise<LanguageSupport | null> {
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

// langColor is the language dot color for a path (design handoff palette).
const DOT: Record<string, string> = {
  go: '#016974',
  ts: '#175BCC',
  tsx: '#175BCC',
  js: '#DE9800',
  jsx: '#DE9800',
  py: '#0E8345',
  md: '#5E5E5E',
  markdown: '#5E5E5E',
  json: '#A33B04',
  proto: '#7C3EC3',
}

export function langColor(path: string): string {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  return DOT[ext] ?? '#A6A6A6'
}
