import type { Page, Route } from '@playwright/test'
import { navigationFixture } from './navigationFixture'

export const auditEvent = (id: number) => ({
  id: `event-${id}`, action: `neutral.action.${id}`, actor_email: 'admin@localhost.test',
  status: 200, created_at: '2026-10-19T11:00:00Z', target: 'example.test/alpha', source_ip: '127.0.0.1',
})
export const analyticsSummary = {
  total_searches: 4, avg_duration_ms: 1,
  daily: [{ date: '2026-10-18', count: 0 }, { date: '2026-10-19', count: 4 }],
  top_repos: [{ name: 'example.test/alpha', count: 4 }],
}
export async function auditAnalyticsFixture(page: Page) {
  const base = await navigationFixture(page)
  const requests: string[] = []
  let admin = true
  let failure = ''
  let invalid = ''
  let empty = false
  let hold = false
  let held: Route | undefined
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    if (url.pathname === '/api/auth/status') {
      await route.fulfill({ json: {
        authenticated: true, auth_required: true, setup_required: false,
        oidc_enabled: false, password_enabled: true, csrf_token: 'audit-fixture',
        user: { id: 'audit-reader', email: 'admin@localhost.test', is_admin: admin },
      } })
      return
    }
    if (!['/api/audit', '/api/analytics'].includes(url.pathname)) { await route.fallback(); return }
    requests.push(url.pathname + url.search)
    if (!admin) base.unexpected.push('admin read after access loss')
    if (url.pathname === failure) { await route.fulfill({ status: 503, body: 'neutral outage' }); return }
    if (url.pathname === '/api/audit') {
      if (hold) { hold = false; held = route; return }
      const offset = Number(url.searchParams.get('offset'))
      const events = offset === 0 ? [auditEvent(1), auditEvent(2)] : offset === 2 ? [auditEvent(2), auditEvent(3)] : [auditEvent(4)]
      await route.fulfill({ json: { events: invalid === 'audit' ? [auditEvent(1), auditEvent(1)] : events, has_more: offset < 4 } })
    } else await route.fulfill({ json: invalid === 'analytics' ? { ...analyticsSummary, total_searches: -1 } : empty ? { ...analyticsSummary, daily: null, top_repos: null } : analyticsSummary })
  })
  return {
    ...base, requests,
    admin: (value: boolean) => { admin = value },
    fail: (path: string) => { failure = path },
    invalid: (value: string) => { invalid = value },
    empty: () => { empty = true },
    holdAudit: () => { hold = true },
    held: () => held !== undefined,
    release: async () => { await held!.fulfill({ json: { events: [auditEvent(99)], has_more: false } }); held = undefined },
  }
}
