import { expect, type Page, type Route } from '@playwright/test'
import { navigationFixture } from './navigationFixture'
import type { APIKeySummary, LifecycleStatus } from '../src/api'
import { TYPED_PROVIDERS } from '../src/typedIndex'

export const settingsLifecycle: LifecycleStatus = {
  schema: 'phebs-lifecycle-status-v1',
  policy: { enabled: false, owners: 0, soft_watermark_percent: 80, hard_watermark_percent: 90, resume_watermark_percent: 75, max_candidates_per_turn: 64, max_deletes_per_turn: 16, max_queries_per_turn: 16 },
  capacity: { completeness: 'unavailable', pressure: 'unavailable' }, owners: [],
}
export async function settingsAuthFixture(page: Page) {
  const base = await navigationFixture(page)
  let user = 0
  let admin = false
  let authenticated = true
  let setup = false
  let oidc = false
  let badStatus = false
  let fail = ''
  let hold = ''
  let held: { route: Route; value: unknown; logout?: boolean } | undefined
  const keys: APIKeySummary[][] = [[], []]
  const requests: { method: string; path: string; csrf?: string }[] = []
  const status = () => ({ authenticated, auth_required: true, setup_required: setup, oidc_enabled: oidc,
    password_enabled: !oidc, csrf_token: authenticated ? `settings-csrf-${user}` : undefined,
    user: authenticated ? { id: `settings-reader-${user}`, email: `reader${user}@localhost.test`, is_admin: admin } : undefined,
  })
  await page.route('**/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    requests.push({ method, path, csrf: request.headers()['x-csrf-token'] })
    let value: unknown
    if (path === '/api/auth/status') value = { ...status(), ...(badStatus ? { auth_required: 'false' } : {}) }
    else if (path === '/api/version') value = { version: 'settings-auth-audit', capabilities: [] }
    else if (path === '/api/auth/login' || path === '/api/auth/setup') {
      expect(request.postDataJSON().password).toBe('neutral fixture password')
      if (path.endsWith('/setup')) expect(request.postDataJSON().setup_token).toBe('fixture-setup-token')
      authenticated = true; setup = false
      await route.fulfill({ json: status(), headers: { 'Set-Cookie': 'settings-fixture-session=current; Path=/; SameSite=Lax' } }); return
    } else if (path === '/api/auth/logout') {
      expect(request.headers()['x-csrf-token']).toBe(`settings-csrf-${user}`)
      authenticated = false
      if (path === hold) { hold = ''; held = { route, value: undefined, logout: true }; return }
      await route.fulfill({ status: 204, headers: { 'Set-Cookie': 'settings-fixture-session=; Max-Age=0; Path=/; SameSite=Lax' } }); return
    } else if (path === '/api/auth/keys' && method === 'GET') value = { keys: [...keys[user]] }
    else if (path === '/api/auth/keys' && method === 'POST') {
      expect(request.headers()['x-csrf-token']).toBe(`settings-csrf-${user}`)
      const key = { id: `key-${user}`, name: request.postDataJSON().name, prefix: `phebs_fixture_${user}`, capabilities: [], created_at: '2026-10-19T12:00:00Z' }
      keys[user].push(key)
      value = { key, token: `phebs_fixture_${user}.only-display-fixture` }
    } else if (path.startsWith('/api/auth/keys/') && method === 'DELETE') {
      expect(request.headers()['x-csrf-token']).toBe(`settings-csrf-${user}`)
      keys[user] = keys[user].filter(key => key.id !== path.split('/').pop())
      await route.fulfill({ status: 204 }); return
    } else if (path === '/api/lifecycle-status') value = settingsLifecycle
    else if (path === '/api/code-navigation-indexing/providers') value = { schema: 'phebs-typed-index-providers-v1', providers: TYPED_PROVIDERS.map((id, index) => ({ id, name: ['Bazel', 'Go module / workspace', 'Existing artifact'][index], available: false })) }
    else { requests.pop(); await route.fallback(); return }
    if (path === hold) { hold = ''; held = { route, value }; return }
    if (path === fail) { await route.fulfill({ status: 503, json: { error: 'temporary audit outage' } }); return }
    await route.fulfill({ json: value })
  })
  return { ...base, requests,
    authenticated: (value: boolean) => { authenticated = value },
    setup: () => { setup = true; authenticated = false },
    oidc: () => { oidc = true; authenticated = false },
    admin: (value: boolean) => { admin = value },
    user: (value: number) => { user = value },
    fail: (path: string) => { fail = path },
    badStatus: (value: boolean) => { badStatus = value },
    hold: (path: string) => { hold = path }, held: () => held !== undefined,
    release: async () => {
      const pending = held!; held = undefined
      await pending.route.fulfill(pending.logout ? { status: 204, headers: { 'Set-Cookie': 'settings-fixture-session=; Max-Age=0; Path=/; SameSite=Lax' } } : { json: pending.value })
    },
  }
}
