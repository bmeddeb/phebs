import { afterEach, expect, test, vi } from 'vitest'
import { fetchBlame, fetchCommit, fetchCommits, fetchDefinition, fetchDiff, fetchHover, fetchReferences } from './api'
const ref = 'a'.repeat(40)
const repo = 'example.test/alpha'
const identity = { name: 'Reader', email: 'reader@localhost.test', time: '2026-10-19T12:00:00Z' }
const commit = { id: ref, short_id: 'aaaaaaa', subject: 'Current change', message: 'Current change', author: identity, committer: identity, parent_ids: [] }
const location = { repo, revision: ref, path: 'lib.go', encoding: 'utf16', range: { start: { line: 2, character: 1 }, end: { line: 2, character: 3 } } }
const commits = { revision: ref, commits: [commit], offset: 0, has_more: false }
const blame = { revision: ref, path: 'main.go', lines: [{ line: 1, original_line: 1, commit_id: ref, original_path: 'main.go', content: 'package main' }], commits: [commit], truncated: false }
const detail = { revision: ref, commit, changes: [] }
const diff = { head: ref, base: '', files: [], patch: '', truncated: false }
const reads = {
  history: () => fetchCommits(repo, ref, '', 50, 0), blame: () => fetchBlame(repo, 'main.go', ref),
  commit: () => fetchCommit(repo, ref), diff: () => fetchDiff(repo, ref),
  definition: () => fetchDefinition(repo, ref, 'main.go', 0, 0),
  references: () => fetchReferences(repo, ref, 'main.go', 0, 0),
  hover: () => fetchHover(repo, ref, 'main.go', 0, 0),
}
function response(value: unknown) { vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => value })) }
afterEach(() => vi.unstubAllGlobals())

test.each([
  ['history', { ...commits, revision: 'b'.repeat(40) }],
  ['history', { ...commits, offset: 1 }],
  ['history', { ...commits, commits: [], has_more: true }],
  ['history', { ...commits, commits: [commit, commit] }],
  ['history', { ...commits, commits: Array.from({ length: 51 }, (_, index) => ({ ...commit, id: index.toString(16).padStart(40, '0') })) }],
  ['blame', { ...blame, path: 'other.go' }],
  ['blame', { ...blame, revision: 'b'.repeat(40) }],
  ['blame', { ...blame, lines: [{ ...blame.lines[0], commit_id: 'b'.repeat(40) }] }],
  ['commit', { ...detail, revision: 'b'.repeat(40) }],
  ['commit', { ...detail, commit: { ...commit, author: null } }],
  ['diff', { ...diff, head: 'b'.repeat(40) }],
  ['diff', { ...diff, files: [{ status: 'added', path: 'main.go', additions: -1 }] }],
  ['diff', { ...diff, patch: 'x'.repeat((2 << 20) + 1) }],
  ['definition', { available: true, location: { ...location, repo: 'example.test/other' } }],
  ['definition', { available: true, location: { ...location, revision: 'b'.repeat(40) } }],
  ['definition', { available: true, location: { ...location, encoding: 'unknown' } }],
  ['references', { available: true, locations: [{ ...location, path: '../outside.go' }] }],
  ['references', { available: true, locations: Array(501).fill(location) }],
  ['references', { available: false, locations: [location] }],
  ['hover', { available: true, hover: { symbol: 'main', encoding: 'utf16', range: location.range, documentation: [{}] } }],
  ['hover', { available: 'false' }],
] as const)('refuses crossed, malformed or excessive %s response %#', async (kind, value) => {
  response(value)
  await expect(reads[kind]()).rejects.toThrow(/Invalid .* response/)
})

test('valid pinned responses and explicit unavailable navigation remain compatible', async () => {
  for (const [kind, value] of Object.entries({ history: commits, blame, commit: detail, diff, definition: { available: true, location }, references: { available: true, locations: [location] }, hover: { available: false } })) {
    response(value)
    expect(await reads[kind as keyof typeof reads]()).toEqual(value)
  }
})

test('uppercase pinned refs and nullable reference slices retain compatibility', async () => {
  response({ available: true, location })
  expect(await fetchDefinition(repo, ref.toUpperCase(), 'main.go', 0, 0)).toEqual({ available: true, location })
  response({ available: true, locations: null })
  expect(await fetchReferences(repo, ref, 'main.go', 0, 0)).toEqual({ available: true, locations: null })
})
