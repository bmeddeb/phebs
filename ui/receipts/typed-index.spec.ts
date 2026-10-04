import { expect, test, type Page } from '@playwright/test'
import { TYPED_PROVIDERS, TYPED_STATES } from '../src/typedIndex'

// Presentation fixtures only: these never install a runtime or execute a provider.
const repository = 'example.test/neutral'
const commit = 'a'.repeat(40)
const digest = 'sha256:' + 'b'.repeat(64)
const resource = 'native-arm64-bounded-v1'
const view = { schema: 'phebs-typed-index-status-v1', repository, commit, revision: digest, available: true, state: 'absent', provider: TYPED_PROVIDERS[0], profile: 'neutral-canary', target_profile: 'neutral-canary', config_profile: 'ordinary', resource_profile: resource, request_digest: '', job_state: '', current_commit: '', reason: '', checked_purpose: '' }
async function fixture(page: Page, admin = true) {
  const requests: { path: string; body: Record<string, string> | null; csrf: string | undefined }[] = []
  let state: string = 'absent'
  let refusal = false
  let enqueueFailure = false
  let unavailable = false
  await page.route('**/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const body = request.method() === 'POST' ? request.postDataJSON() : null
    requests.push({ path, body, csrf: request.headers()['x-csrf-token'] })
    let value: unknown
    let status = 200
    switch (path) {
      case '/api/auth/status': value = { authenticated: true, auth_required: true, setup_required: false, oidc_enabled: false, password_enabled: true, csrf_token: 'receipt-csrf', user: { id: 'neutral-admin', email: 'neutral@localhost.test', display_name: 'Neutral operator', is_admin: admin } }; break
      case '/api/version': value = { version: 'receipt', capabilities: [] }; break
      case '/api/auth/keys': value = { keys: [] }; break
      case '/api/repo-status': value = [{ name: repository, indexed_commit_hash: commit, clone_url: '', orphaned: false }]; break
      case '/api/lifecycle-status': value = { schema: 'phebs-lifecycle-status-v1', policy: { enabled: false, owners: 0, soft_watermark_percent: 80, hard_watermark_percent: 90, resume_watermark_percent: 75, max_candidates_per_turn: 64, max_deletes_per_turn: 16, max_queries_per_turn: 16 }, capacity: { completeness: 'unavailable', pressure: 'unavailable' }, owners: [] }; break
      case '/api/code-navigation-indexing/providers': value = { schema: 'phebs-typed-index-providers-v1', providers: TYPED_PROVIDERS.map((id, index) => ({ id, name: ['Bazel', 'Go module / workspace', 'Existing artifact'][index], available: !unavailable && index === 0 })) }; break
      case '/api/code-navigation-indexing/status': value = refusal ? { detail: '/private/worker/stderr' } : { ...view, available: !unavailable, state, ...(unavailable ? { provider: '', profile: '', target_profile: '', config_profile: '', resource_profile: '', revision: '' } : {}) }; status = refusal ? 503 : 200; break
      case '/api/code-navigation-indexing/plan': value = { schema: 'phebs-typed-index-preview-v1', selection: body, commit, request_digest: digest, idempotency_key: 'c'.repeat(64), resource_profile: resource }; break
      case '/api/code-navigation-indexing/enqueue': value = enqueueFailure ? { detail: '/private/worker/stderr' } : { ...view, state: 'planning', request_digest: digest, job_state: 'pending' }; status = enqueueFailure ? 503 : 200; state = enqueueFailure ? state : 'planning'; break
      case '/api/source': value = { content: 'package neutral\nfunc Example() {}\n', encoding: 'utf8', size: 34 }; break
      case '/api/folder_contents': value = { entries: [{ name: 'main.go', type: 'file', size: 34 }] }; break
      case '/api/find_definitions': case '/api/find_references': case '/api/hover': value = { available: false }; break
      default: throw new Error('Unexpected fixture request: ' + path)
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) })
  })
  return { requests, setState: (next: string) => { state = next }, refuse: () => { refusal = true }, failEnqueue: (next: boolean) => { enqueueFailure = next }, unavailable: () => { unavailable = true } }
}
const url = '/#/settings?' + new URLSearchParams({ repo: repository, section: 'code-navigation-indexing' })
for (const theme of ['light', 'dark'] as const) for (const width of [1280, 390]) for (const density of ['comfortable', 'dense']) {
  test(`managed indexing ${theme} ${width} ${density}`, async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
    await fixture(page)
    await page.setViewportSize({ width, height: 900 })
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
    await page.addInitScript(mode => { localStorage.setItem('phebs-theme', mode.split(':')[0]); localStorage.setItem('phebs-density', mode.split(':')[1]); localStorage.setItem('phebs-palette', 'phebs') }, theme + ':' + density)
    const navigationStart = Date.now()
    await page.goto(url)
    const section = page.getByRole('region', { name: 'Code navigation indexing' })
    await expect(section.getByText(commit, { exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Code navigation indexing', exact: true })).toBeFocused()
    await expect(section.getByRole('article')).toHaveCount(3)
    await page.keyboard.press('Tab')
    await expect(section.getByRole('button', { name: 'Refresh indexing' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(section.getByRole('combobox', { name: 'Indexing repository' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(section.getByRole('combobox', { name: 'Indexing action' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(section.getByRole('button', { name: 'Review indexing plan' })).toBeFocused()
    const readyMillis = Date.now() - navigationStart
    const previewStart = Date.now()
    await page.keyboard.press('Enter')
    await expect(section.getByText('Exact commit:', { exact: false })).toBeVisible()
    await expect(section.getByRole('button', { name: 'Generate navigation', exact: true })).toBeEnabled()
    await page.evaluate(() => document.fonts.ready)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    // Capture the complete panel below sticky chrome without masking anatomy.
    const bounds = await section.boundingBox()
    if (bounds) await page.setViewportSize({ width, height: Math.ceil(bounds.height) + 160 })
    await section.evaluate(node => window.scrollTo(0, window.scrollY + node.getBoundingClientRect().top - 80))
    await expect.poll(() => section.evaluate(node => node.getBoundingClientRect().top)).toBeGreaterThanOrEqual(64)
    await expect(section).toHaveScreenshot(`typed-index-preview--${theme}--${width}--${density}.png`)
    expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true)
    expect(await section.locator('select').evaluateAll(nodes => nodes.every(node => !!(node as HTMLSelectElement).labels?.length))).toBe(true)
    expect(errors).toEqual([])
    console.log(JSON.stringify({ theme, width, density, fixtureReadyMillis: readyMillis, fixturePreviewMillis: Date.now() - previewStart, consoleErrors: errors.length }))
  })
}

test('exact request retry, all states and last-good polling', async ({ page }) => {
  const f = await fixture(page)
  await page.goto(url)
  const section = page.getByRole('region', { name: 'Code navigation indexing' })
  for (const purpose of ['canary', 'dry-run', 'publish']) {
    await section.getByRole('combobox', { name: 'Indexing action' }).selectOption(purpose)
    await section.getByRole('button', { name: 'Review indexing plan' }).click()
    await expect(section.getByText('Exact commit:', { exact: false })).toBeVisible()
    expect(f.requests.filter(r => r.path.endsWith('/enqueue'))).toHaveLength(0)
    expect(f.requests.filter(r => r.path.endsWith('/plan')).at(-1)?.body?.purpose).toBe(purpose)
  }
  f.failEnqueue(true)
  await section.getByRole('button', { name: 'Generate navigation', exact: true }).click()
  await expect(section.getByRole('button', { name: 'Retry exact request' })).toBeVisible()
  expect(await section.innerText()).not.toContain('/private/')
  f.failEnqueue(false)
  await section.getByRole('button', { name: 'Retry exact request' }).click()
  const enqueues = f.requests.filter(r => r.path.endsWith('/enqueue'))
  expect(enqueues).toHaveLength(2)
  expect(enqueues[0].body).toEqual(enqueues[1].body)
  expect(enqueues.every(r => r.csrf === 'receipt-csrf')).toBe(true)
  await expect(section.getByText('planning', { exact: true })).toBeVisible()
  for (const state of TYPED_STATES) {
    f.setState(state)
    await section.getByRole('button', { name: 'Refresh indexing' }).click()
    await expect(section.getByText(state, { exact: true })).toBeVisible()
  }
  f.setState('indexing')
  await section.getByRole('button', { name: 'Refresh indexing' }).click()
  await expect(section.getByText('indexing', { exact: true })).toBeVisible()
  const initialCount = f.requests.length
  f.refuse()
  await expect(section.getByRole('alert')).toContainText('Last confirmed state is retained', { timeout: 8000 })
  await expect(section.getByText('indexing', { exact: true })).toBeVisible()
  expect(f.requests.slice(initialCount).map(r => r.path)).toEqual(['/api/code-navigation-indexing/status'])
  expect(await section.innerText()).not.toContain('/private/')
})

test('ordinary user has no managed reads or actions; File link is administrator-only', async ({ page }) => {
  const f = await fixture(page, false)
  await page.goto(url)
  await expect(page.getByRole('heading', { name: 'Appearance' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Code navigation indexing' })).toHaveCount(0)
  expect(f.requests.some(r => r.path.startsWith('/api/code-navigation-indexing'))).toBe(false)
  await page.goto('/#/file?' + new URLSearchParams({ repo: repository, path: 'main.go', ref: commit }))
  await page.locator('.cm-line').first().click()
  await expect(page.getByText('SCIP data is not available for this revision.')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Open code navigation indexing' })).toHaveCount(0)
})
test('File unavailable link preselects repository and focuses Settings section', async ({ page }) => {
  await fixture(page)
  await page.goto('/#/file?' + new URLSearchParams({ repo: repository, path: 'main.go', ref: commit }))
  await page.locator('.cm-line').first().click()
  await page.getByRole('link', { name: 'Open code navigation indexing' }).click()
  await expect(page.getByRole('combobox', { name: 'Indexing repository' })).toHaveValue(repository)
  await expect(page.getByRole('heading', { name: 'Code navigation indexing', exact: true })).toBeFocused()
})
test('unavailable build cannot review or generate', async ({ page }) => {
  const f = await fixture(page)
  f.unavailable()
  await page.goto(url)
  const section = page.getByRole('region', { name: 'Code navigation indexing' })
  await expect(section.getByText('Managed generation is not registered in this build. Committed SCIP artifacts remain the code-navigation source.')).toBeVisible()
  await expect(section.getByRole('button', { name: 'Review indexing plan' })).toHaveCount(0)
  expect(f.requests.some(r => r.body !== null)).toBe(false)
})
