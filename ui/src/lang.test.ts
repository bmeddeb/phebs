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
    const tokens = tokenize('message OrderRequest {', lang, 'light')
    // The keyword role must land on `message` — a colored span, not the
    // plain fallback.
    expect(tokens.some((token) => token.color !== undefined)).toBe(true)
  })
})
