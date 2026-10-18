import { expect, test, type Page, type Route, type TestInfo } from '@playwright/test'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const signedIn = {
  authenticated: true, auth_required: true, setup_required: false,
  oidc_enabled: false, password_enabled: true, csrf_token: 'audit-csrf',
  user: { id: 'audit-reader', email: 'audit@localhost.test', is_admin: false },
}
const signedOut = { ...signedIn, authenticated: false, csrf_token: '', user: undefined }
const repo = {
  name: 'github.com/example/audit', clone_url: '', orphaned: false,
  indexed_commit_hash: 'a'.repeat(40), connections: [],
}

async function fixture(page: Page) {
  const errors: string[] = []
  const unexpected: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (['error', 'warning'].includes(message.type())) errors.push(message.text()) })
  let authenticated = true
  let delayed: Route | undefined
  let holdStatus = false
  await page.route('**/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/auth/status' && holdStatus) { delayed = route; return }
    let value: unknown
    if (path === '/api/auth/status') value = authenticated ? signedIn : signedOut
    else if (path === '/api/version') value = { version: 'audit-fixture', capabilities: [] }
    else if (path === '/api/auth/keys') value = { keys: [] }
    else if (path === '/api/repo-status') value = [repo]
    else if (path === '/api/auth/logout' && request.method() === 'POST') {
      expect(request.headers()['x-csrf-token']).toBe('audit-csrf')
      authenticated = false
      await route.fulfill({ status: 204 })
      return
    } else { unexpected.push(path); value = { error: 'unexpected audit fixture request' } }
    await route.fulfill({ status: unexpected.includes(path) ? 501 : 200, json: value })
  })
  return {
    errors, unexpected,
    hold: () => { holdStatus = true },
    held: () => delayed !== undefined,
    release: async () => {
      holdStatus = false
      await delayed!.fulfill({ json: signedIn })
    },
    resume: () => { holdStatus = false },
  }
}

async function check(page: Page, f: Awaited<ReturnType<typeof fixture>>, image: string, info: TestInfo) {
  await expect(page.locator('vite-error-overlay')).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {})))
  })
  await page.addScriptTag({ path: require.resolve('axe-core/axe.min.js') })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (options: unknown) => Promise<{ violations: { id: string }[] }> } }).axe
    return (await axe.run({ runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations
  })
  if (violations.length) console.log(JSON.stringify(violations))
  expect(violations).toEqual([])
  expect(f.errors).toEqual([])
  expect(f.unexpected).toEqual([])
  await page.screenshot({ path: info.outputPath(image), animations: 'disabled' })
}

test.beforeEach(async ({ page }, info) => {
  const width = info.title.endsWith('390') ? 390 : 1280
  await page.setViewportSize({ width, height: 900 })
  await page.emulateMedia({ colorScheme: width === 390 ? 'dark' : 'light', reducedMotion: 'reduce' })
})

for (const width of [1280, 390]) {
  for (const operation of ['getItem', 'setItem'] as const) {
    test(`blocked ${operation} keeps Settings usable ${width}`, async ({ page }, info) => {
      const f = await fixture(page)
      await page.addInitScript(operation => {
        Storage.prototype[operation] = () => { throw new DOMException('Storage unavailable', 'SecurityError') }
      }, operation)
      await page.goto('/#/settings')
      await expect(page).toHaveTitle('phebs · Settings')
      const select = page.getByRole('combobox', { name: 'Code highlight palette' })
      await select.focus()
      await page.keyboard.press('q')
      await expect(select).toHaveValue('quiet')
      await expect(page.locator('pre[data-palette-ready="true"]')).toBeVisible()
      const theme = page.getByRole('button', { name: /Switch to .* theme/ })
      await theme.focus()
      await page.keyboard.press('Enter')
      await expect(theme).toHaveAttribute('aria-label', width === 390 ? 'Switch to dark theme' : 'Switch to light theme')
      await expect(page.locator('pre[data-palette-ready="true"]')).toBeVisible()
      await expect(page.getByText('No API keys.', { exact: true })).toBeVisible()
      await check(page, f, `storage-${operation}-${width}.png`, info)
    })
  }

  test(`late auth status cannot undo sign out ${width}`, async ({ page }, info) => {
    const f = await fixture(page)
    await page.goto('/#/settings')
    await expect(page.getByText('No API keys.', { exact: true })).toBeVisible()
    f.hold()
    await page.evaluate(() => window.dispatchEvent(new CustomEvent('phebs-auth-required')))
    await expect.poll(f.held).toBe(true)
    f.resume()
    await page.getByRole('button', { name: 'Sign out' }).focus()
    await page.keyboard.press('Enter')
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible()
    const response = page.waitForResponse(response => response.url().endsWith('/api/auth/status'))
    await f.release()
    await response
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Sign out' })).toHaveCount(0)
    await check(page, f, `signed-out-${width}.png`, info)
  })

  test(`commit copy waits and reports denied writes ${width}`, async ({ page }, info) => {
    const f = await fixture(page)
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: {
        writeText: () => new Promise<void>((resolve, reject) => {
          Object.assign(window, { finishAuditCopy: resolve, denyAuditCopy: () => reject(new DOMException('Denied', 'NotAllowedError')) })
        }),
      } })
    })
    await page.goto('/#/repos')
    await expect(page).toHaveTitle('phebs · Repositories')
    const copy = page.getByRole('button', { name: 'Copy commit', exact: true })
    await copy.focus()
    await page.keyboard.press('Enter')
    await expect(copy).toBeVisible()
    await expect(page.getByRole('button', { name: 'Commit copied' })).toHaveCount(0)
    const started = Date.now()
    await page.evaluate(() => (window as unknown as { finishAuditCopy: () => void }).finishAuditCopy())
    await expect(page.getByRole('button', { name: 'Commit copied' })).toBeVisible()
    console.log(JSON.stringify({ width, copySettlementMillis: Date.now() - started }))
    await page.getByRole('button', { name: 'Commit copied' }).press('Enter')
    await page.evaluate(() => (window as unknown as { denyAuditCopy: () => void }).denyAuditCopy())
    await expect(page.getByRole('button', { name: 'Copy commit failed' })).toBeVisible()
    await check(page, f, `copy-failed-${width}.png`, info)
  })
}
