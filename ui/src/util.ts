export function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export function relTime(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  return `${Math.floor(s / 86400)} d ago`
}

export function repoFilter(repo: string): string {
  return queryAtom('repo', `^${escapeRegex(repo)}$`)
}

export function fileFilter(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1)
  return queryAtom('file', `${escapeRegex(name)}$`)
}

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function queryAtom(prefix: string, value: string): string {
  const quoted = value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
  return `${prefix}:"${quoted}"`
}

export function splitQueryTerms(value: string): string[] {
  const terms: string[] = []
  let current = ''
  let quoted = false
  let escaped = false
  for (const char of value) {
    if (escaped) {
      current += char
      escaped = false
      continue
    }
    if (char === '\\') {
      current += char
      escaped = true
      continue
    }
    if (char === '"') {
      current += char
      quoted = !quoted
      continue
    }
    if (/\s/.test(char) && !quoted) {
      if (current) terms.push(current)
      current = ''
      continue
    }
    current += char
  }
  if (current) terms.push(current)
  return terms
}

export function ancestorFolders(path: string): string[] {
  const parts = path.split('/').filter(Boolean)
  const ancestors: string[] = []
  let current = ''
  for (let index = 0; index < parts.length - 1; index++) {
    current = current ? `${current}/${parts[index]}` : parts[index]
    ancestors.push(current)
  }
  return ancestors
}

export function runeColumnToUTF16Offset(line: string, column: number): number {
  if (column <= 1) return 0
  let runeColumn = 1
  let offset = 0
  for (const rune of line) {
    if (runeColumn >= column) break
    offset += rune.length
    runeColumn++
  }
  return offset
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

export function boundedError(cause: unknown): string {
  const message = String(cause).replace(/^Error:\s*/, '')
  return message.length <= 512 ? message : `${message.slice(0, 511)}…`
}
