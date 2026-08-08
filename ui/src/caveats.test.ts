import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  CALLER_ROWS_UNAVAILABLE_ADDENDUM,
  CENSUS_LEADS_LINE,
  COMPARISON_UNAVAILABLE_ADDENDUM,
  EXPLORER_DIAGRAM_ADDENDUM,
  RELATIONSHIP_CAVEAT_MIRROR,
} from './caveats'

// A mirror pin must depend on the OWNING Go constant, not on a second copy
// of the same literal: read the Go source at test time and extract it, so a
// Go-side wording change fails this suite instead of leaving a stale mirror.
function goConstant(file: string, name: string): string {
  // Vitest runs with cwd at ui/; the Go tree is the repo root above it.
  const source = readFileSync(resolve(process.cwd(), '..', file), 'utf8')
  const match = source.match(new RegExp(`${name}\\s*=\\s*"((?:[^"\\\\]|\\\\.)*)"`))
  if (!match) throw new Error(`constant ${name} not found in ${file}`)
  return JSON.parse(`"${match[1]}"`)
}

// T43.6 byte-identity pins: claim-boundary wording is contract text, not
// copy. A deliberate wording change edits BOTH the constant and this pin in
// the same reviewed diff; anything else is drift and fails here. Mirrors
// name their owning Go constant so a Go-side change is found by grep.

describe('claim-boundary wording is pinned byte-for-byte', () => {
  it('census exception naming line (charter §1, adjudicated T43.1 §9)', () => {
    expect(CENSUS_LEADS_LINE).toBe(
      'The census leads deliberately: unresolved coverage is the claim this '
      + 'page establishes, and the producer and consumer rows below are '
      + 'evidence within it.',
    )
  })
  it('relationshipCaveat mirror matches the owning Go constant byte-for-byte', () => {
    expect(RELATIONSHIP_CAVEAT_MIRROR).toBe(
      goConstant('internal/api/relationships.go', 'relationshipCaveat'),
    )
  })
  it('presentation addenda', () => {
    expect(EXPLORER_DIAGRAM_ADDENDUM).toBe(
      'The table is authoritative for this exact page; the optional diagram '
      + 'adds no edge or transitivity.',
    )
    expect(CALLER_ROWS_UNAVAILABLE_ADDENDUM).toBe('This is not zero callers, and no partial rows are shown.')
    expect(COMPARISON_UNAVAILABLE_ADDENDUM).toBe('No partial rows are classified, and this is not evidence of zero callers.')
  })
})
