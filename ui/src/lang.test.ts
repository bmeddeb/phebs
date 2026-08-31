import { describe, expect, it } from 'vitest'
import { languageFor, langName } from './lang'
import { tokenize } from './highlight'

describe('proto language support (T44.1)', () => {
  it('loads a language for .proto files', async () => {
    const lang = await languageFor('contracts/orders/v1/orders.proto')
    expect(lang).not.toBeNull()
    expect(langName('contracts/orders/v1/orders.proto')).toBe('Protobuf')
  })

  it('tokenizes proto keywords with palette roles', async () => {
    const lang = await languageFor('a.proto')
    const source = 'message OrderRequest {'
    const tokens = tokenize(source, lang, 'light')
    const message = tokens.find((token) => source.slice(token.from, token.to) === 'message')
    // The keyword role must land on `message` — a colored span, not merely
    // some unrelated colored token or the plain fallback.
    expect(message?.color).toBeTruthy()
  })
})

describe('thrift language support (T44.1)', () => {
  it('loads a language for .thrift files', async () => {
    const lang = await languageFor('contracts/orders.thrift')
    expect(lang).not.toBeNull()
    expect(langName('contracts/orders.thrift')).toBe('Thrift')
  })

  it('tokenizes thrift keywords, base types, and both comment forms', async () => {
    const lang = await languageFor('a.thrift')
    expect(tokenize('struct Order {', lang, 'light').some((token) => token.color !== undefined)).toBe(true)
    expect(tokenize('  1: required i64 id,', lang, 'light').some((token) => token.color !== undefined)).toBe(true)
    const hash = tokenize('# a shell-style comment', lang, 'light')
    const slash = tokenize('// a line comment', lang, 'light')
    expect(hash.some((token) => token.fontStyle === 'italic')).toBe(true)
    expect(slash.some((token) => token.fontStyle === 'italic')).toBe(true)
  })
})
