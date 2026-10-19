import { afterEach, expect, test, vi } from 'vitest'
import { fetchAnalytics, fetchAudit } from './api'

const event = { id: 'e1', action: 'auth.login', status: 200, created_at: '2026-10-19T12:00:00Z' }
const audit = { events: [event], has_more: false }
const analytics = { total_searches: 4, avg_duration_ms: 1, daily: [{ date: '2026-10-19', count: 4 }], top_repos: [{ name: 'example.test/alpha', count: 4 }] }
function response(value: unknown) {
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => value })
  vi.stubGlobal('fetch', fetch)
  return fetch
}
afterEach(() => vi.unstubAllGlobals())

test('admin reads retain their exact parameters, credentials and cancellation signal', async () => {
  const signal = new AbortController().signal
  const fetch = response(audit)
  expect(await fetchAudit(50, 50, signal)).toEqual(audit)
  expect(fetch).toHaveBeenLastCalledWith('/api/audit?offset=50&limit=50', { credentials: 'same-origin', signal })
  response(analytics)
  expect(await fetchAnalytics(30, signal)).toEqual(analytics)
  expect(globalThis.fetch).toHaveBeenLastCalledWith('/api/analytics?days=30', { credentials: 'same-origin', signal })
})

test.each([
  null,
  { ...audit, events: {} },
  { ...audit, has_more: 'false' },
  { events: [], has_more: true },
  { ...audit, events: Array.from({ length: 51 }, (_, index) => ({ ...event, id: `e${index}` })) },
  { ...audit, events: [event, event] },
  { ...audit, events: [{ ...event, created_at: 'unavailable' }] },
  { ...audit, events: [{ ...event, status: 200.5 }] },
  { ...audit, events: [{ ...event, actor_email: {} }] },
])('refuses malformed or unbounded audit pages %#', async value => {
  response(value)
  await expect(fetchAudit(0)).rejects.toThrow('Invalid audit response')
})

test.each([
  null,
  { ...analytics, total_searches: -1 },
  { ...analytics, avg_duration_ms: Number.MAX_SAFE_INTEGER + 1 },
  { ...analytics, daily: [{ date: '2026-02-30', count: 1 }] },
  { ...analytics, daily: [{ date: '2026-10-19', count: -1 }] },
  { ...analytics, daily: [analytics.daily[0], analytics.daily[0]] },
  { ...analytics, daily: [{ date: '2026-10-19', count: 1 }, { date: '2026-10-18', count: 1 }] },
  { ...analytics, daily: Array.from({ length: 31 }, (_, index) => ({ date: `2026-10-${String(index + 1).padStart(2, '0')}`, count: 1 })) },
  { ...analytics, top_repos: [{ name: 'example.test/alpha', count: '4' }] },
  { ...analytics, top_repos: [analytics.top_repos[0], analytics.top_repos[0]] },
  { ...analytics, top_repos: Array.from({ length: 11 }, (_, index) => ({ name: `example.test/repo-${index}`, count: 1 })) },
])('refuses malformed or unbounded analytics summaries %#', async value => {
  response(value)
  await expect(fetchAnalytics()).rejects.toThrow('Invalid analytics response')
})

test('nullable slices and measured zero counts remain valid', async () => {
  response({ events: null, has_more: false })
  expect((await fetchAudit(0)).events).toBeNull()
  const empty = { total_searches: 0, avg_duration_ms: 0, daily: null, top_repos: null }
  response(empty)
  expect(await fetchAnalytics()).toEqual(empty)
})
